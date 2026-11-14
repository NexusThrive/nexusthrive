package capability_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestOwnerCannotEnableAdminCapability guards criterion 8 with the real owner
// role of the bootstrap: the owner of a business cannot enable a capability,
// cannot hold the key that does, and cannot hand it out or create a role that
// holds it.
func TestOwnerCannotEnableAdminCapability(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := state.CreateStandardRoles(context.Background(), w.api, system, w.spaceA); err != nil {
		t.Fatalf("CreateStandardRoles: %v", err)
	}
	var ownerRole string
	if err := w.admin.QueryRow(`SELECT id::text FROM kernel.roles WHERE space_id = $1 AND key = 'owner'`, w.spaceA).Scan(&ownerRole); err != nil {
		t.Fatalf("read owner role: %v", err)
	}
	owner := w.person(w.spaceA, "owner")
	w.grant(w.spaceA, owner, ownerRole)

	// The owner works with what the role lists.
	for _, key := range model.OwnerRole().Permissions {
		requireAllowed(t, "owner "+key, w.check(w.spaceA, owner, req(key)))
	}
	// And never with the platform key or the keys of the kernel processes.
	for _, key := range []string{model.PermissionCapabilityWrite, model.PermissionTimerProcess, model.PermissionOutboxClaim} {
		requireCode(t, "owner "+key, w.check(w.spaceA, owner, req(key)), model.CodePermissionDenied)
	}

	for _, source := range []model.CapabilitySource{model.CapabilitySourceAdmin, model.CapabilitySourcePackage, model.CapabilitySourceAI, model.CapabilitySourceCode} {
		in := model.NewSpaceCapability{CapabilityKey: "test.capability", Source: source}
		if source == model.CapabilitySourceAdmin {
			in.EnabledBy = &model.Grantor{ParticipantID: owner.ParticipantID, SpaceID: w.spaceA}
		}
		err := w.inSpace(w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
			return w.auth.CheckCapabilityEnable(ctx, tx, owner, in)
		})
		requireCode(t, "owner enabling with source "+string(source), err, model.CodePermissionDenied)
	}

	// The owner cannot create a role that holds the key, nor grant it.
	err := w.inSpace(w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		return w.auth.CheckRoleCreatable(ctx, tx, owner,
			model.NewRole{Key: "boss", Name: "Boss", Permissions: []string{model.PermissionCapabilityWrite}})
	})
	requireCode(t, "owner creating a role with the key", err, model.CodePermissionDenied)
	err = w.inSpace(w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		return w.auth.CheckGrantable(ctx, tx, owner, model.NewGrant{
			ParticipantID: w.person(w.spaceA, "colleague").ParticipantID, RoleID: ownerRole,
			ExtraPermissions: []string{model.PermissionCapabilityWrite},
		})
	})
	requireCode(t, "owner granting the key", err, model.CodePermissionDenied)

	// The owner may do what the role allows: create a narrower role.
	err = w.inSpace(w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		return w.auth.CheckRoleCreatable(ctx, tx, owner,
			model.NewRole{Key: "clerk", Name: "Clerk", Permissions: []string{model.PermissionLedgerRead}})
	})
	requireAllowed(t, "owner creating a narrower role", err)

	if !slices.Contains(model.KernelCatalog().Keys(), model.PermissionCapabilityWrite) {
		t.Fatal("the catalog lost kernel.capability.write")
	}
}
