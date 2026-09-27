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
)

var ErrLLM = errors.New("LLM API error")

type DeepSeekClient struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

func NewDeepSeekClient(apiKey, baseURL string, client *http.Client) *DeepSeekClient {
	if client == nil {
		client = http.DefaultClient
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
		return ChatResponse{}, fmt.Errorf("encode LLM request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("create LLM request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	response, err := c.HTTPClient.Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("%w: request failed: %v", ErrLLM, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("%w: read response: %v", ErrLLM, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ChatResponse{}, fmt.Errorf("%w: HTTP %d: %s", ErrLLM, response.StatusCode, llmError(body))
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

func llmError(body []byte) string {
	var envelope struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error != nil {
		if encoded, err := json.Marshal(envelope.Error); err == nil {
			return string(encoded)
		}
	}
	value := strings.TrimSpace(string(body))
	if value == "" {
		return "empty response body"
	}
	if len(value) > 500 {
		return value[:500] + "…"
	}
	return value
}
