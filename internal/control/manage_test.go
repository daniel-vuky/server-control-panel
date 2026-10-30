package control

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// markMissing marks the project named name missing, as a rescan that could
// not find its folder does.
func (e *env) markMissing(t *testing.T, name string) {
	t.Helper()
	ctx := context.Background()
	tr, err := e.st.TrackedProject(ctx, e.ids[name])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := e.st.ApplyRescan(ctx, store.RescanApply{Workspaces: []store.WorkspaceApply{{
		ID: tr.WorkspaceID, LastScanAt: now, Missing: []string{tr.Path},
	}}}, now); err != nil {
		t.Fatal(err)
	}
}

// hold marks project id held for a store write, as WithLocks does.
func (e *env) hold(id int64) {
	e.ctl.mu.Lock()
	e.ctl.locks[id] = projectLock{held: true}
	e.ctl.gen++
	e.ctl.mu.Unlock()
}

// noCompose fails when a lifecycle command ran.
func noCompose(t *testing.T, cmd *fakeCmd) {
	t.Helper()
	for _, a := range []runner.Action{
		runner.ComposeUp, runner.ComposeUpOverride, runner.ComposeStop, runner.ComposeStopOverride,
		runner.ComposeRestart, runner.ComposeRestartOverride,
	} {
		if n := cmd.count(a); n != 0 {
			t.Fatalf("%s ran %d times", a, n)
		}
	}
}

func TestViewsMissingIsNotFound(t *testing.T) {
	since := time.Now()
	ts := []store.Tracked{
		{ID: 1, DisplayName: "shop", ComposeProject: "shop", Services: []string{"app", "db"}, MissingSince: &since},
		{ID: 2, DisplayName: "blog", ComposeProject: "blog", Services: []string{"app", "db"}, MissingSince: &since},
		{ID: 3, DisplayName: "notes", ComposeProject: "notes", Services: []string{"app", "db"}},
	}
	cs := project.ParseContainers([]byte(runningOut("shop") + runningOut("blog") + runningOut("notes")))
	lock := func(id int64) (project.Status, bool, bool) {
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
		want     []want
	}{
		{"Docker up", nil, true, []want{
			{project.StatusNotFound, []project.Action{project.ActionRemove}},
			{project.StatusStopping, []project.Action{}}, // in flight beats missing
			{project.StatusRunning, []project.Action{project.ActionStop, project.ActionRestart, project.ActionUntrack}},
		}},
		{"Docker down", errors.New("down"), false, []want{
			{project.StatusNotFound, []project.Action{project.ActionRemove}}, // missing beats Docker
			{project.StatusStopping, []project.Action{}},
			{project.StatusUnknown, []project.Action{project.ActionUntrack}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := Views(ts, cs, tt.readErr, tt.dockerUp, lock)
			for i, v := range vs {
				if v.Status != tt.want[i].status || !slices.Equal(v.Actions, tt.want[i].actions) || v.Actions == nil {
					t.Errorf("view %s = %s %v, want %s %v", v.DisplayName, v.Status, v.Actions, tt.want[i].status, tt.want[i].actions)
				}
			}
		})
	}
}

func TestViewsHeldHasNoActions(t *testing.T) {
	since := time.Now()
	ts := []store.Tracked{
		{ID: 1, DisplayName: "shop", ComposeProject: "shop", Services: []string{"app", "db"}},
		{ID: 2, DisplayName: "blog", ComposeProject: "blog", Services: []string{"app", "db"}, MissingSince: &since},
		{ID: 3, DisplayName: "notes", ComposeProject: "notes", Services: []string{"app", "db"}},
	}
	cs := project.ParseContainers([]byte(runningOut("shop") + runningOut("notes")))
	lock := func(id int64) (project.Status, bool, bool) {
		if id == 1 || id == 2 {
			return "", true, true
		}
		return "", false, false
	}
	vs := Views(ts, cs, nil, true, lock)
	want := []struct {
		status  project.Status
		actions []project.Action
	}{
		{project.StatusRunning, []project.Action{}},
		{project.StatusNotFound, []project.Action{}},
		{project.StatusRunning, []project.Action{project.ActionStop, project.ActionRestart, project.ActionUntrack}},
	}
	for i, v := range vs {
		if v.Status != want[i].status || !slices.Equal(v.Actions, want[i].actions) || v.Actions == nil {
			t.Errorf("view %s = %s %v, want %s %v", v.DisplayName, v.Status, v.Actions, want[i].status, want[i].actions)
		}
	}
}

func TestLockAndInFlight(t *testing.T) {
	e := newEnv(t)
	shop, blog := e.ids["shop"], e.ids["blog"]
	e.hold(shop)
	e.ctl.mu.Lock()
	e.ctl.locks[blog] = projectLock{status: project.StatusStarting}
	e.ctl.mu.Unlock()

	if s, held, ok := e.ctl.Lock(shop); !ok || !held || s != "" {
		t.Fatalf("Lock(shop) = %q %v %v, want held", s, held, ok)
	}
	if _, in := e.ctl.InFlight(shop); in {
		t.Fatal("a held project is in flight")
	}
	if s, held, ok := e.ctl.Lock(blog); !ok || held || s != project.StatusStarting {
		t.Fatalf("Lock(blog) = %q %v %v, want starting", s, held, ok)
	}
	if s, in := e.ctl.InFlight(blog); !in || s != project.StatusStarting {
		t.Fatalf("InFlight(blog) = %q %v", s, in)
	}
	if _, _, ok := e.ctl.Lock(e.ids["over"]); ok {
		t.Fatal("over is locked")
	}
}

func TestDoRefusesNotFound(t *testing.T) {
	e := newEnv(t)
	e.markMissing(t, "shop")
	e.cmd.containers(stoppedOut("shop"))
	for _, a := range []project.Action{project.ActionStart, project.ActionStop, project.ActionRestart} {
		_, err := e.ctl.Do(context.Background(), e.ids["shop"], a, actor)
		var ref *Refusal
		verb := map[project.Action]string{project.ActionStart: "Start", project.ActionStop: "Stop", project.ActionRestart: "Restart"}[a]
		msg := verb + " is not allowed while shop is Not found."
		if !errors.As(err, &ref) || ref.Code != CodeNotAllowed || ref.Status != project.StatusNotFound || ref.Message != msg {
			t.Fatalf("Do(%s) error = %#v, want not_allowed %q", a, err, msg)
		}
		wantRow(t, e, "shop", "project."+string(a), store.ResultRefused, msg)
	}
	noCompose(t, e.cmd)
}

func TestDoRefusesHeld(t *testing.T) {
	e := newEnv(t)
	e.cmd.containers(stoppedOut("shop"))
	e.hold(e.ids["shop"])
	_, err := e.ctl.Do(context.Background(), e.ids["shop"], project.ActionStart, actor)
	var ref *Refusal
	msg := "shop is being removed from the panel."
	if !errors.As(err, &ref) || ref.Code != CodeLocked || !ref.Held || ref.Message != msg ||
		ref.Status != project.StatusStopped || ref.ProjectID != e.ids["shop"] {
		t.Fatalf("Do error = %#v, want locked and held", err)
	}
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultRefused, msg)
	noCompose(t, e.cmd)
}

// Stop tracking lands while Start is between its first read and its lock
// (review focus 3): no command runs on the untracked project.
func TestDoAfterUntrackRunsNothing(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.cmd.on(runner.DockerContainers, reply{stdout: stoppedOut("shop"), gate: gate}).on(runner.ComposeUp, reply{})
	errc := make(chan error, 1)
	go func() {
		_, err := e.ctl.Do(context.Background(), e.ids["shop"], project.ActionStart, actor)
		errc <- err
	}()
	e.cmd.waitRunning(t, runner.DockerContainers, 1)
	if _, err := e.ctl.Untrack(context.Background(), e.ids["shop"], actor); err != nil {
		t.Fatalf("Untrack: %v", err)
	}
	gen := e.ctl.Generation()
	close(gate)
	if err := <-errc; !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Do error = %v, want ErrNotFound", err)
	}
	noCompose(t, e.cmd)
	if _, _, ok := e.ctl.Lock(e.ids["shop"]); ok {
		t.Fatal("shop is still locked")
	}
	if g := e.ctl.Generation(); g != gen+2 {
		t.Fatalf("generation = %d, want %d (taken and released)", g, gen+2)
	}
	wantRow(t, e, "shop", store.ActProjectUntrack, store.ResultOK, "")
}

// A rescan marks the project missing while Start is between its first read
// and its lock: Start is refused and runs nothing.
func TestDoAfterMissingRunsNothing(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.cmd.on(runner.DockerContainers, reply{stdout: stoppedOut("shop"), gate: gate}).on(runner.ComposeUp, reply{})
	errc := make(chan error, 1)
	go func() {
		_, err := e.ctl.Do(context.Background(), e.ids["shop"], project.ActionStart, actor)
		errc <- err
	}()
	e.cmd.waitRunning(t, runner.DockerContainers, 1)
	e.markMissing(t, "shop")
	close(gate)
	err := <-errc
	var ref *Refusal
	msg := "Start is not allowed while shop is Not found."
	if !errors.As(err, &ref) || ref.Code != CodeNotAllowed || ref.Status != project.StatusNotFound || ref.Message != msg {
		t.Fatalf("Do error = %#v, want not_allowed", err)
	}
	wantRow(t, e, "shop", store.ActProjectStart, store.ResultRefused, msg)
	noCompose(t, e.cmd)
	if _, _, ok := e.ctl.Lock(e.ids["shop"]); ok {
		t.Fatal("shop is still locked")
	}
}

func TestUntrackIgnores(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	got, err := e.ctl.Untrack(ctx, e.ids["shop"], actor)
	if err != nil {
		t.Fatalf("Untrack: %v", err)
	}
	if got.ID != e.ids["shop"] || got.DisplayName != "shop" || got.MissingSince != nil {
		t.Fatalf("Untrack = %+v", got)
	}
	if _, err := e.st.TrackedProject(ctx, e.ids["shop"]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("TrackedProject after untrack = %v, want ErrNotFound", err)
	}
	known, err := e.st.KnownProjects(ctx)
	if err != nil || known[e.dirs["shop"]].State != "ignored" {
		t.Fatalf("known = %+v, %v; want shop ignored", known[e.dirs["shop"]], err)
	}
	if _, _, ok := e.ctl.Lock(e.ids["shop"]); ok {
		t.Fatal("shop is still locked")
	}
	e.cmd.mu.Lock()
	calls := len(e.cmd.calls)
	e.cmd.mu.Unlock()
	if calls != 0 {
		t.Fatalf("%d Docker calls, want none", calls)
	}
}

func TestUntrackRemovesMissing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.markMissing(t, "shop")
	got, err := e.ctl.Untrack(ctx, e.ids["shop"], actor)
	if err != nil {
		t.Fatalf("Untrack: %v", err)
	}
	if got.MissingSince == nil {
		t.Fatalf("Untrack = %+v, want the missing project", got)
	}
	known, err := e.st.KnownProjects(ctx)
	if _, ok := known[e.dirs["shop"]]; err != nil || ok {
		t.Fatalf("shop still known (%v)", err)
	}
	wantRow(t, e, "shop", store.ActProjectRemove, store.ResultOK, "")
}

func TestUntrackRefusedWhileLocked(t *testing.T) {
	t.Run("in flight", func(t *testing.T) {
		e := newEnv(t)
		gate := make(chan struct{})
		e.cmd.containers(stoppedOut("shop"), runningOut("shop")).on(runner.ComposeUp, reply{gate: gate})
		done := make(chan Result, 1)
		go func() {
			res, _ := e.ctl.Do(context.Background(), e.ids["shop"], project.ActionStart, actor)
			done <- res
		}()
		e.cmd.waitRunning(t, runner.ComposeUp, 1)

		_, err := e.ctl.Untrack(context.Background(), e.ids["shop"], actor)
		var ref *Refusal
		msg := "shop is Starting…; wait for it to finish."
		if !errors.As(err, &ref) || ref.Code != CodeLocked || ref.Status != project.StatusStarting ||
			ref.Held || ref.ProjectID != e.ids["shop"] || ref.Message != msg {
			t.Fatalf("Untrack error = %#v, want locked", err)
		}
		wantRow(t, e, "shop", store.ActProjectUntrack, store.ResultRefused, msg)
		if _, err := e.st.TrackedProject(context.Background(), e.ids["shop"]); err != nil {
			t.Fatalf("shop is no longer tracked: %v", err)
		}
		close(gate)
		wantResult(t, <-done, OutcomeOK, project.StatusRunning, "")
	})
	t.Run("held", func(t *testing.T) {
		e := newEnv(t)
		e.markMissing(t, "shop")
		e.hold(e.ids["shop"])
		_, err := e.ctl.Untrack(context.Background(), e.ids["shop"], actor)
		var ref *Refusal
		msg := "shop is being removed from the panel."
		if !errors.As(err, &ref) || ref.Code != CodeLocked || !ref.Held || ref.ProjectID != e.ids["shop"] || ref.Message != msg {
			t.Fatalf("Untrack error = %#v, want locked and held", err)
		}
		wantRow(t, e, "shop", store.ActProjectRemove, store.ResultRefused, msg)
	})
}

func TestUntrackActivity(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.ctl.Untrack(ctx, e.ids["blog"], "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	r := e.lastRow(t)
	if r.Actor != "owner@example.com" || r.ActorKind != store.ActorPerson || r.Action != store.ActProjectUntrack ||
		r.ProjectID != e.ids["blog"] || r.Target != "blog" || r.Result != store.ResultOK || r.Reason != "" || r.FinishedAt == nil {
		t.Fatalf("activity row = %+v", r)
	}

	// Unknown, ignored now, and zero ids: ErrNotFound and no row.
	for _, id := range []int64{999, e.ids["blog"], 0} {
		if _, err := e.ctl.Untrack(ctx, id, actor); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Untrack(%d) = %v, want ErrNotFound", id, err)
		}
	}
	if got := e.lastRow(t); got.ID != r.ID {
		t.Fatalf("a not-found untrack was recorded: %+v", got)
	}
}

func TestWithLocksHoldsAndReleases(t *testing.T) {
	e := newEnv(t)
	shop, blog := e.ids["shop"], e.ids["blog"]
	obs := &fakeObserver{ctl: e.ctl, id: shop}
	e.ctl.setObserver(obs)
	gen := e.ctl.Generation()
	boom := errors.New("boom")
	err := e.ctl.WithLocks([]int64{shop, blog}, func() error {
		for _, id := range []int64{shop, blog} {
			if _, held, ok := e.ctl.Lock(id); !ok || !held {
				t.Errorf("project %d is not held inside fn", id)
			}
		}
		if g := e.ctl.Generation(); g != gen+1 {
			t.Errorf("generation inside fn = %d, want %d", g, gen+1)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithLocks = %v, want fn's error", err)
	}
	for _, id := range []int64{shop, blog} {
		if _, _, ok := e.ctl.Lock(id); ok {
			t.Fatalf("project %d still locked", id)
		}
	}
	if g := e.ctl.Generation(); g != gen+2 {
		t.Fatalf("generation = %d, want %d", g, gen+2)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.changes) != 2 || obs.changes[0].fresh != nil || obs.changes[1].fresh != nil ||
		obs.changes[0].gen != gen+1 || obs.changes[1].gen != gen+2 {
		t.Fatalf("lockChanged calls = %+v, want take then release", obs.changes)
	}
}

// A successful write does not tell the observer of the release: its cached
// list still holds the changed rows. The generation still moves.
func TestWithLocksSuccessSkipsReleaseNotify(t *testing.T) {
	e := newEnv(t)
	shop := e.ids["shop"]
	obs := &fakeObserver{ctl: e.ctl, id: shop}
	e.ctl.setObserver(obs)
	gen := e.ctl.Generation()
	if err := e.ctl.WithLocks([]int64{shop}, func() error { return nil }); err != nil {
		t.Fatalf("WithLocks = %v", err)
	}
	if _, _, ok := e.ctl.Lock(shop); ok {
		t.Fatal("shop still locked")
	}
	if g := e.ctl.Generation(); g != gen+2 {
		t.Fatalf("generation = %d, want %d", g, gen+2)
	}
	if _, err := e.ctl.Untrack(context.Background(), e.ids["blog"], actor); err != nil {
		t.Fatalf("Untrack: %v", err)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.changes) != 2 || obs.changes[0].gen != gen+1 || obs.changes[1].gen != gen+3 {
		t.Fatalf("lockChanged calls = %+v, want only the two takes", obs.changes)
	}
}

func TestWithLocksReleasesOnPanic(t *testing.T) {
	e := newEnv(t)
	shop, blog := e.ids["shop"], e.ids["blog"]
	obs := &fakeObserver{ctl: e.ctl, id: shop}
	e.ctl.setObserver(obs)
	gen := e.ctl.Generation()
	func() {
		defer func() {
			if r := recover(); r != "fn" {
				t.Fatalf("recovered %v, want the fn panic", r)
			}
		}()
		_ = e.ctl.WithLocks([]int64{shop, blog}, func() error { panic("fn") })
	}()
	for _, id := range []int64{shop, blog} {
		if _, _, ok := e.ctl.Lock(id); ok {
			t.Fatalf("project %d still locked after a panic", id)
		}
	}
	if g := e.ctl.Generation(); g != gen+2 {
		t.Fatalf("generation = %d, want %d", g, gen+2)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.changes) != 2 {
		t.Fatalf("lockChanged calls = %+v, want 2", obs.changes)
	}
}

func TestWithLocksRefusesWhenOneInFlight(t *testing.T) {
	cases := []struct {
		name   string
		lock   projectLock
		status project.Status
	}{
		{"in flight", projectLock{status: project.StatusStopping}, project.StatusStopping},
		{"held", projectLock{held: true}, project.StatusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			shop, blog := e.ids["shop"], e.ids["blog"]
			obs := &fakeObserver{ctl: e.ctl, id: shop}
			e.ctl.setObserver(obs)
			e.ctl.mu.Lock()
			e.ctl.locks[blog] = tc.lock
			e.ctl.mu.Unlock()
			gen := e.ctl.Generation()
			ran := false
			err := e.ctl.WithLocks([]int64{shop, blog}, func() error { ran = true; return nil })
			var ref *Refusal
			if !errors.As(err, &ref) || ref.Code != CodeLocked || ref.Status != tc.status ||
				ref.ProjectID != blog || ref.Held != tc.lock.held || ref.Message == "" {
				t.Fatalf("WithLocks = %#v, want locked on blog", err)
			}
			if ran {
				t.Fatal("fn ran")
			}
			if _, _, ok := e.ctl.Lock(shop); ok {
				t.Fatal("shop was left locked")
			}
			if e.ctl.Generation() != gen {
				t.Fatal("a refusal moved the generation")
			}
			obs.mu.Lock()
			defer obs.mu.Unlock()
			if len(obs.changes) != 0 {
				t.Fatalf("lockChanged calls = %+v, want none", obs.changes)
			}
		})
	}
}

// A tick that read the store before an untrack must not publish the removed
// project: Refresh during it makes one more tick run.
func TestHubRefreshRepublishesWithoutStaleTs(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	gate := make(chan struct{})
	e.cmd.on(runner.DockerPing, reply{}).
		containers(runningOut("shop")).                                             // the subscribe tick
		on(runner.DockerContainers, reply{stdout: stoppedOut("shop"), gate: gate}). // the stale tick
		containers(stoppedOut("shop"))                                              // the tick after
	cur, updates := subscribe(t, h)
	if viewOf(t, cur, "shop").Status != project.StatusRunning {
		t.Fatalf("state = %+v", cur)
	}

	checked := make(chan State, 1)
	go func() { checked <- h.Check(context.Background()) }()
	e.cmd.waitRunning(t, runner.DockerContainers, 1)
	if _, err := e.st.UntrackProject(context.Background(), e.ids["shop"]); err != nil {
		t.Fatal(err)
	}
	pings := e.cmd.count(runner.DockerPing)
	h.Refresh()
	if n := e.cmd.count(runner.DockerPing); n != pings {
		t.Fatal("Refresh started a tick while one was running")
	}
	close(gate)

	s := <-checked
	if len(s.Projects) != 4 || slices.ContainsFunc(s.Projects, func(v ProjectView) bool { return v.DisplayName == "shop" }) {
		t.Fatalf("Check = %+v, want 4 projects without shop", s)
	}
	u := nextUpdate(t, updates, time.Second)
	if slices.ContainsFunc(u.Projects, func(v ProjectView) bool { return v.DisplayName == "shop" }) {
		t.Fatalf("published %+v with the removed shop", u)
	}
	noUpdate(t, updates)
	if n := e.cmd.count(runner.DockerPing); n != pings+1 {
		t.Fatalf("%d pings after Refresh, want 1 (the extra tick)", n-pings)
	}
	if p := e.cmd.maxInFlight(); p != 1 {
		t.Fatalf("%d Docker calls in flight at once, want 1", p)
	}
}

// A successful Untrack never republishes the removed project with actions:
// the take shows it held (no actions), and the Refresh tick drops it.
func TestHubUntrackThenRefresh(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	_, updates := subscribe(t, h)

	if _, err := e.ctl.Untrack(context.Background(), e.ids["shop"], actor); err != nil {
		t.Fatalf("Untrack: %v", err)
	}
	u := nextUpdate(t, updates, time.Second)
	if v := viewOf(t, u, "shop"); v.Status != project.StatusRunning || len(v.Actions) != 0 {
		t.Fatalf("shop after the take = %s %v, want running with no actions", v.Status, v.Actions)
	}
	noUpdate(t, updates) // the release published nothing

	h.Refresh()
	u = nextUpdate(t, updates, time.Second)
	if len(u.Projects) != 4 || slices.ContainsFunc(u.Projects, func(v ProjectView) bool { return v.DisplayName == "shop" }) {
		t.Fatalf("update after Refresh = %+v, want 4 projects without shop", u)
	}
}

func TestHubRefreshStartsATick(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	_, updates := subscribe(t, h)
	if _, err := e.st.UntrackProject(context.Background(), e.ids["shop"]); err != nil {
		t.Fatal(err)
	}
	h.Refresh() // returns at once; the tick runs on its own
	u := nextUpdate(t, updates, time.Second)
	if len(u.Projects) != 4 || slices.ContainsFunc(u.Projects, func(v ProjectView) bool { return v.DisplayName == "shop" }) {
		t.Fatalf("update = %+v, want 4 projects without shop", u)
	}
}

func TestHubRefreshNoLoopInvalidates(t *testing.T) {
	e, h := newHubEnv(t, time.Hour)
	e.cmd.on(runner.DockerPing, reply{}).containers(runningOut("shop"))
	_, _, cancel := h.Subscribe()
	cancel()
	waitLoops(t, h)

	if _, err := e.st.UntrackProject(context.Background(), e.ids["shop"]); err != nil {
		t.Fatal(err)
	}
	pings := e.cmd.count(runner.DockerPing)
	h.Refresh()
	time.Sleep(50 * time.Millisecond)
	if n := e.cmd.count(runner.DockerPing); n != pings {
		t.Fatalf("Refresh without a subscriber read Docker %d times", n-pings)
	}
	h.mu.Lock()
	ready := h.ready
	h.mu.Unlock()
	if ready {
		t.Fatal("the cache is still ready after Refresh")
	}

	// The next subscriber reads fresh instead of reusing the cache.
	cur, _ := subscribe(t, h)
	if n := e.cmd.count(runner.DockerPing); n != pings+1 {
		t.Fatalf("subscribe ran %d ticks, want 1", n-pings)
	}
	if len(cur.Projects) != 4 {
		t.Fatalf("state = %+v, want 4 projects without shop", cur)
	}
}
