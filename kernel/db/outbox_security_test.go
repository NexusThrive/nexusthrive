package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// outboxSetup returns the administrator and API pools with the event fixtures.
func outboxSetup(t *testing.T) (admin, api *sql.DB) {
	t.Helper()
	admin = dbtest.Database(t)
	api = dbtest.APIPool(t, admin)
	seedEventFixtures(t, admin)
	return admin, api
}

// updateRow builds an UPDATE of the row identified by seq and the internal
// subscriber.
func updateRow(seq int64, set string) stmt {
	return stmt{`UPDATE kernel.outbox SET ` + set + ` WHERE event_seq = $1 AND subscriber = 'internal'`, []any{seq}}
}

// asRoles runs one statement as the API role inside space A and as the
// administrator, who bypasses RLS and holds every privilege, and returns both
// errors in that order.
func asRoles(t *testing.T, admin, api *sql.DB, s stmt) (apiErr, adminErr error) {
	t.Helper()
	return runTx(t, api, eventSpaceA, s), runTx(t, admin, "", s)
}

// TestOutboxAPIInsertCannotSetKernelColumns guards the attack of inserting a
// row that is already sent, has false attempts or holds a claim: the API role
// has INSERT only on the columns a caller legitimately chooses.
func TestOutboxAPIInsertCannotSetKernelColumns(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	for column, value := range map[string]string{
		"status": "'sent'", "attempts": "4", "claim_token": "'" + outboxToken + "'",
		"claimed_until": "now()", "delivered_at": "now()", "last_error": "'x'", "created_at": "now()",
	} {
		seq := newOutboxEvent(t, admin, eventSpaceA)
		err := runTx(t, api, eventSpaceA, stmt{`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at, ` + column + `)
			VALUES ($1, kernel.current_space_id(), 'internal', now(), ` + value + `)`, []any{seq}})
		requireRule(t, "insert naming "+column, err, "42501", "")
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox`); got != 0 {
		t.Errorf("%d rows were written, want none", got)
	}
	// The normal path still works and starts the row in the initial state.
	seq := newOutboxEvent(t, admin, eventSpaceA)
	if err := runTx(t, api, eventSpaceA, stmt{`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
		VALUES ($1, kernel.current_space_id(), 'internal', now())`, []any{seq}}); err != nil {
		t.Fatalf("ordinary insert: %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox WHERE status = 'pending' AND attempts = 0
		AND claim_token IS NULL AND delivered_at IS NULL AND last_error IS NULL`); got != 1 {
		t.Error("the ordinary insert did not start the row as pending with no attempts")
	}
}

// TestOutboxTransitionsRejected guards the transition trigger: the same
// statement fails for the API role and for the administrator, who is the owner
// and has every privilege.
func TestOutboxTransitionsRejected(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	const next = `, next_attempt_at = next_attempt_at + interval '1 minute'`
	const release = `claim_token = NULL, claimed_until = NULL`
	cases := []struct {
		name string
		from outboxSeed
		set  string
	}{
		{"reopen a sent row", seedSent, `status = 'pending', delivered_at = NULL`},
		{"change a sent row in place", seedSent, `last_error = 'x'`},
		{"claim a sent row", seedSent, `claim_token = '` + outboxOtherToken + `', claimed_until = now()`},
		{"revive a dead row as failed", seedDead, `status = 'failed', attempts = 4`},
		{"revive a dead row as pending", seedDead, `status = 'pending', attempts = 0, last_error = NULL`},
		{"lower the attempts of a held row", seedClaimedFailed, `status = 'failed', attempts = 1, ` + release},
		{"keep the attempts of a held row", seedClaimedFailed, `status = 'failed', ` + release},
		{"skip an attempt", seedClaimed, `status = 'failed', attempts = 2, last_error = 'x', ` + release + next},
		{"jump to dead", seedClaimedFailed, `status = 'dead', attempts = 5, last_error = 'x', ` + release},
		{"move next_attempt_at back", seedClaimed, `status = 'failed', attempts = 1, last_error = 'x', ` + release + `, next_attempt_at = next_attempt_at - interval '1 hour'`},
		{"deliver without a claim", seedFree, `status = 'sent', delivered_at = now()`},
		{"stamp delivered_at alone", seedFree, `delivered_at = now()`},
		{"change attempts of a free row", seedFree, `attempts = 3`},
		{"push a free row back in time", seedFree, `next_attempt_at = next_attempt_at + interval '1 hour'`},
		{"fail a free row", seedFree, `status = 'failed', attempts = 1, last_error = 'x'`},
		{"steal a held row", seedClaimed, `claim_token = '` + outboxOtherToken + `', claimed_until = claimed_until + interval '1 hour'`},
		{"extend the lease of a held row", seedClaimed, `claimed_until = claimed_until + interval '1 hour'`},
		{"write an error while claiming", seedFree, `claim_token = '` + outboxToken + `', claimed_until = now(), last_error = 'x'`},
		{"claim and change status", seedFree, `claim_token = '` + outboxToken + `', claimed_until = now(), status = 'failed'`},
		{"release a held row untouched", seedClaimed, release},
		{"send and change attempts", seedClaimed, `status = 'sent', delivered_at = now(), attempts = 1, ` + release},
		{"send and write an error", seedClaimedFailed, `status = 'sent', delivered_at = now(), last_error = 'other', ` + release},
		{"send and move next_attempt_at", seedClaimed, `status = 'sent', delivered_at = now(), ` + release + next},
		{"keep the claim after sending", seedClaimed, `status = 'sent', delivered_at = now()`},
		{"keep the claim after failing", seedClaimed, `status = 'failed', attempts = 1, last_error = 'x'`},
	}
	for _, tc := range cases {
		for _, role := range []string{"API", "administrator"} {
			seq := newOutboxEvent(t, admin, eventSpaceA)
			seedOutbox(t, admin, seq, eventSpaceA, "internal", tc.from)
			before := outboxSnapshot(t, admin, seq, "internal")
			var err error
			if role == "API" {
				err = runTx(t, api, eventSpaceA, updateRow(seq, tc.set))
			} else {
				err = runTx(t, admin, "", updateRow(seq, tc.set))
			}
			requireRule(t, role+": "+tc.name, err, "23001", "")
			if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
				t.Errorf("%s: %s changed the row: %q -> %q", role, tc.name, before, after)
			}
		}
	}
}

// TestOutboxDeadNeedsTheFifthAttempt guards the limit: one more attempt is a
// valid step for the trigger, so it is the table check that refuses a row that
// is dead before its fifth attempt or still failed at it, for every role.
func TestOutboxDeadNeedsTheFifthAttempt(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	const release = `claim_token = NULL, claimed_until = NULL`
	for name, tc := range map[string]struct {
		from outboxSeed
		set  string
	}{
		"dead after the first attempt": {seedClaimed, `status = 'dead', attempts = 1, last_error = 'x', ` + release},
		"dead after the fourth":        {seedClaimedFailed, `status = 'dead', attempts = 4, last_error = 'x', ` + release},
		"failed past the limit":        {seedClaimedLast, `status = 'failed', attempts = 5, last_error = 'x', ` + release},
	} {
		for _, role := range []string{"API", "administrator"} {
			seq := newOutboxEvent(t, admin, eventSpaceA)
			seedOutbox(t, admin, seq, eventSpaceA, "internal", tc.from)
			before := outboxSnapshot(t, admin, seq, "internal")
			pool, space := api, eventSpaceA
			if role == "administrator" {
				pool, space = admin, ""
			}
			requireRule(t, role+": "+name, runTx(t, pool, space, updateRow(seq, tc.set)), "23514", "outbox_state_check")
			if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
				t.Errorf("%s: %s changed the row", role, name)
			}
		}
	}
}

// TestOutboxValidTransitionsStillWork is the control of the trigger test: the
// transitions the kernel needs pass for the API role, so the trigger is not
// simply refusing everything.
func TestOutboxValidTransitionsStillWork(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	const release = `claim_token = NULL, claimed_until = NULL`
	cases := []struct {
		name string
		from outboxSeed
		set  string
		want string
	}{
		{"claim a free row", seedFree, `claim_token = '` + outboxToken + `', claimed_until = now()`, "pending"},
		{"claim a failed row", outboxSeed{status: "failed", attempts: 2, lastError: "boom"}, `claim_token = '` + outboxToken + `', claimed_until = now()`, "failed"},
		{"send a held row", seedClaimed, `status = 'sent', delivered_at = now(), ` + release, "sent"},
		{"send after failures", seedClaimedFailed, `status = 'sent', delivered_at = now(), ` + release, "sent"},
		{"fail a held row", seedClaimed, `status = 'failed', attempts = 1, last_error = 'x', ` + release + `, next_attempt_at = next_attempt_at + interval '30 seconds'`, "failed"},
		{"fail without delay", seedClaimedFailed, `status = 'failed', attempts = 4, last_error = 'x', ` + release, "failed"},
		{"kill after the fifth failure", seedClaimedLast, `status = 'dead', attempts = 5, last_error = 'x', ` + release, "dead"},
	}
	for _, tc := range cases {
		seq := newOutboxEvent(t, admin, eventSpaceA)
		seedOutbox(t, admin, seq, eventSpaceA, "internal", tc.from)
		if err := runTx(t, api, eventSpaceA, updateRow(seq, tc.set)); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := count(t, admin, `SELECT count(*) FROM kernel.outbox WHERE event_seq = $1 AND status = $2`, seq, tc.want); got != 1 {
			t.Errorf("%s: the row did not reach %s", tc.name, tc.want)
		}
	}
}

// TestOutboxIdentityColumnsImmutable guards the identity of a row: the API
// role has no UPDATE on those columns and, for anyone else, the trigger
// refuses the change.
func TestOutboxIdentityColumnsImmutable(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	other := newOutboxEvent(t, admin, eventSpaceA)
	// The row is held and the change otherwise a valid send, so the identity
	// rule is the only thing that can stop it.
	const send = `status = 'sent', delivered_at = now(), claim_token = NULL, claimed_until = NULL, `
	for _, set := range []string{
		fmt.Sprintf("event_seq = %d", other), `space_id = '` + eventSpaceB + `'`, `subscriber = 'ai'`, `created_at = now() - interval '1 day'`,
	} {
		seq := newOutboxEvent(t, admin, eventSpaceA)
		seedOutbox(t, admin, seq, eventSpaceA, "internal", seedClaimed)
		before := outboxSnapshot(t, admin, seq, "internal")
		apiErr, adminErr := asRoles(t, admin, api, updateRow(seq, send+set))
		requireRule(t, "API "+set, apiErr, "42501", "")
		requireRule(t, "administrator "+set, adminErr, "23001", "")
		if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
			t.Errorf("%s: the row changed: %q -> %q", set, before, after)
		}
	}
}

// TestOutboxNoDeleteNoTruncate guards removal: the API role has no privilege,
// and for everyone else the triggers refuse, also through TRUNCATE ... CASCADE
// from the tables the outbox references.
func TestOutboxNoDeleteNoTruncate(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	seq := newOutboxEvent(t, admin, eventSpaceA)
	seedOutbox(t, admin, seq, eventSpaceA, "internal", seedSent)
	before := outboxSnapshot(t, admin, seq, "internal")

	for _, query := range []string{`DELETE FROM kernel.outbox`, `TRUNCATE kernel.outbox`, `TRUNCATE kernel.outbox CASCADE`} {
		requireRule(t, "API "+query, runTx(t, api, eventSpaceA, stmt{query, nil}), "42501", "")
		requireRule(t, "administrator "+query, runTx(t, admin, "", stmt{query, nil}), "23001", "")
	}
	// The parents have their own triggers, which would fail first. Switching
	// them off inside a rolled-back transaction shows that the outbox trigger
	// alone stops a cascade from reaching it.
	for _, query := range []string{`TRUNCATE kernel.events CASCADE`, `TRUNCATE kernel.spaces CASCADE`} {
		tx, err := admin.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		for _, disable := range []string{
			`ALTER TABLE kernel.events DISABLE TRIGGER events_no_truncate`,
		} {
			if _, err := tx.Exec(disable); err != nil {
				t.Fatalf("%s: %v", disable, err)
			}
		}
		if query == `TRUNCATE kernel.spaces CASCADE` {
			rows, err := tx.Query(`SELECT c.relname, t.tgname FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
				JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'kernel' AND NOT t.tgisinternal
				AND (t.tgtype & 32) <> 0 AND c.relname <> 'outbox'`)
			if err != nil {
				t.Fatalf("list truncate triggers: %v", err)
			}
			var disables []string
			for rows.Next() {
				var table, trigger string
				if err := rows.Scan(&table, &trigger); err != nil {
					t.Fatalf("scan: %v", err)
				}
				disables = append(disables, `ALTER TABLE kernel.`+pgx.Identifier{table}.Sanitize()+` DISABLE TRIGGER `+pgx.Identifier{trigger}.Sanitize())
			}
			rows.Close()
			for _, disable := range disables {
				if _, err := tx.Exec(disable); err != nil {
					t.Fatalf("%s: %v", disable, err)
				}
			}
		}
		_, err = tx.Exec(query)
		_ = tx.Rollback()
		requireRule(t, "cascade "+query, err, "23001", "")
		if err != nil && !strings.Contains(err.Error(), "outbox") {
			t.Errorf("%s was stopped by something other than the outbox trigger: %v", query, err)
		}
	}
	if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
		t.Errorf("the row changed: %q -> %q", before, after)
	}
}

// TestOutboxOnConflictCannotRewrite guards INSERT ... ON CONFLICT DO UPDATE,
// which reaches UPDATE through the insert path: the same column grants and the
// same trigger apply, so a terminal or free row cannot be rewritten.
func TestOutboxOnConflictCannotRewrite(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	for name, tc := range map[string]struct {
		from outboxSeed
		set  string
	}{
		"reopen a sent row":       {seedSent, `status = 'pending', delivered_at = NULL`},
		"rewrite a sent row":      {seedSent, `next_attempt_at = EXCLUDED.next_attempt_at`},
		"deliver a free row":      {seedFree, `status = 'sent', delivered_at = now()`},
		"reset a free row":        {seedFree, `next_attempt_at = EXCLUDED.next_attempt_at + interval '1 day'`},
		"revive a dead row":       {seedDead, `status = 'failed', attempts = 1`},
		"fail a free row":         {seedFree, `status = 'failed', attempts = 1, last_error = 'x'`},
		"steal a held row":        {seedClaimed, `claim_token = '` + outboxOtherToken + `', claimed_until = now()`},
		"rewrite identity column": {seedFree, `subscriber = 'ai'`},
	} {
		seq := newOutboxEvent(t, admin, eventSpaceA)
		seedOutbox(t, admin, seq, eventSpaceA, "internal", tc.from)
		before := outboxSnapshot(t, admin, seq, "internal")
		err := runTx(t, api, eventSpaceA, stmt{`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
			VALUES ($1, kernel.current_space_id(), 'internal', now())
			ON CONFLICT (event_seq, subscriber) DO UPDATE SET ` + tc.set, []any{seq}})
		if got := pgCode(err); got != "23001" && got != "42501" {
			t.Errorf("%s: SQLSTATE %q (%v), want 23001 or 42501", name, got, err)
		}
		if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
			t.Errorf("%s changed the row: %q -> %q", name, before, after)
		}
	}
	// DO NOTHING keeps the row as it is.
	seq := newOutboxEvent(t, admin, eventSpaceA)
	seedOutbox(t, admin, seq, eventSpaceA, "internal", seedSent)
	before := outboxSnapshot(t, admin, seq, "internal")
	if err := runTx(t, api, eventSpaceA, stmt{`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
		VALUES ($1, kernel.current_space_id(), 'internal', now()) ON CONFLICT DO NOTHING`, []any{seq}}); err != nil {
		t.Fatalf("DO NOTHING: %v", err)
	}
	if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
		t.Errorf("DO NOTHING changed the row: %q -> %q", before, after)
	}
}

// TestOutboxCopyRespectsColumnGrants guards COPY. Row level security refuses
// COPY FROM for the API role whatever the columns, so it cannot bulk load the
// queue. For the administrator, who bypasses it, COPY runs the same unique
// key and foreign key as INSERT, and cannot overwrite a row.
func TestOutboxCopyRespectsColumnGrants(t *testing.T) {
	t.Parallel()
	admin, _ := outboxSetup(t)
	ctx := context.Background()
	seq := newOutboxEvent(t, admin, eventSpaceA)
	seedOutbox(t, admin, seq, eventSpaceA, "internal", seedSent)
	before := outboxSnapshot(t, admin, seq, "internal")

	apiConn := pgxConn(t, admin, true)
	adminConn := pgxConn(t, admin, false)
	for name, tc := range map[string]struct {
		columns []string
		want    string
	}{
		// Granted columns still hit the RLS refusal of COPY FROM.
		"granted columns": {[]string{"event_seq", "space_id", "subscriber", "next_attempt_at"}, "0A000"},
		// The others are refused for lack of a column privilege first.
		"status":            {[]string{"event_seq", "space_id", "subscriber", "next_attempt_at", "status", "delivered_at"}, "42501"},
		"claim and attempt": {[]string{"event_seq", "space_id", "subscriber", "next_attempt_at", "claim_token", "claimed_until", "attempts"}, "42501"},
	} {
		columns := tc.columns
		tx, err := apiConn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('kernel.space_id', $1, true)`, eventSpaceA); err != nil {
			t.Fatalf("set space: %v", err)
		}
		row := map[string]any{"event_seq": seq, "space_id": eventSpaceA, "subscriber": "ai", "next_attempt_at": outboxBase,
			"status": "sent", "delivered_at": outboxBase, "claim_token": outboxToken, "claimed_until": outboxBase, "attempts": 4}
		values := make([]any, len(columns))
		for i, column := range columns {
			values[i] = row[column]
		}
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"kernel", "outbox"}, columns, pgx.CopyFromRows([][]any{values}))
		_ = tx.Rollback(ctx)
		requireRule(t, "API COPY "+name, err, tc.want, "")
	}
	// The administrator cannot overwrite the existing row, nor reference an
	// event of another space.
	for name, values := range map[string][]any{
		"duplicate key":      {seq, eventSpaceA, "internal", outboxBase},
		"event of space A":   {seq, eventSpaceB, "ai", outboxBase},
		"unknown subscriber": {seq, eventSpaceA, "pigeon", outboxBase},
	} {
		tx, err := adminConn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"kernel", "outbox"},
			[]string{"event_seq", "space_id", "subscriber", "next_attempt_at"}, pgx.CopyFromRows([][]any{values}))
		_ = tx.Rollback(ctx)
		want := map[string]string{"duplicate key": "23505", "event of space A": "23503", "unknown subscriber": "23514"}[name]
		requireRule(t, "administrator COPY "+name, err, want, "")
	}
	if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
		t.Errorf("the row changed: %q -> %q", before, after)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox`); got != 1 {
		t.Errorf("%d rows after the attacks, want only the seeded one", got)
	}
}

// TestOutboxAPICannotDisableTriggersOrChangeRole guards the attacks that need
// more than the column privileges: DDL, disabling triggers, replication mode,
// and moving to the owner.
func TestOutboxAPICannotDisableTriggersOrChangeRole(t *testing.T) {
	t.Parallel()
	admin, _ := outboxSetup(t)
	seq := newOutboxEvent(t, admin, eventSpaceA)
	seedOutbox(t, admin, seq, eventSpaceA, "internal", seedSent)
	before := outboxSnapshot(t, admin, seq, "internal")

	for _, query := range []string{
		`ALTER TABLE kernel.outbox DISABLE TRIGGER ALL`,
		`ALTER TABLE kernel.outbox DISABLE TRIGGER outbox_guard_change`,
		`DROP TRIGGER outbox_guard_change ON kernel.outbox`,
		`ALTER TABLE kernel.outbox DISABLE ROW LEVEL SECURITY`,
		`ALTER TABLE kernel.outbox OWNER TO nexusthrive_api`,
		`ALTER TABLE kernel.outbox DROP CONSTRAINT outbox_state_check`,
		`ALTER TABLE kernel.outbox ADD COLUMN rogue int`,
		`DROP TABLE kernel.outbox`,
		`CREATE OR REPLACE FUNCTION kernel.guard_outbox_change() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
		`ALTER FUNCTION kernel.guard_outbox_change() SECURITY DEFINER`,
		`SET session_replication_role = replica`,
	} {
		requireRule(t, query, asAPI(t, admin, query), "42501", "")
	}
	// A table lock would let one caller stall every writer; it needs a
	// privilege the API role lacks, and it only exists inside a transaction.
	requireRule(t, "LOCK TABLE", runTx(t, dbtest.APIPool(t, admin), "", stmt{`LOCK TABLE kernel.outbox IN ACCESS EXCLUSIVE MODE`, nil}), "42501", "")
	// Moving to the owner needs membership in it; the API role has none, so a
	// session that is only the API role cannot become the owner.
	var member bool
	if err := admin.QueryRow(`SELECT pg_has_role($1, c.relowner, 'USAGE') FROM pg_class c
		WHERE c.oid = 'kernel.outbox'::regclass`, db.APIRole).Scan(&member); err != nil || member {
		t.Errorf("the API role can become the owner of the outbox (%v, %v)", member, err)
	}
	if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
		t.Errorf("the row changed: %q -> %q", before, after)
	}
}

// TestOutboxHasNothingToOverride guards OVERRIDING SYSTEM VALUE: the table has
// no identity or generated column, so there is nothing to override, and the
// clause does not widen what the API role may write.
func TestOutboxHasNothingToOverride(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	if got := count(t, admin, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'kernel'
		AND table_name = 'outbox' AND (is_identity = 'YES' OR is_generated <> 'NEVER')`); got != 0 {
		t.Errorf("%d identity or generated columns in the outbox, want none", got)
	}
	seq := newOutboxEvent(t, admin, eventSpaceA)
	err := runTx(t, api, eventSpaceA, stmt{`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at, status, attempts)
		OVERRIDING SYSTEM VALUE VALUES ($1, kernel.current_space_id(), 'internal', now(), 'sent', 4)`, []any{seq}})
	requireRule(t, "OVERRIDING SYSTEM VALUE naming status", err, "42501", "")
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox`); got != 0 {
		t.Errorf("%d rows were written, want none", got)
	}
}

// TestOutboxUpdateOtherSpaceAffectsZeroRows guards isolation of writes: the
// row of another space is invisible to the update, so nothing changes and the
// attempt is not an error that reveals the row.
func TestOutboxUpdateOtherSpaceAffectsZeroRows(t *testing.T) {
	t.Parallel()
	admin, api := outboxSetup(t)
	seq := newOutboxEvent(t, admin, eventSpaceA)
	seedOutbox(t, admin, seq, eventSpaceA, "internal", seedFree)
	before := outboxSnapshot(t, admin, seq, "internal")

	tx, err := api.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, eventSpaceB); err != nil {
		t.Fatalf("set space: %v", err)
	}
	var visible int
	if err := tx.QueryRow(`SELECT count(*) FROM kernel.outbox`).Scan(&visible); err != nil || visible != 0 {
		t.Errorf("space B sees %d rows of space A (%v)", visible, err)
	}
	for _, query := range []string{
		`UPDATE kernel.outbox SET claim_token = '` + outboxToken + `', claimed_until = now() WHERE event_seq = $1`,
		`UPDATE kernel.outbox SET status = 'sent', delivered_at = now() WHERE event_seq = $1`,
	} {
		result, err := tx.Exec(query, seq)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n, _ := result.RowsAffected(); n != 0 {
			t.Errorf("%s affected %d rows of another space", query, n)
		}
	}
	// Moving the row into the space of the caller is refused, not applied.
	_, err = tx.Exec(`UPDATE kernel.outbox SET space_id = $1 WHERE event_seq = $2`, eventSpaceB, seq)
	requireRule(t, "move the row to another space", err, "42501", "")
	if after := outboxSnapshot(t, admin, seq, "internal"); after != before {
		t.Errorf("the row changed: %q -> %q", before, after)
	}
}
