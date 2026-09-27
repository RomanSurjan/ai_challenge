package mcpcurrency

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-challenge/day-18/internal/currencystore"
	"ai-challenge/day-18/internal/frankfurter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeRateService struct {
	validateErr error
	rate        frankfurter.Rate
	rateErr     error
}

func (f fakeRateService) ValidatePair(context.Context, string, string) error { return f.validateErr }
func (f fakeRateService) GetRate(_ context.Context, b, q string) (frankfurter.Rate, error) {
	if f.rateErr != nil {
		return frankfurter.Rate{}, f.rateErr
	}
	v := f.rate
	if v.Value == "" {
		v = frankfurter.Rate{Base: b, Quote: q, Value: "0.923456789123456789", Date: "2026-09-26", Fetched: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC), Source: frankfurter.Source}
	}
	return v, nil
}
func testSession(t *testing.T) (Session, *store.Store) {
	t.Helper()
	storage, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	server := NewServer(storage, fakeRateService{})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close(); _ = serverSession.Close() })
	return &remoteSession{session: clientSession}, storage
}

func TestFullMCPContract(t *testing.T) {
	session, _ := testSession(t)
	tools, err := session.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{CancelToolName, ConvertToolName, SummaryToolName, ListToolName, ScheduleToolName}
	if len(tools) != len(want) {
		t.Fatalf("tools=%+v", tools)
	}
	for i, tool := range tools {
		if tool.Name != want[i] {
			t.Fatalf("tool[%d]=%s want %s", i, tool.Name, want[i])
		}
		if tool.Description == "" || tool.OutputSchema == nil {
			t.Fatalf("incomplete tool=%+v", tool)
		}
		encoded, _ := json.Marshal(tool.InputSchema)
		var schema map[string]any
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("input schema=%s", encoded)
		}
		annotations, ok := tool.Annotations.(*mcp.ToolAnnotations)
		if !ok || annotations == nil {
			t.Fatalf("annotations=%#v", tool.Annotations)
		}
		readOnly := tool.Name == SummaryToolName || tool.Name == ListToolName || tool.Name == ConvertToolName
		if annotations.ReadOnlyHint != readOnly {
			t.Fatalf("readOnly %s=%v", tool.Name, annotations.ReadOnlyHint)
		}
	}
	listSchema, _ := json.Marshal(tools[3].InputSchema)
	if strings.Contains(string(listSchema), `"required"`) {
		t.Fatalf("optional list schema must omit required: %s", listSchema)
	}
	encoded, _ := json.Marshal(tools[4].InputSchema)
	text := string(encoded)
	for _, part := range []string{`"required":["base_currency","quote_currency","interval_seconds"]`, `"minimum":60`, `"maximum":2592000`, `"additionalProperties":false`} {
		if !strings.Contains(text, part) {
			t.Fatalf("schedule schema %s misses %s", text, part)
		}
	}
}

func TestMCPStructuredCallsConversionAndIdempotentCancel(t *testing.T) {
	session, _ := testSession(t)
	created, err := session.CallTool(context.Background(), ScheduleToolName, map[string]any{"base_currency": "usd", "quote_currency": "eur", "interval_seconds": 60, "max_runs": 2})
	if err != nil || created.IsError || created.Structured == nil {
		t.Fatalf("created=%+v %v", created, err)
	}
	var schedule ScheduleOutput
	decode(t, created.Structured, &schedule)
	if !schedule.OK || schedule.ScheduleID == "" || schedule.BaseCurrency != "USD" {
		t.Fatalf("schedule=%+v", schedule)
	}
	listed, err := session.CallTool(context.Background(), ListToolName, map[string]any{"status": "active"})
	if err != nil || listed.IsError {
		t.Fatalf("listed=%+v %v", listed, err)
	}
	var list ListOutput
	decode(t, listed.Structured, &list)
	if list.Count != 1 || list.Monitors[0].ID != schedule.ScheduleID {
		t.Fatalf("list=%+v", list)
	}
	summaryResult, err := session.CallTool(context.Background(), SummaryToolName, map[string]any{"schedule_id": schedule.ScheduleID})
	if err != nil || summaryResult.IsError {
		t.Fatalf("summary=%+v %v", summaryResult, err)
	}
	var summary SummaryOutput
	decode(t, summaryResult.Structured, &summary)
	if !summary.OK || summary.SnapshotCount != 0 || !summary.InsufficientData {
		t.Fatalf("summary=%+v", summary)
	}
	converted, err := session.CallTool(context.Background(), ConvertToolName, map[string]any{"amount": "100.00", "base_currency": "USD", "quote_currency": "EUR"})
	if err != nil || converted.IsError {
		t.Fatalf("converted=%+v %v", converted, err)
	}
	var conversion ConvertOutput
	decode(t, converted.Structured, &conversion)
	if conversion.ConvertedAmount != "92.3456789123456789" || conversion.Rate != "0.923456789123456789" {
		t.Fatalf("conversion=%+v", conversion)
	}
	for i := range 2 {
		result, err := session.CallTool(context.Background(), CancelToolName, map[string]any{"schedule_id": schedule.ScheduleID})
		if err != nil || result.IsError {
			t.Fatalf("cancel=%+v %v", result, err)
		}
		var output CancelOutput
		decode(t, result.Structured, &output)
		if output.AlreadyCancelled != (i == 1) || output.Status != store.StatusCancelled {
			t.Fatalf("cancel %d=%+v", i, output)
		}
	}
}

func TestMCPStructuredErrorsAndStrictSchemas(t *testing.T) {
	session, _ := testSession(t)
	invalid, err := session.CallTool(context.Background(), ScheduleToolName, map[string]any{"base_currency": "USD", "quote_currency": "EUR", "interval_seconds": 1})
	if err != nil || !invalid.IsError || !strings.Contains(invalid.Text, "minimum") {
		t.Fatalf("invalid=%+v %v", invalid, err)
	}
	missing, err := session.CallTool(context.Background(), SummaryToolName, map[string]any{"schedule_id": "mon_00000000000000000000000000000000"})
	if err != nil || !missing.IsError {
		t.Fatalf("missing=%+v %v", missing, err)
	}
	var output SummaryOutput
	decode(t, missing.Structured, &output)
	if output.Error == nil || output.Error.Code != "not_found" {
		t.Fatalf("output=%+v", output)
	}
	extra, err := session.CallTool(context.Background(), ListToolName, map[string]any{"unknown": true})
	if err != nil || !extra.IsError || !strings.Contains(extra.Text, "additional") {
		t.Fatalf("extra=%+v %v", extra, err)
	}
}

func TestUnsupportedCurrencyIsStructured(t *testing.T) {
	storage, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	server := NewServer(storage, fakeRateService{validateErr: fmtUnsupported()})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "unsupported-test", Version: "1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	session := &remoteSession{session: clientSession}
	result, err := session.CallTool(context.Background(), ScheduleToolName, map[string]any{"base_currency": "USD", "quote_currency": "XXX", "interval_seconds": 60})
	if err != nil || !result.IsError {
		t.Fatalf("result=%+v %v", result, err)
	}
	var output ScheduleOutput
	decode(t, result.Structured, &output)
	if output.Error == nil || output.Error.Code != "unsupported_currency" {
		t.Fatalf("output=%+v", output)
	}
}
func fmtUnsupported() error {
	return errors.Join(frankfurter.ErrUnsupportedCurrency, errors.New("XXX is unavailable"))
}
func decode(t *testing.T, value, target any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
}
