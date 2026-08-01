package mounter

import "fmt"

// Error is a mount / unmount / resolve failure.
type Error struct {
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "mounter error"
	}
	return e.Message
}

func mounterErrorf(format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}
