package model

import "time"

// CapabilitySource is where the activation of a capability came from.
type CapabilitySource string

const (
	// CapabilitySourcePackage is a capability that comes with a skin package.
	CapabilitySourcePackage CapabilitySource = "package"
	// CapabilitySourceAI is a capability the assistant enabled within the
	// suggested ones.
	CapabilitySourceAI CapabilitySource = "ai"
	// CapabilitySourceAdmin is a capability NexusThrive staff enabled.
	CapabilitySourceAdmin CapabilitySource = "admin"
	// CapabilitySourceCode is a capability the kernel enabled by itself.
	CapabilitySourceCode CapabilitySource = "code"
)

// Valid reports whether s is one of the known sources.
func (s CapabilitySource) Valid() bool {
	switch s {
	case CapabilitySourcePackage, CapabilitySourceAI, CapabilitySourceAdmin, CapabilitySourceCode:
		return true
	}
	return false
}

// NewSpaceCapability is the input of a capability to enable in a space.
// EnabledBy is required when Source is CapabilitySourceAdmin. Reason is optional.
type NewSpaceCapability struct {
	CapabilityKey string
	Source        CapabilitySource
	EnabledBy     *Grantor
	Reason        string
}

// AccessAction is the kind of access an audit entry records.
type AccessAction string

const (
	// AccessActionRead is a read of data of the audited space.
	AccessActionRead AccessAction = "read"
	// AccessActionWrite is a change to data of the audited space.
	AccessActionWrite AccessAction = "write"
)

// Valid reports whether a is one of the known actions.
func (a AccessAction) Valid() bool { return a == AccessActionRead || a == AccessActionWrite }

// NewAccessAudit is the input of an audit entry. The actor lives in
// ActorSpaceID, which is usually not the audited space. Reason and RequestID
// are mandatory; SubjectRef and GrantID are optional.
type NewAccessAudit struct {
	ActorParticipantID string
	ActorSpaceID       string
	Action             AccessAction
	SubjectRef         string
	Reason             string
	RequestID          string
	GrantID            string
}

// AccessAudit is a stored audit entry. Entries are append-only.
type AccessAudit struct {
	ID                 string
	SpaceID            string
	ActorParticipantID string
	ActorSpaceID       string
	Action             AccessAction
	SubjectRef         string
	Reason             string
	RequestID          string
	GrantID            string
	OccurredAt         time.Time
}
