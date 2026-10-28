// Package config loads the panel's runtime configuration from the environment.
package config

import (
	"fmt"
	"net"
	"strconv"
)

// DefaultAddr is the address the panel binds when PANEL_ADDR is not set.
const DefaultAddr = "127.0.0.1:8471"

// Config is the panel's runtime configuration.
type Config struct {
	// Addr is the listen address. It is always 127.0.0.1:<port> (ADR 0006).
	Addr string
}

// Load reads configuration through getenv (usually os.Getenv). It refuses any
// listen address that is not 127.0.0.1 with a port from 1 to 65535, so the panel
// can only be reached through the Cloudflare Tunnel.
func Load(getenv func(string) string) (Config, error) {
	addr := getenv("PANEL_ADDR")
	if addr == "" {
		addr = DefaultAddr
	}
	if err := validateAddr(addr); err != nil {
		return Config{}, err
	}
	return Config{Addr: addr}, nil
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
