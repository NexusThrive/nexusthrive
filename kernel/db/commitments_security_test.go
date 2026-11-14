package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// updateOf builds an UPDATE of one commitment.
func updateOf(id, set string) stmt {
	return stmt{`UPDATE kernel.commitments SET ` + set + ` WHERE id = $1`, []any{id}}
}

// closeCommitments brings three commitments of space A to each terminal state
// and returns their ids by state. The fulfilled one is covered by a real entry;
// the expired one by the way the kernel does it: a timer that fires and its event.
func (e commitmentEnv) closeCommitments(t *testing.T) map[string]string {
	t.Helper()
	fulfilled := e.commit(t, ledgerSpaceA, "40.00")
	entry := e.creditEntry(t, "k-close", exAccountA2, exAccountA3, ledgerResourceA, "40.00")
	if err := e.fulfill(t, fulfilled, entry, "40.00"); err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	canceled := e.commit(t, ledgerSpaceA, "10.00")
	if err := runTx(t, e.api, ledgerSpaceA, updateOf(canceled, `canceled_reason = 'asked to stop'`)); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	expired := e.expireCommitment(t, ledgerSpaceA, "10.00")
	for status, id := range map[string]string{"fulfilled": fulfilled, "canceled": canceled, "expired": expired} {
		if got, _, closed := commitmentState(t, e.admin, id); got != status || !closed {
			t.Fatalf("commitment %s = %s closed %v", status, got, closed)
		}
	}
	return map[string]string{"fulfilled": fulfilled, "canceled": canceled, "expired": expired}
}

// TestAPICannotForgeStatusOrFulfilledAmount guards the attacks of inventing a
// state: the insert admits only an open commitment with nothing fulfilled, and
// no update writes the fulfilled amount unless it is the sum of fulfillments.
// The owner is held to the same triggers as the API role.
func TestAPICannotForgeStatusOrFulfilledAmount(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	const columns = `id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id, amount, due_at, status, created_by_participant_id`
	insert := func(extraColumns, extraValues, status string) stmt {
		return stmt{`INSERT INTO kernel.commitments (` + columns + extraColumns + `)
			VALUES ($1, $2, 'test.commitment', $3, $4, $5, 100, now() + interval '1 day', '` + status + `', $4` + extraValues + `)`,
			[]any{newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantA, ledgerResourceA}}
	}
	// Both writers are stopped by the trigger, with the same error.
	for _, status := range []string{"fulfilled", "canceled", "expired"} {
		requireRule(t, "API insert as "+status, runTx(t, e.api, ledgerSpaceA, insert("", "", status)), "23001", "")
		requireRule(t, "owner insert as "+status, runTx(t, e.admin, "", insert("", "", status)), "23001", "")
	}
	// Columns the API role may not write fail for lack of privilege; the owner
	// may name them and is stopped by the trigger or has the value replaced.
	for name, extra := range map[string][2]string{
		"fulfilled_amount": {", fulfilled_amount", ", 100"},
		"closed_at":        {", closed_at", ", now()"},
		"canceled_reason":  {", canceled_reason", ", 'why'"},
		"created_at":       {", created_at", ", '2000-01-01T00:00:00Z'"},
	} {
		requireRule(t, "API insert with "+name, runTx(t, e.api, ledgerSpaceA, insert(extra[0], extra[1], "open")), "42501", "")
	}
	for name, extra := range map[string][2]string{
		"fulfilled_amount": {", fulfilled_amount", ", 50"},
		"closed_at":        {", closed_at", ", now()"},
		"canceled_reason":  {", canceled_reason", ", 'why'"},
	} {
		requireRule(t, "owner insert with "+name, runTx(t, e.admin, "", insert(extra[0], extra[1], "open")), "23001", "")
	}
	// A false creation time is replaced by the transaction time.
	forgedCreation := insert(", created_at", ", '2000-01-01T00:00:00Z'", "open")
	if err := runTx(t, e.admin, "", forgedCreation); err != nil {
		t.Fatalf("owner insert with created_at: %v", err)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments WHERE created_at < now() - interval '1 minute'`); got != 0 {
		t.Error("the owner forged created_at")
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments`); got != 1 {
		t.Errorf("%d commitments, want only the owner's valid one", got)
	}

	// Updates: no fulfilled amount that is not the sum of fulfillments.
	id := e.commit(t, ledgerSpaceA, "100.00")
	for _, set := range []string{`fulfilled_amount = amount`, `fulfilled_amount = 10`, `fulfilled_amount = 0.01`} {
		requireRule(t, "API "+set, runTx(t, e.api, ledgerSpaceA, updateOf(id, set)), "23001", "")
		requireRule(t, "owner "+set, runTx(t, e.admin, "", updateOf(id, set)), "23001", "")
	}
	// With no current space the row is invisible, so the statement changes nothing.
	if err := runTx(t, e.api, "", updateOf(id, `fulfilled_amount = 10`)); err != nil {
		t.Errorf("update without a current space: %v", err)
	}
	if status, fulfilled, closed := commitmentState(t, e.admin, id); status != "open" || fulfilled != "0" || closed {
		t.Errorf("commitment = %s %s closed %v, want open 0 open", status, fulfilled, closed)
	}
}

// TestAPICannotSetStatusOrExpire guards that no column that decides the state
// can be chosen by the API role. status is updatable only so that the cascade of
// a fired timer runs with its rights, and the guard refuses every write of it
// that is not that cascade; closed_at has no privilege at all.
func TestAPICannotSetStatusOrExpire(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "100.00")
	for _, set := range []string{`status = 'expired'`, `status = 'fulfilled'`, `status = 'canceled'`, `status = 'late'`} {
		requireRule(t, set, runTx(t, e.api, ledgerSpaceA, updateOf(id, set)), "23001", "")
	}
	// Writing the state it already has changes nothing.
	if err := runTx(t, e.api, ledgerSpaceA, updateOf(id, `status = 'open'`)); err != nil {
		t.Errorf("status = 'open' on an open commitment: %v", err)
	}
	for _, set := range []string{`closed_at = now()`, `status = 'expired', closed_at = now()`} {
		requireRule(t, set, runTx(t, e.api, ledgerSpaceA, updateOf(id, set)), "42501", "")
	}
	if status, _, _ := commitmentState(t, e.admin, id); status != "open" {
		t.Errorf("status = %s, want open", status)
	}
}

// TestAPICannotChangeIdentityColumns guards that what defines a commitment is
// immutable: the API role lacks the privilege and the trigger stops the owner,
// also when the privilege is granted by mistake.
func TestAPICannotChangeIdentityColumns(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "100.00")

	sets := map[string]string{
		"amount": `amount = 1`, "starts_at": `starts_at = now() - interval '1 year'`,
		"kind": `kind = 'test.other'`, "debtor": `debtor_participant_id = '` + ledgerParticipantA + `'`,
		"creditor": `creditor_participant_id = '` + cmDebtorA + `'`, "resource": `resource_id = '` + ledgerResourceA2 + `'`,
		"space": `space_id = '` + ledgerSpaceB + `'`, "origin": `origin_ref = 'test.origin/9'`,
		"created_by": `created_by_participant_id = '` + cmDebtorA + `'`, "created_at": `created_at = now()`,
		"id": `id = '` + newID(t) + `'`,
	}
	for name, set := range sets {
		requireRule(t, "API "+name, runTx(t, e.api, ledgerSpaceA, updateOf(id, set)), "42501", "")
	}
	for name, set := range sets {
		requireRule(t, "owner "+name, runTx(t, e.admin, "", updateOf(id, set)), "23001", "")
	}
	if _, err := e.admin.Exec(`GRANT UPDATE ON kernel.commitments TO nexusthrive_api`); err != nil {
		t.Fatalf("grant: %v", err)
	}
	for name, set := range sets {
		requireRule(t, "API with the privilege granted by mistake: "+name, runTx(t, e.api, ledgerSpaceA, updateOf(id, set)), "23001", "")
	}
	var amount string
	if err := e.admin.QueryRow(`SELECT amount::text FROM kernel.commitments WHERE id = $1`, id).Scan(&amount); err != nil || amount != "100.00" {
		t.Errorf("amount = %q (%v), want 100.00", amount, err)
	}
}

// TestTerminalStatesAreFinal guards that a closed commitment never changes: not
// reopened, not given another reason or closing time, by the owner or by the
// API role with the privileges granted by mistake. An open commitment cannot be
// forged into a terminal one either.
func TestTerminalStatesAreFinal(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	closed := e.closeCommitments(t)
	open := e.commit(t, ledgerSpaceA, "100.00")
	if _, err := e.admin.Exec(`GRANT UPDATE (status, closed_at, amount, due_at) ON kernel.commitments TO nexusthrive_api`); err != nil {
		t.Fatalf("grant: %v", err)
	}

	for state, id := range closed {
		for _, set := range []string{
			`status = 'open'`, `status = 'open', closed_at = NULL`, `status = 'fulfilled'`, `status = 'canceled'`,
			`canceled_reason = 'another reason'`, `canceled_reason = NULL`, `closed_at = now()`, `fulfilled_amount = 0`,
			`amount = 1`, `due_at = now() + interval '1 day'`,
		} {
			requireRule(t, "owner on "+state+": "+set, runTx(t, e.admin, "", updateOf(id, set)), "23001", "")
			requireRule(t, "API on "+state+": "+set, runTx(t, e.api, ledgerSpaceA, updateOf(id, set)), "23001", "")
		}
		// Even a statement that changes nothing goes through the guard of a closed row.
		requireRule(t, "no-op on "+state, runTx(t, e.admin, "", updateOf(id, `fulfilled_amount = fulfilled_amount`)), "23001", "")
	}
	for _, set := range []string{
		`status = 'fulfilled'`, `status = 'canceled', canceled_reason = 'x'`, `status = 'canceled'`,
		`closed_at = now()`, `status = 'expired', closed_at = '2000-01-01T00:00:00Z'`,
		`status = 'expired', canceled_reason = 'x'`, `status = 'late'`, `amount = 1`,
	} {
		requireRule(t, "API on an open one: "+set, runTx(t, e.api, ledgerSpaceA, updateOf(open, set)), "23001", "")
	}
	if status, _, _ := commitmentState(t, e.admin, open); status != "open" {
		t.Errorf("status = %s, want open", status)
	}
}

// TestExpiryStampsClosedAtAndIsOneEffect guards the way a commitment expires:
// the kernel stamps closed_at with the time of the database, and the write that
// expires it can do nothing else. The cascade is turned off in this throwaway
// database so that the guard of commitments answers by itself, with a timer
// that has fired in the same transaction.
func TestExpiryStampsClosedAtAndIsOneEffect(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	toggleTrigger(t, e.admin, "timers", "timers_expire_commitment", false)
	toggleTrigger(t, e.admin, "timers", "timers_coherence_check", false)
	toggleTrigger(t, e.admin, "commitments", "timers_coherence_check", false)
	for _, w := range e.writers() {
		id := e.commitPast(t, ledgerSpaceA, "100.00")
		fire := fireTimerStmt(id)
		requireRule(t, w.name+": expiry and a reason", runTx(t, w.pool, w.space, fire, updateOf(id, `status = 'expired', canceled_reason = 'x'`)), "23001", "")
		requireRule(t, w.name+": expiry and a forged closing time", runTx(t, e.admin, "", fire, updateOf(id, `status = 'expired', closed_at = '2000-01-01T00:00:00Z'`)), "23001", "")
		requireRule(t, w.name+": expiry and a moved due date", runTx(t, w.pool, w.space, fire, updateOf(id, `status = 'expired', due_at = now() + interval '1 day'`)), "23001", "")
		before := time.Now().Add(-time.Minute)
		if err := runTx(t, w.pool, w.space, fire, updateOf(id, `status = 'expired'`)); err != nil {
			t.Fatalf("%s: expire: %v", w.name, err)
		}
		var closedAt time.Time
		if err := e.admin.QueryRow(`SELECT closed_at FROM kernel.commitments WHERE id = $1`, id).Scan(&closedAt); err != nil || closedAt.Before(before) {
			t.Errorf("%s: closed_at = %v (%v), want the transaction time", w.name, closedAt, err)
		}
		if status, _, _ := commitmentState(t, e.admin, id); status != "expired" {
			t.Errorf("%s: status = %s, want expired", w.name, status)
		}
	}
}

// TestCommitmentOfOtherSpaceReferencesRejected guards the composite keys: a
// debtor, creditor, actor or resource of another space is not a reference.
func TestCommitmentOfOtherSpaceReferencesRejected(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)

	for name, tc := range map[string]struct {
		stmt       stmt
		constraint string
	}{
		"debtor of space B":   {commitmentOf(newID(t), ledgerSpaceA, cmDebtorB, ledgerParticipantA, ledgerResourceA, "10.00"), "commitments_debtor_fkey"},
		"creditor of space B": {commitmentOf(newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantB, ledgerResourceA, "10.00"), "commitments_creditor_fkey"},
		"resource of space B": {commitmentOf(newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantA, ledgerResourceB, "10.00"), "commitments_resource_fkey"},
		"actor of space B": {stmt{`INSERT INTO kernel.commitments (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id,
			amount, due_at, status, created_by_participant_id)
			VALUES ($1, $2, 'test.commitment', $3, $4, $5, 10, now() + interval '1 day', 'open', $6)`,
			[]any{newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantA, ledgerResourceA, ledgerParticipantB}}, "commitments_created_by_fkey"},
	} {
		requireRule(t, "API: "+name, runTx(t, e.api, ledgerSpaceA, tc.stmt), "23503", tc.constraint)
		requireRule(t, "owner: "+name, runTx(t, e.admin, "", tc.stmt), "23503", tc.constraint)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments`); got != 0 {
		t.Errorf("%d commitments, want 0", got)
	}
}

// TestFulfillmentCreatedAtIsStampedByTheKernel guards that the creation time of
// a fulfillment is the transaction time: the API role lacks the privilege and
// the owner gets the time of the transaction whatever it writes.
func TestFulfillmentCreatedAtIsStampedByTheKernel(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "100.00")
	entry := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "40.00")

	forged := stmt{`INSERT INTO kernel.fulfillments (id, space_id, commitment_id, entry_id, resource_id, amount, created_at)
		VALUES ($1, $2, $3, $4, $5, 40, '2000-01-01T00:00:00Z')`, []any{newID(t), ledgerSpaceA, id, entry, ledgerResourceA}}
	requireRule(t, "API insert with created_at", runTx(t, e.api, ledgerSpaceA, forged), "42501", "")
	if err := runTx(t, e.admin, "", forged); err != nil {
		t.Fatalf("owner insert with created_at: %v", err)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.fulfillments WHERE created_at > now() - interval '1 minute'`); got != 1 {
		t.Error("the owner forged the creation time of a fulfillment")
	}
}

// TestFulfillmentDeferredSumCheck guards the second layer of the fulfilled
// amount: with the immediate guards disabled, a commit that leaves it different
// from the sum of the fulfillments fails.
func TestFulfillmentDeferredSumCheck(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "100.00")
	entry := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "40.00")

	if _, err := e.admin.Exec(`ALTER TABLE kernel.commitments DISABLE TRIGGER commitments_guard_update`); err != nil {
		t.Fatalf("disable guard: %v", err)
	}
	requireRule(t, "a value that is not the sum", runTx(t, e.admin, "", updateOf(id, `fulfilled_amount = 10`)), "23000", "commitments_fulfilled_sum")
	if _, err := e.admin.Exec(`ALTER TABLE kernel.commitments ENABLE TRIGGER commitments_guard_update`); err != nil {
		t.Fatalf("enable guard: %v", err)
	}

	if _, err := e.admin.Exec(`ALTER TABLE kernel.fulfillments DISABLE TRIGGER fulfillments_apply`); err != nil {
		t.Fatalf("disable apply: %v", err)
	}
	requireRule(t, "a fulfillment that the commitment does not reflect",
		e.fulfill(t, id, entry, "40.00"), "23000", "commitments_fulfilled_sum")
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.fulfillments`); got != 0 {
		t.Errorf("%d fulfillments after the failed commit, want 0", got)
	}
	if status, fulfilled, _ := commitmentState(t, e.admin, id); status != "open" || fulfilled != "0" {
		t.Errorf("commitment = %s %s, want open 0", status, fulfilled)
	}
}

// TestCommitmentSpaceSwitchBeforeCommitFailsClosed guards the attack of changing
// kernel.space_id before the commit so that row level security hides the
// commitment from the deferred check: the check then sees nothing and fails.
func TestCommitmentSpaceSwitchBeforeCommitFailsClosed(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	existing := e.commit(t, ledgerSpaceA, "100.00")
	entry := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "40.00")

	for name, switchTo := range map[string]string{
		"space B": ledgerSpaceB, "no space": "", "nil uuid": "00000000-0000-0000-0000-000000000000",
	} {
		for scenario, stmts := range map[string][]stmt{
			"new commitment": {commitmentStmt(newID(t), ledgerSpaceA, "10.00")},
			"fulfillment":    {fulfillStmt(t, existing, entry, ledgerSpaceA, ledgerResourceA, "40.00")},
		} {
			tx, err := e.api.Begin()
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
				t.Fatalf("set space: %v", err)
			}
			for _, s := range stmts {
				if _, err := tx.Exec(s.query, s.args...); err != nil {
					t.Fatalf("%s/%s: insert: %v", scenario, name, err)
				}
			}
			if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, switchTo); err != nil {
				t.Fatalf("switch space: %v", err)
			}
			requireRule(t, scenario+" then "+name, tx.Commit(), "23000", "commitments_fulfilled_sum")
		}
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments`); got != 1 {
		t.Errorf("%d commitments, want only the committed one", got)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.fulfillments`); got != 0 {
		t.Errorf("%d fulfillments, want 0", got)
	}
}

// TestCommitmentOnConflictCannotBypassGuards guards INSERT ... ON CONFLICT: DO
// UPDATE goes through the same update guard and privileges, and DO NOTHING
// writes nothing.
func TestCommitmentOnConflictCannotBypassGuards(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "100.00")
	entry := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "40.00")
	if err := e.fulfill(t, id, entry, "40.00"); err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	closed := e.closeCommitments(t)

	again := func(target, conflict string) stmt {
		s := commitmentStmt(target, ledgerSpaceA, "100.00")
		s.query += " ON CONFLICT (id) " + conflict
		return s
	}
	for name, tc := range map[string]struct{ conflict, state string }{
		"fulfilled amount to the full amount": {`DO UPDATE SET fulfilled_amount = EXCLUDED.amount`, "23001"},
		"fulfilled amount to a wrong sum":     {`DO UPDATE SET fulfilled_amount = 41`, "23001"},
		"status":                              {`DO UPDATE SET status = 'fulfilled'`, "23001"},
		"amount":                              {`DO UPDATE SET amount = 1`, "42501"},
		"closing time":                        {`DO UPDATE SET closed_at = now()`, "42501"},
	} {
		requireRule(t, name, runTx(t, e.api, ledgerSpaceA, again(id, tc.conflict)), tc.state, "")
	}
	requireRule(t, "reason on a closed one", runTx(t, e.api, ledgerSpaceA, again(closed["fulfilled"], `DO UPDATE SET canceled_reason = 'x'`)), "23001", "")
	requireRule(t, "fulfillment amount", runTx(t, e.api, ledgerSpaceA, stmt{
		`INSERT INTO kernel.fulfillments (id, space_id, commitment_id, entry_id, resource_id, amount)
		 VALUES ($1, $2, $3, $4, $5, 40) ON CONFLICT (entry_id) DO UPDATE SET amount = 1`,
		[]any{newID(t), ledgerSpaceA, id, entry, ledgerResourceA}}), "42501", "")
	if err := runTx(t, e.api, ledgerSpaceA, again(id, `DO NOTHING`)); err != nil {
		t.Errorf("DO NOTHING on an existing commitment: %v", err)
	}
	if err := runTx(t, e.api, ledgerSpaceA, stmt{
		`INSERT INTO kernel.fulfillments (id, space_id, commitment_id, entry_id, resource_id, amount)
		 VALUES ($1, $2, $3, $4, $5, 40) ON CONFLICT DO NOTHING`,
		[]any{newID(t), ledgerSpaceA, id, entry, ledgerResourceA}}); err != nil {
		t.Errorf("DO NOTHING on an entry already applied: %v", err)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.fulfillments WHERE entry_id = $1`, entry); got != 1 {
		t.Errorf("the entry is applied %d times, want 1", got)
	}
	if status, fulfilled, _ := commitmentState(t, e.admin, id); status != "open" || fulfilled != "40.00" {
		t.Errorf("commitment = %s %s, want open 40.00", status, fulfilled)
	}
}

// TestCommitmentCopyFromStillFiresTriggers guards COPY. Row level security
// refuses COPY FROM for the API role; for the administrator COPY runs the same
// triggers as INSERT.
func TestCommitmentCopyFromStillFiresTriggers(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	ctx := context.Background()
	commitment := e.commit(t, ledgerSpaceA, "100.00")
	entry := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "40.00")
	due := time.Now().Add(24 * time.Hour)

	columns := []string{"id", "space_id", "kind", "debtor_participant_id", "creditor_participant_id", "resource_id",
		"amount", "due_at", "status", "created_by_participant_id", "fulfilled_amount", "closed_at"}
	row := func(status string, fulfilled int64, closed any) []any {
		return []any{newID(t), ledgerSpaceA, "test.commitment", cmDebtorA, ledgerParticipantA, ledgerResourceA,
			decimal(10000, -2), due, status, ledgerParticipantA, decimal(fulfilled, 0), closed}
	}
	copyRows := func(conn *pgx.Conn, table string, cols []string, rows [][]any) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"kernel", table}, cols, pgx.CopyFromRows(rows))
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}
	administrator := pgxConn(t, e.admin, false)
	api := pgxConn(t, e.admin, true)

	requireRule(t, "COPY of a fulfilled commitment", copyRows(administrator, "commitments", columns, [][]any{row("fulfilled", 100, time.Now())}), "23001", "")
	requireRule(t, "COPY with a fulfilled amount", copyRows(administrator, "commitments", columns, [][]any{row("open", 5, nil)}), "23001", "")
	requireRule(t, "COPY with a closing time", copyRows(administrator, "commitments", columns, [][]any{row("open", 0, time.Now())}), "23001", "")
	// The API role is refused by privilege when it names a column it cannot write,
	// and by row level security, which does not support COPY FROM, when it does not.
	requireRule(t, "API COPY with a fulfilled amount", copyRows(api, "commitments", columns, [][]any{row("open", 0, nil)}), "42501", "")
	requireRule(t, "API COPY of commitments", copyRows(api, "commitments", columns[:10], [][]any{row("open", 0, nil)[:10]}), "0A000", "")
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments`); got != 1 {
		t.Errorf("%d commitments after the refused copies, want 1", got)
	}
	if err := copyRows(administrator, "commitments", columns[:10], [][]any{row("open", 0, nil)[:10]}); err != nil {
		t.Errorf("COPY of a valid open commitment: %v", err)
	}

	fulfillmentColumns := []string{"id", "space_id", "commitment_id", "entry_id", "resource_id", "amount"}
	bad := [][]any{{newID(t), ledgerSpaceA, commitment, entry, ledgerResourceA, decimal(3900, -2)}}
	requireRule(t, "COPY of a fulfillment that does not apply the entry in full", copyRows(administrator, "fulfillments", fulfillmentColumns, bad), "23000", "fulfillments_amount_entry")
	requireRule(t, "API COPY of fulfillments", copyRows(api, "fulfillments", fulfillmentColumns, bad), "0A000", "")
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.fulfillments`); got != 0 {
		t.Errorf("%d fulfillments after the refused copies, want 0", got)
	}
	good := [][]any{{newID(t), ledgerSpaceA, commitment, entry, ledgerResourceA, decimal(4000, -2)}}
	if err := copyRows(administrator, "fulfillments", fulfillmentColumns, good); err != nil {
		t.Errorf("COPY of a valid fulfillment: %v", err)
	}
	if status, fulfilled, _ := commitmentState(t, e.admin, commitment); status != "open" || fulfilled != "40.00" {
		t.Errorf("commitment after a copied fulfillment = %s %s, want open 40.00", status, fulfilled)
	}
}

// TestSessionReplicationRoleDeniedToAPI guards the attacks that need more than
// the privileges of the API role: replication mode, DDL, trigger switches, table
// locks and system columns.
func TestSessionReplicationRoleDeniedToAPI(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)

	for _, query := range []string{
		`SET session_replication_role = replica`,
		`ALTER TABLE kernel.commitments DISABLE TRIGGER ALL`,
		`ALTER TABLE kernel.commitments DISABLE TRIGGER commitments_guard_update`,
		`ALTER TABLE kernel.fulfillments DISABLE TRIGGER fulfillments_guard_insert`,
		`ALTER TABLE kernel.commitments DISABLE TRIGGER commitments_fulfilled_sum_check`,
		`DROP TRIGGER fulfillments_no_row_change ON kernel.fulfillments`,
		`DROP TRIGGER commitments_no_truncate ON kernel.commitments`,
		`CREATE TRIGGER rogue BEFORE INSERT ON kernel.commitments FOR EACH ROW EXECUTE FUNCTION kernel.stamp_exchange()`,
		`ALTER TABLE kernel.commitments DISABLE ROW LEVEL SECURITY`,
		`ALTER TABLE kernel.fulfillments NO FORCE ROW LEVEL SECURITY`,
		`ALTER TABLE kernel.commitments DROP CONSTRAINT commitments_status_check`,
		`ALTER TABLE kernel.commitments ADD COLUMN rogue int`,
		`ALTER TABLE kernel.commitments OWNER TO nexusthrive_api`,
		`ALTER FUNCTION kernel.guard_commitment_update() SECURITY DEFINER`,
		`CREATE OR REPLACE FUNCTION kernel.guard_commitment_update() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
		`CREATE OR REPLACE FUNCTION kernel.check_commitment_fulfilled() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NULL; END'`,
		`DROP TABLE kernel.fulfillments`,
		`INSERT INTO kernel.commitments (id, created_at) OVERRIDING SYSTEM VALUE VALUES ('01900000-0000-7000-8000-0000000001f1', now())`,
	} {
		requireRule(t, query, asAPI(t, e.admin, query), "42501", "")
	}
	// SET ROLE needs another role to belong to, and the test harness runs as a
	// superuser that can always switch back, so the property is checked in the
	// catalog: the API role belongs to no role and has no special attribute.
	if got := count(t, e.admin, `SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = $1`, db.APIRole); got != 0 {
		t.Errorf("the API role is a member of %d roles, want none", got)
	}
	for _, query := range []string{
		`LOCK TABLE kernel.commitments IN SHARE MODE`, `LOCK TABLE kernel.commitments IN ACCESS EXCLUSIVE MODE`,
		`LOCK TABLE kernel.fulfillments IN SHARE MODE`,
	} {
		requireRule(t, query, runTx(t, e.api, "", stmt{query, nil}), "42501", "")
	}
	if got := count(t, e.admin, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'kernel'
		AND table_name IN ('commitments', 'fulfillments') AND (is_identity = 'YES' OR is_generated <> 'NEVER')`); got != 0 {
		t.Errorf("%d identity or generated columns in the commitment tables, want none", got)
	}
}

// commitmentFunctions are the functions the commitment migration adds or replaces.
var commitmentFunctions = []string{
	"guard_commitment_insert", "guard_commitment_update", "guard_fulfillment_insert", "apply_fulfillment",
	"check_commitment_fulfilled", "reject_commitment_removal", "guard_resource_change",
}

// TestCommitmentFunctionsPinSearchPathAndNotSecurityDefiner guards the attack
// surface of privilege escalation and of a function planted in a hostile search
// path.
func TestCommitmentFunctionsPinSearchPathAndNotSecurityDefiner(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	if got := count(t, admin, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'kernel' AND p.prosecdef`); got != 0 {
		t.Errorf("%d kernel functions are SECURITY DEFINER, want none", got)
	}
	for _, name := range commitmentFunctions {
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

// TestCommitmentGuardsIgnoreShadowing proves that functions planted in the
// temporary schema, which comes first in a hostile search path, replace neither
// round() nor sum() inside the guards.
func TestCommitmentGuardsIgnoreShadowing(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	commitment := e.commit(t, ledgerSpaceA, "100.00")
	entry := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "40.00")

	tx, err := e.admin.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`CREATE FUNCTION pg_temp.round(numeric, integer) RETURNS numeric LANGUAGE sql AS 'SELECT 0::numeric'`,
		`CREATE FUNCTION pg_temp.sum(numeric) RETURNS numeric LANGUAGE sql AS 'SELECT 0::numeric'`,
		`SET LOCAL search_path = pg_temp, public, pg_catalog`,
	} {
		if _, err := tx.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	s := fulfillStmt(t, commitment, entry, ledgerSpaceA, ledgerResourceA, "40.00")
	if _, err := tx.Exec(s.query, s.args...); err != nil {
		t.Fatalf("a planted function must not be used: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, fulfilled, _ := commitmentState(t, e.admin, commitment); fulfilled != "40.00" {
		t.Errorf("fulfilled amount = %s, want 40.00", fulfilled)
	}
}

// TestResourceScaleAndKindFrozenOnceCommitmentsExist guards the replaced
// guard_resource_change: not even the administrator may change scale or kind of
// a resource that has commitments, whatever the current space.
func TestResourceScaleAndKindFrozenOnceCommitmentsExist(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	if err := runTx(t, e.admin, "", commitmentOf(newID(t), ledgerSpaceA, cmDebtorA, ledgerParticipantA, ledgerResourceA2, "5")); err != nil {
		t.Fatalf("commitment on the unit resource: %v", err)
	}

	for name, tc := range map[string]struct{ space, query string }{
		"scale":                  {ledgerSpaceA, `UPDATE kernel.resources SET scale = 1 WHERE id = $1`},
		"kind":                   {ledgerSpaceA, `UPDATE kernel.resources SET kind = 'time' WHERE id = $1`},
		"scale in another space": {ledgerSpaceB, `UPDATE kernel.resources SET scale = 1 WHERE id = $1`},
		"kind with no space":     {"", `UPDATE kernel.resources SET kind = 'time' WHERE id = $1`},
	} {
		requireRule(t, name, runTx(t, e.admin, tc.space, stmt{tc.query, []any{ledgerResourceA2}}), "23001", "")
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.resources WHERE id = $1 AND scale = 0 AND kind = 'unit'`, ledgerResourceA2); got != 1 {
		t.Error("the resource with commitments changed")
	}
	if err := runTx(t, e.admin, "", stmt{`UPDATE kernel.resources SET name = 'Unit renamed', scale = scale WHERE id = $1`, []any{ledgerResourceA2}}); err != nil {
		t.Errorf("a rename with the same scale must pass: %v", err)
	}
	// A resource with neither entries nor commitments stays editable from its own space.
	if err := runTx(t, e.admin, ledgerSpaceA, stmt{`UPDATE kernel.resources SET scale = 3 WHERE id = $1`, []any{ledgerResourceA}}); err != nil {
		t.Errorf("a resource without commitments must be editable: %v", err)
	}
}

// fulfillmentSeed leaves one commitment with one partial fulfillment.
func (e commitmentEnv) fulfillmentSeed(t *testing.T) (commitment, entry string) {
	t.Helper()
	commitment = e.commit(t, ledgerSpaceA, "100.00")
	entry = e.creditEntry(t, "k-seed", exAccountA2, exAccountA3, ledgerResourceA, "40.00")
	if err := e.fulfill(t, commitment, entry, "40.00"); err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	return commitment, entry
}

// TestFulfillmentsAreAppendOnly guards the history of fulfillments: the API role
// lacks the privilege and the triggers stop the owner, TRUNCATE CASCADE from a
// parent table included, also with the privileges granted by mistake.
func TestFulfillmentsAreAppendOnly(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	e.fulfillmentSeed(t)

	for _, query := range []string{
		`UPDATE kernel.fulfillments SET amount = 1`, `DELETE FROM kernel.fulfillments`, `TRUNCATE kernel.fulfillments`,
	} {
		requireRule(t, "API: "+query, asAPI(t, e.admin, query), "42501", "")
	}
	for _, query := range []string{
		`UPDATE kernel.fulfillments SET amount = 1`, `UPDATE kernel.fulfillments SET commitment_id = commitment_id`,
		`DELETE FROM kernel.fulfillments`, `TRUNCATE kernel.fulfillments`, `TRUNCATE kernel.fulfillments CASCADE`,
		`TRUNCATE kernel.commitments, kernel.fulfillments, kernel.timers`, `TRUNCATE kernel.commitments CASCADE`,
		`TRUNCATE kernel.entries CASCADE`, `TRUNCATE kernel.exchanges CASCADE`, `TRUNCATE kernel.accounts CASCADE`,
		`TRUNCATE kernel.resources CASCADE`, `TRUNCATE kernel.participants CASCADE`, `TRUNCATE kernel.spaces CASCADE`,
	} {
		_, err := e.admin.Exec(query)
		requireRule(t, "administrator: "+query, err, "23001", "")
	}
	// Alone, a parent table is protected by PostgreSQL itself, because the
	// fulfillments reference it; the statements above cover the ways around that.
	for _, query := range []string{`TRUNCATE kernel.commitments`, `TRUNCATE kernel.entries`} {
		_, err := e.admin.Exec(query)
		requireRule(t, "administrator: "+query+" alone", err, "0A000", "")
	}

	for _, query := range []string{
		`GRANT UPDATE, DELETE, TRUNCATE ON kernel.fulfillments TO nexusthrive_api`,
		`CREATE POLICY mistaken_update ON kernel.fulfillments FOR UPDATE USING (true) WITH CHECK (true)`,
		`CREATE POLICY mistaken_delete ON kernel.fulfillments FOR DELETE USING (true)`,
	} {
		if _, err := e.admin.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, query := range []string{
		`UPDATE kernel.fulfillments SET amount = 1`, `DELETE FROM kernel.fulfillments`, `TRUNCATE kernel.fulfillments`,
	} {
		requireRule(t, "API after a mistaken grant: "+query, runTx(t, e.api, ledgerSpaceA, stmt{query, nil}), "23001", "")
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.fulfillments WHERE amount = 40`); got != 1 {
		t.Errorf("the fulfillments changed: %d rows with the original amount, want 1", got)
	}
}

// TestCommitmentsCannotBeDeleted guards that no writer deletes a commitment,
// also with the privileges granted by mistake and without the foreign key that
// makes PostgreSQL refuse a lone TRUNCATE on its own.
func TestCommitmentsCannotBeDeleted(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	e.commit(t, ledgerSpaceA, "100.00")

	for _, query := range []string{`DELETE FROM kernel.commitments`, `TRUNCATE kernel.commitments`} {
		requireRule(t, "API: "+query, asAPI(t, e.admin, query), "42501", "")
	}
	for _, query := range []string{`DELETE FROM kernel.commitments`, `TRUNCATE kernel.commitments, kernel.fulfillments, kernel.timers`} {
		_, err := e.admin.Exec(query)
		requireRule(t, "administrator: "+query, err, "23001", "")
	}
	_, err := e.admin.Exec(`TRUNCATE kernel.commitments`)
	requireRule(t, "administrator: TRUNCATE kernel.commitments alone", err, "0A000", "")

	if _, err := e.admin.Exec(`ALTER TABLE kernel.fulfillments DROP CONSTRAINT fulfillments_commitment_fkey`); err != nil {
		t.Fatalf("drop foreign key: %v", err)
	}
	if _, err := e.admin.Exec(`ALTER TABLE kernel.timers DROP CONSTRAINT timers_commitment_fkey`); err != nil {
		t.Fatalf("drop foreign key: %v", err)
	}
	_, err = e.admin.Exec(`TRUNCATE kernel.commitments`)
	requireRule(t, "administrator without the foreign keys: TRUNCATE", err, "23001", "")
	for _, query := range []string{
		`GRANT DELETE, TRUNCATE ON kernel.commitments TO nexusthrive_api`,
		`CREATE POLICY mistaken_delete ON kernel.commitments FOR DELETE USING (true)`,
	} {
		if _, err := e.admin.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, query := range []string{`DELETE FROM kernel.commitments`, `TRUNCATE kernel.commitments`} {
		requireRule(t, "API after a mistaken grant: "+query, runTx(t, e.api, ledgerSpaceA, stmt{query, nil}), "23001", "")
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments`); got != 1 {
		t.Errorf("%d commitments, want 1", got)
	}
}

// TestFulfillWaitsForRowLock guards the concurrency rule: a second fulfillment
// of the same commitment waits for the first one instead of reading a stale
// pending amount, and is then refused as exceeding it.
func TestFulfillWaitsForRowLock(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "100.00")
	first := e.creditEntry(t, "k1", exAccountA2, exAccountA3, ledgerResourceA, "60.00")
	second := e.creditEntry(t, "k2", exAccountA2, exAccountA3, ledgerResourceA, "60.00")

	holding, err := e.api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer holding.Rollback()
	if _, err := holding.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	s := fulfillStmt(t, id, first, ledgerSpaceA, ledgerResourceA, "60.00")
	if _, err := holding.Exec(s.query, s.args...); err != nil {
		t.Fatalf("first fulfillment: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- e.fulfill(t, id, second, "60.00") }()
	waitForLockWait(t, e.admin, "INSERT INTO kernel.fulfillments")
	select {
	case err := <-done:
		t.Fatalf("the second fulfillment did not wait for the first: %v", err)
	default:
	}
	if err := holding.Commit(); err != nil {
		t.Fatalf("commit first: %v", err)
	}
	requireRule(t, "second fulfillment after the first committed", <-done, "23000", "fulfillments_within_pending")
	if status, fulfilled, _ := commitmentState(t, e.admin, id); status != "open" || fulfilled != "60.00" {
		t.Errorf("commitment = %s %s, want open 60.00", status, fulfilled)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.fulfillments`); got != 1 {
		t.Errorf("%d fulfillments, want 1", got)
	}
}
