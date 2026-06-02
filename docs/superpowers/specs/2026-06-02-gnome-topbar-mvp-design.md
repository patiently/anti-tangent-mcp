# gnome-topbar — MVP design (v2: in-sandbox Go tray)

**Status:** approved design (brainstorming output). Component A (the Go daemon) is **implemented and reviewed** (plan Tasks 0–12). Component B is **revised** here from a host gnome-shell extension to an in-sandbox Go tray; pre-implementation.
**Date:** 2026-06-02
**Lives at:** `gnome-topbar/` (subdirectory of this repo, **separate Go module**)

> **Why v2.** The dev/runtime environment is a Docker sandbox that shares the **host's GNOME
> session bus** (`/tmp/dbus-…`) and **X11** (`/tmp/.X11-unix`), but the host's gnome-shell
> *extensions directory is not writable* from the sandbox. A gnome-shell extension can only
> run inside the host's shell, which we can't deploy to or iterate on from here. A
> **StatusNotifierItem tray** registered on the shared host bus, by contrast, runs entirely
> in the sandbox and projects onto the host's top bar — no host deploy, no network bridge,
> and we can run and *see* it live from this session. So Component B is now a Go tray, not a
> gjs extension. The daemon is unchanged.

> **Privacy note (this repo is PUBLIC).** Every committed file under `gnome-topbar/` uses
> **generic placeholders** (`<username>`, `<BM_URL>`, example PRs/todos). No real BM
> namespace, internal ticket content, private repo names, URLs, or tokens enter git history.
> Real values live only in `~/.config/gnome-topbar/` (never committed).

---

## 1. Summary

A single Go process that puts an icon in the GNOME top bar with a dropdown menu showing
**what needs attention now**, and raises native desktop notifications:

- **Currently working on** — a short summary the operator's AI assistant keeps fresh in a
  Basic Memory note.
- **GitHub PRs** — PRs the operator authored, and PRs where the operator is a requested
  reviewer (notifies on a new review request).
- **Basic Memory todos** — the rolling todo list, with "due today / overdue" surfaced and
  notified.
- **anti-tangent / CodeScene stats** — read-only, shown when the stats files exist.

The process has two parts in one binary: a **data layer** (the daemon — GitHub/BM polling,
parsing, snapshot, notification dedup) and a **tray UI** (`internal/tray`) that registers a
**StatusNotifierItem** on the host session bus and renders the snapshot as a menu. The tray
reads the snapshot **in-process**; no HTTP hop. A bearer-protected loopback HTTP API is
retained as an optional **debug/`curl` interface**, off the critical path.

### 1.1 Goals

- One glanceable top-bar surface for "what needs my attention right now," running entirely in
  the sandbox and integrating with the host via the shared session bus + X11.
- Native, clickable notifications for new review requests and due todos; clicking a PR opens
  it in the **host** browser.
- Robust: a flaky upstream call must never crash the process or spam notifications.

### 1.2 Non-goals (MVP)

- **Free-text Basic Memory search of epics/stories** — *deferred*. A tray `dbusmenu` cannot
  host a text-entry field, and the sandbox has no GUI dialog toolkit (zenity/kdialog/GTK).
  The `bm` search source + `/search` debug endpoint remain in the codebase but are **not
  surfaced in the tray MVP**. A later iteration can add a browse submenu or a GTK window.
- **Claude account usage / quota panel** — deferred (no official quota API).
- **Editing from the tray** — todos/notes are read-only (no tick-from-tray).
- **A host gnome-shell extension** — explicitly replaced by the tray (see "Why v2").
- **Multi-user / multi-machine sync.**

`anti-tangent + CodeScene stats` are **in scope** (read-only): the anti-tangent v0.10.0 stats
subsystem writes `rollup.json` (+ optional `summary.md`, + optional top-level `codescene`
object) to `ANTI_TANGENT_STATS_DIR`; the tray reads them when present (§3.5a) and shows them
as menu items, omitting the section entirely when absent.

---

## 2. Architecture

```
  ┌─ Docker sandbox (claude-sandbox) ───────────────────────────┐
  │  gnome-topbar  (one Go static binary)                       │
  │  ┌── data layer (daemon) ──────────┐  ┌── internal/tray ──┐ │
  │  │ GitHub poll (go-gh)             │  │ StatusNotifierItem │ │
  │  │ Basic Memory poll (MCP/SSE)     │  │ + com.canonical.   │ │
  │  │ currently-working-on note       │─▶│   dbusmenu         │ │
  │  │ snapshot + seen/ack dedup       │  │ (reads snapshot    │ │
  │  │ anti-tangent stats file read    │  │  in-process)       │ │
  │  └─────────────────────────────────┘  └─────────┬──────────┘ │
  │  (optional debug: loopback HTTP /state, bearer)  │            │
  └──────────────────────────────────────────────────┼───────────┘
                shared host session bus  ◀────────────┘  + X11
         org.kde.StatusNotifierWatcher (tray icon on host top bar)
         org.freedesktop.Notifications  (host notifications)
         org.freedesktop.portal.Desktop (OpenURI → host browser)
                              ▼
  ┌─ Host GNOME shell (top bar) ── 🛈 icon + dropdown menu ──────┐
  └─────────────────────────────────────────────────────────────┘
```

**Integration channel.** The sandbox mounts the host's session-bus socket
(`$DBUS_SESSION_BUS_ADDRESS`) and X11 socket. Verified live: `org.gnome.Shell`,
`org.freedesktop.Notifications`, `org.kde.StatusNotifierWatcher` (owner present → host
accepts tray icons), and `org.freedesktop.portal.Desktop` are all reachable, and a test
`notify-send` reached the host. So the tray registers on the host bus and appears on the host
top bar with **no host-side deployment and no network bridge**.

**Why one process.** The tray reads the daemon's snapshot directly (a method call on the
in-memory `Poller`), so there is no serialization or transport between UI and data. The
loopback HTTP server is kept for debugging only.

---

## 3. Component A — the Go daemon (data layer) — **implemented (Tasks 0–12)**

Already built, reviewed, and verified end-to-end against real services. Summarized here as
design-of-record; details live in the implementation and the plan.

`gnome-topbar/daemon` is its **own `go.mod`** (independent of the root anti-tangent module).
Packages: `config`, `mcphttp` (minimal MCP streamable-HTTP/SSE client), `bm` (todo +
now-working parsers, search), `github` (PRs via `go-gh`), `state` (snapshot, event
computation, seen/ack store), `server` (loopback HTTP), `cmd` (poll loops + wiring).

### 3.1 Config & secrets
From `~/.config/gnome-topbar/config.toml` overlaid by env (`BM_URL`, `BM_BEARER_TOKEN`).
Keys: `bm_username`, `bm_project`, `listen_port`, `stats_dir`, plus generated `api_token`
(for the debug HTTP). GitHub uses the `gh` CLI's own credentials via `go-gh`. Secrets never
logged.

### 3.2 Source — GitHub (PRs)
`go-gh` search: `is:open is:pr author:@me` and `…review-requested:@me`. Each PR →
`{repo, number, title, url, author, updated_at}`. (Verified: 12 authored + 2 review-requested
against the operator's real account.)

### 3.3 Source — Basic Memory todos
Reads `<username>/todo/main`; parses `## Active` open bullets, optional leading `[YYYY-MM-DD]`
due date; "due/overdue" = open & due ≤ today (injectable clock).

### 3.4 Source — Basic Memory search (epics/stories) — **dormant in MVP**
`SearchEpicsStories(query)` (note type read from `metadata.note_type`, verified live) and the
`/search` debug endpoint exist, but the **tray does not surface search** (§1.2). Kept for the
debug interface and a future iteration.

### 3.5 Source — "currently working on"
Reads `<username>/notes/currently-working-on/main`; returns `{ body, updated, has_updated }`.
**Fix (was Task-11 follow-up):** Basic Memory returns a missing note as a *successful*
`read_note` whose text is "Note Not Found…" guidance prose. The daemon must detect that
marker and treat it as **not-set-up** (empty body, `present:false`-style), so the tray shows
a "currently-working-on not set up" item rather than BM's guidance text. Staleness ("⟳ age")
is computed from `updated` when present.

### 3.5a Source — anti-tangent stats (optional, read-only) — **to implement**
Reads `rollup.json` (+ optional `summary.md`, + optional top-level `codescene` object) from
`stats_dir` (default `${XDG_STATE_HOME:-~/.local/state}/anti-tangent-mcp`). Absent
`rollup.json` → `present:false` (tray omits the section). The `rollup.json` key names are a
**cross-component contract** (snake_case) with the anti-tangent stats writer
(`2026-06-02-anti-tangent-stats-design.md` §3.3); CodeScene rides in the optional `codescene`
sub-object. **Not yet built** — the daemon MVP shipped Tasks 0–12 only; the `atstats` reader,
the `Snapshot.AntiTangent` field, and its refresh wiring are part of the remaining tray-phase
work (the original plan's Task 17, re-homed into the tray plan).

### 3.6 Snapshot + event/ack model
In-memory `Snapshot` refreshed by independent poll loops; a persisted seen/ack store
(`~/.local/state/gnome-topbar/seen.json`). `ComputeEvents` yields un-acked events (new
review-requested PRs + due todos) keyed by stable IDs (`pr:<repo>#<n>`, `todo:<date>:<hash>`).
**The tray** raises a notification per un-acked event and acks it via the store (in-process) —
at-least-once with explicit ack, restart-safe.

### 3.7 Debug HTTP API (loopback, bearer-protected) — **optional, off critical path**
`GET /state`, `GET /search?q=`, `POST /ack`, `GET /healthz`, all (except `/healthz`) behind
`Authorization: Bearer <api_token>`, bound `127.0.0.1`. Retained for `curl`-debugging and a
future external consumer; the tray does not use it.

### 3.8 Poll cadences
GitHub ~120 s; BM todos + now-working ~300 s + a morning sweep; anti-tangent stats ~300 s.
Sources poll independently; per-source errors captured into `snapshot.sources[*]`.

---

## 4. Component B — the Go tray (`internal/tray`) — **to implement**

A new package in the daemon module that registers a **StatusNotifierItem** (SNI) on the host
session bus and exposes a **`com.canonical.dbusmenu`** menu. Implemented in Go via
`github.com/godbus/dbus/v5` (verified fetchable) — **no GTK, no gjs**. The SNI/dbusmenu
protocol is either hand-rolled on godbus or via a pure-DBus Go SNI library (a spike picks the
lib vs hand-roll; the menu-model code is library-agnostic).

### 4.1 Tray icon
- A StatusNotifierItem with a **named theme icon** (e.g. a symbolic icon already in the host
  theme — no shipped pixmaps for MVP) and a tooltip showing the attention count
  (`review_requested + due_todo`). Status `Active`.
- Registered with `org.kde.StatusNotifierWatcher.RegisterStatusNotifierItem`. On watcher
  loss/regain (shell restart), re-register.

### 4.2 Menu (dbusmenu)
Rebuilt from the snapshot on a timer (~30 s) and on the menu's `AboutToShow`:

```
 🛠  Currently working on — <summary>            (disabled header; "⟳ <age>")
     └ "(not set up)" when the note is missing
 ──────────
 🔵 Review requested (N)
     <org>/<repo> #123  <title…>      → opens PR in host browser
 🟣 My open PRs (M)
     <org>/<repo> #789  <title…>      → opens PR in host browser
 ──────────
 ✅ Due / overdue (K)   ⚠ <todo text>
    Active (J)          <todo text>
 ──────────
 🛡 anti-tangent — <calls> · <pass%>/<warn%>/<fail%> · top <cat> · p95 <ms>   (when present)
    📊 CodeScene — score <s> (<trend>) · <reg>r/<imp>i                          (when present)
 ──────────
 ↻ Refresh        Quit
```

- **PR items** → `org.freedesktop.portal.Desktop.OpenURI.OpenURI` (opens the PR in the host's
  default browser; verified the portal frontend is on the bus). Fallback: GNOME's URL handler.
- **Todo / now-working / stats items** are display-only (read-only MVP). Due/overdue
  emphasized in the label (dbusmenu has no rich styling; use a `⚠` prefix).
- **No search item** (deferred, §1.2).
- **Refresh** triggers an immediate poll of all sources; **Quit** stops the process.
- The menu model is a **pure function** `snapshot → []MenuItem` (unit-testable), separate from
  the DBus export glue.

### 4.3 Notifications
For each un-acked event in the snapshot, raise a host notification via
`org.freedesktop.Notifications.Notify` (godbus):
- **review request** → actionable; the default action opens the PR via the portal.
- **due/overdue todo** → informational.
After raising, ack via the store (in-process). The tray also tracks raised IDs in-memory to
avoid re-raising before the ack settles.

### 4.4 Degraded states
- **A source errors** (`snapshot.sources[x].ok == false`) → that section shows a single
  "⚠ <source>: <reason>" item; other sections render normally.
- **StatusNotifierWatcher absent** (host has no tray support) → log a clear message and keep
  running (notifications still work); retry registration periodically.
- **Bus/portal unavailable** → the tray logs and degrades (no crash); the daemon keeps polling.

---

## 5. "Currently working on" wiring

A one-time instruction in the operator's assistant config (e.g. global `CLAUDE.md`): *when you
begin or switch tasks, update `<username>/notes/currently-working-on/main` — frontmatter
`updated:` to the current RFC3339 time, body = a 1–3 sentence summary (ticket/branch + next
step).* The daemon reads it (with the not-found handling of §3.5); the tray renders the body
with a "⟳ <age>" stamp, or "(not set up)" when missing. No daemon-side LLM call — the
assistant maintains the note.

---

## 6. End-to-end data flow

1. The process starts (in the sandbox; `DBUS_SESSION_BUS_ADDRESS` + `DISPLAY` inherited),
   loads config, starts the poll loops, and registers the SNI on the host bus.
2. Each loop refreshes its snapshot slice and recomputes `unacked_events` against the store.
3. The tray rebuilds its menu from the snapshot (timer + `AboutToShow`) and updates the
   tooltip/attention count.
4. For each `unacked_event`, the tray raises a host notification and acks via the store.
5. Clicking a PR calls the portal `OpenURI` → opens in the host browser. Refresh forces a poll.

---

## 7. Error handling

- **Source isolation + backoff** (daemon): one source failing never blanks another; per-source
  status carried in the snapshot.
- **Tray best-effort:** DBus/portal/notification failures are logged and never crash the
  process; the data layer keeps running.
- **No notification spam:** daemon-owned dedup (seen/ack store) + the tray's in-session raised
  set guarantee at-least-once-without-repeat across restarts.

---

## 8. Security & privacy posture

- Everything runs in the sandbox as the same user; the **host session bus** is the integration
  channel (already shared into the sandbox by the environment). No new cross-boundary secrets.
- The debug HTTP keeps **loopback + bearer token** (`0600` under `~/.config/gnome-topbar/`).
- BM token comes from the environment; secrets never logged, never committed.
- **Public-repo anonymization** (hard rule): committed files use placeholders only; real
  config lives in `~/.config/gnome-topbar/` and is gitignored.

---

## 9. Testing strategy

- **Data layer:** `go test -race ./...` (already green across all packages; no network in unit
  tests; httptest + fake MCP/REST doubles).
- **Tray menu model:** the pure `snapshot → []MenuItem` builder is unit-tested (sections,
  counts, due-emphasis, stats present/absent, now-working set/not-set) with no DBus.
- **SNI/DBus glue:** thin and verified **live on the host** — because the session bus is shared,
  we run the binary in the sandbox and confirm the icon + menu + notifications + open-in-browser
  on the real top bar (this is possible here, unlike a host-only gnome-shell extension).

---

## 10. Project layout

```
gnome-topbar/
  daemon/                       # OWN go.mod (separate module)
    cmd/gnome-topbar-daemon/main.go     # wires data layer + tray + (debug) HTTP
    internal/
      config/  mcphttp/  bm/  github/  state/  server/   # data layer (built, Tasks 0–12)
      atstats/                  # NEW: anti-tangent stats reader (not yet built)
      tray/                     # NEW: SNI + dbusmenu + notifications + portal (godbus)
  packaging/
    systemd/gnome-topbar-daemon.service
    Makefile
  config.example.toml           # placeholders only
  .gitignore
  README.md
```
(No `extension/` directory — Component B is Go now.)

---

## 11. Packaging, install & run

- **One binary** (`gnome-topbar-daemon`) runs the data layer + tray. Build via the Makefile to
  `~/.local/bin/`.
- **In the sandbox (dev + actual use here):** run the binary directly; it inherits
  `DBUS_SESSION_BUS_ADDRESS` + `DISPLAY` and registers on the host bus. A `Makefile run` target
  builds + runs it foreground for live iteration.
- **On a normal host (no sandbox):** the same binary runs as a `systemd --user` service
  (`gnome-topbar-daemon.service`, `EnvironmentFile=~/.config/gnome-topbar/env`); the unit is
  unchanged from the daemon MVP. (The service needs the user's session bus + display, which a
  `--user` unit has.)
- **README** documents config, the `gh`/BM prerequisites, the `currently-working-on` assistant
  instruction, and the sandbox-vs-host run modes.

---

## 12. Repo integration (CI / branch strategy)

`gnome-topbar/daemon` is a **separate Go module**; root build/test/goreleaser ignore it. All
`gnome-topbar/` work is on the plain **`feat/gnome-topbar`** feature branch, **exempt** from
the anti-tangent version/release flow (CI path-excludes `gnome-topbar/**`; already in place).
No anti-tangent release rides this work.

---

## 13. Forward-compat (deferred slices stay additive)

- **Free-text epic/story search** — deferred; add via a browse submenu or a GTK window later
  (the `bm` search source already exists).
- **Claude usage panel** — deferred (no quota API); a new source + menu section when solved.
- **anti-tangent + CodeScene stats** — in scope; tray renders from `rollup.json`.
- The snapshot is a registry of sources and the menu model is a pure function, so new sources =
  a new source module + a new menu section, no rework.

---

## 14. Acceptance (MVP "done")

- A tray icon appears on the host top bar (registered via StatusNotifierWatcher), with a
  tooltip/attention count = review-requested + due-todo.
- The menu shows: currently-working-on (with "⟳ age", or "(not set up)" when the note is
  missing — i.e. BM's "Note Not Found" prose is NOT shown), review-requested PRs, my open PRs,
  due/overdue + active todos, and the anti-tangent/CodeScene stats section when `rollup.json`
  is present (omitted when absent).
- Clicking a PR opens it in the **host** browser (via the portal).
- A **new** review request raises one host notification; a due todo raises one; neither
  re-fires after the process restarts.
- A single source failing shows only that section's error; others render. DBus/portal hiccups
  never crash the process.
- `go test -race ./...` passes in `gnome-topbar/daemon` (incl. the tray menu-model tests); no
  real personal data in any committed file.
- Verified **live on the host** from the sandbox (icon, menu, notification, open-in-browser).
