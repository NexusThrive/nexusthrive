package main

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/gateway"
)

func TestComposedGatewayRoutesConnectBeforeLegacyFallback(t *testing.T) {
	verifier, err := gateway.NewVerifier(gateway.StaticKeys{}, gateway.VerifierConfig{Issuer: "https://issuer.test"})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := composeGateway(new(execution.Engine), verifier, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/space/kernel.v1.KernelService/ReadEvents", strings.NewReader(`{"limit":10}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"code":"unauthenticated"`) {
		t.Fatalf("Connect was not routed: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("legacy health lost: %d", w.Code)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/spaces/019a8000-0000-7000-8000-000000000001/events", nil))
	if w.Code != 404 {
		t.Fatalf("development cut exposed outside development: %d", w.Code)
	}
}
