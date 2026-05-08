package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type anthropicReviewer struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewAnthropic(apiKey, baseURL string, timeout time.Duration) Reviewer {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &anthropicReviewer{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

func (r *anthropicReviewer) Name() string { return "anthropic" }

func (r *anthropicReviewer) Review(ctx context.Context, req Request) (Response, error) {
	var schema map[string]any
	if err := json.Unmarshal(req.JSONSchema, &schema); err != nil {
		return Response{}, fmt.Errorf("anthropic: invalid schema: %w", err)
	}

	body := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"system":     req.System,
		"messages": []map[string]any{
			{"role": "user", "content": req.User},
		},
		"tools": []map[string]any{{
			"name":         "submit_review",
			"description":  "Submit the structured review verdict.",
			"input_schema": schema,
		}},
		"tool_choice": map[string]any{"type": "tool", "name": "submit_review"},
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: marshal request body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", r.baseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", r.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed struct {
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	for _, c := range parsed.Content {
		if c.Type == "tool_use" && len(c.Input) > 0 {
			return Response{
				RawJSON:      []byte(c.Input),
				Model:        parsed.Model,
				InputTokens:  parsed.Usage.InputTokens,
				OutputTokens: parsed.Usage.OutputTokens,
			}, nil
		}
	}
	return Response{}, fmt.Errorf("anthropic: no tool_use block in response")
}
