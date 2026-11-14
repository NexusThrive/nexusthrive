package execution

import (
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// validOperation is the control of every rejection: it registers.
func validOperation() Operation {
	return Operation{
		Name: "test.read_ledger", Permissions: []string{model.PermissionLedgerRead}, Risk: model.RiskRead,
	}
}

// TestNewEngineRejectsInvalidOperations guards criterion 7: the engine refuses
// to start with an operation whose declaration is malformed, and every
// rejection has a valid control that registers, so no case fails for another
// reason.
func TestNewEngineRejectsInvalidOperations(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	extended, err := model.KernelCatalog().Extend("test", []model.PermissionDef{{Key: "test.thing.write", Risk: model.RiskWrite}})
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	extendedAuth, err := capability.NewAuthorizer(extended, w.clock)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}

	valid := func(change func(*Operation)) []Operation {
		op := validOperation()
		change(&op)
		return []Operation{op}
	}
	cases := []struct {
		name string
		ops  []Operation
	}{
		{"empty name", valid(func(o *Operation) { o.Name = "" })},
		{"uppercase name", valid(func(o *Operation) { o.Name = "Test.read" })},
		{"name with one segment", valid(func(o *Operation) { o.Name = "readledger" })},
		{"name with a trailing newline", valid(func(o *Operation) { o.Name = "test.read\n" })},
		{"name with a space", valid(func(o *Operation) { o.Name = "test.read ledger" })},
		{"name with a version-like empty segment", valid(func(o *Operation) { o.Name = "test..read" })},
		{"name under the kernel namespace", valid(func(o *Operation) { o.Name = "kernel.read_ledger" })},
		{"namespace that starts with kernel", valid(func(o *Operation) { o.Name = "kernelx.read_ledger" })},
		{"name of a built-in operation", valid(func(o *Operation) { o.Name = "kernel.timer.fire" })},
		{"duplicate name", []Operation{validOperation(), validOperation()}},
		{"no permissions", valid(func(o *Operation) { o.Permissions = nil })},
		{"repeated permission", valid(func(o *Operation) {
			o.Permissions = []string{model.PermissionLedgerRead, model.PermissionLedgerRead}
		})},
		{"permission outside the catalog", valid(func(o *Operation) { o.Permissions = []string{"test.thing.write"}; o.Risk = model.RiskWrite })},
		{"risk lower than the permission", valid(func(o *Operation) {
			o.Permissions = []string{model.PermissionExchangeWrite}
			o.Risk = model.RiskWrite
			o.Idempotent = true
		})},
		{"risk lower than the highest of two permissions", valid(func(o *Operation) {
			o.Permissions = []string{model.PermissionLedgerRead, model.PermissionCommitmentWrite}
			o.Risk = model.RiskRead
		})},
		{"risk higher than the permissions", valid(func(o *Operation) { o.Risk = model.RiskWrite })},
		{"unknown risk", valid(func(o *Operation) { o.Risk = "danger" })},
		{"empty risk", valid(func(o *Operation) { o.Risk = "" })},
		{"malformed capability", valid(func(o *Operation) { o.Capability = "Not A Key" })},
		{"money without idempotency", valid(func(o *Operation) {
			o.Permissions = []string{model.PermissionExchangeWrite}
			o.Risk = model.RiskMoney
			o.Idempotent = false
		})},
	}
	for _, c := range cases {
		engine, err := NewEngine(w.api, w.auth, c.ops...)
		if engine != nil {
			t.Errorf("%s: an engine was returned", c.name)
		}
		requireCode(t, c.name, err, model.CodeInvalidInput)
	}

	// Controls: each shape registers when it is valid.
	good := []struct {
		name string
		auth *capability.Authorizer
		ops  []Operation
	}{
		{"read operation", w.auth, []Operation{validOperation()}},
		{"write operation with a capability", w.auth, []Operation{{
			Name: "test.write_commitment", Permissions: []string{model.PermissionCommitmentWrite}, Capability: "test.billing", Risk: model.RiskWrite}}},
		{"money operation with idempotency", w.auth, []Operation{moneyOperation}},
		{"mixed permissions at the highest risk", w.auth, []Operation{{
			Name: "test.mixed", Permissions: []string{model.PermissionLedgerRead, model.PermissionCommitmentWrite}, Risk: model.RiskWrite}}},
		{"write operation that also declares idempotency", w.auth, []Operation{{
			Name: "test.idempotent_write", Permissions: []string{model.PermissionCommitmentWrite}, Risk: model.RiskWrite, Idempotent: true}}},
		{"permission of an extension", extendedAuth, []Operation{{
			Name: "test.thing", Permissions: []string{"test.thing.write"}, Risk: model.RiskWrite}}},
		{"no operations at all", w.auth, nil},
	}
	for _, c := range good {
		if _, err := NewEngine(w.api, c.auth, c.ops...); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// TestNewEngineRefusesMoneyWithoutIdempotency guards criterion 7 on its own:
// the one rule of the criterion, with its control.
func TestNewEngineRefusesMoneyWithoutIdempotency(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	op := moneyOperation
	op.Idempotent = false
	_, err := NewEngine(w.api, w.auth, op)
	requireCode(t, "money without idempotency", err, model.CodeInvalidInput)
	if err == nil || !strings.Contains(err.Error(), "idempot") {
		t.Errorf("the error does not name the missing idempotency: %v", err)
	}
	if _, err := NewEngine(w.api, w.auth, moneyOperation); err != nil {
		t.Errorf("control: %v", err)
	}
}

// TestNewEngineValidatesItsDependencies guards that an engine without a pool
// or an authorizer cannot exist: it would run with no verification.
func TestNewEngineValidatesItsDependencies(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, err := NewEngine(nil, w.auth)
	requireCode(t, "nil pool", err, model.CodeInvalidInput)
	_, err = NewEngine(w.api, nil)
	requireCode(t, "nil authorizer", err, model.CodeInvalidInput)
	if _, err := NewEngine(w.api, w.auth); err != nil {
		t.Errorf("control: %v", err)
	}
}

// TestEngineCopiesTheDeclaration guards that a caller cannot weaken an
// operation after registering it by editing the slice it passed in.
func TestEngineCopiesTheDeclaration(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	permissions := []string{model.PermissionLedgerRead}
	op := Operation{Name: "test.read_ledger", Permissions: permissions, Risk: model.RiskRead}
	engine, err := NewEngine(w.api, w.auth, op)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	permissions[0] = "kernel.nope.read"
	got, ok := engine.ops["test.read_ledger"]
	if !ok || got.Permissions[0] != model.PermissionLedgerRead {
		t.Errorf("the registered permissions changed with the caller's slice: %+v", got)
	}
}
