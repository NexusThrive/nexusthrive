package db_test

import (
	"context"
	"database/sql"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// ledgerFunctions are the functions the ledger migration adds or replaces.
var ledgerFunctions = []string{
	"reject_ledger_change", "stamp_exchange", "guard_entry_insert",
	"check_exchange_balance", "guard_resource_change", "reject_account_reopen",
}

// TestAPIRoleCannotForgeRecordedAt guards the attack of choosing the record
// time: the API role has no privilege on the column, and even the owner gets
// the time of the transaction.
func TestAPIRoleCannotForgeRecordedAt(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	forged := func(id, key string) stmt {
		return stmt{`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, recorded_at)
			VALUES ($1, $2, 'test.exchange', $3, $4, '2000-01-01T00:00:00Z')`, []any{id, ledgerSpaceA, key, ledgerParticipantA}}
	}
	id := newID(t)
	err := runTx(t, api, ledgerSpaceA, forged(id, "forged-api"),
		entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, "1.00"),
		entryStmt(t, id, ledgerSpaceA, exAccountA2, ledgerResourceA, "-1.00"))
	requireRule(t, "API insert with recorded_at", err, "42501", "")
	err = runTx(t, api, ledgerSpaceA, stmt{`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, recorded_at)
		VALUES ($1, $2, 'test.exchange', 'forged-override', $3, now()) ON CONFLICT DO NOTHING`, []any{newID(t), ledgerSpaceA, ledgerParticipantA}})
	requireRule(t, "API insert with recorded_at and ON CONFLICT", err, "42501", "")

	// The owner may name the column and still gets the transaction time.
	before := time.Now().Add(-time.Minute)
	id = newID(t)
	err = runTx(t, admin, "", forged(id, "forged-owner"),
		entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, "1.00"),
		entryStmt(t, id, ledgerSpaceA, exAccountA2, ledgerResourceA, "-1.00"))
	if err != nil {
		t.Fatalf("the owner insert: %v", err)
	}
	var recorded time.Time
	if err := admin.QueryRow(`SELECT recorded_at FROM kernel.exchanges WHERE id = $1`, id).Scan(&recorded); err != nil {
		t.Fatalf("read exchange: %v", err)
	}
	if recorded.Before(before) {
		t.Errorf("recorded_at = %v, the owner forged it", recorded)
	}
}

// TestSpaceSwitchBeforeCommitFailsClosed guards the attack of changing
// kernel.space_id before the commit so that row level security hides the
// entries from the deferred check: the check then sees none and fails.
func TestSpaceSwitchBeforeCommitFailsClosed(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	for name, tc := range map[string]struct{ switchTo, second string }{
		"balanced then space B":    {ledgerSpaceB, "-1.00"},
		"unbalanced then space B":  {ledgerSpaceB, "-0.50"},
		"unbalanced then no space": {"", "-0.50"},
		"unbalanced then nil uuid": {"00000000-0000-0000-0000-000000000000", "-0.50"},
	} {
		id := newID(t)
		tx, err := api.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		for _, s := range []stmt{
			exchangeStmt(id, ledgerSpaceA, "k-"+name),
			entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, "1.00"),
			entryStmt(t, id, ledgerSpaceA, exAccountA2, ledgerResourceA, tc.second),
		} {
			if _, err := tx.Exec(s.query, s.args...); err != nil {
				t.Fatalf("%s: insert: %v", name, err)
			}
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, tc.switchTo); err != nil {
			t.Fatalf("switch space: %v", err)
		}
		err = tx.Commit()
		requireRule(t, name, err, "23000", "exchanges_min_entries")
	}
	if exchanges, entries := ledgerRows(t, admin); exchanges != 0 || entries != 0 {
		t.Errorf("rows after the attacks = %d exchanges, %d entries, want 0 and 0", exchanges, entries)
	}
}

// TestOnConflictCannotReuseCommittedExchange guards INSERT ... ON CONFLICT:
// DO NOTHING on a committed id inserts nothing, and the entries that follow
// fall on the same-transaction rule; DO UPDATE needs a privilege the API lacks.
func TestOnConflictCannotReuseCommittedExchange(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)
	id := commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")

	err := runTx(t, api, ledgerSpaceA,
		stmt{`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		      VALUES ($1, $2, 'test.exchange', 'other-key', $3) ON CONFLICT DO NOTHING`, []any{id, ledgerSpaceA, ledgerParticipantA}},
		entryStmt(t, id, ledgerSpaceA, exAccountA3, ledgerResourceA, "7.00"),
		entryStmt(t, id, ledgerSpaceA, exAccountA4, ledgerResourceA, "-7.00"))
	requireRule(t, "DO NOTHING then entries", err, "23000", "entries_same_transaction")

	err = runTx(t, api, ledgerSpaceA, stmt{`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		VALUES ($1, $2, 'test.exchange', 'other-key', $3) ON CONFLICT (id) DO UPDATE SET memo = 'rewritten'`, []any{id, ledgerSpaceA, ledgerParticipantA}})
	requireRule(t, "DO UPDATE on exchanges", err, "42501", "")
	err = runTx(t, api, ledgerSpaceA, stmt{`INSERT INTO kernel.entries (id, exchange_id, space_id, account_id, resource_id, amount)
		SELECT id, exchange_id, space_id, account_id, resource_id, 1 FROM kernel.entries
		ON CONFLICT (id) DO UPDATE SET amount = 1`, nil})
	requireRule(t, "DO UPDATE on entries", err, "42501", "")
	if got := count(t, admin, `SELECT count(*) FROM kernel.entries WHERE exchange_id = $1`, id); got != 2 {
		t.Errorf("the committed exchange has %d entries, want 2", got)
	}
}

// pgxConn opens a direct pgx connection to the test database, for the COPY
// protocol that database/sql does not expose. With asAPI it runs as the API
// role; otherwise as the administrator.
func pgxConn(t *testing.T, admin *sql.DB, asAPI bool) *pgx.Conn {
	t.Helper()
	var name string
	if err := admin.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatalf("current database: %v", err)
	}
	target, err := url.Parse(dbtest.URL())
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	target.Path = "/" + name
	conn, err := pgx.Connect(context.Background(), target.String())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	if asAPI {
		if _, err := conn.Exec(context.Background(), "SET ROLE "+pgx.Identifier{db.APIRole}.Sanitize()); err != nil {
			t.Fatalf("set role: %v", err)
		}
	}
	return conn
}

// decimal builds a numeric value for the binary COPY protocol.
func decimal(units int64, exp int32) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(units), Exp: exp, Valid: true}
}

// TestCopyFromStillFiresTriggers guards COPY. Row level security refuses COPY
// FROM for the API role altogether, so the role cannot bulk load the ledger.
// For the administrator, who bypasses row level security, COPY runs the same
// triggers as INSERT: a lone or unbalanced entry fails at the commit and the
// other rules fail at once.
func TestCopyFromStillFiresTriggers(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedExchangeFixtures(t, admin)
	ctx := context.Background()
	committed := commitPairAdmin(t, admin, "k-committed")

	entryColumns := []string{"id", "exchange_id", "space_id", "account_id", "resource_id", "amount"}
	type copyCase struct {
		rows        func(exchange string) [][]any
		state, rule string
		reuse       bool
	}
	cases := map[string]copyCase{
		"unbalanced pair": {func(e string) [][]any {
			return [][]any{
				{newID(t), e, ledgerSpaceA, ledgerAccountA, ledgerResourceA, decimal(5000, -2)},
				{newID(t), e, ledgerSpaceA, exAccountA2, ledgerResourceA, decimal(-4999, -2)},
			}
		}, "23000", "exchanges_conservation", false},
		"lone entry": {func(e string) [][]any {
			return [][]any{{newID(t), e, ledgerSpaceA, ledgerAccountA, ledgerResourceA, decimal(5000, -2)}}
		}, "23000", "exchanges_min_entries", false},
		"zero amount": {func(e string) [][]any {
			return [][]any{{newID(t), e, ledgerSpaceA, ledgerAccountA, ledgerResourceA, decimal(0, 0)}}
		}, "23514", "entries_amount_check", false},
		"closed account": {func(e string) [][]any {
			return [][]any{{newID(t), e, ledgerSpaceA, exAccountClosed, ledgerResourceA, decimal(100, -2)}}
		}, "23000", "entries_account_open", false},
		"committed exchange": {func(string) [][]any {
			return [][]any{
				{newID(t), committed, ledgerSpaceA, exAccountA3, ledgerResourceA, decimal(700, -2)},
				{newID(t), committed, ledgerSpaceA, exAccountA4, ledgerResourceA, decimal(-700, -2)},
			}
		}, "23000", "entries_same_transaction", true},
	}
	run := func(conn *pgx.Conn, name string, tc copyCase, wantState, wantRule string) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		exchange := committed
		if !tc.reuse {
			exchange = newID(t)
			s := exchangeStmt(exchange, ledgerSpaceA, "k-copy-"+name)
			if _, err := tx.Exec(ctx, s.query, s.args...); err != nil {
				t.Fatalf("%s: insert exchange: %v", name, err)
			}
		}
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"kernel", "entries"}, entryColumns, pgx.CopyFromRows(tc.rows(exchange)))
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		requireRule(t, name, err, wantState, wantRule)
	}
	administrator := pgxConn(t, admin, false)
	api := pgxConn(t, admin, true)
	for name, tc := range cases {
		run(administrator, "administrator "+name, tc, tc.state, tc.rule)
		run(api, "API "+name, tc, "0A000", "")
	}
	// The column grants are not what keeps the API role out of COPY, but they
	// hold too: recorded_at is refused before the protocol starts.
	tx, err := api.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	_, err = tx.CopyFrom(ctx, pgx.Identifier{"kernel", "exchanges"},
		[]string{"id", "space_id", "kind", "idempotency_key", "actor_participant_id", "recorded_at"},
		pgx.CopyFromRows([][]any{{newID(t), ledgerSpaceA, "test.exchange", "k-copy-forged", ledgerParticipantA, time.Unix(0, 0)}}))
	if err == nil {
		t.Error("COPY of recorded_at as the API role was accepted")
	}
	if exchanges, entries := ledgerRows(t, admin); exchanges != 1 || entries != 2 {
		t.Errorf("rows after the attacks = %d exchanges, %d entries, want only the committed one", exchanges, entries)
	}
}

// commitPairAdmin commits a balanced exchange of space A as the administrator.
func commitPairAdmin(t *testing.T, admin *sql.DB, key string) string {
	t.Helper()
	id, stmts := pairStmts(t, ledgerSpaceA, key, ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")
	if err := runTx(t, admin, "", stmts...); err != nil {
		t.Fatalf("commit %s: %v", key, err)
	}
	return id
}

// TestResourceScaleAndKindFrozenOnceEntriesExist guards follow-up 17: not even
// the administrator may change scale or kind of a resource that has entries,
// whatever the current space, while resources without entries stay editable.
func TestResourceScaleAndKindFrozenOnceEntriesExist(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedExchangeFixtures(t, admin)
	commitPairAdmin(t, admin, "k1")

	update := func(query string, args ...any) stmt { return stmt{query, args} }
	for name, tc := range map[string]struct{ space, query string }{
		"scale":                  {ledgerSpaceA, `UPDATE kernel.resources SET scale = 3 WHERE id = $1`},
		"kind":                   {ledgerSpaceA, `UPDATE kernel.resources SET kind = 'unit' WHERE id = $1`},
		"scale in another space": {ledgerSpaceB, `UPDATE kernel.resources SET scale = 3 WHERE id = $1`},
		"kind with no space":     {"", `UPDATE kernel.resources SET kind = 'unit' WHERE id = $1`},
	} {
		err := runTx(t, admin, tc.space, update(tc.query, ledgerResourceA))
		requireRule(t, name, err, "23001", "")
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.resources WHERE id = $1 AND scale = 2 AND kind = 'money'`, ledgerResourceA); got != 1 {
		t.Error("the resource with entries changed")
	}
	// Changes that leave scale and kind alone are not blocked.
	if err := runTx(t, admin, "", update(`UPDATE kernel.resources SET name = 'Peso colombiano', scale = scale WHERE id = $1`, ledgerResourceA)); err != nil {
		t.Errorf("a rename with the same scale must pass: %v", err)
	}
	// A resource with no entries may still change, from its own space only.
	if err := runTx(t, admin, ledgerSpaceA, update(`UPDATE kernel.resources SET scale = 1, kind = 'time' WHERE id = $1`, ledgerResourceA2)); err != nil {
		t.Errorf("a resource without entries must be editable: %v", err)
	}
	requireRule(t, "resource without entries from another space",
		runTx(t, admin, ledgerSpaceB, update(`UPDATE kernel.resources SET scale = 2 WHERE id = $1`, ledgerResourceA2)), "23001", "")
}

// TestAPICannotForgeClosedAt guards follow-up 19: the kernel stamps closed_at
// when an account closes, whatever the API role writes.
func TestAPICannotForgeClosedAt(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	for _, query := range []string{
		`UPDATE kernel.accounts SET status = 'closed', closed_at = '2000-01-01T00:00:00Z' WHERE id = $1`,
		`UPDATE kernel.accounts SET status = 'closed', closed_at = now() + interval '10 years' WHERE id = $1`,
		`UPDATE kernel.accounts SET status = 'closed' WHERE id = $1`,
	} {
		tx, err := api.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		if _, err := tx.Exec(query, exAccountA3); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		var stamped bool
		if err := tx.QueryRow(`SELECT closed_at = now() FROM kernel.accounts WHERE id = $1`, exAccountA3).Scan(&stamped); err != nil || !stamped {
			t.Errorf("%s: closed_at is not the transaction time (%v)", query, err)
		}
		_ = tx.Rollback()
	}
	// Moving closed_at of an open account stays impossible.
	err := runTx(t, api, ledgerSpaceA, stmt{`UPDATE kernel.accounts SET closed_at = '2000-01-01T00:00:00Z' WHERE id = $1`, []any{exAccountA3}})
	requireRule(t, "closed_at on an open account", err, "23514", "")
}

// TestNoSecurityDefinerFunctions guards the attack surface of privilege
// escalation: no function of the kernel runs with the rights of its owner, and
// every function of the ledger pins its search path.
func TestNoSecurityDefinerFunctions(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	if got := count(t, admin, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'kernel' AND p.prosecdef`); got != 0 {
		t.Errorf("%d kernel functions are SECURITY DEFINER, want none", got)
	}
	for _, name := range ledgerFunctions {
		var config string
		err := admin.QueryRow(`SELECT coalesce(array_to_string(proconfig, ','), '') FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'kernel' AND p.proname = $1`, name).Scan(&config)
		if err != nil {
			t.Errorf("function %s: %v", name, err)
			continue
		}
		if config != "search_path=pg_catalog, pg_temp" {
			t.Errorf("function %s config = %q, want a fixed search_path of pg_catalog, pg_temp", name, config)
		}
	}
}

// TestLedgerFunctionsIgnoreShadowing proves that a function planted in the
// temporary schema, which comes first in a hostile search path, does not
// replace round() inside the entry guard.
func TestLedgerFunctionsIgnoreShadowing(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedExchangeFixtures(t, admin)

	tx, err := admin.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`CREATE FUNCTION pg_temp.round(numeric, integer) RETURNS numeric LANGUAGE sql AS 'SELECT 0::numeric'`,
		`SET LOCAL search_path = pg_temp, public, pg_catalog`,
	} {
		if _, err := tx.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	id, stmts := pairStmts(t, ledgerSpaceA, "k-shadow", ledgerAccountA, exAccountA2, ledgerResourceA, "10.00")
	_ = id
	for _, s := range stmts {
		if _, err := tx.Exec(s.query, s.args...); err != nil {
			t.Fatalf("a planted round() must not be used: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestAPIRoleCannotRewireTheLedger gathers the attacks that need more than
// the INSERT privilege: DDL, disabling triggers, replication mode, table
// locks, role switching and system columns.
func TestAPIRoleCannotRewireTheLedger(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedExchangeFixtures(t, admin)

	for _, query := range []string{
		`ALTER TABLE kernel.entries DISABLE TRIGGER ALL`,
		`ALTER TABLE kernel.exchanges DISABLE TRIGGER exchanges_balance_check`,
		`DROP TRIGGER entries_balance_check ON kernel.entries`,
		`CREATE TRIGGER rogue BEFORE INSERT ON kernel.entries FOR EACH ROW EXECUTE FUNCTION kernel.stamp_exchange()`,
		`ALTER TABLE kernel.entries ADD COLUMN rogue int`,
		`ALTER TABLE kernel.entries DISABLE ROW LEVEL SECURITY`,
		`ALTER TABLE kernel.entries OWNER TO nexusthrive_api`,
		`DROP TABLE kernel.entries`,
		`ALTER FUNCTION kernel.guard_entry_insert() SECURITY DEFINER`,
		`CREATE OR REPLACE FUNCTION kernel.check_exchange_balance() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NULL; END'`,
		`CREATE FUNCTION kernel.rogue() RETURNS int LANGUAGE sql AS 'SELECT 1'`,
		`CREATE TABLE kernel.rogue (id int)`,
		`SET session_replication_role = replica`,
		`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, recorded_at) OVERRIDING SYSTEM VALUE
		 VALUES ('01900000-0000-7000-8000-0000000001a1', '` + ledgerSpaceA + `', 'test.exchange', 'x', '` + ledgerParticipantA + `', now())`,
	} {
		requireRule(t, query, asAPI(t, admin, query), "42501", "")
	}
	// Table locks would let one caller stall every writer; they need a privilege
	// the API role lacks, and they only exist inside a transaction.
	for _, query := range []string{
		`LOCK TABLE kernel.entries IN SHARE MODE`,
		`LOCK TABLE kernel.exchanges IN ACCESS EXCLUSIVE MODE`,
		`LOCK TABLE kernel.accounts IN ACCESS EXCLUSIVE MODE`,
	} {
		requireRule(t, query, runTx(t, dbtest.APIPool(t, admin), "", stmt{query, nil}), "42501", "")
	}
	// System columns cannot be assigned at all.
	for _, query := range []string{
		`INSERT INTO kernel.exchanges (xmin, id, space_id, kind, idempotency_key, actor_participant_id)
		 VALUES (1, '01900000-0000-7000-8000-0000000001a2', '` + ledgerSpaceA + `', 'test.exchange', 'x', '` + ledgerParticipantA + `')`,
		`INSERT INTO kernel.entries (ctid) VALUES ('(0,1)')`,
	} {
		if err := asAPI(t, admin, query); err == nil {
			t.Errorf("%s: the system column assignment was accepted", query)
		}
	}
	// There is nothing to override: no identity or generated column exists.
	if got := count(t, admin, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'kernel'
		AND table_name IN ('exchanges', 'entries') AND (is_identity = 'YES' OR is_generated <> 'NEVER')`); got != 0 {
		t.Errorf("%d identity or generated columns in the ledger, want none", got)
	}
	// SET ROLE needs another role to belong to: the API role belongs to none,
	// has no right to create objects in the schema and no special attribute.
	if got := count(t, admin, `SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = $1`, db.APIRole); got != 0 {
		t.Errorf("the API role is a member of %d roles, want none", got)
	}
	if got := count(t, admin, `SELECT count(*) FROM pg_roles WHERE rolname = $1 AND NOT (rolsuper OR rolcreaterole OR rolcreatedb OR rolbypassrls OR rolreplication)`, db.APIRole); got != 1 {
		t.Error("the API role has a special attribute")
	}
	var canCreate bool
	if err := admin.QueryRow(`SELECT has_schema_privilege($1, 'kernel', 'CREATE')`, db.APIRole).Scan(&canCreate); err != nil || canCreate {
		t.Errorf("the API role can create objects in the kernel schema (%v)", err)
	}
}

// TestAPICannotForgeActorsOrSpaces guards the attacks that try to pass the
// actor and space checks with invented rows: another space's participants are
// out of reach, a space of its own gives nothing, and platform spaces cannot
// be created.
func TestAPICannotForgeActorsOrSpaces(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	requireRule(t, "participant created in another space",
		runTx(t, api, ledgerSpaceB, stmt{`INSERT INTO kernel.participants (id, space_id, kind, display_name)
			VALUES ($1, $2, 'system', 'fake')`, []any{newID(t), ledgerSpaceA}}), "42501", "")
	requireRule(t, "platform space created by the API",
		runTx(t, api, "01900000-0000-7000-8000-0000000001b1", stmt{`INSERT INTO kernel.spaces (id, kind, name, slug)
			VALUES ('01900000-0000-7000-8000-0000000001b1', 'platform', 'fake', 'fake-platform')`, nil}), "23001", "")

	// A new space of the API's own is a different space: its exchanges cannot
	// name an actor of space A, and space A's accounts are out of reach.
	const fake = "01900000-0000-7000-8000-0000000001b2"
	id := newID(t)
	err := runTx(t, api, fake,
		stmt{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'fake', 'fake-business')`, []any{fake}},
		stmt{`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		      VALUES ($1, $2, 'test.exchange', 'k', $3)`, []any{id, fake, ledgerParticipantA}})
	requireRule(t, "own space with an actor of space A", err, "23503", "exchanges_actor_fkey")
	err = runTx(t, api, fake,
		stmt{`INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'fake', 'fake-business')`, []any{fake}},
		stmt{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'system', 'fake')`, []any{id, fake}},
		stmt{`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		      VALUES ($1, $2, 'test.exchange', 'k', $1)`, []any{id, fake}},
		entryStmt(t, id, fake, ledgerAccountA, ledgerResourceA, "1.00"),
		entryStmt(t, id, fake, exAccountA2, ledgerResourceA, "-1.00"))
	requireRule(t, "own space with accounts of space A", err, "23000", "entries_account_open")
}

// TestActorIsAssertedByTheApplicationToday fixes the accepted limit of follow-up
// 29: the database only checks that the actor exists in the space, so the API
// role may create a participant of its own space and name it. Linking the actor
// to an identity arrives with the permission checks.
func TestActorIsAssertedByTheApplicationToday(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	actor, id := newID(t), newID(t)
	err := runTx(t, api, ledgerSpaceA,
		stmt{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'system', 'asserted')`, []any{actor, ledgerSpaceA}},
		stmt{`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		      VALUES ($1, $2, 'test.exchange', 'k', $3)`, []any{id, ledgerSpaceA, actor}},
		entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, "1.00"),
		entryStmt(t, id, ledgerSpaceA, exAccountA2, ledgerResourceA, "-1.00"))
	if err != nil {
		t.Errorf("an actor created in the same space is accepted today: %v", err)
	}
}

// waitForLockWait blocks until a backend of the test database is waiting on a
// lock for a statement that contains fragment.
func waitForLockWait(t *testing.T, admin *sql.DB, fragment string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := count(t, admin, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%' || $1 || '%'`, fragment); got > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no backend waited on a lock for %q", fragment)
}

// TestCloseAccountRacesWithEntry guards criterion 5 under concurrency, in both
// orders: an entry and a close of the same account serialize, so no entry
// lands on a closed account.
func TestCloseAccountRacesWithEntry(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)
	closeAccount := stmt{`UPDATE kernel.accounts SET status = 'closed' WHERE id = $1`, []any{exAccountA3}}

	t.Run("entry first", func(t *testing.T) {
		id := newID(t)
		tx, err := api.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		for _, s := range []stmt{exchangeStmt(id, ledgerSpaceA, "k-race-1"), entryStmt(t, id, ledgerSpaceA, exAccountA3, ledgerResourceA, "1.00")} {
			if _, err := tx.Exec(s.query, s.args...); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
		done := make(chan error, 1)
		go func() { done <- runTx(t, api, ledgerSpaceA, closeAccount) }()
		waitForLockWait(t, admin, "UPDATE kernel.accounts")
		select {
		case err := <-done:
			t.Fatalf("the close did not wait for the entry transaction: %v", err)
		default:
		}
		if _, err := tx.Exec(`INSERT INTO kernel.entries (id, exchange_id, space_id, account_id, resource_id, amount)
			VALUES ($1, $2, $3, $4, $5, -1)`, newID(t), id, ledgerSpaceA, ledgerAccountA, ledgerResourceA); err != nil {
			t.Fatalf("second entry: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("the entry transaction must commit: %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("the close must finish once the entry commits: %v", err)
		}
		if got := count(t, admin, `SELECT count(*) FROM kernel.accounts WHERE id = $1 AND status = 'closed'`, exAccountA3); got != 1 {
			t.Error("the account must end closed")
		}
	})

	t.Run("close first", func(t *testing.T) {
		closing, err := api.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer closing.Rollback()
		if _, err := closing.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		if _, err := closing.Exec(`UPDATE kernel.accounts SET status = 'closed' WHERE id = $1`, exAccountA4); err != nil {
			t.Fatalf("close: %v", err)
		}
		id, stmts := pairStmts(t, ledgerSpaceA, "k-race-2", exAccountA4, ledgerAccountA, ledgerResourceA, "1.00")
		_ = id
		done := make(chan error, 1)
		go func() { done <- runTx(t, api, ledgerSpaceA, stmts...) }()
		waitForLockWait(t, admin, "INSERT INTO kernel.entries")
		if err := closing.Commit(); err != nil {
			t.Fatalf("commit close: %v", err)
		}
		requireRule(t, "entry waiting on a close", <-done, "23000", "entries_account_open")
		if got := count(t, admin, `SELECT count(*) FROM kernel.entries WHERE account_id = $1`, exAccountA4); got != 0 {
			t.Errorf("%d entries landed on the closed account", got)
		}
	})
}
