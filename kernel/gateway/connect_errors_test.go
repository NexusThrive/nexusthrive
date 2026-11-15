package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	kernelv1 "github.com/nexusthrive/nexusthrive/kernel/gen/kernel/v1"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

func TestConnectErrorMappingNeverExposesInternalCauses(t *testing.T) {
	s := &ConnectServer{cfg: ConnectConfig{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	for domain, want := range map[model.Code]connect.Code{model.CodeInvalidInput: connect.CodeInvalidArgument, model.CodePermissionDenied: connect.CodePermissionDenied, model.CodeNotFound: connect.CodeNotFound, model.CodeIdempotencyConflict: connect.CodeAlreadyExists, model.CodeAlreadyDone: connect.CodeAlreadyExists, model.CodeSpaceSuspended: connect.CodeFailedPrecondition, model.CodeCapabilityInactive: connect.CodeFailedPrecondition, model.CodeRuleViolation: connect.CodeFailedPrecondition, model.CodeRetryable: connect.CodeAborted} {
		err := s.rpcError(model.NewError(domain, "public message", errors.New("secret SQL cause")))
		if connect.CodeOf(err) != want || strings.Contains(err.Error(), "secret") {
			t.Fatalf("%s mapped to %v", domain, err)
		}
		var transport *connect.Error
		if !errors.As(err, &transport) || len(transport.Details()) != 1 {
			t.Fatalf("%s lacks typed detail", domain)
		}
		detail, err := transport.Details()[0].Value()
		if err != nil {
			t.Fatal(err)
		}
		if detail.(*kernelv1.OperationError).GetMessage() != "public message" {
			t.Fatal("wrong public error detail")
		}
	}
	for _, cause := range []error{errors.New("secret error"), &execution.PanicError{Operation: "secret", Value: "secret payload"}} {
		err := s.rpcError(cause)
		if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "secret") {
			t.Fatalf("internal failure leaked: %v", err)
		}
	}
	if connect.CodeOf(s.rpcError(context.DeadlineExceeded)) != connect.CodeDeadlineExceeded {
		t.Fatal("deadline lost its transport code")
	}
}
