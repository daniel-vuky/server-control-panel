// Command panel is the server control panel's single binary.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/api"
	"github.com/daniel-vuky/server-control-panel/internal/auth"
	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: api.NewRouter(api.Deps{
			Version:  version,
			Verifier: auth.DefaultVerifier(),
			Web:      http.NotFoundHandler(), // replaced by the embedded SPA later
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("panel listening", "addr", cfg.Addr, "version", version)

	select {
	case err := <-errc:
		log.Error("server failed", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
}
