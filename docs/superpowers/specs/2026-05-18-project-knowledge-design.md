# Project knowledge — design

**Status:** draft 2026-05-18 — Updated 2026-05-20: BM contract verified (v0.21.1; no `supersede_note` verb — see verified-contract block in `docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md`); env-var count reconciled to five; section-8 storage approach updated from cron to systemd-timer-primary; dispatch-clause placement clarified (before Task spec field list).
**Target version:** 0.6.0 (minor bump)
**Tracking issue:** [patiently/anti-tangent-mcp#23](https://github.com/patiently/anti-tangent-mcp/issues/23)

## Background

On epic-scale projects with multiple agents (and multiple human authors) collaborating, implementers lose track of what's already been built and what decisions were taken (and why). Each task validates cleanly on its own, but the pieces drift apart — the parts don't compose into a working end product. Field reports cited in the [0.4.0 mcp-feedback-improvements design](./2026-05-17-mcp-feedback-improvements-design.md) anticipated this seam: anti-tangent's reviewer is text-only by design, so it has no way to know "what's already true about this project" beyond what the caller paste-includes.

This design adds a knowledge-base loop alongside the existing review loop:

- A **prime** call before a task that recommends which existing notes to read, and
- An **extract** call after completion that proposes new notes / updates to the knowledge base.

The knowledge itself lives in [Basic Memory](https://github.com/basicmachines-co/basic-memory) (recommended) or any other markdown-backed store; anti-tangent never reads or writes that store directly. The inspiration is [vp-claude](https://github.com/voxpelli/vp-claude), which composes Basic Memory + slash commands + autonomous agents on top. We adopt the iterative-knowledge-accumulation pattern, scoped to project-internal evidence only (no external research integrations).

## Scope

In scope:

- Two new stateless MCP tools: `prime_project_knowledge` and `extract_project_knowledge`.
- One new input field, `project_knowledge` (string), on `validate_task_spec` and `validate_plan`.
- Five new env vars, headed by `ANTI_TANGENT_KB_STORE` (gates output-format adaptation; default empty, no behavior change for existing users), plus four optional reviewer-model and max-tokens overrides; see §5.1 for the full list.
- Five note-type templates under `examples/project-knowledge/`: `decision`, `module`, `feature`, `glossary`, `epic`.
- Two new content sections in `INTEGRATION.md` ("Project knowledge (optional)") and a new operator-facing doc `docs/team-setup/basic-memory-shared-vm.md`.
- A 0.6.0 changelog entry.

Out of scope:

- Server-side disk I/O or persistent storage. Both new tools are stateless and read no files.
- Code dependency on Basic Memory. The integration is convention-only; `bm_commands` in outputs is paste-ready text, not a client call.
- External research integrations (DeepWiki, Tavily, GitHub APIs). Project knowledge is project-internal evidence only.
- Automatic application of proposals. The server proposes; the caller writes.
- Autonomous gardener / maintainer agents inside the MCP. Those are caller-side skill territory.
- Language-specific code analysis. Reviewer remains text-only.
- Backwards-compat break for existing callers (any prior 0.x release). Existing flows are unaffected when the new field is unset and the new env var is empty.

## Design

### 1. Architecture & boundaries

```
Multiple agents          Anti-tangent MCP                Reviewer LLM
& human authors          (advisory, stateless)           (different provider)
─────────────────        ─────────────────────           ────────────────
                                                                ▲
       ┌──── kb_index, excerpts ──▶ prime_project_knowledge ────┘
       │      (from BM queries)         │
       │                                ▼
       │                          "read these notes;
       │                           KB gaps: …"
       │
       └──── validate_task_spec(+project_knowledge) ──▶ existing review,
       │                                                grounded in KB excerpts
       │
       │                          completion envelopes
       │                                │
       │     ┌──── envelopes,     ──▶ extract_project_knowledge ──▶ proposals
       │     │     kb_index,            │                          (create/update/
       │     │     excerpts             ▼                           supersede)
       │     │                    structured proposals
       │     │                          │
       ▼     ▼                          ▼
┌────────────────────────┐  caller applies proposals
│   Basic Memory MCP     │◀────────────┘
│  (shared local store)  │
└────────────────────────┘
```

**Component split.**

1. **Anti-tangent server** (this repo): two new stateless tools, one new input field on existing tools. Anti-tangent has **zero code dependency** on Basic Memory — it consumes `kb_index` and excerpts as plain inputs, regardless of where the caller assembled them.
2. **Storage layer**: Basic Memory (recommended). The caller assembles inputs via Basic Memory queries and applies proposals via Basic Memory writes. Users who prefer markdown-in-repo or another store can wire that in; the protocol is the same.
3. **Schema**: five note-type templates layered on Basic Memory's standard frontmatter, shipped in `examples/project-knowledge/`.
4. **Dispatch clause additions**: small additions to the existing paste-verbatim block instructing the controller to prime/extract and the implementer to read the auto-attached project-knowledge section.

**Sharing behavior on the team's deployment.**

- Concurrent agents on one developer machine: both talk to the same shared Basic Memory; BM's concurrency handles it.
- Multiple human authors / multiple developer machines: handled by whichever transport the shared BM exposes (per `docs/team-setup/basic-memory-shared-vm.md`).
- Contradictory decisions: explicit `status: proposed | accepted | superseded` plus `supersedes: [permalink]` chains. Anti-tangent's extract flags contradictions as `contradicts_existing` findings; the caller decides whether to supersede.

### 2. Note types & schemas

Five types, organized as two layers:

- **Durable reference layer** (timeless / slow-evolving): `decision`, `module`, `feature`, `glossary`. Survives epics.
- **Epic-scoped layer** (time-bounded): `epic`. Becomes mostly read-only at epic close.

All five layer on Basic Memory's standard frontmatter (`permalink`, `tags`, etc.) so BM's own indexing, tagging, and cross-linking work out of the box.

#### `epic` (new)

The "tight development" container. One per epic. Created at kickoff; extract appends to its progress ledger; closed at epic-done.

```yaml
---
permalink: epics/2026-q2-large-project-support
type: epic
title: Large-project support (KB integration)
status: in_progress             # planned | in_progress | closed | abandoned
opened_at: 2026-05-18
closed_at: null
owners: ["@patrick"]
plan_refs: [docs/superpowers/specs/2026-05-18-…]
touches_modules: [modules/mcpsrv, modules/prompts, modules/config]
produces_decisions: []          # filled in by extract over time
relates: [features/validate-plan, features/validate-task-spec]
tags: [epic]
---
```

Body sections: `## Charter`, `## In scope`, `## Out of scope`, `## Acceptance (epic-level)`, `## Progress ledger` (append-only; one entry per finished task), `## Open questions`.

#### `feature` (new)

The user-facing capability catalog. Persists across epics. Links to the decisions that shaped it.

```yaml
---
permalink: features/validate-task-spec
type: feature
title: validate_task_spec — per-task pre-implementation gate
surface: mcp_tool               # mcp_tool | cli | env_var | protocol | other
status: stable                  # experimental | stable | deprecated | removed
since_version: 0.1.0
last_changed_in: 0.4.0
relates_modules: [modules/mcpsrv, modules/session, modules/prompts]
shaped_by_decisions: [decisions/0017-text-only-reviewer]
tags: [protocol, validators]
---
```

Body sections: `## What it does`, `## How it works`, `## Recent material changes` (release-tagged pointers; details live in linked decisions), `## Related`.

#### `decision` (existing pattern, one new field)

ADR-style. Append-only. One new optional field: `epic_origin: <permalink>` — extract back-links decisions to the epic that produced them, which is how `epic.produces_decisions` gets coherently filled.

```yaml
---
permalink: decisions/0042-cache-pass-reviews-3m
type: decision
title: Cache identical passing plan reviews for 3 minutes
status: accepted                # proposed | accepted | superseded
supersedes: []                  # list of permalinks
proposed_by: "@patrick"
decided_at: 2026-05-12
epic_origin: epics/2026-q2-large-project-support   # NEW (optional)
relates: [modules/plan-gate, decisions/0017-text-only-reviewer]
tags: [validate_plan, caching]
---
```

Body sections: `## Context`, `## Decision`, `## Consequences`, `## Alternatives considered`.

#### `module` (unchanged schema)

Internal structural notes. Body: `## Purpose`, `## Invariants`, `## Conventions`, `## Touch-points`.

#### `glossary` (unchanged schema)

Domain-term canonical definitions. Body: short definition + notes.

#### Maintenance-ownership defaults

| Type | Author at birth | Updated by |
|---|---|---|
| `epic` | Human at kickoff | Mostly automated (extract appends ledger; humans edit open questions) |
| `decision` | Drafted by extract → reviewed by human → merged | Append-only; new decisions supersede old ones |
| `module` | Human (or seeded from a spec) | Mostly human; extract proposes invariant/convention edits when it sees drift |
| `feature` | Human (or seeded from a spec) | Mostly human; extract proposes "Recent material changes" entries |
| `glossary` | Opportunistic (human or extract) | Opportunistic |

### 3. Server surface

Two new stateless tools, one new input field, five new env vars (see §5.1). The result envelope follows the existing `Result` shape from `internal/verdict/` so the parser and `summary_block` paths don't fork.

#### 3.1 `prime_project_knowledge`

Stateless — no session created. Called at task start; the caller routes the answer into the implementer's brief and into `validate_task_spec.project_knowledge`.

Inputs:

| field | type | required | notes |
|---|---|---|---|
| `task_title` | string | ✓ | |
| `goal` | string | ✓ | |
| `acceptance_criteria` | []string | ✓ | |
| `non_goals` | []string |  | optional |
| `context` | string |  | optional |
| `kb_index` | []KBIndexEntry |  | `{ permalink, type, title, summary, tags? }`. Empty array = no KB yet; reviewer returns gaps only. |
| `epic_permalink` | string |  | bias picks toward the epic note, its `touches_modules`, and its linked decisions |
| `max_picks` | int |  | default 10, ceiling 25 |
| `max_tokens_override` | int |  | clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING` |

Outputs (`Result`-shaped envelope):

- `verdict`: `pass | warn | fail`
- `findings`: standard shape. New categories: `kb_gap`, `ambiguous_pick`, `missing_index_entry`. Plus existing `other`.
- `picks`: `[]{ permalink, reason, priority: critical|major|minor }`
- `bm_commands` *(only when `ANTI_TANGENT_KB_STORE=basic-memory`)*: `[]{ tool: "read_note", args: { permalink } }`
- Standard envelope: `next_action`, `summary_block`, `model_used`, `review_ms`

#### 3.2 `extract_project_knowledge`

Stateless. Called post-completion; emits proposals the caller persists via the KB store.

Inputs:

| field | type | required | notes |
|---|---|---|---|
| `completion_envelopes` | []CompletionEnvelope | ✓ | one or more `validate_completion` outputs. Recommend `final_diff` over `final_files` for size. |
| `plan_text` | string |  | optional |
| `kb_index` | []KBIndexEntry |  | same shape as prime |
| `current_kb_excerpts` | map[permalink]string |  | full bodies of notes likely to be edited (caller pre-selects) |
| `epic_permalink` | string |  | when set, extract appends to that epic's progress ledger and sets `epic_origin` on any new decision proposals |
| `max_tokens_override` | int |  | |

Outputs:

- `verdict`: `pass | warn | fail`. `pass` = proposals confident; `warn` = partial; `fail` = insufficient evidence.
- `findings`. New categories: `insufficient_evidence`, `redundant_proposal`, `contradicts_existing`. Severity scales as today.
- `proposals`: `[]Proposal` —

  ```
  {
    action: "create" | "update" | "supersede",
    type: "decision" | "module" | "feature" | "glossary" | "epic",
    permalink: "decisions/0042-…",
    title: "…",
    frontmatter: { …type-specific fields… },
    body: "<markdown>",                   // for create or full-body update
    body_patch: "<unified diff>",          // for update; alternative to body when the existing note is large
    rationale: "why this proposal",
    evidence_refs: [ "completion[0].finding[2]", "plan_text:…" ],
    supersedes: ["decisions/0017-…"]       // for action=supersede
  }
  ```

  For `action: "update"`, the reviewer chooses `body` or `body_patch`; the server does not enforce a threshold beyond the existing 200 KB payload cap. `body_patch` uses standard unified-diff format (`diff -u` shape).

- `bm_commands` *(only when `ANTI_TANGENT_KB_STORE=basic-memory`)*: `[]{ tool, args_json }` — paste-ready `write_note` / `edit_note` calls (args_json is a JSON-encoded object string per OpenAI strict-mode requirements; see plan's cross-cutting constraints). For supersede, emit the two-step mapping (`write_note` for the new note + `edit_note` to flip the predecessor's `status`); BM does not expose a `supersede_note` verb. Tool names anchored in INTEGRATION.md so a BM version bump is a doc-only change.
- Standard envelope.

#### 3.3 `project_knowledge` field on existing tools

Added to `validate_task_spec` and `validate_plan` inputs.

- Type: `string` (plain text; markdown is fine).
- Reviewer posture: **authoritative** — same as `pinned_by`. Stated facts are treated as true and not flagged as `unverifiable_codebase_claim`.
- Caps: counts against the existing 200 KB payload cap. INTEGRATION.md recommends keeping under ~16 KB per call; the prime call's picks are what keep it bounded in practice.
- Unset behavior: identical to today. This is the backward-compat guarantee.
- Not stored in the session beyond the current call. Rationale: KB content can change during a task's session lifetime (sliding 4 h TTL), and a snapshot stored at `validate_task_spec` time would silently drift. `check_progress` and `validate_completion` do not gain a `project_knowledge` field in 0.6.0 — those calls keep the existing scope-drift / AC-coverage posture without KB grounding. Field evidence can drive a follow-up minor bump if completion-time KB grounding turns out to be load-bearing.

#### 3.4 Findings vocabulary additions (in `internal/verdict/`)

```
// prime
kb_gap
ambiguous_pick
missing_index_entry

// extract
insufficient_evidence
redundant_proposal
contradicts_existing
```

These join the existing set (`missing_acceptance_criterion`, `scope_drift`, `ambiguous_spec`, `unaddressed_finding`, `quality`, `session_not_found`, `payload_too_large`, `unverifiable_codebase_claim`, `malformed_evidence`, `other`).

### 4. Caller workflow & dispatch-clause changes

The server is stateless; everything that ties prime → implement → extract together lives in the caller's dispatch logic.

#### Controller flow (per epic)

```
Epic kickoff  (human, once)
  └── Create epic note in BM (charter + scope + epic-level ACs, status=in_progress)

Plan handoff  (controller, once per plan)
  ├── validate_plan(plan_text, project_knowledge?)   ── existing flow + optional new field
  └── proceed to per-task dispatch

Per task  (controller, before each subagent)
  ├── search BM for relevant notes by task terms +
  │   epic.touches_modules + epic.relates           → kb_index
  ├── prime_project_knowledge(task fields, kb_index, epic_permalink) → picks
  ├── read picked notes from BM                     → kb_excerpts (string)
  └── dispatch subagent with brief that includes kb_excerpts;
      pass same kb_excerpts as project_knowledge into validate_task_spec

Per task  (subagent → DONE → controller)
  └── validate_completion envelope                   ── as today

Post-task  (controller, per task or batched per plan)
  ├── extract_project_knowledge(completion_envelopes,
  │                             plan_text, kb_index,
  │                             current_kb_excerpts,
  │                             epic_permalink)      → proposals
  ├── apply by ladder (see 4.3)
  └── write via BM write_note / edit_note (supersede = two-step write_note + edit_note; no supersede_note verb)

Epic close  (human, once)
  └── set epic.status=closed, closed_at; freeze the progress ledger
```

#### Implementer dispatch-clause addition

Inserted **before** the existing "Task spec" section in the paste-verbatim clause (the implementer must read the project-knowledge brief BEFORE deciding what to pass into the `validate_task_spec` call — the brief informs the call):

```markdown
## Project knowledge (auto-attached by the controller)

The task brief above includes a "Project knowledge" section with excerpts
the controller pre-selected from the project KB. Read it before validate_
task_spec — it carries decisions, module invariants, and prior context
relevant to this task. Treat it as authoritative.

When calling validate_task_spec, also pass that same section verbatim
as `project_knowledge` so the reviewer has the same grounding you do.
```

The subagent sees no Basic Memory and makes no prime/extract calls. Prime is a controller responsibility (the controller owns epic context).

#### 4.3 Auto-apply ladder for extract proposals

Recommended default disposition (the server doesn't enforce; teams can override):

| Proposal kind | Default disposition |
|---|---|
| `epic` progress-ledger append | Auto-apply |
| `feature` "Recent material changes" append | Auto-apply |
| `decision` create with `status: proposed` | Auto-apply (draft for humans to review) |
| `decision` create with `status: accepted` | **Human review** |
| `decision` supersede | **Human review** |
| `module` invariant/convention edit | **Human review** |
| `glossary` create | Auto-apply |
| Anything with `contradicts_existing` finding | **Human review, blocking** |

#### Behavior with `ANTI_TANGENT_KB_STORE` unset

- New tools still callable; outputs omit `bm_commands` only. Caller maps to whatever store they use.
- Existing tools unchanged. `project_knowledge` left unset behaves identically to today.
- The dispatch clause's project-knowledge block simply isn't attached when there's no KB to draw from.

#### Behavior with `ANTI_TANGENT_KB_STORE=basic-memory`

- `bm_commands` arrays appear in prime/extract outputs.
- INTEGRATION.md anchors the recommended BM tool names (`search_notes`, `read_note`, `write_note`, `edit_note`; supersede uses a two-step `write_note` + `edit_note` mapping rather than a `supersede_note` verb — BM does not expose one).

### 5. Operational details

#### 5.1 Environment variables (new)

| Var | Default | Purpose |
|---|---|---|
| `ANTI_TANGENT_KB_STORE` | `""` (off) | Set to `basic-memory` to enable `bm_commands` in outputs |
| `ANTI_TANGENT_PRIME_MODEL` | falls back to `ANTI_TANGENT_PLAN_MODEL` → `ANTI_TANGENT_PRE_MODEL` | Reviewer for prime |
| `ANTI_TANGENT_EXTRACT_MODEL` | falls back to `ANTI_TANGENT_PLAN_MODEL` → `ANTI_TANGENT_PRE_MODEL` | Reviewer for extract |
| `ANTI_TANGENT_PRIME_MAX_TOKENS` | `4096` | Output cap, ceiling-clamped |
| `ANTI_TANGENT_EXTRACT_MAX_TOKENS` | `8192` | Output cap, ceiling-clamped |

All existing env vars unchanged.

#### 5.2 Payload sizing

- **prime:** `kb_index` is the scaling input. Recommend 600-entry soft cap; use `search_notes(query=…)` pre-filtering.
- **extract:** `current_kb_excerpts` is the squeeze. Recommend caller pre-selects by overlap with completion terms + everything in `epic.touches_modules`.

The existing 200 KB hard cap and the `payload_too_large` finding apply unchanged.

#### 5.3 Output token budget scaling

When `max_tokens_override` is omitted:

- **prime:** `max(PrimeMaxTokens, min(MaxTokensCeiling, 1500 + 50*len(kb_index)))`
- **extract:** `max(ExtractMaxTokens, min(MaxTokensCeiling, 2000 + 1200*len(completion_envelopes)))`

Truncation surfaces via the existing `category: other`, `criterion: reviewer_response` finding shape.

#### 5.4 Caching

No caching for either new tool. Inputs vary too much per call (the KB index shifts as the KB grows), and caching with a stale index would silently miss freshly-added notes. Revisit if measured friction warrants.

#### 5.5 Errors & failure modes

| Situation | Category | Severity |
|---|---|---|
| Empty `kb_index` on prime | `kb_gap` | minor |
| `epic_permalink` not in `kb_index` | `missing_index_entry` | major |
| Empty `completion_envelopes` on extract | `insufficient_evidence` | critical |
| Completion envelopes carry no `final_diff` AND no `final_files` | `insufficient_evidence` | major |
| `ANTI_TANGENT_KB_STORE=basic-memory` but permalinks don't look like BM permalinks | `other`, `criterion: kb_store_mismatch` | minor |

Existing categories (`payload_too_large`, truncation, session-related) apply unchanged.

#### 5.6 Logging

One structured JSON log line per call on stderr, matching existing format:

```json
{"tool":"prime_project_knowledge","duration_ms":2143,"model":"…","verdict":"pass",
 "picks":4,"findings":1,"kb_index_size":312,"epic":"epics/…"}
```

```json
{"tool":"extract_project_knowledge","duration_ms":7820,"model":"…","verdict":"pass",
 "proposals":3,"findings":0,"envelopes":2,"epic":"epics/…"}
```

`ANTI_TANGENT_LOG_LEVEL=debug` also logs full prompts and reviewer responses (as today).

#### 5.7 Testing

- Prompt rendering: golden files in `internal/prompts/testdata/`, updated via `-update`.
- Provider responses: `httptest.Server`-driven unit tests per the existing `internal/providers/` pattern. Covers findings categories, `bm_commands` presence/absence per env, output-token scaling.
- MCP integration: `internal/mcpsrv/integration_test.go` extended to cover both new tools end-to-end (request → handler → reviewer mock → response shape), including stateless behavior under `-race`, env-gated output adaptation, payload-cap rejection, empty-input fast-fails.
- E2E: behind `-tags=e2e`. One scenario per new tool against a real reviewer with a small synthetic KB.
- No new fixtures of real BM data — kb_index entries inline for determinism.

#### 5.8 Versioning & release

- Branch: `version/0.6.0` (minor bump, additive features). 0.5.0 is already in flight on a separate branch; 0.6.0 cuts after that ships.
- Merge commit tag: `[minor]`.
- CHANGELOG entry under `## [0.6.0]`: `### Added` block listing the two tools, the field, the env var, the templates, the INTEGRATION.md section, and the team-setup doc.
- No deprecations, no breaking changes.

## Migration & backward compatibility

- Existing users (any prior 0.x release) with no KB infrastructure: zero action required. Existing flows unchanged. `ANTI_TANGENT_KB_STORE` stays empty; the new tools are simply unused.
- Users adopting the new feature: opt in by setting `ANTI_TANGENT_KB_STORE=basic-memory` (or any other store), provisioning the shared BM per `docs/team-setup/basic-memory-shared-vm.md`, updating their dispatch clause with the project-knowledge block, and beginning to call prime/extract from their controller. Bootstrap the KB by running one or more completed plans through `extract_project_knowledge` to seed `module`, `feature`, and `decision` notes.

## Documentation deliverables

```
examples/project-knowledge/decision.md          ── template
examples/project-knowledge/module.md            ── template
examples/project-knowledge/feature.md           ── template
examples/project-knowledge/glossary.md          ── template
examples/project-knowledge/epic.md              ── template
examples/project-knowledge/README.md            ── overview + conventions
INTEGRATION.md                                  ── new "Project knowledge (optional)" section
README.md                                       ── one paragraph + link
docs/team-setup/basic-memory-shared-vm.md       ── NEW operator-facing setup doc
CHANGELOG.md                                    ── 0.6.0 entry
```

### `docs/team-setup/basic-memory-shared-vm.md` outline

1. What this doc is and isn't (scope: shared-VM teams; solo devs skip)
2. Topology overview (4-dev shared VM, anti-tangent has no direct BM contact)
3. VM baseline (sizing, OS, firewall rules)
4. Installing Basic Memory on the VM (link to upstream; systemd unit)
5. Configuring remote MCP transport (stdio-via-SSH-proxy per the verified contract; BM v0.21.1 README does not prescribe a remote transport, so SSH-proxy against BM's default stdio mode is the conventional pattern)
6. Auth & access control (per-dev SSH keypairs or tokens, depending on the verified transport; rotation, secret storage)
7. Per-developer Claude Code MCP config (concrete JSON snippet — shape depends on the verified transport)
8. Storage & backup (git-backed KB directory; systemd timer at 60s primary, inotify-recursive watcher alternative, 5-minute timer/cron fallback — see team-setup doc §8 for the full shape)
9. Day-2 ops (upgrades, token rotation, adding/removing devs, VM restore)
10. Verification checklist (5-step smoke test)
11. License compatibility note (AGPL-3.0; unmodified upstream → network-service pattern → trivially compliant; team policy: no fork-and-patch, bugs go upstream to the BM repo)
12. Troubleshooting

## Open questions (resolve during implementation)

- Whether `epic_origin` belongs on `module` and `feature` proposals too (currently only on `decision`). Defer until we see field evidence; YAGNI by default.

*(The previous open questions on BM transport and tool names were resolved by Task 0a — see the verified-contract block at the bottom of `docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md`.)*

## Non-goals (reaffirmed)

- No persistent storage on the server.
- No external knowledge sources.
- No code dependency on Basic Memory.
- No automatic application of proposals.
- No autonomous gardener.
- No language-specific code analysis.
- No backwards-compat break for existing callers (any prior 0.x release).
