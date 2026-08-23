package benefit

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
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
)

type Repository interface {
	WithinTx(context.Context, func(*sql.Tx) error) error
	PlanByID(context.Context, store.DBTX, string) (domain.SupportPlan, error)
	CreateAuthorization(context.Context, store.DBTX, domain.Authorization) error
	AuthorizationByID(context.Context, store.DBTX, string, string) (domain.Authorization, error)
	UpdateAuthorization(context.Context, store.DBTX, domain.Authorization, int64) error
	DistrictBudget(context.Context, store.DBTX, string) (storesqlite.DistrictBudget, error)
	ReserveBudget(context.Context, store.DBTX, string, int64, int64, time.Time) error
	ReleaseBudget(context.Context, store.DBTX, string, int64, time.Time) error
	SettleBudget(context.Context, store.DBTX, string, int64, time.Time) error
	VisitByID(context.Context, store.DBTX, string, string) (domain.Visit, error)
	CreateClaim(context.Context, store.DBTX, domain.Claim) error
	ClaimByID(context.Context, store.DBTX, string, string) (domain.Claim, error)
	UpdateClaim(context.Context, store.DBTX, domain.Claim, int64) error
	PersistClaimReview(context.Context, domain.Claim, int64) error
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

func (s *Service) Authorize(ctx context.Context, actor domain.Actor, planID, serviceCode string, units int, unitPrice int64, startsAt, endsAt time.Time) (domain.Authorization, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Authorization{}, apperr.New(apperr.CodeForbidden, "only coordinators can reserve service benefits")
	}
	id, err := s.ids.New("authorization")
	if err != nil {
		return domain.Authorization{}, apperr.Internal("generate authorization id", err)
	}
	now := s.now.Now()
	a, err := domain.NewAuthorization(id, planID, actor.DistrictID, serviceCode, units, unitPrice, startsAt, endsAt, now)
	if err != nil {
		return domain.Authorization{}, apperr.Validation("authorization", err.Error())
	}
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		plan, err := s.repo.PlanByID(ctx, tx, planID)
		if err != nil {
			return err
		}
		if plan.Status != domain.PlanActive {
			return fmt.Errorf("support plan is not active")
		}
		budget, err := s.repo.DistrictBudget(ctx, tx, actor.DistrictID)
		if err != nil {
			return err
		}
		if err := s.repo.ReserveBudget(ctx, tx, actor.DistrictID, a.ReservedCents, budget.Version, now); err != nil {
			return fmt.Errorf("budget reservation failed: %w", err)
		}
		if err := s.repo.CreateAuthorization(ctx, tx, a); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "authorization", id, "reserve", "success", map[string]any{"reserved_cents": a.ReservedCents}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "authorization.reserved", "authorization", id, map[string]any{"plan_id": planID}, now)
	})
	if err != nil {
		return domain.Authorization{}, mapBenefitError("reserve authorization", err)
	}
	return a, nil
}

func (s *Service) Activate(ctx context.Context, actor domain.Actor, id string, expectedVersion int64) (domain.Authorization, error) {
	if actor.Role != domain.RoleCoordinator {
		return domain.Authorization{}, apperr.New(apperr.CodeForbidden, "only coordinators can activate benefits")
	}
	now := s.now.Now()
	var updated domain.Authorization
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.AuthorizationByID(ctx, tx, id, actor.DistrictID)
		if err != nil {
			return err
		}
		plan, err := s.repo.PlanByID(ctx, tx, current.PlanID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("authorization version conflict")
		}
		updated, err = current.Activate(plan, now)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateAuthorization(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actor, "authorization", id, "activate", "success", map[string]any{}, now)
	})
	if err != nil {
		return domain.Authorization{}, mapBenefitError("activate authorization", err)
	}
	return updated, nil
}

func (s *Service) CreateClaim(ctx context.Context, actor domain.Actor, visitID string) (domain.Claim, error) {
	if actor.Role != domain.RoleProvider {
		return domain.Claim{}, apperr.New(apperr.CodeForbidden, "only providers can submit service claims")
	}
	id, err := s.ids.New("claim")
	if err != nil {
		return domain.Claim{}, apperr.Internal("generate claim id", err)
	}
	now := s.now.Now()
	var claim domain.Claim
	err = s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		visit, err := s.repo.VisitByID(ctx, tx, visitID, actor.DistrictID)
		if err != nil {
			return err
		}
		a, err := s.repo.AuthorizationByID(ctx, tx, visit.AuthorizationID, actor.DistrictID)
		if err != nil {
			return err
		}
		claim, err = domain.NewClaim(id, actor.DistrictID, visit, a, now)
		if err != nil {
			return err
		}
		claim, err = claim.Submit(now)
		if err != nil {
			return err
		}
		if err := s.repo.CreateClaim(ctx, tx, claim); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "claim", id, "submit", "success", map[string]any{"visit_id": visitID}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "claim.submitted", "claim", id, map[string]any{"amount_cents": claim.AmountCents}, now)
	})
	if err != nil {
		return domain.Claim{}, mapBenefitError("create claim", err)
	}
	return claim, nil
}

func (s *Service) ReviewClaim(ctx context.Context, actor domain.Actor, claimID string, approve bool, reason string, expectedVersion int64) (domain.Claim, error) {
	if !actor.Role.CanReviewClaims() {
		return domain.Claim{}, apperr.New(apperr.CodeForbidden, "this role cannot review claims")
	}
	now := s.now.Now()
	if approve {
		current, err := s.repo.ClaimByID(ctx, nil, claimID, actor.DistrictID)
		if err != nil { return domain.Claim{}, mapBenefitError("review claim", err) }
		if current.Version != expectedVersion { return domain.Claim{}, apperr.Conflict("claim version conflict") }
		updated, err := current.Approve(actor.UserID, now)
		if err != nil { return domain.Claim{}, mapBenefitError("review claim", err) }
		if err := s.repo.PersistClaimReview(ctx, updated, current.Version); err != nil {
			return domain.Claim{}, mapBenefitError("review claim", err)
		}
		if err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
			return s.audit.Record(ctx, tx, actor, "claim", claimID, "review", "success", map[string]any{"status": updated.Status}, now)
		}); err != nil { return domain.Claim{}, mapBenefitError("review claim", err) }
		return updated, nil
	}
	var updated domain.Claim
	err := s.repo.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repo.ClaimByID(ctx, tx, claimID, actor.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("claim version conflict")
		}
		if approve {
			updated, err = current.Approve(actor.UserID, now)
		} else {
			updated, err = current.Reject(actor.UserID, reason, now)
		}
		if err != nil {
			return err
		}
		if err := s.repo.UpdateClaim(ctx, tx, updated, current.Version); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "claim", claimID, "review", "success", map[string]any{"status": updated.Status}, now); err != nil {
			return err
		}
		return s.events.Enqueue(ctx, tx, actor.DistrictID, "claim.reviewed", "claim", claimID, map[string]any{"status": updated.Status}, now)
	})
	if err != nil {
		return domain.Claim{}, mapBenefitError("review claim", err)
	}
	return updated, nil
}

func mapBenefitError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.CodeConflict, "a related record was missing or changed")
	}
	return apperr.Wrap(apperr.CodeConflict, operation, err.Error(), err)
}
