package scheduler

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-challenge/day-18/internal/githubapi"
	"ai-challenge/day-18/internal/store"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	ticker *fakeTicker
}

func (c *fakeClock) Now() time.Time    { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Set(now time.Time) { c.mu.Lock(); c.now = now; c.mu.Unlock() }
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

type fakeGitHub struct {
	calls atomic.Int32
	fn    func(context.Context, githubapi.Query) (githubapi.Repository, error)
}

func (f *fakeGitHub) GetRepository(ctx context.Context, query githubapi.Query) (githubapi.Repository, error) {
	f.calls.Add(1)
	if f.fn != nil {
		return f.fn(ctx, query)
	}
	return githubapi.Repository{FullName: query.Owner + "/" + query.Repository, DefaultBranch: "main", Stars: 10,
		Forks: 2, OpenIssues: 3, Language: "Go", License: "Apache-2.0", UpdatedAt: "2026-09-26T10:00:00Z",
		PushedAt: "2026-09-26T09:00:00Z", RateLimitRemaining: 50, Source: "GitHub REST API"}, nil
}

func schedulerStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func scheduleAt(t *testing.T, s *store.Store, now time.Time, maxRuns *int64) store.Schedule {
	t.Helper()
	value, err := s.CreateSchedule(context.Background(), store.CreateParams{Owner: "modelcontextprotocol", Repository: "go-sdk", IntervalSeconds: 60, MaxRuns: maxRuns, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRunOnceExecutesDueButNotEarlyAndCalculatesNextRun(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	s := schedulerStore(t)
	created := scheduleAt(t, s, now, nil)
	clock := &fakeClock{now: now}
	github := &fakeGitHub{}
	planner := New(s, github)
	planner.Clock = clock
	if err := planner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if github.calls.Load() != 1 {
		t.Fatalf("GitHub calls = %d", github.calls.Load())
	}
	after, err := s.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantNext := now.Add(time.Minute)
	if after.NextRunAt == nil || !after.NextRunAt.Equal(wantNext) || after.RunCount != 1 || after.SnapshotCount != 1 {
		t.Fatalf("schedule after run = %+v", after)
	}
	clock.Set(now.Add(30 * time.Second))
	if err := planner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if github.calls.Load() != 1 {
		t.Fatalf("early execution made %d calls", github.calls.Load())
	}
}

func TestRunOnceRecoversOverdueScheduleWithoutRapidCatchupAndHonorsMaxRuns(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	maxRuns := int64(2)
	s := schedulerStore(t)
	created := scheduleAt(t, s, start, &maxRuns)
	clock := &fakeClock{now: start.Add(10 * time.Minute)}
	github := &fakeGitHub{}
	planner := New(s, github)
	planner.Clock = clock
	if err := planner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	afterFirst, _ := s.GetSchedule(ctx, created.ID)
	if afterFirst.NextRunAt == nil || !afterFirst.NextRunAt.Equal(start.Add(11*time.Minute)) || github.calls.Load() != 1 {
		t.Fatalf("overdue next run = %+v, calls=%d", afterFirst, github.calls.Load())
	}
	clock.Set(start.Add(11 * time.Minute))
	if err := planner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	completed, _ := s.GetSchedule(ctx, created.ID)
	if completed.Status != store.StatusCompleted || completed.NextRunAt != nil || completed.RunCount != 2 {
		t.Fatalf("max-runs schedule = %+v", completed)
	}
}

func TestRunOnceRecordsGitHubErrorAndRateLimitBackoff(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	s := schedulerStore(t)
	created := scheduleAt(t, s, now, nil)
	reset := now.Add(10 * time.Minute)
	github := &fakeGitHub{fn: func(context.Context, githubapi.Query) (githubapi.Repository, error) {
		return githubapi.Repository{}, &githubapi.APIError{StatusCode: http.StatusForbidden, Message: "rate limited", Kind: githubapi.ErrRateLimit, RateLimitReset: "1790417400"}
	}}
	// Keep the assertion independent of the literal epoch above.
	github.fn = func(context.Context, githubapi.Query) (githubapi.Repository, error) {
		return githubapi.Repository{}, &githubapi.APIError{StatusCode: http.StatusForbidden, Message: "rate limited", Kind: githubapi.ErrRateLimit, RateLimitReset: formatEpoch(reset)}
	}
	planner := New(s, github)
	planner.Clock = &fakeClock{now: now}
	if err := planner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetSchedule(ctx, created.ID)
	if after.ErrorCount != 1 || after.RunCount != 1 || after.LastError == "" || after.NextRunAt == nil || after.NextRunAt.Before(reset) {
		t.Fatalf("rate-limit state = %+v", after)
	}
}

func TestRunOnceAppliesTimeoutAndRunStopsOnCancellation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	s := schedulerStore(t)
	created := scheduleAt(t, s, now, nil)
	github := &fakeGitHub{fn: func(ctx context.Context, _ githubapi.Query) (githubapi.Repository, error) {
		<-ctx.Done()
		return githubapi.Repository{}, ctx.Err()
	}}
	planner := New(s, github)
	planner.Clock = &fakeClock{now: now}
	planner.RunTimeout = 5 * time.Millisecond
	if err := planner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetSchedule(ctx, created.ID)
	if after.ErrorCount != 1 || after.LastError != context.DeadlineExceeded.Error() {
		t.Fatalf("timeout was not recorded: %+v", after)
	}

	clock := &fakeClock{now: now}
	stopPlanner := New(s, &fakeGitHub{})
	stopPlanner.Clock = clock
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- stopPlanner.Run(runCtx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func formatEpoch(value time.Time) string {
	return strconv.FormatInt(value.UTC().Unix(), 10)
}
