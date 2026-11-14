package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// maxAuditListLimit bounds a single ListAccessAudit call.
const maxAuditListLimit = 1000

// RecordAccessAudit appends an audit entry about spaceID and returns its UUID
// v7. Only the system actor may call it until permissions arrive. Entries are
// append-only: there is no operation to change or remove one.
//
// The actor lives in NewAccessAudit.ActorSpaceID, usually a platform space,
// while the entry belongs to the audited space spaceID. A reason and a request
// id are mandatory and not blank. An actor, or a grant, that does not exist
// where it says is rule_violation.
func RecordAccessAudit(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in model.NewAccessAudit) (string, error) {
	if actor.Kind != model.ActorSystem {
		return "", model.NewError(model.CodePermissionDenied, "only the system actor records access audit entries", nil)
	}
	if err := validateNewAccessAudit(in); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate audit id: %w", err)
	}

	err = InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.access_audit
			   (id, space_id, actor_participant_id, actor_space_id, action, subject_ref, reason, request_id, grant_id)
			 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5, $6, $7, $8)`,
			id.String(), in.ActorParticipantID, in.ActorSpaceID, string(in.Action),
			nullIfEmpty(in.SubjectRef), in.Reason, in.RequestID, nullIfEmpty(in.GrantID))
		return err
	})
	if err != nil {
		return "", translateGrantError("record access audit", err)
	}
	return id.String(), nil
}

// ListAccessAudit returns up to limit audit entries of spaceID, oldest first.
// The limit must be between 1 and 1000.
func ListAccessAudit(ctx context.Context, pool *sql.DB, spaceID string, limit int) ([]model.AccessAudit, error) {
	if limit < 1 || limit > maxAuditListLimit {
		return nil, model.NewError(model.CodeInvalidInput,
			fmt.Sprintf("limit must be between 1 and %d", maxAuditListLimit), nil)
	}
	var out []model.AccessAudit
	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT id::text, space_id::text, actor_participant_id::text, actor_space_id::text, action,
			        subject_ref, reason, request_id, grant_id::text, occurred_at
			 FROM kernel.access_audit ORDER BY occurred_at, id LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var entry model.AccessAudit
			var action string
			var subject, grant sql.NullString
			if err := rows.Scan(&entry.ID, &entry.SpaceID, &entry.ActorParticipantID, &entry.ActorSpaceID,
				&action, &subject, &entry.Reason, &entry.RequestID, &grant, &entry.OccurredAt); err != nil {
				return err
			}
			entry.Action = model.AccessAction(action)
			entry.SubjectRef = subject.String
			entry.GrantID = grant.String
			entry.OccurredAt = entry.OccurredAt.UTC()
			out = append(out, entry)
		}
		return rows.Err()
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return nil, err
		}
		return nil, fmt.Errorf("list access audit: %w", err)
	}
	return out, nil
}

// validateNewAccessAudit checks the input before touching the database.
func validateNewAccessAudit(in model.NewAccessAudit) error {
	if err := requireID("actor participant id", in.ActorParticipantID); err != nil {
		return err
	}
	if err := requireID("actor space id", in.ActorSpaceID); err != nil {
		return err
	}
	if !in.Action.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown access action %q", in.Action), nil)
	}
	if strings.TrimSpace(in.Reason) == "" {
		return model.NewError(model.CodeInvalidInput, "an access audit entry needs a reason", nil)
	}
	if strings.TrimSpace(in.RequestID) == "" {
		return model.NewError(model.CodeInvalidInput, "an access audit entry needs a request id", nil)
	}
	if in.SubjectRef != "" && strings.TrimSpace(in.SubjectRef) == "" {
		return model.NewError(model.CodeInvalidInput, "the subject reference must not be blank", nil)
	}
	if in.GrantID != "" {
		return requireID("grant id", in.GrantID)
	}
	return nil
}
