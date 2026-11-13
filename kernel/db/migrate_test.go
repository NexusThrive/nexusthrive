package db_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/nexusthrive/nexusthrive/kernel/db"
)

// Integration tests read the PostgreSQL URL from DATABASE_URL and fall back
// to the one used by infra/docker/compose.yaml (task infra:up).
const defaultDatabaseURL = "postgres://nexusthrive:nexusthrive@localhost:55432/nexusthrive?sslmode=disable"

// openDatabase connects to PostgreSQL and fails the test when it is not
// reachable; integration tests are never skipped.
func openDatabase(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = defaultDatabaseURL
	}
	conn, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.PingContext(context.Background()); err != nil {
		t.Fatalf("PostgreSQL is not reachable (run `task infra:up` or set DATABASE_URL): %v", err)
	}
	return conn
}

// randomSchema returns a unique schema name and drops it when the test ends.
func randomSchema(t *testing.T, conn *sql.DB) string {
	t.Helper()
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("random schema id: %v", err)
	}
	name := "kernel_test_" + hex.EncodeToString(raw)
	t.Cleanup(func() {
		if _, err := conn.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", name)); err != nil {
			t.Errorf("drop schema %s: %v", name, err)
		}
	})
	return name
}

func count(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func schemaTables(t *testing.T, conn *sql.DB, schema, table string) int {
	return count(t, conn, "SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2", schema, table)
}

func TestMigrateCreatesSchemaAndVersionTable(t *testing.T) {
	conn := openDatabase(t)
	schema := randomSchema(t, conn)

	if err := db.Migrate(context.Background(), conn, db.Config{Schema: schema, Files: fstest.MapFS{}}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := count(t, conn, "SELECT count(*) FROM information_schema.schemata WHERE schema_name = $1", schema); got != 1 {
		t.Errorf("schema %s count = %d, want 1", schema, got)
	}
	if got := schemaTables(t, conn, schema, "goose_db_version"); got != 1 {
		t.Errorf("%s.goose_db_version count = %d, want 1", schema, got)
	}
	if got := schemaTables(t, conn, "public", "goose_db_version"); got != 0 {
		t.Errorf("public.goose_db_version must not exist, count = %d", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	conn := openDatabase(t)
	schema := randomSchema(t, conn)
	files := fstest.MapFS{
		"00001_probe.sql": {Data: []byte(fmt.Sprintf("-- +goose Up\nCREATE TABLE %s.probe (id int PRIMARY KEY);\n", schema))},
	}
	cfg := db.Config{Schema: schema, Files: files}

	for run := 1; run <= 2; run++ {
		if err := db.Migrate(context.Background(), conn, cfg); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if got := schemaTables(t, conn, schema, "probe"); got != 1 {
		t.Errorf("probe table count = %d, want 1", got)
	}
	query := fmt.Sprintf("SELECT count(*) FROM %s.goose_db_version WHERE version_id = 1", schema)
	if got := count(t, conn, query); got != 1 {
		t.Errorf("version 1 recorded %d times, want 1", got)
	}
}

func TestMigrateInvalidSQLRollsBack(t *testing.T) {
	conn := openDatabase(t)
	schema := randomSchema(t, conn)
	body := fmt.Sprintf("-- +goose Up\nCREATE TABLE %s.first_step (id int);\nSELECT * FROM %s.missing_table;\n", schema, schema)
	files := fstest.MapFS{"00001_broken.sql": {Data: []byte(body)}}

	err := db.Migrate(context.Background(), conn, db.Config{Schema: schema, Files: files})
	if err == nil {
		t.Fatal("Migrate with invalid SQL returned nil")
	}
	if !strings.Contains(err.Error(), "00001_broken.sql") {
		t.Errorf("error %q does not name the failing file", err)
	}
	if got := schemaTables(t, conn, schema, "first_step"); got != 0 {
		t.Errorf("first statement persisted: first_step count = %d, want 0", got)
	}
}

func TestMigrateRejectsInvalidSchemaName(t *testing.T) {
	for _, name := range []string{"", "Kernel", `kernel"; DROP SCHEMA public; --`, "1kernel", "kernel-x", "kernel x"} {
		// A nil handle proves validation happens before the database is touched.
		err := db.Migrate(context.Background(), nil, db.Config{Schema: name, Files: fstest.MapFS{}})
		if err == nil {
			t.Errorf("schema %q was accepted", name)
		}
	}
}
