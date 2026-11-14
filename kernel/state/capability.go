package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// EnableCapability records an active capability in spaceID. Only the system
// actor may call it until permissions arrive; the capability is never enabled
// by a role of the space (rule 9, ADR-0029).
//
// A pair (space, key) already recorded is rule_violation. A source that is not
// package, ai, admin or code is invalid_input, and so is an admin source with
// no EnabledBy. An enabler must live in spaceID or in a platform space.
// Suspending or reactivating a capability is another story.
func EnableCapability(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in model.NewSpaceCapability) error {
	if actor.Kind != model.ActorSystem {
		return model.NewError(model.CodePermissionDenied, "only the system actor enables capabilities", nil)
	}
	if err := validateNewSpaceCapability(in); err != nil {
		return err
	}
	var enabledBy, enabledBySpace, enabledByKind any
	if in.EnabledBy != nil {
		kind, err := spaceKind(ctx, pool, in.EnabledBy.SpaceID)
		if err != nil {
			return err
		}
		enabledBy, enabledBySpace, enabledByKind = in.EnabledBy.ParticipantID, in.EnabledBy.SpaceID, kind
	}

	err := InSpace(ctx, pool, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kernel.space_capabilities
			   (space_id, capability_key, source, enabled_by, enabled_by_space_id, enabled_by_space_kind, reason)
			 VALUES (kernel.current_space_id(), $1, $2, $3, $4, $5, $6)`,
			in.CapabilityKey, string(in.Source), enabledBy, enabledBySpace, enabledByKind, nullIfEmpty(in.Reason))
		return err
	})
	if err != nil {
		return translateGrantError("enable capability", err)
	}
	return nil
}

// validateNewSpaceCapability checks the input before touching the database.
func validateNewSpaceCapability(in model.NewSpaceCapability) error {
	if !model.ValidPermissionKey(in.CapabilityKey) {
		return model.NewError(model.CodeInvalidInput, "capability key must have two or more lowercase segments separated by dots", nil)
	}
	if !in.Source.Valid() {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("unknown capability source %q", in.Source), nil)
	}
	if in.Reason != "" && strings.TrimSpace(in.Reason) == "" {
		return model.NewError(model.CodeInvalidInput, "capability reason must not be blank", nil)
	}
	if in.EnabledBy == nil {
		if in.Source == model.CapabilitySourceAdmin {
			return model.NewError(model.CodeInvalidInput, "an admin capability needs the participant that enabled it", nil)
		}
		return nil
	}
	if err := requireID("enabler participant id", in.EnabledBy.ParticipantID); err != nil {
		return err
	}
	return requireID("enabler space id", in.EnabledBy.SpaceID)
}
