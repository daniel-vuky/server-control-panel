package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The notifier's limits (decisions 17 and 18).
const (
	queueSize   = 100              // queued alerts; a full queue drops with a Warn log
	retryAfter  = 30 * time.Second // a channel's one retry after a network error
	capAlerts   = 10               // at most this many alerts leave the panel…
	capWindow   = 5 * time.Minute  // …in this window
	sendTimeout = 30 * time.Second // a ceiling over the senders' own timeouts
)

// The words an alert's activity row can end with.
const (
	alertActor     = "System"
	wordsNoChannel = "No alert channel is on."
	wordsCapped    = "Too many alerts in 5 minutes; see Activity."
	wordsStopped   = "The panel stopped before the retry."
	wordsTurnedOff = "The channel was turned off before the retry."
	wordsNotSent   = "Could not send the alert."
)

var errSenderPanic = errors.New("alerts: a sender panicked")

// Alert is one alert to send. Key names the incident it opens ("server:cpu",
// "project:<id>:error"; "" for a one-off such as a failed backup). ProjectID
// is the project, 0 for none; Path is the panel page of an alert with no
// project ("/server", "/backups") and is ignored when ProjectID is set. Action
// and Target are the activity row's. The notifier writes Message.Footer and
// Message.Link; whatever the caller put there is replaced.
type Alert struct {
	Key                  string
	ProjectID            int64
	Action, Target, Path string
	Message              Message
}

// Targets gives the channels a message goes to now (Service.Targets).
type Targets interface {
	Targets() ([]Target, int)
}

// item is one queued entry: an alert, a recovery, or the silent close of an
// incident (close), which goes through the queue so it lands after a raise of
// the same key that is still waiting.
type item struct {
	a        Alert
	at       time.Time // when it was raised: the footer's time and the incident's
	recovery bool
	close    bool
}

// delivery is an alert being sent: its activity row and each channel's
// result; waiting counts its channels parked for their retry.
type delivery struct {
	a       Alert
	id      int64
	results []channelResult
	waiting int
}

type channelResult struct {
	name  string
	ok    bool
	words string
}

// parkedSend is a channel of a delivery waiting for its one retry.
type parkedSend struct {
	d         *delivery
	i         int
	notBefore time.Time
}

// Notifier delivers alerts: Raise and Recover only push onto an in-memory
// queue, and one worker records each alert in activity, persists its incident
// and sends it through every channel that is on. A channel that fails with a
// network error is parked and retried once 30 s later, without holding up the
// alerts behind it.
//
// The incident book (the open keys) is authoritative while the panel runs: a
// keyed Raise marks its key open before queueing and Recover clears it, so a
// check that runs while the worker is behind never raises a key twice; the
// worker only mirrors it into the store. mu guards the in-memory state (the
// queue, the book, the parked sends and the cap's send times) and is never
// held across a store call or a send. A delivery's results are the worker's
// alone, and Close's after the worker has stopped.
type Notifier struct {
	st      *store.Store
	targets Targets
	frame   Frame
	log     *slog.Logger
	after   func(time.Duration) <-chan time.Time // time.After; tests replace it before Start

	wake chan struct{}
	wg   sync.WaitGroup

	mu      sync.Mutex
	book    map[string]struct{}
	queue   []item
	alerts  int // queued alerts and recoveries (not closes), at most queueSize
	parked  []parkedSend
	sentAt  []time.Time // when the last capAlerts alerts left, oldest first
	started bool
	closed  bool
	cancel  context.CancelFunc
}

// NewNotifier returns a notifier; Start loads the open incidents and starts
// its worker.
func NewNotifier(st *store.Store, t Targets, f Frame, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	return &Notifier{
		st:      st,
		targets: t,
		frame:   f,
		log:     log,
		after:   time.After,
		wake:    make(chan struct{}, 1),
		book:    map[string]struct{}{},
	}
}

// Start loads the stored open incidents into the book (so a problem still
// there after a restart does not alert again) and starts the worker, which
// runs until Close or until ctx is done.
func (n *Notifier) Start(ctx context.Context) error {
	open, err := n.st.OpenIncidents(ctx)
	if err != nil {
		return fmt.Errorf("alerts: load incidents: %w", err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || n.started {
		return errors.New("alerts: the notifier was already started or closed")
	}
	for _, in := range open {
		n.book[in.Key] = struct{}{}
	}
	wctx, cancel := context.WithCancel(ctx)
	n.started, n.cancel = true, cancel
	n.wg.Add(1)
	go n.work(wctx)
	return nil
}

// Close stops the worker and sends nothing more: a send in flight is cut
// short, a parked channel counts as failed ("The panel stopped before the
// retry.") and the alerts still queued are recorded as interrupted. A keyed
// alert left queued was never stored as an incident, so after a restart it
// may alert once more. Raise and Recover after Close do nothing.
func (n *Notifier) Close() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.closed = true
	cancel := n.cancel
	n.mu.Unlock()
	if cancel != nil {
		cancel()
		n.wg.Wait()
	}
	n.drain()
}

// Raise queues an alert. It returns false, and queues nothing, when a.Key is
// already open, after Close, or when the queue is full (the key then stays
// open in memory, so it is not raised again while the problem lasts).
func (n *Notifier) Raise(a Alert) bool {
	at := n.frame.now()
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		n.log.Info("alerts: an alert raised after the notifier closed is not sent", "action", a.Action, "key", a.Key)
		return false
	}
	if a.Key != "" {
		if _, open := n.book[a.Key]; open {
			n.mu.Unlock()
			return false
		}
		n.book[a.Key] = struct{}{}
	}
	ok := n.enqueueLocked(item{a: a, at: at})
	n.mu.Unlock()
	if !ok {
		n.log.Warn("alerts: the queue is full; an alert was dropped", "action", a.Action, "key", a.Key)
	}
	return ok
}

// Recover queues the recovery of an open key: it clears the key from the book
// and sends a as a recovery (action alert.recovery, the ✅ title). It returns
// false when the key is not open, after Close, or when the queue is full.
func (n *Notifier) Recover(key string, a Alert) bool {
	at := n.frame.now()
	a.Key, a.Action, a.Message.Recovery = key, store.ActAlertRecovery, true
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		n.log.Info("alerts: a recovery after the notifier closed is not sent", "key", key)
		return false
	}
	if _, open := n.book[key]; !open {
		n.mu.Unlock()
		return false
	}
	delete(n.book, key)
	ok := n.enqueueLocked(item{a: a, at: at, recovery: true})
	n.mu.Unlock()
	if !ok {
		n.log.Warn("alerts: the queue is full; a recovery was dropped", "key", key)
	}
	return ok
}

// CloseSilently closes an open key with no message and no activity row (a
// project stopped on purpose or gone, a disk that left the list). The stored
// incident is closed by the worker, after any queued raise of the same key.
func (n *Notifier) CloseSilently(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	if _, open := n.book[key]; !open {
		return
	}
	delete(n.book, key)
	n.enqueueLocked(item{a: Alert{Key: key}, close: true})
}

// OpenKeys returns the open incident keys, sorted.
func (n *Notifier) OpenKeys() []string {
	n.mu.Lock()
	keys := make([]string, 0, len(n.book))
	for k := range n.book {
		keys = append(keys, k)
	}
	n.mu.Unlock()
	slices.Sort(keys)
	return keys
}

// enqueueLocked appends an item and wakes the worker; false when the alerts
// queue is full. A silent close is always taken: there is at most one per
// open key.
func (n *Notifier) enqueueLocked(it item) bool {
	if !it.close {
		if n.alerts >= queueSize {
			return false
		}
		n.alerts++
	}
	n.queue = append(n.queue, it)
	select {
	case n.wake <- struct{}{}:
	default:
	}
	return true
}

func (n *Notifier) work(ctx context.Context) {
	defer n.wg.Done()
	for ctx.Err() == nil {
		it, p, wait, ok := n.next()
		switch {
		case p != nil:
			n.retry(ctx, *p)
		case ok:
			n.handle(ctx, it)
		default:
			var timer <-chan time.Time // nil with nothing parked: wait for the queue
			if wait > 0 {
				timer = n.after(wait)
			}
			select {
			case <-ctx.Done():
			case <-n.wake:
			case <-timer:
			}
		}
	}
}

// next takes the next piece of work: a parked send that is due first, then
// the oldest queued item. With neither, wait is how long until the earliest
// parked send is due (0 with nothing parked).
func (n *Notifier) next() (it item, p *parkedSend, wait time.Duration, ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.parked) > 0 {
		now := n.frame.now()
		first := 0
		for i := range n.parked {
			if n.parked[i].notBefore.Before(n.parked[first].notBefore) {
				first = i
			}
		}
		if !n.parked[first].notBefore.After(now) {
			due := n.parked[first]
			n.parked = slices.Delete(n.parked, first, first+1)
			return item{}, &due, 0, false
		}
		wait = n.parked[first].notBefore.Sub(now)
	}
	if len(n.queue) > 0 {
		it = n.queue[0]
		n.queue[0] = item{}
		n.queue = n.queue[1:]
		if !it.close {
			n.alerts--
		}
		return it, nil, 0, true
	}
	return item{}, nil, wait, false
}

// handle records an alert, persists its incident change and sends it through
// every channel that is on; a channel parked for its retry leaves the row
// running until retry finishes it.
func (n *Notifier) handle(ctx context.Context, it item) {
	sctx := context.WithoutCancel(ctx) // a row is finished even while Close cuts the sends
	if it.close {
		n.closeIncident(sctx, it.a.Key)
		return
	}
	if ctx.Err() != nil { // taken just as Close began: as if still queued
		n.interrupt(sctx, it)
		return
	}
	a := it.a
	a.Message.Footer = n.frame.footerAt(it.at)
	a.Message.Link = n.frame.link(a.ProjectID, a.Path)
	d := &delivery{a: a, id: n.record(sctx, a)}
	if a.Key != "" {
		if it.recovery {
			n.closeIncident(sctx, a.Key)
		} else if err := n.st.OpenIncident(sctx, store.Incident{Key: a.Key, ProjectID: a.ProjectID, OpenedAt: it.at, ActivityID: d.id}); err != nil {
			n.log.Error("alerts: cannot store an incident", "key", a.Key, "error", err)
		}
	}
	targets, _ := n.targets.Targets()
	if len(targets) == 0 {
		n.end(sctx, d, store.ResultNotSent, nil, wordsNoChannel)
		return
	}
	if !n.allow() {
		n.end(sctx, d, store.ResultNotSent, nil, wordsCapped)
		return
	}
	d.results = make([]channelResult, len(targets))
	for i, t := range targets {
		d.results[i].name = t.Name
		n.send(ctx, d, i, t.Sender, true)
	}
	if d.waiting == 0 {
		n.finish(sctx, d)
	}
}

// allow takes a slot of the rate cap: false when capAlerts alerts already
// left within capWindow. A clock set back never blocks.
func (n *Notifier) allow() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.frame.now()
	if len(n.sentAt) == capAlerts {
		if since := now.Sub(n.sentAt[0]); since >= 0 && since < capWindow {
			return false
		}
		n.sentAt = append(n.sentAt[:0], n.sentAt[1:]...)
	}
	n.sentAt = append(n.sentAt, now)
	return true
}

// send delivers through one channel and records its result. A network
// failure on the first try parks the channel for one retry; any other
// failure, or one on the retry, is final.
func (n *Notifier) send(ctx context.Context, d *delivery, i int, s Sender, first bool) {
	r := &d.results[i]
	if ctx.Err() != nil {
		r.words = wordsStopped
		return
	}
	err := n.deliver(ctx, s, d.a.Message)
	switch {
	case err == nil:
		r.ok, r.words = true, ""
		return
	case ctx.Err() != nil: // cut short by Close, not an outage
		r.words = wordsStopped
		return
	}
	var se *SendError
	if !errors.As(err, &se) {
		// Not worded by a sender, so neither shown nor logged.
		n.log.Warn("alerts: an alert could not be sent", "channel", r.name)
		r.words = wordsNotSent
		return
	}
	n.log.Warn("alerts: an alert could not be sent", "channel", r.name, "code", se.Code, "retry", se.Retry && first)
	if se.Retry && first {
		n.mu.Lock()
		n.parked = append(n.parked, parkedSend{d: d, i: i, notBefore: n.frame.now().Add(retryAfter)})
		n.mu.Unlock()
		d.waiting++
		return
	}
	r.words = se.Words
}

// deliver runs one send under sendTimeout; a panicking sender is a failure.
func (n *Notifier) deliver(ctx context.Context, s Sender, m Message) (err error) {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	defer func() {
		if v := recover(); v != nil {
			n.log.Error("alerts: a sender panicked")
			err = errSenderPanic
		}
	}()
	return s.Send(ctx, m)
}

// retry is a parked channel's one retry, through the channel as it is now.
func (n *Notifier) retry(ctx context.Context, p parkedSend) {
	d := p.d
	name := d.results[p.i].name
	var s Sender
	targets, _ := n.targets.Targets()
	for _, t := range targets {
		if t.Name == name {
			s = t.Sender
		}
	}
	if s == nil {
		d.results[p.i].words = wordsTurnedOff
	} else {
		n.send(ctx, d, p.i, s, false)
	}
	d.waiting--
	if d.waiting == 0 {
		n.finish(context.WithoutCancel(ctx), d)
	}
}

// finish ends a delivery's row once every channel has its final result: sent
// when at least one delivered (the reason names the ones that failed), else
// failed with each channel's words.
func (n *Notifier) finish(ctx context.Context, d *delivery) {
	var channels, failed []string
	for _, r := range d.results {
		if r.ok {
			channels = append(channels, r.name)
		} else {
			failed = append(failed, channelWord(r.name)+": "+r.words)
		}
	}
	result := store.ResultFailed
	if len(channels) > 0 {
		result = store.ResultSent
	}
	n.end(ctx, d, result, channels, failed...)
}

// end finishes a delivery's row; the reason is the detail line, then words.
func (n *Notifier) end(ctx context.Context, d *delivery, result string, channels []string, words ...string) {
	if d.id == 0 {
		return
	}
	reason := joinReason(append([]string{d.a.Message.Detail}, words...)...)
	if err := n.st.FinishActivityWith(ctx, d.id, result, reason, channels); err != nil {
		n.log.Error("alerts: cannot finish an alert's record", "action", d.a.Action, "error", err)
	}
}

// record writes the alert's running row; 0 when it cannot (the alert is
// still sent).
func (n *Notifier) record(ctx context.Context, a Alert) int64 {
	id, err := n.st.StartActivity(ctx, n.activity(a))
	if err != nil {
		n.log.Error("alerts: cannot record an alert", "action", a.Action, "error", err)
		return 0
	}
	return id
}

func (n *Notifier) activity(a Alert) store.Activity {
	return store.Activity{Actor: alertActor, ActorKind: store.ActorSystem, Action: a.Action, ProjectID: a.ProjectID, Target: a.Target}
}

// interrupt records a queued alert that Close kept from being sent. Its
// incident is left as it is: a raise was never stored, and a recovery's
// incident stays open so the recovery is sent after the restart.
func (n *Notifier) interrupt(ctx context.Context, it item) {
	if err := n.st.RecordActivity(ctx, n.activity(it.a), store.ResultInterrupted, it.a.Message.Detail); err != nil {
		n.log.Error("alerts: cannot record an interrupted alert", "action", it.a.Action, "error", err)
	}
}

func (n *Notifier) closeIncident(ctx context.Context, key string) {
	if err := n.st.CloseIncident(ctx, key); err != nil {
		n.log.Error("alerts: cannot close an incident", "key", key, "error", err)
	}
}

// drain finishes what the stopped worker left: parked channels fail, queued
// alerts are interrupted and queued silent closes are applied.
func (n *Notifier) drain() {
	n.mu.Lock()
	queue, parked := n.queue, n.parked
	n.queue, n.parked, n.alerts = nil, nil, 0
	n.mu.Unlock()
	ctx := context.Background()
	for _, p := range parked {
		p.d.results[p.i].words = wordsStopped
		p.d.waiting--
		if p.d.waiting == 0 {
			n.finish(ctx, p.d)
		}
	}
	for _, it := range queue {
		if it.close {
			n.closeIncident(ctx, it.a.Key)
			continue
		}
		n.interrupt(ctx, it)
	}
}

// channelWord is a channel's name in a reason.
func channelWord(name string) string {
	switch name {
	case ChannelTelegram:
		return "Telegram"
	case ChannelEmail:
		return "Email"
	}
	return name
}

func joinReason(parts ...string) string {
	kept := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}
