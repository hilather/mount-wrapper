package archives

import "fmt"

// ArchiveRelocateError is returned when archive relocation or free-space
// checks fail. Message strings match tarmount-wsl where practical
// (e.g. "insufficient_space_for_relocate: …").
type ArchiveRelocateError struct {
	Msg string
}

func (e *ArchiveRelocateError) Error() string {
	if e == nil {
		return "archive relocate error"
	}
	return e.Msg
}

func relocateErrorf(format string, args ...any) *ArchiveRelocateError {
	return &ArchiveRelocateError{Msg: fmt.Sprintf(format, args...)}
}
