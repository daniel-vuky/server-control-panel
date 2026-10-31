//go:build dev

package drive

import (
	"path/filepath"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

func TestDevClientFakeWithoutGoogleID(t *testing.T) {
	dir := t.TempDir()
	c, ok := NewClient(config.Config{PublicURL: "http://localhost:5173"}, dir).(*fake)
	if !ok {
		t.Fatal("a dev build without GOOGLE_CLIENT_ID is not the fake")
	}
	if c.dir != filepath.Join(dir, "fake-drive") || c.publicURL != "http://localhost:5173" {
		t.Fatalf("fake = %+v", c)
	}
	if _, ok := NewClient(config.Config{GoogleClientID: "id", GoogleClientSecret: "s"}, dir).(*Google); !ok {
		t.Fatal("a dev build with GOOGLE_CLIENT_ID is not Google")
	}
}
