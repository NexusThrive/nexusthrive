package gateway

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func signPolicyToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "key"})
	body, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestTokenPolicyAuthorizedPartiesAndAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000000000, 0)
	v, err := NewVerifier(StaticKeys{"key": &key.PublicKey}, VerifierConfig{Issuer: "https://issuer.test", AuthorizedParties: []string{"https://one.test", "https://two.test"}, Audience: "kernel", Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		azp   any
		aud   any
		valid bool
	}{
		{"first origin", "https://one.test", "kernel", true}, {"second origin", "https://two.test", []string{"other", "kernel"}, true},
		{"wrong origin", "https://evil.test", "kernel", false}, {"missing origin", nil, "kernel", false}, {"wrong audience", "https://one.test", "other", false}, {"missing audience", "https://one.test", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]any{"iss": "https://issuer.test", "sub": "person", "exp": now.Add(time.Minute).Unix(), "azp": tc.azp, "aud": tc.aud}
			_, err := v.Verify(context.Background(), signPolicyToken(t, key, claims))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if err != nil && !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("non-generic refusal: %v", err)
			}
		})
	}
	if _, err := NewVerifier(StaticKeys{"key": &key.PublicKey}, VerifierConfig{Issuer: "https://issuer.test", RequireAuthorizedParty: true}); err == nil {
		t.Fatal("production verifier accepted no authorized origins")
	}
}

func TestJWKSBoundedOutageAndFailedInitialRefresh(t *testing.T) {
	issuer, err := NewDevIssuer("https://issuer.test")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Keys []struct {
			Kid string `json:"kid"`
		}
	}
	if err := json.Unmarshal(issuer.JWKS(), &document); err != nil {
		t.Fatal(err)
	}
	kid := document.Keys[0].Kid
	now := time.Unix(2000000000, 0)
	var down atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if down.Load() {
			http.Error(w, "down", 500)
			return
		}
		_, _ = w.Write(issuer.JWKS())
	}))
	defer server.Close()
	keys := &RemoteKeys{URL: server.URL, AllowInsecureHTTP: true, Clock: func() time.Time { return now }}
	if _, err := keys.Key(context.Background(), kid); err != nil {
		t.Fatal(err)
	}
	down.Store(true)
	now = now.Add(6 * time.Minute)
	if _, err := keys.Key(context.Background(), kid); err != nil {
		t.Fatalf("bounded outage rejected cached key: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("stale known key did not refresh: %d calls", calls.Load())
	}
	now = now.Add(54 * time.Minute)
	if _, err := keys.Key(context.Background(), kid); err == nil {
		t.Fatal("accepted key after one hour without a valid download")
	}
	initial := &RemoteKeys{URL: server.URL, AllowInsecureHTTP: true, Clock: func() time.Time { return now }}
	before := calls.Load()
	for i := 0; i < 5; i++ {
		if _, err := initial.Key(context.Background(), "forged"); err == nil {
			t.Fatal("accepted unknown key")
		}
	}
	if calls.Load() != before+1 {
		t.Fatalf("failed initial refresh was not throttled: %d calls", calls.Load()-before)
	}
}

func TestJWKSConcurrentUnknownKeysShareRefresh(t *testing.T) {
	issuer, _ := NewDevIssuer("https://issuer.test")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = w.Write(issuer.JWKS()) }))
	defer server.Close()
	keys := &RemoteKeys{URL: server.URL, AllowInsecureHTTP: true}
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() { defer group.Done(); _, _ = keys.Key(context.Background(), "unknown") }()
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("unknown-key flood triggered %d requests", calls.Load())
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := keys.Key(cancelled, "unknown"); err == nil {
		t.Fatal("cancelled request accepted")
	}
}

func TestJWKSRejectsAmbiguousDocumentsAndInsecureOrigins(t *testing.T) {
	issuer, _ := NewDevIssuer("https://issuer.test")
	var document struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(issuer.JWKS(), &document); err != nil {
		t.Fatal(err)
	}
	duplicated, _ := json.Marshal(map[string]any{"keys": append(document.Keys, document.Keys[0])})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(duplicated) }))
	defer server.Close()
	keys := &RemoteKeys{URL: server.URL, AllowInsecureHTTP: true}
	if _, err := keys.Key(context.Background(), "unknown"); err == nil || keys.keys != nil {
		t.Fatal("ambiguous document became a valid cache")
	}
	if _, err := (&RemoteKeys{URL: server.URL}).Key(context.Background(), "unknown"); err == nil {
		t.Fatal("production JWKS accepted HTTP")
	}
}

func TestTokenPolicyDatesAndDiagnosticPrivacy(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000000000, 0)
	var diagnostic string
	v, err := NewVerifier(StaticKeys{"key": &key.PublicKey}, VerifierConfig{Issuer: "https://issuer.test", Clock: func() time.Time { return now }, OnReject: func(reason string) { diagnostic = reason }})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"expired":        func(c map[string]any) { c["exp"] = now.Add(-time.Minute).Unix() },
		"missing expiry": func(c map[string]any) { delete(c, "exp") },
		"future nbf":     func(c map[string]any) { c["nbf"] = now.Add(time.Minute).Unix() },
		"null nbf":       func(c map[string]any) { c["nbf"] = nil },
		"string expiry":  func(c map[string]any) { c["exp"] = "9999999999" },
		"wrong issuer":   func(c map[string]any) { c["iss"] = "https://evil.test" },
	} {
		t.Run(name, func(t *testing.T) {
			claims := map[string]any{"iss": "https://issuer.test", "sub": "sensitive-subject", "exp": now.Add(time.Minute).Unix()}
			mutate(claims)
			token := signPolicyToken(t, key, claims)
			diagnostic = ""
			_, err := v.Verify(context.Background(), token)
			if !errors.Is(err, ErrInvalidToken) || diagnostic == "" || diagnostic == token || diagnostic == "sensitive-subject" {
				t.Fatalf("refusal or privacy failure: %v, %q", err, diagnostic)
			}
		})
	}
}

func TestJWKSRotationRemovesOldKeysAndInvalidRefreshDoesNotRejuvenate(t *testing.T) {
	first, _ := NewDevIssuer("https://issuer.test")
	second, _ := NewDevIssuer("https://issuer.test")
	var rotated struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(second.JWKS(), &rotated); err != nil {
		t.Fatal(err)
	}
	rotated.Keys[0]["kid"] = "replacement"
	secondDocument, err := json.Marshal(rotated)
	if err != nil {
		t.Fatal(err)
	}
	kid := func(raw []byte) string {
		var d struct {
			Keys []struct {
				Kid string `json:"kid"`
			}
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		return d.Keys[0].Kid
	}
	var body atomic.Value
	body.Store(first.JWKS())
	now := time.Unix(2000000000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body.Load().([]byte)) }))
	defer server.Close()
	keys := &RemoteKeys{URL: server.URL, AllowInsecureHTTP: true, Clock: func() time.Time { return now }}
	if _, err := keys.Key(context.Background(), kid(first.JWKS())); err != nil {
		t.Fatal(err)
	}
	body.Store(secondDocument)
	now = now.Add(5 * time.Minute)
	if _, err := keys.Key(context.Background(), kid(first.JWKS())); err == nil {
		t.Fatal("removed key remained accepted")
	}
	if _, err := keys.Key(context.Background(), kid(secondDocument)); err != nil {
		t.Fatal(err)
	}
	lastSuccess := keys.fetchedAt
	body.Store([]byte(`{"keys":[]}`))
	now = now.Add(6 * time.Minute)
	if _, err := keys.Key(context.Background(), kid(secondDocument)); err != nil {
		t.Fatal("invalid refresh erased a bounded cache")
	}
	if !keys.fetchedAt.Equal(lastSuccess) {
		t.Fatal("invalid refresh prolonged cache age")
	}
	now = lastSuccess.Add(time.Hour)
	if _, err := keys.Key(context.Background(), kid(secondDocument)); err == nil {
		t.Fatal("invalid refresh kept a key alive beyond its maximum age")
	}
}

func TestJWKSRejectsOversizeAndCrossOriginRedirect(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer redirect.Close()
	if _, err := (&RemoteKeys{URL: redirect.URL, AllowInsecureHTTP: true}).Key(context.Background(), "key"); err == nil || destinationCalls.Load() != 0 {
		t.Fatal("followed a JWKS redirect to another origin")
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, (1<<20)+1)) }))
	defer large.Close()
	if _, err := (&RemoteKeys{URL: large.URL, AllowInsecureHTTP: true}).Key(context.Background(), "key"); err == nil {
		t.Fatal("accepted oversized JWKS")
	}
}
