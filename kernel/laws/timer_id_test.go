package laws_test

import "testing"

// TestTimerIdIsTheCommitmentIdAndNoSecondTimerFits fixes follow-up 65 as far as
// a test can: the guard of timer inserts does not demand that the id of a
// timer be the id of its commitment, a hardening that needs a migration and
// stays out of this story. What holds today is that every timer has its
// commitment's id and that a second scheduled timer with an id of its own is
// refused by the unique index, so the gap cannot be reached.
func TestTimerIdIsTheCommitmentIdAndNoSecondTimerFits(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")
	op := newOperation(t, f)
	if err := w.run(actor, w.spaceA, "request-timer", op.standard()); err != nil {
		t.Fatalf("the operation failed: %v", err)
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM kernel.timers WHERE id <> commitment_id`); got != 0 {
		t.Errorf("%d timers have an id other than their commitment's", got)
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM kernel.timers WHERE id = $1`, op.commitment); got != 1 {
		t.Errorf("the commitment %s has %d timers under its own id, want 1", op.commitment, got)
	}

	second := stmt{
		`INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at) VALUES ($1, $2, $3, (SELECT due_at FROM kernel.commitments WHERE id = $3))`,
		[]any{newID(t), w.spaceA, op.commitment},
	}
	err := runIn(t, w.api, w.spaceA, second)
	if pgCode(err) != "23505" || pgConstraint(err) != "timers_one_scheduled_key" {
		t.Errorf("a second scheduled timer: error %v, want the unique index timers_one_scheduled_key", err)
	}
}
