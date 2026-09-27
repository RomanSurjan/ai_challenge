package mcpgithub

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-challenge/day-18/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http/httptest"
)

func testSession(t *testing.T) (Session, *store.Store) {
	t.Helper()
	storage, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	server := httptest.NewServer(Handler(storage))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	session, err := NewRemoteConnector(server.URL+"/mcp", 5*time.Second).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, storage
}

func TestValidateScheduleInput(t *testing.T) {
	validMax := int64(2)
	tests := []struct {
		name  string
		input ScheduleInput
		valid bool
	}{
		{"valid", ScheduleInput{Owner: "modelcontextprotocol", Repository: "go-sdk", IntervalSeconds: 60, MaxRuns: &validMax}, true},
		{"missing owner", ScheduleInput{Repository: "go-sdk", IntervalSeconds: 60}, false},
		{"bad owner", ScheduleInput{Owner: "-owner", Repository: "go-sdk", IntervalSeconds: 60}, false},
		{"bad repo", ScheduleInput{Owner: "owner", Repository: "bad/repo", IntervalSeconds: 60}, false},
		{"too fast", ScheduleInput{Owner: "owner", Repository: "repo", IntervalSeconds: 59}, false},
		{"too slow", ScheduleInput{Owner: "owner", Repository: "repo", IntervalSeconds: maximumIntervalSeconds + 1}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validateScheduleInput(test.input) == nil; got != test.valid {
				t.Fatalf("valid = %v", got)
			}
		})
	}
}

func TestMCPToolsListIsCompleteDeterministicAndAnnotated(t *testing.T) {
	session, _ := testSession(t)
	tools, err := session.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{CancelToolName, SummaryToolName, ListToolName, ScheduleToolName}
	if len(tools) != len(want) {
		t.Fatalf("tools = %+v", tools)
	}
	for i, tool := range tools {
		if tool.Name != want[i] {
			t.Fatalf("tool[%d] = %q, want %q", i, tool.Name, want[i])
		}
		if tool.Description == "" || tool.OutputSchema == nil {
			t.Fatalf("incomplete tool = %+v", tool)
		}
		encoded, _ := json.Marshal(tool.InputSchema)
		var schema map[string]any
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("input schema = %s", encoded)
		}
		annotations, ok := tool.Annotations.(*mcp.ToolAnnotations)
		if !ok || annotations == nil {
			t.Fatalf("annotations = %#v", tool.Annotations)
		}
		if (tool.Name == SummaryToolName || tool.Name == ListToolName) != annotations.ReadOnlyHint {
			t.Fatalf("readOnlyHint for %s = %v", tool.Name, annotations.ReadOnlyHint)
		}
	}
	scheduleSchema, _ := json.Marshal(tools[3].InputSchema)
	for _, expected := range []string{`"required":["owner","repository","interval_seconds"]`, `"minimum":60`, `"maximum":2592000`} {
		if !strings.Contains(string(scheduleSchema), expected) {
			t.Fatalf("schedule schema %s misses %s", scheduleSchema, expected)
		}
	}
}

func TestMCPToolCallsReturnStructuredResultsAndIdempotentCancel(t *testing.T) {
	session, _ := testSession(t)
	created, err := session.CallTool(context.Background(), ScheduleToolName, map[string]any{
		"owner": "modelcontextprotocol", "repository": "go-sdk", "interval_seconds": 60, "max_runs": 2,
	})
	if err != nil || created.IsError || created.Structured == nil {
		t.Fatalf("schedule result = %+v, %v", created, err)
	}
	var schedule ScheduleOutput
	decodeStructured(t, created.Structured, &schedule)
	if !schedule.OK || schedule.ScheduleID == "" || schedule.Status != store.StatusActive || schedule.NextRunAt == nil {
		t.Fatalf("schedule = %+v", schedule)
	}

	listed, err := session.CallTool(context.Background(), ListToolName, map[string]any{"status": "active"})
	if err != nil || listed.IsError {
		t.Fatalf("list result = %+v, %v", listed, err)
	}
	var list ListOutput
	decodeStructured(t, listed.Structured, &list)
	if !list.OK || list.Count != 1 || list.Monitors[0].ID != schedule.ScheduleID {
		t.Fatalf("list = %+v", list)
	}

	summarized, err := session.CallTool(context.Background(), SummaryToolName, map[string]any{"schedule_id": schedule.ScheduleID})
	if err != nil || summarized.IsError {
		t.Fatalf("summary result = %+v, %v", summarized, err)
	}
	var summary SummaryOutput
	decodeStructured(t, summarized.Structured, &summary)
	if !summary.OK || summary.SnapshotCount != 0 || !summary.InsufficientData {
		t.Fatalf("summary = %+v", summary)
	}

	for i := range 2 {
		cancelled, err := session.CallTool(context.Background(), CancelToolName, map[string]any{"schedule_id": schedule.ScheduleID})
		if err != nil || cancelled.IsError {
			t.Fatalf("cancel %d = %+v, %v", i, cancelled, err)
		}
		var output CancelOutput
		decodeStructured(t, cancelled.Structured, &output)
		if !output.OK || output.Status != store.StatusCancelled || output.AlreadyCancelled != (i == 1) {
			t.Fatalf("cancel %d = %+v", i, output)
		}
	}
}

func TestMCPToolErrorsAreVisible(t *testing.T) {
	session, _ := testSession(t)
	invalid, err := session.CallTool(context.Background(), ScheduleToolName, map[string]any{"owner": "owner", "repository": "repo", "interval_seconds": 1})
	if err != nil || !invalid.IsError || !strings.Contains(invalid.Text, "minimum") {
		t.Fatalf("invalid result = %+v, %v", invalid, err)
	}

	missingID := "mon_00000000000000000000000000000000"
	missing, err := session.CallTool(context.Background(), SummaryToolName, map[string]any{"schedule_id": missingID})
	if err != nil || !missing.IsError || missing.Structured == nil {
		t.Fatalf("missing result = %+v, %v", missing, err)
	}
	var summary SummaryOutput
	decodeStructured(t, missing.Structured, &summary)
	if summary.Error == nil || summary.Error.Code != "not_found" {
		t.Fatalf("missing output = %+v", summary)
	}

	extra, err := session.CallTool(context.Background(), ListToolName, map[string]any{"unknown": true})
	if err != nil || !extra.IsError || !strings.Contains(extra.Text, "additional") {
		t.Fatalf("extra-property result = %+v, %v", extra, err)
	}
}

func decodeStructured(t *testing.T, value any, target any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
}
