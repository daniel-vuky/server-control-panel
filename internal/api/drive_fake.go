//go:build dev

package api

import (
	"html/template"
	"net/http"
	"net/url"
	"regexp"
)

// fakeState is what a state from StartConnect looks like (32 bytes,
// base64url); the fake consent page takes nothing else.
var fakeState = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// fakeConsentPage is the fake Drive's consent page: Allow or Deny, posted back
// to /api/drive/fake-consent.
var fakeConsentPage = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Fake Google Drive</title></head>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 16px">
<h1 style="font-size:1.25rem">Fake Google Drive</h1>
<p>A dev build stands in for Google. Let the panel use the fake Drive?</p>
<form method="post" action="/api/drive/fake-consent" style="display:flex;gap:8px">
<input type="hidden" name="state" value="{{.}}">
<button type="submit" name="decision" value="deny">Deny</button>
<button type="submit" name="decision" value="allow">Allow</button>
</form>
</body></html>
`))

// registerDevRoutes adds the fake Drive's consent page (dev build only). It
// is behind protect, but not JSON: it is a browser page and its form.
func registerDevRoutes(mux *http.ServeMux, s *server, protect func(http.Handler) http.Handler) {
	mux.Handle("GET /api/drive/fake-consent", protect(http.HandlerFunc(s.fakeConsentPage)))
	mux.Handle("POST /api/drive/fake-consent", protect(http.HandlerFunc(s.fakeConsent)))
}

// fakeConsentPage is GET /api/drive/fake-consent?state=…: the Allow and Deny
// page. The redirect parameter the fake's URL carries is ignored.
func (s *server) fakeConsentPage(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !fakeState.MatchString(state) {
		writePage(w, http.StatusBadRequest, "Fake Google Drive", "This consent link has no valid state.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = fakeConsentPage.Execute(w, state)
}

// fakeConsent is POST /api/drive/fake-consent {state, decision}: it sends the
// browser to the panel's own callback the way Google would, with the fake's
// code (allow) or error=access_denied (deny). It only ever redirects to
// CallbackURL, never to a value from the request.
func (s *server) fakeConsent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		writePage(w, http.StatusBadRequest, "Fake Google Drive", "The form could not be read.")
		return
	}
	state := r.PostForm.Get("state")
	if !fakeState.MatchString(state) {
		writePage(w, http.StatusBadRequest, "Fake Google Drive", "This consent link has no valid state.")
		return
	}
	q := url.Values{"state": {state}}
	switch r.PostForm.Get("decision") {
	case "allow":
		q.Set("code", "fake-"+state)
	case "deny":
		q.Set("error", "access_denied")
	default:
		writePage(w, http.StatusBadRequest, "Fake Google Drive", "Choose Allow or Deny.")
		return
	}
	w.Header().Set("Location", s.Drive.CallbackURL()+"?"+q.Encode())
	w.WriteHeader(http.StatusFound)
}
