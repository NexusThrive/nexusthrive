package model

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// maxIdempotencyKeyLength is the longest accepted idempotency key, in characters.
const maxIdempotencyKeyLength = 255

// maxExchangeEntries bounds the entries of one exchange. The balance trigger
// of the database rereads the entries of the exchange for each one it checks,
// so its cost grows with the square of this number.
const maxExchangeEntries = 100

// ReversalKind is the kind of the exchange that corrects another one.
const ReversalKind = "kernel.exchange.reversal"

// NewEntry is one line of an exchange: an amount that enters (positive) or
// leaves (negative) an account of the given resource.
type NewEntry struct {
	AccountID  string
	ResourceID string
	Amount     Amount
}

// NewExchange is the input of an exchange. The space and the actor are never
// part of it: the space comes from the transaction and the actor from the
// operation, so a caller cannot write into another space.
type NewExchange struct {
	// Kind names the exchange with a namespace, for example test.exchange.
	Kind string
	// IdempotencyKey is unique per space; repeating it is rejected.
	IdempotencyKey string
	// ReversesExchangeID is the exchange this one corrects; empty for none.
	ReversesExchangeID string
	// Memo is an optional free note.
	Memo string
	// Metadata is optional extra data and must be a JSON object; empty means {}.
	Metadata json.RawMessage
	// OccurredAt is when the exchange happened; zero means the database clock.
	OccurredAt time.Time
	// Entries are the lines of the exchange; they must sum to zero per resource.
	Entries []NewEntry
}

// Entry is a stored line of an exchange.
type Entry struct {
	ID         string
	ExchangeID string
	AccountID  string
	ResourceID string
	Amount     Amount
}

// Exchange is a stored exchange with its entries.
type Exchange struct {
	ID                 string
	SpaceID            string
	Kind               string
	IdempotencyKey     string
	ActorParticipantID string
	ReversesExchangeID string
	Memo               string
	Metadata           json.RawMessage
	OccurredAt         time.Time
	// RecordedAt is when the kernel stored the exchange, in UTC.
	RecordedAt time.Time
	Entries    []Entry
}

// Validate checks the exchange without touching the database. Every failure is
// invalid_input. Balance per resource is not checked here: it is the law of
// the database, applied when the transaction commits.
func (e NewExchange) Validate() error {
	if len(e.Kind) > maxEventTypeLength || !eventTypePattern.MatchString(e.Kind) {
		return NewError(CodeInvalidInput, "exchange kind must be two or more lowercase segments separated by dots", nil)
	}
	if strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > maxIdempotencyKeyLength {
		return NewError(CodeInvalidInput, "idempotency key must be between 1 and 255 characters and not blank", nil)
	}
	if e.Memo != "" && strings.TrimSpace(e.Memo) == "" {
		return NewError(CodeInvalidInput, "exchange memo must not be blank", nil)
	}
	if len(e.Metadata) > 0 && !isJSONObject(e.Metadata) {
		return NewError(CodeInvalidInput, "exchange metadata must be a JSON object", nil)
	}
	if e.ReversesExchangeID != "" && !isNonNilUUID(e.ReversesExchangeID) {
		return NewError(CodeInvalidInput, "reversed exchange id must be a non-nil UUID", nil)
	}
	if len(e.Entries) < 2 || len(e.Entries) > maxExchangeEntries {
		return NewError(CodeInvalidInput, "an exchange needs between 2 and 100 entries", nil)
	}
	for _, entry := range e.Entries {
		if !isNonNilUUID(entry.AccountID) || !isNonNilUUID(entry.ResourceID) {
			return NewError(CodeInvalidInput, "entry account and resource ids must be non-nil UUIDs", nil)
		}
		if entry.Amount.IsZero() {
			return NewError(CodeInvalidInput, "entry amounts must not be zero", nil)
		}
	}
	return nil
}

// isNonNilUUID reports whether text is a well formed UUID other than the nil one.
func isNonNilUUID(text string) bool {
	parsed, err := uuid.Parse(text)
	return err == nil && parsed != uuid.Nil
}
