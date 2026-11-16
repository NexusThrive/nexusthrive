package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWebhookSecretBindingAndSignature(t *testing.T) {
	keys, err := NewWebhookKeys("k1", map[string][]byte{"k1": bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	sealed, secret, err := keys.NewSecret("space-a", "endpoint-a")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed.Ciphertext, []byte(secret)) {
		t.Fatal("plaintext persisted")
	}
	raw, err := keys.Open("space-a", "endpoint-a", sealed)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if !bytes.Equal(raw, encoded) || len(raw) != 32 {
		t.Fatal("wrong secret")
	}
	for _, pair := range [][2]string{{"space-b", "endpoint-a"}, {"space-a", "endpoint-b"}} {
		if _, err := keys.Open(pair[0], pair[1], sealed); err == nil {
			t.Fatal("swapped secret accepted")
		}
	}
	other, _, _ := keys.NewSecret("space-a", "endpoint-a")
	if bytes.Equal(other.Nonce, sealed.Nonce) {
		t.Fatal("nonce reused")
	}
	body := []byte(`{"id":"stable"}`)
	at := time.Unix(1700000000, 0)
	signature := WebhookSignature(at, body, raw)
	mac := hmac.New(sha256.New, raw)
	mac.Write(append([]byte("1700000000."), body...))
	want := "t=1700000000,v1=" + hex.EncodeToString(mac.Sum(nil))
	if signature != want {
		t.Fatalf("signature %s", signature)
	}
	sealed.Ciphertext[0] ^= 1
	if _, err := keys.Open("space-a", "endpoint-a", sealed); err == nil {
		t.Fatal("tampered secret accepted")
	}
	if _, err := NewWebhookKeys("missing", map[string][]byte{"k1": raw}); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err := NewWebhookKeys("k1", map[string][]byte{"k1": {1}}); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestWebhookDestinationRejectsUnsafeAddresses(t *testing.T) {
	policy := newWebhookNetwork(false)
	policy.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	for _, url := range []string{"http://example.com", "https://u:p@example.com", "https://127.0.0.1", "https://[::1]", "https://10.0.0.1", "https://169.254.169.254", "https://[::ffff:127.0.0.1]", "https://[2001:db8::1]", "https://example.com/#fragment"} {
		if err := policy.Validate(context.Background(), url); err == nil {
			t.Errorf("accepted %s", url)
		}
	}
	if err := policy.Validate(context.Background(), "https://example.com/hook"); err != nil {
		t.Fatal(err)
	}
	policy.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("192.168.1.1")}}, nil
	}
	if err := policy.Validate(context.Background(), "https://example.com"); err == nil {
		t.Fatal("mixed DNS accepted")
	}
	local := newWebhookNetwork(true)
	if err := local.Validate(context.Background(), "http://127.0.0.1:12345/hook"); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookDialRechecksDNSAndNeverUsesEnvironmentProxy(t *testing.T) {
	policy := newWebhookNetwork(false)
	count := 0
	policy.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		count++
		if count == 1 {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if err := policy.Validate(context.Background(), "https://example.com/hook"); err != nil {
		t.Fatal(err)
	}
	called := false
	policy.dial = func(context.Context, string, string) (net.Conn, error) {
		called = true
		return nil, fmt.Errorf("unexpected dial")
	}
	client := policy.client()
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
	if response, err := client.Get("https://example.com/hook"); err == nil {
		response.Body.Close()
		t.Fatal("DNS rebinding accepted")
	}
	if called {
		t.Fatal("dialed a private address")
	}
}

func TestWebhookDualSignatureWindow(t *testing.T) {
	keys, err := NewWebhookKeys("app", map[string][]byte{"app": bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	old, _, _ := keys.NewSecret("space", "endpoint")
	current, _, _ := keys.NewSecret("space", "endpoint")
	until := time.Unix(1800000000, 0)
	job := model.WebhookJob{SpaceID: "space", WebhookDelivery: model.WebhookDelivery{EndpointID: "endpoint"}, Secret: current, PreviousSecret: &old, PreviousValidUntil: &until, Body: []byte(`{"id":"same"}`)}
	hooks := &WebhookGateway{keys: keys}
	signature, err := hooks.signWebhookJob(job, until.Add(-time.Second))
	if err != nil || len(strings.Split(signature, ",")) != 3 {
		t.Fatal(signature, err)
	}
	previousRaw, _ := keys.Open("space", "endpoint", old)
	currentRaw, _ := keys.Open("space", "endpoint", current)
	_, tail, _ := strings.Cut(WebhookSignature(until.Add(-time.Second), job.Body, previousRaw), ",")
	if signature != WebhookSignature(until.Add(-time.Second), job.Body, currentRaw)+","+tail {
		t.Fatal("dual signature mismatch")
	}
	signature, err = hooks.signWebhookJob(job, until)
	if err != nil || signature != WebhookSignature(until, job.Body, currentRaw) {
		t.Fatal("old key signed at expiry", signature, err)
	}
	job.PreviousSecret.KeyID = "removed"
	if _, err := hooks.signWebhookJob(job, until.Add(-time.Second)); err == nil {
		t.Fatal("missing active old key ignored")
	}
	if _, err := hooks.signWebhookJob(job, until); err != nil {
		t.Fatal("expired key still required", err)
	}
}
