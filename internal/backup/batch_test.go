package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// shopWith is the test project with only the databases of keys, in that order.
func (e *env) shopWith(keys ...string) store.Tracked {
	t := e.shop
	t.Databases = nil
	for _, k := range keys {
		for _, d := range e.shop.Databases {
			if project.DatabaseKey(d) == k {
				t.Databases = append(t.Databases, d)
			}
		}
	}
	return t
}

// startAll starts a batch over ps and fails the test on an error.
func (e *env) startAll(t *testing.T, by Actor, ps ...store.Tracked) Batch {
	t.Helper()
	b, err := e.m.StartAll(context.Background(), by, ps)
	if err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	return b
}

// waitBatch waits until the current batch has finished and its backup.all
// activity is no longer running, and returns both.
func (e *env) waitBatch(t *testing.T) (Batch, store.ActivityRow) {
	t.Helper()
	var b *Batch
	var row store.ActivityRow
	waitFor(t, "the batch to finish", func() bool {
		b = e.m.CurrentBatch()
		if b == nil || b.FinishedAt == nil {
			return false
		}
		rows := e.allActivity(t)
		if len(rows) == 0 || rows[0].Result == store.ResultRunning {
			return false
		}
		row = rows[0]
		return true
	})
	return *b, row
}

// allActivity is the backup.all activity, newest first.
func (e *env) allActivity(t *testing.T) []store.ActivityRow {
	t.Helper()
	rows, err := e.st.RecentActivity(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.ActivityRow
	for _, r := range rows {
		if r.Action == store.ActBackupAll {
			out = append(out, r)
		}
	}
	return out
}

func (e *env) shopBackups(t *testing.T) []store.Backup {
	t.Helper()
	bs, _, err := e.st.ProjectBackups(context.Background(), e.shop.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return bs
}

func countsOf(b Batch) string {
	return fmt.Sprintf("total %d done %d ok %d failed %d skipped %d", b.Total, b.Done, b.OK, b.Failed, b.Skipped)
}

// gateDump makes every dump wait until the returned release is called (or its
// context ends).
func gateDump(e *env) (release func()) {
	gate := make(chan struct{})
	e.r.dump = func(ctx context.Context, c call, w io.Writer) ([]byte, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, fmt.Errorf("docker dump: %w", ctx.Err())
		}
		_, err := w.Write(defaultDump(c))
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

// Back up all queues every supported database of the projects and counts the
// others (SQLite, a host database) as skipped, outside the total.
func TestStartAllQueuesSupportedSkipsOthers(t *testing.T) {
	e := newEnv(t)
	if e.m.CurrentBatch() != nil {
		t.Fatal("a batch before the first StartAll")
	}
	before := e.changes.Load()
	got := e.startAll(t, Person(actor), e.shopWith(keyPG, keySQLite, keyMongo, keyHost))
	if got.ID != 1 || got.StartedBy != actor || got.StartedAt.IsZero() || got.Total != 2 || got.Skipped != 2 || got.FinishedAt != nil {
		t.Fatalf("StartAll = %+v", got)
	}
	if e.changes.Load() == before {
		t.Error("StartAll published nothing")
	}
	b, act := e.waitBatch(t)
	if countsOf(b) != "total 2 done 2 ok 2 failed 0 skipped 2" {
		t.Errorf("batch = %s", countsOf(b))
	}
	rows := e.shopBackups(t)
	keys := map[string]bool{}
	for _, r := range rows {
		keys[r.DBKey] = true
		if r.StartedBy != actor || r.State != StateOK {
			t.Errorf("row = %+v", r)
		}
	}
	if len(rows) != 2 || !keys[keyPG] || !keys[keyMongo] {
		t.Errorf("rows = %+v", rows)
	}
	if act.Actor != actor || act.ActorKind != store.ActorPerson || act.Result != store.ResultOK ||
		act.Reason != "2 succeeded, 0 failed, 2 skipped" || act.FinishedAt == nil {
		t.Errorf("backup.all activity = %+v", act)
	}
	if runs := e.activity(t); len(runs) != 2 || runs[0].Actor != actor {
		t.Errorf("backup.run activity = %+v", runs)
	}

	// CurrentBatch is a copy.
	cp := e.m.CurrentBatch()
	cp.Done, *cp.FinishedAt = 99, time.Time{}
	if again := e.m.CurrentBatch(); again.Done != 2 || again.FinishedAt.IsZero() {
		t.Errorf("CurrentBatch is not a copy: %+v", again)
	}
	// The next batch gets the next id.
	if next := e.startAll(t, Person(actor), e.shopWith(keyPG)); next.ID != 2 {
		t.Errorf("second batch id = %d", next.ID)
	}
	b, _ = e.waitBatch(t)
	if b.ID != 2 || countsOf(b) != "total 1 done 1 ok 1 failed 0 skipped 0" {
		t.Errorf("second batch = %+v", b)
	}
}

// Drive not connected (or not set up, or without a folder) refuses the whole
// batch before anything is queued.
func TestStartAllDriveRefusedNothingQueued(t *testing.T) {
	cases := []struct {
		name string
		opt  envOpt
		set  func(t *testing.T, e *env)
		code string
	}{
		{"not connected", envOpt{noConnect: true}, nil, CodeDriveNotConnected},
		{"no folder", envOpt{noFolder: true}, nil, CodeFolderMissing},
		{"not configured", envOpt{}, func(t *testing.T, e *env) {
			d, err := drive.NewService(config.Config{}, e.st, drive.NewFake(t.TempDir(), ""), time.Now)
			if err != nil {
				t.Fatal(err)
			}
			e.m.d = d
		}, CodeDriveNotConnected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.opt.noStart = true
			e := newEnv(t, c.opt)
			if c.set != nil {
				c.set(t, e)
			}
			e.m.Start(context.Background())
			_, err := e.m.StartAll(context.Background(), Person(actor), []store.Tracked{e.shop})
			if r := refusalOf(t, err); r.Code != c.code || r.Words == "" {
				t.Fatalf("refusal = %+v", r)
			}
			if e.m.CurrentBatch() != nil {
				t.Errorf("batch = %+v", e.m.CurrentBatch())
			}
			if rows := e.shopBackups(t); len(rows) != 0 {
				t.Errorf("rows = %+v", rows)
			}
			if calls := e.r.callsOf(func(runner.Action) bool { return true }); len(calls) != 0 {
				t.Errorf("runner calls = %+v", calls)
			}
			if acts := e.allActivity(t); len(acts) != 1 || acts[0].Result != store.ResultRefused || acts[0].Actor != actor {
				t.Errorf("backup.all activity = %+v", acts)
			}
		})
	}
}

// A second Back up all while one runs is refused batch_running; once it has
// finished a new one starts.
func TestStartAllBatchRunning(t *testing.T) {
	e := newEnv(t)
	release := gateDump(e)
	defer release()
	first := e.startAll(t, Person(actor), e.shopWith(keyPG))
	_, err := e.m.StartAll(context.Background(), Person("other@example.com"), []store.Tracked{e.shop})
	if r := refusalOf(t, err); r.Code != CodeBatchRunning || r.Words == "" {
		t.Fatalf("refusal = %+v", r)
	}
	if cur := e.m.CurrentBatch(); cur.ID != first.ID || cur.StartedBy != actor {
		t.Errorf("current = %+v", cur)
	}
	release()
	e.waitBatch(t)
	if next := e.startAll(t, Person(actor), e.shopWith(keyMongo)); next.ID != first.ID+1 {
		t.Errorf("next id = %d", next.ID)
	}
	e.waitBatch(t)
	acts := e.allActivity(t)
	refused := 0
	for _, a := range acts {
		if a.Result == store.ResultRefused {
			refused++
		}
	}
	if len(acts) != 3 || refused != 1 {
		t.Errorf("backup.all activity = %+v", acts)
	}
}

// A dump failure and a refusal at enqueue count as failed, the rest ok.
func TestStartAllCountsOKFailedRefused(t *testing.T) {
	e := newEnv(t)
	e.r.dump = func(_ context.Context, c call, w io.Writer) ([]byte, error) {
		if c.v["dbname"] == "crm" {
			return []byte("mysqldump: Got error: 1045\n"), &runner.ExitError{Code: 2, Stderr: "mysqldump: Got error: 1045"}
		}
		_, err := w.Write(defaultDump(c))
		return nil, err
	}
	e.startAll(t, Person(actor), e.shopWith(keyPG, keyMySQL, keyStopped, keyMongo, keySQLite))
	b, act := e.waitBatch(t)
	if countsOf(b) != "total 4 done 4 ok 2 failed 2 skipped 1" {
		t.Errorf("batch = %s", countsOf(b))
	}
	if act.Result != store.ResultFailed || act.Reason != "2 succeeded, 2 failed, 1 skipped" {
		t.Errorf("backup.all activity = %+v", act)
	}
	states := map[string]string{}
	for _, r := range e.shopBackups(t) {
		states[r.DBKey] = r.State
	}
	want := map[string]string{keyPG: StateOK, keyMySQL: StateFailed, keyStopped: StateRefused, keyMongo: StateOK}
	if fmt.Sprint(states) != fmt.Sprint(want) {
		t.Errorf("states = %v, want %v", states, want)
	}
}

// A database whose backup is already queued or running joins the batch as that
// backup: no second row, and its result counts.
func TestStartAllJoinsAlreadyRunning(t *testing.T) {
	e := newEnv(t)
	release := gateDump(e)
	defer release()
	manual := e.enqueue(t, keyPG)
	waitFor(t, "the manual dump", func() bool { return len(e.r.callsOf(isDumpAction)) == 1 })
	e.startAll(t, Person("other@example.com"), e.shopWith(keyPG, keyMongo))
	waitFor(t, "the batch to queue", func() bool { return len(e.m.Active()) == 2 })
	if b := e.m.CurrentBatch(); b.Done != 0 || b.FinishedAt != nil {
		t.Errorf("batch before the joined backup ends = %+v", b)
	}
	release()
	b, act := e.waitBatch(t)
	if countsOf(b) != "total 2 done 2 ok 2 failed 0 skipped 0" {
		t.Errorf("batch = %s", countsOf(b))
	}
	if act.Actor != "other@example.com" || act.Reason != "2 succeeded, 0 failed" {
		t.Errorf("backup.all activity = %+v", act)
	}
	rows := e.shopBackups(t)
	pg := 0
	for _, r := range rows {
		if r.DBKey == keyPG {
			pg++
			if r.ID != manual || r.StartedBy != actor {
				t.Errorf("pg row = %+v", r)
			}
		}
	}
	if len(rows) != 2 || pg != 1 {
		t.Errorf("rows = %+v", rows)
	}
}

// A batch Enqueue that waited on another Enqueue of the same database joins the
// backup that one queued; when that backup has already ended by the time the
// join is attached, its stored state is counted.
func TestStartAllJoinEndedJobCountsStoredState(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.r.sizeHook = func(ctx context.Context, _ call) {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	e.r.dump = func(context.Context, call, io.Writer) ([]byte, error) {
		return []byte("pg_dump: error: boom\n"), &runner.ExitError{Code: 1, Stderr: "pg_dump: error: boom"}
	}
	var joined atomic.Int64
	e.m.joinHook = func(id int64) {
		joined.Store(id)
		deadline := time.Now().Add(10 * time.Second)
		for e.active(id) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	type res struct {
		id  int64
		err error
	}
	done := make(chan res, 1)
	go func() {
		id, err := e.m.Enqueue(context.Background(), e.shop, keyPG, Person(actor))
		done <- res{id, err}
	}()
	waitFor(t, "the manual size read", func() bool { return len(e.r.callsOf(func(a runner.Action) bool { return !isStopAction(a) })) == 1 })
	e.startAll(t, Person(actor), e.shopWith(keyPG))
	waitFor(t, "the batch to wait on the reservation", func() bool { return e.m.waiters(e.shop.ID, keyPG) == 1 })
	close(gate)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	b, act := e.waitBatch(t)
	if joined.Load() != r.id {
		t.Errorf("joined %d, want %d", joined.Load(), r.id)
	}
	if countsOf(b) != "total 1 done 1 ok 0 failed 1 skipped 0" {
		t.Errorf("batch = %s", countsOf(b))
	}
	if act.Reason != "0 succeeded, 1 failed" {
		t.Errorf("activity = %+v", act)
	}
	if rows := e.shopBackups(t); len(rows) != 1 || rows[0].ID != r.id {
		t.Errorf("rows = %+v", rows)
	}
}

// Close interrupts the batch's running and queued backups; an Enqueue in
// flight and the candidates not reached yet count as failed with no row; the
// batch ends and its activity is finished.
func TestStartAllCloseInterruptsAndEnds(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	e.r.dump = func(ctx context.Context, _ call, w io.Writer) ([]byte, error) {
		_, _ = w.Write([]byte("-- partial\n"))
		<-ctx.Done()
		return nil, fmt.Errorf("docker dump: %w", ctx.Err())
	}
	e.r.sizeHook = func(ctx context.Context, c call) {
		if c.v["service"] == "mongo" {
			<-ctx.Done()
		}
	}
	e.m.Start(context.Background())
	e.startAll(t, Person(actor), e.shopWith(keyPG, keyMySQL, keyMongo, keyMaria))
	waitFor(t, "the first dump", func() bool { return len(e.r.callsOf(isDumpAction)) == 1 })
	waitFor(t, "the mongo size read", func() bool {
		for _, c := range e.r.callsOf(func(a runner.Action) bool { return !isDumpAction(a) && !isStopAction(a) }) {
			if c.v["service"] == "mongo" {
				return true
			}
		}
		return false
	})
	e.m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	b := e.m.CurrentBatch()
	if b == nil || b.FinishedAt == nil || countsOf(*b) != "total 4 done 4 ok 0 failed 4 skipped 0" {
		t.Fatalf("batch = %+v", b)
	}
	acts := e.allActivity(t)
	if len(acts) != 1 || acts[0].Result != store.ResultFailed || acts[0].Reason != "0 succeeded, 4 failed" {
		t.Errorf("backup.all activity = %+v", acts)
	}
	states := map[string]string{}
	for _, r := range e.shopBackups(t) {
		states[r.DBKey] = r.State
	}
	want := map[string]string{keyPG: StateInterrupted, keyMySQL: StateInterrupted}
	if fmt.Sprint(states) != fmt.Sprint(want) {
		t.Errorf("states = %v, want %v", states, want)
	}
	if _, err := e.m.StartAll(context.Background(), Person(actor), []store.Tracked{e.shop}); !errors.Is(err, ErrClosed) {
		t.Errorf("StartAll after Close: %v", err)
	}
}

// A project untracked after the snapshot: its databases not yet queued are
// skipped (not_found at enqueue); one already queued is refused by the
// worker's re-check and counts as failed.
func TestStartAllProjectGoneCountsSkipped(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.r.sizeHook = func(ctx context.Context, _ call) {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	e.startAll(t, Person(actor), e.shopWith(keyPG, keyMongo, keyMaria))
	waitFor(t, "the first size read", func() bool { return len(e.r.callsOf(func(runner.Action) bool { return true })) == 1 })
	if _, err := e.st.UntrackProject(context.Background(), e.shop.ID); err != nil {
		t.Fatal(err)
	}
	close(gate)
	b, act := e.waitBatch(t)
	if countsOf(b) != "total 3 done 3 ok 0 failed 1 skipped 2" {
		t.Errorf("batch = %s", countsOf(b))
	}
	if act.Reason != "0 succeeded, 1 failed, 2 skipped" {
		t.Errorf("activity = %+v", act)
	}
}

// The batch only ever enqueues: the one worker runs its backups one at a time.
func TestStartAllRunsOneAtATime(t *testing.T) {
	e := newEnv(t)
	var running, most atomic.Int64
	e.r.dump = func(_ context.Context, c call, w io.Writer) ([]byte, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		_, err := w.Write(defaultDump(c))
		return nil, err
	}
	e.startAll(t, Person(actor), e.shopWith(keyPG, keyMySQL, keyMongo, keyMaria, keyPGPlace))
	b, _ := e.waitBatch(t)
	if countsOf(b) != "total 5 done 5 ok 5 failed 0 skipped 0" {
		t.Errorf("batch = %s", countsOf(b))
	}
	if most.Load() != 1 {
		t.Errorf("at most %d dumps ran at once", most.Load())
	}
}

// A panic in the batch goroutine after a candidate was registered counts that
// candidate once (by its backup's end), and only the candidates not yet
// queued as failed.
func TestStartAllPanicAfterRegisterCountsOnce(t *testing.T) {
	e := newEnv(t)
	e.m.enqueuedHook = func(key string) {
		if key == keyPG {
			panic("boom")
		}
	}
	e.startAll(t, Person(actor), e.shopWith(keyPG, keyMongo))
	b, act := e.waitBatch(t)
	if countsOf(b) != "total 2 done 2 ok 1 failed 1 skipped 0" {
		t.Errorf("batch = %s", countsOf(b))
	}
	if act.Result != store.ResultFailed || act.Reason != "1 succeeded, 1 failed" {
		t.Errorf("backup.all activity = %+v", act)
	}
	rows := e.shopBackups(t)
	if len(rows) != 1 || rows[0].DBKey != keyPG || rows[0].State != StateOK {
		t.Errorf("rows = %+v", rows)
	}
}
