package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

type OutboxRepository interface {
	ClaimOutbox(context.Context, string, time.Time, time.Duration) (domain.OutboxEvent, error)
	MarkOutboxDelivered(context.Context, string, string, time.Time) error
	RetryOutbox(context.Context, string, string, string, time.Time, bool) error
}

type EventSink interface {
	Deliver(context.Context, domain.OutboxEvent) error
}

type EventSinkFunc func(context.Context, domain.OutboxEvent) error

func (f EventSinkFunc) Deliver(ctx context.Context, event domain.OutboxEvent) error {
	return f(ctx, event)
}

type OutboxRunner struct {
	repo        OutboxRepository
	sink        EventSink
	now         clock.Clock
	logger      *slog.Logger
	workerID    string
	interval    time.Duration
	lease       time.Duration
	maxAttempts int
}

func NewOutboxRunner(repo OutboxRepository, sink EventSink, now clock.Clock, logger *slog.Logger, workerID string, interval time.Duration) *OutboxRunner {
	return &OutboxRunner{repo: repo, sink: sink, now: now, logger: logger,
		workerID: workerID, interval: interval, lease: 30 * time.Second, maxAttempts: 8}
}

func (r *OutboxRunner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if err := r.DrainOne(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, context.Canceled) {
			r.logger.ErrorContext(ctx, "outbox cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *OutboxRunner) DrainOne(ctx context.Context) error {
	now := r.now.Now()
	event, err := r.repo.ClaimOutbox(ctx, r.workerID, now, r.lease)
	if err != nil {
		return err
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, r.lease/2)
	err = r.sink.Deliver(deliveryCtx, event)
	cancel()
	if err != nil {
		dead := event.Attempts >= r.maxAttempts
		available := r.now.Now().Add(retryBackoff(event.Attempts))
		if saveErr := r.repo.RetryOutbox(ctx, event.ID, r.workerID, err.Error(), available, dead); saveErr != nil {
			return fmt.Errorf("save outbox failure after delivery error %v: %w", err, saveErr)
		}
		return fmt.Errorf("deliver outbox event %s: %w", event.ID, err)
	}
	if err := r.repo.MarkOutboxDelivered(ctx, event.ID, r.workerID, r.now.Now()); err != nil {
		return fmt.Errorf("mark outbox event %s delivered: %w", event.ID, err)
	}
	r.logger.InfoContext(ctx, "outbox event delivered", "event_id", event.ID, "topic", event.Topic)
	return nil
}

type LoggingSink struct{ logger *slog.Logger }

func NewLoggingSink(logger *slog.Logger) *LoggingSink { return &LoggingSink{logger: logger} }

func (s *LoggingSink) Deliver(ctx context.Context, event domain.OutboxEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "domain event", "topic", event.Topic,
		"aggregate_type", event.AggregateType, "aggregate_id", event.AggregateID,
		"district_id", event.DistrictID)
	return nil
}
