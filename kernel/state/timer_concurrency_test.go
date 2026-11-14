package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// requireNoDeadlock fails when err is a deadlock or a serialization failure,
// which the lock order of the kernel must make impossible.
func requireNoDeadlock(t *testing.T, label string, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001") {
		t.Errorf("%s: %v", label, err)
	}
}

// TestConcurrentFireDueTimersFiresOnce guards criterion 7 under real
// concurrency: from two to eight rounds started at the same instant fire one
// due timer once, write one event and one outbox row, and expire the commitment
// once. It repeats over fresh commitments.
func TestConcurrentFireDueTimersFiresOnce(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	for round := 0; round < 20; round++ {
		created := c.createPast(t, "10.00")
		callers := 2 + round%7

		var mu sync.Mutex
		fired := 0
		fns := make([]func() error, callers)
		for i := range fns {
			fns[i] = func() error {
				run, err := c.fireDue(realClock, 10)
				mu.Lock()
				fired += len(run.Fired)
				mu.Unlock()
				return err
			}
		}
		for i, err := range runConcurrently(fns...) {
			if err != nil {
				t.Fatalf("round %d caller %d: %v", round, i, err)
			}
		}
		if fired != 1 {
			t.Fatalf("round %d: %d timers fired by %d callers, want 1", round, fired, callers)
		}
		if got := c.get(t, created.ID); got.Status != model.CommitmentStatusExpired {
			t.Fatalf("round %d: status = %s, want expired", round, got.Status)
		}
		subject := "kernel.commitment/" + created.ID
		if got := count(t, c.admin, `SELECT count(*) FROM kernel.events WHERE type = 'kernel.commitment.expired' AND subject_ref = $1`, subject); got != 1 {
			t.Fatalf("round %d: %d events for the commitment, want 1", round, got)
		}
		if got := count(t, c.admin, `SELECT count(*) FROM kernel.outbox o JOIN kernel.events e ON e.seq = o.event_seq WHERE e.subject_ref = $1`, subject); got != 1 {
			t.Fatalf("round %d: %d outbox rows for the commitment, want 1", round, got)
		}
	}
}

// TestFireSkipsCommitmentLockedByAnotherTransaction guards that a round never
// waits for a commitment another transaction holds: it skips it and a later
// round takes it.
func TestFireSkipsCommitmentLockedByAnotherTransaction(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.createPast(t, "10.00")
	ctx := context.Background()

	holding, err := c.admin.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer holding.Rollback()
	if _, err := holding.ExecContext(ctx, `SELECT 1 FROM kernel.commitments WHERE id = $1 FOR NO KEY UPDATE`, created.ID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	done := make(chan error, 1)
	var run model.TimerRun
	go func() {
		var err error
		run, err = c.fireDue(realClock, 10)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil || len(run.Fired) != 0 || len(run.Canceled) != 0 {
			t.Errorf("round with a locked commitment = %+v (%v), want an empty round", run, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the round waited for a commitment locked by another transaction")
	}
	if err := holding.Rollback(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := c.mustFire(t, realClock, 10); len(got.Fired) != 1 {
		t.Errorf("round after the release fired %d timers, want 1", len(got.Fired))
	}
}

// TestFireRacesWithFulfill guards criterion 7 against a fulfillment: the due
// commitment is either fulfilled or expired, never both, whichever goes first.
// The losing fulfillment is a rule_violation and the losing round writes nothing.
func TestFireRacesWithFulfill(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	for round := 0; round < 15; round++ {
		created := c.createPast(t, "100.00")
		entry := c.credit(t, fmt.Sprintf("race-%d", round), "100.00")

		var fulfilled bool
		var fired int
		results := runConcurrently(
			func() error {
				_, _, err := c.fulfill(created.ID, entry)
				fulfilled = err == nil
				if err != nil && !isCode(err, model.CodeRuleViolation) {
					return err
				}
				return nil
			},
			func() error {
				run, err := c.fireDue(realClock, 10)
				fired = len(run.Fired)
				return err
			},
		)
		for _, err := range results {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		got := c.get(t, created.ID)
		subject := "kernel.commitment/" + created.ID
		events := count(t, c.admin, `SELECT count(*) FROM kernel.events WHERE subject_ref = $1`, subject)
		switch {
		case fulfilled && fired == 0 && got.Status == model.CommitmentStatusFulfilled && events == 0 && len(got.Fulfillments) == 1:
			if timer := c.timer(t, created.ID); timer.Status != model.TimerStatusCanceled {
				t.Fatalf("round %d: timer = %s after the fulfillment won, want canceled", round, timer.Status)
			}
		case !fulfilled && fired == 1 && got.Status == model.CommitmentStatusExpired && events == 1 && len(got.Fulfillments) == 0:
		default:
			t.Fatalf("round %d: fulfilled %v, fired %d, status %s, events %d, fulfillments %d: want exactly one winner",
				round, fulfilled, fired, got.Status, events, len(got.Fulfillments))
		}
	}
}

// TestFireWaitsForNothingAndTakesLocksInOrder guards the lock order, commitment
// first and timer second, in every operation that touches both: firing,
// canceling and rescheduling the same commitment at once never deadlocks, and
// the commitment ends in a state its timer agrees with.
func TestFireWaitsForNothingAndTakesLocksInOrder(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()
	for lap := 0; lap < 50; lap++ {
		created := c.createPast(t, "10.00")
		results := runConcurrently(
			func() error {
				_, err := c.fireDue(realClock, 10)
				return err
			},
			func() error {
				_, err := state.CancelCommitment(ctx, c.api, c.actor, spaceA, created.ID, "race")
				return err
			},
			func() error {
				_, err := state.RescheduleCommitment(ctx, c.api, c.actor, spaceA, created.ID, time.Now().Add(48*time.Hour))
				return err
			},
		)
		for i, err := range results {
			requireNoDeadlock(t, fmt.Sprintf("lap %d caller %d", lap, i), err)
			if err != nil && !isCode(err, model.CodeAlreadyDone) {
				t.Errorf("lap %d caller %d: %v", lap, i, err)
			}
		}
		got := c.get(t, created.ID)
		timer := c.timer(t, created.ID)
		switch got.Status {
		case model.CommitmentStatusOpen:
			if timer.Status != model.TimerStatusScheduled || !timer.FireAt.Equal(got.DueAt) {
				t.Errorf("lap %d: open commitment with timer %+v", lap, timer)
			}
		case model.CommitmentStatusCanceled:
			if timer.Status != model.TimerStatusCanceled {
				t.Errorf("lap %d: canceled commitment with timer %s", lap, timer.Status)
			}
		case model.CommitmentStatusExpired:
			if timer.Status != model.TimerStatusFired {
				t.Errorf("lap %d: expired commitment with timer %s", lap, timer.Status)
			}
		default:
			t.Errorf("lap %d: status %s", lap, got.Status)
		}
	}
}

// TestFireRacesWithRescheduleOfManyCommitments guards the repair of a stale
// candidate: while one round scans many due commitments, each of them is
// rescheduled by its own transaction. A reschedule that commits after the scan
// read the old fire time but before the round locks the commitment must leave
// that timer alone: the round fires only what is still due when it fires, and
// the commitment that was moved stays open with its timer at the new time.
func TestFireRacesWithRescheduleOfManyCommitments(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()
	const commitments = 30
	for lap := 0; lap < 6; lap++ {
		created := make([]model.Commitment, commitments)
		for i := range created {
			created[i] = c.createPast(t, "10.00")
		}
		later := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Microsecond)
		fns := []func() error{func() error {
			_, err := c.fireDue(realClock, 500)
			return err
		}}
		moved := make([]bool, commitments)
		for i := range created {
			fns = append(fns, func() error {
				_, err := state.RescheduleCommitment(ctx, c.api, c.actor, spaceA, created[i].ID, later)
				moved[i] = err == nil
				if isCode(err, model.CodeAlreadyDone) {
					return nil
				}
				return err
			})
		}
		for i, err := range runConcurrently(fns...) {
			requireNoDeadlock(t, fmt.Sprintf("lap %d caller %d", lap, i), err)
			if err != nil {
				t.Fatalf("lap %d caller %d: %v", lap, i, err)
			}
		}
		for i, got := range created {
			stored := c.get(t, got.ID)
			timer := c.timer(t, got.ID)
			switch {
			case moved[i] && stored.Status == model.CommitmentStatusOpen && timer.Status == model.TimerStatusScheduled && timer.FireAt.Equal(later):
			case !moved[i] && stored.Status == model.CommitmentStatusExpired && timer.Status == model.TimerStatusFired:
			default:
				t.Fatalf("lap %d commitment %d: moved %v, commitment %s, timer %s at %v", lap, i, moved[i], stored.Status, timer.Status, timer.FireAt)
			}
		}
	}
}
