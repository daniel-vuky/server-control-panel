package drive

import (
	"bytes"
	"context"
	"io"
	"testing"
)

// contractEnv is one implementation under the shared contract.
type contractEnv struct {
	client Client
	email  string
	// connect runs the consent flow and returns a refresh token.
	connect func(t *testing.T) string
}

const testRedirect = "https://panel.example.com/api/drive/callback"

func googleEnv(t *testing.T) contractEnv {
	m := newMockGoogle(t)
	c := m.client()
	return contractEnv{client: c, email: "owner@example.com", connect: func(t *testing.T) string {
		t.Helper()
		refresh, err := c.Exchange(context.Background(), m.issueCode(testRedirect), testRedirect)
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		return refresh
	}}
}

func fakeEnv(t *testing.T) contractEnv {
	c := NewFake(t.TempDir(), "http://localhost:5173")
	return contractEnv{client: c, email: "dev@fake-drive.local", connect: func(t *testing.T) string {
		t.Helper()
		_ = c.AuthURL("st-1", testRedirect)
		refresh, err := c.Exchange(context.Background(), "fake-st-1", testRedirect)
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		return refresh
	}}
}

func TestContractGoogle(t *testing.T) {
	setUploadChunk(t, 256<<10)
	runContract(t, googleEnv)
}

func TestContractFake(t *testing.T) { runContract(t, fakeEnv) }

// setUploadChunk lowers the resumable chunk size for one test.
func setUploadChunk(t *testing.T, n int) {
	old := uploadChunk
	uploadChunk = n
	t.Cleanup(func() { uploadChunk = old })
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := CodeOf(err); got != code {
		t.Fatalf("error = %v (code %q), want code %q", err, got, code)
	}
}

func runContract(t *testing.T, newEnv func(t *testing.T) contractEnv) {
	ctx := context.Background()

	t.Run("account", func(t *testing.T) {
		env := newEnv(t)
		email, err := env.client.Session(env.connect(t)).Account(ctx)
		if err != nil || email != env.email {
			t.Fatalf("Account = %q, %v; want %q", email, err, env.email)
		}
	})

	t.Run("exchange refuses an unknown or reused code", func(t *testing.T) {
		env := newEnv(t)
		if _, err := env.client.Exchange(ctx, "fake-nope", testRedirect); err == nil {
			t.Fatal("Exchange accepted an unknown code")
		}
	})

	t.Run("create and list folders", func(t *testing.T) {
		env := newEnv(t)
		s := env.client.Session(env.connect(t))
		b, err := s.CreateFolder(ctx, "b-backups", "")
		if err != nil {
			t.Fatalf("CreateFolder b: %v", err)
		}
		a, err := s.CreateFolder(ctx, "a-backups", "")
		if err != nil {
			t.Fatalf("CreateFolder a: %v", err)
		}
		if a.ID == "" || a.Name != "a-backups" || a.ID == b.ID {
			t.Fatalf("folders a=%+v b=%+v", a, b)
		}
		// A sub-folder (a project's) is not a destination folder.
		if _, err := s.CreateFolder(ctx, "shop", a.ID); err != nil {
			t.Fatalf("CreateFolder sub: %v", err)
		}
		got, err := s.ListFolders(ctx)
		if err != nil {
			t.Fatalf("ListFolders: %v", err)
		}
		want := []Folder{a, b}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("ListFolders = %+v, want %+v", got, want)
		}
	})

	t.Run("find or create is idempotent", func(t *testing.T) {
		env := newEnv(t)
		s := env.client.Session(env.connect(t))
		root, err := s.FindOrCreateFolder(ctx, "server-backups", "")
		if err != nil {
			t.Fatalf("FindOrCreateFolder: %v", err)
		}
		again, err := s.FindOrCreateFolder(ctx, "server-backups", "")
		if err != nil || again != root {
			t.Fatalf("second FindOrCreateFolder = %+v, %v; want %+v", again, err, root)
		}
		sub, err := s.FindOrCreateFolder(ctx, "it's-shop", root.ID)
		if err != nil {
			t.Fatalf("sub: %v", err)
		}
		sub2, err := s.FindOrCreateFolder(ctx, "it's-shop", root.ID)
		if err != nil || sub2 != sub || sub.ID == root.ID {
			t.Fatalf("sub again = %+v, %v; first %+v", sub2, err, sub)
		}
		other, err := s.FindOrCreateFolder(ctx, "it's-shop", "")
		if err != nil || other.ID == sub.ID {
			t.Fatalf("same name under root = %+v, %v; want a new folder", other, err)
		}
	})

	t.Run("upload, download, delete", func(t *testing.T) {
		env := newEnv(t)
		s := env.client.Session(env.connect(t))
		folder, err := s.CreateFolder(ctx, "server-backups", "")
		if err != nil {
			t.Fatal(err)
		}
		data := bytes.Repeat([]byte("0123456789abcdef"), (3*(256<<10)+17)/16)
		var seen []int64
		id, link, err := s.Upload(ctx, folder.ID, "shop_db_20261030-1000.sql.gz", int64(len(data)), bytes.NewReader(data),
			func(n int64) { seen = append(seen, n) })
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if id == "" || link == "" {
			t.Fatalf("Upload = %q, %q", id, link)
		}
		if len(seen) == 0 || seen[len(seen)-1] != int64(len(data)) {
			t.Fatalf("progress = %v, want it to end at %d", seen, len(data))
		}
		for i := 1; i < len(seen); i++ {
			if seen[i] < seen[i-1] {
				t.Fatalf("progress went back: %v", seen)
			}
		}
		rc, err := s.Download(ctx, id)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("Download read %d bytes, err %v; want the %d uploaded", len(got), err, len(data))
		}
		if err := s.Delete(ctx, id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err = s.Download(ctx, id)
		wantCode(t, err, CodeFolderMissing)
	})

	t.Run("empty upload", func(t *testing.T) {
		env := newEnv(t)
		s := env.client.Session(env.connect(t))
		folder, err := s.CreateFolder(ctx, "f", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Upload(ctx, folder.ID, "empty.txt", 0, bytes.NewReader(nil), nil); err != nil {
			t.Fatalf("Upload empty: %v", err)
		}
	})

	t.Run("deleted folder is folder_missing", func(t *testing.T) {
		env := newEnv(t)
		s := env.client.Session(env.connect(t))
		folder, err := s.CreateFolder(ctx, "server-backups", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, folder.ID); err != nil {
			t.Fatalf("Delete folder: %v", err)
		}
		_, _, err = s.Upload(ctx, folder.ID, "x.sql.gz", 3, bytes.NewReader([]byte("abc")), nil)
		wantCode(t, err, CodeFolderMissing)
		_, err = s.FindOrCreateFolder(ctx, "shop", folder.ID)
		wantCode(t, err, CodeFolderMissing)
		got, err := s.ListFolders(ctx)
		if err != nil || len(got) != 0 {
			t.Fatalf("ListFolders after delete = %+v, %v", got, err)
		}
	})

	t.Run("revoked token is token_expired", func(t *testing.T) {
		env := newEnv(t)
		refresh := env.connect(t)
		s := env.client.Session(refresh)
		if _, err := s.Account(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.Revoke(ctx); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		_, err := s.ListFolders(ctx)
		wantCode(t, err, CodeTokenExpired)
		_, err = env.client.Session(refresh).Account(ctx)
		wantCode(t, err, CodeTokenExpired)
	})
}
