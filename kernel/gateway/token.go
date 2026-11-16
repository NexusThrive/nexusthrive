package gateway

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
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
	// AuthorizedParties is an exact allowlist of origins, without wildcards.
	AuthorizedParties []string
	// RequireAuthorizedParty refuses an empty allowlist outside development.
	RequireAuthorizedParty bool
	// Audience, when configured, must occur in the token's aud claim.
	Audience string
	// OnReject records a fixed diagnostic category, never token contents.
	OnReject func(string)
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
	parties := append([]string(nil), cfg.AuthorizedParties...)
	if cfg.AuthorizedParty != "" {
		parties = append(parties, cfg.AuthorizedParty)
	}
	for _, party := range parties {
		if strings.TrimSpace(party) == "" || strings.Contains(party, "*") {
			return nil, errors.New("authorized parties must be exact nonempty origins")
		}
	}
	if cfg.RequireAuthorizedParty && len(parties) == 0 {
		return nil, errors.New("a production verifier needs authorized parties")
	}
	if cfg.Leeway < 0 {
		return nil, errors.New("token leeway must not be negative")
	}
	cfg.AuthorizedParties = parties
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
	Exp json.RawMessage `json:"exp"`
	Nbf json.RawMessage `json:"nbf"`
	Aud json.RawMessage `json:"aud"`
}

// Verify checks the signature (RS256 only: "none" and symmetric algorithms are
// refused), the issuer, the authorized party, the expiry and not-before, and
// returns the claims. Every failure is ErrInvalidToken.
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	reject := func(reason string) (Claims, error) {
		if v.cfg.OnReject != nil {
			v.cfg.OnReject(reason)
		}
		return Claims{}, ErrInvalidToken
	}
	if ctx.Err() != nil {
		return reject("context")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 8192 {
		return reject("format")
	}
	var header tokenHeader
	if err := decodeSegment(parts[0], &header); err != nil || header.Alg != "RS256" || header.Kid == "" {
		return reject("algorithm_or_key_id")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return reject("signature_encoding")
	}
	key, err := v.keys.Key(ctx, header.Kid)
	if err != nil || key == nil {
		return reject("keys")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return reject("signature")
	}
	var claims tokenClaims
	if err := decodeSegment(parts[1], &claims); err != nil {
		return reject("claims")
	}
	if claims.Iss != v.cfg.Issuer || strings.TrimSpace(claims.Sub) == "" {
		return reject("issuer_or_subject")
	}
	if len(v.cfg.AuthorizedParties) > 0 && !containsParty(v.cfg.AuthorizedParties, claims.Azp) {
		return reject("authorized_party")
	}
	if v.cfg.Audience != "" && !matchesAudience(claims.Aud, v.cfg.Audience) {
		return reject("audience")
	}
	now := v.cfg.Clock()
	expires, err := numericDate(claims.Exp)
	if err != nil {
		return reject("expiry_format")
	}
	if !now.Before(expires.Add(v.cfg.Leeway)) {
		return reject("expiry")
	}
	if len(claims.Nbf) > 0 {
		notBefore, err := numericDate(claims.Nbf)
		if err != nil || now.Add(v.cfg.Leeway).Before(notBefore) {
			return reject("not_before")
		}
	}
	return Claims{Subject: claims.Sub, Expires: expires}, nil
}

func containsParty(parties []string, party string) bool {
	for _, allowed := range parties {
		if party == allowed {
			return true
		}
	}
	return false
}

func matchesAudience(raw json.RawMessage, expected string) bool {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == expected
	}
	var multiple []string
	return json.Unmarshal(raw, &multiple) == nil && containsParty(multiple, expected)
}

// numericDate rejects missing, null and nonnumeric dates before Unix conversion.
func numericDate(raw json.RawMessage) (time.Time, error) {
	if len(raw) == 0 || raw[0] == '"' || string(raw) == "null" {
		return time.Time{}, ErrInvalidToken
	}
	var value *float64
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return time.Time{}, ErrInvalidToken
	}
	seconds := *value
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds >= float64(math.MaxInt64) {
		return time.Time{}, ErrInvalidToken
	}
	whole := int64(seconds)
	return time.Unix(whole, int64((seconds-float64(whole))*1e9)), nil
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
