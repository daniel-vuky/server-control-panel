//go:build !dev

package auth

import (
	"fmt"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// DefaultVerifier is the verifier a release build starts with: Cloudflare Access
// token verification (ADR 0006). It fails when CF_ACCESS_TEAM_DOMAIN or
// CF_ACCESS_AUD is missing or malformed, so the panel never starts unprotected.
func DefaultVerifier(cfg config.Config) (Verifier, error) {
	team, err := NormalizeTeamDomain(cfg.AccessTeamDomain)
	if err != nil {
		return nil, fmt.Errorf("CF_ACCESS_TEAM_DOMAIN: %w", err)
	}
	aud, err := NormalizeAUD(cfg.AccessAUD)
	if err != nil {
		return nil, fmt.Errorf("CF_ACCESS_AUD: %w", err)
	}
	return NewAccessVerifier(team, aud), nil
}
