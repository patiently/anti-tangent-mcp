# gnome-topbar — MVP design

**Status:** approved design (brainstorming output), pre-implementation
**Date:** 2026-06-02
**Lives at:** `gnome-topbar/` (subdirectory of this repo, **separate Go module**)

> **Privacy note (this repo is PUBLIC).** Every committed file under `gnome-topbar/` —
> code, docs, examples, and this spec — uses **generic placeholders** (`<username>`,
> `<BM_URL>`, example PRs/todos). No real BM namespace, no internal ticket content, no
> private repo names enter git history. The operator's real values live only in
> `~/.config/gnome-topbar/` (never committed). This spec follows that rule throughout.

---

## 1. Summary

A GNOME Shell extension that adds a button to the top bar. Opening it shows a dropdown
with three things at a glance. The panel also raises native desktop notifications (events
computed and deduplicated by the daemon; the panel renders them and acks) for two of them:

- **Currently working on** — a short summary the operator's AI coding assistant keeps
  fresh in a Basic Memory note.
- **GitHub PRs** — PRs the operator authored, and PRs where the operator is a requested
  reviewer. Notifies when a new review is requested.
- **Basic Memory todos** — the operator's rolling todo list, with "due today / overdue"
  surfaced and notified; plus a search box over Basic Memory epics/stories.

All data fetching, parsing, auth, and notification dedup happen in a **Go daemon** running
as a `systemd --user` service. The GNOME extension (gjs/JavaScript) is a thin client that
polls the daemon and renders. This keeps heavy, fallible I/O out of the compositor process.

### 1.1 Goals

- One glanceable top-bar surface for "what needs my attention right now."
- Native, clickable GNOME notifications for new review requests and due todos.
- Robust: a flaky network call or upstream outage must never destabilize gnome-shell.
- Cleanly extensible to the deferred panels (§13) without rework.

### 1.2 Non-goals (MVP)

- **Claude account usage / quota panel** — deferred. No official API exposes subscription
  quota/reset; needs its own research slice.
- **anti-tangent + CodeScene statistics panel** — *in scope* (read-only, optional). The
  anti-tangent v0.10.0 stats subsystem writes `rollup.json` + `summary.md` to
  `ANTI_TANGENT_STATS_DIR`, and it **aggregates CodeScene runs into the same `rollup.json`**
  under an optional top-level `codescene` object. This extension reads those files when
  present (see §3.5a) and renders an anti-tangent section plus a CodeScene sub-block. The
  panel never *produces* either dataset — it only surfaces what that subsystem wrote, and
  reads exactly one file (`rollup.json`), never anti-tangent's raw `*-events.jsonl`.
- **Editing from the panel** — todos and notes are read-only in MVP (no tick-from-panel,
  no note editing). Cleanly addable later.
- **Multi-user / multi-machine sync.** Single desktop, single user.

---

## 2. Architecture

```
┌─ gnome-shell (compositor process) ─────────┐        ┌─ systemd --user service ──────────┐
│  extension/  (gjs / JavaScript)            │  HTTP  │  daemon/  (Go static binary)      │
│  • top-bar button + badge count            │ ─────► │  • GitHub poll  (PRs)             │
│  • dropdown menu (4 sections)              │ /state │  • Basic Memory poll (todos)      │
│  • polls daemon on a GLib timer (~45s)     │ ◄───── │  • Basic Memory search proxy      │
│  • raises GNOME notifications + POST /ack   │  JSON  │  • reads currently-working-on note│
│  • render-only; ~no business logic         │        │  • computes "new" events + dedup  │
└─────────────────────────────────────────────┘        └───────────────────────────────────┘
        client: 127.0.0.1:<port> + bearer token (loopback only)
```

**Why two components.** A GNOME Shell extension runs *inside* gnome-shell; an uncaught
error or a blocking call there degrades the whole desktop. Polling external APIs and
speaking the Basic Memory MCP transport is exactly the kind of fallible, latency-prone work
that must not live there. The Go daemon isolates it, is unit-testable, and matches the
existing Go stack in this repo.

**Transport.** The daemon listens on `127.0.0.1:<fixed port>` and requires a per-install
**bearer token** (generated at first run, stored `0600` in `~/.config/gnome-topbar/`). The
extension sends that token on every request. Loopback + token keeps the gjs HTTP client
trivial while bounding access to same-user processes that can read the config file. A Unix
domain socket is the documented hardening alternative (§11, open questions).

---

## 3. Component A — the Go daemon

Module path (placeholder): `github.com/<owner>/anti-tangent-mcp/gnome-topbar/daemon` — but it
is its **own `go.mod`**, independent of the root anti-tangent module, so its dependencies
(`go-gh`, an MCP client) never enter the root module and root `go test ./...` / goreleaser
ignore it.

### 3.1 Config & secrets

Read at startup from `~/.config/gnome-topbar/config.toml` (and/or environment):

| Key | Meaning | Source |
|---|---|---|
| `bm_url` | Basic Memory MCP base URL | operator (`<BM_URL>`) |
| `bm_bearer_token` | Basic Memory auth | operator |
| `bm_username` | namespace prefix for personal notes (e.g. `<username>`) | operator |
| `bm_project` | Basic Memory project name (e.g. `main`) | operator |
| `github` | uses `go-gh` → reuses the `gh` CLI's own stored credentials | `gh` |
| `listen_port` | loopback port for the local API | config (default fixed) |
| `api_token` | bearer the extension must present | auto-generated on first run |

Secrets never appear in logs. The `systemd --user` unit references the config via
`EnvironmentFile=` for any env-shaped values.

### 3.2 Source — GitHub (PRs)

- Client: **`go-gh`** (`github.com/cli/go-gh`), which authenticates with the same
  credentials as the operator's `gh` CLI — no token handling, pagination, or rate-limit code
  to reimplement.
- Queries (GitHub search):
  - **Authored:** `is:open is:pr author:@me`
  - **Review-requested:** `is:open is:pr review-requested:@me`
- Each PR row carries: repo `owner/name`, number, title, `html_url`, author, updated-at, and
  (for review-requested) requested-at if available.
- **Rate limits** respected via `go-gh` / response headers; on limit, back off and mark the
  source `stale` rather than hammering.
- **Caveat to verify early:** the operator's active GitHub token may be a **fine-grained
  PAT** scoped to specific repos/orgs; PRs outside that scope will not appear. First
  implementation task validates real visibility against expectations.

### 3.3 Source — Basic Memory todos

- Reads the rolling todo note at permalink `<username>/todo/main` via the MCP client
  (`read_note`).
- **Parsing rules** (the note's existing convention):
  - Bullets live under a `## Active` heading; completed items under `## Done`.
  - An item is `- [ ] ...` (open) or `- [x] ...` (done).
  - An optional leading `[YYYY-MM-DD]` token is the **due / target date**.
  - "Due today / overdue" = an **open** item whose due date is **≤ today** (local date).
  - Items with no date are listed under "Active" but never fire a due notification.
- Output: `{ active: [...], due: [...] }`, each item `{ text, due_date?, overdue: bool }`.

### 3.4 Source — Basic Memory search (epics/stories)

- Proxies `search_notes` with `note_types: ["epic","story"]` (the project-knowledge types),
  scoped to the knowledge project. Returns `{ title, type, permalink, snippet }[]`.
- Exposed as a request-driven endpoint (not polled): the panel calls it on user input.

### 3.5 Source — "currently working on"

- Reads the note at `<username>/notes/currently-working-on/main` (`read_note`).
- Returns `{ body, updated_at }`. `updated_at` comes from the note's frontmatter/modified
  time so the panel can show staleness ("⟳ 2m ago").
- If the note does not exist yet, returns an empty body + a one-line hint; the panel shows a
  "not set up yet" affordance rather than an error.

### 3.5a Source — anti-tangent stats (optional, read-only, if present)

Surfaces the anti-tangent v0.10.0 stats subsystem's output. Pure local file reads — no MCP,
no LLM, no network.

- **Locating it:** a `stats_dir` key in gnome-topbar's `config.toml`, default
  `${XDG_STATE_HOME:-~/.local/state}/anti-tangent-mcp` (the same default the anti-tangent stats
  spec suggests). The daemon is a separate process from anti-tangent, so it does **not** inherit
  `ANTI_TANGENT_STATS_DIR`; the operator points `stats_dir` at the same directory.
- **Reads two files** on a modest cadence (~300 s; the files only change on anti-tangent
  compaction, ~24 h / 50 events):
  - `rollup.json` — deterministic aggregates → a few headline figures: total calls, pass/warn/fail
    %, top finding category, p95 `review_ms`, and `generated_at`. It may also carry an optional
    top-level `codescene` object (anti-tangent aggregates CodeScene runs into it): runs,
    latest score/delta/trend, p50 score, regression/improvement counts, category histogram.
    Absence of the `codescene` key = "no CodeScene data this window," not an error.
  - `summary.md` — the latest LLM performance narrative (rendered truncated, expandable).
- **Graceful absence ("if they exist"):** if `rollup.json` is missing, the source reports
  `present: false` and the panel **omits the section entirely** — no error, no empty box.
- **rollup.json schema contract:** the field names this reader expects must match what the
  anti-tangent stats writer emits (`docs/superpowers/specs/2026-06-02-anti-tangent-stats-design.md`
  §3.3). The exact JSON keys are pinned in the implementation plan's reader task; this is a
  cross-component contract between the two repos/branches.

### 3.6 State aggregation + event/ack model

- The daemon maintains an in-memory **snapshot** refreshed by the poll loops, plus a small
  **persisted store** at `~/.local/state/gnome-topbar/seen.json` holding:
  - the set of review-request identities already notified, and
  - the set of `(todo-text-hash, due-date)` pairs already notified.
- After each poll it computes **new events** (review requests / due todos not in the seen
  store) and stamps them `unacked`.
- `/state` returns the full snapshot **plus** `unacked_events`. The panel raises a GNOME
  notification per event and calls `POST /ack {event_ids}`; the daemon then records them as
  seen. This is at-least-once with explicit ack, so neither a daemon restart nor a panel
  restart re-notifies, and a missed delivery is retried on the next poll.

### 3.7 HTTP API (loopback, bearer-protected)

| Method/Path | Purpose | Response |
|---|---|---|
| `GET /state` | aggregated snapshot | `{ now_working, prs:{authored,review_requested}, todos:{active,due}, sources:{<name>:{ok,error,stale_since}}, unacked_events:[...], generated_at }` |
| `GET /search?q=&types=epic,story` | BM search proxy | `{ results:[{title,type,permalink,snippet}] }` |
| `POST /ack` | mark events notified | `{ acked: n }` |
| `GET /healthz` | liveness | `200 ok` |

All endpoints require `Authorization: Bearer <api_token>`. Bind to `127.0.0.1` only.

### 3.8 Poll cadences

- GitHub: **~120 s**.
- BM todos + currently-working-on: **~300 s**, plus a **morning sweep** (first poll after a
  configurable local time, e.g. 08:00) so "due today" fires early in the day.
- Cadences are config-overridable. Each source polls independently; one source being slow or
  failing never blocks another.

---

## 4. Component B — the gjs extension

`extension/` is a standard GNOME Shell extension (`metadata.json`, `extension.js`,
`stylesheet.css`, optional `prefs.js`). It targets the GNOME Shell version on the operator's
machine (confirmed during implementation; `metadata.json` `shell-version` set accordingly).

### 4.1 Top-bar button + badge

- A `PanelMenu.Button` with an icon and a small **badge** showing
  `review_requested_count + due_todo_count`, so attention-worthy state is visible without
  opening the menu. Badge hidden when zero.

### 4.2 Dropdown menu (mock — generic data)

```
 ┌────────────────────────────────────────────┐
 │ 🛠  Currently working on            ⟳ 2m ago│
 │   <one-to-three sentence summary the        │
 │    assistant keeps fresh>                    │
 ├────────────────────────────────────────────┤
 │ 🔵 Review requested (2)                     │
 │   <org>/<repo> #123  <title…>           ↗  │
 │   <org>/<repo> #456  <title…>           ↗  │
 │ 🟣 My open PRs (3)                          │
 │   <org>/<repo> #789  <title…>           ↗  │
 ├────────────────────────────────────────────┤
 │ ✅ Todos · due/overdue (1)                  │
 │   ⚠ [MM-DD] <todo text…>                    │
 │   Active (3)  <todo text…>                  │
 ├────────────────────────────────────────────┤
 │ 🔍 [ search epics / stories…            ]   │
 │   <type> <title…>   (click → expand snippet)│
 └────────────────────────────────────────────┘
```

- **PR rows** open `html_url` in the default browser on click
  (`Gio.AppInfo.launch_default_for_uri`).
- **Todo rows** read-only in MVP; due/overdue rows visually emphasized.
- **Search**: a `St.Entry` in the menu; on debounced input the panel calls `GET /search`,
  renders results inline. Click expands the snippet and copies the `memory://<permalink>` to
  the clipboard (Basic Memory has no clean per-note web URL).

### 4.3 Polling & rendering

- A `GLib.timeout_add_seconds` loop polls `GET /state` every **~45 s**, and an extra
  immediate poll fires when the menu opens. The timer runs regardless of menu visibility (so
  notifications fire while the menu is closed).
- HTTP via `libsoup` (`Soup.Session`) to `http://127.0.0.1:<port>` with the bearer header.
- Rendering is idempotent: each poll rebuilds the section contents from the snapshot.

### 4.4 Notifications

- For each `unacked_event` in `/state`, raise a native notification via a
  `MessageTray.Source` notification:
  - **review request** → clickable, default action opens the PR `html_url`.
  - **due/overdue todo** → informational, body is the todo text.
- After raising, `POST /ack` with the event ids. Dedup is the daemon's responsibility
  (§3.6), so the panel never tracks "seen" itself.

### 4.5 Degraded states (panel side)

- **Daemon unreachable** → menu shows a single row: "daemon offline —
  `systemctl --user start gnome-topbar-daemon`". No errors thrown.
- **Per-source error** (from `sources` map) → that section is dimmed with a tooltip carrying
  the reason ("GitHub: not authenticated", "Basic Memory: timeout"); other sections render
  normally.

---

## 5. "Currently working on" wiring

A one-time instruction is added to the operator's assistant config (e.g. the global
`CLAUDE.md`): *when you begin or switch tasks, update the Basic Memory note
`<username>/notes/currently-working-on/main` with a 1–3 sentence summary — ticket/branch and
the immediate next step.* The daemon reads that note; the panel renders its body with a
"⟳ <age>" staleness indicator computed from the note's `updated_at`.

This deliberately avoids any daemon-side LLM call: the assistant already holds the
subscription and BM access, so summary generation stays where the context already is. It is
best-effort (depends on the assistant honoring the instruction); the visible staleness stamp
makes a stale summary obvious.

---

## 6. End-to-end data flow

1. `systemd --user` starts the daemon; it loads config, generates the api token on first
   run, and starts independent poll loops.
2. Each loop refreshes its slice of the in-memory snapshot and recomputes `unacked_events`
   against `seen.json`.
3. The extension polls `GET /state` (~45 s and on menu-open), updates the badge, and
   rebuilds the menu sections.
4. For any `unacked_events`, the extension raises native notifications and `POST /ack`s.
5. On search input, the extension calls `GET /search` and renders results inline.

---

## 7. Error handling

- **Source isolation**: GitHub and Basic Memory poll and fail independently; one failure
  never blanks another section. `/state.sources[name]` carries `{ok,error,stale_since}`.
- **Backoff**: failed polls retry with exponential backoff; GitHub rate limits respected.
- **Auth failures** surface as per-source errors, not crashes.
- **Daemon down**: panel degrades to a single hint row (§4.5).
- **Stale data**: the panel shows last-known values with a staleness indicator rather than
  blanking, so a transient outage doesn't erase the view.

---

## 8. Security & privacy posture

- **Loopback + bearer token**; daemon binds `127.0.0.1` only.
- **Secrets**: BM token + api token stored `0600` under `~/.config/gnome-topbar/`; never
  logged; never committed.
- **Public-repo anonymization** (hard rule): all committed files use placeholders; the
  operator's real namespace, URL, repos, and todo content live only in local config and are
  `.gitignore`d. The `gnome-topbar/.gitignore` excludes any sample config that contains real
  values.

---

## 9. Testing strategy

- **Daemon (primary coverage)**: `go test -race ./...` inside the `gnome-topbar/daemon`
  module, no network in unit tests.
  - GitHub source: `httptest` server returning canned search payloads (incl. rate-limit
    headers, auth failure).
  - BM source: a fake MCP endpoint (httptest speaking the MCP JSON-RPC shape) returning
    canned `read_note` / `search_notes` responses.
  - **Todo parser**: table tests for the `[YYYY-MM-DD]` + `- [ ]/[x]` + `## Active/## Done`
    grammar and the due-today/overdue boundary (incl. no-date items, malformed dates).
  - **Ack store**: restart-safe dedup, at-least-once-with-ack semantics.
- **Extension**: logic is kept minimal in gjs; verified via a manual checklist and a nested
  shell (`dbus-run-session -- gnome-shell --nested`) on the operator's machine. **This dev
  sandbox has no GNOME installed**, so gjs verification runs on the operator's machine,
  driven interactively during implementation.

---

## 10. Project layout

```
gnome-topbar/
  daemon/                       # OWN go.mod (separate module)
    cmd/gnome-topbar-daemon/main.go
    internal/
      config/                   # config.toml + token bootstrap
      github/                   # go-gh PR fetch + row mapping
      bm/                       # MCP client + todo parser + search + note read
      state/                    # snapshot aggregation + seen/ack store
      server/                   # loopback HTTP/JSON + bearer auth
  extension/
    metadata.json
    extension.js
    stylesheet.css
    prefs.js                    # optional: port/cadence settings
  packaging/
    systemd/gnome-topbar-daemon.service   # user unit
    Makefile                    # build, install, dev-loop targets
  config.example.toml           # placeholders only
  .gitignore
  README.md
```

---

## 11. Packaging, install & dev loop

- **Daemon**: `go build` → static binary; installed to `~/.local/bin/`. A `systemd --user`
  unit (`gnome-topbar-daemon.service`) with `EnvironmentFile=~/.config/gnome-topbar/env`
  runs it; `systemctl --user enable --now`.
- **Extension**: `make install` symlinks/copies `extension/` into
  `~/.local/share/gnome-shell/extensions/gnome-topbar@<owner>/`; enable with
  `gnome-extensions enable`. Iterate in a nested shell.
- **README** documents first-run config (`config.example.toml` → `~/.config/gnome-topbar/`),
  the `gh` auth prerequisite, the BM URL/token, and the `currently-working-on` assistant
  instruction.

### Open questions / hardening (non-blocking)

- **Unix socket vs loopback TCP**: loopback+token chosen for MVP simplicity; switching to a
  `0600` Unix socket is a self-contained hardening change behind the same client interface.
- **Fine-grained PAT scope** (§3.2): confirm real PR visibility before building UI around it.
- **GNOME Shell version**: pin `metadata.json` `shell-version` to the operator's actual
  version during implementation.

---

## 12. Repo integration (CI / branch strategy)

- `gnome-topbar/daemon` is a **separate Go module**: root `go build ./...` /
  `go test ./...` / goreleaser do not descend into it, so the anti-tangent binary and its
  release are unaffected.
- The repo enforces a `version/X.Y.Z` branch ↔ `CHANGELOG.md` entry and a release flow for
  the **anti-tangent binary**. Adding `gnome-topbar/` is **not** an anti-tangent release.
  **Decision:** all `gnome-topbar/` work happens on a plain **`feat/gnome-topbar`** feature
  branch (not a `version/X.Y.Z` branch), **exempt from the version/release flow**. The
  **first plan task** adjusts CI so the anti-tangent release lane — the branch↔CHANGELOG
  check and goreleaser — **path-excludes `gnome-topbar/**`**, so this subdir never rides the
  binary's semver/release train. This task gates how the work is committed and must land
  before any daemon/extension code.

---

## 13. Forward-compat (deferred slices stay additive)

`/state` is a **registry of sources**. Each future panel = one new source module in the
daemon + one new menu section in the extension, with no change to existing sources:

- **Claude usage panel** — a source reading `~/.claude` and `~/.claude-alt` token-accounting
  logs (the second account's config home is confirmed present). Subscription quota/reset
  remains an open research item.
The **anti-tangent + CodeScene stats panel** is no longer deferred: the data source (the
v0.10.0 stats subsystem) is being built in parallel and aggregates CodeScene into the same
`rollup.json`, so this extension reads `rollup.json`/`summary.md` directly (§3.5a). Only the
**Claude usage panel** stays out of MVP by decision.

---

## 14. Acceptance (MVP "done")

- Top-bar button shows a badge = review-requested + due-todo counts.
- Dropdown renders: currently-working-on (with staleness), review-requested PRs, my open
  PRs, due/overdue + active todos, and a working epic/story search box.
- Clicking a PR opens it in the browser.
- When `stats_dir` contains `rollup.json`, an "anti-tangent" section shows headline numbers +
  the latest summary with an `as of <generated_at>` stamp; when those files are **absent**, the
  section is omitted entirely (no error). When `rollup.json` carries a `codescene` object, a
  CodeScene sub-block (score/delta/trend/regression-improvement counts) appears; its absence
  drops only the sub-block.
- A **new** review request raises one clickable notification (opens the PR); a todo coming
  due raises one notification; neither re-fires after daemon or panel restart.
- Killing the daemon degrades the panel to an "offline" hint with no shell instability; a
  single source failing dims only its section.
- `go test -race ./...` passes in `gnome-topbar/daemon`; no real personal data in any
  committed file.
