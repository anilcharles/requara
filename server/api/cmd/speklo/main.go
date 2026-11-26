// Command speklo runs the Speklo backend: JSON API, single sign-on (any OIDC provider), request
// runner, mock servers and background jobs.
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

	"github.com/anilcharles/speklo/server/api/internal/api"
	"github.com/anilcharles/speklo/server/api/internal/auth"
	"github.com/anilcharles/speklo/server/api/internal/config"
	"github.com/anilcharles/speklo/server/api/internal/db"
	"github.com/anilcharles/speklo/server/api/internal/jobs"
	"github.com/anilcharles/speklo/server/api/internal/seed"
	"github.com/anilcharles/speklo/server/api/internal/storage"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("speklo stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, baoClient, err := config.Load(ctx)
	if err != nil {
		return err
	}
	slog.Info("secrets loaded from OpenBao", "env", cfg.Env)

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	s3, err := storage.New(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, false)
	if err != nil {
		return err
	}
	jobClient, err := jobs.Start(ctx, pool, cfg.SMTPAddr, cfg.SMTPFrom)
	if err != nil {
		return err
	}
	signin, err := auth.New(ctx, cfg.Providers, cfg.FrontendURL+"/auth/callback", cfg.SessionKey)
	if err != nil {
		return err
	}
	if err := seed.Run(ctx, pool, baoClient, cfg.PublicURL, api.SyncRequests); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(cfg, pool, baoClient, signin, s3, jobClient).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("speklo listening", "addr", cfg.Addr, "app", cfg.FrontendURL)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	return jobClient.Stop(shutdown)
}
