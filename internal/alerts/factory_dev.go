//go:build dev

package alerts

import (
	"log/slog"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// NewFactory returns the fake senders, which log each message and keep the
// newest 50 for the dev outbox, unless PANEL_DEV_REAL_ALERTS=1 asks for the
// real Telegram and SMTP senders.
func NewFactory(cfg config.Config, host func() string) Factory {
	if cfg.DevRealAlerts {
		return newRealFactory(cfg, host)
	}
	f := NewFakeFactory()
	f.log = func(e OutboxEntry) {
		slog.Info("alerts: dev outbox", "channel", e.Channel, "text", e.Text)
	}
	return f
}
