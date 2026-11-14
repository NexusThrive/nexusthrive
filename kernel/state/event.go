package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// maxReadEvents is the largest page ReadEvents returns.
const maxReadEvents = 1000

// eventColumns lists the columns scanEvent reads, in order.
const eventColumns = `seq, id, space_id, type, version, subject_ref, payload,
	actor_participant_id, actor_kind, request_id, occurred_at`

// AppendEvent writes one event in the caller's transaction and returns it with
// its seq. The transaction must already have a current space; the space of the
// event is that space, never a value from the caller.
//
// It never begins, commits or rolls back, and uses no savepoints, so the event
// lives or dies with the rest of the operation (one operation, one
// transaction). The input is validated first: an invalid_input or
// permission_denied error writes nothing and leaves the transaction usable. A
// database error leaves it aborted, and the caller must roll it back. A
// transaction with no current space gets rule_violation before any write.
func AppendEvent(ctx context.Context, tx *sql.Tx, in model.NewEvent) (model.Event, error) {
	if err := in.Validate(); err != nil {
		return model.Event{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return model.Event{}, fmt.Errorf("generate event id: %w", err)
	}
	var occurredAt any
	if !in.OccurredAt.IsZero() {
		occurredAt = in.OccurredAt
	}

	// Check the space first and explicitly: relying on the database error would
	// mean treating a privilege failure (42501) as "no space", which hides real
	// permission problems. The read does not abort the transaction.
	var space sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT kernel.current_space_id()::text`).Scan(&space); err != nil {
		return model.Event{}, fmt.Errorf("read current space: %w", err)
	}
	if !space.Valid {
		return model.Event{}, model.NewError(model.CodeRuleViolation, "the transaction has no current space to write events in", nil)
	}

	stored := model.Event{NewEvent: in, ID: id.String()}
	var payload []byte
	err = tx.QueryRowContext(ctx,
		`INSERT INTO kernel.events
		   (id, space_id, type, version, subject_ref, payload, actor_participant_id, actor_kind, request_id, occurred_at)
		 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5::jsonb, $6, $7, $8, COALESCE($9::timestamptz, now()))
		 RETURNING seq, space_id, payload, occurred_at`,
		stored.ID, in.Type, in.Version, nullIfEmpty(in.SubjectRef), string(in.Payload),
		nullIfEmpty(in.Actor.ParticipantID), string(in.Actor.Kind), nullIfEmpty(in.RequestID), occurredAt,
	).Scan(&stored.Seq, &stored.SpaceID, &payload, &stored.OccurredAt)
	if err != nil {
		return model.Event{}, translateEventError(err)
	}
	stored.Payload = json.RawMessage(payload)
	return stored, nil
}

// RecordEvent appends one event to spaceID in a transaction of its own. It is
// for standalone use and tests; operations that write more than the event call
// AppendEvent inside their own transaction.
func RecordEvent(ctx context.Context, pool *sql.DB, spaceID string, in model.NewEvent) (model.Event, error) {
	var stored model.Event
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stored, err = AppendEvent(ctx, tx, in)
		return err
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return model.Event{}, err
		}
		return model.Event{}, fmt.Errorf("record event: %w", err)
	}
	return stored, nil
}

// ReadEvents returns up to limit committed events of spaceID with seq of at
// least fromSeq, in ascending seq order. The lower bound is inclusive, so a
// consumer stores the last seq it handled plus one as its cursor. Because seq
// follows allocation and not commit order, a consumer must overlap its reads
// and deduplicate by id (ADR-0028).
func ReadEvents(ctx context.Context, pool *sql.DB, spaceID string, fromSeq int64, limit int) ([]model.Event, error) {
	if err := validateEventRead(fromSeq, limit); err != nil {
		return nil, err
	}
	var events []model.Event
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		events, err = ReadEventsTx(ctx, tx, fromSeq, limit)
		return err
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return nil, err
		}
		return nil, fmt.Errorf("read events: %w", err)
	}
	return events, nil
}

// ReadEventsTx is ReadEvents inside the caller's transaction, whose current
// space decides which events are visible.
func ReadEventsTx(ctx context.Context, tx *sql.Tx, fromSeq int64, limit int) ([]model.Event, error) {
	if err := validateEventRead(fromSeq, limit); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM kernel.events WHERE seq >= $1 ORDER BY seq ASC LIMIT $2`,
		fromSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events := []model.Event{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return events, nil
}

// validateEventRead checks the cursor and page size of a read.
func validateEventRead(fromSeq int64, limit int) error {
	if fromSeq < 0 {
		return model.NewError(model.CodeInvalidInput, "from seq must not be negative", nil)
	}
	if limit < 1 || limit > maxReadEvents {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("limit must be between 1 and %d", maxReadEvents), nil)
	}
	return nil
}

// scanEvent reads one row selected with eventColumns.
func scanEvent(rows *sql.Rows) (model.Event, error) {
	var (
		event                           model.Event
		subject, participant, requestID sql.NullString
		payload                         []byte
		kind                            string
	)
	if err := rows.Scan(&event.Seq, &event.ID, &event.SpaceID, &event.Type, &event.Version,
		&subject, &payload, &participant, &kind, &requestID, &event.OccurredAt); err != nil {
		return model.Event{}, fmt.Errorf("scan event: %w", err)
	}
	event.SubjectRef = subject.String
	event.RequestID = requestID.String
	event.Payload = json.RawMessage(payload)
	event.Actor = model.Actor{Kind: model.ActorKind(kind), ParticipantID: participant.String}
	return event, nil
}

// translateEventError maps database failures of an event append to typed
// errors and leaves any other error wrapped but untyped.
func translateEventError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23502":
			return model.NewError(model.CodeRuleViolation, "the transaction has no current space to write events in", err)
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "event data violates a constraint", err)
		case "23503":
			if pgErr.ConstraintName == "events_actor_fkey" {
				return model.NewError(model.CodeRuleViolation, "event actor does not exist in this space", err)
			}
		}
	}
	return fmt.Errorf("append event: %w", err)
}
