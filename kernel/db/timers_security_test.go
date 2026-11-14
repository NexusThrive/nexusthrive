package db_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/db"
)

// toggleTrigger enables or disables one trigger of a table of the throwaway
// database, as the administrator.
func toggleTrigger(t *testing.T, admin *sql.DB, table, trigger string, enable bool) {
	t.Helper()
	action := "DISABLE"
	if enable {
		action = "ENABLE"
	}
	if _, err := admin.Exec(`ALTER TABLE kernel.` + table + ` ` + action + ` TRIGGER ` + trigger); err != nil {
		t.Fatalf("%s trigger %s: %v", action, trigger, err)
	}
}

// withoutTrigger runs fn with one trigger disabled and enables it again after.
func withoutTrigger(t *testing.T, admin *sql.DB, table, trigger string, fn func()) {
	t.Helper()
	toggleTrigger(t, admin, table, trigger, false)
	defer toggleTrigger(t, admin, table, trigger, true)
	fn()
}

// corruptFiredTimer leaves a due commitment open with a timer that fired in a
// transaction of its own: the state a guard must never mistake for a valid
// expiry. It disables the cascade and the commit checks only while it runs.
func (e commitmentEnv) corruptFiredTimer(t *testing.T) string {
	t.Helper()
	id := e.commitPast(t, ledgerSpaceA, "10.00")
	withoutTrigger(t, e.admin, "timers", "timers_expire_commitment", func() {
		withoutTrigger(t, e.admin, "timers", "timers_coherence_check", func() {
			withoutTrigger(t, e.admin, "commitments", "timers_coherence_check", func() {
				if err := runTx(t, e.admin, "", fireTimerStmt(id)); err != nil {
					t.Fatalf("fire without the cascade: %v", err)
				}
			})
		})
	})
	if status, _, _ := commitmentState(t, e.admin, id); status != "open" {
		t.Fatalf("commitment = %s, want open", status)
	}
	return id
}

// TestAPICannotExpireCommitmentDirectly guards the main attack: bringing a
// commitment to expired without the timer that proves it. Neither writer may do
// it with a timer still scheduled, with a due date in the future, with a timer
// that fired in another transaction, or with one inserted by hand.
func TestAPICannotExpireCommitmentDirectly(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	for _, w := range e.writers() {
		due := e.commitPast(t, ledgerSpaceA, "10.00")
		future := e.commit(t, ledgerSpaceA, "10.00")
		requireRule(t, w.name+": due commitment, timer scheduled", runTx(t, w.pool, w.space, updateOf(due, `status = 'expired'`)), "23001", "")
		requireRule(t, w.name+": due date in the future", runTx(t, w.pool, w.space, updateOf(future, `status = 'expired'`)), "23001", "")
		requireRule(t, w.name+": expired and a forged closing time", runTx(t, w.pool, w.space,
			updateOf(due, `status = 'expired', closed_at = now()`)), map[string]string{"API": "42501", "administrator": "23001"}[w.name], "")
		if status, _, _ := commitmentState(t, e.admin, due); status != "open" {
			t.Errorf("%s: commitment = %s, want open", w.name, status)
		}
	}

	// A timer that fired in another transaction proves nothing about this one.
	for _, w := range e.writers() {
		id := e.corruptFiredTimer(t)
		requireRule(t, w.name+": timer fired in another transaction", runTx(t, w.pool, w.space, updateOf(id, `status = 'expired'`)), "23001", "")
		if status, _, _ := commitmentState(t, e.admin, id); status != "open" {
			t.Errorf("%s: commitment = %s, want open", w.name, status)
		}
	}

	// A fired timer written by hand is refused: timers are born scheduled.
	for _, w := range e.writers() {
		id := e.commit(t, ledgerSpaceA, "10.00")
		forged := stmt{`INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at, status, fired_at)
			VALUES ($1, $2, $3, now() - interval '1 day', 'fired', now())`, []any{newID(t), ledgerSpaceA, id}}
		requireRule(t, w.name+": inserted fired timer", runTx(t, w.pool, w.space, forged, updateOf(id, `status = 'expired'`)),
			map[string]string{"API": "42501", "administrator": "23001"}[w.name], "")
	}
}

// TestAPICannotFireTimerBeforeDue guards that a timer fires only when its time
// has come, according to the clock of the database, and only as one effect.
func TestAPICannotFireTimerBeforeDue(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	future := e.commit(t, ledgerSpaceA, "10.00")
	for _, w := range e.writers() {
		for name, set := range map[string]string{
			"fire":                  `status = 'fired'`,
			"fire and move to past": `status = 'fired', fire_at = now() - interval '1 day'`,
			"move to the past":      `fire_at = now() - interval '1 day'`,
			"cancel and move":       `status = 'canceled', fire_at = now() + interval '1 day'`,
		} {
			requireRule(t, w.name+": "+name, runTx(t, w.pool, w.space,
				stmt{`UPDATE kernel.timers SET ` + set + ` WHERE commitment_id = $1`, []any{future}}), "23001", "")
		}
	}
	requireTimer(t, e.admin, future, "scheduled")
	if status, _, _ := commitmentState(t, e.admin, future); status != "open" {
		t.Errorf("commitment = %s, want open", status)
	}
}

// TestAPICannotDesyncTimerFromDueAt guards that the fire time of a scheduled
// timer is always the due time of its commitment.
func TestAPICannotDesyncTimerFromDueAt(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "10.00")
	for _, w := range e.writers() {
		for name, set := range map[string]string{
			"later":   `fire_at = fire_at + interval '1 day'`,
			"earlier": `fire_at = fire_at - interval '1 day'`,
			"now":     `fire_at = now()`,
		} {
			requireRule(t, w.name+": "+name, runTx(t, w.pool, w.space,
				stmt{`UPDATE kernel.timers SET ` + set + ` WHERE commitment_id = $1`, []any{id}}), "23001", "")
		}
	}
	requireTimer(t, e.admin, id, "scheduled")
}

// TestAPICannotForgeTimerColumns guards the columns the kernel stamps: the API
// role lacks the privilege to write them and the triggers stop the
// administrator.
func TestAPICannotForgeTimerColumns(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "10.00")
	insert := func(columns, values string) stmt {
		return stmt{`INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at` + columns + `)
			SELECT $1, space_id, id, due_at` + values + ` FROM kernel.commitments WHERE id = $2`, []any{newID(t), id}}
	}
	update := func(set string) stmt {
		return stmt{`UPDATE kernel.timers SET ` + set + ` WHERE commitment_id = $1`, []any{id}}
	}

	for name, s := range map[string]stmt{
		"status on insert":     insert(", status", ", 'scheduled'"),
		"fired status":         insert(", status, fired_at", ", 'fired', now()"),
		"fired_at on insert":   insert(", fired_at", ", now()"),
		"created_at on insert": insert(", created_at", ", '2000-01-01T00:00:00Z'"),
	} {
		requireRule(t, "API "+name, runTx(t, e.api, ledgerSpaceA, s), "42501", "")
	}
	for name, set := range map[string]string{
		"fired_at": `fired_at = now()`, "created_at": `created_at = now()`, "commitment_id": `commitment_id = '` + newID(t) + `'`,
		"space_id": `space_id = '` + ledgerSpaceB + `'`, "id": `id = '` + newID(t) + `'`,
	} {
		requireRule(t, "API update of "+name, runTx(t, e.api, ledgerSpaceA, update(set)), "42501", "")
		requireRule(t, "administrator update of "+name, runTx(t, e.admin, "", update(set)), "23001", "")
	}
	for name, s := range map[string]stmt{
		"fired status":       insert(", status, fired_at", ", 'fired', now()"),
		"fired without time": insert(", status", ", 'fired'"),
		"canceled status":    insert(", status", ", 'canceled'"),
		"fired_at on insert": insert(", fired_at", ", now()"),
	} {
		requireRule(t, "administrator "+name, runTx(t, e.admin, "", s), "23001", "")
	}
	requireTimer(t, e.admin, id, "scheduled")
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.timers`); got != 1 {
		t.Errorf("%d timers, want 1", got)
	}
}

// TestTimerCreatedAtIsStamped guards that the creation time of a timer is the
// transaction time even when the administrator writes another one by hand.
func TestTimerCreatedAtIsStamped(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	withoutTrigger(t, e.admin, "commitments", "commitments_timer_create", func() {
		withoutTrigger(t, e.admin, "commitments", "timers_coherence_check", func() {
			id := newID(t)
			err := runTx(t, e.admin, "", commitmentStmt(id, ledgerSpaceA, "10.00"), stmt{
				`INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at, created_at)
				 SELECT id, space_id, id, due_at, '2000-01-01T00:00:00Z' FROM kernel.commitments WHERE id = $1`, []any{id}})
			if err != nil {
				t.Fatalf("insert by hand: %v", err)
			}
		})
	})
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.timers WHERE created_at > now() - interval '1 minute'`); got != 1 {
		t.Error("the administrator forged the creation time of a timer")
	}
}

// TestTimersAreNeverDeleted guards that no writer deletes or truncates timers,
// not even through a cascade, with the privileges granted by mistake.
func TestTimersAreNeverDeleted(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	e.commit(t, ledgerSpaceA, "10.00")

	for _, query := range []string{`DELETE FROM kernel.timers`, `TRUNCATE kernel.timers`} {
		requireRule(t, "API: "+query, asAPI(t, e.admin, query), "42501", "")
	}
	for _, query := range []string{`DELETE FROM kernel.timers`, `TRUNCATE kernel.timers`, `TRUNCATE kernel.timers CASCADE`,
		`TRUNCATE kernel.commitments, kernel.timers, kernel.fulfillments`, `TRUNCATE kernel.commitments CASCADE`,
		`TRUNCATE kernel.participants CASCADE`, `TRUNCATE kernel.resources CASCADE`, `TRUNCATE kernel.spaces CASCADE`} {
		_, err := e.admin.Exec(query)
		requireRule(t, "administrator: "+query, err, "23001", "")
	}
	for _, query := range []string{
		`GRANT DELETE, TRUNCATE ON kernel.timers TO nexusthrive_api`,
		`CREATE POLICY mistaken_delete ON kernel.timers FOR DELETE USING (true)`,
	} {
		if _, err := e.admin.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, query := range []string{`DELETE FROM kernel.timers`, `TRUNCATE kernel.timers`} {
		requireRule(t, "API after a mistaken grant: "+query, runTx(t, e.api, ledgerSpaceA, stmt{query, nil}), "23001", "")
	}

	// A cascade from the parent tables reaches the timers: with the triggers of
	// the parents out of the way, the timers must be the ones that refuse.
	toggleTrigger(t, e.admin, "commitments", "commitments_no_truncate", false)
	toggleTrigger(t, e.admin, "fulfillments", "fulfillments_no_truncate", false)
	_, err := e.admin.Exec(`TRUNCATE kernel.commitments CASCADE`)
	if err == nil || !strings.Contains(err.Error(), "timers cannot be deleted") {
		t.Errorf("TRUNCATE kernel.commitments CASCADE = %v, want the timers to refuse it", err)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.timers`); got != 1 {
		t.Errorf("%d timers, want 1", got)
	}
}

// TestTerminalTimerIsFinal guards that a fired or canceled timer never changes
// again, for either writer.
func TestTerminalTimerIsFinal(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	fired := e.expireCommitment(t, ledgerSpaceA, "10.00")
	canceled := e.commit(t, ledgerSpaceA, "10.00")
	if err := runTx(t, e.api, ledgerSpaceA, updateOf(canceled, `canceled_reason = 'stop'`)); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	for state, id := range map[string]string{"fired": fired, "canceled": canceled} {
		for _, set := range []string{
			`status = 'scheduled'`, `status = 'fired'`, `status = 'canceled'`, `fire_at = now()`, `fire_at = fire_at`,
			`status = 'scheduled', fire_at = now() + interval '1 day'`,
		} {
			for _, w := range e.writers() {
				requireRule(t, w.name+" on a "+state+" timer: "+set, runTx(t, w.pool, w.space,
					stmt{`UPDATE kernel.timers SET ` + set + ` WHERE commitment_id = $1`, []any{id}}), "23001", "")
			}
		}
	}
	requireTimer(t, e.admin, fired, "fired")
	requireTimer(t, e.admin, canceled, "canceled")
}

// TestExpiryWithoutEventFailsAtCommit guards the law of memory for every
// writer: a commitment that expires without the event of its expiry, or with an
// event that does not match, fails when the transaction commits.
func TestExpiryWithoutEventFailsAtCommit(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	eventWith := func(space, typ string, version int, subject string) stmt {
		return stmt{`INSERT INTO kernel.events (id, space_id, type, version, subject_ref, payload, actor_kind)
			VALUES ($1, $2, $3, $4, $5, '{}'::jsonb, 'system')`, []any{newID(t), space, typ, version, subject}}
	}
	for _, w := range e.writers() {
		id := e.commitPast(t, ledgerSpaceA, "10.00")
		other := e.commitPast(t, ledgerSpaceA, "10.00")
		for name, events := range map[string][]stmt{
			"no event":           nil,
			"another subject":    {eventWith(ledgerSpaceA, "kernel.commitment.expired", 1, "kernel.commitment/"+other)},
			"another type":       {eventWith(ledgerSpaceA, "kernel.commitment.canceled", 1, "kernel.commitment/"+id)},
			"another version":    {eventWith(ledgerSpaceA, "kernel.commitment.expired", 2, "kernel.commitment/"+id)},
			"a subject prefixed": {eventWith(ledgerSpaceA, "kernel.commitment.expired", 1, "x.kernel.commitment/"+id)},
		} {
			requireRule(t, w.name+": "+name, runTx(t, w.pool, w.space, append([]stmt{fireTimerStmt(id)}, events...)...), "23000", "timers_coherent")
		}
		if status, _, _ := commitmentState(t, e.admin, id); status != "open" {
			t.Errorf("%s: commitment = %s after the failed commits, want open", w.name, status)
		}
		requireTimer(t, e.admin, id, "scheduled")
	}

	// An event of another space does not count. The API role cannot even write it.
	id := e.commitPast(t, ledgerSpaceA, "10.00")
	foreign := expiryEventStmt(t, id, ledgerSpaceB)
	requireRule(t, "API writes the event into space B", runTx(t, e.api, ledgerSpaceA, fireTimerStmt(id), foreign), "42501", "")
	requireRule(t, "administrator: event of space B", runTx(t, e.admin, "", fireTimerStmt(id), foreign), "23000", "timers_coherent")

	// The event may come before the firing.
	if err := runTx(t, e.admin, "", expiryEventStmt(t, id, ledgerSpaceA), fireTimerStmt(id)); err != nil {
		t.Errorf("event first, then the firing: %v", err)
	}
	if status, _, closed := commitmentState(t, e.admin, id); status != "expired" || !closed {
		t.Errorf("commitment = %s closed %v, want expired", status, closed)
	}
}

// TestOpenCommitmentWithoutTimerFailsAtCommit guards the second layer of the
// timer rules: with each trigger that keeps them out of the way, a commit that
// leaves timers and commitments incoherent fails, for either writer.
func TestOpenCommitmentWithoutTimerFailsAtCommit(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	for _, w := range e.writers() {
		withoutTrigger(t, e.admin, "commitments", "commitments_timer_create", func() {
			requireRule(t, w.name+": open commitment with no timer", runTx(t, w.pool, w.space, commitmentStmt(newID(t), ledgerSpaceA, "10.00")),
				"23000", "timers_coherent")
		})
		withoutTrigger(t, e.admin, "commitments", "commitments_timer_sync", func() {
			id := e.commit(t, ledgerSpaceA, "10.00")
			requireRule(t, w.name+": timer not at the due time", runTx(t, w.pool, w.space, updateOf(id, `due_at = due_at + interval '1 day'`)),
				"23000", "timers_coherent")
			requireRule(t, w.name+": closed commitment with a scheduled timer", runTx(t, w.pool, w.space, updateOf(id, `canceled_reason = 'stop'`)),
				"23000", "timers_coherent")
			requireTimer(t, e.admin, id, "scheduled")
		})
		withoutTrigger(t, e.admin, "timers", "timers_guard_update", func() {
			id := e.commit(t, ledgerSpaceA, "10.00")
			requireRule(t, w.name+": timer moved by itself", runTx(t, w.pool, w.space,
				stmt{`UPDATE kernel.timers SET fire_at = fire_at + interval '1 day' WHERE commitment_id = $1`, []any{id}}),
				"23000", "timers_coherent")
			requireTimer(t, e.admin, id, "scheduled")
		})
		withoutTrigger(t, e.admin, "timers", "timers_expire_commitment", func() {
			id := e.commitPast(t, ledgerSpaceA, "10.00")
			requireRule(t, w.name+": fired timer of an open commitment", runTx(t, w.pool, w.space, fireTimerStmt(id), expiryEventStmt(t, id, ledgerSpaceA)),
				"23000", "timers_coherent")
			requireTimer(t, e.admin, id, "scheduled")
		})
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments c WHERE NOT EXISTS (SELECT 1 FROM kernel.timers t WHERE t.commitment_id = c.id)`); got != 0 {
		t.Errorf("%d commitments with no timer, want 0", got)
	}
}

// TestExpiryRequiresDueAtPassedEvenWithoutTimerGuard guards the conditions of
// the commitment guard on their own: with the guard of timers out of the way a
// timer can claim to have fired, and the commitment still does not expire
// unless its own due date has passed and the timer fired in this transaction.
func TestExpiryRequiresDueAtPassedEvenWithoutTimerGuard(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	toggleTrigger(t, e.admin, "timers", "timers_guard_update", false)
	toggleTrigger(t, e.admin, "timers", "timers_expire_commitment", false)

	future := e.commit(t, ledgerSpaceA, "10.00")
	forceFire := stmt{`UPDATE kernel.timers SET fire_at = now() - interval '1 day', status = 'fired', fired_at = now() WHERE commitment_id = $1`, []any{future}}
	expire := updateOf(future, `status = 'expired'`)
	for _, w := range e.writers() {
		// The API role cannot even write fired_at; the administrator reaches the guard of commitments.
		requireRule(t, w.name+": timer says fired, due date in the future", runTx(t, w.pool, w.space, forceFire, expire),
			map[string]string{"API": "42501", "administrator": "23001"}[w.name], "")
	}

	// A timer that claims to have fired before its time does not count: the
	// CHECK that ties fired_at to fire_at is dropped here to reach the third
	// condition of the guard on its own.
	if _, err := e.admin.Exec(`ALTER TABLE kernel.timers DROP CONSTRAINT timers_fired_after_fire_at`); err != nil {
		t.Fatalf("drop check: %v", err)
	}
	for _, query := range []string{
		`ALTER TABLE kernel.timers DISABLE TRIGGER timers_coherence_check`, `ALTER TABLE kernel.commitments DISABLE TRIGGER timers_coherence_check`,
	} {
		if _, err := e.admin.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	early := e.commitPast(t, ledgerSpaceA, "10.00")
	requireRule(t, "timer fired before its fire time", runTx(t, e.admin, "",
		stmt{`UPDATE kernel.timers SET fire_at = now() + interval '1 day', status = 'fired', fired_at = now() WHERE commitment_id = $1`, []any{early}},
		updateOf(early, `status = 'expired'`)), "23001", "")

	// A fired timer from an earlier transaction does not count either.
	due := e.commitPast(t, ledgerSpaceA, "10.00")
	if err := runTx(t, e.admin, "", stmt{`UPDATE kernel.timers SET status = 'fired', fired_at = now() WHERE commitment_id = $1`, []any{due}}); err != nil {
		t.Fatalf("fire: %v", err)
	}
	for _, w := range e.writers() {
		requireRule(t, w.name+": timer fired earlier", runTx(t, w.pool, w.space, updateOf(due, `status = 'expired'`)), "23001", "")
	}
}

// TestRescheduleRules guards the rules of moving a due date, for either writer:
// only toward the future, only while open, and as the only effect of the update.
func TestRescheduleRules(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	closed := e.closeCommitments(t)
	for _, w := range e.writers() {
		open := e.commit(t, ledgerSpaceA, "10.00")
		for name, set := range map[string]string{
			"to the past": `due_at = now() - interval '1 day'`, "to now": `due_at = now()`,
			"to just before now": `due_at = now() - interval '1 microsecond'`,
		} {
			requireRule(t, w.name+": reschedule "+name, runTx(t, w.pool, w.space, updateOf(open, set)), "23000", "commitments_reschedule_past")
		}
		requireRule(t, w.name+": reschedule and cancel", runTx(t, w.pool, w.space,
			updateOf(open, `due_at = now() + interval '90 days', canceled_reason = 'x'`)), "23001", "")
		requireRule(t, w.name+": reschedule and fulfilled amount", runTx(t, w.pool, w.space,
			updateOf(open, `due_at = now() + interval '90 days', fulfilled_amount = 0.01`)), "23001", "")
		for state, id := range closed {
			requireRule(t, w.name+": reschedule a "+state+" commitment", runTx(t, w.pool, w.space,
				updateOf(id, `due_at = now() + interval '90 days'`)), "23001", "")
		}
		requireTimer(t, e.admin, open, "scheduled")
		if err := runTx(t, w.pool, w.space, updateOf(open, `due_at = now() + interval '90 days'`)); err != nil {
			t.Errorf("%s: reschedule to the future: %v", w.name, err)
		}
		requireTimer(t, e.admin, open, "scheduled")
	}
}

// TestRescheduleCannotForceExpiryInOneTransaction guards the loophole of moving
// the due date to the present and firing the timer at once: the move is refused
// because the present is already the past for the rest of the transaction.
func TestRescheduleCannotForceExpiryInOneTransaction(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	for _, w := range e.writers() {
		id := e.commit(t, ledgerSpaceA, "10.00")
		err := runTx(t, w.pool, w.space, updateOf(id, `due_at = now()`), fireTimerStmt(id), expiryEventStmt(t, id, ledgerSpaceA))
		requireRule(t, w.name+": reschedule to now and fire", err, "23000", "commitments_reschedule_past")
		if status, _, _ := commitmentState(t, e.admin, id); status != "open" {
			t.Errorf("%s: commitment = %s, want open", w.name, status)
		}
	}
}

// TestTimerGuardLocksCommitment guards the lock order: a direct write of a timer
// waits for a concurrent change of its commitment, so the guard reads the
// committed state. The cascade of a firing would wait on the commitment by
// itself, so it is turned off to let the lock of the guard answer alone.
func TestTimerGuardLocksCommitment(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	toggleTrigger(t, e.admin, "timers", "timers_expire_commitment", false)
	toggleTrigger(t, e.admin, "timers", "timers_coherence_check", false)
	toggleTrigger(t, e.admin, "commitments", "timers_coherence_check", false)
	id := e.commitPast(t, ledgerSpaceA, "10.00")

	holding, err := e.admin.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer holding.Rollback()
	if _, err := holding.Exec(`SELECT 1 FROM kernel.commitments WHERE id = $1 FOR NO KEY UPDATE`, id); err != nil {
		t.Fatalf("lock: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- runTx(t, e.api, ledgerSpaceA, fireTimerStmt(id)) }()
	waitForLockWait(t, e.admin, "UPDATE kernel.timers")
	select {
	case err := <-done:
		t.Fatalf("the firing did not wait for the lock on the commitment: %v", err)
	default:
	}
	if err := holding.Rollback(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := <-done; err != nil {
		t.Errorf("firing after the lock was released: %v", err)
	}
	requireTimer(t, e.admin, id, "fired")
}

// TestOnConflictAndCopyDoNotBypassTimerGuards guards INSERT ... ON CONFLICT and
// COPY: both go through the same guards and privileges as plain statements.
func TestOnConflictAndCopyDoNotBypassTimerGuards(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	id := e.commit(t, ledgerSpaceA, "10.00")
	again := func(conflict string) stmt {
		return stmt{`INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at)
			SELECT id, space_id, id, due_at FROM kernel.commitments WHERE id = $1 ON CONFLICT (id) ` + conflict, []any{id}}
	}
	for name, tc := range map[string]struct{ conflict, state string }{
		"fire early":            {`DO UPDATE SET status = 'fired'`, "23001"},
		"cancel":                {`DO UPDATE SET status = 'canceled'`, "23001"},
		"move the fire time":    {`DO UPDATE SET fire_at = EXCLUDED.fire_at - interval '1 day'`, "23001"},
		"stamp the firing time": {`DO UPDATE SET fired_at = now()`, "42501"},
		"change the commitment": {`DO UPDATE SET commitment_id = EXCLUDED.id`, "42501"},
	} {
		requireRule(t, "API "+name, runTx(t, e.api, ledgerSpaceA, again(tc.conflict)), tc.state, "")
	}
	for _, w := range e.writers() {
		if err := runTx(t, w.pool, w.space, again(`DO NOTHING`)); err != nil {
			t.Errorf("%s: DO NOTHING on an existing timer: %v", w.name, err)
		}
	}

	ctx := context.Background()
	columns := []string{"id", "space_id", "commitment_id", "fire_at", "status", "fired_at"}
	copyRows := func(conn *pgx.Conn, cols []string, rows [][]any) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		if _, err = tx.CopyFrom(ctx, pgx.Identifier{"kernel", "timers"}, cols, pgx.CopyFromRows(rows)); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}
	now := time.Now()
	forged := [][]any{{newID(t), ledgerSpaceA, id, now.Add(-time.Hour), "fired", now}}
	requireRule(t, "administrator COPY of a fired timer", copyRows(pgxConn(t, e.admin, false), columns, forged), "23001", "")
	requireRule(t, "API COPY naming forbidden columns", copyRows(pgxConn(t, e.admin, true), columns, forged), "42501", "")
	requireRule(t, "API COPY of timers", copyRows(pgxConn(t, e.admin, true), columns[:4], [][]any{forged[0][:4]}), "0A000", "")
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.timers`); got != 1 {
		t.Errorf("%d timers after the refused copies, want 1", got)
	}
}

// TestSessionReplicationRoleAndAlterTableDeniedToAPI guards the attacks that
// need more than the privileges of the API role: replication mode, DDL,
// trigger switches, locks and system columns.
func TestSessionReplicationRoleAndAlterTableDeniedToAPI(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	for _, query := range []string{
		`SET session_replication_role = replica`,
		`ALTER TABLE kernel.timers DISABLE TRIGGER ALL`,
		`ALTER TABLE kernel.timers DISABLE TRIGGER timers_guard_update`,
		`ALTER TABLE kernel.timers DISABLE TRIGGER timers_guard_insert`,
		`ALTER TABLE kernel.timers DISABLE TRIGGER timers_expire_commitment`,
		`ALTER TABLE kernel.timers DISABLE TRIGGER timers_coherence_check`,
		`ALTER TABLE kernel.commitments DISABLE TRIGGER timers_coherence_check`,
		`ALTER TABLE kernel.commitments DISABLE TRIGGER commitments_timer_create`,
		`ALTER TABLE kernel.commitments DISABLE TRIGGER commitments_timer_sync`,
		`DROP TRIGGER timers_no_delete ON kernel.timers`,
		`DROP TRIGGER commitments_timer_sync ON kernel.commitments`,
		`CREATE TRIGGER rogue BEFORE INSERT ON kernel.timers FOR EACH ROW EXECUTE FUNCTION kernel.stamp_exchange()`,
		`ALTER TABLE kernel.timers DISABLE ROW LEVEL SECURITY`,
		`ALTER TABLE kernel.timers NO FORCE ROW LEVEL SECURITY`,
		`ALTER TABLE kernel.timers DROP CONSTRAINT timers_fired_coherent`,
		`ALTER TABLE kernel.timers DROP CONSTRAINT timers_commitment_fkey`,
		`DROP INDEX kernel.timers_one_scheduled_key`,
		`ALTER TABLE kernel.timers ADD COLUMN rogue int`,
		`ALTER TABLE kernel.timers OWNER TO nexusthrive_api`,
		`ALTER FUNCTION kernel.timers_guard_update() SECURITY DEFINER`,
		`CREATE OR REPLACE FUNCTION kernel.timers_guard_update() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
		`CREATE OR REPLACE FUNCTION kernel.check_timer_coherence() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NULL; END'`,
		`CREATE OR REPLACE FUNCTION kernel.guard_commitment_update() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
		`DROP TABLE kernel.timers`,
		`INSERT INTO kernel.timers (id, fired_at) OVERRIDING SYSTEM VALUE VALUES ('01900000-0000-7000-8000-0000000001f2', now())`,
	} {
		requireRule(t, query, asAPI(t, e.admin, query), "42501", "")
	}
	// SET ROLE needs another role to belong to; the catalog proves the API role has none.
	if got := count(t, e.admin, `SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = $1`, db.APIRole); got != 0 {
		t.Errorf("the API role is a member of %d roles, want none", got)
	}
	requireRule(t, "LOCK TABLE kernel.timers", runTx(t, e.api, "", stmt{`LOCK TABLE kernel.timers IN SHARE MODE`, nil}), "42501", "")
	if got := count(t, e.admin, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'kernel'
		AND table_name = 'timers' AND (is_identity = 'YES' OR is_generated <> 'NEVER')`); got != 0 {
		t.Errorf("%d identity or generated columns in timers, want none", got)
	}
}

// TestTimerSpaceSwitchBeforeCommitFailsClosed guards the attack of changing
// kernel.space_id before the commit so that row level security hides the
// commitment from the deferred check: the check then sees nothing and fails.
func TestTimerSpaceSwitchBeforeCommitFailsClosed(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	// The sum check of commitments fails closed in the same way; it is turned
	// off here so the check of timers answers on its own.
	toggleTrigger(t, e.admin, "commitments", "commitments_fulfilled_sum_check", false)

	for name, switchTo := range map[string]string{"space B": ledgerSpaceB, "no space": "", "nil uuid": "00000000-0000-0000-0000-000000000000"} {
		due := e.commitPast(t, ledgerSpaceA, "10.00")
		for scenario, stmts := range map[string][]stmt{
			"new commitment": {commitmentStmt(newID(t), ledgerSpaceA, "10.00")},
			"expiry":         {fireTimerStmt(due), expiryEventStmt(t, due, ledgerSpaceA)},
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
					t.Fatalf("%s/%s: %v", scenario, name, err)
				}
			}
			if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, switchTo); err != nil {
				t.Fatalf("switch space: %v", err)
			}
			requireRule(t, scenario+" then "+name, tx.Commit(), "23000", "timers_coherent")
		}
		if status, _, _ := commitmentState(t, e.admin, due); status != "open" {
			t.Errorf("commitment = %s after the failed commits, want open", status)
		}
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.commitments`); got != 3 {
		t.Errorf("%d commitments, want only the 3 that were committed first", got)
	}
}

// TestExpiryAtExactlyTheDueTimeIsAllowed guards the boundary of the clock of the
// database: a commitment whose due time is the very instant of the transaction
// is due, so its timer may fire in that transaction. One microsecond later is
// still the future and is refused.
func TestExpiryAtExactlyTheDueTimeIsAllowed(t *testing.T) {
	t.Parallel()
	e := newCommitmentEnv(t)
	dueAt := func(id, due string) stmt {
		debtor, creditor, resource := partiesOf(ledgerSpaceA)
		return stmt{`INSERT INTO kernel.commitments (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id,
			amount, starts_at, due_at, status, created_by_participant_id)
			VALUES ($1, $2, 'test.commitment', $3, $4, $5, 10, now() - interval '1 day', ` + due + `, 'open', $4)`,
			[]any{id, ledgerSpaceA, debtor, creditor, resource}}
	}
	for _, w := range e.writers() {
		onTime, early := newID(t), newID(t)
		if err := runTx(t, w.pool, w.space, dueAt(onTime, "now()"), fireTimerStmt(onTime), expiryEventStmt(t, onTime, ledgerSpaceA)); err != nil {
			t.Errorf("%s: due exactly now: %v", w.name, err)
		}
		if status, _, _ := commitmentState(t, e.admin, onTime); status != "expired" {
			t.Errorf("%s: commitment due now = %s, want expired", w.name, status)
		}
		requireRule(t, w.name+": due one microsecond later", runTx(t, w.pool, w.space,
			dueAt(early, "now() + interval '1 microsecond'"), fireTimerStmt(early), expiryEventStmt(t, early, ledgerSpaceA)), "23001", "")
	}
}
