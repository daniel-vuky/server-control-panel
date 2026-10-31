// Package api builds the panel's HTTP router.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/auth"
	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/logs"
	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/roots"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// Deps are the router's dependencies.
type Deps struct {
	Version  string
	Verifier auth.Verifier
	Web      http.Handler // serves the single-page app for every non-API path

	Guard  *roots.Guard   // the allowed workspace folders
	Store  *store.Store   // the panel database
	Scans  *scan.Manager  // runs scan jobs
	Docker scan.Commander // reads container status for GET /api/projects

	Control *control.Controller // runs the lifecycle commands
	Hub     *control.Hub        // the live state behind GET /api/events
	Logs    *logs.Manager       // the shared log streams behind GET /api/projects/{id}/logs

	Drive   *drive.Service  // the Google Drive connection
	Backups *backup.Manager // queues and runs the backups
	Metrics Monitor         // the server metrics (*metrics.Monitor)

	// Zone is the server zone schedule times, next runs and the backup page's
	// days are read in (UTC when nil); Now is the clock (time.Now when nil).
	// Neither is required: main owns the scheduler that uses the same ones.
	Zone *time.Location
	Now  func() time.Time
}

// Monitor is what the server metrics routes read: *metrics.Monitor. Each call
// takes only the Monitor's own lock; a handler reads it and the hub or the
// store in separate steps.
type Monitor interface {
	Latest() (metrics.Tick, bool)
	Disk() map[int64]metrics.DiskUsage
	Disks() (disks []metrics.Disk, fallback bool)
	System(ctx context.Context) metrics.System
	Prune(ctx context.Context) error
	Thresholds() metrics.Thresholds
	Load() (one, five, fifteen float64, ok bool)
	BootTime() (time.Time, bool)
}

var _ Monitor = (*metrics.Monitor)(nil)

// server holds what the handlers share.
type server struct {
	Deps
	log *slog.Logger
}

// NewRouter returns the panel's handler. Only GET /api/health is public; every
// other path needs a verified identity (ADR 0006). It panics when a dependency
// other than Version, Zone or Now is nil, so a misconfigured server fails at
// startup rather than per request.
func NewRouter(d Deps) http.Handler {
	for _, dep := range []struct {
		name string
		nil  bool
	}{
		{"Verifier", d.Verifier == nil},
		{"Web", d.Web == nil},
		{"Guard", d.Guard == nil},
		{"Store", d.Store == nil},
		{"Scans", d.Scans == nil},
		{"Docker", d.Docker == nil},
		{"Control", d.Control == nil},
		{"Hub", d.Hub == nil},
		{"Logs", d.Logs == nil},
		{"Drive", d.Drive == nil},
		{"Backups", d.Backups == nil},
		{"Metrics", d.Metrics == nil},
	} {
		if dep.nil {
			panic("api.NewRouter: Deps." + dep.name + " is nil")
		}
	}
	if d.Zone == nil {
		d.Zone = time.UTC
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &server{Deps: d, log: slog.Default()}
	protect := auth.Require(d.Verifier)
	mux := http.NewServeMux()

	mux.Handle("GET /api/health", healthHandler(d.Version))
	mux.Handle("GET /api/me", protect(meHandler()))
	// Every state-changing POST needs a JSON body type (requireJSON), checked
	// after the identity.
	mux.Handle("POST /api/scans", protect(requireJSON(http.HandlerFunc(s.startScan))))
	mux.Handle("GET /api/scans/{id}", protect(http.HandlerFunc(s.getScan)))
	mux.Handle("GET /api/scans/{id}/events", protect(http.HandlerFunc(s.scanEvents)))
	mux.Handle("POST /api/scans/{id}/track", protect(requireJSON(http.HandlerFunc(s.trackScan))))
	mux.Handle("POST /api/rescans", protect(requireJSON(http.HandlerFunc(s.startRescan))))
	mux.Handle("GET /api/workspaces", protect(http.HandlerFunc(s.listWorkspaces)))
	mux.Handle("POST /api/workspaces/{id}/remove", protect(requireJSON(http.HandlerFunc(s.removeWorkspace))))
	mux.Handle("GET /api/projects", protect(http.HandlerFunc(s.listProjects)))
	// The literal untrack and settings routes win over {action}, which keeps
	// 404 for untrack, remove and settings as actions.
	mux.Handle("POST /api/projects/{id}/untrack", protect(requireJSON(http.HandlerFunc(s.untrackProject))))
	mux.Handle("POST /api/projects/{id}/settings", protect(requireJSON(http.HandlerFunc(s.projectSettings))))
	mux.Handle("GET /api/projects/{id}/logs", protect(http.HandlerFunc(s.projectLogs)))
	// The literal backups and backup-schedule segments win over {action} too,
	// so neither ever reaches projectAction.
	mux.Handle("POST /api/projects/{id}/backups", protect(requireJSON(http.HandlerFunc(s.enqueueBackup))))
	mux.Handle("GET /api/projects/{id}/backups", protect(http.HandlerFunc(s.projectBackups)))
	mux.Handle("POST /api/projects/{id}/backup-schedule", protect(requireJSON(http.HandlerFunc(s.backupSchedule))))
	// GET only, so a POST to metrics reaches {action} and is its 404.
	mux.Handle("GET /api/projects/{id}/metrics", protect(http.HandlerFunc(s.projectMetrics)))
	mux.Handle("POST /api/projects/{id}/{action}", protect(requireJSON(http.HandlerFunc(s.projectAction))))
	mux.Handle("GET /api/backup-settings", protect(http.HandlerFunc(s.backupSettings)))
	mux.Handle("GET /api/server", protect(http.HandlerFunc(s.serverInfo)))
	mux.Handle("GET /api/server/metrics", protect(http.HandlerFunc(s.serverMetrics)))
	mux.Handle("GET /api/server/usage", protect(http.HandlerFunc(s.serverUsage)))
	mux.Handle("GET /api/metrics-settings", protect(http.HandlerFunc(s.metricsSettings)))
	mux.Handle("POST /api/metrics-settings", protect(requireJSON(http.HandlerFunc(s.saveMetricsSettings))))
	mux.Handle("POST /api/backup-settings/defaults", protect(requireJSON(http.HandlerFunc(s.backupDefaults))))
	// The literal all, summary and latest segments win over {id}.
	mux.Handle("POST /api/backups/all", protect(requireJSON(http.HandlerFunc(s.backupAll))))
	mux.Handle("GET /api/backups", protect(http.HandlerFunc(s.listBackups)))
	mux.Handle("GET /api/backups/summary", protect(http.HandlerFunc(s.backupSummary)))
	mux.Handle("GET /api/backups/latest", protect(http.HandlerFunc(s.latestBackups)))
	mux.Handle("GET /api/backups/{id}", protect(http.HandlerFunc(s.getBackup)))
	mux.Handle("POST /api/backups/{id}/retry", protect(requireJSON(http.HandlerFunc(s.retryBackup))))
	mux.Handle("GET /api/backups/{id}/download", protect(http.HandlerFunc(s.downloadBackup)))
	mux.Handle("GET /api/events", protect(http.HandlerFunc(s.events)))
	mux.Handle("POST /api/docker/check", protect(requireJSON(http.HandlerFunc(s.dockerCheck))))
	mux.Handle("GET /api/drive", protect(http.HandlerFunc(s.driveStatus)))
	mux.Handle("POST /api/drive/connect", protect(requireJSON(http.HandlerFunc(s.driveConnect))))
	// Google redirects the browser here, so it is a GET with no JSON body; its
	// single-use state, bound to the actor, is the CSRF guard.
	mux.Handle("GET /api/drive/callback", protect(http.HandlerFunc(s.driveCallback)))
	mux.Handle("GET /api/drive/folders", protect(http.HandlerFunc(s.driveFolders)))
	mux.Handle("POST /api/drive/folder", protect(requireJSON(http.HandlerFunc(s.driveFolder))))
	mux.Handle("POST /api/drive/test", protect(requireJSON(http.HandlerFunc(s.driveTest))))
	mux.Handle("POST /api/drive/disconnect", protect(requireJSON(http.HandlerFunc(s.driveDisconnect))))
	// The fake Drive's consent page, in a dev build only.
	registerDevRoutes(mux, s, protect)
	mux.Handle("/api/", protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})))
	mux.Handle("/", protect(d.Web))

	return mux
}

// record stores one finished activity row for the verified identity of r. The
// actor comes only from auth.FromContext. A store error is logged and does not
// fail the request. The write outlives r's cancellation, so a client that goes
// away does not lose the row.
func (s *server) record(r *http.Request, a store.Activity, result, reason string) {
	a.Actor, a.ActorKind = actor(r), store.ActorPerson
	if err := s.Store.RecordActivity(context.WithoutCancel(r.Context()), a, result, reason); err != nil {
		s.log.Error("record activity", "action", a.Action, "result", result, "err", err)
	}
}

// actor is the verified email of r's identity, "" when there is none. It
// comes only from auth.FromContext.
func actor(r *http.Request) string {
	if id, ok := auth.FromContext(r.Context()); ok {
		return id.Email
	}
	return ""
}

// internalError logs err and answers 500 without its text, which may name
// paths or database details.
func (s *server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "The panel could not complete this request.")
}
