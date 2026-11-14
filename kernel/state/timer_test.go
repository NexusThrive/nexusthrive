package state_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// realClock is the clock of production, for tests that fire timers: the clock
// may run behind the database but never ahead of it.
var realClock model.Clock = model.SystemClock{}

// newPastCommitment builds a commitment that started two days ago and was due a
// day ago, the only kind a test can expire, because the injected clock may not
// run ahead of the clock of the database.
func (c commitmentFixtures) newPastCommitment(t *testing.T, value string) model.NewCommitment {
	t.Helper()
	in := c.newCommitment(t, value)
	now := time.Now().UTC().Truncate(time.Microsecond)
	in.StartsAt, in.DueAt = now.Add(-48*time.Hour), now.Add(-24*time.Hour)
	return in
}

// createPast creates a commitment that is already due in space A.
func (c commitmentFixtures) createPast(t *testing.T, value string) model.Commitment {
	t.Helper()
	got, err := state.CreateCommitment(context.Background(), c.api, c.actor, spaceA, c.newPastCommitment(t, value))
	if err != nil {
		t.Fatalf("CreateCommitment(%s): %v", value, err)
	}
	return got
}

// fireDue fires the timers of space A that are due according to clock.
func (c commitmentFixtures) fireDue(clock model.Clock, limit int) (model.TimerRun, error) {
	return state.FireDueTimers(context.Background(), c.api, clock, c.actor, spaceA, limit)
}

// mustFire is fireDue that fails the test on error.
func (c commitmentFixtures) mustFire(t *testing.T, clock model.Clock, limit int) model.TimerRun {
	t.Helper()
	run, err := c.fireDue(clock, limit)
	if err != nil {
		t.Fatalf("FireDueTimers: %v", err)
	}
	return run
}

// expire creates a commitment that is due and expires it the way the kernel
// does, by firing its timer. It returns the commitment as stored after expiry.
func (c commitmentFixtures) expire(t *testing.T, value string) model.Commitment {
	t.Helper()
	created := c.createPast(t, value)
	run := c.mustFire(t, realClock, 500)
	if len(run.Fired) == 0 {
		t.Fatalf("no timer fired for the due commitment %s", created.ID)
	}
	got := c.get(t, created.ID)
	if got.Status != model.CommitmentStatusExpired {
		t.Fatalf("commitment = %s, want expired", got.Status)
	}
	return got
}

// timer reads the timer of a commitment of space A.
func (c commitmentFixtures) timer(t *testing.T, commitmentID string) model.Timer {
	t.Helper()
	got, err := state.GetTimer(context.Background(), c.api, spaceA, commitmentID)
	if err != nil {
		t.Fatalf("GetTimer: %v", err)
	}
	return got
}

// counts returns the timers with a status, the expiry events and the outbox rows.
func (c commitmentFixtures) counts(t *testing.T, status string) (timers, events, outbox int) {
	t.Helper()
	return count(t, c.admin, `SELECT count(*) FROM kernel.timers WHERE status = $1`, status),
		count(t, c.admin, `SELECT count(*) FROM kernel.events WHERE type = 'kernel.commitment.expired'`),
		count(t, c.admin, `SELECT count(*) FROM kernel.outbox`)
}

// TestCommitmentCreatesScheduledTimerWithDueAt guards criterion 1 through the
// state functions: the timer exists as soon as the commitment does.
func TestCommitmentCreatesScheduledTimerWithDueAt(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")

	got := c.timer(t, created.ID)
	if got.ID != created.ID || got.CommitmentID != created.ID || got.SpaceID != spaceA {
		t.Errorf("timer = %+v, want the id and the commitment of %s in space A", got, created.ID)
	}
	if got.Status != model.TimerStatusScheduled || !got.FireAt.Equal(created.DueAt) || !got.FiredAt.IsZero() {
		t.Errorf("timer = %s fire %v fired %v, want scheduled at %v and not fired", got.Status, got.FireAt, got.FiredAt, created.DueAt)
	}
	for name, at := range map[string]time.Time{"FireAt": got.FireAt, "CreatedAt": got.CreatedAt} {
		if at.Location() != time.UTC || at.IsZero() {
			t.Errorf("%s = %v, want a time in UTC", name, at)
		}
	}
}

// TestCommitmentRollbackLeavesNoTimer guards criterion 1: the timer lives and
// dies with the transaction of its commitment.
func TestCommitmentRollbackLeavesNoTimer(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()
	err := state.InSpace(ctx, c.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := state.AppendCommitment(ctx, tx, c.actor, c.newCommitment(t, "10.00")); err != nil {
			return err
		}
		return model.NewError(model.CodeRuleViolation, "the rest of the operation failed", nil)
	})
	if !isCode(err, model.CodeRuleViolation) {
		t.Fatalf("error = %v", err)
	}
	if n := count(t, c.admin, `SELECT count(*) FROM kernel.timers`); n != 0 {
		t.Errorf("%d timers after a rollback, want 0", n)
	}
}

// TestGetTimerOfUnknownCommitmentIsNotFound guards that a timer is read through
// its commitment and the space.
func TestGetTimerOfUnknownCommitmentIsNotFound(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")
	if _, err := state.GetTimer(context.Background(), c.api, spaceA, "01900000-0000-7000-8000-0000000000ff"); !isCode(err, model.CodeNotFound) {
		t.Errorf("unknown commitment: %v, want not_found", err)
	}
	if _, err := state.GetTimer(context.Background(), c.api, spaceB, created.ID); !isCode(err, model.CodeNotFound) {
		t.Errorf("commitment of another space: %v, want not_found", err)
	}
	if _, err := state.GetTimer(context.Background(), c.api, spaceA, "not-a-uuid"); !isCode(err, model.CodeInvalidInput) {
		t.Errorf("bad id: %v, want invalid_input", err)
	}
}

// TestRescheduleMovesTimerWithoutSecondOne guards criterion 2: the timer moves
// with the due date, in place, and the commitment never has a second one.
func TestRescheduleMovesTimerWithoutSecondOne(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")
	newDue := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Microsecond)

	got, err := state.RescheduleCommitment(context.Background(), c.api, c.actor, spaceA, created.ID, newDue)
	if err != nil {
		t.Fatalf("RescheduleCommitment: %v", err)
	}
	if !got.DueAt.Equal(newDue) || got.Status != model.CommitmentStatusOpen {
		t.Errorf("commitment = %s due %v, want open due %v", got.Status, got.DueAt, newDue)
	}
	moved := c.timer(t, created.ID)
	if moved.ID != created.ID || !moved.FireAt.Equal(newDue) || moved.Status != model.TimerStatusScheduled {
		t.Errorf("timer = %+v, want the same timer scheduled at %v", moved, newDue)
	}
	if n := count(t, c.admin, `SELECT count(*) FROM kernel.timers`); n != 1 {
		t.Errorf("%d timers, want 1", n)
	}
	if n := count(t, c.admin, `SELECT count(*) FROM kernel.timers WHERE status = 'scheduled'`); n != 1 {
		t.Errorf("%d scheduled timers, want 1", n)
	}
}

// TestRescheduleToThePastIsRuleViolation guards criterion 2 and the loophole
// behind it: moving the due date into the past would let a writer force expiry.
func TestRescheduleToThePastIsRuleViolation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")
	for name, at := range map[string]time.Time{
		"a day ago": time.Now().Add(-24 * time.Hour), "a second ago": time.Now().Add(-time.Second), "now": time.Now(),
	} {
		if _, err := state.RescheduleCommitment(context.Background(), c.api, c.actor, spaceA, created.ID, at); !isCode(err, model.CodeRuleViolation) {
			t.Errorf("reschedule to %s: error = %v, want rule_violation", name, err)
		}
	}
	if got := c.get(t, created.ID); !got.DueAt.Equal(created.DueAt) {
		t.Errorf("due date = %v, want it unchanged at %v", got.DueAt, created.DueAt)
	}
	if got := c.timer(t, created.ID); !got.FireAt.Equal(created.DueAt) {
		t.Errorf("timer fires at %v, want it unchanged at %v", got.FireAt, created.DueAt)
	}
}

// TestRescheduleClosedCommitmentIsAlreadyDone guards criterion 2: a closed
// commitment is not rescheduled, whatever state it closed in.
func TestRescheduleClosedCommitmentIsAlreadyDone(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	future := time.Now().Add(90 * 24 * time.Hour)
	for status, id := range c.closedCommitments(t) {
		if _, err := state.RescheduleCommitment(context.Background(), c.api, c.actor, spaceA, id, future); !isCode(err, model.CodeAlreadyDone) {
			t.Errorf("reschedule a %s commitment: error = %v, want already_done", status, err)
		}
	}
}

// TestRescheduleValidatesInputAndActor guards the errors found before any write.
func TestRescheduleValidatesInputAndActor(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")
	ctx := context.Background()
	future := time.Now().Add(90 * 24 * time.Hour)

	if _, err := state.RescheduleCommitment(ctx, c.api, c.actor, spaceA, created.ID, time.Time{}); !isCode(err, model.CodeInvalidInput) {
		t.Errorf("zero due date: %v, want invalid_input", err)
	}
	if _, err := state.RescheduleCommitment(ctx, c.api, c.actor, spaceA, "nope", future); !isCode(err, model.CodeInvalidInput) {
		t.Errorf("bad id: %v, want invalid_input", err)
	}
	if _, err := state.RescheduleCommitment(ctx, c.api, c.actor, spaceA, "01900000-0000-7000-8000-0000000000ff", future); !isCode(err, model.CodeNotFound) {
		t.Errorf("unknown commitment: %v, want not_found", err)
	}
	if _, err := state.RescheduleCommitment(ctx, c.api, c.actorB, spaceB, created.ID, future); !isCode(err, model.CodeNotFound) {
		t.Errorf("commitment of another space: %v, want not_found", err)
	}
	for name, actor := range map[string]model.Actor{
		"person": {Kind: model.ActorPerson, ParticipantID: c.debtor}, "null": {}, "system without participant": {Kind: model.ActorSystem},
	} {
		if _, err := state.RescheduleCommitment(ctx, c.api, actor, spaceA, created.ID, future); !isCode(err, model.CodePermissionDenied) {
			t.Errorf("%s actor: %v, want permission_denied", name, err)
		}
	}
	if got := c.get(t, created.ID); !got.DueAt.Equal(created.DueAt) {
		t.Errorf("due date = %v, want it unchanged", got.DueAt)
	}
}

// TestAppendRescheduleLivesAndDiesWithTheTransaction guards one operation, one
// transaction for rescheduling.
func TestAppendRescheduleLivesAndDiesWithTheTransaction(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")
	future := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Microsecond)
	err := state.InSpace(context.Background(), c.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := state.AppendRescheduleCommitment(ctx, tx, c.actor, created.ID, future); err != nil {
			return err
		}
		return model.NewError(model.CodeRuleViolation, "the rest of the operation failed", nil)
	})
	if !isCode(err, model.CodeRuleViolation) {
		t.Fatalf("error = %v", err)
	}
	if got := c.timer(t, created.ID); !got.FireAt.Equal(created.DueAt) {
		t.Errorf("timer fires at %v after a rollback, want %v", got.FireAt, created.DueAt)
	}
}

// TestFulfillCancelsTimer guards criterion 3: a commitment closed by its last
// fulfillment leaves no scheduled timer.
func TestFulfillCancelsTimer(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "40.00")
	c.mustFulfill(t, created.ID, c.credit(t, "k1", "40.00"))
	if got := c.timer(t, created.ID); got.Status != model.TimerStatusCanceled {
		t.Errorf("timer = %s, want canceled", got.Status)
	}
}

// TestPartialFulfillKeepsTimerScheduled guards that only closing cancels it.
func TestPartialFulfillKeepsTimerScheduled(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")
	c.mustFulfill(t, created.ID, c.credit(t, "k1", "40.00"))
	if got := c.timer(t, created.ID); got.Status != model.TimerStatusScheduled {
		t.Errorf("timer = %s, want scheduled", got.Status)
	}
}

// TestCancelCommitmentCancelsTimer guards criterion 3: canceling closes the
// timer too.
func TestCancelCommitmentCancelsTimer(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")
	if _, err := state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, created.ID, "asked to stop"); err != nil {
		t.Fatalf("CancelCommitment: %v", err)
	}
	if got := c.timer(t, created.ID); got.Status != model.TimerStatusCanceled {
		t.Errorf("timer = %s, want canceled", got.Status)
	}
}

// TestFireDueTimersBeforeFireAtReturnsNone guards criterion 4: with the clock
// one second before the fire time, nothing fires.
func TestFireDueTimersBeforeFireAtReturnsNone(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.createPast(t, "100.00")

	run := c.mustFire(t, &fixedClock{at: created.DueAt.Add(-time.Second)}, 10)
	if len(run.Fired) != 0 || len(run.Canceled) != 0 {
		t.Errorf("run = %+v, want nothing before the fire time", run)
	}
	if got := c.get(t, created.ID); got.Status != model.CommitmentStatusOpen {
		t.Errorf("commitment = %s, want open", got.Status)
	}
	if timers, events, outbox := c.counts(t, "scheduled"); timers != 1 || events != 0 || outbox != 0 {
		t.Errorf("scheduled %d, events %d, outbox %d, want 1, 0, 0", timers, events, outbox)
	}
}

// TestFireDueTimersClockCannotRunAheadOfDatabase guards the ceiling of the
// database clock: a clock a year ahead does not expire what is not yet due.
func TestFireDueTimersClockCannotRunAheadOfDatabase(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "100.00")

	run := c.mustFire(t, &fixedClock{at: time.Now().Add(365 * 24 * time.Hour)}, 10)
	if len(run.Fired) != 0 {
		t.Errorf("run = %+v, want nothing: the commitment is due in a month", run)
	}
	if got := c.get(t, created.ID); got.Status != model.CommitmentStatusOpen {
		t.Errorf("commitment = %s, want open", got.Status)
	}
}

// TestFireDueTimersFiresExpiresAndWritesEventAndOutbox guards criterion 5: one
// round fires the timer, expires the commitment, writes the event and queues it
// for the internal subscriber, all in one transaction.
func TestFireDueTimersFiresExpiresAndWritesEventAndOutbox(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.createPast(t, "100.00")
	c.mustFulfill(t, created.ID, c.credit(t, "k1", "40.00"))
	// The commitment is partly fulfilled and still open: it is the one that expires.
	before := time.Now().Add(-time.Minute)

	run := c.mustFire(t, &fixedClock{at: created.DueAt.Add(time.Second)}, 10)
	after := time.Now().Add(time.Minute)
	if len(run.Fired) != 1 || len(run.Canceled) != 0 {
		t.Fatalf("run = %+v, want one fired timer", run)
	}
	fired := run.Fired[0]
	if fired.ID != created.ID || fired.CommitmentID != created.ID || fired.Status != model.TimerStatusFired {
		t.Errorf("fired timer = %+v, want the timer of %s fired", fired, created.ID)
	}
	if fired.FiredAt.Before(before) || fired.FiredAt.After(after) || fired.FiredAt.Location() != time.UTC {
		t.Errorf("fired_at = %v, want the time of the database in UTC", fired.FiredAt)
	}
	stored := c.timer(t, created.ID)
	if stored.Status != model.TimerStatusFired || !stored.FiredAt.Equal(fired.FiredAt) {
		t.Errorf("stored timer = %+v, want the fired one", stored)
	}
	got := c.get(t, created.ID)
	if got.Status != model.CommitmentStatusExpired || !got.ClosedAt.Equal(fired.FiredAt) {
		t.Errorf("commitment = %s closed %v, want expired and closed when the timer fired (%v)", got.Status, got.ClosedAt, fired.FiredAt)
	}
	requireAmount(t, "fulfilled amount", got.FulfilledAmount, "40")

	events, err := state.ReadEvents(context.Background(), c.api, spaceA, 1, 100)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var expiry []model.Event
	for _, e := range events {
		if e.Type == model.EventTypeCommitmentExpired {
			expiry = append(expiry, e)
		}
	}
	if len(expiry) != 1 {
		t.Fatalf("%d expiry events, want 1", len(expiry))
	}
	event := expiry[0]
	if event.Version != 1 || event.SubjectRef != "kernel.commitment/"+created.ID || event.Actor.Kind != model.ActorSystem {
		t.Errorf("event = v%d %q %s, want v1 about the commitment by the system", event.Version, event.SubjectRef, event.Actor.Kind)
	}
	var payload map[string]string
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	for key, want := range map[string]string{
		"commitment_id": created.ID, "timer_id": created.ID, "kind": "test.commitment", "amount": "100", "fulfilled_amount": "40",
		"debtor_participant_id": c.debtor, "creditor_participant_id": c.actor.ParticipantID, "resource_id": c.cop,
		"due_at": created.DueAt.UTC().Format(time.RFC3339Nano), "expired_at": got.ClosedAt.UTC().Format(time.RFC3339Nano),
	} {
		if payload[key] != want {
			t.Errorf("payload[%s] = %q, want %q", key, payload[key], want)
		}
	}

	var subscribers, statuses []string
	rows, err := c.admin.Query(`SELECT subscriber, status FROM kernel.outbox WHERE event_seq = $1`, event.Seq)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var subscriber, status string
		if err := rows.Scan(&subscriber, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		subscribers, statuses = append(subscribers, subscriber), append(statuses, status)
	}
	if len(subscribers) != 1 || subscribers[0] != "internal" || statuses[0] != "pending" {
		t.Errorf("outbox rows = %v %v, want one pending row for internal", subscribers, statuses)
	}

	// Nothing is left to fire: a second round does nothing.
	if again := c.mustFire(t, realClock, 10); len(again.Fired) != 0 {
		t.Errorf("second round fired %d timers, want 0", len(again.Fired))
	}
	if _, events, outbox := c.counts(t, "fired"); events != 1 || outbox != 1 {
		t.Errorf("events %d, outbox %d after two rounds, want 1 and 1", events, outbox)
	}
}

// TestFireDueTimersRespectsLimitAndOrder guards that a round takes the oldest
// fire times first and no more than the limit.
func TestFireDueTimersRespectsLimitAndOrder(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()
	var created []model.Commitment
	for i, hours := range []time.Duration{3, 1, 2} {
		in := c.newPastCommitment(t, "10.00")
		in.DueAt = time.Now().UTC().Add(-hours * time.Hour).Truncate(time.Microsecond)
		got, err := state.CreateCommitment(ctx, c.api, c.actor, spaceA, in)
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		created = append(created, got)
	}
	run := c.mustFire(t, realClock, 2)
	if len(run.Fired) != 2 || run.Fired[0].CommitmentID != created[0].ID || run.Fired[1].CommitmentID != created[2].ID {
		t.Fatalf("fired = %+v, want the commitments due 3 and 2 hours ago, in that order", run.Fired)
	}
	if got := c.get(t, created[1].ID); got.Status != model.CommitmentStatusOpen {
		t.Errorf("the newest due commitment is %s, want open", got.Status)
	}
	if rest := c.mustFire(t, realClock, 2); len(rest.Fired) != 1 || rest.Fired[0].CommitmentID != created[1].ID {
		t.Errorf("second round = %+v, want the last one", rest.Fired)
	}
}

// TestFireDueTimersRollsBackEverythingOnFailure guards criterion 5: one
// transaction. A round rolled back by the caller leaves nothing, and a failure
// while writing the outbox row undoes the firing and the event as well.
func TestFireDueTimersRollsBackEverythingOnFailure(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.createPast(t, "100.00")
	ctx := context.Background()

	err := state.InSpace(ctx, c.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		run, err := state.FireDueTimersTx(ctx, tx, realClock, c.actor, 10)
		if err != nil {
			return err
		}
		if len(run.Fired) != 1 {
			t.Errorf("fired %d inside the transaction, want 1", len(run.Fired))
		}
		return model.NewError(model.CodeRuleViolation, "the rest of the operation failed", nil)
	})
	if !isCode(err, model.CodeRuleViolation) {
		t.Fatalf("error = %v", err)
	}
	requireUntouched := func(label string) {
		t.Helper()
		if got := c.timer(t, created.ID); got.Status != model.TimerStatusScheduled || !got.FiredAt.IsZero() {
			t.Errorf("%s: timer = %s, want scheduled", label, got.Status)
		}
		if got := c.get(t, created.ID); got.Status != model.CommitmentStatusOpen || !got.ClosedAt.IsZero() {
			t.Errorf("%s: commitment = %s, want open", label, got.Status)
		}
		if _, events, outbox := c.counts(t, "fired"); events != 0 || outbox != 0 {
			t.Errorf("%s: events %d, outbox %d, want 0 and 0", label, events, outbox)
		}
	}
	requireUntouched("rolled back by the caller")

	// A failure in the last write of the round: the outbox row cannot be written.
	if _, err := c.admin.Exec(`CREATE FUNCTION public.refuse_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbox is down'; END $$`); err != nil {
		t.Fatalf("create function: %v", err)
	}
	if _, err := c.admin.Exec(`CREATE TRIGGER refuse_outbox BEFORE INSERT ON kernel.outbox FOR EACH ROW EXECUTE FUNCTION public.refuse_outbox()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if _, err := c.fireDue(realClock, 10); err == nil || !strings.Contains(err.Error(), "outbox is down") {
		t.Fatalf("FireDueTimers with a failing outbox = %v, want that failure", err)
	}
	requireUntouched("outbox failure")
}

// fulfillLeavingTimerScheduled fulfills a commitment with the triggers that
// cancel its timer turned off, and turns them on again: the corrupt state that a
// firing round must repair, built in a throwaway database.
func (c commitmentFixtures) fulfillLeavingTimerScheduled(t *testing.T, created model.Commitment, key string) {
	t.Helper()
	entry := c.credit(t, key, created.Amount.String())
	toggle := func(action string) {
		for _, query := range []string{
			`ALTER TABLE kernel.commitments ` + action + ` TRIGGER commitments_timer_sync`,
			`ALTER TABLE kernel.commitments ` + action + ` TRIGGER timers_coherence_check`,
			`ALTER TABLE kernel.timers ` + action + ` TRIGGER timers_coherence_check`,
		} {
			if _, err := c.admin.Exec(query); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		}
	}
	toggle("DISABLE")
	c.mustFulfill(t, created.ID, entry)
	toggle("ENABLE")
	if got := c.timer(t, created.ID); got.Status != model.TimerStatusScheduled {
		t.Fatalf("timer = %s, want the corrupt scheduled one", got.Status)
	}
}

// TestFireDueTimersCancelsTimerOfFulfilledCommitment guards criterion 6: a
// timer whose commitment is already closed is canceled and the commitment and
// the history stay as they were.
func TestFireDueTimersCancelsTimerOfFulfilledCommitment(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.createPast(t, "40.00")
	c.fulfillLeavingTimerScheduled(t, created, "k1")
	closedAt := c.get(t, created.ID).ClosedAt

	run := c.mustFire(t, realClock, 10)
	if len(run.Fired) != 0 || len(run.Canceled) != 1 || run.Canceled[0].CommitmentID != created.ID || run.Canceled[0].Status != model.TimerStatusCanceled {
		t.Fatalf("run = %+v, want the one timer canceled and none fired", run)
	}
	if got := c.timer(t, created.ID); got.Status != model.TimerStatusCanceled {
		t.Errorf("timer = %s, want canceled", got.Status)
	}
	if got := c.get(t, created.ID); got.Status != model.CommitmentStatusFulfilled || !got.ClosedAt.Equal(closedAt) {
		t.Errorf("commitment = %s closed %v, want it untouched", got.Status, got.ClosedAt)
	}
	if _, events, outbox := c.counts(t, "fired"); events != 0 || outbox != 0 {
		t.Errorf("events %d, outbox %d, want none for a canceled timer", events, outbox)
	}
}

// TestFireDueTimersClockCeilingAlsoLimitsCancellation guards that the ceiling of
// the database clock applies to the repair of a corrupt timer too: a timer that
// is not yet due is left alone even when the injected clock runs far ahead.
func TestFireDueTimersClockCeilingAlsoLimitsCancellation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.create(t, "40.00")
	c.fulfillLeavingTimerScheduled(t, created, "k1")

	run := c.mustFire(t, &fixedClock{at: time.Now().Add(365 * 24 * time.Hour)}, 10)
	if len(run.Fired) != 0 || len(run.Canceled) != 0 {
		t.Errorf("run = %+v, want nothing: the timer is not due yet", run)
	}
	if got := c.timer(t, created.ID); got.Status != model.TimerStatusScheduled {
		t.Errorf("timer = %s, want it left alone", got.Status)
	}
}

// TestFireDueTimersIsolationBetweenSpaces guards criterion 8: a round of one
// space neither fires nor sees the timers of another.
func TestFireDueTimersIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()
	inA := c.createPast(t, "10.00")
	debtorB := mustParticipant(t, c.api, spaceB, newPerson("", "Debtor B"))
	in := model.NewCommitment{
		Kind: "test.commitment", DebtorParticipantID: debtorB, CreditorParticipantID: c.actorB.ParticipantID, ResourceID: c.resourceB,
		Amount: amount(t, "10.00"), StartsAt: time.Now().Add(-48 * time.Hour), DueAt: time.Now().Add(-24 * time.Hour),
	}
	inB, err := state.CreateCommitment(ctx, c.api, c.actorB, spaceB, in)
	if err != nil {
		t.Fatalf("create in space B: %v", err)
	}

	run := c.mustFire(t, realClock, 10)
	if len(run.Fired) != 1 || run.Fired[0].CommitmentID != inA.ID {
		t.Fatalf("run of space A = %+v, want only its own timer", run.Fired)
	}
	if got, err := state.GetTimer(ctx, c.api, spaceB, inB.ID); err != nil || got.Status != model.TimerStatusScheduled {
		t.Errorf("timer of space B = %+v (%v), want it scheduled", got, err)
	}
	if _, err := state.GetTimer(ctx, c.api, spaceA, inB.ID); !isCode(err, model.CodeNotFound) {
		t.Errorf("space A reads the timer of space B: %v, want not_found", err)
	}
	if got := count(t, c.admin, `SELECT count(*) FROM kernel.events WHERE type = 'kernel.commitment.expired' AND space_id = $1`, spaceB); got != 0 {
		t.Errorf("%d expiry events in space B, want 0", got)
	}
	runB, err := state.FireDueTimers(ctx, c.api, realClock, c.actorB, spaceB, 10)
	if err != nil || len(runB.Fired) != 1 || runB.Fired[0].CommitmentID != inB.ID {
		t.Errorf("run of space B = %+v (%v), want its own timer", runB.Fired, err)
	}
}

// TestFireWithoutCurrentSpaceIsRuleViolation guards criterion 8: a transaction
// with no current space fires nothing and writes nothing.
func TestFireWithoutCurrentSpaceIsRuleViolation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.createPast(t, "10.00")
	ctx := context.Background()

	tx, err := c.api.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := state.FireDueTimersTx(ctx, tx, realClock, c.actor, 10); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("FireDueTimersTx without a space: %v, want rule_violation", err)
	}
	if _, err := state.AppendRescheduleCommitment(ctx, tx, c.actor, created.ID, time.Now().Add(time.Hour)); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("AppendRescheduleCommitment without a space: %v, want rule_violation", err)
	}
	_ = tx.Rollback()
	if got := c.timer(t, created.ID); got.Status != model.TimerStatusScheduled {
		t.Errorf("timer = %s, want scheduled", got.Status)
	}
	if _, err := c.fireDue(realClock, 10); err != nil {
		t.Errorf("a round with a space still works: %v", err)
	}
}

// TestFulfillExpiredCommitmentIsRuleViolation guards criterion 9: after a real
// expiry, by firing, no fulfillment is accepted.
func TestFulfillExpiredCommitmentIsRuleViolation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	expired := c.expire(t, "100.00")
	entry := c.credit(t, "k-late", "40.00")
	if _, _, err := c.fulfill(expired.ID, entry); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("fulfill an expired commitment: error = %v, want rule_violation", err)
	}
	if n := count(t, c.admin, `SELECT count(*) FROM kernel.fulfillments`); n != 0 {
		t.Errorf("%d fulfillments, want 0", n)
	}
	if got := c.get(t, expired.ID); got.Status != model.CommitmentStatusExpired || !got.FulfilledAmount.IsZero() {
		t.Errorf("commitment = %s fulfilled %s, want expired and untouched", got.Status, got.FulfilledAmount)
	}
}

// TestCancelExpiredCommitmentIsAlreadyDone guards criterion 9: an expired
// commitment is closed for good.
func TestCancelExpiredCommitmentIsAlreadyDone(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	expired := c.expire(t, "100.00")
	if _, err := state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, expired.ID, "too late"); !isCode(err, model.CodeAlreadyDone) {
		t.Errorf("cancel an expired commitment: error = %v, want already_done", err)
	}
	if got := c.get(t, expired.ID); got.Status != model.CommitmentStatusExpired || got.CanceledReason != "" {
		t.Errorf("commitment = %s %q, want expired and untouched", got.Status, got.CanceledReason)
	}
}

// TestFireDueTimersRequiresSystemActor guards the actor rule: only the system
// fires timers, and a refused call leaves every count as it was.
func TestFireDueTimersRequiresSystemActor(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	created := c.createPast(t, "100.00")
	for name, actor := range map[string]model.Actor{
		"person":  {Kind: model.ActorPerson, ParticipantID: c.debtor},
		"agent":   {Kind: model.ActorAgent, ParticipantID: c.debtor},
		"api key": {Kind: model.ActorAPIKey, ParticipantID: c.debtor},
		"null":    {},
		"unknown": {Kind: "robot", ParticipantID: c.debtor},
	} {
		_, err := state.FireDueTimers(context.Background(), c.api, realClock, actor, spaceA, 10)
		if !isCode(err, model.CodePermissionDenied) {
			t.Errorf("%s actor: error = %v, want permission_denied", name, err)
		}
	}
	if timers, events, outbox := c.counts(t, "scheduled"); timers != 1 || events != 0 || outbox != 0 {
		t.Errorf("scheduled %d, events %d, outbox %d after the refused calls, want 1, 0, 0", timers, events, outbox)
	}
	if got := c.get(t, created.ID); got.Status != model.CommitmentStatusOpen {
		t.Errorf("commitment = %s, want open", got.Status)
	}
	// The system actor needs no participant to fire.
	if run, err := state.FireDueTimers(context.Background(), c.api, realClock, model.Actor{Kind: model.ActorSystem}, spaceA, 10); err != nil || len(run.Fired) != 1 {
		t.Errorf("system actor without participant = %+v (%v), want one fired timer", run, err)
	}
}

// TestFireDueTimersValidatesInput guards the errors found before any write.
func TestFireDueTimersValidatesInput(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	c.createPast(t, "100.00")
	ctx := context.Background()

	if _, err := state.FireDueTimers(ctx, c.api, nil, c.actor, spaceA, 10); !isCode(err, model.CodeInvalidInput) {
		t.Errorf("nil clock: %v, want invalid_input", err)
	}
	for _, limit := range []int{-1, 0, 501} {
		if _, err := state.FireDueTimers(ctx, c.api, realClock, c.actor, spaceA, limit); !isCode(err, model.CodeInvalidInput) {
			t.Errorf("limit %d: %v, want invalid_input", limit, err)
		}
	}
	for name, id := range map[string]string{"malformed": "x", "nil uuid": "00000000-0000-0000-0000-000000000000"} {
		actor := model.Actor{Kind: model.ActorSystem, ParticipantID: id}
		if _, err := state.FireDueTimers(ctx, c.api, realClock, actor, spaceA, 10); !isCode(err, model.CodeInvalidInput) {
			t.Errorf("%s participant id: %v, want invalid_input", name, err)
		}
	}
	if _, err := state.FireDueTimers(ctx, c.api, realClock, c.actor, "", 10); !isCode(err, model.CodeInvalidInput) {
		t.Errorf("no space: %v, want invalid_input", err)
	}
	if run, err := state.FireDueTimers(ctx, c.api, realClock, c.actor, spaceA, 500); err != nil || len(run.Fired) != 1 {
		t.Errorf("limit 500 = %+v (%v), want one fired timer", run, err)
	}
}
