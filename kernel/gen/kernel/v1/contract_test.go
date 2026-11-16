package kernelv1_test

import (
	"testing"

	kernelv1 "github.com/nexusthrive/nexusthrive/kernel/gen/kernel/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// TestOperationPoliciesAreDiscoverable guards the authority metadata consumed
// by the engine and read-only MCP generation, rather than a separate registry.
func TestOperationPoliciesAreDiscoverable(t *testing.T) {
	service := kernelv1.File_kernel_v1_kernel_proto.Services().ByName("KernelService")
	for _, tc := range []struct {
		method      string
		name        string
		risk        kernelv1.Risk
		idempotent  bool
		permissions []string
	}{
		{"WriteExchange", "kernel.exchange.write", kernelv1.Risk_RISK_MONEY, true, []string{"kernel.exchange.write", "kernel.commitment.write"}},
		{"ReadEvents", "kernel.event.read", kernelv1.Risk_RISK_READ, false, []string{"kernel.event.read"}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			method := service.Methods().ByName(protoreflect.Name(tc.method))
			policy := proto.GetExtension(method.Options(), kernelv1.E_Operation).(*kernelv1.OperationPolicy)
			if policy.GetName() != tc.name || policy.GetRisk() != tc.risk || policy.GetIdempotent() != tc.idempotent || policy.GetCapability() != "" {
				t.Fatalf("unexpected policy: %v", policy)
			}
			if len(policy.GetPermissions()) != len(tc.permissions) {
				t.Fatalf("permissions: %v", policy.GetPermissions())
			}
			for i, key := range tc.permissions {
				if policy.GetPermissions()[i] != key {
					t.Errorf("permission %d: got %q, want %q", i, policy.GetPermissions()[i], key)
				}
			}
		})
	}
}

// TestExactValuesSurviveJSON protects decimal and cursor precision across SDKs.
func TestExactValuesSurviveJSON(t *testing.T) {
	entry := &kernelv1.EntryInput{Amount: "9007199254740993.01"}
	body, err := protojson.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var decoded kernelv1.EntryInput
	if err := protojson.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetAmount() != entry.GetAmount() {
		t.Fatalf("decimal lost precision: %s", body)
	}
	var request kernelv1.ReadEventsRequest
	if err := protojson.Unmarshal([]byte(`{"fromSeq":"9007199254740993","limit":10}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.GetFromSeq() != 9007199254740993 {
		t.Fatalf("cursor lost precision: %v", request.GetFromSeq())
	}
}

// TestRequestsCarryNoAssertedAuthority prevents callers from supplying an actor
// or selecting another space through the operation body.
func TestRequestsCarryNoAssertedAuthority(t *testing.T) {
	for _, message := range []proto.Message{&kernelv1.WriteExchangeRequest{}, &kernelv1.ReadEventsRequest{}} {
		fields := message.ProtoReflect().Descriptor().Fields()
		for _, name := range []protoreflect.Name{"actor", "identity_id", "participant_id", "space_id", "actor_system"} {
			if fields.ByName(name) != nil {
				t.Errorf("%s exposes %s", message.ProtoReflect().Descriptor().Name(), name)
			}
		}
	}
	if (&kernelv1.WriteExchangeRequest{}).ProtoReflect().Descriptor().Fields().ByName("idempotency_key") == nil {
		t.Fatal("money request needs a contract idempotency key")
	}
	for _, name := range []string{"IDEMPOTENCY_CONFLICT", "RETRYABLE"} {
		if _, ok := kernelv1.ErrorCode_value["ERROR_CODE_"+name]; !ok {
			t.Errorf("missing error code %s", name)
		}
	}
}
