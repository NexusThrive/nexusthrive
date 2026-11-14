package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// system is the kernel acting by itself.
var system = model.Actor{Kind: model.ActorSystem}

// fakeClock is a Clock the tests move by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the frozen time.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to at.
func (c *fakeClock) Set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

// start is the instant every test clock begins at.
var start = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// moneyOperation is the operation of the tests: it needs two permissions,
// moves value and declares idempotency.
var moneyOperation = Operation{
	Name:        "test.exchange_and_commitment",
	Permissions: []string{model.PermissionExchangeWrite, model.PermissionCommitmentWrite},
	Risk:        model.RiskMoney,
	Idempotent:  true,
}

// world is a database with two active spaces, an authorizer with a fake clock
// and an engine that knows moneyOperation.
type world struct {
	t      *testing.T
	admin  *sql.DB
	api    *sql.DB
	clock  *fakeClock
	auth   *capability.Authorizer
	engine *Engine
	spaceA string
	spaceB string
}

// newWorld builds a world over a disposable database.
func newWorld(t *testing.T, ops ...Operation) *world {
	t.Helper()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	clock := &fakeClock{now: start}
	auth, err := capability.NewAuthorizer(model.KernelCatalog(), clock)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	if len(ops) == 0 {
		ops = []Operation{moneyOperation}
	}
	engine, err := NewEngine(api, auth, ops...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	w := &world{t: t, admin: admin, api: api, clock: clock, auth: auth, engine: engine}
	w.spaceA = w.space("world-a")
	w.spaceB = w.space("world-b")
	return w
}

// space creates an active business space.
func (w *world) space(slug string) string {
	w.t.Helper()
	id, err := state.CreateSpace(context.Background(), w.api, system, state.NewSpace{Kind: model.SpaceKindBusiness, Name: slug, Slug: slug})
	if err != nil {
		w.t.Fatalf("CreateSpace: %v", err)
	}
	w.setStatus(id, "active")
	return id
}

// setStatus changes the status of a space as the administrator.
func (w *world) setStatus(spaceID, status string) {
	w.t.Helper()
	if _, err := w.admin.Exec(`UPDATE kernel.spaces SET status = $2 WHERE id = $1`, spaceID, status); err != nil {
		w.t.Fatalf("set status %s: %v", status, err)
	}
}

// person creates an active participant and returns its actor.
func (w *world) person(spaceID, name string) model.Actor {
	w.t.Helper()
	id, err := state.CreateParticipant(context.Background(), w.api, system, spaceID,
		state.NewParticipant{Kind: model.ParticipantKindPerson, DisplayName: name})
	if err != nil {
		w.t.Fatalf("CreateParticipant: %v", err)
	}
	return model.Actor{Kind: model.ActorPerson, ParticipantID: id}
}

// grantRole creates a role with the permissions, grants it to the actor and
// returns the id of the grant.
func (w *world) grantRole(spaceID string, holder model.Actor, key string, expiresAt *time.Time, permissions ...string) string {
	w.t.Helper()
	role, err := state.CreateRole(context.Background(), w.api, system, spaceID, model.NewRole{Key: key, Name: key, Permissions: permissions})
	if err != nil {
		w.t.Fatalf("CreateRole %s: %v", key, err)
	}
	grant, err := state.GrantRole(context.Background(), w.api, w.clock, system, spaceID,
		model.NewGrant{ParticipantID: holder.ParticipantID, RoleID: role, ExpiresAt: expiresAt})
	if err != nil {
		w.t.Fatalf("GrantRole: %v", err)
	}
	return grant
}

// holder creates a person that holds the permissions through a role.
func (w *world) holder(spaceID, name string, permissions ...string) model.Actor {
	w.t.Helper()
	actor := w.person(spaceID, name)
	w.grantRole(spaceID, actor, name+"_role", nil, permissions...)
	return actor
}

// writer creates a person with both permissions of moneyOperation.
func (w *world) writer(spaceID, name string) model.Actor {
	w.t.Helper()
	return w.holder(spaceID, name, model.PermissionExchangeWrite, model.PermissionCommitmentWrite)
}

// fixtures are the ledger rows an operation of the tests writes against.
type fixtures struct {
	debtor   string
	creditor string
	resource string
	from, to string
}

// ledger creates two accounts of one resource and a debtor and a creditor.
func (w *world) ledger(spaceID string) fixtures {
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
			w.t.Fatalf("OpenAccount %s: %v", code, err)
		}
		return id
	}
	return fixtures{debtor: debtor.ParticipantID, creditor: creditor.ParticipantID, resource: resource, from: account("FROM"), to: account("TO")}
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

// steps records which writes of the test handler ran, so that a test of a
// failure can prove that the earlier writes did happen before the rollback.
type steps struct {
	exchange, commitment, event bool
	calls                       int
}

// handler returns the handler of moneyOperation: it records an exchange,
// creates a commitment and appends an event with an outbox row, naming the ids
// in the payload. The mutate function changes the inputs to make a step fail.
func (f fixtures) handler(t *testing.T, key string, ran *steps, mutate func(*model.NewExchange, *model.NewCommitment)) Handler {
	return func(ctx context.Context, tx *Tx) error {
		ran.calls++
		ex := model.NewExchange{Kind: "test.exchange", IdempotencyKey: key, Entries: []model.NewEntry{
			{AccountID: f.to, ResourceID: f.resource, Amount: amount(t, "10.00")},
			{AccountID: f.from, ResourceID: f.resource, Amount: amount(t, "10.00").Neg()},
		}}
		cm := model.NewCommitment{
			Kind: "test.commitment", DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor,
			ResourceID: f.resource, Amount: amount(t, "10.00"), DueAt: time.Now().Add(48 * time.Hour),
		}
		if mutate != nil {
			mutate(&ex, &cm)
		}
		exchange, err := tx.RecordExchange(ctx, ex)
		if err != nil {
			return err
		}
		ran.exchange = true
		commitment, err := tx.CreateCommitment(ctx, cm)
		if err != nil {
			return err
		}
		ran.commitment = true
		payload, err := json.Marshal(map[string]string{"exchange_id": exchange.ID, "commitment_id": commitment.ID})
		if err != nil {
			return err
		}
		_, err = tx.AppendEvent(ctx, EventInput{Type: "test.exchange_recorded", Version: 1, Payload: payload}, model.SubscriberInternal)
		if err != nil {
			return err
		}
		ran.event = true
		return nil
	}
}

// count counts rows with a query as the administrator.
func (w *world) count(query string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.admin.QueryRow(query, args...).Scan(&n); err != nil {
		w.t.Fatalf("query %q: %v", query, err)
	}
	return n
}

// snapshot counts the rows of every table of the kernel schema, read from the
// catalog of the database so that a table added later is counted without
// editing the test.
func (w *world) snapshot() map[string]int {
	w.t.Helper()
	rows, err := w.admin.Query(`SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'kernel' AND c.relkind IN ('r', 'p') ORDER BY 1`)
	if err != nil {
		w.t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			w.t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) < 14 {
		w.t.Fatalf("found %d kernel tables, expected the whole schema: %v", len(tables), tables)
	}
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		counts[table] = w.count(`SELECT count(*) FROM kernel.` + pgx.Identifier{table}.Sanitize())
	}
	return counts
}

// changed lists the tables whose row count differs from an earlier snapshot.
func (w *world) changed(before map[string]int) map[string]int {
	w.t.Helper()
	out := map[string]int{}
	for table, n := range w.snapshot() {
		if n != before[table] {
			out[table] = n - before[table]
		}
	}
	return out
}

// requireCode fails unless err is a typed error of the given code.
func requireCode(t *testing.T, label string, err error, want model.Code) {
	t.Helper()
	if got, typed := model.CodeOf(err); !typed || got != want {
		t.Fatalf("%s: error = %v, want code %s", label, err, want)
	}
}

// call builds a call with a fresh request id.
func call(actor model.Actor, spaceID string) Call {
	return Call{Actor: actor, SpaceID: spaceID, RequestID: NewRequestID()}
}

// isPgCode reports whether err carries a database error with the given code.
func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
