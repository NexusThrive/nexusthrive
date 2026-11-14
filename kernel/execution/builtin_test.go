package execution

import (
	"context"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// pastCommitment creates a commitment that is already due in the space, as the
// system, and returns it. The clock of the world is moved ahead of the
// database so that only the ceiling of the database decides when it is due.
func (w *world) pastCommitment(spaceID string, f fixtures) model.Commitment {
	w.t.Helper()
	w.clock.Set(time.Now().Add(time.Hour))
	now := time.Now().UTC().Truncate(time.Microsecond)
	created, err := state.CreateCommitment(context.Background(), w.api, model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}, spaceID,
		model.NewCommitment{
			Kind: "test.commitment", DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor,
			ResourceID: f.resource, Amount: amount(w.t, "10.00"), StartsAt: now.Add(-48 * time.Hour), DueAt: now.Add(-24 * time.Hour),
		})
	if err != nil {
		w.t.Fatalf("CreateCommitment: %v", err)
	}
	return created
}

// TestFireDueTimersNeedsTimerProcess guards follow-up 49 and 62: a firing
// round runs only with kernel.timer.process, only for the system actor, and
// not in a suspended space; a refusal leaves every table as it was.
func TestFireDueTimersNeedsTimerProcess(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	created := w.pastCommitment(w.spaceA, f)
	owner := w.holder(w.spaceA, "owner", model.OwnerRole().Permissions...)
	withKey := w.holder(w.spaceA, "processor", model.PermissionTimerProcess)
	before := w.snapshot()

	for name, actor := range map[string]model.Actor{
		"owner without the key": owner,
		"null actor":            {},
		"person with the key":   withKey,
	} {
		_, err := w.engine.FireDueTimers(context.Background(), call(actor, w.spaceA), 10)
		requireCode(t, name, err, model.CodePermissionDenied)
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("a refused firing round changed %v", changed)
	}

	w.setStatus(w.spaceA, "suspended")
	_, err := w.engine.FireDueTimers(context.Background(), call(system, w.spaceA), 10)
	requireCode(t, "suspended space", err, model.CodeSpaceSuspended)
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("a firing round in a suspended space changed %v", changed)
	}
	w.setStatus(w.spaceA, "active")

	// The clock of the authorizer decides what is due: a clock behind the fire
	// time fires nothing, even though the clock of the database is ahead.
	w.clock.Set(time.Now().Add(-72 * time.Hour))
	run, err := w.engine.FireDueTimers(context.Background(), call(system, w.spaceA), 10)
	if err != nil || len(run.Fired) != 0 {
		t.Errorf("a round with a clock behind the fire time = %+v (%v), want nothing fired", run, err)
	}
	w.clock.Set(time.Now().Add(time.Hour))

	run, err = w.engine.FireDueTimers(context.Background(), call(system, w.spaceA), 10)
	if err != nil || len(run.Fired) != 1 || run.Fired[0].CommitmentID != created.ID {
		t.Fatalf("system round = %+v (%v), want the timer of %s fired", run, err, created.ID)
	}
	if changed := w.changed(before); changed["events"] != 1 || changed["outbox"] != 1 {
		t.Errorf("a round that fired changed %v, want an event and an outbox row", changed)
	}
	var status string
	if err := w.admin.QueryRow(`SELECT status FROM kernel.commitments WHERE id = $1`, created.ID).Scan(&status); err != nil || status != "expired" {
		t.Errorf("commitment status = %q (%v), want expired", status, err)
	}
	_, err = w.engine.FireDueTimers(context.Background(), call(system, w.spaceA), 0)
	requireCode(t, "limit of zero", err, model.CodeInvalidInput)
}

// TestClaimOutboxNeedsClaimPermission guards follow-up 42, 44 and 45: a claim
// runs only with kernel.outbox.claim and not in a suspended space, and the
// rows of a refused claim stay pending and unclaimed.
func TestClaimOutboxNeedsClaimPermission(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	writer := w.writer(w.spaceA, "writer")
	w.clock.Set(time.Now().Add(time.Hour))
	var ran steps
	if err := w.runMoney(context.Background(), call(writer, w.spaceA), moneyOperation.Name, f.handler(t, &ran, nil)); err != nil {
		t.Fatalf("seed an outbox row: %v", err)
	}
	owner := w.holder(w.spaceA, "owner", model.OwnerRole().Permissions...)
	claimer := w.holder(w.spaceA, "claimer", model.PermissionOutboxClaim)
	unclaimed := func() int {
		return w.count(`SELECT count(*) FROM kernel.outbox WHERE status = 'pending' AND claim_token IS NULL AND claimed_until IS NULL`)
	}

	for name, actor := range map[string]model.Actor{"owner without the key": owner, "null actor": {}} {
		_, err := w.engine.ClaimOutbox(context.Background(), call(actor, w.spaceA), model.SubscriberInternal, 10)
		requireCode(t, name, err, model.CodePermissionDenied)
	}
	if got := unclaimed(); got != 1 {
		t.Errorf("unclaimed pending rows = %d after the refused claims, want 1", got)
	}

	w.setStatus(w.spaceA, "suspended")
	_, err := w.engine.ClaimOutbox(context.Background(), call(claimer, w.spaceA), model.SubscriberInternal, 10)
	requireCode(t, "suspended space", err, model.CodeSpaceSuspended)
	if got := unclaimed(); got != 1 {
		t.Errorf("unclaimed pending rows = %d after the claim in a suspended space, want 1", got)
	}
	w.setStatus(w.spaceA, "active")

	// The clock of the authorizer decides what is due: behind the due time
	// nothing is claimed.
	w.clock.Set(time.Now().Add(-48 * time.Hour))
	rows, err := w.engine.ClaimOutbox(context.Background(), call(claimer, w.spaceA), model.SubscriberInternal, 10)
	if err != nil || len(rows) != 0 {
		t.Errorf("claim with a clock behind the due time = %d rows (%v), want none", len(rows), err)
	}
	w.clock.Set(time.Now().Add(time.Hour))

	rows, err = w.engine.ClaimOutbox(context.Background(), call(claimer, w.spaceA), model.SubscriberInternal, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("claim with the key = %d rows (%v), want 1", len(rows), err)
	}
	if got := unclaimed(); got != 0 {
		t.Errorf("unclaimed pending rows = %d after the claim, want 0", got)
	}
	rows, err = w.engine.ClaimOutbox(context.Background(), call(system, w.spaceA), model.SubscriberInternal, 10)
	if err != nil || len(rows) != 0 {
		t.Errorf("system claim of a leased row = %d rows (%v), want none", len(rows), err)
	}
	_, err = w.engine.ClaimOutbox(context.Background(), call(system, w.spaceA), "nobody", 10)
	requireCode(t, "unknown subscriber", err, model.CodeInvalidInput)
}

// TestRunRefusesKernelOperationsByName guards that the exported Run cannot
// call the operations of the kernel with a handler of the caller: an actor
// that holds kernel.timer.process or kernel.outbox.claim must not be able to
// write events of the kernel namespace through them. Names that only look
// like the kernel namespace are not registered and are refused too. The
// engine's own entry points keep working.
func TestRunRefusesKernelOperationsByName(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	w.pastCommitment(w.spaceA, f)
	processor := w.holder(w.spaceA, "processor", model.PermissionTimerProcess, model.PermissionOutboxClaim)
	before := w.snapshot()
	calls := 0

	for _, name := range []string{operationTimerFire, operationOutboxClaim, "KERNEL.timer.fire", " kernel.timer.fire", "kernel.timer.fire ", "Kernel.outbox.claim", "kernel.other"} {
		err := w.engine.Run(context.Background(), call(processor, w.spaceA), name, func(ctx context.Context, tx *Tx) error {
			calls++
			_, err := tx.AppendEvent(ctx, EventInput{Type: model.EventTypeCommitmentExpired, Version: 1, Payload: []byte(`{}`)}, model.SubscriberInternal)
			return err
		})
		requireCode(t, name, err, model.CodeInvalidInput)
	}
	if calls != 0 {
		t.Errorf("a handler of the caller ran %d times under a kernel name", calls)
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("refused kernel names changed %v", changed)
	}

	// The engine's own paths still work for the system actor.
	if run, err := w.engine.FireDueTimers(context.Background(), call(system, w.spaceA), 10); err != nil || len(run.Fired) != 1 {
		t.Errorf("FireDueTimers = %+v (%v), want one fired", run, err)
	}
	if _, err := w.engine.ClaimOutbox(context.Background(), call(processor, w.spaceA), model.SubscriberInternal, 10); err != nil {
		t.Errorf("ClaimOutbox: %v", err)
	}
}
