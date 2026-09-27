package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)

var ErrNotFound = errors.New("repository monitor not found")

type Store struct{ db *sql.DB }

type Schedule struct {
	ID              string     `json:"schedule_id"`
	Owner           string     `json:"owner"`
	Repository      string     `json:"repository"`
	IntervalSeconds int64      `json:"interval_seconds"`
	MaxRuns         *int64     `json:"max_runs,omitempty"`
	Status          string     `json:"status"`
	RunCount        int64      `json:"run_count"`
	SuccessCount    int64      `json:"successful_runs"`
	ErrorCount      int64      `json:"error_runs"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	SnapshotCount   int64      `json:"snapshot_count"`
}

type Snapshot struct {
	ID                 int64  `json:"snapshot_id"`
	FetchedAt          string `json:"fetched_at"`
	Stars              int    `json:"stars"`
	Forks              int    `json:"forks"`
	OpenIssues         int    `json:"open_issues"`
	DefaultBranch      string `json:"default_branch"`
	Archived           bool   `json:"archived"`
	Language           string `json:"language"`
	License            string `json:"license"`
	GitHubUpdatedAt    string `json:"github_updated_at"`
	GitHubPushedAt     string `json:"github_pushed_at"`
	RateLimitRemaining int    `json:"rate_limit_remaining"`
	Source             string `json:"source"`
}

type MetricRange struct {
	Latest int `json:"latest"`
	Delta  int `json:"delta"`
	Min    int `json:"min"`
	Max    int `json:"max"`
}

type Summary struct {
	Schedule               Schedule    `json:"schedule"`
	AggregationWindowFrom  string      `json:"aggregation_window_from,omitempty"`
	AggregationWindowTo    string      `json:"aggregation_window_to,omitempty"`
	LastSuccessfulSnapshot *Snapshot   `json:"last_successful_snapshot,omitempty"`
	Stars                  MetricRange `json:"stars"`
	Forks                  MetricRange `json:"forks"`
	OpenIssues             MetricRange `json:"open_issues"`
	InsufficientData       bool        `json:"insufficient_data"`
	Source                 string      `json:"source"`
	Text                   string      `json:"summary"`
}

type CreateParams struct {
	Owner           string
	Repository      string
	IntervalSeconds int64
	MaxRuns         *int64
	Now             time.Time
}

type ClaimedRun struct {
	RunID        int64
	ScheduledFor time.Time
	Schedule     Schedule
}

func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("database path is required")
	}
	dsnURL := &url.URL{Scheme: "file", Path: path}
	query := dsnURL.Query()
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"} {
		query.Add("_pragma", pragma)
	}
	dsnURL.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", dsnURL.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	s := &Store{db: db}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping SQLite: %w", err)
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS schedules (
  id TEXT PRIMARY KEY,
  owner TEXT NOT NULL,
  repository TEXT NOT NULL,
  interval_seconds INTEGER NOT NULL CHECK(interval_seconds BETWEEN 60 AND 2592000),
  max_runs INTEGER CHECK(max_runs IS NULL OR max_runs BETWEEN 1 AND 100000),
  status TEXT NOT NULL CHECK(status IN ('active','completed','cancelled')),
  run_count INTEGER NOT NULL DEFAULT 0,
  success_count INTEGER NOT NULL DEFAULT 0,
  error_count INTEGER NOT NULL DEFAULT 0,
  next_run_at TEXT,
  last_run_at TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  schedule_id TEXT NOT NULL REFERENCES schedules(id) ON DELETE RESTRICT,
  scheduled_for TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL CHECK(status IN ('running','success','error')),
  error TEXT,
  snapshot_id INTEGER,
  UNIQUE(schedule_id, scheduled_for)
);
CREATE TABLE IF NOT EXISTS snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  schedule_id TEXT NOT NULL REFERENCES schedules(id) ON DELETE RESTRICT,
  run_id INTEGER NOT NULL UNIQUE REFERENCES runs(id) ON DELETE RESTRICT,
  fetched_at TEXT NOT NULL,
  stars INTEGER NOT NULL CHECK(stars >= 0),
  forks INTEGER NOT NULL CHECK(forks >= 0),
  open_issues INTEGER NOT NULL CHECK(open_issues >= 0),
  default_branch TEXT NOT NULL,
  archived INTEGER NOT NULL CHECK(archived IN (0,1)),
  language TEXT NOT NULL,
  license TEXT NOT NULL,
  github_updated_at TEXT NOT NULL,
  github_pushed_at TEXT NOT NULL,
  rate_limit_remaining INTEGER NOT NULL,
  source TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS schedules_due_idx ON schedules(status, next_run_at);
CREATE INDEX IF NOT EXISTS runs_schedule_idx ON runs(schedule_id, scheduled_for);
CREATE INDEX IF NOT EXISTS snapshots_schedule_idx ON snapshots(schedule_id, fetched_at);
`
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func (s *Store) CreateSchedule(ctx context.Context, p CreateParams) (Schedule, error) {
	now := p.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	id, err := newID()
	if err != nil {
		return Schedule{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO schedules
    (id, owner, repository, interval_seconds, max_runs, status, next_run_at, created_at, updated_at)
    VALUES(?, ?, ?, ?, ?, 'active', ?, ?, ?)`,
		id, p.Owner, p.Repository, p.IntervalSeconds, nullableInt(p.MaxRuns), formatTime(now), formatTime(now), formatTime(now))
	if err != nil {
		return Schedule{}, fmt.Errorf("create schedule: %w", err)
	}
	return s.GetSchedule(ctx, id)
}

func (s *Store) GetSchedule(ctx context.Context, id string) (Schedule, error) {
	row := s.db.QueryRowContext(ctx, scheduleSelect+` WHERE s.id = ?`, id)
	return scanSchedule(row)
}

func (s *Store) ListSchedules(ctx context.Context, status string) ([]Schedule, error) {
	query := scheduleSelect
	args := []any{}
	if status != "" {
		query += ` WHERE s.status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY s.created_at, s.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	var result []Schedule
	for rows.Next() {
		item, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if result == nil {
		result = []Schedule{}
	}
	return result, rows.Err()
}

func (s *Store) CancelSchedule(ctx context.Context, id string, now time.Time) (Schedule, bool, error) {
	current, err := s.GetSchedule(ctx, id)
	if err != nil {
		return Schedule{}, false, err
	}
	if current.Status == StatusCancelled {
		return current, true, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE schedules SET status='cancelled', next_run_at=NULL, updated_at=? WHERE id=?`, formatTime(now.UTC()), id)
	if err != nil {
		return Schedule{}, false, fmt.Errorf("cancel schedule: %w", err)
	}
	updated, err := s.GetSchedule(ctx, id)
	return updated, false, err
}

func (s *Store) DueScheduleIDs(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM schedules WHERE status='active' AND next_run_at <= ? ORDER BY next_run_at, id LIMIT ?`, formatTime(now.UTC()), limit)
	if err != nil {
		return nil, fmt.Errorf("list due schedules: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) ClaimDue(ctx context.Context, id string, now time.Time) (*ClaimedRun, error) {
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, scheduleSelect+` WHERE s.id = ?`, id)
	schedule, err := scanSchedule(row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if schedule.Status != StatusActive || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
		return nil, nil
	}
	scheduledFor := schedule.NextRunAt.UTC()
	next := nextAfter(scheduledFor, time.Duration(schedule.IntervalSeconds)*time.Second, now)
	result, err := tx.ExecContext(ctx, `UPDATE schedules SET next_run_at=?, updated_at=? WHERE id=? AND status='active' AND next_run_at=?`,
		formatTime(next), formatTime(now), id, formatTime(scheduledFor))
	if err != nil {
		return nil, fmt.Errorf("claim schedule: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return nil, nil
	}
	insert, err := tx.ExecContext(ctx, `INSERT INTO runs(schedule_id, scheduled_for, started_at, status) VALUES(?, ?, ?, 'running')`,
		id, formatTime(scheduledFor), formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("create scheduled run: %w", err)
	}
	runID, err := insert.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	schedule.NextRunAt = &next
	return &ClaimedRun{RunID: runID, ScheduledFor: scheduledFor, Schedule: schedule}, nil
}

func (s *Store) CompleteSuccess(ctx context.Context, run ClaimedRun, snapshot Snapshot, finished time.Time) error {
	finished = finished.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO snapshots
    (schedule_id, run_id, fetched_at, stars, forks, open_issues, default_branch, archived, language, license, github_updated_at, github_pushed_at, rate_limit_remaining, source)
    VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.Schedule.ID, run.RunID, snapshot.FetchedAt, snapshot.Stars, snapshot.Forks, snapshot.OpenIssues,
		snapshot.DefaultBranch, snapshot.Archived, snapshot.Language, snapshot.License, snapshot.GitHubUpdatedAt,
		snapshot.GitHubPushedAt, snapshot.RateLimitRemaining, snapshot.Source)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	snapshotID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE runs SET status='success', finished_at=?, snapshot_id=? WHERE id=? AND status='running'`, formatTime(finished), snapshotID, run.RunID)
	if err != nil {
		return err
	}
	if n, _ := updated.RowsAffected(); n != 1 {
		return errors.New("scheduled run is no longer active")
	}
	if err := updateScheduleAfterRun(ctx, tx, run.Schedule.ID, true, "", finished, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CompleteFailure(ctx context.Context, run ClaimedRun, runErr error, finished time.Time, retryAt *time.Time) error {
	finished = finished.UTC()
	message := truncateError(runErr)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	updated, err := tx.ExecContext(ctx, `UPDATE runs SET status='error', finished_at=?, error=? WHERE id=? AND status='running'`, formatTime(finished), message, run.RunID)
	if err != nil {
		return err
	}
	if n, _ := updated.RowsAffected(); n != 1 {
		return errors.New("scheduled run is no longer active")
	}
	if err := updateScheduleAfterRun(ctx, tx, run.Schedule.ID, false, message, finished, retryAt); err != nil {
		return err
	}
	return tx.Commit()
}

func updateScheduleAfterRun(ctx context.Context, tx *sql.Tx, id string, success bool, message string, finished time.Time, retryAt *time.Time) error {
	successInc, errorInc := 0, 1
	if success {
		successInc, errorInc = 1, 0
	}
	nextOverride := any(nil)
	if retryAt != nil {
		nextOverride = formatTime(retryAt.UTC())
	}
	_, err := tx.ExecContext(ctx, `UPDATE schedules SET
    run_count=run_count+1,
    success_count=success_count+?,
    error_count=error_count+?,
    last_run_at=?,
    last_error=?,
    status=CASE WHEN status='cancelled' THEN 'cancelled' WHEN max_runs IS NOT NULL AND run_count+1 >= max_runs THEN 'completed' ELSE status END,
    next_run_at=CASE WHEN status='cancelled' OR (max_runs IS NOT NULL AND run_count+1 >= max_runs) THEN NULL
      WHEN ? IS NOT NULL AND (next_run_at IS NULL OR next_run_at < ?) THEN ? ELSE next_run_at END,
    updated_at=?
    WHERE id=?`, successInc, errorInc, formatTime(finished), nullableString(message), nextOverride, nextOverride, nextOverride, formatTime(finished), id)
	return err
}

func (s *Store) RecoverInterrupted(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, schedule_id, scheduled_for FROM runs WHERE status='running' ORDER BY id`)
	if err != nil {
		return 0, err
	}
	var interrupted []ClaimedRun
	for rows.Next() {
		var run ClaimedRun
		var scheduled string
		if err := rows.Scan(&run.RunID, &run.Schedule.ID, &scheduled); err != nil {
			rows.Close()
			return 0, err
		}
		run.ScheduledFor, err = parseTime(scheduled)
		if err != nil {
			rows.Close()
			return 0, err
		}
		interrupted = append(interrupted, run)
	}
	rows.Close()
	for _, run := range interrupted {
		if err := s.CompleteFailure(ctx, run, errors.New("service restarted during scheduled run"), now, nil); err != nil {
			return 0, err
		}
	}
	return len(interrupted), nil
}

func (s *Store) Summary(ctx context.Context, id string) (Summary, error) {
	schedule, err := s.GetSchedule(ctx, id)
	if err != nil {
		return Summary{}, err
	}
	result := Summary{Schedule: schedule, InsufficientData: schedule.SnapshotCount < 2, Source: "GitHub REST API"}
	if schedule.SnapshotCount == 0 {
		result.Text = fmt.Sprintf("Мониторинг %s/%s пока не содержит успешных снимков.", schedule.Owner, schedule.Repository)
		return result, nil
	}
	var first, last Snapshot
	if err := scanSnapshot(s.db.QueryRowContext(ctx, snapshotSelect+` WHERE schedule_id=? ORDER BY fetched_at, id LIMIT 1`, id), &first); err != nil {
		return Summary{}, err
	}
	if err := scanSnapshot(s.db.QueryRowContext(ctx, snapshotSelect+` WHERE schedule_id=? ORDER BY fetched_at DESC, id DESC LIMIT 1`, id), &last); err != nil {
		return Summary{}, err
	}
	var minStars, maxStars, minForks, maxForks, minIssues, maxIssues int
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(stars), MAX(stars), MIN(forks), MAX(forks), MIN(open_issues), MAX(open_issues) FROM snapshots WHERE schedule_id=?`, id).
		Scan(&minStars, &maxStars, &minForks, &maxForks, &minIssues, &maxIssues); err != nil {
		return Summary{}, err
	}
	result.AggregationWindowFrom = first.FetchedAt
	result.AggregationWindowTo = last.FetchedAt
	result.LastSuccessfulSnapshot = &last
	result.Stars = MetricRange{Latest: last.Stars, Delta: last.Stars - first.Stars, Min: minStars, Max: maxStars}
	result.Forks = MetricRange{Latest: last.Forks, Delta: last.Forks - first.Forks, Min: minForks, Max: maxForks}
	result.OpenIssues = MetricRange{Latest: last.OpenIssues, Delta: last.OpenIssues - first.OpenIssues, Min: minIssues, Max: maxIssues}
	result.Text = fmt.Sprintf("Мониторинг %s/%s: %d снимков, успешных запусков %d, ошибок %d; звёзды %d (%+d), форки %d (%+d), открытые задачи %d (%+d).",
		schedule.Owner, schedule.Repository, schedule.SnapshotCount, schedule.SuccessCount, schedule.ErrorCount,
		last.Stars, result.Stars.Delta, last.Forks, result.Forks.Delta, last.OpenIssues, result.OpenIssues.Delta)
	return result, nil
}

const scheduleSelect = `SELECT s.id, s.owner, s.repository, s.interval_seconds, s.max_runs, s.status,
  s.run_count, s.success_count, s.error_count, s.next_run_at, s.last_run_at, COALESCE(s.last_error,''),
  s.created_at, s.updated_at, (SELECT COUNT(*) FROM snapshots p WHERE p.schedule_id=s.id)
  FROM schedules s`

type rowScanner interface{ Scan(...any) error }

func scanSchedule(row rowScanner) (Schedule, error) {
	var s Schedule
	var maxRuns sql.NullInt64
	var next, last sql.NullString
	var created, updated string
	err := row.Scan(&s.ID, &s.Owner, &s.Repository, &s.IntervalSeconds, &maxRuns, &s.Status,
		&s.RunCount, &s.SuccessCount, &s.ErrorCount, &next, &last, &s.LastError, &created, &updated, &s.SnapshotCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("scan schedule: %w", err)
	}
	if maxRuns.Valid {
		s.MaxRuns = &maxRuns.Int64
	}
	if next.Valid {
		value, err := parseTime(next.String)
		if err != nil {
			return Schedule{}, err
		}
		s.NextRunAt = &value
	}
	if last.Valid {
		value, err := parseTime(last.String)
		if err != nil {
			return Schedule{}, err
		}
		s.LastRunAt = &value
	}
	var errParse error
	if s.CreatedAt, errParse = parseTime(created); errParse != nil {
		return Schedule{}, errParse
	}
	if s.UpdatedAt, errParse = parseTime(updated); errParse != nil {
		return Schedule{}, errParse
	}
	return s, nil
}

const snapshotSelect = `SELECT id, fetched_at, stars, forks, open_issues, default_branch, archived, language, license,
 github_updated_at, github_pushed_at, rate_limit_remaining, source FROM snapshots`

func scanSnapshot(row rowScanner, p *Snapshot) error {
	return row.Scan(&p.ID, &p.FetchedAt, &p.Stars, &p.Forks, &p.OpenIssues, &p.DefaultBranch, &p.Archived,
		&p.Language, &p.License, &p.GitHubUpdatedAt, &p.GitHubPushedAt, &p.RateLimitRemaining, &p.Source)
}

func nextAfter(scheduled time.Time, interval time.Duration, now time.Time) time.Time {
	if scheduled.After(now) {
		return scheduled
	}
	steps := now.Sub(scheduled)/interval + 1
	return scheduled.Add(steps * interval).UTC()
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate schedule id: %w", err)
	}
	return "mon_" + hex.EncodeToString(value), nil
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse UTC timestamp %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

func nullableInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func truncateError(err error) string {
	if err == nil {
		return "unknown error"
	}
	value := strings.TrimSpace(err.Error())
	if len(value) > 1000 {
		value = value[:1000]
	}
	return value
}
