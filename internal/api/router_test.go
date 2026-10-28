package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/auth"
)

type allowVerifier struct{}

func (allowVerifier) Verify(*http.Request) (auth.Identity, error) {
	return auth.Identity{Email: "test@example.com"}, nil
}

func webStub() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("web"))
	})
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthIsPublic(t *testing.T) {
	h := NewRouter(Deps{Version: "1.2.3", Verifier: auth.RejectAll{}, Web: webStub()})

	rec := do(h, http.MethodGet, "/api/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	if got != (HealthResponse{Status: "ok", Version: "1.2.3"}) {
		t.Errorf("body = %+v, want {ok 1.2.3}", got)
	}
}

func TestEverythingElseNeedsIdentity(t *testing.T) {
	h := NewRouter(Deps{Version: "1.2.3", Verifier: auth.RejectAll{}, Web: webStub()})

	for _, path := range []string{"/", "/backups", "/api/anything"} {
		t.Run(path, func(t *testing.T) {
			rec := do(h, http.MethodGet, path)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "web") {
				t.Errorf("body leaked web content: %q", rec.Body.String())
			}
		})
	}
}

func TestHealthRejectsNonGet(t *testing.T) {
	h := NewRouter(Deps{Version: "1.2.3", Verifier: auth.RejectAll{}, Web: webStub()})

	rec := do(h, http.MethodPost, "/api/health")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /api/health status = %d, want 401", rec.Code)
	}
}

func TestHealthAllowsHead(t *testing.T) {
	h := NewRouter(Deps{Version: "1.2.3", Verifier: auth.RejectAll{}, Web: webStub()})

	rec := do(h, http.MethodHead, "/api/health")
	if rec.Code != http.StatusOK {
		t.Errorf("HEAD /api/health status = %d, want 200", rec.Code)
	}
}

func TestHealthTrailingSlashNeedsIdentity(t *testing.T) {
	h := NewRouter(Deps{Version: "1.2.3", Verifier: auth.RejectAll{}, Web: webStub()})

	rec := do(h, http.MethodGet, "/api/health/")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/health/ status = %d, want 401", rec.Code)
	}
}

func TestNewRouterPanicsOnMissingDeps(t *testing.T) {
	tests := []struct {
		name string
		deps Deps
	}{
		{"nil Web", Deps{Version: "1", Verifier: auth.RejectAll{}}},
		{"nil Verifier", Deps{Version: "1", Web: webStub()}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("NewRouter did not panic")
				}
			}()
			NewRouter(tc.deps)
		})
	}
}

func TestUnknownAPIRouteIs404JSON(t *testing.T) {
	h := NewRouter(Deps{Version: "1.2.3", Verifier: allowVerifier{}, Web: webStub()})

	rec := do(h, http.MethodGet, "/api/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"not found"}` {
		t.Errorf("body = %q, want %q", got, `{"error":"not found"}`)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	rec = do(h, http.MethodGet, "/backups")
	if rec.Code != http.StatusOK || rec.Body.String() != "web" {
		t.Errorf("GET /backups = %d %q, want 200 \"web\"", rec.Code, rec.Body.String())
	}
}
