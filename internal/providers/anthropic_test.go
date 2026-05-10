package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropic_Review_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "claude-sonnet-4-6", req["model"])

		// Anthropic returns tool_use content blocks; we shape one here.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_x",
			"model": "claude-sonnet-4-6",
			"content": [{
				"type": "tool_use",
				"id": "tu_1",
				"name": "submit_review",
				"input": {"verdict":"pass","findings":[],"next_action":"ship"}
			}],
			"usage": {"input_tokens": 10, "output_tokens": 7}
		}`))
	}))
	defer srv.Close()

	rv := NewAnthropic("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model:      "claude-sonnet-4-6",
		System:     "be exact",
		User:       "review this",
		MaxTokens:  1024,
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-4-6", resp.Model)
	assert.Equal(t, 10, resp.InputTokens)
	assert.Equal(t, 7, resp.OutputTokens)
	assert.JSONEq(t, `{"verdict":"pass","findings":[],"next_action":"ship"}`, string(resp.RawJSON))
}

func TestAnthropic_Review_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	rv := NewAnthropic("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{
		Model:      "claude-sonnet-4-6",
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
}

func TestAnthropic_Review_NoToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [{"type":"text","text":"sorry I can't"}],
			"usage": {"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer srv.Close()

	rv := NewAnthropic("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{
		Model:      "claude-sonnet-4-6",
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "tool_use")
}
