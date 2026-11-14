package model

// SpaceKind is the type of a space; it decides which distribution may run in it.
type SpaceKind string

const (
	// SpaceKindPlatform is the space of NexusThrive staff.
	SpaceKindPlatform SpaceKind = "platform"
	// SpaceKindBusiness is a space where a business operates.
	SpaceKindBusiness SpaceKind = "business"
	// SpaceKindCommunity is a space shared by a group of people.
	SpaceKindCommunity SpaceKind = "community"
	// SpaceKindPersonal is the private space of one person.
	SpaceKindPersonal SpaceKind = "personal"
)

// Valid reports whether k is one of the known kinds.
func (k SpaceKind) Valid() bool {
	switch k {
	case SpaceKindPlatform, SpaceKindBusiness, SpaceKindCommunity, SpaceKindPersonal:
		return true
	}
	return false
}

// SpaceStatus is the lifecycle state of a space.
type SpaceStatus string

const (
	// SpaceStatusProvisioning is the state of a space that is still being set up.
	SpaceStatusProvisioning SpaceStatus = "provisioning"
	// SpaceStatusActive is a space in normal operation.
	SpaceStatusActive SpaceStatus = "active"
	// SpaceStatusSuspended is a space paused by NexusThrive staff.
	SpaceStatusSuspended SpaceStatus = "suspended"
	// SpaceStatusClosed is a space that no longer accepts activity.
	SpaceStatusClosed SpaceStatus = "closed"
)

// ActorKind tells who performs an operation.
type ActorKind string

const (
	// ActorPerson is a human acting through a participant.
	ActorPerson ActorKind = "person"
	// ActorAgent is an automated agent acting through a participant.
	ActorAgent ActorKind = "agent"
	// ActorAPIKey is an integration acting with an API key.
	ActorAPIKey ActorKind = "api_key"
	// ActorSystem is the kernel itself, used for internal bootstrap operations.
	ActorSystem ActorKind = "system"
)

// Valid reports whether k is one of the known kinds.
func (k ActorKind) Valid() bool {
	switch k {
	case ActorPerson, ActorAgent, ActorAPIKey, ActorSystem:
		return true
	}
	return false
}

// Actor identifies who performs an operation. ParticipantID is the participant
// the actor stands for; it may be empty only for the system actor. The
// application layer asserts the actor in M1: a permission check proves that
// this participant may act, not that the caller is this participant (ADR-0032).
type Actor struct {
	Kind          ActorKind
	ParticipantID string
}

// IsNil reports whether the actor is null: its kind is empty or unknown, or it
// is not the system actor and stands for no participant. A null actor is never
// allowed to do anything. The system actor with no participant is not null.
func (a Actor) IsNil() bool {
	if !a.Kind.Valid() {
		return true
	}
	return a.Kind != ActorSystem && a.ParticipantID == ""
}
