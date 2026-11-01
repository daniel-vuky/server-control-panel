package config

import (
	"strings"
	"testing"
)

func TestLoadSMTP(t *testing.T) {
	t.Run("all empty is not configured", func(t *testing.T) {
		cfg, err := envLoad(nil)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.SMTP.Configured() || cfg.SMTP != (SMTP{}) {
			t.Errorf("SMTP = %+v, want zero value", cfg.SMTP)
		}
	})

	t.Run("host and from default the port to 587", func(t *testing.T) {
		cfg, err := envLoad(map[string]string{"SMTP_HOST": "mail.x.io", "SMTP_FROM": "panel@x.io"})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := SMTP{Host: "mail.x.io", Port: 587, From: "panel@x.io"}
		if cfg.SMTP != want || !cfg.SMTP.Configured() {
			t.Errorf("SMTP = %+v, want %+v configured", cfg.SMTP, want)
		}
	})

	t.Run("every value is read and trimmed", func(t *testing.T) {
		cfg, err := envLoad(map[string]string{
			"SMTP_HOST": " mail.x.io ", "SMTP_PORT": " 465 ", "SMTP_USERNAME": " u ",
			"SMTP_PASSWORD": " p ", "SMTP_FROM": " panel@x.io ",
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := SMTP{Host: "mail.x.io", Port: 465, Username: "u", Password: "p", From: "panel@x.io"}
		if cfg.SMTP != want {
			t.Errorf("SMTP = %+v, want %+v", cfg.SMTP, want)
		}
	})

	t.Run("a from with a display name is accepted", func(t *testing.T) {
		if _, err := envLoad(map[string]string{"SMTP_HOST": "h", "SMTP_FROM": "Panel <panel@x.io>"}); err != nil {
			t.Errorf("Load: %v", err)
		}
	})

	bad := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"port 0", map[string]string{"SMTP_HOST": "h", "SMTP_FROM": "a@x.io", "SMTP_PORT": "0"}, "SMTP_PORT"},
		{"port too big", map[string]string{"SMTP_HOST": "h", "SMTP_FROM": "a@x.io", "SMTP_PORT": "70000"}, "SMTP_PORT"},
		{"port not a number", map[string]string{"SMTP_HOST": "h", "SMTP_FROM": "a@x.io", "SMTP_PORT": "abc"}, "SMTP_PORT"},
		{"host without from", map[string]string{"SMTP_HOST": "h"}, "SMTP_FROM"},
		{"from not an address", map[string]string{"SMTP_HOST": "h", "SMTP_FROM": "not-an-address"}, "SMTP_FROM"},
		{"username without host", map[string]string{"SMTP_USERNAME": "u"}, "SMTP_HOST"},
		{"password without host", map[string]string{"SMTP_PASSWORD": "secret-pw"}, "SMTP_HOST"},
		{"from without host", map[string]string{"SMTP_FROM": "a@x.io"}, "SMTP_HOST"},
		{"port without host", map[string]string{"SMTP_PORT": "25"}, "SMTP_HOST"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := envLoad(tc.env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one naming %s", err, tc.want)
			}
		})
	}

	t.Run("the password is never in an error", func(t *testing.T) {
		for _, env := range []map[string]string{
			{"SMTP_PASSWORD": "secret-pw"},
			{"SMTP_HOST": "h", "SMTP_PASSWORD": "secret-pw"},
			{"SMTP_HOST": "h", "SMTP_PASSWORD": "secret-pw", "SMTP_FROM": "nope"},
			{"SMTP_HOST": "h", "SMTP_PASSWORD": "secret-pw", "SMTP_FROM": "a@x.io", "SMTP_PORT": "abc"},
		} {
			_, err := envLoad(env)
			if err == nil {
				t.Fatalf("env %v: want an error", env)
			}
			if strings.Contains(err.Error(), "secret-pw") {
				t.Errorf("error leaks the password: %v", err)
			}
		}
	})
}
