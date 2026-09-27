package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeepSeekRetriesOnlyTransportErrors(t *testing.T) {
	attempts := 0
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("temporary transport")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	deep := NewDeepSeekClient("secret", "https://example.invalid", client)
	if _, err := deep.Complete(context.Background(), ChatRequest{Model: "x"}); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}

	attempts = 0
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"error":"rate"}`)), Header: make(http.Header), Request: r}, nil
	})
	if _, err := deep.Complete(context.Background(), ChatRequest{Model: "x"}); err == nil {
		t.Fatal("expected HTTP error")
	}
	if attempts != 1 {
		t.Fatalf("HTTP error retried: %d", attempts)
	}
}
