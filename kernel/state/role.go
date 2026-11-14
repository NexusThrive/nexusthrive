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

// CreateRole creates a role in spaceID and returns its UUID v7. Only the
// system actor may call it until permissions arrive.
//
// The space of the row is the space of the transaction, never a value from the
// caller. A key already used in the space is rule_violation; a space that does
// not exist is not_found.
func CreateRole(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in model.NewRole) (string, error) {
	if actor.Kind != model.ActorSystem {
		return "", model.NewError(model.CodePermissionDenied, "only the system actor creates roles", nil)
	}
	if err := validateNewRole(in); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate role id: %w", err)
	}

	err = InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.roles (id, space_id, key, name, permissions, system)
			 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5)`,
			id.String(), in.Key, in.Name, nonNilList(in.Permissions), in.System)
		return err
	})
	if err != nil {
		return "", translateGrantError("create role", err)
	}
	return id.String(), nil
}

// validateNewRole checks the input before touching the database.
func validateNewRole(in model.NewRole) error {
	if !model.ValidRoleKey(in.Key) {
		return model.NewError(model.CodeInvalidInput, "role key must be lowercase letters, digits and underscores, starting with a letter, up to 64 characters", nil)
	}
	if strings.TrimSpace(in.Name) == "" {
		return model.NewError(model.CodeInvalidInput, "role name must not be empty", nil)
	}
	return validatePermissions("role permissions", in.Permissions)
}

// validatePermissions reports invalid_input unless list is a valid permission
// list. The label names the field in the message.
func validatePermissions(label string, list []string) error {
	if !model.ValidPermissionList(list) {
		return model.NewError(model.CodeInvalidInput,
			fmt.Sprintf("%s must hold at most %d keys of two or more lowercase segments separated by dots", label, model.MaxPermissions), nil)
	}
	return nil
}

// nonNilList returns list, or an empty list for nil, so that the database
// receives an empty array and not a null.
func nonNilList(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// translateGrantError maps database constraint failures of roles, grants,
// capabilities and audit entries to typed errors and leaves any other error
// wrapped but untyped. A reference that is missing, from another space or of
// the wrong kind is a rule violation, because the database check of a foreign
// key ignores row level security; only a missing space is not_found.
func translateGrantError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return model.NewError(model.CodeRuleViolation, "a role key, an active grant or a capability is already in use", err)
		case "23001":
			// restrict_violation: the database refuses a permission or a write
			// that the space may not hold, such as kernel.capability.write
			// outside a platform space. Same precedent as translateSpaceError.
			return model.NewError(model.CodePermissionDenied, "the space may not hold this permission or perform this write", err)
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "data violates a constraint", err)
		case "23503":
			if strings.HasSuffix(pgErr.ConstraintName, "_space_id_fkey") {
				return model.NewError(model.CodeNotFound, "space does not exist", err)
			}
			return model.NewError(model.CodeRuleViolation,
				"a referenced participant, role, space or grant does not exist in this space or does not match", err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
