package model

import (
	"strings"
	"time"
)

const (
	// maxOriginRefLength is the longest opaque origin reference, in characters.
	maxOriginRefLength = 255
	// maxCancelReasonLength is the longest cancellation reason, in characters.
	maxCancelReasonLength = 500
)

// CommitmentStatus is the state of a commitment. The kernel derives it: callers
// never write it.
type CommitmentStatus string

const (
	// CommitmentStatusOpen is a commitment with something still pending.
	CommitmentStatusOpen CommitmentStatus = "open"
	// CommitmentStatusFulfilled is a commitment whose fulfillments reach its amount.
	CommitmentStatusFulfilled CommitmentStatus = "fulfilled"
	// CommitmentStatusCanceled is a commitment closed with a reason.
	CommitmentStatusCanceled CommitmentStatus = "canceled"
	// CommitmentStatusExpired is a commitment closed because it was not met in
	// time. Only the timer story writes it.
	CommitmentStatusExpired CommitmentStatus = "expired"
)

// Valid reports whether s is one of the known statuses.
func (s CommitmentStatus) Valid() bool {
	switch s {
	case CommitmentStatusOpen, CommitmentStatusFulfilled, CommitmentStatusCanceled, CommitmentStatusExpired:
		return true
	}
	return false
}

// NewCommitment is the input of a commitment: the debtor owes the creditor an
// amount of a resource by DueAt. The space, the creator, the state and the
// fulfilled amount are never part of it: the space comes from the transaction,
// the creator from the actor and the rest from the kernel.
type NewCommitment struct {
	// Kind names the commitment with a namespace, for example test.commitment.
	Kind                  string
	DebtorParticipantID   string
	CreditorParticipantID string
	ResourceID            string
	// OriginRef is an optional opaque pointer to whatever created the commitment.
	OriginRef string
	Amount    Amount
	// StartsAt is when the commitment begins; zero means the database clock.
	StartsAt time.Time
	// DueAt is when the commitment is due; it is required.
	DueAt time.Time
}

// Commitment is a stored commitment.
type Commitment struct {
	ID                     string
	SpaceID                string
	Kind                   string
	DebtorParticipantID    string
	CreditorParticipantID  string
	ResourceID             string
	OriginRef              string
	CanceledReason         string
	CreatedByParticipantID string
	Amount                 Amount
	FulfilledAmount        Amount
	Status                 CommitmentStatus
	// StartsAt, DueAt and CreatedAt are in UTC.
	StartsAt  time.Time
	DueAt     time.Time
	CreatedAt time.Time
	// ClosedAt is when the commitment left the open state; zero while open.
	ClosedAt time.Time
	// Fulfillments are filled only by reading a single commitment.
	Fulfillments []Fulfillment
}

// Fulfillment ties one ledger entry, applied in full, to a commitment.
type Fulfillment struct {
	ID           string
	SpaceID      string
	CommitmentID string
	EntryID      string
	ResourceID   string
	Amount       Amount
	CreatedAt    time.Time
}

// Validate checks the commitment without touching the database. Every failure
// is invalid_input. The scale of the resource is checked by the database, which
// knows it.
func (c NewCommitment) Validate() error {
	if len(c.Kind) > maxEventTypeLength || !eventTypePattern.MatchString(c.Kind) {
		return NewError(CodeInvalidInput, "commitment kind must be two or more lowercase segments separated by dots", nil)
	}
	if !isNonNilUUID(c.DebtorParticipantID) || !isNonNilUUID(c.CreditorParticipantID) || !isNonNilUUID(c.ResourceID) {
		return NewError(CodeInvalidInput, "debtor, creditor and resource ids must be non-nil UUIDs", nil)
	}
	if c.DebtorParticipantID == c.CreditorParticipantID {
		return NewError(CodeInvalidInput, "debtor and creditor must be different participants", nil)
	}
	if c.Amount.Sign() <= 0 {
		return NewError(CodeInvalidInput, "commitment amount must be greater than zero", nil)
	}
	if c.DueAt.IsZero() {
		return NewError(CodeInvalidInput, "commitment due date is required", nil)
	}
	if !c.StartsAt.IsZero() && c.DueAt.Before(c.StartsAt) {
		return NewError(CodeInvalidInput, "commitment due date must not be before its start", nil)
	}
	if c.OriginRef != "" && (strings.TrimSpace(c.OriginRef) == "" || len(c.OriginRef) > maxOriginRefLength) {
		return NewError(CodeInvalidInput, "commitment origin reference must be between 1 and 255 characters and not blank", nil)
	}
	return nil
}

// ValidateCancelReason checks the reason for cancelling a commitment: one to
// 500 characters that are not all blank. Every failure is invalid_input.
func ValidateCancelReason(reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > maxCancelReasonLength {
		return NewError(CodeInvalidInput, "cancellation reason must be between 1 and 500 characters and not blank", nil)
	}
	return nil
}
