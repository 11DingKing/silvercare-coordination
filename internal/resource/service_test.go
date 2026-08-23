package resource

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/audit"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/outbox"
	"github.com/11DingKing/silvercare-coordination/internal/store"
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
)

// failingOutbox is an outbox.Repository whose EnqueueOutbox always fails,
// simulating the "事件记录异常" (event recording failure) reported in the
// field while AppendAudit on the same store succeeds.
type failingOutbox struct{}

func (failingOutbox) EnqueueOutbox(_ context.Context, _ store.DBTX, _ domain.OutboxEvent) error {
	return errors.New("simulated event recording failure")
}

func newFailingPublisher() *outbox.Publisher {
	return outbox.NewPublisher(failingOutbox{}, &idgen.Sequence{})
}

func resourceFixture(t *testing.T) (*storesqlite.Store, *clock.Fixed, domain.Actor, domain.Resource, domain.Resident) {
	t.Helper()
	ctx := context.Background()
	now := clock.NewFixed(time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC))
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "resource.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_1", Name: "示范街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now.Now(), UpdatedAt: now.Now()}); err != nil {
		t.Fatal(err)
	}
	actor := domain.Actor{UserID: "coordinator_1", DistrictID: "district_1", Role: domain.RoleCoordinator, RequestID: "request_1"}
	resident, rerr := domain.NewResident("resident_1", "district_1", "EXT-1", "张建国", "household_1", now.Now().AddDate(-70, 0, 0), now.Now())
	if rerr != nil {
		t.Fatal(rerr)
	}
	resident, _ = resident.GrantConsent(now.Now().AddDate(1, 0, 0), now.Now())
	if err := repo.CreateResident(ctx, nil, resident); err != nil {
		t.Fatal(err)
	}
	resource, _ := domain.NewResource("resource_1", "district_1", "assistive_device", "SN-001", now.Now())
	if err := repo.CreateResource(ctx, nil, resource); err != nil {
		t.Fatal(err)
	}
	return repo, now, actor, resource, resident
}

// TestAssignDoesNotLeakOwnershipWhenEventRecordingFails reproduces the field
// report: a service-station processing a walker checkout hit an event-recording
// failure, yet the device already showed as assigned to the resident. Retrying
// then returned "not allocatable" because the assignment had persisted in a
// separate transaction that survived the failure. The fix performs the resource
// update, audit, and outbox event in one transaction, so a failure rolls the
// assignment back and the original version can complete the delivery.
func TestAssignDoesNotLeakOwnershipWhenEventRecordingFails(t *testing.T) {
	ctx := context.Background()
	repo, fixed, actor, resource, resident := resourceFixture(t)
	now := fixed.Now()
	service := NewService(repo, &idgen.Sequence{}, fixed, audit.NewRecorder(repo), newFailingPublisher())

	dueAt := now.AddDate(0, 1, 0)
	if _, err := service.Assign(ctx, actor, resource.ID, resident.ID, dueAt, resource.Version); err == nil {
		t.Fatalf("expected assign to fail when event recording fails")
	}

	// No state residue: the device must remain available and unassigned.
	current, err := repo.ResourceByID(ctx, nil, resource.ID, actor.DistrictID)
	if err != nil {
		t.Fatalf("reload resource: %v", err)
	}
	if current.Status != domain.ResourceAvailable || current.AssignedResidentID != "" || current.Version != resource.Version {
		t.Fatalf("state residue: status=%s resident=%q version=%d (want available, empty, %d)",
			current.Status, current.AssignedResidentID, current.Version, resource.Version)
	}

	// After recovery the same original version completes the delivery.
	healthy := NewService(repo, &idgen.Sequence{}, fixed, audit.NewRecorder(repo), outbox.NewPublisher(repo, &idgen.Sequence{}))
	updated, err := healthy.Assign(ctx, actor, resource.ID, resident.ID, dueAt, resource.Version)
	if err != nil {
		t.Fatalf("retry assign after recovery: %v", err)
	}
	if updated.Status != domain.ResourceAssigned || updated.AssignedResidentID != resident.ID {
		t.Fatalf("retry did not assign: %+v", updated)
	}
}
