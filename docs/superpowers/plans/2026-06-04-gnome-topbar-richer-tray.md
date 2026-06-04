# gnome-topbar richer tray (BM search · rendered notes · todo create/done) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Basic Memory search, in-browser rendered-markdown note viewing (with mermaid and clickable inter-note links), todo creation, and mark-todo-done to the gnome-topbar tray — using only capabilities the claude-sandbox already provides.

**Architecture:** The tray stays a pure-Go static binary on the shared DBus session bus. Text-input and rich-rendering features (search, new-todo, note view) are served as small HTML pages by the daemon's existing loopback HTTP server and opened in the **in-container Chrome** (`/usr/bin/google-chrome-stable`, already the default browser) over the live X11 display (`DISPLAY=:10`). Because the page lives on the container's own `127.0.0.1` and `NO_PROXY` covers loopback, Chrome reaches it directly — no host networking, no GTK, no cgo. The first page open carries the API token in `?t=`; the handler plants a session cookie so in-page navigation (search results, wikilinks between BM notes) needs no token. Mark-todo-done is a clickable tray row that calls Basic Memory's `edit_note`. Mermaid renders client-side from a vendored `mermaid.min.js` served as a cached static asset.

**Tech Stack:** Go 1.25 (pure, `CGO_ENABLED` untouched), `fyne.io/systray` + `godbus` (existing), `github.com/yuin/goldmark` (new, markdown→HTML), vendored `mermaid@11` (self-contained global build, fetched via npm), Basic Memory MCP over the existing `mcphttp` client.

---

## Why this shape (grounded in the actual sandbox)

Probed live in the running sandbox on 2026-06-04:

| Fact | Value | Consequence |
|---|---|---|
| Display | `DISPLAY=:10`, `/tmp/.X11-unix/X10` mounted `:ro`; no Wayland, no `XDG_RUNTIME_DIR` | GUI must use the X11 backend; Chrome already does |
| Browser | `/usr/bin/google-chrome-stable`, default via `chrome-sandbox.desktop`, `xdg-open` shim routes to it | We render our own HTML in-container |
| Network | bridge (`172.28.0.3`) + egress proxy `172.28.0.2:3128`; `NO_PROXY` covers `127.0.0.1` | Chrome-to-daemon loopback is a direct same-namespace hop |
| Proxy allowlist | `.npmjs.org` + `.github.com` allowed; **jsdelivr/unpkg/cdnjs blocked** (verified: `npm view mermaid` returns `11.15.0`) | mermaid is fetched via **npm**, not a CDN, then committed (`go:embed`). The release binary is built in GitHub Actions (open internet) and just embeds the committed file |
| Daemon port | loopback `:47615`, **not** published in compose `ports:` | Container-internal only: Chrome reaches it, host can't (good) |
| Toolkit/build | GTK3 runtime libs yes; `zenity`/`pkg-config`/`-dev` no; `gcc`+CGO yes but binary is static pure-Go | Confirms: avoid GTK/cgo, keep the static binary |
| mermaid build shape | `dist/mermaid.min.js` ends with `globalThis["mermaid"]=…` (self-contained, sets a global) | A plain `<script src>` works; no ESM module juggling, no chunks |
| Deploy | Dockerfile.sandbox installs a pinned, checksum-verified static binary via `GNOME_TOPBAR_VERSION` | Shipping = tag `v0.2.0` + bump that ARG (separate repo) |

Net: every feature below needs **zero change to the container image**. The one non-code prerequisite is the BM token's write scope (below).

## Prerequisites (verify before Task 1)

- [ ] **BM token has write scope.** The daemon currently only reads (`read_note`, `search_notes`). Create + mark-done call `edit_note`. Confirm `BM_BEARER_TOKEN` permits writes against `$BM_URL` (one-shot manual check; replace `<user>`):

  ```bash
  curl -s -X POST "$BM_URL" \
    -H "Authorization: Bearer $BM_BEARER_TOKEN" \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_note","arguments":{"identifier":"<user>/todo/main","project":"main"}}}' | head -c 400
  ```
  Expected: the todo note markdown comes back (proves the note exists at `<user>/todo/main` with `## Active` / `## Done` sections — both write paths assume that shape). If it 401s, the token is read-scoped or wrong; stop and fix credentials first.
- [ ] **Working directory:** all commands run from `gnome-topbar/daemon/` unless stated. Branch: `feat/gnome-topbar-richer-tray` (gnome-topbar uses `feat/gnome-topbar*` branches and `gnome-topbar-vX.Y.Z` tags; target tag is `gnome-topbar-v0.2.0`, a minor feature bump from `v0.1.2`).

## File structure

| File | Responsibility | Change |
|---|---|---|
| `internal/bm/todo.go` | todo parsing | **Modify** — preserve the raw source line on each `TodoItem` |
| `internal/bm/write.go` | todo writes | **Create** — `AppendTodo`, `MarkTodoDone` (wrap `edit_note`) |
| `internal/bm/write_test.go` | write unit tests | **Create** |
| `cmd/gnome-topbar-daemon/main.go` | wiring + Poller | **Modify** — `AppendTodo`/`MarkTodoDone`/`ReadNote` provider methods, action closures, local opener |
| `internal/server/markdown.go` | note MD→HTML, mermaid, inter-note link rewriting | **Create** — pure render |
| `internal/server/assets/mermaid.min.js` | vendored mermaid 11 (npm) | **Create** (committed binary asset) |
| `internal/server/ui.go` | loopback HTML UI + cookie auth + asset route | **Create** — `/ui/search`, `/ui/search/results`, `/ui/note`, `/ui/new-todo`, `/assets/mermaid.min.js` |
| `internal/server/server.go` | mux + Provider | **Modify** — widen `Provider`, register UI |
| `internal/server/ui_test.go`, `markdown_test.go` | UI + render tests | **Create** |
| `internal/tray/openurl.go` | URL openers | **Modify** — add `OpenLocal` (in-container Chrome) |
| `internal/tray/tray.go` | menu assembly | **Modify** — clickable done rows, Search/New-todo items, Refresh/Quit to top |
| `internal/tray/tray_test.go` | tray tests | **Modify** — new `Actions` param |

---

## Task 1: Preserve the raw todo line

A mark-done edit does `find_replace` on the **exact** source bullet (`tick-todo` does the same), so `TodoItem` must keep the original line, not just the date-stripped display text.

**Files:**
- Modify: `internal/bm/todo.go`
- Test: `internal/bm/todo_test.go`

- [ ] **Step 1: Add a failing test for the raw field**

Append to `internal/bm/todo_test.go`:

```go
func TestParseTodosKeepsRawLine(t *testing.T) {
	md := "## Active\n- [ ] [2026-06-04] ship the thing\n- [ ] no date here\n## Done\n- [x] old\n"
	today := time.Date(2026, 6, 4, 9, 0, 0, 0, time.Local)
	active, _ := ParseTodos(md, today)
	if len(active) != 2 {
		t.Fatalf("want 2 active, got %d", len(active))
	}
	if active[0].Raw != "- [ ] [2026-06-04] ship the thing" {
		t.Errorf("raw[0] = %q", active[0].Raw)
	}
	if active[0].Text != "ship the thing" {
		t.Errorf("text[0] = %q (date prefix should be stripped for display)", active[0].Text)
	}
	if active[1].Raw != "- [ ] no date here" {
		t.Errorf("raw[1] = %q", active[1].Raw)
	}
}
```

- [ ] **Step 2: Run it; verify it fails to compile**

Run: `go test ./internal/bm/ -run TestParseTodosKeepsRawLine`
Expected: FAIL — `active[0].Raw undefined (type TodoItem has no field Raw)`.

- [ ] **Step 3: Add the field and populate it**

In `internal/bm/todo.go`, add `Raw` to the struct:

```go
type TodoItem struct {
	Text    string     `json:"text"`
	Raw     string     `json:"raw"` // exact source bullet line, for find_replace edits
	Due     *time.Time `json:"due,omitempty"`
	Overdue bool       `json:"overdue"`
}
```

In `ParseTodos`, set `Raw` from the original line, before the date prefix is stripped. Replace the `item := TodoItem{...}` line:

```go
		item := TodoItem{Text: strings.TrimSpace(m[1]), Raw: line}
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/bm/`
Expected: PASS (existing tests unaffected; `Raw` is additive).

- [ ] **Step 5: Commit**

```bash
git add internal/bm/todo.go internal/bm/todo_test.go
git commit -m "feat(gnome-topbar): preserve raw todo line for find_replace edits"
```

---

## Task 2: Basic Memory todo writes (`AppendTodo`, `MarkTodoDone`)

Replicates `bm-scribe:add-todo` (insert before `## Done`) and `bm-scribe:tick-todo` (find_replace the bullet) over the existing generic `Caller.CallTool`.

**Files:**
- Create: `internal/bm/write.go`
- Test: `internal/bm/write_test.go`

- [ ] **Step 1: Write the failing tests with a fake Caller**

Create `internal/bm/write_test.go`:

```go
package bm

import (
	"context"
	"testing"
	"time"
)

type fakeCaller struct {
	name string
	args map[string]any
	err  error
}

func (f *fakeCaller) CallTool(_ context.Context, name string, args map[string]any) (string, error) {
	f.name, f.args = name, args
	return "ok", f.err
}

func TestAppendTodoCall(t *testing.T) {
	fc := &fakeCaller{}
	c := New(fc, "main")
	if err := c.AppendTodo(context.Background(), "alice", "buy milk"); err != nil {
		t.Fatal(err)
	}
	if fc.name != "edit_note" {
		t.Fatalf("tool = %q", fc.name)
	}
	if fc.args["identifier"] != "alice/todo/main" {
		t.Errorf("identifier = %v", fc.args["identifier"])
	}
	if fc.args["operation"] != "insert_before_section" || fc.args["section"] != "## Done" {
		t.Errorf("op/section = %v / %v", fc.args["operation"], fc.args["section"])
	}
	if fc.args["content"] != "- [ ] buy milk\n" {
		t.Errorf("content = %q", fc.args["content"])
	}
}

func TestMarkTodoDoneCall(t *testing.T) {
	fc := &fakeCaller{}
	c := New(fc, "main")
	raw := "- [ ] [2026-06-04] ship the thing"
	today := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	if err := c.MarkTodoDone(context.Background(), "alice", raw, today); err != nil {
		t.Fatal(err)
	}
	if fc.args["operation"] != "find_replace" {
		t.Fatalf("op = %v", fc.args["operation"])
	}
	if fc.args["find_text"] != raw {
		t.Errorf("find_text = %v", fc.args["find_text"])
	}
	if fc.args["replace_text"] != "- [x] [2026-06-04] ship the thing — done 2026-06-04" {
		t.Errorf("replace_text = %v", fc.args["replace_text"])
	}
}
```

- [ ] **Step 2: Run it; verify it fails**

Run: `go test ./internal/bm/ -run 'AppendTodoCall|MarkTodoDoneCall'`
Expected: FAIL — `c.AppendTodo undefined` / `c.MarkTodoDone undefined`.

- [ ] **Step 3: Implement the writes**

Create `internal/bm/write.go`:

```go
package bm

import (
	"context"
	"strings"
	"time"
)

// AppendTodo inserts "- [ ] <text>" at the end of the "## Active" section of
// <username>/todo/main, matching bm-scribe:add-todo (insert before "## Done").
// The todo note must already exist with an "## Active" and "## Done" section.
func (c *Client) AppendTodo(ctx context.Context, username, text string) error {
	_, err := c.caller.CallTool(ctx, "edit_note", map[string]any{
		"identifier": username + "/todo/main",
		"operation":  "insert_before_section",
		"section":    "## Done",
		"content":    "- [ ] " + text + "\n",
		"project":    c.project,
	})
	return err
}

// MarkTodoDone flips a single bullet from "- [ ]" to "- [x]" and appends a
// done-date, matching bm-scribe:tick-todo. rawLine is the exact source bullet
// (TodoItem.Raw); today supplies the stamp (injected for testability).
func (c *Client) MarkTodoDone(ctx context.Context, username, rawLine string, today time.Time) error {
	replacement := strings.Replace(rawLine, "- [ ]", "- [x]", 1) + " — done " + today.Format("2006-01-02")
	_, err := c.caller.CallTool(ctx, "edit_note", map[string]any{
		"identifier":   username + "/todo/main",
		"operation":    "find_replace",
		"find_text":    rawLine,
		"replace_text": replacement,
		"project":      c.project,
	})
	return err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bm/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bm/write.go internal/bm/write_test.go
git commit -m "feat(gnome-topbar): bm AppendTodo + MarkTodoDone via edit_note"
```

---

## Task 3: Poller provider methods

Expose the writes + a note read on the `Poller`, refreshing the snapshot after a successful write so the tray reflects the change immediately.

**Files:**
- Modify: `cmd/gnome-topbar-daemon/main.go`
- Test: none new (thin wiring over a real BM dependency; the `bm` and server/tray tests cover the logic).

- [ ] **Step 1: Add the methods**

In `cmd/gnome-topbar-daemon/main.go`, after the existing `func (p *Poller) Search(...)`:

```go
// ReadNote returns the raw markdown of a Basic Memory note (used by the note view).
func (p *Poller) ReadNote(ctx context.Context, identifier string) (string, error) {
	return p.bm.ReadNote(ctx, identifier)
}

// AppendTodo adds a bullet to the rolling todo note, then refreshes BM so the
// tray reflects it on the next render.
func (p *Poller) AppendTodo(ctx context.Context, text string) error {
	if p.cfg.BMUsername == "" {
		return fmt.Errorf("bm_username not set")
	}
	if err := p.bm.AppendTodo(ctx, p.cfg.BMUsername, text); err != nil {
		return err
	}
	p.refreshBM(ctx)
	return nil
}

// MarkTodoDone ticks a specific bullet, then refreshes BM.
func (p *Poller) MarkTodoDone(ctx context.Context, rawLine string) error {
	if p.cfg.BMUsername == "" {
		return fmt.Errorf("bm_username not set")
	}
	if err := p.bm.MarkTodoDone(ctx, p.cfg.BMUsername, rawLine, time.Now()); err != nil {
		return err
	}
	p.refreshBM(ctx)
	return nil
}
```

(`fmt` and `time` are already imported in `main.go`.)

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./...`
Expected: success (the new methods are unused until Tasks 5–7 wire them; Go allows unused methods).

- [ ] **Step 3: Commit**

```bash
git add cmd/gnome-topbar-daemon/main.go
git commit -m "feat(gnome-topbar): Poller ReadNote/AppendTodo/MarkTodoDone"
```

---

## Task 4: Note markdown → HTML (mermaid + clickable inter-note links)

BM notes link to each other with **wikilinks whose target is a permalink**, e.g. `[[<PROJECT>/decisions/0001-text-only-reviewer/main]]` (verified in `examples/project-knowledge/`), occasionally `[text](memory://<permalink>)`. The renderer rewrites both to same-origin `/ui/note?id=<identifier>` links so clicking navigates to the next note. BM's `read_note` accepts a permalink **or** a title as `identifier`, so the rewrite passes the wikilink target through verbatim and lets BM resolve it.

**Files:**
- Create: `internal/server/assets/mermaid.min.js` (committed)
- Create: `internal/server/markdown.go`
- Test: `internal/server/markdown_test.go`

- [ ] **Step 1: Vendor mermaid 11 via npm (jsdelivr is blocked) and add goldmark**

```bash
mkdir -p internal/server/assets
( cd /tmp && npm pack mermaid@11 && tar xzf mermaid-*.tgz package/dist/mermaid.min.js )
cp /tmp/package/dist/mermaid.min.js internal/server/assets/mermaid.min.js
test -s internal/server/assets/mermaid.min.js && tail -c 80 internal/server/assets/mermaid.min.js; echo
go get github.com/yuin/goldmark@latest
```

Expected: a ~3.3 MB file ending in `globalThis["mermaid"]=…` (MIT-licensed; provenance = npm `mermaid@11`), and goldmark added to `go.mod`. The committed file is the build input on both local `make run` and the GitHub Actions release build — neither re-fetches.

- [ ] **Step 2: Write the failing render tests**

Create `internal/server/markdown_test.go`:

```go
package server

import "strings"
import "testing"

func TestRenderNoteHTML_StripsFrontmatterAndRendersHeading(t *testing.T) {
	md := "---\ntitle: X\npermalink: a/b/main\n---\n\n# Hello\n\nbody text\n"
	out, err := renderNoteHTML("a/b/main", md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<h1>Hello</h1>") {
		t.Error("heading not rendered")
	}
	if strings.Contains(out, "permalink: a/b/main") {
		t.Error("frontmatter leaked into rendered HTML")
	}
	if !strings.Contains(out, `<script src="/assets/mermaid.min.js">`) {
		t.Error("mermaid asset script tag missing")
	}
	if !strings.Contains(out, "mermaid.initialize") {
		t.Error("mermaid bootstrap missing")
	}
}

func TestRenderNoteHTML_MermaidFenceBecomesDiv(t *testing.T) {
	md := "# D\n\n```mermaid\ngraph TD; A-->B;\n```\n"
	out, err := renderNoteHTML("d", md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<pre class="mermaid">`) {
		t.Error("mermaid fence not rewritten to <pre class=\"mermaid\">")
	}
	if strings.Contains(out, `class="language-mermaid"`) {
		t.Error("raw goldmark mermaid code block survived")
	}
}

func TestRenderNoteHTML_WikilinkBecomesNoteLink(t *testing.T) {
	md := "See [[proj/decisions/0001-x/main]] and [[proj/modules/m/main|the module]].\n"
	out, err := renderNoteHTML("d", md)
	if err != nil {
		t.Fatal(err)
	}
	// permalink contains slashes → QueryEscape encodes them; goldmark must not mangle it.
	if !strings.Contains(out, `href="/ui/note?id=proj%2Fdecisions%2F0001-x%2Fmain"`) {
		t.Errorf("wikilink not rewritten to note link; out=%s", out)
	}
	if !strings.Contains(out, ">the module</a>") {
		t.Error("piped wikilink label not used")
	}
}

func TestRenderNoteHTML_MemoryLinkRewritten(t *testing.T) {
	md := "[other](memory://proj/stories/S-1/main)\n"
	out, err := renderNoteHTML("d", md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/ui/note?id=proj%2Fstories%2FS-1%2Fmain"`) {
		t.Errorf("memory:// link not rewritten; out=%s", out)
	}
}
```

- [ ] **Step 3: Run; verify it fails**

Run: `go test ./internal/server/ -run RenderNoteHTML`
Expected: FAIL — `undefined: renderNoteHTML`.

- [ ] **Step 4: Implement the renderer**

Create `internal/server/markdown.go`:

```go
package server

import (
	"bytes"
	_ "embed"
	"html"
	"net/url"
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed assets/mermaid.min.js
var mermaidJS []byte

var (
	frontmatterRe  = regexp.MustCompile(`(?s)\A---\n.*?\n---\n`)
	mermaidFenceRe = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)
	// [[target]] or [[target|label]] — BM's inter-note wikilink syntax.
	wikilinkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	// [text](memory://<id>) links some BM notes emit.
	memHrefRe = regexp.MustCompile(`href="memory://([^"]+)"`)
)

// noteHref builds the same-origin note-view URL for a BM identifier (permalink
// or title). Auth rides on the session cookie, so the link carries no token.
func noteHref(id string) string {
	return "/ui/note?id=" + url.QueryEscape(id)
}

// renderNoteHTML converts a BM note's markdown into a standalone HTML page.
// Inter-note links become /ui/note links so clicking navigates to the target:
//   - [[id]] / [[id|label]] wikilinks (pre-processed before goldmark)
//   - [text](memory://id) links (post-processed on the rendered HTML)
// ```mermaid fences become <pre class="mermaid"> for client-side rendering. The
// HTML-entity-escaped diagram source survives because mermaid reads textContent
// (the browser un-escapes). Pure: no I/O. (Wikilinks inside fenced code blocks
// are also rewritten — acceptable for v1.)
func renderNoteHTML(title, md string) (string, error) {
	body := frontmatterRe.ReplaceAllString(md, "")
	body = wikilinkRe.ReplaceAllStringFunc(body, func(m string) string {
		g := wikilinkRe.FindStringSubmatch(m)
		target, label := g[1], g[1]
		if g[2] != "" {
			label = g[2]
		}
		return "[" + label + "](" + noteHref(target) + ")"
	})
	var buf bytes.Buffer
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := gm.Convert([]byte(body), &buf); err != nil {
		return "", err
	}
	out := buf.String()
	out = mermaidFenceRe.ReplaceAllString(out, `<pre class="mermaid">$1</pre>`)
	out = memHrefRe.ReplaceAllStringFunc(out, func(m string) string {
		g := memHrefRe.FindStringSubmatch(m)
		return `href="` + noteHref(g[1]) + `"`
	})
	return pageShell(title, out), nil
}

const baseCSS = `body{font:16px/1.6 system-ui,Segoe UI,Roboto,sans-serif;max-width:48rem;margin:2rem auto;padding:0 1rem;color:#1a1a1a}
pre{background:#f5f5f5;padding:.75rem;border-radius:6px;overflow:auto}
pre.mermaid{background:transparent}
code{font-family:ui-monospace,Menlo,monospace}
table{border-collapse:collapse}td,th{border:1px solid #ddd;padding:.3rem .6rem}
h1,h2,h3{line-height:1.25}a{color:#0b69c7}`

// pageShell wraps body HTML in a document that loads mermaid from the cached
// /assets route (mermaid.min.js sets a global, so a plain <script src> works).
func pageShell(title, bodyHTML string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title>` +
		`<style>` + baseCSS + `</style></head><body><main class="md">` +
		bodyHTML +
		`</main><script src="/assets/mermaid.min.js"></script>` +
		`<script>mermaid.initialize({startOnLoad:true});</script></body></html>`
}
```

- [ ] **Step 5: Run; verify it passes**

Run: `go test ./internal/server/ -run RenderNoteHTML`
Expected: PASS. (If the wikilink test fails on a double-encoded `%252F`, goldmark re-escaped the destination — switch `noteHref` to build the query without `QueryEscape` and instead `strings.ReplaceAll(id, " ", "%20")`; rerun. goldmark ≥ v1.7 preserves existing `%XX`, so the plain form should pass.)

- [ ] **Step 6: Commit (include the vendored asset)**

```bash
go mod tidy
git add internal/server/markdown.go internal/server/markdown_test.go internal/server/assets/mermaid.min.js go.mod go.sum
git commit -m "feat(gnome-topbar): note render with mermaid + inter-note link rewriting"
```

---

## Task 5: Loopback HTML UI + cookie session + asset route

Adds the `/ui/*` pages and the `/assets/mermaid.min.js` route on the existing loopback server. Auth: the first hit carries `?t=<api_token>`; the handler validates it (constant time) and sets a `gtb_session` cookie scoped to `/ui`, so subsequent in-page navigation (search results, wikilinks, the new-todo form POST) authenticates via cookie with no token in the URL. The mermaid asset is public (an OSS library, no secret) and served without auth.

**Files:**
- Modify: `internal/server/server.go`
- Create: `internal/server/ui.go`
- Test: `internal/server/ui_test.go`

- [ ] **Step 1: Write failing UI tests**

Create `internal/server/ui_test.go`:

```go
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

type fakeProv struct {
	results  []bm.SearchResult
	note     string
	appended string
}

func (f *fakeProv) Snapshot() state.Snapshot { return state.Snapshot{} }
func (f *fakeProv) Ack([]string)             {}
func (f *fakeProv) Search(context.Context, string) ([]bm.SearchResult, error) {
	return f.results, nil
}
func (f *fakeProv) ReadNote(context.Context, string) (string, error) { return f.note, nil }
func (f *fakeProv) AppendTodo(_ context.Context, text string) error  { f.appended = text; return nil }

const tok = "secret-token"

func srv(p Provider) http.Handler { return New(p, tok) }

func TestUINoteRendersAndSetsCookie(t *testing.T) {
	p := &fakeProv{note: "# Title\n\nhi\n"}
	r := httptest.NewRequest("GET", "/ui/note?id=a/b/main&t="+tok, nil)
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<h1>Title</h1>") {
		t.Error("note markdown not rendered")
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "gtb_session=") {
		t.Error("session cookie not set on token hit")
	}
}

func TestUIRejectsNoCredential(t *testing.T) {
	r := httptest.NewRequest("GET", "/ui/note?id=x", nil) // no token, no cookie
	w := httptest.NewRecorder()
	srv(&fakeProv{}).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestUINavigationViaCookie(t *testing.T) {
	p := &fakeProv{note: "# Next\n"}
	r := httptest.NewRequest("GET", "/ui/note?id=next", nil) // no ?t=
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("cookie auth failed: %d", w.Code)
	}
}

func TestUISearchResultsLinkToNoteNoToken(t *testing.T) {
	p := &fakeProv{results: []bm.SearchResult{{Title: "Epic One", Type: "epic", Permalink: "proj/epics/E-1/main", Snippet: "s"}}}
	r := httptest.NewRequest("GET", "/ui/search/results?q=one&t="+tok, nil)
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Epic One") {
		t.Error("result title missing")
	}
	if !strings.Contains(body, `href="/ui/note?id=proj%2Fepics%2FE-1%2Fmain"`) {
		t.Errorf("result link wrong; body=%s", body)
	}
}

func TestUINewTodoPostViaCookie(t *testing.T) {
	p := &fakeProv{}
	form := url.Values{"text": {"do the thing"}}
	r := httptest.NewRequest("POST", "/ui/new-todo", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if p.appended != "do the thing" {
		t.Errorf("appended = %q", p.appended)
	}
}

func TestAssetServedWithoutAuth(t *testing.T) {
	r := httptest.NewRequest("GET", "/assets/mermaid.min.js", nil)
	w := httptest.NewRecorder()
	srv(&fakeProv{}).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("asset code %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("content-type = %q", ct)
	}
}
```

- [ ] **Step 2: Run; verify it fails**

Run: `go test ./internal/server/ -run 'TestUI|TestAsset'`
Expected: FAIL — `fakeProv` does not implement the widened `Provider` and `/ui/*` + `/assets/*` routes 404.

- [ ] **Step 3: Widen the Provider and register UI**

In `internal/server/server.go`, replace the `Provider` interface and register UI at the end of `New` (before `return mux`):

```go
type Provider interface {
	Snapshot() state.Snapshot
	Search(ctx context.Context, q string) ([]bm.SearchResult, error)
	Ack(ids []string)
	ReadNote(ctx context.Context, identifier string) (string, error)
	AppendTodo(ctx context.Context, text string) error
}
```

```go
	registerUI(mux, p, token)
```

- [ ] **Step 4: Implement the UI handlers**

Create `internal/server/ui.go`:

```go
package server

import (
	"crypto/subtle"
	"fmt"
	"html"
	"net/http"
)

// noteHref + URL building live in markdown.go (same package).

const sessionCookie = "gtb_session"

func registerUI(mux *http.ServeMux, p Provider, token string) {
	// Public OSS library — no secret, no auth. Cached aggressively.
	mux.HandleFunc("/assets/mermaid.min.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(mermaidJS)
	})

	mux.HandleFunc("/ui/search", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, pageShell("Search", `<h1>Search epics &amp; stories</h1>`+
			`<form method="GET" action="/ui/search/results">`+
			`<input name="q" autofocus placeholder="query" style="font-size:1rem;padding:.4rem;width:70%">`+
			`<button style="font-size:1rem;padding:.4rem .8rem">Search</button></form>`))
	}))

	mux.HandleFunc("/ui/search/results", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		res, err := p.Search(r.Context(), q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		body := `<h1>Results for ` + html.EscapeString(q) + `</h1>`
		if len(res) == 0 {
			body += `<p>No epics or stories matched.</p>`
		}
		body += `<ul>`
		for _, rr := range res {
			body += `<li><a href="` + noteHref(rr.Permalink) + `">` + html.EscapeString(rr.Title) + `</a> ` +
				`<small>(` + html.EscapeString(rr.Type) + `)</small><br><small>` +
				html.EscapeString(rr.Snippet) + `</small></li>`
		}
		body += `</ul><p><a href="/ui/search">New search</a></p>`
		writeHTML(w, pageShell("Results", body))
	}))

	mux.HandleFunc("/ui/note", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		md, err := p.ReadNote(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out, err := renderNoteHTML(id, md)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeHTML(w, out)
	}))

	mux.HandleFunc("/ui/new-todo", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			text := r.FormValue("text")
			if text == "" {
				http.Error(w, "empty todo", http.StatusBadRequest)
				return
			}
			if err := p.AppendTodo(r.Context(), text); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			writeHTML(w, pageShell("Added", `<h1>Added</h1><p>`+html.EscapeString(text)+
				`</p><p><a href="/ui/new-todo">Add another</a></p>`))
			return
		}
		writeHTML(w, pageShell("New todo", `<h1>New todo</h1>`+
			`<form method="POST" action="/ui/new-todo">`+
			`<input name="text" autofocus placeholder="what needs doing" style="font-size:1rem;padding:.4rem;width:70%">`+
			`<button style="font-size:1rem;padding:.4rem .8rem">Add</button></form>`))
	}))
}

// uiAuth allows a request bearing a valid ?t= token OR a valid session cookie.
// On a token hit it plants the cookie so in-page navigation needs no token.
func uiAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		viaToken := tokenOK(token, r.URL.Query().Get("t"))
		if !viaToken && !cookieOK(token, r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if viaToken {
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookie, Value: token, Path: "/ui",
				HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
		}
		next(w, r)
	}
}

func cookieOK(token string, r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && tokenOK(token, c.Value)
}

func tokenOK(want, got string) bool {
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func writeHTML(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, s)
}
```

- [ ] **Step 5: Run the server tests**

Run: `go test ./internal/server/`
Expected: PASS (existing `/state`/`/search`/`/ack` tests still pass; `Poller` already satisfies the wider `Provider` from Task 3, so `go build ./...` stays green).

- [ ] **Step 6: Commit**

```bash
git add internal/server/server.go internal/server/ui.go internal/server/ui_test.go
git commit -m "feat(gnome-topbar): loopback UI (search/note/new-todo) with cookie session"
```

---

## Task 6: Tray — local opener, clickable done, Search/New-todo items, Refresh/Quit to top

**Files:**
- Modify: `internal/tray/openurl.go`
- Modify: `internal/tray/tray.go`
- Test: `internal/tray/openurl_test.go` (new), `internal/tray/tray_test.go`

- [ ] **Step 1: Failing test for the loopback-only opener guard**

Create `internal/tray/openurl_test.go`:

```go
package tray

import "testing"

func TestOpenLocalRejectsNonLoopback(t *testing.T) {
	if err := OpenLocal("https://example.com"); err == nil {
		t.Error("expected refusal for non-loopback URL")
	}
	if err := OpenLocal("file:///etc/passwd"); err == nil {
		t.Error("expected refusal for file URL")
	}
}
```

- [ ] **Step 2: Run; verify it fails**

Run: `go test ./internal/tray/ -run TestOpenLocal`
Expected: FAIL — `undefined: OpenLocal`.

- [ ] **Step 3: Implement `OpenLocal`**

In `internal/tray/openurl.go`, add `"os/exec"` to the imports (keep existing `fmt`, `strings`) and append:

```go
// OpenLocal opens a loopback URL in the in-container browser (Chrome over X11)
// via xdg-open. The daemon's own UI pages live on the container's 127.0.0.1,
// which the host browser cannot reach, so these must NOT go through the host
// portal. Restricted to loopback as defense-in-depth. Non-blocking.
func OpenLocal(rawURL string) error {
	if !strings.HasPrefix(rawURL, "http://127.0.0.1") && !strings.HasPrefix(rawURL, "http://localhost") {
		return fmt.Errorf("refusing non-loopback URL: %q", rawURL)
	}
	return exec.Command("xdg-open", rawURL).Start()
}
```

- [ ] **Step 4: Run; verify it passes**

Run: `go test ./internal/tray/ -run TestOpenLocal`
Expected: PASS.

- [ ] **Step 5: Add the `Actions` type and thread it through `New`**

In `internal/tray/tray.go`, add the type after the `Provider` interface:

```go
// Actions are the side-effecting callbacks the tray triggers on clicks. Any
// field may be nil (treated as a no-op) so tests can construct a bare Tray.
type Actions struct {
	OpenSearch  func()               // open the search page in the in-container browser
	OpenNewTodo func()               // open the new-todo page
	MarkDone    func(rawLine string) // tick a todo bullet in Basic Memory
}
```

Add `act Actions` to the `Tray` struct, and add the raw-line slices next to the due/active pools:

```go
	dueParent *systray.MenuItem
	duePool   []*systray.MenuItem
	dueRaw    []string

	activeParent *systray.MenuItem
	activePool   []*systray.MenuItem
	activeRaw    []string
```

(Replace the existing `dueParent/duePool` and `activeParent/activePool` field lines with the versions above.)

Change the constructor:

```go
func New(p Provider, opener func(string), ack func([]string), act Actions) *Tray {
	return &Tray{prov: p, opener: opener, ack: ack, act: act, raised: map[string]bool{}}
}
```

- [ ] **Step 6: Make todo pools clickable, add menu items, reorder**

Add the `makeDonePool` helper next to `makeClickPool`:

```go
// makeDonePool creates n hidden clickable items under parent; clicking one calls
// Actions.MarkDone with the raw todo line backing that slot, then re-renders.
func (t *Tray) makeDonePool(n int, parent *systray.MenuItem) ([]*systray.MenuItem, []string) {
	pool := make([]*systray.MenuItem, n)
	raw := make([]string, n)
	for i := 0; i < n; i++ {
		mi := t.addItem(parent)
		mi.Hide()
		pool[i] = mi
		idx := i
		go func() {
			for range mi.ClickedCh {
				t.mu.Lock()
				line := raw[idx]
				t.mu.Unlock()
				if line != "" && t.act.MarkDone != nil {
					t.act.MarkDone(line)
					t.render()
				}
			}
		}()
	}
	return pool, raw
}
```

In `onReady`, replace the due/active pool creation:

```go
	t.dueParent = systray.AddMenuItem("✅ Due / overdue", "todos due or overdue")
	t.dueParent.Hide()
	t.duePool, t.dueRaw = t.makeDonePool(capDue, t.dueParent)

	t.activeParent = systray.AddMenuItem("📋 Active todos", "your active todos")
	t.activeParent.Hide()
	t.activePool, t.activeRaw = t.makeDonePool(capActive, t.activeParent)
```

Update `fillTodoPool` to record the raw line and its two call sites:

```go
func fillTodoPool(pool []*systray.MenuItem, raw []string, todos []bm.TodoItem, prefix string) {
	for i, mi := range pool {
		if i < len(todos) {
			mi.SetTitle(prefix + oneLine(todos[i].Text, labelWidth))
			raw[i] = todos[i].Raw
			mi.Show()
		} else {
			raw[i] = ""
			mi.Hide()
		}
	}
}
```

```go
	fillTodoPool(t.duePool, t.dueRaw, snap.Todos.Due, "⚠ ")
	...
	fillTodoPool(t.activePool, t.activeRaw, snap.Todos.Active, "")
```

Pin quick actions to the **top** of the menu — at the very start of `onReady` (before `t.nowItem`):

```go
	// Quick actions pinned to the top of the menu (closest the dbusmenu model
	// gets to "top-right corner buttons"; true top-bar buttons need a host
	// GNOME Shell extension — out of scope, see the plan).
	t.refreshItem = systray.AddMenuItem("↻ Refresh", "force an immediate poll")
	t.quitItem = systray.AddMenuItem("✕ Quit", "")
	searchItem := systray.AddMenuItem("🔎 Search epics/stories…", "open BM search in the browser")
	newTodoItem := systray.AddMenuItem("➕ New todo…", "create a todo in Basic Memory")
	systray.AddSeparator()
	go func() {
		for range searchItem.ClickedCh {
			if t.act.OpenSearch != nil {
				t.act.OpenSearch()
			}
		}
	}()
	go func() {
		for range newTodoItem.ClickedCh {
			if t.act.OpenNewTodo != nil {
				t.act.OpenNewTodo()
			}
		}
	}()
```

Then **delete** the old `systray.AddSeparator(); t.refreshItem = ...; t.quitItem = ...` block near the end of `onReady` (Refresh/Quit are now created at the top). **Keep** the two goroutines reading `t.refreshItem.ClickedCh` and `t.quitItem.ClickedCh`.

- [ ] **Step 7: Fix the existing tray tests**

In `internal/tray/tray_test.go`, append `, Actions{}` to every `New(...)` call, e.g.:

```go
	tr := New(p, func(string) {}, func([]string) {}, Actions{})
```

If any test calls `fillTodoPool`, pass a raw slice: `fillTodoPool(pool, make([]string, len(pool)), todos, "")`.

- [ ] **Step 8: Run the tray tests**

Run: `go test ./internal/tray/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/tray/
git commit -m "feat(gnome-topbar): clickable done, search/new-todo actions, refresh/quit on top"
```

---

## Task 7: Wire actions in main + build/verify

**Files:**
- Modify: `cmd/gnome-topbar-daemon/main.go`

- [ ] **Step 1: Build the local opener + actions and pass to the tray**

In `main.go`, replace the `tr := tray.New(...)` block with:

```go
	localBase := fmt.Sprintf("http://127.0.0.1:%d", cfg.ListenPort)
	openLocal := func(path string) {
		u := localBase + path + "?t=" + url.QueryEscape(cfg.APIToken)
		if err := tray.OpenLocal(u); err != nil {
			log.Error("open local ui", "path", path, "err", err)
		}
	}
	actions := tray.Actions{
		OpenSearch:  func() { openLocal("/ui/search") },
		OpenNewTodo: func() { openLocal("/ui/new-todo") },
		MarkDone: func(rawLine string) {
			if err := p.MarkTodoDone(ctx, rawLine); err != nil {
				log.Error("mark todo done", "err", err)
			}
		},
	}
	tr := tray.New(p, func(u string) {
		if err := tray.OpenURIOnHost(u); err != nil {
			log.Error("open url", "url", u, "err", err)
		}
	}, func(ids []string) { p.Ack(ids) }, actions)
```

Add `"net/url"` to the imports.

- [ ] **Step 2: Full build + race tests**

Run: `go build ./... && go test -race ./...`
Expected: all packages build and pass.

- [ ] **Step 3: Manual smoke test in the sandbox**

```bash
cd ../packaging && make run
```
Then on the tray menu: click **🔎 Search epics/stories…** (Chrome opens the search page) → search → click a result (the note renders; ```mermaid blocks draw; a `[[…]]` wikilink to another note is a clickable link that loads that note) → **➕ New todo…** → add one → confirm it appears under **📋 Active todos** → click a todo row → confirm it disappears (ticked in BM). Expected: each step works; `make run` logs show no errors.

- [ ] **Step 4: Changelog note in the README**

gnome-topbar has no `CHANGELOG.md`; release notes live in the GitHub release for the tag. Add a `## Changelog` section to `gnome-topbar/README.md`:

```markdown
## Changelog

### v0.2.0
- Basic Memory search, rendered note view (mermaid + clickable inter-note links), and todo create — opened in the in-container browser.
- Mark a todo done by clicking its tray row.
- Refresh / Quit / Search / New-todo pinned to the top of the menu.
```

- [ ] **Step 5: Commit**

```bash
git add cmd/gnome-topbar-daemon/main.go ../README.md
git commit -m "feat(gnome-topbar): wire search/new-todo/mark-done actions + v0.2.0 notes"
```

---

## Out of scope (requires a container change you don't have today)

- **#3 literal top-bar corner buttons with bordered icons.** A `StatusNotifierItem` gives one icon + a vendor-rendered `dbusmenu` list; it cannot place bordered icon-buttons in the top-bar corner. The only thing that can is a **host GNOME Shell extension**, which needs the host's `~/.local/share/gnome-shell/extensions` mounted **writable** into the sandbox plus a shell-reload channel — neither is mounted today (the X11 socket is mounted `:ro`, the extensions dir not at all). Task 6 delivers the achievable subset: Refresh/Quit/Search/New-todo pinned to the top of the dropdown with glyphs. If corner buttons become a hard requirement, the work moves to the `claude-sandbox` repo (mount + reload) and a new gjs extension — a separate plan.
- **Embedded WebView (in-app rendering instead of Chrome).** Would need `webkit2gtk-4.1` + GPU/`/dev/dri` + cgo. The browser-of-record here is the in-container Chrome, so this buys nothing.

## Deployment note (separate repo)

The sandbox runs a **pinned release binary**, not a local build. To make these features available in a fresh sandbox after merge:
1. Tag/release `gnome-topbar-v0.2.0` (follow the repo's gnome-topbar release procedure — split code from docs commits; the release workflow is triggered from the GitHub UI). The release build runs on GitHub Actions with open internet and embeds the committed `mermaid.min.js`.
2. In `~/Development/YC/claude-sandbox/Dockerfile.sandbox`, bump `ARG GNOME_TOPBAR_VERSION=gnome-topbar-v0.2.0` (and the matching default in `docker-compose.yml`), then rebuild the image.

During development, `make run` uses the locally built binary, so no image rebuild is needed to iterate.

---

## Self-review

- **Spec coverage.** Search (#1) → Tasks 4–7 UI + opener. Rendered MD + mermaid (#2) → Task 4 (`renderNoteHTML` + `/assets` route) + `/ui/note`. **Inter-note navigation** (the follow-up ask) → Task 4 wikilink/`memory://` rewriting to `/ui/note?id=` + Task 5 cookie session so links need no token. New-todo (#4) → Tasks 2,3,5,7. Mark done (#5) → Tasks 1,2,3,6. Refresh/Quit relocation (partial #3) → Task 6; full #3 explicitly deferred with rationale. mermaid fetch via npm (jsdelivr blocked) → Task 4 Step 1. BM token write scope → Prerequisites.
- **Placeholder scan.** No TBD/TODO; every code step carries complete code and exact commands.
- **Type consistency.** `TodoItem.Raw` (Task 1) → `fillTodoPool`/`makeDonePool` (Task 6) + `MarkTodoDone` (Task 2). `server.Provider` gains `ReadNote`+`AppendTodo` (Task 5), both on `Poller` (Task 3). `noteHref`/`renderNoteHTML` defined in Task 4, used in Task 5. `mermaidJS []byte` embedded in Task 4, served in Task 5. `tray.Actions{OpenSearch,OpenNewTodo,MarkDone}` defined Task 6, populated Task 7. `OpenLocal` defined Task 6, called Task 7. `tray.New` arity change (Task 6) fixed in the same task's test step and used in Task 7. Note route param is `id` (accepts BM permalink or title) consistently across Tasks 4, 5, and the search-result links.
```
