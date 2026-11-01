package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/secret"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The words a saved or refused setting shows.
const (
	wordsNoSecretKey = "Set PANEL_SECRET_KEY in panel.env first."
	wordsPercent     = "Enter a whole number from 1 to 100."
	wordsMinutes     = "Enter a whole number from 1 to 60."
	wordsBot         = "Enter the bot token from BotFather, like 123456789:AAH…"
	wordsChat        = "Enter the numeric chat id, like -1001234567890."
	wordsRecipients  = "Enter 1 to 10 email addresses, separated by commas or new lines."

	testTimeout  = 20 * time.Second
	maxRecipient = 10
)

// The FieldError codes.
const (
	FieldRequired = "required"
	FieldInvalid  = "invalid"
	FieldRange    = "range"
)

var (
	// ErrNoSecretKey refuses a Telegram save: without PANEL_SECRET_KEY the
	// bot token cannot be sealed.
	ErrNoSecretKey = errors.New("alerts: PANEL_SECRET_KEY is not set")
	// ErrNotConfigured refuses a change or a test of a channel that is not set
	// up.
	ErrNotConfigured = errors.New("alerts: channel is not configured")

	tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{35,}$`)
	chatPattern  = regexp.MustCompile(`^-?[0-9]+$`)
)

// FieldError is a value the owner can fix: the request field, a code
// (required, invalid or range) and the words to show.
type FieldError struct {
	Field, Code, Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// ChannelView is one channel as the API shows it: no token, only hints.
type ChannelView struct {
	Available, Configured, Enabled bool
	TokenHint, ChatID              string
	Recipients                     []string
	Reason                         string
}

// View is the alert settings as the API shows them.
type View struct {
	Telegram, Email  ChannelView
	Thresholds       metrics.Thresholds
	SustainedMinutes int
	CanSeal          bool
}

// TestResult is the answer to Send test.
type TestResult struct {
	OK      bool
	Message string
}

// Target is a channel a message goes to now.
type Target struct {
	Name   string
	Sender Sender
}

// chanState is a stored channel with what the views need from it. The plain
// bot token is never kept: only the sealed one and the hint.
type chanState struct {
	ch     store.AlertChannel
	exists bool
	hint   string
}

// Service holds the channel configuration and the thresholds: it reads both
// from the store at start, caches them and writes through on a save. The
// plain Telegram token is never kept: the sealed one is stored, and the hint
// is derived by opening the seal once at load (there is no hint column). It
// writes no activity; the API records what Save* and SetThresholds return.
// writeMu serialises the writers (Save*, SetEnabled, SetThresholds) from their
// cache read to their cache write, so a save never builds on a stale copy; it
// is taken by nothing outside the service, and held across store calls. mu
// guards the cache only, never a store or network call.
type Service struct {
	st  *store.Store
	f   Factory
	key []byte
	log *slog.Logger

	writeMu sync.Mutex

	mu         sync.RWMutex
	chans      map[string]chanState
	thresholds metrics.Thresholds
	sustained  int
}

// NewService loads the channels and thresholds. key is PANEL_SECRET_KEY (32
// bytes) or empty: without it Telegram cannot be set up.
func NewService(st *store.Store, f Factory, key []byte, log *slog.Logger) (*Service, error) {
	if len(key) != 0 && len(key) != 32 {
		return nil, errors.New("alerts: secret key must be 32 bytes")
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Service{st: st, f: f, key: key, log: log, chans: map[string]chanState{}}
	ctx := context.Background()
	rows, err := st.AlertChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: load channels: %w", err)
	}
	for kind, c := range rows {
		s.chans[kind] = chanState{ch: c, exists: true, hint: s.hintFor(c)}
	}
	a, err := st.AlertSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: load thresholds: %w", err)
	}
	s.thresholds = metrics.Thresholds{CPUPercent: a.CPUPercent, RAMPercent: a.RAMPercent, DiskPercent: a.DiskPercent}
	s.sustained = a.SustainedMinutes
	return s, nil
}

func (s *Service) canSeal() bool { return len(s.key) == 32 }

// hintFor is a stored Telegram channel's token hint, "" when it cannot be
// opened.
func (s *Service) hintFor(c store.AlertChannel) string {
	if c.Kind != store.ChannelTelegram || c.SealedToken == "" || !s.canSeal() {
		return ""
	}
	plain, err := secret.Open(s.key, c.SealedToken)
	if err != nil {
		return ""
	}
	return tokenHint(plain)
}

// tokenHint is "••••••:" and the first 3 characters after the colon.
func tokenHint(token string) string {
	_, rest, _ := strings.Cut(token, ":")
	r := []rune(rest)
	if len(r) > 3 {
		r = r[:3]
	}
	return "••••••:" + string(r) + "…"
}

// maskChat keeps the sign, a "-100" group prefix and the last 4 digits.
func maskChat(id string) string {
	head := ""
	switch {
	case strings.HasPrefix(id, "-100"):
		head = "-100"
	case strings.HasPrefix(id, "-"):
		head = "-"
	}
	body := id[len(head):]
	if len(body) <= 4 {
		return head + "••••"
	}
	return head + "••••" + body[len(body)-4:]
}

func (s *Service) state(kind string) chanState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.chans[kind]
}

func (s *Service) put(kind string, c store.AlertChannel, hint string) {
	c.UpdatedAt = time.Now()
	s.mu.Lock()
	s.chans[kind] = chanState{ch: c, exists: true, hint: hint}
	s.mu.Unlock()
}

// View returns the settings with hints only.
func (s *Service) View() View {
	tg, em := s.state(store.ChannelTelegram), s.state(store.ChannelEmail)
	s.mu.RLock()
	v := View{Thresholds: s.thresholds, SustainedMinutes: s.sustained, CanSeal: s.canSeal()}
	s.mu.RUnlock()
	v.Telegram = ChannelView{Available: s.canSeal(), Configured: tg.exists, Enabled: tg.exists && tg.ch.Enabled}
	if !s.canSeal() {
		v.Telegram.Reason = wordsNoSecretKey
	}
	if tg.exists {
		v.Telegram.TokenHint = tg.hint
		v.Telegram.ChatID = maskChat(tg.ch.ChatID)
	}
	reason := s.f.EmailReason()
	v.Email = ChannelView{Available: reason == "", Configured: em.exists, Enabled: em.exists && em.ch.Enabled, Reason: reason}
	if em.exists {
		v.Email.Recipients = append([]string(nil), em.ch.Recipients...)
	}
	return v
}

// Thresholds returns the server-wide thresholds, as saved.
func (s *Service) Thresholds() metrics.Thresholds {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.thresholds
}

// Sustained is how long a resource must stay over a threshold to alert.
func (s *Service) Sustained() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Duration(s.sustained) * time.Minute
}

// SaveTelegram stores the bot token (sealed) and the chat id. An empty token
// keeps the stored one; a given one is always sealed again and reads "token
// changed". A new channel starts on. changes is "" when nothing
// changed (nothing is written), else the reason text for activity; it never
// holds the token.
func (s *Service) SaveTelegram(ctx context.Context, token, chatID string) (string, error) {
	if !s.canSeal() {
		return "", ErrNoSecretKey
	}
	token, chatID = strings.TrimSpace(token), strings.TrimSpace(chatID)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cur := s.state(store.ChannelTelegram)
	switch {
	case token != "" && !tokenPattern.MatchString(token):
		return "", &FieldError{Field: "token", Code: FieldInvalid, Message: wordsBot}
	case token == "" && (!cur.exists || cur.ch.SealedToken == ""):
		return "", &FieldError{Field: "token", Code: FieldRequired, Message: wordsBot}
	case !chatPattern.MatchString(chatID):
		code := FieldInvalid
		if chatID == "" {
			code = FieldRequired
		}
		return "", &FieldError{Field: "chatId", Code: code, Message: wordsChat}
	}

	next := store.AlertChannel{Kind: store.ChannelTelegram, Enabled: true, SealedToken: cur.ch.SealedToken, ChatID: chatID}
	if cur.exists {
		next.Enabled = cur.ch.Enabled
	}
	hint, tokenChanged := cur.hint, false
	if token != "" {
		hint = tokenHint(token)
		sealed, err := secret.Seal(s.key, token)
		if err != nil {
			return "", errors.New("alerts: seal the bot token failed")
		}
		next.SealedToken, tokenChanged = sealed, true
	}
	before, existed, changed, err := s.st.SetAlertChannel(ctx, next)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", nil
	}
	s.put(store.ChannelTelegram, next, hint)
	if !existed {
		return "set up · chat " + maskChat(chatID), nil
	}
	var parts []string
	if tokenChanged {
		parts = append(parts, "token changed")
	}
	if before.ChatID != chatID {
		parts = append(parts, "chat "+maskChat(before.ChatID)+" → "+maskChat(chatID))
	}
	return strings.Join(parts, " · "), nil
}

// parseRecipients splits on commas and newlines, trims, drops repeats and
// checks each with net/mail (a bare address, no display name).
func parseRecipients(list string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		a, err := mail.ParseAddress(p)
		if err != nil || a.Address != p {
			return nil, &FieldError{Field: "recipients", Code: FieldInvalid, Message: "“" + p + "” is not an email address."}
		}
		if key := strings.ToLower(p); !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	switch {
	case len(out) == 0:
		return nil, &FieldError{Field: "recipients", Code: FieldRequired, Message: wordsRecipients}
	case len(out) > maxRecipient:
		return nil, &FieldError{Field: "recipients", Code: FieldRange, Message: wordsRecipients}
	}
	return out, nil
}

// SaveEmail stores the recipients (comma- or newline-separated, 1 to 10). A new
// channel starts on. ErrNotConfigured when the server has no SMTP settings.
func (s *Service) SaveEmail(ctx context.Context, recipients string) (string, error) {
	if s.f.EmailReason() != "" {
		return "", ErrNotConfigured
	}
	list, err := parseRecipients(recipients)
	if err != nil {
		return "", err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cur := s.state(store.ChannelEmail)
	next := store.AlertChannel{Kind: store.ChannelEmail, Enabled: true, Recipients: list}
	if cur.exists {
		next.Enabled = cur.ch.Enabled
	}
	before, existed, changed, err := s.st.SetAlertChannel(ctx, next)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", nil
	}
	s.put(store.ChannelEmail, next, "")
	if !existed {
		return "set up · " + strings.Join(list, ", "), nil
	}
	return "recipients " + strings.Join(before.Recipients, ", ") + " → " + strings.Join(list, ", "), nil
}

// SetEnabled turns a configured channel on or off. changes is "off → on" or
// "on → off", "" when it already was so.
func (s *Service) SetEnabled(ctx context.Context, kind string, on bool) (string, error) {
	if kind != store.ChannelTelegram && kind != store.ChannelEmail {
		return "", ErrNotConfigured
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cur := s.state(kind)
	if !cur.exists {
		return "", ErrNotConfigured
	}
	next := cur.ch
	next.Enabled = on
	_, _, changed, err := s.st.SetAlertChannel(ctx, next)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", nil
	}
	s.put(kind, next, cur.hint)
	if on {
		return "off → on", nil
	}
	return "on → off", nil
}

// SetThresholds saves the server-wide thresholds (percent 1 to 100, sustained
// minutes 1 to 60). changes is e.g. "CPU 90 → 80 % · sustained 5 → 10 min",
// "" when nothing changed (nothing is written).
func (s *Service) SetThresholds(ctx context.Context, cpu, ram, disk, sustained int) (string, error) {
	for _, f := range []struct {
		field string
		v     int
	}{{"cpuPercent", cpu}, {"ramPercent", ram}, {"diskPercent", disk}} {
		if f.v < 1 || f.v > 100 {
			return "", &FieldError{Field: f.field, Code: FieldRange, Message: wordsPercent}
		}
	}
	if sustained < 1 || sustained > 60 {
		return "", &FieldError{Field: "sustainedMinutes", Code: FieldRange, Message: wordsMinutes}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before, changed, err := s.st.SetAlertThresholds(ctx, store.AlertSettings{
		CPUPercent: cpu, RAMPercent: ram, DiskPercent: disk, SustainedMinutes: sustained,
	})
	if err != nil {
		return "", err
	}
	if !changed {
		return "", nil
	}
	s.mu.Lock()
	s.thresholds = metrics.Thresholds{CPUPercent: cpu, RAMPercent: ram, DiskPercent: disk}
	s.sustained = sustained
	s.mu.Unlock()
	var parts []string
	if before.CPUPercent != cpu {
		parts = append(parts, fmt.Sprintf("CPU %d → %d %%", before.CPUPercent, cpu))
	}
	if before.RAMPercent != ram {
		parts = append(parts, fmt.Sprintf("RAM %d → %d %%", before.RAMPercent, ram))
	}
	if before.DiskPercent != disk {
		parts = append(parts, fmt.Sprintf("disk %d → %d %%", before.DiskPercent, disk))
	}
	if before.SustainedMinutes != sustained {
		parts = append(parts, fmt.Sprintf("sustained %d → %d min", before.SustainedMinutes, sustained))
	}
	return strings.Join(parts, " · "), nil
}

// sender builds a sender for a stored, set-up channel; the token is opened
// here and only handed to the factory. ok is false when it cannot be built:
// the channel is not usable (an unopenable token is logged by its name only).
func (s *Service) sender(kind string, c store.AlertChannel) (Sender, bool) {
	switch kind {
	case store.ChannelTelegram:
		if !s.canSeal() {
			return nil, false
		}
		token, err := secret.Open(s.key, c.SealedToken)
		if err != nil {
			s.log.Error("alerts: the Telegram bot token cannot be opened", "channel", kind)
			return nil, false
		}
		return s.f.Telegram(token, c.ChatID), true
	case store.ChannelEmail:
		if s.f.EmailReason() != "" {
			return nil, false
		}
		return s.f.Email(append([]string(nil), c.Recipients...)), true
	}
	return nil, false
}

// Targets returns the channels a message goes to now (set up and on, Telegram
// first) and how many channels are set up at all.
func (s *Service) Targets() (enabled []Target, configured int) {
	for _, kind := range []string{store.ChannelTelegram, store.ChannelEmail} {
		st := s.state(kind)
		if !st.exists {
			continue
		}
		configured++
		if !st.ch.Enabled {
			continue
		}
		if snd, ok := s.sender(kind, st.ch); ok {
			enabled = append(enabled, Target{Name: kind, Sender: snd})
		}
	}
	return enabled, configured
}

// Test sends a test message through a channel, on or off. ErrNotConfigured
// when the channel is not set up. The answer is "Delivered" or the error
// words; a test is not an alert and nothing records it.
func (s *Service) Test(ctx context.Context, kind string) (TestResult, error) {
	if kind != store.ChannelTelegram && kind != store.ChannelEmail {
		return TestResult{}, ErrNotConfigured
	}
	st := s.state(kind)
	if !st.exists || (kind == store.ChannelEmail && s.f.EmailReason() != "") {
		return TestResult{}, ErrNotConfigured
	}
	snd, ok := s.sender(kind, st.ch)
	if !ok {
		if kind == store.ChannelTelegram && !s.canSeal() {
			return TestResult{Message: wordsNoSecretKey}, nil
		}
		return TestResult{Message: "The stored bot token cannot be opened. Enter it again."}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	err := snd.Send(ctx, Message{
		Title:    "Test from Server Control Panel",
		Detail:   "Alerts reach this channel.",
		Recovery: true, // the green check: a test is good news
	})
	if err == nil {
		return TestResult{OK: true, Message: "Delivered"}, nil
	}
	var se *SendError
	if errors.As(err, &se) {
		return TestResult{Message: se.Words}, nil
	}
	// Anything else is not worded by a sender, so it is not shown.
	s.log.Warn("alerts: test message failed", "channel", kind)
	return TestResult{Message: "Could not send the test message."}, nil
}

// Outbox is what the dev fake senders delivered; nil in a release build.
func (s *Service) Outbox() []OutboxEntry { return s.f.Outbox() }
