package drive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthURLScopeOnlyDriveFile(t *testing.T) {
	g := NewGoogle("my-client", "my-secret")
	u, err := url.Parse(g.AuthURL("st-abc", testRedirect))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "accounts.google.com" {
		t.Errorf("auth URL host = %s://%s", u.Scheme, u.Host)
	}
	q := u.Query()
	want := map[string]string{
		"scope":         "https://www.googleapis.com/auth/drive.file",
		"access_type":   "offline",
		"prompt":        "consent",
		"response_type": "code",
		"client_id":     "my-client",
		"redirect_uri":  testRedirect,
		"state":         "st-abc",
	}
	for k, v := range want {
		if got := q[k]; len(got) != 1 || got[0] != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if strings.Contains(u.String(), "my-secret") {
		t.Error("the auth URL carries the client secret")
	}
}

func TestExchangeAndRefresh(t *testing.T) {
	ctx := context.Background()
	m := newMockGoogle(t)
	g := m.client()
	if _, err := g.Exchange(ctx, m.issueCode(testRedirect), "https://other.example.com/cb"); err == nil {
		t.Fatal("Exchange with another redirect succeeded")
	}
	refresh, err := g.Exchange(ctx, m.issueCode(testRedirect), testRedirect)
	if err != nil || !strings.HasPrefix(refresh, "refresh-") {
		t.Fatalf("Exchange = %q, %v", refresh, err)
	}
	s := g.Session(refresh)
	for range 2 {
		if _, err := s.Account(ctx); err != nil {
			t.Fatalf("Account: %v", err)
		}
	}
	if m.refreshCount != 1 {
		t.Fatalf("refreshes = %d, want 1 (the access token is reused)", m.refreshCount)
	}
	// Google drops the access token: a 401 refreshes once and retries.
	m.expireAccess()
	if _, err := s.Account(ctx); err != nil {
		t.Fatalf("Account after expiry: %v", err)
	}
	if m.refreshCount != 2 {
		t.Fatalf("refreshes = %d, want 2", m.refreshCount)
	}
	// A short-lived token is refreshed before it runs out.
	m.expiresIn = 1
	m.expireAccess()
	_, _ = s.Account(ctx)
	_, _ = s.Account(ctx)
	if m.refreshCount != 4 {
		t.Fatalf("refreshes = %d, want 4", m.refreshCount)
	}
}

func TestInvalidGrantIsTokenExpired(t *testing.T) {
	ctx := context.Background()
	m := newMockGoogle(t)
	g := m.client()
	refresh := "refresh-revoked-secret-value"
	_, err := g.Session(refresh).ListFolders(ctx)
	wantCode(t, err, CodeTokenExpired)
	if strings.Contains(err.Error(), refresh) {
		t.Fatalf("error %q carries the refresh token", err)
	}
	// A bad code at Exchange is the same.
	_, err = g.Exchange(ctx, "nope", testRedirect)
	wantCode(t, err, CodeTokenExpired)
}

func TestAboutEmail(t *testing.T) {
	ctx := context.Background()
	m := newMockGoogle(t)
	m.email = "someone@gmail.com"
	g := m.client()
	refresh, err := g.Exchange(ctx, m.issueCode(testRedirect), testRedirect)
	if err != nil {
		t.Fatal(err)
	}
	email, err := g.Session(refresh).Account(ctx)
	if err != nil || email != "someone@gmail.com" {
		t.Fatalf("Account = %q, %v", email, err)
	}
	if q, _ := url.ParseQuery(m.lastAboutQuery); q.Get("fields") != "user(emailAddress)" {
		t.Fatalf("about query = %q, want fields=user(emailAddress)", m.lastAboutQuery)
	}
}

func connectMock(t *testing.T, m *mockGoogle) Session {
	t.Helper()
	g := m.client()
	refresh, err := g.Exchange(context.Background(), m.issueCode(testRedirect), testRedirect)
	if err != nil {
		t.Fatal(err)
	}
	return g.Session(refresh)
}

func noBackoff(t *testing.T) {
	old := resumeBackoff
	resumeBackoff = time.Millisecond
	t.Cleanup(func() { resumeBackoff = old })
}

func TestResumableUploadChunksAndResumesAfter503(t *testing.T) {
	ctx := context.Background()
	setUploadChunk(t, 256<<10)
	noBackoff(t)
	m := newMockGoogle(t)
	s := connectMock(t, m)
	folder := m.addFolder("server-backups", "root", false)
	// The second PUT stores half its chunk, then fails 503.
	m.putHook = func(u *mockUpload, n int, body []byte, w http.ResponseWriter) bool {
		if n != 2 {
			return false
		}
		m.mu.Lock()
		u.data = append(u.data, body[:len(body)/2]...)
		m.mu.Unlock()
		apiError(w, 503, "backendError")
		return true
	}
	data := bytes.Repeat([]byte("x"), 3*(256<<10)+100)
	var last int64
	id, link, err := s.Upload(ctx, folder, "a.sql.gz", int64(len(data)), bytes.NewReader(data), func(n int64) { last = n })
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !strings.Contains(link, id) || last != int64(len(data)) {
		t.Fatalf("Upload = %q %q, progress %d", id, link, last)
	}
	if !bytes.Equal(m.files[id].content, data) {
		t.Fatal("uploaded content differs")
	}
	// 4 chunks, one failed and resent from its middle.
	if m.puts != 5 || m.queries != 1 {
		t.Fatalf("puts = %d, status queries = %d; want 5 and 1", m.puts, m.queries)
	}

	// Four failures on one chunk give up: three resumes only.
	m.puts, m.queries = 0, 0
	m.putHook = func(_ *mockUpload, _ int, _ []byte, w http.ResponseWriter) bool {
		apiError(w, 503, "backendError")
		return true
	}
	_, _, err = s.Upload(ctx, folder, "b.sql.gz", int64(len(data)), bytes.NewReader(data), nil)
	wantCode(t, err, CodeUnreachable)
	if m.puts != 4 {
		t.Fatalf("puts = %d, want 4 (1 + 3 resumes)", m.puts)
	}
}

func TestUploadStall(t *testing.T) {
	ctx := context.Background()
	old := uploadStall
	uploadStall = 150 * time.Millisecond
	t.Cleanup(func() { uploadStall = old })
	m := newMockGoogle(t)
	s := connectMock(t, m)
	folder := m.addFolder("server-backups", "root", false)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	m.putHook = func(_ *mockUpload, _ int, _ []byte, _ http.ResponseWriter) bool {
		select { // never answers
		case <-release:
		case <-time.After(10 * time.Second):
		}
		return true
	}
	start := time.Now()
	_, _, err := s.Upload(ctx, folder, "a.sql.gz", 5, bytes.NewReader([]byte("hello")), nil)
	wantCode(t, err, CodeUnreachable)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the stall took %s to fire", d)
	}
}

// statusServer answers every Drive call with one status and body; the token
// endpoint works.
func statusServer(t *testing.T, status int, reason string, delay time.Duration) Session {
	t.Helper()
	var n atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"access_token": "tok-" + strconv.FormatInt(n.Add(1), 10), "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		apiError(w, status, reason)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	g := newGoogle("client-id", "client-secret", googleEndpoints{
		Auth: srv.URL + "/auth", Token: srv.URL + "/token", Revoke: srv.URL + "/revoke", API: srv.URL,
	}, srv.Client())
	return g.Session("refresh-x")
}

func TestErrorMapping(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		status int
		reason string
		want   string
	}{
		{"401", 401, "authError", CodeNoAccess},
		{"403", 403, "insufficientFilePermissions", CodeNoAccess},
		{"404", 404, "notFound", CodeFolderMissing},
		{"quota", 403, "storageQuotaExceeded", CodeQuota},
		{"rate limit", 403, "userRateLimitExceeded", CodeUnreachable},
		{"429", 429, "rateLimitExceeded", CodeUnreachable},
		{"500", 500, "backendError", CodeUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := statusServer(t, tc.status, tc.reason, 0)
			_, err := s.ListFolders(ctx)
			wantCode(t, err, tc.want)
			if strings.Contains(err.Error(), "tok-") {
				t.Fatalf("error %q carries the access token", err)
			}
		})
	}
	t.Run("quota on upload", func(t *testing.T) {
		m := newMockGoogle(t)
		s := connectMock(t, m)
		folder := m.addFolder("f", "root", false)
		m.putHook = func(_ *mockUpload, _ int, _ []byte, w http.ResponseWriter) bool {
			apiError(w, 403, "storageQuotaExceeded")
			return true
		}
		_, _, err := s.Upload(ctx, folder, "a", 1, bytes.NewReader([]byte("a")), nil)
		wantCode(t, err, CodeQuota)
	})
	t.Run("trashed folder", func(t *testing.T) {
		m := newMockGoogle(t)
		s := connectMock(t, m)
		folder := m.addFolder("f", "root", true)
		_, _, err := s.Upload(ctx, folder, "a", 1, bytes.NewReader([]byte("a")), nil)
		wantCode(t, err, CodeFolderMissing)
		_, err = s.FindOrCreateFolder(ctx, "shop", folder)
		wantCode(t, err, CodeFolderMissing)
	})
	t.Run("timeout", func(t *testing.T) {
		old := callTimeout
		callTimeout = 100 * time.Millisecond
		t.Cleanup(func() { callTimeout = old })
		s := statusServer(t, 200, "", 5*time.Second)
		_, err := s.ListFolders(ctx)
		wantCode(t, err, CodeUnreachable)
	})
	t.Run("network", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		g := newGoogle("id", "secret", googleEndpoints{Token: srv.URL + "/token", API: srv.URL, Revoke: srv.URL}, http.DefaultClient)
		_, err := g.Session("r").ListFolders(ctx)
		wantCode(t, err, CodeUnreachable)
	})
	t.Run("caller cancel passes through", func(t *testing.T) {
		s := statusServer(t, 200, "", 5*time.Second)
		cctx, cancel := context.WithCancel(ctx)
		go func() { time.Sleep(50 * time.Millisecond); cancel() }()
		_, err := s.ListFolders(cctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
}

func TestRevokeAndDownloadErrors(t *testing.T) {
	ctx := context.Background()
	m := newMockGoogle(t)
	s := connectMock(t, m)
	_, err := s.Download(ctx, "missing")
	wantCode(t, err, CodeFolderMissing)
	if err := s.Delete(ctx, "missing"); CodeOf(err) != CodeFolderMissing {
		t.Fatalf("Delete missing = %v", err)
	}
	if err := s.Revoke(ctx); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// A second revoke of the same token is token_expired.
	wantCode(t, s.Revoke(ctx), CodeTokenExpired)
}

func TestWords(t *testing.T) {
	want := map[string]string{
		CodeTokenExpired:  "The Google Drive token has expired. Reconnect Google Drive. If the Google Cloud app is still in Testing, publish it: tokens issued in Testing expire after 7 days.",
		CodeNoAccess:      "The panel no longer has access to this folder.",
		CodeFolderMissing: "The destination folder is missing or in the trash. Choose or create a folder.",
		CodeQuota:         "Google Drive is full.",
		CodeUnreachable:   "Google Drive is unreachable.",
	}
	for code, w := range want {
		if got := Words(code); got != w {
			t.Errorf("Words(%s) = %q, want %q", code, got, w)
		}
	}
	e := &Error{Code: CodeQuota, Err: io.ErrUnexpectedEOF}
	if !errors.Is(e, io.ErrUnexpectedEOF) || CodeOf(e) != CodeQuota {
		t.Error("Error does not unwrap")
	}
}

// A chunk answered before its body is sent must not run progress on the
// transport's goroutine: progress stays serial with the caller (run with -race).
func TestUploadEarlyAnswerProgressSerial(t *testing.T) {
	ctx := context.Background()
	setUploadChunk(t, 4<<20)
	m := newMockGoogle(t)
	s := connectMock(t, m)
	folder := m.addFolder("server-backups", "root", false)
	m.earlyPut = func(w http.ResponseWriter) bool {
		apiError(w, 403, "insufficientFilePermissions")
		return true
	}
	data := bytes.Repeat([]byte("y"), 4<<20)
	for range 5 {
		var calls []int64 // unsynchronised on purpose
		_, _, err := s.Upload(ctx, folder, "a.sql.gz", int64(len(data)), bytes.NewReader(data), func(n int64) { calls = append(calls, n) })
		wantCode(t, err, CodeNoAccess)
		calls = append(calls, -1)
	}
}

// A 4xx on a chunk other than the mapped ones is not resumed.
func TestUploadClientErrorNotResumed(t *testing.T) {
	ctx := context.Background()
	noBackoff(t)
	m := newMockGoogle(t)
	s := connectMock(t, m)
	folder := m.addFolder("server-backups", "root", false)
	m.putHook = func(_ *mockUpload, _ int, _ []byte, w http.ResponseWriter) bool {
		apiError(w, 400, "badRequest")
		return true
	}
	_, _, err := s.Upload(ctx, folder, "a.sql.gz", 3, bytes.NewReader([]byte("abc")), nil)
	wantCode(t, err, CodeUnreachable)
	if m.puts != 1 || m.queries != 0 {
		t.Fatalf("puts = %d, queries = %d; want 1 and 0 (no resume)", m.puts, m.queries)
	}
}

// Download refreshes the access token within callTimeout, like every other
// call: a hung token endpoint is unreachable, not a hang. The streamed body
// keeps the caller's context.
func TestDownloadTokenRefreshTimesOut(t *testing.T) {
	old := callTimeout
	callTimeout = 100 * time.Millisecond
	t.Cleanup(func() { callTimeout = old })
	hung := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(http.ResponseWriter, *http.Request) {
		select {
		case <-hung:
		case <-time.After(5 * time.Second):
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hung) }) // runs first: lets srv.Close return
	g := newGoogle("client-id", "client-secret", googleEndpoints{
		Auth: srv.URL + "/auth", Token: srv.URL + "/token", Revoke: srv.URL + "/revoke", API: srv.URL,
	}, srv.Client())
	start := time.Now()
	_, err := g.Session("refresh-x").Download(context.Background(), "f")
	wantCode(t, err, CodeUnreachable)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Download took %s with a hung token endpoint, want about callTimeout", took)
	}
}

// The streamed body of a download is not cut by callTimeout: it lives on the
// caller's context.
func TestDownloadBodyOutlivesCallTimeout(t *testing.T) {
	old := callTimeout
	callTimeout = 100 * time.Millisecond
	t.Cleanup(func() { callTimeout = old })
	m := newMockGoogle(t)
	s := connectMock(t, m)
	folder := m.addFolder("f", "root", false)
	data := bytes.Repeat([]byte("x"), 1<<10)
	id, _, err := s.Upload(context.Background(), folder, "a", int64(len(data)), bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := s.Download(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	time.Sleep(3 * callTimeout)
	got, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("body = %d bytes, %v", len(got), err)
	}
}
