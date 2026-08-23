package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/store"
)

type DistrictBudget struct {
	ID            string
	Name          string
	Timezone      string
	MonthlyCents  int64
	ReservedCents int64
	SettledCents  int64
	Version       int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (s *Store) EnsureDistrict(ctx context.Context, q store.DBTX, district DistrictBudget) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO districts(
        id, name, timezone, monthly_budget_cents, reserved_budget_cents, settled_budget_cents,
        version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO NOTHING`, district.ID, district.Name, district.Timezone,
		district.MonthlyCents, district.ReservedCents, district.SettledCents, district.Version,
		formatTime(district.CreatedAt), formatTime(district.UpdatedAt))
	if err != nil {
		return fmt.Errorf("ensure district: %w", err)
	}
	return nil
}

func (s *Store) DistrictBudget(ctx context.Context, q store.DBTX, districtID string) (DistrictBudget, error) {
	if q == nil {
		q = s.db
	}
	var budget DistrictBudget
	var createdAt, updatedAt string
	err := q.QueryRowContext(ctx, `SELECT id, name, timezone, monthly_budget_cents,
        reserved_budget_cents, settled_budget_cents, version, created_at, updated_at
        FROM districts WHERE id = ?`, districtID).Scan(
		&budget.ID, &budget.Name, &budget.Timezone, &budget.MonthlyCents,
		&budget.ReservedCents, &budget.SettledCents, &budget.Version, &createdAt, &updatedAt)
	if err != nil {
		return DistrictBudget{}, err
	}
	if budget.CreatedAt, err = parseTime(createdAt); err != nil {
		return DistrictBudget{}, err
	}
	if budget.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return DistrictBudget{}, err
	}
	return budget, nil
}

func (s *Store) ReserveBudget(ctx context.Context, q store.DBTX, districtID string, cents, expectedVersion int64, now time.Time) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE districts
        SET reserved_budget_cents = reserved_budget_cents + ?, version = version + 1, updated_at = ?
        WHERE id = ? AND version = ?
          AND reserved_budget_cents + settled_budget_cents + ? <= monthly_budget_cents`,
		cents, formatTime(now), districtID, expectedVersion, cents)
	if err != nil {
		return fmt.Errorf("reserve district budget: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read budget reserve count: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ReleaseBudget(ctx context.Context, q store.DBTX, districtID string, cents int64, now time.Time) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE districts SET
        reserved_budget_cents = reserved_budget_cents - ?, version = version + 1, updated_at = ?
        WHERE id = ? AND reserved_budget_cents >= ?`, cents, formatTime(now), districtID, cents)
	if err != nil {
		return fmt.Errorf("release district budget: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read budget release count: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SettleBudget(ctx context.Context, q store.DBTX, districtID string, cents int64, now time.Time) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE districts SET
        reserved_budget_cents = reserved_budget_cents - ?,
        settled_budget_cents = settled_budget_cents + ?,
        version = version + 1, updated_at = ?
        WHERE id = ? AND reserved_budget_cents >= ?`, cents, cents, formatTime(now), districtID, cents)
	if err != nil {
		return fmt.Errorf("settle district budget: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read budget settle count: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}
