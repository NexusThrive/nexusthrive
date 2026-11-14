package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// expiredCommitment is a commitment as the database leaves it after expiry.
func expiredCommitment(t *testing.T) model.Commitment {
	t.Helper()
	return model.Commitment{
		ID: "01900000-0000-7000-8000-0000000000c1", SpaceID: "01900000-0000-7000-8000-0000000000a1",
		Kind: "test.commitment", DebtorParticipantID: testDebtorID, CreditorParticipantID: testCreditorID,
		ResourceID: testResourceID, Amount: mustAmount(t, "100.00"), FulfilledAmount: mustAmount(t, "40.00"),
		Status: model.CommitmentStatusExpired,
		DueAt:  time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), ClosedAt: time.Date(2030, 1, 1, 0, 0, 5, 0, time.UTC),
	}
}

// TestNewCommitmentExpiredEvent guards the pure shape of the expiry event: type,
// version, subject, system actor and the payload of the commitment as strings.
func TestNewCommitmentExpiredEvent(t *testing.T) {
	t.Parallel()
	c := expiredCommitment(t)
	const timerID = "01900000-0000-7000-8000-0000000000c2"
	got := model.NewCommitmentExpiredEvent(c, timerID)

	if got.Type != "kernel.commitment.expired" || model.EventTypeCommitmentExpired != "kernel.commitment.expired" {
		t.Errorf("Type = %q, want kernel.commitment.expired", got.Type)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if want := "kernel.commitment/" + c.ID; got.SubjectRef != want {
		t.Errorf("SubjectRef = %q, want %q", got.SubjectRef, want)
	}
	if got.Actor.Kind != model.ActorSystem || got.Actor.ParticipantID != "" {
		t.Errorf("Actor = %+v, want the system actor with no participant", got.Actor)
	}
	if !got.OccurredAt.IsZero() {
		t.Errorf("OccurredAt = %v, want zero so the database stamps it", got.OccurredAt)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("the event does not validate: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	want := map[string]any{
		"commitment_id": c.ID, "timer_id": timerID, "kind": "test.commitment",
		"debtor_participant_id": testDebtorID, "creditor_participant_id": testCreditorID,
		"resource_id": testResourceID, "amount": "100", "fulfilled_amount": "40",
		"due_at": "2030-01-01T00:00:00Z", "expired_at": "2030-01-01T00:00:05Z",
	}
	if len(payload) != len(want) {
		t.Errorf("payload has %d keys, want %d: %v", len(payload), len(want), payload)
	}
	for key, value := range want {
		if payload[key] != value {
			t.Errorf("payload[%s] = %v, want %v", key, payload[key], value)
		}
	}
}

// TestNewCommitmentExpiredEventUsesUTC guards that times in another zone are
// written in UTC, so the same instant always yields the same payload.
func TestNewCommitmentExpiredEventUsesUTC(t *testing.T) {
	t.Parallel()
	c := expiredCommitment(t)
	bogota := time.FixedZone("America/Bogota", -5*3600)
	c.DueAt, c.ClosedAt = c.DueAt.In(bogota), c.ClosedAt.In(bogota)
	var payload map[string]any
	if err := json.Unmarshal(model.NewCommitmentExpiredEvent(c, "t").Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["due_at"] != "2030-01-01T00:00:00Z" || payload["expired_at"] != "2030-01-01T00:00:05Z" {
		t.Errorf("times = %v %v, want UTC", payload["due_at"], payload["expired_at"])
	}
}

// TestValidateDueAt guards the rule of rescheduling input: a due date is
// required and the database decides whether it is in the past.
func TestValidateDueAt(t *testing.T) {
	t.Parallel()
	if err := model.ValidateDueAt(time.Time{}); err == nil {
		t.Error("a zero due date must be invalid_input")
	} else if code, _ := model.CodeOf(err); code != model.CodeInvalidInput {
		t.Errorf("code = %v, want invalid_input", code)
	}
	if err := model.ValidateDueAt(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Errorf("a due date must be valid: %v", err)
	}
}

// TestTimerStatusValid guards the closed set of timer states.
func TestTimerStatusValid(t *testing.T) {
	t.Parallel()
	for _, s := range []model.TimerStatus{model.TimerStatusScheduled, model.TimerStatusFired, model.TimerStatusCanceled} {
		if !s.Valid() {
			t.Errorf("%q must be valid", s)
		}
	}
	for _, s := range []model.TimerStatus{"", "Scheduled", "late"} {
		if s.Valid() {
			t.Errorf("%q must be invalid", s)
		}
	}
}
