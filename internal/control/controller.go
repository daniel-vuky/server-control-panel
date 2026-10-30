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
// ok, and the masked reason otherwise. Actions are what that status allows
// now (empty when the re-read failed), so a client never re-derives them.
type Result struct {
	Outcome Outcome
	Status  project.Status
	Actions []project.Action
	Message string
}

// The refusal codes.
const (
	CodeLocked     = "locked"
	CodeNotAllowed = "not_allowed"
	CodeDockerDown = "docker_down"
)

// Refusal is returned by Do, Untrack and WithLocks when nothing was run.
// Status is the project's status at the time: the in-flight status when it is
// locked by a command, and unknown when Docker is down. ProjectID is the
// project that caused it. Held is true when that project is held for a store
// write (WithLocks) rather than running a command; WithLocks then gives the
// status unknown, and Do the project's computed status.
type Refusal struct {
	Code      string // locked | not_allowed | docker_down
	Message   string
	Status    project.Status
	ProjectID int64
	Held      bool
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
	// (fresh is the re-read container list, or nil when it failed), except
	// after a successful WithLocks write: its caller refreshes the hub.
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
	msgHeld         = " is being removed from the panel."

	// stopTemplateDown is the stop template that stops with compose down.
	stopTemplateDown = "down"
)

// projectLock is one entry of the Controller's lock map: a command running
// (status is its in-flight status), or the project held for a store write
// (held, with no status).
type projectLock struct {
	status project.Status
	held   bool
}

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
	locks map[int64]projectLock // project id → its lock
	gen   uint64

	wg sync.WaitGroup
}

// NewController returns a Controller that records activity in st and runs
// commands with cmd.
func NewController(st *store.Store, cmd scan.Commander, log *slog.Logger) *Controller {
	return &Controller{st: st, cmd: cmd, log: log, locks: map[int64]projectLock{}}
}

// setObserver sets the observer. It must be called before the first Do.
func (c *Controller) setObserver(o observer) { c.obs = o }

// InFlight returns the in-flight status of a project while a command runs on
// it; false when no command runs (a project held for a store write is not in
// flight).
func (c *Controller) InFlight(id int64) (project.Status, bool) {
	s, held, ok := c.Lock(id)
	if !ok || held {
		return "", false
	}
	return s, true
}

// Lock tells whether project id is locked: by a command (status is its
// in-flight status) or held for a store write (held true, status empty). ok is
// false when it is not locked. Views takes it as its lock lookup.
func (c *Controller) Lock(id int64) (status project.Status, held bool, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.locks[id]
	return l.status, l.held, ok
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
// when the command may not run, or another error. A project a rescan could not
// find counts as Not found, which allows no lifecycle action. Once the lock is
// taken Do reads the project again, so an untrack or a removal that committed
// meanwhile runs nothing: ErrNotFound when it is gone, not_allowed when it is
// now missing.
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
		return Result{}, c.refuse(ctx, rec, &Refusal{Code: CodeDockerDown, Message: msgDockerDown, Status: project.StatusUnknown, ProjectID: t.ID})
	}
	status := statusOf(t, cs)
	if s, held, locked := c.Lock(id); locked {
		return Result{}, c.refuse(ctx, rec, lockedRefusal(t, projectLock{status: s, held: held}, status))
	}
	if r := notAllowed(t, a, verb, status); r != nil {
		return Result{}, c.refuse(ctx, rec, r)
	}

	// Take the lock: check and set in one critical section.
	c.mu.Lock()
	if l, locked := c.locks[id]; locked {
		c.mu.Unlock()
		return Result{}, c.refuse(ctx, rec, lockedRefusal(t, l, status))
	}
	c.locks[id] = projectLock{status: project.InFlight(a)}
	c.gen++
	c.mu.Unlock()

	// Re-read under the lock: an untrack, a workspace removal or a rescan may
	// have committed since the first read.
	if t, err = c.st.TrackedProject(ctx, id); err != nil || t.MissingSince != nil {
		c.unlock(id)
		c.notify(nil)
		if err != nil {
			return Result{}, err
		}
		return Result{}, c.refuse(ctx, rec, &Refusal{
			Code: CodeNotAllowed, Status: project.StatusNotFound, ProjectID: t.ID,
			Message: verb + " is not allowed while " + t.DisplayName + " is " + project.Word(project.StatusNotFound) + ".",
		})
	}

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

// statusOf is the status the pre-check judges t by: Not found when a rescan
// could not find its folder, otherwise its status from cs.
func statusOf(t store.Tracked, cs []project.Container) project.Status {
	if t.MissingSince != nil {
		return project.StatusNotFound
	}
	return project.StatusOf(cs, t.ComposeProject, t.Services)
}

// notAllowed is the refusal of action a on t at status, or nil when a is
// allowed.
func notAllowed(t store.Tracked, a project.Action, verb string, status project.Status) *Refusal {
	if t.Self {
		return &Refusal{
			Code: CodeNotAllowed, Status: status, ProjectID: t.ID,
			Message: t.DisplayName + " is the panel itself and is Monitor only.",
		}
	}
	if !slices.Contains(project.Allowed(status, false, true), a) {
		return &Refusal{
			Code: CodeNotAllowed, Status: status, ProjectID: t.ID,
			Message: verb + " is not allowed while " + t.DisplayName + " is " + project.Word(status) + ".",
		}
	}
	return nil
}

// lockedRefusal is the refusal for t while lock l is on it. status is the
// project's computed status, given when it is held for a store write.
func lockedRefusal(t store.Tracked, l projectLock, status project.Status) *Refusal {
	if l.held {
		return &Refusal{Code: CodeLocked, Status: status, ProjectID: t.ID, Held: true, Message: t.DisplayName + msgHeld}
	}
	return &Refusal{
		Code: CodeLocked, Status: l.status, ProjectID: t.ID,
		Message: t.DisplayName + " is " + project.Word(l.status) + "; wait for it to finish.",
	}
}

// WithLocks holds the locks of every project in ids while fn runs, so no
// command starts on them meanwhile; it is how a store write that drops
// projects (untrack, workspace removal) runs. The locks are taken in one
// critical section, or none is: when any id is locked it returns a locked
// *Refusal for the first such id and fn does not run. fn runs outside the
// Controller's mutex, so it may call the store. The locks are released on
// every path, a panic in fn included (the panic goes on). fn's error is
// returned as is.
//
// The observer hears of the take, and of the release only when fn failed or
// panicked, so the projects get their actions back. After a successful write
// it is not told: its cached list still holds the rows fn changed, and a
// publish from it would show them again with their actions. The caller must
// call Hub.Refresh after a successful write instead; the generation bump on
// release makes a tick already running read again.
func (c *Controller) WithLocks(ids []int64, fn func() error) (err error) {
	c.mu.Lock()
	for _, id := range ids {
		if l, locked := c.locks[id]; locked {
			c.mu.Unlock()
			status := l.status
			msg := "A project is " + project.Word(status) + "; wait for it to finish."
			if l.held {
				status, msg = project.StatusUnknown, "A project"+msgHeld
			}
			return &Refusal{Code: CodeLocked, Status: status, ProjectID: id, Held: l.held, Message: msg}
		}
	}
	for _, id := range ids {
		c.locks[id] = projectLock{held: true}
	}
	c.gen++
	c.mu.Unlock()
	c.notify(nil)

	succeeded := false // stays false on a panic
	defer func() {
		c.mu.Lock()
		for _, id := range ids {
			delete(c.locks, id)
		}
		c.gen++
		c.mu.Unlock()
		if !succeeded {
			c.notify(nil)
		}
	}()
	err = fn()
	succeeded = err == nil
	return err
}

// Untrack stops tracking project id for actor and returns it as it was: a
// present project becomes ignored, and one a rescan could not find is removed
// from the list. It holds the project's lock for the store write, so it is
// refused (locked) while a command runs on it, and no command can start
// meanwhile. It records project.untrack, or project.remove for a missing
// project, as ok or refused. It returns store.ErrNotFound for an unknown or
// ignored project. After it succeeds the caller must call Hub.Refresh: the
// release does not tell the hub (see WithLocks).
func (c *Controller) Untrack(ctx context.Context, id int64, actor string) (store.Tracked, error) {
	t, err := c.st.TrackedProject(ctx, id)
	if err != nil {
		return store.Tracked{}, err
	}
	rec := store.Activity{Actor: actor, ActorKind: store.ActorPerson, Action: untrackAction(t), ProjectID: t.ID, Target: t.DisplayName}

	var (
		ran  bool
		gone store.Tracked
	)
	err = c.WithLocks([]int64{id}, func() error {
		ran = true
		var err error
		gone, err = c.st.UntrackProject(ctx, id)
		return err
	})
	if !ran {
		var r *Refusal
		if errors.As(err, &r) {
			return store.Tracked{}, c.refuse(ctx, rec, lockedRefusal(t, projectLock{status: r.Status, held: r.Held}, project.StatusUnknown))
		}
		return store.Tracked{}, err
	}
	if err != nil {
		return store.Tracked{}, err
	}
	// The write decides which it was: the project may have gone missing, or
	// come back, since the first read.
	rec.Action = untrackAction(gone)
	if err := c.st.RecordActivity(ctx, rec, store.ResultOK, ""); err != nil {
		c.log.Error("control: record the untrack", "project", id, "err", err)
	}
	return gone, nil
}

// untrackAction is the activity action of untracking t.
func untrackAction(t store.Tracked) string {
	if t.MissingSince != nil {
		return store.ActProjectRemove
	}
	return store.ActProjectUntrack
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
	_, runErr := c.cmd.Run(ctx, runner.StackCompose, composeAction(a, override, t.StopTemplate), values, secrets)

	rctx, cancel := context.WithTimeout(ctx, rereadTimeout)
	cs, readErr := scan.ReadContainers(rctx, c.cmd)
	cancel()
	res.Status = project.StatusUnknown
	if readErr == nil {
		// Never nil on success: the observer reads nil as a failed re-read,
		// and an empty list (nothing left running on the host) is a real one.
		fresh = append([]project.Container{}, cs...)
		res.Status = project.StatusOf(cs, t.ComposeProject, t.Services)
	} else {
		c.log.Warn("control: cannot re-read containers", "project", t.ID, "err", readErr)
	}
	res.Actions = project.Allowed(res.Status, t.Self, readErr == nil)
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
// override file. Stop runs compose down when the project's stop template is
// "down"; start and restart ignore the template.
func composeAction(a project.Action, override bool, template string) runner.Action {
	switch a {
	case project.ActionStart:
		if override {
			return runner.ComposeUpOverride
		}
		return runner.ComposeUp
	case project.ActionStop:
		if template == stopTemplateDown {
			if override {
				return runner.ComposeDownOverride
			}
			return runner.ComposeDown
		}
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
