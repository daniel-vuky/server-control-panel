//go:build dev

package backup

import (
	"context"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// TestDevMinFreeOverride: in a dev build PANEL_DEV_BACKUP_MIN_FREE raises the
// free space the disk check needs, so the "not enough space" refusal can be
// seen on a roomy disk.
func TestDevMinFreeOverride(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		if k == "PANEL_DEV_BACKUP_MIN_FREE" {
			return "1000000000000000"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, envOpt{noStart: true})
	e.m.SetMinFree(cfg.DevBackupMinFree)
	_, err = e.m.Enqueue(context.Background(), e.shop, keyPG, actor)
	r := refusalOf(t, err)
	if r.Code != CodeDiskShort || r.NeedBytes != 1000000000000000 || r.FreeBytes != e.free.Load() {
		t.Fatalf("refusal = %+v", r)
	}
	// Below the rule's own need, the override changes nothing.
	e.m.SetMinFree(1)
	if _, err := e.m.Enqueue(context.Background(), e.shop, keyPG, actor); err != nil {
		t.Errorf("with a small override: %v", err)
	}
}
