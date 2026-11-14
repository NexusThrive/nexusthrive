package execution

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// PanicError is what Run returns when a handler panics. It is deliberately not
// a typed contract error: the contract has no internal code, and the value of
// the panic must never lend its own code to the answer. It has no Unwrap for
// the same reason. A gateway translates it to a generic answer without message
// or stack.
type PanicError struct {
	// Operation is the name of the operation that panicked.
	Operation string
	// Value is what the handler passed to panic.
	Value any
	// Stack is the stack at the point of the panic, for logs only.
	Stack []byte
}

// Error names the operation only. The value and the stack stay in their fields
// for logs: the value may hold sensitive data and must not reach a caller by
// accident.
func (e *PanicError) Error() string {
	return fmt.Sprintf("operation %s panicked", e.Operation)
}

// classify gives the error of a run its final shape, in this order: a typed
// error passes without wrapping; a panic error passes; an error of the context
// is wrapped without type; the restrict_violation of the database is
// permission_denied; the failure of a deferred check at commit is
// rule_violation; a missing privilege (42501) and a serialization failure or
// deadlock (40001, 40P01) are wrapped without type, so that a real fault is
// never disguised as a rule and nothing is retried, because the contract has
// no retryable code and running the handler again would repeat its effects;
// everything else is wrapped with the operation name.
func classify(operation string, err error) error {
	if err == nil {
		return nil
	}
	if _, typed := model.CodeOf(err); typed {
		return err
	}
	var panicked *PanicError
	if errors.As(err, &panicked) {
		return err
	}
	wrapped := fmt.Errorf("execute %s: %w", operation, err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return wrapped
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23001":
			return model.NewError(model.CodePermissionDenied, "the space may not hold this permission or perform this write", err)
		case "42501", "40001", "40P01":
			return wrapped
		}
	}
	if translated := state.TranslateCommitError(err); translated != err {
		return translated
	}
	return wrapped
}
