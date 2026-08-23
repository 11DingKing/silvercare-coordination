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

func (s *Store) CreateEscalation(ctx context.Context, q store.DBTX, escalation domain.Escalation) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO escalations(
        id, district_id, resident_id, visit_id, severity, status, summary,
        acknowledgement_due_at, acknowledged_by, acknowledged_at, resolved_at,
        version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, escalation.ID, escalation.DistrictID,
		escalation.ResidentID, nullString(escalation.VisitID), string(escalation.Severity), string(escalation.Status),
		escalation.Summary, formatTime(escalation.AcknowledgementDueAt), nullString(escalation.AcknowledgedBy),
		formatNullableTime(escalation.AcknowledgedAt), formatNullableTime(escalation.ResolvedAt),
		escalation.Version, formatTime(escalation.CreatedAt), formatTime(escalation.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert escalation: %w", err)
	}
	return nil
}

func (s *Store) EscalationByID(ctx context.Context, q store.DBTX, id, districtID string) (domain.Escalation, error) {
	if q == nil {
		q = s.db
	}
	return scanEscalation(q.QueryRowContext(ctx, `SELECT id, district_id, resident_id,
        visit_id, severity, status, summary, acknowledgement_due_at, acknowledged_by,
        acknowledged_at, resolved_at, version, created_at, updated_at
        FROM escalations WHERE id = ? AND district_id = ?`, id, districtID))
}

func (s *Store) UpdateEscalation(ctx context.Context, q store.DBTX, escalation domain.Escalation, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE escalations SET status = ?, acknowledged_by = ?,
        acknowledged_at = ?, resolved_at = ?, version = ?, updated_at = ?
        WHERE id = ? AND district_id = ? AND version = ?`, string(escalation.Status),
		nullString(escalation.AcknowledgedBy), formatNullableTime(escalation.AcknowledgedAt),
		formatNullableTime(escalation.ResolvedAt), escalation.Version, formatTime(escalation.UpdatedAt),
		escalation.ID, escalation.DistrictID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update escalation: %w", err)
	}
	return requireOne(result, "escalation version conflict")
}

func (s *Store) PersistEscalationExpiry(ctx context.Context, escalation domain.Escalation, expectedVersion int64) error {
	return s.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.EscalationByID(ctx, tx, escalation.ID, escalation.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion || current.Status != domain.EscalationOpen {
			return fmt.Errorf("escalation version conflict")
		}
		return s.UpdateEscalation(ctx, tx, escalation, expectedVersion)
	})
}

func (s *Store) OverdueOpenEscalations(ctx context.Context, q store.DBTX, now time.Time, limit int) ([]domain.Escalation, error) {
	if q == nil {
		q = s.db
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.QueryContext(ctx, `SELECT id, district_id, resident_id, visit_id,
        severity, status, summary, acknowledgement_due_at, acknowledged_by,
        acknowledged_at, resolved_at, version, created_at, updated_at
        FROM escalations WHERE status = 'open' AND acknowledgement_due_at <= ?
        ORDER BY acknowledgement_due_at ASC LIMIT ?`, formatTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("query overdue escalations: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Escalation, 0, limit)
	for rows.Next() {
		e, err := scanEscalationValue(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate overdue escalations: %w", err)
	}
	return result, nil
}

func scanEscalation(row *sql.Row) (domain.Escalation, error) { return scanEscalationValue(row) }

func scanEscalationValue(row rowScanner) (domain.Escalation, error) {
	var e domain.Escalation
	var visit, acknowledgedBy, acknowledgedAt, resolvedAt sql.NullString
	var severity, status, dueAt, createdAt, updatedAt string
	if err := row.Scan(&e.ID, &e.DistrictID, &e.ResidentID, &visit, &severity, &status,
		&e.Summary, &dueAt, &acknowledgedBy, &acknowledgedAt, &resolvedAt,
		&e.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Escalation{}, sql.ErrNoRows
		}
		return domain.Escalation{}, fmt.Errorf("scan escalation: %w", err)
	}
	e.VisitID = visit.String
	e.Severity = domain.Severity(severity)
	e.Status = domain.EscalationStatus(status)
	e.AcknowledgedBy = acknowledgedBy.String
	var err error
	if e.AcknowledgementDueAt, err = parseTime(dueAt); err != nil {
		return domain.Escalation{}, err
	}
	if e.AcknowledgedAt, err = nullableTime(acknowledgedAt); err != nil {
		return domain.Escalation{}, err
	}
	if e.ResolvedAt, err = nullableTime(resolvedAt); err != nil {
		return domain.Escalation{}, err
	}
	if e.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Escalation{}, err
	}
	if e.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Escalation{}, err
	}
	return e, nil
}
