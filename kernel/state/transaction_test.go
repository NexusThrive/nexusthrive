package state_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestInSpaceRejectsInvalidSpaceID guards criterion 8: an empty, nil or
// malformed space is refused before any transaction is opened. The pool is
// closed, so a BeginTx call would return a different error.
func TestInSpaceRejectsInvalidSpaceID(t *testing.T) {
	t.Parallel()
	closed, err := sql.Open("pgx", dbtest.URL())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	closed.Close()

	for _, id := range []string{"", uuid.Nil.String(), "abc"} {
		called := false
		err := state.InSpace(context.Background(), closed, id, func(context.Context, *sql.Tx) error {
			called = true
			return nil
		})
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("InSpace(%q) = %v, want invalid_input", id, err)
		}
		if called {
			t.Errorf("InSpace(%q) ran the callback", id)
		}
	}
}

// TestNoSpaceSeesNoRows guards criterion 4.
func TestNoSpaceSeesNoRows(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	seedSpaces(t, admin)

	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces"); got != 2 {
		t.Fatalf("administrator sees %d rows, want 2", got)
	}
	if got := count(t, api, "SELECT count(*) FROM kernel.spaces"); got != 0 {
		t.Errorf("API role without a space sees %d rows, want 0", got)
	}
}

// TestSpaceDoesNotLeakAcrossTransactions guards criterion 6 with a pool of a
// single connection, so the second query reuses the first one's session.
func TestSpaceDoesNotLeakAcrossTransactions(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	api.SetMaxOpenConns(1)
	seedSpaces(t, admin)

	var pidInside int
	err := state.InSpace(context.Background(), api, spaceA, func(ctx context.Context, tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pidInside); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM kernel.spaces").Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("inside the space: %d rows, want 1", n)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InSpace: %v", err)
	}

	var pidAfter int
	var setting sql.NullString
	if err := api.QueryRow("SELECT pg_backend_pid(), current_setting('kernel.space_id', true)").Scan(&pidAfter, &setting); err != nil {
		t.Fatalf("query after commit: %v", err)
	}
	if pidAfter != pidInside {
		t.Fatalf("connection changed (%d then %d): the test does not prove reuse", pidInside, pidAfter)
	}
	if setting.Valid && setting.String != "" {
		t.Errorf("space_id leaked after commit: %q", setting.String)
	}
	if got := count(t, api, "SELECT count(*) FROM kernel.spaces"); got != 0 {
		t.Errorf("reused connection sees %d rows, want 0", got)
	}
}

// TestInSpaceRollsBackOnErrorAndPanic proves nothing written inside a failed or
// panicking callback survives and that the panic is re-raised.
func TestInSpaceRollsBackOnErrorAndPanic(t *testing.T) {
	t.Parallel()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	const id = "01900000-0000-7000-8000-0000000000c1"
	insert := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', 'n', 'test-space-c')`, id)
		return err
	}

	boom := errors.New("boom")
	err := state.InSpace(context.Background(), api, id, func(ctx context.Context, tx *sql.Tx) error {
		if err := insert(ctx, tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap boom", err)
	}
	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces"); got != 0 {
		t.Errorf("after error: %d rows, want 0", got)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != "kaboom" {
				t.Errorf("recovered %v, want kaboom re-raised", recovered)
			}
		}()
		_ = state.InSpace(context.Background(), api, id, func(ctx context.Context, tx *sql.Tx) error {
			if err := insert(ctx, tx); err != nil {
				return err
			}
			panic("kaboom")
		})
	}()
	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces"); got != 0 {
		t.Errorf("after panic: %d rows, want 0", got)
	}

	if err := state.InSpace(context.Background(), api, id, insert); err != nil {
		t.Fatalf("commit path: %v", err)
	}
	if got := count(t, admin, "SELECT count(*) FROM kernel.spaces"); got != 1 {
		t.Errorf("after commit: %d rows, want 1", got)
	}
}
