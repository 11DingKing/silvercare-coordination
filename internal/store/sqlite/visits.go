package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

func (s *Store) CreateVisit(ctx context.Context, q store.DBTX, visit domain.Visit) error {
	if q == nil {
		q = s.db
	}
	evidence, err := visit.EvidenceJSON()
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO visits(
        id, authorization_id, provider_id, assigned_user_id, resident_id, status,
        scheduled_start, scheduled_end, checked_in_at, completed_at, evidence_json,
        version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, visit.ID, visit.AuthorizationID,
		visit.ProviderID, nullString(visit.AssignedUserID), visit.ResidentID, string(visit.Status),
		formatTime(visit.ScheduledStart), formatTime(visit.ScheduledEnd), formatNullableTime(visit.CheckedInAt),
		formatNullableTime(visit.CompletedAt), nullString(evidence), visit.Version,
		formatTime(visit.CreatedAt), formatTime(visit.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert visit: %w", err)
	}
	return nil
}

func (s *Store) VisitByID(ctx context.Context, q store.DBTX, id, districtID string) (domain.Visit, error) {
	if q == nil {
		q = s.db
	}
	return scanVisit(q.QueryRowContext(ctx, `SELECT v.id, v.authorization_id, v.provider_id,
        v.assigned_user_id, v.resident_id, v.status, v.scheduled_start, v.scheduled_end,
        v.checked_in_at, v.completed_at, v.evidence_json, v.version, v.created_at, v.updated_at
        FROM visits v JOIN residents r ON r.id = v.resident_id
        WHERE v.id = ? AND r.district_id = ?`, id, districtID))
}

func (s *Store) UpdateVisit(ctx context.Context, q store.DBTX, visit domain.Visit, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	evidence, err := visit.EvidenceJSON()
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `UPDATE visits SET assigned_user_id = ?, status = ?,
        checked_in_at = ?, completed_at = ?, evidence_json = ?, version = ?, updated_at = ?
        WHERE id = ? AND version = ?`, nullString(visit.AssignedUserID), string(visit.Status),
		formatNullableTime(visit.CheckedInAt), formatNullableTime(visit.CompletedAt), nullString(evidence),
		visit.Version, formatTime(visit.UpdatedAt), visit.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update visit: %w", err)
	}
	return requireOne(result, "visit version conflict")
}

func (s *Store) CountProviderWindowConflicts(ctx context.Context, q store.DBTX, providerID, exceptID string, start, end time.Time) (int, error) {
	if q == nil {
		q = s.db
	}
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM visits WHERE provider_id = ? AND id != ?
        AND status NOT IN ('cancelled','disputed') AND scheduled_start < ? AND scheduled_end > ?`,
		providerID, exceptID, formatTime(end), formatTime(start)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count visit window conflicts: %w", err)
	}
	return count, nil
}

func (s *Store) CountResidentWindowConflicts(ctx context.Context, q store.DBTX, residentID, exceptID string, start, end time.Time) (int, error) {
	if q == nil {
		q = s.db
	}
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM visits WHERE resident_id = ? AND id != ?
        AND status NOT IN ('cancelled','disputed') AND scheduled_start < ? AND scheduled_end > ?`,
		residentID, exceptID, formatTime(end), formatTime(start)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count resident visit conflicts: %w", err)
	}
	return count, nil
}

func scanVisit(row *sql.Row) (domain.Visit, error) {
	var visit domain.Visit
	var assigned, checkedIn, completed, evidence sql.NullString
	var status, scheduledStart, scheduledEnd, createdAt, updatedAt string
	if err := row.Scan(&visit.ID, &visit.AuthorizationID, &visit.ProviderID, &assigned,
		&visit.ResidentID, &status, &scheduledStart, &scheduledEnd, &checkedIn, &completed,
		&evidence, &visit.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Visit{}, sql.ErrNoRows
		}
		return domain.Visit{}, fmt.Errorf("scan visit: %w", err)
	}
	visit.AssignedUserID = assigned.String
	visit.Status = domain.VisitStatus(status)
	visit.Evidence = map[string]string{}
	if evidence.Valid {
		if err := decodeJSON(evidence.String, &visit.Evidence); err != nil {
			return domain.Visit{}, err
		}
	}
	var err error
	if visit.ScheduledStart, err = parseTime(scheduledStart); err != nil {
		return domain.Visit{}, err
	}
	if visit.ScheduledEnd, err = parseTime(scheduledEnd); err != nil {
		return domain.Visit{}, err
	}
	if visit.CheckedInAt, err = nullableTime(checkedIn); err != nil {
		return domain.Visit{}, err
	}
	if visit.CompletedAt, err = nullableTime(completed); err != nil {
		return domain.Visit{}, err
	}
	if visit.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Visit{}, err
	}
	if visit.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Visit{}, err
	}
	return visit, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
