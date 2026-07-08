# Slim install: on-demand protocol via `anti-tangent-protocol` plugin — design

**Date:** 2026-07-08
**Repo:** `anti-tangent-mcp`
**Issue:** [#48](https://github.com/patiently/anti-tangent-mcp/issues/48)
**Version:** 0.11.1 (patch — docs + new plugin artifact; no server/Go code change)
**Status:** approved (brainstorm)

## Problem

The documented install wires the **entire** `INTEGRATION.md` into the user's
**global** agent config as an always-loaded import. `INTEGRATION.md` is ~40,000
bytes (~10k tokens), so every following-the-docs user pays that token cost on
**every API call, in every project** — including the large majority of sessions
that never touch a plan-based Goal/AC task.

- **Claude Code path** (`README.md` step 8): appends `@/…/.claude/anti-tangent.md`
  (a full mirror of `INTEGRATION.md`) under `## Active integrations` in global
  `~/.claude/CLAUDE.md`. Claude Code `@`-imports expand inline on every request.
- **opencode path** (`README.md` step 7): adds the same full file to opencode's
  top-level `instructions` array, which opencode also always-loads.

Both documented paths are "always-on, everywhere." The protocol is only relevant
when a task carries a `Goal / Acceptance criteria / Non-goals` header (§1); for
everything else the tokens are pure overhead.

## Goal

- Drop the always-loaded footprint from ~10k tokens to a **single
  skill-description line**. The full protocol loads **on demand**, only when a
  Goal/AC task actually appears.
- Keep `INTEGRATION.md` as the single source of truth for the protocol content.
- Cover both hosts: Claude Code (via a skill) and opencode (via a slim pointer).

## Non-goals

- No change to the MCP **server** — no Go code, no tool behavior, no config.
- The protocol **content** in `INTEGRATION.md` is untouched by this work.
- `INTEGRATION.md` stays under the existing 40,000-byte CI budget.
- No deterministic "`@`-import the whole file" fallback is documented — the
  slim/on-demand path fully replaces the old default (decision c).

## Design

### Component 1 — `plugin/anti-tangent-protocol/` (mirrors `plugin/bm-scribe/`)

A new marketplace plugin whose sole job is to make the protocol available
on demand in Claude Code.

```
plugin/anti-tangent-protocol/
  .claude-plugin/plugin.json          name: anti-tangent-protocol, version 0.1.0
  skills/anti-tangent-protocol/
    SKILL.md                          thin loader (below)
  INTEGRATION.md                      bundled copy — the on-demand payload
  README.md                           what it is, how to install, the trade-off
```

- **`SKILL.md` frontmatter `description`** — the *only* always-loaded text. Worded
  so the Goal/AC trigger is unambiguous, e.g.:

  > Use when you are about to implement, or dispatch a subagent to implement, a
  > task that has a Goal / Acceptance-criteria header from an implementation
  > plan. Loads the anti-tangent-mcp drift-protection protocol
  > (validate_task_spec → check_progress → validate_completion; validate_plan for
  > controllers).

- **`SKILL.md` body** — thin. Instruct the agent to `Read` the bundled
  `../../INTEGRATION.md` (relative path, mirroring how bm-scribe references
  `../../docs/three-step-pattern.md`), then follow the implementer clause (§4.2)
  or controller clause (§5) that applies to this task. The body carries no
  protocol content of its own — the bundled doc is the source.

- **Skill name:** `anti-tangent-protocol:anti-tangent-protocol` (single-skill
  plugin; `<plugin>:<skill>` per the bm-scribe convention). Skills are
  auto-discovered from `skills/*/SKILL.md`.

- **No plugin-level `CLAUDE.md`.** bm-scribe ships one, but a plugin's `CLAUDE.md`
  is loaded into context whenever the plugin is active — including it would
  reintroduce the exact always-loaded cost this work removes. The skill's
  `description` is the **sole** always-on surface; all other content is
  description-triggered or `Read` on demand.

- **`.claude-plugin/marketplace.json`** — add an `anti-tangent-protocol` entry
  alongside `bm-scribe` and bump the marketplace `version`.

### Component 2 — bundled-copy sync guard (CI)

The bundled `INTEGRATION.md` is a second on-disk copy that must never drift from
root. A new CI step makes that a hard invariant rather than manual discipline:

```bash
diff -q INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md
# fail with a clear message on any drift
```

Added as a step in the existing `.github/workflows/ci.yml` (near the
`INTEGRATION.md size budget` job). The resync one-liner
(`cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md`) is documented
in `CLAUDE.md` next to the INTEGRATION.md guidance so editors know to run it.

### Component 3 — opencode slim pointer

opencode has no skill mechanism, so it gets a hand-authored slim pointer instead
of the full file in `instructions`.

- New canonical artifact `examples/anti-tangent-pointer.md` (~1 screen). It states
  when the protocol applies (a Goal/AC task header) and instructs the agent to
  `Read` the full doc at the downloaded absolute path on demand.
- Install downloads the full `INTEGRATION.md` to
  `~/.config/opencode/anti-tangent.md` (**on disk, not in `instructions`**) and
  puts only `examples/anti-tangent-pointer.md`'s content — as
  `~/.config/opencode/anti-tangent-pointer.md` — into the `instructions` array.

### Component 4 — README rewrite

- **Claude Code one-shot prompt:** replace steps 7–8 (download 40KB mirror +
  `@`-import into `~/.claude/CLAUDE.md`) with a plugin install:
  `claude plugin marketplace add patiently/anti-tangent-mcp` (shared with the
  existing bm-scribe step) then
  `claude plugin install anti-tangent-protocol@anti-tangent-mcp`; verify with
  `claude plugin list`. The bm-scribe step is renumbered/deduped so the
  `marketplace add` isn't issued twice.
- **opencode one-shot prompt:** replace steps 6–7 with download-full-on-demand +
  slim-pointer-in-`instructions` per Component 3.
- Update the **manual install** prose, the **Integration** section pointer, and
  the "One-shot install" intro sentence (which currently says the agent will
  "download `INTEGRATION.md` … and wire it into your user-level instructions").

### Component 5 — CHANGELOG + version

- Add `## [0.11.1] - 2026-07-08` with:
  - `### Changed` — recommended install is now slim + on-demand (CC plugin;
    opencode slim pointer); the full `INTEGRATION.md` no longer loads on every
    call.
  - `### Added` — the `anti-tangent-protocol` companion plugin and the
    `examples/anti-tangent-pointer.md` opencode pointer.
- Branch `version/0.11.1`; do **not** bump `VERSION` on the branch (the release
  workflow auto-bumps). Merge commit to `main` carries the default patch bump.

## Trade-off (documented in the plugin README)

A skill body loads when the model judges its `description` relevant — slightly
less deterministic than an always-inlined block. That is the correct trade
against a flat ~10k-token tax on every call; the description's Goal/AC-header
wording is written to make the trigger fire reliably. Per decision (c), no
always-inline fallback is documented.

## Verification

- `go build ./...` / `go test ./...` unaffected (no Go change) — run to confirm.
- New CI drift guard passes when the bundled copy is identical; fails on a
  deliberate one-byte edit (verified locally with `diff -q`).
- `INTEGRATION.md` still `< 40000` bytes (existing budget job).
- Manual read-through: the CC prompt installs the plugin and never writes a
  40KB file into `~/.claude/`; the opencode prompt puts only the slim pointer in
  `instructions`.
- Plugin shape matches bm-scribe: `plugin.json` parses, `SKILL.md` frontmatter
  has `name` + `description`, relative `../../INTEGRATION.md` resolves from the
  skill dir.

## File manifest

**New**
- `plugin/anti-tangent-protocol/.claude-plugin/plugin.json`
- `plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md`
- `plugin/anti-tangent-protocol/INTEGRATION.md` (bundled copy of root)
- `plugin/anti-tangent-protocol/README.md`
- `examples/anti-tangent-pointer.md`

**Modified**
- `.claude-plugin/marketplace.json` (add plugin entry + version bump)
- `.github/workflows/ci.yml` (bundled-copy drift guard)
- `README.md` (CC + opencode prompts, manual steps, Integration pointer, intro)
- `CLAUDE.md` (resync note next to INTEGRATION.md guidance)
- `CHANGELOG.md` (`## [0.11.1]`)
