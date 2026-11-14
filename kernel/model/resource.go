package model

import "regexp"

// resourceCodePattern accepts an uppercase code of one to 32 characters that
// starts with a letter, such as COP, UND or MIN.
var resourceCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)

const (
	// MinResourceScale is the fewest decimals a resource may allow.
	MinResourceScale = 0
	// MaxResourceScale is the most decimals a resource may allow.
	MaxResourceScale = 18
)

// ResourceKind is the type of value a resource represents.
type ResourceKind string

const (
	// ResourceKindMoney is currency, such as COP.
	ResourceKindMoney ResourceKind = "money"
	// ResourceKindUnit is a countable thing.
	ResourceKindUnit ResourceKind = "unit"
	// ResourceKindTime is a measure of time.
	ResourceKindTime ResourceKind = "time"
	// ResourceKindAccess is a right to enter or use something.
	ResourceKindAccess ResourceKind = "access"
)

// Valid reports whether k is one of the known kinds.
func (k ResourceKind) Valid() bool {
	switch k {
	case ResourceKindMoney, ResourceKindUnit, ResourceKindTime, ResourceKindAccess:
		return true
	}
	return false
}

// ValidResourceCode reports whether code is an uppercase resource code. The
// database enforces the same rule; this copy lets callers fail before opening
// a transaction.
func ValidResourceCode(code string) bool {
	return resourceCodePattern.MatchString(code)
}

// ValidResourceScale reports whether scale is within the decimals a resource
// may allow.
func ValidResourceScale(scale int) bool {
	return scale >= MinResourceScale && scale <= MaxResourceScale
}

// Resource is something a space holds and moves. Scale is the number of
// decimals an amount of it may carry.
type Resource struct {
	ID      string
	SpaceID string
	Kind    ResourceKind
	Code    string
	Name    string
	Scale   int
}
