package state_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// ledgerSetup returns the administrator and API pools with spaces A and B seeded.
func ledgerSetup(t *testing.T) (admin, api *sql.DB) {
	t.Helper()
	admin = dbtest.Database(t)
	api = dbtest.APIPool(t, admin)
	seedSpaces(t, admin)
	return admin, api
}

// newCOP builds the Colombian peso resource of the first criterion.
func newCOP() state.NewResource {
	return state.NewResource{Kind: model.ResourceKindMoney, Code: "COP", Name: "Colombian peso", Scale: 2}
}

// mustResource creates a resource and returns its id.
func mustResource(t *testing.T, api *sql.DB, spaceID string, in state.NewResource) string {
	t.Helper()
	id, err := state.CreateResource(context.Background(), api, system, spaceID, in)
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}
	return id
}

// TestCreateResourceStoresMoneyCOP guards criterion 1.
func TestCreateResourceStoresMoneyCOP(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)

	id := mustResource(t, api, spaceA, newCOP())
	got, err := state.GetResource(context.Background(), api, spaceA, id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	want := model.Resource{ID: id, SpaceID: spaceA, Kind: model.ResourceKindMoney, Code: "COP", Name: "Colombian peso", Scale: 2}
	if got != want {
		t.Errorf("resource = %+v, want %+v", got, want)
	}
	if n := count(t, admin, `SELECT count(*) FROM kernel.resources WHERE id = $1 AND substr(id::text, 15, 1) = '7'`, id); n != 1 {
		t.Errorf("stored rows with a v7 id = %d, want 1", n)
	}
}

// TestCreateResourceDuplicateCodeIsRuleViolation guards criterion 1: a code
// repeats nowhere inside a space, but two spaces may share it.
func TestCreateResourceDuplicateCodeIsRuleViolation(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)

	mustResource(t, api, spaceA, newCOP())
	_, err := state.CreateResource(context.Background(), api, system, spaceA, newCOP())
	requireCode(t, err, model.CodeRuleViolation)

	mustResource(t, api, spaceB, newCOP())
	if got := count(t, admin, `SELECT count(*) FROM kernel.resources`); got != 2 {
		t.Errorf("rows = %d, want 2", got)
	}
}

// TestCreateResourceRejectsBadKindScaleCode guards criterion 2: nothing is written.
func TestCreateResourceRejectsBadKindScaleCode(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)

	cases := map[string]func(*state.NewResource){
		"unknown kind":     func(r *state.NewResource) { r.Kind = "foo" },
		"empty kind":       func(r *state.NewResource) { r.Kind = "" },
		"negative scale":   func(r *state.NewResource) { r.Scale = -1 },
		"scale above 18":   func(r *state.NewResource) { r.Scale = 19 },
		"lowercase code":   func(r *state.NewResource) { r.Code = "cop" },
		"empty code":       func(r *state.NewResource) { r.Code = "" },
		"code too long":    func(r *state.NewResource) { r.Code = "A" + strings.Repeat("B", 32) },
		"blank name":       func(r *state.NewResource) { r.Name = "  " },
		"code with spaces": func(r *state.NewResource) { r.Code = "C O" },
	}
	for name, mutate := range cases {
		in := newCOP()
		mutate(&in)
		_, err := state.CreateResource(context.Background(), api, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.resources`); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
	for _, spaceID := range []string{"", "not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		_, err := state.CreateResource(context.Background(), api, system, spaceID, newCOP())
		requireCode(t, err, model.CodeInvalidInput)
	}
}

// TestCreateResourceAcceptsScaleLimits covers the edges of the scale range.
func TestCreateResourceAcceptsScaleLimits(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)

	mustResource(t, api, spaceA, state.NewResource{Kind: model.ResourceKindUnit, Code: "UND", Name: "Unit", Scale: 0})
	mustResource(t, api, spaceA, state.NewResource{Kind: model.ResourceKindTime, Code: "MIN", Name: "Minute", Scale: 18})
}

// TestCreateResourceInUnknownSpaceIsNotFound: the space must exist.
func TestCreateResourceInUnknownSpaceIsNotFound(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)

	_, err := state.CreateResource(context.Background(), api, system, "01900000-0000-7000-8000-0000000000ff", newCOP())
	requireCode(t, err, model.CodeNotFound)
}

// TestLedgerWritesRejectNonSystemActor: only the system actor writes until
// permissions arrive, and nothing is touched.
func TestLedgerWritesRejectNonSystemActor(t *testing.T) {
	t.Parallel()
	admin, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	resource := mustResource(t, api, spaceA, newCOP())
	account := mustAccount(t, api, spaceA, cashAccount(holder, resource))

	for _, actor := range []model.Actor{{}, {Kind: model.ActorPerson}, {Kind: "user"}} {
		_, err := state.CreateResource(context.Background(), api, actor, spaceA, state.NewResource{Kind: "unit", Code: "UND", Name: "Unit"})
		requireCode(t, err, model.CodePermissionDenied)
		_, err = state.OpenAccount(context.Background(), api, actor, spaceA, state.NewAccount{
			HolderParticipantID: holder, ResourceID: resource, Class: model.AccountClassAsset, Code: "BANK", Name: "Bank"})
		requireCode(t, err, model.CodePermissionDenied)
		requireCode(t, state.CloseAccount(context.Background(), api, actor, spaceA, account), model.CodePermissionDenied)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.resources`); got != 1 {
		t.Errorf("resources = %d, want 1", got)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.accounts WHERE status = 'open'`); got != 1 {
		t.Errorf("open accounts = %d, want 1", got)
	}
}

// TestResourcesIsolationBetweenSpaces guards criterion 7 for resources.
func TestResourcesIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)

	id := mustResource(t, api, spaceA, newCOP())
	visible(t, api, spaceB, func(tx *sql.Tx) {
		if got := count(t, tx, `SELECT count(*) FROM kernel.resources`); got != 0 {
			t.Errorf("space B sees %d resources of A, want 0", got)
		}
	})
	_, err := state.GetResource(context.Background(), api, spaceB, id)
	requireCode(t, err, model.CodeNotFound)
}

// TestInsertForeignSpaceRejected guards criterion 7: the API role cannot write
// a row whose space is not the current one.
func TestInsertForeignSpaceRejected(t *testing.T) {
	t.Parallel()
	_, api := ledgerSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder A"))
	resource := mustResource(t, api, spaceA, newCOP())

	for name, query := range map[string]string{
		"resource": `INSERT INTO kernel.resources (id, space_id, kind, code, name, scale)
			VALUES ('01900000-0000-7000-8000-000000000f01', '` + spaceA + `', 'money', 'USD', 'Dollar', 2)`,
		"account": `INSERT INTO kernel.accounts (id, space_id, holder_participant_id, resource_id, class, code, name)
			VALUES ('01900000-0000-7000-8000-000000000f02', '` + spaceA + `', '` + holder + `', '` + resource + `', 'asset', 'X', 'X')`,
	} {
		err := state.InSpace(context.Background(), api, spaceB, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, query)
			return err
		})
		if err == nil {
			t.Errorf("%s: insert with the foreign space must fail", name)
			continue
		}
		requireSQLState(t, err, "42501")
	}
}
