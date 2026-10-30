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
	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/control"
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
	scans := scan.NewManager(st, docker, log)
	ctl, hub := control.New(st, docker, log)

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
			Guard:    roots.New(cfg.WorkspaceRoots, log),
			Store:    st,
			Scans:    scans,
			Docker:   docker,
			Control:  ctl,
			Hub:      hub,
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

	// The store closes when run returns; let a running scan or lifecycle
	// command store its outcome first, for a short while.
	waitCtx, cancelWait := context.WithTimeout(context.Background(), jobWait)
	defer cancelWait()
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
