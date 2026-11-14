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

// maxSlugLength keeps a slug usable as a DNS label (ADR-0023).
const maxSlugLength = 63

// slugPattern allows lowercase letters, digits and single hyphens between them.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// NewSpace is the input of CreateSpace.
type NewSpace struct {
	Kind model.SpaceKind
	Name string
	Slug string
}

// CreateSpace creates a space in status provisioning and returns its UUID v7.
// It is the internal bootstrap operation: only the system actor may call it,
// because public creation of spaces belongs to the platform layer.
//
// A space of kind platform is provisioned at bootstrap with the administrator
// connection; through the API role it is permission_denied (ADR-0029).
//
// The id is generated here so the transaction can run as the new space; the
// row then satisfies its own isolation policy.
func CreateSpace(ctx context.Context, pool *sql.DB, actor model.Actor, in NewSpace) (string, error) {
	if actor.Kind != model.ActorSystem {
		return "", model.NewError(model.CodePermissionDenied, "only the system actor creates spaces", nil)
	}
	if err := validateNewSpace(in); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate space id: %w", err)
	}

	err = InSpace(ctx, pool, id.String(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, $2, $3, $4)`,
			id.String(), string(in.Kind), in.Name, in.Slug)
		return err
	})
	if err != nil {
		return "", translateSpaceError(err)
	}
	return id.String(), nil
}

// validateNewSpace checks the input before touching the database.
func validateNewSpace(in NewSpace) error {
	if !in.Kind.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown space kind %q", in.Kind), nil)
	}
	if strings.TrimSpace(in.Name) == "" {
		return model.NewError(model.CodeInvalidInput, "space name must not be empty", nil)
	}
	if len(in.Slug) > maxSlugLength || !slugPattern.MatchString(in.Slug) {
		return model.NewError(model.CodeInvalidInput, "space slug must be lowercase letters, digits and hyphens, at most 63 characters", nil)
	}
	return nil
}

// translateSpaceError maps database constraint failures to typed errors and
// leaves any other error wrapped but untyped.
func translateSpaceError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23001":
			return model.NewError(model.CodePermissionDenied, "platform spaces are provisioned at bootstrap, not through the API role", err)
		case "23505":
			return model.NewError(model.CodeRuleViolation, "space slug is already taken", err)
		case "23514", "22P02":
			return model.NewError(model.CodeInvalidInput, "space data violates a constraint", err)
		}
	}
	return fmt.Errorf("create space: %w", err)
}
