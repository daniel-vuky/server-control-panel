package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// dockerCheckAge is how old the hub's container read may be when System has
// no recent tick to tell whether Docker answers.
const dockerCheckAge = 30 * time.Second

// Options are what a Monitor reads: the store, Docker through the runner and
// the hub, and the host's files.
type Options struct {
	Store  *store.Store
	Cmd    scan.Commander
	Source Source
	Host   Host
	// Socket is the Docker socket; empty is /var/run/docker.sock.
	Socket string
	// CloudflaredAddr is cloudflared's metrics server (CLOUDFLARED_METRICS);
	// empty tries cloudflared's default addresses.
	CloudflaredAddr string
	Now             func() time.Time
	Log             *slog.Logger
}

// Monitor is the server's metrics: it runs the Sampler (the 30-second
// samples and the latest tick), reads the projects' disk usage every hour,
// and answers the System info and System services cards from cached reads
// (docker.info at most every 60 s, cloudflared every 15 s).
//
// mu guards only the in-memory state below it and is never held while the
// store, the runner, the hub or the network is called. A refresh of
// docker.info or cloudflared runs once at a time: a caller that finds one
// running waits for it on a channel, not on a lock.
type Monitor struct {
	st      *store.Store
	cmd     scan.Commander
	src     Source
	host    Host
	socket  string
	now     func() time.Time
	log     *slog.Logger
	sampler *Sampler
	client  *http.Client
	addrs   []string

	// Seams; tests replace them before Start.
	after func(time.Duration) <-chan time.Time
	walk  func(ctx context.Context, root string) (bytes int64, partial bool, err error)

	// vols is the last good docker.df read (nil before one); only the disk
	// loop touches it.
	vols map[string]int64

	mu         sync.Mutex
	disk       map[int64]DiskUsage
	info       DockerInfo
	haveInfo   bool
	infoAt     time.Time // the last docker.info attempt
	infoBusy   chan struct{}
	tunnel     Tunnel
	haveTunnel bool
	tunnelAt   time.Time
	tunnelBusy chan struct{}
	started    bool
	closed     bool
	cancel     context.CancelFunc
	done       chan struct{}
}

// New returns a Monitor over o. Now is time.Now and Log slog.Default when nil.
func New(o Options) *Monitor {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Socket == "" {
		o.Socket = dockerSocket
	}
	addrs := cloudflaredDefaults
	if o.CloudflaredAddr != "" {
		addrs = []string{o.CloudflaredAddr}
	}
	return &Monitor{
		st: o.Store, cmd: o.Cmd, src: o.Source, host: o.Host, socket: o.Socket, now: o.Now, log: o.Log,
		sampler: NewSampler(o.Store, o.Cmd, o.Source, o.Host, o.Now, o.Log),
		client:  newCloudflaredClient(),
		addrs:   slices.Clone(addrs),
		after:   time.After,
		walk: func(ctx context.Context, root string) (int64, bool, error) {
			return walkFolder(ctx, root, walkMaxEntries, walkDeadline, os.ReadDir)
		},
		disk: map[int64]DiskUsage{},
	}
}

// Start starts the sampler and the disk usage loop (its first read 2 minutes
// on, then one an hour after the last ended). A second Start, or a Start after
// Close, does nothing.
func (m *Monitor) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started || m.closed {
		m.mu.Unlock()
		return
	}
	ctx, m.cancel = context.WithCancel(ctx)
	m.started, m.done = true, make(chan struct{})
	go m.diskLoop(ctx, m.done)
	m.mu.Unlock()
	m.sampler.Start(ctx)
}

// Close stops the disk usage loop (a read in progress is cancelled and
// publishes nothing) and the sampler, and waits for both.
func (m *Monitor) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	m.sampler.Close()
}

// Latest is the sampler's latest tick, ok false before one or when it is more
// than 90 s old.
func (m *Monitor) Latest() (Tick, bool) { return m.sampler.Latest() }

// Prune rolls up and prunes the samples at once (after a retention save).
func (m *Monitor) Prune(ctx context.Context) error { return m.sampler.Prune(ctx) }

// Disks lists the server's disks (Host.Disks): one statfs per disk, no
// Docker read.
func (m *Monitor) Disks() (disks []Disk, fallback bool) { return m.host.Disks() }

// Thresholds are the server's alert lines.
func (m *Monitor) Thresholds() Thresholds { return DefaultThresholds }

// Disk returns the latest disk usage per tracked, present project; a project
// never read is absent.
func (m *Monitor) Disk() map[int64]DiskUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int64]DiskUsage, len(m.disk))
	for id, u := range m.disk {
		out[id] = u
	}
	return out
}

// diskLoop reads the disk usage diskFirst after the start, then diskEvery
// after each read ended, so two reads never overlap.
func (m *Monitor) diskLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	wait := diskFirst
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.after(wait):
		}
		m.readDisk(ctx)
		wait = diskEvery
	}
}

// readDisk reads the named volumes' sizes once (docker.df), then walks each
// tracked, present project's folder, one at a time (decision 17). A failed
// docker.df keeps the last good volume sizes (with none, the values are only
// lower bounds); a project whose folder cannot be read keeps its last value.
// A read cut by Close publishes nothing.
func (m *Monitor) readDisk(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			m.log.Error("metrics: the disk usage read panicked", "panic", p, "stack", string(debug.Stack()))
		}
	}()
	ts, err := m.st.TrackedProjects(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Warn("metrics: cannot read the tracked projects for disk usage", "err", err)
		}
		return
	}
	res, err := m.cmd.Run(ctx, runner.StackDocker, runner.DockerDF, nil, nil)
	if ctx.Err() != nil {
		return
	}
	var vols map[string]int64
	if err == nil && !res.Truncated {
		vols = ParseVolumes(res.Stdout)
	}
	if vols != nil {
		m.vols = vols
	} else {
		m.log.Warn("metrics: cannot read the volume sizes; the last ones are kept", "err", err, "truncated", res.Truncated)
	}

	prev := m.Disk()
	next := make(map[int64]DiskUsage, len(ts))
	for _, t := range ts {
		if t.MissingSince != nil {
			continue
		}
		n, partial, err := m.walk(ctx, t.Path)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.log.Debug("metrics: cannot read a project folder", "project", t.ID, "err", err)
			if p, ok := prev[t.ID]; ok {
				next[t.ID] = p
			}
			continue
		}
		next[t.ID] = DiskUsage{Volumes: m.vols[t.ComposeProject], Folder: n, Partial: partial || m.vols == nil, At: m.now()}
	}
	m.mu.Lock()
	m.disk = next
	m.mu.Unlock()
}

// System reads the System info and System services cards: Docker's state
// (Not installed without the socket, else from the latest tick, or one hub
// read when there is no recent tick), the facts of docker.info (cached; read
// only while Docker answers, at most every 60 s; a failed read keeps the last
// facts, and before any, cores and RAM come from the host's /proc), cloudflared
// (cached 15 s) and the disks.
func (m *Monitor) System(ctx context.Context) System {
	sys := System{Available: m.host.Available(), Docker: m.dockerState(ctx)}
	if info, ok := m.dockerInfo(ctx, sys.Docker == DockerRunning); ok {
		sys.Hostname, sys.OS, sys.Kernel = info.Name, info.OperatingSystem, info.KernelVersion
		sys.Cores, sys.RAMBytes = info.NCPU, info.MemTotal
		sys.DockerVersion, sys.ContainersRunning = info.ServerVersion, info.ContainersRunning
	}
	if sys.Cores == 0 || sys.RAMBytes == 0 {
		if c, err := m.host.Read(m.now()); err == nil {
			if sys.Cores == 0 {
				sys.Cores = c.CPU.Cores
			}
			if sys.RAMBytes == 0 {
				sys.RAMBytes = c.MemTotal
			}
		}
	}
	sys.Tunnel = m.cloudflared(ctx)
	sys.Disks, sys.DisksFallback = m.host.Disks()
	return sys
}

// dockerState is Not installed when the socket is not there, else whether
// Docker answered at the latest tick (no older than 90 s), or, with none, at a
// hub read no older than dockerCheckAge.
func (m *Monitor) dockerState(ctx context.Context) DockerState {
	if !isSocket(m.socket) {
		return DockerNotInstalled
	}
	var up bool
	if tk, ok := m.sampler.Latest(); ok {
		up = tk.DockerUp
	} else {
		_, up = m.src.RecentContainers(ctx, dockerCheckAge)
	}
	if up {
		return DockerRunning
	}
	return DockerNotResponding
}

// dockerInfo returns the cached docker.info facts, reading them first when
// Docker answers and the last attempt is infoEvery old.
func (m *Monitor) dockerInfo(ctx context.Context, up bool) (DockerInfo, bool) {
	if up {
		m.refresh(ctx, &m.infoBusy, func(now time.Time) bool { return expired(m.infoAt, now, infoEvery) }, func(ctx context.Context) {
			res, err := m.cmd.Run(ctx, runner.StackDocker, runner.DockerInfo, nil, nil)
			var info DockerInfo
			if err == nil && res.Truncated {
				err = errors.New("metrics: docker info is too large to read")
			}
			if err == nil {
				info, err = ParseDockerInfo(res.Stdout)
			}
			if err != nil {
				m.log.Debug("metrics: cannot read docker info; the last facts are kept", "err", err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			m.infoAt = m.now()
			if err == nil {
				m.info, m.haveInfo = info, true
			}
		})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.info, m.haveInfo
}

// cloudflared returns cloudflared's state, probed again when the cached one is
// tunnelEvery old. Whether it runs is read from the host's /proc first, so a
// stopped cloudflared costs no request.
func (m *Monitor) cloudflared(ctx context.Context) Tunnel {
	m.refresh(ctx, &m.tunnelBusy, func(now time.Time) bool { return !m.haveTunnel || expired(m.tunnelAt, now, tunnelEvery) }, func(ctx context.Context) {
		t := ProbeCloudflared(ctx, m.client, m.addrs, m.host.Running(cloudflaredComm))
		m.mu.Lock()
		defer m.mu.Unlock()
		m.tunnel, m.haveTunnel, m.tunnelAt = t, true, m.now()
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.haveTunnel {
		return Tunnel{State: TunnelUnknown} // this caller gave up waiting for the first probe
	}
	return m.tunnel
}

// refresh runs read when due says the cache is stale, one at a time: a caller
// that finds a read running waits for it (or for its own ctx) and returns. due
// is called with mu held; read runs without it, on a context the caller's
// cancel does not cut, since its answer is shared.
func (m *Monitor) refresh(ctx context.Context, busy *chan struct{}, due func(now time.Time) bool, read func(ctx context.Context)) {
	m.mu.Lock()
	if !due(m.now()) {
		m.mu.Unlock()
		return
	}
	if ch := *busy; ch != nil {
		m.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
		}
		return
	}
	ch := make(chan struct{})
	*busy = ch
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		*busy = nil
		m.mu.Unlock()
		close(ch)
	}()
	read(context.WithoutCancel(ctx))
}

// expired reports whether a value read at at is every old at now (or from a
// clock that went back). A zero at is always expired.
func expired(at, now time.Time, every time.Duration) bool {
	return at.IsZero() || now.Sub(at) >= every || now.Before(at)
}
