package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func envLoad(env map[string]string) (Config, error) {
	return Load(func(k string) string { return env[k] })
}

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

func TestSecretKeyForms(t *testing.T) {
	key := testKey()
	cases := []struct {
		name, value string
		ok          bool
	}{
		{"hex", hex.EncodeToString(key), true},
		{"hex upper", strings.ToUpper(hex.EncodeToString(key)), true},
		{"std padded", base64.StdEncoding.EncodeToString(key), true},
		{"std raw", base64.RawStdEncoding.EncodeToString(key), true},
		{"url padded", base64.URLEncoding.EncodeToString(key), true},
		{"url raw", base64.RawURLEncoding.EncodeToString(key), true},
		{"31 bytes", base64.StdEncoding.EncodeToString(key[:31]), false},
		{"33 bytes", base64.StdEncoding.EncodeToString(append(bytes.Clone(key), 1)), false},
		{"hex 62 chars", hex.EncodeToString(key[:31]), false},
		{"junk", "not a key!", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := envLoad(map[string]string{"PANEL_SECRET_KEY": tc.value})
			if tc.ok {
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}
				if !bytes.Equal(cfg.SecretKey, key) {
					t.Errorf("SecretKey = %x, want %x", cfg.SecretKey, key)
				}
				return
			}
			if err == nil {
				t.Fatal("Load() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), "PANEL_SECRET_KEY") || strings.Contains(err.Error(), tc.value) {
				t.Errorf("error %q must name the variable and never the value", err)
			}
		})
	}
	cfg, err := envLoad(nil)
	if err != nil || cfg.SecretKey != nil {
		t.Errorf("unset: SecretKey = %v, err = %v; want nil, nil", cfg.SecretKey, err)
	}
}

func TestPublicURL(t *testing.T) {
	cases := []struct {
		value, want string
		ok          bool
	}{
		{"", "", true},
		{"https://panel.example.com", "https://panel.example.com", true},
		{"https://panel.example.com:8443", "https://panel.example.com:8443", true},
		{"https://panel.example.com/", "https://panel.example.com", true},
		{"http://localhost", "http://localhost", true},
		{"http://localhost:5173", "http://localhost:5173", true},
		{"http://127.0.0.1:5173", "http://127.0.0.1:5173", true},
		{"http://example.com", "", false},
		{"https://x/path", "", false},
		{"https://x/a/", "", false},
		{"https://x?a=1", "", false},
		{"https://x/?a=1", "", false},
		{"https://x#frag", "", false},
		{"https://user@x", "", false},
		{"ftp://x", "", false},
		{"panel.example.com", "", false},
		{"https://", "", false},
		{"https://x:port", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			cfg, err := envLoad(map[string]string{"PANEL_PUBLIC_URL": tc.value})
			if !tc.ok {
				if err == nil {
					t.Fatalf("Load() = %q, want an error", cfg.PublicURL)
				}
				if !strings.Contains(err.Error(), "PANEL_PUBLIC_URL") {
					t.Errorf("error %q must name the variable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.PublicURL != tc.want {
				t.Errorf("PublicURL = %q, want %q", cfg.PublicURL, tc.want)
			}
		})
	}
}

func TestGooglePair(t *testing.T) {
	cases := []struct {
		id, secret string
		ok         bool
	}{
		{"", "", true},
		{"id", "secret", true},
		{"id", "", false},
		{"", "secret", false},
	}
	for _, tc := range cases {
		cfg, err := envLoad(map[string]string{"GOOGLE_CLIENT_ID": tc.id, "GOOGLE_CLIENT_SECRET": tc.secret})
		if tc.ok != (err == nil) {
			t.Fatalf("id=%q secret=%q: err = %v, want ok=%v", tc.id, tc.secret, err, tc.ok)
		}
		if tc.ok && (cfg.GoogleClientID != tc.id || cfg.GoogleClientSecret != tc.secret) {
			t.Errorf("got %q/%q, want %q/%q", cfg.GoogleClientID, cfg.GoogleClientSecret, tc.id, tc.secret)
		}
	}
}
