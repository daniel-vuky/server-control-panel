package metrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// fakeClock is the sampler's clock in the tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeSource stands in for the hub. A nil use or insp is no fresh read. A
// non-nil during runs inside each RecentContainers call.
type fakeSource struct {
	mu     sync.Mutex
	cs     []project.Container
	up     bool
	use    map[string]project.Usage
	insp   map[string]project.Inspect
	during func()

	// The max ages the sampler asked for, last call of each.
	containersAge, statsAge, inspectAge time.Duration

	starts   []time.Time // when each RecentContainers call began
	inFlight int
	peak     int
}

func (f *fakeSource) RecentContainers(_ context.Context, maxAge time.Duration) ([]project.Container, bool) {
	f.mu.Lock()
	f.starts = append(f.starts, time.Now())
	f.inFlight++
	f.peak = max(f.peak, f.inFlight)
	f.containersAge = maxAge
	during, cs, up := f.during, slices.Clone(f.cs), f.up
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()
	if during != nil {
		during()
	}
	if !up {
		return nil, false
	}
	return cs, true
}

func (f *fakeSource) RecentStats(maxAge time.Duration) (map[string]project.Usage, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statsAge = maxAge
	if f.use == nil {
		return nil, time.Time{}, false
	}
	return maps.Clone(f.use), time.Now(), true
}

func (f *fakeSource) RecentInspect(maxAge time.Duration) (map[string]project.Inspect, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectAge = maxAge
	if f.insp == nil {
		return nil, time.Time{}, false
	}
	return maps.Clone(f.insp), time.Now(), true
}

func (f *fakeSource) set(fn func(f *fakeSource)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// cmdCall is one command the fake commander ran.
type cmdCall struct {
	Action runner.Action
	Values runner.Values
}

// fakeCmd answers docker.stats and docker.inspect with canned output. While
// block is set a call waits for its context to end.
type fakeCmd struct {
	mu      sync.Mutex
	out     map[runner.Action]string
	err     map[runner.Action]error
	block   bool
	calls   []cmdCall
	blocked int
}

func newFakeCmd() *fakeCmd {
	return &fakeCmd{out: map[runner.Action]string{}, err: map[runner.Action]error{}}
}

func (f *fakeCmd) Run(ctx context.Context, s runner.Stack, a runner.Action, v runner.Values, _ []string) (runner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cmdCall{Action: a, Values: maps.Clone(v)})
	out, err, block := f.out[a], f.err[a], f.block
	if block {
		f.blocked++
	}
	f.mu.Unlock()
	if s != runner.StackDocker {
		return runner.Result{}, fmt.Errorf("fake: stack %s", s)
	}
	if block {
		<-ctx.Done()
		return runner.Result{}, ctx.Err()
	}
	if err != nil {
		return runner.Result{ExitCode: 1, Stdout: []byte(out)}, err
	}
	return runner.Result{Stdout: []byte(out)}, nil
}

func (f *fakeCmd) set(a runner.Action, out string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.out[a], f.err[a] = out, err
}

func (f *fakeCmd) callsOf(a runner.Action) []cmdCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cmdCall
	for _, c := range f.calls {
		if c.Action == a {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeCmd) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// cid is a stable 64-hex container id per (project, service).
func cid(proj, service string) string {
	sum := sha256.Sum256([]byte(proj + "/" + service))
	return hex.EncodeToString(sum[:])
}

// ctr is a running container of (proj, service).
func ctr(proj, service string) project.Container {
	return project.Container{Project: proj, Service: service, State: "running", ID: cid(proj, service),
		Name: proj + "-" + service + "-1", Image: "nginx"}
}

// panelCtr is the panel's own container, in compose project scp.
func panelCtr() project.Container {
	c := ctr("scp", "panel")
	c.PanelImage = true
	return c
}

// hostState is what the fake host's /proc says: CPU jiffies, memory in KiB,
// disk sectors and network bytes.
type hostState struct {
	busy, idle          uint64
	memUsed, memTotal   int64
	readSect, writeSect uint64
	netIn, netOut       uint64
}

// samplerEnv is a store with the tracked projects shop (app, db), notes (app)
// and blog (app, missing), a host /proc tree, the panel's own /proc, a fake
// clock, hub and commander, and a Sampler over them.
type samplerEnv struct {
	t        *testing.T
	st       *store.Store
	proc     string
	selfProc string
	clock    *fakeClock
	src      *fakeSource
	cmd      *fakeCmd
	ids      map[string]int64
	s        *Sampler
}

func newSamplerEnv(t *testing.T) *samplerEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	var ps []store.NewProject
	for _, p := range []struct {
		name     string
		services []string
	}{{"shop", []string{"app", "db"}}, {"notes", []string{"app"}}, {"blog", []string{"app"}}} {
		var svcs []project.Service
		for _, s := range p.services {
			svcs = append(svcs, project.Service{Name: s})
		}
		ps = append(ps, store.NewProject{Path: filepath.Join(root, p.name), ComposeFiles: []string{"compose.yaml"},
			ComposeProject: p.name, DisplayName: p.name, Tracked: true, Details: project.Details{Services: svcs}})
	}
	wsID, _, err := st.AddWorkspace(ctx, store.NewWorkspace{Name: "apps", Path: root, Depth: 1, LastScanAt: time.Now()}, ps)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyRescan(ctx, store.RescanApply{Workspaces: []store.WorkspaceApply{{
		ID: wsID, LastScanAt: time.Now(), Missing: []string{filepath.Join(root, "blog")},
	}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ts, err := st.TrackedProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := &samplerEnv{t: t, st: st, proc: t.TempDir(), selfProc: t.TempDir(),
		clock: &fakeClock{t: time.Date(2026, 10, 31, 10, 0, 10, 0, time.UTC)},
		src:   &fakeSource{up: true}, cmd: newFakeCmd(), ids: map[string]int64{}}
	for _, tr := range ts {
		e.ids[tr.DisplayName] = tr.ID
	}
	e.setHost(hostState{busy: 1000, idle: 9000, memUsed: 2 << 20, memTotal: 8 << 20})
	e.setSelf(1000, 16384)
	e.s = NewSampler(st, e.cmd, e.src, Host{Proc: e.proc, Sys: filepath.Join(e.proc, "no-sys")}, e.clock.now,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.s.selfProc = e.selfProc
	return e
}

func (e *samplerEnv) write(dir, name, content string) {
	e.t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

const netDevHeader = "Inter-|   Receive                                                |  Transmit\n" +
	" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"

func netDevLine(iface string, in, out uint64) string {
	return fmt.Sprintf("%6s: %d 0 0 0 0 0 0 0 %d 0 0 0 0 0 0 0\n", iface, in, out)
}

// setHost rewrites the host's stat (two cores), meminfo, diskstats and
// 1/net/dev.
func (e *samplerEnv) setHost(h hostState) {
	e.t.Helper()
	cpu := func(name string, busy, idle uint64) string {
		return fmt.Sprintf("%s %d 0 0 %d 0 0 0 0 0 0\n", name, busy, idle)
	}
	e.write(e.proc, "stat", cpu("cpu ", h.busy, h.idle)+cpu("cpu0", h.busy/2, h.idle/2)+cpu("cpu1", h.busy/2, h.idle/2))
	e.write(e.proc, "meminfo", fmt.Sprintf("MemTotal: %d kB\nMemFree: 1 kB\nMemAvailable: %d kB\n", h.memTotal, h.memTotal-h.memUsed))
	e.write(e.proc, "diskstats", fmt.Sprintf("   8       0 sda 1 0 %d 0 1 0 %d 0 0 0 0 0 0 0 0 0 0\n", h.readSect, h.writeSect))
	e.write(e.proc, "1/net/dev", netDevHeader+netDevLine("lo", 5, 5)+netDevLine("eth0", h.netIn, h.netOut)+netDevLine("veth1", 7, 7))
}

// setNet rewrites the net/dev of host pid pid, a container's namespace.
func (e *samplerEnv) setNet(pid int, in, out uint64) {
	e.t.Helper()
	e.write(e.proc, fmt.Sprintf("%d/net/dev", pid), netDevHeader+netDevLine("lo", 999, 999)+netDevLine("eth0", in, out))
}

// setSelf rewrites the panel's own /proc/self: CPU ticks and VmRSS in kB.
func (e *samplerEnv) setSelf(ticks uint64, rssKiB int64) {
	e.t.Helper()
	e.write(e.selfProc, "self/stat", fmt.Sprintf("7 (panel) S 1 7 7 0 -1 4194560 8123 0 12 0 %d 0 0 0 20 0 11 0 214230 1785856000 4096\n", ticks))
	e.write(e.selfProc, "self/status", fmt.Sprintf("Name:\tpanel\nVmRSS:\t%d kB\n", rssKiB))
}

// tick runs one sampler tick at the fake clock's time.
func (e *samplerEnv) tick() {
	e.t.Helper()
	e.s.tick(context.Background())
}

// slotRange is the raw 30-second slot of at.
func slotRange(at time.Time) store.Range {
	from := time.Unix(at.Unix()/30*30, 0).UTC()
	return store.Range{From: from, To: from, Step: 30 * time.Second}
}

// server returns the raw server row of at's slot; ok is false with none.
func (e *samplerEnv) server(at time.Time) (store.ServerSample, bool) {
	e.t.Helper()
	rows, err := e.st.ServerSeries(context.Background(), slotRange(at))
	if err != nil {
		e.t.Fatal(err)
	}
	r := rows[0]
	return r, r.CPU != nil || r.MemUsed != nil || r.MemTotal != nil || r.DiskRead != nil || r.NetIn != nil
}

// projectRow returns the raw row of the project named name at at's slot; ok
// is false with none.
func (e *samplerEnv) projectRow(name string, at time.Time) (store.ProjectSample, bool) {
	e.t.Helper()
	rows, err := e.st.ProjectSeries(context.Background(), e.ids[name], slotRange(at))
	if err != nil {
		e.t.Fatal(err)
	}
	r := rows[0]
	return r, r.CPU != nil || r.Mem != nil || r.NetIn != nil || r.NetOut != nil
}

// historyStart is the oldest kept sample of project id (0 = the server).
func (e *samplerEnv) historyStart(id int64) *time.Time {
	e.t.Helper()
	at, err := e.st.HistoryStart(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return at
}

func f64(p *float64) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}

func i64(p *int64) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}

func near(p *float64, want float64) bool {
	return p != nil && *p > want-1e-6 && *p < want+1e-6
}

// The standard scene: shop's app and db and notes' app run, with network
// namespaces at pids 101, 102 and 201; a stranger project runs untracked.
func (e *samplerEnv) standardScene() {
	e.src.set(func(f *fakeSource) {
		f.cs = []project.Container{ctr("shop", "app"), ctr("shop", "db"), ctr("notes", "app"), ctr("stranger", "app")}
		f.use = map[string]project.Usage{
			cid("shop", "app"):     {CPUPercent: 10, MemBytes: 100 << 20},
			cid("shop", "db"):      {CPUPercent: 5, MemBytes: 50 << 20},
			cid("notes", "app"):    {CPUPercent: 1, MemBytes: 10 << 20},
			cid("stranger", "app"): {CPUPercent: 50, MemBytes: 500 << 20},
		}
		f.insp = map[string]project.Inspect{
			cid("shop", "app"):  {Pid: 101, NetworkMode: "bridge"},
			cid("shop", "db"):   {Pid: 102, NetworkMode: "shop_default"},
			cid("notes", "app"): {Pid: 201, NetworkMode: "bridge"},
		}
	})
	e.setNet(101, 1000, 2000)
	e.setNet(102, 100, 200)
	e.setNet(201, 10, 20)
}

func TestSamplerFirstTickIsBaseline(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	e.tick()
	if at := e.historyStart(0); at != nil {
		t.Fatalf("server history starts %s after the first tick, want no row", at)
	}
	for _, name := range []string{"shop", "notes"} {
		if at := e.historyStart(e.ids[name]); at != nil {
			t.Fatalf("%s history starts %s after the first tick, want no row", name, at)
		}
	}
	if tk, ok := e.s.Latest(); ok {
		t.Fatalf("Latest after the first tick = %+v, want none", tk)
	}

	e.clock.add(30 * time.Second)
	e.tick()
	if _, ok := e.server(e.clock.now()); !ok {
		t.Fatal("no server row after the second tick")
	}
	if _, ok := e.s.Latest(); !ok {
		t.Fatal("no latest tick after the second tick")
	}
}

func TestSamplerWritesServerAndProjects(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	e.tick()

	e.clock.add(30 * time.Second)
	// CPU: 300 busy of 1000 jiffies. Disk: 6000 and 3000 sectors in 30 s.
	// Network: 30000 in and 60000 out in 30 s.
	e.setHost(hostState{busy: 1300, idle: 9700, memUsed: 3 << 20, memTotal: 8 << 20,
		readSect: 6000, writeSect: 3000, netIn: 30000, netOut: 60000})
	e.setNet(101, 1000+3000, 2000+6000)
	e.setNet(102, 100+300, 200+600)
	e.setNet(201, 10+30, 20+60)
	e.tick()
	now := e.clock.now()

	srv, ok := e.server(now)
	if !ok || !near(srv.CPU, 30) || i64(srv.MemUsed) != fmt.Sprint(int64(3<<30)) || i64(srv.MemTotal) != fmt.Sprint(int64(8<<30)) ||
		!near(srv.DiskRead, 6000*512/30.0) || !near(srv.DiskWrite, 3000*512/30.0) || !near(srv.NetIn, 1000) || !near(srv.NetOut, 2000) {
		t.Fatalf("server row = cpu %s mem %s/%s disk %s/%s net %s/%s", f64(srv.CPU), i64(srv.MemUsed), i64(srv.MemTotal),
			f64(srv.DiskRead), f64(srv.DiskWrite), f64(srv.NetIn), f64(srv.NetOut))
	}
	shop, ok := e.projectRow("shop", now)
	if !ok || !near(shop.CPU, 15) || i64(shop.Mem) != fmt.Sprint(int64(150<<20)) || !near(shop.NetIn, 110) || !near(shop.NetOut, 220) {
		t.Fatalf("shop row = cpu %s mem %s net %s/%s", f64(shop.CPU), i64(shop.Mem), f64(shop.NetIn), f64(shop.NetOut))
	}
	notes, ok := e.projectRow("notes", now)
	if !ok || !near(notes.CPU, 1) || i64(notes.Mem) != fmt.Sprint(int64(10<<20)) || !near(notes.NetIn, 1) || !near(notes.NetOut, 2) {
		t.Fatalf("notes row = cpu %s mem %s net %s/%s", f64(notes.CPU), i64(notes.Mem), f64(notes.NetIn), f64(notes.NetOut))
	}
	if at := e.historyStart(e.ids["blog"]); at != nil {
		t.Fatalf("blog (missing) has samples from %s, want none", at)
	}

	tk, ok := e.s.Latest()
	if !ok || !tk.At.Equal(now) || !tk.DockerUp || tk.Server == nil || !near(tk.Server.CPU, 30) || tk.Cores != 2 {
		t.Fatalf("Latest = %+v %v", tk, ok)
	}
	if len(tk.Projects) != 2 {
		t.Fatalf("Latest has %d projects, want shop and notes", len(tk.Projects))
	}
	p := tk.Projects[e.ids["shop"]]
	if !p.Running || !near(p.CPU, 15) || i64(p.Mem) != fmt.Sprint(int64(150<<20)) || !near(p.NetIn, 110) || !near(p.NetOut, 220) {
		t.Fatalf("Latest shop = %+v", p)
	}
	if tk.SelfProject != "" || tk.Self == nil || !tk.Self.Running {
		t.Fatalf("Latest self = %q %+v, want the panel process", tk.SelfProject, tk.Self)
	}
	e.src.mu.Lock()
	ages := []time.Duration{e.src.containersAge, e.src.statsAge, e.src.inspectAge}
	e.src.mu.Unlock()
	if !slices.Equal(ages, []time.Duration{30 * time.Second, 15 * time.Second, 15 * time.Second}) {
		t.Fatalf("asked the hub for containers, stats and inspect up to %v old, want 30s, 15s, 15s", ages)
	}

	// The latest tick is valid for 90 s.
	e.clock.add(90 * time.Second)
	if _, ok := e.s.Latest(); !ok {
		t.Fatal("Latest 90 s after the tick: want it")
	}
	e.clock.add(time.Second)
	if _, ok := e.s.Latest(); ok {
		t.Fatal("Latest 91 s after the tick: want none")
	}
}

func TestSamplerReusesFreshHubStats(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	e.tick()
	e.clock.add(30 * time.Second)
	e.tick()
	if n := len(e.cmd.callsOf(runner.DockerStats)); n != 0 {
		t.Fatalf("%d docker.stats runs while the hub's read is fresh, want 0", n)
	}

	// The hub has none fresh: one read of the sampler's own.
	e.src.set(func(f *fakeSource) { f.use = nil })
	e.cmd.set(runner.DockerStats, cid("shop", "app")+"\t7.50%\t70MiB / 7.6GiB\n"+cid("shop", "db")+"\t--\t-- / --\n", nil)
	e.clock.add(30 * time.Second)
	e.tick()
	if n := len(e.cmd.callsOf(runner.DockerStats)); n != 1 {
		t.Fatalf("%d docker.stats runs with the hub's read stale, want 1", n)
	}
	shop, ok := e.projectRow("shop", e.clock.now())
	if !ok || !near(shop.CPU, 7.5) || i64(shop.Mem) != fmt.Sprint(int64(70<<20)) {
		t.Fatalf("shop row = cpu %s mem %s, want the sampler's own read", f64(shop.CPU), i64(shop.Mem))
	}
	if _, ok := e.projectRow("notes", e.clock.now()); ok {
		t.Fatal("notes has a row though its container is not in the stats")
	}

	// A failed read: no project row, the server row still written.
	e.cmd.set(runner.DockerStats, "", errors.New("docker stats failed"))
	e.clock.add(30 * time.Second)
	e.tick()
	if _, ok := e.projectRow("shop", e.clock.now()); ok {
		t.Fatal("shop has a row after a failed stats read")
	}
	if _, ok := e.server(e.clock.now()); !ok {
		t.Fatal("no server row after a failed stats read")
	}
	if tk, ok := e.s.Latest(); !ok || !tk.DockerUp || len(tk.Projects) != 0 {
		t.Fatalf("Latest after a failed stats read = %+v %v, want Docker up and no project known", tk, ok)
	}
}

// Rates divide by the time between the two readings of each counter: the
// host read at the start of a tick, a container's net/dev when it is read.
func TestSamplerRateUsesRealElapsed(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	e.tick()

	e.clock.add(45 * time.Second)
	e.setHost(hostState{busy: 1000, idle: 9000, memUsed: 2 << 20, memTotal: 8 << 20, netIn: 4500, netOut: 9000})
	e.setNet(201, 10+5000, 20+10000)
	// The container list takes 5 s this time: the net/dev reads come 50 s
	// after the last ones, the host read 45 s.
	e.src.set(func(f *fakeSource) { f.during = func() { e.clock.add(5 * time.Second) } })
	e.tick()

	tk, _ := e.s.Latest()
	srv, ok := e.server(tk.At)
	if !ok || !near(srv.NetIn, 100) || !near(srv.NetOut, 200) {
		t.Fatalf("server net = %s/%s, want 100/200 (45 s)", f64(srv.NetIn), f64(srv.NetOut))
	}
	notes, ok := e.projectRow("notes", tk.At)
	if !ok || !near(notes.NetIn, 100) || !near(notes.NetOut, 200) {
		t.Fatalf("notes net = %s/%s, want 100/200 (50 s)", f64(notes.NetIn), f64(notes.NetOut))
	}

	// A baseline too old (the panel stalled) gives a gap, not an average.
	e.src.set(func(f *fakeSource) { f.during = nil })
	e.clock.add(10 * time.Minute)
	e.setHost(hostState{busy: 1100, idle: 9900, memUsed: 2 << 20, memTotal: 8 << 20, netIn: 4500 + 60000, netOut: 9000 + 60000})
	e.setNet(201, 10+5000+60000, 20+10000+60000)
	e.tick()
	tk, _ = e.s.Latest()
	if notes, ok := e.projectRow("notes", tk.At); !ok || notes.NetIn != nil || notes.NetOut != nil {
		t.Fatalf("notes net after a 10 min stall = %s/%s, want a gap", f64(notes.NetIn), f64(notes.NetOut))
	}
	if srv, ok := e.server(tk.At); !ok || srv.CPU != nil || srv.NetIn != nil || srv.MemUsed == nil {
		t.Fatalf("server after a 10 min stall = cpu %s net %s mem %s, want rate gaps and the memory", f64(srv.CPU), f64(srv.NetIn), i64(srv.MemUsed))
	}
}

func TestSamplerContainerRestartNoSpike(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	e.setNet(201, 1_000_000, 2_000_000)
	e.tick()

	// notes' app restarted with a new pid. Its counters happen to be above the
	// old ones: still a new baseline, not a delta.
	e.src.set(func(f *fakeSource) { f.insp[cid("notes", "app")] = project.Inspect{Pid: 301, NetworkMode: "bridge"} })
	e.setNet(301, 1_000_500, 2_000_700)
	e.clock.add(30 * time.Second)
	e.tick()
	notes, ok := e.projectRow("notes", e.clock.now())
	if !ok || notes.NetIn != nil || notes.NetOut != nil || !near(notes.CPU, 1) || notes.Mem == nil {
		t.Fatalf("notes after a restart = cpu %s mem %s net %s/%s, want CPU and RAM with a network gap",
			f64(notes.CPU), i64(notes.Mem), f64(notes.NetIn), f64(notes.NetOut))
	}

	e.setNet(301, 1_000_500+3000, 2_000_700+6000)
	e.clock.add(30 * time.Second)
	e.tick()
	if notes, _ := e.projectRow("notes", e.clock.now()); !near(notes.NetIn, 100) || !near(notes.NetOut, 200) {
		t.Fatalf("notes net the tick after = %s/%s, want 100/200", f64(notes.NetIn), f64(notes.NetOut))
	}

	// Same pid, counters went back (the namespace was recreated): no spike.
	e.setNet(301, 10, 10)
	e.clock.add(30 * time.Second)
	e.tick()
	if notes, _ := e.projectRow("notes", e.clock.now()); notes.NetIn != nil || notes.NetOut != nil {
		t.Fatalf("notes net after counters went back = %s/%s, want a gap", f64(notes.NetIn), f64(notes.NetOut))
	}
}

func TestSamplerSkipsHostAndSharedNetwork(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	e.src.set(func(f *fakeSource) {
		f.insp[cid("shop", "app")] = project.Inspect{Pid: 101, NetworkMode: "host"}
		f.insp[cid("shop", "db")] = project.Inspect{Pid: 102, NetworkMode: "container:" + cid("shop", "app")}
		f.insp[cid("notes", "app")] = project.Inspect{Pid: 0, NetworkMode: "bridge"} // restarting
	})
	e.tick()
	e.setNet(101, 1000+3000, 2000+6000)
	e.setNet(102, 100+300, 200+600)
	e.setNet(201, 10+30, 20+60)
	e.clock.add(30 * time.Second)
	e.tick()
	for _, name := range []string{"shop", "notes"} {
		r, ok := e.projectRow(name, e.clock.now())
		if !ok || r.NetIn != nil || r.NetOut != nil || r.CPU == nil {
			t.Fatalf("%s = cpu %s net %s/%s, want CPU and no network", name, f64(r.CPU), f64(r.NetIn), f64(r.NetOut))
		}
	}
}

func TestSamplerInspectsIdsMissingFromHub(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	panel := panelCtr()
	e.src.set(func(f *fakeSource) {
		f.cs = append(f.cs, panel)
		f.use[panel.ID] = project.Usage{CPUPercent: 2, MemBytes: 64 << 20}
	})
	e.cmd.set(runner.DockerInspect, panel.ID+"\t0\tscp_default\t{\"Pid\":401}\n", nil)
	e.setNet(401, 0, 0)
	e.tick()

	calls := e.cmd.callsOf(runner.DockerInspect)
	if len(calls) != 1 || calls[0].Values["ids"] != panel.ID || len(calls[0].Values) != 1 {
		t.Fatalf("inspect calls = %+v, want one with the panel container only", calls)
	}

	// The hub's map is stale: one call with every running id the sampler
	// needs (the stranger's is not one).
	e.src.set(func(f *fakeSource) { f.insp = nil })
	e.clock.add(30 * time.Second)
	e.tick()
	calls = e.cmd.callsOf(runner.DockerInspect)
	if len(calls) != 2 {
		t.Fatalf("%d inspect calls, want 2", len(calls))
	}
	got := strings.Split(calls[1].Values["ids"], " ")
	want := []string{cid("shop", "app"), cid("shop", "db"), cid("notes", "app"), panel.ID}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("inspect ids = %v, want %v", got, want)
	}

	// Every id fresh in the hub's map: no call.
	e.src.set(func(f *fakeSource) {
		f.insp = map[string]project.Inspect{
			cid("shop", "app"):  {Pid: 101, NetworkMode: "bridge"},
			cid("shop", "db"):   {Pid: 102, NetworkMode: "bridge"},
			cid("notes", "app"): {Pid: 201, NetworkMode: "bridge"},
			panel.ID:            {Pid: 401, NetworkMode: "bridge"},
		}
	})
	e.clock.add(30 * time.Second)
	e.tick()
	if n := len(e.cmd.callsOf(runner.DockerInspect)); n != 2 {
		t.Fatalf("%d inspect calls with every id fresh in the hub, want still 2", n)
	}
}

func TestSamplerStoppedProjectNoRow(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	stopped := ctr("notes", "app")
	stopped.State = "exited"
	e.src.set(func(f *fakeSource) {
		f.cs = []project.Container{ctr("shop", "app"), ctr("shop", "db"), stopped}
		delete(f.use, cid("notes", "app"))
	})
	e.tick()
	e.clock.add(30 * time.Second)
	e.tick()
	if _, ok := e.projectRow("notes", e.clock.now()); ok {
		t.Fatal("notes (stopped) has a row, want a gap")
	}
	if _, ok := e.projectRow("shop", e.clock.now()); !ok {
		t.Fatal("shop has no row")
	}
	tk, ok := e.s.Latest()
	p, known := tk.Projects[e.ids["notes"]]
	if !ok || !known || p.Running || !near(p.CPU, 0) || i64(p.Mem) != "0" {
		t.Fatalf("Latest notes = %+v (known %v), want not running with zero CPU and RAM", p, known)
	}
}

func TestSamplerDockerDownGapsProjects(t *testing.T) {
	e := newSamplerEnv(t)
	e.standardScene()
	e.src.set(func(f *fakeSource) { f.up = false })
	e.tick()
	e.setHost(hostState{busy: 1300, idle: 9700, memUsed: 3 << 20, memTotal: 8 << 20})
	e.clock.add(30 * time.Second)
	e.tick()
	if srv, ok := e.server(e.clock.now()); !ok || !near(srv.CPU, 30) {
		t.Fatalf("server row with Docker down = cpu %s, want 30", f64(srv.CPU))
	}
	for _, name := range []string{"shop", "notes"} {
		if at := e.historyStart(e.ids[name]); at != nil {
			t.Fatalf("%s has samples from %s with Docker down, want none", name, at)
		}
	}
	tk, ok := e.s.Latest()
	if !ok || tk.DockerUp || len(tk.Projects) != 0 || tk.Self != nil {
		t.Fatalf("Latest with Docker down = %+v %v, want Docker down and nothing known", tk, ok)
	}
	if n := e.cmd.total(); n != 0 {
		t.Fatalf("%d commands with Docker down, want 0", n)
	}
}

func TestSamplerSelfRow(t *testing.T) {
	t.Run("from the panel container", func(t *testing.T) {
		e := newSamplerEnv(t)
		e.standardScene()
		panel := panelCtr()
		e.src.set(func(f *fakeSource) {
			f.cs = append(f.cs, panel)
			f.use[panel.ID] = project.Usage{CPUPercent: 2, MemBytes: 64 << 20}
			f.insp[panel.ID] = project.Inspect{Pid: 401, NetworkMode: "scp_default"}
		})
		e.setNet(401, 0, 0)
		e.tick()
		e.setNet(401, 300, 600)
		e.clock.add(30 * time.Second)
		e.tick()
		tk, _ := e.s.Latest()
		if tk.SelfProject != "scp" || tk.Self == nil || !tk.Self.Running || !near(tk.Self.CPU, 2) ||
			i64(tk.Self.Mem) != fmt.Sprint(int64(64<<20)) || !near(tk.Self.NetIn, 10) || !near(tk.Self.NetOut, 20) {
			t.Fatalf("self = %q %+v, want the panel container's usage", tk.SelfProject, tk.Self)
		}
		if len(tk.Projects) != 2 {
			t.Fatalf("Latest has %d projects, want shop and notes only", len(tk.Projects))
		}
	})

	t.Run("tracked panel project", func(t *testing.T) {
		e := newSamplerEnv(t)
		e.standardScene()
		// shop holds the panel container: it is the panel's row.
		app := ctr("shop", "app")
		app.PanelImage = true
		e.src.set(func(f *fakeSource) { f.cs[0] = app })
		e.tick()
		e.clock.add(30 * time.Second)
		e.tick()
		tk, _ := e.s.Latest()
		if tk.SelfProject != "shop" || tk.Self != nil || !tk.Projects[e.ids["shop"]].Running {
			t.Fatalf("self = %q %+v, want the tracked shop as the panel's row", tk.SelfProject, tk.Self)
		}
		if _, ok := e.projectRow("shop", e.clock.now()); !ok {
			t.Fatal("the tracked panel project has no row")
		}
	})

	t.Run("from the panel process", func(t *testing.T) {
		e := newSamplerEnv(t)
		e.standardScene()
		e.tick()
		// 600 ticks of 1/100 s in 30 s: 20 % of a core.
		e.setSelf(1000+600, 32768)
		e.clock.add(30 * time.Second)
		e.tick()
		tk, _ := e.s.Latest()
		if tk.SelfProject != "" || tk.Self == nil || !tk.Self.Running || !near(tk.Self.CPU, 20) ||
			i64(tk.Self.Mem) != fmt.Sprint(int64(32<<20)) || tk.Self.NetIn != nil || tk.Self.NetOut != nil {
			t.Fatalf("self = %q %+v, want the panel process's usage", tk.SelfProject, tk.Self)
		}
	})
}

// recordMaintenance replaces the sampler's rollup and prune with recorders
// that call the store's own, and returns the log.
func (e *samplerEnv) recordMaintenance(rollupErr *error) *[]string {
	var mu sync.Mutex
	var log []string
	rollup, prune := e.s.rollup, e.s.pruneMetrics
	e.s.rollup = func(ctx context.Context, now time.Time) error {
		mu.Lock()
		log = append(log, "rollup "+now.Format("15:04:05"))
		mu.Unlock()
		if rollupErr != nil && *rollupErr != nil {
			return *rollupErr
		}
		return rollup(ctx, now)
	}
	e.s.pruneMetrics = func(ctx context.Context, now time.Time) error {
		mu.Lock()
		log = append(log, "prune "+now.Format("15:04:05"))
		mu.Unlock()
		return prune(ctx, now)
	}
	return &log
}

func TestSamplerRollupOnBoundaryOnly(t *testing.T) {
	e := newSamplerEnv(t)
	log := e.recordMaintenance(nil)
	e.tick() // 10:00:10, at start: roll up, then prune
	for range 9 {
		e.clock.add(30 * time.Second)
		e.tick() // up to 10:04:40, no boundary passed
	}
	e.clock.add(30 * time.Second)
	e.tick() // 10:05:10
	e.clock.add(30 * time.Second)
	e.tick() // 10:05:40
	want := []string{"rollup 10:00:10", "prune 10:00:10", "rollup 10:05:10"}
	if !slices.Equal(*log, want) {
		t.Fatalf("maintenance = %v, want %v", *log, want)
	}
}

func TestSamplerWithoutHostProc(t *testing.T) {
	e := newSamplerEnv(t)
	e.s.host = Host{Proc: filepath.Join(t.TempDir(), "absent")}
	if e.s.host.Available() {
		t.Fatal("the host is available")
	}
	e.standardScene()
	e.tick()
	e.clock.add(30 * time.Second)
	e.tick()
	if at := e.historyStart(0); at != nil {
		t.Fatalf("server samples from %s without the host's /proc, want none", at)
	}
	shop, ok := e.projectRow("shop", e.clock.now())
	if !ok || !near(shop.CPU, 15) || shop.Mem == nil || shop.NetIn != nil || shop.NetOut != nil {
		t.Fatalf("shop = cpu %s mem %s net %s/%s, want CPU and RAM without network", f64(shop.CPU), i64(shop.Mem), f64(shop.NetIn), f64(shop.NetOut))
	}
	tk, ok := e.s.Latest()
	if !ok || tk.Server != nil || tk.Cores < 1 {
		t.Fatalf("Latest = %+v %v, want no server sample and a core count", tk, ok)
	}
}

func TestSamplerPrunesHourly(t *testing.T) {
	e := newSamplerEnv(t)
	e.src.set(func(f *fakeSource) { f.up = false })
	var rollupErr error
	log := e.recordMaintenance(&rollupErr)
	e.tick() // 10:00:10
	for range 119 {
		e.clock.add(30 * time.Second)
		e.tick() // up to 10:59:40
	}
	prunes := func() []string {
		var out []string
		for i, l := range *log {
			if strings.HasPrefix(l, "prune") {
				out = append(out, (*log)[i-1]+" / "+l)
			}
		}
		return out
	}
	if got := prunes(); !slices.Equal(got, []string{"rollup 10:00:10 / prune 10:00:10"}) {
		t.Fatalf("prunes in the first hour = %v, want one at start, after a rollup", got)
	}

	// The hourly prune is due, but the rollup fails: no prune until one works.
	rollupErr = errors.New("database is locked")
	e.clock.add(30 * time.Second)
	e.tick() // 11:00:10
	if got := prunes(); len(got) != 1 {
		t.Fatalf("prunes after a failed rollup = %v, want still one", got)
	}
	rollupErr = nil
	e.clock.add(30 * time.Second)
	e.tick() // 11:00:40
	want := []string{"rollup 10:00:10 / prune 10:00:10", "rollup 11:00:40 / prune 11:00:40"}
	if got := prunes(); !slices.Equal(got, want) {
		t.Fatalf("prunes = %v, want %v", got, want)
	}

	// Prune (after a retention save) rolls up, then prunes now.
	old := e.clock.now().Add(-26 * time.Hour)
	mem := int64(1)
	if err := e.st.InsertSamples(context.Background(), &store.ServerSample{TS: old, MemUsed: &mem}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := (*log)[len(*log)-2:]; !slices.Equal(got, []string{"rollup 11:00:40", "prune 11:00:40"}) {
		t.Fatalf("Prune ran %v, want a rollup then a prune", got)
	}
	if at := e.historyStart(0); at != nil && !at.After(old) {
		t.Fatalf("server history starts %s after Prune, want the 26-hour-old row gone", at)
	}
}

func TestSamplerCloseStopsLoop(t *testing.T) {
	e := newSamplerEnv(t)
	e.s.interval = 10 * time.Millisecond
	e.s.now = time.Now
	e.standardScene()
	e.src.set(func(f *fakeSource) { f.use = nil }) // every tick runs docker.stats
	e.cmd.set(runner.DockerStats, "", nil)
	e.s.Start(context.Background())
	waitFor(t, func() bool { return len(e.cmd.callsOf(runner.DockerStats)) >= 3 })

	// A tick held in docker.stats is cancelled by Close, which waits for it.
	e.cmd.mu.Lock()
	e.cmd.block = true
	e.cmd.mu.Unlock()
	waitFor(t, func() bool {
		e.cmd.mu.Lock()
		defer e.cmd.mu.Unlock()
		return e.cmd.blocked > 0
	})
	closed := make(chan struct{})
	go func() {
		e.s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	n := e.cmd.total()
	time.Sleep(50 * time.Millisecond)
	if got := e.cmd.total(); got != n {
		t.Fatalf("%d commands after Close, want 0", got-n)
	}

	e.s.Close() // a second Close does nothing
	e.s.Start(context.Background())
	time.Sleep(30 * time.Millisecond)
	if got := e.cmd.total(); got != n {
		t.Fatalf("%d commands after a Start after Close, want 0", got-n)
	}
}

// Ticks begin on the interval grid and never overlap: a tick longer than the
// interval skips the starts it missed.
func TestSamplerLoopStartToStart(t *testing.T) {
	e := newSamplerEnv(t)
	interval, slow := 30*time.Millisecond, 70*time.Millisecond
	e.s.interval = interval
	e.s.now = time.Now
	e.src.set(func(f *fakeSource) { f.during = func() { time.Sleep(slow) } })
	e.s.Start(context.Background())
	waitFor(t, func() bool {
		e.src.mu.Lock()
		defer e.src.mu.Unlock()
		return len(e.src.starts) >= 4
	})
	e.s.Close()

	e.src.mu.Lock()
	defer e.src.mu.Unlock()
	if e.src.peak != 1 {
		t.Fatalf("%d ticks at once, want 1", e.src.peak)
	}
	slack := 5 * time.Millisecond
	for i := 1; i < len(e.src.starts); i++ {
		// The next start after a 70 ms tick is the third interval, 90 ms on.
		if gap := e.src.starts[i].Sub(e.src.starts[i-1]); gap < 3*interval-slack {
			t.Fatalf("ticks %d and %d began %s apart, want at least %s", i-1, i, gap, 3*interval)
		}
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}
