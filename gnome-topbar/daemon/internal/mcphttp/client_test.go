package mcphttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
				"content": []map[string]any{{"type": "text", "text": "hello-note"}},
				"isError": false,
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

// TestCallToolRecoversFromStaleSession simulates the Basic Memory server
// restarting (or expiring the session) mid-lifetime: the first call establishes
// a session and succeeds, then the server evicts it and answers the stale
// session id with HTTP 404 "Session not found". The client must transparently
// drop the dead session, re-initialize, and retry the call once — matching the
// MCP streamable-HTTP requirement that a 404 to a session-bearing request forces
// a fresh InitializeRequest.
func TestCallToolRecoversFromStaleSession(t *testing.T) {
	var mu sync.Mutex
	inits := 0
	valid := "" // session id the server currently accepts ("" == none)
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     *int   `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		switch req.Method {
		case "initialize":
			mu.Lock()
			inits++
			valid = fmt.Sprintf("sess-%d", inits)
			sid := valid
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			sse(w, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"protocolVersion": "2025-06-18"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			mu.Lock()
			got := r.Header.Get("Mcp-Session-Id")
			if got == "" || got != valid {
				mu.Unlock()
				// Unknown/evicted session: respond exactly as Basic Memory does.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"server-error","error":{"code":-32600,"message":"Session not found"}}`)
				return
			}
			calls++
			n := calls
			if n == 1 {
				valid = "" // evict after the first call: the server "restarted"
			}
			mu.Unlock()
			sse(w, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("call-%d", n)}},
			}})
		default:
			t.Errorf("unexpected method %q", req.Method)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())

	got, err := c.CallTool(context.Background(), "search_notes", map[string]any{"query": "x"})
	if err != nil {
		t.Fatalf("first CallTool: %v", err)
	}
	if got != "call-1" {
		t.Fatalf("first call got %q want call-1", got)
	}

	// Session sess-1 is now evicted. The next call sends the stale id, gets 404,
	// and must recover by re-initializing (sess-2) and retrying.
	got, err = c.CallTool(context.Background(), "search_notes", map[string]any{"query": "y"})
	if err != nil {
		t.Fatalf("second CallTool should recover from stale-session 404, got err: %v", err)
	}
	if got != "call-2" {
		t.Fatalf("second call got %q want call-2", got)
	}

	mu.Lock()
	gotInits := inits
	mu.Unlock()
	if gotInits != 2 {
		t.Fatalf("expected 2 initialize handshakes (initial + one recovery), got %d", gotInits)
	}
}

// TestCallToolInitialize404FailsFast guards the recovery path against masking a
// genuinely bad endpoint: a 404 on a request that carried NO session id (e.g.
// the very first initialize against a wrong URL) is a real error, not a stale
// session, so it must surface immediately without triggering the re-init/retry
// loop.
func TestCallToolInitialize404FailsFast(t *testing.T) {
	var mu sync.Mutex
	inits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"initialize"`) {
			mu.Lock()
			inits++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":-32600,"message":"Not Found"}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", srv.Client())
	_, err := c.CallTool(context.Background(), "search_notes", nil)
	if err == nil {
		t.Fatal("expected error when the endpoint always 404s")
	}
	if errors.Is(err, errStaleSession) {
		t.Fatalf("a 404 with no session id must not be treated as a stale session: %v", err)
	}
	mu.Lock()
	gotInits := inits
	mu.Unlock()
	if gotInits != 1 {
		t.Fatalf("expected exactly 1 initialize attempt (no recovery loop), got %d", gotInits)
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
