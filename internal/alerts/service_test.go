package alerts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

const (
	svcToken = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawX"
	svcChat  = "-1001234564821"
)

var discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))

var testKey = []byte("0123456789abcdef0123456789abcdef")

// recFactory records what the service hands the factory.
type recFactory struct {
	*FakeFactory
	reason string
	token  string
	chat   string
	to     []string
}

func (r *recFactory) Telegram(token, chat string) Sender {
	r.token, r.chat = token, chat
	return r.FakeFactory.Telegram(token, chat)
}

func (r *recFactory) Email(to []string) Sender {
	r.to = to
	return r.FakeFactory.Email(to)
}

func (r *recFactory) EmailReason() string { return r.reason }

func newTestService(t *testing.T, key []byte, reason string) (*Service, *store.Store, *recFactory) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &recFactory{FakeFactory: NewFakeFactory(), reason: reason}
	svc, err := NewService(st, f, key, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, f
}

func fieldOf(t *testing.T, err error) *FieldError {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v (%T), want *FieldError", err, err)
	}
	return fe
}

func TestSaveTelegramSealsAndHints(t *testing.T) {
	ctx := context.Background()
	svc, st, f := newTestService(t, testKey, "")
	changes, err := svc.SaveTelegram(ctx, svcToken, svcChat)
	if err != nil {
		t.Fatal(err)
	}
	if changes != "set up · chat -100••••4821" {
		t.Errorf("changes = %q", changes)
	}
	rows, err := st.AlertChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sealed := rows[store.ChannelTelegram].SealedToken
	if !strings.HasPrefix(sealed, "v1:") || strings.Contains(sealed, svcToken) {
		t.Errorf("sealed_token = %q", sealed)
	}
	v := svc.View()
	if v.Telegram.TokenHint != "••••••:AAH…" || v.Telegram.ChatID != "-100••••4821" {
		t.Errorf("hint = %q chat = %q", v.Telegram.TokenHint, v.Telegram.ChatID)
	}
	if !v.Telegram.Configured || !v.Telegram.Available || !v.CanSeal {
		t.Errorf("view = %+v", v.Telegram)
	}
	if dump := fmt.Sprintf("%+v", v); strings.Contains(dump, svcToken) || strings.Contains(dump, "AAHdqTcv") {
		t.Errorf("View holds the token: %s", dump)
	}
	targets, configured := svc.Targets()
	if len(targets) != 1 || configured != 1 || targets[0].Name != ChannelTelegram {
		t.Fatalf("targets = %v configured = %d", targets, configured)
	}
	if f.token != svcToken || f.chat != svcChat {
		t.Errorf("sender built with token %q chat %q", f.token, f.chat)
	}
}

func TestSaveTelegramKeepsTokenWhenEmpty(t *testing.T) {
	ctx := context.Background()
	svc, st, f := newTestService(t, testKey, "")
	if _, err := svc.SaveTelegram(ctx, svcToken, svcChat); err != nil {
		t.Fatal(err)
	}
	before, _ := st.AlertChannels(ctx)

	same, err := svc.SaveTelegram(ctx, svcToken, svcChat)
	if err != nil || same != "" {
		t.Fatalf("same token and chat: changes %q err %v", same, err)
	}
	changes, err := svc.SaveTelegram(ctx, "", "-1001234561177")
	if err != nil {
		t.Fatal(err)
	}
	if changes != "chat -100••••4821 → -100••••1177" {
		t.Errorf("changes = %q", changes)
	}
	after, _ := st.AlertChannels(ctx)
	if after[store.ChannelTelegram].SealedToken != before[store.ChannelTelegram].SealedToken {
		t.Error("the sealed token changed")
	}
	if _, _ = svc.Targets(); f.token != svcToken || f.chat != "-1001234561177" {
		t.Errorf("sender built with token %q chat %q", f.token, f.chat)
	}
	other := "987654321:BBBdqTcvCH1vGWJxfSeofSAs0K5PALDsawX"
	changes, err = svc.SaveTelegram(ctx, other, "-1001234561177")
	if err != nil || changes != "token changed" {
		t.Errorf("new token: changes %q err %v", changes, err)
	}
	if strings.Contains(changes, other) {
		t.Error("changes hold the token")
	}
}

func TestSaveTelegramNoKey(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newTestService(t, nil, "")
	if _, err := svc.SaveTelegram(ctx, svcToken, svcChat); !errors.Is(err, ErrNoSecretKey) {
		t.Fatalf("err = %v, want ErrNoSecretKey", err)
	}
	if rows, _ := st.AlertChannels(ctx); len(rows) != 0 {
		t.Errorf("stored %v", rows)
	}
	v := svc.View()
	if v.Telegram.Available || v.CanSeal || v.Telegram.Reason != "Set PANEL_SECRET_KEY in panel.env first." {
		t.Errorf("view = %+v", v)
	}
}

func TestSaveTelegramFieldErrors(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newTestService(t, testKey, "")
	for _, tc := range []struct{ token, chat, field string }{
		{"abc", svcChat, "token"},
		{"", svcChat, "token"}, // nothing stored to keep
		{svcToken, "@chan", "chatId"},
		{svcToken, "", "chatId"},
		{svcToken, "12ab", "chatId"},
	} {
		_, err := svc.SaveTelegram(ctx, tc.token, tc.chat)
		if fe := fieldOf(t, err); fe.Field != tc.field {
			t.Errorf("token %q chat %q: field = %q, want %q", tc.token, tc.chat, fe.Field, tc.field)
		}
	}
	if rows, _ := st.AlertChannels(ctx); len(rows) != 0 {
		t.Errorf("stored %v", rows)
	}
}

func TestSaveEmailRecipients(t *testing.T) {
	ctx := context.Background()
	svc, st, f := newTestService(t, testKey, "")
	changes, err := svc.SaveEmail(ctx, "a@x.io, b@x.io\nc@x.io")
	if err != nil {
		t.Fatal(err)
	}
	if want := "set up · a@x.io, b@x.io, c@x.io"; changes != want {
		t.Errorf("changes = %q, want %q", changes, want)
	}
	rows, _ := st.AlertChannels(ctx)
	if got := rows[store.ChannelEmail].Recipients; len(got) != 3 {
		t.Errorf("stored %v", got)
	}
	if v := svc.View(); len(v.Email.Recipients) != 3 || !v.Email.Available || !v.Email.Enabled {
		t.Errorf("view = %+v", v.Email)
	}
	if same, err := svc.SaveEmail(ctx, "a@x.io\nb@x.io\nc@x.io"); err != nil || same != "" {
		t.Errorf("same list: %q %v", same, err)
	}
	if _, configured := svc.Targets(); configured != 1 || len(f.to) != 3 {
		t.Errorf("configured %d to %v", configured, f.to)
	}

	var many []string
	for i := range 11 {
		many = append(many, fmt.Sprintf("u%d@x.io", i))
	}
	for _, bad := range []string{strings.Join(many, ","), "nope", "", "Bob <b@x.io>"} {
		if fe := fieldOf(t, errOf(svc.SaveEmail(ctx, bad))); fe.Field != "recipients" {
			t.Errorf("%q: field = %q", bad, fe.Field)
		}
	}

	unset, _, _ := newTestService(t, testKey, "Set SMTP_HOST and SMTP_FROM in panel.env, see docs/alerts.md")
	if _, err := unset.SaveEmail(ctx, "a@x.io"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
	if v := unset.View(); v.Email.Available || v.Email.Reason == "" {
		t.Errorf("view = %+v", v.Email)
	}
}

func errOf(_ string, err error) error { return err }

func TestNewChannelStartsOn(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, testKey, "")
	if _, err := svc.SaveTelegram(ctx, svcToken, svcChat); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveEmail(ctx, "a@x.io"); err != nil {
		t.Fatal(err)
	}
	v := svc.View()
	if !v.Telegram.Enabled || !v.Email.Enabled {
		t.Errorf("view = %+v", v)
	}
	targets, configured := svc.Targets()
	if len(targets) != 2 || configured != 2 || targets[0].Name != ChannelTelegram || targets[1].Name != ChannelEmail {
		t.Errorf("targets = %v configured = %d", targets, configured)
	}
}

func TestSetEnabledOffLeavesTargetsEmpty(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, testKey, "")
	if _, err := svc.SetEnabled(ctx, ChannelTelegram, false); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("not set up: err = %v", err)
	}
	if _, err := svc.SaveTelegram(ctx, svcToken, svcChat); err != nil {
		t.Fatal(err)
	}
	changes, err := svc.SetEnabled(ctx, ChannelTelegram, false)
	if err != nil || changes != "on → off" {
		t.Fatalf("changes %q err %v", changes, err)
	}
	if targets, configured := svc.Targets(); len(targets) != 0 || configured != 1 {
		t.Errorf("targets = %v configured = %d", targets, configured)
	}
	if v := svc.View(); v.Telegram.Enabled || !v.Telegram.Configured {
		t.Errorf("view = %+v", v.Telegram)
	}
	if again, err := svc.SetEnabled(ctx, ChannelTelegram, false); err != nil || again != "" {
		t.Errorf("again: %q %v", again, err)
	}
	// Saving the chat again keeps it off.
	if _, err := svc.SaveTelegram(ctx, "", "-1001234561177"); err != nil {
		t.Fatal(err)
	}
	if v := svc.View(); v.Telegram.Enabled {
		t.Error("a save turned the channel on")
	}
	if changes, err := svc.SetEnabled(ctx, ChannelTelegram, true); err != nil || changes != "off → on" {
		t.Errorf("on: %q %v", changes, err)
	}
}

func TestSetThresholdsRanges(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newTestService(t, testKey, "")
	if got := svc.Thresholds(); got != (metrics.Thresholds{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85}) || svc.Sustained() != 5*time.Minute {
		t.Fatalf("defaults = %+v %v", got, svc.Sustained())
	}
	const pct, mins = "Enter a whole number from 1 to 100.", "Enter a whole number from 1 to 60."
	for _, tc := range []struct {
		cpu, ram, disk, sus int
		field, msg          string
	}{
		{0, 90, 85, 5, "cpuPercent", pct},
		{101, 90, 85, 5, "cpuPercent", pct},
		{90, 0, 85, 5, "ramPercent", pct},
		{90, 101, 85, 5, "ramPercent", pct},
		{90, 90, 0, 5, "diskPercent", pct},
		{90, 90, 101, 5, "diskPercent", pct},
		{90, 90, 85, 0, "sustainedMinutes", mins},
		{90, 90, 85, 61, "sustainedMinutes", mins},
	} {
		fe := fieldOf(t, errOf(svc.SetThresholds(ctx, tc.cpu, tc.ram, tc.disk, tc.sus)))
		if fe.Field != tc.field || fe.Message != tc.msg || fe.Code != FieldRange {
			t.Errorf("%+v: got %+v", tc, *fe)
		}
	}
	if got, _ := st.AlertSettings(ctx); got.CPUPercent != 90 || got.SustainedMinutes != 5 {
		t.Errorf("a refused save wrote %+v", got)
	}

	changes, err := svc.SetThresholds(ctx, 80, 90, 85, 10)
	if err != nil || changes != "CPU 90 → 80 % · sustained 5 → 10 min" {
		t.Fatalf("changes %q err %v", changes, err)
	}
	if got := svc.Thresholds(); got.CPUPercent != 80 || svc.Sustained() != 10*time.Minute {
		t.Errorf("cache = %+v %v", got, svc.Sustained())
	}
	if v := svc.View(); v.Thresholds.CPUPercent != 80 || v.SustainedMinutes != 10 {
		t.Errorf("view = %+v", v)
	}
	if got, _ := st.AlertSettings(ctx); got.CPUPercent != 80 || got.SustainedMinutes != 10 {
		t.Errorf("stored %+v", got)
	}
	if again, err := svc.SetThresholds(ctx, 80, 90, 85, 10); err != nil || again != "" {
		t.Errorf("same values: %q %v", again, err)
	}
	if more, err := svc.SetThresholds(ctx, 80, 70, 60, 10); err != nil || more != "RAM 90 → 70 % · disk 85 → 60 %" {
		t.Errorf("more: %q %v", more, err)
	}
}

func TestServiceReloadsFromStore(t *testing.T) {
	ctx := context.Background()
	svc, st, f := newTestService(t, testKey, "")
	if _, err := svc.SaveTelegram(ctx, svcToken, svcChat); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetThresholds(ctx, 70, 71, 72, 3); err != nil {
		t.Fatal(err)
	}
	again, err := NewService(st, f, testKey, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	v := again.View()
	if v.Telegram.TokenHint != "••••••:AAH…" || v.Thresholds.RAMPercent != 71 || v.SustainedMinutes != 3 {
		t.Errorf("view = %+v", v)
	}
	if _, err := NewService(st, f, []byte("short"), discardLog); err == nil {
		t.Error("a short key was accepted")
	}
}

func TestTargetsLeaveOutUnopenableToken(t *testing.T) {
	ctx := context.Background()
	svc, st, f := newTestService(t, testKey, "")
	if _, err := svc.SaveTelegram(ctx, svcToken, svcChat); err != nil {
		t.Fatal(err)
	}
	// The same install restarted with another key cannot open the token.
	other, err := NewService(st, f, []byte("ffffffffffffffffffffffffffffffff"), discardLog)
	if err != nil {
		t.Fatal(err)
	}
	targets, configured := other.Targets()
	if len(targets) != 0 || configured != 1 {
		t.Errorf("targets = %v configured = %d", targets, configured)
	}
	if res, err := other.Test(ctx, ChannelTelegram); err != nil || res.OK || res.Message == "" {
		t.Errorf("test = %+v %v", res, err)
	}
}

func TestTestUsesChannelEvenWhenOff(t *testing.T) {
	ctx := context.Background()
	svc, _, f := newTestService(t, testKey, "")
	if _, err := svc.Test(ctx, ChannelTelegram); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("not set up: err = %v", err)
	}
	if _, err := svc.SaveTelegram(ctx, svcToken, svcChat); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetEnabled(ctx, ChannelTelegram, false); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Test(ctx, ChannelTelegram)
	if err != nil || !res.OK || res.Message != "Delivered" {
		t.Fatalf("test = %+v %v", res, err)
	}
	if out := svc.Outbox(); len(out) != 1 || out[0].Channel != ChannelTelegram {
		t.Errorf("outbox = %+v", out)
	}

	f.SetFail(ChannelTelegram, func(Message) error {
		return &SendError{Code: CodeBadToken, Words: "Telegram refused the bot token."}
	})
	if res, err := svc.Test(ctx, ChannelTelegram); err != nil || res.OK || res.Message != "Telegram refused the bot token." {
		t.Errorf("worded failure: %+v %v", res, err)
	}
	f.SetFail(ChannelTelegram, func(Message) error { return errors.New("dial tcp: " + svcToken) })
	res, err = svc.Test(ctx, ChannelTelegram)
	if err != nil || res.OK || strings.Contains(res.Message, svcToken) || res.Message == "" {
		t.Errorf("plain failure: %+v %v", res, err)
	}

	if _, err := svc.SaveEmail(ctx, "a@x.io"); err != nil {
		t.Fatal(err)
	}
	if res, err := svc.Test(ctx, ChannelEmail); err != nil || !res.OK {
		t.Errorf("email test = %+v %v", res, err)
	}
	if _, err := svc.Test(ctx, "sms"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("unknown kind: err = %v", err)
	}
}
