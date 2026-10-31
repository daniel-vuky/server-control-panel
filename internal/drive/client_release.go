//go:build !dev

package drive

import "github.com/daniel-vuky/server-control-panel/internal/config"

// NewClient returns the Google Drive client. A release build never uses the
// fake Drive.
func NewClient(cfg config.Config, _ string) Client {
	return NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret)
}
