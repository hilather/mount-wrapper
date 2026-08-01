package service

import "fmt"

// Error is a fatal service startup / runtime error.
type Error struct {
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "service error"
	}
	return e.Message
}

func serviceErrorf(format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}
