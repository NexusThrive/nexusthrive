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

// ActorSystem is the kernel itself, used for internal bootstrap operations.
const ActorSystem ActorKind = "system"

// Actor identifies who performs an operation. Permission checks that use more
// than the kind arrive with the permissions story.
type Actor struct {
	Kind ActorKind
}
