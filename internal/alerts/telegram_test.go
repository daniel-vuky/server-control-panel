package alerts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "123456789:AAHsecretsecretsecretsecretsecret_-x"

func testMessage() Message {
	return Message{
		Title:  "CPU at 92 % for 5 min (threshold 90 %)",
		Detail: "prod-01 has been over the threshold",
		Footer: "prod-01 · 10:31 (Asia/Ho_Chi_Minh)",
		Link:   "https://panel.example.com/server",
	}
}

func TestMessageText(t *testing.T) {
	m := testMessage()
	want := "🔴 CPU at 92 % for 5 min (threshold 90 %)\nprod-01 has been over the threshold\nprod-01 · 10:31 (Asia/Ho_Chi_Minh)\nhttps://panel.example.com/server"
	if got := m.Text(); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	r := Message{Title: "CPU back to 41 % (threshold 90 %)", Footer: "prod-01 · 10:40 (UTC)", Recovery: true}
	if got, want := r.Text(), "✅ CPU back to 41 % (threshold 90 %)\nprod-01 · 10:40 (UTC)"; got != want {
		t.Fatalf("recovery Text() = %q, want %q", got, want)
	}
	if got, want := m.Subject("prod-01"), "[prod-01] CPU at 92 % for 5 min (threshold 90 %)"; got != want {
		t.Fatalf("Subject = %q, want %q", got, want)
	}
}

type telegramCall struct {
	method, path, contentType string
	form                      url.Values
}

func TestTelegramSends(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []telegramCall
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		mu.Lock()
		calls = append(calls, telegramCall{r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.PostForm})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer srv.Close()

	m := testMessage()
	if err := NewTelegram(srv.URL, testToken, "-1001234564821", srv.Client()).Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.method != http.MethodPost || c.path != "/bot"+testToken+"/sendMessage" {
		t.Fatalf("request = %s %s", c.method, c.path)
	}
	if !strings.HasPrefix(c.contentType, "application/x-www-form-urlencoded") {
		t.Fatalf("Content-Type = %q", c.contentType)
	}
	if got := c.form.Get("chat_id"); got != "-1001234564821" {
		t.Fatalf("chat_id = %q", got)
	}
	if got := c.form.Get("text"); got != m.Text() {
		t.Fatalf("text = %q, want %q", got, m.Text())
	}
	if got := c.form.Get("disable_web_page_preview"); got != "true" {
		t.Fatalf("disable_web_page_preview = %q", got)
	}
	if _, ok := c.form["parse_mode"]; ok {
		t.Fatalf("parse_mode is set: %q", c.form.Get("parse_mode"))
	}
}

type telegramCase struct {
	name    string
	handler http.HandlerFunc
	closed  bool          // the server is closed before the send
	timeout time.Duration // the client timeout, when set
	code    string
	words   string
	retry   bool
}

func telegramCases() []telegramCase {
	answer := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}
	return []telegramCase{
		{name: "401", handler: answer(401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`),
			code: "bad_token", words: "Telegram refused the bot token."},
		{name: "404", handler: answer(404, `{"ok":false,"error_code":404,"description":"Not Found"}`),
			code: "bad_token", words: "Telegram refused the bot token."},
		{name: "400 chat not found", handler: answer(400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`),
			code: "chat_not_found", words: "Chat not found: send the bot a message first, or add it to the group."},
		{name: "403", handler: answer(403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`),
			code: "blocked", words: "The bot was blocked or removed from the chat."},
		{name: "429", handler: answer(429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5","parameters":{"retry_after":5}}`),
			code: "rate_limited", words: "Telegram is rate-limiting the bot.", retry: true},
		{name: "500", handler: answer(500, `oops`),
			code: "unreachable", words: "Could not reach Telegram.", retry: true},
		{name: "server closed", handler: answer(200, `{"ok":true}`), closed: true,
			code: "unreachable", words: "Could not reach Telegram.", retry: true},
		{name: "timeout", handler: func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-r.Context().Done():
			}
		}, timeout: 50 * time.Millisecond,
			code: "unreachable", words: "Could not reach Telegram.", retry: true},
		{name: "302", handler: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/elsewhere/bot"+testToken, http.StatusFound)
		}, code: "failed", words: "Telegram answered HTTP 302."},
		{name: "400 other", handler: answer(400, `{"ok":false,"error_code":400,"description":"Bad Request: message is too long"}`),
			code: "failed", words: "Telegram refused the message: Bad Request: message is too long"},
		{name: "200 not json", handler: answer(200, `<html>`),
			code: "failed", words: "Telegram answered with something that is not a Telegram reply."},
	}
}

// runTelegramCase sends one message for tc and returns the number of requests
// the server saw and the error.
func runTelegramCase(t *testing.T, tc telegramCase) (int, error) {
	t.Helper()
	var (
		mu    sync.Mutex
		count int
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		tc.handler(w, r)
	}))
	client := srv.Client()
	if tc.timeout > 0 {
		client.Timeout = tc.timeout
	}
	if tc.closed {
		srv.Close()
	} else {
		defer srv.Close()
	}
	err := NewTelegram(srv.URL, testToken, "-1001234564821", client).Send(context.Background(), testMessage())
	mu.Lock()
	defer mu.Unlock()
	return count, err
}

func TestTelegramErrorMapping(t *testing.T) {
	for _, tc := range telegramCases() {
		t.Run(tc.name, func(t *testing.T) {
			count, err := runTelegramCase(t, tc)
			var se *SendError
			if !errors.As(err, &se) {
				t.Fatalf("err = %T %v, want *SendError", err, err)
			}
			if se.Code != tc.code || se.Words != tc.words || se.Retry != tc.retry {
				t.Fatalf("SendError = %+v, want code %q words %q retry %v", *se, tc.code, tc.words, tc.retry)
			}
			if err.Error() != tc.words {
				t.Fatalf("Error() = %q, want the words", err.Error())
			}
			if tc.name == "302" && count != 1 {
				t.Fatalf("the redirect was followed: %d requests", count)
			}
		})
	}
}

func TestTelegramErrorsNeverContainToken(t *testing.T) {
	for _, tc := range telegramCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runTelegramCase(t, tc)
			if err == nil {
				t.Fatal("no error")
			}
			for _, s := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprintf("%v", errors.Unwrap(err))} {
				if strings.Contains(s, testToken) || strings.Contains(s, "/bot") || strings.Contains(s, "AAHsecret") {
					t.Fatalf("error text holds the token: %q", s)
				}
			}
		})
	}
}

// The default client never follows a redirect, never uses a proxy and stops
// after 10 s.
func TestTelegramDefaultClient(t *testing.T) {
	tg := NewTelegram(TelegramAPI, testToken, "1", nil)
	if tg.base != "https://api.telegram.org" {
		t.Fatalf("base = %q", tg.base)
	}
	if tg.client.Timeout != 10*time.Second {
		t.Fatalf("Timeout = %v", tg.client.Timeout)
	}
	if tg.client.CheckRedirect == nil || !errors.Is(tg.client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("CheckRedirect does not stop at the first response")
	}
	tr, ok := tg.client.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatalf("Transport = %T, proxy set: %v", tg.client.Transport, ok && tr.Proxy != nil)
	}
	if tr == http.DefaultTransport {
		t.Fatal("the shared default transport was changed")
	}
}
