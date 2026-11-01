//go:build !dev

package alerts

import "github.com/daniel-vuky/server-control-panel/internal/config"

// NewFactory returns the real Telegram and SMTP senders. A release build never
// uses the fake senders. host gives the panel's host name for email subjects.
func NewFactory(cfg config.Config, host func() string) Factory {
	return newRealFactory(cfg, host)
}
