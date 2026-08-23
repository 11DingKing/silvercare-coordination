package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

type ResidentFilter struct {
	DistrictID string
	Household  string
	Consent    domain.ConsentStatus
	Limit      int
	Offset     int
}

func (s *Store) CreateResident(ctx context.Context, q store.DBTX, resident domain.Resident) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO residents(
        id, district_id, external_ref, full_name, birth_date, household_id,
        consent_status, consent_expires_at, version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, resident.ID, resident.DistrictID,
		resident.ExternalRef, resident.FullName, resident.BirthDate.Format("2006-01-02"), resident.HouseholdID,
		string(resident.ConsentStatus), formatNullableTime(resident.ConsentExpiresAt), resident.Version,
		formatTime(resident.CreatedAt), formatTime(resident.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert resident: %w", err)
	}
	return nil
}

func (s *Store) ResidentByID(ctx context.Context, q store.DBTX, id, districtID string) (domain.Resident, error) {
	if q == nil {
		q = s.db
	}
	return scanResident(q.QueryRowContext(ctx, `SELECT id, district_id, external_ref, full_name,
        birth_date, household_id, consent_status, consent_expires_at, version, created_at, updated_at
        FROM residents WHERE id = ? AND district_id = ?`, id, districtID))
}

func (s *Store) UpdateResident(ctx context.Context, q store.DBTX, resident domain.Resident, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE residents SET full_name = ?, household_id = ?,
        consent_status = ?, consent_expires_at = ?, version = ?, updated_at = ?
        WHERE id = ? AND district_id = ? AND version = ?`, resident.FullName, resident.HouseholdID,
		string(resident.ConsentStatus), formatNullableTime(resident.ConsentExpiresAt), resident.Version,
		formatTime(resident.UpdatedAt), resident.ID, resident.DistrictID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update resident: %w", err)
	}
	return requireOne(result, "resident version conflict")
}
func (s *Store) ListResidents(ctx context.Context, filter ResidentFilter) ([]domain.Resident, int, error) {
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	where := " WHERE district_id = ?"
	args := []any{filter.DistrictID}
	if filter.Household != "" {
		where += " AND household_id = ?"
		args = append(args, filter.Household)
	}
	if filter.Consent != "" {
		where += " AND consent_status = ?"
		args = append(args, string(filter.Consent))
	}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM residents"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count residents: %w", err)
	}
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id, district_id, external_ref, full_name,
        birth_date, household_id, consent_status, consent_expires_at, version, created_at, updated_at
        FROM residents`+where+` ORDER BY updated_at DESC, id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list residents: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Resident, 0, filter.Limit)
	for rows.Next() {
		resident, err := scanResidentRows(rows)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, resident)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate residents: %w", err)
	}
	return result, total, nil
}

type rowScanner interface{ Scan(...any) error }

func scanResident(row rowScanner) (domain.Resident, error)     { return scanResidentValue(row) }
func scanResidentRows(rows *sql.Rows) (domain.Resident, error) { return scanResidentValue(rows) }

func scanResidentValue(row rowScanner) (domain.Resident, error) {
	var resident domain.Resident
	var birthDate, consent, createdAt, updatedAt string
	var consentExpiry sql.NullString
	if err := row.Scan(&resident.ID, &resident.DistrictID, &resident.ExternalRef, &resident.FullName,
		&birthDate, &resident.HouseholdID, &consent, &consentExpiry, &resident.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Resident{}, sql.ErrNoRows
		}
		return domain.Resident{}, fmt.Errorf("scan resident: %w", err)
	}
	var err error
	resident.BirthDate, err = time.Parse("2006-01-02", birthDate)
	if err != nil {
		return domain.Resident{}, fmt.Errorf("parse resident birth date: %w", err)
	}
	resident.ConsentStatus = domain.ConsentStatus(consent)
	if resident.ConsentExpiresAt, err = nullableTime(consentExpiry); err != nil {
		return domain.Resident{}, err
	}
	if resident.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Resident{}, err
	}
	if resident.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Resident{}, err
	}
	return resident, nil
}

func requireOne(result sql.Result, message string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if changed != 1 {
		return fmt.Errorf("%s", message)
	}
	return nil
}

func formatNullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func encodeJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode json: %w", err)
	}
	return string(raw), nil
}

func decodeJSON(raw string, target any) error {
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("decode persisted json: %w", err)
	}
	return nil
}
