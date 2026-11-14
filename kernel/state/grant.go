package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// GrantRole gives a participant a role in spaceID and returns the grant's UUID
// v7. Only the system actor may call it until permissions arrive.
//
// The participant and the role must belong to the space; otherwise it is
// rule_violation and nothing is written. A second active grant of the same
// role to the same participant is also rule_violation. The kernel never
// creates a grant that is already expired: an ExpiresAt that is not after
// clock.Now() is invalid_input.
//
// When the grantor lives in a platform space other than spaceID the grant is
// support access, and it needs a non-blank reason and an expiry (rule 10,
// ADR-0029); the same rule is a CHECK in the database, so it holds for any
// writer. A grantor never grants to itself.
func GrantRole(ctx context.Context, pool *sql.DB, clock model.Clock, actor model.Actor, spaceID string, in model.NewGrant) (string, error) {
	if actor.Kind != model.ActorSystem {
		return "", model.NewError(model.CodePermissionDenied, "only the system actor grants roles", nil)
	}
	if err := validateNewGrant(clock, in); err != nil {
		return "", err
	}
	var grantorKind string
	if in.GrantedBy != nil {
		kind, err := spaceKind(ctx, pool, in.GrantedBy.SpaceID)
		if err != nil {
			return "", err
		}
		grantorKind = kind
		if in.GrantedBy.SpaceID != spaceID && kind == string(model.SpaceKindPlatform) &&
			(strings.TrimSpace(in.Reason) == "" || in.ExpiresAt == nil) {
			return "", model.NewError(model.CodeInvalidInput, "support access needs a reason and an expiry", nil)
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate grant id: %w", err)
	}

	var grantedBy, grantedBySpace any
	if in.GrantedBy != nil {
		grantedBy, grantedBySpace = in.GrantedBy.ParticipantID, in.GrantedBy.SpaceID
	}
	var expiresAt any
	if in.ExpiresAt != nil {
		expiresAt = in.ExpiresAt.UTC()
	}
	err = InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.grants
			   (id, space_id, participant_id, role_id, extra_permissions, granted_by,
			    granted_by_space_id, granted_by_space_kind, reason, expires_at)
			 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5, $6, $7, $8, $9)`,
			id.String(), in.ParticipantID, in.RoleID, nonNilList(in.ExtraPermissions), grantedBy,
			grantedBySpace, nullIfEmpty(grantorKind), nullIfEmpty(in.Reason), expiresAt)
		return err
	})
	if err != nil {
		return "", translateGrantError("grant role", err)
	}
	return id.String(), nil
}

// validateNewGrant checks the input before touching the database.
func validateNewGrant(clock model.Clock, in model.NewGrant) error {
	if err := requireID("participant id", in.ParticipantID); err != nil {
		return err
	}
	if err := requireID("role id", in.RoleID); err != nil {
		return err
	}
	if err := validatePermissions("extra permissions", in.ExtraPermissions); err != nil {
		return err
	}
	if in.Reason != "" && strings.TrimSpace(in.Reason) == "" {
		return model.NewError(model.CodeInvalidInput, "grant reason must not be blank", nil)
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(clock.Now()) {
		return model.NewError(model.CodeInvalidInput, "a grant must expire in the future", nil)
	}
	if in.GrantedBy != nil {
		if err := requireID("grantor participant id", in.GrantedBy.ParticipantID); err != nil {
			return err
		}
		if err := requireID("grantor space id", in.GrantedBy.SpaceID); err != nil {
			return err
		}
		if in.GrantedBy.ParticipantID == in.ParticipantID {
			return model.NewError(model.CodeInvalidInput, "a participant cannot grant a role to itself", nil)
		}
	}
	return nil
}

// spaceKind returns the kind of the space spaceID. The space table is visible
// only from the space itself, so the read runs as that space; a grantor of
// another space is how support access reaches this code. A space that does
// not exist is rule_violation.
func spaceKind(ctx context.Context, pool *sql.DB, spaceID string) (string, error) {
	var kind string
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT kind FROM kernel.spaces WHERE id = $1`, spaceID).Scan(&kind)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", model.NewError(model.CodeRuleViolation, "the space of the participant does not exist", nil)
	}
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return "", err
		}
		return "", fmt.Errorf("read space kind: %w", err)
	}
	return kind, nil
}

// RevokeGrant revokes a grant of spaceID. The database clock stamps
// revoked_at; the caller cannot choose it. Revoking a grant that is already
// revoked is already_done and leaves revoked_at unchanged. An id that does
// not exist, or belongs to another space, is not_found. Only the system actor
// may call it until permissions arrive.
//
// Two concurrent calls are safe: the second UPDATE waits for the row lock,
// re-reads the row and finds nothing to change.
func RevokeGrant(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, grantID string) error {
	if actor.Kind != model.ActorSystem {
		return model.NewError(model.CodePermissionDenied, "only the system actor revokes grants", nil)
	}
	if err := requireID("grant id", grantID); err != nil {
		return err
	}

	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE kernel.grants SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, grantID)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected > 0 {
			return err
		}
		// Nothing changed: the grant is already revoked, or the current
		// space cannot see it.
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM kernel.grants WHERE id = $1)`, grantID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return model.NewError(model.CodeNotFound, "grant not found in this space", nil)
		}
		return model.NewError(model.CodeAlreadyDone, "the grant is already revoked", nil)
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return err
		}
		return fmt.Errorf("revoke grant: %w", err)
	}
	return nil
}

// GetGrant returns a grant of spaceID. An id that does not exist, or belongs
// to another space, is not_found.
func GetGrant(ctx context.Context, pool *sql.DB, spaceID, grantID string) (model.Grant, error) {
	if err := requireID("grant id", grantID); err != nil {
		return model.Grant{}, err
	}
	var out model.Grant
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var extra string
		var grantedBy, grantedBySpace, reason sql.NullString
		var expiresAt, revokedAt sql.NullTime
		err := tx.QueryRowContext(ctx,
			`SELECT id::text, space_id::text, participant_id::text, role_id::text,
			        array_to_json(extra_permissions)::text, granted_by::text, granted_by_space_id::text,
			        reason, expires_at, created_at, revoked_at
			 FROM kernel.grants WHERE id = $1`, grantID).
			Scan(&out.ID, &out.SpaceID, &out.ParticipantID, &out.RoleID, &extra, &grantedBy,
				&grantedBySpace, &reason, &expiresAt, &out.CreatedAt, &revokedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return model.NewError(model.CodeNotFound, "grant not found in this space", nil)
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(extra), &out.ExtraPermissions); err != nil {
			return fmt.Errorf("decode extra permissions: %w", err)
		}
		if grantedBy.Valid {
			out.GrantedBy = &model.Grantor{ParticipantID: grantedBy.String, SpaceID: grantedBySpace.String}
		}
		out.Reason = reason.String
		out.CreatedAt = out.CreatedAt.UTC()
		out.ExpiresAt = utcOrNil(expiresAt)
		out.RevokedAt = utcOrNil(revokedAt)
		return nil
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return model.Grant{}, err
		}
		return model.Grant{}, fmt.Errorf("get grant: %w", err)
	}
	return out, nil
}

// IsGrantActive reports whether a grant of spaceID counts at clock.Now(): not
// revoked and not expired. The clock is injected because the answer depends on
// time and a database function with now() cannot be frozen in tests. An id the
// space cannot see is not_found.
func IsGrantActive(ctx context.Context, pool *sql.DB, clock model.Clock, spaceID, grantID string) (bool, error) {
	grant, err := GetGrant(ctx, pool, spaceID, grantID)
	if err != nil {
		return false, err
	}
	return grant.IsActive(clock.Now()), nil
}

// utcOrNil converts a nullable database time to a UTC pointer.
func utcOrNil(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	at := t.Time.UTC()
	return &at
}
