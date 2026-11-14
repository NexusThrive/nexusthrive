package laws_test

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/db"
)

// migrationLedger is the table the migration tool keeps in the schema. It
// holds no data of any space.
const migrationLedger = "goose_db_version"

// spaceFunction is what every policy of the data of a space must call.
const spaceFunction = "current_space_id"

// catalogPolicy is a row level security policy as the catalog stores it.
type catalogPolicy struct {
	Name string
	// Cmd is the polcmd letter: r select, a insert, w update, d delete, * all.
	Cmd   string
	Using string
	Check string
}

// catalogTable is a table of the kernel schema as the catalog describes it.
type catalogTable struct {
	Name       string
	HasSpaceID bool
	RLS        bool
	Forced     bool
	Policies   []catalogPolicy
}

// catalogTables reads every table of the kernel schema, except the ledger of
// the migration tool, with its policies.
func catalogTables(t *testing.T, admin *sql.DB) []catalogTable {
	t.Helper()
	rows, err := admin.Query(`SELECT c.relname, c.relrowsecurity, c.relforcerowsecurity,
		EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped AND a.attname = 'space_id')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'kernel' AND c.relkind IN ('r', 'p') AND c.relname <> $1 ORDER BY 1`, migrationLedger)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []catalogTable
	for rows.Next() {
		var table catalogTable
		if err := rows.Scan(&table.Name, &table.RLS, &table.Forced, &table.HasSpaceID); err != nil {
			rows.Close()
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}

	policies, err := admin.Query(`SELECT c.relname, p.polname, p.polcmd::text,
		coalesce(pg_get_expr(p.polqual, p.polrelid), ''), coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '')
		FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'kernel' ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	defer policies.Close()
	for policies.Next() {
		var table string
		var policy catalogPolicy
		if err := policies.Scan(&table, &policy.Name, &policy.Cmd, &policy.Using, &policy.Check); err != nil {
			t.Fatalf("scan policy: %v", err)
		}
		for i := range tables {
			if tables[i].Name == table {
				tables[i].Policies = append(tables[i].Policies, policy)
			}
		}
	}
	if err := policies.Err(); err != nil {
		t.Fatalf("list policies: %v", err)
	}
	return tables
}

// scopeOf says how the rows of a table belong to a space. The table of spaces
// is its own scope, the identities belong to no space and are visible through
// their participants, and every other table carries a space_id.
type scope struct {
	// own is a predicate, with $1 the space, that selects the rows that space
	// is meant to see.
	own string
	// foreign selects, with $1 the value foreignArg returns for the other
	// space, rows that the space must not see.
	foreign    string
	foreignArg func(w *isolationWorld, other string) any
}

// scopeFor returns the scope of a table.
func scopeFor(table catalogTable) scope {
	switch {
	case table.Name == "spaces":
		return scope{own: "x.id = $1", foreign: "id = $1", foreignArg: func(_ *isolationWorld, other string) any { return other }}
	case table.Name == "identities":
		return scope{
			own:        "EXISTS (SELECT 1 FROM kernel.participants p WHERE p.identity_id = x.id AND p.space_id = $1)",
			foreign:    "id = $1",
			foreignArg: func(w *isolationWorld, other string) any { return w.ownIdentity[other] },
		}
	default:
		return scope{own: "x.space_id = $1", foreign: "space_id = $1", foreignArg: func(_ *isolationWorld, other string) any { return other }}
	}
}

// structureFindings checks, without any data, that every table is isolated:
// row level security is on and forced and the policies call
// current_space_id for reads and for every write. A table with a space_id and
// no policy is the case the closing story exists to catch. A table without a
// space_id that is not one of the two the schema knows is also a finding,
// because nothing says how it is isolated.
func structureFindings(tables []catalogTable) []string {
	var findings []string
	report := func(table, format string, args ...any) {
		findings = append(findings, table+": "+fmt.Sprintf(format, args...))
	}
	for _, table := range tables {
		known := table.HasSpaceID || table.Name == "spaces" || table.Name == "identities"
		if !known {
			report(table.Name, "has no space_id and no isolation rule of its own")
			continue
		}
		if !table.RLS {
			report(table.Name, "row level security is not enabled")
		}
		if !table.Forced {
			report(table.Name, "row level security is not forced")
		}
		if len(table.Policies) == 0 {
			report(table.Name, "has no policy")
			continue
		}
		limitsReads := false
		for _, policy := range table.Policies {
			readsToo := policy.Cmd == "r" || policy.Cmd == "*"
			if readsToo && strings.Contains(policy.Using, spaceFunction) {
				limitsReads = true
			}
			// Both the rows an UPDATE may touch and the rows a DELETE may
			// remove are chosen by USING. An UPDATE and an ALL policy without
			// a CHECK reuse it for the new row.
			if slices.Contains([]string{"w", "d", "*"}, policy.Cmd) && !strings.Contains(policy.Using, spaceFunction) {
				report(table.Name, "policy %s selects the rows to change without calling %s", policy.Name, spaceFunction)
			}
			check := policy.Check
			if check == "" && policy.Cmd != "a" {
				check = policy.Using
			}
			// A new identity exists before any participant, so its insert
			// cannot depend on the space (ADR-0027).
			exempt := table.Name == "identities" && policy.Cmd == "a"
			if slices.Contains([]string{"a", "w", "*"}, policy.Cmd) && !exempt && !strings.Contains(check, spaceFunction) {
				report(table.Name, "policy %s checks new rows without calling %s", policy.Name, spaceFunction)
			}
		}
		if !limitsReads {
			report(table.Name, "no policy limits reads to the current space")
		}
	}
	return findings
}

// queryer is what the checks need of a connection or a transaction.
type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// inSpaceTx runs fn in a transaction of the API pool with the space fixed, or
// with no space when space is empty, and always rolls it back.
func inSpaceTx(t *testing.T, pool *sql.DB, space string, fn func(q queryer)) {
	t.Helper()
	tx, err := pool.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if space != "" {
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, space); err != nil {
			t.Fatalf("set space: %v", err)
		}
	}
	fn(tx)
}

// updatableColumn returns a column of the table the API role may update, or
// "" when it may update none.
func updatableColumn(t *testing.T, admin *sql.DB, table string) string {
	t.Helper()
	var column sql.NullString
	err := admin.QueryRow(`SELECT a.attname FROM pg_attribute a
		WHERE a.attrelid = ('kernel.' || quote_ident($1))::regclass AND a.attnum > 0 AND NOT a.attisdropped
		  AND has_column_privilege($2, a.attrelid, a.attnum, 'UPDATE') ORDER BY a.attnum LIMIT 1`, table, db.APIRole).Scan(&column)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("read the update privileges of %s: %v", table, err)
	}
	return column.String
}

// dataFindings runs the checks with data: for each table and each direction
// between the two spaces, the acting space sees exactly its own rows and none
// of the other, a connection with no space or with an unknown one sees
// nothing, and an update, a delete or an insert aimed at the other space
// changes nothing.
func dataFindings(t *testing.T, w *isolationWorld, tables []catalogTable) []string {
	t.Helper()
	var findings []string
	report := func(table, format string, args ...any) {
		findings = append(findings, table+": "+fmt.Sprintf(format, args...))
	}
	count := func(q queryer, query string, args ...any) (int, error) {
		var n int
		err := q.QueryRow(query, args...).Scan(&n)
		return n, err
	}

	for _, table := range tables {
		if !(table.HasSpaceID || table.Name == "spaces" || table.Name == "identities") {
			continue
		}
		name := "kernel." + pgx.Identifier{table.Name}.Sanitize()
		sc := scopeFor(table)
		update := updatableColumn(t, w.admin, table.Name)
		canDelete := false
		if err := w.admin.QueryRow(`SELECT has_table_privilege($1, $2::regclass, 'DELETE')`, db.APIRole, name).Scan(&canDelete); err != nil {
			t.Fatalf("read the delete privilege of %s: %v", table.Name, err)
		}

		for _, pair := range [][2]string{{w.spaceA, w.spaceB}, {w.spaceB, w.spaceA}} {
			acting, other := pair[0], pair[1]
			arg := sc.foreignArg(w, other)
			own, err := count(w.admin, `SELECT count(*) FROM `+name+` x WHERE `+sc.own, acting)
			if err != nil {
				t.Fatalf("count the rows of %s in a space: %v", table.Name, err)
			}
			foreignRows, err := count(w.admin, `SELECT count(*) FROM `+name+` WHERE `+sc.foreign, arg)
			if err != nil {
				t.Fatalf("count the rows of %s of the other space: %v", table.Name, err)
			}
			if own == 0 || foreignRows == 0 {
				report(table.Name, "the seed left %d rows for a space and %d for the other, so the checks prove nothing", own, foreignRows)
				continue
			}

			inSpaceTx(t, w.api, acting, func(q queryer) {
				seen, err := count(q, `SELECT count(*) FROM `+name)
				if err != nil || seen != own {
					report(table.Name, "a space sees %d rows (%v), want its own %d", seen, err, own)
				}
				leaked, err := count(q, `SELECT count(*) FROM `+name+` WHERE `+sc.foreign, arg)
				if err != nil || leaked != 0 {
					report(table.Name, "a space reads %d rows of the other (%v)", leaked, err)
				}
			})
			if update != "" {
				inSpaceTx(t, w.api, acting, func(q queryer) {
					res, err := q.Exec(`UPDATE `+name+` SET `+pgx.Identifier{update}.Sanitize()+` = `+pgx.Identifier{update}.Sanitize()+` WHERE `+sc.foreign, arg)
					if err == nil {
						if n, _ := res.RowsAffected(); n != 0 {
							report(table.Name, "a space updated %d rows of the other", n)
						}
					}
				})
			}
			if canDelete {
				inSpaceTx(t, w.api, acting, func(q queryer) {
					res, err := q.Exec(`DELETE FROM `+name+` WHERE `+sc.foreign, arg)
					if err == nil {
						if n, _ := res.RowsAffected(); n != 0 {
							report(table.Name, "a space deleted %d rows of the other", n)
						}
					}
				})
			}
			if table.HasSpaceID {
				attack, control, err := crossInsert(t, w, table.Name, acting, other)
				switch {
				case err != nil:
					report(table.Name, "cannot try an insert into the other space: %v", err)
				case attack == "":
					report(table.Name, "a space inserted a row into the other")
				case attack != "42501":
					report(table.Name, "an insert into the other space failed with SQLSTATE %s, want the policy (42501)", attack)
				case control == "42501" && canInsertAnyColumn(t, w.admin, table.Name):
					// The same row is refused even in its own space, so the
					// refusal above does not prove the policy.
					report(table.Name, "the row refused in the other space is refused in its own too (42501): the insert policy is not proven")
				}
			}
		}

		inSpaceTx(t, w.api, "", func(q queryer) {
			if n, err := count(q, `SELECT count(*) FROM `+name); err != nil || n != 0 {
				report(table.Name, "a connection with no space sees %d rows (%v)", n, err)
			}
		})
		inSpaceTx(t, w.api, newID(t), func(q queryer) {
			if n, err := count(q, `SELECT count(*) FROM `+name); err != nil || n != 0 {
				report(table.Name, "a space that does not exist sees %d rows (%v)", n, err)
			}
		})
	}
	return findings
}

// TestLawIsolationEveryTableOfSpaceData guards criterion 3 of the closing
// story. The tables come from the catalog, so a table added later is checked
// without editing this test, and the test fails when one has no policy. With
// two spaces and a row of each in every table, each space reads and writes
// only its own rows, a connection with no space sees nothing, and row level
// security is enabled and forced everywhere, including the identities that
// belong to no space and are visible only through a participant.
func TestLawIsolationEveryTableOfSpaceData(t *testing.T) {
	t.Parallel()
	w := newIsolationWorld(t)
	tables := catalogTables(t, w.admin)

	var withSpace, names []string
	for _, table := range tables {
		names = append(names, table.Name)
		if table.HasSpaceID {
			withSpace = append(withSpace, table.Name)
		}
	}
	if len(withSpace) < 13 {
		t.Fatalf("found %d tables with a space_id, expected the whole schema: %v", len(withSpace), names)
	}
	for _, special := range []string{"spaces", "identities"} {
		if !slices.Contains(names, special) {
			t.Fatalf("the catalog lists no table %s: %v", special, names)
		}
	}

	for _, failure := range fillMissingTables(t, w, tables, seedOverrides) {
		t.Errorf("%s", failure)
	}
	for _, finding := range structureFindings(tables) {
		t.Errorf("%s", finding)
	}
	for _, finding := range dataFindings(t, w, tables) {
		t.Errorf("%s", finding)
	}
}

// probe is a table created to show that the verifier notices a flaw.
type probe struct {
	name string
	// setup returns the statements that complete the table after its creation.
	setup func(table string) []string
	// structure and data say which verifier must report the table.
	structure, data bool
}

// forced turns on and forces row level security on a table.
func forced(table string) []string {
	return []string{`ALTER TABLE kernel.` + table + ` ENABLE ROW LEVEL SECURITY`, `ALTER TABLE kernel.` + table + ` FORCE ROW LEVEL SECURITY`}
}

// ownRows is the condition every correct policy of a probe applies.
const ownRows = `space_id = (SELECT kernel.current_space_id())`

// selectPolicy limits the reads of a probe to the current space.
func selectPolicy(table string) string {
	return `CREATE POLICY p_select ON kernel.` + table + ` FOR SELECT USING (` + ownRows + `)`
}

// writePolicies are the three policies that limit the writes of a probe.
func writePolicies(table string) []string {
	return []string{
		`CREATE POLICY p_insert ON kernel.` + table + ` FOR INSERT WITH CHECK (` + ownRows + `)`,
		`CREATE POLICY p_update ON kernel.` + table + ` FOR UPDATE USING (` + ownRows + `) WITH CHECK (` + ownRows + `)`,
		`CREATE POLICY p_delete ON kernel.` + table + ` FOR DELETE USING (` + ownRows + `)`,
	}
}

// TestIsolationVerifierDetectsFlawedTables proves the two verifiers are not
// vacuous. Each probe table carries a space_id and one flaw; the verifier that
// owns the flaw must report it. The correct probe must pass both, which also
// shows that a table added by a later story, with no seed of its own, is
// filled and verified without changes.
func TestIsolationVerifierDetectsFlawedTables(t *testing.T) {
	t.Parallel()
	w := newIsolationWorld(t)
	probes := []probe{
		{"probe_no_policy", forced, true, true},
		{"probe_no_rls", func(string) []string { return nil }, true, true},
		{"probe_not_forced", func(table string) []string {
			return append([]string{`ALTER TABLE kernel.` + table + ` ENABLE ROW LEVEL SECURITY`}, append([]string{selectPolicy(table)}, writePolicies(table)...)...)
		}, true, false},
		{"probe_open_policy", func(table string) []string {
			return append(forced(table), `CREATE POLICY p_all ON kernel.`+table+` USING (true) WITH CHECK (true)`)
		}, true, true},
		{"probe_open_insert", func(table string) []string {
			return append(forced(table), selectPolicy(table), `CREATE POLICY p_insert ON kernel.`+table+` FOR INSERT WITH CHECK (true)`)
		}, true, true},
		{"probe_correct", func(table string) []string {
			return append(append(forced(table), selectPolicy(table)), writePolicies(table)...)
		}, false, false},
	}
	for _, p := range probes {
		table := p.name
		if _, err := w.admin.Exec(`CREATE TABLE kernel.` + table + ` (
			id uuid PRIMARY KEY, space_id uuid NOT NULL REFERENCES kernel.spaces (id), label text NOT NULL)`); err != nil {
			t.Fatalf("%s: create: %v", table, err)
		}
		if _, err := w.admin.Exec(`GRANT SELECT, INSERT, UPDATE, DELETE ON kernel.` + table + ` TO ` + db.APIRole); err != nil {
			t.Fatalf("%s: grant: %v", table, err)
		}
		for _, statement := range p.setup(table) {
			if _, err := w.admin.Exec(statement); err != nil {
				t.Fatalf("%s: %q: %v", table, statement, err)
			}
		}

		var mine []catalogTable
		for _, candidate := range catalogTables(t, w.admin) {
			if candidate.Name == table {
				mine = append(mine, candidate)
			}
		}
		if len(mine) != 1 || !mine[0].HasSpaceID {
			t.Fatalf("%s: the catalog does not show the probe: %+v", table, mine)
		}
		if failures := fillMissingTables(t, w, mine, nil); len(failures) != 0 {
			t.Fatalf("%s: the probe could not be filled: %v", table, failures)
		}
		if got := structureFindings(mine); (len(got) > 0) != p.structure {
			t.Errorf("%s: structure findings %v, want findings: %v", table, got, p.structure)
		}
		if got := dataFindings(t, w, mine); (len(got) > 0) != p.data {
			t.Errorf("%s: data findings %v, want findings: %v", table, got, p.data)
		}
	}

	// A table whose CHECK the synthesized row cannot meet is reported with the
	// override to add, and is verified once the override writes a valid row.
	const checked = "probe_checked"
	if _, err := w.admin.Exec(`CREATE TABLE kernel.` + checked + ` (
		id uuid PRIMARY KEY, space_id uuid NOT NULL REFERENCES kernel.spaces (id), label text NOT NULL CHECK (label ~ '^valid[.]'))`); err != nil {
		t.Fatalf("create %s: %v", checked, err)
	}
	if _, err := w.admin.Exec(`GRANT SELECT, INSERT, UPDATE, DELETE ON kernel.` + checked + ` TO ` + db.APIRole); err != nil {
		t.Fatalf("grant: %v", err)
	}
	for _, statement := range append(append(forced(checked), selectPolicy(checked)), writePolicies(checked)...) {
		if _, err := w.admin.Exec(statement); err != nil {
			t.Fatalf("%s: %q: %v", checked, statement, err)
		}
	}
	var mine []catalogTable
	for _, candidate := range catalogTables(t, w.admin) {
		if candidate.Name == checked {
			mine = append(mine, candidate)
		}
	}
	failures := fillMissingTables(t, w, mine, nil)
	if len(failures) == 0 || !strings.Contains(failures[0], "seedOverrides") || !strings.Contains(failures[0], checked) {
		t.Errorf("a table that cannot be filled: failures %v, want one naming the table and seedOverrides", failures)
	}
	overrides := map[string]func(*testing.T, *isolationWorld, string) error{
		checked: func(t *testing.T, w *isolationWorld, space string) error {
			return runIn(t, w.api, space, stmt{`INSERT INTO kernel.` + checked + ` (id, space_id, label) VALUES ($1, $2, 'valid.row')`, []any{newID(t), space}})
		},
	}
	if failures := fillMissingTables(t, w, mine, overrides); len(failures) != 0 {
		t.Errorf("the override did not fill the table: %v", failures)
	}
	if got := structureFindings(mine); len(got) != 0 {
		t.Errorf("%s: structure findings %v, want none", checked, got)
	}
	if got := dataFindings(t, w, mine); len(got) != 0 {
		t.Errorf("%s: data findings %v, want none: a row that respects its CHECK is copied and the policy is what refuses it", checked, got)
	}

	// A table with no space_id and no rule is reported too.
	orphan := []catalogTable{{Name: "orphan", RLS: true, Forced: true, Policies: []catalogPolicy{{Name: "p", Cmd: "*", Using: "true", Check: "true"}}}}
	if got := structureFindings(orphan); len(got) != 1 || !strings.Contains(got[0], "no space_id") {
		t.Errorf("a table without space_id and rule: findings %v, want the one about space_id", got)
	}
}
