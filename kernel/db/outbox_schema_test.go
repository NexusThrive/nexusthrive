package db_test

import (
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// outboxBase is the next_attempt_at the seeded rows carry.
var outboxBase = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// Tokens the seeded rows use for their claim.
const (
	outboxToken      = "01900000-0000-7000-8000-0000000000c1"
	outboxOtherToken = "01900000-0000-7000-8000-0000000000c2"
)

// outboxSeed describes the state of a row the administrator inserts directly.
// It reaches states the API role can only reach through valid transitions.
type outboxSeed struct {
	status    string
	attempts  int
	lastError any
	delivered bool
	claimed   bool
}

// Seeds for each state of the life cycle of a row.
var (
	seedFree          = outboxSeed{status: "pending"}
	seedClaimed       = outboxSeed{status: "pending", claimed: true}
	seedClaimedFailed = outboxSeed{status: "failed", attempts: 3, lastError: "boom", claimed: true}
	seedClaimedLast   = outboxSeed{status: "failed", attempts: 4, lastError: "boom", claimed: true}
	seedSent          = outboxSeed{status: "sent", delivered: true}
	seedDead          = outboxSeed{status: "dead", attempts: 5, lastError: "boom"}
)

// newOutboxEvent inserts an event in space as the administrator and returns
// its seq.
func newOutboxEvent(t *testing.T, admin *sql.DB, space string) int64 {
	t.Helper()
	var seq int64
	if err := admin.QueryRow(`INSERT INTO kernel.events (id, space_id, type, version, payload, actor_kind)
		VALUES ($1, $2, 'kernel.test.recorded', 1, '{}', 'system') RETURNING seq`, newID(t), space).Scan(&seq); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return seq
}

// seedOutbox inserts a row in the given state as the administrator.
func seedOutbox(t *testing.T, admin *sql.DB, seq int64, space, subscriber string, s outboxSeed) {
	t.Helper()
	var delivered, token, until any
	if s.delivered {
		delivered = outboxBase
	}
	if s.claimed {
		token, until = outboxToken, outboxBase.Add(5*time.Minute)
	}
	if _, err := admin.Exec(`INSERT INTO kernel.outbox
		(event_seq, space_id, subscriber, status, attempts, next_attempt_at, last_error, delivered_at, claim_token, claimed_until)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		seq, space, subscriber, s.status, s.attempts, outboxBase, s.lastError, delivered, token, until); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
}

// outboxSnapshot reads the columns that identify the state of a row.
func outboxSnapshot(t *testing.T, admin *sql.DB, seq int64, subscriber string) string {
	t.Helper()
	var snapshot string
	if err := admin.QueryRow(`SELECT concat_ws('|', status, attempts, next_attempt_at, coalesce(last_error, '-'),
		coalesce(delivered_at::text, '-'), coalesce(claim_token::text, '-'), coalesce(claimed_until::text, '-'))
		FROM kernel.outbox WHERE event_seq = $1 AND subscriber = $2`, seq, subscriber).Scan(&snapshot); err != nil {
		t.Fatalf("read outbox row: %v", err)
	}
	return snapshot
}

// TestOutboxSubscriberCheckConstraint guards criterion 3 in the database.
func TestOutboxSubscriberCheckConstraint(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedEventFixtures(t, admin)
	seq := newOutboxEvent(t, admin, eventSpaceA)
	for _, subscriber := range []string{"", "Internal", "internal ", "webhook", "x'; DROP TABLE kernel.outbox; --"} {
		_, err := admin.Exec(`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
			VALUES ($1, $2, $3, now())`, seq, eventSpaceA, subscriber)
		requireRule(t, "subscriber "+subscriber, err, "23514", "outbox_subscriber_check")
	}
	for _, subscriber := range []string{"webhooks", "n8n", "ai", "internal"} {
		if _, err := admin.Exec(`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
			VALUES ($1, $2, $3, now())`, seq, eventSpaceA, subscriber); err != nil {
			t.Errorf("subscriber %s rejected: %v", subscriber, err)
		}
	}
}

// TestOutboxStateCheckConstraints exercises each shape rule of the table, which
// ties the attempt limit of 5 to the states so no role can skip or evade it.
func TestOutboxStateCheckConstraints(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	seedEventFixtures(t, admin)

	const insert = `INSERT INTO kernel.outbox
		(event_seq, space_id, subscriber, status, attempts, next_attempt_at, last_error, delivered_at, claim_token, claimed_until)
		VALUES ($1, $2, 'internal', $3, $4, now(), $5, $6, $7, $8)`
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	type row struct {
		status          string
		attempts        int
		lastError       any
		delivered       any
		token, claimEnd any
	}
	bad := map[string]struct {
		row        row
		constraint string
	}{
		"pending with attempts":         {row{"pending", 1, nil, nil, nil, nil}, "outbox_state_check"},
		"pending with error":            {row{"pending", 0, "x", nil, nil, nil}, "outbox_state_check"},
		"pending delivered":             {row{"pending", 0, nil, now, nil, nil}, "outbox_state_check"},
		"failed without attempts":       {row{"failed", 0, "x", nil, nil, nil}, "outbox_state_check"},
		"failed at the limit":           {row{"failed", 5, "x", nil, nil, nil}, "outbox_state_check"},
		"failed without error":          {row{"failed", 1, nil, nil, nil, nil}, "outbox_state_check"},
		"failed delivered":              {row{"failed", 1, "x", now, nil, nil}, "outbox_state_check"},
		"sent without delivery":         {row{"sent", 0, nil, nil, nil, nil}, "outbox_state_check"},
		"sent with claim":               {row{"sent", 0, nil, now, outboxToken, until}, "outbox_state_check"},
		"sent past the limit":           {row{"sent", 5, nil, now, nil, nil}, "outbox_state_check"},
		"dead before the limit":         {row{"dead", 4, "x", nil, nil, nil}, "outbox_state_check"},
		"dead beyond the limit":         {row{"dead", 6, "x", nil, nil, nil}, "outbox_state_check"},
		"dead without error":            {row{"dead", 5, nil, nil, nil, nil}, "outbox_state_check"},
		"dead with claim":               {row{"dead", 5, "x", nil, outboxToken, until}, "outbox_state_check"},
		"claim token without lease":     {row{"pending", 0, nil, nil, outboxToken, nil}, "outbox_claim_check"},
		"lease without claim token":     {row{"pending", 0, nil, nil, nil, until}, "outbox_claim_check"},
		"blank error":                   {row{"failed", 1, "  ", nil, nil, nil}, ""},
		"error over 2000 characters":    {row{"failed", 1, strings.Repeat("x", 2001), nil, nil, nil}, ""},
		"unknown status":                {row{"claimed", 0, nil, nil, nil, nil}, ""},
		"negative attempts as failed":   {row{"failed", -1, "x", nil, nil, nil}, "outbox_state_check"},
		"pending with a negative count": {row{"pending", -1, nil, nil, nil, nil}, "outbox_state_check"},
	}
	for name, tc := range bad {
		seq := newOutboxEvent(t, admin, eventSpaceA)
		r := tc.row
		_, err := admin.Exec(insert, seq, eventSpaceA, r.status, r.attempts, r.lastError, r.delivered, r.token, r.claimEnd)
		requireRule(t, name, err, "23514", tc.constraint)
	}
	good := map[string]row{
		"pending":         {"pending", 0, nil, nil, nil, nil},
		"claimed pending": {"pending", 0, nil, nil, outboxToken, until},
		"failed 1":        {"failed", 1, "x", nil, nil, nil},
		"failed 4 held":   {"failed", 4, "x", nil, outboxToken, until},
		"sent":            {"sent", 0, nil, now, nil, nil},
		"sent after 4":    {"sent", 4, "earlier", now, nil, nil},
		"dead":            {"dead", 5, "x", nil, nil, nil},
	}
	for name, r := range good {
		seq := newOutboxEvent(t, admin, eventSpaceA)
		if _, err := admin.Exec(insert, seq, eventSpaceA, r.status, r.attempts, r.lastError, r.delivered, r.token, r.claimEnd); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
}

// TestOutboxCompositeForeignKey guards the cross-space reference: the key is
// (event_seq, space_id), and the check ignores RLS, so an event of space A
// cannot be named by a row of space B.
func TestOutboxCompositeForeignKey(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedEventFixtures(t, admin)
	seqA := newOutboxEvent(t, admin, eventSpaceA)

	_, err := admin.Exec(`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at) VALUES ($1, $2, 'internal', now())`, seqA, eventSpaceB)
	requireRule(t, "administrator, event of A with space B", err, "23503", "outbox_event_fkey")
	err = runTx(t, api, eventSpaceB, stmt{`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
		VALUES ($1, kernel.current_space_id(), 'internal', now())`, []any{seqA}})
	requireRule(t, "API in B, event of A", err, "23503", "outbox_event_fkey")
	err = runTx(t, api, eventSpaceB, stmt{`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
		VALUES ($1, $2, 'internal', now())`, []any{seqA, eventSpaceA}})
	requireRule(t, "API in B naming space A", err, "42501", "")
	err = runTx(t, api, eventSpaceA, stmt{`INSERT INTO kernel.outbox (event_seq, space_id, subscriber, next_attempt_at)
		VALUES (999999, kernel.current_space_id(), 'internal', now())`, nil})
	requireRule(t, "event that does not exist", err, "23503", "outbox_event_fkey")
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox`); got != 0 {
		t.Errorf("%d rows were written, want none", got)
	}
}

// TestEventsKeyForOutboxDoesNotChangeEventIndexes checks that the unique key
// the outbox needs on events is present and that events still has its own.
func TestEventsKeyForOutboxDoesNotChangeEventIndexes(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	if got := count(t, admin, `SELECT count(*) FROM pg_constraint WHERE conrelid = 'kernel.events'::regclass
		AND conname = 'events_seq_space_key' AND contype = 'u'`); got != 1 {
		t.Errorf("events_seq_space_key present %d times, want 1", got)
	}
	if got := count(t, admin, `SELECT count(*) FROM pg_constraint WHERE conrelid = 'kernel.events'::regclass AND contype = 'p'`); got != 1 {
		t.Errorf("events keeps %d primary keys, want 1", got)
	}
}

// TestOutboxMigrationFunctionsAreSafe checks that the two triggers fix their
// search_path and are not SECURITY DEFINER, so a planted function cannot
// replace what they call and they run with the privileges of the caller.
func TestOutboxMigrationFunctionsAreSafe(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	for _, name := range []string{"reject_outbox_removal", "guard_outbox_change"} {
		var secDef bool
		var config string
		err := admin.QueryRow(`SELECT p.prosecdef, coalesce(array_to_string(p.proconfig, ','), '') FROM pg_proc p
			JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'kernel' AND p.proname = $1`, name).Scan(&secDef, &config)
		if err != nil {
			t.Fatalf("read function %s: %v", name, err)
		}
		if secDef {
			t.Errorf("%s is SECURITY DEFINER", name)
		}
		if !strings.Contains(config, "search_path=pg_catalog, pg_temp") {
			t.Errorf("%s config = %q, want a fixed search_path", name, config)
		}
	}
}

// TestOutboxForcedRLSAndPolicies checks RLS, the exact policies and the exact
// privileges of the API role, column by column.
func TestOutboxForcedRLSAndPolicies(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	requireForcedRLS(t, admin, "outbox")

	cmds := stringColumn(t, admin, `SELECT cmd FROM pg_policies WHERE schemaname = 'kernel' AND tablename = 'outbox' ORDER BY cmd`)
	if want := []string{"INSERT", "SELECT", "UPDATE"}; !slices.Equal(cmds, want) {
		t.Errorf("outbox policies = %v, want %v (no DELETE policy)", cmds, want)
	}

	columns := stringColumn(t, admin, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'kernel' AND table_name = 'outbox' ORDER BY 1`)
	wantColumns := []string{"attempts", "claim_token", "claimed_until", "created_at", "delivered_at", "event_seq",
		"last_error", "next_attempt_at", "space_id", "status", "subscriber"}
	if !slices.Equal(columns, wantColumns) {
		t.Fatalf("outbox columns = %v, want %v", columns, wantColumns)
	}
	allowed := func(privilege string) []string {
		var out []string
		for _, column := range columns {
			var ok bool
			if err := admin.QueryRow(`SELECT has_column_privilege($1, 'kernel.outbox', $2, $3)`, db.APIRole, column, privilege).Scan(&ok); err != nil {
				t.Fatalf("has_column_privilege: %v", err)
			}
			if ok {
				out = append(out, column)
			}
		}
		return out
	}
	if got := allowed("SELECT"); !slices.Equal(got, wantColumns) {
		t.Errorf("API SELECT columns = %v, want all", got)
	}
	if got, want := allowed("INSERT"), []string{"event_seq", "next_attempt_at", "space_id", "subscriber"}; !slices.Equal(got, want) {
		t.Errorf("API INSERT columns = %v, want %v", got, want)
	}
	if got, want := allowed("UPDATE"), []string{"attempts", "claim_token", "claimed_until", "delivered_at", "last_error",
		"next_attempt_at", "status"}; !slices.Equal(got, want) {
		t.Errorf("API UPDATE columns = %v, want %v (never identity columns)", got, want)
	}
	for _, privilege := range []string{"DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
		var ok bool
		if err := admin.QueryRow(`SELECT has_table_privilege($1, 'kernel.outbox', $2)`, db.APIRole, privilege).Scan(&ok); err != nil {
			t.Fatalf("has_table_privilege: %v", err)
		}
		if ok {
			t.Errorf("the API role holds %s on kernel.outbox", privilege)
		}
	}
}
