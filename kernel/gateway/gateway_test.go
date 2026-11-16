package gateway_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/capability"
	"github.com/nexusthrive/nexusthrive/kernel/db/dbtest"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/gateway"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

const issuer = "https://clerk.test"

// fixture is a gateway over a disposable database.
type fixture struct {
	t          *testing.T
	server     *httptest.Server
	issuer     *gateway.DevIssuer
	spaceID    string
	wallet     string
	other      string
	engine     *execution.Engine
	admin, api *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	admin := dbtest.Database(t)
	api := dbtest.APIPool(t, admin)
	auth, err := capability.NewAuthorizer(model.KernelCatalog(), model.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := execution.NewEngine(api, auth, gateway.Operations()...)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := gateway.NewDevIssuer(issuer)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := gateway.NewVerifier(dev.Keys(), gateway.VerifierConfig{Issuer: issuer})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, issuer: dev, engine: engine, admin: admin, api: api}
	f.spaceID, f.wallet, f.other = seed(t, admin, api)
	f.server = httptest.NewServer(gateway.NewServer(engine, verifier, nil, nil))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) token(subject string) string {
	f.t.Helper()
	tok, err := f.issuer.Token(subject, time.Now().Add(time.Hour))
	if err != nil {
		f.t.Fatal(err)
	}
	return tok
}

func (f *fixture) do(method, path, token, key, body string) (*http.Response, map[string]any) {
	f.t.Helper()
	req, _ := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func (f *fixture) transferBody(amount string) string {
	return `{"from_account_id":"` + f.wallet + `","to_account_id":"` + f.other + `","amount":"` + amount + `","memo":"t"}`
}

func (f *fixture) balance(token, account string) string {
	f.t.Helper()
	_, out := f.do("GET", "/v1/spaces/"+f.spaceID+"/balances", token, "", "")
	for _, a := range out["accounts"].([]any) {
		row := a.(map[string]any)
		if row["account_id"] == account {
			return row["balance"].(string)
		}
	}
	f.t.Fatalf("account %s not listed", account)
	return ""
}

func TestTransferEndToEnd(t *testing.T) {
	f := newFixture(t)
	token := f.token(subjectOperator)
	base := "/v1/spaces/" + f.spaceID

	resp, first := f.do("POST", base+"/transfers", token, "k-1", f.transferBody("250.00"))
	if resp.StatusCode != 200 || resp.Header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("first transfer = %d %v", resp.StatusCode, first)
	}
	if got := f.balance(token, f.other); got != "250.00" {
		t.Fatalf("receiver balance = %s, want 250.00", got)
	}

	// The same key and body returns the stored response and moves nothing.
	resp, again := f.do("POST", base+"/transfers", token, "k-1", f.transferBody("250.00"))
	if resp.StatusCode != 200 || resp.Header.Get("Idempotent-Replayed") != "true" || again["exchange_id"] != first["exchange_id"] {
		t.Fatalf("replay = %d %v (replayed=%q)", resp.StatusCode, again, resp.Header.Get("Idempotent-Replayed"))
	}
	if got := f.balance(token, f.other); got != "250.00" {
		t.Fatalf("balance after replay = %s, want 250.00", got)
	}

	// The same key with another body is a conflict.
	resp, _ = f.do("POST", base+"/transfers", token, "k-1", f.transferBody("999.00"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status = %d", resp.StatusCode)
	}

	// The exchange left an event.
	_, events := f.do("GET", base+"/events", token, "", "")
	list := events["events"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["type"] != "ledger.transfer_recorded" {
		t.Fatalf("events = %v", events)
	}
}

func TestTransferRefusals(t *testing.T) {
	f := newFixture(t)
	token := f.token(subjectOperator)
	path := "/v1/spaces/" + f.spaceID + "/transfers"
	for name, tc := range map[string]struct {
		key, body string
		want      int
	}{
		"no idempotency key": {"", f.transferBody("1.00"), 400},
		"zero amount":        {"k", f.transferBody("0"), 400},
		"too many decimals":  {"k", f.transferBody("1.001"), 400},
		"unknown field":      {"k", `{"amount":"1","x":1}`, 400},
		"same account":       {"k", `{"from_account_id":"` + f.wallet + `","to_account_id":"` + f.wallet + `","amount":"1.00"}`, 422},
		"unknown account":    {"k", `{"from_account_id":"` + f.spaceID + `","to_account_id":"` + f.other + `","amount":"1.00"}`, 404},
	} {
		if resp, out := f.do("POST", path, token, tc.key, tc.body); resp.StatusCode != tc.want {
			t.Errorf("%s: status = %d (%v), want %d", name, resp.StatusCode, out, tc.want)
		}
	}
}

func TestAuthentication(t *testing.T) {
	f := newFixture(t)
	path := "/v1/spaces/" + f.spaceID + "/balances"
	expired, _ := f.issuer.Token(subjectOperator, time.Now().Add(-time.Hour))
	stranger := f.token("user_nobody")
	forged := forgeNone(subjectOperator)

	for name, tc := range map[string]struct {
		token string
		want  int
	}{
		"no token":         {"", 401},
		"garbage":          {"abc", 401},
		"alg none":         {forged, 401},
		"expired":          {expired, 401},
		"unknown identity": {stranger, 403},
		"valid operator":   {f.token(subjectOperator), 200},
	} {
		if resp, _ := f.do("GET", path, tc.token, "", ""); resp.StatusCode != tc.want {
			t.Errorf("%s: status = %d, want %d", name, resp.StatusCode, tc.want)
		}
	}

	// A valid identity in a space it does not belong to learns nothing.
	foreign := "/v1/spaces/01900000-0000-7000-8000-00000000abcd/balances"
	if resp, _ := f.do("GET", foreign, f.token(subjectOperator), "", ""); resp.StatusCode != 403 {
		t.Errorf("foreign space status = %d, want 403", resp.StatusCode)
	}
}

func TestVerifierRejectsWrongIssuerAndSigner(t *testing.T) {
	good, _ := gateway.NewDevIssuer(issuer)
	verifier, _ := gateway.NewVerifier(good.Keys(), gateway.VerifierConfig{Issuer: issuer})
	other, _ := gateway.NewDevIssuer("https://evil.test")
	tok, _ := other.Token("x", time.Now().Add(time.Hour))
	if _, err := verifier.Verify(context.Background(), tok); err == nil {
		t.Fatal("a token signed by another key was accepted")
	}
	sameKeyWrongIssuer, _ := gateway.NewVerifier(other.Keys(), gateway.VerifierConfig{Issuer: issuer})
	if _, err := sameKeyWrongIssuer.Verify(context.Background(), tok); err == nil {
		t.Fatal("a token of another issuer was accepted")
	}
	if _, err := verifier.Verify(context.Background(), mustToken(t, good)); err != nil {
		t.Fatalf("a valid token was refused: %v", err)
	}
}

func TestRemoteKeysCachesAndSurvivesOutage(t *testing.T) {
	dev, _ := gateway.NewDevIssuer(issuer)
	fetches, down := 0, false
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		if down {
			http.Error(w, "down", 500)
			return
		}
		_, _ = w.Write(dev.JWKS())
	}))
	defer jwks.Close()
	keys := &gateway.RemoteKeys{URL: jwks.URL, AllowInsecureHTTP: true}
	verifier, _ := gateway.NewVerifier(keys, gateway.VerifierConfig{Issuer: issuer})
	tok := mustToken(t, dev)
	for i := 0; i < 3; i++ {
		if _, err := verifier.Verify(context.Background(), tok); err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
	}
	down = true
	if _, err := verifier.Verify(context.Background(), tok); err != nil {
		t.Fatalf("verify during outage: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("fetches = %d, want 1", fetches)
	}
}

func mustToken(t *testing.T, dev *gateway.DevIssuer) string {
	t.Helper()
	tok, err := dev.Token("someone", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// forgeNone builds an unsigned token that claims alg none.
func forgeNone(subject string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none","kid":"dev-1"}`)) + "." +
		enc([]byte(`{"iss":"`+issuer+`","sub":"`+subject+`","exp":4102444800}`)) + "."
}
