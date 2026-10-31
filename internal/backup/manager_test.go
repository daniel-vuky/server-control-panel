package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

const (
	actor   = "ops@example.com"
	devPass = "devpassword"
	gib     = int64(1) << 30
)

// The databases of the test project "shop", by key.
const (
	keyPG      = "postgresql:db:shop"
	keyMySQL   = "mysql:crm-mysql:crm"
	keyMongo   = "mongodb:mongo:notes"
	keyPGPlace = "postgresql:pg2:pg2"
	keyMyPlace = "mysql:my2:my2"
	keySQLite  = "sqlite:/data/app.db:app.db"
	keyHost    = "postgresql:db.example.com:remote"
	keyGhost   = "postgresql:ghost:ghost"
	keyStopped = "postgresql:stopped:st"
	keyBadName = "mysql:my3:bad name"
	keyMaria   = "mysql:maria:books"
)

// call is one runner call.
type call struct {
	a       runner.Action
	v       runner.Values
	secrets []string
}

// fakeRunner scripts the size reads and the dumps.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []call
	sizeOut string
	sizeErr error
	// sizeHook, when set, runs before a size read answers (it may block).
	sizeHook func(ctx context.Context, c call)
	// dump, when set, replaces the default dump.
	dump func(ctx context.Context, c call, w io.Writer) ([]byte, error)
}

func (r *fakeRunner) record(a runner.Action, v runner.Values, secrets []string) call {
	cp := runner.Values{}
	for k, val := range v {
		cp[k] = val
	}
	c := call{a: a, v: cp, secrets: append([]string(nil), secrets...)}
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
	return c
}

func (r *fakeRunner) Run(ctx context.Context, _ runner.Stack, a runner.Action, v runner.Values, secrets []string) (runner.Result, error) {
	c := r.record(a, v, secrets)
	r.mu.Lock()
	hook, out, err := r.sizeHook, r.sizeOut, r.sizeErr
	r.mu.Unlock()
	if hook != nil {
		hook(ctx, c)
	}
	if err != nil {
		return runner.Result{}, err
	}
	if out == "" {
		out = "1048576\n"
	}
	return runner.Result{Stdout: []byte(out)}, nil
}

func defaultDump(c call) []byte {
	return bytes.Repeat([]byte("-- dump of "+c.v["dbname"]+" from "+c.v["service"]+"\n"), 2000)
}

func (r *fakeRunner) RunTo(ctx context.Context, _ runner.Stack, a runner.Action, v runner.Values, secrets []string, w io.Writer) ([]byte, error) {
	c := r.record(a, v, secrets)
	r.mu.Lock()
	dump := r.dump
	r.mu.Unlock()
	if dump != nil {
		return dump(ctx, c, w)
	}
	_, err := w.Write(defaultDump(c))
	return nil, err
}

func (r *fakeRunner) callsOf(pred func(runner.Action) bool) []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []call
	for _, c := range r.calls {
		if pred(c.a) {
			out = append(out, c)
		}
	}
	return out
}

func isDumpAction(a runner.Action) bool { return strings.Contains(string(a), ".dump") }

// fakeHub is the container list.
type fakeHub struct {
	mu sync.Mutex
	cs []project.Container
	up bool
}

func (h *fakeHub) containers(context.Context) ([]project.Container, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]project.Container(nil), h.cs...), h.up
}

func (h *fakeHub) setState(service, state string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.cs {
		if h.cs[i].Service == service {
			h.cs[i].State = state
		}
	}
}

// stallClient makes every upload fail as the Google client does after 2
// minutes without progress.
type stallClient struct{ drive.Client }

func (c stallClient) Session(refresh string) drive.Session {
	return stallSession{c.Client.Session(refresh)}
}

type stallSession struct{ drive.Session }

func (stallSession) Upload(context.Context, string, string, int64, io.Reader, func(int64)) (string, string, error) {
	return "", "", &drive.Error{Code: drive.CodeUnreachable, Err: errors.New("no progress for 2m0s")}
}

type env struct {
	st      *store.Store
	m       *Manager
	r       *fakeRunner
	hub     *fakeHub
	drv     *drive.Service
	dataDir string
	shop    store.Tracked

	free    atomic.Int64
	changes atomic.Int64
}

type envOpt struct {
	client      func(drive.Client) drive.Client
	noConnect   bool
	noFolder    bool
	noStart     bool
	publishEach time.Duration
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	var o envOpt
	if len(opts) > 0 {
		o = opts[0]
	}
	ctx := context.Background()
	base := t.TempDir()
	e := &env{r: &fakeRunner{}, dataDir: filepath.Join(base, "data")}
	st, err := store.Open(ctx, filepath.Join(e.dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e.st = st

	root := filepath.Join(base, "work")
	dir := filepath.Join(root, "shop")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_PASSWORD="+devPass+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := func(name, image, container string) project.Service {
		return project.Service{Name: name, Image: image, ContainerName: container}
	}
	db := func(typ, name, kind, loc string, sources ...string) project.Database {
		if sources == nil {
			sources = []string{"service " + loc}
		}
		return project.Database{Type: typ, Name: name, LocationKind: kind, Location: loc, Sources: sources}
	}
	np := store.NewProject{
		Path: dir, ComposeFiles: []string{"compose.yaml"}, ComposeProject: "shop", DisplayName: "Shop", Tracked: true,
		Details: project.Details{Services: []project.Service{
			svc("db", "postgres:16", ""), svc("mysql", "mysql:9", "crm-mysql"), svc("mongo", "mongo:7", ""),
			svc("pg2", "postgres:16", ""), svc("my2", "mysql:9", ""), svc("my3", "mysql:9", ""),
			svc("stopped", "postgres:16", ""), svc("maria", "mariadb:11", ""),
		}},
		Databases: []project.Database{
			db("postgresql", "shop", "container", "db", "service db", ".env DATABASE_URL"),
			db("mysql", "crm", "container", "crm-mysql", "service mysql"),
			db("mongodb", "notes", "container", "mongo"),
			db("postgresql", "pg2", "container", "pg2"),
			db("mysql", "my2", "container", "my2"),
			db("mysql", "bad name", "container", "my3"),
			db("sqlite", "app.db", "file", "/data/app.db"),
			db("postgresql", "remote", "host", "db.example.com"),
			db("postgresql", "ghost", "container", "ghost"),
			db("postgresql", "st", "container", "stopped"),
			db("mysql", "books", "container", "maria"),
		},
	}
	if _, _, err := st.AddWorkspace(ctx, store.NewWorkspace{Name: "work", Path: root, Depth: 1, LastScanAt: time.Now()}, []store.NewProject{np}); err != nil {
		t.Fatal(err)
	}
	ts, err := st.TrackedProjects(ctx)
	if err != nil || len(ts) != 1 {
		t.Fatalf("TrackedProjects = %v, %v", ts, err)
	}
	e.shop = ts[0]

	ctr := func(service, name, state string) project.Container {
		return project.Container{Project: "shop", Service: service, Name: name, State: state, ID: strings.Repeat("a", 64)}
	}
	e.hub = &fakeHub{up: true, cs: []project.Container{
		ctr("db", "shop-db-1", "running"), ctr("mysql", "crm-mysql", "running"), ctr("mongo", "shop-mongo-1", "running"),
		ctr("pg2", "shop-pg2-1", "running"), ctr("my2", "shop-my2-1", "running"), ctr("my3", "shop-my3-1", "running"),
		ctr("stopped", "shop-stopped-1", "exited"), ctr("maria", "shop-maria-1", "running"),
	}}

	client := drive.NewFake(filepath.Join(e.dataDir, "fake-drive"), "http://localhost:5173")
	if o.client != nil {
		client = o.client(client)
	}
	cfg := config.Config{
		SecretKey: bytes.Repeat([]byte{7}, 32), PublicURL: "http://localhost:5173",
		GoogleClientID: "client-id", GoogleClientSecret: "client-secret",
	}
	e.drv, err = drive.NewService(cfg, st, client, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if !o.noConnect {
		connectDrive(t, e.drv, !o.noFolder)
	}

	e.free.Store(100 * gib)
	e.m = New(st, e.r, e.drv, e.hub.containers, e.dataDir, time.Now, discardLog())
	e.m.freeSpace = func(string) (int64, error) { return e.free.Load(), nil }
	if o.publishEach > 0 {
		e.m.publishEvery = o.publishEach
	}
	e.m.OnChange(func() { e.changes.Add(1) })
	if !o.noStart {
		e.start(t)
	} else {
		t.Cleanup(func() { e.m.Close() })
	}
	return e
}

func (e *env) start(t *testing.T) {
	t.Helper()
	e.m.Start(context.Background())
	t.Cleanup(func() {
		e.m.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.m.Wait(ctx); err != nil {
			t.Errorf("Wait: %v", err)
		}
	})
}

func connectDrive(t *testing.T, d *drive.Service, folder bool) {
	t.Helper()
	ctx := context.Background()
	u, err := d.StartConnect(ctx, actor, "/settings")
	if err != nil {
		t.Fatal(err)
	}
	pu, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	state := pu.Query().Get("state")
	if _, err := d.FinishConnect(ctx, actor, state, "fake-"+state, ""); err != nil {
		t.Fatal(err)
	}
	if folder {
		if _, err := d.CreateFolder(ctx, "server-backups"); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) active(id int64) bool {
	for _, b := range e.m.Active() {
		if b.ID == id {
			return true
		}
	}
	return false
}

// waitDone waits until backup id has left the active list and returns its row.
func (e *env) waitDone(t *testing.T, id int64) store.Backup {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for e.active(id) {
		if time.Now().After(deadline) {
			t.Fatalf("backup %d still active: %+v", id, e.m.Active())
		}
		time.Sleep(2 * time.Millisecond)
	}
	b, err := e.st.GetBackup(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (e *env) enqueue(t *testing.T, key string) int64 {
	t.Helper()
	id, err := e.m.Enqueue(context.Background(), e.shop, key, actor)
	if err != nil {
		t.Fatalf("Enqueue(%s): %v", key, err)
	}
	return id
}

func (e *env) activity(t *testing.T) []store.ActivityRow {
	t.Helper()
	rows, err := e.st.RecentActivity(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.ActivityRow
	for _, r := range rows {
		if r.Action == store.ActBackupRun {
			out = append(out, r)
		}
	}
	return out
}

func (e *env) folder(t *testing.T) drive.Folder {
	t.Helper()
	st := e.drv.Status()
	if st.Folder == nil {
		t.Fatal("no destination folder")
	}
	return *st.Folder
}

func refusalOf(t *testing.T, err error) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a *Refusal", err)
	}
	return r
}

func stepStates(b store.Backup) string {
	var parts []string
	for _, s := range b.Steps {
		parts = append(parts, s.Step+"="+s.State)
	}
	return strings.Join(parts, " ")
}

func tempFiles(t *testing.T, dataDir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dataDir, "tmp", "backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func assertNoSecret(t *testing.T, what string, values ...string) {
	t.Helper()
	for _, v := range values {
		if strings.Contains(v, devPass) {
			t.Errorf("%s holds the .env secret: %q", what, v)
		}
	}
}

func TestHappyPathStepsSizesFileActivityTempGone(t *testing.T) {
	e := newEnv(t)
	before := time.Now()
	id := e.enqueue(t, keyPG)
	b := e.waitDone(t, id)

	if b.State != "ok" || b.FailedStep != "" || b.Error != "" || b.Hint != "" {
		t.Fatalf("row = %+v", b)
	}
	if got := stepStates(b); got != "dump=done compress=done upload=done prune=skipped" {
		t.Errorf("steps = %s", got)
	}
	if d := b.Steps[0].Detail; !strings.HasPrefix(d, "pg_dump · ") || !strings.HasSuffix(d, "s") {
		t.Errorf("dump detail = %q", d)
	}
	if d := b.Steps[1].Detail; !strings.HasPrefix(d, "gzip · ") || !strings.Contains(d, "→") {
		t.Errorf("compress detail = %q", d)
	}
	if d := b.Steps[3].Detail; d != "No retention set yet. All copies are kept." {
		t.Errorf("prune detail = %q", d)
	}
	if b.ProjectID != e.shop.ID || b.ProjectName != "Shop" || b.DBType != "postgresql" || b.DBName != "shop" ||
		b.DBLocationKind != "container" || b.DBLocation != "db" || b.DBService != "db" || b.DBKey != keyPG || b.StartedBy != actor {
		t.Errorf("snapshot = %+v", b)
	}
	if b.StartedAt == nil || b.FinishedAt == nil || b.StartedAt.Before(before.Add(-time.Second)) || b.FinishedAt.Before(*b.StartedAt) || b.DurationMS < 0 {
		t.Errorf("times = %v %v %d", b.StartedAt, b.FinishedAt, b.DurationMS)
	}
	if want := FileName("Shop", "shop", "postgresql", *b.StartedAt); b.FileName != want {
		t.Errorf("file name = %q, want %q", b.FileName, want)
	}

	// The calls: one size read at Enqueue, one at the worker's re-check, one dump,
	// all with the compose values, the service, the name and the .env secrets.
	sizes := e.r.callsOf(func(a runner.Action) bool { return a == runner.DBPgSize })
	dumps := e.r.callsOf(isDumpAction)
	if len(sizes) != 2 || len(dumps) != 1 || dumps[0].a != runner.DBPgDump {
		t.Fatalf("sizes = %+v, dumps = %+v", sizes, dumps)
	}
	want := runner.Values{"dir": e.shop.Path, "file": filepath.Join(e.shop.Path, "compose.yaml"), "service": "db", "dbname": "shop"}
	if fmt.Sprint(dumps[0].v) != fmt.Sprint(want) {
		t.Errorf("dump values = %v, want %v", dumps[0].v, want)
	}
	if !strings.Contains(strings.Join(dumps[0].secrets, "\n"), devPass) {
		t.Errorf("dump secrets = %v, want the .env values", dumps[0].secrets)
	}

	// The file on Drive is the gzip of the dump, in <folder>/<project slug>/.
	raw := defaultDump(dumps[0])
	if b.RawBytes != int64(len(raw)) {
		t.Errorf("raw bytes = %d, want %d", b.RawBytes, len(raw))
	}
	sess, _, err := e.drv.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rc, err := sess.Download(context.Background(), b.DriveFileID)
	if err != nil {
		t.Fatal(err)
	}
	gzBytes, _ := io.ReadAll(rc)
	_ = rc.Close()
	if int64(len(gzBytes)) != b.SizeBytes || b.SizeBytes == 0 || b.SizeBytes >= b.RawBytes {
		t.Errorf("size bytes = %d, file is %d bytes, raw %d", b.SizeBytes, len(gzBytes), b.RawBytes)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, raw) {
		t.Error("the uploaded file does not gunzip to the dump")
	}
	if b.DriveLink == "" {
		t.Error("no drive link")
	}
	folders, err := sess.FindOrCreateFolder(context.Background(), "shop", e.folder(t).ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "fake-drive", folders.ID, b.FileName)); err != nil {
		t.Errorf("file not in <folder>/shop/: %v", err)
	}

	acts := e.activity(t)
	if len(acts) != 1 || acts[0].Result != store.ResultOK || acts[0].Actor != actor || acts[0].Target != "Shop · shop" ||
		acts[0].ProjectID != e.shop.ID || acts[0].FinishedAt == nil {
		t.Errorf("activity = %+v", acts)
	}
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestSuccessIgnoresStderrOnExitZero(t *testing.T) {
	e := newEnv(t)
	e.r.dump = func(_ context.Context, c call, w io.Writer) ([]byte, error) {
		_, err := w.Write(defaultDump(c))
		return []byte("mysqldump: Error: 'SELECT command denied' when trying to dump masking policies\n"), err
	}
	b := e.waitDone(t, e.enqueue(t, keyMySQL))
	if b.State != "ok" || b.Error != "" || b.Hint != "" {
		t.Fatalf("a dump that exits 0 must succeed: %+v", b)
	}
}

func TestDumpFailureHintAndSkippedSteps(t *testing.T) {
	e := newEnv(t)
	e.r.dump = func(_ context.Context, _ call, w io.Writer) ([]byte, error) {
		_, _ = w.Write([]byte("-- partial\n"))
		stderr := "Warning: something harmless\npg_dump: error: connection failed: FATAL:  password authentication failed for user \"" + devPass + "\"\nWarning: trailing\n"
		return []byte(stderr), &runner.ExitError{Code: 1, Stderr: stderr}
	}
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "failed" || b.FailedStep != "dump" {
		t.Fatalf("row = %+v", b)
	}
	if got := stepStates(b); got != "dump=failed compress=skipped upload=skipped prune=skipped" {
		t.Errorf("steps = %s", got)
	}
	if b.Steps[3].Detail != "temp file deleted" {
		t.Errorf("prune detail = %q", b.Steps[3].Detail)
	}
	if !strings.Contains(b.Error, "password authentication failed") || strings.HasPrefix(b.Error, "Warning") {
		t.Errorf("error = %q", b.Error)
	}
	if want := "Check POSTGRES_PASSWORD (and POSTGRES_USER) in the db service's environment (detected from DATABASE_URL)."; b.Hint != want {
		t.Errorf("hint = %q, want %q", b.Hint, want)
	}
	assertNoSecret(t, "row", b.Error, b.Hint)
	acts := e.activity(t)
	if len(acts) != 1 || acts[0].Result != store.ResultFailed || !strings.HasPrefix(acts[0].Reason, "Dump: ") {
		t.Errorf("activity = %+v", acts)
	}
	assertNoSecret(t, "activity", acts[0].Reason)
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	// Nothing reached Drive.
	if b.DriveFileID != "" || b.FileName == "" {
		t.Errorf("drive file = %q, name = %q", b.DriveFileID, b.FileName)
	}
}

// A MariaDB service is worded with its own tool and variables: the Dump
// step's detail, the exit words and the 1045 hint.
func TestMariaDBWording(t *testing.T) {
	e := newEnv(t)
	ok := e.waitDone(t, e.enqueue(t, keyMaria))
	if d := ok.Steps[0].Detail; ok.State != "ok" || !strings.HasPrefix(d, "mariadb-dump · ") {
		t.Errorf("mariadb dump = %s, detail %q", ok.State, d)
	}
	mysql := e.waitDone(t, e.enqueue(t, keyMySQL))
	if d := mysql.Steps[0].Detail; mysql.State != "ok" || !strings.HasPrefix(d, "mysqldump · ") {
		t.Errorf("mysql dump = %s, detail %q", mysql.State, d)
	}

	e.r.dump = func(context.Context, call, io.Writer) ([]byte, error) {
		stderr := "mariadb-dump: Got error: 1045: Access denied for user 'root'@'localhost' (using password: YES) when trying to connect\n"
		return []byte(stderr), &runner.ExitError{Code: 2, Stderr: stderr}
	}
	b := e.waitDone(t, e.enqueue(t, keyMaria))
	if want := "Check MARIADB_ROOT_PASSWORD or MARIADB_USER / MARIADB_PASSWORD in the maria service's environment."; b.Hint != want {
		t.Errorf("hint = %q, want %q", b.Hint, want)
	}
	e.r.dump = func(context.Context, call, io.Writer) ([]byte, error) { return nil, &runner.ExitError{Code: 2} }
	b = e.waitDone(t, e.enqueue(t, keyMaria))
	if b.Error != "mariadb-dump exited with status 2." || b.Steps[0].Detail != "mariadb-dump" {
		t.Errorf("error = %q, dump detail = %q", b.Error, b.Steps[0].Detail)
	}
}

func TestDumpFailureWords(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"stall", &runner.StallError{After: 5 * time.Minute}, "The dump stopped sending data for 5 minutes. It may still be running inside db."},
		{"ceiling", &runner.TimeoutError{After: 6 * time.Hour}, "The dump ran for 6 hours and was stopped. It may still be running inside db."},
		{"exit without stderr", &runner.ExitError{Code: 3}, "pg_dump exited with status 3."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.r.dump = func(context.Context, call, io.Writer) ([]byte, error) { return nil, tc.err }
			b := e.waitDone(t, e.enqueue(t, keyPG))
			if b.State != "failed" || b.FailedStep != "dump" || b.Error != tc.want || b.Hint != "" {
				t.Errorf("row = %q %q %q %q, want error %q", b.State, b.FailedStep, b.Error, b.Hint, tc.want)
			}
		})
	}
}

func TestDumpWriteErrorFailsDump(t *testing.T) {
	e := newEnv(t)
	// RunTo wraps the failed write of the temporary file, as on a full disk.
	e.r.dump = func(_ context.Context, _ call, w io.Writer) ([]byte, error) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1<<20))
		return nil, fmt.Errorf("docker db.pg.dump: write output: %w", &os.PathError{Op: "write", Path: "/data/tmp/backup-1.gz", Err: errors.New("no space left on device")})
	}
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "failed" || b.FailedStep != "dump" || b.Error != "Could not write the temporary file: no space left on device" {
		t.Errorf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
}

func TestUploadFolderMissing(t *testing.T) {
	e := newEnv(t)
	sess, folder, err := e.drv.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Delete(context.Background(), folder.ID); err != nil {
		t.Fatal(err)
	}
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "failed" || b.FailedStep != "upload" || b.Error != drive.Words(drive.CodeFolderMissing) {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	if got := stepStates(b); got != "dump=done compress=done upload=failed prune=skipped" {
		t.Errorf("steps = %s", got)
	}
	if b.Steps[3].Detail != "temp file deleted" {
		t.Errorf("prune detail = %q", b.Steps[3].Detail)
	}
	acts := e.activity(t)
	if len(acts) != 1 || acts[0].Reason != "Upload: "+drive.Words(drive.CodeFolderMissing) {
		t.Errorf("activity = %+v", acts)
	}
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestUploadTokenExpiredFlipsDriveState(t *testing.T) {
	e := newEnv(t)
	sess, _, err := e.drv.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Revoked at Google: the next call answers invalid_grant.
	if err := sess.Revoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "failed" || b.FailedStep != "upload" || b.Error != drive.Words(drive.CodeTokenExpired) {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	if st := e.drv.Status().State; st != drive.StateExpired {
		t.Errorf("drive state = %s, want expired", st)
	}
	// The next Enqueue is refused at the Drive check.
	_, err = e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
	if r := refusalOf(t, err); r.Code != CodeDriveExpired {
		t.Errorf("code = %s", r.Code)
	}
}

func TestUploadStall(t *testing.T) {
	e := newEnv(t, envOpt{client: func(c drive.Client) drive.Client { return stallClient{c} }})
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "failed" || b.FailedStep != "upload" || b.Error != "Google Drive is unreachable." {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	if got := stepStates(b); got != "dump=done compress=done upload=failed prune=skipped" {
		t.Errorf("steps = %s", got)
	}
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// truncClient cuts the temporary file short before the upload reads it, as a
// failing disk would.
type truncClient struct {
	drive.Client
	dataDir *string // set once the env exists
}

func (c truncClient) Session(refresh string) drive.Session {
	return truncSession{Session: c.Client.Session(refresh), dataDir: c.dataDir}
}

type truncSession struct {
	drive.Session
	dataDir *string
}

func (s truncSession) FindOrCreateFolder(ctx context.Context, name, parentID string) (drive.Folder, error) {
	files, _ := filepath.Glob(filepath.Join(*s.dataDir, "tmp", "backup-*.gz"))
	for _, f := range files {
		_ = os.Truncate(f, 10)
	}
	return s.Session.FindOrCreateFolder(ctx, name, parentID)
}

// A failure to read the temporary file during the upload is the panel's own:
// worded as a local file problem, not "Google Drive is unreachable."
func TestUploadLocalReadFailure(t *testing.T) {
	var dataDir string
	e := newEnv(t, envOpt{client: func(c drive.Client) drive.Client { return truncClient{Client: c, dataDir: &dataDir} }})
	dataDir = e.dataDir
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "failed" || b.FailedStep != "upload" || b.Error != "Could not read the dump file: unexpected EOF" {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// dumpReader turns any read failure into a *localError, a short file included.
func TestDumpReader(t *testing.T) {
	r := &dumpReader{r: strings.NewReader("abc"), left: 3}
	if b, err := io.ReadAll(r); err != nil || string(b) != "abc" {
		t.Fatalf("full read = %q, %v", b, err)
	}
	r = &dumpReader{r: strings.NewReader("abc"), left: 5}
	var local *localError
	if _, err := io.ReadAll(r); !errors.As(err, &local) || local.msg != "Could not read the dump file: unexpected EOF" {
		t.Fatalf("short read = %v", err)
	}
	r = &dumpReader{r: io.MultiReader(strings.NewReader("a"), iotestErr{&os.PathError{Op: "read", Path: "/data/tmp/backup-1.gz", Err: errors.New("input/output error devpassword")}}), left: 5, secrets: []string{devPass}}
	_, err := io.ReadAll(r)
	if !errors.As(err, &local) || local.msg != "Could not read the dump file: input/output error ••••" {
		t.Fatalf("failed read = %v", err)
	}
}

// iotestErr is a reader that fails with err.
type iotestErr struct{ err error }

func (r iotestErr) Read([]byte) (int, error) { return 0, r.err }

func TestRefusalsWriteRowsAndActivity(t *testing.T) {
	type tc struct {
		name    string
		setup   func(e *env) (store.Tracked, string)
		code    string
		words   string
		row     bool
		service string
	}
	shop := func(key string) func(e *env) (store.Tracked, string) {
		return func(e *env) (store.Tracked, string) { return e.shop, key }
	}
	cases := []tc{
		{"missing project", func(e *env) (store.Tracked, string) {
			t := e.shop
			now := time.Now()
			t.MissingSince = &now
			return t, keyPG
		}, CodeNotFound, "", false, ""},
		{"unknown key", shop("postgresql:nope:nope"), CodeUnknownDatabase, "This database is no longer detected.", false, ""},
		{"sqlite", shop(keySQLite), CodeNotSupported, "SQLite backups are not supported yet", false, ""},
		{"host", shop(keyHost), CodeNotSupported, "Backups of databases outside a container are not supported yet", false, ""},
		{"drive not connected", func(e *env) (store.Tracked, string) {
			if err := e.drv.Disconnect(context.Background()); err != nil {
				panic(err)
			}
			return e.shop, keyPG
		}, CodeDriveNotConnected, "Connect Google Drive first", true, ""},
		{"drive expired", func(e *env) (store.Tracked, string) {
			sess, _, err := e.drv.Session(context.Background())
			if err != nil {
				panic(err)
			}
			_ = sess.Revoke(context.Background())
			_, _ = sess.Account(context.Background()) // flips the state
			return e.shop, keyPG
		}, CodeDriveExpired, drive.Words(drive.CodeTokenExpired), true, ""},
		{"service unresolved", shop(keyGhost), CodeNotSupported, "The database's service could not be found", true, ""},
		{"placeholder mysql", shop(keyMyPlace), CodeNoDBName, "The database name is not known. Set MYSQL_DATABASE (or MONGO_INITDB_DATABASE) on the service, then rescan.", true, "my2"},
		{"bad name", shop(keyBadName), CodeNoDBName, "The database name is not known. Set MYSQL_DATABASE (or MONGO_INITDB_DATABASE) on the service, then rescan.", true, "my3"},
		{"not running", shop(keyStopped), CodeDBNotRunning, "Start stopped first", true, "stopped"},
		{"docker down", func(e *env) (store.Tracked, string) {
			e.hub.mu.Lock()
			e.hub.up, e.hub.cs = false, nil
			e.hub.mu.Unlock()
			return e.shop, keyPG
		}, CodeDBNotRunning, "Start db first", true, "db"},
		{"disk short", func(e *env) (store.Tracked, string) {
			e.free.Store(1)
			return e.shop, keyPG
		}, CodeDiskShort, "Needs about 100 MB free, the server has 1 B left. Free up some disk space and try again.", true, "db"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, envOpt{noStart: true})
			tr, key := c.setup(e)
			id, err := e.m.Enqueue(context.Background(), tr, key, actor)
			r := refusalOf(t, err)
			if id != 0 || r.Code != c.code || (c.words != "" && r.Words != c.words) || r.Service != c.service {
				t.Fatalf("Enqueue = %d, %+v; want code %s words %q service %q", id, r, c.code, c.words, c.service)
			}
			rows, _, err := e.st.ProjectBackups(context.Background(), e.shop.ID, 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			acts := e.activity(t)
			if !c.row {
				if len(rows) != 0 || len(acts) != 0 || r.ID != 0 {
					t.Errorf("an input error wrote rows %+v, activity %+v, id %d", rows, acts, r.ID)
				}
				return
			}
			if len(rows) != 1 || rows[0].ID != r.ID || rows[0].State != "refused" || rows[0].Error != r.Words ||
				rows[0].FinishedAt == nil || rows[0].StartedBy != actor || rows[0].DBKey != key || rows[0].DBService != c.service {
				t.Fatalf("rows = %+v, refusal id %d", rows, r.ID)
			}
			if got := stepStates(rows[0]); got != "dump=skipped compress=skipped upload=skipped prune=skipped" {
				t.Errorf("steps = %s", got)
			}
			if len(acts) != 1 || acts[0].Result != store.ResultRefused || acts[0].Reason != r.Words || acts[0].FinishedAt == nil {
				t.Errorf("activity = %+v", acts)
			}
			if len(e.m.Active()) != 0 {
				t.Errorf("a refusal is active: %+v", e.m.Active())
			}
		})
	}

	t.Run("folder missing", func(t *testing.T) {
		e := newEnv(t, envOpt{noStart: true, noFolder: true})
		_, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
		if r := refusalOf(t, err); r.Code != CodeFolderMissing || r.Words != drive.Words(drive.CodeFolderMissing) || r.ID == 0 {
			t.Errorf("refusal = %+v", r)
		}
	})
	t.Run("drive not configured", func(t *testing.T) {
		e := newEnv(t, envOpt{noStart: true, noConnect: true})
		d, err := drive.NewService(config.Config{}, e.st, drive.NewFake(t.TempDir(), ""), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		e.m.d = d
		_, err = e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
		if r := refusalOf(t, err); r.Code != CodeDriveNotConnected || r.Words != "Google Drive is not set up on this server." {
			t.Errorf("refusal = %+v", r)
		}
	})
	t.Run("already running", func(t *testing.T) {
		e := newEnv(t, envOpt{noStart: true})
		id := e.enqueue(t, keyPG)
		got, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
		r := refusalOf(t, err)
		if got != 0 || r.Code != CodeAlreadyRunning || r.ID != id || r.Words != "A backup of shop is already running." {
			t.Errorf("Enqueue = %d, %+v; want already_running %d", got, r, id)
		}
		rows, _, _ := e.st.ProjectBackups(context.Background(), e.shop.ID, 0, 10)
		if len(rows) != 1 {
			t.Errorf("rows = %+v, want only the queued one", rows)
		}
	})
}

func TestRecheckWhenWorkerStarts(t *testing.T) {
	cases := []struct {
		name   string
		change func(e *env)
		code   string
	}{
		{"db stopped", func(e *env) { e.hub.setState("db", "exited") }, CodeDBNotRunning},
		{"drive disconnected", func(e *env) {
			if err := e.drv.Disconnect(context.Background()); err != nil {
				panic(err)
			}
		}, CodeDriveNotConnected},
		{"disk filled", func(e *env) { e.free.Store(1) }, CodeDiskShort},
		{"project untracked", func(e *env) {
			if _, err := e.st.UntrackProject(context.Background(), e.shop.ID); err != nil {
				panic(err)
			}
		}, CodeNotFound},
		{"workspace removed", func(e *env) {
			if _, err := e.st.RemoveWorkspace(context.Background(), e.shop.WorkspaceID); err != nil {
				panic(err)
			}
		}, CodeNotFound},
		{"folder missing", func(e *env) {
			apply := store.RescanApply{Workspaces: []store.WorkspaceApply{{ID: e.shop.WorkspaceID, LastScanAt: time.Now(), Missing: []string{e.shop.Path}}}}
			if err := e.st.ApplyRescan(context.Background(), apply, time.Now()); err != nil {
				panic(err)
			}
		}, CodeNotFound},
		{"database gone", func(e *env) {
			apply := store.RescanApply{Workspaces: []store.WorkspaceApply{{ID: e.shop.WorkspaceID, LastScanAt: time.Now(),
				Refresh: []store.ProjectRefresh{{Path: e.shop.Path, Config: &store.ConfigRefresh{
					ComposeFiles: e.shop.ComposeFiles, ComposeProject: e.shop.ComposeProject,
					Details: project.Details{Services: e.shop.ServiceDetails}}}},
				Found: []string{e.shop.Path}}}}
			if err := e.st.ApplyRescan(context.Background(), apply, time.Now()); err != nil {
				panic(err)
			}
		}, CodeUnknownDatabase},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, envOpt{noStart: true})
			id := e.enqueue(t, keyPG)
			if b, _ := e.st.GetBackup(context.Background(), id); b.State != "queued" {
				t.Fatalf("state = %s, want queued", b.State)
			}
			c.change(e)
			e.start(t)
			b := e.waitDone(t, id)
			if b.State != "refused" || b.Error != wordsOf(c.code, b.Error) {
				t.Fatalf("row = %+v", b)
			}
			if got := stepStates(b); got != "dump=skipped compress=skipped upload=skipped prune=skipped" {
				t.Errorf("steps = %s", got)
			}
			if dumps := e.r.callsOf(isDumpAction); len(dumps) != 0 {
				t.Errorf("a refused backup dumped: %+v", dumps)
			}
			acts := e.activity(t)
			if len(acts) != 1 || acts[0].Result != store.ResultRefused || acts[0].Reason != b.Error {
				t.Errorf("activity = %+v", acts)
			}
		})
	}
}

// wordsOf is the refusal words a re-check gives for code; got for the codes
// whose words carry values (the service, the sizes), when it is not empty.
func wordsOf(code, got string) string {
	switch code {
	case CodeNotFound:
		return wordsNotFound
	case CodeUnknownDatabase:
		return wordsUnknownDatabase
	}
	if got == "" {
		return "(words)"
	}
	return got
}

// A queued backup re-reads its project when the worker takes it: a change
// made meanwhile (here a rescan that found an override file) is what the
// backup runs with.
func TestRecheckUsesTheProjectAsItIsNow(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	id := e.enqueue(t, keyPG)
	files := []string{"compose.yaml", "compose.override.yaml"}
	apply := store.RescanApply{Workspaces: []store.WorkspaceApply{{ID: e.shop.WorkspaceID, LastScanAt: time.Now(),
		Refresh: []store.ProjectRefresh{{Path: e.shop.Path, Config: &store.ConfigRefresh{
			ComposeFiles: files, ComposeProject: e.shop.ComposeProject,
			Details: project.Details{Services: e.shop.ServiceDetails}, Databases: e.shop.Databases}}},
		Found: []string{e.shop.Path}}}}
	if err := e.st.ApplyRescan(context.Background(), apply, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.start(t)
	if b := e.waitDone(t, id); b.State != "ok" {
		t.Fatalf("row = %+v", b)
	}
	dumps := e.r.callsOf(isDumpAction)
	if len(dumps) != 1 || dumps[0].a != runner.DBPgDump+".override" || dumps[0].v["override"] != filepath.Join(e.shop.Path, files[1]) {
		t.Errorf("dump = %+v, want the override file the rescan found", dumps)
	}
}

func TestEnqueueConcurrentSameDatabaseOneBackup(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	e.r.sizeHook = func(ctx context.Context, _ call) {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}
	type result struct {
		id  int64
		err error
	}
	first := make(chan result, 1)
	go func() {
		id, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
		first <- result{id, err}
	}()
	<-entered // the first holds the reservation, in its disk check
	second := make(chan result, 1)
	go func() {
		id, err := e.m.Enqueue(context.Background(), e.shop, keyPG, "other@example.com")
		second <- result{id, err}
	}()
	waitFor(t, "the second Enqueue to wait", func() bool { return e.m.waiters(e.shop.ID, keyPG) == 1 })
	close(release)
	r1, r2 := <-first, <-second
	if r1.err != nil || r1.id == 0 {
		t.Fatalf("first = %+v", r1)
	}
	if r := refusalOf(t, r2.err); r2.id != 0 || r.Code != CodeAlreadyRunning || r.ID != r1.id {
		t.Fatalf("second = %d %+v, want already_running %d", r2.id, r, r1.id)
	}
	rows, _, _ := e.st.ProjectBackups(context.Background(), e.shop.ID, 0, 10)
	if len(rows) != 1 {
		t.Errorf("rows = %d, want 1", len(rows))
	}
	if sizes := e.r.callsOf(func(a runner.Action) bool { return strings.HasSuffix(string(a), ".size") }); len(sizes) != 1 {
		t.Errorf("size reads = %d, want 1: the waiter runs no check", len(sizes))
	}
}

func TestEnqueueWaiterGetsTheSameRefusal(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	e.free.Store(1)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	e.r.sizeHook = func(context.Context, call) {
		once.Do(func() { close(entered); <-release })
	}
	errs := make(chan error, 2)
	go func() { _, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor); errs <- err }()
	<-entered
	go func() { _, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor); errs <- err }()
	waitFor(t, "the waiter", func() bool { return e.m.waiters(e.shop.ID, keyPG) == 1 })
	close(release)
	a, b := refusalOf(t, <-errs), refusalOf(t, <-errs)
	if a.Code != CodeDiskShort || b.Code != CodeDiskShort || a.ID != b.ID || a.ID == 0 {
		t.Errorf("refusals = %+v, %+v; want the same disk_short", a, b)
	}
	rows, _, _ := e.st.ProjectBackups(context.Background(), e.shop.ID, 0, 10)
	if len(rows) != 1 {
		t.Errorf("rows = %d, want one refused row", len(rows))
	}
}

func TestEnqueueWaiterHonoursContext(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	e.r.sizeHook = func(context.Context, call) { once.Do(func() { close(entered); <-release }) }
	done := make(chan struct{})
	go func() { _, _ = e.m.Enqueue(context.Background(), e.shop, keyPG, actor); close(done) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := e.m.Enqueue(ctx, e.shop, keyPG, actor); errc <- err }()
	waitFor(t, "the waiter", func() bool { return e.m.waiters(e.shop.ID, keyPG) == 1 })
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	close(release)
	<-done
}

func TestFIFOAndQueuePosition(t *testing.T) {
	e := newEnv(t)
	hold := make(chan struct{})
	e.r.dump = func(ctx context.Context, c call, w io.Writer) ([]byte, error) {
		if c.v["service"] == "db" {
			select {
			case <-hold:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		_, err := w.Write(defaultDump(c))
		return nil, err
	}
	first := e.enqueue(t, keyPG)
	waitFor(t, "the first dump", func() bool { return len(e.r.callsOf(isDumpAction)) == 1 })
	second := e.enqueue(t, keyMongo)
	third := e.enqueue(t, keyMySQL)

	act := e.m.Active()
	if len(act) != 3 {
		t.Fatalf("active = %+v", act)
	}
	for i, want := range []struct {
		id    int64
		state string
		pos   int
	}{{first, "running", 0}, {second, "queued", 1}, {third, "queued", 2}} {
		if act[i].ID != want.id || act[i].State != want.state || act[i].QueuePosition != want.pos {
			t.Errorf("active[%d] = id %d %s pos %d, want %+v", i, act[i].ID, act[i].State, act[i].QueuePosition, want)
		}
	}
	if act[0].Steps[0].State != "running" || act[1].Steps[0].State != "waiting" {
		t.Errorf("steps = %+v / %+v", act[0].Steps, act[1].Steps)
	}
	close(hold)
	for _, id := range []int64{first, second, third} {
		if b := e.waitDone(t, id); b.State != "ok" {
			t.Errorf("backup %d = %s %s", id, b.State, b.Error)
		}
	}
	var order []string
	for _, c := range e.r.callsOf(isDumpAction) {
		order = append(order, c.v["service"])
	}
	if strings.Join(order, ",") != "db,mongo,mysql" {
		t.Errorf("dump order = %v, want FIFO", order)
	}
	if len(e.m.Active()) != 0 {
		t.Errorf("active = %+v", e.m.Active())
	}
}

func TestRetry(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	ctx := context.Background()
	e.hub.setState("db", "exited")
	_, err := e.m.Enqueue(ctx, e.shop, keyPG, actor)
	refused := refusalOf(t, err)
	e.hub.setState("db", "running")

	id, err := e.m.Retry(ctx, refused.ID, "other@example.com")
	if err != nil || id == 0 || id == refused.ID {
		t.Fatalf("Retry = %d, %v", id, err)
	}
	b, _ := e.st.GetBackup(ctx, id)
	if b.State != "queued" || b.DBKey != keyPG || b.StartedBy != "other@example.com" {
		t.Errorf("retried row = %+v", b)
	}
	// The retry is now queued: retrying it is not allowed, and a second retry of
	// the refused row is already_running.
	if _, err := e.m.Retry(ctx, id, actor); refusalOf(t, err).Code != CodeNotRetryable {
		t.Errorf("retry of a queued row: %v", err)
	}
	if _, err := e.m.Retry(ctx, refused.ID, actor); refusalOf(t, err).Code != CodeAlreadyRunning {
		t.Errorf("second retry: %v", err)
	}
	if _, err := e.m.Retry(ctx, 9999, actor); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}

	// An ok row is not retryable; a failed row whose database is gone is unknown.
	okID, err := e.st.InsertBackup(ctx, store.Backup{ProjectID: e.shop.ID, ProjectName: "Shop", DBType: "postgresql", DBName: "shop",
		DBLocationKind: "container", DBLocation: "db", DBKey: keyPG, State: "ok", StartedBy: actor, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Retry(ctx, okID, actor); refusalOf(t, err).Code != CodeNotRetryable {
		t.Errorf("retry of an ok row: %v", err)
	}
	goneID, err := e.st.InsertBackup(ctx, store.Backup{ProjectID: e.shop.ID, ProjectName: "Shop", DBType: "postgresql", DBName: "old",
		DBLocationKind: "container", DBLocation: "db", DBKey: "postgresql:db:old", State: "failed", StartedBy: actor, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Retry(ctx, goneID, actor); refusalOf(t, err).Code != CodeUnknownDatabase {
		t.Errorf("retry of a gone database: %v", err)
	} else if r := refusalOf(t, err); r.Words != "This database is no longer detected." {
		t.Errorf("words = %q", r.Words)
	}
	// An interrupted row of an untracked project is not_found.
	lostID, err := e.st.InsertBackup(ctx, store.Backup{ProjectID: 777, ProjectName: "Gone", DBType: "postgresql", DBName: "x",
		DBLocationKind: "container", DBLocation: "db", DBKey: "postgresql:db:x", State: "interrupted", StartedBy: actor, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Retry(ctx, lostID, actor); refusalOf(t, err).Code != CodeNotFound {
		t.Errorf("retry of an untracked project: %v", err)
	}
}

func TestStartupRemovesLeftoverTempFiles(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	tmp := filepath.Join(e.dataDir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backup-7.gz", "backup-12.gz", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.start(t)
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("left: %v", left)
	}
	if _, err := os.Stat(filepath.Join(tmp, "keep.txt")); err != nil {
		t.Errorf("another file was removed: %v", err)
	}
}

func TestPlaceholderNames(t *testing.T) {
	e := newEnv(t)
	b := e.waitDone(t, e.enqueue(t, keyPGPlace))
	if b.State != "ok" {
		t.Fatalf("row = %+v", b)
	}
	dumps := e.r.callsOf(isDumpAction)
	sizes := e.r.callsOf(func(a runner.Action) bool { return strings.Contains(string(a), ".size") })
	if len(dumps) != 1 || dumps[0].a != runner.DBPgDumpDefault || len(sizes) == 0 || sizes[0].a != runner.DBPgSizeDefault {
		t.Fatalf("dumps = %+v, sizes = %+v", dumps, sizes)
	}
	if _, ok := dumps[0].v["dbname"]; ok {
		t.Errorf("the default action got a dbname: %v", dumps[0].v)
	}
	if dumps[0].v["service"] != "pg2" {
		t.Errorf("service = %q", dumps[0].v["service"])
	}

	_, err := e.m.Enqueue(context.Background(), e.shop, keyMyPlace, actor)
	if r := refusalOf(t, err); r.Code != CodeNoDBName {
		t.Errorf("mysql placeholder: %+v", r)
	}

	// With an override file the .override actions run, with its path. The
	// worker re-reads the project, so the file is stored, as a rescan does.
	withOverride := e.shop
	withOverride.ComposeFiles = []string{"compose.yaml", "compose.override.yaml"}
	apply := store.RescanApply{Workspaces: []store.WorkspaceApply{{ID: e.shop.WorkspaceID, LastScanAt: time.Now(),
		Refresh: []store.ProjectRefresh{{Path: e.shop.Path, Config: &store.ConfigRefresh{
			ComposeFiles: withOverride.ComposeFiles, ComposeProject: e.shop.ComposeProject,
			Details: project.Details{Services: e.shop.ServiceDetails}, Databases: e.shop.Databases}}},
		Found: []string{e.shop.Path}}}}
	if err := e.st.ApplyRescan(context.Background(), apply, time.Now()); err != nil {
		t.Fatal(err)
	}
	id, err := e.m.Enqueue(context.Background(), withOverride, keyMongo, actor)
	if err != nil {
		t.Fatal(err)
	}
	e.waitDone(t, id)
	last := e.r.callsOf(isDumpAction)
	if c := last[len(last)-1]; c.a != runner.DBMongoDumpOverride || c.v["override"] != filepath.Join(e.shop.Path, "compose.override.yaml") || c.v["dbname"] != "notes" {
		t.Errorf("override dump = %+v", c)
	}
}

func TestServiceResolvedFromContainerName(t *testing.T) {
	e := newEnv(t)
	b := e.waitDone(t, e.enqueue(t, keyMySQL))
	if b.State != "ok" || b.DBLocation != "crm-mysql" || b.DBService != "mysql" || b.DBKey != keyMySQL {
		t.Fatalf("row = %+v", b)
	}
	dumps := e.r.callsOf(isDumpAction)
	if len(dumps) != 1 || dumps[0].a != runner.DBMySQLDump || dumps[0].v["service"] != "mysql" || dumps[0].v["dbname"] != "crm" {
		t.Errorf("dump = %+v", dumps)
	}
	if !strings.HasPrefix(b.Steps[0].Detail, "mysqldump · ") || !strings.HasSuffix(b.FileName, ".sql.gz") {
		t.Errorf("detail %q, file %q", b.Steps[0].Detail, b.FileName)
	}
	// Docker down: the stored container_name still resolves the service.
	e.hub.mu.Lock()
	e.hub.up, e.hub.cs = false, nil
	e.hub.mu.Unlock()
	_, err := e.m.Enqueue(context.Background(), e.shop, keyMySQL, actor)
	if r := refusalOf(t, err); r.Code != CodeDBNotRunning || r.Service != "mysql" || r.Words != "Start mysql first" {
		t.Errorf("refusal = %+v", r)
	}
}

func TestPublishThrottled(t *testing.T) {
	const every = 40 * time.Millisecond
	e := newEnv(t, envOpt{publishEach: every})
	type snap struct {
		at    time.Time
		steps string
	}
	var mu sync.Mutex
	var snaps []snap
	e.m.OnChange(func() {
		var steps string
		for _, b := range e.m.Active() {
			steps += b.State + ":" + stepStates(b) + ";"
		}
		mu.Lock()
		snaps = append(snaps, snap{time.Now(), steps})
		mu.Unlock()
	})
	e.r.dump = func(_ context.Context, _ call, w io.Writer) ([]byte, error) {
		end := time.Now().Add(10 * every)
		for time.Now().Before(end) {
			if _, err := w.Write([]byte("-- row\n")); err != nil {
				return nil, err
			}
			time.Sleep(time.Millisecond)
		}
		return nil, nil
	}
	id := e.enqueue(t, keyPG)
	e.waitDone(t, id)

	mu.Lock()
	defer mu.Unlock()
	progressOnly := 0
	for i := 1; i < len(snaps); i++ {
		// A state change publishes at once (and the Enqueue's and the worker's
		// first publishes may land in either order). Within a running step, only
		// progress publishes, from one goroutine at a time.
		if snaps[i].steps != snaps[i-1].steps || !strings.Contains(snaps[i].steps, "=running") {
			continue
		}
		progressOnly++
		if gap := snaps[i].at.Sub(snaps[i-1].at); gap < every-5*time.Millisecond {
			t.Errorf("progress published %s after the previous one, want at least %s", gap, every)
		}
	}
	if progressOnly < 3 {
		t.Errorf("progress publishes = %d over a %s dump, want several", progressOnly, 10*every)
	}
	if len(snaps) > 40 {
		t.Errorf("publishes = %d, want throttled", len(snaps))
	}
}

func TestDumpProgressDetail(t *testing.T) {
	e := newEnv(t, envOpt{publishEach: 10 * time.Millisecond})
	hold := make(chan struct{})
	e.r.dump = func(_ context.Context, _ call, w io.Writer) ([]byte, error) {
		if _, err := w.Write(bytes.Repeat([]byte("x"), 3<<20)); err != nil {
			return nil, err
		}
		<-hold
		return nil, nil
	}
	id := e.enqueue(t, keyPG)
	waitFor(t, "the read bytes in the detail", func() bool {
		for _, b := range e.m.Active() {
			if b.ID == id && strings.HasSuffix(b.Steps[0].Detail, "3.0 MB read") && strings.HasPrefix(b.Steps[0].Detail, "pg_dump · ") {
				return true
			}
		}
		return false
	})
	close(hold)
	e.waitDone(t, id)
}

func TestDiskRuleAndFallback(t *testing.T) {
	const mib = int64(1) << 20
	cases := []struct {
		name    string
		sizeOut string
		sizeErr error
		need    int64
	}{
		{"size read", fmt.Sprint(gib), nil, gib/2 + 100*mib},
		{"empty database", "0\n", nil, 100 * mib},
		{"size read fails", "", &runner.ExitError{Code: 1, Stderr: "psql: error"}, 500 * mib},
		{"size output is junk", "not a number\n", nil, 500 * mib},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, envOpt{noStart: true})
			e.r.sizeOut, e.r.sizeErr = c.sizeOut, c.sizeErr
			e.free.Store(c.need - 1)
			_, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
			r := refusalOf(t, err)
			if r.Code != CodeDiskShort || r.NeedBytes != c.need || r.FreeBytes != c.need-1 {
				t.Fatalf("refusal = %+v, want need %d", r, c.need)
			}
			want := "Needs about " + formatBytes(c.need) + " free, the server has " + formatBytes(c.need-1) + " left. Free up some disk space and try again."
			if r.Words != want {
				t.Errorf("words = %q, want %q", r.Words, want)
			}
			e.free.Store(c.need)
			if _, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor); err != nil {
				t.Errorf("with exactly the need free: %v", err)
			}
		})
	}
}

func TestCloseInterruptsRunningAndQueued(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	e.r.dump = func(ctx context.Context, _ call, w io.Writer) ([]byte, error) {
		_, _ = w.Write([]byte("-- partial\n"))
		<-ctx.Done()
		return nil, fmt.Errorf("docker db.pg.dump: %w", ctx.Err())
	}
	running := e.enqueue(t, keyPG)
	queued := e.enqueue(t, keyMongo)
	e.m.Start(context.Background())
	waitFor(t, "the dump", func() bool { return len(e.r.callsOf(isDumpAction)) == 1 })
	e.m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{running, queued} {
		b, _ := e.st.GetBackup(context.Background(), id)
		if b.State != "interrupted" || b.FinishedAt == nil || b.Error == "" {
			t.Errorf("backup %d = %+v", id, b)
		}
	}
	// No step is left running or waiting, the shape a reopen also leaves.
	if b, _ := e.st.GetBackup(context.Background(), running); stepStates(b) != "dump=failed compress=skipped upload=skipped prune=skipped" {
		t.Errorf("running backup steps = %s", stepStates(b))
	}
	if b, _ := e.st.GetBackup(context.Background(), queued); stepStates(b) != "dump=skipped compress=skipped upload=skipped prune=skipped" {
		t.Errorf("queued backup steps = %s", stepStates(b))
	}
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	// The running one finishes its started activity; the queued one, which never
	// started, records a finished one.
	acts := e.activity(t)
	if len(acts) != 2 || acts[0].Result != store.ResultInterrupted || acts[1].Result != store.ResultInterrupted {
		t.Errorf("activity = %+v", acts)
	}
	if len(e.m.Active()) != 0 {
		t.Errorf("active = %+v", e.m.Active())
	}
	if _, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor); !errors.Is(err, ErrClosed) {
		t.Errorf("Enqueue after Close: %v", err)
	}
}

func TestActiveIsACopy(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	id := e.enqueue(t, keyPG)
	a := e.m.Active()
	if len(a) != 1 || a[0].ID != id || a[0].QueuePosition != 1 || a[0].State != "queued" {
		t.Fatalf("active = %+v", a)
	}
	a[0].Steps[0].State = "mutated"
	if e.m.Active()[0].Steps[0].State != "waiting" {
		t.Error("Active shares its steps with the manager")
	}
	if e.changes.Load() == 0 {
		t.Error("Enqueue did not publish")
	}
}

func TestWorkerPanicRecovered(t *testing.T) {
	e := newEnv(t)
	e.r.dump = func(_ context.Context, c call, w io.Writer) ([]byte, error) {
		if c.v["service"] == "db" {
			_, _ = w.Write([]byte("-- partial\n"))
			panic("boom with " + devPass)
		}
		_, err := w.Write(defaultDump(c))
		return nil, err
	}
	first := e.enqueue(t, keyPG)
	second := e.enqueue(t, keyMongo)

	b := e.waitDone(t, first)
	if b.State != "failed" || b.FailedStep != "dump" || b.Error != msgInternal || b.Hint != "" || b.FinishedAt == nil {
		t.Fatalf("row = %+v", b)
	}
	if got := stepStates(b); got != "dump=failed compress=skipped upload=skipped prune=skipped" {
		t.Errorf("steps = %s", got)
	}
	if b.Steps[3].Detail != "temp file deleted" {
		t.Errorf("prune detail = %q", b.Steps[3].Detail)
	}
	if strings.Contains(b.Error, "boom") {
		t.Errorf("the panic reached the row: %q", b.Error)
	}
	if b2 := e.waitDone(t, second); b2.State != "ok" {
		t.Errorf("the next backup = %s %s, want ok", b2.State, b2.Error)
	}
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	acts := e.activity(t)
	if len(acts) != 2 {
		t.Fatalf("activity = %+v", acts)
	}
	for _, a := range acts {
		if a.Result == store.ResultRunning || a.FinishedAt == nil || strings.Contains(a.Reason, "boom") {
			t.Errorf("activity = %+v", a)
		}
	}
	if acts[1].Result != store.ResultFailed || acts[1].Reason != "Dump: "+msgInternal {
		t.Errorf("the panicked backup's activity = %+v", acts[1])
	}
}

func TestEnqueueCancelledDuringChecksRecordsNothing(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	e.r.sizeHook = func(context.Context, call) { once.Do(cancel) }
	// The cancelled size read would fall back to 500 MiB; this disk has less.
	e.free.Store(1)
	if _, err := e.m.Enqueue(ctx, e.shop, keyPG, actor); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	rows, _, _ := e.st.ProjectBackups(context.Background(), e.shop.ID, 0, 10)
	if len(rows) != 0 || len(e.activity(t)) != 0 {
		t.Errorf("a cancelled Enqueue recorded rows %+v, activity %+v", rows, e.activity(t))
	}
	e.free.Store(100 * gib)
	if _, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor); err != nil {
		t.Errorf("the reservation was not released: %v", err)
	}
}

func TestWaiterRetriesAfterCancelledHolder(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	holderCtx, cancelHolder := context.WithCancel(context.Background())
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	e.r.sizeHook = func(context.Context, call) {
		once.Do(func() {
			close(entered)
			<-release
			cancelHolder()
		})
	}
	holder := make(chan error, 1)
	go func() { _, err := e.m.Enqueue(holderCtx, e.shop, keyPG, actor); holder <- err }()
	<-entered
	type result struct {
		id  int64
		err error
	}
	waiter := make(chan result, 1)
	go func() {
		id, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
		waiter <- result{id, err}
	}()
	waitFor(t, "the waiter", func() bool { return e.m.waiters(e.shop.ID, keyPG) == 1 })
	close(release)
	if err := <-holder; !errors.Is(err, context.Canceled) {
		t.Errorf("holder = %v, want context.Canceled", err)
	}
	if r := <-waiter; r.err != nil || r.id == 0 {
		t.Errorf("waiter = %+v, want its own backup queued", r)
	}
}

func TestStartContextCancelClosesManager(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	ctx, cancel := context.WithCancel(context.Background())
	e.m.Start(ctx)
	cancel()
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wcancel()
	if err := e.m.Wait(wctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor); !errors.Is(err, ErrClosed) {
		t.Errorf("Enqueue after the worker stopped: %v, want ErrClosed", err)
	}
}

func TestPruneDetailWithoutTempFile(t *testing.T) {
	e := newEnv(t)
	ready := make(chan struct{})
	var n atomic.Int32
	e.r.sizeHook = func(context.Context, call) {
		if n.Add(1) == 2 { // the worker's re-check
			<-ready
		}
	}
	id := e.enqueue(t, keyPG)
	// A folder where the temporary file goes: opening it fails, even as root.
	if err := os.MkdirAll(filepath.Join(e.dataDir, "tmp", fmt.Sprintf("backup-%d.gz", id)), 0o700); err != nil {
		t.Fatal(err)
	}
	close(ready)
	b := e.waitDone(t, id)
	if b.State != "failed" || b.FailedStep != "dump" || !strings.HasPrefix(b.Error, "Could not create the temporary file: ") {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	if b.Steps[3].State != "skipped" || b.Steps[3].Detail != "" {
		t.Errorf("prune = %+v, want skipped with no temp file claim", b.Steps[3])
	}
}

// A PostgreSQL database whose name is not usable is told to set POSTGRES_DB,
// not the MySQL or MongoDB variables.
func TestNoDBNameWordsPerType(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	pg := e.shop
	pg.Databases = append(append([]project.Database(nil), e.shop.Databases...),
		project.Database{Type: "postgresql", Name: "-x", LocationKind: "container", Location: "db", Sources: []string{"service db"}})
	_, err := e.m.Enqueue(context.Background(), pg, "postgresql:db:-x", actor)
	r := refusalOf(t, err)
	if want := "The database name is not usable. Set POSTGRES_DB on the service, then rescan."; r.Code != CodeNoDBName || r.Words != want {
		t.Errorf("postgres refusal = %+v, want %q", r, want)
	}
	_, err = e.m.Enqueue(context.Background(), e.shop, keyMyPlace, actor)
	if r := refusalOf(t, err); r.Words != "The database name is not known. Set MYSQL_DATABASE (or MONGO_INITDB_DATABASE) on the service, then rescan." {
		t.Errorf("mysql words = %q", r.Words)
	}
}

// A second press while a backup of the database is queued answers
// already_running with its id, even when Drive has expired or been
// disconnected meanwhile, and writes no refused row.
func TestAlreadyRunningBeforeDriveCheck(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	id := e.enqueue(t, keyPG)
	if err := e.drv.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
	if r := refusalOf(t, err); got != 0 || r.Code != CodeAlreadyRunning || r.ID != id {
		t.Fatalf("Enqueue = %d, %+v; want already_running %d", got, r, id)
	}
	rows, _, _ := e.st.ProjectBackups(context.Background(), e.shop.ID, 0, 10)
	if len(rows) != 1 || len(e.activity(t)) != 0 {
		t.Errorf("rows = %+v, activity = %+v; want only the queued row", rows, e.activity(t))
	}
	// Another database still gets the Drive refusal.
	_, err = e.m.Enqueue(context.Background(), e.shop, keyMongo, actor)
	if r := refusalOf(t, err); r.Code != CodeDriveNotConnected {
		t.Errorf("other database = %+v", r)
	}
}

// A backup interrupted before its activity started (the worker's re-check
// cancelled, or Close while it was queued) still records one finished
// backup.run activity, interrupted.
func TestInterruptedWithoutStartRecordsActivity(t *testing.T) {
	t.Run("re-check cancelled", func(t *testing.T) {
		e := newEnv(t, envOpt{noStart: true})
		id := e.enqueue(t, keyPG)
		ctx, cancel := context.WithCancel(context.Background())
		var once sync.Once
		e.r.sizeHook = func(context.Context, call) { once.Do(cancel) } // the worker's size read
		e.m.Start(ctx)
		t.Cleanup(func() {
			e.m.Close()
			wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer wcancel()
			_ = e.m.Wait(wctx)
		})
		b := e.waitDone(t, id)
		if b.State != "interrupted" {
			t.Fatalf("row = %+v", b)
		}
		acts := e.activity(t)
		if len(acts) != 1 || acts[0].Result != store.ResultInterrupted || acts[0].FinishedAt == nil || acts[0].Reason != wordsInterrupted {
			t.Errorf("activity = %+v", acts)
		}
	})
	t.Run("closed while queued", func(t *testing.T) {
		e := newEnv(t, envOpt{noStart: true})
		id := e.enqueue(t, keyPG)
		e.m.Close()
		if b, _ := e.st.GetBackup(context.Background(), id); b.State != "interrupted" {
			t.Fatalf("row = %+v", b)
		}
		acts := e.activity(t)
		if len(acts) != 1 || acts[0].Result != store.ResultInterrupted || acts[0].FinishedAt == nil || acts[0].Target != "Shop · shop" {
			t.Errorf("activity = %+v", acts)
		}
	})
	t.Run("closed mid-Enqueue", func(t *testing.T) {
		e := newEnv(t, envOpt{noStart: true})
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		e.r.sizeHook = func(context.Context, call) { once.Do(func() { close(entered); <-release }) }
		done := make(chan int64, 1)
		go func() { id, _ := e.m.Enqueue(context.Background(), e.shop, keyPG, actor); done <- id }()
		<-entered
		e.m.Close()
		close(release)
		id := <-done
		if b, _ := e.st.GetBackup(context.Background(), id); id == 0 || b.State != "interrupted" {
			t.Fatalf("row %d = %+v", id, b)
		}
		acts := e.activity(t)
		if len(acts) != 1 || acts[0].Result != store.ResultInterrupted {
			t.Errorf("activity = %+v", acts)
		}
	})
}

// failCloseGzip is a compressor whose Close fails, as a full disk does on the
// last flush.
type failCloseGzip struct{ io.Writer }

func (failCloseGzip) Close() error { return errors.New("no space left on device") }

// A failed close of the compressor fails Compress, not Dump: the dump itself
// exited 0.
func TestCompressCloseFailureFailsCompress(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	e.m.compressor = func(w io.Writer) (io.WriteCloser, error) { return failCloseGzip{w}, nil }
	e.start(t)
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "failed" || b.FailedStep != "compress" || b.Error != "Could not compress the dump: no space left on device" {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	if got := stepStates(b); got != "dump=done compress=failed upload=skipped prune=skipped" {
		t.Errorf("steps = %s", got)
	}
	acts := e.activity(t)
	if len(acts) != 1 || acts[0].Reason != "Compress: Could not compress the dump: no space left on device" {
		t.Errorf("activity = %+v", acts)
	}
	if left := tempFiles(t, e.dataDir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// linkClient makes every upload answer link.
type linkClient struct {
	drive.Client
	link string
}

func (c linkClient) Session(refresh string) drive.Session {
	return linkSession{c.Client.Session(refresh), c.link}
}

type linkSession struct {
	drive.Session
	link string
}

func (s linkSession) Upload(ctx context.Context, folderID, name string, size int64, r io.Reader, progress func(int64)) (string, string, error) {
	id, _, err := s.Session.Upload(ctx, folderID, name, size, r, progress)
	return id, s.link, err
}

// Drive's link is stored only when it is an https link to Google Drive (or,
// for the fake, the panel's own origin); anything else is stored empty, as
// the UI renders it as an href.
func TestDriveLinkStoredOnlyWhenSafe(t *testing.T) {
	cases := []struct{ link, want string }{
		{"https://drive.google.com/file/d/abc/view?usp=drivesdk", "https://drive.google.com/file/d/abc/view?usp=drivesdk"},
		{"https://docs.google.com/document/d/abc/edit", "https://docs.google.com/document/d/abc/edit"},
		{"http://localhost:5173/settings#fake-drive-1", "http://localhost:5173/settings#fake-drive-1"},
		{"javascript:alert(document.cookie)", ""},
		{"http://drive.google.com/file/d/abc/view", ""},
		{"https://drive.google.com.evil.example/file", ""},
		{"https://user@drive.google.com/file", ""},
		{"https://evil.example/x", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.link, func(t *testing.T) {
			link := c.link
			e := newEnv(t, envOpt{client: func(cl drive.Client) drive.Client { return linkClient{cl, link} }})
			b := e.waitDone(t, e.enqueue(t, keyPG))
			if b.State != "ok" || b.DriveLink != c.want {
				t.Errorf("row = %s, link %q; want %q", b.State, b.DriveLink, c.want)
			}
		})
	}
}

// Bavail counts fragments: the free bytes use Frsize, not the I/O size Bsize.
func TestAvailBytesUsesFragmentSize(t *testing.T) {
	s := syscall.Statfs_t{Bavail: 1000, Bsize: 1 << 20, Frsize: 4096}
	if got := availBytes(&s); got != 1000*4096 {
		t.Errorf("availBytes = %d, want %d", got, 1000*4096)
	}
	s.Frsize = 0
	if got := availBytes(&s); got != 1000<<20 {
		t.Errorf("availBytes without Frsize = %d, want %d", got, 1000<<20)
	}
}
