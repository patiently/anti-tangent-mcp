package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
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

func TestAnthropic_Review_TruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_x",
			"model": "claude-sonnet-4-6",
			"stop_reason": "max_tokens",
			"content": [{"type":"text","text":"truncated"}],
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`))
	}))
	defer srv.Close()

	rv := NewAnthropic("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{Model: "claude-sonnet-4-6", JSONSchema: []byte(`{"type":"object"}`)})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrResponseTruncated))
}

func TestAnthropic_TruncatedResponseReturnsPartialBytes(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "claude-opus-4-7",
			"stop_reason": "max_tokens",
			"content": [
				{"type": "tool_use", "input": {"verdict":"warn","findings":[{"severity":"major"}]}}
			],
			"usage": {"input_tokens": 200, "output_tokens": 4096}
		}`))
	}))
	defer srv.Close()

	rv := NewAnthropic("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model: "claude-opus-4-7", System: "s", User: "u",
		MaxTokens: 4096, JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrResponseTruncated))
	assert.NotEmpty(t, resp.RawJSON, "truncated response should still carry partial bytes")
	assert.Contains(t, string(resp.RawJSON), `"severity":"major"`)
	assert.Equal(t, "claude-opus-4-7", resp.Model)
	assert.Equal(t, 200, resp.InputTokens)
	assert.Equal(t, 4096, resp.OutputTokens)
}

func TestAnthropic_Review_TimeoutIncludesDurationAndEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	rv := NewAnthropic("k", srv.URL, 1*time.Millisecond)
	_, err := rv.Review(context.Background(), Request{
		Model:      "claude-sonnet-4-6",
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anthropic: request timeout 1ms exceeded")
	assert.Contains(t, err.Error(), "ANTI_TANGENT_REQUEST_TIMEOUT")
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
}

// TestAnthropic_Review_PerTaskSchemaNullableSameAs sends the real per-task
// reviewer schema (verdict.Schema()) and checks two things end to end: the
// request the fake server received embeds same_as as a nullable string in
// its input_schema, and a canned tool_use response carrying same_as "f_…"
// on one finding and null on another survives verdict.Parse with those exact
// values. This is the unit-level counterpart to the live call in
// schema_e2e_test.go's TestPerTaskSchema_E2E_NullableSameAs.
func TestAnthropic_Review_PerTaskSchemaNullableSameAs(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotBody))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_x",
			"model": "claude-sonnet-4-6",
			"content": [{
				"type": "tool_use",
				"id": "tu_1",
				"name": "submit_review",
				"input": {
					"verdict": "warn",
					"findings": [
						{"severity":"minor","category":"quality","criterion":"first","evidence":"e","suggestion":"s","same_as":"f_0123abcd"},
						{"severity":"minor","category":"quality","criterion":"second","evidence":"e","suggestion":"s","same_as":null}
					],
					"next_action": "none"
				}
			}],
			"usage": {"input_tokens": 10, "output_tokens": 7}
		}`))
	}))
	defer srv.Close()

	rv := NewAnthropic("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model:      "claude-sonnet-4-6",
		System:     "sys",
		User:       "usr",
		MaxTokens:  1024,
		JSONSchema: verdict.Schema(),
	})
	require.NoError(t, err)

	tools, ok := gotBody["tools"].([]any)
	require.True(t, ok, "tools should be an array")
	require.NotEmpty(t, tools)
	inputSchema, ok := tools[0].(map[string]any)["input_schema"].(map[string]any)
	require.True(t, ok, "tools[0].input_schema should be an object")
	assert.Equal(t, []any{"string", "null"}, sameAsFindingType(t, inputSchema))

	result, err := verdict.Parse(resp.RawJSON)
	require.NoError(t, err)
	require.Len(t, result.Findings, 2)
	require.NotNil(t, result.Findings[0].SameAs)
	assert.Equal(t, "f_0123abcd", *result.Findings[0].SameAs)
	assert.Nil(t, result.Findings[1].SameAs)
}

func TestAnthropicCachePrefix(t *testing.T) {
	capture := func(t *testing.T, req Request) map[string]any {
		t.Helper()
		var got map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode request body: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"m","stop_reason":"tool_use",
				"content":[{"type":"tool_use","input":{"ok":true}}],
				"usage":{"input_tokens":10,"output_tokens":2,
				         "cache_read_input_tokens":7,"cache_creation_input_tokens":3}}`))
		}))
		defer srv.Close()
		_, err := NewAnthropic("k", srv.URL, 5*time.Second).Review(context.Background(), req)
		require.NoError(t, err)
		return got
	}

	base := Request{Model: "m", System: "sys", User: "tail", MaxTokens: 100, JSONSchema: []byte(`{"type":"object"}`)}

	t.Run("no prefix keeps a plain string content", func(t *testing.T) {
		got := capture(t, base)
		msgs := got["messages"].([]any)
		content := msgs[0].(map[string]any)["content"]
		assert.Equal(t, "tail", content, "unchanged wire shape when caching is off")
	})

	t.Run("prefix produces two blocks with cache_control on the first", func(t *testing.T) {
		req := base
		req.CachePrefix = "head"
		got := capture(t, req)

		blocks := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
		require.Len(t, blocks, 2)

		first := blocks[0].(map[string]any)
		assert.Equal(t, "head", first["text"])
		assert.Equal(t, map[string]any{"type": "ephemeral"}, first["cache_control"])

		second := blocks[1].(map[string]any)
		assert.Equal(t, "tail", second["text"])
		assert.NotContains(t, second, "cache_control", "only the prefix is a breakpoint")
	})

	t.Run("cache usage is surfaced", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"m","stop_reason":"tool_use",
				"content":[{"type":"tool_use","input":{"ok":true}}],
				"usage":{"input_tokens":10,"output_tokens":2,
				         "cache_read_input_tokens":7,"cache_creation_input_tokens":3}}`))
		}))
		defer srv.Close()
		resp, err := NewAnthropic("k", srv.URL, 5*time.Second).Review(context.Background(), base)
		require.NoError(t, err)
		assert.Equal(t, 7, resp.CacheReadInputTokens)
		assert.Equal(t, 3, resp.CacheCreationInputTokens)
	})
}
