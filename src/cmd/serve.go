package cmd

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ranaroussi/minigun/internal/api"
	"github.com/ranaroussi/minigun/internal/config"
	"github.com/ranaroussi/minigun/internal/db"
	"github.com/ranaroussi/minigun/internal/mailgun"
	"github.com/ranaroussi/minigun/internal/store"
	"github.com/ranaroussi/minigun/internal/turnstile"
	"github.com/ranaroussi/minigun/internal/worker"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the MiniGun HTTP server",
	RunE:  runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	if err := cfg.RequireForServe(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	d, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := db.Migrate(ctx, d); err != nil {
		return err
	}

	st := store.New(d)
	mg := mailgun.New(cfg.MailgunAPIBase, cfg.MailgunAPIKey)
	ts := turnstile.New(cfg.TurnstileSecretKey)
	wm := worker.NewManager(cfg, st, mg, log)
	if err := wm.RecoverPending(ctx); err != nil {
		log.Error("recover pending sends", "err", err)
	}
	go wm.RunStatsScheduler(ctx, 15*time.Minute)
	// Events archive scheduler — internally no-ops when
	// cfg.EventsArchiveEnabled is false. We spawn the goroutine
	// unconditionally so flipping EVENTS_ARCHIVE_ENABLED=true at runtime
	// only requires a config reload, not a process restart.
	go wm.RunEventsArchiveScheduler(ctx, 15*time.Minute)

	srv := api.New(cfg, st, mg, wm, ts, log)
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if cfg.APIToken == "" {
		log.Warn("MINIGUN_API_TOKEN is not set; the API is open. Set MINIGUN_API_TOKEN to require Authorization: Bearer <token> on all endpoints (the /healthz, /u/{token} and /manage/{token} routes stay public).")
	} else {
		log.Info("bearer auth enabled (set via MINIGUN_API_TOKEN); /healthz, /u/{token}, /manage/{token} are exempt")
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown initiated")
	case err := <-errCh:
		log.Error("http server", "err", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}
	wm.Stop()
	return nil
}
