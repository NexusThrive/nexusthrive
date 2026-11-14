// Package capability decides whether an actor may perform an operation: it
// checks the permission, the state of the space and the capability the
// operation declares, inside the transaction that already fixed the space
// (ADR-0032). It also holds the escalation checks of ADR-0029. It imports only
// the kernel model and database/sql: reading roles and grants goes through the
// same row level security as the operation itself.
package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// Requirement is what an operation declares before it runs: the permission it
// needs and, optionally, the capability the space must have active.
type Requirement struct {
	Permission string
	Capability string
}

// Authorizer verifies requirements against a catalog and a clock. It has no
// mutable state and is safe for concurrent use.
type Authorizer struct {
	catalog model.Catalog
	clock   model.Clock
}

// NewAuthorizer builds an Authorizer. A nil clock or an empty catalog is
// invalid_input: an authorizer that knows no key or no time would deny or
// allow by accident.
func NewAuthorizer(catalog model.Catalog, clock model.Clock) (*Authorizer, error) {
	if clock == nil {
		return nil, model.NewError(model.CodeInvalidInput, "an authorizer needs a clock", nil)
	}
	if len(catalog.Keys()) == 0 {
		return nil, model.NewError(model.CodeInvalidInput, "an authorizer needs a catalog with keys", nil)
	}
	return &Authorizer{catalog: catalog, clock: clock}, nil
}

// denied is the single refusal of a missing permission. Its message is the
// same for every cause, so a caller cannot tell an expired grant from a
// revoked one, an archived participant or a space it cannot see.
func denied() error {
	return model.NewError(model.CodePermissionDenied, "permission denied", nil)
}

// granted is the set of permissions an actor holds in the current space. The
// system actor holds all of them.
type granted struct {
	all  bool
	keys map[string]struct{}
}

// covers reports whether every key is held.
func (g granted) covers(keys []string) bool {
	if g.all {
		return true
	}
	for _, key := range keys {
		if _, ok := g.keys[key]; !ok {
			return false
		}
	}
	return true
}

// decision is what a successful authorization learned, for the escalation
// checks that need more than a yes.
type decision struct {
	spaceID string
	held    granted
}

// Check verifies req for actor inside tx, whose current space is the space of
// the operation. It returns nil when the operation may run.
//
// The order stops at the first failure: a null or unresolvable actor is
// permission_denied, whatever the key; a key outside the catalog, or a
// malformed capability key, is invalid_input; no active grant for the
// permission is permission_denied; a suspended or closed space with a write or
// money permission is space_suspended; a declared capability that is not
// active is capability_inactive. Permission comes before state so that a caller
// with no right learns nothing about the space. The system actor skips only
// the grant. For write and money permissions the grants read and the space row
// are locked with FOR SHARE until the transaction ends, so a revocation or a
// suspension that races with the operation waits for it.
func (a *Authorizer) Check(ctx context.Context, tx *sql.Tx, actor model.Actor, req Requirement) error {
	_, err := a.authorize(ctx, tx, actor, req)
	return err
}

// authorize is Check returning what it learned.
func (a *Authorizer) authorize(ctx context.Context, tx *sql.Tx, actor model.Actor, req Requirement) (decision, error) {
	if actor.IsNil() {
		return decision{}, denied()
	}
	if actor.ParticipantID != "" {
		if parsed, err := uuid.Parse(actor.ParticipantID); err != nil || parsed == uuid.Nil {
			return decision{}, denied()
		}
	}

	spaceID, _, err := a.readSpace(ctx, tx, false)
	if err != nil {
		return decision{}, err
	}
	if actor.Kind != model.ActorSystem {
		if err := a.requireActiveParticipant(ctx, tx, actor.ParticipantID); err != nil {
			return decision{}, err
		}
	}

	def, known := a.catalog.Lookup(req.Permission)
	if !known {
		return decision{}, model.NewError(model.CodeInvalidInput,
			fmt.Sprintf("permission %q is not in the catalog", req.Permission), nil)
	}
	if req.Capability != "" && !model.ValidPermissionKey(req.Capability) {
		return decision{}, model.NewError(model.CodeInvalidInput, "capability key must have two or more lowercase segments separated by dots", nil)
	}

	lock := def.Risk != model.RiskRead
	held := granted{all: true}
	if actor.Kind != model.ActorSystem {
		held, err = a.loadGranted(ctx, tx, actor.ParticipantID, lock)
		if err != nil {
			return decision{}, err
		}
		if !held.covers([]string{req.Permission}) {
			return decision{}, denied()
		}
	}

	if lock {
		_, status, err := a.readSpace(ctx, tx, true)
		if err != nil {
			return decision{}, err
		}
		if status == string(model.SpaceStatusSuspended) || status == string(model.SpaceStatusClosed) {
			return decision{}, model.NewError(model.CodeSpaceSuspended, "the space is "+status, nil)
		}
	}

	if req.Capability != "" {
		if err := a.requireCapability(ctx, tx, req.Capability); err != nil {
			return decision{}, err
		}
	}
	return decision{spaceID: spaceID, held: held}, nil
}

// readSpace returns the id and the status of the current space. No fixed space,
// or a space that does not exist, is permission_denied: nothing is allowed
// outside a space. With lock the row is held with FOR SHARE.
func (a *Authorizer) readSpace(ctx context.Context, tx *sql.Tx, lock bool) (id, status string, err error) {
	query := `SELECT id::text, status FROM kernel.spaces WHERE id = kernel.current_space_id()`
	if lock {
		query += ` FOR SHARE`
	}
	err = tx.QueryRowContext(ctx, query).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", denied()
	}
	if err != nil {
		return "", "", fmt.Errorf("read space: %w", err)
	}
	return id, status, nil
}

// requireActiveParticipant is permission_denied unless the participant exists
// in the current space and is active: an archived participant loses its
// permissions.
func (a *Authorizer) requireActiveParticipant(ctx context.Context, tx *sql.Tx, participantID string) error {
	var status string
	err := tx.QueryRowContext(ctx,
		`SELECT status FROM kernel.participants WHERE id = $1 AND space_id = kernel.current_space_id()`,
		participantID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return denied()
	}
	if err != nil {
		return fmt.Errorf("read participant: %w", err)
	}
	if status != string(model.ParticipantStatusActive) {
		return denied()
	}
	return nil
}

// loadGranted returns the permissions the participant holds through its grants
// that count at clock.Now(). Every grant of the participant is read, because
// model.Grant.IsActive is the one place that decides what counts; the unique
// index of active grants bounds the live ones. Keys are compared in Go and
// never concatenated into SQL. With lock the grants are held with FOR SHARE.
func (a *Authorizer) loadGranted(ctx context.Context, tx *sql.Tx, participantID string, lock bool) (granted, error) {
	query := `SELECT g.expires_at, g.revoked_at, array_to_json(r.permissions)::text, array_to_json(g.extra_permissions)::text
		FROM kernel.grants g
		JOIN kernel.roles r ON r.id = g.role_id AND r.space_id = g.space_id
		WHERE g.participant_id = $1 AND g.space_id = kernel.current_space_id()`
	if lock {
		query += ` FOR SHARE OF g`
	}
	rows, err := tx.QueryContext(ctx, query, participantID)
	if err != nil {
		return granted{}, fmt.Errorf("read grants: %w", err)
	}
	defer rows.Close()

	now := a.clock.Now()
	out := granted{keys: map[string]struct{}{}}
	for rows.Next() {
		var expiresAt, revokedAt sql.NullTime
		var rolePermissions, extraPermissions string
		if err := rows.Scan(&expiresAt, &revokedAt, &rolePermissions, &extraPermissions); err != nil {
			return granted{}, fmt.Errorf("scan grant: %w", err)
		}
		grant := model.Grant{ExpiresAt: timeOrNil(expiresAt), RevokedAt: timeOrNil(revokedAt)}
		if !grant.IsActive(now) {
			continue
		}
		for _, list := range []string{rolePermissions, extraPermissions} {
			var keys []string
			if err := json.Unmarshal([]byte(list), &keys); err != nil {
				return granted{}, fmt.Errorf("decode permissions: %w", err)
			}
			for _, key := range keys {
				out.keys[key] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return granted{}, fmt.Errorf("read grants: %w", err)
	}
	return out, nil
}

// timeOrNil converts a nullable database time to a pointer.
func timeOrNil(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// requireCapability is capability_inactive unless the current space has the
// capability recorded and active.
func (a *Authorizer) requireCapability(ctx context.Context, tx *sql.Tx, key string) error {
	var status string
	err := tx.QueryRowContext(ctx,
		`SELECT status FROM kernel.space_capabilities WHERE space_id = kernel.current_space_id() AND capability_key = $1`,
		key).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status != "active") {
		return model.NewError(model.CodeCapabilityInactive, fmt.Sprintf("capability %q is not active in this space", key), nil)
	}
	if err != nil {
		return fmt.Errorf("read capability: %w", err)
	}
	return nil
}
