package gateway_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/gateway"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

func TestWebhookHTTPRegistrationAndSignedDelivery(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	actor, err := f.engine.ResolveActor(ctx, f.spaceID, model.IdentityProviderClerk, subjectOperator)
	if err != nil {
		t.Fatal(err)
	}
	role, err := state.CreateRole(ctx, f.api, model.Actor{Kind: model.ActorSystem}, f.spaceID, model.NewRole{Key: "hooks", Name: "Hooks", Permissions: []string{model.PermissionWebhookWrite, model.PermissionWebhookRead}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.GrantRole(ctx, f.api, model.SystemClock{}, model.Actor{Kind: model.ActorSystem}, f.spaceID, model.NewGrant{ParticipantID: actor.ParticipantID, RoleID: role}); err != nil {
		t.Fatal(err)
	}
	type receipt struct {
		body      []byte
		signature string
	}
	receipts := make(chan receipt, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receipts <- receipt{body, r.Header.Get("NexusThrive-Signature")}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	keys, err := gateway.NewWebhookKeys("test", map[string][]byte{"test": bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	hooks, err := gateway.NewWebhookGateway(f.engine, keys, true)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := gateway.NewConnectServer(f.engine, func(context.Context, *http.Request, string) (gateway.AuthenticatedCall, error) {
		return gateway.AuthenticatedCall{Actor: actor, SpaceID: f.spaceID, CredentialID: subjectOperator}, nil
	}, gateway.ConnectConfig{Webhooks: hooks})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"url": receiver.URL, "events": []string{"kernel.exchange.recorded.v1"}})
	request := httptest.NewRequest("POST", "/t/kernel.v1.KernelService/CreateWebhookEndpoint", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("secret response can be cached")
	}
	if response.Code != 200 {
		t.Fatalf("create %d: %s", response.Code, response.Body)
	}
	var created struct{ ID, Secret string }
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.ID == "" || !strings.HasPrefix(created.Secret, "whsec_") {
		t.Fatal(created, err)
	}
	var stored []byte
	if err := f.admin.QueryRow(`SELECT secret_ciphertext FROM kernel.webhook_endpoints WHERE id=$1`, created.ID).Scan(&stored); err != nil || bytes.Contains(stored, []byte(created.Secret)) {
		t.Fatal("secret storage", err)
	}
	var event model.Event
	err = state.InSpace(ctx, f.api, f.spaceID, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		event, err = state.AppendEventAndEnqueue(ctx, tx, model.SystemClock{}, model.NewEvent{Type: "kernel.exchange.recorded", Version: 1, Payload: json.RawMessage(`{"exchange_id":"test"}`), Actor: model.Actor{Kind: model.ActorSystem}}, model.SubscriberWebhooks)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := hooks.DispatchPage(ctx, "")
	if err != nil || cursor != f.spaceID {
		t.Fatal(cursor, err)
	}
	packet := <-receipts
	received, signature := packet.body, packet.signature
	var envelope map[string]any
	if err := json.Unmarshal(received, &envelope); err != nil || envelope["id"] != event.ID {
		t.Fatalf("body %s: %v", received, err)
	}
	parts := strings.Split(signature, ",")
	if len(parts) != 2 {
		t.Fatal(signature)
	}
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(created.Secret, "whsec_"))
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strings.TrimPrefix(parts[0], "t=") + "."))
	mac.Write(received)
	if !hmac.Equal([]byte(strings.TrimPrefix(parts[1], "v1=")), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		t.Fatal("signature invalid")
	}
	if _, err := hooks.DispatchPage(ctx, cursor); err != nil {
		t.Fatal(err)
	}
	listBody, _ := json.Marshal(map[string]any{"endpointId": created.ID, "limit": 10})
	listRequest := httptest.NewRequest("POST", "/t/kernel.v1.KernelService/ListWebhookDeliveries", bytes.NewReader(listBody))
	listRequest.Header.Set("Content-Type", "application/json")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, listRequest)
	if listed.Code != 200 || strings.Contains(listed.Body.String(), created.Secret) || strings.Contains(listed.Body.String(), "ciphertext") || !strings.Contains(listed.Body.String(), "sent") {
		t.Fatalf("unsafe delivery listing %d %s", listed.Code, listed.Body)
	}
	var replays int
	if err := f.admin.QueryRow(`SELECT count(*) FROM kernel.idempotency_records WHERE operation='kernel.webhook.create'`).Scan(&replays); err != nil || replays != 0 {
		t.Fatal("secret creation cached in idempotency", replays, err)
	}
	var attempts int
	if err := f.admin.QueryRow(`SELECT count(*) FROM kernel.webhook_delivery_attempts`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal(attempts, err)
	}
}

func TestWebhookHTTPSecretRotationIsOneTime(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	system := model.Actor{Kind: model.ActorSystem}
	actor, err := f.engine.ResolveActor(ctx, f.spaceID, model.IdentityProviderClerk, subjectOperator)
	if err != nil {
		t.Fatal(err)
	}
	role, err := state.CreateRole(ctx, f.api, system, f.spaceID, model.NewRole{Key: "rotation", Name: "Rotation", Permissions: []string{model.PermissionWebhookWrite}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.GrantRole(ctx, f.api, model.SystemClock{}, system, f.spaceID, model.NewGrant{ParticipantID: actor.ParticipantID, RoleID: role}); err != nil {
		t.Fatal(err)
	}
	keys, _ := gateway.NewWebhookKeys("test", map[string][]byte{"test": bytes.Repeat([]byte{4}, 32)})
	hooks, _ := gateway.NewWebhookGateway(f.engine, keys, true)
	id := execution.NewRequestID()
	sealed, original, err := keys.NewSecret(f.spaceID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.CreateWebhookEndpoint(ctx, execution.Call{Actor: actor, SpaceID: f.spaceID, RequestID: execution.NewRequestID()}, model.NewWebhookEndpoint{ID: id, URL: "https://example.com", Events: []string{"kernel.exchange.recorded.v1"}, Secret: sealed}); err != nil {
		t.Fatal(err)
	}
	handler, err := gateway.NewConnectServer(f.engine, func(context.Context, *http.Request, string) (gateway.AuthenticatedCall, error) {
		return gateway.AuthenticatedCall{Actor: actor, SpaceID: f.spaceID, CredentialID: subjectOperator}, nil
	}, gateway.ConnectConfig{Webhooks: hooks})
	if err != nil {
		t.Fatal(err)
	}
	request := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/t/kernel.v1.KernelService/RotateWebhookSecret", strings.NewReader(`{"endpointId":"`+id+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("cached"); w.Code != 400 {
		t.Fatal("rotation accepted cache key", w.Code, w.Body)
	}
	w := request("")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	var rotated struct{ Secret string }
	if err := json.Unmarshal(w.Body.Bytes(), &rotated); err != nil || rotated.Secret == original || rotated.Secret == "" {
		t.Fatal("new secret not presented", err)
	}
	if w := request(""); w.Code != 400 || !strings.Contains(w.Body.String(), "failed_precondition") {
		t.Fatal("overlapping rotation accepted", w.Code, w.Body)
	}
	var audits int
	if err := f.admin.QueryRow(`SELECT count(*) FROM kernel.events WHERE type='kernel.webhook.secret.rotated' AND NOT payload::text LIKE '%whsec_%'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("secret-free audit missing", audits, err)
	}
}
