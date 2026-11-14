package model_test

import (
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// TestSubscriberValid accepts the four subscribers and nothing that merely
// resembles one: the check is exact, with no case folding or trimming.
func TestSubscriberValid(t *testing.T) {
	t.Parallel()
	for _, s := range []model.Subscriber{model.SubscriberWebhooks, model.SubscriberN8N, model.SubscriberAI, model.SubscriberInternal} {
		if !s.Valid() {
			t.Errorf("Subscriber(%q).Valid() = false, want true", s)
		}
	}
	for _, s := range []model.Subscriber{
		"", "Internal", "internal ", " internal", "webhook", "INTERNAL", "n8n\n",
		"internal'; DROP TABLE kernel.outbox; --", "internal\x00",
	} {
		if s.Valid() {
			t.Errorf("Subscriber(%q).Valid() = true, want false", s)
		}
	}
}

// TestOutboxBackoffSchedule pins the starting values of ADR-0031.
func TestOutboxBackoffSchedule(t *testing.T) {
	t.Parallel()
	if model.OutboxMaxAttempts != 5 {
		t.Errorf("OutboxMaxAttempts = %d, want 5", model.OutboxMaxAttempts)
	}
	if model.OutboxLease != 5*time.Minute {
		t.Errorf("OutboxLease = %v, want 5m", model.OutboxLease)
	}
	for attempts, want := range map[int]time.Duration{
		1: 30 * time.Second, 2: 2 * time.Minute, 3: 10 * time.Minute, 4: time.Hour,
		// Outside the retry range there is no wait: a dead row is not retried.
		0: 0, 5: 0, -1: 0, 99: 0,
	} {
		if got := model.OutboxBackoff(attempts); got != want {
			t.Errorf("OutboxBackoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}
