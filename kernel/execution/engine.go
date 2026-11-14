package execution

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// requestIDPattern is what a request id may look like. It is narrow on
// purpose: the id reaches logs and the events table, and a value with line
// breaks or control characters would forge log lines. Go's $ matches only at
// the end of the text, so a trailing newline does not pass.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// conflictMessage is the fixed message of idempotency_conflict. It says nothing
// of the stored record, so a caller learns nothing from the refusal.
const conflictMessage = "the idempotency key was used by a different request"

// Call is who calls an operation, in which space and for which request. The
// engine trusts the actor: whoever builds a Call must have verified it first
// (ADR-0035, precondition of the gateways).
type Call struct {
	Actor     model.Actor
	SpaceID   string
	RequestID string
	// IdempotencyKey names the request for RunIdempotent: 1 to 128 characters
	// among letters, digits, dot, underscore, colon and hyphen. Run ignores it.
	IdempotencyKey string
	// Request is the body of the request as the gateway received it, a JSON
	// value. RunIdempotent fingerprints it and does not store it. The gateways
	// of B3 fill it; Run ignores it.
	Request json.RawMessage
}

// Handler is the body of an operation. It runs with every permission already
// verified, in the transaction of the call, and writes only through the Tx. A
// returned error rolls everything back.
type Handler func(ctx context.Context, tx *Tx) error

// ResultHandler is the body of an idempotent operation: a Handler that also
// returns the response of the operation, a JSON object of at most 64 KiB. The
// engine stores the response with the record and gives the same bytes to every
// repetition of the request.
type ResultHandler func(ctx context.Context, tx *Tx) (json.RawMessage, error)

// Response is what RunIdempotent returns on success.
type Response struct {
	// Body is the response of the first execution, in the form the database
	// stores it, so a repetition returns the same bytes.
	Body json.RawMessage
	// Replayed is true when the body is the stored response of an earlier
	// execution and the handler did not run.
	Replayed bool
}

// Engine runs the registered operations. It is safe for concurrent use: it
// holds no state that changes after NewEngine.
type Engine struct {
	pool *sql.DB
	auth *capability.Authorizer
	ops  map[string]Operation
}

// NewEngine registers the operations of the kernel and the ones given, and
// validates all of them against the catalog of the authorizer. It fails with
// invalid_input, and returns no engine, when any declaration is invalid or a
// name repeats: an engine never starts with an operation it cannot guard.
func NewEngine(pool *sql.DB, auth *capability.Authorizer, ops ...Operation) (*Engine, error) {
	if pool == nil {
		return nil, model.NewError(model.CodeInvalidInput, "an engine needs a pool", nil)
	}
	if auth == nil {
		return nil, model.NewError(model.CodeInvalidInput, "an engine needs an authorizer", nil)
	}
	engine := &Engine{pool: pool, auth: auth, ops: map[string]Operation{}}
	register := func(op Operation, kernelOwned bool) error {
		checked, err := validateOperation(auth.Catalog(), op, kernelOwned)
		if err != nil {
			return err
		}
		if _, exists := engine.ops[checked.Name]; exists {
			return invalid(checked.Name, "the name is already registered")
		}
		engine.ops[checked.Name] = checked
		return nil
	}
	for _, op := range builtinOperations() {
		if err := register(op, true); err != nil {
			return nil, fmt.Errorf("register built-in operation: %w", err)
		}
	}
	for _, op := range ops {
		if err := register(op, false); err != nil {
			return nil, err
		}
	}
	return engine, nil
}

// NewRequestID returns a UUID v7 for callers that have no request of their
// own, such as the scheduler of the kernel.
func NewRequestID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// Run executes the operation name for call. In order, each step stopping the
// call when it fails: the call is validated before anything opens (a request
// id of the accepted shape, a handler, a registered operation that is not
// declared idempotent, a space that is a non-nil UUID); a panic is recovered
// into a *PanicError; the transaction opens with the space fixed; a read
// operation turns the transaction read only; every permission of the operation
// is verified with Authorizer.Check, the capability with the first, still
// before any write; the handler runs; the Tx is closed, whether the handler
// returned or failed; and the transaction commits. Check shares the
// transaction, so the locks it takes last until the commit and a revocation or
// a suspension that races with the operation waits.
//
// An operation declared Idempotent is refused with invalid_input: it keeps a
// record and answers repeated requests, which only RunIdempotent does.
//
// The errors of the handler and of the database come out classified (see
// classify): typed errors untouched, a panic as *PanicError, and nothing is
// retried.
func (e *Engine) Run(ctx context.Context, call Call, name string, fn Handler) error {
	// The operations of the kernel run only through the entry points of the
	// engine, with a handler the engine fixes. Any name that reads as the
	// kernel namespace once case and blanks are ignored is refused with the
	// same answer as an unregistered one, before anything opens.
	if isKernelName(name) {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("operation %q is not registered", name), nil)
	}
	return e.run(ctx, call, name, fn)
}

// isKernelName reports whether name reads as the kernel namespace once case and
// blanks are ignored.
func isKernelName(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), kernelNamespace)
}

// run is Run without the refusal of kernel names. It is not exported: only the
// built-in entry points of the engine reach it.
func (e *Engine) run(ctx context.Context, call Call, name string, fn Handler) error {
	op, canonical, err := e.prepare(call, name, fn != nil)
	if err != nil {
		return err
	}
	if op.Idempotent {
		return model.NewError(model.CodeInvalidInput,
			fmt.Sprintf("operation %q is idempotent and runs through RunIdempotent", name), nil)
	}
	call.SpaceID = canonical
	return e.guarded(name, func() error {
		return state.InSpace(ctx, e.pool, call.SpaceID, func(ctx context.Context, conn *sql.Tx) error {
			return e.execute(ctx, conn, call, op, fn)
		})
	})
}

// guarded runs body with a panic recovered into a *PanicError and classifies
// the error it returns. The recovery is outside InSpace on purpose: while a
// panic unwinds, the deferred rollback of the transaction runs first, so
// nothing is left open by the time this function turns the panic into an error.
func (e *Engine) guarded(name string, body func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Operation: name, Value: recovered, Stack: debug.Stack()}
		}
	}()
	return classify(name, body())
}

// RunIdempotent executes the idempotent operation name for call and answers a
// repeated request with the stored response (ADR-0026). In order, each step
// stopping the call when it fails:
//
//  1. Before anything opens: the call is validated as for Run, the operation
//     must be declared Idempotent and must not be a read, the idempotency key
//     must be valid and the request body canonical JSON (invalid_input).
//  2. The transaction opens with the space fixed, and every permission of the
//     operation is verified with Authorizer.Check, so that a caller without
//     permission neither reads a record nor waits for a key.
//  3. The key is locked until the commit and the record in force is read. If
//     there is one with the same fingerprint (operation, actor and body), its
//     response is returned and the handler does not run; with another
//     fingerprint the call fails with idempotency_conflict and writes nothing.
//  4. Otherwise the clock must be a usable one (not the zero time and not more
//     than 5 minutes behind the database, else invalid_input), the handler runs, its response is validated (a JSON object of at
//     most 64 KiB, else invalid_input) and the record is stored in the same
//     transaction. The Response carries the stored form of the response, byte
//     for byte what a repetition returns.
//
// A failure of the handler, a panic or a failed commit leaves no record: the
// same key and body run again. A second call that arrives while the first runs
// waits for the lock, then sees its record or, if it failed, runs itself; the
// context bounds the wait. The exchanges the handler records get the key
// "<execution id>:<n>", derived here, and a key of the handler is refused.
func (e *Engine) RunIdempotent(ctx context.Context, call Call, name string, fn ResultHandler) (Response, error) {
	if isKernelName(name) {
		return Response{}, model.NewError(model.CodeInvalidInput, fmt.Sprintf("operation %q is not registered", name), nil)
	}
	op, canonical, err := e.prepare(call, name, fn != nil)
	if err != nil {
		return Response{}, err
	}
	if !op.Idempotent {
		return Response{}, model.NewError(model.CodeInvalidInput, fmt.Sprintf("operation %q does not declare idempotency", name), nil)
	}
	if op.Risk == model.RiskRead {
		return Response{}, model.NewError(model.CodeInvalidInput, fmt.Sprintf("operation %q only reads and keeps no idempotency record", name), nil)
	}
	if err := model.ValidateIdempotencyKey(call.IdempotencyKey); err != nil {
		return Response{}, err
	}
	body, err := model.CanonicalJSON(call.Request)
	if err != nil {
		return Response{}, err
	}
	call.SpaceID = canonical

	var response Response
	err = e.guarded(name, func() error {
		return state.InSpace(ctx, e.pool, call.SpaceID, func(ctx context.Context, conn *sql.Tx) error {
			var err error
			response, err = e.executeIdempotent(ctx, conn, call, op, body, fn)
			return err
		})
	})
	if err != nil {
		return Response{}, err
	}
	return response, nil
}

// prepare validates the call and returns the operation and the canonical form
// of the space id. Nothing has been opened yet.
func (e *Engine) prepare(call Call, name string, hasHandler bool) (Operation, string, error) {
	if !requestIDPattern.MatchString(call.RequestID) {
		return Operation{}, "", model.NewError(model.CodeInvalidInput,
			"the request id must be 1 to 128 characters among letters, digits, dot, underscore, colon and hyphen", nil)
	}
	if !hasHandler {
		return Operation{}, "", model.NewError(model.CodeInvalidInput, "an operation needs a handler", nil)
	}
	op, registered := e.ops[name]
	if !registered {
		return Operation{}, "", model.NewError(model.CodeInvalidInput, fmt.Sprintf("operation %q is not registered", name), nil)
	}
	space, err := uuid.Parse(call.SpaceID)
	if call.SpaceID == "" || err != nil || space == uuid.Nil {
		return Operation{}, "", model.NewError(model.CodeInvalidInput, "space id must be a non-nil UUID", nil)
	}
	return op, space.String(), nil
}

// check verifies every permission of the operation for the actor of the call,
// the capability with the first, in the transaction of the call.
func (e *Engine) check(ctx context.Context, conn *sql.Tx, call Call, op Operation) error {
	for i, key := range op.Permissions {
		req := capability.Requirement{Permission: key}
		if i == 0 {
			req.Capability = op.Capability
		}
		if err := e.auth.Check(ctx, conn, call.Actor, req); err != nil {
			return err
		}
	}
	return nil
}

// execute is the body of the transaction of a call: the read-only mode, the
// verification of every permission and the handler.
func (e *Engine) execute(ctx context.Context, conn *sql.Tx, call Call, op Operation, fn Handler) error {
	// The role of the API can write in a read operation: the database cannot
	// tell them apart, so the transaction itself is made read only.
	if op.Risk == model.RiskRead {
		if _, err := conn.ExecContext(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return fmt.Errorf("set transaction read only: %w", err)
		}
	}
	if err := e.check(ctx, conn, call, op); err != nil {
		return err
	}
	tx := &Tx{life: &txLife{}, conn: conn, clock: e.auth.Clock(), op: op, call: call}
	return tx.run(ctx, fn)
}

// executeIdempotent is the body of the transaction of an idempotent call, see
// RunIdempotent. body is the canonical request.
func (e *Engine) executeIdempotent(ctx context.Context, conn *sql.Tx, call Call, op Operation, body []byte, fn ResultHandler) (Response, error) {
	// The lock of the key is only useful if the read that follows it sees what
	// the winner committed, so a level above READ COMMITTED is refused.
	if err := state.RequireReadCommitted(ctx, conn); err != nil {
		return Response{}, err
	}
	// The permissions come first, with the locks that Check takes, and the lock
	// of the key after them. A caller that may not run the operation never
	// reads a record and never waits for a key.
	if err := e.check(ctx, conn, call, op); err != nil {
		return Response{}, err
	}
	// A record names a participant, so an actor that stands for none cannot
	// run an idempotent operation.
	if call.Actor.ParticipantID == "" {
		return Response{}, model.NewError(model.CodePermissionDenied, "an idempotent operation needs an actor that stands for a participant", nil)
	}
	hash, err := model.RequestHash(op.Name, call.Actor, body)
	if err != nil {
		return Response{}, err
	}

	if err := state.LockIdempotencyKey(ctx, conn, call.IdempotencyKey); err != nil {
		return Response{}, err
	}
	record, found, err := state.FindIdempotencyRecord(ctx, conn, e.auth.Clock(), call.IdempotencyKey)
	if err != nil {
		return Response{}, err
	}
	if found {
		// The fingerprint covers the operation and the actor, so a record of
		// another operation or another actor is a conflict, not a replay.
		if !bytes.Equal(record.RequestHash[:], hash[:]) {
			return Response{}, model.NewError(model.CodeIdempotencyConflict, conflictMessage, nil)
		}
		return Response{Body: json.RawMessage(record.Response), Replayed: true}, nil
	}

	// A clock that is the zero time or too far behind the database would date
	// the record so that it starts with less than 24 hours, and a repetition
	// would run the handler again. The refusal comes before the handler and
	// before any write, so the skew is seen and never weakens the idempotency
	// in silence. A replay above does not need the clock to be right.
	if err := state.CheckIdempotencyClock(ctx, conn, e.auth.Clock()); err != nil {
		return Response{}, err
	}
	executionID, err := uuid.NewV7()
	if err != nil {
		return Response{}, fmt.Errorf("generate execution id: %w", err)
	}
	tx := &Tx{
		life: &txLife{}, conn: conn, clock: e.auth.Clock(), op: op, call: call,
		idempotent: true, executionID: executionID.String(),
	}
	result, err := tx.runResult(ctx, fn)
	if err != nil {
		return Response{}, err
	}
	if err := model.ValidateIdempotencyResponse(result); err != nil {
		return Response{}, err
	}
	stored, err := state.AppendIdempotencyRecord(ctx, conn, e.auth.Clock(), model.NewIdempotencyRecord{
		ID: executionID.String(), Key: call.IdempotencyKey, Operation: op.Name, Actor: call.Actor, RequestHash: hash, Response: result,
	})
	if err != nil {
		// A handler that hid a database error leaves the transaction aborted.
		// The commit reports it, as it does for Run, and rolls everything back.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "25P02" {
			return Response{}, nil
		}
		return Response{}, err
	}
	return Response{Body: json.RawMessage(stored.Response)}, nil
}
