// Command requara runs the Requara backend: JSON API, sign-in (OIDC, GitHub,
// SAML, email links), the admin API and SCIM, request runner, mock servers
// and background jobs.
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

	"github.com/anilcharles/requara/server/api/internal/api"
	"github.com/anilcharles/requara/server/api/internal/auth"
	"github.com/anilcharles/requara/server/api/internal/config"
	"github.com/anilcharles/requara/server/api/internal/db"
	"github.com/anilcharles/requara/server/api/internal/jobs"
	"github.com/anilcharles/requara/server/api/internal/seed"
	"github.com/anilcharles/requara/server/api/internal/storage"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("requara stopped", "err", err)
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
	signin, err := auth.New(ctx, cfg.Providers, cfg.FrontendURL, cfg.SessionKey, api.StateStore{DB: pool})
	if err != nil {
		return err
	}
	if err := api.SyncAdmins(ctx, pool, cfg); err != nil {
		return err
	}
	if err := seed.Run(ctx, pool, baoClient, cfg.PublicURL, api.SyncRequests); err != nil {
		return err
	}

	app := api.New(cfg, pool, baoClient, signin, s3, jobClient)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Every minute: run the monitors that are due.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				app.RunDueMonitors(ctx)
			}
		}
	}()
	// Daily: drop audit events past REQUARA_AUDIT_RETENTION_DAYS and expired sign-in state.
	go func() {
		app.Prune(ctx)
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				app.Prune(ctx)
			}
		}
	}()
	errc := make(chan error, 1)
	go func() {
		slog.Info("requara listening", "addr", cfg.Addr, "app", cfg.FrontendURL)
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
