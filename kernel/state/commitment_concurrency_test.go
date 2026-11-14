package state_test

import (
	"context"
	"sync"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// runConcurrently starts every function at the same instant and returns their
// errors in order.
func runConcurrently(fns ...func() error) []error {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, len(fns))
	ready.Add(len(fns))
	done.Add(len(fns))
	for i, fn := range fns {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			errs[i] = fn()
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	return errs
}

// requireOneWinner fails unless exactly one result is nil and every other one
// carries the wanted code (or already_done for a cancellation that lost).
func requireOneWinner(t *testing.T, results []error, loser model.Code, alsoAccepted ...model.Code) {
	t.Helper()
	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
			continue
		}
		if isCode(err, loser) {
			continue
		}
		accepted := false
		for _, code := range alsoAccepted {
			accepted = accepted || isCode(err, code)
		}
		if !accepted {
			t.Errorf("the loser got %v, want %s", err, loser)
		}
	}
	if winners != 1 {
		t.Errorf("%d winners among %v, want exactly 1", winners, results)
	}
}

// TestConcurrentFulfillmentsOnlyOneWins guards criterion 7 under real
// concurrency, many times over fresh commitments: two entries of 60.00 against
// a commitment of 100.00 cannot both apply.
func TestConcurrentFulfillmentsOnlyOneWins(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	for round := 0; round < 10; round++ {
		open := c.create(t, "100.00")
		first := c.credit(t, "a-"+string(rune('a'+round)), "60.00")
		second := c.credit(t, "b-"+string(rune('a'+round)), "60.00")

		results := runConcurrently(
			func() error { _, _, err := c.fulfill(open.ID, first); return err },
			func() error { _, _, err := c.fulfill(open.ID, second); return err },
		)
		requireOneWinner(t, results, model.CodeRuleViolation)
		read := c.get(t, open.ID)
		requireAmount(t, "fulfilled amount", read.FulfilledAmount, "60")
		if read.Status != model.CommitmentStatusOpen || len(read.Fulfillments) != 1 {
			t.Fatalf("round %d: commitment = %s with %d fulfillments, want open with 1", round, read.Status, len(read.Fulfillments))
		}
	}
}

// TestConcurrentFulfillmentsThatFitBothApply shows the lock serializes without
// refusing what fits: two entries of 50.00 close a commitment of 100.00.
func TestConcurrentFulfillmentsThatFitBothApply(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	for round := 0; round < 5; round++ {
		open := c.create(t, "100.00")
		first := c.credit(t, "a-"+string(rune('a'+round)), "50.00")
		second := c.credit(t, "b-"+string(rune('a'+round)), "50.00")

		results := runConcurrently(
			func() error { _, _, err := c.fulfill(open.ID, first); return err },
			func() error { _, _, err := c.fulfill(open.ID, second); return err },
		)
		for _, err := range results {
			if err != nil {
				t.Fatalf("round %d: a fulfillment that fits failed: %v", round, err)
			}
		}
		read := c.get(t, open.ID)
		if read.Status != model.CommitmentStatusFulfilled {
			t.Fatalf("round %d: status = %s, want fulfilled", round, read.Status)
		}
		requireAmount(t, "fulfilled amount", read.FulfilledAmount, "100")
	}
}

// TestCancelRacesWithFulfill guards criterion 9 under concurrency: cancelling
// and completing the same commitment have one winner, in whichever order.
func TestCancelRacesWithFulfill(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	for round := 0; round < 10; round++ {
		open := c.create(t, "100.00")
		entry := c.credit(t, "r-"+string(rune('a'+round)), "100.00")

		var fulfilled, canceled bool
		results := runConcurrently(
			func() error {
				_, _, err := c.fulfill(open.ID, entry)
				fulfilled = err == nil
				return err
			},
			func() error {
				_, err := state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, open.ID, "race")
				canceled = err == nil
				return err
			},
		)
		requireOneWinner(t, results, model.CodeAlreadyDone, model.CodeRuleViolation)
		read := c.get(t, open.ID)
		switch {
		case fulfilled && read.Status != model.CommitmentStatusFulfilled, canceled && read.Status != model.CommitmentStatusCanceled:
			t.Fatalf("round %d: status = %s, fulfilled %v canceled %v", round, read.Status, fulfilled, canceled)
		}
		if canceled && len(read.Fulfillments) != 0 {
			t.Fatalf("round %d: a canceled commitment has %d fulfillments", round, len(read.Fulfillments))
		}
	}
}
