package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// The Check* functions below are the escalation rules of ADR-0029. Each runs
// the permission check first and then the rule that bounds what the actor may
// hand out. They only read: the operation that follows writes, in the same
// transaction, which keeps the locks of Check in force until it commits.

// CheckRoleCreatable verifies that actor may create the role in the current
// space. It needs kernel.role.write; every permission of the role must be in
// the catalog (invalid_input) and held by the actor, so nobody creates a role
// that reaches further than itself; and only the system actor creates a role
// with System set (rule 3). The platform-only key is refused to a person by
// the subset rule and to everyone else by the database.
func (a *Authorizer) CheckRoleCreatable(ctx context.Context, tx *sql.Tx, actor model.Actor, in model.NewRole) error {
	decided, err := a.authorize(ctx, tx, actor, Requirement{Permission: model.PermissionRoleWrite})
	if err != nil {
		return err
	}
	if err := a.catalog.ValidateList(in.Permissions); err != nil {
		return err
	}
	if in.System && actor.Kind != model.ActorSystem {
		return denied()
	}
	if !decided.held.covers(in.Permissions) {
		return denied()
	}
	return nil
}

// CheckGrantable verifies that actor may create the grant in the current
// space. It needs kernel.grant.write. The role must be visible in the space
// (not_found otherwise) and the permissions of the role plus the extra ones
// must be held by the actor. The actor never grants to itself, and GrantedBy
// must be nil or the actor: the attribution is not claimed apart from the
// actor. The system actor keeps the last two rules.
func (a *Authorizer) CheckGrantable(ctx context.Context, tx *sql.Tx, actor model.Actor, in model.NewGrant) error {
	decided, err := a.authorize(ctx, tx, actor, Requirement{Permission: model.PermissionGrantWrite})
	if err != nil {
		return err
	}
	if !validID(in.RoleID) || !validID(in.ParticipantID) {
		return model.NewError(model.CodeInvalidInput, "participant id and role id must be non-nil UUIDs", nil)
	}
	if err := a.catalog.ValidateList(in.ExtraPermissions); err != nil {
		return err
	}
	var roleList string
	err = tx.QueryRowContext(ctx,
		`SELECT array_to_json(permissions)::text FROM kernel.roles WHERE id = $1 AND space_id = kernel.current_space_id()`,
		in.RoleID).Scan(&roleList)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NewError(model.CodeNotFound, "role not found in this space", nil)
	}
	if err != nil {
		return fmt.Errorf("read role: %w", err)
	}
	rolePermissions, err := decodeList(roleList)
	if err != nil {
		return err
	}

	if sameParticipant(actor.ParticipantID, in.ParticipantID) {
		return denied()
	}
	if in.GrantedBy != nil && !(sameParticipant(actor.ParticipantID, in.GrantedBy.ParticipantID) &&
		sameParticipant(decided.spaceID, in.GrantedBy.SpaceID)) {
		return denied()
	}
	if !decided.held.covers(rolePermissions) || !decided.held.covers(in.ExtraPermissions) {
		return denied()
	}
	return nil
}

// CheckRevocable verifies that actor may revoke the grant of the current
// space: it needs kernel.grant.write and must hold every permission the grant
// gives, so whoever could not grant it cannot revoke it (rule 5). A grant the
// space cannot see is not_found.
func (a *Authorizer) CheckRevocable(ctx context.Context, tx *sql.Tx, actor model.Actor, grantID string) error {
	decided, err := a.authorize(ctx, tx, actor, Requirement{Permission: model.PermissionGrantWrite})
	if err != nil {
		return err
	}
	if !validID(grantID) {
		return model.NewError(model.CodeInvalidInput, "grant id must be a non-nil UUID", nil)
	}
	var roleList, extraList string
	err = tx.QueryRowContext(ctx,
		`SELECT array_to_json(r.permissions)::text, array_to_json(g.extra_permissions)::text
		 FROM kernel.grants g JOIN kernel.roles r ON r.id = g.role_id AND r.space_id = g.space_id
		 WHERE g.id = $1 AND g.space_id = kernel.current_space_id()`, grantID).Scan(&roleList, &extraList)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NewError(model.CodeNotFound, "grant not found in this space", nil)
	}
	if err != nil {
		return fmt.Errorf("read grant: %w", err)
	}
	for _, list := range []string{roleList, extraList} {
		keys, err := decodeList(list)
		if err != nil {
			return err
		}
		if !decided.held.covers(keys) {
			return denied()
		}
	}
	return nil
}

// CheckCapabilityEnable verifies that actor may enable the capability in the
// current space. The system actor enables any source. Anyone else needs
// kernel.capability.write, which only a platform space can hold, and may enable
// only the admin source naming itself as the enabler: package, ai and code
// belong to the kernel, and a role of a space never enables a capability
// (rule 9).
func (a *Authorizer) CheckCapabilityEnable(ctx context.Context, tx *sql.Tx, actor model.Actor, in model.NewSpaceCapability) error {
	decided, err := a.authorize(ctx, tx, actor, Requirement{Permission: model.PermissionCapabilityWrite})
	if err != nil {
		return err
	}
	if !model.ValidPermissionKey(in.CapabilityKey) {
		return model.NewError(model.CodeInvalidInput, "capability key must have two or more lowercase segments separated by dots", nil)
	}
	if !in.Source.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown capability source %q", in.Source), nil)
	}
	if actor.Kind == model.ActorSystem {
		return nil
	}
	if in.Source != model.CapabilitySourceAdmin {
		return denied()
	}
	if in.EnabledBy != nil && !(sameParticipant(actor.ParticipantID, in.EnabledBy.ParticipantID) &&
		sameParticipant(decided.spaceID, in.EnabledBy.SpaceID)) {
		return denied()
	}
	return nil
}

// validID reports whether id is a UUID that is not nil.
func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil
}

// sameParticipant reports whether two ids are the same UUID, whatever their
// letter case. An empty or malformed id is never the same as another.
func sameParticipant(a, b string) bool {
	left, err := uuid.Parse(a)
	if err != nil {
		return false
	}
	right, err := uuid.Parse(b)
	return err == nil && left == right
}

// decodeList decodes the JSON array of a permission list.
func decodeList(raw string) ([]string, error) {
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("decode permissions: %w", err)
	}
	return keys, nil
}
