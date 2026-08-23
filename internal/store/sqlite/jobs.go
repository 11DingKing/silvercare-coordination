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

func (s *Store) EnqueueJob(ctx context.Context, q store.DBTX, job domain.WorkerJob) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO worker_jobs(
        id, district_id, kind, object_id, payload_json, state, attempts,
        max_attempts, available_at, locked_by, locked_until, last_error, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, 'pending', 0, ?, ?, NULL, NULL, NULL, ?, ?)`,
		job.ID, job.DistrictID, job.Kind, job.ObjectID, job.PayloadJSON, job.MaxAttempts,
		formatTime(job.AvailableAt), formatTime(job.CreatedAt), formatTime(job.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert worker job: %w", err)
	}
	return nil
}

func (s *Store) ClaimJob(ctx context.Context, workerID string, now time.Time, lease time.Duration) (domain.WorkerJob, error) {
	var job domain.WorkerJob
	err := s.WithinTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT id, district_id, kind, object_id, payload_json,
            state, attempts, max_attempts, available_at, locked_by, locked_until,
            last_error, created_at, updated_at
            FROM worker_jobs WHERE state IN ('pending','retry','running') AND available_at <= ?
              AND (locked_until IS NULL OR locked_until <= ?)
            ORDER BY available_at ASC, created_at ASC LIMIT 1`, formatTime(now), formatTime(now))
		var err error
		job, err = scanJob(row)
		if err != nil {
			return err
		}
		lockedUntil := now.Add(lease)
		result, err := tx.ExecContext(ctx, `UPDATE worker_jobs SET state = 'running',
            attempts = attempts + 1, locked_by = ?, locked_until = ?, updated_at = ?
            WHERE id = ? AND (locked_until IS NULL OR locked_until <= ?)`, workerID,
			formatTime(lockedUntil), formatTime(now), job.ID, formatTime(now))
		if err != nil {
			return fmt.Errorf("claim worker job: %w", err)
		}
		if err := requireOne(result, "worker job was claimed concurrently"); err != nil {
			return err
		}
		job.State = domain.JobRunning
		job.Attempts++
		job.LockedBy = workerID
		job.LockedUntil = &lockedUntil
		job.UpdatedAt = now
		return nil
	})
	return job, err
}

func (s *Store) CompleteJob(ctx context.Context, id, workerID string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE worker_jobs SET state = 'succeeded',
        locked_by = NULL, locked_until = NULL, last_error = NULL, updated_at = ?
        WHERE id = ? AND state = 'running' AND locked_by = ?`, formatTime(now), id, workerID)
	if err != nil {
		return fmt.Errorf("complete worker job: %w", err)
	}
	return requireOne(result, "worker completion lease was lost")
}

func (s *Store) FailJob(ctx context.Context, job domain.WorkerJob, workerID, message string, availableAt, now time.Time) error {
	state := domain.JobRetry
	if job.Attempts >= job.MaxAttempts {
		state = domain.JobDead
	}
	result, err := s.db.ExecContext(ctx, `UPDATE worker_jobs SET state = ?, available_at = ?,
        locked_by = NULL, locked_until = NULL, last_error = ?, updated_at = ?
        WHERE id = ? AND state = 'running' AND locked_by = ?`, string(state),
		formatTime(availableAt), message, formatTime(now), job.ID, workerID)
	if err != nil {
		return fmt.Errorf("fail worker job: %w", err)
	}
	return requireOne(result, "worker failure lease was lost")
}

func (s *Store) JobByID(ctx context.Context, id string) (domain.WorkerJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT id, district_id, kind, object_id,
        payload_json, state, attempts, max_attempts, available_at, locked_by,
        locked_until, last_error, created_at, updated_at FROM worker_jobs WHERE id = ?`, id))
}

func scanJob(row *sql.Row) (domain.WorkerJob, error) {
	var job domain.WorkerJob
	var state, availableAt, createdAt, updatedAt string
	var lockedBy, lockedUntil, lastError sql.NullString
	if err := row.Scan(&job.ID, &job.DistrictID, &job.Kind, &job.ObjectID, &job.PayloadJSON,
		&state, &job.Attempts, &job.MaxAttempts, &availableAt, &lockedBy, &lockedUntil,
		&lastError, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.WorkerJob{}, sql.ErrNoRows
		}
		return domain.WorkerJob{}, fmt.Errorf("scan worker job: %w", err)
	}
	job.State = domain.JobState(state)
	job.LockedBy = lockedBy.String
	job.LastError = lastError.String
	var err error
	if job.AvailableAt, err = parseTime(availableAt); err != nil {
		return domain.WorkerJob{}, err
	}
	if job.LockedUntil, err = nullableTime(lockedUntil); err != nil {
		return domain.WorkerJob{}, err
	}
	if job.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.WorkerJob{}, err
	}
	if job.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.WorkerJob{}, err
	}
	return job, nil
}
