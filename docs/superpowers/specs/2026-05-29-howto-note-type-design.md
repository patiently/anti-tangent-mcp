# Howto note type — design spec

**Status:** proposed
**Version target:** v0.9.0
**Authors:** Patrick Gilmore

## 1. Overview

A "howto" is a project-and-module-scoped **operational procedure** — a runbook the team should *follow* rather than rediscover (deploy a release, run a migration, set up a local environment, cut a release). This spec adds howtos as an **eighth project-knowledge note type** alongside the existing seven (`decision`, `module`, `feature`, `glossary`, `epic`, `story`, `gotcha`), introduces one new bm-scribe creator skill to capture them, and relies entirely on the existing `prime_project_knowledge` loop to surface them on future plans.

`howto` is the **durable-reference** counterpart to `gotcha` (lessons-learned): a `gotcha` records *what bit us*; a `howto` records *the correct procedure*. It joins the **durable** group with `decision` / `module` / `feature` / `glossary`.

Intake path:

- **Post-plan** — anti-tangent's `extract_project_knowledge` proposes `howto`-typed proposals from completion envelopes (`action: create` for a new procedure, `action: update` when an envelope shows an existing howto's steps changed). The user curates them via the new skill.

Unlike `gotcha`, there is **no post-review mining path** — procedures are not typically buried in PR comments, so the `create-howto` skill stays as simple as `create-module`.

## 2. Goals

- **Capture repeatable procedure at the moment it's freshest** — at end-of-plan, when the implementer has just performed (or established) the procedure.
- **Re-use existing prime/validate_plan plumbing.** Howtos stored as BM notes with `modules: [...]` frontmatter are findable by the existing `kb_index`/picks loop. No reviewer-prompt change in prime; no new MCP tool.
- **Preserve the cross-model-review property.** anti-tangent's extract reviewer (a different model from the implementer) does the mining for the post-plan path.
- **One discoverable command.** A single `/bm-scribe:create-howto` skill captures the procedure via the three-step BM v0.21.1 pattern.

## 3. Non-goals

- **No new MCP tool.** No `mine_howtos` endpoint.
- **No post-review mining path.** Unlike `create-gotcha`, `create-howto` has no `--from-review` mode.
- **No supersede / ADR numbering.** Howtos are slug-keyed living documents updated in place; a revised procedure edits the existing note rather than spawning a numbered successor. (`status: deprecated` retires a dead procedure.)
- **No automatic invocation.** The skill is user-triggered like every other bm-scribe creator skill.
- **No status filtering in `prime_project_knowledge`.** Whether `deprecated` howtos rank lower is handled by the controller's `kb_index` search + prime's natural-language ranking, not by a code-level filter.
- **No persistent state in the skill.** Session-scoped only, same as every bm-scribe skill.

## 4. Architecture & component boundaries

Two repos, three components, one new wire — identical topology to the v0.8.0 gotcha addition.

```text
┌───────────────────── anti-tangent-mcp (Go) ──────────────────────┐
│  internal/verdict/extract.go                                     │
│    + ProposalTypeHowto                        (new enum value)   │
│  internal/verdict/extract_parser.go                              │
│    + ProposalTypeHowto in the validation switch (l.82)           │
│  internal/verdict/extract_schema.json                            │
│    + "howto" in the proposals[].type enum                        │
│  internal/prompts/templates/extract.tmpl                         │
│    + "howto" in the durable-knowledge + type lists               │
│    + new "3a-howto" guidance block (create/update; no supersede) │
│  internal/prompts/testdata/extract_basic.golden                  │
│  internal/prompts/testdata/extract_milestone.golden              │
│    + regenerated with `-update`                                  │
│                                                                  │
│  internal/verdict/prime.go        (NO change)                    │
│  internal/prompts/templates/prime.tmpl  (NO change)              │
│  internal/verdict/schema_invariants_test.go  (NO change —        │
│    it asserts category-enum lockstep, not the type enum)         │
└──────────────────────────────────────────────────────────────────┘
                   │   ExtractResult.proposals[type=howto]
                   ▼
┌──────────────────── plugin/bm-scribe ───────────────────────────┐
│  skills/create-howto/SKILL.md        (NEW)                       │
│    Intake: interactive, optionally pre-filled from a recent      │
│            extract envelope's type=howto proposals               │
│    Creator: three-step BM v0.21.1 pattern, slug key              │
│    Update:  action=update edits ## Steps in place                │
│  README.md       (subcommand table + skill count 13→14)          │
│  .claude-plugin/plugin.json / package.json / gemini-extension.json│
│    + version 0.2.0 → 0.3.0                                        │
└──────────────────────────────────────────────────────────────────┘
```

**Anti-tangent's responsibility** ends at producing `Proposal{type: howto, ...}` records in the existing extract envelope. It does not know about the skill.

**bm-scribe's responsibility** is the entire user-facing flow: gather/confirm the procedure, apply the three-step BM pattern, and update an existing howto in place when one already exists.

**Prime (the read side)** needs no code change. Once howtos exist as notes with `modules: [...]` frontmatter, the existing `prime_project_knowledge` loop finds them via `kb_index` like any other note type.

## 5. Data flow

### 5.1 Post-plan (the only invocation path)

```text
[implementer DONE]
       │
       ▼
controller calls extract_project_knowledge(envelopes, kb_index, epic_permalink)
       │
       ▼
ExtractResult.proposals[] includes Proposal{type: "howto", action: "create" | "update", ...}
       │
       ▼
controller presents proposals to user (existing flow); user runs:
       /bm-scribe:create-howto
       │
       ▼
skill reads the most recent extract envelope from conversation context
(or gathers the procedure interactively if none present)
       │
       ▼
for each Proposal{type: howto}:
   • show user: title, modules, proposed When-to-use / Steps / Verification
   • user: accept / edit / skip
   • on action=create → three-step BM pattern (slug key)
   • on action=update → edit_note against the existing note's ## Steps
```

## 6. Note shape

### 6.1 Permalink (slug-keyed, mirrors `module` / `feature`)

```text
<PROJECT>/howtos/<slug>/main
```

`<slug>` follows the same kebab-case derivation rule as `create-module`'s slug (lowercased title with non-alphanumerics collapsed to hyphens). **No ADR number** — howtos are living documents, not a supersede chain.

### 6.2 Frontmatter

```yaml
---
title: Deploy a release
permalink: yc/howtos/deploy-release/main
type: howto
status: active            # enum: active | deprecated
modules: [release, ci]    # one or more module slugs this procedure touches
last_verified: 2026-05-29 # YYYY-MM-DD — when the steps were last confirmed to work
origin: yc/stories/YC-4521/main      # optional but recommended
tags: []
---
```

`status` is `active` for a live procedure, `deprecated` for one that no longer applies. There is **no `supersedes`, no `severity`, no `discovered_at`** — those are gotcha-specific. `last_verified` is the freshness signal: runbooks rot, and a reader should know when the steps were last confirmed.

### 6.3 Body sections

```markdown
## When to use
<the trigger / what this procedure accomplishes — when a reader should reach for it>

## Prerequisites
<optional — what must be true / installed / accessible before starting>

## Steps
1. <step>
2. <step>
<the load-bearing section — prime excerpts this into the next plan's project_knowledge>

## Verification
<how to confirm the procedure worked>

## Rollback / if it goes wrong
<optional — how to undo or recover>

## Related
- [[<wikilink to module note>]]
- [[<wikilink to relevant decision / origin story>]]
```

The `## Steps` section is the load-bearing one — that's what prime excerpts into `project_knowledge` for the next plan, and what `validate_plan`'s reviewer reads when a plan re-implements a procedure that already has a runbook. `## When to use` and `## Steps` are required; `## Prerequisites`, `## Verification`, `## Rollback / if it goes wrong`, and `## Related` are optional (`## Verification` is recommended).

### 6.4 Update mechanics (no supersede)

A revised procedure is an **in-place edit**, not a new note:

1. **Update an existing howto** — `edit_note(operation="replace_section")` against the `## Steps` section (or the relevant section), and `edit_note(operation="find_replace")` to bump the `last_verified:` frontmatter line. No `move_note`, no permalink rewrite — the note already lives at the canonical permalink.
2. **Retire a howto** — `edit_note(find_replace)` flipping `status: active` → `status: deprecated`.

extract expresses an in-place edit as `Proposal{action: "update", ...}` carrying a `body_patch` (the changed section) rather than a full `body`. In Basic Memory mode this maps to one or more `edit_note` `bm_commands`; in non-BM mode `bm_commands` stays `[]` and the `proposals[]` entry carries the semantic payload.

## 7. Prime integration (the read side)

No code change in anti-tangent. The existing prime loop works once notes exist.

### 7.1 How a howto reaches the next plan

```text
new plan touches module `release`
       │
       ▼
controller's pre-dispatch step:
   • build kb_index from BM search:
       search_notes(query="release")  →  includes howtos/deploy-release
   • call prime_project_knowledge(task_fields, kb_index, epic_permalink)
       │
       ▼
prime's reviewer LLM ranks picks; howtos with modules:[release]
score high because the task names that module
       │
       ▼
controller reads picked howto notes → kb_excerpts string
       │
       ▼
kb_excerpts passed into validate_plan(project_knowledge=...)
and dispatched to the implementer as auto-attached "Project knowledge"
```

### 7.2 Encoding howto metadata into kb_index `tags`

Reuse the generic `tags` encoding the gotcha spec introduced — no `KBIndexEntryArg` schema change:

- `status:active` or `status:deprecated` — one entry per howto.
- `module:<slug>` — one entry per module in the howto's frontmatter `modules:` array.

Example `tags` payload for a howto at `yc/howtos/deploy-release/main` with `status: active, modules: [release, ci]`:

```json
["status:active", "module:release", "module:ci"]
```

Prime's reviewer prompt already considers `tags` when ranking, so no prompt change is needed: a task touching `release` matches the `module:release` tag organically, and `status:deprecated` reads as a de-prioritization signal.

### 7.3 What stays unchanged

- `prime_project_knowledge` reviewer prompt — relevance ranking already handles any note type.
- `validate_plan` reviewer prompt — already treats `project_knowledge` as authoritative; howto excerpts inherit that.
- BM schema — howtos are plain markdown with frontmatter.

## 8. Extract reviewer guidance (`3a-howto`)

A new guidance block in `extract.tmpl`, mirroring `3a-gotcha` but with create/update semantics:

- **What a howto is.** A repeatable operational procedure the work established or relied on (deploy steps, migration runbook, local-env setup, release cut) that future work should follow rather than rediscover.
- **How it differs from neighbors** (the reviewer must pick the right type):
  - vs `gotcha` — a gotcha is a *pitfall / lesson* (what NOT to do); a howto is the *procedure* (what TO do).
  - vs `feature` — a feature is a user-facing capability; a howto is an internal operational procedure.
  - vs `module` — a module is a capability surface + invariants; a howto is a step sequence.
  - vs `decision` — a decision is a choice + rationale; a howto is the resulting procedure.
- **Permalink:** `<PROJECT>/howtos/<slug>/main` (kebab slug from title; **no ADR number**).
- **Actions:** `create` for a new procedure; `update` (with `body_patch`) when an envelope shows an existing howto's steps changed. **Never `supersede`.**
- **Frontmatter:** at minimum `status: "active"`, `modules: ["<slug>", ...]`, `last_verified: "<YYYY-MM-DD>"`; optional `origin`.
- **Body:** required `## When to use` and `## Steps`; optional `## Prerequisites`, `## Verification`, `## Rollback / if it goes wrong`, `## Related`. The `## Steps` section is load-bearing.
- **rationale:** one sentence on the cost of NOT recording the procedure.

## 9. Auto-apply ladder disposition

Both `howto` dispositions default to **human review** — a wrong runbook is worse than none:

| Proposal kind | Default disposition |
|---|---|
| `howto` create | **Human review** |
| `howto` update | **Human review** |

This is documented in `INTEGRATION.md`'s auto-apply ladder table alongside the existing rows.

## 10. Testing & verification

### 10.1 Anti-tangent (Go)

| What | Where | How |
|---|---|---|
| `ProposalTypeHowto` enum + parser switch | `internal/verdict/extract_parser_test.go` | Extend the type→folder map and the `types` slice; add `TestParseExtract_AcceptsHowtoType` asserting the parser accepts `{type: "howto"}` for both `action: create` and `action: update` |
| JSON schema `type` enum | (covered by parser test) | `extract_schema.json` gains `"howto"`; `schema_invariants_test.go` is unaffected (it asserts category-enum lockstep, not the type enum) |
| Extract reviewer prompt | `internal/prompts/testdata/extract_basic.golden`, `extract_milestone.golden` | Regenerate with `go test ./internal/prompts/... -update` after editing the template; review the diff to confirm only the new `3a-howto` block + type-list additions changed |
| Round-trip via extract handler | `internal/mcpsrv/integration_test.go` | Optional: stub reviewer returning a howto proposal; assert the handler decodes and forwards it unchanged |

`-race ./...` remains the mainline command (per project `CLAUDE.md`). No `prime.go` change → no prime test change.

### 10.2 bm-scribe skill (smoke against real BM)

Skills are markdown contracts. Validation is an end-to-end smoke run:

1. Run extract on a real plan completion that established a procedure; assert a proposal includes `type: howto`.
2. Run `/bm-scribe:create-howto` against that proposal; assert the three-step BM pattern lands a note at `<PROJECT>/howtos/<slug>/main` with frontmatter intact.
3. Run an update: invoke the skill again with changed steps for the same howto; assert `## Steps` is edited in place and `last_verified` is bumped (no new note created).
4. Run prime against a new plan touching the same module; assert the live howto appears in `picks`.

### 10.3 Failure modes the skill must handle explicitly

| Failure | Handling |
|---|---|
| No extract envelope in context and user provides no procedure | Prompt the user interactively for title / steps; do not error. |
| User edits a candidate to empty steps | Treat as skip; no note written. |
| `action: update` target howto doesn't exist | Fall back to `create` (three-step pattern) and report the fallback. |
| `BM_SCRIBE_PROJECT` unset | Ask user, cache for the session (matches every other bm-scribe creator skill). |

## 11. Versioning & rollout

- **Version target:** `v0.9.0` (backward-compatible feature: new `ProposalType` + new plugin skill).
- **Branch:** `version/0.9.0` (created).
- **CHANGELOG entries:** under `## [0.9.0] - 2026-05-29` — `### Added`: "Howto note type proposed by `extract_project_knowledge`" and "`bm-scribe:create-howto` skill."
- **Plugin version:** bump `plugin.json`, `package.json`, and `gemini-extension.json` from `0.2.0` to `0.3.0`.
- **Migration:** none. Existing KBs work unchanged; howtos appear only when extract starts proposing them.
- **Backward compatibility:** consumers parsing `ExtractResult.proposals` see a new `type` value. The `ProposalType` enum is an open string in the wire schema; exhaustive switches on it (Go) get a compile error and (TS) a runtime miss — flagged as a release-note item.
- **Docs to update:** `INTEGRATION.md` (seven→eight note types table, v0.7.0 layout folder list, auto-apply ladder), `docs/team-setup/project-knowledge-conventions.md` (type count, folder list, search hint), `README.md` (type mentions), `examples/project-knowledge/howto.md` (new template) + its `README.md` row. Per the repo's mirroring convention, the in-repo `INTEGRATION.md` is the source of truth; mirroring into the user's global `anti-tangent.md` is out of band.

## 12. Open questions

None blocking. Two things to monitor after rollout:

- **Type confusion in extract.** If the reviewer proposes `howto` where `gotcha` or `feature` fits better (or vice versa), tighten the `3a-howto` differentiator language. First evidence: dogfood for 2–3 plans and count mis-typed proposals.
- **`last_verified` staleness.** If howtos accumulate stale `last_verified` dates and nobody re-verifies, consider a prime-side "stale runbook" de-prioritization hint in the conventions doc (deferred).
