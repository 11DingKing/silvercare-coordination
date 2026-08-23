package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

func (s *Store) CreateAssessment(ctx context.Context, q store.DBTX, assessment domain.Assessment) error {
	if q == nil {
		q = s.db
	}
	evidence, err := assessment.EvidenceJSON()
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO assessments(
        id, resident_id, assessor_id, status, support_level, evidence_json,
        valid_until, version, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, assessment.ID, assessment.ResidentID,
		assessment.AssessorID, string(assessment.Status), assessment.SupportLevel, evidence,
		formatTime(assessment.ValidUntil), assessment.Version, formatTime(assessment.CreatedAt), formatTime(assessment.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert assessment: %w", err)
	}
	return nil
}

func (s *Store) AssessmentByID(ctx context.Context, q store.DBTX, id string) (domain.Assessment, error) {
	if q == nil {
		q = s.db
	}
	return scanAssessment(q.QueryRowContext(ctx, `SELECT id, resident_id, assessor_id, status,
        support_level, evidence_json, valid_until, version, created_at, updated_at
        FROM assessments WHERE id = ?`, id))
}

func (s *Store) LatestApprovedAssessment(ctx context.Context, q store.DBTX, residentID string) (domain.Assessment, error) {
	if q == nil {
		q = s.db
	}
	return scanAssessment(q.QueryRowContext(ctx, `SELECT id, resident_id, assessor_id, status,
        support_level, evidence_json, valid_until, version, created_at, updated_at
        FROM assessments WHERE resident_id = ? AND status = 'approved'
        ORDER BY valid_until DESC, created_at DESC LIMIT 1`, residentID))
}

func (s *Store) UpdateAssessment(ctx context.Context, q store.DBTX, assessment domain.Assessment, expectedVersion int64) error {
	if q == nil {
		q = s.db
	}
	evidence, err := assessment.EvidenceJSON()
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `UPDATE assessments SET status = ?, support_level = ?,
        evidence_json = ?, valid_until = ?, version = ?, updated_at = ? WHERE id = ? AND version = ?`,
		string(assessment.Status), assessment.SupportLevel, evidence, formatTime(assessment.ValidUntil),
		assessment.Version, formatTime(assessment.UpdatedAt), assessment.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update assessment: %w", err)
	}
	return requireOne(result, "assessment version conflict")
}

func (s *Store) PersistAssessmentApproval(ctx context.Context, assessment domain.Assessment, expectedVersion int64) error {
	return s.WithinTx(ctx, func(tx *sql.Tx) error {
		current, err := s.AssessmentByID(ctx, tx, assessment.ID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return fmt.Errorf("assessment version conflict")
		}
		return s.UpdateAssessment(ctx, tx, assessment, expectedVersion)
	})
}

func (s *Store) SupersedeOtherAssessments(ctx context.Context, q store.DBTX, residentID, keepID string, updatedAt string) (int64, error) {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE assessments SET status = 'superseded',
        version = version + 1, updated_at = ?
        WHERE resident_id = ? AND id != ? AND status = 'approved'`, updatedAt, residentID, keepID)
	if err != nil {
		return 0, fmt.Errorf("supersede prior assessments: %w", err)
	}
	return result.RowsAffected()
}

func scanAssessment(row *sql.Row) (domain.Assessment, error) {
	var assessment domain.Assessment
	var status, evidence, validUntil, createdAt, updatedAt string
	if err := row.Scan(&assessment.ID, &assessment.ResidentID, &assessment.AssessorID, &status,
		&assessment.SupportLevel, &evidence, &validUntil, &assessment.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Assessment{}, sql.ErrNoRows
		}
		return domain.Assessment{}, fmt.Errorf("scan assessment: %w", err)
	}
	assessment.Status = domain.AssessmentStatus(status)
	if err := decodeJSON(evidence, &assessment.Evidence); err != nil {
		return domain.Assessment{}, err
	}
	var err error
	if assessment.ValidUntil, err = parseTime(validUntil); err != nil {
		return domain.Assessment{}, err
	}
	if assessment.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Assessment{}, err
	}
	if assessment.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Assessment{}, err
	}
	return assessment, nil
}
