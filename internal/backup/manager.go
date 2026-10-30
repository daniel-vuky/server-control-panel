// Package backup runs manual database backups (spec §4): the checks before a
// backup is queued, one worker panel-wide that dumps, compresses and uploads to
// Google Drive, the step progress the live state shows, and the hints for a
// failed dump.
package backup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// Runner runs the registry's commands; *runner.Runner satisfies it. Run serves
// the size reads, RunTo the dumps.
type Runner interface {
	Run(ctx context.Context, s runner.Stack, a runner.Action, v runner.Values, secrets []string) (runner.Result, error)
	RunTo(ctx context.Context, s runner.Stack, a runner.Action, v runner.Values, secrets []string, stdout io.Writer) ([]byte, error)
}

// Containers returns a recent container list, ok false when Docker is down
// (control.Hub.RecentContainers). The list is only read.
type Containers func(ctx context.Context) (cs []project.Container, ok bool)

// The refusal codes.
const (
	CodeNotFound          = "not_found"        // the project is not tracked or its folder is missing
	CodeUnknownDatabase   = "unknown_database" // the key is not one of the project's databases
	CodeNotSupported      = "not_supported"
	CodeNoDBName          = "no_db_name"
	CodeDriveNotConnected = "drive_not_connected"
	CodeDriveExpired      = "drive_expired"
	CodeFolderMissing     = "folder_missing"
	CodeAlreadyRunning    = "already_running"
	CodeDBNotRunning      = "db_not_running"
	CodeDiskShort         = "disk_short"
	CodeNotRetryable      = "not_retryable"
)

// The backup states and step names, as stored.
const (
	StateQueued      = "queued"
	StateRunning     = "running"
	StateOK          = "ok"
	StateFailed      = "failed"
	StateRefused     = "refused"
	StateInterrupted = "interrupted"

	StepDump     = "dump"
	StepCompress = "compress"
	StepUpload   = "upload"
	StepPrune    = "prune"

	stepWaiting = "waiting"
	stepRunning = "running"
	stepDone    = "done"
	stepFailed  = "failed"
	stepSkipped = "skipped"
)

// The step indexes in Backup.Steps.
const (
	iDump = iota
	iCompress
	iUpload
	iPrune
)

// The words of the refusals and of the steps.
const (
	wordsNotFound        = "This project is no longer tracked, or its folder is missing."
	wordsUnknownDatabase = "This database is no longer detected."
	wordsSQLite          = "SQLite backups are not supported yet"
	wordsOutside         = "Backups of databases outside a container are not supported yet"
	wordsType            = "Backups of this database type are not supported yet"
	wordsNoService       = "The database's service could not be found"
	wordsNoCompose       = "The project has no compose file."
	wordsNoDBName        = "The database name is not known. Set MYSQL_DATABASE (or MONGO_INITDB_DATABASE) on the service, then rescan."
	wordsNotConfigured   = "Google Drive is not set up on this server."
	wordsNotConnected    = "Connect Google Drive first"
	wordsNotRetryable    = "Only a failed, refused or interrupted backup can be retried."
	wordsInterrupted     = "The panel restarted during this backup"
	wordsRetention       = "No retention set yet. All copies are kept."
	wordsTempDeleted     = "temp file deleted"
)

const (
	mib = int64(1) << 20
	// diskMargin is added to half the database size: free ≥ size/2 + 100 MiB.
	diskMargin = 100 * mib
	// fallbackNeed is the free space needed when the size read fails.
	fallbackNeed = 500 * mib
	// publishEvery is the most often progress within a step is published.
	publishEvery = 500 * time.Millisecond
	// storeWait bounds the worker's store writes, which outlive a cancel.
	storeWait = 5 * time.Second
)

// ErrClosed is returned by Enqueue and Retry after Close.
var ErrClosed = errors.New("backup: the manager is closed")

// Refusal is why a backup was not queued. Words is what the UI shows. ID is the
// backup the refusal points to: the queued or running one for already_running,
// the refused row written for a refusal at the Drive, service, running or disk
// check, and 0 for an input error (not_found, unknown_database, not_supported
// at the location check, not_retryable). NeedBytes and FreeBytes are set for
// disk_short, Service once the compose service is resolved.
type Refusal struct {
	Code, Words          string
	ID                   int64
	NeedBytes, FreeBytes int64
	Service              string
}

func (r *Refusal) Error() string { return "backup: " + r.Code + ": " + r.Words }

// dbRef is one database of one project: the unit of "one backup at a time".
type dbRef struct {
	project int64
	key     string
}

// reservation is an Enqueue between its first slow check and its outcome.
// Another Enqueue of the same database waits on done and returns the same
// outcome: the row it queued (as already_running) or its error.
type reservation struct {
	done    chan struct{}
	waiters int // guarded by Manager.mu; only tests read it
	id      int64
	err     error // set before done is closed
}

// job is a queued or running backup. b is guarded by Manager.mu: the worker is
// its only writer and changes it under the lock; Active copies it under the
// lock.
type job struct {
	b          store.Backup
	t          store.Tracked
	db         project.Database
	plan       plan
	activityID int64
	published  time.Time // the last publish of this job, for the throttle
	tmpMade    bool      // the temporary file was created; only the worker uses it
}

// plan is how a backup runs: the resolved service and the commands.
type plan struct {
	service    string
	image      string // the service's image, for the tool and hint words; "" when not known
	values     runner.Values
	dump, size runner.Action
	tool       string
	secrets    []string
}

// Manager queues and runs the backups: one worker panel-wide, FIFO, and one
// queued or running backup per database.
//
// Locking: mu guards the jobs, the queue, the reservations and the callbacks.
// It is never held while calling the store, the runner, Drive or the container
// list; OnChange callbacks run after it is released. The hub may call Active
// while it holds hub.mu (hub.mu → mgr.mu); the Manager never calls the hub
// under mu.
type Manager struct {
	st      *store.Store
	r       Runner
	d       *drive.Service
	cs      Containers
	dataDir string
	now     func() time.Time
	log     *slog.Logger

	// Tests replace these.
	freeSpace    func(dir string) (int64, error)
	publishEvery time.Duration
	minFree      atomic.Int64

	mu       sync.Mutex
	jobs     map[int64]*job
	queue    []*job
	running  *job
	reserved map[dbRef]*reservation
	onChange []func()
	started  bool
	closed   bool
	cancel   context.CancelFunc
	wake     chan struct{}
	wg       sync.WaitGroup
}

// New returns a Manager that stores in st, runs commands with r, uploads with
// d, reads containers with cs and writes its temporary files under
// dataDir/tmp. now is time.Now and log slog.Default when nil.
func New(st *store.Store, r Runner, d *drive.Service, cs Containers, dataDir string, now func() time.Time, log *slog.Logger) *Manager {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		st: st, r: r, d: d, cs: cs, dataDir: dataDir, now: now, log: log,
		freeSpace: freeSpace, publishEvery: publishEvery,
		jobs: map[int64]*job{}, reserved: map[dbRef]*reservation{},
		wake: make(chan struct{}, 1),
	}
}

// SetMinFree makes the disk check need at least n free bytes. Only a dev build
// sets it (config.DevBackupMinFree, PANEL_DEV_BACKUP_MIN_FREE); 0 is the rule
// alone.
func (m *Manager) SetMinFree(n int64) { m.minFree.Store(n) }

// OnChange registers fn, called after every change of the active list, outside
// the Manager's lock.
func (m *Manager) OnChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = append(m.onChange, fn)
}

func (m *Manager) fire() {
	m.mu.Lock()
	fns := append([]func(){}, m.onChange...)
	m.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (m *Manager) tmpDir() string { return filepath.Join(m.dataDir, "tmp") }

// Start deletes the temporary files a previous process left
// ($PANEL_DATA_DIR/tmp/backup-*) and starts the worker. Rows that process left
// queued or running were made interrupted when the store opened; they are not
// resumed.
func (m *Manager) Start(ctx context.Context) {
	m.removeLeftovers()
	m.mu.Lock()
	if m.started || m.closed {
		m.mu.Unlock()
		return
	}
	wctx, cancel := context.WithCancel(ctx)
	m.started, m.cancel = true, cancel
	m.wg.Add(1)
	m.mu.Unlock()
	go m.work(wctx)
}

func (m *Manager) removeLeftovers() {
	left, err := filepath.Glob(filepath.Join(m.tmpDir(), "backup-*"))
	if err != nil {
		return
	}
	for _, p := range left {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.log.Warn("backup: cannot remove a leftover temporary file", "path", p, "err", err)
		}
	}
}

// Close stops the worker: the running backup and the queued ones end
// interrupted. Enqueue and Retry then return ErrClosed. Wait waits for the
// worker to finish.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	cancel, started := m.cancel, m.started
	m.mu.Unlock()
	if started {
		cancel()
		return
	}
	m.drain()
}

// Wait waits until the worker has stopped, or ctx is done.
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

// Active lists the queued and running backups, running first, then the queue in
// order. QueuePosition is 0 for the running one and 1..N for the queued ones.
// The rows are copies.
func (m *Manager) Active() []store.Backup {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Backup, 0, len(m.queue)+1)
	if m.running != nil {
		b := copyBackup(m.running.b)
		b.QueuePosition = 0
		out = append(out, b)
	}
	for i, j := range m.queue {
		b := copyBackup(j.b)
		b.QueuePosition = i + 1
		out = append(out, b)
	}
	return out
}

func copyBackup(b store.Backup) store.Backup {
	b.Steps = append([]store.BackupStep(nil), b.Steps...)
	if b.StartedAt != nil {
		t := *b.StartedAt
		b.StartedAt = &t
	}
	if b.FinishedAt != nil {
		t := *b.FinishedAt
		b.FinishedAt = &t
	}
	return b
}

// waiters is how many Enqueues wait on the database's reservation (tests).
func (m *Manager) waiters(projectID int64, key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.reserved[dbRef{projectID, key}]; ok {
		return r.waiters
	}
	return 0
}

// Enqueue queues a backup of database dbKey of project t for actor and returns
// its id. It runs spec §4's checks in order and answers a *Refusal when one
// fails:
//
//  1. the project is present (not_found);
//  2. dbKey is one of its databases (unknown_database);
//  3. it is in a container and not SQLite (not_supported);
//  4. Drive is connected with a folder (drive_not_connected, drive_expired,
//     folder_missing);
//  5. the database is reserved: an active backup of it is already_running with
//     its id, and an Enqueue already checking it is waited for (honouring ctx)
//     and its outcome returned;
//  6. its service resolves and runs (not_supported, no_db_name, db_not_running);
//  7. the disk has room for the dump (disk_short).
//
// Checks 1-3 are input errors and write nothing. A refusal at 4, 6 or 7 is
// stored as a refused row and a refused backup.run activity. When ctx ends
// during checks 6-7, Enqueue returns ctx.Err() and records nothing.
func (m *Manager) Enqueue(ctx context.Context, t store.Tracked, dbKey, actor string) (int64, error) {
	if m.isClosed() {
		return 0, ErrClosed
	}
	if t.ID <= 0 || t.MissingSince != nil {
		return 0, &Refusal{Code: CodeNotFound, Words: wordsNotFound}
	}
	db, ok := findDatabase(t, dbKey)
	if !ok {
		return 0, &Refusal{Code: CodeUnknownDatabase, Words: wordsUnknownDatabase}
	}
	if r := checkLocation(db); r != nil {
		return 0, r
	}
	row := m.newRow(t, db, actor)
	if r := m.checkDrive(); r != nil {
		return 0, m.refuse(ctx, row, r)
	}

	key := dbRef{t.ID, dbKey}
	res, err := m.reserve(ctx, key, db.Name)
	if res == nil {
		return 0, err
	}
	var queued *job
	var id int64
	defer func() { m.release(key, res, queued, id, err) }()

	p, r := m.plan(ctx, t, db)
	if r == nil {
		r = m.checkDisk(ctx, p)
	}
	if ctx.Err() != nil {
		// The caller left during the slow checks: a cancelled container or size
		// read says nothing about the database, so nothing is recorded.
		err = ctx.Err()
		return 0, err
	}
	row.DBService = p.service
	if r != nil {
		if r.Service == "" {
			r.Service = p.service
		}
		err = m.refuse(ctx, row, r)
		return 0, err
	}
	row.State = StateQueued
	row.Steps = newSteps(stepWaiting)
	sctx, cancel := storeContext(ctx)
	id, err = m.st.InsertBackup(sctx, row)
	cancel()
	if err != nil {
		return 0, err
	}
	row.ID = id
	queued = &job{b: row, t: t, db: db, plan: p}
	return id, nil
}

// Retry queues a new backup of the database of backup id, like Enqueue, for a
// failed, refused or interrupted row (else not_retryable). store.ErrNotFound for
// an unknown id; not_found when the project is no longer tracked;
// unknown_database when it no longer has that database.
func (m *Manager) Retry(ctx context.Context, id int64, actor string) (int64, error) {
	if m.isClosed() {
		return 0, ErrClosed
	}
	b, err := m.st.GetBackup(ctx, id)
	if err != nil {
		return 0, err
	}
	switch b.State {
	case StateFailed, StateRefused, StateInterrupted:
	default:
		return 0, &Refusal{Code: CodeNotRetryable, Words: wordsNotRetryable, ID: b.ID}
	}
	t, err := m.st.TrackedProject(ctx, b.ProjectID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, &Refusal{Code: CodeNotFound, Words: wordsNotFound}
	}
	if err != nil {
		return 0, err
	}
	return m.Enqueue(ctx, t, b.DBKey, actor)
}

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func findDatabase(t store.Tracked, key string) (project.Database, bool) {
	for _, d := range t.Databases {
		if project.DatabaseKey(d) == key {
			return d, true
		}
	}
	return project.Database{}, false
}

// checkLocation is check 3, before any resolution: only a database in a
// container, and not SQLite.
func checkLocation(d project.Database) *Refusal {
	switch {
	case d.Type == "sqlite":
		return &Refusal{Code: CodeNotSupported, Words: wordsSQLite}
	case d.LocationKind != "container":
		return &Refusal{Code: CodeNotSupported, Words: wordsOutside}
	}
	return nil
}

// checkDrive is check 4, from Drive's in-memory status.
func (m *Manager) checkDrive() *Refusal {
	st := m.d.Status()
	switch st.State {
	case drive.StateConnected:
		if st.Folder == nil {
			return &Refusal{Code: CodeFolderMissing, Words: drive.Words(drive.CodeFolderMissing)}
		}
		return nil
	case drive.StateExpired:
		return &Refusal{Code: CodeDriveExpired, Words: drive.Words(drive.CodeTokenExpired)}
	case drive.StateNotConfigured:
		return &Refusal{Code: CodeDriveNotConnected, Words: wordsNotConfigured}
	}
	return &Refusal{Code: CodeDriveNotConnected, Words: wordsNotConnected}
}

// plan is check 6: the database's service resolves (from the containers, else
// the stored services) and has a running container, and the commands that
// back it up. It reads the container list.
func (m *Manager) plan(ctx context.Context, t store.Tracked, db project.Database) (plan, *Refusal) {
	cs, up := m.cs(ctx)
	service, reason := project.BackupReason(db, t.ComposeProject, t.ServiceDetails, cs, up)
	switch reason {
	case project.ReasonNotSupported:
		words := wordsNoService
		if db.Type != "postgresql" && db.Type != "mysql" && db.Type != "mongodb" {
			words = wordsType
		}
		return plan{}, &Refusal{Code: CodeNotSupported, Words: words}
	case project.ReasonNoDBName:
		return plan{service: service}, &Refusal{Code: CodeNoDBName, Words: wordsNoDBName, Service: service}
	case project.ReasonDBNotRunning:
		return plan{service: service}, &Refusal{Code: CodeDBNotRunning, Words: "Start " + service + " first", Service: service}
	}
	values, override, ok := t.ComposeValues()
	if !ok {
		return plan{service: service}, &Refusal{Code: CodeNotSupported, Words: wordsNoCompose, Service: service}
	}
	values["service"] = service
	byDefault := db.Type == "postgresql" && project.NameIsPlaceholder(db, service)
	if !byDefault {
		values["dbname"] = db.Name
	}
	image := serviceImage(t.ServiceDetails, service)
	p := plan{service: service, image: image, values: values, tool: dumpTool(db.Type, image),
		secrets: scan.SecretValues(scan.ReadDotenv(filepath.Join(t.Path, ".env")))}
	p.dump, p.size = actions(db.Type, byDefault, override)
	return p, nil
}

// serviceImage is the image of the named compose service, "" when not known.
func serviceImage(services []project.Service, name string) string {
	for _, s := range services {
		if s.Name == name {
			return s.Image
		}
	}
	return ""
}

// actions are the dump and size actions of a database type.
func actions(dbType string, byDefault, override bool) (dump, size runner.Action) {
	switch {
	case dbType == "postgresql" && byDefault:
		dump, size = runner.DBPgDumpDefault, runner.DBPgSizeDefault
	case dbType == "postgresql":
		dump, size = runner.DBPgDump, runner.DBPgSize
	case dbType == "mysql":
		dump, size = runner.DBMySQLDump, runner.DBMySQLSize
	default:
		dump, size = runner.DBMongoDump, runner.DBMongoSize
	}
	if override {
		dump, size = dump+".override", size+".override"
	}
	return dump, size
}

// checkDisk is check 7: free ≥ size/2 + 100 MiB on the temporary folder. A
// failed size read needs 500 MiB and is only logged; so is a failed free-space
// read, which does not block.
func (m *Manager) checkDisk(ctx context.Context, p plan) *Refusal {
	dir := m.tmpDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m.log.Warn("backup: cannot create the temporary folder", "err", err)
	}
	need := fallbackNeed
	res, err := m.r.Run(ctx, runner.StackCompose, p.size, p.values, p.secrets)
	if err == nil {
		n, perr := strconv.ParseInt(strings.TrimSpace(string(res.Stdout)), 10, 64)
		if perr == nil && n >= 0 {
			need = n/2 + diskMargin
		} else {
			m.log.Warn("backup: the size read printed no size; needing 500 MiB free", "action", p.size)
		}
	} else {
		m.log.Warn("backup: the size read failed; needing 500 MiB free", "action", p.size, "err", runner.Mask(err.Error(), p.secrets))
	}
	if floor := m.minFree.Load(); floor > need {
		need = floor
	}
	free, err := m.freeSpace(dir)
	if err != nil {
		m.log.Warn("backup: cannot read the free space", "err", err)
		return nil
	}
	if free >= need {
		return nil
	}
	return &Refusal{
		Code: CodeDiskShort, NeedBytes: need, FreeBytes: free, Service: p.service,
		Words: "Needs about " + formatBytes(need) + " free, the server has " + formatBytes(free) + " left. Free up some disk space and try again.",
	}
}

// freeSpace is the space an unprivileged user may still write in dir.
func freeSpace(dir string) (int64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return 0, err
	}
	free := uint64(s.Bavail) * uint64(s.Bsize) //nolint:gosec // G115: statfs block counts and sizes are non-negative
	if free > 1<<62 {
		free = 1 << 62
	}
	return int64(free), nil //nolint:gosec // G115: capped above
}

// newRow is the snapshot of a backup's target.
func (m *Manager) newRow(t store.Tracked, db project.Database, actor string) store.Backup {
	return store.Backup{
		ProjectID: t.ID, ProjectName: t.DisplayName,
		DBType: db.Type, DBName: db.Name, DBLocationKind: db.LocationKind, DBLocation: db.Location,
		DBKey: project.DatabaseKey(db), StartedBy: actor, CreatedAt: m.now(),
	}
}

func newSteps(state string) []store.BackupStep {
	steps := make([]store.BackupStep, 0, 4)
	for _, s := range []string{StepDump, StepCompress, StepUpload, StepPrune} {
		steps = append(steps, store.BackupStep{Step: s, State: state})
	}
	return steps
}

func activityOf(b store.Backup) store.Activity {
	return store.Activity{
		Actor: b.StartedBy, ActorKind: store.ActorPerson, Action: store.ActBackupRun,
		ProjectID: b.ProjectID, Target: b.ProjectName + " · " + b.DBName,
	}
}

// storeContext is the context of a store write that must land even when the
// caller's context is cancelled.
func storeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), storeWait)
}

// refuse stores a refused row and its activity, and returns r with the row's
// id. A failed write is logged; the refusal still stands.
func (m *Manager) refuse(ctx context.Context, row store.Backup, r *Refusal) *Refusal {
	row.State, row.Error = StateRefused, r.Words
	if r.Service != "" {
		row.DBService = r.Service
	}
	row.Steps = newSteps(stepSkipped)
	now := m.now()
	row.FinishedAt = &now
	sctx, cancel := storeContext(ctx)
	defer cancel()
	id, err := m.st.InsertBackup(sctx, row)
	if err != nil {
		m.log.Error("backup: store the refusal", "project", row.ProjectID, "code", r.Code, "err", err)
	}
	r.ID = id
	if err := m.st.RecordActivity(sctx, activityOf(row), store.ResultRefused, r.Words); err != nil {
		m.log.Error("backup: record the refusal", "project", row.ProjectID, "err", err)
	}
	return r
}

// reserve is check 5. It returns the reservation to hold, or nil and the
// outcome to answer: already_running for an active backup, or the outcome of
// the Enqueue that held the reservation (ctx.Err() if ctx ends first). A holder
// whose own caller left reached no outcome, so its waiters try again.
func (m *Manager) reserve(ctx context.Context, key dbRef, dbName string) (*reservation, error) {
	for {
		m.mu.Lock()
		if id, ok := m.activeID(key); ok {
			m.mu.Unlock()
			return nil, alreadyRunning(id, dbName)
		}
		r, ok := m.reserved[key]
		if !ok {
			r = &reservation{done: make(chan struct{})}
			m.reserved[key] = r
			m.mu.Unlock()
			return r, nil
		}
		r.waiters++
		m.mu.Unlock()
		select {
		case <-r.done:
			if errors.Is(r.err, context.Canceled) || errors.Is(r.err, context.DeadlineExceeded) {
				continue
			}
			return nil, r.outcome(dbName)
		case <-ctx.Done():
			m.mu.Lock()
			r.waiters--
			m.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

// activeID is the queued or running backup of key; mu is held.
func (m *Manager) activeID(key dbRef) (int64, bool) {
	for _, j := range m.jobs {
		if j.b.ProjectID == key.project && j.b.DBKey == key.key {
			return j.b.ID, true
		}
	}
	return 0, false
}

func alreadyRunning(id int64, dbName string) *Refusal {
	return &Refusal{Code: CodeAlreadyRunning, Words: "A backup of " + dbName + " is already running.", ID: id}
}

// outcome is what a waiter answers once the holder is done.
func (r *reservation) outcome(dbName string) error {
	var ref *Refusal
	switch {
	case errors.As(r.err, &ref):
		cp := *ref
		return &cp
	case r.err != nil:
		return r.err
	}
	return alreadyRunning(r.id, dbName)
}

// release ends a reservation. A queued job joins the active list in the same
// critical section, so a later Enqueue sees it; then the waiters get the
// outcome.
func (m *Manager) release(key dbRef, res *reservation, j *job, id int64, err error) {
	m.mu.Lock()
	closed := m.closed
	if j != nil && !closed {
		m.jobs[j.b.ID] = j
		m.queue = append(m.queue, j)
	}
	delete(m.reserved, key)
	res.id, res.err = id, err
	m.mu.Unlock()
	close(res.done)
	if j == nil {
		return
	}
	if closed {
		// Closed while checking: the row is not left queued.
		m.endUnstarted(j)
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	m.fire()
}

// endUnstarted makes a queued job that will never run interrupted.
func (m *Manager) endUnstarted(j *job) {
	now := m.now()
	m.mu.Lock()
	j.b.State, j.b.Error, j.b.FinishedAt = StateInterrupted, wordsInterrupted, &now
	j.b.Steps = newSteps(stepSkipped)
	b := copyBackup(j.b)
	m.mu.Unlock()
	m.persist(b)
}

// persist writes a backup's progress; a failure is logged.
func (m *Manager) persist(b store.Backup) {
	ctx, cancel := context.WithTimeout(context.Background(), storeWait)
	defer cancel()
	if err := m.st.UpdateBackup(ctx, b); err != nil {
		m.log.Error("backup: store the progress", "backup", b.ID, "err", err)
	}
}

// drain ends every queued job interrupted; the worker calls it when it stops.
func (m *Manager) drain() {
	m.mu.Lock()
	queued := m.queue
	m.queue = nil
	for _, j := range queued {
		delete(m.jobs, j.b.ID)
	}
	m.mu.Unlock()
	if len(queued) == 0 {
		return
	}
	for _, j := range queued {
		m.endUnstarted(j)
	}
	m.fire()
}

// work is the one worker: it runs the queue in order until ctx ends. However
// it ends (Close, or the Start context), the Manager is then closed: later
// Enqueues get ErrClosed instead of queueing jobs nobody runs.
func (m *Manager) work(ctx context.Context) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		m.drain()
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		if j := m.take(); j != nil {
			m.run(ctx, j)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		}
	}
}

// take moves the first queued job to running.
func (m *Manager) take() *job {
	m.mu.Lock()
	if len(m.queue) == 0 {
		m.mu.Unlock()
		return nil
	}
	j := m.queue[0]
	m.queue = m.queue[1:]
	m.running = j
	now := m.now()
	j.b.State, j.b.StartedAt = StateRunning, &now
	j.published = time.Now()
	m.mu.Unlock()
	m.fire()
	return j
}
