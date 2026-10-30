//go:build dev

package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
)

// postForm posts form values to the fake consent page.
func postForm(h http.Handler, path string, v url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestFakeConsentFlow(t *testing.T) {
	h := NewRouter(newTestDeps(t))

	rec := doJSON(h, http.MethodPost, "/api/drive/connect", DriveConnectRequest{ReturnTo: "/settings"})
	u, err := url.Parse(decode[DriveConnectResponse](t, rec).URL)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")

	// The page shows Allow and Deny for the state, escaped.
	page := do(h, http.MethodGet, "/api/drive/fake-consent?"+u.RawQuery)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(body, "Allow") || !strings.Contains(body, "Deny") || !strings.Contains(body, `value="`+state+`"`) {
		t.Fatalf("page = %d %s", page.Code, body)
	}
	if rec := do(h, http.MethodGet, `/api/drive/fake-consent?state=<script>`); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad state = %d, want 400", rec.Code)
	}

	// Deny goes to the callback with access_denied, never to a redirect value.
	rec = postForm(h, "/api/drive/fake-consent?redirect=https://evil.com", url.Values{"state": {state}, "decision": {"deny"}, "redirect": {"https://evil.com"}})
	want := testPublicURL + "/api/drive/callback?" + url.Values{"error": {"access_denied"}, "state": {state}}.Encode()
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
		t.Fatalf("deny = %d Location %q, want %q", rec.Code, rec.Header().Get("Location"), want)
	}

	rec = postForm(h, "/api/drive/fake-consent", url.Values{"state": {state}, "decision": {"allow"}})
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || rec.Code != http.StatusFound || loc.Scheme+"://"+loc.Host+loc.Path != testPublicURL+"/api/drive/callback" ||
		loc.Query().Get("code") != "fake-"+state || loc.Query().Get("state") != state {
		t.Fatalf("allow = %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
	// Following it connects.
	rec = do(h, http.MethodGet, loc.Path+"?"+loc.RawQuery)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/settings?drive=connected" {
		t.Fatalf("callback = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if st := driveStatus(t, h); st.State != "connected" || st.Email != drive.FakeEmail {
		t.Errorf("status = %+v", st)
	}

	for _, v := range []url.Values{{"state": {state}, "decision": {"maybe"}}, {"decision": {"allow"}}, {"state": {"a b"}, "decision": {"allow"}}} {
		if rec := postForm(h, "/api/drive/fake-consent", v); rec.Code != http.StatusBadRequest {
			t.Errorf("post %v = %d, want 400", v, rec.Code)
		}
	}
}
