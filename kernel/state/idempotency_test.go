package state_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// idempotencyFixtures are two spaces with an actor in each.
type idempotencyFixtures struct {
	admin, api *sql.DB
	actorA     model.Actor
	actorB     model.Actor
}

// idempotencySetup creates the database with spaces A and B and one person in each.
func idempotencySetup(t *testing.T) idempotencyFixtures {
	t.Helper()
	admin, api := ledgerSetup(t)
	return idempotencyFixtures{
		admin: admin, api: api,
		actorA: model.Actor{Kind: model.ActorPerson, ParticipantID: mustParticipant(t, api, spaceA, newPerson("", "Actor A"))},
		actorB: model.Actor{Kind: model.ActorPerson, ParticipantID: mustParticipant(t, api, spaceB, newPerson("", "Actor B"))},
	}
}

// newRecord builds a valid record for actor with a fresh id, a fixed
// fingerprint and a small response.
func newRecord(t *testing.T, actor model.Actor, key string) model.NewIdempotencyRecord {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	hash, err := model.RequestHash("test.pay", actor, []byte(`{"amount":"10.00"}`))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return model.NewIdempotencyRecord{
		ID: id.String(), Key: key, Operation: "test.pay", Actor: actor, RequestHash: hash, Response: []byte(`{"ok":true}`),
	}
}

// inSpace runs fn in a transaction of the space and fails the test on error.
func (f idempotencyFixtures) inSpace(t *testing.T, space string, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	if err := state.InSpace(context.Background(), f.api, space, fn); err != nil {
		t.Fatalf("transaction in %s: %v", space, err)
	}
}

// append stores the record at the time of the clock and returns what was stored.
func (f idempotencyFixtures) append(t *testing.T, space string, clock model.Clock, in model.NewIdempotencyRecord) model.IdempotencyRecord {
	t.Helper()
	var stored model.IdempotencyRecord
	f.inSpace(t, space, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stored, err = state.AppendIdempotencyRecord(ctx, tx, clock, in)
		return err
	})
	return stored
}

// find looks the key up at the time of the clock.
func (f idempotencyFixtures) find(t *testing.T, space string, clock model.Clock, key string) (model.IdempotencyRecord, bool) {
	t.Helper()
	var record model.IdempotencyRecord
	var found bool
	f.inSpace(t, space, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		record, found, err = state.FindIdempotencyRecord(ctx, tx, clock, key)
		return err
	})
	return record, found
}

// age moves every record of key back in time by d, as the administrator: the
// guard of the insert accepts a creation time only inside a window of five
// minutes, so a record that is older than that can only come from rewriting
// one. The trigger that forbids changing a record is switched off for the
// rewrite and on again right after, in a database that only this test uses.
func (f idempotencyFixtures) age(t *testing.T, key string, d time.Duration) {
	t.Helper()
	for _, query := range []string{
		`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER idempotency_records_no_update`,
		fmt.Sprintf(`UPDATE kernel.idempotency_records SET created_at = created_at - interval '%d microseconds', expires_at = expires_at - interval '%d microseconds' WHERE key = '%s'`,
			d.Microseconds(), d.Microseconds(), key),
		`ALTER TABLE kernel.idempotency_records ENABLE TRIGGER idempotency_records_no_update`,
	} {
		if _, err := f.admin.Exec(query); err != nil {
			t.Fatalf("age %q: %v", key, err)
		}
	}
}

// recent returns a clock a minute behind the database: inside the window of
// the guard and far enough behind to be a fixed, comparable instant.
func recent() *fixedClock {
	return &fixedClock{at: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
}

// TestAppendAndFindRoundTrip guards criterion 1 in state: the record is stored
// with the fingerprint, the response in the form of the database, the creation
// time of the clock and an expiry 24 hours later, and a lookup returns it.
func TestAppendAndFindRoundTrip(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	clock := &fixedClock{at: recent().at.Add(123 * time.Microsecond)}
	in := newRecord(t, f.actorA, "round-trip")
	in.Response = []byte(`{"b": 2,"a":[1, 2]}`)

	stored := f.append(t, spaceA, clock, in)
	if stored.ID != in.ID || stored.SpaceID != spaceA || stored.Key != "round-trip" || stored.Generation != 1 ||
		stored.Operation != "test.pay" || stored.ActorParticipantID != f.actorA.ParticipantID || stored.RequestHash != in.RequestHash {
		t.Errorf("stored = %+v, input %+v", stored, in)
	}
	if !stored.CreatedAt.Equal(clock.at) || stored.ExpiresAt.Sub(stored.CreatedAt) != model.IdempotencyTTL {
		t.Errorf("created %v expires %v, want the clock %v and +24h", stored.CreatedAt, stored.ExpiresAt, clock.at)
	}
	if stored.CreatedAt.Location() != time.UTC || stored.ExpiresAt.Location() != time.UTC {
		t.Error("the times are not in UTC")
	}
	if string(stored.Response) != `{"a": [1, 2], "b": 2}` {
		t.Errorf("response = %s, want the form of the database", stored.Response)
	}

	found, ok := f.find(t, spaceA, clock, "round-trip")
	if !ok || found.ID != stored.ID || found.RequestHash != in.RequestHash || !bytes.Equal(found.Response, stored.Response) ||
		!found.CreatedAt.Equal(stored.CreatedAt) || !found.ExpiresAt.Equal(stored.ExpiresAt) {
		t.Errorf("found = %+v (%v), want %+v", found, ok, stored)
	}
	if _, ok := f.find(t, spaceA, clock, "another-key"); ok {
		t.Error("another key found a record")
	}
	if got := count(t, f.admin, `SELECT count(*) FROM kernel.idempotency_records`); got != 1 {
		t.Errorf("%d records, want 1", got)
	}
}

// TestFindHonorsTheExpiryToTheMicrosecond guards criterion 6 at the edge: a
// record is in force until the instant it expires, not at it. Each case ages a
// record so that it has the given time left at the instant of the clock.
func TestFindHonorsTheExpiryToTheMicrosecond(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	clock := recent()
	for i, tc := range []struct {
		left time.Duration
		want bool
	}{
		{model.IdempotencyTTL, true}, {time.Minute, true}, {time.Microsecond, true}, {0, false}, {-time.Microsecond, false}, {-72 * time.Hour, false},
	} {
		key := fmt.Sprintf("edge-%d", i)
		f.append(t, spaceA, clock, newRecord(t, f.actorA, key))
		f.age(t, key, model.IdempotencyTTL-tc.left)
		if _, ok := f.find(t, spaceA, clock, key); ok != tc.want {
			t.Errorf("with %v left: found = %v, want %v", tc.left, ok, tc.want)
		}
	}
}

// TestFindCapsTheClockAtTheDatabase guards the ceiling of the injectable clock:
// a clock that runs ahead of the database, even by years, cannot expire a record
// early. The record is read in force as long as the database says it is.
func TestFindCapsTheClockAtTheDatabase(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	f.append(t, spaceA, realClock, newRecord(t, f.actorA, "ahead"))
	for name, at := range map[string]time.Time{
		"an hour ahead": time.Now().Add(time.Hour), "two days ahead": time.Now().Add(48 * time.Hour), "ten years ahead": time.Now().AddDate(10, 0, 0),
	} {
		if _, ok := f.find(t, spaceA, &fixedClock{at: at}, "ahead"); !ok {
			t.Errorf("%s: a clock ahead of the database expired a record that is in force", name)
		}
	}
}

// TestAppendCapsCreatedAtAtTheDatabase guards that a clock ahead of the
// database never makes the write fail and never stretches the life of the
// record: created_at is the earlier of the clock and the database time.
func TestAppendCapsCreatedAtAtTheDatabase(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	before := time.Now().UTC().Add(-2 * time.Second)
	stored := f.append(t, spaceA, &fixedClock{at: time.Now().AddDate(1, 0, 0)}, newRecord(t, f.actorA, "capped"))
	after := time.Now().UTC().Add(2 * time.Second)
	if stored.CreatedAt.Before(before) || stored.CreatedAt.After(after) {
		t.Errorf("created_at = %v, want the time of the database between %v and %v", stored.CreatedAt, before, after)
	}
	if stored.ExpiresAt.After(after.Add(model.IdempotencyTTL)) {
		t.Errorf("expires_at = %v: the record would live beyond 24 hours of the database", stored.ExpiresAt)
	}
}

// TestAppendFollowsAnExpiredRecordWithTheNextGeneration guards criterion 6: the
// execution after the expiry is a new record of the next generation, and the old
// one stays as it was.
func TestAppendFollowsAnExpiredRecordWithTheNextGeneration(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	clock := recent()
	first := f.append(t, spaceA, clock, newRecord(t, f.actorA, "again"))
	f.age(t, "again", model.IdempotencyTTL+time.Hour)

	if _, ok := f.find(t, spaceA, clock, "again"); ok {
		t.Fatal("the record is still in force after it expired")
	}
	second := f.append(t, spaceA, clock, newRecord(t, f.actorA, "again"))
	if second.Generation != 2 || second.ID == first.ID {
		t.Errorf("second = %+v, want generation 2 and another id", second)
	}
	found, ok := f.find(t, spaceA, clock, "again")
	if !ok || found.ID != second.ID {
		t.Errorf("found = %+v (%v), want the second generation in force", found, ok)
	}
	var kept int
	if err := f.admin.QueryRow(`SELECT count(*) FROM kernel.idempotency_records WHERE id = $1 AND generation = 1 AND response = '{"ok":true}'::jsonb`, first.ID).Scan(&kept); err != nil || kept != 1 {
		t.Errorf("the first generation changed (%d, %v)", kept, err)
	}
}

// TestCheckIdempotencyClock guards the clock before a handler runs: the zero
// time and a clock behind the database by more than the tolerance are
// invalid_input, a clock inside the tolerance, at it, and any clock ahead of the
// database pass.
func TestCheckIdempotencyClock(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	check := func(clock model.Clock) error {
		return state.InSpace(context.Background(), f.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
			return state.CheckIdempotencyClock(ctx, tx, clock)
		})
	}
	now := time.Now()
	for name, at := range map[string]time.Time{
		"the zero time": {}, "year 1": time.Date(1, 1, 1, 0, 0, 1, 0, time.UTC), "25 hours behind": now.Add(-25 * time.Hour),
		"an hour behind": now.Add(-time.Hour), "six minutes behind": now.Add(-6 * time.Minute),
	} {
		requireCodeLabel(t, name, check(&fixedClock{at: at}), model.CodeInvalidInput)
	}
	for name, at := range map[string]time.Time{
		"now": now, "a minute behind": now.Add(-time.Minute), "four minutes behind": now.Add(-4 * time.Minute),
		"a second ahead": now.Add(time.Second), "a year ahead": now.AddDate(1, 0, 0),
	} {
		if err := check(&fixedClock{at: at}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	requireCodeLabel(t, "no clock", check(nil), model.CodeInvalidInput)
}

// TestRequireReadCommitted guards the isolation level the lock relies on: the
// default level passes and a stricter one is rule_violation.
func TestRequireReadCommitted(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	f.inSpace(t, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		return state.RequireReadCommitted(ctx, tx)
	})
	tx, err := f.api.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	requireCodeLabel(t, "repeatable read", state.RequireReadCommitted(context.Background(), tx), model.CodeRuleViolation)
}

// TestAppendInAKeyInForceIsRuleViolation guards that the refusal of a key in
// force comes out typed, never as a raw uniqueness error, for the key in force
// and for a repeated id.
func TestAppendInAKeyInForceIsRuleViolation(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	first := f.append(t, spaceA, realClock, newRecord(t, f.actorA, "dup"))
	for name, in := range map[string]model.NewIdempotencyRecord{
		"the same key": newRecord(t, f.actorA, "dup"),
		"the same id":  func() model.NewIdempotencyRecord { r := newRecord(t, f.actorA, "other"); r.ID = first.ID; return r }(),
	} {
		err := state.InSpace(context.Background(), f.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
			_, err := state.AppendIdempotencyRecord(ctx, tx, realClock, in)
			return err
		})
		requireCodeLabel(t, name, err, model.CodeRuleViolation)
	}
	if got := count(t, f.admin, `SELECT count(*) FROM kernel.idempotency_records`); got != 1 {
		t.Errorf("%d records, want 1", got)
	}
}

// TestAppendRejectsInvalidInputBeforeWriting guards the checks that precede any
// query: a bad record or a missing clock is invalid_input and leaves the
// transaction usable.
func TestAppendRejectsInvalidInputBeforeWriting(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	bad := newRecord(t, f.actorA, "bad key")
	arrayResponse := newRecord(t, f.actorA, "array")
	arrayResponse.Response = []byte(`[]`)
	f.inSpace(t, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		_, err := state.AppendIdempotencyRecord(ctx, tx, realClock, bad)
		requireCodeLabel(t, "bad key", err, model.CodeInvalidInput)
		_, err = state.AppendIdempotencyRecord(ctx, tx, realClock, arrayResponse)
		requireCodeLabel(t, "array response", err, model.CodeInvalidInput)
		_, err = state.AppendIdempotencyRecord(ctx, tx, nil, newRecord(t, f.actorA, "no-clock"))
		requireCodeLabel(t, "no clock", err, model.CodeInvalidInput)
		_, _, err = state.FindIdempotencyRecord(ctx, tx, realClock, "bad key")
		requireCodeLabel(t, "find with a bad key", err, model.CodeInvalidInput)
		_, _, err = state.FindIdempotencyRecord(ctx, tx, nil, "k")
		requireCodeLabel(t, "find with no clock", err, model.CodeInvalidInput)
		err = state.LockIdempotencyKey(ctx, tx, "bad key")
		requireCodeLabel(t, "lock with a bad key", err, model.CodeInvalidInput)
		// The transaction is still usable.
		var one int
		return tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
	})
}

// TestIdempotencyWithoutASpaceIsRuleViolation guards that a transaction with no
// current space cannot lock, look up or write a record.
func TestIdempotencyWithoutASpaceIsRuleViolation(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	tx, err := f.api.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	ctx := context.Background()
	requireCodeLabel(t, "lock", state.LockIdempotencyKey(ctx, tx, "k"), model.CodeRuleViolation)
	_, _, err = state.FindIdempotencyRecord(ctx, tx, realClock, "k")
	requireCodeLabel(t, "find", err, model.CodeRuleViolation)
	_, err = state.AppendIdempotencyRecord(ctx, tx, realClock, newRecord(t, f.actorA, "k"))
	requireCodeLabel(t, "append", err, model.CodeRuleViolation)
}

// TestIdempotencyRecordsStayInTheirSpace guards criterion 4 and 8: the same key
// in another space is a first run, a space never finds the record of another,
// and an actor of another space cannot be named.
func TestIdempotencyRecordsStayInTheirSpace(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	f.append(t, spaceA, realClock, newRecord(t, f.actorA, "shared"))
	if _, ok := f.find(t, spaceB, realClock, "shared"); ok {
		t.Error("space B found the record of space A")
	}
	second := f.append(t, spaceB, realClock, newRecord(t, f.actorB, "shared"))
	if second.Generation != 1 || second.SpaceID != spaceB {
		t.Errorf("record of space B = %+v, want generation 1 in space B", second)
	}
	err := state.InSpace(context.Background(), f.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		_, err := state.AppendIdempotencyRecord(ctx, tx, realClock, newRecord(t, f.actorB, "foreign-actor"))
		return err
	})
	requireCodeLabel(t, "actor of space B in space A", err, model.CodeRuleViolation)
}

// TestLockIdempotencyKeySerializesAKey guards the lock: while one transaction
// holds a key, a second one that asks for the same key waits for it, and a
// different key does not wait.
func TestLockIdempotencyKeySerializesAKey(t *testing.T) {
	t.Parallel()
	f := idempotencySetup(t)
	ctx := context.Background()
	first, err := f.api.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer first.Rollback()
	if _, err := first.ExecContext(ctx, `SELECT set_config($1, $2, true)`, state.SpaceSetting, spaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	if err := state.LockIdempotencyKey(ctx, first, "held"); err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Locking again in the same transaction does not wait for itself.
	if err := state.LockIdempotencyKey(ctx, first, "held"); err != nil {
		t.Fatalf("lock again: %v", err)
	}

	waitFor := func(key string) error {
		bounded, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
		defer cancel()
		return state.InSpace(bounded, f.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
			return state.LockIdempotencyKey(ctx, tx, key)
		})
	}
	if err := waitFor("free"); err != nil {
		t.Errorf("another key waited: %v", err)
	}
	err = waitFor("held")
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("the same key did not wait for the holder: %v", err)
	}
}
