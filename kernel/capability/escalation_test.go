package capability_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// escalationWorld has a delegate who may grant and revoke but holds only
// ledger.read and grant.write, a recipient, and roles of different reach.
type escalationWorld struct {
	*world
	delegate  model.Actor
	recipient model.Actor
	plain     model.Actor
	narrow    string // ledger.read
	wide      string // ledger.read and exchange.write
	foreign   string // a role of space B
}

func newEscalationWorld(t *testing.T) *escalationWorld {
	t.Helper()
	w := newWorld(t)
	e := &escalationWorld{world: w}
	e.delegate = w.holder(w.spaceA, "delegate", model.PermissionGrantWrite, model.PermissionLedgerRead)
	e.recipient = w.person(w.spaceA, "recipient")
	e.plain = w.holder(w.spaceA, "plain", model.PermissionLedgerRead)
	e.narrow = w.role(w.spaceA, "narrow", model.PermissionLedgerRead)
	e.wide = w.role(w.spaceA, "wide", model.PermissionLedgerRead, model.PermissionExchangeWrite)
	e.foreign = w.role(w.spaceB, "foreign", model.PermissionLedgerRead)
	return e
}

// grantable runs CheckGrantable in a transaction of space A.
func (e *escalationWorld) grantable(actor model.Actor, in model.NewGrant) error {
	return e.inSpace(e.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		return e.auth.CheckGrantable(ctx, tx, actor, in)
	})
}

// TestGrantEscalationRules guards the rules of ADR-0029 on granting: a grantor
// needs kernel.grant.write, may hand out only what it holds, never grants to
// itself and never claims another person as the grantor.
func TestGrantEscalationRules(t *testing.T) {
	t.Parallel()
	e := newEscalationWorld(t)
	asDelegate := &model.Grantor{ParticipantID: e.delegate.ParticipantID, SpaceID: e.spaceA}
	someoneElse := &model.Grantor{ParticipantID: e.plain.ParticipantID, SpaceID: e.spaceA}
	otherSpace := &model.Grantor{ParticipantID: e.delegate.ParticipantID, SpaceID: e.spaceB}
	absent, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}

	// Allowed: a subset of what the delegate holds.
	requireAllowed(t, "narrow role", e.grantable(e.delegate, model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow}))
	requireAllowed(t, "narrow role with itself as grantor", e.grantable(e.delegate,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow, GrantedBy: asDelegate}))
	requireAllowed(t, "extra permission it holds", e.grantable(e.delegate,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow, ExtraPermissions: []string{model.PermissionGrantWrite}}))

	denials := map[string]struct {
		actor model.Actor
		in    model.NewGrant
	}{
		"role beyond the grantor": {e.delegate, model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.wide}},
		"extra beyond the grantor": {e.delegate, model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow,
			ExtraPermissions: []string{model.PermissionExchangeWrite}}},
		"the platform-only key": {e.delegate, model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow,
			ExtraPermissions: []string{model.PermissionCapabilityWrite}}},
		"self grant":                {e.delegate, model.NewGrant{ParticipantID: e.delegate.ParticipantID, RoleID: e.narrow}},
		"someone else as grantor":   {e.delegate, model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow, GrantedBy: someoneElse}},
		"grantor of another space":  {e.delegate, model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow, GrantedBy: otherSpace}},
		"actor without grant.write": {e.plain, model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow}},
		"actor without a grant":     {e.recipient, model.NewGrant{ParticipantID: e.plain.ParticipantID, RoleID: e.narrow}},
		"null actor":                {model.Actor{}, model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow}},
	}
	for name, tc := range denials {
		requireCode(t, name, e.grantable(tc.actor, tc.in), model.CodePermissionDenied)
	}

	// The role must be visible in the space of the transaction.
	requireCode(t, "role of another space", e.grantable(e.delegate,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.foreign}), model.CodeNotFound)
	requireCode(t, "role that does not exist", e.grantable(e.delegate,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: absent.String()}), model.CodeNotFound)
	requireCode(t, "malformed role id", e.grantable(e.delegate,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: "nope"}), model.CodeInvalidInput)
	requireCode(t, "extra key outside the catalog", e.grantable(e.delegate,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow, ExtraPermissions: []string{"kernel.nope.write"}}), model.CodeInvalidInput)

	// The system actor skips the subset and the permission, not the attribution.
	requireAllowed(t, "system grants a wide role", e.grantable(system,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.wide}))
	requireCode(t, "system claiming someone as grantor", e.grantable(system,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow, GrantedBy: someoneElse}), model.CodePermissionDenied)

	// A suspended space grants nothing, even to the grantor with the right.
	e.setStatus(e.spaceA, "suspended")
	requireCode(t, "suspended space", e.grantable(e.delegate,
		model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow}), model.CodeSpaceSuspended)
}

// TestGrantEscalationHoldsAfterLosingTheRight shows the grantor's reach is
// read when the check runs: a revoked or expired grantor grants nothing.
func TestGrantEscalationHoldsAfterLosingTheRight(t *testing.T) {
	t.Parallel()
	e := newEscalationWorld(t)
	in := model.NewGrant{ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow}
	requireAllowed(t, "before", e.grantable(e.delegate, in))

	if _, err := e.admin.Exec(`UPDATE kernel.grants SET revoked_at = now() WHERE participant_id = $1`, e.delegate.ParticipantID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	requireCode(t, "after the grantor lost the grant", e.grantable(e.delegate, in), model.CodePermissionDenied)
}

// TestRevokeNeedsGrantWriteAndSubset guards rule 5 of ADR-0029: only someone
// who could grant a grant may revoke it.
func TestRevokeNeedsGrantWriteAndSubset(t *testing.T) {
	t.Parallel()
	e := newEscalationWorld(t)
	narrowGrant := e.grant(e.spaceA, e.recipient, e.narrow)
	other := e.person(e.spaceA, "other")
	wideGrant := e.grant(e.spaceA, other, e.wide)
	foreignHolder := e.person(e.spaceB, "foreign_holder")
	foreignGrant := e.grant(e.spaceB, foreignHolder, e.foreign)
	absent, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}

	revocable := func(actor model.Actor, grantID string) error {
		return e.inSpace(e.spaceA, func(ctx context.Context, tx *sql.Tx) error {
			return e.auth.CheckRevocable(ctx, tx, actor, grantID)
		})
	}
	requireAllowed(t, "a grant within the reach of the revoker", revocable(e.delegate, narrowGrant))
	requireCode(t, "a grant beyond the reach", revocable(e.delegate, wideGrant), model.CodePermissionDenied)
	requireCode(t, "without grant.write", revocable(e.plain, narrowGrant), model.CodePermissionDenied)
	requireCode(t, "null actor", revocable(model.Actor{}, narrowGrant), model.CodePermissionDenied)
	requireAllowed(t, "system revokes any grant", revocable(system, wideGrant))
	requireCode(t, "a grant of another space", revocable(e.delegate, foreignGrant), model.CodeNotFound)
	requireCode(t, "a grant that does not exist", revocable(e.delegate, absent.String()), model.CodeNotFound)
	requireCode(t, "malformed id", revocable(e.delegate, "x"), model.CodeInvalidInput)
}

// TestRoleCreationRules guards rule 3 and the reach of a new role.
func TestRoleCreationRules(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	author := w.holder(w.spaceA, "author", model.PermissionRoleWrite, model.PermissionLedgerRead)
	plain := w.holder(w.spaceA, "plain", model.PermissionLedgerRead)

	creatable := func(actor model.Actor, in model.NewRole) error {
		return w.inSpace(w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
			return w.auth.CheckRoleCreatable(ctx, tx, actor, in)
		})
	}
	role := func(system bool, permissions ...string) model.NewRole {
		return model.NewRole{Key: "new_role", Name: "New", Permissions: permissions, System: system}
	}

	requireAllowed(t, "a role within reach", creatable(author, role(false, model.PermissionLedgerRead)))
	requireAllowed(t, "an empty role", creatable(author, role(false)))
	requireCode(t, "a role beyond reach", creatable(author, role(false, model.PermissionExchangeWrite)), model.CodePermissionDenied)
	requireCode(t, "the platform-only key", creatable(author, role(false, model.PermissionCapabilityWrite)), model.CodePermissionDenied)
	requireCode(t, "a system role by a person", creatable(author, role(true, model.PermissionLedgerRead)), model.CodePermissionDenied)
	requireCode(t, "an empty system role by a person", creatable(author, role(true)), model.CodePermissionDenied)
	requireCode(t, "without role.write", creatable(plain, role(false)), model.CodePermissionDenied)
	requireCode(t, "null actor", creatable(model.Actor{}, role(false)), model.CodePermissionDenied)
	requireCode(t, "a key outside the catalog", creatable(author, role(false, "kernel.nope.write")), model.CodeInvalidInput)
	requireCode(t, "a key of another namespace", creatable(author, role(false, "plugin.item.write")), model.CodeInvalidInput)
	requireAllowed(t, "system creates a system role", creatable(system, role(true, model.PermissionExchangeWrite)))
	requireCode(t, "system with a key outside the catalog", creatable(system, role(true, "kernel.nope.write")), model.CodeInvalidInput)

	w.setStatus(w.spaceA, "suspended")
	requireCode(t, "suspended space", creatable(author, role(false)), model.CodeSpaceSuspended)
}

// TestPlatformOnlyKeyNeverReachesABusinessSpace follows the attack where a
// business owner or a delegate tries every way to hold kernel.capability.write:
// the check refuses the person, and the database refuses even the system actor.
func TestPlatformOnlyKeyNeverReachesABusinessSpace(t *testing.T) {
	t.Parallel()
	e := newEscalationWorld(t)

	// The kernel itself writing the key into a business space is stopped by
	// the database, whatever the application layer does.
	if _, err := state.CreateRole(context.Background(), e.api, system, e.spaceA,
		model.NewRole{Key: "evil", Name: "Evil", Permissions: []string{model.PermissionCapabilityWrite}}); err == nil {
		t.Error("a business role with kernel.capability.write was created")
	}
	if _, err := state.GrantRole(context.Background(), e.api, e.clock, system, e.spaceA, model.NewGrant{
		ParticipantID: e.recipient.ParticipantID, RoleID: e.narrow, ExtraPermissions: []string{model.PermissionCapabilityWrite},
	}); err == nil {
		t.Error("a business grant with kernel.capability.write was written")
	}
	for table, column := range map[string]string{"roles": "permissions", "grants": "extra_permissions"} {
		var holders int
		if err := e.admin.QueryRow(`SELECT count(*) FROM kernel.` + table + ` WHERE ` + column + ` @> ARRAY['kernel.capability.write']`).Scan(&holders); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if holders != 0 {
			t.Errorf("%d rows of %s hold kernel.capability.write in a business space", holders, table)
		}
	}
	// And nobody holds it, so the permission check denies a person.
	requireCode(t, "capability.write for a delegate", e.check(e.spaceA, e.delegate, req(model.PermissionCapabilityWrite)), model.CodePermissionDenied)
}

// TestCapabilityEnableSources guards rule 9 and rule 4 of ADR-0029: a role of a
// space never enables a capability; staff holding the key may enable an admin
// capability in a platform space; package, ai and code are the kernel's.
func TestCapabilityEnableSources(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	platform := w.space("platform", "world-platform")
	staff := w.holder(platform, "staff", model.PermissionCapabilityWrite)
	staffOther := w.holder(platform, "staff_other", model.PermissionCapabilityWrite)
	businessOwner := w.holder(w.spaceA, "business_owner", model.PermissionGrantWrite, model.PermissionRoleWrite)

	enable := func(spaceID string, actor model.Actor, in model.NewSpaceCapability) error {
		return w.inSpace(spaceID, func(ctx context.Context, tx *sql.Tx) error {
			return w.auth.CheckCapabilityEnable(ctx, tx, actor, in)
		})
	}
	admin := func(by model.Actor, spaceID string) model.NewSpaceCapability {
		return model.NewSpaceCapability{
			CapabilityKey: "test.capability", Source: model.CapabilitySourceAdmin,
			EnabledBy: &model.Grantor{ParticipantID: by.ParticipantID, SpaceID: spaceID},
		}
	}
	source := func(s model.CapabilitySource) model.NewSpaceCapability {
		return model.NewSpaceCapability{CapabilityKey: "test.capability", Source: s}
	}

	// Staff with the key, in the platform space.
	requireAllowed(t, "staff admin source", enable(platform, staff, admin(staff, platform)))
	for _, s := range []model.CapabilitySource{model.CapabilitySourcePackage, model.CapabilitySourceAI, model.CapabilitySourceCode} {
		requireCode(t, "staff with source "+string(s), enable(platform, staff, source(s)), model.CodePermissionDenied)
	}
	requireCode(t, "staff naming another enabler", enable(platform, staff, admin(staffOther, platform)), model.CodePermissionDenied)

	// A business owner, or any role of a space, never does it.
	for _, s := range []model.CapabilitySource{model.CapabilitySourceAdmin, model.CapabilitySourcePackage, model.CapabilitySourceAI, model.CapabilitySourceCode} {
		in := source(s)
		if s == model.CapabilitySourceAdmin {
			in = admin(businessOwner, w.spaceA)
		}
		requireCode(t, "business owner with source "+string(s), enable(w.spaceA, businessOwner, in), model.CodePermissionDenied)
	}
	// Staff of the platform space acting in a customer space is not resolvable
	// there until the platform layer defines support access (B4).
	requireCode(t, "staff in a customer space", enable(w.spaceA, staff, admin(staff, platform)), model.CodePermissionDenied)

	// The system actor enables any source.
	for _, s := range []model.CapabilitySource{model.CapabilitySourcePackage, model.CapabilitySourceAI, model.CapabilitySourceCode} {
		requireAllowed(t, "system with source "+string(s), enable(w.spaceA, system, source(s)))
	}
	requireAllowed(t, "system with the admin source", enable(w.spaceA, system, admin(staff, platform)))

	requireCode(t, "null actor", enable(w.spaceA, model.Actor{}, source(model.CapabilitySourceCode)), model.CodePermissionDenied)
	requireCode(t, "unknown source", enable(w.spaceA, system, source("magic")), model.CodeInvalidInput)
	requireCode(t, "malformed key", enable(w.spaceA, system,
		model.NewSpaceCapability{CapabilityKey: "Bad", Source: model.CapabilitySourceCode}), model.CodeInvalidInput)

	w.setStatus(w.spaceA, "suspended")
	requireCode(t, "suspended space", enable(w.spaceA, system, source(model.CapabilitySourceCode)), model.CodeSpaceSuspended)
}

// TestCapabilityWriteIsBlockedInSuspendedPlatformSpace shows the platform-only
// key obeys the state of the space like any write permission.
func TestCapabilityWriteIsBlockedInSuspendedPlatformSpace(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	platform := w.space("platform", "world-platform")
	staff := w.holder(platform, "staff", model.PermissionCapabilityWrite)

	requireAllowed(t, "active platform space", w.check(platform, staff, req(model.PermissionCapabilityWrite)))
	w.setStatus(platform, "suspended")
	requireCode(t, "suspended platform space", w.check(platform, staff, req(model.PermissionCapabilityWrite)), model.CodeSpaceSuspended)
	requireCode(t, "suspended platform space, system", w.check(platform, system, req(model.PermissionCapabilityWrite)), model.CodeSpaceSuspended)
}
