package laws_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestLawConservationUnbalancedExchangeRejectedAtCommit guards the law of
// conservation: the entries of an exchange add up to zero per resource, and
// the database refuses the COMMIT of an exchange that does not, leaving no
// row of it. The statements themselves succeed, so the refusal comes from the
// check deferred to the end of the transaction. A balanced exchange in the
// same space is the positive control.
func TestLawConservationUnbalancedExchangeRejectedAtCommit(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	ctx := context.Background()

	unit, err := state.CreateResource(ctx, w.api, system, w.spaceA, state.NewResource{Kind: model.ResourceKindMoney, Code: "UNT", Name: "Unit", Scale: 0})
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}
	unitAccount, err := state.OpenAccount(ctx, w.api, system, w.spaceA, state.NewAccount{
		HolderParticipantID: f.creditor, ResourceID: unit, Class: model.AccountClassAsset, Code: "UNIT", Name: "UNIT"})
	if err != nil {
		t.Fatalf("OpenAccount: %v", err)
	}

	cases := []struct {
		name    string
		entries [][3]string
	}{
		{"credit larger than debit", [][3]string{{f.to, f.resource, "10.00"}, {f.from, f.resource, "-5.00"}}},
		{"debit larger than credit", [][3]string{{f.to, f.resource, "5.00"}, {f.from, f.resource, "-10.00"}}},
		{"three entries off by one cent", [][3]string{{f.to, f.resource, "10.00"}, {f.from, f.resource, "-5.00"}, {f.from, f.resource, "-4.99"}}},
		// Zero in total, but not per resource: each resource is short on its own.
		{"zero in total and not per resource", [][3]string{{f.to, f.resource, "10.00"}, {unitAccount, unit, "-10"}}},
	}
	for i, c := range cases {
		before := w.counts()
		stmts := exchangeStmts(t, w.spaceA, "k-unbalanced-"+strconv.Itoa(i), f.creditor, c.entries...)
		if err := runIn(t, w.api, w.spaceA, stmts...); err == nil {
			t.Errorf("%s: the commit succeeded", c.name)
		}
		if changed := diff(before, w.counts()); len(changed) != 0 {
			t.Errorf("%s: the rejected exchange left %v", c.name, changed)
		}
	}

	before := w.counts()
	balanced := exchangeStmts(t, w.spaceA, "k-balanced", f.creditor,
		[3]string{f.to, f.resource, "10.00"}, [3]string{f.from, f.resource, "-10.00"})
	if err := runIn(t, w.api, w.spaceA, balanced...); err != nil {
		t.Fatalf("a balanced exchange failed: %v", err)
	}
	if changed := diff(before, w.counts()); changed["exchanges"] != 1 || changed["entries"] != 2 || len(changed) != 2 {
		t.Errorf("a balanced exchange changed %v, want one exchange and two entries", changed)
	}
}
