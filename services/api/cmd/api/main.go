// Command api runs the NexusThrive kernel with the platform layer and the
// registered distributions compiled into a single binary. At this stage it
// serves the kernel gateway only.
package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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
	"github.com/nexusthrive/nexusthrive/services/api/internal/scheduler"
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
	var operations []execution.Operation
	if dev {
		operations = gateway.Operations()
	}
	engine, err := execution.NewEngine(pool, auth, operations...)
	if err != nil {
		return fmt.Errorf("build engine: %w", err)
	}

	hooks, err := webhooksFromEnv(engine, dev)
	if err != nil {
		return err
	}
	var handler http.Handler
	if dev {
		handler, err = devServer(engine, log, hooks)
	} else {
		handler, err = clerkServer(engine, log, hooks)
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
	var delivery scheduler.Delivery
	if hooks != nil {
		delivery = hooks
	}
	worker, err := scheduler.New(engine, delivery, log)
	if err != nil {
		return err
	}
	log.Info("api listening", "addr", addr, "dev", dev)
	return runHTTP(ctx, srv, worker)
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
func devServer(engine *execution.Engine, log *slog.Logger, hooks *gateway.WebhookGateway) (http.Handler, error) {
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
	return composeGatewayWithHooks(engine, verifier, &gateway.Dev{Issuer: issuer, SpaceID: space, Subject: subject, Name: os.Getenv("DEMO_NAME")}, hooks, log)
}

// clerkServer verifies the tokens of Clerk against its published keys.
func clerkServer(engine *execution.Engine, log *slog.Logger, hooks *gateway.WebhookGateway) (http.Handler, error) {
	issuer, jwks := os.Getenv("CLERK_ISSUER"), os.Getenv("CLERK_JWKS_URL")
	if issuer == "" || jwks == "" {
		return nil, errors.New("CLERK_ISSUER and CLERK_JWKS_URL are required")
	}
	parties := strings.FieldsFunc(os.Getenv("CLERK_AZP"), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	verifier, err := gateway.NewVerifier(&gateway.RemoteKeys{URL: jwks}, gateway.VerifierConfig{Issuer: issuer, AuthorizedParties: parties, RequireAuthorizedParty: true, Audience: os.Getenv("CLERK_AUDIENCE"), OnReject: func(reason string) { log.Warn("token rejected", "reason", reason) }})
	if err != nil {
		return nil, err
	}
	return composeGatewayWithHooks(engine, verifier, nil, hooks, log)
}

// composeGateway wires verified slug routes while retaining the development cut.
func composeGateway(engine *execution.Engine, verifier *gateway.Verifier, dev *gateway.Dev, log *slog.Logger) (http.Handler, error) {
	hooks, err := webhooksFromEnv(engine, dev != nil)
	if err != nil {
		return nil, err
	}
	return composeGatewayWithHooks(engine, verifier, dev, hooks, log)
}

func composeGatewayWithHooks(engine *execution.Engine, verifier *gateway.Verifier, dev *gateway.Dev, hooks *gateway.WebhookGateway, log *slog.Logger) (http.Handler, error) {
	auth, err := gateway.NewClerkAuthenticator(engine, verifier)
	if err != nil {
		return nil, err
	}
	service, err := gateway.NewConnectServer(engine, auth, gateway.ConnectConfig{Log: log, Webhooks: hooks})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/{slug}/kernel.v1.KernelService/", service)
	legacy := gateway.NewServer(engine, verifier, dev, log)
	mux.Handle("GET /healthz", legacy)
	if dev != nil {
		mux.Handle("/", legacy)
	}
	return mux, nil
}

// webhooksFromEnv requires durable keys whenever the explicit feature is enabled.
func webhooksFromEnv(engine *execution.Engine, local bool) (*gateway.WebhookGateway, error) {
	enabled := os.Getenv("NEXUSTHRIVE_WEBHOOKS_ENABLED")
	if enabled == "" || enabled == "0" {
		return nil, nil
	}
	if enabled != "1" {
		return nil, errors.New("invalid webhook enable flag")
	}
	var encoded map[string]string
	if err := json.Unmarshal([]byte(os.Getenv("WEBHOOK_ENCRYPTION_KEYS")), &encoded); err != nil {
		return nil, errors.New("WEBHOOK_ENCRYPTION_KEYS must contain a JSON key ring")
	}
	keys := make(map[string][]byte, len(encoded))
	for id, value := range encoded {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, errors.New("invalid webhook encryption key encoding")
		}
		keys[id] = key
	}
	ring, err := gateway.NewWebhookKeys(os.Getenv("WEBHOOK_ENCRYPTION_ACTIVE_KEY"), keys)
	if err != nil {
		return nil, err
	}
	return gateway.NewWebhookGateway(engine, ring, local)
}

// backgroundWorker lets the lifecycle wait for shutdown without exposing any state access.
type backgroundWorker interface{ Run(context.Context) }

func runHTTP(parent context.Context, srv *http.Server, worker backgroundWorker) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	workerDone, httpDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(workerDone); worker.Run(ctx) }()
	go func() {
		defer close(httpDone)
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = srv.Shutdown(shutdown)
	}()
	serveErr := srv.ListenAndServe()
	cancel()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for workerDone != nil || httpDone != nil {
		select {
		case <-workerDone:
			workerDone = nil
		case <-httpDone:
			httpDone = nil
		case <-timeout.C:
			return errors.New("API shutdown exceeded ten seconds")
		}
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve API: %w", serveErr)
	}
	return nil
}
