package state_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// testEvent returns a valid system event whose payload carries tag.
func testEvent(tag string) model.NewEvent {
	return model.NewEvent{
		Type:    "kernel.test.recorded",
		Version: 1,
		Payload: json.RawMessage(`{"tag":"` + tag + `"}`),
		Actor:   system,
	}
}

// eventSetup returns the administrator and API pools with spaces A and B seeded.
func eventSetup(t *testing.T) (admin, api *sql.DB) {
	t.Helper()
	admin = dbtest.Database(t)
	api = dbtest.APIPool(t, admin)
	seedSpaces(t, admin)
	return admin, api
}

// mustRecord records an event in spaceID and fails the test on error.
func mustRecord(t *testing.T, api *sql.DB, spaceID string, in model.NewEvent) model.Event {
	t.Helper()
	got, err := state.RecordEvent(context.Background(), api, spaceID, in)
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	return got
}

// beginInSpace opens a transaction on api whose current space is spaceID. The
// caller commits or rolls back.
func beginInSpace(t *testing.T, api *sql.DB, spaceID string) *sql.Tx {
	t.Helper()
	tx, err := api.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config($1, $2, true)`, state.SpaceSetting, spaceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("set space: %v", err)
	}
	return tx
}

// readSeqs reads the seq values visible in spaceID from fromSeq.
func readSeqs(t *testing.T, api *sql.DB, spaceID string, fromSeq int64) []int64 {
	t.Helper()
	events, err := state.ReadEvents(context.Background(), api, spaceID, fromSeq, 1000)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	seqs := make([]int64, len(events))
	for i, e := range events {
		seqs[i] = e.Seq
	}
	return seqs
}

// TestRecordEventAssignsSeqAndV7ID guards criterion 1.
func TestRecordEventAssignsSeqAndV7ID(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)

	first := mustRecord(t, api, spaceA, testEvent("one"))
	second := mustRecord(t, api, spaceA, testEvent("two"))

	if first.Seq < 1 || second.Seq <= first.Seq {
		t.Errorf("seq = %d then %d, want increasing from 1", first.Seq, second.Seq)
	}
	parsed, err := uuid.Parse(first.ID)
	if err != nil || parsed.Version() != 7 {
		t.Errorf("id = %q, want a UUID v7", first.ID)
	}
	if first.SpaceID != spaceA || first.Type != "kernel.test.recorded" || first.Version != 1 {
		t.Errorf("event = %+v, want space A, type kernel.test.recorded, version 1", first)
	}
	if first.Actor.Kind != model.ActorSystem || first.OccurredAt.IsZero() {
		t.Errorf("actor = %+v, occurred_at = %v, want system and a timestamp", first.Actor, first.OccurredAt)
	}
	var payload map[string]string
	if err := json.Unmarshal(first.Payload, &payload); err != nil || payload["tag"] != "one" {
		t.Errorf("payload = %s (%v), want {\"tag\":\"one\"}", first.Payload, err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.events WHERE id = $1 AND seq = $2`, first.ID, first.Seq); got != 1 {
		t.Errorf("stored rows for the first event = %d, want 1", got)
	}
}

// TestRecordEventKeepsOptionalFields checks actor, subject, request id and an
// explicit occurrence time.
func TestRecordEventKeepsOptionalFields(t *testing.T) {
	t.Parallel()
	_, api := eventSetup(t)
	participant := mustParticipant(t, api, spaceA, newPerson("", "Actor"))
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	in := testEvent("full")
	in.Actor = model.Actor{Kind: model.ActorPerson, ParticipantID: participant}
	in.SubjectRef = "participant:" + participant
	in.RequestID = "req-123"
	in.OccurredAt = at
	recorded := mustRecord(t, api, spaceA, in)

	events, err := state.ReadEvents(context.Background(), api, spaceA, recorded.Seq, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("ReadEvents = %v, %v, want one event", events, err)
	}
	got := events[0]
	if got.Actor != in.Actor || got.SubjectRef != in.SubjectRef || got.RequestID != "req-123" || !got.OccurredAt.Equal(at) {
		t.Errorf("read back %+v, want the recorded fields", got)
	}
}

// TestSeqStrictlyIncreasesAcrossTransactions guards criterion 2, first half.
func TestSeqStrictlyIncreasesAcrossTransactions(t *testing.T) {
	t.Parallel()
	_, api := eventSetup(t)
	first := mustRecord(t, api, spaceA, testEvent("a"))
	second := mustRecord(t, api, spaceB, testEvent("b"))
	if second.Seq <= first.Seq {
		t.Errorf("seq = %d then %d, want strictly increasing across spaces", first.Seq, second.Seq)
	}
}

// TestRollbackLeavesGapNeverDuplicate guards criterion 2, second half.
func TestRollbackLeavesGapNeverDuplicate(t *testing.T) {
	t.Parallel()
	_, api := eventSetup(t)
	first := mustRecord(t, api, spaceA, testEvent("kept"))

	tx := beginInSpace(t, api, spaceA)
	lost, err := state.AppendEvent(context.Background(), tx, testEvent("lost"))
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	next := mustRecord(t, api, spaceA, testEvent("next"))

	if lost.Seq == next.Seq || lost.Seq <= first.Seq {
		t.Errorf("rolled back seq %d, next %d: want distinct and ordered", lost.Seq, next.Seq)
	}
	if next.Seq < first.Seq+2 {
		t.Errorf("next seq = %d, want a gap of at least one after %d", next.Seq, first.Seq)
	}
}

// TestConcurrentAppendsNeverRepeatSeq runs many writers, some of which roll
// back, and checks that no seq is ever handed out twice.
func TestConcurrentAppendsNeverRepeatSeq(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)
	const workers, perWorker = 20, 10

	type outcome struct {
		seqs      []int64
		committed int
		err       error
	}
	results := make([]outcome, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := &results[w]
			for i := 0; i < perWorker; i++ {
				tx, err := api.BeginTx(context.Background(), nil)
				if err != nil {
					out.err = err
					return
				}
				if _, err := tx.Exec(`SELECT set_config($1, $2, true)`, state.SpaceSetting, spaceA); err != nil {
					_ = tx.Rollback()
					out.err = err
					return
				}
				got, err := state.AppendEvent(context.Background(), tx, testEvent("c"))
				if err != nil {
					_ = tx.Rollback()
					out.err = err
					return
				}
				out.seqs = append(out.seqs, got.Seq)
				if (w+i)%3 == 0 {
					_ = tx.Rollback()
					continue
				}
				if err := tx.Commit(); err != nil {
					out.err = err
					return
				}
				out.committed++
			}
		}()
	}
	wg.Wait()

	seen := map[int64]bool{}
	committed := 0
	for w, out := range results {
		if out.err != nil {
			t.Fatalf("worker %d: %v", w, out.err)
		}
		for i, seq := range out.seqs {
			if seen[seq] {
				t.Errorf("seq %d was handed out twice", seq)
			}
			seen[seq] = true
			if i > 0 && seq <= out.seqs[i-1] {
				t.Errorf("worker %d: seq %d after %d, want increasing", w, seq, out.seqs[i-1])
			}
		}
		committed += out.committed
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != committed {
		t.Errorf("stored events = %d, want %d committed", got, committed)
	}
	if got := count(t, admin, `SELECT count(DISTINCT seq) FROM kernel.events`); got != committed {
		t.Errorf("distinct seq stored = %d, want %d", got, committed)
	}
}

// TestSeqIsAllocationOrderNotCommitOrder documents the guarantee of ADR-0028: a
// lower seq can become visible after a higher one, so cursor consumers must
// overlap their reads and deduplicate by id.
func TestSeqIsAllocationOrderNotCommitOrder(t *testing.T) {
	t.Parallel()
	_, api := eventSetup(t)
	ctx := context.Background()

	tx1 := beginInSpace(t, api, spaceA)
	slow, err := state.AppendEvent(ctx, tx1, testEvent("slow"))
	if err != nil {
		t.Fatalf("append in tx1: %v", err)
	}
	fast := mustRecord(t, api, spaceA, testEvent("fast"))
	if fast.Seq <= slow.Seq {
		t.Fatalf("seq of the later insert = %d, want above %d", fast.Seq, slow.Seq)
	}

	if got := readSeqs(t, api, spaceA, 0); len(got) != 1 || got[0] != fast.Seq {
		t.Errorf("before tx1 commits a reader sees %v, want only [%d]", got, fast.Seq)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}
	got := readSeqs(t, api, spaceA, 0)
	if len(got) != 2 || got[0] != slow.Seq || got[1] != fast.Seq {
		t.Errorf("after tx1 commits a reader sees %v, want [%d %d]", got, slow.Seq, fast.Seq)
	}
}

// TestRecordEventInvalidInputWritesNothing guards criterion 4 end to end.
func TestRecordEventInvalidInputWritesNothing(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)
	bad := testEvent("x")
	bad.Type = "NotAType"
	_, err := state.RecordEvent(context.Background(), api, spaceA, bad)
	requireCode(t, err, model.CodeInvalidInput)

	bad = testEvent("x")
	bad.Version = 0
	_, err = state.RecordEvent(context.Background(), api, spaceA, bad)
	requireCode(t, err, model.CodeInvalidInput)

	bad = testEvent("x")
	bad.Payload = json.RawMessage(`[1]`)
	_, err = state.RecordEvent(context.Background(), api, spaceA, bad)
	requireCode(t, err, model.CodeInvalidInput)

	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != 0 {
		t.Errorf("stored events = %d, want 0", got)
	}
}

// TestAppendEventInvalidInputKeepsTransactionUsable checks that validation runs
// before any statement, so the caller's transaction is not poisoned.
func TestAppendEventInvalidInputKeepsTransactionUsable(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)
	tx := beginInSpace(t, api, spaceA)
	bad := testEvent("x")
	bad.Version = -1
	_, err := state.AppendEvent(context.Background(), tx, bad)
	requireCode(t, err, model.CodeInvalidInput)
	if _, err := state.AppendEvent(context.Background(), tx, testEvent("ok")); err != nil {
		t.Fatalf("the transaction was poisoned by validation: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != 1 {
		t.Errorf("stored events = %d, want 1", got)
	}
}

// TestNewEventWithoutActorRejected guards criterion 6 end to end.
func TestNewEventWithoutActorRejected(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)
	in := testEvent("x")
	in.Actor = model.Actor{Kind: model.ActorPerson}
	_, err := state.RecordEvent(context.Background(), api, spaceA, in)
	requireCode(t, err, model.CodePermissionDenied)
	in.Actor = model.Actor{Kind: "robot"}
	_, err = state.RecordEvent(context.Background(), api, spaceA, in)
	requireCode(t, err, model.CodeInvalidInput)
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != 0 {
		t.Errorf("stored events = %d, want 0", got)
	}
}

// TestEventActorMustBelongToSpace checks that an actor of another space is a
// rule violation, not a silent write.
func TestEventActorMustBelongToSpace(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)
	foreign := mustParticipant(t, api, spaceB, newPerson("", "Foreign"))
	in := testEvent("x")
	in.Actor = model.Actor{Kind: model.ActorPerson, ParticipantID: foreign}
	_, err := state.RecordEvent(context.Background(), api, spaceA, in)
	requireCode(t, err, model.CodeRuleViolation)
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != 0 {
		t.Errorf("stored events = %d, want 0", got)
	}
}

// TestEventsIsolationBetweenSpaces guards criterion 7, read side.
func TestEventsIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	_, api := eventSetup(t)
	mustRecord(t, api, spaceA, testEvent("a"))

	events, err := state.ReadEvents(context.Background(), api, spaceB, 0, 100)
	if err != nil || len(events) != 0 {
		t.Errorf("space B reads %v (%v), want no events", events, err)
	}
	visible(t, api, spaceB, func(tx *sql.Tx) {
		if got := count(t, tx, `SELECT count(*) FROM kernel.events`); got != 0 {
			t.Errorf("space B counts %d events, want 0", got)
		}
	})
}

// TestEventsInsertForeignSpaceRejected guards criterion 7, write side: even a
// literal statement cannot name another space.
func TestEventsInsertForeignSpaceRejected(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)
	err := state.InSpace(context.Background(), api, spaceB, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kernel.events (id, space_id, type, version, payload, actor_kind)
			VALUES ($1, $2, 'kernel.test.recorded', 1, '{}', 'system')`, mustV7(t), spaceA)
		return err
	})
	requireSQLState(t, err, "42501")
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != 0 {
		t.Errorf("stored events = %d, want 0", got)
	}
}

// mustV7 returns a new UUID v7 as text.
func mustV7(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id.String()
}

// TestReadEventsFromSeqAscendingCurrentSpaceOnly guards criterion 8.
func TestReadEventsFromSeqAscendingCurrentSpaceOnly(t *testing.T) {
	t.Parallel()
	_, api := eventSetup(t)
	var inA []model.Event
	for i := 0; i < 4; i++ {
		inA = append(inA, mustRecord(t, api, spaceA, testEvent("a")))
		mustRecord(t, api, spaceB, testEvent("b"))
	}
	ctx := context.Background()

	all, err := state.ReadEvents(ctx, api, spaceA, 0, 100)
	if err != nil || len(all) != 4 {
		t.Fatalf("ReadEvents all = %d events (%v), want 4", len(all), err)
	}
	for i, e := range all {
		if e.Seq != inA[i].Seq || e.SpaceID != spaceA {
			t.Errorf("event %d = seq %d space %s, want seq %d in A", i, e.Seq, e.SpaceID, inA[i].Seq)
		}
	}

	// The cursor is inclusive: reading from the seq of the third event returns it.
	from, err := state.ReadEvents(ctx, api, spaceA, inA[2].Seq, 100)
	if err != nil || len(from) != 2 || from[0].Seq != inA[2].Seq || from[1].Seq != inA[3].Seq {
		t.Errorf("ReadEvents from %d = %v (%v), want the last two", inA[2].Seq, from, err)
	}
	limited, err := state.ReadEvents(ctx, api, spaceA, 0, 3)
	if err != nil || len(limited) != 3 || limited[2].Seq != inA[2].Seq {
		t.Errorf("ReadEvents limit 3 = %v (%v), want the first three", limited, err)
	}
	past, err := state.ReadEvents(ctx, api, spaceA, inA[3].Seq+1, 100)
	if err != nil || len(past) != 0 {
		t.Errorf("ReadEvents past the end = %v (%v), want none", past, err)
	}
}

// TestReadEventsRejectsBadArguments checks the cursor and limit bounds.
func TestReadEventsRejectsBadArguments(t *testing.T) {
	t.Parallel()
	_, api := eventSetup(t)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"negative cursor": func() error { _, err := state.ReadEvents(ctx, api, spaceA, -1, 10); return err },
		"zero limit":      func() error { _, err := state.ReadEvents(ctx, api, spaceA, 0, 0); return err },
		"huge limit":      func() error { _, err := state.ReadEvents(ctx, api, spaceA, 0, 1001); return err },
		"bad space":       func() error { _, err := state.ReadEvents(ctx, api, "nope", 0, 10); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: want invalid_input, got nil", name)
		} else {
			requireCode(t, err, model.CodeInvalidInput)
		}
	}
}

// TestAppendEventSharesCallerTransaction checks that the helper writes in the
// caller's transaction, so a rollback removes the event with the other writes.
func TestAppendEventSharesCallerTransaction(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)
	ctx := context.Background()

	tx := beginInSpace(t, api, spaceA)
	if _, err := tx.Exec(`INSERT INTO kernel.participants (id, space_id, kind, display_name)
		VALUES ($1, kernel.current_space_id(), 'person', 'Same tx')`, mustV7(t)); err != nil {
		t.Fatalf("foreign insert: %v", err)
	}
	if _, err := state.AppendEvent(ctx, tx, testEvent("same-tx")); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != 0 {
		t.Errorf("events after rollback = %d, want 0", got)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.participants`); got != 0 {
		t.Errorf("participants after rollback = %d, want 0", got)
	}

	tx = beginInSpace(t, api, spaceA)
	if _, err := state.AppendEvent(ctx, tx, testEvent("kept")); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != 1 {
		t.Errorf("events after commit = %d, want 1", got)
	}
}

// TestAppendEventWithoutSpaceFails checks that the space comes from the
// transaction and that a transaction without one cannot write.
func TestAppendEventWithoutSpaceFails(t *testing.T) {
	t.Parallel()
	admin, api := eventSetup(t)
	tx, err := api.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	_, err = state.AppendEvent(context.Background(), tx, testEvent("x"))
	requireCode(t, err, model.CodeRuleViolation)
	var typed *model.Error
	if !errors.As(err, &typed) {
		t.Fatalf("want a typed error, got %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.events`); got != 0 {
		t.Errorf("stored events = %d, want 0", got)
	}
}

// TestRecordEventRejectsBadSpace checks the space id of the autonomous helper.
func TestRecordEventRejectsBadSpace(t *testing.T) {
	t.Parallel()
	_, api := eventSetup(t)
	_, err := state.RecordEvent(context.Background(), api, "", testEvent("x"))
	requireCode(t, err, model.CodeInvalidInput)
}
