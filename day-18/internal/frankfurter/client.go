package frankfurter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"ai-challenge/day-18/internal/decimal"
)

const (
	DefaultBaseURL  = "https://api.frankfurter.dev"
	Source          = "Frankfurter API v2 — reference rates from central banks and official sources"
	UserAgent       = "day-18-currency/1.0 (+https://frankfurter.dev/)"
	defaultMaxBytes = int64(1 << 20)
)

var (
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrRateLimited         = errors.New("Frankfurter API rate limited")
	ErrUpstream            = errors.New("Frankfurter API unavailable")
	currencyPattern        = regexp.MustCompile(`^[A-Z]{3}$`)
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	HTTPClient       HTTPDoer
	BaseURL          string
	Timeout          time.Duration
	MaxResponseBytes int64
}

type Rate struct {
	Date    string    `json:"date"`
	Base    string    `json:"base"`
	Quote   string    `json:"quote"`
	Value   string    `json:"rate"`
	Source  string    `json:"source"`
	Fetched time.Time `json:"fetched_at"`
}

type Currency struct {
	ISOCode string `json:"iso_code"`
	Name    string `json:"name"`
}

type APIError struct {
	StatusCode int
	Message    string
	Kind       error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Frankfurter API HTTP %d: %s", e.StatusCode, e.Message)
}
func (e *APIError) Unwrap() error { return e.Kind }

func NewClient(httpClient HTTPDoer) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 12 * time.Second}
	}
	return &Client{HTTPClient: httpClient, BaseURL: DefaultBaseURL, Timeout: 12 * time.Second, MaxResponseBytes: defaultMaxBytes}
}

func NormalizeCurrency(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !currencyPattern.MatchString(value) {
		return "", errors.New("currency must be a three-letter ISO 4217 code")
	}
	return value, nil
}

func (c *Client) GetRate(ctx context.Context, base, quote string) (Rate, error) {
	base, err := NormalizeCurrency(base)
	if err != nil {
		return Rate{}, err
	}
	quote, err = NormalizeCurrency(quote)
	if err != nil {
		return Rate{}, err
	}
	endpoint, err := url.Parse(strings.TrimRight(c.BaseURL, "/") + "/v2/rate/" + url.PathEscape(base) + "/" + url.PathEscape(quote))
	if err != nil {
		return Rate{}, fmt.Errorf("build Frankfurter URL: %w", err)
	}
	body, err := c.get(ctx, endpoint.String())
	if err != nil {
		return Rate{}, err
	}
	var response struct {
		Date  string      `json:"date"`
		Base  string      `json:"base"`
		Quote string      `json:"quote"`
		Rate  json.Number `json:"rate"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil {
		return Rate{}, fmt.Errorf("decode Frankfurter rate: %w", err)
	}
	if response.Date == "" || response.Base != base || response.Quote != quote || response.Rate == "" {
		return Rate{}, errors.New("Frankfurter API returned an incomplete or mismatched rate")
	}
	if _, err := time.Parse("2006-01-02", response.Date); err != nil {
		return Rate{}, errors.New("Frankfurter API returned an invalid rate date")
	}
	parsed, err := decimal.Parse(response.Rate.String())
	if err != nil || parsed.Sign() <= 0 {
		return Rate{}, errors.New("Frankfurter API returned an invalid positive rate")
	}
	return Rate{Date: response.Date, Base: base, Quote: quote, Value: parsed.String(), Source: Source, Fetched: time.Now().UTC()}, nil
}

func (c *Client) ListCurrencies(ctx context.Context) ([]Currency, error) {
	body, err := c.get(ctx, strings.TrimRight(c.BaseURL, "/")+"/v2/currencies")
	if err != nil {
		return nil, err
	}
	var currencies []Currency
	if err := json.Unmarshal(body, &currencies); err != nil {
		return nil, fmt.Errorf("decode Frankfurter currencies: %w", err)
	}
	if len(currencies) == 0 {
		return nil, errors.New("Frankfurter API returned no currencies")
	}
	for _, item := range currencies {
		if _, err := NormalizeCurrency(item.ISOCode); err != nil || strings.TrimSpace(item.Name) == "" {
			return nil, errors.New("Frankfurter API returned an invalid currency catalogue")
		}
	}
	return currencies, nil
}

func (c *Client) ValidatePair(ctx context.Context, base, quote string) error {
	currencies, err := c.ListCurrencies(ctx)
	if err != nil {
		return err
	}
	foundBase, foundQuote := false, false
	for _, item := range currencies {
		foundBase = foundBase || item.ISOCode == base
		foundQuote = foundQuote || item.ISOCode == quote
	}
	if !foundBase || !foundQuote {
		missing := base
		if foundBase {
			missing = quote
		}
		return fmt.Errorf("%w: %s is not in the Frankfurter currency catalogue", ErrUnsupportedCurrency, missing)
	}
	return nil
}

func (c *Client) get(ctx context.Context, endpoint string) ([]byte, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Frankfurter request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", UserAgent)
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Frankfurter API request: %w", err)
	}
	defer response.Body.Close()
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = defaultMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read Frankfurter response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("Frankfurter API response exceeds %d bytes", limit)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, responseError(response.StatusCode, body)
	}
	return body, nil
}

func responseError(status int, body []byte) error {
	var envelope struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &envelope)
	message := strings.TrimSpace(envelope.Message)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if len(message) > 500 {
		message = message[:500]
	}
	if message == "" {
		message = http.StatusText(status)
	}
	kind := ErrUpstream
	if status == http.StatusBadRequest || status == http.StatusNotFound || status == http.StatusUnprocessableEntity {
		kind = ErrUnsupportedCurrency
	} else if status == http.StatusTooManyRequests {
		kind = ErrRateLimited
	}
	return &APIError{StatusCode: status, Message: message, Kind: kind}
}

type Service interface {
	GetRate(context.Context, string, string) (Rate, error)
	ValidatePair(context.Context, string, string) error
}
