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

// commitmentColumns is the column list every read of a commitment shares with
// scanCommitment.
const commitmentColumns = `id::text, space_id::text, kind, debtor_participant_id::text, creditor_participant_id::text,
	resource_id::text, amount::text, fulfilled_amount::text, starts_at, due_at, status, origin_ref, canceled_reason,
	closed_at, created_at, created_by_participant_id::text`

// rowScanner is what scanCommitment needs from a sql.Row or sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanCommitment reads one commitment in the order of commitmentColumns and
// converts the times to UTC.
func scanCommitment(row rowScanner) (model.Commitment, error) {
	var out model.Commitment
	var amountText, fulfilledText, status string
	var origin, reason sql.NullString
	var closed sql.NullTime
	if err := row.Scan(&out.ID, &out.SpaceID, &out.Kind, &out.DebtorParticipantID, &out.CreditorParticipantID,
		&out.ResourceID, &amountText, &fulfilledText, &out.StartsAt, &out.DueAt, &status, &origin, &reason,
		&closed, &out.CreatedAt, &out.CreatedByParticipantID); err != nil {
		return model.Commitment{}, err
	}
	var err error
	if out.Amount, err = model.ParseAmount(amountText); err != nil {
		return model.Commitment{}, fmt.Errorf("read commitment amount %q: %w", amountText, err)
	}
	if out.FulfilledAmount, err = model.ParseAmount(fulfilledText); err != nil {
		return model.Commitment{}, fmt.Errorf("read fulfilled amount %q: %w", fulfilledText, err)
	}
	out.Status = model.CommitmentStatus(status)
	out.OriginRef, out.CanceledReason = origin.String, reason.String
	out.StartsAt, out.DueAt, out.CreatedAt = out.StartsAt.UTC(), out.DueAt.UTC(), out.CreatedAt.UTC()
	if closed.Valid {
		out.ClosedAt = closed.Time.UTC()
	}
	return out, nil
}

// AppendCommitment writes one open commitment in the caller's transaction and
// returns it as stored. The transaction must already have a current space; the
// space is that space and the creator is the actor, never values from the
// caller.
//
// Like AppendExchange it never begins, commits or rolls back and uses no
// savepoints, so the commitment lives or dies with the rest of the operation.
// Invalid input and a denied actor write nothing and leave the transaction
// usable; a database error leaves it aborted and the caller must roll it back.
// The state, the fulfilled amount and the closing time are never written here:
// the kernel derives them. The database also creates the expiry timer of the
// commitment in the same statement, at its due date (ADR-0034), so a commitment
// with a due date in the past expires at the first firing round.
func AppendCommitment(ctx context.Context, tx *sql.Tx, actor model.Actor, in model.NewCommitment) (model.Commitment, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Commitment{}, err
	}
	if err := in.Validate(); err != nil {
		return model.Commitment{}, err
	}
	if err := requireCurrentSpace(ctx, tx, "write commitments in"); err != nil {
		return model.Commitment{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return model.Commitment{}, fmt.Errorf("generate commitment id: %w", err)
	}
	var startsAt any
	if !in.StartsAt.IsZero() {
		startsAt = in.StartsAt
	}
	stored, err := scanCommitment(tx.QueryRowContext(ctx,
		`INSERT INTO kernel.commitments
		   (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id, amount, starts_at, due_at,
		    status, origin_ref, created_by_participant_id)
		 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5, $6::numeric, COALESCE($7::timestamptz, now()), $8, 'open', $9, $10)
		 RETURNING `+commitmentColumns,
		id.String(), in.Kind, in.DebtorParticipantID, in.CreditorParticipantID, in.ResourceID, in.Amount.String(),
		startsAt, in.DueAt, nullIfEmpty(in.OriginRef), actor.ParticipantID))
	if err != nil {
		return model.Commitment{}, translateCommitmentError("append commitment", err)
	}
	return stored, nil
}

// CreateCommitment appends one commitment to spaceID in a transaction of its
// own. It is for standalone use and tests; operations that write more than the
// commitment call AppendCommitment inside their own transaction.
func CreateCommitment(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in model.NewCommitment) (model.Commitment, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Commitment{}, err
	}
	if err := in.Validate(); err != nil {
		return model.Commitment{}, err
	}
	var stored model.Commitment
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stored, err = AppendCommitment(ctx, tx, actor, in)
		return err
	})
	if err != nil {
		return model.Commitment{}, translateCommitmentError("create commitment", err)
	}
	return stored, nil
}

// readCommitment returns a commitment visible in the transaction, or not_found.
func readCommitment(ctx context.Context, tx *sql.Tx, id string) (model.Commitment, error) {
	got, err := scanCommitment(tx.QueryRowContext(ctx, `SELECT `+commitmentColumns+` FROM kernel.commitments WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Commitment{}, model.NewError(model.CodeNotFound, "commitment not found in this space", nil)
	}
	if err != nil {
		return model.Commitment{}, fmt.Errorf("read commitment: %w", err)
	}
	return got, nil
}

// AppendFulfillment applies one ledger entry to a commitment in the caller's
// transaction and returns the fulfillment and the commitment after it. The
// amount is the amount of the entry, read by the statement itself, so a caller
// cannot apply an entry in part or for a value of its own. Everything else
// follows AppendCommitment.
//
// A commitment that does not exist in this space is not_found. An entry that is
// not visible, belongs to another resource, does not enter an account of the
// creditor, belongs to a reversal or was applied before is rule_violation, and
// so is a commitment that is not open or whose pending amount is smaller than
// the entry. The database locks the commitment row first, so two concurrent
// fulfillments serialize and cannot exceed the amount between them.
func AppendFulfillment(ctx context.Context, tx *sql.Tx, actor model.Actor, commitmentID, entryID string) (model.Fulfillment, model.Commitment, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Fulfillment{}, model.Commitment{}, err
	}
	if err := requireID("commitment id", commitmentID); err != nil {
		return model.Fulfillment{}, model.Commitment{}, err
	}
	if err := requireID("entry id", entryID); err != nil {
		return model.Fulfillment{}, model.Commitment{}, err
	}
	if err := requireCurrentSpace(ctx, tx, "write fulfillments in"); err != nil {
		return model.Fulfillment{}, model.Commitment{}, err
	}
	if _, err := readCommitment(ctx, tx, commitmentID); err != nil {
		return model.Fulfillment{}, model.Commitment{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return model.Fulfillment{}, model.Commitment{}, fmt.Errorf("generate fulfillment id: %w", err)
	}

	out := model.Fulfillment{}
	var amountText string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO kernel.fulfillments (id, space_id, commitment_id, entry_id, resource_id, amount)
		 SELECT $1, kernel.current_space_id(), $2, e.id, e.resource_id, e.amount FROM kernel.entries e WHERE e.id = $3
		 RETURNING id::text, space_id::text, commitment_id::text, entry_id::text, resource_id::text, amount::text, created_at`,
		id.String(), commitmentID, entryID,
	).Scan(&out.ID, &out.SpaceID, &out.CommitmentID, &out.EntryID, &out.ResourceID, &amountText, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Fulfillment{}, model.Commitment{}, model.NewError(model.CodeRuleViolation, "the entry does not exist in this space", nil)
	}
	if err != nil {
		return model.Fulfillment{}, model.Commitment{}, translateCommitmentError("append fulfillment", err)
	}
	if out.Amount, err = model.ParseAmount(amountText); err != nil {
		return model.Fulfillment{}, model.Commitment{}, fmt.Errorf("read fulfillment amount %q: %w", amountText, err)
	}
	out.CreatedAt = out.CreatedAt.UTC()

	// Read the commitment again: the database moved its fulfilled amount and,
	// when the last fulfillment reached the amount, its state.
	updated, err := readCommitment(ctx, tx, commitmentID)
	if err != nil {
		return model.Fulfillment{}, model.Commitment{}, err
	}
	return out, updated, nil
}

// FulfillCommitment runs AppendFulfillment for spaceID in a transaction of its
// own.
func FulfillCommitment(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, commitmentID, entryID string) (model.Fulfillment, model.Commitment, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Fulfillment{}, model.Commitment{}, err
	}
	var fulfillment model.Fulfillment
	var commitment model.Commitment
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		fulfillment, commitment, err = AppendFulfillment(ctx, tx, actor, commitmentID, entryID)
		return err
	})
	if err != nil {
		return model.Fulfillment{}, model.Commitment{}, translateCommitmentError("fulfill commitment", err)
	}
	return fulfillment, commitment, nil
}

// AppendCancelCommitment closes an open commitment with a reason in the
// caller's transaction and returns it. What was fulfilled stays recorded. A
// commitment that does not exist in this space is not_found and one that is no
// longer open is already_done; the update waits for a concurrent change of the
// same commitment and then decides on the committed state, so a cancellation and
// a fulfillment that race have one winner. Everything else follows
// AppendCommitment.
func AppendCancelCommitment(ctx context.Context, tx *sql.Tx, actor model.Actor, commitmentID, reason string) (model.Commitment, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Commitment{}, err
	}
	if err := requireID("commitment id", commitmentID); err != nil {
		return model.Commitment{}, err
	}
	if err := model.ValidateCancelReason(reason); err != nil {
		return model.Commitment{}, err
	}
	if err := requireCurrentSpace(ctx, tx, "write commitments in"); err != nil {
		return model.Commitment{}, err
	}
	got, err := scanCommitment(tx.QueryRowContext(ctx,
		`UPDATE kernel.commitments SET canceled_reason = $2 WHERE id = $1 AND status = 'open' RETURNING `+commitmentColumns,
		commitmentID, reason))
	if err == nil {
		return got, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.Commitment{}, translateCommitmentError("cancel commitment", err)
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

// CancelCommitment runs AppendCancelCommitment for spaceID in a transaction of
// its own.
func CancelCommitment(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, commitmentID, reason string) (model.Commitment, error) {
	if err := validateExchangeActor(actor); err != nil {
		return model.Commitment{}, err
	}
	if err := model.ValidateCancelReason(reason); err != nil {
		return model.Commitment{}, err
	}
	var stored model.Commitment
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stored, err = AppendCancelCommitment(ctx, tx, actor, commitmentID, reason)
		return err
	})
	if err != nil {
		return model.Commitment{}, translateCommitmentError("cancel commitment", err)
	}
	return stored, nil
}

// GetCommitment returns a commitment of spaceID with its fulfillments, oldest
// first. An id that does not exist, or belongs to another space, is not_found.
func GetCommitment(ctx context.Context, pool *sql.DB, spaceID, commitmentID string) (model.Commitment, error) {
	if err := requireID("commitment id", commitmentID); err != nil {
		return model.Commitment{}, err
	}
	var out model.Commitment
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if out, err = readCommitment(ctx, tx, commitmentID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT id::text, space_id::text, commitment_id::text, entry_id::text, resource_id::text, amount::text, created_at
			 FROM kernel.fulfillments WHERE commitment_id = $1 ORDER BY id`, commitmentID)
		if err != nil {
			return fmt.Errorf("read fulfillments: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var fulfillment model.Fulfillment
			var text string
			if err := rows.Scan(&fulfillment.ID, &fulfillment.SpaceID, &fulfillment.CommitmentID, &fulfillment.EntryID,
				&fulfillment.ResourceID, &text, &fulfillment.CreatedAt); err != nil {
				return fmt.Errorf("scan fulfillment: %w", err)
			}
			if fulfillment.Amount, err = model.ParseAmount(text); err != nil {
				return fmt.Errorf("read fulfillment amount %q: %w", text, err)
			}
			fulfillment.CreatedAt = fulfillment.CreatedAt.UTC()
			out.Fulfillments = append(out.Fulfillments, fulfillment)
		}
		return rows.Err()
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return model.Commitment{}, err
		}
		return model.Commitment{}, fmt.Errorf("get commitment: %w", err)
	}
	return out, nil
}

// translateCommitmentError maps the database failures of commitments and
// fulfillments to typed errors, first by constraint name and then by code, and
// leaves any other error wrapped but untyped. An error that is already typed
// passes through, and so do the errors of a commit: errors.As crosses the
// wrapper. A restrict_violation from a trigger is not typed: reaching it is a
// bug of the caller code, and a serialization failure or deadlock (40001,
// 40P01) is left for the caller to retry.
func translateCommitmentError(operation string, err error) error {
	if _, typed := model.CodeOf(err); typed {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "commitments_debtor_fkey", "commitments_creditor_fkey", "commitments_created_by_fkey", "commitments_resource_fkey":
			return model.NewError(model.CodeRuleViolation, "the parties and the resource of a commitment must exist in this space", err)
		case "fulfillments_commitment_fkey", "fulfillments_entry_fkey":
			return model.NewError(model.CodeRuleViolation, "the commitment and the entry of a fulfillment must exist in this space and share the resource", err)
		case "fulfillments_commitment_open":
			return model.NewError(model.CodeRuleViolation, "only an open commitment can be fulfilled", err)
		case "fulfillments_within_pending":
			return model.NewError(model.CodeRuleViolation, "the fulfillment exceeds the pending amount of the commitment", err)
		case "fulfillments_entry_creditor":
			return model.NewError(model.CodeRuleViolation, "a fulfillment needs a positive entry in an account of the creditor", err)
		case "fulfillments_entry_reversal":
			return model.NewError(model.CodeRuleViolation, "an entry of a reversal cannot fulfill a commitment", err)
		case "fulfillments_amount_entry":
			return model.NewError(model.CodeRuleViolation, "a fulfillment applies its entry in full", err)
		case "fulfillments_entry_key":
			return model.NewError(model.CodeRuleViolation, "the entry is already applied to a commitment", err)
		case "commitments_space_id_fkey", "fulfillments_space_id_fkey":
			return model.NewError(model.CodeNotFound, "the space does not exist", err)
		case "commitments_reschedule_past":
			return model.NewError(model.CodeRuleViolation, "a commitment can only be rescheduled to a time in the future", err)
		case "timers_one_scheduled_key", "timers_commitment_fkey":
			return model.NewError(model.CodeRuleViolation, "the commitment must have exactly one scheduled timer in this space", err)
		case "commitments_amount_scale":
			return model.NewError(model.CodeInvalidInput, "the amount has more decimals than the scale of its resource", err)
		}
		switch pgErr.Code {
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "commitment data violates a constraint", err)
		case "23502":
			// A null space means the transaction has no current space; a null
			// anywhere else is a value the caller left out.
			if pgErr.ColumnName == "space_id" {
				return model.NewError(model.CodeRuleViolation, "the transaction has no current space to write commitments in", err)
			}
			return model.NewError(model.CodeInvalidInput, "a required commitment value is missing", err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
