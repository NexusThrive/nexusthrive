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

// maxReversalEntries bounds the entries read back from an exchange to reverse
// it; it matches the limit an exchange is allowed to have.
const maxReversalEntries = 100

// AppendExchange writes one exchange and its entries in the caller's
// transaction and returns them as stored. The transaction must already have a
// current space; the space of the exchange is that space, never a value from
// the caller.
//
// Like AppendEvent it never begins, commits or rolls back and uses no
// savepoints, so the exchange lives or dies with the rest of the operation
// (one operation, one transaction). The input is validated first: an
// invalid_input or permission_denied error writes nothing and leaves the
// transaction usable. A transaction with no current space gets rule_violation
// before any write. A database error leaves the transaction aborted, and the
// caller must roll it back.
//
// The balance of the entries is checked before returning, so a conservation
// failure comes out of this call as rule_violation. The deferred triggers of
// the database stay the final guarantee when the transaction commits.
func AppendExchange(ctx context.Context, tx *sql.Tx, actor model.Actor, in model.NewExchange) (model.Exchange, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Exchange{}, err
	}
	if err := in.Validate(); err != nil {
		return model.Exchange{}, err
	}

	// Check the space explicitly, as AppendEvent does, so that a missing space
	// is not confused with a privilege failure.
	var space sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT kernel.current_space_id()::text`).Scan(&space); err != nil {
		return model.Exchange{}, fmt.Errorf("read current space: %w", err)
	}
	if !space.Valid {
		return model.Exchange{}, model.NewError(model.CodeRuleViolation, "the transaction has no current space to write exchanges in", nil)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return model.Exchange{}, fmt.Errorf("generate exchange id: %w", err)
	}
	var occurredAt, metadata any
	if !in.OccurredAt.IsZero() {
		occurredAt = in.OccurredAt
	}
	if len(in.Metadata) > 0 {
		metadata = string(in.Metadata)
	}

	stored := model.Exchange{
		ID: id.String(), Kind: in.Kind, IdempotencyKey: in.IdempotencyKey,
		ActorParticipantID: actor.ParticipantID, ReversesExchangeID: in.ReversesExchangeID, Memo: in.Memo,
	}
	var storedMetadata string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO kernel.exchanges
		   (id, space_id, kind, idempotency_key, actor_participant_id, reverses_exchange_id, memo, metadata, occurred_at)
		 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5, $6, COALESCE($7::jsonb, '{}'::jsonb), COALESCE($8::timestamptz, now()))
		 RETURNING space_id::text, metadata::text, occurred_at, recorded_at`,
		stored.ID, in.Kind, in.IdempotencyKey, actor.ParticipantID, nullIfEmpty(in.ReversesExchangeID),
		nullIfEmpty(in.Memo), metadata, occurredAt,
	).Scan(&stored.SpaceID, &storedMetadata, &stored.OccurredAt, &stored.RecordedAt)
	if err != nil {
		return model.Exchange{}, translateExchangeError("append exchange", err)
	}
	stored.Metadata = json.RawMessage(storedMetadata)
	stored.OccurredAt = stored.OccurredAt.UTC()
	stored.RecordedAt = stored.RecordedAt.UTC()

	for _, entry := range in.Entries {
		entryID, err := uuid.NewV7()
		if err != nil {
			return model.Exchange{}, fmt.Errorf("generate entry id: %w", err)
		}
		var amountText string
		err = tx.QueryRowContext(ctx,
			`INSERT INTO kernel.entries (id, exchange_id, space_id, account_id, resource_id, amount)
			 VALUES ($1, $2, kernel.current_space_id(), $3, $4, $5::numeric)
			 RETURNING amount::text`,
			entryID.String(), stored.ID, entry.AccountID, entry.ResourceID, entry.Amount.String(),
		).Scan(&amountText)
		if err != nil {
			return model.Exchange{}, translateExchangeError("append entry", err)
		}
		storedAmount, err := model.ParseAmount(amountText)
		if err != nil {
			return model.Exchange{}, fmt.Errorf("read stored amount %q: %w", amountText, err)
		}
		stored.Entries = append(stored.Entries, model.Entry{
			ID: entryID.String(), ExchangeID: stored.ID, AccountID: entry.AccountID,
			ResourceID: entry.ResourceID, Amount: storedAmount,
		})
	}

	// Run the deferred checks now so the failure is typed and belongs to this
	// call, then defer them again for anything the caller writes afterwards.
	if _, err := tx.ExecContext(ctx, `SET CONSTRAINTS kernel.exchanges_balance_check, kernel.entries_balance_check IMMEDIATE`); err != nil {
		return model.Exchange{}, translateExchangeError("check exchange balance", err)
	}
	if _, err := tx.ExecContext(ctx, `SET CONSTRAINTS kernel.exchanges_balance_check, kernel.entries_balance_check DEFERRED`); err != nil {
		return model.Exchange{}, fmt.Errorf("defer exchange balance checks: %w", err)
	}
	return stored, nil
}

// RecordExchange appends one exchange to spaceID in a transaction of its own.
// It is for standalone use and tests; operations that write more than the
// exchange call AppendExchange inside their own transaction.
func RecordExchange(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in model.NewExchange) (model.Exchange, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Exchange{}, err
	}
	if err := in.Validate(); err != nil {
		return model.Exchange{}, err
	}
	var stored model.Exchange
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stored, err = AppendExchange(ctx, tx, actor, in)
		return err
	})
	if err != nil {
		return model.Exchange{}, translateExchangeError("record exchange", err)
	}
	return stored, nil
}

// AppendReversal writes, in the caller's transaction, the exchange that
// corrects exchangeID: the same accounts and resources with every amount of
// the opposite sign. Corrections never edit history. The exchange to correct
// must be visible in the current space, else it is not_found; correcting one
// twice is already_done, decided by the unique index of the database so that a
// race has one winner. Everything else follows AppendExchange.
func AppendReversal(ctx context.Context, tx *sql.Tx, actor model.Actor, exchangeID, idempotencyKey string) (model.Exchange, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Exchange{}, err
	}
	if err := requireID("exchange id", exchangeID); err != nil {
		return model.Exchange{}, err
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT account_id::text, resource_id::text, amount::text FROM kernel.entries
		 WHERE exchange_id = $1 ORDER BY id LIMIT $2`, exchangeID, maxReversalEntries+1)
	if err != nil {
		return model.Exchange{}, fmt.Errorf("read entries to reverse: %w", err)
	}
	defer rows.Close()
	var entries []model.NewEntry
	for rows.Next() {
		var account, resource, text string
		if err := rows.Scan(&account, &resource, &text); err != nil {
			return model.Exchange{}, fmt.Errorf("scan entry to reverse: %w", err)
		}
		original, err := model.ParseAmount(text)
		if err != nil {
			return model.Exchange{}, fmt.Errorf("read amount %q to reverse: %w", text, err)
		}
		entries = append(entries, model.NewEntry{AccountID: account, ResourceID: resource, Amount: original.Neg()})
	}
	if err := rows.Err(); err != nil {
		return model.Exchange{}, fmt.Errorf("read entries to reverse: %w", err)
	}
	rows.Close()
	if len(entries) == 0 {
		return model.Exchange{}, model.NewError(model.CodeNotFound, "exchange not found in this space", nil)
	}

	return AppendExchange(ctx, tx, actor, model.NewExchange{
		Kind: model.ReversalKind, IdempotencyKey: idempotencyKey, ReversesExchangeID: exchangeID, Entries: entries,
	})
}

// ReverseExchange runs AppendReversal for spaceID in a transaction of its own.
func ReverseExchange(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, exchangeID, idempotencyKey string) (model.Exchange, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Exchange{}, err
	}
	var stored model.Exchange
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stored, err = AppendReversal(ctx, tx, actor, exchangeID, idempotencyKey)
		return err
	})
	if err != nil {
		return model.Exchange{}, translateExchangeError("reverse exchange", err)
	}
	return stored, nil
}

// GetExchange returns an exchange of spaceID with its entries. An id that does
// not exist, or belongs to another space, is not_found.
func GetExchange(ctx context.Context, pool *sql.DB, spaceID, exchangeID string) (model.Exchange, error) {
	if err := requireID("exchange id", exchangeID); err != nil {
		return model.Exchange{}, err
	}
	var out model.Exchange
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var reverses, memo sql.NullString
		var metadata string
		err := tx.QueryRowContext(ctx,
			`SELECT id::text, space_id::text, kind, idempotency_key, actor_participant_id::text,
			        reverses_exchange_id::text, memo, metadata::text, occurred_at, recorded_at
			 FROM kernel.exchanges WHERE id = $1`, exchangeID).
			Scan(&out.ID, &out.SpaceID, &out.Kind, &out.IdempotencyKey, &out.ActorParticipantID,
				&reverses, &memo, &metadata, &out.OccurredAt, &out.RecordedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return model.NewError(model.CodeNotFound, "exchange not found in this space", nil)
		}
		if err != nil {
			return err
		}
		out.ReversesExchangeID = reverses.String
		out.Memo = memo.String
		out.Metadata = json.RawMessage(metadata)
		out.OccurredAt = out.OccurredAt.UTC()
		out.RecordedAt = out.RecordedAt.UTC()

		rows, err := tx.QueryContext(ctx,
			`SELECT id::text, account_id::text, resource_id::text, amount::text
			 FROM kernel.entries WHERE exchange_id = $1 ORDER BY id`, exchangeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			entry := model.Entry{ExchangeID: exchangeID}
			var text string
			if err := rows.Scan(&entry.ID, &entry.AccountID, &entry.ResourceID, &text); err != nil {
				return err
			}
			if entry.Amount, err = model.ParseAmount(text); err != nil {
				return fmt.Errorf("read amount %q: %w", text, err)
			}
			out.Entries = append(out.Entries, entry)
		}
		return rows.Err()
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return model.Exchange{}, err
		}
		return model.Exchange{}, fmt.Errorf("get exchange: %w", err)
	}
	return out, nil
}

// validateExchangeActor checks who writes the exchange. Nothing is written
// anonymously: only the system actor may write until permissions arrive, and it
// must act through a participant of the space.
func validateExchangeActor(actor model.Actor) error {
	if actor.Kind != model.ActorSystem {
		return model.NewError(model.CodePermissionDenied, "only the system actor writes exchanges", nil)
	}
	if actor.ParticipantID == "" {
		return model.NewError(model.CodePermissionDenied, "an exchange needs an actor participant", nil)
	}
	return requireID("actor participant id", actor.ParticipantID)
}

// translateExchangeError maps the database failures of the ledger to typed
// errors, first by constraint name and then by code, and leaves any other error
// wrapped but untyped. An error that is already typed passes through. The
// errors of a commit are translated too: errors.As crosses the wrapper.
func translateExchangeError(operation string, err error) error {
	if _, typed := model.CodeOf(err); typed {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "exchanges_idempotency_key":
			return model.NewError(model.CodeRuleViolation, "an exchange with this idempotency key already exists", err)
		case "exchanges_actor_fkey":
			return model.NewError(model.CodeRuleViolation, "the actor does not exist in this space", err)
		case "entries_account_fkey", "entries_account_open", "entries_account_resource":
			return model.NewError(model.CodeRuleViolation, "an entry account must exist in this space, be open and hold the entry resource", err)
		case "entries_same_transaction":
			return model.NewError(model.CodeRuleViolation, "entries cannot be added to an exchange that is already committed", err)
		case "exchanges_conservation":
			return model.NewError(model.CodeRuleViolation, "the entries of an exchange must add up to zero per resource", err)
		case "exchanges_min_entries":
			return model.NewError(model.CodeRuleViolation, "an exchange needs at least two entries", err)
		case "exchanges_reversal_mirror":
			return model.NewError(model.CodeRuleViolation, "a reversal must mirror the exchange it reverses", err)
		case "exchanges_reverses_once_key":
			return model.NewError(model.CodeAlreadyDone, "the exchange is already reversed", err)
		case "exchanges_reverses_fkey", "exchanges_space_id_fkey", "entries_space_id_fkey":
			return model.NewError(model.CodeNotFound, "the space or the reversed exchange does not exist", err)
		case "entries_amount_scale", "exchanges_max_entries":
			return model.NewError(model.CodeInvalidInput, "the exchange data is out of range: too many decimals for the scale or too many entries", err)
		}
		switch pgErr.Code {
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "exchange data violates a constraint", err)
		case "23502":
			return model.NewError(model.CodeRuleViolation, "the transaction has no current space to write exchanges in", err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
