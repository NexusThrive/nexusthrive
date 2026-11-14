package state_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// outboxT0 is the instant the outbox tests start at, with microseconds so the
// precision of the comparison is exercised.
var outboxT0 = time.Date(2026, 11, 14, 12, 0, 0, 123456000, time.UTC)

// outboxSetup returns the administrator and API pools, the clock and spaces A
// and B seeded.
func outboxSetup(t *testing.T) (admin, api *sql.DB, clock *fixedClock) {
	t.Helper()
	admin, api = eventSetup(t)
	return admin, api, &fixedClock{at: outboxT0}
}

// enqueueEvents records n events in spaceID, each with a row for every
// subscriber, in one transaction, and returns the events.
func enqueueEvents(t *testing.T, api *sql.DB, spaceID string, clock model.Clock, n int, subs ...model.Subscriber) []model.Event {
	t.Helper()
	var events []model.Event
	err := state.InSpace(context.Background(), api, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			event, err := state.AppendEventAndEnqueue(ctx, tx, clock, testEvent("outbox"), subs...)
			if err != nil {
				return err
			}
			events = append(events, event)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("enqueue events: %v", err)
	}
	return events
}

// mustClaim claims a batch and fails the test on error.
func mustClaim(t *testing.T, api *sql.DB, spaceID string, clock model.Clock, s model.Subscriber, limit int) []model.OutboxRow {
	t.Helper()
	rows, err := state.ClaimOutbox(context.Background(), api, clock, spaceID, s, limit)
	if err != nil {
		t.Fatalf("ClaimOutbox: %v", err)
	}
	return rows
}

// outboxRowOf reads one row as the administrator.
func outboxRowOf(t *testing.T, admin *sql.DB, seq int64, s model.Subscriber) (row struct {
	Status        string
	Attempts      int
	NextAttemptAt time.Time
	LastError     sql.NullString
	DeliveredAt   sql.NullTime
	ClaimToken    sql.NullString
	ClaimedUntil  sql.NullTime
	SpaceID       string
}) {
	t.Helper()
	err := admin.QueryRow(`SELECT status, attempts, next_attempt_at, last_error, delivered_at, claim_token::text, claimed_until, space_id::text
		FROM kernel.outbox WHERE event_seq = $1 AND subscriber = $2`, seq, string(s)).
		Scan(&row.Status, &row.Attempts, &row.NextAttemptAt, &row.LastError, &row.DeliveredAt, &row.ClaimToken, &row.ClaimedUntil, &row.SpaceID)
	if err != nil {
		t.Fatalf("read outbox row: %v", err)
	}
	return row
}

// seqsOf returns the event seq of each row.
func seqsOf(rows []model.OutboxRow) []int64 {
	out := make([]int64, len(rows))
	for i, row := range rows {
		out[i] = row.EventSeq
	}
	return out
}

// mark runs MarkOutboxSent or MarkOutboxFailed in spaceID.
func markSent(api *sql.DB, spaceID string, clock model.Clock, row model.OutboxRow) error {
	return state.MarkOutboxSent(context.Background(), api, clock, spaceID, row)
}

// markFailed runs MarkOutboxFailed in spaceID.
func markFailed(api *sql.DB, spaceID string, clock model.Clock, row model.OutboxRow, cause string) error {
	return state.MarkOutboxFailed(context.Background(), api, clock, spaceID, row, cause)
}

// TestEnqueueOutboxWritesPendingRow guards criterion 1.
func TestEnqueueOutboxWritesPendingRow(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	events := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)

	row := outboxRowOf(t, admin, events[0].Seq, model.SubscriberInternal)
	if row.Status != "pending" || row.Attempts != 0 {
		t.Errorf("row = %s with %d attempts, want pending with 0", row.Status, row.Attempts)
	}
	if !row.NextAttemptAt.Equal(outboxT0) {
		t.Errorf("next_attempt_at = %v, want exactly the injected clock %v", row.NextAttemptAt, outboxT0)
	}
	if row.ClaimToken.Valid || row.ClaimedUntil.Valid || row.DeliveredAt.Valid || row.LastError.Valid {
		t.Errorf("a new row carries claim, delivery or error data: %+v", row)
	}
	if row.SpaceID != spaceA {
		t.Errorf("space = %s, want %s", row.SpaceID, spaceA)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox`); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
}

// TestAppendEventAndEnqueueWritesOneRowPerSubscriber covers the integration
// call: one event and one row for each subscriber.
func TestAppendEventAndEnqueueWritesOneRowPerSubscriber(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	events := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal, model.SubscriberN8N, model.SubscriberAI, model.SubscriberWebhooks)
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox WHERE event_seq = $1`, events[0].Seq); got != 4 {
		t.Errorf("rows = %d, want 4", got)
	}
	// No subscribers is an event alone.
	more := enqueueEvents(t, api, spaceA, clock, 1)
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox WHERE event_seq = $1`, more[0].Seq); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.events WHERE seq = $1`, more[0].Seq); got != 1 {
		t.Errorf("events = %d, want 1", got)
	}
}

// TestAppendEventAndEnqueueRollbackLeavesNothing guards criterion 2.
func TestAppendEventAndEnqueueRollbackLeavesNothing(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	tx := beginInSpace(t, api, spaceA)
	if _, err := state.AppendEventAndEnqueue(context.Background(), tx, clock, testEvent("rolled"), model.SubscriberInternal); err != nil {
		t.Fatalf("AppendEventAndEnqueue: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`) + count(t, admin, `SELECT count(*) FROM kernel.outbox`); got != 0 {
		t.Errorf("%d rows survived the rollback, want none", got)
	}

	tx = beginInSpace(t, api, spaceA)
	if _, err := state.AppendEventAndEnqueue(context.Background(), tx, clock, testEvent("kept"), model.SubscriberInternal); err != nil {
		t.Fatalf("AppendEventAndEnqueue: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if events, rows := count(t, admin, `SELECT count(*) FROM kernel.events`), count(t, admin, `SELECT count(*) FROM kernel.outbox`); events != 1 || rows != 1 {
		t.Errorf("after commit: %d events and %d rows, want 1 and 1", events, rows)
	}
}

// TestEnqueueOutboxRejectsUnknownSubscriber guards criterion 3: nothing is
// written and the transaction stays usable.
func TestEnqueueOutboxRejectsUnknownSubscriber(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1)[0]
	for _, s := range []model.Subscriber{"", "Internal", "internal ", "webhook", "x'; DROP TABLE kernel.outbox; --"} {
		tx := beginInSpace(t, api, spaceA)
		err := state.EnqueueOutbox(context.Background(), tx, clock, event.Seq, s)
		requireCode(t, err, model.CodeInvalidInput)
		if err := tx.QueryRow(`SELECT 1`).Scan(new(int)); err != nil {
			t.Errorf("subscriber %q: the transaction is not usable after the rejection: %v", s, err)
		}
		_ = tx.Rollback()

		tx = beginInSpace(t, api, spaceA)
		_, err = state.AppendEventAndEnqueue(context.Background(), tx, clock, testEvent("x"), model.SubscriberInternal, s)
		requireCode(t, err, model.CodeInvalidInput)
		if err := tx.QueryRow(`SELECT 1`).Scan(new(int)); err != nil {
			t.Errorf("subscriber %q: the transaction is not usable after the rejection: %v", s, err)
		}
		_ = tx.Rollback()
	}
	// A repeated subscriber is rejected before anything is written.
	tx := beginInSpace(t, api, spaceA)
	_, err := state.AppendEventAndEnqueue(context.Background(), tx, clock, testEvent("x"), model.SubscriberAI, model.SubscriberAI)
	requireCode(t, err, model.CodeInvalidInput)
	if got := count(t, tx, `SELECT count(*) FROM kernel.events`); got != 1 {
		t.Errorf("events = %d inside the transaction, want only the first", got)
	}
	_ = tx.Rollback()
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox`); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
	tx = beginInSpace(t, api, spaceA)
	requireCode(t, state.EnqueueOutbox(context.Background(), tx, clock, 0, model.SubscriberAI), model.CodeInvalidInput)
	_ = tx.Rollback()
}

// TestEnqueueOutboxRequiresEventOfSameSpace covers an event of another space, a
// repeated row and a transaction with no space: all rule violations.
func TestEnqueueOutboxRequiresEventOfSameSpace(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]

	tx := beginInSpace(t, api, spaceB)
	requireCode(t, state.EnqueueOutbox(context.Background(), tx, clock, event.Seq, model.SubscriberAI), model.CodeRuleViolation)
	_ = tx.Rollback()

	// An event that does not exist gives the same answer, so existence does not leak.
	tx = beginInSpace(t, api, spaceB)
	missing := state.EnqueueOutbox(context.Background(), tx, clock, event.Seq+1000, model.SubscriberAI)
	requireCode(t, missing, model.CodeRuleViolation)
	_ = tx.Rollback()

	tx = beginInSpace(t, api, spaceB)
	other := state.EnqueueOutbox(context.Background(), tx, clock, event.Seq, model.SubscriberAI)
	_ = tx.Rollback()
	if mm, om := other.(*model.Error).Message, missing.(*model.Error).Message; mm != om {
		t.Errorf("messages differ (%q, %q): the answer reveals whether the event exists", mm, om)
	}

	tx = beginInSpace(t, api, spaceA)
	requireCode(t, state.EnqueueOutbox(context.Background(), tx, clock, event.Seq, model.SubscriberInternal), model.CodeRuleViolation)
	_ = tx.Rollback()

	tx, err := api.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	requireCode(t, state.EnqueueOutbox(context.Background(), tx, clock, event.Seq, model.SubscriberAI), model.CodeRuleViolation)
	_ = tx.Rollback()
	if got := count(t, admin, `SELECT count(*) FROM kernel.outbox`); got != 1 {
		t.Errorf("rows = %d, want only the first", got)
	}
}

// TestClaimedRowsAreNotClaimedAgain guards criterion 4 across transactions: the
// first claim is committed and the row locks are gone, so only the claim
// marker stops a second claimer.
func TestClaimedRowsAreNotClaimedAgain(t *testing.T) {
	t.Parallel()
	_, api, clock := outboxSetup(t)
	enqueueEvents(t, api, spaceA, clock, 3, model.SubscriberInternal)
	first := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10)
	if len(first) != 3 {
		t.Fatalf("first claim = %d rows, want 3", len(first))
	}
	if again := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10); len(again) != 0 {
		t.Errorf("second claim = %d rows, want 0", len(again))
	}
	token := first[0].ClaimToken
	for _, row := range first {
		if row.ClaimToken != token || row.ClaimedUntil == nil || !row.ClaimedUntil.Equal(outboxT0.Add(model.OutboxLease)) {
			t.Errorf("row %+v: want one token and a lease of five minutes", row)
		}
		if parsed, err := uuid.Parse(row.ClaimToken); err != nil || parsed.Version() != 7 {
			t.Errorf("claim token %q is not a UUID v7", row.ClaimToken)
		}
		if row.SpaceID != spaceA || row.Subscriber != model.SubscriberInternal || row.Status != model.OutboxPending {
			t.Errorf("row = %+v, want space A, internal and pending", row)
		}
	}
	// Each call uses a fresh token.
	enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)
	if next := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10); len(next) != 1 || next[0].ClaimToken == token {
		t.Errorf("next claim = %+v, want one row with a new token", next)
	}
}

// TestClaimRespectsSubscriberLimitAndOrder checks that a claim takes only its
// subscriber, no more than the limit, oldest due first.
func TestClaimRespectsSubscriberLimitAndOrder(t *testing.T) {
	t.Parallel()
	_, api, clock := outboxSetup(t)
	early := enqueueEvents(t, api, spaceA, clock, 2, model.SubscriberInternal, model.SubscriberAI)
	clock.at = outboxT0.Add(time.Second)
	late := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)
	clock.at = outboxT0.Add(time.Minute)

	rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 2)
	if got := seqsOf(rows); len(got) != 2 || got[0] != early[0].Seq || got[1] != early[1].Seq {
		t.Errorf("claimed %v, want the two oldest %d, %d", got, early[0].Seq, early[1].Seq)
	}
	rest := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10)
	if got := seqsOf(rest); len(got) != 1 || got[0] != late[0].Seq {
		t.Errorf("claimed %v, want only %d", got, late[0].Seq)
	}
	if ai := mustClaim(t, api, spaceA, clock, model.SubscriberAI, 10); len(ai) != 2 {
		t.Errorf("ai claim = %d rows, want 2", len(ai))
	}
}

// TestMarkSentFixesDeliveredAt guards criterion 5.
func TestMarkSentFixesDeliveredAt(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	row := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]
	clock.at = outboxT0.Add(7*time.Second + 321*time.Microsecond)
	if err := markSent(api, spaceA, clock, row); err != nil {
		t.Fatalf("MarkOutboxSent: %v", err)
	}
	got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal)
	if got.Status != "sent" || !got.DeliveredAt.Valid || !got.DeliveredAt.Time.Equal(clock.at) {
		t.Errorf("row = %+v, want sent and delivered_at = %v", got, clock.at)
	}
	if got.ClaimToken.Valid || got.ClaimedUntil.Valid || got.Attempts != 0 {
		t.Errorf("a sent row keeps its claim or changed its attempts: %+v", got)
	}
}

// TestMarkSentTwiceIsAlreadyDone guards the second half of criterion 5.
func TestMarkSentTwiceIsAlreadyDone(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	row := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]
	if err := markSent(api, spaceA, clock, row); err != nil {
		t.Fatalf("MarkOutboxSent: %v", err)
	}
	first := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal)
	clock.at = clock.at.Add(time.Hour)
	requireCode(t, markSent(api, spaceA, clock, row), model.CodeAlreadyDone)
	requireCode(t, markFailed(api, spaceA, clock, row, "late"), model.CodeAlreadyDone)
	if after := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); after != first {
		t.Errorf("the row changed: %+v -> %+v", first, after)
	}
}

// TestMarkWithWrongTokenIsRuleViolation checks that only the holder of the
// current claim can mark the row.
func TestMarkWithWrongTokenIsRuleViolation(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	row := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]
	before := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal)

	forged := row
	forged.ClaimToken = uuid.NewString()
	requireCode(t, markSent(api, spaceA, clock, forged), model.CodeRuleViolation)
	requireCode(t, markFailed(api, spaceA, clock, forged, "boom"), model.CodeRuleViolation)
	unclaimed := row
	unclaimed.ClaimToken = ""
	requireCode(t, markSent(api, spaceA, clock, unclaimed), model.CodeInvalidInput)
	malformed := row
	malformed.ClaimToken = "x' OR '1'='1"
	requireCode(t, markSent(api, spaceA, clock, malformed), model.CodeInvalidInput)
	invalid := row
	invalid.Subscriber = "pigeon"
	requireCode(t, markSent(api, spaceA, clock, invalid), model.CodeInvalidInput)
	if after := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); after != before {
		t.Errorf("the row changed: %+v -> %+v", before, after)
	}
	if err := markSent(api, spaceA, clock, row); err != nil {
		t.Errorf("the real holder is refused: %v", err)
	}
}

// TestMarkUnknownRowIsNotFound checks the answer for a row that does not exist.
func TestMarkUnknownRowIsNotFound(t *testing.T) {
	t.Parallel()
	_, api, clock := outboxSetup(t)
	ghost := model.OutboxRow{EventSeq: 424242, Subscriber: model.SubscriberInternal, ClaimToken: uuid.NewString()}
	requireCode(t, markSent(api, spaceA, clock, ghost), model.CodeNotFound)
	requireCode(t, markFailed(api, spaceA, clock, ghost, "boom"), model.CodeNotFound)
}

// TestMarkSentOnDeadRowIsAlreadyDone checks that a dead row is final.
func TestMarkSentOnDeadRowIsAlreadyDone(t *testing.T) {
	t.Parallel()
	_, api, clock := outboxSetup(t)
	enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)
	var last model.OutboxRow
	for i := 0; i < model.OutboxMaxAttempts; i++ {
		clock.at = clock.at.Add(2 * time.Hour)
		last = mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]
		if err := markFailed(api, spaceA, clock, last, "boom"); err != nil {
			t.Fatalf("MarkOutboxFailed %d: %v", i+1, err)
		}
	}
	requireCode(t, markSent(api, spaceA, clock, last), model.CodeAlreadyDone)
}

// TestMarkFailedIncrementsAttemptsAndSpacesRetries guards criterion 6, first
// half: attempts 1 to 4 stay failed and the wait follows the table.
func TestMarkFailedIncrementsAttemptsAndSpacesRetries(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	for attempt, wait := range []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour} {
		row := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)
		if len(row) != 1 {
			t.Fatalf("attempt %d: claimed %d rows, want 1", attempt+1, len(row))
		}
		clock.at = clock.at.Add(time.Second)
		cause := "boom " + string(rune('1'+attempt))
		if err := markFailed(api, spaceA, clock, row[0], cause); err != nil {
			t.Fatalf("MarkOutboxFailed: %v", err)
		}
		got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal)
		if got.Status != "failed" || got.Attempts != attempt+1 || got.LastError.String != cause {
			t.Errorf("after failure %d: %+v, want failed, attempts %d, error %q", attempt+1, got, attempt+1, cause)
		}
		if want := clock.at.Add(wait); !got.NextAttemptAt.Equal(want) {
			t.Errorf("after failure %d: next_attempt_at = %v, want %v", attempt+1, got.NextAttemptAt, want)
		}
		if got.ClaimToken.Valid || got.ClaimedUntil.Valid {
			t.Errorf("after failure %d: the claim was kept", attempt+1)
		}
		// Not claimable until the wait has passed.
		clock.at = got.NextAttemptAt.Add(-time.Microsecond)
		if early := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1); len(early) != 0 {
			t.Fatalf("failure %d: the row was claimed before its wait ended", attempt+1)
		}
		clock.at = got.NextAttemptAt
	}
}

// TestMarkFailedCause checks the cause: blank is rejected, long is cut to 1000
// characters and bytes the database cannot store are cleaned.
func TestMarkFailedCause(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	row := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]
	for _, cause := range []string{"", " ", "\n\t "} {
		requireCode(t, markFailed(api, spaceA, clock, row, cause), model.CodeInvalidInput)
	}
	if got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); got.Status != "pending" {
		t.Fatalf("a rejected cause changed the row: %+v", got)
	}
	if err := markFailed(api, spaceA, clock, row, strings.Repeat("é", 1500)); err != nil {
		t.Fatalf("long cause: %v", err)
	}
	if got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); len([]rune(got.LastError.String)) != 1000 {
		t.Errorf("stored cause has %d characters, want 1000", len([]rune(got.LastError.String)))
	}
	clock.at = clock.at.Add(time.Hour)
	row = mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]
	if err := markFailed(api, spaceA, clock, row, "bad\x00 bytes \xff here"); err != nil {
		t.Fatalf("cause with NUL and invalid UTF-8: %v", err)
	}
	if got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); !strings.Contains(got.LastError.String, "bad") || strings.ContainsRune(got.LastError.String, 0) {
		t.Errorf("stored cause = %q", got.LastError.String)
	}
}

// TestFifthFailureMakesRowDead guards criterion 6, second half: the limit is
// exact, and a dead row is never claimed again.
func TestFifthFailureMakesRowDead(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	for attempt := 1; attempt <= model.OutboxMaxAttempts; attempt++ {
		clock.at = clock.at.Add(2 * time.Hour)
		row := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)
		if len(row) != 1 {
			t.Fatalf("attempt %d: claimed %d rows, want 1", attempt, len(row))
		}
		if err := markFailed(api, spaceA, clock, row[0], "boom"); err != nil {
			t.Fatalf("MarkOutboxFailed %d: %v", attempt, err)
		}
		got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal)
		want := "failed"
		if attempt == model.OutboxMaxAttempts {
			want = "dead"
		}
		if got.Status != want || got.Attempts != attempt {
			t.Errorf("after failure %d: %s with %d attempts, want %s", attempt, got.Status, got.Attempts, want)
		}
	}
	clock.at = clock.at.Add(24 * 365 * time.Hour)
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10); len(rows) != 0 {
		t.Errorf("a dead row was claimed after a year: %+v", rows)
	}
}

// TestExpiredClaimCountsAsFailedAttempt guards the lease: a claim that outlives
// five minutes is a failed attempt and the old holder loses the row.
func TestExpiredClaimCountsAsFailedAttempt(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	holder := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]

	// One microsecond before the lease ends the claim still holds.
	clock.at = outboxT0.Add(model.OutboxLease - time.Microsecond)
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1); len(rows) != 0 {
		t.Fatalf("claimed %d rows inside the lease", len(rows))
	}
	if got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); got.Attempts != 0 || !got.ClaimToken.Valid {
		t.Fatalf("the lease was cut short: %+v", got)
	}

	clock.at = outboxT0.Add(model.OutboxLease + time.Second)
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1); len(rows) != 0 {
		t.Fatalf("the row was claimable before the retry wait: %+v", rows)
	}
	got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal)
	if got.Status != "failed" || got.Attempts != 1 || got.LastError.String != "claim expired" || got.ClaimToken.Valid {
		t.Errorf("after the expiry: %+v, want failed, 1 attempt, claim expired, no claim", got)
	}
	if want := clock.at.Add(30 * time.Second); !got.NextAttemptAt.Equal(want) {
		t.Errorf("next_attempt_at = %v, want %v", got.NextAttemptAt, want)
	}
	requireCode(t, markSent(api, spaceA, clock, holder), model.CodeRuleViolation)
	requireCode(t, markFailed(api, spaceA, clock, holder, "late"), model.CodeRuleViolation)

	clock.at = clock.at.Add(30 * time.Second)
	rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)
	if len(rows) != 1 || rows[0].Attempts != 1 || rows[0].ClaimToken == holder.ClaimToken {
		t.Fatalf("reclaim = %+v, want the row with 1 attempt and a new token", rows)
	}
	if err := markSent(api, spaceA, clock, rows[0]); err != nil {
		t.Errorf("the new holder is refused: %v", err)
	}
}

// TestExpiredClaimsReachDead checks that a poisonous message, whose consumer
// always dies, ends up dead after five expired claims.
func TestExpiredClaimsReachDead(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	for attempt := 1; attempt <= model.OutboxMaxAttempts; attempt++ {
		clock.at = clock.at.Add(2 * time.Hour)
		if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1); len(rows) != 1 {
			t.Fatalf("attempt %d: claimed %d rows, want 1", attempt, len(rows))
		}
		clock.at = clock.at.Add(model.OutboxLease + time.Second)
		mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)
	}
	got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal)
	if got.Status != "dead" || got.Attempts != model.OutboxMaxAttempts || got.LastError.String != "claim expired" || got.ClaimToken.Valid {
		t.Errorf("row = %+v, want dead after 5 attempts with no claim", got)
	}
	clock.at = clock.at.Add(24 * time.Hour)
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1); len(rows) != 0 {
		t.Errorf("a dead row was claimed: %+v", rows)
	}
}

// TestClaimSkipsRowsNotYetDue guards criterion 7 to the microsecond.
func TestClaimSkipsRowsNotYetDue(t *testing.T) {
	t.Parallel()
	_, api, clock := outboxSetup(t)
	clock.at = outboxT0.Add(time.Microsecond)
	enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)
	clock.at = outboxT0
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10); len(rows) != 0 {
		t.Errorf("a row one microsecond in the future was claimed: %+v", rows)
	}
	clock.at = outboxT0.Add(time.Microsecond)
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10); len(rows) != 1 {
		t.Errorf("a row due exactly now was not claimed")
	}

	// The clock is cut to microseconds, so nanoseconds neither make a row due
	// early nor hide a row that is due.
	clock.at = outboxT0.Add(time.Hour)
	enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)
	clock.at = outboxT0.Add(time.Hour - time.Nanosecond)
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10); len(rows) != 0 {
		t.Errorf("a row was claimed before its next_attempt_at: %+v", rows)
	}
	clock.at = outboxT0.Add(time.Hour + 999*time.Nanosecond)
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10); len(rows) != 1 {
		t.Errorf("a row due within the same microsecond was not claimed")
	}
}

// TestClaimIgnoresOtherSpaces guards criterion 8.
func TestClaimIgnoresOtherSpaces(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	events := enqueueEvents(t, api, spaceA, clock, 3, model.SubscriberInternal)
	if rows := mustClaim(t, api, spaceB, clock, model.SubscriberInternal, 10); len(rows) != 0 {
		t.Errorf("space B claimed %d rows of space A", len(rows))
	}
	for _, event := range events {
		if got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); got.ClaimToken.Valid || got.Status != "pending" {
			t.Errorf("a row of space A was touched by space B: %+v", got)
		}
	}
	if rows := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 10); len(rows) != 3 {
		t.Errorf("space A claimed %d rows, want 3", len(rows))
	}
}

// TestClaimExpiryStaysInOwnSpace checks that the expiry step, which runs before
// each claim, only fails the claims of the space it runs in.
func TestClaimExpiryStaysInOwnSpace(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)
	clock.at = clock.at.Add(model.OutboxLease + time.Hour)
	mustClaim(t, api, spaceB, clock, model.SubscriberInternal, 1)
	if got := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); got.Attempts != 0 || !got.ClaimToken.Valid {
		t.Errorf("space B expired a claim of space A: %+v", got)
	}
}

// TestMarkCannotTouchOtherSpace guards criterion 8 for marks: space B holding
// the valid token of space A gets not_found and the row stays as it was.
func TestMarkCannotTouchOtherSpace(t *testing.T) {
	t.Parallel()
	admin, api, clock := outboxSetup(t)
	event := enqueueEvents(t, api, spaceA, clock, 1, model.SubscriberInternal)[0]
	row := mustClaim(t, api, spaceA, clock, model.SubscriberInternal, 1)[0]
	before := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal)
	requireCode(t, markSent(api, spaceB, clock, row), model.CodeNotFound)
	requireCode(t, markFailed(api, spaceB, clock, row, "boom"), model.CodeNotFound)
	if after := outboxRowOf(t, admin, event.Seq, model.SubscriberInternal); after != before {
		t.Errorf("the row changed: %+v -> %+v", before, after)
	}
}

// TestClaimValidatesInput covers the arguments of the claim.
func TestClaimValidatesInput(t *testing.T) {
	t.Parallel()
	_, api, clock := outboxSetup(t)
	ctx := context.Background()
	for _, limit := range []int{0, -1, 1001} {
		_, err := state.ClaimOutbox(ctx, api, clock, spaceA, model.SubscriberInternal, limit)
		requireCode(t, err, model.CodeInvalidInput)
	}
	for _, s := range []model.Subscriber{"", "pigeon", "Internal"} {
		_, err := state.ClaimOutbox(ctx, api, clock, spaceA, s, 1)
		requireCode(t, err, model.CodeInvalidInput)
	}
	for _, space := range []string{"", "not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		_, err := state.ClaimOutbox(ctx, api, clock, space, model.SubscriberInternal, 1)
		requireCode(t, err, model.CodeInvalidInput)
	}
	if rows, err := state.ClaimOutbox(ctx, api, clock, spaceA, model.SubscriberInternal, 1000); err != nil || len(rows) != 0 {
		t.Errorf("limit 1000 on an empty queue = %v, %v, want no rows and no error", rows, err)
	}
	tx, err := api.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	_, err = state.ClaimOutboxTx(ctx, tx, clock, model.SubscriberInternal, 1)
	requireCode(t, err, model.CodeRuleViolation)
}
