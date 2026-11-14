package laws_test

import (
	"context"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestLawCommitmentFulfilledNeverExceedsAmount guards the second half of the
// law through the state package: a fulfillment that would take the fulfilled
// amount past the amount is refused and the commitment keeps what it had.
func TestLawCommitmentFulfilledNeverExceedsAmount(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	ctx := context.Background()
	bookkeeper := model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}

	created, err := state.CreateCommitment(ctx, w.api, bookkeeper, w.spaceA,
		model.NewCommitment{Kind: "test.commitment", DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor,
			ResourceID: f.resource, Amount: money(t, "10.00"), DueAt: time.Now().Add(48 * time.Hour)})
	if err != nil {
		t.Fatalf("CreateCommitment: %v", err)
	}
	payment := func(key string) string {
		out, err := state.RecordExchange(ctx, w.api, bookkeeper, w.spaceA, model.NewExchange{
			Kind: "test.exchange", IdempotencyKey: key, Entries: []model.NewEntry{
				{AccountID: f.to, ResourceID: f.resource, Amount: money(t, "6.00")},
				{AccountID: f.from, ResourceID: f.resource, Amount: money(t, "6.00").Neg()},
			}})
		if err != nil {
			t.Fatalf("RecordExchange: %v", err)
		}
		var entryID string
		if err := w.admin.QueryRow(`SELECT id::text FROM kernel.entries WHERE exchange_id = $1 AND amount > 0`, out.ID).Scan(&entryID); err != nil {
			t.Fatalf("read the entry: %v", err)
		}
		return entryID
	}
	if _, partial, err := state.FulfillCommitment(ctx, w.api, bookkeeper, w.spaceA, created.ID, payment("k-six-a")); err != nil || partial.Status != model.CommitmentStatusOpen {
		t.Fatalf("the first fulfillment = %+v (%v), want the commitment still open", partial, err)
	}
	before := checksum(t, w.admin, "commitments") + checksum(t, w.admin, "fulfillments")
	if _, _, err := state.FulfillCommitment(ctx, w.api, bookkeeper, w.spaceA, created.ID, payment("k-six-b")); err == nil {
		t.Errorf("a fulfillment past the amount succeeded")
	}
	if after := checksum(t, w.admin, "commitments") + checksum(t, w.admin, "fulfillments"); after != before {
		t.Errorf("the refused fulfillment changed the commitment or its fulfillments")
	}
}
