package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrLLM = errors.New("DeepSeek API error")

type DeepSeekClient struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

func NewDeepSeekClient(apiKey, baseURL string, client *http.Client) *DeepSeekClient {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSHandshakeTimeout = 30 * time.Second
		client = &http.Client{Timeout: 90 * time.Second, Transport: transport}
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return &DeepSeekClient{APIKey: strings.TrimSpace(apiKey), BaseURL: baseURL, HTTPClient: client}
}

func (c *DeepSeekClient) Complete(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if c.APIKey == "" {
		return ChatResponse{}, errors.New("DEEPSEEK_API_KEY is not configured")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("encode model request: %w", err)
	}
	var response *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		req, createErr := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
		if createErr != nil {
			return ChatResponse{}, createErr
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		response, err = c.HTTPClient.Do(req)
		if err == nil {
			break
		}
		if ctx.Err() != nil || attempt == 1 {
			return ChatResponse{}, fmt.Errorf("%w: transport failed after %d attempt(s): %v", ErrLLM, attempt+1, err)
		}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("%w: read response: %v", ErrLLM, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ChatResponse{}, fmt.Errorf("%w: HTTP %d: %s", ErrLLM, response.StatusCode, safeAPIError(body))
	}
	var decoded ChatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ChatResponse{}, fmt.Errorf("%w: decode response: %v", ErrLLM, err)
	}
	if len(decoded.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("%w: response has no choices", ErrLLM)
	}
	return decoded, nil
}

func safeAPIError(body []byte) string {
	value := strings.TrimSpace(string(body))
	if value == "" {
		return "empty response body"
	}
	if len(value) > 500 {
		value = value[:500] + "…"
	}
	return value
}
