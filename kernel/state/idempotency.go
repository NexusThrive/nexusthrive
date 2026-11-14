package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// LockIdempotencyKey takes, in the caller's transaction, the advisory lock of
// key in the current space and holds it until the transaction ends. Two
// executions of the same key serialize on it: the second one waits for the
// first, then reads its record (ADR-0026). Locking again in the same
// transaction does not wait. The wait ends with the context of the call.
//
// It never begins, commits or rolls back. An invalid key is invalid_input and a
// transaction with no current space is rule_violation, both before any query
// that locks.
func LockIdempotencyKey(ctx context.Context, tx *sql.Tx, key string) error {
	if err := model.ValidateIdempotencyKey(key); err != nil {
		return err
	}
	if err := requireCurrentSpace(ctx, tx, "lock an idempotency key in"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT kernel.idempotency_lock($1)`, key); err != nil {
		return fmt.Errorf("lock idempotency key: %w", err)
	}
	return nil
}

// FindIdempotencyRecord returns the record in force for key in the current
// space: the one of the greatest generation, as long as it has not expired. A
// record expires at the instant its expiry is reached, by the earlier of the
// injected clock and the clock of the database. The clock can therefore run
// behind the database, which keeps a record longer, never ahead of it, which
// would expire a record that is still in force.
//
// It never begins, commits or rolls back. It does not lock: the caller takes
// LockIdempotencyKey first, so that what it reads cannot change under it.
func FindIdempotencyRecord(ctx context.Context, tx *sql.Tx, clock model.Clock, key string) (model.IdempotencyRecord, bool, error) {
	if err := model.ValidateIdempotencyKey(key); err != nil {
		return model.IdempotencyRecord{}, false, err
	}
	if clock == nil {
		return model.IdempotencyRecord{}, false, model.NewError(model.CodeInvalidInput, "an idempotency lookup needs a clock", nil)
	}
	if err := requireCurrentSpace(ctx, tx, "look up an idempotency key in"); err != nil {
		return model.IdempotencyRecord{}, false, err
	}

	var out model.IdempotencyRecord
	var hash []byte
	var response string
	var inForce bool
	// The greatest generation is the one that counts: an older one that the
	// clock would still call in force must never answer for it.
	err := tx.QueryRowContext(ctx,
		`SELECT id::text, space_id::text, key, generation, operation, actor_participant_id::text, request_hash,
		        response::text, created_at, expires_at, expires_at > LEAST($2::timestamptz, now())
		 FROM kernel.idempotency_records
		 WHERE space_id = kernel.current_space_id() AND key = $1
		 ORDER BY generation DESC
		 LIMIT 1`,
		key, outboxTime(clock),
	).Scan(&out.ID, &out.SpaceID, &out.Key, &out.Generation, &out.Operation, &out.ActorParticipantID, &hash,
		&response, &out.CreatedAt, &out.ExpiresAt, &inForce)
	if errors.Is(err, sql.ErrNoRows) {
		return model.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return model.IdempotencyRecord{}, false, fmt.Errorf("find idempotency record: %w", err)
	}
	if !inForce {
		return model.IdempotencyRecord{}, false, nil
	}
	if len(hash) != len(out.RequestHash) {
		return model.IdempotencyRecord{}, false, fmt.Errorf("find idempotency record: stored fingerprint has %d bytes", len(hash))
	}
	copy(out.RequestHash[:], hash)
	out.Response = []byte(response)
	out.CreatedAt, out.ExpiresAt = out.CreatedAt.UTC(), out.ExpiresAt.UTC()
	return out, true, nil
}

// AppendIdempotencyRecord writes the record of one execution in the caller's
// transaction and returns it as stored, with the generation and the expiry the
// database stamped. The space is the current space of the transaction. Its
// creation time is the earlier of the injected clock and the clock of the
// database, so that a clock ahead of the database, even by a few milliseconds
// between two hosts, never makes the write fail and never stretches the life of
// the record beyond 24 hours of the database.
//
// It never begins, commits or rolls back, and uses no savepoints: the record
// lives or dies with the rest of the operation. The input is validated first: an
// invalid_input error writes nothing and leaves the transaction usable. A key
// that already has a record in force, or an id already used, is rule_violation,
// and so is an actor that is not a participant of the space; a database error
// leaves the transaction aborted and the caller must roll it back. The caller
// holds LockIdempotencyKey, so that two executions of one key cannot both get here.
func AppendIdempotencyRecord(ctx context.Context, tx *sql.Tx, clock model.Clock, in model.NewIdempotencyRecord) (model.IdempotencyRecord, error) {
	if clock == nil {
		return model.IdempotencyRecord{}, model.NewError(model.CodeInvalidInput, "an idempotency record needs a clock", nil)
	}
	if err := in.Validate(); err != nil {
		return model.IdempotencyRecord{}, err
	}
	if err := requireCurrentSpace(ctx, tx, "write an idempotency record in"); err != nil {
		return model.IdempotencyRecord{}, err
	}

	stored := model.IdempotencyRecord{
		ID: in.ID, Key: in.Key, Operation: in.Operation, ActorParticipantID: in.Actor.ParticipantID, RequestHash: in.RequestHash,
	}
	var response string
	err := tx.QueryRowContext(ctx,
		`INSERT INTO kernel.idempotency_records
		   (id, space_id, key, operation, actor_participant_id, request_hash, response, created_at)
		 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5, $6::jsonb, LEAST($7::timestamptz, now()))
		 RETURNING space_id::text, generation, response::text, created_at, expires_at`,
		in.ID, in.Key, in.Operation, in.Actor.ParticipantID, in.RequestHash[:], string(in.Response), outboxTime(clock),
	).Scan(&stored.SpaceID, &stored.Generation, &response, &stored.CreatedAt, &stored.ExpiresAt)
	if err != nil {
		return model.IdempotencyRecord{}, translateIdempotencyError("append idempotency record", err)
	}
	stored.Response = []byte(response)
	stored.CreatedAt, stored.ExpiresAt = stored.CreatedAt.UTC(), stored.ExpiresAt.UTC()
	return stored, nil
}

// translateIdempotencyError maps the database failures of the records to typed
// errors, first by constraint name and then by code, so that a raw uniqueness
// error never reaches a caller, and leaves any other error wrapped but untyped.
// An error that is already typed passes through.
func translateIdempotencyError(operation string, err error) error {
	if _, typed := model.CodeOf(err); typed {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "idempotency_records_key_in_force", "idempotency_records_pkey", "idempotency_records_id_space_key":
			return model.NewError(model.CodeRuleViolation, "an idempotency record with this key or id already exists in force", err)
		case "idempotency_records_actor_fkey":
			return model.NewError(model.CodeRuleViolation, "the actor of an idempotency record must be a participant of the space", err)
		}
		switch pgErr.Code {
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "idempotency record data violates a constraint", err)
		case "23001":
			return model.NewError(model.CodeRuleViolation, "the database refused the idempotency record: it is outside the current space or dated after the clock of the database", err)
		case "23502":
			return model.NewError(model.CodeRuleViolation, "the transaction has no current space to write an idempotency record in", err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
