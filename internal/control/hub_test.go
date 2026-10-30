package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// newHubEnv is newEnv plus a fifth project, notes, in a second workspace, with
// a Controller and a Hub wired by New. tickInterval is set to interval for the
// test. Every loop the test starts must have stopped by the end of the test.
func newHubEnv(t *testing.T, interval time.Duration) (*env, *Hub) {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "notes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.st.AddWorkspace(ctx, store.NewWorkspace{Name: "second", Path: root, Depth: 1, LastScanAt: time.Now()},
		[]store.NewProject{{
			Path: dir, ComposeFiles: []string{"compose.yaml"}, ComposeProject: "notes", DisplayName: "notes", Tracked: true,
			Details: project.Details{Services: []project.Service{{Name: "app"}, {Name: "db"}}},
		}}); err != nil {
		t.Fatal(err)
	}
	ts, err := e.st.TrackedProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range ts {
		e.ids[tr.DisplayName] = tr.ID
	}
	e.dirs["notes"] = dir

	old := tickInterval
	tickInterval = interval
	t.Cleanup(func() { tickInterval = old })

	var h *Hub
	e.ctl, h = New(e.st, e.cmd, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { waitLoops(t, h) })
	return e, h
}

// waitLoops waits until every hub loop has returned.
func waitLoops(t *testing.T, h *Hub) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		h.loops.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the hub loop did not stop")
	}
}

// subscribe subscribes for the rest of the test.
func subscribe(t *testing.T, h *Hub) (State, <-chan State) {
	t.Helper()
	cur, updates, cancel := h.Subscribe()
	t.Cleanup(cancel)
	return cur, updates
}

// viewOf returns the view of the project named name.
func viewOf(t *testing.T, s State, name string) ProjectView {
	t.Helper()
	for _, v := range s.Projects {
		if v.DisplayName == name {
			return v
		}
	}
	t.Fatalf("no project %s in %+v", name, s)
	return ProjectView{}
}

// waitTicks waits until the fake has seen n more pings than it had.
func waitTicks(t *testing.T, cmd *fakeCmd, n int) {
	t.Helper()
	want := cmd.count(runner.DockerPing) + n
	deadline := time.Now().Add(5 * time.Second)
	for cmd.count(runner.DockerPing) < want {
		if time.Now().After(deadline) {
			t.Fatalf("%d pings, want %d", cmd.count(runner.DockerPing), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// noUpdate fails when an update is waiting.
func noUpdate(t *testing.T, updates <-chan State) {
	t.Helper()
	select {
	case s := <-updates:
		t.Fatalf("unexpected update %+v", s)
	default:
	}
}

// nextUpdate waits for the next update.
func nextUpdate(t *testing.T, updates <-chan State, within time.Duration) State {
	t.Helper()
	select {
	case s := <-updates:
		return s
	case <-time.After(within):
		t.Fatalf("no update within %s", within)
		return State{}
	}
}

func TestViewsEffectiveStatus(t *testing.T) {
	ts := []store.Tracked{
		{ID: 1, DisplayName: "shop", ComposeProject: "shop", Services: []string{"app", "db"}},
		{ID: 2, DisplayName: "blog", ComposeProject: "blog", Services: []string{"app", "db"}},
		{ID: 3, DisplayName: "panel", ComposeProject: "panel", Services: []string{"app", "db"}, Self: true},
	}
	cs := project.ParseContainers([]byte(runningOut("shop") + runningOut("blog") + runningOut("panel")))
	inFlight := func(id int64) (project.Status, bool, bool) {
		if id == 2 {
			return project.StatusStopping, false, true
		}
		return "", false, false
	}
	type want struct {
		status  project.Status
		actions []project.Action
	}
	tests := []struct {
		name     string
		readErr  error
		dockerUp bool
		inFlight func(int64) (project.Status, bool, bool)
		want     []want
	}{
		{"in-flight wins, self has only untrack", nil, true, inFlight, []want{
			{project.StatusRunning, []project.Action{project.ActionStop, project.ActionRestart, project.ActionUntrack}},
			{project.StatusStopping, []project.Action{}},
			{project.StatusRunning, []project.Action{project.ActionUntrack}},
		}},
		{"a read error gives unknown", errors.New("boom"), true, inFlight, []want{
			{project.StatusUnknown, []project.Action{project.ActionUntrack}},
			{project.StatusStopping, []project.Action{}},
			{project.StatusUnknown, []project.Action{project.ActionUntrack}},
		}},
		{"Docker down gives only untrack", errors.New("down"), false, nil, []want{
			{project.StatusUnknown, []project.Action{project.ActionUntrack}},
			{project.StatusUnknown, []project.Action{project.ActionUntrack}},
			{project.StatusUnknown, []project.Action{project.ActionUntrack}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := Views(ts, cs, tt.readErr, tt.dockerUp, tt.inFlight, nil, nil)
			if len(vs) != len(ts) {
				t.Fatalf("got %d views, want %d", len(vs), len(ts))
			}
			for i, v := range vs {
				if v.ID != ts[i].ID || v.Status != tt.want[i].status || !slices.Equal(v.Actions, tt.want[i].actions) || v.Actions == nil {
					t.Errorf("view %s = %s %v, want %s %v", v.DisplayName, v.Status, v.Actions, tt.want[i].status, tt.want[i].actions)
				}
			}
		})
	}
}

func TestHubNoWorkWithoutSubscribers(t *testing.T) {
	e, _ := newHubEnv(t, 20*time.Millisecond)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	time.Sleep(100 * time.Millisecond)
	if n := len(e.cmd.callsOf(runner.DockerPing)) + len(e.cmd.callsOf(runner.DockerContainers)); n != 0 {
		t.Fatalf("%d Docker calls with no subscriber, want 0", n)
	}
}

func TestHubStopsWhenLastSubscriberLeaves(t *testing.T) {
	e, h := newHubEnv(t, 20*time.Millisecond)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	_, _, cancel1 := h.Subscribe()
	_, _, cancel2 := h.Subscribe()
	cancel1()
	cancel1() // a second cancel does nothing
	waitTicks(t, e.cmd, 2)
	cancel2()
	waitLoops(t, h)
	n := e.cmd.count(runner.DockerPing)
	time.Sleep(100 * time.Millisecond)
	if got := e.cmd.count(runner.DockerPing); got != n {
		t.Fatalf("%d pings after the last cancel, want %d", got, n)
	}
}

func TestHubSubscribeGetsState(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop") + partialOut("notes"))
	cur, _ := subscribe(t, h)
	if !cur.DockerUp || len(cur.Projects) != 5 {
		t.Fatalf("state = %+v, want Docker up and 5 projects", cur)
	}
	for name, want := range map[string]project.Status{
		"shop": project.StatusRunning, "notes": project.StatusPartial, "blog": project.StatusStopped, "panel": project.StatusStopped,
	} {
		if v := viewOf(t, cur, name); v.Status != want {
			t.Errorf("%s is %s, want %s", name, v.Status, want)
		}
	}
	if v := viewOf(t, cur, "panel"); !slices.Equal(v.Actions, []project.Action{project.ActionUntrack}) {
		t.Errorf("panel actions = %v, want only untrack", v.Actions)
	}
	if v := viewOf(t, cur, "shop"); !slices.Equal(v.Actions, []project.Action{project.ActionStop, project.ActionRestart, project.ActionUntrack}) {
		t.Errorf("shop actions = %v", v.Actions)
	}

	// A second subscriber within recentMax gets the state from the last tick.
	n := e.cmd.count(runner.DockerContainers)
	cur2, _ := subscribe(t, h)
	if viewOf(t, cur2, "shop").Status != project.StatusRunning {
		t.Fatalf("second subscriber state = %+v", cur2)
	}
	if got := e.cmd.count(runner.DockerContainers); got != n {
		t.Fatalf("the second subscribe read Docker %d times", got-n)
	}
}

func TestHubTicksNeverOverlap(t *testing.T) {
	e, h := newHubEnv(t, 20*time.Millisecond)
	e.cmd.on(runner.DockerPing, reply{delay: 50 * time.Millisecond}).
		on(runner.DockerContainers, reply{stdout: runningOut("shop"), delay: 50 * time.Millisecond})
	subscribe(t, h)

	var wg sync.WaitGroup
	stop := time.Now().Add(300 * time.Millisecond)
	for range 4 {
		wg.Go(func() {
			for time.Now().Before(stop) {
				if s := h.Check(context.Background()); !s.DockerUp {
					t.Errorf("Check = %+v, want Docker up", s)
					return
				}
			}
		})
	}
	wg.Wait()
	if p := e.cmd.maxInFlight(); p != 1 {
		t.Fatalf("%d Docker calls in flight at once, want 1", p)
	}
}

func TestHubCheckWaitsForTheTickInProgress(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	gate := make(chan struct{})
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")).on(runner.DockerContainers, reply{stdout: stoppedOut("shop"), gate: gate})
	subscribe(t, h)

	first := make(chan State, 1)
	go func() { first <- h.Check(context.Background()) }()
	e.cmd.waitRunning(t, runner.DockerContainers, 1)
	second := make(chan State, 1)
	go func() { second <- h.Check(context.Background()) }()
	time.Sleep(20 * time.Millisecond) // let the second Check find the tick in progress
	close(gate)
	for _, ch := range []chan State{first, second} {
		if s := <-ch; viewOf(t, s, "shop").Status != project.StatusStopped {
			t.Fatalf("Check = %+v, want shop stopped", s)
		}
	}
	if n := e.cmd.count(runner.DockerContainers); n != 2 {
		t.Fatalf("%d containers reads, want 2 (one subscribe tick, one shared check)", n)
	}
}

func TestHubBroadcastsOnlyOnChange(t *testing.T) {
	e, h := newHubEnv(t, 20*time.Millisecond)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	cur, updates := subscribe(t, h)
	if viewOf(t, cur, "shop").Status != project.StatusRunning {
		t.Fatalf("state = %+v", cur)
	}
	waitTicks(t, e.cmd, 3)
	noUpdate(t, updates)

	e.cmd.set(runner.DockerContainers, reply{stdout: stoppedOut("shop")})
	s := nextUpdate(t, updates, 2*time.Second)
	if viewOf(t, s, "shop").Status != project.StatusStopped {
		t.Fatalf("update = %+v, want shop stopped", s)
	}
	waitTicks(t, e.cmd, 3)
	noUpdate(t, updates)
}

func TestHubLockChangedPushesWithoutTick(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	_, updates := subscribe(t, h)
	reads := e.cmd.count(runner.DockerPing) + e.cmd.count(runner.DockerContainers)

	// Take the lock as Do does, then tell the hub.
	id := e.ids["shop"]
	e.ctl.mu.Lock()
	e.ctl.locks[id] = projectLock{status: project.StatusStopping}
	e.ctl.gen++
	e.ctl.mu.Unlock()
	start := time.Now()
	h.lockChanged(nil)
	s := nextUpdate(t, updates, 10*time.Millisecond)
	if d := time.Since(start); d > 10*time.Millisecond {
		t.Fatalf("update after %s, want within 10ms", d)
	}
	if v := viewOf(t, s, "shop"); v.Status != project.StatusStopping || len(v.Actions) != 0 {
		t.Fatalf("shop = %s %v, want stopping with no actions", v.Status, v.Actions)
	}

	// Release with the re-read list: the hub uses it at once.
	e.ctl.unlock(id)
	h.lockChanged(project.ParseContainers([]byte(stoppedOut("shop"))))
	s = nextUpdate(t, updates, 10*time.Millisecond)
	if v := viewOf(t, s, "shop"); v.Status != project.StatusStopped || !slices.Equal(v.Actions, []project.Action{project.ActionStart, project.ActionUntrack}) {
		t.Fatalf("shop = %s %v, want stopped with start and untrack", v.Status, v.Actions)
	}
	if n := e.cmd.count(runner.DockerPing) + e.cmd.count(runner.DockerContainers); n != reads {
		t.Fatalf("%d Docker reads after the lock changes, want none", n-reads)
	}
	// The fresh list is what recent hands the pre-check.
	cs, up, ok := h.recent(recentMax)
	if !ok || !up || project.StatusOf(cs, "shop", []string{"app", "db"}) != project.StatusStopped {
		t.Fatalf("recent = %v %v %v, want the stopped list", cs, up, ok)
	}
}

// A hub read that began before a stop and lands after its release must not
// put the project back to running (review focus 3).
func TestHubDropsStaleTick(t *testing.T) {
	// The loop ticks once; the stale tick is driven by Check, so it is the
	// only tick in progress when the gate opens.
	e, h := newHubEnv(t, time.Hour)
	gate := make(chan struct{})
	e.cmd.on(runner.DockerPing, reply{}).
		containers(runningOut("shop")).                                             // 1: the first tick
		on(runner.DockerContainers, reply{stdout: runningOut("shop"), gate: gate}). // 2: the stale hub read
		containers(stoppedOut("shop")).                                             // 3: the controller's re-read, then the retry
		on(runner.ComposeStop, reply{})
	cur, updates := subscribe(t, h)
	if viewOf(t, cur, "shop").Status != project.StatusRunning {
		t.Fatalf("state = %+v", cur)
	}

	stale := make(chan State, 1)
	go func() { stale <- h.Check(context.Background()) }()
	e.cmd.waitRunning(t, runner.DockerContainers, 1)

	res := e.do(t, "shop", project.ActionStop)
	wantResult(t, res, OutcomeOK, project.StatusStopped, "")
	if n := e.cmd.count(runner.DockerContainers); n != 3 {
		t.Fatalf("%d containers reads before the gate opens, want 3 (the pre-check uses the hub's list)", n)
	}
	close(gate)

	s := <-stale
	if v := viewOf(t, s, "shop"); v.Status != project.StatusStopped {
		t.Fatalf("state after the stale tick = %s, want stopped", v.Status)
	}
	for {
		select {
		case u := <-updates:
			if v := viewOf(t, u, "shop"); v.Status == project.StatusRunning {
				t.Fatalf("an update put shop back to running")
			}
			continue
		default:
		}
		break
	}
	if s := h.Check(context.Background()); viewOf(t, s, "shop").Status != project.StatusStopped {
		t.Fatalf("last state = %+v, want shop stopped", s)
	}
}

func TestHubStaleTickGivesUpAfterThreeReads(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	subscribe(t, h)

	// Every read lands after a lock change: the hub publishes its cached
	// list after the third read.
	before := e.cmd.count(runner.DockerContainers)
	e.cmd.set(runner.DockerContainers, reply{stdout: stoppedOut("shop"), hook: func() {
		e.ctl.mu.Lock()
		e.ctl.gen++
		e.ctl.mu.Unlock()
	}})
	s := h.Check(context.Background())
	if n := e.cmd.count(runner.DockerContainers) - before; n != 3 {
		t.Fatalf("%d reads, want 3", n)
	}
	if !s.DockerUp || viewOf(t, s, "shop").Status != project.StatusRunning {
		t.Fatalf("state = %+v, want Docker up and the cached running list", s)
	}
}

// Do, Untrack, WithLocks and Refresh mixed with Check and the loop.
func TestNoDeadlock(t *testing.T) {
	e, h := newHubEnv(t, 5*time.Millisecond)
	all := ""
	names := []string{"shop", "blog", "over", "panel", "notes"}
	for _, n := range names {
		all += partialOut(n)
	}
	e.cmd.on(runner.DockerPing, reply{}).containers(all)
	for _, a := range []runner.Action{
		runner.ComposeUp, runner.ComposeUpOverride, runner.ComposeStop, runner.ComposeStopOverride,
		runner.ComposeRestart, runner.ComposeRestartOverride,
	} {
		e.cmd.on(a, reply{delay: time.Millisecond})
	}
	e.markMissing(t, "notes")
	_, updates := subscribe(t, h)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithCancel(context.Background())
		var bg sync.WaitGroup
		bg.Go(func() {
			for ctx.Err() == nil {
				h.Check(ctx)
			}
		})
		bg.Go(func() {
			for ctx.Err() == nil {
				h.Refresh()
				time.Sleep(time.Millisecond)
			}
		})
		bg.Go(func() {
			for {
				select {
				case <-updates:
				case <-ctx.Done():
					return
				}
			}
		})
		ok := func(err error) bool {
			var r *Refusal
			return err == nil || errors.As(err, &r) || errors.Is(err, store.ErrNotFound)
		}
		var wg sync.WaitGroup
		actions := []project.Action{project.ActionStart, project.ActionStop, project.ActionRestart}
		for i := range 60 {
			id := e.ids[names[i%len(names)]]
			wg.Go(func() {
				switch i % 4 {
				case 0:
					if _, err := e.ctl.Untrack(context.Background(), id, actor); !ok(err) {
						t.Errorf("Untrack: %v", err)
					}
				case 1:
					if err := e.ctl.WithLocks([]int64{e.ids["shop"], e.ids["blog"]}, func() error {
						_, err := e.st.TrackedProjects(context.Background())
						return err
					}); !ok(err) {
						t.Errorf("WithLocks: %v", err)
					}
				default:
					if _, err := e.ctl.Do(context.Background(), id, actions[i%len(actions)], actor); !ok(err) {
						t.Errorf("Do: %v", err)
					}
				}
			})
		}
		wg.Wait()
		cancel()
		bg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		buf := make([]byte, 1<<20)
		t.Fatalf("deadlock: not done after 10s\n%s", buf[:runtime.Stack(buf, true)])
	}
	for _, n := range names {
		if _, _, locked := e.ctl.Lock(e.ids[n]); locked {
			t.Errorf("%s is still locked", n)
		}
	}
}

func TestHubDockerDown(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{err: errors.New("Cannot connect to the Docker daemon")}).containers(runningOut("shop"))
	cur, _ := subscribe(t, h)
	if cur.DockerUp || len(cur.Projects) != 5 {
		t.Fatalf("state = %+v, want Docker down and 5 projects", cur)
	}
	for _, v := range cur.Projects {
		if v.Status != project.StatusUnknown || !slices.Equal(v.Actions, []project.Action{project.ActionUntrack}) {
			t.Errorf("%s = %s %v, want unknown with only untrack", v.DisplayName, v.Status, v.Actions)
		}
	}
	if n := e.cmd.count(runner.DockerContainers); n != 0 {
		t.Fatalf("%d containers reads with Docker down, want 0", n)
	}
	// The pre-check sees Docker down without reading it.
	if _, up, ok := h.recent(recentMax); !ok || up {
		t.Fatalf("recent: up %v ok %v, want down and ok", up, ok)
	}

	e.cmd.set(runner.DockerPing, reply{})
	s := h.Check(context.Background())
	if !s.DockerUp || viewOf(t, s, "shop").Status != project.StatusRunning {
		t.Fatalf("Check = %+v, want Docker up and shop running", s)
	}
}

func TestHubSurvivesAPanicInATick(t *testing.T) {
	e, h := newHubEnv(t, 10*time.Millisecond)
	// The second ping panics, on the loop goroutine; the others succeed.
	e.cmd.on(runner.DockerPing, reply{}, reply{panic: true}, reply{}).containers(runningOut("shop"))
	cur, _ := subscribe(t, h)
	if !cur.DockerUp {
		t.Fatalf("state = %+v, want Docker up", cur)
	}
	// The loop keeps ticking after the panic, and a Check still works.
	waitTicks(t, e.cmd, 3)
	if s := h.Check(context.Background()); !s.DockerUp || viewOf(t, s, "shop").Status != project.StatusRunning {
		t.Fatalf("Check after a panic = %+v, want Docker up and shop running", s)
	}
}

func TestHubPanicReleasesWaiters(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	gate := make(chan struct{})
	e.cmd.on(runner.DockerPing, reply{gate: gate, panic: true}, reply{}).containers(runningOut("shop"))

	first := make(chan State, 1)
	go func() { first <- h.Check(context.Background()) }()
	e.cmd.waitRunning(t, runner.DockerPing, 1)
	second := make(chan State, 1)
	go func() { second <- h.Check(context.Background()) }()
	time.Sleep(20 * time.Millisecond) // the second Check now waits on the tick
	close(gate)

	for name, ch := range map[string]chan State{"first": first, "second": second} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("the %s Check was not released after the panic", name)
		}
	}
	if s := h.Check(context.Background()); !s.DockerUp {
		t.Fatalf("Check after the panic = %+v, want Docker up", s)
	}
}

func TestHubNotReadyWhenTheFirstTickCannotReadTheProjects(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	if err := e.st.Close(); err != nil { // TrackedProjects now fails
		t.Fatal(err)
	}
	cur, _ := subscribe(t, h)
	if len(cur.Projects) != 0 {
		t.Fatalf("state = %+v, want no projects", cur)
	}
	h.mu.Lock()
	ready := h.ready
	h.mu.Unlock()
	if ready {
		t.Fatal("the hub is ready though it never read the tracked projects")
	}
	// Not ready, so the next subscriber does not reuse that empty state.
	n := e.cmd.count(runner.DockerPing)
	subscribe(t, h)
	if got := e.cmd.count(runner.DockerPing); got <= n {
		t.Fatalf("the second subscribe ran no tick (%d pings, had %d)", got, n)
	}
}

// setMetrics sets metricsInterval and metricsMaxAge for the test. Call it
// before newHubEnv, so the values are restored only after the loops stopped.
func setMetrics(t *testing.T, interval, maxAge time.Duration) {
	t.Helper()
	oldInterval, oldAge := metricsInterval, metricsMaxAge
	metricsInterval, metricsMaxAge = interval, maxAge
	t.Cleanup(func() { metricsInterval, metricsMaxAge = oldInterval, oldAge })
}

// componentOf returns the component of v for service.
func componentOf(t *testing.T, v ProjectView, service string) project.Component {
	t.Helper()
	for _, c := range v.Components {
		if c.Service == service {
			return c
		}
	}
	t.Fatalf("no component %s in %+v", service, v.Components)
	return project.Component{}
}

// untilUpdate reads updates until ok accepts one.
func untilUpdate(t *testing.T, updates <-chan State, ok func(State) bool) State {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-updates:
			if ok(s) {
				return s
			}
		case <-deadline:
			t.Fatal("no matching update within 5s")
			return State{}
		}
	}
}

// cpuOf returns the CPU total of the project named name.
func cpuOf(t *testing.T, s State, name string) *float64 {
	t.Helper()
	return viewOf(t, s, name).CPUPercent
}

func TestTickRunsInspectForTrackedContainers(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.markMissing(t, "notes")
	oneOff := strings.Join([]string{"shop", "app", "running", "Up 1 second", "nginx", "True",
		containerID("shop", "run"), "shop-app-run-1", "nginx", ""}, "\t") + "\n"
	e.cmd.on(runner.DockerPing, reply{}).
		containers(runningOut("shop")+oneOff+partialOut("blog")+runningOut("stranger")+runningOut("notes")).
		on(runner.DockerInspect, reply{})
	subscribe(t, h)

	calls := e.cmd.callsOf(runner.DockerInspect)
	if len(calls) == 0 {
		t.Fatal("no docker.inspect call")
	}
	want := []string{containerID("shop", "app"), containerID("shop", "db"), containerID("blog", "app"), containerID("blog", "db")}
	slices.Sort(want)
	for _, c := range calls {
		got := strings.Split(c.Values["ids"], " ")
		slices.Sort(got)
		if c.Stack != runner.StackDocker || len(c.Values) != 1 || !slices.Equal(got, want) {
			t.Fatalf("inspect call = %s %v, want the ids of shop and blog only %v", c.Stack, got, want)
		}
	}
}

func TestTickSkipsInspectWithNoContainers(t *testing.T) {
	for name, out := range map[string]string{"none at all": "", "only untracked": runningOut("stranger")} {
		t.Run(name, func(t *testing.T) {
			e, h := newHubEnv(t, time.Hour)
			e.cmd.on(runner.DockerPing, reply{}).containers(out).on(runner.DockerInspect, reply{})
			subscribe(t, h)
			h.Check(context.Background())
			if n := e.cmd.count(runner.DockerInspect); n != 0 {
				t.Fatalf("%d inspect calls, want 0", n)
			}
		})
	}
}

func TestTickInspectPartialFailureKeepsGoodLines(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	started := "2026-10-30T08:00:00Z"
	// db vanished between docker ps and docker inspect: app's line, then exit 1.
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")).
		on(runner.DockerInspect, reply{stdout: inspectLine("shop", "app", 3, started),
			err: &runner.ExitError{Code: 1, Stderr: "Error: No such object: " + containerID("shop", "db")}})
	cur, _ := subscribe(t, h)

	v := viewOf(t, cur, "shop")
	app := componentOf(t, v, "app")
	if app.RestartCount != 3 || app.StartedAt == nil || app.StartedAt.Format(time.RFC3339) != started {
		t.Fatalf("app = %+v, want 3 restarts, started %s", app, started)
	}
	if db := componentOf(t, v, "db"); db.StartedAt != nil {
		t.Fatalf("db = %+v, want no start time", db)
	}
	if v.UpSince == nil || v.UpSince.Format(time.RFC3339) != started {
		t.Fatalf("shop up since %v, want %s", v.UpSince, started)
	}

	// A run with no parseable line keeps the last data.
	e.cmd.set(runner.DockerInspect, reply{err: &runner.ExitError{Code: 1, Stderr: "boom"}})
	s := h.Check(context.Background())
	if app := componentOf(t, viewOf(t, s, "shop"), "app"); app.RestartCount != 3 || app.StartedAt == nil {
		t.Fatalf("app after a failed inspect = %+v, want the last data", app)
	}

	// Ids no longer listed are dropped.
	e.cmd.set(runner.DockerContainers, reply{stdout: ""})
	h.Check(context.Background())
	h.mu.Lock()
	n := len(h.insp)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d inspect entries for containers no longer listed, want 0", n)
	}
}

func TestLockChangedKeepsInspectAndMetrics(t *testing.T) {
	setMetrics(t, time.Hour, time.Hour)
	e, h := newHubEnv(t, time.Hour)
	started := "2026-10-30T08:00:00Z"
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")).
		on(runner.DockerInspect, reply{stdout: inspectLine("shop", "app", 3, started) + inspectLine("shop", "db", 1, started)}).
		on(runner.DockerStats, reply{stdout: statsLine("shop", "app", "1.50%", "100MiB") + statsLine("shop", "db", "2.50%", "200MiB")})
	cur, updates := subscribe(t, h)
	if cpuOf(t, cur, "shop") == nil {
		untilUpdate(t, updates, func(s State) bool { return cpuOf(t, s, "shop") != nil })
	}

	check := func(s State, status project.Status) {
		t.Helper()
		v := viewOf(t, s, "shop")
		if v.Status != status || v.CPUPercent == nil || *v.CPUPercent != 4 || v.MemBytes == nil || *v.MemBytes != 300<<20 ||
			componentOf(t, v, "app").RestartCount != 3 || componentOf(t, v, "db").StartedAt == nil {
			t.Fatalf("shop = %s cpu %v mem %v %+v, want %s with its inspect and metrics", v.Status, v.CPUPercent, v.MemBytes, v.Components, status)
		}
	}
	id := e.ids["shop"]
	e.ctl.mu.Lock()
	e.ctl.locks[id] = projectLock{status: project.StatusRestarting}
	e.ctl.gen++
	e.ctl.mu.Unlock()
	h.lockChanged(nil)
	check(nextUpdate(t, updates, time.Second), project.StatusRestarting)

	// The release's re-read runs no inspect: the hub keeps its maps.
	e.ctl.unlock(id)
	h.lockChanged(project.ParseContainers([]byte(runningOut("shop"))))
	check(nextUpdate(t, updates, time.Second), project.StatusRunning)
}

func TestMetricsCadenceAndNoOverlap(t *testing.T) {
	interval := 20 * time.Millisecond
	setMetrics(t, interval, time.Hour)
	e, h := newHubEnv(t, 5*time.Millisecond)
	gate := make(chan struct{})
	line := statsLine("shop", "app", "5.00%", "64MiB")
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")).
		on(runner.DockerStats, reply{stdout: line, gate: gate}, reply{stdout: line})
	_, updates, cancel := h.Subscribe()
	e.cmd.waitRunning(t, runner.DockerStats, 1)

	// The status ticks go on while the stats read is blocked.
	e.cmd.set(runner.DockerContainers, reply{stdout: partialOut("shop")})
	untilUpdate(t, updates, func(s State) bool { return viewOf(t, s, "shop").Status == project.StatusPartial })
	waitTicks(t, e.cmd, 5)

	// The last subscriber leaves and a new one comes: the new loop does not
	// start a second read while the first is still running.
	cancel()
	_, updates, cancel = h.Subscribe()
	t.Cleanup(cancel)
	waitTicks(t, e.cmd, 5)
	time.Sleep(3 * interval)
	if n := e.cmd.count(runner.DockerStats); n != 1 {
		t.Fatalf("%d stats reads while the first one ran, want 1", n)
	}
	close(gate)

	// The cut read counts as failed; the new loop's next read fills the values.
	s := untilUpdate(t, updates, func(s State) bool { return cpuOf(t, s, "shop") != nil })
	if cpu := cpuOf(t, s, "shop"); *cpu != 5 {
		t.Fatalf("shop cpu = %v, want 5", *cpu)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.cmd.count(runner.DockerStats) < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("%d stats reads, want 4", e.cmd.count(runner.DockerStats))
		}
		time.Sleep(time.Millisecond)
	}
	if p := e.cmd.maxInFlightOf(runner.DockerStats); p != 1 {
		t.Fatalf("%d stats reads in flight at once, want 1", p)
	}
	// The fake stamps a call a moment after the hub notes its start, so allow
	// a little slack below the interval.
	slack := interval / 10
	calls := e.cmd.callsOf(runner.DockerStats)
	for i := 1; i < len(calls); i++ {
		if gap := calls[i].At.Sub(calls[i-1].At); gap < interval-slack {
			t.Fatalf("stats reads %d and %d began %s apart, want at least %s", i-1, i, gap, interval)
		}
	}
}

// Reads begin metricsInterval apart: the wait after a read is what is left
// of the interval, not a whole interval after the read ends.
func TestMetricsStartsIntervalApartWhenTheReadTakesTime(t *testing.T) {
	interval, read := 200*time.Millisecond, 150*time.Millisecond
	setMetrics(t, interval, time.Hour)
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")).
		on(runner.DockerStats, reply{stdout: statsLine("shop", "app", "1.00%", "1MiB"), delay: read})
	subscribe(t, h)
	deadline := time.Now().Add(5 * time.Second)
	for e.cmd.count(runner.DockerStats) < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("%d stats reads, want 4", e.cmd.count(runner.DockerStats))
		}
		time.Sleep(time.Millisecond)
	}
	calls := e.cmd.callsOf(runner.DockerStats)
	slack := interval / 20 // the fake stamps a call a moment after the hub notes its start
	for i := 1; i < len(calls); i++ {
		// A whole interval after the read ends would be interval+read apart.
		if gap := calls[i].At.Sub(calls[i-1].At); gap < interval-slack || gap >= interval+read*2/3 {
			t.Fatalf("stats reads %d and %d began %s apart, want %s", i-1, i, gap, interval)
		}
	}
}

// While the hub knows Docker is down it runs no docker.stats, but the max
// age still clears the last values; reads come back with Docker.
func TestNoMetricsWhileDockerDown(t *testing.T) {
	maxAge := 150 * time.Millisecond
	setMetrics(t, 5*time.Millisecond, maxAge)
	e, h := newHubEnv(t, 5*time.Millisecond)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")).
		on(runner.DockerStats, reply{stdout: statsLine("shop", "app", "1.00%", "1MiB")})
	cur, updates := subscribe(t, h)
	if cpuOf(t, cur, "shop") == nil {
		untilUpdate(t, updates, func(s State) bool { return cpuOf(t, s, "shop") != nil })
	}

	e.cmd.set(runner.DockerPing, reply{err: errors.New("Cannot connect to the Docker daemon")})
	waitTicks(t, e.cmd, 2)
	// A read that checked Docker just before the down tick may still finish.
	time.Sleep(20 * time.Millisecond)
	e.cmd.waitRunning(t, runner.DockerStats, 0)
	n := e.cmd.count(runner.DockerStats)
	time.Sleep(maxAge + 50*time.Millisecond)
	if got := e.cmd.count(runner.DockerStats); got != n {
		t.Fatalf("%d stats reads while Docker was down, want 0", got-n)
	}
	h.mu.Lock()
	use := h.use
	h.mu.Unlock()
	if use != nil {
		t.Fatalf("metrics = %v after the max age with Docker down, want cleared", use)
	}

	e.cmd.set(runner.DockerPing, reply{})
	untilUpdate(t, updates, func(s State) bool { return cpuOf(t, s, "shop") != nil })
	if got := e.cmd.count(runner.DockerStats); got <= n {
		t.Fatal("no stats read once Docker was up again")
	}
}

func TestMetricsClearedAfterMaxAge(t *testing.T) {
	maxAge := 200 * time.Millisecond
	setMetrics(t, 10*time.Millisecond, maxAge)
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")).
		on(runner.DockerStats, reply{stdout: statsLine("shop", "app", "1.00%", "1MiB")}, reply{err: errors.New("stats failed")})
	cur, updates := subscribe(t, h)
	if cpuOf(t, cur, "shop") == nil {
		untilUpdate(t, updates, func(s State) bool { return cpuOf(t, s, "shop") != nil })
	}
	seen := time.Now()
	s := untilUpdate(t, updates, func(s State) bool { return cpuOf(t, s, "shop") == nil })
	if d := time.Since(seen); d < maxAge*3/4 {
		t.Fatalf("metrics cleared %s after the last good read, want after about %s", d, maxAge)
	}
	v := viewOf(t, s, "shop")
	if len(v.Components) != 2 || v.MemBytes != nil || componentOf(t, v, "app").CPUPercent != nil {
		t.Fatalf("shop after the clear = %+v", v)
	}
}

func TestNoMetricsWithoutSubscriber(t *testing.T) {
	setMetrics(t, 5*time.Millisecond, time.Hour)
	e, h := newHubEnv(t, 5*time.Millisecond)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")).
		on(runner.DockerStats, reply{stdout: statsLine("shop", "app", "1.00%", "1MiB")})
	h.Check(context.Background())
	h.Refresh()
	time.Sleep(50 * time.Millisecond)
	if n := e.cmd.count(runner.DockerStats); n != 0 {
		t.Fatalf("%d stats reads with no subscriber, want 0", n)
	}

	_, _, cancel := h.Subscribe()
	deadline := time.Now().Add(5 * time.Second)
	for e.cmd.count(runner.DockerStats) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no stats read with a subscriber")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	waitLoops(t, h)
	n := e.cmd.count(runner.DockerStats)
	time.Sleep(50 * time.Millisecond)
	if got := e.cmd.count(runner.DockerStats); got != n {
		t.Fatalf("%d stats reads after the last subscriber left, want 0", got-n)
	}
}

// After a successful WithLocks write the hub's cached list still holds the
// rows the write dropped; a metrics merge must not publish them with their
// actions before the caller's Refresh.
func TestMetricsDoNotRepublishAfterWithLocks(t *testing.T) {
	setMetrics(t, 5*time.Millisecond, time.Hour)
	e, h := newHubEnv(t, time.Hour)
	var rs []reply
	for i := range 2000 { // each read changes the values, so each merge would publish
		rs = append(rs, reply{stdout: statsLine("blog", "app", fmt.Sprintf("%d.00%%", i%7+1), "1MiB")})
	}
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop")+runningOut("blog")).on(runner.DockerStats, rs...)
	cur, updates := subscribe(t, h)
	if cpuOf(t, cur, "blog") == nil {
		untilUpdate(t, updates, func(s State) bool { return cpuOf(t, s, "blog") != nil })
	}

	shop := e.ids["shop"]
	if err := e.ctl.WithLocks([]int64{shop}, func() error {
		_, err := e.st.UntrackProject(context.Background(), shop)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reads := e.cmd.count(runner.DockerStats)
	end := time.After(100 * time.Millisecond)
	for done := false; !done; {
		select {
		case s := <-updates:
			for _, v := range s.Projects {
				if v.ID == shop && len(v.Actions) > 0 {
					t.Fatalf("published the untracked shop with actions %v", v.Actions)
				}
			}
		case <-end:
			done = true
		}
	}
	if n := e.cmd.count(runner.DockerStats) - reads; n < 3 {
		t.Fatalf("%d stats reads during the check, want at least 3", n)
	}
	h.Refresh()
	untilUpdate(t, updates, func(s State) bool {
		return !slices.ContainsFunc(s.Projects, func(v ProjectView) bool { return v.ID == shop })
	})
}

func TestHubContainers(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	cs, ok := h.Containers(context.Background())
	if !ok || len(cs) != 2 {
		t.Fatalf("Containers = %v %v, want shop's two containers", cs, ok)
	}
	e.cmd.set(runner.DockerPing, reply{err: errors.New("Cannot connect to the Docker daemon")})
	if cs, ok := h.Containers(context.Background()); ok {
		t.Fatalf("Containers with Docker down = %v, ok", cs)
	}
	e.cmd.set(runner.DockerPing, reply{})
	e.cmd.set(runner.DockerContainers, reply{err: errors.New("boom")})
	if cs, ok := h.Containers(context.Background()); ok {
		t.Fatalf("Containers after a failed read = %v, ok", cs)
	}
}

func TestViewsComponentsAndTotals(t *testing.T) {
	ts := []store.Tracked{
		{ID: 1, DisplayName: "shop", ComposeProject: "shop", Services: []string{"app", "db"}},
		{ID: 2, DisplayName: "blog", ComposeProject: "blog", Services: []string{"app", "db"}},
		{ID: 3, DisplayName: "over", ComposeProject: "over", Services: []string{"app", "db"}},
	}
	cs := project.ParseContainers([]byte(runningOut("shop") + partialOut("blog") + errorOut("over")))
	early := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	insp := map[string]project.Inspect{
		containerID("shop", "app"): {RestartCount: 2, StartedAt: late},
		containerID("shop", "db"):  {StartedAt: early},
		containerID("blog", "app"): {StartedAt: late},
		containerID("over", "db"):  {StartedAt: early},
	}
	use := map[string]project.Usage{
		containerID("shop", "app"): {CPUPercent: 1.5, MemBytes: 100},
		containerID("shop", "db"):  {CPUPercent: 2, MemBytes: 50},
		containerID("blog", "db"):  {CPUPercent: 9, MemBytes: 9}, // exited: not counted
		containerID("over", "db"):  {CPUPercent: 3, MemBytes: 30},
	}
	vs := Views(ts, cs, nil, true, nil, insp, use)

	shop := vs[0]
	if len(shop.Components) != 2 || shop.CPUPercent == nil || *shop.CPUPercent != 3.5 || shop.MemBytes == nil || *shop.MemBytes != 150 ||
		shop.UpSince == nil || !shop.UpSince.Equal(early) || componentOf(t, shop, "app").RestartCount != 2 {
		t.Fatalf("shop = cpu %v mem %v up %v %+v", shop.CPUPercent, shop.MemBytes, shop.UpSince, shop.Components)
	}
	blog := vs[1]
	if blog.Status != project.StatusPartial || blog.CPUPercent != nil || blog.MemBytes != nil || blog.UpSince == nil || !blog.UpSince.Equal(late) {
		t.Fatalf("blog = %s cpu %v mem %v up %v, want Partial, no metrics, up since %s", blog.Status, blog.CPUPercent, blog.MemBytes, blog.UpSince, late)
	}
	over := vs[2]
	if over.Status != project.StatusError || len(over.Components) != 2 || over.CPUPercent == nil || *over.CPUPercent != 3 || over.UpSince != nil {
		t.Fatalf("over = %s cpu %v up %v, want Error, cpu 3, no up since", over.Status, over.CPUPercent, over.UpSince)
	}

	// Without the maps (GET /api/projects) the docker ps part is still there.
	vs = Views(ts, cs, nil, true, nil, nil, nil)
	if v := vs[0]; len(v.Components) != 2 || v.CPUPercent != nil || v.MemBytes != nil || v.UpSince != nil {
		t.Fatalf("shop without maps = cpu %v mem %v up %v %+v", v.CPUPercent, v.MemBytes, v.UpSince, v.Components)
	}
}

func TestViewsDockerDownNoComponents(t *testing.T) {
	ts := []store.Tracked{{ID: 1, DisplayName: "shop", ComposeProject: "shop", Services: []string{"app", "db"}}}
	cs := project.ParseContainers([]byte(runningOut("shop")))
	insp := map[string]project.Inspect{containerID("shop", "app"): {StartedAt: time.Now()}}
	use := map[string]project.Usage{containerID("shop", "app"): {CPUPercent: 1, MemBytes: 1}}
	for name, tc := range map[string]struct {
		readErr  error
		dockerUp bool
	}{"Docker down": {errors.New("down"), false}, "read failed": {errors.New("boom"), true}} {
		t.Run(name, func(t *testing.T) {
			v := Views(ts, cs, tc.readErr, tc.dockerUp, nil, insp, use)[0]
			if v.Components == nil || len(v.Components) != 0 || v.CPUPercent != nil || v.MemBytes != nil || v.UpSince != nil {
				t.Fatalf("shop = %+v, want no components and no metrics", v)
			}
		})
	}
}
