package metrics

import (
	"context"
	"log/slog"
	"math"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// Interval is the time between the starts of two sampler ticks (decision 1).
const Interval = 30 * time.Second

// LatestMaxAge is how long the latest tick stands for the current values.
const LatestMaxAge = 90 * time.Second

const (
	// hubMaxAge is how old the hub's stats or inspect read may be for the
	// sampler to reuse it instead of reading Docker itself (decisions 7, 7a).
	hubMaxAge = 15 * time.Second
	// maxBaselineAge is the longest time between two readings of a counter
	// that still gives a rate: after a longer stall the sample is a gap, not
	// an average over the stall.
	maxBaselineAge = 3 * time.Minute
	// rollupSeconds is the width of a rollup bucket; a rollup runs once a
	// bucket boundary has passed.
	rollupSeconds = 300
	// pruneEvery is the time between two prunes of old samples.
	pruneEvery = time.Hour
	// clockTicks is the kernel's USER_HZ, the unit of /proc/self/stat's CPU
	// times.
	clockTicks = 100
	// maxInspectIDs is the most container ids the runner takes in one
	// docker.inspect call.
	maxInspectIDs = 1000
)

// tunnelTimeout bounds the Tunnel hook, so a slow probe cannot stretch a
// tick. A variable so a test can shorten it.
var tunnelTimeout = 5 * time.Second

// Source is where the sampler gets Docker's state: *control.Hub, whose cached
// reads are reused when fresh. RecentContainers may read Docker (sharing a hub
// tick); RecentStats and RecentInspect only copy what the hub holds.
type Source interface {
	RecentContainers(ctx context.Context, maxAge time.Duration) ([]project.Container, bool)
	RecentStats(maxAge time.Duration) (map[string]project.Usage, time.Time, bool)
	RecentInspect(maxAge time.Duration) (map[string]project.Inspect, time.Time, bool)
}

// ProjectNow is a project's usage at one tick: CPU (Docker's %, 100 = one
// core) and RAM summed over its running containers, and the network rates in
// bytes per second (nil when no container had a usable delta). A project with
// no running container has Running false and every value 0.
type ProjectNow struct {
	CPU           *float64
	Mem           *int64
	NetIn, NetOut *float64
	Running       bool
}

// Tick is one sample of the server and the projects, kept for the current
// values (the usage ranking). Server is nil when the host's /proc could not
// be read. Cores is the server's core count: the cpu<N> lines of the host's
// /proc/stat, else the panel's own count; never 0.
//
// Projects has an entry per tracked, present project only when Docker and the
// stats were read; a missing entry is unknown. SelfProject is the compose
// project of the panel's own container ("" with none, as in dev). When that is
// a tracked project, its entry in Projects is the panel's row and Self is nil;
// otherwise Self is the panel's row, from its containers or, without one, from
// the panel process itself (no network). Self is nil with Docker down.
//
// A Tick is shared once published: callers must not modify it.
type Tick struct {
	At          time.Time
	DockerUp    bool
	Server      *store.ServerSample
	Cores       int
	Projects    map[int64]ProjectNow
	Self        *ProjectNow
	SelfProject string
}

// netKey is a container's network namespace: the same container with a new
// pid (a restart) starts a new baseline.
type netKey struct {
	id  string
	pid int
}

// netReading is one read of a namespace's byte counters, at its own time.
type netReading struct {
	in, out uint64
	at      time.Time
}

// selfReading is one read of the panel process's usage.
type selfReading struct {
	usage SelfUsage
	at    time.Time
}

// Sampler samples the server and the tracked projects every Interval (start
// to start, never overlapping; a tick that overruns skips the starts it
// missed) and stores the samples: the host's counters from /proc, the
// projects' CPU and RAM from docker stats (the hub's when fresh), and their
// network from each running container's own net/dev. Rates are deltas
// between two readings over the real time between them, so the first tick
// after a start only sets the baselines and writes nothing. It rolls the
// samples up once a 5-minute boundary has passed and prunes them every hour,
// always rolling up first so no bucket is pruned before it is rolled.
//
// The tick's own state (the baselines, the maintenance times) is touched only
// by the tick (ticks never overlap); mu guards the latest tick and the loop,
// and nothing is called with it held.
type Sampler struct {
	st   *store.Store
	cmd  scan.Commander
	src  Source
	host Host
	now  func() time.Time
	log  *slog.Logger

	// Set by NewSampler; tests change them before the first tick.
	interval     time.Duration
	selfProc     string // the panel's own /proc, for ReadSelf
	rollup       func(ctx context.Context, now time.Time) error
	pruneMetrics func(ctx context.Context, now time.Time) error

	// Tunnel reports cloudflared's state for the tunnel series; nil, or
	// TunnelUnknown, stores a gap. Monitor sets it. It is called outside every
	// lock, with a context cut after tunnelTimeout.
	Tunnel func(ctx context.Context) TunnelState

	// The tick's state.
	based      bool // the first tick has set the baselines
	prevHost   *Counters
	prevNet    map[netKey]netReading
	prevSelf   *selfReading
	rolled     int64     // the bucket (unix seconds / 300) of the last rollup
	prunedAt   time.Time // when the last prune ran
	hostLogged bool      // the host's /proc being unreadable was logged

	mu         sync.Mutex
	latest     Tick
	haveLatest bool
	started    bool
	closed     bool
	cancel     context.CancelFunc
	done       chan struct{}
}

// NewSampler returns a Sampler that stores into st, reads Docker through src
// and cmd and the host through host. now is time.Now and log slog.Default when
// nil.
func NewSampler(st *store.Store, cmd scan.Commander, src Source, host Host, now func() time.Time, log *slog.Logger) *Sampler {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sampler{st: st, cmd: cmd, src: src, host: host, now: now, log: log,
		interval: Interval, selfProc: "/proc", rollup: st.Rollup, pruneMetrics: st.PruneMetrics}
}

// Start runs the first tick at once, then one every Interval, start to
// start, until ctx ends or Close. A second Start, or a Start after Close, does
// nothing.
func (s *Sampler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.started, s.done = true, make(chan struct{})
	go s.loop(ctx, s.done)
}

// Close stops the sampler: a tick in progress has its reads cancelled and
// writes nothing, and Close waits for it to end.
func (s *Sampler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// loop ticks on a grid of s.interval from its start: after a tick it waits
// for the next start on the grid, so a tick that overran skips the starts it
// missed and two ticks never overlap. The cadence uses the real clock; s.now
// stamps the readings and rows.
func (s *Sampler) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		start := time.Now()
		s.tick(ctx)
		timer.Reset(s.interval - time.Since(start)%s.interval)
	}
}

// Latest returns the latest written tick, ok false before the first one or
// when it is more than 90 s old.
func (s *Sampler) Latest() (Tick, bool) {
	s.mu.Lock()
	tk, ok := s.latest, s.haveLatest
	s.mu.Unlock()
	if !ok || s.now().Sub(tk.At) > LatestMaxAge {
		return Tick{}, false
	}
	return tk, true
}

// Prune rolls up the completed buckets, then deletes the samples past their
// age (raw after 25 hours, rollups after the retention setting). The API calls
// it right after the retention is saved.
func (s *Sampler) Prune(ctx context.Context) error {
	now := s.now()
	if err := s.rollup(ctx, now); err != nil {
		return err
	}
	return s.pruneMetrics(ctx, now)
}

// tick takes one sample. A panic is logged and recovered, so the loop goes on.
func (s *Sampler) tick(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("metrics: the sampler tick panicked", "panic", p, "stack", string(debug.Stack()))
		}
	}()
	now := s.now()
	tk := Tick{At: now, Cores: runtime.NumCPU(), Projects: map[int64]ProjectNow{}}
	var cores int
	tk.Server, cores = s.sampleHost(ctx, now)
	if cores > 0 {
		tk.Cores = cores
	}
	var rows []store.ProjectSample
	cs, up := s.src.RecentContainers(ctx, Interval)
	tk.DockerUp = up && ctx.Err() == nil
	if tk.DockerUp {
		rows = s.sampleDocker(ctx, &tk, cs)
	}
	if ctx.Err() != nil {
		return // cut by Close: write nothing
	}

	if !s.based {
		s.based = true // the baselines are set; the first tick writes nothing
	} else {
		if tk.Server != nil || len(rows) > 0 {
			if err := s.st.InsertSamples(ctx, tk.Server, rows); err != nil && ctx.Err() == nil {
				s.log.Warn("metrics: cannot store the samples", "err", err)
			}
		}
		s.mu.Lock()
		s.latest, s.haveLatest = tk, true
		s.mu.Unlock()
	}
	s.maintain(ctx, now)
}

// sampleHost reads the host's counters and returns the server sample at
// now, with the rates against the previous reading, and the core count. It
// returns nil and 0 when the host's /proc cannot be read (no tunnel value is
// stored then either); the baseline is kept (it ages out after
// maxBaselineAge). Besides the counters it reads the 1-minute load, the disks'
// used and total bytes summed (a gap with no disk listed) and the tunnel, each
// a gap when it cannot be read.
func (s *Sampler) sampleHost(ctx context.Context, now time.Time) (*store.ServerSample, int) {
	c, err := s.host.Read(s.now())
	if err != nil {
		if !s.hostLogged {
			s.log.Info("metrics: server metrics are not available: the host's /proc cannot be read", "err", err)
			s.hostLogged = true
		}
		return nil, 0
	}
	s.hostLogged = false
	prev := s.prevHost
	s.prevHost = &c
	used, total := c.MemUsed, c.MemTotal
	srv := &store.ServerSample{TS: now, MemUsed: &used, MemTotal: &total}
	if one, _, _, err := s.host.Loadavg(); err == nil {
		srv.Load1 = &one
	}
	if disks, _ := s.host.Disks(); len(disks) > 0 {
		var du, dt int64
		for _, d := range disks {
			du += int64(min(d.Used, math.MaxInt64))
			dt += int64(min(d.Total, math.MaxInt64))
		}
		srv.DiskUsed, srv.DiskTotal = &du, &dt
	}
	srv.Tunnel = s.tunnelValue(ctx)
	if prev != nil {
		if el, ok := elapsed(prev.At, c.At); ok {
			if p, ok := CPUPercent(prev.CPU, c.CPU); ok {
				srv.CPU = &p
			}
			if prev.DiskOK && c.DiskOK {
				srv.DiskRead, srv.DiskWrite = rate(prev.DiskRead, c.DiskRead, el), rate(prev.DiskWrite, c.DiskWrite, el)
			}
			if prev.NetOK && c.NetOK {
				srv.NetIn, srv.NetOut = rate(prev.NetIn, c.NetIn, el), rate(prev.NetOut, c.NetOut, el)
			}
		}
	}
	return srv, max(1, c.CPU.Cores)
}

// tunnelValue is the tunnel series' value from the hook: 1 connected, 0
// disconnected or not running, nil (a gap) for unknown, without a hook, or
// when the hook does not answer within tunnelTimeout. The hook runs in its own
// goroutine, so one that ignores its context (Monitor's shared probe runs on a
// context its caller cannot cut) still cannot stretch the tick; its late
// answer is dropped.
func (s *Sampler) tunnelValue(ctx context.Context) *float64 {
	if s.Tunnel == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, tunnelTimeout)
	defer cancel()
	res := make(chan TunnelState, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("metrics: the tunnel probe panicked", "panic", p, "stack", string(debug.Stack()))
				res <- TunnelUnknown
			}
		}()
		res <- s.Tunnel(ctx)
	}()
	var st TunnelState
	select {
	case st = <-res:
	case <-ctx.Done():
		return nil
	}
	if ctx.Err() != nil {
		return nil // answered after the deadline: not a reading of this tick
	}
	var v float64
	switch st {
	case TunnelConnected:
		v = 1
	case TunnelDisconnected, TunnelNotRunning:
		v = 0
	default:
		return nil
	}
	return &v
}

// group is the containers a row sums: a tracked project's, or the panel's.
type group struct {
	id      int64 // the tracked project; 0 for the untracked panel
	members []project.Container
}

// sampleDocker fills tk's projects and panel row from cs and the stats, and
// returns the rows to store: one per tracked, present project with at least
// one container in the stats. Without the tracked list or the stats no
// project is known.
func (s *Sampler) sampleDocker(ctx context.Context, tk *Tick, cs []project.Container) []store.ProjectSample {
	ts, err := s.st.TrackedProjects(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("metrics: cannot read the tracked projects", "err", err)
		}
		return nil
	}
	tk.SelfProject = project.SelfProject(cs)
	var groups []group
	selfTracked := false
	for _, t := range ts {
		if t.MissingSince != nil {
			continue
		}
		if tk.SelfProject != "" && t.ComposeProject == tk.SelfProject {
			selfTracked = true
		}
		groups = append(groups, group{id: t.ID, members: membersOf(cs, t.ComposeProject)})
	}
	if tk.SelfProject == "" {
		tk.Self = s.selfUsage()
	} else if !selfTracked {
		groups = append(groups, group{members: membersOf(cs, tk.SelfProject)})
	}

	use, ok := s.stats(ctx)
	if !ok {
		return nil
	}
	var ids []string // the running containers whose network is read
	for _, g := range groups {
		for _, c := range g.members {
			if _, ok := use[c.ID]; ok {
				ids = append(ids, c.ID)
			}
		}
	}
	insp := s.inspect(ctx, ids)
	if ctx.Err() != nil {
		return nil
	}
	rates := s.containerRates(ids, insp)

	var rows []store.ProjectSample
	for _, g := range groups {
		p := usageOf(g.members, use, rates)
		if g.id == 0 {
			tk.Self = &p
			continue
		}
		tk.Projects[g.id] = p
		if p.Running {
			rows = append(rows, store.ProjectSample{ProjectID: g.id, TS: tk.At, CPU: p.CPU, Mem: p.Mem, NetIn: p.NetIn, NetOut: p.NetOut})
		}
	}
	return rows
}

// membersOf returns the containers of compose project name, one-off ones left
// out, as the hub's views count them.
func membersOf(cs []project.Container, name string) []project.Container {
	var out []project.Container
	for _, c := range cs {
		if c.Project == name && !c.OneOff {
			out = append(out, c)
		}
	}
	return out
}

// usageOf sums the members found in use (the running ones) and their network
// rates. With none running every value is 0; the network is nil when no
// running member has a rate.
func usageOf(members []project.Container, use map[string]project.Usage, rates map[string][2]float64) ProjectNow {
	var cpu, in, out float64
	var mem int64
	running, withNet := 0, 0
	for _, c := range members {
		u, ok := use[c.ID]
		if !ok {
			continue
		}
		running++
		cpu += u.CPUPercent
		mem += u.MemBytes
		if r, ok := rates[c.ID]; ok {
			withNet++
			in += r[0]
			out += r[1]
		}
	}
	p := ProjectNow{CPU: &cpu, Mem: &mem, Running: running > 0}
	if running == 0 || withNet > 0 {
		p.NetIn, p.NetOut = &in, &out
	}
	return p
}

// stats returns the containers' CPU and RAM: the hub's read when it is at
// most hubMaxAge old, else one docker.stats read. ok is false when that read
// failed.
func (s *Sampler) stats(ctx context.Context) (map[string]project.Usage, bool) {
	if use, _, ok := s.src.RecentStats(hubMaxAge); ok {
		return use, true
	}
	res, err := s.cmd.Run(ctx, runner.StackDocker, runner.DockerStats, nil, nil)
	switch {
	case ctx.Err() != nil:
		return nil, false
	case err != nil:
		s.log.Debug("metrics: cannot read the container stats", "err", err)
		return nil, false
	case res.Truncated:
		s.log.Warn("metrics: the container stats are too large to read")
		return nil, false
	}
	return project.ParseStats(res.Stdout), true
}

// inspect returns the pid and network mode of the containers ids: from the
// hub's inspect read when it is at most hubMaxAge old, and from one
// docker.inspect run on the ids it is stale for or lacks (the hub inspects
// only tracked projects' containers, never an untracked panel's). An id
// neither answers is left out. The output of a failed run is still read, as
// the hub does: a container gone meanwhile makes docker print the others.
func (s *Sampler) inspect(ctx context.Context, ids []string) map[string]project.Inspect {
	out := make(map[string]project.Inspect, len(ids))
	if len(ids) == 0 {
		return out
	}
	hub, _, fresh := s.src.RecentInspect(hubMaxAge)
	var missing []string
	for _, id := range ids {
		if in, ok := hub[id]; fresh && ok {
			out[id] = in
		} else if isContainerID(id) {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out
	}
	if len(missing) > maxInspectIDs {
		missing = missing[:maxInspectIDs] // the rest add no network this tick
	}
	res, err := s.cmd.Run(ctx, runner.StackDocker, runner.DockerInspect, runner.Values{"ids": strings.Join(missing, " ")}, nil)
	got := project.ParseInspect(res.Stdout)
	if err != nil && ctx.Err() == nil {
		s.log.Debug("metrics: docker inspect failed", "err", err, "parsed", len(got), "asked", len(missing))
	}
	for _, id := range missing {
		if in, ok := got[id]; ok {
			out[id] = in
		}
	}
	return out
}

// containerRates reads the network counters of each container in ids through
// its host pid and returns the rates (in, out bytes per second) of those with
// a usable delta: the same container and pid read at the last tick, counters
// not gone back, and at most maxBaselineAge between the reads. A container
// with no pid (restarting) or sharing another namespace (host, container: or
// service: mode) is skipped, so a namespace is never counted twice. The
// readings become the next tick's baselines.
func (s *Sampler) containerRates(ids []string, insp map[string]project.Inspect) map[string][2]float64 {
	next := make(map[netKey]netReading, len(ids))
	rates := map[string][2]float64{}
	for _, id := range ids {
		in, ok := insp[id]
		if !ok || in.Pid <= 0 || sharedNetwork(in.NetworkMode) {
			continue
		}
		rx, tx, err := s.host.ContainerNet(in.Pid)
		if err != nil {
			continue // the process ended, no host /proc, or only lo
		}
		k := netKey{id: id, pid: in.Pid}
		cur := netReading{in: rx, out: tx, at: s.now()}
		next[k] = cur
		prev, ok := s.prevNet[k]
		if !ok || cur.in < prev.in || cur.out < prev.out {
			continue
		}
		if el, ok := elapsed(prev.at, cur.at); ok {
			rates[id] = [2]float64{float64(cur.in-prev.in) / el.Seconds(), float64(cur.out-prev.out) / el.Seconds()}
		}
	}
	s.prevNet = next
	return rates
}

// sharedNetwork reports whether a network mode joins a namespace counted
// elsewhere: the host's, or another container's.
func sharedNetwork(mode string) bool {
	return mode == "host" || strings.HasPrefix(mode, "container:") || strings.HasPrefix(mode, "service:")
}

// selfUsage is the panel process's own row (decision 8): CPU from its clock
// ticks since the last read (Docker's %, 100 = one core) and its resident
// memory; no network. Nil when /proc/self cannot be read.
func (s *Sampler) selfUsage() *ProjectNow {
	u, err := ReadSelf(s.selfProc)
	if err != nil {
		s.log.Debug("metrics: cannot read the panel's own usage", "err", err)
		return nil
	}
	cur := selfReading{usage: u, at: s.now()}
	prev := s.prevSelf
	s.prevSelf = &cur
	rss := u.RSS
	p := &ProjectNow{Mem: &rss, Running: true}
	if prev != nil && u.CPUTicks >= prev.usage.CPUTicks {
		if el, ok := elapsed(prev.at, cur.at); ok {
			cpu := float64(u.CPUTicks-prev.usage.CPUTicks) / clockTicks / el.Seconds() * 100
			p.CPU = &cpu
		}
	}
	return p
}

// maintain rolls the samples up once a 5-minute boundary has passed since the
// last rollup, and prunes them every hour (the first tick does both). A prune
// always follows a successful rollup in the same tick, so a bucket left
// unrolled by an outage is rolled before its raw rows go; a failed rollup
// defers the prune to the next tick.
func (s *Sampler) maintain(ctx context.Context, now time.Time) {
	bucket := now.Unix() / rollupSeconds
	pruneDue := s.prunedAt.IsZero() || now.Sub(s.prunedAt) >= pruneEvery || now.Before(s.prunedAt)
	if bucket == s.rolled && !pruneDue {
		return
	}
	if err := s.rollup(ctx, now); err != nil {
		if ctx.Err() == nil {
			s.log.Warn("metrics: cannot roll up the samples", "err", err)
		}
		return
	}
	s.rolled = bucket
	if !pruneDue {
		return
	}
	if err := s.pruneMetrics(ctx, now); err != nil {
		if ctx.Err() == nil {
			s.log.Warn("metrics: cannot prune old samples", "err", err)
		}
		return
	}
	s.prunedAt = now
}

// elapsed is the time between two readings, ok only when it is positive (a
// clock that went back gives no rate) and at most maxBaselineAge.
func elapsed(prev, cur time.Time) (time.Duration, bool) {
	d := cur.Sub(prev)
	return d, d > 0 && d <= maxBaselineAge
}

// rate is the per-second increase of a counter, nil when it went back.
func rate(prev, cur uint64, el time.Duration) *float64 {
	if cur < prev {
		return nil
	}
	r := float64(cur-prev) / el.Seconds()
	return &r
}

// isContainerID reports whether id is a full container id: 64 lowercase hex
// digits, the only form the runner's ids slot takes.
func isContainerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
