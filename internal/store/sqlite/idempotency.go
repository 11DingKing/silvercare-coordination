package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/store"
)

type IdempotencyRecord struct {
	DistrictID   string
	ActorID      string
	Operation    string
	Key          string
	RequestHash  string
	ResponseCode int
	ResponseJSON string
	State        string
	ExpiresAt    time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (s *Store) BeginIdempotency(ctx context.Context, q store.DBTX, record IdempotencyRecord) (IdempotencyRecord, bool, error) {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO idempotency_records(
        district_id, actor_id, operation, idempotency_key, request_hash,
        response_code, response_json, state, expires_at, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, NULL, NULL, 'started', ?, ?, ?)`, record.DistrictID,
		record.ActorID, record.Operation, record.Key, record.RequestHash,
		formatTime(record.ExpiresAt), formatTime(record.CreatedAt), formatTime(record.UpdatedAt))
	if err == nil {
		return record, true, nil
	}
	existing, getErr := s.Idempotency(ctx, q, record.DistrictID, record.ActorID, record.Operation, record.Key)
	if getErr != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("begin idempotency: %w", err)
	}
	if existing.RequestHash != record.RequestHash {
		return IdempotencyRecord{}, false, fmt.Errorf("idempotency key was reused with a different request")
	}
	return existing, false, nil
}

func (s *Store) CompleteIdempotency(ctx context.Context, q store.DBTX, record IdempotencyRecord) error {
	if q == nil {
		q = s.db
	}
	result, err := q.ExecContext(ctx, `UPDATE idempotency_records SET response_code = ?,
        response_json = ?, state = 'completed', updated_at = ?
        WHERE district_id = ? AND actor_id = ? AND operation = ? AND idempotency_key = ?
          AND request_hash = ? AND state = 'started'`, record.ResponseCode, record.ResponseJSON,
		formatTime(record.UpdatedAt), record.DistrictID, record.ActorID, record.Operation,
		record.Key, record.RequestHash)
	if err != nil {
		return fmt.Errorf("complete idempotency: %w", err)
	}
	return requireOne(result, "idempotency record is no longer owned")
}

func (s *Store) Idempotency(ctx context.Context, q store.DBTX, districtID, actorID, operation, key string) (IdempotencyRecord, error) {
	if q == nil {
		q = s.db
	}
	var record IdempotencyRecord
	var responseCode sql.NullInt64
	var responseJSON sql.NullString
	var expiresAt, createdAt, updatedAt string
	err := q.QueryRowContext(ctx, `SELECT district_id, actor_id, operation, idempotency_key,
        request_hash, response_code, response_json, state, expires_at, created_at, updated_at
        FROM idempotency_records WHERE district_id = ? AND actor_id = ? AND operation = ? AND idempotency_key = ?`,
		districtID, actorID, operation, key).Scan(&record.DistrictID, &record.ActorID, &record.Operation,
		&record.Key, &record.RequestHash, &responseCode, &responseJSON, &record.State,
		&expiresAt, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IdempotencyRecord{}, sql.ErrNoRows
		}
		return IdempotencyRecord{}, fmt.Errorf("read idempotency record: %w", err)
	}
	record.ResponseCode = int(responseCode.Int64)
	record.ResponseJSON = responseJSON.String
	if record.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return IdempotencyRecord{}, err
	}
	if record.CreatedAt, err = parseTime(createdAt); err != nil {
		return IdempotencyRecord{}, err
	}
	if record.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return IdempotencyRecord{}, err
	}
	return record, nil
}

func (s *Store) DeleteExpiredIdempotency(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM idempotency_records WHERE expires_at <= ?", formatTime(before))
	if err != nil {
		return 0, fmt.Errorf("delete expired idempotency records: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) DeleteExpiredIdempotencyBatch(ctx context.Context, before time.Time) (int64, error) {
	var deleted int64
	err := s.WithinTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT district_id, actor_id, operation, idempotency_key FROM idempotency_records WHERE expires_at <= ? ORDER BY expires_at, idempotency_key", formatTime(before))
		if err != nil {
			return fmt.Errorf("list expired idempotency records: %w", err)
		}
		type key struct{ district, actor, operation, value string }
		var keys []key
		for rows.Next() {
			var k key
			if err := rows.Scan(&k.district, &k.actor, &k.operation, &k.value); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, k)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, k := range keys {
			res, err := tx.ExecContext(ctx, "DELETE FROM idempotency_records WHERE district_id=? AND actor_id=? AND operation=? AND idempotency_key=?", k.district, k.actor, k.operation, k.value)
			if err != nil {
				return fmt.Errorf("delete expired idempotency %s: %w", k.value, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			deleted += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
