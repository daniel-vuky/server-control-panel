package metrics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

// fixture reads testdata/<name>.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304: test fixture names
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseDockerInfo(t *testing.T) {
	got, err := ParseDockerInfo(fixture(t, "docker/info.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := DockerInfo{Name: "docker-desktop", OperatingSystem: "Docker Desktop", KernelVersion: "6.12.76-linuxkit",
		ServerVersion: "29.3.1", NCPU: 12, MemTotal: 8216776704, ContainersRunning: 3}
	if got != want {
		t.Fatalf("ParseDockerInfo = %+v, want %+v", got, want)
	}
	for _, bad := range []string{
		"{not json",
		`{}`,
		`{"ServerVersion":"29.3.1","ServerErrors":["Cannot connect to the Docker daemon"]}`,
	} {
		if _, err := ParseDockerInfo([]byte(bad)); err == nil {
			t.Errorf("ParseDockerInfo(%s): want an error", bad)
		}
	}
}

// monitorEnv is a samplerEnv with a Monitor over it: a Docker socket at sock
// (absent until listen), the fallback disk row and no cloudflared.
type monitorEnv struct {
	*samplerEnv
	sock string
	m    *Monitor
}

func newMonitorEnv(t *testing.T) *monitorEnv {
	t.Helper()
	e := newSamplerEnv(t)
	dir, err := os.MkdirTemp("", "sock") // short: a socket path is limited to about 100 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	me := &monitorEnv{samplerEnv: e, sock: filepath.Join(dir, "docker.sock")}
	host := Host{Proc: e.proc, Sys: filepath.Join(e.proc, "no-sys"), DataDir: "/data",
		Statfs: func(string) (uint64, uint64, error) { return 3 << 30, 10 << 30, nil }}
	me.m = New(Options{Store: e.st, Cmd: e.cmd, Source: e.src, Host: host, Socket: me.sock,
		Now: e.clock.now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return me
}

// listen puts a real unix socket at the Docker socket's path.
func (me *monitorEnv) listen() {
	me.t.Helper()
	l, err := net.Listen("unix", me.sock)
	if err != nil {
		me.t.Fatal(err)
	}
	me.t.Cleanup(func() { _ = l.Close() })
}

// setLatest makes the sampler's latest tick one taken now with Docker up or
// not.
func (me *monitorEnv) setLatest(up bool) {
	s := me.m.sampler
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest, s.haveLatest = Tick{At: me.clock.now(), DockerUp: up, Cores: 2}, true
}

func TestSystemDockerStates(t *testing.T) {
	ctx := context.Background()
	me := newMonitorEnv(t)
	me.cmd.set(runner.DockerInfo, string(fixture(t, "docker/info.json")), nil)

	// No socket: Not installed, and nothing asks Docker.
	sys := me.m.System(ctx)
	if sys.Docker != DockerNotInstalled {
		t.Fatalf("Docker = %q without a socket, want not_installed", sys.Docker)
	}
	if n := me.cmd.total(); n != 0 {
		t.Fatalf("%d commands without a socket, want 0", n)
	}
	// Before any good docker.info: no hostname, kernel or version; cores and
	// RAM from the host's /proc.
	if !sys.Available || sys.Hostname != "" || sys.Kernel != "" || sys.DockerVersion != "" || sys.OS != "" ||
		sys.Cores != 2 || sys.RAMBytes != 8<<30 {
		t.Fatalf("System before docker.info = %+v, want only cores 2 and RAM 8 GiB", sys)
	}
	if !sys.DisksFallback || len(sys.Disks) != 1 || sys.Disks[0] != (Disk{Mount: "/ (panel data)", Used: 3 << 30, Total: 10 << 30}) {
		t.Fatalf("Disks = %+v (fallback %v), want the panel data row", sys.Disks, sys.DisksFallback)
	}
	if sys.Tunnel.State != TunnelNotRunning {
		t.Fatalf("Tunnel = %+v, want not_running", sys.Tunnel)
	}

	// A file that is not a socket is not Docker either.
	if err := os.WriteFile(me.sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if sys := me.m.System(ctx); sys.Docker != DockerNotInstalled {
		t.Fatalf("Docker = %q with a plain file, want not_installed", sys.Docker)
	}
	if err := os.Remove(me.sock); err != nil {
		t.Fatal(err)
	}
	me.listen()

	// A socket and a tick that could not read Docker: Not responding, and
	// docker.info is not tried.
	me.setLatest(false)
	if sys := me.m.System(ctx); sys.Docker != DockerNotResponding {
		t.Fatalf("Docker = %q after a tick with Docker down, want not_responding", sys.Docker)
	}
	if n := len(me.cmd.callsOf(runner.DockerInfo)); n != 0 {
		t.Fatalf("%d docker.info reads with Docker down, want 0", n)
	}

	// Docker up: the facts come from docker.info.
	me.setLatest(true)
	sys = me.m.System(ctx)
	want := System{Available: true, Hostname: "docker-desktop", OS: "Docker Desktop", Kernel: "6.12.76-linuxkit",
		Cores: 12, RAMBytes: 8216776704, Docker: DockerRunning, DockerVersion: "29.3.1", ContainersRunning: 3}
	if sys.Hostname != want.Hostname || sys.OS != want.OS || sys.Kernel != want.Kernel || sys.Cores != want.Cores ||
		sys.RAMBytes != want.RAMBytes || sys.Docker != want.Docker || sys.DockerVersion != want.DockerVersion ||
		sys.ContainersRunning != want.ContainersRunning {
		t.Fatalf("System = %+v, want %+v", sys, want)
	}

	// Read again within 60 s: the cache answers.
	me.clock.add(59 * time.Second)
	me.setLatest(true)
	me.m.System(ctx)
	if n := len(me.cmd.callsOf(runner.DockerInfo)); n != 1 {
		t.Fatalf("%d docker.info reads within 60 s, want 1", n)
	}

	// After 60 s it is read again; a failed read keeps the cached facts.
	me.clock.add(2 * time.Second)
	me.setLatest(true)
	me.cmd.set(runner.DockerInfo, "", errors.New("exit status 1"))
	sys = me.m.System(ctx)
	if n := len(me.cmd.callsOf(runner.DockerInfo)); n != 2 {
		t.Fatalf("%d docker.info reads after 61 s, want 2", n)
	}
	if sys.Hostname != "docker-desktop" || sys.Cores != 12 || sys.DockerVersion != "29.3.1" || sys.Docker != DockerRunning {
		t.Fatalf("System after a failed docker.info = %+v, want the cached facts", sys)
	}

	// Docker down later: Not responding, the facts still shown.
	me.clock.add(time.Minute)
	me.setLatest(false)
	sys = me.m.System(ctx)
	if sys.Docker != DockerNotResponding || sys.Hostname != "docker-desktop" || sys.Kernel != "6.12.76-linuxkit" {
		t.Fatalf("System with Docker down = %+v, want not_responding with the cached facts", sys)
	}
}

// With no tick, or one older than 90 s, System asks the hub once.
func TestSystemDockerStateWithoutTick(t *testing.T) {
	ctx := context.Background()
	me := newMonitorEnv(t)
	me.listen()
	me.cmd.set(runner.DockerInfo, string(fixture(t, "docker/info.json")), nil)

	me.src.set(func(f *fakeSource) { f.up = false })
	if sys := me.m.System(ctx); sys.Docker != DockerNotResponding {
		t.Fatalf("Docker = %q with the hub down, want not_responding", sys.Docker)
	}
	me.src.set(func(f *fakeSource) { f.up = true })
	if sys := me.m.System(ctx); sys.Docker != DockerRunning {
		t.Fatalf("Docker = %q with the hub up, want running", sys.Docker)
	}
	me.src.mu.Lock()
	calls, age := len(me.src.starts), me.src.containersAge
	me.src.mu.Unlock()
	if calls != 2 || age != 30*time.Second {
		t.Fatalf("%d RecentContainers calls with max age %s, want 2 with 30s", calls, age)
	}

	// A tick older than 90 s does not count.
	me.setLatest(false)
	me.clock.add(91 * time.Second)
	if sys := me.m.System(ctx); sys.Docker != DockerRunning {
		t.Fatalf("Docker = %q with a stale tick and the hub up, want running", sys.Docker)
	}
}

// cloudflaredServer serves /ready and /config from the fixtures (or the
// overrides) and counts the requests.
type cloudflaredServer struct {
	*httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	ready  []byte
	status int
	config []byte
	cfgSt  int
}

func newCloudflaredServer(t *testing.T, ready string, status int) *cloudflaredServer {
	t.Helper()
	cs := &cloudflaredServer{hits: map[string]int{}, ready: fixture(t, "cloudflared/"+ready), status: status,
		config: fixture(t, "cloudflared/config.json"), cfgSt: http.StatusOK}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.hits[r.URL.Path]++
		ready, status, config, cfgSt := cs.ready, cs.status, cs.config, cs.cfgSt
		cs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ready":
			w.WriteHeader(status)
			_, _ = w.Write(ready)
		case "/config":
			w.WriteHeader(cfgSt)
			_, _ = w.Write(config)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *cloudflaredServer) addr() string { return strings.TrimPrefix(cs.URL, "http://") }

func (cs *cloudflaredServer) hitsOf(path string) int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.hits[path]
}

// closedAddr is a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func intp(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}

func TestProbeCloudflared(t *testing.T) {
	ctx := context.Background()
	client := newCloudflaredClient()

	t.Run("connected", func(t *testing.T) {
		cs := newCloudflaredServer(t, "ready-200.json", http.StatusOK)
		got := ProbeCloudflared(ctx, client, []string{closedAddr(t), cs.addr()}, true)
		if got.State != TunnelConnected || got.Hostnames == nil || *got.Hostnames != 5 {
			t.Fatalf("Probe = %s, hostnames %s; want connected, 5", got.State, intp(got.Hostnames))
		}
	})
	t.Run("disconnected", func(t *testing.T) {
		cs := newCloudflaredServer(t, "ready-503.json", http.StatusServiceUnavailable)
		got := ProbeCloudflared(ctx, client, []string{cs.addr()}, true)
		if got.State != TunnelDisconnected || got.Hostnames == nil || *got.Hostnames != 5 {
			t.Fatalf("Probe = %s, hostnames %s; want disconnected, 5", got.State, intp(got.Hostnames))
		}
	})
	t.Run("config not served", func(t *testing.T) {
		cs := newCloudflaredServer(t, "ready-200.json", http.StatusOK)
		cs.cfgSt, cs.config = http.StatusNotFound, []byte("404 page not found\n")
		got := ProbeCloudflared(ctx, client, []string{cs.addr()}, true)
		if got.State != TunnelConnected || got.Hostnames != nil {
			t.Fatalf("Probe = %s, hostnames %s; want connected, nil", got.State, intp(got.Hostnames))
		}
	})
	t.Run("config over 1 MiB", func(t *testing.T) {
		cs := newCloudflaredServer(t, "ready-200.json", http.StatusOK)
		cs.config = append(fixture(t, "cloudflared/config.json"), make([]byte, 1<<20)...)
		for i := range cs.config[len(cs.config)-(1<<20):] {
			cs.config[len(cs.config)-(1<<20)+i] = ' '
		}
		got := ProbeCloudflared(ctx, client, []string{cs.addr()}, true)
		if got.State != TunnelConnected || got.Hostnames != nil {
			t.Fatalf("Probe = %s, hostnames %s; want connected, nil", got.State, intp(got.Hostnames))
		}
	})
	t.Run("ready over 1 MiB", func(t *testing.T) {
		cs := newCloudflaredServer(t, "ready-200.json", http.StatusOK)
		cs.ready = append(fixture(t, "cloudflared/ready-200.json"), []byte(strings.Repeat(" ", 1<<20))...)
		if got := ProbeCloudflared(ctx, client, []string{cs.addr()}, true); got.State != TunnelUnknown {
			t.Fatalf("Probe = %s, want unknown", got.State)
		}
	})
	t.Run("not cloudflared", func(t *testing.T) {
		other := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(other.Close)
		got := ProbeCloudflared(ctx, client, []string{strings.TrimPrefix(other.URL, "http://")}, true)
		if got.State != TunnelUnknown || got.Hostnames != nil {
			t.Fatalf("Probe = %+v, want unknown", got)
		}
	})
	t.Run("redirect not followed", func(t *testing.T) {
		cs := newCloudflaredServer(t, "ready-200.json", http.StatusOK)
		redir := httptest.NewServer(http.RedirectHandler(cs.URL+"/ready", http.StatusFound))
		t.Cleanup(redir.Close)
		got := ProbeCloudflared(ctx, client, []string{strings.TrimPrefix(redir.URL, "http://")}, true)
		if got.State != TunnelUnknown || cs.hitsOf("/ready") != 0 {
			t.Fatalf("Probe through a redirect = %s with %d hits, want unknown and none", got.State, cs.hitsOf("/ready"))
		}
	})
	t.Run("nothing listening", func(t *testing.T) {
		got := ProbeCloudflared(ctx, client, []string{closedAddr(t), closedAddr(t)}, true)
		if got.State != TunnelUnknown || got.Hostnames != nil {
			t.Fatalf("Probe = %+v, want unknown", got)
		}
	})
	t.Run("not running", func(t *testing.T) {
		cs := newCloudflaredServer(t, "ready-200.json", http.StatusOK)
		got := ProbeCloudflared(ctx, client, []string{cs.addr()}, false)
		if got.State != TunnelNotRunning || got.Hostnames != nil || cs.hitsOf("/ready") != 0 {
			t.Fatalf("Probe = %+v with %d hits, want not_running without a request", got, cs.hitsOf("/ready"))
		}
	})
	t.Run("client", func(t *testing.T) {
		tr, ok := client.Transport.(*http.Transport)
		if !ok || tr.Proxy != nil || client.Timeout != 2*time.Second || client.CheckRedirect == nil {
			t.Fatalf("client: transport %T, proxy set %v, timeout %s; want no proxy, 2s, no redirects",
				client.Transport, ok && tr.Proxy != nil, client.Timeout)
		}
		// Only loopback addresses are dialled.
		start := time.Now()
		resp, err := client.Get("http://192.0.2.1:20241/ready")
		if err == nil {
			_ = resp.Body.Close()
			t.Fatal("the client dialled a non-loopback address")
		}
		if time.Since(start) > time.Second {
			t.Fatalf("refusing a non-loopback address took %s", time.Since(start))
		}
	})

	// The Monitor caches the answer for 15 s.
	t.Run("monitor cache", func(t *testing.T) {
		me := newMonitorEnv(t)
		cs := newCloudflaredServer(t, "ready-200.json", http.StatusOK)
		me.m.addrs = []string{cs.addr()}
		me.write(me.proc, "4242/comm", "cloudflared\n")
		if sys := me.m.System(ctx); sys.Tunnel.State != TunnelConnected {
			t.Fatalf("Tunnel = %+v, want connected", sys.Tunnel)
		}
		me.clock.add(14 * time.Second)
		me.m.System(ctx)
		if n := cs.hitsOf("/ready"); n != 1 {
			t.Fatalf("%d /ready requests within 15 s, want 1", n)
		}
		me.clock.add(2 * time.Second)
		cs.mu.Lock()
		cs.ready, cs.status = fixture(t, "cloudflared/ready-503.json"), http.StatusServiceUnavailable
		cs.mu.Unlock()
		if sys := me.m.System(ctx); sys.Tunnel.State != TunnelDisconnected || cs.hitsOf("/ready") != 2 {
			t.Fatalf("Tunnel after 16 s = %+v with %d requests, want disconnected and 2", sys.Tunnel, cs.hitsOf("/ready"))
		}
		if err := os.Remove(filepath.Join(me.proc, "4242", "comm")); err != nil {
			t.Fatal(err)
		}
		me.clock.add(16 * time.Second)
		if sys := me.m.System(ctx); sys.Tunnel.State != TunnelNotRunning || cs.hitsOf("/ready") != 2 {
			t.Fatalf("Tunnel without the process = %+v, want not_running without a request", sys.Tunnel)
		}
	})
	t.Run("default addresses", func(t *testing.T) {
		m := New(Options{})
		want := []string{"127.0.0.1:20241", "127.0.0.1:20242", "127.0.0.1:20243", "127.0.0.1:20244", "127.0.0.1:20245"}
		if strings.Join(m.addrs, ",") != strings.Join(want, ",") {
			t.Fatalf("addrs = %v, want %v", m.addrs, want)
		}
		if m := New(Options{CloudflaredAddr: "[::1]:6000"}); len(m.addrs) != 1 || m.addrs[0] != "[::1]:6000" {
			t.Fatalf("addrs = %v, want only [::1]:6000", m.addrs)
		}
	})
}

func TestThresholds(t *testing.T) {
	if (New(Options{})).Thresholds() != (Thresholds{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85}) {
		t.Fatal("Thresholds are not 90, 90, 85")
	}
}
