//go:build !dev

package auth

import (
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

func TestDefaultVerifierRequiresAccessConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"empty team", config.Config{AccessAUD: "abc"}, "CF_ACCESS_TEAM_DOMAIN"},
		{"empty aud", config.Config{AccessTeamDomain: "t.cloudflareaccess.com"}, "CF_ACCESS_AUD"},
		{"malformed team", config.Config{AccessTeamDomain: "x.example.com", AccessAUD: "abc"}, "CF_ACCESS_TEAM_DOMAIN"},
		{"malformed aud", config.Config{AccessTeamDomain: "t.cloudflareaccess.com", AccessAUD: "xyz"}, "CF_ACCESS_AUD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := DefaultVerifier(tt.cfg)
			if err == nil {
				t.Fatalf("DefaultVerifier() = %v, want an error", v)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to name %s", err, tt.want)
			}
		})
	}
}

func TestDefaultVerifierBuildsAccessVerifier(t *testing.T) {
	v, err := DefaultVerifier(config.Config{AccessTeamDomain: "https://Team.cloudflareaccess.com/", AccessAUD: "abc"})
	if err != nil {
		t.Fatalf("DefaultVerifier() error = %v", err)
	}
	av, ok := v.(*AccessVerifier)
	if !ok {
		t.Fatalf("verifier = %T, want *AccessVerifier", v)
	}
	if av.issuer != "https://team.cloudflareaccess.com" {
		t.Errorf("issuer = %q, want https://team.cloudflareaccess.com", av.issuer)
	}
}
