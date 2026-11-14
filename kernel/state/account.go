package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// NewAccount is the input of OpenAccount. An empty ParentAccountID makes a
// top-level account.
type NewAccount struct {
	HolderParticipantID string
	ResourceID          string
	ParentAccountID     string
	Class               model.AccountClass
	Code                string
	Name                string
}

// OpenAccount opens an account in spaceID and returns its UUID v7. Every
// account is born open. Only the system actor may call it until permissions
// arrive.
//
// The holder, the resource and the parent must belong to the space, and the
// parent must hold the same resource; otherwise it is rule_violation. A code
// already used by the same holder is also rule_violation.
func OpenAccount(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in NewAccount) (string, error) {
	if actor.Kind != model.ActorSystem {
		return "", model.NewError(model.CodePermissionDenied, "only the system actor opens accounts", nil)
	}
	if err := validateNewAccount(in); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate account id: %w", err)
	}

	err = InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.accounts
			   (id, space_id, holder_participant_id, resource_id, parent_account_id, class, code, name)
			 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5, $6, $7)`,
			id.String(), in.HolderParticipantID, in.ResourceID, nullIfEmpty(in.ParentAccountID),
			string(in.Class), in.Code, in.Name)
		return err
	})
	if err != nil {
		return "", translateLedgerError("open account", err)
	}
	return id.String(), nil
}

// CloseAccount sets an account of spaceID to closed and stamps closed_at. A
// closed account never reopens. Closing a closed account succeeds and leaves
// closed_at unchanged. An id that does not exist, or belongs to another space,
// is not_found. Only the system actor may call it until permissions arrive.
//
// Refusing entries on a closed account belongs to the ledger story, which
// reads the status under a share lock so that a concurrent close and entry
// serialize.
func CloseAccount(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, accountID string) error {
	if actor.Kind != model.ActorSystem {
		return model.NewError(model.CodePermissionDenied, "only the system actor closes accounts", nil)
	}
	if err := requireID("account id", accountID); err != nil {
		return err
	}

	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE kernel.accounts SET status = 'closed', closed_at = now()
			 WHERE id = $1 AND status = 'open'`, accountID)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected > 0 {
			return err
		}
		// Nothing changed: either the account is already closed, which is
		// fine, or the current space cannot see it.
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM kernel.accounts WHERE id = $1)`, accountID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return model.NewError(model.CodeNotFound, "account not found in this space", nil)
		}
		return nil
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return err
		}
		return fmt.Errorf("close account: %w", err)
	}
	return nil
}

// GetAccount returns an account of spaceID. An id that does not exist, or
// belongs to another space, is not_found.
func GetAccount(ctx context.Context, pool *sql.DB, spaceID, accountID string) (model.Account, error) {
	if err := requireID("account id", accountID); err != nil {
		return model.Account{}, err
	}
	var out model.Account
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var class, status string
		var parent sql.NullString
		var closedAt sql.NullTime
		err := tx.QueryRowContext(ctx,
			`SELECT id::text, space_id::text, holder_participant_id::text, resource_id::text,
			        parent_account_id::text, class, code, name, status, closed_at
			 FROM kernel.accounts WHERE id = $1`, accountID).
			Scan(&out.ID, &out.SpaceID, &out.HolderParticipantID, &out.ResourceID,
				&parent, &class, &out.Code, &out.Name, &status, &closedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return model.NewError(model.CodeNotFound, "account not found in this space", nil)
		}
		if err != nil {
			return err
		}
		out.ParentAccountID = parent.String
		out.Class = model.AccountClass(class)
		out.Status = model.AccountStatus(status)
		if closedAt.Valid {
			at := closedAt.Time.UTC()
			out.ClosedAt = &at
		}
		return nil
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return model.Account{}, err
		}
		return model.Account{}, fmt.Errorf("get account: %w", err)
	}
	return out, nil
}

// validateNewAccount checks the input before touching the database.
func validateNewAccount(in NewAccount) error {
	if !in.Class.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown account class %q", in.Class), nil)
	}
	if strings.TrimSpace(in.Code) == "" {
		return model.NewError(model.CodeInvalidInput, "account code must not be empty", nil)
	}
	if strings.TrimSpace(in.Name) == "" {
		return model.NewError(model.CodeInvalidInput, "account name must not be empty", nil)
	}
	if err := requireID("holder participant id", in.HolderParticipantID); err != nil {
		return err
	}
	if err := requireID("resource id", in.ResourceID); err != nil {
		return err
	}
	if in.ParentAccountID != "" {
		if err := requireID("parent account id", in.ParentAccountID); err != nil {
			return err
		}
	}
	return nil
}
