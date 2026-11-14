package capability_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// system is the kernel acting by itself.
var system = model.Actor{Kind: model.ActorSystem}

// fakeClock is a Clock the tests move by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the frozen time.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to at.
func (c *fakeClock) Set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

// start is the instant every test clock begins at. It is in the real future
// of no one: grants never compare it with the database clock.
var start = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// world is a database with two active business spaces and an authorizer over
// the kernel catalog with a fake clock.
type world struct {
	t      *testing.T
	admin  *sql.DB
	api    *sql.DB
	clock  *fakeClock
	auth   *capability.Authorizer
	spaceA string
	spaceB string
}

// newWorld builds a world. The spaces are active so that writes pass.
func newWorld(t *testing.T) *world {
	t.Helper()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	clock := &fakeClock{now: start}
	auth, err := capability.NewAuthorizer(model.KernelCatalog(), clock)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	w := &world{t: t, admin: admin, api: api, clock: clock, auth: auth}
	w.spaceA = w.space("business", "world-a")
	w.spaceB = w.space("business", "world-b")
	return w
}

// space creates an active space of the given kind through the kernel, except a
// platform space, which only the administrator provisions.
func (w *world) space(kind, slug string) string {
	w.t.Helper()
	var id string
	if kind == string(model.SpaceKindPlatform) {
		id = "01900000-0000-7000-8000-0000000007f1"
		if _, err := w.admin.Exec(`INSERT INTO kernel.spaces (id, kind, name, slug, status) VALUES ($1, 'platform', 'Platform', $2, 'active')`, id, slug); err != nil {
			w.t.Fatalf("seed platform space: %v", err)
		}
		return id
	}
	id, err := state.CreateSpace(context.Background(), w.api, system, state.NewSpace{Kind: model.SpaceKind(kind), Name: slug, Slug: slug})
	if err != nil {
		w.t.Fatalf("CreateSpace: %v", err)
	}
	w.setStatus(id, "active")
	return id
}

// setStatus changes the status of a space as the administrator.
func (w *world) setStatus(spaceID, status string) {
	w.t.Helper()
	if _, err := w.admin.Exec(`UPDATE kernel.spaces SET status = $2 WHERE id = $1`, spaceID, status); err != nil {
		w.t.Fatalf("set status %s: %v", status, err)
	}
}

// person creates an active participant and returns its actor.
func (w *world) person(spaceID, name string) model.Actor {
	w.t.Helper()
	id, err := state.CreateParticipant(context.Background(), w.api, system, spaceID,
		state.NewParticipant{Kind: model.ParticipantKindPerson, DisplayName: name})
	if err != nil {
		w.t.Fatalf("CreateParticipant: %v", err)
	}
	return model.Actor{Kind: model.ActorPerson, ParticipantID: id}
}

// role creates a role with the given permissions and returns its id.
func (w *world) role(spaceID, key string, permissions ...string) string {
	w.t.Helper()
	id, err := state.CreateRole(context.Background(), w.api, system, spaceID,
		model.NewRole{Key: key, Name: key, Permissions: permissions})
	if err != nil {
		w.t.Fatalf("CreateRole %s: %v", key, err)
	}
	return id
}

// grant gives a role to a participant, with extra permissions, and returns
// the grant id.
func (w *world) grant(spaceID string, holder model.Actor, roleID string, extra ...string) string {
	w.t.Helper()
	return w.grantUntil(spaceID, holder, roleID, nil, extra...)
}

// grantUntil is grant with an expiry.
func (w *world) grantUntil(spaceID string, holder model.Actor, roleID string, expiresAt *time.Time, extra ...string) string {
	w.t.Helper()
	id, err := state.GrantRole(context.Background(), w.api, w.clock, system, spaceID, model.NewGrant{
		ParticipantID: holder.ParticipantID, RoleID: roleID, ExtraPermissions: extra, ExpiresAt: expiresAt,
	})
	if err != nil {
		w.t.Fatalf("GrantRole: %v", err)
	}
	return id
}

// holder creates a person with a new role that lists the permissions.
func (w *world) holder(spaceID, name string, permissions ...string) model.Actor {
	w.t.Helper()
	actor := w.person(spaceID, name)
	w.grant(spaceID, actor, w.role(spaceID, name+"_role", permissions...))
	return actor
}

// inSpace runs fn in a transaction of spaceID as the API role.
func (w *world) inSpace(spaceID string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	return state.InSpace(context.Background(), w.api, spaceID, fn)
}

// check runs Check in a transaction of spaceID.
func (w *world) check(spaceID string, actor model.Actor, req capability.Requirement) error {
	return w.inSpace(spaceID, func(ctx context.Context, tx *sql.Tx) error {
		return w.auth.Check(ctx, tx, actor, req)
	})
}

// requireCode fails unless err is a typed error of the given code.
func requireCode(t *testing.T, label string, err error, want model.Code) {
	t.Helper()
	got, typed := model.CodeOf(err)
	if !typed || got != want {
		t.Fatalf("%s: error = %v, want code %s", label, err, want)
	}
}

// requireAllowed fails when err is not nil.
func requireAllowed(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", label, err)
	}
}

// req builds a requirement for a permission.
func req(permission string) capability.Requirement {
	return capability.Requirement{Permission: permission}
}
