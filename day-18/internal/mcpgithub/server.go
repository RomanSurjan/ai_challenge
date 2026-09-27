package mcpgithub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ai-challenge/day-18/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName             = "day18-periodic-github-mcp"
	ServerVersion          = "1.0.0"
	ScheduleToolName       = "schedule_repository_monitor"
	SummaryToolName        = "get_repository_monitor_summary"
	ListToolName           = "list_repository_monitors"
	CancelToolName         = "cancel_repository_monitor"
	ownerPatternText       = `^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`
	repositoryPatternText  = `^[A-Za-z0-9._-]+$`
	scheduleIDPatternText  = `^mon_[a-f0-9]{32}$`
	minimumIntervalSeconds = int64(60)
	maximumIntervalSeconds = int64(2592000)
	maximumRuns            = int64(100000)
)

var (
	ownerPattern      = regexp.MustCompile(ownerPatternText)
	repositoryPattern = regexp.MustCompile(repositoryPatternText)
	scheduleIDPattern = regexp.MustCompile(scheduleIDPatternText)
)

type MonitorStore interface {
	CreateSchedule(context.Context, store.CreateParams) (store.Schedule, error)
	Summary(context.Context, string) (store.Summary, error)
	ListSchedules(context.Context, string) ([]store.Schedule, error)
	CancelSchedule(context.Context, string, time.Time) (store.Schedule, bool, error)
}

type ScheduleInput struct {
	Owner           string `json:"owner"`
	Repository      string `json:"repository"`
	IntervalSeconds int64  `json:"interval_seconds"`
	MaxRuns         *int64 `json:"max_runs,omitempty"`
}

type ScheduleIDInput struct {
	ScheduleID string `json:"schedule_id"`
}

type ListInput struct {
	Status string `json:"status,omitempty"`
}

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ScheduleOutput struct {
	OK              bool       `json:"ok"`
	ScheduleID      string     `json:"schedule_id,omitempty"`
	Status          string     `json:"status,omitempty"`
	Owner           string     `json:"owner,omitempty"`
	Repository      string     `json:"repository,omitempty"`
	IntervalSeconds int64      `json:"interval_seconds,omitempty"`
	MaxRuns         *int64     `json:"max_runs,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
	Message         string     `json:"message,omitempty"`
	Error           *ToolError `json:"error,omitempty"`
}

type SummaryOutput struct {
	OK                     bool              `json:"ok"`
	ScheduleID             string            `json:"schedule_id,omitempty"`
	Owner                  string            `json:"owner,omitempty"`
	Repository             string            `json:"repository,omitempty"`
	Status                 string            `json:"status,omitempty"`
	IntervalSeconds        int64             `json:"interval_seconds,omitempty"`
	MaxRuns                *int64            `json:"max_runs,omitempty"`
	CreatedAt              *time.Time        `json:"created_at,omitempty"`
	LastRunAt              *time.Time        `json:"last_run_at,omitempty"`
	NextRunAt              *time.Time        `json:"next_run_at,omitempty"`
	RunCount               int64             `json:"run_count"`
	SuccessfulRuns         int64             `json:"successful_runs"`
	ErrorRuns              int64             `json:"error_runs"`
	SnapshotCount          int64             `json:"snapshot_count"`
	AggregationWindowFrom  string            `json:"aggregation_window_from,omitempty"`
	AggregationWindowTo    string            `json:"aggregation_window_to,omitempty"`
	LastSuccessfulSnapshot *store.Snapshot   `json:"last_successful_snapshot,omitempty"`
	Stars                  store.MetricRange `json:"stars"`
	Forks                  store.MetricRange `json:"forks"`
	OpenIssues             store.MetricRange `json:"open_issues"`
	LastError              string            `json:"last_error,omitempty"`
	Source                 string            `json:"source,omitempty"`
	Summary                string            `json:"summary,omitempty"`
	InsufficientData       bool              `json:"insufficient_data"`
	Error                  *ToolError        `json:"error,omitempty"`
}

type ListOutput struct {
	OK       bool             `json:"ok"`
	Count    int              `json:"count"`
	Monitors []store.Schedule `json:"monitors"`
	Error    *ToolError       `json:"error,omitempty"`
}

type CancelOutput struct {
	OK               bool       `json:"ok"`
	ScheduleID       string     `json:"schedule_id,omitempty"`
	Status           string     `json:"status,omitempty"`
	AlreadyCancelled bool       `json:"already_cancelled"`
	SnapshotCount    int64      `json:"snapshot_count"`
	Message          string     `json:"message,omitempty"`
	Error            *ToolError `json:"error,omitempty"`
}

func NewServer(storage MonitorStore) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: ServerVersion},
		&mcp.ServerOptions{Instructions: "Manage persistent periodic monitors for public GitHub repositories. Schedules continue in the background and summaries are computed from SQLite snapshots."},
	)
	RegisterTools(server, storage)
	return server
}

// RegisterTools adds the repository-monitoring tools to an existing MCP server.
// It lets the unified day-18 server expose multiple independent domains through
// one Streamable HTTP endpoint and one agent connection.
func RegisterTools(server *mcp.Server, storage MonitorStore) {
	openWorld, closedWorld, nonDestructive := true, false, false

	mcp.AddTool(server, &mcp.Tool{
		Name: ScheduleToolName, Title: "Create repository monitor",
		Description: "Create a persistent periodic monitor for a public GitHub repository. The first collection is due immediately; subsequent runs continue without an open browser. Minimum interval is 60 seconds.",
		InputSchema: scheduleInputSchema(), OutputSchema: scheduleOutputSchema(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false, DestructiveHint: &nonDestructive, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ScheduleInput) (*mcp.CallToolResult, ScheduleOutput, error) {
		input.Owner = strings.TrimSpace(input.Owner)
		input.Repository = strings.TrimSpace(input.Repository)
		if err := validateScheduleInput(input); err != nil {
			return toolFailure(), ScheduleOutput{OK: false, Error: errorDetail("invalid_argument", err)}, nil
		}
		created, err := storage.CreateSchedule(ctx, store.CreateParams{
			Owner: input.Owner, Repository: input.Repository, IntervalSeconds: input.IntervalSeconds,
			MaxRuns: input.MaxRuns, Now: time.Now().UTC(),
		})
		if err != nil {
			return toolFailure(), ScheduleOutput{OK: false, Error: errorDetail("storage_error", err)}, nil
		}
		return nil, scheduleOutput(created), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: SummaryToolName, Title: "Repository monitor summary",
		Description: "Return an aggregate computed from persisted successful GitHub snapshots for one schedule, including deltas, minima, maxima, run counts, timestamps, and the latest error.",
		InputSchema: scheduleIDInputSchema(), OutputSchema: summaryOutputSchema(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ScheduleIDInput) (*mcp.CallToolResult, SummaryOutput, error) {
		input.ScheduleID = strings.TrimSpace(input.ScheduleID)
		if !scheduleIDPattern.MatchString(input.ScheduleID) {
			return toolFailure(), SummaryOutput{OK: false, Error: errorDetail("invalid_argument", errors.New("schedule_id has an invalid format"))}, nil
		}
		summary, err := storage.Summary(ctx, input.ScheduleID)
		if err != nil {
			return toolFailure(), SummaryOutput{OK: false, Error: storeError(err)}, nil
		}
		return nil, summaryOutput(summary), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: ListToolName, Title: "List repository monitors",
		Description: "List persistent repository monitor schedules in deterministic creation order. Optionally filter by active, completed, or cancelled status.",
		InputSchema: listInputSchema(), OutputSchema: listOutputSchema(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, ListOutput, error) {
		input.Status = strings.TrimSpace(strings.ToLower(input.Status))
		if input.Status != "" && input.Status != store.StatusActive && input.Status != store.StatusCompleted && input.Status != store.StatusCancelled {
			return toolFailure(), ListOutput{OK: false, Monitors: []store.Schedule{}, Error: errorDetail("invalid_argument", errors.New("status must be active, completed, or cancelled"))}, nil
		}
		monitors, err := storage.ListSchedules(ctx, input.Status)
		if err != nil {
			return toolFailure(), ListOutput{OK: false, Monitors: []store.Schedule{}, Error: errorDetail("storage_error", err)}, nil
		}
		return nil, ListOutput{OK: true, Count: len(monitors), Monitors: monitors}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: CancelToolName, Title: "Cancel repository monitor",
		Description: "Stop future runs for a repository monitor while preserving every run, snapshot, and aggregate. Repeating cancellation is safe and returns the same cancelled state.",
		InputSchema: scheduleIDInputSchema(), OutputSchema: cancelOutputSchema(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true, DestructiveHint: &nonDestructive, OpenWorldHint: &closedWorld},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ScheduleIDInput) (*mcp.CallToolResult, CancelOutput, error) {
		input.ScheduleID = strings.TrimSpace(input.ScheduleID)
		if !scheduleIDPattern.MatchString(input.ScheduleID) {
			return toolFailure(), CancelOutput{OK: false, Error: errorDetail("invalid_argument", errors.New("schedule_id has an invalid format"))}, nil
		}
		cancelled, already, err := storage.CancelSchedule(ctx, input.ScheduleID, time.Now().UTC())
		if err != nil {
			return toolFailure(), CancelOutput{OK: false, Error: storeError(err)}, nil
		}
		message := "Future runs were cancelled; saved history remains available."
		if already {
			message = "Schedule was already cancelled; saved history remains available."
		}
		return nil, CancelOutput{OK: true, ScheduleID: cancelled.ID, Status: cancelled.Status, AlreadyCancelled: already, SnapshotCount: cancelled.SnapshotCount, Message: message}, nil
	})
}

func Handler(storage MonitorStore) http.Handler {
	mux := http.NewServeMux()
	server := NewServer(storage)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true},
	))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": ServerName})
	})
	return mux
}

func validateScheduleInput(input ScheduleInput) error {
	if input.Owner == "" || input.Repository == "" {
		return errors.New("owner and repository are required")
	}
	if !ownerPattern.MatchString(input.Owner) {
		return errors.New("owner must be a valid GitHub account name")
	}
	if len(input.Repository) > 100 || !repositoryPattern.MatchString(input.Repository) {
		return errors.New("repository must be a valid GitHub repository name")
	}
	if input.IntervalSeconds < minimumIntervalSeconds || input.IntervalSeconds > maximumIntervalSeconds {
		return fmt.Errorf("interval_seconds must be between %d and %d", minimumIntervalSeconds, maximumIntervalSeconds)
	}
	if input.MaxRuns != nil && (*input.MaxRuns < 1 || *input.MaxRuns > maximumRuns) {
		return fmt.Errorf("max_runs must be between 1 and %d", maximumRuns)
	}
	return nil
}

func scheduleOutput(value store.Schedule) ScheduleOutput {
	created := value.CreatedAt
	return ScheduleOutput{OK: true, ScheduleID: value.ID, Status: value.Status, Owner: value.Owner, Repository: value.Repository,
		IntervalSeconds: value.IntervalSeconds, MaxRuns: value.MaxRuns, NextRunAt: value.NextRunAt, CreatedAt: &created,
		Message: "Schedule created. The first background collection is due immediately."}
}

func summaryOutput(value store.Summary) SummaryOutput {
	s := value.Schedule
	created := s.CreatedAt
	return SummaryOutput{OK: true, ScheduleID: s.ID, Owner: s.Owner, Repository: s.Repository, Status: s.Status,
		IntervalSeconds: s.IntervalSeconds, MaxRuns: s.MaxRuns, CreatedAt: &created, LastRunAt: s.LastRunAt, NextRunAt: s.NextRunAt,
		RunCount: s.RunCount, SuccessfulRuns: s.SuccessCount, ErrorRuns: s.ErrorCount, SnapshotCount: s.SnapshotCount,
		AggregationWindowFrom: value.AggregationWindowFrom, AggregationWindowTo: value.AggregationWindowTo,
		LastSuccessfulSnapshot: value.LastSuccessfulSnapshot, Stars: value.Stars, Forks: value.Forks, OpenIssues: value.OpenIssues,
		LastError: s.LastError, Source: value.Source, Summary: value.Text, InsufficientData: value.InsufficientData}
}

func toolFailure() *mcp.CallToolResult { return &mcp.CallToolResult{IsError: true} }
func errorDetail(code string, err error) *ToolError {
	return &ToolError{Code: code, Message: err.Error()}
}
func storeError(err error) *ToolError {
	if errors.Is(err, store.ErrNotFound) {
		return errorDetail("not_found", err)
	}
	return errorDetail("storage_error", err)
}

func scheduleInputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"owner", "repository", "interval_seconds"},
		"properties": map[string]any{
			"owner":            map[string]any{"type": "string", "minLength": 1, "maxLength": 39, "pattern": ownerPatternText, "description": "GitHub account or organization."},
			"repository":       map[string]any{"type": "string", "minLength": 1, "maxLength": 100, "pattern": repositoryPatternText, "description": "Public repository name without owner."},
			"interval_seconds": map[string]any{"type": "integer", "minimum": minimumIntervalSeconds, "maximum": maximumIntervalSeconds, "description": "Seconds between background collections."},
			"max_runs":         map[string]any{"type": "integer", "minimum": 1, "maximum": maximumRuns, "description": "Optional total run limit; schedule completes automatically."},
		},
	}
}

func scheduleIDInputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"schedule_id"},
		"properties": map[string]any{"schedule_id": map[string]any{"type": "string", "minLength": 36, "maxLength": 36, "pattern": scheduleIDPatternText, "description": "Persistent monitor identifier returned when the schedule was created."}},
	}
}

func listInputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"status": map[string]any{"type": "string", "enum": []string{"active", "completed", "cancelled"}, "description": "Optional schedule status filter."}},
	}
}

func objectOutputSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}

func errorSchema() map[string]any {
	return objectOutputSchema(map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}, "code", "message")
}

func scheduleOutputSchema() map[string]any {
	return objectOutputSchema(map[string]any{
		"ok": map[string]any{"type": "boolean"}, "schedule_id": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"},
		"owner": map[string]any{"type": "string"}, "repository": map[string]any{"type": "string"}, "interval_seconds": map[string]any{"type": "integer"},
		"max_runs": map[string]any{"type": "integer"}, "next_run_at": map[string]any{"type": "string", "format": "date-time"},
		"created_at": map[string]any{"type": "string", "format": "date-time"}, "message": map[string]any{"type": "string"}, "error": errorSchema(),
	}, "ok")
}

func metricSchema() map[string]any {
	return objectOutputSchema(map[string]any{
		"latest": map[string]any{"type": "integer"}, "delta": map[string]any{"type": "integer"},
		"min": map[string]any{"type": "integer"}, "max": map[string]any{"type": "integer"},
	}, "latest", "delta", "min", "max")
}

func summaryOutputSchema() map[string]any {
	properties := map[string]any{
		"ok": map[string]any{"type": "boolean"}, "schedule_id": map[string]any{"type": "string"}, "owner": map[string]any{"type": "string"},
		"repository": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "interval_seconds": map[string]any{"type": "integer"},
		"max_runs": map[string]any{"type": "integer"}, "created_at": map[string]any{"type": "string", "format": "date-time"},
		"last_run_at": map[string]any{"type": "string", "format": "date-time"}, "next_run_at": map[string]any{"type": "string", "format": "date-time"},
		"run_count": map[string]any{"type": "integer"}, "successful_runs": map[string]any{"type": "integer"}, "error_runs": map[string]any{"type": "integer"},
		"snapshot_count": map[string]any{"type": "integer"}, "aggregation_window_from": map[string]any{"type": "string", "format": "date-time"},
		"aggregation_window_to": map[string]any{"type": "string", "format": "date-time"}, "last_successful_snapshot": map[string]any{"type": "object"},
		"stars": metricSchema(), "forks": metricSchema(), "open_issues": metricSchema(), "last_error": map[string]any{"type": "string"},
		"source": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"}, "insufficient_data": map[string]any{"type": "boolean"}, "error": errorSchema(),
	}
	return objectOutputSchema(properties, "ok", "run_count", "successful_runs", "error_runs", "snapshot_count", "stars", "forks", "open_issues", "insufficient_data")
}

func listOutputSchema() map[string]any {
	return objectOutputSchema(map[string]any{
		"ok": map[string]any{"type": "boolean"}, "count": map[string]any{"type": "integer"},
		"monitors": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "error": errorSchema(),
	}, "ok", "count", "monitors")
}

func cancelOutputSchema() map[string]any {
	return objectOutputSchema(map[string]any{
		"ok": map[string]any{"type": "boolean"}, "schedule_id": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"},
		"already_cancelled": map[string]any{"type": "boolean"}, "snapshot_count": map[string]any{"type": "integer"},
		"message": map[string]any{"type": "string"}, "error": errorSchema(),
	}, "ok", "already_cancelled", "snapshot_count")
}
