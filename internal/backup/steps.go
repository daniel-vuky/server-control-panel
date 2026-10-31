package backup

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// stepWords are the step names in an activity reason: "Dump: <error>".
var stepWords = map[string]string{StepDump: "Dump", StepCompress: "Compress", StepUpload: "Upload", StepPrune: "Prune"}

// msgInternal is the error of a backup ended by a panic; the panic itself goes
// only to the log.
const msgInternal = "The backup stopped on an internal error. Try again."

// run takes one backup through its re-checks and steps. It always ends the
// job, a panic included: the temporary file is removed, the row is final, the
// activity finished and the job off the active list, and the worker goes on.
func (m *Manager) run(ctx context.Context, j *job) {
	ext := ".gz"
	if !j.compress {
		ext = ".raw"
	}
	tmp := filepath.Join(m.tmpDir(), "backup-"+strconv.FormatInt(j.b.ID, 10)+ext)
	defer func() {
		p := recover()
		if p != nil {
			// The panic value may quote anything; it is masked like stderr.
			m.log.Error("backup: the worker panicked", "backup", j.b.ID,
				"panic", runner.Mask(fmt.Sprint(p), j.plan.secrets), "stack", string(debug.Stack()))
		}
		if j.tmpMade {
			if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
				m.log.Warn("backup: cannot remove the temporary file", "backup", j.b.ID, "err", err)
			}
		}
		if p != nil {
			m.failInternal(j)
		}
		m.finish(j)
	}()

	// Things change while a backup waits: the project as it is now (checks 1-3),
	// then checks 5, 7 and 8 again.
	r := m.recheckProject(ctx, j)
	if r == nil {
		r = m.checkDrive()
	}
	if r == nil {
		var p plan
		p, r = m.plan(ctx, j.t, j.db)
		if r == nil {
			r = m.checkDisk(ctx, p, j.compress)
		}
		if r == nil {
			j.plan = p
		}
	}
	if ctx.Err() != nil {
		m.interrupt(j, -1)
		return
	}
	if r != nil {
		m.refuseQueued(j, r)
		return
	}

	actx, cancel := storeContext(ctx)
	id, err := m.st.StartActivity(actx, activityOf(j.b))
	cancel()
	if err != nil {
		m.log.Error("backup: record the start", "backup", j.b.ID, "err", err)
	}
	j.activityID = id
	m.update(j, true, func(b *store.Backup) {
		b.DBService = j.plan.service
		b.FileName = FileName(b.ProjectName, b.DBName, b.DBType, j.compress, *b.StartedAt)
	})

	if m.dump(ctx, j, tmp) && m.upload(ctx, j, tmp) {
		// The new copy is on Drive: the backup is ok whatever Prune meets.
		m.prune(ctx, j)
	}
}

// recheckProject re-reads a queued backup's project from the store: one
// untracked, removed or missing meanwhile is not_found, a database no longer
// detected unknown_database (and its location is checked again). Otherwise the
// job goes on with the project and database as they are now. A failed read
// keeps the project of the Enqueue; the checks that follow still run.
func (m *Manager) recheckProject(ctx context.Context, j *job) *Refusal {
	t, err := m.st.TrackedProject(ctx, j.t.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return &Refusal{Code: CodeNotFound, Words: wordsNotFound}
	case err != nil:
		if ctx.Err() == nil {
			m.log.Warn("backup: cannot re-read the project; using it as queued", "backup", j.b.ID, "err", err)
		}
		return nil
	case t.MissingSince != nil:
		return &Refusal{Code: CodeNotFound, Words: wordsNotFound}
	}
	db, ok := findDatabase(t, j.b.DBKey)
	if !ok {
		return &Refusal{Code: CodeUnknownDatabase, Words: wordsUnknownDatabase}
	}
	if r := checkLocation(db); r != nil {
		return r
	}
	j.t, j.db = t, db
	return nil
}

// failInternal ends a job a panic stopped: failed at its running step (else
// the first one not done) with msgInternal. A job already final keeps its state.
func (m *Manager) failInternal(j *job) {
	m.update(j, false, func(b *store.Backup) {
		if b.State != StateRunning {
			return
		}
		i := -1
		for k, st := range b.Steps {
			if st.State == stepRunning {
				i = k
				break
			}
		}
		for k := 0; i < 0 && k < len(b.Steps); k++ {
			if b.Steps[k].State != stepDone {
				i = k
			}
		}
		b.State, b.Error, b.Hint = StateFailed, msgInternal, ""
		if i >= 0 {
			b.FailedStep = b.Steps[i].Step
			b.Steps[i].State = stepFailed
		}
		skipAfter(b, i)
	})
}

// refuseQueued ends a job refused by its re-check: one finished refused
// activity row, and the row refused with the refusal's words.
func (m *Manager) refuseQueued(j *job, r *Refusal) {
	m.update(j, false, func(b *store.Backup) {
		b.State, b.Error = StateRefused, r.Words
		b.Steps = newSteps(stepSkipped)
	})
	ctx, cancel := context.WithTimeout(context.Background(), storeWait)
	if err := m.st.RecordActivity(ctx, activityOf(j.b), store.ResultRefused, r.Words); err != nil {
		m.log.Error("backup: record the refusal", "backup", j.b.ID, "err", err)
	}
	cancel()
}

// update changes the job's row under the lock, then stores it when persist is
// set, and publishes.
func (m *Manager) update(j *job, persist bool, fn func(b *store.Backup)) {
	b := func() store.Backup {
		m.mu.Lock()
		defer m.mu.Unlock() // fn may panic; the worker recovers, so mu must not stay held
		fn(&j.b)
		j.published = time.Now()
		return copyBackup(j.b)
	}()
	if persist {
		m.persist(b)
	}
	m.fire()
}

// setStep sets a step's state and detail: a state change, stored and
// published at once.
func (m *Manager) setStep(j *job, i int, state, detail string) {
	m.update(j, true, func(b *store.Backup) {
		b.Steps[i].State, b.Steps[i].Detail = state, detail
	})
}

// progress sets a running step's detail. It is published only when the job's
// last publish is publishEvery old, and never stored.
func (m *Manager) progress(j *job, i int, detail string) {
	now := time.Now()
	due := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		j.b.Steps[i].Detail = detail
		due := now.Sub(j.published) >= m.publishEvery
		if due {
			j.published = now
		}
		return due
	}()
	if due {
		m.fire()
	}
}

// fail ends the job failed at step i with msg and hint; the steps after it are
// skipped.
func (m *Manager) fail(j *job, i int, msg, hint string) {
	m.update(j, false, func(b *store.Backup) {
		b.State, b.FailedStep, b.Error, b.Hint = StateFailed, b.Steps[i].Step, msg, hint
		b.Steps[i].State = stepFailed
		skipAfter(b, i)
	})
}

// interrupt ends the job interrupted: the running step i (-1 for none) failed,
// the rest skipped.
func (m *Manager) interrupt(j *job, i int) {
	m.update(j, false, func(b *store.Backup) {
		b.State, b.Error = StateInterrupted, wordsInterrupted
		if i >= 0 {
			b.Steps[i].State = stepFailed
		}
		skipAfter(b, i)
	})
}

func skipAfter(b *store.Backup, i int) {
	for k := i + 1; k < len(b.Steps); k++ {
		if b.Steps[k].State == stepWaiting || b.Steps[k].State == stepRunning {
			b.Steps[k].State, b.Steps[k].Detail = stepSkipped, ""
		}
	}
}

// finish ends the job: the end time and the duration, the row stored, the
// activity finished and the job off the active list. It keeps the Prune step
// the worker set (done or failed by Prune, or a panic's failed) and only fills
// in a default: skipped for an ok backup whose step is still waiting, and
// "temp file deleted" for a backup that ended before Prune once the file was
// made.
//
// The active row turns ok only once the store has it: a client that sees ok in
// the live state and downloads at once reads a stored ok row.
func (m *Manager) finish(j *job) {
	now := m.now()
	m.mu.Lock()
	final := copyBackup(j.b)
	m.mu.Unlock()
	if final.State == StateRunning {
		final.State = StateOK
	}
	prune := &final.Steps[iPrune]
	switch {
	case prune.State == stepWaiting && final.State == StateOK:
		prune.State, prune.Detail = stepSkipped, ""
	case final.State != StateOK && j.tmpMade && (prune.State == stepWaiting || prune.State == stepSkipped):
		prune.State, prune.Detail = stepSkipped, wordsTempDeleted
	}
	final.FinishedAt = &now
	if final.StartedAt != nil {
		final.DurationMS = now.Sub(*final.StartedAt).Milliseconds()
	}

	m.persist(final)
	m.mu.Lock()
	j.b = copyBackup(final)
	m.mu.Unlock()
	result, reason := store.ResultOK, ""
	switch final.State {
	case StateFailed:
		result, reason = store.ResultFailed, final.Error
		if w := stepWords[final.FailedStep]; w != "" {
			reason = w + ": " + final.Error
		}
	case StateInterrupted:
		result, reason = store.ResultInterrupted, final.Error
	}
	switch {
	case j.activityID != 0:
		ctx, cancel := context.WithTimeout(context.Background(), storeWait)
		if err := m.st.FinishActivity(ctx, j.activityID, result, reason); err != nil {
			m.log.Error("backup: record the end", "backup", final.ID, "err", err)
		}
		cancel()
	case final.State == StateFailed || final.State == StateInterrupted:
		// Ended before the activity started (a panic in the re-check, or the
		// worker stopped during it): one finished record. A refusal recorded its
		// own in refuseQueued.
		m.recordUnstarted(final, result, reason)
	}
	m.log.Info("backup finished", "backup", final.ID, "project", final.ProjectID, "state", final.State,
		"step", final.FailedStep, "duration", time.Duration(final.DurationMS)*time.Millisecond)

	m.mu.Lock()
	delete(m.jobs, j.b.ID)
	if m.running == j {
		m.running = nil
	}
	m.mu.Unlock()
	// Off the active list, so no batch can join it any more.
	m.ended(j)
	m.fire()
}

// counter counts the bytes written through it. n is read by the progress
// ticker while exec's copy goroutine writes.
type counter struct {
	w io.Writer
	n atomic.Int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// dump runs the Dump and Compress steps: RunTo → raw counter → gzip (level 1) →
// compressed counter → the temporary file (mode 0600). With compression off
// the raw counter writes the file itself and Compress is skipped. It reports
// whether both succeeded; on failure the job is already ended failed or
// interrupted.
func (m *Manager) dump(ctx context.Context, j *job, tmp string) bool {
	p := j.plan
	m.setStep(j, iDump, stepRunning, p.tool)
	if err := os.MkdirAll(m.tmpDir(), 0o700); err != nil {
		m.fail(j, iDump, "Could not create the temporary folder: "+pathReason(err), "")
		return false
	}
	//nolint:gosec // G304: the path is $PANEL_DATA_DIR/tmp/backup-<id>.gz (or .raw), built from the row id
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		m.fail(j, iDump, "Could not create the temporary file: "+pathReason(err), "")
		return false
	}
	j.tmpMade = true
	defer func() { _ = f.Close() }() // a second Close after the checked one is harmless
	compressed := &counter{w: f}
	var gz io.WriteCloser
	raw := &counter{w: f}
	if j.compress {
		if gz, err = m.compressor(compressed); err != nil { // only for a bad level
			_ = f.Close()
			m.fail(j, iDump, err.Error(), "")
			return false
		}
		raw = &counter{w: gz}
	}

	start := m.now()
	stop := m.ticker(func() {
		m.progress(j, iDump, p.tool+" · "+formatDuration(m.now().Sub(start))+" · "+formatBytes(raw.n.Load())+" read")
	})
	defer stop() // stops the ticker even when RunTo panics
	// A dump that did not exit by itself may still run inside the container
	// (ADR 0011); a panic in RunTo leaves it so too.
	inRunTo := true
	defer func() {
		if inRunTo {
			m.stopDump(ctx, j)
		}
	}()
	stderr, err := m.r.RunTo(ctx, runner.StackCompose, p.dump, p.values, p.secrets, raw)
	inRunTo = false
	stop()
	elapsed := m.now().Sub(start)
	stopped := false
	if !exitedByItself(err) {
		stopped = m.stopDump(ctx, j)
	}
	// The dump has exited; the compressor's last flush and the file's close
	// belong to Compress.
	var cerr error
	if err == nil {
		if gz != nil {
			cerr = gz.Close()
		}
		if ferr := f.Close(); cerr == nil {
			cerr = ferr
		}
	}

	if ctx.Err() != nil {
		m.interrupt(j, iDump)
		return false
	}
	if err != nil {
		msg, hint := m.dumpError(j, err, stderr, stopped)
		m.fail(j, iDump, msg, hint)
		return false
	}
	dumpDetail := p.tool + " · " + formatDuration(elapsed)
	if cerr != nil && !j.compress {
		m.fail(j, iDump, "Could not write the temporary file: "+pathReason(cerr), "")
		return false
	}
	if cerr != nil {
		m.update(j, false, func(b *store.Backup) {
			b.Steps[iDump].State, b.Steps[iDump].Detail = stepDone, dumpDetail
		})
		m.fail(j, iCompress, "Could not compress the dump: "+pathReason(cerr), "")
		return false
	}
	rawN, compN := raw.n.Load(), compressed.n.Load()
	compState, compDetail := stepDone, formatCompress(rawN, compN)
	if !j.compress {
		compN, compState, compDetail = rawN, stepSkipped, wordsCompressionOff
	}
	m.update(j, true, func(b *store.Backup) {
		b.RawBytes, b.SizeBytes = rawN, compN
		b.Steps[iDump].State, b.Steps[iDump].Detail = stepDone, dumpDetail
		b.Steps[iCompress].State, b.Steps[iCompress].Detail = compState, compDetail
	})
	return true
}

// newGzip is the compressor: gzip at level 1.
func newGzip(w io.Writer) (io.WriteCloser, error) { return gzip.NewWriterLevel(w, gzip.BestSpeed) }

// exitedByItself reports whether RunTo's error means the dump ended inside the
// container: it exited, with any status. A stall, the ceiling, a failed write,
// a cancel or a docker client that did not run kill only the client.
func exitedByItself(err error) bool {
	var exit *runner.ExitError
	return err == nil || errors.As(err, &exit)
}

// stopCall is the stop action for the plan's dump: db.dump.stop, or its
// override variant, with the dump's compose values but not its database name.
func stopCall(p plan) (runner.Action, runner.Values) {
	v := runner.Values{}
	for k, val := range p.values {
		if k != "dbname" {
			v[k] = val
		}
	}
	if _, ok := v["override"]; ok {
		return runner.DBDumpStopOverride, v
	}
	return runner.DBDumpStop, v
}

// stopDump stops the job's dump inside its container, for a dump that ended
// without exiting by itself: killing the docker client leaves it running there
// (ADR 0011). It is best effort, on a context the job's cancellation does not
// end, bounded by stopWait, so Close waits for it at most that long. It
// reports whether the stop ran; a failure is logged.
func (m *Manager) stopDump(ctx context.Context, j *job) bool {
	p := j.plan
	a, v := stopCall(p)
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.stopWait)
	defer cancel()
	if _, err := m.r.Run(sctx, runner.StackCompose, a, v, p.secrets); err != nil {
		m.log.Warn("backup: could not stop the dump inside its container", "backup", j.b.ID,
			"service", p.service, "err", runner.Mask(err.Error(), p.secrets))
		return false
	}
	return true
}

// dumpError words a failed dump. The exit code is the verdict: stderr is read
// only here, after a failure (a mysqldump that exits 0 may still print an
// Error line). Stderr is masked again with the project's secrets. stopped says
// that a dump which did not exit by itself was stopped inside its container.
func (m *Manager) dumpError(j *job, err error, stderr []byte, stopped bool) (msg, hint string) {
	p := j.plan
	var (
		stall   *runner.StallError
		timeout *runner.TimeoutError
		exit    *runner.ExitError
		pathErr *fs.PathError
	)
	switch {
	case errors.As(err, &stall):
		if stopped {
			return "The dump stopped sending data for " + formatSpan(stall.After) + " and was stopped.", ""
		}
		return "The dump stopped sending data for " + formatSpan(stall.After) + ". It may still be running inside " + p.service + ".", ""
	case errors.As(err, &timeout):
		if stopped {
			return "The dump ran for " + formatSpan(timeout.After) + " and was stopped.", ""
		}
		return "The dump ran for " + formatSpan(timeout.After) + " and was stopped. It may still be running inside " + p.service + ".", ""
	case errors.As(err, &pathErr):
		return "Could not write the temporary file: " + pathErr.Err.Error(), ""
	case errors.As(err, &exit):
		masked := runner.Mask(string(stderr), p.secrets)
		if masked == "" {
			masked = runner.Mask(exit.Stderr, p.secrets)
		}
		msg = ErrorLine(masked)
		if msg == "" {
			msg = p.tool + " exited with status " + strconv.Itoa(exit.Code) + "."
		}
		return msg, Hint(j.db.Type, p.image, j.db.Name, p.service, masked, j.db.Sources)
	}
	return cut(runner.Mask(err.Error(), p.secrets), maxErrorLine), ""
}

// pathReason is the reason of a file error without its path.
func pathReason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// ticker calls fn every publishEvery until the returned stop is called; stop
// waits for a running fn and may be called more than once.
func (m *Manager) ticker(fn func()) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(m.publishEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				fn()
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		wg.Wait()
	}
}

// upload runs the Upload step: find or create <folder>/<project slug>/ and
// upload the temporary file there, with progress. It reports whether the file
// is on Drive; on failure the job is ended failed (with the Drive words) or
// interrupted.
func (m *Manager) upload(ctx context.Context, j *job, tmp string) bool {
	size := j.b.SizeBytes
	m.setStep(j, iUpload, stepRunning, formatProgress(0, size))
	start := m.now()
	fileID, link, err := m.send(ctx, j, tmp, size)
	if err != nil {
		if ctx.Err() != nil {
			m.interrupt(j, iUpload)
			return false
		}
		m.fail(j, iUpload, m.driveWords(j, "upload", err), "")
		return false
	}
	elapsed := m.now().Sub(start)
	m.update(j, true, func(b *store.Backup) {
		// The link is rendered as an href: only a Drive link (or the fake's
		// panel origin) is kept.
		b.DriveFileID, b.DriveLink = fileID, m.d.SafeLink(link)
		b.Steps[iUpload].State, b.Steps[iUpload].Detail = stepDone, formatBytes(size)+" · "+formatDuration(elapsed)
	})
	return true
}

// localError is a failure on the panel's side of the upload, worded as it is.
type localError struct{ msg string }

func (e *localError) Error() string { return e.msg }

// dumpReader reads the temporary file for the upload. A failure to read it,
// a file shorter than the size the upload was told included, is the panel's
// own: a *localError, so it is never worded or logged as a Drive outage. The
// reason is masked like stderr.
type dumpReader struct {
	r       io.Reader
	left    int64 // bytes still expected
	secrets []string
}

func (d *dumpReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	d.left -= int64(n)
	if err == nil || (errors.Is(err, io.EOF) && d.left <= 0) {
		return n, err
	}
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return n, &localError{"Could not read the dump file: " + runner.Mask(pathReason(err), d.secrets)}
}

func (m *Manager) send(ctx context.Context, j *job, tmp string, size int64) (string, string, error) {
	sess, folder, err := m.d.Session(ctx)
	if err != nil {
		return "", "", err
	}
	sub, err := sess.FindOrCreateFolder(ctx, ProjectFolder(j.b.ProjectName), folder.ID)
	if err != nil {
		return "", "", err
	}
	//nolint:gosec // G304: the path is $PANEL_DATA_DIR/tmp/backup-<id>.gz (or .raw), built from the row id
	f, err := os.Open(tmp)
	if err != nil {
		return "", "", &localError{"Could not read the temporary file: " + pathReason(err)}
	}
	defer func() { _ = f.Close() }()
	r := &dumpReader{r: f, left: size, secrets: j.plan.secrets}
	return sess.Upload(ctx, sub.ID, j.b.FileName, size, r, func(sent int64) {
		m.progress(j, iUpload, formatProgress(sent, size))
	})
}

// driveWords words a failed Drive call (op names it in the log): the Drive
// code's words, never a raw error from Google.
func (m *Manager) driveWords(j *job, op string, err error) string {
	var local *localError
	switch {
	case errors.As(err, &local):
		return local.msg
	case errors.Is(err, drive.ErrNotConfigured):
		return wordsNotConfigured
	case errors.Is(err, drive.ErrNotConnected):
		return "Google Drive is not connected."
	}
	if w := drive.Words(drive.CodeOf(err)); w != "" {
		return w
	}
	m.log.Warn("backup: "+op+" failed", "backup", j.b.ID, "err", err)
	return drive.Words(drive.CodeUnreachable)
}
