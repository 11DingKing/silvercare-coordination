package resource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
	CreateResource(context.Context, store.DBTX, domain.Resource) error
	ResourceByID(context.Context, store.DBTX, string, string) (domain.Resource, error)
	UpdateResource(context.Context, store.DBTX, domain.Resource, int64) error
	ResidentByID(context.Context, store.DBTX, string, string) (domain.Resident, error)
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

func (s *Service) Register(ctx context.Context, actor domain.Actor, resourceType, serial string) (domain.Resource, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Resource{}, apperr.New(apperr.CodeForbidden, "only coordinators can register support resources")
	}
	id, err := s.ids.New("resource")
	if err != nil {
		return domain.Resource{}, apperr.Internal("generate resource id", err)
	}
	now := s.now.Now()
	entity, err := domain.NewResource(id, actor.DistrictID, resourceType, serial, now)
	if err != nil {
		return domain.Resource{}, apperr.Validation("resource", err.Error())
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.CreateResource(ctx, tx, entity); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actor, "resource", id, "register", "success", map[string]any{"serial": serial}, now)
	})
	if err != nil {
		return domain.Resource{}, apperr.Internal("register resource", err)
	}
	return entity, nil
}

func (s *Service) Assign(ctx context.Context, actor domain.Actor, resourceID, residentID string, dueAt time.Time, expectedVersion int64) (domain.Resource, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Resource{}, apperr.New(apperr.CodeForbidden, "only coordinators can assign support resources")
	}
	now := s.now.Now()
	var updated domain.Resource
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.ResourceByID(ctx, tx, resourceID, actor.DistrictID)
		if err != nil {
			return err
		}
		resident, err := s.repo.ResidentByID(ctx, tx, residentID, actor.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("resource version conflict")
		}
		updated, err = current.Assign(resident, dueAt, now)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateResource(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "resource", resourceID, "assign", "success", map[string]any{"resident_id": residentID, "due_at": dueAt}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "resource.assigned", "resource", resourceID, map[string]any{"resident_id": residentID}, now)
	})
	if err != nil {
		return domain.Resource{}, mapResourceError("assign resource", err)
	}
	return updated, nil
}

func (s *Service) Return(ctx context.Context, actor domain.Actor, resourceID string, quarantine bool, expectedVersion int64) (domain.Resource, error) {
	if actor.Role != domain.RoleCoordinator && actor.Role != domain.RoleProvider {
		return domain.Resource{}, apperr.New(apperr.CodeForbidden, "this role cannot receive returned resources")
	}
	now := s.now.Now()
	var updated domain.Resource
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.ResourceByID(ctx, tx, resourceID, actor.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("resource version conflict")
		}
		updated, err = current.Return(quarantine, now)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateResource(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "resource", resourceID, "return", "success", map[string]any{"status": updated.Status}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "resource.returned", "resource", resourceID, map[string]any{"status": updated.Status}, now)
	})
	if err != nil {
		return domain.Resource{}, mapResourceError("return resource", err)
	}
	return updated, nil
}

func mapResourceError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.CodeNotFound, "resource or resident was not found")
	}
	return apperr.Wrap(apperr.CodeConflict, operation, err.Error(), err)
}
