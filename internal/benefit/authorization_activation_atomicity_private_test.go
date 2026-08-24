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

func TestFailedAuthorizationActivationPreservesReservedState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_auth", Name: "额度街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		if _, e := repo.DB().ExecContext(ctx, q, a...); e != nil {
			t.Fatal(e)
		}
	}
	ts := now.Format(time.RFC3339Nano)
	exec(`INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('coord_auth','district_auth','auth-coord@example.test','hash','协调员','coordinator',1,?,?)`, ts, ts)
	exec(`INSERT INTO residents(id,district_id,external_ref,full_name,birth_date,household_id,consent_status,consent_expires_at,version,created_at,updated_at) VALUES('resident_auth','district_auth','EXT-AUTH','郭秀兰','1947-01-01','house_auth','granted',?,1,?,?)`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO assessments(id,resident_id,assessor_id,status,support_level,evidence_json,valid_until,version,created_at,updated_at) VALUES('assessment_auth','resident_auth','coord_auth','approved',2,'{}',?,1,?,?)`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO support_plans(id,resident_id,assessment_id,coordinator_id,status,starts_at,ends_at,goals_json,version,created_at,updated_at) VALUES('plan_auth','resident_auth','assessment_auth','coord_auth','active',?,?,'[]',1,?,?)`, now.Add(-time.Hour).Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO authorizations(id,plan_id,district_id,service_code,status,units_authorized,units_consumed,unit_price_cents,reserved_cents,starts_at,ends_at,version,created_at,updated_at) VALUES('authorization_activation','plan_auth','district_auth','home_support','reserved',2,0,1000,2000,?,?,1,?,?)`, now.Add(-time.Hour).Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`CREATE TRIGGER reject_auth_audit BEFORE INSERT ON audit_events WHEN NEW.object_id='authorization_activation' AND NEW.action='activate' BEGIN SELECT RAISE(ABORT,'forced auth audit failure'); END`)
	service := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "coord_auth", DistrictID: "district_auth", Role: domain.RoleCoordinator, RequestID: "auth-request"}
	if _, err := service.Activate(ctx, actor, "authorization_activation", 1); err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	after, err := repo.AuthorizationByID(ctx, nil, "authorization_activation", "district_auth")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.AuthorizationReserved || after.Version != 1 {
		t.Errorf("partial activation status=%s version=%d", after.Status, after.Version)
	}
	exec(`DROP TRIGGER reject_auth_audit`)
	if _, err := service.Activate(ctx, actor, "authorization_activation", 1); err != nil {
		t.Fatalf("retry activation: %v", err)
	}
	after, err = repo.AuthorizationByID(ctx, nil, "authorization_activation", "district_auth")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.AuthorizationActive || after.Version != 2 {
		t.Errorf("retry status=%s version=%d", after.Status, after.Version)
	}
}
