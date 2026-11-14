package model

import (
	"encoding/json"
	"time"
)

// EventTypeCommitmentExpired is the type of the event written when a timer
// expires a commitment. Its payload has version 1 (ADR-0034).
const EventTypeCommitmentExpired = "kernel.commitment.expired"

// TimerStatus is the state of a timer. The database derives it: callers never
// choose it.
type TimerStatus string

const (
	// TimerStatusScheduled is a timer waiting for its fire time.
	TimerStatusScheduled TimerStatus = "scheduled"
	// TimerStatusFired is a timer that expired its commitment.
	TimerStatusFired TimerStatus = "fired"
	// TimerStatusCanceled is a timer whose commitment closed before it fired.
	TimerStatusCanceled TimerStatus = "canceled"
)

// Valid reports whether s is one of the known timer statuses.
func (s TimerStatus) Valid() bool {
	switch s {
	case TimerStatusScheduled, TimerStatusFired, TimerStatusCanceled:
		return true
	}
	return false
}

// Timer is the stored expiry timer of one commitment. It has the id of its
// commitment, because a commitment has exactly one expiry timer.
type Timer struct {
	ID           string
	SpaceID      string
	CommitmentID string
	Status       TimerStatus
	// FireAt and CreatedAt are in UTC.
	FireAt    time.Time
	CreatedAt time.Time
	// FiredAt is when the timer fired, in UTC; zero until it does.
	FiredAt time.Time
}

// TimerRun is what one firing round did: the timers it fired, whose commitments
// are now expired, and the timers it canceled because their commitment had
// already closed.
type TimerRun struct {
	Fired    []Timer
	Canceled []Timer
}

// ValidateDueAt checks a due date given to reschedule a commitment. A zero
// value is invalid_input; whether the date lies in the past is decided by the
// database, which owns the clock.
func ValidateDueAt(dueAt time.Time) error {
	if dueAt.IsZero() {
		return NewError(CodeInvalidInput, "commitment due date is required", nil)
	}
	return nil
}

// NewCommitmentExpiredEvent builds the event of a commitment that has just
// expired: it names the commitment as subject and carries its terms, all as
// strings and RFC 3339 times in UTC. The commitment must be the one the
// database returned after expiry, so ClosedAt is the expiry time. The actor is
// the system and the occurrence time is left to the database clock.
func NewCommitmentExpiredEvent(c Commitment, timerID string) NewEvent {
	payload, err := json.Marshal(map[string]string{
		"commitment_id":           c.ID,
		"timer_id":                timerID,
		"kind":                    c.Kind,
		"debtor_participant_id":   c.DebtorParticipantID,
		"creditor_participant_id": c.CreditorParticipantID,
		"resource_id":             c.ResourceID,
		"amount":                  c.Amount.String(),
		"fulfilled_amount":        c.FulfilledAmount.String(),
		"due_at":                  c.DueAt.UTC().Format(time.RFC3339Nano),
		"expired_at":              c.ClosedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		// A map of strings always marshals; failing here is a programming error.
		panic("marshal commitment expired payload: " + err.Error())
	}
	return NewEvent{
		Type:       EventTypeCommitmentExpired,
		Version:    1,
		SubjectRef: "kernel.commitment/" + c.ID,
		Payload:    payload,
		Actor:      Actor{Kind: ActorSystem},
	}
}
