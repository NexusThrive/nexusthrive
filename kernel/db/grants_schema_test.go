package db_test

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// Fixed v7 identifiers for rows these tests insert as the administrator.
const (
	gSpaceA     = "01900000-0000-7000-8000-000000000501"
	gSpaceB     = "01900000-0000-7000-8000-000000000502"
	gSpacePlat  = "01900000-0000-7000-8000-000000000503"
	gPartA1     = "01900000-0000-7000-8000-000000000511"
	gPartA2     = "01900000-0000-7000-8000-000000000512"
	gPartB      = "01900000-0000-7000-8000-000000000513"
	gStaff      = "01900000-0000-7000-8000-000000000514"
	gStaff2     = "01900000-0000-7000-8000-000000000515"
	gRoleA      = "01900000-0000-7000-8000-000000000521"
	gRoleB      = "01900000-0000-7000-8000-000000000522"
	gRolePlat   = "01900000-0000-7000-8000-000000000523"
	gRoleNew    = "01900000-0000-7000-8000-000000000524"
	gGrantA     = "01900000-0000-7000-8000-000000000531"
	gGrantNew   = "01900000-0000-7000-8000-000000000532"
	gGrantOther = "01900000-0000-7000-8000-000000000533"
	gAudit      = "01900000-0000-7000-8000-000000000541"
	gAuditNew   = "01900000-0000-7000-8000-000000000542"
)

// seedGrantFixtures inserts a business space A, a business space B and a
// platform space, a participant or two in each, one role per space, one
// active grant in A and one audit row, all as the administrator.
func seedGrantFixtures(t *testing.T, admin *sql.DB) {
	t.Helper()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'A', 'grants-a')`, []any{gSpaceA}},
		{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'B', 'grants-b')`, []any{gSpaceB}},
		{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'platform', 'P', 'grants-p')`, []any{gSpacePlat}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'A1')`, []any{gPartA1, gSpaceA}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'A2')`, []any{gPartA2, gSpaceA}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'B')`, []any{gPartB, gSpaceB}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'S1')`, []any{gStaff, gSpacePlat}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'S2')`, []any{gStaff2, gSpacePlat}},
		{`INSERT INTO kernel.roles (id, space_id, key, name, permissions, system) VALUES ($1, $2, 'owner', 'Owner', ARRAY['test.read'], true)`, []any{gRoleA, gSpaceA}},
		{`INSERT INTO kernel.roles (id, space_id, key, name, permissions, system) VALUES ($1, $2, 'owner', 'Owner', ARRAY['test.read'], true)`, []any{gRoleB, gSpaceB}},
		{`INSERT INTO kernel.roles (id, space_id, key, name, permissions, system) VALUES ($1, $2, 'staff', 'Staff', ARRAY['test.read'], true)`, []any{gRolePlat, gSpacePlat}},
		{`INSERT INTO kernel.grants (id, space_id, participant_id, role_id) VALUES ($1, $2, $3, $4)`, []any{gGrantA, gSpaceA, gPartA1, gRoleA}},
		{`INSERT INTO kernel.access_audit (id, space_id, actor_participant_id, actor_space_id, actor_space_kind, action, reason, request_id)
		  VALUES ($1, $2, $3, $4, 'platform', 'read', 'support ticket', 'req-1')`, []any{gAudit, gSpaceA, gStaff, gSpacePlat}},
	} {
		if _, err := admin.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed %q: %v", stmt.query, err)
		}
	}
}

// apiExecInSpace runs one statement as the API role inside spaceID and returns
// the error. The transaction is always rolled back.
func apiExecInSpace(t *testing.T, api *sql.DB, spaceID, query string, args ...any) error {
	t.Helper()
	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, spaceID); err != nil {
		t.Fatalf("set space: %v", err)
	}
	_, err = tx.Exec(query, args...)
	return err
}

// TestRolesCheckConstraints guards criterion 1 by SQL: malformed keys, names
// and permission lists are refused, and a well formed role is accepted.
func TestRolesCheckConstraints(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	insert := func(id, key, name, perms string) error {
		_, err := admin.Exec(fmt.Sprintf(`INSERT INTO kernel.roles (id, space_id, key, name, permissions, system)
			VALUES ($1, $2, $3, $4, %s, false)`, perms), id, gSpaceA, key, name)
		return err
	}
	bad := map[string][3]string{
		"uppercase key":     {"Role", "Role", "ARRAY['test.read']"},
		"empty key":         {"", "Role", "ARRAY['test.read']"},
		"key with space":    {"a b", "Role", "ARRAY['test.read']"},
		"key too long":      {strings.Repeat("a", 65), "Role", "ARRAY['test.read']"},
		"blank name":        {"okay", "  ", "ARRAY['test.read']"},
		"one segment":       {"okay", "Role", "ARRAY['single']"},
		"uppercase segment": {"okay", "Role", "ARRAY['Test.read']"},
		"null element":      {"okay", "Role", "ARRAY['test.read', NULL]"},
		"only null element": {"okay", "Role", "ARRAY[NULL]::text[]"},
		"too many elements": {"okay", "Role", "ARRAY(SELECT 'test.read'::text FROM generate_series(1, 201))"},
		"comma in element":  {"okay", "Role", "ARRAY['test.read,test.write']"},
	}
	for name, c := range bad {
		if got := pgCode(insert(gRoleNew, c[0], c[1], c[2])); got != "23514" {
			t.Errorf("%s: SQLSTATE %q, want 23514", name, got)
		}
	}
	if got := pgCode(insert(gRoleNew, "okay", "Role", "NULL")); got != "23502" {
		t.Errorf("null permissions: SQLSTATE %q, want 23502", got)
	}
	if got := pgCode(insert("01900000-0000-4000-8000-000000000524", "okay", "Role", "ARRAY['test.read']")); got != "23514" {
		t.Errorf("non v7 id: SQLSTATE %q, want 23514", got)
	}
	if err := insert("01900000-0000-7000-8000-000000000526", "okay3", "Role", "'{}'"); err != nil {
		t.Errorf("empty permission list must be accepted: %v", err)
	}
	if err := insert("01900000-0000-7000-8000-000000000525", "okay2", "Role", "ARRAY(SELECT 'test.read'::text FROM generate_series(1, 200))"); err != nil {
		t.Errorf("200 permissions must be accepted: %v", err)
	}
}

// TestGrantsNoSelfGrant guards criterion 2: a participant never grants to itself.
func TestGrantsNoSelfGrant(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	_, err := admin.Exec(`INSERT INTO kernel.grants (id, space_id, participant_id, role_id, granted_by, granted_by_space_id, granted_by_space_kind)
		VALUES ($1, $2, $3, $4, $3, $2, 'business')`, gGrantNew, gSpaceA, gPartA2, gRoleA)
	requireCode(t, "self grant", err, "23514")
	_, err = admin.Exec(`INSERT INTO kernel.grants (id, space_id, participant_id, role_id, granted_by, granted_by_space_id, granted_by_space_kind)
		VALUES ($1, $2, $3, $4, $5, $2, 'business')`, gGrantNew, gSpaceA, gPartA2, gRoleA, gPartA1)
	if err != nil {
		t.Errorf("a grant by another participant of the space must be accepted: %v", err)
	}
}

// TestGrantsOnlyOneActivePerParticipantRole guards criterion 2: the same role
// is granted once while active, and again after the first is revoked.
func TestGrantsOnlyOneActivePerParticipantRole(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	insert := `INSERT INTO kernel.grants (id, space_id, participant_id, role_id) VALUES ($1, $2, $3, $4)`
	_, err := admin.Exec(insert, gGrantNew, gSpaceA, gPartA1, gRoleA)
	requireCode(t, "second active grant", err, "23505")
	if _, err := admin.Exec(`UPDATE kernel.grants SET revoked_at = now() WHERE id = $1`, gGrantA); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := admin.Exec(insert, gGrantNew, gSpaceA, gPartA1, gRoleA); err != nil {
		t.Errorf("a new grant after revocation must be accepted: %v", err)
	}
}

// TestGrantsReferencesStayInTheSpace guards criterion 2: the composite keys
// refuse a participant or a role of another space, even for the administrator.
func TestGrantsReferencesStayInTheSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	insert := `INSERT INTO kernel.grants (id, space_id, participant_id, role_id) VALUES ($1, $2, $3, $4)`
	_, err := admin.Exec(insert, gGrantNew, gSpaceA, gPartB, gRoleA)
	requireCode(t, "participant of another space", err, "23503")
	_, err = admin.Exec(insert, gGrantNew, gSpaceA, gPartA2, gRoleB)
	requireCode(t, "role of another space", err, "23503")
}

// TestGrantsSupportRuleCannotBeBypassed guards criterion 4 in the database:
// as the administrator and as the API role, a grant across spaces from a
// platform participant needs a reason and an expiry, the declared kind cannot
// lie, and a grantor of another business space is refused.
func TestGrantsSupportRuleCannotBeBypassed(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	// Arguments: grant id, grantor, grantor space, declared kind, reason, expiry.
	const insert = `INSERT INTO kernel.grants (id, space_id, participant_id, role_id, granted_by, granted_by_space_id, granted_by_space_kind, reason, expires_at)
		VALUES ($1, $7, $8, $9, $2, $3, $4, $5, $6)`
	future := time.Now().Add(time.Hour)
	type args struct {
		grantor, space, kind string
		reason               any
		expiry               any
	}
	cases := map[string]struct {
		in   args
		want string
	}{
		"platform grantor claiming a business kind": {args{gStaff, gSpacePlat, "business", "ticket", future}, "23514"},
		"business grantor claiming a platform kind": {args{gPartB, gSpaceB, "platform", "ticket", future}, "23503"},
		"grantor of another business space":         {args{gPartB, gSpaceB, "business", "ticket", future}, "23514"},
		"platform grantor without reason":           {args{gStaff, gSpacePlat, "platform", nil, future}, "23514"},
		"platform grantor with blank reason":        {args{gStaff, gSpacePlat, "platform", "  ", future}, "23514"},
		"platform grantor without expiry":           {args{gStaff, gSpacePlat, "platform", "ticket", nil}, "23514"},
		"grantor not in the declared space":         {args{gPartA1, gSpacePlat, "platform", "ticket", future}, "23503"},
	}
	for name, c := range cases {
		in := c.in
		run := func(label string, exec func(query string, a ...any) error) {
			err := exec(insert, gGrantNew, in.grantor, in.space, in.kind, in.reason, in.expiry, gSpaceA, gPartA2, gRoleA)
			requireCode(t, label+": "+name, err, c.want)
		}
		run("admin", func(q string, a ...any) error {
			tx, err := admin.Begin()
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback()
			_, err = tx.Exec(q, a...)
			return err
		})
		run("api", func(q string, a ...any) error { return apiExecInSpace(t, api, gSpaceA, q, a...) })
	}

	// The grantor columns come together or not at all.
	const partial = `INSERT INTO kernel.grants (id, space_id, participant_id, role_id, granted_by, granted_by_space_id, granted_by_space_kind, reason, expires_at)
		VALUES ($1, $2, $3, $4, $5, %s, $6, 'ticket', now() + interval '1 hour')`
	_, err := admin.Exec(fmt.Sprintf(partial, "NULL"), gGrantNew, gSpaceA, gPartA2, gRoleA, gStaff, nil)
	requireCode(t, "grantor without space", err, "23514")
	_, err = admin.Exec(fmt.Sprintf(partial, "$7"), gGrantNew, gSpaceA, gPartA2, gRoleA, nil, "platform", gSpacePlat)
	requireCode(t, "space without grantor", err, "23514")

	if err := apiExecInSpace(t, api, gSpaceA, insert, gGrantNew, gStaff, gSpacePlat, "platform", "ticket", future, gSpaceA, gPartA2, gRoleA); err != nil {
		t.Errorf("a support grant with reason and expiry must be accepted: %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants`); got != 1 {
		t.Errorf("grants = %d, want only the seeded one", got)
	}
}

// TestPlatformGrantWithinOwnSpaceNeedsNoExpiry guards the reading of criterion
// 4: adding staff inside the platform space is not support access.
func TestPlatformGrantWithinOwnSpaceNeedsNoExpiry(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	_, err := admin.Exec(`INSERT INTO kernel.grants (id, space_id, participant_id, role_id, granted_by, granted_by_space_id, granted_by_space_kind)
		VALUES ($1, $2, $3, $4, $5, $2, 'platform')`, gGrantNew, gSpacePlat, gStaff2, gRolePlat, gStaff)
	if err != nil {
		t.Errorf("a platform grant inside the platform space needs no reason or expiry: %v", err)
	}
}

// TestCapabilitiesConstraints checks the shape of space_capabilities.
func TestCapabilitiesConstraints(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	const insert = `INSERT INTO kernel.space_capabilities (space_id, capability_key, source, enabled_by, enabled_by_space_id, enabled_by_space_kind, reason) VALUES `
	for name, c := range map[string]struct{ query, want string }{
		"unknown source": {insert + `($1, 'test.capability', 'owner', NULL, NULL, NULL, NULL)`, "23514"},
		"one segment":    {insert + `($1, 'capability', 'package', NULL, NULL, NULL, NULL)`, "23514"},
		"uppercase":      {insert + `($1, 'Test.capability', 'package', NULL, NULL, NULL, NULL)`, "23514"},
		"blank reason":   {insert + `($1, 'test.capability', 'package', NULL, NULL, NULL, ' ')`, "23514"},
	} {
		_, err := admin.Exec(c.query, gSpaceA)
		requireCode(t, name, err, c.want)
	}
	_, err := admin.Exec(`INSERT INTO kernel.space_capabilities (space_id, capability_key, source)
		VALUES ('01900000-0000-7000-8000-000000000599', 'test.capability', 'package')`)
	requireCode(t, "unknown space", err, "23503")
	if _, err := admin.Exec(`INSERT INTO kernel.space_capabilities (space_id, capability_key, status, source)
		VALUES ($1, 'test.capability', 'paused', 'package')`, gSpaceA); pgCode(err) != "23514" {
		t.Errorf("unknown status: SQLSTATE %q, want 23514", pgCode(err))
	}
	for _, source := range []string{"package", "ai", "code"} {
		if _, err := admin.Exec(`INSERT INTO kernel.space_capabilities (space_id, capability_key, source) VALUES ($1, $2, $3)`,
			gSpaceA, "test."+source, source); err != nil {
			t.Errorf("source %s without enabled_by: %v", source, err)
		}
	}
	var status string
	var enabledAt time.Time
	if err := admin.QueryRow(`SELECT status, enabled_at FROM kernel.space_capabilities WHERE capability_key = 'test.ai'`).Scan(&status, &enabledAt); err != nil {
		t.Fatalf("read capability: %v", err)
	}
	if status != "active" || time.Since(enabledAt) > time.Minute {
		t.Errorf("status = %q, enabled_at = %v, want active and recent", status, enabledAt)
	}
	_, err = admin.Exec(`INSERT INTO kernel.space_capabilities (space_id, capability_key, source) VALUES ($1, 'test.ai', 'ai')`, gSpaceA)
	requireCode(t, "duplicate key in the space", err, "23505")
}

// TestAdminCapabilityRequiresEnabledBy guards criterion 5: an admin capability
// always names who enabled it.
func TestAdminCapabilityRequiresEnabledBy(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	_, err := admin.Exec(`INSERT INTO kernel.space_capabilities (space_id, capability_key, source) VALUES ($1, 'test.capability', 'admin')`, gSpaceA)
	requireCode(t, "admin without enabled_by", err, "23514")
	_, err = admin.Exec(`INSERT INTO kernel.space_capabilities (space_id, capability_key, source, enabled_by, enabled_by_space_id, enabled_by_space_kind)
		VALUES ($1, 'test.capability', 'admin', $2, $3, 'platform')`, gSpaceA, gStaff, gSpacePlat)
	if err != nil {
		t.Errorf("admin capability enabled by platform staff: %v", err)
	}
}

// TestCapabilityEnabledByMustBeSameSpaceOrPlatform guards rule 9 in the
// database: a participant of another business space cannot enable a
// capability, and the declared kind cannot lie.
func TestCapabilityEnabledByMustBeSameSpaceOrPlatform(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	const insert = `INSERT INTO kernel.space_capabilities (space_id, capability_key, source, enabled_by, enabled_by_space_id, enabled_by_space_kind)
		VALUES ($1, 'test.capability', 'admin', $2, $3, $4)`
	_, err := admin.Exec(insert, gSpaceA, gPartB, gSpaceB, "business")
	requireCode(t, "participant of another business space", err, "23514")
	_, err = admin.Exec(insert, gSpaceA, gStaff, gSpacePlat, "business")
	requireCode(t, "platform participant claiming a business kind", err, "23514")
	_, err = admin.Exec(insert, gSpaceA, gPartB, gSpaceB, "platform")
	requireCode(t, "business participant claiming a platform kind", err, "23503")
	_, err = admin.Exec(insert, gSpaceA, gPartA1, gSpacePlat, "platform")
	requireCode(t, "participant not in the declared space", err, "23503")
	if _, err := admin.Exec(insert, gSpaceA, gPartA1, gSpaceA, "business"); err != nil {
		t.Errorf("a participant of the same space is in scope: %v", err)
	}
}

// requireAuditIntact fails unless the seeded audit row is still there unchanged.
func requireAuditIntact(t *testing.T, admin *sql.DB) {
	t.Helper()
	if got := count(t, admin, `SELECT count(*) FROM kernel.access_audit WHERE id = $1 AND reason = 'support ticket' AND request_id = 'req-1'`, gAudit); got != 1 {
		t.Errorf("the stored audit row changed or disappeared")
	}
}

// TestAccessAuditRejectsUpdateDeleteTruncateAsAPI guards criterion 6 for the
// API role: no privilege.
func TestAccessAuditRejectsUpdateDeleteTruncateAsAPI(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)
	for _, query := range []string{
		`UPDATE kernel.access_audit SET reason = 'edited'`,
		`DELETE FROM kernel.access_audit`,
		`TRUNCATE kernel.access_audit`,
	} {
		if got := pgCode(asAPI(t, admin, query)); got != "42501" {
			t.Errorf("%s as API: SQLSTATE %q, want 42501", query, got)
		}
	}
	requireAuditIntact(t, admin)
}

// TestAccessAuditRejectsUpdateDeleteTruncateAsAdmin guards criterion 6 for the
// owner and superuser, who hold every privilege and are stopped by the trigger.
func TestAccessAuditRejectsUpdateDeleteTruncateAsAdmin(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)
	for _, query := range []string{
		`UPDATE kernel.access_audit SET reason = 'edited'`,
		`DELETE FROM kernel.access_audit`,
		`TRUNCATE kernel.access_audit`,
	} {
		_, err := admin.Exec(query)
		requireCode(t, query+" as admin", err, "23001")
	}
	requireAuditIntact(t, admin)
}

// TestAccessAuditTriggerHoldsEvenIfGranted shows the second layer: with the
// privileges and policies a careless migration could add, the API role still
// cannot change an audit row.
func TestAccessAuditTriggerHoldsEvenIfGranted(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)
	for _, stmt := range []string{
		`GRANT UPDATE, DELETE ON kernel.access_audit TO nexusthrive_api`,
		`CREATE POLICY mistaken_update ON kernel.access_audit FOR UPDATE USING (true) WITH CHECK (true)`,
		`CREATE POLICY mistaken_delete ON kernel.access_audit FOR DELETE USING (true)`,
	} {
		if _, err := admin.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for _, query := range []string{`UPDATE kernel.access_audit SET reason = 'edited'`, `DELETE FROM kernel.access_audit`} {
		err := apiExecInSpace(t, api, gSpaceA, query)
		requireCode(t, query+" as API with grants", err, "23001")
	}
	requireAuditIntact(t, admin)
}

// TestAccessAuditCheckConstraints guards the shape of an audit row: a reason,
// a request id and an actor are mandatory, and the actor lives where it says.
func TestAccessAuditCheckConstraints(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	// The identifiers are fixed test constants, so the statements are built
	// with them inline and every case can leave different columns out.
	row := func(actor, action, subject, reason, request, grant string) string {
		return fmt.Sprintf(`INSERT INTO kernel.access_audit (id, space_id, actor_participant_id, actor_space_id, actor_space_kind, action, subject_ref, reason, request_id, grant_id)
			VALUES ('%s', '%s', %s, '%s', 'platform', %s, %s, %s, %s, %s)`, gAuditNew, gSpaceA, actor, gSpacePlat, action, subject, reason, request, grant)
	}
	staff := "'" + gStaff + "'"
	for name, c := range map[string]struct{ query, want string }{
		"blank reason":     {row(staff, "'read'", "NULL", "' '", "'req-2'", "NULL"), "23514"},
		"null reason":      {row(staff, "'read'", "NULL", "NULL", "'req-2'", "NULL"), "23502"},
		"blank request id": {row(staff, "'read'", "NULL", "'why'", "' '", "NULL"), "23514"},
		"null request id":  {row(staff, "'read'", "NULL", "'why'", "NULL", "NULL"), "23502"},
		"unknown action":   {row(staff, "'delete'", "NULL", "'why'", "'req-2'", "NULL"), "23514"},
		"blank subject":    {row(staff, "'read'", "' '", "'why'", "'req-2'", "NULL"), "23514"},
		"null actor":       {row("NULL", "'read'", "NULL", "'why'", "'req-2'", "NULL"), "23502"},
		"actor elsewhere":  {row("'"+gPartB+"'", "'read'", "NULL", "'why'", "'req-2'", "NULL"), "23503"},
		"grant elsewhere":  {row(staff, "'read'", "NULL", "'why'", "'req-2'", "'"+gGrantOther+"'"), "23503"},
	} {
		_, err := admin.Exec(c.query)
		requireCode(t, name, err, c.want)
	}
	if _, err := admin.Exec(row(staff, "'write'", "'participant:x'", "'why'", "'req-2'", "'"+gGrantA+"'")); err != nil {
		t.Errorf("a complete audit row must be accepted: %v", err)
	}
}

// expectedPolicies and the privileges below are the exact surface the API role
// has on the four tables.
var expectedPolicies = map[string][]string{
	"roles":              {"INSERT", "SELECT"},
	"grants":             {"INSERT", "SELECT", "UPDATE"},
	"space_capabilities": {"INSERT", "SELECT"},
	"access_audit":       {"INSERT", "SELECT"},
}

var expectedInsertColumns = map[string][]string{
	"roles": {"id", "key", "name", "permissions", "space_id", "system"},
	"grants": {"expires_at", "extra_permissions", "granted_by", "granted_by_space_id", "granted_by_space_kind",
		"id", "participant_id", "reason", "role_id", "space_id"},
	"space_capabilities": {"capability_key", "enabled_by", "enabled_by_space_id", "enabled_by_space_kind",
		"reason", "source", "space_id"},
	"access_audit": {"action", "actor_participant_id", "actor_space_id", "actor_space_kind", "grant_id", "id", "reason",
		"request_id", "space_id", "subject_ref"},
}

// TestGrantsTablesForcedRLSAndExactPrivileges guards criterion 7: RLS is
// enabled and forced, the policies are per command, and the grants by column
// leave out every column the kernel sets itself.
func TestGrantsTablesForcedRLSAndExactPrivileges(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	for table, wantPolicies := range expectedPolicies {
		requireForcedRLS(t, admin, table)
		if got := stringColumn(t, admin, fmt.Sprintf(`SELECT cmd FROM pg_policies WHERE schemaname = 'kernel' AND tablename = '%s' ORDER BY cmd`, table)); !slices.Equal(got, wantPolicies) {
			t.Errorf("%s policies = %v, want %v", table, got, wantPolicies)
		}
		if got := stringColumn(t, admin, fmt.Sprintf(`SELECT privilege_type FROM information_schema.role_table_grants
			WHERE table_schema = 'kernel' AND table_name = '%s' AND grantee = 'nexusthrive_api' ORDER BY 1`, table)); !slices.Equal(got, []string{"SELECT"}) {
			t.Errorf("%s table privileges = %v, want [SELECT]", table, got)
		}
		if got := stringColumn(t, admin, fmt.Sprintf(`SELECT column_name FROM information_schema.column_privileges
			WHERE table_schema = 'kernel' AND table_name = '%s' AND grantee = 'nexusthrive_api' AND privilege_type = 'INSERT' ORDER BY 1`, table)); !slices.Equal(got, expectedInsertColumns[table]) {
			t.Errorf("%s insert columns = %v, want %v", table, got, expectedInsertColumns[table])
		}
		wantUpdate := []string(nil)
		if table == "grants" {
			wantUpdate = []string{"revoked_at"}
		}
		if got := stringColumn(t, admin, fmt.Sprintf(`SELECT column_name FROM information_schema.column_privileges
			WHERE table_schema = 'kernel' AND table_name = '%s' AND grantee = 'nexusthrive_api' AND privilege_type = 'UPDATE' ORDER BY 1`, table)); !slices.Equal(got, wantUpdate) {
			t.Errorf("%s update columns = %v, want %v", table, got, wantUpdate)
		}
		if got := stringColumn(t, admin, fmt.Sprintf(`SELECT privilege_type FROM information_schema.column_privileges
			WHERE table_schema = 'kernel' AND table_name = '%s' AND grantee = 'nexusthrive_api'
			AND privilege_type IN ('DELETE', 'TRUNCATE', 'REFERENCES') ORDER BY 1`, table)); len(got) != 0 {
			t.Errorf("%s has unexpected privileges %v", table, got)
		}
		for _, column := range []string{"revoked_at", "created_at", "enabled_at", "occurred_at", "status"} {
			if slices.Contains(expectedInsertColumns[table], column) {
				t.Errorf("%s: the API role must not insert %s", table, column)
			}
		}
	}
}

// TestRevokedAtIsSetByKernelNotClient guards criterion 8 and follow-up 19: a
// revocation date written by the API role is replaced by the database clock.
func TestRevokedAtIsSetByKernelNotClient(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	before := time.Now().Add(-time.Minute)
	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, gSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	if _, err := tx.Exec(`UPDATE kernel.grants SET revoked_at = '2000-01-01' WHERE id = $1`, gGrantA); err != nil {
		t.Fatalf("API revoke: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var revokedAt time.Time
	if err := admin.QueryRow(`SELECT revoked_at FROM kernel.grants WHERE id = $1`, gGrantA).Scan(&revokedAt); err != nil {
		t.Fatalf("read revoked_at: %v", err)
	}
	if revokedAt.Before(before) {
		t.Errorf("revoked_at = %v, want the database clock, not the client value", revokedAt)
	}
}

// TestRevokedGrantCannotBeUnrevokedOrChanged guards criterion 8: a revoked
// grant is frozen, and an active grant only changes its revocation, for the
// API role and for the administrator alike.
func TestRevokedGrantCannotBeUnrevokedOrChanged(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	// An active grant cannot change any other column, even for the owner.
	for _, query := range []string{
		`UPDATE kernel.grants SET reason = 'edited'`,
		`UPDATE kernel.grants SET expires_at = now() + interval '1 day'`,
		`UPDATE kernel.grants SET role_id = role_id, extra_permissions = ARRAY['test.read']`,
	} {
		_, err := admin.Exec(query)
		requireCode(t, query+" as admin", err, "23001")
	}
	if _, err := admin.Exec(`UPDATE kernel.grants SET revoked_at = now() WHERE id = $1`, gGrantA); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	var first time.Time
	if err := admin.QueryRow(`SELECT revoked_at FROM kernel.grants WHERE id = $1`, gGrantA).Scan(&first); err != nil {
		t.Fatalf("read revoked_at: %v", err)
	}

	for _, query := range []string{
		`UPDATE kernel.grants SET revoked_at = NULL`,
		`UPDATE kernel.grants SET revoked_at = now() + interval '1 day'`,
		`UPDATE kernel.grants SET reason = 'edited'`,
	} {
		_, err := admin.Exec(query)
		requireCode(t, query+" as admin", err, "23001")
	}
	for _, query := range []string{
		`UPDATE kernel.grants SET revoked_at = NULL`,
		`UPDATE kernel.grants SET revoked_at = now() + interval '1 day'`,
	} {
		requireCode(t, query+" as API", apiExecInSpace(t, api, gSpaceA, query), "23001")
	}
	if got := pgCode(asAPI(t, admin, `UPDATE kernel.grants SET reason = 'edited'`)); got != "42501" {
		t.Errorf("API changing reason: SQLSTATE %q, want 42501", got)
	}
	var after time.Time
	if err := admin.QueryRow(`SELECT revoked_at FROM kernel.grants WHERE id = $1`, gGrantA).Scan(&after); err != nil {
		t.Fatalf("read revoked_at: %v", err)
	}
	if !after.Equal(first) {
		t.Errorf("revoked_at changed from %v to %v", first, after)
	}
}

// TestGrantsRejectDelete guards the memory of who had access: grants are
// never deleted, by the API role or by the owner.
func TestGrantsRejectDelete(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	for _, query := range []string{`DELETE FROM kernel.grants`, `TRUNCATE kernel.grants`} {
		if got := pgCode(asAPI(t, admin, query)); got != "42501" {
			t.Errorf("%s as API: SQLSTATE %q, want 42501", query, got)
		}
	}
	_, err := admin.Exec(`DELETE FROM kernel.grants`)
	requireCode(t, "DELETE as admin", err, "23001")
	// A plain TRUNCATE is refused already because audit entries reference
	// grants (0A000); with CASCADE the triggers stop it.
	_, err = admin.Exec(`TRUNCATE kernel.grants`)
	requireCode(t, "TRUNCATE as admin", err, "0A000")
	_, err = admin.Exec(`TRUNCATE kernel.grants CASCADE`)
	requireCode(t, "TRUNCATE CASCADE as admin", err, "23001")
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants`); got != 1 {
		t.Errorf("grants = %d, want 1", got)
	}
}

// TestGrantTriggersPinSearchPath checks the hardening lesson: every function
// of this migration fixes its search_path.
func TestGrantTriggersPinSearchPath(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	got := stringColumn(t, admin, `SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'kernel' AND p.proname IN ('guard_grant_update', 'reject_api_platform_space', 'reject_space_kind_change', 'valid_permission_list', 'reject_grant_delete', 'reject_access_audit_change')
		AND p.proconfig IS NOT NULL AND 'search_path=pg_catalog, pg_temp' = ANY (p.proconfig) ORDER BY 1`)
	want := []string{"guard_grant_update", "reject_access_audit_change", "reject_api_platform_space", "reject_grant_delete", "reject_space_kind_change", "valid_permission_list"}
	if !slices.Equal(got, want) {
		t.Errorf("functions with a pinned search_path = %v, want %v", got, want)
	}
}

// TestAccessAuditActorScope guards the scope of an audit actor: it lives in a
// platform space or in the audited space itself, and the declared kind is the
// real one.
func TestAccessAuditActorScope(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedGrantFixtures(t, admin)

	const insert = `INSERT INTO kernel.access_audit (id, space_id, actor_participant_id, actor_space_id, actor_space_kind, action, reason, request_id)
		VALUES ($1, $2, $3, $4, $5, 'read', 'why', 'req-9')`
	for name, c := range map[string]struct{ actor, space, kind, want string }{
		"business actor claiming business": {gPartB, gSpaceB, "business", "23514"},
		"business actor claiming platform": {gPartB, gSpaceB, "platform", "23503"},
		"platform actor claiming business": {gStaff, gSpacePlat, "business", "23514"},
		"actor not in the declared space":  {gPartA1, gSpacePlat, "platform", "23503"},
	} {
		_, err := admin.Exec(insert, gAuditNew, gSpaceA, c.actor, c.space, c.kind)
		requireCode(t, name, err, c.want)
	}
	if _, err := admin.Exec(insert, gAuditNew, gSpaceA, gStaff, gSpacePlat, "platform"); err != nil {
		t.Errorf("a platform actor must be accepted: %v", err)
	}
	if _, err := admin.Exec(insert, "01900000-0000-7000-8000-000000000543", gSpaceA, gPartA1, gSpaceA, "business"); err != nil {
		t.Errorf("an actor of the audited space must be accepted: %v", err)
	}
}

// TestSpacesIdentityColumnsCannotBeChangedByAPI guards the privilege layer:
// the API role updates only the state columns of a space.
func TestSpacesIdentityColumnsCannotBeChangedByAPI(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	for _, query := range []string{
		`UPDATE kernel.spaces SET kind = 'platform' WHERE id = $1`,
		`UPDATE kernel.spaces SET id = '01900000-0000-7000-8000-0000000005ff' WHERE id = $1`,
		`UPDATE kernel.spaces SET slug = 'renamed' WHERE id = $1`,
		`UPDATE kernel.spaces SET created_at = now() WHERE id = $1`,
		`UPDATE kernel.spaces SET parent_space_id = id WHERE id = $1`,
	} {
		requireCode(t, query, apiExecInSpace(t, api, gSpaceA, query, gSpaceA), "42501")
	}
	if err := apiExecInSpace(t, api, gSpaceA, `UPDATE kernel.spaces SET name = 'Renamed' WHERE id = $1`, gSpaceA); err != nil {
		t.Errorf("updating the name must stay allowed: %v", err)
	}
	got := stringColumn(t, admin, `SELECT column_name FROM information_schema.column_privileges
		WHERE table_schema = 'kernel' AND table_name = 'spaces' AND grantee = 'nexusthrive_api'
		AND privilege_type = 'UPDATE' ORDER BY 1`)
	want := []string{"closed_at", "name", "status", "suspended_at", "suspended_reason"}
	if !slices.Equal(got, want) {
		t.Errorf("API update columns on spaces = %v, want %v", got, want)
	}
}

// TestSpaceKindIsImmutableForEveryone guards the trigger layer: nobody changes
// the kind of a space, not even the owner or with the privilege granted by mistake.
func TestSpaceKindIsImmutableForEveryone(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	_, err := admin.Exec(`UPDATE kernel.spaces SET kind = 'platform' WHERE id = $1`, gSpaceB)
	requireCode(t, "administrator changing kind", err, "23001")
	if _, err := admin.Exec(`GRANT UPDATE (kind) ON kernel.spaces TO nexusthrive_api`); err != nil {
		t.Fatalf("grant: %v", err)
	}
	err = apiExecInSpace(t, api, gSpaceA, `UPDATE kernel.spaces SET kind = 'platform' WHERE id = $1`, gSpaceA)
	requireCode(t, "API with the privilege granted by mistake", err, "23001")
	if got := count(t, admin, `SELECT count(*) FROM kernel.spaces WHERE kind = 'business'`); got != 2 {
		t.Errorf("business spaces = %d, want 2", got)
	}
	if _, err := admin.Exec(`UPDATE kernel.spaces SET name = 'ok' WHERE id = $1`, gSpaceB); err != nil {
		t.Errorf("other updates stay allowed: %v", err)
	}
}

// TestSelfPromotionToPlatformIsImpossible reproduces the full attack: a
// session of a business space promotes its own space and then grants itself
// support access without reason or expiry.
func TestSelfPromotionToPlatformIsImpossible(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, gSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	_, err = tx.Exec(`UPDATE kernel.spaces SET kind = 'platform' WHERE id = $1`, gSpaceA)
	requireCode(t, "promotion", err, "42501")
	tx.Rollback()

	err = apiExecInSpace(t, api, gSpaceA, `INSERT INTO kernel.grants (id, space_id, participant_id, role_id, granted_by, granted_by_space_id, granted_by_space_kind)
		VALUES ($1, $2, $3, $4, $5, $2, 'platform')`, gGrantNew, gSpaceA, gPartA2, gRoleA, gPartA1)
	requireCode(t, "grant claiming the platform kind", err, "23503")
}

const (
	gFakePlatform = "01900000-0000-7000-8000-0000000005a1"
	gFakeStaff    = "01900000-0000-7000-8000-0000000005a2"
)

// TestAPICannotInsertPlatformSpace reproduces the full attack: the API role
// chooses its own session context, inserts a new platform space, creates a
// participant in it and uses it as grantor and audit actor. The first INSERT
// must already fail.
func TestAPICannotInsertPlatformSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedGrantFixtures(t, admin)

	err := apiExecInSpace(t, api, gFakePlatform,
		`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'platform', 'Fake', 'fake-platform')`, gFakePlatform)
	requireCode(t, "API inserting a platform space", err, "23001")

	// The rest of the chain cannot even start: the space does not exist.
	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, gFakePlatform); err != nil {
		t.Fatalf("set space: %v", err)
	}
	_, err = tx.Exec(`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'platform', 'Fake', 'fake-platform')`, gFakePlatform)
	requireCode(t, "platform insert in a long transaction", err, "23001")
	if got := count(t, admin, `SELECT count(*) FROM kernel.spaces WHERE kind = 'platform'`); got != 1 {
		t.Errorf("platform spaces = %d, want only the seeded one", got)
	}
}

// TestAPICanStillInsertOrdinarySpaces checks the regression: the API role
// provisions business, community and personal spaces.
func TestAPICanStillInsertOrdinarySpaces(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	for i, kind := range []string{"business", "community", "personal"} {
		id := fmt.Sprintf("01900000-0000-7000-8000-0000000005b%d", i)
		if err := apiExecInSpace(t, api, id,
			`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, $2, 'Ordinary', $3)`, id, kind, "ordinary-"+kind); err != nil {
			t.Errorf("API inserting a %s space: %v", kind, err)
		}
	}
}

// TestAdministratorCanInsertPlatformSpace: platform spaces are provisioned by
// the owner or administrator at bootstrap, which the trigger lets through.
func TestAdministratorCanInsertPlatformSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	if _, err := admin.Exec(`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'platform', 'P', 'admin-platform')`, gFakePlatform); err != nil {
		t.Errorf("administrator inserting a platform space: %v", err)
	}
}

// TestAPIInsertOnSpacesIsByColumn: the API role cannot set the columns the
// kernel fixes itself when it creates a space.
func TestAPIInsertOnSpacesIsByColumn(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	for i, column := range []string{"status", "suspended_at", "suspended_reason", "closed_at", "created_at"} {
		values := map[string]string{
			"status": "'active'", "suspended_at": "now()", "suspended_reason": "'x'", "closed_at": "now()", "created_at": "now()",
		}
		id := fmt.Sprintf("01900000-0000-7000-8000-0000000005c%d", i)
		err := apiExecInSpace(t, api, id, fmt.Sprintf(
			`INSERT INTO kernel.spaces (id, kind, name, slug, %s) VALUES ($1, 'business', 'X', $2, %s)`, column, values[column]),
			id, "col-"+column)
		requireCode(t, "API inserting "+column, err, "42501")
	}
}
