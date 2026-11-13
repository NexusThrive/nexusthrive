package db_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// columnSet returns the columns of a kernel table with their nullability.
func columnSet(t *testing.T, admin *sql.DB, table string) map[string]string {
	t.Helper()
	rows, err := admin.Query(`SELECT column_name, is_nullable FROM information_schema.columns
		WHERE table_schema = 'kernel' AND table_name = $1`, table)
	if err != nil {
		t.Fatalf("list columns of %s: %v", table, err)
	}
	defer rows.Close()
	columns := map[string]string{}
	for rows.Next() {
		var name, nullable string
		if err := rows.Scan(&name, &nullable); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = nullable
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list columns of %s: %v", table, err)
	}
	return columns
}

// requireForcedRLS fails unless the table has row level security enabled and forced.
func requireForcedRLS(t *testing.T, admin *sql.DB, table string) {
	t.Helper()
	if got := count(t, admin, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'kernel' AND c.relname = $1 AND c.relrowsecurity AND c.relforcerowsecurity`, table); got != 1 {
		t.Errorf("kernel.%s must have RLS enabled and forced", table)
	}
}

// TestIdentitiesTableShape checks the identities table: no space column, a
// unique (provider, subject), a provider check and per-command policies.
func TestIdentitiesTableShape(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	columns := columnSet(t, admin, "identities")
	for name, nullable := range map[string]string{
		"id": "NO", "provider": "NO", "subject": "NO", "primary_email": "YES",
		"display_name": "NO", "last_seen_at": "YES", "disabled_at": "YES",
	} {
		if got, ok := columns[name]; !ok || got != nullable {
			t.Errorf("column %s = (%q, present %v), want nullable %s", name, got, ok, nullable)
		}
	}
	if _, ok := columns["space_id"]; ok {
		t.Error("identities must not have space_id")
	}
	if len(columns) != 7 {
		t.Errorf("identities has %d columns, want 7", len(columns))
	}

	if got := count(t, admin, `SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'kernel.identities'::regclass AND contype = 'u' AND conname = 'identities_provider_subject_key'`); got != 1 {
		t.Error("identities needs a unique constraint on (provider, subject)")
	}
	if _, err := admin.Exec(`INSERT INTO kernel.identities (id, provider, subject, display_name)
		VALUES ('01900000-0000-7000-8000-000000000001', 'google', 's', 'n')`); err == nil {
		t.Error("an unknown provider must be rejected")
	}
	requireForcedRLS(t, admin, "identities")

	rows, err := admin.Query(`SELECT cmd FROM pg_policies WHERE schemaname = 'kernel' AND tablename = 'identities' ORDER BY cmd`)
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	defer rows.Close()
	var cmds []string
	for rows.Next() {
		var cmd string
		if err := rows.Scan(&cmd); err != nil {
			t.Fatalf("scan policy: %v", err)
		}
		cmds = append(cmds, cmd)
	}
	if len(cmds) != 2 || cmds[0] != "INSERT" || cmds[1] != "SELECT" {
		t.Errorf("identities policies = %v, want [INSERT SELECT]", cmds)
	}
}

// TestParticipantsTableShape checks columns, the mandatory space, the partial
// unique index and forced RLS.
func TestParticipantsTableShape(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	columns := columnSet(t, admin, "participants")
	for name, nullable := range map[string]string{
		"id": "NO", "space_id": "NO", "identity_id": "YES", "kind": "NO", "display_name": "NO",
		"legal_name": "YES", "document_type": "YES", "document_number": "YES",
		"country_code": "YES", "status": "NO", "archived_at": "YES",
	} {
		if got, ok := columns[name]; !ok || got != nullable {
			t.Errorf("column %s = (%q, present %v), want nullable %s", name, got, ok, nullable)
		}
	}
	if len(columns) != 11 {
		t.Errorf("participants has %d columns, want 11", len(columns))
	}

	var def string
	if err := admin.QueryRow(`SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'kernel' AND indexname = 'participants_space_identity_key'`).Scan(&def); err != nil {
		t.Fatalf("read unique index: %v", err)
	}
	for _, part := range []string{"UNIQUE", "(space_id, identity_id)", "identity_id IS NOT NULL"} {
		if !strings.Contains(def, part) {
			t.Errorf("index definition %q lacks %q", def, part)
		}
	}
	requireForcedRLS(t, admin, "participants")
}

// TestAPIRoleGrantsOnParticipantTables checks the API role cannot modify
// identities, cannot delete anywhere and may update only the archive columns.
func TestAPIRoleGrantsOnParticipantTables(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	has := func(query string, args ...any) bool {
		var ok bool
		if err := admin.QueryRow(query, args...).Scan(&ok); err != nil {
			t.Fatalf("privilege query %q: %v", query, err)
		}
		return ok
	}
	for _, table := range []string{"kernel.identities", "kernel.participants"} {
		for _, privilege := range []string{"SELECT", "INSERT"} {
			if !has(`SELECT has_table_privilege($1, $2, $3)`, db.APIRole, table, privilege) {
				t.Errorf("API role needs %s on %s", privilege, table)
			}
		}
		for _, privilege := range []string{"DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
			if has(`SELECT has_table_privilege($1, $2, $3)`, db.APIRole, table, privilege) {
				t.Errorf("API role must not have %s on %s", privilege, table)
			}
		}
	}
	if has(`SELECT has_table_privilege($1, 'kernel.identities', 'UPDATE')`, db.APIRole) {
		t.Error("API role must not have UPDATE on kernel.identities")
	}
	if has(`SELECT has_table_privilege($1, 'kernel.participants', 'UPDATE')`, db.APIRole) &&
		has(`SELECT has_column_privilege($1, 'kernel.participants', 'space_id', 'UPDATE')`, db.APIRole) {
		t.Error("API role must not update participants.space_id")
	}
	for column, want := range map[string]bool{
		"status": true, "archived_at": true,
		"space_id": false, "identity_id": false, "kind": false, "display_name": false, "id": false,
	} {
		if got := has(`SELECT has_column_privilege($1, 'kernel.participants', $2, 'UPDATE')`, db.APIRole, column); got != want {
			t.Errorf("UPDATE on participants.%s = %v, want %v", column, got, want)
		}
	}
}
