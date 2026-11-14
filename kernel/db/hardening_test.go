package db_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// TestCurrentSpaceIDPinsSearchPath checks that the function carries its own
// search_path, so callers cannot redirect the names it resolves.
func TestCurrentSpaceIDPinsSearchPath(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	var config string
	err := admin.QueryRow(`SELECT coalesce(array_to_string(proconfig, ','), '') FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'kernel' AND p.proname = 'current_space_id'`).Scan(&config)
	if err != nil {
		t.Fatalf("read proconfig: %v", err)
	}
	if !strings.Contains(config, "search_path=pg_catalog, pg_temp") {
		t.Errorf("proconfig = %q, want a fixed search_path of pg_catalog, pg_temp", config)
	}
}

// TestCurrentSpaceIDIgnoresShadowingFunction proves a function planted earlier
// in the search path cannot replace current_setting inside the function.
func TestCurrentSpaceIDIgnoresShadowingFunction(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	const real, fake = "01900000-0000-7000-8000-00000000000a", "01900000-0000-7000-8000-00000000000f"

	if _, err := admin.Exec(`CREATE FUNCTION public.current_setting(text, boolean) RETURNS text
		LANGUAGE sql AS $$ SELECT '` + fake + `'::text $$`); err != nil {
		t.Fatalf("create shadowing function: %v", err)
	}
	tx, err := admin.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SET LOCAL search_path = public, pg_catalog`); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, real); err != nil {
		t.Fatalf("set space: %v", err)
	}
	var got string
	if err := tx.QueryRow(`SELECT kernel.current_space_id()::text`).Scan(&got); err != nil {
		t.Fatalf("current_space_id: %v", err)
	}
	if got != real {
		t.Errorf("current_space_id() = %s, want %s: a shadowing function won", got, real)
	}
}

// TestCurrentSpaceIDMalformedValueFails keeps the fail-closed behaviour: an
// empty value is unset, a malformed one is an error, a valid one is returned.
func TestCurrentSpaceIDMalformedValueFails(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)

	read := func(value string) (sql.NullString, error) {
		tx, err := admin.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, value); err != nil {
			t.Fatalf("set space: %v", err)
		}
		var got sql.NullString
		err = tx.QueryRow(`SELECT kernel.current_space_id()::text`).Scan(&got)
		return got, err
	}

	_, err := read("abc")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22P02" {
		t.Errorf("malformed value error = %v, want SQLSTATE 22P02", err)
	}
	if got, err := read(""); err != nil || got.Valid {
		t.Errorf("empty value = %v (%v), want NULL", got, err)
	}
	const valid = "01900000-0000-7000-8000-00000000000a"
	if got, err := read(valid); err != nil || got.String != valid {
		t.Errorf("valid value = %v (%v), want %s", got, err, valid)
	}
}

// guardBlock extracts the first StatementBegin/StatementEnd block of the
// hardening migration, which holds the role guard.
func guardBlock(t *testing.T) string {
	t.Helper()
	raw, err := fs.ReadFile(db.KernelMigrations(), "00004_hardening.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	match := regexp.MustCompile(`(?s)-- \+goose StatementBegin\n(.*?)-- \+goose StatementEnd`).FindSubmatch(raw)
	if match == nil {
		t.Fatal("no StatementBegin block in 00004_hardening.sql")
	}
	return string(match[1])
}

// TestRoleGuardRejectsDangerousAttributes runs the guard inside a transaction
// that grants each dangerous attribute and then rolls back. ALTER ROLE is
// transactional and invisible to other sessions.
func TestRoleGuardRejectsDangerousAttributes(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	guard := guardBlock(t)

	for _, attribute := range []string{"SUPERUSER", "BYPASSRLS", "CREATEROLE", "CREATEDB", "REPLICATION"} {
		t.Run(attribute, func(t *testing.T) {
			tx, err := admin.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec("ALTER ROLE " + db.APIRole + " " + attribute); err != nil {
				t.Fatalf("alter role: %v", err)
			}
			if _, err := tx.Exec(guard); err == nil {
				t.Errorf("guard accepted a role with %s", attribute)
			}
		})
	}

	// The unchanged role passes the guard.
	if _, err := admin.Exec(guard); err != nil {
		t.Errorf("guard rejected the limited role: %v", err)
	}
}
