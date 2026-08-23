package plan

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

func TestFailedPlanActivationPreservesReviewState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 13, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_plan", Name: "照护街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	resident, err := domain.NewResident("resident_plan", "district_plan", "EXT-PLAN", "张桂兰", "household_plan", now.AddDate(-79, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	resident, err = resident.GrantConsent(now.AddDate(1, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateResident(ctx, nil, resident); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('coord_plan','district_plan','coord-plan@example.test','hash','协调员','coordinator',1,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `INSERT INTO assessments(id,resident_id,assessor_id,status,support_level,evidence_json,valid_until,version,created_at,updated_at) VALUES('assessment_plan','resident_plan','coord_plan','approved',2,'{}',?,1,?,?)`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `INSERT INTO support_plans(id,resident_id,assessment_id,coordinator_id,status,starts_at,ends_at,goals_json,version,created_at,updated_at) VALUES('plan_activation','resident_plan','assessment_plan','coord_plan','review',?,?,'["保持每日用药"]',2,?,?)`, now.Add(-time.Hour).Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `CREATE TRIGGER reject_plan_activation_event BEFORE INSERT ON outbox_events WHEN NEW.topic='support_plan.activated' BEGIN SELECT RAISE(ABORT,'forced plan activation event failure'); END`); err != nil {
		t.Fatal(err)
	}
	fixed := clock.NewFixed(now)
	service := NewService(repo, &idgen.Sequence{}, fixed, audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "coord_plan", DistrictID: "district_plan", Role: domain.RoleCoordinator, RequestID: "plan-request"}
	if _, err := service.Activate(ctx, actor, "plan_activation", 2); err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	after, err := repo.PlanByID(ctx, nil, "plan_activation")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.PlanReview || after.Version != 2 {
		t.Errorf("partial activation changed status=%s version=%d", after.Status, after.Version)
	}
	if _, err := repo.DB().ExecContext(ctx, "DROP TRIGGER reject_plan_activation_event"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Activate(ctx, actor, "plan_activation", 2); err != nil {
		t.Fatalf("retry activation: %v", err)
	}
	after, err = repo.PlanByID(ctx, nil, "plan_activation")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.PlanActive || after.Version != 3 {
		t.Errorf("retry state status=%s version=%d", after.Status, after.Version)
	}
}
