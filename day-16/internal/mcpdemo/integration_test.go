package mcpdemo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConnectAndListTools(t *testing.T) {
	httpServer := httptest.NewServer(Handler())
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := ConnectAndListTools(ctx, httpServer.URL+"/mcp")
	if err != nil {
		t.Fatalf("ConnectAndListTools() error = %v", err)
	}
	if result.ServerName != ServerName {
		t.Fatalf("server name = %q, want %q", result.ServerName, ServerName)
	}
	if result.ProtocolVersion == "" {
		t.Fatal("protocol version is empty")
	}
	if len(result.Tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(result.Tools))
	}
	if result.Tools[0].Name != ToolName {
		t.Fatalf("tool name = %q, want %q", result.Tools[0].Name, ToolName)
	}
	if result.Tools[0].InputSchema == nil {
		t.Fatal("tool input schema is nil")
	}
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
}
