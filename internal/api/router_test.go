package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/auth"
	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/roots"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
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

// fakeDocker answers compose.config with canned JSON per {dir} (a project
// with one nginx service by default), docker.containers with canned lines,
// docker.ping with pingErr, and the lifecycle commands with lifecycleErr. A
// lifecycle command that succeeds replaces the container lines with after,
// when set. It logs every action it is asked to run.
type fakeDocker struct {
	mu            sync.Mutex
	configs       map[string]string
	containers    string
	containersErr error
	pingErr       error
	lifecycleErr  error
	after         *string // containers after a successful lifecycle command
	calls         []runner.Action
	lifecycleRuns []runner.Values

	gate  chan struct{} // when set, docker.containers waits for it to close
	once  sync.Once
	lgate chan struct{} // when set, the lifecycle commands wait for it to close
	lonce sync.Once
}

func isLifecycle(a runner.Action) bool {
	switch a {
	case runner.ComposeUp, runner.ComposeUpOverride, runner.ComposeStop, runner.ComposeStopOverride,
		runner.ComposeRestart, runner.ComposeRestartOverride:
		return true
	}
	return false
}

func (f *fakeDocker) Run(_ context.Context, s runner.Stack, a runner.Action, v runner.Values, _ []string) (runner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, a)
	gate, lgate, containers, containersErr := f.gate, f.lgate, f.containers, f.containersErr
	cfg, ok := f.configs[v["dir"]]
	f.mu.Unlock()
	switch {
	case a == runner.DockerContainers:
		if gate != nil {
			<-gate
		}
		if containersErr != nil {
			return runner.Result{}, containersErr
		}
		return runner.Result{Stdout: []byte(containers)}, nil
	case a == runner.ComposeConfig || a == runner.ComposeConfigOverride:
		if !ok {
			cfg = configJSON(scan.NormalizeProjectName(filepath.Base(v["dir"])), map[string]service{"app": {Image: "nginx"}})
		}
		return runner.Result{Stdout: []byte(cfg)}, nil
	case a == runner.DockerPing:
		f.mu.Lock()
		defer f.mu.Unlock()
		return runner.Result{Stdout: []byte("29.0.0\n")}, f.pingErr
	case isLifecycle(a):
		if lgate != nil {
			<-lgate
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.lifecycleRuns = append(f.lifecycleRuns, v)
		if f.lifecycleErr != nil {
			return runner.Result{ExitCode: 1}, f.lifecycleErr
		}
		if f.after != nil {
			f.containers = *f.after
		}
		return runner.Result{}, nil
	}
	return runner.Result{}, fmt.Errorf("fake: unexpected %s %s", s, a)
}

// hold makes docker.containers wait until release is called; release also
// runs at cleanup, so a test that fails early does not leave a scan blocked.
func (f *fakeDocker) hold(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	f.gate = make(chan struct{})
	f.once = sync.Once{}
	f.mu.Unlock()
	t.Cleanup(f.release)
}

func (f *fakeDocker) release() {
	f.mu.Lock()
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		f.once.Do(func() { close(gate) })
	}
}

// holdLifecycle makes the lifecycle commands wait until releaseLifecycle is
// called. It does not hold docker.containers, which the pre-check and the
// re-read need answered. releaseLifecycle also runs at cleanup.
func (f *fakeDocker) holdLifecycle(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	f.lgate = make(chan struct{})
	f.lonce = sync.Once{}
	f.mu.Unlock()
	t.Cleanup(f.releaseLifecycle)
}

func (f *fakeDocker) releaseLifecycle() {
	f.mu.Lock()
	gate := f.lgate
	f.mu.Unlock()
	if gate != nil {
		f.lonce.Do(func() { close(gate) })
	}
}

// set runs fn with the fake locked, to change its answers.
func (f *fakeDocker) set(fn func(f *fakeDocker)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// callCount returns how many commands the fake was asked to run.
func (f *fakeDocker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeDocker) setConfig(dir, cfg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configs[dir] = cfg
}

type service struct {
	Image       string            `json:"image"`
	Environment map[string]string `json:"environment,omitempty"`
}

// configJSON is a docker compose config with the given name and services.
func configJSON(name string, services map[string]service) string {
	b, err := json.Marshal(map[string]any{"name": name, "services": services})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// newTestDeps returns router dependencies for tests: an identity that is
// always allowed, a temp store, a guard over one temp root (resolved, so it
// compares equal to stored paths), a fakeDocker, a scan manager, and the
// lifecycle controller and live hub on the same fake.
func newTestDeps(t *testing.T) Deps {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	docker := &fakeDocker{configs: map[string]string{}}
	ctl, hub := control.New(st, docker, log)
	// Runs before the store closes: a command a test left running stores its
	// outcome first.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = ctl.Wait(ctx)
	})
	return Deps{
		Version:  "1.2.3",
		Verifier: allowVerifier{},
		Web:      webStub(),
		Guard:    roots.New([]string{root}, log),
		Store:    st,
		Scans:    scan.NewManager(st, docker, log),
		Docker:   docker,
		Control:  ctl,
		Hub:      hub,
	}
}

// rootOf returns the temp workspace root of deps from newTestDeps.
func rootOf(d Deps) string { return d.Guard.Display()[0] }

// dockerOf returns the fakeDocker of deps from newTestDeps.
func dockerOf(d Deps) *fakeDocker { return d.Docker.(*fakeDocker) }

// rejecting returns deps whose verifier refuses every request.
func rejecting(t *testing.T) Deps {
	d := newTestDeps(t)
	d.Verifier = auth.RejectAll{}
	return d
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthIsPublic(t *testing.T) {
	h := NewRouter(rejecting(t))

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
	h := NewRouter(rejecting(t))

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
	h := NewRouter(rejecting(t))

	rec := do(h, http.MethodPost, "/api/health")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /api/health status = %d, want 401", rec.Code)
	}
}

func TestHealthAllowsHead(t *testing.T) {
	h := NewRouter(rejecting(t))

	rec := do(h, http.MethodHead, "/api/health")
	if rec.Code != http.StatusOK {
		t.Errorf("HEAD /api/health status = %d, want 200", rec.Code)
	}
}

func TestHealthTrailingSlashNeedsIdentity(t *testing.T) {
	h := NewRouter(rejecting(t))

	rec := do(h, http.MethodGet, "/api/health/")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/health/ status = %d, want 401", rec.Code)
	}
}

func TestNewRouterPanicsOnMissingDeps(t *testing.T) {
	tests := []struct {
		name  string
		unset func(*Deps)
	}{
		{"nil Web", func(d *Deps) { d.Web = nil }},
		{"nil Verifier", func(d *Deps) { d.Verifier = nil }},
		{"nil Guard", func(d *Deps) { d.Guard = nil }},
		{"nil Store", func(d *Deps) { d.Store = nil }},
		{"nil Scans", func(d *Deps) { d.Scans = nil }},
		{"nil Docker", func(d *Deps) { d.Docker = nil }},
		{"nil Control", func(d *Deps) { d.Control = nil }},
		{"nil Hub", func(d *Deps) { d.Hub = nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDeps(t)
			tc.unset(&d)
			defer func() {
				if recover() == nil {
					t.Error("NewRouter did not panic")
				}
			}()
			NewRouter(d)
		})
	}
}

func TestNewEndpointsNeedIdentity(t *testing.T) {
	h := NewRouter(rejecting(t))
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/scans"},
		{http.MethodGet, "/api/scans/1"},
		{http.MethodGet, "/api/scans/1/events"},
		{http.MethodPost, "/api/scans/1/track"},
		{http.MethodGet, "/api/workspaces"},
		{http.MethodGet, "/api/projects"},
		{http.MethodPost, "/api/projects/1/start"},
		{http.MethodPost, "/api/projects/1/stop"},
		{http.MethodPost, "/api/projects/1/restart"},
		{http.MethodGet, "/api/events"},
		{http.MethodPost, "/api/docker/check"},
	} {
		if rec := do(h, tc.method, tc.path); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestUnknownAPIRouteIs404JSON(t *testing.T) {
	h := NewRouter(newTestDeps(t))

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

func TestMeReturnsVerifiedEmail(t *testing.T) {
	h := NewRouter(newTestDeps(t))

	rec := do(h, http.MethodGet, "/api/me")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	if got != (MeResponse{Email: "test@example.com"}) {
		t.Errorf("body = %+v, want {test@example.com}", got)
	}
}

func TestMeNeedsIdentity(t *testing.T) {
	h := NewRouter(rejecting(t))

	rec := do(h, http.MethodGet, "/api/me")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
