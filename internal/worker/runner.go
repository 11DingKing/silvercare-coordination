package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

type JobRepository interface {
	ClaimJob(context.Context, string, time.Time, time.Duration) (domain.WorkerJob, error)
	CompleteJob(context.Context, string, string, time.Time) error
	FailJob(context.Context, domain.WorkerJob, string, string, time.Time, time.Time) error
}

type Handler interface {
	Handle(context.Context, domain.WorkerJob) error
}

type HandlerFunc func(context.Context, domain.WorkerJob) error

func (f HandlerFunc) Handle(ctx context.Context, job domain.WorkerJob) error { return f(ctx, job) }

type Runner struct {
	repo     JobRepository
	clock    clock.Clock
	logger   *slog.Logger
	workerID string
	interval time.Duration
	lease    time.Duration
	handlers map[string]Handler
	mu       sync.RWMutex
}

func NewRunner(repo JobRepository, now clock.Clock, logger *slog.Logger, workerID string, interval time.Duration) *Runner {
	return &Runner{repo: repo, clock: now, logger: logger, workerID: workerID,
		interval: interval, lease: 30 * time.Second, handlers: map[string]Handler{}}
}

func (r *Runner) Register(kind string, handler Handler) error {
	if kind == "" || handler == nil {
		return fmt.Errorf("worker kind and handler are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		return fmt.Errorf("worker handler %q already exists", kind)
	}
	r.handlers[kind] = handler
	return nil
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if err := r.DrainOne(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, context.Canceled) {
			r.logger.ErrorContext(ctx, "worker cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Runner) DrainOne(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := r.clock.Now()
	job, err := r.repo.ClaimJob(ctx, r.workerID, now, r.lease)
	if err != nil {
		return err
	}
	r.mu.RLock()
	handler := r.handlers[job.Kind]
	r.mu.RUnlock()
	if handler == nil {
		return r.fail(ctx, job, fmt.Errorf("no handler registered for job kind %q", job.Kind))
	}
	handleCtx, cancel := context.WithTimeout(ctx, r.lease/2)
	err = handler.Handle(handleCtx, job)
	cancel()
	if err != nil {
		return r.fail(ctx, job, err)
	}
	if err := r.repo.CompleteJob(ctx, job.ID, r.workerID, r.clock.Now()); err != nil {
		return fmt.Errorf("complete job %s: %w", job.ID, err)
	}
	r.logger.InfoContext(ctx, "worker job completed", "job_id", job.ID, "kind", job.Kind, "attempt", job.Attempts)
	return nil
}

func (r *Runner) fail(ctx context.Context, job domain.WorkerJob, cause error) error {
	now := r.clock.Now()
	backoff := retryBackoff(job.Attempts)
	if err := r.repo.FailJob(ctx, job, r.workerID, cause.Error(), now.Add(backoff), now); err != nil {
		return fmt.Errorf("record job %s failure after %v: %w", job.ID, cause, err)
	}
	r.logger.WarnContext(ctx, "worker job failed", "job_id", job.ID, "kind", job.Kind,
		"attempt", job.Attempts, "max_attempts", job.MaxAttempts, "error", cause)
	return cause
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	seconds := math.Pow(2, float64(attempt-1))
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}
