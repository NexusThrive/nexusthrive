package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// EventInput is what a handler gives to append an event. It has no actor and
// no request id: the Tx stamps both from the call, so a handler cannot
// attribute an event to someone else.
type EventInput struct {
	// Type must start with the namespace of the operation and a dot.
	Type       string
	Version    int
	SubjectRef string
	Payload    json.RawMessage
	// OccurredAt is when the fact happened; zero means the database clock.
	OccurredAt time.Time
}

// Tx is the handle a handler gets for the transaction of its call. It exposes
// only state primitives, with no SQL and no *sql.Tx, and it is valid only while
// the handler runs: after that every method fails with rule_violation. It
// carries the verified actor, the space and the request id of the call.
type Tx struct {
	// life is shared by every copy of the Tx, so that closing the original
	// closes a copy made by value inside the handler too.
	life  *txLife
	conn  *sql.Tx
	clock model.Clock
	op    Operation
	call  Call
}

// txLife is the lifetime of a Tx: its mutex serializes the use of the Tx and
// the closing of it, so that a goroutine that outlives the handler cannot
// write while the transaction commits.
type txLife struct {
	mu     sync.Mutex
	closed bool
}

// SpaceID returns the space of the call.
func (t *Tx) SpaceID() string { return t.call.SpaceID }

// Actor returns the actor of the call.
func (t *Tx) Actor() model.Actor { return t.call.Actor }

// RequestID returns the request id of the call.
func (t *Tx) RequestID() string { return t.call.RequestID }

// run executes the handler and closes the Tx when it ends, whether it returned,
// failed or panicked.
func (t *Tx) run(ctx context.Context, fn Handler) error {
	defer t.close()
	return fn(ctx, t)
}

// close marks the Tx as finished. It waits for a method that is still running.
func (t *Tx) close() {
	t.life.mu.Lock()
	defer t.life.mu.Unlock()
	t.life.closed = true
}

// begin takes the lock and refuses a closed Tx. The caller unlocks.
func (t *Tx) begin() error {
	t.life.mu.Lock()
	if t.life.closed {
		t.life.mu.Unlock()
		return model.NewError(model.CodeRuleViolation, "the transaction of the operation has ended", nil)
	}
	return nil
}

// requireDeclared is permission_denied unless the operation declared the
// permission: a primitive never does more than the operation asked for.
func (t *Tx) requireDeclared(key string) error {
	if !t.op.declares(key) {
		return model.NewError(model.CodePermissionDenied,
			fmt.Sprintf("operation %q did not declare %s", t.op.Name, key), nil)
	}
	return nil
}

// requireNamespace is rule_violation unless value starts with the namespace of
// the operation and a dot, so an operation of an extension cannot write what
// belongs to the kernel or to another extension.
func (t *Tx) requireNamespace(what, value string) error {
	if !strings.HasPrefix(value, t.op.namespace()+".") {
		return model.NewError(model.CodeRuleViolation,
			fmt.Sprintf("%s %q is outside the namespace %q of the operation", what, value, t.op.namespace()), nil)
	}
	return nil
}

// RecordExchange writes one exchange and its entries as the actor of the call.
// The operation must have declared kernel.exchange.write, and the kind of the
// exchange must start with the namespace of the operation.
func (t *Tx) RecordExchange(ctx context.Context, in model.NewExchange) (model.Exchange, error) {
	if err := t.begin(); err != nil {
		return model.Exchange{}, err
	}
	defer t.life.mu.Unlock()
	if err := t.requireDeclared(model.PermissionExchangeWrite); err != nil {
		return model.Exchange{}, err
	}
	if err := t.requireNamespace("exchange kind", in.Kind); err != nil {
		return model.Exchange{}, err
	}
	return state.AppendExchange(ctx, t.conn, t.call.Actor, in)
}

// CreateCommitment writes one open commitment as the actor of the call. The
// operation must have declared kernel.commitment.write, and the kind of the
// commitment must start with the namespace of the operation.
func (t *Tx) CreateCommitment(ctx context.Context, in model.NewCommitment) (model.Commitment, error) {
	if err := t.begin(); err != nil {
		return model.Commitment{}, err
	}
	defer t.life.mu.Unlock()
	if err := t.requireDeclared(model.PermissionCommitmentWrite); err != nil {
		return model.Commitment{}, err
	}
	if err := t.requireNamespace("commitment kind", in.Kind); err != nil {
		return model.Commitment{}, err
	}
	return state.AppendCommitment(ctx, t.conn, t.call.Actor, in)
}

// AppendEvent writes one event, stamped with the actor and the request id of
// the call, and one outbox row for each subscriber. Only a write or money
// operation may call it, and the type of the event must start with the
// namespace of the operation.
func (t *Tx) AppendEvent(ctx context.Context, in EventInput, subs ...model.Subscriber) (model.Event, error) {
	if err := t.begin(); err != nil {
		return model.Event{}, err
	}
	defer t.life.mu.Unlock()
	if t.op.Risk == model.RiskRead {
		return model.Event{}, model.NewError(model.CodePermissionDenied, "a read operation cannot write events", nil)
	}
	if err := t.requireNamespace("event type", in.Type); err != nil {
		return model.Event{}, err
	}
	return state.AppendEventAndEnqueue(ctx, t.conn, t.clock, model.NewEvent{
		Type: in.Type, Version: in.Version, SubjectRef: in.SubjectRef, Payload: in.Payload,
		Actor: t.call.Actor, RequestID: t.call.RequestID, OccurredAt: in.OccurredAt,
	}, subs...)
}

// ReadEvents returns up to limit events of the space with seq of at least
// fromSeq. The operation must have declared kernel.event.read.
func (t *Tx) ReadEvents(ctx context.Context, fromSeq int64, limit int) ([]model.Event, error) {
	if err := t.begin(); err != nil {
		return nil, err
	}
	defer t.life.mu.Unlock()
	if err := t.requireDeclared(model.PermissionEventRead); err != nil {
		return nil, err
	}
	return state.ReadEventsTx(ctx, t.conn, fromSeq, limit)
}

// fireDueTimers runs a firing round. It is not exported: only the built-in
// operation of the kernel, whose permission was verified, reaches it.
func (t *Tx) fireDueTimers(ctx context.Context, limit int) (model.TimerRun, error) {
	if err := t.begin(); err != nil {
		return model.TimerRun{}, err
	}
	defer t.life.mu.Unlock()
	return state.FireDueTimersTx(ctx, t.conn, t.clock, t.call.Actor, limit)
}

// claimOutbox claims outbox rows. It is not exported for the same reason as
// fireDueTimers.
func (t *Tx) claimOutbox(ctx context.Context, s model.Subscriber, limit int) ([]model.OutboxRow, error) {
	if err := t.begin(); err != nil {
		return nil, err
	}
	defer t.life.mu.Unlock()
	return state.ClaimOutboxTx(ctx, t.conn, t.clock, s, limit)
}
