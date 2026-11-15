// Command api runs the NexusThrive kernel with the platform layer and the
// registered distributions compiled into a single binary. At this stage it
// serves the kernel gateway only.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/gateway"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("api stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dev := os.Getenv("NEXUSTHRIVE_DEV") == "1"
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is empty")
	}
	pool, err := openPool(url, dev)
	if err != nil {
		return err
	}
	defer pool.Close()

	auth, err := capability.NewAuthorizer(model.KernelCatalog(), model.SystemClock{})
	if err != nil {
		return fmt.Errorf("build authorizer: %w", err)
	}
	engine, err := execution.NewEngine(pool, auth, gateway.Operations()...)
	if err != nil {
		return fmt.Errorf("build engine: %w", err)
	}

	var handler http.Handler
	if dev {
		handler, err = devServer(engine, log)
	} else {
		handler, err = clerkServer(engine, log)
	}
	if err != nil {
		return err
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("api listening", "addr", addr, "dev", dev)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openPool opens the pool of the API. In development the connection is made by a
// superuser and every session switches to the limited API role, so row level
// security applies as it does in production; elsewhere the URL must already log
// in as that role.
func openPool(url string, dev bool) (*sql.DB, error) {
	if !dev {
		return sql.Open("pgx", url)
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	return sql.OpenDB(stdlib.GetConnector(*cfg, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{db.APIRole}.Sanitize())
		return err
	}))), nil
}

// devServer serves the console page and signs tokens with a key of its own.
func devServer(engine *execution.Engine, log *slog.Logger) (http.Handler, error) {
	issuer, err := gateway.NewDevIssuer("http://nexusthrive.dev")
	if err != nil {
		return nil, err
	}
	verifier, err := gateway.NewVerifier(issuer.Keys(), gateway.VerifierConfig{Issuer: issuer.Issuer})
	if err != nil {
		return nil, err
	}
	space, subject := os.Getenv("DEMO_SPACE_ID"), os.Getenv("DEMO_SUBJECT")
	if space == "" || subject == "" {
		return nil, errors.New("development mode needs DEMO_SPACE_ID and DEMO_SUBJECT: run the seed command first")
	}
	return gateway.NewServer(engine, verifier, &gateway.Dev{Issuer: issuer, SpaceID: space, Subject: subject, Name: os.Getenv("DEMO_NAME")}, log), nil
}

// clerkServer verifies the tokens of Clerk against its published keys.
func clerkServer(engine *execution.Engine, log *slog.Logger) (http.Handler, error) {
	issuer, jwks := os.Getenv("CLERK_ISSUER"), os.Getenv("CLERK_JWKS_URL")
	if issuer == "" || jwks == "" {
		return nil, errors.New("CLERK_ISSUER and CLERK_JWKS_URL are required")
	}
	parties := strings.FieldsFunc(os.Getenv("CLERK_AZP"), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	verifier, err := gateway.NewVerifier(&gateway.RemoteKeys{URL: jwks}, gateway.VerifierConfig{Issuer: issuer, AuthorizedParties: parties, RequireAuthorizedParty: true, Audience: os.Getenv("CLERK_AUDIENCE"), OnReject: func(reason string) { log.Warn("token rejected", "reason", reason) }})
	if err != nil {
		return nil, err
	}
	return gateway.NewServer(engine, verifier, nil, log), nil
}
