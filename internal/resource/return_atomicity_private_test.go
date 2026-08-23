package resource

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

func TestFailedResourceReturnPreservesAssignment(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "return.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_return", Name: "辅具街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		if _, e := repo.DB().ExecContext(ctx, q, a...); e != nil {
			t.Fatal(e)
		}
	}
	ts := now.Format(time.RFC3339Nano)
	exec(`INSERT INTO residents(id,district_id,external_ref,full_name,birth_date,household_id,consent_status,consent_expires_at,version,created_at,updated_at) VALUES('resident_return','district_return','EXT-RETURN','杨秀芳','1944-01-01','house_return','granted',?,1,?,?)`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO resources(id,district_id,resource_type,serial_number,status,assigned_resident_id,assigned_at,due_at,version,created_at,updated_at) VALUES('resource_return','district_return','walker','W-RETURN','assigned','resident_return',?,?,2,?,?)`, now.Add(-7*24*time.Hour).Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano), ts, ts)
	exec(`INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('coord_return','district_return','return@example.test','hash','协调员','coordinator',1,?,?)`, ts, ts)
	exec(`CREATE TRIGGER reject_return_event BEFORE INSERT ON outbox_events WHEN NEW.topic='resource.returned' BEGIN SELECT RAISE(ABORT,'forced return event failure'); END`)
	service := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "coord_return", DistrictID: "district_return", Role: domain.RoleCoordinator, RequestID: "return-request"}
	if _, err := service.Return(ctx, actor, "resource_return", false, 2); err == nil {
		t.Fatal("return unexpectedly succeeded")
	}
	after, err := repo.ResourceByID(ctx, nil, "resource_return", "district_return")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.ResourceAssigned || after.AssignedResidentID != "resident_return" || after.Version != 2 {
		t.Errorf("partial return status=%s resident=%s version=%d", after.Status, after.AssignedResidentID, after.Version)
	}
	exec(`DROP TRIGGER reject_return_event`)
	if _, err := service.Return(ctx, actor, "resource_return", false, 2); err != nil {
		t.Fatalf("retry return: %v", err)
	}
	after, err = repo.ResourceByID(ctx, nil, "resource_return", "district_return")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.ResourceAvailable || after.AssignedResidentID != "" || after.Version != 3 {
		t.Errorf("retry status=%s resident=%s version=%d", after.Status, after.AssignedResidentID, after.Version)
	}
}
