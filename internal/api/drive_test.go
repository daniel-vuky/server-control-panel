package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/auth"
	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// emailVerifier is a verifier that always allows one email.
type emailVerifier string

func (v emailVerifier) Verify(*http.Request) (auth.Identity, error) {
	return auth.Identity{Email: string(v)}, nil
}

// startConnect posts /api/drive/connect and returns the consent URL's state.
func startConnect(t *testing.T, h http.Handler, returnTo string) string {
	t.Helper()
	rec := doJSON(h, http.MethodPost, "/api/drive/connect", DriveConnectRequest{ReturnTo: returnTo})
	if rec.Code != http.StatusOK {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	u, err := url.Parse(decode[DriveConnectResponse](t, rec).URL)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("consent URL %s has no state", u)
	}
	return state
}

// callback sends Google's redirect with the query q.
func callback(h http.Handler, q url.Values) *httptest.ResponseRecorder {
	return do(h, http.MethodGet, "/api/drive/callback?"+q.Encode())
}

// connectDrive connects the fake Drive through the API and, with folder,
// creates and selects the folder server-backups.
func connectDrive(t *testing.T, h http.Handler, folder bool) {
	t.Helper()
	state := startConnect(t, h, "/settings")
	if rec := callback(h, url.Values{"state": {state}, "code": {"fake-" + state}}); rec.Code != http.StatusFound {
		t.Fatalf("callback = %d %s", rec.Code, rec.Body.String())
	}
	if folder {
		if rec := doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{Name: "server-backups"}); rec.Code != http.StatusOK {
			t.Fatalf("folder = %d %s", rec.Code, rec.Body.String())
		}
	}
}

func driveStatus(t *testing.T, h http.Handler) DriveStatus {
	t.Helper()
	rec := do(h, http.MethodGet, "/api/drive")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/drive = %d %s", rec.Code, rec.Body.String())
	}
	return decode[DriveStatus](t, rec)
}

func TestDriveStatus(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)

	rec := do(h, http.MethodGet, "/api/drive")
	if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK ||
		got != `{"state":"not_connected","email":"","folder":null,"reason":""}` {
		t.Fatalf("GET /api/drive = %d %s", rec.Code, got)
	}

	connectDrive(t, h, true)
	st := driveStatus(t, h)
	if st.State != "connected" || st.Email != drive.FakeEmail || st.Folder == nil || st.Folder.Name != "server-backups" || st.Folder.ID == "" {
		t.Errorf("status = %+v", st)
	}

	// Drive not set up on this server.
	nc, err := drive.NewService(config.Config{}, d.Store, drive.NewFake(t.TempDir(), testPublicURL), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	d.Drive = nc
	h = NewRouter(d)
	if st := driveStatus(t, h); st.State != "not_configured" || st.Reason != "PANEL_SECRET_KEY is not set" {
		t.Errorf("not configured status = %+v", st)
	}
	rec = doJSON(h, http.MethodPost, "/api/drive/connect", DriveConnectRequest{ReturnTo: "/settings"})
	if rec.Code != http.StatusConflict || decode[DriveError](t, rec) != (DriveError{Error: "not_configured", Message: "Google Drive is not set up on this server."}) {
		t.Errorf("connect not configured = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDriveConnectReturnsURLAndStoresState(t *testing.T) {
	h := NewRouter(newTestDeps(t))

	rec := doJSON(h, http.MethodPost, "/api/drive/connect", DriveConnectRequest{ReturnTo: "/projects/3/database?backup=postgresql:db:alpha"})
	if rec.Code != http.StatusOK {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	raw := decode[DriveConnectResponse](t, rec).URL
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(raw, testPublicURL+"/api/drive/fake-consent?") {
		t.Fatalf("url = %q (%v)", raw, err)
	}
	state := u.Query().Get("state")
	if len(state) != 43 || u.Query().Get("redirect") != testPublicURL+"/api/drive/callback" {
		t.Errorf("url = %s, want a 32-byte state and the callback as redirect", raw)
	}

	rec = callback(h, url.Values{"state": {state}, "code": {"fake-" + state}})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != "/projects/3/database?backup=postgresql:db:alpha&drive=connected" {
		t.Errorf("callback = %d Location %q", rec.Code, loc)
	}

	// returnTo is sanitised when stored.
	for _, bad := range []string{"//evil.com", "https://evil.com/x", `/\evil.com`, "settings", ""} {
		state := startConnect(t, h, bad)
		rec := callback(h, url.Values{"state": {state}, "code": {"fake-" + state}})
		if loc := rec.Header().Get("Location"); loc != "/settings?drive=connected" {
			t.Errorf("returnTo %q: Location = %q, want /settings?drive=connected", bad, loc)
		}
	}
}

func TestDriveCallbackHappyPath(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)

	state := startConnect(t, h, "/settings?tab=drive")
	rec := callback(h, url.Values{"state": {state}, "code": {"fake-" + state}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/settings?tab=drive&drive=connected" {
		t.Fatalf("callback = %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if rp := rec.Header().Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", rp)
	}
	if st := driveStatus(t, h); st.State != "connected" || st.Email != drive.FakeEmail || st.Folder != nil {
		t.Errorf("status = %+v", st)
	}
	rows := activityOf(t, d, store.ActDriveConnect)
	if len(rows) != 1 || rows[0].Result != store.ResultOK || rows[0].Actor != "test@example.com" || rows[0].Target != drive.FakeEmail {
		t.Errorf("drive.connect activity = %+v", rows)
	}

	// The state is single use.
	if rec := callback(h, url.Values{"state": {state}, "code": {"fake-" + state}}); rec.Code != http.StatusBadRequest {
		t.Errorf("reused state = %d, want 400", rec.Code)
	}
}

func TestDriveCallbackDeclined(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)

	state := startConnect(t, h, "/settings")
	rec := callback(h, url.Values{"state": {state}, "error": {"access_denied"}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/settings?drive=declined" {
		t.Fatalf("declined = %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
	if st := driveStatus(t, h); st.State != "not_connected" {
		t.Errorf("status = %+v, want not_connected", st)
	}
	if rows := activityOf(t, d, store.ActDriveConnect); len(rows) != 0 {
		t.Errorf("a declined connect recorded %+v", rows)
	}

	// Another error from Google comes back as drive=error and is recorded.
	state = startConnect(t, h, "/settings")
	rec = callback(h, url.Values{"state": {state}, "error": {"server_error"}})
	if rec.Header().Get("Location") != "/settings?drive=error&code=no_access" {
		t.Errorf("error = Location %q", rec.Header().Get("Location"))
	}
	// no_access at connect time is Google refusing the app, not a folder.
	if rows := activityOf(t, d, store.ActDriveConnect); len(rows) != 1 || rows[0].Result != store.ResultFailed ||
		rows[0].Reason != "Google refused the connection. Check GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET and the redirect URI (docs/google-drive.md)." {
		t.Errorf("failed connect activity = %+v", rows)
	}

	// Another code keeps its words.
	state = startConnect(t, h, "/settings")
	rec = callback(h, url.Values{"state": {state}, "code": {"fake-other"}})
	if rec.Header().Get("Location") != "/settings?drive=error&code=token_expired" {
		t.Errorf("bad code = Location %q", rec.Header().Get("Location"))
	}
	if rows := activityOf(t, d, store.ActDriveConnect); len(rows) != 2 || rows[0].Reason != drive.Words(drive.CodeTokenExpired) {
		t.Errorf("failed connect activity = %+v", rows)
	}
}

func TestDriveCallbackRejectsForeignStateAndOpenRedirect(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	other := d
	other.Verifier = emailVerifier("other@example.com")
	ho := NewRouter(other)

	wantExpired := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusBadRequest || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
			!strings.Contains(rec.Body.String(), "This link has expired. Go back to Settings and connect again.") ||
			rec.Header().Get("Location") != "" {
			t.Errorf("%s = %d %q %s", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}

	// Another actor's state is refused, and used up.
	state := startConnect(t, h, "/settings")
	wantExpired("foreign actor", callback(ho, url.Values{"state": {state}, "code": {"fake-" + state}}))
	wantExpired("after a foreign try", callback(h, url.Values{"state": {state}, "code": {"fake-" + state}}))

	wantExpired("unknown state", callback(h, url.Values{"state": {"nope"}, "code": {"fake-nope"}}))
	wantExpired("no state", callback(h, url.Values{"code": {"x"}}))

	// A redirect parameter on the callback is ignored: the redirect is the
	// stored, sanitised returnTo.
	state = startConnect(t, h, "https://evil.com")
	rec := callback(h, url.Values{"state": {state}, "code": {"fake-" + state}, "redirect": {"https://evil.com"}, "returnTo": {"//evil.com"}})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != "/settings?drive=connected" {
		t.Errorf("callback = %d Location %q", rec.Code, loc)
	}
	if st := driveStatus(t, h); st.State != "connected" {
		t.Errorf("status = %+v", st)
	}
}

func TestDriveFolders(t *testing.T) {
	h := NewRouter(newTestDeps(t))

	rec := do(h, http.MethodGet, "/api/drive/folders")
	if rec.Code != http.StatusConflict || decode[DriveError](t, rec).Error != "not_connected" {
		t.Fatalf("folders not connected = %d %s", rec.Code, rec.Body.String())
	}

	connectDrive(t, h, false)
	rec = do(h, http.MethodGet, "/api/drive/folders")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("folders = %d %s, want []", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"server-backups", "archive"} {
		if rec := doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{Name: name}); rec.Code != http.StatusOK {
			t.Fatalf("create %s = %d %s", name, rec.Code, rec.Body.String())
		}
	}
	got := decode[[]DriveFolder](t, do(h, http.MethodGet, "/api/drive/folders"))
	if len(got) != 2 || got[0].Name != "archive" || got[1].Name != "server-backups" || got[0].ID == "" {
		t.Errorf("folders = %+v", got)
	}
}

func TestDriveCreateFolderValidation(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)

	rec := doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{Name: "x"})
	if rec.Code != http.StatusConflict || decode[DriveError](t, rec).Error != "not_connected" {
		t.Errorf("create not connected = %d %s", rec.Code, rec.Body.String())
	}
	connectDrive(t, h, false)

	for _, name := range []string{"", "   ", "a/b", strings.Repeat("x", 101)} {
		rec := doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{Name: name})
		fe := decode[FieldError](t, rec)
		if rec.Code != http.StatusUnprocessableEntity || fe.Field != "name" || fe.Code != "invalid" ||
			fe.Error != "A folder name is 1 to 100 characters, with no /." {
			t.Errorf("name %q = %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := doJSON(h, http.MethodPost, "/api/drive/folder", "{"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON = %d", rec.Code)
	}

	rec = doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{Name: "  backups  "})
	st := decode[DriveStatus](t, rec)
	if rec.Code != http.StatusOK || st.Folder == nil || st.Folder.Name != "backups" {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	first := *st.Folder
	rec = doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{Name: "other"})
	if rec.Code != http.StatusOK {
		t.Fatalf("create other = %d %s", rec.Code, rec.Body.String())
	}

	// Select an existing folder by id; an id that is not one of the panel's
	// folders is a field error.
	rec = doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{ID: first.ID})
	if st := decode[DriveStatus](t, rec); rec.Code != http.StatusOK || st.Folder == nil || *st.Folder != first {
		t.Errorf("select = %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{ID: "nope"})
	if fe := decode[FieldError](t, rec); rec.Code != http.StatusUnprocessableEntity || fe.Field != "id" || fe.Code != drive.CodeFolderMissing {
		t.Errorf("select unknown = %d %s", rec.Code, rec.Body.String())
	}

	rows := activityOf(t, d, store.ActDriveFolder)
	if len(rows) != 3 {
		t.Fatalf("drive.folder activity = %+v, want 3 rows", rows)
	}
	if r := rows[0]; r.Result != store.ResultOK || r.Target != "backups" || r.Reason != `folder: "other" → "backups"` {
		t.Errorf("select activity = %+v", r)
	}
	if r := rows[2]; r.Target != "backups" || r.Reason != `folder: none → "backups"` {
		t.Errorf("first create activity = %+v", r)
	}
}

func TestDriveTest(t *testing.T) {
	h := NewRouter(newTestDeps(t))

	test := func() (int, DriveTestResult, string) {
		rec := doJSON(h, http.MethodPost, "/api/drive/test", "")
		return rec.Code, decode[DriveTestResult](t, rec), rec.Body.String()
	}
	if code, got, _ := test(); code != http.StatusOK || got != (DriveTestResult{Code: "not_connected", Message: "Connect Google Drive first"}) {
		t.Errorf("test not connected = %d %+v", code, got)
	}
	connectDrive(t, h, false)
	if code, got, _ := test(); code != http.StatusOK || got != (DriveTestResult{Code: drive.CodeFolderMissing, Message: drive.Words(drive.CodeFolderMissing)}) {
		t.Errorf("test without folder = %d %+v", code, got)
	}
	if rec := doJSON(h, http.MethodPost, "/api/drive/folder", DriveFolderRequest{Name: "server-backups"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if code, got, body := test(); code != http.StatusOK || got != (DriveTestResult{OK: true}) || strings.TrimSpace(body) != `{"ok":true}` {
		t.Errorf("test = %d %s", code, body)
	}
}

// holdClient is a Drive client whose uploads wait for hold to close and
// report on entered first.
type holdClient struct {
	drive.Client
	entered chan struct{}
	hold    chan struct{}
}

func (c *holdClient) Session(refresh string) drive.Session {
	return &holdSession{Session: c.Client.Session(refresh), c: c}
}

type holdSession struct {
	drive.Session
	c *holdClient
}

func (s *holdSession) Upload(ctx context.Context, folderID, name string, size int64, r io.Reader, progress func(int64)) (string, string, error) {
	s.c.entered <- struct{}{}
	<-s.c.hold
	return s.Session.Upload(ctx, folderID, name, size, r, progress)
}

func TestDriveTestBusy(t *testing.T) {
	hc := &holdClient{entered: make(chan struct{}, 1), hold: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(hc.hold) }) }
	t.Cleanup(release)
	d := newTestDepsWith(t, func(c drive.Client) drive.Client { hc.Client = c; return hc })
	h := NewRouter(d)
	connectDrive(t, h, true)

	first := postAsync(h, "/api/drive/test")
	<-hc.entered
	rec := doJSON(h, http.MethodPost, "/api/drive/test", "")
	if rec.Code != http.StatusConflict || decode[DriveError](t, rec).Error != "busy" {
		t.Errorf("second test = %d %s, want 409 busy", rec.Code, rec.Body.String())
	}
	release()
	if rec := <-first; rec.Code != http.StatusOK || decode[DriveTestResult](t, rec) != (DriveTestResult{OK: true}) {
		t.Errorf("first test = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDriveDisconnect(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	connectDrive(t, h, true)

	rec := doJSON(h, http.MethodPost, "/api/drive/disconnect", "")
	if st := decode[DriveStatus](t, rec); rec.Code != http.StatusOK || st.State != "not_connected" || st.Email != "" || st.Folder != nil {
		t.Fatalf("disconnect = %d %s", rec.Code, rec.Body.String())
	}
	if st := driveStatus(t, h); st.State != "not_connected" {
		t.Errorf("status = %+v", st)
	}
	if _, ok, err := d.Store.Drive(context.Background()); ok || err != nil {
		t.Errorf("the drive row is still stored (%v)", err)
	}
	rows := activityOf(t, d, store.ActDriveDisconnect)
	if len(rows) != 1 || rows[0].Result != store.ResultOK || rows[0].Target != drive.FakeEmail || rows[0].Actor != "test@example.com" {
		t.Errorf("drive.disconnect activity = %+v", rows)
	}

	// Not configured: nothing to disconnect.
	nc, err := drive.NewService(config.Config{}, d.Store, drive.NewFake(t.TempDir(), testPublicURL), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	d.Drive = nc
	rec = doJSON(NewRouter(d), http.MethodPost, "/api/drive/disconnect", "")
	if rec.Code != http.StatusConflict || decode[DriveError](t, rec).Error != "not_configured" {
		t.Errorf("disconnect not configured = %d %s", rec.Code, rec.Body.String())
	}
}

// A connect that lands while Disconnect waits is kept: 409 busy with words
// that say nothing was disconnected, recorded as refused.
func TestDriveDisconnectConnectedMeanwhile(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	connectDrive(t, h, true)
	d.Drive.SetBeforeDisconnectWrite(func() { connectDrive(t, h, false) })

	rec := doJSON(h, http.MethodPost, "/api/drive/disconnect", "")
	want := DriveError{Error: "busy", Message: "Google Drive was connected again meanwhile; nothing was disconnected."}
	if rec.Code != http.StatusConflict || decode[DriveError](t, rec) != want {
		t.Fatalf("disconnect = %d %s", rec.Code, rec.Body.String())
	}
	if st := driveStatus(t, h); st.State != "connected" {
		t.Errorf("status = %+v, want the new connection kept", st)
	}
	rows := activityOf(t, d, store.ActDriveDisconnect)
	if len(rows) != 1 || rows[0].Result != store.ResultRefused || rows[0].Reason != want.Message {
		t.Errorf("drive.disconnect activity = %+v", rows)
	}
}

func TestDriveRoutesNeedJSON(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	for _, path := range []string{"/api/drive/connect", "/api/drive/folder", "/api/drive/test", "/api/drive/disconnect"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"x","returnTo":"/settings"}`))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s as a form = %d, want 415", path, rec.Code)
		}
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("a refused POST recorded %+v", rows)
	}
}

// decodeRaw decodes a JSON body into a generic value, for tests that look at
// its shape.
func decodeRaw(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return v
}
