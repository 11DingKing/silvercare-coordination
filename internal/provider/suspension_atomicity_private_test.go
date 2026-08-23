package provider

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

func TestFailedProviderSuspensionPreservesActiveState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	repo.Migrate(ctx)
	repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "d", Name: "街道", Timezone: "Asia/Shanghai", MonthlyCents: 1000000, Version: 1, CreatedAt: now, UpdatedAt: now})
	exec := func(q string, a ...any) {
		if _, e := repo.DB().ExecContext(ctx, q, a...); e != nil {
			t.Fatal(e)
		}
	}
	ts := now.Format(time.RFC3339Nano)
	exec(`INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('u','d','u@e.test','h','u','coordinator',1,?,?)`, ts, ts)
	exec(`INSERT INTO providers(id,district_id,name,accreditation_status,capabilities_json,capacity_per_day,version,created_at,updated_at) VALUES('p','d','站','active','["home_support"]',8,1,?,?)`, ts, ts)
	exec(`CREATE TRIGGER reject BEFORE INSERT ON outbox_events WHEN NEW.topic='provider.accreditation_changed' BEGIN SELECT RAISE(ABORT,'x'); END`)
	s := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	a := domain.Actor{UserID: "u", DistrictID: "d", Role: domain.RoleCoordinator, RequestID: "r"}
	if _, e := s.Suspend(ctx, a, "p", 1); e == nil {
		t.Fatal("ok")
	}
	p, _ := repo.ProviderByID(ctx, nil, "p", "d")
	if p.AccreditationStatus != domain.AccreditationActive || p.Version != 1 {
		t.Errorf("state=%s/%d", p.AccreditationStatus, p.Version)
	}
	exec(`DROP TRIGGER reject`)
	if _, e := s.Suspend(ctx, a, "p", 1); e != nil {
		t.Fatal(e)
	}
}
