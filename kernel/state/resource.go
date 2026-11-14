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

// NewResource is the input of CreateResource.
type NewResource struct {
	Kind  model.ResourceKind
	Code  string
	Name  string
	Scale int
}

// CreateResource creates a resource in spaceID and returns its UUID v7. Only
// the system actor may call it until permissions arrive.
//
// The space of the row is the space of the transaction, never a value from the
// caller. A code already used in the space is rule_violation; a space that
// does not exist is not_found.
func CreateResource(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in NewResource) (string, error) {
	if actor.Kind != model.ActorSystem {
		return "", model.NewError(model.CodePermissionDenied, "only the system actor creates resources", nil)
	}
	if err := validateNewResource(in); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate resource id: %w", err)
	}

	err = InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.resources (id, space_id, kind, code, name, scale)
			 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5)`,
			id.String(), string(in.Kind), in.Code, in.Name, in.Scale)
		return err
	})
	if err != nil {
		return "", translateLedgerError("create resource", err)
	}
	return id.String(), nil
}

// GetResource returns a resource of spaceID. An id that does not exist, or
// belongs to another space, is not_found.
func GetResource(ctx context.Context, pool *sql.DB, spaceID, resourceID string) (model.Resource, error) {
	if err := requireID("resource id", resourceID); err != nil {
		return model.Resource{}, err
	}
	var out model.Resource
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var kind string
		err := tx.QueryRowContext(ctx,
			`SELECT id::text, space_id::text, kind, code, name, scale FROM kernel.resources WHERE id = $1`,
			resourceID).Scan(&out.ID, &out.SpaceID, &kind, &out.Code, &out.Name, &out.Scale)
		if errors.Is(err, sql.ErrNoRows) {
			return model.NewError(model.CodeNotFound, "resource not found in this space", nil)
		}
		out.Kind = model.ResourceKind(kind)
		return err
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return model.Resource{}, err
		}
		return model.Resource{}, fmt.Errorf("get resource: %w", err)
	}
	return out, nil
}

// validateNewResource checks the input before touching the database.
func validateNewResource(in NewResource) error {
	if !in.Kind.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown resource kind %q", in.Kind), nil)
	}
	if !model.ValidResourceCode(in.Code) {
		return model.NewError(model.CodeInvalidInput, "resource code must be uppercase letters, digits or underscores, starting with a letter, up to 32 characters", nil)
	}
	if strings.TrimSpace(in.Name) == "" {
		return model.NewError(model.CodeInvalidInput, "resource name must not be empty", nil)
	}
	if !model.ValidResourceScale(in.Scale) {
		return model.NewError(model.CodeInvalidInput,
			fmt.Sprintf("resource scale must be between %d and %d", model.MinResourceScale, model.MaxResourceScale), nil)
	}
	return nil
}

// requireID reports invalid_input unless id is a non-nil UUID. The label names
// the field in the message.
func requireID(label, id string) error {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		return model.NewError(model.CodeInvalidInput, label+" must be a non-nil UUID", nil)
	}
	return nil
}

// translateLedgerError maps database constraint failures of resources and
// accounts to typed errors and leaves any other error wrapped but untyped.
// The foreign keys that carry the space and the resource report a reference
// that is missing, from another space or of another resource as a rule
// violation, because the database check ignores row level security.
func translateLedgerError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return model.NewError(model.CodeRuleViolation, "the code is already in use", err)
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "data violates a constraint", err)
		case "23503":
			switch pgErr.ConstraintName {
			case "accounts_holder_fkey", "accounts_resource_fkey", "accounts_parent_fkey":
				return model.NewError(model.CodeRuleViolation,
					"holder, resource or parent account does not exist in this space or does not match the resource", err)
			case "resources_space_id_fkey", "accounts_space_id_fkey":
				return model.NewError(model.CodeNotFound, "space does not exist", err)
			}
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
