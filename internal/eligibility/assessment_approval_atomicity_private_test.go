package eligibility

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

func TestFailedApprovalPreservesAssessmentState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "approval.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_approval", Name: "福祉街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	resident, err := domain.NewResident("resident_approval", "district_approval", "EXT-APPROVAL", "王秀梅", "household_approval", now.AddDate(-80, 0, 0), now)
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
	if _, err := repo.DB().ExecContext(ctx, `INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('coordinator','district_approval','coord@example.test','hash','协调员','coordinator',1,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	old, err := domain.NewAssessment("assessment_old", resident.ID, "coordinator", 2, now.AddDate(1, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	old.Status = domain.AssessmentApproved
	if err := repo.CreateAssessment(ctx, nil, old); err != nil {
		t.Fatal(err)
	}
	current, err := domain.NewAssessment("assessment_current", resident.ID, "coordinator", 3, now.AddDate(1, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	current, _ = current.PutEvidence("income", "low", now)
	current, _ = current.PutEvidence("mobility", "limited", now)
	current, err = current.Submit(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateAssessment(ctx, nil, current); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `CREATE TRIGGER reject_assessment_approval_event BEFORE INSERT ON outbox_events WHEN NEW.topic='assessment.approved' BEGIN SELECT RAISE(ABORT,'forced approval event failure'); END`); err != nil {
		t.Fatal(err)
	}
	fixed := clock.NewFixed(now)
	service := NewService(repo, &idgen.Sequence{}, fixed, audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "coordinator", DistrictID: resident.DistrictID, Role: domain.RoleCoordinator, RequestID: "approval-request"}
	if _, err := service.Approve(ctx, actor, current.ID, current.Version); err == nil {
		t.Fatal("approval unexpectedly succeeded")
	}
	after, err := repo.AssessmentByID(ctx, nil, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldAfter, err := repo.AssessmentByID(ctx, nil, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.AssessmentSubmitted || after.Version != current.Version || oldAfter.Status != domain.AssessmentApproved || oldAfter.Version != old.Version {
		t.Errorf("partial approval changed current=%s/v%d old=%s/v%d", after.Status, after.Version, oldAfter.Status, oldAfter.Version)
	}
	if _, err := repo.DB().ExecContext(ctx, "DROP TRIGGER reject_assessment_approval_event"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(ctx, actor, current.ID, current.Version); err != nil {
		t.Fatalf("retry approval: %v", err)
	}
	after, err = repo.AssessmentByID(ctx, nil, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldAfter, err = repo.AssessmentByID(ctx, nil, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.AssessmentApproved || after.Version != current.Version+1 || oldAfter.Status != domain.AssessmentSuperseded || oldAfter.Version != old.Version+1 {
		t.Errorf("retry state current=%s/v%d old=%s/v%d", after.Status, after.Version, oldAfter.Status, oldAfter.Version)
	}
}
