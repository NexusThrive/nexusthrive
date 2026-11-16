package scheduler

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

func TestPassExpiresRealCommitmentOnceWithoutWebhookConfiguration(t *testing.T) {
	ctx := context.Background()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	system := model.Actor{Kind: model.ActorSystem}
	space, err := state.CreateSpace(ctx, api, system, state.NewSpace{Kind: model.SpaceKindBusiness, Name: "Scheduler", Slug: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`UPDATE kernel.spaces SET status='active' WHERE id=$1`, space); err != nil {
		t.Fatal(err)
	}
	person := func(name string) string {
		t.Helper()
		id, err := state.CreateParticipant(ctx, api, system, space, state.NewParticipant{Kind: model.ParticipantKindPerson, DisplayName: name})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	debtor, creditor := person("Debtor"), person("Creditor")
	resource, err := state.CreateResource(ctx, api, system, space, state.NewResource{Kind: model.ResourceKindMoney, Code: "COP", Name: "Peso", Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	amount, err := model.ParseAmount("100.00")
	if err != nil {
		t.Fatal(err)
	}
	commitment, err := state.CreateCommitment(ctx, api, model.Actor{Kind: model.ActorSystem, ParticipantID: creditor}, space, model.NewCommitment{Kind: "test.commitment", DebtorParticipantID: debtor, CreditorParticipantID: creditor, ResourceID: resource, Amount: amount, StartsAt: time.Now().Add(-2 * time.Hour), DueAt: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := capability.NewAuthorizer(model.KernelCatalog(), model.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := execution.NewEngine(api, auth)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := New(engine, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	runner.Pass(ctx)
	runner.Pass(ctx)
	var status string
	if err := admin.QueryRow(`SELECT status FROM kernel.commitments WHERE id=$1`, commitment.ID).Scan(&status); err != nil || status != "expired" {
		t.Fatal(status, err)
	}
	var events, outbox int
	if err := admin.QueryRow(`SELECT count(*) FROM kernel.events WHERE type='kernel.commitment.expired'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(`SELECT count(*) FROM kernel.outbox WHERE status='pending'`).Scan(&outbox); err != nil || events != 1 || outbox != 2 {
		t.Fatal("expiry not atomic or repeated", events, outbox, err)
	}
}
