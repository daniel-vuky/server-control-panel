package api

import (
	"net/http"

	"github.com/daniel-vuky/server-control-panel/internal/auth"
)

// MeResponse is the body of GET /api/me.
type MeResponse struct {
	Email string `json:"email"`
}

func meHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeJSON(w, http.StatusOK, MeResponse{Email: id.Email})
	})
}
