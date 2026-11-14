package state_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// commitmentFixtures extends the ledger fixtures with a debtor in space A. The
// creditor is the system participant, who holds every account of the ledger.
type commitmentFixtures struct {
	ledger
	debtor string
}

// commitmentSetup seeds the ledger and a debtor.
func commitmentSetup(t *testing.T) commitmentFixtures {
	t.Helper()
	l := exchangeSetup(t)
	return commitmentFixtures{ledger: l, debtor: mustParticipant(t, l.api, spaceA, newPerson("", "Debtor"))}
}

// newCommitment builds a commitment of value in the first resource, due in a
// month.
func (c commitmentFixtures) newCommitment(t *testing.T, value string) model.NewCommitment {
	t.Helper()
	return model.NewCommitment{
		Kind: "test.commitment", DebtorParticipantID: c.debtor, CreditorParticipantID: c.actor.ParticipantID,
		ResourceID: c.cop, Amount: amount(t, value), DueAt: time.Now().Add(30 * 24 * time.Hour),
	}
}

// create creates a commitment in space A and fails the test on error.
func (c commitmentFixtures) create(t *testing.T, value string) model.Commitment {
	t.Helper()
	got, err := state.CreateCommitment(context.Background(), c.api, c.actor, spaceA, c.newCommitment(t, value))
	if err != nil {
		t.Fatalf("CreateCommitment(%s): %v", value, err)
	}
	return got
}

// credit records an exchange that enters value into an account of the creditor
// and returns the id of the positive entry.
func (c commitmentFixtures) credit(t *testing.T, key, value string) string {
	t.Helper()
	return c.record(t, c.pair(t, key, c.a1, c.a2, c.cop, value)).Entries[0].ID
}

// fulfill applies the entry to the commitment in space A.
func (c commitmentFixtures) fulfill(commitment, entry string) (model.Fulfillment, model.Commitment, error) {
	return state.FulfillCommitment(context.Background(), c.api, c.actor, spaceA, commitment, entry)
}

// mustFulfill is fulfill that fails the test on error.
func (c commitmentFixtures) mustFulfill(t *testing.T, commitment, entry string) (model.Fulfillment, model.Commitment) {
	t.Helper()
	fulfillment, got, err := c.fulfill(commitment, entry)
	if err != nil {
		t.Fatalf("FulfillCommitment: %v", err)
	}
	return fulfillment, got
}

// get reads a commitment of space A.
func (c commitmentFixtures) get(t *testing.T, id string) model.Commitment {
	t.Helper()
	got, err := state.GetCommitment(context.Background(), c.api, spaceA, id)
	if err != nil {
		t.Fatalf("GetCommitment: %v", err)
	}
	return got
}

// rows counts the rows of both commitment tables.
func (c commitmentFixtures) rows(t *testing.T) (commitments, fulfillments int) {
	t.Helper()
	return count(t, c.admin, `SELECT count(*) FROM kernel.commitments`), count(t, c.admin, `SELECT count(*) FROM kernel.fulfillments`)
}

// requireAmount fails unless got equals the decimal text want.
func requireAmount(t *testing.T, label string, got model.Amount, want string) {
	t.Helper()
	if !got.Equal(amount(t, want)) {
		t.Errorf("%s = %s, want %s", label, got, want)
	}
}

// TestCommitmentCreatedOpenWithZeroFulfilled guards criterion 1.
func TestCommitmentCreatedOpenWithZeroFulfilled(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)

	in := c.newCommitment(t, "100.00")
	in.OriginRef = "test.origin/1"
	before := time.Now().Add(-time.Second)
	got, err := state.CreateCommitment(context.Background(), c.api, c.actor, spaceA, in)
	if err != nil {
		t.Fatalf("CreateCommitment: %v", err)
	}
	if got.ID == "" || got.SpaceID != spaceA || got.Kind != "test.commitment" || got.DebtorParticipantID != c.debtor ||
		got.CreditorParticipantID != c.actor.ParticipantID || got.ResourceID != c.cop || got.OriginRef != "test.origin/1" ||
		got.CreatedByParticipantID != c.actor.ParticipantID || got.CanceledReason != "" {
		t.Errorf("commitment = %+v", got)
	}
	if got.Status != model.CommitmentStatusOpen {
		t.Errorf("status = %s, want open", got.Status)
	}
	requireAmount(t, "amount", got.Amount, "100")
	requireAmount(t, "fulfilled amount", got.FulfilledAmount, "0")
	if !got.ClosedAt.IsZero() {
		t.Errorf("ClosedAt = %v, want zero while open", got.ClosedAt)
	}
	for name, at := range map[string]time.Time{"CreatedAt": got.CreatedAt, "StartsAt": got.StartsAt, "DueAt": got.DueAt} {
		if at.Location() != time.UTC {
			t.Errorf("%s must be UTC, got %v", name, at.Location())
		}
	}
	if got.CreatedAt.Before(before) || got.CreatedAt.After(time.Now().Add(time.Second)) || got.StartsAt.Before(before) {
		t.Errorf("CreatedAt %v and StartsAt %v must be the database time", got.CreatedAt, got.StartsAt)
	}
	if !got.DueAt.Equal(in.DueAt.Truncate(time.Microsecond)) {
		t.Errorf("DueAt = %v, want %v", got.DueAt, in.DueAt)
	}
	if again := c.get(t, got.ID); again.ID != got.ID || again.Status != model.CommitmentStatusOpen || len(again.Fulfillments) != 0 {
		t.Errorf("GetCommitment = %+v", again)
	}
}

func TestCreateCommitmentWithStartDate(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)

	in := c.newCommitment(t, "10.00")
	in.StartsAt = time.Now().Add(24 * time.Hour)
	in.DueAt = in.StartsAt.Add(24 * time.Hour)
	got, err := state.CreateCommitment(context.Background(), c.api, c.actor, spaceA, in)
	if err != nil {
		t.Fatalf("CreateCommitment: %v", err)
	}
	if !got.StartsAt.Equal(in.StartsAt.Truncate(time.Microsecond)) {
		t.Errorf("StartsAt = %v, want %v", got.StartsAt, in.StartsAt)
	}
}

// TestCreateCommitmentInvalidInput guards criterion 3: bad input is
// invalid_input and writes nothing; a reference to another space is a rule
// violation.
func TestCreateCommitmentInvalidInput(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()

	for name, mutate := range map[string]func(*model.NewCommitment){
		"zero amount":      func(in *model.NewCommitment) { in.Amount = amount(t, "0") },
		"negative amount":  func(in *model.NewCommitment) { in.Amount = amount(t, "-5") },
		"due before start": func(in *model.NewCommitment) { in.StartsAt = in.DueAt.Add(time.Hour) },
		"same parties":     func(in *model.NewCommitment) { in.CreditorParticipantID = in.DebtorParticipantID },
		"no due date":      func(in *model.NewCommitment) { in.DueAt = time.Time{} },
		"bad kind":         func(in *model.NewCommitment) { in.Kind = "Commitment" },
		"too many decimals": func(in *model.NewCommitment) {
			in.Amount = amount(t, "10.001")
		},
		"fractional units": func(in *model.NewCommitment) { in.ResourceID = c.unit; in.Amount = amount(t, "1.5") },
	} {
		in := c.newCommitment(t, "10.00")
		mutate(&in)
		_, err := state.CreateCommitment(ctx, c.api, c.actor, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	for name, mutate := range map[string]func(*model.NewCommitment){
		"debtor of another space":   func(in *model.NewCommitment) { in.DebtorParticipantID = c.actorB.ParticipantID },
		"creditor of another space": func(in *model.NewCommitment) { in.CreditorParticipantID = c.actorB.ParticipantID },
		"resource of another space": func(in *model.NewCommitment) { in.ResourceID = c.resourceB },
		"unknown debtor":            func(in *model.NewCommitment) { in.DebtorParticipantID = "01900000-0000-7000-8000-00000000ffff" },
	} {
		in := c.newCommitment(t, "10.00")
		mutate(&in)
		_, err := state.CreateCommitment(ctx, c.api, c.actor, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeRuleViolation {
			t.Errorf("%s: error = %v, want rule_violation", name, err)
		}
	}
	// A space that does not exist is not_found.
	_, err := state.CreateCommitment(ctx, c.api, c.actor, "01900000-0000-7000-8000-00000000ffff", c.newCommitment(t, "10.00"))
	if err == nil {
		t.Error("a commitment in an unknown space was accepted")
	}
	if n, _ := c.rows(t); n != 0 {
		t.Errorf("%d commitments after the refused ones, want 0", n)
	}
}

// TestCommitmentOperationsNeedASystemActorAndLeaveNothingWhenDenied guards that
// a denied operation leaves no rows and an operation with no current space
// writes nothing.
func TestCommitmentOperationsNeedASystemActorAndLeaveNothingWhenDenied(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()
	open := c.create(t, "100.00")
	entry := c.credit(t, "k1", "40.00")

	for name, actor := range map[string]model.Actor{
		"anonymous":            {},
		"user":                 {Kind: model.ActorPerson, ParticipantID: c.actor.ParticipantID},
		"system without actor": {Kind: model.ActorSystem},
	} {
		if _, err := state.CreateCommitment(ctx, c.api, actor, spaceA, c.newCommitment(t, "5.00")); !isCode(err, model.CodePermissionDenied) {
			t.Errorf("%s create: error = %v, want permission_denied", name, err)
		}
		if _, _, err := state.FulfillCommitment(ctx, c.api, actor, spaceA, open.ID, entry); !isCode(err, model.CodePermissionDenied) {
			t.Errorf("%s fulfill: error = %v, want permission_denied", name, err)
		}
		if _, err := state.CancelCommitment(ctx, c.api, actor, spaceA, open.ID, "why"); !isCode(err, model.CodePermissionDenied) {
			t.Errorf("%s cancel: error = %v, want permission_denied", name, err)
		}
	}
	if commitments, fulfillments := c.rows(t); commitments != 1 || fulfillments != 0 {
		t.Errorf("rows = %d commitments, %d fulfillments, want 1 and 0", commitments, fulfillments)
	}
	if got := c.get(t, open.ID); got.Status != model.CommitmentStatusOpen {
		t.Errorf("status = %s, want open", got.Status)
	}

	// A transaction with no current space gets rule_violation before any write.
	tx, err := c.api.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := state.AppendCommitment(ctx, tx, c.actor, c.newCommitment(t, "5.00")); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("append without a space: error = %v, want rule_violation", err)
	}
}

// isCode reports whether err is a typed error with the given code.
func isCode(err error, want model.Code) bool {
	code, ok := model.CodeOf(err)
	return ok && code == want
}

// TestAppendCommitmentLivesAndDiesWithTheTransaction guards one operation, one
// transaction: the Append functions never commit or roll back by themselves.
func TestAppendCommitmentLivesAndDiesWithTheTransaction(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()

	err := state.InSpace(ctx, c.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := state.AppendCommitment(ctx, tx, c.actor, c.newCommitment(t, "10.00")); err != nil {
			return err
		}
		return model.NewError(model.CodeRuleViolation, "the rest of the operation failed", nil)
	})
	if !isCode(err, model.CodeRuleViolation) {
		t.Fatalf("error = %v", err)
	}
	if n, _ := c.rows(t); n != 0 {
		t.Errorf("%d commitments after a rollback, want 0", n)
	}
	// Two operations in one transaction commit together.
	var created model.Commitment
	entry := c.credit(t, "k1", "40.00")
	err = state.InSpace(ctx, c.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if created, err = state.AppendCommitment(ctx, tx, c.actor, c.newCommitment(t, "40.00")); err != nil {
			return err
		}
		_, updated, err := state.AppendFulfillment(ctx, tx, c.actor, created.ID, entry)
		if err != nil {
			return err
		}
		if updated.Status != model.CommitmentStatusFulfilled {
			t.Errorf("status inside the transaction = %s, want fulfilled", updated.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("one transaction: %v", err)
	}
	if got := c.get(t, created.ID); got.Status != model.CommitmentStatusFulfilled {
		t.Errorf("status = %s, want fulfilled", got.Status)
	}
}

// TestFulfillPartialKeepsOpen guards criterion 5.
func TestFulfillPartialKeepsOpen(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	entry := c.credit(t, "k1", "40.00")

	fulfillment, got := c.mustFulfill(t, open.ID, entry)
	if fulfillment.ID == "" || fulfillment.CommitmentID != open.ID || fulfillment.EntryID != entry ||
		fulfillment.SpaceID != spaceA || fulfillment.ResourceID != c.cop || fulfillment.CreatedAt.Location() != time.UTC {
		t.Errorf("fulfillment = %+v", fulfillment)
	}
	requireAmount(t, "fulfillment amount", fulfillment.Amount, "40")
	if got.Status != model.CommitmentStatusOpen || !got.ClosedAt.IsZero() {
		t.Errorf("commitment = %s closed %v, want open", got.Status, got.ClosedAt)
	}
	requireAmount(t, "fulfilled amount", got.FulfilledAmount, "40")
	if n := count(t, c.admin, `SELECT count(*) FROM kernel.fulfillments WHERE commitment_id = $1`, open.ID); n != 1 {
		t.Errorf("%d fulfillments, want 1", n)
	}
	read := c.get(t, open.ID)
	if len(read.Fulfillments) != 1 || read.Fulfillments[0].ID != fulfillment.ID {
		t.Errorf("GetCommitment fulfillments = %+v", read.Fulfillments)
	}
	requireAmount(t, "stored fulfilled amount", read.FulfilledAmount, "40")
}

// TestFulfillRemainderClosesCommitment guards criterion 6.
func TestFulfillRemainderClosesCommitment(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	first, second := c.credit(t, "k1", "40.00"), c.credit(t, "k2", "60.00")

	c.mustFulfill(t, open.ID, first)
	before := time.Now().Add(-time.Second)
	_, got := c.mustFulfill(t, open.ID, second)
	after := time.Now().Add(time.Second)

	if got.Status != model.CommitmentStatusFulfilled {
		t.Errorf("status = %s, want fulfilled", got.Status)
	}
	requireAmount(t, "fulfilled amount", got.FulfilledAmount, "100")
	if got.ClosedAt.IsZero() || got.ClosedAt.Before(before) || got.ClosedAt.After(after) || got.ClosedAt.Location() != time.UTC {
		t.Errorf("ClosedAt = %v, want between %v and %v in UTC", got.ClosedAt, before, after)
	}
	read := c.get(t, open.ID)
	sum := model.Amount{}
	for _, fulfillment := range read.Fulfillments {
		sum = sum.Add(fulfillment.Amount)
	}
	requireAmount(t, "sum of fulfillments", sum, "100")
	if read.Status != model.CommitmentStatusFulfilled || len(read.Fulfillments) != 2 {
		t.Errorf("stored commitment = %s with %d fulfillments", read.Status, len(read.Fulfillments))
	}
}

// TestFulfillBeyondPendingIsRuleViolationAndWritesNothing guards criterion 7.
func TestFulfillBeyondPendingIsRuleViolationAndWritesNothing(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	c.mustFulfill(t, open.ID, c.credit(t, "k1", "40.00"))
	tooMuch := c.credit(t, "k2", "70.00")

	_, _, err := c.fulfill(open.ID, tooMuch)
	if !isCode(err, model.CodeRuleViolation) {
		t.Fatalf("error = %v, want rule_violation", err)
	}
	if _, fulfillments := c.rows(t); fulfillments != 1 {
		t.Errorf("%d fulfillments, want only the first", fulfillments)
	}
	read := c.get(t, open.ID)
	requireAmount(t, "fulfilled amount", read.FulfilledAmount, "40")
	if read.Status != model.CommitmentStatusOpen {
		t.Errorf("status = %s, want open", read.Status)
	}
	// The exact pending amount is accepted.
	c.mustFulfill(t, open.ID, c.credit(t, "k3", "60.00"))
}

func TestFulfillUnknownOrInvalidIsTyped(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	entry := c.credit(t, "k1", "40.00")
	const unknown = "01900000-0000-7000-8000-00000000ffff"

	for name, tc := range map[string]struct {
		commitment, entry string
		want              model.Code
	}{
		"unknown commitment": {unknown, entry, model.CodeNotFound},
		"unknown entry":      {open.ID, unknown, model.CodeRuleViolation},
		"bad commitment id":  {"nope", entry, model.CodeInvalidInput},
		"bad entry id":       {open.ID, "", model.CodeInvalidInput},
	} {
		if _, _, err := c.fulfill(tc.commitment, tc.entry); !isCode(err, tc.want) {
			t.Errorf("%s: error = %v, want %s", name, err, tc.want)
		}
	}
	if _, fulfillments := c.rows(t); fulfillments != 0 {
		t.Errorf("%d fulfillments, want 0", fulfillments)
	}
	if _, err := state.GetCommitment(context.Background(), c.api, spaceA, unknown); !isCode(err, model.CodeNotFound) {
		t.Errorf("GetCommitment of an unknown id: %v, want not_found", err)
	}
	if _, err := state.GetCommitment(context.Background(), c.api, spaceA, "nope"); !isCode(err, model.CodeInvalidInput) {
		t.Errorf("GetCommitment of a bad id: %v, want invalid_input", err)
	}
	if _, err := state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, unknown, "why"); !isCode(err, model.CodeNotFound) {
		t.Errorf("CancelCommitment of an unknown id: %v, want not_found", err)
	}
}

// TestFulfillWithEntryOfOtherResourceIsRuleViolation guards criterion 8.
func TestFulfillWithEntryOfOtherResourceIsRuleViolation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	unit := c.record(t, c.pair(t, "k-unit", c.u1, c.u2, c.unit, "10")).Entries[0].ID

	if _, _, err := c.fulfill(open.ID, unit); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("error = %v, want rule_violation", err)
	}
	if _, fulfillments := c.rows(t); fulfillments != 0 {
		t.Errorf("%d fulfillments, want 0", fulfillments)
	}
}

// TestFulfillWithEntryOfOtherSpaceIsRuleViolation guards criterion 8 with a
// real entry of space B.
func TestFulfillWithEntryOfOtherSpaceIsRuleViolation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	foreign, err := state.RecordExchange(context.Background(), c.api, c.actorB, spaceB, model.NewExchange{
		Kind: "test.exchange", IdempotencyKey: "kb",
		Entries: []model.NewEntry{
			{AccountID: c.b1, ResourceID: c.resourceB, Amount: amount(t, "40.00")},
			{AccountID: c.b2, ResourceID: c.resourceB, Amount: amount(t, "-40.00")},
		}})
	if err != nil {
		t.Fatalf("RecordExchange in space B: %v", err)
	}
	if _, _, err := c.fulfill(open.ID, foreign.Entries[0].ID); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("error = %v, want rule_violation", err)
	}
	if _, fulfillments := c.rows(t); fulfillments != 0 {
		t.Errorf("%d fulfillments, want 0", fulfillments)
	}
}

// TestFulfillWithDebitEntryOrNonCreditorEntryIsRuleViolation guards the stricter
// rule of ADR-0033: only an entry that enters an account of the creditor counts.
func TestFulfillWithDebitEntryOrNonCreditorEntryIsRuleViolation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	exchange := c.record(t, c.pair(t, "k1", c.a1, c.a2, c.cop, "40.00"))
	debit := exchange.Entries[1].ID
	debtorAccount := mustAccount(t, c.api, spaceA, state.NewAccount{
		HolderParticipantID: c.debtor, ResourceID: c.cop, Class: model.AccountClassAsset, Code: "DEBTOR", Name: "Debtor"})
	other := c.record(t, c.pair(t, "k2", debtorAccount, c.a2, c.cop, "40.00")).Entries[0].ID

	for name, entry := range map[string]string{"debit entry": debit, "entry of the debtor's account": other} {
		if _, _, err := c.fulfill(open.ID, entry); !isCode(err, model.CodeRuleViolation) {
			t.Errorf("%s: error = %v, want rule_violation", name, err)
		}
	}
	if _, fulfillments := c.rows(t); fulfillments != 0 {
		t.Errorf("%d fulfillments, want 0", fulfillments)
	}
}

// TestFulfillWithReversalEntryIsRuleViolation guards that an entry of a
// compensation cannot pay a debt.
func TestFulfillWithReversalEntryIsRuleViolation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	original := c.record(t, c.pair(t, "k1", c.a1, c.a2, c.cop, "40.00"))
	reversal, err := state.ReverseExchange(context.Background(), c.api, c.actor, spaceA, original.ID, "k1-reversal")
	if err != nil {
		t.Fatalf("ReverseExchange: %v", err)
	}
	var positive string
	for _, entry := range reversal.Entries {
		if entry.Amount.Sign() > 0 {
			positive = entry.ID
		}
	}
	if _, _, err := c.fulfill(open.ID, positive); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("error = %v, want rule_violation", err)
	}
	if _, fulfillments := c.rows(t); fulfillments != 0 {
		t.Errorf("%d fulfillments, want 0", fulfillments)
	}
}

// TestEntryAppliesOnlyOnce guards criterion 8: an entry fulfills one
// commitment, once, also when two transactions try at the same time.
func TestEntryAppliesOnlyOnce(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	first, second := c.create(t, "100.00"), c.create(t, "100.00")
	entry := c.credit(t, "k1", "40.00")

	c.mustFulfill(t, first.ID, entry)
	if _, _, err := c.fulfill(first.ID, entry); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("same entry, same commitment: error = %v, want rule_violation", err)
	}
	if _, _, err := c.fulfill(second.ID, entry); !isCode(err, model.CodeRuleViolation) {
		t.Errorf("same entry, another commitment: error = %v, want rule_violation", err)
	}
	if _, fulfillments := c.rows(t); fulfillments != 1 {
		t.Errorf("%d fulfillments, want 1", fulfillments)
	}

	// Concurrently, on two fresh commitments: the unique index has one winner.
	third, fourth := c.create(t, "100.00"), c.create(t, "100.00")
	raced := c.credit(t, "k2", "40.00")
	results := runConcurrently(
		func() error { _, _, err := c.fulfill(third.ID, raced); return err },
		func() error { _, _, err := c.fulfill(fourth.ID, raced); return err },
	)
	requireOneWinner(t, results, model.CodeRuleViolation)
	if n := count(t, c.admin, `SELECT count(*) FROM kernel.fulfillments WHERE entry_id = $1`, raced); n != 1 {
		t.Errorf("the entry is applied %d times, want 1", n)
	}
}

// TestCancelOpenCommitment guards criterion 9.
func TestCancelOpenCommitment(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")

	before := time.Now().Add(-time.Second)
	got, err := state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, open.ID, "customer asked")
	if err != nil {
		t.Fatalf("CancelCommitment: %v", err)
	}
	if got.Status != model.CommitmentStatusCanceled || got.CanceledReason != "customer asked" {
		t.Errorf("commitment = %s %q, want canceled with the reason", got.Status, got.CanceledReason)
	}
	if got.ClosedAt.IsZero() || got.ClosedAt.Before(before) || got.ClosedAt.Location() != time.UTC {
		t.Errorf("ClosedAt = %v, want the cancellation time", got.ClosedAt)
	}
	if read := c.get(t, open.ID); read.Status != model.CommitmentStatusCanceled || read.CanceledReason != "customer asked" {
		t.Errorf("stored commitment = %s %q", read.Status, read.CanceledReason)
	}

	// A partially fulfilled commitment keeps what was fulfilled.
	partial := c.create(t, "100.00")
	c.mustFulfill(t, partial.ID, c.credit(t, "k1", "40.00"))
	got, err = state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, partial.ID, "stopped")
	if err != nil {
		t.Fatalf("CancelCommitment of a partial one: %v", err)
	}
	requireAmount(t, "fulfilled amount after cancel", got.FulfilledAmount, "40")
	if got.Status != model.CommitmentStatusCanceled {
		t.Errorf("status = %s, want canceled", got.Status)
	}
}

// TestCancelWithoutReasonIsInvalidInput guards criterion 9.
func TestCancelWithoutReasonIsInvalidInput(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")

	for name, reason := range map[string]string{"empty": "", "blank": "   ", "too long": strings.Repeat("r", 501)} {
		if _, err := state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, open.ID, reason); !isCode(err, model.CodeInvalidInput) {
			t.Errorf("%s reason: error = %v, want invalid_input", name, err)
		}
	}
	if got := c.get(t, open.ID); got.Status != model.CommitmentStatusOpen || got.CanceledReason != "" {
		t.Errorf("commitment = %s %q, want untouched", got.Status, got.CanceledReason)
	}
}

// closedCommitments returns one commitment in each terminal state.
func (c commitmentFixtures) closedCommitments(t *testing.T) map[model.CommitmentStatus]string {
	t.Helper()
	fulfilled := c.create(t, "40.00")
	c.mustFulfill(t, fulfilled.ID, c.credit(t, "k-closed", "40.00"))
	canceled := c.create(t, "40.00")
	if _, err := state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, canceled.ID, "stopped"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	expired := c.create(t, "40.00")
	// Expiry belongs to the timer story; the owner of the tables is its only writer today.
	if _, err := c.admin.Exec(`UPDATE kernel.commitments SET status = 'expired' WHERE id = $1`, expired.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	return map[model.CommitmentStatus]string{
		model.CommitmentStatusFulfilled: fulfilled.ID, model.CommitmentStatusCanceled: canceled.ID, model.CommitmentStatusExpired: expired.ID,
	}
}

// TestCancelClosedCommitmentIsAlreadyDone guards criterion 9: a closed
// commitment is never canceled, whatever the state it closed in.
func TestCancelClosedCommitmentIsAlreadyDone(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	for status, id := range c.closedCommitments(t) {
		if _, err := state.CancelCommitment(context.Background(), c.api, c.actor, spaceA, id, "late"); !isCode(err, model.CodeAlreadyDone) {
			t.Errorf("cancel a %s commitment: error = %v, want already_done", status, err)
		}
		if got := c.get(t, id); got.Status != status || got.CanceledReason == "late" {
			t.Errorf("a %s commitment changed to %s %q", status, got.Status, got.CanceledReason)
		}
	}
}

// TestFulfillNonOpenCommitmentIsRuleViolation guards criterion 10: fulfilled,
// canceled and expired commitments accept no more fulfillments.
func TestFulfillNonOpenCommitmentIsRuleViolation(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	for status, id := range c.closedCommitments(t) {
		entry := c.credit(t, "k-after-"+string(status), "5.00")
		if _, _, err := c.fulfill(id, entry); !isCode(err, model.CodeRuleViolation) {
			t.Errorf("fulfill a %s commitment: error = %v, want rule_violation", status, err)
		}
	}
	if n := count(t, c.admin, `SELECT count(*) FROM kernel.fulfillments`); n != 1 {
		t.Errorf("%d fulfillments, want only the one that closed the first", n)
	}
}

// TestCommitmentsAreInvisibleFromOtherSpaces guards criterion 12 through the
// state functions.
func TestCommitmentsAreInvisibleFromOtherSpaces(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	open := c.create(t, "100.00")
	entry := c.credit(t, "k1", "40.00")

	if _, err := state.GetCommitment(context.Background(), c.api, spaceB, open.ID); !isCode(err, model.CodeNotFound) {
		t.Errorf("GetCommitment from space B: %v, want not_found", err)
	}
	if _, _, err := state.FulfillCommitment(context.Background(), c.api, c.actorB, spaceB, open.ID, entry); !isCode(err, model.CodeNotFound) {
		t.Errorf("FulfillCommitment from space B: %v, want not_found", err)
	}
	if _, err := state.CancelCommitment(context.Background(), c.api, c.actorB, spaceB, open.ID, "why"); !isCode(err, model.CodeNotFound) {
		t.Errorf("CancelCommitment from space B: %v, want not_found", err)
	}
	if got := c.get(t, open.ID); got.Status != model.CommitmentStatusOpen || !got.FulfilledAmount.IsZero() {
		t.Errorf("commitment = %+v, want untouched", got)
	}
}
