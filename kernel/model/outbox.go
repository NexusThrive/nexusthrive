package model

import "time"

// Subscriber names who an outbox row is delivered to.
type Subscriber string

// The subscribers an outbox row can address. The database checks the same set.
const (
	SubscriberWebhooks Subscriber = "webhooks"
	SubscriberN8N      Subscriber = "n8n"
	SubscriberAI       Subscriber = "ai"
	SubscriberInternal Subscriber = "internal"
)

// Valid reports whether s is one of the four subscribers, exactly: there is no
// case folding or trimming.
func (s Subscriber) Valid() bool {
	switch s {
	case SubscriberWebhooks, SubscriberN8N, SubscriberAI, SubscriberInternal:
		return true
	}
	return false
}

// OutboxStatus is the delivery state of an outbox row.
type OutboxStatus string

// The delivery states. A row is pending until its first claim, failed between
// retries, and sent or dead when final.
const (
	OutboxPending OutboxStatus = "pending"
	OutboxSent    OutboxStatus = "sent"
	OutboxFailed  OutboxStatus = "failed"
	OutboxDead    OutboxStatus = "dead"
)

const (
	// OutboxMaxAttempts is the number of failed attempts after which a row is
	// dead. The database holds the same limit in outbox_state_check (ADR-0031).
	OutboxMaxAttempts = 5
	// OutboxLease is how long a claim holds a row before it counts as a failed
	// attempt (ADR-0031).
	OutboxLease = 5 * time.Minute
)

// outboxBackoff is the wait after the first four failed attempts.
var outboxBackoff = [OutboxMaxAttempts - 1]time.Duration{
	30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour,
}

// OutboxBackoff returns how long to wait before the next try of a row that has
// failed attempts times. Outside 1 to 4 there is no wait: a row with no
// failure has not waited, and one with five is dead and is not retried.
func OutboxBackoff(attempts int) time.Duration {
	if attempts < 1 || attempts > len(outboxBackoff) {
		return 0
	}
	return outboxBackoff[attempts-1]
}

// OutboxRow is one delivery of an event to a subscriber.
type OutboxRow struct {
	// EventSeq is the seq of the event to deliver.
	EventSeq int64
	// SpaceID is the space of the event and of the row.
	SpaceID string
	// Subscriber is who the event is delivered to.
	Subscriber Subscriber
	// Status is the delivery state.
	Status OutboxStatus
	// Attempts counts the failed attempts, expired claims included.
	Attempts int
	// NextAttemptAt is the earliest time the row may be claimed.
	NextAttemptAt time.Time
	// LastError is the cause of the last failure, empty if none.
	LastError string
	// DeliveredAt is when the row was marked sent; nil until then.
	DeliveredAt *time.Time
	// ClaimToken names the holder of the claim, empty when the row is free.
	ClaimToken string
	// ClaimedUntil is when the claim lapses; nil when the row is free.
	ClaimedUntil *time.Time
}
