package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != "127.0.0.1:8471" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, "127.0.0.1:8471")
	}
}

func TestLoadAcceptsLoopbackAddr(t *testing.T) {
	env := map[string]string{"PANEL_ADDR": "127.0.0.1:9000"}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, "127.0.0.1:9000")
	}
}

func TestLoadRejectsNonLoopbackAddr(t *testing.T) {
	for _, addr := range []string{
		"localhost:8471",
		":8471",
		"0.0.0.0:8471",
		"[::1]:8471",
		"127.0.0.2:80",
		"127.0.0.1:",
		"127.0.0.1",
		"127.0.0.1:70000",
	} {
		t.Run(addr, func(t *testing.T) {
			_, err := Load(func(k string) string {
				if k == "PANEL_ADDR" {
					return addr
				}
				return ""
			})
			if err == nil {
				t.Fatalf("Load() with PANEL_ADDR=%q: want error, got nil", addr)
			}
			if !strings.Contains(err.Error(), "must bind 127.0.0.1") {
				t.Errorf("error = %q, want it to contain %q", err, "must bind 127.0.0.1")
			}
		})
	}
}
