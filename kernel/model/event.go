package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// eventTypePattern accepts two or more lowercase segments separated by dots,
// such as kernel.space.created. The version is a column, never part of the type.
var eventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// eventTypeVersionTail matches a final version segment such as v1, which does
// not belong in the type because the version is its own field.
var eventTypeVersionTail = regexp.MustCompile(`\.v[0-9]+$`)

// maxEventTypeLength is the longest accepted event type, in characters.
const maxEventTypeLength = 128

// NewEvent is the input of an event append. The space is never part of it: it
// comes from the transaction, so a caller cannot write into another space.
type NewEvent struct {
	// Type names the fact, for example kernel.space.created.
	Type string
	// Version is the shape version of Payload, starting at 1.
	Version int
	// SubjectRef optionally points at what the event is about.
	SubjectRef string
	// Payload is the event data and must be a JSON object.
	Payload json.RawMessage
	// Actor is who caused the event.
	Actor Actor
	// RequestID optionally ties the event to the request that caused it.
	RequestID string
	// OccurredAt is when the fact happened; zero means the database clock.
	OccurredAt time.Time
}

// Event is a stored event: the input plus the values the kernel assigned.
type Event struct {
	NewEvent
	// Seq is the global sequence number. It grows by allocation order, may have
	// gaps and is not the commit order (ADR-0028).
	Seq int64
	// ID is the UUID v7 of the event.
	ID string
	// SpaceID is the space the event belongs to.
	SpaceID string
}

// Validate checks the event without touching the database. A malformed field is
// invalid_input; an event with no actor is permission_denied, because nothing
// is written anonymously.
func (e NewEvent) Validate() error {
	if len(e.Type) > maxEventTypeLength || !eventTypePattern.MatchString(e.Type) || eventTypeVersionTail.MatchString(e.Type) {
		return NewError(CodeInvalidInput, "event type must be two or more lowercase segments separated by dots and no version suffix", nil)
	}
	if e.Version < 1 {
		return NewError(CodeInvalidInput, "event version must be at least 1", nil)
	}
	if !isJSONObject(e.Payload) {
		return NewError(CodeInvalidInput, "event payload must be a JSON object", nil)
	}
	if !e.Actor.Kind.Valid() {
		return NewError(CodeInvalidInput, fmt.Sprintf("unknown actor kind %q", e.Actor.Kind), nil)
	}
	if e.Actor.ParticipantID != "" {
		if parsed, err := uuid.Parse(e.Actor.ParticipantID); err != nil || parsed == uuid.Nil {
			return NewError(CodeInvalidInput, "actor participant id must be a non-nil UUID", nil)
		}
	} else if e.Actor.Kind != ActorSystem {
		return NewError(CodePermissionDenied, "an event needs an actor", nil)
	}
	if e.SubjectRef != "" && strings.TrimSpace(e.SubjectRef) == "" {
		return NewError(CodeInvalidInput, "event subject reference must not be blank", nil)
	}
	if e.RequestID != "" && strings.TrimSpace(e.RequestID) == "" {
		return NewError(CodeInvalidInput, "event request id must not be blank", nil)
	}
	return nil
}

// isJSONObject reports whether raw is one valid JSON document whose top level
// is an object. Empty input, null, arrays and scalars are not objects.
func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}
