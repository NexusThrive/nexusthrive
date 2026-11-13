package model

// IdentityProvider is the system that vouches for an identity.
type IdentityProvider string

const (
	// IdentityProviderClerk is a person who signs in through the identity service.
	IdentityProviderClerk IdentityProvider = "clerk"
	// IdentityProviderAPIKey is an agent that authenticates with a key.
	IdentityProviderAPIKey IdentityProvider = "api_key"
	// IdentityProviderSystem is the kernel itself or an internal process.
	IdentityProviderSystem IdentityProvider = "system"
)

// Valid reports whether p is one of the known providers.
func (p IdentityProvider) Valid() bool {
	switch p {
	case IdentityProviderClerk, IdentityProviderAPIKey, IdentityProviderSystem:
		return true
	}
	return false
}

// ParticipantKind is the type of a participant.
type ParticipantKind string

const (
	// ParticipantKindPerson is a natural person.
	ParticipantKindPerson ParticipantKind = "person"
	// ParticipantKindOrganization is a legal entity or a group.
	ParticipantKindOrganization ParticipantKind = "organization"
	// ParticipantKindAgent is an automated actor working on behalf of others.
	ParticipantKindAgent ParticipantKind = "agent"
	// ParticipantKindSystem is the kernel acting inside the space.
	ParticipantKindSystem ParticipantKind = "system"
)

// Valid reports whether k is one of the known kinds.
func (k ParticipantKind) Valid() bool {
	switch k {
	case ParticipantKindPerson, ParticipantKindOrganization, ParticipantKindAgent, ParticipantKindSystem:
		return true
	}
	return false
}

// ParticipantStatus is the lifecycle state of a participant.
type ParticipantStatus string

const (
	// ParticipantStatusActive is a participant in normal use.
	ParticipantStatusActive ParticipantStatus = "active"
	// ParticipantStatusArchived is a participant kept for history but retired.
	ParticipantStatusArchived ParticipantStatus = "archived"
)

// Valid reports whether s is one of the known statuses.
func (s ParticipantStatus) Valid() bool {
	return s == ParticipantStatusActive || s == ParticipantStatusArchived
}

// DocumentType is the kind of legal document that identifies a participant.
type DocumentType string

const (
	// DocumentTypeCC is a citizenship card.
	DocumentTypeCC DocumentType = "CC"
	// DocumentTypeNIT is a tax identification number.
	DocumentTypeNIT DocumentType = "NIT"
	// DocumentTypeCE is a foreigner card.
	DocumentTypeCE DocumentType = "CE"
	// DocumentTypePP is a passport.
	DocumentTypePP DocumentType = "PP"
)

// Valid reports whether d is one of the known document types.
func (d DocumentType) Valid() bool {
	switch d {
	case DocumentTypeCC, DocumentTypeNIT, DocumentTypeCE, DocumentTypePP:
		return true
	}
	return false
}
