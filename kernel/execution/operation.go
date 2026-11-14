package execution

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// maxOperationName is the longest accepted operation name, in characters.
const maxOperationName = 128

// operationNamePattern accepts two or more lowercase segments separated by
// dots. The first segment is the namespace of the operation.
var operationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// kernelNamespace is the namespace reserved to the operations of the kernel.
const kernelNamespace = "kernel"

// Operation is the declaration of one operation: what it needs to run and how
// much harm it can do. Declaring is not implementing: the handler arrives with
// each call.
type Operation struct {
	// Name is dotted lowercase; its first segment is its namespace. The
	// namespace kernel, and any that starts with it, is reserved to the
	// operations of the kernel itself.
	Name string
	// Permissions are the keys the actor must hold, every one of them, all in
	// the catalog of the authorizer; at least one.
	Permissions []string
	// Capability is optional; when set it must be active in the space.
	Capability string
	// Risk must equal the highest risk among Permissions, so an operation that
	// moves value cannot be declared as a plain write.
	Risk model.Risk
	// Idempotent declares that the operation answers repeated requests with the
	// response of the first one; it is required when Risk is money. Such an
	// operation runs only through Engine.RunIdempotent (ADR-0026), and Run
	// refuses it.
	Idempotent bool
}

// namespace returns the first segment of the operation name.
func (o Operation) namespace() string {
	name, _, _ := strings.Cut(o.Name, ".")
	return name
}

// riskRank orders the risks so that the highest of a list can be found.
func riskRank(r model.Risk) int {
	switch r {
	case model.RiskRead:
		return 1
	case model.RiskWrite:
		return 2
	case model.RiskMoney:
		return 3
	}
	return 0
}

// invalid builds the invalid_input error of a rejected declaration.
func invalid(name, reason string) error {
	return model.NewError(model.CodeInvalidInput, fmt.Sprintf("operation %q: %s", name, reason), nil)
}

// validateOperation checks a declaration against the catalog. A kernel
// operation may use the reserved namespace; any other may not. It returns a
// copy of the operation whose permission list is its own, so that a caller
// cannot change it after registering it.
func validateOperation(catalog model.Catalog, op Operation, kernelOwned bool) (Operation, error) {
	if len(op.Name) > maxOperationName || !operationNamePattern.MatchString(op.Name) {
		return Operation{}, invalid(op.Name, "the name must be two or more lowercase segments separated by dots")
	}
	if !kernelOwned && strings.HasPrefix(op.namespace(), kernelNamespace) {
		return Operation{}, invalid(op.Name, "the namespace kernel is reserved to the operations of the kernel")
	}
	if len(op.Permissions) == 0 {
		return Operation{}, invalid(op.Name, "an operation needs at least one permission")
	}
	seen := map[string]bool{}
	highest := 0
	for _, key := range op.Permissions {
		def, known := catalog.Lookup(key)
		if !known {
			return Operation{}, invalid(op.Name, fmt.Sprintf("permission %q is not in the catalog", key))
		}
		if seen[key] {
			return Operation{}, invalid(op.Name, fmt.Sprintf("permission %q is repeated", key))
		}
		seen[key] = true
		highest = max(highest, riskRank(def.Risk))
	}
	if !op.Risk.Valid() {
		return Operation{}, invalid(op.Name, "the risk must be read, write or money")
	}
	if riskRank(op.Risk) != highest {
		return Operation{}, invalid(op.Name, "the risk must equal the highest risk among its permissions")
	}
	if op.Capability != "" && !model.ValidPermissionKey(op.Capability) {
		return Operation{}, invalid(op.Name, "the capability key must have two or more lowercase segments separated by dots")
	}
	if op.Risk == model.RiskMoney && !op.Idempotent {
		return Operation{}, invalid(op.Name, "a money operation must declare idempotency")
	}
	op.Permissions = append([]string(nil), op.Permissions...)
	return op, nil
}

// declares reports whether the operation lists the permission.
func (o Operation) declares(key string) bool {
	for _, declared := range o.Permissions {
		if declared == key {
			return true
		}
	}
	return false
}
