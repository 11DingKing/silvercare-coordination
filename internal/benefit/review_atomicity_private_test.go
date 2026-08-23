package benefit

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

func TestFailedClaimApprovalPreservesSubmitted(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil { t.Fatal(err) }
	defer repo.Close()
	repo.Migrate(ctx)
	repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "d", Name: "街道", Timezone: "Asia/Shanghai", MonthlyCents: 1000000, Version: 1, CreatedAt: now, UpdatedAt: now})
	exec := func(q string, args ...any) { if _, e := repo.DB().ExecContext(ctx, q, args...); e != nil { t.Fatal(e) } }
	ts := now.Format(time.RFC3339Nano)
	future := now.Add(24 * time.Hour).Format(time.RFC3339Nano)
	exec(`INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('u','d','u@e.test','h','u','coordinator',1,?,?)`, ts, ts)
	exec(`INSERT INTO residents(id,district_id,external_ref,full_name,birth_date,household_id,consent_status,version,created_at,updated_at) VALUES('r','d','ext','老人','1940-01-01','h','granted',1,?,?)`, ts, ts)
	exec(`INSERT INTO assessments(id,resident_id,assessor_id,status,support_level,evidence_json,valid_until,version,created_at,updated_at) VALUES('a','r','u','approved',3,'{}',?,1,?,?)`, future, ts, ts)
	exec(`INSERT INTO support_plans(id,resident_id,assessment_id,coordinator_id,status,starts_at,ends_at,goals_json,version,created_at,updated_at) VALUES('p','r','a','u','active',?,?, '["goal"]',1,?,?)`, ts, future, ts, ts)
	exec(`INSERT INTO authorizations(id,plan_id,district_id,service_code,status,units_authorized,units_consumed,unit_price_cents,reserved_cents,starts_at,ends_at,version,created_at,updated_at) VALUES('az','p','d','home','active',1,0,100,100,?,?,1,?,?)`, ts, future, ts, ts)
	exec(`INSERT INTO providers(id,district_id,name,accreditation_status,capabilities_json,capacity_per_day,version,created_at,updated_at) VALUES('pr','d','站','active','[]',8,1,?,?)`, ts, ts)
	exec(`INSERT INTO visits(id,authorization_id,provider_id,resident_id,status,scheduled_start,scheduled_end,completed_at,version,created_at,updated_at) VALUES('v','az','pr','r','completed',?,?,?,1,?,?)`, ts, now.Add(time.Hour).Format(time.RFC3339Nano), now.Add(time.Hour).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO claims(id,visit_id,district_id,status,amount_cents,version,created_at,updated_at) VALUES('c','v','d','submitted',100,1,?,?)`, ts, ts)
	exec(`CREATE TRIGGER reject BEFORE INSERT ON audit_events WHEN NEW.action='review' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	s := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "u", DistrictID: "d", Role: domain.RoleCoordinator, RequestID: "req"}
	if _, err := s.ReviewClaim(ctx, actor, "c", true, "", 1); err == nil { t.Fatal("expected failure") }
	got, _ := repo.ClaimByID(ctx, nil, "c", "d")
	if got.Status != domain.ClaimSubmitted || got.Version != 1 { t.Errorf("state=%s/%d", got.Status, got.Version) }
	exec(`DROP TRIGGER reject`)
	if _, err := s.ReviewClaim(ctx, actor, "c", true, "", 1); err != nil { t.Fatal(err) }
}
