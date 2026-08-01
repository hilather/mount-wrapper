package convert

import "fmt"

// Error is a convert-pipeline error with optional path context.
type Error struct {
	Op   string
	Path string
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return "convert: <nil>"
	}
	if e.Path != "" {
		return fmt.Sprintf("convert %s %s: %s", e.Op, e.Path, e.Msg)
	}
	return fmt.Sprintf("convert %s: %s", e.Op, e.Msg)
}

func convertErrorf(op, msg string, args ...any) error {
	return &Error{Op: op, Msg: fmt.Sprintf(msg, args...)}
}
