package api

import (
	"bytes"
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
	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/logs"
	"github.com/daniel-vuky/server-control-panel/internal/project"
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
// when set. The compose.logs.tail* backlog commands answer backlog, and each
// Stream (the compose.logs follow) delivers the lines sent on follow until
// follow is closed (it then exits 0) or its context ends. It logs every action
// it is asked to run.
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

	backlog         string
	follow          chan string        // the follow's lines; closed, the follow exits 0
	streamStarted   chan runner.Values // one value per Stream call
	streamCancelled chan struct{}      // one value per Stream that ended on its ctx

	dbSize string        // what the db.*.size actions print ("1024" by default)
	dump   []byte        // what the db.*.dump actions write (dumpData by default)
	dgate  chan struct{} // when set, the dumps wait for it to close
	donce  sync.Once
}

// dumpData is what a dump writes by default.
var dumpData = bytes.Repeat([]byte("INSERT INTO t VALUES (1);\n"), 64)

func isDBAction(a runner.Action, kind string) bool {
	return strings.HasPrefix(string(a), "db.") && strings.Contains(string(a), "."+kind)
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		configs:         map[string]string{},
		follow:          make(chan string),
		streamStarted:   make(chan runner.Values, 16),
		streamCancelled: make(chan struct{}, 16),
	}
}

func isLogsTail(a runner.Action) bool {
	switch a {
	case runner.ComposeLogsTail, runner.ComposeLogsTailOverride, runner.ComposeLogsTailService, runner.ComposeLogsTailServiceOverride:
		return true
	}
	return false
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
	case isLogsTail(a):
		f.mu.Lock()
		defer f.mu.Unlock()
		return runner.Result{Stdout: []byte(f.backlog)}, nil
	case a == runner.DockerPing:
		f.mu.Lock()
		defer f.mu.Unlock()
		return runner.Result{Stdout: []byte("29.0.0\n")}, f.pingErr
	case isDBAction(a, "size"):
		f.mu.Lock()
		defer f.mu.Unlock()
		size := f.dbSize
		if size == "" {
			size = "1024"
		}
		return runner.Result{Stdout: []byte(size + "\n")}, nil
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

// RunTo answers the db.*.dump actions: it writes dump (dumpData by default)
// to stdout, after waiting for dgate when it is set.
func (f *fakeDocker) RunTo(ctx context.Context, s runner.Stack, a runner.Action, _ runner.Values, _ []string, stdout io.Writer) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, a)
	gate, data := f.dgate, f.dump
	f.mu.Unlock()
	if !isDBAction(a, "dump") {
		return nil, fmt.Errorf("fake: unexpected %s %s", s, a)
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if data == nil {
		data = dumpData
	}
	_, err := stdout.Write(data)
	return nil, err
}

// holdDumps makes the dumps wait until releaseDumps is called, which also
// runs at cleanup.
func (f *fakeDocker) holdDumps(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	f.dgate = make(chan struct{})
	f.donce = sync.Once{}
	f.mu.Unlock()
	t.Cleanup(f.releaseDumps)
}

func (f *fakeDocker) releaseDumps() {
	f.mu.Lock()
	gate := f.dgate
	f.mu.Unlock()
	if gate != nil {
		f.donce.Do(func() { close(gate) })
	}
}

// Stream is the compose.logs follow: see fakeDocker.
func (f *fakeDocker) Stream(ctx context.Context, _ runner.Stack, a runner.Action, v runner.Values, _ []string, onLine func(string)) error {
	f.mu.Lock()
	f.calls = append(f.calls, a)
	follow := f.follow
	f.mu.Unlock()
	f.streamStarted <- v
	for {
		select {
		case line, ok := <-follow:
			if !ok {
				return nil
			}
			onLine(line)
		case <-ctx.Done():
			f.streamCancelled <- struct{}{}
			return ctx.Err()
		}
	}
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

// testPublicURL is the panel's public origin in the test config.
const testPublicURL = "http://localhost:5173"

// testDriveConfig is a config whose DriveReason() is "" in both tag sets.
func testDriveConfig() config.Config {
	return config.Config{
		SecretKey: bytes.Repeat([]byte{7}, 32), PublicURL: testPublicURL,
		GoogleClientID: "client-id", GoogleClientSecret: "client-secret",
	}
}

// newTestDeps returns router dependencies for tests: an identity that is
// always allowed, a temp store, a guard over one temp root (resolved, so it
// compares equal to stored paths), a fakeDocker, a scan manager, the
// lifecycle controller and live hub on the same fake, a Drive service over
// the fake Drive (not connected) and a started backup manager, wired as in
// cmd/panel.
func newTestDeps(t *testing.T) Deps {
	t.Helper()
	return newTestDepsWith(t, nil)
}

// newTestDepsWith is newTestDeps with the fake Drive client wrapped by wrap
// (when not nil).
func newTestDepsWith(t *testing.T, wrap func(drive.Client) drive.Client) Deps {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	docker := newFakeDocker()
	ctl, hub := control.New(st, docker, log)
	// Runs before the store closes: a command a test left running stores its
	// outcome first.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = ctl.Wait(ctx)
	})
	guard := roots.New([]string{root}, log)
	// Wired as in cmd/panel.
	scans := scan.NewManager(st, docker, log)
	scans.SetGuard(guard)
	scans.SetOnChange(hub.Refresh)

	client := drive.NewFake(filepath.Join(dataDir, "fake-drive"), testPublicURL)
	if wrap != nil {
		client = wrap(client)
	}
	drv, err := drive.NewService(testDriveConfig(), st, client, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	containers := func(ctx context.Context) ([]project.Container, bool) {
		return hub.RecentContainers(ctx, 10*time.Second)
	}
	backups := backup.New(st, docker, drv, containers, dataDir, time.Now, log)
	hub.SetBackupSource(backups.Active)
	hub.SetBatchSource(backups.CurrentBatch)
	hub.SetDriveSource(drv.Status)
	backups.OnChange(hub.Publish)
	drv.OnChange(hub.Publish)
	backups.Start(context.Background())
	// Runs before the store closes: the worker stores its last row first.
	t.Cleanup(func() {
		backups.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := backups.Wait(ctx); err != nil {
			t.Errorf("backups.Wait: %v", err)
		}
	})
	return Deps{
		Version:  "1.2.3",
		Verifier: allowVerifier{},
		Web:      webStub(),
		Guard:    guard,
		Store:    st,
		Scans:    scans,
		Docker:   docker,
		Control:  ctl,
		Hub:      hub,
		Logs:     logs.New(docker, hub.Containers, time.Now),
		Drive:    drv,
		Backups:  backups,
		Metrics:  &fakeMonitor{},
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
		{"nil Logs", func(d *Deps) { d.Logs = nil }},
		{"nil Drive", func(d *Deps) { d.Drive = nil }},
		{"nil Backups", func(d *Deps) { d.Backups = nil }},
		{"nil Metrics", func(d *Deps) { d.Metrics = nil }},
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
		{http.MethodPost, "/api/rescans"},
		{http.MethodPost, "/api/workspaces/1/remove"},
		{http.MethodPost, "/api/projects/1/untrack"},
		{http.MethodPost, "/api/projects/1/settings"},
		{http.MethodGet, "/api/projects/1/logs"},
		{http.MethodGet, "/api/drive"},
		{http.MethodPost, "/api/drive/connect"},
		{http.MethodGet, "/api/drive/callback"},
		{http.MethodGet, "/api/drive/folders"},
		{http.MethodPost, "/api/drive/folder"},
		{http.MethodPost, "/api/drive/test"},
		{http.MethodPost, "/api/drive/disconnect"},
		{http.MethodGet, "/api/drive/fake-consent"},
		{http.MethodPost, "/api/drive/fake-consent"},
		{http.MethodPost, "/api/projects/1/backups"},
		{http.MethodGet, "/api/projects/1/backups"},
		{http.MethodGet, "/api/backups/1"},
		{http.MethodPost, "/api/backups/1/retry"},
		{http.MethodGet, "/api/backups/1/download"},
		{http.MethodGet, "/api/backup-settings"},
		{http.MethodPost, "/api/backup-settings/defaults"},
		{http.MethodPost, "/api/projects/1/backup-schedule"},
		{http.MethodPost, "/api/backups/all"},
		{http.MethodGet, "/api/backups"},
		{http.MethodGet, "/api/backups/summary"},
		{http.MethodGet, "/api/backups/latest"},
		{http.MethodGet, "/api/server"},
		{http.MethodGet, "/api/server/metrics?range=1h"},
		{http.MethodGet, "/api/server/usage"},
		{http.MethodGet, "/api/projects/1/metrics?range=1h"},
		{http.MethodGet, "/api/metrics-settings"},
		{http.MethodPost, "/api/metrics-settings"},
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
