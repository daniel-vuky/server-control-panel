package control

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	inFlight := func(id int64) (project.Status, bool) {
		if id == 2 {
			return project.StatusStopping, true
		}
		return "", false
	}
	type want struct {
		status  project.Status
		actions []project.Action
	}
	tests := []struct {
		name     string
		readErr  error
		dockerUp bool
		inFlight func(int64) (project.Status, bool)
		want     []want
	}{
		{"in-flight wins, self has no actions", nil, true, inFlight, []want{
			{project.StatusRunning, []project.Action{project.ActionStop, project.ActionRestart}},
			{project.StatusStopping, []project.Action{}},
			{project.StatusRunning, []project.Action{}},
		}},
		{"a read error gives unknown", errors.New("boom"), true, inFlight, []want{
			{project.StatusUnknown, []project.Action{}},
			{project.StatusStopping, []project.Action{}},
			{project.StatusUnknown, []project.Action{}},
		}},
		{"Docker down gives no actions", errors.New("down"), false, nil, []want{
			{project.StatusUnknown, []project.Action{}},
			{project.StatusUnknown, []project.Action{}},
			{project.StatusUnknown, []project.Action{}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := Views(ts, cs, tt.readErr, tt.dockerUp, tt.inFlight)
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
	if v := viewOf(t, cur, "panel"); len(v.Actions) != 0 {
		t.Errorf("panel actions = %v, want none", v.Actions)
	}
	if v := viewOf(t, cur, "shop"); !slices.Equal(v.Actions, []project.Action{project.ActionStop, project.ActionRestart}) {
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
	e.ctl.locks[id] = project.StatusStopping
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
	if v := viewOf(t, s, "shop"); v.Status != project.StatusStopped || !slices.Equal(v.Actions, []project.Action{project.ActionStart}) {
		t.Fatalf("shop = %s %v, want stopped with start", v.Status, v.Actions)
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

func TestNoDeadlock(t *testing.T) {
	e, h := newHubEnv(t, 20*time.Millisecond)
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
	_, updates := subscribe(t, h)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithCancel(context.Background())
		var checks sync.WaitGroup
		checks.Go(func() {
			for ctx.Err() == nil {
				h.Check(ctx)
			}
		})
		checks.Go(func() {
			for {
				select {
				case <-updates:
				case <-ctx.Done():
					return
				}
			}
		})
		var wg sync.WaitGroup
		actions := []project.Action{project.ActionStart, project.ActionStop, project.ActionRestart}
		for i := range 50 {
			wg.Go(func() {
				_, err := e.ctl.Do(context.Background(), e.ids[names[i%len(names)]], actions[i%len(actions)], actor)
				var r *Refusal
				if err != nil && !errors.As(err, &r) {
					t.Errorf("Do: %v", err)
				}
			})
		}
		wg.Wait()
		cancel()
		checks.Wait()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		buf := make([]byte, 1<<20)
		t.Fatalf("deadlock: not done after 10s\n%s", buf[:runtime.Stack(buf, true)])
	}
	for _, n := range names {
		if _, locked := e.ctl.InFlight(e.ids[n]); locked {
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
		if v.Status != project.StatusUnknown || len(v.Actions) != 0 || v.Actions == nil {
			t.Errorf("%s = %s %v, want unknown with no actions", v.DisplayName, v.Status, v.Actions)
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
