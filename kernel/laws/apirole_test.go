package laws_test

import (
	"database/sql"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// rowQuerier is what apiRoleFindings needs: a connection or a transaction.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// apiRoleFindings checks that the role of the API cannot go around row level
// security: it is not a superuser, it has no BYPASSRLS, it cannot create roles
// or databases or replicate, it owns no table, function or schema of the
// database, and it is a member of no other role that could.
func apiRoleFindings(t *testing.T, q rowQuerier) []string {
	t.Helper()
	var findings []string
	var super, bypass, createRole, createDB, replication bool
	err := q.QueryRow(`SELECT rolsuper, rolbypassrls, rolcreaterole, rolcreatedb, rolreplication FROM pg_roles WHERE rolname = $1`, db.APIRole).
		Scan(&super, &bypass, &createRole, &createDB, &replication)
	if err != nil {
		t.Fatalf("read the role %s: %v", db.APIRole, err)
	}
	for attribute, set := range map[string]bool{
		"SUPERUSER": super, "BYPASSRLS": bypass, "CREATEROLE": createRole, "CREATEDB": createDB, "REPLICATION": replication,
	} {
		if set {
			findings = append(findings, "the API role has "+attribute)
		}
	}

	var owned int
	err = q.QueryRow(`SELECT
		(SELECT count(*) FROM pg_class c WHERE c.relowner = r.oid)
		+ (SELECT count(*) FROM pg_proc p WHERE p.proowner = r.oid)
		+ (SELECT count(*) FROM pg_namespace s WHERE s.nspowner = r.oid)
		+ (SELECT count(*) FROM pg_type y WHERE y.typowner = r.oid AND y.typrelid = 0)
		FROM pg_roles r WHERE r.rolname = $1`, db.APIRole).Scan(&owned)
	if err != nil {
		t.Fatalf("count what the role owns: %v", err)
	}
	if owned != 0 {
		findings = append(findings, "the API role owns objects of the database")
	}

	var parents int
	err = q.QueryRow(`SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = $1`, db.APIRole).Scan(&parents)
	if err != nil {
		t.Fatalf("count the parent roles of the role: %v", err)
	}
	if parents != 0 {
		findings = append(findings, "the API role is a member of another role")
	}
	return findings
}

// TestLawPermissionAPIRoleCannotBypassIsolation guards criterion 4: the role
// the API connects as has no superuser, BYPASSRLS or any other attribute that
// weakens isolation, and owns no table. It checks the catalog and, through a
// pool whose connections run as that role, the live session, where the
// settings of the session must say the same.
func TestLawPermissionAPIRoleCannotBypassIsolation(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	for _, finding := range apiRoleFindings(t, admin) {
		t.Errorf("%s", finding)
	}

	var user, superuser string
	var bypass bool
	if err := api.QueryRow(`SELECT current_user, current_setting('is_superuser'), r.rolbypassrls FROM pg_roles r WHERE r.rolname = current_user`).
		Scan(&user, &superuser, &bypass); err != nil {
		t.Fatalf("read the live session: %v", err)
	}
	if user != db.APIRole || superuser != "off" || bypass {
		t.Errorf("live session = user %s, superuser %s, bypassrls %v; want %s, off, false", user, superuser, bypass, db.APIRole)
	}

	// A table the role owns would be outside the FORCE of row level security
	// only for its owner; none exists, and the owner of every table of the
	// schema is somebody else.
	var owners int
	if err := admin.QueryRow(`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'kernel' AND c.relkind IN ('r', 'p') AND pg_get_userbyid(c.relowner) = $1`, db.APIRole).Scan(&owners); err != nil {
		t.Fatalf("count tables owned by the role: %v", err)
	}
	if owners != 0 {
		t.Errorf("the API role owns %d tables of the schema", owners)
	}
}

// TestAPIRoleVerifierDetectsWeakRoles proves the check is not vacuous: each
// dangerous attribute, and ownership of a table, is reported. ALTER ROLE and
// the other statements run in a transaction that rolls back, which no other
// session sees.
func TestAPIRoleVerifierDetectsWeakRoles(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	for _, attribute := range []string{"SUPERUSER", "BYPASSRLS", "CREATEROLE", "CREATEDB", "REPLICATION"} {
		tx, err := admin.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec("ALTER ROLE " + db.APIRole + " " + attribute); err != nil {
			tx.Rollback()
			t.Fatalf("alter role: %v", err)
		}
		if got := apiRoleFindings(t, tx); len(got) == 0 {
			t.Errorf("the verifier accepted a role with %s", attribute)
		}
		tx.Rollback()
	}

	for name, statements := range map[string][]string{
		"owner of a table":    {`ALTER TABLE kernel.spaces OWNER TO ` + db.APIRole},
		"owner of a function": {`ALTER FUNCTION kernel.current_space_id() OWNER TO ` + db.APIRole},
		"owner of the schema": {`ALTER SCHEMA kernel OWNER TO ` + db.APIRole},
		"member of a role":    {`CREATE ROLE laws_probe_parent`, `GRANT laws_probe_parent TO ` + db.APIRole},
	} {
		tx, err := admin.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement); err != nil {
				tx.Rollback()
				t.Fatalf("%s: %q: %v", name, statement, err)
			}
		}
		if got := apiRoleFindings(t, tx); len(got) == 0 {
			t.Errorf("the verifier accepted a role that is the %s", name)
		}
		tx.Rollback()
	}
}
