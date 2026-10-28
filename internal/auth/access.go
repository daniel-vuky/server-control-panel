package auth

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// accessHeader carries the Cloudflare Access JWT. Only this header is read,
// never the CF_Authorization cookie or the plain email header.
const accessHeader = "Cf-Access-Jwt-Assertion"

// keyFetchTimeout bounds one request for the team's key set.
const keyFetchTimeout = 5 * time.Second

var (
	teamDomainPattern = regexp.MustCompile(`^[a-z0-9-]+\.cloudflareaccess\.com$`)
	audPattern        = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// AccessVerifier is a Verifier for Cloudflare Access (ADR 0006). It accepts a
// request whose Cf-Access-Jwt-Assertion header holds an RS256 token signed by
// the team's keys, issued by the team and addressed to the application's AUD.
type AccessVerifier struct {
	issuer   string
	certsURL string
	keys     *keySet
	verifier *oidc.IDTokenVerifier
}

// NewAccessVerifier returns a verifier for the team domain and application
// AUD, both already normalised by NormalizeTeamDomain and NormalizeAUD. It
// makes no network call: keys are fetched when a token first needs them.
func NewAccessVerifier(teamDomain, aud string) *AccessVerifier {
	issuer := "https://" + teamDomain
	return newAccessVerifier(issuer, issuer+"/cdn-cgi/access/certs", aud, &http.Client{Timeout: keyFetchTimeout}, time.Now)
}

func newAccessVerifier(issuer, certsURL, aud string, client *http.Client, now func() time.Time) *AccessVerifier {
	ks := newKeySet(certsURL, client, now)
	return &AccessVerifier{
		issuer:   issuer,
		certsURL: certsURL,
		keys:     ks,
		verifier: oidc.NewVerifier(issuer, ks, &oidc.Config{ClientID: aud, Now: now}),
	}
}

// Verify returns the identity in the request's Access token. Every refusal
// wraps ErrNoIdentity and says why, without quoting the token.
func (v *AccessVerifier) Verify(r *http.Request) (Identity, error) {
	raw := strings.TrimSpace(r.Header.Get(accessHeader))
	if raw == "" {
		return Identity{}, fmt.Errorf("%w: no token", ErrNoIdentity)
	}
	tok, err := v.verifier.Verify(r.Context(), raw)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrNoIdentity, err)
	}
	var claims struct {
		Email *string `json:"email"`
	}
	if err := tok.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: read claims: %w", ErrNoIdentity, err)
	}
	if claims.Email == nil || *claims.Email == "" {
		return Identity{}, fmt.Errorf("%w: token has no email", ErrNoIdentity)
	}
	return Identity{Email: *claims.Email}, nil
}

// NormalizeTeamDomain turns the CF_ACCESS_TEAM_DOMAIN setting into a bare host
// such as myteam.cloudflareaccess.com. It accepts an optional https:// prefix
// and trailing slash, and rejects anything that is not a team domain.
func NormalizeTeamDomain(s string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(s))
	d = strings.TrimSuffix(strings.TrimPrefix(d, "https://"), "/")
	if !teamDomainPattern.MatchString(d) {
		return "", fmt.Errorf("team domain %q must look like myteam.cloudflareaccess.com", s)
	}
	return d, nil
}

// NormalizeAUD trims the CF_ACCESS_AUD setting and checks it is a hex string.
func NormalizeAUD(s string) (string, error) {
	aud := strings.TrimSpace(s)
	if aud == "" {
		return "", errors.New("application AUD is empty")
	}
	if !audPattern.MatchString(aud) {
		return "", errors.New("application AUD must be a hex string")
	}
	return aud, nil
}

var _ Verifier = (*AccessVerifier)(nil)
