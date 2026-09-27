package mcpcurrency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ai-challenge/day-18/internal/currencystore"
	"ai-challenge/day-18/internal/decimal"
	"ai-challenge/day-18/internal/frankfurter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName             = "day18-currency-monitor-mcp"
	ServerVersion          = "1.0.0"
	ScheduleToolName       = "schedule_exchange_rate_monitor"
	SummaryToolName        = "get_exchange_rate_monitor_summary"
	ListToolName           = "list_exchange_rate_monitors"
	CancelToolName         = "cancel_exchange_rate_monitor"
	ConvertToolName        = "convert_currency"
	currencyPatternText    = `^[A-Za-z]{3}$`
	scheduleIDPatternText  = `^mon_[a-f0-9]{32}$`
	amountPatternText      = `^[+]?(?:[0-9]{1,30})(?:\.[0-9]{1,18})?$`
	minimumIntervalSeconds = int64(60)
	maximumIntervalSeconds = int64(2592000)
	maximumRuns            = int64(100000)
)

var scheduleIDPattern = regexp.MustCompile(scheduleIDPatternText)

type MonitorStore interface {
	CreateSchedule(context.Context, store.CreateParams) (store.Schedule, error)
	Summary(context.Context, string) (store.Summary, error)
	ListSchedules(context.Context, string) ([]store.Schedule, error)
	CancelSchedule(context.Context, string, time.Time) (store.Schedule, bool, error)
}
type ScheduleInput struct {
	BaseCurrency    string `json:"base_currency"`
	QuoteCurrency   string `json:"quote_currency"`
	IntervalSeconds int64  `json:"interval_seconds"`
	MaxRuns         *int64 `json:"max_runs,omitempty"`
}
type ScheduleIDInput struct {
	ScheduleID string `json:"schedule_id"`
}
type ListInput struct {
	Status string `json:"status,omitempty"`
}
type ConvertInput struct {
	Amount        string `json:"amount"`
	BaseCurrency  string `json:"base_currency"`
	QuoteCurrency string `json:"quote_currency"`
}
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ScheduleOutput struct {
	OK              bool       `json:"ok"`
	ScheduleID      string     `json:"schedule_id,omitempty"`
	Status          string     `json:"status,omitempty"`
	BaseCurrency    string     `json:"base_currency,omitempty"`
	QuoteCurrency   string     `json:"quote_currency,omitempty"`
	IntervalSeconds int64      `json:"interval_seconds,omitempty"`
	MaxRuns         *int64     `json:"max_runs,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
	Message         string     `json:"message,omitempty"`
	Error           *ToolError `json:"error,omitempty"`
}
type SummaryOutput struct {
	OK               bool       `json:"ok"`
	ScheduleID       string     `json:"schedule_id,omitempty"`
	BaseCurrency     string     `json:"base_currency,omitempty"`
	QuoteCurrency    string     `json:"quote_currency,omitempty"`
	Status           string     `json:"status,omitempty"`
	IntervalSeconds  int64      `json:"interval_seconds,omitempty"`
	MaxRuns          *int64     `json:"max_runs,omitempty"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	NextRunAt        *time.Time `json:"next_run_at,omitempty"`
	RunCount         int64      `json:"run_count"`
	SuccessfulRuns   int64      `json:"successful_runs"`
	ErrorRuns        int64      `json:"error_runs"`
	SnapshotCount    int64      `json:"snapshot_count"`
	CurrentRate      string     `json:"current_rate,omitempty"`
	FirstRate        string     `json:"first_rate,omitempty"`
	LastRate         string     `json:"last_rate,omitempty"`
	AbsoluteChange   string     `json:"absolute_change,omitempty"`
	PercentChange    string     `json:"percent_change,omitempty"`
	MinimumRate      string     `json:"minimum_rate,omitempty"`
	MaximumRate      string     `json:"maximum_rate,omitempty"`
	WindowFrom       string     `json:"window_from,omitempty"`
	WindowTo         string     `json:"window_to,omitempty"`
	RateDate         string     `json:"rate_date,omitempty"`
	LastFetchedAt    string     `json:"last_fetched_at,omitempty"`
	Source           string     `json:"source,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	Summary          string     `json:"summary,omitempty"`
	InsufficientData bool       `json:"insufficient_data"`
	Error            *ToolError `json:"error,omitempty"`
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
type ConvertOutput struct {
	OK              bool       `json:"ok"`
	Amount          string     `json:"amount,omitempty"`
	BaseCurrency    string     `json:"base_currency,omitempty"`
	QuoteCurrency   string     `json:"quote_currency,omitempty"`
	Rate            string     `json:"rate,omitempty"`
	ConvertedAmount string     `json:"converted_amount,omitempty"`
	RateDate        string     `json:"rate_date,omitempty"`
	FetchedAt       *time.Time `json:"fetched_at,omitempty"`
	Source          string     `json:"source,omitempty"`
	Error           *ToolError `json:"error,omitempty"`
}

func NewServer(storage MonitorStore, rates frankfurter.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: ServerVersion}, &mcp.ServerOptions{Instructions: "Manage persistent periodic monitors for official-source reference exchange rates. Never describe these rates as real-time trading quotes. Use returned schedule IDs and exact decimal strings; never invent either."})
	RegisterTools(server, storage, rates)
	return server
}

// RegisterTools adds the exchange-rate tools to an existing MCP server.
func RegisterTools(server *mcp.Server, storage MonitorStore, rates frankfurter.Service) {
	openWorld, closedWorld, nonDestructive := true, false, false
	mcp.AddTool(server, &mcp.Tool{Name: ScheduleToolName, Title: "Create exchange-rate monitor", Description: "Create a persistent background monitor for an ISO 4217 currency pair. The first reference-rate request is due immediately, future runs survive service restarts, and max_runs can stop it automatically. Use this when the user asks to check a rate repeatedly.", InputSchema: scheduleInputSchema(), OutputSchema: scheduleOutputSchema(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false, DestructiveHint: &nonDestructive, OpenWorldHint: &openWorld}}, func(ctx context.Context, _ *mcp.CallToolRequest, input ScheduleInput) (*mcp.CallToolResult, ScheduleOutput, error) {
		base, quote, err := validatePair(input.BaseCurrency, input.QuoteCurrency)
		if err != nil {
			return toolFailure(), ScheduleOutput{Error: errorDetail("invalid_argument", err)}, nil
		}
		if input.IntervalSeconds < minimumIntervalSeconds || input.IntervalSeconds > maximumIntervalSeconds {
			return toolFailure(), ScheduleOutput{Error: errorDetail("invalid_argument", fmt.Errorf("interval_seconds must be between %d and %d", minimumIntervalSeconds, maximumIntervalSeconds))}, nil
		}
		if input.MaxRuns != nil && (*input.MaxRuns < 1 || *input.MaxRuns > maximumRuns) {
			return toolFailure(), ScheduleOutput{Error: errorDetail("invalid_argument", fmt.Errorf("max_runs must be between 1 and %d", maximumRuns))}, nil
		}
		if rates == nil {
			return toolFailure(), ScheduleOutput{Error: errorDetail("configuration_error", errors.New("Frankfurter client is not configured"))}, nil
		}
		if err := rates.ValidatePair(ctx, base, quote); err != nil {
			return toolFailure(), ScheduleOutput{Error: apiError(err)}, nil
		}
		created, err := storage.CreateSchedule(ctx, store.CreateParams{BaseCurrency: base, QuoteCurrency: quote, IntervalSeconds: input.IntervalSeconds, MaxRuns: input.MaxRuns, Now: time.Now().UTC()})
		if err != nil {
			return toolFailure(), ScheduleOutput{Error: errorDetail("storage_error", err)}, nil
		}
		at := created.CreatedAt
		return nil, ScheduleOutput{OK: true, ScheduleID: created.ID, Status: created.Status, BaseCurrency: base, QuoteCurrency: quote, IntervalSeconds: created.IntervalSeconds, MaxRuns: created.MaxRuns, NextRunAt: created.NextRunAt, CreatedAt: &at, Message: "Monitor created. The first background check is due immediately."}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: SummaryToolName, Title: "Exchange-rate monitor summary", Description: "Return the persisted aggregate for exactly one monitor: current, first and last rates; absolute and percentage change; minimum and maximum; success/error counts; API rate date, fetch time, next check, source and latest error. Requires a real schedule_id returned by a tool.", InputSchema: scheduleIDInputSchema(), OutputSchema: summaryOutputSchema(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}}, func(ctx context.Context, _ *mcp.CallToolRequest, input ScheduleIDInput) (*mcp.CallToolResult, SummaryOutput, error) {
		input.ScheduleID = strings.TrimSpace(input.ScheduleID)
		if !scheduleIDPattern.MatchString(input.ScheduleID) {
			return toolFailure(), SummaryOutput{Error: errorDetail("invalid_argument", errors.New("schedule_id has an invalid format"))}, nil
		}
		value, err := storage.Summary(ctx, input.ScheduleID)
		if err != nil {
			return toolFailure(), SummaryOutput{Error: storeError(err)}, nil
		}
		return nil, summaryOutput(value), nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: ListToolName, Title: "List exchange-rate monitors", Description: "List all persistent exchange-rate monitor schedules, or filter by active, completed, or cancelled. Use this to discover real schedule IDs before a summary or cancellation.", InputSchema: listInputSchema(), OutputSchema: listOutputSchema(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}}, func(ctx context.Context, _ *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, ListOutput, error) {
		input.Status = strings.ToLower(strings.TrimSpace(input.Status))
		if input.Status != "" && input.Status != store.StatusActive && input.Status != store.StatusCompleted && input.Status != store.StatusCancelled {
			return toolFailure(), ListOutput{Monitors: []store.Schedule{}, Error: errorDetail("invalid_argument", errors.New("status must be active, completed, or cancelled"))}, nil
		}
		items, err := storage.ListSchedules(ctx, input.Status)
		if err != nil {
			return toolFailure(), ListOutput{Monitors: []store.Schedule{}, Error: errorDetail("storage_error", err)}, nil
		}
		return nil, ListOutput{OK: true, Count: len(items), Monitors: items}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: CancelToolName, Title: "Cancel exchange-rate monitor", Description: "Stop all future checks for one real schedule_id while preserving its run history, snapshots, errors and aggregates. Repeated cancellation is safe and idempotent.", InputSchema: scheduleIDInputSchema(), OutputSchema: cancelOutputSchema(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true, DestructiveHint: &nonDestructive, OpenWorldHint: &closedWorld}}, func(ctx context.Context, _ *mcp.CallToolRequest, input ScheduleIDInput) (*mcp.CallToolResult, CancelOutput, error) {
		input.ScheduleID = strings.TrimSpace(input.ScheduleID)
		if !scheduleIDPattern.MatchString(input.ScheduleID) {
			return toolFailure(), CancelOutput{Error: errorDetail("invalid_argument", errors.New("schedule_id has an invalid format"))}, nil
		}
		value, already, err := storage.CancelSchedule(ctx, input.ScheduleID, time.Now().UTC())
		if err != nil {
			return toolFailure(), CancelOutput{Error: storeError(err)}, nil
		}
		message := "Future checks were cancelled; saved history remains available."
		if already {
			message = "Monitor was already cancelled; saved history remains available."
		}
		return nil, CancelOutput{OK: true, ScheduleID: value.ID, Status: value.Status, AlreadyCancelled: already, SnapshotCount: value.SnapshotCount, Message: message}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: ConvertToolName, Title: "Convert currency amount", Description: "Fetch the latest Frankfurter reference rate for a currency pair and multiply an exact decimal amount without binary floating point. Use for one-off questions such as how much 100 USD is in EUR; this does not create a monitor.", InputSchema: convertInputSchema(), OutputSchema: convertOutputSchema(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &openWorld}}, func(ctx context.Context, _ *mcp.CallToolRequest, input ConvertInput) (*mcp.CallToolResult, ConvertOutput, error) {
		base, quote, err := validatePair(input.BaseCurrency, input.QuoteCurrency)
		if err != nil {
			return toolFailure(), ConvertOutput{Error: errorDetail("invalid_argument", err)}, nil
		}
		amount, err := decimal.Parse(input.Amount)
		if err != nil || amount.Sign() < 0 {
			return toolFailure(), ConvertOutput{Error: errorDetail("invalid_argument", errors.New("amount must be a non-negative decimal with at most 30 integer and 18 fractional digits"))}, nil
		}
		rate, err := rates.GetRate(ctx, base, quote)
		if err != nil {
			return toolFailure(), ConvertOutput{Error: apiError(err)}, nil
		}
		rateValue, err := decimal.Parse(rate.Value)
		if err != nil {
			return toolFailure(), ConvertOutput{Error: errorDetail("upstream_error", err)}, nil
		}
		fetched := rate.Fetched
		return nil, ConvertOutput{OK: true, Amount: amount.String(), BaseCurrency: base, QuoteCurrency: quote, Rate: rate.Value, ConvertedAmount: amount.Mul(rateValue).String(), RateDate: rate.Date, FetchedAt: &fetched, Source: rate.Source}, nil
	})
}

func Handler(storage MonitorStore, rates frankfurter.Service) http.Handler {
	mux := http.NewServeMux()
	server := NewServer(storage, rates)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": ServerName})
	})
	return mux
}
func validatePair(base, quote string) (string, string, error) {
	base, err := frankfurter.NormalizeCurrency(base)
	if err != nil {
		return "", "", fmt.Errorf("base_currency: %w", err)
	}
	quote, err = frankfurter.NormalizeCurrency(quote)
	if err != nil {
		return "", "", fmt.Errorf("quote_currency: %w", err)
	}
	return base, quote, nil
}
func summaryOutput(value store.Summary) SummaryOutput {
	s := value.Schedule
	created := s.CreatedAt
	return SummaryOutput{OK: true, ScheduleID: s.ID, BaseCurrency: s.BaseCurrency, QuoteCurrency: s.QuoteCurrency, Status: s.Status, IntervalSeconds: s.IntervalSeconds, MaxRuns: s.MaxRuns, CreatedAt: &created, LastRunAt: s.LastRunAt, NextRunAt: s.NextRunAt, RunCount: s.RunCount, SuccessfulRuns: s.SuccessCount, ErrorRuns: s.ErrorCount, SnapshotCount: s.SnapshotCount, CurrentRate: value.CurrentRate, FirstRate: value.FirstRate, LastRate: value.LastRate, AbsoluteChange: value.AbsoluteChange, PercentChange: value.PercentChange, MinimumRate: value.MinimumRate, MaximumRate: value.MaximumRate, WindowFrom: value.WindowFrom, WindowTo: value.WindowTo, RateDate: value.RateDate, LastFetchedAt: value.LastFetchedAt, Source: value.Source, LastError: s.LastError, Summary: value.Text, InsufficientData: value.InsufficientData}
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
func apiError(err error) *ToolError {
	switch {
	case errors.Is(err, frankfurter.ErrUnsupportedCurrency):
		return errorDetail("unsupported_currency", err)
	case errors.Is(err, frankfurter.ErrRateLimited):
		return errorDetail("rate_limited", err)
	default:
		return errorDetail("upstream_error", err)
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
func errorSchema() map[string]any {
	return objectSchema(map[string]any{"code": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}, "message": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000}}, "code", "message")
}
func scheduleInputSchema() map[string]any {
	return objectSchema(map[string]any{"base_currency": currencySchema("Base ISO 4217 currency code."), "quote_currency": currencySchema("Target ISO 4217 currency code."), "interval_seconds": map[string]any{"type": "integer", "minimum": minimumIntervalSeconds, "maximum": maximumIntervalSeconds, "description": "Seconds between background checks."}, "max_runs": map[string]any{"type": "integer", "minimum": 1, "maximum": maximumRuns, "description": "Optional total number of attempts, including errors."}}, "base_currency", "quote_currency", "interval_seconds")
}
func scheduleIDInputSchema() map[string]any {
	return objectSchema(map[string]any{"schedule_id": map[string]any{"type": "string", "minLength": 36, "maxLength": 36, "pattern": scheduleIDPatternText, "description": "Persistent monitor ID returned by schedule or list tools."}}, "schedule_id")
}
func listInputSchema() map[string]any {
	return objectSchema(map[string]any{"status": map[string]any{"type": "string", "enum": []string{"active", "completed", "cancelled"}, "description": "Optional exact status filter."}})
}
func convertInputSchema() map[string]any {
	return objectSchema(map[string]any{"amount": map[string]any{"type": "string", "minLength": 1, "maxLength": 49, "pattern": amountPatternText, "description": "Non-negative decimal amount encoded as a string to preserve precision."}, "base_currency": currencySchema("Currency of the input amount."), "quote_currency": currencySchema("Currency of the converted amount.")}, "amount", "base_currency", "quote_currency")
}
func currencySchema(description string) map[string]any {
	return map[string]any{"type": "string", "minLength": 3, "maxLength": 3, "pattern": currencyPatternText, "description": description}
}
func baseOutputProperties() map[string]any {
	return map[string]any{"ok": map[string]any{"type": "boolean"}, "error": errorSchema()}
}
func scheduleOutputSchema() map[string]any {
	p := baseOutputProperties()
	p["schedule_id"] = map[string]any{"type": "string"}
	p["status"] = map[string]any{"type": "string"}
	p["base_currency"] = map[string]any{"type": "string"}
	p["quote_currency"] = map[string]any{"type": "string"}
	p["interval_seconds"] = map[string]any{"type": "integer"}
	p["max_runs"] = map[string]any{"type": "integer"}
	p["next_run_at"] = dateTimeSchema()
	p["created_at"] = dateTimeSchema()
	p["message"] = map[string]any{"type": "string"}
	return objectSchema(p, "ok")
}
func summaryOutputSchema() map[string]any {
	p := baseOutputProperties()
	for _, name := range []string{"schedule_id", "base_currency", "quote_currency", "status", "current_rate", "first_rate", "last_rate", "absolute_change", "percent_change", "minimum_rate", "maximum_rate", "rate_date", "source", "last_error", "summary"} {
		p[name] = map[string]any{"type": "string"}
	}
	for _, name := range []string{"interval_seconds", "max_runs", "run_count", "successful_runs", "error_runs", "snapshot_count"} {
		p[name] = map[string]any{"type": "integer"}
	}
	for _, name := range []string{"created_at", "last_run_at", "next_run_at", "window_from", "window_to", "last_fetched_at"} {
		p[name] = dateTimeSchema()
	}
	p["insufficient_data"] = map[string]any{"type": "boolean"}
	return objectSchema(p, "ok", "run_count", "successful_runs", "error_runs", "snapshot_count", "insufficient_data")
}
func listOutputSchema() map[string]any {
	p := baseOutputProperties()
	p["count"] = map[string]any{"type": "integer", "minimum": 0}
	p["monitors"] = map[string]any{"type": "array", "items": monitorSchema()}
	return objectSchema(p, "ok", "count", "monitors")
}
func monitorSchema() map[string]any {
	return objectSchema(map[string]any{"schedule_id": map[string]any{"type": "string"}, "base_currency": map[string]any{"type": "string"}, "quote_currency": map[string]any{"type": "string"}, "interval_seconds": map[string]any{"type": "integer"}, "max_runs": map[string]any{"type": "integer"}, "status": map[string]any{"type": "string"}, "run_count": map[string]any{"type": "integer"}, "successful_runs": map[string]any{"type": "integer"}, "error_runs": map[string]any{"type": "integer"}, "next_run_at": dateTimeSchema(), "last_run_at": dateTimeSchema(), "last_error": map[string]any{"type": "string"}, "created_at": dateTimeSchema(), "updated_at": dateTimeSchema(), "snapshot_count": map[string]any{"type": "integer"}}, "schedule_id", "base_currency", "quote_currency", "interval_seconds", "status", "run_count", "successful_runs", "error_runs", "created_at", "updated_at", "snapshot_count")
}
func cancelOutputSchema() map[string]any {
	p := baseOutputProperties()
	p["schedule_id"] = map[string]any{"type": "string"}
	p["status"] = map[string]any{"type": "string"}
	p["already_cancelled"] = map[string]any{"type": "boolean"}
	p["snapshot_count"] = map[string]any{"type": "integer"}
	p["message"] = map[string]any{"type": "string"}
	return objectSchema(p, "ok", "already_cancelled", "snapshot_count")
}
func convertOutputSchema() map[string]any {
	p := baseOutputProperties()
	for _, name := range []string{"amount", "base_currency", "quote_currency", "rate", "converted_amount", "rate_date", "source"} {
		p[name] = map[string]any{"type": "string"}
	}
	p["fetched_at"] = dateTimeSchema()
	return objectSchema(p, "ok")
}
func dateTimeSchema() map[string]any { return map[string]any{"type": "string", "format": "date-time"} }
