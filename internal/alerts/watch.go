package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The watcher's values (decisions 12 to 15).
const (
	passInterval   = 30 * time.Second // start to start; domains every other pass
	readMaxAge     = 90 * time.Second // the hub's cached container and inspect reads
	tunnelTimeout  = 5 * time.Second  // the Monitor's tunnel read may wait on a probe
	errorReadGap   = 30 * time.Second // two Error reads at least this far apart
	tunnelDownRead = 2                // reads in a row not connected
	domainFailures = 3                // failed checks in a row

	keyTunnel   = "tunnel"
	keyCPU      = "server:cpu"
	keyRAM      = "server:ram"
	keyDiskPfx  = "server:disk:"
	keyProjPfx  = "project:"
	serverPath  = "/server"
	serverWords = "Server" // the target of a server alert when the host name is not known
)

// Sources is what the watcher reads, each outside its lock: the tracked
// projects (the store), the hub's cached container list and inspect data and
// the controller's locks, the Monitor's latest tick, disks and tunnel, and
// the stored tunnel_seen_connected flag. A nil source is never read (a
// missing reading).
type Sources struct {
	Tracked    func(ctx context.Context) ([]store.Tracked, error)
	Containers func(maxAge time.Duration) ([]project.Container, time.Time, bool, bool)
	Inspect    func(maxAge time.Duration) (map[string]project.Inspect, time.Time, bool)
	Lock       func(int64) (project.Status, bool, bool)
	Latest     func() (metrics.Tick, bool)
	Disks      func() ([]metrics.Disk, bool)
	Tunnel     func(ctx context.Context) metrics.Tunnel
	Settings   func(ctx context.Context) (store.AlertSettings, error)
	SetSeen    func(ctx context.Context, seen bool) error
}

// Watcher checks the server and the projects every 30 s and raises an alert
// when a problem starts and a recovery when it clears (decisions 11 to 15):
// a resource over its threshold for the sustained time, the tunnel down on
// two reads after it was once seen connected, a project in Error on two reads
// 30 s apart, and (every other pass) a public domain failing 3 checks in a
// row. Every pass also closes, silently, the open incidents nothing watches
// any more.
//
// The notifier's book of open keys is authoritative: a pass reads it with
// every other source, decides, and only raises a key that is not open and
// recovers or closes one that is. Its own state is in memory (the time a
// measure went over, the first Error read, the domain failure counts), so a
// restart restarts the timers but never alerts again for a stored incident.
//
// mu guards that state and is held only while deciding: a pass reads every
// source, then decides under mu, then calls the notifier and the store,
// outside it. Passes must not overlap: the loop is the only caller once
// Start ran.
type Watcher struct {
	src Sources
	n   *Notifier
	th  interface {
		Thresholds() metrics.Thresholds
		Sustained() time.Duration
	}
	frame  Frame
	client *http.Client
	log    *slog.Logger
	after  func(time.Duration) <-chan time.Time // time.After; tests replace it before Start

	wg sync.WaitGroup

	mu         sync.Mutex
	passes     int
	lastTick   time.Time            // the tick already evaluated
	over       map[string]time.Time // a measure's first reading over, by key
	errFirst   map[int64]time.Time  // a project's first Error read
	domains    map[int64]*domainState
	seen       bool // the tunnel has been seen connected
	seenLoaded bool // seen was read from the store
	seenStored bool // the store says so too
	down       int  // tunnel reads in a row not connected
	started    bool
	closed     bool
	cancel     context.CancelFunc
}

// domainState is a project's domain and its failed checks in a row.
type domainState struct {
	domain string
	fails  int
}

// NewWatcher returns a watcher over src that raises through n. th gives the
// thresholds and the sustained time, f the clock and the host name; client
// makes the domain checks (NewDomainClient when nil).
func NewWatcher(src Sources, n *Notifier, th interface {
	Thresholds() metrics.Thresholds
	Sustained() time.Duration
}, f Frame, client *http.Client, log *slog.Logger) *Watcher {
	if client == nil {
		client = NewDomainClient()
	}
	if log == nil {
		log = slog.Default()
	}
	return &Watcher{
		src:      src,
		n:        n,
		th:       th,
		frame:    f,
		client:   client,
		log:      log,
		after:    time.After,
		over:     map[string]time.Time{},
		errFirst: map[int64]time.Time{},
		domains:  map[int64]*domainState{},
	}
}

// Start runs a pass now and then every 30 s, start to start (an overrun
// starts the next pass at once), until Close or until ctx is done.
func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.closed {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	w.started, w.cancel = true, cancel
	w.wg.Add(1)
	go w.loop(ctx)
}

// Close stops the loop and waits for a pass in progress, whose requests are
// cut short; it raises nothing more.
func (w *Watcher) Close() {
	w.mu.Lock()
	w.closed = true
	cancel := w.cancel
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
}

func (w *Watcher) loop(ctx context.Context) {
	defer w.wg.Done()
	for ctx.Err() == nil {
		start := time.Now()
		w.safePass(ctx)
		select {
		case <-ctx.Done():
		case <-w.after(max(0, passInterval-time.Since(start))):
		}
	}
}

// safePass is a pass whose panic is logged, not fatal: the next one runs.
func (w *Watcher) safePass(ctx context.Context) {
	defer func() {
		if v := recover(); v != nil {
			w.log.Error("alerts: a watcher pass panicked", "panic", fmt.Sprint(v))
		}
	}()
	w.Pass(ctx)
}

// Pass is one pass: read every source, check the domains on every other pass
// (not while a seen tunnel is down: nothing would be counted), decide, then
// raise, recover and close. A pass cut short by ctx decides nothing.
func (w *Watcher) Pass(ctx context.Context) {
	in := w.read(ctx)
	if ctx.Err() != nil {
		return
	}
	if in.domainPass && !in.domainsPaused {
		in.domains = w.checkDomains(ctx, in.views)
		if ctx.Err() != nil {
			return
		}
	}
	w.act(ctx, w.decide(in))
}

// observed is what one pass read.
type observed struct {
	now        time.Time
	host       string
	th         metrics.Thresholds
	sustained  time.Duration
	tick       metrics.Tick
	tickOK     bool // a tick at most metrics.LatestMaxAge old
	disks      []metrics.Disk
	disksReal  bool // disks is the mount table's list, not the fallback row
	tunnel     metrics.TunnelState
	settings   *store.AlertSettings // read until it has been once
	tracked    []store.Tracked
	trackedOK  bool
	views      []control.ProjectView // the statuses, when the containers were read
	viewsOK    bool
	readAt     time.Time // when the hub read the containers
	open       map[string]bool
	domainPass bool
	// domainsPaused: the tunnel, seen connected, reads down, so domain
	// failures are not counted and the domains are not checked.
	domainsPaused bool
	domains       map[int64]domainResult
}

// read reads every source, outside mu.
func (w *Watcher) read(ctx context.Context) observed {
	in := observed{now: w.frame.now(), th: w.th.Thresholds(), sustained: w.th.Sustained(), host: serverWords}
	if w.frame.Host != nil {
		if h := w.frame.Host(); h != "" {
			in.host = h
		}
	}
	if w.src.Latest != nil {
		in.tick, in.tickOK = w.src.Latest()
		in.tickOK = in.tickOK && !in.tick.At.IsZero() && in.now.Sub(in.tick.At) <= metrics.LatestMaxAge
	}
	if w.src.Disks != nil {
		var fallback bool
		in.disks, fallback = w.src.Disks()
		in.disksReal = !fallback && len(in.disks) > 0
	}
	in.tunnel = w.readTunnel(ctx)

	w.mu.Lock()
	loaded, seen := w.seenLoaded, w.seen
	in.domainPass = w.passes%2 == 0
	w.passes++
	w.mu.Unlock()
	if !loaded && w.src.Settings != nil {
		if s, err := w.src.Settings(ctx); err != nil {
			w.log.Warn("alerts: cannot read the alert settings", "error", err)
		} else {
			in.settings = &s
			seen = seen || s.TunnelSeenConnected
		}
	}
	in.domainsPaused = seen && tunnelIsDown(in.tunnel)

	if w.src.Tracked != nil {
		if ts, err := w.src.Tracked(ctx); err != nil {
			w.log.Warn("alerts: cannot read the tracked projects", "error", err)
		} else {
			in.tracked, in.trackedOK = ts, true
		}
	}
	if in.trackedOK && w.src.Containers != nil {
		if cs, at, up, ok := w.src.Containers(readMaxAge); ok {
			var insp map[string]project.Inspect
			if w.src.Inspect != nil {
				insp, _, _ = w.src.Inspect(readMaxAge)
			}
			in.views = control.Views(in.tracked, cs, nil, up, w.src.Lock, insp, nil, drive.Status{})
			in.viewsOK, in.readAt = true, at
		}
	}

	in.open = map[string]bool{}
	for _, k := range w.n.OpenKeys() {
		in.open[k] = true
	}
	return in
}

// readTunnel is the tunnel's state, Unknown when the source does not answer
// within tunnelTimeout. The source runs in its own goroutine, so one that
// waits on a probe in progress cannot stretch the pass; its late answer is
// dropped.
func (w *Watcher) readTunnel(ctx context.Context) metrics.TunnelState {
	if w.src.Tunnel == nil {
		return metrics.TunnelUnknown
	}
	tctx, cancel := context.WithTimeout(ctx, tunnelTimeout)
	defer cancel()
	got := make(chan metrics.TunnelState, 1)
	go func() { got <- w.src.Tunnel(tctx).State }()
	select {
	case s := <-got:
		return s
	case <-tctx.Done():
		return metrics.TunnelUnknown
	}
}

// checkDomains checks, one at a time, the public domain of every present
// project that is Running, Partial or Error (never one Stopped, Not found,
// Unknown or in flight).
func (w *Watcher) checkDomains(ctx context.Context, views []control.ProjectView) map[int64]domainResult {
	out := map[int64]domainResult{}
	for _, v := range views {
		if v.PublicDomain == "" || v.MissingSince != nil || !domainChecked(v.Status) {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		out[v.ID] = checkDomain(ctx, w.client, v.PublicDomain)
	}
	return out
}

func domainChecked(s project.Status) bool {
	return s == project.StatusRunning || s == project.StatusPartial || s == project.StatusError
}

// act is one notifier call (or the store write of the seen flag) decided by a
// pass.
type act struct {
	kind actKind
	key  string
	a    Alert
}

type actKind int

const (
	actRaise actKind = iota
	actRecover
	actClose
	actSeen
)

// decision collects a pass's acts: a key is raised only when it is not open,
// recovered or closed only when it is, and acted on once per pass.
type decision struct {
	open map[string]bool
	done map[string]bool
	acts []act
}

func (d *decision) raise(key string, a Alert) {
	if d.open[key] || d.done[key] {
		return
	}
	d.done[key] = true
	a.Key = key
	d.acts = append(d.acts, act{kind: actRaise, key: key, a: a})
}

func (d *decision) recover(key string, a Alert) {
	if !d.open[key] || d.done[key] {
		return
	}
	d.done[key] = true
	d.acts = append(d.acts, act{kind: actRecover, key: key, a: a})
}

func (d *decision) close(key string) {
	if !d.open[key] || d.done[key] {
		return
	}
	d.done[key] = true
	d.acts = append(d.acts, act{kind: actClose, key: key})
}

// decide updates the in-memory state from what the pass read and returns the
// acts, under mu.
func (w *Watcher) decide(in observed) []act {
	w.mu.Lock()
	defer w.mu.Unlock()
	d := &decision{open: in.open, done: map[string]bool{}}
	w.decideResources(in, d)
	w.decideTunnel(in, d)
	if in.viewsOK {
		w.decideProjects(in, d)
		w.decideDomains(in, d)
	}
	w.sweep(in, d)
	w.forget(in)
	return d.acts
}

// act calls the notifier and the store, outside mu.
func (w *Watcher) act(ctx context.Context, acts []act) {
	for _, a := range acts {
		switch a.kind {
		case actRaise:
			w.n.Raise(a.a)
		case actRecover:
			w.n.Recover(a.key, a.a)
		case actClose:
			w.n.CloseSilently(a.key)
		case actSeen:
			if w.src.SetSeen == nil {
				continue
			}
			if err := w.src.SetSeen(ctx, true); err != nil {
				w.log.Warn("alerts: cannot store that the tunnel was seen connected", "error", err)
				continue
			}
			w.mu.Lock()
			w.seenStored = true
			w.mu.Unlock()
		}
	}
}

// reading is one measure at one time: over its threshold or not, with the
// alert and the recovery it would send.
type reading struct {
	key      string
	at       time.Time
	over     bool
	alert    Alert
	recovery Alert
}

// decideResources applies the readings of a tick not evaluated before (server
// and project CPU and RAM, at the tick's time) and the disks (at the pass's
// time). A measure with no reading neither resets nor advances its timer.
func (w *Watcher) decideResources(in observed, d *decision) {
	var rs []reading
	if in.tickOK && !in.tick.At.Equal(w.lastTick) {
		w.lastTick = in.tick.At
		rs = append(rs, serverReadings(in)...)
		if in.trackedOK {
			rs = append(rs, projectReadings(in)...)
			w.stoppedProjects(in, d)
		}
	}
	rs = append(rs, diskReadings(in)...)
	for _, r := range rs {
		if !r.over {
			delete(w.over, r.key)
			d.recover(r.key, r.recovery)
			continue
		}
		first, ok := w.over[r.key]
		if !ok {
			first = r.at
			w.over[r.key] = first
		}
		if r.at.Sub(first) >= in.sustained {
			d.raise(r.key, r.alert)
		}
	}
}

// stoppedProjects drops the CPU and RAM timers of each present project the
// tick read with nothing running, and closes their incidents silently: a
// project stopped on purpose is not a recovery (decisions 14 and 15), and its
// zeros are no reading. Called only for a tick just evaluated.
func (w *Watcher) stoppedProjects(in observed, d *decision) {
	for _, t := range in.tracked {
		p, ok := in.tick.Projects[t.ID]
		if !ok || p.Running || t.MissingSince != nil {
			continue
		}
		for _, kind := range []string{"cpu", "ram"} {
			key := projectKey(t.ID, kind)
			delete(w.over, key)
			d.close(key)
		}
	}
}

// serverAlert is a server measure's alert or recovery.
func serverAlert(in observed, title string) Alert {
	return Alert{Action: store.ActAlertResource, Target: in.host, Path: serverPath, Message: Message{Title: title}}
}

func serverReadings(in observed) []reading {
	s := in.tick.Server
	if s == nil {
		return nil
	}
	var rs []reading
	sus := minutes(in.sustained)
	if s.CPU != nil {
		v, th := *s.CPU, in.th.CPUPercent
		rs = append(rs, reading{key: keyCPU, at: in.tick.At, over: v > float64(th),
			alert:    serverAlert(in, fmt.Sprintf("CPU at %s %% for %s (threshold %d %%)", percent(v), sus, th)),
			recovery: serverAlert(in, fmt.Sprintf("CPU back to %s %% (threshold %d %%)", percent(v), th)),
		})
	}
	if s.MemUsed != nil && s.MemTotal != nil && *s.MemTotal > 0 {
		v, th := float64(*s.MemUsed)/float64(*s.MemTotal)*100, in.th.RAMPercent
		rs = append(rs, reading{key: keyRAM, at: in.tick.At, over: v > float64(th),
			alert:    serverAlert(in, fmt.Sprintf("RAM at %s %% for %s (threshold %d %%)", percent(v), sus, th)),
			recovery: serverAlert(in, fmt.Sprintf("RAM back to %s %% (threshold %d %%)", percent(v), th)),
		})
	}
	return rs
}

func diskReadings(in observed) []reading {
	var rs []reading
	th := in.th.DiskPercent
	for _, dk := range in.disks {
		if dk.Total == 0 {
			continue
		}
		v := float64(dk.Used) / float64(dk.Total) * 100
		rs = append(rs, reading{key: keyDiskPfx + dk.Mount, at: in.now, over: v > float64(th),
			alert:    serverAlert(in, fmt.Sprintf("Disk %s at %s %% (threshold %d %%)", dk.Mount, percent(v), th)),
			recovery: serverAlert(in, fmt.Sprintf("Disk %s back to %s %% (threshold %d %%)", dk.Mount, percent(v), th)),
		})
	}
	return rs
}

// projectReadings are each present, running project's CPU as a share of the
// server (Docker's % over the cores) and its RAM, against its own thresholds
// or the server-wide percentages (decision 10). A project the tick read with
// nothing running has no reading (stoppedProjects closes its incidents).
func projectReadings(in observed) []reading {
	cores := float64(max(1, in.tick.Cores))
	sus := minutes(in.sustained)
	var total float64
	if s := in.tick.Server; s != nil && s.MemTotal != nil {
		total = float64(*s.MemTotal)
	}
	var rs []reading
	for _, t := range in.tracked {
		p, ok := in.tick.Projects[t.ID]
		if !ok || !p.Running || t.MissingSince != nil {
			continue
		}
		alert := func(title string) Alert {
			return Alert{ProjectID: t.ID, Action: store.ActAlertResource, Target: t.DisplayName, Message: Message{Title: title}}
		}
		name := t.DisplayName
		if p.CPU != nil {
			v, th := *p.CPU/cores, in.th.CPUPercent
			if t.AlertCPUPercent != nil {
				th = *t.AlertCPUPercent
			}
			rs = append(rs, reading{key: projectKey(t.ID, "cpu"), at: in.tick.At, over: v > float64(th),
				alert:    alert(fmt.Sprintf("%s CPU at %s %% of server for %s (threshold %d %%)", name, percent(v), sus, th)),
				recovery: alert(fmt.Sprintf("%s CPU back to %s %% of server (threshold %d %%)", name, percent(v), th)),
			})
		}
		switch {
		case p.Mem == nil:
		case t.AlertRAMMB != nil:
			limit := int64(*t.AlertRAMMB) << 20
			rs = append(rs, reading{key: projectKey(t.ID, "ram"), at: in.tick.At, over: *p.Mem > limit,
				alert:    alert(fmt.Sprintf("%s RAM at %s for %s (threshold %s)", name, memory(*p.Mem), sus, memory(limit))),
				recovery: alert(fmt.Sprintf("%s RAM back to %s (threshold %s)", name, memory(*p.Mem), memory(limit))),
			})
		case total > 0:
			v, th := float64(*p.Mem)/total*100, in.th.RAMPercent
			rs = append(rs, reading{key: projectKey(t.ID, "ram"), at: in.tick.At, over: v > float64(th),
				alert:    alert(fmt.Sprintf("%s RAM at %s %% of server for %s (threshold %d %%)", name, percent(v), sus, th)),
				recovery: alert(fmt.Sprintf("%s RAM back to %s %% of server (threshold %d %%)", name, percent(v), th)),
			})
		}
	}
	return rs
}

// decideTunnel raises once the tunnel, seen connected before, reads not
// connected twice in a row (disconnected or not running), and recovers on
// the first connected read. Unknown changes nothing. The first connected read
// is stored, once.
func (w *Watcher) decideTunnel(in observed, d *decision) {
	if in.settings != nil && !w.seenLoaded {
		w.seenLoaded = true
		if in.settings.TunnelSeenConnected {
			w.seen, w.seenStored = true, true
		}
	}
	switch in.tunnel {
	case metrics.TunnelConnected:
		w.down = 0
		w.seen = true
		if !w.seenStored {
			d.acts = append(d.acts, act{kind: actSeen})
		}
		d.recover(keyTunnel, Alert{Target: in.host, Path: serverPath, Message: Message{Title: "Cloudflare Tunnel connected again"}})
	case metrics.TunnelDisconnected, metrics.TunnelNotRunning:
		if !w.seen {
			return
		}
		w.down++
		if w.down < tunnelDownRead {
			return
		}
		detail := "cloudflared has no connection to Cloudflare"
		if in.tunnel == metrics.TunnelNotRunning {
			detail = "cloudflared is not running"
		}
		d.raise(keyTunnel, Alert{Action: store.ActAlertTunnel, Target: in.host, Path: serverPath,
			Message: Message{Title: "Cloudflare Tunnel disconnected", Detail: detail}})
	}
}

// tunnelIsDown is whether a tunnel read counts as down: disconnected or not
// running, as the Dashboard treats it.
func tunnelIsDown(s metrics.TunnelState) bool {
	return s == metrics.TunnelDisconnected || s == metrics.TunnelNotRunning
}

// decideProjects raises Error when a project is Error on two reads at least
// 30 s apart, recovers when it reads Running or Partial, and closes silently
// when it is Stopped or Not found. In flight and Unknown change nothing.
func (w *Watcher) decideProjects(in observed, d *decision) {
	for _, v := range in.views {
		key := projectKey(v.ID, "error")
		switch v.Status {
		case project.StatusError:
			first, ok := w.errFirst[v.ID]
			if !ok {
				w.errFirst[v.ID] = in.readAt
				continue
			}
			if in.readAt.Sub(first) >= errorReadGap {
				d.raise(key, Alert{ProjectID: v.ID, Action: store.ActAlertProject, Target: v.DisplayName,
					Message: Message{Title: v.DisplayName + " is in Error", Detail: issueOf(v.Components)}})
			}
		case project.StatusRunning, project.StatusPartial:
			delete(w.errFirst, v.ID)
			d.recover(key, Alert{ProjectID: v.ID, Target: v.DisplayName, Message: Message{Title: v.DisplayName + " is running again"}})
		case project.StatusStopped, project.StatusNotFound:
			delete(w.errFirst, v.ID)
			d.close(key)
		}
	}
}

// decideDomains counts each checked domain's failures in a row (no domain
// is checked while a seen tunnel is down), raises at 3 and recovers on the
// first answer. A project Stopped, Not found or in flight starts over, and
// one Stopped or Not found closes its incident silently, as does a changed
// domain; Unknown (Docker not answering) keeps the count.
func (w *Watcher) decideDomains(in observed, d *decision) {
	for _, v := range in.views {
		key := projectKey(v.ID, "domain")
		domain, name := v.PublicDomain, v.DisplayName
		if domain == "" || v.MissingSince != nil {
			delete(w.domains, v.ID) // the sweep closes its incident
			continue
		}
		st := w.domains[v.ID]
		if st == nil || st.domain != domain {
			if st != nil {
				d.close(key) // the incident was for the old domain
			}
			st = &domainState{domain: domain}
			w.domains[v.ID] = st
		}
		if !domainChecked(v.Status) {
			switch v.Status {
			case project.StatusUnknown: // Docker did not answer: a blip, the count is kept
			case project.StatusStopped, project.StatusNotFound:
				st.fails = 0
				d.close(key)
			default: // in flight
				st.fails = 0
			}
			continue
		}
		r, ok := in.domains[v.ID]
		if !ok || r.domain != domain {
			continue // not a domain pass
		}
		if r.answered {
			st.fails = 0
			d.recover(key, Alert{ProjectID: v.ID, Target: name, Message: Message{Title: domain + " (" + name + ") is answering again"}})
			continue
		}
		st.fails++
		if st.fails >= domainFailures {
			d.raise(key, Alert{ProjectID: v.ID, Action: store.ActAlertDomain, Target: name,
				Message: Message{Title: fmt.Sprintf("%s (%s) did not answer %d checks", domain, name, domainFailures), Detail: r.words}})
		}
	}
}

// sweep closes, silently, the open incidents nothing watches any more: a disk
// that left the list, a project no longer tracked and present, a public
// domain cleared. A source that could not be read sweeps nothing, and the
// fallback disk row counts as an unread disk list.
func (w *Watcher) sweep(in observed, d *decision) {
	var mounts map[string]bool
	if in.disksReal {
		mounts = map[string]bool{}
		for _, dk := range in.disks {
			mounts[dk.Mount] = true
		}
	}
	present := presentProjects(in)
	for key := range in.open {
		switch {
		case strings.HasPrefix(key, keyDiskPfx):
			if mounts != nil && !mounts[strings.TrimPrefix(key, keyDiskPfx)] {
				d.close(key)
			}
		case strings.HasPrefix(key, keyProjPfx):
			id, kind, ok := parseProjectKey(key)
			if !ok || present == nil {
				continue
			}
			t, tracked := present[id]
			if !tracked || (kind == "domain" && t.PublicDomain == "") {
				d.close(key)
			}
		}
	}
}

// forget drops the state of what is no longer watched: projects no longer
// tracked and present, disks that left the list.
func (w *Watcher) forget(in observed) {
	present := presentProjects(in)
	if present != nil {
		for id := range w.errFirst {
			if _, ok := present[id]; !ok {
				delete(w.errFirst, id)
			}
		}
		for id := range w.domains {
			if _, ok := present[id]; !ok {
				delete(w.domains, id)
			}
		}
	}
	for key := range w.over {
		switch {
		case strings.HasPrefix(key, keyProjPfx):
			if id, _, ok := parseProjectKey(key); ok && present != nil {
				if _, tracked := present[id]; !tracked {
					delete(w.over, key)
				}
			}
		case strings.HasPrefix(key, keyDiskPfx) && in.disksReal:
			if !hasMount(in.disks, strings.TrimPrefix(key, keyDiskPfx)) {
				delete(w.over, key)
			}
		}
	}
}

// presentProjects are the tracked projects whose folder is present, by id;
// nil when the list could not be read.
func presentProjects(in observed) map[int64]store.Tracked {
	if !in.trackedOK {
		return nil
	}
	out := make(map[int64]store.Tracked, len(in.tracked))
	for _, t := range in.tracked {
		if t.MissingSince == nil {
			out[t.ID] = t
		}
	}
	return out
}

func hasMount(disks []metrics.Disk, mount string) bool {
	for _, dk := range disks {
		if dk.Mount == mount {
			return true
		}
	}
	return false
}

// projectKey is a project incident's key: "project:<id>:<kind>", kind one of
// error, domain, cpu and ram.
func projectKey(id int64, kind string) string {
	return keyProjPfx + strconv.FormatInt(id, 10) + ":" + kind
}

func parseProjectKey(key string) (id int64, kind string, ok bool) {
	rest, found := strings.CutPrefix(key, keyProjPfx)
	if !found {
		return 0, "", false
	}
	idPart, kind, found := strings.Cut(rest, ":")
	if !found {
		return 0, "", false
	}
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return id, kind, true
}

// issueOf is what an Error alert names, in the order the Dashboard's issue.ts
// uses: the highest restart count, else the first container with an exit
// reason, else how many containers run.
func issueOf(comps []project.Component) string {
	var name string
	most := 0
	for _, c := range comps {
		if c.RestartCount > most {
			name, most = c.Name, c.RestartCount
		}
	}
	if most > 0 {
		word := "times"
		if most == 1 {
			word = "time"
		}
		return fmt.Sprintf("%s restarted %d %s", name, most, word)
	}
	for _, c := range comps {
		if c.ExitReason != "" {
			return c.Name + ": " + c.ExitReason
		}
	}
	if len(comps) == 0 {
		return "Not running"
	}
	running := 0
	for _, c := range comps {
		if c.State == "running" {
			running++
		}
	}
	return fmt.Sprintf("%d of %d containers running", running, len(comps))
}

// percent is a share rounded to a whole number: "92".
func percent(v float64) string { return strconv.FormatFloat(math.Round(v), 'f', 0, 64) }

// minutes is the sustained time in whole minutes: "5 min".
func minutes(d time.Duration) string { return strconv.Itoa(int(d/time.Minute)) + " min" }

// memory is a RAM size, binary units: "3.1 GB", "512 MB".
func memory(b int64) string {
	if b >= 1<<30 {
		return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + " GB"
	}
	return strconv.FormatFloat(float64(b)/(1<<20), 'f', 0, 64) + " MB"
}
