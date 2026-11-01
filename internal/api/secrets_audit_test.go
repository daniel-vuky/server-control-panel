package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/alerts"
	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

// The audit fixture's secrets: one in .env, one written in compose
// `environment:` and one in an env_file. auditLeak quotes all three, as a
// command's output or error might.
const (
	auditDotenv  = "envsecret1"  //nolint:gosec // fake credential, the test subject
	auditLiteral = "literal22"   //nolint:gosec // fake credential, the test subject
	auditEnvFile = "filesecret3" //nolint:gosec // fake credential, the test subject
	auditLeak    = auditDotenv + " " + auditLiteral + " " + auditEnvFile
)

// auditTargets sends every alert to one sender.
type auditTargets struct{ s alerts.Sender }

func (a auditTargets) Targets() ([]alerts.Target, int) {
	return []alerts.Target{{Name: alerts.ChannelTelegram, Sender: a.s}}, 1
}

// TestNoComposeSecretInExitPoints drives a project whose secrets sit in .env,
// in compose `environment:` and in an env_file through every place a value
// could leave the panel, with each command's output quoting all three: the
// scan (its rows and a row warning), the projects, the log lines, a failed
// action, a failed backup and its alert text, and the activity rows. None may
// carry any of them, and each must show the mask, so the test exercises it.
func TestNoComposeSecretInExitPoints(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	f := dockerOf(d)
	ws := filepath.Join(rootOf(d), "ws")
	vault := filepath.Join(ws, "vault")
	mkProject(t, vault, "compose.yaml")
	writeAuditFile(t, filepath.Join(vault, ".env"), "POSTGRES_PASSWORD="+auditDotenv+"\n")
	writeAuditFile(t, filepath.Join(vault, "db.env"), "API_TOKEN="+auditEnvFile+"\n")
	f.setConfig(vault, configJSON("vault", map[string]service{"db": {
		Image: "postgres:16",
		// --no-env-resolution still interpolates: the .env value shows here.
		Environment: map[string]string{"POSTGRES_DB": "vault", "POSTGRES_PASSWORD": auditDotenv, "DB_PW": auditLiteral},
		EnvFile:     []envFile{{Path: filepath.Join(vault, "db.env"), Required: true}},
	}}))
	// A project whose config cannot be read: the scan's row warning quotes
	// compose's stderr. Before the first read only .env is known, so that is
	// what compose can quote there.
	broken := filepath.Join(ws, "broken")
	mkProject(t, broken, "compose.yaml")
	writeAuditFile(t, filepath.Join(broken, ".env"), "API_SECRET="+auditDotenv+"\n")
	f.set(func(f *fakeDocker) {
		f.configErrs[broken] = &runner.ExitError{Code: 1, Stderr: "yaml: invalid value " + auditDotenv}
	})

	check := func(where, body string) {
		t.Helper()
		for _, s := range []string{auditDotenv, auditLiteral, auditEnvFile} {
			if strings.Contains(body, s) {
				t.Errorf("%s carries %q: %s", where, s, body)
			}
		}
	}
	masked := func(where, body string) {
		t.Helper()
		check(where, body)
		if !strings.Contains(body, "••••") {
			t.Errorf("%s shows no mask: %s", where, body)
		}
	}

	// The scan, its rows and the row warning.
	scanID, job := scanDone(t, h, ws, 1)
	masked("scan", do(h, http.MethodGet, scanURL(scanID, "")).Body.String())
	var warned bool
	for _, r := range job.Result.Rows {
		warned = warned || strings.Contains(r.Warning, "••••")
	}
	if !warned {
		t.Fatalf("no masked row warning in %+v", job.Result.Rows)
	}
	if rec := doJSON(h, http.MethodPost, scanURL(scanID, "/track"), trackAll(job)); rec.Code != http.StatusOK {
		t.Fatalf("track = %d %s", rec.Code, rec.Body.String())
	}

	// The projects, with the detected database.
	rec := do(h, http.MethodGet, "/api/projects")
	check("projects", rec.Body.String())
	var id int64
	var key string
	for _, p := range decode[[]Project](t, rec) {
		if p.Path == vault && len(p.Databases) == 1 {
			id, key = p.ID, p.Databases[0].Key
		}
	}
	if id == 0 || key == "" {
		t.Fatalf("vault or its database is not listed: %s", rec.Body.String())
	}

	// The log lines (nothing runs: the backlog, then the end).
	f.set(func(f *fakeDocker) {
		f.containers = containerLine("vault", "db", "exited", "Exited (1) 1 minute ago")
		f.backlog = "db-1 | " + time.Now().UTC().Format(time.RFC3339Nano) + " boot " + auditLeak + "\n"
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	var lines strings.Builder
	for _, ev := range readSSE(t, srv.URL+logsURL(id, "")) {
		lines.WriteString(ev.kind + " " + ev.data + "\n")
	}
	masked("log lines", lines.String())

	// A failed action and its error.
	f.set(func(f *fakeDocker) {
		f.lifecycleErr = &runner.ExitError{Code: 1, Stderr: "Error response from daemon: " + auditLeak}
	})
	rec = doJSON(h, http.MethodPost, actionURL(id, "start"), "")
	if rec.Code != http.StatusOK || decode[ActionResult](t, rec).Outcome != "failed" {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	masked("action error", rec.Body.String())

	// A failed backup and every backup read.
	f.set(func(f *fakeDocker) {
		f.containers = containerLine("vault", "db", "running", "Up 1 hour")
		f.dumpErr = &runner.ExitError{Code: 1, Stderr: "pg_dump: error: " + auditLeak}
	})
	// A fresh Docker read, so the backup sees db running.
	if rec := doJSON(h, http.MethodPost, "/api/docker/check", ""); rec.Code != http.StatusOK {
		t.Fatalf("docker check = %d %s", rec.Code, rec.Body.String())
	}
	connectDrive(t, h, true)
	bid := startBackup(t, h, id, key)
	if b := waitBackup(t, h, bid); b.State != "failed" {
		t.Fatalf("backup = %+v, want failed", b)
	}
	masked("backup", do(h, http.MethodGet, backupURL(bid, "")).Body.String())
	masked("project backups", do(h, http.MethodGet, backupsURL(id)).Body.String())
	masked("all backups", do(h, http.MethodGet, "/api/backups").Body.String())
	for _, path := range []string{"/api/backups/summary", "/api/backups/latest"} {
		check(path, do(h, http.MethodGet, path).Body.String())
	}

	// The alert a scheduled run of that backup sends, built from its row.
	row, err := d.Store.GetBackup(context.Background(), bid)
	if err != nil {
		t.Fatal(err)
	}
	row.StartedBy = backup.ScheduleActor.Name
	senders := alerts.NewFakeFactory()
	n := alerts.NewNotifier(d.Store, auditTargets{senders.Telegram("", "")}, alerts.Frame{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	n.BackupEnded(row)
	waitFor(t, "the backup alert", func() bool { return len(senders.Outbox()) == 1 })
	n.Close()
	masked("alert text", senders.Outbox()[0].Text)

	// The activity rows: the scan, the track, the action, the backup and the alert.
	masked("activity", do(h, http.MethodGet, "/api/activity").Body.String())
}

func writeAuditFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
