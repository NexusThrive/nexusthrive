package model_test

import (
	"slices"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// wantCatalog is the approved catalog of the story: key, risk and whether
// only a platform space may hold it.
var wantCatalog = map[string]struct {
	risk         model.Risk
	platformOnly bool
}{
	"kernel.participant.read":  {model.RiskRead, false},
	"kernel.participant.write": {model.RiskWrite, false},
	"kernel.role.write":        {model.RiskWrite, false},
	"kernel.grant.write":       {model.RiskWrite, false},
	"kernel.capability.write":  {model.RiskWrite, true},
	"kernel.resource.write":    {model.RiskWrite, false},
	"kernel.ledger.read":       {model.RiskRead, false},
	"kernel.exchange.write":    {model.RiskMoney, false},
	"kernel.commitment.read":   {model.RiskRead, false},
	"kernel.commitment.write":  {model.RiskWrite, false},
	"kernel.event.read":        {model.RiskRead, false},
	"kernel.timer.process":     {model.RiskWrite, false},
	"kernel.outbox.claim":      {model.RiskWrite, false},
}

// TestKernelCatalogHasThirteenKeysWithRisk pins the approved catalog.
func TestKernelCatalogHasThirteenKeysWithRisk(t *testing.T) {
	t.Parallel()
	catalog := model.KernelCatalog()
	keys := catalog.Keys()
	if len(keys) != len(wantCatalog) || len(keys) != 13 {
		t.Fatalf("catalog has %d keys, want 13: %v", len(keys), keys)
	}
	for key, want := range wantCatalog {
		def, ok := catalog.Lookup(key)
		if !ok {
			t.Errorf("%s is missing from the catalog", key)
			continue
		}
		if def.Key != key || def.Risk != want.risk || def.PlatformOnly != want.platformOnly {
			t.Errorf("%s = %+v, want risk %s platform-only %v", key, def, want.risk, want.platformOnly)
		}
	}
	if _, ok := catalog.Lookup("kernel.unknown.write"); ok {
		t.Error("an unknown key is in the catalog")
	}
	if !slices.IsSorted(keys) {
		t.Errorf("Keys is not sorted: %v", keys)
	}
}

// TestKernelCatalogConstantsMatchTable ties each exported constant to its key.
func TestKernelCatalogConstantsMatchTable(t *testing.T) {
	t.Parallel()
	got := []string{
		model.PermissionParticipantRead, model.PermissionParticipantWrite, model.PermissionRoleWrite,
		model.PermissionGrantWrite, model.PermissionCapabilityWrite, model.PermissionResourceWrite,
		model.PermissionLedgerRead, model.PermissionExchangeWrite, model.PermissionCommitmentRead,
		model.PermissionCommitmentWrite, model.PermissionEventRead, model.PermissionTimerProcess,
		model.PermissionOutboxClaim,
	}
	slices.Sort(got)
	want := model.KernelCatalog().Keys()
	if !slices.Equal(got, want) {
		t.Errorf("constants = %v, catalog = %v", got, want)
	}
}

// TestCatalogValidateList rejects keys outside the catalog and malformed ones.
func TestCatalogValidateList(t *testing.T) {
	t.Parallel()
	catalog := model.KernelCatalog()
	if err := catalog.ValidateList(nil); err != nil {
		t.Errorf("empty list: %v", err)
	}
	if err := catalog.ValidateList([]string{model.PermissionLedgerRead, model.PermissionGrantWrite}); err != nil {
		t.Errorf("valid list: %v", err)
	}
	for name, list := range map[string][]string{
		"unknown key":   {"kernel.ledger.read", "kernel.nope.write"},
		"malformed key": {"Kernel Ledger"},
		"empty key":     {""},
	} {
		err := catalog.ValidateList(list)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
}

// TestCatalogExtendRules covers every rule of Extend.
func TestCatalogExtendRules(t *testing.T) {
	t.Parallel()
	base := model.KernelCatalog()
	good := []model.PermissionDef{{Key: "plugin.item.write", Risk: model.RiskWrite}}

	extended, err := base.Extend("plugin", good)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if def, ok := extended.Lookup("plugin.item.write"); !ok || def.Risk != model.RiskWrite {
		t.Errorf("extended lookup = %+v, %v", def, ok)
	}
	if _, ok := extended.Lookup(model.PermissionLedgerRead); !ok {
		t.Error("the extension dropped a kernel key")
	}
	if _, ok := base.Lookup("plugin.item.write"); ok {
		t.Error("Extend changed the receiver")
	}

	cases := map[string]struct {
		namespace string
		defs      []model.PermissionDef
	}{
		"namespace kernel":               {"kernel", []model.PermissionDef{{Key: "kernel.item.write", Risk: model.RiskWrite}}},
		"namespace starting with kernel": {"kernelx", []model.PermissionDef{{Key: "kernelx.item.write", Risk: model.RiskWrite}}},
		"empty namespace":                {"", []model.PermissionDef{{Key: ".item.write", Risk: model.RiskWrite}}},
		"malformed namespace":            {"Plugin", []model.PermissionDef{{Key: "Plugin.item.write", Risk: model.RiskWrite}}},
		"dotted namespace":               {"plug.in", []model.PermissionDef{{Key: "plug.in.item.write", Risk: model.RiskWrite}}},
		"key of the kernel":              {"plugin", []model.PermissionDef{{Key: "kernel.timer.process", Risk: model.RiskWrite}}},
		"key of another namespace":       {"plugin", []model.PermissionDef{{Key: "other.item.write", Risk: model.RiskWrite}}},
		"prefix without the dot":         {"plug", []model.PermissionDef{{Key: "plugin.item.write", Risk: model.RiskWrite}}},
		"namespace alone as key":         {"plugin", []model.PermissionDef{{Key: "plugin", Risk: model.RiskWrite}}},
		"malformed key":                  {"plugin", []model.PermissionDef{{Key: "plugin.Item", Risk: model.RiskWrite}}},
		"duplicate in the input":         {"plugin", append(slices.Clone(good), good...)},
		"invalid risk":                   {"plugin", []model.PermissionDef{{Key: "plugin.item.write", Risk: "danger"}}},
		"empty risk":                     {"plugin", []model.PermissionDef{{Key: "plugin.item.write"}}},
		"platform only":                  {"plugin", []model.PermissionDef{{Key: "plugin.item.write", Risk: model.RiskWrite, PlatformOnly: true}}},
	}
	for name, tc := range cases {
		if _, err := base.Extend(tc.namespace, tc.defs); err == nil {
			t.Errorf("%s: Extend accepted it", name)
		} else if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}

	if _, err := extended.Extend("plugin", good); err == nil {
		t.Error("registering the same key twice was accepted")
	}
	if _, err := extended.Extend("other", []model.PermissionDef{{Key: "other.item.read", Risk: model.RiskRead}}); err != nil {
		t.Errorf("a second namespace: %v", err)
	}
}

// TestCatalogIsImmutable shows that nothing a caller holds changes a catalog.
func TestCatalogIsImmutable(t *testing.T) {
	t.Parallel()
	first := model.KernelCatalog()
	keys := first.Keys()
	keys[0] = "tampered"
	platform := first.PlatformOnlyKeys()
	if len(platform) > 0 {
		platform[0] = "tampered"
	}
	def, _ := first.Lookup(model.PermissionExchangeWrite)
	def.Risk = model.RiskRead

	second := model.KernelCatalog()
	if got, _ := second.Lookup(model.PermissionExchangeWrite); got.Risk != model.RiskMoney {
		t.Errorf("risk of exchange.write = %s after a caller changed its copy", got.Risk)
	}
	if got, _ := first.Lookup(model.PermissionExchangeWrite); got.Risk != model.RiskMoney {
		t.Errorf("the catalog changed through a returned definition: %s", got.Risk)
	}
	if slices.Contains(first.Keys(), "tampered") || slices.Contains(first.PlatformOnlyKeys(), "tampered") {
		t.Error("the catalog changed through a returned slice")
	}
	if !slices.Equal(first.PlatformOnlyKeys(), []string{model.PermissionCapabilityWrite}) {
		t.Errorf("platform-only keys = %v", first.PlatformOnlyKeys())
	}

	// A zero Catalog knows nothing and does not panic.
	var empty model.Catalog
	if _, ok := empty.Lookup(model.PermissionLedgerRead); ok {
		t.Error("the zero catalog knows a key")
	}
}

// TestOwnerRoleExcludesCapabilityWrite pins the ten keys of the owner role.
func TestOwnerRoleExcludesCapabilityWrite(t *testing.T) {
	t.Parallel()
	role := model.OwnerRole()
	if role.Key != "owner" || !role.System || role.Name == "" {
		t.Errorf("owner role = %+v", role)
	}
	want := []string{
		"kernel.participant.read", "kernel.participant.write", "kernel.role.write", "kernel.grant.write",
		"kernel.resource.write", "kernel.ledger.read", "kernel.exchange.write", "kernel.commitment.read",
		"kernel.commitment.write", "kernel.event.read",
	}
	if !slices.Equal(role.Permissions, want) {
		t.Errorf("owner permissions = %v", role.Permissions)
	}
	for _, key := range []string{model.PermissionCapabilityWrite, model.PermissionTimerProcess, model.PermissionOutboxClaim} {
		if slices.Contains(role.Permissions, key) {
			t.Errorf("the owner role holds %s", key)
		}
	}
	if err := model.KernelCatalog().ValidateList(role.Permissions); err != nil {
		t.Errorf("owner permissions are not in the catalog: %v", err)
	}
	// Each call returns an independent slice.
	role.Permissions[0] = "tampered"
	if model.OwnerRole().Permissions[0] == "tampered" {
		t.Error("OwnerRole shares its slice between calls")
	}
}

// TestActorIsNil lists what is a null actor and what is not.
func TestActorIsNil(t *testing.T) {
	t.Parallel()
	const participant = "01900000-0000-7000-8000-000000000001"
	cases := map[string]struct {
		actor model.Actor
		want  bool
	}{
		"zero actor":                  {model.Actor{}, true},
		"unknown kind":                {model.Actor{Kind: "robot", ParticipantID: participant}, true},
		"person without participant":  {model.Actor{Kind: model.ActorPerson}, true},
		"agent without participant":   {model.Actor{Kind: model.ActorAgent}, true},
		"api key without participant": {model.Actor{Kind: model.ActorAPIKey}, true},
		"empty kind with participant": {model.Actor{ParticipantID: participant}, true},
		"person":                      {model.Actor{Kind: model.ActorPerson, ParticipantID: participant}, false},
		"api key":                     {model.Actor{Kind: model.ActorAPIKey, ParticipantID: participant}, false},
		"system without participant":  {model.Actor{Kind: model.ActorSystem}, false},
		"system with participant":     {model.Actor{Kind: model.ActorSystem, ParticipantID: participant}, false},
	}
	for name, tc := range cases {
		if got := tc.actor.IsNil(); got != tc.want {
			t.Errorf("%s: IsNil = %v, want %v", name, got, tc.want)
		}
	}
}

// TestSpaceStateCodesExist pins the two codes of the contract that permission
// checks introduce to the model.
func TestSpaceStateCodesExist(t *testing.T) {
	t.Parallel()
	if model.CodeSpaceSuspended != "space_suspended" || model.CodeCapabilityInactive != "capability_inactive" {
		t.Errorf("codes = %q, %q", model.CodeSpaceSuspended, model.CodeCapabilityInactive)
	}
}
