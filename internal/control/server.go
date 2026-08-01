package control

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// MaxRequestBytes is the maximum accepted control request size (1 MiB).
const MaxRequestBytes = 1_000_000

// DefaultConnTimeout is the per-connection read/write timeout.
const DefaultConnTimeout = 30 * time.Second

// Handler dispatches a parsed control request and returns a response map.
// Typically service.Service.HandleRequest.
type Handler func(req map[string]any) map[string]any

// Server accepts Unix-socket connections and dispatches JSON-lines requests.
//
// Parity with Python ControlServer: non-blocking accept via ServeReady (service
// tick polls), one request line per connection, then close.
type Server struct {
	// Path is the Unix socket filesystem path.
	Path string
	// Handler is required; called for authorized requests.
	Handler Handler
	// AllowAllAuth skips peer credential checks (tests / --allow-unauth).
	AllowAllAuth bool
	// GroupName is the authorized Unix group (default DefaultAuthGroup).
	GroupName string
	// Owner is the optional best-effort chown user (default DefaultServiceUser).
	Owner string
	// OwnerGroup is the optional best-effort chown group (default GroupName).
	OwnerGroup string
	// Backlog is reserved for parity; net.ListenUnix uses the default backlog.
	Backlog int
	// ConnTimeout is the per-connection deadline (default DefaultConnTimeout).
	ConnTimeout time.Duration

	// PeerCredentials injectable for tests (default platform.PeerCredentials).
	PeerCredentials PeerCredentialsFunc
	// UserInGroup injectable for tests (default UserInGroup).
	UserInGroup UserInGroupFunc
	// AllowUnauthEnv overrides env escape hatch when non-nil (tests).
	AllowUnauthEnv *bool

	mu   sync.Mutex
	ln   *net.UnixListener
	path string
}

// NewServer builds a Server with the given path and handler.
func NewServer(path string, handler Handler, allowAllAuth bool) *Server {
	return &Server{
		Path:         path,
		Handler:      handler,
		AllowAllAuth: allowAllAuth,
	}
}

// Active reports whether the server is listening.
func (s *Server) Active() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ln != nil
}

// Start binds the Unix socket, removes a stale path, chmod 0660, optional chown.
func (s *Server) Start() error {
	if s == nil {
		return NewError("nil control server", "INTERNAL")
	}
	if s.Handler == nil {
		return NewError("control handler is nil", "INTERNAL")
	}
	path := s.Path
	if path == "" {
		return NewError("control socket path is empty", "INTERNAL")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return NewError("mkdir control socket dir: "+err.Error(), "IO_ERROR")
	}
	// Remove stale socket (parity with Python).
	if _, err := os.Lstat(path); err == nil {
		_ = os.Remove(path)
	}

	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return NewError("resolve control socket: "+err.Error(), "IO_ERROR")
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return NewError("bind control socket: "+err.Error(), "IO_ERROR")
	}

	if err := os.Chmod(path, 0o660); err != nil {
		slog.Warn("chmod control socket failed", "path", path, "err", err)
	}
	s.bestEffortChown(path)

	s.mu.Lock()
	s.ln = ln
	s.path = path
	s.mu.Unlock()
	slog.Info("control socket listening", "path", path)
	return nil
}

func (s *Server) bestEffortChown(path string) {
	owner := s.Owner
	if owner == "" {
		owner = DefaultServiceUser
	}
	group := s.OwnerGroup
	if group == "" {
		group = s.GroupName
	}
	if group == "" {
		group = DefaultAuthGroup
	}

	uid, uidOK := nameToUID(owner)
	gid, gidOK := nameToGID(group)
	if !uidOK && !gidOK {
		return
	}
	useUID, useGID := -1, -1
	if uidOK {
		useUID = uid
	}
	if gidOK {
		useGID = gid
	}
	if err := os.Chown(path, useUID, useGID); err != nil {
		slog.Debug("chown control socket skipped", "path", path, "err", err)
	}
}

// Close stops the listener and removes the socket path.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	ln := s.ln
	path := s.path
	s.ln = nil
	s.mu.Unlock()

	var first error
	if ln != nil {
		if err := ln.Close(); err != nil {
			first = err
		}
	}
	if path != "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// ServeReady accepts and handles all currently pending connections.
// Uses a short accept deadline so the service tick can poll without blocking.
func (s *Server) ServeReady() {
	if s == nil {
		return
	}
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()
	if ln == nil {
		return
	}

	for {
		// Short deadline ≈ non-blocking accept (Python setblocking(False)).
		_ = ln.SetDeadline(time.Now().Add(2 * time.Millisecond))
		conn, err := ln.AcceptUnix()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return
			}
			// Listener closed or other error — stop polling this tick.
			return
		}
		s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn *net.UnixConn) {
	defer func() {
		_ = conn.Close()
	}()

	timeout := s.ConnTimeout
	if timeout <= 0 {
		timeout = DefaultConnTimeout
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// Read the request line before auth so a client that dials then writes is not
	// racing a fast deny+close (broken pipe on write). Peer credentials are still
	// available on the accepted connection without consuming the payload first.
	line, err := readRequestLine(conn, MaxRequestBytes)
	if err != nil {
		if err == io.EOF || isTimeout(err) {
			return
		}
		if ce, ok := err.(*Error); ok {
			_ = writeResponse(conn, ErrResponse(ce.Message, ce.Code))
			return
		}
		slog.Warn("control connection error", "err", err)
		return
	}
	if line == "" {
		return
	}

	auth := AuthorizePeer(conn, AuthorizeOpts{
		AllowAll:        s.AllowAllAuth,
		GroupName:       s.GroupName,
		PeerCredentials: s.PeerCredentials,
		UserInGroup:     s.UserInGroup,
		AllowUnauthEnv:  s.AllowUnauthEnv,
	})
	if !auth.Allowed {
		slog.Warn("control auth denied", "reason", auth.Reason)
		group := s.GroupName
		if group == "" {
			group = DefaultAuthGroup
		}
		_ = writeResponse(conn, ErrResponse(
			"permission denied: user must be root or in group "+group+" ("+auth.Reason+")",
			"PERMISSION_DENIED",
		))
		return
	}

	req, err := ParseRequest(line)
	if err != nil {
		if ce, ok := err.(*Error); ok {
			_ = writeResponse(conn, ErrResponse(ce.Message, ce.Code))
			return
		}
		_ = writeResponse(conn, ErrResponse(err.Error(), "BAD_REQUEST"))
		return
	}

	slog.Debug("control op", "op", req["op"], "auth", auth.Reason)

	var resp map[string]any
	func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("control handler panic", "op", req["op"], "panic", r)
				resp = ErrResponse(fmt.Sprintf("handler panic: %v", r), "INTERNAL")
			}
		}()
		resp = s.Handler(req)
	}()
	if resp == nil {
		resp = OKResponse(nil)
	}
	// If handler returned a bare data object without ok, wrap it.
	if _, hasOK := resp["ok"]; !hasOK {
		resp = OKResponse(resp)
	}
	if err := writeResponse(conn, resp); err != nil {
		slog.Warn("control write response failed", "err", err)
	}
}

func readRequestLine(r io.Reader, maxBytes int) (string, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			if len(buf)+len(chunk) > maxBytes {
				return "", NewError("request too large", "BAD_REQUEST")
			}
			buf = append(buf, chunk...)
			if chunk[len(chunk)-1] == '\n' {
				line := string(buf[:len(buf)-1])
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				return line, nil
			}
		}
		if err == io.EOF {
			if len(buf) == 0 {
				return "", io.EOF
			}
			return string(buf), nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return "", err
		}
	}
}

func writeResponse(w io.Writer, resp map[string]any) error {
	raw, err := EncodeResponse(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func isTimeout(err error) bool {
	if ne, ok := err.(net.Error); ok {
		return ne.Timeout()
	}
	return false
}

func nameToUID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, false
	}
	id, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, false
	}
	return id, true
}

func nameToGID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	id, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, false
	}
	return id, true
}
