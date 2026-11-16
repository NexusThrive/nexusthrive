package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

func TestWebhookRetryBudgetAndEndpointDisableAreIndependent(t *testing.T) {
	for _, reset := range []bool{false, true} {
		label := "disable"
		if reset {
			label = "success resets"
		}
		t.Run(label, func(t *testing.T) {
			w := newWorld(t)
			ctx := context.Background()
			id := NewRequestID()
			c := call(system, w.spaceA)
			if err := w.engine.CreateWebhookEndpoint(ctx, c, model.NewWebhookEndpoint{ID: id, URL: "https://example.com", Events: []string{"kernel.exchange.recorded.v1"}, Secret: model.SealedWebhookSecret{KeyID: "test", Nonce: make([]byte, 12), Ciphertext: make([]byte, 48)}}); err != nil {
				t.Fatal(err)
			}
			failures := 0
			for event := 0; event < 4; event++ {
				if err := state.InSpace(ctx, w.api, w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
					_, err := state.AppendEventAndEnqueue(ctx, tx, w.clock, model.NewEvent{Type: "kernel.exchange.recorded", Version: 1, Payload: json.RawMessage(`{}`), Actor: system}, model.SubscriberWebhooks)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if err := w.engine.FanoutWebhooks(ctx, c, 100); err != nil {
					t.Fatal(err)
				}
				for attempt := 1; attempt <= 5; attempt++ {
					job, err := w.engine.ClaimWebhookDelivery(ctx, c)
					if err != nil || job == nil {
						t.Fatal("missing due job", job, err)
					}
					status, success := 500, false
					// A client HTTP timeout has no status but is still a remote failure.
					if !reset && failures == 19 {
						status = 0
					}
					if reset && failures == 19 {
						status, success = 204, true
					}
					if err := w.engine.FinishWebhookDelivery(ctx, c, *job, status, 1, success, true); err != nil {
						t.Fatal(err)
					}
					if !success {
						failures++
					}
					if attempt < 5 {
						if next, err := w.engine.ClaimWebhookDelivery(ctx, c); err != nil || next != nil {
							t.Fatal("retry did not wait", next, err)
						}
						w.clock.Set(w.clock.Now().Add(model.OutboxBackoff(attempt) + time.Millisecond))
					}
				}
			}
			var state string
			var consecutive int
			if err := w.admin.QueryRow(`SELECT status,consecutive_failures FROM kernel.webhook_endpoints WHERE id=$1`, id).Scan(&state, &consecutive); err != nil {
				t.Fatal(err)
			}
			if reset {
				if state != "active" || consecutive != 0 {
					t.Fatal("success did not reset", state, consecutive)
				}
			} else {
				if state != "disabled" || consecutive != 20 {
					t.Fatal("endpoint not disabled", state, consecutive)
				}
			}
			if n := w.count(`SELECT count(*) FROM kernel.webhook_delivery_attempts`); n != 20 {
				t.Fatal("attempt history", n)
			}
			if n := w.count(`SELECT count(*) FROM kernel.outbox WHERE status='sent' AND attempts=0`); n != 4 {
				t.Fatal("HTTP failures changed handoff rows", n)
			}
			rows, err := w.engine.ListWebhookDeliveries(ctx, c, id, 10)
			if err != nil || len(rows) != 4 {
				t.Fatal("history hidden", rows, err)
			}
			if !reset && rows[0].FailureReason != "20 consecutive delivery failures" {
				t.Fatal("disabled reason missing", rows[0])
			}
			if job, err := w.engine.ClaimWebhookDelivery(ctx, c); err != nil || job != nil {
				t.Fatal("terminal job reclaimed", job, err)
			}
		})
	}
}

func TestWebhookLocalFailureDoesNotDisableEndpoint(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	c := call(system, w.spaceA)
	id := NewRequestID()
	if err := w.engine.CreateWebhookEndpoint(ctx, c, model.NewWebhookEndpoint{ID: id, URL: "https://example.com", Events: []string{"kernel.exchange.recorded.v1"}, Secret: model.SealedWebhookSecret{KeyID: "test", Nonce: make([]byte, 12), Ciphertext: make([]byte, 48)}}); err != nil {
		t.Fatal(err)
	}
	if err := state.InSpace(ctx, w.api, w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		_, err := state.AppendEventAndEnqueue(ctx, tx, w.clock, model.NewEvent{Type: "kernel.exchange.recorded", Version: 1, Payload: json.RawMessage(`{}`), Actor: system}, model.SubscriberWebhooks)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.engine.FanoutWebhooks(ctx, c, 100); err != nil {
		t.Fatal(err)
	}
	job, err := w.engine.ClaimWebhookDelivery(ctx, c)
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	if _, err := w.admin.Exec(`UPDATE kernel.webhook_endpoints SET consecutive_failures=19 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := w.engine.FinishWebhookDelivery(ctx, c, *job, 0, 1, false, false); err != nil {
		t.Fatal(err)
	}
	var status string
	var failures int
	if err := w.admin.QueryRow(`SELECT status,consecutive_failures FROM kernel.webhook_endpoints WHERE id=$1`, id).Scan(&status, &failures); err != nil || status != "active" || failures != 19 {
		t.Fatal("local failure attributed to endpoint", status, failures, err)
	}
}
