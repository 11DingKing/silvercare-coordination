package escalation

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
	ResidentByID(context.Context, store.DBTX, string, string) (domain.Resident, error)
	VisitByID(context.Context, store.DBTX, string, string) (domain.Visit, error)
	CreateEscalation(context.Context, store.DBTX, domain.Escalation) error
	EscalationByID(context.Context, store.DBTX, string, string) (domain.Escalation, error)
	UpdateEscalation(context.Context, store.DBTX, domain.Escalation, int64) error
	PersistEscalationResolution(context.Context, domain.Escalation, int64) error
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

func (s *Service) Open(ctx context.Context, actor domain.Actor, residentID, visitID string, severity domain.Severity, summary string) (domain.Escalation, error) {
	if actor.Role != domain.RoleProvider && actor.Role != domain.RoleCoordinator {
		return domain.Escalation{}, apperr.New(apperr.CodeForbidden, "this role cannot open an assistance escalation")
	}
	id, err := s.ids.New("escalation")
	if err != nil {
		return domain.Escalation{}, apperr.Internal("generate escalation id", err)
	}
	now := s.now.Now()
	entity, err := domain.NewEscalation(id, actor.DistrictID, residentID, visitID, severity, summary, now)
	if err != nil {
		return domain.Escalation{}, apperr.Validation("escalation", err.Error())
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.repo.ResidentByID(ctx, tx, residentID, actor.DistrictID); err != nil {
			return err
		}
		if visitID != "" {
			visit, err := s.repo.VisitByID(ctx, tx, visitID, actor.DistrictID)
			if err != nil {
				return err
			}
			if visit.ResidentID != residentID {
				return fmt.Errorf("visit belongs to another resident")
			}
		}
		if err := s.repo.CreateEscalation(ctx, tx, entity); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "escalation", id, "open", "success", map[string]any{"severity": severity}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "escalation.opened", "escalation", id, map[string]any{"severity": severity, "due_at": entity.AcknowledgementDueAt}, now)
	})
	if err != nil {
		return domain.Escalation{}, mapEscalationError("open escalation", err)
	}
	return entity, nil
}

func (s *Service) Acknowledge(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.Escalation, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Escalation{}, apperr.New(apperr.CodeForbidden, "only coordinators can acknowledge escalations")
	}
	return s.change(ctx, actor, id, expectedVersion, "acknowledge", func(current domain.Escalation) (domain.Escalation, error) {
		return current.Acknowledge(actor.UserID, s.now.Now())
	})
}

func (s *Service) Resolve(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.Escalation, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Escalation{}, apperr.New(apperr.CodeForbidden, "only coordinators can resolve escalations")
	}
	now:=s.now.Now(); current,err:=s.repo.EscalationByID(ctx,nil,id,actor.DistrictID); if err!=nil{return domain.Escalation{},mapEscalationError("resolve escalation",err)}; if current.Version!=expectedVersion{return domain.Escalation{},apperr.Conflict("escalation version conflict")}; updated,err:=current.Resolve(actor.UserID,now); if err!=nil{return domain.Escalation{},mapEscalationError("resolve escalation",err)}; if err:=s.repo.PersistEscalationResolution(ctx,updated,current.Version);err!=nil{return domain.Escalation{},mapEscalationError("resolve escalation",err)}; if err:=s.repo.WithinTx(ctx,func(tx *sql.Tx)error{return s.audit.Record(ctx,tx,actor,"escalation",id,"resolve","success",map[string]any{"status":updated.Status},now)});err!=nil{return domain.Escalation{},mapEscalationError("resolve escalation",err)}; return updated,nil
}

func (s *Service) change(ctx context.Context, actor domain.Actor, id string, expectedVersion int64, action string, mutate func(domain.Escalation) (domain.Escalation, error)) (domain.Escalation, error) {
	now := s.now.Now()
	var updated domain.Escalation
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.EscalationByID(ctx, tx, id, actor.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("escalation version conflict")
		}
		updated, err = mutate(current)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateEscalation(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "escalation", id, action, "success", map[string]any{"status": updated.Status}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "escalation."+action, "escalation", id, map[string]any{"status": updated.Status}, now)
	})
	if err != nil {
		return domain.Escalation{}, mapEscalationError(action+" escalation", err)
	}
	return updated, nil
}

func mapEscalationError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.CodeNotFound, "escalation or dependency was not found")
	}
	return apperr.Wrap(apperr.CodeConflict, operation, err.Error(), err)
}
