package model_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

func TestCodeOfFindsWrappedError(t *testing.T) {
	t.Parallel()
	base := &model.Error{Code: model.CodeRuleViolation, Message: "slug taken"}
	wrapped := fmt.Errorf("create space: %w", base)

	if code, ok := model.CodeOf(wrapped); !ok || code != model.CodeRuleViolation {
		t.Errorf("CodeOf = %q, %v", code, ok)
	}
	if _, ok := model.CodeOf(errors.New("plain")); ok {
		t.Error("a plain error has no code")
	}
	if _, ok := model.CodeOf(nil); ok {
		t.Error("nil has no code")
	}
}

func TestErrorMessageAndUnwrap(t *testing.T) {
	t.Parallel()
	cause := errors.New("cause")
	err := model.NewError(model.CodeInvalidInput, "bad slug", cause)
	if err.Error() != "invalid_input: bad slug: cause" {
		t.Errorf("Error() = %q", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Error("Unwrap must expose the cause")
	}
	if got := model.NewError(model.CodePermissionDenied, "no", nil).Error(); got != "permission_denied: no" {
		t.Errorf("Error() = %q", got)
	}
}

// TestAlreadyDoneCodeValue pins the wire value of the already_done code.
func TestAlreadyDoneCodeValue(t *testing.T) {
	t.Parallel()
	if model.CodeAlreadyDone != "already_done" {
		t.Errorf("CodeAlreadyDone = %q", model.CodeAlreadyDone)
	}
}

// TestIdempotencyConflictCodeValue pins the wire value of the
// idempotency_conflict code (ADR-0004 amendment, ADR-0026).
func TestIdempotencyConflictCodeValue(t *testing.T) {
	t.Parallel()
	if model.CodeIdempotencyConflict != "idempotency_conflict" {
		t.Errorf("CodeIdempotencyConflict = %q", model.CodeIdempotencyConflict)
	}
}
