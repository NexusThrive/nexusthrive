package db_test

import (
	"database/sql"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// Fixed v7 identifiers for the rows these tests insert.
const (
	pgRoleOne   = "01900000-0000-7000-8000-000000000601"
	pgRoleTwo   = "01900000-0000-7000-8000-000000000602"
	pgRoleThree = "01900000-0000-7000-8000-000000000603"
	pgGrantOne  = "01900000-0000-7000-8000-000000000611"
	pgGrantTwo  = "01900000-0000-7000-8000-000000000612"
)

// restrictViolation is the SQLSTATE the guards raise.
const restrictViolation = "23001"

// insufficientPrivilege is the SQLSTATE of an action the role may not do.
const insufficientPrivilege = "42501"

// apiCommitInSpace runs one statement as the API role inside spaceID and
// commits it.
func apiCommitInSpace(t *testing.T, api *sql.DB, spaceID, query string, args ...any) error {
	t.Helper()
	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, spaceID); err != nil {
		tx.Rollback()
		t.Fatalf("set space: %v", err)
	}
	if _, err := tx.Exec(query, args...); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// TestPlatformOnlyPermissionsRejectedOutsidePlatformSpace guards criterion 8
// in the database: a role or a grant of a space that is not a platform space
// can never carry kernel.capability.write, for the API role and for the
// administrator alike, on insert and on update, alone or mixed with others.
func TestPlatformOnlyPermissionsRejectedOutsidePlatformSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)
	rolesBefore := count(t, admin, `SELECT count(*) FROM kernel.roles`)
	grantsBefore := count(t, admin, `SELECT count(*) FROM kernel.grants`)

	const insertRole = `INSERT INTO kernel.roles (id, space_id, key, name, permissions, system) VALUES ($1, $2, 'sneaky', 'Sneaky', $3::text[], false)`
	for name, perms := range map[string]string{
		"alone":           `{kernel.capability.write}`,
		"mixed":           `{kernel.ledger.read,kernel.capability.write}`,
		"repeated":        `{kernel.capability.write,kernel.capability.write}`,
		"after the first": `{kernel.ledger.read,kernel.grant.write,kernel.capability.write}`,
	} {
		err := apiExecInSpace(t, api, gSpaceA, insertRole, pgRoleOne, gSpaceA, perms)
		requireCode(t, "API role, role "+name, err, restrictViolation)
		_, err = admin.Exec(insertRole, pgRoleOne, gSpaceA, perms)
		requireCode(t, "administrator, role "+name, err, restrictViolation)
	}
	// Also for a space of another kind than business: the rule is "not platform".
	for _, kind := range []string{"community", "personal"} {
		const space = "01900000-0000-7000-8000-000000000691"
		if _, err := admin.Exec(`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, $2, 'K', 'guard-' || $2)`, space, kind); err != nil {
			t.Fatalf("seed %s space: %v", kind, err)
		}
		_, err := admin.Exec(insertRole, pgRoleOne, space, `{kernel.capability.write}`)
		requireCode(t, kind+" space role", err, restrictViolation)
		if _, err := admin.Exec(`DELETE FROM kernel.spaces WHERE id = $1`, space); err != nil {
			t.Fatalf("remove %s space: %v", kind, err)
		}
	}

	const insertGrant = `INSERT INTO kernel.grants (id, space_id, participant_id, role_id, extra_permissions) VALUES ($1, $2, $3, $4, $5::text[])`
	for name, perms := range map[string]string{
		"alone": `{kernel.capability.write}`,
		"mixed": `{kernel.event.read,kernel.capability.write}`,
	} {
		err := apiExecInSpace(t, api, gSpaceA, insertGrant, pgGrantOne, gSpaceA, gPartA2, gRoleA, perms)
		requireCode(t, "API role, grant "+name, err, restrictViolation)
		_, err = admin.Exec(insertGrant, pgGrantOne, gSpaceA, gPartA2, gRoleA, perms)
		requireCode(t, "administrator, grant "+name, err, restrictViolation)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.roles`); got != rolesBefore {
		t.Errorf("roles = %d, want %d: a rejected insert left a row", got, rolesBefore)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants`); got != grantsBefore {
		t.Errorf("grants = %d, want %d: a rejected insert left a row", got, grantsBefore)
	}

	// The owner of the tables cannot widen an existing role either.
	_, err := admin.Exec(`UPDATE kernel.roles SET permissions = ARRAY['kernel.capability.write'] WHERE id = $1`, gRoleA)
	requireCode(t, "administrator widening a role", err, restrictViolation)
	if got := count(t, admin, `SELECT count(*) FROM kernel.roles WHERE permissions @> ARRAY['kernel.capability.write']`); got != 0 {
		t.Errorf("%d roles hold kernel.capability.write", got)
	}

	// Positive controls: ordinary keys, and a platform space, are accepted.
	if err := apiExecInSpace(t, api, gSpaceA, insertRole, pgRoleTwo, gSpaceA, `{kernel.ledger.read,kernel.capability.writer}`); err != nil {
		t.Errorf("a role with ordinary keys was rejected: %v", err)
	}
	if err := apiExecInSpace(t, api, gSpaceA, insertGrant, pgGrantOne, gSpaceA, gPartA2, gRoleA, `{kernel.event.read}`); err != nil {
		t.Errorf("a grant with an ordinary key was rejected: %v", err)
	}
	if _, err := admin.Exec(insertRole, pgRoleThree, gSpacePlat, `{kernel.capability.write}`); err != nil {
		t.Errorf("a platform space role was rejected: %v", err)
	}
	if _, err := admin.Exec(insertGrant, pgGrantTwo, gSpacePlat, gStaff2, gRolePlat, `{kernel.capability.write}`); err != nil {
		t.Errorf("a platform space grant was rejected: %v", err)
	}
}

// TestPlatformOnlyListMatchesCatalog ties the fixed list of the database to
// the PlatformOnly flags of the Go catalog, in both directions.
func TestPlatformOnlyListMatchesCatalog(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	got := stringColumn(t, admin, `SELECT unnest(kernel.platform_only_permissions()) ORDER BY 1`)
	want := model.KernelCatalog().PlatformOnlyKeys()
	if !slices.Equal(got, want) {
		t.Errorf("database list = %v, catalog = %v", got, want)
	}
	if len(want) == 0 {
		t.Error("the catalog has no platform-only key")
	}
}

// TestClosedSpaceStaysClosed guards the terminal state: neither the API role
// nor the administrator reopens a closed space or erases its closing date, and
// the closing date is the database clock, never a value the writer chooses.
func TestClosedSpaceStaysClosed(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	// A client date is replaced by the clock of the database.
	if err := apiCommitInSpace(t, api, gSpaceA,
		`UPDATE kernel.spaces SET status = 'closed', closed_at = '2000-01-01T00:00:00Z' WHERE id = $1`, gSpaceA); err != nil {
		t.Fatalf("closing a space: %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.spaces WHERE id = $1 AND status = 'closed'
		AND closed_at > now() - interval '1 minute' AND closed_at <= now()`, gSpaceA); got != 1 {
		t.Fatalf("closed_at was not stamped with the database clock")
	}
	var stamped string
	if err := admin.QueryRow(`SELECT closed_at::text FROM kernel.spaces WHERE id = $1`, gSpaceA).Scan(&stamped); err != nil {
		t.Fatalf("read closed_at: %v", err)
	}

	for name, query := range map[string]string{
		"reopen as active":       `UPDATE kernel.spaces SET status = 'active' WHERE id = $1`,
		"reopen as suspended":    `UPDATE kernel.spaces SET status = 'suspended' WHERE id = $1`,
		"reopen as provisioning": `UPDATE kernel.spaces SET status = 'provisioning' WHERE id = $1`,
		"erase the closing date": `UPDATE kernel.spaces SET closed_at = NULL WHERE id = $1`,
		"change the closing":     `UPDATE kernel.spaces SET closed_at = '2000-01-01T00:00:00Z' WHERE id = $1`,
		"reopen and erase":       `UPDATE kernel.spaces SET status = 'active', closed_at = NULL WHERE id = $1`,
	} {
		requireCode(t, "API role: "+name, apiExecInSpace(t, api, gSpaceA, query, gSpaceA), restrictViolation)
		_, err := admin.Exec(query, gSpaceA)
		requireCode(t, "administrator: "+name, err, restrictViolation)
	}
	var after string
	if err := admin.QueryRow(`SELECT closed_at::text FROM kernel.spaces WHERE id = $1 AND status = 'closed'`, gSpaceA).Scan(&after); err != nil || after != stamped {
		t.Errorf("closed space changed: closed_at %q -> %q (%v)", stamped, after, err)
	}

	// Other columns of a closed space are not the guard's business, and
	// writing closed to a closed space is no change.
	if _, err := admin.Exec(`UPDATE kernel.spaces SET name = 'Renamed', status = 'closed' WHERE id = $1`, gSpaceA); err != nil {
		t.Errorf("renaming a closed space: %v", err)
	}

	// A space that is not closed carries no closing date, and a legitimate
	// transition from suspended to closed still works.
	requireCode(t, "closing date on an open space",
		apiExecInSpace(t, api, gSpaceB, `UPDATE kernel.spaces SET closed_at = now() WHERE id = $1`, gSpaceB), restrictViolation)
	if err := apiCommitInSpace(t, api, gSpaceB, `UPDATE kernel.spaces SET status = 'suspended' WHERE id = $1`, gSpaceB); err != nil {
		t.Fatalf("suspending: %v", err)
	}
	if err := apiCommitInSpace(t, api, gSpaceB, `UPDATE kernel.spaces SET status = 'active' WHERE id = $1`, gSpaceB); err != nil {
		t.Errorf("reactivating a suspended space is still allowed in M1: %v", err)
	}
	if err := apiCommitInSpace(t, api, gSpaceB, `UPDATE kernel.spaces SET status = 'closed' WHERE id = $1`, gSpaceB); err != nil {
		t.Errorf("closing an active space: %v", err)
	}
}

// guardFunctions are the three functions of migration 00011.
var guardFunctions = []string{"guard_platform_only_permissions", "guard_space_closed", "platform_only_permissions"}

// TestGuardFunctionsHaveFixedSearchPathAndNoDefiner checks the hardening
// lesson on the new functions: a fixed search_path and no SECURITY DEFINER.
func TestGuardFunctionsHaveFixedSearchPathAndNoDefiner(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	got := stringColumn(t, admin, `SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'kernel' AND p.proname IN ('guard_platform_only_permissions', 'guard_space_closed', 'platform_only_permissions')
		AND NOT p.prosecdef AND p.proconfig IS NOT NULL
		AND 'search_path=pg_catalog, pg_temp' = ANY (p.proconfig) ORDER BY 1`)
	if !slices.Equal(got, guardFunctions) {
		t.Errorf("functions with a pinned search_path and no definer = %v, want %v", got, guardFunctions)
	}
}

// TestAPIRoleCannotDisableOrReplaceGuards shows the role of the API has no way
// to switch off, drop or rewrite the new triggers, and that they still work
// after every attempt.
func TestAPIRoleCannotDisableOrReplaceGuards(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	for _, table := range []string{"roles", "grants", "spaces"} {
		if got := count(t, admin, `SELECT count(*) FROM information_schema.role_table_grants
			WHERE table_schema = 'kernel' AND table_name = $1 AND grantee = 'nexusthrive_api' AND privilege_type = 'TRIGGER'`, table); got != 0 {
			t.Errorf("the API role holds TRIGGER on kernel.%s", table)
		}
	}
	attempts := []string{
		`ALTER TABLE kernel.roles DISABLE TRIGGER ALL`,
		`ALTER TABLE kernel.roles DISABLE TRIGGER USER`,
		`ALTER TABLE kernel.grants DISABLE TRIGGER USER`,
		`ALTER TABLE kernel.spaces DISABLE TRIGGER USER`,
		`ALTER TABLE kernel.roles DISABLE ROW LEVEL SECURITY`,
		`DROP TRIGGER roles_guard_platform_only ON kernel.roles`,
		`DROP TRIGGER grants_guard_platform_only ON kernel.grants`,
		`DROP TRIGGER spaces_guard_closed ON kernel.spaces`,
		`DROP FUNCTION kernel.guard_platform_only_permissions() CASCADE`,
		`DROP FUNCTION kernel.guard_space_closed() CASCADE`,
		`CREATE OR REPLACE FUNCTION kernel.guard_space_closed() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
		`CREATE OR REPLACE FUNCTION kernel.platform_only_permissions() RETURNS text[] LANGUAGE sql AS 'SELECT ARRAY[]::text[]'`,
		`ALTER FUNCTION kernel.guard_space_closed() SET search_path = public`,
		`SET session_replication_role = replica`,
	}
	for _, attempt := range attempts {
		_, err := api.Exec(attempt)
		requireCode(t, "API role: "+attempt, err, insufficientPrivilege)
	}

	// The guards still hold after every attempt.
	err := apiExecInSpace(t, api, gSpaceA,
		`INSERT INTO kernel.roles (id, space_id, key, name, permissions, system) VALUES ($1, $2, 'x', 'X', ARRAY['kernel.capability.write'], false)`,
		pgRoleOne, gSpaceA)
	requireCode(t, "role after the attempts", err, restrictViolation)
	if err := apiCommitInSpace(t, api, gSpaceA, `UPDATE kernel.spaces SET status = 'closed' WHERE id = $1`, gSpaceA); err != nil {
		t.Fatalf("closing: %v", err)
	}
	requireCode(t, "reopening after the attempts",
		apiExecInSpace(t, api, gSpaceA, `UPDATE kernel.spaces SET status = 'active' WHERE id = $1`, gSpaceA), restrictViolation)
}

// TestPermissionGuardsAddNoTablesOrPrivileges compares the schema before and
// after migration 00011: it adds exactly the three functions and the three
// triggers, and changes no table, column, constraint, policy or privilege.
func TestPermissionGuardsAddNoTablesOrPrivileges(t *testing.T) {
	t.Parallel()
	all := db.KernelMigrations()
	entries, err := fs.ReadDir(all, ".")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	before := fstest.MapFS{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "00011_") {
			continue
		}
		raw, err := fs.ReadFile(all, entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		before[entry.Name()] = &fstest.MapFile{Data: raw}
	}
	if len(before) != len(entries)-1 {
		t.Fatalf("migration 00011 not found among %d files", len(entries))
	}
	oldSchema := schemaSnapshot(t, dbtest.DatabaseWith(t, before))
	newSchema := schemaSnapshot(t, dbtest.Database(t))

	for kind, rows := range newSchema {
		if kind == "functions" || kind == "triggers" {
			continue
		}
		if !slices.Equal(rows, oldSchema[kind]) {
			t.Errorf("%s changed:\nbefore %v\nafter  %v", kind, oldSchema[kind], rows)
		}
	}
	var addedFunctions []string
	for _, row := range newSchema["functions"] {
		if !slices.Contains(oldSchema["functions"], row) {
			addedFunctions = append(addedFunctions, row)
		}
	}
	if !slices.Equal(addedFunctions, guardFunctions) || len(newSchema["functions"]) != len(oldSchema["functions"])+3 {
		t.Errorf("added functions = %v, want %v", addedFunctions, guardFunctions)
	}
	if len(newSchema["triggers"]) != len(oldSchema["triggers"])+3 {
		t.Errorf("triggers: %d before, %d after, want 3 more", len(oldSchema["triggers"]), len(newSchema["triggers"]))
	}
}

// schemaSnapshot lists, by kind, everything that describes the kernel schema
// and its privileges for the API role.
func schemaSnapshot(t *testing.T, conn *sql.DB) map[string][]string {
	t.Helper()
	queries := map[string]string{
		"columns": `SELECT table_name || '.' || column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns WHERE table_schema = 'kernel' ORDER BY 1`,
		"tables": `SELECT c.relname || ':' || c.relkind::text || ':' || c.relrowsecurity::text || ':' || c.relforcerowsecurity::text
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'kernel' ORDER BY 1`,
		"constraints": `SELECT conrelid::regclass || ':' || conname || ':' || pg_get_constraintdef(oid)
			FROM pg_constraint WHERE connamespace = 'kernel'::regnamespace ORDER BY 1`,
		"policies": `SELECT tablename || ':' || policyname || ':' || cmd || ':' || coalesce(qual, '') || ':' || coalesce(with_check, '')
			FROM pg_policies WHERE schemaname = 'kernel' ORDER BY 1`,
		"table privileges": `SELECT table_name || ':' || privilege_type FROM information_schema.role_table_grants
			WHERE table_schema = 'kernel' AND grantee = 'nexusthrive_api' ORDER BY 1`,
		"column privileges": `SELECT table_name || '.' || column_name || ':' || privilege_type FROM information_schema.column_privileges
			WHERE table_schema = 'kernel' AND grantee = 'nexusthrive_api' ORDER BY 1`,
		"functions": `SELECT p.proname FROM pg_proc p WHERE p.pronamespace = 'kernel'::regnamespace ORDER BY 1`,
		"triggers": `SELECT tgrelid::regclass || ':' || tgname FROM pg_trigger WHERE NOT tgisinternal
			AND tgrelid IN (SELECT oid FROM pg_class WHERE relnamespace = 'kernel'::regnamespace) ORDER BY 1`,
	}
	out := map[string][]string{}
	for kind, query := range queries {
		out[kind] = stringColumn(t, conn, query)
		if len(out[kind]) == 0 {
			t.Fatalf("snapshot %q is empty: the query is wrong", kind)
		}
	}
	return out
}
