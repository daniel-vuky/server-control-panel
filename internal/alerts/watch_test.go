package alerts

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// fakeSources is the watcher's Sources, changed by the test through set.
type fakeSources struct {
	mu         sync.Mutex
	tracked    []store.Tracked
	trackedErr error
	cs         []project.Container
	csAt       time.Time
	dockerUp   bool
	csOK       bool
	insp       map[string]project.Inspect
	locks      map[int64]project.Status
	tick       metrics.Tick
	tickOK     bool
	disks      []metrics.Disk
	fallback   bool // Disks gave the one "/ (panel data)" row
	tunnel     metrics.TunnelState
	seen       bool
	setSeen    []bool
}

func (f *fakeSources) set(fn func(f *fakeSources)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeSources) seenWrites() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.setSeen)
}

func (f *fakeSources) sources() Sources {
	return Sources{
		Tracked: func(context.Context) ([]store.Tracked, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return slices.Clone(f.tracked), f.trackedErr
		},
		Containers: func(time.Duration) ([]project.Container, time.Time, bool, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return slices.Clone(f.cs), f.csAt, f.dockerUp, f.csOK
		},
		Inspect: func(time.Duration) (map[string]project.Inspect, time.Time, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return maps.Clone(f.insp), f.csAt, f.insp != nil
		},
		Lock: func(id int64) (project.Status, bool, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			s, ok := f.locks[id]
			return s, false, ok
		},
		Latest: func() (metrics.Tick, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.tick, f.tickOK
		},
		Disks: func() ([]metrics.Disk, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return slices.Clone(f.disks), f.fallback
		},
		Tunnel: func(context.Context) metrics.Tunnel {
			f.mu.Lock()
			defer f.mu.Unlock()
			return metrics.Tunnel{State: f.tunnel}
		},
		Settings: func(context.Context) (store.AlertSettings, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return store.AlertSettings{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 5, TunnelSeenConnected: f.seen}, nil
		},
		SetSeen: func(_ context.Context, seen bool) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.setSeen = append(f.setSeen, seen)
			f.seen = seen
			return nil
		},
	}
}

// fakeThresholds is the watcher's thresholds, changed by the test.
type fakeThresholds struct {
	mu  sync.Mutex
	th  metrics.Thresholds
	sus time.Duration
}

func (f *fakeThresholds) Thresholds() metrics.Thresholds {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.th
}

func (f *fakeThresholds) Sustained() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sus
}

func (f *fakeThresholds) setCPU(v int) {
	f.mu.Lock()
	f.th.CPUPercent = v
	f.mu.Unlock()
}

type watchRig struct {
	*notifierRig
	src *fakeSources
	th  *fakeThresholds
	tg  *recSender
	w   *Watcher
	ids map[string]int64 // the seeded projects' ids by display name

	sentinels int // raised by settle
}

// newWatchRig builds a watcher over fake sources, a real notifier on a fresh
// store with one recording Telegram target and a test clock shared by both.
// The projects named are stored (tracked) so their incidents can be stored;
// the fake Tracked source is set by each test. Neither is started.
func newWatchRig(t *testing.T, client *http.Client, projects ...string) *watchRig {
	t.Helper()
	tg := &recSender{}
	frame := Frame{Host: func() string { return "prod-01" }}
	r := newRig(t, frame, Target{Name: ChannelTelegram, Sender: tg})
	wr := &watchRig{
		notifierRig: r,
		src:         &fakeSources{},
		th:          &fakeThresholds{th: metrics.DefaultThresholds, sus: 5 * time.Minute},
		tg:          tg,
		ids:         seedProjects(t, r.st, projects...),
	}
	frame.Now = r.clock.Now
	wr.w = NewWatcher(wr.src.sources(), r.n, wr.th, frame, client, slog.New(slog.NewTextHandler(r.logs, nil)))
	return wr
}

// seedProjects stores a workspace with the named projects, all tracked, and
// returns their ids.
func seedProjects(t *testing.T, st *store.Store, names ...string) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	if len(names) == 0 {
		return ids
	}
	ps := make([]store.NewProject, 0, len(names))
	for _, n := range names {
		ps = append(ps, store.NewProject{Path: "/srv/apps/" + n, ComposeFiles: []string{"compose.yaml"}, ComposeProject: n, DisplayName: n, Tracked: true})
	}
	ctx := context.Background()
	if _, _, err := st.AddWorkspace(ctx, store.NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 1, LastScanAt: time.Now()}, ps); err != nil {
		t.Fatal(err)
	}
	ts, err := st.TrackedProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range ts {
		ids[tr.DisplayName] = tr.ID
	}
	return ids
}

func (wr *watchRig) pass() { wr.w.Pass(context.Background()) }

func (wr *watchRig) isOpen(key string) bool { return slices.Contains(wr.n.OpenKeys(), key) }

// settle waits until everything raised so far has been handled (a sentinel
// queued behind it has its row, and no row is running) and returns the
// delivered messages, the sentinels left out.
func (wr *watchRig) settle(t *testing.T) []Message {
	t.Helper()
	wr.sentinels++
	want := wr.sentinels
	wr.n.Raise(Alert{Action: store.ActAlertBackup, Target: "sentinel", Message: Message{Title: "sentinel"}})
	wr.waitRows(t, "the sentinel", func(rows []store.ActivityRow) bool {
		n := 0
		for _, r := range rows {
			if r.Result == store.ResultRunning {
				return false
			}
			if r.Target == "sentinel" {
				n++
			}
		}
		return n == want
	})
	var out []Message
	for _, m := range wr.tg.delivered() {
		if m.Title != "sentinel" {
			out = append(out, m)
		}
	}
	return out
}

func titles(ms []Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		title := m.Title
		if m.Recovery {
			title = "✅ " + title
		}
		out = append(out, title)
	}
	return out
}

func wantTitles(t *testing.T, ms []Message, want ...string) {
	t.Helper()
	if got := titles(ms); !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

func f64p(v float64) *float64 { return &v }
func i64p(v int64) *int64     { return &v }
func intp(v int) *int         { return &v }

const gib = int64(1) << 30

// serverTick is a tick at at with the server's CPU and its RAM used of 16 GiB.
func serverTick(at time.Time, cpu float64, memUsed int64) metrics.Tick {
	return metrics.Tick{At: at, DockerUp: true, Cores: 4, Projects: map[int64]metrics.ProjectNow{},
		Server: &store.ServerSample{TS: at, CPU: f64p(cpu), MemUsed: i64p(memUsed), MemTotal: i64p(16 * gib)}}
}

// passWithCPU sets a fresh tick with the server CPU at now, then passes.
func (wr *watchRig) passWithCPU(cpu float64) {
	at := wr.clock.Now()
	wr.src.set(func(f *fakeSources) { f.tick, f.tickOK = serverTick(at, cpu, 8*gib), true })
	wr.pass()
}

func TestResourceSustained(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	for i := 0; i <= 10; i++ { // ticks at t, t+30 s … t+5 min
		wr.passWithCPU(95)
		if open, want := wr.isOpen("server:cpu"), i == 10; open != want {
			t.Fatalf("pass %d (t+%ds): open = %v, want %v", i, i*30, open, want)
		}
		wr.clock.Add(30 * time.Second)
	}
	wr.passWithCPU(95) // still over: no second alert
	ms := wr.settle(t)
	wantTitles(t, ms, "CPU at 95 % for 5 min (threshold 90 %)")
	row := wr.rows(t)[0]
	if row.Action != store.ActAlertResource || row.Target != "prod-01" || row.ProjectID != 0 || row.Reason != "CPU at 95 % for 5 min (threshold 90 %)" {
		t.Fatalf("row = %+v", row)
	}
}

func TestResourceSameTickTwiceNotCounted(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	at := wr.clock.Now()
	wr.src.set(func(f *fakeSources) { f.tick, f.tickOK = serverTick(at, 95, 8*gib), true })
	for i := range 10 {
		wr.pass()
		if wr.isOpen("server:cpu") {
			t.Fatalf("pass %d: the same tick raised an alert", i)
		}
		wr.clock.Add(8 * time.Second) // the tick stays fresh (≤ 90 s) throughout
	}
	for range 10 { // and later, once it is stale, still nothing
		wr.clock.Add(30 * time.Second)
		wr.pass()
	}
	if wr.isOpen("server:cpu") {
		t.Fatal("a stale tick raised an alert")
	}
	if ms := wr.settle(t); len(ms) != 0 {
		t.Fatalf("messages = %q, want none", titles(ms))
	}
}

func TestResourceGapKeepsTimer(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	for i := 0; i <= 10; i++ { // t … t+5 min
		switch i {
		case 2, 3: // no tick at all
			wr.src.set(func(f *fakeSources) { f.tickOK = false })
			wr.pass()
		case 5: // a tick whose CPU could not be read
			at := wr.clock.Now()
			wr.src.set(func(f *fakeSources) {
				f.tick, f.tickOK = serverTick(at, 0, 8*gib), true
				f.tick.Server.CPU = nil
			})
			wr.pass()
		default:
			wr.passWithCPU(95)
		}
		if open, want := wr.isOpen("server:cpu"), i == 10; open != want {
			t.Fatalf("pass %d (t+%ds): open = %v, want %v", i, i*30, open, want)
		}
		wr.clock.Add(30 * time.Second)
	}
}

func TestResourceResetAndRecovery(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	for range 4 { // t … t+90 s over
		wr.passWithCPU(95)
		wr.clock.Add(30 * time.Second)
	}
	wr.passWithCPU(85) // t+120 s under: the timer resets
	wr.clock.Add(30 * time.Second)
	for i := 0; i <= 10; i++ { // t+150 s … t+450 s over
		wr.passWithCPU(95)
		if open, want := wr.isOpen("server:cpu"), i == 10; open != want {
			t.Fatalf("after the reset, pass %d: open = %v, want %v", i, open, want)
		}
		wr.clock.Add(30 * time.Second)
	}
	wr.passWithCPU(41)
	if wr.isOpen("server:cpu") {
		t.Fatal("the incident is still open after a reading under")
	}
	ms := wr.settle(t)
	wantTitles(t, ms, "CPU at 95 % for 5 min (threshold 90 %)", "✅ CPU back to 41 % (threshold 90 %)")
	if rows := wr.rows(t); rows[1].Action != store.ActAlertRecovery || rows[1].Reason != "CPU back to 41 % (threshold 90 %)" {
		t.Fatalf("recovery row = %+v", rows[1])
	}
}

func TestServerRAM(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	for i := 0; i <= 10; i++ {
		at := wr.clock.Now()
		wr.src.set(func(f *fakeSources) { f.tick, f.tickOK = serverTick(at, 10, 15*gib), true }) // 93.75 %
		wr.pass()
		wr.clock.Add(30 * time.Second)
	}
	at := wr.clock.Now()
	wr.src.set(func(f *fakeSources) { f.tick, f.tickOK = serverTick(at, 10, 8*gib), true })
	wr.pass()
	wantTitles(t, wr.settle(t), "RAM at 94 % for 5 min (threshold 90 %)", "✅ RAM back to 50 % (threshold 90 %)")
}

func TestDiskPerMount(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	wr.src.set(func(f *fakeSources) {
		f.disks = []metrics.Disk{{Mount: "/", Used: 50, Total: 100}, {Mount: "/var", Used: 91, Total: 100}}
	})
	for i := 0; i <= 10; i++ {
		wr.pass()
		if open, want := wr.isOpen("server:disk:/var"), i == 10; open != want {
			t.Fatalf("pass %d: open = %v, want %v", i, open, want)
		}
		wr.clock.Add(30 * time.Second)
	}
	if wr.isOpen("server:disk:/") {
		t.Fatal("/ at 50 % raised an alert")
	}
	wr.src.set(func(f *fakeSources) { f.disks[1].Used = 70 })
	wr.pass()
	ms := wr.settle(t)
	wantTitles(t, ms, "Disk /var at 91 % (threshold 85 %)", "✅ Disk /var back to 70 % (threshold 85 %)")
	if ms[0].Link != "" || wr.rows(t)[0].Target != "prod-01" {
		t.Fatalf("disk alert = %+v", ms[0])
	}
}

func TestProjectThresholds(t *testing.T) {
	wr := newWatchRig(t, nil, "shop-api", "blog")
	wr.start(t)
	shop, blog := wr.ids["shop-api"], wr.ids["blog"]
	wr.src.set(func(f *fakeSources) {
		f.tracked = []store.Tracked{
			{ID: shop, DisplayName: "shop-api", ComposeProject: "shop-api", AlertCPUPercent: intp(50), AlertRAMMB: intp(2048)},
			{ID: blog, DisplayName: "blog", ComposeProject: "blog"},
		}
	})
	for i := 0; i <= 10; i++ {
		at := wr.clock.Now()
		wr.src.set(func(f *fakeSources) {
			f.tick, f.tickOK = serverTick(at, 10, 8*gib), true
			f.tick.Projects = map[int64]metrics.ProjectNow{
				shop: {CPU: f64p(240), Mem: i64p(3328599654), Running: true}, // 60 % of 4 cores, 3.1 GiB
				blog: {CPU: f64p(350), Mem: i64p(15 * gib), Running: true},   // 87.5 %, 93.75 % of RAM
				99:   {CPU: f64p(400), Mem: i64p(16 * gib), Running: true},   // not tracked
			}
		})
		wr.pass()
		wr.clock.Add(30 * time.Second)
	}
	keys := wr.n.OpenKeys()
	want := []string{projectKey(blog, "ram"), projectKey(shop, "cpu"), projectKey(shop, "ram")}
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Fatalf("open keys = %q, want %q", keys, want)
	}
	ms := wr.settle(t)
	got := titles(ms)
	slices.Sort(got)
	exp := []string{
		"blog RAM at 94 % of server for 5 min (threshold 90 %)",
		"shop-api CPU at 60 % of server for 5 min (threshold 50 %)",
		"shop-api RAM at 3.1 GB for 5 min (threshold 2.0 GB)",
	}
	if !slices.Equal(got, exp) {
		t.Fatalf("messages = %q, want %q", got, exp)
	}
	for _, row := range wr.rows(t)[:3] {
		if row.ProjectID == 0 || row.Action != store.ActAlertResource || (row.Target != "shop-api" && row.Target != "blog") {
			t.Fatalf("row = %+v", row)
		}
	}
}

func TestThresholdRaisedRecovers(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	for range 11 {
		wr.passWithCPU(95)
		wr.clock.Add(30 * time.Second)
	}
	if !wr.isOpen("server:cpu") {
		t.Fatal("no alert at 95 %")
	}
	wr.th.setCPU(96)
	wr.passWithCPU(95)
	if wr.isOpen("server:cpu") {
		t.Fatal("still open after the threshold was raised above the value")
	}
	wantTitles(t, wr.settle(t), "CPU at 95 % for 5 min (threshold 90 %)", "✅ CPU back to 95 % (threshold 96 %)")
}

// tunnelPasses sets each state in turn and passes once per state.
func (wr *watchRig) tunnelPasses(states ...metrics.TunnelState) {
	for _, s := range states {
		wr.src.set(func(f *fakeSources) { f.tunnel = s })
		wr.pass()
		wr.clock.Add(30 * time.Second)
	}
}

func TestTunnelNeedsSeenConnected(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	wr.tunnelPasses(metrics.TunnelDisconnected, metrics.TunnelDisconnected, metrics.TunnelNotRunning,
		metrics.TunnelDisconnected, metrics.TunnelDisconnected)
	if wr.isOpen(keyTunnel) {
		t.Fatal("a tunnel never seen connected raised an alert")
	}
	if got := wr.src.seenWrites(); len(got) != 0 {
		t.Fatalf("SetSeen = %v, want no call", got)
	}
}

func TestTunnelTwoReads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		states []metrics.TunnelState
		open   bool
	}{
		{"one disconnected read", []metrics.TunnelState{metrics.TunnelDisconnected}, false},
		{"two disconnected reads", []metrics.TunnelState{metrics.TunnelDisconnected, metrics.TunnelDisconnected}, true},
		{"not running counts", []metrics.TunnelState{metrics.TunnelNotRunning, metrics.TunnelDisconnected}, true},
		{"unknown changes nothing", []metrics.TunnelState{metrics.TunnelDisconnected, metrics.TunnelUnknown, metrics.TunnelUnknown, metrics.TunnelNotRunning}, true},
		{"unknown alone", []metrics.TunnelState{metrics.TunnelUnknown, metrics.TunnelUnknown, metrics.TunnelUnknown}, false},
		{"connected resets", []metrics.TunnelState{metrics.TunnelDisconnected, metrics.TunnelConnected, metrics.TunnelDisconnected}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wr := newWatchRig(t, nil)
			wr.start(t)
			wr.src.set(func(f *fakeSources) { f.seen = true })
			wr.tunnelPasses(tc.states...)
			if open := wr.isOpen(keyTunnel); open != tc.open {
				t.Fatalf("open = %v, want %v", open, tc.open)
			}
			if tc.open {
				wantTitles(t, wr.settle(t), "Cloudflare Tunnel disconnected")
				if row := wr.rows(t)[0]; row.Action != store.ActAlertTunnel || row.Target != "prod-01" {
					t.Fatalf("row = %+v", row)
				}
			}
			if got := wr.src.seenWrites(); len(got) != 0 {
				t.Fatalf("SetSeen = %v, want no call (already seen)", got)
			}
		})
	}
}

func TestTunnelRecovery(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	wr.src.set(func(f *fakeSources) { f.seen = true })
	wr.tunnelPasses(metrics.TunnelDisconnected, metrics.TunnelNotRunning)
	if !wr.isOpen(keyTunnel) {
		t.Fatal("no tunnel alert")
	}
	wr.tunnelPasses(metrics.TunnelUnknown) // changes nothing
	if !wr.isOpen(keyTunnel) {
		t.Fatal("unknown closed the incident")
	}
	wr.tunnelPasses(metrics.TunnelConnected, metrics.TunnelConnected)
	ms := wr.settle(t)
	wantTitles(t, ms, "Cloudflare Tunnel disconnected", "✅ Cloudflare Tunnel connected again")
	if ms[0].Detail != "cloudflared is not running" && ms[0].Detail != "cloudflared has no connection to Cloudflare" {
		t.Fatalf("detail = %q", ms[0].Detail)
	}
}

func TestTunnelSeenStoredOnce(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	wr.tunnelPasses(metrics.TunnelConnected, metrics.TunnelConnected, metrics.TunnelUnknown, metrics.TunnelConnected)
	if got := wr.src.seenWrites(); !slices.Equal(got, []bool{true}) {
		t.Fatalf("SetSeen = %v, want one true", got)
	}
	wr.tunnelPasses(metrics.TunnelDisconnected, metrics.TunnelDisconnected)
	if !wr.isOpen(keyTunnel) {
		t.Fatal("no alert once the tunnel was seen")
	}
}

// appScene is a project named name with two services: db running and wp in
// wpState (wp restarted 14 times; "restarting" is Error).
func appScene(id int64, name, wpState, domain string) (store.Tracked, []project.Container, map[string]project.Inspect) {
	tr := store.Tracked{ID: id, DisplayName: name, ComposeProject: name, Services: []string{"db", "wp"}, PublicDomain: domain}
	cs := []project.Container{
		{Project: name, Service: "db", State: "running", ID: name + "-db", Name: name + "-db-1"},
		{Project: name, Service: "wp", State: wpState, ID: name + "-wp", Name: name + "-wp-1"},
	}
	insp := map[string]project.Inspect{name + "-db": {}, name + "-wp": {RestartCount: 14, ExitCode: 1}}
	return tr, cs, insp
}

func blogScene(id int64, wpState, domain string) (store.Tracked, []project.Container, map[string]project.Inspect) {
	return appScene(id, "blog", wpState, domain)
}

// setApp stores a container read made at at of the seeded project name, with
// wp in wpState.
func (wr *watchRig) setApp(name string, at time.Time, wpState, domain string) {
	tr, cs, insp := appScene(wr.ids[name], name, wpState, domain)
	wr.src.set(func(f *fakeSources) {
		f.tracked, f.cs, f.insp, f.csAt, f.dockerUp, f.csOK = []store.Tracked{tr}, cs, insp, at, true, true
	})
}

// setRead is setApp for blog.
func (wr *watchRig) setRead(at time.Time, wpState, domain string) {
	wr.setApp("blog", at, wpState, domain)
}

func TestProjectErrorTwoReads(t *testing.T) {
	wr := newWatchRig(t, nil, "blog")
	wr.start(t)
	key := projectKey(wr.ids["blog"], "error")
	readA := wr.clock.Now()
	wr.setRead(readA, "restarting", "")
	wr.pass()
	wr.clock.Add(30 * time.Second)
	wr.pass() // the same read again
	if wr.isOpen(key) {
		t.Fatal("one read raised the Error alert")
	}
	wr.clock.Add(10 * time.Second)
	wr.setRead(readA.Add(40*time.Second), "restarting", "")
	wr.pass()
	if !wr.isOpen(key) {
		t.Fatal("no alert after two reads 40 s apart")
	}
	ms := wr.settle(t)
	wantTitles(t, ms, "blog is in Error")
	if ms[0].Detail != "blog-wp-1 restarted 14 times" {
		t.Fatalf("detail = %q", ms[0].Detail)
	}
	if row := wr.rows(t)[0]; row.Action != store.ActAlertProject || row.ProjectID != wr.ids["blog"] || row.Target != "blog" || row.Reason != "blog-wp-1 restarted 14 times" {
		t.Fatalf("row = %+v", row)
	}
}

func TestProjectErrorReadsCloserThan30s(t *testing.T) {
	wr := newWatchRig(t, nil, "blog")
	wr.start(t)
	key := projectKey(wr.ids["blog"], "error")
	t0 := wr.clock.Now()
	for _, d := range []time.Duration{0, 10 * time.Second, 20 * time.Second} {
		wr.setRead(t0.Add(d), "restarting", "")
		wr.pass()
	}
	if wr.isOpen(key) {
		t.Fatal("reads 20 s apart raised the alert")
	}
	wr.setRead(t0.Add(30*time.Second), "restarting", "")
	wr.pass()
	if !wr.isOpen(key) {
		t.Fatal("no alert once a read is 30 s after the first")
	}
}

func TestProjectErrorMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		cs   []project.Container
		insp map[string]project.Inspect
		want string
	}{
		{"highest restart count", []project.Container{
			{Project: "blog", Service: "db", State: "restarting", ID: "a", Name: "blog-db-1"},
			{Project: "blog", Service: "wp", State: "restarting", ID: "b", Name: "blog-wp-1"},
		}, map[string]project.Inspect{"a": {RestartCount: 1}, "b": {RestartCount: 3}}, "blog-wp-1 restarted 3 times"},
		{"one restart", []project.Container{
			{Project: "blog", Service: "wp", State: "restarting", ID: "b", Name: "blog-wp-1"},
		}, map[string]project.Inspect{"b": {RestartCount: 1}}, "blog-wp-1 restarted 1 time"},
		{"first exit reason", []project.Container{
			{Project: "blog", Service: "db", State: "running", ID: "a", Name: "blog-db-1"},
			{Project: "blog", Service: "wp", State: "exited", ExitCode: 1, HasExitCode: true, ID: "b", Name: "blog-wp-1"},
		}, nil, "blog-wp-1: Exited (1)"},
		{"dead", []project.Container{
			{Project: "blog", Service: "db", State: "running", ID: "a", Name: "blog-db-1"},
			{Project: "blog", Service: "wp", State: "dead", ID: "b", Name: "blog-wp-1"},
		}, map[string]project.Inspect{"a": {}, "b": {}}, "blog-wp-1: Dead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comps := project.Components(tc.cs, "blog", []string{"db", "wp"}, tc.insp, nil)
			if got := issueOf(comps); got != tc.want {
				t.Fatalf("issueOf = %q, want %q", got, tc.want)
			}
		})
	}
	// With no restart and no exit reason (a paused container is not
	// running), the running count.
	comps := []project.Component{{Name: "a", State: "running"}, {Name: "b", State: "paused"}, {Name: "c", Status: "Not created"}}
	if got := issueOf(comps); got != "1 of 3 containers running" {
		t.Fatalf("issueOf = %q", got)
	}
	if got := issueOf(nil); got != "Not running" {
		t.Fatalf("issueOf(nil) = %q", got)
	}
}

// raiseError puts blog into Error on two reads 30 s apart.
func (wr *watchRig) raiseError(t *testing.T, domain string) {
	t.Helper()
	at := wr.clock.Now()
	wr.setRead(at, "restarting", domain)
	wr.pass()
	wr.clock.Add(30 * time.Second)
	wr.setRead(at.Add(30*time.Second), "restarting", domain)
	wr.pass()
	if !wr.isOpen(projectKey(wr.ids["blog"], "error")) {
		t.Fatal("no Error alert")
	}
}

func TestProjectInFlightSkipped(t *testing.T) {
	wr := newWatchRig(t, nil, "blog")
	wr.start(t)
	id := wr.ids["blog"]
	key := projectKey(id, "error")
	wr.src.set(func(f *fakeSources) { f.locks = map[int64]project.Status{id: project.StatusRestarting} })
	at := wr.clock.Now()
	for i := range 4 {
		wr.setRead(at.Add(time.Duration(i)*40*time.Second), "restarting", "")
		wr.pass()
	}
	if wr.isOpen(key) {
		t.Fatal("a project in flight raised the Error alert")
	}

	// An open incident is left as it is while an action runs.
	wr.src.set(func(f *fakeSources) { f.locks = nil })
	wr.clock.Add(time.Minute)
	wr.raiseError(t, "")
	wr.src.set(func(f *fakeSources) { f.locks = map[int64]project.Status{id: project.StatusStopping} })
	wr.setRead(wr.clock.Now().Add(time.Minute), "exited", "")
	wr.pass()
	if !wr.isOpen(key) {
		t.Fatal("an action in flight closed the incident")
	}
	wantTitles(t, wr.settle(t), "blog is in Error")
}

func TestProjectRecoveryOnRunningOrPartial(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wpState string
	}{{"running", "running"}, {"partial", "exited"}} {
		t.Run(tc.name, func(t *testing.T) {
			wr := newWatchRig(t, nil, "blog")
			wr.start(t)
			wr.raiseError(t, "")
			wr.clock.Add(30 * time.Second)
			at := wr.clock.Now()
			tr, cs, insp := blogScene(wr.ids["blog"], tc.wpState, "")
			cs[1].HasExitCode = tc.wpState == "exited" // exit 0: Partial, not Error
			wr.src.set(func(f *fakeSources) { f.tracked, f.cs, f.insp, f.csAt = []store.Tracked{tr}, cs, insp, at })
			wr.pass()
			if wr.isOpen(projectKey(wr.ids["blog"], "error")) {
				t.Fatal("still open")
			}
			ms := wr.settle(t)
			wantTitles(t, ms, "blog is in Error", "✅ blog is running again")
			if row := wr.rows(t)[1]; row.Action != store.ActAlertRecovery || row.ProjectID != wr.ids["blog"] {
				t.Fatalf("recovery row = %+v", row)
			}
		})
	}
}

func TestProjectStoppedClosesSilently(t *testing.T) {
	wr := newWatchRig(t, nil, "blog")
	wr.start(t)
	wr.raiseError(t, "")
	wr.clock.Add(30 * time.Second)
	at := wr.clock.Now()
	tr, _, _ := blogScene(wr.ids["blog"], "", "")
	wr.src.set(func(f *fakeSources) { f.tracked, f.cs, f.csAt = []store.Tracked{tr}, nil, at }) // nothing left: Stopped
	wr.pass()
	if wr.isOpen(projectKey(wr.ids["blog"], "error")) {
		t.Fatal("still open after the project stopped")
	}
	wantTitles(t, wr.settle(t), "blog is in Error")
	waitFor(t, "the stored incident closed", func() bool {
		in, err := wr.st.OpenIncidents(context.Background())
		return err == nil && len(in) == 0
	})
}

func TestProjectDockerDownChangesNothing(t *testing.T) {
	wr := newWatchRig(t, nil, "blog")
	wr.start(t)
	wr.raiseError(t, "")
	wr.src.set(func(f *fakeSources) { f.dockerUp, f.cs, f.csAt = false, nil, f.csAt.Add(time.Minute) })
	wr.pass()
	if !wr.isOpen(projectKey(wr.ids["blog"], "error")) {
		t.Fatal("Docker down closed the incident")
	}
}

func TestWatcherRestartWithOpenIncidentSendsNothing(t *testing.T) {
	wr := newWatchRig(t, nil, "blog")
	id := wr.ids["blog"]
	key := projectKey(id, "error")
	ctx := context.Background()
	for _, in := range []store.Incident{{Key: key, ProjectID: id, OpenedAt: wr.clock.Now()}, {Key: keyTunnel, OpenedAt: wr.clock.Now()}} {
		if err := wr.st.OpenIncident(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	wr.start(t) // loads the stored incidents
	wr.src.set(func(f *fakeSources) { f.seen = true })

	at := wr.clock.Now()
	for i := range 4 { // still in Error and the tunnel still down
		wr.setRead(at.Add(time.Duration(i)*40*time.Second), "restarting", "")
		wr.tunnelPasses(metrics.TunnelDisconnected)
	}
	if ms := wr.settle(t); len(ms) != 0 {
		t.Fatalf("messages after the restart = %q, want none", titles(ms))
	}
	wr.setRead(wr.clock.Now(), "running", "")
	wr.tunnelPasses(metrics.TunnelConnected)
	got := titles(wr.settle(t))
	slices.Sort(got)
	if want := []string{"✅ Cloudflare Tunnel connected again", "✅ blog is running again"}; !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

func TestWatcherStartAndClose(t *testing.T) {
	wr := newWatchRig(t, nil)
	wr.start(t)
	timers := &testTimers{}
	wr.w.after = timers.after
	wr.src.set(func(f *fakeSources) { f.disks = []metrics.Disk{{Mount: "/", Used: 99, Total: 100}} })
	wr.w.Start(context.Background())
	waitFor(t, "the first pass", func() bool { return timers.count() == 1 })
	for i := 1; i <= 10; i++ {
		wr.clock.Add(30 * time.Second)
		timers.fire()
		waitFor(t, "the next pass", func() bool { return timers.count() == i+1 })
	}
	wr.w.Close()
	if !wr.isOpen("server:disk:/") {
		t.Fatal("the loop's passes raised no disk alert")
	}
	wr.w.Close() // twice is fine
}

func TestSweepClosesUnwatched(t *testing.T) {
	wr := newWatchRig(t, nil, "blog", "shop")
	blog, shop := wr.ids["blog"], wr.ids["shop"]
	ctx := context.Background()
	keys := []string{
		projectKey(blog, "error"), projectKey(blog, "cpu"), projectKey(blog, "domain"),
		projectKey(shop, "domain"), projectKey(shop, "ram"),
		"server:disk:/old", "server:disk:/", "server:cpu", keyTunnel,
	}
	for _, k := range keys {
		if err := wr.st.OpenIncident(ctx, store.Incident{Key: k, OpenedAt: wr.clock.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	wr.start(t)
	// blog is no longer tracked; shop has no public domain any more; /old
	// left the disk list.
	wr.src.set(func(f *fakeSources) {
		f.tracked = []store.Tracked{{ID: shop, DisplayName: "shop", ComposeProject: "shop"}}
		f.disks = []metrics.Disk{{Mount: "/", Used: 90, Total: 100}}
		f.tunnel, f.seen = metrics.TunnelDisconnected, true
	})
	wr.pass()
	want := []string{projectKey(shop, "ram"), "server:cpu", "server:disk:/", keyTunnel}
	slices.Sort(want)
	if got := wr.n.OpenKeys(); !slices.Equal(got, want) {
		t.Fatalf("open keys = %q, want %q", got, want)
	}
	if ms := wr.settle(t); len(ms) != 0 {
		t.Fatalf("messages = %q, want none", titles(ms))
	}
	waitFor(t, "the stored incidents closed", func() bool {
		in, err := wr.st.OpenIncidents(ctx)
		return err == nil && len(in) == len(want)
	})

	// A failed read of the tracked list sweeps no project.
	wr.src.set(func(f *fakeSources) { f.trackedErr, f.tracked = errTest, nil })
	wr.pass()
	if got := wr.n.OpenKeys(); !slices.Equal(got, want) {
		t.Fatalf("open keys after a failed read = %q, want %q", got, want)
	}
}

var errTest = errors.New("test")

func TestAlertReasonFallsBackToTitle(t *testing.T) {
	r := newRig(t, Frame{})
	r.start(t)
	r.n.Raise(Alert{Action: store.ActAlertResource, Target: "prod-01", Message: Message{Title: "CPU at 95 % for 5 min (threshold 90 %)"}})
	row := r.waitRows(t, "the row", finished(1))[0]
	if !strings.HasPrefix(row.Reason, "CPU at 95 % for 5 min (threshold 90 %) · ") {
		t.Fatalf("reason = %q", row.Reason)
	}
}

// A project whose containers all stopped reads Running false with CPU and RAM
// at 0 in the tick: that is no reading, so its resource incidents close
// silently and send no "back to 0 %" recovery.
func TestProjectStoppedClosesResourceSilently(t *testing.T) {
	wr := newWatchRig(t, nil, "shop")
	wr.start(t)
	shop := wr.ids["shop"]
	wr.src.set(func(f *fakeSources) {
		f.tracked = []store.Tracked{{ID: shop, DisplayName: "shop", ComposeProject: "shop"}}
	})
	tickWith := func(p metrics.ProjectNow) {
		at := wr.clock.Now()
		wr.src.set(func(f *fakeSources) {
			f.tick, f.tickOK = serverTick(at, 10, 8*gib), true
			f.tick.Projects = map[int64]metrics.ProjectNow{shop: p}
		})
		wr.pass()
		wr.clock.Add(30 * time.Second)
	}
	for range 11 { // 95 % of 4 cores and 15 GiB of 16 for 5 minutes
		tickWith(metrics.ProjectNow{CPU: f64p(380), Mem: i64p(15 * gib), Running: true})
	}
	cpu, ram := projectKey(shop, "cpu"), projectKey(shop, "ram")
	if !wr.isOpen(cpu) || !wr.isOpen(ram) {
		t.Fatalf("open keys = %q, want the CPU and RAM incidents", wr.n.OpenKeys())
	}
	wantTitles(t, wr.settle(t),
		"shop CPU at 95 % of server for 5 min (threshold 90 %)",
		"shop RAM at 94 % of server for 5 min (threshold 90 %)")

	tickWith(metrics.ProjectNow{CPU: f64p(0), Mem: i64p(0), Running: false})
	if wr.isOpen(cpu) || wr.isOpen(ram) {
		t.Fatalf("open keys = %q after the project stopped", wr.n.OpenKeys())
	}
	if ms := wr.settle(t); len(ms) != 2 {
		t.Fatalf("messages = %q, want no recovery", titles(ms))
	}
	waitFor(t, "the stored incidents closed", func() bool {
		in, err := wr.st.OpenIncidents(context.Background())
		return err == nil && len(in) == 0
	})

	// Started again and hot: the timer starts over, it was not kept.
	for range 10 {
		tickWith(metrics.ProjectNow{CPU: f64p(380), Mem: i64p(1 * gib), Running: true})
	}
	if wr.isOpen(cpu) {
		t.Fatal("the timer from before the stop was kept")
	}
	tickWith(metrics.ProjectNow{CPU: f64p(380), Mem: i64p(1 * gib), Running: true})
	if !wr.isOpen(cpu) {
		t.Fatal("no CPU alert 5 minutes after the restart")
	}
}

// The fallback disk list ("/ (panel data)", no host root) says nothing about
// the real disks: it neither closes their incidents nor drops their timers.
func TestDiskFallbackSweepsNothing(t *testing.T) {
	wr := newWatchRig(t, nil)
	if err := wr.st.OpenIncident(context.Background(), store.Incident{Key: "server:disk:/old", OpenedAt: wr.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	wr.start(t)
	// /old is still listed (and still over) before the fallback pass.
	before := []metrics.Disk{{Mount: "/", Used: 50, Total: 100}, {Mount: "/old", Used: 90, Total: 100}, {Mount: "/var", Used: 91, Total: 100}}
	after := []metrics.Disk{{Mount: "/", Used: 50, Total: 100}, {Mount: "/var", Used: 91, Total: 100}}
	fallback := []metrics.Disk{{Mount: "/ (panel data)", Used: 50, Total: 100}}
	passWith := func(disks []metrics.Disk, fb bool) {
		wr.src.set(func(f *fakeSources) { f.disks, f.fallback = slices.Clone(disks), fb })
		wr.pass()
		wr.clock.Add(30 * time.Second)
	}
	for range 6 {
		passWith(before, false)
	}
	passWith(fallback, true)
	if !wr.isOpen("server:disk:/old") {
		t.Fatal("the fallback list closed a real disk's incident")
	}
	for i := range 4 {
		passWith(after, false)
		if open, want := wr.isOpen("server:disk:/var"), i == 3; open != want {
			t.Fatalf("pass %d after the fallback: open = %v, want %v (the timer was dropped)", i, open, want)
		}
	}
	// The real list sweeps as before, silently.
	if wr.isOpen("server:disk:/old") {
		t.Fatal("a real list did not close the disk that left it")
	}
	wantTitles(t, wr.settle(t), "Disk /var at 91 % (threshold 85 %)")
}
