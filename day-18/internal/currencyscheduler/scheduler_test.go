package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-challenge/day-18/internal/currencystore"
	"ai-challenge/day-18/internal/frankfurter"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	ticker *fakeTicker
}

func (c *fakeClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Set(v time.Time) { c.mu.Lock(); c.now = v; c.mu.Unlock() }
func (c *fakeClock) NewTicker(time.Duration) Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ticker == nil {
		c.ticker = &fakeTicker{ch: make(chan time.Time, 1)}
	}
	return c.ticker
}

type fakeTicker struct{ ch chan time.Time }

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (*fakeTicker) Stop()                 {}

type fakeRates struct {
	calls atomic.Int32
	fn    func(context.Context, string, string) (frankfurter.Rate, error)
}

func (f *fakeRates) GetRate(ctx context.Context, b, q string) (frankfurter.Rate, error) {
	f.calls.Add(1)
	if f.fn != nil {
		return f.fn(ctx, b, q)
	}
	return frankfurter.Rate{Date: "2026-09-26", Base: b, Quote: q, Value: "0.92", Source: frankfurter.Source, Fetched: time.Date(2026, 9, 27, 10, 0, 1, 0, time.UTC)}, nil
}
func (*fakeRates) ValidatePair(context.Context, string, string) error { return nil }
func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func schedule(t *testing.T, s *store.Store, now time.Time, max *int64) store.Schedule {
	t.Helper()
	v, err := s.CreateSchedule(context.Background(), store.CreateParams{BaseCurrency: "USD", QuoteCurrency: "EUR", IntervalSeconds: 60, MaxRuns: max, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestImmediateRunNoCatchupAndMaxRuns(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	max := int64(2)
	db := testStore(t)
	created := schedule(t, db, start, &max)
	clock := &fakeClock{now: start.Add(10 * time.Minute)}
	rates := &fakeRates{}
	runner := New(db, rates)
	runner.Clock = clock
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := db.GetSchedule(ctx, created.ID)
	if first.NextRunAt == nil || !first.NextRunAt.Equal(start.Add(11*time.Minute)) || rates.calls.Load() != 1 {
		t.Fatalf("first=%+v calls=%d", first, rates.calls.Load())
	}
	clock.Set(start.Add(11 * time.Minute))
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	completed, _ := db.GetSchedule(ctx, created.ID)
	if completed.Status != store.StatusCompleted || completed.RunCount != 2 || completed.NextRunAt != nil {
		t.Fatalf("completed=%+v", completed)
	}
}

func TestSchedulerRecordsAPIErrorTimeoutAndStops(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	db := testStore(t)
	created := schedule(t, db, now, nil)
	rates := &fakeRates{fn: func(context.Context, string, string) (frankfurter.Rate, error) {
		return frankfurter.Rate{}, errors.New("API down")
	}}
	runner := New(db, rates)
	runner.Clock = &fakeClock{now: now}
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetSchedule(ctx, created.ID)
	if after.ErrorCount != 1 || after.LastError == "" {
		t.Fatalf("after=%+v", after)
	}

	created = schedule(t, db, now, nil)
	blocking := &fakeRates{fn: func(ctx context.Context, _, _ string) (frankfurter.Rate, error) {
		<-ctx.Done()
		return frankfurter.Rate{}, ctx.Err()
	}}
	runner = New(db, blocking)
	runner.Clock = &fakeClock{now: now}
	runner.RunTimeout = time.Millisecond
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ = db.GetSchedule(ctx, created.ID)
	if after.LastError != context.DeadlineExceeded.Error() {
		t.Fatalf("timeout=%+v", after)
	}

	stop := New(db, &fakeRates{})
	stop.Clock = &fakeClock{now: now}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- stop.Run(runCtx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}
