package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

func (s *Store) CreateAuthorization(ctx context.Context, q store.DBTX, a domain.Authorization) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO authorizations(
        id, plan_id, district_id, service_code, status, units_authorized, units_consumed,
        unit_price_cents, reserved_cents, starts_at, ends_at, version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.ID, a.PlanID, a.DistrictID,
		a.ServiceCode, string(a.Status), a.UnitsAuthorized, a.UnitsConsumed, a.UnitPriceCents,
		a.ReservedCents, formatTime(a.StartsAt), formatTime(a.EndsAt), a.Version,
		formatTime(a.CreatedAt), formatTime(a.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert authorization: %w", err)
	}
	return nil
}

func (s *Store) AuthorizationByID(ctx context.Context, q store.DBTX, id, districtID string) (domain.Authorization, error) {
	if q == nil {
		q = s.db
	}
	return scanAuthorization(q.QueryRowContext(ctx, `SELECT id, plan_id, district_id, service_code,
        status, units_authorized, units_consumed, unit_price_cents, reserved_cents,
        starts_at, ends_at, version, created_at, updated_at
        FROM authorizations WHERE id = ? AND district_id = ?`, id, districtID))
}

func (s *Store) UpdateAuthorization(ctx context.Context, q store.DBTX, a domain.Authorization, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE authorizations SET status = ?, units_consumed = ?,
        reserved_cents = ?, version = ?, updated_at = ? WHERE id = ? AND district_id = ? AND version = ?`,
		string(a.Status), a.UnitsConsumed, a.ReservedCents, a.Version, formatTime(a.UpdatedAt),
		a.ID, a.DistrictID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update authorization: %w", err)
	}
	return requireOne(result, "authorization version conflict")
}

func (s *Store) PersistAuthorizationActivation(ctx context.Context, a domain.Authorization, expectedVersion int64) error {
	return s.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.AuthorizationByID(ctx, tx, a.ID, a.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("authorization version conflict")
		}
		return s.UpdateAuthorization(ctx, tx, a, expectedVersion)
	})
}

func scanAuthorization(row *sql.Row) (domain.Authorization, error) {
	var a domain.Authorization
	var status, startsAt, endsAt, createdAt, updatedAt string
	if err := row.Scan(&a.ID, &a.PlanID, &a.DistrictID, &a.ServiceCode, &status,
		&a.UnitsAuthorized, &a.UnitsConsumed, &a.UnitPriceCents, &a.ReservedCents,
		&startsAt, &endsAt, &a.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Authorization{}, sql.ErrNoRows
		}
		return domain.Authorization{}, fmt.Errorf("scan authorization: %w", err)
	}
	a.Status = domain.AuthorizationStatus(status)
	var err error
	if a.StartsAt, err = parseTime(startsAt); err != nil {
		return domain.Authorization{}, err
	}
	if a.EndsAt, err = parseTime(endsAt); err != nil {
		return domain.Authorization{}, err
	}
	if a.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Authorization{}, err
	}
	if a.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Authorization{}, err
	}
	return a, nil
}
