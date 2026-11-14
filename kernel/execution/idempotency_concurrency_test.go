package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// outcome is what one concurrent call returned.
type outcome struct {
	response Response
	err      error
}

// raceCalls starts one RunIdempotent per call behind a barrier, so that they
// all begin together, and returns what each of them returned.
func (w *world) raceCalls(calls []Call, name string, fns []ResultHandler) []outcome {
	w.t.Helper()
	outcomes := make([]outcome, len(calls))
	barrier := make(chan struct{})
	var started, done sync.WaitGroup
	for i := range calls {
		started.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			started.Done()
			<-barrier
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			outcomes[i].response, outcomes[i].err = w.engine.RunIdempotent(ctx, calls[i], name, fns[i])
		}()
	}
	started.Wait()
	close(barrier)
	done.Wait()
	return outcomes
}

// sleepingHandler is recordHandler that waits 50 ms inside the transaction, so
// that every concurrent call has time to reach the lock before the first ends.
func (f fixtures) sleepingHandler(t *testing.T, runs *counter) ResultHandler {
	inner := f.recordHandler(t, runs)
	return func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
		time.Sleep(50 * time.Millisecond)
		return inner(ctx, tx)
	}
}

// TestConcurrentSameKeyAndBodyRunsOnce guards criterion 5: from 2 to 8 calls
// with the same key and the same body, started together, run the handler once,
// write one exchange and one record, return the same response byte for byte,
// and exactly one of them is not a replay. No call fails, and none leaks a
// uniqueness error. It repeats 20 times with a new key each time.
func TestConcurrentSameKeyAndBodyRunsOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")

	for round := 0; round < 20; round++ {
		callers := 2 + round%7
		key := fmt.Sprintf("k-race-%d", round)
		var runs counter
		calls := make([]Call, callers)
		fns := make([]ResultHandler, callers)
		for i := range calls {
			calls[i] = idemCall(holder, w.spaceA, key, idemBody)
			fns[i] = f.sleepingHandler(t, &runs)
		}
		exchangesBefore := w.count(`SELECT count(*) FROM kernel.exchanges`)

		outcomes := w.raceCalls(calls, moneyOperation.Name, fns)
		fresh := 0
		for i, o := range outcomes {
			if o.err != nil {
				t.Fatalf("round %d, call %d (of %d): %v", round, i, callers, o.err)
			}
			if !o.response.Replayed {
				fresh++
			}
			if !bytes.Equal(o.response.Body, outcomes[0].response.Body) {
				t.Errorf("round %d: call %d answered %s, call 0 answered %s", round, i, o.response.Body, outcomes[0].response.Body)
			}
		}
		if runs.Count() != 1 || fresh != 1 {
			t.Errorf("round %d (%d callers): the handler ran %d times and %d responses were not replays; want 1 and 1", round, callers, runs.Count(), fresh)
		}
		if got := w.count(`SELECT count(*) FROM kernel.exchanges`) - exchangesBefore; got != 1 {
			t.Errorf("round %d: %d exchanges written, want 1", round, got)
		}
		if generations, _, _ := w.recordsOf(key); len(generations) != 1 {
			t.Errorf("round %d: %d records, want 1", round, len(generations))
		}
	}
}

// TestConcurrentSameKeyOtherBodyOneWinnerOtherConflict guards criterion 3 under
// concurrency: two calls with the same key and different bodies have one winner,
// the other gets idempotency_conflict, and the handler runs once.
func TestConcurrentSameKeyOtherBodyOneWinnerOtherConflict(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")

	for round := 0; round < 10; round++ {
		key := fmt.Sprintf("k-race-body-%d", round)
		var runs counter
		calls := []Call{
			idemCall(holder, w.spaceA, key, `{"amount":"10.00"}`),
			idemCall(holder, w.spaceA, key, `{"amount":"20.00"}`),
		}
		outcomes := w.raceCalls(calls, moneyOperation.Name, []ResultHandler{f.sleepingHandler(t, &runs), f.sleepingHandler(t, &runs)})

		winners, conflicts := 0, 0
		for _, o := range outcomes {
			switch {
			case o.err == nil && !o.response.Replayed:
				winners++
			case o.err != nil:
				if code, typed := model.CodeOf(o.err); typed && code == model.CodeIdempotencyConflict {
					conflicts++
				} else {
					t.Errorf("round %d: unexpected error %v", round, o.err)
				}
			default:
				t.Errorf("round %d: a call with another body was answered as a replay", round)
			}
		}
		if winners != 1 || conflicts != 1 || runs.Count() != 1 {
			t.Errorf("round %d: %d winners, %d conflicts, %d runs; want 1, 1 and 1", round, winners, conflicts, runs.Count())
		}
		if generations, _, _ := w.recordsOf(key); len(generations) != 1 {
			t.Errorf("round %d: %d records, want 1", round, len(generations))
		}
	}
}

// TestConcurrentFailureIsNotCachedAndSecondRuns guards criterion 7 under
// concurrency: when the call that holds the key fails, the one that waited for
// it runs the handler itself and succeeds, and only that run leaves a record.
func TestConcurrentFailureIsNotCachedAndSecondRuns(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")

	for round := 0; round < 10; round++ {
		key := fmt.Sprintf("k-race-fail-%d", round)
		var attempts, runs counter
		inner := f.sleepingHandler(t, &runs)
		fn := func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
			if attempts.n.Add(1) == 1 {
				time.Sleep(50 * time.Millisecond)
				return nil, model.NewError(model.CodeRuleViolation, "the first attempt fails", nil)
			}
			return inner(ctx, tx)
		}
		calls := []Call{idemCall(holder, w.spaceA, key, idemBody), idemCall(holder, w.spaceA, key, idemBody)}
		outcomes := w.raceCalls(calls, moneyOperation.Name, []ResultHandler{fn, fn})

		failed, succeeded := 0, 0
		for _, o := range outcomes {
			switch {
			case o.err == nil && !o.response.Replayed:
				succeeded++
			case o.err != nil:
				if code, typed := model.CodeOf(o.err); typed && code == model.CodeRuleViolation {
					failed++
				} else {
					t.Errorf("round %d: unexpected error %v", round, o.err)
				}
			default:
				t.Errorf("round %d: the second call was answered as a replay of a failure", round)
			}
		}
		if failed != 1 || succeeded != 1 || attempts.Count() != 2 {
			t.Errorf("round %d: %d failed, %d succeeded, %d attempts; want 1, 1 and 2", round, failed, succeeded, attempts.Count())
		}
		if generations, _, _ := w.recordsOf(key); len(generations) != 1 {
			t.Errorf("round %d: %d records, want 1", round, len(generations))
		}
	}
}

// TestUnauthorizedCallDoesNotTouchOrWaitOnTheKey guards the order of the engine:
// the permissions are verified before the lock of the key, so a caller without
// permission is refused at once even when another transaction holds the key, and
// cannot learn that the key is busy by waiting for it.
func TestUnauthorizedCallDoesNotTouchOrWaitOnTheKey(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	stranger := w.person(w.spaceA, "stranger")

	busy, err := w.admin.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer busy.Rollback()
	if _, err := busy.Exec(`SELECT set_config('kernel.space_id', $1, true)`, w.spaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	if _, err := busy.Exec(`SELECT kernel.idempotency_lock('k-busy')`); err != nil {
		t.Fatalf("hold the key: %v", err)
	}

	var runs counter
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = w.engine.RunIdempotent(ctx, idemCall(stranger, w.spaceA, "k-busy", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	requireCode(t, "unauthorized call on a busy key", err, model.CodePermissionDenied)
	if time.Since(started) > 2*time.Second {
		t.Errorf("the unauthorized call waited %v for the key", time.Since(started))
	}

	// An authorized call does wait, and goes on once the key is free.
	done := make(chan error, 1)
	go func() {
		_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-busy", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the authorized call did not wait for the key: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := busy.Rollback(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := <-done; err != nil {
		t.Errorf("the authorized call after the key was released: %v", err)
	}
	if runs.Count() != 1 {
		t.Errorf("the handler ran %d times, want 1", runs.Count())
	}
}

// TestEngineLockOrderNoDeadlockWithRevocation guards the lock order: the engine
// takes the locks of the permission check first and the lock of the key after,
// so a grant revoked while operations run never produces a deadlock (40P01).
func TestEngineLockOrderNoDeadlockWithRevocation(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)

	for round := 0; round < 50; round++ {
		actor := w.person(w.spaceA, fmt.Sprintf("racer-%d", round))
		grant := w.grantRole(w.spaceA, actor, fmt.Sprintf("racer_role_%d", round), nil, model.PermissionExchangeWrite, model.PermissionCommitmentWrite)
		key := fmt.Sprintf("k-lock-order-%d", round)
		var runs counter

		var wg sync.WaitGroup
		errs := make(chan error, 3)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := w.engine.RunIdempotent(context.Background(), idemCall(actor, w.spaceA, key, idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
				errs <- err
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.admin.Exec(`UPDATE kernel.grants SET revoked_at = now() WHERE id = $1`, grant)
			errs <- err
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			if isPgCode(err, "40P01") {
				t.Fatalf("round %d: deadlock detected: %v", round, err)
			}
			if err != nil {
				if code, typed := model.CodeOf(err); !typed || code != model.CodePermissionDenied {
					t.Errorf("round %d: unexpected error %v", round, err)
				}
			}
		}
	}
}
