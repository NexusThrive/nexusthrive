package db_test

import (
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// timerColumns lists every column of kernel.timers; the grants must cover
// exactly these.
var timerColumns = []string{"id", "space_id", "commitment_id", "fire_at", "status", "fired_at", "created_at"}

// timerInsertColumns and timerUpdateColumns are the only columns the API role
// may write: the creation trigger runs with its rights, and the guards accept
// nothing but the derived values.
var (
	timerInsertColumns = []string{"id", "space_id", "commitment_id", "fire_at"}
	timerUpdateColumns = []string{"status", "fire_at"}
)

// writer is a role that writes the kernel tables: the API role inside a space
// or the administrator with none.
type writer struct {
	name  string
	pool  *sql.DB
	space string
}

// writers returns both writers of the environment. The guards must hold for
// each of them.
func (e commitmentEnv) writers() []writer {
	return []writer{{"API", e.api, ledgerSpaceA}, {"administrator", e.admin, ""}}
}

// pastCommitmentStmt inserts an open commitment that started two days ago and
// was due a day ago: the only way to test expiry, because the injected clock
// may run behind the clock of the database but never ahead of it.
func pastCommitmentStmt(id, space, amount string) stmt {
	debtor, creditor, resource := partiesOf(space)
	return stmt{
		`INSERT INTO kernel.commitments (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id,
		   amount, starts_at, due_at, status, created_by_participant_id)
		 VALUES ($1, $2, 'test.commitment', $3, $4, $5, $6::numeric, now() - interval '2 days', now() - interval '1 day', 'open', $4)`,
		[]any{id, space, debtor, creditor, resource, amount},
	}
}

// commitPast commits a commitment that is already due, as the API role.
func (e commitmentEnv) commitPast(t *testing.T, space, amount string) string {
	t.Helper()
	id := newID(t)
	if err := runTx(t, e.api, space, pastCommitmentStmt(id, space, amount)); err != nil {
		t.Fatalf("insert past commitment: %v", err)
	}
	return id
}

// fireTimerStmt fires the timer of a commitment the way a writer would by SQL.
func fireTimerStmt(commitment string) stmt {
	return stmt{`UPDATE kernel.timers SET status = 'fired' WHERE commitment_id = $1`, []any{commitment}}
}

// expiryEventStmt writes the event that the coherence check demands for an
// expired commitment, with the type, version and subject the kernel uses.
func expiryEventStmt(t *testing.T, commitment, space string) stmt {
	t.Helper()
	return stmt{
		`INSERT INTO kernel.events (id, space_id, type, version, subject_ref, payload, actor_kind)
		 VALUES ($1, $2, 'kernel.commitment.expired', 1, 'kernel.commitment/' || $3::text, '{}'::jsonb, 'system')`,
		[]any{newID(t), space, commitment},
	}
}

// expireCommitment creates a commitment that is already due in space and
// expires it the way the kernel does: it fires the timer and writes the event
// in one transaction. It returns the id of the expired commitment.
func (e commitmentEnv) expireCommitment(t *testing.T, space, amount string) string {
	t.Helper()
	id := e.commitPast(t, space, amount)
	if err := runTx(t, e.api, space, fireTimerStmt(id), expiryEventStmt(t, id, space)); err != nil {
		t.Fatalf("expire commitment: %v", err)
	}
	if status, _, closed := commitmentState(t, e.admin, id); status != "expired" || !closed {
		t.Fatalf("commitment = %s closed %v, want expired and closed", status, closed)
	}
	return id
}

// timerOf reads the status of the timer of a commitment and whether its fire
// time is the due time of the commitment.
func timerOf(t *testing.T, admin *sql.DB, commitment string) (status string, dueMatches bool) {
	t.Helper()
	err := admin.QueryRow(`SELECT t.status, t.fire_at = c.due_at FROM kernel.timers t
		JOIN kernel.commitments c ON c.id = t.commitment_id WHERE t.commitment_id = $1`, commitment).Scan(&status, &dueMatches)
	if err != nil {
		t.Fatalf("read timer of %s: %v", commitment, err)
	}
	return status, dueMatches
}

// requireTimer fails unless the commitment has exactly one timer in the status
// and, for a scheduled one, due at the time of the commitment.
func requireTimer(t *testing.T, admin *sql.DB, commitment, status string) {
	t.Helper()
	if got := count(t, admin, `SELECT count(*) FROM kernel.timers WHERE commitment_id = $1`, commitment); got != 1 {
		t.Errorf("commitment %s has %d timers, want 1", commitment, got)
		return
	}
	got, dueMatches := timerOf(t, admin, commitment)
	if got != status {
		t.Errorf("timer of %s is %s, want %s", commitment, got, status)
	}
	if status == "scheduled" && !dueMatches {
		t.Errorf("timer of %s does not fire at the due time", commitment)
	}
}

// TestInsertingCommitmentCreatesTimerForAnyWriter guards criterion 1: whoever
// writes the commitment, the database creates its scheduled timer with the id
// of the commitment, the same space and the due time.
func TestInsertingCommitmentCreatesTimerForAnyWriter(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	for _, w := range e.writers() {
		id := newID(t)
		if err := runTx(t, w.pool, w.space, commitmentStmt(id, ledgerSpaceA, "10.00")); err != nil {
			t.Fatalf("%s insert: %v", w.name, err)
		}
		requireTimer(t, e.admin, id, "scheduled")
		var timerID, space string
		var createdAt time.Time
		if err := e.admin.QueryRow(`SELECT id::text, space_id::text, created_at FROM kernel.timers WHERE commitment_id = $1`, id).
			Scan(&timerID, &space, &createdAt); err != nil {
			t.Fatalf("%s read timer: %v", w.name, err)
		}
		if timerID != id || space != ledgerSpaceA {
			t.Errorf("%s: timer = %s in %s, want %s in %s", w.name, timerID, space, id, ledgerSpaceA)
		}
		if time.Since(createdAt) > time.Minute {
			t.Errorf("%s: timer created_at = %v, want the transaction time", w.name, createdAt)
		}
	}
}

// TestRolledBackCommitmentLeavesNoTimer guards that the timer lives and dies
// with its commitment.
func TestRolledBackCommitmentLeavesNoTimer(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	tx, err := e.api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	s := commitmentStmt(newID(t), ledgerSpaceA, "10.00")
	if _, err := tx.Exec(s.query, s.args...); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.timers`); got != 0 {
		t.Errorf("%d timers after a rollback, want 0", got)
	}
}

// TestDueAtUpdateMovesTimerForAnyWriter guards criterion 2: moving the due
// date moves the single timer of the commitment, whoever writes it.
func TestDueAtUpdateMovesTimerForAnyWriter(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	for _, w := range e.writers() {
		id := e.commit(t, ledgerSpaceA, "10.00")
		var before time.Time
		if err := e.admin.QueryRow(`SELECT fire_at FROM kernel.timers WHERE commitment_id = $1`, id).Scan(&before); err != nil {
			t.Fatalf("read timer: %v", err)
		}
		if err := runTx(t, w.pool, w.space, updateOf(id, `due_at = due_at + interval '10 days'`)); err != nil {
			t.Fatalf("%s reschedule: %v", w.name, err)
		}
		requireTimer(t, e.admin, id, "scheduled")
		var after time.Time
		if err := e.admin.QueryRow(`SELECT fire_at FROM kernel.timers WHERE commitment_id = $1`, id).Scan(&after); err != nil {
			t.Fatalf("read timer: %v", err)
		}
		if got := after.Sub(before); got != 10*24*time.Hour {
			t.Errorf("%s: the timer moved by %v, want 240h", w.name, got)
		}
	}
}

// TestSecondScheduledTimerRejectedByIndex guards criterion 2: a commitment has
// one scheduled timer. The guard lets a second one in only when it is coherent
// (this one is), so the partial unique index is the last word, and it holds
// with the guard disabled too.
func TestSecondScheduledTimerRejectedByIndex(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "10.00")
	second := func() stmt {
		return stmt{`INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at)
			SELECT $1, space_id, id, due_at FROM kernel.commitments WHERE id = $2`, []any{newID(t), id}}
	}
	for _, w := range e.writers() {
		requireRule(t, w.name+" with the guard", runTx(t, w.pool, w.space, second()), "23505", "timers_one_scheduled_key")
	}
	if _, err := e.admin.Exec(`ALTER TABLE kernel.timers DISABLE TRIGGER timers_guard_insert`); err != nil {
		t.Fatalf("disable guard: %v", err)
	}
	for _, w := range e.writers() {
		requireRule(t, w.name+" without the guard", runTx(t, w.pool, w.space, second()), "23505", "timers_one_scheduled_key")
	}
	requireTimer(t, e.admin, id, "scheduled")
}

// TestClosingCommitmentCancelsTimerForAnyWriter guards criterion 3: a commitment
// that fulfills or cancels leaves no scheduled timer, whoever closes it.
func TestClosingCommitmentCancelsTimerForAnyWriter(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)

	fulfilled := e.commit(t, ledgerSpaceA, "10.00")
	entry := e.creditEntry(t, "k-close-timer", exAccountA2, exAccountA3, ledgerResourceA, "10.00")
	if err := e.fulfill(t, fulfilled, entry, "10.00"); err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	requireTimer(t, e.admin, fulfilled, "canceled")

	for _, w := range e.writers() {
		id := e.commit(t, ledgerSpaceA, "10.00")
		if err := runTx(t, w.pool, w.space, updateOf(id, `canceled_reason = 'asked to stop'`)); err != nil {
			t.Fatalf("%s cancel: %v", w.name, err)
		}
		requireTimer(t, e.admin, id, "canceled")
	}
}

// TestTimerCannotBeCanceledWhileCommitmentOpen guards criterion 3: nobody
// cancels a timer by itself, so an open commitment never loses its timer.
func TestTimerCannotBeCanceledWhileCommitmentOpen(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "10.00")
	for _, w := range e.writers() {
		requireRule(t, w.name+" cancels the timer", runTx(t, w.pool, w.space,
			stmt{`UPDATE kernel.timers SET status = 'canceled' WHERE commitment_id = $1`, []any{id}}), "23001", "")
	}
	requireTimer(t, e.admin, id, "scheduled")
}

// TestTimerOfOtherSpaceRejected guards the composite key: a timer lives in the
// space of its commitment, with the guard and without it.
func TestTimerOfOtherSpaceRejected(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.expireCommitment(t, ledgerSpaceA, "10.00")
	open := e.commit(t, ledgerSpaceA, "10.00")
	other := func(commitment string) stmt {
		return stmt{`INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at) VALUES ($1, $2, $3, now())`,
			[]any{newID(t), ledgerSpaceB, commitment}}
	}
	requireRule(t, "API writes into space B", runTx(t, e.api, ledgerSpaceA, other(open)), "23503", "timers_commitment_fkey")
	requireRule(t, "administrator, timer of space B for a commitment of A", runTx(t, e.admin, "", other(open)), "23503", "timers_commitment_fkey")
	requireRule(t, "administrator, expired commitment", runTx(t, e.admin, "", other(id)), "23503", "timers_commitment_fkey")
	if _, err := e.admin.Exec(`ALTER TABLE kernel.timers DISABLE TRIGGER timers_guard_insert`); err != nil {
		t.Fatalf("disable guard: %v", err)
	}
	requireRule(t, "administrator without the guard", runTx(t, e.admin, "", other(id)), "23503", "timers_commitment_fkey")
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.timers WHERE space_id = $1`, ledgerSpaceB); got != 0 {
		t.Errorf("%d timers in space B, want 0", got)
	}
}

// TestTimersIsolationBetweenSpaces guards that a space sees none of the timers
// of another and cannot change them.
func TestTimersIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commitPast(t, ledgerSpaceA, "10.00")
	e.commit(t, ledgerSpaceB, "10.00")

	tx, err := e.api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceB); err != nil {
		t.Fatalf("set space: %v", err)
	}
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM kernel.timers WHERE space_id = $1`, ledgerSpaceA).Scan(&n); err != nil || n != 0 {
		t.Errorf("space B sees %d timers of space A (%v), want 0", n, err)
	}
	result, err := tx.Exec(`UPDATE kernel.timers SET status = 'fired' WHERE commitment_id = $1`, id)
	if err != nil {
		t.Fatalf("update from space B: %v", err)
	}
	if rows, _ := result.RowsAffected(); rows != 0 {
		t.Errorf("space B changed %d timers of space A, want 0", rows)
	}
	_ = tx.Rollback()
	requireTimer(t, e.admin, id, "scheduled")
}

// TestTimersForcedRLSAndGrants guards forced row level security, only the
// planned policies and exact column grants, on timers and on the two columns of
// commitments that the cascade needs.
func TestTimersForcedRLSAndGrants(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	requireForcedRLS(t, admin, "timers")
	if got, want := policyCommands(t, admin, "timers"), []string{"INSERT", "SELECT", "UPDATE"}; !slices.Equal(got, want) {
		t.Errorf("timers policies = %v, want %v", got, want)
	}
	for _, privilege := range []string{"DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
		var ok bool
		if err := admin.QueryRow(`SELECT has_table_privilege($1, 'kernel.timers', $2)`, db.APIRole, privilege).Scan(&ok); err != nil {
			t.Fatalf("privilege query: %v", err)
		}
		if ok {
			t.Errorf("API role must not have %s on kernel.timers", privilege)
		}
	}
	if got, want := privilegeSet(t, admin, "timers"), expectPrivileges(timerColumns, timerInsertColumns, timerUpdateColumns); !slices.Equal(got, want) {
		t.Errorf("timers privileges = %v, want %v", got, want)
	}
	if got := columnSet(t, admin, "timers"); len(got) != len(timerColumns) {
		t.Errorf("timers has %d columns, the grants cover %d: update the grants with the table", len(got), len(timerColumns))
	}
	if got, want := privilegeSet(t, admin, "commitments"),
		expectPrivileges(commitmentColumns, commitmentInsertColumns, []string{"fulfilled_amount", "canceled_reason", "due_at", "status"}); !slices.Equal(got, want) {
		t.Errorf("commitments privileges = %v, want %v", got, want)
	}
	for column, want := range map[string]string{"status": "NO", "fired_at": "YES", "created_at": "NO", "fire_at": "NO"} {
		if got := columnSet(t, admin, "timers")[column]; got != want {
			t.Errorf("timers.%s nullable = %q, want %s", column, got, want)
		}
	}
}

// timerFunctions are the functions the timer migration adds or replaces.
var timerFunctions = []string{
	"timers_guard_insert", "timers_guard_update", "timers_expire_commitment", "commitments_timer_create",
	"commitments_timer_sync", "check_timer_coherence", "reject_timer_removal", "guard_commitment_update",
}

// TestTimerFunctionsPinSearchPathAndNotSecurityDefiner guards the attack
// surface of privilege escalation and of a function planted in a hostile
// search path, also for the replaced guard of commitments.
func TestTimerFunctionsPinSearchPathAndNotSecurityDefiner(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	if got := count(t, admin, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'kernel' AND p.prosecdef`); got != 0 {
		t.Errorf("%d kernel functions are SECURITY DEFINER, want none", got)
	}
	for _, name := range timerFunctions {
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
