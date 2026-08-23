package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

func (s *Store) CreateUser(ctx context.Context, q store.DBTX, user domain.User) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO users(
        id, district_id, email, password_hash, display_name, role, active, created_at, updated_at
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.DistrictID, user.Email, user.PasswordHash, user.DisplayName, string(user.Role),
		boolInt(user.Active), formatTime(user.CreatedAt), formatTime(user.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *Store) UserByEmail(ctx context.Context, districtID, email string) (domain.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id, district_id, email, password_hash,
        display_name, role, active, created_at, updated_at
        FROM users WHERE district_id = ? AND email = ?`, districtID, email))
}

func (s *Store) UserByID(ctx context.Context, id string) (domain.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id, district_id, email, password_hash,
        display_name, role, active, created_at, updated_at
        FROM users WHERE id = ?`, id))
}

func (s *Store) UserByIDWith(ctx context.Context, q store.DBTX, id string) (domain.User, error) {
	if q == nil {
		q = s.db
	}
	return scanUser(q.QueryRowContext(ctx, `SELECT id, district_id, email, password_hash,
        display_name, role, active, created_at, updated_at FROM users WHERE id = ?`, id))
}

func scanUser(row *sql.Row) (domain.User, error) {
	var user domain.User
	var role, createdAt, updatedAt string
	var active int
	if err := row.Scan(&user.ID, &user.DistrictID, &user.Email, &user.PasswordHash,
		&user.DisplayName, &role, &active, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.User{}, sql.ErrNoRows
		}
		return domain.User{}, fmt.Errorf("scan user: %w", err)
	}
	parsedRole, err := domain.ParseRole(role)
	if err != nil {
		return domain.User{}, fmt.Errorf("parse user role: %w", err)
	}
	user.Role = parsedRole
	user.Active = active == 1
	if user.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.User{}, err
	}
	if user.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) SetUserActive(ctx context.Context, id string, active bool, updatedAt string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE users SET active = ?, updated_at = ? WHERE id = ?", boolInt(active), updatedAt, id)
	if err != nil {
		return fmt.Errorf("set user active: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read user update count: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}
