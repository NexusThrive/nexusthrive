package state_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

var system = model.Actor{Kind: model.ActorSystem}

func newSpace(slug string) state.NewSpace {
	return state.NewSpace{Kind: model.SpaceKindBusiness, Name: "Test " + slug, Slug: slug}
}

func mustCreate(t *testing.T, api *sql.DB, slug string) string {
	t.Helper()
	id, err := state.CreateSpace(context.Background(), api, system, newSpace(slug))
	if err != nil {
		t.Fatalf("CreateSpace(%s): %v", slug, err)
	}
	return id
}

func listSlugs(t *testing.T, ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) []string {
	t.Helper()
	rows, err := q.QueryContext(ctx, "SELECT slug FROM kernel.spaces ORDER BY slug")
	if err != nil {
		t.Fatalf("list spaces: %v", err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		slugs = append(slugs, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list spaces: %v", err)
	}
	return slugs
}

// TestCreateSpaceVisibleOnlyInOwnSpace guards criterion 3, through the helper
// and through a literal SET LOCAL.
func TestCreateSpaceVisibleOnlyInOwnSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	a := mustCreate(t, api, "test-space-a")
	b := mustCreate(t, api, "test-space-b")

	for id, want := range map[string]string{a: "test-space-a", b: "test-space-b"} {
		err := state.InSpace(context.Background(), api, id, func(ctx context.Context, tx *sql.Tx) error {
			if got := listSlugs(t, ctx, tx); len(got) != 1 || got[0] != want {
				t.Errorf("space %s sees %v, want [%s]", id, got, want)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("InSpace: %v", err)
		}
	}

	tx, err := api.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("SET LOCAL kernel.space_id = '" + a + "'"); err != nil {
		t.Fatalf("set local: %v", err)
	}
	if got := listSlugs(t, context.Background(), tx); len(got) != 1 || got[0] != "test-space-a" {
		t.Errorf("literal SET LOCAL sees %v, want [test-space-a]", got)
	}
}

// TestRLSRejectsWritesToOtherSpace guards criterion 5. An insert or an id
// change toward another space is rejected with SQLSTATE 42501; an update that
// targets another space's row touches nothing, because the policy hides it.
func TestRLSRejectsWritesToOtherSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	a := mustCreate(t, api, "test-space-a")
	b := mustCreate(t, api, "test-space-b")

	insufficient := func(err error) bool {
		var pg *pgconn.PgError
		return errors.As(err, &pg) && pg.Code == "42501"
	}

	err := state.InSpace(context.Background(), api, a, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'x', 'test-space-x')`, uuid.Must(uuid.NewV7()).String())
		return err
	})
	if !insufficient(err) {
		t.Errorf("insert of another id: %v, want 42501", err)
	}
	err = state.InSpace(context.Background(), api, a, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE kernel.spaces SET id = $1 WHERE id = $2`, b, a)
		return err
	})
	if !insufficient(err) {
		t.Errorf("update id to another space: %v, want 42501", err)
	}
	err = state.InSpace(context.Background(), api, a, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE kernel.spaces SET name = 'hijacked' WHERE id = $1`, b)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 0 {
			t.Errorf("update of another space's row affected %d rows, want 0", n)
		}
		return nil
	})
	if err != nil {
		t.Errorf("update of a hidden row: %v, want no error", err)
	}
	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces WHERE name = 'hijacked'"); got != 0 {
		t.Errorf("another space's row was modified")
	}
	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces"); got != 2 {
		t.Errorf("rows = %d, want 2", got)
	}
}

// TestCreateSpaceCreatesProvisioningSpace guards the success path of criterion 7.
func TestCreateSpaceCreatesProvisioningSpace(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	id := mustCreate(t, api, "test-space-a")

	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("id %q is not a UUID v7 (%v)", id, err)
	}
	var kind, name, status, locale, tz, currency string
	if err := admin.QueryRow(`SELECT kind, name, status, locale, timezone, currency FROM kernel.spaces WHERE id = $1`, id).
		Scan(&kind, &name, &status, &locale, &tz, &currency); err != nil {
		t.Fatalf("read space: %v", err)
	}
	if kind != "business" || name != "Test test-space-a" || status != "provisioning" || locale != "es-CO" || tz != "America/Bogota" || currency != "COP" {
		t.Errorf("space = %s %s %s %s %s %s", kind, name, status, locale, tz, currency)
	}
}

// TestCreateSpaceDuplicateSlug guards criterion 7: a repeated slug is a rule
// violation and writes nothing.
func TestCreateSpaceDuplicateSlug(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	mustCreate(t, api, "test-space-a")

	_, err := state.CreateSpace(context.Background(), api, system, newSpace("test-space-a"))
	if code, ok := model.CodeOf(err); !ok || code != model.CodeRuleViolation {
		t.Errorf("duplicate slug = %v, want rule_violation", err)
	}
	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces"); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
}

// TestCreateSpaceRejectsInvalidInput covers unknown kind, empty name and bad slugs.
func TestCreateSpaceRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	cases := map[string]state.NewSpace{
		"unknown kind":  {Kind: "galaxy", Name: "n", Slug: "test-space-a"},
		"empty kind":    {Kind: "", Name: "n", Slug: "test-space-a"},
		"empty name":    {Kind: model.SpaceKindBusiness, Name: "  ", Slug: "test-space-a"},
		"empty slug":    {Kind: model.SpaceKindBusiness, Name: "n", Slug: ""},
		"uppercase":     {Kind: model.SpaceKindBusiness, Name: "n", Slug: "Test-Space"},
		"underscore":    {Kind: model.SpaceKindBusiness, Name: "n", Slug: "test_space"},
		"edge hyphen":   {Kind: model.SpaceKindBusiness, Name: "n", Slug: "-test"},
		"double hyphen": {Kind: model.SpaceKindBusiness, Name: "n", Slug: "test--space"},
		"too long":      {Kind: model.SpaceKindBusiness, Name: "n", Slug: strings.Repeat("a", 64)},
	}
	for name, in := range cases {
		_, err := state.CreateSpace(context.Background(), api, system, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: %v, want invalid_input", name, err)
		}
	}
	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces"); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
}

// TestCreateSpaceRejectsNonSystemActor proves only the system actor bootstraps.
func TestCreateSpaceRejectsNonSystemActor(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)

	for _, actor := range []model.Actor{{}, {Kind: "person"}} {
		_, err := state.CreateSpace(context.Background(), api, actor, newSpace("test-space-a"))
		if code, ok := model.CodeOf(err); !ok || code != model.CodePermissionDenied {
			t.Errorf("actor %q = %v, want permission_denied", actor.Kind, err)
		}
	}
	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces"); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
}

// TestCreateSpacePlatformThroughAPIRoleIsPermissionDenied: platform spaces are
// provisioned at bootstrap, never by the execution role. The administrator
// connection can create one.
func TestCreateSpacePlatformThroughAPIRoleIsPermissionDenied(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	in := state.NewSpace{Kind: model.SpaceKindPlatform, Name: "Platform", Slug: "platform-x"}

	_, err := state.CreateSpace(context.Background(), api, system, in)
	requireCode(t, err, model.CodePermissionDenied)
	if got := count(t, admin, `SELECT count(*) FROM kernel.spaces`); got != 0 {
		t.Errorf("spaces = %d, want 0", got)
	}
	if _, err := state.CreateSpace(context.Background(), admin, system, in); err != nil {
		t.Errorf("CreateSpace as the administrator: %v", err)
	}
	for _, kind := range []model.SpaceKind{model.SpaceKindBusiness, model.SpaceKindCommunity, model.SpaceKindPersonal} {
		if _, err := state.CreateSpace(context.Background(), api, system, state.NewSpace{Kind: kind, Name: "N", Slug: "ok-" + string(kind)}); err != nil {
			t.Errorf("CreateSpace %s: %v", kind, err)
		}
	}
}
