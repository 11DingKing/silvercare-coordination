package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

func (s *Store) CreatePlan(ctx context.Context, q store.DBTX, plan domain.SupportPlan) error {
	if q == nil {
		q = s.db
	}
	goals, err := plan.GoalsJSON()
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO support_plans(
        id, resident_id, assessment_id, coordinator_id, status, starts_at, ends_at,
        goals_json, version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, plan.ID, plan.ResidentID, plan.AssessmentID,
		plan.CoordinatorID, string(plan.Status), formatTime(plan.StartsAt), formatTime(plan.EndsAt), goals,
		plan.Version, formatTime(plan.CreatedAt), formatTime(plan.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert support plan: %w", err)
	}
	return nil
}

func (s *Store) PlanByID(ctx context.Context, q store.DBTX, id string) (domain.SupportPlan, error) {
	if q == nil {
		q = s.db
	}
	return scanPlan(q.QueryRowContext(ctx, `SELECT id, resident_id, assessment_id, coordinator_id,
        status, starts_at, ends_at, goals_json, version, created_at, updated_at
        FROM support_plans WHERE id = ?`, id))
}

func (s *Store) UpdatePlan(ctx context.Context, q store.DBTX, plan domain.SupportPlan, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	goals, err := plan.GoalsJSON()
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `UPDATE support_plans SET status = ?, starts_at = ?,
        ends_at = ?, goals_json = ?, version = ?, updated_at = ? WHERE id = ? AND version = ?`,
		string(plan.Status), formatTime(plan.StartsAt), formatTime(plan.EndsAt), goals,
		plan.Version, formatTime(plan.UpdatedAt), plan.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update support plan: %w", err)
	}
	return requireOne(result, "support plan version conflict")
}

func (s *Store) CountOpenPlansForResident(ctx context.Context, q store.DBTX, residentID, exceptID string) (int, error) {
	if q == nil {
		q = s.db
	}
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM support_plans
        WHERE resident_id = ? AND id != ? AND status IN ('review','active','suspended')`, residentID, exceptID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count open resident plans: %w", err)
	}
	return count, nil
}

func scanPlan(row *sql.Row) (domain.SupportPlan, error) {
	var plan domain.SupportPlan
	var status, startsAt, endsAt, goals, createdAt, updatedAt string
	if err := row.Scan(&plan.ID, &plan.ResidentID, &plan.AssessmentID, &plan.CoordinatorID,
		&status, &startsAt, &endsAt, &goals, &plan.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.SupportPlan{}, sql.ErrNoRows
		}
		return domain.SupportPlan{}, fmt.Errorf("scan support plan: %w", err)
	}
	plan.Status = domain.PlanStatus(status)
	if err := decodeJSON(goals, &plan.Goals); err != nil {
		return domain.SupportPlan{}, err
	}
	var err error
	if plan.StartsAt, err = parseTime(startsAt); err != nil {
		return domain.SupportPlan{}, err
	}
	if plan.EndsAt, err = parseTime(endsAt); err != nil {
		return domain.SupportPlan{}, err
	}
	if plan.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.SupportPlan{}, err
	}
	if plan.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.SupportPlan{}, err
	}
	return plan, nil
}
