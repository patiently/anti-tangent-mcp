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

func TestGoogle_Review_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.True(t, strings.HasPrefix(r.URL.Path, "/v1beta/models/gemini-2.5-pro:generateContent"))
		assert.Equal(t, "test-key", r.URL.Query().Get("key"))

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))

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
