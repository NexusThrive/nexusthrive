package laws_test

import (
	"context"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestParticipantChecksHoldInSQL closes follow-up 7: the CHECK constraints of
// participants refuse, straight from SQL and without the Go validation, an
// archived status that disagrees with archived_at, a document type without a
// number or the reverse, a country that is not two capital letters, and an
// unknown kind, document type or blank name. Valid rows of each shape are the
// positive controls.
func TestParticipantChecksHoldInSQL(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)

	insert := func(columns, marks string, args ...any) error {
		all := append([]any{newID(t), w.spaceA}, args...)
		_, err := w.admin.Exec(`INSERT INTO kernel.participants (id, space_id, `+columns+`) VALUES ($1, $2, `+marks+`)`, all...)
		return err
	}
	rejected := []struct {
		name    string
		columns string
		marks   string
		args    []any
	}{
		{"archived without archived_at", "kind, display_name, status", "'person', 'x', 'archived'", nil},
		{"active with archived_at", "kind, display_name, archived_at", "'person', 'x', now()", nil},
		{"document type without number", "kind, display_name, document_type", "'person', 'x', 'CC'", nil},
		{"document number without type", "kind, display_name, document_number", "'person', 'x', '123'", nil},
		{"unknown document type", "kind, display_name, document_type, document_number", "'person', 'x', 'XX', '123'", nil},
		{"country in lower case", "kind, display_name, country_code", "'person', 'x', $3", []any{"co"}},
		{"country with three letters", "kind, display_name, country_code", "'person', 'x', $3", []any{"COL"}},
		{"country with a digit", "kind, display_name, country_code", "'person', 'x', $3", []any{"C1"}},
		{"empty country", "kind, display_name, country_code", "'person', 'x', $3", []any{""}},
		{"unknown kind", "kind, display_name", "'robot', 'x'", nil},
		{"blank display name", "kind, display_name", "'person', '   '", nil},
	}
	before := w.counts()
	for _, c := range rejected {
		if err := insert(c.columns, c.marks, c.args...); pgCode(err) != "23514" {
			t.Errorf("%s: error %v, want SQLSTATE 23514 (check violation)", c.name, err)
		}
	}
	if changed := diff(before, w.counts()); len(changed) != 0 {
		t.Errorf("the refused participants left %v", changed)
	}

	accepted := []struct {
		name    string
		columns string
		marks   string
		args    []any
	}{
		{"archived with archived_at", "kind, display_name, status, archived_at", "'person', 'x', 'archived', now()", nil},
		{"document pair", "kind, display_name, document_type, document_number", "'person', 'x', 'NIT', '900123'", nil},
		{"country", "kind, display_name, country_code", "'person', 'x', $3", []any{"CO"}},
	}
	for _, c := range accepted {
		if err := insert(c.columns, c.marks, c.args...); err != nil {
			t.Errorf("%s: the valid row failed: %v", c.name, err)
		}
	}
	if changed := diff(before, w.counts()); changed["participants"] != len(accepted) || len(changed) != 1 {
		t.Errorf("the valid participants changed %v, want %d participants", changed, len(accepted))
	}
}

// TestIdentityOfAnotherSpaceCanBeLinked fixes the behaviour of follow-up 4,
// accepted for milestone 1: an identity is visible from a space only through
// a participant of that space, but nothing stops a participant from linking an
// identity id that belongs to another space, and from then on both spaces
// read the same email. The kernel trusts the caller to resolve the identity
// on the server (invitation or session); the precondition for B3 is that an
// IdentityID never comes from the request of a client.
func TestIdentityOfAnotherSpaceCanBeLinked(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	ctx := context.Background()

	identity, err := state.CreateIdentity(ctx, w.api, system, state.NewIdentity{
		Provider: model.IdentityProviderClerk, Subject: "follow-up-4", PrimaryEmail: "shared@example.test", DisplayName: "Shared"})
	if err != nil {
		t.Fatalf("CreateIdentity: %v", err)
	}
	link := func(space string) {
		t.Helper()
		if _, err := state.CreateParticipant(ctx, w.api, system, space,
			state.NewParticipant{Kind: model.ParticipantKindPerson, IdentityID: identity, DisplayName: "Linked"}); err != nil {
			t.Fatalf("linking the identity in space %s failed: %v", space, err)
		}
	}
	emailIn := func(space string) (email string, rows int) {
		t.Helper()
		inSpaceTx(t, w.api, space, func(q queryer) {
			err := q.QueryRow(`SELECT count(*), coalesce(max(primary_email), '') FROM kernel.identities WHERE id = $1`, identity).Scan(&rows, &email)
			if err != nil {
				t.Fatalf("read the identity: %v", err)
			}
		})
		return email, rows
	}

	link(w.spaceA)
	if _, rows := emailIn(w.spaceB); rows != 0 {
		t.Errorf("before it is linked, space B sees %d rows of the identity, want 0", rows)
	}
	// The link of space B succeeds although the identity is invisible there.
	link(w.spaceB)
	for _, space := range []string{w.spaceA, w.spaceB} {
		if email, rows := emailIn(space); rows != 1 || email != "shared@example.test" {
			t.Errorf("space %s reads %d rows with email %q, want the shared one", space, rows, email)
		}
	}
}

// TestReversalOfAReversalRestoresTheOriginal fixes the behaviour of follow-up
// 35, accepted for milestone 1: nothing forbids compensating a compensation.
// Conservation holds, the history only grows, and the net effect of the third
// exchange is the effect of the first. Whether to forbid it is a decision for
// an ADR; until then this test records what the kernel does.
func TestReversalOfAReversalRestoresTheOriginal(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	ctx := context.Background()
	bookkeeper := model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}

	first, err := state.RecordExchange(ctx, w.api, bookkeeper, w.spaceA, model.NewExchange{
		Kind: "test.exchange", IdempotencyKey: "k-r1", Entries: []model.NewEntry{
			{AccountID: f.to, ResourceID: f.resource, Amount: money(t, "10.00")},
			{AccountID: f.from, ResourceID: f.resource, Amount: money(t, "10.00").Neg()},
		}})
	if err != nil {
		t.Fatalf("RecordExchange: %v", err)
	}
	second, err := state.ReverseExchange(ctx, w.api, bookkeeper, w.spaceA, first.ID, "k-r2")
	if err != nil {
		t.Fatalf("the first reversal failed: %v", err)
	}
	third, err := state.ReverseExchange(ctx, w.api, bookkeeper, w.spaceA, second.ID, "k-r3")
	if err != nil {
		t.Fatalf("the reversal of a reversal failed: %v", err)
	}
	if third.ReversesExchangeID != second.ID {
		t.Errorf("the third exchange reverses %q, want %s", third.ReversesExchangeID, second.ID)
	}

	var toNet, fromNet string
	err = w.admin.QueryRow(`SELECT
		(SELECT sum(amount)::text FROM kernel.entries WHERE account_id = $1),
		(SELECT sum(amount)::text FROM kernel.entries WHERE account_id = $2)`, f.to, f.from).Scan(&toNet, &fromNet)
	if err != nil {
		t.Fatalf("read the balances: %v", err)
	}
	if toNet != "10.00" || fromNet != "-10.00" {
		t.Errorf("balances = %s and %s, want the effect of the original, 10.00 and -10.00", toNet, fromNet)
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM (SELECT exchange_id FROM kernel.entries GROUP BY exchange_id HAVING sum(amount) <> 0) x`); got != 0 {
		t.Errorf("%d exchanges do not add up to zero", got)
	}
	// The database does not demand the reversal kind either: an exchange of
	// another kind that mirrors an exchange and names it is accepted, and only
	// the Go layer sets the kind (second half of follow-up 35).
	fourth, err := state.RecordExchange(ctx, w.api, bookkeeper, w.spaceA, model.NewExchange{
		Kind: "test.exchange", IdempotencyKey: "k-r4", Entries: []model.NewEntry{
			{AccountID: f.to, ResourceID: f.resource, Amount: money(t, "5.00")},
			{AccountID: f.from, ResourceID: f.resource, Amount: money(t, "5.00").Neg()},
		}})
	if err != nil {
		t.Fatalf("RecordExchange: %v", err)
	}
	mirror := exchangeStmts(t, w.spaceA, "k-r5", f.creditor,
		[3]string{f.to, f.resource, "-5.00"}, [3]string{f.from, f.resource, "5.00"})
	mirror[0] = stmt{
		`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id, reverses_exchange_id)
		 VALUES ($1, $2, 'test.not_a_reversal', $3, $4, $5)`,
		append(append([]any{}, mirror[0].args...), fourth.ID),
	}
	if err := runIn(t, w.api, w.spaceA, mirror...); err != nil {
		t.Errorf("an exchange of another kind that mirrors %s was refused: %v; the follow-up says it is accepted", fourth.ID, err)
	}
}
