package config

import (
	"slices"
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

func TestLoadReadsAccessSettingsRaw(t *testing.T) {
	env := map[string]string{
		"CF_ACCESS_TEAM_DOMAIN": " HTTPS://X/ ",
		"CF_ACCESS_AUD":         "zz",
	}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AccessTeamDomain != " HTTPS://X/ " {
		t.Errorf("AccessTeamDomain = %q, want the raw value", cfg.AccessTeamDomain)
	}
	if cfg.AccessAUD != "zz" {
		t.Errorf("AccessAUD = %q, want zz", cfg.AccessAUD)
	}
}

func TestLoadSucceedsWithoutAccessSettings(t *testing.T) {
	cfg, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AccessTeamDomain != "" || cfg.AccessAUD != "" {
		t.Errorf("Access settings = (%q, %q), want both empty", cfg.AccessTeamDomain, cfg.AccessAUD)
	}
}

func TestWorkspaceRoots(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		want    []string
		wantErr bool
	}{
		{name: "empty", env: "", want: []string{}},
		{name: "trims, cleans and drops empties", env: " /srv , /home/ ,", want: []string{"/srv", "/home"}},
		{name: "keeps order", env: "/b,/a", want: []string{"/b", "/a"}},
		{name: "relative", env: "srv", wantErr: true},
		{name: "one relative among absolute", env: "/srv,srv", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(func(k string) string {
				if k == "WORKSPACE_ROOTS" {
					return tt.env
				}
				return ""
			})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() with WORKSPACE_ROOTS=%q: want error, got nil", tt.env)
				}
				if !strings.Contains(err.Error(), "WORKSPACE_ROOTS") {
					t.Errorf("error = %q, want it to name WORKSPACE_ROOTS", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.WorkspaceRoots == nil || !slices.Equal(cfg.WorkspaceRoots, tt.want) {
				t.Errorf("WorkspaceRoots = %#v, want %#v", cfg.WorkspaceRoots, tt.want)
			}
		})
	}
}

func TestDataDirDefault(t *testing.T) {
	cfg, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DataDir != "/data" {
		t.Errorf("DataDir = %q, want /data", cfg.DataDir)
	}
}

func TestDataDirFromEnv(t *testing.T) {
	cfg, err := Load(func(k string) string {
		if k == "PANEL_DATA_DIR" {
			return "/src/data/"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DataDir != "/src/data" {
		t.Errorf("DataDir = %q, want /src/data", cfg.DataDir)
	}
}

func TestLoadZone(t *testing.T) {
	cases := []struct {
		name    string
		tz      string
		want    string
		wantErr bool
	}{
		{"empty is UTC", "", "UTC", false},
		{"IANA name", "Asia/Ho_Chi_Minh", "Asia/Ho_Chi_Minh", false},
		{"POSIX leading colon", ":Europe/Paris", "Europe/Paris", false},
		{"Etc/UTC", "Etc/UTC", "Etc/UTC", false},
		{"unknown zone", "Mars/Base", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(func(k string) string {
				if k == "TZ" {
					return tc.tz
				}
				return ""
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), "TZ") {
					t.Errorf("error %q does not name TZ", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Zone == nil {
				t.Fatal("Zone is nil")
			}
			if cfg.Zone.String() != tc.want {
				t.Errorf("Zone = %q, want %q", cfg.Zone, tc.want)
			}
		})
	}
}

func TestConfigCloudflaredMetrics(t *testing.T) {
	load := func(v string) (Config, error) {
		return Load(func(k string) string {
			if k == "CLOUDFLARED_METRICS" {
				return v
			}
			return ""
		})
	}
	for _, v := range []string{"", "127.0.0.1:20241", "localhost:60123", "[::1]:20245", "127.0.0.1:1", "127.0.0.1:65535"} {
		t.Run("ok "+v, func(t *testing.T) {
			cfg, err := load(v)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.CloudflaredMetrics != v {
				t.Errorf("CloudflaredMetrics = %q, want %q", cfg.CloudflaredMetrics, v)
			}
		})
	}
	for _, v := range []string{
		"10.0.0.1:20241",
		":20241",
		"localhost",
		"127.0.0.1",
		"0.0.0.0:20241",
		"::1:20241",
		"127.0.0.1:0",
		"127.0.0.1:65536",
		"127.0.0.1:+80",
		"http://127.0.0.1:20241",
		"example.com:20241",
	} {
		t.Run("bad "+v, func(t *testing.T) {
			_, err := load(v)
			if err == nil {
				t.Fatalf("Load() with CLOUDFLARED_METRICS=%q: want an error", v)
			}
			if !strings.Contains(err.Error(), "CLOUDFLARED_METRICS") {
				t.Errorf("error %q does not name CLOUDFLARED_METRICS", err)
			}
		})
	}
}
