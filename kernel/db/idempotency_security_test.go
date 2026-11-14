package db_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/db"
)

// seedRecord commits one record in space A as the API role and returns its key.
func (e idempotencyEnv) seedRecord(t *testing.T, key, created string) {
	t.Helper()
	if err := runTx(t, e.api, ledgerSpaceA, recordStmt(t, ledgerSpaceA, key, created)); err != nil {
		t.Fatalf("seed record %q: %v", key, err)
	}
}

// requireRecordsIntact fails unless the record of a key is still there with its
// first generation, the given response and its original expiry.
func (e idempotencyEnv) requireRecordsIntact(t *testing.T, key string, want int) {
	t.Helper()
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.idempotency_records WHERE key = $1 AND response = '{"ok":true}'::jsonb
		AND expires_at = created_at + interval '24 hours'`, key); got != want {
		t.Errorf("records of %q intact = %d, want %d", key, got, want)
	}
}

// disableOtherTruncateTriggers turns off every TRUNCATE trigger of the kernel
// that does not belong to the idempotency records, so that a cascade from a
// parent table reaches the records and the records answer on their own.
func disableOtherTruncateTriggers(t *testing.T, admin *sql.DB) {
	t.Helper()
	rows, err := admin.Query(`SELECT c.relname, t.tgname FROM pg_trigger t
		JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'kernel' AND NOT t.tgisinternal AND (t.tgtype & 32) <> 0 AND c.relname <> 'idempotency_records'`)
	if err != nil {
		t.Fatalf("list truncate triggers: %v", err)
	}
	var found [][2]string
	for rows.Next() {
		var table, trigger string
		if err := rows.Scan(&table, &trigger); err != nil {
			rows.Close()
			t.Fatalf("scan trigger: %v", err)
		}
		found = append(found, [2]string{table, trigger})
	}
	rows.Close()
	if len(found) == 0 {
		t.Fatal("no truncate triggers found, so the cascade test would prove nothing")
	}
	for _, f := range found {
		toggleTrigger(t, admin, pgx.Identifier{f[0]}.Sanitize(), pgx.Identifier{f[1]}.Sanitize(), false)
	}
}

// TestAPICannotUpdateDeleteOrTruncateRecords guards memory: no writer changes,
// deletes or truncates a record. The API role lacks the privilege; even when a
// grant is given by mistake, and for the administrator, the triggers refuse,
// also for a TRUNCATE that reaches the table by CASCADE from its parents.
func TestAPICannotUpdateDeleteOrTruncateRecords(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	e.seedRecord(t, "memory", "now()")

	queries := []string{
		`UPDATE kernel.idempotency_records SET response = '{"ok":false}'::jsonb`,
		`UPDATE kernel.idempotency_records SET request_hash = decode(repeat('cd', 32), 'hex')`,
		`UPDATE kernel.idempotency_records SET key = key`,
		`DELETE FROM kernel.idempotency_records`,
		`DELETE FROM kernel.idempotency_records WHERE expires_at < now() + interval '48 hours'`,
		`TRUNCATE kernel.idempotency_records`,
		`TRUNCATE kernel.idempotency_records CASCADE`,
		`TRUNCATE kernel.idempotency_records RESTART IDENTITY`,
	}
	for _, query := range queries {
		requireRule(t, "API: "+query, asAPI(t, e.admin, query), "42501", "")
	}
	requireRule(t, "API: TRUNCATE kernel.spaces CASCADE", asAPI(t, e.admin, `TRUNCATE kernel.spaces CASCADE`), "42501", "")
	requireRule(t, "API: TRUNCATE kernel.participants CASCADE", asAPI(t, e.admin, `TRUNCATE kernel.participants CASCADE`), "42501", "")

	// The administrator reaches the triggers: whole statements, no row needed.
	for _, query := range queries {
		_, err := e.admin.Exec(query)
		requireRule(t, "administrator: "+query, err, "23001", "")
	}
	// A grant given by mistake does not open the door either.
	for _, query := range []string{
		`GRANT UPDATE, DELETE, TRUNCATE ON kernel.idempotency_records TO nexusthrive_api`,
		`CREATE POLICY mistaken_update ON kernel.idempotency_records FOR UPDATE USING (true)`,
		`CREATE POLICY mistaken_delete ON kernel.idempotency_records FOR DELETE USING (true)`,
	} {
		if _, err := e.admin.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, query := range []string{
		`UPDATE kernel.idempotency_records SET response = '{"ok":false}'::jsonb`, `DELETE FROM kernel.idempotency_records`, `TRUNCATE kernel.idempotency_records`,
	} {
		requireRule(t, "API after a mistaken grant: "+query, runTx(t, e.api, ledgerSpaceA, stmt{query, nil}), "23001", "")
	}

	// A cascade from the parents reaches the records. The triggers of the other
	// tables are turned off so that the records are the ones that answer.
	disableOtherTruncateTriggers(t, e.admin)
	for _, query := range []string{`TRUNCATE kernel.spaces CASCADE`, `TRUNCATE kernel.participants CASCADE`} {
		_, err := e.admin.Exec(query)
		if err == nil || !strings.Contains(err.Error(), "idempotency records cannot be changed, deleted or truncated") {
			t.Errorf("%s = %v, want the records to refuse it", query, err)
		}
	}
	e.requireRecordsIntact(t, "memory", 1)
}

// TestAPICannotWriteManagedColumns guards the columns the kernel stamps: the API
// role lacks the privilege to write them, and a value the administrator writes is
// replaced by the one the trigger derives.
func TestAPICannotWriteManagedColumns(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	insert := func(extraColumns, extraValues string) stmt {
		return stmt{`INSERT INTO kernel.idempotency_records (id, space_id, key, operation, actor_participant_id, request_hash, response, created_at` + extraColumns + `)
			VALUES ($1, $2, 'managed', 'test.pay', $3, decode(repeat('ab', 32), 'hex'), '{}'::jsonb, now()` + extraValues + `)`,
			[]any{newID(t), ledgerSpaceA, ledgerParticipantA}}
	}
	for name, s := range map[string]stmt{
		"generation":              insert(", generation", ", 7"),
		"generation of one":       insert(", generation", ", 1"),
		"expires_at":              insert(", expires_at", ", now() + interval '24 hours'"),
		"expires_at far away":     insert(", expires_at", ", now() + interval '100 years'"),
		"generation and expiry":   insert(", generation, expires_at", ", 2, now()"),
		"expires_at in the past":  insert(", expires_at", ", now() - interval '1 day'"),
		"expires_at null (stamp)": insert(", expires_at", ", NULL"),
	} {
		requireRule(t, "API "+name, runTx(t, e.api, ledgerSpaceA, s), "42501", "")
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.idempotency_records`); got != 0 {
		t.Errorf("%d records after the refused inserts, want 0", got)
	}

	// The administrator may name the columns, but the trigger decides the values.
	if err := runTx(t, e.admin, ledgerSpaceA, insert(", generation, expires_at", ", 9, now() + interval '100 years'")); err != nil {
		t.Fatalf("administrator names the managed columns: %v", err)
	}
	var generation int
	var ttlOK bool
	if err := e.admin.QueryRow(`SELECT generation, expires_at = created_at + interval '24 hours' FROM kernel.idempotency_records WHERE key = 'managed'`).Scan(&generation, &ttlOK); err != nil {
		t.Fatalf("read: %v", err)
	}
	if generation != 1 || !ttlOK {
		t.Errorf("generation %d, ttl ok %v: the trigger did not replace what the administrator wrote", generation, ttlOK)
	}
}

// TestInsertCannotBeBornInTheFuture guards the ceiling of the injectable clock:
// a record whose creation time is after the clock of the database is refused
// for either writer, to the microsecond, so that no clock can stretch the life
// of a record beyond 24 hours of the database.
func TestInsertCannotBeBornInTheFuture(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	for _, w := range e.writers() {
		for name, created := range map[string]string{
			"one microsecond ahead": "now() + interval '1 microsecond'", "one second ahead": "now() + interval '1 second'",
			"a day ahead": "now() + interval '1 day'", "far ahead": "now() + interval '100 years'", "infinity": "'infinity'::timestamptz",
		} {
			requireRule(t, w.name+": "+name, runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, "future-"+w.name, created)), "23001", "")
		}
		requireRule(t, w.name+": null creation time", runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, "future-"+w.name, "NULL")), "23001", "")
		for name, created := range map[string]string{"now": "now()", "one microsecond behind": "now() - interval '1 microsecond'", "far behind": "now() - interval '100 years'"} {
			if err := runTx(t, w.pool, w.space, recordStmt(t, ledgerSpaceA, "past-"+w.name+"-"+strings.ReplaceAll(name, " ", "-"), created)); err != nil {
				t.Errorf("%s: %s: %v", w.name, name, err)
			}
		}
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.idempotency_records WHERE key LIKE 'future-%'`); got != 0 {
		t.Errorf("%d records born in the future were stored", got)
	}
}

// TestInsertGuardsApplyToTheAdministrator guards that the rules of the insert
// are not for the API role alone: the owner of the table is stopped by the same
// triggers when it writes in a key in force, outside its space or a record that
// names the managed columns by hand.
func TestInsertGuardsApplyToTheAdministrator(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	e.seedRecord(t, "admin-guard", "now()")
	requireRule(t, "administrator, key in force", runTx(t, e.admin, ledgerSpaceA, recordStmt(t, ledgerSpaceA, "admin-guard", "now()")),
		"23505", "idempotency_records_key_in_force")
	requireRule(t, "administrator, other space", runTx(t, e.admin, ledgerSpaceA, recordStmt(t, ledgerSpaceB, "admin-guard", "now()")), "23001", "")
	requireRule(t, "administrator, future", runTx(t, e.admin, ledgerSpaceA, recordStmt(t, ledgerSpaceA, "admin-future", "now() + interval '1 minute'")), "23001", "")
	requireRule(t, "administrator, update", runTx(t, e.admin, ledgerSpaceA, stmt{`UPDATE kernel.idempotency_records SET key = 'moved'`, nil}), "23001", "")
	requireRule(t, "administrator, delete", runTx(t, e.admin, ledgerSpaceA, stmt{`DELETE FROM kernel.idempotency_records`, nil}), "23001", "")
	e.requireRecordsIntact(t, "admin-guard", 1)
}

// TestConcurrentInsertsOfOneKeyLeaveOneInForce guards the lock of the trigger on
// its own, below the engine: while one transaction holds an uncommitted record
// of a key, a second insert of the key waits, and when the first commits the
// second is refused as a key in force, never as a duplicate of the primary key
// that would leak a raw uniqueness error.
func TestConcurrentInsertsOfOneKeyLeaveOneInForce(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	first, err := e.api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer first.Rollback()
	if _, err := first.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	s := recordStmt(t, ledgerSpaceA, "race", "now()")
	if _, err := first.Exec(s.query, s.args...); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- runTx(t, e.api, ledgerSpaceA, recordStmt(t, ledgerSpaceA, "race", "now()")) }()
	waitForLockWait(t, e.admin, "INSERT INTO kernel.idempotency_records")
	select {
	case err := <-done:
		t.Fatalf("the second insert did not wait for the first: %v", err)
	default:
	}
	if err := first.Commit(); err != nil {
		t.Fatalf("commit the first: %v", err)
	}
	requireRule(t, "second insert after the first commits", <-done, "23505", "idempotency_records_key_in_force")
	requireGenerations(t, "after the race", e.admin, ledgerSpaceA, "race", 1)
}

// TestOnConflictCopyAndSetRoleDoNotBypass guards INSERT ... ON CONFLICT, COPY
// and SET ROLE: none of them goes around the privileges or the triggers.
func TestOnConflictCopyAndSetRoleDoNotBypass(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	e.seedRecord(t, "bypass", "now()")
	var id string
	if err := e.admin.QueryRow(`SELECT id::text FROM kernel.idempotency_records WHERE key = 'bypass'`).Scan(&id); err != nil {
		t.Fatalf("read id: %v", err)
	}
	again := func(conflict string) stmt {
		return stmt{`INSERT INTO kernel.idempotency_records ` + recordColumns + `
			VALUES ($1, $2, 'bypass', 'test.pay', $3, decode(repeat('cd', 32), 'hex'), '{"ok":false}'::jsonb, now()) ` + conflict,
			[]any{id, ledgerSpaceA, ledgerParticipantA}}
	}
	for name, conflict := range map[string]string{
		"rewrite the response": `ON CONFLICT (space_id, key, generation) DO UPDATE SET response = EXCLUDED.response`,
		"rewrite the hash":     `ON CONFLICT (id, space_id) DO UPDATE SET request_hash = EXCLUDED.request_hash`,
		"rewrite the expiry":   `ON CONFLICT (space_id, key, generation) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
	} {
		err := runTx(t, e.api, ledgerSpaceA, again(conflict))
		requireRule(t, "API "+name, err, "42501", "")
	}
	// DO NOTHING does not hide the refusal of the trigger: a key in force is an error, not a skipped row.
	for _, w := range e.writers() {
		requireRule(t, w.name+": DO NOTHING on a key in force", runTx(t, w.pool, w.space, again(`ON CONFLICT DO NOTHING`)),
			"23505", "idempotency_records_key_in_force")
	}
	requireRule(t, "administrator: DO UPDATE on a key in force",
		runTx(t, e.admin, ledgerSpaceA, again(`ON CONFLICT (space_id, key, generation) DO UPDATE SET response = EXCLUDED.response`)),
		"23505", "idempotency_records_key_in_force")
	e.requireRecordsIntact(t, "bypass", 1)

	// COPY: row level security refuses COPY FROM for the API role, and the triggers still run for the owner.
	ctx := context.Background()
	columns := []string{"id", "space_id", "key", "operation", "actor_participant_id", "request_hash", "response", "created_at"}
	copyRows := func(conn *pgx.Conn, key string, created time.Time) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		rows := [][]any{{newID(t), ledgerSpaceA, key, "test.pay", ledgerParticipantA, make([]byte, 32), `{}`, created}}
		if _, err = tx.CopyFrom(ctx, pgx.Identifier{"kernel", "idempotency_records"}, columns, pgx.CopyFromRows(rows)); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}
	requireRule(t, "API COPY", copyRows(pgxConn(t, e.admin, true), "copied", time.Now().Add(-time.Hour)), "0A000", "")
	requireRule(t, "administrator COPY into a key in force", copyRows(pgxConn(t, e.admin, false), "bypass", time.Now().Add(-time.Minute)),
		"23505", "idempotency_records_key_in_force")
	requireRule(t, "administrator COPY from the future", copyRows(pgxConn(t, e.admin, false), "copied", time.Now().Add(time.Hour)), "23001", "")
	if err := copyRows(pgxConn(t, e.admin, false), "copied", time.Now().Add(-time.Hour)); err != nil {
		t.Errorf("administrator COPY of a valid record: %v", err)
	}
	requireGenerations(t, "after the copies", e.admin, ledgerSpaceA, "copied", 1)

	// SET ROLE needs another role to belong to: the catalog proves the API role
	// has none, and that it has no attribute that would bypass anything.
	if got := count(t, e.admin, `SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = $1`, db.APIRole); got != 0 {
		t.Errorf("the API role is a member of %d roles, want none", got)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM pg_roles WHERE rolname = $1 AND (rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolcanlogin)`, db.APIRole); got != 0 {
		t.Error("the API role has an attribute that bypasses the guards, or can log in")
	}
}

// TestIdempotencySessionReplicationRoleAndAlterTableDeniedToAPI guards the attacks that
// need more than the privileges of the API role: replication mode, DDL, trigger
// switches, locks and system columns.
func TestIdempotencySessionReplicationRoleAndAlterTableDeniedToAPI(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	for _, query := range []string{
		`SET session_replication_role = replica`,
		`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER ALL`,
		`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER USER`,
		`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER idempotency_records_guard_insert`,
		`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER idempotency_records_no_update`,
		`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER idempotency_records_no_delete`,
		`ALTER TABLE kernel.idempotency_records DISABLE TRIGGER idempotency_records_no_truncate`,
		`ALTER TABLE kernel.idempotency_records ENABLE REPLICA TRIGGER idempotency_records_guard_insert`,
		`DROP TRIGGER idempotency_records_no_update ON kernel.idempotency_records`,
		`DROP TRIGGER idempotency_records_guard_insert ON kernel.idempotency_records`,
		`CREATE TRIGGER rogue BEFORE INSERT ON kernel.idempotency_records FOR EACH ROW EXECUTE FUNCTION kernel.stamp_exchange()`,
		`ALTER TABLE kernel.idempotency_records DISABLE ROW LEVEL SECURITY`,
		`ALTER TABLE kernel.idempotency_records NO FORCE ROW LEVEL SECURITY`,
		`ALTER TABLE kernel.idempotency_records DROP CONSTRAINT idempotency_records_ttl`,
		`ALTER TABLE kernel.idempotency_records DROP CONSTRAINT idempotency_records_pkey`,
		`ALTER TABLE kernel.idempotency_records DROP CONSTRAINT idempotency_records_actor_fkey`,
		`ALTER TABLE kernel.idempotency_records ADD COLUMN rogue int`,
		`ALTER TABLE kernel.idempotency_records OWNER TO nexusthrive_api`,
		`ALTER TABLE kernel.idempotency_records ALTER COLUMN generation SET DEFAULT 1`,
		`ALTER FUNCTION kernel.idempotency_records_guard_insert() SECURITY DEFINER`,
		`ALTER FUNCTION kernel.idempotency_lock(text) SECURITY DEFINER`,
		`CREATE OR REPLACE FUNCTION kernel.idempotency_lock(lock_key text) RETURNS void LANGUAGE plpgsql AS 'BEGIN END'`,
		`CREATE OR REPLACE FUNCTION kernel.idempotency_records_guard_insert() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
		`CREATE OR REPLACE FUNCTION kernel.reject_idempotency_change() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
		`DROP FUNCTION kernel.idempotency_lock(text)`,
		`DROP TABLE kernel.idempotency_records`,
		`CREATE POLICY rogue ON kernel.idempotency_records FOR ALL USING (true)`,
		`INSERT INTO kernel.idempotency_records (id, generation) OVERRIDING SYSTEM VALUE VALUES ('01900000-0000-7000-8000-0000000001f3', 5)`,
	} {
		requireRule(t, query, asAPI(t, e.admin, query), "42501", "")
	}
	// A grant by a role that holds no grant option is a no-op, not an error: the privileges stay as they were.
	for _, query := range []string{
		`GRANT UPDATE, DELETE ON kernel.idempotency_records TO nexusthrive_api`,
		`GRANT INSERT (generation, expires_at) ON kernel.idempotency_records TO nexusthrive_api`,
	} {
		_ = asAPI(t, e.admin, query)
	}
	if got, want := privilegeSet(t, e.admin, "idempotency_records"), expectPrivileges(idempotencyColumns, idempotencyInsertColumns, nil); !slices.Equal(got, want) {
		t.Errorf("privileges after the API granted itself more = %v, want %v", got, want)
	}
	requireRule(t, "LOCK TABLE kernel.idempotency_records", runTx(t, e.api, "", stmt{`LOCK TABLE kernel.idempotency_records IN SHARE MODE`, nil}), "42501", "")
	if got := count(t, e.admin, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'kernel'
		AND table_name = 'idempotency_records' AND (is_identity = 'YES' OR is_generated <> 'NEVER' OR column_default IS NOT NULL)`); got != 0 {
		t.Errorf("%d identity, generated or defaulted columns in idempotency_records, want none", got)
	}
}

// TestRecordWithoutExchangeIsHarmlessButOwnSpaceOnly documents the limit of the
// API role: it is the process of the kernel (follow-up 42), so it can write a
// forged record in its own space. That only blocks, or replays a made-up answer
// for, a key of that same space; it crosses no space and the other keys are
// untouched. The composite keys and row level security hold the line.
func TestRecordWithoutExchangeIsHarmlessButOwnSpaceOnly(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	e.seedRecord(t, "poisoned", "now()")
	requireRule(t, "a second record of the poisoned key", runTx(t, e.api, ledgerSpaceA, recordStmt(t, ledgerSpaceA, "poisoned", "now()")),
		"23505", "idempotency_records_key_in_force")
	// The same key in another space is a first run: the forged record reaches nobody else.
	if err := runTx(t, e.api, ledgerSpaceB, recordStmt(t, ledgerSpaceB, "poisoned", "now()")); err != nil {
		t.Errorf("the same key in another space: %v", err)
	}
	// Space B cannot poison the keys of space A.
	requireRule(t, "space B names space A", runTx(t, e.api, ledgerSpaceB, recordStmt(t, ledgerSpaceA, "never-written", "now()")), "23001", "")
	requireGenerations(t, "space A", e.admin, ledgerSpaceA, "poisoned", 1)
	requireGenerations(t, "space B", e.admin, ledgerSpaceB, "poisoned", 1)
	requireGenerations(t, "never written", e.admin, ledgerSpaceA, "never-written")
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.exchanges`); got != 0 {
		t.Errorf("%d exchanges, want 0: a record is not an exchange", got)
	}
}

// TestHashCollisionCheckIsFullLength guards that only a fingerprint of exactly
// 32 bytes is stored, so a truncated or padded one can never match a full one.
func TestHashCollisionCheckIsFullLength(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	for _, w := range e.writers() {
		for name, size := range map[string]int{"31 bytes": 31, "33 bytes": 33, "16 bytes": 16, "64 bytes": 64, "none": 0} {
			s := stmt{`INSERT INTO kernel.idempotency_records ` + recordColumns + `
				VALUES ($1, $2, 'hash-size', 'test.pay', $3, ` + decodeHash(size) + `, '{}'::jsonb, now())`, []any{newID(t), ledgerSpaceA, ledgerParticipantA}}
			requireRule(t, w.name+": fingerprint of "+name, runTx(t, w.pool, w.space, s), "23514", "idempotency_records_hash_size")
		}
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.idempotency_records`); got != 0 {
		t.Errorf("%d records after the refused inserts, want 0", got)
	}
}

// TestIdempotencySpaceSwitchBeforeCommitFailsClosed guards the attack of changing
// kernel.space_id inside a transaction: a record is bound to the space that was
// current when it was written, and after the switch the lock and every insert
// refuse, so the transaction cannot write into or read from a space it left.
func TestIdempotencySpaceSwitchBeforeCommitFailsClosed(t *testing.T) {
	t.Parallel()
	e := newIdempotencyEnv(t)
	for name, switchTo := range map[string]string{"space B": ledgerSpaceB, "no space": "", "nil uuid": "00000000-0000-0000-0000-000000000000"} {
		tx, err := e.api.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		key := "switch-" + strings.ReplaceAll(name, " ", "-")
		s := recordStmt(t, ledgerSpaceA, key, "now()")
		if _, err := tx.Exec(s.query, s.args...); err != nil {
			t.Fatalf("%s: first insert: %v", name, err)
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, switchTo); err != nil {
			t.Fatalf("switch space: %v", err)
		}
		again := recordStmt(t, ledgerSpaceA, key+"-after", "now()")
		_, err = tx.Exec(again.query, again.args...)
		requireRule(t, name+": insert of space A after the switch", err, "23001", "")
		_ = tx.Rollback()
		requireGenerations(t, name+": rolled back", e.admin, ledgerSpaceA, key)
	}

	// A record written before the switch and committed stays in its own space.
	tx, err := e.api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceA); err != nil {
		t.Fatalf("set space: %v", err)
	}
	s := recordStmt(t, ledgerSpaceA, "kept", "now()")
	if _, err := tx.Exec(s.query, s.args...); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, ledgerSpaceB); err != nil {
		t.Fatalf("switch space: %v", err)
	}
	var seen int
	if err := tx.QueryRow(`SELECT count(*) FROM kernel.idempotency_records WHERE key = 'kept'`).Scan(&seen); err != nil || seen != 0 {
		t.Errorf("after the switch the transaction sees %d records of space A (%v), want 0", seen, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := count(t, e.admin, `SELECT count(*) FROM kernel.idempotency_records WHERE key = 'kept' AND space_id = $1`, ledgerSpaceA); got != 1 {
		t.Errorf("the record of space A is stored %d times, want 1", got)
	}
}
