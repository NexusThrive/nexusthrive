package execution

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// commitmentOnly declares one permission, so its Tx may not write exchanges.
var commitmentOnly = Operation{
	Name: "test.commitment_only", Permissions: []string{model.PermissionCommitmentWrite}, Risk: model.RiskWrite,
}

// eventReader declares the read of events and nothing that writes.
var eventReader = Operation{
	Name: "test.event_reader", Permissions: []string{model.PermissionEventRead}, Risk: model.RiskRead,
}

// TestTxRequiresDeclaredPermission guards that each primitive of a Tx needs
// its permission among the ones the operation declared, so that an operation
// cannot do more than it asked for, and that a refusal writes nothing.
func TestTxRequiresDeclaredPermission(t *testing.T) {
	t.Parallel()
	w := newWorld(t, moneyOperation, commitmentOnly, eventReader)
	f := w.ledger(w.spaceA)
	holder := w.holder(w.spaceA, "holder", model.PermissionExchangeWrite, model.PermissionCommitmentWrite, model.PermissionEventRead)
	before := w.snapshot()

	exchange := model.NewExchange{Kind: "test.exchange", Entries: []model.NewEntry{
		{AccountID: f.to, ResourceID: f.resource, Amount: amount(t, "1.00")},
		{AccountID: f.from, ResourceID: f.resource, Amount: amount(t, "1.00").Neg()},
	}}
	err := w.engine.Run(context.Background(), call(holder, w.spaceA), commitmentOnly.Name, func(ctx context.Context, tx *Tx) error {
		_, err := tx.RecordExchange(ctx, exchange)
		requireCode(t, "exchange from a commitment-only operation", err, model.CodePermissionDenied)
		_, err = tx.ReadEvents(ctx, 0, 10)
		requireCode(t, "events from an operation that did not declare the read", err, model.CodePermissionDenied)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("a refused primitive wrote %v", changed)
	}

	err = w.engine.Run(context.Background(), call(holder, w.spaceA), eventReader.Name, func(ctx context.Context, tx *Tx) error {
		if _, err := tx.ReadEvents(ctx, 0, 10); err != nil {
			t.Errorf("the declared read of events: %v", err)
		}
		_, err := tx.CreateCommitment(ctx, model.NewCommitment{})
		requireCode(t, "commitment from a read operation", err, model.CodePermissionDenied)
		_, err = tx.RecordExchange(ctx, exchange)
		requireCode(t, "exchange from a read operation", err, model.CodePermissionDenied)
		return nil
	})
	if err != nil {
		t.Fatalf("Run reader: %v", err)
	}

	err = w.runMoney(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		_, err := tx.ReadEvents(ctx, 0, 10)
		requireCode(t, "events from the money operation", err, model.CodePermissionDenied)
		if _, err := tx.RecordExchange(ctx, exchange); err != nil {
			t.Errorf("the declared exchange write: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run money: %v", err)
	}
}

// TestTxRejectsForeignNamespace guards that an operation writes only inside
// its own namespace: an event or a kind of another namespace, above all the
// kernel one, is refused before it reaches the database, and the near miss of
// a namespace that merely starts with the same letters is refused too.
func TestTxRejectsForeignNamespace(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	before := w.snapshot()

	for _, eventType := range []string{model.EventTypeCommitmentExpired, "kernel.space.created", "other.thing", "testing.thing", "test", "test.thing\n"} {
		err := w.runMoney(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
			_, err := tx.AppendEvent(ctx, EventInput{Type: eventType, Version: 1, Payload: json.RawMessage(`{}`)}, model.SubscriberInternal)
			return err
		})
		if got, typed := model.CodeOf(err); !typed || (got != model.CodeRuleViolation && got != model.CodeInvalidInput) {
			t.Errorf("event type %q: error = %v, want rule_violation or invalid_input", eventType, err)
		}
	}
	for _, kind := range []string{"kernel.exchange.reversal", "other.exchange", "testing.exchange"} {
		err := w.runMoney(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
			_, err := tx.RecordExchange(ctx, model.NewExchange{Kind: kind, Entries: []model.NewEntry{
				{AccountID: f.to, ResourceID: f.resource, Amount: amount(t, "1.00")},
				{AccountID: f.from, ResourceID: f.resource, Amount: amount(t, "1.00").Neg()},
			}})
			return err
		})
		requireCode(t, "exchange kind "+kind, err, model.CodeRuleViolation)
		err = w.runMoney(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
			_, err := tx.CreateCommitment(ctx, model.NewCommitment{
				Kind: kind, DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor, ResourceID: f.resource, Amount: amount(t, "1.00"),
			})
			return err
		})
		requireCode(t, "commitment kind "+kind, err, model.CodeRuleViolation)
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("a refused write changed %v", changed)
	}

	// Control: the own namespace writes.
	err := w.runMoney(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		_, err := tx.AppendEvent(ctx, EventInput{Type: "test.thing", Version: 1, Payload: json.RawMessage(`{}`)})
		return err
	})
	if err != nil {
		t.Errorf("an event of the own namespace: %v", err)
	}
}

// TestTxClosedAfterHandler guards that a Tx captured by the handler is dead
// once the call ends, on success and on error: every method answers
// rule_violation, writes nothing and does not panic.
func TestTxClosedAfterHandler(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")

	var captured []*Tx
	capture := func(returned error) Handler {
		return func(ctx context.Context, tx *Tx) error {
			captured = append(captured, tx)
			return returned
		}
	}
	if err := w.runMoney(context.Background(), call(holder, w.spaceA), moneyOperation.Name, capture(nil)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = w.runMoney(context.Background(), call(holder, w.spaceA), moneyOperation.Name, capture(model.NewError(model.CodeNotFound, "x", nil)))
	if len(captured) != 2 {
		t.Fatalf("captured %d transactions, want 2", len(captured))
	}
	before := w.snapshot()
	for i, tx := range captured {
		ctx := context.Background()
		_, err := tx.RecordExchange(ctx, model.NewExchange{Kind: "test.exchange", Entries: []model.NewEntry{
			{AccountID: f.to, ResourceID: f.resource, Amount: amount(t, "1.00")},
			{AccountID: f.from, ResourceID: f.resource, Amount: amount(t, "1.00").Neg()},
		}})
		requireCode(t, "late exchange", err, model.CodeRuleViolation)
		_, err = tx.CreateCommitment(ctx, model.NewCommitment{Kind: "test.commitment"})
		requireCode(t, "late commitment", err, model.CodeRuleViolation)
		_, err = tx.AppendEvent(ctx, EventInput{Type: "test.thing", Version: 1, Payload: json.RawMessage(`{}`)}, model.SubscriberInternal)
		requireCode(t, "late event", err, model.CodeRuleViolation)
		_, err = tx.ReadEvents(ctx, 0, 1)
		requireCode(t, "late read", err, model.CodeRuleViolation)
		if tx.SpaceID() != w.spaceA || tx.Actor() != holder {
			t.Errorf("transaction %d lost its identity after closing", i)
		}
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("a closed transaction wrote %v", changed)
	}
}

// TestReadOperationCannotWrite guards that a read operation cannot write by
// any road: the Tx refuses, and a raw statement fails in the database because
// the transaction is read only, with the code 25006.
func TestReadOperationCannotWrite(t *testing.T) {
	t.Parallel()
	read := Operation{Name: "test.read_ledger", Permissions: []string{model.PermissionLedgerRead}, Risk: model.RiskRead}
	w := newWorld(t, read)
	holder := w.holder(w.spaceA, "holder", model.PermissionLedgerRead)
	before := w.snapshot()

	var raw error
	err := w.engine.Run(context.Background(), call(holder, w.spaceA), read.Name, func(ctx context.Context, tx *Tx) error {
		_, err := tx.AppendEvent(ctx, EventInput{Type: "test.thing", Version: 1, Payload: json.RawMessage(`{}`)}, model.SubscriberInternal)
		requireCode(t, "event from a read operation", err, model.CodePermissionDenied)

		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		_, raw = tx.conn.ExecContext(ctx,
			`INSERT INTO kernel.events (id, space_id, type, version, payload, actor_participant_id, actor_kind)
			 VALUES ($1, kernel.current_space_id(), 'test.thing', 1, '{}'::jsonb, $2, 'person')`, id.String(), holder.ParticipantID)
		return raw
	})
	if err == nil {
		t.Fatal("a read operation wrote an event with a raw statement")
	}
	if !isPgCode(err, "25006") {
		t.Errorf("raw write error = %v, want read_only_sql_transaction (25006)", err)
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("a read operation changed %v", changed)
	}
}

// TestTxCopyIsClosedToo guards that a Tx copied by value inside the handler
// shares the state of the original: once the call ends the copy is closed as
// well and cannot write in the window before the commit.
func TestTxCopyIsClosedToo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	holder := w.writer(w.spaceA, "holder")
	var copied Tx
	err := w.runMoney(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		copied = *tx
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	before := w.snapshot()
	_, err = copied.AppendEvent(context.Background(), EventInput{Type: "test.thing", Version: 1, Payload: json.RawMessage(`{}`)}, model.SubscriberInternal)
	requireCode(t, "event through a copy", err, model.CodeRuleViolation)
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("a copy of the Tx wrote %v", changed)
	}
}
