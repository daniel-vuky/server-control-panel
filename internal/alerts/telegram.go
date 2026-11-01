package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

// TelegramAPI is the Bot API base URL.
const TelegramAPI = "https://api.telegram.org"

const (
	telegramTimeout = 10 * time.Second
	telegramMaxBody = 64 << 10
	descriptionMax  = 200
	// textMax is where a message is cut: Telegram refuses a text over 4096
	// characters.
	textMax = 4000
)

// The Telegram words.
const (
	wordsBadToken     = "Telegram refused the bot token."
	wordsChatNotFound = "Chat not found: send the bot a message first, or add it to the group."
	wordsBlocked      = "The bot was blocked or removed from the chat."
	wordsRateLimited  = "Telegram is rate-limiting the bot."
	wordsNoTelegram   = "Could not reach Telegram."
)

// Telegram sends a message through a bot with sendMessage. The request URL
// holds the bot token, so no error it returns ever wraps or quotes the HTTP
// client's error (a *url.Error prints the URL). It holds the plain token for
// one delivery only: a fresh sender is built for each.
type Telegram struct {
	base   string
	token  string
	chatID string
	client *http.Client
}

// NewTelegram returns a sender for the bot token and chat id. c may be nil
// (a 10 s timeout); its timeout is kept when set. Either way the client never
// follows a redirect (it would resend the token), never uses a proxy and keeps
// no idle connection.
func NewTelegram(baseURL, token, chatID string, c *http.Client) *Telegram {
	var client http.Client
	if c != nil {
		client = *c
	}
	if client.Timeout == 0 {
		client.Timeout = telegramTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	rt := client.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if tr, ok := rt.(*http.Transport); ok {
		tr = tr.Clone()
		tr.Proxy = nil
		tr.DisableKeepAlives = true
		rt = tr
	}
	client.Transport = rt
	return &Telegram{base: strings.TrimRight(baseURL, "/"), token: token, chatID: chatID, client: &client}
}

// Send posts the message as plain text (no parse mode, so nothing needs
// escaping) with link previews off, cut to 4000 characters.
func (t *Telegram) Send(ctx context.Context, m Message) error {
	form := url.Values{
		"chat_id":                  {t.chatID},
		"text":                     {cutText(m.Text())},
		"disable_web_page_preview": {"true"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/bot"+t.token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		// The error quotes the URL, so it is dropped.
		return &SendError{Code: CodeFailed, Words: "Could not build the Telegram request."}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		// A timeout, a refused connection, a TLS failure or a cancel: all read
		// "Could not reach Telegram.". The *url.Error holds the URL with the
		// token, so it is never wrapped, quoted or logged.
		return &SendError{Code: CodeUnreachable, Words: wordsNoTelegram, Retry: true}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, telegramMaxBody))
	var reply struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	jsonErr := json.Unmarshal(body, &reply)
	return t.answer(resp.StatusCode, reply.OK && jsonErr == nil, reply.Description)
}

// answer maps Telegram's status and description to nil or a SendError.
func (t *Telegram) answer(status int, ok bool, description string) error {
	switch {
	case status == http.StatusOK && ok:
		return nil
	case status == http.StatusOK:
		return &SendError{Code: CodeFailed, Words: "Telegram answered with something that is not a Telegram reply."}
	case status == http.StatusUnauthorized, status == http.StatusNotFound:
		// 404 is Telegram's answer to a token it cannot parse.
		return &SendError{Code: CodeBadToken, Words: wordsBadToken}
	case status == http.StatusBadRequest && strings.Contains(strings.ToLower(description), "chat not found"):
		return &SendError{Code: CodeChatNotFound, Words: wordsChatNotFound}
	case status == http.StatusForbidden:
		return &SendError{Code: CodeBlocked, Words: wordsBlocked}
	case status == http.StatusTooManyRequests:
		return &SendError{Code: CodeRateLimited, Words: wordsRateLimited, Retry: true}
	case status >= http.StatusInternalServerError:
		return &SendError{Code: CodeUnreachable, Words: wordsNoTelegram, Retry: true}
	case status == http.StatusBadRequest && description != "":
		return &SendError{Code: CodeFailed, Words: "Telegram refused the message: " + t.clean(description)}
	default:
		return &SendError{Code: CodeFailed, Words: fmt.Sprintf("Telegram answered HTTP %d.", status)}
	}
}

// cutText keeps the beginning of a text longer than textMax runes and ends it
// with "…", textMax runes in all.
func cutText(s string) string {
	if utf8.RuneCountInString(s) <= textMax {
		return s
	}
	return string([]rune(s)[:textMax-1]) + "…"
}

// clean makes Telegram's description one short line with the token masked.
func (t *Telegram) clean(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > descriptionMax {
		s = string(r[:descriptionMax]) + "…"
	}
	return runner.Mask(s, []string{t.token})
}
