package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

const (
	// infoEvery is how often docker.info is read at most (decision 11).
	infoEvery = 60 * time.Second
	// tunnelEvery is how long a cloudflared answer is kept (decision 14).
	tunnelEvery = 15 * time.Second
	// probeTimeout bounds each request to cloudflared's metrics server, and
	// probeMaxBody each answer.
	probeTimeout = 2 * time.Second
	probeMaxBody = 1 << 20
	// dockerSocket is where the Docker socket is mounted in the panel.
	dockerSocket = "/var/run/docker.sock"
	// cloudflaredComm is cloudflared's process name in /proc/<pid>/comm.
	cloudflaredComm = "cloudflared"
)

// cloudflaredDefaults are the addresses cloudflared's metrics server takes by
// default: the first free one of 127.0.0.1:20241 to 20245.
var cloudflaredDefaults = []string{
	"127.0.0.1:20241", "127.0.0.1:20242", "127.0.0.1:20243", "127.0.0.1:20244", "127.0.0.1:20245",
}

// DockerInfo is the part of `docker info` the System info card shows.
type DockerInfo struct {
	Name, OperatingSystem, KernelVersion, ServerVersion string
	NCPU                                                int
	MemTotal                                            int64
	ContainersRunning                                   int
}

// ParseDockerInfo reads `docker info --format {{json .}}`. An answer without
// a server version, or with server errors (the CLI's own half printed while
// the daemon did not answer), is an error.
func ParseDockerInfo(b []byte) (DockerInfo, error) {
	var raw struct {
		DockerInfo
		ServerErrors []string
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return DockerInfo{}, fmt.Errorf("metrics: read docker info: %w", err)
	}
	if len(raw.ServerErrors) > 0 || raw.ServerVersion == "" {
		return DockerInfo{}, errors.New("metrics: docker info has no server answer")
	}
	return raw.DockerInfo, nil
}

// DockerState is the Docker daemon's row in System services.
type DockerState string

// The Docker states (decisions 11 and 13).
const (
	DockerRunning       DockerState = "running"
	DockerNotResponding DockerState = "not_responding"
	DockerNotInstalled  DockerState = "not_installed"
)

// TunnelState is cloudflared's row in System services.
type TunnelState string

// The cloudflared states (decision 14).
const (
	TunnelConnected    TunnelState = "connected"
	TunnelDisconnected TunnelState = "disconnected"
	TunnelNotRunning   TunnelState = "not_running"
	TunnelUnknown      TunnelState = "unknown" // running, no metrics server answered
)

// Tunnel is cloudflared's state and, when its /config could be read, the
// number of distinct hostnames it routes.
type Tunnel struct {
	State     TunnelState
	Hostnames *int
}

// Thresholds are the server's alert lines in percent (decision 12).
type Thresholds struct {
	CPUPercent, RAMPercent, DiskPercent int
}

// DefaultThresholds are constants until the thresholds become settings (#66).
var DefaultThresholds = Thresholds{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85}

// System is what the System info and System services cards show. Available
// is false without the host's /proc. Hostname, OS, Kernel and DockerVersion
// are empty before any good docker.info; Cores and RAMBytes then come from
// /proc (0 without it). DisksFallback means the one panel data row.
type System struct {
	Available         bool
	Hostname, OS      string
	Kernel            string
	Cores             int
	RAMBytes          int64
	Docker            DockerState
	DockerVersion     string
	ContainersRunning int
	Tunnel            Tunnel
	Disks             []Disk
	DisksFallback     bool
}

// isSocket reports whether path is a unix socket (a symlink to one counts).
func isSocket(path string) bool {
	fi, err := os.Stat(path) //nolint:gosec // G703: the configured Docker socket path
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

// newCloudflaredClient is the HTTP client of the cloudflared probe: it dials
// loopback addresses only, never through a proxy, follows no redirect and
// gives up after probeTimeout.
func newCloudflaredClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: probeTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
				return fmt.Errorf("metrics: %s is not a loopback address", address)
			}
			return nil
		},
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:             nil, // never HTTP(S)_PROXY: the probe stays on this host
			DialContext:       dialer.DialContext,
			DisableKeepAlives: true,
		},
		Timeout:       probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ProbeCloudflared reads cloudflared's state over its metrics server
// (decision 14). Not running is not_running without a request. Otherwise the
// first address in addrs whose /ready answers decides: readyConnections above
// 0 is connected, 0 disconnected, and the hostnames are counted from /config
// on that address (nil when it cannot be read). No answer at all is unknown.
func ProbeCloudflared(ctx context.Context, client *http.Client, addrs []string, running bool) Tunnel {
	if !running {
		return Tunnel{State: TunnelNotRunning}
	}
	for _, addr := range addrs {
		var ready struct {
			ReadyConnections *int `json:"readyConnections"`
		}
		status, err := getJSON(ctx, client, "http://"+addr+"/ready", &ready)
		if err != nil || (status != http.StatusOK && status != http.StatusServiceUnavailable) || ready.ReadyConnections == nil {
			continue
		}
		t := Tunnel{State: TunnelDisconnected, Hostnames: hostnames(ctx, client, addr)}
		if *ready.ReadyConnections > 0 {
			t.State = TunnelConnected
		}
		return t
	}
	return Tunnel{State: TunnelUnknown}
}

// hostnames counts the distinct non-empty ingress hostnames of cloudflared's
// /config at addr, nil when it cannot be read.
func hostnames(ctx context.Context, client *http.Client, addr string) *int {
	var cfg struct {
		Config struct {
			Ingress []struct {
				Hostname string `json:"hostname"`
			} `json:"ingress"`
		} `json:"config"`
	}
	status, err := getJSON(ctx, client, "http://"+addr+"/config", &cfg)
	if err != nil || status != http.StatusOK {
		return nil
	}
	seen := map[string]bool{}
	for _, r := range cfg.Config.Ingress {
		if r.Hostname != "" {
			seen[r.Hostname] = true
		}
	}
	n := len(seen)
	return &n
}

// getJSON GETs url and decodes its body, at most probeMaxBody bytes, into v.
// It returns the status; a body it cannot read or decode is an error.
func getJSON(ctx context.Context, client *http.Client, url string, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, probeMaxBody+1))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(b) > probeMaxBody {
		return resp.StatusCode, errors.New("metrics: answer over 1 MiB")
	}
	if err := json.Unmarshal(b, v); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}
