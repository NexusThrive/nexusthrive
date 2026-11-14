package db_test

import (
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// Rows the commitment tests add to the ledger fixtures: a debtor in each space
// and an account of the debtor of space A. The creditor of space A is
// ledgerParticipantA, who holds every other account of the fixtures.
const (
	cmDebtorA       = "01900000-0000-7000-8000-0000000001e1"
	cmDebtorB       = "01900000-0000-7000-8000-0000000001e2"
	cmDebtorAccount = "01900000-0000-7000-8000-0000000001e3"
)

// commitmentColumns lists every column of kernel.commitments; the grants must
// cover exactly these.
var commitmentColumns = []string{
	"id", "space_id", "kind", "debtor_participant_id", "creditor_participant_id", "resource_id", "amount",
	"fulfilled_amount", "starts_at", "due_at", "status", "origin_ref", "canceled_reason", "closed_at",
	"created_at", "created_by_participant_id",
}

// commitmentInsertColumns and fulfillmentInsertColumns are the columns the API
// role may write on insert; everything else is derived by the kernel.
var (
	commitmentInsertColumns = []string{
		"id", "space_id", "kind", "debtor_participant_id", "creditor_participant_id", "resource_id",
		"amount", "starts_at", "due_at", "status", "origin_ref", "created_by_participant_id",
	}
	fulfillmentColumns       = []string{"id", "space_id", "commitment_id", "entry_id", "resource_id", "amount", "created_at"}
	fulfillmentInsertColumns = []string{"id", "space_id", "commitment_id", "entry_id", "resource_id", "amount"}
)

// seedCommitmentFixtures extends the exchange fixtures with a debtor in each
// space and an account of the debtor, all as the administrator.
func seedCommitmentFixtures(t *testing.T, admin *sql.DB) {
	t.Helper()
	seedExchangeFixtures(t, admin)
	for _, s := range []stmt{
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'Debtor A')`, []any{cmDebtorA, ledgerSpaceA}},
		{`INSERT INTO kernel.participants (id, space_id, kind, display_name) VALUES ($1, $2, 'person', 'Debtor B')`, []any{cmDebtorB, ledgerSpaceB}},
		{`INSERT INTO kernel.accounts (id, space_id, holder_participant_id, resource_id, class, code, name)
		  VALUES ($1, $2, $3, $4, 'asset', 'DEBTOR', 'Debtor')`, []any{cmDebtorAccount, ledgerSpaceA, cmDebtorA, ledgerResourceA}},
	} {
		if _, err := admin.Exec(s.query, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.query, err)
		}
	}
}

// commitmentEnv is a database seeded for the commitment tests.
type commitmentEnv struct {
	admin, api *sql.DB
}

// newCommitmentEnv creates the database, the API pool and the fixtures.
func newCommitmentEnv(t *testing.T) commitmentEnv {
	t.Helper()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedCommitmentFixtures(t, admin)
	return commitmentEnv{admin: admin, api: api}
}

// partiesOf returns the debtor, the creditor and the resource of a space.
func partiesOf(space string) (debtor, creditor, resource string) {
	if space == ledgerSpaceB {
		return cmDebtorB, ledgerParticipantB, ledgerResourceB
	}
	return cmDebtorA, ledgerParticipantA, ledgerResourceA
}

// commitmentStmt inserts an open commitment of amount with the granted
// columns only, due in thirty days.
func commitmentStmt(id, space, amount string) stmt {
	debtor, creditor, resource := partiesOf(space)
	return commitmentOf(id, space, debtor, creditor, resource, amount)
}

// commitmentOf is commitmentStmt with explicit parties and resource.
func commitmentOf(id, space, debtor, creditor, resource, amount string) stmt {
	return stmt{
		`INSERT INTO kernel.commitments (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id,
		   amount, due_at, status, created_by_participant_id)
		 VALUES ($1, $2, 'test.commitment', $3, $4, $5, $6::numeric, now() + interval '30 days', 'open', $4)`,
		[]any{id, space, debtor, creditor, resource, amount},
	}
}

// commit commits one commitment of amount in space as the API role and
// returns its id.
func (e commitmentEnv) commit(t *testing.T, space, amount string) string {
	t.Helper()
	id := newID(t)
	if err := runTx(t, e.api, space, commitmentStmt(id, space, amount)); err != nil {
		t.Fatalf("insert commitment: %v", err)
	}
	return id
}

// pair commits a balanced exchange of space A that enters amount into the
// account credit and leaves it from debit. It returns the ids of the positive
// and the negative entry.
func (e commitmentEnv) pair(t *testing.T, key, credit, debit, resource, amount string) (positive, negative string) {
	t.Helper()
	_, stmts := pairStmts(t, ledgerSpaceA, key, credit, debit, resource, amount)
	if err := runTx(t, e.api, ledgerSpaceA, stmts...); err != nil {
		t.Fatalf("commit exchange %s: %v", key, err)
	}
	return stringAt(stmts[1].args, 0), stringAt(stmts[2].args, 0)
}

// creditEntry is pair for callers that only need the positive entry.
func (e commitmentEnv) creditEntry(t *testing.T, key, credit, debit, resource, amount string) string {
	t.Helper()
	positive, _ := e.pair(t, key, credit, debit, resource, amount)
	return positive
}

// fulfillStmt inserts a fulfillment with the granted columns only.
func fulfillStmt(t *testing.T, commitment, entry, space, resource, amount string) stmt {
	t.Helper()
	return stmt{
		`INSERT INTO kernel.fulfillments (id, space_id, commitment_id, entry_id, resource_id, amount)
		 VALUES ($1, $2, $3, $4, $5, $6::numeric)`,
		[]any{newID(t), space, commitment, entry, resource, amount},
	}
}

// fulfill commits one fulfillment of space A as the API role.
func (e commitmentEnv) fulfill(t *testing.T, commitment, entry, amount string) error {
	t.Helper()
	return runTx(t, e.api, ledgerSpaceA, fulfillStmt(t, commitment, entry, ledgerSpaceA, ledgerResourceA, amount))
}

// commitmentState reads status, fulfilled amount and closing flag.
func commitmentState(t *testing.T, admin *sql.DB, id string) (status, fulfilled string, closed bool) {
	t.Helper()
	if err := admin.QueryRow(`SELECT status, fulfilled_amount::text, closed_at IS NOT NULL FROM kernel.commitments WHERE id = $1`, id).
		Scan(&status, &fulfilled, &closed); err != nil {
		t.Fatalf("read commitment: %v", err)
	}
	return status, fulfilled, closed
}

// TestAPIInsertCommitmentStoresDerivedColumns guards criterion 1: the API
// inserts an open commitment and the kernel supplies the rest.
func TestAPIInsertCommitmentStoresDerivedColumns(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)

	before := time.Now().Add(-time.Minute)
	id := e.commit(t, ledgerSpaceA, "100.00")

	var status, fulfilled, amount, createdBy string
	var closed sql.NullTime
	var created, starts time.Time
	if err := e.admin.QueryRow(`SELECT status, fulfilled_amount::text, amount::text, created_by_participant_id::text, closed_at, created_at, starts_at
		FROM kernel.commitments WHERE id = $1`, id).Scan(&status, &fulfilled, &amount, &createdBy, &closed, &created, &starts); err != nil {
		t.Fatalf("read commitment: %v", err)
	}
	if status != "open" || fulfilled != "0" || amount != "100.00" || createdBy != ledgerParticipantA || closed.Valid {
		t.Errorf("commitment = %s %s %s %s closed %v", status, fulfilled, amount, createdBy, closed.Valid)
	}
	if created.Before(before) || starts.Before(before) {
		t.Errorf("created_at %v and starts_at %v must be the transaction time", created, starts)
	}
	var typeName string
	if err := e.admin.QueryRow(`SELECT pg_typeof(created_at)::text FROM kernel.commitments LIMIT 1`).Scan(&typeName); err != nil || typeName != "timestamp with time zone" {
		t.Errorf("created_at type = %q (%v), want timestamp with time zone", typeName, err)
	}
}

// TestCommitmentWithoutDueAtOrStatusRejected guards criterion 2: neither the
// due date nor the status has a default, so omitting them fails for the owner
// and for the API role.
func TestCommitmentWithoutDueAtOrStatusRejected(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)

	for _, column := range []string{"due_at", "status"} {
		var def sql.NullString
		if err := e.admin.QueryRow(`SELECT column_default FROM information_schema.columns
			WHERE table_schema = 'kernel' AND table_name = 'commitments' AND column_name = $1`, column).Scan(&def); err != nil {
			t.Fatalf("read default of %s: %v", column, err)
		}
		if def.Valid {
			t.Errorf("%s has default %q, want none", column, def.String)
		}
	}
	const withoutDue = `INSERT INTO kernel.commitments (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id, amount, status, created_by_participant_id)
		VALUES ($1, $2, 'test.commitment', $3, $4, $5, 10, 'open', $4)`
	const withoutStatus = `INSERT INTO kernel.commitments (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id, amount, due_at, created_by_participant_id)
		VALUES ($1, $2, 'test.commitment', $3, $4, $5, 10, now() + interval '1 day', $4)`
	for name, query := range map[string]string{"due_at": withoutDue, "status": withoutStatus} {
		args := []any{newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantA, ledgerResourceA}
		requireRule(t, "owner without "+name, runTx(t, e.admin, "", stmt{query, args}), "23502", "")
		args = []any{newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantA, ledgerResourceA}
		requireRule(t, "API without "+name, runTx(t, e.api, ledgerSpaceA, stmt{query, args}), "23502", "")
	}
}

// TestCommitmentChecksRejectedByDatabase guards criterion 3: an amount that is
// not positive, a due date before the start and equal parties are refused by
// CHECK constraints, for every writer.
func TestCommitmentChecksRejectedByDatabase(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)

	insert := func(amount, due, debtor, kind, origin string) stmt {
		return stmt{`INSERT INTO kernel.commitments (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id,
			amount, starts_at, due_at, status, origin_ref, created_by_participant_id)
			VALUES ($1, $2, $6, $3, $4, $5, $7::numeric, now(), $8::timestamptz, 'open', $9, $4)`,
			[]any{newID(t), ledgerSpaceA, debtor, ledgerParticipantA, ledgerResourceA, kind, amount, due, nullIfBlank(origin)}}
	}
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	cases := map[string]struct {
		stmt       stmt
		constraint string
	}{
		"zero amount":      {insert("0", future, cmDebtorA, "test.commitment", ""), "commitments_amount_check"},
		"negative amount":  {insert("-5", future, cmDebtorA, "test.commitment", ""), "commitments_amount_check"},
		"NaN amount":       {insert("NaN", future, cmDebtorA, "test.commitment", ""), "commitments_amount_check"},
		"infinite amount":  {insert("Infinity", future, cmDebtorA, "test.commitment", ""), "commitments_amount_check"},
		"huge amount":      {insert("1e30", future, cmDebtorA, "test.commitment", ""), "commitments_amount_check"},
		"due before start": {insert("10", past, cmDebtorA, "test.commitment", ""), "commitments_due_after_start"},
		"same parties":     {insert("10", future, ledgerParticipantA, "test.commitment", ""), "commitments_distinct_parties"},
		"bad kind":         {insert("10", future, cmDebtorA, "Commitment", ""), ""},
		"blank origin":     {insert("10", future, cmDebtorA, "test.commitment", " "), ""},
	}
	for name, tc := range cases {
		requireRule(t, "owner: "+name, runTx(t, e.admin, "", tc.stmt), "23514", tc.constraint)
		requireRule(t, "API: "+name, runTx(t, e.api, ledgerSpaceA, tc.stmt), "23514", tc.constraint)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments`); got != 0 {
		t.Errorf("%d commitments after the refused inserts, want 0", got)
	}
}

// nullIfBlank returns nil for an empty text and the text otherwise, for
// optional columns; a blank but not empty text stays as it is.
func nullIfBlank(text string) any {
	if text == "" {
		return nil
	}
	return text
}

// TestCommitmentCoherenceChecksHoldWithoutTriggers shows the second layer: with
// the triggers off, CHECK constraints and keys alone refuse an incoherent row.
func TestCommitmentCoherenceChecksHoldWithoutTriggers(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	for _, query := range []string{
		`ALTER TABLE kernel.commitments DISABLE TRIGGER commitments_guard_insert`,
		`ALTER TABLE kernel.commitments DISABLE TRIGGER commitments_guard_update`,
	} {
		if _, err := e.admin.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	insert := func(status, fulfilled, closedAt, reason string) stmt {
		return stmt{`INSERT INTO kernel.commitments (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id,
			amount, fulfilled_amount, due_at, status, closed_at, canceled_reason, created_by_participant_id)
			VALUES ($1, $2, 'test.commitment', $3, $4, $5, 100, $6::numeric, now() + interval '1 day', $7, $8::timestamptz, $9, $4)`,
			[]any{newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantA, ledgerResourceA, fulfilled, status, nullIfBlank(closedAt), nullIfBlank(reason)}}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for name, tc := range map[string]struct {
		stmt       stmt
		constraint string
	}{
		"unknown status":              {insert("late", "0", now, ""), "commitments_status_check"},
		"open with a closing time":    {insert("open", "0", now, ""), "commitments_closed_coherent"},
		"closed without closing time": {insert("expired", "0", "", ""), "commitments_closed_coherent"},
		"canceled without reason":     {insert("canceled", "0", now, ""), "commitments_canceled_coherent"},
		"reason on an open one":       {insert("open", "0", "", "why"), "commitments_canceled_coherent"},
		"fulfilled with a gap":        {insert("fulfilled", "40", now, ""), "commitments_state_amount_coherent"},
		"open and fully fulfilled":    {insert("open", "100", "", ""), "commitments_state_amount_coherent"},
		"more than the amount":        {insert("open", "150", "", ""), "commitments_fulfilled_within_amount"},
		"negative fulfilled":          {insert("open", "-1", "", ""), "commitments_fulfilled_within_amount"},
	} {
		requireRule(t, name, runTx(t, e.admin, "", tc.stmt), "23514", tc.constraint)
	}
}

// TestFulfilledAmountCannotExceedAmount guards criterion 4. With the guard
// trigger disabled in this throwaway database the CHECK refuses an excess; with
// it active the writers are stopped earlier by the trigger.
func TestFulfilledAmountCannotExceedAmount(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "100.00")

	set := stmt{`UPDATE kernel.commitments SET fulfilled_amount = 150 WHERE id = $1`, []any{id}}
	requireRule(t, "API with the trigger", runTx(t, e.api, ledgerSpaceA, set), "23001", "")
	requireRule(t, "owner with the trigger", runTx(t, e.admin, "", set), "23001", "")

	if _, err := e.admin.Exec(`ALTER TABLE kernel.commitments DISABLE TRIGGER commitments_guard_update`); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	requireRule(t, "owner without the trigger", runTx(t, e.admin, "", set), "23514", "commitments_fulfilled_within_amount")
	if status, fulfilled, _ := commitmentState(t, e.admin, id); status != "open" || fulfilled != "0" {
		t.Errorf("commitment = %s %s, want open 0", status, fulfilled)
	}
}

// TestAmountBeyondResourceScaleIsInvalidInput guards the scale of the
// resource: 10.001 does not fit two decimals, 10.100 is stored as 10.10.
func TestAmountBeyondResourceScaleIsInvalidInput(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)

	for name, pool := range map[string]*sql.DB{"API": e.api, "owner": e.admin} {
		space := ledgerSpaceA
		if name == "owner" {
			space = ""
		}
		requireRule(t, name+" 10.001", runTx(t, pool, space, commitmentStmt(newID(t), ledgerSpaceA, "10.001")), "23000", "commitments_amount_scale")
		// The unit resource has no decimals at all.
		requireRule(t, name+" 1.5 units", runTx(t, pool, space,
			commitmentOf(newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantA, ledgerResourceA2, "1.5")), "23000", "commitments_amount_scale")
	}
	id := e.commit(t, ledgerSpaceA, "10.100")
	var stored string
	if err := e.admin.QueryRow(`SELECT amount::text FROM kernel.commitments WHERE id = $1`, id).Scan(&stored); err != nil || stored != "10.10" {
		t.Errorf("stored amount = %q (%v), want 10.10", stored, err)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments`); got != 1 {
		t.Errorf("%d commitments, want only the valid one", got)
	}
}

// TestFulfillmentEntryRulesInDatabase guards criterion 8 and the stricter rules
// of ADR-0033: the entry must be positive, of an account of the creditor, of
// the same resource and space, outside a reversal, applied once and in full.
func TestFulfillmentEntryRulesInDatabase(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	commitment := e.commit(t, ledgerSpaceA, "100.00")

	good := e.creditEntry(t, "k-good", exAccountA2, exAccountA3, ledgerResourceA, "40.00")
	_, debitEntry := e.pair(t, "k-debit-side", exAccountA3, exAccountA4, ledgerResourceA, "10.00")
	debtorEntry := e.creditEntry(t, "k-debtor", cmDebtorAccount, exAccountA4, ledgerResourceA, "10.00")
	unitEntry := e.creditEntry(t, "k-unit", exAccountUnit1, exAccountUnit2, ledgerResourceA2, "10")
	otherSpace := newID(t)
	otherSpaceStmts := []stmt{exchangeStmt(otherSpace, ledgerSpaceB, "k-b"),
		entryStmt(t, otherSpace, ledgerSpaceB, exAccountB1, ledgerResourceB, "10.00"),
		entryStmt(t, otherSpace, ledgerSpaceB, exAccountB2, ledgerResourceB, "-10.00")}
	if err := runTx(t, e.api, ledgerSpaceB, otherSpaceStmts...); err != nil {
		t.Fatalf("exchange of space B: %v", err)
	}
	e.creditEntry(t, "k-original", exAccountA2, exAccountA4, ledgerResourceA, "30.00")
	reversalEntry := e.reversalCreditEntry(t, "k-original", "k-reversal")

	cases := []struct {
		name, entry, resource, amount, constraint string
	}{
		{"entry of a debit", debitEntry, ledgerResourceA, "10.00", "fulfillments_entry_creditor"},
		{"entry of an account that is not the creditor's", debtorEntry, ledgerResourceA, "10.00", "fulfillments_entry_creditor"},
		{"entry of a reversal", reversalEntry, ledgerResourceA, "30.00", "fulfillments_entry_reversal"},
		{"entry of another resource", unitEntry, ledgerResourceA, "10", "fulfillments_entry_fkey"},
		{"entry of another space", stringAt(otherSpaceStmts[1].args, 0), ledgerResourceA, "10.00", "fulfillments_entry_fkey"},
		{"resource of another commitment", good, ledgerResourceA2, "40.00", "fulfillments_commitment_fkey"},
		{"amount lower than the entry", good, ledgerResourceA, "39.00", "fulfillments_amount_entry"},
		{"amount higher than the entry", good, ledgerResourceA, "41.00", "fulfillments_amount_entry"},
		{"missing entry", newID(t), ledgerResourceA, "40.00", "fulfillments_entry_fkey"},
	}
	for _, tc := range cases {
		err := runTx(t, e.api, ledgerSpaceA, fulfillStmt(t, commitment, tc.entry, ledgerSpaceA, tc.resource, tc.amount))
		requireRule(t, tc.name, err, "23000", tc.constraint)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.fulfillments`); got != 0 {
		t.Errorf("%d fulfillments after the refused ones, want 0", got)
	}
	if status, fulfilled, _ := commitmentState(t, e.admin, commitment); status != "open" || fulfilled != "0" {
		t.Errorf("commitment = %s %s, want open 0", status, fulfilled)
	}
	// The valid one still works, and the same entry cannot be applied twice,
	// to the same commitment or to another.
	if err := e.fulfill(t, commitment, good, "40.00"); err != nil {
		t.Fatalf("a valid fulfillment: %v", err)
	}
	other := e.commit(t, ledgerSpaceA, "50.00")
	requireRule(t, "same entry, same commitment", e.fulfill(t, commitment, good, "40.00"), "23505", "fulfillments_entry_key")
	requireRule(t, "same entry, another commitment", e.fulfill(t, other, good, "40.00"), "23505", "fulfillments_entry_key")
	if status, fulfilled, _ := commitmentState(t, e.admin, commitment); status != "open" || fulfilled != "40.00" {
		t.Errorf("commitment = %s %s, want open 40.00", status, fulfilled)
	}
}

// stringAt returns the argument at index as a string.
func stringAt(args []any, index int) string { return args[index].(string) }

// reversalCreditEntry reverses the exchange committed under originalKey and
// returns the positive entry of the reversal, which lands on the account that
// gave in the original.
func (e commitmentEnv) reversalCreditEntry(t *testing.T, originalKey, key string) string {
	t.Helper()
	var original string
	if err := e.admin.QueryRow(`SELECT id::text FROM kernel.exchanges WHERE idempotency_key = $1`, originalKey).Scan(&original); err != nil {
		t.Fatalf("read exchange: %v", err)
	}
	id := newID(t)
	stmts := []stmt{{
		`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, reverses_exchange_id)
		 VALUES ($1, $2, 'kernel.exchange.reversal', $3, $4, $5)`,
		[]any{id, ledgerSpaceA, key, ledgerParticipantA, original}}}
	// The original entered A2 and left A4; the reversal enters A4, an account of the creditor.
	positive := entryStmt(t, id, ledgerSpaceA, exAccountA4, ledgerResourceA, "30.00")
	stmts = append(stmts, entryStmt(t, id, ledgerSpaceA, exAccountA2, ledgerResourceA, "-30.00"), positive)
	if err := runTx(t, e.api, ledgerSpaceA, stmts...); err != nil {
		t.Fatalf("commit reversal: %v", err)
	}
	return stringAt(positive.args, 0)
}

// TestFulfillmentCompositeKeysHoldWithoutGuard shows the second layer of the
// space and resource rules: with the guard dropped the composite keys alone
// refuse a fulfillment that mixes spaces or resources.
func TestFulfillmentCompositeKeysHoldWithoutGuard(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	commitment := e.commit(t, ledgerSpaceA, "100.00")
	entry := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "40.00")
	unit := e.creditEntry(t, "k-unit", exAccountUnit1, exAccountUnit2, ledgerResourceA2, "10")

	for name, want := range map[string][2]string{
		"fulfillments_commitment_fkey": {"commitment_id,space_id,resource_id", "id,space_id,resource_id"},
		"fulfillments_entry_fkey":      {"entry_id,space_id,resource_id", "id,space_id,resource_id"},
		"commitments_resource_fkey":    {"resource_id,space_id", "id,space_id"},
		"commitments_debtor_fkey":      {"debtor_participant_id,space_id", "id,space_id"},
		"commitments_creditor_fkey":    {"creditor_participant_id,space_id", "id,space_id"},
		"commitments_created_by_fkey":  {"created_by_participant_id,space_id", "id,space_id"},
	} {
		if local, referenced := columnsOfConstraint(t, e.admin, name); local != want[0] || referenced != want[1] {
			t.Errorf("%s = (%s) -> (%s), want (%s) -> (%s)", name, local, referenced, want[0], want[1])
		}
	}
	if _, err := e.admin.Exec(`ALTER TABLE kernel.fulfillments DISABLE TRIGGER fulfillments_guard_insert`); err != nil {
		t.Fatalf("disable guard: %v", err)
	}
	requireRule(t, "entry of another resource", runTx(t, e.admin, "",
		fulfillStmt(t, commitment, unit, ledgerSpaceA, ledgerResourceA, "10")), "23503", "fulfillments_entry_fkey")
	requireRule(t, "commitment of another resource", runTx(t, e.admin, "",
		fulfillStmt(t, commitment, unit, ledgerSpaceA, ledgerResourceA2, "10")), "23503", "fulfillments_commitment_fkey")
	requireRule(t, "space of another space", runTx(t, e.admin, "",
		fulfillStmt(t, commitment, entry, ledgerSpaceB, ledgerResourceA, "40.00")), "23503", "")
}

// TestCommitmentsForcedRLSAndGrants guards criteria 11 and 12: forced row level
// security, only the planned policies and exact column grants.
func TestCommitmentsForcedRLSAndGrants(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	for table, policies := range map[string][]string{
		"commitments":  {"INSERT", "SELECT", "UPDATE"},
		"fulfillments": {"INSERT", "SELECT"},
	} {
		requireForcedRLS(t, admin, table)
		if got := policyCommands(t, admin, table); !slices.Equal(got, policies) {
			t.Errorf("%s policies = %v, want %v", table, got, policies)
		}
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
	if got, want := privilegeSet(t, admin, "commitments"),
		expectPrivileges(commitmentColumns, commitmentInsertColumns, []string{"fulfilled_amount", "canceled_reason"}); !slices.Equal(got, want) {
		t.Errorf("commitments privileges = %v, want %v", got, want)
	}
	if got, want := privilegeSet(t, admin, "fulfillments"),
		expectPrivileges(fulfillmentColumns, fulfillmentInsertColumns, nil); !slices.Equal(got, want) {
		t.Errorf("fulfillments privileges = %v, want %v", got, want)
	}
	if got := columnSet(t, admin, "commitments"); len(got) != len(commitmentColumns) {
		t.Errorf("commitments has %d columns, the grants cover %d: update the grants with the table", len(got), len(commitmentColumns))
	}
	if got := columnSet(t, admin, "fulfillments"); len(got) != len(fulfillmentColumns) {
		t.Errorf("fulfillments has %d columns, the grants cover %d: update the grants with the table", len(got), len(fulfillmentColumns))
	}
}

// TestCommitmentsIsolationBetweenSpaces guards criterion 12: a space sees none
// of the commitments of another and cannot write into it.
func TestCommitmentsIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "100.00")
	entry := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "40.00")
	if err := e.fulfill(t, id, entry, "40.00"); err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	e.commit(t, ledgerSpaceB, "5.00")

	tx, err := e.api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceB); err != nil {
		t.Fatalf("set space: %v", err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM kernel.commitments WHERE space_id = '` + ledgerSpaceA + `'`,
		`SELECT count(*) FROM kernel.fulfillments`,
	} {
		var n int
		if err := tx.QueryRow(query).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s = %d (%v), want 0 from space B", query, n, err)
		}
	}
	result, err := tx.Exec(`UPDATE kernel.commitments SET canceled_reason = 'x' WHERE id = $1`, id)
	if err != nil {
		t.Fatalf("update from space B: %v", err)
	}
	if n, _ := result.RowsAffected(); n != 0 {
		t.Errorf("update from space B changed %d rows, want 0", n)
	}
	_ = tx.Rollback()

	requireRule(t, "insert into space A from space B", runTx(t, e.api, ledgerSpaceB, commitmentStmt(newID(t), ledgerSpaceA, "1.00")), "42501", "")
	// The commitment of space A is invisible from space B, so there is nothing to fulfill.
	requireRule(t, "fulfillment into space A from space B", runTx(t, e.api, ledgerSpaceB,
		fulfillStmt(t, id, entry, ledgerSpaceA, ledgerResourceA, "40.00")), "23000", "fulfillments_commitment_fkey")
	if status, fulfilled, _ := commitmentState(t, e.admin, id); status != "open" || fulfilled != "40.00" {
		t.Errorf("commitment of space A = %s %s, want open 40.00", status, fulfilled)
	}
}

// TestAmountColumnsOfCommitmentsAreNumeric complements the regression guard of
// resources_schema_test: the new amount columns exist and are exact decimals.
func TestAmountColumnsOfCommitmentsAreNumeric(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	for _, column := range [][2]string{{"commitments", "amount"}, {"commitments", "fulfilled_amount"}, {"fulfillments", "amount"}} {
		var dataType string
		if err := admin.QueryRow(`SELECT data_type FROM information_schema.columns
			WHERE table_schema = 'kernel' AND table_name = $1 AND column_name = $2`, column[0], column[1]).Scan(&dataType); err != nil || dataType != "numeric" {
			t.Errorf("%s.%s is %q (%v), want numeric", column[0], column[1], dataType, err)
		}
	}
}
