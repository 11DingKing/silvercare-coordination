package provider

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/audit"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/outbox"
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
)

func TestFailedProviderActivationPreservesPendingState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "provider.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_provider", Name: "服务街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('coord_provider','district_provider','provider-coord@example.test','hash','协调员','coordinator',1,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `INSERT INTO providers(id,district_id,name,accreditation_status,capabilities_json,capacity_per_day,version,created_at,updated_at) VALUES('provider_atomic','district_provider','康复服务站','pending','["home_support"]',8,1,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `CREATE TRIGGER reject_provider_event BEFORE INSERT ON outbox_events WHEN NEW.topic='provider.accreditation_changed' BEGIN SELECT RAISE(ABORT,'forced provider event failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "coord_provider", DistrictID: "district_provider", Role: domain.RoleCoordinator, RequestID: "provider-request"}
	if _, err := service.Activate(ctx, actor, "provider_atomic", 1); err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	after, err := repo.ProviderByID(ctx, nil, "provider_atomic", "district_provider")
	if err != nil {
		t.Fatal(err)
	}
	if after.AccreditationStatus != domain.AccreditationPending || after.Version != 1 {
		t.Errorf("partial activation status=%s version=%d", after.AccreditationStatus, after.Version)
	}
	if _, err := repo.DB().ExecContext(ctx, "DROP TRIGGER reject_provider_event"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Activate(ctx, actor, "provider_atomic", 1); err != nil {
		t.Fatalf("retry activation: %v", err)
	}
	after, err = repo.ProviderByID(ctx, nil, "provider_atomic", "district_provider")
	if err != nil {
		t.Fatal(err)
	}
	if after.AccreditationStatus != domain.AccreditationActive || after.Version != 2 {
		t.Errorf("retry state status=%s version=%d", after.AccreditationStatus, after.Version)
	}
}
