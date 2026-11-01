//go:build dev

package alerts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

func TestDevFactoryIsFakeUnlessRealAlerts(t *testing.T) {
	host := func() string { return "prod-01" }
	if _, ok := NewFactory(config.Config{}, host).(*FakeFactory); !ok {
		t.Fatalf("NewFactory without PANEL_DEV_REAL_ALERTS is %T, want *FakeFactory", NewFactory(config.Config{}, host))
	}
	realF := NewFactory(config.Config{DevRealAlerts: true}, host)
	if _, ok := realF.(*FakeFactory); ok {
		t.Fatal("PANEL_DEV_REAL_ALERTS=1 still gives the fakes")
	}
	if tg, ok := realF.Telegram(testToken, "1").(*Telegram); !ok || tg.base != TelegramAPI {
		t.Fatalf("real Telegram = %T", realF.Telegram(testToken, "1"))
	}
	if realF.Outbox() != nil {
		t.Fatal("the real senders have an outbox")
	}
}

func TestDevFactoryRecordsAndLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	f := NewFactory(config.Config{}, func() string { return "prod-01" })
	if f.EmailReason() != "" {
		t.Fatalf("EmailReason = %q, want the fake email to count as set up", f.EmailReason())
	}
	m := testMessage()
	if err := f.Email([]string{"admin@company.vn"}).Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := f.Telegram(testToken, "-1001234564821").Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	out := f.Outbox()
	if len(out) != 2 || out[0].Channel != ChannelEmail || out[1].Channel != ChannelTelegram {
		t.Fatalf("Outbox = %+v", out)
	}
	if out[0].Text != m.Text() || out[0].At.IsZero() {
		t.Fatalf("entry = %+v", out[0])
	}
	if strings.Count(buf.String(), "alerts: dev outbox") != 2 {
		t.Fatalf("log = %q, want one line per message", buf.String())
	}
	if strings.Contains(buf.String(), testToken) {
		t.Fatal("the log holds the token")
	}
}

func TestFakeOutboxKeepsNewest50(t *testing.T) {
	f := NewFakeFactory()
	s := f.Email([]string{"admin@company.vn"})
	for i := 1; i <= 60; i++ {
		if err := s.Send(context.Background(), Message{Title: fmt.Sprintf("alert %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	out := f.Outbox()
	if len(out) != 50 {
		t.Fatalf("len = %d, want 50", len(out))
	}
	if out[0].Text != "🔴 alert 11" || out[49].Text != "🔴 alert 60" {
		t.Fatalf("kept %q … %q, want alert 11 … alert 60", out[0].Text, out[49].Text)
	}
	out[0].Text = "changed"
	if f.Outbox()[0].Text == "changed" {
		t.Fatal("Outbox returned the ring itself")
	}
}

func TestFakeFailSeam(t *testing.T) {
	f := NewFakeFactory()
	want := &SendError{Code: CodeUnreachable, Words: "Could not reach Telegram.", Retry: true}
	f.SetFail(ChannelTelegram, func(Message) error { return want })
	if err := f.Telegram(testToken, "1").Send(context.Background(), testMessage()); !errors.Is(err, want) {
		t.Fatalf("err = %v, want the seam's", err)
	}
	if err := f.Email([]string{"a@b.c"}).Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("email failed: %v", err)
	}
	if out := f.Outbox(); len(out) != 1 || out[0].Channel != ChannelEmail {
		t.Fatalf("Outbox = %+v, want only the delivered email", out)
	}
	f.Fail[ChannelEmail] = func(Message) error { return errors.New("down") }
	if err := f.Email([]string{"a@b.c"}).Send(context.Background(), testMessage()); err == nil {
		t.Fatal("the Fail map entry was ignored")
	}
}
