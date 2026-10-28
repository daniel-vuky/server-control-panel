// Package api builds the panel's HTTP router.
package api

import (
	"net/http"

	"github.com/daniel-vuky/server-control-panel/internal/auth"
)

// Deps are the router's dependencies.
type Deps struct {
	Version  string
	Verifier auth.Verifier
	Web      http.Handler // serves the single-page app for every non-API path
}

// NewRouter returns the panel's handler. Only GET /api/health is public; every
// other path needs a verified identity (ADR 0006). It panics when Verifier or
// Web is nil, so a misconfigured server fails at startup rather than per request.
func NewRouter(d Deps) http.Handler {
	if d.Verifier == nil {
		panic("api.NewRouter: Deps.Verifier is nil")
	}
	if d.Web == nil {
		panic("api.NewRouter: Deps.Web is nil")
	}
	protect := auth.Require(d.Verifier)
	mux := http.NewServeMux()

	mux.Handle("GET /api/health", healthHandler(d.Version))
	mux.Handle("/api/", protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})))
	mux.Handle("/", protect(d.Web))

	return mux
}
