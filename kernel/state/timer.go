package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// maxFireTimers is the largest batch one firing round takes.
const maxFireTimers = 500

// timerColumns lists the columns scanTimer reads, in order, each prefixed with
// the alias of the table.
func timerColumns(alias string) string {
	return alias + `id::text, ` + alias + `space_id::text, ` + alias + `commitment_id::text, ` + alias + `fire_at, ` +
		alias + `status, ` + alias + `fired_at, ` + alias + `created_at`
}

// scanTimer reads one timer in the order of timerColumns and converts the
// times to UTC.
func scanTimer(row rowScanner) (model.Timer, error) {
	var out model.Timer
	var status string
	var firedAt sql.NullTime
	if err := row.Scan(&out.ID, &out.SpaceID, &out.CommitmentID, &out.FireAt, &status, &firedAt, &out.CreatedAt); err != nil {
		return model.Timer{}, err
	}
	out.Status = model.TimerStatus(status)
	out.FireAt, out.CreatedAt = out.FireAt.UTC(), out.CreatedAt.UTC()
	if firedAt.Valid {
		out.FiredAt = firedAt.Time.UTC()
	}
	return out, nil
}

// validateFire checks what a firing round needs before it writes anything: a
// clock, a limit between 1 and 500 and the system actor. The actor check is an
// additional defense: the permission kernel.timer.process is verified with
// Authorizer.Check by execution.Engine, because state does not import
// capability.
func validateFire(clock model.Clock, actor model.Actor, limit int) error {
	if clock == nil {
		return model.NewError(model.CodeInvalidInput, "a firing round needs a clock", nil)
	}
	if limit < 1 || limit > maxFireTimers {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("the limit of a firing round must be between 1 and %d", maxFireTimers), nil)
	}
	if actor.Kind != model.ActorSystem {
		return model.NewError(model.CodePermissionDenied, "only the system actor fires timers", nil)
	}
	if actor.ParticipantID != "" {
		return requireID("actor participant id", actor.ParticipantID)
	}
	return nil
}

// FireDueTimers runs one firing round for spaceID in a transaction of its own.
// It is for standalone use and tests and runs no permission check beyond the
// system actor; production goes through execution.Engine. See FireDueTimersTx
// for what a round does.
func FireDueTimers(ctx context.Context, pool *sql.DB, clock model.Clock, actor model.Actor, spaceID string, limit int) (model.TimerRun, error) {
	if err := validateFire(clock, actor, limit); err != nil {
		return model.TimerRun{}, err
	}
	var run model.TimerRun
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		run, err = FireDueTimersTx(ctx, tx, clock, actor, limit)
		return err
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return model.TimerRun{}, err
		}
		return model.TimerRun{}, fmt.Errorf("fire due timers: %w", err)
	}
	return run, nil
}

// FireDueTimersTx fires up to limit due timers of the current space in the
// caller's transaction, oldest fire time first, and returns what it did. A
// timer is due when its fire time is not after the injected clock and not after
// the clock of the database: the injected clock can run behind the database but
// never ahead of it, so a caller cannot expire a commitment early. Claiming and
// firing are one transaction, so there is no lease: it is firing, not claiming.
//
// The commitments of the candidates are locked first (FOR NO KEY UPDATE, SKIP
// LOCKED), the lock order of the whole kernel: a commitment that another
// transaction holds is skipped, never waited for, and a later round takes it.
// A timer whose commitment is no longer open is canceled and nothing else
// changes. A timer that fires expires its commitment, which closes with the
// time of the database, and the same transaction writes the
// kernel.commitment.expired event and its outbox row for the internal
// subscriber, so either all of it happens or none.
//
// It never begins, commits or rolls back. A clock, a limit or an actor that is
// not valid is refused before any write and leaves the transaction usable; a
// database error leaves it aborted and the caller must roll it back.
func FireDueTimersTx(ctx context.Context, tx *sql.Tx, clock model.Clock, actor model.Actor, limit int) (model.TimerRun, error) {
	if err := validateFire(clock, actor, limit); err != nil {
		return model.TimerRun{}, err
	}
	if err := requireCurrentSpace(ctx, tx, "fire timers in"); err != nil {
		return model.TimerRun{}, err
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT t.id::text
		 FROM kernel.timers t
		 JOIN kernel.commitments c ON c.id = t.commitment_id AND c.space_id = t.space_id
		 WHERE t.space_id = kernel.current_space_id() AND t.status = 'scheduled'
		   AND t.fire_at <= LEAST($1::timestamptz, now())
		 ORDER BY t.fire_at, t.id
		 LIMIT $2
		 FOR NO KEY UPDATE OF c SKIP LOCKED`,
		outboxTime(clock), limit)
	if err != nil {
		return model.TimerRun{}, fmt.Errorf("select due timers: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return model.TimerRun{}, fmt.Errorf("scan due timer: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return model.TimerRun{}, fmt.Errorf("select due timers: %w", err)
	}
	rows.Close()
	run := model.TimerRun{}
	if len(ids) == 0 {
		return run, nil
	}

	// A timer of a commitment that closed without it is canceled and does not
	// expire anything. Each statement takes a fresh snapshot.
	if run.Canceled, err = queryTimers(ctx, tx,
		`UPDATE kernel.timers t SET status = 'canceled'
		 FROM kernel.commitments c
		 WHERE t.id = ANY($1::text[]::uuid[]) AND t.status = 'scheduled'
		   AND c.id = t.commitment_id AND c.space_id = t.space_id AND c.status <> 'open'
		 RETURNING `+timerColumns("t."), ids); err != nil {
		return model.TimerRun{}, fmt.Errorf("cancel timers of closed commitments: %w", err)
	}
	// Whoever arrives late finds the timer already fired or canceled, or moved
	// to a later time by a reschedule that committed after the candidates were
	// read, and gets no row: the same conditions as the scan are repeated on the
	// current version of the row, which the guard of the database requires too.
	if run.Fired, err = queryTimers(ctx, tx,
		`UPDATE kernel.timers SET status = 'fired'
		 WHERE id = ANY($1::text[]::uuid[]) AND status = 'scheduled' AND fire_at <= LEAST($2::timestamptz, now())
		 RETURNING `+timerColumns(""), ids, outboxTime(clock)); err != nil {
		return model.TimerRun{}, fmt.Errorf("fire timers: %w", err)
	}
	sortTimers(run.Canceled)
	sortTimers(run.Fired)

	for _, timer := range run.Fired {
		expired, err := readCommitment(ctx, tx, timer.CommitmentID)
		if err != nil {
			return model.TimerRun{}, fmt.Errorf("read expired commitment: %w", err)
		}
		event := model.NewCommitmentExpiredEvent(expired, timer.ID)
		event.Actor = actor
		if _, err := AppendEventAndEnqueue(ctx, tx, clock, event, model.SubscriberInternal); err != nil {
			return model.TimerRun{}, fmt.Errorf("record the expiry of %s: %w", timer.CommitmentID, err)
		}
	}
	return run, nil
}

// queryTimers runs a statement that returns timers and reads them all.
func queryTimers(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]model.Timer, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Timer
	for rows.Next() {
		timer, err := scanTimer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan timer: %w", err)
		}
		out = append(out, timer)
	}
	return out, rows.Err()
}

// sortTimers orders timers by fire time and then id, the order of a round.
func sortTimers(timers []model.Timer) {
	slices.SortFunc(timers, func(a, b model.Timer) int {
		if c := a.FireAt.Compare(b.FireAt); c != 0 {
			return c
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
}

// GetTimer returns the timer of a commitment of spaceID. A commitment that does
// not exist, or belongs to another space, is not_found.
func GetTimer(ctx context.Context, pool *sql.DB, spaceID, commitmentID string) (model.Timer, error) {
	if err := requireID("commitment id", commitmentID); err != nil {
		return model.Timer{}, err
	}
	var out model.Timer
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		out, err = scanTimer(tx.QueryRowContext(ctx,
			`SELECT `+timerColumns("")+` FROM kernel.timers WHERE commitment_id = $1`, commitmentID))
		if errors.Is(err, sql.ErrNoRows) {
			return model.NewError(model.CodeNotFound, "timer not found in this space", nil)
		}
		return err
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return model.Timer{}, err
		}
		return model.Timer{}, fmt.Errorf("get timer: %w", err)
	}
	return out, nil
}

// AppendRescheduleCommitment moves the due date of an open commitment in the
// caller's transaction and returns it; the database moves its timer with it. A
// zero date is invalid_input and a date that is not in the future of the clock
// of the database is rule_violation: moving it into the past would let a writer
// force an expiry. A commitment that does not exist in this space is not_found
// and one that is no longer open is already_done; the update waits for a
// concurrent change of the same commitment and then decides on the committed
// state. Everything else follows AppendCommitment.
func AppendRescheduleCommitment(ctx context.Context, tx *sql.Tx, actor model.Actor, commitmentID string, dueAt time.Time) (model.Commitment, error) {
	if err := validateWriteActor(actor); err != nil {
		return model.Commitment{}, err
	}
	if err := requireID("commitment id", commitmentID); err != nil {
		return model.Commitment{}, err
	}
	if err := model.ValidateDueAt(dueAt); err != nil {
		return model.Commitment{}, err
	}
	if err := requireCurrentSpace(ctx, tx, "write commitments in"); err != nil {
		return model.Commitment{}, err
	}
	got, err := scanCommitment(tx.QueryRowContext(ctx,
		`UPDATE kernel.commitments SET due_at = $2 WHERE id = $1 AND status = 'open' RETURNING `+commitmentColumns,
		commitmentID, dueAt))
	if err == nil {
		return got, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.Commitment{}, translateCommitmentError("reschedule commitment", err)
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM kernel.commitments WHERE id = $1`, commitmentID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Commitment{}, model.NewError(model.CodeNotFound, "commitment not found in this space", nil)
	}
	if err != nil {
		return model.Commitment{}, fmt.Errorf("read commitment status: %w", err)
	}
	return model.Commitment{}, model.NewError(model.CodeAlreadyDone, "the commitment is already "+status, nil)
}

// RescheduleCommitment runs AppendRescheduleCommitment for spaceID in a
// transaction of its own.
func RescheduleCommitment(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, commitmentID string, dueAt time.Time) (model.Commitment, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Commitment{}, err
	}
	if err := model.ValidateDueAt(dueAt); err != nil {
		return model.Commitment{}, err
	}
	var stored model.Commitment
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stored, err = AppendRescheduleCommitment(ctx, tx, actor, commitmentID, dueAt)
		return err
	})
	if err != nil {
		return model.Commitment{}, translateCommitmentError("reschedule commitment", err)
	}
	return stored, nil
}
