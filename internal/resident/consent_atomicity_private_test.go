package resident

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

func TestFailedConsentGrantPreservesWithdrawnState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 14, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "consent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_consent", Name: "康养街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	res, err := domain.NewResident("resident_consent", "district_consent", "EXT-CONSENT", "孙桂香", "household_consent", now.AddDate(-82, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateResident(ctx, nil, res); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('coord_consent','district_consent','consent@example.test','hash','协调员','coordinator',1,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `CREATE TRIGGER reject_consent_event BEFORE INSERT ON outbox_events WHEN NEW.topic='resident.consent_granted' BEGIN SELECT RAISE(ABORT,'forced consent event failure'); END`); err != nil {
		t.Fatal(err)
	}
	fixed := clock.NewFixed(now)
	service := NewService(repo, &idgen.Sequence{}, fixed, audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "coord_consent", DistrictID: "district_consent", Role: domain.RoleCoordinator, RequestID: "consent-request"}
	if _, err := service.GrantConsent(ctx, actor, res.ID, now.AddDate(1, 0, 0), res.Version); err == nil {
		t.Fatal("consent grant unexpectedly succeeded")
	}
	after, err := repo.ResidentByID(ctx, nil, res.ID, res.DistrictID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ConsentStatus != domain.ConsentWithdrawn || after.Version != res.Version || after.ConsentExpiresAt != nil {
		t.Errorf("partial consent status=%s version=%d expires=%v", after.ConsentStatus, after.Version, after.ConsentExpiresAt)
	}
	if _, err := repo.DB().ExecContext(ctx, "DROP TRIGGER reject_consent_event"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GrantConsent(ctx, actor, res.ID, now.AddDate(1, 0, 0), res.Version); err != nil {
		t.Fatalf("retry consent: %v", err)
	}
	after, err = repo.ResidentByID(ctx, nil, res.ID, res.DistrictID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ConsentStatus != domain.ConsentGranted || after.Version != res.Version+1 || after.ConsentExpiresAt == nil {
		t.Errorf("retry consent status=%s version=%d expires=%v", after.ConsentStatus, after.Version, after.ConsentExpiresAt)
	}
}
