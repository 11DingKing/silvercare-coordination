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

func (s *Store) CreateProvider(ctx context.Context, q store.DBTX, provider domain.Provider) error {
	if q == nil {
		q = s.db
	}
	capabilities, err := provider.CapabilitiesJSON()
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO providers(
        id, district_id, name, accreditation_status, capabilities_json,
        capacity_per_day, version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, provider.ID, provider.DistrictID, provider.Name,
		string(provider.AccreditationStatus), capabilities, provider.CapacityPerDay, provider.Version,
		formatTime(provider.CreatedAt), formatTime(provider.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert provider: %w", err)
	}
	return nil
}

func (s *Store) ProviderByID(ctx context.Context, q store.DBTX, id, districtID string) (domain.Provider, error) {
	if q == nil {
		q = s.db
	}
	return scanProvider(q.QueryRowContext(ctx, `SELECT id, district_id, name, accreditation_status,
        capabilities_json, capacity_per_day, version, created_at, updated_at
        FROM providers WHERE id = ? AND district_id = ?`, id, districtID))
}

func (s *Store) UpdateProvider(ctx context.Context, q store.DBTX, provider domain.Provider, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	capabilities, err := provider.CapabilitiesJSON()
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `UPDATE providers SET name = ?, accreditation_status = ?,
        capabilities_json = ?, capacity_per_day = ?, version = ?, updated_at = ?
        WHERE id = ? AND district_id = ? AND version = ?`, provider.Name,
		string(provider.AccreditationStatus), capabilities, provider.CapacityPerDay, provider.Version,
		formatTime(provider.UpdatedAt), provider.ID, provider.DistrictID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	return requireOne(result, "provider version conflict")
}

func (s *Store) ProviderVisitCountOnDay(ctx context.Context, q store.DBTX, providerID string, day time.Time) (int, error) {
	if q == nil {
		q = s.db
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM visits WHERE provider_id = ?
        AND scheduled_start >= ? AND scheduled_start < ?
        AND status NOT IN ('cancelled','disputed')`, providerID, formatTime(start), formatTime(end)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count provider visits: %w", err)
	}
	return count, nil
}

func scanProvider(row *sql.Row) (domain.Provider, error) {
	var provider domain.Provider
	var status, capabilities, createdAt, updatedAt string
	if err := row.Scan(&provider.ID, &provider.DistrictID, &provider.Name, &status, &capabilities,
		&provider.CapacityPerDay, &provider.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Provider{}, sql.ErrNoRows
		}
		return domain.Provider{}, fmt.Errorf("scan provider: %w", err)
	}
	provider.AccreditationStatus = domain.AccreditationStatus(status)
	if err := decodeJSON(capabilities, &provider.Capabilities); err != nil {
		return domain.Provider{}, err
	}
	var err error
	if provider.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Provider{}, err
	}
	if provider.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Provider{}, err
	}
	return provider, nil
}
