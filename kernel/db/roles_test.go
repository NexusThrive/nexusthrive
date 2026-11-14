package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// TestSetAPIRolePassword runs inside a transaction that is rolled back: the
// role belongs to the whole cluster and must stay NOLOGIN for other tests.
func TestSetAPIRolePassword(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	ctx := context.Background()

	canLogin := func(q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}) bool {
		var login bool
		if err := q.QueryRowContext(ctx, "SELECT rolcanlogin FROM pg_roles WHERE rolname = $1", db.APIRole).Scan(&login); err != nil {
			t.Fatalf("read role: %v", err)
		}
		return login
	}

	tx, err := admin.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	if err := db.SetAPIRolePassword(ctx, tx, ""); err != nil {
		t.Fatalf("empty password: %v", err)
	}
	if canLogin(tx) {
		t.Error("an empty password must leave the role NOLOGIN")
	}
	// A quote and a backslash exercise the server-side quoting.
	if err := db.SetAPIRolePassword(ctx, tx, `p'a\ss"word`); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if !canLogin(tx) {
		t.Error("a password must enable login")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if canLogin(admin) {
		t.Error("the role must be NOLOGIN again after the rollback")
	}
}
