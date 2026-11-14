package laws_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// system is the kernel acting by itself.
var system = model.Actor{Kind: model.ActorSystem}

// clock is a Clock the test moves by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the frozen time.
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to at.
func (c *clock) Set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

// kernelTables lists every table of the kernel schema, read from the catalog
// of the database so that a table added later is counted without editing the
// test.
func kernelTables(t *testing.T, admin *sql.DB) []string {
	t.Helper()
	rows, err := admin.Query(`SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'kernel' AND c.relkind IN ('r', 'p') ORDER BY 1`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	return tables
}

// rowCounts counts the rows of every kernel table as the administrator, who
// sees every space.
func rowCounts(t *testing.T, admin *sql.DB, tables []string) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var n int
		query := "SELECT count(*) FROM kernel." + pgx.Identifier{table}.Sanitize()
		if err := admin.QueryRow(query).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

// diff lists the tables whose count changed, with the change.
func diff(before, after map[string]int) map[string]int {
	changed := map[string]int{}
	for table, n := range after {
		if n != before[table] {
			changed[table] = n - before[table]
		}
	}
	return changed
}

// TestLawPermissionDeniedLeavesNoRows guards criterion 10 of the permission law:
// when the check refuses an operation, no table of the kernel schema gains or
// loses a row. The operation under test checks first and writes a resource
// after; a positive control with permission raises exactly one table by one
// row, which proves the counting can see a write.
func TestLawPermissionDeniedLeavesNoRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	now := &clock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	auth, err := capability.NewAuthorizer(model.KernelCatalog(), now)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	tables := kernelTables(t, admin)
	if len(tables) < 12 {
		t.Fatalf("found %d kernel tables, expected the whole schema: %v", len(tables), tables)
	}

	newSpace := func(slug string) string {
		id, err := state.CreateSpace(ctx, api, system, state.NewSpace{Kind: model.SpaceKindBusiness, Name: slug, Slug: slug})
		if err != nil {
			t.Fatalf("CreateSpace: %v", err)
		}
		if _, err := admin.Exec(`UPDATE kernel.spaces SET status = 'active' WHERE id = $1`, id); err != nil {
			t.Fatalf("activate: %v", err)
		}
		return id
	}
	person := func(spaceID, name string) model.Actor {
		id, err := state.CreateParticipant(ctx, api, system, spaceID, state.NewParticipant{Kind: model.ParticipantKindPerson, DisplayName: name})
		if err != nil {
			t.Fatalf("CreateParticipant: %v", err)
		}
		return model.Actor{Kind: model.ActorPerson, ParticipantID: id}
	}
	holder := func(spaceID, name string, expiresAt *time.Time) model.Actor {
		actor := person(spaceID, name)
		role, err := state.CreateRole(ctx, api, system, spaceID,
			model.NewRole{Key: name + "_role", Name: name, Permissions: []string{model.PermissionResourceWrite}})
		if err != nil {
			t.Fatalf("CreateRole: %v", err)
		}
		if _, err := state.GrantRole(ctx, api, now, system, spaceID,
			model.NewGrant{ParticipantID: actor.ParticipantID, RoleID: role, ExpiresAt: expiresAt}); err != nil {
			t.Fatalf("GrantRole: %v", err)
		}
		return actor
	}

	space := newSpace("laws-space")
	otherSpace := newSpace("laws-other")
	const capabilityKey = "test.capability"

	// operate is the operation of the test: check, then write.
	code := 0
	operate := func(spaceID string, actor model.Actor, req capability.Requirement) error {
		code++
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatalf("uuid: %v", err)
		}
		return state.InSpace(ctx, api, spaceID, func(ctx context.Context, tx *sql.Tx) error {
			if err := auth.Check(ctx, tx, actor, req); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO kernel.resources (id, space_id, kind, code, name, scale)
				 VALUES ($1, kernel.current_space_id(), 'money', $2, 'Test resource', 2)`,
				id.String(), fmt.Sprintf("R%03d", code))
			return err
		})
	}
	write := capability.Requirement{Permission: model.PermissionResourceWrite}

	expiry := now.Now().Add(time.Hour)
	allowed := holder(space, "allowed", nil)
	expiring := holder(space, "expiring", &expiry)
	nobody := person(space, "nobody")
	foreigner := holder(otherSpace, "foreigner", nil)
	archived := holder(space, "archived", nil)
	if err := state.ArchiveParticipant(ctx, api, system, space, archived.ParticipantID); err != nil {
		t.Fatalf("ArchiveParticipant: %v", err)
	}

	type attempt struct {
		name  string
		space string
		actor model.Actor
		req   capability.Requirement
		want  model.Code
		setup func()
	}
	attempts := []attempt{
		{"no grant", space, nobody, write, model.CodePermissionDenied, nil},
		{"actor of another space", space, foreigner, write, model.CodePermissionDenied, nil},
		{"archived participant", space, archived, write, model.CodePermissionDenied, nil},
		{"null actor", space, model.Actor{}, write, model.CodePermissionDenied, nil},
		{"person without a participant", space, model.Actor{Kind: model.ActorPerson}, write, model.CodePermissionDenied, nil},
		{"unknown key", space, allowed, capability.Requirement{Permission: "kernel.nope.write"}, model.CodeInvalidInput, nil},
		{"capability not active", space, allowed, capability.Requirement{Permission: model.PermissionResourceWrite, Capability: capabilityKey}, model.CodeCapabilityInactive, nil},
		{"expired grant", space, expiring, write, model.CodePermissionDenied, func() { now.Set(expiry) }},
		{"suspended space", space, allowed, write, model.CodeSpaceSuspended, func() {
			if _, err := admin.Exec(`UPDATE kernel.spaces SET status = 'suspended' WHERE id = $1`, space); err != nil {
				t.Fatalf("suspend: %v", err)
			}
		}},
	}
	for _, a := range attempts {
		if a.setup != nil {
			a.setup()
		}
		before := rowCounts(t, admin, tables)
		err := operate(a.space, a.actor, a.req)
		if got, typed := model.CodeOf(err); !typed || got != a.want {
			t.Errorf("%s: error = %v, want code %s", a.name, err, a.want)
		}
		if changed := diff(before, rowCounts(t, admin, tables)); len(changed) != 0 {
			t.Errorf("%s: a denied operation changed the tables: %v", a.name, changed)
		}
	}

	// Positive control, on a space that is active again and a clock before the
	// expiry: exactly one table, resources, gains exactly one row.
	if _, err := admin.Exec(`UPDATE kernel.spaces SET status = 'active' WHERE id = $1`, space); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	now.Set(expiry.Add(-time.Minute))
	before := rowCounts(t, admin, tables)
	if err := operate(space, allowed, write); err != nil {
		t.Fatalf("the allowed operation failed: %v", err)
	}
	if changed := diff(before, rowCounts(t, admin, tables)); len(changed) != 1 || changed["resources"] != 1 {
		t.Errorf("the allowed operation changed %v, want only resources +1", changed)
	}
	if err := operate(space, expiring, write); err != nil {
		t.Errorf("the grant before its expiry was refused: %v", err)
	}
}
