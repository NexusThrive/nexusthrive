package db_test

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// Fixed v7 identifiers for rows these tests insert as the administrator.
const (
	ledgerSpaceA       = "01900000-0000-7000-8000-0000000000c1"
	ledgerSpaceB       = "01900000-0000-7000-8000-0000000000c2"
	ledgerParticipantA = "01900000-0000-7000-8000-0000000000c3"
	ledgerParticipantB = "01900000-0000-7000-8000-0000000000c4"
	ledgerResourceA    = "01900000-0000-7000-8000-0000000000c5"
	ledgerResourceA2   = "01900000-0000-7000-8000-0000000000c6"
	ledgerResourceB    = "01900000-0000-7000-8000-0000000000c7"
	ledgerAccountA     = "01900000-0000-7000-8000-0000000000c8"
	ledgerAccountNew   = "01900000-0000-7000-8000-0000000000c9"
	ledgerResourceNew  = "01900000-0000-7000-8000-0000000000ca"
)

// seedLedgerFixtures inserts two spaces, a participant in each, two resources
// in space A, one in space B and an open account in space A, all as the
// administrator.
func seedLedgerFixtures(t *testing.T, admin *sql.DB) {
	t.Helper()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'A', 'ledger-a')`, []any{ledgerSpaceA}},
		{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'B', 'ledger-b')`, []any{ledgerSpaceB}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'PA')`, []any{ledgerParticipantA, ledgerSpaceA}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'PB')`, []any{ledgerParticipantB, ledgerSpaceB}},
		{`INSERT INTO kernel.resources (id, space_id, kind, code, name, scale) VALUES ($1, $2, 'money', 'COP', 'Peso', 2)`, []any{ledgerResourceA, ledgerSpaceA}},
		{`INSERT INTO kernel.resources (id, space_id, kind, code, name, scale) VALUES ($1, $2, 'unit', 'UND', 'Unit', 0)`, []any{ledgerResourceA2, ledgerSpaceA}},
		{`INSERT INTO kernel.resources (id, space_id, kind, code, name, scale) VALUES ($1, $2, 'money', 'COP', 'Peso', 2)`, []any{ledgerResourceB, ledgerSpaceB}},
		{`INSERT INTO kernel.accounts (id, space_id, holder_participant_id, resource_id, class, code, name)
		  VALUES ($1, $2, $3, $4, 'asset', 'CASH', 'Cash')`, []any{ledgerAccountA, ledgerSpaceA, ledgerParticipantA, ledgerResourceA}},
	} {
		if _, err := admin.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed %q: %v", stmt.query, err)
		}
	}
}

// requireCode fails unless err carries the given SQLSTATE.
func requireCode(t *testing.T, label string, err error, want string) {
	t.Helper()
	if got := pgCode(err); got != want {
		t.Errorf("%s: SQLSTATE %q (%v), want %s", label, got, err, want)
	}
}

// TestResourcesUniqueSpaceCode guards criterion 1: a code is unique per space,
// but the same code may exist in another space.
func TestResourcesUniqueSpaceCode(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedLedgerFixtures(t, admin)

	_, err := admin.Exec(`INSERT INTO kernel.resources (id, space_id, kind, code, name, scale)
		VALUES ($1, $2, 'money', 'COP', 'Peso again', 2)`, ledgerResourceNew, ledgerSpaceA)
	requireCode(t, "duplicate code in the same space", err, "23505")
	// Space B already holds COP from the fixtures, so the same code in two
	// spaces is proven by the seed itself.
	if got := count(t, admin, `SELECT count(*) FROM kernel.resources WHERE code = 'COP'`); got != 2 {
		t.Errorf("COP rows = %d, want 2 (one per space)", got)
	}
}

// TestResourcesCheckConstraints guards criterion 2 by SQL as the administrator.
func TestResourcesCheckConstraints(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedLedgerFixtures(t, admin)

	const insert = `INSERT INTO kernel.resources (id, space_id, kind, code, name, scale) VALUES ($1, $2, $3, $4, $5, $6)`
	type row struct {
		id, kind, code, name string
		scale                int
	}
	good := row{id: ledgerResourceNew, kind: "time", code: "MIN", name: "Minute", scale: 0}
	cases := map[string]func(*row){
		"id not v7":       func(r *row) { r.id = "01900000-0000-4000-8000-0000000000ca" },
		"unknown kind":    func(r *row) { r.kind = "foo" },
		"lowercase code":  func(r *row) { r.code = "cop" },
		"empty code":      func(r *row) { r.code = "" },
		"code with space": func(r *row) { r.code = "A B" },
		"code too long":   func(r *row) { r.code = "A" + strings.Repeat("B", 32) },
		"blank name":      func(r *row) { r.name = "  " },
		"negative scale":  func(r *row) { r.scale = -1 },
		"scale above 18":  func(r *row) { r.scale = 19 },
	}
	for name, mutate := range cases {
		r := good
		mutate(&r)
		_, err := admin.Exec(insert, r.id, ledgerSpaceA, r.kind, r.code, r.name, r.scale)
		requireCode(t, name, err, "23514")
	}
	for id, scale := range map[string]int{
		"01900000-0000-7000-8000-0000000000d0": 0,
		"01900000-0000-7000-8000-0000000000d1": 18,
	} {
		if _, err := admin.Exec(insert, id, ledgerSpaceA, "unit", "EDGE"+strings.Repeat("X", scale/9), "Edge", scale); err != nil {
			t.Errorf("scale %d must be accepted: %v", scale, err)
		}
	}
}

// TestAccountsCheckConstraints guards criterion 3 by SQL as the administrator.
func TestAccountsCheckConstraints(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedLedgerFixtures(t, admin)

	const insert = `INSERT INTO kernel.accounts
		(id, space_id, holder_participant_id, resource_id, parent_account_id, class, code, name, status, closed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	type row struct {
		id, class, code, name, status string
		parent, closedAt              any
	}
	good := row{id: ledgerAccountNew, class: "asset", code: "BANK", name: "Bank", status: "open"}
	cases := map[string]func(*row){
		"id not v7":           func(r *row) { r.id = "01900000-0000-4000-8000-0000000000c9" },
		"unknown class":       func(r *row) { r.class = "foo" },
		"blank code":          func(r *row) { r.code = " " },
		"blank name":          func(r *row) { r.name = " " },
		"unknown status":      func(r *row) { r.status = "frozen" },
		"closed without time": func(r *row) { r.status = "closed" },
		"open with time":      func(r *row) { r.closedAt = "2026-01-01T00:00:00Z" },
		"own parent":          func(r *row) { r.parent = ledgerAccountNew },
	}
	for name, mutate := range cases {
		r := good
		mutate(&r)
		_, err := admin.Exec(insert, r.id, ledgerSpaceA, ledgerParticipantA, ledgerResourceA, r.parent, r.class, r.code, r.name, r.status, r.closedAt)
		requireCode(t, name, err, "23514")
	}
	// A repeated (space, holder, code) is a rule of its own.
	_, err := admin.Exec(insert, ledgerAccountNew, ledgerSpaceA, ledgerParticipantA, ledgerResourceA, nil, "asset", "CASH", "Cash again", "open", nil)
	requireCode(t, "duplicate holder code", err, "23505")
}

// columnsOfConstraint returns the comma-joined local and referenced columns of
// a foreign key, in key order.
func columnsOfConstraint(t *testing.T, admin *sql.DB, name string) (local, referenced string) {
	t.Helper()
	err := admin.QueryRow(`
		SELECT
		  (SELECT string_agg(a.attname, ',' ORDER BY k.ord)
		     FROM unnest(c.conkey) WITH ORDINALITY k(attnum, ord)
		     JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum),
		  (SELECT string_agg(a.attname, ',' ORDER BY k.ord)
		     FROM unnest(c.confkey) WITH ORDINALITY k(attnum, ord)
		     JOIN pg_attribute a ON a.attrelid = c.confrelid AND a.attnum = k.attnum)
		FROM pg_constraint c WHERE c.contype = 'f' AND c.conname = $1`, name).Scan(&local, &referenced)
	if err != nil {
		t.Fatalf("read foreign key %s: %v", name, err)
	}
	return local, referenced
}

// TestAccountsParentFkeyIsComposite guards criteria 4 and 5: foreign keys skip
// row level security, so every reference carries the space and the parent also
// carries the resource.
func TestAccountsParentFkeyIsComposite(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	for name, want := range map[string][2]string{
		"accounts_parent_fkey":   {"parent_account_id,space_id,resource_id", "id,space_id,resource_id"},
		"accounts_holder_fkey":   {"holder_participant_id,space_id", "id,space_id"},
		"accounts_resource_fkey": {"resource_id,space_id", "id,space_id"},
	} {
		local, referenced := columnsOfConstraint(t, admin, name)
		if local != want[0] || referenced != want[1] {
			t.Errorf("%s = (%s) -> (%s), want (%s) -> (%s)", name, local, referenced, want[0], want[1])
		}
	}
}

// TestClosedAccountCannotBeReopenedEvenByOwner guards criterion 6: a closed
// account stays closed for everyone, including the administrator.
func TestClosedAccountCannotBeReopenedEvenByOwner(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedLedgerFixtures(t, admin)

	if _, err := admin.Exec(`UPDATE kernel.accounts SET status = 'closed', closed_at = now() WHERE id = $1`, ledgerAccountA); err != nil {
		t.Fatalf("close an open account: %v", err)
	}
	for _, query := range []string{
		`UPDATE kernel.accounts SET status = 'open', closed_at = NULL WHERE id = $1`,
		`UPDATE kernel.accounts SET closed_at = now() + interval '1 day' WHERE id = $1`,
		`UPDATE kernel.accounts SET status = 'closed', closed_at = NULL WHERE id = $1`,
	} {
		_, err := admin.Exec(query, ledgerAccountA)
		if got := pgCode(err); got != "23001" && got != "23514" {
			t.Errorf("%s: SQLSTATE %q (%v), want 23001 or 23514", query, got, err)
		}
	}
	// The reopen with matching columns must be stopped by the trigger itself.
	_, err := admin.Exec(`UPDATE kernel.accounts SET status = 'open', closed_at = NULL WHERE id = $1`, ledgerAccountA)
	requireCode(t, "reopen", err, "23001")
	_, err = admin.Exec(`UPDATE kernel.accounts SET closed_at = now() + interval '1 day' WHERE id = $1`, ledgerAccountA)
	requireCode(t, "move closed_at", err, "23001")
	if got := count(t, admin, `SELECT count(*) FROM kernel.accounts WHERE id = $1 AND status = 'closed'`, ledgerAccountA); got != 1 {
		t.Error("the account must still be closed")
	}
}

// TestAPICannotInsertClosedAccount guards criterion 6: an account is born
// open, so the API role has no INSERT on status or closed_at.
func TestAPICannotInsertClosedAccount(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedLedgerFixtures(t, admin)

	for _, query := range []string{
		`INSERT INTO kernel.accounts (id, space_id, holder_participant_id, resource_id, class, code, name, status, closed_at)
		 VALUES ('` + ledgerAccountNew + `', '` + ledgerSpaceA + `', '` + ledgerParticipantA + `', '` + ledgerResourceA + `', 'asset', 'X', 'X', 'closed', now())`,
		`INSERT INTO kernel.accounts (id, space_id, holder_participant_id, resource_id, class, code, name, status)
		 VALUES ('` + ledgerAccountNew + `', '` + ledgerSpaceA + `', '` + ledgerParticipantA + `', '` + ledgerResourceA + `', 'asset', 'X', 'X', 'open')`,
		`INSERT INTO kernel.accounts (id, space_id, holder_participant_id, resource_id, class, code, name, closed_at)
		 VALUES ('` + ledgerAccountNew + `', '` + ledgerSpaceA + `', '` + ledgerParticipantA + `', '` + ledgerResourceA + `', 'asset', 'X', 'X', now())`,
	} {
		requireCode(t, "API insert of lifecycle columns", asAPI(t, admin, query), "42501")
	}
}

// privilegeSet lists "column:PRIVILEGE" pairs the API role holds on a table.
func privilegeSet(t *testing.T, admin *sql.DB, table string) []string {
	t.Helper()
	rows, err := admin.Query(`SELECT column_name || ':' || privilege_type FROM information_schema.column_privileges
		WHERE grantee = $1 AND table_schema = 'kernel' AND table_name = $2`, db.APIRole, table)
	if err != nil {
		t.Fatalf("list privileges of %s: %v", table, err)
	}
	defer rows.Close()
	var set []string
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			t.Fatalf("scan privilege: %v", err)
		}
		set = append(set, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list privileges of %s: %v", table, err)
	}
	slices.Sort(set)
	return set
}

// expectPrivileges builds the sorted expected set from column lists.
func expectPrivileges(selectColumns, insertColumns, updateColumns []string) []string {
	var set []string
	for privilege, columns := range map[string][]string{"SELECT": selectColumns, "INSERT": insertColumns, "UPDATE": updateColumns} {
		for _, column := range columns {
			set = append(set, column+":"+privilege)
		}
	}
	slices.Sort(set)
	return set
}

// policyCommands lists the commands of the policies of a table.
func policyCommands(t *testing.T, admin *sql.DB, table string) []string {
	t.Helper()
	rows, err := admin.Query(`SELECT cmd FROM pg_policies WHERE schemaname = 'kernel' AND tablename = $1 ORDER BY cmd`, table)
	if err != nil {
		t.Fatalf("list policies of %s: %v", table, err)
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
	return cmds
}

// TestResourcesAccountsForcedRLSAndGrants guards criterion 7: forced row level
// security, only the planned policies and exact column grants.
func TestResourcesAccountsForcedRLSAndGrants(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	for _, table := range []string{"resources", "accounts"} {
		requireForcedRLS(t, admin, table)
		for _, privilege := range []string{"DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
			var ok bool
			if err := admin.QueryRow(`SELECT has_table_privilege($1, $2, $3)`, db.APIRole, "kernel."+table, privilege).Scan(&ok); err != nil {
				t.Fatalf("privilege query: %v", err)
			}
			if ok {
				t.Errorf("API role must not have %s on kernel.%s", privilege, table)
			}
		}
	}
	if got := policyCommands(t, admin, "resources"); !slices.Equal(got, []string{"INSERT", "SELECT"}) {
		t.Errorf("resources policies = %v, want [INSERT SELECT]", got)
	}
	if got := policyCommands(t, admin, "accounts"); !slices.Equal(got, []string{"INSERT", "SELECT", "UPDATE"}) {
		t.Errorf("accounts policies = %v, want [INSERT SELECT UPDATE]", got)
	}

	resourceColumns := []string{"id", "space_id", "kind", "code", "name", "scale"}
	if got, want := privilegeSet(t, admin, "resources"), expectPrivileges(resourceColumns, resourceColumns, nil); !slices.Equal(got, want) {
		t.Errorf("resources privileges = %v, want %v", got, want)
	}
	accountInsert := []string{"id", "space_id", "holder_participant_id", "resource_id", "parent_account_id", "class", "code", "name"}
	accountSelect := append(slices.Clone(accountInsert), "status", "closed_at")
	if got, want := privilegeSet(t, admin, "accounts"), expectPrivileges(accountSelect, accountInsert, []string{"status", "closed_at"}); !slices.Equal(got, want) {
		t.Errorf("accounts privileges = %v, want %v", got, want)
	}
	if got := columnSet(t, admin, "accounts"); len(got) != len(accountSelect) {
		t.Errorf("accounts has %d columns, the grants cover %d: update the grants with the table", len(got), len(accountSelect))
	}
	if got := columnSet(t, admin, "resources"); len(got) != len(resourceColumns) {
		t.Errorf("resources has %d columns, the grants cover %d: update the grants with the table", len(got), len(resourceColumns))
	}
}

// expectedAmountColumns lists the ledger columns that must exist and hold
// exact decimals. The commitment tables add theirs.
var expectedAmountColumns = []string{"entries.amount", "commitments.amount", "commitments.fulfilled_amount", "fulfillments.amount"}

// floatingTypes are the column types that can never hold money.
var floatingTypes = []string{"real", "double precision", "money"}

// amountColumnProblems inspects the kernel schema and returns one line per
// violation: an amount column that is not numeric, a floating or money typed
// column anywhere, or an expected amount column that is missing.
func amountColumnProblems(t *testing.T, conn *sql.DB) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT table_name, column_name, data_type FROM information_schema.columns WHERE table_schema = 'kernel'`)
	if err != nil {
		t.Fatalf("list columns: %v", err)
	}
	defer rows.Close()
	var problems []string
	found := map[string]bool{}
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, &dataType); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		qualified := table + "." + column
		found[qualified] = true
		if (column == "amount" || strings.HasSuffix(column, "_amount")) && dataType != "numeric" {
			problems = append(problems, qualified+" is "+dataType+", want numeric")
		}
		if slices.Contains(floatingTypes, dataType) {
			problems = append(problems, qualified+" uses forbidden type "+dataType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list columns: %v", err)
	}
	for _, qualified := range expectedAmountColumns {
		if !found[qualified] {
			problems = append(problems, qualified+" is expected but missing")
		}
	}
	return problems
}

// TestAmountColumnsAreNumeric guards criterion 8 as a regression guard: every
// amount column of the kernel schema is numeric and no column is floating.
func TestAmountColumnsAreNumeric(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	if problems := amountColumnProblems(t, admin); len(problems) > 0 {
		t.Errorf("amount columns: %v", problems)
	}
	var dataType string
	if err := admin.QueryRow(`SELECT data_type FROM information_schema.columns
		WHERE table_schema = 'kernel' AND table_name = 'resources' AND column_name = 'scale'`).Scan(&dataType); err != nil {
		t.Fatalf("read resources.scale: %v", err)
	}
	if dataType != "integer" {
		t.Errorf("resources.scale is %s, want integer", dataType)
	}
}

// TestAmountCheckDetectsFloat keeps the guard from being empty: the checker
// must flag a column named amount that is double precision.
func TestAmountCheckDetectsFloat(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	if _, err := admin.Exec(`CREATE TABLE kernel.probe_entries (amount double precision, fee_amount real, cash money)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	problems := amountColumnProblems(t, admin)
	for _, want := range []string{"probe_entries.amount", "probe_entries.fee_amount", "probe_entries.cash"} {
		if !slices.ContainsFunc(problems, func(p string) bool { return strings.HasPrefix(p, want+" ") }) {
			t.Errorf("checker did not flag %s: %v", want, problems)
		}
	}
}
