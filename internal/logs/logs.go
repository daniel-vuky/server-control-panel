// Package logs keeps one shared docker compose logs stream per (project,
// service or all): a backlog of the newest lines, then a follow while a
// container of the selection runs, fanned out to every viewer in batches.
// It runs commands only through the Runner seam (internal/runner).
package logs

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

const (
	// MaxFollows is how many streams may hold a follow slot at once, panel-wide.
	MaxFollows = 3
	// RingSize is how many of its newest lines a stream keeps for late viewers.
	RingSize = 500
	// BatchEvery is the most often a viewer is sent a batch of lines.
	BatchEvery = 200 * time.Millisecond
	// subBuffer is how many batches a viewer may fall behind before it is
	// ended slow.
	subBuffer = 16
)

// The reasons a viewer's stream ends.
const (
	ReasonExited = "exited" // nothing to follow, or the follow exited on its own
	ReasonSlow   = "slow"   // the viewer fell subBuffer batches behind
	ReasonError  = "error"  // the backlog or the follow failed; Message says why
)

var (
	// ErrTooMany refuses a new stream while MaxFollows streams run.
	ErrTooMany = errors.New("logs: too many log streams")
	// ErrDockerDown refuses a new stream while Docker cannot be read.
	ErrDockerDown = errors.New("logs: docker is not responding")
)

// Runner runs the registry's commands; *runner.Runner satisfies it.
type Runner interface {
	Run(ctx context.Context, s runner.Stack, a runner.Action, v runner.Values, secrets []string) (runner.Result, error)
	Stream(ctx context.Context, s runner.Stack, a runner.Action, v runner.Values, secrets []string, onLine func(string)) error
}

// Containers returns Docker's container list, ok false when Docker is down
// (control.Hub.Containers). The list is only read.
type Containers func(ctx context.Context) (cs []project.Container, ok bool)

// EndReason is why a viewer's stream ended. Message is set, and masked, for
// ReasonError.
type EndReason struct {
	Reason  string
	Message string
}

type key struct {
	project int64
	service string // "" is every service
}

// Manager owns the shared streams. Its mutex guards only the key map and the
// reservation count, and is never held during a command or a container read.
// Lock order: Manager.mu, then stream.mu.
type Manager struct {
	r   Runner
	cs  Containers
	now func() time.Time
	log *slog.Logger

	// Tests shorten or replace the batch ticker.
	batchEvery time.Duration
	newTicker  func(time.Duration) (<-chan time.Time, func())

	mu       sync.Mutex
	streams  map[key]*stream
	reserved int // streams that hold a follow slot, from Subscribe to their end
}

// New returns a Manager that runs commands with r, reads containers with cs and
// takes the follow's --since from now (time.Now when nil).
func New(r Runner, cs Containers, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{
		r: r, cs: cs, now: now, log: slog.Default(),
		batchEvery: BatchEvery,
		newTicker: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
		streams: map[key]*stream{},
	}
}

// Sub is one viewer of a stream. Lines carries batches, the first being the
// backlog or, for a joiner, the stream's ring (possibly empty). When the
// stream ends for this viewer, End receives the reason and then Lines is
// closed, so a viewer reads Lines until it is closed and then End. Cancel
// leaves the stream; it closes both channels with no reason sent.
type Sub struct {
	Lines <-chan []Line
	End   <-chan EndReason

	lines chan []Line
	end   chan EndReason
	s     *stream
	once  sync.Once
}

// Cancel leaves the stream. The last viewer to leave kills the follow at once.
// It may be called more than once, and after the end.
func (sub *Sub) Cancel() {
	sub.once.Do(func() { sub.s.release(sub) })
}

// Subscribe joins the stream of t's service ("" for every service), starting
// it when none runs. Joining a running stream always succeeds. Starting one
// first reads the containers with ctx (ErrDockerDown when Docker is down), then
// takes a follow slot (ErrTooMany at MaxFollows); nothing runs on a refusal.
// The stream itself outlives ctx: it ends on its own or with the last Cancel.
func (m *Manager) Subscribe(ctx context.Context, t store.Tracked, service string) (*Sub, error) {
	k := key{t.ID, service}
	if sub := m.join(k); sub != nil {
		return sub, nil
	}
	cs, ok := m.cs(ctx)
	if !ok {
		return nil, ErrDockerDown
	}

	m.mu.Lock()
	if s := m.streams[k]; s != nil { // started while the containers were read
		sub := s.add()
		m.mu.Unlock()
		return sub, nil
	}
	if m.reserved >= MaxFollows {
		m.mu.Unlock()
		return nil, ErrTooMany
	}
	m.reserved++
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s := &stream{m: m, key: k, t: t, cs: cs, since: m.now(), ctx: sctx, cancel: cancel, subs: map[*Sub]struct{}{}}
	sub := s.add()
	m.streams[k] = s
	m.mu.Unlock()

	go s.run()
	return sub, nil
}

// join adds a viewer to the running stream of k, or returns nil.
func (m *Manager) join(k key) *Sub {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.streams[k]; s != nil {
		return s.add()
	}
	return nil
}

// lineID identifies a line for the backlog-to-follow hand-off.
type lineID struct {
	container string
	time      time.Time
	text      string
}

type stream struct {
	m      *Manager
	key    key
	t      store.Tracked
	cs     []project.Container // read when the stream started
	since  time.Time
	ctx    context.Context
	cancel context.CancelFunc
	ended  sync.Once

	mu      sync.Mutex
	subs    map[*Sub]struct{}
	ready   bool // the backlog was delivered
	ring    []Line
	pending []Line // live lines since the last batch
	seen    map[lineID]struct{}
	done    bool // every viewer was ended
	closing bool // the last viewer left; the stream is out of the map
}

// add creates a viewer. Called with m.mu held, so the stream is in the map and
// not closing; a stream that finishes meanwhile ends this viewer too.
func (s *stream) add() *Sub {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := &Sub{lines: make(chan []Line, subBuffer), end: make(chan EndReason, 1), s: s}
	sub.Lines, sub.End = sub.lines, sub.end
	if s.ready {
		sub.lines <- append([]Line{}, s.ring...)
	}
	s.subs[sub] = struct{}{}
	return sub
}

// release removes sub (nil to only check) and, when no viewer is left, takes
// the stream out of the map and cancels it, which kills the follow.
func (s *stream) release(sub *Sub) {
	m := s.m
	m.mu.Lock()
	s.mu.Lock()
	if _, ok := s.subs[sub]; ok {
		delete(s.subs, sub)
		close(sub.end)
		close(sub.lines)
	}
	last := len(s.subs) == 0 && !s.done && !s.closing
	if last {
		s.closing = true
		if m.streams[s.key] == s {
			delete(m.streams, s.key)
		}
	}
	s.mu.Unlock()
	m.mu.Unlock()
	if last {
		s.cancel()
	}
}

// endSub sends the reason and closes the viewer's channels. s.mu is held.
func endSub(sub *Sub, r EndReason) {
	sub.end <- r
	close(sub.end)
	close(sub.lines)
}

// finish ends every viewer with r, frees the key and the follow slot. Only the
// first call counts.
func (s *stream) finish(r EndReason) {
	s.ended.Do(func() {
		m := s.m
		m.mu.Lock()
		if m.streams[s.key] == s {
			delete(m.streams, s.key)
		}
		m.reserved--
		m.mu.Unlock()

		s.mu.Lock()
		s.done = true
		for sub := range s.subs {
			endSub(sub, r)
		}
		clear(s.subs)
		s.mu.Unlock()
		s.cancel()
	})
}

func (s *stream) run() {
	defer func() {
		if r := recover(); r != nil {
			s.m.log.Error("logs: stream panicked", "project", s.t.ID, "panic", r, "stack", string(debug.Stack()))
			s.finish(EndReason{Reason: ReasonError, Message: "Reading the logs failed."})
		}
	}()
	values, override, ok := s.t.ComposeValues()
	if !ok {
		s.finish(EndReason{Reason: ReasonError, Message: "The project has no compose file."})
		return
	}
	if s.key.service != "" {
		values["service"] = s.key.service
	}
	secrets := scan.SecretValues(scan.ReadDotenv(filepath.Join(s.t.Path, ".env")))

	tail, follow := actions(override, s.key.service != "")
	res, err := s.m.r.Run(s.ctx, runner.StackCompose, tail, values, secrets)
	if err != nil {
		s.finish(s.failure(err, secrets))
		return
	}
	s.deliverBacklog(s.parseBacklog(res, secrets))
	if !s.active() || s.ctx.Err() != nil { // nothing to follow, or every viewer left
		s.finish(EndReason{Reason: ReasonExited})
		return
	}

	fv := maps.Clone(values)
	fv["since"] = s.since.UTC().Format(time.RFC3339Nano)
	err = s.follow(follow, fv, secrets)
	if err == nil {
		s.finish(EndReason{Reason: ReasonExited})
		return
	}
	s.finish(s.failure(err, secrets))
}

// failure is the end of a failed command: nothing to report when every viewer
// left (the cancel caused it), else the masked error.
func (s *stream) failure(err error, secrets []string) EndReason {
	if s.ctx.Err() != nil {
		return EndReason{Reason: ReasonExited}
	}
	return EndReason{Reason: ReasonError, Message: runner.Mask(err.Error(), secrets)}
}

// follow runs the follow command with the batch ticker, then sends what is
// left pending.
func (s *stream) follow(a runner.Action, v runner.Values, secrets []string) error {
	ticks, stopTicker := s.m.newTicker(s.m.batchEvery)
	quit, flusherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(flusherDone)
		for {
			select {
			case <-ticks:
				s.flush()
			case <-quit:
				return
			}
		}
	}()
	defer func() {
		close(quit)
		<-flusherDone
		stopTicker()
		s.flush()
	}()
	return s.m.r.Stream(s.ctx, runner.StackCompose, a, v, secrets, func(raw string) {
		// The runner masked the line already; masking again keeps the rule
		// in this package whatever the Runner is.
		l, ok := ParseLine(runner.Mask(raw, secrets), s.t.ComposeProject, s.cs)
		if !ok {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, dup := s.seen[idOf(l)]; dup {
			return
		}
		if len(s.pending) >= RingSize { // a batch never carries more than the ring
			s.pending = s.pending[1:]
		}
		s.pending = append(s.pending, l)
	})
}

// parseBacklog masks and parses the backlog output, sorts it by time and keeps
// the newest RingSize lines. A truncated output loses its last line, which may
// be cut through a secret the mask cannot recognise.
func (s *stream) parseBacklog(res runner.Result, secrets []string) []Line {
	raw := strings.Split(string(res.Stdout), "\n")
	if res.Truncated && len(raw) > 0 {
		raw = raw[:len(raw)-1]
	}
	lines := make([]Line, 0, len(raw))
	for _, r := range raw {
		if l, ok := ParseLine(runner.Mask(r, secrets), s.t.ComposeProject, s.cs); ok {
			lines = append(lines, l)
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Time.Before(lines[j].Time) })
	if len(lines) > RingSize {
		lines = append([]Line{}, lines[len(lines)-RingSize:]...)
	}
	return lines
}

// deliverBacklog makes the backlog the ring and sends it as every viewer's
// first batch.
func (s *stream) deliverBacklog(backlog []Line) {
	seen := make(map[lineID]struct{}, len(backlog))
	for _, l := range backlog {
		seen[idOf(l)] = struct{}{}
	}
	s.mu.Lock()
	s.ring, s.seen, s.ready = backlog, seen, true
	for sub := range s.subs {
		sub.lines <- append([]Line{}, backlog...) // the first batch: the buffer is empty
	}
	s.mu.Unlock()
}

// flush sends the pending lines as one batch to every viewer and adds them to
// the ring. A viewer whose buffer is full is ended slow.
func (s *stream) flush() {
	s.mu.Lock()
	if len(s.pending) == 0 || s.done {
		s.mu.Unlock()
		return
	}
	batch := s.pending
	s.pending = nil
	s.ring = append(s.ring, batch...)
	if n := len(s.ring); n > RingSize {
		s.ring = append([]Line{}, s.ring[n-RingSize:]...)
	}
	dropped := false
	for sub := range s.subs {
		select {
		case sub.lines <- batch:
		default:
			delete(s.subs, sub)
			endSub(sub, EndReason{Reason: ReasonSlow})
			dropped = true
		}
	}
	s.mu.Unlock()
	if dropped {
		s.release(nil) // stops the follow when that was the last viewer
	}
}

// active says whether a container of the selection runs or restarts.
func (s *stream) active() bool {
	cs := s.cs
	if s.key.service != "" {
		cs = nil
		for _, c := range s.cs {
			if c.Service == s.key.service {
				cs = append(cs, c)
			}
		}
	}
	return project.AnyActive(cs, s.t.ComposeProject)
}

func idOf(l Line) lineID { return lineID{l.Container, l.Time, l.Text} }

// actions are the backlog and follow actions for the project's files and the
// selection.
func actions(override, service bool) (tail, follow runner.Action) {
	switch {
	case override && service:
		return runner.ComposeLogsTailServiceOverride, runner.ComposeLogsServiceOverride
	case override:
		return runner.ComposeLogsTailOverride, runner.ComposeLogsOverride
	case service:
		return runner.ComposeLogsTailService, runner.ComposeLogsService
	}
	return runner.ComposeLogsTail, runner.ComposeLogs
}
