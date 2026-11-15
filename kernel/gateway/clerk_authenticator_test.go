package gateway_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/gateway"
)

func TestClerkAuthenticatorResolvesSlugWithoutProvisioning(t *testing.T) {
	f := newFixture(t)
	verifier, err := gateway.NewVerifier(f.issuer.Keys(), gateway.VerifierConfig{Issuer: issuer})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := gateway.NewClerkAuthenticator(f.engine, verifier)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/t/kernel.v1.KernelService/ReadEvents", nil)
	r.Header.Set("Authorization", "Bearer "+f.token(subjectOperator))
	r.Header.Set("Identity-Id", "asserted-identity")
	r.Header.Set("Actor-System", "true")
	resolved, err := auth(context.Background(), r, "t")
	if err != nil || resolved.SpaceID != f.spaceID || resolved.CredentialID == "asserted-identity" || resolved.Actor.Kind == "system" {
		t.Fatalf("invalid resolution: %+v, %v", resolved, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := auth(cancelled, r, "t"); !errors.Is(err, context.Canceled) {
		t.Fatalf("authentication swallowed cancellation: %v", err)
	}
	handler, err := gateway.NewConnectServer(f.engine, auth, gateway.ConnectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	request := func(slug, subject string) (int, string) {
		r := httptest.NewRequest("POST", "/"+slug+"/kernel.v1.KernelService/ReadEvents", strings.NewReader(`{"limit":10}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+f.token(subject))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if status, body := request("t", subjectOperator); status != 200 {
		t.Fatalf("authenticated read: %d %s", status, body)
	}
	var before, after int
	if err := f.admin.QueryRow(`SELECT count(*) FROM kernel.identities`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	status, unknownSpace := request("missing", subjectOperator)
	if status != 403 {
		t.Fatalf("unknown space: %d %s", status, unknownSpace)
	}
	status, unknownIdentity := request("t", "unknown-person")
	if status != 403 || unknownSpace != unknownIdentity {
		t.Fatalf("enumerating error: %s vs %s", unknownSpace, unknownIdentity)
	}
	if err := f.admin.QueryRow(`SELECT count(*) FROM kernel.identities`).Scan(&after); err != nil || before != after {
		t.Fatalf("first entry provisioned identity: %v", err)
	}
}
