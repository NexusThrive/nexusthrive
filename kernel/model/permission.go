package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Risk is how much harm an operation can do: it decides what a suspended or
// closed space still allows (ADR-0032).
type Risk string

const (
	// RiskRead is an operation that only reads.
	RiskRead Risk = "read"
	// RiskWrite is an operation that changes state.
	RiskWrite Risk = "write"
	// RiskMoney is an operation that moves value; it is always idempotent.
	RiskMoney Risk = "money"
)

// Valid reports whether r is one of the known risks.
func (r Risk) Valid() bool { return r == RiskRead || r == RiskWrite || r == RiskMoney }

// The permission keys of the kernel catalog (ADR-0032).
const (
	// PermissionParticipantRead lists and reads participants.
	PermissionParticipantRead = "kernel.participant.read"
	// PermissionParticipantWrite creates and archives participants.
	PermissionParticipantWrite = "kernel.participant.write"
	// PermissionRoleWrite creates roles.
	PermissionRoleWrite = "kernel.role.write"
	// PermissionGrantWrite grants and revokes roles.
	PermissionGrantWrite = "kernel.grant.write"
	// PermissionCapabilityWrite enables or suspends the capabilities of a
	// space. Only a platform space may hold it; the database enforces that.
	PermissionCapabilityWrite = "kernel.capability.write"
	// PermissionResourceWrite defines resources and opens or closes accounts.
	PermissionResourceWrite = "kernel.resource.write"
	// PermissionLedgerRead reads resources, accounts, exchanges and entries.
	PermissionLedgerRead = "kernel.ledger.read"
	// PermissionExchangeWrite records exchanges and compensations.
	PermissionExchangeWrite = "kernel.exchange.write"
	// PermissionCommitmentRead reads commitments and fulfillments.
	PermissionCommitmentRead = "kernel.commitment.read"
	// PermissionCommitmentWrite creates, fulfills and cancels commitments.
	PermissionCommitmentWrite = "kernel.commitment.write"
	// PermissionEventRead reads events.
	PermissionEventRead = "kernel.event.read"
	// PermissionTimerProcess claims due timers.
	PermissionTimerProcess = "kernel.timer.process"
	// PermissionOutboxClaim claims outbox rows.
	PermissionOutboxClaim = "kernel.outbox.claim"
)

// PermissionDef describes one permission key. PlatformOnly marks a key that
// roles and grants of a space of another kind than platform can never hold.
type PermissionDef struct {
	Key          string
	Risk         Risk
	PlatformOnly bool
}

// kernelNamespace is the namespace of the kernel catalog; no extension may
// use it or a name that starts with it.
const kernelNamespace = "kernel"

// namespacePattern is one lowercase word: a namespace has no dots.
var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Catalog is the set of permission keys a process knows. It has no global
// state: the kernel catalog comes from KernelCatalog and an extension builds a
// new Catalog with Extend. Every method returns copies, so a caller can never
// change a catalog it received.
type Catalog struct {
	defs map[string]PermissionDef
}

// KernelCatalog returns the catalog of the 13 keys of the kernel.
func KernelCatalog() Catalog {
	defs := []PermissionDef{
		{PermissionParticipantRead, RiskRead, false},
		{PermissionParticipantWrite, RiskWrite, false},
		{PermissionRoleWrite, RiskWrite, false},
		{PermissionGrantWrite, RiskWrite, false},
		{PermissionCapabilityWrite, RiskWrite, true},
		{PermissionResourceWrite, RiskWrite, false},
		{PermissionLedgerRead, RiskRead, false},
		{PermissionExchangeWrite, RiskMoney, false},
		{PermissionCommitmentRead, RiskRead, false},
		{PermissionCommitmentWrite, RiskWrite, false},
		{PermissionEventRead, RiskRead, false},
		{PermissionTimerProcess, RiskWrite, false},
		{PermissionOutboxClaim, RiskWrite, false},
	}
	catalog := Catalog{defs: make(map[string]PermissionDef, len(defs))}
	for _, def := range defs {
		catalog.defs[def.Key] = def
	}
	return catalog
}

// Lookup returns the definition of key and whether the catalog has it.
func (c Catalog) Lookup(key string) (PermissionDef, bool) {
	def, ok := c.defs[key]
	return def, ok
}

// Keys returns every key of the catalog, sorted.
func (c Catalog) Keys() []string {
	keys := make([]string, 0, len(c.defs))
	for key := range c.defs {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// PlatformOnlyKeys returns the keys only a platform space may hold, sorted. The
// database keeps the same list in kernel.platform_only_permissions().
func (c Catalog) PlatformOnlyKeys() []string {
	keys := []string{}
	for _, key := range c.Keys() {
		if c.defs[key].PlatformOnly {
			keys = append(keys, key)
		}
	}
	return keys
}

// ValidateList returns invalid_input unless every element of list is a key of
// the catalog. An empty list is valid.
func (c Catalog) ValidateList(list []string) error {
	for _, key := range list {
		if _, ok := c.defs[key]; !ok {
			return NewError(CodeInvalidInput, fmt.Sprintf("permission %q is not in the catalog", key), nil)
		}
	}
	return nil
}

// Extend returns a new catalog with defs added; the receiver does not change.
// The namespace must be one lowercase word that is not "kernel" and does not
// start with it, every key must start with namespace plus a dot, keys may not
// repeat or already exist, every risk must be valid and PlatformOnly must be
// false: the database knows only the platform-only keys of the kernel.
func (c Catalog) Extend(namespace string, defs []PermissionDef) (Catalog, error) {
	if !namespacePattern.MatchString(namespace) || strings.HasPrefix(namespace, kernelNamespace) {
		return Catalog{}, NewError(CodeInvalidInput,
			"a catalog namespace is one lowercase word that does not start with kernel", nil)
	}
	next := Catalog{defs: make(map[string]PermissionDef, len(c.defs)+len(defs))}
	for key, def := range c.defs {
		next.defs[key] = def
	}
	for _, def := range defs {
		switch {
		case !strings.HasPrefix(def.Key, namespace+".") || !ValidPermissionKey(def.Key):
			return Catalog{}, NewError(CodeInvalidInput,
				fmt.Sprintf("permission %q must be a valid key that starts with %q", def.Key, namespace+"."), nil)
		case !def.Risk.Valid():
			return Catalog{}, NewError(CodeInvalidInput, fmt.Sprintf("permission %q has an invalid risk", def.Key), nil)
		case def.PlatformOnly:
			return Catalog{}, NewError(CodeInvalidInput,
				fmt.Sprintf("permission %q cannot be platform-only: only the kernel defines those", def.Key), nil)
		}
		if _, exists := next.defs[def.Key]; exists {
			return Catalog{}, NewError(CodeInvalidInput, fmt.Sprintf("permission %q is already registered", def.Key), nil)
		}
		next.defs[def.Key] = def
	}
	return next, nil
}

// OwnerRole returns the system role every space receives at bootstrap. It has
// ten keys and never kernel.capability.write (rule 9), nor the keys of the
// processes of the kernel: timer.process and outbox.claim belong to the system
// actor. Nothing in the code gives the role any authority beyond its list.
func OwnerRole() NewRole {
	return NewRole{
		Key:  "owner",
		Name: "Owner",
		Permissions: []string{
			PermissionParticipantRead, PermissionParticipantWrite, PermissionRoleWrite,
			PermissionGrantWrite, PermissionResourceWrite, PermissionLedgerRead,
			PermissionExchangeWrite, PermissionCommitmentRead, PermissionCommitmentWrite,
			PermissionEventRead,
		},
		System: true,
	}
}
