package control

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// DefaultClientTimeout is the default dial/read timeout for Client.
const DefaultClientTimeout = 30 * time.Second

// Client is a thin JSON-lines client for the control socket.
type Client struct {
	// SocketPath is the Unix socket filesystem path.
	SocketPath string
	// Timeout is the dial and I/O timeout (default DefaultClientTimeout).
	Timeout time.Duration
}

// NewClient builds a Client for the given socket path.
func NewClient(socketPath string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultClientTimeout
	}
	return &Client{SocketPath: socketPath, Timeout: timeout}
}

// Request sends one JSON-lines request and returns the decoded response map.
// Transport failures use code UNAVAILABLE; invalid response JSON uses BAD_RESPONSE.
func (c *Client) Request(op string, fields map[string]any) (map[string]any, error) {
	if c == nil {
		return nil, NewError("nil control client", "INTERNAL")
	}
	path := c.SocketPath
	if path == "" {
		return nil, NewError("control socket path is empty", "UNAVAILABLE")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultClientTimeout
	}

	payload, err := EncodeRequest(op, fields)
	if err != nil {
		return nil, NewError("encode request: "+err.Error(), "ERROR")
	}

	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return nil, NewError(fmt.Sprintf("cannot connect to control socket %s: %v", path, err), "UNAVAILABLE")
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(payload); err != nil {
		return nil, NewError(fmt.Sprintf("cannot connect to control socket %s: %v", path, err), "UNAVAILABLE")
	}
	// Half-close write side so a server that waits for EOF can finish; we still
	// read one line. Most servers close after one response regardless.
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}

	line, err := readOneLine(conn, MaxRequestBytes)
	if err != nil {
		if err == io.EOF {
			return nil, NewError("empty response from service", "UNAVAILABLE")
		}
		return nil, NewError(fmt.Sprintf("cannot connect to control socket %s: %v", path, err), "UNAVAILABLE")
	}
	if line == "" {
		return nil, NewError("empty response from service", "UNAVAILABLE")
	}

	var resp any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, NewError(fmt.Sprintf("invalid response JSON: %v", err), "BAD_RESPONSE")
	}
	m, ok := resp.(map[string]any)
	if !ok {
		return nil, NewError("response must be an object", "BAD_RESPONSE")
	}
	return m, nil
}

// RequestOK sends a request and returns data when ok is true.
// When ok is false, returns a control Error with the server code.
func (c *Client) RequestOK(op string, fields map[string]any) (any, error) {
	resp, err := c.Request(op, fields)
	if err != nil {
		return nil, err
	}
	if ok, _ := resp["ok"].(bool); !ok {
		msg, _ := resp["error"].(string)
		if msg == "" {
			msg = "request failed"
		}
		code, _ := resp["code"].(string)
		if code == "" {
			code = "ERROR"
		}
		return nil, NewError(msg, code)
	}
	return resp["data"], nil
}

func readOneLine(r io.Reader, maxBytes int) (string, error) {
	br := bufio.NewReader(r)
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > maxBytes {
			return "", NewError("response too large", "BAD_RESPONSE")
		}
		buf = append(buf, chunk...)
		if err == nil {
			// Full line including newline.
			line := string(buf)
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return line, nil
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
		return "", err
	}
}
