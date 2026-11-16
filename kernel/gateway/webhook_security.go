package gateway

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// WebhookKeys is an immutable application key ring. Keys are copied at construction.
type WebhookKeys struct {
	active string
	keys   map[string]cipher.AEAD
}

// NewWebhookKeys requires an explicit active AES-256 key; no ephemeral fallback exists.
func NewWebhookKeys(active string, keys map[string][]byte) (*WebhookKeys, error) {
	ring := &WebhookKeys{active: active, keys: map[string]cipher.AEAD{}}
	for id, key := range keys {
		if len(id) == 0 || len(id) > 128 || len(key) != 32 {
			return nil, errors.New("webhook keys require identifiers and 32-byte keys")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, errors.New("invalid webhook encryption key")
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		ring.keys[id] = aead
	}
	if ring.keys[active] == nil {
		return nil, errors.New("active webhook encryption key is missing")
	}
	return ring, nil
}

func webhookAAD(space, endpoint, keyID string) []byte {
	raw, _ := json.Marshal([]string{"webhook-secret.v1", space, endpoint, keyID})
	return raw
}

// NewSecret generates a one-time presentation and its space-bound encrypted form.
func (k *WebhookKeys) NewSecret(space, endpoint string) (model.SealedWebhookSecret, string, error) {
	if k == nil {
		return model.SealedWebhookSecret{}, "", errors.New("webhook keys are not configured")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return model.SealedWebhookSecret{}, "", err
	}
	aead := k.keys[k.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return model.SealedWebhookSecret{}, "", err
	}
	sealed := model.SealedWebhookSecret{KeyID: k.active, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, raw, webhookAAD(space, endpoint, k.active))}
	return sealed, "whsec_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Open authenticates both the ciphertext and its endpoint ownership before use.
func (k *WebhookKeys) Open(space, endpoint string, sealed model.SealedWebhookSecret) ([]byte, error) {
	if k == nil {
		return nil, errors.New("webhook keys are not configured")
	}
	aead := k.keys[sealed.KeyID]
	if aead == nil || len(sealed.Nonce) != aead.NonceSize() {
		return nil, errors.New("webhook secret cannot be decrypted")
	}
	raw, err := aead.Open(nil, sealed.Nonce, sealed.Ciphertext, webhookAAD(space, endpoint, sealed.KeyID))
	if err != nil || len(raw) != 32 {
		return nil, errors.New("webhook secret cannot be decrypted")
	}
	return raw, nil
}

// WebhookSignature signs timestamp and exact stored bytes using the raw signing key.
func WebhookSignature(at time.Time, body, secret []byte) string {
	stamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(stamp + "."))
	_, _ = mac.Write(body)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

type webhookNetwork struct {
	local  bool
	lookup func(context.Context, string) ([]net.IPAddr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func newWebhookNetwork(local bool) *webhookNetwork {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &webhookNetwork{local: local, lookup: net.DefaultResolver.LookupIPAddr, dial: dialer.DialContext}
}

// Special-use ranges supplement Go's global-unicast/private classification.
var webhookReserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("fec0::/10"),
}

func publicWebhookIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range webhookReserved {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func (n *webhookNetwork) addresses(ctx context.Context, host string) ([]net.IPAddr, error) {
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		var err error
		ips, err = n.lookup(ctx, host)
		if err != nil {
			return nil, errors.New("webhook destination cannot be resolved")
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("webhook destination has no addresses")
	}
	for _, ip := range ips {
		// Local mode permits only a literal loopback receiver, not arbitrary private DNS.
		if n.local && net.ParseIP(host) != nil && ip.IP.IsLoopback() {
			continue
		}
		if ip.Zone != "" || !publicWebhookIP(ip.IP) {
			return nil, errors.New("webhook destination is not public")
		}
	}
	return ips, nil
}

func (n *webhookNetwork) Validate(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.Contains(u.Hostname(), "%") {
		return model.NewError(model.CodeInvalidInput, "invalid webhook destination", nil)
	}
	if u.Scheme != "https" && !(n.local && u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback()) {
		return model.NewError(model.CodeInvalidInput, "webhook destination requires HTTPS", nil)
	}
	if port := u.Port(); port != "" {
		p, e := strconv.Atoi(port)
		if e != nil || p < 1 || p > 65535 {
			return model.NewError(model.CodeInvalidInput, "invalid webhook destination port", nil)
		}
	}
	if _, err := n.addresses(ctx, u.Hostname()); err != nil {
		return model.NewError(model.CodeInvalidInput, "webhook destination is not public or resolvable", nil)
	}
	return nil
}

func (n *webhookNetwork) client() *http.Client {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 5 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := n.addresses(ctx, host)
			if err != nil {
				return nil, err
			}
			// Pin the validated address while retaining the request hostname for TLS.
			return n.dial(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("webhook redirects are disabled") }}
}
