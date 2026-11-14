package db_test

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// Fixed v7 identifiers for rows these tests insert as the administrator.
const (
	eventSpaceA       = "01900000-0000-7000-8000-0000000000a1"
	eventSpaceB       = "01900000-0000-7000-8000-0000000000b1"
	eventParticipantA = "01900000-0000-7000-8000-0000000000a2"
	eventID1          = "01900000-0000-7000-8000-0000000000e1"
	eventID2          = "01900000-0000-7000-8000-0000000000e2"
)

// seedEventFixtures inserts two spaces, a participant in space A and one event
// in space A, all as the administrator.
func seedEventFixtures(t *testing.T, admin *sql.DB) {
	t.Helper()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'A', 'event-a')`, []any{eventSpaceA}},
		{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'B', 'event-b')`, []any{eventSpaceB}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'P')`, []any{eventParticipantA, eventSpaceA}},
		{`INSERT INTO kernel.events (id, space_id, type, version, payload, actor_kind)
		  VALUES ($1, $2, 'kernel.test.recorded', 1, '{}', 'system')`, []any{eventID1, eventSpaceA}},
	} {
		if _, err := admin.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed %q: %v", stmt.query, err)
		}
	}
}

// pgCode returns the SQLSTATE of err, or "" when it is not a database error.
func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// asAPI runs query as the limited API role on a single connection and returns
// the error. The role is reset afterwards so the pooled connection is clean.
func asAPI(t *testing.T, admin *sql.DB, query string) error {
	t.Helper()
	conn, err := admin.Conn(context.Background())
	if err != nil {
		t.Fatalf("take connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "SET ROLE nexusthrive_api"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	defer conn.ExecContext(context.Background(), "RESET ROLE")
	_, err = conn.ExecContext(context.Background(), query)
	return err
}

// requireEventsIntact fails unless the seeded event is still there unchanged.
func requireEventsIntact(t *testing.T, admin *sql.DB) {
	t.Helper()
	if got := count(t, admin, `SELECT count(*) FROM kernel.events WHERE id = $1 AND type = 'kernel.test.recorded' AND version = 1`, eventID1); got != 1 {
		t.Errorf("the stored event changed or disappeared")
	}
}

// TestEventsRejectUpdateDeleteTruncateAsAPI guards criterion 3 for the API role.
func TestEventsRejectUpdateDeleteTruncateAsAPI(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedEventFixtures(t, admin)
	for _, query := range []string{
		`UPDATE kernel.events SET version = 2`,
		`DELETE FROM kernel.events`,
		`TRUNCATE kernel.events`,
	} {
		if got := pgCode(asAPI(t, admin, query)); got != "42501" {
			t.Errorf("%s as API: SQLSTATE %q, want 42501", query, got)
		}
	}
	requireEventsIntact(t, admin)
}

// TestEventsRejectUpdateDeleteTruncateAsAdmin guards criterion 3 for the owner
// and superuser, who hold every privilege and are stopped by the trigger.
func TestEventsRejectUpdateDeleteTruncateAsAdmin(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedEventFixtures(t, admin)
	for _, query := range []string{
		`UPDATE kernel.events SET version = 2`,
		`DELETE FROM kernel.events`,
		`TRUNCATE kernel.events`,
		`TRUNCATE kernel.spaces CASCADE`,
	} {
		_, err := admin.Exec(query)
		if got := pgCode(err); got != "23001" {
			t.Errorf("%s as admin: SQLSTATE %q (%v), want 23001 restrict_violation", query, got, err)
		}
	}
	requireEventsIntact(t, admin)
}

// TestTriggerHoldsEvenIfGrantedByMistake shows the second layer: even with the
// privileges and the row security policies a careless migration could add, the
// API role still cannot change an event.
func TestTriggerHoldsEvenIfGrantedByMistake(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedEventFixtures(t, admin)
	for _, stmt := range []string{
		`GRANT UPDATE, DELETE ON kernel.events TO nexusthrive_api`,
		`CREATE POLICY mistaken_update ON kernel.events FOR UPDATE USING (true) WITH CHECK (true)`,
		`CREATE POLICY mistaken_delete ON kernel.events FOR DELETE USING (true)`,
	} {
		if _, err := admin.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for _, query := range []string{`UPDATE kernel.events SET version = 2`, `DELETE FROM kernel.events`} {
		tx, err := admin.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(`SET LOCAL ROLE nexusthrive_api`); err != nil {
			t.Fatalf("set role: %v", err)
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, eventSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		_, err = tx.Exec(query)
		_ = tx.Rollback()
		if got := pgCode(err); got != "23001" {
			t.Errorf("%s after a mistaken grant and policy: SQLSTATE %q (%v), want 23001", query, got, err)
		}
	}
	requireEventsIntact(t, admin)
}

// TestEventsSeqCannotBeSet checks that seq is generated always.
func TestEventsSeqCannotBeSet(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedEventFixtures(t, admin)
	_, err := admin.Exec(`INSERT INTO kernel.events (seq, id, space_id, type, version, payload, actor_kind)
		VALUES (999, $1, $2, 'kernel.test.recorded', 1, '{}', 'system')`, eventID2, eventSpaceA)
	if got := pgCode(err); got != "428C9" {
		t.Errorf("explicit seq: SQLSTATE %q (%v), want 428C9", got, err)
	}
}

// TestEventsCheckConstraints exercises each CHECK by SQL as the administrator,
// which bypasses RLS: criteria 4, 5 and 6.
func TestEventsCheckConstraints(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedEventFixtures(t, admin)
	const insert = `INSERT INTO kernel.events (id, space_id, type, version, subject_ref, payload, actor_participant_id, actor_kind, request_id)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)`
	type row struct {
		id, typ, payload, kind string
		version                int
		subject, request       any
		actor                  any
	}
	good := row{id: eventID2, typ: "kernel.test.recorded", version: 1, payload: `{}`, kind: "system"}
	cases := map[string]func(*row){
		"id not v7":          func(r *row) { r.id = "01900000-0000-4000-8000-0000000000e2" },
		"type one segment":   func(r *row) { r.typ = "recorded" },
		"type uppercase":     func(r *row) { r.typ = "Kernel.test" },
		"type version tail":  func(r *row) { r.typ = "kernel.test.recorded.v1" },
		"type too long":      func(r *row) { r.typ = "kernel." + strings.Repeat("a", 125) },
		"version zero":       func(r *row) { r.version = 0 },
		"payload array":      func(r *row) { r.payload = `[]` },
		"payload null":       func(r *row) { r.payload = `null` },
		"payload scalar":     func(r *row) { r.payload = `1` },
		"unknown actor kind": func(r *row) { r.kind = "robot" },
		"blank subject":      func(r *row) { r.subject = " " },
		"blank request":      func(r *row) { r.request = " " },
		"no actor":           func(r *row) { r.kind = "person" },
	}
	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := good
		cases[name](&r)
		_, err := admin.Exec(insert, r.id, eventSpaceA, r.typ, r.version, r.subject, r.payload, r.actor, r.kind, r.request)
		if got := pgCode(err); got != "23514" {
			t.Errorf("%s: SQLSTATE %q (%v), want 23514", name, got, err)
		}
	}
	// A system event with no participant and a person event with one both work.
	if _, err := admin.Exec(insert, eventID2, eventSpaceA, good.typ, 1, nil, `{}`, nil, "system", nil); err != nil {
		t.Errorf("valid system event rejected: %v", err)
	}
}

// TestEventActorMustBelongToSpace checks the composite foreign key: an actor
// from another space cannot be named.
func TestEventActorMustBelongToSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedEventFixtures(t, admin)
	const insert = `INSERT INTO kernel.events (id, space_id, type, version, payload, actor_participant_id, actor_kind)
		VALUES ($1, $2, 'kernel.test.recorded', 1, '{}', $3, 'person')`
	if _, err := admin.Exec(insert, eventID2, eventSpaceA, eventParticipantA); err != nil {
		t.Fatalf("actor of the same space rejected: %v", err)
	}
	_, err := admin.Exec(insert, "01900000-0000-7000-8000-0000000000e3", eventSpaceB, eventParticipantA)
	if got := pgCode(err); got != "23503" {
		t.Errorf("actor of another space: SQLSTATE %q (%v), want 23503", got, err)
	}
}

// TestEventsForcedRLSAndPolicies checks RLS, the exact policies and the grants.
func TestEventsForcedRLSAndPolicies(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	requireForcedRLS(t, admin, "events")

	rows, err := admin.Query(`SELECT cmd FROM pg_policies WHERE schemaname = 'kernel' AND tablename = 'events' ORDER BY cmd`)
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	defer rows.Close()
	var cmds []string
	for rows.Next() {
		var cmd string
		if err := rows.Scan(&cmd); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cmds = append(cmds, cmd)
	}
	if len(cmds) != 2 || cmds[0] != "INSERT" || cmds[1] != "SELECT" {
		t.Errorf("event policies = %v, want [INSERT SELECT]", cmds)
	}

	grants, err := admin.Query(`SELECT privilege_type FROM information_schema.role_table_grants
		WHERE table_schema = 'kernel' AND table_name = 'events' AND grantee = 'nexusthrive_api' ORDER BY 1`)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	defer grants.Close()
	var got []string
	for grants.Next() {
		var p string
		if err := grants.Scan(&p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, p)
	}
	if len(got) != 2 || got[0] != "INSERT" || got[1] != "SELECT" {
		t.Errorf("API grants on events = %v, want [INSERT SELECT]", got)
	}
}
