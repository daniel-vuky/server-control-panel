package config

import (
	"fmt"
	"net/mail"
	"strconv"
	"strings"
)

// DefaultSMTPPort is the submission port used when SMTP_PORT is not set (STARTTLS).
const DefaultSMTPPort = 587

// SMTP is the mail server the email alerts go through. It comes from panel.env;
// the password is never typed into the panel and never appears in an error.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Configured reports whether an SMTP server is set (SMTP_HOST is not empty).
func (s SMTP) Configured() bool { return s.Host != "" }

// parseSMTP reads SMTP_HOST, SMTP_PORT (default 587), SMTP_USERNAME,
// SMTP_PASSWORD and SMTP_FROM, all trimmed. Everything empty means email is not
// set up. A host needs a valid SMTP_FROM; any other SMTP_* value without a host
// is an error naming SMTP_HOST. No error contains the password.
func parseSMTP(getenv func(string) string) (SMTP, error) {
	host := strings.TrimSpace(getenv("SMTP_HOST"))
	portValue := strings.TrimSpace(getenv("SMTP_PORT"))
	user := strings.TrimSpace(getenv("SMTP_USERNAME"))
	pass := strings.TrimSpace(getenv("SMTP_PASSWORD"))
	from := strings.TrimSpace(getenv("SMTP_FROM"))

	if host == "" {
		for _, v := range []struct{ name, value string }{
			{"SMTP_PORT", portValue}, {"SMTP_USERNAME", user}, {"SMTP_PASSWORD", pass}, {"SMTP_FROM", from},
		} {
			if v.value != "" {
				return SMTP{}, fmt.Errorf("%s is set but SMTP_HOST is not: set SMTP_HOST or remove the SMTP_* values", v.name)
			}
		}
		return SMTP{}, nil
	}

	port := DefaultSMTPPort
	if portValue != "" {
		n, err := strconv.Atoi(portValue)
		if err != nil || n < 1 || n > 65535 {
			return SMTP{}, fmt.Errorf("SMTP_PORT %q: must be a port from 1 to 65535", portValue)
		}
		port = n
	}
	if from == "" {
		return SMTP{}, fmt.Errorf("SMTP_FROM is not set: it is required with SMTP_HOST")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return SMTP{}, fmt.Errorf("SMTP_FROM %q: not an email address", from)
	}
	return SMTP{Host: host, Port: port, Username: user, Password: pass, From: from}, nil
}
