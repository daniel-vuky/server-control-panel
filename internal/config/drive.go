package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// secretKeyLen is the length of PANEL_SECRET_KEY in bytes (AES-256).
const secretKeyLen = 32

// parseSecretKey decodes PANEL_SECRET_KEY: 64 hex characters, or base64 (standard
// or URL alphabet, padded or not) of exactly 32 bytes. Empty means unset (nil).
// The error never contains the value.
func parseSecretKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if len(value) == 2*secretKeyLen {
		if key, err := hex.DecodeString(value); err == nil {
			return key, nil
		}
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if key, err := enc.DecodeString(value); err == nil && len(key) == secretKeyLen {
			return key, nil
		}
	}
	return nil, fmt.Errorf("PANEL_SECRET_KEY: must be 32 bytes as 64 hex characters or base64")
}

// parsePublicURL validates PANEL_PUBLIC_URL: https://host[:port], or
// http://localhost|127.0.0.1[:port], with no path, query, fragment or userinfo.
// It returns the origin without a trailing slash. Empty means unset.
func parsePublicURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	bad := func() error {
		return fmt.Errorf("PANEL_PUBLIC_URL %q: must be https://<host>[:port] (or http://localhost[:port] in dev) with no path", value)
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || strings.Contains(value, "#") {
		return "", bad()
	}
	switch u.Scheme {
	case "https":
	case "http":
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" {
			return "", bad()
		}
	default:
		return "", bad()
	}
	return u.Scheme + "://" + u.Host, nil
}

// DriveReason says why Google Drive cannot be configured, or "" when it can: the
// first of the secret key, the public URL, the Google client ID and the Google
// client secret that is missing. A dev build uses the fake Drive, so it checks
// only the key and URL.
func (c Config) DriveReason() string {
	switch {
	case len(c.SecretKey) == 0:
		return "PANEL_SECRET_KEY is not set"
	case c.PublicURL == "":
		return "PANEL_PUBLIC_URL is not set"
	case !devBuild && c.GoogleClientID == "":
		return "GOOGLE_CLIENT_ID is not set"
	case !devBuild && c.GoogleClientSecret == "":
		return "GOOGLE_CLIENT_SECRET is not set"
	}
	return ""
}
