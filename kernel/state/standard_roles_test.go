package state_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestCreateStandardRolesCreatesOwner guards the bootstrap of a space: it gets
// the owner role with the ten keys of the story, marked as a system role, and
// nothing else.
func TestCreateStandardRolesCreatesOwner(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	ctx := context.Background()

	if err := state.CreateStandardRoles(ctx, api, system, spaceA); err != nil {
		t.Fatalf("CreateStandardRoles: %v", err)
	}
	var key string
	var isSystem bool
	if err := admin.QueryRow(`SELECT key, system FROM kernel.roles WHERE space_id = $1`, spaceA).Scan(&key, &isSystem); err != nil {
		t.Fatalf("read the only role: %v", err)
	}
	if key != "owner" || !isSystem {
		t.Fatalf("role = %q, system %v; want the system role owner", key, isSystem)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.roles WHERE space_id = $1`, spaceA); got != 1 {
		t.Fatalf("roles = %d, want 1", got)
	}

	var stored []string
	listRows, err := admin.Query(`SELECT unnest(permissions) FROM kernel.roles WHERE space_id = $1 AND key = 'owner'`, spaceA)
	if err != nil {
		t.Fatalf("read permissions: %v", err)
	}
	defer listRows.Close()
	for listRows.Next() {
		var p string
		if err := listRows.Scan(&p); err != nil {
			t.Fatalf("scan permission: %v", err)
		}
		stored = append(stored, p)
	}
	want := slices.Clone(model.OwnerRole().Permissions)
	slices.Sort(stored)
	slices.Sort(want)
	if len(want) != 10 || !slices.Equal(stored, want) {
		t.Errorf("owner permissions = %v, want %v", stored, want)
	}
	if slices.Contains(stored, model.PermissionCapabilityWrite) {
		t.Error("the owner role holds kernel.capability.write")
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.roles WHERE space_id = $1`, spaceB); got != 0 {
		t.Errorf("space B has %d roles, want 0", got)
	}
	visible(t, api, spaceB, func(tx *sql.Tx) {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM kernel.roles`).Scan(&n); err != nil || n != 0 {
			t.Errorf("space B sees %d roles (%v), want 0", n, err)
		}
	})
}

// TestCreateStandardRolesIsAllOrNothing covers a repeat, a refused actor and a
// bad space: each leaves the roles as they were.
func TestCreateStandardRolesIsAllOrNothing(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	ctx := context.Background()
	if err := state.CreateStandardRoles(ctx, api, system, spaceA); err != nil {
		t.Fatalf("CreateStandardRoles: %v", err)
	}

	err := state.CreateStandardRoles(ctx, api, system, spaceA)
	if code, ok := model.CodeOf(err); !ok || code != model.CodeRuleViolation {
		t.Errorf("repeating = %v, want rule_violation", err)
	}
	person := model.Actor{Kind: model.ActorPerson, ParticipantID: "01900000-0000-7000-8000-000000000999"}
	for name, actor := range map[string]model.Actor{"person": person, "null actor": {}, "api key": {Kind: model.ActorAPIKey, ParticipantID: person.ParticipantID}} {
		err := state.CreateStandardRoles(ctx, api, actor, spaceB)
		if code, ok := model.CodeOf(err); !ok || code != model.CodePermissionDenied {
			t.Errorf("%s = %v, want permission_denied", name, err)
		}
	}
	if err := state.CreateStandardRoles(ctx, api, system, "nope"); err == nil {
		t.Error("a malformed space id was accepted")
	} else if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
		t.Errorf("malformed space = %v, want invalid_input", err)
	}
	ghost, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	if err := state.CreateStandardRoles(ctx, api, system, ghost.String()); err == nil {
		t.Error("a space that does not exist was accepted")
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.roles`); got != 1 {
		t.Errorf("roles = %d, want 1", got)
	}
}
