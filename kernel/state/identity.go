package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// NewIdentity is the input of CreateIdentity. PrimaryEmail is optional; an
// empty value is stored as null.
type NewIdentity struct {
	Provider     model.IdentityProvider
	Subject      string
	PrimaryEmail string
	DisplayName  string
}

// CreateIdentity creates an identity and returns its UUID v7. Only the system
// actor may call it until permissions arrive.
//
// It runs without a current space because an identity exists before any
// participant. The row is inserted without RETURNING or ON CONFLICT: both
// need the new row to pass the SELECT policy, and an identity with no
// participant does not. The id is generated here for that reason.
func CreateIdentity(ctx context.Context, pool *sql.DB, actor model.Actor, in NewIdentity) (string, error) {
	if actor.Kind != model.ActorSystem {
		return "", model.NewError(model.CodePermissionDenied, "only the system actor creates identities", nil)
	}
	if err := validateNewIdentity(in); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate identity id: %w", err)
	}

	err = inTransaction(ctx, pool, "", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.identities (id, provider, subject, primary_email, display_name)
			 VALUES ($1, $2, $3, $4, $5)`,
			id.String(), string(in.Provider), in.Subject, nullIfEmpty(in.PrimaryEmail), in.DisplayName)
		return err
	})
	if err != nil {
		return "", translateIdentityError(err)
	}
	return id.String(), nil
}

// validateNewIdentity checks the input before touching the database.
func validateNewIdentity(in NewIdentity) error {
	if !in.Provider.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown identity provider %q", in.Provider), nil)
	}
	if strings.TrimSpace(in.Subject) == "" {
		return model.NewError(model.CodeInvalidInput, "identity subject must not be empty", nil)
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		return model.NewError(model.CodeInvalidInput, "identity display name must not be empty", nil)
	}
	if in.PrimaryEmail != "" && strings.TrimSpace(in.PrimaryEmail) == "" {
		return model.NewError(model.CodeInvalidInput, "identity primary email must not be blank", nil)
	}
	return nil
}

// translateIdentityError maps database constraint failures to typed errors and
// leaves any other error wrapped but untyped.
func translateIdentityError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return model.NewError(model.CodeRuleViolation, "identity already exists for this provider and subject", err)
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "identity data violates a constraint", err)
		}
	}
	return fmt.Errorf("create identity: %w", err)
}

// nullIfEmpty turns an empty string into a SQL null, so optional text columns
// never hold an empty string.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
