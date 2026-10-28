// Package auth is the seam between the panel and whatever proves who a request
// comes from. Release builds verify the Cloudflare Access token on every request
// (ADR 0006); dev builds use a fixed identity.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"
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

// maxReasonBytes bounds the refusal reason in the log: it can echo claim
// values the caller chose.
const maxReasonBytes = 256

// truncate cuts s to at most n bytes without splitting a rune and marks the cut
// with an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// Require wraps a handler so that it only runs for requests v accepts. The
// verified identity is available to the handler through FromContext. All other
// requests get 401 and the handler is not called; the refusal is logged with the
// request path and the reason, never the query string or the token.
func Require(v Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := v.Verify(r)
			if err != nil {
				slog.Warn("request refused", "path", r.URL.Path, "reason", truncate(err.Error(), maxReasonBytes))
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
