package alerts

import (
	"context"
	"sync"
	"time"
)

// outboxSize is how many delivered messages the fake senders keep.
const outboxSize = 50

// FakeFactory is the fake senders of dev builds and tests: a send succeeds
// and is kept in a ring of the newest 50, unless Fail has a function for the
// channel that returns an error. The fake email counts as set up.
type FakeFactory struct {
	// Fail is the per-channel failure seam (ChannelTelegram, ChannelEmail):
	// a nil function, or one returning nil, means the send succeeds. Set it
	// before sending, or through SetFail while sends may run.
	Fail map[string]func(Message) error

	mu     sync.Mutex
	outbox []OutboxEntry
	log    func(OutboxEntry) // called after a delivery; nil logs nothing
}

// NewFakeFactory returns fake senders that all succeed.
func NewFakeFactory() *FakeFactory {
	return &FakeFactory{Fail: map[string]func(Message) error{}}
}

// SetFail sets the channel's failure function under the lock, so a test can
// change it while a sender runs.
func (f *FakeFactory) SetFail(channel string, fn func(Message) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail == nil {
		f.Fail = map[string]func(Message) error{}
	}
	f.Fail[channel] = fn
}

// Telegram returns the fake Telegram sender; the token and chat are ignored.
func (f *FakeFactory) Telegram(_, _ string) Sender { return fakeSender{f: f, channel: ChannelTelegram} }

// Email returns the fake email sender; the recipients are ignored.
func (f *FakeFactory) Email(_ []string) Sender { return fakeSender{f: f, channel: ChannelEmail} }

// EmailReason is "": the fake email is always set up.
func (f *FakeFactory) EmailReason() string { return "" }

// Outbox returns a copy of the newest 50 delivered messages, oldest first.
func (f *FakeFactory) Outbox() []OutboxEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append(make([]OutboxEntry, 0, len(f.outbox)), f.outbox...)
}

type fakeSender struct {
	f       *FakeFactory
	channel string
}

func (s fakeSender) Send(_ context.Context, m Message) error {
	f := s.f
	f.mu.Lock()
	fail := f.Fail[s.channel]
	f.mu.Unlock()
	if fail != nil {
		if err := fail(m); err != nil {
			return err
		}
	}
	e := OutboxEntry{At: time.Now(), Channel: s.channel, Text: m.Text()}
	f.mu.Lock()
	f.outbox = append(f.outbox, e)
	if len(f.outbox) > outboxSize {
		f.outbox = append([]OutboxEntry(nil), f.outbox[len(f.outbox)-outboxSize:]...)
	}
	log := f.log
	f.mu.Unlock()
	if log != nil {
		log(e)
	}
	return nil
}
