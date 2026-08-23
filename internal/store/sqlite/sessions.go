package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

func (s *Store) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(
        id, user_id, token_hash, expires_at, revoked_at, created_at, last_seen_at
    ) VALUES(?, ?, ?, ?, NULL, ?, ?)`,
		session.ID, session.UserID, session.TokenHash, formatTime(session.ExpiresAt),
		formatTime(session.CreatedAt), formatTime(session.LastSeenAt))
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *Store) SessionAndUserByTokenHash(ctx context.Context, tokenHash string) (domain.Session, domain.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
        se.id, se.user_id, se.token_hash, se.expires_at, se.revoked_at, se.created_at, se.last_seen_at,
        u.id, u.district_id, u.email, u.password_hash, u.display_name, u.role, u.active, u.created_at, u.updated_at
        FROM sessions se JOIN users u ON u.id = se.user_id WHERE se.token_hash = ?`, tokenHash)
	var session domain.Session
	var user domain.User
	var expiresAt, sessionCreated, lastSeen string
	var revoked sql.NullString
	var role, userCreated, userUpdated string
	var active int
	if err := row.Scan(
		&session.ID, &session.UserID, &session.TokenHash, &expiresAt, &revoked, &sessionCreated, &lastSeen,
		&user.ID, &user.DistrictID, &user.Email, &user.PasswordHash, &user.DisplayName, &role, &active, &userCreated, &userUpdated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, domain.User{}, sql.ErrNoRows
		}
		return domain.Session{}, domain.User{}, fmt.Errorf("scan session and user: %w", err)
	}
	var err error
	if session.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if session.RevokedAt, err = nullableTime(revoked); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if session.CreatedAt, err = parseTime(sessionCreated); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if session.LastSeenAt, err = parseTime(lastSeen); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if user.Role, err = domain.ParseRole(role); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	user.Active = active == 1
	if user.CreatedAt, err = parseTime(userCreated); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if user.UpdatedAt, err = parseTime(userUpdated); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	return session, user, nil
}

func (s *Store) TouchSession(ctx context.Context, id string, seenAt time.Time) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL AND expires_at > ?",
		formatTime(seenAt), id, formatTime(seenAt))
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read session touch count: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RevokeSession(ctx context.Context, id string, revokedAt time.Time) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL",
		formatTime(revokedAt), id)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read session revoke count: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RevokeAllUserSessions(ctx context.Context, userID string, revokedAt time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL",
		formatTime(revokedAt), userID)
	if err != nil {
		return 0, fmt.Errorf("revoke user sessions: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read revoked session count: %w", err)
	}
	return changed, nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE expires_at <= ? OR (revoked_at IS NOT NULL AND revoked_at <= ?)",
		formatTime(before), formatTime(before.Add(-24*time.Hour)))
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return result.RowsAffected()
}
