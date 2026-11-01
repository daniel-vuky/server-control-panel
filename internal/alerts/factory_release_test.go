//go:build !dev

package alerts

import (
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// A release build always sends for real: the fake senders must never reach a
// release binary.
func TestReleaseFactoryIsReal(t *testing.T) {
	host := func() string { return "prod-01" }
	for _, cfg := range []config.Config{{}, {DevRealAlerts: true}} {
		f := NewFactory(cfg, host)
		tg, ok := f.Telegram(testToken, "-1001234564821").(*Telegram)
		if !ok {
			t.Fatalf("Telegram = %T, want *Telegram", f.Telegram(testToken, "1"))
		}
		if tg.base != "https://api.telegram.org" {
			t.Fatalf("base = %q", tg.base)
		}
		if f.Outbox() != nil {
			t.Fatal("a release build has an outbox")
		}
		if got := f.EmailReason(); got != "Set SMTP_HOST and SMTP_FROM in panel.env, see docs/alerts.md" {
			t.Fatalf("EmailReason = %q", got)
		}
	}

	cfg := config.Config{SMTP: config.SMTP{Host: "smtp.example.com", Port: 587, From: "panel@example.com"}}
	f := NewFactory(cfg, host)
	if got := f.EmailReason(); got != "" {
		t.Fatalf("EmailReason with SMTP set = %q", got)
	}
	e, ok := f.Email([]string{"admin@company.vn"}).(*Email)
	if !ok {
		t.Fatalf("Email = %T, want *Email", f.Email(nil))
	}
	if e.host != "prod-01" || e.cfg.Host != "smtp.example.com" {
		t.Fatalf("Email = host %q, server %q", e.host, e.cfg.Host)
	}
}
