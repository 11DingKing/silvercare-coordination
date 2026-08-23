package escalation

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

func TestFailedEscalationAcknowledgementPreservesOpenState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 19, 0, 0, 0, time.UTC)
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "esc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_esc", Name: "应急街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	res, err := domain.NewResident("resident_esc", "district_esc", "EXT-ESC", "郑秀兰", "house_esc", now.AddDate(-81, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateResident(ctx, nil, res); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('coord_esc','district_esc','esc@example.test','hash','协调员','coordinator',1,?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	esc, err := domain.NewEscalation("esc_ack", "district_esc", res.ID, "", domain.SeverityUrgent, "夜间紧急求助持续无人确认", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateEscalation(ctx, nil, esc); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx, `CREATE TRIGGER reject_ack_event BEFORE INSERT ON outbox_events WHEN NEW.topic='escalation.acknowledge' BEGIN SELECT RAISE(ABORT,'forced ack event failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	actor := domain.Actor{UserID: "coord_esc", DistrictID: "district_esc", Role: domain.RoleCoordinator, RequestID: "esc-request"}
	if _, err := service.Acknowledge(ctx, actor, esc.ID, esc.Version); err == nil {
		t.Fatal("ack unexpectedly succeeded")
	}
	after, err := repo.EscalationByID(ctx, nil, esc.ID, "district_esc")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.EscalationOpen || after.Version != 1 || after.AcknowledgedBy != "" {
		t.Errorf("partial ack status=%s version=%d by=%s", after.Status, after.Version, after.AcknowledgedBy)
	}
	repo.DB().ExecContext(ctx, "DROP TRIGGER reject_ack_event")
	if _, err := service.Acknowledge(ctx, actor, esc.ID, esc.Version); err != nil {
		t.Fatalf("retry ack: %v", err)
	}
}
