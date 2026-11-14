package state_test

import (
	"context"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestEnableCapabilityStoresActive guards criterion 5: each source is stored
// with status active.
func TestEnableCapabilityStoresActive(t *testing.T) {
	t.Parallel()
	admin, api, staff := grantSetup(t)
	ctx := context.Background()

	for _, source := range []model.CapabilitySource{
		model.CapabilitySourcePackage, model.CapabilitySourceAI, model.CapabilitySourceCode,
	} {
		in := model.NewSpaceCapability{CapabilityKey: "test." + string(source), Source: source}
		if err := state.EnableCapability(ctx, api, system, spaceA, in); err != nil {
			t.Fatalf("EnableCapability(%s): %v", source, err)
		}
	}
	in := model.NewSpaceCapability{
		CapabilityKey: "test.capability", Source: model.CapabilitySourceAdmin, Reason: "contract signed",
		EnabledBy: &model.Grantor{ParticipantID: staff, SpaceID: spacePlatform},
	}
	if err := state.EnableCapability(ctx, api, system, spaceA, in); err != nil {
		t.Fatalf("EnableCapability(admin): %v", err)
	}

	var status, source, reason, kind string
	if err := admin.QueryRow(`SELECT status, source, coalesce(reason, ''), enabled_by_space_kind FROM kernel.space_capabilities
		WHERE space_id = $1 AND capability_key = 'test.capability'`, spaceA).Scan(&status, &source, &reason, &kind); err != nil {
		t.Fatalf("read capability: %v", err)
	}
	if status != "active" || source != "admin" || reason != "contract signed" || kind != "platform" {
		t.Errorf("stored = %q %q %q %q", status, source, reason, kind)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.space_capabilities WHERE status = 'active'`); got != 4 {
		t.Errorf("active capabilities = %d, want 4", got)
	}
}

// TestEnableCapabilityRejectsUnknownSource guards criterion 5.
func TestEnableCapabilityRejectsUnknownSource(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	for name, in := range map[string]model.NewSpaceCapability{
		"unknown source": {CapabilityKey: "test.capability", Source: "owner"},
		"empty source":   {CapabilityKey: "test.capability"},
		"bad key":        {CapabilityKey: "Capability", Source: model.CapabilitySourcePackage},
		"blank reason":   {CapabilityKey: "test.capability", Source: model.CapabilitySourcePackage, Reason: " "},
		"bad enabler":    {CapabilityKey: "test.capability", Source: model.CapabilitySourceAdmin, EnabledBy: &model.Grantor{ParticipantID: "x", SpaceID: spaceA}},
	} {
		err := state.EnableCapability(context.Background(), api, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.space_capabilities`); got != 0 {
		t.Errorf("capabilities = %d, want 0", got)
	}
}

// TestEnableCapabilityDuplicateIsRuleViolation guards criterion 5: the pair
// (space, key) is unique, and another space may enable the same key.
func TestEnableCapabilityDuplicateIsRuleViolation(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	in := model.NewSpaceCapability{CapabilityKey: "test.capability", Source: model.CapabilitySourcePackage}
	ctx := context.Background()

	if err := state.EnableCapability(ctx, api, system, spaceA, in); err != nil {
		t.Fatalf("EnableCapability: %v", err)
	}
	requireCode(t, state.EnableCapability(ctx, api, system, spaceA, in), model.CodeRuleViolation)
	if err := state.EnableCapability(ctx, api, system, spaceB, in); err != nil {
		t.Errorf("another space must accept the same key: %v", err)
	}
}

// TestAdminCapabilityRequiresEnabledBy guards criterion 5: an admin capability
// names who enabled it.
func TestAdminCapabilityRequiresEnabledBy(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	err := state.EnableCapability(context.Background(), api, system, spaceA,
		model.NewSpaceCapability{CapabilityKey: "test.capability", Source: model.CapabilitySourceAdmin})
	requireCode(t, err, model.CodeInvalidInput)
}

// TestEnableCapabilityByParticipantOfAnotherBusinessSpaceIsRefused guards
// rule 9: a business space never enables its own or another's capability
// through a foreign participant.
func TestEnableCapabilityByParticipantOfAnotherBusinessSpaceIsRefused(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	other := mustParticipant(t, api, spaceB, newPerson("", "Other"))
	err := state.EnableCapability(context.Background(), api, system, spaceA, model.NewSpaceCapability{
		CapabilityKey: "test.capability", Source: model.CapabilitySourceAdmin,
		EnabledBy: &model.Grantor{ParticipantID: other, SpaceID: spaceB},
	})
	if code, ok := model.CodeOf(err); !ok || (code != model.CodeInvalidInput && code != model.CodeRuleViolation) {
		t.Errorf("error = %v, want invalid_input or rule_violation", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.space_capabilities`); got != 0 {
		t.Errorf("capabilities = %d, want 0", got)
	}
}
