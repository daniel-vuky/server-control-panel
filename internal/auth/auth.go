// Package auth is the seam between the panel and whatever proves who a request
// comes from. Release builds reject everything until Cloudflare Access
// verification lands (ADR 0006, issue #10).
package auth

import (
	"context"
	"errors"
	"net/http"
)

// Identity is the person a request is attributed to.
type Identity struct {
	Email string
}

// ErrNoIdentity means the request carries no valid identity.
var ErrNoIdentity = errors.New("no valid identity")

// Verifier extracts a verified identity from a request.
type Verifier interface {
	Verify(r *http.Request) (Identity, error)
}

// RejectAll is a Verifier that never accepts a request.
type RejectAll struct{}

// Verify always returns ErrNoIdentity.
func (RejectAll) Verify(*http.Request) (Identity, error) { return Identity{}, ErrNoIdentity }

type ctxKey struct{}

// Require wraps a handler so that it only runs for requests v accepts. The
// verified identity is available to the handler through FromContext. All other
// requests get 401 and the handler is not called.
func Require(v Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := v.Verify(r)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}` + "\n"))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
		})
	}
}

// FromContext returns the identity Require stored in ctx.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}
