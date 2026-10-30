package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/secret"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

func testConfig() config.Config {
	return config.Config{
		SecretKey:          bytes.Repeat([]byte{7}, 32),
		PublicURL:          "https://panel.example.com",
		GoogleClientID:     "client-id",
		GoogleClientSecret: "client-secret",
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// recClient wraps the fake: it records the refresh tokens it hands out, can
// answer another account email, and can hold an upload.
type recClient struct {
	Client
	mu        sync.Mutex
	email     string
	refreshes []string
	hold      chan struct{} // when set, Upload waits on it
	entered   chan struct{}
}

func (c *recClient) Exchange(ctx context.Context, code, redirect string) (string, error) {
	r, err := c.Client.Exchange(ctx, code, redirect)
	if err == nil {
		c.mu.Lock()
		c.refreshes = append(c.refreshes, r)
		c.mu.Unlock()
	}
	return r, err
}

func (c *recClient) Session(refresh string) Session {
	return &recSession{Session: c.Client.Session(refresh), c: c}
}

func (c *recClient) lastRefresh() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshes[len(c.refreshes)-1]
}

type recSession struct {
	Session
	c *recClient
}

func (s *recSession) Account(ctx context.Context) (string, error) {
	e, err := s.Session.Account(ctx)
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if err == nil && s.c.email != "" {
		return s.c.email, nil
	}
	return e, err
}

func (s *recSession) Upload(ctx context.Context, folderID, name string, size int64, r io.Reader, progress func(int64)) (string, string, error) {
	if s.c.hold != nil {
		s.c.entered <- struct{}{}
		<-s.c.hold
	}
	return s.Session.Upload(ctx, folderID, name, size, r, progress)
}

type svcEnv struct {
	svc     *Service
	client  *recClient
	st      *store.Store
	clock   *clock
	changes atomic.Int64
	dir     string
}

func newSvcEnv(t *testing.T) *svcEnv {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	e := &svcEnv{
		client: &recClient{Client: NewFake(dir, "https://panel.example.com")},
		st:     st,
		clock:  &clock{t: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)},
		dir:    dir,
	}
	e.svc = e.newService(t, testConfig())
	return e
}

func (e *svcEnv) newService(t *testing.T, cfg config.Config) *Service {
	t.Helper()
	svc, err := NewService(cfg, e.st, e.client, e.clock.now)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// Status from inside the callback would deadlock if it ran under the lock.
	svc.OnChange(func() { _ = svc.Status(); e.changes.Add(1) })
	return svc
}

// startState starts a connect and returns the state from the consent URL.
func (e *svcEnv) startState(t *testing.T, actor, returnTo string) string {
	t.Helper()
	raw, err := e.svc.StartConnect(context.Background(), actor, returnTo)
	if err != nil {
		t.Fatalf("StartConnect: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("redirect"); got != "https://panel.example.com/api/drive/callback" {
		t.Fatalf("redirect = %q", got)
	}
	return u.Query().Get("state")
}

func (e *svcEnv) connect(t *testing.T, actor string) string {
	t.Helper()
	state := e.startState(t, actor, "/settings")
	to, err := e.svc.FinishConnect(context.Background(), actor, state, "fake-"+state, "")
	if err != nil {
		t.Fatalf("FinishConnect: %v", err)
	}
	return to
}

func TestReturnToSanitising(t *testing.T) {
	cases := map[string]string{
		"/settings":                     "/settings",
		"/projects/3/database?backup=k": "/projects/3/database?backup=k",
		"//evil.com":                    "/settings",
		"https://x":                     "/settings",
		"/\\x":                          "/settings",
		"/a\nb":                         "/settings",
		"/a\x7fb":                       "/settings",
		"settings":                      "/settings",
		"":                              "/settings",
		"/ok//no":                       "/settings",
		"/a:b":                          "/a:b",
		"javascript:alert(1)":           "/settings",
	}
	for in, want := range cases {
		if got := SanitizeReturnTo(in); got != want {
			t.Errorf("SanitizeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotConfigured(t *testing.T) {
	e := newSvcEnv(t)
	svc := e.newService(t, config.Config{PublicURL: "https://panel.example.com"})
	st := svc.Status()
	if st.State != StateNotConfigured || st.Reason != "PANEL_SECRET_KEY is not set" {
		t.Fatalf("Status = %+v", st)
	}
	if _, err := svc.StartConnect(context.Background(), "a@x", "/settings"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("StartConnect = %v, want ErrNotConfigured", err)
	}
	if _, _, err := svc.Session(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Session = %v, want ErrNotConfigured", err)
	}
}

func TestConnectStoresSealedToken(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	if st := e.svc.Status(); st.State != StateNotConnected {
		t.Fatalf("initial Status = %+v", st)
	}
	if to := e.connect(t, "a@x"); to != "/settings?drive=connected" {
		t.Fatalf("redirect = %q", to)
	}
	st := e.svc.Status()
	if st.State != StateConnected || st.Email != "dev@fake-drive.local" || st.Folder != nil || st.Reason != "" {
		t.Fatalf("Status = %+v", st)
	}
	row, ok, err := e.st.Drive(ctx)
	if err != nil || !ok {
		t.Fatalf("Drive row: %v %v", ok, err)
	}
	plain, err := secret.Open(testConfig().SecretKey, row.RefreshTokenEnc)
	if err != nil || plain != e.client.lastRefresh() {
		t.Fatalf("sealed token does not open to the issued one: %v", err)
	}
	if row.ConnectedBy != "a@x" || !row.ConnectedAt.Equal(e.clock.now()) || row.State != "connected" {
		t.Fatalf("row = %+v", row)
	}
	if e.changes.Load() == 0 {
		t.Fatal("OnChange not called")
	}
	// The state is single use.
	if _, err := e.svc.FinishConnect(ctx, "a@x", "unknown", "fake-unknown", ""); !errors.Is(err, ErrBadState) {
		t.Fatalf("unknown state = %v", err)
	}
}

func TestFinishConnectForeignStateRejected(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	state := e.startState(t, "a@x", "/settings")
	if _, err := e.svc.FinishConnect(ctx, "b@x", state, "fake-"+state, ""); !errors.Is(err, ErrBadState) {
		t.Fatalf("another actor = %v, want ErrBadState", err)
	}
	// The foreign use burned it.
	if _, err := e.svc.FinishConnect(ctx, "a@x", state, "fake-"+state, ""); !errors.Is(err, ErrBadState) {
		t.Fatalf("reuse = %v, want ErrBadState", err)
	}
	old := e.startState(t, "a@x", "/settings")
	e.clock.advance(11 * time.Minute)
	if _, err := e.svc.FinishConnect(ctx, "a@x", old, "fake-"+old, ""); !errors.Is(err, ErrBadState) {
		t.Fatalf("expired = %v, want ErrBadState", err)
	}
	if st := e.svc.Status(); st.State != StateNotConnected {
		t.Fatalf("Status = %+v", st)
	}
}

func TestFinishConnectDeclined(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	state := e.startState(t, "a@x", "/projects/3/database?backup=k")
	to, err := e.svc.FinishConnect(ctx, "a@x", state, "", "access_denied")
	if err != nil || to != "/projects/3/database?backup=k&drive=declined" {
		t.Fatalf("declined = %q, %v", to, err)
	}
	state = e.startState(t, "a@x", "//evil.com")
	to, err = e.svc.FinishConnect(ctx, "a@x", state, "", "")
	if err != nil || to != "/settings?drive=declined" {
		t.Fatalf("no code = %q, %v", to, err)
	}
	if _, ok, _ := e.st.Drive(ctx); ok {
		t.Fatal("a declined connect stored a row")
	}
	if st := e.svc.Status(); st.State != StateNotConnected {
		t.Fatalf("Status = %+v", st)
	}
}

func TestFinishConnectExchangeError(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	state := e.startState(t, "a@x", "/settings")
	to, err := e.svc.FinishConnect(ctx, "a@x", state, "fake-other", "")
	if err != nil || to != "/settings?drive=error&code=token_expired" {
		t.Fatalf("bad code = %q, %v", to, err)
	}
}

func TestReconnectSameAccountKeepsFolder(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	f, err := e.svc.CreateFolder(ctx, "  server-backups ")
	if err != nil || f.Name != "server-backups" {
		t.Fatalf("CreateFolder = %+v, %v", f, err)
	}
	e.connect(t, "b@x")
	st := e.svc.Status()
	if st.Folder == nil || *st.Folder != f {
		t.Fatalf("Status after reconnect = %+v, want folder %+v", st, f)
	}
	if row, _, _ := e.st.Drive(ctx); row.FolderID != f.ID || row.ConnectedBy != "b@x" {
		t.Fatalf("row = %+v", row)
	}
}

func TestOtherAccountClearsFolder(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	if _, err := e.svc.CreateFolder(ctx, "server-backups"); err != nil {
		t.Fatal(err)
	}
	e.client.mu.Lock()
	e.client.email = "other@example.com"
	e.client.mu.Unlock()
	e.connect(t, "a@x")
	st := e.svc.Status()
	if st.Email != "other@example.com" || st.Folder != nil {
		t.Fatalf("Status = %+v, want the other account with no folder", st)
	}
	if row, _, _ := e.st.Drive(ctx); row.FolderID != "" {
		t.Fatalf("row folder = %q", row.FolderID)
	}
}

func TestStatusNeverHitsStore(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	if _, err := e.svc.CreateFolder(ctx, "server-backups"); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Close(); err != nil {
		t.Fatal(err)
	}
	st := e.svc.Status()
	if st.State != StateConnected || st.Folder == nil || st.Folder.Name != "server-backups" {
		t.Fatalf("Status with the store closed = %+v", st)
	}
	// Status hands out a copy.
	st.Folder.Name = "changed"
	if e.svc.Status().Folder.Name != "server-backups" {
		t.Fatal("Status shares its folder")
	}
}

func TestLoadFromStore(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	f, err := e.svc.CreateFolder(ctx, "server-backups")
	if err != nil {
		t.Fatal(err)
	}
	again := e.newService(t, testConfig())
	if st := again.Status(); st.State != StateConnected || st.Folder == nil || *st.Folder != f {
		t.Fatalf("reloaded Status = %+v", st)
	}
	// Another key cannot open the token: the connection is expired.
	cfg := testConfig()
	cfg.SecretKey = bytes.Repeat([]byte{9}, 32)
	other := e.newService(t, cfg)
	if st := other.Status(); st.State != StateExpired {
		t.Fatalf("Status with another key = %+v, want expired", st)
	}
	if row, _, _ := e.st.Drive(ctx); row.State != "expired" {
		t.Fatalf("row state = %q", row.State)
	}
}

func TestSelectFolder(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	if _, err := e.svc.Folders(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Folders before connect = %v", err)
	}
	e.connect(t, "a@x")
	sess := e.client.Session(e.client.lastRefresh())
	a, _ := sess.CreateFolder(ctx, "a", "")
	b, _ := sess.CreateFolder(ctx, "b", "")
	list, err := e.svc.Folders(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("Folders = %+v, %v", list, err)
	}
	before := e.changes.Load()
	got, err := e.svc.SelectFolder(ctx, b.ID)
	if err != nil || got != b {
		t.Fatalf("SelectFolder = %+v, %v", got, err)
	}
	if st := e.svc.Status(); st.Folder == nil || *st.Folder != b {
		t.Fatalf("Status = %+v", st)
	}
	if e.changes.Load() == before {
		t.Fatal("OnChange not called on a folder change")
	}
	_, err = e.svc.SelectFolder(ctx, "nope")
	wantCode(t, err, CodeFolderMissing)
	_ = a
}

func TestCreateFolderNames(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	for _, bad := range []string{"", "   ", "a/b", strings.Repeat("é", 101)} {
		if _, err := e.svc.CreateFolder(ctx, bad); !errors.Is(err, ErrBadName) {
			t.Errorf("CreateFolder(%q) = %v, want ErrBadName", bad, err)
		}
	}
	if _, err := e.svc.CreateFolder(ctx, strings.Repeat("é", 100)); err != nil {
		t.Errorf("100 characters: %v", err)
	}
	// The same name selects the folder there is.
	a, _ := e.svc.CreateFolder(ctx, "server-backups")
	b, _ := e.svc.CreateFolder(ctx, "server-backups")
	if a != b {
		t.Errorf("same name gave %+v and %+v", a, b)
	}
}

func TestTestUploadsAndDeletes(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	wantCode(t, e.svc.Test(ctx), CodeFolderMissing)
	f, err := e.svc.CreateFolder(ctx, "server-backups")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}
	entries, _ := filepath.Glob(filepath.Join(e.dir, f.ID, "*"))
	if len(entries) != 0 {
		t.Fatalf("the test file was left: %v", entries)
	}
}

func TestTestBusy(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	if _, err := e.svc.CreateFolder(ctx, "server-backups"); err != nil {
		t.Fatal(err)
	}
	e.client.hold = make(chan struct{})
	e.client.entered = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- e.svc.Test(ctx) }()
	<-e.client.entered
	if err := e.svc.Test(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Test = %v, want ErrBusy", err)
	}
	close(e.client.hold)
	if err := <-done; err != nil {
		t.Fatalf("first Test: %v", err)
	}
	e.client.hold = nil
	if err := e.svc.Test(ctx); err != nil {
		t.Fatalf("Test after: %v", err)
	}
}

func TestSessionTokenExpiredFlipsState(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	f, err := e.svc.CreateFolder(ctx, "server-backups")
	if err != nil {
		t.Fatal(err)
	}
	sess, folder, err := e.svc.Session(ctx)
	if err != nil || folder != f {
		t.Fatalf("Session = %v, %+v", err, folder)
	}
	if err := e.client.Session(e.client.lastRefresh()).Revoke(ctx); err != nil {
		t.Fatal(err)
	}
	before := e.changes.Load()
	_, _, err = sess.Upload(ctx, folder.ID, "x", 1, bytes.NewReader([]byte("x")), nil)
	wantCode(t, err, CodeTokenExpired)
	if st := e.svc.Status(); st.State != StateExpired || st.Email != "dev@fake-drive.local" {
		t.Fatalf("Status = %+v, want expired", st)
	}
	if row, _, _ := e.st.Drive(ctx); row.State != "expired" {
		t.Fatalf("row state = %q", row.State)
	}
	if e.changes.Load() == before {
		t.Fatal("OnChange not called")
	}
	_, _, err = e.svc.Session(ctx)
	wantCode(t, err, CodeTokenExpired)
	// A reconnect makes it connected again, and the old session cannot flip it back.
	e.connect(t, "a@x")
	_, _ = sess.ListFolders(ctx)
	if st := e.svc.Status(); st.State != StateConnected {
		t.Fatalf("Status after reconnect = %+v", st)
	}
}

func TestDisconnect(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	refresh := e.client.lastRefresh()
	before := e.changes.Load()
	if err := e.svc.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if st := e.svc.Status(); st.State != StateNotConnected || st.Email != "" {
		t.Fatalf("Status = %+v", st)
	}
	if _, ok, _ := e.st.Drive(ctx); ok {
		t.Fatal("row kept")
	}
	if e.changes.Load() == before {
		t.Fatal("OnChange not called")
	}
	_, err := e.client.Session(refresh).Account(ctx)
	wantCode(t, err, CodeTokenExpired)
	if _, _, err := e.svc.Session(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Session after disconnect = %v", err)
	}
	// Disconnecting again is fine.
	if err := e.svc.Disconnect(ctx); err != nil {
		t.Fatalf("second Disconnect: %v", err)
	}
}

func TestNoTokenInStatusOrErrors(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.connect(t, "a@x")
	if _, err := e.svc.CreateFolder(ctx, "server-backups"); err != nil {
		t.Fatal(err)
	}
	refresh := e.client.lastRefresh()
	row, _, _ := e.st.Drive(ctx)
	sealed := row.RefreshTokenEnc

	var texts []string
	add := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, string(b))
		if err, ok := v.(error); ok && err != nil {
			texts = append(texts, err.Error())
		}
	}
	add(e.svc.Status())
	_, err := e.svc.CreateFolder(ctx, "a/b")
	add(err)
	_, err = e.svc.FinishConnect(ctx, "a@x", "nope", "fake-nope", "")
	add(err)
	if err := e.client.Session(refresh).Revoke(ctx); err != nil {
		t.Fatal(err)
	}
	add(e.svc.Test(ctx))
	_, _, err = e.svc.Session(ctx)
	add(err)
	_, err = e.svc.Folders(ctx)
	add(err)
	add(e.svc.Status())
	add(e.svc.Disconnect(ctx))
	for _, s := range texts {
		if strings.Contains(s, refresh) || strings.Contains(s, sealed) || strings.Contains(s, sealed[3:]) {
			t.Fatalf("%q carries the token", s)
		}
	}
}
