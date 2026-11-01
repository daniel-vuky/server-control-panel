// Package alerts sends the panel's alerts through Telegram and email, records
// them in activity and watches the server and the projects for problems.
package alerts

import (
	"context"
	"strings"
)

// The alert channels.
const (
	ChannelTelegram = "telegram"
	ChannelEmail    = "email"
)

// The SendError codes.
const (
	CodeBadToken         = "bad_token"
	CodeChatNotFound     = "chat_not_found"
	CodeBlocked          = "blocked"
	CodeRateLimited      = "rate_limited"
	CodeUnreachable      = "unreachable"
	CodeAuthFailed       = "auth_failed"
	CodeRecipientRefused = "recipient_refused"
	CodeNoTLS            = "no_tls"
	CodeFailed           = "failed"
)

// Message is one alert or recovery as it is sent: a title line, a detail line,
// a footer (host and time) and a link to the panel. Empty parts are left out.
type Message struct {
	Title, Detail, Footer, Link string
	Recovery                    bool
}

// Text is the plain-text message: the title after 🔴 (alert) or ✅ (recovery),
// then the detail, footer and link, one per line, empty lines dropped.
func (m Message) Text() string {
	icon := "🔴 "
	if m.Recovery {
		icon = "✅ "
	}
	lines := make([]string, 0, 4)
	for _, l := range []string{icon + m.Title, m.Detail, m.Footer, m.Link} {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// line is the message's detail line for its activity row, or its title when
// the message has a single line ("CPU at 92 % for 5 min (threshold 90 %)").
func (m Message) line() string {
	if m.Detail != "" {
		return m.Detail
	}
	return m.Title
}

// Subject is the email subject: "[<host>] <title>".
func (m Message) Subject(host string) string {
	return "[" + host + "] " + m.Title
}

// Sender delivers a message through one channel.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SendError is a failed delivery: a code, the user's words and whether the
// failure is worth one retry (a network error or a temporary refusal). The
// words are fixed or masked and never hold a token, a password or a URL.
type SendError struct {
	Code  string
	Words string
	Retry bool
}

func (e *SendError) Error() string { return e.Words }
