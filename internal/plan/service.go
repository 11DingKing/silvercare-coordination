package plan

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
	ResidentByID(context.Context, store.DBTX, string, string) (domain.Resident, error)
	AssessmentByID(context.Context, store.DBTX, string) (domain.Assessment, error)
	CreatePlan(context.Context, store.DBTX, domain.SupportPlan) error
	PlanByID(context.Context, store.DBTX, string) (domain.SupportPlan, error)
	UpdatePlan(context.Context, store.DBTX, domain.SupportPlan, int64) error
	PersistPlanActivation(context.Context, domain.SupportPlan, int64) error
	CountOpenPlansForResident(context.Context, store.DBTX, string, string) (int, error)
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

func (s *Service) Create(ctx context.Context, actor domain.Actor, residentID, assessmentID string, startsAt, endsAt time.Time) (domain.SupportPlan, error) {
	if !actor.Role.CanManageResidents() {
		return domain.SupportPlan{}, apperr.New(apperr.CodeForbidden, "only coordinators can create support plans")
	}
	id, err := s.ids.New("plan")
	if err != nil {
		return domain.SupportPlan{}, apperr.Internal("generate support plan id", err)
	}
	now := s.now.Now()
	entity, err := domain.NewSupportPlan(id, residentID, assessmentID, actor.UserID, startsAt, endsAt, now)
	if err != nil {
		return domain.SupportPlan{}, apperr.Validation("support_plan", err.Error())
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		resident, err := s.repo.ResidentByID(ctx, tx, residentID, actor.DistrictID)
		if err != nil {
			return err
		}
		assessment, err := s.repo.AssessmentByID(ctx, tx, assessmentID)
		if err != nil {
			return err
		}
		if assessment.ResidentID != resident.ID {
			return fmt.Errorf("assessment belongs to another resident")
		}
		if err := s.repo.CreatePlan(ctx, tx, entity); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actor, "support_plan", id, "create", "success", map[string]any{"resident_id": residentID}, now)
	})
	if err != nil {
		return domain.SupportPlan{}, mapPlanError("create support plan", err)
	}
	return entity, nil
}

func (s *Service) AddGoal(ctx context.Context, actor domain.Actor, id, goal string, expectedVersion int64) (domain.SupportPlan, error) {
	return s.change(ctx, actor, id, expectedVersion, "add_goal", func(current domain.SupportPlan, _ domain.Resident, _ domain.Assessment, now time.Time) (domain.SupportPlan, error) {
		return current.AddGoal(goal, now)
	})
}

func (s *Service) Submit(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.SupportPlan, error) {
	return s.change(ctx, actor, id, expectedVersion, "submit", func(current domain.SupportPlan, _ domain.Resident, _ domain.Assessment, now time.Time) (domain.SupportPlan, error) {
		return current.SubmitForReview(now)
	})
}

func (s *Service) Activate(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.SupportPlan, error) {
	if !actor.Role.CanManageResidents() {
		return domain.SupportPlan{}, apperr.New(apperr.CodeForbidden, "only coordinators can change support plans")
	}
	now := s.now.Now()
	current, err := s.repo.PlanByID(ctx, nil, id)
	if err != nil {
		return domain.SupportPlan{}, mapPlanError("load support plan", err)
	}
	resident, err := s.repo.ResidentByID(ctx, nil, current.ResidentID, actor.DistrictID)
	if err != nil {
		return domain.SupportPlan{}, mapPlanError("load resident for support plan", err)
	}
	assessment, err := s.repo.AssessmentByID(ctx, nil, current.AssessmentID)
	if err != nil {
		return domain.SupportPlan{}, mapPlanError("load assessment for support plan", err)
	}
	if current.Version != expectedVersion {
		return domain.SupportPlan{}, mapPlanError("activate support plan", fmt.Errorf("support plan version conflict"))
	}
	count, err := s.repo.CountOpenPlansForResident(ctx, nil, current.ResidentID, current.ID)
	if err != nil {
		return domain.SupportPlan{}, mapPlanError("count support plans", err)
	}
	if count > 0 {
		return domain.SupportPlan{}, mapPlanError("activate support plan", fmt.Errorf("resident already has another open support plan"))
	}
	updated, err := current.Activate(assessment, resident, now)
	if err != nil {
		return domain.SupportPlan{}, mapPlanError("activate support plan", err)
	}
	if err := s.repo.PersistPlanActivation(ctx, updated, current.Version); err != nil {
		return domain.SupportPlan{}, mapPlanError("persist support plan activation", err)
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if err := s.audit.Record(ctx, tx, actor, "support_plan", id, "activate", "success", map[string]any{"status": updated.Status}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "support_plan.activated", "support_plan", id, map[string]any{"resident_id": current.ResidentID}, now)
	})
	if err != nil {
		return domain.SupportPlan{}, mapPlanError("activate support plan", err)
	}
	return updated, nil
}

func (s *Service) change(ctx context.Context, actor domain.Actor, id string, expectedVersion int64, action string, mutate func(domain.SupportPlan, domain.Resident, domain.Assessment, time.Time) (domain.SupportPlan, error)) (domain.SupportPlan, error) {
	if !actor.Role.CanManageResidents() {
		return domain.SupportPlan{}, apperr.New(apperr.CodeForbidden, "only coordinators can change support plans")
	}
	now := s.now.Now()
	var updated domain.SupportPlan
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.PlanByID(ctx, tx, id)
		if err != nil {
			return err
		}
		resident, err := s.repo.ResidentByID(ctx, tx, current.ResidentID, actor.DistrictID)
		if err != nil {
			return err
		}
		assessment, err := s.repo.AssessmentByID(ctx, tx, current.AssessmentID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("support plan version conflict")
		}
		if action == "activate" {
			count, err := s.repo.CountOpenPlansForResident(ctx, tx, current.ResidentID, current.ID)
			if err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("resident already has another open support plan")
			}
		}
		updated, err = mutate(current, resident, assessment, now)
		if err != nil {
			return err
		}
		if err := s.repo.UpdatePlan(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "support_plan", id, action, "success", map[string]any{"status": updated.Status}, now); err != nil {
			return err
		}
		if action == "activate" {
			return s.events.Enqueue(ctx, tx, actor.DistrictID, "support_plan.activated", "support_plan", id, map[string]any{"resident_id": current.ResidentID}, now)
		}
		return nil
	})
	if err != nil {
		return domain.SupportPlan{}, mapPlanError(action+" support plan", err)
	}
	return updated, nil
}

func mapPlanError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.CodeNotFound, "support plan dependency was not found")
	}
	return apperr.Wrap(apperr.CodeConflict, operation, err.Error(), err)
}
