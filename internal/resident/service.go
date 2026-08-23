package resident

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/apperr"
	"github.com/11DingKing/silvercare-coordination/internal/audit"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/outbox"
	"github.com/11DingKing/silvercare-coordination/internal/store"
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
)

type Repository interface {
	WithinTx(context.Context, func(*sql.Tx) error) error
	CreateResident(context.Context, store.DBTX, domain.Resident) error
	ResidentByID(context.Context, store.DBTX, string, string) (domain.Resident, error)
	UpdateResident(context.Context, store.DBTX, domain.Resident, int64) error
	PersistResidentWithdrawal(context.Context, domain.Resident, int64) error
	ListResidents(context.Context, storesqlite.ResidentFilter) ([]domain.Resident, int, error)
}

type Service struct {
	repo   Repository
	ids    idgen.Generator
	now    clock.Clock
	audit  *audit.Recorder
	events *outbox.Publisher
}

type CreateInput struct {
	ExternalRef string
	FullName    string
	HouseholdID string
	BirthDate   time.Time
}

func NewService(repo Repository, ids idgen.Generator, now clock.Clock, recorder *audit.Recorder, events *outbox.Publisher) *Service {
	return &Service{repo: repo, ids: ids, now: now, audit: recorder, events: events}
}

func (s *Service) Create(ctx context.Context, actor domain.Actor, input CreateInput) (domain.Resident, error) {
	if !actor.Role.CanManageResidents() {
		return domain.Resident{}, apperr.New(apperr.CodeForbidden, "only coordinators can enroll residents")
	}
	id, err := s.ids.New("resident")
	if err != nil {
		return domain.Resident{}, apperr.Internal("generate resident id", err)
	}
	now := s.now.Now()
	entity, err := domain.NewResident(id, actor.DistrictID, input.ExternalRef, input.FullName, input.HouseholdID, input.BirthDate, now)
	if err != nil {
		return domain.Resident{}, apperr.Validation("resident", err.Error())
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.CreateResident(ctx, tx, entity); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "resident", entity.ID, "enroll", "success", map[string]any{"external_ref": entity.ExternalRef}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "resident.enrolled", "resident", entity.ID, map[string]any{"resident_id": entity.ID}, now)
	})
	if err != nil {
		return domain.Resident{}, mapWriteError("enroll resident", err)
	}
	return entity, nil
}

func (s *Service) GrantConsent(ctx context.Context, actor domain.Actor, residentID string, until time.Time, expectedVersion int64) (domain.Resident, error) {
	if !actor.Role.CanManageResidents() {
		return domain.Resident{}, apperr.New(apperr.CodeForbidden, "only coordinators can record consent")
	}
	now := s.now.Now()
	var updated domain.Resident
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.ResidentByID(ctx, tx, residentID, actor.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("resident version conflict")
		}
		updated, err = current.GrantConsent(until, now)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateResident(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "resident", residentID, "grant_consent", "success", map[string]any{"expires_at": until}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "resident.consent_granted", "resident", residentID, map[string]any{"expires_at": until}, now)
	})
	if err != nil {
		return domain.Resident{}, mapWriteError("grant resident consent", err)
	}
	return updated, nil
}

func (s *Service) WithdrawConsent(ctx context.Context, actor domain.Actor, residentID string, expectedVersion int64) (domain.Resident, error) {
	if !actor.Role.CanManageResidents() {
		return domain.Resident{}, apperr.New(apperr.CodeForbidden, "only coordinators can withdraw consent")
	}
	now := s.now.Now()
	var updated domain.Resident
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.ResidentByID(ctx, tx, residentID, actor.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("resident version conflict")
		}
		updated = current.WithdrawConsent(now)
		if err := s.repo.PersistResidentWithdrawal(ctx, updated, current.Version); err != nil { return err }
		if err := s.audit.Record(ctx, tx, actor, "resident", residentID, "withdraw_consent", "success", map[string]any{}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "resident.consent_withdrawn", "resident", residentID, map[string]any{"resident_id": residentID}, now)
	})
	if err != nil {
		return domain.Resident{}, mapWriteError("withdraw resident consent", err)
	}
	return updated, nil
}

func (s *Service) Get(ctx context.Context, actor domain.Actor, id string) (domain.Resident, error) {
	entity, err := s.repo.ResidentByID(ctx, nil, id, actor.DistrictID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Resident{}, apperr.NotFound("resident", id)
		}
		return domain.Resident{}, apperr.Internal("load resident", err)
	}
	return entity, nil
}

func (s *Service) List(ctx context.Context, actor domain.Actor, filter storesqlite.ResidentFilter) ([]domain.Resident, int, error) {
	filter.DistrictID = actor.DistrictID
	items, total, err := s.repo.ListResidents(ctx, filter)
	if err != nil {
		return nil, 0, apperr.Internal("list residents", err)
	}
	return items, total, nil
}

func mapWriteError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.CodeConflict, "the record changed or no longer exists")
	}
	message := err.Error()
	if containsAny(message, "UNIQUE constraint", "version conflict", "cannot", "requires", "invalid", "outside") {
		return apperr.Wrap(apperr.CodeConflict, operation, message, err)
	}
	return apperr.Internal(operation, err)
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
