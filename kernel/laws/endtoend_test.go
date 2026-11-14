package laws_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
)

// writtenTables are the tables the operation of the tests writes, with the
// steps that have already succeeded when the first insert into the table
// fails. A step is marked done when its call returns, so the entries of an
// exchange and the timer of a commitment belong to the step that creates them.
//
// The exchange is the first write of the standard operation, so for the
// exchanges and entries the test runs the event step before it, which gives
// the rollback something to remove.
var writtenTables = []struct {
	table string
	ran   []string
}{
	{"exchanges", []string{"event"}},
	{"entries", []string{"event"}},
	{"commitments", []string{"exchange"}},
	{"timers", []string{"exchange"}},
	{"events", []string{"exchange", "commitment"}},
	{"outbox", []string{"exchange", "commitment"}},
}

// TestLawEndToEndInjectedFailureLeavesNothing guards criterion 5 of the
// closing story: one operation through the engine records an exchange,
// creates a commitment and writes an event with its outbox row; when the
// database fails on the first insert into any one of those tables, no table of
// the schema keeps a row. Each case proves that the steps before the failure
// ran, so the rollback is what removed them, and the operation succeeds with
// the same inputs once the failure is removed.
func TestLawEndToEndInjectedFailureLeavesNothing(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")

	if _, err := w.admin.Exec(`CREATE FUNCTION kernel.inject_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'failure injected into %', TG_TABLE_NAME; END $$`); err != nil {
		t.Fatalf("create the failing function: %v", err)
	}
	for _, target := range writtenTables {
		if _, err := w.admin.Exec(`CREATE TRIGGER inject_failure BEFORE INSERT ON kernel.` + target.table +
			` FOR EACH ROW EXECUTE FUNCTION kernel.inject_failure()`); err != nil {
			t.Fatalf("%s: create the failing trigger: %v", target.table, err)
		}
		before := w.counts()
		op := newOperation(t, f, "k-inject-"+target.table)
		handler := op.standard()
		if target.table == "exchanges" || target.table == "entries" {
			handler = func(ctx context.Context, tx *execution.Tx) error {
				if err := op.stepEvent(ctx, tx); err != nil {
					return err
				}
				if err := op.stepExchange(ctx, tx, true); err != nil {
					return err
				}
				return op.stepCommitment(ctx, tx, op.f.debtor)
			}
		}
		err := w.run(actor, w.spaceA, "request-inject", handler)
		if err == nil {
			t.Errorf("%s: the operation succeeded although the database failed", target.table)
		} else if !strings.Contains(err.Error(), "failure injected into "+target.table) {
			t.Errorf("%s: the operation failed for another reason: %v", target.table, err)
		}
		for _, step := range target.ran {
			if !op.ran[step] {
				t.Errorf("%s: step %s did not run before the failure, so the rollback is not proven", target.table, step)
			}
		}
		if changed := diff(before, w.counts()); len(changed) != 0 {
			t.Errorf("%s: the failed operation left %v", target.table, changed)
		}
		if _, err := w.admin.Exec(`DROP TRIGGER inject_failure ON kernel.` + target.table); err != nil {
			t.Fatalf("%s: drop the failing trigger: %v", target.table, err)
		}
	}

	// Without an injected failure the same operation writes all six tables, so
	// the failures above came from the injection and not from the operation.
	before := w.counts()
	if err := w.run(actor, w.spaceA, "request-ok", newOperation(t, f, "k-inject-control").standard()); err != nil {
		t.Fatalf("the operation without an injected failure failed: %v", err)
	}
	changed := diff(before, w.counts())
	for _, target := range writtenTables {
		if changed[target.table] == 0 {
			t.Errorf("the operation without a failure did not write %s: %v", target.table, changed)
		}
	}
}
