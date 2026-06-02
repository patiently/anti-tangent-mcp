# gnome-topbar Tray Implementation Plan (Component B, v2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the (superseded) gjs extension with an in-sandbox **Go tray** — a StatusNotifierItem on the host top bar showing PRs / todos / currently-working-on / anti-tangent stats, with native notifications and open-in-host-browser — reusing the already-built daemon (Tasks 0–12).

**Architecture:** One Go binary = the existing daemon (data layer: config/mcphttp/bm/github/state/server) + a new `internal/tray` package. The tray uses **`fyne.io/systray`** (pure-DBus on Linux, no GTK — spike-confirmed) for the SNI icon + `dbusmenu`, and **`github.com/godbus/dbus/v5`** directly for notifications (`org.freedesktop.Notifications`) and open-in-browser (`org.freedesktop.portal.Desktop.OpenURI`). The tray reads the daemon's `Poller` snapshot in-process; the loopback HTTP server stays as a debug interface.

**Tech Stack:** Go 1.25 (the `gnome-topbar/daemon` module), `fyne.io/systray` v1.12.1, `github.com/godbus/dbus/v5`. Verified-live on the host (shared session bus + X11) rather than via a nested shell.

**Design spec:** `docs/superpowers/specs/2026-06-02-gnome-topbar-mvp-design.md` (v2).

**Environment facts (spike-verified):** the sandbox shares the host session bus (`$DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/dbus-…`); `org.kde.StatusNotifierWatcher` (owner present), `org.freedesktop.Notifications`, and `org.freedesktop.portal.Desktop` are all reachable; `notify-send` reaches the host; `fyne.io/systray` builds without GTK. `DISPLAY=:10`.

**Privacy (PUBLIC repo):** committed files use placeholders only; no real BM namespace, tickets, repos, URLs, or tokens. The probe in Task L uses the real `pgilmore` namespace via env, never committed.

---

## Status / dependencies

Daemon Tasks 0–12 are complete (data layer, race-green, live-verified). This plan adds the remaining work. Tasks T0–T2 are TDD (unit-testable); T3–T5 are DBus/SNI glue **verified live on the host** (not unit-tested); T6–T7 are packaging + live acceptance.

---

## File structure

```
gnome-topbar/daemon/
  internal/
    bm/nowworking.go            # MODIFY: detect BM "Note Not Found" → NotFound flag
    atstats/atstats.go          # NEW: anti-tangent stats reader (rollup.json + codescene)
    state/state.go              # MODIFY: add Snapshot.AntiTangent field
    tray/
      menu.go                   # NEW: pure BuildMenu(snapshot, now) []Row  (unit-tested)
      tray.go                   # NEW: fyne.io/systray glue — icon, item pool, applier, refresh
      notify.go                 # NEW: godbus org.freedesktop.Notifications
      openurl.go                # NEW: godbus portal OpenURI (host browser)
      icon.go + icon.png        # NEW: embedded tray icon
  cmd/gnome-topbar-daemon/main.go  # MODIFY: wire atstats refresh + start tray (systray.Run)
gnome-topbar/packaging/Makefile    # MODIFY: add `run` target
gnome-topbar/README.md             # MODIFY: sandbox vs host run modes
```

**Type ownership (defined once):**
- `bm.NowWorking { Body string; Updated time.Time; HasUpdated bool; NotFound bool }` (adds `NotFound`)
- `atstats.Stats { Present bool; GeneratedAt time.Time; TotalCalls int; PassPct/WarnPct/FailPct float64; TopCategory string; ReviewMSP95 int64; Summary string; CodeScene *CodeSceneStats }`
- `atstats.CodeSceneStats { Runs int; LatestScore/LatestDelta/ScoreP50 float64; LatestTrend string; Regressions/Improvements/Neutral int; CategoryHistogram map[string]int }`
- `state.Snapshot` gains `AntiTangent atstats.Stats json:"anti_tangent"`
- `tray.Row { Kind RowKind; Label string; URL string; Disabled bool }`; `tray.RowKind` enum

---

## Task T0: daemon — handle BM "Note Not Found" for currently-working-on

**Files:**
- Modify: `gnome-topbar/daemon/internal/bm/nowworking.go`
- Test: `gnome-topbar/daemon/internal/bm/nowworking_test.go`

Basic Memory returns a missing note as a *successful* `read_note` whose text begins `# Note Not Found in`. The parser must flag this so the tray shows "(not set up)" instead of the guidance prose.

- [ ] **Step 1: Add the failing test**

Append to `nowworking_test.go`:
```go
func TestParseNowWorkingDetectsNoteNotFound(t *testing.T) {
	md := "\n# Note Not Found in main: \"alice/notes/currently-working-on/main\"\n\nI couldn't find an exact match...\n"
	nw := ParseNowWorking(md)
	if !nw.NotFound {
		t.Fatalf("expected NotFound=true, got %+v", nw)
	}
}

func TestParseNowWorkingRealNoteIsNotFlagged(t *testing.T) {
	md := "---\ntitle: x\nupdated: 2026-06-02T08:00:00Z\n---\n\nWiring the tray.\n"
	nw := ParseNowWorking(md)
	if nw.NotFound {
		t.Fatalf("real note wrongly flagged NotFound: %+v", nw)
	}
	if nw.Body != "Wiring the tray." {
		t.Fatalf("body=%q", nw.Body)
	}
}
```

- [ ] **Step 2: Run → confirm FAIL**

Run: `cd gnome-topbar/daemon && go test ./internal/bm/... -run NowWorking && cd -`
Expected: FAIL — `nw.NotFound` undefined.

- [ ] **Step 3: Implement**

In `nowworking.go`, add `NotFound bool \`json:"not_found"\`` to the `NowWorking` struct, and at the end of `ParseNowWorking`, before `return nw`:
```go
	if strings.HasPrefix(strings.TrimSpace(md), "# Note Not Found in") {
		nw.NotFound = true
		nw.Body = ""
	}
```
(`strings` is already imported.)

- [ ] **Step 4: Run → confirm PASS**

Run: `cd gnome-topbar/daemon && go test -race ./internal/bm/... && cd -`
Expected: PASS (whole package).

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon/internal/bm/nowworking.go gnome-topbar/daemon/internal/bm/nowworking_test.go
git commit -m "fix(gnome-topbar): flag BM 'Note Not Found' so currently-working-on shows '(not set up)'"
```

---

## Task T1: daemon — anti-tangent stats reader + snapshot field + wiring

**Files:**
- Create: `gnome-topbar/daemon/internal/atstats/atstats.go`
- Test: `gnome-topbar/daemon/internal/atstats/atstats_test.go`
- Modify: `gnome-topbar/daemon/internal/config/config.go` (StatsDir field + default)
- Modify: `gnome-topbar/daemon/internal/state/state.go` (Snapshot.AntiTangent)
- Modify: `gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go` (refreshAntiTangent)

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/atstats/atstats_test.go`:
```go
package atstats

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAbsentIsNotPresent(t *testing.T) {
	if Read(filepath.Join(t.TempDir(), "nope")).Present {
		t.Fatal("expected not present")
	}
	if Read("").Present {
		t.Fatal("empty dir must be not present")
	}
}

func TestReadParsesRollupSummaryAndCodescene(t *testing.T) {
	dir := t.TempDir()
	rollup := `{"total_calls":10,"verdict_counts":{"pass":7,"warn":2,"fail":1},
	  "category_histogram":{"ambiguous_spec":5},"review_ms_p95":1800,"generated_at":"2026-06-02T08:00:00Z",
	  "codescene":{"runs":12,"latest_score":8.4,"latest_delta":-0.3,"latest_trend":"regression",
	  "score_p50":8.6,"regressions":3,"improvements":7,"neutral":2,"category_histogram":{"complex-method":5}}}`
	_ = os.WriteFile(filepath.Join(dir, "rollup.json"), []byte(rollup), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "summary.md"), []byte("All healthy."), 0o600)
	s := Read(dir)
	if !s.Present || s.TotalCalls != 10 || s.ReviewMSP95 != 1800 {
		t.Fatalf("bad: %+v", s)
	}
	if s.PassPct != 70 || s.WarnPct != 20 || s.FailPct != 10 {
		t.Fatalf("pct: %+v", s)
	}
	if s.TopCategory != "ambiguous_spec" || s.Summary != "All healthy." {
		t.Fatalf("cat/summary: %+v", s)
	}
	if s.CodeScene == nil || s.CodeScene.Runs != 12 || s.CodeScene.LatestTrend != "regression" {
		t.Fatalf("codescene: %+v", s.CodeScene)
	}
}

func TestReadCodesceneAbsentIsNil(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "rollup.json"), []byte(`{"total_calls":1,"verdict_counts":{"pass":1},"generated_at":"2026-06-02T08:00:00Z"}`), 0o600)
	if Read(dir).CodeScene != nil {
		t.Fatal("expected nil CodeScene")
	}
}
```

- [ ] **Step 2: Run → confirm FAIL**

Run: `cd gnome-topbar/daemon && go test ./internal/atstats/... && cd -`
Expected: FAIL — `undefined: Read`.

- [ ] **Step 3: Implement `atstats.go`**

`gnome-topbar/daemon/internal/atstats/atstats.go`:
```go
// Package atstats reads the anti-tangent v0.10.0 stats output (rollup.json +
// summary.md, with an optional codescene object) if present. Pure local file
// reads; absence is reported as Present=false with no error.
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
	CodeScene   *CodeSceneStats `json:"codescene,omitempty"`
}

// CodeSceneStats mirrors the optional top-level "codescene" object inside
// anti-tangent's rollup.json. Contract: 2026-06-02-anti-tangent-stats-design.md.
type CodeSceneStats struct {
	Runs              int            `json:"runs"`
	LatestScore       float64        `json:"latest_score"`
	LatestDelta       float64        `json:"latest_delta"`
	LatestTrend       string         `json:"latest_trend"`
	ScoreP50          float64        `json:"score_p50"`
	Regressions       int            `json:"regressions"`
	Improvements      int            `json:"improvements"`
	Neutral           int            `json:"neutral"`
	CategoryHistogram map[string]int `json:"category_histogram"`
}

type rollup struct {
	TotalCalls        int             `json:"total_calls"`
	VerdictCounts     map[string]int  `json:"verdict_counts"`
	CategoryHistogram map[string]int  `json:"category_histogram"`
	ReviewMSP95       int64           `json:"review_ms_p95"`
	GeneratedAt       time.Time       `json:"generated_at"`
	CodeScene         *CodeSceneStats `json:"codescene"`
}

// Read returns Present=false (no error) when dir is empty or rollup.json is
// absent/unreadable/unparseable.
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
		Present: true, GeneratedAt: r.GeneratedAt, TotalCalls: r.TotalCalls,
		ReviewMSP95: r.ReviewMSP95, TopCategory: topKey(r.CategoryHistogram), CodeScene: r.CodeScene,
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

- [ ] **Step 4: Run → confirm PASS**

Run: `cd gnome-topbar/daemon && go test -race ./internal/atstats/... && cd -`
Expected: PASS (all three).

- [ ] **Step 5: Add `StatsDir` to config**

In `internal/config/config.go`, add to `Config`:
```go
	StatsDir string `toml:"stats_dir"`
```
and at the end of `Load`, before writing client.json, default it when empty:
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
(`os` and `path/filepath` are already imported.)

- [ ] **Step 6: Add `AntiTangent` to `Snapshot`**

In `internal/state/state.go`, add the import `"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"` and add to the `Snapshot` struct, after `Sources`:
```go
	AntiTangent atstats.Stats `json:"anti_tangent"`
```

- [ ] **Step 7: Wire a refresh in main**

In `cmd/gnome-topbar-daemon/main.go`, add the import for `atstats`, then a method:
```go
func (p *Poller) refreshAntiTangent(ctx context.Context) {
	s := atstats.Read(p.cfg.StatsDir)
	p.mu.Lock()
	p.snap.AntiTangent = s
	p.snap.GeneratedAt = time.Now()
	p.mu.Unlock()
}
```
In `main`, after the initial `p.refreshBM(ctx)` block:
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
git commit -m "feat(gnome-topbar): anti-tangent stats reader + snapshot field + refresh"
```

---

## Task T2: tray — pure menu model

**Files:**
- Create: `gnome-topbar/daemon/internal/tray/menu.go`
- Test: `gnome-topbar/daemon/internal/tray/menu_test.go`

A pure function `BuildMenu(snapshot, now) []Row` — no DBus, fully unit-tested. The glue (Task T3) renders these rows onto systray items.

- [ ] **Step 1: Write the failing test**

`gnome-topbar/daemon/internal/tray/menu_test.go`:
```go
package tray

import (
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

func sampleSnapshot() state.Snapshot {
	var s state.Snapshot
	s.Sources = map[string]state.SourceStatus{"github": {OK: true}, "basic-memory": {OK: true}}
	s.NowWorking = bm.NowWorking{Body: "Wiring the tray", HasUpdated: true,
		Updated: time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)}
	s.PRs.ReviewRequested = []github.PR{{Repo: "o/r", Number: 7, Title: "Fix", URL: "https://x/7"}}
	s.PRs.Authored = []github.PR{{Repo: "o/r", Number: 8, Title: "Add", URL: "https://x/8"}}
	due := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	s.Todos.Due = []bm.TodoItem{{Text: "ship it", Due: &due, Overdue: true}}
	s.Todos.Active = []bm.TodoItem{{Text: "later"}}
	return s
}

func find(rows []Row, kind RowKind) []Row {
	var out []Row
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func TestBuildMenuPRsAndTodos(t *testing.T) {
	rows := BuildMenu(sampleSnapshot(), time.Date(2026, 6, 2, 8, 5, 0, 0, time.UTC))
	prs := find(rows, RowPR)
	if len(prs) != 2 {
		t.Fatalf("want 2 PR rows, got %d", len(prs))
	}
	if prs[0].URL != "https://x/7" {
		t.Fatalf("review-requested PR should be first with its URL: %+v", prs[0])
	}
	// a PR row is clickable (has URL, not disabled); a todo row is disabled.
	todos := find(rows, RowTodo)
	if len(todos) != 2 {
		t.Fatalf("want 2 todo rows, got %d", len(todos))
	}
	for _, td := range todos {
		if !td.Disabled {
			t.Fatal("todo rows must be disabled")
		}
	}
}

func TestBuildMenuNowWorkingNotSet(t *testing.T) {
	s := sampleSnapshot()
	s.NowWorking = bm.NowWorking{NotFound: true}
	rows := BuildMenu(s, time.Now())
	h := find(rows, RowHeader)
	found := false
	for _, r := range h {
		if r.Label == "🛠 Currently working on — (not set up)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected not-set header, headers=%v", h)
	}
}

func TestBuildMenuStatsOmittedWhenAbsent(t *testing.T) {
	s := sampleSnapshot()
	s.AntiTangent = atstats.Stats{Present: false}
	rows := BuildMenu(s, time.Now())
	for _, r := range rows {
		if r.Kind == RowStat {
			t.Fatalf("stats must be omitted when absent: %+v", r)
		}
	}
}

func TestBuildMenuStatsShownWhenPresent(t *testing.T) {
	s := sampleSnapshot()
	s.AntiTangent = atstats.Stats{Present: true, TotalCalls: 10, PassPct: 70, ReviewMSP95: 1800, TopCategory: "ambiguous_spec"}
	rows := BuildMenu(s, time.Now())
	if len(find(rows, RowStat)) == 0 {
		t.Fatal("expected a stat row when present")
	}
}

func TestBuildMenuAlwaysHasRefreshAndQuit(t *testing.T) {
	rows := BuildMenu(sampleSnapshot(), time.Now())
	acts := find(rows, RowAction)
	if len(acts) != 2 || acts[0].Label != "↻ Refresh" || acts[1].Label != "Quit" {
		t.Fatalf("want Refresh+Quit actions, got %+v", acts)
	}
}
```

- [ ] **Step 2: Run → confirm FAIL**

Run: `cd gnome-topbar/daemon && go test ./internal/tray/... && cd -`
Expected: FAIL — `undefined: BuildMenu`.

- [ ] **Step 3: Implement `menu.go`**

`gnome-topbar/daemon/internal/tray/menu.go`:
```go
// Package tray renders the daemon snapshot as a StatusNotifierItem tray menu.
// BuildMenu is a pure function (no DBus) so it can be unit-tested; tray.go maps
// its output onto fyne.io/systray items.
package tray

import (
	"fmt"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

type RowKind int

const (
	RowHeader    RowKind = iota // section / now-working header (disabled)
	RowPR                       // a PR (clickable; has URL)
	RowTodo                     // a todo (disabled)
	RowStat                     // a stats line (disabled)
	RowSourceErr                // a "source error" line (disabled)
	RowSeparator
	RowAction // Refresh / Quit
)

type Row struct {
	Kind     RowKind
	Label    string
	URL      string // RowPR only
	Disabled bool
}

// BuildMenu turns a snapshot into logical menu rows. `now` drives the
// currently-working-on age, injected for testability.
func BuildMenu(s state.Snapshot, now time.Time) []Row {
	var rows []Row
	add := func(k RowKind, label string) { rows = append(rows, Row{Kind: k, Label: label, Disabled: k != RowPR && k != RowAction}) }

	// currently-working-on
	nw := s.NowWorking
	if nw.NotFound || strings.TrimSpace(nw.Body) == "" {
		add(RowHeader, "🛠 Currently working on — (not set up)")
	} else {
		age := ""
		if nw.HasUpdated {
			age = " (⟳ " + humanAge(now.Sub(nw.Updated)) + ")"
		}
		add(RowHeader, "🛠 "+oneLine(nw.Body, 80)+age)
	}
	add(RowSeparator, "")

	// PRs
	add(RowHeader, fmt.Sprintf("🔵 Review requested (%d)", len(s.PRs.ReviewRequested)))
	for _, pr := range s.PRs.ReviewRequested {
		rows = append(rows, Row{Kind: RowPR, Label: prLabel(pr.Repo, pr.Number, pr.Title), URL: pr.URL})
	}
	add(RowHeader, fmt.Sprintf("🟣 My open PRs (%d)", len(s.PRs.Authored)))
	for _, pr := range s.PRs.Authored {
		rows = append(rows, Row{Kind: RowPR, Label: prLabel(pr.Repo, pr.Number, pr.Title), URL: pr.URL})
	}
	add(RowSeparator, "")

	// todos
	add(RowHeader, fmt.Sprintf("✅ Due / overdue (%d)", len(s.Todos.Due)))
	for _, td := range s.Todos.Due {
		add(RowTodo, "⚠ "+oneLine(td.Text, 80))
	}
	add(RowHeader, fmt.Sprintf("Active (%d)", len(s.Todos.Active)))
	for _, td := range s.Todos.Active {
		add(RowTodo, "  "+oneLine(td.Text, 80))
	}

	// per-source errors
	for name, st := range s.Sources {
		if !st.OK {
			add(RowSourceErr, "⚠ "+name+": "+oneLine(st.Error, 60))
		}
	}

	// anti-tangent stats (only when present)
	if at := s.AntiTangent; at.Present {
		add(RowSeparator, "")
		add(RowStat, fmt.Sprintf("🛡 anti-tangent — %d calls · %.0f%%/%.0f%%/%.0f%% · top %s · p95 %dms",
			at.TotalCalls, at.PassPct, at.WarnPct, at.FailPct, dash(at.TopCategory), at.ReviewMSP95))
		if cs := at.CodeScene; cs != nil {
			add(RowStat, fmt.Sprintf("📊 CodeScene — score %.1f (%s) · %dr/%di",
				cs.LatestScore, dash(cs.LatestTrend), cs.Regressions, cs.Improvements))
		}
	}

	// actions
	add(RowSeparator, "")
	rows = append(rows, Row{Kind: RowAction, Label: "↻ Refresh"})
	rows = append(rows, Row{Kind: RowAction, Label: "Quit"})
	return rows
}

func prLabel(repo string, num int, title string) string {
	return fmt.Sprintf("%s #%d  %s", repo, num, oneLine(title, 60))
}

func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func humanAge(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
```

- [ ] **Step 4: Run → confirm PASS**

Run: `cd gnome-topbar/daemon && go test -race ./internal/tray/... && cd -`
Expected: PASS (all menu tests).

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon/internal/tray/menu.go gnome-topbar/daemon/internal/tray/menu_test.go
git commit -m "feat(gnome-topbar): pure tray menu model (snapshot -> rows)"
```

---

## Task T3: tray — SNI glue (fyne.io/systray) + embedded icon

**Files:**
- Create: `gnome-topbar/daemon/internal/tray/icon.go`, `gnome-topbar/daemon/internal/tray/icon.png`
- Create: `gnome-topbar/daemon/internal/tray/tray.go`

This is **DBus/SNI glue, verified live** (Task L), not unit-tested. It maps `BuildMenu` rows onto a fixed pool of `systray` menu items (the lib's menu is append-only, so we pre-allocate slots and Show/Hide them).

- [ ] **Step 1: Add deps**

Run: `cd gnome-topbar/daemon && go get fyne.io/systray@v1.12.1 && go get github.com/godbus/dbus/v5 && cd -`

- [ ] **Step 2: Generate + embed a tray icon**

Create the icon with a tiny throwaway generator (run once, then delete it):
```bash
cd gnome-topbar/daemon/internal/tray
cat > /tmp/genicon.go <<'EOF'
package main
import ("image";"image/color";"image/png";"os")
func main(){
 const n=22; img:=image.NewRGBA(image.Rect(0,0,n,n))
 c:=color.RGBA{0x3b,0x82,0xf6,0xff} // a blue square (generic, no branding)
 for y:=0;y<n;y++{for x:=0;x<n;x++{img.Set(x,y,c)}}
 f,_:=os.Create("icon.png"); defer f.Close(); png.Encode(f,img)
}
EOF
go run /tmp/genicon.go && rm /tmp/genicon.go && ls -l icon.png
cd -
```
`gnome-topbar/daemon/internal/tray/icon.go`:
```go
package tray

import _ "embed"

//go:embed icon.png
var trayIcon []byte
```

- [ ] **Step 3: Implement `tray.go`**

`gnome-topbar/daemon/internal/tray/tray.go`:
```go
package tray

import (
	"context"
	"strconv"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

// Provider is the slice of the daemon the tray needs (implemented by *main.Poller).
type Provider interface {
	Snapshot() state.Snapshot
	RefreshNow(ctx context.Context) // force an immediate poll of all sources
}

const slots = 30 // max rendered rows per dynamic section pool

type Tray struct {
	prov   Provider
	cancel context.CancelFunc
	opener func(url string) // injected: open a URL on the host (Task T4)

	pool []*systray.MenuItem // flat pool of pre-allocated items, reused each refresh
	urls []string            // url backing each pool slot (for click handling)
	mu   sync.Mutex
}

// New returns a Tray. opener is the host open-URL function (tray/openurl.go).
func New(p Provider, opener func(string)) *Tray { return &Tray{prov: p, opener: opener} }

// Run blocks, running the systray event loop until Quit. Call on the main
// goroutine. ctx cancellation also stops it.
func (t *Tray) Run(ctx context.Context) {
	ctx, t.cancel = context.WithCancel(ctx)
	systray.Run(func() { t.onReady(ctx) }, func() {})
}

func (t *Tray) onReady(ctx context.Context) {
	systray.SetIcon(trayIcon)
	systray.SetTitle("")
	systray.SetTooltip("gnome-topbar")

	// Pre-allocate a flat pool of items + click handlers. Items beyond the
	// current row count are hidden each refresh (the lib's menu is append-only).
	for i := 0; i < slots; i++ {
		mi := systray.AddMenuItem("", "")
		mi.Hide()
		t.pool = append(t.pool, mi)
		t.urls = append(t.urls, "")
		idx := i
		go func() {
			for range mi.ClickedCh {
				t.onClick(ctx, idx)
			}
		}()
	}

	t.render() // immediate
	go func() {
		tk := time.NewTicker(30 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				systray.Quit()
				return
			case <-tk.C:
				t.render()
			}
		}
	}()
}

func (t *Tray) render() {
	snap := t.prov.Snapshot()
	rows := BuildMenu(snap, time.Now())
	t.mu.Lock()
	for i, mi := range t.pool {
		if i < len(rows) {
			r := rows[i]
			label := r.Label
			if r.Kind == RowSeparator {
				label = "────────"
			}
			mi.SetTitle(label)
			if r.Disabled {
				mi.Disable()
			} else {
				mi.Enable()
			}
			t.urls[i] = r.URL
			mi.Show()
		} else {
			mi.Hide()
			t.urls[i] = ""
		}
	}
	t.mu.Unlock()

	if n := len(snap.PRs.ReviewRequested) + len(snap.Todos.Due); n > 0 {
		systray.SetTooltip("gnome-topbar · " + strconv.Itoa(n) + " need attention")
	} else {
		systray.SetTooltip("gnome-topbar")
	}
	// (Task T4 appends notification-raising here, using `snap`.)
}

func (t *Tray) onClick(ctx context.Context, idx int) {
	t.mu.Lock()
	url := t.urls[idx]
	t.mu.Unlock()
	if url != "" {
		t.opener(url)
		return
	}
	// Non-URL rows: dispatch the current Refresh/Quit actions by row label.
	rows := BuildMenu(t.prov.Snapshot(), time.Now())
	if idx < len(rows) {
		switch rows[idx].Label {
		case "↻ Refresh":
			t.prov.RefreshNow(ctx)
			t.render()
		case "Quit":
			systray.Quit()
		}
	}
}
```

> **Implementer note (verify against fyne.io/systray v1.12.1 — compiler-checked):** the expected API is `systray.Run(onReady, onExit func())`, `systray.AddMenuItem(title, tooltip) *MenuItem`, `MenuItem.ClickedCh`, `systray.SetIcon/SetTitle/SetTooltip`, `MenuItem.Show/Hide/Enable/Disable/SetTitle`, `systray.Quit()`. If a signature differs in v1.12.1, adjust — the structure is unaffected. Separators: this plan renders separator rows as a disabled "────────" label item from the pool (visually adequate); using real `systray.AddSeparator()` outside the pool is optional polish.

- [ ] **Step 4: Build (no unit test — glue)**

Run: `cd gnome-topbar/daemon && go build ./... && go vet ./... && cd -`
Expected: clean build. (Behavioral verification is Task L.)

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon/internal/tray gnome-topbar/daemon/go.mod gnome-topbar/daemon/go.sum
git commit -m "feat(gnome-topbar): tray SNI glue (fyne.io/systray) + embedded icon"
```

---

## Task T4: tray — notifications + open-URL-on-host (godbus)

**Files:**
- Create: `gnome-topbar/daemon/internal/tray/openurl.go`
- Create: `gnome-topbar/daemon/internal/tray/notify.go`

DBus glue (verified live). Open URLs via the host portal; raise notifications via the host bus.

- [ ] **Step 1: Implement `openurl.go`**

`gnome-topbar/daemon/internal/tray/openurl.go`:
```go
package tray

import (
	"github.com/godbus/dbus/v5"
)

// OpenURIOnHost opens url in the host's default browser via the desktop portal
// (org.freedesktop.portal.Desktop), which is reachable on the shared session
// bus. Best-effort: errors are returned for logging, never fatal.
func OpenURIOnHost(url string) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	obj := conn.Object("org.freedesktop.portal.Desktop", dbus.ObjectPath("/org/freedesktop/portal/desktop"))
	// OpenURI(parent_window string, uri string, options a{sv}) -> handle o
	call := obj.Call("org.freedesktop.portal.OpenURI.OpenURI", 0, "", url, map[string]dbus.Variant{})
	return call.Err
}
```

- [ ] **Step 2: Implement `notify.go`**

`gnome-topbar/daemon/internal/tray/notify.go`:
```go
package tray

import (
	"github.com/godbus/dbus/v5"
)

// Notify raises a host desktop notification via org.freedesktop.Notifications.
// Returns the notification id (or error). Best-effort.
func Notify(summary, body string) (uint32, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return 0, err
	}
	obj := conn.Object("org.freedesktop.Notifications", dbus.ObjectPath("/org/freedesktop/Notifications"))
	// Notify(app_name, replaces_id, app_icon, summary, body, actions, hints, expire_timeout)
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		"gnome-topbar", uint32(0), "", summary, body,
		[]string{}, map[string]dbus.Variant{}, int32(-1))
	if call.Err != nil {
		return 0, call.Err
	}
	var id uint32
	_ = call.Store(&id)
	return id, nil
}
```

- [ ] **Step 3: Wire notifications into the tray refresh**

In `tray.go`'s `render()`, after rebuilding the menu, raise notifications for new un-acked events and ack them. Add to the `Tray` struct a `raised map[string]bool` (init in `New`) and an `ack func([]string)` injected dependency, then at the end of `render()`:
```go
	for _, ev := range snap.UnackedEvents {
		if t.raised[ev.ID] {
			continue
		}
		t.raised[ev.ID] = true
		title := "Todo due"
		if ev.Kind == "review_request" {
			title = "Review requested: " + ev.Title
		}
		_, _ = Notify(title, ev.Body)
	}
	if ids := unackedIDs(snap); len(ids) > 0 && t.ack != nil {
		t.ack(ids)
	}
```
Add `raised map[string]bool` (alloc in `New`), an `ack func([]string)` field set via a new param or setter, and:
```go
func unackedIDs(s state.Snapshot) []string {
	ids := make([]string, 0, len(s.UnackedEvents))
	for _, e := range s.UnackedEvents {
		ids = append(ids, e.ID)
	}
	return ids
}
```
Update `New` to `func New(p Provider, opener func(string), ack func([]string)) *Tray` and set `raised: map[string]bool{}`.

- [ ] **Step 4: Build**

Run: `cd gnome-topbar/daemon && go build ./... && go vet ./... && go test -race ./... && cd -`
Expected: clean build, existing tests still pass (menu_test unaffected; the tray package has no unit tests for the DBus glue).

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon/internal/tray
git commit -m "feat(gnome-topbar): tray notifications + open-URL-on-host via godbus"
```

---

## Task T5: main wiring — run the tray alongside the daemon

**Files:**
- Modify: `gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go`

`systray.Run` blocks, so start the poller + HTTP in goroutines and run the tray on the main goroutine. Give the `Poller` the methods the tray's `Provider` needs.

- [ ] **Step 1: Add `RefreshNow` to `Poller`**

In `main.go`, add:
```go
func (p *Poller) RefreshNow(ctx context.Context) {
	p.refreshGitHub(ctx)
	p.refreshBM(ctx)
	p.refreshAntiTangent(ctx)
}
```
(`Snapshot()` already exists from Task 11.)

- [ ] **Step 2: Start HTTP in a goroutine, run the tray on main**

In `main`, replace the blocking `srv.ListenAndServe()` tail with:
```go
	go func() {
		log.Info("debug http listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("serve", "err", err)
		}
	}()

	tr := tray.New(p, func(url string) {
		if err := tray.OpenURIOnHost(url); err != nil {
			log.Error("open url", "url", url, "err", err)
		}
	}, func(ids []string) { p.Ack(ids) })

	tr.Run(ctx) // blocks until Quit / ctx cancel
	cancel()
	sc, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	_ = srv.Shutdown(sc)
```
Add the import `"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/tray"`. Keep the SIGINT/SIGTERM `signal.NotifyContext` `ctx` from Task 11 (it cancels `tr.Run` cleanly).

- [ ] **Step 3: Build + vet + full test**

Run: `cd gnome-topbar/daemon && go build ./... && go vet ./... && go test -race ./... && cd -`
Expected: clean build, all unit tests PASS.

- [ ] **Step 4: Commit**

```bash
git add gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go
git commit -m "feat(gnome-topbar): wire tray into main (run alongside daemon)"
```

---

## Task T6: packaging — `make run` + README run modes

**Files:**
- Modify: `gnome-topbar/packaging/Makefile`
- Modify: `gnome-topbar/README.md`

- [ ] **Step 1: Add a `run` target**

Append to `gnome-topbar/packaging/Makefile`:
```makefile
run: build
	$(DAEMON_BIN)
```

- [ ] **Step 2: Document run modes in README**

Replace the README's "Install" / extension sections with sandbox-vs-host run modes:
- **Sandbox (here):** ensure `~/.config/gnome-topbar/config.toml` has `bm_username`; `BM_URL`/`BM_BEARER_TOKEN` in env; the process inherits `DBUS_SESSION_BUS_ADDRESS` + `DISPLAY`. `cd packaging && make run` → a tray icon appears on the host top bar.
- **Normal host:** `make install-daemon enable` runs the same binary as a `systemd --user` service (it has the user session bus + display). No gnome-shell extension to install.
- Remove references to `make install-extension` / `gnome-extensions enable` / nested shells.

- [ ] **Step 3: Commit**

```bash
git add gnome-topbar/packaging/Makefile gnome-topbar/README.md
git commit -m "docs(gnome-topbar): make run target + sandbox/host run modes"
```

---

## Task L: live host verification (the acceptance gate)

**Files:** none (verification only; run interactively with the operator present at their GNOME desktop).

- [ ] **Step 1: Full unit suite**

Run: `cd gnome-topbar/daemon && go test -race ./... && cd -`
Expected: all PASS (config, mcphttp, bm, github, state, server, atstats, tray menu model).

- [ ] **Step 2: Run the binary against real services + the host bus**

Ensure `~/.config/gnome-topbar/config.toml` has `bm_username = "pgilmore"` (local only, never committed) and `BM_URL`/`BM_BEARER_TOKEN` are in env. Then:
```bash
cd gnome-topbar/daemon && go run ./cmd/gnome-topbar-daemon &
```

- [ ] **Step 3: Confirm on the host (operator observes)**

- [ ] A tray icon appears on the host GNOME top bar.
- [ ] Its menu shows: currently-working-on (or "(not set up)"), review-requested + my PRs (real counts), due/overdue + active todos, and — if `stats_dir` has a `rollup.json` — the anti-tangent (+ CodeScene) line; otherwise that section is absent.
- [ ] Clicking a PR opens it in the **host** browser.
- [ ] Deleting `~/.local/state/gnome-topbar/seen.json` while running re-surfaces current review-requests/due-todos as **one** host notification each (no repeat on later refreshes; no repeat after restart).
- [ ] "↻ Refresh" forces a poll; "Quit" exits cleanly.
- [ ] No personal data in committed files: `git grep -nE "pgilmore|YN-[0-9]|patiently/(powow|yobify)|youcruit" -- gnome-topbar/` returns nothing.

- [ ] **Step 4: Stop + commit any fixes**

```bash
kill %1 2>/dev/null
git add -A && git commit -m "test(gnome-topbar): live host verification fixes" # only if fixes were needed
```

---

## Notes on remaining risk

- **fyne.io/systray API surface** (Task T3) is the main unknown; the build step + Task L catch any drift in `ClickedCh`/`AddMenuItem`/`SetIcon` against v1.12.1. The menu *model* (T2) is lib-independent and fully tested.
- **Append-only menu:** the pool-of-slots approach (cap `slots=30`) bounds rendered rows; very long PR/todo lists truncate — acceptable for a tray, and the tooltip count reflects the true totals.
- **Portal OpenURI** signature (Task T4) is verified live; if the host's portal rejects an empty `parent_window`, fall back to `org.freedesktop.portal.OpenURI` with a window-handle hint or to GNOME's `org.gnome.Shell`-side handler.
- **rollup.json contract** (Task T1) — snake_case keys must match the anti-tangent v0.10.0 stats writer (the parked `version/0.10.0` work); see the design spec §3.5a.
