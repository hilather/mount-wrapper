package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultSSEInterval is how often SSE clients poll status and emit deltas.
const DefaultSSEInterval = 2 * time.Second

// DefaultHeartbeatInterval is the SSE comment heartbeat period.
const DefaultHeartbeatInterval = 15 * time.Second

// DefaultSSEFullSnapshotEvery is how many refresh ticks between full snapshot
// resync events (in addition to delta events on every change).
const DefaultSSEFullSnapshotEvery = 4

// Server is the localhost HTTP API + optional embedded SPA for the operator UI.
//
// Construct with New, then ListenAndServe (or Serve on an existing listener).
// Handlers are pure control-plane proxies via Backend plus offline doctor/wsl-info.
type Server struct {
	backend Backend
	version string
	bind    string
	token   string
	opts    ServerOptions

	// limitDestructive gates POST purge / unmount-all / rescan (per-client).
	limitDestructive *actionLimiter

	mu     sync.Mutex
	srv    *http.Server
	ln     net.Listener
	closed bool
}

// ServerOptions configures the HTTP API server.
type ServerOptions struct {
	// Bind is host:port (default from config or 127.0.0.1:8787).
	Bind string
	// Version is reported as web_version / health version.
	Version string
	// Token is the optional Bearer web_token (empty = no auth).
	Token string
	// SSEInterval is the SSE refresh period for status polling + delta emit.
	SSEInterval time.Duration
	// HeartbeatInterval is the SSE comment heartbeat period.
	HeartbeatInterval time.Duration
	// SSEFullSnapshotEvery is how many refresh ticks between full snapshot
	// resync events. Default DefaultSSEFullSnapshotEvery (4). <=0 uses default.
	SSEFullSnapshotEvery int
	// SSEIncludeSizes requests status with include_sizes so metrics_summary
	// can drive optional metrics delta events (heavier; off by default).
	SSEIncludeSizes bool
	// DestructiveMinInterval is the min gap between admits for destructive
	// POSTs (purge, unmount-all, rescan) per client IP. Zero → default 2s.
	// Negative disables the limiter (tests only).
	DestructiveMinInterval time.Duration
}

// Options is an alias for ServerOptions (call-site convenience).
type Options = ServerOptions

// New builds an API Server. backend is required.
func New(backend Backend, opts ServerOptions) *Server {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.SSEInterval <= 0 {
		opts.SSEInterval = DefaultSSEInterval
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if opts.SSEFullSnapshotEvery <= 0 {
		opts.SSEFullSnapshotEvery = DefaultSSEFullSnapshotEvery
	}
	bind := opts.Bind
	if bind == "" && backend != nil {
		if cfg := backend.Config(); cfg != nil {
			host := cfg.WebHost
			if host == "" {
				host = "127.0.0.1"
			}
			port := cfg.WebPort
			if port == 0 {
				port = 8787
			}
			bind = host + ":" + strconv.Itoa(port)
		}
	}
	if bind == "" {
		bind = "127.0.0.1:8787"
	}
	token := opts.Token
	if token == "" && backend != nil {
		if cfg := backend.Config(); cfg != nil {
			token = cfg.WebToken
		}
	}
	var limiter *actionLimiter
	if opts.DestructiveMinInterval >= 0 {
		gap := opts.DestructiveMinInterval
		if gap == 0 {
			gap = DefaultDestructiveMinInterval
		}
		limiter = newActionLimiter(gap)
	}
	return &Server{
		backend:          backend,
		version:          opts.Version,
		bind:             bind,
		token:            token,
		opts:             opts,
		limitDestructive: limiter,
	}
}

// Handler returns the root mux (API + SPA). Useful for httptest tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// API routes (auth wrapper).
	api := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, withAPIAuth(s.token, h))
	}
	api("/api/health", s.handleHealth)
	api("/api/status", s.handleStatus)
	api("/api/status/sizes", s.handleStatus)
	api("/api/archives", s.handleArchives)
	api("/api/metrics", s.handleMetrics)
	api("/api/config", s.handleConfig)
	api("/api/rescan", s.handleRescan)
	api("/api/unmount", s.handleUnmount)
	api("/api/retry", s.handleRetry)
	api("/api/purge", s.handlePurge)
	api("/api/hooks", s.handleHooks)
	api("/api/doctor", s.handleDoctor)
	api("/api/wsl-info", s.handleWSLInfo)
	api("/api/events", s.handleEvents)

	// Prometheus text exposition (not under /api/*). Auth: open on loopback
	// bind; Bearer/token required on non-loopback when web_token is set.
	mux.HandleFunc("/metrics", s.withMetricsAuth(s.handlePrometheus))

	// SPA / static assets at /
	spa, err := spaHandler(s.token)
	if err != nil {
		slog.Warn("spa handler unavailable", "err", err)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if stringsHasAPIPrefix(r.URL.Path) {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
				return
			}
			http.NotFound(w, r)
		})
	} else {
		mux.Handle("/", spa)
	}
	return mux
}

func stringsHasAPIPrefix(path string) bool {
	return len(path) >= 5 && path[:5] == "/api/"
}

// ListenAndServe binds and serves until Close.
func (s *Server) ListenAndServe() error {
	if s == nil {
		return fmt.Errorf("nil api server")
	}
	host, _, err := net.SplitHostPort(s.bind)
	if err != nil {
		// bind may be host without port in pathological cases
		host = s.bind
	}
	warnNonLoopback(host)
	ln, err := net.Listen("tcp", s.bind)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// warnNonLoopback logs if the bind host is not loopback (still allowed).
func warnNonLoopback(host string) {
	h := strings.TrimSpace(strings.ToLower(host))
	switch h {
	case "127.0.0.1", "localhost", "::1", "":
		return
	}
	slog.Warn("web_host is not loopback — dashboard will be reachable beyond this machine",
		"web_host", host)
}

// Serve serves on an existing listener until Close.
func (s *Server) Serve(ln net.Listener) error {
	if s == nil {
		return fmt.Errorf("nil api server")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	s.ln = ln
	s.srv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv := s.srv
	s.mu.Unlock()

	slog.Info("http api listening", "addr", ln.Addr().String())
	err := srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Close gracefully shuts down the HTTP server.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// Bind returns the configured bind address.
func (s *Server) Bind() string {
	if s == nil {
		return ""
	}
	return s.bind
}
