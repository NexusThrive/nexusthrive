package execution

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// requestIDPattern is what a request id may look like. It is narrow on
// purpose: the id reaches logs and the events table, and a value with line
// breaks or control characters would forge log lines. Go's $ matches only at
// the end of the text, so a trailing newline does not pass.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// Call is who calls an operation, in which space and for which request. The
// engine trusts the actor: whoever builds a Call must have verified it first
// (ADR-0035, precondition of the gateways).
type Call struct {
	Actor     model.Actor
	SpaceID   string
	RequestID string
}

// Handler is the body of an operation. It runs with every permission already
// verified, in the transaction of the call, and writes only through the Tx. A
// returned error rolls everything back.
type Handler func(ctx context.Context, tx *Tx) error

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
// id of the accepted shape, a handler, a registered operation, a space that is
// a non-nil UUID); a panic is recovered into a *PanicError; the transaction
// opens with the space fixed; a read operation turns the transaction read
// only; every permission of the operation is verified with Authorizer.Check,
// the capability with the first, still before any write; the handler runs; the
// Tx is closed, whether the handler returned or failed; and the transaction
// commits. Check shares the transaction, so the locks it takes last until the
// commit and a revocation or a suspension that races with the operation waits.
//
// The errors of the handler and of the database come out classified (see
// classify): typed errors untouched, a panic as *PanicError, and nothing is
// retried.
func (e *Engine) Run(ctx context.Context, call Call, name string, fn Handler) error {
	// The operations of the kernel run only through the entry points of the
	// engine, with a handler the engine fixes. Any name that reads as the
	// kernel namespace once case and blanks are ignored is refused with the
	// same answer as an unregistered one, before anything opens.
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), kernelNamespace) {
		return model.NewError(model.CodeInvalidInput, fmt.Sprintf("operation %q is not registered", name), nil)
	}
	return e.run(ctx, call, name, fn)
}

// run is Run without the refusal of kernel names. It is not exported: only the
// built-in entry points of the engine reach it.
func (e *Engine) run(ctx context.Context, call Call, name string, fn Handler) (err error) {
	op, canonical, err := e.prepare(call, name, fn)
	if err != nil {
		return err
	}
	call.SpaceID = canonical

	// The recovery is outside InSpace on purpose: while a panic unwinds, the
	// deferred rollback of the transaction runs first, so nothing is left open
	// by the time this function turns the panic into an error.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Operation: name, Value: recovered, Stack: debug.Stack()}
		}
	}()
	err = state.InSpace(ctx, e.pool, call.SpaceID, func(ctx context.Context, conn *sql.Tx) error {
		return e.execute(ctx, conn, call, op, fn)
	})
	return classify(name, err)
}

// prepare validates the call and returns the operation and the canonical form
// of the space id. Nothing has been opened yet.
func (e *Engine) prepare(call Call, name string, fn Handler) (Operation, string, error) {
	if !requestIDPattern.MatchString(call.RequestID) {
		return Operation{}, "", model.NewError(model.CodeInvalidInput,
			"the request id must be 1 to 128 characters among letters, digits, dot, underscore, colon and hyphen", nil)
	}
	if fn == nil {
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
	for i, key := range op.Permissions {
		req := capability.Requirement{Permission: key}
		if i == 0 {
			req.Capability = op.Capability
		}
		if err := e.auth.Check(ctx, conn, call.Actor, req); err != nil {
			return err
		}
	}
	tx := &Tx{life: &txLife{}, conn: conn, clock: e.auth.Clock(), op: op, call: call}
	return tx.run(ctx, fn)
}
