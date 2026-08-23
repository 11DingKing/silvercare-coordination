package visit

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
	AuthorizationByID(context.Context, store.DBTX, string, string) (domain.Authorization, error)
	UpdateAuthorization(context.Context, store.DBTX, domain.Authorization, int64) error
	PersistAuthorizationConsumption(context.Context, domain.Authorization, int64) error
	ProviderByID(context.Context, store.DBTX, string, string) (domain.Provider, error)
	ProviderVisitCountOnDay(context.Context, store.DBTX, string, time.Time) (int, error)
	ResidentByID(context.Context, store.DBTX, string, string) (domain.Resident, error)
	CreateVisit(context.Context, store.DBTX, domain.Visit) error
	VisitByID(context.Context, store.DBTX, string, string) (domain.Visit, error)
	UpdateVisit(context.Context, store.DBTX, domain.Visit, int64) error
	CountProviderWindowConflicts(context.Context, store.DBTX, string, string, time.Time, time.Time) (int, error)
	CountResidentWindowConflicts(context.Context, store.DBTX, string, string, time.Time, time.Time) (int, error)
	UserByIDWith(context.Context, store.DBTX, string) (domain.User, error)
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

func (s *Service) Schedule(ctx context.Context, actor domain.Actor, authorizationID, providerID, residentID string, start, end time.Time) (domain.Visit, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Visit{}, apperr.New(apperr.CodeForbidden, "only coordinators can schedule visits")
	}
	id, err := s.ids.New("visit")
	if err != nil {
		return domain.Visit{}, apperr.Internal("generate visit id", err)
	}
	now := s.now.Now()
	var entity domain.Visit
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		a, err := s.repo.AuthorizationByID(ctx, tx, authorizationID, actor.DistrictID)
		if err != nil {
			return err
		}
		provider, err := s.repo.ProviderByID(ctx, tx, providerID, actor.DistrictID)
		if err != nil {
			return err
		}
		resident, err := s.repo.ResidentByID(ctx, tx, residentID, actor.DistrictID)
		if err != nil {
			return err
		}
		if !resident.HasActiveConsent(now) {
			return fmt.Errorf("resident consent is not active")
		}
		entity, err = domain.NewVisit(id, a, provider, residentID, start, end, now)
		if err != nil {
			return err
		}
		providerConflicts, err := s.repo.CountProviderWindowConflicts(ctx, tx, providerID, "", start, end)
		if err != nil {
			return err
		}
		residentConflicts, err := s.repo.CountResidentWindowConflicts(ctx, tx, residentID, "", start, end)
		if err != nil {
			return err
		}
		if providerConflicts > 0 || residentConflicts > 0 {
			return fmt.Errorf("visit overlaps an existing provider or resident commitment")
		}
		dailyCount, err := s.repo.ProviderVisitCountOnDay(ctx, tx, providerID, start)
		if err != nil {
			return err
		}
		if dailyCount >= provider.CapacityPerDay {
			return fmt.Errorf("provider daily capacity is exhausted")
		}
		if err := s.repo.CreateVisit(ctx, tx, entity); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "visit", id, "schedule", "success", map[string]any{"provider_id": providerID, "resident_id": residentID}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "visit.scheduled", "visit", id, map[string]any{"scheduled_start": start}, now)
	})
	if err != nil {
		return domain.Visit{}, mapVisitError("schedule visit", err)
	}
	return entity, nil
}

func (s *Service) Assign(ctx context.Context, actor domain.Actor, visitID, userID string, expectedVersion int64) (domain.Visit, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Visit{}, apperr.New(apperr.CodeForbidden, "only coordinators can assign visits")
	}
	return s.change(ctx, actor, visitID, expectedVersion, "assign", func(tx *sql.Tx, current domain.Visit, now time.Time) (domain.Visit, error) {
		user, err := s.repo.UserByIDWith(ctx, tx, userID)
		if err != nil {
			return domain.Visit{}, err
		}
		if user.DistrictID != actor.DistrictID || user.Role != domain.RoleProvider || !user.Active {
			return domain.Visit{}, fmt.Errorf("assignee is not an active provider user in this district")
		}
		return current.Assign(userID, now)
	})
}

func (s *Service) Accept(ctx context.Context, actor domain.Actor, visitID string, expectedVersion int64) (domain.Visit, error) {
	if actor.Role != domain.RoleProvider {
		return domain.Visit{}, apperr.New(apperr.CodeForbidden, "only provider users can accept visits")
	}
	return s.change(ctx, actor, visitID, expectedVersion, "accept", func(_ *sql.Tx, current domain.Visit, now time.Time) (domain.Visit, error) {
		return current.Accept(actor.UserID, now)
	})
}

func (s *Service) StartTravel(ctx context.Context, actor domain.Actor, visitID string, expectedVersion int64) (domain.Visit, error) {
	if actor.Role != domain.RoleProvider {
		return domain.Visit{}, apperr.New(apperr.CodeForbidden, "only provider users can start travel")
	}
	return s.change(ctx, actor, visitID, expectedVersion, "start_travel", func(_ *sql.Tx, current domain.Visit, now time.Time) (domain.Visit, error) {
		return current.StartTravel(actor.UserID, now)
	})
}

func (s *Service) CheckIn(ctx context.Context, actor domain.Actor, visitID string, at time.Time, expectedVersion int64) (domain.Visit, error) {
	if actor.Role != domain.RoleProvider {
		return domain.Visit{}, apperr.New(apperr.CodeForbidden, "only provider users can check in")
	}
	return s.change(ctx, actor, visitID, expectedVersion, "check_in", func(_ *sql.Tx, current domain.Visit, now time.Time) (domain.Visit, error) {
		return current.CheckIn(actor.UserID, at, now)
	})
}

func (s *Service) Complete(ctx context.Context, actor domain.Actor, visitID string, evidence map[string]string, at time.Time, expectedVersion int64) (domain.Visit, error) {
	if actor.Role != domain.RoleProvider {
		return domain.Visit{}, apperr.New(apperr.CodeForbidden, "only provider users can complete visits")
	}
	now := s.now.Now()
	current, err := s.repo.VisitByID(ctx, nil, visitID, actor.DistrictID)
	if err != nil {
		return domain.Visit{}, mapVisitError("load visit for completion", err)
	}
	if current.Version != expectedVersion {
		return domain.Visit{}, mapVisitError("complete visit", fmt.Errorf("visit version conflict"))
	}
	updated, err := current.Complete(actor.UserID, evidence, at, now)
	if err != nil {
		return domain.Visit{}, mapVisitError("complete visit", err)
	}
	a, err := s.repo.AuthorizationByID(ctx, nil, current.AuthorizationID, actor.DistrictID)
	if err != nil {
		return domain.Visit{}, mapVisitError("load authorization for completion", err)
	}
	consumed, err := a.Consume(1, now)
	if err != nil {
		return domain.Visit{}, mapVisitError("consume authorization", err)
	}
	if err := s.repo.PersistAuthorizationConsumption(ctx, consumed, a.Version); err != nil {
		return domain.Visit{}, mapVisitError("persist authorization consumption", err)
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.UpdateVisit(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "visit", visitID, "complete", "success", map[string]any{"status": updated.Status}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "visit.complete", "visit", visitID, map[string]any{"status": updated.Status}, now)
	})
	if err != nil {
		return domain.Visit{}, mapVisitError("complete visit", err)
	}
	return updated, nil
}

func (s *Service) change(ctx context.Context, actor domain.Actor, id string, expectedVersion int64, action string, mutate func(*sql.Tx, domain.Visit, time.Time) (domain.Visit, error)) (domain.Visit, error) {
	now := s.now.Now()
	var updated domain.Visit
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.VisitByID(ctx, tx, id, actor.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("visit version conflict")
		}
		updated, err = mutate(tx, current, now)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateVisit(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "visit", id, action, "success", map[string]any{"status": updated.Status}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "visit."+action, "visit", id, map[string]any{"status": updated.Status}, now)
	})
	if err != nil {
		return domain.Visit{}, mapVisitError(action+" visit", err)
	}
	return updated, nil
}

func mapVisitError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.CodeNotFound, "visit or dependency was not found")
	}
	return apperr.Wrap(apperr.CodeConflict, operation, err.Error(), err)
}
