//go:build !dev

package backup

import (
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// TestDevMinFreeOverrideIgnoredInRelease: a release build never reads
// PANEL_DEV_BACKUP_MIN_FREE, so the manager's override stays 0.
func TestDevMinFreeOverrideIgnoredInRelease(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		if k == "PANEL_DEV_BACKUP_MIN_FREE" {
			return "1000000000000000"
		}
		return ""
	})
	if err != nil || cfg.DevBackupMinFree != 0 {
		t.Fatalf("DevBackupMinFree = %d, %v; want 0", cfg.DevBackupMinFree, err)
	}
}
