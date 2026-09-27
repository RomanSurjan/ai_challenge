package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "monitor.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func createTestSchedule(t *testing.T, s *Store, now time.Time, maxRuns *int64) Schedule {
	t.Helper()
	created, err := s.CreateSchedule(context.Background(), CreateParams{
		Owner: "modelcontextprotocol", Repository: "go-sdk", IntervalSeconds: 60, MaxRuns: maxRuns, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	return created
}

func TestStoreMigrationsCreateListCancelAndPersist(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	s, path := openTestStore(t)
	created := createTestSchedule(t, s, now, nil)
	if created.ID == "" || created.Status != StatusActive || created.NextRunAt == nil || !created.NextRunAt.Equal(now) {
		t.Fatalf("created schedule = %+v", created)
	}
	loaded, err := s.GetSchedule(ctx, created.ID)
	if err != nil || loaded.Repository != "go-sdk" {
		t.Fatalf("GetSchedule() = %+v, %v", loaded, err)
	}
	items, err := s.ListSchedules(ctx, StatusActive)
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("ListSchedules() = %+v, %v", items, err)
	}
	cancelled, already, err := s.CancelSchedule(ctx, created.ID, now.Add(time.Second))
	if err != nil || already || cancelled.Status != StatusCancelled || cancelled.NextRunAt != nil {
		t.Fatalf("CancelSchedule() = %+v, %v, %v", cancelled, already, err)
	}
	_, already, err = s.CancelSchedule(ctx, created.ID, now.Add(2*time.Second))
	if err != nil || !already {
		t.Fatalf("second CancelSchedule() already=%v err=%v", already, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen/migrate error = %v", err)
	}
	defer reopened.Close()
	persisted, err := reopened.GetSchedule(ctx, created.ID)
	if err != nil || persisted.Status != StatusCancelled {
		t.Fatalf("persisted schedule = %+v, %v", persisted, err)
	}
	var foreignKeys int
	if err := reopened.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, %v", foreignKeys, err)
	}
	var journal string
	if err := reopened.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal_mode = %q, %v", journal, err)
	}
}

func TestStoreRunSnapshotsAndAggregation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	s, _ := openTestStore(t)
	created := createTestSchedule(t, s, now, nil)

	empty, err := s.Summary(ctx, created.ID)
	if err != nil || empty.Schedule.SnapshotCount != 0 || !empty.InsufficientData {
		t.Fatalf("empty Summary() = %+v, %v", empty, err)
	}
	run1, err := s.ClaimDue(ctx, created.ID, now)
	if err != nil || run1 == nil {
		t.Fatalf("ClaimDue(first) = %+v, %v", run1, err)
	}
	if err := s.CompleteSuccess(ctx, *run1, testSnapshot(now, 10, 2, 3), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	one, err := s.Summary(ctx, created.ID)
	if err != nil || one.Schedule.SnapshotCount != 1 || !one.InsufficientData || one.Stars.Delta != 0 {
		t.Fatalf("one-snapshot Summary() = %+v, %v", one, err)
	}

	run2, err := s.ClaimDue(ctx, created.ID, now.Add(60*time.Second))
	if err != nil || run2 == nil {
		t.Fatalf("ClaimDue(second) = %+v, %v", run2, err)
	}
	if err := s.CompleteFailure(ctx, *run2, errors.New("temporary network error"), now.Add(61*time.Second), nil); err != nil {
		t.Fatal(err)
	}
	run3, err := s.ClaimDue(ctx, created.ID, now.Add(120*time.Second))
	if err != nil || run3 == nil {
		t.Fatalf("ClaimDue(third) = %+v, %v", run3, err)
	}
	if err := s.CompleteSuccess(ctx, *run3, testSnapshot(now.Add(120*time.Second), 14, 4, 1), now.Add(121*time.Second)); err != nil {
		t.Fatal(err)
	}

	many, err := s.Summary(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if many.Schedule.RunCount != 3 || many.Schedule.SuccessCount != 2 || many.Schedule.ErrorCount != 1 || many.Schedule.SnapshotCount != 2 {
		t.Fatalf("run counters = %+v", many.Schedule)
	}
	if many.InsufficientData || many.Stars.Latest != 14 || many.Stars.Delta != 4 || many.Stars.Min != 10 || many.Stars.Max != 14 || many.Forks.Delta != 2 || many.OpenIssues.Delta != -2 {
		t.Fatalf("aggregate metrics = %+v", many)
	}
	if many.LastSuccessfulSnapshot == nil || many.LastSuccessfulSnapshot.Stars != 14 || many.Schedule.LastError != "" {
		t.Fatalf("last snapshot/error = %+v", many)
	}
}

func TestStoreConcurrentClaimIsUnique(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	s, _ := openTestStore(t)
	created := createTestSchedule(t, s, now, nil)
	var claimed atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := s.ClaimDue(ctx, created.ID, now)
			if err != nil {
				errs <- err
				return
			}
			if run != nil {
				claimed.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ClaimDue() error = %v", err)
	}
	if claimed.Load() != 1 {
		t.Fatalf("successful claims = %d, want 1", claimed.Load())
	}
}

func TestStoreMaxRunsCompletesAndRecoveryRecordsInterruptedRun(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	maxRuns := int64(1)
	s, _ := openTestStore(t)
	created := createTestSchedule(t, s, now, &maxRuns)
	run, err := s.ClaimDue(ctx, created.ID, now)
	if err != nil || run == nil {
		t.Fatalf("ClaimDue() = %+v, %v", run, err)
	}
	count, err := s.RecoverInterrupted(ctx, now.Add(time.Second))
	if err != nil || count != 1 {
		t.Fatalf("RecoverInterrupted() = %d, %v", count, err)
	}
	completed, err := s.GetSchedule(ctx, created.ID)
	if err != nil || completed.Status != StatusCompleted || completed.RunCount != 1 || completed.ErrorCount != 1 || completed.NextRunAt != nil {
		t.Fatalf("completed schedule = %+v, %v", completed, err)
	}
}

func testSnapshot(at time.Time, stars, forks, issues int) Snapshot {
	return Snapshot{FetchedAt: at.UTC().Format(time.RFC3339Nano), Stars: stars, Forks: forks, OpenIssues: issues,
		DefaultBranch: "main", Language: "Go", License: "Apache-2.0", GitHubUpdatedAt: at.UTC().Format(time.RFC3339),
		GitHubPushedAt: at.UTC().Format(time.RFC3339), RateLimitRemaining: 50, Source: "GitHub REST API"}
}
