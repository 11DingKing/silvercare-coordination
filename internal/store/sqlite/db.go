package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/11DingKing/silvercare-coordination/migrations"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		path = filepath.Clean(path)
	}
	dsn := path
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)
	store := &Store{db: db}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Migrate(ctx context.Context) error {
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	if len(entries) == 0 {
		return fmt.Errorf("no database migrations were embedded")
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        name TEXT NOT NULL,
        applied_at TEXT NOT NULL
    )`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	for _, name := range entries {
		if err := s.applyMigration(ctx, name); err != nil {
			return err
		}
	}
	return s.ValidateHistoricalData(ctx)
}

func (s *Store) applyMigration(ctx context.Context, name string) error {
	version, err := migrationVersion(name)
	if err != nil {
		return err
	}
	var found int
	err = s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", version).Scan(&found)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read migration %d: %w", version, err)
	}
	body, err := migrations.Files.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations(version, name, applied_at) VALUES(?, ?, ?)",
		version, name, formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

func migrationVersion(name string) (int, error) {
	base := filepath.Base(name)
	prefix, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, fmt.Errorf("migration %q does not start with a numeric version", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %q has invalid version", name)
	}
	return version, nil
}

func (s *Store) ValidateHistoricalData(ctx context.Context) error {
	checks := []struct {
		name  string
		query string
	}{
		{"district budget", "SELECT COUNT(*) FROM districts WHERE reserved_budget_cents < 0 OR settled_budget_cents < 0 OR reserved_budget_cents + settled_budget_cents > monthly_budget_cents"},
		{"authorization consumption", "SELECT COUNT(*) FROM authorizations WHERE units_consumed < 0 OR units_consumed > units_authorized OR reserved_cents != (units_authorized - units_consumed) * unit_price_cents"},
		{"visit windows", "SELECT COUNT(*) FROM visits WHERE scheduled_end <= scheduled_start"},
		{"resource ownership", "SELECT COUNT(*) FROM resources WHERE (status = 'assigned' AND (assigned_resident_id IS NULL OR assigned_at IS NULL OR due_at IS NULL)) OR (status != 'assigned' AND (assigned_resident_id IS NOT NULL OR assigned_at IS NOT NULL OR due_at IS NOT NULL))"},
		{"session lifetime", "SELECT COUNT(*) FROM sessions WHERE expires_at <= created_at"},
	}
	for _, check := range checks {
		var count int
		if err := s.db.QueryRowContext(ctx, check.query).Scan(&count); err != nil {
			return fmt.Errorf("validate historical %s: %w", check.name, err)
		}
		if count != 0 {
			return fmt.Errorf("historical %s conflicts detected: %d rows", check.name, count)
		}
	}
	return nil
}

func (s *Store) WithinTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse persisted time %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

func nullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
