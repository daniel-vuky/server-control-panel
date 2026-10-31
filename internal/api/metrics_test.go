package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// fakeMonitor is the server metrics of the API tests: it answers the tick,
// the disk usage, the disks and the system facts it was given, and Prune
// counts its calls and keeps the last call's context.
type fakeMonitor struct {
	mu       sync.Mutex
	tick     metrics.Tick
	haveTick bool
	disk     map[int64]metrics.DiskUsage
	disks    []metrics.Disk
	fallback bool
	sys      metrics.System
	load     [3]float64
	loadOK   bool
	boot     time.Time
	bootOK   bool
	prunes   int
	pruneCtx context.Context
}

func (f *fakeMonitor) Latest() (metrics.Tick, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tick, f.haveTick
}

func (f *fakeMonitor) Disk() map[int64]metrics.DiskUsage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]metrics.DiskUsage{}
	for id, u := range f.disk {
		out[id] = u
	}
	return out
}

func (f *fakeMonitor) Disks() ([]metrics.Disk, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.disks, f.fallback
}

func (f *fakeMonitor) System(context.Context) metrics.System {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sys
}

func (f *fakeMonitor) Load() (one, five, fifteen float64, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load[0], f.load[1], f.load[2], f.loadOK
}

func (f *fakeMonitor) BootTime() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.boot, f.bootOK
}

func (f *fakeMonitor) Prune(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prunes++
	f.pruneCtx = ctx
	return nil
}

func (f *fakeMonitor) Thresholds() metrics.Thresholds { return metrics.DefaultThresholds }

// set runs fn with the fake locked, to change its answers.
func (f *fakeMonitor) set(fn func(f *fakeMonitor)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// monitorOf returns the fakeMonitor of deps from newTestDeps.
func monitorOf(d Deps) *fakeMonitor { return d.Metrics.(*fakeMonitor) }

// metricsNow is the clock of the metrics tests: 10 s into a 30-second slot,
// on a whole half hour, so the last bucket of every range starts at 10:00.
var metricsNow = time.Date(2030, 1, 15, 10, 0, 10, 0, time.UTC)

// metricsSetup is alpha and beta (addProjects: beta is the panel's own
// project) with a router in Asia/Ho_Chi_Minh whose clock reads metricsNow.
func metricsSetup(t *testing.T) (Deps, http.Handler) {
	t.Helper()
	d := newTestDeps(t)
	d.Zone, d.Now = mustZone(t, "Asia/Ho_Chi_Minh"), fixedClock(metricsNow)
	addProjects(t, d)
	return d, NewRouter(d)
}

// zoned is t in RFC 3339 in Asia/Ho_Chi_Minh.
func zoned(t *testing.T, at time.Time) string {
	t.Helper()
	return at.In(mustZone(t, "Asia/Ho_Chi_Minh")).Format(time.RFC3339)
}

func TestServerInfo(t *testing.T) {
	d, h := metricsSetup(t)
	monitorOf(d).set(func(f *fakeMonitor) {
		f.sys = metrics.System{
			Available: true, Hostname: "srv-1", OS: "Ubuntu 24.04.1 LTS", Kernel: "6.8.0-45-generic",
			Cores: 4, RAMBytes: 8 << 30, Docker: metrics.DockerRunning, DockerVersion: "29.3.0", ContainersRunning: 7,
			Tunnel: metrics.Tunnel{State: metrics.TunnelConnected, Hostnames: ptr(5)},
			Disks:  []metrics.Disk{{Mount: "/", Used: 40 << 30, Total: 100 << 30}},
		}
		f.load, f.loadOK = [3]float64{1.36, 1.12, 0.98}, true
		f.boot, f.bootOK = time.Date(2030, 1, 10, 3, 0, 0, 0, time.UTC), true
	})

	rec := do(h, http.MethodGet, "/api/server")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/server = %d %s", rec.Code, rec.Body.String())
	}
	want := ServerInfo{
		Available: true, Hostname: "srv-1", OS: "Ubuntu 24.04.1 LTS", Kernel: "6.8.0-45-generic",
		Cores: 4, RAMBytes: 8 << 30,
		Docker:        DockerService{State: "running", Version: "29.3.0", ContainersRunning: 7},
		Cloudflared:   TunnelService{State: "connected", Hostnames: ptr(5)},
		Disks:         []DiskInfo{{Mount: "/", UsedBytes: 40 << 30, TotalBytes: 100 << 30}},
		Thresholds:    Thresholds{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85},
		SampleSeconds: 30, RetentionDays: 7,
		TimeZone: TimeZone{Name: "Asia/Ho_Chi_Minh", Offset: "+07:00"},
		Load:     &LoadAverage{One: 1.36, Five: 1.12, Fifteen: 0.98},
		BootTime: ptr("2030-01-10T10:00:00+07:00"),
	}
	if got := decode[ServerInfo](t, rec); !reflect.DeepEqual(got, want) {
		t.Errorf("server info = %+v,\nwant %+v", got, want)
	}

	// Without the host's /proc: not available, with the words; Docker not
	// installed, no tunnel config and the fallback disk row still show.
	monitorOf(d).set(func(f *fakeMonitor) {
		f.sys = metrics.System{
			Docker: metrics.DockerNotInstalled, Tunnel: metrics.Tunnel{State: metrics.TunnelNotRunning},
			Disks: []metrics.Disk{{Mount: "/ (panel data)", Used: 1, Total: 2}}, DisksFallback: true,
		}
	})
	got := decode[ServerInfo](t, do(h, http.MethodGet, "/api/server"))
	if got.Available || got.Reason != "Server metrics are not available: the host's /proc is not mounted." {
		t.Errorf("available = %v, reason = %q", got.Available, got.Reason)
	}
	if got.Docker.State != "not_installed" || got.Cloudflared != (TunnelService{State: "not_running"}) {
		t.Errorf("docker = %+v, cloudflared = %+v", got.Docker, got.Cloudflared)
	}
	if !got.DisksPartial || len(got.Disks) != 1 || got.Disks[0].Mount != "/ (panel data)" {
		t.Errorf("disks = %+v, partial = %v", got.Disks, got.DisksPartial)
	}

	// A load or boot time that cannot be read is JSON null, the keys present.
	monitorOf(d).set(func(f *fakeMonitor) { f.loadOK, f.bootOK = false, false })
	body := do(h, http.MethodGet, "/api/server").Body.String()
	if !strings.Contains(body, `"load":null`) || !strings.Contains(body, `"bootTime":null`) {
		t.Errorf("body = %s, want load and bootTime null", body)
	}

	// No disk at all is an empty list, not null.
	monitorOf(d).set(func(f *fakeMonitor) { f.sys = metrics.System{Docker: metrics.DockerNotResponding} })
	rec = do(h, http.MethodGet, "/api/server")
	if body := rec.Body.String(); !strings.Contains(body, `"disks":[]`) {
		t.Errorf("body = %s, want an empty disks list", body)
	}
}

// jsonOf is v as JSON, for failure messages.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// serverSample is a raw server sample at at with CPU cpu and 1 GiB used.
func serverSample(at time.Time, cpu float64) *store.ServerSample {
	used, total := int64(1<<30), int64(8<<30)
	load, du, dt, tun := 1.5, int64(40<<30), int64(100<<30), 1.0
	return &store.ServerSample{TS: at, CPU: &cpu, MemUsed: &used, MemTotal: &total,
		Load1: &load, DiskUsed: &du, DiskTotal: &dt, Tunnel: &tun}
}

func insertSample(t *testing.T, d Deps, srv *store.ServerSample, ps ...store.ProjectSample) {
	t.Helper()
	if err := d.Store.InsertSamples(context.Background(), srv, ps); err != nil {
		t.Fatal(err)
	}
}

func serverSeries(t *testing.T, h http.Handler, rng string) ServerSeries {
	t.Helper()
	rec := do(h, http.MethodGet, "/api/server/metrics?range="+rng)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET server metrics %s = %d %s", rng, rec.Code, rec.Body.String())
	}
	return decode[ServerSeries](t, rec)
}

func TestServerMetricsRanges(t *testing.T) {
	d, h := metricsSetup(t)
	last := time.Date(2030, 1, 15, 10, 0, 0, 0, time.UTC) // the bucket containing now, in every range
	old := last.Add(-72 * time.Hour)
	insertSample(t, d, serverSample(old, 50))
	insertSample(t, d, serverSample(last.Add(-60*time.Second), 10))
	insertSample(t, d, serverSample(last.Add(-30*time.Second), 20))

	for _, c := range []struct {
		rng      string
		step     time.Duration
		n        int
		values   map[int]float64 // index -> cpu; every other point is a gap
		withOldT bool
	}{
		{"1h", 30 * time.Second, 120, map[int]float64{117: 10, 118: 20}, false},
		{"24h", 5 * time.Minute, 288, map[int]float64{286: 15}, false},
		{"7d", 30 * time.Minute, 336, map[int]float64{191: 50, 334: 15}, true},
	} {
		t.Run(c.rng, func(t *testing.T) {
			s := serverSeries(t, h, c.rng)
			from := last.Add(-time.Duration(c.n-1) * c.step)
			if s.Range != c.rng || s.StepSeconds != int(c.step/time.Second) || len(s.Points) != c.n {
				t.Fatalf("range %q step %d points %d", s.Range, s.StepSeconds, len(s.Points))
			}
			if s.From != zoned(t, from) || s.To != zoned(t, last) {
				t.Errorf("from %s to %s, want %s and %s", s.From, s.To, zoned(t, from), zoned(t, last))
			}
			if s.HistoryStart == nil || *s.HistoryStart != zoned(t, old) {
				t.Errorf("historyStart = %v, want %s", s.HistoryStart, zoned(t, old))
			}
			for i, p := range s.Points {
				if want := zoned(t, from.Add(time.Duration(i)*c.step)); p.T != want {
					t.Fatalf("point %d at %s, want %s", i, p.T, want)
				}
				want, ok := c.values[i]
				switch {
				case ok && (p.CPU == nil || *p.CPU != want):
					t.Errorf("point %d cpu = %v, want %v", i, p.CPU, want)
				case ok && (p.MemUsed == nil || *p.MemUsed != 1<<30):
					t.Errorf("point %d memUsed = %v, want 1 GiB", i, p.MemUsed)
				case !ok && (p.CPU != nil || p.MemUsed != nil || p.NetIn != nil):
					t.Errorf("point %d = %+v, want a gap", i, p)
				}
			}
		})
	}

	// Latest is the tick's server sample while it is at most 90 s old.
	at := metricsNow.Add(-20 * time.Second)
	monitorOf(d).set(func(f *fakeMonitor) {
		f.tick, f.haveTick = metrics.Tick{At: at, DockerUp: true, Cores: 4, Server: serverSample(at, 33)}, true
	})
	s := serverSeries(t, h, "1h")
	if s.Latest == nil || s.Latest.At != zoned(t, at) || s.Latest.CPU == nil || *s.Latest.CPU != 33 || *s.Latest.MemTotal != 8<<30 {
		t.Errorf("latest = %+v, want the tick's sample at %s", s.Latest, zoned(t, at))
	}
	if l := s.Latest; l.Load1 == nil || *l.Load1 != 1.5 || l.DiskUsed == nil || *l.DiskUsed != 40<<30 ||
		l.DiskTotal == nil || *l.DiskTotal != 100<<30 || l.Tunnel == nil || *l.Tunnel != 1 {
		t.Errorf("latest = %+v, want load1 1.5, disk 40 of 100 GiB, tunnel 1", l)
	}
	if p := s.Points[118]; p.Load1 == nil || *p.Load1 != 1.5 || p.DiskUsed == nil || *p.DiskUsed != 40<<30 ||
		p.DiskTotal == nil || *p.DiskTotal != 100<<30 || p.Tunnel == nil || *p.Tunnel != 1 {
		t.Errorf("point 118 = %+v, want load1 1.5, disk 40 of 100 GiB, tunnel 1", p)
	}
	if p := s.Points[0]; p.Load1 != nil || p.DiskUsed != nil || p.DiskTotal != nil || p.Tunnel != nil {
		t.Errorf("point 0 = %+v, want a gap", p)
	}
	monitorOf(d).set(func(f *fakeMonitor) { f.tick.At = metricsNow.Add(-91 * time.Second) })
	if s := serverSeries(t, h, "1h"); s.Latest != nil {
		t.Errorf("latest = %+v, want null for a tick older than 90 s", s.Latest)
	}
	monitorOf(d).set(func(f *fakeMonitor) { f.tick.At, f.tick.Server = at, nil })
	if s := serverSeries(t, h, "1h"); s.Latest != nil {
		t.Errorf("latest = %+v, want null without a server sample", s.Latest)
	}
}

func TestServerMetricsNoHistory(t *testing.T) {
	_, h := metricsSetup(t)
	s := serverSeries(t, h, "24h")
	if s.HistoryStart != nil || s.Latest != nil || len(s.Points) != 288 {
		t.Errorf("historyStart %v, latest %v, %d points", s.HistoryStart, s.Latest, len(s.Points))
	}
}

func TestServerMetricsBadRange(t *testing.T) {
	d, h := metricsSetup(t)
	alpha, _ := trackedIDs(t, d)
	for _, path := range []string{
		"/api/server/metrics", "/api/server/metrics?range=", "/api/server/metrics?range=2h",
		"/api/server/metrics?range=1H", "/api/projects/" + strconv.FormatInt(alpha, 10) + "/metrics?range=30d",
	} {
		rec := do(h, http.MethodGet, path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
			continue
		}
		if got := decode[QueryError](t, rec); got.Field != "range" || got.Error == "" {
			t.Errorf("GET %s body = %+v, want field range", path, got)
		}
	}
}

func projectMetricsURL(id int64, rng string) string {
	return "/api/projects/" + strconv.FormatInt(id, 10) + "/metrics?range=" + rng
}

func TestProjectMetricsRouteWinsOverAction(t *testing.T) {
	d, h := metricsSetup(t)
	alpha, beta := trackedIDs(t, d)
	last := time.Date(2030, 1, 15, 10, 0, 0, 0, time.UTC)
	cpu, mem, in, out := 150.0, int64(256<<20), 1000.0, 200.0
	first := last.Add(-30 * time.Second)
	insertSample(t, d, nil,
		store.ProjectSample{ProjectID: alpha, TS: first, CPU: &cpu, Mem: &mem, NetIn: &in, NetOut: &out},
		store.ProjectSample{ProjectID: beta, TS: last.Add(-time.Hour), CPU: &cpu, Mem: &mem})
	at := metricsNow.Add(-5 * time.Second)
	monitorOf(d).set(func(f *fakeMonitor) {
		f.tick, f.haveTick = metrics.Tick{At: at, DockerUp: true, Cores: 4, Projects: map[int64]metrics.ProjectNow{
			alpha: {CPU: &cpu, Mem: &mem, NetIn: &in, NetOut: &out, Running: true},
			beta:  {CPU: ptr(0.0), Mem: ptr(int64(0)), NetIn: ptr(0.0), NetOut: ptr(0.0)},
		}}, true
	})

	rec := do(h, http.MethodGet, projectMetricsURL(alpha, "1h"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET project metrics = %d %s", rec.Code, rec.Body.String())
	}
	s := decode[ProjectSeries](t, rec)
	if s.Range != "1h" || s.StepSeconds != 30 || len(s.Points) != 120 {
		t.Fatalf("range %q step %d points %d", s.Range, s.StepSeconds, len(s.Points))
	}
	if p := s.Points[118]; p.T != zoned(t, first) || p.CPU == nil || *p.CPU != 150 || *p.Mem != 256<<20 || *p.NetIn != 1000 || *p.NetOut != 200 {
		t.Errorf("point 118 = %+v", p)
	}
	if p := s.Points[119]; p.CPU != nil || p.Mem != nil || p.NetIn != nil || p.NetOut != nil {
		t.Errorf("point 119 = %+v, want a gap", p)
	}
	if s.HistoryStart == nil || *s.HistoryStart != zoned(t, first) {
		t.Errorf("historyStart = %v, want alpha's own oldest sample %s", s.HistoryStart, zoned(t, first))
	}
	if s.Latest == nil || s.Latest.At != zoned(t, at) || *s.Latest.NetIn != 1000 || *s.Latest.CPU != 150 {
		t.Errorf("latest = %+v", s.Latest)
	}
	// A project with nothing running has no latest sample.
	if s := decode[ProjectSeries](t, do(h, http.MethodGet, projectMetricsURL(beta, "24h"))); s.Latest != nil || len(s.Points) != 288 {
		t.Errorf("beta latest = %+v, %d points", s.Latest, len(s.Points))
	}

	// metrics is never a lifecycle action.
	before := dockerOf(d).callCount()
	rec = doJSON(h, http.MethodPost, "/api/projects/"+strconv.FormatInt(alpha, 10)+"/metrics", map[string]any{})
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST metrics = %d, want 404", rec.Code)
	}
	if n := dockerOf(d).callCount(); n != before {
		t.Errorf("POST metrics ran %d Docker commands", n-before)
	}
}

func TestProjectMetrics404(t *testing.T) {
	d, h := metricsSetup(t)
	_, beta := trackedIDs(t, d)
	// An ignored project has no metrics either.
	if _, err := d.Store.UntrackProject(context.Background(), beta); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{projectMetricsURL(999, "1h"), projectMetricsURL(beta, "1h"), "/api/projects/abc/metrics?range=1h"} {
		if rec := do(h, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func usage(t *testing.T, h http.Handler) UsageReport {
	t.Helper()
	rec := do(h, http.MethodGet, "/api/server/usage")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET usage = %d %s", rec.Code, rec.Body.String())
	}
	return decode[UsageReport](t, rec)
}

func TestUsageReport(t *testing.T) {
	d, h := metricsSetup(t)
	alpha, beta := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	if _, ok := d.Hub.RecentContainers(context.Background(), time.Minute); !ok {
		t.Fatal("hub read failed")
	}
	at := metricsNow.Add(-10 * time.Second)
	diskAt := metricsNow.Add(-time.Hour)
	srv := serverSample(at, 40)
	srv.NetIn, srv.NetOut = ptr(1000.0), ptr(500.0)
	monitorOf(d).set(func(f *fakeMonitor) {
		f.tick, f.haveTick = metrics.Tick{At: at, DockerUp: true, Server: srv, Cores: 4, Projects: map[int64]metrics.ProjectNow{
			alpha: {CPU: ptr(80.0), Mem: ptr(int64(512 << 20)), NetIn: ptr(100.0), NetOut: ptr(50.0), Running: true},
			beta:  {CPU: ptr(0.0), Mem: ptr(int64(0)), NetIn: ptr(0.0), NetOut: ptr(0.0)},
		}}, true
		f.disk = map[int64]metrics.DiskUsage{alpha: {Volumes: 1000, Folder: 500, Partial: true, At: diskAt}}
		f.disks = []metrics.Disk{{Mount: "/", Used: 300, Total: 1000}, {Mount: "/data", Used: 200, Total: 1000}}
	})

	got := usage(t, h)
	want := UsageReport{
		At:     ptr(zoned(t, at)),
		Totals: UsageTotals{CPUPercent: ptr(40.0), MemBytes: ptr(int64(1 << 30)), DiskUsedBytes: ptr(uint64(500)), NetBps: ptr(1500.0)},
		Rows: []UsageRow{
			{
				ProjectID: ptr(alpha), Name: "Alpha", Status: ptr("running"), CPUPercent: ptr(20.0), MemBytes: ptr(int64(512 << 20)),
				NetBps: ptr(150.0), DiskBytes: ptr(int64(1500)), DiskPartial: true, DiskAt: ptr(zoned(t, diskAt)),
			},
			// Beta is the panel's own tracked project: its row is the
			// panel's, and nothing runs, so it uses 0.
			{
				ProjectID: ptr(beta), Name: "Beta", Status: ptr("stopped"), Self: true,
				CPUPercent: ptr(0.0), MemBytes: ptr(int64(0)), NetBps: ptr(0.0),
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("usage = %s,\nwant %s", jsonOf(t, got), jsonOf(t, want))
	}

	// The panel's project untracked: a Server Control Panel row from the
	// tick's Self, with no project, status or disk.
	if _, err := d.Store.UntrackProject(context.Background(), beta); err != nil {
		t.Fatal(err)
	}
	monitorOf(d).set(func(f *fakeMonitor) {
		delete(f.tick.Projects, beta)
		f.tick.Self = &metrics.ProjectNow{CPU: ptr(8.0), Mem: ptr(int64(100 << 20)), Running: true}
	})
	got = usage(t, h)
	if len(got.Rows) != 2 || got.Rows[0].Name != "Alpha" {
		t.Fatalf("rows = %s", jsonOf(t, got.Rows))
	}
	panel := UsageRow{Name: "Server Control Panel", Self: true, CPUPercent: ptr(2.0), MemBytes: ptr(int64(100 << 20))}
	if !reflect.DeepEqual(got.Rows[1], panel) {
		t.Errorf("panel row = %s, want %s", jsonOf(t, got.Rows[1]), jsonOf(t, panel))
	}

	// Docker down: CPU, RAM and network are unknown; the disk is still known.
	monitorOf(d).set(func(f *fakeMonitor) {
		f.tick.DockerUp, f.tick.Projects, f.tick.Self = false, map[int64]metrics.ProjectNow{}, nil
	})
	got = usage(t, h)
	a := got.Rows[0]
	if a.CPUPercent != nil || a.MemBytes != nil || a.NetBps != nil || a.DiskBytes == nil || *a.DiskBytes != 1500 {
		t.Errorf("alpha with Docker down = %s", jsonOf(t, a))
	}
	if p := got.Rows[1]; p.CPUPercent != nil || p.MemBytes != nil || p.NetBps != nil {
		t.Errorf("panel row with Docker down = %s", jsonOf(t, p))
	}
	if got.Totals.CPUPercent == nil || *got.Totals.CPUPercent != 40 {
		t.Errorf("totals with Docker down = %s, want the server's", jsonOf(t, got.Totals))
	}

	// No tick in the last 90 s: every live value is unknown.
	monitorOf(d).set(func(f *fakeMonitor) { f.tick.At = metricsNow.Add(-2 * time.Minute) })
	got = usage(t, h)
	if got.At != nil || got.Totals.CPUPercent != nil || got.Totals.MemBytes != nil || got.Totals.NetBps != nil {
		t.Errorf("stale usage = %s", jsonOf(t, got))
	}
	if got.Totals.DiskUsedBytes == nil || *got.Totals.DiskUsedBytes != 500 {
		t.Errorf("disk total = %v, want 500 from the disks", got.Totals.DiskUsedBytes)
	}
}

func metricsSettings(t *testing.T, h http.Handler) int {
	t.Helper()
	rec := do(h, http.MethodGet, "/api/metrics-settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET metrics settings = %d %s", rec.Code, rec.Body.String())
	}
	return decode[MetricsSettings](t, rec).RetentionDays
}

func TestMetricsSettingsValidation(t *testing.T) {
	d, h := metricsSetup(t)
	for _, body := range []string{
		`{"retentionDays":6}`, `{"retentionDays":31}`, `{"retentionDays":7.5}`, `{"retentionDays":"7"}`,
		`{"retentionDays":null}`, `{}`,
	} {
		rec := doJSON(h, http.MethodPost, "/api/metrics-settings", body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("POST %s = %d %s, want 422", body, rec.Code, rec.Body.String())
			continue
		}
		if fe := decode[FieldError](t, rec); fe.Field != "retentionDays" || fe.Code != "range" || fe.Error == "" {
			t.Errorf("POST %s body = %+v", body, fe)
		}
	}
	if rec := doJSON(h, http.MethodPost, "/api/metrics-settings", `{"retentionDays":`); rec.Code != http.StatusBadRequest {
		t.Errorf("POST bad JSON = %d, want 400", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/metrics-settings", strings.NewReader(`{"retentionDays":10}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST as text = %d, want 415", rec.Code)
	}
	if n := metricsSettings(t, h); n != 7 {
		t.Errorf("retention = %d after refused saves, want 7", n)
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("activity = %+v, want none", rows)
	}
	if n := monitorOf(d).prunes; n != 0 {
		t.Errorf("pruned %d times, want 0", n)
	}
}

// The prune after a save outlives the request but not forever: it has a
// 30-second deadline.
func TestMetricsSettingsPruneHasADeadline(t *testing.T) {
	d, h := metricsSetup(t)
	start := time.Now()
	rec := doJSON(h, http.MethodPost, "/api/metrics-settings", MetricsSettings{RetentionDays: 10})
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	f := monitorOf(d)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prunes != 1 || f.pruneCtx == nil {
		t.Fatalf("prunes = %d, want 1", f.prunes)
	}
	dl, ok := f.pruneCtx.Deadline()
	if !ok {
		t.Fatal("the prune has no deadline")
	}
	if left := dl.Sub(start); left <= 0 || left > 30*time.Second+time.Second {
		t.Fatalf("deadline %v after the save, want about 30 s", left)
	}
}

func TestMetricsSettingsPrunesAtOnce(t *testing.T) {
	d := newTestDeps(t)
	d.Zone, d.Now = mustZone(t, "Asia/Ho_Chi_Minh"), fixedClock(metricsNow)
	// The real Monitor (never started): its Prune rolls up and prunes the
	// store at the router's clock.
	d.Metrics = metrics.New(metrics.Options{Store: d.Store, Cmd: d.Docker, Source: d.Hub, Now: d.Now})
	h := NewRouter(d)

	save := func(days int) {
		t.Helper()
		rec := doJSON(h, http.MethodPost, "/api/metrics-settings", MetricsSettings{RetentionDays: days})
		if rec.Code != http.StatusOK || decode[MetricsSettings](t, rec).RetentionDays != days {
			t.Fatalf("save %d = %d %s", days, rec.Code, rec.Body.String())
		}
	}
	save(30)

	last := time.Date(2030, 1, 15, 10, 0, 0, 0, time.UTC)
	old := last.Add(-20 * 24 * time.Hour)
	insertSample(t, d, serverSample(old, 5))
	// Rolled up while it was recent; its raw row is pruned after 25 hours.
	if err := d.Store.Rollup(context.Background(), old.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recent := last.Add(-time.Hour)
	insertSample(t, d, serverSample(recent, 7))
	if s := serverSeries(t, h, "7d"); s.HistoryStart == nil || *s.HistoryStart != zoned(t, old) {
		t.Fatalf("historyStart = %v, want the 20-day rollup %s", s.HistoryStart, zoned(t, old))
	}

	save(7)
	if s := serverSeries(t, h, "7d"); s.HistoryStart == nil || *s.HistoryStart != zoned(t, recent) {
		t.Errorf("historyStart = %v after 30 → 7, want %s", s.HistoryStart, zoned(t, recent))
	}
	if n := metricsSettings(t, h); n != 7 {
		t.Errorf("retention = %d, want 7", n)
	}

	rows := activity(t, d)
	if len(rows) != 2 {
		t.Fatalf("activity = %+v, want two saves", rows)
	}
	for i, reason := range []string{"retention: 30 → 7 days", "retention: 7 → 30 days"} {
		r := rows[i]
		if r.Action != store.ActMetricsRetention || r.Target != "Metrics history" || r.Result != store.ResultOK ||
			r.Reason != reason || r.Actor != "test@example.com" || r.ProjectID != 0 {
			t.Errorf("activity %d = %+v, want %q", i, r, reason)
		}
	}

	// A save that changes nothing writes no activity.
	save(7)
	if n := len(activity(t, d)); n != 2 {
		t.Errorf("activity rows = %d after a same-value save, want 2", n)
	}
}
