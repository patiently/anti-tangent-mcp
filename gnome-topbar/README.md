# gnome-topbar

A GNOME top-bar tray showing your GitHub PRs, Basic Memory todos, a "currently
working on" summary, and (optionally) anti-tangent/CodeScene stats plus a Claude
usage panel — backed by a small Go daemon.
See `../docs/superpowers/specs/2026-06-02-gnome-topbar-mvp-design.md`.

## Claude usage panel

When `$ANTI_TANGENT_STATS_DIR/claude-stats.json` is present (written by the
claude-sandbox usage poller), the tray shows per-account Claude Code usage and
the real 5h / weekly rate-limit utilization + reset times. The file is the
contract's consumer side; the schema and producer behaviour live in the
`claude-sandbox` repo at `docs/claude-stats/`. The panel is inert when the file
is absent, and degrades gracefully when a limit fetch failed
(`limits.error` → "limits unavailable", cost fields still render).

## Prerequisites
- GNOME Shell 45/46/47 (Wayland or X11)
- `gh` CLI logged in (`gh auth status`)
- Basic Memory reachable; `BM_URL` + `BM_BEARER_TOKEN` available
- Go 1.25 to build

## Run modes

### Sandbox (development / Claude Code session)

The sandbox shares the host session bus (`$DBUS_SESSION_BUS_ADDRESS`) and
display (`$DISPLAY`), so the tray appears on the host top bar without any
special setup:

1. Copy and fill in the config:
   ```bash
   cp config.example.toml ~/.config/gnome-topbar/config.toml
   # set bm_username in the file
   ```
2. Export the Basic Memory env vars (or add to your shell profile):
   ```bash
   export BM_URL=...
   export BM_BEARER_TOKEN=...
   ```
3. Start the tray:
   ```bash
   cd packaging && make run
   ```
   A tray icon appears on the host top bar. The process inherits
   `DBUS_SESSION_BUS_ADDRESS` and `DISPLAY` from the sandbox environment.

### Normal host (permanent install via systemd)

Runs the same binary as a `systemd --user` service on your host machine.
No GNOME Shell extension to install or enable.

1. Copy and fill in the config (same as above).
2. Install and enable the service:
   ```bash
   cd packaging && make install-daemon enable
   ```
3. Watch logs:
   ```bash
   make logs
   ```

To stop: `systemctl --user stop gnome-topbar-daemon`.

## Currently-working-on note

Add to your AI assistant config (e.g. `~/.claude/CLAUDE.md`): when you start or
switch tasks, update the Basic Memory note `<username>/notes/currently-working-on/main`
with frontmatter `updated: <RFC3339 timestamp>` and a 1–3 sentence body. The
tray renders the body with a staleness indicator.

## Changelog

### v0.3.0
- Per-model weekly Claude usage: the Claude usage submenu now shows a row per model (incl. **Fable**), decoded from the producer's new `limits.weekly_models` (claude-stats schema 1.2); the legacy `seven_day_opus`/`seven_day_sonnet` are back-filled from it.
- New web detail pages opened from the tray: **📊 Stats details…** (`/ui/stats` — the full anti-tangent rollup: verdict mix, per-tool split, severity/category histograms, p50/p95, cache/partial, model usage, plus a CodeScene block or empty-state hint) and **🤖 Claude usage details…** (`/ui/claude` — per-account usage, 5h/weekly/per-model rate limits, and error/stale states). The tray dropdown stays lean; the top-bar icon and compact overview are unchanged.
- CodeScene stats now render the **verdict / quality-gate / problem-points** shape from anti-tangent **v0.11.0**'s redesigned `codescene` rollup block (`analyze_change_set` is categorical, not a 1-10 score): the tray line shows runs · gate · trend · reg/imp, and `/ui/stats` shows gates pass/fail, latest gate/trend, net-pp + p50, files analyzed, and the category histogram. (Replaces the old score/delta fields.)

### v0.2.2
- Dynamic top-bar icon: one vertical bar per Claude account, height ∝ its worst rate-limit window (5h vs weekly), colored green/amber/red by the same 60/80 thresholds as the menu (gray when stats are stale). Falls back to the static icon when no usage stats are present; clicking still opens the full usage panel.

### v0.2.0
- Basic Memory search across the knowledge base (epics, stories, gotchas, modules, features & decisions), rendered note view (mermaid + clickable inter-note links), and todo create — opened in the in-container browser.
- Browse pages: **Howtos**, **Gotchas**, **Modules**, **Features**, **Decisions**, and **My notes** (`/ui/howtos`, `/ui/gotchas`, `/ui/modules`, `/ui/features`, `/ui/decisions`, `/ui/notes`).
- Dark-themed UI with a sticky topbar (search + browse navigation) on every page.
- Mark a todo done by clicking its tray row.
- Refresh / Quit / Search / New-todo pinned to the top of the menu.

## Known sandbox gotcha — Chrome hijacks the default browser

The UI pages open in the **in-container Chrome** via `xdg-open`, which follows the
XDG default-browser setting. On a fresh container Chrome's first run registers
*itself* (`google-chrome.desktop`, which launches `/usr/bin/google-chrome-stable`
**without** `--no-sandbox`) as the default, replacing the container-safe
`chrome-sandbox.desktop` wrapper. The stock launcher then FATALs on namespace
creation in the unprivileged container, so the *first* page open works but every
later one silently fails (Chrome crashes instantly).

Workaround (per session):

```bash
xdg-settings set default-web-browser chrome-sandbox.desktop
```

Durable fix (claude-sandbox image): add `--no-default-browser-check --no-first-run`
to the `chrome-wrapper` `ARGS` so Chrome never re-registers itself, or point
`google-chrome.desktop`'s `Exec` at the wrapper.
