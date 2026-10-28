package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-jose/go-jose/v4"
)

// testKey is an RSA signing key with the key ID it is published under.
type testKey struct {
	kid  string
	priv *rsa.PrivateKey
}

func newTestKey(t *testing.T, kid string) testKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return testKey{kid: kid, priv: priv}
}

// sign returns an RS256 compact JWS over claims, with the kid header set.
func (k testKey) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	return k.signWith(t, claims, jose.JSONWebKey{Key: k.priv, KeyID: k.kid})
}

// signNoKid is sign without the kid header.
func (k testKey) signNoKid(t *testing.T, claims map[string]any) string {
	t.Helper()
	return k.signWith(t, claims, jose.JSONWebKey{Key: k.priv})
}

func (k testKey) signWith(t *testing.T, claims map[string]any, key jose.JSONWebKey) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	compact, err := jws.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return compact
}

// algNoneJWT builds an unsigned token whose header says alg none.
func algNoneJWT(claims map[string]any) string {
	enc := base64.RawURLEncoding
	payload, _ := json.Marshal(claims)
	return enc.EncodeToString([]byte(`{"alg":"none","kid":"k1"}`)) + "." + enc.EncodeToString(payload) + "."
}

// jwksServer serves a JWKS document and counts how often it is fetched.
type jwksServer struct {
	*httptest.Server
	fetches atomic.Int32

	mu     sync.Mutex
	keys   []testKey
	status int
}

func newJWKSServer(t *testing.T, keys ...testKey) *jwksServer {
	t.Helper()
	s := &jwksServer{keys: keys, status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.fetches.Add(1)
		s.mu.Lock()
		status, keys := s.status, s.keys
		s.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		set := jose.JSONWebKeySet{}
		for _, k := range keys {
			set.Keys = append(set.Keys, jose.JSONWebKey{Key: &k.priv.PublicKey, KeyID: k.kid, Algorithm: "RS256", Use: "sig"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(s.Close)
	return s
}

// setKeys changes the keys the next fetch returns.
func (s *jwksServer) setKeys(keys ...testKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = keys
	s.status = http.StatusOK
}

// fail makes the next fetches answer with status.
func (s *jwksServer) fail(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}
