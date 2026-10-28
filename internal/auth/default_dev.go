//go:build dev

package auth

import "net/http"

type devVerifier struct{}

func (devVerifier) Verify(*http.Request) (Identity, error) {
	return Identity{Email: "dev@localhost"}, nil
}

// DefaultVerifier is the verifier of a dev build: every request is the fixed
// identity dev@localhost. It exists only under the dev build tag, so release
// binaries cannot bypass verification (ADR 0006).
func DefaultVerifier() Verifier { return devVerifier{} }
