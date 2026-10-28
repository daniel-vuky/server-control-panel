package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testIssuer = "https://team.cloudflareaccess.com"

func newTestVerifier(t *testing.T) (*AccessVerifier, *jwksServer, testKey) {
	t.Helper()
	k1 := newTestKey(t, "k1")
	jwks := newJWKSServer(t, k1)
	return newAccessVerifier(testIssuer, jwks.URL, "abc123", jwks.Client(), time.Now), jwks, k1
}

func validClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":   testIssuer,
		"aud":   []string{"abc123"},
		"exp":   now.Add(time.Hour).Unix(),
		"iat":   now.Unix(),
		"email": "op@example.com",
		"sub":   "user-1",
	}
}

func withClaims(mutate func(map[string]any)) map[string]any {
	c := validClaims()
	mutate(c)
	return c
}

func requestWithToken(rawJWT string, set bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	if set {
		r.Header.Set("Cf-Access-Jwt-Assertion", rawJWT)
	}
	return r
}

func TestAccessVerifierAcceptsValidToken(t *testing.T) {
	v, _, k1 := newTestVerifier(t)

	id, err := v.Verify(requestWithToken(k1.sign(t, validClaims()), true))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id != (Identity{Email: "op@example.com"}) {
		t.Errorf("identity = %+v", id)
	}
}

func TestAccessVerifierRejects(t *testing.T) {
	v, _, k1 := newTestVerifier(t)
	k9 := newTestKey(t, "k9")

	tests := []struct {
		name   string
		rawJWT string
		set    bool
	}{
		{"no header", "", false},
		{"blank header", "   ", true},
		{"expired", k1.sign(t, withClaims(func(c map[string]any) { c["exp"] = time.Now().Add(-2 * time.Hour).Unix() })), true},
		{"wrong audience", k1.sign(t, withClaims(func(c map[string]any) { c["aud"] = []string{"other"} })), true},
		{"wrong issuer", k1.sign(t, withClaims(func(c map[string]any) { c["iss"] = "https://evil.cloudflareaccess.com" })), true},
		{"unknown key", k9.sign(t, validClaims()), true},
		{"not a jwt", "not.a.jwt", true},
		{"alg none", algNoneJWT(validClaims()), true},
		{"no email", k1.sign(t, withClaims(func(c map[string]any) { delete(c, "email") })), true},
		{"empty email", k1.sign(t, withClaims(func(c map[string]any) { c["email"] = "" })), true},
		{"numeric email", k1.sign(t, withClaims(func(c map[string]any) { c["email"] = 42 })), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Verify(requestWithToken(tc.rawJWT, tc.set))
			if !errors.Is(err, ErrNoIdentity) {
				t.Fatalf("err = %v, want ErrNoIdentity", err)
			}
			if raw := strings.TrimSpace(tc.rawJWT); raw != "" && strings.Contains(err.Error(), raw) {
				t.Errorf("error text contains the token: %v", err)
			}
		})
	}
}

func TestAccessVerifierNoTokenDoesNotFetch(t *testing.T) {
	v, jwks, _ := newTestVerifier(t)

	for _, r := range []*http.Request{requestWithToken("", false), requestWithToken("   ", true)} {
		if _, err := v.Verify(r); !errors.Is(err, ErrNoIdentity) {
			t.Fatalf("err = %v, want ErrNoIdentity", err)
		}
	}
	if n := jwks.fetches.Load(); n != 0 {
		t.Errorf("fetches = %d, want 0", n)
	}
}

func TestNormalizeTeamDomain(t *testing.T) {
	ok := map[string]string{
		"myteam.cloudflareaccess.com":              "myteam.cloudflareaccess.com",
		"https://MyTeam.cloudflareaccess.com/":     "myteam.cloudflareaccess.com",
		"  https://MyTeam.cloudflareaccess.com/  ": "myteam.cloudflareaccess.com",
		"  my-team1.cloudflareaccess.com":          "my-team1.cloudflareaccess.com",
	}
	for in, want := range ok {
		got, err := NormalizeTeamDomain(in)
		if err != nil || got != want {
			t.Errorf("NormalizeTeamDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"",
		"myteam.example.com",
		"https://myteam.cloudflareaccess.com/path",
		"http://myteam.cloudflareaccess.com",
		".cloudflareaccess.com",
		"my team.cloudflareaccess.com",
	}
	for _, in := range bad {
		if got, err := NormalizeTeamDomain(in); err == nil {
			t.Errorf("NormalizeTeamDomain(%q) = %q, want error", in, got)
		}
	}
}

func TestNormalizeAUD(t *testing.T) {
	got, err := NormalizeAUD("  ABCdef0123  ")
	if err != nil || got != "ABCdef0123" {
		t.Errorf("NormalizeAUD = %q, %v", got, err)
	}
	for _, in := range []string{"", "   ", "xyz", "ab cd"} {
		if got, err := NormalizeAUD(in); err == nil {
			t.Errorf("NormalizeAUD(%q) = %q, want error", in, got)
		}
	}
}

func TestNewAccessVerifierDerivesURLs(t *testing.T) {
	v := NewAccessVerifier("t.cloudflareaccess.com", "a")

	if v.issuer != "https://t.cloudflareaccess.com" {
		t.Errorf("issuer = %q", v.issuer)
	}
	if v.certsURL != "https://t.cloudflareaccess.com/cdn-cgi/access/certs" {
		t.Errorf("certsURL = %q", v.certsURL)
	}
}
