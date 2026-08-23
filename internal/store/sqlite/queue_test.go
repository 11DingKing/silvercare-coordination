package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

func TestOutboxLeaseRetryAndDelivery(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	event := domain.OutboxEvent{ID: "event_1", DistrictID: "district_1", Topic: "resident.created", AggregateType: "resident", AggregateID: "resident_1", PayloadJSON: "{}", AvailableAt: now, CreatedAt: now}
	if err := store.EnqueueOutbox(context.Background(), nil, event); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOutbox(context.Background(), "worker_a", now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != domain.OutboxProcessing || claimed.Attempts != 1 {
		t.Fatalf("claimed=%+v", claimed)
	}
	if _, err := store.ClaimOutbox(context.Background(), "worker_b", now, 30*time.Second); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("concurrent claim err=%v", err)
	}
	if err := store.RetryOutbox(context.Background(), claimed.ID, "worker_a", "temporary", now.Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOutbox(context.Background(), "worker_b", now, 30*time.Second); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("early retry claim err=%v", err)
	}
	claimed, err = store.ClaimOutbox(context.Background(), "worker_b", now.Add(time.Minute), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkOutboxDelivered(context.Background(), claimed.ID, "worker_b", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := store.db.QueryRow("SELECT state FROM outbox_events WHERE id='event_1'").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "delivered" {
		t.Fatalf("state=%s", state)
	}
}

func TestWorkerJobRetryThenSuccess(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	job := domain.WorkerJob{ID: "job_1", DistrictID: "district_1", Kind: "follow_up", ObjectID: "resident_1", PayloadJSON: "{}", MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	if err := store.EnqueueJob(context.Background(), nil, job); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimJob(context.Background(), "worker_a", now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Attempts != 1 || claimed.State != domain.JobRunning {
		t.Fatalf("claimed=%+v", claimed)
	}
	if err := store.FailJob(context.Background(), claimed, "worker_a", "temporary", now.Add(time.Second), now); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimJob(context.Background(), "worker_b", now.Add(time.Second), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Attempts != 2 {
		t.Fatalf("attempts=%d", claimed.Attempts)
	}
	if err := store.CompleteJob(context.Background(), claimed.ID, "worker_b", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.JobByID(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != domain.JobSucceeded {
		t.Fatalf("state=%s", loaded.State)
	}
}

func TestIdempotencyScopesOperationAndRequestHash(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	record := IdempotencyRecord{DistrictID: "district_1", ActorID: "user_1", Operation: "create_visit", Key: "key_1", RequestHash: "hash_a", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
	_, created, err := store.BeginIdempotency(context.Background(), nil, record)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	_, created, err = store.BeginIdempotency(context.Background(), nil, record)
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	changed := record
	changed.RequestHash = "hash_b"
	if _, _, err := store.BeginIdempotency(context.Background(), nil, changed); err == nil {
		t.Fatal("key reused with another request hash")
	}
	record.ResponseCode = 201
	record.ResponseJSON = `{"id":"visit_1"}`
	record.UpdatedAt = now.Add(time.Second)
	if err := store.CompleteIdempotency(context.Background(), nil, record); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Idempotency(context.Background(), nil, record.DistrictID, record.ActorID, record.Operation, record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != "completed" || loaded.ResponseCode != 201 {
		t.Fatalf("loaded=%+v", loaded)
	}
}
