package laws_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
)

// goCommand runs the go tool in the kernel module against the database at
// databaseURL and returns its combined output.
func goCommand(t *testing.T, module, databaseURL string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = module
	command.Env = append(os.Environ(), "DATABASE_URL="+databaseURL)
	out, err := command.CombinedOutput()
	return string(out), err
}

// TestLawsRunOnEmptyBaseAfterMigrateCommand guards criterion 6 of the closing
// story, the same sequence the CI runs: on an empty database the command
// `go run ./db/cmd/migrate` applies the migrations, and then the suite runs
// against that database. The command is also safe to run twice. The test
// builds the base itself because the suite creates its own databases beside
// it, and it runs one test of the suite as a witness, since a test cannot run
// the whole suite that contains it.
func TestLawsRunOnEmptyBaseAfterMigrateCommand(t *testing.T) {
	t.Parallel()
	admin := dbtest.Admin(t)
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("random name: %v", err)
	}
	name := fmt.Sprintf("kernel_test_%016x%s", time.Now().UnixNano(), hex.EncodeToString(raw))
	if _, err := admin.Exec("CREATE DATABASE " + pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create the empty database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
	target, err := url.Parse(dbtest.URL())
	if err != nil {
		t.Fatalf("parse the database URL: %v", err)
	}
	target.Path = "/" + name
	module, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve the module: %v", err)
	}

	empty, err := sql.Open("pgx", target.String())
	if err != nil {
		t.Fatalf("open the empty database: %v", err)
	}
	defer empty.Close()
	var tables int
	if err := empty.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("the database is not empty: %d tables (%v)", tables, err)
	}

	for round := 1; round <= 2; round++ {
		out, err := goCommand(t, module, target.String(), "run", "./db/cmd/migrate")
		if err != nil || !strings.Contains(out, "migrations applied to schema kernel") {
			t.Fatalf("migrate run %d: %v\n%s", round, err, out)
		}
	}
	if err := empty.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname = 'kernel' AND tablename <> 'goose_db_version'`).Scan(&tables); err != nil || tables < 14 {
		t.Fatalf("after the migration the schema has %d tables (%v), want the whole kernel", tables, err)
	}

	// The witness: a test of the suite that needs the schema, run against the
	// migrated base. The pattern keeps it from running this test again.
	out, err := goCommand(t, module, target.String(), "test", "./laws", "-count=1", "-run", "^TestLawPermissionAPIRoleCannotBypassIsolation$")
	if err != nil {
		t.Fatalf("the suite failed on the migrated base: %v\n%s", err, out)
	}
}
