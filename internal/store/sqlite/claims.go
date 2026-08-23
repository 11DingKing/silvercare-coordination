package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

func (s *Store) CreateClaim(ctx context.Context, q store.DBTX, claim domain.Claim) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO claims(
        id, visit_id, district_id, status, amount_cents, reviewer_id,
        rejection_reason, version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, claim.ID, claim.VisitID, claim.DistrictID,
		string(claim.Status), claim.AmountCents, nullString(claim.ReviewerID),
		nullString(claim.RejectionReason), claim.Version, formatTime(claim.CreatedAt), formatTime(claim.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert claim: %w", err)
	}
	return nil
}

func (s *Store) ClaimByID(ctx context.Context, q store.DBTX, id, districtID string) (domain.Claim, error) {
	if q == nil {
		q = s.db
	}
	return scanClaim(q.QueryRowContext(ctx, `SELECT id, visit_id, district_id, status,
        amount_cents, reviewer_id, rejection_reason, version, created_at, updated_at
        FROM claims WHERE id = ? AND district_id = ?`, id, districtID))
}

func (s *Store) UpdateClaim(ctx context.Context, q store.DBTX, claim domain.Claim, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE claims SET status = ?, reviewer_id = ?,
        rejection_reason = ?, version = ?, updated_at = ? WHERE id = ? AND district_id = ? AND version = ?`,
		string(claim.Status), nullString(claim.ReviewerID), nullString(claim.RejectionReason),
		claim.Version, formatTime(claim.UpdatedAt), claim.ID, claim.DistrictID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update claim: %w", err)
	}
	return requireOne(result, "claim version conflict")
}

func (s *Store) ClaimForVisit(ctx context.Context, q store.DBTX, visitID string) (domain.Claim, error) {
	if q == nil {
		q = s.db
	}
	return scanClaim(q.QueryRowContext(ctx, `SELECT id, visit_id, district_id, status,
        amount_cents, reviewer_id, rejection_reason, version, created_at, updated_at
        FROM claims WHERE visit_id = ?`, visitID))
}

func scanClaim(row *sql.Row) (domain.Claim, error) {
	var claim domain.Claim
	var status, createdAt, updatedAt string
	var reviewer, reason sql.NullString
	if err := row.Scan(&claim.ID, &claim.VisitID, &claim.DistrictID, &status,
		&claim.AmountCents, &reviewer, &reason, &claim.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Claim{}, sql.ErrNoRows
		}
		return domain.Claim{}, fmt.Errorf("scan claim: %w", err)
	}
	claim.Status = domain.ClaimStatus(status)
	claim.ReviewerID = reviewer.String
	claim.RejectionReason = reason.String
	var err error
	if claim.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Claim{}, err
	}
	if claim.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Claim{}, err
	}
	return claim, nil
}
