// Package config loads the panel's runtime configuration from the environment.
package config

import (
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultAddr is the address the panel binds when PANEL_ADDR is not set.
const DefaultAddr = "127.0.0.1:8471"

// DefaultDataDir is the directory the panel keeps its own state in when
// PANEL_DATA_DIR is not set.
const DefaultDataDir = "/data"

// Config is the panel's runtime configuration.
type Config struct {
	// Addr is the listen address. It is always 127.0.0.1:<port> (ADR 0006).
	Addr string
	// AccessTeamDomain is the raw CF_ACCESS_TEAM_DOMAIN setting. Release builds
	// validate it in auth.DefaultVerifier; dev builds ignore it.
	AccessTeamDomain string
	// AccessAUD is the raw CF_ACCESS_AUD setting, the Cloudflare Access
	// application audience tag. Release builds validate it in auth.DefaultVerifier;
	// dev builds ignore it.
	AccessAUD string
	// WorkspaceRoots are the folders the owner may add as workspaces: the
	// WORKSPACE_ROOTS entries, trimmed and cleaned, in the order given. It is
	// empty (never nil) when the variable is empty, and then every path is refused.
	WorkspaceRoots []string
	// DataDir is the directory for the panel's own state (PANEL_DATA_DIR),
	// cleaned. It defaults to DefaultDataDir.
	DataDir string
	// SecretKey is the 32-byte key that seals the Drive refresh token
	// (PANEL_SECRET_KEY). It is nil when unset; Drive is then not configured.
	SecretKey []byte
	// PublicURL is the panel's public origin (PANEL_PUBLIC_URL), without a trailing
	// slash. It is empty when unset.
	PublicURL string
	// GoogleClientID and GoogleClientSecret are the OAuth client values. They are
	// both set or both empty.
	GoogleClientID     string
	GoogleClientSecret string
	// DevBackupMinFree is PANEL_DEV_BACKUP_MIN_FREE in bytes, read only in a dev
	// build (0 otherwise). It forces the "not enough space" refusal.
	DevBackupMinFree int64
}

// Load reads configuration through getenv (usually os.Getenv). It refuses any
// listen address that is not 127.0.0.1 with a port from 1 to 65535, so the panel
// can only be reached through the Cloudflare Tunnel. The Cloudflare Access
// settings are read as they are; auth.DefaultVerifier validates them.
// WORKSPACE_ROOTS is a comma-separated list of absolute paths; Load refuses a
// relative entry.
func Load(getenv func(string) string) (Config, error) {
	addr := getenv("PANEL_ADDR")
	if addr == "" {
		addr = DefaultAddr
	}
	if err := validateAddr(addr); err != nil {
		return Config{}, err
	}
	roots, err := parseRoots(getenv("WORKSPACE_ROOTS"))
	if err != nil {
		return Config{}, err
	}
	dataDir := strings.TrimSpace(getenv("PANEL_DATA_DIR"))
	if dataDir == "" {
		dataDir = DefaultDataDir
	}
	key, err := parseSecretKey(getenv("PANEL_SECRET_KEY"))
	if err != nil {
		return Config{}, err
	}
	publicURL, err := parsePublicURL(getenv("PANEL_PUBLIC_URL"))
	if err != nil {
		return Config{}, err
	}
	clientID := strings.TrimSpace(getenv("GOOGLE_CLIENT_ID"))
	clientSecret := strings.TrimSpace(getenv("GOOGLE_CLIENT_SECRET"))
	if (clientID == "") != (clientSecret == "") {
		return Config{}, fmt.Errorf("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together")
	}
	minFree, err := devBackupMinFree(getenv)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Addr:               addr,
		AccessTeamDomain:   getenv("CF_ACCESS_TEAM_DOMAIN"),
		AccessAUD:          getenv("CF_ACCESS_AUD"),
		WorkspaceRoots:     roots,
		DataDir:            filepath.Clean(dataDir),
		SecretKey:          key,
		PublicURL:          publicURL,
		GoogleClientID:     clientID,
		GoogleClientSecret: clientSecret,
		DevBackupMinFree:   minFree,
	}, nil
}

// parseRoots splits a WORKSPACE_ROOTS value, trims and cleans each entry and
// drops empty ones. A relative entry is an error.
func parseRoots(value string) ([]string, error) {
	roots := []string{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !filepath.IsAbs(entry) {
			return nil, fmt.Errorf("WORKSPACE_ROOTS %q: every entry must be an absolute path starting with /", entry)
		}
		roots = append(roots, filepath.Clean(entry))
	}
	return roots, nil
}

func validateAddr(addr string) error {
	bad := func() error {
		return fmt.Errorf("PANEL_ADDR %q: the panel must bind 127.0.0.1 with a port from 1 to 65535", addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		return bad()
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return bad()
	}
	return nil
}
