package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/audit"
	"github.com/11DingKing/silvercare-coordination/internal/auth"
	"github.com/11DingKing/silvercare-coordination/internal/benefit"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/config"
	"github.com/11DingKing/silvercare-coordination/internal/eligibility"
	"github.com/11DingKing/silvercare-coordination/internal/escalation"
	"github.com/11DingKing/silvercare-coordination/internal/httpapi"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/outbox"
	"github.com/11DingKing/silvercare-coordination/internal/plan"
	"github.com/11DingKing/silvercare-coordination/internal/provider"
	"github.com/11DingKing/silvercare-coordination/internal/resident"
	"github.com/11DingKing/silvercare-coordination/internal/resource"
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
	"github.com/11DingKing/silvercare-coordination/internal/visit"
	"github.com/11DingKing/silvercare-coordination/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	if err := ensureDatabaseDirectory(cfg.DatabasePath); err != nil {
		return err
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancelStartup := context.WithTimeout(rootCtx, 20*time.Second)
	defer cancelStartup()
	repo, err := storesqlite.Open(startupCtx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer repo.Close()
	if err := repo.Migrate(startupCtx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	ids := idgen.Random{}
	now := clock.Real{}
	if err := auth.Bootstrap(startupCtx, repo, ids, cfg.Bootstrap, now.Now()); err != nil {
		return fmt.Errorf("bootstrap application: %w", err)
	}
	recorder := audit.NewRecorder(repo)
	events := outbox.NewPublisher(repo, ids)
	services := httpapi.Services{
		Auth:        auth.NewService(repo, ids, now, cfg.SessionTTL),
		Residents:   resident.NewService(repo, ids, now, recorder, events),
		Eligibility: eligibility.NewService(repo, ids, now, recorder, events),
		Plans:       plan.NewService(repo, ids, now, recorder, events),
		Providers:   provider.NewService(repo, ids, now, recorder, events),
		Benefits:    benefit.NewService(repo, ids, now, recorder, events),
		Visits:      visit.NewService(repo, ids, now, recorder, events),
		Resources:   resource.NewService(repo, ids, now, recorder, events),
		Escalations: escalation.NewService(repo, ids, now, recorder, events),
		Audit:       audit.NewService(repo),
	}
	api := httpapi.New(services, repo, ids, logger)
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           api,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	maintenance := worker.NewMaintenance(repo, now)
	jobRunner := worker.NewRunner(repo, now, logger, "jobs-main", cfg.WorkerInterval)
	if err := jobRunner.Register("cleanup_sessions", worker.HandlerFunc(maintenance.CleanupSessions)); err != nil {
		return err
	}
	if err := jobRunner.Register("cleanup_idempotency", worker.HandlerFunc(maintenance.CleanupIdempotency)); err != nil {
		return err
	}
	if err := jobRunner.Register("expire_escalations", worker.HandlerFunc(maintenance.ExpireEscalations)); err != nil {
		return err
	}
	outboxRunner := worker.NewOutboxRunner(repo, worker.NewLoggingSink(logger), now, logger, "outbox-main", cfg.WorkerInterval)

	errCh := make(chan error, 3)
	go func() {
		logger.Info("http server listening", "address", cfg.Address)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serve http: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() { errCh <- normalizeContextError(jobRunner.Run(rootCtx)) }()
	go func() { errCh <- normalizeContextError(outboxRunner.Run(rootCtx)) }()

	var runErr error
	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received")
	case runErr = <-errCh:
		if runErr != nil {
			stop()
		}
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown http server: %w", err)
	}
	return runErr
}

func ensureDatabaseDirectory(path string) error {
	if path == ":memory:" || len(path) >= 5 && path[:5] == "file:" {
		return nil
	}
	directory := filepath.Dir(path)
	if directory == "." {
		return nil
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	return nil
}

func newLogger(level string) *slog.Logger {
	var selected slog.Level
	switch level {
	case "debug":
		selected = slog.LevelDebug
	case "warn":
		selected = slog.LevelWarn
	case "error":
		selected = slog.LevelError
	default:
		selected = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: selected}))
}

func normalizeContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
