// Package api builds the panel's HTTP router.
package api

import (
	"log/slog"
	"net/http"

	"github.com/daniel-vuky/server-control-panel/internal/auth"
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
}

// server holds what the handlers share.
type server struct {
	Deps
	log *slog.Logger
}

// NewRouter returns the panel's handler. Only GET /api/health is public; every
// other path needs a verified identity (ADR 0006). It panics when a dependency
// other than Version is nil, so a misconfigured server fails at startup rather
// than per request.
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
	} {
		if dep.nil {
			panic("api.NewRouter: Deps." + dep.name + " is nil")
		}
	}
	s := &server{Deps: d, log: slog.Default()}
	protect := auth.Require(d.Verifier)
	mux := http.NewServeMux()

	mux.Handle("GET /api/health", healthHandler(d.Version))
	mux.Handle("GET /api/me", protect(meHandler()))
	mux.Handle("POST /api/scans", protect(http.HandlerFunc(s.startScan)))
	mux.Handle("GET /api/scans/{id}", protect(http.HandlerFunc(s.getScan)))
	mux.Handle("GET /api/scans/{id}/events", protect(http.HandlerFunc(s.scanEvents)))
	mux.Handle("POST /api/scans/{id}/track", protect(http.HandlerFunc(s.trackScan)))
	mux.Handle("GET /api/workspaces", protect(http.HandlerFunc(s.listWorkspaces)))
	mux.Handle("GET /api/projects", protect(http.HandlerFunc(s.listProjects)))
	mux.Handle("/api/", protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})))
	mux.Handle("/", protect(d.Web))

	return mux
}

// internalError logs err and answers 500 without its text, which may name
// paths or database details.
func (s *server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "The panel could not complete this request.")
}
