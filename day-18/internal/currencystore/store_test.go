package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-challenge/day-18/internal/frankfurter"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rates.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}
func createSchedule(t *testing.T, s *Store, now time.Time, maxRuns *int64) Schedule {
	t.Helper()
	value, err := s.CreateSchedule(context.Background(), CreateParams{BaseCurrency: "USD", QuoteCurrency: "EUR", IntervalSeconds: 60, MaxRuns: maxRuns, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCRUDPersistenceAndSQLiteConfiguration(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s, path := openTestStore(t)
	created := createSchedule(t, s, now, nil)
	if created.Status != StatusActive || created.NextRunAt == nil || !created.NextRunAt.Equal(now) {
		t.Fatalf("created=%+v", created)
	}
	items, err := s.ListSchedules(ctx, StatusActive)
	if err != nil || len(items) != 1 {
		t.Fatalf("list=%+v %v", items, err)
	}
	cancelled, already, err := s.CancelSchedule(ctx, created.ID, now.Add(time.Second))
	if err != nil || already || cancelled.Status != StatusCancelled {
		t.Fatalf("cancel=%+v %v %v", cancelled, already, err)
	}
	_, already, err = s.CancelSchedule(ctx, created.ID, now.Add(2*time.Second))
	if err != nil || !already {
		t.Fatalf("idempotent cancel=%v %v", already, err)
	}
	_ = s.Close()
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.GetSchedule(ctx, created.ID)
	if err != nil || persisted.Status != StatusCancelled {
		t.Fatalf("persisted=%+v %v", persisted, err)
	}
	var foreignKeys int
	_ = reopened.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys)
	var journal string
	_ = reopened.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal)
	if foreignKeys != 1 || journal != "wal" {
		t.Fatalf("foreign_keys=%d journal=%s", foreignKeys, journal)
	}
}

func TestAggregationForZeroOneAndSeveralSnapshots(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s, _ := openTestStore(t)
	schedule := createSchedule(t, s, now, nil)
	empty, err := s.Summary(ctx, schedule.ID)
	if err != nil || !empty.InsufficientData || empty.CurrentRate != "" {
		t.Fatalf("empty=%+v %v", empty, err)
	}
	run1, _ := s.ClaimDue(ctx, schedule.ID, now)
	if err := s.CompleteSuccess(ctx, *run1, snapshot("0.8000", "2026-09-26", now), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	one, err := s.Summary(ctx, schedule.ID)
	if err != nil || !one.InsufficientData || one.AbsoluteChange != "0" {
		t.Fatalf("one=%+v %v", one, err)
	}
	run2, _ := s.ClaimDue(ctx, schedule.ID, now.Add(time.Minute))
	_ = s.CompleteFailure(ctx, *run2, errors.New("upstream unavailable"), now.Add(time.Minute+time.Second))
	run3, _ := s.ClaimDue(ctx, schedule.ID, now.Add(2*time.Minute))
	_ = s.CompleteSuccess(ctx, *run3, snapshot("1.0000", "2026-09-27", now.Add(2*time.Minute)), now.Add(2*time.Minute+time.Second))
	many, err := s.Summary(ctx, schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if many.CurrentRate != "1.0000" || many.FirstRate != "0.8000" || many.AbsoluteChange != "0.2" || many.PercentChange != "25" || many.MinimumRate != "0.8" || many.MaximumRate != "1" {
		t.Fatalf("summary=%+v", many)
	}
	if many.Schedule.RunCount != 3 || many.Schedule.SuccessCount != 2 || many.Schedule.ErrorCount != 1 || many.Schedule.SnapshotCount != 2 {
		t.Fatalf("counts=%+v", many.Schedule)
	}
}

func TestConcurrentClaimAndUniqueScheduledRun(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s, _ := openTestStore(t)
	schedule := createSchedule(t, s, now, nil)
	var claimed atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := s.ClaimDue(context.Background(), schedule.ID, now)
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
		t.Fatal(err)
	}
	if claimed.Load() != 1 {
		t.Fatalf("claims=%d", claimed.Load())
	}
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE schedule_id=?`, schedule.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("run rows=%d", count)
	}
}

func TestRecoveryAndMaxRuns(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	maxRuns := int64(1)
	s, _ := openTestStore(t)
	schedule := createSchedule(t, s, now, &maxRuns)
	run, err := s.ClaimDue(ctx, schedule.ID, now)
	if err != nil || run == nil {
		t.Fatalf("claim=%+v %v", run, err)
	}
	count, err := s.RecoverInterrupted(ctx, now.Add(time.Second))
	if err != nil || count != 1 {
		t.Fatalf("recover=%d %v", count, err)
	}
	completed, _ := s.GetSchedule(ctx, schedule.ID)
	if completed.Status != StatusCompleted || completed.RunCount != 1 || completed.ErrorCount != 1 || completed.NextRunAt != nil {
		t.Fatalf("completed=%+v", completed)
	}
}

func snapshot(rate, date string, at time.Time) Snapshot {
	return Snapshot{Rate: rate, RateDate: date, FetchedAt: at.UTC().Format(time.RFC3339Nano), Source: frankfurter.Source}
}
