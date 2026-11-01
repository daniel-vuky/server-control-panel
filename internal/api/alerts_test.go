package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/alerts"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// testBotToken is a bot token of the right shape; testBotTail is the part
// after the colon that must never leave the server.
const (
	testBotTail  = "AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	testBotToken = "123456789:" + testBotTail + "x"
)

// alertsService is the alerts service of the API tests: the fake senders
// over st, sealing with key (none when nil).
func alertsService(t *testing.T, st *store.Store, key []byte) *alerts.Service {
	t.Helper()
	svc, err := alerts.NewService(st, alerts.NewFakeFactory(), key, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func saveTelegram(t *testing.T, h http.Handler, token, chat string) AlertsConfig {
	t.Helper()
	rec := doJSON(h, http.MethodPost, "/api/alerts/telegram", TelegramRequest{Token: token, ChatID: chat})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/alerts/telegram = %d %s", rec.Code, rec.Body.String())
	}
	return decode[AlertsConfig](t, rec)
}

func TestAlertsConfigDefaults(t *testing.T) {
	h := NewRouter(newTestDeps(t))
	rec := do(h, http.MethodGet, "/api/alerts")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/alerts = %d %s", rec.Code, rec.Body.String())
	}
	got := decode[AlertsConfig](t, rec)
	want := AlertsConfig{
		Telegram:   AlertChannel{Available: true, Recipients: []string{}},
		Email:      AlertChannel{Available: true, Recipients: []string{}},
		Thresholds: AlertThresholds{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 5},
		CanSeal:    true,
	}
	if got.Telegram.Available != want.Telegram.Available || got.Telegram.Configured || got.Email.Configured ||
		!got.Email.Available || got.Thresholds != want.Thresholds || !got.CanSeal {
		t.Errorf("config = %+v, want %+v", got, want)
	}
	// Recipients are an empty list, never null.
	if !strings.Contains(rec.Body.String(), `"recipients":[]`) {
		t.Errorf("body = %s, want empty recipients lists", rec.Body.String())
	}
}

func TestAlertsConfigHasNoToken(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)

	saved := saveTelegram(t, h, testBotToken, "-1001234564821")
	tg := saved.Telegram
	if !tg.Configured || !tg.Enabled || tg.TokenHint != "••••••:AAH…" || tg.ChatID != "-100••••4821" {
		t.Errorf("telegram = %+v", tg)
	}
	rec := do(h, http.MethodGet, "/api/alerts")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, testBotTail) || strings.Contains(body, "1234564821") {
		t.Errorf("GET /api/alerts = %d %s, has the token or the chat id", rec.Code, body)
	}
	if !strings.Contains(body, "••••••:AAH…") {
		t.Errorf("GET /api/alerts = %s, want the hint", body)
	}
	for _, r := range activity(t, d) {
		if strings.Contains(r.Reason, testBotTail) {
			t.Errorf("activity reason %q holds the token", r.Reason)
		}
	}
}

func TestAlertsTelegram409WithoutKey(t *testing.T) {
	d := newTestDeps(t)
	d.Alerts = alertsService(t, d.Store, nil)
	h := NewRouter(d)

	rec := doJSON(h, http.MethodPost, "/api/alerts/telegram", TelegramRequest{Token: testBotToken, ChatID: "-1001234564821"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /api/alerts/telegram = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if got := decode[AlertRefused](t, rec); got != (AlertRefused{Error: "no_secret_key", Message: "Set PANEL_SECRET_KEY in panel.env first."}) {
		t.Errorf("body = %+v", got)
	}
	cfg := decode[AlertsConfig](t, do(h, http.MethodGet, "/api/alerts"))
	if cfg.CanSeal || cfg.Telegram.Available || cfg.Telegram.Reason != "Set PANEL_SECRET_KEY in panel.env first." {
		t.Errorf("config = %+v, want Telegram not available", cfg)
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("a refused save recorded %+v", rows)
	}
}

func TestAlertsFieldErrors(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	var addrs []string
	for c := 'a'; c <= 'k'; c++ {
		addrs = append(addrs, string(c)+"@example.com")
	}
	eleven := strings.Join(addrs, ",")
	for _, tc := range []struct {
		name, path string
		body       any
		want       FieldError
	}{
		{"no token", "/api/alerts/telegram", TelegramRequest{ChatID: "-100123"},
			FieldError{Field: "token", Code: "required"}},
		{"bad token", "/api/alerts/telegram", TelegramRequest{Token: "nope", ChatID: "-100123"},
			FieldError{Field: "token", Code: "invalid"}},
		{"bad chat", "/api/alerts/telegram", TelegramRequest{Token: testBotToken, ChatID: "@channel"},
			FieldError{Field: "chatId", Code: "invalid"}},
		{"no chat", "/api/alerts/telegram", TelegramRequest{Token: testBotToken},
			FieldError{Field: "chatId", Code: "required"}},
		{"no recipients", "/api/alerts/email", EmailRequest{Recipients: " , "},
			FieldError{Field: "recipients", Code: "required"}},
		{"bad recipient", "/api/alerts/email", EmailRequest{Recipients: "ops@example.com, not-an-address"},
			FieldError{Field: "recipients", Code: "invalid", Error: "“not-an-address” is not an email address."}},
		{"eleven recipients", "/api/alerts/email", EmailRequest{Recipients: eleven},
			FieldError{Field: "recipients", Code: "range"}},
		{"cpu 0", "/api/alerts/thresholds", AlertThresholds{RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 5},
			FieldError{Field: "cpuPercent", Code: "range", Error: "Enter a whole number from 1 to 100."}},
		{"ram 101", "/api/alerts/thresholds", AlertThresholds{CPUPercent: 90, RAMPercent: 101, DiskPercent: 85, SustainedMinutes: 5},
			FieldError{Field: "ramPercent", Code: "range", Error: "Enter a whole number from 1 to 100."}},
		{"disk fraction", "/api/alerts/thresholds", `{"cpuPercent":90,"ramPercent":90,"diskPercent":85.5,"sustainedMinutes":5}`,
			FieldError{Field: "diskPercent", Code: "range", Error: "Enter a whole number from 1 to 100."}},
		{"sustained 61", "/api/alerts/thresholds", AlertThresholds{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 61},
			FieldError{Field: "sustainedMinutes", Code: "range", Error: "Enter a whole number from 1 to 60."}},
		// Form order: the first field in error wins over a later fraction.
		{"cpu before a fraction", "/api/alerts/thresholds", `{"cpuPercent":0,"ramPercent":90,"diskPercent":85,"sustainedMinutes":5.5}`,
			FieldError{Field: "cpuPercent", Code: "range", Error: "Enter a whole number from 1 to 100."}},
		{"missing fields", "/api/alerts/thresholds", `{}`,
			FieldError{Field: "cpuPercent", Code: "range", Error: "Enter a whole number from 1 to 100."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(h, http.MethodPost, tc.path, tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST %s = %d %s, want 422", tc.path, rec.Code, rec.Body.String())
			}
			got := decode[FieldError](t, rec)
			if got.Field != tc.want.Field || got.Code != tc.want.Code || got.Error == "" ||
				(tc.want.Error != "" && got.Error != tc.want.Error) {
				t.Errorf("422 = %+v, want %+v", got, tc.want)
			}
		})
	}
	for _, path := range []string{"/api/alerts/telegram", "/api/alerts/email", "/api/alerts/thresholds", "/api/alerts/email/enabled"} {
		if rec := doJSON(h, http.MethodPost, path, `{"x":`); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s with bad JSON = %d, want 400", path, rec.Code)
		}
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("refused saves recorded %+v", rows)
	}
}

func TestAlertsRecordsActivity(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)

	post := func(path string, body any) AlertsConfig {
		t.Helper()
		rec := doJSON(h, http.MethodPost, path, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d %s", path, rec.Code, rec.Body.String())
		}
		return decode[AlertsConfig](t, rec)
	}
	type row struct{ action, target, reason string }
	var want []row
	check := func(step string) {
		t.Helper()
		rows := activity(t, d)
		if len(rows) != len(want) {
			t.Fatalf("%s: %d rows %+v, want %d", step, len(rows), rows, len(want))
		}
		got := rows[0]
		w := want[len(want)-1]
		if got.Action != w.action || got.Target != w.target || got.Reason != w.reason || got.Result != store.ResultOK ||
			got.Actor != "test@example.com" || got.ActorKind != store.ActorPerson {
			t.Errorf("%s: row = %+v, want %+v", step, got, w)
		}
	}

	cfg := post("/api/alerts/thresholds", AlertThresholds{CPUPercent: 80, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 10})
	if cfg.Thresholds != (AlertThresholds{CPUPercent: 80, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 10}) {
		t.Errorf("thresholds = %+v", cfg.Thresholds)
	}
	want = append(want, row{store.ActAlertsThresholds, "Alert thresholds", "CPU 90 → 80 % · sustained 5 → 10 min"})
	check("thresholds")
	post("/api/alerts/thresholds", AlertThresholds{CPUPercent: 80, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 10})
	check("the same thresholds again")

	post("/api/alerts/telegram", TelegramRequest{Token: testBotToken, ChatID: "-1001234564821"})
	want = append(want, row{store.ActAlertsChannel, "Telegram", "set up · chat -100••••4821"})
	check("telegram set up")
	post("/api/alerts/telegram", TelegramRequest{ChatID: "-1001234564821"})
	check("telegram unchanged with the token kept")
	post("/api/alerts/telegram", TelegramRequest{ChatID: "-1001234561177"})
	want = append(want, row{store.ActAlertsChannel, "Telegram", "chat -100••••4821 → -100••••1177"})
	check("telegram chat")

	cfg = post("/api/alerts/telegram/enabled", AlertEnabledRequest{Enabled: false})
	if cfg.Telegram.Enabled || !cfg.Telegram.Configured {
		t.Errorf("telegram = %+v, want configured and off", cfg.Telegram)
	}
	want = append(want, row{store.ActAlertsChannel, "Telegram", "on → off"})
	check("telegram off")
	post("/api/alerts/telegram/enabled", AlertEnabledRequest{Enabled: false})
	check("telegram off again")

	cfg = post("/api/alerts/email", EmailRequest{Recipients: "ops@example.com\nme@example.com"})
	if !cfg.Email.Configured || !cfg.Email.Enabled || strings.Join(cfg.Email.Recipients, ",") != "ops@example.com,me@example.com" {
		t.Errorf("email = %+v", cfg.Email)
	}
	want = append(want, row{store.ActAlertsChannel, "Email", "set up · ops@example.com, me@example.com"})
	check("email set up")

	// A test is not an alert and is not recorded, on or off.
	for _, ch := range []string{"telegram", "email"} {
		rec := doJSON(h, http.MethodPost, "/api/alerts/"+ch+"/test", map[string]any{})
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s test = %d %s", ch, rec.Code, rec.Body.String())
		}
		if got := decode[AlertTestResult](t, rec); got != (AlertTestResult{OK: true, Message: "Delivered"}) {
			t.Errorf("%s test = %+v", ch, got)
		}
	}
	check("tests")
}

func TestAlertsNotConfigured409(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	for _, path := range []string{"/api/alerts/telegram/enabled", "/api/alerts/email/enabled", "/api/alerts/telegram/test", "/api/alerts/email/test"} {
		rec := doJSON(h, http.MethodPost, path, AlertEnabledRequest{Enabled: true})
		if rec.Code != http.StatusConflict {
			t.Fatalf("POST %s = %d %s, want 409", path, rec.Code, rec.Body.String())
		}
		if got := decode[AlertRefused](t, rec); got.Error != "not_configured" || got.Message == "" {
			t.Errorf("POST %s = %+v", path, got)
		}
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("refused changes recorded %+v", rows)
	}
}

func TestAlertsUnknownChannel404(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	for _, path := range []string{"/api/alerts/slack/enabled", "/api/alerts/slack/test", "/api/alerts/thresholds/enabled", "/api/alerts/slack"} {
		if rec := doJSON(h, http.MethodPost, path, AlertEnabledRequest{Enabled: true}); rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d %s, want 404", path, rec.Code, rec.Body.String())
		}
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("unknown channels recorded %+v", rows)
	}
}

func TestAlertsNeedJSON(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	for _, path := range []string{
		"/api/alerts/telegram", "/api/alerts/email", "/api/alerts/thresholds",
		"/api/alerts/telegram/enabled", "/api/alerts/email/test",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"recipients":"ops@example.com","enabled":false}`))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s as a form = %d, want 415", path, rec.Code)
		}
	}
	if cfg := decode[AlertsConfig](t, do(h, http.MethodGet, "/api/alerts")); cfg.Email.Configured {
		t.Error("a form post set up email")
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("a refused POST recorded %+v", rows)
	}
}

func TestServerInfoUsesSavedThresholds(t *testing.T) {
	_, h := metricsSetup(t)
	before := decode[ServerInfo](t, do(h, http.MethodGet, "/api/server"))
	if before.Thresholds != (Thresholds{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85}) {
		t.Errorf("thresholds before = %+v", before.Thresholds)
	}
	rec := doJSON(h, http.MethodPost, "/api/alerts/thresholds", AlertThresholds{CPUPercent: 80, RAMPercent: 70, DiskPercent: 60, SustainedMinutes: 5})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST thresholds = %d %s", rec.Code, rec.Body.String())
	}
	after := decode[ServerInfo](t, do(h, http.MethodGet, "/api/server"))
	if after.Thresholds != (Thresholds{CPUPercent: 80, RAMPercent: 70, DiskPercent: 60}) {
		t.Errorf("thresholds after = %+v, want 80, 70, 60", after.Thresholds)
	}
}

// The alerts service reads no context of a request: a save lands even when
// the client went away (the store write outlives the request).
func TestAlertsSaveOutlivesClient(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/alerts/thresholds",
		strings.NewReader(`{"cpuPercent":70,"ramPercent":90,"diskPercent":85,"sustainedMinutes":5}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if th := d.Alerts.Thresholds(); th.CPUPercent != 70 {
		t.Errorf("thresholds = %+v, want CPU 70", th)
	}
	if rows := activity(t, d); len(rows) != 1 || rows[0].Action != store.ActAlertsThresholds {
		t.Errorf("activity = %+v, want the thresholds row", rows)
	}
}
