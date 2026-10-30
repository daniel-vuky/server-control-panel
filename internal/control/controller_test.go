package control

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

const actor = "dev@localhost"

// testProject is a tracked project to seed: its folder name, compose files
// and whether it is the panel itself. Every one has the services app and db.
type testProject struct {
	name  string
	files []string
	self  bool
}

type env struct {
	ctl  *Controller
	st   *store.Store
	cmd  *fakeCmd
	ids  map[string]int64
	dirs map[string]string
}

// newEnv opens a temp store with the tracked projects shop (one compose file),
// blog, over (with an override) and panel (self). shop/.env holds devpassword.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	projects := []testProject{
		{name: "shop", files: []string{"compose.yaml"}},
		{name: "blog", files: []string{"compose.yaml"}},
		{name: "over", files: []string{"compose.yaml", "compose.override.yaml"}},
		{name: "panel", files: []string{"compose.yaml"}, self: true},
	}
	e := &env{st: st, cmd: newFakeCmd(), ids: map[string]int64{}, dirs: map[string]string{}}
	var ps []store.NewProject
	for _, p := range projects {
		dir := filepath.Join(root, p.name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, f := range p.files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("services: {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		e.dirs[p.name] = dir
		ps = append(ps, store.NewProject{
			Path: dir, ComposeFiles: p.files, ComposeProject: p.name, DisplayName: p.name,
			Tracked: true, Self: p.self,
			Details: project.Details{Services: []project.Service{{Name: "app"}, {Name: "db"}}},
		})
	}
	if err := os.WriteFile(filepath.Join(e.dirs["shop"], ".env"), []byte("DB_PASSWORD=devpassword\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AddWorkspace(ctx, store.NewWorkspace{Name: "root", Path: root, Depth: 1, LastScanAt: time.Now()}, ps); err != nil {
		t.Fatal(err)
	}
	ts, err := st.TrackedProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range ts {
		e.ids[tr.DisplayName] = tr.ID
	}
	e.ctl = NewController(st, e.cmd, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.ctl.Wait(ctx); err != nil {
			t.Errorf("Wait: %v", err)
		}
	})
	return e
}

// do runs Do on a background context and fails on a non-nil error.
func (e *env) do(t *testing.T, name string, a project.Action) Result {
	t.Helper()
	res, err := e.ctl.Do(context.Background(), e.ids[name], a, actor)
	if err != nil {
		t.Fatalf("Do(%s, %s) error = %v", name, a, err)
	}
	return res
}

// lastRow returns the newest activity row.
func (e *env) lastRow(t *testing.T) store.ActivityRow {
	t.Helper()
	rows, err := e.st.RecentActivity(context.Background(), 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	return rows[0]
}

func wantResult(t *testing.T, got Result, outcome Outcome, status project.Status, msg string) {
	t.Helper()
	if got.Outcome != outcome || got.Status != status || got.Message != msg {
		t.Fatalf("result = %+v, want {%s %s %q}", got, outcome, status, msg)
	}
}

func wantRow(t *testing.T, e *env, name, action, result, reason string) {
	t.Helper()
	r := e.lastRow(t)
	if r.Actor != actor || r.ActorKind != store.ActorPerson || r.Action != action ||
		r.ProjectID != e.ids[name] || r.Target != name || r.Result != result || r.Reason != reason || r.FinishedAt == nil {
		t.Fatalf("activity row = %+v, want %s %s %s %q", r, name, action, result, reason)
	}
}

func TestDoStartOK(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop"), runningOut("shop")).on(runner.ComposeUp, reply{})
	wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeOK, project.StatusRunning, "")
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultOK, "")

	calls := e.cmd.callsOf(runner.ComposeUp)
	if len(calls) != 1 {
		t.Fatalf("compose.up calls = %d, want 1", len(calls))
	}
	dir := e.dirs["shop"]
	want := runner.Values{"dir": dir, "file": filepath.Join(dir, "compose.yaml")}
	if c := calls[0]; c.Stack != runner.StackCompose || len(c.Values) != 2 || c.Values["dir"] != want["dir"] || c.Values["file"] != want["file"] {
		t.Fatalf("compose.up call = %+v, want values %v", c, want)
	}
	if _, in := e.ctl.InFlight(e.ids["shop"]); in {
		t.Fatal("shop is still locked")
	}
}

func TestDoStopOK(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(runningOut("shop"), stoppedOut("shop")).on(runner.ComposeStop, reply{})
	res := e.do(t, "shop", project.ActionStop)
	wantResult(t, res, OutcomeOK, project.StatusStopped, "")
	if !slices.Equal(res.Actions, []project.Action{project.ActionStart, project.ActionUntrack}) {
		t.Errorf("actions = %v, want [start untrack]", res.Actions)
	}
	wantRow(t, e, "shop", store.ActProjectStop, store.ResultOK, "")
}

func TestDoRestartOK(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(runningOut("shop")).on(runner.ComposeRestart, reply{})
	wantResult(t, e.do(t, "shop", project.ActionRestart), OutcomeOK, project.StatusRunning, "")
	wantRow(t, e, "shop", store.ActProjectRestart, store.ResultOK, "")
}

func TestDoStartPartialIsOK(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop"), partialOut("shop")).on(runner.ComposeUp, reply{})
	wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeOK, project.StatusPartial, "")
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultOK, "")
}

func TestDoStartLeavesErrorFails(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop"), errorOut("shop")).on(runner.ComposeUp, reply{})
	msg := "The command finished but shop is Error."
	wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeFailed, project.StatusError, msg)
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultFailed, msg)
}

func TestDoStopExited1IsOK(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(runningOut("shop"), exited1Out("shop")).on(runner.ComposeStop, reply{})
	wantResult(t, e.do(t, "shop", project.ActionStop), OutcomeOK, project.StatusError, "")
	wantRow(t, e, "shop", store.ActProjectStop, store.ResultOK, "")
}

func TestDoStopStillRunningFails(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(runningOut("shop"), partialOut("shop")).on(runner.ComposeStop, reply{})
	msg := "The command finished but shop is Partial."
	wantResult(t, e.do(t, "shop", project.ActionStop), OutcomeFailed, project.StatusPartial, msg)
	wantRow(t, e, "shop", store.ActProjectStop, store.ResultFailed, msg)
}

func TestDoRereadFails(t *testing.T) {
	for _, a := range []project.Action{project.ActionStart, project.ActionStop} {
		t.Run(string(a), func(t *testing.T) {
			e := newEnv(t)
			before := stoppedOut("shop")
			if a == project.ActionStop {
				before = runningOut("shop")
			}
			e.cmd.on(runner.DockerContainers, reply{stdout: before}, reply{err: errors.New("cannot connect to the Docker daemon")}).
				on(runner.ComposeUp, reply{}).on(runner.ComposeStop, reply{})
			msg := "The command finished but Docker could not be read."
			res := e.do(t, "shop", a)
			wantResult(t, res, OutcomeFailed, project.StatusUnknown, msg)
			if !slices.Equal(res.Actions, []project.Action{project.ActionUntrack}) {
				t.Errorf("actions = %v, want only untrack when Docker could not be read", res.Actions)
			}
			wantRow(t, e, "shop", "project."+string(a), store.ResultFailed, msg)
		})
	}
}

func TestDoExitErrorFails(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop")).on(runner.ComposeUp, reply{err: &runner.ExitError{Code: 1, Stderr: "line1\nboom"}})
	wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeFailed, project.StatusStopped, "line1\nboom")
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultFailed, "line1\nboom")
}

func TestDoExitErrorKeepsLastLines(t *testing.T) {
	e := newEnv(t)
	var lines []string
	for i := range 40 {
		lines = append(lines, strings.Repeat("x", 20)+string(rune('a'+i%26)))
	}
	e.cmd.containers(stoppedOut("shop")).on(runner.ComposeUp, reply{err: &runner.ExitError{Code: 1, Stderr: strings.Join(lines, "\n")}})
	// Each line is 21 characters plus a newline, so 13 lines (286) fit in 300.
	res := e.do(t, "shop", project.ActionStart)
	if want := strings.Join(lines[27:], "\n"); res.Message != want {
		t.Fatalf("message = %q, want the last 13 lines %q", res.Message, want)
	}

	// One line longer than 300 characters keeps its last 300.
	e.cmd.mu.Lock()
	e.cmd.queues[runner.ComposeUp] = []reply{{err: &runner.ExitError{Code: 1, Stderr: strings.Repeat("é", 250) + strings.Repeat("z", 100)}}}
	e.cmd.mu.Unlock()
	res = e.do(t, "shop", project.ActionStart)
	if want := strings.Repeat("é", 200) + strings.Repeat("z", 100); res.Message != want {
		t.Fatalf("message = %q, want the last 300 characters", res.Message)
	}
}

func TestDoExitErrorWithoutStderr(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop")).on(runner.ComposeUp, reply{err: &runner.ExitError{Code: 3}})
	wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeFailed, project.StatusStopped, "exit status 3")
}

func TestDoMasksSecrets(t *testing.T) {
	t.Run("exit error", func(t *testing.T) {
		e := newEnv(t)
		e.cmd.containers(stoppedOut("shop")).
			on(runner.ComposeUp, reply{err: &runner.ExitError{Code: 1, Stderr: "password authentication failed: ••••"}})
		res := e.do(t, "shop", project.ActionStart)
		calls := e.cmd.callsOf(runner.ComposeUp)
		if len(calls) != 1 || !slices.Contains(calls[0].Secrets, "devpassword") {
			t.Fatalf("compose.up secrets = %+v, want devpassword", calls)
		}
		row := e.lastRow(t)
		if res.Outcome != OutcomeFailed || strings.Contains(res.Message, "devpassword") || strings.Contains(row.Reason, "devpassword") {
			t.Fatalf("result %+v, reason %q leak the secret", res, row.Reason)
		}
		if !strings.Contains(res.Message, "••••") || !strings.Contains(row.Reason, "••••") {
			t.Fatalf("result %+v, reason %q, want the masked stderr", res, row.Reason)
		}
	})
	t.Run("other error", func(t *testing.T) {
		e := newEnv(t)
		e.cmd.containers(stoppedOut("shop")).on(runner.ComposeUp, reply{err: errors.New("cannot use devpassword here")})
		res := e.do(t, "shop", project.ActionStart)
		row := e.lastRow(t)
		if res.Message != "cannot use •••• here" || row.Reason != res.Message || row.Result != store.ResultFailed {
			t.Fatalf("result %+v, row %+v, want the masked error", res, row)
		}
	})
}

func TestDoTimeout(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop")).on(runner.ComposeUp, reply{err: &runner.TimeoutError{After: runner.LifecycleTimeout}})
	msg := "Timed out after 60 seconds."
	wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeTimedOut, project.StatusStopped, msg)
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultTimedOut, msg)
}

func TestDoTimeoutRereadFails(t *testing.T) {
	e := newEnv(t)
	e.cmd.on(runner.DockerContainers, reply{stdout: stoppedOut("shop")}, reply{err: errors.New("down")}).
		on(runner.ComposeUp, reply{err: &runner.TimeoutError{After: runner.LifecycleTimeout}})
	wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeTimedOut, project.StatusUnknown, "Timed out after 60 seconds.")
}

func TestDoRefusals(t *testing.T) {
	cases := []struct {
		name    string
		project string
		action  project.Action
		setup   func(e *env)
		code    string
		status  project.Status
		msg     string
	}{
		{
			name: "locked", project: "shop", action: project.ActionStart,
			setup: func(e *env) {
				e.cmd.containers(stoppedOut("shop"))
				e.ctl.mu.Lock()
				e.ctl.locks[e.ids["shop"]] = project.StatusStopping
				e.ctl.mu.Unlock()
			},
			code: CodeLocked, status: project.StatusStopping, msg: "shop is Stopping…; wait for it to finish.",
		},
		{
			name: "not allowed", project: "shop", action: project.ActionStop,
			setup: func(e *env) { e.cmd.containers(stoppedOut("shop")) },
			code:  CodeNotAllowed, status: project.StatusStopped, msg: "Stop is not allowed while shop is Stopped.",
		},
		{
			name: "restart not allowed", project: "shop", action: project.ActionRestart,
			setup: func(e *env) { e.cmd.containers(stoppedOut("shop")) },
			code:  CodeNotAllowed, status: project.StatusStopped, msg: "Restart is not allowed while shop is Stopped.",
		},
		{
			name: "self", project: "panel", action: project.ActionRestart,
			setup: func(e *env) { e.cmd.containers(runningOut("panel")) },
			code:  CodeNotAllowed, status: project.StatusRunning, msg: "panel is the panel itself and is Monitor only.",
		},
		{
			name: "docker down", project: "shop", action: project.ActionStart,
			setup: func(e *env) { e.cmd.on(runner.DockerContainers, reply{err: errors.New("cannot connect")}) },
			code:  CodeDockerDown, status: project.StatusUnknown, msg: "Docker is not responding. Try again when it is back.",
		},
		{
			name: "docker down before locked", project: "shop", action: project.ActionStart,
			setup: func(e *env) {
				e.cmd.on(runner.DockerContainers, reply{err: errors.New("cannot connect")})
				e.ctl.mu.Lock()
				e.ctl.locks[e.ids["shop"]] = project.StatusStopping
				e.ctl.mu.Unlock()
			},
			code: CodeDockerDown, status: project.StatusUnknown, msg: "Docker is not responding. Try again when it is back.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.setup(e)
			gen := e.ctl.Generation()
			_, err := e.ctl.Do(context.Background(), e.ids[tc.project], tc.action, actor)
			var ref *Refusal
			if !errors.As(err, &ref) {
				t.Fatalf("Do error = %v, want a *Refusal", err)
			}
			if ref.Code != tc.code || ref.Status != tc.status || ref.Message != tc.msg || ref.Error() != tc.msg {
				t.Fatalf("refusal = %+v, want {%s %q %s}", ref, tc.code, tc.msg, tc.status)
			}
			wantRow(t, e, tc.project, "project."+string(tc.action), store.ResultRefused, tc.msg)
			for _, a := range []runner.Action{runner.ComposeUp, runner.ComposeStop, runner.ComposeRestart} {
				if n := len(e.cmd.callsOf(a)); n != 0 {
					t.Fatalf("%s ran %d times", a, n)
				}
			}
			if e.ctl.Generation() != gen {
				t.Fatal("a refusal moved the generation")
			}
		})
	}
}

func TestDoUnknownProject(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop"))
	if _, err := e.ctl.Do(context.Background(), 999, project.ActionStart, actor); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Do error = %v, want ErrNotFound", err)
	}
	if _, err := e.ctl.Do(context.Background(), e.ids["shop"], "kill", actor); err == nil {
		t.Fatal("Do(kill) succeeded")
	}
}

func TestDoConcurrentSameProject(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.cmd.containers(stoppedOut("shop"), stoppedOut("shop"), runningOut("shop")).
		on(runner.ComposeUp, reply{gate: gate})

	first := make(chan Result, 1)
	go func() {
		res, _ := e.ctl.Do(context.Background(), e.ids["shop"], project.ActionStart, actor)
		first <- res
	}()
	e.cmd.waitRunning(t, runner.ComposeUp, 1)

	_, err := e.ctl.Do(context.Background(), e.ids["shop"], project.ActionStart, actor)
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Code != CodeLocked || ref.Status != project.StatusStarting ||
		ref.Message != "shop is Starting…; wait for it to finish." {
		t.Fatalf("second Do error = %v, want locked", err)
	}
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultRefused, ref.Message)

	close(gate)
	wantResult(t, <-first, OutcomeOK, project.StatusRunning, "")
	if n := len(e.cmd.callsOf(runner.ComposeUp)); n != 1 {
		t.Fatalf("compose.up ran %d times, want 1", n)
	}
}

func TestDoOtherProjectRunsInParallel(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	both := func(f func(string) string) string { return f("shop") + f("blog") }
	e.cmd.containers(both(stoppedOut), both(stoppedOut), both(runningOut)).
		on(runner.ComposeUp, reply{gate: gate})

	results := make(chan Result, 2)
	for _, name := range []string{"shop", "blog"} {
		go func() {
			res, _ := e.ctl.Do(context.Background(), e.ids[name], project.ActionStart, actor)
			results <- res
		}()
	}
	e.cmd.waitRunning(t, runner.ComposeUp, 2)
	close(gate)
	for range 2 {
		wantResult(t, <-results, OutcomeOK, project.StatusRunning, "")
	}
}

func TestDoReleasesLockOnPanic(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop")).on(runner.ComposeUp, reply{panic: true})
	gen := e.ctl.Generation()
	wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeFailed, project.StatusUnknown, "internal error")
	if _, in := e.ctl.InFlight(e.ids["shop"]); in {
		t.Fatal("shop is still locked after a panic")
	}
	if g := e.ctl.Generation(); g != gen+2 {
		t.Fatalf("generation = %d, want %d", g, gen+2)
	}
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultFailed, "internal error")
}

func TestDoUsesOverrideEntry(t *testing.T) {
	cases := []struct {
		action project.Action
		before string
		want   runner.Action
	}{
		{project.ActionStart, stoppedOut("over"), runner.ComposeUpOverride},
		{project.ActionStop, runningOut("over"), runner.ComposeStopOverride},
		{project.ActionRestart, runningOut("over"), runner.ComposeRestartOverride},
	}
	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			e := newEnv(t)
			e.cmd.containers(tc.before).on(tc.want, reply{})
			e.do(t, "over", tc.action)
			calls := e.cmd.callsOf(tc.want)
			dir := e.dirs["over"]
			if len(calls) != 1 {
				t.Fatalf("%s calls = %d, want 1", tc.want, len(calls))
			}
			v := calls[0].Values
			if len(v) != 3 || v["dir"] != dir || v["file"] != filepath.Join(dir, "compose.yaml") ||
				v["override"] != filepath.Join(dir, "compose.override.yaml") {
				t.Fatalf("values = %v", v)
			}
		})
	}
}

func TestInFlightAndGeneration(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.cmd.containers(stoppedOut("shop"), runningOut("shop")).on(runner.ComposeUp, reply{gate: gate})
	id := e.ids["shop"]
	gen := e.ctl.Generation()
	if _, in := e.ctl.InFlight(id); in {
		t.Fatal("shop is locked before Do")
	}

	done := make(chan Result, 1)
	go func() {
		res, _ := e.ctl.Do(context.Background(), id, project.ActionStart, actor)
		done <- res
	}()
	e.cmd.waitRunning(t, runner.ComposeUp, 1)
	if s, in := e.ctl.InFlight(id); !in || s != project.StatusStarting {
		t.Fatalf("InFlight = %s, %v, want starting", s, in)
	}
	if g := e.ctl.Generation(); g != gen+1 {
		t.Fatalf("generation while running = %d, want %d", g, gen+1)
	}
	if r := e.lastRow(t); r.Result != store.ResultRunning || r.FinishedAt != nil {
		t.Fatalf("row while running = %+v", r)
	}

	close(gate)
	<-done
	if _, in := e.ctl.InFlight(id); in {
		t.Fatal("shop is locked after Do")
	}
	if g := e.ctl.Generation(); g != gen+2 {
		t.Fatalf("generation after = %d, want %d", g, gen+2)
	}
}

func TestDoCommandSurvivesCanceledContext(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.cmd.containers(runningOut("shop"), stoppedOut("shop")).on(runner.ComposeStop, reply{gate: gate})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := e.ctl.Do(ctx, e.ids["shop"], project.ActionStop, actor)
		errc <- err
	}()
	e.cmd.waitRunning(t, runner.ComposeStop, 1)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Do did not return on cancel")
	}
	if _, in := e.ctl.InFlight(e.ids["shop"]); !in {
		t.Fatal("the lock was released before the command finished")
	}

	close(gate)
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wcancel()
	if err := e.ctl.Wait(wctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	wantRow(t, e, "shop", store.ActProjectStop, store.ResultOK, "")
	if _, in := e.ctl.InFlight(e.ids["shop"]); in {
		t.Fatal("shop is still locked")
	}
}

func TestDoObserver(t *testing.T) {
	t.Run("recent list and lock changes", func(t *testing.T) {
		e := newEnv(t)
		id := e.ids["shop"]
		obs := &fakeObserver{ctl: e.ctl, id: id, cs: project.ParseContainers([]byte(runningOut("shop"))), dockerUp: true, ok: true}
		e.ctl.setObserver(obs)
		e.cmd.containers(stoppedOut("shop")).on(runner.ComposeStop, reply{})
		gen := e.ctl.Generation()

		wantResult(t, e.do(t, "shop", project.ActionStop), OutcomeOK, project.StatusStopped, "")
		if n := len(e.cmd.callsOf(runner.DockerContainers)); n != 1 {
			t.Fatalf("docker.containers ran %d times, want 1 (the re-read only)", n)
		}
		obs.mu.Lock()
		defer obs.mu.Unlock()
		if len(obs.recentAge) != 1 || obs.recentAge[0] != 10*time.Second {
			t.Fatalf("recent calls = %v, want one with 10s", obs.recentAge)
		}
		if len(obs.changes) != 2 {
			t.Fatalf("lockChanged calls = %+v, want 2", obs.changes)
		}
		take, release := obs.changes[0], obs.changes[1]
		if take.fresh != nil || !take.inFlight || take.gen != gen+1 {
			t.Fatalf("take = %+v", take)
		}
		if len(release.fresh) != 2 || release.inFlight || release.gen != gen+2 {
			t.Fatalf("release = %+v", release)
		}
	})
	t.Run("recent docker down", func(t *testing.T) {
		e := newEnv(t)
		e.ctl.setObserver(&fakeObserver{ctl: e.ctl, ok: true, dockerUp: false})
		_, err := e.ctl.Do(context.Background(), e.ids["shop"], project.ActionStart, actor)
		var ref *Refusal
		if !errors.As(err, &ref) || ref.Code != CodeDockerDown {
			t.Fatalf("Do error = %v, want docker_down", err)
		}
		if n := len(e.cmd.callsOf(runner.DockerContainers)); n != 0 {
			t.Fatalf("docker.containers ran %d times, want 0", n)
		}
	})
	t.Run("stale recent reads docker", func(t *testing.T) {
		e := newEnv(t)
		e.ctl.setObserver(&fakeObserver{ctl: e.ctl, ok: false})
		e.cmd.containers(stoppedOut("shop"), runningOut("shop")).on(runner.ComposeUp, reply{})
		wantResult(t, e.do(t, "shop", project.ActionStart), OutcomeOK, project.StatusRunning, "")
		if n := len(e.cmd.callsOf(runner.DockerContainers)); n != 2 {
			t.Fatalf("docker.containers ran %d times, want 2", n)
		}
	})
}

// A re-read that succeeds with no container at all (the last project on the
// host stopped) is handed to the observer as an empty list, not nil, which
// would mean the re-read failed and leave the old list in place.
func TestDoEmptyRereadIsFresh(t *testing.T) {
	e := newEnv(t)
	id := e.ids["shop"]
	obs := &fakeObserver{ctl: e.ctl, id: id, cs: project.ParseContainers([]byte(runningOut("shop"))), dockerUp: true, ok: true}
	e.ctl.setObserver(obs)
	e.cmd.containers("").on(runner.ComposeStop, reply{})

	wantResult(t, e.do(t, "shop", project.ActionStop), OutcomeOK, project.StatusStopped, "")
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.changes) != 2 {
		t.Fatalf("lockChanged calls = %+v, want 2", obs.changes)
	}
	if release := obs.changes[1]; release.fresh == nil || len(release.fresh) != 0 {
		t.Fatalf("release fresh = %#v, want an empty non-nil list", release.fresh)
	}
}
