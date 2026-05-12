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

func TestOpenAI_Review_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "gpt-5", req["model"])
		assert.Contains(t, req, "max_completion_tokens")
		assert.EqualValues(t, 1024, req["max_completion_tokens"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "gpt-5",
			"choices": [{
				"message": {"role":"assistant","content":"{\"verdict\":\"pass\",\"findings\":[],\"next_action\":\"ok\"}"}
			}],
			"usage": {"prompt_tokens": 12, "completion_tokens": 8}
		}`))
	}))
	defer srv.Close()

	rv := NewOpenAI("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model:      "gpt-5",
		System:     "sys",
		User:       "usr",
		MaxTokens:  1024,
		JSONSchema: []byte(`{"type":"object","properties":{"verdict":{"type":"string"}}}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-5", resp.Model)
	assert.Equal(t, 12, resp.InputTokens)
	assert.Equal(t, 8, resp.OutputTokens)
	assert.JSONEq(t, `{"verdict":"pass","findings":[],"next_action":"ok"}`, string(resp.RawJSON))
}

func TestOpenAI_Review_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	rv := NewOpenAI("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{
		Model:      "gpt-5",
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestOpenAI_Review_TruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "gpt-5",
			"choices": [{"finish_reason":"length","message":{"role":"assistant","content":"{}"}}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 1}
		}`))
	}))
	defer srv.Close()

	rv := NewOpenAI("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{Model: "gpt-5", JSONSchema: []byte(`{"type":"object"}`)})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrResponseTruncated))
}

func TestOpenAI_Review_TimeoutIncludesDurationAndEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	rv := NewOpenAI("k", srv.URL, 1*time.Millisecond)
	_, err := rv.Review(context.Background(), Request{
		Model:      "gpt-5",
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openai: request timeout 1ms exceeded")
	assert.Contains(t, err.Error(), "ANTI_TANGENT_REQUEST_TIMEOUT")
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
}
