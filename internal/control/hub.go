package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// Variables only so that tests can shorten them.
var (
	// tickInterval is the pause between two hub ticks while someone subscribes.
	tickInterval = 5 * time.Second
	// recentMax is how old the last tick may be for a new subscriber's state.
	recentMax = 10 * time.Second
	// metricsInterval is the least time between the starts of two metrics
	// reads while someone subscribes.
	metricsInterval = 10 * time.Second
	// metricsMaxAge is how long failed metrics reads keep the last values.
	metricsMaxAge = 30 * time.Second
)

const (
	// maxTickReads is how many container reads one tick makes, in all, while
	// lock changes keep making its answer stale.
	maxTickReads = 3
	// maxInspectIDs is the most container ids the runner takes in one
	// docker.inspect call.
	maxInspectIDs = 1000
)

// errNoRead stands for a container list the hub could not read.
var errNoRead = errors.New("control: the container list could not be read")

// ProjectView is a tracked project with its effective status, the actions it
// allows, its containers and its totals. Components is empty (never nil) when
// Docker could not be read. CPUPercent and MemBytes are nil when no running
// container has a value; UpSince is nil unless the status is Running or
// Partial and a running container's start time is known. DatabaseViews has one
// entry per detected database, in Tracked.Databases order (empty, never nil).
type ProjectView struct {
	store.Tracked
	Status        project.Status
	Actions       []project.Action
	Components    []project.Component
	CPUPercent    *float64
	MemBytes      *int64
	UpSince       *time.Time
	DatabaseViews []DatabaseView
}

// DatabaseView is a detected database with its key (project.DatabaseKey) and
// whether it can be backed up now.
type DatabaseView struct {
	project.Database
	Key    string
	Backup BackupRule
}

// BackupRule says whether a backup may start now. Allowed is true only when
// Reason is "". Reason is one of ReasonNotFound, project.ReasonNotSupported,
// project.ReasonNoDBName, project.ReasonDBNotRunning, ReasonDriveNotConnected,
// ReasonDriveExpired or ReasonFolderMissing. Each equals the backup manager's
// refusal code of the same name. Service is the compose service
// project.BackupReason resolved ("" when it resolved none, or for not_found),
// so the words can name it ("Start <service> first").
type BackupRule struct {
	Allowed bool
	Reason  string
	Service string
}

// The reasons a database cannot be backed up, layered around
// project.BackupReason: a project whose folder is missing, then the Drive
// reasons.
const (
	ReasonNotFound          = "not_found"
	ReasonDriveNotConnected = "drive_not_connected"
	ReasonDriveExpired      = "drive_expired"
	ReasonFolderMissing     = "folder_missing"
)

// State is one snapshot of the tracked projects, the active backups (queued
// and running, from the backup source) and the Drive connection. Subscribers
// share it, so they must not modify it.
type State struct {
	DockerUp bool
	Projects []ProjectView
	Backups  []store.Backup
	Drive    drive.Status
}

// Views gives each tracked project its effective status and allowed actions:
// the in-flight status while a command runs on it, else Not found when a
// rescan could not find its folder, else unknown when Docker is down or the
// containers could not be read (readErr), else its status from cs. A project
// held for a store write keeps that computed status and allows no action.
// lock has Controller.Lock's shape and may be nil.
//
// When cs was read, each view also lists the project's containers with the
// inspect and usage data in insp and use (keyed by container id; either may
// be nil) and sums them; otherwise it has no component and no metrics.
//
// Each detected database gets its backup rule (databaseViews) from cs, with
// Docker down or a failed read counting as not running, and from drv.
func Views(ts []store.Tracked, cs []project.Container, readErr error, dockerUp bool,
	lock func(int64) (status project.Status, held bool, ok bool),
	insp map[string]project.Inspect, use map[string]project.Usage, drv drive.Status) []ProjectView {
	read := dockerUp && readErr == nil
	out := make([]ProjectView, 0, len(ts))
	for _, t := range ts {
		s, held, locked := lookup(lock, t.ID)
		status := project.StatusUnknown
		switch {
		case locked && !held:
			status = s
		case t.MissingSince != nil:
			status = project.StatusNotFound
		case dockerUp && readErr == nil:
			status = project.StatusOf(cs, t.ComposeProject, t.Services)
		}
		actions := project.Allowed(status, t.Self, dockerUp)
		if held {
			actions = []project.Action{}
		}
		v := ProjectView{Tracked: t, Status: status, Actions: actions, Components: []project.Component{},
			DatabaseViews: databaseViews(t, cs, read, drv)}
		if read {
			v.Components = project.Components(cs, t.ComposeProject, t.Services, insp, use)
			v.CPUPercent, v.MemBytes, v.UpSince = project.Totals(v.Components)
			if status != project.StatusRunning && status != project.StatusPartial {
				v.UpSince = nil
			}
		}
		out = append(out, v)
	}
	return out
}

// databaseViews gives each database of t its key and backup rule: not_found
// for a project whose folder is missing (the backup manager refuses it), else
// project.BackupReason, then, only when that allows it, the Drive reason.
//
// The database is checked before Drive on purpose: the reason is what the UI
// names as the first thing to fix, and a fix on the project itself (start the
// service, set the name) comes before connecting Drive.
func databaseViews(t store.Tracked, cs []project.Container, dockerUp bool, drv drive.Status) []DatabaseView {
	out := make([]DatabaseView, 0, len(t.Databases))
	for _, d := range t.Databases {
		reason, service := ReasonNotFound, ""
		if t.MissingSince == nil {
			service, reason = project.BackupReason(d, t.ComposeProject, t.ServiceDetails, cs, dockerUp)
		}
		if reason == "" {
			reason = driveReason(drv)
		}
		out = append(out, DatabaseView{Database: d, Key: project.DatabaseKey(d),
			Backup: BackupRule{Allowed: reason == "", Reason: reason, Service: service}})
	}
	return out
}

// driveReason is the reason Drive gives for refusing a backup, "" when it is
// connected with a folder. Not configured, not connected or an unknown state
// is drive_not_connected, expired is drive_expired, and connected without a
// folder is folder_missing.
func driveReason(drv drive.Status) string {
	switch drv.State {
	case drive.StateConnected:
		if drv.Folder == nil {
			return ReasonFolderMissing
		}
		return ""
	case drive.StateExpired:
		return ReasonDriveExpired
	default:
		return ReasonDriveNotConnected
	}
}

func lookup(lock func(int64) (project.Status, bool, bool), id int64) (project.Status, bool, bool) {
	if lock == nil {
		return "", false, false
	}
	return lock(id)
}

// tickRun is one tick in progress. Callers that find it wait for done, then
// read state; ok is false when the tick was abandoned (its context ended) or
// stale (Refresh or storeChanged said the store changed while it ran, so it
// published nothing and another tick must run).
type tickRun struct {
	done  chan struct{}
	state State
	ok    bool
	stale bool
}

// Hub reads Docker once for every subscriber and broadcasts the snapshots
// (ADR 0003). While at least one subscriber is connected its loop ticks every
// tickInterval; with none it does no work. Ticks never overlap: Check during a
// tick waits for that tick's result. Each tick reads docker.containers, then
// docker.inspect for the containers of tracked, present projects.
//
// Metrics (docker.stats, which takes a second or two) are read by a second
// loop that runs alongside the tick loop: never inside a tick, never two at
// once, at most every metricsInterval. Inspect and metrics data are kept in
// maps keyed by container id, so a lock release's re-read (docker.containers
// only) does not blank them.
//
// Lock order: the hub calls the Controller's Lock and Generation while it
// holds mu (hub.mu → ctl.mu), and the backup and Drive sources while it builds
// a state (hub.mu → mgr.mu, hub.mu → drive.mu); it never runs Docker or reads
// the store with mu held. The Controller calls lockChanged and storeChanged
// without ctl.mu held (a project lock may be held: that is no mutex).
type Hub struct {
	st  *store.Store
	cmd scan.Commander
	ctl *Controller
	log *slog.Logger

	// The optional in-memory sources a state is built with, set once before
	// the first Subscribe. Both are called with mu held, so neither may call
	// the hub, read the store or run a command.
	backups  func() []store.Backup
	driveNow func() drive.Status

	loops sync.WaitGroup // running loops; tests wait on it

	// loopStart, when set (tests only, before the first Subscribe), runs at
	// the start of each tick loop goroutine, so a test can hold the loop
	// until Subscribe has returned.
	loopStart func()

	mu       sync.Mutex
	subs     map[chan State]struct{}
	stopLoop context.CancelFunc // non-nil while the loop runs
	running  *tickRun           // the tick in progress, or nil
	tsGen    uint64             // bumped by a store change; a tick that saw it change publishes nothing

	// The cache: the tracked projects and the last container list, as the
	// last tick or lock release left them.
	ready    bool // a tick has finished, so ts is loaded
	ts       []store.Tracked
	cs       []project.Container
	csOK     bool // cs is a successful read
	dockerUp bool
	// dockerSeen: a tick has checked Docker, so dockerUp is known.
	dockerSeen bool
	at         time.Time // when cs (or Docker down) was last seen

	lastSent []byte // the JSON of the last state broadcast

	// cacheGen is the Controller generation the cache last matched (a tick
	// or a lock change published it). Between a lock change and its
	// lockChanged call the cached views may not match the locks, so the
	// cache-only publishes (Publish, the metrics merge) wait for it.
	cacheGen uint64
	// cacheTS is the tsGen the cached tracked list was read under. While it
	// is behind tsGen the store changed after that read: the cached list may
	// still hold rows the store dropped, so nothing publishes it until a
	// tick has read the store again (see storeCurrentLocked).
	cacheTS uint64

	// Container details and metrics by container id.
	insp         map[string]project.Inspect
	use          map[string]project.Usage
	useAt        time.Time // when use was last read
	metricsBusy  bool      // a docker.stats read is running
	metricsStart time.Time // when the last docker.stats read began
}

func newHub(st *store.Store, cmd scan.Commander, ctl *Controller, log *slog.Logger) *Hub {
	return &Hub{st: st, cmd: cmd, ctl: ctl, log: log, subs: map[chan State]struct{}{}}
}

// SetBackupSource sets where states get the active backups (the backup
// manager's Active). Call it before the first Subscribe. src is called with
// the hub's mutex held: it must be in-memory and never call the hub.
func (h *Hub) SetBackupSource(src func() []store.Backup) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.backups = src
}

// SetDriveSource sets where states get the Drive connection (the Drive
// service's Status). Call it before the first Subscribe. src is called with
// the hub's mutex held: it must be in-memory and never call the hub. Without
// a source, Drive is not connected.
func (h *Hub) SetDriveSource(src func() drive.Status) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.driveNow = src
}

// Publish rebuilds the state from the cache, with the sources read again, and
// broadcasts it when it changed; the backup manager and the Drive service call
// it from their OnChange, never with their own lock held. It reads neither
// Docker nor the store. Like the metrics merge it publishes only while the
// cache is current (cacheCurrentLocked); otherwise the next tick or lock
// change, which reads the sources again, carries the change.
func (h *Hub) Publish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cacheCurrentLocked() {
		h.publishLocked()
	}
}

// cacheCurrentLocked reports whether a state built from the cache alone may
// be published: the store is current (storeCurrentLocked) and the cache
// matches the Controller's locks. h.mu must be held.
func (h *Hub) cacheCurrentLocked() bool {
	return h.storeCurrentLocked() && h.cacheGen == h.ctl.Generation()
}

// storeCurrentLocked reports whether a tick has loaded the cache and no store
// change came after its tracked list was read. h.mu must be held.
func (h *Hub) storeCurrentLocked() bool {
	return h.ready && h.cacheTS == h.tsGen
}

// RecentContainers returns the cached container list when it is at most
// maxAge old, else reads Docker as Containers does (sharing a tick in
// progress). ok is false when Docker is down or the list could not be read;
// cs is then nil. A recent Docker-down check is reused too. Callers must not
// modify cs.
func (h *Hub) RecentContainers(ctx context.Context, maxAge time.Duration) (cs []project.Container, ok bool) {
	if cs, up, fresh := h.recent(maxAge); fresh {
		if !up {
			return nil, false
		}
		return cs, true
	}
	return h.Containers(ctx)
}

// Subscribe returns the current state and a channel of later ones. The
// channel holds the latest state not yet received; it is never closed. The
// first subscriber starts the tick and metrics loops and the last cancel stops
// them. The current state comes from the last tick when it is at most
// recentMax old (and no store change came after it), otherwise from one fresh
// tick.
//
// The tick loop starts only once that state is built, and its first tick
// comes tickInterval after the last read (firstWaitLocked): a loop started
// earlier could run its first tick right after Subscribe's own and read
// Docker twice.
func (h *Hub) Subscribe() (current State, updates <-chan State, cancel func()) {
	ch := make(chan State, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	var loopCtx context.Context // set when this call starts the loops
	if h.stopLoop == nil {
		var stop context.CancelFunc
		loopCtx, stop = context.WithCancel(context.Background())
		h.stopLoop = stop
		h.loops.Add(2)
		go h.metricsLoop(loopCtx)
	}
	fresh := h.storeCurrentLocked() && time.Since(h.at) <= recentMax
	h.mu.Unlock()
	if !fresh {
		h.shared(context.Background())
	}

	h.mu.Lock()
	current = h.publishLocked()
	select { // current is at least as new as anything already queued
	case <-ch:
	default:
	}
	if loopCtx != nil { // counted in h.loops above; a cancel meanwhile ends it at once
		go h.loop(loopCtx, h.firstWaitLocked())
	}
	h.mu.Unlock()

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.subs, ch)
			if len(h.subs) == 0 && h.stopLoop != nil {
				h.stopLoop()
				h.stopLoop = nil
			}
		})
	}
	return current, ch, cancel
}

// Check reads Docker now and broadcasts the result when it changed. When a
// tick is in progress it waits for that tick and returns its state instead of
// starting a second read. When ctx ends first it returns the cached state.
func (h *Hub) Check(ctx context.Context) State {
	return h.shared(ctx)
}

// Containers reads Docker as Check does (sharing a tick in progress) and
// returns the container list it holds then. ok is false when Docker is down or
// the list could not be read; cs is then nil. Callers must not modify cs.
func (h *Hub) Containers(ctx context.Context) (cs []project.Container, ok bool) {
	h.Check(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.csOK || !h.dockerUp {
		return nil, false
	}
	return h.cs, true
}

// Refresh makes the next published state reflect the store, without waiting;
// call it after a change to the tracked list made outside WithLocks (which
// tells the hub itself). With no subscriber it only marks the cache not
// ready, so the next Subscribe reads fresh (the hub never reads Docker without
// a subscriber). While a tick runs, that tick may have read the store before
// the change: it publishes nothing and one more tick runs at once. Otherwise
// it starts a tick on its own goroutine.
func (h *Hub) Refresh() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.markStoreChangedLocked()
	h.catchUpLocked()
}

// storeChanged is the Controller's call after a successful WithLocks write,
// with the write's locks still held. It marks the cache behind the store, so
// a tick that read the tracked list before the write publishes nothing. It
// starts no tick: the release's lockChanged does, once the locks are gone, so
// that tick need not read the containers again for the release.
func (h *Hub) storeChanged() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.markStoreChangedLocked()
}

// markStoreChangedLocked records a store change: every tick that read the
// tracked list before it is stale, and nothing publishes from the cache until
// a tick has read the store again (storeCurrentLocked). With no loop it also
// marks the cache not ready. h.mu must be held.
func (h *Hub) markStoreChangedLocked() {
	// Bumped in every case: a Check's tick can run with no subscriber, and
	// must not publish (or mark ready) a list read before the change.
	h.tsGen++
	if h.stopLoop == nil {
		h.ready = false
	}
}

// catchUpLocked makes sure a tick reads the store again: while a loop runs and
// no tick does, it starts one on its own goroutine. A tick in progress that
// read the store before the change is stale and its runner ticks again; a
// Check runner whose context ends, or the maxTickReads cap, can leave the
// cache behind until the next interval tick or lock change. h.mu must be held.
func (h *Hub) catchUpLocked() {
	if h.stopLoop == nil || h.running != nil {
		return
	}
	// Two quick calls may start two goroutines; the second finds the first
	// one's tick running and shares it.
	h.loops.Add(1)
	go func() {
		defer h.loops.Done()
		h.shared(context.Background())
	}()
}

// loop waits first, then ticks and waits tickInterval, until ctx ends. It
// never uses a Ticker, so a slow Docker stretches the cadence instead of
// piling up reads.
func (h *Hub) loop(ctx context.Context, first time.Duration) {
	defer h.loops.Done()
	if h.loopStart != nil {
		h.loopStart()
	}
	for wait := first; ; wait = tickInterval {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
		h.shared(ctx)
	}
}

// firstWaitLocked is how long a new tick loop waits before its first tick:
// what is left of tickInterval since the containers were last read when the
// cache is current, else 0. h.mu must be held.
func (h *Hub) firstWaitLocked() time.Duration {
	if !h.storeCurrentLocked() || h.at.IsZero() {
		return 0
	}
	return max(0, tickInterval-time.Since(h.at))
}

// shared runs one tick, or waits for the one in progress, and returns its
// state. If that tick was abandoned or stale, it runs its own; a stale tick it
// ran itself is run again at once, at most maxTickReads times in all.
func (h *Hub) shared(ctx context.Context) State {
	for ticks := 1; ; {
		h.mu.Lock()
		run := h.running
		if run == nil {
			run = &tickRun{done: make(chan struct{})}
			h.running = run
			h.mu.Unlock()
			h.runTick(ctx, run)
			if run.ok {
				return run.state
			}
			if run.stale && ctx.Err() == nil && ticks < maxTickReads {
				ticks++
				continue
			}
			return h.current()
		}
		h.mu.Unlock()
		select {
		case <-run.done:
		case <-ctx.Done():
			return h.current()
		}
		if run.ok {
			return run.state
		}
		if ctx.Err() != nil {
			return h.current()
		}
	}
}

// runTick runs the tick of run and always ends it. A panic in the tick is
// logged and recovered, so the loop goroutine cannot take the process down;
// the tick then counts as abandoned (ok false) and waiters run their own.
func (h *Hub) runTick(ctx context.Context, run *tickRun) {
	defer func() {
		r := recover()
		h.mu.Lock()
		if h.running == run { // the tick may have ended it already
			h.running = nil
		}
		h.mu.Unlock()
		if r != nil {
			h.log.Error("hub tick panicked", "panic", r, "stack", string(debug.Stack()))
			run.ok = false
		}
		close(run.done)
	}()
	run.state, run.ok, run.stale = h.tick(ctx, run)
}

// tick reads the tracked projects and Docker, then updates the cache and
// broadcasts in one critical section. If a lock changed during the read, the
// container list may predate the change: it is dropped and read again, at
// most maxTickReads times in all; after that the cached list (which the lock
// release refreshed) is published. If the store changed meanwhile (Refresh or
// storeChanged bumped tsGen), the tracked projects may predate the change:
// the tick changes and publishes nothing and returns stale. ok is false when
// ctx ended or the tick is stale. A tick that read the store without error
// records the tsGen it read under in cacheTS.
//
// The publish also ends run (h.running = nil) in the same critical section:
// a store change after it must start a new tick, since this one can no longer
// see the change.
func (h *Hub) tick(ctx context.Context, run *tickRun) (s State, ok, stale bool) {
	h.mu.Lock()
	tsGen := h.tsGen
	cachedTS := h.ts // whose containers to inspect when the store cannot be read
	h.mu.Unlock()
	gen := h.ctl.Generation()
	ts, tsErr := h.st.TrackedProjects(ctx)
	_, pingErr := h.cmd.Run(ctx, runner.StackDocker, runner.DockerPing, nil, nil)
	up := pingErr == nil
	if ctx.Err() != nil {
		return State{}, false, false
	}
	if tsErr != nil {
		h.log.Warn("hub: cannot read the tracked projects", "err", tsErr)
	}
	if !up {
		h.log.Debug("hub: Docker is not responding", "err", pingErr)
	}

	for reads := 1; ; reads++ {
		var cs []project.Container
		var readErr error
		var insp map[string]project.Inspect
		if up {
			cs, readErr = scan.ReadContainers(ctx, h.cmd)
			if ctx.Err() != nil {
				return State{}, false, false
			}
			if readErr != nil {
				h.log.Warn("hub: cannot read containers", "err", readErr)
			} else {
				tracked := ts
				if tsErr != nil {
					tracked = cachedTS
				}
				insp = h.inspect(ctx, cs, tracked)
				if ctx.Err() != nil {
					return State{}, false, false
				}
			}
		}

		h.mu.Lock()
		if h.tsGen != tsGen {
			h.mu.Unlock()
			return State{}, false, true
		}
		stale := h.ctl.Generation() != gen
		if stale && up && reads < maxTickReads {
			gen = h.ctl.Generation()
			h.mu.Unlock()
			continue
		}
		if tsErr == nil {
			h.ts = ts
		}
		// Without the tracked projects the first state would be an empty list,
		// so the hub is not ready until a tick has read them.
		if tsErr == nil || h.ready {
			h.ready = true
		}
		h.dockerUp, h.dockerSeen = up, true
		if !stale {
			h.cs, h.csOK, h.at = cs, up && readErr == nil, time.Now()
			if h.csOK {
				if len(insp) > 0 {
					h.insp = insp
				}
				listed := make(map[string]bool, len(cs))
				for _, c := range cs {
					listed[c.ID] = true
				}
				keepListed(h.insp, listed)
				keepListed(h.use, listed)
			}
		}
		h.cacheGen = h.ctl.Generation()
		if tsErr == nil {
			h.cacheTS = tsGen // checked equal to h.tsGen above
		}
		s = h.publishLocked()
		if h.running == run {
			h.running = nil
		}
		h.mu.Unlock()
		return s, true, false
	}
}

// inspect runs docker.inspect for the containers of the tracked, present
// projects in ts and returns the lines that parsed, or nil when there is no
// such container or no line parsed. The output of a failed run is still read:
// a container removed between docker ps and docker inspect makes it print the
// others' lines and exit 1, which is normal during a down or a recreate.
func (h *Hub) inspect(ctx context.Context, cs []project.Container, ts []store.Tracked) map[string]project.Inspect {
	present := map[string]bool{}
	for _, t := range ts {
		if t.MissingSince == nil {
			present[t.ComposeProject] = true
		}
	}
	var ids []string
	for _, c := range cs {
		if present[c.Project] && !c.OneOff && isContainerID(c.ID) {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxInspectIDs {
		ids = ids[:maxInspectIDs] // the rest keep their docker ps data
	}
	res, err := h.cmd.Run(ctx, runner.StackDocker, runner.DockerInspect, runner.Values{"ids": strings.Join(ids, " ")}, nil)
	in := project.ParseInspect(res.Stdout)
	if err != nil && ctx.Err() == nil {
		if len(in) == 0 {
			h.log.Warn("hub: cannot inspect containers", "err", err)
		} else {
			h.log.Debug("hub: docker inspect missed some containers", "err", err)
		}
	}
	if len(in) == 0 {
		return nil
	}
	return in
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

// keepListed drops the entries of m whose container id is not in listed.
func keepListed[V any](m map[string]V, listed map[string]bool) {
	for id := range m {
		if !listed[id] {
			delete(m, id)
		}
	}
}

// metricsLoop reads metrics until ctx ends, waiting between reads as
// readMetrics says. Like loop it never uses a Ticker.
func (h *Hub) metricsLoop(ctx context.Context) {
	defer h.loops.Done()
	for {
		wait := h.readMetrics(ctx)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

// readMetrics runs one docker.stats read, unless one is running (a loop that
// ended may have left it), the last one began less than metricsInterval ago
// or the last tick found Docker down, and returns how long to wait before the
// next try: what is left of metricsInterval since this read began, so reads
// begin metricsInterval apart however long each takes. It never holds h.mu
// during the read. A successful read replaces the metrics; a failed or skipped
// one (its context ending included) keeps them until they are metricsMaxAge
// old, then clears them. Either way the state is published when it changed. A
// panic is logged and recovered, as in a tick.
func (h *Hub) readMetrics(ctx context.Context) (wait time.Duration) {
	h.mu.Lock()
	if h.metricsBusy {
		h.mu.Unlock()
		return metricsInterval
	}
	if since := time.Since(h.metricsStart); !h.metricsStart.IsZero() && since < metricsInterval {
		h.mu.Unlock()
		return metricsInterval - since
	}
	if h.dockerSeen && !h.dockerUp {
		// docker stats would only fail; the values still age out.
		if h.expireMetricsLocked(time.Now()) && h.cacheCurrentLocked() {
			h.publishLocked()
		}
		h.mu.Unlock()
		return metricsInterval
	}
	start := time.Now()
	h.metricsBusy, h.metricsStart = true, start
	h.mu.Unlock()

	var (
		use map[string]project.Usage
		ok  bool
	)
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("hub metrics read panicked", "panic", r, "stack", string(debug.Stack()))
			use, ok = nil, false
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.metricsBusy = false
		now := time.Now()
		if ok {
			h.use, h.useAt = use, now
		} else {
			_ = h.expireMetricsLocked(now)
		}
		if h.cacheCurrentLocked() {
			h.publishLocked()
		}
		wait = max(0, metricsInterval-now.Sub(start))
	}()
	res, err := h.cmd.Run(ctx, runner.StackDocker, runner.DockerStats, nil, nil)
	switch {
	case ctx.Err() != nil:
		// The last subscriber left during the read: it counts as failed.
	case err != nil:
		h.log.Debug("hub: cannot read metrics", "err", err)
	case res.Truncated:
		h.log.Warn("hub: the metrics output is too large to read")
	default:
		use, ok = project.ParseStats(res.Stdout), true
	}
	return // the deferred func sets wait
}

// expireMetricsLocked clears the metrics once they are metricsMaxAge old and
// reports whether it did. Call it with h.mu held.
func (h *Hub) expireMetricsLocked(now time.Time) bool {
	if h.use == nil || now.Sub(h.useAt) <= metricsMaxAge {
		return false
	}
	h.use = nil
	return true
}

// recent returns the cached container list when it is at most maxAge old.
// When the last check found Docker down, it returns dockerUp false and ok. A
// failed read is not reused: ok is false.
func (h *Hub) recent(maxAge time.Duration) (cs []project.Container, dockerUp bool, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.at.IsZero() || time.Since(h.at) > maxAge {
		return nil, false, false
	}
	if !h.dockerUp {
		return nil, false, true
	}
	if !h.csOK {
		return nil, false, false
	}
	return h.cs, true, true
}

// lockChanged is called by the Controller after a lock take (fresh nil) or a
// release (fresh is the re-read list, nil when it failed). It publishes from
// the cache, unless the store changed after the cached list was read (the
// release of a successful WithLocks write, or a Refresh whose tick has not
// landed): that list may hold rows the store dropped, now unlocked, so it
// makes sure a tick runs instead (catchUpLocked), which publishes this change
// too. It neither reads the store nor runs Docker itself (a catch-up tick
// runs on its own goroutine), and must not panic: the Controller calls it
// from its release path.
func (h *Hub) lockChanged(fresh []project.Container) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if fresh != nil {
		// The inspect and metrics maps stay: the re-read has no such data.
		h.cs, h.csOK, h.dockerUp, h.at = fresh, true, true, time.Now()
	}
	h.cacheGen = h.ctl.Generation()
	switch {
	case h.storeCurrentLocked():
		h.publishLocked()
	case h.ready:
		h.catchUpLocked()
	}
}

// current builds the state from the cache, without broadcasting.
func (h *Hub) current() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buildLocked()
}

// buildLocked builds the state from the cache plus the in-flight overlay.
// h.mu must be held.
func (h *Hub) buildLocked() State {
	var readErr error
	if !h.csOK {
		readErr = errNoRead
	}
	drv := drive.Status{State: drive.StateNotConnected}
	if h.driveNow != nil {
		drv = h.driveNow()
	}
	backups := []store.Backup{}
	if h.backups != nil {
		if bs := h.backups(); bs != nil {
			backups = bs
		}
	}
	return State{
		DockerUp: h.dockerUp,
		Projects: Views(h.ts, h.cs, readErr, h.dockerUp, h.ctl.Lock, h.insp, h.use, drv),
		Backups:  backups,
		Drive:    drv,
	}
}

// stateKey is what publishLocked compares: the state plus the backups' queue
// positions, which store.Backup leaves out of its JSON.
type stateKey struct {
	State          State
	QueuePositions []int
}

// publishLocked builds the state and sends it to every subscriber when its
// JSON differs from the last one sent. Each subscriber keeps only the latest
// state. h.mu must be held; it is the only place that sends.
func (h *Hub) publishLocked() State {
	s := h.buildLocked()
	key := stateKey{State: s, QueuePositions: make([]int, len(s.Backups))}
	for i, b := range s.Backups {
		key.QueuePositions[i] = b.QueuePosition
	}
	b, err := json.Marshal(key)
	if err != nil {
		h.log.Error("hub: encode the state", "err", err)
		return s
	}
	if bytes.Equal(b, h.lastSent) {
		return s
	}
	h.lastSent = b
	for ch := range h.subs {
		select { // drop the older state not yet received
		case <-ch:
		default:
		}
		ch <- s // cannot block: only publishLocked sends, under h.mu
	}
	return s
}
