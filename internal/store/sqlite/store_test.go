package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "silvercare.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		store.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedDistrict(t *testing.T, store *Store, now time.Time) {
	t.Helper()
	err := store.EnsureDistrict(context.Background(), nil, DistrictBudget{ID: "district_1", Name: "示范街道", Timezone: "Asia/Shanghai", MonthlyCents: 1_000_000, Version: 1, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("seed district: %v", err)
	}
}

func seedUser(t *testing.T, store *Store, id string, role domain.Role, now time.Time) domain.User {
	t.Helper()
	user := domain.User{ID: id, DistrictID: "district_1", Email: id + "@example.test", PasswordHash: "hash", DisplayName: id, Role: role, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(context.Background(), nil, user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return user
}

func seedResident(t *testing.T, store *Store, now time.Time) domain.Resident {
	t.Helper()
	resident, err := domain.NewResident("resident_1", "district_1", "EXT-1", "张建国", "household_1", now.AddDate(-70, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	resident, err = resident.GrantConsent(now.AddDate(1, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateResident(context.Background(), nil, resident); err != nil {
		t.Fatal(err)
	}
	return resident
}

func TestMigrateIsIdempotentAndCreatesRelationships(t *testing.T) {
	store := testStore(t)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration count = %d", count)
	}
	var foreignKeys int
	if err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d", foreignKeys)
	}
	if _, err := store.db.Exec(`INSERT INTO residents(id,district_id,external_ref,full_name,birth_date,household_id,consent_status,version,created_at,updated_at)
        VALUES('r','missing','e','name','1950-01-01','h','pending',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("foreign key violation accepted")
	}
}

func TestRestartRecoversPersistedResident(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	ctx := context.Background()
	now := time.Now().UTC()
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	seedDistrict(t, first, now)
	resident := seedResident(t, first, now)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := second.ResidentByID(ctx, nil, resident.ID, resident.DistrictID)
	if err != nil {
		t.Fatalf("load after restart: %v", err)
	}
	if loaded.ExternalRef != resident.ExternalRef || loaded.ConsentStatus != domain.ConsentGranted {
		t.Fatalf("loaded = %+v", loaded)
	}
}

func TestWithinTxRollsBackAllWrites(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	errSentinel := errors.New("audit unavailable")
	err := store.WithinTx(context.Background(), func(tx *sql.Tx) error {
		resident, err := domain.NewResident("resident_1", "district_1", "EXT-1", "张建国", "household_1", now.AddDate(-70, 0, 0), now)
		if err != nil {
			return err
		}
		if err := store.CreateResident(context.Background(), tx, resident); err != nil {
			return err
		}
		if err := store.EnqueueOutbox(context.Background(), tx, domain.OutboxEvent{ID: "event_1", DistrictID: "district_1", Topic: "resident.created", AggregateType: "resident", AggregateID: resident.ID, PayloadJSON: "{}", AvailableAt: now, CreatedAt: now}); err != nil {
			return err
		}
		return errSentinel
	})
	if !errors.Is(err, errSentinel) {
		t.Fatalf("err = %v", err)
	}
	var residents, events int
	_ = store.db.QueryRow("SELECT COUNT(*) FROM residents").Scan(&residents)
	_ = store.db.QueryRow("SELECT COUNT(*) FROM outbox_events").Scan(&events)
	if residents != 0 || events != 0 {
		t.Fatalf("rollback leaked residents=%d events=%d", residents, events)
	}
}

func TestBudgetReservationIsConditionalAndTransactional(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	budget, err := store.DistrictBudget(context.Background(), nil, "district_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveBudget(context.Background(), nil, "district_1", 700_000, budget.Version, now); err != nil {
		t.Fatal(err)
	}
	budget, _ = store.DistrictBudget(context.Background(), nil, "district_1")
	if budget.ReservedCents != 700_000 {
		t.Fatalf("reserved = %d", budget.ReservedCents)
	}
	if err := store.ReserveBudget(context.Background(), nil, "district_1", 400_000, budget.Version, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("over-budget err = %v", err)
	}
	if err := store.ReleaseBudget(context.Background(), nil, "district_1", 200_000, now); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleBudget(context.Background(), nil, "district_1", 300_000, now); err != nil {
		t.Fatal(err)
	}
	budget, _ = store.DistrictBudget(context.Background(), nil, "district_1")
	if budget.ReservedCents != 200_000 || budget.SettledCents != 300_000 {
		t.Fatalf("budget = %+v", budget)
	}
}

func TestResidentOptimisticVersionConflict(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	resident := seedResident(t, store, now)
	first := resident.WithdrawConsent(now.Add(time.Hour))
	if err := store.UpdateResident(context.Background(), nil, first, resident.Version); err != nil {
		t.Fatal(err)
	}
	second := resident.WithdrawConsent(now.Add(2 * time.Hour))
	if err := store.UpdateResident(context.Background(), nil, second, resident.Version); err == nil {
		t.Fatal("stale resident update succeeded")
	}
	loaded, _ := store.ResidentByID(context.Background(), nil, resident.ID, resident.DistrictID)
	if loaded.Version != first.Version || !loaded.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("loaded = %+v", loaded)
	}
}

func TestConcurrentBudgetReservationsPreserveLimit(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			budget, err := store.DistrictBudget(context.Background(), nil, "district_1")
			if err != nil {
				results <- err
				return
			}
			results <- store.ReserveBudget(context.Background(), nil, "district_1", 700_000, budget.Version, now)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("successful reservations = %d", success)
	}
	budget, _ := store.DistrictBudget(context.Background(), nil, "district_1")
	if budget.ReservedCents != 700_000 || budget.ReservedCents+budget.SettledCents > budget.MonthlyCents {
		t.Fatalf("budget = %+v", budget)
	}
}
