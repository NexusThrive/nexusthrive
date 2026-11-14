package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// countryCodePattern accepts a two-letter uppercase country code.
var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)

// NewParticipant is the input of CreateParticipant. Empty optional fields are
// stored as null. A document type and a document number go together.
type NewParticipant struct {
	Kind           model.ParticipantKind
	IdentityID     string
	DisplayName    string
	LegalName      string
	DocumentType   model.DocumentType
	DocumentNumber string
	CountryCode    string
}

// CreateParticipant creates an active participant in spaceID and returns its
// UUID v7. Only the system actor may call it until permissions arrive.
//
// The space of the row is the space of the transaction, never a value from the
// caller. Linking an identity makes it visible to the space from then on; the
// foreign key check ignores row level security, so the identity need not be
// visible beforehand.
func CreateParticipant(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in NewParticipant) (string, error) {
	if actor.Kind != model.ActorSystem {
		return "", model.NewError(model.CodePermissionDenied, "only the system actor creates participants", nil)
	}
	if err := validateNewParticipant(in); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate participant id: %w", err)
	}

	err = InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.participants
			   (id, space_id, identity_id, kind, display_name, legal_name, document_type, document_number, country_code)
			 VALUES ($1, kernel.current_space_id(), $2, $3, $4, $5, $6, $7, $8)`,
			id.String(), nullIfEmpty(in.IdentityID), string(in.Kind), in.DisplayName,
			nullIfEmpty(in.LegalName), nullIfEmpty(string(in.DocumentType)),
			nullIfEmpty(in.DocumentNumber), nullIfEmpty(in.CountryCode))
		return err
	})
	if err != nil {
		return "", translateParticipantError(err)
	}
	return id.String(), nil
}

// ArchiveParticipant sets a participant of spaceID to archived and stamps
// archived_at. The row stays readable. Archiving an archived participant
// succeeds and leaves archived_at unchanged. An id that does not exist, or
// belongs to another space, is not_found. Only the system actor may call it
// until permissions arrive.
func ArchiveParticipant(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, participantID string) error {
	if actor.Kind != model.ActorSystem {
		return model.NewError(model.CodePermissionDenied, "only the system actor archives participants", nil)
	}
	if parsed, err := uuid.Parse(participantID); err != nil || parsed == uuid.Nil {
		return model.NewError(model.CodeInvalidInput, "participant id must be a non-nil UUID", nil)
	}

	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE kernel.participants SET status = 'archived', archived_at = now()
			 WHERE id = $1 AND status = 'active'`, participantID)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected > 0 {
			return err
		}
		// Nothing changed: either the participant is already archived, which is
		// fine, or the current space cannot see it.
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM kernel.participants WHERE id = $1)`, participantID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return model.NewError(model.CodeNotFound, "participant not found in this space", nil)
		}
		return nil
	})
	if err != nil {
		if _, typed := model.CodeOf(err); typed {
			return err
		}
		return fmt.Errorf("archive participant: %w", err)
	}
	return nil
}

// validateNewParticipant checks the input before touching the database.
func validateNewParticipant(in NewParticipant) error {
	if !in.Kind.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown participant kind %q", in.Kind), nil)
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		return model.NewError(model.CodeInvalidInput, "participant display name must not be empty", nil)
	}
	if in.IdentityID != "" {
		if parsed, err := uuid.Parse(in.IdentityID); err != nil || parsed == uuid.Nil {
			return model.NewError(model.CodeInvalidInput, "identity id must be empty or a non-nil UUID", nil)
		}
	}
	if (in.DocumentType == "") != (in.DocumentNumber == "") {
		return model.NewError(model.CodeInvalidInput, "document type and document number go together", nil)
	}
	if in.DocumentType != "" && !in.DocumentType.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown document type %q", in.DocumentType), nil)
	}
	if in.CountryCode != "" && !countryCodePattern.MatchString(in.CountryCode) {
		return model.NewError(model.CodeInvalidInput, "country code must be two uppercase letters", nil)
	}
	return nil
}

// translateParticipantError maps database constraint failures to typed errors
// and leaves any other error wrapped but untyped.
func translateParticipantError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return model.NewError(model.CodeRuleViolation, "identity already has a participant in this space", err)
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "participant data violates a constraint", err)
		case "23503":
			switch pgErr.ConstraintName {
			case "participants_identity_id_fkey":
				return model.NewError(model.CodeInvalidInput, "identity does not exist", err)
			case "participants_space_id_fkey":
				return model.NewError(model.CodeNotFound, "space does not exist", err)
			}
		}
	}
	return fmt.Errorf("create participant: %w", err)
}
