package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// Commander runs whitelisted commands; *runner.Runner is one, tests use a fake.
type Commander interface {
	Run(ctx context.Context, s runner.Stack, a runner.Action, v runner.Values, secrets []string) (runner.Result, error)
}

// Progress is how far a scan is: folders examined out of the folders listed,
// and the compose file or folder being examined.
type Progress struct {
	Examined int    `json:"examined"`
	Total    int    `json:"total"`
	Current  string `json:"current"`
}

// Row is one compose project a scan found. It is stored in the job's result
// and read back when the owner tracks the scan, so every field round-trips
// through JSON. It holds no secret value.
type Row struct {
	Path           string             `json:"path"`
	ComposeFiles   []string           `json:"composeFiles"` // file names: the compose file, then the override
	DefaultName    string             `json:"defaultName"`
	ComposeProject string             `json:"composeProject"`
	Status         project.Status     `json:"status"`
	Self           bool               `json:"self"`
	Services       int                `json:"services"`
	Details        project.Details    `json:"details"`
	Databases      []project.Database `json:"databases"`
	Warning        string             `json:"warning"`
}

// Result is what a finished scan found. AlreadyTracked and Ignored count the
// projects left out because their path is already stored.
type Result struct {
	Rows           []Row    `json:"rows"`
	FoldersScanned int      `json:"foldersScanned"`
	AlreadyTracked int      `json:"alreadyTracked"`
	Ignored        int      `json:"ignored"`
	Warnings       []string `json:"warnings"`
}

// BusyError is returned by Start while another scan runs.
type BusyError struct {
	ID    int64
	Path  string
	Depth int
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("scan %d of %s (depth %d) is running", e.ID, e.Path, e.Depth)
}

// Job states written by the manager.
const (
	stateDone   = "done"
	stateFailed = "failed"
)

// progressInterval is the shortest time between two progress events.
const progressInterval = 200 * time.Millisecond

// maxDotenv caps how much of a project's .env is read.
const maxDotenv = 1 << 20

// duplicateWarning is the row warning for a compose project name that another
// folder uses too.
func duplicateWarning(name string) string {
	return "Another folder uses the compose project name " + name + "; Docker treats them as one project."
}

// Manager runs scan jobs, one at a time, and publishes their progress.
type Manager struct {
	st       *store.Store
	cmd      Commander
	log      *slog.Logger
	interval time.Duration

	startMu sync.Mutex // serialises Start, so only one job is ever created at a time

	mu      sync.Mutex // guards running and everything in it
	running *job

	wg sync.WaitGroup
}

// job is the in-memory state of the running scan. Its fields are guarded by
// Manager.mu, except id, path and depth, which never change.
type job struct {
	id    int64
	path  string
	depth int

	progress Progress
	subs     *broadcaster
	lastSent time.Time
	timer    *time.Timer // a coalesced progress event waiting to go out
	finished bool
}

// NewManager returns a Manager that stores jobs in st and runs commands with cmd.
func NewManager(st *store.Store, cmd Commander, log *slog.Logger) *Manager {
	return &Manager{st: st, cmd: cmd, log: log, interval: progressInterval}
}

// Start creates a scan job for a resolved workspace path and runs it in the
// background, detached from ctx. It returns the job id, or a *BusyError when
// a scan is already running.
func (m *Manager) Start(ctx context.Context, resolvedPath string, depth int, by string) (int64, error) {
	if depth < 1 || depth > 3 {
		return 0, fmt.Errorf("scan: depth %d is not between 1 and 3", depth)
	}
	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	if j := m.running; j != nil {
		m.mu.Unlock()
		return 0, &BusyError{ID: j.id, Path: j.path, Depth: j.depth}
	}
	m.mu.Unlock()

	id, err := m.st.CreateScanJob(ctx, resolvedPath, depth, by)
	if err != nil {
		return 0, err
	}
	j := &job{id: id, path: resolvedPath, depth: depth, subs: newBroadcaster()}
	m.mu.Lock()
	m.running = j
	m.mu.Unlock()

	m.wg.Add(1)
	//nolint:gosec // G118: the scan outlives the request on purpose (ADR 0007); run uses context.Background
	go m.run(j)
	return id, nil
}

// Subscribe attaches to a running job. It returns the latest progress and a
// channel of further events, ending with "end" and then closed. cancel stops
// the subscription early and is safe to call at any time. live is false when
// the job is not running (finished, interrupted or unknown): the caller reads
// the job from the store instead.
func (m *Manager) Subscribe(id int64) (snapshot Progress, events <-chan Event, cancel func(), live bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.running
	if j == nil || j.id != id {
		return Progress{}, nil, func() {}, false
	}
	ch := j.subs.add()
	cancel = func() {
		m.mu.Lock()
		j.subs.remove(ch)
		m.mu.Unlock()
	}
	return j.progress, ch, cancel, true
}

// Progress returns the latest progress of a running job; false when it is not
// running.
func (m *Manager) Progress(id int64) (Progress, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.running; j != nil && j.id == id {
		return j.progress, true
	}
	return Progress{}, false
}

// wait blocks until every started job has finished.
func (m *Manager) wait() { m.wg.Wait() }

// Wait blocks until the running scan, if any, has finished and stored its
// outcome, or until ctx is done; then it returns ctx's error. The panel calls
// it on shutdown, before it closes the store. A scan still running when ctx
// ends is left: the next start marks its job interrupted.
func (m *Manager) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run scans, stores the outcome and then ends the job for its subscribers.
// FinishJob is written before the job leaves the running set, so a
// subscriber either is live and sees end, or is not live and finds the
// finished job in the store.
func (m *Manager) run(j *job) {
	defer m.wg.Done()
	started := time.Now()
	res, err := m.scanSafely(j)

	state, errMsg := stateDone, ""
	var resJSON json.RawMessage
	if err == nil {
		resJSON, err = json.Marshal(res)
	}
	if err != nil {
		state, errMsg, resJSON = stateFailed, err.Error(), nil
		m.log.Error("scan failed", "job", j.id, "path", j.path, "err", err)
	} else {
		m.log.Info("scan done", "job", j.id, "path", j.path, "folders", res.FoldersScanned,
			"rows", len(res.Rows), "duration", time.Since(started).Round(time.Millisecond))
	}

	m.mu.Lock()
	final := j.progress
	m.mu.Unlock()
	progJSON, perr := json.Marshal(final)
	if perr != nil {
		progJSON = nil
	}
	if err := m.st.FinishJob(context.Background(), j.id, state, progJSON, resJSON, errMsg); err != nil {
		m.log.Error("scan: store the finished job", "job", j.id, "err", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	j.finished = true
	if j.timer != nil {
		j.timer.Stop()
		j.timer = nil
	}
	j.subs.progress(j.progress)
	j.subs.end(state)
	m.running = nil
}

// scanSafely runs the scan and turns a panic into an error.
func (m *Manager) scanSafely(j *job) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("scan panicked", "job", j.id, "panic", r, "stack", string(debug.Stack()))
			err = errors.New("the scan stopped on an internal error")
		}
	}()
	return m.scan(context.Background(), j)
}

func (m *Manager) scan(ctx context.Context, j *job) (Result, error) {
	folders, warnings := List(j.path, j.depth)
	total := len(folders)
	m.setProgress(j, Progress{Total: total})

	known, err := m.st.KnownProjects(ctx)
	if err != nil {
		return Result{}, err
	}
	containers, cerr := ReadContainers(ctx, m.cmd)
	if cerr != nil {
		m.log.Warn("scan: cannot read containers; every status is unknown", "job", j.id, "err", cerr)
	}
	self := project.SelfProject(containers)

	res := Result{Rows: []Row{}, FoldersScanned: total, Warnings: warnings}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	for i, f := range folders {
		current := f.Path
		if f.ComposeFile != "" {
			current = f.ComposeFile
		}
		m.setProgress(j, Progress{Examined: i, Total: total, Current: current})
		if f.ComposeFile == "" {
			continue
		}
		if k, ok := known[f.Path]; ok {
			if k.State == "ignored" {
				res.Ignored++
			} else {
				res.AlreadyTracked++
			}
			continue
		}
		row, configRead := m.examine(ctx, f)
		if configRead && cerr == nil {
			names := make([]string, len(row.Details.Services))
			for i, s := range row.Details.Services {
				names[i] = s.Name
			}
			row.Status = project.StatusOf(containers, row.ComposeProject, names)
		}
		row.Self = self != "" && row.ComposeProject == self
		res.Rows = append(res.Rows, row)
	}
	m.setProgress(j, Progress{Examined: total, Total: total})

	warnDuplicates(res.Rows, known)
	return res, nil
}

// examine reads one project's .env and compose config and fills its row,
// with status Unknown and without self. A config failure keeps the row with a
// warning, empty details and a guessed compose project name; configRead is
// false then.
func (m *Manager) examine(ctx context.Context, f Folder) (row Row, configRead bool) {
	row = Row{
		Path:         f.Path,
		ComposeFiles: []string{filepath.Base(f.ComposeFile)},
		Details:      project.Details{Services: []project.Service{}},
		Databases:    []project.Database{},
		Status:       project.StatusUnknown,
	}
	values := runner.Values{"dir": f.Path, "file": f.ComposeFile}
	action := runner.ComposeConfig
	if f.Override != "" {
		row.ComposeFiles = append(row.ComposeFiles, filepath.Base(f.Override))
		values["override"] = f.Override
		action = runner.ComposeConfigOverride
	}

	dotenv := readDotenv(filepath.Join(f.Path, ".env"))
	secrets := SecretValues(dotenv)

	cfg, warning := m.readConfig(ctx, action, values, secrets)
	if warning != "" {
		name := strings.TrimSpace(dotenv["COMPOSE_PROJECT_NAME"])
		if name == "" {
			name = NormalizeProjectName(filepath.Base(f.Path))
		}
		row.ComposeProject, row.DefaultName, row.Warning = name, name, warning
		return row, false
	}
	row.ComposeProject, row.DefaultName = cfg.Name, cfg.Name
	row.Details = cfg.Details()
	row.Services = len(cfg.Services)
	row.Databases = DetectDatabases(cfg, dotenv)
	return row, true
}

// readConfig runs the config command and parses it. On failure it returns a
// row warning: the first line of the (masked) error that is not a compose
// warning, or a fixed message.
func (m *Manager) readConfig(ctx context.Context, action runner.Action, values runner.Values, secrets []string) (ComposeConfig, string) {
	res, err := m.cmd.Run(ctx, runner.StackCompose, action, values, secrets)
	if err != nil {
		text := err.Error()
		var exitErr *runner.ExitError
		if errors.As(err, &exitErr) && strings.TrimSpace(exitErr.Stderr) != "" {
			text = exitErr.Stderr
		}
		// ExitError carries masked stderr; mask again so no other error text
		// can carry a value either.
		first := firstErrorLine(runner.Mask(text, secrets))
		if first == "" {
			first = "docker compose config failed."
		}
		return ComposeConfig{}, first
	}
	if res.Truncated {
		return ComposeConfig{}, "The compose config is too large to read."
	}
	cfg, err := ParseConfig(res.Stdout)
	if err != nil || cfg.Name == "" {
		return ComposeConfig{}, "docker compose config did not return a readable config."
	}
	return cfg, ""
}

// firstErrorLine returns the first non-blank line of text that is not a
// compose warning, such as "WARN[0000] ... the attribute `version` is
// obsolete", which compose prints before the error for legacy files. With
// only warnings it returns the first of them.
func firstErrorLine(text string) string {
	fallback := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "WARN") {
			return line
		}
		if fallback == "" {
			fallback = line
		}
	}
	return fallback
}

// readDotenv parses a project's .env if it is a regular file. A symlink, a
// missing file or anything else gives an empty map. O_NOFOLLOW refuses a
// symlink at open time, and O_NONBLOCK keeps a FIFO from blocking the open.
func readDotenv(path string) map[string]string {
	//nolint:gosec // G304: the path is <project folder>/.env inside the workspace, opened without following symlinks
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return map[string]string{}
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return map[string]string{}
	}
	b, err := io.ReadAll(io.LimitReader(f, maxDotenv))
	if err != nil {
		return map[string]string{}
	}
	return ParseDotenv(b)
}

// warnDuplicates adds the duplicate-name warning to every row whose compose
// project name another row of this scan, or a stored project, also uses.
func warnDuplicates(rows []Row, known map[string]store.Known) {
	stored := map[string]bool{}
	for _, k := range known {
		stored[k.ComposeProject] = true
	}
	count := map[string]int{}
	for _, r := range rows {
		count[r.ComposeProject]++
	}
	for i := range rows {
		name := rows[i].ComposeProject
		if name == "" || (count[name] < 2 && !stored[name]) {
			continue
		}
		if rows[i].Warning != "" {
			rows[i].Warning += "\n"
		}
		rows[i].Warning += duplicateWarning(name)
	}
}

// setProgress records p as the job's latest progress and publishes it,
// coalesced to one event per interval: an update inside the interval arms a
// timer that sends whatever is latest when the interval ends.
func (m *Manager) setProgress(j *job, p Progress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.progress = p
	if j.finished || j.timer != nil {
		return
	}
	wait := m.interval - time.Since(j.lastSent)
	if wait <= 0 {
		j.lastSent = time.Now()
		j.subs.progress(p)
		return
	}
	j.timer = time.AfterFunc(wait, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		j.timer = nil
		if j.finished {
			return
		}
		j.lastSent = time.Now()
		j.subs.progress(j.progress)
	})
}

// ReadContainers runs docker.containers once and parses it. Output past the
// runner's cap is an error, since the last lines would be missing.
func ReadContainers(ctx context.Context, cmd Commander) ([]project.Container, error) {
	res, err := cmd.Run(ctx, runner.StackDocker, runner.DockerContainers, nil, nil)
	if err != nil {
		return nil, err
	}
	if res.Truncated {
		return nil, errors.New("the container list is too large to read")
	}
	return project.ParseContainers(res.Stdout), nil
}
