package worker

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

type jobRepo struct {
	mu        sync.Mutex
	jobs      []domain.WorkerJob
	completed []string
	failed    []string
}

func (r *jobRepo) ClaimJob(_ context.Context, worker string, now time.Time, lease time.Duration) (domain.WorkerJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.jobs) == 0 {
		return domain.WorkerJob{}, sql.ErrNoRows
	}
	job := r.jobs[0]
	r.jobs = r.jobs[1:]
	job.Attempts++
	job.State = domain.JobRunning
	job.LockedBy = worker
	until := now.Add(lease)
	job.LockedUntil = &until
	return job, nil
}
func (r *jobRepo) CompleteJob(_ context.Context, id, worker string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed = append(r.completed, id+":"+worker)
	return nil
}
func (r *jobRepo) FailJob(_ context.Context, job domain.WorkerJob, worker, message string, _ time.Time, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, job.ID+":"+worker+":"+message)
	return nil
}
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunnerDispatchesAndCompletesJob(t *testing.T) {
	now := time.Now().UTC()
	repo := &jobRepo{jobs: []domain.WorkerJob{{ID: "job_1", Kind: "follow_up", MaxAttempts: 3}}}
	runner := NewRunner(repo, clock.NewFixed(now), discardLogger(), "worker_1", time.Millisecond)
	handled := false
	if err := runner.Register("follow_up", HandlerFunc(func(_ context.Context, job domain.WorkerJob) error {
		handled = true
		if job.ID != "job_1" {
			t.Fatalf("job=%+v", job)
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !handled || len(repo.completed) != 1 || len(repo.failed) != 0 {
		t.Fatalf("handled=%v complete=%v failed=%v", handled, repo.completed, repo.failed)
	}
}

func TestRunnerRetriesHandlerFailure(t *testing.T) {
	now := time.Now().UTC()
	repo := &jobRepo{jobs: []domain.WorkerJob{{ID: "job_1", Kind: "follow_up", MaxAttempts: 3}}}
	runner := NewRunner(repo, clock.NewFixed(now), discardLogger(), "worker_1", time.Millisecond)
	sentinel := errors.New("temporary downstream failure")
	_ = runner.Register("follow_up", HandlerFunc(func(context.Context, domain.WorkerJob) error { return sentinel }))
	err := runner.DrainOne(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
	if len(repo.failed) != 1 || len(repo.completed) != 0 {
		t.Fatalf("complete=%v failed=%v", repo.completed, repo.failed)
	}
}

func TestRunnerFailsUnknownJobKind(t *testing.T) {
	now := time.Now().UTC()
	repo := &jobRepo{jobs: []domain.WorkerJob{{ID: "job_1", Kind: "unknown", MaxAttempts: 3}}}
	runner := NewRunner(repo, clock.NewFixed(now), discardLogger(), "worker_1", time.Millisecond)
	err := runner.DrainOne(context.Background())
	if err == nil {
		t.Fatal("unknown job succeeded")
	}
	if len(repo.failed) != 1 {
		t.Fatalf("failed=%v", repo.failed)
	}
}

func TestRunnerRegistrationRejectsDuplicates(t *testing.T) {
	runner := NewRunner(&jobRepo{}, clock.NewFixed(time.Now()), discardLogger(), "worker", time.Millisecond)
	handler := HandlerFunc(func(context.Context, domain.WorkerJob) error { return nil })
	if err := runner.Register("kind", handler); err != nil {
		t.Fatal(err)
	}
	if err := runner.Register("kind", handler); err == nil {
		t.Fatal("duplicate handler accepted")
	}
	if err := runner.Register("", handler); err == nil {
		t.Fatal("empty kind accepted")
	}
}

func TestRunnerRunStopsOnContextCancellation(t *testing.T) {
	runner := NewRunner(&jobRepo{}, clock.NewFixed(time.Now()), discardLogger(), "worker", time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestRetryBackoffCapsAtFiveMinutes(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{{0, time.Second}, {1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}, {10, 300 * time.Second}, {100, 300 * time.Second}}
	for _, test := range tests {
		if got := retryBackoff(test.attempt); got != test.want {
			t.Fatalf("attempt=%d got=%s want=%s", test.attempt, got, test.want)
		}
	}
}
