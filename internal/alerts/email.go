package alerts

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

const (
	smtpDialTimeout = 10 * time.Second
	// smtpTimeout is the deadline for the whole message: net/smtp has no
	// timeout of its own.
	smtpTimeout = 15 * time.Second
	// implicitTLSPort is SMTPS: TLS from the first byte. Every other port
	// needs STARTTLS.
	implicitTLSPort = 465
)

// The email words.
const (
	wordsAuthFailed   = "The mail server refused the login."
	wordsNoMailServer = "Could not reach the mail server."
	wordsNoTLS        = "The mail server does not offer TLS (STARTTLS), so nothing was sent. Only a server on localhost may be used without TLS."
	wordsEmailNotSet  = "Set SMTP_HOST and SMTP_FROM in panel.env, see docs/alerts.md"
)

// Email sends a message to the recipients through the SMTP server of
// panel.env. It requires TLS (implicit on port 465, STARTTLS on any other)
// unless the server is localhost, and masks the password in every error.
type Email struct {
	cfg  config.SMTP
	to   []string
	host string // the panel's host name, for the subject

	// Seams for tests.
	dial      func(ctx context.Context, network, addr string) (net.Conn, error)
	tlsConfig *tls.Config   // the base TLS config (RootCAs); nil uses the system roots
	timeout   time.Duration // the whole-message deadline
}

// NewEmail returns a sender to the recipients through cfg. host is the panel's
// host name, used in the subject.
func NewEmail(cfg config.SMTP, to []string, host string) *Email {
	d := &net.Dialer{Timeout: smtpDialTimeout}
	return &Email{
		cfg:     cfg,
		to:      append([]string(nil), to...),
		host:    host,
		dial:    d.DialContext,
		timeout: smtpTimeout,
	}
}

// Send delivers the message to every recipient, or to none: a refused
// recipient stops the send before DATA.
func (e *Email) Send(ctx context.Context, m Message) error {
	if !e.cfg.Configured() {
		return &SendError{Code: CodeFailed, Words: wordsEmailNotSet}
	}
	if len(e.to) == 0 {
		return &SendError{Code: CodeFailed, Words: "No email recipient is set."}
	}
	from, err := mail.ParseAddress(e.cfg.From)
	if err != nil {
		return &SendError{Code: CodeFailed, Words: "SMTP_FROM is not an email address."}
	}
	port := e.cfg.Port
	if port == 0 {
		port = config.DefaultSMTPPort
	}
	conn, err := e.dial(ctx, "tcp", net.JoinHostPort(e.cfg.Host, strconv.Itoa(port)))
	if err != nil {
		return &SendError{Code: CodeUnreachable, Words: wordsNoMailServer, Retry: true}
	}
	deadline := time.Now().Add(e.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	// A cancel closes the connection, so a blocked read or write returns.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()

	if se := e.deliver(conn, port, from.Address, m); se != nil {
		se.Words = runner.Mask(se.Words, []string{e.cfg.Password})
		return se
	}
	return nil
}

// deliver runs the SMTP conversation on conn.
func (e *Email) deliver(conn net.Conn, port int, from string, m Message) *SendError {
	tlsCfg := e.tlsFor()
	if port == implicitTLSPort {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.Handshake(); err != nil {
			return tlsFailure(err)
		}
		conn = tc
	}
	// The host name given here is the one smtp.PlainAuth checks: it refuses
	// plain-text auth to anything but localhost, a second guard.
	c, err := smtp.NewClient(conn, e.cfg.Host)
	if err != nil {
		return smtpFailure(err, nil)
	}
	defer func() { _ = c.Close() }()

	if port != implicitTLSPort {
		// Extension swallows an EHLO failure, so the greeting is sent first.
		if err := c.Hello("localhost"); err != nil {
			return smtpFailure(err, nil)
		}
		ok, _ := c.Extension("STARTTLS")
		switch {
		case ok:
			if err := c.StartTLS(tlsCfg); err != nil {
				return tlsFailure(err)
			}
		case !localHost(e.cfg.Host):
			_ = c.Quit()
			return &SendError{Code: CodeNoTLS, Words: wordsNoTLS}
		}
	}

	if e.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", e.cfg.Username, e.cfg.Password, e.cfg.Host)); err != nil {
			return smtpFailure(err, func(*textproto.Error) *SendError {
				return &SendError{Code: CodeAuthFailed, Words: wordsAuthFailed}
			})
		}
	}
	if err := c.Mail(from); err != nil {
		return smtpFailure(err, nil)
	}
	for _, to := range e.to {
		if err := c.Rcpt(to); err != nil {
			return smtpFailure(err, func(*textproto.Error) *SendError {
				return &SendError{Code: CodeRecipientRefused, Words: "The mail server refused " + to + "."}
			})
		}
	}
	w, err := c.Data()
	if err != nil {
		return smtpFailure(err, nil)
	}
	if _, err := w.Write(e.message(m, time.Now())); err != nil {
		return smtpFailure(err, nil)
	}
	if err := w.Close(); err != nil {
		return smtpFailure(err, func(te *textproto.Error) *SendError {
			return &SendError{Code: CodeFailed, Words: "The mail server refused the message: " + reply(te)}
		})
	}
	// The message is accepted; a failed QUIT changes nothing.
	_ = c.Quit()
	return nil
}

// tlsFor is the TLS config for the server: verified, TLS 1.2 at least.
func (e *Email) tlsFor() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if e.tlsConfig != nil {
		cfg = e.tlsConfig.Clone()
		if cfg.MinVersion < tls.VersionTLS12 {
			cfg.MinVersion = tls.VersionTLS12
		}
	}
	cfg.ServerName = e.cfg.Host
	return cfg
}

// message is the RFC 5322 message: headers, a blank line, the plain-text body,
// CRLF line endings throughout.
func (e *Email) message(m Message, now time.Time) []byte {
	var b strings.Builder
	header := func(name, value string) {
		b.WriteString(name + ": " + oneLine(value) + "\r\n")
	}
	header("From", e.cfg.From)
	header("To", strings.Join(e.to, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", oneLine(m.Subject(e.host))))
	header("Date", now.Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	body := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(m.Text())
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// oneLine keeps a header value on one line, so no value can add a header.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
}

// localHost reports whether the configured SMTP host is a local relay, the
// only kind that may be used without TLS. It is decided by the name, never by
// what it resolves to.
func localHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// tlsFailure words a failed TLS handshake (implicit or STARTTLS).
func tlsFailure(err error) *SendError {
	var verify *tls.CertificateVerificationError
	if errors.As(err, &verify) {
		return &SendError{Code: CodeFailed, Words: "The mail server's TLS certificate was refused: " + verify.Err.Error()}
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		return smtpFailure(err, nil)
	}
	if connectionError(err) {
		return &SendError{Code: CodeUnreachable, Words: wordsNoMailServer, Retry: true}
	}
	return &SendError{Code: CodeFailed, Words: "Could not set up TLS with the mail server."}
}

// smtpFailure words an SMTP error. A 4xx reply is temporary (retried once); a
// 5xx is worded by refused when given; a broken connection (a timeout, a
// reset, a closed socket) is unreachable and retried.
func smtpFailure(err error, refused func(*textproto.Error) *SendError) *SendError {
	var te *textproto.Error
	if errors.As(err, &te) {
		if te.Code >= 400 && te.Code < 500 {
			return &SendError{Code: CodeFailed, Words: "The mail server is not accepting mail right now: " + reply(te), Retry: true}
		}
		if refused != nil {
			return refused(te)
		}
		return &SendError{Code: CodeFailed, Words: "The mail server answered: " + reply(te)}
	}
	var pe textproto.ProtocolError
	if errors.As(err, &pe) {
		return &SendError{Code: CodeFailed, Words: "The mail server answered something that is not SMTP."}
	}
	if connectionError(err) {
		return &SendError{Code: CodeUnreachable, Words: wordsNoMailServer, Retry: true}
	}
	// net/smtp's own refusals, such as PlainAuth's "unencrypted connection".
	// The caller masks the password.
	return &SendError{Code: CodeFailed, Words: "Could not send through the mail server: " + err.Error()}
}

// reply is the server's answer as one line, "451 4.3.0 Try again later".
func reply(te *textproto.Error) string {
	return oneLine(fmt.Sprintf("%03d %s", te.Code, te.Msg))
}

// connectionError reports whether err is the connection failing: a network
// error (timeouts included), a closed socket or the server hanging up.
func connectionError(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
