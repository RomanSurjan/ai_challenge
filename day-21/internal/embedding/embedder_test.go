package embedding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type varyingEmbedder struct{}

func (varyingEmbedder) Embed(_ context.Context, _ string, texts []string) ([][]float64, error) {
	result := make([][]float64, len(texts))
	for i := range result {
		result[i] = []float64{3, 4}
	}
	if len(texts) > 1 {
		result[1] = []float64{1, 2, 3}
	}
	return result, nil
}

func TestEmbedAllRejectsMismatchedDimensions(t *testing.T) {
	_, _, err := EmbedAll(context.Background(), varyingEmbedder{}, "model", []string{"a", "b"}, 10)
	if err == nil {
		t.Fatal("expected dimension mismatch")
	}
}

func TestOllamaBatchRequestSetsTruncateFalse(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		var request struct {
			Model    string   `json:"model"`
			Input    []string `json:"input"`
			Truncate *bool    `json:"truncate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || len(request.Input) != 2 || request.Truncate == nil || *request.Truncate {
			t.Errorf("unexpected request: %#v", request)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1,0],[0,1]]}`)),
		}, nil
	})
	embedder := NewOllama("http://ollama.test/api/embed", DefaultTimeout)
	embedder.Client.Transport = transport
	vectors, err := embedder.Embed(context.Background(), "test-model", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 {
		t.Fatalf("got %d vectors", len(vectors))
	}
}

func TestNormalize(t *testing.T) {
	vector := []float64{3, 4}
	if err := Normalize(vector); err != nil {
		t.Fatal(err)
	}
	if vector[0] != 0.6 || vector[1] != 0.8 {
		t.Fatalf("normalized vector = %#v", vector)
	}
}
