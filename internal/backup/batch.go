package backup

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

const (
	wordsBatchRunning = "Back up all is already running."
	// containersWait bounds Back up all's one container read for its snapshot.
	containersWait = 15 * time.Second
)

// Batch is one Back up all (spec decisions 17-20), the latest one only, in
// memory. Total is the databases queued for it (the supported ones of the
// tracked, present projects); Done counts those that ended, as OK, Failed
// (failed, refused, interrupted, or not queued at all) or Skipped (the project
// or database was gone at enqueue). Skipped also counts the not supported
// databases, which are not in Total. FinishedAt is set once every one has
// ended.
type Batch struct {
	ID         int64
	StartedBy  string
	StartedAt  time.Time
	Total      int
	Done       int
	OK         int
	Failed     int
	Skipped    int
	FinishedAt *time.Time
}

// batchState is the Manager's batch; every field is guarded by Manager.mu.
type batchState struct {
	b          Batch
	pending    int  // registered members (queued, joined or stored refusals) not ended yet
	enqueuing  bool // the enqueue goroutine still runs
	activityID int64
	cancel     context.CancelFunc // ends the enqueue goroutine's context
}

// slot is one batch Enqueue: the batch it belongs to and whether its outcome
// was registered with the batch (then the batch counts it when it ends).
// Only the enqueue goroutine's own Enqueue call uses it.
type slot struct {
	batch   int64
	counted bool
}

// target is one database Back up all queues.
type target struct {
	t   store.Tracked
	key string
}

// CurrentBatch is a copy of the latest batch, nil before the first.
func (m *Manager) CurrentBatch() *Batch {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batch == nil {
		return nil
	}
	b := m.batch.b
	if b.FinishedAt != nil {
		t := *b.FinishedAt
		b.FinishedAt = &t
	}
	return &b
}

// StartAll starts a Back up all of projects (the tracked ones) by by. Before
// anything is queued it refuses with a *Refusal: Drive not connected (or not
// set up), expired or without a folder, or batch_running while the previous
// batch has not finished; ErrClosed after Close. It reads the containers once
// and takes as candidates every database of the present projects that is not
// not_supported (as the scheduler does); the others are counted skipped. It
// records a running backup.all activity and returns the new batch at once; a
// goroutine then queues the candidates one at a time through Enqueue, each
// with its own checks, re-reading its project first. The batch ends when that
// goroutine is done and every backup it queued or joined has ended; the
// activity is then finished with "N succeeded, M failed" (", K skipped" when
// K > 0).
func (m *Manager) StartAll(ctx context.Context, by Actor, projects []store.Tracked) (Batch, error) {
	if m.isClosed() {
		return Batch{}, ErrClosed
	}
	if r := m.checkDrive(); r != nil {
		m.recordAllRefused(ctx, by, r.Words)
		return Batch{}, r
	}
	cctx, cancel := context.WithTimeout(ctx, containersWait)
	cs, up := m.cs(cctx)
	cancel()
	cands, skipped := candidates(projects, cs, up)

	bctx, stop := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		stop()
		return Batch{}, ErrClosed
	}
	if m.batch != nil && m.batch.b.FinishedAt == nil {
		m.mu.Unlock()
		stop()
		r := &Refusal{Code: CodeBatchRunning, Words: wordsBatchRunning}
		m.recordAllRefused(ctx, by, r.Words)
		return Batch{}, r
	}
	m.batchSeq++
	bs := &batchState{
		b:         Batch{ID: m.batchSeq, StartedBy: by.Name, StartedAt: m.now(), Total: len(cands), Skipped: skipped},
		enqueuing: true, cancel: stop,
	}
	m.batch = bs
	m.wg.Add(1)
	out := bs.b
	m.mu.Unlock()

	sctx, scancel := storeContext(ctx)
	aid, err := m.st.StartActivity(sctx, allActivity(by, fmt.Sprintf("All databases: %d queued, %d skipped", len(cands), skipped)))
	scancel()
	if err != nil {
		m.log.Error("backup: record back up all", "err", err)
	}
	m.mu.Lock()
	bs.activityID = aid // before the goroutine starts: the batch cannot end earlier
	m.mu.Unlock()
	m.log.Info("backup: back up all started", "batch", out.ID, "total", out.Total, "skipped", out.Skipped)
	m.fire()
	go m.runBatch(bctx, bs, by, cands)
	return out, nil
}

// candidates are the supported databases of the present projects, each once,
// in order, and the number of not supported ones.
func candidates(projects []store.Tracked, cs []project.Container, up bool) ([]target, int) {
	var out []target
	skipped := 0
	seen := map[dbRef]bool{}
	for _, t := range projects {
		if t.ID <= 0 || t.MissingSince != nil {
			continue
		}
		for _, d := range t.Databases {
			key := dbRef{t.ID, project.DatabaseKey(d)}
			if seen[key] {
				continue
			}
			seen[key] = true
			if !supportedWith(t, d, cs, up) {
				skipped++
				continue
			}
			out = append(out, target{t: t, key: key.key})
		}
	}
	return out, skipped
}

func allActivity(by Actor, target string) store.Activity {
	return store.Activity{Actor: by.Name, ActorKind: by.Kind, Action: store.ActBackupAll, Target: target}
}

func (m *Manager) recordAllRefused(ctx context.Context, by Actor, words string) {
	sctx, cancel := storeContext(ctx)
	defer cancel()
	if err := m.st.RecordActivity(sctx, allActivity(by, "All databases"), store.ResultRefused, words); err != nil {
		m.log.Error("backup: record the back up all refusal", "err", err)
	}
}

// runBatch queues the candidates one at a time. Once the Manager is closed the
// candidates left count as failed, with no row. After a panic the candidate in
// hand counts as left only when nothing counted it yet (its slot).
func (m *Manager) runBatch(ctx context.Context, bs *batchState, by Actor, cands []target) {
	defer m.wg.Done()
	left := len(cands)
	var cur *slot
	defer func() {
		if p := recover(); p != nil {
			m.log.Error("backup: back up all panicked", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
		}
		bs.cancel()
		m.mu.Lock()
		if cur != nil && cur.counted {
			left--
		}
		bs.b.Done += left
		bs.b.Failed += left
		bs.enqueuing = false
		fin := m.endIfDone(bs)
		m.mu.Unlock()
		m.finishBatch(fin)
		m.fire()
	}()
	for _, c := range cands {
		if ctx.Err() != nil || m.isClosed() {
			return
		}
		cur = &slot{batch: bs.b.ID}
		m.enqueueOne(ctx, bs, by, c, cur)
		cur = nil
		left--
	}
}

// enqueueOne queues one candidate with the project as it is now. An outcome
// the batch did not register (the backup it queued or joined, a refusal it
// stored) is counted here: skipped when the project or database is gone or
// no longer supported, failed otherwise (ErrClosed, a cancelled or failed
// Enqueue). s is the candidate's slot, marked counted once anything counts it.
func (m *Manager) enqueueOne(ctx context.Context, bs *batchState, by Actor, c target, s *slot) {
	ectx, cancel := context.WithTimeout(ctx, enqueueWait)
	defer cancel()
	t := c.t
	fresh, err := m.st.TrackedProject(ectx, t.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		m.countDirect(bs, s, true)
		return
	case err != nil:
		if ectx.Err() == nil {
			m.log.Warn("backup: back up all cannot re-read a project; using the snapshot", "project", t.ID, "err", err)
		}
	default:
		t = fresh
	}
	_, err = m.enqueue(ectx, t, c.key, by, s)
	if m.enqueuedHook != nil {
		m.enqueuedHook(c.key)
	}
	if s.counted {
		return
	}
	var r *Refusal
	skip := errors.As(err, &r) && r.ID == 0 &&
		(r.Code == CodeNotFound || r.Code == CodeUnknownDatabase || r.Code == CodeNotSupported)
	if err != nil && !skip && !errors.Is(err, ErrClosed) && ectx.Err() == nil {
		m.log.Warn("backup: back up all could not queue a backup", "project", t.ID, "db", c.key, "err", err)
	}
	m.countDirect(bs, s, skip)
}

// countDirect counts an outcome the enqueue goroutine decides for slot s:
// skipped or failed. The batch cannot end here, since its goroutine still
// runs.
func (m *Manager) countDirect(bs *batchState, s *slot, skipped bool) {
	m.mu.Lock()
	s.counted = true
	bs.b.Done++
	if skipped {
		bs.b.Skipped++
	} else {
		bs.b.Failed++
	}
	m.mu.Unlock()
	m.fire()
}

// register makes j a member of slot s's batch (mu is held): the batch counts
// it when ended(j) runs. Nothing happens without a slot, for a slot already
// registered, a job already in a batch, or a batch that is no longer the
// running one.
func (m *Manager) register(j *job, s *slot) {
	if s == nil || s.counted || j.batch != 0 {
		return
	}
	bs := m.batch
	if bs == nil || bs.b.ID != s.batch || bs.b.FinishedAt != nil {
		return
	}
	j.batch = s.batch
	bs.pending++
	s.counted = true
}

// countNow counts, for slot s's batch, an outcome that has already ended in
// state: a refusal stored at enqueue, or a joined backup that ended before the
// join was attached.
func (m *Manager) countNow(state string, s *slot) {
	if s == nil {
		return
	}
	j := &job{b: store.Backup{State: state}}
	m.mu.Lock()
	m.register(j, s)
	m.mu.Unlock()
	m.ended(j)
}

// joinOutcome makes a batch Enqueue that waited on another Enqueue of the same
// database (s not nil) count that one's outcome: the backup it queued is
// joined, or counted from its stored row when it has already ended; a refusal
// it stored counts as failed. Any other error is left to the enqueue
// goroutine.
func (m *Manager) joinOutcome(ctx context.Context, out error, s *slot) {
	var r *Refusal
	if s == nil || !errors.As(out, &r) || r.ID == 0 {
		return
	}
	if r.Code != CodeAlreadyRunning {
		m.countNow(StateRefused, s)
		return
	}
	if m.joinHook != nil {
		m.joinHook(r.ID)
	}
	m.mu.Lock()
	if j, ok := m.jobs[r.ID]; ok {
		m.register(j, s)
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	sctx, cancel := storeContext(ctx)
	b, err := m.st.GetBackup(sctx, r.ID)
	cancel()
	if err != nil {
		m.log.Warn("backup: back up all cannot read a joined backup", "backup", r.ID, "err", err)
		return
	}
	// Not on the active list: final, or about to be interrupted (queued while
	// the Manager closed), which counts as failed too.
	m.countNow(b.State, s)
}

// ended is the one hook every end of a backup goes through (finish,
// endUnstarted, a refusal stored at enqueue): when the backup is a member of
// the running batch, the batch counts it and, when it was the last, ends. It
// is called once the backup is off the active list, so no batch joins it
// afterwards.
func (m *Manager) ended(j *job) {
	m.mu.Lock()
	id, state := j.batch, j.b.State
	j.batch = 0
	bs := m.batch
	if id == 0 || bs == nil || bs.b.ID != id {
		m.mu.Unlock()
		return
	}
	bs.pending--
	bs.b.Done++
	if state == StateOK {
		bs.b.OK++
	} else {
		bs.b.Failed++
	}
	fin := m.endIfDone(bs)
	m.mu.Unlock()
	m.finishBatch(fin)
	m.fire()
}

// batchEnd is what finishBatch records, taken under mu.
type batchEnd struct {
	b          Batch
	activityID int64
}

// endIfDone ends bs when its goroutine is done and no member is unfinished;
// mu is held. It returns the end to record, nil when the batch goes on (or
// had ended).
func (m *Manager) endIfDone(bs *batchState) *batchEnd {
	if bs.enqueuing || bs.pending > 0 || bs.b.FinishedAt != nil {
		return nil
	}
	now := m.now()
	bs.b.FinishedAt = &now
	return &batchEnd{b: bs.b, activityID: bs.activityID}
}

// finishBatch finishes the backup.all activity of an ended batch.
func (m *Manager) finishBatch(fin *batchEnd) {
	if fin == nil {
		return
	}
	b := fin.b
	reason := fmt.Sprintf("%d succeeded, %d failed", b.OK, b.Failed)
	if b.Skipped > 0 {
		reason += fmt.Sprintf(", %d skipped", b.Skipped)
	}
	result := store.ResultOK
	if b.Failed > 0 {
		result = store.ResultFailed
	}
	if fin.activityID != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), storeWait)
		if err := m.st.FinishActivity(ctx, fin.activityID, result, reason); err != nil {
			m.log.Error("backup: record the end of back up all", "batch", b.ID, "err", err)
		}
		cancel()
	}
	m.log.Info("backup: back up all finished", "batch", b.ID, "total", b.Total,
		"ok", b.OK, "failed", b.Failed, "skipped", b.Skipped)
}
