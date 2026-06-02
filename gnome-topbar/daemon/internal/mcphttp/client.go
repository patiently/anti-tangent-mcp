// Package mcphttp is a minimal MCP streamable-HTTP client supporting exactly
// the handshake (initialize + initialized) and tools/call needed by this
// daemon. Responses are Server-Sent Events; the first data: frame carries the
// JSON-RPC message.
package mcphttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type Client struct {
	url   string
	token string
	hc    *http.Client

	handshakeMu sync.Mutex // serializes the initialize handshake (one at a time)

	mu        sync.Mutex
	sessionID string
	ready     bool
	nextID    int
}

func New(url, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{url: url, token: token, hc: hc}
}

type rpcResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

type rpcResponse struct {
	Result rpcResult       `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// CallTool ensures the session is initialized, then invokes name with args and
// returns result.content[0].text.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if err := c.ensureReady(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	resp, err := c.post(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Error) > 0 {
		return "", fmt.Errorf("mcp error: %s", string(resp.Error))
	}
	if resp.Result.IsError {
		text := ""
		if len(resp.Result.Content) > 0 {
			text = resp.Result.Content[0].Text
		}
		return "", fmt.Errorf("tool %s reported error: %s", name, text)
	}
	if len(resp.Result.Content) == 0 {
		return "", fmt.Errorf("tool %s returned no content", name)
	}
	return resp.Result.Content[0].Text, nil
}

func (c *Client) ensureReady(ctx context.Context) error {
	// Serialize the handshake: concurrent CallTool calls (e.g. a scheduled BM
	// poll overlapping a manual Refresh) must not both run initialize, which
	// would open duplicate sessions and race the session-id/ready state.
	c.handshakeMu.Lock()
	defer c.handshakeMu.Unlock()

	c.mu.Lock()
	ready := c.ready
	c.mu.Unlock()
	if ready {
		return nil
	}

	if _, err := c.post(ctx, map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "gnome-topbar", "version": "0.1.0"},
		},
	}); err != nil {
		return err
	}
	// initialized is a notification (no id, no result expected)
	if err := c.postNotify(ctx, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}); err != nil {
		return err
	}
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	return nil
}

func (c *Client) newReq(ctx context.Context, payload any) (*http.Request, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Unlock()
	return req, nil
}

func (c *Client) post(ctx context.Context, payload any) (rpcResponse, error) {
	var out rpcResponse
	req, err := c.newReq(ctx, payload)
	if err != nil {
		return out, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("mcp http %d", resp.StatusCode)
	}
	data, err := firstSSEData(resp.Body)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decode mcp response: %w", err)
	}
	return out, nil
}

// postNotify sends a notification and discards any response body.
func (c *Client) postNotify(ctx context.Context, payload any) error {
	req, err := c.newReq(ctx, payload)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("notify http %d", resp.StatusCode)
	}
	return nil
}

// firstSSEData reads an SSE stream and returns the payload of the first event,
// concatenating multiple `data:` lines with newlines per the SSE spec (so a
// JSON message split across data lines is reassembled correctly).
func firstSSEData(r io.Reader) ([]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var buf bytes.Buffer
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			// SSE strips a single leading space after "data:".
			buf.WriteString(strings.TrimPrefix(line[len("data:"):], " "))
			continue
		}
		if line == "" && buf.Len() > 0 {
			return buf.Bytes(), nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if buf.Len() > 0 {
		return buf.Bytes(), nil
	}
	return nil, fmt.Errorf("no SSE data frame in response")
}
