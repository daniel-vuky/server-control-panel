package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

// reply is one canned answer of the fake. A non-nil gate blocks the call until
// it is closed; delay then holds it a while longer; a non-nil hook runs next;
// panic makes the call panic. stdout is returned with err too, as the runner
// returns the output of a command that exited non-zero.
type reply struct {
	stdout string
	err    error
	gate   chan struct{}
	delay  time.Duration
	hook   func()
	panic  bool
}

// fakeCall is one call the fake received.
type fakeCall struct {
	Stack   runner.Stack
	Action  runner.Action
	Values  runner.Values
	Secrets []string
	At      time.Time // when the call began
}

// fakeCmd answers each action from its own reply queue. A queue with one reply
// left repeats it. It counts the calls in flight, per action and in all, keeps
// the peak, and logs every call with the secrets it was given.
type fakeCmd struct {
	mu       sync.Mutex
	queues   map[runner.Action][]reply
	calls    []fakeCall
	inFlight map[runner.Action]int
	peakOf   map[runner.Action]int
	total    int
	peak     int
}

func newFakeCmd() *fakeCmd {
	return &fakeCmd{queues: map[runner.Action][]reply{}, inFlight: map[runner.Action]int{}, peakOf: map[runner.Action]int{}}
}

// on appends replies to the queue of action a.
func (f *fakeCmd) on(a runner.Action, rs ...reply) *fakeCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queues[a] = append(f.queues[a], rs...)
	return f
}

// set replaces the queue of action a with rs.
func (f *fakeCmd) set(a runner.Action, rs ...reply) *fakeCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queues[a] = rs
	return f
}

// containers queues docker.containers replies, one output each.
func (f *fakeCmd) containers(outs ...string) *fakeCmd {
	for _, o := range outs {
		f.on(runner.DockerContainers, reply{stdout: o})
	}
	return f
}

func (f *fakeCmd) Run(_ context.Context, s runner.Stack, a runner.Action, v runner.Values, secrets []string) (runner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{s, a, v, slices.Clone(secrets), time.Now()})
	var r reply
	q := f.queues[a]
	switch {
	case len(q) == 0:
		f.mu.Unlock()
		return runner.Result{}, fmt.Errorf("fake: no reply for %s %s", s, a)
	case len(q) == 1:
		r = q[0]
	default:
		r, f.queues[a] = q[0], q[1:]
	}
	f.inFlight[a]++
	f.peakOf[a] = max(f.peakOf[a], f.inFlight[a])
	f.total++
	f.peak = max(f.peak, f.total)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight[a]--
		f.total--
		f.mu.Unlock()
	}()

	if r.gate != nil {
		<-r.gate
	}
	time.Sleep(r.delay)
	if r.hook != nil {
		r.hook()
	}
	if r.panic {
		panic("fake: " + string(a))
	}
	if r.err != nil {
		return runner.Result{ExitCode: 1, Stdout: []byte(r.stdout)}, r.err
	}
	return runner.Result{Stdout: []byte(r.stdout)}, nil
}

// callsOf returns the calls of action a, in order.
func (f *fakeCmd) callsOf(a runner.Action) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.Action == a {
			out = append(out, c)
		}
	}
	return out
}

// count returns how many calls of action a the fake received.
func (f *fakeCmd) count(a runner.Action) int {
	return len(f.callsOf(a))
}

// maxInFlight returns the most calls, of any action, that were in flight at once.
func (f *fakeCmd) maxInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak
}

// maxInFlightOf returns the most calls of action a that were in flight at once.
func (f *fakeCmd) maxInFlightOf(a runner.Action) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peakOf[a]
}

// running returns how many calls of action a are in flight.
func (f *fakeCmd) running(a runner.Action) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inFlight[a]
}

// waitRunning waits until n calls of action a are in flight.
func (f *fakeCmd) waitRunning(t *testing.T, a runner.Action, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.running(a) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%d calls of %s in flight, want %d", f.running(a), a, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// containerLine is one docker.containers line in runner.ContainerColumns
// order: project, service, state, status, image (panel label), oneoff, id, name, imageref, ports.
func containerLine(proj, service, state, status string) string {
	image := "nginx"
	if proj == "panel" {
		image = "panel"
	}
	id := containerID(proj, service)
	return strings.Join([]string{proj, service, state, status, image, "False", id, proj + "-" + service + "-1", image, ""}, "\t") + "\n"
}

// containerID is a stable 64-hex id per (project, service), as Docker gives.
func containerID(proj, service string) string {
	sum := sha256.Sum256([]byte(proj + "/" + service))
	return hex.EncodeToString(sum[:])
}

// Container outputs for a project with the services app and db.
func runningOut(proj string) string {
	return containerLine(proj, "app", "running", "Up 2 minutes") + containerLine(proj, "db", "running", "Up 2 minutes")
}

func partialOut(proj string) string {
	return containerLine(proj, "app", "running", "Up 2 minutes") + containerLine(proj, "db", "exited", "Exited (0) 1 second ago")
}

func stoppedOut(proj string) string {
	return containerLine(proj, "app", "exited", "Exited (0) 1 second ago") + containerLine(proj, "db", "exited", "Exited (137) 1 second ago")
}

func errorOut(proj string) string {
	return containerLine(proj, "app", "exited", "Exited (1) 1 second ago") + containerLine(proj, "db", "running", "Up 2 minutes")
}

// exited1Out is a project whose app exited 1 on SIGTERM: Error, but nothing active.
func exited1Out(proj string) string {
	return containerLine(proj, "app", "exited", "Exited (1) 1 second ago") + containerLine(proj, "db", "exited", "Exited (0) 1 second ago")
}

// inspectLine is one docker.inspect line for the container of (proj, service):
// restarts restarts, started at started (RFC 3339).
func inspectLine(proj, service string, restarts int, started string) string {
	return fmt.Sprintf("%s\t%d\t{\"StartedAt\":%q,\"FinishedAt\":\"0001-01-01T00:00:00Z\",\"ExitCode\":0,\"OOMKilled\":false,\"Error\":\"\"}\n",
		containerID(proj, service), restarts, started)
}

// statsLine is one docker.stats line for the container of (proj, service).
func statsLine(proj, service, cpu, mem string) string {
	return containerID(proj, service) + "\t" + cpu + "\t" + mem + " / 7.6GiB\n"
}

// fakeObserver records the observer calls. Each lockChanged call also reads
// the Controller, which would deadlock if the Controller held its mutex.
type fakeObserver struct {
	ctl *Controller
	id  int64

	mu        sync.Mutex
	cs        []project.Container
	dockerUp  bool
	ok        bool
	recentAge []time.Duration
	changes   []lockChange
}

type lockChange struct {
	fresh    []project.Container
	inFlight bool
	gen      uint64
}

func (o *fakeObserver) recent(maxAge time.Duration) ([]project.Container, bool, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.recentAge = append(o.recentAge, maxAge)
	return o.cs, o.dockerUp, o.ok
}

func (o *fakeObserver) lockChanged(fresh []project.Container) {
	_, in := o.ctl.InFlight(o.id)
	gen := o.ctl.Generation()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.changes = append(o.changes, lockChange{fresh: fresh, inFlight: in, gen: gen})
}
