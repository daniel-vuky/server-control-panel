package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubVerifier struct{ id Identity }

func (s stubVerifier) Verify(*http.Request) (Identity, error) { return s.id, nil }

func TestRequireRejectsWithoutIdentity(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	rec := httptest.NewRecorder()
	Require(RejectAll{})(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"unauthorized"}` {
		t.Errorf("body = %q, want %q", got, `{"error":"unauthorized"}`)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if called {
		t.Error("next handler was called without an identity")
	}
}

func TestRequirePutsIdentityInContext(t *testing.T) {
	var got Identity
	var ok bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = FromContext(r.Context())
	})

	rec := httptest.NewRecorder()
	Require(stubVerifier{Identity{Email: "a@b.c"}})(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !ok || got.Email != "a@b.c" {
		t.Errorf("FromContext = (%+v, %v), want ({a@b.c}, true)", got, ok)
	}
}

func TestFromContextEmpty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := FromContext(r.Context()); ok {
		t.Error("FromContext on a bare context returned ok = true")
	}
}
