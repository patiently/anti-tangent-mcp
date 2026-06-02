# gnome-topbar MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a GNOME Shell top-bar extension (thin gjs panel) backed by a Go daemon. The daemon aggregates GitHub PRs + Basic Memory todos/search/currently-working-on and computes/deduplicates notification events; the gjs panel renders them and is the component that raises native GNOME notifications (for new review requests and due todos) and POSTs `/ack`.

**Architecture:** A `systemd --user` Go daemon does all I/O (GitHub via `go-gh`, Basic Memory via a minimal MCP streamable-HTTP client, currently-working-on note read) and exposes a loopback JSON API (`127.0.0.1:<port>` + bearer token). The gjs extension polls that API on a GLib timer, renders a dropdown, and raises GNOME notifications. The daemon owns notification dedup (a persisted seen/ack store); the panel acks events it has shown.

**Tech Stack:** Go 1.25 (separate nested module at `gnome-topbar/daemon/`), `github.com/cli/go-gh/v2` (REST, reuses `gh` auth), a hand-rolled MCP streamable-HTTP client (Basic Memory 3.3.1, protocol `2025-06-18`, SSE responses), gjs/GNOME Shell 46 ESM extension with `libsoup` 3, systemd user service.

**Design spec:** `docs/superpowers/specs/2026-06-02-gnome-topbar-mvp-design.md`.

**Privacy (this repo is PUBLIC):** every committed file uses generic placeholders. No real BM namespace, internal ticket content, private repo names, URLs, or tokens in any committed file. Real values live only in `~/.config/gnome-topbar/` (gitignored). Module paths use the repo's public owner (`github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon`) — that is the public repo, not personal data. The extension uuid is `gnome-topbar@localhost`. The only per-operator value, the Basic Memory username, is referenced as `<username>` and read from local config at runtime.

---

## Verified facts (from pre-plan probing — do not re-derive)

- **BM transport** is MCP streamable-HTTP. `initialize` → HTTP 200, `Content-Type: text/event-stream`, response header `Mcp-Session-Id: <id>`, body framed as `event: message\ndata: {<json-rpc>}`. `protocolVersion: "2025-06-18"`, `serverInfo.name: "Basic Memory"`.
- After `initialize`, send a `notifications/initialized` notification (with the `Mcp-Session-Id` header), then `tools/call` (same header). `tools/call` response is SSE; the JSON-RPC `result` has `content: [{type:"text", text:"<payload>"}]`, `structuredContent`, and `isError: bool`.
- `read_note` payload (`content[0].text`) is the **full note markdown including frontmatter** (`---\ntitle:...\n---\n<body>`).
- BM creds reach the daemon via env `BM_URL` and `BM_BEARER_TOKEN`; BM project is `main`; personal namespace prefix is configured (placeholder `<username>`).
- **GitHub** `search/issues` items expose `number`, `title`, `html_url`, `repository_url` (e.g. `https://api.github.com/repos/<owner>/<repo>`), `updated_at`, `user.login`. The active token is a fine-grained PAT and **does** return authored PRs.
- **CI**: `ci.yml` `changelog` job self-skips non-`version/X.Y.Z` branches; root `build-test` runs `go build/test ./...` from repo root (won't descend into a nested module). `release.yml` triggers on push to `main` with no path filter.

---

## File structure

```
.github/workflows/gnome-topbar.yml      # NEW: CI lane for the nested daemon module
.github/workflows/release.yml           # MODIFY: add paths-ignore: ['gnome-topbar/**']

gnome-topbar/
  .gitignore                            # ignore local config samples + build output
  README.md                             # install + first-run + currently-working-on wiring
  config.example.toml                   # placeholders only
  packaging/
    systemd/gnome-topbar-daemon.service # user unit
    Makefile                            # build/install/dev targets
  daemon/                               # SEPARATE go.mod
    go.mod
    cmd/gnome-topbar-daemon/main.go     # wire config, sources, poll loops, server
    internal/
      config/config.go                  # load config.toml + env; bootstrap api token + client.json
      mcphttp/client.go                 # minimal MCP streamable-HTTP client (initialize, tools/call, SSE)
      bm/bm.go                          # Caller interface + Client (ReadNote/SearchNotes)
      bm/todo.go                        # todo markdown parser + due-today logic
      bm/nowworking.go                  # currently-working-on note parser (body + updated)
      bm/search.go                      # epic/story search parsing
      github/github.go                  # go-gh PR fetch + repository_url -> owner/repo
      atstats/atstats.go                # read anti-tangent rollup.json + summary.md (if present)
      state/store.go                    # persisted seen/ack store
      state/state.go                    # Snapshot, Event, SourceStatus, event computation
      server/server.go                  # loopback HTTP/JSON + bearer auth
  extension/
    metadata.json
    daemonClient.js                     # libsoup client; reads ~/.config/gnome-topbar/client.json
    extension.js                        # PanelMenu button, menu, poll timer, render, notifications
    stylesheet.css
```

**Type ownership (defined once, reused everywhere):**
- `github.PR { Repo, Number, Title, URL, Author string; UpdatedAt time.Time }`
- `bm.TodoItem { Text string; Due *time.Time; Overdue bool }`
- `bm.NowWorking { Body string; Updated time.Time; HasUpdated bool }`
- `bm.SearchResult { Title, Type, Permalink, Snippet string }`
- `state.SourceStatus { OK bool; Error string; StaleSince *time.Time }`
- `state.Event { ID, Kind, Title, URL, Body string }`
- `atstats.Stats { Present bool; GeneratedAt time.Time; TotalCalls int; PassPct/WarnPct/FailPct float64; TopCategory string; ReviewMSP95 int64; Summary string; CodeScene *CodeSceneStats }`
- `atstats.CodeSceneStats { Runs int; LatestScore/LatestDelta/ScoreP50 float64; LatestTrend string; Regressions/Improvements/Neutral int; CategoryHistogram map[string]int; WindowStart/WindowEnd time.Time }` (from rollup.json's optional `codescene` key)
- `state.Snapshot { NowWorking bm.NowWorking; PRs {Authored,ReviewRequested []github.PR}; Todos {Active,Due []bm.TodoItem}; Sources map[string]SourceStatus; UnackedEvents []Event; AntiTangent atstats.Stats; GeneratedAt time.Time }`

The JSON the extension consumes is `state.Snapshot` marshaled with the json tags defined in Task 9 / Task 10.

---

### Task 0: CI exemption + module/branch scaffolding

**Files:**
- Create: `gnome-topbar/daemon/go.mod`
- Create: `gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go` (stub so the module builds)
- Create: `gnome-topbar/.gitignore`
- Create: `.github/workflows/gnome-topbar.yml`
- Modify: `.github/workflows/release.yml:3-5`

- [ ] **Step 1: Confirm you are on the feature branch**

Run: `git rev-parse --abbrev-ref HEAD`
Expected: `feat/gnome-topbar` (if not: `git checkout feat/gnome-topbar`)

- [ ] **Step 2: Create the nested Go module**

`gnome-topbar/daemon/go.mod`:
```
module github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon

go 1.25.0
```

- [ ] **Step 3: Add a buildable stub main**

`gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go`:
```go
package main

func main() {}
```

- [ ] **Step 4: Add gnome-topbar/.gitignore**

`gnome-topbar/.gitignore`:
```
/daemon/bin/
*.local.toml
/config.local.toml
```

- [ ] **Step 5: Verify the nested module builds in isolation**

Run: `cd gnome-topbar/daemon && go build ./... && go vet ./... && cd -`
Expected: no output, exit 0.

- [ ] **Step 6: Verify the ROOT module still ignores the nested one**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0 (root tooling does not descend into `gnome-topbar/daemon`).

- [ ] **Step 7: Add a CI lane for the daemon module**

`.github/workflows/gnome-topbar.yml`:
```yaml
name: gnome-topbar

on:
  push:
    branches: ['**']
    paths: ['gnome-topbar/**', '.github/workflows/gnome-topbar.yml']
  pull_request:
    branches: ['**']
    paths: ['gnome-topbar/**', '.github/workflows/gnome-topbar.yml']

permissions:
  contents: read

jobs:
  daemon:
    name: Build & Test daemon (Go)
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: gnome-topbar/daemon
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version: '1.25'
      - run: go mod download
      - run: go vet ./...
      - run: go build ./...
      - run: go test -race -count=1 ./...
```

- [ ] **Step 8: Exempt gnome-topbar from the anti-tangent release flow**

Modify `.github/workflows/release.yml` `on.push` (lines 3-5) to:
```yaml
on:
  push:
    branches: [main]
    paths-ignore: ['gnome-topbar/**']
```

- [ ] **Step 9: Commit**

```bash
git add gnome-topbar/ .github/workflows/gnome-topbar.yml .github/workflows/release.yml
git commit -m "build(gnome-topbar): scaffold nested module + CI lane, exempt from release flow"
```

---

### Task 1: daemon config + token bootstrap

**Files:**
- Create: `gnome-topbar/daemon/internal/config/config.go`
- Test: `gnome-topbar/daemon/internal/config/config_test.go`

Config sources: a TOML file (`~/.config/gnome-topbar/config.toml`) overlaid by env (`BM_URL`, `BM_BEARER_TOKEN`). On load, if no `api_token` exists, generate one and persist both the token and `{port, token}` to `~/.config/gnome-topbar/client.json` (mode 0600) for the extension to read.

- [ ] **Step 1: Add the TOML dependency**

Run: `cd gnome-topbar/daemon && go get github.com/BurntSushi/toml@latest && cd -`
Expected: `go.mod` now requires `github.com/BurntSushi/toml`.

- [ ] **Step 2: Write the failing test**

`gnome-topbar/daemon/internal/config/config_test.go`:
```go
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BM_URL", "http://bm.example/mcp")
	t.Setenv("BM_BEARER_TOKEN", "bm-secret")

	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("bm_username = \"alice\"\nbm_project = \"main\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(cfgPath, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BMURL != "http://bm.example/mcp" || c.BMToken != "bm-secret" {
		t.Fatalf("env not applied: %+v", c)
	}
	if c.BMUsername != "alice" || c.BMProject != "main" {
		t.Fatalf("toml not applied: %+v", c)
	}
	if c.ListenPort == 0 || c.APIToken == "" {
		t.Fatalf("defaults/token missing: %+v", c)
	}

	// client.json must be written for the extension, mode 0600
	cj := filepath.Join(dir, "client.json")
	info, err := os.Stat(cj)
	if err != nil {
		t.Fatalf("client.json missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("client.json mode = %v, want 0600", info.Mode().Perm())
	}
	var client struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	b, _ := os.ReadFile(cj)
	_ = json.Unmarshal(b, &client)
	if client.Token != c.APIToken || client.Port != c.ListenPort {
		t.Fatalf("client.json mismatch: %+v vs cfg %d/%s", client, c.ListenPort, c.APIToken)
	}
}

func TestLoadReusesExistingToken(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte(""), 0o600)
	c1, err := Load(cfgPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Load(cfgPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if c1.APIToken != c2.APIToken {
		t.Fatalf("token not stable across loads: %s vs %s", c1.APIToken, c2.APIToken)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/config/... && cd -`
Expected: FAIL — `undefined: Load` / package has no test target.

- [ ] **Step 4: Implement config**

`gnome-topbar/daemon/internal/config/config.go`:
```go
// Package config loads daemon configuration from a TOML file overlaid by
// environment variables, and bootstraps the loopback API token shared with
// the GNOME extension via client.json.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	BMURL      string `toml:"bm_url"`
	BMToken    string `toml:"bm_bearer_token"`
	BMUsername string `toml:"bm_username"`
	BMProject  string `toml:"bm_project"`
	ListenPort int    `toml:"listen_port"`
	APIToken   string `toml:"api_token"`

	GitHubIntervalSec int `toml:"github_interval_sec"`
	BMIntervalSec     int `toml:"bm_interval_sec"`
	MorningSweepHour  int `toml:"morning_sweep_hour"`
}

// Load reads cfgPath (TOML), applies env + defaults, ensures an API token
// exists, and writes client.json into stateDir for the extension.
func Load(cfgPath, stateDir string) (Config, error) {
	var c Config
	if b, err := os.ReadFile(cfgPath); err == nil {
		if err := toml.Unmarshal(b, &c); err != nil {
			return c, err
		}
	} else if !os.IsNotExist(err) {
		return c, err
	}

	if v := os.Getenv("BM_URL"); v != "" {
		c.BMURL = v
	}
	if v := os.Getenv("BM_BEARER_TOKEN"); v != "" {
		c.BMToken = v
	}
	if c.BMProject == "" {
		c.BMProject = "main"
	}
	if c.ListenPort == 0 {
		c.ListenPort = 47615
	}
	if c.GitHubIntervalSec == 0 {
		c.GitHubIntervalSec = 120
	}
	if c.BMIntervalSec == 0 {
		c.BMIntervalSec = 300
	}
	if c.MorningSweepHour == 0 {
		c.MorningSweepHour = 8
	}
	// Reuse a previously-bootstrapped token: if the TOML carries no api_token,
	// read the one written to client.json on a prior load so the token stays
	// stable across daemon restarts (the extension caches it).
	if c.APIToken == "" {
		if b, err := os.ReadFile(filepath.Join(stateDir, "client.json")); err == nil {
			var existing struct {
				Token string `json:"token"`
			}
			if json.Unmarshal(b, &existing) == nil {
				c.APIToken = existing.Token
			}
		}
	}
	if c.APIToken == "" {
		t, err := randomToken()
		if err != nil {
			return c, err
		}
		c.APIToken = t
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return c, err
	}
	client := struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}{c.ListenPort, c.APIToken}
	b, err := json.Marshal(client)
	if err != nil {
		return c, err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "client.json"), b, 0o600); err != nil {
		return c, err
	}
	return c, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/config/... && cd -`
Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): daemon config load + api token bootstrap"
```

---

### Task 2: minimal MCP streamable-HTTP client

**Files:**
- Create: `gnome-topbar/daemon/internal/mcphttp/client.go`
- Test: `gnome-topbar/daemon/internal/mcphttp/client_test.go`

A client that lazily performs `initialize` (capturing `Mcp-Session-Id`), sends `notifications/initialized`, then exposes `CallTool(ctx, name, args) (string, error)` returning `result.content[0].text`. Responses are SSE; parse the first `data:` JSON-RPC message.

- [ ] **Step 1: Write the failing test (httptest SSE fake)**

`gnome-topbar/daemon/internal/mcphttp/client_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/mcphttp/... && cd -`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Implement the client**

`gnome-topbar/daemon/internal/mcphttp/client.go`:
```go
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
	c.mu.Lock()
	if c.ready {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

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

// firstSSEData reads an SSE stream and returns the JSON payload of the first
// `data:` frame.
func firstSSEData(r io.Reader) ([]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var buf bytes.Buffer
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			buf.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/mcphttp/... && cd -`
Expected: PASS (both tests).

- [ ] **Step 5: Verify against the REAL BM server (integration sanity, not committed as a test)**

Run (from `gnome-topbar/daemon`; uses your real `BM_*` env and namespace — replace `<username>` with your real Basic Memory username for this one-off probe):
```bash
cat > /tmp/mcpprobe.go <<'EOF'
package main
import ("context";"fmt";"net/http";"os";"time"
 mc "github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/mcphttp")
func main(){
 c:=mc.New(os.Getenv("BM_URL"),os.Getenv("BM_BEARER_TOKEN"),&http.Client{Timeout:20*time.Second})
 t,err:=c.CallTool(context.Background(),"read_note",map[string]any{"identifier":os.Getenv("PROBE_NOTE"),"project":"main"})
 if err!=nil{fmt.Println("ERR",err);os.Exit(1)}
 fmt.Println("OK, note bytes:",len(t))
}
EOF
PROBE_NOTE="<username>/todo/main" go run /tmp/mcpprobe.go
```
Expected: `OK, note bytes: <n>` with n > 0. (The probe file is in /tmp, never committed.) Delete `/tmp/mcpprobe.go` after.

- [ ] **Step 6: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): minimal MCP streamable-HTTP client"
```

---

### Task 3: BM client wrapper (Caller seam)

**Files:**
- Create: `gnome-topbar/daemon/internal/bm/bm.go`
- Test: `gnome-topbar/daemon/internal/bm/bm_test.go`

`bm.Client` depends on a `Caller` interface (satisfied by `*mcphttp.Client`) so parsing logic is tested without any HTTP.

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/bm/bm_test.go`:
```go
package bm

import (
	"context"
	"testing"
)

type fakeCaller struct {
	last struct {
		name string
		args map[string]any
	}
	ret string
	err error
}

func (f *fakeCaller) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	f.last.name = name
	f.last.args = args
	return f.ret, f.err
}

func TestReadNotePassesIdentifierAndProject(t *testing.T) {
	fc := &fakeCaller{ret: "note-body"}
	c := New(fc, "main")
	got, err := c.ReadNote(context.Background(), "alice/todo/main")
	if err != nil {
		t.Fatal(err)
	}
	if got != "note-body" {
		t.Fatalf("got %q", got)
	}
	if fc.last.name != "read_note" || fc.last.args["identifier"] != "alice/todo/main" || fc.last.args["project"] != "main" {
		t.Fatalf("bad call: %+v", fc.last)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... && cd -`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Implement bm.Client**

`gnome-topbar/daemon/internal/bm/bm.go`:
```go
// Package bm reads Basic Memory notes/search via an MCP Caller and parses the
// returned markdown into the daemon's domain types.
package bm

import "context"

// Caller is the subset of an MCP client this package needs.
type Caller interface {
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
}

type Client struct {
	caller  Caller
	project string
}

func New(caller Caller, project string) *Client {
	return &Client{caller: caller, project: project}
}

// ReadNote returns the raw markdown (including frontmatter) of a note.
func (c *Client) ReadNote(ctx context.Context, identifier string) (string, error) {
	return c.caller.CallTool(ctx, "read_note", map[string]any{
		"identifier": identifier,
		"project":    c.project,
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... && cd -`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): bm.Client Caller seam + ReadNote"
```

---

### Task 4: todo parser + due-today logic

**Files:**
- Create: `gnome-topbar/daemon/internal/bm/todo.go`
- Test: `gnome-topbar/daemon/internal/bm/todo_test.go`

Grammar: bullets `- [ ]`/`- [x]` under `## Active`; optional leading `[YYYY-MM-DD]` is the due date. Open + due ≤ today → due/overdue.

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/bm/todo_test.go`:
```go
package bm

import (
	"testing"
	"time"
)

const todoFixture = `---
title: alice's todo
type: personal_todo
---

# Todo

## Active

- [ ] [2026-06-01] overdue item
- [ ] [2026-06-02] due today item
- [ ] [2026-06-10] future item
- [ ] no-date item
- [x] [2026-05-01] done item should be ignored

## Done

- [x] archived
`

func TestParseTodos(t *testing.T) {
	today := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	active, due := ParseTodos(todoFixture, today)

	if len(active) != 4 {
		t.Fatalf("active=%d want 4: %+v", len(active), active)
	}
	if len(due) != 2 {
		t.Fatalf("due=%d want 2: %+v", len(due), due)
	}
	// due set = overdue + due-today, both open
	texts := map[string]bool{}
	for _, d := range due {
		texts[d.Text] = true
	}
	if !texts["overdue item"] || !texts["due today item"] {
		t.Fatalf("wrong due items: %+v", due)
	}
	// overdue flagged
	for _, d := range due {
		if d.Text == "overdue item" && !d.Overdue {
			t.Fatal("overdue item not flagged Overdue")
		}
	}
	// no-date item present in active, never in due
	for _, d := range due {
		if d.Text == "no-date item" {
			t.Fatal("no-date item must not be due")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... -run TestParseTodos && cd -`
Expected: FAIL — `undefined: ParseTodos`.

- [ ] **Step 3: Implement the parser**

`gnome-topbar/daemon/internal/bm/todo.go`:
```go
package bm

import (
	"regexp"
	"strings"
	"time"
)

type TodoItem struct {
	Text    string     `json:"text"`
	Due     *time.Time `json:"due,omitempty"`
	Overdue bool       `json:"overdue"`
}

var (
	openRe = regexp.MustCompile(`^- \[ \]\s+(.*)$`)
	doneRe = regexp.MustCompile(`^- \[[xX]\]\s+(.*)$`)
	dateRe = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2})\]\s*(.*)$`)
)

// ParseTodos returns open items under "## Active" and the subset that is due
// today or overdue (relative to today's local date).
func ParseTodos(md string, today time.Time) (active, due []TodoItem) {
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	inActive := false
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.HasPrefix(line, "## ") {
			inActive = strings.EqualFold(strings.TrimSpace(line[3:]), "Active")
			continue
		}
		if !inActive {
			continue
		}
		if doneRe.MatchString(line) {
			continue
		}
		m := openRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		item := TodoItem{Text: strings.TrimSpace(m[1])}
		if dm := dateRe.FindStringSubmatch(item.Text); dm != nil {
			if d, err := time.ParseInLocation("2006-01-02", dm[1], today.Location()); err == nil {
				item.Due = &d
				item.Text = strings.TrimSpace(dm[2])
			}
		}
		active = append(active, item)
		if item.Due != nil && !item.Due.After(day) {
			item.Overdue = item.Due.Before(day)
			due = append(due, item)
		}
	}
	return active, due
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... && cd -`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): todo parser + due-today logic"
```

---

### Task 5: currently-working-on note parser

**Files:**
- Create: `gnome-topbar/daemon/internal/bm/nowworking.go`
- Test: `gnome-topbar/daemon/internal/bm/nowworking_test.go`

Note template the assistant maintains carries frontmatter `updated: <RFC3339>`; parser returns body (after frontmatter) + updated time.

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/bm/nowworking_test.go`:
```go
package bm

import (
	"testing"
	"time"
)

func TestParseNowWorking(t *testing.T) {
	md := `---
title: Currently working on
type: note
updated: 2026-06-02T08:30:00Z
---

Wiring the gnome-topbar daemon; next step is the state aggregator.
`
	nw := ParseNowWorking(md)
	if !nw.HasUpdated {
		t.Fatal("expected HasUpdated")
	}
	want := time.Date(2026, 6, 2, 8, 30, 0, 0, time.UTC)
	if !nw.Updated.Equal(want) {
		t.Fatalf("updated=%v want %v", nw.Updated, want)
	}
	if nw.Body != "Wiring the gnome-topbar daemon; next step is the state aggregator." {
		t.Fatalf("body=%q", nw.Body)
	}
}

func TestParseNowWorkingNoFrontmatter(t *testing.T) {
	nw := ParseNowWorking("just a line\n")
	if nw.HasUpdated {
		t.Fatal("did not expect HasUpdated")
	}
	if nw.Body != "just a line" {
		t.Fatalf("body=%q", nw.Body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... -run TestParseNowWorking && cd -`
Expected: FAIL — `undefined: ParseNowWorking`.

- [ ] **Step 3: Implement the parser**

`gnome-topbar/daemon/internal/bm/nowworking.go`:
```go
package bm

import (
	"strings"
	"time"
)

type NowWorking struct {
	Body       string    `json:"body"`
	Updated    time.Time `json:"updated"`
	HasUpdated bool      `json:"has_updated"`
}

// ParseNowWorking splits optional YAML frontmatter from the body and reads an
// `updated:` RFC3339 timestamp if present.
func ParseNowWorking(md string) NowWorking {
	var nw NowWorking
	body := md
	if strings.HasPrefix(md, "---\n") {
		if end := strings.Index(md[4:], "\n---"); end >= 0 {
			front := md[4 : 4+end]
			rest := md[4+end+len("\n---"):]
			rest = strings.TrimPrefix(rest, "\n")
			body = rest
			for _, line := range strings.Split(front, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "updated:") {
					v := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
					if t, err := time.Parse(time.RFC3339, v); err == nil {
						nw.Updated = t
						nw.HasUpdated = true
					}
				}
			}
		}
	}
	nw.Body = strings.TrimSpace(body)
	return nw
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... && cd -`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): currently-working-on note parser"
```

---

### Task 6: epic/story search

**Files:**
- Create: `gnome-topbar/daemon/internal/bm/search.go`
- Test: `gnome-topbar/daemon/internal/bm/search_test.go`

`SearchEpicsStories` calls `search_notes` with `note_types:["epic","story"]`, `output_format:"json"`, and parses the JSON the server returns (a list of result objects). Because the exact JSON differs by server version, the parser tolerates the common fields and is verified against the real server in Step 6.

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/bm/search_test.go`:
```go
package bm

import (
	"context"
	"testing"
)

func TestSearchEpicsStoriesParsesResults(t *testing.T) {
	payload := `{"results":[
	  {"title":"YN epic","type":"entity","permalink":"monorepo/epics/X/main","content":"do the thing","metadata":{"note_type":"epic"}},
	  {"title":"YN story","type":"entity","permalink":"monorepo/stories/Y/main","content":"a story body","metadata":{"note_type":"story"}}
	]}`
	fc := &fakeCaller{ret: payload}
	c := New(fc, "main")
	res, err := c.SearchEpicsStories(context.Background(), "thing")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("results=%d want 2", len(res))
	}
	if res[0].Title != "YN epic" || res[0].Type != "epic" || res[0].Permalink != "monorepo/epics/X/main" {
		t.Fatalf("bad result[0]: %+v", res[0])
	}
	if fc.last.name != "search_notes" {
		t.Fatalf("called %q", fc.last.name)
	}
	types, _ := fc.last.args["note_types"].([]string)
	if len(types) != 2 || types[0] != "epic" || types[1] != "story" {
		t.Fatalf("note_types=%v", fc.last.args["note_types"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... -run TestSearch && cd -`
Expected: FAIL — `undefined: (*Client).SearchEpicsStories`.

- [ ] **Step 3: Implement search**

`gnome-topbar/daemon/internal/bm/search.go`:
```go
package bm

import (
	"context"
	"encoding/json"
)

type SearchResult struct {
	Title     string `json:"title"`
	Type      string `json:"type"`
	Permalink string `json:"permalink"`
	Snippet   string `json:"snippet"`
}

// SearchEpicsStories runs a Basic Memory search limited to epic/story notes.
func (c *Client) SearchEpicsStories(ctx context.Context, query string) ([]SearchResult, error) {
	raw, err := c.caller.CallTool(ctx, "search_notes", map[string]any{
		"query":         query,
		"note_types":    []string{"epic", "story"},
		"project":       c.project,
		"output_format": "json",
	})
	if err != nil {
		return nil, err
	}
	return parseSearch(raw)
}

func parseSearch(raw string) ([]SearchResult, error) {
	var env struct {
		Results []struct {
			Title     string `json:"title"`
			Type      string `json:"type"` // server returns "entity" for all; real note type is in metadata.note_type
			Permalink string `json:"permalink"`
			Content   string `json:"content"`
			Snippet   string `json:"snippet"`
			Metadata  struct {
				NoteType string `json:"note_type"`
			} `json:"metadata"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(env.Results))
	for _, r := range env.Results {
		// Basic Memory's search_notes returns type:"entity" for every result;
		// the epic/story note type lives in metadata.note_type. Prefer it,
		// falling back to the top-level type only if metadata is absent.
		typ := r.Metadata.NoteType
		if typ == "" {
			typ = r.Type
		}
		snip := r.Snippet
		if snip == "" {
			snip = r.Content
		}
		if len(snip) > 200 {
			snip = snip[:200]
		}
		out = append(out, SearchResult{Title: r.Title, Type: typ, Permalink: r.Permalink, Snippet: snip})
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... && cd -`
Expected: PASS.

- [ ] **Step 5: Verify the real search JSON shape**

Run a throwaway probe analogous to Task 2 Step 6 calling `search_notes` with `output_format:"json"` and `note_types:["epic","story"]`, and print `content[0].text`. Confirm the top-level key is `results` with `title/type/permalink`. If the real shape differs, adjust `parseSearch`'s struct tags accordingly and re-run Step 4 before committing. (Probe in `/tmp`, never committed.)

- [ ] **Step 6: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): epic/story search"
```

---

### Task 7: GitHub PR source

**Files:**
- Create: `gnome-topbar/daemon/internal/github/github.go`
- Test: `gnome-topbar/daemon/internal/github/github_test.go`

`Source` depends on a tiny `restGetter` interface (`Get(path, &resp) error`), satisfied in prod by `api.DefaultRESTClient()` and in tests by a fake.

- [ ] **Step 1: Add go-gh**

Run: `cd gnome-topbar/daemon && go get github.com/cli/go-gh/v2@latest && cd -`

- [ ] **Step 2: Write the failing test**

`gnome-topbar/daemon/internal/github/github_test.go`:
```go
package github

import (
	"encoding/json"
	"testing"
)

type fakeRest struct {
	byPath map[string]string
	calls  []string
}

func (f *fakeRest) Get(path string, resp any) error {
	f.calls = append(f.calls, path)
	body := f.byPath[path]
	return json.Unmarshal([]byte(body), resp)
}

func TestFetchAuthoredMapsFields(t *testing.T) {
	const path = "search/issues?q=is%3Aopen+is%3Apr+author%3A%40me"
	fr := &fakeRest{byPath: map[string]string{
		path: `{"items":[{"number":40,"title":"Fix thing","html_url":"https://github.com/o/r/pull/40","repository_url":"https://api.github.com/repos/o/r","updated_at":"2026-02-26T16:22:36Z","user":{"login":"me"}}]}`,
	}}
	s := New(fr)
	prs, err := s.FetchAuthored()
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("prs=%d", len(prs))
	}
	p := prs[0]
	if p.Repo != "o/r" || p.Number != 40 || p.Title != "Fix thing" || p.Author != "me" {
		t.Fatalf("bad mapping: %+v", p)
	}
	if p.URL != "https://github.com/o/r/pull/40" {
		t.Fatalf("bad url: %s", p.URL)
	}
}

func TestFetchReviewRequestedUsesRightQuery(t *testing.T) {
	const path = "search/issues?q=is%3Aopen+is%3Apr+review-requested%3A%40me"
	fr := &fakeRest{byPath: map[string]string{path: `{"items":[]}`}}
	s := New(fr)
	if _, err := s.FetchReviewRequested(); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 || fr.calls[0] != path {
		t.Fatalf("calls=%v", fr.calls)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/github/... && cd -`
Expected: FAIL — `undefined: New`.

- [ ] **Step 4: Implement the source**

`gnome-topbar/daemon/internal/github/github.go`:
```go
// Package github fetches the operator's open PRs and review-requested PRs via
// the GitHub search API, reusing gh CLI credentials through go-gh in prod.
package github

import (
	"net/url"
	"strings"
	"time"
)

type PR struct {
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Author    string    `json:"author"`
	UpdatedAt time.Time `json:"updated_at"`
}

type restGetter interface {
	Get(path string, resp any) error
}

type Source struct{ rest restGetter }

func New(rest restGetter) *Source { return &Source{rest: rest} }

func (s *Source) FetchAuthored() ([]PR, error) {
	return s.search("is:open is:pr author:@me")
}

func (s *Source) FetchReviewRequested() ([]PR, error) {
	return s.search("is:open is:pr review-requested:@me")
}

type searchItem struct {
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	HTMLURL       string    `json:"html_url"`
	RepositoryURL string    `json:"repository_url"`
	UpdatedAt     time.Time `json:"updated_at"`
	User          struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (s *Source) search(q string) ([]PR, error) {
	path := "search/issues?q=" + url.QueryEscape(q)
	var resp struct {
		Items []searchItem `json:"items"`
	}
	if err := s.rest.Get(path, &resp); err != nil {
		return nil, err
	}
	out := make([]PR, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, PR{
			Repo:      repoFromURL(it.RepositoryURL),
			Number:    it.Number,
			Title:     it.Title,
			URL:       it.HTMLURL,
			Author:    it.User.Login,
			UpdatedAt: it.UpdatedAt,
		})
	}
	return out, nil
}

func repoFromURL(repoURL string) string {
	const marker = "/repos/"
	if i := strings.Index(repoURL, marker); i >= 0 {
		return repoURL[i+len(marker):]
	}
	return repoURL
}
```

> **Note:** `url.QueryEscape` encodes spaces as `+` and `:`/`@` as `%3A`/`%40`, matching the test's expected `path`. Keep the test's expected string in sync if you change the query.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/github/... && cd -`
Expected: PASS.

- [ ] **Step 6: Add the prod constructor helper**

Append to `github.go`:
```go
// Note: the prod REST client is constructed in main via
// api.DefaultRESTClient() from github.com/cli/go-gh/v2/pkg/api, whose
// *RESTClient satisfies restGetter (it has Get(path string, resp any) error).
```
(No code change needed beyond the comment; wiring happens in Task 11.)

- [ ] **Step 7: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): github PR source via go-gh"
```

---

### Task 8: persisted seen/ack store

**Files:**
- Create: `gnome-topbar/daemon/internal/state/store.go`
- Test: `gnome-topbar/daemon/internal/state/store_test.go`

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/state/store_test.go`:
```go
package state

import (
	"path/filepath"
	"testing"
)

func TestStoreSeenAckRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seen.json")
	s, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsNew("pr:o/r#1") {
		t.Fatal("expected new before ack")
	}
	if err := s.MarkSeen([]string{"pr:o/r#1"}); err != nil {
		t.Fatal(err)
	}
	if s.IsNew("pr:o/r#1") {
		t.Fatal("expected not-new after ack")
	}
	// reload persists
	s2, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.IsNew("pr:o/r#1") {
		t.Fatal("ack did not persist across reload")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/state/... && cd -`
Expected: FAIL — `undefined: LoadStore`.

- [ ] **Step 3: Implement the store**

`gnome-topbar/daemon/internal/state/store.go`:
```go
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	path string
	mu   sync.Mutex
	seen map[string]bool
}

func LoadStore(path string) (*Store, error) {
	s := &Store{path: path, seen: map[string]bool{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
		s.seen[id] = true
	}
	return s, nil
}

func (s *Store) IsNew(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.seen[id]
}

func (s *Store) MarkSeen(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.seen[id] = true
	}
	return s.persist()
}

func (s *Store) persist() error {
	ids := make([]string, 0, len(s.seen))
	for id := range s.seen {
		ids = append(ids, id)
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/state/... && cd -`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): persisted seen/ack store"
```

---

### Task 9: snapshot + event computation

**Files:**
- Create: `gnome-topbar/daemon/internal/state/state.go`
- Test: `gnome-topbar/daemon/internal/state/state_test.go`

Defines `Snapshot`, `Event`, `SourceStatus`, and `ComputeEvents(snap, store)` (review-requested PRs + due todos that are `IsNew`).

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/state/state_test.go`:
```go
package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
)

func TestComputeEventsNewOnly(t *testing.T) {
	store, _ := LoadStore(filepath.Join(t.TempDir(), "seen.json"))
	due := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	snap := &Snapshot{}
	snap.PRs.ReviewRequested = []github.PR{{Repo: "o/r", Number: 7, Title: "Please review", URL: "https://x/7"}}
	snap.Todos.Due = []bm.TodoItem{{Text: "ship it", Due: &due, Overdue: true}}

	ev := ComputeEvents(snap, store)
	if len(ev) != 2 {
		t.Fatalf("events=%d want 2: %+v", len(ev), ev)
	}

	// ack one, recompute -> only the other remains
	for _, e := range ev {
		if e.Kind == "review_request" {
			_ = store.MarkSeen([]string{e.ID})
		}
	}
	ev2 := ComputeEvents(snap, store)
	if len(ev2) != 1 || ev2[0].Kind != "todo_due" {
		t.Fatalf("after ack: %+v", ev2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/state/... -run TestComputeEvents && cd -`
Expected: FAIL — `undefined: Snapshot` / `ComputeEvents`.

- [ ] **Step 3: Implement state types + events**

`gnome-topbar/daemon/internal/state/state.go`:
```go
package state

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
)

type SourceStatus struct {
	OK         bool       `json:"ok"`
	Error      string     `json:"error,omitempty"`
	StaleSince *time.Time `json:"stale_since,omitempty"`
}

type Event struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // "review_request" | "todo_due"
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	Body  string `json:"body,omitempty"`
}

type Snapshot struct {
	NowWorking bm.NowWorking `json:"now_working"`
	PRs        struct {
		Authored        []github.PR `json:"authored"`
		ReviewRequested []github.PR `json:"review_requested"`
	} `json:"prs"`
	Todos struct {
		Active []bm.TodoItem `json:"active"`
		Due    []bm.TodoItem `json:"due"`
	} `json:"todos"`
	Sources       map[string]SourceStatus `json:"sources"`
	UnackedEvents []Event                 `json:"unacked_events"`
	GeneratedAt   time.Time               `json:"generated_at"`
}

// ComputeEvents returns events not yet acked in the store. It does not mark
// them seen (the panel acks after showing).
func ComputeEvents(s *Snapshot, store *Store) []Event {
	var out []Event
	for _, pr := range s.PRs.ReviewRequested {
		id := reviewID(pr)
		if store.IsNew(id) {
			out = append(out, Event{ID: id, Kind: "review_request",
				Title: pr.Repo + " #" + strconv.Itoa(pr.Number), URL: pr.URL, Body: pr.Title})
		}
	}
	for _, td := range s.Todos.Due {
		id := todoID(td)
		if store.IsNew(id) {
			out = append(out, Event{ID: id, Kind: "todo_due", Title: "Todo due", Body: td.Text})
		}
	}
	return out
}

func reviewID(pr github.PR) string { return "pr:" + pr.Repo + "#" + strconv.Itoa(pr.Number) }

func todoID(td bm.TodoItem) string {
	d := "nodate"
	if td.Due != nil {
		d = td.Due.Format("2006-01-02")
	}
	h := sha1.Sum([]byte(td.Text))
	return "todo:" + d + ":" + hex.EncodeToString(h[:])[:8]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/state/... && cd -`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): snapshot + event computation"
```

---

### Task 10: loopback HTTP server

**Files:**
- Create: `gnome-topbar/daemon/internal/server/server.go`
- Test: `gnome-topbar/daemon/internal/server/server_test.go`

Endpoints: `GET /state`, `GET /search?q=`, `POST /ack`, `GET /healthz`. All except `/healthz` require `Authorization: Bearer <token>`. The server is constructed with a `Provider` interface so it doesn't import the poller.

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/server/server_test.go`:
```go
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

type fakeProvider struct{ acked []string }

func (f *fakeProvider) Snapshot() state.Snapshot {
	s := state.Snapshot{Sources: map[string]state.SourceStatus{"github": {OK: true}}}
	s.Todos.Active = []bm.TodoItem{{Text: "x"}}
	return s
}
func (f *fakeProvider) Search(ctx context.Context, q string) ([]bm.SearchResult, error) {
	return []bm.SearchResult{{Title: "T", Type: "epic"}}, nil
}
func (f *fakeProvider) Ack(ids []string) { f.acked = append(f.acked, ids...) }

func newTestServer() (*httptest.Server, *fakeProvider) {
	fp := &fakeProvider{}
	h := New(fp, "secret")
	return httptest.NewServer(h), fp
}

func TestStateRequiresBearer(t *testing.T) {
	srv, _ := newTestServer()
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/state")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", resp.StatusCode)
	}
}

func TestStateReturnsSnapshot(t *testing.T) {
	srv, _ := newTestServer()
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/state", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("err=%v status=%v", err, resp.StatusCode)
	}
	var snap state.Snapshot
	_ = json.NewDecoder(resp.Body).Decode(&snap)
	if len(snap.Todos.Active) != 1 {
		t.Fatalf("snapshot not returned: %+v", snap)
	}
}

func TestAck(t *testing.T) {
	srv, fp := newTestServer()
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/ack", strings.NewReader(`{"event_ids":["a","b"]}`))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("err=%v status=%v", err, resp.StatusCode)
	}
	if len(fp.acked) != 2 {
		t.Fatalf("acked=%v", fp.acked)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/server/... && cd -`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Implement the server**

`gnome-topbar/daemon/internal/server/server.go`:
```go
// Package server exposes the daemon snapshot over a loopback, bearer-protected
// JSON API consumed by the GNOME extension.
package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

type Provider interface {
	Snapshot() state.Snapshot
	Search(ctx context.Context, q string) ([]bm.SearchResult, error)
	Ack(ids []string)
}

func New(p Provider, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/state", auth(token, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, p.Snapshot())
	}))
	mux.HandleFunc("/search", auth(token, func(w http.ResponseWriter, r *http.Request) {
		res, err := p.Search(r.Context(), r.URL.Query().Get("q"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]any{"results": res})
	}))
	mux.HandleFunc("/ack", auth(token, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			EventIDs []string `json:"event_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		p.Ack(body.EventIDs)
		writeJSON(w, map[string]any{"acked": len(body.EventIDs)})
	}))
	return mux
}

func auth(token string, next http.HandlerFunc) http.HandlerFunc {
	want := "Bearer " + token
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/server/... && cd -`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): loopback bearer-protected HTTP API"
```

---

### Task 11: main wiring + poll loops

**Files:**
- Modify: `gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go`

Wires config → mcphttp → bm + github → a `Poller` (implements `server.Provider`) → server. Poll goroutines refresh GitHub and BM on their cadences; a morning sweep forces a BM refresh after `MorningSweepHour`. Per-source errors are captured into `Snapshot.Sources`. No new unit test (covered by integration run in Step 3); keep `main` thin.

- [ ] **Step 1: Implement main + Poller**

Replace `gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go`:
```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/config"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/mcphttp"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/server"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	home, _ := os.UserHomeDir()
	cfgDir := filepath.Join(home, ".config", "gnome-topbar")
	stateDir := filepath.Join(home, ".local", "state", "gnome-topbar")
	cfg, err := config.Load(filepath.Join(cfgDir, "config.toml"), cfgDir)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	store, err := state.LoadStore(filepath.Join(stateDir, "seen.json"))
	if err != nil {
		log.Error("store", "err", err)
		os.Exit(1)
	}

	rest, err := api.DefaultRESTClient()
	if err != nil {
		log.Error("github auth (is gh logged in?)", "err", err)
		os.Exit(1)
	}
	gh := github.New(rest)
	mc := mcphttp.New(cfg.BMURL, cfg.BMToken, &http.Client{Timeout: 20 * time.Second})
	bmc := bm.New(mc, cfg.BMProject)

	p := &Poller{
		log: log, cfg: cfg, store: store, gh: gh, bm: bmc,
		snap: state.Snapshot{Sources: map[string]state.SourceStatus{}},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	p.refreshGitHub(ctx)
	p.refreshBM(ctx)
	go p.loop(ctx, time.Duration(cfg.GitHubIntervalSec)*time.Second, p.refreshGitHub)
	go p.loop(ctx, time.Duration(cfg.BMIntervalSec)*time.Second, p.refreshBM)
	go p.morningSweep(ctx)

	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", cfg.ListenPort))
	srv := &http.Server{Addr: addr, Handler: server.New(p, cfg.APIToken)}
	go func() {
		<-ctx.Done()
		sc, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(sc)
	}()
	log.Info("listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

type Poller struct {
	log   *slog.Logger
	cfg   config.Config
	store *state.Store
	gh    *github.Source
	bm    *bm.Client

	mu   sync.RWMutex
	snap state.Snapshot
}

func (p *Poller) loop(ctx context.Context, every time.Duration, fn func(context.Context)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

func (p *Poller) morningSweep(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	done := -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			if now.Hour() == p.cfg.MorningSweepHour && done != now.YearDay() {
				done = now.YearDay()
				p.refreshBM(ctx)
			}
		}
	}
}

func (p *Poller) refreshGitHub(ctx context.Context) {
	authored, err1 := p.gh.FetchAuthored()
	reviews, err2 := p.gh.FetchReviewRequested()
	p.mu.Lock()
	defer p.mu.Unlock()
	st := state.SourceStatus{OK: true}
	if err1 != nil || err2 != nil {
		st = staleStatus(err1, err2)
	} else {
		p.snap.PRs.Authored = authored
		p.snap.PRs.ReviewRequested = reviews
	}
	p.snap.Sources["github"] = st
	p.recompute()
}

func (p *Poller) refreshBM(ctx context.Context) {
	todoMD, errT := p.bm.ReadNote(ctx, p.cfg.BMUsername+"/todo/main")
	nowMD, errN := p.bm.ReadNote(ctx, p.cfg.BMUsername+"/notes/currently-working-on/main")
	p.mu.Lock()
	defer p.mu.Unlock()
	st := state.SourceStatus{OK: true}
	if errT != nil {
		st = staleStatus(errT, nil)
	} else {
		active, due := bm.ParseTodos(todoMD, time.Now())
		p.snap.Todos.Active = active
		p.snap.Todos.Due = due
	}
	if errN == nil {
		p.snap.NowWorking = bm.ParseNowWorking(nowMD)
	}
	p.snap.Sources["basic-memory"] = st
	p.recompute()
}

// recompute refreshes events + timestamp; caller holds p.mu.
func (p *Poller) recompute() {
	p.snap.GeneratedAt = time.Now()
	p.snap.UnackedEvents = state.ComputeEvents(&p.snap, p.store)
}

func staleStatus(errs ...error) state.SourceStatus {
	now := time.Now()
	for _, e := range errs {
		if e != nil {
			return state.SourceStatus{OK: false, Error: e.Error(), StaleSince: &now}
		}
	}
	return state.SourceStatus{OK: true}
}

// server.Provider implementation.
func (p *Poller) Snapshot() state.Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snap
}

func (p *Poller) Search(ctx context.Context, q string) ([]bm.SearchResult, error) {
	return p.bm.SearchEpicsStories(ctx, q)
}

func (p *Poller) Ack(ids []string) {
	_ = p.store.MarkSeen(ids)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recompute()
}
```

- [ ] **Step 2: Build + vet + full test**

Run: `cd gnome-topbar/daemon && go build ./... && go vet ./... && go test -race ./... && cd -`
Expected: build clean, all tests PASS.

- [ ] **Step 3: Integration smoke against real services**

Run (real `gh` + BM env must be present):
```bash
cd gnome-topbar/daemon
go run ./cmd/gnome-topbar-daemon &
DPID=$!
sleep 3
PORT=$(python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/.config/gnome-topbar/client.json')))['port'])")
TOK=$(python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/.config/gnome-topbar/client.json')))['token'])")
curl -s -H "Authorization: Bearer $TOK" "http://127.0.0.1:$PORT/state" | python3 -m json.tool | head -40
kill $DPID
cd -
```
Expected: JSON snapshot with `prs.authored` populated (you have ≥1), `todos`, `sources.github.ok=true`. (First run also writes `~/.config/gnome-topbar/config.toml`? No — you must create it; see note.)

> **Note:** before this smoke run, create `~/.config/gnome-topbar/config.toml` with at least `bm_username = "<your real username>"` (the daemon reads `BM_URL`/`BM_BEARER_TOKEN` from env). This file is outside the repo. The smoke run will fail the BM source until `bm_username` is set, but GitHub should still populate.

- [ ] **Step 4: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): daemon main wiring + poll loops"
```

---

### Task 12: packaging — systemd unit, Makefile, config example, README

**Files:**
- Create: `gnome-topbar/packaging/systemd/gnome-topbar-daemon.service`
- Create: `gnome-topbar/packaging/Makefile`
- Create: `gnome-topbar/config.example.toml`
- Create: `gnome-topbar/README.md`

- [ ] **Step 1: systemd user unit**

`gnome-topbar/packaging/systemd/gnome-topbar-daemon.service`:
```ini
[Unit]
Description=gnome-topbar daemon
After=default.target

[Service]
Type=simple
ExecStart=%h/.local/bin/gnome-topbar-daemon
EnvironmentFile=%h/.config/gnome-topbar/env
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

- [ ] **Step 2: Makefile**

`gnome-topbar/packaging/Makefile`:
```makefile
DAEMON_BIN := $(HOME)/.local/bin/gnome-topbar-daemon
EXT_UUID := gnome-topbar@localhost
EXT_DIR := $(HOME)/.local/share/gnome-shell/extensions/$(EXT_UUID)
UNIT := gnome-topbar-daemon.service

.PHONY: build install-daemon install-extension install enable logs

build:
	cd ../daemon && go build -o $(DAEMON_BIN) ./cmd/gnome-topbar-daemon

install-daemon: build
	mkdir -p $(HOME)/.config/systemd/user
	cp systemd/$(UNIT) $(HOME)/.config/systemd/user/$(UNIT)
	systemctl --user daemon-reload

install-extension:
	mkdir -p $(EXT_DIR)
	cp -r ../extension/* $(EXT_DIR)/

install: install-daemon install-extension

enable:
	systemctl --user enable --now $(UNIT)

logs:
	journalctl --user -u $(UNIT) -f
```

- [ ] **Step 3: config example**

`gnome-topbar/config.example.toml`:
```toml
# Copy to ~/.config/gnome-topbar/config.toml and fill in.
# BM_URL and BM_BEARER_TOKEN are read from the environment (see env file below);
# you usually only need to set bm_username here.

bm_username = "<your-basic-memory-username>"
bm_project  = "main"

# Optional overrides (defaults shown):
# listen_port        = 47615
# github_interval_sec = 120
# bm_interval_sec     = 300
# morning_sweep_hour  = 8
```

- [ ] **Step 4: README (placeholders only)**

`gnome-topbar/README.md`:
```markdown
# gnome-topbar

A GNOME Shell top-bar extension showing your GitHub PRs, Basic Memory todos,
epic/story search, and a "currently working on" summary, backed by a small Go
daemon. See `../docs/superpowers/specs/2026-06-02-gnome-topbar-mvp-design.md`.

## Prerequisites
- GNOME Shell 45/46/47 (Wayland or X11)
- `gh` CLI logged in (`gh auth status`)
- Basic Memory reachable; `BM_URL` + `BM_BEARER_TOKEN` available
- Go 1.25 to build

## Install
1. `cp config.example.toml ~/.config/gnome-topbar/config.toml` and set `bm_username`.
2. Create `~/.config/gnome-topbar/env`:
   ```
   BM_URL=...
   BM_BEARER_TOKEN=...
   ```
3. `cd packaging && make install enable`
4. `gnome-extensions enable gnome-topbar@localhost` (log out/in if the shell
   doesn't pick it up on Wayland).

## Currently-working-on note
Add to your AI assistant config (e.g. `~/.claude/CLAUDE.md`): when you start or
switch tasks, update the Basic Memory note `<username>/notes/currently-working-on/main`
with frontmatter `updated: <RFC3339 timestamp>` and a 1–3 sentence body. The
panel renders the body with a staleness indicator.
```

- [ ] **Step 5: Verify the unit installs and the daemon runs under systemd**

Run (ensure `~/.config/gnome-topbar/config.toml` exists with `bm_username` first — see Task 11 Step 3 note):
```bash
mkdir -p ~/.config/gnome-topbar
printf 'BM_URL=%s\nBM_BEARER_TOKEN=%s\n' "$BM_URL" "$BM_BEARER_TOKEN" > ~/.config/gnome-topbar/env
cd gnome-topbar/packaging && make install-daemon && make enable && sleep 2 \
  && systemctl --user is-active gnome-topbar-daemon && cd -
```
Expected: `active`. Then `journalctl --user -u gnome-topbar-daemon -n 5` shows a `listening` log line.

- [ ] **Step 6: Commit**

```bash
git add gnome-topbar/packaging gnome-topbar/config.example.toml gnome-topbar/README.md
git commit -m "feat(gnome-topbar): packaging (systemd unit, Makefile, README)"
```

---

### Task 13: extension skeleton + /state rendering

**Files:**
- Create: `gnome-topbar/extension/metadata.json`
- Create: `gnome-topbar/extension/daemonClient.js`
- Create: `gnome-topbar/extension/extension.js`
- Create: `gnome-topbar/extension/stylesheet.css`

> **Verify shell version first.** On the target machine run `gnome-shell --version`; set `shell-version` in `metadata.json` to include that major (e.g. `46`). The code below uses GNOME 45+ ESM imports.

- [ ] **Step 1: metadata.json**

`gnome-topbar/extension/metadata.json`:
```json
{
  "uuid": "gnome-topbar@localhost",
  "name": "gnome-topbar",
  "description": "GitHub PRs, Basic Memory todos/search, and currently-working-on in the top bar.",
  "shell-version": ["45", "46", "47"],
  "url": ""
}
```

- [ ] **Step 2: daemonClient.js (libsoup 3, reads client.json)**

`gnome-topbar/extension/daemonClient.js`:
```javascript
import Soup from 'gi://Soup';
import GLib from 'gi://GLib';
import Gio from 'gi://Gio';

export class DaemonClient {
    constructor() {
        this._session = new Soup.Session();
        this._port = 0;
        this._token = '';
        this._loadConfig();
    }

    _loadConfig() {
        const path = GLib.build_filenamev([GLib.get_home_dir(), '.config', 'gnome-topbar', 'client.json']);
        try {
            const [ok, bytes] = GLib.file_get_contents(path);
            if (!ok) return;
            const cfg = JSON.parse(new TextDecoder().decode(bytes));
            this._port = cfg.port;
            this._token = cfg.token;
        } catch (e) {
            logError(e, 'gnome-topbar: cannot read client.json');
        }
    }

    get configured() {
        return this._port > 0 && this._token.length > 0;
    }

    _url(path) {
        return `http://127.0.0.1:${this._port}${path}`;
    }

    // Returns a Promise<object> parsed from JSON, or rejects.
    _request(method, path, body) {
        return new Promise((resolve, reject) => {
            const msg = Soup.Message.new(method, this._url(path));
            msg.get_request_headers().append('Authorization', `Bearer ${this._token}`);
            if (body !== undefined) {
                const data = new TextEncoder().encode(JSON.stringify(body));
                msg.set_request_body_from_bytes('application/json', GLib.Bytes.new(data));
            }
            this._session.send_and_read_async(msg, GLib.PRIORITY_DEFAULT, null, (session, res) => {
                try {
                    const bytes = session.send_and_read_finish(res);
                    if (msg.get_status() >= 300) {
                        reject(new Error(`HTTP ${msg.get_status()}`));
                        return;
                    }
                    const text = new TextDecoder().decode(bytes.get_data());
                    resolve(text ? JSON.parse(text) : {});
                } catch (e) {
                    reject(e);
                }
            });
        });
    }

    getState() { return this._request('GET', '/state'); }
    search(q) { return this._request('GET', `/search?q=${encodeURIComponent(q)}`); }
    ack(ids) { return this._request('POST', '/ack', { event_ids: ids }); }
}
```

- [ ] **Step 3: stylesheet.css**

`gnome-topbar/extension/stylesheet.css`:
```css
.gtb-section-title { font-weight: bold; padding: 4px 0 2px 0; }
.gtb-overdue { color: #f66; }
.gtb-stale { color: #aaa; }
.gtb-badge { background-color: #c00; color: #fff; border-radius: 8px; padding: 0 5px; font-size: 9px; }
```

- [ ] **Step 4: extension.js — button, menu, poll, render**

`gnome-topbar/extension/extension.js`:
```javascript
import St from 'gi://St';
import GObject from 'gi://GObject';
import GLib from 'gi://GLib';
import Gio from 'gi://Gio';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import * as PopupMenu from 'resource:///org/gnome/shell/ui/popupMenu.js';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {DaemonClient} from './daemonClient.js';

const POLL_SECONDS = 45;

const Indicator = GObject.registerClass(
class Indicator extends PanelMenu.Button {
    _init() {
        super._init(0.0, 'gnome-topbar');
        this._client = new DaemonClient();

        const box = new St.BoxLayout({style_class: 'panel-status-menu-box'});
        this._icon = new St.Icon({icon_name: 'view-list-symbolic', style_class: 'system-status-icon'});
        this._badge = new St.Label({style_class: 'gtb-badge', text: '', y_align: 2});
        box.add_child(this._icon);
        box.add_child(this._badge);
        this.add_child(box);

        this._sections = {};
        this._buildMenu();
        this._poll();
        this._timer = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, POLL_SECONDS, () => {
            this._poll();
            return GLib.SOURCE_CONTINUE;
        });
        this.menu.connect('open-state-changed', (_m, open) => { if (open) this._poll(); });
    }

    _buildMenu() {
        this._nowItem = new PopupMenu.PopupMenuItem('', {reactive: false});
        this.menu.addMenuItem(this._nowItem);
        this.menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());
        this._prSection = new PopupMenu.PopupMenuSection();
        this.menu.addMenuItem(this._prSection);
        this.menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());
        this._todoSection = new PopupMenu.PopupMenuSection();
        this.menu.addMenuItem(this._todoSection);
    }

    async _poll() {
        if (!this._client.configured) {
            this._nowItem.label.text = 'gnome-topbar: client.json missing — is the daemon running?';
            return;
        }
        try {
            const state = await this._client.getState();
            this._render(state);
        } catch (e) {
            this._nowItem.label.text = 'daemon offline — systemctl --user start gnome-topbar-daemon';
            this._badge.text = '';
        }
    }

    _render(state) {
        // currently working on
        const nw = state.now_working || {};
        this._nowItem.label.text = nw.body ? `🛠 ${nw.body}` : '🛠 (currently-working-on not set)';

        // PRs
        this._prSection.removeAll();
        const rr = (state.prs && state.prs.review_requested) || [];
        const mine = (state.prs && state.prs.authored) || [];
        this._addTitle(this._prSection, `🔵 Review requested (${rr.length})`);
        rr.forEach(pr => this._addPR(this._prSection, pr));
        this._addTitle(this._prSection, `🟣 My open PRs (${mine.length})`);
        mine.forEach(pr => this._addPR(this._prSection, pr));

        // todos
        this._todoSection.removeAll();
        const due = (state.todos && state.todos.due) || [];
        const active = (state.todos && state.todos.active) || [];
        this._addTitle(this._todoSection, `✅ Due / overdue (${due.length})`);
        due.forEach(td => {
            const it = new PopupMenu.PopupMenuItem(`⚠ ${td.text}`, {reactive: false});
            if (td.overdue) it.label.add_style_class_name('gtb-overdue');
            this._todoSection.addMenuItem(it);
        });
        this._addTitle(this._todoSection, `Active (${active.length})`);
        active.forEach(td => this._todoSection.addMenuItem(
            new PopupMenu.PopupMenuItem(td.text, {reactive: false})));

        // badge
        const count = rr.length + due.length;
        this._badge.text = count > 0 ? String(count) : '';

        // dim sources with errors
        const sources = state.sources || {};
        if (sources.github && !sources.github.ok) this._addTitle(this._prSection, '⚠ GitHub error');
        if (sources['basic-memory'] && !sources['basic-memory'].ok) this._addTitle(this._todoSection, '⚠ Basic Memory error');
    }

    _addTitle(section, text) {
        const item = new PopupMenu.PopupMenuItem(text, {reactive: false});
        item.label.add_style_class_name('gtb-section-title');
        section.addMenuItem(item);
    }

    _addPR(section, pr) {
        const item = new PopupMenu.PopupMenuItem(`${pr.repo} #${pr.number}  ${pr.title}`);
        item.connect('activate', () => {
            Gio.AppInfo.launch_default_for_uri(pr.url, null);
        });
        section.addMenuItem(item);
    }

    destroy() {
        if (this._timer) {
            GLib.source_remove(this._timer);
            this._timer = null;
        }
        super.destroy();
    }
});

export default class GnomeTopbarExtension extends Extension {
    enable() {
        this._indicator = new Indicator();
        Main.panel.addToStatusArea('gnome-topbar', this._indicator);
    }
    disable() {
        this._indicator?.destroy();
        this._indicator = null;
    }
}
```

- [ ] **Step 5: Install + load in a nested shell and observe**

Run:
```bash
cd gnome-topbar/packaging && make install-extension && cd -
dbus-run-session -- gnome-shell --nested --wayland &
# In the nested shell: enable the extension
gnome-extensions enable gnome-topbar@localhost
```
Expected: a list icon appears in the nested shell's top bar; clicking it shows the "currently working on" line, PR sections (your authored PR visible), and todo sections. If nothing appears, check `journalctl --user -f | grep -i gnome-topbar` and Looking Glass (`Alt+F2`, `lg`) for JS errors. Fix any Soup3 API drift (method names: `get_request_headers`, `set_request_body_from_bytes`, `send_and_read_async/finish`) before continuing.

- [ ] **Step 6: Commit**

```bash
git add gnome-topbar/extension
git commit -m "feat(gnome-topbar): extension skeleton + /state rendering"
```

---

### Task 14: extension — epic/story search box

**Files:**
- Modify: `gnome-topbar/extension/extension.js`

Add a search `St.Entry` below the todo section; on debounced input call `/search` and render results; click copies the permalink to the clipboard.

- [ ] **Step 1: Add the search UI to `_buildMenu`**

Insert at the end of `_buildMenu()`:
```javascript
        this.menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());
        this._searchEntry = new St.Entry({hint_text: 'search epics / stories…', can_focus: true, x_expand: true});
        const entryItem = new PopupMenu.PopupBaseMenuItem({reactive: false});
        entryItem.add_child(this._searchEntry);
        this.menu.addMenuItem(entryItem);
        this._searchResults = new PopupMenu.PopupMenuSection();
        this.menu.addMenuItem(this._searchResults);

        this._searchEntry.clutter_text.connect('text-changed', () => {
            if (this._searchTimer) GLib.source_remove(this._searchTimer);
            this._searchTimer = GLib.timeout_add(GLib.PRIORITY_DEFAULT, 300, () => {
                this._searchTimer = null;
                this._runSearch(this._searchEntry.get_text());
                return GLib.SOURCE_REMOVE;
            });
        });
```

- [ ] **Step 2: Add `_runSearch`**

Add method to the Indicator class:
```javascript
    async _runSearch(q) {
        this._searchResults.removeAll();
        if (!q || q.length < 2) return;
        try {
            const res = await this._client.search(q);
            (res.results || []).forEach(r => {
                const item = new PopupMenu.PopupMenuItem(`${r.type === 'epic' ? '📦' : '📄'} ${r.title}`);
                item.connect('activate', () => {
                    St.Clipboard.get_default().set_text(
                        St.ClipboardType.CLIPBOARD, `memory://${r.permalink}`);
                });
                this._searchResults.addMenuItem(item);
            });
        } catch (e) {
            logError(e, 'gnome-topbar: search failed');
        }
    }
```

- [ ] **Step 3: Clean up the search timer in `destroy`**

In `destroy()`, before `super.destroy()`:
```javascript
        if (this._searchTimer) {
            GLib.source_remove(this._searchTimer);
            this._searchTimer = null;
        }
```

- [ ] **Step 4: Reinstall + verify in nested shell**

Run: `cd gnome-topbar/packaging && make install-extension && cd -` then reload the nested shell (restart it) and type in the search box.
Expected: typing ≥2 chars shows epic/story results; clicking one copies `memory://<permalink>` (paste elsewhere to confirm). No JS errors in `lg`.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/extension
git commit -m "feat(gnome-topbar): epic/story search box"
```

---

### Task 15: extension — notifications + ack

**Files:**
- Modify: `gnome-topbar/extension/extension.js`

Raise a native notification per `unacked_event` not already raised this session, then `POST /ack`. Track raised ids in-memory to avoid re-raising before the ack lands.

- [ ] **Step 1: Init the raised-set in `_init`**

After `this._client = new DaemonClient();` add:
```javascript
        this._raised = new Set();
```

- [ ] **Step 2: Handle events in `_render`**

At the end of `_render(state)` add:
```javascript
        this._handleEvents(state.unacked_events || []);
```

- [ ] **Step 3: Add `_handleEvents`**

Add this method (MVP uses `Main.notify`, which is non-clickable but satisfies the acceptance criteria):
```javascript
    _handleEvents(events) {
        const fresh = events.filter(e => !this._raised.has(e.id));
        if (fresh.length === 0) return;
        for (const e of fresh) {
            this._raised.add(e.id);
            const title = e.kind === 'review_request' ? `Review requested: ${e.title}` : 'Todo due';
            Main.notify(title, e.body || '');
        }
        this._client.ack(fresh.map(e => e.id)).catch(err => logError(err, 'gnome-topbar: ack failed'));
    }
```

> **Optional polish (not required):** to make a review notification clickable (open the PR), construct a `MessageTray.Source` + `MessageTray.Notification` with a default action calling `Gio.AppInfo.launch_default_for_uri(e.url, null)` instead of `Main.notify`. Skip for MVP.

- [ ] **Step 4: Verify notification + dedup**

Reinstall + restart nested shell. To force an event without waiting on real data, temporarily delete `~/.local/state/gnome-topbar/seen.json` while the daemon runs (it will re-surface your current review-requested PRs / due todos as unacked on next poll).
Expected: one notification per new event appears once; it does NOT re-fire on subsequent polls (ack persisted). Restarting the daemon does not re-notify.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/extension
git commit -m "feat(gnome-topbar): native notifications + ack"
```

---

### Task 16: currently-working-on note + assistant wiring

**Files:**
- Modify: `gnome-topbar/README.md` (already has the snippet; refine if needed)
- (Operator action, not committed) create the BM note + add the CLAUDE.md instruction

- [ ] **Step 1: Create the note (operator machine, real namespace)**

Using your BM tooling, create `<username>/notes/currently-working-on/main` with:
```markdown
---
title: Currently working on
type: note
updated: 2026-06-02T09:00:00Z
---

Bootstrapping gnome-topbar; next step is end-to-end verification.
```

- [ ] **Step 2: Add the assistant instruction**

Add to `~/.claude/CLAUDE.md` (outside this repo): *"When you begin or switch tasks, update the Basic Memory note `<username>/notes/currently-working-on/main` — set frontmatter `updated:` to the current RFC3339 timestamp and replace the body with a 1–3 sentence summary (ticket/branch + next step)."*

- [ ] **Step 3: Verify it renders with staleness**

With the daemon running and the extension loaded, open the menu.
Expected: the "🛠" line shows your note body. (Staleness display is computed from `now_working.updated`; if you want the "⟳ age" text in the panel, it is derived client-side from `now_working.updated` — optional polish.)

- [ ] **Step 4: Commit any README refinements**

```bash
git add gnome-topbar/README.md
git commit -m "docs(gnome-topbar): currently-working-on wiring notes"
```

---

### Task 17: anti-tangent stats — daemon source

**Files:**
- Create: `gnome-topbar/daemon/internal/atstats/atstats.go`
- Test: `gnome-topbar/daemon/internal/atstats/atstats_test.go`
- Modify: `internal/config/config.go` (add `StatsDir`), `internal/state/state.go` (add `AntiTangent` to `Snapshot`), `cmd/gnome-topbar-daemon/main.go` (wire a refresh)

Reads the anti-tangent v0.10.0 stats files (`rollup.json` + `summary.md`) from a configured
dir, if present. Pure local file reads.

> **Cross-component contract:** `rollup.json`'s keys must match what the anti-tangent stats
> writer emits (`docs/superpowers/specs/2026-06-02-anti-tangent-stats-design.md` §3.3). This
> reader pins the **snake_case** keys below; when implementing the v0.10.0 stats feature, give
> its `Rollup` struct matching json tags (Go marshals PascalCase by default, which would NOT
> match). Missing/extra keys degrade gracefully (zero values), but the headline numbers depend
> on these names.

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/atstats/atstats_test.go`:
```go
package atstats

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAbsentDirIsNotPresent(t *testing.T) {
	if got := Read(filepath.Join(t.TempDir(), "nope")); got.Present {
		t.Fatalf("expected not present, got %+v", got)
	}
	if got := Read(""); got.Present {
		t.Fatal("empty dir must be not present")
	}
}

func TestReadParsesRollupAndSummary(t *testing.T) {
	dir := t.TempDir()
	rollup := `{"total_calls":10,"verdict_counts":{"pass":7,"warn":2,"fail":1},
	  "category_histogram":{"ambiguous_spec":5,"scope_drift":2},
	  "review_ms_p95":1800,"generated_at":"2026-06-02T08:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "rollup.json"), []byte(rollup), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte("All healthy this week."), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Read(dir)
	if !s.Present {
		t.Fatal("expected present")
	}
	if s.TotalCalls != 10 || s.ReviewMSP95 != 1800 {
		t.Fatalf("bad numbers: %+v", s)
	}
	if s.PassPct != 70 || s.WarnPct != 20 || s.FailPct != 10 {
		t.Fatalf("bad pcts: %+v", s)
	}
	if s.TopCategory != "ambiguous_spec" {
		t.Fatalf("top category=%q", s.TopCategory)
	}
	if s.Summary != "All healthy this week." {
		t.Fatalf("summary=%q", s.Summary)
	}
}

func TestReadRollupPresentSummaryMissing(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "rollup.json"), []byte(`{"total_calls":1,"verdict_counts":{"pass":1},"generated_at":"2026-06-02T08:00:00Z"}`), 0o600)
	s := Read(dir)
	if !s.Present || s.Summary != "" {
		t.Fatalf("expected present with empty summary: %+v", s)
	}
}

func TestReadCodeSceneOptional(t *testing.T) {
	// Absent codescene key -> nil, no error.
	dir1 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir1, "rollup.json"), []byte(`{"total_calls":1,"verdict_counts":{"pass":1},"generated_at":"2026-06-02T08:00:00Z"}`), 0o600)
	if cs := Read(dir1).CodeScene; cs != nil {
		t.Fatalf("expected nil CodeScene when key absent, got %+v", cs)
	}
	// Present codescene key -> parsed.
	dir2 := t.TempDir()
	rollup := `{"total_calls":5,"verdict_counts":{"pass":5},"generated_at":"2026-06-02T08:00:00Z",
	  "codescene":{"runs":12,"latest_score":8.4,"latest_delta":-0.3,"latest_trend":"regression",
	  "score_p50":8.6,"regressions":3,"improvements":7,"neutral":2,
	  "category_histogram":{"complex-method":5,"bumpy-road":2}}}`
	_ = os.WriteFile(filepath.Join(dir2, "rollup.json"), []byte(rollup), 0o600)
	cs := Read(dir2).CodeScene
	if cs == nil {
		t.Fatal("expected CodeScene parsed")
	}
	if cs.Runs != 12 || cs.LatestScore != 8.4 || cs.LatestDelta != -0.3 || cs.LatestTrend != "regression" {
		t.Fatalf("bad CodeScene: %+v", cs)
	}
	if cs.Regressions != 3 || cs.Improvements != 7 {
		t.Fatalf("bad reg/imp: %+v", cs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gnome-topbar/daemon && go test ./internal/atstats/... && cd -`
Expected: FAIL — `undefined: Read`.

- [ ] **Step 3: Implement the reader**

`gnome-topbar/daemon/internal/atstats/atstats.go`:
```go
// Package atstats reads the anti-tangent v0.10.0 stats subsystem's output
// (rollup.json + summary.md) if present. Pure local file reads; absence is
// reported as Present=false with no error.
package atstats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Stats struct {
	Present     bool            `json:"present"`
	GeneratedAt time.Time       `json:"generated_at"`
	TotalCalls  int             `json:"total_calls"`
	PassPct     float64         `json:"pass_pct"`
	WarnPct     float64         `json:"warn_pct"`
	FailPct     float64         `json:"fail_pct"`
	TopCategory string          `json:"top_category"`
	ReviewMSP95 int64           `json:"review_ms_p95"`
	Summary     string          `json:"summary"`
	CodeScene   *CodeSceneStats `json:"codescene,omitempty"` // nil when absent in rollup.json
}

// CodeSceneStats mirrors the OPTIONAL top-level "codescene" object inside
// anti-tangent's rollup.json (anti-tangent aggregates CodeScene runs into it).
// Contract: 2026-06-02-anti-tangent-stats-design.md (codescene block).
type CodeSceneStats struct {
	Runs              int            `json:"runs"`
	LatestScore       float64        `json:"latest_score"`
	LatestDelta       float64        `json:"latest_delta"`
	LatestTrend       string         `json:"latest_trend"` // regression | improvement | neutral
	ScoreP50          float64        `json:"score_p50"`
	Regressions       int            `json:"regressions"`
	Improvements      int            `json:"improvements"`
	Neutral           int            `json:"neutral"`
	CategoryHistogram map[string]int `json:"category_histogram"`
	WindowStart       time.Time      `json:"window_start"`
	WindowEnd         time.Time      `json:"window_end"`
}

// rollup is the subset of anti-tangent's rollup.json this panel reads.
// Contract: keys match 2026-06-02-anti-tangent-stats-design.md §3.3 (snake_case).
type rollup struct {
	TotalCalls        int             `json:"total_calls"`
	VerdictCounts     map[string]int  `json:"verdict_counts"`
	CategoryHistogram map[string]int  `json:"category_histogram"`
	ReviewMSP95       int64           `json:"review_ms_p95"`
	GeneratedAt       time.Time       `json:"generated_at"`
	CodeScene         *CodeSceneStats `json:"codescene"` // optional; nil when key omitted
}

// Read returns Present=false (no error) when dir is empty or rollup.json is
// absent/unreadable/unparseable — the panel then omits the section.
func Read(dir string) Stats {
	if dir == "" {
		return Stats{}
	}
	b, err := os.ReadFile(filepath.Join(dir, "rollup.json"))
	if err != nil {
		return Stats{}
	}
	var r rollup
	if err := json.Unmarshal(b, &r); err != nil {
		return Stats{}
	}
	s := Stats{
		Present:     true,
		GeneratedAt: r.GeneratedAt,
		TotalCalls:  r.TotalCalls,
		ReviewMSP95: r.ReviewMSP95,
		TopCategory: topKey(r.CategoryHistogram),
		CodeScene:   r.CodeScene, // nil when the "codescene" key is absent
	}
	if r.TotalCalls > 0 {
		s.PassPct = pct(r.VerdictCounts["pass"], r.TotalCalls)
		s.WarnPct = pct(r.VerdictCounts["warn"], r.TotalCalls)
		s.FailPct = pct(r.VerdictCounts["fail"], r.TotalCalls)
	}
	if sb, err := os.ReadFile(filepath.Join(dir, "summary.md")); err == nil {
		s.Summary = truncate(string(sb), 600)
	}
	return s
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

func topKey(m map[string]int) string {
	best, bestN := "", -1
	for k, v := range m {
		if v > bestN {
			best, bestN = k, v
		}
	}
	return best
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max]
	}
	return s
}
```

> **Note:** `truncate` may split a multibyte UTF-8 rune at `max` bytes. For a 600-byte
> headline snippet this is acceptable (worst case one trailing replacement glyph); if you
> prefer rune-safe truncation, convert to `[]rune` first. Keeping it byte-based here avoids a
> dependency and matches the "headline only" intent.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gnome-topbar/daemon && go test ./internal/atstats/... && cd -`
Expected: PASS (all three).

- [ ] **Step 5: Add `StatsDir` to config (modify `internal/config/config.go`)**

Add a field to the `Config` struct (from Task 1):
```go
	StatsDir string `toml:"stats_dir"`
```
And at the end of `Load`, before writing client.json, default it when empty:
```go
	if c.StatsDir == "" {
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".local", "state")
		}
		c.StatsDir = filepath.Join(base, "anti-tangent-mcp")
	}
```
(`path/filepath` is already imported in config.go; add `os` if not present — it is, from the token bootstrap.)

- [ ] **Step 6: Add `AntiTangent` to `Snapshot` (modify `internal/state/state.go`)**

Add an import and a field to `Snapshot` (from Task 9):
```go
	// add to imports:
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"

	// add to the Snapshot struct, after Sources:
	AntiTangent atstats.Stats `json:"anti_tangent"`
```

- [ ] **Step 7: Wire a refresh in main (modify `cmd/gnome-topbar-daemon/main.go`)**

Add an import for `atstats`, then add a refresh method on `Poller` and start it on the BM cadence (the files change rarely):
```go
	// import:
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"

	// new method:
func (p *Poller) refreshAntiTangent(ctx context.Context) {
	s := atstats.Read(p.cfg.StatsDir)
	p.mu.Lock()
	p.snap.AntiTangent = s
	p.snap.GeneratedAt = time.Now()
	p.mu.Unlock()
}
```
In `main`, after the initial `p.refreshBM(ctx)` and its `go p.loop(...)`:
```go
	p.refreshAntiTangent(ctx)
	go p.loop(ctx, time.Duration(cfg.BMIntervalSec)*time.Second, p.refreshAntiTangent)
```

- [ ] **Step 8: Build + full test**

Run: `cd gnome-topbar/daemon && go build ./... && go vet ./... && go test -race ./... && cd -`
Expected: clean build, all PASS.

- [ ] **Step 9: Commit**

```bash
git add gnome-topbar/daemon
git commit -m "feat(gnome-topbar): read anti-tangent stats (rollup.json + summary.md) if present"
```

---

### Task 18: anti-tangent stats — panel section

**Files:**
- Modify: `gnome-topbar/extension/extension.js`

Render an "anti-tangent" section when `state.anti_tangent.present`; omit it entirely when not.

- [ ] **Step 1: Add the section to `_buildMenu`**

Insert before the search separator added in Task 14 (so stats sits above search):
```javascript
        this.menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());
        this._atSection = new PopupMenu.PopupMenuSection();
        this.menu.addMenuItem(this._atSection);
```

- [ ] **Step 2: Render it in `_render`**

Add at the end of `_render(state)`:
```javascript
        this._renderAntiTangent(state.anti_tangent);
```

- [ ] **Step 3: Add `_renderAntiTangent`**

Add method to the Indicator class:
```javascript
    _renderAntiTangent(at) {
        this._atSection.removeAll();
        if (!at || !at.present) return; // "if they exist" — omit the whole section
        const when = at.generated_at ? new Date(at.generated_at).toLocaleString() : '?';
        this._addTitle(this._atSection, `🛡 anti-tangent · as of ${when}`);
        const line = `${at.total_calls} calls · ${Math.round(at.pass_pct)}% pass / ` +
            `${Math.round(at.warn_pct)}% warn / ${Math.round(at.fail_pct)}% fail · ` +
            `top: ${at.top_category || '—'} · p95 ${at.review_ms_p95}ms`;
        this._atSection.addMenuItem(new PopupMenu.PopupMenuItem(line, {reactive: false}));
        if (at.summary) {
            const item = new PopupMenu.PopupMenuItem(at.summary, {reactive: false});
            item.label.clutter_text.set_line_wrap(true);
            this._atSection.addMenuItem(item);
        }
        // CodeScene rides inside the same rollup.json under an optional "codescene"
        // key; render a distinct sub-block only when present.
        const cs = at.codescene;
        if (cs) {
            const arrow = cs.latest_trend === 'regression' ? '▼'
                : cs.latest_trend === 'improvement' ? '▲' : '·';
            this._addTitle(this._atSection, `📊 CodeScene · ${cs.runs} runs`);
            let top = '—', topN = -1;
            const cats = cs.category_histogram || {};
            for (const k in cats) { if (cats[k] > topN) { top = k; topN = cats[k]; } }
            const csLine = `score ${cs.latest_score} (${arrow}${Math.abs(cs.latest_delta)} ${cs.latest_trend}) · ` +
                `p50 ${cs.score_p50} · ${cs.regressions} reg / ${cs.improvements} imp · top: ${top}`;
            const csItem = new PopupMenu.PopupMenuItem(csLine, {reactive: false});
            if (cs.latest_trend === 'regression') csItem.label.add_style_class_name('gtb-overdue');
            this._atSection.addMenuItem(csItem);
        }
    }
```

- [ ] **Step 4: Reinstall + verify in nested shell**

Create a fake stats dir to exercise the path without waiting on a real anti-tangent run:
```bash
mkdir -p ~/.local/state/anti-tangent-mcp
cat > ~/.local/state/anti-tangent-mcp/rollup.json <<'EOF'
{"total_calls":12,"verdict_counts":{"pass":9,"warn":2,"fail":1},"category_histogram":{"ambiguous_spec":4},"review_ms_p95":1500,"generated_at":"2026-06-02T08:00:00Z","codescene":{"runs":12,"latest_score":8.4,"latest_delta":-0.3,"latest_trend":"regression","score_p50":8.6,"regressions":3,"improvements":7,"neutral":2,"category_histogram":{"complex-method":5,"bumpy-road":2}}}
EOF
echo "Reviews look healthy; ambiguous ACs are the most common finding." > ~/.local/state/anti-tangent-mcp/summary.md
cd gnome-topbar/packaging && make install-extension && cd -
```
Restart the nested shell; open the menu.
Expected: a "🛡 anti-tangent · as of …" section shows the numbers line + summary, **plus** a "📊 CodeScene · 12 runs" sub-block (`score 8.4 (▼0.3 regression) … 3 reg / 7 imp · top: complex-method`, the line styled as a regression). Remove the `"codescene":{…}` key (leave the rest) → only the CodeScene sub-block disappears. Then `rm ~/.local/state/anti-tangent-mcp/rollup.json`, wait one poll (or reopen): the whole section disappears with no error.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/extension
git commit -m "feat(gnome-topbar): anti-tangent stats panel section"
```

---

### Task 19: end-to-end verification + acceptance

**Files:** none (verification only)

- [ ] **Step 1: Full daemon test suite**

Run: `cd gnome-topbar/daemon && go test -race ./... && cd -`
Expected: all PASS.

- [ ] **Step 2: Acceptance checklist (operator machine)**

Confirm each spec acceptance item:
- [ ] Top-bar badge shows `review_requested + due_todo` count (non-zero when applicable).
- [ ] Dropdown renders currently-working-on, review-requested PRs, my open PRs, due/overdue + active todos, and a working epic/story search.
- [ ] Clicking a PR opens it in the browser.
- [ ] A new review request raises one notification; a due todo raises one; neither re-fires after daemon or panel restart.
- [ ] With a `rollup.json` (+ optional `summary.md`) in `stats_dir`, the "anti-tangent" section shows numbers + summary with an "as of" stamp; removing `rollup.json` makes the section vanish (no error).
- [ ] When `rollup.json` contains a `codescene` object, a "📊 CodeScene" sub-block shows score/delta/trend/reg-imp/top; removing only that key drops just the sub-block (anti-tangent numbers remain).
- [ ] Killing the daemon (`systemctl --user stop gnome-topbar-daemon`) degrades the panel to the "daemon offline" hint with no shell instability.
- [ ] A single source failing (e.g. break `bm_username`) dims/marks only that section; GitHub still renders.
- [ ] No real personal data in the committed tool tree: `git grep -nE "pgilmore|YN-[0-9]|patiently/(powow|yobify)" -- gnome-topbar/` returns nothing. (Scope is `gnome-topbar/` only — the spec/plan under `docs/` are authored anonymized and reviewed separately; do not grep them with this pattern or it self-matches its own example strings. `BM_BEARER_TOKEN` is a public env-var *name* and is expected to appear as a placeholder in `README.md`.)
- [ ] `~/.config/gnome-topbar/` (real `bm_username`, env file, token, client.json) is outside the repo and untracked: `git status --porcelain | grep -i gnome-topbar` shows only intended `gnome-topbar/` tree files, never anything under `~/.config`.

- [ ] **Step 3: Final commit (if any verification fixes were needed)**

```bash
git add -A
git commit -m "test(gnome-topbar): end-to-end verification fixes"
```

---

## Notes on remaining risk

- **Soup 3 / GNOME ESM API drift** is the most likely source of friction; Tasks 13–15 each end with a nested-shell observation step so drift surfaces immediately. The method names used (`get_request_headers`, `set_request_body_from_bytes`, `send_and_read_async`/`send_and_read_finish`) are the libsoup-3 forms shipped with GNOME 45+.
- **BM search JSON shape** is verified live in Task 6 Step 5; adjust `parseSearch` tags if the real server differs.
- **Fine-grained PAT scope**: authored PRs are confirmed visible; if review-requested PRs from some org never appear, widen the token's repo/org scope (GitHub settings) — a config/permissions fix, not code.
- **anti-tangent stats** (Tasks 17–18) read `rollup.json`/`summary.md` from `stats_dir` if present. The `rollup.json` key names are a **cross-component contract** with the anti-tangent v0.10.0 stats writer (snake_case; see Task 17's contract note) — keep them in sync. **CodeScene** data rides inside the same `rollup.json` under an optional top-level `codescene` object (anti-tangent aggregates it); the consumer reads exactly one file and treats `codescene` absence as "no data this window," never an error. It never reads `codescene-events.jsonl` (anti-tangent's raw source).
- **Deferred slices** (Claude usage only) are intentionally out; `state.Snapshot.Sources` + the section-per-source render pattern keep them additive.
