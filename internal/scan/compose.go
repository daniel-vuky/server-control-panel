// Package scan reads what a workspace contains: it parses the output of
// `docker compose config` and a project's `.env`, and finds the compose
// projects under a workspace folder.
package scan

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

// ComposeService is one service of a compose project. Environment holds the
// values compose resolved; it is used for database detection and is never
// stored or returned as it is.
type ComposeService struct {
	Name          string
	Image         string
	ContainerName string
	Environment   map[string]string
	Ports         []string
	Volumes       []project.Volume
}

// ComposeConfig is a parsed `docker compose config --format json`.
type ComposeConfig struct {
	Name     string
	Services []ComposeService // sorted by name
}

type rawConfig struct {
	Name     string                `json:"name"`
	Services map[string]rawService `json:"services"`
}

type rawService struct {
	Image         string             `json:"image"`
	ContainerName string             `json:"container_name"`
	Environment   map[string]*string `json:"environment"`
	Ports         []rawPort          `json:"ports"`
	Volumes       []rawVolume        `json:"volumes"`
}

type rawPort struct {
	Target    int    `json:"target"`
	Published any    `json:"published"` // a string in practice; tolerate a number
	Protocol  string `json:"protocol"`
}

type rawVolume struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// ParseConfig parses the stdout of `docker compose config --format json`. It
// fails on empty, truncated or non-JSON input, including a warning line before
// the JSON; the caller turns that into a row warning.
func ParseConfig(stdout []byte) (ComposeConfig, error) {
	var raw rawConfig
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return ComposeConfig{}, fmt.Errorf("compose config is not valid JSON: %w", err)
	}
	if raw.Services == nil && raw.Name == "" {
		return ComposeConfig{}, errors.New("compose config has no name and no services")
	}
	cfg := ComposeConfig{Name: raw.Name, Services: make([]ComposeService, 0, len(raw.Services))}
	for name, s := range raw.Services {
		svc := ComposeService{
			Name:          name,
			Image:         s.Image,
			ContainerName: s.ContainerName,
			Environment:   make(map[string]string, len(s.Environment)),
			Ports:         make([]string, 0, len(s.Ports)),
			Volumes:       make([]project.Volume, 0, len(s.Volumes)),
		}
		for k, v := range s.Environment {
			if v == nil {
				svc.Environment[k] = ""
			} else {
				svc.Environment[k] = *v
			}
		}
		for _, p := range s.Ports {
			svc.Ports = append(svc.Ports, formatPort(p))
		}
		for _, v := range s.Volumes {
			svc.Volumes = append(svc.Volumes, project.Volume{Type: v.Type, Source: v.Source, Target: v.Target})
		}
		cfg.Services = append(cfg.Services, svc)
	}
	sort.Slice(cfg.Services, func(i, j int) bool { return cfg.Services[i].Name < cfg.Services[j].Name })
	return cfg, nil
}

// formatPort renders published:target/protocol, or target/protocol when the
// port is not published.
func formatPort(p rawPort) string {
	proto := p.Protocol
	if proto == "" {
		proto = "tcp"
	}
	var published string
	switch v := p.Published.(type) {
	case string:
		published = v
	case float64:
		published = strconv.FormatInt(int64(v), 10)
	}
	if published == "" || published == "0" {
		return fmt.Sprintf("%d/%s", p.Target, proto)
	}
	return fmt.Sprintf("%s:%d/%s", published, p.Target, proto)
}

// Details is the part of the config the API returns: services, images, ports
// and volumes, never environment values.
func (c ComposeConfig) Details() project.Details {
	d := project.Details{Services: make([]project.Service, 0, len(c.Services))}
	for _, s := range c.Services {
		d.Services = append(d.Services, project.Service{
			Name:          s.Name,
			Image:         s.Image,
			ContainerName: s.ContainerName,
			Ports:         s.Ports,
			Volumes:       s.Volumes,
		})
	}
	return d
}

// NormalizeProjectName turns a folder name into the compose project name:
// lower-case, only a-z, 0-9, _ and - kept, and leading characters that are not
// a letter or digit removed.
func NormalizeProjectName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case (r == '_' || r == '-') && b.Len() > 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}
