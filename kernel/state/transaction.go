// Package state is the kernel's access to persistent state. Every read and
// write of space data goes through InSpace, so row level security always knows
// which space it is serving (ADR-0027).
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// SpaceSetting is the session parameter that row level security policies read
// through kernel.current_space_id().
const SpaceSetting = "kernel.space_id"

// InSpace runs fn in a transaction whose current space is spaceID and commits
// when fn returns nil. An error or a panic rolls back; the panic is re-raised.
//
// An empty, malformed or nil spaceID is rejected as invalid_input before any
// transaction is opened. The setting is fixed with set_config(..., true), the
// parametrized form of SET LOCAL, so it ends with the transaction and cannot
// leak to the next user of a pooled connection.
func InSpace(ctx context.Context, pool *sql.DB, spaceID string, fn func(ctx context.Context, tx *sql.Tx) error) (err error) {
	parsed, parseErr := uuid.Parse(spaceID)
	if spaceID == "" || parseErr != nil || parsed == uuid.Nil {
		return model.NewError(model.CodeInvalidInput, "space id must be a non-nil UUID", nil)
	}

	return inTransaction(ctx, pool, parsed.String(), fn)
}

// inTransaction runs fn in a transaction and commits when fn returns nil. A
// non-empty spaceID is fixed as the current space first; an empty one runs
// without a space, for the few operations that precede any space data, such as
// creating an identity. It is not exported so that every access to space data
// keeps going through InSpace.
func inTransaction(ctx context.Context, pool *sql.DB, spaceID string, fn func(ctx context.Context, tx *sql.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// Rollback also runs while a panic unwinds; the panic then continues.
		// Its own error is ignored because the transaction is already failed.
		_ = tx.Rollback()
	}()

	if spaceID != "" {
		if _, err := tx.ExecContext(ctx, "SELECT set_config($1, $2, true)", SpaceSetting, spaceID); err != nil {
			return fmt.Errorf("set current space: %w", err)
		}
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// deferredConstraints are the checks the database postpones to the COMMIT. A
// failure of one of them is a rule the operation broke, not a fault of the
// system.
var deferredConstraints = map[string]struct{}{
	"exchanges_conservation":    {},
	"exchanges_min_entries":     {},
	"exchanges_reversal_mirror": {},
	"commitments_fulfilled_sum": {},
	"timers_coherent":           {},
}

// TranslateCommitError turns the failure of a deferred check at COMMIT into a
// rule_violation that keeps the cause. Any other error, a missing privilege
// (42501) included, and any error that is already typed is returned as it came,
// so that a real fault is never disguised as a rule.
func TranslateCommitError(err error) error {
	if err == nil {
		return nil
	}
	if _, typed := model.CodeOf(err); typed {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if _, deferred := deferredConstraints[pgErr.ConstraintName]; deferred {
			return model.NewError(model.CodeRuleViolation, "the operation breaks a rule that is checked when it commits", err)
		}
	}
	return err
}
