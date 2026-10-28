//go:build dev

package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

func TestDefaultVerifierIsDevIdentityInDevBuild(t *testing.T) {
	for name, cfg := range map[string]config.Config{
		"empty":   {},
		"garbage": {AccessTeamDomain: "!!not a domain", AccessAUD: "zzz"},
	} {
		t.Run(name, func(t *testing.T) {
			v, err := DefaultVerifier(cfg)
			if err != nil {
				t.Fatalf("DefaultVerifier() error = %v", err)
			}
			id, err := v.Verify(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if id.Email != "dev@localhost" {
				t.Errorf("Email = %q, want dev@localhost", id.Email)
			}
		})
	}
}
