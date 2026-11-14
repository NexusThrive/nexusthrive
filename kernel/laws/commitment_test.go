package laws_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// commitmentColumns are the columns an insert of a commitment can name, in the
// order of commitmentInsert.
var commitmentColumns = []string{
	"id", "space_id", "kind", "debtor_participant_id", "creditor_participant_id", "resource_id",
	"amount", "due_at", "status", "created_by_participant_id",
}

// commitmentInsert returns the insert of a commitment of f in space, with the
// columns in omit left out and the values in override replaced.
func commitmentInsert(t *testing.T, space string, f fixtures, omit []string, override map[string]any) stmt {
	t.Helper()
	values := map[string]any{
		"id": newID(t), "space_id": space, "kind": "test.commitment",
		"debtor_participant_id": f.debtor, "creditor_participant_id": f.creditor, "resource_id": f.resource,
		"amount": "10.00", "due_at": time.Now().Add(48 * time.Hour), "status": "open",
		"created_by_participant_id": f.creditor,
	}
	for key, value := range override {
		values[key] = value
	}
	var names, marks []string
	var args []any
	for _, column := range commitmentColumns {
		skipped := false
		for _, o := range omit {
			skipped = skipped || o == column
		}
		if skipped {
			continue
		}
		args = append(args, values[column])
		names = append(names, column)
		marks = append(marks, "$"+strconv.Itoa(len(args)))
	}
	// Columns outside the list of the grant of the API role are named too, so
	// the statement can show the refusal.
	for column, value := range override {
		known := false
		for _, c := range commitmentColumns {
			known = known || c == column
		}
		if !known {
			args = append(args, value)
			names = append(names, column)
			marks = append(marks, "$"+strconv.Itoa(len(args)))
		}
	}
	return stmt{`INSERT INTO kernel.commitments (` + strings.Join(names, ", ") + `) VALUES (` + strings.Join(marks, ", ") + `)`, args}
}

// TestLawCommitmentRequiredAndBoundedFields guards the law of commitment: a
// commitment without a due date or a status is refused, and so is one that
// claims to have fulfilled more than its amount, whether the attempt comes
// through the grants of the API role, through the guards of the table or, with
// the triggers switched off, through the CHECK of the table itself. The
// positive control is a commitment with every field, which commits with its
// timer.
func TestLawCommitmentRequiredAndBoundedFields(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)

	insertAsAPI := func(omit []string, override map[string]any) error {
		return runIn(t, w.api, w.spaceA, commitmentInsert(t, w.spaceA, f, omit, override))
	}
	before := w.counts()
	if err := insertAsAPI([]string{"due_at"}, nil); pgCode(err) != "23502" {
		t.Errorf("no due_at: error %v, want SQLSTATE 23502 (not null)", err)
	}
	if err := insertAsAPI([]string{"status"}, nil); pgCode(err) != "23502" {
		t.Errorf("no status: error %v, want SQLSTATE 23502 (not null)", err)
	}
	// fulfilled_amount is not a column the API role may name in an insert.
	if err := insertAsAPI(nil, map[string]any{"fulfilled_amount": "11.00"}); pgCode(err) != "42501" {
		t.Errorf("fulfilled_amount above the amount through the API: error %v, want SQLSTATE 42501", err)
	}

	// The administrator owns the table: the guard of inserts still refuses it.
	adminInsert := commitmentInsert(t, w.spaceA, f, nil, map[string]any{"fulfilled_amount": "11.00"})
	if err := runIn(t, w.admin, "", adminInsert); pgCode(err) != "23001" {
		t.Errorf("fulfilled_amount above the amount through the guard: error %v, want SQLSTATE 23001", err)
	}

	// With the triggers switched off, as in a restore, the CHECK still holds.
	tx, err := w.admin.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("switch the triggers off: %v", err)
	}
	_, err = tx.Exec(adminInsert.query, adminInsert.args...)
	if pgCode(err) != "23514" || pgConstraint(err) != "commitments_fulfilled_within_amount" {
		t.Errorf("fulfilled_amount above the amount with no triggers: error %v, want the CHECK commitments_fulfilled_within_amount", err)
	}
	tx.Rollback()

	if changed := diff(before, w.counts()); len(changed) != 0 {
		t.Errorf("the refused commitments left %v", changed)
	}
	if err := insertAsAPI(nil, nil); err != nil {
		t.Fatalf("a complete commitment failed: %v", err)
	}
	if changed := diff(before, w.counts()); changed["commitments"] != 1 || changed["timers"] != 1 || len(changed) != 2 {
		t.Errorf("a complete commitment changed %v, want one commitment and its timer", changed)
	}
}

// TestLawCommitmentCancelWithNullReasonIsNoOpInSQL fixes the behaviour of
// follow-up 58: in SQL, writing a null cancellation reason on an open
// commitment changes nothing and succeeds, and a blank reason is refused by
// the CHECK. The refusal of a missing reason lives in the Go layer
// (invalid_input), which the last assertion keeps in view.
func TestLawCommitmentCancelWithNullReasonIsNoOpInSQL(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	created, err := state.CreateCommitment(context.Background(), w.api, model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}, w.spaceA,
		model.NewCommitment{Kind: "test.commitment", DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor,
			ResourceID: f.resource, Amount: money(t, "10.00"), DueAt: time.Now().Add(48 * time.Hour)})
	if err != nil {
		t.Fatalf("CreateCommitment: %v", err)
	}

	before := checksum(t, w.admin, "commitments")
	if err := runIn(t, w.api, w.spaceA, stmt{`UPDATE kernel.commitments SET canceled_reason = NULL WHERE id = $1`, []any{created.ID}}); err != nil {
		t.Errorf("a null reason in SQL failed: %v, want a silent no-op", err)
	}
	if after := checksum(t, w.admin, "commitments"); after != before {
		t.Errorf("a null reason changed the commitment: %s to %s", before, after)
	}
	err = runIn(t, w.api, w.spaceA, stmt{`UPDATE kernel.commitments SET canceled_reason = '  ' WHERE id = $1`, []any{created.ID}})
	if pgCode(err) != "23514" {
		t.Errorf("a blank reason: error %v, want SQLSTATE 23514", err)
	}
	if _, err := state.CancelCommitment(context.Background(), w.api, model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}, w.spaceA, created.ID, ""); err == nil {
		t.Errorf("Go accepted an empty reason")
	} else if code, _ := model.CodeOf(err); code != model.CodeInvalidInput {
		t.Errorf("an empty reason through Go: code %s, want invalid_input", code)
	}
}

// TestLawCommitmentCreditorMayFulfillWithOwnEntry fixes the behaviour of
// follow-up 56, accepted for milestone 1: the kernel asks that an entry
// fulfilling a commitment be positive, whole and in an account of the
// creditor, and nothing more. A creditor therefore fulfills its own
// commitment with a transfer between its own accounts, and with an entry
// recorded before the commitment existed. Tightening this is a decision for
// ADR-0033, not a change of this test.
func TestLawCommitmentCreditorMayFulfillWithOwnEntry(t *testing.T) {
	t.Parallel()
	w := newEngineWorld(t)
	f := w.ledger(w.spaceA)
	ctx := context.Background()
	bookkeeper := model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}

	// An entry that predates the commitment: a transfer between two accounts
	// of the creditor, with no part of the debtor in it.
	earlier, err := state.RecordExchange(ctx, w.api, bookkeeper, w.spaceA, model.NewExchange{
		Kind: "test.exchange", IdempotencyKey: "k-own-transfer", Entries: []model.NewEntry{
			{AccountID: f.to, ResourceID: f.resource, Amount: money(t, "10.00")},
			{AccountID: f.from, ResourceID: f.resource, Amount: money(t, "10.00").Neg()},
		}})
	if err != nil {
		t.Fatalf("RecordExchange: %v", err)
	}
	var entryID string
	if err := w.admin.QueryRow(`SELECT id::text FROM kernel.entries WHERE exchange_id = $1 AND amount > 0`, earlier.ID).Scan(&entryID); err != nil {
		t.Fatalf("read the entry: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	created, err := state.CreateCommitment(ctx, w.api, bookkeeper, w.spaceA,
		model.NewCommitment{Kind: "test.commitment", DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor,
			ResourceID: f.resource, Amount: money(t, "10.00"), DueAt: time.Now().Add(48 * time.Hour)})
	if err != nil {
		t.Fatalf("CreateCommitment: %v", err)
	}

	_, fulfilled, err := state.FulfillCommitment(ctx, w.api, bookkeeper, w.spaceA, created.ID, entryID)
	if err != nil {
		t.Fatalf("the creditor could not fulfill with its own earlier entry: %v", err)
	}
	if fulfilled.Status != model.CommitmentStatusFulfilled {
		t.Errorf("status = %s, want fulfilled", fulfilled.Status)
	}
}
