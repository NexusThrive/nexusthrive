package state_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// claimResult is what one concurrent claimer got.
type claimResult struct {
	rows []model.OutboxRow
	err  error
}

// TestClaimNeverDeliversTwiceConcurrently guards criterion 4: two claimers ask
// for a batch of 10 at the same instant, twenty times over fresh rows. No row
// appears in both results and together they cover every row.
func TestClaimNeverDeliversTwiceConcurrently(t *testing.T) {
	t.Parallel()
	_, api, clock := outboxSetup(t)
	for round := 0; round < 20; round++ {
		events := enqueueEvents(t, api, spaceA, clock, 10, model.SubscriberInternal)
		wanted := map[int64]bool{}
		for _, event := range events {
			wanted[event.Seq] = true
		}

		var ready, done sync.WaitGroup
		start := make(chan struct{})
		results := make([]claimResult, 2)
		ready.Add(2)
		done.Add(2)
		for i := range results {
			go func() {
				defer done.Done()
				ready.Done()
				<-start
				results[i].rows, results[i].err = state.ClaimOutbox(context.Background(), api, clock, spaceA, model.SubscriberInternal, 10)
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()

		seen := map[int64]int{}
		for i, result := range results {
			if result.err != nil {
				t.Fatalf("round %d: claimer %d: %v", round, i, result.err)
			}
			for _, row := range result.rows {
				seen[row.EventSeq]++
			}
		}
		for seq, n := range seen {
			if n != 1 || !wanted[seq] {
				t.Fatalf("round %d: seq %d was delivered %d times (wanted: %v)", round, seq, n, wanted[seq])
			}
		}
		if len(seen) != 10 {
			t.Fatalf("round %d: the claimers covered %d rows, want 10", round, len(seen))
		}
		clock.at = clock.at.Add(time.Second)
	}
}

// TestClaimSkipsLockedRowsWithoutBlocking guards the SKIP LOCKED part of
// criterion 4: while one claimer holds its transaction open, a second one
// neither waits nor takes the rows, which is also why claimers do not queue.
func TestClaimSkipsLockedRowsWithoutBlocking(t *testing.T) {
	t.Parallel()
	_, api, clock := outboxSetup(t)
	enqueueEvents(t, api, spaceA, clock, 5, model.SubscriberInternal)

	holder := beginInSpace(t, api, spaceA)
	defer holder.Rollback()
	held, err := state.ClaimOutboxTx(context.Background(), holder, clock, model.SubscriberInternal, 10)
	if err != nil || len(held) != 5 {
		t.Fatalf("holder claimed %d rows (%v), want 5", len(held), err)
	}

	finished := make(chan claimResult, 1)
	go func() {
		rows, err := state.ClaimOutbox(context.Background(), api, clock, spaceA, model.SubscriberInternal, 10)
		finished <- claimResult{rows, err}
	}()
	select {
	case result := <-finished:
		if result.err != nil || len(result.rows) != 0 {
			t.Errorf("second claimer = %d rows, %v, want none and no error", len(result.rows), result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the second claimer blocked on rows locked by the first")
	}
	if err := holder.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestConcurrentMarksOfOneRowOnlyOneWins checks that two holders racing to mark
// the same row cannot both succeed.
func TestConcurrentMarksOfOneRowOnlyOneWins(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	row := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]

	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if i == 0 {
				errs[i] = markSent(api, spaceA, clock, row)
			} else {
				errs[i] = markFailed(api, spaceA, clock, row, "boom")
			}
		}()
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if code, _ := model.CodeOf(err); code != model.CodeAlreadyDone && code != model.CodeRuleViolation {
			t.Errorf("loser error = %v, want already_done or rule_violation", err)
		}
	}
	if wins != 1 {
		t.Errorf("%d marks won, want exactly 1", wins)
	}
	if got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); got.Status != "sent" && got.Status != "failed" {
		t.Errorf("row = %+v", got)
	}
}
