package alerts

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// testClock is the notifier's clock (Frame.Now), moved by the test.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// testTimers is the notifier's after: no timer ever fires by itself; fire
// fires every one handed out so far.
type testTimers struct {
	mu    sync.Mutex
	chans []chan time.Time
}

func (f *testTimers) after(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.mu.Lock()
	f.chans = append(f.chans, ch)
	f.mu.Unlock()
	return ch
}

func (f *testTimers) fire() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.chans {
		select {
		case ch <- time.Time{}:
		default:
		}
	}
}

func (f *testTimers) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.chans)
}

// recSender records every call; fn, when set, can fail or block a send.
type recSender struct {
	mu    sync.Mutex
	calls int
	got   []Message
	fn    func(ctx context.Context, m Message) error
}

func (s *recSender) Send(ctx context.Context, m Message) error {
	s.mu.Lock()
	s.calls++
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		if err := fn(ctx, m); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.got = append(s.got, m)
	s.mu.Unlock()
	return nil
}

func (s *recSender) setFn(fn func(ctx context.Context, m Message) error) {
	s.mu.Lock()
	s.fn = fn
	s.mu.Unlock()
}

func (s *recSender) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *recSender) delivered() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.got...)
}

// testTargets is the Targets seam, changed by the test.
type testTargets struct {
	mu   sync.Mutex
	list []Target
}

func (t *testTargets) Targets() ([]Target, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Target(nil), t.list...), len(t.list)
}

func (t *testTargets) set(list ...Target) {
	t.mu.Lock()
	t.list = list
	t.mu.Unlock()
}

// syncBuffer is a log destination safe for the worker and the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type notifierRig struct {
	n       *Notifier
	st      *store.Store
	clock   *testClock
	timers  *testTimers
	targets *testTargets
	logs    *syncBuffer
}

// newRig builds a notifier on a fresh store with the given targets, a test
// clock and test timers; it is not started.
func newRig(t *testing.T, frame Frame, targets ...Target) *notifierRig {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	r := &notifierRig{
		st:      st,
		clock:   &testClock{t: time.Date(2026, 11, 1, 3, 31, 0, 0, time.UTC)},
		timers:  &testTimers{},
		targets: &testTargets{list: targets},
		logs:    &syncBuffer{},
	}
	frame.Now = r.clock.Now
	r.n = NewNotifier(st, r.targets, frame, slog.New(slog.NewTextHandler(r.logs, nil)))
	r.n.after = r.timers.after
	return r
}

func (r *notifierRig) start(t *testing.T) {
	t.Helper()
	if err := r.n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.n.Close) // before the store closes
}

// rows returns the activity rows, oldest first.
func (r *notifierRig) rows(t *testing.T) []store.ActivityRow {
	t.Helper()
	rows, err := r.st.RecentActivity(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(rows)
	return rows
}

// waitRows waits until ok holds for the rows (oldest first) and returns them.
func (r *notifierRig) waitRows(t *testing.T, what string, ok func([]store.ActivityRow) bool) []store.ActivityRow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows := r.rows(t)
		if ok(rows) {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; rows: %+v", what, rows)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// finished is a waitRows condition: n rows, none running.
func finished(n int) func([]store.ActivityRow) bool {
	return func(rows []store.ActivityRow) bool {
		if len(rows) != n {
			return false
		}
		for _, r := range rows {
			if r.Result == store.ResultRunning {
				return false
			}
		}
		return true
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func cpuAlert(detail string) Alert {
	return Alert{
		Key:     "server:cpu",
		Action:  store.ActAlertResource,
		Target:  "prod-01",
		Path:    "/server",
		Message: Message{Title: "CPU at 92 % for 5 min (threshold 90 %)", Detail: detail},
	}
}

func oneOff(title string) Alert {
	return Alert{Action: store.ActAlertBackup, Target: "crm", Path: "/backups", Message: Message{Title: title, Detail: title + " detail"}}
}

var (
	errUnreachable = &SendError{Code: CodeUnreachable, Words: "Could not reach Telegram.", Retry: true}
	errBadToken    = &SendError{Code: CodeBadToken, Words: "Telegram refused the bot token."}
	errAuthFailed  = &SendError{Code: CodeAuthFailed, Words: "The mail server refused the login."}
)

func TestFrameFooter(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 11, 1, 3, 31, 0, 0, time.UTC) }
	f := Frame{Host: func() string { return "prod-01" }, Zone: time.FixedZone("Asia/Ho_Chi_Minh", 7*3600), Now: now}
	if got := f.Footer(); got != "prod-01 · 10:31 (Asia/Ho_Chi_Minh)" {
		t.Fatalf("Footer = %q", got)
	}
	if got := (Frame{Now: now}).Footer(); got != "03:31 (UTC)" {
		t.Fatalf("Footer with no host or zone = %q", got)
	}
}

func TestNotifierSentRecordsChannels(t *testing.T) {
	tg, em := &recSender{}, &recSender{}
	r := newRig(t, Frame{Host: func() string { return "prod-01" }},
		Target{Name: ChannelTelegram, Sender: tg}, Target{Name: ChannelEmail, Sender: em})
	r.start(t)

	if !r.n.Raise(cpuAlert("CPU at 92 % since 10:26")) {
		t.Fatal("Raise = false")
	}
	rows := r.waitRows(t, "the alert row", finished(1))
	row := rows[0]
	if row.Actor != "System" || row.ActorKind != store.ActorSystem || row.Action != store.ActAlertResource || row.Target != "prod-01" {
		t.Fatalf("row = %+v", row)
	}
	if row.Result != store.ResultSent || row.Reason != "CPU at 92 % since 10:26" {
		t.Fatalf("result, reason = %q, %q", row.Result, row.Reason)
	}
	if strings.Join(row.Channels, ",") != "telegram,email" {
		t.Fatalf("channels = %v", row.Channels)
	}
	incidents, err := r.st.OpenIncidents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].Key != "server:cpu" || incidents[0].ActivityID != row.ID {
		t.Fatalf("incidents = %+v, want server:cpu with activity %d", incidents, row.ID)
	}
	for _, s := range []*recSender{tg, em} {
		got := s.delivered()
		if len(got) != 1 || got[0].Footer != "prod-01 · 03:31 (UTC)" {
			t.Fatalf("delivered = %+v", got)
		}
	}
}

func TestNotifierPartialAndFailed(t *testing.T) {
	tg, em := &recSender{}, &recSender{}
	em.setFn(func(context.Context, Message) error { return errAuthFailed })
	r := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg}, Target{Name: ChannelEmail, Sender: em})
	r.start(t)

	r.n.Raise(oneOff("first"))
	row := r.waitRows(t, "the partial row", finished(1))[0]
	if row.Result != store.ResultSent || strings.Join(row.Channels, ",") != "telegram" {
		t.Fatalf("result, channels = %q, %v", row.Result, row.Channels)
	}
	if !strings.HasPrefix(row.Reason, "first detail") || !strings.Contains(row.Reason, "Email: The mail server refused the login.") {
		t.Fatalf("reason = %q", row.Reason)
	}

	tg.setFn(func(context.Context, Message) error { return errBadToken })
	r.n.Raise(oneOff("second"))
	row = r.waitRows(t, "the failed row", finished(2))[1]
	if row.Result != store.ResultFailed || len(row.Channels) != 0 {
		t.Fatalf("result, channels = %q, %v", row.Result, row.Channels)
	}
	for _, want := range []string{"second detail", "Telegram: Telegram refused the bot token.", "Email: The mail server refused the login."} {
		if !strings.Contains(row.Reason, want) {
			t.Fatalf("reason = %q, want it to contain %q", row.Reason, want)
		}
	}
}

func TestNotifierNoChannel(t *testing.T) {
	r := newRig(t, Frame{})
	r.start(t)
	r.n.Raise(oneOff("lonely"))
	row := r.waitRows(t, "the not sent row", finished(1))[0]
	if row.Result != store.ResultNotSent || row.Reason != "lonely detail · No alert channel is on." || len(row.Channels) != 0 {
		t.Fatalf("row = %+v", row)
	}
}

func TestNotifierParkedRetryDoesNotBlock(t *testing.T) {
	tg := &recSender{}
	var once sync.Once
	tg.setFn(func(_ context.Context, m Message) error {
		var err error
		if m.Title == "first" {
			once.Do(func() { err = errUnreachable })
		}
		return err
	})
	r := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg})
	r.start(t)

	r.n.Raise(oneOff("first"))
	waitFor(t, "the first send and its parked retry", func() bool { return tg.callCount() == 1 && r.timers.count() > 0 })
	r.n.Raise(oneOff("second"))
	rows := r.waitRows(t, "the second row while the first is parked", func(rows []store.ActivityRow) bool {
		return len(rows) == 2 && rows[1].Result == store.ResultSent
	})
	if rows[0].Result != store.ResultRunning {
		t.Fatalf("first row = %+v, want still running before the retry", rows[0])
	}
	if got := tg.delivered(); len(got) != 1 || got[0].Title != "second" {
		t.Fatalf("delivered = %+v, want only the second so far", got)
	}

	r.clock.Add(30 * time.Second)
	rows = r.waitRows(t, "the retried first row", func(rows []store.ActivityRow) bool {
		r.timers.fire()
		return finished(2)(rows)
	})
	if rows[0].Result != store.ResultSent || strings.Join(rows[0].Channels, ",") != "telegram" || rows[0].Reason != "first detail" {
		t.Fatalf("first row = %+v", rows[0])
	}
	if tg.callCount() != 3 {
		t.Fatalf("calls = %d, want 3 (first, second, retry)", tg.callCount())
	}
}

func TestNotifierRetryIsNotEarly(t *testing.T) {
	tg := &recSender{}
	tg.setFn(func(context.Context, Message) error { return errUnreachable })
	r := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg})
	r.start(t)

	r.n.Raise(oneOff("first"))
	waitFor(t, "the parked retry", func() bool { return r.timers.count() > 0 })
	r.clock.Add(29 * time.Second)
	r.timers.fire() // a timer that fires before the not-before time retries nothing
	waitFor(t, "a new timer for the rest of the wait", func() bool { return r.timers.count() > 1 })
	if tg.callCount() != 1 {
		t.Fatalf("calls = %d, want 1 before 30 s", tg.callCount())
	}
	r.clock.Add(time.Second)
	row := r.waitRows(t, "the failed row", func(rows []store.ActivityRow) bool {
		r.timers.fire()
		return finished(1)(rows)
	})[0]
	// The one retry also failed with a network error: final, never parked again.
	if row.Result != store.ResultFailed || !strings.Contains(row.Reason, "Telegram: Could not reach Telegram.") {
		t.Fatalf("row = %+v", row)
	}
	if tg.callCount() != 2 {
		t.Fatalf("calls = %d, want 2", tg.callCount())
	}
}

func TestNotifierRetryUsesChannelAsItIsNow(t *testing.T) {
	tg, em := &recSender{}, &recSender{}
	tg.setFn(func(context.Context, Message) error { return errUnreachable })
	r := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg}, Target{Name: ChannelEmail, Sender: em})
	r.start(t)

	r.n.Raise(oneOff("first"))
	waitFor(t, "the parked retry", func() bool { return r.timers.count() > 0 })
	r.targets.set(Target{Name: ChannelEmail, Sender: em}) // Telegram turned off meanwhile
	r.clock.Add(30 * time.Second)
	row := r.waitRows(t, "the finished row", func(rows []store.ActivityRow) bool {
		r.timers.fire()
		return finished(1)(rows)
	})[0]
	if row.Result != store.ResultSent || strings.Join(row.Channels, ",") != "email" ||
		!strings.Contains(row.Reason, "Telegram: The channel was turned off before the retry.") {
		t.Fatalf("row = %+v", row)
	}
	if tg.callCount() != 1 {
		t.Fatalf("telegram calls = %d, want 1", tg.callCount())
	}
}

func TestNotifierConfigErrorNotRetried(t *testing.T) {
	tg := &recSender{}
	tg.setFn(func(context.Context, Message) error { return errBadToken })
	r := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg})
	r.start(t)

	r.n.Raise(oneOff("first"))
	row := r.waitRows(t, "the failed row", finished(1))[0]
	if row.Result != store.ResultFailed || !strings.Contains(row.Reason, "Telegram: Telegram refused the bot token.") {
		t.Fatalf("row = %+v", row)
	}
	r.clock.Add(time.Minute)
	r.timers.fire()
	if tg.callCount() != 1 || r.timers.count() != 0 {
		t.Fatalf("calls = %d, timers = %d, want 1 and 0", tg.callCount(), r.timers.count())
	}
}

func TestRaiseSameKeyWhileQueued(t *testing.T) {
	tg := &recSender{}
	r := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg})
	if !r.n.Raise(cpuAlert("first")) {
		t.Fatal("first Raise = false")
	}
	if r.n.Raise(cpuAlert("second")) {
		t.Fatal("second Raise of an open key = true")
	}
	if got := r.n.OpenKeys(); !slices.Equal(got, []string{"server:cpu"}) {
		t.Fatalf("OpenKeys = %v", got)
	}
	r.start(t)
	r.waitRows(t, "one alert", finished(1))

	if !r.n.Recover("server:cpu", cpuAlert("CPU back to 41 %")) {
		t.Fatal("Recover = false")
	}
	if r.n.Recover("server:cpu", cpuAlert("again")) {
		t.Fatal("Recover of a closed key = true")
	}
	if !r.n.Raise(cpuAlert("third")) {
		t.Fatal("Raise after Recover = false")
	}
	rows := r.waitRows(t, "three rows", finished(3))
	got := tg.delivered()
	if len(got) != 3 {
		t.Fatalf("delivered %d messages, want 3", len(got))
	}
	if got[0].Detail != "first" || got[1].Detail != "CPU back to 41 %" || !got[1].Recovery || got[2].Detail != "third" {
		t.Fatalf("delivered = %+v", got)
	}
	if rows[1].Action != store.ActAlertRecovery || rows[1].Target != "prod-01" {
		t.Fatalf("recovery row = %+v", rows[1])
	}
	incidents, err := r.st.OpenIncidents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 || incidents[0].ActivityID != rows[2].ID {
		t.Fatalf("incidents = %+v, want the third alert's", incidents)
	}
}

func TestCloseSilently(t *testing.T) {
	tg := &recSender{}
	r := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg})
	r.start(t)
	r.n.Raise(cpuAlert("first"))
	r.n.CloseSilently("server:cpu")
	if len(r.n.OpenKeys()) != 0 {
		t.Fatalf("OpenKeys = %v", r.n.OpenKeys())
	}
	r.n.Raise(oneOff("marker")) // queued after the close: once it is done, so is the close
	r.waitRows(t, "two rows", finished(2))
	incidents, err := r.st.OpenIncidents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 0 {
		t.Fatalf("incidents = %+v, want none", incidents)
	}
	if len(r.rows(t)) != 2 {
		t.Fatal("CloseSilently wrote an activity row")
	}
}

func TestRateCap(t *testing.T) {
	tg := &recSender{}
	r := newRig(t, Frame{}) // no channel on yet
	r.start(t)

	r.n.Raise(oneOff("nobody"))
	r.waitRows(t, "the no channel row", finished(1))

	r.targets.set(Target{Name: ChannelTelegram, Sender: tg})
	r.n.Raise(cpuAlert("cpu"))
	for i := range 10 {
		r.n.Raise(oneOff("one-off " + string(rune('a'+i))))
	}
	r.n.Recover("server:cpu", cpuAlert("CPU back to 41 %"))
	rows := r.waitRows(t, "the capped rows", finished(13))

	if rows[0].Result != store.ResultNotSent || !strings.HasSuffix(rows[0].Reason, "No alert channel is on.") {
		t.Fatalf("no channel row = %+v", rows[0])
	}
	sent, capped := 0, 0
	for _, row := range rows[1:] {
		switch {
		case row.Result == store.ResultSent:
			sent++
		case row.Result == store.ResultNotSent && strings.HasSuffix(row.Reason, "Too many alerts in 5 minutes; see Activity."):
			capped++
		default:
			t.Fatalf("row = %+v", row)
		}
	}
	if sent != 10 || capped != 2 || len(tg.delivered()) != 10 {
		t.Fatalf("sent %d, capped %d, delivered %d; want 10, 2, 10", sent, capped, len(tg.delivered()))
	}
	recovery := rows[12]
	if recovery.Action != store.ActAlertRecovery || recovery.Result != store.ResultNotSent {
		t.Fatalf("recovery row = %+v", recovery)
	}
	incidents, err := r.st.OpenIncidents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 0 || len(r.n.OpenKeys()) != 0 {
		t.Fatalf("incidents = %+v, open keys = %v; the capped recovery still closes it", incidents, r.n.OpenKeys())
	}

	r.clock.Add(5 * time.Minute)
	r.n.Raise(oneOff("later"))
	rows = r.waitRows(t, "the row after the window", finished(14))
	if rows[13].Result != store.ResultSent {
		t.Fatalf("row after the window = %+v", rows[13])
	}
}

func TestLinks(t *testing.T) {
	tg := &recSender{}
	r := newRig(t, Frame{PublicURL: "https://panel.x.io"}, Target{Name: ChannelTelegram, Sender: tg})
	r.start(t)
	project := oneOff("project")
	project.ProjectID, project.Path = 3, "/backups" // the project page wins
	server := oneOff("server")
	server.Path = "/server"
	none := oneOff("none")
	none.Path = ""
	r.n.Raise(project)
	r.n.Raise(server)
	r.n.Raise(none)
	r.waitRows(t, "three rows", finished(3))
	got := tg.delivered()
	if got[0].Link != "https://panel.x.io/projects/3" || got[1].Link != "https://panel.x.io/server" || got[2].Link != "" {
		t.Fatalf("links = %q, %q, %q", got[0].Link, got[1].Link, got[2].Link)
	}
	if row := r.rows(t)[0]; row.ProjectID != 3 {
		t.Fatalf("project row = %+v", row)
	}

	tg2 := &recSender{}
	r2 := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg2})
	r2.start(t)
	r2.n.Raise(project)
	r2.waitRows(t, "one row", finished(1))
	m := tg2.delivered()[0]
	if m.Link != "" || strings.Contains(m.Text(), "http") {
		t.Fatalf("message without a public URL = %q", m.Text())
	}
}

func TestCloseFinishesParkedAndQueued(t *testing.T) {
	tg := &recSender{}
	blocked := make(chan struct{})
	tg.setFn(func(ctx context.Context, m Message) error {
		switch m.Title {
		case "parked":
			return errUnreachable
		case "in flight":
			close(blocked)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	r := newRig(t, Frame{}, Target{Name: ChannelTelegram, Sender: tg})
	r.start(t)

	r.n.Raise(oneOff("parked"))
	waitFor(t, "the parked retry", func() bool { return r.timers.count() > 0 })
	r.n.Raise(oneOff("in flight"))
	<-blocked
	r.n.Raise(oneOff("queued"))
	r.n.Close()

	rows := r.waitRows(t, "three finished rows", finished(3))
	if rows[0].Result != store.ResultFailed || !strings.Contains(rows[0].Reason, "Telegram: The panel stopped before the retry.") {
		t.Fatalf("parked row = %+v", rows[0])
	}
	if rows[1].Result != store.ResultFailed || !strings.Contains(rows[1].Reason, "Telegram: The panel stopped before the retry.") {
		t.Fatalf("in-flight row = %+v", rows[1])
	}
	if rows[2].Result != store.ResultInterrupted || rows[2].Reason != "queued detail" {
		t.Fatalf("queued row = %+v", rows[2])
	}
	if r.n.Raise(oneOff("late")) {
		t.Fatal("Raise after Close = true")
	}
	if !strings.Contains(r.logs.String(), "after the notifier closed") {
		t.Fatalf("logs = %q", r.logs.String())
	}
	if len(r.rows(t)) != 3 {
		t.Fatal("a Raise after Close wrote a row")
	}
}

func TestStartLoadsIncidents(t *testing.T) {
	r := newRig(t, Frame{})
	if err := r.st.OpenIncident(context.Background(), store.Incident{Key: "tunnel", OpenedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r.start(t)
	if got := r.n.OpenKeys(); !slices.Equal(got, []string{"tunnel"}) {
		t.Fatalf("OpenKeys = %v", got)
	}
	if r.n.Raise(Alert{Key: "tunnel", Action: store.ActAlertTunnel, Message: Message{Title: "Cloudflare Tunnel disconnected"}}) {
		t.Fatal("Raise of a stored incident = true")
	}
}

func TestQueueFullDrops(t *testing.T) {
	r := newRig(t, Frame{}) // not started: nothing drains the queue
	for i := range queueSize {
		if !r.n.Raise(oneOff("fill")) {
			t.Fatalf("Raise %d = false", i)
		}
	}
	if r.n.Raise(Alert{Key: "tunnel", Action: store.ActAlertTunnel}) {
		t.Fatal("Raise on a full queue = true")
	}
	if !strings.Contains(r.logs.String(), "queue is full") {
		t.Fatalf("logs = %q", r.logs.String())
	}
	// The key stays open in memory, so the watcher does not raise it again.
	if got := r.n.OpenKeys(); !slices.Equal(got, []string{"tunnel"}) {
		t.Fatalf("OpenKeys = %v", got)
	}
}
