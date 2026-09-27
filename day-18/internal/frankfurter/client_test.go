package frankfurter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestClientGetsAndParsesExactRate(t *testing.T) {
	client := NewClient(doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/rate/USD/EUR" || r.Header.Get("User-Agent") != UserAgent {
			t.Errorf("request = %s %#v", r.URL.Path, r.Header)
		}
		return response(200, `{"date":"2026-09-25","base":"USD","quote":"EUR","rate":0.923456789123456789}`), nil
	}))
	rate, err := client.GetRate(context.Background(), " usd ", "eur")
	if err != nil || rate.Value != "0.923456789123456789" || rate.Date != "2026-09-25" || rate.Source != Source {
		t.Fatalf("rate = %+v, %v", rate, err)
	}
}

func TestClientUnsupportedCurrencyAndHTTPFailures(t *testing.T) {
	for _, status := range []int{400, 404, 422, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
				return response(status, `{"message":"invalid currency: XXX"}`), nil
			}))
			_, err := client.GetRate(context.Background(), "USD", "XXX")
			if err == nil {
				t.Fatal("expected error")
			}
			if status == 429 && !errors.Is(err, ErrRateLimited) {
				t.Fatalf("error = %v", err)
			}
			if (status == 400 || status == 404 || status == 422) && !errors.Is(err, ErrUnsupportedCurrency) {
				t.Fatalf("error = %v", err)
			}
			if status >= 500 && !errors.Is(err, ErrUpstream) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestClientTimeoutInvalidAndOversizedResponses(t *testing.T) {
	client := NewClient(doerFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }))
	client.Timeout = time.Millisecond
	if _, err := client.GetRate(context.Background(), "USD", "EUR"); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("timeout error = %v", err)
	}

	client = NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"date":"bad","base":"USD","quote":"EUR","rate":-1}`), nil
	}))
	client.MaxResponseBytes = 8
	if _, err := client.GetRate(context.Background(), "USD", "EUR"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestValidatePairUsesCurrencyCatalogue(t *testing.T) {
	client := NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `[{"iso_code":"USD","name":"US Dollar"},{"iso_code":"EUR","name":"Euro"}]`), nil
	}))
	if err := client.ValidatePair(context.Background(), "USD", "EUR"); err != nil {
		t.Fatal(err)
	}
	if err := client.ValidatePair(context.Background(), "USD", "XXX"); !errors.Is(err, ErrUnsupportedCurrency) {
		t.Fatalf("error = %v", err)
	}
}
