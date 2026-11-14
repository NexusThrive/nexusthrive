package laws_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// stmt is one SQL statement of a test transaction.
type stmt struct {
	query string
	args  []any
}

// newID returns a fresh UUID v7.
func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	return id.String()
}

// pgCode returns the SQLSTATE of a database error, or "" for any other error.
func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// pgConstraint returns the constraint named by a database error.
func pgConstraint(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.ConstraintName
	}
	return ""
}

// runIn runs the statements in one transaction and commits. A non-empty space
// is fixed as the current space first. It returns the first error, including
// the one of the COMMIT, which is where the deferred checks fail.
func runIn(t *testing.T, pool *sql.DB, space string, stmts ...stmt) error {
	t.Helper()
	tx, err := pool.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if space != "" {
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, space); err != nil {
			t.Fatalf("set space: %v", err)
		}
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s.query, s.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// exchangeStmts inserts an exchange of the given actor and one entry per
// triple of account, resource and amount.
func exchangeStmts(t *testing.T, space, key, actor string, entries ...[3]string) []stmt {
	t.Helper()
	id := newID(t)
	stmts := []stmt{{
		`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
		 VALUES ($1, $2, 'test.exchange', $3, $4)`,
		[]any{id, space, key, actor},
	}}
	for _, e := range entries {
		stmts = append(stmts, stmt{
			`INSERT INTO kernel.entries (id, exchange_id, space_id, account_id, resource_id, amount)
			 VALUES ($1, $2, $3, $4, $5, $6::numeric)`,
			[]any{newID(t), id, space, e[0], e[1], e[2]},
		})
	}
	return stmts
}

// checksum summarizes the rows of a table as the administrator, so a test can
// prove that an attempt left them exactly as they were. The summary starts
// with the number of rows.
func checksum(t *testing.T, admin *sql.DB, table string) string {
	t.Helper()
	var sum string
	err := admin.QueryRow(`SELECT count(*) || ':' || coalesce(md5(string_agg(x::text, '' ORDER BY x::text)), '')
		FROM kernel.` + table + ` x`).Scan(&sum)
	if err != nil {
		t.Fatalf("checksum %s: %v", table, err)
	}
	return sum
}
