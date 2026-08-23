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

func TestFailedAssessmentSubmissionPreservesDraft(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db")); if err != nil { t.Fatal(err) }; defer repo.Close(); repo.Migrate(ctx)
	repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID:"d", Name:"街道", Timezone:"Asia/Shanghai", MonthlyCents:1000000, Version:1, CreatedAt:now, UpdatedAt:now})
	exec := func(q string, a ...any) { if _, e := repo.DB().ExecContext(ctx, q, a...); e != nil { t.Fatal(e) } }
	ts := now.Format(time.RFC3339Nano)
	exec(`INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('u','d','u@e.test','h','u','coordinator',1,?,?)`, ts, ts)
	exec(`INSERT INTO residents(id,district_id,external_ref,full_name,birth_date,household_id,consent_status,consent_expires_at,version,created_at,updated_at) VALUES('r','d','ext','老人','1940-01-01','h','granted',?,1,?,?)`, now.Add(24*time.Hour).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO assessments(id,resident_id,assessor_id,status,support_level,evidence_json,valid_until,version,created_at,updated_at) VALUES('a','r','u','draft',3,'{"home":"x","income":"y"}',?,1,?,?)`, now.Add(24*time.Hour).Format(time.RFC3339Nano), ts, ts)
	exec(`CREATE TRIGGER reject BEFORE INSERT ON audit_events WHEN NEW.action='submit' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	s := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	a := domain.Actor{UserID:"u", DistrictID:"d", Role:domain.RoleCoordinator, RequestID:"req"}
	if _, e := s.Submit(ctx, a, "a", 1); e == nil { t.Fatal("expected failure") }
	got, _ := repo.AssessmentByID(ctx, nil, "a"); if got.Status != domain.AssessmentDraft || got.Version != 1 { t.Errorf("state=%s/%d", got.Status, got.Version) }
	exec(`DROP TRIGGER reject`); if _, e := s.Submit(ctx, a, "a", 1); e != nil { t.Fatal(e) }
}
