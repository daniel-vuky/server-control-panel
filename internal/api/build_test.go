//go:build !dev

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
)

// A release build has no fake Drive: no consent route, and NewClient is
// Google even without a client id.
func TestReleaseHasNoFakeDrive(t *testing.T) {
	h := NewRouter(newTestDeps(t))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := do(h, method, "/api/drive/fake-consent?state=x")
		if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"not found"}` {
			t.Errorf("%s /api/drive/fake-consent = %d %s, want the API 404", method, rec.Code, rec.Body.String())
		}
	}
	cfg := testDriveConfig()
	cfg.GoogleClientID = ""
	if c, ok := drive.NewClient(cfg, t.TempDir()).(*drive.Google); !ok || c == nil {
		t.Errorf("NewClient = %T, want *drive.Google", drive.NewClient(cfg, t.TempDir()))
	}
}
