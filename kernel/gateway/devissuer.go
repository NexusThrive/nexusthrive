package gateway

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

// DevIssuer signs tokens with a key it generates, for local development and
// tests. Its tokens pass through the same Verifier as the ones of Clerk; only
// the key source differs. It must never be mounted in production.
type DevIssuer struct {
	Issuer string
	kid    string
	key    *rsa.PrivateKey
}

// NewDevIssuer generates a key for issuer.
func NewDevIssuer(issuer string) (*DevIssuer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate dev key: %w", err)
	}
	return &DevIssuer{Issuer: issuer, kid: "dev-1", key: key}, nil
}

// Keys is the key source that verifies the tokens of this issuer.
func (d *DevIssuer) Keys() KeySource { return StaticKeys{d.kid: &d.key.PublicKey} }

// JWKS is the public key set, as a JWKS document.
func (d *DevIssuer) JWKS() []byte {
	doc := map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": d.kid,
		"n": base64.RawURLEncoding.EncodeToString(d.key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(d.key.E)).Bytes()),
	}}}
	raw, _ := json.Marshal(doc)
	return raw
}

// Token signs a token for subject that expires at expires.
func (d *DevIssuer) Token(subject string, expires time.Time) (string, error) {
	return d.sign(map[string]any{"alg": "RS256", "typ": "JWT", "kid": d.kid},
		map[string]any{"iss": d.Issuer, "sub": subject, "exp": expires.Unix(), "iat": time.Now().Unix()})
}

func (d *DevIssuer) sign(header, claims map[string]any) (string, error) {
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, d.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
