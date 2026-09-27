package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"ai-challenge/day-18/internal/githubapi"
	"ai-challenge/day-18/internal/store"
)

type RepositoryStore interface {
	RecoverInterrupted(context.Context, time.Time) (int, error)
	DueScheduleIDs(context.Context, time.Time, int) ([]string, error)
	ClaimDue(context.Context, string, time.Time) (*store.ClaimedRun, error)
	CompleteSuccess(context.Context, store.ClaimedRun, store.Snapshot, time.Time) error
	CompleteFailure(context.Context, store.ClaimedRun, error, time.Time, *time.Time) error
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type Clock interface {
	Now() time.Time
	NewTicker(time.Duration) Ticker
}

type realClock struct{}

func (realClock) Now() time.Time                   { return time.Now().UTC() }
func (realClock) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }

type Scheduler struct {
	Store        RepositoryStore
	GitHub       githubapi.Service
	Clock        Clock
	Workers      int
	PollInterval time.Duration
	RunTimeout   time.Duration
	OnError      func(error)
}

func New(storage RepositoryStore, github githubapi.Service) *Scheduler {
	return &Scheduler{
		Store: storage, GitHub: github, Clock: realClock{}, Workers: 2,
		PollInterval: time.Second, RunTimeout: 15 * time.Second,
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	if _, err := s.Store.RecoverInterrupted(ctx, s.Clock.Now()); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("recover interrupted runs: %w", err)
	}
	jobs := make(chan string, s.Workers*2)
	var workers sync.WaitGroup
	for range s.Workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				if err := s.execute(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
					s.report(err)
				}
			}
		}()
	}
	dispatch := func() {
		ids, err := s.Store.DueScheduleIDs(ctx, s.Clock.Now(), s.Workers*4)
		if err != nil {
			s.report(err)
			return
		}
		for _, id := range ids {
			select {
			case jobs <- id:
			case <-ctx.Done():
				return
			default:
				return
			}
		}
	}
	dispatch()
	ticker := s.Clock.NewTicker(s.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return nil
		case <-ticker.C():
			dispatch()
		}
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	ids, err := s.Store.DueScheduleIDs(ctx, s.Clock.Now(), s.Workers*4)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.execute(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) execute(ctx context.Context, id string) error {
	claimed, err := s.Store.ClaimDue(ctx, id, s.Clock.Now())
	if err != nil || claimed == nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, s.RunTimeout)
	repository, apiErr := s.GitHub.GetRepository(runCtx, githubapi.Query{
		Owner: claimed.Schedule.Owner, Repository: claimed.Schedule.Repository,
	})
	cancel()
	finished := s.Clock.Now()
	if apiErr != nil {
		var retryAt *time.Time
		if value, ok := rateLimitRetryAt(apiErr, finished); ok {
			retryAt = &value
		}
		if err := s.Store.CompleteFailure(context.WithoutCancel(ctx), *claimed, apiErr, finished, retryAt); err != nil {
			return fmt.Errorf("record failed run %d: %w", claimed.RunID, err)
		}
		return nil
	}
	snapshot := store.Snapshot{
		FetchedAt: finished.UTC().Format(time.RFC3339Nano), Stars: repository.Stars, Forks: repository.Forks,
		OpenIssues: repository.OpenIssues, DefaultBranch: repository.DefaultBranch, Archived: repository.Archived,
		Language: repository.Language, License: repository.License, GitHubUpdatedAt: repository.UpdatedAt,
		GitHubPushedAt: repository.PushedAt, RateLimitRemaining: repository.RateLimitRemaining, Source: repository.Source,
	}
	if err := s.Store.CompleteSuccess(context.WithoutCancel(ctx), *claimed, snapshot, finished); err != nil {
		return fmt.Errorf("record successful run %d: %w", claimed.RunID, err)
	}
	return nil
}

func (s *Scheduler) validate() error {
	if s.Store == nil || s.GitHub == nil {
		return errors.New("scheduler dependencies are not configured")
	}
	if s.Clock == nil {
		s.Clock = realClock{}
	}
	if s.Workers < 1 {
		s.Workers = 1
	}
	if s.PollInterval <= 0 {
		s.PollInterval = time.Second
	}
	if s.RunTimeout <= 0 {
		s.RunTimeout = 15 * time.Second
	}
	return nil
}

func (s *Scheduler) report(err error) {
	if s.OnError != nil {
		s.OnError(err)
	}
}

func rateLimitRetryAt(err error, now time.Time) (time.Time, bool) {
	if !errors.Is(err, githubapi.ErrRateLimit) {
		return time.Time{}, false
	}
	retry := now.UTC().Add(time.Minute)
	var apiErr *githubapi.APIError
	if errors.As(err, &apiErr) {
		if seconds, parseErr := strconv.ParseInt(apiErr.RetryAfter, 10, 64); parseErr == nil && seconds > 0 {
			candidate := now.UTC().Add(time.Duration(seconds) * time.Second)
			if candidate.After(retry) {
				retry = candidate
			}
		}
		if epoch, parseErr := strconv.ParseInt(apiErr.RateLimitReset, 10, 64); parseErr == nil {
			candidate := time.Unix(epoch, 0).UTC()
			if candidate.After(retry) {
				retry = candidate
			}
		}
	}
	return retry, true
}
