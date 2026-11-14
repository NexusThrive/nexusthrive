package db_test

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// Accounts the ledger tests add to the fixtures of seedLedgerFixtures. Space A
// holds ledgerAccountA plus these: three more accounts of the first resource
// (two decimals), two of the unit resource (no decimals) and a closed one.
// Space B holds two accounts of its own resource.
const (
	exAccountA2     = "01900000-0000-7000-8000-000000000101"
	exAccountA3     = "01900000-0000-7000-8000-000000000102"
	exAccountA4     = "01900000-0000-7000-8000-000000000103"
	exAccountUnit1  = "01900000-0000-7000-8000-000000000104"
	exAccountUnit2  = "01900000-0000-7000-8000-000000000105"
	exAccountClosed = "01900000-0000-7000-8000-000000000106"
	exAccountB1     = "01900000-0000-7000-8000-000000000107"
	exAccountB2     = "01900000-0000-7000-8000-000000000108"
)

// seedExchangeFixtures extends the ledger fixtures with the accounts above, all
// as the administrator.
func seedExchangeFixtures(t *testing.T, admin *sql.DB) {
	t.Helper()
	seedLedgerFixtures(t, admin)
	const insert = `INSERT INTO kernel.accounts (id, space_id, holder_participant_id, resource_id, class, code, name, status, closed_at)
		VALUES ($1, $2, $3, $4, 'asset', $5, $5, $6, $7)`
	for _, row := range []struct {
		id, space, holder, resource, code, status string
		closedAt                                  any
	}{
		{exAccountA2, ledgerSpaceA, ledgerParticipantA, ledgerResourceA, "A2", "open", nil},
		{exAccountA3, ledgerSpaceA, ledgerParticipantA, ledgerResourceA, "A3", "open", nil},
		{exAccountA4, ledgerSpaceA, ledgerParticipantA, ledgerResourceA, "A4", "open", nil},
		{exAccountUnit1, ledgerSpaceA, ledgerParticipantA, ledgerResourceA2, "U1", "open", nil},
		{exAccountUnit2, ledgerSpaceA, ledgerParticipantA, ledgerResourceA2, "U2", "open", nil},
		{exAccountClosed, ledgerSpaceA, ledgerParticipantA, ledgerResourceA, "CLOSED", "closed", time.Now().UTC()},
		{exAccountB1, ledgerSpaceB, ledgerParticipantB, ledgerResourceB, "B1", "open", nil},
		{exAccountB2, ledgerSpaceB, ledgerParticipantB, ledgerResourceB, "B2", "open", nil},
	} {
		if _, err := admin.Exec(insert, row.id, row.space, row.holder, row.resource, row.code, row.status, row.closedAt); err != nil {
			t.Fatalf("seed account %s: %v", row.code, err)
		}
	}
}

// stmt is one SQL statement of a test transaction.
type stmt struct {
	query string
	args  []any
}

// newID returns a fresh UUID v7.
func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	return id.String()
}

// actorOf returns the fixture participant that lives in the given space.
func actorOf(space string) string {
	if space == ledgerSpaceB {
		return ledgerParticipantB
	}
	return ledgerParticipantA
}

// exchangeStmt inserts an exchange with the granted columns only.
func exchangeStmt(id, space, key string) stmt {
	return stmt{
		`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		 VALUES ($1, $2, 'test.exchange', $3, $4)`,
		[]any{id, space, key, actorOf(space)},
	}
}

// entryStmt inserts an entry with a fresh id.
func entryStmt(t *testing.T, exchangeID, space, account, resource, amount string) stmt {
	t.Helper()
	return stmt{
		`INSERT INTO kernel.entries (id, exchange_id, space_id, account_id, resource_id, amount)
		 VALUES ($1, $2, $3, $4, $5, $6::numeric)`,
		[]any{newID(t), exchangeID, space, account, resource, amount},
	}
}

// pairStmts returns the statements of a balanced two-entry exchange: amount
// enters credit and leaves debit.
func pairStmts(t *testing.T, space, key, credit, debit, resource, amount string) (string, []stmt) {
	t.Helper()
	id := newID(t)
	negative := "-" + strings.TrimPrefix(amount, "-")
	return id, []stmt{
		exchangeStmt(id, space, key),
		entryStmt(t, id, space, credit, resource, amount),
		entryStmt(t, id, space, debit, resource, negative),
	}
}

// runTx runs the statements in one transaction and commits. A non-empty space
// is fixed as the current space first. It returns the first error, including
// the one of the COMMIT, which is where the deferred checks fail.
func runTx(t *testing.T, pool *sql.DB, space string, stmts ...stmt) error {
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
	for _, s := range stmts {
		if _, err := tx.Exec(s.query, s.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// constraintOf returns the constraint name carried by a database error.
func constraintOf(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.ConstraintName
	}
	return ""
}

// requireRule fails unless err has the SQLSTATE and, when given, the
// constraint name that Go uses to tell the rule apart.
func requireRule(t *testing.T, label string, err error, state, constraint string) {
	t.Helper()
	if got := pgCode(err); got != state {
		t.Errorf("%s: SQLSTATE %q (%v), want %s", label, got, err, state)
		return
	}
	if constraint != "" && constraintOf(err) != constraint {
		t.Errorf("%s: constraint %q (%v), want %q", label, constraintOf(err), err, constraint)
	}
}

// ledgerRows counts the rows of both ledger tables.
func ledgerRows(t *testing.T, admin *sql.DB) (exchanges, entries int) {
	t.Helper()
	return count(t, admin, `SELECT count(*) FROM kernel.exchanges`), count(t, admin, `SELECT count(*) FROM kernel.entries`)
}

// commitPair commits a balanced exchange as the API role and returns its id.
func commitPair(t *testing.T, api *sql.DB, space, key, credit, debit, resource, amount string) string {
	t.Helper()
	id, stmts := pairStmts(t, space, key, credit, debit, resource, amount)
	if err := runTx(t, api, space, stmts...); err != nil {
		t.Fatalf("commit exchange %s: %v", key, err)
	}
	return id
}

// TestBalancedExchangeCommitsWithUTCRecordedAt guards criterion 1: a balanced
// exchange is stored with the amounts as written and a recorded_at taken by
// the kernel.
func TestBalancedExchangeCommitsWithUTCRecordedAt(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	before := time.Now().Add(-time.Second)
	id := commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")
	after := time.Now().Add(time.Second)

	var recorded time.Time
	if err := admin.QueryRow(`SELECT recorded_at FROM kernel.exchanges WHERE id = $1`, id).Scan(&recorded); err != nil {
		t.Fatalf("read exchange: %v", err)
	}
	if recorded.Before(before) || recorded.After(after) {
		t.Errorf("recorded_at = %v, want between %v and %v", recorded, before, after)
	}
	amounts := stringColumn(t, admin, `SELECT amount::text AS shown FROM kernel.entries ORDER BY amount`)
	if !slices.Equal(amounts, []string{"-50.00", "50.00"}) {
		t.Errorf("amounts = %v, want [-50.00 50.00]", amounts)
	}
	var kind string
	if err := admin.QueryRow(`SELECT pg_typeof(recorded_at)::text FROM kernel.exchanges LIMIT 1`).Scan(&kind); err != nil || kind != "timestamp with time zone" {
		t.Errorf("recorded_at type = %q (%v), want timestamp with time zone", kind, err)
	}
}

// TestUnbalancedExchangeFailsAtCommitLeavesNoRows guards criterion 2: the
// inserts pass and the deferred check fails the COMMIT, leaving nothing.
func TestUnbalancedExchangeFailsAtCommitLeavesNoRows(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	id := newID(t)
	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	for _, s := range []stmt{
		exchangeStmt(id, ledgerSpaceA, "k1"),
		entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, "50.00"),
		entryStmt(t, id, ledgerSpaceA, exAccountA2, ledgerResourceA, "-49.99"),
	} {
		if _, err := tx.Exec(s.query, s.args...); err != nil {
			t.Fatalf("the inserts must pass, only the commit fails: %v", err)
		}
	}
	requireRule(t, "commit of +50.00 and -49.99", tx.Commit(), "23000", "exchanges_conservation")
	if exchanges, entries := ledgerRows(t, admin); exchanges != 0 || entries != 0 {
		t.Errorf("rows after a failed commit = %d exchanges, %d entries, want 0 and 0", exchanges, entries)
	}
}

// TestBalanceChecksAreDeferrable shows that the checks are constraint
// triggers deferred to the commit: the first entry alone does not fail.
func TestBalanceChecksAreDeferrable(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	for _, name := range []string{"exchanges_balance_check", "entries_balance_check"} {
		var deferrable, deferred bool
		err := admin.QueryRow(`SELECT condeferrable, condeferred FROM pg_constraint WHERE contype = 't' AND conname = $1`, name).Scan(&deferrable, &deferred)
		if err != nil {
			t.Fatalf("read constraint trigger %s: %v", name, err)
		}
		if !deferrable || !deferred {
			t.Errorf("%s: deferrable %v, initially deferred %v, want both true", name, deferrable, deferred)
		}
	}

	id := newID(t)
	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	for _, s := range []stmt{exchangeStmt(id, ledgerSpaceA, "k1"), entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, "10.00")} {
		if _, err := tx.Exec(s.query, s.args...); err != nil {
			t.Errorf("a lone first entry must not fail before the commit: %v", err)
		}
	}
}

// TestBalanceIsPerResource guards criterion 3: the sum is zero per
// (exchange, resource), so a global zero across two resources is not enough.
func TestBalanceIsPerResource(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	build := func(key string, amounts [4]string) []stmt {
		id := newID(t)
		return []stmt{
			exchangeStmt(id, ledgerSpaceA, key),
			entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, amounts[0]),
			entryStmt(t, id, ledgerSpaceA, exAccountA2, ledgerResourceA, amounts[1]),
			entryStmt(t, id, ledgerSpaceA, exAccountUnit1, ledgerResourceA2, amounts[2]),
			entryStmt(t, id, ledgerSpaceA, exAccountUnit2, ledgerResourceA2, amounts[3]),
		}
	}
	requireRule(t, "R1 zero and R2 +1",
		runTx(t, api, ledgerSpaceA, build("k-bad-1", [4]string{"5.00", "-5.00", "3", "-2"})...), "23000", "exchanges_conservation")
	// R1 sums +1.00 and R2 sums -1, so the whole adds up to zero, yet each
	// resource is unbalanced.
	requireRule(t, "R1 +1 and R2 -1",
		runTx(t, api, ledgerSpaceA, build("k-bad-2", [4]string{"2.00", "-1.00", "-3", "2"})...), "23000", "")
	crossed := []stmt{}
	id := newID(t)
	crossed = append(crossed,
		exchangeStmt(id, ledgerSpaceA, "k-bad-3"),
		entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, "1.00"),
		entryStmt(t, id, ledgerSpaceA, exAccountUnit1, ledgerResourceA2, "-1"))
	requireRule(t, "one entry per resource, global sum zero", runTx(t, api, ledgerSpaceA, crossed...), "23000", "exchanges_conservation")
	if exchanges, _ := ledgerRows(t, admin); exchanges != 0 {
		t.Errorf("rejected exchanges left %d rows", exchanges)
	}
	good := build("k-good", [4]string{"5.00", "-5.00", "3", "-3"})
	if err := runTx(t, api, ledgerSpaceA, good...); err != nil {
		t.Errorf("an exchange balanced in each resource must commit: %v", err)
	}
}

// TestExchangeWithoutEntriesRejectedAtCommit guards criterion 4: the check on
// exchanges is what catches an exchange that has no entries at all.
func TestExchangeWithoutEntriesRejectedAtCommit(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	err := runTx(t, api, ledgerSpaceA, exchangeStmt(newID(t), ledgerSpaceA, "k-empty"))
	requireRule(t, "exchange without entries", err, "23000", "exchanges_min_entries")
	if exchanges, _ := ledgerRows(t, admin); exchanges != 0 {
		t.Errorf("the empty exchange left %d rows", exchanges)
	}
}

// TestExchangeWithSingleEntryRejected guards criterion 4 for one entry.
func TestExchangeWithSingleEntryRejected(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	id := newID(t)
	err := runTx(t, api, ledgerSpaceA, exchangeStmt(id, ledgerSpaceA, "k-single"),
		entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, "1.00"))
	requireRule(t, "exchange with one entry", err, "23000", "exchanges_min_entries")
	if exchanges, entries := ledgerRows(t, admin); exchanges != 0 || entries != 0 {
		t.Errorf("the single entry exchange left %d exchanges and %d entries", exchanges, entries)
	}
}

// TestZeroNaNInfinityAmountsRejected guards criterion 4 and the amount range:
// the CHECK refuses zero, NaN, the infinities and values of 1e30 or more.
func TestZeroNaNInfinityAmountsRejected(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	for _, amount := range []string{"0", "0.00", "-0", "NaN", "Infinity", "-Infinity", "1000000000000000000000000000000", "-1000000000000000000000000000000"} {
		id := newID(t)
		err := runTx(t, api, ledgerSpaceA, exchangeStmt(id, ledgerSpaceA, "k-"+amount),
			entryStmt(t, id, ledgerSpaceA, exAccountUnit1, ledgerResourceA2, amount))
		requireRule(t, "amount "+amount, err, "23514", "entries_amount_check")
	}
	// The largest value of the range is accepted.
	id := newID(t)
	big := "999999999999999999999999999999"
	err := runTx(t, api, ledgerSpaceA, exchangeStmt(id, ledgerSpaceA, "k-max"),
		entryStmt(t, id, ledgerSpaceA, exAccountUnit1, ledgerResourceA2, big),
		entryStmt(t, id, ledgerSpaceA, exAccountUnit2, ledgerResourceA2, "-"+big))
	if err != nil {
		t.Errorf("the largest amount of the range must be accepted: %v", err)
	}
}

// TestExchangeEntryCountIsBoundedByDatabase guards the cost of the balance
// check: it runs once per entry, so the database itself refuses more than 100
// entries in an exchange, whatever the caller validated.
func TestExchangeEntryCountIsBoundedByDatabase(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	build := func(key string, entries int) []stmt {
		id := newID(t)
		stmts := []stmt{exchangeStmt(id, ledgerSpaceA, key)}
		for i := 0; i < entries; i++ {
			amount := "1.00"
			if i%2 == 1 {
				amount = "-1.00"
			}
			stmts = append(stmts, entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceA, amount))
		}
		return stmts
	}
	if err := runTx(t, api, ledgerSpaceA, build("k-100", 100)...); err != nil {
		t.Errorf("100 entries must commit: %v", err)
	}
	requireRule(t, "102 entries", runTx(t, api, ledgerSpaceA, build("k-102", 102)...), "23000", "exchanges_max_entries")
}

// TestEntryScaleEnforcedByDatabase guards criterion 6 for the API role by SQL:
// the scale of the resource decides, by value, and the stored amount is the
// canonical one.
func TestEntryScaleEnforcedByDatabase(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	for _, tc := range []struct{ account, resource, amount string }{
		{ledgerAccountA, ledgerResourceA, "10.001"},
		{exAccountUnit1, ledgerResourceA2, "5.5"},
		{exAccountUnit1, ledgerResourceA2, "0.1"},
	} {
		id := newID(t)
		err := runTx(t, api, ledgerSpaceA, exchangeStmt(id, ledgerSpaceA, "k-"+tc.amount),
			entryStmt(t, id, ledgerSpaceA, tc.account, tc.resource, tc.amount))
		requireRule(t, "amount "+tc.amount, err, "23000", "entries_amount_scale")
	}
	commitPair(t, api, ledgerSpaceA, "k-canonical", ledgerAccountA, exAccountA2, ledgerResourceA, "10.100")
	commitPair(t, api, ledgerSpaceA, "k-canonical-unit", exAccountUnit1, exAccountUnit2, ledgerResourceA2, "5.0")
	got := stringColumn(t, admin, `SELECT amount::text AS shown FROM kernel.entries ORDER BY amount`)
	if !slices.Equal(got, []string{"-10.10", "-5", "5", "10.10"}) {
		t.Errorf("stored amounts = %v, want the canonical -10.10 -5 5 10.10", got)
	}
}

// TestEntryAccountRulesInDatabase guards criterion 5 by SQL: another resource,
// a closed account and an account of another space are rule failures raised
// before the foreign key, so Go can tell them apart.
func TestEntryAccountRulesInDatabase(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	for name, tc := range map[string]struct {
		account, resource, constraint string
	}{
		"account of another resource": {ledgerAccountA, ledgerResourceA2, "entries_account_resource"},
		"closed account":              {exAccountClosed, ledgerResourceA, "entries_account_open"},
		"account of another space":    {exAccountB1, ledgerResourceA, "entries_account_open"},
		"account that does not exist": {newID(t), ledgerResourceA, "entries_account_open"},
	} {
		id := newID(t)
		err := runTx(t, api, ledgerSpaceA, exchangeStmt(id, ledgerSpaceA, "k-"+name),
			entryStmt(t, id, ledgerSpaceA, tc.account, tc.resource, "1.00"))
		requireRule(t, name, err, "23000", tc.constraint)
	}
	// The administrator bypasses row level security and is stopped by the
	// space filter of the trigger itself.
	id := newID(t)
	err := runTx(t, admin, "", exchangeStmt(id, ledgerSpaceA, "k-admin-cross"),
		entryStmt(t, id, ledgerSpaceA, exAccountB1, ledgerResourceA, "1.00"))
	requireRule(t, "administrator with an account of another space", err, "23000", "entries_account_open")
	id = newID(t)
	err = runTx(t, admin, "", exchangeStmt(id, ledgerSpaceA, "k-admin-resource"),
		entryStmt(t, id, ledgerSpaceA, ledgerAccountA, ledgerResourceB, "1.00"))
	requireRule(t, "administrator with a resource of another space", err, "23000", "entries_account_resource")
}

// TestEntryCannotJoinCommittedExchange guards criterion 7 and the attack of
// adding balanced entries to history that is already written.
func TestEntryCannotJoinCommittedExchange(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	id := commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")
	for label, pool := range map[string]*sql.DB{"API role": api, "administrator": admin} {
		space := ledgerSpaceA
		if pool == admin {
			space = ""
		}
		err := runTx(t, pool, space,
			entryStmt(t, id, ledgerSpaceA, exAccountA3, ledgerResourceA, "7.00"),
			entryStmt(t, id, ledgerSpaceA, exAccountA4, ledgerResourceA, "-7.00"))
		requireRule(t, label+" adds a balanced pair to a committed exchange", err, "23000", "entries_same_transaction")
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.entries WHERE exchange_id = $1`, id); got != 2 {
		t.Errorf("the committed exchange has %d entries, want 2", got)
	}
}

// TestLedgerIsAppendOnly guards criterion 7: the API role lacks the privilege
// and the administrator is stopped by the triggers, TRUNCATE CASCADE included.
func TestLedgerIsAppendOnly(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)
	commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")

	for _, query := range []string{
		`UPDATE kernel.entries SET amount = 1`, `DELETE FROM kernel.entries`, `TRUNCATE kernel.entries`,
		`UPDATE kernel.exchanges SET memo = 'x'`, `DELETE FROM kernel.exchanges`, `TRUNCATE kernel.exchanges`,
	} {
		requireRule(t, "API: "+query, asAPI(t, admin, query), "42501", "")
	}
	for _, query := range []string{
		`UPDATE kernel.entries SET amount = 1`, `DELETE FROM kernel.entries`, `TRUNCATE kernel.entries`,
		`UPDATE kernel.exchanges SET memo = 'x'`, `UPDATE kernel.exchanges SET recorded_at = now()`,
		`DELETE FROM kernel.exchanges`, `TRUNCATE kernel.exchanges, kernel.entries`, `TRUNCATE kernel.exchanges CASCADE`,
		`TRUNCATE kernel.accounts CASCADE`, `TRUNCATE kernel.resources CASCADE`,
		`TRUNCATE kernel.participants CASCADE`, `TRUNCATE kernel.spaces CASCADE`,
	} {
		_, err := admin.Exec(query)
		requireRule(t, "administrator: "+query, err, "23001", "")
	}
	// Alone, the parent table is protected by PostgreSQL itself, because entries
	// reference it; the statements above cover the ways around that.
	_, err := admin.Exec(`TRUNCATE kernel.exchanges`)
	requireRule(t, "administrator: TRUNCATE kernel.exchanges alone", err, "0A000", "")
	if exchanges, entries := ledgerRows(t, admin); exchanges != 1 || entries != 2 {
		t.Errorf("the ledger changed: %d exchanges, %d entries, want 1 and 2", exchanges, entries)
	}
}

// TestExchangesTruncateTriggerHoldsWithoutForeignKey shows that the truncate
// trigger of exchanges does not depend on the foreign key that makes
// PostgreSQL refuse the statement on its own.
func TestExchangesTruncateTriggerHoldsWithoutForeignKey(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)
	commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")

	if _, err := admin.Exec(`ALTER TABLE kernel.entries DROP CONSTRAINT entries_exchange_fkey`); err != nil {
		t.Fatalf("drop foreign key: %v", err)
	}
	_, err := admin.Exec(`TRUNCATE kernel.exchanges`)
	requireRule(t, "TRUNCATE kernel.exchanges", err, "23001", "")
	if exchanges, _ := ledgerRows(t, admin); exchanges != 1 {
		t.Errorf("exchanges = %d, want 1", exchanges)
	}
}

// TestLedgerTriggersHoldEvenIfGrantedByMistake shows the second layer: with
// the privileges and the policies a careless migration could add, the API role
// still cannot change or empty the ledger.
func TestLedgerTriggersHoldEvenIfGrantedByMistake(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)
	commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")

	for _, table := range []string{"exchanges", "entries"} {
		for _, query := range []string{
			`GRANT UPDATE, DELETE, TRUNCATE ON kernel.` + table + ` TO nexusthrive_api`,
			`CREATE POLICY mistaken_update ON kernel.` + table + ` FOR UPDATE USING (true) WITH CHECK (true)`,
			`CREATE POLICY mistaken_delete ON kernel.` + table + ` FOR DELETE USING (true)`,
		} {
			if _, err := admin.Exec(query); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		}
	}
	for _, query := range []string{
		`UPDATE kernel.entries SET amount = 1`, `DELETE FROM kernel.entries`, `TRUNCATE kernel.entries`,
		`UPDATE kernel.exchanges SET memo = 'x'`, `DELETE FROM kernel.exchanges`, `TRUNCATE kernel.exchanges, kernel.entries`,
	} {
		tx, err := admin.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(`SET LOCAL ROLE nexusthrive_api`); err != nil {
			t.Fatalf("set role: %v", err)
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		_, err = tx.Exec(query)
		_ = tx.Rollback()
		requireRule(t, query+" after a mistaken grant", err, "23001", "")
	}
	if exchanges, entries := ledgerRows(t, admin); exchanges != 1 || entries != 2 {
		t.Errorf("the ledger changed: %d exchanges, %d entries", exchanges, entries)
	}
}

// TestReversalMustMirrorOriginal guards criterion 8: the net sum per account of
// a reversal is the exact opposite of the original, else the commit fails.
func TestReversalMustMirrorOriginal(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	original := func(key string) string {
		return commitPair(t, api, ledgerSpaceA, key, ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")
	}
	reversal := func(of, key string, lines ...[2]string) error {
		id := newID(t)
		stmts := []stmt{{
			`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, reverses_exchange_id)
			 VALUES ($1, $2, 'kernel.exchange.reversal', $3, $4, $5)`,
			[]any{id, ledgerSpaceA, key, ledgerParticipantA, of},
		}}
		for _, line := range lines {
			stmts = append(stmts, entryStmt(t, id, ledgerSpaceA, line[0], ledgerResourceA, line[1]))
		}
		return runTx(t, api, ledgerSpaceA, stmts...)
	}

	for name, lines := range map[string][][2]string{
		"different amounts":      {{ledgerAccountA, "-40.00"}, {exAccountA2, "40.00"}},
		"different accounts":     {{exAccountA3, "-50.00"}, {exAccountA4, "50.00"}},
		"same direction":         {{ledgerAccountA, "50.00"}, {exAccountA2, "-50.00"}},
		"one account swapped":    {{ledgerAccountA, "-50.00"}, {exAccountA3, "50.00"}},
		"extra account involved": {{ledgerAccountA, "-50.00"}, {exAccountA2, "20.00"}, {exAccountA3, "30.00"}},
	} {
		of := original("k-orig-" + name)
		requireRule(t, name, reversal(of, "k-rev-"+name, lines...), "23000", "exchanges_reversal_mirror")
	}
	// The exact mirror passes, and so does a split of the same net per account.
	if err := reversal(original("k-orig-exact"), "k-rev-exact", [2]string{ledgerAccountA, "-50.00"}, [2]string{exAccountA2, "50.00"}); err != nil {
		t.Errorf("the exact mirror must commit: %v", err)
	}
	if err := reversal(original("k-orig-split"), "k-rev-split",
		[2]string{ledgerAccountA, "-20.00"}, [2]string{ledgerAccountA, "-30.00"}, [2]string{exAccountA2, "50.00"}); err != nil {
		t.Errorf("a split with the same net per account must commit: %v", err)
	}
}

// TestReversalIsUnique guards criterion 8 in the database: an exchange is
// reversed once, never by itself and never across spaces.
func TestReversalIsUnique(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	of := commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")
	reverse := func(space, of, key, credit, debit, resource, amount string) error {
		id := newID(t)
		return runTx(t, api, space,
			stmt{`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, reverses_exchange_id)
			      VALUES ($1, $2, 'kernel.exchange.reversal', $3, $4, $5)`, []any{id, space, key, actorOf(space), of}},
			entryStmt(t, id, space, credit, resource, amount), entryStmt(t, id, space, debit, resource, "-"+strings.TrimPrefix(amount, "-")))
	}
	if err := reverse(ledgerSpaceA, of, "k-rev-1", exAccountA2, ledgerAccountA, ledgerResourceA, "50.00"); err != nil {
		t.Fatalf("first reversal: %v", err)
	}
	requireRule(t, "second reversal", reverse(ledgerSpaceA, of, "k-rev-2", exAccountA2, ledgerAccountA, ledgerResourceA, "50.00"), "23505", "exchanges_reverses_once_key")
	requireRule(t, "reversal of an exchange of another space", reverse(ledgerSpaceB, of, "k-rev-b", exAccountB1, exAccountB2, ledgerResourceB, "50.00"), "23503", "exchanges_reverses_fkey")

	_, err := admin.Exec(`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, reverses_exchange_id)
		VALUES ($1, $2, 'kernel.exchange.reversal', 'k-self', $3, $1)`, newID(t), ledgerSpaceA, ledgerParticipantA)
	requireRule(t, "exchange that reverses itself", err, "23514", "exchanges_not_self_reverse")
}

// TestIdempotencyKeyUniquePerSpace guards criterion 9: today a repeated key is
// rejected; HU-kernel-idempotency-14 will return the original result instead.
func TestIdempotencyKeyUniquePerSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	commitPair(t, api, ledgerSpaceA, "same-key", ledgerAccountA, exAccountA2, ledgerResourceA, "1.00")
	_, repeated := pairStmts(t, ledgerSpaceA, "same-key", ledgerAccountA, exAccountA2, ledgerResourceA, "2.00")
	requireRule(t, "same key in the same space", runTx(t, api, ledgerSpaceA, repeated...), "23505", "exchanges_idempotency_key")
	commitPair(t, api, ledgerSpaceB, "same-key", exAccountB1, exAccountB2, ledgerResourceB, "1.00")
	if got := count(t, admin, `SELECT count(*) FROM kernel.exchanges WHERE idempotency_key = 'same-key'`); got != 2 {
		t.Errorf("exchanges with the key = %d, want one per space", got)
	}
	for name, key := range map[string]string{"blank": "  ", "empty": "", "too long": strings.Repeat("k", 256)} {
		requireRule(t, "key "+name, runTx(t, api, ledgerSpaceA, exchangeStmt(newID(t), ledgerSpaceA, key)), "23514", "")
	}
	_, err := admin.Exec(`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		VALUES ($1, $2, 'test.exchange', NULL, $3)`, newID(t), ledgerSpaceA, ledgerParticipantA)
	requireRule(t, "null key", err, "23502", "")
}

// TestLedgerIsolationBetweenSpaces guards criterion 10: space B neither reads
// nor writes the ledger of space A.
func TestLedgerIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)
	id := commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")

	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceB); err != nil {
		t.Fatalf("set space: %v", err)
	}
	for _, table := range []string{"exchanges", "entries"} {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM kernel.` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("space B reads %d rows of %s (%v), want 0", n, table, err)
		}
	}
	_ = tx.Rollback()

	_, forged := pairStmts(t, ledgerSpaceA, "forged", ledgerAccountA, exAccountA2, ledgerResourceA, "9.00")
	requireRule(t, "space B writes an exchange of space A", runTx(t, api, ledgerSpaceB, forged...), "42501", "")
	// The guard runs before the row level security check and rejects first.
	requireRule(t, "space B adds entries to an exchange of A with the space of A",
		runTx(t, api, ledgerSpaceB, entryStmt(t, id, ledgerSpaceA, exAccountA3, ledgerResourceA, "1.00")), "23000", "entries_same_transaction")
	requireRule(t, "space B adds entries to an exchange of A with its own space",
		runTx(t, api, ledgerSpaceB, entryStmt(t, id, ledgerSpaceB, exAccountB1, ledgerResourceB, "1.00")), "23000", "entries_same_transaction")
	if exchanges, entries := ledgerRows(t, admin); exchanges != 1 || entries != 2 {
		t.Errorf("the ledger changed: %d exchanges, %d entries, want 1 and 2", exchanges, entries)
	}
}

// TestEntriesPolicyHoldsWithoutGuard shows that the row level security policy
// of entries refuses a foreign space on its own, without relying on the guard
// trigger that normally answers first.
func TestEntriesPolicyHoldsWithoutGuard(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)
	id := commitPair(t, api, ledgerSpaceA, "k1", ledgerAccountA, exAccountA2, ledgerResourceA, "50.00")

	if _, err := admin.Exec(`DROP TRIGGER entries_guard ON kernel.entries`); err != nil {
		t.Fatalf("drop guard: %v", err)
	}
	err := runTx(t, api, ledgerSpaceB, entryStmt(t, id, ledgerSpaceA, exAccountA3, ledgerResourceA, "1.00"))
	requireRule(t, "space B writes an entry of space A", err, "42501", "")
}

// TestLedgerForeignKeysAreComposite guards the references of the ledger:
// foreign key checks ignore row level security, so each key carries the space
// and the account key also carries the resource. With the guard trigger gone,
// the keys alone still refuse an account or a resource of another space.
func TestLedgerForeignKeysAreComposite(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedExchangeFixtures(t, admin)

	for name, want := range map[string][2]string{
		"entries_exchange_fkey":   {"exchange_id,space_id", "id,space_id"},
		"entries_account_fkey":    {"account_id,space_id,resource_id", "id,space_id,resource_id"},
		"exchanges_actor_fkey":    {"actor_participant_id,space_id", "id,space_id"},
		"exchanges_reverses_fkey": {"reverses_exchange_id,space_id", "id,space_id"},
	} {
		local, referenced := columnsOfConstraint(t, admin, name)
		if local != want[0] || referenced != want[1] {
			t.Errorf("%s = (%s) -> (%s), want (%s) -> (%s)", name, local, referenced, want[0], want[1])
		}
	}

	if _, err := admin.Exec(`DROP TRIGGER entries_guard ON kernel.entries`); err != nil {
		t.Fatalf("drop guard: %v", err)
	}
	for name, tc := range map[string]struct{ account, resource string }{
		"account of another space":   {exAccountB1, ledgerResourceA},
		"resource of another space":  {ledgerAccountA, ledgerResourceB},
		"other resource, same space": {ledgerAccountA, ledgerResourceA2},
	} {
		id := newID(t)
		err := runTx(t, admin, "", exchangeStmt(id, ledgerSpaceA, "k-"+name),
			entryStmt(t, id, ledgerSpaceA, tc.account, tc.resource, "1"))
		requireRule(t, name, err, "23503", "entries_account_fkey")
	}
}

// TestLedgerForcedRLSAndGrants guards criteria 7 and 10: forced row level
// security, only the planned policies and exact column grants, without
// recorded_at.
func TestLedgerForcedRLSAndGrants(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	for _, table := range []string{"exchanges", "entries"} {
		requireForcedRLS(t, admin, table)
		for _, privilege := range []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
			var ok bool
			if err := admin.QueryRow(`SELECT has_table_privilege($1, $2, $3)`, db.APIRole, "kernel."+table, privilege).Scan(&ok); err != nil {
				t.Fatalf("privilege query: %v", err)
			}
			if ok {
				t.Errorf("API role must not have %s on kernel.%s", privilege, table)
			}
		}
		if got := policyCommands(t, admin, table); !slices.Equal(got, []string{"INSERT", "SELECT"}) {
			t.Errorf("%s policies = %v, want [INSERT SELECT]", table, got)
		}
	}
	exchangeInsert := []string{"id", "space_id", "kind", "idempotency_key", "actor_participant_id", "reverses_exchange_id", "memo", "metadata", "occurred_at"}
	exchangeSelect := append(slices.Clone(exchangeInsert), "recorded_at")
	if got, want := privilegeSet(t, admin, "exchanges"), expectPrivileges(exchangeSelect, exchangeInsert, nil); !slices.Equal(got, want) {
		t.Errorf("exchanges privileges = %v, want %v", got, want)
	}
	entryColumns := []string{"id", "exchange_id", "space_id", "account_id", "resource_id", "amount"}
	if got, want := privilegeSet(t, admin, "entries"), expectPrivileges(entryColumns, entryColumns, nil); !slices.Equal(got, want) {
		t.Errorf("entries privileges = %v, want %v", got, want)
	}
	if got := columnSet(t, admin, "exchanges"); len(got) != len(exchangeSelect) {
		t.Errorf("exchanges has %d columns, the grants cover %d: update the grants with the table", len(got), len(exchangeSelect))
	}
	if got := columnSet(t, admin, "entries"); len(got) != len(entryColumns) {
		t.Errorf("entries has %d columns, the grants cover %d: update the grants with the table", len(got), len(entryColumns))
	}
}

// TestExchangeActorRules guards criterion 11 by SQL: the actor is required and
// must live in the space of the exchange.
func TestExchangeActorRules(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedExchangeFixtures(t, admin)

	_, err := admin.Exec(`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		VALUES ($1, $2, 'test.exchange', 'k1', NULL)`, newID(t), ledgerSpaceA)
	requireRule(t, "exchange without actor", err, "23502", "")
	err = runTx(t, api, ledgerSpaceA, stmt{
		`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		 VALUES ($1, $2, 'test.exchange', 'k2', $3)`, []any{newID(t), ledgerSpaceA, ledgerParticipantB}})
	requireRule(t, "actor of another space", err, "23503", "exchanges_actor_fkey")
}

// TestExchangeCheckConstraints exercises the CHECK constraints of exchanges and
// entries by SQL as the administrator.
func TestExchangeCheckConstraints(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedExchangeFixtures(t, admin)

	const insert = `INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, memo, metadata)
		VALUES ($1, $2, $3, 'k', $4, $5, $6::jsonb)`
	type row struct{ id, kind, memo, metadata string }
	good := row{id: newID(t), kind: "test.exchange", metadata: `{}`}
	cases := map[string]func(*row){
		"id not v7":      func(r *row) { r.id = "01900000-0000-4000-8000-0000000001ff" },
		"one segment":    func(r *row) { r.kind = "exchange" },
		"uppercase kind": func(r *row) { r.kind = "Test.exchange" },
		"long kind":      func(r *row) { r.kind = "test." + strings.Repeat("a", 130) },
		"blank memo":     func(r *row) { r.memo = " " },
		"array metadata": func(r *row) { r.metadata = `[]` },
		"null metadata":  func(r *row) { r.metadata = `null` },
	}
	for name, mutate := range cases {
		r := good
		mutate(&r)
		var memo any
		if r.memo != "" {
			memo = r.memo
		}
		_, err := admin.Exec(insert, r.id, ledgerSpaceA, r.kind, ledgerParticipantA, memo, r.metadata)
		requireRule(t, name, err, "23514", "")
	}
}

// TestEntriesAmountIsNumeric checks that the entries amount is numeric and not
// a floating type; the general guard is TestAmountColumnsAreNumeric.
func TestEntriesAmountIsNumeric(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	var dataType string
	if err := admin.QueryRow(`SELECT data_type FROM information_schema.columns
		WHERE table_schema = 'kernel' AND table_name = 'entries' AND column_name = 'amount'`).Scan(&dataType); err != nil {
		t.Fatalf("read entries.amount: %v", err)
	}
	if dataType != "numeric" {
		t.Errorf("entries.amount is %s, want numeric", dataType)
	}
}
