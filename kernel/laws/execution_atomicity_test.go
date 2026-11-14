package laws_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// moneyOperation is the operation of the tests of the laws: it needs the two
// permissions of criterion 1, moves value and declares idempotency.
var moneyOperation = execution.Operation{
	Name:        "test.exchange_and_commitment",
	Permissions: []string{model.PermissionExchangeWrite, model.PermissionCommitmentWrite},
	Risk:        model.RiskMoney,
	Idempotent:  true,
}

// billingOperation is moneyOperation behind a capability the space must have.
var billingOperation = execution.Operation{
	Name:        "test.billed_exchange",
	Permissions: []string{model.PermissionExchangeWrite, model.PermissionCommitmentWrite},
	Capability:  "test.billing",
	Risk:        model.RiskMoney,
	Idempotent:  true,
}

// writeOperation is a plain write operation.
var writeOperation = execution.Operation{
	Name: "test.write_commitment", Permissions: []string{model.PermissionCommitmentWrite}, Risk: model.RiskWrite,
}

// readOperation reads events and writes nothing.
var readOperation = execution.Operation{
	Name: "test.read_events", Permissions: []string{model.PermissionEventRead}, Risk: model.RiskRead,
}

// engineWorld is a disposable database with two active spaces and an engine
// that knows the operations of the tests.
type engineWorld struct {
	t      *testing.T
	admin  *sql.DB
	api    *sql.DB
	clock  *clock
	engine *execution.Engine
	tables []string
	spaceA string
	spaceB string
}

// newEngineWorld builds the world.
func newEngineWorld(t *testing.T) *engineWorld {
	t.Helper()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	now := &clock{now: time.Now().UTC()}
	auth, err := capability.NewAuthorizer(model.KernelCatalog(), now)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	engine, err := execution.NewEngine(api, auth, moneyOperation, billingOperation, writeOperation, readOperation)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	w := &engineWorld{t: t, admin: admin, api: api, clock: now, engine: engine, tables: kernelTables(t, admin)}
	if len(w.tables) < 12 {
		t.Fatalf("found %d kernel tables, expected the whole schema: %v", len(w.tables), w.tables)
	}
	w.spaceA = w.space("engine-a")
	w.spaceB = w.space("engine-b")
	return w
}

// space creates an active business space.
func (w *engineWorld) space(slug string) string {
	w.t.Helper()
	id, err := state.CreateSpace(context.Background(), w.api, system, state.NewSpace{Kind: model.SpaceKindBusiness, Name: slug, Slug: slug})
	if err != nil {
		w.t.Fatalf("CreateSpace: %v", err)
	}
	w.setStatus(id, "active")
	return id
}

// setStatus changes the status of a space as the administrator.
func (w *engineWorld) setStatus(spaceID, status string) {
	w.t.Helper()
	if _, err := w.admin.Exec(`UPDATE kernel.spaces SET status = $2 WHERE id = $1`, spaceID, status); err != nil {
		w.t.Fatalf("set status %s: %v", status, err)
	}
}

// person creates an active participant and returns its actor.
func (w *engineWorld) person(spaceID, name string) model.Actor {
	w.t.Helper()
	id, err := state.CreateParticipant(context.Background(), w.api, system, spaceID,
		state.NewParticipant{Kind: model.ParticipantKindPerson, DisplayName: name})
	if err != nil {
		w.t.Fatalf("CreateParticipant: %v", err)
	}
	return model.Actor{Kind: model.ActorPerson, ParticipantID: id}
}

// grant creates a role with the permissions and grants it to the actor with an
// optional expiry; it returns the id of the grant.
func (w *engineWorld) grant(spaceID string, actor model.Actor, key string, expiresAt *time.Time, permissions ...string) string {
	w.t.Helper()
	role, err := state.CreateRole(context.Background(), w.api, system, spaceID, model.NewRole{Key: key, Name: key, Permissions: permissions})
	if err != nil {
		w.t.Fatalf("CreateRole: %v", err)
	}
	id, err := state.GrantRole(context.Background(), w.api, w.clock, system, spaceID,
		model.NewGrant{ParticipantID: actor.ParticipantID, RoleID: role, ExpiresAt: expiresAt})
	if err != nil {
		w.t.Fatalf("GrantRole: %v", err)
	}
	return id
}

// writer creates a person that holds both permissions of the money operation.
func (w *engineWorld) writer(spaceID, name string) model.Actor {
	w.t.Helper()
	actor := w.person(spaceID, name)
	w.grant(spaceID, actor, name+"_role", nil, model.PermissionExchangeWrite, model.PermissionCommitmentWrite, model.PermissionEventRead)
	return actor
}

// counts reads the rows of every kernel table.
func (w *engineWorld) counts() map[string]int { return rowCounts(w.t, w.admin, w.tables) }

// fixtures are the ledger rows the operation writes against.
type fixtures struct {
	debtor, creditor, resource, from, to string
}

// ledger creates a resource, two accounts, a debtor and a creditor in a space.
func (w *engineWorld) ledger(spaceID string) fixtures {
	w.t.Helper()
	ctx := context.Background()
	creditor := w.person(spaceID, "Creditor")
	debtor := w.person(spaceID, "Debtor")
	resource, err := state.CreateResource(ctx, w.api, system, spaceID, state.NewResource{Kind: model.ResourceKindMoney, Code: "COP", Name: "Peso", Scale: 2})
	if err != nil {
		w.t.Fatalf("CreateResource: %v", err)
	}
	account := func(code string) string {
		id, err := state.OpenAccount(ctx, w.api, system, spaceID, state.NewAccount{
			HolderParticipantID: creditor.ParticipantID, ResourceID: resource, Class: model.AccountClassAsset, Code: code, Name: code})
		if err != nil {
			w.t.Fatalf("OpenAccount: %v", err)
		}
		return id
	}
	return fixtures{debtor: debtor.ParticipantID, creditor: creditor.ParticipantID, resource: resource, from: account("FROM"), to: account("TO")}
}

// money parses a decimal.
func money(t *testing.T, text string) model.Amount {
	t.Helper()
	parsed, err := model.ParseAmount(text)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", text, err)
	}
	return parsed
}

// operation runs the writes of the operation under test, one function per
// step, in the order the test chooses. The flags say which steps ran.
type operation struct {
	t          *testing.T
	f          fixtures
	calls      int
	exchange   string
	commitment string
	ran        map[string]bool
}

// newOperation prepares an operation over the fixtures.
func newOperation(t *testing.T, f fixtures) *operation {
	return &operation{t: t, f: f, ran: map[string]bool{}}
}

// stepExchange records an exchange, balanced or not. It names no key: the
// operations of these tests are idempotent, so the engine derives it.
func (o *operation) stepExchange(ctx context.Context, tx *execution.Tx, balanced bool) error {
	credit := "10.00"
	debit := "10.00"
	if !balanced {
		debit = "5.00"
	}
	out, err := tx.RecordExchange(ctx, model.NewExchange{Kind: "test.exchange", Entries: []model.NewEntry{
		{AccountID: o.f.to, ResourceID: o.f.resource, Amount: money(o.t, credit)},
		{AccountID: o.f.from, ResourceID: o.f.resource, Amount: money(o.t, debit).Neg()},
	}})
	if err != nil {
		return err
	}
	o.exchange = out.ID
	o.ran["exchange"] = true
	return nil
}

// stepCommitment creates a commitment with the debtor given.
func (o *operation) stepCommitment(ctx context.Context, tx *execution.Tx, debtor string) error {
	out, err := tx.CreateCommitment(ctx, model.NewCommitment{
		Kind: "test.commitment", DebtorParticipantID: debtor, CreditorParticipantID: o.f.creditor,
		ResourceID: o.f.resource, Amount: money(o.t, "10.00"), DueAt: time.Now().Add(48 * time.Hour),
	})
	if err != nil {
		return err
	}
	o.commitment = out.ID
	o.ran["commitment"] = true
	return nil
}

// stepEvent appends the event with the ids of the earlier steps and its outbox
// row for the internal subscriber.
func (o *operation) stepEvent(ctx context.Context, tx *execution.Tx) error {
	payload, err := json.Marshal(map[string]string{"exchange_id": o.exchange, "commitment_id": o.commitment})
	if err != nil {
		return err
	}
	if _, err := tx.AppendEvent(ctx, execution.EventInput{Type: "test.exchange_recorded", Version: 1, Payload: payload}, model.SubscriberInternal); err != nil {
		return err
	}
	o.ran["event"] = true
	return nil
}

// standard is the handler of criterion 1: exchange, commitment, event.
func (o *operation) standard() execution.Handler {
	return func(ctx context.Context, tx *execution.Tx) error {
		o.calls++
		if err := o.stepExchange(ctx, tx, true); err != nil {
			return err
		}
		if err := o.stepCommitment(ctx, tx, o.f.debtor); err != nil {
			return err
		}
		return o.stepEvent(ctx, tx)
	}
}

// run executes the money operation in a space.
func (w *engineWorld) run(actor model.Actor, spaceID string, requestID string, fn execution.Handler) error {
	return w.runOperation(actor, spaceID, requestID, moneyOperation.Name, fn)
}

// runOperation executes an idempotent operation the way these tests did before
// idempotency existed: through RunIdempotent with a fresh key and an empty body,
// so that every call is a first execution, and with a Handler adapted to the
// ResultHandler it takes, so that the assertions of the tests stay as they were.
func (w *engineWorld) runOperation(actor model.Actor, spaceID, requestID, name string, fn execution.Handler) error {
	_, err := w.engine.RunIdempotent(context.Background(),
		execution.Call{Actor: actor, SpaceID: spaceID, RequestID: requestID, IdempotencyKey: "key-" + execution.NewRequestID(), Request: json.RawMessage(`{}`)},
		name, func(ctx context.Context, tx *execution.Tx) (json.RawMessage, error) {
			return json.RawMessage(`{}`), fn(ctx, tx)
		})
	return err
}

// TestOperationWritesAllFourOrNone guards criterion 1: an operation with both
// permissions writes the exchange and its entries, the commitment with its
// timer, the event, the outbox row and, because the operation is idempotent, its
// idempotency record, together; the event carries the request id and the actor
// of the call, and no other table changes.
func TestOperationWritesAllFourOrNone(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")
	op := newOperation(t, f)

	before := w.counts()
	if err := w.run(actor, w.spaceA, "request-1", op.standard()); err != nil {
		t.Fatalf("the operation failed: %v", err)
	}
	want := map[string]int{"exchanges": 1, "entries": 2, "commitments": 1, "timers": 1, "events": 1, "outbox": 1, "idempotency_records": 1}
	got := diff(before, w.counts())
	for table, n := range want {
		if got[table] != n {
			t.Errorf("%s changed by %d, want %d", table, got[table], n)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the operation changed %v, want exactly %v", got, want)
	}

	var seq int64
	var requestID, actorID, kind string
	var payload []byte
	err := w.admin.QueryRow(`SELECT seq, request_id, actor_participant_id::text, actor_kind, payload FROM kernel.events WHERE type = 'test.exchange_recorded'`).
		Scan(&seq, &requestID, &actorID, &kind, &payload)
	if err != nil {
		t.Fatalf("read the event: %v", err)
	}
	if requestID != "request-1" || actorID != actor.ParticipantID || kind != string(model.ActorPerson) {
		t.Errorf("event request id = %q, actor = %s %s; want request-1 and the person %s", requestID, kind, actorID, actor.ParticipantID)
	}
	var outboxSubscriber, outboxStatus string
	if err := w.admin.QueryRow(`SELECT subscriber, status FROM kernel.outbox WHERE event_seq = $1`, seq).Scan(&outboxSubscriber, &outboxStatus); err != nil {
		t.Fatalf("read the outbox row of the event: %v", err)
	}
	if outboxSubscriber != string(model.SubscriberInternal) || outboxStatus != string(model.OutboxPending) {
		t.Errorf("outbox row = %s %s, want a pending row for the internal subscriber", outboxSubscriber, outboxStatus)
	}
	var ids map[string]string
	if err := json.Unmarshal(payload, &ids); err != nil {
		t.Fatalf("decode the payload: %v", err)
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM kernel.exchanges WHERE id = $1 AND actor_participant_id = $2`, ids["exchange_id"], actor.ParticipantID); got != 1 {
		t.Errorf("the exchange named by the payload was not written by the actor")
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM kernel.commitments WHERE id = $1 AND created_by_participant_id = $2`, ids["commitment_id"], actor.ParticipantID); got != 1 {
		t.Errorf("the commitment named by the payload was not created by the actor")
	}
	if got := countOf(t, w.admin, `SELECT count(*) FROM kernel.timers WHERE commitment_id = $1`, ids["commitment_id"]); got != 1 {
		t.Errorf("the commitment named by the payload has no timer")
	}
}

// countOf runs a count query as the administrator.
func countOf(t *testing.T, admin *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

// TestOperationFailureLeavesNothing guards criteria 2, 3 and 4: when a step
// fails, whichever it is, nothing of the steps before it remains. Every case
// proves that the earlier steps did run, so the rollback is what removed them,
// and compares every table of the schema.
func TestOperationFailureLeavesNothing(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	actor := w.writer(w.spaceA, "writer")
	const missingDebtor = "01900000-0000-7000-8000-0000000000ee"

	cases := []struct {
		name string
		fn   func(*operation) execution.Handler
		code model.Code
		ran  []string
	}{
		{
			name: "the commitment fails (criterion 2)",
			fn: func(o *operation) execution.Handler {
				return func(ctx context.Context, tx *execution.Tx) error {
					if err := o.stepExchange(ctx, tx, true); err != nil {
						return err
					}
					return o.stepCommitment(ctx, tx, missingDebtor)
				}
			},
			code: model.CodeRuleViolation,
			ran:  []string{"exchange"},
		},
		{
			name: "the entries do not add up to zero (criterion 4)",
			fn: func(o *operation) execution.Handler {
				return func(ctx context.Context, tx *execution.Tx) error {
					if err := o.stepCommitment(ctx, tx, o.f.debtor); err != nil {
						return err
					}
					if err := o.stepEvent(ctx, tx); err != nil {
						return err
					}
					return o.stepExchange(ctx, tx, false)
				}
			},
			code: model.CodeRuleViolation,
			ran:  []string{"commitment", "event"},
		},
	}
	for _, c := range cases {
		before := w.counts()
		o := newOperation(t, f)
		err := w.run(actor, w.spaceA, "request-fail", c.fn(o))
		if err == nil {
			t.Errorf("%s: the operation succeeded", c.name)
		}
		if c.code != "" {
			if got, typed := model.CodeOf(err); !typed || got != c.code {
				t.Errorf("%s: error = %v, want code %s", c.name, err, c.code)
			}
		}
		for _, step := range c.ran {
			if !o.ran[step] {
				t.Errorf("%s: step %s did not run before the failure, so the rollback is not proven", c.name, step)
			}
		}
		if changed := diff(before, w.counts()); len(changed) != 0 {
			t.Errorf("%s: the failed operation left %v", c.name, changed)
		}
	}

	// The same operation succeeds, so the failures above came from the steps
	// and not from the operation. The failure injected in each write is in
	// TestLawEndToEndInjectedFailureLeavesNothing.
	before := w.counts()
	if err := w.run(actor, w.spaceA, "request-ok", newOperation(t, f).standard()); err != nil {
		t.Fatalf("the operation afterwards failed: %v", err)
	}
	if changed := diff(before, w.counts()); changed["outbox"] != 1 || changed["exchanges"] != 1 {
		t.Errorf("the operation afterwards changed %v", changed)
	}
}
