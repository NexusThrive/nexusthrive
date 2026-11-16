package gateway

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// RemoteKeys keeps a bounded cache and shares refresh attempts per issuer.
// Configure its public fields before first use; they are immutable afterwards.
type RemoteKeys struct {
	URL        string
	Client     *http.Client
	MinRefresh time.Duration
	FreshFor   time.Duration
	MaxAge     time.Duration
	Clock      func() time.Time
	// AllowInsecureHTTP is only for explicitly configured development and tests.
	AllowInsecureHTTP bool
	mu                sync.Mutex
	keys              map[string]*rsa.PublicKey
	fetchedAt         time.Time
	lastAttempt       time.Time
	refreshing        chan struct{}
}

// Key never extends cache age after a failed download. Waiting for another
// refresh respects the caller's context rather than waiting on a network mutex.
func (r *RemoteKeys) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	clock := r.Clock
	if clock == nil {
		clock = time.Now
	}
	fresh, maxAge, minRefresh := r.FreshFor, r.MaxAge, r.MinRefresh
	if fresh == 0 {
		fresh = 5 * time.Minute
	}
	if maxAge == 0 {
		maxAge = time.Hour
	}
	if minRefresh == 0 {
		minRefresh = 30 * time.Second
	}
	if fresh < 0 || maxAge < fresh || minRefresh < 0 || kid == "" {
		return nil, ErrInvalidToken
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.mu.Lock()
		now := clock()
		key := r.keys[kid]
		age := now.Sub(r.fetchedAt)
		valid := key != nil && !r.fetchedAt.IsZero() && age >= 0 && age < maxAge
		if valid && age < fresh {
			r.mu.Unlock()
			return key, nil
		}
		if pending := r.refreshing; pending != nil {
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending:
				continue
			}
		}
		if !r.lastAttempt.IsZero() && now.Sub(r.lastAttempt) < minRefresh {
			r.mu.Unlock()
			if valid {
				return key, nil
			}
			return nil, ErrInvalidToken
		}
		r.lastAttempt = now
		r.refreshing = make(chan struct{})
		r.mu.Unlock()
		fetched, err := r.fetch(ctx)
		r.mu.Lock()
		if err == nil {
			r.keys = fetched
			r.fetchedAt = clock()
		}
		key = r.keys[kid]
		age = clock().Sub(r.fetchedAt)
		valid = key != nil && !r.fetchedAt.IsZero() && age >= 0 && age < maxAge
		close(r.refreshing)
		r.refreshing = nil
		r.mu.Unlock()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if valid {
			return key, nil
		}
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidToken
	}
}

func (r *RemoteKeys) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	origin, err := url.Parse(r.URL)
	if err != nil || origin.Host == "" || origin.User != nil || (origin.Scheme != "https" && !(origin.Scheme == "http" && r.AllowInsecureHTTP)) {
		return nil, errors.New("JWKS needs an HTTPS origin")
	}
	client := http.Client{}
	if r.Client != nil {
		client = *r.Client
	}
	priorRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != origin.Scheme || req.URL.Host != origin.Host || len(via) >= 10 {
			return errors.New("JWKS redirect is not allowed")
		}
		if priorRedirect != nil {
			return priorRedirect(req, via)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build JWKS request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch JWKS: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read JWKS: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, errors.New("JWKS exceeds the size limit")
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
			Use string `json:"use"`
			Alg string `json:"alg"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	seen := map[string]bool{}
	for _, k := range doc.Keys {
		if k.Kid != "" && seen[k.Kid] {
			return nil, errors.New("duplicate JWKS key id")
		}
		seen[k.Kid] = true
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		exponent, errE := base64.RawURLEncoding.DecodeString(k.E)
		modulus := new(big.Int).SetBytes(n)
		e := new(big.Int).SetBytes(exponent)
		if errN != nil || errE != nil || modulus.BitLen() < 2048 || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 2147483647 || e.Int64()%2 == 0 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: modulus, E: int(e.Int64())}
	}
	if len(keys) == 0 {
		return nil, errors.New("JWKS contains no admissible keys")
	}
	return keys, nil
}
