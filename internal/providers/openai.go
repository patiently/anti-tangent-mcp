package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type openaiReviewer struct {
	apiKey  string
	baseURL string
	timeout time.Duration
	client  *http.Client
}

func NewOpenAI(apiKey, baseURL string, timeout time.Duration) Reviewer {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &openaiReviewer{
		apiKey:  apiKey,
		baseURL: baseURL,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

func (r *openaiReviewer) Name() string { return "openai" }

func (r *openaiReviewer) Review(ctx context.Context, req Request) (Response, error) {
	var schema map[string]any
	if err := json.Unmarshal(req.JSONSchema, &schema); err != nil {
		return Response{}, fmt.Errorf("openai: invalid schema: %w", err)
	}

	body := map[string]any{
		"model": req.Model,
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.User},
		},
		"max_completion_tokens": req.MaxTokens,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "review",
				"strict": true,
				"schema": schema,
			},
		},
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("openai: marshal request body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", r.baseURL+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Response{}, fmt.Errorf("openai: request timeout %s exceeded (set ANTI_TANGENT_REQUEST_TIMEOUT to raise): %w", r.timeout, err)
		}
		return Response{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("openai: read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return Response{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: no choices in response")
	}

	return Response{
		RawJSON:      []byte(parsed.Choices[0].Message.Content),
		Model:        parsed.Model,
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}, nil
}
