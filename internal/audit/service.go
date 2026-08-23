package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/apperr"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

type Repository interface {
	AppendAudit(context.Context, store.DBTX, domain.AuditEvent) error
	ListAudit(context.Context, string, string, string, int, int) ([]domain.AuditEvent, error)
}

type Recorder struct{ repo Repository }

func NewRecorder(repo Repository) *Recorder { return &Recorder{repo: repo} }

func (r *Recorder) Record(ctx context.Context, q store.DBTX, actor domain.Actor, objectType, objectID, action, result string, detail any, now time.Time) error {
	if err := actor.Validate(); err != nil {
		return fmt.Errorf("audit actor: %w", err)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode audit detail: %w", err)
	}
	return r.repo.AppendAudit(ctx, q, domain.AuditEvent{
		DistrictID: actor.DistrictID, ActorID: actor.UserID, RequestID: actor.RequestID,
		ObjectType: objectType, ObjectID: objectID, Action: action, Result: result,
		DetailJSON: string(raw), CreatedAt: now.UTC(),
	})
}

type Service struct{ repo Repository }

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) List(ctx context.Context, actor domain.Actor, objectType, objectID string, limit, offset int) ([]domain.AuditEvent, error) {
	if !actor.Role.CanReadAudit() {
		return nil, apperr.New(apperr.CodeForbidden, "only auditors can inspect immutable audit history")
	}
	if objectType == "" || objectID == "" {
		return nil, apperr.Validation("object", "type and id are required")
	}
	events, err := s.repo.ListAudit(ctx, actor.DistrictID, objectType, objectID, limit, offset)
	if err != nil {
		if err == sql.ErrNoRows {
			return []domain.AuditEvent{}, nil
		}
		return nil, apperr.Internal("list audit history", err)
	}
	return events, nil
}
