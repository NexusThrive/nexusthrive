package state_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// ledger holds the fixtures of the exchange tests: in space A a system
// participant, a resource with two decimals and one with none, and accounts of
// both; in space B a participant, a resource and two accounts.
type ledger struct {
	admin, api *sql.DB
	actor      model.Actor
	actorB     model.Actor
	cop, unit  string
	a1, a2, a3 string
	u1, u2     string
	b1, b2     string
	resourceB  string
}

// exchangeSetup seeds the fixtures through the state operations.
func exchangeSetup(t *testing.T) ledger {
	t.Helper()
	admin, api := ledgerSetup(t)
	l := ledger{admin: admin, api: api}
	systemParticipant := func(space string) string {
		return mustParticipant(t, api, space, state.NewParticipant{Kind: model.ParticipantKindSystem, DisplayName: "System"})
	}
	l.actor = model.Actor{Kind: model.ActorSystem, ParticipantID: systemParticipant(spaceA)}
	l.actorB = model.Actor{Kind: model.ActorSystem, ParticipantID: systemParticipant(spaceB)}
	l.cop = mustResource(t, api, spaceA, newCOP())
	l.unit = mustResource(t, api, spaceA, state.NewResource{Kind: model.ResourceKindUnit, Code: "UND", Name: "Unit", Scale: 0})
	l.resourceB = mustResource(t, api, spaceB, newCOP())
	account := func(space, holder, resource, code string) string {
		return mustAccount(t, api, space, state.NewAccount{
			HolderParticipantID: holder, ResourceID: resource, Class: model.AccountClassAsset, Code: code, Name: code})
	}
	l.a1 = account(spaceA, l.actor.ParticipantID, l.cop, "A1")
	l.a2 = account(spaceA, l.actor.ParticipantID, l.cop, "A2")
	l.a3 = account(spaceA, l.actor.ParticipantID, l.cop, "A3")
	l.u1 = account(spaceA, l.actor.ParticipantID, l.unit, "U1")
	l.u2 = account(spaceA, l.actor.ParticipantID, l.unit, "U2")
	l.b1 = account(spaceB, l.actorB.ParticipantID, l.resourceB, "B1")
	l.b2 = account(spaceB, l.actorB.ParticipantID, l.resourceB, "B2")
	return l
}

// amount parses a decimal for a test.
func amount(t *testing.T, text string) model.Amount {
	t.Helper()
	parsed, err := model.ParseAmount(text)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", text, err)
	}
	return parsed
}

// pair builds a balanced exchange that moves value from debit to credit.
func (l ledger) pair(t *testing.T, key, credit, debit, resource, value string) model.NewExchange {
	t.Helper()
	return model.NewExchange{
		Kind: "test.exchange", IdempotencyKey: key,
		Entries: []model.NewEntry{
			{AccountID: credit, ResourceID: resource, Amount: amount(t, value)},
			{AccountID: debit, ResourceID: resource, Amount: amount(t, value).Neg()},
		},
	}
}

// record records an exchange in space A and fails the test on error.
func (l ledger) record(t *testing.T, in model.NewExchange) model.Exchange {
	t.Helper()
	got, err := state.RecordExchange(context.Background(), l.api, l.actor, spaceA, in)
	if err != nil {
		t.Fatalf("RecordExchange(%s): %v", in.IdempotencyKey, err)
	}
	return got
}

// TestRecordExchangeStoresExchangeAndEntries guards criterion 1.
func TestRecordExchangeStoresExchangeAndEntries(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)

	in := l.pair(t, "k1", l.a1, l.a2, l.cop, "50.00")
	in.Memo = "first"
	in.Metadata = json.RawMessage(`{"source":"test"}`)
	before := time.Now().Add(-time.Second)
	got := l.record(t, in)

	if got.SpaceID != spaceA || got.Kind != "test.exchange" || got.IdempotencyKey != "k1" ||
		got.ActorParticipantID != l.actor.ParticipantID || got.Memo != "first" || got.ReversesExchangeID != "" {
		t.Errorf("exchange = %+v", got)
	}
	if got.RecordedAt.Location() != time.UTC || got.OccurredAt.Location() != time.UTC {
		t.Errorf("times must be UTC: recorded %v, occurred %v", got.RecordedAt.Location(), got.OccurredAt.Location())
	}
	if got.RecordedAt.Before(before) || got.RecordedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("RecordedAt = %v", got.RecordedAt)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(got.Entries))
	}
	sum := model.Amount{}
	for _, entry := range got.Entries {
		sum = sum.Add(entry.Amount)
		if entry.ExchangeID != got.ID || entry.ResourceID != l.cop {
			t.Errorf("entry = %+v", entry)
		}
	}
	if !sum.IsZero() {
		t.Errorf("entries sum to %s, want 0", sum)
	}
	if !got.Entries[0].Amount.Equal(amount(t, "50.00")) || !got.Entries[1].Amount.Equal(amount(t, "-50.00")) {
		t.Errorf("amounts = %s, %s, want 50.00 and -50.00", got.Entries[0].Amount, got.Entries[1].Amount)
	}

	stored, err := state.GetExchange(context.Background(), l.api, spaceA, got.ID)
	if err != nil {
		t.Fatalf("GetExchange: %v", err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(stored.Metadata, &metadata); err != nil || metadata["source"] != "test" {
		t.Errorf("stored metadata = %s (%v)", stored.Metadata, err)
	}
	if stored.ID != got.ID || len(stored.Entries) != 2 || !stored.RecordedAt.Equal(got.RecordedAt) || stored.Memo != "first" {
		t.Errorf("stored = %+v", stored)
	}
	if got := count(t, l.admin, `SELECT count(*) FROM kernel.entries WHERE amount::text IN ('50.00', '-50.00')`); got != 2 {
		t.Errorf("canonical stored amounts = %d, want 2", got)
	}
}

// TestRecordExchangeDefaultsMetadataAndOccurredAt checks the optional fields.
func TestRecordExchangeDefaultsMetadataAndOccurredAt(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)

	got := l.record(t, l.pair(t, "k1", l.a1, l.a2, l.cop, "1.00"))
	if string(got.Metadata) != `{}` || got.OccurredAt.IsZero() {
		t.Errorf("metadata %s, occurred_at %v", got.Metadata, got.OccurredAt)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	in := l.pair(t, "k2", l.a1, l.a2, l.cop, "1.00")
	in.OccurredAt = at
	if got := l.record(t, in); !got.OccurredAt.Equal(at) {
		t.Errorf("OccurredAt = %v, want %v", got.OccurredAt, at)
	}
}

// TestAppendExchangeUnbalancedIsRuleViolation guards criteria 2 and 3: the
// conservation error is typed and comes out of the call, not the commit.
func TestAppendExchangeUnbalancedIsRuleViolation(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)

	cases := map[string][]model.NewEntry{
		"+50.00 and -49.99": {
			{AccountID: l.a1, ResourceID: l.cop, Amount: amount(t, "50.00")},
			{AccountID: l.a2, ResourceID: l.cop, Amount: amount(t, "-49.99")},
		},
		"one resource balanced and the other not": {
			{AccountID: l.a1, ResourceID: l.cop, Amount: amount(t, "5.00")},
			{AccountID: l.a2, ResourceID: l.cop, Amount: amount(t, "-5.00")},
			{AccountID: l.u1, ResourceID: l.unit, Amount: amount(t, "1")},
			{AccountID: l.u2, ResourceID: l.unit, Amount: amount(t, "1")},
		},
		"zero overall but unbalanced per resource": {
			{AccountID: l.a1, ResourceID: l.cop, Amount: amount(t, "1.00")},
			{AccountID: l.u1, ResourceID: l.unit, Amount: amount(t, "-1")},
		},
	}
	for name, entries := range cases {
		in := model.NewExchange{Kind: "test.exchange", IdempotencyKey: "k-" + name, Entries: entries}
		_, err := state.RecordExchange(context.Background(), l.api, l.actor, spaceA, in)
		requireCode(t, err, model.CodeRuleViolation)

		err = state.InSpace(context.Background(), l.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
			_, err := state.AppendExchange(ctx, tx, l.actor, in)
			return err
		})
		requireCode(t, err, model.CodeRuleViolation)
	}
	if got := count(t, l.admin, `SELECT count(*) FROM kernel.exchanges`); got != 0 {
		t.Errorf("exchanges = %d, want 0", got)
	}
}

// TestAppendExchangeSharesTransaction guards one operation, one transaction: a
// rolled back transaction leaves no exchange, and an event written next to it
// commits together.
func TestAppendExchangeSharesTransaction(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	ctx := context.Background()

	rollback := state.InSpace(ctx, l.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := state.AppendExchange(ctx, tx, l.actor, l.pair(t, "k-rollback", l.a1, l.a2, l.cop, "1.00")); err != nil {
			return err
		}
		return sql.ErrTxDone
	})
	if rollback == nil || count(t, l.admin, `SELECT count(*) FROM kernel.exchanges`) != 0 {
		t.Fatalf("a failed operation must leave nothing, error %v", rollback)
	}

	err := state.InSpace(ctx, l.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := state.AppendExchange(ctx, tx, l.actor, l.pair(t, "k-together", l.a1, l.a2, l.cop, "1.00")); err != nil {
			return err
		}
		_, err := state.AppendEvent(ctx, tx, testEvent("with-exchange"))
		return err
	})
	if err != nil {
		t.Fatalf("exchange and event: %v", err)
	}
	if count(t, l.admin, `SELECT count(*) FROM kernel.exchanges`) != 1 || count(t, l.admin, `SELECT count(*) FROM kernel.events`) != 1 {
		t.Error("the exchange and the event must commit together")
	}
}

// TestAppendExchangeValidatesBeforeWriting checks that invalid input and a
// missing space write nothing and leave the transaction usable.
func TestAppendExchangeValidatesBeforeWriting(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	ctx := context.Background()

	tx := beginInSpace(t, l.api, spaceA)
	defer tx.Rollback()
	bad := l.pair(t, "k-bad", l.a1, l.a2, l.cop, "1.00")
	bad.Kind = "nokind"
	_, err := state.AppendExchange(ctx, tx, l.actor, bad)
	requireCode(t, err, model.CodeInvalidInput)
	if _, err := state.AppendExchange(ctx, tx, l.actor, l.pair(t, "k-ok", l.a1, l.a2, l.cop, "1.00")); err != nil {
		t.Errorf("the transaction must stay usable after invalid input: %v", err)
	}

	noSpace, err := l.api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer noSpace.Rollback()
	_, err = state.AppendExchange(ctx, noSpace, l.actor, l.pair(t, "k-nospace", l.a1, l.a2, l.cop, "1.00"))
	requireCode(t, err, model.CodeRuleViolation)
	if _, err := noSpace.Exec(`SELECT 1`); err != nil {
		t.Errorf("the transaction must stay usable without a space: %v", err)
	}

	_, err = state.RecordExchange(ctx, l.api, l.actor, "not-a-uuid", l.pair(t, "k", l.a1, l.a2, l.cop, "1.00"))
	requireCode(t, err, model.CodeInvalidInput)
}

// TestEntryOnAccountOfOtherResourceIsRuleViolation guards criterion 5.
func TestEntryOnAccountOfOtherResourceIsRuleViolation(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)

	in := l.pair(t, "k1", l.a1, l.a2, l.unit, "1")
	_, err := state.RecordExchange(context.Background(), l.api, l.actor, spaceA, in)
	requireCode(t, err, model.CodeRuleViolation)
	if got := count(t, l.admin, `SELECT count(*) FROM kernel.exchanges`); got != 0 {
		t.Errorf("exchanges = %d, want 0", got)
	}
}

// TestEntryOnClosedAccountIsRuleViolation guards criterion 5.
func TestEntryOnClosedAccountIsRuleViolation(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	if err := state.CloseAccount(context.Background(), l.api, system, spaceA, l.a2); err != nil {
		t.Fatalf("CloseAccount: %v", err)
	}

	_, err := state.RecordExchange(context.Background(), l.api, l.actor, spaceA, l.pair(t, "k1", l.a1, l.a2, l.cop, "1.00"))
	requireCode(t, err, model.CodeRuleViolation)
	// The open accounts still work.
	l.record(t, l.pair(t, "k2", l.a1, l.a3, l.cop, "1.00"))
}

// TestEntryOnAccountOfOtherSpaceIsRuleViolation guards criteria 5 and 10 with a
// real account of space B.
func TestEntryOnAccountOfOtherSpaceIsRuleViolation(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)

	_, err := state.RecordExchange(context.Background(), l.api, l.actor, spaceA, l.pair(t, "k1", l.a1, l.b1, l.cop, "1.00"))
	requireCode(t, err, model.CodeRuleViolation)
	_, err = state.RecordExchange(context.Background(), l.api, l.actor, spaceA, l.pair(t, "k2", l.b1, l.b2, l.resourceB, "1.00"))
	requireCode(t, err, model.CodeRuleViolation)
	if got := count(t, l.admin, `SELECT count(*) FROM kernel.exchanges`); got != 0 {
		t.Errorf("exchanges = %d, want 0", got)
	}
}

// TestAmountBeyondResourceScaleIsInvalidInput guards criterion 6.
func TestAmountBeyondResourceScaleIsInvalidInput(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)

	_, err := state.RecordExchange(context.Background(), l.api, l.actor, spaceA, l.pair(t, "k1", l.a1, l.a2, l.cop, "10.001"))
	requireCode(t, err, model.CodeInvalidInput)
	_, err = state.RecordExchange(context.Background(), l.api, l.actor, spaceA, l.pair(t, "k2", l.u1, l.u2, l.unit, "1.5"))
	requireCode(t, err, model.CodeInvalidInput)

	got := l.record(t, l.pair(t, "k3", l.a1, l.a2, l.cop, "10.100"))
	if !got.Entries[0].Amount.Equal(amount(t, "10.10")) {
		t.Errorf("amount = %s, want 10.10", got.Entries[0].Amount)
	}
	if n := count(t, l.admin, `SELECT count(*) FROM kernel.entries WHERE amount::text IN ('10.10', '-10.10')`); n != 2 {
		t.Errorf("canonical stored amounts = %d, want 2", n)
	}
}

// TestReverseExchangeNetsToZeroPerAccount guards criterion 8.
func TestReverseExchangeNetsToZeroPerAccount(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	ctx := context.Background()
	original := l.record(t, l.pair(t, "k1", l.a1, l.a2, l.cop, "50.00"))

	reversal, err := state.ReverseExchange(ctx, l.api, l.actor, spaceA, original.ID, "k1-reversal")
	if err != nil {
		t.Fatalf("ReverseExchange: %v", err)
	}
	if reversal.ReversesExchangeID != original.ID || reversal.Kind != model.ReversalKind || len(reversal.Entries) != 2 {
		t.Errorf("reversal = %+v", reversal)
	}
	rows, err := l.admin.Query(`SELECT account_id::text, sum(amount)::text FROM kernel.entries GROUP BY account_id`)
	if err != nil {
		t.Fatalf("net per account: %v", err)
	}
	defer rows.Close()
	accounts := 0
	for rows.Next() {
		var account, net string
		if err := rows.Scan(&account, &net); err != nil {
			t.Fatalf("scan: %v", err)
		}
		accounts++
		if !amount(t, net).IsZero() {
			t.Errorf("account %s nets %s, want 0", account, net)
		}
	}
	if accounts != 2 {
		t.Errorf("accounts with entries = %d, want 2", accounts)
	}
}

// TestReverseTwiceIsAlreadyDone guards criterion 8.
func TestReverseTwiceIsAlreadyDone(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	original := l.record(t, l.pair(t, "k1", l.a1, l.a2, l.cop, "50.00"))

	if _, err := state.ReverseExchange(context.Background(), l.api, l.actor, spaceA, original.ID, "r1"); err != nil {
		t.Fatalf("first reversal: %v", err)
	}
	_, err := state.ReverseExchange(context.Background(), l.api, l.actor, spaceA, original.ID, "r2")
	requireCode(t, err, model.CodeAlreadyDone)
	if got := count(t, l.admin, `SELECT count(*) FROM kernel.exchanges`); got != 2 {
		t.Errorf("exchanges = %d, want the original and one reversal", got)
	}
}

// TestConcurrentReversalsOnlyOneWins guards criterion 8 under a race: the
// unique index decides, never a check in Go.
func TestConcurrentReversalsOnlyOneWins(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	original := l.record(t, l.pair(t, "k1", l.a1, l.a2, l.cop, "50.00"))

	const workers = 4
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = state.ReverseExchange(context.Background(), l.api, l.actor, spaceA, original.ID, "race-"+string(rune('a'+i)))
		}()
	}
	wg.Wait()
	wins, already := 0, 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if code, _ := model.CodeOf(err); code == model.CodeAlreadyDone {
			already++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 || already != workers-1 {
		t.Errorf("wins %d, already_done %d, want 1 and %d", wins, already, workers-1)
	}
	if got := count(t, l.admin, `SELECT count(*) FROM kernel.exchanges WHERE reverses_exchange_id IS NOT NULL`); got != 1 {
		t.Errorf("reversals = %d, want 1", got)
	}
}

// TestReverseExchangeOfOtherSpaceIsNotFound guards criterion 8.
func TestReverseExchangeOfOtherSpaceIsNotFound(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	original := l.record(t, l.pair(t, "k1", l.a1, l.a2, l.cop, "50.00"))

	_, err := state.ReverseExchange(context.Background(), l.api, l.actorB, spaceB, original.ID, "r1")
	requireCode(t, err, model.CodeNotFound)
	_, err = state.ReverseExchange(context.Background(), l.api, l.actor, spaceA, mustV7(t), "r2")
	requireCode(t, err, model.CodeNotFound)
	_, err = state.ReverseExchange(context.Background(), l.api, l.actor, spaceA, "nope", "r3")
	requireCode(t, err, model.CodeInvalidInput)
	if got := count(t, l.admin, `SELECT count(*) FROM kernel.exchanges`); got != 1 {
		t.Errorf("exchanges = %d, want only the original", got)
	}
}

// TestReverseNeedsOpenAccounts documents follow-up 30: an account closed after
// the original blocks its reversal.
func TestReverseNeedsOpenAccounts(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	original := l.record(t, l.pair(t, "k1", l.a1, l.a2, l.cop, "50.00"))
	if err := state.CloseAccount(context.Background(), l.api, system, spaceA, l.a2); err != nil {
		t.Fatalf("CloseAccount: %v", err)
	}
	_, err := state.ReverseExchange(context.Background(), l.api, l.actor, spaceA, original.ID, "r1")
	requireCode(t, err, model.CodeRuleViolation)
}

// TestIdempotencyKeyUniquePerSpace guards criterion 9: today a repeated key is
// rejected; HU-kernel-idempotency-14 will return the original result instead.
func TestIdempotencyKeyUniquePerSpace(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	ctx := context.Background()
	l.record(t, l.pair(t, "same", l.a1, l.a2, l.cop, "1.00"))

	_, err := state.RecordExchange(ctx, l.api, l.actor, spaceA, l.pair(t, "same", l.a1, l.a2, l.cop, "2.00"))
	requireCode(t, err, model.CodeRuleViolation)
	if _, err := state.RecordExchange(ctx, l.api, l.actorB, spaceB, l.pair(t, "same", l.b1, l.b2, l.resourceB, "1.00")); err != nil {
		t.Errorf("the same key in space B must be accepted: %v", err)
	}

	const workers = 2
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = state.RecordExchange(ctx, l.api, l.actor, spaceA, l.pair(t, "raced", l.a1, l.a3, l.cop, "1.00"))
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else {
			requireCode(t, err, model.CodeRuleViolation)
		}
	}
	if wins != 1 {
		t.Errorf("racing writers with one key: %d wins, want 1", wins)
	}
}

// TestLedgerIsolationBetweenSpaces guards criterion 10 through Go.
func TestLedgerIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	original := l.record(t, l.pair(t, "k1", l.a1, l.a2, l.cop, "50.00"))

	_, err := state.GetExchange(context.Background(), l.api, spaceB, original.ID)
	requireCode(t, err, model.CodeNotFound)
	_, err = state.GetExchange(context.Background(), l.api, spaceA, "nope")
	requireCode(t, err, model.CodeInvalidInput)
	if got, err := state.GetExchange(context.Background(), l.api, spaceA, original.ID); err != nil || got.ID != original.ID {
		t.Errorf("GetExchange in its own space = %v, %v", got.ID, err)
	}
}

// TestExchangeWithoutActorRejected guards criterion 11: there are no anonymous
// writes, and the actor must be a participant of the space.
func TestExchangeWithoutActorRejected(t *testing.T) {
	t.Parallel()
	l := exchangeSetup(t)
	ctx := context.Background()
	in := l.pair(t, "k1", l.a1, l.a2, l.cop, "1.00")

	for name, actor := range map[string]model.Actor{
		"system without participant": {Kind: model.ActorSystem},
		"zero actor":                 {},
		"person with participant":    {Kind: model.ActorPerson, ParticipantID: l.actor.ParticipantID},
		"person without participant": {Kind: model.ActorPerson},
	} {
		_, err := state.RecordExchange(ctx, l.api, actor, spaceA, in)
		if code, _ := model.CodeOf(err); code != model.CodePermissionDenied {
			t.Errorf("%s: error = %v, want permission_denied", name, err)
		}
	}
	_, err := state.RecordExchange(ctx, l.api, model.Actor{Kind: model.ActorSystem, ParticipantID: "nope"}, spaceA, in)
	requireCode(t, err, model.CodeInvalidInput)
	_, err = state.RecordExchange(ctx, l.api, l.actorB, spaceA, in)
	requireCode(t, err, model.CodeRuleViolation)
	_, err = state.RecordExchange(ctx, l.api, model.Actor{Kind: model.ActorSystem, ParticipantID: mustV7(t)}, spaceA, in)
	requireCode(t, err, model.CodeRuleViolation)
	if got := count(t, l.admin, `SELECT count(*) FROM kernel.exchanges`); got != 0 {
		t.Errorf("exchanges = %d, want 0", got)
	}
}
