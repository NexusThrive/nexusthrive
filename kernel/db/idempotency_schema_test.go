package db_test

import (
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// idempotencyColumns lists every column of kernel.idempotency_records; the
// grants must cover exactly these.
var idempotencyColumns = []string{
	"id", "space_id", "key", "generation", "operation", "actor_participant_id", "request_hash", "response", "created_at", "expires_at",
}

// idempotencyInsertColumns are the only columns the API role may write: the
// trigger stamps generation and expires_at, and nobody may update a record.
var idempotencyInsertColumns = []string{"id", "space_id", "key", "operation", "actor_participant_id", "request_hash", "response", "created_at"}

// idempotencyFunctions are the functions the idempotency migration adds.
var idempotencyFunctions = []string{"idempotency_lock", "idempotency_records_guard_insert", "reject_idempotency_change"}

// idempotencyEnv is a database with the ledger fixtures: two spaces, each with
// a participant, and the API pool.
type idempotencyEnv struct {
	admin, api *sql.DB
}

// newIdempotencyEnv creates the database, the API pool and the fixtures.
func newIdempotencyEnv(t *testing.T) idempotencyEnv {
	t.Helper()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedLedgerFixtures(t, admin)
	return idempotencyEnv{admin: admin, api: api}
}

// writers returns both writers inside space A: the guards must hold for each of
// them. The administrator fixes the space too, because the guard of the insert
// is about the current space and not about who writes.
func (e idempotencyEnv) writers() []writer {
	return []writer{{"API", e.api, ledgerSpaceA}, {"administrator", e.admin, ledgerSpaceA}}
}

// recordColumns is the list of columns of an insert with the granted columns only.
const recordColumns = `(id, space_id, key, operation, actor_participant_id, request_hash, response, created_at)`

// recordStmt inserts a record of space with the granted columns only. created
// is a SQL expression for created_at, such as now() - interval '25 hours'.
func recordStmt(t *testing.T, space, key, created string) stmt {
	t.Helper()
	return stmt{
		`INSERT INTO kernel.idempotency_records ` + recordColumns + `
		 VALUES ($1, $2, $3, 'test.pay', $4, decode(repeat('ab', 32), 'hex'), '{"ok":true}'::jsonb, ` + created + `)`,
		[]any{newID(t), space, key, actorOf(space)},
	}
}

// agedRecordStmts writes a record of key in space A and then moves it back in
// time, as the administrator, in one transaction: the record is inserted at
// now(), the trigger that forbids changing a record is switched off inside the
// transaction, created_at and expires_at are rewritten (created is a SQL
// expression) and the trigger is switched on again. The insert guard accepts
// only a creation time inside its window, so this is the way to have a record
// that is older than the window without weakening the guard. The switch is
// part of the transaction: nothing outside it ever sees the trigger off.
func agedRecordStmts(t *testing.T, key, created string) []stmt {
	t.Helper()
	return []stmt{
		recordStmt(t, ledgerSpaceA, key, "now()"),
		{`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER idempotency_records_no_update`, nil},
		{`UPDATE kernel.idempotency_records SET created_at = ` + created + `, expires_at = ` + created + ` + interval '24 hours'
		  WHERE space_id = '` + ledgerSpaceA + `' AND key = '` + key + `'`, nil},
		{`ALTER TABLE kernel.idempotency_records ENABLE TRIGGER idempotency_records_no_update`, nil},
	}
}

// ageRecord commits agedRecordStmts as the administrator.
func (e idempotencyEnv) ageRecord(t *testing.T, key, created string) {
	t.Helper()
	if err := runTx(t, e.admin, ledgerSpaceA, agedRecordStmts(t, key, created)...); err != nil {
		t.Fatalf("age record %q: %v", key, err)
	}
}

// generationsOf lists the generations stored for a key of a space, in order.
func generationsOf(t *testing.T, admin *sql.DB, space, key string) []int {
	t.Helper()
	rows, err := admin.Query(`SELECT generation FROM kernel.idempotency_records WHERE space_id = $1 AND key = $2 ORDER BY generation`, space, key)
	if err != nil {
		t.Fatalf("list generations: %v", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var generation int
		if err := rows.Scan(&generation); err != nil {
			t.Fatalf("scan generation: %v", err)
		}
		out = append(out, generation)
	}
	return out
}

// requireGenerations fails unless the key has exactly the given generations.
func requireGenerations(t *testing.T, label string, admin *sql.DB, space, key string, want ...int) {
	t.Helper()
	if got := generationsOf(t, admin, space, key); !slices.Equal(got, want) {
		t.Errorf("%s: generations of %q = %v, want %v", label, key, got, want)
	}
}

// TestInsertStampsGenerationAndExpiry guards criterion 1 in the database: a
// record is born with generation 1 and an expiry of exactly 24 hours after its
// creation, whoever writes it, and the response is kept as given.
func TestInsertStampsGenerationAndExpiry(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	for _, w := range e.writers() {
		key := "stamp-" + w.name
		if err := runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, key, "now() - interval '1 minute'")); err != nil {
			t.Fatalf("%s: insert: %v", w.name, err)
		}
		var generation int
		var ttlOK, createdOK bool
		var response string
		err := e.admin.QueryRow(`SELECT generation, expires_at = created_at + interval '24 hours',
			created_at BETWEEN now() - interval '90 seconds' AND now() - interval '30 seconds', response::text
			FROM kernel.idempotency_records WHERE space_id = $1 AND key = $2`, ledgerSpaceA, key).Scan(&generation, &ttlOK, &createdOK, &response)
		if err != nil {
			t.Fatalf("%s: read: %v", w.name, err)
		}
		if generation != 1 || !ttlOK || !createdOK || response != `{"ok": true}` {
			t.Errorf("%s: generation %d, ttl ok %v, created kept %v, response %s", w.name, generation, ttlOK, createdOK, response)
		}
	}
}

// TestTTLConstantMatchesSQL guards that the 24 hours of the database and the
// constant of Go are the same duration.
func TestTTLConstantMatchesSQL(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	if err := runTx(t, e.api, ledgerSpaceA, recordStmt(t, ledgerSpaceA, "ttl", "now()")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var seconds float64
	if err := e.admin.QueryRow(`SELECT extract(epoch FROM expires_at - created_at) FROM kernel.idempotency_records WHERE key = 'ttl'`).Scan(&seconds); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := time.Duration(seconds * float64(time.Second)); got != model.IdempotencyTTL {
		t.Errorf("the database keeps a record for %v, the model says %v", got, model.IdempotencyTTL)
	}
}

// TestInsertInKeyInForceRejected guards criterion 3 below the engine: a second
// record for a key whose record is still in force is refused with the named
// constraint, for either writer, and the first record is untouched.
func TestInsertInKeyInForceRejected(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	e.ageRecord(t, "force", "now() - interval '23 hours 59 minutes'")
	for _, w := range e.writers() {
		requireRule(t, w.name+": second record of a key in force", runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, "force", "now()")),
			"23505", "idempotency_records_key_in_force")
	}
	requireGenerations(t, "after the refused inserts", e.admin, ledgerSpaceA, "force", 1)
	// Another key is not affected, and the same key in another space is a first run.
	if err := runTx(t, e.api, ledgerSpaceA, recordStmt(t, ledgerSpaceA, "other", "now()")); err != nil {
		t.Errorf("another key: %v", err)
	}
	if err := runTx(t, e.api, ledgerSpaceB, recordStmt(t, ledgerSpaceB, "force", "now()")); err != nil {
		t.Errorf("the same key in another space: %v", err)
	}
}

// TestExpiredRecordIsFollowedByNextGeneration guards criterion 6 in the
// database: an expired record is not replaced and not changed, the next record
// of its key is the next generation, and that one is now in force.
func TestExpiredRecordIsFollowedByNextGeneration(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	for _, w := range e.writers() {
		key := "gen-" + w.name
		e.ageRecord(t, key, "now() - interval '26 hours'")
		if err := runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, key, "now() - interval '1 minute'")); err != nil {
			t.Fatalf("%s: record after the expiry: %v", w.name, err)
		}
		requireGenerations(t, w.name, e.admin, ledgerSpaceA, key, 1, 2)
		requireRule(t, w.name+": third record while generation 2 is in force", runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, key, "now()")),
			"23505", "idempotency_records_key_in_force")
		if got := count(t, e.admin, `SELECT count(*) FROM kernel.idempotency_records WHERE key = $1 AND generation = 1 AND created_at < now() - interval '25 hours'`, key); got != 1 {
			t.Errorf("%s: generation 1 changed", w.name)
		}
	}
}

// TestRecordAtTheBoundaryOfExpiry guards the edges: a record that expires at
// exactly this instant is already expired, and one that expires a microsecond
// later is still in force. now() is the same for the whole transaction, so both
// edges are exact.
func TestRecordAtTheBoundaryOfExpiry(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	err := runTx(t, e.admin, ledgerSpaceA, append(agedRecordStmts(t, "edge", "now() - interval '24 hours'"), recordStmt(t, ledgerSpaceA, "edge", "now()"))...)
	if err != nil {
		t.Errorf("a record that expires exactly now was treated as in force: %v", err)
	}
	requireGenerations(t, "expired at the edge", e.admin, ledgerSpaceA, "edge", 1, 2)

	err = runTx(t, e.admin, ledgerSpaceA, append(agedRecordStmts(t, "inside", "now() - interval '24 hours' + interval '1 microsecond'"), recordStmt(t, ledgerSpaceA, "inside", "now()"))...)
	requireRule(t, "a microsecond before the expiry", err, "23505", "idempotency_records_key_in_force")
	requireGenerations(t, "refused inside the edge", e.admin, ledgerSpaceA, "inside")
}

// TestTTLCheckHoldsWithoutTrigger guards the second layer of the expiry: with
// the insert guard out of the way, the CHECK still refuses an expiry that is
// not exactly 24 hours after the creation, and a generation below 1.
func TestTTLCheckHoldsWithoutTrigger(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	insert := func(generation int, expires string) stmt {
		return stmt{`INSERT INTO kernel.idempotency_records (id, space_id, key, generation, operation, actor_participant_id, request_hash, response, created_at, expires_at)
			VALUES ($1, $2, 'ttl-check', $3, 'test.pay', $4, decode(repeat('ab', 32), 'hex'), '{}'::jsonb, now() - interval '1 hour', ` + expires + `)`,
			[]any{newID(t), ledgerSpaceA, generation, ledgerParticipantA}}
	}
	withoutTrigger(t, e.admin, "idempotency_records", "idempotency_records_guard_insert", func() {
		for name, expires := range map[string]string{
			"a longer life":    "now() - interval '1 hour' + interval '25 hours'",
			"a shorter life":   "now() - interval '1 hour' + interval '23 hours'",
			"an off by one µs": "now() - interval '1 hour' + interval '24 hours' + interval '1 microsecond'",
			"expiry first":     "now() - interval '2 hours'",
		} {
			requireRule(t, name, runTx(t, e.admin, ledgerSpaceA, insert(1, expires)), "23514", "idempotency_records_ttl")
		}
		requireRule(t, "generation 0", runTx(t, e.admin, ledgerSpaceA, insert(0, "now() - interval '1 hour' + interval '24 hours'")),
			"23514", "idempotency_records_generation_check")
		if err := runTx(t, e.admin, ledgerSpaceA, insert(1, "now() - interval '1 hour' + interval '24 hours'")); err != nil {
			t.Errorf("a coherent record without the trigger: %v", err)
		}
	})
}

// TestRecordChecksRejectedByDatabase guards the shape of every column: a key, an
// operation, a fingerprint or a response that does not fit is refused by the
// database, for either writer.
func TestRecordChecksRejectedByDatabase(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	insert := func(id, key, operation, hash, response string) stmt {
		return stmt{`INSERT INTO kernel.idempotency_records ` + recordColumns + `
			VALUES ($1, $2, $3, $4, $5, ` + hash + `, $6::jsonb, now())`,
			[]any{id, ledgerSpaceA, key, operation, ledgerParticipantA, response}}
	}
	good := decodeHash(32)
	bigObject := `{"p":"` + strings.Repeat("a", 70000) + `"}`
	for _, w := range e.writers() {
		for name, s := range map[string]struct {
			stmt       stmt
			constraint string
		}{
			"empty key":              {insert(newID(t), "", "test.pay", good, `{}`), "idempotency_records_key_check"},
			"key with a space":       {insert(newID(t), "a b", "test.pay", good, `{}`), "idempotency_records_key_check"},
			"key ending in newline":  {insert(newID(t), "key\n", "test.pay", good, `{}`), "idempotency_records_key_check"},
			"key of 129":             {insert(newID(t), strings.Repeat("k", 129), "test.pay", good, `{}`), "idempotency_records_key_check"},
			"operation in uppercase": {insert(newID(t), "k1", "Test.Pay", good, `{}`), "idempotency_records_operation_check"},
			"operation of one part":  {insert(newID(t), "k1", "pay", good, `{}`), "idempotency_records_operation_check"},
			"operation too long":     {insert(newID(t), "k1", "test."+strings.Repeat("a", 130), good, `{}`), "idempotency_records_operation_check"},
			"fingerprint of 31":      {insert(newID(t), "k1", "test.pay", decodeHash(31), `{}`), "idempotency_records_hash_size"},
			"fingerprint of 33":      {insert(newID(t), "k1", "test.pay", decodeHash(33), `{}`), "idempotency_records_hash_size"},
			"fingerprint empty":      {insert(newID(t), "k1", "test.pay", decodeHash(0), `{}`), "idempotency_records_hash_size"},
			"response array":         {insert(newID(t), "k1", "test.pay", good, `[]`), "idempotency_records_response_check"},
			"response string":        {insert(newID(t), "k1", "test.pay", good, `"x"`), "idempotency_records_response_check"},
			"response null":          {insert(newID(t), "k1", "test.pay", good, `null`), "idempotency_records_response_check"},
			"response over 64 KiB":   {insert(newID(t), "k1", "test.pay", good, bigObject), "idempotency_records_response_check"},
			"id that is not v7":      {insert("01900000-0000-4000-8000-0000000000aa", "k1", "test.pay", good, `{}`), ""},
		} {
			requireRule(t, w.name+": "+name, runTx(t, w.pool, w.space, s.stmt), "23514", s.constraint)
		}
		if err := runTx(t, w.pool, w.space, insert(newID(t), "k-"+w.name, "test.pay", good, `{}`)); err != nil {
			t.Errorf("%s: control insert: %v", w.name, err)
		}
	}
}

// decodeHash returns the SQL for a fingerprint of n bytes.
func decodeHash(n int) string {
	return `decode(repeat('ab', ` + strconv.Itoa(n) + `), 'hex')`
}

// TestInsertOfOtherSpaceRejected guards criterion 8 for writes: a record of
// another space, or one whose actor lives in another space, is refused whether
// the guard, row level security or the composite key answers.
func TestInsertOfOtherSpaceRejected(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	foreignRecord := recordStmt(t, ledgerSpaceB, "foreign", "now()")
	foreignActor := stmt{`INSERT INTO kernel.idempotency_records ` + recordColumns + `
		VALUES ($1, $2, 'foreign-actor', 'test.pay', $3, decode(repeat('ab', 32), 'hex'), '{}'::jsonb, now())`,
		[]any{newID(t), ledgerSpaceA, ledgerParticipantB}}

	for _, w := range e.writers() {
		requireRule(t, w.name+": record of space B written from space A", runTx(t, w.pool, w.space, foreignRecord), "23001", "")
		requireRule(t, w.name+": actor of space B", runTx(t, w.pool, w.space, foreignActor), "23503", "idempotency_records_actor_fkey")
		requireRule(t, w.name+": record with no current space", runTx(t, w.pool, "", recordStmt(t, ledgerSpaceA, "no-space", "now()")), "23001", "")
	}
	// With the guard out of the way the other layers answer on their own.
	withoutTrigger(t, e.admin, "idempotency_records", "idempotency_records_guard_insert", func() {
		requireRule(t, "API, no guard: row level security", runTx(t, e.api, ledgerSpaceA, foreignRecord), "42501", "")
		requireRule(t, "API, no guard, no space: row level security", runTx(t, e.api, "", recordStmt(t, ledgerSpaceA, "no-space", "now()")), "42501", "")
		stamped := stmt{`INSERT INTO kernel.idempotency_records (id, space_id, key, generation, operation, actor_participant_id, request_hash, response, created_at, expires_at)
			VALUES ($1, $2, 'foreign-actor', 1, 'test.pay', $3, decode(repeat('ab', 32), 'hex'), '{}'::jsonb, now(), now() + interval '24 hours')`,
			[]any{newID(t), ledgerSpaceA, ledgerParticipantB}}
		requireRule(t, "administrator, no guard: actor of space B", runTx(t, e.admin, ledgerSpaceA, stamped), "23503", "idempotency_records_actor_fkey")
	})
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.idempotency_records`); got != 0 {
		t.Errorf("%d records after the refused inserts, want 0", got)
	}
}

// TestIdempotencyRecordsIsolationBetweenSpaces guards criterion 8: a space sees
// none of the records of another, and a transaction with no space sees none.
func TestIdempotencyRecordsIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	if err := runTx(t, e.api, ledgerSpaceA, recordStmt(t, ledgerSpaceA, "iso", "now()")); err != nil {
		t.Fatalf("record of A: %v", err)
	}
	if err := runTx(t, e.api, ledgerSpaceB, recordStmt(t, ledgerSpaceB, "iso", "now()")); err != nil {
		t.Fatalf("record of B: %v", err)
	}
	for space, want := range map[string]int{ledgerSpaceA: 1, ledgerSpaceB: 1, "": 0} {
		tx, err := e.api.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if space != "" {
			if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, space); err != nil {
				t.Fatalf("set space: %v", err)
			}
		}
		var total, ofA int
		if err := tx.QueryRow(`SELECT count(*), count(*) FILTER (WHERE space_id = $1) FROM kernel.idempotency_records`, ledgerSpaceA).Scan(&total, &ofA); err != nil {
			t.Fatalf("read: %v", err)
		}
		_ = tx.Rollback()
		if total != want || (space == ledgerSpaceB && ofA != 0) {
			t.Errorf("space %q sees %d records (%d of A), want %d", space, total, ofA, want)
		}
	}
}

// TestIdempotencyForcedRLSAndGrants guards forced row level security, only the
// planned policies and the exact column grants: select everywhere, insert on
// eight columns, nothing else.
func TestIdempotencyForcedRLSAndGrants(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	requireForcedRLS(t, admin, "idempotency_records")
	if got, want := policyCommands(t, admin, "idempotency_records"), []string{"INSERT", "SELECT"}; !slices.Equal(got, want) {
		t.Errorf("idempotency_records policies = %v, want %v", got, want)
	}
	for _, privilege := range []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
		var ok bool
		if err := admin.QueryRow(`SELECT has_table_privilege($1, 'kernel.idempotency_records', $2)`, db.APIRole, privilege).Scan(&ok); err != nil {
			t.Fatalf("privilege query: %v", err)
		}
		if ok {
			t.Errorf("API role must not have %s on kernel.idempotency_records", privilege)
		}
	}
	if got, want := privilegeSet(t, admin, "idempotency_records"), expectPrivileges(idempotencyColumns, idempotencyInsertColumns, nil); !slices.Equal(got, want) {
		t.Errorf("idempotency_records privileges = %v, want %v", got, want)
	}
	if got := columnSet(t, admin, "idempotency_records"); len(got) != len(idempotencyColumns) {
		t.Errorf("idempotency_records has %d columns, the grants cover %d: update the grants with the table", len(got), len(idempotencyColumns))
	}
	for column, nullable := range columnSet(t, admin, "idempotency_records") {
		if nullable != "NO" {
			t.Errorf("idempotency_records.%s is nullable", column)
		}
	}
	// The function of the lock is for the API role and the owner, not for PUBLIC.
	if got := count(t, admin, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace, aclexplode(p.proacl) a
		WHERE n.nspname = 'kernel' AND p.proname = 'idempotency_lock' AND a.grantee = 0`); got != 0 {
		t.Error("kernel.idempotency_lock is executable by PUBLIC")
	}
	var ok bool
	if err := admin.QueryRow(`SELECT has_function_privilege($1, 'kernel.idempotency_lock(text)', 'EXECUTE')`, db.APIRole).Scan(&ok); err != nil || !ok {
		t.Errorf("the API role cannot execute kernel.idempotency_lock (%v)", err)
	}
}

// TestIdempotencyFunctionsPinSearchPathAndNotSecurityDefiner guards the
// attack surface of privilege escalation and of a function planted in a hostile
// search path.
func TestIdempotencyFunctionsPinSearchPathAndNotSecurityDefiner(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	if got := count(t, admin, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'kernel' AND p.prosecdef`); got != 0 {
		t.Errorf("%d kernel functions are SECURITY DEFINER, want none", got)
	}
	for _, name := range idempotencyFunctions {
		var config string
		err := admin.QueryRow(`SELECT coalesce(array_to_string(proconfig, ','), '') FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'kernel' AND p.proname = $1`, name).Scan(&config)
		if err != nil {
			t.Errorf("function %s: %v", name, err)
			continue
		}
		if config != "search_path=pg_catalog, pg_temp" {
			t.Errorf("function %s config = %q, want a fixed search_path of pg_catalog, pg_temp", name, config)
		}
	}
}

// TestIdempotencyLockNeedsASpaceAndSerializesAKey guards the lock function: with
// no current space it refuses, and a second transaction asking for the same key
// of the same space waits for the first while another key or another space does
// not.
func TestIdempotencyLockNeedsASpaceAndSerializesAKey(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	requireRule(t, "lock with no space", runTx(t, e.api, "", stmt{`SELECT kernel.idempotency_lock('k')`, nil}), "23502", "")
	requireRule(t, "lock with a malformed space", runTx(t, e.api, "nope", stmt{`SELECT kernel.idempotency_lock('k')`, nil}), "22P02", "")

	holder, err := e.admin.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer holder.Rollback()
	if _, err := holder.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	if _, err := holder.Exec(`SELECT kernel.idempotency_lock('same')`); err != nil {
		t.Fatalf("hold the lock: %v", err)
	}
	// Other keys and other spaces pass at once.
	for name, probe := range map[string]struct{ space, key string }{"another key": {ledgerSpaceA, "different"}, "another space": {ledgerSpaceB, "same"}} {
		other, err := e.admin.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := other.Exec(`SET LOCAL lock_timeout = '300ms'`); err != nil {
			t.Fatalf("set lock_timeout: %v", err)
		}
		if _, err := other.Exec(`SELECT set_config('kernel.space_id', $1, true)`, probe.space); err != nil {
			t.Fatalf("set space: %v", err)
		}
		if _, err := other.Exec(`SELECT kernel.idempotency_lock($1)`, probe.key); err != nil {
			t.Errorf("%s waited for the lock: %v", name, err)
		}
		_ = other.Rollback()
	}
	// The same key of the same space waits.
	same, err := e.admin.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer same.Rollback()
	if _, err := same.Exec(`SET LOCAL lock_timeout = '300ms'`); err != nil {
		t.Fatalf("set lock_timeout: %v", err)
	}
	if _, err := same.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	_, err = same.Exec(`SELECT kernel.idempotency_lock('same')`)
	requireRule(t, "the same key of the same space", err, "55P03", "")
}

// TestToleranceConstantMatchesSQL guards that the window of the trigger and the
// constant of Go are the same duration: a record exactly at the edge is stored,
// one microsecond further is refused.
func TestToleranceConstantMatchesSQL(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	edge := fmt.Sprintf("now() - make_interval(secs => %d)", int(model.IdempotencyClockTolerance.Seconds()))
	for _, w := range e.writers() {
		requireRule(t, w.name+": one microsecond beyond the tolerance", runTx(t, w.pool, w.space,
			recordStmt(t, ledgerSpaceA, "beyond-"+w.name, edge+" - interval '1 microsecond'")), "23001", "")
		if err := runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, "edge-"+w.name, edge)); err != nil {
			t.Errorf("%s: a record exactly at the tolerance: %v", w.name, err)
		}
	}
}

// TestInsertCannotBeBornBeyondTheTolerance guards the lower bound of created_at:
// a record older than the tolerance, from an old clock, the zero time or far in
// the past, is refused for either writer, so that no record is born with less
// than its 24 hours. A record just inside the window is stored.
func TestInsertCannotBeBornBeyondTheTolerance(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	for _, w := range e.writers() {
		for name, created := range map[string]string{
			"six minutes behind": "now() - interval '6 minutes'", "an hour behind": "now() - interval '1 hour'",
			"25 hours behind": "now() - interval '25 hours'", "a century behind": "now() - interval '100 years'",
			"year 1": "'0001-01-01T00:00:00Z'::timestamptz", "the epoch": "'1970-01-01T00:00:00Z'::timestamptz", "-infinity": "'-infinity'::timestamptz",
		} {
			requireRule(t, w.name+": "+name, runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, "old-"+w.name, created)), "23001", "")
		}
		if err := runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, "inside-"+w.name, "now() - interval '4 minutes'")); err != nil {
			t.Errorf("%s: a record four minutes behind: %v", w.name, err)
		}
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.idempotency_records WHERE key LIKE 'old-%'`); got != 0 {
		t.Errorf("%d records born beyond the tolerance were stored", got)
	}
}
