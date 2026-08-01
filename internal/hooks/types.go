package hooks

import (
	"fmt"
	"time"
)

// ExitRetry is EX_TEMPFAIL — soft / retryable failure (design + sysexits.h).
const ExitRetry = 75

// EnvPrefix is the only environment variable prefix hooks receive for
// mount-wrapper fields (D3: no TARMOUNT_* dual export).
const EnvPrefix = "MOUNT_WRAPPER_"

// DiscoveredHook is one executable hook script candidate (pre-security).
type DiscoveredHook struct {
	Name string
	Path string
}

// RunResult is the outcome of running a single hook.
type RunResult struct {
	HookName string
	Status   string // success | failed | retry | skipped
	ExitCode *int
	Attempts int
	Error    string
	TimedOut bool
	Duration time.Duration
}

// CycleResult is the aggregate outcome of a first-mount hook cycle for one archive.
type CycleResult struct {
	ArchiveID     string
	Ran           bool
	HooksStatus   string
	Results       []RunResult
	SkippedReason string
}

// Error is a hooks package error (discovery, execution, missing archive).
type Error struct {
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "hooks error"
	}
	return e.Message
}

func hookErrorf(format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// SecurityError is returned when a hooks.d path fails security policy.
type SecurityError struct {
	Message string
}

func (e *SecurityError) Error() string {
	if e == nil {
		return "hook security error"
	}
	return e.Message
}

func securityErrorf(format string, args ...any) *SecurityError {
	return &SecurityError{Message: fmt.Sprintf(format, args...)}
}
