package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// APIRole is the database role the API connects as. Migration 00001 creates it
// without superuser, BYPASSRLS or object ownership (ADR-0027).
const APIRole = "nexusthrive_api"

// Executor is what SetAPIRolePassword needs from a connection; *sql.DB and
// *sql.Tx both satisfy it, which lets tests run it inside a rolled back
// transaction.
type Executor interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SetAPIRolePassword lets APIRole log in with password. The password is set
// out of band, never in a migration, so it stays out of the repository. An
// empty password leaves the role as it is, unable to log in.
//
// DDL does not accept bind parameters, so the literal is quoted by the server
// with quote_literal and then embedded in the statement.
func SetAPIRolePassword(ctx context.Context, conn Executor, password string) error {
	if password == "" {
		return nil
	}
	var literal string
	if err := conn.QueryRowContext(ctx, "SELECT quote_literal($1::text)", password).Scan(&literal); err != nil {
		return fmt.Errorf("quote api role password: %w", err)
	}
	stmt := "ALTER ROLE " + pgx.Identifier{APIRole}.Sanitize() + " LOGIN PASSWORD " + literal
	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("set api role password: %w", err)
	}
	return nil
}
