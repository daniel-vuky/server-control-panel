package alerts

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

const (
	testSMTPPassword = "s3cret-mail-pass"
	testSMTPHost     = "mail.example.test" // not loopback: the injected dialer points it at the test server
)

// testCert is a self-signed certificate for mail.example.test and localhost,
// and the pool that trusts it.
type testCert struct {
	cert tls.Certificate
	pool *x509.CertPool
}

func newTestCert(t *testing.T) testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: testSMTPHost},
		DNSNames:              []string{testSMTPHost, "localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return testCert{cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool: pool}
}

// smtpScript is what the test server offers and answers.
type smtpScript struct {
	startTLS  bool              // EHLO offers STARTTLS
	implicit  bool              // the listener is TLS from the first byte (port 465)
	silent    bool              // never greets
	authReply string            // default 235
	mailReply string            // default 250
	rcptReply map[string]string // per address, default 250
}

// smtpSeen is what the test server received.
type smtpSeen struct {
	tls      bool
	auth     string
	mailFrom string
	rcpt     []string
	data     string
}

type smtpServer struct {
	ln     net.Listener
	script smtpScript
	tlsCfg *tls.Config
	wg     sync.WaitGroup
	mu     sync.Mutex
	seen   smtpSeen
}

func startSMTP(t *testing.T, cert testCert, script smtpScript) *smtpServer {
	t.Helper()
	cfg := &tls.Config{Certificates: []tls.Certificate{cert.cert}, MinVersion: tls.VersionTLS12}
	var (
		ln  net.Listener
		err error
	)
	if script.implicit {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", cfg)
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpServer{ln: ln, script: script, tlsCfg: cfg}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *smtpServer) received() smtpSeen {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := s.seen
	seen.rcpt = append([]string(nil), s.seen.rcpt...)
	return seen
}

func (s *smtpServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}
}

func (s *smtpServer) record(fn func(*smtpSeen)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.seen)
}

func (s *smtpServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if s.script.silent {
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	isTLS := s.script.implicit
	if isTLS {
		s.record(func(seen *smtpSeen) { seen.tls = true })
	}
	tp := textproto.NewConn(conn)
	reply := func(lines ...string) {
		for _, l := range lines {
			_ = tp.PrintfLine("%s", l)
		}
	}
	orDefault := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	reply("220 " + testSMTPHost + " ESMTP test")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			lines := []string{"250-" + testSMTPHost}
			if s.script.startTLS && !isTLS {
				lines = append(lines, "250-STARTTLS")
			}
			reply(append(lines, "250-AUTH PLAIN", "250 8BITMIME")...)
		case "STARTTLS":
			reply("220 2.0.0 Ready to start TLS")
			tc := tls.Server(conn, s.tlsCfg)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, tp, isTLS = tc, textproto.NewConn(tc), true
			s.record(func(seen *smtpSeen) { seen.tls = true })
		case "AUTH":
			_, b64, _ := strings.Cut(arg, " ")
			dec, _ := base64.StdEncoding.DecodeString(b64)
			s.record(func(seen *smtpSeen) { seen.auth = string(dec) })
			reply(orDefault(s.script.authReply, "235 2.7.0 Authentication successful"))
		case "*":
			reply("501 5.0.0 Authentication aborted")
		case "MAIL":
			s.record(func(seen *smtpSeen) { seen.mailFrom = arg })
			reply(orDefault(s.script.mailReply, "250 2.1.0 OK"))
		case "RCPT":
			addr := arg[strings.Index(arg, "<")+1 : strings.Index(arg, ">")]
			s.record(func(seen *smtpSeen) { seen.rcpt = append(seen.rcpt, addr) })
			reply(orDefault(s.script.rcptReply[addr], "250 2.1.5 OK"))
		case "DATA":
			reply("354 End data with <CR><LF>.<CR><LF>")
			b, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			s.record(func(seen *smtpSeen) { seen.data = string(b) })
			reply("250 2.0.0 Queued")
		case "RSET", "NOOP":
			reply("250 2.0.0 OK")
		case "QUIT":
			reply("221 2.0.0 Bye")
			return
		default:
			reply("502 5.5.2 Command not recognized")
		}
	}
}

func testSMTP(host string, port int) config.SMTP {
	return config.SMTP{Host: host, Port: port, Username: "alerts", Password: testSMTPPassword, From: "Panel <panel@example.com>"}
}

// newTestEmail is an Email whose dialer always reaches srv and whose TLS
// trusts the test certificate.
func newTestEmail(cfg config.SMTP, to []string, srv *smtpServer, cert testCert) *Email {
	e := NewEmail(cfg, to, "prod-01")
	e.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.ln.Addr().String())
	}
	e.tlsConfig = &tls.Config{RootCAs: cert.pool, MinVersion: tls.VersionTLS12}
	return e
}

func emailMessage() Message {
	return Message{
		Title:  "CPU at 92 %",
		Detail: "CPU at 92 % for 5 min (threshold 90 %)",
		Footer: "prod-01 · 10:31 (Asia/Ho_Chi_Minh)",
		Link:   "https://panel.example.com/server",
	}
}

func asSendError(t *testing.T, err error) *SendError {
	t.Helper()
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T %v, want *SendError", err, err)
	}
	for _, s := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(s, testSMTPPassword) {
			t.Fatalf("error text holds the password: %q", s)
		}
	}
	return se
}

func TestEmailSendsWithStartTLS(t *testing.T) {
	cert := newTestCert(t)
	srv := startSMTP(t, cert, smtpScript{startTLS: true})
	to := []string{"admin@company.vn", "dev@company.vn"}
	m := emailMessage()
	if err := newTestEmail(testSMTP(testSMTPHost, 587), to, srv, cert).Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.received()
	if !got.tls {
		t.Fatal("the message was not sent over TLS")
	}
	if got.auth != "\x00alerts\x00"+testSMTPPassword {
		t.Fatalf("AUTH PLAIN = %q", got.auth)
	}
	if !strings.HasPrefix(got.mailFrom, "FROM:<panel@example.com>") {
		t.Fatalf("MAIL %q, want the bare address", got.mailFrom)
	}
	if strings.Join(got.rcpt, ",") != "admin@company.vn,dev@company.vn" {
		t.Fatalf("RCPT = %v", got.rcpt)
	}
	head, body, ok := strings.Cut(got.data, "\n\n")
	if !ok {
		t.Fatalf("no header end in %q", got.data)
	}
	for _, h := range []string{
		"From: Panel <panel@example.com>",
		"To: admin@company.vn, dev@company.vn",
		"Subject: [prod-01] CPU at 92 %",
		"Content-Type: text/plain; charset=utf-8",
	} {
		if !strings.Contains(head+"\n", h+"\n") {
			t.Fatalf("header %q missing in:\n%s", h, head)
		}
	}
	if !strings.Contains(head, "\nDate: ") {
		t.Fatalf("no Date header in:\n%s", head)
	}
	if strings.TrimSuffix(body, "\n") != m.Text() {
		t.Fatalf("body = %q, want %q", body, m.Text())
	}
}

func TestEmailRefusesWithoutTLSOnRemoteHost(t *testing.T) {
	cert := newTestCert(t)
	srv := startSMTP(t, cert, smtpScript{})
	to := []string{"admin@company.vn"}

	err := newTestEmail(testSMTP(testSMTPHost, 587), to, srv, cert).Send(context.Background(), emailMessage())
	se := asSendError(t, err)
	if se.Code != "no_tls" || se.Retry {
		t.Fatalf("SendError = %+v, want no_tls without retry", *se)
	}
	if got := srv.received(); got.auth != "" || got.mailFrom != "" || got.data != "" {
		t.Fatalf("something was sent without TLS: %+v", got)
	}

	// The same server addressed as a local relay gets the message.
	if err := newTestEmail(testSMTP("localhost", 587), to, srv, cert).Send(context.Background(), emailMessage()); err != nil {
		t.Fatalf("Send to localhost: %v", err)
	}
	if got := srv.received(); got.tls || got.data == "" {
		t.Fatalf("localhost send: %+v", got)
	}
}

func TestLocalHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "127.0.0.1": true, "::1": true,
		"mail.example.test": false, "127.0.0.2": false, "localhost.example.com": false, "": false,
	} {
		if got := localHost(host); got != want {
			t.Errorf("localHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestEmailAuthFailure(t *testing.T) {
	cert := newTestCert(t)
	srv := startSMTP(t, cert, smtpScript{startTLS: true, authReply: "535 5.7.8 Bad credentials " + testSMTPPassword})
	err := newTestEmail(testSMTP(testSMTPHost, 587), []string{"admin@company.vn"}, srv, cert).Send(context.Background(), emailMessage())
	se := asSendError(t, err)
	if se.Code != "auth_failed" || se.Words != "The mail server refused the login." || se.Retry {
		t.Fatalf("SendError = %+v", *se)
	}
	if got := srv.received(); got.mailFrom != "" || got.data != "" {
		t.Fatalf("sent after a refused login: %+v", got)
	}
}

func TestEmailRecipientRefused(t *testing.T) {
	cert := newTestCert(t)
	srv := startSMTP(t, cert, smtpScript{startTLS: true, rcptReply: map[string]string{"dev@company.vn": "550 5.1.1 No such user"}})
	err := newTestEmail(testSMTP(testSMTPHost, 587), []string{"admin@company.vn", "dev@company.vn"}, srv, cert).Send(context.Background(), emailMessage())
	se := asSendError(t, err)
	if se.Code != "recipient_refused" || se.Words != "The mail server refused dev@company.vn." || se.Retry {
		t.Fatalf("SendError = %+v", *se)
	}
	if got := srv.received(); got.data != "" {
		t.Fatal("DATA was sent after a refused recipient")
	}
}

func TestEmailUnreachable(t *testing.T) {
	// A closed port, through the real dialer.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	err = NewEmail(testSMTP("127.0.0.1", port), []string{"admin@company.vn"}, "prod-01").Send(context.Background(), emailMessage())
	se := asSendError(t, err)
	if se.Code != "unreachable" || se.Words != "Could not reach the mail server." || !se.Retry {
		t.Fatalf("closed port: SendError = %+v", *se)
	}

	// A temporary refusal is retried.
	cert := newTestCert(t)
	srv := startSMTP(t, cert, smtpScript{startTLS: true, mailReply: "451 4.3.0 Try again later"})
	err = newTestEmail(testSMTP(testSMTPHost, 587), []string{"admin@company.vn"}, srv, cert).Send(context.Background(), emailMessage())
	se = asSendError(t, err)
	if !se.Retry || se.Code != "failed" || !strings.Contains(se.Words, "451 4.3.0 Try again later") {
		t.Fatalf("4xx on MAIL: SendError = %+v", *se)
	}

	// A server that never answers hits the message deadline.
	silent := startSMTP(t, cert, smtpScript{silent: true})
	e := newTestEmail(testSMTP(testSMTPHost, 587), []string{"admin@company.vn"}, silent, cert)
	e.timeout = 100 * time.Millisecond
	start := time.Now()
	se = asSendError(t, e.Send(context.Background(), emailMessage()))
	if se.Code != "unreachable" || !se.Retry {
		t.Fatalf("silent server: SendError = %+v", *se)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the deadline did not stop the send: %v", took)
	}
}

func TestEmailUntrustedCertificate(t *testing.T) {
	cert := newTestCert(t)
	srv := startSMTP(t, cert, smtpScript{startTLS: true})
	e := newTestEmail(testSMTP(testSMTPHost, 587), []string{"admin@company.vn"}, srv, cert)
	e.tlsConfig = nil // the system roots do not trust the test certificate
	se := asSendError(t, e.Send(context.Background(), emailMessage()))
	if se.Code != "failed" || se.Retry || !strings.HasPrefix(se.Words, "The mail server's TLS certificate was refused") {
		t.Fatalf("SendError = %+v", *se)
	}
	if got := srv.received(); got.auth != "" || got.data != "" {
		t.Fatalf("sent over an untrusted TLS: %+v", got)
	}
}

func TestEmailImplicitTLS(t *testing.T) {
	cert := newTestCert(t)
	srv := startSMTP(t, cert, smtpScript{implicit: true})
	m := emailMessage()
	if err := newTestEmail(testSMTP(testSMTPHost, 465), []string{"admin@company.vn"}, srv, cert).Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.received()
	if !got.tls || got.auth != "\x00alerts\x00"+testSMTPPassword || got.data == "" {
		t.Fatalf("received %+v", got)
	}
}

func TestEmailMessageUsesCRLF(t *testing.T) {
	e := NewEmail(testSMTP(testSMTPHost, 587), []string{"admin@company.vn"}, "prod-01")
	m := emailMessage()
	raw := string(e.message(m, time.Date(2026, 11, 1, 10, 31, 0, 0, time.UTC)))
	if strings.Contains(strings.ReplaceAll(raw, "\r\n", ""), "\n") {
		t.Fatalf("a bare LF in %q", raw)
	}
	head, body, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		t.Fatalf("no header end in %q", raw)
	}
	if !strings.Contains(head, "\r\nDate: Sun, 01 Nov 2026 10:31:00 +0000") {
		t.Fatalf("Date header in %q", head)
	}
	if body != strings.ReplaceAll(m.Text(), "\n", "\r\n")+"\r\n" {
		t.Fatalf("body = %q", body)
	}

	// A line break in the title cannot add a header.
	m.Title = "evil\r\nBcc: x@example.com"
	head, _, _ = strings.Cut(string(e.message(m, time.Now())), "\r\n\r\n")
	if strings.Contains(head, "\nBcc:") || strings.Contains(head, "\rBcc:") {
		t.Fatalf("a header was injected through the title: %q", head)
	}
}
