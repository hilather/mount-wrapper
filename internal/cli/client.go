package cli

import (
	"time"

	"github.com/hilather/mount-wrapper/internal/control"
)

// DefaultControlTimeout is the dial/read timeout for socket-backed CLI ops.
const DefaultControlTimeout = control.DefaultClientTimeout

// ControlError is a control-plane protocol or transport error (CLI-facing).
// Mirrors control.Error so CLI exit-code mapping stays local.
type ControlError struct {
	Message string
	Code    string
}

func (e *ControlError) Error() string {
	if e == nil {
		return "control error"
	}
	return e.Message
}

// ControlClient is a thin newline-JSON client for the control socket.
// Delegates framing/transport to internal/control.Client.
type ControlClient struct {
	SocketPath string
	Timeout    time.Duration
}

// newControlClient builds a ControlClient for the given socket path.
func newControlClient(socketPath string) *ControlClient {
	return &ControlClient{SocketPath: socketPath, Timeout: DefaultControlTimeout}
}

// Request sends {v, op, ...fields} and returns the full response map.
func (c *ControlClient) Request(op string, fields map[string]any) (map[string]any, error) {
	if c == nil || c.SocketPath == "" {
		return nil, &ControlError{Message: "control socket path is empty", Code: "UNAVAILABLE"}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultControlTimeout
	}
	inner := control.NewClient(c.SocketPath, timeout)
	resp, err := inner.Request(op, fields)
	if err != nil {
		return nil, mapControlErr(err)
	}
	return resp, nil
}

// RequestOK sends a request and returns data when ok is true.
func (c *ControlClient) RequestOK(op string, fields map[string]any) (any, error) {
	if c == nil || c.SocketPath == "" {
		return nil, &ControlError{Message: "control socket path is empty", Code: "UNAVAILABLE"}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultControlTimeout
	}
	inner := control.NewClient(c.SocketPath, timeout)
	data, err := inner.RequestOK(op, fields)
	if err != nil {
		return nil, mapControlErr(err)
	}
	return data, nil
}

func mapControlErr(err error) error {
	if err == nil {
		return nil
	}
	if ce, ok := err.(*control.Error); ok {
		return &ControlError{Message: ce.Message, Code: ce.Code}
	}
	return &ControlError{Message: err.Error(), Code: "ERROR"}
}
