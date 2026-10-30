package logs

import (
	"context"
	"errors"
	"fmt"
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

const wait = 2 * time.Second

var (
	t0    = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	clock = time.Date(2026, 10, 30, 10, 0, 0, 500_000_000, time.UTC)
)

// call is one Run or Stream the fake received.
type call struct {
	Action  runner.Action
	Values  runner.Values
	Secrets []string
}

// fakeRunner answers Run with a fixed backlog (or error) and drives each
// Stream from the follow channel: every line sent is delivered, a closed
// channel ends the follow with followErr, and a cancelled ctx ends it with the
// ctx error. Each delivered line is acknowledged on delivered, so emit returns
// only after onLine ran.
type fakeRunner struct {
	mu         sync.Mutex
	backlog    string
	truncated  bool
	backlogErr error
	gate       chan struct{} // when non-nil, Run waits for it to close
	followErr  error
	follow     chan string
	delivered  chan struct{}
	runs       []call
	streams    []call
	started    chan call     // one value per Stream call
	ctxDone    chan struct{} // one value per Stream that ended on its ctx
}

func newFake(backlog string) *fakeRunner {
	return &fakeRunner{
		backlog:   backlog,
		follow:    make(chan string),
		delivered: make(chan struct{}),
		started:   make(chan call, 16),
		ctxDone:   make(chan struct{}, 16),
	}
}

func (f *fakeRunner) Run(ctx context.Context, _ runner.Stack, a runner.Action, v runner.Values, secrets []string) (runner.Result, error) {
	f.mu.Lock()
	f.runs = append(f.runs, call{a, v, secrets})
	gate, out, err, trunc := f.gate, f.backlog, f.backlogErr, f.truncated
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return runner.Result{}, ctx.Err()
		}
	}
	return runner.Result{Stdout: []byte(out), Truncated: trunc}, err
}

func (f *fakeRunner) Stream(ctx context.Context, _ runner.Stack, a runner.Action, v runner.Values, secrets []string, onLine func(string)) error {
	f.mu.Lock()
	c := call{a, v, secrets}
	f.streams = append(f.streams, c)
	exit := f.followErr
	f.mu.Unlock()
	f.started <- c
	for {
		select {
		case line, ok := <-f.follow:
			if !ok {
				return exit
			}
			onLine(line)
			f.delivered <- struct{}{}
		case <-ctx.Done():
			f.ctxDone <- struct{}{}
			return fmt.Errorf("docker: %w", ctx.Err())
		}
	}
}

// emit delivers one follow line and returns once onLine has run.
func (f *fakeRunner) emit(t *testing.T, line string) {
	t.Helper()
	select {
	case f.follow <- line:
	case <-time.After(wait):
		t.Fatal("no follow is reading")
	}
	<-f.delivered
}

func (f *fakeRunner) counts() (runs, streams int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs), len(f.streams)
}

func (f *fakeRunner) waitStarted(t *testing.T) call {
	t.Helper()
	select {
	case c := <-f.started:
		return c
	case <-time.After(wait):
		t.Fatal("Stream was not called")
	}
	return call{}
}

func (f *fakeRunner) waitCtxDone(t *testing.T) {
	t.Helper()
	select {
	case <-f.ctxDone:
	case <-time.After(wait):
		t.Fatal("the follow's ctx was not cancelled")
	}
}

// fakeContainers is a Containers source that counts its calls.
type fakeContainers struct {
	mu    sync.Mutex
	cs    []project.Container
	ok    bool
	calls int
}

func (c *fakeContainers) read(context.Context) ([]project.Container, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if !c.ok {
		return nil, false
	}
	return c.cs, true
}

func (c *fakeContainers) set(cs []project.Container, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cs, c.ok = cs, ok
}

func running(projects ...string) *fakeContainers {
	var cs []project.Container
	for _, p := range projects {
		cs = append(cs,
			project.Container{Project: p, Service: "web", State: "running", Name: p + "-web-1"},
			project.Container{Project: p, Service: "db", State: "exited", Name: p + "-db-1"},
		)
	}
	return &fakeContainers{cs: cs, ok: true}
}

func newManager(f *fakeRunner, cs *fakeContainers) *Manager {
	m := New(f, cs.read, func() time.Time { return clock })
	m.batchEvery = 5 * time.Millisecond
	return m
}

func tracked(t *testing.T, id int64, composeProject string) store.Tracked {
	t.Helper()
	return store.Tracked{
		ID: id, Path: t.TempDir(), ComposeProject: composeProject,
		ComposeFiles: []string{"compose.yaml"}, Services: []string{"web", "db"},
	}
}

func raw(prefix string, at time.Time, text string) string {
	return prefix + " | " + at.Format(time.RFC3339Nano) + " " + text
}

// next returns the next batch, failing when none comes or the stream ended.
func next(t *testing.T, sub *Sub) []Line {
	t.Helper()
	select {
	case b, ok := <-sub.Lines:
		if !ok {
			t.Fatalf("stream ended early: %+v", <-sub.End)
		}
		return b
	case <-time.After(wait):
		t.Fatal("no batch")
	}
	return nil
}

// collect gathers lines until n have arrived.
func collect(t *testing.T, sub *Sub, n int) []Line {
	t.Helper()
	var got []Line
	for len(got) < n {
		got = append(got, next(t, sub)...)
	}
	return got
}

// drainEnd reads every batch left and then the end reason.
func drainEnd(t *testing.T, sub *Sub) ([]Line, EndReason) {
	t.Helper()
	var got []Line
	timeout := time.After(wait)
	for {
		select {
		case b, ok := <-sub.Lines:
			if !ok {
				return got, <-sub.End
			}
			got = append(got, b...)
		case <-timeout:
			t.Fatal("stream did not end")
		}
	}
}

func noBatch(t *testing.T, sub *Sub, d time.Duration) {
	t.Helper()
	select {
	case b, ok := <-sub.Lines:
		t.Fatalf("unexpected batch %+v (open %v)", b, ok)
	case <-time.After(d):
	}
}

func texts(ls []Line) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Text
	}
	return out
}

func reserved(m *Manager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reserved
}

func waitReserved(t *testing.T, m *Manager, n int) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for reserved(m) != n {
		if time.Now().After(deadline) {
			t.Fatalf("reserved = %d, want %d", reserved(m), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBacklogSortedNewest500(t *testing.T) {
	// compose prints each container's tail in turn, so the two are out of
	// time order: web at even seconds, then db at odd seconds.
	var b strings.Builder
	for i := range 350 {
		fmt.Fprintln(&b, raw("web-1", t0.Add(time.Duration(2*i)*time.Second), fmt.Sprintf("w%d", 2*i)))
	}
	for i := range 350 {
		fmt.Fprintln(&b, raw("db-1", t0.Add(time.Duration(2*i+1)*time.Second), fmt.Sprintf("d%d", 2*i+1)))
	}
	f := newFake(b.String())
	cs := &fakeContainers{ok: true, cs: []project.Container{
		{Project: "shop", Service: "web", State: "exited", Name: "shop-web-1"},
		{Project: "shop", Service: "db", State: "exited", Name: "shop-db-1"},
	}}
	m := newManager(f, cs)
	tr := tracked(t, 1, "shop")
	sub, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	got, end := drainEnd(t, sub)
	if len(got) != RingSize {
		t.Fatalf("got %d lines, want %d", len(got), RingSize)
	}
	for i, l := range got {
		want := t0.Add(time.Duration(200+i) * time.Second)
		if !l.Time.Equal(want) {
			t.Fatalf("line %d at %v, want %v", i, l.Time, want)
		}
		if (i%2 == 0) != (l.Service == "web") {
			t.Fatalf("line %d: %+v", i, l)
		}
	}
	if end.Reason != ReasonExited {
		t.Fatalf("end = %+v", end)
	}
	if len(f.runs) != 1 || f.runs[0].Action != runner.ComposeLogsTail ||
		f.runs[0].Values["dir"] != tr.Path || f.runs[0].Values["file"] != filepath.Join(tr.Path, "compose.yaml") || len(f.runs[0].Values) != 2 {
		t.Fatalf("runs = %+v", f.runs)
	}
}

func TestBacklogDropsTruncatedLastLine(t *testing.T) {
	f := newFake(raw("web-1", t0, "whole") + "\n" + raw("web-1", t0.Add(time.Second), "cut sec"))
	f.truncated = true
	m := newManager(f, &fakeContainers{ok: true})
	sub, err := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := drainEnd(t, sub)
	if !slices.Equal(texts(got), []string{"whole"}) {
		t.Fatalf("got %q", texts(got))
	}
}

func TestHandOffDedupe(t *testing.T) {
	var b strings.Builder
	for i := range 5 {
		fmt.Fprintln(&b, raw("web-1", t0.Add(time.Duration(i)*time.Second), fmt.Sprintf("b%d", i)))
	}
	f := newFake(b.String())
	m := newManager(f, running("shop"))
	sub, err := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := next(t, sub); len(got) != 5 {
		t.Fatalf("backlog = %q", texts(got))
	}
	c := f.waitStarted(t)
	if c.Action != runner.ComposeLogs || c.Values["since"] != "2026-10-30T10:00:00.5Z" {
		t.Fatalf("follow = %+v", c)
	}
	for i := 2; i < 5; i++ {
		f.emit(t, raw("web-1", t0.Add(time.Duration(i)*time.Second), fmt.Sprintf("b%d", i)))
	}
	f.emit(t, raw("web-1", t0.Add(5*time.Second), "new"))
	close(f.follow)
	got, end := drainEnd(t, sub)
	if !slices.Equal(texts(got), []string{"new"}) || end.Reason != ReasonExited {
		t.Fatalf("got %q, end %+v", texts(got), end)
	}
}

func TestJoinerGetsRingThenLive(t *testing.T) {
	f := newFake(raw("web-1", t0, "b0") + "\n")
	m := newManager(f, running("shop"))
	tr := tracked(t, 1, "shop")
	a, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	next(t, a)
	f.waitStarted(t)
	f.emit(t, raw("web-1", t0.Add(time.Second), "l1"))
	collect(t, a, 1)

	b, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := next(t, b); !slices.Equal(texts(got), []string{"b0", "l1"}) {
		t.Fatalf("joiner ring = %q", texts(got))
	}
	f.emit(t, raw("web-1", t0.Add(2*time.Second), "l2"))
	for _, s := range []*Sub{a, b} {
		if got := collect(t, s, 1); !slices.Equal(texts(got), []string{"l2"}) {
			t.Fatalf("live = %q", texts(got))
		}
	}
	if runs, streams := f.counts(); runs != 1 || streams != 1 {
		t.Fatalf("runs %d, streams %d", runs, streams)
	}
	a.Cancel()
	b.Cancel()
}

func TestOneFollowPerKey(t *testing.T) {
	f := newFake(raw("web-1", t0, "b0") + "\n")
	f.gate = make(chan struct{})
	cs := running("shop")
	m := newManager(f, cs)
	tr := tracked(t, 1, "shop")
	a, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	// b joins while the backlog still runs: it waits for it, then gets the ring.
	b, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	noBatch(t, b, 20*time.Millisecond)
	close(f.gate)
	for _, s := range []*Sub{a, b} {
		if got := next(t, s); !slices.Equal(texts(got), []string{"b0"}) {
			t.Fatalf("ring = %q", texts(got))
		}
	}
	f.waitStarted(t)
	if runs, streams := f.counts(); runs != 1 || streams != 1 {
		t.Fatalf("runs %d, streams %d", runs, streams)
	}
	if cs.calls != 1 {
		t.Fatalf("containers read %d times; a joiner must not read", cs.calls)
	}
	// Another service is another key.
	c, err := m.Subscribe(context.Background(), tr, "web")
	if err != nil {
		t.Fatal(err)
	}
	next(t, c)
	if got := f.waitStarted(t); got.Action != runner.ComposeLogsService || got.Values["service"] != "web" {
		t.Fatalf("service follow = %+v", got)
	}
	if got := f.runs[1]; got.Action != runner.ComposeLogsTailService || got.Values["service"] != "web" {
		t.Fatalf("service backlog = %+v", got)
	}
	for _, s := range []*Sub{a, b, c} {
		s.Cancel()
	}
}

func TestOverrideActions(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("shop"))
	tr := tracked(t, 1, "shop")
	tr.ComposeFiles = []string{"compose.yaml", "compose.override.yaml"}
	sub, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	next(t, sub)
	c := f.waitStarted(t)
	if f.runs[0].Action != runner.ComposeLogsTailOverride || c.Action != runner.ComposeLogsOverride ||
		c.Values["override"] != filepath.Join(tr.Path, "compose.override.yaml") {
		t.Fatalf("run %+v, follow %+v", f.runs[0], c)
	}
	sub.Cancel()

	sub, err = m.Subscribe(context.Background(), tr, "web")
	if err != nil {
		t.Fatal(err)
	}
	next(t, sub)
	c = f.waitStarted(t)
	if f.runs[1].Action != runner.ComposeLogsTailServiceOverride || c.Action != runner.ComposeLogsServiceOverride {
		t.Fatalf("run %+v, follow %+v", f.runs[1], c)
	}
	sub.Cancel()
}

func TestCapRefusesFourthFollowButNotJoiner(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("p1", "p2", "p3", "p4"))
	var subs []*Sub
	for i := int64(1); i <= MaxFollows; i++ {
		s, err := m.Subscribe(context.Background(), tracked(t, i, fmt.Sprintf("p%d", i)), "")
		if err != nil {
			t.Fatal(err)
		}
		next(t, s)
		f.waitStarted(t)
		subs = append(subs, s)
	}
	fourth := tracked(t, 4, "p4")
	if _, err := m.Subscribe(context.Background(), fourth, ""); !errors.Is(err, ErrTooMany) {
		t.Fatalf("fourth = %v, want ErrTooMany", err)
	}
	if runs, _ := f.counts(); runs != MaxFollows {
		t.Fatalf("runs = %d: the refused request ran something", runs)
	}
	joiner, err := m.Subscribe(context.Background(), tracked(t, 1, "p1"), "")
	if err != nil {
		t.Fatalf("joiner refused: %v", err)
	}
	next(t, joiner)

	// Freeing a slot lets the fourth in.
	subs[0].Cancel()
	joiner.Cancel()
	f.waitCtxDone(t)
	waitReserved(t, m, MaxFollows-1)
	s, err := m.Subscribe(context.Background(), fourth, "")
	if err != nil {
		t.Fatalf("after a slot freed: %v", err)
	}
	next(t, s)
	for _, s := range append(subs[1:], s) {
		s.Cancel()
	}
	waitReserved(t, m, 0)
}

func TestStreamNoRunningContainerSendsBacklogThenExited(t *testing.T) {
	f := newFake(raw("web-1", t0, "a") + "\n" + raw("db-1", t0.Add(time.Second), "b") + "\n")
	cs := &fakeContainers{ok: true, cs: []project.Container{
		{Project: "shop", Service: "web", State: "exited", Name: "shop-web-1"},
		// Running, but another project's and a one-off: neither counts.
		{Project: "other", Service: "web", State: "running", Name: "other-web-1"},
		{Project: "shop", Service: "web", State: "running", Name: "shop-web-run-1", OneOff: true},
	}}
	m := newManager(f, cs)
	sub, err := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	if err != nil {
		t.Fatal(err)
	}
	got, end := drainEnd(t, sub)
	if !slices.Equal(texts(got), []string{"a", "b"}) || end.Reason != ReasonExited {
		t.Fatalf("got %q, end %+v", texts(got), end)
	}
	if _, streams := f.counts(); streams != 0 {
		t.Fatalf("Stream called %d times", streams)
	}
	waitReserved(t, m, 0)
	sub.Cancel() // after the end: a no-op
}

func TestEmptyBacklogIsAnEmptyBatch(t *testing.T) {
	m := newManager(newFake(""), &fakeContainers{ok: true})
	sub, err := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	if err != nil {
		t.Fatal(err)
	}
	b := next(t, sub)
	if b == nil || len(b) != 0 {
		t.Fatalf("first batch = %#v, want an empty non-nil batch", b)
	}
	if _, end := drainEnd(t, sub); end.Reason != ReasonExited {
		t.Fatalf("end = %+v", end)
	}
}

func TestServiceSelectionNarrowsFollow(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("shop")) // web running, db exited
	sub, err := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "db")
	if err != nil {
		t.Fatal(err)
	}
	if _, end := drainEnd(t, sub); end.Reason != ReasonExited {
		t.Fatalf("end = %+v", end)
	}
	if _, streams := f.counts(); streams != 0 {
		t.Fatalf("Stream called %d times for a stopped service", streams)
	}
}

func TestStreamFollowsRestartingContainer(t *testing.T) {
	f := newFake("")
	cs := &fakeContainers{ok: true, cs: []project.Container{
		{Project: "shop", Service: "web", State: "restarting", Name: "shop-web-1"},
	}}
	m := newManager(f, cs)
	sub, err := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "web")
	if err != nil {
		t.Fatal(err)
	}
	next(t, sub)
	f.waitStarted(t)
	sub.Cancel()
}

func TestDockerDownRefusesNewKeyOnly(t *testing.T) {
	f := newFake("")
	cs := &fakeContainers{ok: false}
	m := newManager(f, cs)
	tr := tracked(t, 1, "shop")
	if _, err := m.Subscribe(context.Background(), tr, ""); !errors.Is(err, ErrDockerDown) {
		t.Fatalf("err = %v, want ErrDockerDown", err)
	}
	if runs, _ := f.counts(); runs != 0 || reserved(m) != 0 {
		t.Fatalf("runs %d, reserved %d", runs, reserved(m))
	}
	cs.set(running("shop").cs, true)
	a, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	next(t, a)
	f.waitStarted(t)
	cs.set(nil, false)
	b, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatalf("joiner refused while Docker is down: %v", err)
	}
	next(t, b)
	a.Cancel()
	b.Cancel()
}

func TestLastCancelKillsFollow(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("shop"))
	tr := tracked(t, 1, "shop")
	a, _ := m.Subscribe(context.Background(), tr, "")
	b, _ := m.Subscribe(context.Background(), tr, "")
	next(t, a)
	next(t, b)
	f.waitStarted(t)

	a.Cancel()
	a.Cancel() // twice is harmless
	select {
	case <-f.ctxDone:
		t.Fatal("follow cancelled while a viewer is left")
	case <-time.After(20 * time.Millisecond):
	}
	f.emit(t, raw("web-1", t0, "still"))
	collect(t, b, 1)

	b.Cancel()
	f.waitCtxDone(t)
	waitReserved(t, m, 0)

	// The key is free again: a new viewer starts a new stream.
	c, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	next(t, c)
	f.waitStarted(t)
	if runs, streams := f.counts(); runs != 2 || streams != 2 {
		t.Fatalf("runs %d, streams %d", runs, streams)
	}
	c.Cancel()
}

func TestCancelDuringBacklog(t *testing.T) {
	f := newFake("")
	f.gate = make(chan struct{})
	m := newManager(f, running("shop"))
	sub, err := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	if err != nil {
		t.Fatal(err)
	}
	sub.Cancel()
	waitReserved(t, m, 0)
	if _, streams := f.counts(); streams != 0 {
		t.Fatalf("Stream called %d times after the viewer left", streams)
	}
}

func TestFollowExitEndsExited(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("shop"))
	tr := tracked(t, 1, "shop")
	a, _ := m.Subscribe(context.Background(), tr, "")
	b, _ := m.Subscribe(context.Background(), tr, "")
	next(t, a)
	next(t, b)
	f.waitStarted(t)
	f.emit(t, raw("web-1", t0, "last words"))
	close(f.follow)
	for _, s := range []*Sub{a, b} {
		got, end := drainEnd(t, s)
		if !slices.Equal(texts(got), []string{"last words"}) || end.Reason != ReasonExited {
			t.Fatalf("got %q, end %+v", texts(got), end)
		}
	}
	waitReserved(t, m, 0)
	f.follow = make(chan string)
	c, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	next(t, c)
	f.waitStarted(t)
	if runs, _ := f.counts(); runs != 2 {
		t.Fatalf("the ended key was reused: runs %d", runs)
	}
	c.Cancel()
}

func writeEnv(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_PASSWORD=devpassword\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFollowErrorEndsError(t *testing.T) {
	f := newFake("")
	f.followErr = &runner.ExitError{Code: 1, Stderr: "auth devpassword failed"}
	m := newManager(f, running("shop"))
	tr := tracked(t, 1, "shop")
	writeEnv(t, tr.Path)
	sub, _ := m.Subscribe(context.Background(), tr, "")
	next(t, sub)
	f.waitStarted(t)
	close(f.follow)
	_, end := drainEnd(t, sub)
	if end.Reason != ReasonError || end.Message != "auth •••• failed" {
		t.Fatalf("end = %+v", end)
	}
	waitReserved(t, m, 0)
}

func TestBacklogErrorEndsError(t *testing.T) {
	f := newFake("")
	f.backlogErr = &runner.ExitError{Code: 1, Stderr: "no such file devpassword"}
	f.gate = make(chan struct{})
	m := newManager(f, running("shop"))
	tr := tracked(t, 1, "shop")
	writeEnv(t, tr.Path)
	a, _ := m.Subscribe(context.Background(), tr, "")
	b, _ := m.Subscribe(context.Background(), tr, "")
	close(f.gate)
	for _, s := range []*Sub{a, b} {
		got, end := drainEnd(t, s)
		if len(got) != 0 || end.Reason != ReasonError || end.Message != "no such file ••••" {
			t.Fatalf("got %q, end %+v", texts(got), end)
		}
	}
	if _, streams := f.counts(); streams != 0 {
		t.Fatalf("Stream called %d times", streams)
	}
	waitReserved(t, m, 0)
	// The key is free.
	f.backlogErr = nil
	c, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	next(t, c)
	c.Cancel()
}

func TestNoComposeFileEndsError(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("shop"))
	tr := tracked(t, 1, "shop")
	tr.ComposeFiles = nil
	sub, err := m.Subscribe(context.Background(), tr, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, end := drainEnd(t, sub); end.Reason != ReasonError || end.Message == "" {
		t.Fatalf("end = %+v", end)
	}
	if runs, _ := f.counts(); runs != 0 {
		t.Fatalf("runs = %d", runs)
	}
	waitReserved(t, m, 0)
}

// manualTicks replaces the batch ticker with a channel the test drives, and
// records the interval asked for. tick returns once that tick's flush is
// done: the second send is taken only when the flusher is back at its select,
// and with nothing pending it flushes nothing.
func manualTicks(m *Manager) (tick func(), asked *time.Duration) {
	ch := make(chan time.Time)
	asked = new(time.Duration)
	m.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
		*asked = d
		return ch, func() {}
	}
	return func() { ch <- time.Now(); ch <- time.Now() }, asked
}

func TestBatchingEvery200ms(t *testing.T) {
	f := newFake("")
	m := New(f, running("shop").read, func() time.Time { return clock })
	if m.batchEvery != BatchEvery || BatchEvery != 200*time.Millisecond {
		t.Fatalf("batch interval = %v", m.batchEvery)
	}
	tick, asked := manualTicks(m)
	sub, _ := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	next(t, sub)
	f.waitStarted(t)
	for i := range 3 {
		f.emit(t, raw("web-1", t0.Add(time.Duration(i)*time.Second), fmt.Sprintf("l%d", i)))
	}
	if *asked != BatchEvery {
		t.Fatalf("ticker asked for %v", *asked)
	}
	noBatch(t, sub, 20*time.Millisecond)
	tick()
	if got := next(t, sub); !slices.Equal(texts(got), []string{"l0", "l1", "l2"}) {
		t.Fatalf("batch = %q", texts(got))
	}
	tick() // nothing pending: no batch
	noBatch(t, sub, 20*time.Millisecond)
	sub.Cancel()
}

func TestSlowSubscriberEnded(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("shop"))
	tick, _ := manualTicks(m)
	tr := tracked(t, 1, "shop")
	slow, _ := m.Subscribe(context.Background(), tr, "")
	fast, _ := m.Subscribe(context.Background(), tr, "")
	f.waitStarted(t)

	var mu sync.Mutex
	var fastGot []Line
	fastDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		for b := range fast.Lines {
			mu.Lock()
			fastGot = append(fastGot, b...)
			mu.Unlock()
		}
	}()
	// slow holds the backlog batch; 15 more fill its buffer of 16, the 16th
	// overflows it.
	for i := range 16 {
		f.emit(t, raw("web-1", t0.Add(time.Duration(i)*time.Second), fmt.Sprintf("l%d", i)))
		tick()
	}
	select {
	case end := <-slow.End:
		if end.Reason != ReasonSlow {
			t.Fatalf("end = %+v", end)
		}
	case <-time.After(wait):
		t.Fatal("slow viewer not ended")
	}
	got, _ := drainEnd(t, slow)
	if len(got) != 15 {
		t.Fatalf("slow got %d lines before its end", len(got))
	}
	// The fast viewer and the follow live on.
	f.emit(t, raw("web-1", t0.Add(time.Minute), "after"))
	tick()
	deadline := time.Now().Add(wait)
	for {
		mu.Lock()
		n := len(fastGot)
		mu.Unlock()
		if n == 17 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fast got %d lines, want 17", n)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-f.ctxDone:
		t.Fatal("follow cancelled with a viewer left")
	default:
	}
	fast.Cancel()
	<-fastDone
	f.waitCtxDone(t)
}

// When the slow viewer was the last one, the follow stops too.
func TestSlowLastSubscriberStopsFollow(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("shop"))
	tick, _ := manualTicks(m)
	sub, _ := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	f.waitStarted(t)
	for i := range 16 {
		f.emit(t, raw("web-1", t0.Add(time.Duration(i)*time.Second), "x"))
		tick()
	}
	if _, end := drainEnd(t, sub); end.Reason != ReasonSlow {
		t.Fatalf("end = %+v", end)
	}
	f.waitCtxDone(t)
	waitReserved(t, m, 0)
}

func TestLinesMasked(t *testing.T) {
	f := newFake(raw("web-1", t0, "connect with devpassword") + "\n")
	m := newManager(f, running("shop"))
	tr := tracked(t, 1, "shop")
	writeEnv(t, tr.Path)
	sub, _ := m.Subscribe(context.Background(), tr, "")
	if got := next(t, sub); !slices.Equal(texts(got), []string{"connect with ••••"}) {
		t.Fatalf("backlog = %q", texts(got))
	}
	c := f.waitStarted(t)
	if !slices.Contains(c.Secrets, "devpassword") || !slices.Contains(f.runs[0].Secrets, "devpassword") {
		t.Fatalf("secrets: run %v, follow %v", f.runs[0].Secrets, c.Secrets)
	}
	f.emit(t, raw("web-1", t0.Add(time.Second), "live devpassword"))
	if got := collect(t, sub, 1); !slices.Equal(texts(got), []string{"live ••••"}) {
		t.Fatalf("live = %q", texts(got))
	}
	sub.Cancel()
}

// Unparsed follow output (status lines, compose warnings) never reaches a viewer.
func TestFollowDropsNonContainerLines(t *testing.T) {
	f := newFake("")
	m := newManager(f, running("shop"))
	sub, _ := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	next(t, sub)
	f.waitStarted(t)
	f.emit(t, "\x1b[Kweb-1 exited with code 137")
	f.emit(t, `time="2026-10-30T10:00:00Z" level=warning msg="x"`)
	f.emit(t, raw("web-1", t0, "kept"))
	close(f.follow)
	got, _ := drainEnd(t, sub)
	if !slices.Equal(texts(got), []string{"kept"}) || got[0].Container != "shop-web-1" || got[0].Service != "web" {
		t.Fatalf("got %+v", got)
	}
}

// A container that logs CRLF: the backlog loses the CR like the runner's
// follow lines do, so the hand-off still dedupes.
func TestBacklogCRLFDedupes(t *testing.T) {
	f := newFake(raw("web-1", t0, "b0") + "\r\n" + raw("web-1", t0.Add(time.Second), "b1") + "\r\n")
	m := newManager(f, running("shop"))
	sub, _ := m.Subscribe(context.Background(), tracked(t, 1, "shop"), "")
	if got := next(t, sub); !slices.Equal(texts(got), []string{"b0", "b1"}) {
		t.Fatalf("backlog = %q", texts(got))
	}
	f.waitStarted(t)
	// The runner strips the CR from a follow line.
	f.emit(t, raw("web-1", t0.Add(time.Second), "b1"))
	f.emit(t, raw("web-1", t0.Add(2*time.Second), "new"))
	close(f.follow)
	got, _ := drainEnd(t, sub)
	if !slices.Equal(texts(got), []string{"new"}) {
		t.Fatalf("live = %q", texts(got))
	}
}
