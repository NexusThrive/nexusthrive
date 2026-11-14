package capability_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestNewAuthorizerRejectsMissingParts refuses a null clock or an empty
// catalog: an authorizer that knows nothing would deny by accident.
func TestNewAuthorizerRejectsMissingParts(t *testing.T) {
	t.Parallel()
	if _, err := capability.NewAuthorizer(model.KernelCatalog(), nil); err == nil {
		t.Error("a nil clock was accepted")
	} else {
		requireCode(t, "nil clock", err, model.CodeInvalidInput)
	}
	if _, err := capability.NewAuthorizer(model.Catalog{}, &fakeClock{}); err == nil {
		t.Error("an empty catalog was accepted")
	} else {
		requireCode(t, "empty catalog", err, model.CodeInvalidInput)
	}
}

// TestCheckAllowsPermissionFromRole guards criterion 1.
func TestCheckAllowsPermissionFromRole(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.holder(w.spaceA, "cashier", model.PermissionExchangeWrite)

	requireAllowed(t, "exchange.write from the role", w.check(w.spaceA, actor, req(model.PermissionExchangeWrite)))
	requireCode(t, "a key the role lacks", w.check(w.spaceA, actor, req(model.PermissionLedgerRead)), model.CodePermissionDenied)
}

// TestCheckAllowsPermissionFromExtraPermissions guards criterion 2.
func TestCheckAllowsPermissionFromExtraPermissions(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.person(w.spaceA, "reader")
	w.grant(w.spaceA, actor, w.role(w.spaceA, "empty"), model.PermissionCommitmentRead)

	requireAllowed(t, "commitment.read from extra permissions", w.check(w.spaceA, actor, req(model.PermissionCommitmentRead)))
	requireCode(t, "another key", w.check(w.spaceA, actor, req(model.PermissionCommitmentWrite)), model.CodePermissionDenied)
}

// TestCheckDeniesWithoutGrantExpiredRevokedOrArchived guards criterion 3 and
// the archived-participant reading: every cause gives the same typed error
// with the same message, so a caller cannot tell them apart.
func TestCheckDeniesWithoutGrantExpiredRevokedOrArchived(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	key := req(model.PermissionLedgerRead)

	none := w.person(w.spaceA, "none")

	expiry := start.Add(time.Hour)
	expired := w.person(w.spaceA, "expired")
	w.grantUntil(w.spaceA, expired, w.role(w.spaceA, "r_expired", model.PermissionLedgerRead), &expiry)

	revoked := w.person(w.spaceA, "revoked")
	revokedGrant := w.grant(w.spaceA, revoked, w.role(w.spaceA, "r_revoked", model.PermissionLedgerRead))

	archived := w.person(w.spaceA, "archived")
	w.grant(w.spaceA, archived, w.role(w.spaceA, "r_archived", model.PermissionLedgerRead))

	// Before anything happens, the holders of grants are allowed.
	requireAllowed(t, "expired holder before expiry", w.check(w.spaceA, expired, key))
	requireAllowed(t, "revoked holder before revocation", w.check(w.spaceA, revoked, key))
	requireAllowed(t, "archived holder before archiving", w.check(w.spaceA, archived, key))

	// The grant expires exactly at the instant: it is already expired there.
	w.clock.Set(expiry.Add(-time.Nanosecond))
	requireAllowed(t, "one instant before expiry", w.check(w.spaceA, expired, key))
	w.clock.Set(expiry)
	if err := state.RevokeGrant(context.Background(), w.api, system, w.spaceA, revokedGrant); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	if err := state.ArchiveParticipant(context.Background(), w.api, system, w.spaceA, archived.ParticipantID); err != nil {
		t.Fatalf("ArchiveParticipant: %v", err)
	}

	var messages []string
	for name, actor := range map[string]model.Actor{"no grant": none, "expired": expired, "revoked": revoked, "archived": archived} {
		err := w.check(w.spaceA, actor, key)
		requireCode(t, name, err, model.CodePermissionDenied)
		messages = append(messages, err.Error())
	}
	for _, message := range messages[1:] {
		if message != messages[0] {
			t.Errorf("denial messages differ: %q and %q", messages[0], message)
		}
	}
}

// TestCheckDeniesNilActor guards criterion 4: an anonymous or unresolvable
// actor is always denied, and a made-up key does not change that into an
// answer.
func TestCheckDeniesNilActor(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	holder := w.holder(w.spaceA, "holder", model.PermissionLedgerRead)
	absent, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}

	actors := map[string]model.Actor{
		"zero actor":                 {},
		"person without participant": {Kind: model.ActorPerson},
		"api key without one":        {Kind: model.ActorAPIKey},
		"unknown kind":               {Kind: "robot", ParticipantID: holder.ParticipantID},
		"empty kind":                 {ParticipantID: holder.ParticipantID},
		"participant that is absent": {Kind: model.ActorPerson, ParticipantID: absent.String()},
		"malformed participant":      {Kind: model.ActorPerson, ParticipantID: "not-a-uuid"},
		"injection in the id":        {Kind: model.ActorPerson, ParticipantID: "' OR '1'='1"},
		"nil uuid":                   {Kind: model.ActorPerson, ParticipantID: uuid.Nil.String()},
		"system with a malformed id": {Kind: model.ActorSystem, ParticipantID: "not-a-uuid"},
	}
	for name, actor := range actors {
		for _, key := range []string{model.PermissionLedgerRead, model.PermissionExchangeWrite, "kernel.unknown.write", "", "Not A Key"} {
			requireCode(t, name+" with key "+key, w.check(w.spaceA, actor, req(key)), model.CodePermissionDenied)
		}
		requireCode(t, name+" with a capability", w.check(w.spaceA, actor,
			capability.Requirement{Permission: model.PermissionLedgerRead, Capability: "bad key"}), model.CodePermissionDenied)
	}

	// Positive controls: the real holder and the system actor pass.
	requireAllowed(t, "holder", w.check(w.spaceA, holder, req(model.PermissionLedgerRead)))
	requireAllowed(t, "system without a participant", w.check(w.spaceA, system, req(model.PermissionLedgerRead)))
}

// TestCheckUnknownKeyIsInvalidInput guards criterion 5.
func TestCheckUnknownKeyIsInvalidInput(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	nobody := w.person(w.spaceA, "nobody")
	holder := w.holder(w.spaceA, "holder", model.PermissionLedgerRead)

	for _, key := range []string{"", "Bad Key", "kernel.nope.write", "kernel.ledger", "plugin.item.write", "kernel.ledger.read ", "KERNEL.LEDGER.READ"} {
		for name, actor := range map[string]model.Actor{"actor without grants": nobody, "holder": holder, "system": system} {
			requireCode(t, name+" with key "+key, w.check(w.spaceA, actor, req(key)), model.CodeInvalidInput)
		}
	}
}

// TestCheckSuspendedAndClosedBlockWriteAndMoney guards criterion 6 and the
// readings on closed and provisioning spaces, across the whole catalog.
func TestCheckSuspendedAndClosedBlockWriteAndMoney(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	catalog := model.KernelCatalog()
	var tradable []string
	for _, key := range catalog.Keys() {
		if def, _ := catalog.Lookup(key); !def.PlatformOnly {
			tradable = append(tradable, key)
		}
	}
	full := w.holder(w.spaceA, "full", tradable...)
	empty := w.person(w.spaceA, "empty")

	for _, status := range []string{"active", "provisioning", "suspended", "closed"} {
		w.setStatus(w.spaceA, status)
		blocked := status == "suspended" || status == "closed"
		for _, key := range catalog.Keys() {
			def, _ := catalog.Lookup(key)
			for name, actor := range map[string]model.Actor{"system": system, "holder": full} {
				if name == "holder" && def.PlatformOnly {
					continue
				}
				err := w.check(w.spaceA, actor, req(key))
				label := status + " " + name + " " + key
				if blocked && def.Risk != model.RiskRead {
					requireCode(t, label, err, model.CodeSpaceSuspended)
				} else {
					requireAllowed(t, label, err)
				}
			}
			// Without permission the answer is permission_denied: a caller
			// with no right learns nothing about the state of the space.
			requireCode(t, status+" without permission "+key, w.check(w.spaceA, empty, req(key)), model.CodePermissionDenied)
		}
	}
}

// TestCheckInactiveCapability guards criterion 7.
func TestCheckInactiveCapability(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.holder(w.spaceA, "worker", model.PermissionCommitmentWrite, model.PermissionLedgerRead)
	empty := w.person(w.spaceA, "empty")
	needs := func(permission string) capability.Requirement {
		return capability.Requirement{Permission: permission, Capability: "test.capability"}
	}

	requireCode(t, "absent capability", w.check(w.spaceA, actor, needs(model.PermissionCommitmentWrite)), model.CodeCapabilityInactive)
	requireCode(t, "absent capability on a read", w.check(w.spaceA, actor, needs(model.PermissionLedgerRead)), model.CodeCapabilityInactive)
	requireCode(t, "absent capability for the system actor", w.check(w.spaceA, system, needs(model.PermissionLedgerRead)), model.CodeCapabilityInactive)
	requireCode(t, "no permission comes first", w.check(w.spaceA, empty, needs(model.PermissionLedgerRead)), model.CodePermissionDenied)
	requireCode(t, "another space has no capability", w.check(w.spaceB, system, needs(model.PermissionLedgerRead)), model.CodeCapabilityInactive)

	if err := state.EnableCapability(context.Background(), w.api, system, w.spaceA,
		model.NewSpaceCapability{CapabilityKey: "test.capability", Source: model.CapabilitySourceCode}); err != nil {
		t.Fatalf("EnableCapability: %v", err)
	}
	requireAllowed(t, "active capability", w.check(w.spaceA, actor, needs(model.PermissionCommitmentWrite)))
	requireCode(t, "the capability is per space", w.check(w.spaceB, system, needs(model.PermissionLedgerRead)), model.CodeCapabilityInactive)

	if _, err := w.admin.Exec(`UPDATE kernel.space_capabilities SET status = 'suspended' WHERE space_id = $1`, w.spaceA); err != nil {
		t.Fatalf("suspend capability: %v", err)
	}
	requireCode(t, "suspended capability", w.check(w.spaceA, actor, needs(model.PermissionCommitmentWrite)), model.CodeCapabilityInactive)

	// A suspended space answers first, before the capability.
	w.setStatus(w.spaceA, "suspended")
	requireCode(t, "suspended space precedes", w.check(w.spaceA, actor, needs(model.PermissionCommitmentWrite)), model.CodeSpaceSuspended)

	// A malformed capability key is an error of the code, not of the data.
	for _, key := range []string{"Bad", "bad key", "a", "UPPER.case"} {
		requireCode(t, "capability "+key, w.check(w.spaceA, actor,
			capability.Requirement{Permission: model.PermissionLedgerRead, Capability: key}), model.CodeInvalidInput)
	}
}

// TestCheckDeniesAcrossSpaces guards criterion 9: the space is the one of the
// transaction, so an actor of one space has nothing in another.
func TestCheckDeniesAcrossSpaces(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	inA := w.holder(w.spaceA, "in_a", model.PermissionLedgerRead)
	inB := w.holder(w.spaceB, "in_b", model.PermissionLedgerRead)

	requireAllowed(t, "A in A", w.check(w.spaceA, inA, req(model.PermissionLedgerRead)))
	requireAllowed(t, "B in B", w.check(w.spaceB, inB, req(model.PermissionLedgerRead)))
	requireCode(t, "A actor in B", w.check(w.spaceB, inA, req(model.PermissionLedgerRead)), model.CodePermissionDenied)
	requireCode(t, "B actor in A", w.check(w.spaceA, inB, req(model.PermissionLedgerRead)), model.CodePermissionDenied)
	requireCode(t, "A actor in B, write", w.check(w.spaceB, inA, req(model.PermissionExchangeWrite)), model.CodePermissionDenied)

	// A grant written in B for a participant of A cannot exist: the composite
	// key refuses it, so there is nothing to leak.
	role := w.role(w.spaceB, "b_role", model.PermissionLedgerRead)
	if _, err := state.GrantRole(context.Background(), w.api, w.clock, system, w.spaceB,
		model.NewGrant{ParticipantID: inA.ParticipantID, RoleID: role}); err == nil {
		t.Error("a grant of a participant of another space was written")
	}
	requireCode(t, "still denied in B", w.check(w.spaceB, inA, req(model.PermissionLedgerRead)), model.CodePermissionDenied)
}

// TestCheckWithoutSpaceIsDenied guards the missing-space case: with no space
// fixed in the transaction, or a space that does not exist, nothing is allowed,
// not even for the system actor.
func TestCheckWithoutSpaceIsDenied(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.holder(w.spaceA, "holder", model.PermissionLedgerRead)
	ghost, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}

	run := func(actor model.Actor, key string, setup func(tx *sql.Tx)) error {
		tx, err := w.api.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback()
		if setup != nil {
			setup(tx)
		}
		return w.auth.Check(context.Background(), tx, actor, req(key))
	}
	for name, actor := range map[string]model.Actor{"holder": actor, "system": system} {
		requireCode(t, name+" without a space", run(actor, model.PermissionLedgerRead, nil), model.CodePermissionDenied)
		requireCode(t, name+" with an empty setting", run(actor, model.PermissionLedgerRead, func(tx *sql.Tx) {
			if _, err := tx.Exec(`SELECT set_config('kernel.space_id', '', true)`); err != nil {
				t.Fatalf("set: %v", err)
			}
		}), model.CodePermissionDenied)
		requireCode(t, name+" in a space that does not exist", run(actor, model.PermissionLedgerRead, func(tx *sql.Tx) {
			if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ghost.String()); err != nil {
				t.Fatalf("set: %v", err)
			}
		}), model.CodePermissionDenied)
		requireCode(t, name+" without a space and a write key", run(actor, model.PermissionExchangeWrite, nil), model.CodePermissionDenied)
	}
}

// TestCheckSeesRevocationInSameTransaction shows nothing is cached: a grant
// revoked, or expired by the clock, inside the transaction stops counting.
func TestCheckSeesRevocationInSameTransaction(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.person(w.spaceA, "worker")
	grantID := w.grant(w.spaceA, actor, w.role(w.spaceA, "r", model.PermissionLedgerRead))
	expiring := w.person(w.spaceA, "expiring")
	expiry := start.Add(time.Hour)
	w.grantUntil(w.spaceA, expiring, w.role(w.spaceA, "r2", model.PermissionLedgerRead), &expiry)

	err := w.inSpace(w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		if err := w.auth.Check(ctx, tx, actor, req(model.PermissionLedgerRead)); err != nil {
			t.Errorf("before revoking: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE kernel.grants SET revoked_at = now() WHERE id = $1`, grantID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		requireCode(t, "after revoking in the same transaction", w.auth.Check(ctx, tx, actor, req(model.PermissionLedgerRead)), model.CodePermissionDenied)

		if err := w.auth.Check(ctx, tx, expiring, req(model.PermissionLedgerRead)); err != nil {
			t.Errorf("before the clock moves: %v", err)
		}
		w.clock.Set(expiry)
		requireCode(t, "after the clock reaches the expiry", w.auth.Check(ctx, tx, expiring, req(model.PermissionLedgerRead)), model.CodePermissionDenied)
		return nil
	})
	if err != nil {
		t.Fatalf("InSpace: %v", err)
	}
}

// TestCheckRegisteredKeysComeFromTheCatalog shows that only the catalog the
// authorizer received counts: a key stored by a role but not registered is
// inert, and an extension registers real keys with their own risk.
func TestCheckRegisteredKeysComeFromTheCatalog(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	const key = "plugin.item.write"
	actor := w.holder(w.spaceA, "plugin_user", key, model.PermissionLedgerRead)

	// The role holds an unregistered key: Check rejects it as a code error.
	requireCode(t, "unregistered key", w.check(w.spaceA, actor, req(key)), model.CodeInvalidInput)

	extended, err := model.KernelCatalog().Extend("plugin", []model.PermissionDef{{Key: key, Risk: model.RiskMoney}})
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	registered, err := capability.NewAuthorizer(extended, w.clock)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	checkWith := func(auth *capability.Authorizer, spaceID string) error {
		return w.inSpace(spaceID, func(ctx context.Context, tx *sql.Tx) error {
			return auth.Check(ctx, tx, actor, req(key))
		})
	}
	requireAllowed(t, "registered key", checkWith(registered, w.spaceA))
	w.setStatus(w.spaceA, "suspended")
	requireCode(t, "registered key keeps its risk", checkWith(registered, w.spaceA), model.CodeSpaceSuspended)
	requireCode(t, "the kernel authorizer still refuses it", w.check(w.spaceA, actor, req(key)), model.CodeInvalidInput)
}

// TestCheckHoldsRevocationUntilCommit proves the race of use against
// revocation is closed: while a write check is open, a revocation of the
// grant it read waits for the transaction to end.
func TestCheckHoldsRevocationUntilCommit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.person(w.spaceA, "worker")
	grantID := w.grant(w.spaceA, actor, w.role(w.spaceA, "r", model.PermissionExchangeWrite, model.PermissionLedgerRead))

	for _, tc := range []struct {
		name    string
		key     string
		blocked bool
	}{
		{"write check blocks the revocation", model.PermissionExchangeWrite, true},
		{"read check does not block", model.PermissionLedgerRead, false},
	} {
		tx, err := w.api.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, w.spaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		if err := w.auth.Check(context.Background(), tx, actor, req(tc.key)); err != nil {
			t.Fatalf("%s: Check: %v", tc.name, err)
		}

		done := make(chan error, 1)
		go func() {
			done <- state.RevokeGrant(context.Background(), w.api, system, w.spaceA, grantID)
		}()
		finished := false
		select {
		case err := <-done:
			finished = true
			if tc.blocked {
				t.Errorf("%s: the revocation finished while the check was open (%v)", tc.name, err)
			} else if err != nil {
				t.Errorf("%s: revocation: %v", tc.name, err)
			}
		case <-time.After(750 * time.Millisecond):
			if !tc.blocked {
				t.Errorf("%s: the revocation waited for a read check", tc.name)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if !finished {
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("%s: revocation after commit: %v", tc.name, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: the revocation never finished after the commit", tc.name)
			}
		}
		if tc.blocked {
			// The first round revoked the grant; grant again for the second.
			actor = w.person(w.spaceA, "worker2")
			grantID = w.grant(w.spaceA, actor, w.role(w.spaceA, "r2", model.PermissionExchangeWrite, model.PermissionLedgerRead))
		}
	}
	requireCode(t, "after the revocation", w.check(w.spaceA, actor, req(model.PermissionLedgerRead)), model.CodePermissionDenied)
}

// TestCheckHoldsSuspensionUntilCommit proves the race of writing against
// suspending the space: the status change waits for an open write check.
func TestCheckHoldsSuspensionUntilCommit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.holder(w.spaceA, "worker", model.PermissionExchangeWrite)

	tx, err := w.api.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, w.spaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	if err := w.auth.Check(context.Background(), tx, actor, req(model.PermissionExchangeWrite)); err != nil {
		t.Fatalf("Check: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- w.inSpace(w.spaceA, func(ctx context.Context, other *sql.Tx) error {
			_, err := other.ExecContext(ctx, `UPDATE kernel.spaces SET status = 'suspended' WHERE id = $1`, w.spaceA)
			return err
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("the suspension finished while the write check was open (%v)", err)
	case <-time.After(750 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("suspension after commit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the suspension never finished after the commit")
	}
	requireCode(t, "after the suspension", w.check(w.spaceA, actor, req(model.PermissionExchangeWrite)), model.CodeSpaceSuspended)
}

// TestCheckKeysAreNeverConcatenated feeds hostile text as keys and ids: every
// one is a typed rejection and the tables are untouched afterwards.
func TestCheckKeysAreNeverConcatenated(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.holder(w.spaceA, "holder", model.PermissionLedgerRead)
	before := w.grantCount()

	for _, hostile := range []string{
		"kernel.ledger.read'; DROP TABLE kernel.grants; --",
		"kernel.ledger.read' OR '1'='1",
		"$1", "kernel.ledger.read\x00",
	} {
		err := w.check(w.spaceA, actor, capability.Requirement{Permission: hostile})
		if c, ok := model.CodeOf(err); !ok || c != model.CodeInvalidInput {
			t.Errorf("hostile permission %q: error = %v", hostile, err)
		}
		err = w.check(w.spaceA, actor, capability.Requirement{Permission: model.PermissionLedgerRead, Capability: hostile})
		if c, ok := model.CodeOf(err); !ok || c != model.CodeInvalidInput {
			t.Errorf("hostile capability %q: error = %v", hostile, err)
		}
	}
	if got := w.grantCount(); got != before {
		t.Errorf("grants = %d, want %d", got, before)
	}
	if !slices.Contains(model.KernelCatalog().Keys(), model.PermissionLedgerRead) {
		t.Fatal("catalog lost a key")
	}
}

// grantCount counts every grant as the administrator.
func (w *world) grantCount() int {
	w.t.Helper()
	var n int
	if err := w.admin.QueryRow(`SELECT count(*) FROM kernel.grants`).Scan(&n); err != nil {
		w.t.Fatalf("count grants: %v", err)
	}
	return n
}
