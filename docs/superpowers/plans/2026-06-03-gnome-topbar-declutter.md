# gnome-topbar Tray Declutter + Colored Usage Bars — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Flatten the Claude usage panel into per-account colored block bars and collapse the rest of the tray menu so a quiet menu is short.

**Architecture:** The tray is a `fyne.io/systray` v1.12.1 StatusNotifierItem rendered by a Go daemon. The menu is a plain vertical `dbusmenu` list — no positioning, no color, no horizontal layout. So severity is conveyed with **colored square emoji** (`🟩🟨🟥⬜`), and "collapse" means moving inline rows under collapsible submenu parents that hide when their count is 0. Pure label builders stay unit-tested; the `onReady`/`render` systray tree is verified by build + run (it has never been unit-tested, since systray can't run headless).

**Tech Stack:** Go 1.25, `fyne.io/systray`, standard library (`math`, `strings`, `fmt`, `sort`).

**Spec:** [`docs/superpowers/specs/2026-06-03-gnome-topbar-declutter-design.md`](../specs/2026-06-03-gnome-topbar-declutter-design.md)

**Branch:** `gnome-topbar-tray-declutter` (already created). All commands run from `gnome-topbar/daemon/` unless noted.

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `internal/tray/claude.go` | Modify | Add `barFill`, `usageBar`, `windowBarSegment`; replace `claudeInlineLabels` with `claudeOverviewLabels`; delete dead `windowInlineSuffix`. Pure, unit-tested. |
| `internal/tray/claude_test.go` | Modify | Replace the `claudeInlineLabels` tests with `claudeOverviewLabels` tests; add `TestBarFill` / `TestUsageBar`. |
| `internal/tray/tray.go` | Modify | Rewire Review/Due/Stats from inline to collapsible submenus; add `showIf` + hide-when-empty; add footer separator and `✕ Quit`; call `claudeOverviewLabels`. Not unit-tested (systray tree). |

Two tasks. Task 1 is the pure, fully-TDD'd label logic (keeps the build green by updating its one non-test caller). Task 2 is the systray restructure.

---

### Task 1: Colored usage bars + overview labels (`claude.go`)

**Goal:** Render the Claude overview as one row per account with 5h + week colored block bars, replacing the four verbose inline rows.

**Acceptance criteria:**
- `usageBar(pct, 5)` returns a 5-cell string of `🟩`/`🟨`/`🟥` filled cells (by threshold) plus `⬜` empties; a nonzero pct that rounds to 0 cells still shows exactly one filled cell; pct is clamped to `[0,100]` cells.
- `barFill(pct)` returns `🟩` for `pct < 60`, `🟨` for `60 ≤ pct < 80`, `🟥` for `pct ≥ 80`.
- `claudeOverviewLabels` returns one row per account that has limit-window data; a single such account is unprefixed and carries both windows on one row; multiple accounts are name-prefixed (alphabetical, padded). Cost and reset times do **not** appear in overview rows. Absent stats → `nil`; a stale snapshot prepends the existing stale marker; a limit-error-only or all-null-window account yields no overview row.
- `go test -race ./...` passes; `go build ./...` is green (sole non-test caller updated).

**Non-goals:** No change to `claudeUsageRows` (the detail submenu) or any other package.

**Files:**
- Modify: `internal/tray/claude.go`
- Modify: `internal/tray/claude.go:91-139` (delete `windowInlineSuffix`, replace `claudeInlineLabels`)
- Modify: `internal/tray/claude.go:1-15` (imports + threshold/width consts)
- Modify: `internal/tray/tray.go:230` (callsite rename)
- Test: `internal/tray/claude_test.go`

- [ ] **Step 1: Write the new + rewritten tests**

In `internal/tray/claude_test.go`: **add** these two new functions (anywhere in the file):

```go
func TestBarFill(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{0, "🟩"}, {59, "🟩"}, {59.9, "🟩"},
		{60, "🟨"}, {79, "🟨"}, {79.9, "🟨"},
		{80, "🟥"}, {100, "🟥"}, {150, "🟥"},
	}
	for _, c := range cases {
		if got := barFill(c.pct); got != c.want {
			t.Errorf("barFill(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestUsageBar(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{0, "⬜⬜⬜⬜⬜"},
		{3, "🟩⬜⬜⬜⬜"},   // nonzero rounds to 0 cells → forced to 1
		{27, "🟩⬜⬜⬜⬜"},  // round(1.35) = 1
		{38, "🟩🟩⬜⬜⬜"},  // round(1.9) = 2
		{60, "🟨🟨🟨⬜⬜"},  // round(3.0) = 3, yellow
		{65, "🟨🟨🟨⬜⬜"},  // round(3.25) = 3
		{80, "🟥🟥🟥🟥⬜"},  // round(4.0) = 4, red
		{82, "🟥🟥🟥🟥⬜"},  // round(4.1) = 4
		{100, "🟥🟥🟥🟥🟥"},
		{150, "🟥🟥🟥🟥🟥"}, // clamped to width
	}
	for _, c := range cases {
		if got := usageBar(c.pct, barWidth); got != c.want {
			t.Errorf("usageBar(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}
```

Then **replace** the five existing tests that reference `claudeInlineLabels`
(`TestClaudeInlineLabels_PrimaryAccount`, `TestClaudeInlineLabels_HighUtilizationWarns`,
`TestClaudeInlineLabels_AbsentOrNoLimits`, `TestClaudeInlineLabels_StaleMarker`, and
`TestEmptyWindowNotRendered`) with these. (`ptrF`, `claudeUsageRows`, and the other
detail tests stay untouched.)

```go
func TestClaudeOverviewLabels_PrimaryAccount(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset5h := time.Date(2026, 6, 3, 11, 46, 0, 0, time.UTC)
	reset7d := time.Date(2026, 6, 8, 20, 0, 0, 0, time.UTC)
	cs := claudestats.Stats{
		Present:     true,
		GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			"default": {
				Week: &claudestats.Usage{CostUSD: 84.10, TotalTokens: 33106912},
				Limits: &claudestats.Limits{
					FiveHour: &claudestats.Window{Utilization: ptrF(4), ResetsAt: &reset5h},
					SevenDay: &claudestats.Window{Utilization: ptrF(26), ResetsAt: &reset7d},
				},
			},
		},
	}
	got := claudeOverviewLabels(cs, now)
	if len(got) != 1 {
		t.Fatalf("single account → one overview row, got %d: %q", len(got), got)
	}
	row := got[0]
	for _, want := range []string{"5h", "4%", "wk", "26%", "🟩"} {
		if !strings.Contains(row, want) {
			t.Errorf("overview row %q missing %q", row, want)
		}
	}
	// Cost and reset time live in the detail submenu, not the overview.
	if strings.Contains(row, "$84") || strings.Contains(row, "resets") || strings.Contains(row, "in 2h") {
		t.Errorf("overview must not carry cost/reset: %q", row)
	}
	// Single limit-account → no account-name prefix.
	if strings.Contains(row, "default") {
		t.Errorf("single account should not be prefixed with its name: %q", row)
	}
}

func TestClaudeOverviewLabels_HighUtilizationWarnsRed(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	cs := claudestats.Stats{
		Present: true, GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			"default": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(91), ResetsAt: &reset},
			}},
		},
	}
	got := claudeOverviewLabels(cs, now)
	if len(got) != 1 || !strings.Contains(got[0], "⚠") || !strings.Contains(got[0], "🟥") {
		t.Errorf("91%% utilization should warn (⚠) and fill red (🟥): %q", got)
	}
}

func TestClaudeOverviewLabels_MultiAccountPrefixed(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	cs := claudestats.Stats{
		Present: true, GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			"default": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(27), ResetsAt: &reset},
				SevenDay: &claudestats.Window{Utilization: ptrF(38), ResetsAt: &reset},
			}},
			"alt": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(65), ResetsAt: &reset},
			}},
		},
	}
	got := claudeOverviewLabels(cs, now)
	if len(got) != 2 {
		t.Fatalf("two limit-accounts → two rows, got %d: %q", len(got), got)
	}
	// accountsWithWindows sorts keys: "alt" < "default".
	if !strings.Contains(got[0], "alt") || !strings.Contains(got[0], "🟨") || !strings.Contains(got[0], "65%") {
		t.Errorf("alt row should be first, yellow, 65%%: %q", got[0])
	}
	if !strings.Contains(got[1], "default") || !strings.Contains(got[1], "wk") {
		t.Errorf("default row should be second and carry the week window: %q", got[1])
	}
}

func TestClaudeOverviewLabels_AbsentOrNoLimits(t *testing.T) {
	now := time.Now()
	if got := claudeOverviewLabels(claudestats.Stats{Present: false}, now); got != nil {
		t.Errorf("absent stats → nil overview rows, got %q", got)
	}
	// Present but the only account's limit fetch failed → no overview rows.
	errMsg := "usage endpoint HTTP 401"
	cs := claudestats.Stats{Present: true, GeneratedAt: now, Accounts: map[string]claudestats.Account{
		"alt": {Limits: &claudestats.Limits{Error: &errMsg}},
	}}
	if got := claudeOverviewLabels(cs, now); len(got) != 0 {
		t.Errorf("limit-error-only account → no overview rows, got %q", got)
	}
}

func TestClaudeOverviewLabels_StaleMarker(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	cs := claudestats.Stats{
		Present:     true,
		GeneratedAt: now.Add(-15 * time.Minute), // stale
		Accounts: map[string]claudestats.Account{
			"default": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(4), ResetsAt: &reset},
			}},
		},
	}
	got := claudeOverviewLabels(cs, now)
	if len(got) == 0 || !strings.Contains(got[0], "stale") {
		t.Errorf("stale snapshot should lead with a stale marker row: %q", got)
	}
}

func TestEmptyWindowNotRendered(t *testing.T) {
	now := time.Now()
	cs := claudestats.Stats{Present: true, GeneratedAt: now, Accounts: map[string]claudestats.Account{
		"default": {Limits: &claudestats.Limits{FiveHour: &claudestats.Window{}}}, // all-null window
	}}
	if got := claudeOverviewLabels(cs, now); len(got) != 0 {
		t.Errorf("all-null window should produce no overview row, got %q", got)
	}
	rows := strings.Join(claudeUsageRows(cs, now), "\n")
	if strings.Contains(rows, "5h") {
		t.Errorf("all-null window should not render a bare 5h detail row:\n%s", rows)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `go test ./internal/tray/ 2>&1 | head -20`
Expected: build error — `undefined: barFill`, `undefined: usageBar`, `undefined: barWidth`, `undefined: claudeOverviewLabels`.

- [ ] **Step 3: Add the `math` import and threshold/width consts in `claude.go`**

Change the import block (`internal/tray/claude.go:3-11`) to add `"math"`:

```go
import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)
```

Then replace the existing `utilWarnPct` const block (`internal/tray/claude.go:13-15`):

```go
// utilWarnPct is the utilization at/above which a window is flagged ⚠ and its bar
// fills red (close to the plan limit). utilYellowPct is the lower amber threshold.
const (
	utilWarnPct   = 80.0
	utilYellowPct = 60.0
	// barWidth is the emoji cells per bar. Emoji are double-width, so a compact 5
	// keeps the overview row from getting too wide.
	barWidth = 5
)
```

- [ ] **Step 4: Add `barFill` + `usageBar` in `claude.go`**

Insert immediately above `accountsWithWindows` (`internal/tray/claude.go:63`):

```go
// barFill picks the severity glyph for a utilization percent: green below
// utilYellowPct, yellow at/above it, red at/above utilWarnPct.
func barFill(pct float64) string {
	switch {
	case pct >= utilWarnPct:
		return "🟥"
	case pct >= utilYellowPct:
		return "🟨"
	default:
		return "🟩"
	}
}

// usageBar renders a 0–100 percent as a width-cell colored bar (🟩/🟨/🟥 filled, ⬜
// empty). A nonzero value that rounds to 0 cells still shows one filled cell, so a
// low-but-present utilization never reads as fully empty; the cell count is clamped
// to [0, width].
func usageBar(pct float64, width int) string {
	full := int(math.Round(pct / 100 * float64(width)))
	if full < 0 {
		full = 0
	}
	if full > width {
		full = width
	}
	if full == 0 && pct > 0 {
		full = 1
	}
	return strings.Repeat(barFill(pct), full) + strings.Repeat("⬜", width-full)
}
```

- [ ] **Step 5: Delete `windowInlineSuffix` and replace `claudeInlineLabels`**

Delete the whole `windowInlineSuffix` function (`internal/tray/claude.go:91-105`, including its doc comment) and replace the whole `claudeInlineLabels` function (`internal/tray/claude.go:107-139`, including its doc comment) with:

```go
// windowBarSegment renders one window's overview cell: "5h 🟩⬜⬜⬜⬜ 27%" (the ⚠ is
// appended by utilPct at/above utilWarnPct). Returns "<name> —" when the window
// exists but its utilization is unknown, and "" when the window carries no data.
func windowBarSegment(name string, w *claudestats.Window) string {
	if !w.HasData() {
		return ""
	}
	if w.Utilization == nil {
		return name + " —"
	}
	return name + " " + usageBar(*w.Utilization, barWidth) + " " + utilPct(w)
}

// claudeOverviewLabels builds the always-visible overview rows: one row per account
// that has rate-limit window data, each carrying a 5h bar and a week bar. Cost, reset
// times, and per-period usage live in the detail submenu (claudeUsageRows), not here.
// Returns nil when stats are absent or no account has limit windows. The account name
// (key) is shown, left-padded for alignment, only when more than one account has
// windows.
func claudeOverviewLabels(cs claudestats.Stats, now time.Time) []string {
	if !cs.Present {
		return nil
	}
	keys := accountsWithWindows(cs)
	multi := len(keys) > 1
	nameW := 0
	if multi {
		for _, k := range keys {
			if len(k) > nameW {
				nameW = len(k)
			}
		}
	}
	var rows []string
	for _, k := range keys {
		a := cs.Accounts[k]
		prefix := "🤖 "
		if multi {
			prefix += fmt.Sprintf("%-*s  ", nameW, k)
		}
		var segs []string
		if s := windowBarSegment("5h", a.Limits.FiveHour); s != "" {
			segs = append(segs, s)
		}
		if s := windowBarSegment("wk", a.Limits.SevenDay); s != "" {
			segs = append(segs, s)
		}
		rows = append(rows, prefix+strings.Join(segs, "  "))
	}
	if cs.Stale(now) {
		rows = append([]string{"🤖 ⚠ Claude stats stale (" + humanAge(now.Sub(cs.GeneratedAt)) + ")"}, rows...)
	}
	return rows
}
```

(`accountsWithWindows` guarantees `a.Limits != nil` for every key it returns, so `a.Limits.FiveHour`/`.SevenDay` are safe.)

- [ ] **Step 6: Update the sole non-test caller in `tray.go`**

In `internal/tray/tray.go:230`, rename the call:

```go
	fillLabelPool(t.claudePool, claudeOverviewLabels(snap.ClaudeStats, now))
```

- [ ] **Step 7: Verify build, vet, and tests pass**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: build clean, vet clean, `ok` across all packages (the only behavior change is in `internal/tray`). (No `windowInlineSuffix`/`claudeInlineLabels` undefined errors; `fmt`/`math`/`strings` all still used.)

- [ ] **Step 8: Commit**

```bash
cd /home/pgilmore/Development/Patiently/anti-tangent-mcp
git add gnome-topbar/daemon/internal/tray/claude.go gnome-topbar/daemon/internal/tray/claude_test.go gnome-topbar/daemon/internal/tray/tray.go
git commit -m "feat(gnome-topbar): colored per-account Claude usage bars

Replace the four verbose inline Claude rows with one row per account
carrying 5h + week colored block bars (🟩 <60%, 🟨 ≥60%, 🟥 ≥80%, ⬜
empty), 5 cells wide. Cost/reset move to the detail submenu. Drop the
now-dead windowInlineSuffix.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Restructure the tray menu (collapse + hide-empty + footer)

**Goal:** Collapse Review requested / Due / Stats into submenus (My open PRs and Active todos are already submenus), make every collapsible section — all five — hide when its count is 0, and add a separator + `✕ Quit` footer.

**Acceptance criteria:**
- Review requested, My open PRs, Due / overdue, Active todos, and Stats each render as a collapsible submenu that is shown only when it has ≥1 item (Stats: only when `AntiTangent.Present`).
- Review-requested children remain clickable (open the PR URL).
- A separator sits above the footer; the footer reads `↻ Refresh` then `✕ Quit`.
- The Claude overview uses `claudeOverviewLabels`; the `🤖 Claude usage` detail submenu is unchanged.
- `go build ./...`, `go vet ./...`, and `go test -race ./...` all pass.

**Non-goals:** No change to notifications, polling, `claude.go`, or the `/state` endpoint. No unit test for the systray tree (consistent with the existing code).

**Files:**
- Modify: `internal/tray/tray.go` (struct fields ~39-72; `onReady` ~92-128; `render` ~205-237; add `showIf` helper)

- [ ] **Step 1: Update the `Tray` struct fields**

In `internal/tray/tray.go`, replace the `rrHeader` field (line ~50) and the `dueHeader` field (line ~58) and the bare `statPool` field (line ~64) so the three collapsing sections carry submenu parents. The relevant struct region becomes:

```go
	rrParent *systray.MenuItem // collapsible submenu (hidden when empty)
	rrPool   []*systray.MenuItem
	rrURLs   []string

	myPRsParent *systray.MenuItem // collapsible submenu (hidden when empty)
	myPRsPool   []*systray.MenuItem
	myPRsURLs   []string

	dueParent *systray.MenuItem // collapsible submenu (hidden when empty)
	duePool   []*systray.MenuItem

	activeParent *systray.MenuItem // collapsible submenu (hidden when empty)
	activePool   []*systray.MenuItem

	statsParent *systray.MenuItem // collapsible submenu (hidden when absent)
	statPool    []*systray.MenuItem
```

(Leave `nowItem`, `errPool`, `claudePool`, `claudeParent`, `claudeUsagePool`, `refreshItem`, `quitItem` as they are.)

- [ ] **Step 2: Rewrite the `onReady` item construction**

Replace the block from the `// Review requested` comment through the `t.quitItem = ...` line (`internal/tray/tray.go:99-128`) with:

```go
	// Review requested — collapsed submenu (hidden when count is 0)
	t.rrParent = systray.AddMenuItem("🔵 Review requested", "PRs awaiting your review")
	t.rrParent.Hide()
	t.rrPool, t.rrURLs = t.makeClickPool(capReviewReq, t.rrParent)

	// My open PRs — collapsed submenu (hidden when count is 0)
	t.myPRsParent = systray.AddMenuItem("🟣 My open PRs", "your open pull requests")
	t.myPRsParent.Hide()
	t.myPRsPool, t.myPRsURLs = t.makeClickPool(capMyPRs, t.myPRsParent)

	// Due / overdue todos — collapsed submenu (hidden when count is 0)
	t.dueParent = systray.AddMenuItem("✅ Due / overdue", "todos due or overdue")
	t.dueParent.Hide()
	t.duePool = t.makeDisabledPool(capDue, t.dueParent)

	// Active todos — collapsed submenu (hidden when count is 0)
	t.activeParent = systray.AddMenuItem("📋 Active todos", "your active todos")
	t.activeParent.Hide()
	t.activePool = t.makeDisabledPool(capActive, t.activeParent)

	// anti-tangent / CodeScene stats — collapsed submenu, shown only when present
	t.statsParent = systray.AddMenuItem("📊 Stats", "anti-tangent / CodeScene stats")
	t.statsParent.Hide()
	t.statPool = t.makeDisabledPool(capStat, t.statsParent)

	// Claude usage — inline per-account bar overview (shown only when present), with a
	// collapsed submenu carrying per-account detail.
	t.claudePool = t.makeDisabledPool(capClaude, nil)
	t.claudeParent = systray.AddMenuItem("🤖 Claude usage", "Claude Code usage + rate limits")
	t.claudeParent.Hide()
	t.claudeUsagePool = t.makeDisabledPool(capClaudeUse, t.claudeParent)

	systray.AddSeparator()
	t.refreshItem = systray.AddMenuItem("↻ Refresh", "")
	t.quitItem = systray.AddMenuItem("✕ Quit", "")
```

- [ ] **Step 3: Rewrite the `render` section for the five collapsibles + Claude**

Replace the block from `t.rrHeader.SetTitle(...)` through the `fillLabelPool(t.claudePool, ...)` line (`internal/tray/tray.go:208-230`) with:

```go
	t.rrParent.SetTitle(fmt.Sprintf("🔵 Review requested (%d)", len(snap.PRs.ReviewRequested)))
	fillPRPool(t.rrPool, t.rrURLs, snap.PRs.ReviewRequested)
	showIf(t.rrParent, len(snap.PRs.ReviewRequested) > 0)

	t.myPRsParent.SetTitle(fmt.Sprintf("🟣 My open PRs (%d)", len(snap.PRs.Authored)))
	fillPRPool(t.myPRsPool, t.myPRsURLs, snap.PRs.Authored)
	showIf(t.myPRsParent, len(snap.PRs.Authored) > 0)

	t.dueParent.SetTitle(fmt.Sprintf("✅ Due / overdue (%d)", len(snap.Todos.Due)))
	fillTodoPool(t.duePool, snap.Todos.Due, "⚠ ")
	showIf(t.dueParent, len(snap.Todos.Due) > 0)

	t.activeParent.SetTitle(fmt.Sprintf("📋 Active todos (%d)", len(snap.Todos.Active)))
	fillTodoPool(t.activePool, snap.Todos.Active, "")
	showIf(t.activeParent, len(snap.Todos.Active) > 0)

	var stats []string
	if at := snap.AntiTangent; at.Present {
		stats = append(stats, antiTangentLabel(at))
		if at.CodeScene != nil {
			stats = append(stats, codeSceneLabel(at.CodeScene))
		}
	}
	fillLabelPool(t.statPool, stats)
	showIf(t.statsParent, len(stats) > 0)

	// Claude usage: inline per-account bar overview + per-account detail submenu.
	fillLabelPool(t.claudePool, claudeOverviewLabels(snap.ClaudeStats, now))
```

(The lines immediately after — `fillLabelPool(t.claudeUsagePool, ...)` and the
`claudeParent` Show/Hide block — stay exactly as they are.)

- [ ] **Step 4: Add the `showIf` helper**

Insert next to the other pool helpers, immediately above `fillSourceErrors` (`internal/tray/tray.go:286`):

```go
// showIf shows mi when cond is true, else hides it — used to drop a zero-count
// submenu from the menu entirely (caller holds t.mu).
func showIf(mi *systray.MenuItem, cond bool) {
	if cond {
		mi.Show()
	} else {
		mi.Hide()
	}
}
```

- [ ] **Step 5: Verify build, vet, and tests**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: all clean; no references to `rrHeader`/`dueHeader` remain
(`grep -rn "rrHeader\|dueHeader" internal/` returns nothing).

- [ ] **Step 6: Manual smoke check (if a GNOME session is available)**

Run (from `gnome-topbar/`): `cd packaging && make run`
Expected: tray icon appears; opening the menu shows `🛠 Currently working on`, then
collapsed `▸ 🔵 Review requested` / `▸ 🟣 My open PRs` / `▸ ✅ Due / overdue` /
`▸ 📋 Active todos` (each hidden when its count is 0), the colored Claude bar rows,
`▸ 🤖 Claude usage`, a separator, then `↻ Refresh` and `✕ Quit`. Click `✕ Quit` to
exit. If no display/credentials are available, rely on Step 5 (the systray tree has
no unit coverage by design).

- [ ] **Step 7: Commit**

```bash
cd /home/pgilmore/Development/Patiently/anti-tangent-mcp
git add gnome-topbar/daemon/internal/tray/tray.go
git commit -m "feat(gnome-topbar): collapse menu sections + tidy footer

Review requested / Due / Stats move from inline rows into collapsible
submenus; every collapsible section hides when its count is 0. Add a
separator above the footer and render Quit as '✕ Quit'.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## After merge — cut the release

gnome-topbar releases independently of the anti-tangent binary via a
`gnome-topbar-vX.Y.Z` tag (workflow `.github/workflows/gnome-topbar-release.yml`); no
CHANGELOG gate. After this branch merges to `main`, tag the patch release:

```bash
git tag gnome-topbar-v0.1.2 && git push origin gnome-topbar-v0.1.2
```

The workflow builds the static Linux daemon (`-X main.version=gnome-topbar-v0.1.2`)
and publishes the GitHub Release.

---

## Self-Review

**Spec coverage** (every §5 design point maps to a task):
- §5.2 colored bars / `usageBar` / `barFill` / `windowBarSegment` / `claudeOverviewLabels` → Task 1.
- §5.3 Review-requested submenu, §5.4 Due submenu, §5.6 Stats submenu → Task 2 Steps 2–3.
- §5.5 hide-when-empty (all five collapsibles) → Task 2 `showIf` + Step 3.
- §5.7 footer separator + `✕ Quit` → Task 2 Step 2.
- §5.8 wiring/struct/order → Task 2 Steps 1–4.
- §6 edge cases → covered by Task 1 tests (stale, util==nil, all-null, ≥80% ⚠+red, color thresholds, nonzero→1 cell).
- §7 testing split (pure builders tested; systray tree build+run only) → Task 1 vs Task 2.
- §8 release `gnome-topbar-v0.1.2` → "After merge" section.

**Placeholder scan:** none — every code/test step shows full content; no TBD/TODO.

**Type consistency:** `barWidth`/`utilWarnPct`/`utilYellowPct` consts defined in Task 1 Step 3, used in Steps 1/4/5 and tests. `claudeOverviewLabels`/`usageBar`/`barFill`/`windowBarSegment` defined in Task 1, called in Task 2 Step 3. `showIf` defined Task 2 Step 4, used Step 3. Struct fields `rrParent`/`dueParent`/`statsParent` defined Step 1, used Steps 2–3.
