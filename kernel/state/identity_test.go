package state_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// newIdentity builds a valid system identity with the given subject.
func newIdentity(subject string) state.NewIdentity {
	return state.NewIdentity{
		Provider:     model.IdentityProviderSystem,
		Subject:      subject,
		PrimaryEmail: subject + "@example.com",
		DisplayName:  "Identity " + subject,
	}
}

// requireCode fails the test unless err is a typed error with the given code.
func requireCode(t *testing.T, err error, want model.Code) {
	t.Helper()
	got, ok := model.CodeOf(err)
	if !ok || got != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}

// mustIdentity creates an identity and returns its id.
func mustIdentity(t *testing.T, api *sql.DB, subject string) string {
	t.Helper()
	id, err := state.CreateIdentity(context.Background(), api, system, newIdentity(subject))
	if err != nil {
		t.Fatalf("CreateIdentity(%s): %v", subject, err)
	}
	return id
}

// TestCreateIdentityDuplicateIsRuleViolation guards criterion 1.
func TestCreateIdentityDuplicateIsRuleViolation(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	mustIdentity(t, api, "s1")
	_, err := state.CreateIdentity(context.Background(), api, system, newIdentity("s1"))
	requireCode(t, err, model.CodeRuleViolation)
	if got := count(t, admin, `SELECT count(*) FROM kernel.identities WHERE provider = 'system' AND subject = 's1'`); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
}

// TestCreateIdentityRejectsUnknownProvider guards criterion 2, plus the
// other invalid input of the contract.
func TestCreateIdentityRejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	for _, provider := range []model.IdentityProvider{"", "google"} {
		in := newIdentity("s1")
		in.Provider = provider
		_, err := state.CreateIdentity(context.Background(), api, system, in)
		requireCode(t, err, model.CodeInvalidInput)
	}
	blankSubject := newIdentity("s1")
	blankSubject.Subject = "  "
	_, err := state.CreateIdentity(context.Background(), api, system, blankSubject)
	requireCode(t, err, model.CodeInvalidInput)
	blankName := newIdentity("s1")
	blankName.DisplayName = ""
	_, err = state.CreateIdentity(context.Background(), api, system, blankName)
	requireCode(t, err, model.CodeInvalidInput)

	if got := count(t, admin, `SELECT count(*) FROM kernel.identities`); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
}

// TestCreateIdentityReturnsV7 checks the returned id is the stored UUID v7.
func TestCreateIdentityReturnsV7(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	id := mustIdentity(t, api, "s1")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("id %q is not a UUID v7 (%v)", id, err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.identities WHERE id = $1`, id); got != 1 {
		t.Errorf("stored rows = %d, want 1", got)
	}
}

// TestCreateIdentityStaysInvisibleWithoutParticipant: the API role cannot read
// an identity that has no participant in the current space.
func TestCreateIdentityStaysInvisibleWithoutParticipant(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	mustIdentity(t, api, "s1")
	if got := count(t, admin, `SELECT count(*) FROM kernel.identities`); got != 1 {
		t.Fatalf("administrator sees %d identities, want 1", got)
	}
	err := state.InSpace(context.Background(), api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		if got := count(t, tx, `SELECT count(*) FROM kernel.identities`); got != 0 {
			t.Errorf("space A sees %d identities, want 0", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InSpace: %v", err)
	}
}

// TestIdentityOperationsRejectNonSystemActor: only the system actor creates
// identities until permissions arrive.
func TestIdentityOperationsRejectNonSystemActor(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	for _, actor := range []model.Actor{{}, {Kind: "user"}} {
		_, err := state.CreateIdentity(context.Background(), api, actor, newIdentity("s1"))
		requireCode(t, err, model.CodePermissionDenied)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.identities`); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
}
