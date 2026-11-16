package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/gateway"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

func TestConnectMoneyPreservesBodyAndReplays(t *testing.T) {
	f := newFixture(t)
	actor, err := f.engine.ResolveActor(context.Background(), f.spaceID, model.IdentityProviderClerk, subjectOperator)
	if err != nil {
		t.Fatal(err)
	}
	role, err := state.CreateRole(context.Background(), f.api, model.Actor{Kind: model.ActorSystem}, f.spaceID, model.NewRole{Key: "contract", Name: "Contract", Permissions: []string{model.PermissionCommitmentWrite}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.GrantRole(context.Background(), f.api, model.SystemClock{}, model.Actor{Kind: model.ActorSystem}, f.spaceID, model.NewGrant{ParticipantID: actor.ParticipantID, RoleID: role}); err != nil {
		t.Fatal(err)
	}
	var resource, creditor string
	if err := f.admin.QueryRow(`SELECT resource_id,holder_participant_id FROM kernel.accounts WHERE id=$1`, f.other).Scan(&resource, &creditor); err != nil {
		t.Fatal(err)
	}
	auth := func(_ context.Context, _ *http.Request, slug string) (gateway.AuthenticatedCall, error) {
		if slug != "t" {
			return gateway.AuthenticatedCall{}, model.NewError(model.CodePermissionDenied, "denied", nil)
		}
		return gateway.AuthenticatedCall{Actor: actor, SpaceID: f.spaceID, CredentialID: subjectOperator}, nil
	}
	handler, err := gateway.NewConnectServer(f.engine, auth, gateway.ConnectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	body := `{ "entries": [{"accountId":"` + f.wallet + `","resourceId":"` + resource + `","amount":"-1"},{"accountId":"` + f.other + `","resourceId":"` + resource + `","amount":"1"}], "commitment":{"debtorParticipantId":"` + actor.ParticipantID + `","creditorParticipantId":"` + creditor + `","resourceId":"` + resource + `","amount":"1","dueAt":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"} }`
	// ProtoJSON accepts both aliases. Re-encoding would change this spelling
	// and therefore the fingerprint, despite describing the same protobuf.
	body = strings.ReplaceAll(body, "accountId", "account_id")
	do := func(key, body string) (int, http.Header, string) {
		t.Helper()
		r, _ := http.NewRequest(http.MethodPost, server.URL+"/t/kernel.v1.KernelService/WriteExchange", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("Actor-System", "true")
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(raw)
	}
	status, _, first := do("key", body)
	if status != 200 {
		t.Fatalf("write %d: %s", status, first)
	}
	status, header, again := do("key", body)
	if status != 200 || first != again || header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay %d: %s", status, again)
	}
	status, _, failure := do("", body)
	if status != 400 || !strings.Contains(failure, "invalid_argument") {
		t.Fatalf("missing key %d: %s", status, failure)
	}
	status, _, failure = do("key", strings.Replace(body, `"amount":"1"`, `"amount":"2"`, 1))
	if status != 409 || !strings.Contains(failure, "already_exists") {
		t.Fatalf("conflict %d: %s", status, failure)
	}
	var storedHash []byte
	canonical, err := model.CanonicalJSON([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := model.RequestHash("kernel.exchange.write", actor, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.admin.QueryRow(`SELECT request_hash FROM kernel.idempotency_records WHERE key='key'`).Scan(&storedHash); err != nil || !bytes.Equal(storedHash, expected[:]) {
		t.Fatalf("original body was not fingerprinted: %v", err)
	}
}

func TestConnectAuthenticationRateLimitAndDeadline(t *testing.T) {
	f := newFixture(t)
	now := time.Unix(2000000000, 0)
	auth := func(ctx context.Context, r *http.Request, _ string) (gateway.AuthenticatedCall, error) {
		switch r.Header.Get("Authorization") {
		case "wait":
			<-ctx.Done()
			return gateway.AuthenticatedCall{}, ctx.Err()
		case "panic-error":
			return gateway.AuthenticatedCall{}, &execution.PanicError{Operation: "secret", Value: "secret payload"}
		}
		return gateway.AuthenticatedCall{}, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	handler, err := gateway.NewConnectServer(f.engine, auth, gateway.ConnectConfig{RequestsPerMinute: 2, Clock: func() time.Time { return now }, Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	request := func(authorization string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/t/kernel.v1.KernelService/ReadEvents", strings.NewReader(`{"limit":10}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request(""); w.Code != 401 {
		t.Fatalf("unauthenticated=%d %s", w.Code, w.Body)
	}
	if w := request("wait"); w.Code != 504 || !strings.Contains(w.Body.String(), "deadline_exceeded") {
		t.Fatalf("deadline=%d %s", w.Code, w.Body)
	}
	if w := request(""); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("limit=%d %s", w.Code, w.Body)
	}
	now = now.Add(time.Minute)
	if w := request("panic-error"); w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("panic leaked: %d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("POST", "/t/kernel.v1.KernelService/ClaimOutbox", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("outbox exposed: %d", w.Code)
	}
}

func TestConnectRejectsBinaryMoneyBeforeExecution(t *testing.T) {
	f := newFixture(t)
	handler, err := gateway.NewConnectServer(f.engine, func(context.Context, *http.Request, string) (gateway.AuthenticatedCall, error) {
		return gateway.AuthenticatedCall{Actor: model.Actor{Kind: model.ActorPerson, ParticipantID: execution.NewRequestID()}, SpaceID: f.spaceID, CredentialID: "test"}, nil
	}, gateway.ConnectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/t/kernel.v1.KernelService/WriteExchange", strings.NewReader("binary"))
	r.Header.Set("Content-Type", "application/proto")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("binary money accepted: %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
}
