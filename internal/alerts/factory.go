package alerts

import (
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// Factory builds a fresh sender for each delivery, so nothing keeps an opened
// token longer than one send.
type Factory interface {
	Telegram(token, chatID string) Sender
	Email(to []string) Sender
	// EmailReason is "" when email can be used, else the sentence that says
	// what is missing.
	EmailReason() string
	// Outbox is what the dev fake senders delivered; nil for real senders.
	Outbox() []OutboxEntry
}

// OutboxEntry is one message a fake sender delivered.
type OutboxEntry struct {
	At      time.Time
	Channel string
	Text    string
}

// realFactory builds the Telegram and SMTP senders.
type realFactory struct {
	smtp config.SMTP
	host func() string
}

func newRealFactory(cfg config.Config, host func() string) realFactory {
	return realFactory{smtp: cfg.SMTP, host: host}
}

func (f realFactory) Telegram(token, chatID string) Sender {
	return NewTelegram(TelegramAPI, token, chatID, nil)
}

func (f realFactory) Email(to []string) Sender {
	host := ""
	if f.host != nil {
		host = f.host()
	}
	return NewEmail(f.smtp, to, host)
}

func (f realFactory) EmailReason() string {
	if !f.smtp.Configured() {
		return wordsEmailNotSet
	}
	return ""
}

func (realFactory) Outbox() []OutboxEntry { return nil }
