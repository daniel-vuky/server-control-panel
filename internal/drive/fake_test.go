package drive

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeAuthURL(t *testing.T) {
	c := NewFake(t.TempDir(), "http://localhost:5173")
	u, err := url.Parse(c.AuthURL("st-9", testRedirect))
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "http://localhost:5173/api/drive/fake-consent" {
		t.Fatalf("fake auth URL = %s", got)
	}
	if u.Query().Get("state") != "st-9" || u.Query().Get("redirect") != testRedirect {
		t.Fatalf("fake auth query = %s", u.RawQuery)
	}
}

func TestFakeExchangeOnlyIssuedCodes(t *testing.T) {
	ctx := context.Background()
	c := NewFake(t.TempDir(), "http://localhost:5173")
	if _, err := c.Exchange(ctx, "fake-never-issued", testRedirect); err == nil {
		t.Fatal("accepted a code for a state never issued")
	}
	_ = c.AuthURL("st-1", testRedirect)
	if _, err := c.Exchange(ctx, "st-1", testRedirect); err == nil {
		t.Fatal("accepted a code without the fake- prefix")
	}
	if _, err := c.Exchange(ctx, "fake-st-1", "https://evil.example.com/cb"); err == nil {
		t.Fatal("accepted another redirect")
	}
	a, err := c.Exchange(ctx, "fake-st-1", testRedirect)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if _, err := c.Exchange(ctx, "fake-st-1", testRedirect); err == nil {
		t.Fatal("a code was used twice")
	}
	_ = c.AuthURL("st-2", testRedirect)
	b, err := c.Exchange(ctx, "fake-st-2", testRedirect)
	if err != nil || a == b || len(a) < 32 {
		t.Fatalf("refresh tokens %q and %q (err %v): want fresh random values", a, b, err)
	}
}

func TestFakePersistsAndStoresFilesByFolder(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c := NewFake(dir, "http://localhost:5173")
	_ = c.AuthURL("st", testRedirect)
	refresh, err := c.Exchange(ctx, "fake-st", testRedirect)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Session(refresh)
	folder, err := s.CreateFolder(ctx, "server-backups", "")
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := s.Upload(ctx, folder.ID, "a.sql.gz", 3, bytes.NewReader([]byte("abc")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, folder.ID, "a.sql.gz")); err != nil || string(b) != "abc" { //nolint:gosec // G304: a file the fake wrote in the test's temp dir
		t.Fatalf("file on disk = %q, %v", b, err)
	}
	// A second fake over the same folder (a dev restart) still knows everything.
	s2 := NewFake(dir, "http://localhost:5173").Session(refresh)
	got, err := s2.ListFolders(ctx)
	if err != nil || len(got) != 1 || got[0] != folder {
		t.Fatalf("ListFolders after restart = %+v, %v", got, err)
	}
	if rc, err := s2.Download(ctx, id); err != nil {
		t.Fatalf("Download after restart: %v", err)
	} else {
		_ = rc.Close()
	}
	// The index never holds a refresh token in the clear.
	idx, err := os.ReadFile(filepath.Join(dir, "index.json")) //nolint:gosec // G304: the fake's index in the test's temp dir
	if err != nil || strings.Contains(string(idx), refresh) {
		t.Fatalf("index.json read %v; holds the token: %v", err, strings.Contains(string(idx), refresh))
	}
	// A size that does not match is refused and leaves no file.
	if _, _, err := s.Upload(ctx, folder.ID, "short.gz", 10, bytes.NewReader([]byte("abc")), nil); err == nil {
		t.Fatal("a short upload succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, folder.ID, "short.gz")); !os.IsNotExist(err) {
		t.Fatalf("short upload left a file: %v", err)
	}
	// A name that is not a plain file name is refused.
	for _, name := range []string{"", ".", "..", "a/b", "../x"} {
		if _, _, err := s.Upload(ctx, folder.ID, name, 1, bytes.NewReader([]byte("a")), nil); err == nil {
			t.Fatalf("Upload accepted the name %q", name)
		}
	}
	if err := s.Delete(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, folder.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted folder still on disk: %v", err)
	}
}
