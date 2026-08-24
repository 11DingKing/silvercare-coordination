package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/store"
	"github.com/11DingKing/silvercare-coordination/internal/worker"
)

func seedOverdueEscalation(t *testing.T, store *Store, id string, created, due time.Time) domain.Escalation {
	t.Helper()
	e, err := domain.NewEscalation(id, "district_1", "resident_1", "", domain.SeverityCritical, "需要立即升级无人确认的支持事项", created)
	if err != nil {
		t.Fatal(err)
	}
	e.AcknowledgementDueAt = due
	if err := store.CreateEscalation(context.Background(), nil, e); err != nil {
		t.Fatalf("seed escalation %s: %v", id, err)
	}
	return e
}

func assertEscalationStatus(t *testing.T, store *Store, id string, want domain.EscalationStatus) {
	t.Helper()
	got, err := store.EscalationByID(context.Background(), nil, id, "district_1")
	if err != nil {
		t.Fatalf("load %s: %v", id, err)
	}
	if got.Status != want {
		t.Fatalf("escalation %s status = %s want %s", id, got.Status, want)
	}
}

func TestExpireEscalationsCommitsBatchAtomically(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	created := time.Now().UTC().Add(-1 * time.Hour)
	seedDistrict(t, store, created)
	seedResident(t, store, created)
	now := created.Add(30 * time.Minute)
	seedOverdueEscalation(t, store, "esc_1", created, created.Add(5*time.Minute))
	seedOverdueEscalation(t, store, "esc_2", created, created.Add(6*time.Minute))

	m := worker.NewMaintenance(store, clock.NewFixed(now))
	if err := m.ExpireEscalations(ctx, domain.WorkerJob{}); err != nil {
		t.Fatalf("expire escalations: %v", err)
	}
	assertEscalationStatus(t, store, "esc_1", domain.EscalationExpired)
	assertEscalationStatus(t, store, "esc_2", domain.EscalationExpired)
}

// faultingMaintRepo wraps the Store and fails PersistEscalationExpiry for a
// specific escalation, simulating a downstream fault partway through a batch.
type faultingMaintRepo struct {
	*Store
	failID string
	ran    bool
}

func (f *faultingMaintRepo) PersistEscalationExpiry(ctx context.Context, q store.DBTX, e domain.Escalation, expectedVersion int64) error {
	if e.ID == f.failID {
		f.ran = true
		return errDownstreamFault
	}
	return f.Store.PersistEscalationExpiry(ctx, q, e, expectedVersion)
}

var errDownstreamFault = errFault("downstream persist fault")

type errFault string

func (e errFault) Error() string { return string(e) }

// TestExpireEscalationsRollsBackBatchOnFailure reproduces the nightly patrol
// partial-state bug: when a later escalation cannot be persisted, escalations
// flagged expired earlier in the same batch must roll back so every pending
// escalation stays open and the whole batch can complete once the fault clears.
func TestExpireEscalationsRollsBackBatchOnFailure(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	created := time.Now().UTC().Add(-1 * time.Hour)
	seedDistrict(t, store, created)
	seedResident(t, store, created)
	now := created.Add(30 * time.Minute)
	seedOverdueEscalation(t, store, "esc_1", created, created.Add(5*time.Minute))
	seedOverdueEscalation(t, store, "esc_2", created, created.Add(6*time.Minute))

	repo := &faultingMaintRepo{Store: store, failID: "esc_2"}
	m := worker.NewMaintenance(repo, clock.NewFixed(now))
	err := m.ExpireEscalations(ctx, domain.WorkerJob{})
	if err == nil {
		t.Fatal("expected batch to fail when a later escalation faults")
	}
	if !repo.ran {
		t.Fatal("faulting escalation was never reached")
	}

	// Both escalations must remain open: the shared transaction rolled back
	// esc_1 even though it was processed before the failure.
	assertEscalationStatus(t, store, "esc_1", domain.EscalationOpen)
	assertEscalationStatus(t, store, "esc_2", domain.EscalationOpen)

	// Once the fault clears, the whole batch can complete.
	repo.failID = ""
	if err := m.ExpireEscalations(ctx, domain.WorkerJob{}); err != nil {
		t.Fatalf("retry after fault cleared: %v", err)
	}
	assertEscalationStatus(t, store, "esc_1", domain.EscalationExpired)
	assertEscalationStatus(t, store, "esc_2", domain.EscalationExpired)
}
