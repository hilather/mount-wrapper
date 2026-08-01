package paths

import "fmt"

// PathMapError is returned when a configured path cannot be mapped to a
// WSL/Linux path (empty input, rejected WSL UNC, relative/UNC without wslpath, etc.).
type PathMapError struct {
	Message string
}

func (e *PathMapError) Error() string {
	if e == nil {
		return "path map error"
	}
	return e.Message
}

func pathMapError(msg string) *PathMapError {
	return &PathMapError{Message: msg}
}

func pathMapErrorf(format string, args ...any) *PathMapError {
	return &PathMapError{Message: fmt.Sprintf(format, args...)}
}
