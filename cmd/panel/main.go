// Command panel is the server control panel's single binary.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/api"
	"github.com/daniel-vuky/server-control-panel/internal/auth"
	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/logs"
	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/roots"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
	"github.com/daniel-vuky/server-control-panel/internal/web"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// readTimeout bounds every docker command the panel runs to read state.
const readTimeout = 15 * time.Second

// recentContainers is how old the hub's container list may be for a backup's
// "is the database running" check before it reads Docker again.
const recentContainers = 10 * time.Second

// jobWait is how long shutdown waits for a running scan or lifecycle command to
// store its outcome before the database closes. One still running then is
// left; the next start marks it interrupted.
const jobWait = 5 * time.Second

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	os.Exit(run(log))
}

// run starts the panel and blocks until it stops; it returns the exit code.
// It is separate from main so that deferred cleanup runs before the exit.
func run(log *slog.Logger) int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "error", err)
		return 1
	}
	// Before anything reads the clock: dates in logs and backup file names use
	// the server zone.
	time.Local = cfg.Zone

	verifier, err := auth.DefaultVerifier(cfg)
	if err != nil {
		log.Error("invalid configuration", "error", err)
		return 1
	}

	dbPath := filepath.Join(cfg.DataDir, "panel.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		log.Error("cannot open the database", "path", dbPath, "error", err)
		return 1
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("close the database", "error", err)
		}
	}()

	docker := &runner.Runner{Bin: "docker", DataDir: cfg.DataDir, Timeout: readTimeout}
	guard := roots.New(cfg.WorkspaceRoots, log)
	scans := scan.NewManager(st, docker, log)
	ctl, hub := control.New(st, docker, log)
	// A rescan checks every workspace with the guard, and its Not found marks
	// and refreshes reach /api/events at once.
	scans.SetGuard(guard)
	scans.SetOnChange(hub.Refresh)
	// Log streams read the containers through the hub, so a stream's
	// container list is never older than a tick.
	logStreams := logs.New(docker, hub.Containers, time.Now)

	// Google Drive (the fake one in a dev build without GOOGLE_CLIENT_ID) and
	// the backup manager. Both publish to /api/events through the hub; their
	// sources are set before the API serves.
	driveSvc, err := drive.NewService(cfg, st, drive.NewClient(cfg, cfg.DataDir), time.Now)
	if err != nil {
		log.Error("cannot load the Google Drive connection", "error", err)
		return 1
	}
	if reason := cfg.DriveReason(); reason != "" {
		log.Info("Google Drive is not configured", "reason", reason)
	}
	recent := func(ctx context.Context) ([]project.Container, bool) {
		return hub.RecentContainers(ctx, recentContainers)
	}
	backups := backup.New(st, docker, driveSvc, recent, cfg.DataDir, time.Now, log)
	backups.SetMinFree(cfg.DevBackupMinFree)
	hub.SetBackupSource(backups.Active)
	hub.SetBatchSource(backups.CurrentBatch)
	hub.SetDriveSource(driveSvc.Status)
	backups.OnChange(hub.Publish)
	driveSvc.OnChange(hub.Publish)
	backups.Start(context.Background())
	defer backups.Close() // an early return; the shutdown below also waits
	// The scheduler queues through the manager, so it starts after it and
	// stops before it (deferred calls run last in, first out).
	sched := backup.NewScheduler(st, backups, cfg.Zone, time.Now, log)
	sched.Start(context.Background())
	defer sched.Close()

	// The server metrics: samples every 30 s (reusing the hub's fresh reads),
	// the hourly disk usage and the System cards. The host's /proc, its root
	// (read-only) and the Docker socket are mounted by deploy/compose.yaml.
	monitor := metrics.New(metrics.Options{
		Store:           st,
		Cmd:             docker,
		Source:          hub,
		Host:            metrics.Host{Proc: "/host/proc", Sys: "/sys", Root: "/host/root", DataDir: cfg.DataDir},
		Socket:          "/var/run/docker.sock",
		CloudflaredAddr: cfg.CloudflaredMetrics,
		Now:             time.Now,
		Log:             log,
	})
	monitor.Start(context.Background())
	defer monitor.Close() // an early return; the shutdown below closes it first

	// Long-lived requests (the SSE streams) watch this context, so they
	// return when shutdown begins and Shutdown does not wait for them.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: api.NewRouter(api.Deps{
			Version:  version,
			Verifier: verifier,
			Web:      web.Handler(),
			Guard:    guard,
			Store:    st,
			Scans:    scans,
			Docker:   docker,
			Control:  ctl,
			Hub:      hub,
			Logs:     logStreams,
			Drive:    driveSvc,
			Backups:  backups,
			Metrics:  monitor,
			Zone:     cfg.Zone,
			Now:      time.Now,
		}),
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		log.Error("cannot listen", "addr", cfg.Addr, "error", err)
		return 1
	}
	log.Info("panel listening", "addr", ln.Addr().String(), "version", version, "data", cfg.DataDir)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "error", err)
			return 1
		}
	case <-ctx.Done():
		stop() // restore default signal handling: a second Ctrl-C terminates immediately
		log.Info("shutting down")
	}

	cancelBase() // shutdown begins: end the SSE streams first
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		log.Error("shutdown failed", "error", shutdownErr)
	}

	// The store closes when run returns; let a running scan, lifecycle
	// command or backup store its outcome first, for a short while. A running
	// or queued backup ends interrupted.
	waitCtx, cancelWait := context.WithTimeout(context.Background(), jobWait)
	defer cancelWait()
	monitor.Close() // no sample is written after this
	sched.Close()   // no scheduled backup is queued after this
	backups.Close()
	if err := backups.Wait(waitCtx); err != nil {
		log.Warn("a backup is still stopping; the next start marks it interrupted", "error", err)
	}
	if err := scans.Wait(waitCtx); err != nil {
		log.Warn("a scan is still running; the next start marks it interrupted", "error", err)
	}
	if err := ctl.Wait(waitCtx); err != nil {
		log.Warn("a command is still running; the next start marks it interrupted", "error", err)
	}
	if shutdownErr != nil {
		return 1
	}
	return 0
}
