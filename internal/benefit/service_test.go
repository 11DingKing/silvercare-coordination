package benefit

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
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

// failingOutboxStore embeds the real SQLite store so every method keeps its
// real transactional behavior, and overrides EnqueueOutbox to simulate a
// settlement-event save failure. This exercises the exact rollback path the
// production code relies on.
type failingOutboxStore struct {
	*storesqlite.Store
	mu         sync.Mutex
	enqueueErr error
	enqueued   int
}

func (s *failingOutboxStore) EnqueueOutbox(ctx context.Context, q store.DBTX, event domain.OutboxEvent) error {
	s.mu.Lock()
	if s.enqueueErr != nil {
		s.mu.Unlock()
		return s.enqueueErr
	}
	s.mu.Unlock()
	s.mu.Lock()
	s.enqueued++
	s.mu.Unlock()
	return s.Store.EnqueueOutbox(ctx, q, event)
}

func newClaimService(t *testing.T, now time.Time, store_ *failingOutboxStore) *Service {
	t.Helper()
	recorder := audit.NewRecorder(store_)
	publisher := outbox.NewPublisher(store_, &idgen.Sequence{})
	return NewService(store_, &idgen.Sequence{}, clock.NewFixed(now), recorder, publisher)
}

func seedClaimFixture(t *testing.T, store_ *failingOutboxStore, now time.Time) (domain.Visit, domain.Actor) {
	t.Helper()
	ctx := context.Background()
	if err := store_.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{
		ID: "district_1", Name: "示范街道", Timezone: "Asia/Shanghai",
		MonthlyCents: 100_000_000, Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store_.CreateUser(ctx, nil, domain.User{
		ID: "provider_1", DistrictID: "district_1", Email: "provider@example.test",
		PasswordHash: "hash", DisplayName: "护理员", Role: domain.RoleProvider,
		Active: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	resident, err := domain.NewResident("resident_1", "district_1", "EXT-1", "张建国", "household_1", now.AddDate(-70, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	resident, err = resident.GrantConsent(now.AddDate(1, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store_.CreateResident(ctx, nil, resident); err != nil {
		t.Fatal(err)
	}
	assessor, err := domain.NewAssessment("assessment_1", resident.ID, "provider_1", 2, now.AddDate(0, 1, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	assessor, _ = assessor.PutEvidence("intake", "completed", now)
	assessor, _ = assessor.PutEvidence("interview", "completed", now)
	assessor, _ = assessor.Submit(now)
	assessor, _ = assessor.Approve(now)
	if err := store_.CreateAssessment(ctx, nil, assessor); err != nil {
		t.Fatal(err)
	}
	plan, err := domain.NewSupportPlan("plan_1", resident.ID, assessor.ID, "provider_1", now, now.Add(180*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	plan, _ = plan.AddGoal("协助日常生活与个人护理", now)
	plan, _ = plan.SubmitForReview(now)
	plan, _ = plan.Activate(assessor, resident, now)
	if err := store_.CreatePlan(ctx, nil, plan); err != nil {
		t.Fatal(err)
	}
	authorization, err := domain.NewAuthorization("auth_1", plan.ID, "district_1", "personal_care", 5, 2500, now.Add(-6*time.Hour), now.Add(24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ = authorization.Activate(plan, now)
	if err := store_.CreateAuthorization(ctx, nil, authorization); err != nil {
		t.Fatal(err)
	}
	if err := store_.ReserveBudget(ctx, nil, "district_1", authorization.ReservedCents, 1, now); err != nil {
		t.Fatal(err)
	}
	provider, err := domain.NewProvider("provider_1", "district_1", "示范护理站", []string{"personal_care"}, 8, now)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ = provider.Activate(now)
	if err := store_.CreateProvider(ctx, nil, provider); err != nil {
		t.Fatal(err)
	}
	visit, err := domain.NewVisit("visit_1", authorization, provider, resident.ID, now.Add(-3*time.Hour), now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store_.CreateVisit(ctx, nil, visit); err != nil {
		t.Fatal(err)
	}
	visit, _ = visit.Assign("provider_1", now)
	visit, _ = visit.Accept("provider_1", now)
	visit, _ = visit.StartTravel("provider_1", now)
	checkedIn := now.Add(-2 * time.Hour)
	visit, _ = visit.CheckIn("provider_1", checkedIn, now)
	completed := now.Add(-time.Hour)
	visit, _ = visit.Complete("provider_1", map[string]string{"resident_confirmation": "yes", "service_note": "delivered"}, completed, now)
	if err := store_.UpdateVisit(ctx, nil, visit, 1); err != nil {
		t.Fatal(err)
	}
	actor := domain.Actor{UserID: "provider_1", DistrictID: "district_1", Role: domain.RoleProvider, RequestID: "request_1"}
	return visit, actor
}

// TestCreateClaimLeavesNoResidueWhenEventEnqueueFails reproduces the reported
// bug: when the settlement-event save fails after the claim was created, the
// submitted claim must not linger. With the single-transaction fix the claim
// row, audit event, and outbox event roll back together, so a clean retry
// succeeds once the settlement pipeline works again.
func TestCreateClaimLeavesNoResidueWhenEventEnqueueFails(t *testing.T) {
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	store_ := &failingOutboxStore{Store: openTestStore(t)}
	visit, actor := seedClaimFixture(t, store_, now)
	store_.enqueueErr = sql.ErrConnDone
	service := newClaimService(t, now, store_)

	if _, err := service.CreateClaim(context.Background(), actor, visit.ID); err == nil {
		t.Fatal("failed settlement submission returned no error")
	}
	var claims, audits, events int
	_ = store_.DB().QueryRow("SELECT COUNT(*) FROM claims WHERE visit_id=?", visit.ID).Scan(&claims)
	_ = store_.DB().QueryRow("SELECT COUNT(*) FROM audit_events WHERE object_type='claim' AND action='submit'").Scan(&audits)
	_ = store_.DB().QueryRow("SELECT COUNT(*) FROM outbox_events WHERE topic='claim.submitted'").Scan(&events)
	if claims != 0 || audits != 0 || events != 0 {
		t.Fatalf("residual rows after failed submission: claims=%d audits=%d events=%d", claims, audits, events)
	}

	// The visit must be cleanly re-submittable once the settlement pipeline works.
	store_.enqueueErr = nil
	claim, err := service.CreateClaim(context.Background(), actor, visit.ID)
	if err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if claim.Status != domain.ClaimSubmitted {
		t.Fatalf("retry status = %s", claim.Status)
	}
	_ = store_.DB().QueryRow("SELECT COUNT(*) FROM claims WHERE visit_id=?", visit.ID).Scan(&claims)
	_ = store_.DB().QueryRow("SELECT COUNT(*) FROM audit_events WHERE object_type='claim' AND action='submit'").Scan(&audits)
	_ = store_.DB().QueryRow("SELECT COUNT(*) FROM outbox_events WHERE topic='claim.submitted'").Scan(&events)
	if claims != 1 || audits != 1 || events != 1 {
		t.Fatalf("retry rows: claims=%d audits=%d events=%d", claims, audits, events)
	}
}

func openTestStore(t *testing.T) *storesqlite.Store {
	t.Helper()
	store_, err := storesqlite.Open(context.Background(), filepath.Join(t.TempDir(), "benefit.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store_.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store_.Close() })
	return store_
}
