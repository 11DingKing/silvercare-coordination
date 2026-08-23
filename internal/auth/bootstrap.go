package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/config"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
)

type BootstrapRepository interface {
	WithinTx(context.Context, func(*sql.Tx) error) error
	EnsureDistrict(context.Context, interfaceDBTX, sqlite.DistrictBudget) error
	UserByEmail(context.Context, string, string) (domain.User, error)
	CreateUser(context.Context, interfaceDBTX, domain.User) error
}

type interfaceDBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Bootstrap uses the concrete store because it is startup wiring, not a domain use case.
func Bootstrap(ctx context.Context, repo *sqlite.Store, ids idgen.Generator, cfg config.Bootstrap, now time.Time) error {
	if !cfg.Enabled {
		return nil
	}
	email, err := domain.NormalizeEmail(cfg.AdminEmail)
	if err != nil {
		return fmt.Errorf("normalize bootstrap email: %w", err)
	}
	if _, err := repo.UserByEmail(ctx, cfg.DistrictID, email); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check bootstrap user: %w", err)
	}
	passwordHash, err := HashPassword(cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("bootstrap password: %w", err)
	}
	userID, err := ids.New("user")
	if err != nil {
		return fmt.Errorf("bootstrap user id: %w", err)
	}
	return repo.WithinTx(ctx, func(tx *sql.Tx) error {
		if err := repo.EnsureDistrict(ctx, tx, sqlite.DistrictBudget{
			ID: cfg.DistrictID, Name: cfg.DistrictName, Timezone: "Asia/Shanghai",
			MonthlyCents: 100_000_000, Version: 1, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return repo.CreateUser(ctx, tx, domain.User{
			ID: userID, DistrictID: cfg.DistrictID, Email: email, PasswordHash: passwordHash,
			DisplayName: "系统协调员", Role: domain.RoleCoordinator, Active: true,
			CreatedAt: now, UpdatedAt: now,
		})
	})
}
