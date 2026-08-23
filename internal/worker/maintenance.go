package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

type MaintenanceRepository interface {
	DeleteExpiredSessions(context.Context, time.Time) (int64, error)
	DeleteExpiredIdempotency(context.Context, time.Time) (int64, error)
	OverdueOpenEscalations(context.Context, store.DBTX, time.Time, int) ([]domain.Escalation, error)
	PersistEscalationExpiry(context.Context, domain.Escalation, int64) error
}

type Maintenance struct {
	repo MaintenanceRepository
	now  clock.Clock
}

func NewMaintenance(repo MaintenanceRepository, now clock.Clock) *Maintenance {
	return &Maintenance{repo: repo, now: now}
}

func (m *Maintenance) CleanupSessions(ctx context.Context, _ domain.WorkerJob) error {
	_, err := m.repo.DeleteExpiredSessions(ctx, m.now.Now())
	return err
}

func (m *Maintenance) CleanupIdempotency(ctx context.Context, _ domain.WorkerJob) error {
	_, err := m.repo.DeleteExpiredIdempotency(ctx, m.now.Now())
	return err
}

func (m *Maintenance) ExpireEscalations(ctx context.Context, _ domain.WorkerJob) error {
	now := m.now.Now()
	items, err := m.repo.OverdueOpenEscalations(ctx, nil, now, 100)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, current := range items {
		updated, err := current.Expire(now)
		if err != nil {
			return fmt.Errorf("expire escalation %s: %w", current.ID, err)
		}
		if err := m.repo.PersistEscalationExpiry(ctx, updated, current.Version); err != nil {
			return fmt.Errorf("persist escalation %s expiry: %w", current.ID, err)
		}
	}
	return nil
}
