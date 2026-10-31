package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// alphaKey is the key of alpha's database (addProjects).
const alphaKey = "postgresql:db:alpha"

// ledgerUp is the docker.containers output of ledger with every service running.
func ledgerUp() string {
	return containerLine("ledger", "db", "running", "Up 1 hour") + containerLine("ledger", "maria", "running", "Up 1 hour")
}

// addLedger stores a second workspace with ledger: a PostgreSQL database
// (books), a MariaDB whose name is a placeholder, and a SQLite file. It
// returns ledger's id.
func addLedger(t *testing.T, d Deps) int64 {
	t.Helper()
	ws := filepath.Join(rootOf(d), "ws2")
	_, _, err := d.Store.AddWorkspace(context.Background(),
		store.NewWorkspace{Name: "ws2", Path: ws, Depth: 1, LastScanAt: time.Now()},
		[]store.NewProject{{
			Path: filepath.Join(ws, "ledger"), ComposeFiles: []string{"compose.yaml"}, ComposeProject: "ledger",
			DisplayName: "Ledger", Tracked: true, Details: services("db", "maria"),
			Databases: []project.Database{
				{Type: "postgresql", Name: "books", LocationKind: "container", Location: "db", Sources: []string{"service db"}},
				{Type: "mysql", Name: "maria", LocationKind: "container", Location: "maria", Sources: []string{"service maria"}},
				{Type: "sqlite", Name: "app.db", LocationKind: "file", Location: "/data/app.db", Sources: []string{"file"}},
			},
		}})
	if err != nil {
		t.Fatal(err)
	}
	ts, err := d.Store.TrackedProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ts {
		if p.DisplayName == "Ledger" {
			return p.ID
		}
	}
	t.Fatal("ledger is not tracked")
	return 0
}

// backupSetup is alpha and beta (addProjects) running, with Drive connected
// and a folder chosen. It returns alpha's id.
func backupSetup(t *testing.T) (Deps, http.Handler, int64) {
	t.Helper()
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	connectDrive(t, h, true)
	return d, h, alpha
}

func backupsURL(id int64) string { return "/api/projects/" + strconv.FormatInt(id, 10) + "/backups" }

func backupURL(id int64, suffix string) string {
	return "/api/backups/" + strconv.FormatInt(id, 10) + suffix
}

// startBackup posts a backup of key and returns its id.
func startBackup(t *testing.T, h http.Handler, projectID int64, key string) int64 {
	t.Helper()
	rec := doJSON(h, http.MethodPost, backupsURL(projectID), BackupRequest{Database: key})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("backup = %d %s", rec.Code, rec.Body.String())
	}
	return decode[BackupStarted](t, rec).ID
}

// waitBackup polls GET /api/backups/{id} until the backup is final.
func waitBackup(t *testing.T, h http.Handler, id int64) Backup {
	t.Helper()
	var b Backup
	waitFor(t, "the backup to finish", func() bool {
		b = decode[Backup](t, do(h, http.MethodGet, backupURL(id, "")))
		return b.State != "queued" && b.State != "running"
	})
	return b
}

// storedBackup inserts a finished backup row of alpha's database.
func storedBackup(t *testing.T, d Deps, projectID int64, state string) int64 {
	t.Helper()
	now := time.Now().UTC()
	b := store.Backup{
		ProjectID: projectID, ProjectName: "Alpha", DBType: "postgresql", DBName: "alpha", DBLocationKind: "container",
		DBLocation: "db", DBKey: alphaKey, State: state, StartedBy: "someone@example.com", CreatedAt: now, FinishedAt: &now,
		Steps: []store.BackupStep{{Step: "dump", State: "failed", Detail: "pg_dump"}},
	}
	if state == "failed" {
		b.FailedStep, b.Error = "dump", "boom"
	}
	id, err := d.Store.InsertBackup(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBackupEnqueueCodes(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, d Deps, h http.Handler) // after backupSetup
		path   func(alpha, ledger int64) string
		key    string
		status int
		want   BackupRefused // compared when status is 409
		field  string        // the 422 field
	}{
		{name: "unknown project", path: func(int64, int64) string { return backupsURL(999) }, key: alphaKey, status: http.StatusNotFound},
		{name: "bad id", path: func(int64, int64) string { return "/api/projects/abc/backups" }, key: alphaKey, status: http.StatusNotFound},
		{name: "unknown database", key: "postgresql:db:nope", status: http.StatusUnprocessableEntity, field: "database"},
		{name: "no database", key: "", status: http.StatusUnprocessableEntity, field: "database"},
		{
			name: "missing project", key: alphaKey, status: http.StatusConflict,
			setup: func(t *testing.T, d Deps, _ http.Handler) { markMissing(t, d) },
			want:  BackupRefused{Error: "not_found", Message: "This project is no longer tracked, or its folder is missing."},
		},
		{
			name: "sqlite", path: func(_, l int64) string { return backupsURL(l) }, key: "sqlite:/data/app.db:app.db", status: http.StatusConflict,
			want: BackupRefused{Error: "not_supported", Message: "SQLite backups are not supported yet"},
		},
		{
			name: "drive not connected", key: alphaKey, status: http.StatusConflict,
			setup: func(t *testing.T, _ Deps, h http.Handler) {
				if rec := doJSON(h, http.MethodPost, "/api/drive/disconnect", ""); rec.Code != http.StatusOK {
					t.Fatal(rec.Body.String())
				}
			},
			want: BackupRefused{Error: "drive_not_connected", Message: "Connect Google Drive first"},
		},
		{
			name: "drive expired", key: alphaKey, status: http.StatusConflict,
			setup: func(t *testing.T, d Deps, _ http.Handler) {
				ctx := context.Background()
				sess, _, err := d.Drive.Session(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := sess.Revoke(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := d.Drive.Folders(ctx); drive.CodeOf(err) != drive.CodeTokenExpired {
					t.Fatalf("Folders after revoke = %v", err)
				}
			},
			want: BackupRefused{Error: "drive_expired", Message: drive.Words(drive.CodeTokenExpired)},
		},
		{
			name: "folder missing", key: alphaKey, status: http.StatusConflict,
			setup: func(t *testing.T, _ Deps, h http.Handler) {
				doJSON(h, http.MethodPost, "/api/drive/disconnect", "")
				connectDrive(t, h, false)
			},
			want: BackupRefused{Error: "folder_missing", Message: drive.Words(drive.CodeFolderMissing)},
		},
		{
			name: "no db name", path: func(_, l int64) string { return backupsURL(l) }, key: "mysql:maria:maria", status: http.StatusConflict,
			want: BackupRefused{Error: "no_db_name", Service: "maria",
				Message: "The database name is not known. Set MYSQL_DATABASE (or MONGO_INITDB_DATABASE) on the service, then rescan."},
		},
		{
			name: "db not running", key: alphaKey, status: http.StatusConflict,
			setup: func(_ *testing.T, d Deps, _ http.Handler) {
				dockerOf(d).set(func(f *fakeDocker) {
					f.containers = containerLine("alpha", "web", "running", "Up") + containerLine("alpha", "db", "exited", "Exited (0)")
				})
			},
			want: BackupRefused{Error: "db_not_running", Message: "Start db first", Service: "db"},
		},
		{
			name: "disk short", key: alphaKey, status: http.StatusConflict,
			setup: func(_ *testing.T, d Deps, _ http.Handler) {
				dockerOf(d).set(func(f *fakeDocker) { f.dbSize = strconv.FormatInt(1<<62, 10) })
			},
			want: BackupRefused{Error: "disk_short", Service: "db"},
		},
		{name: "queued", key: alphaKey, status: http.StatusAccepted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, h, alpha := backupSetup(t)
			ledger := addLedger(t, d)
			dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() + ledgerUp() })
			if tc.setup != nil {
				tc.setup(t, d, h)
			}
			path := backupsURL(alpha)
			if tc.path != nil {
				path = tc.path(alpha, ledger)
			}
			rec := doJSON(h, http.MethodPost, path, BackupRequest{Database: tc.key})
			if rec.Code != tc.status {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
			}
			switch tc.status {
			case http.StatusAccepted:
				if id := decode[BackupStarted](t, rec).ID; id <= 0 {
					t.Errorf("id = %d", id)
				}
			case http.StatusUnprocessableEntity:
				fe := decode[FieldError](t, rec)
				if fe.Field != tc.field || fe.Code != "unknown" || fe.Error != "This database is no longer detected." {
					t.Errorf("field error = %+v", fe)
				}
			case http.StatusConflict:
				got := decode[BackupRefused](t, rec)
				if tc.want.Error == "disk_short" {
					if got.NeedBytes <= 1<<61 || got.FreeBytes <= 0 || got.Service != "db" || got.ID != 0 ||
						!strings.HasPrefix(got.Message, "Needs about ") || !strings.HasSuffix(got.Message, "Free up some disk space and try again.") {
						t.Errorf("disk_short = %+v", got)
					}
					return
				}
				if got != tc.want {
					t.Errorf("refusal = %+v, want %+v", got, tc.want)
				}
				if raw := decodeRaw(t, rec.Body.Bytes()); raw["id"] != nil {
					t.Errorf("body %s carries an id, want none outside already_running", rec.Body.String())
				}
			}
		})
	}
}

func TestBackupAlreadyRunning(t *testing.T) {
	d, h, alpha := backupSetup(t)
	dockerOf(d).holdDumps(t)

	id := startBackup(t, h, alpha, alphaKey)
	rec := doJSON(h, http.MethodPost, backupsURL(alpha), BackupRequest{Database: alphaKey})
	got := decode[BackupRefused](t, rec)
	if rec.Code != http.StatusConflict || got.Error != "already_running" || got.ID != id {
		t.Fatalf("second = %d %s, want 409 already_running with id %d", rec.Code, rec.Body.String(), id)
	}
	dockerOf(d).releaseDumps()
	if b := waitBackup(t, h, id); b.State != "ok" {
		t.Errorf("backup = %+v", b)
	}
}

func TestBackupRouteNeedsJSON(t *testing.T) {
	_, h, alpha := backupSetup(t)
	for _, path := range []string{backupsURL(alpha), backupURL(1, "/retry")} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"database":"`+alphaKey+`"}`))
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s = %d, want 415", path, rec.Code)
		}
	}
}

func TestBackupHistoryPagination(t *testing.T) {
	d, h, alpha := backupSetup(t)
	var ids []int64
	for range 5 {
		ids = append(ids, storedBackup(t, d, alpha, "failed"))
	}
	storedBackup(t, d, 999, "ok") // another project's

	rec := do(h, http.MethodGet, backupsURL(alpha)+"?limit=2")
	page := decode[BackupHistory](t, rec)
	if rec.Code != http.StatusOK || len(page.Backups) != 2 || !page.More || page.Backups[0].ID != ids[4] || page.Backups[1].ID != ids[3] {
		t.Fatalf("first page = %d %s", rec.Code, rec.Body.String())
	}
	page = decode[BackupHistory](t, do(h, http.MethodGet, backupsURL(alpha)+"?limit=2&before="+strconv.FormatInt(ids[3], 10)))
	if len(page.Backups) != 2 || !page.More || page.Backups[0].ID != ids[2] {
		t.Fatalf("second page = %+v", page)
	}
	page = decode[BackupHistory](t, do(h, http.MethodGet, backupsURL(alpha)+"?limit=2&before="+strconv.FormatInt(ids[1], 10)))
	if len(page.Backups) != 1 || page.More || page.Backups[0].ID != ids[0] {
		t.Fatalf("last page = %+v", page)
	}
	// The default page holds 50.
	if page := decode[BackupHistory](t, do(h, http.MethodGet, backupsURL(alpha))); len(page.Backups) != 5 || page.More {
		t.Errorf("default page = %+v", page)
	}
	// A project with none: an empty array.
	if rec := do(h, http.MethodGet, backupsURL(12345)); strings.TrimSpace(rec.Body.String()) != `{"backups":[],"more":false,"stored":{}}` {
		t.Errorf("empty = %s", rec.Body.String())
	}
	for _, q := range []string{"?limit=0", "?limit=x", "?limit=101", "?before=x", "?before=-1"} {
		if rec := do(h, http.MethodGet, backupsURL(alpha)+q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, rec.Code)
		}
	}
	if rec := do(h, http.MethodGet, "/api/projects/x/backups"); rec.Code != http.StatusNotFound {
		t.Errorf("bad id = %d, want 404", rec.Code)
	}
}

func TestBackupGet(t *testing.T) {
	d, h, alpha := backupSetup(t)
	ledger := addLedger(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() + ledgerUp() })

	for _, path := range []string{backupURL(999, ""), "/api/backups/x"} {
		if rec := do(h, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}

	stored := storedBackup(t, d, alpha, "failed")
	rec := do(h, http.MethodGet, backupURL(stored, ""))
	got := decode[Backup](t, rec)
	want := BackupDatabase{Type: "postgresql", Name: "alpha", LocationKind: "container", Location: "db", Key: alphaKey}
	if rec.Code != http.StatusOK || got.ID != stored || got.ProjectID != alpha || got.ProjectName != "Alpha" || got.Database != want ||
		got.State != "failed" || got.FailedStep != "dump" || got.Error != "boom" || got.StartedBy != "someone@example.com" ||
		got.CreatedAt == "" || got.FinishedAt == "" || got.StartedAt != "" || len(got.Steps) != 1 || got.QueuePosition != 0 {
		t.Errorf("stored = %d %s", rec.Code, rec.Body.String())
	}

	// An active backup is read from the manager, with its queue position.
	dockerOf(d).holdDumps(t)
	running := startBackup(t, h, alpha, alphaKey)
	waitFor(t, "the first backup to run", func() bool {
		return decode[Backup](t, do(h, http.MethodGet, backupURL(running, ""))).State == "running"
	})
	queued := startBackup(t, h, ledger, "postgresql:db:books")
	if b := decode[Backup](t, do(h, http.MethodGet, backupURL(queued, ""))); b.State != "queued" || b.QueuePosition != 1 || len(b.Steps) != 4 {
		t.Errorf("queued = %+v", b)
	}
	dockerOf(d).releaseDumps()
	if b := waitBackup(t, h, queued); b.State != "ok" || b.QueuePosition != 0 || b.FileName == "" || b.DriveLink == "" || b.SizeBytes <= 0 {
		t.Errorf("done = %+v", b)
	}
}

func TestBackupRetry(t *testing.T) {
	d, h, alpha := backupSetup(t)

	failed := storedBackup(t, d, alpha, "failed")
	rec := doJSON(h, http.MethodPost, backupURL(failed, "/retry"), "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry = %d %s", rec.Code, rec.Body.String())
	}
	id := decode[BackupStarted](t, rec).ID
	if id == failed {
		t.Errorf("retry reused the id")
	}
	if b := waitBackup(t, h, id); b.State != "ok" || b.StartedBy != "test@example.com" {
		t.Errorf("retried = %+v", b)
	}

	ok := storedBackup(t, d, alpha, "ok")
	rec = doJSON(h, http.MethodPost, backupURL(ok, "/retry"), "")
	if got := decode[BackupRefused](t, rec); rec.Code != http.StatusConflict || got.Error != "not_retryable" || got.ID != 0 {
		t.Errorf("retry ok = %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{backupURL(999, "/retry"), "/api/backups/x/retry"} {
		if rec := doJSON(h, http.MethodPost, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
	// A project no longer tracked: 404, as a new backup would be.
	gone := storedBackup(t, d, 999, "failed")
	if rec := doJSON(h, http.MethodPost, backupURL(gone, "/retry"), ""); rec.Code != http.StatusNotFound {
		t.Errorf("untracked = %d %s, want 404", rec.Code, rec.Body.String())
	}
	// A database no longer detected: 422.
	b := store.Backup{
		ProjectID: alpha, ProjectName: "Alpha", DBType: "mysql", DBName: "old", DBLocationKind: "container", DBLocation: "db",
		DBKey: "mysql:db:old", State: "failed", StartedBy: "x@example.com", CreatedAt: time.Now(),
	}
	oldID, err := d.Store.InsertBackup(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	rec = doJSON(h, http.MethodPost, backupURL(oldID, "/retry"), "")
	if fe := decode[FieldError](t, rec); rec.Code != http.StatusUnprocessableEntity || fe.Field != "database" || fe.Code != "unknown" ||
		fe.Error != "This database is no longer detected." {
		t.Errorf("gone database = %d %s", rec.Code, rec.Body.String())
	}
	// A refusal on retry is answered like a new backup's.
	dockerOf(d).set(func(f *fakeDocker) { f.containers = containerLine("alpha", "db", "exited", "Exited (0)") })
	d.Hub.Check(context.Background()) // read the containers again
	rec = doJSON(h, http.MethodPost, backupURL(failed, "/retry"), "")
	if got := decode[BackupRefused](t, rec); rec.Code != http.StatusConflict || got.Error != "db_not_running" || got.Service != "db" || got.ID != 0 {
		t.Errorf("retry refused = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBackupDownloadHeadersAndStream(t *testing.T) {
	d, h, alpha := backupSetup(t)
	id := startBackup(t, h, alpha, alphaKey)
	b := waitBackup(t, h, id)
	if b.State != "ok" {
		t.Fatalf("backup = %+v", b)
	}

	rec := do(h, http.MethodGet, backupURL(id, "/download"))
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="`+b.FileName+`"` {
		t.Errorf("Content-Disposition = %q, want the file name %s", cd, b.FileName)
	}
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := io.ReadAll(zr); err != nil || !bytes.Equal(raw, dumpData) {
		t.Errorf("content = %q (%v), want the dump", raw, err)
	}
	for _, r := range activity(t, d) {
		if r.Action != store.ActBackupRun && r.Action != store.ActDriveConnect && r.Action != store.ActDriveFolder {
			t.Errorf("activity %+v, want the download unrecorded", r)
		}
	}
}

// failClient is a Drive client whose downloads deliver some bytes and then
// fail.
type failClient struct{ drive.Client }

func (c failClient) Session(refresh string) drive.Session {
	return failSession{c.Client.Session(refresh)}
}

type failSession struct{ drive.Session }

func (s failSession) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	rc, err := s.Session.Download(ctx, fileID)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(io.MultiReader(io.LimitReader(rc, 10), errReader{})), nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, &drive.Error{Code: drive.CodeUnreachable, Err: errors.New("cut")}
}

// wantPage checks that a download failure is a readable HTML page with
// status, the words and a link back, not a JSON body a browser would save.
func wantPage(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, words, back string) {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != status || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("%s = %d %q, want %d text/html", what, rec.Code, rec.Header().Get("Content-Type"), status)
	}
	if rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("%s has Content-Disposition %q", what, rec.Header().Get("Content-Disposition"))
	}
	if !strings.Contains(body, html.EscapeString(words)) || !strings.Contains(body, `href="`+back+`"`) {
		t.Errorf("%s body = %s, want %q and a link to %s", what, body, words, back)
	}
}

func TestBackupDownloadNotOK(t *testing.T) {
	d, h, alpha := backupSetup(t)
	back := "/projects/" + strconv.FormatInt(alpha, 10) + "/database"

	wantPage(t, "unknown", do(h, http.MethodGet, backupURL(999, "/download")), http.StatusNotFound, "This backup does not exist.", "/backups")
	failed := storedBackup(t, d, alpha, "failed")
	wantPage(t, "failed row", do(h, http.MethodGet, backupURL(failed, "/download")), http.StatusConflict,
		"Only a backup that succeeded can be downloaded.", back)

	// An ok row whose file is gone from Drive: 502 with the Drive words.
	now := time.Now()
	gone, err := d.Store.InsertBackup(context.Background(), store.Backup{
		ProjectID: alpha, ProjectName: "Alpha", DBType: "postgresql", DBName: "alpha", DBLocationKind: "container",
		DBLocation: "db", DBKey: alphaKey, State: "ok", FileName: "x.sql.gz", DriveFileID: "nope", StartedBy: "x@example.com",
		CreatedAt: now, FinishedAt: &now,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPage(t, "gone file", do(h, http.MethodGet, backupURL(gone, "/download")), http.StatusBadGateway,
		drive.Words(drive.CodeFolderMissing), back)

	// Drive not connected.
	doJSON(h, http.MethodPost, "/api/drive/disconnect", "")
	wantPage(t, "not connected", do(h, http.MethodGet, backupURL(gone, "/download")), http.StatusConflict,
		"Connect Google Drive first", back)
}

func TestBackupDownloadCutAfterFirstByte(t *testing.T) {
	d := newTestDepsWith(t, func(c drive.Client) drive.Client { return failClient{c} })
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	connectDrive(t, h, true)
	id := startBackup(t, h, alpha, alphaKey)
	if b := waitBackup(t, h, id); b.State != "ok" {
		t.Fatalf("backup = %+v", b)
	}

	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + backupURL(id, "/download"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the failure comes after the first byte)", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Errorf("the body ended cleanly after %d bytes, want the connection cut", len(body))
	}
}

// cancelClient's downloads deliver some bytes, then end as the request's own
// context does when the browser goes away: the reader cancels it and fails.
type cancelClient struct {
	drive.Client
	cancel *context.CancelFunc
}

func (c cancelClient) Session(refresh string) drive.Session {
	return cancelSession{Session: c.Client.Session(refresh), cancel: c.cancel}
}

type cancelSession struct {
	drive.Session
	cancel *context.CancelFunc
}

func (s cancelSession) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	rc, err := s.Session.Download(ctx, fileID)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(io.MultiReader(io.LimitReader(rc, 10), cancelReader{s.cancel})), nil
}

type cancelReader struct{ cancel *context.CancelFunc }

func (r cancelReader) Read([]byte) (int, error) {
	(*r.cancel)()
	return 0, context.Canceled
}

// A download the client cancels is not a warning: only a Drive failure is.
func TestBackupDownloadClientCancelIsNotAWarning(t *testing.T) {
	var cancel context.CancelFunc
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	d := newTestDepsWith(t, func(c drive.Client) drive.Client { return cancelClient{Client: c, cancel: &cancel} })
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	connectDrive(t, h, true)
	id := startBackup(t, h, alpha, alphaKey)
	if b := waitBackup(t, h, id); b.State != "ok" {
		t.Fatalf("backup = %+v", b)
	}

	logs.Reset()
	ctx, c := context.WithCancel(context.Background())
	defer c()
	cancel = c
	req := httptest.NewRequest(http.MethodGet, backupURL(id, "/download"), nil).WithContext(ctx)
	func() {
		defer func() {
			if p := recover(); p != nil {
				if err, ok := p.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
					panic(p)
				}
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	if strings.Contains(logs.String(), "the download stopped") {
		t.Errorf("a client cancel was logged as a warning: %s", logs.String())
	}
}

func TestBackupsRouteNeverHitsProjectAction(t *testing.T) {
	d, h, alpha := backupSetup(t)
	before := len(dockerOf(d).lifecycleRuns)

	rec := doJSON(h, http.MethodPost, backupsURL(alpha), BackupRequest{Database: alphaKey})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST backups = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, backupsURL(alpha)); rec.Code != http.StatusOK {
		t.Errorf("GET backups = %d %s", rec.Code, rec.Body.String())
	}
	dockerOf(d).set(func(f *fakeDocker) {
		if len(f.lifecycleRuns) != before {
			t.Errorf("a lifecycle command ran: %+v", f.lifecycleRuns)
		}
	})
	for _, r := range activity(t, d) {
		if strings.HasPrefix(r.Action, "project.") {
			t.Errorf("activity %+v, want no project action", r)
		}
	}
	// The other actions still reach projectAction.
	if rec := doJSON(h, http.MethodPost, actionURL(alpha, "backup"), ""); rec.Code != http.StatusNotFound {
		t.Errorf("POST .../backup = %d, want 404", rec.Code)
	}
}

func TestLiveStateCarriesBackupsAndDrive(t *testing.T) {
	d, h, alpha := backupSetup(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	s := nextState(t, r)
	if s.Drive.State != "connected" || s.Drive.Email != drive.FakeEmail || s.Drive.Folder == nil || s.Drive.Folder.Name != "server-backups" {
		t.Errorf("drive = %+v", s.Drive)
	}
	if s.Backups == nil || len(s.Backups) != 0 {
		t.Errorf("backups = %#v, want []", s.Backups)
	}
	db := s.Projects[0].Databases[0]
	if db.Key != alphaKey || db.Backup == nil || *db.Backup != (BackupRule{Allowed: true, Service: "db"}) {
		t.Errorf("alpha database = %+v", db)
	}

	dockerOf(d).holdDumps(t)
	id := startBackup(t, h, alpha, alphaKey)
	s = stateUntil(t, r, "the backup running", func(s LiveState) bool {
		return len(s.Backups) == 1 && s.Backups[0].State == "running"
	})
	if b := s.Backups[0]; b.ID != id || b.ProjectID != alpha || b.Database.Key != alphaKey || b.QueuePosition != 0 || len(b.Steps) != 4 {
		t.Errorf("active backup = %+v", b)
	}
	dockerOf(d).releaseDumps()
	stateUntil(t, r, "no active backup", func(s LiveState) bool { return len(s.Backups) == 0 })

	doJSON(h, http.MethodPost, "/api/drive/disconnect", "")
	s = stateUntil(t, r, "drive disconnected", func(s LiveState) bool { return s.Drive.State == "not_connected" })
	if b := s.Projects[0].Databases[0].Backup; b == nil || *b != (BackupRule{Reason: "drive_not_connected", Service: "db"}) {
		t.Errorf("rule after disconnect = %+v", b)
	}
}

func TestDatabaseBackupNullOnScanRowsSetOnProjects(t *testing.T) {
	_, h, _ := backupSetup(t)
	rec := do(h, http.MethodGet, "/api/projects")
	got := decode[[]Project](t, rec)
	if db := got[0].Databases[0]; db.Key != alphaKey || db.Backup == nil || !db.Backup.Allowed {
		t.Errorf("project database = %+v", db)
	}
	rows := toDatabases([]project.Database{{Type: "postgresql", Name: "x", LocationKind: "container", Location: "db"}})
	if rows[0].Key != "postgresql:db:x" || rows[0].Backup != nil {
		t.Errorf("scan row = %+v", rows[0])
	}
}

// recordClient records the refresh tokens the fake Drive issues.
type recordClient struct {
	drive.Client
	refresh []string
}

func (c *recordClient) Exchange(ctx context.Context, code, redirect string) (string, error) {
	r, err := c.Client.Exchange(ctx, code, redirect)
	if err == nil {
		c.refresh = append(c.refresh, r)
	}
	return r, err
}

func TestNoTokenInAnyJSON(t *testing.T) {
	rc := &recordClient{}
	d := newTestDepsWith(t, func(c drive.Client) drive.Client { rc.Client = c; return rc })
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })

	var bodies []string
	keep := func(rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		bodies = append(bodies, rec.Body.String(), rec.Header().Get("Location"))
		return rec
	}
	state := startConnect(t, h, "/settings")
	keep(callback(h, url.Values{"state": {state}, "code": {"fake-" + state}}))
	keep(doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{Name: "server-backups"}))
	keep(doJSON(h, http.MethodPost, "/api/drive/test", ""))
	keep(do(h, http.MethodGet, "/api/drive"))
	keep(do(h, http.MethodGet, "/api/drive/folders"))
	keep(doJSON(h, http.MethodPost, "/api/drive/connect", DriveConnectRequest{ReturnTo: "/settings"}))
	id := startBackup(t, h, alpha, alphaKey)
	waitBackup(t, h, id)
	keep(do(h, http.MethodGet, backupURL(id, "")))
	keep(do(h, http.MethodGet, backupsURL(alpha)))
	keep(do(h, http.MethodGet, "/api/projects"))
	srv := httptest.NewServer(h)
	defer srv.Close()
	r, closeFn := openSSE(t, srv.URL+"/api/events")
	s := nextState(t, r)
	closeFn()
	if s.Drive.State != "connected" {
		t.Fatalf("state drive = %+v", s.Drive)
	}
	ev, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	bodies = append(bodies, string(ev))
	row, ok, err := d.Store.Drive(context.Background())
	if err != nil || !ok || !strings.HasPrefix(row.RefreshTokenEnc, "v1:") {
		t.Fatalf("drive row = %+v, %v, %v", row, ok, err)
	}
	keep(doJSON(h, http.MethodPost, "/api/drive/disconnect", ""))
	for _, a := range activity(t, d) {
		bodies = append(bodies, a.Target, a.Reason)
	}

	if len(rc.refresh) != 1 || rc.refresh[0] == "" {
		t.Fatalf("refresh tokens = %v", rc.refresh)
	}
	secrets := []string{
		rc.refresh[0], strings.TrimPrefix(rc.refresh[0], "fake-refresh-"),
		row.RefreshTokenEnc, strings.TrimPrefix(row.RefreshTokenEnc, "v1:"),
	}
	for i, b := range bodies {
		for _, sec := range secrets {
			if strings.Contains(b, sec) {
				t.Errorf("body %d holds a token: %s", i, b)
			}
		}
	}
}

func TestProjectBackupsStored(t *testing.T) {
	d, h, alpha := backupSetup(t)
	ok := startBackup(t, h, alpha, alphaKey)
	if b := waitBackup(t, h, ok); b.State != "ok" || b.PrunedAt != nil {
		t.Fatalf("backup = %+v", b)
	}
	// A pruned copy and a failed row without a file do not count.
	pruned := insertAt(t, d, alpha, "ok", time.Now().UTC(), func(b *store.Backup) { b.DriveFileID = "old" })
	if err := d.Store.MarkPruned(context.Background(), pruned, time.Now()); err != nil {
		t.Fatal(err)
	}
	storedBackup(t, d, alpha, "failed")

	page := decode[BackupHistory](t, do(h, http.MethodGet, backupsURL(alpha)))
	if len(page.Stored) != 1 || page.Stored[alphaKey] != 1 {
		t.Errorf("stored = %v, want %s: 1", page.Stored, alphaKey)
	}
	for _, b := range page.Backups {
		if (b.ID == pruned) != (b.PrunedAt != nil) {
			t.Errorf("backup %d prunedAt = %v", b.ID, b.PrunedAt)
		}
	}
}

func TestDownloadPrunedIs410Page(t *testing.T) {
	d, h, alpha := backupSetup(t)
	id := startBackup(t, h, alpha, alphaKey)
	if b := waitBackup(t, h, id); b.State != "ok" {
		t.Fatalf("backup = %+v", b)
	}
	if err := d.Store.MarkPruned(context.Background(), id, time.Now()); err != nil {
		t.Fatal(err)
	}
	wantPage(t, "pruned", do(h, http.MethodGet, backupURL(id, "/download")), http.StatusGone,
		"This copy was removed from Drive by retention.", "/projects/"+strconv.FormatInt(alpha, 10)+"/database")
}

func TestDownloadRawContentType(t *testing.T) {
	_, h, alpha := backupSetup(t)
	saveSchedule(t, h, alpha, BackupScheduleInput{Database: alphaKey, Frequency: "off", Time: "02:00", Keep: 14, Compress: false})
	id := startBackup(t, h, alpha, alphaKey)
	b := waitBackup(t, h, id)
	if b.State != "ok" || !strings.HasSuffix(b.FileName, ".sql") {
		t.Fatalf("backup = %+v", b)
	}
	rec := do(h, http.MethodGet, backupURL(id, "/download"))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="`+b.FileName+`"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !bytes.Equal(rec.Body.Bytes(), dumpData) {
		t.Errorf("content = %q, want the raw dump", rec.Body.Bytes())
	}
}

func TestSafeFileNameFallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		compressed bool
		want       string
	}{
		{"", true, "backup.gz"},
		{"", false, "backup.sql"},
		{"a\"b.sql", false, "a_b.sql"},
		{"x.sql.gz", true, "x.sql.gz"},
	} {
		if got := safeFileName(tc.name, tc.compressed); got != tc.want {
			t.Errorf("safeFileName(%q, %v) = %q, want %q", tc.name, tc.compressed, got, tc.want)
		}
	}
	if got := contentTypeOf("x.sql.gz"); got != "application/gzip" {
		t.Errorf("gz = %q", got)
	}
	if got := contentTypeOf("x.archive"); got != "application/octet-stream" {
		t.Errorf("archive = %q", got)
	}
}

func TestLatestBackupsRoute(t *testing.T) {
	d, h, alpha := scheduleSetup(t, mustZone(t, "Asia/Ho_Chi_Minh"))
	ledger := addLedger(t, d)

	// No backup at all: a null latest and an empty list, not null.
	rec := do(h, http.MethodGet, "/api/backups/latest")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"latest":null,"projects":[]}` {
		t.Fatalf("empty latest = %d %s", rec.Code, rec.Body.String())
	}

	// Alpha's newest ended backup counts, not the running one after it; the
	// ledger's refused row is the highest id.
	insertAt(t, d, alpha, "ok", time.Date(2030, 1, 14, 1, 0, 0, 0, time.UTC), nil)
	failed := insertAt(t, d, alpha, "failed", time.Date(2030, 1, 15, 1, 0, 0, 0, time.UTC), nil)
	insertAt(t, d, alpha, "running", time.Date(2030, 1, 15, 2, 0, 0, 0, time.UTC), nil)
	refused := insertAt(t, d, ledger, "refused", time.Date(2030, 1, 15, 3, 0, 0, 0, time.UTC), func(b *store.Backup) {
		b.ProjectName, b.DBName = "Ledger", "books"
	})

	rec = do(h, http.MethodGet, "/api/backups/latest")
	if rec.Code != http.StatusOK {
		t.Fatalf("latest = %d %s", rec.Code, rec.Body.String())
	}
	got := decode[LatestBackups](t, rec)
	wantAlpha := LatestBackup{ID: failed, ProjectID: alpha, ProjectName: "Alpha", Database: "alpha", State: "failed", At: "2030-01-15T01:00:00Z"}
	wantLedger := LatestBackup{ID: refused, ProjectID: ledger, ProjectName: "Ledger", Database: "books", State: "refused", At: "2030-01-15T03:00:00Z"}
	if got.Latest == nil || *got.Latest != wantLedger {
		t.Errorf("latest = %+v, want %+v", got.Latest, wantLedger)
	}
	if !reflect.DeepEqual(got.Projects, []LatestBackup{wantLedger, wantAlpha}) {
		t.Errorf("projects = %+v, want ledger then alpha", got.Projects)
	}
}

func TestLatestBackupsLiteralWinsOverID(t *testing.T) {
	_, h, _ := scheduleSetup(t, time.UTC)

	// GET is the literal route (getBackup would answer 404 for "latest").
	rec := do(h, http.MethodGet, "/api/backups/latest")
	if raw := decodeRaw(t, rec.Body.Bytes()); rec.Code != http.StatusOK || raw["projects"] == nil {
		t.Errorf("GET /api/backups/latest = %d %s", rec.Code, rec.Body.String())
	}
	// Only GET is routed: a POST answers as a POST to summary does.
	want := doJSON(h, http.MethodPost, "/api/backups/summary", "")
	got := doJSON(h, http.MethodPost, "/api/backups/latest", "")
	if got.Code != want.Code || got.Body.String() != want.Body.String() {
		t.Errorf("POST latest = %d %s, POST summary = %d %s", got.Code, got.Body.String(), want.Code, want.Body.String())
	}
}
