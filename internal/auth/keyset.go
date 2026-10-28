package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// keyRefreshInterval is the least time between two attempts to fetch the key
// set, counted from the last attempt whether it succeeded or not.
const keyRefreshInterval = 30 * time.Second

// maxKeySetBytes bounds how much of a key set response is read.
const maxKeySetBytes = 1 << 20

// keySet verifies RS256 signatures against the team's Cloudflare Access keys.
// It implements oidc.KeySet. Keys are fetched lazily, and only when a token
// names a kid that is not cached, at most once per keyRefreshInterval.
type keySet struct {
	certsURL string
	client   *http.Client
	now      func() time.Time

	// refreshMu serialises fetches so concurrent requests share one. It is
	// always taken before mu, never while holding it.
	refreshMu sync.Mutex

	mu          sync.Mutex // guards keys and lastAttempt
	keys        map[string]jose.JSONWebKey
	lastAttempt time.Time
}

func newKeySet(certsURL string, client *http.Client, now func() time.Time) *keySet {
	return &keySet{certsURL: certsURL, client: client, now: now}
}

// VerifySignature checks the signature of rawJWT and returns its payload. The
// context is not used for the key fetch: one caller's cancellation must not
// fail a refresh that other callers are waiting on.
func (k *keySet) VerifySignature(_ context.Context, rawJWT string) ([]byte, error) {
	jws, err := jose.ParseSigned(rawJWT, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	kid := jws.Signatures[0].Header.KeyID
	if kid == "" {
		return nil, errors.New("token has no kid")
	}

	key, ok := k.cached(kid)
	if !ok {
		if err := k.refresh(kid); err != nil {
			return nil, err
		}
		if key, ok = k.cached(kid); !ok {
			return nil, fmt.Errorf("no key for kid %q", kid)
		}
	}
	payload, err := jws.Verify(key.Key)
	if err != nil {
		return nil, fmt.Errorf("verify signature for kid %q: %w", kid, err)
	}
	return payload, nil
}

func (k *keySet) cached(kid string) (jose.JSONWebKey, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, ok := k.keys[kid]
	return key, ok
}

// refresh fetches the key set unless kid turned up meanwhile or the last
// attempt was less than keyRefreshInterval ago. It returns nil when the caller
// should look kid up again, and an error when there is nothing more to try.
func (k *keySet) refresh(kid string) error {
	k.refreshMu.Lock()
	defer k.refreshMu.Unlock()

	k.mu.Lock()
	if _, ok := k.keys[kid]; ok {
		k.mu.Unlock()
		return nil
	}
	if !k.lastAttempt.IsZero() && k.now().Sub(k.lastAttempt) < keyRefreshInterval {
		k.mu.Unlock()
		return fmt.Errorf("no key for kid %q, key refresh throttled", kid)
	}
	k.lastAttempt = k.now()
	k.mu.Unlock()

	keys, err := k.fetch()
	if err != nil {
		return fmt.Errorf("refresh keys for kid %q: %w", kid, err)
	}
	k.mu.Lock()
	k.keys = keys
	k.mu.Unlock()
	return nil
}

func (k *keySet) fetch() (map[string]jose.JSONWebKey, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, k.certsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch key set: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch key set: status %d", resp.StatusCode)
	}
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxKeySetBytes)).Decode(&set); err != nil {
		return nil, fmt.Errorf("parse key set: %w", err)
	}
	if len(set.Keys) == 0 {
		return nil, errors.New("key set has no keys")
	}
	keys := make(map[string]jose.JSONWebKey, len(set.Keys))
	for _, key := range set.Keys {
		keys[key.KeyID] = key
	}
	return keys, nil
}
