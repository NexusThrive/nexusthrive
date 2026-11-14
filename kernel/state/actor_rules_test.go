package state_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestAppendFunctionsAcceptPersonActor guards the relaxed actor rule: the
// Append variants accept any known actor kind that stands for a participant,
// because the permission is decided by the engine and not by the kind of the
// actor. A null actor, an unknown kind or a missing participant is still
// refused before any write.
func TestAppendFunctionsAcceptPersonActor(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()
	person := model.Actor{Kind: model.ActorPerson, ParticipantID: c.debtor}

	original := c.record(t, c.pair(t, "k-original", c.a1, c.a2, c.cop, "10.00"))
	open := c.create(t, "100.00")
	toCancel := c.create(t, "50.00")
	toMove := c.create(t, "40.00")
	entry := c.credit(t, "k-entry", "20.00")

	err := state.InSpace(ctx, c.api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		exchange, err := state.AppendExchange(ctx, tx, person, c.pair(t, "k-person", c.a1, c.a2, c.cop, "1.00"))
		if err != nil {
			t.Errorf("AppendExchange as a person: %v", err)
		} else if exchange.ActorParticipantID != c.debtor {
			t.Errorf("exchange actor = %s, want the person %s", exchange.ActorParticipantID, c.debtor)
		}
		if _, err := state.AppendReversal(ctx, tx, person, original.ID, "k-reversal"); err != nil {
			t.Errorf("AppendReversal as a person: %v", err)
		}
		created, err := state.AppendCommitment(ctx, tx, person, c.newCommitment(t, "5.00"))
		if err != nil {
			t.Errorf("AppendCommitment as a person: %v", err)
		} else if created.CreatedByParticipantID != c.debtor {
			t.Errorf("commitment creator = %s, want the person %s", created.CreatedByParticipantID, c.debtor)
		}
		if _, _, err := state.AppendFulfillment(ctx, tx, person, open.ID, entry); err != nil {
			t.Errorf("AppendFulfillment as a person: %v", err)
		}
		if _, err := state.AppendCancelCommitment(ctx, tx, person, toCancel.ID, "by a person"); err != nil {
			t.Errorf("AppendCancelCommitment as a person: %v", err)
		}
		if _, err := state.AppendRescheduleCommitment(ctx, tx, person, toMove.ID, time.Now().Add(72*time.Hour)); err != nil {
			t.Errorf("AppendRescheduleCommitment as a person: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}

	// A refused actor writes nothing and the transaction stays usable.
	refused := map[string]model.Actor{
		"zero actor":        {},
		"person no one":     {Kind: model.ActorPerson},
		"system no one":     {Kind: model.ActorSystem},
		"unknown kind":      {Kind: "robot", ParticipantID: c.debtor},
		"api key no one":    {Kind: model.ActorAPIKey},
		"agent without one": {Kind: model.ActorAgent},
	}
	for name, actor := range refused {
		tx := beginInSpace(t, c.api, spaceA)
		_, err := state.AppendExchange(ctx, tx, actor, c.pair(t, "k-refused", c.a1, c.a2, c.cop, "1.00"))
		requireCodeLabel(t, name+" exchange", err, model.CodePermissionDenied)
		_, err = state.AppendCommitment(ctx, tx, actor, c.newCommitment(t, "1.00"))
		requireCodeLabel(t, name+" commitment", err, model.CodePermissionDenied)
		_, err = state.AppendCancelCommitment(ctx, tx, actor, open.ID, "no")
		requireCodeLabel(t, name+" cancel", err, model.CodePermissionDenied)
		_, err = state.AppendRescheduleCommitment(ctx, tx, actor, toMove.ID, time.Now().Add(96*time.Hour))
		requireCodeLabel(t, name+" reschedule", err, model.CodePermissionDenied)
		if _, err := tx.Exec(`SELECT 1`); err != nil {
			t.Errorf("%s: the transaction must stay usable: %v", name, err)
		}
		tx.Rollback()
	}

	// A malformed participant is invalid_input and one of another space is a
	// rule violation decided by the database.
	tx := beginInSpace(t, c.api, spaceA)
	defer tx.Rollback()
	_, err = state.AppendExchange(ctx, tx, model.Actor{Kind: model.ActorPerson, ParticipantID: "nope"}, c.pair(t, "k-bad", c.a1, c.a2, c.cop, "1.00"))
	requireCodeLabel(t, "malformed participant", err, model.CodeInvalidInput)
}

// requireCodeLabel fails unless err is a typed error of the given code.
func requireCodeLabel(t *testing.T, label string, err error, want model.Code) {
	t.Helper()
	if got, typed := model.CodeOf(err); !typed || got != want {
		t.Fatalf("%s: error = %v, want code %s", label, err, want)
	}
}

// TestPoolWrappersStaySystemOnly guards that the wrappers that open their own
// transaction keep requiring the system actor: they are for tests and
// bootstrap, and production goes through the engine.
func TestPoolWrappersStaySystemOnly(t *testing.T) {
	t.Parallel()
	c := commitmentSetup(t)
	ctx := context.Background()
	person := model.Actor{Kind: model.ActorPerson, ParticipantID: c.debtor}
	original := c.record(t, c.pair(t, "k-original", c.a1, c.a2, c.cop, "10.00"))
	open := c.create(t, "100.00")
	entry := c.credit(t, "k-entry", "20.00")

	_, err := state.RecordExchange(ctx, c.api, person, spaceA, c.pair(t, "k-person", c.a1, c.a2, c.cop, "1.00"))
	requireCodeLabel(t, "RecordExchange", err, model.CodePermissionDenied)
	_, err = state.ReverseExchange(ctx, c.api, person, spaceA, original.ID, "k-rev")
	requireCodeLabel(t, "ReverseExchange", err, model.CodePermissionDenied)
	_, err = state.CreateCommitment(ctx, c.api, person, spaceA, c.newCommitment(t, "1.00"))
	requireCodeLabel(t, "CreateCommitment", err, model.CodePermissionDenied)
	_, _, err = state.FulfillCommitment(ctx, c.api, person, spaceA, open.ID, entry)
	requireCodeLabel(t, "FulfillCommitment", err, model.CodePermissionDenied)
	_, err = state.CancelCommitment(ctx, c.api, person, spaceA, open.ID, "no")
	requireCodeLabel(t, "CancelCommitment", err, model.CodePermissionDenied)
	_, err = state.RescheduleCommitment(ctx, c.api, person, spaceA, open.ID, time.Now().Add(72*time.Hour))
	requireCodeLabel(t, "RescheduleCommitment", err, model.CodePermissionDenied)

	if got := count(t, c.admin, `SELECT count(*) FROM kernel.exchanges`); got != 2 {
		t.Errorf("exchanges = %d, want 2 (the two set up first, none from the refused calls)", got)
	}
}

// TestCreateRoleCapabilityWriteOutsidePlatformIsPermissionDenied guards the
// mapping of the database restriction on kernel.capability.write (follow-up
// 50): a role or a grant that carries it in a space that is not a platform
// space is permission_denied, not an untyped error.
func TestCreateRoleCapabilityWriteOutsidePlatformIsPermissionDenied(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	ctx := context.Background()
	system := model.Actor{Kind: model.ActorSystem}

	_, err := state.CreateRole(ctx, api, system, spaceA, model.NewRole{
		Key: "capability_admin", Name: "Capability admin", Permissions: []string{model.PermissionCapabilityWrite}})
	requireCodeLabel(t, "CreateRole", err, model.CodePermissionDenied)

	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	roleID, err := state.CreateRole(ctx, api, system, spaceA, model.NewRole{Key: "plain", Name: "Plain", Permissions: []string{model.PermissionLedgerRead}})
	if err != nil {
		t.Fatalf("CreateRole plain: %v", err)
	}
	_, err = state.GrantRole(ctx, api, realClock, system, spaceA, model.NewGrant{
		ParticipantID: holder, RoleID: roleID, ExtraPermissions: []string{model.PermissionCapabilityWrite}})
	requireCodeLabel(t, "GrantRole extra permission", err, model.CodePermissionDenied)

	if got := count(t, admin, `SELECT count(*) FROM kernel.roles WHERE key = 'capability_admin'`); got != 0 {
		t.Errorf("roles with the key = %d, want 0", got)
	}
}

// TestTranslateCommitError guards the translation of the deferred checks that
// fail when a transaction commits: the known ones are rule_violation, and
// anything else, including a missing privilege, passes through untouched.
func TestTranslateCommitError(t *testing.T) {
	t.Parallel()
	for _, constraint := range []string{
		"exchanges_conservation", "exchanges_min_entries", "exchanges_reversal_mirror", "commitments_fulfilled_sum", "timers_coherent",
	} {
		cause := &pgconn.PgError{Code: "23000", ConstraintName: constraint}
		got := state.TranslateCommitError(errors.Join(errors.New("commit transaction"), cause))
		requireCodeLabel(t, constraint, got, model.CodeRuleViolation)
		if !errors.Is(got, cause) {
			t.Errorf("%s: the cause was lost", constraint)
		}
	}

	privilege := &pgconn.PgError{Code: "42501"}
	if got := state.TranslateCommitError(privilege); got != error(privilege) {
		t.Errorf("a privilege error was changed to %v", got)
	}
	if _, typed := model.CodeOf(state.TranslateCommitError(privilege)); typed {
		t.Error("a privilege error became typed")
	}
	other := &pgconn.PgError{Code: "23000", ConstraintName: "something_else"}
	if got := state.TranslateCommitError(other); got != error(other) {
		t.Errorf("an unknown constraint was changed to %v", got)
	}
	typed := model.NewError(model.CodeNotFound, "kept", nil)
	if got := state.TranslateCommitError(typed); got != error(typed) {
		t.Errorf("a typed error was changed to %v", got)
	}
	if got := state.TranslateCommitError(nil); got != nil {
		t.Errorf("nil became %v", got)
	}
}
