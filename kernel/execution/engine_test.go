package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestRunChecksBeforeHandler guards that a denied call never reaches the
// handler, and that the same call with the permission does.
func TestRunChecksBeforeHandler(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	allowed := w.writer(w.spaceA, "allowed")
	onlyOne := w.holder(w.spaceA, "only_one", model.PermissionExchangeWrite)
	var ran steps

	for name, actor := range map[string]model.Actor{
		"no grant":            w.person(w.spaceA, "nobody"),
		"one of two required": onlyOne,
		"null actor":          {},
		"person without one":  {Kind: model.ActorPerson},
		"unknown kind":        {Kind: "robot", ParticipantID: allowed.ParticipantID},
	} {
		err := w.engine.Run(context.Background(), call(actor, w.spaceA), moneyOperation.Name, f.handler(t, "k-"+name, &ran, nil))
		requireCode(t, name, err, model.CodePermissionDenied)
	}
	if ran.calls != 0 {
		t.Errorf("the handler ran %d times for denied calls, want 0", ran.calls)
	}
	if err := w.engine.Run(context.Background(), call(allowed, w.spaceA), moneyOperation.Name, f.handler(t, "k-control", &ran, nil)); err != nil {
		t.Fatalf("control: %v", err)
	}
	if ran.calls != 1 {
		t.Errorf("the handler ran %d times after the control, want 1", ran.calls)
	}
}

// TestRunKeepsCheckLocksUntilCommit proves that Check and the operation share
// one transaction: while the handler runs, a suspension of the space and a
// revocation of the grant, both from another connection, wait on the locks
// that Check took and give up with lock_not_available. Once the operation
// commits, the same statements succeed.
func TestRunKeepsCheckLocksUntilCommit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.person(w.spaceA, "holder")
	grant := w.grantRole(w.spaceA, holder, "holder_role", nil, model.PermissionExchangeWrite, model.PermissionCommitmentWrite)

	conn, err := w.admin.Conn(context.Background())
	if err != nil {
		t.Fatalf("admin connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `SET lock_timeout = '200ms'`); err != nil {
		t.Fatalf("set lock_timeout: %v", err)
	}
	suspend := func() error {
		_, err := conn.ExecContext(context.Background(), `UPDATE kernel.spaces SET status = 'suspended' WHERE id = $1`, w.spaceA)
		return err
	}
	revoke := func() error {
		_, err := conn.ExecContext(context.Background(), `UPDATE kernel.grants SET revoked_at = now() WHERE id = $1`, grant)
		return err
	}
	lockNotAvailable := func(err error) bool {
		var pgErr *pgconn.PgError
		return errors.As(err, &pgErr) && pgErr.Code == "55P03"
	}

	var ran steps
	inner := f.handler(t, "k-locks", &ran, nil)
	err = w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		if err := suspend(); !lockNotAvailable(err) {
			t.Errorf("suspending the space while the operation runs: error = %v, want lock_not_available", err)
		}
		if err := revoke(); !lockNotAvailable(err) {
			t.Errorf("revoking the grant while the operation runs: error = %v, want lock_not_available", err)
		}
		return inner(ctx, tx)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := suspend(); err != nil {
		t.Errorf("suspending after the commit: %v", err)
	}
	if err := revoke(); err != nil {
		t.Errorf("revoking after the commit: %v", err)
	}
}

// TestRunValidatesCall guards the checks that happen before anything opens: a
// malformed request id, a missing handler, an unknown operation and a bad
// space are invalid_input and the handler never runs.
func TestRunValidatesCall(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	var ran steps
	handler := f.handler(t, "k-call", &ran, nil)

	invalid := map[string]struct {
		call Call
		name string
		fn   Handler
	}{
		"empty request id":          {Call{Actor: holder, SpaceID: w.spaceA, RequestID: ""}, moneyOperation.Name, handler},
		"blank request id":          {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "   "}, moneyOperation.Name, handler},
		"request id of 129":         {Call{Actor: holder, SpaceID: w.spaceA, RequestID: strings.Repeat("a", 129)}, moneyOperation.Name, handler},
		"request id with a newline": {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "abc\ndef"}, moneyOperation.Name, handler},
		"request id ending in one":  {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "abc\n"}, moneyOperation.Name, handler},
		"request id with a space":   {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "abc def"}, moneyOperation.Name, handler},
		"request id with a quote":   {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "abc'def"}, moneyOperation.Name, handler},
		"request id not ASCII":      {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "abcé"}, moneyOperation.Name, handler},
		"empty space":               {Call{Actor: holder, SpaceID: "", RequestID: "r1"}, moneyOperation.Name, handler},
		"nil space":                 {Call{Actor: holder, SpaceID: uuid.Nil.String(), RequestID: "r1"}, moneyOperation.Name, handler},
		"malformed space":           {Call{Actor: holder, SpaceID: "nope", RequestID: "r1"}, moneyOperation.Name, handler},
		"nil handler":               {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "r1"}, moneyOperation.Name, nil},
		"unknown operation":         {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "r1"}, "test.unknown", handler},
		"empty operation":           {Call{Actor: holder, SpaceID: w.spaceA, RequestID: "r1"}, "", handler},
	}
	for name, c := range invalid {
		requireCode(t, name, w.engine.Run(context.Background(), c.call, c.name, c.fn), model.CodeInvalidInput)
	}
	if ran.calls != 0 {
		t.Errorf("the handler ran %d times for invalid calls, want 0", ran.calls)
	}

	// Controls: the longest request id and every allowed character pass.
	for i, id := range []string{strings.Repeat("a", 128), "A.b_c:d-1", "0", NewRequestID()} {
		err := w.engine.Run(context.Background(), Call{Actor: holder, SpaceID: w.spaceA, RequestID: id}, moneyOperation.Name,
			f.handler(t, fmt.Sprintf("k-valid-%d", i), &ran, nil))
		if err != nil {
			t.Errorf("request id %q: %v", id, err)
		}
	}
}

// TestRunStampsActorAndRequestID guards that the actor and the request id of
// the call, and nothing else, mark what the operation writes.
func TestRunStampsActorAndRequestID(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	var ran steps
	c := Call{Actor: holder, SpaceID: w.spaceA, RequestID: "request-A.1"}

	if err := w.engine.Run(context.Background(), c, moneyOperation.Name, f.handler(t, "k-stamp", &ran, nil)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var actor, kind, requestID string
	if err := w.admin.QueryRow(`SELECT actor_participant_id::text, actor_kind, request_id FROM kernel.events WHERE type = 'test.exchange_recorded'`).
		Scan(&actor, &kind, &requestID); err != nil {
		t.Fatalf("read the event: %v", err)
	}
	if actor != holder.ParticipantID || kind != string(model.ActorPerson) || requestID != "request-A.1" {
		t.Errorf("event actor = %s %s, request id = %s; want the person %s and request-A.1", kind, actor, requestID, holder.ParticipantID)
	}
	if got := w.count(`SELECT count(*) FROM kernel.exchanges WHERE actor_participant_id = $1`, holder.ParticipantID); got != 1 {
		t.Errorf("exchanges written by the actor = %d, want 1", got)
	}
	if got := w.count(`SELECT count(*) FROM kernel.commitments WHERE created_by_participant_id = $1`, holder.ParticipantID); got != 1 {
		t.Errorf("commitments created by the actor = %d, want 1", got)
	}

	var seen Call
	err := w.engine.Run(context.Background(), c, moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		seen = Call{Actor: tx.Actor(), SpaceID: tx.SpaceID(), RequestID: tx.RequestID()}
		return nil
	})
	if err != nil || seen != c {
		t.Errorf("Tx accessors = %+v (%v), want %+v", seen, err, c)
	}

	// The space id is canonicalized: the Tx and the database see the same
	// lowercase form whatever way the caller wrote the UUID.
	upper := c
	upper.SpaceID = strings.ToUpper(w.spaceA)
	err = w.engine.Run(context.Background(), upper, moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		seen = Call{Actor: tx.Actor(), SpaceID: tx.SpaceID(), RequestID: tx.RequestID()}
		return nil
	})
	if err != nil || seen != c {
		t.Errorf("Tx accessors for an uppercase space id = %+v (%v), want %+v", seen, err, c)
	}
}

// TestRunSwallowedDatabaseErrorCannotCommit guards that a handler that hides a
// database error and returns nil does not get its writes committed: the
// transaction is aborted and the commit rolls back.
func TestRunSwallowedDatabaseErrorCannotCommit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	var ran steps
	before := w.snapshot()

	inner := f.handler(t, "k-swallow", &ran, nil)
	err := w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		if err := inner(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.conn.ExecContext(ctx, `SELECT 1/0`); err == nil {
			t.Error("the failing statement did not fail")
		}
		return nil
	})
	if err == nil {
		t.Fatal("Run committed an aborted transaction")
	}
	if !errors.Is(err, pgx.ErrTxCommitRollback) {
		t.Errorf("error = %v, want the commit rollback of the driver", err)
	}
	if !ran.event {
		t.Error("the writes before the swallowed error did not run, so the test proves nothing")
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Errorf("the rolled back operation changed %v", changed)
	}
}

// TestRunKeepsPrivilegeErrorsUntyped guards that a missing database privilege
// (42501) is never disguised: it comes out untyped, with the database error
// reachable through errors.As.
func TestRunKeepsPrivilegeErrorsUntyped(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	holder := w.writer(w.spaceA, "holder")

	err := w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		_, err := tx.conn.ExecContext(ctx, `DELETE FROM kernel.resources`)
		return err
	})
	if err == nil {
		t.Fatal("the API role deleted resources")
	}
	if code, typed := model.CodeOf(err); typed {
		t.Errorf("a privilege error became %s: %v", code, err)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Errorf("error = %v, want the 42501 of the database", err)
	}
}

// TestRunMapsRestrictViolationToPermissionDenied guards follow-up 50 at the
// engine: the database refusal of kernel.capability.write in a space that is
// not a platform space is permission_denied.
func TestRunMapsRestrictViolationToPermissionDenied(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	holder := w.writer(w.spaceA, "holder")
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}

	err = w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		_, err := tx.conn.ExecContext(ctx,
			`INSERT INTO kernel.roles (id, space_id, key, name, permissions) VALUES ($1, kernel.current_space_id(), 'cap', 'Cap', ARRAY['kernel.capability.write'])`,
			id.String())
		return err
	})
	requireCode(t, "capability.write outside a platform space", err, model.CodePermissionDenied)
	if got := w.count(`SELECT count(*) FROM kernel.roles WHERE key = 'cap'`); got != 0 {
		t.Errorf("roles = %d, want 0", got)
	}
}

// TestRunDoesNotWrapTypedErrors guards that a typed error of the handler comes
// out as it went in, wrapped or not, and that an untyped one is wrapped with
// the operation name and keeps its cause.
func TestRunDoesNotWrapTypedErrors(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	holder := w.writer(w.spaceA, "holder")
	run := func(fn Handler) error {
		return w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, fn)
	}

	typed := model.NewError(model.CodeNotFound, "not here", nil)
	if err := run(func(context.Context, *Tx) error { return typed }); err != error(typed) {
		t.Errorf("typed error = %v, want the same error", err)
	}
	wrapped := fmt.Errorf("while working: %w", typed)
	if err := run(func(context.Context, *Tx) error { return wrapped }); err != wrapped {
		t.Errorf("wrapped typed error = %v, want the same error", err)
	}

	cause := errors.New("boom")
	err := run(func(context.Context, *Tx) error { return cause })
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), moneyOperation.Name) {
		t.Errorf("untyped error = %v, want it wrapped with the operation name and the cause", err)
	}
	if _, isTyped := model.CodeOf(err); isTyped {
		t.Error("an untyped error became typed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = w.engine.Run(ctx, call(holder, w.spaceA), moneyOperation.Name, func(context.Context, *Tx) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context: error = %v, want context.Canceled", err)
	}
	if _, isTyped := model.CodeOf(err); isTyped {
		t.Errorf("a canceled context became typed: %v", err)
	}
}

// TestRunTranslatesCommitFailure guards the deferred checks that fail when the
// transaction commits: the handler writes raw rows that break the rule and
// returns nil, the commit fails, the error is rule_violation and nothing stays.
func TestRunTranslatesCommitFailure(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	before := w.snapshot()

	exchangeID := func() string {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatalf("uuid: %v", err)
		}
		return id.String()
	}
	insertExchange := func(ctx context.Context, tx *Tx, id string) error {
		_, err := tx.conn.ExecContext(ctx,
			`INSERT INTO kernel.exchanges (id, space_id, kind, idempotency_key, actor_participant_id)
			 VALUES ($1, kernel.current_space_id(), 'test.exchange', $2, $3)`, id, "commit-"+id, holder.ParticipantID)
		return err
	}
	insertEntry := func(ctx context.Context, tx *Tx, exchange, account, value string) error {
		_, err := tx.conn.ExecContext(ctx,
			`INSERT INTO kernel.entries (id, exchange_id, space_id, account_id, resource_id, amount)
			 VALUES ($1, $2, kernel.current_space_id(), $3, $4, $5::numeric)`, exchangeID(), exchange, account, f.resource, value)
		return err
	}

	cases := []struct {
		name string
		fn   Handler
	}{
		{"entries that do not add up to zero", func(ctx context.Context, tx *Tx) error {
			id := exchangeID()
			if err := insertExchange(ctx, tx, id); err != nil {
				return err
			}
			if err := insertEntry(ctx, tx, id, f.to, "10.00"); err != nil {
				return err
			}
			return insertEntry(ctx, tx, id, f.from, "-5.00")
		}},
		{"an exchange with no entries", func(ctx context.Context, tx *Tx) error {
			return insertExchange(ctx, tx, exchangeID())
		}},
		{"an expired commitment with no event", func(ctx context.Context, tx *Tx) error {
			now := time.Now().UTC().Truncate(time.Microsecond)
			created, err := state.AppendCommitment(ctx, tx.conn, tx.Actor(), model.NewCommitment{
				Kind: "test.commitment", DebtorParticipantID: f.debtor, CreditorParticipantID: f.creditor,
				ResourceID: f.resource, Amount: amount(t, "10.00"), StartsAt: now.Add(-48 * time.Hour), DueAt: now.Add(-24 * time.Hour),
			})
			if err != nil {
				return err
			}
			_, err = tx.conn.ExecContext(ctx, `UPDATE kernel.timers SET status = 'fired' WHERE commitment_id = $1`, created.ID)
			return err
		}},
	}
	for _, c := range cases {
		err := w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, c.fn)
		requireCode(t, c.name, err, model.CodeRuleViolation)
		if changed := w.changed(before); len(changed) != 0 {
			t.Errorf("%s: the failed commit changed %v", c.name, changed)
		}
	}
}

// TestRunFixesSpaceBeforeAnyQuery guards criterion 8: the first thing the
// handler sees is the space of the call, and a raw read of a table sees only
// the rows of that space.
func TestRunFixesSpaceBeforeAnyQuery(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	holder := w.writer(w.spaceA, "holder")
	w.person(w.spaceB, "elsewhere")
	w.person(w.spaceB, "elsewhere two")

	err := w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
		var setting string
		if err := tx.conn.QueryRowContext(ctx, `SELECT current_setting('kernel.space_id')`).Scan(&setting); err != nil {
			return err
		}
		if setting != w.spaceA {
			t.Errorf("kernel.space_id = %q, want %q", setting, w.spaceA)
		}
		var visible, foreign int
		if err := tx.conn.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE space_id <> $1::uuid) FROM kernel.participants`, w.spaceA).Scan(&visible, &foreign); err != nil {
			return err
		}
		if foreign != 0 || visible == 0 {
			t.Errorf("participants visible = %d, of another space = %d", visible, foreign)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestRunLeavesNoSpaceSettingOrOpenTransaction guards that nothing outlives a
// call: after a success, an error and a panic, on a pool of one connection,
// the connection carries no space and no read-only mode, is back in the pool
// and serves the next call at once.
func TestRunLeavesNoSpaceSettingOrOpenTransaction(t *testing.T) {
	t.Parallel()
	read := Operation{Name: "test.read_ledger", Permissions: []string{model.PermissionLedgerRead}, Risk: model.RiskRead}
	w := newWorld(t, moneyOperation, read)
	f := w.ledger(w.spaceA)
	holder := w.holder(w.spaceA, "holder", model.PermissionExchangeWrite, model.PermissionCommitmentWrite, model.PermissionLedgerRead)
	w.api.SetMaxOpenConns(1)

	inspect := func(label string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var setting sql.NullString
		var readOnly string
		if err := w.api.QueryRowContext(ctx, `SELECT current_setting('kernel.space_id', true), current_setting('transaction_read_only')`).Scan(&setting, &readOnly); err != nil {
			t.Fatalf("%s: the pool did not serve the next statement: %v", label, err)
		}
		if setting.String != "" {
			t.Errorf("%s: kernel.space_id = %q after the call, want empty", label, setting.String)
		}
		if readOnly != "off" {
			t.Errorf("%s: transaction_read_only = %s, want off", label, readOnly)
		}
		if stats := w.api.Stats(); stats.InUse != 0 {
			t.Errorf("%s: %d connections in use after the call", label, stats.InUse)
		}
		start := time.Now()
		var ran steps
		err := w.engine.Run(ctx, call(holder, w.spaceA), moneyOperation.Name, f.handler(t, "k-after-"+label, &ran, nil))
		if err != nil || time.Since(start) > 2*time.Second {
			t.Errorf("%s: a call after it failed or was slow: %v", label, err)
		}
	}

	var ran steps
	if err := w.engine.Run(context.Background(), call(holder, w.spaceA), read.Name, func(context.Context, *Tx) error { return nil }); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	inspect("read")
	if err := w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, f.handler(t, "k-ok", &ran, nil)); err != nil {
		t.Fatalf("success: %v", err)
	}
	inspect("success")
	err := w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(context.Context, *Tx) error { return errors.New("failed") })
	if err == nil {
		t.Fatal("the failing handler succeeded")
	}
	inspect("error")
	err = w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(context.Context, *Tx) error { panic("boom") })
	var panicked *PanicError
	if !errors.As(err, &panicked) {
		t.Fatalf("panic: error = %v, want a PanicError", err)
	}
	inspect("panic")
}

// TestRunRecoversPanic guards criterion 10: a panic of any value rolls the
// transaction back, comes out as an untyped *PanicError with the operation
// and the stack, never inherits a code from the value and leaves the pool
// usable.
func TestRunRecoversPanic(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := w.ledger(w.spaceA)
	holder := w.writer(w.spaceA, "holder")
	before := w.snapshot()

	cases := []struct {
		name  string
		value any
	}{
		{"a string", "x"},
		{"an error", errors.New("an error")},
		{"a typed error", model.NewError(model.CodePermissionDenied, "forged denial", nil)},
		{"nil", nil},
	}
	for i, c := range cases {
		var ran steps
		inner := f.handler(t, fmt.Sprintf("k-panic-%d", i), &ran, nil)
		err := w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, func(ctx context.Context, tx *Tx) error {
			if err := inner(ctx, tx); err != nil {
				return err
			}
			panic(c.value)
		})
		var panicked *PanicError
		if !errors.As(err, &panicked) {
			t.Fatalf("%s: error = %v, want a PanicError", c.name, err)
		}
		if panicked.Operation != moneyOperation.Name || len(panicked.Stack) == 0 {
			t.Errorf("%s: operation = %q, stack of %d bytes", c.name, panicked.Operation, len(panicked.Stack))
		}
		if code, typed := model.CodeOf(err); typed {
			t.Errorf("%s: the panic inherited the code %s", c.name, code)
		}
		message := err.Error()
		if !strings.Contains(message, moneyOperation.Name) || strings.Contains(message, "goroutine") {
			t.Errorf("%s: message %q must name the operation and carry no stack", c.name, message)
		}
		if !ran.event {
			t.Errorf("%s: the writes before the panic did not run", c.name)
		}
		if changed := w.changed(before); len(changed) != 0 {
			t.Errorf("%s: the panic left rows behind: %v", c.name, changed)
		}
	}

	var ran steps
	if err := w.engine.Run(context.Background(), call(holder, w.spaceA), moneyOperation.Name, f.handler(t, "k-after-panics", &ran, nil)); err != nil {
		t.Errorf("the engine is not usable after the panics: %v", err)
	}
}

// TestPanicErrorHidesTheStack guards the message of a PanicError.
func TestPanicErrorHidesTheStack(t *testing.T) {
	t.Parallel()
	err := &PanicError{Operation: "test.op", Value: "x", Stack: []byte("goroutine 1 [running]")}
	if got := err.Error(); !strings.Contains(got, "test.op") || !strings.Contains(got, "x") || strings.Contains(got, "goroutine") {
		t.Errorf("Error() = %q", got)
	}
	if errors.Unwrap(err) != nil {
		t.Error("a PanicError unwraps to its value")
	}
}

// TestNewRequestID guards that the generated id is a UUID v7 that Run accepts.
func TestNewRequestID(t *testing.T) {
	t.Parallel()
	a, b := NewRequestID(), NewRequestID()
	parsed, err := uuid.Parse(a)
	if err != nil || parsed.Version() != 7 || a == b {
		t.Errorf("NewRequestID = %q, %q (%v), want two distinct UUID v7", a, b, err)
	}
	if !requestIDPattern.MatchString(a) {
		t.Errorf("%q does not match the accepted pattern", a)
	}
}
