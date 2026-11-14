package state_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// cashAccount builds a valid asset account named CASH for a holder.
func cashAccount(holder, resource string) state.NewAccount {
	return state.NewAccount{
		HolderParticipantID: holder, ResourceID: resource,
		Class: model.AccountClassAsset, Code: "CASH", Name: "Cash",
	}
}

// mustAccount opens an account and returns its id.
func mustAccount(t *testing.T, api *sql.DB, spaceID string, in state.NewAccount) string {
	t.Helper()
	id, err := state.OpenAccount(context.Background(), api, system, spaceID, in)
	if err != nil {
		t.Fatalf("OpenAccount: %v", err)
	}
	return id
}

// TestOpenAccountStartsOpen guards criterion 3.
func TestOpenAccountStartsOpen(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	resource := mustResource(t, api, spaceA, newCOP())

	id := mustAccount(t, api, spaceA, cashAccount(holder, resource))
	got, err := state.GetAccount(context.Background(), api, spaceA, id)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	want := model.Account{
		ID: id, SpaceID: spaceA, HolderParticipantID: holder, ResourceID: resource,
		Class: model.AccountClassAsset, Code: "CASH", Name: "Cash", Status: model.AccountStatusOpen,
	}
	if got != want {
		t.Errorf("account = %+v, want %+v", got, want)
	}
	if n := count(t, admin, `SELECT count(*) FROM kernel.accounts WHERE id = $1 AND parent_account_id IS NULL AND closed_at IS NULL`, id); n != 1 {
		t.Errorf("stored account rows = %d, want 1", n)
	}
}

// TestOpenAccountRejectsBadClass guards criterion 3 and the blank fields.
func TestOpenAccountRejectsBadClass(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	resource := mustResource(t, api, spaceA, newCOP())

	cases := map[string]func(*state.NewAccount){
		"unknown class": func(a *state.NewAccount) { a.Class = "foo" },
		"empty class":   func(a *state.NewAccount) { a.Class = "" },
		"blank code":    func(a *state.NewAccount) { a.Code = " " },
		"blank name":    func(a *state.NewAccount) { a.Name = "" },
		"bad holder":    func(a *state.NewAccount) { a.HolderParticipantID = "nope" },
		"empty holder":  func(a *state.NewAccount) { a.HolderParticipantID = "" },
		"nil resource":  func(a *state.NewAccount) { a.ResourceID = "00000000-0000-0000-0000-000000000000" },
		"bad parent":    func(a *state.NewAccount) { a.ParentAccountID = "nope" },
	}
	for name, mutate := range cases {
		in := cashAccount(holder, resource)
		mutate(&in)
		_, err := state.OpenAccount(context.Background(), api, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.accounts`); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
}

// TestOpenAccountDuplicateHolderCodeIsRuleViolation: a code is unique per
// holder; another holder may reuse it.
func TestOpenAccountDuplicateHolderCodeIsRuleViolation(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)
	first := mustParticipant(t, api, spaceA, newPerson("", "First"))
	second := mustParticipant(t, api, spaceA, newPerson("", "Second"))
	resource := mustResource(t, api, spaceA, newCOP())

	mustAccount(t, api, spaceA, cashAccount(first, resource))
	_, err := state.OpenAccount(context.Background(), api, system, spaceA, cashAccount(first, resource))
	requireCode(t, err, model.CodeRuleViolation)
	mustAccount(t, api, spaceA, cashAccount(second, resource))
}

// TestOpenAccountWithParentSameResource guards criterion 4: the valid path.
func TestOpenAccountWithParentSameResource(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	resource := mustResource(t, api, spaceA, newCOP())

	parent := mustAccount(t, api, spaceA, cashAccount(holder, resource))
	child := cashAccount(holder, resource)
	child.Code, child.ParentAccountID = "DRAWER", parent
	id := mustAccount(t, api, spaceA, child)
	got, err := state.GetAccount(context.Background(), api, spaceA, id)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.ParentAccountID != parent {
		t.Errorf("parent = %q, want %q", got.ParentAccountID, parent)
	}
}

// TestOpenAccountParentOfOtherResourceIsRuleViolation guards criterion 4.
func TestOpenAccountParentOfOtherResourceIsRuleViolation(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	cop := mustResource(t, api, spaceA, newCOP())
	units := mustResource(t, api, spaceA, state.NewResource{Kind: model.ResourceKindUnit, Code: "UND", Name: "Unit"})

	parent := mustAccount(t, api, spaceA, cashAccount(holder, cop))
	child := cashAccount(holder, units)
	child.Code, child.ParentAccountID = "STOCK", parent
	_, err := state.OpenAccount(context.Background(), api, system, spaceA, child)
	requireCode(t, err, model.CodeRuleViolation)
	if got := count(t, admin, `SELECT count(*) FROM kernel.accounts`); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
}

// TestOpenAccountParentOfOtherSpaceIsRuleViolation guards criterion 4: the
// foreign parent is a real account, inserted by the administrator.
func TestOpenAccountParentOfOtherSpaceIsRuleViolation(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	holderA := mustParticipant(t, api, spaceA, newPerson("", "Holder A"))
	holderB := mustParticipant(t, api, spaceB, newPerson("", "Holder B"))
	resourceA := mustResource(t, api, spaceA, newCOP())
	resourceB := mustResource(t, api, spaceB, newCOP())
	parentB := mustAccount(t, api, spaceB, cashAccount(holderB, resourceB))

	child := cashAccount(holderA, resourceA)
	child.Code, child.ParentAccountID = "DRAWER", parentB
	_, err := state.OpenAccount(context.Background(), api, system, spaceA, child)
	requireCode(t, err, model.CodeRuleViolation)

	// The same holds when the administrator forces the row in space A.
	_, err = admin.Exec(`INSERT INTO kernel.accounts (id, space_id, holder_participant_id, resource_id, parent_account_id, class, code, name)
		VALUES ('01900000-0000-7000-8000-000000000f03', $1, $2, $3, $4, 'asset', 'FORCED', 'Forced')`, spaceA, holderA, resourceA, parentB)
	requireSQLState(t, err, "23503")
}

// TestOpenAccountHolderOfOtherSpaceIsRuleViolation guards criterion 5.
func TestOpenAccountHolderOfOtherSpaceIsRuleViolation(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)
	holderB := mustParticipant(t, api, spaceB, newPerson("", "Holder B"))
	resourceA := mustResource(t, api, spaceA, newCOP())

	_, err := state.OpenAccount(context.Background(), api, system, spaceA, cashAccount(holderB, resourceA))
	requireCode(t, err, model.CodeRuleViolation)
}

// TestOpenAccountResourceOfOtherSpaceIsRuleViolation guards criterion 5.
func TestOpenAccountResourceOfOtherSpaceIsRuleViolation(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)
	holderA := mustParticipant(t, api, spaceA, newPerson("", "Holder A"))
	resourceB := mustResource(t, api, spaceB, newCOP())

	_, err := state.OpenAccount(context.Background(), api, system, spaceA, cashAccount(holderA, resourceB))
	requireCode(t, err, model.CodeRuleViolation)
}

// TestOpenAccountWithMissingReferencesIsRuleViolation: an id that exists
// nowhere breaks the same keys as one of another space.
func TestOpenAccountWithMissingReferencesIsRuleViolation(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	resource := mustResource(t, api, spaceA, newCOP())
	const ghost = "01900000-0000-7000-8000-000000000f99"

	for name, mutate := range map[string]func(*state.NewAccount){
		"holder":   func(a *state.NewAccount) { a.HolderParticipantID = ghost },
		"resource": func(a *state.NewAccount) { a.ResourceID = ghost },
		"parent":   func(a *state.NewAccount) { a.ParentAccountID = ghost },
	} {
		in := cashAccount(holder, resource)
		mutate(&in)
		_, err := state.OpenAccount(context.Background(), api, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeRuleViolation {
			t.Errorf("missing %s: error = %v, want rule_violation", name, err)
		}
	}
}

// TestCloseAccountSetsClosedAtAndIsIdempotent guards criterion 6.
func TestCloseAccountSetsClosedAtAndIsIdempotent(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	resource := mustResource(t, api, spaceA, newCOP())
	id := mustAccount(t, api, spaceA, cashAccount(holder, resource))

	if err := state.CloseAccount(context.Background(), api, system, spaceA, id); err != nil {
		t.Fatalf("CloseAccount: %v", err)
	}
	first, err := state.GetAccount(context.Background(), api, spaceA, id)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if first.Status != model.AccountStatusClosed || first.ClosedAt == nil || time.Since(*first.ClosedAt) > time.Minute {
		t.Fatalf("closed account = %+v, want status closed and a recent closed_at", first)
	}

	if err := state.CloseAccount(context.Background(), api, system, spaceA, id); err != nil {
		t.Fatalf("second CloseAccount: %v", err)
	}
	second, err := state.GetAccount(context.Background(), api, spaceA, id)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if second.ClosedAt == nil || !second.ClosedAt.Equal(*first.ClosedAt) {
		t.Errorf("closed_at changed from %v to %v", first.ClosedAt, second.ClosedAt)
	}
}

// TestCloseAccountOfOtherSpaceIsNotFound guards criterion 6 and 7.
func TestCloseAccountOfOtherSpaceIsNotFound(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	resource := mustResource(t, api, spaceA, newCOP())
	id := mustAccount(t, api, spaceA, cashAccount(holder, resource))

	requireCode(t, state.CloseAccount(context.Background(), api, system, spaceB, id), model.CodeNotFound)
	requireCode(t, state.CloseAccount(context.Background(), api, system, spaceA, "01900000-0000-7000-8000-000000000f98"), model.CodeNotFound)
	requireCode(t, state.CloseAccount(context.Background(), api, system, spaceA, "nope"), model.CodeInvalidInput)
	if got := count(t, admin, `SELECT count(*) FROM kernel.accounts WHERE status = 'open'`); got != 1 {
		t.Errorf("open accounts = %d, want 1", got)
	}
}

// TestAccountsIsolationBetweenSpaces guards criterion 7 for accounts.
func TestAccountsIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	resource := mustResource(t, api, spaceA, newCOP())
	id := mustAccount(t, api, spaceA, cashAccount(holder, resource))

	visible(t, api, spaceB, func(tx *sql.Tx) {
		if got := count(t, tx, `SELECT count(*) FROM kernel.accounts`); got != 0 {
			t.Errorf("space B sees %d accounts of A, want 0", got)
		}
	})
	_, err := state.GetAccount(context.Background(), api, spaceB, id)
	requireCode(t, err, model.CodeNotFound)
}
