// Package model holds the kernel's shared vocabulary: typed errors, actors and
// the kinds and statuses of spaces. It depends on nothing else in the kernel,
// so every other package can import it.
package model

import (
	"errors"
	"fmt"
)

// Code is the machine-readable class of a typed contract error.
type Code string

const (
	// CodeInvalidInput marks a request that is malformed or out of range.
	CodeInvalidInput Code = "invalid_input"
	// CodeRuleViolation marks a request that is well formed but breaks a rule,
	// such as a duplicated unique value.
	CodeRuleViolation Code = "rule_violation"
	// CodeNotFound marks a request for something that does not exist, or that
	// the current space cannot see.
	CodeNotFound Code = "not_found"
	// CodePermissionDenied marks an actor that may not perform the operation.
	CodePermissionDenied Code = "permission_denied"
	// CodeAlreadyDone marks a request whose effect already holds, such as
	// revoking a grant that is already revoked. Nothing changes.
	CodeAlreadyDone Code = "already_done"
	// CodeSpaceSuspended marks a write or money operation in a space that is
	// suspended or closed; reads still work.
	CodeSpaceSuspended Code = "space_suspended"
	// CodeCapabilityInactive marks an operation whose capability the space
	// does not have active.
	CodeCapabilityInactive Code = "capability_inactive"
	// CodeIdempotencyConflict marks a request that reuses the idempotency key of
	// another request still in force: the same key, but another operation, actor
	// or body (ADR-0026). Nothing is written.
	CodeIdempotencyConflict Code = "idempotency_conflict"
)

// Error is a typed contract error. Message is safe to show to callers; Err
// keeps the underlying cause for logs and errors.Is.
type Error struct {
	Code    Code
	Message string
	Err     error
}

// NewError builds an Error with an optional cause.
func NewError(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Err: cause}
}

// Error implements error as "code: message" plus the cause when present.
func (e *Error) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// CodeOf returns the code of the first Error in err's chain, or false when the
// chain has none.
func CodeOf(err error) (Code, bool) {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code, true
	}
	return "", false
}
