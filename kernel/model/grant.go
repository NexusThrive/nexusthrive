package model

import (
	"regexp"
	"time"
)

// Clock tells the current time. Operations that decide by time take a Clock so
// that tests can move it; a database function with now() cannot be frozen.
type Clock interface {
	Now() time.Time
}

// SystemClock is the Clock of production: the real time, in UTC.
type SystemClock struct{}

// Now returns the current time in UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// MaxPermissions is the most permissions a role or a grant may list.
const MaxPermissions = 200

// maxKeyLength bounds a permission or capability key.
const maxKeyLength = 128

var (
	roleKeyPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	permissionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
)

// ValidRoleKey reports whether key is a lowercase word of up to 64 characters
// that starts with a letter and holds only letters, digits and underscores.
func ValidRoleKey(key string) bool { return roleKeyPattern.MatchString(key) }

// ValidPermissionKey reports whether key has two or more lowercase segments
// separated by dots. Capability keys share the shape.
func ValidPermissionKey(key string) bool {
	return len(key) <= maxKeyLength && permissionKeyPattern.MatchString(key)
}

// ValidPermissionList reports whether every element is a valid permission key
// and the list holds at most MaxPermissions elements. An empty list is valid.
func ValidPermissionList(list []string) bool {
	if len(list) > MaxPermissions {
		return false
	}
	for _, key := range list {
		if !ValidPermissionKey(key) {
			return false
		}
	}
	return true
}

// NewRole is the input of a role to create. System marks a role the kernel or
// NexusThrive staff manage.
type NewRole struct {
	Key         string
	Name        string
	Permissions []string
	System      bool
}

// Role is a named set of permissions defined by a space.
type Role struct {
	ID          string
	SpaceID     string
	Key         string
	Name        string
	Permissions []string
	System      bool
}

// Grantor is the participant that performs an act and the space it lives in.
// The space may differ from the space of the act: staff of a platform space
// grant access in the spaces they support (ADR-0029).
type Grantor struct {
	ParticipantID string
	SpaceID       string
}

// NewGrant is the input of a grant to create. A nil GrantedBy records no
// grantor. Reason is optional except for support access; a nil ExpiresAt
// means the grant does not expire.
type NewGrant struct {
	ParticipantID    string
	RoleID           string
	ExtraPermissions []string
	GrantedBy        *Grantor
	Reason           string
	ExpiresAt        *time.Time
}

// Grant gives a participant a role in a space. It is active until it expires
// or is revoked.
type Grant struct {
	ID               string
	SpaceID          string
	ParticipantID    string
	RoleID           string
	ExtraPermissions []string
	GrantedBy        *Grantor
	Reason           string
	ExpiresAt        *time.Time
	CreatedAt        time.Time
	RevokedAt        *time.Time
}

// IsActive reports whether the grant counts at the given time: it is not
// revoked and it has no expiry or the expiry is still in the future. A grant
// whose expiry equals now is already expired.
func (g Grant) IsActive(now time.Time) bool {
	if g.RevokedAt != nil {
		return false
	}
	return g.ExpiresAt == nil || now.Before(*g.ExpiresAt)
}
