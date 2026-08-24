package worker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
)

func TestEscalationExpiryBatchRollsBackOnLaterFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 23, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "expiry-batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{
		ID: "district_batch", Name: "夜间服务街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000,
		Version: 1, CreatedAt: now.AddDate(0, -1, 0), UpdatedAt: now.AddDate(0, -1, 0),
	}); err != nil {
		t.Fatal(err)
	}
	resident, err := domain.NewResident("resident_batch", "district_batch", "EXT-BATCH", "赵玉兰", "household_batch", now.AddDate(-78, 0, 0), now.AddDate(0, -1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateResident(ctx, nil, resident); err != nil {
		t.Fatal(err)
	}
	first, err := domain.NewEscalation("escalation_first", "district_batch", resident.ID, "", domain.SeverityUrgent, "夜间呼救持续无人确认需要巡检", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.NewEscalation("escalation_second", "district_batch", resident.ID, "", domain.SeverityUrgent, "紧急用药求助持续无人确认需处理", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateEscalation(ctx, nil, first); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateEscalation(ctx, nil, second); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `
		CREATE TRIGGER reject_second_escalation_expiry
		BEFORE UPDATE ON escalations
		WHEN NEW.id = 'escalation_second' AND NEW.status = 'expired'
		BEGIN
			SELECT RAISE(ABORT, 'forced later expiry failure');
		END;
	`); err != nil {
		t.Fatal(err)
	}

	maintenance := NewMaintenance(repo, clock.NewFixed(now))
	if err := maintenance.ExpireEscalations(ctx, domain.WorkerJob{}); err == nil {
		t.Fatal("expiry batch unexpectedly succeeded")
	}
	firstAfterFailure, err := repo.EscalationByID(ctx, nil, first.ID, "district_batch")
	if err != nil {
		t.Fatal(err)
	}
	secondAfterFailure, err := repo.EscalationByID(ctx, nil, second.ID, "district_batch")
	if err != nil {
		t.Fatal(err)
	}
	if firstAfterFailure.Status != domain.EscalationOpen || secondAfterFailure.Status != domain.EscalationOpen || firstAfterFailure.Version != 1 || secondAfterFailure.Version != 1 {
		t.Errorf("failed batch left first=%s/v%d second=%s/v%d", firstAfterFailure.Status, firstAfterFailure.Version, secondAfterFailure.Status, secondAfterFailure.Version)
	}

	if _, err := repo.DB().ExecContext(ctx, "DROP TRIGGER reject_second_escalation_expiry"); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.ExpireEscalations(ctx, domain.WorkerJob{}); err != nil {
		t.Fatalf("retry expiry batch: %v", err)
	}
	firstAfterRetry, err := repo.EscalationByID(ctx, nil, first.ID, "district_batch")
	if err != nil {
		t.Fatal(err)
	}
	secondAfterRetry, err := repo.EscalationByID(ctx, nil, second.ID, "district_batch")
	if err != nil {
		t.Fatal(err)
	}
	if firstAfterRetry.Status != domain.EscalationExpired || secondAfterRetry.Status != domain.EscalationExpired || firstAfterRetry.Version != 2 || secondAfterRetry.Version != 2 {
		t.Errorf("retry batch left first=%s/v%d second=%s/v%d", firstAfterRetry.Status, firstAfterRetry.Version, secondAfterRetry.Status, secondAfterRetry.Version)
	}
}
