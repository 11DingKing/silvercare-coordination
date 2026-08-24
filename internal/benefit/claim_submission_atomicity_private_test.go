package benefit

import (
	"context"
	"github.com/11DingKing/silvercare-coordination/internal/audit"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/outbox"
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestFailedClaimSubmissionLeavesNoClaim(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 16, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_claim", Name: "结算街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		if _, e := repo.DB().ExecContext(ctx, q, a...); e != nil {
			t.Fatal(e)
		}
	}
	ts := now.Format(time.RFC3339Nano)
	exec(`INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('provider_claim','district_claim','provider-claim@example.test','hash','服务员','provider',1,?,?)`, ts, ts)
	exec(`INSERT INTO residents(id,district_id,external_ref,full_name,birth_date,household_id,consent_status,consent_expires_at,version,created_at,updated_at) VALUES('resident_claim','district_claim','EXT-CLAIM','陈秀英','1946-01-01','house_claim','granted',?,1,?,?)`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO assessments(id,resident_id,assessor_id,status,support_level,evidence_json,valid_until,version,created_at,updated_at) VALUES('assessment_claim','resident_claim','provider_claim','approved',2,'{}',?,1,?,?)`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO support_plans(id,resident_id,assessment_id,coordinator_id,status,starts_at,ends_at,goals_json,version,created_at,updated_at) VALUES('plan_claim','resident_claim','assessment_claim','provider_claim','active',?,?,'[]',1,?,?)`, now.Add(-time.Hour).Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO providers(id,district_id,name,accreditation_status,capabilities_json,capacity_per_day,version,created_at,updated_at) VALUES('provider_station','district_claim','照护站','active','["home_support"]',8,1,?,?)`, ts, ts)
	exec(`INSERT INTO authorizations(id,plan_id,district_id,service_code,status,units_authorized,units_consumed,unit_price_cents,reserved_cents,starts_at,ends_at,version,created_at,updated_at) VALUES('auth_claim','plan_claim','district_claim','home_support','active',2,1,1000,1000,?,?,1,?,?)`, now.Add(-time.Hour).Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO visits(id,authorization_id,provider_id,assigned_user_id,resident_id,status,scheduled_start,scheduled_end,checked_in_at,completed_at,evidence_json,version,created_at,updated_at) VALUES('visit_claim','auth_claim','provider_station','provider_claim','resident_claim','completed',?,?,?, ?,?,4,?,?)`, now.Add(-time.Hour).Format(time.RFC3339Nano), now.Add(time.Hour).Format(time.RFC3339Nano), now.Add(-30*time.Minute).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), `{"resident_confirmation":"yes","service_note":"done"}`, ts, ts)
	exec(`CREATE TRIGGER reject_claim_event BEFORE INSERT ON outbox_events WHEN NEW.topic='claim.submitted' BEGIN SELECT RAISE(ABORT,'forced claim event failure'); END`)
	service := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "provider_claim", DistrictID: "district_claim", Role: domain.RoleProvider, RequestID: "claim-request"}
	if _, err := service.CreateClaim(ctx, actor, "visit_claim"); err == nil {
		t.Fatal("claim unexpectedly succeeded")
	}
	var count int
	if err := repo.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM claims WHERE visit_id='visit_claim'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("failed claim left %d rows", count)
	}
	exec(`DROP TRIGGER reject_claim_event`)
	if _, err := service.CreateClaim(ctx, actor, "visit_claim"); err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	if err := repo.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM claims WHERE visit_id='visit_claim' AND status='submitted'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("retry claim count=%d", count)
	}
}
