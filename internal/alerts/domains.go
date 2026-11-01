package alerts

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// domainTimeout bounds one domain check (decision 15).
const domainTimeout = 10 * time.Second

// NewDomainClient is the client of the domain checks: a 10 s timeout, no
// proxy, no redirect followed (a 302 to the Access login is an answer), TLS
// verified, and a fresh connection per check so an idle one never stands in
// for the site answering.
func NewDomainClient() *http.Client {
	dialer := &net.Dialer{Timeout: domainTimeout}
	return &http.Client{
		Timeout: domainTimeout,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: domainTimeout,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// domainResult is one check of a project's public domain: answered is any
// HTTP status below 500; otherwise words say what failed ("503", "timed
// out").
type domainResult struct {
	domain   string
	answered bool
	words    string
}

// checkDomain sends one GET https://<domain>/ and reads only the status; the
// body is closed unread. An error is worded with fixed words and never kept,
// so no URL or address reaches a message.
func checkDomain(ctx context.Context, c *http.Client, domain string) domainResult {
	r := domainResult{domain: domain}
	u := url.URL{Scheme: "https", Host: domain, Path: "/"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		r.words = "no connection"
		return r
	}
	resp, err := c.Do(req)
	if err != nil {
		r.words = failWords(err)
		return r
	}
	_ = resp.Body.Close()
	if resp.StatusCode < http.StatusInternalServerError {
		r.answered = true
		return r
	}
	r.words = strconv.Itoa(resp.StatusCode)
	return r
}

// failWords names a failed request: timed out, a TLS error, or no
// connection (a name that does not resolve, a refused or reset connection).
func failWords(err error) string {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timed out"
	}
	var verr *tls.CertificateVerificationError
	var rerr tls.RecordHeaderError
	var aerr tls.AlertError
	if errors.As(err, &verr) || errors.As(err, &rerr) || errors.As(err, &aerr) {
		return "TLS error"
	}
	return "no connection"
}
