package laws_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestLawMemoryHistoryIsAppendOnly guards the law of memory: events, entries
// and fulfillments are never updated or deleted, by the API role and by the
// administrator who owns the tables, and every refused statement leaves the
// table exactly as it was. The positive control is the correction the law
// allows: a compensating exchange adds rows and changes none.
func TestLawMemoryHistoryIsAppendOnly(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")
	ctx := context.Background()
	// The ledger functions of the state package are written by the system actor.
	bookkeeper := model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}

	op := newOperation(t, f)
	if err := w.run(actor, w.spaceA, "request-memory", op.standard()); err != nil {
		t.Fatalf("the operation failed: %v", err)
	}
	var entryID string
	if err := w.admin.QueryRow(`SELECT id::text FROM kernel.entries WHERE exchange_id = $1 AND amount > 0`, op.exchange).Scan(&entryID); err != nil {
		t.Fatalf("read the entry: %v", err)
	}
	if _, _, err := state.FulfillCommitment(ctx, w.api, bookkeeper, w.spaceA, op.commitment, entryID); err != nil {
		t.Fatalf("FulfillCommitment: %v", err)
	}

	attempts := []struct{ table, update, remove string }{
		{"events", `UPDATE kernel.events SET type = 'test.rewritten'`, `DELETE FROM kernel.events`},
		{"entries", `UPDATE kernel.entries SET amount = 1`, `DELETE FROM kernel.entries`},
		{"fulfillments", `UPDATE kernel.fulfillments SET amount = 1`, `DELETE FROM kernel.fulfillments`},
	}
	for _, a := range attempts {
		before := checksum(t, w.admin, a.table)
		if strings.HasPrefix(before, "0:") {
			t.Fatalf("%s has no rows, so the attempts prove nothing", a.table)
		}
		for _, query := range []string{a.update, a.remove} {
			if err := runIn(t, w.api, w.spaceA, stmt{query: query}); err == nil {
				t.Errorf("API: %q succeeded", query)
			}
			if _, err := w.admin.Exec(query); err == nil {
				t.Errorf("administrator: %q succeeded", query)
			}
		}
		if after := checksum(t, w.admin, a.table); after != before {
			t.Errorf("%s changed from %s to %s", a.table, before, after)
		}
	}

	// The correction is a new exchange that mirrors the original.
	entriesBefore, eventsBefore := checksum(t, w.admin, "entries"), checksum(t, w.admin, "events")
	reversal, err := state.ReverseExchange(ctx, w.api, bookkeeper, w.spaceA, op.exchange, "k-memory-reversal")
	if err != nil {
		t.Fatalf("ReverseExchange: %v", err)
	}
	if reversal.ReversesExchangeID != op.exchange {
		t.Errorf("the correction reverses %q, want %s", reversal.ReversesExchangeID, op.exchange)
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM kernel.entries WHERE exchange_id = $1`, op.exchange); got != 2 {
		t.Errorf("the original exchange has %d entries, want 2 untouched", got)
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM kernel.entries`); got != 4 {
		t.Errorf("entries = %d, want the original two and the two of the correction", got)
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM (SELECT account_id FROM kernel.entries GROUP BY account_id HAVING sum(amount) <> 0) x`); got != 0 {
		t.Errorf("%d accounts keep a net amount after the correction, want 0", got)
	}
	if entriesBefore == checksum(t, w.admin, "entries") || eventsBefore != checksum(t, w.admin, "events") {
		t.Errorf("the correction must add entries and leave events as they were")
	}
}
