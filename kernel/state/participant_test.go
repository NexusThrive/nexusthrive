package state_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// newPerson builds a valid person linked to identityID ("" for none).
func newPerson(identityID, name string) state.NewParticipant {
	return state.NewParticipant{Kind: model.ParticipantKindPerson, IdentityID: identityID, DisplayName: name}
}

// mustParticipant creates a participant and returns its id.
func mustParticipant(t *testing.T, api *sql.DB, spaceID string, in state.NewParticipant) string {
	t.Helper()
	id, err := state.CreateParticipant(context.Background(), api, system, spaceID, in)
	if err != nil {
		t.Fatalf("CreateParticipant: %v", err)
	}
	return id
}

// visible runs fn as the API role inside spaceID.
func visible(t *testing.T, api *sql.DB, spaceID string, fn func(tx *sql.Tx)) {
	t.Helper()
	err := state.InSpace(context.Background(), api, spaceID, func(ctx context.Context, tx *sql.Tx) error {
		fn(tx)
		return nil
	})
	if err != nil {
		t.Fatalf("InSpace: %v", err)
	}
}

// requireSQLState fails unless err carries the given SQLSTATE.
func requireSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != want {
		t.Fatalf("error = %v, want SQLSTATE %s", err, want)
	}
}

// TestIdentityVisibleOnlyThroughParticipantInCurrentSpace guards criterion 3.
func TestIdentityVisibleOnlyThroughParticipantInCurrentSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	x := mustIdentity(t, api, "x")
	y := mustIdentity(t, api, "y")
	mustParticipant(t, api, spaceA, newPerson(x, "X in A"))
	mustParticipant(t, api, spaceB, newPerson(x, "X in B"))
	mustParticipant(t, api, spaceB, newPerson(y, "Y in B"))

	check := func(spaceID string, wantParticipants int, wantIdentities map[string]bool) {
		visible(t, api, spaceID, func(tx *sql.Tx) {
			if got := count(t, tx, `SELECT count(*) FROM kernel.participants`); got != wantParticipants {
				t.Errorf("space %s sees %d participants, want %d", spaceID, got, wantParticipants)
			}
			for id, want := range wantIdentities {
				got := count(t, tx, `SELECT count(*) FROM kernel.identities WHERE id = $1`, id) == 1
				if got != want {
					t.Errorf("space %s sees identity %s = %v, want %v", spaceID, id, got, want)
				}
			}
		})
	}
	check(spaceA, 1, map[string]bool{x: true, y: false})
	check(spaceB, 2, map[string]bool{x: true, y: true})
}

// TestCreateParticipantRejectsUnknownKind guards criterion 4.
func TestCreateParticipantRejectsUnknownKind(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	for _, kind := range []model.ParticipantKind{"", "robot"} {
		in := newPerson("", "Someone")
		in.Kind = kind
		_, err := state.CreateParticipant(context.Background(), api, system, spaceA, in)
		requireCode(t, err, model.CodeInvalidInput)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.participants`); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
}

// TestCreateParticipantWithoutIdentity guards criterion 5.
func TestCreateParticipantWithoutIdentity(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	id := mustParticipant(t, api, spaceA, newPerson("", "No account"))
	var identity sql.NullString
	var status, space string
	if err := admin.QueryRow(`SELECT identity_id::text, status, space_id::text FROM kernel.participants WHERE id = $1`, id).
		Scan(&identity, &status, &space); err != nil {
		t.Fatalf("read participant: %v", err)
	}
	if identity.Valid || status != "active" || space != spaceA {
		t.Errorf("participant = (%v, %s, %s), want (null, active, %s)", identity, status, space, spaceA)
	}
}

// TestCreateParticipantSecondForSameIdentityIsRuleViolation covers the
// partial unique index on (space_id, identity_id).
func TestCreateParticipantSecondForSameIdentityIsRuleViolation(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	x := mustIdentity(t, api, "x")
	mustParticipant(t, api, spaceA, newPerson(x, "First"))
	_, err := state.CreateParticipant(context.Background(), api, system, spaceA, newPerson(x, "Second"))
	requireCode(t, err, model.CodeRuleViolation)

	mustParticipant(t, api, spaceB, newPerson(x, "Other space"))
	mustParticipant(t, api, spaceA, newPerson("", "No account 1"))
	mustParticipant(t, api, spaceA, newPerson("", "No account 2"))
	if got := count(t, admin, `SELECT count(*) FROM kernel.participants`); got != 4 {
		t.Errorf("rows = %d, want 4", got)
	}
}

// TestCreateParticipantRejectsBadDocumentAndCountry covers the validations
// the design adds beyond the criteria.
func TestCreateParticipantRejectsBadDocumentAndCountry(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	cases := map[string]func(*state.NewParticipant){
		"blank display name":      func(p *state.NewParticipant) { p.DisplayName = " " },
		"unknown document type":   func(p *state.NewParticipant) { p.DocumentType = "DNI"; p.DocumentNumber = "1" },
		"type without number":     func(p *state.NewParticipant) { p.DocumentType = model.DocumentTypeCC },
		"number without type":     func(p *state.NewParticipant) { p.DocumentNumber = "123" },
		"lowercase country":       func(p *state.NewParticipant) { p.CountryCode = "co" },
		"three letter country":    func(p *state.NewParticipant) { p.CountryCode = "COL" },
		"nil identity uuid":       func(p *state.NewParticipant) { p.IdentityID = "00000000-0000-0000-0000-000000000000" },
		"malformed identity uuid": func(p *state.NewParticipant) { p.IdentityID = "not-a-uuid" },
	}
	for name, mutate := range cases {
		in := newPerson("", "Someone")
		mutate(&in)
		_, err := state.CreateParticipant(context.Background(), api, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}

	in := newPerson("", "Complete")
	in.LegalName, in.DocumentType, in.DocumentNumber, in.CountryCode = "Complete SAS", model.DocumentTypeNIT, "900123", "CO"
	mustParticipant(t, api, spaceA, in)
	if got := count(t, admin, `SELECT count(*) FROM kernel.participants`); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
}

// TestCreateParticipantUnknownIdentity: a foreign key failure on the identity
// is invalid input, and an unknown space is not found.
func TestCreateParticipantUnknownIdentity(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	_, err := state.CreateParticipant(context.Background(), api, system, spaceA,
		newPerson("01900000-0000-7000-8000-0000000000aa", "Ghost"))
	requireCode(t, err, model.CodeInvalidInput)

	_, err = state.CreateParticipant(context.Background(), api, system,
		"01900000-0000-7000-8000-0000000000bb", newPerson("", "Nowhere"))
	requireCode(t, err, model.CodeNotFound)
}

// TestParticipantIsolatedAcrossSpaces guards criterion 6 with direct SQL, as
// the Go API always fixes space_id.
func TestParticipantIsolatedAcrossSpaces(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	id := mustParticipant(t, api, spaceA, newPerson("", "In A"))
	visible(t, api, spaceB, func(tx *sql.Tx) {
		if got := count(t, tx, `SELECT count(*) FROM kernel.participants WHERE id = $1`, id); got != 0 {
			t.Errorf("space B reads %d rows of A, want 0", got)
		}
	})

	err := state.InSpace(context.Background(), api, spaceB, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE kernel.participants SET status = 'archived', archived_at = now() WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 0 {
			t.Errorf("space B updated %d rows of A, want 0", n)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("update from B: %v", err)
	}

	err = state.InSpace(context.Background(), api, spaceB, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kernel.participants (id, space_id, kind, display_name)
			VALUES ('01900000-0000-7000-8000-0000000000c1', $1, 'person', 'Intruder')`, spaceA)
		return err
	})
	requireSQLState(t, err, "42501")

	err = state.InSpace(context.Background(), api, spaceB, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE kernel.participants SET space_id = $1 WHERE id = $2`, spaceB, id)
		return err
	})
	requireSQLState(t, err, "42501")

	var status string
	if err := admin.QueryRow(`SELECT status FROM kernel.participants WHERE id = $1`, id).Scan(&status); err != nil || status != "active" {
		t.Errorf("participant status = %q (%v), want active", status, err)
	}
}

// TestArchiveParticipantKeepsItReadable guards criterion 7 and idempotency.
func TestArchiveParticipantKeepsItReadable(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	id := mustParticipant(t, api, spaceA, newPerson("", "To archive"))
	if err := state.ArchiveParticipant(context.Background(), api, system, spaceA, id); err != nil {
		t.Fatalf("ArchiveParticipant: %v", err)
	}
	var status string
	var first sql.NullTime
	read := func() {
		visible(t, api, spaceA, func(tx *sql.Tx) {
			if err := tx.QueryRow(`SELECT status, archived_at FROM kernel.participants WHERE id = $1`, id).Scan(&status, &first); err != nil {
				t.Fatalf("read archived participant: %v", err)
			}
		})
	}
	read()
	if status != "archived" || !first.Valid {
		t.Fatalf("participant = (%s, %v), want archived with archived_at", status, first)
	}

	if err := state.ArchiveParticipant(context.Background(), api, system, spaceA, id); err != nil {
		t.Fatalf("second ArchiveParticipant: %v", err)
	}
	firstAt := first.Time
	read()
	if !first.Time.Equal(firstAt) {
		t.Errorf("archived_at changed from %v to %v", firstAt, first.Time)
	}
}

// TestArchiveParticipantUnknownOrOtherSpaceIsNotFound: a missing id, or one of
// another space, is not found.
func TestArchiveParticipantUnknownOrOtherSpaceIsNotFound(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	id := mustParticipant(t, api, spaceA, newPerson("", "In A"))
	err := state.ArchiveParticipant(context.Background(), api, system, spaceB, id)
	requireCode(t, err, model.CodeNotFound)
	err = state.ArchiveParticipant(context.Background(), api, system, spaceA, "01900000-0000-7000-8000-0000000000dd")
	requireCode(t, err, model.CodeNotFound)
	err = state.ArchiveParticipant(context.Background(), api, system, "", id)
	requireCode(t, err, model.CodeInvalidInput)

	var status string
	if err := admin.QueryRow(`SELECT status FROM kernel.participants WHERE id = $1`, id).Scan(&status); err != nil || status != "active" {
		t.Errorf("participant status = %q (%v), want active", status, err)
	}
}

// TestParticipantOperationsRejectNonSystemActor: only the system actor writes
// participants until permissions arrive.
func TestParticipantOperationsRejectNonSystemActor(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	id := mustParticipant(t, api, spaceA, newPerson("", "In A"))
	for _, actor := range []model.Actor{{}, {Kind: "user"}} {
		_, err := state.CreateParticipant(context.Background(), api, actor, spaceA, newPerson("", "Other"))
		requireCode(t, err, model.CodePermissionDenied)
		err = state.ArchiveParticipant(context.Background(), api, actor, spaceA, id)
		requireCode(t, err, model.CodePermissionDenied)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.participants WHERE status = 'active'`); got != 1 {
		t.Errorf("active rows = %d, want 1", got)
	}
}
