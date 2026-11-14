package state_test

import (
	"database/sql"
	"testing"
)

// Fixed v7 identifiers for rows the administrator inserts directly.
const (
	spaceA = "01900000-0000-7000-8000-00000000000a"
	spaceB = "01900000-0000-7000-8000-00000000000b"
)

// seedSpaces inserts test-space-a and test-space-b as the administrator, who
// bypasses RLS because it is a superuser.
func seedSpaces(t *testing.T, admin *sql.DB) {
	t.Helper()
	const insert = `INSERT INTO kernel.spaces (id, kind, name, slug) VALUES ($1, 'business', $2, $3)`
	if _, err := admin.Exec(insert, spaceA, "Space A", "test-space-a"); err != nil {
		t.Fatalf("seed space a: %v", err)
	}
	if _, err := admin.Exec(insert, spaceB, "Space B", "test-space-b"); err != nil {
		t.Fatalf("seed space b: %v", err)
	}
}

// count runs a single-count query and fails the test on error.
func count(t *testing.T, conn interface {
	QueryRow(string, ...any) *sql.Row
}, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}
