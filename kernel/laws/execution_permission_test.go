package laws_test

import (
	"context"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestLawPermissionExecutionDeniedLeavesNoRows guards criteria 5 and 6: an operation that
// the actor may not run answers permission_denied, the handler never runs and
// no table of the schema changes. The same operation with the right actor, in
// the same space, is the positive control.
func TestLawPermissionExecutionDeniedLeavesNoRows(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	allowed := w.writer(w.spaceA, "allowed")
	nobody := w.person(w.spaceA, "nobody")
	oneOfTwo := w.person(w.spaceA, "one_of_two")
	w.grant(w.spaceA, oneOfTwo, "one_role", nil, model.PermissionExchangeWrite)
	expiry := w.clock.Now().Add(time.Hour)
	expiring := w.person(w.spaceA, "expiring")
	w.grant(w.spaceA, expiring, "expiring_role", &expiry, model.PermissionExchangeWrite, model.PermissionCommitmentWrite)
	revoked := w.person(w.spaceA, "revoked")
	revokedGrant := w.grant(w.spaceA, revoked, "revoked_role", nil, model.PermissionExchangeWrite, model.PermissionCommitmentWrite)
	if err := state.RevokeGrant(context.Background(), w.api, system, w.spaceA, revokedGrant); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	archived := w.writer(w.spaceA, "archived")
	if err := state.ArchiveParticipant(context.Background(), w.api, system, w.spaceA, archived.ParticipantID); err != nil {
		t.Fatalf("ArchiveParticipant: %v", err)
	}
	foreigner := w.writer(w.spaceB, "foreigner")
	// The grant of the expiring actor lapses when the clock reaches its expiry.
	w.clock.Set(expiry)

	op := newOperation(t, f)
	attempts := []struct {
		name  string
		actor model.Actor
		want  model.Code
	}{
		{"no grant", nobody, model.CodePermissionDenied},
		{"only one of the two permissions", oneOfTwo, model.CodePermissionDenied},
		{"expired grant", expiring, model.CodePermissionDenied},
		{"revoked grant", revoked, model.CodePermissionDenied},
		{"archived participant", archived, model.CodePermissionDenied},
		{"actor of another space", foreigner, model.CodePermissionDenied},
		{"null actor (C6)", model.Actor{}, model.CodePermissionDenied},
		{"person without a participant (C6)", model.Actor{Kind: model.ActorPerson}, model.CodePermissionDenied},
		{"unknown kind of actor", model.Actor{Kind: "robot", ParticipantID: allowed.ParticipantID}, model.CodePermissionDenied},
		{"participant that does not exist", model.Actor{Kind: model.ActorPerson, ParticipantID: "01900000-0000-7000-8000-0000000000ee"}, model.CodePermissionDenied},
	}
	for _, a := range attempts {
		before := w.counts()
		err := w.run(a.actor, w.spaceA, "request-denied", op.standard())
		if got, typed := model.CodeOf(err); !typed || got != a.want {
			t.Errorf("%s: error = %v, want code %s", a.name, err, a.want)
		}
		if changed := diff(before, w.counts()); len(changed) != 0 {
			t.Errorf("%s: a denied operation changed %v", a.name, changed)
		}
	}
	if op.calls != 0 {
		t.Errorf("the handler ran %d times for denied calls, want 0", op.calls)
	}

	// An operation behind a capability the space does not have is refused,
	// also for an actor with every permission, and runs once it is active.
	billing := func() error {
		return w.runOperation(allowed, w.spaceA, "request-billing", billingOperation.Name, op.standard())
	}
	before := w.counts()
	err := billing()
	if got, typed := model.CodeOf(err); !typed || got != model.CodeCapabilityInactive {
		t.Errorf("capability not active: error = %v, want capability_inactive", err)
	}
	if changed := diff(before, w.counts()); len(changed) != 0 || op.calls != 0 {
		t.Errorf("a refused capability changed %v and ran the handler %d times", changed, op.calls)
	}
	if err := state.EnableCapability(context.Background(), w.api, system, w.spaceA,
		model.NewSpaceCapability{CapabilityKey: "test.billing", Source: model.CapabilitySourcePackage}); err != nil {
		t.Fatalf("EnableCapability: %v", err)
	}
	if err := billing(); err != nil {
		t.Errorf("the operation with the capability active failed: %v", err)
	}

	// Positive control in the same space: the allowed actor writes the seven
	// tables of the operation (the six of the writes and the idempotency
	// record) and nothing else.
	before = w.counts()
	if err := w.run(allowed, w.spaceA, "request-control", newOperation(t, f).standard()); err != nil {
		t.Fatalf("the allowed operation failed: %v", err)
	}
	if changed := diff(before, w.counts()); len(changed) != 7 || changed["idempotency_records"] != 1 {
		t.Errorf("the allowed operation changed %v, want seven tables, one of them the idempotency record", changed)
	}
}

// TestSystemActorOfAnotherSpaceWritesNothing documents the limit of the
// verification and proves what still holds: the system actor holds every
// permission, but a participant of another space cannot be written as the
// actor of an exchange, so the operation fails and leaves nothing.
func TestSystemActorOfAnotherSpaceWritesNothing(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	foreigner := w.person(w.spaceB, "foreigner")
	op := newOperation(t, f)

	before := w.counts()
	err := w.run(model.Actor{Kind: model.ActorSystem, ParticipantID: foreigner.ParticipantID}, w.spaceA, "request-system", op.standard())
	if got, typed := model.CodeOf(err); !typed || got != model.CodeRuleViolation {
		t.Errorf("error = %v, want rule_violation", err)
	}
	if changed := diff(before, w.counts()); len(changed) != 0 {
		t.Errorf("the operation changed %v", changed)
	}
}

// TestExecutionIsolatedBySpace guards criterion 8 at the engine: an operation
// of space A reads no event of B and cannot write a commitment whose debtor
// lives in B.
func TestExecutionIsolatedBySpace(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	fA := w.ledger(w.spaceA)
	fB := w.ledger(w.spaceB)
	writerA := w.writer(w.spaceA, "writer_a")
	writerB := w.writer(w.spaceB, "writer_b")
	if err := w.run(writerA, w.spaceA, "request-a", newOperation(t, fA).standard()); err != nil {
		t.Fatalf("operation in A: %v", err)
	}
	if err := w.run(writerB, w.spaceB, "request-b", newOperation(t, fB).standard()); err != nil {
		t.Fatalf("operation in B: %v", err)
	}

	var seen []model.Event
	err := w.engine.Run(context.Background(), execution.Call{Actor: writerA, SpaceID: w.spaceA, RequestID: "request-read"}, readOperation.Name,
		func(ctx context.Context, tx *execution.Tx) error {
			var err error
			seen, err = tx.ReadEvents(ctx, 0, 100)
			return err
		})
	if err != nil {
		t.Fatalf("read in A: %v", err)
	}
	if len(seen) != 1 || seen[0].SpaceID != w.spaceA || seen[0].RequestID != "request-a" {
		t.Errorf("space A reads %d events %+v, want only its own", len(seen), seen)
	}

	before := w.counts()
	op := newOperation(t, fA)
	err = w.run(writerA, w.spaceA, "request-cross", func(ctx context.Context, tx *execution.Tx) error {
		return op.stepCommitment(ctx, tx, fB.debtor)
	})
	if got, typed := model.CodeOf(err); !typed || got != model.CodeRuleViolation {
		t.Errorf("a debtor of another space: error = %v, want rule_violation", err)
	}
	if changed := diff(before, w.counts()); len(changed) != 0 {
		t.Errorf("a cross-space commitment changed %v", changed)
	}
}

// TestSuspendedSpaceBlocksWriteAndMoney guards criterion 9: a write and a money
// operation answer space_suspended in a suspended space and in a closed one,
// and write nothing; a read operation still runs.
func TestSuspendedSpaceBlocksWriteAndMoney(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")
	op := newOperation(t, f)

	for _, status := range []string{"suspended", "closed"} {
		w.setStatus(w.spaceA, status)
		before := w.counts()
		err := w.run(actor, w.spaceA, "request-money", op.standard())
		if got, typed := model.CodeOf(err); !typed || got != model.CodeSpaceSuspended {
			t.Errorf("%s space, money operation: error = %v, want space_suspended", status, err)
		}
		err = w.engine.Run(context.Background(), execution.Call{Actor: actor, SpaceID: w.spaceA, RequestID: "request-write"}, writeOperation.Name,
			func(ctx context.Context, tx *execution.Tx) error { return op.stepCommitment(ctx, tx, f.debtor) })
		if got, typed := model.CodeOf(err); !typed || got != model.CodeSpaceSuspended {
			t.Errorf("%s space, write operation: error = %v, want space_suspended", status, err)
		}
		if changed := diff(before, w.counts()); len(changed) != 0 || op.calls != 0 {
			t.Errorf("%s space: the refused operations changed %v and ran the handler %d times", status, changed, op.calls)
		}
		ran := false
		err = w.engine.Run(context.Background(), execution.Call{Actor: actor, SpaceID: w.spaceA, RequestID: "request-read"}, readOperation.Name,
			func(ctx context.Context, tx *execution.Tx) error {
				ran = true
				_, err := tx.ReadEvents(ctx, 0, 10)
				return err
			})
		if err != nil || !ran {
			t.Errorf("%s space, read operation: ran = %v, error = %v, want it to run", status, ran, err)
		}
	}
}
