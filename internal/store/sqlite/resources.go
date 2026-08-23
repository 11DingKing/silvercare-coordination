package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

func (s *Store) CreateResource(ctx context.Context, q store.DBTX, resource domain.Resource) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO resources(
        id, district_id, resource_type, serial_number, status, assigned_resident_id,
        assigned_at, due_at, version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, resource.ID, resource.DistrictID,
		resource.ResourceType, resource.SerialNumber, string(resource.Status),
		nullString(resource.AssignedResidentID), formatNullableTime(resource.AssignedAt),
		formatNullableTime(resource.DueAt), resource.Version, formatTime(resource.CreatedAt), formatTime(resource.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert resource: %w", err)
	}
	return nil
}

func (s *Store) ResourceByID(ctx context.Context, q store.DBTX, id, districtID string) (domain.Resource, error) {
	if q == nil {
		q = s.db
	}
	return scanResource(q.QueryRowContext(ctx, `SELECT id, district_id, resource_type,
        serial_number, status, assigned_resident_id, assigned_at, due_at,
        version, created_at, updated_at FROM resources WHERE id = ? AND district_id = ?`, id, districtID))
}

func (s *Store) UpdateResource(ctx context.Context, q store.DBTX, resource domain.Resource, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE resources SET status = ?, assigned_resident_id = ?,
        assigned_at = ?, due_at = ?, version = ?, updated_at = ?
        WHERE id = ? AND district_id = ? AND version = ?`, string(resource.Status),
		nullString(resource.AssignedResidentID), formatNullableTime(resource.AssignedAt),
		formatNullableTime(resource.DueAt), resource.Version, formatTime(resource.UpdatedAt),
		resource.ID, resource.DistrictID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update resource: %w", err)
	}
	return requireOne(result, "resource version conflict")
}

func (s *Store) PersistResourceReturn(ctx context.Context, resource domain.Resource, expectedVersion int64) error {
	return s.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.ResourceByID(ctx, tx, resource.ID, resource.DistrictID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("resource version conflict")
		}
		return s.UpdateResource(ctx, tx, resource, expectedVersion)
	})
}

func scanResource(row *sql.Row) (domain.Resource, error) {
	var resource domain.Resource
	var status, createdAt, updatedAt string
	var resident, assignedAt, dueAt sql.NullString
	if err := row.Scan(&resource.ID, &resource.DistrictID, &resource.ResourceType,
		&resource.SerialNumber, &status, &resident, &assignedAt, &dueAt,
		&resource.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Resource{}, sql.ErrNoRows
		}
		return domain.Resource{}, fmt.Errorf("scan resource: %w", err)
	}
	resource.Status = domain.ResourceStatus(status)
	resource.AssignedResidentID = resident.String
	var err error
	if resource.AssignedAt, err = nullableTime(assignedAt); err != nil {
		return domain.Resource{}, err
	}
	if resource.DueAt, err = nullableTime(dueAt); err != nil {
		return domain.Resource{}, err
	}
	if resource.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Resource{}, err
	}
	if resource.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Resource{}, err
	}
	return resource, nil
}
