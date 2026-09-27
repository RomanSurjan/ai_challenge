package agent

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestDeepSeekRetriesOneTransportFailureWithSamePayload(t *testing.T) {
	attempts := 0
	var bodies []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(body))
		if attempts == 1 {
			return nil, errors.New("TLS handshake timeout")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))}, nil
	})}
	deepSeek := NewDeepSeekClient("test-key", "https://example.test", client)
	result, err := deepSeek.Complete(t.Context(), ChatRequest{Model: "test", Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("attempts=%d bodies=%q", attempts, bodies)
	}
	if result.Choices[0].Message.Content != "ok" {
		t.Fatalf("result=%#v", result)
	}
}
