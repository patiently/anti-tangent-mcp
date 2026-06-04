# gnome-topbar tray declutter + Claude usage bars

**Status:** design (awaiting review)
**Date:** 2026-06-03
**Component:** `gnome-topbar/daemon` (systray UI layer)
**Supersedes nothing.** Builds on [`2026-06-02-gnome-topbar-mvp-design.md`](2026-06-02-gnome-topbar-mvp-design.md) and the v0.1.1 Claude usage panel.

## 1. Motivation

The tray dropdown has grown cluttered. On a normal day it shows ~13 always-visible
rows, the noisiest being the **four inline Claude usage rows** (5h + 7d per account,
each carrying utilization %, cost, and reset time). The user wants:

1. The Claude overview **flattened to compact usage bars** (5h + week), with full
   detail kept in the existing expandable submenu.
2. A **full declutter pass**: collapse non-essential sections into submenus, keep
   only what needs attention inline.
3. A **tidy footer** for Refresh / Quit.

## 2. Goals

- Replace the 4 inline Claude rows with **one bar row per account** (5h bar + week
  bar), cost/reset moved into the detail submenu.
- Collapse **Review requested**, **Due / overdue**, and **Stats** (anti-tangent /
  CodeScene) from always-inline into collapsed submenus, matching how **My open PRs**
  and **Active todos** already behave.
- **Hide collapsed sections whose count is 0** so a quiet menu is genuinely short.
- Add a **separator** above the footer and render Refresh / Quit as compact
  glyph+text rows (`↻ Refresh`, `✕ Quit`).
- Keep all pure label logic unit-tested; no behavior regressions in the detail
  submenu.

## 3. Non-goals

- No new data sources, no schema changes to `claude-stats.json`.
- No PNG menu-item icons (`MenuItem.SetIcon`). GNOME's AppIndicator extension
  frequently ignores per-item icons; the reliable path is Unicode glyphs in the
  title text. See §9.
- **No GNOME Shell extension.** See §4 — the literal "icons in the top-right corner
  with borders" is impossible in the current architecture and out of scope.
- No change to notifications, polling, the `/state` debug endpoint, or any non-tray
  package.

## 4. Hard constraint: the menu is a vertical dbusmenu list

The tray is a `fyne.io/systray` v1.12.1 **StatusNotifierItem**, whose menu is a DBus
`com.canonical.dbusmenu` that GNOME renders as a **plain vertical list of full-width
rows**. The only per-row affordances are: title text, tooltip, optional left-aligned
icon, checkbox, separator, enable/disable, show/hide. There is:

- **no positioning** (items stack top-to-bottom in insertion order — no "corner"),
- **no horizontal layout** (nothing side-by-side),
- **no styling / borders** (GNOME draws standard menu rows).

Therefore "Quit as a bordered ✕ in the top-right corner with Refresh next to it" is
**not achievable** here — that needs a custom popup, which only a GNOME Shell
extension (custom `St.Button` widgets) can draw, and this project is deliberately
*not* a Shell extension (MVP design §1.2). The chosen footer (§5.6) is the tidy,
reliable approximation: a separator plus compact glyph+text rows at the bottom.

## 5. Design

### 5.1 Final menu layout

Top → bottom (`▸` = collapsed submenu; everything else is inline). Insertion order in
`onReady` preserves today's relative ordering — only the inline sections become
submenus, so nothing reshuffles:

```
🛠 Currently working on — …                       inline header (always)
⚠ <source>: <error>                               inline, only when a source fails
▸ 🔵 Review requested (3)                          submenu  (was inline)
▸ 🟣 My open PRs (14)                               submenu  (unchanged)
▸ ✅ Due / overdue (2)                              submenu  (was inline)
▸ 📋 Active todos (3)                               submenu  (unchanged)
▸ 📊 Stats                                          submenu  (was 2 inline rows)
🤖 default  5h 🟩⬜⬜⬜⬜ 27%  wk 🟩🟩⬜⬜⬜ 38%        inline bars, 1 row/account
🤖 alt      5h 🟩⬜⬜⬜⬜  3%  wk 🟩⬜⬜⬜⬜  7%
▸ 🤖 Claude usage (2)                               detail submenu (unchanged)
──────────────                                     separator
↻ Refresh
✕ Quit
```

Each collapsed submenu **hides entirely when its count is 0** (§5.5). This is new
behavior for My open PRs and Active todos (which today always render) and for the
now-collapsed Review requested / Due sections.

### 5.2 Claude usage bars (`internal/tray/claude.go`)

Replace `claudeInlineLabels` with **`claudeOverviewLabels(cs, now) []string`**: one
row per account that has renderable limit-window data, each row carrying a 5h bar and
a week bar.

**Bar renderer.** Menu labels are plain text with no per-glyph color, so severity is
conveyed with **colored square emoji** (which render in color, like the menu's
existing `🔵 🟣 ✅`). The whole bar takes the threshold color: `🟩` green `< 60%`,
`🟨` yellow `≥ 60%`, `🟥` red `≥ 80%`; `⬜` is the empty cell. Because emoji are
double-width, the bar is **5 cells** (a wider bar makes the row too wide).

```go
const barWidth = 5 // emoji cells per bar (emoji are double-width — keep compact)

// utilYellowPct is the utilization at/above which the bar turns yellow.
// utilWarnPct (existing, = 80) is the red threshold and also drives the ⚠ suffix.
const utilYellowPct = 60.0

// barFill picks the severity glyph for a utilization percent.
func barFill(pct float64) string {
    switch {
    case pct >= utilWarnPct:   return "🟥"
    case pct >= utilYellowPct: return "🟨"
    default:                   return "🟩"
    }
}

// usageBar renders a 0–100 percent as a barWidth-cell colored bar. A nonzero value
// that rounds to 0 filled cells shows 1 cell, so it never reads as fully empty.
func usageBar(pct float64, width int) string {
    full := int(math.Round(pct / 100 * float64(width)))
    if full < 0 { full = 0 }
    if full > width { full = width }
    if full == 0 && pct > 0 { full = 1 }
    return strings.Repeat(barFill(pct), full) + strings.Repeat("⬜", width-full)
}
```

Worked examples (width 5): `3% → 🟩⬜⬜⬜⬜`, `27% → 🟩⬜⬜⬜⬜`, `38% → 🟩🟩⬜⬜⬜`,
`60% → 🟨🟨🟨⬜⬜`, `65% → 🟨🟨🟨⬜⬜`, `80% → 🟥🟥🟥🟥⬜`, `82% → 🟥🟥🟥🟥⬜`,
`100% → 🟥🟥🟥🟥🟥`. Thresholds are at-or-above: exactly 60% is yellow, exactly 80% is
red.

**Per-window segment** (reuses the existing `utilPct`, which already appends ` ⚠` at
`>= utilWarnPct` = 80%):

```go
// windowBarSegment renders "5h 🟩⬜⬜⬜⬜ 27%" (or "… 🟥🟥🟥🟥⬜ 91% ⚠"), or "5h —"
// when the window exists but utilization is unknown, or "" when it has no data.
func windowBarSegment(name string, w *claudestats.Window) string {
    if !w.HasData()        { return "" }
    if w.Utilization == nil { return name + " —" }
    return name + " " + usageBar(*w.Utilization, barWidth) + " " + utilPct(w)
}
```

**Row assembly** (`claudeOverviewLabels`):

- Return `nil` immediately when `!cs.Present`. Otherwise iterate
  `accountsWithWindows(cs)` (existing helper: the sorted account keys with ≥1
  `HasData` window).
- `multi := len(keys) > 1`. Show the account **key** (short, e.g. `default`/`alt`) as
  a prefix only when `multi`; a single account drops the name (`🤖 5h … wk …`),
  matching today's inline behavior. When `multi`, right-pad keys to the longest key
  width for best-effort column alignment (proportional menu font → not pixel-perfect;
  block glyphs mostly line up — accepted).
- Each row: `"🤖 " + [paddedName + " "] + join(non-empty segments, "  ")`, where the
  segments are `windowBarSegment("5h", FiveHour)` and `windowBarSegment("wk", SevenDay)`.
- **Stale**: if `cs.Stale(now)`, prepend `🤖 ⚠ Claude stats stale (<age>)` exactly as
  today (preserved even when there are no window rows).

The overview labels the week window **`wk`** (the approved mockup). The detail submenu
keeps its existing **`7d`** label (`windowDetail`); this asymmetry is intentional —
`wk` reads better next to a bar.

**Cost, reset times, today/month, active block, and `limits unavailable` are NOT in
the overview** — they already render in `claudeUsageRows` (the detail submenu), which
is unchanged. Accounts whose limit fetch errored (all windows nil) or that have no
window data simply get no overview row; their detail rows still appear in the submenu.

### 5.3 Collapse Review requested → submenu

`Review requested` becomes a collapsed submenu instead of an inline header + inline
clickable rows. Clicking a child still opens the PR URL (the existing `makeClickPool`
already supports a parent). Notifications for new review-requests are unaffected, so
collapsing it does not bury anything urgent.

### 5.4 Collapse Due / overdue → submenu

Same treatment: a collapsed submenu carrying the disabled `⚠ <todo>` rows. Due-todo
notifications are unaffected.

### 5.5 Hide-when-empty

In `render()`, each collapsible parent is shown only when it has content:

| Section | Show when |
|---|---|
| Review requested | `len(ReviewRequested) > 0` |
| My open PRs | `len(Authored) > 0` |
| Due / overdue | `len(Due) > 0` |
| Active todos | `len(Active) > 0` |
| Stats | `AntiTangent.Present` (existing gate) |
| Claude usage | `ClaudeStats.Present && len(Accounts) > 0` (existing) |

Hiding a parent `MenuItem` hides the whole entry; this is the same pattern already
used for `claudeParent`. The error rows (`errPool`) and the `Currently working on`
header remain unconditionally inline.

### 5.6 Stats submenu

The `🛡 anti-tangent …` and `📊 CodeScene …` labels (built by `antiTangentLabel` /
`codeSceneLabel`, unchanged) move from an inline pool into a `📊 Stats` collapsed
submenu, shown only when `AntiTangent.Present`.

### 5.7 Footer

- Insert `systray.AddSeparator()` immediately before the Refresh item in `onReady`.
- `refreshItem` title stays `↻ Refresh`.
- `quitItem` title becomes `✕ Quit` (glyph in the title text — reliable, it is just
  text; no `SetIcon`).

### 5.8 `tray.go` wiring changes

`Tray` struct: drop `rrHeader`, `dueHeader`; add `rrParent`, `dueParent`,
`statsParent` (`*systray.MenuItem`). `onReady` insertion order (preserving §5.1):

1. `nowItem` (inline)
2. `errPool` (inline)
3. `rrParent` submenu + `rrPool = makeClickPool(capReviewReq, rrParent)`
4. `myPRsParent` submenu + pool (unchanged)
5. `dueParent` submenu + `duePool = makeDisabledPool(capDue, dueParent)`
6. `activeParent` submenu + pool (unchanged)
7. `statsParent` submenu + `statPool = makeDisabledPool(capStat, statsParent)`
8. `claudePool` (inline bars)
9. `claudeParent` submenu + `claudeUsagePool` (unchanged)
10. `systray.AddSeparator()`
11. `refreshItem`, `quitItem`

`render()`: set each parent's title with its count, call the matching `fill*` helper,
and `Show()`/`Hide()` the parent per §5.5. `claudePool` is filled from
`claudeOverviewLabels`. Pool caps unchanged (`capClaude = 8` still covers
1 row/account + stale marker).

## 6. Edge cases (all preserved or newly specified)

| Case | Behavior |
|---|---|
| Stats absent / Claude absent | Their submenus hidden (no empty entries). |
| Account limit fetch errored | No overview row; `limits unavailable` shows in detail submenu (today's behavior). |
| Window has resets but `utilization == nil` | Segment renders `5h —` (no bar). |
| All-null window | Segment omitted; no bare `5h`/`wk` row. |
| Single account | No name prefix; both bars on one row. |
| Stale snapshot | `🤖 ⚠ Claude stats stale (…)` prepended. |
| `< 60%` / `≥ 60%` / `≥ 80%` utilization | Bar fill `🟩` / `🟨` / `🟥` (at-or-above). |
| `≥ 80%` utilization | ` ⚠` appended after the percent (existing `utilWarnPct`), in addition to the red fill. |
| Nonzero but rounds to 0 cells | 1 colored cell (never fully `⬜`). |
| Source failure | `⚠ <source>: <error>` stays inline near the top. |

## 7. Testing strategy

Pure label builders are unit-tested (matching the existing `claude_test.go` style);
the `onReady`/`render` systray tree is **not** unit-tested (systray can't run
headless) and is verified by running the tray (`make run`) and eyeballing the menu —
same split as today.

New / updated table tests in `internal/tray/claude_test.go`:

- `usageBar` / `barFill`: the worked examples in §5.2, the color-threshold
  boundaries (59/60/79/80 → green/yellow/red), the nonzero→1-cell rule, 0%, 100%,
  and a clamp case (`>100%`).
- `claudeOverviewLabels`: single account (no name prefix, both bars one row);
  multi-account (name prefixes, padded); `>= 80%` appends `⚠`; `utilization == nil`
  → `5h —`; stale prepends the marker; absent → `nil`; limit-error-only account →
  no row. (Replaces the `claudeInlineLabels` tests.)
- `claudeUsageRows` detail tests remain green unchanged (the submenu is untouched).

Mainline: `go test -race ./...` inside `gnome-topbar/daemon`.

## 8. Versioning / release

gnome-topbar releases independently of the anti-tangent binary via a
`gnome-topbar-vX.Y.Z` git tag (workflow `.github/workflows/gnome-topbar-release.yml`);
there is no CHANGELOG gate and no `VERSION` file (the version is injected via
`-ldflags -X main.version=<tag>` at release build). Released as a patch bump →
next tag **`gnome-topbar-v0.1.2`**. Work lands on a feature branch (e.g.
`gnome-topbar-tray-declutter`); the anti-tangent version/CHANGELOG CI path-excludes
`gnome-topbar/**`, so the `version/X.Y.Z`-branch convention does not apply here. The
release tag is cut after merge.

## 9. Out of scope / future

- **Top-right bordered icon buttons** → requires a GNOME Shell extension rewrite of
  the UI layer (custom `St` popup). Large effort, reverses the project's "no Shell
  extension" decision; a separate project if ever wanted.
- **PNG menu-item icons** via `MenuItem.SetIcon` → unreliable on GNOME's AppIndicator
  extension; deferred in favor of glyph titles.
- **Count-threshold auto-collapse** (inline if ≤ N, else collapse) → YAGNI; sections
  are statically inline or collapsed.
