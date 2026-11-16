package execution

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

func TestWebhookRotationWindowAndReplayPreserveHistory(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	actor := w.holder(w.spaceA, "hooks", model.PermissionWebhookWrite, model.PermissionWebhookRead)
	id := NewRequestID()
	initial := model.SealedWebhookSecret{KeyID: "old", Nonce: bytes.Repeat([]byte{1}, 12), Ciphertext: bytes.Repeat([]byte{2}, 48)}
	next := model.SealedWebhookSecret{KeyID: "new", Nonce: bytes.Repeat([]byte{3}, 12), Ciphertext: bytes.Repeat([]byte{4}, 48)}
	c := call(actor, w.spaceA)
	if err := w.engine.CreateWebhookEndpoint(ctx, c, model.NewWebhookEndpoint{ID: id, URL: "https://example.com", Events: []string{"kernel.exchange.recorded.v1"}, Secret: initial}); err != nil {
		t.Fatal(err)
	}
	err := state.InSpace(ctx, w.api, w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		_, err := state.AppendEventAndEnqueue(ctx, tx, w.clock, model.NewEvent{Type: "kernel.exchange.recorded", Version: 1, Payload: json.RawMessage(`{}`), Actor: system}, model.SubscriberWebhooks)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.engine.FanoutWebhooks(ctx, call(system, w.spaceA), 100); err != nil {
		t.Fatal(err)
	}
	w.clock.Set(time.Now().UTC().Truncate(time.Microsecond))
	until, err := w.engine.RotateWebhookSecret(ctx, c, id, next)
	if err != nil || !until.Equal(w.clock.Now().Add(15*time.Minute)) {
		t.Fatal(until, err)
	}
	if _, err := w.engine.RotateWebhookSecret(ctx, c, id, next); err == nil {
		t.Fatal("overlapping rotation accepted")
	}
	_, err = w.engine.RotateWebhookSecret(ctx, call(system, w.spaceB), id, next)
	requireCode(t, "cross-space rotation", err, model.CodeNotFound)
	job, err := w.engine.ClaimWebhookDelivery(ctx, call(system, w.spaceA))
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	if job.PreviousSecret == nil || job.PreviousValidUntil == nil || !bytes.Equal(job.PreviousSecret.Ciphertext, initial.Ciphertext) || !bytes.Equal(job.Secret.Ciphertext, next.Ciphertext) {
		t.Fatal("rotation secrets not preserved")
	}
	if err := w.engine.FinishWebhookDelivery(ctx, call(system, w.spaceA), *job, 500, 1, false, true); err != nil {
		t.Fatal(err)
	}
	replay := c
	replay.IdempotencyKey = "replay-1"
	replay.Request = json.RawMessage(`{"deliveryId":"` + job.ID + `"}`)
	first, err := w.engine.ReplayWebhookDelivery(ctx, replay)
	if err != nil {
		t.Fatal(err)
	}
	again, err := w.engine.ReplayWebhookDelivery(ctx, replay)
	if err != nil || !again.Replayed || !bytes.Equal(first.Body, again.Body) {
		t.Fatal("replay not idempotent", again, err)
	}
	if w.count(`SELECT count(*) FROM kernel.webhook_deliveries`) != 2 {
		t.Fatal("original changed or replay duplicated")
	}
	repeated, err := w.engine.ClaimWebhookDelivery(ctx, call(system, w.spaceA))
	if err != nil || repeated == nil || !bytes.Equal(repeated.Body, job.Body) || repeated.ID == job.ID {
		t.Fatal("replay changed stable event body", repeated, err)
	}
	if err := w.engine.FinishWebhookDelivery(ctx, call(system, w.spaceA), *repeated, 200, 1, true, true); err != nil {
		t.Fatal(err)
	}
	denied := replay
	denied.Actor = w.person(w.spaceA, "no-replay")
	denied.IdempotencyKey = "denied"
	_, err = w.engine.ReplayWebhookDelivery(ctx, denied)
	requireCode(t, "replay permission", err, model.CodePermissionDenied)
	cross := replay
	cross.Actor = w.holder(w.spaceB, "other", model.PermissionWebhookWrite)
	cross.SpaceID = w.spaceB
	cross.IdempotencyKey = "cross"
	_, err = w.engine.ReplayWebhookDelivery(ctx, cross)
	requireCode(t, "replay isolation", err, model.CodeNotFound)
	if _, err := w.admin.Exec(`UPDATE kernel.webhook_endpoints SET status='disabled' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	replay.IdempotencyKey = "disabled"
	_, err = w.engine.ReplayWebhookDelivery(ctx, replay)
	requireCode(t, "disabled replay", err, model.CodeRuleViolation)
	w.clock.Set(until)
	if _, err := w.engine.RotateWebhookSecret(ctx, c, id, initial); err != nil {
		t.Fatal("rotation at boundary denied", err)
	}
	if w.count(`SELECT count(*) FROM kernel.webhook_delivery_attempts`) != 2 {
		t.Fatal("history changed")
	}
}
