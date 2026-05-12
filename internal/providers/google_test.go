package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoogle_Review_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1beta/models/gemini-2.5-pro:generateContent", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		assert.Empty(t, r.URL.Query().Get("key"))

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))
		genCfg, ok := req["generationConfig"].(map[string]any)
		require.True(t, ok, "generationConfig should be an object")
		assert.Contains(t, genCfg, "responseJsonSchema", "raw JSON Schema must be passed under responseJsonSchema, not responseSchema")
		assert.NotContains(t, genCfg, "responseSchema", "responseSchema is for Gemini's OpenAPI-Schema-subset, not raw JSON Schema")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [{"text": "{\"verdict\":\"pass\",\"findings\":[],\"next_action\":\"ok\"}"}]}
			}],
			"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 4},
			"modelVersion": "gemini-2.5-pro"
		}`))
	}))
	defer srv.Close()

	rv := NewGoogle("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model:      "gemini-2.5-pro",
		System:     "sys",
		User:       "usr",
		MaxTokens:  1024,
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "gemini-2.5-pro", resp.Model)
	assert.Equal(t, 5, resp.InputTokens)
	assert.Equal(t, 4, resp.OutputTokens)
	assert.JSONEq(t, `{"verdict":"pass","findings":[],"next_action":"ok"}`, string(resp.RawJSON))
}

func TestGoogle_Review_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	rv := NewGoogle("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{
		Model:      "gemini-2.5-pro",
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestGoogle_Review_TruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"{}"}]}}],
			"usageMetadata": {"promptTokenCount": 1, "candidatesTokenCount": 1},
			"modelVersion": "gemini-2.5-pro"
		}`))
	}))
	defer srv.Close()

	rv := NewGoogle("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{Model: "gemini-2.5-pro", JSONSchema: []byte(`{"type":"object"}`)})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrResponseTruncated))
}

func TestGoogle_TruncatedResponseReturnsPartialBytes(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"finishReason": "MAX_TOKENS",
				"content": {"parts": [{"text": "{\"verdict\":\"warn\",\"findings\":[{\"severity\":\"major\""}]}
			}],
			"usageMetadata": {"promptTokenCount": 300, "candidatesTokenCount": 4096},
			"modelVersion": "gemini-2.5-pro"
		}`))
	}))
	defer srv.Close()

	rv := NewGoogle("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model: "gemini-2.5-pro", System: "s", User: "u",
		MaxTokens: 4096, JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrResponseTruncated))
	assert.NotEmpty(t, resp.RawJSON, "truncated response should still carry partial bytes")
	assert.Contains(t, string(resp.RawJSON), `"severity":"major"`)
	assert.Equal(t, "gemini-2.5-pro", resp.Model)
	assert.Equal(t, 300, resp.InputTokens)
	assert.Equal(t, 4096, resp.OutputTokens)
}

func TestGoogle_Review_TimeoutIncludesDurationAndEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	rv := NewGoogle("k", srv.URL, 1*time.Millisecond)
	_, err := rv.Review(context.Background(), Request{
		Model:      "gemini-2.5-pro",
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "google: request timeout 1ms exceeded")
	assert.Contains(t, err.Error(), "ANTI_TANGENT_REQUEST_TIMEOUT")
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
}
