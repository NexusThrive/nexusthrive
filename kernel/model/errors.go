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
	// CodePermissionDenied marks an actor that may not perform the operation.
	CodePermissionDenied Code = "permission_denied"
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
