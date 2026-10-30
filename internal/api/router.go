// Package api builds the panel's HTTP router.
package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/daniel-vuky/server-control-panel/internal/auth"
	"github.com/daniel-vuky/server-control-panel/internal/control"
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
		{"Control", d.Control == nil},
		{"Hub", d.Hub == nil},
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
	// Every state-changing POST needs a JSON body type (requireJSON), checked
	// after the identity.
	mux.Handle("POST /api/scans", protect(requireJSON(http.HandlerFunc(s.startScan))))
	mux.Handle("GET /api/scans/{id}", protect(http.HandlerFunc(s.getScan)))
	mux.Handle("GET /api/scans/{id}/events", protect(http.HandlerFunc(s.scanEvents)))
	mux.Handle("POST /api/scans/{id}/track", protect(requireJSON(http.HandlerFunc(s.trackScan))))
	mux.Handle("GET /api/workspaces", protect(http.HandlerFunc(s.listWorkspaces)))
	mux.Handle("GET /api/projects", protect(http.HandlerFunc(s.listProjects)))
	mux.Handle("POST /api/projects/{id}/{action}", protect(requireJSON(http.HandlerFunc(s.projectAction))))
	mux.Handle("GET /api/events", protect(http.HandlerFunc(s.events)))
	mux.Handle("POST /api/docker/check", protect(requireJSON(http.HandlerFunc(s.dockerCheck))))
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
	a.Actor, a.ActorKind = "", store.ActorPerson
	if id, ok := auth.FromContext(r.Context()); ok {
		a.Actor = id.Email
	}
	if err := s.Store.RecordActivity(context.WithoutCancel(r.Context()), a, result, reason); err != nil {
		s.log.Error("record activity", "action", a.Action, "result", result, "err", err)
	}
}

// internalError logs err and answers 500 without its text, which may name
// paths or database details.
func (s *server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "The panel could not complete this request.")
}
