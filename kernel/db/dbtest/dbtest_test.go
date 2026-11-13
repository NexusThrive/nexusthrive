package dbtest

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func countRows(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func databaseExists(t *testing.T, admin *sql.DB, name string) bool {
	t.Helper()
	return countRows(t, admin, "SELECT count(*) FROM pg_database WHERE datname = $1", name) == 1
}

func schemaExists(t *testing.T, admin *sql.DB, name string) bool {
	t.Helper()
	return countRows(t, admin, "SELECT count(*) FROM information_schema.schemata WHERE schema_name = $1", name) == 1
}

func currentDatabase(t *testing.T, conn *sql.DB) string {
	t.Helper()
	var name string
	if err := conn.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	return name
}

func TestDatabaseAppliesKernelMigrations(t *testing.T) {
	t.Parallel()
	conn := Database(t)

	if got := countRows(t, conn, "SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'kernel'"); got != 1 {
		t.Errorf("kernel schema count = %d, want 1", got)
	}
	if got := countRows(t, conn, "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'kernel' AND table_name = 'goose_db_version'"); got != 1 {
		t.Errorf("kernel.goose_db_version count = %d, want 1", got)
	}
}

func TestDatabaseIsDroppedAfterTest(t *testing.T) {
	admin := Admin(t)
	var name string
	t.Run("create", func(t *testing.T) {
		name = currentDatabase(t, Database(t))
		if !databaseExists(t, admin, name) {
			t.Fatalf("database %s should exist while the test runs", name)
		}
	})
	if name == "" {
		t.Fatal("subtest did not record a database name")
	}
	if databaseExists(t, admin, name) {
		t.Errorf("database %s still exists after the test ended", name)
	}
}

func TestSchemaIsDroppedAfterTest(t *testing.T) {
	admin := Admin(t)
	var name string
	t.Run("create", func(t *testing.T) {
		_, name = Schema(t)
		if !schemaExists(t, admin, name) {
			t.Fatalf("schema %s should exist while the test runs", name)
		}
	})
	if name == "" {
		t.Fatal("subtest did not record a schema name")
	}
	if schemaExists(t, admin, name) {
		t.Errorf("schema %s still exists after the test ended", name)
	}
}

func TestParallelDatabasesDoNotSeeEachOther(t *testing.T) {
	const workers = 8
	var mu sync.Mutex
	names := map[string]bool{}

	t.Run("group", func(t *testing.T) {
		for i := 0; i < workers; i++ {
			t.Run(fmt.Sprintf("worker_%d", i), func(t *testing.T) {
				t.Parallel()
				conn := Database(t)
				mu.Lock()
				names[currentDatabase(t, conn)] = true
				mu.Unlock()

				if _, err := conn.Exec("CREATE TABLE probe (worker int)"); err != nil {
					t.Fatalf("create probe: %v", err)
				}
				for row := 0; row <= i; row++ {
					if _, err := conn.Exec("INSERT INTO probe VALUES ($1)", i); err != nil {
						t.Fatalf("insert: %v", err)
					}
				}
				if got := countRows(t, conn, "SELECT count(*) FROM probe"); got != i+1 {
					t.Errorf("worker %d sees %d rows, want %d", i, got, i+1)
				}
				if got := countRows(t, conn, "SELECT count(*) FROM probe WHERE worker <> $1", i); got != 0 {
					t.Errorf("worker %d sees %d rows from other workers", i, got)
				}
			})
		}
	})
	if len(names) != workers {
		t.Errorf("got %d distinct databases, want %d", len(names), workers)
	}
}

func TestParallelSchemasAreDistinct(t *testing.T) {
	const workers = 8
	var mu sync.Mutex
	names := map[string]bool{}

	t.Run("group", func(t *testing.T) {
		for i := 0; i < workers; i++ {
			t.Run(fmt.Sprintf("worker_%d", i), func(t *testing.T) {
				t.Parallel()
				_, name := Schema(t)
				mu.Lock()
				names[name] = true
				mu.Unlock()
			})
		}
	})
	if len(names) != workers {
		t.Errorf("got %d distinct schemas, want %d", len(names), workers)
	}
}

func TestConnectErrorMentionsInfraUp(t *testing.T) {
	t.Parallel()
	conn, err := connect("postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable")
	if err == nil {
		conn.Close()
		t.Fatal("connect to a closed port returned nil")
	}
	for _, want := range []string{"task infra:up", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestURLUsesDatabaseURLEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example/custom")
	if got := URL(); got != "postgres://example/custom" {
		t.Errorf("URL() = %q, want the DATABASE_URL value", got)
	}

	t.Setenv("DATABASE_URL", "")
	if got := URL(); !strings.Contains(got, ":55432/") {
		t.Errorf("default URL %q does not use port 55432", got)
	}
}

func TestNamesAreValidIdentifiers(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		name, err := newName(time.Now())
		if err != nil {
			t.Fatalf("newName: %v", err)
		}
		if !identifierPattern.MatchString(name) {
			t.Fatalf("name %q is not a valid identifier", name)
		}
		if seen[name] {
			t.Fatalf("name %q repeated", name)
		}
		seen[name] = true
	}
}

func TestSweepRemovesOnlyStaleObjects(t *testing.T) {
	t.Parallel()
	admin := Admin(t)
	now := time.Now()

	stale, err := newName(now.Add(-3 * time.Hour))
	if err != nil {
		t.Fatalf("newName: %v", err)
	}
	fresh, err := newName(now)
	if err != nil {
		t.Fatalf("newName: %v", err)
	}
	for _, name := range []string{stale, fresh} {
		if _, err := admin.Exec("CREATE SCHEMA " + name); err != nil {
			t.Fatalf("create schema %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		for _, name := range []string{stale, fresh} {
			if _, err := admin.Exec("DROP SCHEMA IF EXISTS " + name + " CASCADE"); err != nil {
				t.Errorf("drop schema %s: %v", name, err)
			}
		}
	})

	if err := sweep(admin, now.Add(-time.Hour)); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if schemaExists(t, admin, stale) {
		t.Errorf("stale schema %s survived the sweep", stale)
	}
	if !schemaExists(t, admin, fresh) {
		t.Errorf("fresh schema %s was removed by the sweep", fresh)
	}
}
