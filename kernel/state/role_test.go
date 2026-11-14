package state_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// spacePlatform is a platform space the grant tests seed as the administrator.
const spacePlatform = "01900000-0000-7000-8000-0000000005f1"

// fixedClock is a clock the test moves by hand.
type fixedClock struct{ at time.Time }

// Now returns the time the test fixed.
func (c *fixedClock) Now() time.Time { return c.at }

// grantSetup returns the administrator and API pools with spaces A and B
// (business) and a platform space seeded, plus a staff participant in the
// platform space.
func grantSetup(t *testing.T) (admin, api *sql.DB, staff string) {
	t.Helper()
	admin, api = ledgerSetup(t)
	if _, err := admin.Exec(`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'platform', 'Platform', 'test-platform')`, spacePlatform); err != nil {
		t.Fatalf("seed platform space: %v", err)
	}
	return admin, api, mustParticipant(t, api, spacePlatform, newPerson("", "Staff"))
}

// ownerRole builds a valid system role with one permission.
func ownerRole() model.NewRole {
	return model.NewRole{Key: "owner", Name: "Owner", Permissions: []string{"kernel.test.read"}, System: true}
}

// mustRole creates a role and returns its id.
func mustRole(t *testing.T, api *sql.DB, spaceID string, in model.NewRole) string {
	t.Helper()
	id, err := state.CreateRole(context.Background(), api, system, spaceID, in)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	return id
}

// TestCreateRoleStoresAndRejectsDuplicateKeyPerSpace guards criterion 1.
func TestCreateRoleStoresAndRejectsDuplicateKeyPerSpace(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)

	id := mustRole(t, api, spaceA, ownerRole())
	var key, name string
	var permissions string
	var isSystem bool
	if err := admin.QueryRow(`SELECT key, name, permissions::text, system FROM kernel.roles WHERE id = $1 AND space_id = $2`, id, spaceA).
		Scan(&key, &name, &permissions, &isSystem); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if key != "owner" || name != "Owner" || permissions != "{kernel.test.read}" || !isSystem {
		t.Errorf("stored role = %q %q %q %v", key, name, permissions, isSystem)
	}

	_, err := state.CreateRole(context.Background(), api, system, spaceA, ownerRole())
	requireCode(t, err, model.CodeRuleViolation)
	mustRole(t, api, spaceB, ownerRole())
	if got := count(t, admin, `SELECT count(*) FROM kernel.roles`); got != 2 {
		t.Errorf("roles = %d, want 2", got)
	}
}

// TestCreateRoleRejectsMalformedInput guards criterion 1: invalid_input
// before any write.
func TestCreateRoleRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)

	cases := map[string]func(*model.NewRole){
		"uppercase key":   func(r *model.NewRole) { r.Key = "Owner" },
		"empty key":       func(r *model.NewRole) { r.Key = "" },
		"blank name":      func(r *model.NewRole) { r.Name = "  " },
		"bad permission":  func(r *model.NewRole) { r.Permissions = []string{"Bad"} },
		"one segment":     func(r *model.NewRole) { r.Permissions = []string{"single"} },
		"too many":        func(r *model.NewRole) { r.Permissions = make([]string, model.MaxPermissions+1) },
		"empty permitted": func(r *model.NewRole) { r.Permissions = []string{""} },
	}
	for name, mutate := range cases {
		in := ownerRole()
		mutate(&in)
		_, err := state.CreateRole(context.Background(), api, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	_, err := state.CreateRole(context.Background(), api, system, "nope", ownerRole())
	requireCode(t, err, model.CodeInvalidInput)
	if got := count(t, admin, `SELECT count(*) FROM kernel.roles`); got != 0 {
		t.Errorf("roles = %d, want 0", got)
	}
}

// TestCreateRoleAcceptsEmptyPermissions: a role may hold no permission yet.
func TestCreateRoleAcceptsEmptyPermissions(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	in := ownerRole()
	in.Permissions = nil
	mustRole(t, api, spaceA, in)
}

// TestCreateRoleInUnknownSpaceIsNotFound: a space that does not exist.
func TestCreateRoleInUnknownSpaceIsNotFound(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	_, err := state.CreateRole(context.Background(), api, system, "01900000-0000-7000-8000-000000000f77", ownerRole())
	requireCode(t, err, model.CodeNotFound)
}
