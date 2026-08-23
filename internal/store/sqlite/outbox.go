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

func (s *Store) EnqueueOutbox(ctx context.Context, q store.DBTX, event domain.OutboxEvent) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO outbox_events(
        id, district_id, topic, aggregate_type, aggregate_id, payload_json,
        state, attempts, available_at, locked_by, locked_until, last_error, created_at, delivered_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, ?, NULL)`, event.ID, event.DistrictID,
		event.Topic, event.AggregateType, event.AggregateID, event.PayloadJSON,
		string(domain.OutboxPending), 0, formatTime(event.AvailableAt), formatTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func (s *Store) ClaimOutbox(ctx context.Context, workerID string, now time.Time, lease time.Duration) (domain.OutboxEvent, error) {
	var claimed domain.OutboxEvent
	err := s.WithinTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT id, district_id, topic, aggregate_type, aggregate_id,
            payload_json, state, attempts, available_at, locked_by, locked_until,
            last_error, created_at, delivered_at
            FROM outbox_events WHERE state IN ('pending','processing') AND available_at <= ?
              AND (locked_until IS NULL OR locked_until <= ?)
            ORDER BY available_at ASC, created_at ASC LIMIT 1`, formatTime(now), formatTime(now))
		var err error
		claimed, err = scanOutbox(row)
		if err != nil {
			return err
		}
		lockedUntil := now.Add(lease)
		result, err := tx.ExecContext(ctx, `UPDATE outbox_events SET state = 'processing',
            attempts = attempts + 1, locked_by = ?, locked_until = ?
            WHERE id = ? AND (locked_until IS NULL OR locked_until <= ?)`,
			workerID, formatTime(lockedUntil), claimed.ID, formatTime(now))
		if err != nil {
			return fmt.Errorf("claim outbox event: %w", err)
		}
		if err := requireOne(result, "outbox event was claimed concurrently"); err != nil {
			return err
		}
		claimed.State = domain.OutboxProcessing
		claimed.Attempts++
		claimed.LockedBy = workerID
		claimed.LockedUntil = &lockedUntil
		return nil
	})
	return claimed, err
}

func (s *Store) MarkOutboxDelivered(ctx context.Context, id, workerID string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE outbox_events SET state = 'delivered',
        locked_by = NULL, locked_until = NULL, last_error = NULL, delivered_at = ?
        WHERE id = ? AND state = 'processing' AND locked_by = ?`, formatTime(now), id, workerID)
	if err != nil {
		return fmt.Errorf("mark outbox delivered: %w", err)
	}
	return requireOne(result, "outbox delivery lease was lost")
}

func (s *Store) RetryOutbox(ctx context.Context, id, workerID, message string, availableAt time.Time, dead bool) error {
	state := domain.OutboxPending
	if dead {
		state = domain.OutboxDead
	}
	result, err := s.db.ExecContext(ctx, `UPDATE outbox_events SET state = ?, available_at = ?,
        locked_by = NULL, locked_until = NULL, last_error = ?
        WHERE id = ? AND state = 'processing' AND locked_by = ?`, string(state), formatTime(availableAt), message, id, workerID)
	if err != nil {
		return fmt.Errorf("retry outbox event: %w", err)
	}
	return requireOne(result, "outbox retry lease was lost")
}

func scanOutbox(row *sql.Row) (domain.OutboxEvent, error) {
	var event domain.OutboxEvent
	var state, availableAt, createdAt string
	var lockedBy, lockedUntil, lastError, deliveredAt sql.NullString
	if err := row.Scan(&event.ID, &event.DistrictID, &event.Topic, &event.AggregateType,
		&event.AggregateID, &event.PayloadJSON, &state, &event.Attempts, &availableAt,
		&lockedBy, &lockedUntil, &lastError, &createdAt, &deliveredAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.OutboxEvent{}, sql.ErrNoRows
		}
		return domain.OutboxEvent{}, fmt.Errorf("scan outbox event: %w", err)
	}
	event.State = domain.OutboxState(state)
	event.LockedBy = lockedBy.String
	event.LastError = lastError.String
	var err error
	if event.AvailableAt, err = parseTime(availableAt); err != nil {
		return domain.OutboxEvent{}, err
	}
	if event.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.OutboxEvent{}, err
	}
	if event.LockedUntil, err = nullableTime(lockedUntil); err != nil {
		return domain.OutboxEvent{}, err
	}
	if event.DeliveredAt, err = nullableTime(deliveredAt); err != nil {
		return domain.OutboxEvent{}, err
	}
	return event, nil
}
