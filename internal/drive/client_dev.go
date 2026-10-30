//go:build dev

package drive

import (
	"path/filepath"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// NewClient returns the Drive client for cfg. A dev build without
// GOOGLE_CLIENT_ID uses the fake Drive under <dataDir>/fake-drive.
func NewClient(cfg config.Config, dataDir string) Client {
	if cfg.GoogleClientID == "" {
		return NewFake(filepath.Join(dataDir, "fake-drive"), cfg.PublicURL)
	}
	return NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret)
}
