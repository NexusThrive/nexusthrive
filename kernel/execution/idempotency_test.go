package execution

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// refundOperation is a second idempotent operation with the same permissions as
// moneyOperation, to prove that a key is bound to the operation that used it.
var refundOperation = Operation{
	Name:        "test.refund_exchange",
	Permissions: []string{model.PermissionExchangeWrite, model.PermissionCommitmentWrite},
	Risk:        model.RiskMoney,
	Idempotent:  true,
}

// idemBody is the body of the requests of the tests.
const idemBody = `{"amount":"10.00","to":"account"}`

// idemCall builds an idempotent call with a fresh request id.
func idemCall(actor model.Actor, spaceID, key, body string) Call {
	c := call(actor, spaceID)
	c.IdempotencyKey = key
	c.Request = json.RawMessage(body)
	return c
}

// counter counts how many times the handlers of a test ran.
type counter struct{ n atomic.Int32 }

// Count returns how many times the handler ran.
func (c *counter) Count() int { return int(c.n.Load()) }

// recordHandler is the handler of the idempotent tests: it counts its runs,
// records one exchange without a key (the engine derives it) and answers with
// the id of the exchange and the number of the run.
func (f fixtures) recordHandler(t *testing.T, runs *counter) ResultHandler {
	return func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
		run := runs.n.Add(1)
		exchange, err := tx.RecordExchange(ctx, model.NewExchange{Kind: "test.exchange", Entries: []model.NewEntry{
			{AccountID: f.to, ResourceID: f.resource, Amount: amount(t, "10.00")},
			{AccountID: f.from, ResourceID: f.resource, Amount: amount(t, "10.00").Neg()},
		}})
		if err != nil {
			return nil, err
		}
		return json.RawMessage(fmt.Sprintf(`{"run": %d,"exchange_id":%q}`, run, exchange.ID)), nil
	}
}

// at moves the clock of the world to the time given, behind the database.
func (w *world) at(t time.Time) { w.clock.Set(t.UTC().Truncate(time.Microsecond)) }

// behind returns the instant d before the real time, in whole microseconds.
func behind(d time.Duration) time.Time { return time.Now().UTC().Add(-d).Truncate(time.Microsecond) }

// recordsOf lists the records of a key as the administrator: generation, id and
// the text of the response.
func (w *world) recordsOf(key string) (generations []int, ids []string, responses []string) {
	w.t.Helper()
	rows, err := w.admin.Query(`SELECT generation, id::text, response::text FROM kernel.idempotency_records WHERE key = $1 ORDER BY generation`, key)
	if err != nil {
		w.t.Fatalf("list records: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var generation int
		var id, response string
		if err := rows.Scan(&generation, &id, &response); err != nil {
			w.t.Fatalf("scan record: %v", err)
		}
		generations, ids, responses = append(generations, generation), append(ids, id), append(responses, response)
	}
	return generations, ids, responses
}

// TestFirstRunStoresHashResponseAndExpiryInSameTransaction guards criterion 1:
// the first execution runs the handler once and stores, in its own
// transaction, a record with the fingerprint, the response and an expiry 24
// hours after the creation by the clock. The record is not visible while the
// handler runs and it is there with the exchange when the call returns.
func TestFirstRunStoresHashResponseAndExpiryInSameTransaction(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	created := behind(30 * time.Hour)
	w.at(created)
	var runs counter

	inner := f.recordHandler(t, &runs)
	var duringHandler int
	got, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-first", idemBody), moneyOperation.Name,
		func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
			duringHandler = w.count(`SELECT count(*) FROM kernel.idempotency_records`)
			return inner(ctx, tx)
		})
	if err != nil {
		t.Fatalf("RunIdempotent: %v", err)
	}
	if runs.Count() != 1 || got.Replayed {
		t.Errorf("handler ran %d times, replayed = %v; want 1 and false", runs.Count(), got.Replayed)
	}
	if duringHandler != 0 {
		t.Errorf("the record was visible to other connections while the handler ran (%d)", duringHandler)
	}

	hash, err := model.RequestHash(moneyOperation.Name, holder, []byte(idemBody))
	if err != nil {
		t.Fatalf("RequestHash: %v", err)
	}
	var id, actor, operation, response string
	var storedHash []byte
	var generation int
	var createdAt, expiresAt time.Time
	err = w.admin.QueryRow(`SELECT id::text, actor_participant_id::text, operation, request_hash, response::text, generation, created_at, expires_at
		FROM kernel.idempotency_records WHERE space_id = $1 AND key = 'k-first'`, w.spaceA).
		Scan(&id, &actor, &operation, &storedHash, &response, &generation, &createdAt, &expiresAt)
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	if !bytes.Equal(storedHash, hash[:]) || actor != holder.ParticipantID || operation != moneyOperation.Name || generation != 1 {
		t.Errorf("record: actor %s operation %s generation %d hash %x", actor, operation, generation, storedHash)
	}
	if !createdAt.Equal(created) || !expiresAt.Equal(created.Add(model.IdempotencyTTL)) {
		t.Errorf("created %v expires %v, want %v and 24 hours later", createdAt, expiresAt, created)
	}
	if string(got.Body) != response {
		t.Errorf("the response returned is %s, the stored one is %s", got.Body, response)
	}
	var key string
	if err := w.admin.QueryRow(`SELECT idempotency_key FROM kernel.exchanges WHERE space_id = $1`, w.spaceA).Scan(&key); err != nil || key != id+":1" {
		t.Errorf("exchange key = %q (%v), want the execution id and a counter %q", key, err, id+":1")
	}
}

// TestFailedCommitLeavesNoRecordNorExchange guards criterion 1 against a late
// failure: when the commit fails after the record was inserted, the record and
// the exchange both disappear, and the key can be used again.
func TestFailedCommitLeavesNoRecordNorExchange(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	before := w.snapshot()
	var runs counter

	inner := f.recordHandler(t, &runs)
	_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-late-failure", idemBody), moneyOperation.Name,
		func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
			body, err := inner(ctx, tx)
			if err != nil {
				return nil, err
			}
			// An exchange with no entries breaks a check that runs when the transaction commits.
			id, err := uuid.NewV7()
			if err != nil {
				return nil, err
			}
			_, err = tx.conn.ExecContext(ctx,
				`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
				 VALUES ($1, kernel.current_space_id(), 'test.exchange', $2, $3)`, id.String(), "late-"+id.String(), holder.ParticipantID)
			return body, err
		})
	requireCode(t, "failed commit", err, model.CodeRuleViolation)
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the failed commit left %v", changed)
	}
	if got, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-late-failure", idemBody), moneyOperation.Name, f.recordHandler(t, &runs)); err != nil || got.Replayed {
		t.Errorf("the key after the failure: %v, replayed %v; want a new execution", err, got.Replayed)
	}
}

// TestRepeatWithSameKeyAndBodyReplaysWithoutHandler guards criterion 2: the
// same key and the same body, even written in another order, return the stored
// response byte for byte without running the handler or writing anything.
func TestRepeatWithSameKeyAndBodyReplaysWithoutHandler(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	var runs counter

	first, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-repeat", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	if err != nil || first.Replayed {
		t.Fatalf("first run: %v, replayed %v", err, first.Replayed)
	}
	before := w.snapshot()
	exchanges, entries := w.count(`SELECT count(*) FROM kernel.exchanges`), w.count(`SELECT count(*) FROM kernel.entries`)

	for i, body := range []string{idemBody, `{"to":"account","amount":"10.00"}`, " { \"to\" : \"account\", \"amount\":\"10.00\" } \n"} {
		failing := func(context.Context, *Tx) (json.RawMessage, error) {
			t.Error("the handler ran for a repeated request")
			return nil, errors.New("must not run")
		}
		again, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-repeat", body), moneyOperation.Name, failing)
		if err != nil {
			t.Fatalf("repeat %d: %v", i, err)
		}
		if !again.Replayed || !bytes.Equal(again.Body, first.Body) {
			t.Errorf("repeat %d: replayed %v, body %s, want replayed and %s", i, again.Replayed, again.Body, first.Body)
		}
	}
	if runs.Count() != 1 {
		t.Errorf("the handler ran %d times, want 1", runs.Count())
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the repeats changed %v", changed)
	}
	if got := w.count(`SELECT count(*) FROM kernel.exchanges`); got != exchanges || w.count(`SELECT count(*) FROM kernel.entries`) != entries {
		t.Errorf("the repeats wrote exchanges or entries")
	}
}

// TestReplayIsTheStoredFormOfTheFirstResponse guards that the first execution
// answers with the same bytes a replay does: the form the database stores, not
// the form the handler wrote.
func TestReplayIsTheStoredFormOfTheFirstResponse(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	holder := w.writer(w.spaceA, "holder")
	handler := func(context.Context, *Tx) (json.RawMessage, error) {
		return json.RawMessage(`{"b": 2,  "a":[1,2]}`), nil
	}

	first, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-form", idemBody), moneyOperation.Name, handler)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-form", idemBody), moneyOperation.Name, handler)
	if err != nil || !again.Replayed {
		t.Fatalf("repeat: %v, replayed %v", err, again.Replayed)
	}
	if string(first.Body) != `{"a": [1, 2], "b": 2}` || !bytes.Equal(first.Body, again.Body) {
		t.Errorf("first %s, replay %s; want the stored form on both", first.Body, again.Body)
	}
}

// TestReplayStillChecksPermission guards the order of the engine: a caller that
// lacks the permission, or whose space is suspended, is refused before any
// record is read, so it cannot tell which keys exist nor receive a stored
// response.
func TestReplayStillChecksPermission(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	stranger := w.person(w.spaceA, "stranger")
	var runs counter
	if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-secret", idemBody), moneyOperation.Name, f.recordHandler(t, &runs)); err != nil {
		t.Fatalf("first: %v", err)
	}

	for name, actor := range map[string]model.Actor{"no grant": stranger, "null actor": {}, "unknown kind": {Kind: "robot", ParticipantID: holder.ParticipantID}} {
		for _, body := range []string{idemBody, `{"amount":"99.00"}`} {
			got, err := w.engine.RunIdempotent(context.Background(), idemCall(actor, w.spaceA, "k-secret", body), moneyOperation.Name, f.recordHandler(t, &runs))
			requireCode(t, name, err, model.CodePermissionDenied)
			if len(got.Body) != 0 || got.Replayed {
				t.Errorf("%s: the refusal carried a response %s", name, got.Body)
			}
		}
	}
	w.setStatus(w.spaceA, "suspended")
	_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-secret", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	requireCode(t, "suspended space", err, model.CodeSpaceSuspended)
	if runs.Count() != 1 {
		t.Errorf("the handler ran %d times, want 1", runs.Count())
	}
}

// TestRepeatWithOtherBodyIsIdempotencyConflictAndWritesNothing guards criterion
// 3: the same key with another body is idempotency_conflict with a fixed
// message that says nothing of the stored record, and writes nothing.
func TestRepeatWithOtherBodyIsIdempotencyConflictAndWritesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	var runs counter
	first, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-conflict", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	before := w.snapshot()

	var messages []string
	for name, body := range map[string]string{
		"another amount": `{"amount":"11.00","to":"account"}`, "an extra field": `{"amount":"10.00","to":"account","x":1}`,
		"another number literal": `{"amount":"10.00","to":"account","n":1.0}`, "an empty object": `{}`, "an array": `[]`,
	} {
		got, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-conflict", body), moneyOperation.Name, f.recordHandler(t, &runs))
		requireCode(t, name, err, model.CodeIdempotencyConflict)
		if len(got.Body) != 0 {
			t.Errorf("%s: the conflict carried a body %s", name, got.Body)
		}
		var typed *model.Error
		if errors.As(err, &typed) {
			messages = append(messages, typed.Message)
			if strings.Contains(typed.Error(), string(first.Body)) || strings.Contains(typed.Error(), "exchange_id") {
				t.Errorf("%s: the message reveals the stored response: %v", name, typed)
			}
		}
	}
	for _, message := range messages {
		if message != messages[0] {
			t.Errorf("the message of the conflict changes: %q and %q", messages[0], message)
		}
	}
	if runs.Count() != 1 {
		t.Errorf("the handler ran %d times, want 1", runs.Count())
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the conflicts changed %v", changed)
	}
}

// TestOtherOperationOrActorWithSameKeyIsConflict guards that the fingerprint
// binds the operation and the actor: with the same key and body, another
// operation or another actor gets a conflict and never the response of someone
// else.
func TestOtherOperationOrActorWithSameKeyIsConflict(t *testing.T) {
	t.Parallel()
	w := newWorld(t, moneyOperation, refundOperation)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	colleague := w.writer(w.spaceA, "colleague")
	var runs counter
	if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-bound", idemBody), moneyOperation.Name, f.recordHandler(t, &runs)); err != nil {
		t.Fatalf("first: %v", err)
	}
	before := w.snapshot()

	_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-bound", idemBody), refundOperation.Name, f.recordHandler(t, &runs))
	requireCode(t, "another operation", err, model.CodeIdempotencyConflict)
	_, err = w.engine.RunIdempotent(context.Background(), idemCall(colleague, w.spaceA, "k-bound", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	requireCode(t, "another actor", err, model.CodeIdempotencyConflict)
	if runs.Count() != 1 {
		t.Errorf("the handler ran %d times, want 1", runs.Count())
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the conflicts changed %v", changed)
	}
	// The original caller still gets its own response.
	if got, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-bound", idemBody), moneyOperation.Name, f.recordHandler(t, &runs)); err != nil || !got.Replayed {
		t.Errorf("the original caller after the conflicts: %v, replayed %v", err, got.Replayed)
	}
}

// TestTamperedFingerprintIsConflict guards the full length of the comparison: a
// stored fingerprint that differs in one bit, in any of its bytes, no longer
// matches the request.
func TestTamperedFingerprintIsConflict(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	var runs counter
	if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-bits", idemBody), moneyOperation.Name, f.recordHandler(t, &runs)); err != nil {
		t.Fatalf("first: %v", err)
	}
	for _, position := range []int{1, 16, 32} {
		w.tamper(`UPDATE kernel.idempotency_records SET request_hash = set_bit(request_hash, ($1::int - 1) * 8, 1 - get_bit(request_hash, ($1::int - 1) * 8)) WHERE key = 'k-bits'`, position)
		_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-bits", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
		requireCode(t, fmt.Sprintf("bit of byte %d flipped", position), err, model.CodeIdempotencyConflict)
		w.tamper(`UPDATE kernel.idempotency_records SET request_hash = set_bit(request_hash, ($1::int - 1) * 8, 1 - get_bit(request_hash, ($1::int - 1) * 8)) WHERE key = 'k-bits'`, position)
	}
	if got, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-bits", idemBody), moneyOperation.Name, f.recordHandler(t, &runs)); err != nil || !got.Replayed {
		t.Errorf("after restoring the fingerprint: %v, replayed %v", err, got.Replayed)
	}
}

// tamper runs a statement as the administrator with the trigger that forbids
// changing a record switched off, and switches it on again after. It stands for
// someone who rewrote a stored record outside the engine.
func (w *world) tamper(query string, args ...any) {
	w.t.Helper()
	for _, step := range []string{`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER idempotency_records_no_update`, query,
		`ALTER TABLE kernel.idempotency_records ENABLE TRIGGER idempotency_records_no_update`} {
		var err error
		if step == query {
			_, err = w.admin.Exec(step, args...)
		} else {
			_, err = w.admin.Exec(step)
		}
		if err != nil {
			w.t.Fatalf("tamper %q: %v", step, err)
		}
	}
}

// TestSameKeyInOtherSpaceIsFirstRun guards criterion 4: the key belongs to the
// space, so the same key with the same body in another space runs the handler
// and stores its own record.
func TestSameKeyInOtherSpaceIsFirstRun(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	fA, fB := w.ledger(w.spaceA), w.ledger(w.spaceB)
	holderA, holderB := w.writer(w.spaceA, "holder_a"), w.writer(w.spaceB, "holder_b")
	var runsA, runsB counter

	a, err := w.engine.RunIdempotent(context.Background(), idemCall(holderA, w.spaceA, "k-shared", idemBody), moneyOperation.Name, fA.recordHandler(t, &runsA))
	if err != nil {
		t.Fatalf("space A: %v", err)
	}
	b, err := w.engine.RunIdempotent(context.Background(), idemCall(holderB, w.spaceB, "k-shared", idemBody), moneyOperation.Name, fB.recordHandler(t, &runsB))
	if err != nil || b.Replayed {
		t.Fatalf("space B: %v, replayed %v; want a first run", err, b.Replayed)
	}
	if runsA.Count() != 1 || runsB.Count() != 1 || bytes.Equal(a.Body, b.Body) {
		t.Errorf("handlers ran %d and %d times; the two responses are the same: %v", runsA.Count(), runsB.Count(), bytes.Equal(a.Body, b.Body))
	}
	if got := w.count(`SELECT count(DISTINCT space_id) FROM kernel.idempotency_records WHERE key = 'k-shared'`); got != 2 {
		t.Errorf("records of the key live in %d spaces, want 2", got)
	}
}

// TestIdempotencyRecordsIsolationBetweenSpaces guards criterion 8 at the engine:
// the handler of a call in space B, reading raw, sees no record of space A.
func TestIdempotencyRecordsIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	fA := w.ledger(w.spaceA)
	holderA, holderB := w.writer(w.spaceA, "holder_a"), w.writer(w.spaceB, "holder_b")
	var runs counter
	if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holderA, w.spaceA, "k-iso", idemBody), moneyOperation.Name, fA.recordHandler(t, &runs)); err != nil {
		t.Fatalf("space A: %v", err)
	}
	var visible int
	_, err := w.engine.RunIdempotent(context.Background(), idemCall(holderB, w.spaceB, "k-other", idemBody), moneyOperation.Name,
		func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
			err := tx.conn.QueryRowContext(ctx, `SELECT count(*) FROM kernel.idempotency_records`).Scan(&visible)
			return json.RawMessage(`{}`), err
		})
	if err != nil {
		t.Fatalf("space B: %v", err)
	}
	if visible != 0 {
		t.Errorf("space B sees %d records of space A", visible)
	}
}

// TestExpiredRecordIsReplacedByNextGeneration guards criterion 6: after 24 hours
// the same key runs again as a new execution with a new exchange key, the
// record of the first execution stays as it was, and the new generation is the
// one in force.
func TestExpiredRecordIsReplacedByNextGeneration(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	start := behind(30 * time.Hour)
	var runs counter
	run := func() (Response, error) {
		return w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-expire", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	}

	w.at(start)
	first, err := run()
	if err != nil || first.Replayed {
		t.Fatalf("first: %v, replayed %v", err, first.Replayed)
	}
	generations, ids, responses := w.recordsOf("k-expire")
	if len(ids) != 1 {
		t.Fatalf("records after the first run = %v", generations)
	}
	w.at(start.Add(model.IdempotencyTTL - time.Second))
	if again, err := run(); err != nil || !again.Replayed || runs.Count() != 1 {
		t.Fatalf("a second before the expiry: %v, replayed %v, runs %d", err, again.Replayed, runs.Count())
	}

	w.at(start.Add(model.IdempotencyTTL + time.Second))
	second, err := run()
	if err != nil || second.Replayed || runs.Count() != 2 {
		t.Fatalf("after the expiry: %v, replayed %v, runs %d; want a new execution", err, second.Replayed, runs.Count())
	}
	if bytes.Equal(first.Body, second.Body) {
		t.Error("the new execution returned the old response")
	}
	generations2, ids2, responses2 := w.recordsOf("k-expire")
	if len(ids2) != 2 || generations2[0] != 1 || generations2[1] != 2 || ids2[0] != ids[0] || responses2[0] != responses[0] {
		t.Errorf("records = %v %v; want the first intact and a second of generation 2", generations2, ids2)
	}
	keys := w.stringColumn(`SELECT idempotency_key FROM kernel.exchanges ORDER BY recorded_at`)
	if len(keys) != 2 || keys[0] == keys[1] || keys[0] != ids[0]+":1" || keys[1] != ids2[1]+":1" {
		t.Errorf("exchange keys = %v, want one per execution", keys)
	}
	if again, err := run(); err != nil || !again.Replayed || !bytes.Equal(again.Body, second.Body) || runs.Count() != 2 {
		t.Errorf("the next repeat: %v, replayed %v, runs %d; want the second generation replayed", err, again.Replayed, runs.Count())
	}
}

// stringColumn returns the first column of a query as strings.
func (w *world) stringColumn(query string) []string {
	w.t.Helper()
	rows, err := w.admin.Query(query)
	if err != nil {
		w.t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			w.t.Fatalf("scan: %v", err)
		}
		out = append(out, value)
	}
	return out
}

// TestRecordAt23h59IsStillInForce and TestRecordExpiresAtExactly24Hours guard
// the two edges of the criterion 6 to the microsecond.
func TestRecordAt23h59IsStillInForce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	start := behind(30 * time.Hour)
	var runs counter
	run := func() (Response, error) {
		return w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-edge", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	}
	w.at(start)
	if _, err := run(); err != nil {
		t.Fatalf("first: %v", err)
	}
	for name, at := range map[string]time.Time{
		"23 h 59 min": start.Add(23*time.Hour + 59*time.Minute), "a microsecond before": start.Add(model.IdempotencyTTL - time.Microsecond),
	} {
		w.at(at)
		if again, err := run(); err != nil || !again.Replayed {
			t.Errorf("%s: %v, replayed %v; want the record in force", name, err, again.Replayed)
		}
	}
	if runs.Count() != 1 {
		t.Errorf("the handler ran %d times, want 1", runs.Count())
	}
}

// TestRecordExpiresAtExactly24Hours guards the other edge: at the very instant
// of the expiry the record is no longer in force.
func TestRecordExpiresAtExactly24Hours(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	start := behind(30 * time.Hour)
	var runs counter
	run := func() (Response, error) {
		return w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-exact", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	}
	w.at(start)
	if _, err := run(); err != nil {
		t.Fatalf("first: %v", err)
	}
	w.at(start.Add(model.IdempotencyTTL))
	if again, err := run(); err != nil || again.Replayed || runs.Count() != 2 {
		t.Errorf("at exactly 24 hours: %v, replayed %v, runs %d; want a new execution", err, again.Replayed, runs.Count())
	}
}

// TestClockAheadOfTheDatabaseCannotExpireOrBreakTheRecord guards the ceiling of
// the injectable clock: a clock ahead of the database, by a second or by years,
// neither makes the first run fail nor expires a record that is in force.
func TestClockAheadOfTheDatabaseCannotExpireOrBreakTheRecord(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	var runs counter
	for i, ahead := range []time.Duration{time.Second, 48 * time.Hour, 10 * 365 * 24 * time.Hour} {
		key := fmt.Sprintf("k-ahead-%d", i)
		run := func() (Response, error) {
			return w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, key, idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
		}
		w.at(time.Now().Add(ahead))
		first, err := run()
		if err != nil || first.Replayed {
			t.Fatalf("%v ahead, first run: %v, replayed %v", ahead, err, first.Replayed)
		}
		again, err := run()
		if err != nil || !again.Replayed || !bytes.Equal(again.Body, first.Body) {
			t.Errorf("%v ahead, repeat: %v, replayed %v; want the record in force", ahead, err, again.Replayed)
		}
		var createdAhead bool
		if err := w.admin.QueryRow(`SELECT created_at > now() FROM kernel.idempotency_records WHERE key = $1`, key).Scan(&createdAhead); err != nil || createdAhead {
			t.Errorf("%v ahead: the record was born in the future of the database (%v)", ahead, err)
		}
	}
	if runs.Count() != 3 {
		t.Errorf("the handler ran %d times, want 3", runs.Count())
	}
}

// TestRuleErrorIsNotCachedAndRetryRuns guards criterion 7: a handler that fails
// leaves no record, so the same key and body run again, and what the first
// attempt wrote disappears with it.
func TestRuleErrorIsNotCachedAndRetryRuns(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	before := w.snapshot()
	var runs counter

	inner := f.recordHandler(t, &runs)
	for name, failure := range map[string]error{
		"a rule violation": model.NewError(model.CodeRuleViolation, "refused", nil), "a plain error": errors.New("boom"),
	} {
		_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-fail", idemBody), moneyOperation.Name,
			func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
				if _, err := inner(ctx, tx); err != nil {
					return nil, err
				}
				return nil, failure
			})
		if err == nil || !errors.Is(err, failure) {
			t.Fatalf("%s: error = %v, want the failure of the handler", name, err)
		}
		if changed := w.changed(before); len(changed) != 0 {
			t.Errorf("%s: the failed attempt left %v", name, changed)
		}
	}
	got, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-fail", idemBody), moneyOperation.Name, f.recordHandler(t, &runs))
	if err != nil || got.Replayed {
		t.Fatalf("the retry: %v, replayed %v; want a new execution", err, got.Replayed)
	}
	if generations, _, _ := w.recordsOf("k-fail"); len(generations) != 1 || generations[0] != 1 {
		t.Errorf("records after the retry = %v, want one of generation 1", generations)
	}
}

// TestHandlerPanicLeavesNoRecord guards criterion 7 for a panic: it comes out as
// a PanicError, nothing stays, and the same key runs again.
func TestHandlerPanicLeavesNoRecord(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	before := w.snapshot()
	var runs counter

	inner := f.recordHandler(t, &runs)
	_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-panic", idemBody), moneyOperation.Name,
		func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
			if _, err := inner(ctx, tx); err != nil {
				return nil, err
			}
			panic("boom")
		})
	var panicked *PanicError
	if !errors.As(err, &panicked) {
		t.Fatalf("error = %v, want a PanicError", err)
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the panic left %v", changed)
	}
	if got, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-panic", idemBody), moneyOperation.Name, f.recordHandler(t, &runs)); err != nil || got.Replayed {
		t.Errorf("the retry after the panic: %v, replayed %v", err, got.Replayed)
	}
}

// TestInvalidResponseIsInvalidInputAndNotStored guards the shape of a response:
// anything but a JSON object of at most 64 KiB is refused, nothing is stored and
// the key is free for another attempt.
func TestInvalidResponseIsInvalidInputAndNotStored(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	before := w.snapshot()
	var runs counter
	inner := f.recordHandler(t, &runs)

	for name, response := range map[string]string{
		"an array": `[]`, "a string": `"ok"`, "null": `null`, "a number": `1`, "nothing": ``, "not JSON": `{`,
		"duplicate keys": `{"a":1,"a":2}`, "over 64 KiB": `{"p":"` + strings.Repeat("a", 64<<10) + `"}`,
	} {
		_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-response", idemBody), moneyOperation.Name,
			func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
				if _, err := inner(ctx, tx); err != nil {
					return nil, err
				}
				return json.RawMessage(response), nil
			})
		requireCode(t, name, err, model.CodeInvalidInput)
		if changed := w.changed(before); len(changed) != 0 {
			t.Errorf("%s: the refused response left %v", name, changed)
		}
	}
	if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-response", idemBody), moneyOperation.Name, f.recordHandler(t, &runs)); err != nil {
		t.Errorf("a valid response after the refused ones: %v", err)
	}
}

// closedEngine returns an engine over a pool that is already closed: any call
// that opens a transaction fails with a pool error, so a typed error that comes
// out proves that nothing was opened.
func (w *world) closedEngine(t *testing.T) *Engine {
	t.Helper()
	closed, err := sql.Open("pgx", "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	closed.Close()
	engine, err := NewEngine(closed, w.auth, moneyOperation, commitmentOnly)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

// TestMoneyOperationWithoutKeyIsInvalidInput guards criterion 9: a missing or
// malformed key, and a body that is not canonical JSON, are invalid_input before
// anything opens, so nothing is written and the handler never runs.
func TestMoneyOperationWithoutKeyIsInvalidInput(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	holder := w.writer(w.spaceA, "holder")
	engine := w.closedEngine(t)
	ran := false
	handler := func(context.Context, *Tx) (json.RawMessage, error) {
		ran = true
		return json.RawMessage(`{}`), nil
	}
	deep := strings.Repeat("[", 65) + strings.Repeat("]", 65)
	for name, c := range map[string]Call{
		"no key":                   idemCall(holder, w.spaceA, "", idemBody),
		"blank key":                idemCall(holder, w.spaceA, "   ", idemBody),
		"key ending in a newline":  idemCall(holder, w.spaceA, "key\n", idemBody),
		"key with a space":         idemCall(holder, w.spaceA, "a b", idemBody),
		"key of 129":               idemCall(holder, w.spaceA, strings.Repeat("k", 129), idemBody),
		"key not ASCII":            idemCall(holder, w.spaceA, "clavé", idemBody),
		"no body":                  {Actor: holder, SpaceID: w.spaceA, RequestID: "r1", IdempotencyKey: "k"},
		"empty body":               idemCall(holder, w.spaceA, "k", ""),
		"blank body":               idemCall(holder, w.spaceA, "k", "  "),
		"body with duplicate keys": idemCall(holder, w.spaceA, "k", `{"a":1,"a":2}`),
		"body with invalid UTF-8":  idemCall(holder, w.spaceA, "k", "{\"a\":\"\xff\"}"),
		"body too deep":            idemCall(holder, w.spaceA, "k", deep),
		"body over 1 MiB":          idemCall(holder, w.spaceA, "k", `"`+strings.Repeat("a", 1<<20)+`"`),
		"body with a tail":         idemCall(holder, w.spaceA, "k", `{} {}`),
		"body that is not JSON":    idemCall(holder, w.spaceA, "k", `{`),
	} {
		_, err := engine.RunIdempotent(context.Background(), c, moneyOperation.Name, handler)
		requireCode(t, name, err, model.CodeInvalidInput)
	}
	if ran {
		t.Error("the handler ran for an invalid call")
	}
	before := w.snapshot()
	for _, key := range []string{"", "key\n", strings.Repeat("k", 129)} {
		_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, key, idemBody), moneyOperation.Name, handler)
		requireCode(t, fmt.Sprintf("key %q", key), err, model.CodeInvalidInput)
	}
	if changed := w.changed(before); len(changed) != 0 || ran {
		t.Errorf("invalid keys changed %v", changed)
	}
	// The longest key and every allowed character are accepted.
	for i, key := range []string{strings.Repeat("k", 128), "A.b_c:d-1", "0"} {
		if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, key, idemBody), moneyOperation.Name, handler); err != nil {
			t.Errorf("key %d (%q): %v", i, key, err)
		}
	}
}

// TestRunRejectsIdempotentOperation guards that Run, which has no key and
// records nothing, refuses an operation declared idempotent, and that
// RunIdempotent refuses one that is not, one that only reads and a name of the
// kernel namespace, always before anything opens.
func TestRunRejectsIdempotentOperation(t *testing.T) {
	t.Parallel()
	read := Operation{Name: "test.read_idem", Permissions: []string{model.PermissionLedgerRead}, Risk: model.RiskRead, Idempotent: true}
	w := newWorld(t, moneyOperation, commitmentOnly, read)
	holder := w.holder(w.spaceA, "holder", model.PermissionExchangeWrite, model.PermissionCommitmentWrite, model.PermissionLedgerRead)
	engine := w.closedEngine(t)
	ran := false

	for name, c := range map[string]Call{"with a key": idemCall(holder, w.spaceA, "k", idemBody), "without one": call(holder, w.spaceA)} {
		err := engine.Run(context.Background(), c, moneyOperation.Name, func(context.Context, *Tx) error { ran = true; return nil })
		requireCode(t, "Run of an idempotent operation "+name, err, model.CodeInvalidInput)
		err = w.engine.Run(context.Background(), c, moneyOperation.Name, func(context.Context, *Tx) error { ran = true; return nil })
		requireCode(t, "Run of an idempotent operation "+name+" (live pool)", err, model.CodeInvalidInput)
	}
	handler := func(context.Context, *Tx) (json.RawMessage, error) { ran = true; return json.RawMessage(`{}`), nil }
	for name, op := range map[string]string{
		"an operation that is not idempotent": commitmentOnly.Name, "an unknown operation": "test.unknown", "an operation of the kernel": "kernel.timer.fire",
		"the kernel namespace in capitals": " KERNEL.timer.fire", "an empty name": "",
	} {
		_, err := engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k", idemBody), op, handler)
		requireCode(t, name, err, model.CodeInvalidInput)
	}
	if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-read", idemBody), read.Name, handler); err == nil {
		t.Error("an idempotent read operation was run")
	} else {
		requireCode(t, "idempotent read operation", err, model.CodeInvalidInput)
	}
	if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k", idemBody), moneyOperation.Name, nil); err == nil {
		t.Error("RunIdempotent accepted a nil handler")
	} else {
		requireCode(t, "nil handler", err, model.CodeInvalidInput)
	}
	if ran {
		t.Error("a handler ran for a refused call")
	}
	// The control: the same operation through RunIdempotent runs.
	if _, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-control", idemBody), moneyOperation.Name, handler); err != nil {
		t.Errorf("RunIdempotent of an idempotent operation: %v", err)
	}
}

// TestRunIdempotentRefusesAnActorWithoutParticipant guards that a record always
// names a participant: an actor that stands for none is refused.
func TestRunIdempotentRefusesAnActorWithoutParticipant(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	before := w.snapshot()
	_, err := w.engine.RunIdempotent(context.Background(), idemCall(model.Actor{Kind: model.ActorSystem}, w.spaceA, "k-system", idemBody), moneyOperation.Name,
		func(context.Context, *Tx) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	requireCode(t, "system actor without participant", err, model.CodePermissionDenied)
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the refused call changed %v", changed)
	}
}

// TestEngineDerivesTheExchangeKeyAndRejectsTheHandlersKey guards ADR-0030's
// amendment: an idempotent execution names its exchanges by its own id and a
// counter, and a handler that brings a key of its own is refused before
// anything is written.
func TestEngineDerivesTheExchangeKeyAndRejectsTheHandlersKey(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	pair := func(key string) model.NewExchange {
		return model.NewExchange{Kind: "test.exchange", IdempotencyKey: key, Entries: []model.NewEntry{
			{AccountID: f.to, ResourceID: f.resource, Amount: amount(t, "1.00")},
			{AccountID: f.from, ResourceID: f.resource, Amount: amount(t, "1.00").Neg()},
		}}
	}

	before := w.snapshot()
	_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-own-key", idemBody), moneyOperation.Name,
		func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
			_, err := tx.RecordExchange(ctx, pair("handler-key"))
			return json.RawMessage(`{}`), err
		})
	requireCode(t, "key of the handler", err, model.CodeInvalidInput)
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the refused key left %v", changed)
	}

	got, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-two", idemBody), moneyOperation.Name,
		func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
			for i := 0; i < 2; i++ {
				if _, err := tx.RecordExchange(ctx, pair("")); err != nil {
					return nil, err
				}
			}
			return json.RawMessage(`{}`), nil
		})
	if err != nil || got.Replayed {
		t.Fatalf("two exchanges: %v", err)
	}
	_, ids, _ := w.recordsOf("k-two")
	keys := w.stringColumn(`SELECT idempotency_key FROM kernel.exchanges ORDER BY idempotency_key`)
	if len(keys) != 2 || keys[0] != ids[0]+":1" || keys[1] != ids[0]+":2" {
		t.Errorf("exchange keys = %v, want %s:1 and %s:2", keys, ids[0], ids[0])
	}
}

// TestRunIdempotentLeavesNoOpenTransaction guards the pool: after a first run, a
// repeat, a conflict, a failure and a panic, on a pool of one connection, the
// connection is back and serves the next call at once.
func TestRunIdempotentLeavesNoOpenTransaction(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	w.api.SetMaxOpenConns(1)
	var runs counter
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	steps := []struct {
		name string
		body string
		fn   ResultHandler
	}{
		{"first run", idemBody, f.recordHandler(t, &runs)},
		{"replay", idemBody, f.recordHandler(t, &runs)},
		{"conflict", `{"amount":"1"}`, f.recordHandler(t, &runs)},
		{"failure", `{"other":1}`, func(context.Context, *Tx) (json.RawMessage, error) { return nil, errors.New("failed") }},
		{"panic", `{"other":2}`, func(context.Context, *Tx) (json.RawMessage, error) { panic("boom") }},
	}
	for _, s := range steps {
		key := "k-pool"
		if strings.HasPrefix(s.body, `{"other"`) {
			key = "k-pool-" + s.name
		}
		_, _ = w.engine.RunIdempotent(ctx, idemCall(holder, w.spaceA, key, s.body), moneyOperation.Name, s.fn)
		var setting sql.NullString
		if err := w.api.QueryRowContext(ctx, `SELECT current_setting('kernel.space_id', true)`).Scan(&setting); err != nil {
			t.Fatalf("%s: the pool did not serve the next statement: %v", s.name, err)
		}
		if setting.String != "" || w.api.Stats().InUse != 0 {
			t.Errorf("%s: space %q, %d connections in use after the call", s.name, setting.String, w.api.Stats().InUse)
		}
	}
}

// TestRunIdempotentSwallowedDatabaseErrorCannotCommit guards that a handler that
// hides a database error and returns a response still cannot commit: the
// transaction is aborted, nothing is stored and the error is the rollback of the
// commit, as it is for Run.
func TestRunIdempotentSwallowedDatabaseErrorCannotCommit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	before := w.snapshot()
	var runs counter

	inner := f.recordHandler(t, &runs)
	_, err := w.engine.RunIdempotent(context.Background(), idemCall(holder, w.spaceA, "k-swallow", idemBody), moneyOperation.Name,
		func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
			body, err := inner(ctx, tx)
			if err != nil {
				return nil, err
			}
			if _, err := tx.conn.ExecContext(ctx, `SELECT 1/0`); err == nil {
				t.Error("the failing statement did not fail")
			}
			return body, nil
		})
	if !errors.Is(err, pgx.ErrTxCommitRollback) {
		t.Errorf("error = %v, want the commit rollback of the driver", err)
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the aborted call left %v", changed)
	}
}

// TestRunIdempotentTypedErrorsOfTheStore guards that a raw uniqueness error of
// the records never reaches a caller: the classification maps the constraints of
// the table to a typed rule_violation.
func TestRunIdempotentTypedErrorsOfTheStore(t *testing.T) {
	t.Parallel()
	for _, constraint := range []string{"idempotency_records_pkey", "idempotency_records_key_in_force", "idempotency_records_id_space_key"} {
		err := classify("test.op", fmt.Errorf("commit: %w", &pgconn.PgError{Code: "23505", ConstraintName: constraint}))
		requireCode(t, constraint, err, model.CodeRuleViolation)
	}
	// A uniqueness error of another table is not disguised.
	if _, typed := model.CodeOf(classify("test.op", fmt.Errorf("commit: %w", &pgconn.PgError{Code: "23505", ConstraintName: "other_pkey"}))); typed {
		t.Error("a uniqueness error of another table became typed")
	}
}

// TestFixedMessageOfTheConflict pins the message of idempotency_conflict, so that
// a change to it is deliberate.
func TestFixedMessageOfTheConflict(t *testing.T) {
	t.Parallel()
	if conflictMessage != "the idempotency key was used by a different request" {
		t.Errorf("conflictMessage = %q", conflictMessage)
	}
}
