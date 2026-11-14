// Command migrate applies the kernel migrations to the database named by
// DATABASE_URL and, when API_DB_PASSWORD is set, lets the API role log in.
// Use a direct connection or a session-mode pooler, never a
// transaction-mode pooler (ADR-0019).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/nexusthrive/nexusthrive/kernel/db"
)

// kernelSchema is the only schema the kernel writes to (ADR-0010).
const kernelSchema = "kernel"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is empty")
	}
	conn, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()

	if err := db.Migrate(ctx, conn, db.Config{Schema: kernelSchema}); err != nil {
		return err
	}
	// The API role password comes from the environment, never from a migration.
	if err := db.SetAPIRolePassword(ctx, conn, os.Getenv("API_DB_PASSWORD")); err != nil {
		return err
	}
	fmt.Println("migrations applied to schema", kernelSchema)
	return nil
}
