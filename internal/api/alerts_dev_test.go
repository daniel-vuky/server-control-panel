//go:build dev

package api

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// The dev outbox lists what the fake senders delivered, oldest first, with
// the time in the server zone.
func TestDevOutbox(t *testing.T) {
	d := newTestDeps(t)
	d.Zone = mustZone(t, "Asia/Ho_Chi_Minh")
	h := NewRouter(d)

	rec := do(h, http.MethodGet, "/api/alerts/dev-outbox")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty outbox = %d %s, want 200 []", rec.Code, rec.Body.String())
	}

	saveTelegram(t, h, testBotToken, "-1001234564821")
	if rec := doJSON(h, http.MethodPost, "/api/alerts/telegram/test", map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("test = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(h, http.MethodGet, "/api/alerts/dev-outbox")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/alerts/dev-outbox = %d %s", rec.Code, rec.Body.String())
	}
	got := decode[[]AlertOutboxEntry](t, rec)
	if len(got) != 1 || got[0].Channel != "telegram" || !strings.Contains(got[0].Text, "Test from Server Control Panel") {
		t.Fatalf("outbox = %+v", got)
	}
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\+07:00$`).MatchString(got[0].At) {
		t.Errorf("at = %q, want RFC 3339 at +07:00", got[0].At)
	}
}
