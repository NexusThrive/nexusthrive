package db_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// TestSpacesTableShape guards acceptance criterion 1: the catalog describes
// kernel.spaces with its columns, defaults, constraints and forced RLS.
func TestSpacesTableShape(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	wantColumns := map[string]struct{ dataType, nullable, def string }{
		"id":               {"uuid", "NO", ""},
		"kind":             {"text", "NO", ""},
		"name":             {"text", "NO", ""},
		"slug":             {"text", "NO", ""},
		"status":           {"text", "NO", "'provisioning'::text"},
		"parent_space_id":  {"uuid", "YES", ""},
		"locale":           {"text", "NO", "'es-CO'::text"},
		"timezone":         {"text", "NO", "'America/Bogota'::text"},
		"currency":         {"text", "NO", "'COP'::text"},
		"created_at":       {"timestamp with time zone", "NO", "now()"},
		"suspended_at":     {"timestamp with time zone", "YES", ""},
		"suspended_reason": {"text", "YES", ""},
		"closed_at":        {"timestamp with time zone", "YES", ""},
	}
	rows, err := admin.Query(`SELECT column_name, data_type, is_nullable, coalesce(column_default, '')
		FROM information_schema.columns WHERE table_schema = 'kernel' AND table_name = 'spaces'`)
	if err != nil {
		t.Fatalf("list columns: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, dataType, nullable, def string
		if err := rows.Scan(&name, &dataType, &nullable, &def); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		want, ok := wantColumns[name]
		if !ok {
			t.Errorf("unexpected column %s", name)
			continue
		}
		seen++
		if dataType != want.dataType || nullable != want.nullable || def != want.def {
			t.Errorf("column %s = (%s, %s, %q), want (%s, %s, %q)", name, dataType, nullable, def, want.dataType, want.nullable, want.def)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list columns: %v", err)
	}
	if seen != len(wantColumns) {
		t.Errorf("found %d columns, want %d", seen, len(wantColumns))
	}

	if got := count(t, admin, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'kernel' AND c.relname = 'spaces' AND c.relrowsecurity AND c.relforcerowsecurity`); got != 1 {
		t.Errorf("kernel.spaces must have RLS enabled and forced")
	}

	const insert = `INSERT INTO kernel.spaces (id, kind, name, slug, status) VALUES ($1, $2, 'n', $3, $4)`
	const idA, idB = "01900000-0000-7000-8000-00000000000a", "01900000-0000-7000-8000-00000000000b"
	if _, err := admin.Exec(insert, idA, "business", "test-space-a", "active"); err != nil {
		t.Fatalf("valid insert: %v", err)
	}
	for name, args := range map[string][]any{
		"duplicate slug": {idB, "business", "test-space-a", "active"},
		"unknown kind":   {idB, "unknown", "test-space-b", "active"},
		"unknown status": {idB, "business", "test-space-b", "gone"},
		"not a v7 uuid":  {"550e8400-e29b-41d4-a716-446655440000", "business", "test-space-b", "active"},
	} {
		if _, err := admin.Exec(insert, args...); err == nil {
			t.Errorf("%s: insert must fail", name)
		}
	}
	if _, err := admin.Exec(`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'platform', 'n', 'test-space-b')`, idB); err != nil {
		t.Fatalf("insert with defaults: %v", err)
	}
	var status string
	if err := admin.QueryRow(`SELECT status FROM kernel.spaces WHERE id = $1`, idB).Scan(&status); err != nil || status != "provisioning" {
		t.Errorf("default status = %q (%v), want provisioning", status, err)
	}
}

// TestAPIRoleIsLimited guards acceptance criterion 2.
func TestAPIRoleIsLimited(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	var super, bypass, createRole, createDB, login bool
	if err := admin.QueryRow(`SELECT rolsuper, rolbypassrls, rolcreaterole, rolcreatedb, rolcanlogin FROM pg_roles WHERE rolname = $1`, db.APIRole).
		Scan(&super, &bypass, &createRole, &createDB, &login); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if super || bypass || createRole || createDB || login {
		t.Errorf("role flags super=%v bypass=%v createrole=%v createdb=%v login=%v, want all false", super, bypass, createRole, createDB, login)
	}

	if got := count(t, admin, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_roles r ON r.oid = c.relowner WHERE n.nspname = 'kernel' AND r.rolname = $1`, db.APIRole); got != 0 {
		t.Errorf("API role owns %d relations in schema kernel, want 0", got)
	}
	if got := count(t, admin, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		JOIN pg_roles r ON r.oid = p.proowner WHERE n.nspname = 'kernel' AND r.rolname = $1`, db.APIRole); got != 0 {
		t.Errorf("API role owns %d functions in schema kernel, want 0", got)
	}
	if got := count(t, admin, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_roles r ON r.oid = c.relowner WHERE n.nspname = 'kernel' AND c.relname = 'spaces' AND r.rolname <> $1`, db.APIRole); got != 1 {
		t.Errorf("kernel.spaces must have an owner other than the API role")
	}

	privileges := func(query string, args ...any) bool {
		var ok bool
		if err := admin.QueryRow(query, args...).Scan(&ok); err != nil {
			t.Fatalf("privilege query %q: %v", query, err)
		}
		return ok
	}
	if !privileges(`SELECT has_schema_privilege($1, 'kernel', 'USAGE')`, db.APIRole) {
		t.Error("API role needs USAGE on schema kernel")
	}
	if privileges(`SELECT has_schema_privilege($1, 'kernel', 'CREATE')`, db.APIRole) {
		t.Error("API role must not have CREATE on schema kernel")
	}
	if !privileges(`SELECT has_table_privilege($1, 'kernel.spaces', 'SELECT')`, db.APIRole) {
		t.Error("API role needs SELECT on kernel.spaces")
	}
	// INSERT is granted by column only: the kernel sets status, created_at and
	// the suspension and closing columns itself.
	if privileges(`SELECT has_table_privilege($1, 'kernel.spaces', 'INSERT')`, db.APIRole) {
		t.Error("API role must not have table-wide INSERT on kernel.spaces")
	}
	for _, column := range []string{"id", "kind", "name", "slug"} {
		if !privileges(`SELECT has_column_privilege($1, 'kernel.spaces', $2, 'INSERT')`, db.APIRole, column) {
			t.Errorf("API role needs INSERT on kernel.spaces.%s", column)
		}
	}
	for _, column := range []string{"status", "created_at", "suspended_at", "suspended_reason", "closed_at"} {
		if privileges(`SELECT has_column_privilege($1, 'kernel.spaces', $2, 'INSERT')`, db.APIRole, column) {
			t.Errorf("API role must not have INSERT on kernel.spaces.%s", column)
		}
	}
	// UPDATE is granted by column only (state columns, never kind or id).
	if privileges(`SELECT has_table_privilege($1, 'kernel.spaces', 'UPDATE')`, db.APIRole) {
		t.Error("API role must not have table-wide UPDATE on kernel.spaces")
	}
	if !privileges(`SELECT has_column_privilege($1, 'kernel.spaces', 'name', 'UPDATE')`, db.APIRole) {
		t.Error("API role needs UPDATE on kernel.spaces.name")
	}
	for _, privilege := range []string{"DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
		if privileges(`SELECT has_table_privilege($1, 'kernel.spaces', $2)`, db.APIRole, privilege) {
			t.Errorf("API role must not have %s on kernel.spaces", privilege)
		}
	}
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		if privileges(`SELECT has_table_privilege($1, 'kernel.goose_db_version', $2)`, db.APIRole, privilege) {
			t.Errorf("API role must not have %s on kernel.goose_db_version", privilege)
		}
	}
}

// TestAPIRoleNameMatchesConstant keeps db.APIRole and the migration in step:
// the role the SQL creates is the one Go code switches to.
func TestAPIRoleNameMatchesConstant(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	if got := count(t, admin, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, db.APIRole); got != 1 {
		t.Errorf("role %q count = %d, want 1", db.APIRole, got)
	}
	if db.APIRole != "nexusthrive_api" {
		t.Errorf("APIRole = %q, want nexusthrive_api", db.APIRole)
	}
}

// TestMigrationsAreIdempotentWithExistingRole proves a database can be
// migrated when the cluster-wide role already exists, even in parallel.
func TestMigrationsAreIdempotentWithExistingRole(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	conns := make([]*sql.DB, 4)
	for i := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conns[i] = dbtest.Database(t)
		}()
	}
	wg.Wait()
	for i, conn := range conns {
		if conn == nil {
			t.Fatalf("database %d was not created", i)
		}
		if err := db.Migrate(context.Background(), conn, db.Config{Schema: "kernel"}); err != nil {
			t.Errorf("second migrate of database %d: %v", i, err)
		}
	}
}
