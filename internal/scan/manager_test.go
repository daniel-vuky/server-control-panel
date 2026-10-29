package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

type fakeCall struct {
	Stack   runner.Stack
	Action  runner.Action
	Values  runner.Values
	Secrets []string
}

type fakeReply struct {
	stdout    string
	err       error
	truncated bool
}

// fakeCmd answers compose.config with canned JSON per {dir} and
// docker.containers with canned lines, and records every call.
type fakeCmd struct {
	mu            sync.Mutex
	configs       map[string]fakeReply
	containers    string
	containersErr error
	gate          chan struct{} // when set, docker.containers waits for it to close
	calls         []fakeCall
}

func newFakeCmd() *fakeCmd { return &fakeCmd{configs: map[string]fakeReply{}} }

func (f *fakeCmd) Run(_ context.Context, s runner.Stack, a runner.Action, v runner.Values, secrets []string) (runner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{s, a, v, slices.Clone(secrets)})
	gate := f.gate
	f.mu.Unlock()
	switch a {
	case runner.DockerContainers:
		if gate != nil {
			<-gate
		}
		if f.containersErr != nil {
			return runner.Result{}, f.containersErr
		}
		return runner.Result{Stdout: []byte(f.containers)}, nil
	case runner.ComposeConfig, runner.ComposeConfigOverride:
		r, ok := f.configs[v["dir"]]
		if !ok {
			// A folder without a canned reply is a project with one service.
			r = fakeReply{stdout: configJSON(NormalizeProjectName(filepath.Base(v["dir"])), map[string]string{"app": "nginx"})}
		}
		if r.err != nil {
			return runner.Result{ExitCode: 1}, r.err
		}
		return runner.Result{Stdout: []byte(r.stdout), Truncated: r.truncated}, nil
	}
	return runner.Result{}, fmt.Errorf("fake: unexpected %s %s", s, a)
}

func (f *fakeCmd) callsOf(a runner.Action) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.Action == a {
			out = append(out, c)
		}
	}
	return out
}

// configJSON is a compose config with the given name and services (name → image).
func configJSON(name string, services map[string]string) string {
	svcs := map[string]any{}
	for n, img := range services {
		svcs[n] = map[string]any{"image": img}
	}
	b, err := json.Marshal(map[string]any{"name": name, "services": svcs})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// containerLine is one docker.containers line in runner.ContainerColumns order.
func containerLine(proj, service, state, status, image string) string {
	return strings.Join([]string{proj, service, state, status, image, "False"}, "\t") + "\n"
}

func newTestManager(t *testing.T, cmd Commander) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := NewManager(st, cmd, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(m.wait)
	return m, st
}

// runScan starts a scan, waits for it and returns the stored job and result.
func runScan(t *testing.T, m *Manager, root string, depth int) (store.Job, Result) {
	t.Helper()
	id, err := m.Start(context.Background(), root, depth, "dev@localhost")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.wait()
	job, err := m.st.Job(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var res Result
	if job.State == "done" {
		if err := json.Unmarshal(job.Result, &res); err != nil {
			t.Fatalf("result JSON: %v", err)
		}
	}
	return job, res
}

func rowByPath(t *testing.T, res Result, path string) Row {
	t.Helper()
	for _, r := range res.Rows {
		if r.Path == path {
			return r
		}
	}
	t.Fatalf("no row for %s in %+v", path, res.Rows)
	return Row{}
}

func TestScanRowsAndStatus(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "shop/compose.yaml", "")
	mkfile(t, root, "shop/compose.override.yaml", "")
	mkfile(t, root, "notes/readme.txt", "")
	odd := "my app; $(id)"
	mkfile(t, root, odd+"/compose.yaml", "")

	fc := newFakeCmd()
	shop := filepath.Join(root, "shop")
	fc.configs[shop] = fakeReply{stdout: configJSON("shop", map[string]string{"web": "nginx:1.27", "db": "postgres:16"})}
	fc.containers = containerLine("shop", "web", "running", "Up 2 hours", "") +
		containerLine("shop", "db", "exited", "Exited (0) 1 hour ago", "")
	m, _ := newTestManager(t, fc)

	job, res := runScan(t, m, root, 2)
	if job.State != "done" {
		t.Fatalf("state %s (%s)", job.State, job.Error)
	}
	if res.FoldersScanned != 3 || len(res.Rows) != 2 {
		t.Fatalf("foldersScanned %d, rows %d; want 3 and 2", res.FoldersScanned, len(res.Rows))
	}
	row := rowByPath(t, res, shop)
	want := Row{
		Path:           shop,
		ComposeFiles:   []string{"compose.yaml", "compose.override.yaml"},
		DefaultName:    "shop",
		ComposeProject: "shop",
		Status:         project.StatusPartial,
		Services:       2,
		Details: project.Details{Services: []project.Service{
			{Name: "db", Image: "postgres:16", Ports: []string{}, Volumes: []project.Volume{}},
			{Name: "web", Image: "nginx:1.27", Ports: []string{}, Volumes: []project.Volume{}},
		}},
		Databases: []project.Database{{Type: "postgresql", Name: "db", LocationKind: "container", Location: "db", Sources: []string{"service db"}}},
	}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("row\n got %+v\nwant %+v", row, want)
	}

	// The override variant is used, and the odd folder name reaches the runner intact.
	var sawOverride, sawOdd bool
	for _, c := range append(fc.callsOf(runner.ComposeConfig), fc.callsOf(runner.ComposeConfigOverride)...) {
		if c.Values["dir"] == shop {
			sawOverride = c.Action == runner.ComposeConfigOverride &&
				c.Values["file"] == filepath.Join(shop, "compose.yaml") &&
				c.Values["override"] == filepath.Join(shop, "compose.override.yaml")
		}
		if c.Values["dir"] == filepath.Join(root, odd) && c.Values["file"] == filepath.Join(root, odd, "compose.yaml") {
			sawOdd = true
		}
	}
	if !sawOverride || !sawOdd {
		t.Fatalf("override call %v, odd folder call %v: %+v", sawOverride, sawOdd, fc.calls)
	}
	if n := len(fc.callsOf(runner.DockerContainers)); n != 1 {
		t.Fatalf("docker.containers ran %d times, want once", n)
	}
	// The odd folder has no running container: Stopped.
	if s := rowByPath(t, res, filepath.Join(root, odd)).Status; s != project.StatusStopped {
		t.Fatalf("odd folder status %s, want stopped", s)
	}
}

func TestRowRoundTripsJSON(t *testing.T) {
	in := Result{
		Rows: []Row{{
			Path: "/w/a", ComposeFiles: []string{"compose.yaml"}, DefaultName: "a", ComposeProject: "a",
			Status: project.StatusRunning, Self: true, Services: 1,
			Details:   project.Details{Services: []project.Service{{Name: "web", Image: "nginx", Ports: []string{"80:80/tcp"}, Volumes: []project.Volume{{Type: "bind", Source: "/x", Target: "/y"}}}}},
			Databases: []project.Database{{Type: "sqlite", Name: "/d.db", LocationKind: "file", Location: "/d.db", Sources: []string{"DATABASE_URL (.env)"}}},
			Warning:   "w",
		}},
		FoldersScanned: 3, AlreadyTracked: 1, Ignored: 1, Warnings: []string{"cannot read /w/x"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"rows"`, `"composeFiles"`, `"defaultName"`, `"composeProject"`, `"details"`, `"foldersScanned"`, `"alreadyTracked"`, `"warnings"`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("%s missing from %s", key, b)
		}
	}
	var out Result
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip\n got %+v\nwant %+v", out, in)
	}
	p, _ := json.Marshal(Progress{Examined: 1, Total: 2, Current: "/w"})
	if string(p) != `{"examined":1,"total":2,"current":"/w"}` {
		t.Fatalf("progress JSON %s", p)
	}
}

func TestSelfDetected(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "panel/compose.yaml", "")
	mkfile(t, root, "other/compose.yaml", "")
	fc := newFakeCmd()
	fc.configs[filepath.Join(root, "panel")] = fakeReply{stdout: configJSON("scp", map[string]string{"panel": "ghcr.io/x/panel"})}
	fc.containers = containerLine("scp", "panel", "running", "Up", "panel") +
		containerLine("other", "app", "running", "Up", "")
	m, _ := newTestManager(t, fc)

	_, res := runScan(t, m, root, 1)
	if !rowByPath(t, res, filepath.Join(root, "panel")).Self {
		t.Fatal("the panel's own project is not self")
	}
	if r := rowByPath(t, res, filepath.Join(root, "other")); r.Self || r.Status != project.StatusRunning {
		t.Fatalf("other row %+v", r)
	}
}

func TestConfigFailureRowIsUnknownWithMaskedWarning(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "Broken App/compose.yaml", "")
	mkfile(t, root, "named/compose.yaml", "")
	mkfile(t, root, "named/.env", "COMPOSE_PROJECT_NAME=custom\nDB_PASSWORD=devpassword\n")
	mkfile(t, root, "big/compose.yaml", "")
	mkfile(t, root, "noisy/compose.yaml", "")
	fail := &runner.ExitError{Code: 1, Stderr: "required variable •••• missing\nsecond line"}
	fc := newFakeCmd()
	fc.configs[filepath.Join(root, "Broken App")] = fakeReply{err: fail}
	fc.configs[filepath.Join(root, "named")] = fakeReply{err: fail}
	fc.configs[filepath.Join(root, "big")] = fakeReply{stdout: `{"name":"big","services":{`, truncated: true}
	fc.configs[filepath.Join(root, "noisy")] = fakeReply{stdout: "WARN something\n{}"}
	fc.containers = containerLine("brokenapp", "web", "running", "Up", "")
	m, _ := newTestManager(t, fc)

	job, res := runScan(t, m, root, 1)
	if job.State != "done" {
		t.Fatalf("state %s, want done", job.State)
	}
	broken := rowByPath(t, res, filepath.Join(root, "Broken App"))
	want := Row{
		Path: filepath.Join(root, "Broken App"), ComposeFiles: []string{"compose.yaml"},
		DefaultName: "brokenapp", ComposeProject: "brokenapp", Status: project.StatusUnknown,
		Details: project.Details{Services: []project.Service{}}, Databases: []project.Database{},
		Warning: "required variable •••• missing",
	}
	if !reflect.DeepEqual(broken, want) {
		t.Fatalf("broken row\n got %+v\nwant %+v", broken, want)
	}
	if r := rowByPath(t, res, filepath.Join(root, "named")); r.ComposeProject != "custom" || r.DefaultName != "custom" || r.Status != project.StatusUnknown {
		t.Fatalf("named row %+v, want COMPOSE_PROJECT_NAME custom", r)
	}
	for _, name := range []string{"big", "noisy"} {
		if r := rowByPath(t, res, filepath.Join(root, name)); r.Warning == "" || r.Status != project.StatusUnknown || len(r.Details.Services) != 0 {
			t.Fatalf("%s row %+v, want a warning and Unknown", name, r)
		}
	}
	if strings.Contains(string(job.Result), "devpassword") {
		t.Fatal("a secret reached the stored result")
	}
}

func TestDockerDownEveryStatusUnknown(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "")
	mkfile(t, root, "b/compose.yaml", "")
	fc := newFakeCmd()
	fc.containersErr = &runner.TimeoutError{After: 15 * time.Second}
	m, _ := newTestManager(t, fc)

	job, res := runScan(t, m, root, 1)
	if job.State != "done" || len(res.Rows) != 2 {
		t.Fatalf("state %s, rows %d", job.State, len(res.Rows))
	}
	for _, r := range res.Rows {
		if r.Status != project.StatusUnknown || r.Services != 1 {
			t.Fatalf("row %+v, want Unknown with its config read", r)
		}
	}
}

func TestKnownPathsLeftOut(t *testing.T) {
	root := tempRoot(t)
	for _, n := range []string{"a", "b", "c"} {
		mkfile(t, root, n+"/compose.yaml", "")
	}
	fc := newFakeCmd()
	m, st := newTestManager(t, fc)
	_, _, err := st.AddWorkspace(context.Background(), store.NewWorkspace{Name: "old", Path: "/elsewhere", Depth: 1, LastScanAt: time.Now()},
		[]store.NewProject{
			{Path: filepath.Join(root, "a"), ComposeProject: "a", DisplayName: "a", Tracked: true},
			{Path: filepath.Join(root, "b"), ComposeProject: "b", DisplayName: "b"},
		})
	if err != nil {
		t.Fatal(err)
	}

	_, res := runScan(t, m, root, 1)
	if len(res.Rows) != 1 || res.Rows[0].Path != filepath.Join(root, "c") {
		t.Fatalf("rows %+v, want only c", res.Rows)
	}
	if res.AlreadyTracked != 1 || res.Ignored != 1 || res.FoldersScanned != 3 {
		t.Fatalf("alreadyTracked %d, ignored %d, foldersScanned %d", res.AlreadyTracked, res.Ignored, res.FoldersScanned)
	}
	if n := len(fc.callsOf(runner.ComposeConfig)); n != 1 {
		t.Fatalf("config ran %d times, want only for c", n)
	}
}

func TestDuplicateComposeNameWarned(t *testing.T) {
	root := tempRoot(t)
	for _, n := range []string{"one", "two", "three", "four"} {
		mkfile(t, root, n+"/compose.yaml", "")
	}
	fc := newFakeCmd()
	fc.configs[filepath.Join(root, "one")] = fakeReply{stdout: configJSON("same", map[string]string{"a": "x"})}
	fc.configs[filepath.Join(root, "two")] = fakeReply{stdout: configJSON("same", map[string]string{"a": "x"})}
	fc.configs[filepath.Join(root, "three")] = fakeReply{stdout: configJSON("stored", map[string]string{"a": "x"})}
	m, st := newTestManager(t, fc)
	_, _, err := st.AddWorkspace(context.Background(), store.NewWorkspace{Name: "old", Path: "/elsewhere", Depth: 1, LastScanAt: time.Now()},
		[]store.NewProject{{Path: "/elsewhere/x", ComposeProject: "stored", DisplayName: "x", Tracked: true}})
	if err != nil {
		t.Fatal(err)
	}

	_, res := runScan(t, m, root, 1)
	warn := func(x string) string {
		return "Another folder uses the compose project name " + x + "; Docker treats them as one project."
	}
	for name, want := range map[string]string{"one": warn("same"), "two": warn("same"), "three": warn("stored"), "four": ""} {
		if got := rowByPath(t, res, filepath.Join(root, name)).Warning; got != want {
			t.Fatalf("%s warning %q, want %q", name, got, want)
		}
	}
}

func TestOneScanAtATime(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "")
	fc := newFakeCmd()
	fc.gate = make(chan struct{})
	m, _ := newTestManager(t, fc)

	id, err := m.Start(context.Background(), root, 2, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Start(context.Background(), "/other", 1, "dev@localhost")
	var busy *BusyError
	if !errors.As(err, &busy) {
		close(fc.gate)
		t.Fatalf("second Start: %v, want *BusyError", err)
	}
	if *busy != (BusyError{ID: id, Path: root, Depth: 2}) {
		t.Fatalf("busy %+v", *busy)
	}
	close(fc.gate)
	m.wait()
	if _, err := m.Start(context.Background(), root, 1, "dev@localhost"); err != nil {
		t.Fatalf("Start after the first finished: %v", err)
	}
}

// collect reads events until the channel closes.
func collect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var evs []Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return evs
			}
			evs = append(evs, ev)
		case <-timeout:
			t.Fatalf("no end after %d events", len(evs))
		}
	}
}

func TestProgressReachesTotal(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "")
	mkdir(t, root, "b")
	mkdir(t, root, "c/d")
	fc := newFakeCmd()
	fc.gate = make(chan struct{})
	m, st := newTestManager(t, fc)

	id, err := m.Start(context.Background(), root, 2, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	_, events, cancel, live := m.Subscribe(id)
	defer cancel()
	if !live {
		t.Fatal("a running scan is not live")
	}
	if _, ok := m.Progress(id); !ok {
		t.Fatal("Progress of a running scan not found")
	}
	close(fc.gate)
	evs := collect(t, events)
	if len(evs) < 2 {
		t.Fatalf("events %+v", evs)
	}
	last, end := evs[len(evs)-2], evs[len(evs)-1]
	if last.Kind != "progress" || last.Progress.Examined != 4 || last.Progress.Total != 4 {
		t.Fatalf("last progress %+v, want 4/4", last)
	}
	if end.Kind != "end" || end.State != "done" {
		t.Fatalf("end %+v", end)
	}
	job, err := st.Job(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "done" {
		t.Fatalf("stored state %s", job.State)
	}
	var p Progress
	if err := json.Unmarshal(job.Progress, &p); err != nil || p.Examined != 4 || p.Total != 4 {
		t.Fatalf("stored progress %s, %v", job.Progress, err)
	}
	if _, ok := m.Progress(id); ok {
		t.Fatal("Progress of a finished scan still found")
	}
}

func TestLateSubscriberGetsNotLive(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "")
	m, _ := newTestManager(t, newFakeCmd())
	job, _ := runScan(t, m, root, 1)
	if _, events, cancel, live := m.Subscribe(job.ID); live || events != nil {
		t.Fatalf("live %v, events %v after the end", live, events)
	} else {
		cancel()
	}
	if _, _, cancel, live := m.Subscribe(9999); live {
		t.Fatal("an unknown job is live")
	} else {
		cancel()
	}
}

func TestManyFoldersCoalesced(t *testing.T) {
	root := tempRoot(t)
	for i := range 2000 {
		mkdir(t, root, fmt.Sprintf("f%04d", i))
	}
	fc := newFakeCmd()
	fc.gate = make(chan struct{})
	m, _ := newTestManager(t, fc)
	id, err := m.Start(context.Background(), root, 1, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	_, events, cancel, live := m.Subscribe(id)
	defer cancel()
	if !live {
		t.Fatal("not live")
	}
	close(fc.gate)
	evs := collect(t, events)
	n := 0
	for _, ev := range evs {
		if ev.Kind == "progress" {
			n++
		}
	}
	if n > 50 {
		t.Fatalf("%d progress events for 2000 folders, want at most 50", n)
	}
	if last := evs[len(evs)-2]; last.Progress.Examined != 2000 || last.Progress.Total != 2000 {
		t.Fatalf("last progress %+v", last)
	}
}

// A subscriber that never reads must not block the scan, and still gets end.
func TestStalledSubscriberDoesNotBlock(t *testing.T) {
	root := tempRoot(t)
	for i := range 50 {
		mkfile(t, root, fmt.Sprintf("p%02d/compose.yaml", i), "")
	}
	fc := newFakeCmd()
	fc.gate = make(chan struct{})
	m, _ := newTestManager(t, fc)
	m.interval = 0 // publish every update
	id, err := m.Start(context.Background(), root, 1, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	_, stalled, cancelStalled, _ := m.Subscribe(id)
	defer cancelStalled()
	_, dropped, cancelDropped, _ := m.Subscribe(id)
	cancelDropped()
	close(fc.gate)
	done := make(chan struct{})
	go func() { m.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the scan is blocked by a subscriber that does not read")
	}
	evs := collect(t, stalled)
	if end := evs[len(evs)-1]; end.Kind != "end" || end.State != "done" {
		t.Fatalf("stalled subscriber's last event %+v, want end", end)
	}
	if _, ok := <-dropped; ok {
		t.Fatal("a cancelled subscription still delivers")
	}
}

func TestSecretsPassedToRunner(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "shop/compose.yaml", "")
	mkfile(t, root, "shop/.env", "POSTGRES_PASSWORD=devpassword\nDATABASE_URL=postgres://shop:devpassword@db/shop\nPOSTGRES_DB=shop\n")
	fc := newFakeCmd()
	fc.configs[filepath.Join(root, "shop")] = fakeReply{stdout: configJSON("shop", map[string]string{"db": "postgres:16"})}
	m, _ := newTestManager(t, fc)

	job, res := runScan(t, m, root, 1)
	calls := fc.callsOf(runner.ComposeConfig)
	if len(calls) != 1 || !slices.Contains(calls[0].Secrets, "devpassword") {
		t.Fatalf("config calls %+v, want devpassword among the secrets", calls)
	}
	if strings.Contains(string(job.Result), "devpassword") || strings.Contains(string(job.Progress), "devpassword") {
		t.Fatal("a secret reached the stored job")
	}
	dbs := rowByPath(t, res, filepath.Join(root, "shop")).Databases
	if len(dbs) != 1 || dbs[0].Type != "postgresql" || len(dbs[0].Sources) < 2 {
		t.Fatalf("databases %+v, want the .env variables merged into the db service", dbs)
	}
}

func TestSymlinkedEnvNotRead(t *testing.T) {
	root := tempRoot(t)
	outside := tempRoot(t)
	mkfile(t, outside, "secret.env", "API_TOKEN=outsidetoken\n")
	mkfile(t, root, "p/compose.yaml", "")
	if err := os.Symlink(filepath.Join(outside, "secret.env"), filepath.Join(root, "p", ".env")); err != nil {
		t.Fatal(err)
	}
	fc := newFakeCmd()
	m, _ := newTestManager(t, fc)
	runScan(t, m, root, 1)
	calls := fc.callsOf(runner.ComposeConfig)
	if len(calls) != 1 || len(calls[0].Secrets) != 0 {
		t.Fatalf("calls %+v, want no secrets from a symlinked .env", calls)
	}
}

func TestStartRejectsBadDepth(t *testing.T) {
	m, _ := newTestManager(t, newFakeCmd())
	for _, d := range []int{0, 4} {
		if _, err := m.Start(context.Background(), "/w", d, "dev@localhost"); err == nil {
			t.Fatalf("depth %d accepted", d)
		}
	}
}

func TestReadContainers(t *testing.T) {
	fc := newFakeCmd()
	fc.containers = containerLine("a", "web", "running", "Up", "")
	cs, err := ReadContainers(context.Background(), fc)
	if err != nil || len(cs) != 1 || cs[0].Project != "a" {
		t.Fatalf("got %+v, %v", cs, err)
	}
	fc.containersErr = errors.New("daemon down")
	if _, err := ReadContainers(context.Background(), fc); err == nil {
		t.Fatal("no error when docker fails")
	}
}

func TestConfigWarningSkipsComposeWarnLines(t *testing.T) {
	fc := newFakeCmd()
	m, _ := newTestManager(t, fc)
	secrets := []string{"devpassword"}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"WARN before the error",
			&runner.ExitError{Code: 1, Stderr: "WARN[0000] /srv/a/docker-compose.yml: the attribute `version` is obsolete, it will be ignored\nrequired variable DB_NAME is missing"},
			"required variable DB_NAME is missing"},
		{"blank lines and several WARN lines",
			&runner.ExitError{Code: 1, Stderr: "\nWARN[0000] one\nWARN[0000] two\n\n  invalid interpolation\nmore"},
			"invalid interpolation"},
		{"only WARN lines gives the first",
			&runner.ExitError{Code: 1, Stderr: "WARN[0000] first warning\nWARN[0000] second"},
			"WARN[0000] first warning"},
		{"the chosen line is masked",
			&runner.ExitError{Code: 1, Stderr: "WARN[0000] obsolete\nbad value devpassword here"},
			"bad value •••• here"},
		{"empty stderr gives the exit status",
			&runner.ExitError{Code: 3},
			"exit status 3"},
		{"another error is masked too",
			errors.New("docker compose.config: devpassword\nnext"),
			"docker compose.config: ••••"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := "/srv/" + strings.ReplaceAll(tc.name, " ", "-")
			fc.configs[dir] = fakeReply{err: tc.err}
			_, got := m.readConfig(context.Background(), runner.ComposeConfig, runner.Values{"dir": dir}, secrets)
			if got != tc.want {
				t.Fatalf("warning %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWait(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "")
	fc := newFakeCmd()
	fc.gate = make(chan struct{})
	m, st := newTestManager(t, fc)

	if err := m.Wait(context.Background()); err != nil {
		t.Fatalf("Wait with no scan: %v", err)
	}
	id, err := m.Start(context.Background(), root, 1, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		close(fc.gate)
		t.Fatalf("Wait on a running scan: %v, want DeadlineExceeded", err)
	}
	close(fc.gate)
	if err := m.Wait(context.Background()); err != nil {
		t.Fatalf("Wait after the gate opened: %v", err)
	}
	// Wait returns only after the job's outcome is stored.
	job, err := st.Job(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "done" {
		t.Fatalf("stored state %q after Wait, want done", job.State)
	}
}
