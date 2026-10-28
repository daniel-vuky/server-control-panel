package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
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

type errVerifier struct{ err error }

func (e errVerifier) Verify(*http.Request) (Identity, error) { return Identity{}, e.err }

func TestRequireLogsRefusal(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const rawJWT = "eyJhidden.payload.sig"
	v := errVerifier{fmt.Errorf("%w: oidc: token is expired", ErrNoIdentity)}
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/api/x?q=hidden", nil)
	req.Header.Set("Cf-Access-Jwt-Assertion", rawJWT)
	rec := httptest.NewRecorder()
	Require(v)(next).ServeHTTP(rec, req)

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

	logged := buf.String()
	var rec1 map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(logged)), &rec1); err != nil {
		t.Fatalf("log is not one JSON line: %v\n%s", err, logged)
	}
	if rec1["level"] != "WARN" || rec1["msg"] != "request refused" || rec1["path"] != "/api/x" {
		t.Errorf("log = %v, want WARN \"request refused\" for /api/x", rec1)
	}
	if reason, _ := rec1["reason"].(string); !strings.Contains(reason, "token is expired") {
		t.Errorf("reason = %q, want it to contain %q", rec1["reason"], "token is expired")
	}
	for _, leak := range []string{"eyJhidden", "q=hidden"} {
		if strings.Contains(logged, leak) {
			t.Errorf("log leaks %q: %s", leak, logged)
		}
	}
}

func TestRequireTruncatesLongReason(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	v := errVerifier{fmt.Errorf("%w: %s", ErrNoIdentity, strings.Repeat("x", 1000))}
	rec := httptest.NewRecorder()
	Require(v)(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil))

	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &line); err != nil {
		t.Fatalf("log is not one JSON line: %v\n%s", err, buf.String())
	}
	reason, _ := line["reason"].(string)
	if len(reason) > 260 || !strings.HasSuffix(reason, "…") {
		t.Errorf("reason has %d bytes and suffix %q, want at most 260 ending in an ellipsis", len(reason), reason[max(0, len(reason)-4):])
	}
}
