// Package dbtest gives integration tests an isolated PostgreSQL database or
// schema with the kernel migrations applied and guaranteed cleanup. Tests run
// against the PostgreSQL of `task infra:up` locally and the postgres service
// in CI; they fail, never skip, when it is not reachable.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/nexusthrive/nexusthrive/kernel/db"
)

// defaultURL is the PostgreSQL started by infra/docker/compose.yaml.
const defaultURL = "postgres://nexusthrive:nexusthrive@localhost:55432/nexusthrive?sslmode=disable"

// namePrefix marks every database and schema created by this package so the
// sweep can recognise leftovers without touching anything else.
const namePrefix = "kernel_test_"

// staleAfter is how old a leftover must be before the sweep removes it. It is
// long enough not to hit objects of a package that is still running.
const staleAfter = time.Hour

// connectTimeout bounds the first ping so an absent server fails fast.
const connectTimeout = 3 * time.Second

// namePattern recovers the creation time from a generated name: 16 hex digits
// of unix nanoseconds followed by 12 random ones.
var namePattern = regexp.MustCompile(`^` + namePrefix + `([0-9a-f]{16})[0-9a-f]{12}$`)

// identifierPattern is the same rule db.Migrate applies to schema names.
var identifierPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

var (
	adminOnce sync.Once
	adminConn *sql.DB
	adminErr  error
)

// URL returns DATABASE_URL, or the compose default on port 55432.
func URL() string {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
	return defaultURL
}

// connect opens a pool and pings it. The error tells the developer how to
// start the database because a missing server is the usual cause.
func connect(rawURL string) (*sql.DB, error) {
	conn, err := sql.Open("pgx", rawURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("PostgreSQL is not reachable: run `task infra:up` or set DATABASE_URL: %w", err)
	}
	return conn, nil
}

// newName returns a unique identifier that encodes at, so the sweep can tell
// how old a leftover is. The random tail keeps parallel tests and concurrent
// test processes from colliding.
func newName(at time.Time) (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("random name: %w", err)
	}
	name := fmt.Sprintf("%s%016x%s", namePrefix, at.UnixNano(), hex.EncodeToString(raw))
	if !identifierPattern.MatchString(name) {
		return "", fmt.Errorf("generated name %q is not a valid identifier", name)
	}
	return name, nil
}

// Admin returns a small pool shared by the whole test process, connected to
// the database named in URL. The first call also sweeps stale leftovers of
// interrupted runs. The pool is never closed explicitly: it lives until the
// process exits, because tests of any package may still need it during
// cleanup and Go offers no hook that runs after the last cleanup.
func Admin(t testing.TB) *sql.DB {
	t.Helper()
	adminOnce.Do(func() {
		adminConn, adminErr = connect(URL())
		if adminErr != nil {
			return
		}
		adminConn.SetMaxOpenConns(2)
		if err := sweep(adminConn, time.Now().Add(-staleAfter)); err != nil {
			// A failed sweep must not block tests; leftovers are only clutter.
			fmt.Fprintf(os.Stderr, "dbtest: sweep: %v\n", err)
		}
	})
	if adminErr != nil {
		t.Fatalf("%v", adminErr)
	}
	return adminConn
}

// sweep drops generated databases and schemas created before cutoff. Objects
// whose name does not match the generated format are left alone.
func sweep(admin *sql.DB, cutoff time.Time) error {
	ctx := context.Background()
	stale := func(name string) bool {
		match := namePattern.FindStringSubmatch(name)
		if match == nil {
			return false
		}
		nanos, err := strconv.ParseInt(match[1], 16, 64)
		return err == nil && time.Unix(0, nanos).Before(cutoff)
	}

	for _, target := range []struct {
		query string
		drop  string
	}{
		{"SELECT datname FROM pg_database WHERE datname LIKE 'kernel\\_test\\_%'", "DROP DATABASE IF EXISTS %s WITH (FORCE)"},
		{"SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'kernel\\_test\\_%'", "DROP SCHEMA IF EXISTS %s CASCADE"},
	} {
		rows, err := admin.QueryContext(ctx, target.query)
		if err != nil {
			return fmt.Errorf("list leftovers: %w", err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return fmt.Errorf("scan leftover: %w", err)
			}
			if stale(name) {
				names = append(names, name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("list leftovers: %w", err)
		}
		for _, name := range names {
			if _, err := admin.ExecContext(ctx, fmt.Sprintf(target.drop, pgx.Identifier{name}.Sanitize())); err != nil {
				return fmt.Errorf("drop leftover %s: %w", name, err)
			}
		}
	}
	return nil
}

// Schema creates an empty schema "kernel_test_<id>" in the shared database,
// drops it on cleanup and returns a pool and the name. It runs no migrations;
// it is for tests that choose their own schema, such as the migration tests.
func Schema(t testing.TB) (conn *sql.DB, name string) {
	t.Helper()
	admin := Admin(t)
	name, err := newName(time.Now())
	if err != nil {
		t.Fatalf("%v", err)
	}
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec("CREATE SCHEMA " + quoted); err != nil {
		t.Fatalf("create schema %s: %v", name, err)
	}
	conn, err = connect(URL())
	if err != nil {
		// The schema exists already, so register its removal first.
		t.Cleanup(func() { dropSchema(t, admin, name) })
		t.Fatalf("%v", err)
	}
	conn.SetMaxOpenConns(4)
	// Cleanups run last in, first out: close the pool, then drop the schema.
	t.Cleanup(func() { dropSchema(t, admin, name) })
	t.Cleanup(func() { conn.Close() })
	return conn, name
}

func dropSchema(t testing.TB, admin *sql.DB, name string) {
	t.Helper()
	if _, err := admin.Exec("DROP SCHEMA IF EXISTS " + pgx.Identifier{name}.Sanitize() + " CASCADE"); err != nil {
		t.Errorf("drop schema %s: %v", name, err)
	}
}

// Database creates a database "kernel_test_<id>", applies the kernel
// migrations into the schema "kernel" and returns a pool on it. Cleanup closes
// the pool and drops the database.
func Database(t testing.TB) *sql.DB {
	t.Helper()
	return DatabaseWith(t, db.KernelMigrations())
}

// DatabaseWith is Database with explicit migration files.
func DatabaseWith(t testing.TB, files fs.FS) *sql.DB {
	t.Helper()
	admin := Admin(t)
	name, err := newName(time.Now())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := admin.Exec("CREATE DATABASE " + pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})

	target, err := url.Parse(URL())
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	target.Path = "/" + name
	conn, err := connect(target.String())
	if err != nil {
		t.Fatalf("%v", err)
	}
	conn.SetMaxOpenConns(4)
	// Registered after the drop, so it runs first: connections must be closed
	// before the database can be removed cleanly.
	t.Cleanup(func() { conn.Close() })

	if err := db.Migrate(context.Background(), conn, db.Config{Schema: "kernel", Files: files}); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	return conn
}

// APIPool returns a pool on the database of admin whose every connection runs
// as the limited API role, so row level security applies to it as it does in
// production. admin stays the superuser used only to prepare and inspect
// data: it sees every space because superusers bypass RLS. Cleanup closes the
// pool before the database is dropped.
func APIPool(t testing.TB, admin *sql.DB) *sql.DB {
	t.Helper()
	var name string
	if err := admin.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		t.Fatalf("current database: %v", err)
	}
	target, err := url.Parse(URL())
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	target.Path = "/" + name
	config, err := pgx.ParseConfig(target.String())
	if err != nil {
		t.Fatalf("parse connection config: %v", err)
	}
	// SET ROLE needs no login of the limited role, only being a member of it, which
	// the administrator has as a superuser. B6 tests a real login.
	connector := stdlib.GetConnector(*config, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{db.APIRole}.Sanitize())
		return err
	}))
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(4)
	// Registered after the database drop of Database, so it runs before it.
	t.Cleanup(func() { pool.Close() })
	return pool
}
