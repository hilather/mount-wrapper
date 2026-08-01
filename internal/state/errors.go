package state

import (
	"errors"
	"fmt"
)

// Sentinel bases for errors.Is checks.
var (
	ErrState      = errors.New("state error")
	ErrSchema     = errors.New("schema error")
	ErrTransition = errors.New("transition error")
	ErrNotFound   = errors.New("not found")
)

// StateError is the base error for state store operations.
type StateError struct {
	Msg string
	Err error // optional wrapped cause
}

func (e *StateError) Error() string {
	if e == nil {
		return "state error"
	}
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "state error"
}

func (e *StateError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Err != nil {
		return e.Err
	}
	return ErrState
}

func (e *StateError) Is(target error) bool {
	return target == ErrState
}

// SchemaError is a schema version / migration failure.
type SchemaError struct {
	Msg string
	Err error
}

func (e *SchemaError) Error() string {
	if e == nil {
		return "schema error"
	}
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "schema error"
}

func (e *SchemaError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Err != nil {
		return e.Err
	}
	return ErrSchema
}

func (e *SchemaError) Is(target error) bool {
	return target == ErrSchema || target == ErrState
}

// TransitionError is an illegal archive status transition or optimistic-lock miss.
type TransitionError struct {
	Msg string
	Err error
}

func (e *TransitionError) Error() string {
	if e == nil {
		return "transition error"
	}
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "transition error"
}

func (e *TransitionError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Err != nil {
		return e.Err
	}
	return ErrTransition
}

func (e *TransitionError) Is(target error) bool {
	return target == ErrTransition || target == ErrState
}

// NotFoundError is returned when an archive or hook row is not found.
type NotFoundError struct {
	Msg string
	Err error
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return "not found"
	}
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "not found"
}

func (e *NotFoundError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Err != nil {
		return e.Err
	}
	return ErrNotFound
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound || target == ErrState
}

func stateErrorf(format string, args ...any) *StateError {
	return &StateError{Msg: fmt.Sprintf(format, args...)}
}

func schemaErrorf(format string, args ...any) *SchemaError {
	return &SchemaError{Msg: fmt.Sprintf(format, args...)}
}

func transitionErrorf(format string, args ...any) *TransitionError {
	return &TransitionError{Msg: fmt.Sprintf(format, args...)}
}

func notFoundErrorf(format string, args ...any) *NotFoundError {
	return &NotFoundError{Msg: fmt.Sprintf(format, args...)}
}
