package gateway

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrInvalidToken is the only answer of Verify to a token it refuses. It says
// nothing of the reason, so a caller cannot probe which check failed.
var ErrInvalidToken = errors.New("invalid token")

// KeySource finds the public key a token was signed with.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// Claims are the claims of a verified token the gateway uses.
type Claims struct {
	Subject string
	Expires time.Time
}

// VerifierConfig fixes what a token must say to be accepted.
type VerifierConfig struct {
	// Issuer is the exact iss claim.
	Issuer string
	// AuthorizedParty, when set, is the exact azp claim (Clerk names the
	// frontend that asked for the session there).
	AuthorizedParty string
	// Clock is the time source; nil means the real clock.
	Clock func() time.Time
	// Leeway tolerates a small skew in exp and nbf; zero means 5 seconds.
	Leeway time.Duration
}

// Verifier checks RS256 tokens against a KeySource.
type Verifier struct {
	cfg  VerifierConfig
	keys KeySource
}

// NewVerifier builds a verifier. An empty issuer is refused: a token from any
// issuer must never pass.
func NewVerifier(keys KeySource, cfg VerifierConfig) (*Verifier, error) {
	if keys == nil || cfg.Issuer == "" {
		return nil, errors.New("a verifier needs a key source and an issuer")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 5 * time.Second
	}
	return &Verifier{cfg: cfg, keys: keys}, nil
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type tokenClaims struct {
	Iss string          `json:"iss"`
	Sub string          `json:"sub"`
	Azp string          `json:"azp"`
	Exp *float64        `json:"exp"`
	Nbf *float64        `json:"nbf"`
	Aud json.RawMessage `json:"aud"`
}

// Verify checks the signature (RS256 only: "none" and symmetric algorithms are
// refused), the issuer, the authorized party, the expiry and not-before, and
// returns the claims. Every failure is ErrInvalidToken.
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 8192 {
		return Claims{}, ErrInvalidToken
	}
	var header tokenHeader
	if err := decodeSegment(parts[0], &header); err != nil || header.Alg != "RS256" || header.Kid == "" {
		return Claims{}, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	key, err := v.keys.Key(ctx, header.Kid)
	if err != nil || key == nil {
		return Claims{}, ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims tokenClaims
	if err := decodeSegment(parts[1], &claims); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if claims.Iss != v.cfg.Issuer || claims.Sub == "" || claims.Exp == nil {
		return Claims{}, ErrInvalidToken
	}
	if v.cfg.AuthorizedParty != "" && claims.Azp != v.cfg.AuthorizedParty {
		return Claims{}, ErrInvalidToken
	}
	now := v.cfg.Clock()
	expires := time.Unix(int64(*claims.Exp), 0)
	if !now.Before(expires.Add(v.cfg.Leeway)) {
		return Claims{}, ErrInvalidToken
	}
	if claims.Nbf != nil && now.Add(v.cfg.Leeway).Before(time.Unix(int64(*claims.Nbf), 0)) {
		return Claims{}, ErrInvalidToken
	}
	return Claims{Subject: claims.Sub, Expires: expires}, nil
}

func decodeSegment(segment string, into any) error {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// StaticKeys is a KeySource over a fixed set of keys, for tests and the local
// development issuer.
type StaticKeys map[string]*rsa.PublicKey

// Key returns the key named kid.
func (s StaticKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if key, ok := s[kid]; ok {
		return key, nil
	}
	return nil, errors.New("unknown key id")
}

// RemoteKeys fetches a JWKS document (the keys of Clerk) and caches it. A key
// id it does not know triggers one refresh, at most once per MinRefresh, so a
// flood of forged kids cannot turn the gateway into a client of the issuer. If
// the issuer is down the cached keys keep serving.
type RemoteKeys struct {
	URL        string
	Client     *http.Client
	MinRefresh time.Duration
	Clock      func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// Key returns the key named kid, refreshing the cache when it is unknown.
func (r *RemoteKeys) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key, ok := r.keys[kid]; ok {
		return key, nil
	}
	clock := r.Clock
	if clock == nil {
		clock = time.Now
	}
	minRefresh := r.MinRefresh
	if minRefresh == 0 {
		minRefresh = 30 * time.Second
	}
	if r.keys != nil && clock().Sub(r.fetchedAt) < minRefresh {
		return nil, errors.New("unknown key id")
	}
	fetched, err := r.fetch(ctx)
	r.fetchedAt = clock()
	if err != nil {
		return nil, err
	}
	r.keys = fetched
	if key, ok := r.keys[kid]; ok {
		return key, nil
	}
	return nil, errors.New("unknown key id")
}

func (r *RemoteKeys) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build jwks request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil || len(n) < 256 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return keys, nil
}
