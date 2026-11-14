package laws_test

import (
	"context"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestLawMotionDueTimerExpiresCommitment guards the law of motion: time moves
// what is due without anyone asking. With the clock of the engine behind the
// due date nothing fires; with it past the due date the timer fires, the
// commitment becomes expired and the event and its outbox row are written in
// the same round. The clock of the engine can only be behind the clock of the
// database, so a clock a year ahead expires nothing that is not yet due.
func TestLawMotionDueTimerExpiresCommitment(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	ctx := context.Background()
	bookkeeper := model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}
	fire := func() model.TimerRun {
		t.Helper()
		run, err := w.engine.FireDueTimers(ctx, execution.Call{Actor: system, SpaceID: w.spaceA, RequestID: "request-motion"}, 10)
		if err != nil {
			t.Fatalf("FireDueTimers: %v", err)
		}
		return run
	}
	status := func(id string) string {
		t.Helper()
		var got string
		if err := w.admin.QueryRow(`SELECT status FROM kernel.commitments WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatalf("read the commitment: %v", err)
		}
		return got
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	future, err := state.CreateCommitment(ctx, w.api, bookkeeper, w.spaceA, model.NewCommitment{
		Kind: "test.commitment", DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor,
		ResourceID: f.resource, Amount: money(t, "10.00"), DueAt: now.Add(30 * 24 * time.Hour)})
	if err != nil {
		t.Fatalf("CreateCommitment: %v", err)
	}
	due, err := state.CreateCommitment(ctx, w.api, bookkeeper, w.spaceA, model.NewCommitment{
		Kind: "test.commitment", DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor,
		ResourceID: f.resource, Amount: money(t, "10.00"), StartsAt: now.Add(-48 * time.Hour), DueAt: now.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("CreateCommitment: %v", err)
	}

	before := w.counts()
	w.clock.Set(now.Add(-72 * time.Hour))
	if run := fire(); len(run.Fired) != 0 {
		t.Errorf("a clock behind the due date fired %+v", run.Fired)
	}
	if changed := diff(before, w.counts()); len(changed) != 0 {
		t.Errorf("a round that fired nothing changed %v", changed)
	}

	// A year ahead of the database: the overdue timer fires, the one due in a
	// month does not.
	w.clock.Set(now.Add(365 * 24 * time.Hour))
	run := fire()
	if len(run.Fired) != 1 || run.Fired[0].CommitmentID != due.ID {
		t.Fatalf("round = %+v, want only the timer of %s fired", run, due.ID)
	}
	if got := status(due.ID); got != string(model.CommitmentStatusExpired) {
		t.Errorf("overdue commitment = %s, want expired", got)
	}
	if got := status(future.ID); got != string(model.CommitmentStatusOpen) {
		t.Errorf("commitment due in a month = %s, want open: the clock cannot pass the database", got)
	}
	changed := diff(before, w.counts())
	if changed["events"] != 1 || changed["outbox"] != 1 {
		t.Errorf("the round changed %v, want one event and one outbox row", changed)
	}
	var eventType, subscriber, outboxStatus string
	err = w.admin.QueryRow(`SELECT e.type, o.subscriber, o.status FROM kernel.events e JOIN kernel.outbox o ON o.event_seq = e.seq`).
		Scan(&eventType, &subscriber, &outboxStatus)
	if err != nil {
		t.Fatalf("read the event and its outbox row: %v", err)
	}
	if eventType != model.EventTypeCommitmentExpired || subscriber != string(model.SubscriberInternal) || outboxStatus != string(model.OutboxPending) {
		t.Errorf("event %s with outbox %s %s, want the expiry event queued pending for the internal subscriber", eventType, subscriber, outboxStatus)
	}
	if run := fire(); len(run.Fired) != 0 {
		t.Errorf("a second round fired %+v, want nothing: the timer fires once", run.Fired)
	}
}
