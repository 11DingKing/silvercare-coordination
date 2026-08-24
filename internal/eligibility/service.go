package eligibility

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
	CreateAssessment(context.Context, store.DBTX, domain.Assessment) error
	AssessmentByID(context.Context, store.DBTX, string) (domain.Assessment, error)
	UpdateAssessment(context.Context, store.DBTX, domain.Assessment, int64) error
	SupersedeOtherAssessments(context.Context, store.DBTX, string, string, string) (int64, error)
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

func (s *Service) Start(ctx context.Context, actor domain.Actor, residentID string, supportLevel int, validUntil time.Time) (domain.Assessment, error) {
	if !actor.Role.CanManageResidents() {
		return domain.Assessment{}, apperr.New(apperr.CodeForbidden, "only coordinators can assess eligibility")
	}
	resident, err := s.repo.ResidentByID(ctx, nil, residentID, actor.DistrictID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Assessment{}, apperr.NotFound("resident", residentID)
		}
		return domain.Assessment{}, apperr.Internal("load assessment resident", err)
	}
	if !resident.HasActiveConsent(s.now.Now()) {
		return domain.Assessment{}, apperr.Conflict("resident consent must be active before assessment")
	}
	id, err := s.ids.New("assessment")
	if err != nil {
		return domain.Assessment{}, apperr.Internal("generate assessment id", err)
	}
	now := s.now.Now()
	assessment, err := domain.NewAssessment(id, residentID, actor.UserID, supportLevel, validUntil, now)
	if err != nil {
		return domain.Assessment{}, apperr.Validation("assessment", err.Error())
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.CreateAssessment(ctx, tx, assessment); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actor, "assessment", id, "start", "success", map[string]any{"resident_id": residentID}, now)
	})
	if err != nil {
		return domain.Assessment{}, apperr.Internal("start assessment", err)
	}
	return assessment, nil
}

func (s *Service) AddEvidence(ctx context.Context, actor domain.Actor, id, key, value string, expectedVersion int64) (domain.Assessment, error) {
	return s.change(ctx, actor, id, expectedVersion, "add_evidence", func(current domain.Assessment, now time.Time) (domain.Assessment, error) {
		return current.PutEvidence(key, value, now)
	})
}

func (s *Service) Submit(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.Assessment, error) {
	return s.change(ctx, actor, id, expectedVersion, "submit", func(current domain.Assessment, now time.Time) (domain.Assessment, error) {
		return current.Submit(now)
	})
}

func (s *Service) Approve(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.Assessment, error) {
	if !actor.Role.CanManageResidents() {
		return domain.Assessment{}, apperr.New(apperr.CodeForbidden, "only coordinators can approve assessments")
	}
	now := s.now.Now()
	var updated domain.Assessment
	current, err := s.repo.AssessmentByID(ctx, nil, id)
	if err != nil {
		return domain.Assessment{}, mapError("load assessment for approval", err)
	}
	resident, err := s.repo.ResidentByID(ctx, nil, current.ResidentID, actor.DistrictID)
	if err != nil {
		return domain.Assessment{}, mapError("load resident for approval", err)
	}
	if !resident.HasActiveConsent(now) {
		return domain.Assessment{}, mapError("approve assessment", fmt.Errorf("resident consent expired before approval"))
	}
	if current.Version != expectedVersion {
		return domain.Assessment{}, mapError("approve assessment", fmt.Errorf("assessment version conflict"))
	}
	updated, err = current.Approve(now)
	if err != nil {
		return domain.Assessment{}, mapError("approve assessment", err)
	}
	if err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.UpdateAssessment(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if _, err := s.repo.SupersedeOtherAssessments(ctx, tx, current.ResidentID, current.ID, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "assessment", id, "approve", "success", map[string]any{"resident_id": current.ResidentID}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "assessment.approved", "assessment", id, map[string]any{"resident_id": current.ResidentID}, now)
	}); err != nil {
		return domain.Assessment{}, mapError("approve assessment", err)
	}
	return updated, nil
}

func (s *Service) change(ctx context.Context, actor domain.Actor, id string, expectedVersion int64, action string, mutate func(domain.Assessment, time.Time) (domain.Assessment, error)) (domain.Assessment, error) {
	if !actor.Role.CanManageResidents() {
		return domain.Assessment{}, apperr.New(apperr.CodeForbidden, "only coordinators can change assessments")
	}
	now := s.now.Now()
	var updated domain.Assessment
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.AssessmentByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := s.repo.ResidentByID(ctx, tx, current.ResidentID, actor.DistrictID); err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("assessment version conflict")
		}
		updated, err = mutate(current, now)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateAssessment(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actor, "assessment", id, action, "success", map[string]any{"status": updated.Status}, now)
	})
	if err != nil {
		return domain.Assessment{}, mapError(action+" assessment", err)
	}
	return updated, nil
}

func mapError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.CodeNotFound, "assessment or resident was not found")
	}
	return apperr.Wrap(apperr.CodeConflict, operation, err.Error(), err)
}
