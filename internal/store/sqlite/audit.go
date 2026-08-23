package sqlite

import (
	"context"
	"fmt"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

func (s *Store) AppendAudit(ctx context.Context, q store.DBTX, event domain.AuditEvent) error {
	if q == nil {
		q = s.db
	}
	if event.DistrictID == "" || event.RequestID == "" || event.ObjectType == "" || event.ObjectID == "" {
		return fmt.Errorf("audit event identity is incomplete")
	}
	_, err := q.ExecContext(ctx, `INSERT INTO audit_events(
        district_id, actor_id, request_id, object_type, object_id, action, result, detail_json, created_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.DistrictID, nullString(event.ActorID), event.RequestID,
		event.ObjectType, event.ObjectID, event.Action, event.Result, event.DetailJSON, formatTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func (s *Store) ListAudit(ctx context.Context, districtID, objectType, objectID string, limit, offset int) ([]domain.AuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, district_id, actor_id, request_id,
        object_type, object_id, action, result, detail_json, created_at
        FROM audit_events WHERE district_id = ? AND object_type = ? AND object_id = ?
        ORDER BY id DESC LIMIT ? OFFSET ?`, districtID, objectType, objectID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	result := make([]domain.AuditEvent, 0, limit)
	for rows.Next() {
		var event domain.AuditEvent
		var actorID, createdAt string
		if err := rows.Scan(&event.ID, &event.DistrictID, &actorID, &event.RequestID,
			&event.ObjectType, &event.ObjectID, &event.Action, &event.Result, &event.DetailJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		event.ActorID = actorID
		var err error
		if event.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return result, nil
}
