package mcphttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sse(w http.ResponseWriter, payload any) {
	b, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "event: message\ndata: "+string(b)+"\n\n")
}

func TestCallToolHandshakeAndResult(t *testing.T) {
	var sawInit, sawInitialized, sawCall bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     *int   `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		switch req.Method {
		case "initialize":
			sawInit = true
			w.Header().Set("Mcp-Session-Id", "sess-123")
			sse(w, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"protocolVersion": "2025-06-18"}})
		case "notifications/initialized":
			sawInitialized = true
			if r.Header.Get("Mcp-Session-Id") != "sess-123" {
				t.Errorf("initialized missing session id")
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			sawCall = true
			if r.Header.Get("Mcp-Session-Id") != "sess-123" {
				t.Errorf("call missing session id")
			}
			sse(w, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content":  []map[string]any{{"type": "text", "text": "hello-note"}},
				"isError":  false,
			}})
		default:
			t.Errorf("unexpected method %q", req.Method)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	got, err := c.CallTool(context.Background(), "read_note", map[string]any{"identifier": "x"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got != "hello-note" {
		t.Fatalf("got %q want hello-note", got)
	}
	if !sawInit || !sawInitialized || !sawCall {
		t.Fatalf("handshake incomplete: init=%v initialized=%v call=%v", sawInit, sawInitialized, sawCall)
	}
}

func TestCallToolPropagatesIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "initialize") {
			w.Header().Set("Mcp-Session-Id", "s")
			sse(w, map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{}})
			return
		}
		if strings.Contains(string(body), "tools/call") {
			sse(w, map[string]any{"jsonrpc": "2.0", "id": 2, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "boom"}},
				"isError": true,
			}})
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", srv.Client())
	if _, err := c.CallTool(context.Background(), "read_note", nil); err == nil {
		t.Fatal("expected error when isError=true")
	}
}
