package alerts

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// domainServer is every public domain: a TLS test server answering status.
type domainServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	seen   []string // host and path of each request
}

func newDomainServer(t *testing.T) *domainServer {
	t.Helper()
	d := &domainServer{status: http.StatusOK}
	d.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.seen = append(d.seen, r.Host+r.URL.Path)
		status := d.status
		d.mu.Unlock()
		if status == http.StatusFound {
			w.Header().Set("Location", "/cdn-cgi/access/login")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("a body nobody reads"))
	}))
	d.srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the refused handshakes of TestDomainTLSFailure
	d.srv.StartTLS()
	t.Cleanup(d.srv.Close)
	return d
}

func (d *domainServer) setStatus(s int) {
	d.mu.Lock()
	d.status = s
	d.mu.Unlock()
}

func (d *domainServer) requests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.seen)
}

// client is NewDomainClient with every dial sent to the test server; trusted
// says whether it trusts the server's certificate (issued for example.com).
func (d *domainServer) client(trusted bool) *http.Client {
	c := NewDomainClient()
	tr := c.Transport.(*http.Transport).Clone()
	addr := d.srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, addr)
	}
	tr.TLSClientConfig = &tls.Config{ServerName: "example.com", MinVersion: tls.VersionTLS12}
	if trusted {
		tr.TLSClientConfig.RootCAs = d.srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	}
	c.Transport = tr
	return c
}

const shopDomain = "shop.example.com"

// domainRig is a watch rig whose seeded shop-api is Running with the public
// domain shop.example.com, served by the test server.
func domainRig(t *testing.T, trusted bool) (*watchRig, *domainServer) {
	t.Helper()
	ds := newDomainServer(t)
	wr := newWatchRig(t, ds.client(trusted), "shop-api")
	wr.start(t)
	wr.setApp("shop-api", wr.clock.Now(), "running", shopDomain)
	return wr, ds
}

// check runs two passes, the first of which checks the domains.
func (wr *watchRig) check() {
	wr.pass()
	wr.clock.Add(30 * time.Second)
	wr.pass()
	wr.clock.Add(30 * time.Second)
}

func TestDomainThreeFailures(t *testing.T) {
	wr, ds := domainRig(t, true)
	key := projectKey(wr.ids["shop-api"], "domain")
	ds.setStatus(http.StatusServiceUnavailable)
	for i := 1; i <= 3; i++ {
		wr.check()
		if got := len(ds.requests()); got != i {
			t.Fatalf("after %d checks: %d requests", i, got)
		}
		if open, want := wr.isOpen(key), i == 3; open != want {
			t.Fatalf("check %d: open = %v, want %v", i, open, want)
		}
	}
	wr.check() // still failing: no second alert
	if got := ds.requests(); got[0] != "shop.example.com/" {
		t.Fatalf("request = %q", got[0])
	}
	ms := wr.settle(t)
	wantTitles(t, ms, "shop.example.com (shop-api) did not answer 3 checks")
	if text := ms[0].Title + " · " + ms[0].Detail; text != "shop.example.com (shop-api) did not answer 3 checks · 503" {
		t.Fatalf("message = %q", text)
	}
	if row := wr.rows(t)[0]; row.Action != store.ActAlertDomain || row.ProjectID != wr.ids["shop-api"] || row.Target != "shop-api" {
		t.Fatalf("row = %+v", row)
	}
}

func TestDomainAnsweredBelow500(t *testing.T) {
	wr, ds := domainRig(t, true)
	key := projectKey(wr.ids["shop-api"], "domain")
	for _, s := range []int{503, 502, http.StatusNotFound, 530, 500, http.StatusFound, 503, 504} {
		ds.setStatus(s)
		wr.check()
		if wr.isOpen(key) {
			t.Fatalf("open after %d: a 404 or a 302 must reset the count", s)
		}
	}
	ds.setStatus(http.StatusBadGateway)
	wr.check()
	if !wr.isOpen(key) {
		t.Fatal("no alert after 3 failures in a row")
	}
	if ms := wr.settle(t); ms[0].Detail != "502" {
		t.Fatalf("detail = %q", ms[0].Detail)
	}
}

func TestDomainTLSFailure(t *testing.T) {
	wr, _ := domainRig(t, false)
	for range 3 {
		wr.check()
	}
	if !wr.isOpen(projectKey(wr.ids["shop-api"], "domain")) {
		t.Fatal("an unverified certificate was taken as an answer")
	}
	if ms := wr.settle(t); ms[0].Detail != "TLS error" {
		t.Fatalf("detail = %q", ms[0].Detail)
	}
}

func TestDomainRecovery(t *testing.T) {
	wr, ds := domainRig(t, true)
	key := projectKey(wr.ids["shop-api"], "domain")
	ds.setStatus(http.StatusServiceUnavailable)
	for range 3 {
		wr.check()
	}
	if !wr.isOpen(key) {
		t.Fatal("no alert")
	}
	ds.setStatus(http.StatusFound)
	wr.check()
	if wr.isOpen(key) {
		t.Fatal("still open after an answer")
	}
	wantTitles(t, wr.settle(t), "shop.example.com (shop-api) did not answer 3 checks", "✅ shop.example.com (shop-api) is answering again")
}

func TestDomainNotCheckedWhenStopped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scene func(wr *watchRig)
	}{
		{"stopped", func(wr *watchRig) { wr.src.set(func(f *fakeSources) { f.cs = nil }) }},
		{"unknown", func(wr *watchRig) { wr.src.set(func(f *fakeSources) { f.dockerUp, f.cs = false, nil }) }},
		{"not found", func(wr *watchRig) {
			missing := time.Now()
			wr.src.set(func(f *fakeSources) { f.tracked[0].MissingSince = &missing })
		}},
		{"in flight", func(wr *watchRig) {
			id := wr.ids["shop-api"]
			wr.src.set(func(f *fakeSources) { f.locks = map[int64]project.Status{id: project.StatusStarting} })
		}},
		{"no cached read", func(wr *watchRig) { wr.src.set(func(f *fakeSources) { f.csOK = false }) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wr, ds := domainRig(t, true)
			tc.scene(wr)
			for range 4 {
				wr.check()
			}
			if got := ds.requests(); len(got) != 0 {
				t.Fatalf("requests = %q, want none", got)
			}
		})
	}
	t.Run("error is checked", func(t *testing.T) {
		wr, ds := domainRig(t, true)
		wr.setApp("shop-api", wr.clock.Now(), "restarting", shopDomain)
		wr.check()
		if got := len(ds.requests()); got != 1 {
			t.Fatalf("requests = %d, want 1", got)
		}
	})
}

func TestDomainStoppedClosesSilently(t *testing.T) {
	wr, ds := domainRig(t, true)
	key := projectKey(wr.ids["shop-api"], "domain")
	ds.setStatus(http.StatusServiceUnavailable)
	for range 3 {
		wr.check()
	}
	wr.src.set(func(f *fakeSources) { f.cs = nil }) // Stopped
	wr.pass()
	if wr.isOpen(key) {
		t.Fatal("still open after the project stopped")
	}
	// Started again, still failing: the count started over.
	wr.setApp("shop-api", wr.clock.Now(), "running", shopDomain)
	for range 2 {
		wr.check()
	}
	if wr.isOpen(key) {
		t.Fatal("the count did not start over")
	}
	wantTitles(t, wr.settle(t), "shop.example.com (shop-api) did not answer 3 checks")
}

func TestDomainChangedClosesSilently(t *testing.T) {
	wr, ds := domainRig(t, true)
	key := projectKey(wr.ids["shop-api"], "domain")
	ds.setStatus(http.StatusServiceUnavailable)
	for range 3 {
		wr.check()
	}
	wr.setApp("shop-api", wr.clock.Now(), "running", "shop2.example.com")
	wr.check() // the new domain's first failure
	if wr.isOpen(key) {
		t.Fatal("still open after the domain changed")
	}
	wr.check()
	if wr.isOpen(key) {
		t.Fatal("the new domain inherited the old count")
	}
	wr.check()
	if !wr.isOpen(key) {
		t.Fatal("no alert for the new domain")
	}
	wantTitles(t, wr.settle(t),
		"shop.example.com (shop-api) did not answer 3 checks",
		"shop2.example.com (shop-api) did not answer 3 checks")
}

func TestDomainPausedOnlyWhileSeenTunnelDown(t *testing.T) {
	t.Run("seen and down", func(t *testing.T) {
		wr, ds := domainRig(t, true)
		wr.src.set(func(f *fakeSources) { f.seen, f.tunnel = true, metrics.TunnelDisconnected })
		ds.setStatus(http.StatusBadGateway)
		for range 5 {
			wr.check()
		}
		if wr.isOpen(projectKey(wr.ids["shop-api"], "domain")) {
			t.Fatal("domain failures were counted while the seen tunnel was down")
		}
		if !wr.isOpen(keyTunnel) {
			t.Fatal("no tunnel alert")
		}
		wantTitles(t, wr.settle(t), "Cloudflare Tunnel disconnected")

		// The tunnel is back: failures count again, from zero.
		wr.src.set(func(f *fakeSources) { f.tunnel = metrics.TunnelConnected })
		for range 2 {
			wr.check()
		}
		if wr.isOpen(projectKey(wr.ids["shop-api"], "domain")) {
			t.Fatal("failures from the pause were counted")
		}
		wr.check()
		if !wr.isOpen(projectKey(wr.ids["shop-api"], "domain")) {
			t.Fatal("no domain alert once the tunnel is back")
		}
	})
	t.Run("never seen", func(t *testing.T) {
		wr, ds := domainRig(t, true)
		wr.src.set(func(f *fakeSources) { f.tunnel = metrics.TunnelNotRunning })
		ds.setStatus(http.StatusBadGateway)
		for range 3 {
			wr.check()
		}
		if !wr.isOpen(projectKey(wr.ids["shop-api"], "domain")) {
			t.Fatal("domain failures were not counted on a server with no tunnel seen")
		}
	})
}

func TestNewDomainClient(t *testing.T) {
	c := NewDomainClient()
	if c.Timeout != 10*time.Second {
		t.Fatalf("Timeout = %s", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatalf("transport = %T, proxy set = %v", c.Transport, ok && tr.Proxy != nil)
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS is not verified")
	}
	if c.CheckRedirect == nil || !errors.Is(c.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("a redirect would be followed")
	}
}
