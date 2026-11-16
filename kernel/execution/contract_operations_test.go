package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	kernelv1 "github.com/nexusthrive/nexusthrive/kernel/gen/kernel/v1"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func contractRequest(t *testing.T, f fixtures) *kernelv1.WriteExchangeRequest {
	t.Helper()
	return &kernelv1.WriteExchangeRequest{Entries: []*kernelv1.EntryInput{
		{AccountId: f.from, ResourceId: f.resource, Amount: "-10.00"},
		{AccountId: f.to, ResourceId: f.resource, Amount: "10.00"},
	}, Commitment: &kernelv1.CommitmentInput{DebtorParticipantId: f.debtor, CreditorParticipantId: f.creditor,
		ResourceId: f.resource, Amount: "10.00", DueAt: timestamppb.New(time.Now().Add(48 * time.Hour))}}
}

func contractCall(t *testing.T, actor model.Actor, space string, req *kernelv1.WriteExchangeRequest) Call {
	t.Helper()
	body, err := protojson.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	c := call(actor, space)
	c.IdempotencyKey, c.Request = "contract-key", body
	return c
}

func TestContractExchangeIsAtomicAndIdempotent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")
	c := contractCall(t, actor, w.spaceA, contractRequest(t, f))
	first, err := w.engine.WriteExchange(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	var response kernelv1.WriteExchangeResponse
	if err := protojson.Unmarshal(first.Body, &response); err != nil {
		t.Fatal(err)
	}
	if response.GetExchangeId() == "" || response.GetCommitmentId() == "" || response.GetEventId() == "" {
		t.Fatal("response lacks created references")
	}
	for table, want := range map[string]int{"exchanges": 1, "entries": 2, "commitments": 1, "events": 1, "outbox": 1, "idempotency_records": 1} {
		if got := w.count("SELECT count(*) FROM kernel." + table); got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
	}
	if got := w.count(`SELECT count(*) FROM kernel.commitments WHERE origin_ref=$1 AND id=$2`, response.GetExchangeId(), response.GetCommitmentId()); got != 1 {
		t.Fatal("commitment does not reference its exchange")
	}
	before := w.snapshot()
	again, err := w.engine.WriteExchange(context.Background(), c)
	if err != nil || !again.Replayed || !bytes.Equal(first.Body, again.Body) {
		t.Fatalf("replay: %v, %v", again, err)
	}
	c.Request = bytes.Replace(c.Request, []byte("10.00"), []byte("11.00"), 1)
	_, err = w.engine.WriteExchange(context.Background(), c)
	requireCode(t, "conflicting request", err, model.CodeIdempotencyConflict)
	if changed := w.changed(before); len(changed) != 0 {
		t.Fatalf("replay or conflict changed rows: %v", changed)
	}
}

func TestContractExchangeRollbackAndPermissions(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")
	for _, permission := range []string{model.PermissionExchangeWrite, model.PermissionCommitmentWrite} {
		partial := w.holder(w.spaceA, "partial_"+permission[7:15], permission)
		before := w.snapshot()
		_, err := w.engine.WriteExchange(context.Background(), contractCall(t, partial, w.spaceA, contractRequest(t, f)))
		requireCode(t, "missing one permission", err, model.CodePermissionDenied)
		if changed := w.changed(before); len(changed) != 0 {
			t.Fatalf("denied write left %v", changed)
		}
	}
	before := w.snapshot()
	req := contractRequest(t, f)
	req.Commitment.CreditorParticipantId = NewRequestID()
	_, err := w.engine.WriteExchange(context.Background(), contractCall(t, actor, w.spaceA, req))
	if err == nil {
		t.Fatal("accepted a missing creditor")
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Fatalf("failed commitment left %v", changed)
	}
	c := contractCall(t, actor, w.spaceA, contractRequest(t, f))
	c.IdempotencyKey = ""
	_, err = w.engine.WriteExchange(context.Background(), c)
	requireCode(t, "missing key", err, model.CodeInvalidInput)
}

func TestContractEventsRemainIsolatedAndReserved(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	actor := w.holder(w.spaceA, "reader_writer", model.PermissionExchangeWrite, model.PermissionCommitmentWrite, model.PermissionEventRead)
	if _, err := w.engine.WriteExchange(context.Background(), contractCall(t, actor, w.spaceA, contractRequest(t, w.ledger(w.spaceA)))); err != nil {
		t.Fatal(err)
	}
	events, err := w.engine.ReadEvents(context.Background(), call(actor, w.spaceA), 0, 100)
	if err != nil || len(events) != 1 || events[0].Type != "kernel.exchange.recorded" {
		t.Fatalf("events=%v, err=%v", events, err)
	}
	other := w.holder(w.spaceB, "other_reader", model.PermissionEventRead)
	events, err = w.engine.ReadEvents(context.Background(), call(other, w.spaceB), 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("cross-space events=%v, err=%v", events, err)
	}
	_, err = w.engine.ReadEvents(context.Background(), call(other, w.spaceA), 0, 100)
	requireCode(t, "actor from another space", err, model.CodePermissionDenied)
	ran := false
	err = w.engine.Run(context.Background(), call(actor, w.spaceA), "kernel.event.read", func(context.Context, *Tx) error { ran = true; return nil })
	requireCode(t, "external kernel callback", err, model.CodeInvalidInput)
	_, err = w.engine.RunIdempotent(context.Background(), contractCall(t, actor, w.spaceA, contractRequest(t, w.ledger(w.spaceB))), "kernel.exchange.write", func(context.Context, *Tx) (json.RawMessage, error) { ran = true; return json.RawMessage(`{}`), nil })
	requireCode(t, "external money callback", err, model.CodeInvalidInput)
	if ran {
		t.Fatal("external callback ran in reserved namespace")
	}
}

func TestContractTransactionAbortsAreRetryable(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		err := classify("kernel.exchange.write", &pgconn.PgError{Code: code})
		requireCode(t, code, err, model.Code("retryable"))
	}
	if _, typed := model.CodeOf(classify("kernel.exchange.write", &pgconn.PgError{Code: "42501"})); typed {
		t.Fatal("missing privilege disguised as domain error")
	}
}

func TestContractInputAndConservationFailuresLeaveNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")
	for name, mutate := range map[string]func(*kernelv1.WriteExchangeRequest){
		"unbalanced":         func(r *kernelv1.WriteExchangeRequest) { r.Entries[0].Amount = "-9.00" },
		"missing commitment": func(r *kernelv1.WriteExchangeRequest) { r.Commitment = nil },
		"missing due date":   func(r *kernelv1.WriteExchangeRequest) { r.Commitment.DueAt = nil },
		"invalid decimal":    func(r *kernelv1.WriteExchangeRequest) { r.Entries[0].Amount = "NaN" },
		"excess scale": func(r *kernelv1.WriteExchangeRequest) {
			r.Entries[0].Amount = "-10.001"
			r.Entries[1].Amount = "10.001"
		},
		"key mismatch": func(r *kernelv1.WriteExchangeRequest) { r.IdempotencyKey = "another-key" },
	} {
		t.Run(name, func(t *testing.T) {
			request := contractRequest(t, f)
			mutate(request)
			before := w.snapshot()
			if _, err := w.engine.WriteExchange(context.Background(), contractCall(t, actor, w.spaceA, request)); err == nil {
				t.Fatal("invalid request succeeded")
			}
			if changed := w.changed(before); len(changed) != 0 {
				t.Fatalf("failed request left %v", changed)
			}
		})
	}
	request := contractRequest(t, f)
	request.IdempotencyKey = "field-key"
	c := contractCall(t, actor, w.spaceA, request)
	c.IdempotencyKey = ""
	if _, err := w.engine.WriteExchange(context.Background(), c); err != nil {
		t.Fatalf("contract field key failed: %v", err)
	}
}
