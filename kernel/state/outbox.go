package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

const (
	// maxClaimOutbox is the largest batch ClaimOutbox returns.
	maxClaimOutbox = 1000
	// maxExpireOutbox is the most lapsed claims one claim call fails.
	maxExpireOutbox = 1000
	// maxOutboxCause is the longest failure cause kept, in characters.
	maxOutboxCause = 1000
	// claimExpiredCause is the error recorded when a lease lapses.
	claimExpiredCause = "claim expired"
)

// outboxColumns lists the columns scanOutboxRow reads, in order.
const outboxColumns = `event_seq, space_id::text, subscriber, status, attempts, next_attempt_at,
	last_error, delivered_at, claim_token::text, claimed_until`

// outboxTime reads the clock in UTC and cuts it to microseconds, the precision
// of the database, so that comparisons in SQL and in Go agree exactly.
func outboxTime(clock model.Clock) time.Time {
	return clock.Now().UTC().Truncate(time.Microsecond)
}

// backoffMicros returns the retry wait, in microseconds, after 1 to
// OutboxMaxAttempts-1 failed attempts. SQL indexes it by attempts, from 1.
func backoffMicros() []int64 {
	waits := make([]int64, 0, model.OutboxMaxAttempts-1)
	for attempts := 1; attempts < model.OutboxMaxAttempts; attempts++ {
		waits = append(waits, model.OutboxBackoff(attempts).Microseconds())
	}
	return waits
}

// requireCurrentSpace fails with rule_violation when the transaction has no
// current space. The check is explicit so that a privilege failure is never
// mistaken for a missing space.
func requireCurrentSpace(ctx context.Context, tx *sql.Tx, what string) error {
	var space sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT kernel.current_space_id()::text`).Scan(&space); err != nil {
		return fmt.Errorf("read current space: %w", err)
	}
	if !space.Valid {
		return model.NewError(model.CodeRuleViolation, "the transaction has no current space to "+what, nil)
	}
	return nil
}

// EnqueueOutbox writes the pending outbox row of one event for one subscriber
// in the caller's transaction. The row is due at the injected clock, and its
// status, attempts and creation time are the defaults of the table, never
// values from the caller. The space is the current space of the transaction.
//
// It never begins, commits or rolls back. An invalid_input or rule_violation
// error found before the write leaves the transaction usable; a database error
// leaves it aborted, and the caller must roll it back. An event that does not
// exist and an event of another space get the same rule_violation, so the
// answer does not reveal which events exist.
func EnqueueOutbox(ctx context.Context, tx *sql.Tx, clock model.Clock, eventSeq int64, s model.Subscriber) error {
	if eventSeq < 1 {
		return model.NewError(model.CodeInvalidInput, "event seq must be at least 1", nil)
	}
	if !s.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown outbox subscriber %q", string(s)), nil)
	}
	if err := requireCurrentSpace(ctx, tx, "write outbox rows in"); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
		 VALUES ($1, kernel.current_space_id(), $2, $3)`,
		eventSeq, string(s), outboxTime(clock))
	if err != nil {
		return translateOutboxError(err)
	}
	return nil
}

// AppendEventAndEnqueue writes one event with AppendEvent and one pending
// outbox row for each subscriber, all in the caller's transaction, so they live
// or die together. The subscribers are validated before anything is written:
// an unknown or repeated one is invalid_input and leaves the transaction
// usable. With no subscribers it is AppendEvent alone.
func AppendEventAndEnqueue(ctx context.Context, tx *sql.Tx, clock model.Clock, in model.NewEvent, subs ...model.Subscriber) (model.Event, error) {
	seen := map[model.Subscriber]bool{}
	for _, s := range subs {
		if !s.Valid() {
			return model.Event{}, model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown outbox subscriber %q", string(s)), nil)
		}
		if seen[s] {
			return model.Event{}, model.NewError(model.CodeInvalidInput, fmt.Sprintf("outbox subscriber %q is repeated", string(s)), nil)
		}
		seen[s] = true
	}
	event, err := AppendEvent(ctx, tx, in)
	if err != nil {
		return model.Event{}, err
	}
	for _, s := range subs {
		if err := EnqueueOutbox(ctx, tx, clock, event.Seq, s); err != nil {
			return model.Event{}, err
		}
	}
	return event, nil
}

// ClaimOutbox claims up to limit due rows of one subscriber in spaceID, in a
// transaction of its own. It is for standalone use and tests and runs no
// permission check; production goes through execution.Engine. See ClaimOutboxTx
// for what a claim does.
func ClaimOutbox(ctx context.Context, pool *sql.DB, clock model.Clock, spaceID string, s model.Subscriber, limit int) ([]model.OutboxRow, error) {
	if err := validateOutboxClaim(s, limit); err != nil {
		return nil, err
	}
	var rows []model.OutboxRow
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		rows, err = ClaimOutboxTx(ctx, tx, clock, s, limit)
		return err
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return nil, err
		}
		return nil, fmt.Errorf("claim outbox: %w", err)
	}
	return rows, nil
}

// ClaimOutboxTx claims up to limit due rows of one subscriber in the current
// space of the caller's transaction, and returns them oldest due first. A row
// is due when it is pending or failed, free of any claim, and its
// next_attempt_at is not after the clock.
//
// The call first fails the claims whose lease has lapsed (up to 1000): each
// counts as a failed attempt with the error "claim expired", waits as any
// failure does and becomes dead at the fifth. It then claims the due rows with
// FOR UPDATE SKIP LOCKED, so concurrent claimers neither wait for each other
// nor take the same row, and marks them with one new token and a lease of
// model.OutboxLease. The lock ends with the transaction; the lease is what keeps
// a second claimer away from a committed claim, and delivery is at least once
// (ADR-0031). A transaction runs in one space only.
func ClaimOutboxTx(ctx context.Context, tx *sql.Tx, clock model.Clock, s model.Subscriber, limit int) ([]model.OutboxRow, error) {
	if err := validateOutboxClaim(s, limit); err != nil {
		return nil, err
	}
	if err := requireCurrentSpace(ctx, tx, "claim outbox rows in"); err != nil {
		return nil, err
	}
	now := outboxTime(clock)

	// Failing a lapsed claim is the same step as a failed mark: one more
	// attempt, the wait of the table, and dead at the limit. GREATEST keeps the
	// schedule from moving back, as the trigger requires.
	if _, err := tx.ExecContext(ctx,
		`UPDATE kernel.outbox o SET
		   attempts = o.attempts + 1,
		   status = CASE WHEN o.attempts + 1 >= $1 THEN 'dead' ELSE 'failed' END,
		   last_error = $2,
		   next_attempt_at = CASE WHEN o.attempts + 1 >= $1 THEN o.next_attempt_at
		     ELSE GREATEST(o.next_attempt_at, $3::timestamptz + ($4::bigint[])[o.attempts + 1] * interval '1 microsecond') END,
		   claim_token = NULL, claimed_until = NULL
		 FROM (SELECT event_seq, subscriber FROM kernel.outbox
		       WHERE subscriber = $5 AND claim_token IS NOT NULL AND claimed_until <= $3
		       ORDER BY claimed_until, event_seq LIMIT $6 FOR UPDATE SKIP LOCKED) lapsed
		 WHERE o.event_seq = lapsed.event_seq AND o.subscriber = lapsed.subscriber`,
		model.OutboxMaxAttempts, claimExpiredCause, now, backoffMicros(), string(s), maxExpireOutbox); err != nil {
		return nil, fmt.Errorf("expire lapsed claims: %w", err)
	}

	token, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate claim token: %w", err)
	}
	result, err := tx.QueryContext(ctx,
		`WITH due AS (
		   SELECT event_seq, subscriber FROM kernel.outbox
		   WHERE subscriber = $1 AND status IN ('pending', 'failed') AND claim_token IS NULL AND next_attempt_at <= $2
		   ORDER BY next_attempt_at, event_seq LIMIT $3 FOR UPDATE SKIP LOCKED)
		 UPDATE kernel.outbox o SET claim_token = $4::uuid, claimed_until = $5
		 FROM due WHERE o.event_seq = due.event_seq AND o.subscriber = due.subscriber
		 RETURNING `+qualify("o", outboxColumns),
		string(s), now, limit, token.String(), now.Add(model.OutboxLease))
	if err != nil {
		return nil, fmt.Errorf("claim outbox rows: %w", err)
	}
	defer result.Close()

	rows := []model.OutboxRow{}
	for result.Next() {
		row, err := scanOutboxRow(result)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("claim outbox rows: %w", err)
	}
	slices.SortFunc(rows, func(a, b model.OutboxRow) int {
		if c := a.NextAttemptAt.Compare(b.NextAttemptAt); c != 0 {
			return c
		}
		switch {
		case a.EventSeq < b.EventSeq:
			return -1
		case a.EventSeq > b.EventSeq:
			return 1
		}
		return 0
	})
	return rows, nil
}

// MarkOutboxSent marks a claimed row as delivered, stamping delivered_at with
// the clock and releasing the claim. Only the holder of the current claim can
// do it: a row that does not exist, or lives in another space, is not_found; a
// row already sent or dead is already_done; any other token, including one
// whose claim lapsed and was failed, is rule_violation ("claim lost").
func MarkOutboxSent(ctx context.Context, pool *sql.DB, clock model.Clock, spaceID string, row model.OutboxRow) error {
	if err := validateOutboxRef(row); err != nil {
		return err
	}
	return markOutbox(ctx, pool, spaceID, row, `UPDATE kernel.outbox
		 SET status = 'sent', delivered_at = $4, claim_token = NULL, claimed_until = NULL
		 WHERE event_seq = $1 AND subscriber = $2 AND claim_token = $3::uuid AND status IN ('pending', 'failed')`,
		outboxTime(clock))
}

// MarkOutboxFailed records a failed delivery of a claimed row: one more
// attempt, the cause (trimmed, cut to 1000 characters and stripped of bytes the
// database cannot store) and the claim released. At the fifth attempt the row
// is dead; before it, it is failed and due again after the wait of
// model.OutboxBackoff. A blank cause is invalid_input; the other errors are
// those of MarkOutboxSent.
func MarkOutboxFailed(ctx context.Context, pool *sql.DB, clock model.Clock, spaceID string, row model.OutboxRow, cause string) error {
	if err := validateOutboxRef(row); err != nil {
		return err
	}
	cause = cleanOutboxCause(cause)
	if cause == "" {
		return model.NewError(model.CodeInvalidInput, "the failure cause must not be blank", nil)
	}
	return markOutbox(ctx, pool, spaceID, row, `UPDATE kernel.outbox SET
		   attempts = attempts + 1,
		   status = CASE WHEN attempts + 1 >= $4 THEN 'dead' ELSE 'failed' END,
		   last_error = $5,
		   next_attempt_at = CASE WHEN attempts + 1 >= $4 THEN next_attempt_at
		     ELSE GREATEST(next_attempt_at, $6::timestamptz + ($7::bigint[])[attempts + 1] * interval '1 microsecond') END,
		   claim_token = NULL, claimed_until = NULL
		 WHERE event_seq = $1 AND subscriber = $2 AND claim_token = $3::uuid AND status IN ('pending', 'failed')`,
		model.OutboxMaxAttempts, cause, outboxTime(clock), backoffMicros())
}

// markOutbox runs a marking UPDATE in spaceID. The statement takes the event
// seq, subscriber and claim token of row as $1 to $3 and extra as the rest. When
// it changes no row, the row is read to tell the three reasons apart.
func markOutbox(ctx context.Context, pool *sql.DB, spaceID string, row model.OutboxRow, query string, extra ...any) error {
	args := append([]any{row.EventSeq, string(row.Subscriber), row.ClaimToken}, extra...)
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("mark outbox row: %w", err)
		}
		if changed, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("mark outbox row: %w", err)
		} else if changed == 1 {
			return nil
		}

		var status string
		err = tx.QueryRowContext(ctx, `SELECT status FROM kernel.outbox WHERE event_seq = $1 AND subscriber = $2`,
			row.EventSeq, string(row.Subscriber)).Scan(&status)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return model.NewError(model.CodeNotFound, "the outbox row does not exist", nil)
		case err != nil:
			return fmt.Errorf("diagnose outbox row: %w", err)
		case status == string(model.OutboxSent) || status == string(model.OutboxDead):
			return model.NewError(model.CodeAlreadyDone, "the outbox row is already "+status, nil)
		}
		return model.NewError(model.CodeRuleViolation, "claim lost: the claim of this outbox row is not the current one", nil)
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return err
		}
		return fmt.Errorf("mark outbox: %w", err)
	}
	return nil
}

// validateOutboxClaim checks the arguments of a claim.
func validateOutboxClaim(s model.Subscriber, limit int) error {
	if !s.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown outbox subscriber %q", string(s)), nil)
	}
	if limit < 1 || limit > maxClaimOutbox {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("limit must be between 1 and %d", maxClaimOutbox), nil)
	}
	return nil
}

// validateOutboxRef checks that row names a row and carries a well formed claim
// token, so a malformed value is rejected before it reaches the database.
func validateOutboxRef(row model.OutboxRow) error {
	if row.EventSeq < 1 {
		return model.NewError(model.CodeInvalidInput, "event seq must be at least 1", nil)
	}
	if !row.Subscriber.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown outbox subscriber %q", string(row.Subscriber)), nil)
	}
	if parsed, err := uuid.Parse(row.ClaimToken); err != nil || parsed == uuid.Nil {
		return model.NewError(model.CodeInvalidInput, "claim token must be a non-nil UUID", nil)
	}
	return nil
}

// cleanOutboxCause makes a failure cause storable: invalid UTF-8 and NUL bytes
// are replaced or dropped, the ends are trimmed and it is cut to
// maxOutboxCause characters. It returns "" for a blank cause.
func cleanOutboxCause(cause string) string {
	cause = strings.ToValidUTF8(strings.ReplaceAll(cause, "\x00", ""), "�")
	cause = strings.TrimSpace(cause)
	if utf8.RuneCountInString(cause) > maxOutboxCause {
		cause = string([]rune(cause)[:maxOutboxCause])
	}
	return strings.TrimSpace(cause)
}

// qualify prefixes every column of a comma separated list with alias.
func qualify(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = alias + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

// scanOutboxRow reads one row selected with outboxColumns.
func scanOutboxRow(rows *sql.Rows) (model.OutboxRow, error) {
	var (
		row                     model.OutboxRow
		subscriber, status      string
		lastError, token        sql.NullString
		deliveredAt, claimedEnd sql.NullTime
	)
	if err := rows.Scan(&row.EventSeq, &row.SpaceID, &subscriber, &status, &row.Attempts, &row.NextAttemptAt,
		&lastError, &deliveredAt, &token, &claimedEnd); err != nil {
		return model.OutboxRow{}, fmt.Errorf("scan outbox row: %w", err)
	}
	row.Subscriber = model.Subscriber(subscriber)
	row.Status = model.OutboxStatus(status)
	row.NextAttemptAt = row.NextAttemptAt.UTC()
	row.LastError = lastError.String
	row.ClaimToken = token.String
	if deliveredAt.Valid {
		at := deliveredAt.Time.UTC()
		row.DeliveredAt = &at
	}
	if claimedEnd.Valid {
		at := claimedEnd.Time.UTC()
		row.ClaimedUntil = &at
	}
	return row, nil
}

// translateOutboxError maps database failures of an enqueue to typed errors
// and leaves any other error wrapped but untyped. Constraints are told apart by
// name or column, never by SQLSTATE alone.
func translateOutboxError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23514":
			return model.NewError(model.CodeInvalidInput, "outbox data violates a constraint", err)
		case "23503":
			if pgErr.ConstraintName == "outbox_event_fkey" {
				return model.NewError(model.CodeRuleViolation, "the event does not exist in this space", err)
			}
		case "23505":
			return model.NewError(model.CodeRuleViolation, "the event is already queued for this subscriber", err)
		case "23502":
			if pgErr.ColumnName == "space_id" {
				return model.NewError(model.CodeRuleViolation, "the transaction has no current space to write outbox rows in", err)
			}
		}
	}
	return fmt.Errorf("enqueue outbox: %w", err)
}
