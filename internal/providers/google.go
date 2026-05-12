package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type googleReviewer struct {
	apiKey  string
	baseURL string
	timeout time.Duration
	client  *http.Client
}

func NewGoogle(apiKey, baseURL string, timeout time.Duration) Reviewer {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	return &googleReviewer{
		apiKey:  apiKey,
		baseURL: baseURL,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

func (r *googleReviewer) Name() string { return "google" }

func (r *googleReviewer) Review(ctx context.Context, req Request) (Response, error) {
	var schema map[string]any
	if err := json.Unmarshal(req.JSONSchema, &schema); err != nil {
		return Response{}, fmt.Errorf("google: invalid schema: %w", err)
	}

	body := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": req.System}},
		},
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]string{{"text": req.User}},
		}},
		"generationConfig": map[string]any{
			"maxOutputTokens":    req.MaxTokens,
			"responseMimeType":   "application/json",
			"responseJsonSchema": schema,
		},
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("google: marshal request body: %w", err)
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent",
		r.baseURL, url.PathEscape(req.Model))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(buf))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", r.apiKey)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Response{}, fmt.Errorf("google: request timeout %s exceeded (set ANTI_TANGENT_REQUEST_TIMEOUT to raise): %w", r.timeout, err)
		}
		return Response{}, fmt.Errorf("google: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("google: read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("google: HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return Response{}, fmt.Errorf("google: decode response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return Response{}, fmt.Errorf("google: empty candidates in response")
	}

	return Response{
		RawJSON:      []byte(parsed.Candidates[0].Content.Parts[0].Text),
		Model:        parsed.ModelVersion,
		InputTokens:  parsed.UsageMetadata.PromptTokenCount,
		OutputTokens: parsed.UsageMetadata.CandidatesTokenCount,
	}, nil
}
