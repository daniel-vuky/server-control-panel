// Package control runs the lifecycle commands (start, stop, restart) of
// tracked projects, one at a time per project, and records each in the
// activity log.
package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// Outcome is how a lifecycle command ended.
type Outcome string

// The outcomes of a command that ran.
const (
	OutcomeOK       Outcome = "ok"
	OutcomeFailed   Outcome = "failed"
	OutcomeTimedOut Outcome = "timed_out"
)

// Result is the outcome of a command and the project's status after it, as
// re-read from Docker (unknown when the re-read failed). Message is empty on
// ok, and the masked reason otherwise.
type Result struct {
	Outcome Outcome
	Status  project.Status
	Message string
}

// The refusal codes.
const (
	CodeLocked     = "locked"
	CodeNotAllowed = "not_allowed"
	CodeDockerDown = "docker_down"
)

// Refusal is returned by Do when the command was not run. Status is the
// project's status at the time: the in-flight status when it is locked, and
// unknown when Docker is down.
type Refusal struct {
	Code    string // locked | not_allowed | docker_down
	Message string
	Status  project.Status
}

func (r *Refusal) Error() string { return r.Message }

// observer is told about lock changes, and holds a recent container list the
// pre-check may reuse. *Hub implements it.
type observer interface {
	// recent returns the last container list when it is at most maxAge old;
	// ok is false otherwise. dockerUp false means the last check found Docker
	// down.
	recent(maxAge time.Duration) (cs []project.Container, dockerUp bool, ok bool)
	// lockChanged is called after a lock is taken (fresh nil) or released
	// (fresh is the re-read container list, or nil when it failed).
	lockChanged(fresh []project.Container)
}

const (
	// precheckMaxAge is how old a recent container list may be for the pre-check.
	precheckMaxAge = 10 * time.Second
	// readTimeout bounds the pre-check's own docker.containers read.
	readTimeout = 15 * time.Second
	// rereadTimeout bounds the docker.containers read after the command.
	rereadTimeout = 10 * time.Second
	// maxReason is the most characters of stderr kept as a failure reason.
	maxReason = 300

	msgDockerDown   = "Docker is not responding. Try again when it is back."
	msgTimedOut     = "Timed out after 60 seconds."
	msgRereadFailed = "The command finished but Docker could not be read."
	msgInternal     = "internal error"
)

// Controller holds the per-project locks and runs the lifecycle commands.
//
// Lock order: the Controller never calls the store or the observer while it
// holds mu. The observer (the Hub) may call InFlight and Generation while it
// holds its own mutex, so the only order is hub.mu → ctl.mu.
type Controller struct {
	st  *store.Store
	cmd scan.Commander
	log *slog.Logger
	obs observer // set once by New before any Do; nil means none

	mu    sync.Mutex
	locks map[int64]project.Status // project id → in-flight status
	gen   uint64

	wg sync.WaitGroup
}

// NewController returns a Controller that records activity in st and runs
// commands with cmd.
func NewController(st *store.Store, cmd scan.Commander, log *slog.Logger) *Controller {
	return &Controller{st: st, cmd: cmd, log: log, locks: map[int64]project.Status{}}
}

// setObserver sets the observer. It must be called before the first Do.
func (c *Controller) setObserver(o observer) { c.obs = o }

// InFlight returns the in-flight status of a project while a command runs on
// it; false when it is not locked.
func (c *Controller) InFlight(id int64) (project.Status, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.locks[id]
	return s, ok
}

// Generation is bumped on every lock take and release.
func (c *Controller) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// Wait blocks until every running command has finished and stored its
// outcome, or until ctx is done; then it returns ctx's error. The panel calls
// it on shutdown, before it closes the store. A command still running when
// ctx ends is left: the next open marks its activity row interrupted.
func (c *Controller) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Do runs action a on tracked project id for actor and waits for the outcome.
// It returns store.ErrNotFound for an unknown or ignored project, a *Refusal
// when the command may not run, or another error.
//
// The checks and the lock take run on ctx. Once the lock is taken, the
// command, the re-read and the release run on their own, detached from ctx's
// cancellation: when ctx ends first, Do returns ctx.Err() and the command
// still finishes, records its outcome and releases the lock.
func (c *Controller) Do(ctx context.Context, id int64, a project.Action, actor string) (Result, error) {
	act, verb, ok := describe(a)
	if !ok {
		return Result{}, fmt.Errorf("control: unknown action %q", a)
	}
	t, err := c.st.TrackedProject(ctx, id)
	if err != nil {
		return Result{}, err
	}
	rec := store.Activity{Actor: actor, ActorKind: store.ActorPerson, Action: act, ProjectID: t.ID, Target: t.DisplayName}

	cs, up, err := c.precheck(ctx)
	if err != nil {
		return Result{}, err
	}
	if !up {
		return Result{}, c.refuse(ctx, rec, &Refusal{Code: CodeDockerDown, Message: msgDockerDown, Status: project.StatusUnknown})
	}
	if s, locked := c.InFlight(id); locked {
		return Result{}, c.refuse(ctx, rec, lockedRefusal(t.DisplayName, s))
	}
	status := project.StatusOf(cs, t.ComposeProject, t.Services)
	if t.Self {
		return Result{}, c.refuse(ctx, rec, &Refusal{
			Code: CodeNotAllowed, Status: status,
			Message: t.DisplayName + " is the panel itself and is Monitor only.",
		})
	}
	if !slices.Contains(project.Allowed(status, false, true), a) {
		return Result{}, c.refuse(ctx, rec, &Refusal{
			Code: CodeNotAllowed, Status: status,
			Message: verb + " is not allowed while " + t.DisplayName + " is " + project.Word(status) + ".",
		})
	}

	// Take the lock: check and set in one critical section.
	c.mu.Lock()
	if s, locked := c.locks[id]; locked {
		c.mu.Unlock()
		return Result{}, c.refuse(ctx, rec, lockedRefusal(t.DisplayName, s))
	}
	c.locks[id] = project.InFlight(a)
	c.gen++
	c.mu.Unlock()

	activityID, err := c.st.StartActivity(ctx, rec)
	if err != nil {
		c.unlock(id)
		c.notify(nil)
		return Result{}, err
	}
	c.notify(nil)

	done := make(chan Result, 1) // buffered: the goroutine never blocks when Do has returned
	c.wg.Add(1)
	go c.run(context.WithoutCancel(ctx), t, a, activityID, done)
	select {
	case res := <-done:
		return res, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// precheck returns the containers to judge the current status by: the
// observer's recent list when there is one, otherwise one read of Docker.
// up is false when Docker is down. err is ctx's error when ctx ended during
// the read.
func (c *Controller) precheck(ctx context.Context) (cs []project.Container, up bool, err error) {
	if c.obs != nil {
		if cs, up, ok := c.obs.recent(precheckMaxAge); ok {
			return cs, up, nil
		}
	}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	cs, rerr := scan.ReadContainers(rctx, c.cmd)
	if rerr != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		c.log.Warn("control: cannot read containers", "err", rerr)
		return nil, false, nil
	}
	return cs, true, nil
}

// refuse records a refusal and returns it.
func (c *Controller) refuse(ctx context.Context, rec store.Activity, r *Refusal) error {
	if err := c.st.RecordActivity(ctx, rec, store.ResultRefused, r.Message); err != nil {
		c.log.Error("control: record the refusal", "project", rec.ProjectID, "err", err)
	}
	return r
}

func lockedRefusal(name string, s project.Status) *Refusal {
	return &Refusal{Code: CodeLocked, Status: s, Message: name + " is " + project.Word(s) + "; wait for it to finish."}
}

// unlock clears the lock of project id and bumps the generation.
func (c *Controller) unlock(id int64) {
	c.mu.Lock()
	delete(c.locks, id)
	c.gen++
	c.mu.Unlock()
}

// notify tells the observer, if any, that a lock changed. Never call it with
// mu held.
func (c *Controller) notify(fresh []project.Container) {
	if c.obs != nil {
		c.obs.lockChanged(fresh)
	}
}

// run executes the command and always releases the lock, even on a panic,
// which becomes a failed outcome and is not re-raised.
func (c *Controller) run(ctx context.Context, t store.Tracked, a project.Action, activityID int64, done chan<- Result) {
	defer c.wg.Done()
	started := time.Now()
	var (
		res   Result
		fresh []project.Container
	)
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("control: command panicked", "project", t.ID, "action", a, "panic", r, "stack", string(debug.Stack()))
			res, fresh = Result{Outcome: OutcomeFailed, Status: project.StatusUnknown, Message: msgInternal}, nil
		}
		c.unlock(t.ID)
		if err := c.st.FinishActivity(ctx, activityID, activityResult(res.Outcome), res.Message); err != nil {
			c.log.Error("control: finish the activity", "project", t.ID, "action", a, "err", err)
		}
		c.notify(fresh)
		// The message may hold (masked) stderr; it stays out of the log.
		c.log.Info("project action", "project", t.ID, "action", a, "outcome", res.Outcome,
			"status", res.Status, "duration", time.Since(started).Round(time.Millisecond))
		done <- res
	}()
	res, fresh = c.execute(ctx, t, a)
}

// execute runs the command, re-reads Docker and decides the outcome. fresh is
// the re-read container list, nil when the re-read failed.
func (c *Controller) execute(ctx context.Context, t store.Tracked, a project.Action) (res Result, fresh []project.Container) {
	if len(t.ComposeFiles) == 0 {
		return Result{Outcome: OutcomeFailed, Status: project.StatusUnknown, Message: "The project has no compose file."}, nil
	}
	values := runner.Values{"dir": t.Path, "file": filepath.Join(t.Path, t.ComposeFiles[0])}
	override := len(t.ComposeFiles) > 1
	if override {
		values["override"] = filepath.Join(t.Path, t.ComposeFiles[1])
	}
	secrets := scan.SecretValues(scan.ReadDotenv(filepath.Join(t.Path, ".env")))
	_, runErr := c.cmd.Run(ctx, runner.StackCompose, composeAction(a, override), values, secrets)

	rctx, cancel := context.WithTimeout(ctx, rereadTimeout)
	cs, readErr := scan.ReadContainers(rctx, c.cmd)
	cancel()
	res.Status = project.StatusUnknown
	if readErr == nil {
		fresh = cs
		res.Status = project.StatusOf(cs, t.ComposeProject, t.Services)
	} else {
		c.log.Warn("control: cannot re-read containers", "project", t.ID, "err", readErr)
	}
	finishedBut := "The command finished but " + t.DisplayName + " is " + project.Word(res.Status) + "."

	var exitErr *runner.ExitError
	var timeoutErr *runner.TimeoutError
	switch {
	case errors.As(runErr, &timeoutErr):
		res.Outcome, res.Message = OutcomeTimedOut, msgTimedOut
	case errors.As(runErr, &exitErr):
		res.Outcome, res.Message = OutcomeFailed, exitReason(exitErr, secrets)
	case runErr != nil:
		res.Outcome, res.Message = OutcomeFailed, runner.Mask(runErr.Error(), secrets)
	case readErr != nil:
		res.Outcome, res.Message = OutcomeFailed, msgRereadFailed
	case a == project.ActionStop:
		res.Outcome = OutcomeOK
		if project.AnyActive(cs, t.ComposeProject) {
			res.Outcome, res.Message = OutcomeFailed, finishedBut
		}
	case res.Status == project.StatusRunning || res.Status == project.StatusPartial:
		res.Outcome = OutcomeOK
	default:
		res.Outcome, res.Message = OutcomeFailed, finishedBut
	}
	return res, fresh
}

// exitReason is the last lines of the command's stderr, at most maxReason
// characters, or the error text when stderr is empty. Stderr is already
// masked by the runner; it is masked again so no other text can carry a value.
func exitReason(e *runner.ExitError, secrets []string) string {
	text := strings.TrimSpace(e.Stderr)
	if text == "" {
		text = e.Error()
	}
	return lastChars(runner.Mask(text, secrets), maxReason)
}

// lastChars returns the last whole lines of s that fit in n characters, or the
// last n characters of the last line when even that line is longer.
func lastChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	tail := r[len(r)-n:]
	if r[len(r)-n-1] == '\n' {
		return string(tail) // the tail starts on a whole line
	}
	// Drop the partial first line when a later line starts within the tail.
	for i, ch := range tail {
		if ch == '\n' {
			if i+1 < len(tail) {
				return string(tail[i+1:])
			}
			break
		}
	}
	return string(tail)
}

// describe returns the activity action and the verb of a lifecycle action.
func describe(a project.Action) (act, verb string, ok bool) {
	switch a {
	case project.ActionStart:
		return store.ActProjectStart, "Start", true
	case project.ActionStop:
		return store.ActProjectStop, "Stop", true
	case project.ActionRestart:
		return store.ActProjectRestart, "Restart", true
	}
	return "", "", false
}

// composeAction is the runner entry of a lifecycle action, with or without the
// override file.
func composeAction(a project.Action, override bool) runner.Action {
	switch a {
	case project.ActionStart:
		if override {
			return runner.ComposeUpOverride
		}
		return runner.ComposeUp
	case project.ActionStop:
		if override {
			return runner.ComposeStopOverride
		}
		return runner.ComposeStop
	}
	if override {
		return runner.ComposeRestartOverride
	}
	return runner.ComposeRestart
}

func activityResult(o Outcome) string {
	switch o {
	case OutcomeOK:
		return store.ResultOK
	case OutcomeTimedOut:
		return store.ResultTimedOut
	}
	return store.ResultFailed
}
