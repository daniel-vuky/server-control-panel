package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

var _ oidc.KeySet = (*keySet)(nil)

var testClaims = map[string]any{"sub": "someone"}

// fakeClock is a clock the test advances by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestKeySetVerifiesKnownKey(t *testing.T) {
	k1 := newTestKey(t, "k1")
	srv := newJWKSServer(t, k1)
	ks := newKeySet(srv.URL, srv.Client(), newFakeClock().now)

	payload, err := ks.VerifySignature(context.Background(), k1.sign(t, testClaims))
	if err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil || got["sub"] != "someone" {
		t.Fatalf("payload = %s, err = %v", payload, err)
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}
}

func TestKeySetKnownKidBadSignatureDoesNotFetch(t *testing.T) {
	k1, other := newTestKey(t, "k1"), newTestKey(t, "k1")
	srv := newJWKSServer(t, k1)
	clock := newFakeClock()
	ks := newKeySet(srv.URL, srv.Client(), clock.now)

	if _, err := ks.VerifySignature(context.Background(), k1.sign(t, testClaims)); err != nil {
		t.Fatalf("good token: %v", err)
	}
	clock.advance(time.Hour)
	if _, err := ks.VerifySignature(context.Background(), other.sign(t, testClaims)); err == nil {
		t.Fatal("token signed by another key was accepted")
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}
}

func TestKeySetNoKidDoesNotFetch(t *testing.T) {
	k1 := newTestKey(t, "k1")
	srv := newJWKSServer(t, k1)
	ks := newKeySet(srv.URL, srv.Client(), newFakeClock().now)

	if _, err := ks.VerifySignature(context.Background(), k1.signNoKid(t, testClaims)); err == nil {
		t.Fatal("token with no kid was accepted")
	}
	if n := srv.fetches.Load(); n != 0 {
		t.Fatalf("fetches = %d, want 0", n)
	}
}

func TestKeySetUnknownKidThrottled(t *testing.T) {
	k1, k2 := newTestKey(t, "k1"), newTestKey(t, "k2")
	srv := newJWKSServer(t, k1)
	clock := newFakeClock()
	ks := newKeySet(srv.URL, srv.Client(), clock.now)
	rawJWT := k2.sign(t, testClaims)

	for range 5 {
		if _, err := ks.VerifySignature(context.Background(), rawJWT); err == nil {
			t.Fatal("unknown kid accepted")
		}
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches after 5 attempts = %d, want 1", n)
	}

	clock.advance(29 * time.Second)
	if _, err := ks.VerifySignature(context.Background(), rawJWT); err == nil {
		t.Fatal("unknown kid accepted inside the throttle window")
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches inside window = %d, want 1", n)
	}

	// The key rotates in, and the window has passed.
	clock.advance(2 * time.Second)
	srv.setKeys(k1, k2)
	if _, err := ks.VerifySignature(context.Background(), rawJWT); err != nil {
		t.Fatalf("rotated key rejected: %v", err)
	}
	if n := srv.fetches.Load(); n != 2 {
		t.Fatalf("fetches after rotation = %d, want 2", n)
	}
}

func TestKeySetConcurrentUnknownKidFetchesOnce(t *testing.T) {
	k1 := newTestKey(t, "k1")
	srv := newJWKSServer(t, k1)
	ks := newKeySet(srv.URL, srv.Client(), newFakeClock().now)
	rawJWT := k1.sign(t, testClaims)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			_, err := ks.VerifySignature(context.Background(), rawJWT)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("VerifySignature: %v", err)
		}
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}
}

func TestKeySetFailedRefreshKeepsCache(t *testing.T) {
	k1, k9 := newTestKey(t, "k1"), newTestKey(t, "k9")
	srv := newJWKSServer(t, k1)
	clock := newFakeClock()
	ks := newKeySet(srv.URL, srv.Client(), clock.now)

	if _, err := ks.VerifySignature(context.Background(), k1.sign(t, testClaims)); err != nil {
		t.Fatalf("first token: %v", err)
	}
	srv.fail(http.StatusInternalServerError)
	clock.advance(31 * time.Second)
	if _, err := ks.VerifySignature(context.Background(), k9.sign(t, testClaims)); err == nil {
		t.Fatal("unknown kid accepted")
	}
	if n := srv.fetches.Load(); n != 2 {
		t.Fatalf("fetches = %d, want 2", n)
	}
	if _, err := ks.VerifySignature(context.Background(), k1.sign(t, testClaims)); err != nil {
		t.Fatalf("cached key lost after failed refresh: %v", err)
	}
}

func TestKeySetUnreachableBeforeCache(t *testing.T) {
	k1 := newTestKey(t, "k1")
	srv := newJWKSServer(t, k1)
	srv.fail(http.StatusInternalServerError)
	ks := newKeySet(srv.URL, srv.Client(), newFakeClock().now)
	rawJWT := k1.sign(t, testClaims)

	for i := range 2 {
		if _, err := ks.VerifySignature(context.Background(), rawJWT); err == nil {
			t.Fatalf("call %d: accepted with no keys", i+1)
		}
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}
}

func TestKeySetBadJSON(t *testing.T) {
	k1 := newTestKey(t, "k1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	ks := newKeySet(srv.URL, srv.Client(), newFakeClock().now)

	if _, err := ks.VerifySignature(context.Background(), k1.sign(t, testClaims)); err == nil {
		t.Fatal("token accepted with an unparseable key set")
	}
}

func TestKeySetRejectsAlgNone(t *testing.T) {
	k1 := newTestKey(t, "k1")
	srv := newJWKSServer(t, k1)
	ks := newKeySet(srv.URL, srv.Client(), newFakeClock().now)

	if _, err := ks.VerifySignature(context.Background(), algNoneJWT(testClaims)); err == nil {
		t.Fatal("alg none token accepted")
	}
}

func TestKeySetEmptyAnswerKeepsCachedKeys(t *testing.T) {
	k1 := newTestKey(t, "k1")
	k9 := newTestKey(t, "k9")
	srv := newJWKSServer(t, k1)
	clock := newFakeClock()
	ks := newKeySet(srv.URL, srv.Client(), clock.now)

	if _, err := ks.VerifySignature(context.Background(), k1.sign(t, testClaims)); err != nil {
		t.Fatalf("good token: %v", err)
	}
	srv.setKeys()
	clock.advance(time.Minute)
	if _, err := ks.VerifySignature(context.Background(), k9.sign(t, testClaims)); err == nil {
		t.Fatal("unknown kid was accepted")
	}
	if n := srv.fetches.Load(); n != 2 {
		t.Fatalf("fetches = %d, want 2", n)
	}
	if _, err := ks.VerifySignature(context.Background(), k1.sign(t, testClaims)); err != nil {
		t.Fatalf("cached key lost after an empty key set answer: %v", err)
	}
}
