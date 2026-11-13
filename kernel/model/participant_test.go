package model_test

import (
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

func TestIdentityProviderValid(t *testing.T) {
	for p, want := range map[model.IdentityProvider]bool{
		model.IdentityProviderClerk: true, model.IdentityProviderAPIKey: true, model.IdentityProviderSystem: true,
		"": false, "google": false, "Clerk": false,
	} {
		if got := p.Valid(); got != want {
			t.Errorf("IdentityProvider(%q).Valid() = %v, want %v", p, got, want)
		}
	}
}

func TestParticipantKindValid(t *testing.T) {
	for k, want := range map[model.ParticipantKind]bool{
		model.ParticipantKindPerson: true, model.ParticipantKindOrganization: true,
		model.ParticipantKindAgent: true, model.ParticipantKindSystem: true,
		"": false, "robot": false,
	} {
		if got := k.Valid(); got != want {
			t.Errorf("ParticipantKind(%q).Valid() = %v, want %v", k, got, want)
		}
	}
}

func TestParticipantStatusValid(t *testing.T) {
	for s, want := range map[model.ParticipantStatus]bool{
		model.ParticipantStatusActive: true, model.ParticipantStatusArchived: true,
		"": false, "deleted": false,
	} {
		if got := s.Valid(); got != want {
			t.Errorf("ParticipantStatus(%q).Valid() = %v, want %v", s, got, want)
		}
	}
}

func TestDocumentTypeValid(t *testing.T) {
	for d, want := range map[model.DocumentType]bool{
		model.DocumentTypeCC: true, model.DocumentTypeNIT: true, model.DocumentTypeCE: true, model.DocumentTypePP: true,
		"": false, "cc": false, "DNI": false,
	} {
		if got := d.Valid(); got != want {
			t.Errorf("DocumentType(%q).Valid() = %v, want %v", d, got, want)
		}
	}
}
