package provider

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/11DingKing/silvercare-coordination/internal/apperr"
	"github.com/11DingKing/silvercare-coordination/internal/audit"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/outbox"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

type Repository interface {
	WithinTx(context.Context, func(*sql.Tx) error) error
	CreateProvider(context.Context, store.DBTX, domain.Provider) error
	ProviderByID(context.Context, store.DBTX, string, string) (domain.Provider, error)
	UpdateProvider(context.Context, store.DBTX, domain.Provider, int64) error
}

type Service struct {
	repo   Repository
	ids    idgen.Generator
	now    clock.Clock
	audit  *audit.Recorder
	events *outbox.Publisher
}

func NewService(repo Repository, ids idgen.Generator, now clock.Clock, recorder *audit.Recorder, events *outbox.Publisher) *Service {
	return &Service{repo: repo, ids: ids, now: now, audit: recorder, events: events}
}

func (s *Service) Register(ctx context.Context, actor domain.Actor, name string, capabilities []string, capacity int) (domain.Provider, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Provider{}, apperr.New(apperr.CodeForbidden, "only coordinators can register providers")
	}
	id, err := s.ids.New("provider")
	if err != nil {
		return domain.Provider{}, apperr.Internal("generate provider id", err)
	}
	now := s.now.Now()
	entity, err := domain.NewProvider(id, actor.DistrictID, name, capabilities, capacity, now)
	if err != nil {
		return domain.Provider{}, apperr.Validation("provider", err.Error())
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.CreateProvider(ctx, tx, entity); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actor, "provider", id, "register", "success", map[string]any{"name": entity.Name}, now)
	})
	if err != nil {
		return domain.Provider{}, apperr.Internal("register provider", err)
	}
	return entity, nil
}

func (s *Service) Activate(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.Provider, error) {
	return s.change(ctx, actor, id, expectedVersion, "activate", func(p domain.Provider) (domain.Provider, error) { return p.Activate(s.now.Now()) })
}

func (s *Service) Suspend(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.Provider, error) {
	return s.change(ctx, actor, id, expectedVersion, "suspend", func(p domain.Provider) (domain.Provider, error) { return p.Suspend(s.now.Now()) })
}

func (s *Service) change(ctx context.Context, actor domain.Actor, id string, expectedVersion int64, action string, mutate func(domain.Provider) (domain.Provider, error)) (domain.Provider, error) {
	if actor.Role != domain.RoleCoordinator && actor.Role != domain.RoleAuditor {
		return domain.Provider{}, apperr.New(apperr.CodeForbidden, "this role cannot change provider accreditation")
	}
	now := s.now.Now()
	var updated domain.Provider
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.ProviderByID(ctx, tx, id, actor.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("provider version conflict")
		}
		updated, err = mutate(current)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateProvider(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "provider", id, action, "success", map[string]any{"status": updated.AccreditationStatus}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "provider.accreditation_changed", "provider", id, map[string]any{"status": updated.AccreditationStatus}, now)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Provider{}, apperr.NotFound("provider", id)
		}
		return domain.Provider{}, apperr.Wrap(apperr.CodeConflict, action+" provider", err.Error(), err)
	}
	return updated, nil
}
