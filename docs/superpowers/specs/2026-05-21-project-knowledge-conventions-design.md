# Project-knowledge conventions — design

**Status:** draft 2026-05-21
**Target version:** 0.7.0 (minor bump)
**Tracking issue:** [patiently/anti-tangent-mcp#31](https://github.com/patiently/anti-tangent-mcp/issues/31)

## Background

v0.6.0 shipped the project-knowledge loop: two new stateless MCP tools (`prime_project_knowledge`, `extract_project_knowledge`), a `project_knowledge` field on `validate_task_spec` / `validate_plan`, five note-type templates under `examples/project-knowledge/`, and a generic-adopter integration guide in INTEGRATION.md. Field-testing the loop end-to-end against a real Basic Memory v0.21.1 instance (see [`2026-05-18-project-knowledge-design.md`](2026-05-18-project-knowledge-design.md) for the v0.6.0 design) surfaced two coupled gaps:

1. **The shipped templates capture durable architectural knowledge well but don't model operational state.** Teams running ticket-driven workflows (Jira / Linear / GitHub Issues / similar) want their KB to function as a live operational dashboard — what stories are in flight under each epic, what PRs are open on each story, what's been deployed, what decisions came out of which story. v0.6.0 has an `epic` type with a charter + progress ledger, but no dashboard sections; and no notion of a `story` at all.

2. **The shipped permalink shape doesn't namespace by project.** Today's templates use shapes like `decisions/0042-x` — which works for a single-product KB but breaks when one BM instance hosts multiple repos (e.g., a Basic Memory daemon on a shared host serving an open-source project, a private team monorepo, and various smaller repos). Cross-references across projects are ambiguous; new notes inherit no namespace context from the existing KB.

The bar for what's worth a note also benefits from being made explicit. Field-testing showed that KB notes have real value even when the captured fact is canonical elsewhere (CHANGELOG, PR description, team-setup doc, commit message) — the KB note adds cross-linking and durable rationale framing that the canonical sources don't. A KB capturing only "things with no canonical home" is too narrow.

This design extends the v0.6.0 surface to address both gaps while staying additive — v0.6.x adopters continue working without changes.

## Scope

In scope:

- A 6th note type `story` with its own template, schema-enum entry, and parser support. Frontmatter scoped to ticket-driven workflow (issue ID, parent epic, owners, tracker URL); body sections provide a live operational dashboard (multi-PR list with relationships, subtasks, deployment state, decisions produced).
- Rewrite of the `epic.md` template body — dashboard sections (Stories list with status + deployment, Open-PR list across stories, epic-level acceptance criteria with checkmarks) plus the existing charter and progress-ledger sections.
- Project-prefixed folder-per-ticket permalink shape across all six note types: `<PROJECT>/<type>/<key>/main`. All cross-references in frontmatter become permalink strings.
- An optional `story_origin` field on `decision` frontmatter, alongside the existing `epic_origin`.
- Reviewer-prompt changes in `internal/prompts/templates/extract.tmpl`: recognise the `story` type, infer the project prefix from `kb_index` permalinks, propose dashboard-update bm_commands only on milestone events (PR opened, PR state transition, deployment landed, decision finalized), and emit them as `replace_section` operations against the appropriate dashboard section.
- A new operator-facing conventions doc at `docs/team-setup/project-knowledge-conventions.md` describing the per-project tuning loop.
- A small directory of frozen-snapshot real anti-tangent example notes under `examples/project-knowledge/dogfood/`.

Out of scope:

- New finding categories. The existing v0.6.0 six (`kb_gap`, `ambiguous_pick`, `missing_index_entry`, `insufficient_evidence`, `redundant_proposal`, `contradicts_existing`) cover every failure mode this design needs.
- Anti-tangent-side validation of issue-ID format. The `<TICKET-ID>` placeholder is a doc convention; adopters substitute Jira / Linear / GitHub Issues / whatever they use, and the reviewer prompt doesn't enforce a specific format.
- Runtime Basic Memory integration. Anti-tangent remains text-only; extract emits paste-ready bm_commands, the caller applies them.
- Concurrent-write resolution beyond the `replace_section` semantics already supported. Sequential extract calls in our protocol are the norm; if concurrent agents updating the same KB note becomes a real problem, a v0.8.x follow-up can switch to `find_replace` for single-row diffs.
- Re-snapshot automation for the committed dogfood examples. The directory is frozen at each minor-release boundary and re-snapshotted manually if anti-tangent's epic/story state has materially evolved.
- Story-done as a separate milestone. PR-merged covers it — the last PR landing for a story IS the story-done event.

## Design

### 1. File layout & versioning

This is a minor bump (`v0.7.0`). Adding a value to a schema enum is additive — backwards-compatible with v0.6.x callers — but it expands the public response surface, so by repo policy it's minor, not patch.

```
examples/project-knowledge/
├── README.md                     [MODIFY]   document the 6 types + folder-per-ticket pattern + adopter-doc link
├── epic.md                       [REWRITE]  dashboard sections + folder-per-ticket permalink
├── story.md                      [NEW]      6th type
├── decision.md                   [MODIFY]   optional `story_origin` field; permalink shape
├── module.md                     [MODIFY]   permalink shape; README framing reference
├── feature.md                    [MODIFY]   permalink shape only
├── glossary.md                   [MODIFY]   permalink shape only
└── dogfood/                      [NEW DIR]  frozen real anti-tangent example notes
    ├── README.md
    ├── epics/gh-23/main.md
    ├── stories/gh-25/main.md
    ├── decisions/0001-text-only-reviewer/main.md
    └── modules/review-pipeline/main.md

docs/team-setup/
└── project-knowledge-conventions.md   [NEW]   adopter-tuning guide

internal/prompts/templates/
└── extract.tmpl                  [MODIFY]   recognise `story`; milestone-only dashboard rules

internal/prompts/testdata/
├── extract_basic.golden          [REGEN]    after extract.tmpl changes
└── extract_milestone.golden      [NEW]      covers a milestone-triggered dashboard update

internal/verdict/
├── extract.go                    [MODIFY]   add `ProposalTypeStory ProposalType = "story"`
├── extract_schema.json           [MODIFY]   `proposals[].type` enum gains "story"
└── extract_parser_test.go        [MODIFY]   regression tests covering all 6 types

CHANGELOG.md                      [NEW]      ## [0.7.0] entry
INTEGRATION.md                    [MODIFY]   mention 6 types; link to new conventions doc
```

> **IMPORTANT — INTEGRATION.md size budget.** Any v0.7.0 addition to `INTEGRATION.md` MUST keep its total size **under 40,000 bytes**, the user-instructions warning threshold. Post-v0.6.2 trim the file sits at 38,835 bytes (~1.2 KB headroom). The v0.7.0 add (mention of 6 types + one link to the new conventions doc) needs to fit inside that headroom. If it doesn't, trim other prose in the same PR to land back under the threshold — same posture as v0.5.1 and v0.6.2. The "Project knowledge (optional)" anchor referenced from README.md:343 must stay intact.

### 2. Note types — full schemas

All six types share the same permalink shape: `<PROJECT>/<type>/<key>/main`, where `<PROJECT>` is the BM project name and `<key>` is either a slug (decisions, modules, features, glossary) or a ticket ID (epics, stories). The trailing `/main` allows arbitrary side-docs to live in the same folder (e.g., `epics/<TICKET-ID>/retro.md`) without polluting the top-level KB index.

#### 2.1 `story` (new)

The operational-dashboard layer at story scope. Time-bounded: opened when work starts, closed when the last PR for the story merges and lands.

```yaml
---
permalink: <PROJECT>/stories/<TICKET-ID>/main
type: story
title: <one-line story title>
status: planned                  # planned | in_progress | review | done | abandoned
opened_at: <YYYY-MM-DD>
closed_at: null
parent_epic: <PROJECT>/epics/<EPIC-TICKET-ID>/main   # null if standalone
owners: ["@<handle>"]
tracker_url: <issue URL or null>
tags: [story]
---
```

Body sections (in order):

1. `## Story brief` — one to two paragraphs: scope, why it's part of the parent epic, what success looks like.
2. `## Acceptance criteria` — testable criteria; story-scoped.
3. `## PRs` — table: `PR | State | Branch | Relationship | Merged into | Deployed`. State enum: `draft | review | merged | closed`. Relationship enum: `initial | follow-up to #N | supersedes #N | parallel with #N`.
4. `## Subtasks` — markdown task list (`- [ ] / - [x]`); each item may link to a PR.
5. `## Deployment state` — table: `Env | Latest landed | Date | Notes`. One row per environment the team tracks (staging, prod, etc.).
6. `## Decisions produced` — list of `<PROJECT>/decisions/<NNNN>-<slug>/main` permalinks with one-sentence summaries.
7. `## Open questions` — bullets.

#### 2.2 `epic` (rewritten)

Becomes a live operational dashboard. Charter content from v0.6.0 stays as supporting context; new dashboard sections become the body's center of gravity.

```yaml
---
permalink: <PROJECT>/epics/<EPIC-TICKET-ID>/main
type: epic
title: <one-line epic title>
status: planned                  # planned | in_progress | closed | abandoned
opened_at: <YYYY-MM-DD>
closed_at: null
owners: ["@<handle>"]
tracker_url: <issue URL or null>
plan_refs: [docs/superpowers/plans/<plan-file>]
touches_modules: [<PROJECT>/modules/<name>/main]   # KB permalinks, not Go-package paths
produces_decisions: []                              # filled in by extract over time
relates: [<PROJECT>/features/<slug>/main]
tags: [epic]
---
```

Body sections (in order):

1. `## Charter` — two to three sentences naming the user-visible goal and the success criterion.
2. `## In scope / Out of scope` — bullet lists.
3. `## Acceptance (epic-level)` — checklist (`- [ ] / - [x]`); per-story ACs live in story notes.
4. `## Stories` — table: `Story (linked) | Status | Deployment | Tracker`.
5. `## Open PRs` — table aggregating open PRs across all stories in this epic: `PR | Story (linked) | State | Title`. Once a PR merges or closes, it leaves this table; the story's own dashboard keeps the durable per-story PR history.
6. `## Progress ledger` — append-only; one entry per milestone.
7. `## Open questions` — bullets.

**Note on terminal-status asymmetry.** Stories terminate with `status: done`; epics terminate with `status: closed`. The asymmetry is deliberate — engineering finishes a story (`done`); product management closes an epic (`closed`) once all its stories are done AND any post-merge follow-through (deployment, retro, AC confirmation) has landed. Both states are functionally terminal; the divergent vocabulary signals the divergent gesture.

#### 2.3 `decision`

```yaml
---
permalink: <PROJECT>/decisions/<NNNN>-<slug>/main
type: decision
title: <one-line title>
status: proposed                 # proposed | accepted | superseded | rejected
supersedes: []                   # list of permalinks
proposed_by: "@<handle>"
decided_at: <YYYY-MM-DD>
epic_origin: <PROJECT>/epics/<EPIC-TICKET-ID>/main   # optional; epic-level origin
story_origin: <PROJECT>/stories/<TICKET-ID>/main      # NEW — optional; story-scoped origin
relates: []
tags: []
---
```

Body sections unchanged from v0.6.0: `## Context / Decision / Consequences / Alternatives considered`.

`story_origin` enables extract to populate a story's `## Decisions produced` section by walking `story_origin` matches across decision notes. `epic_origin` stays the broader category for decisions made at epic level (not under a specific story).

#### 2.4 `module`

```yaml
---
permalink: <PROJECT>/modules/<module-name>/main
type: module
title: <one-line title>
status: stable                   # experimental | stable | deprecated | removed
last_changed_in: <X.Y.Z>
relates_features: []             # list of permalinks
shaped_by_decisions: []          # list of permalinks
tags: [module]
---
```

Body sections unchanged: `## Purpose / Invariants / Conventions / Touch-points`.

The README clarifies that modules describe coherent capabilities (user-facing surface), not 1:1 Go-package mappings. Example: `<PROJECT>/modules/review-pipeline/main` spans `internal/mcpsrv` + `internal/verdict` + `internal/prompts` + `internal/providers` jointly implementing the `validate_X` surface.

#### 2.5 `feature`

```yaml
---
permalink: <PROJECT>/features/<slug>/main
type: feature
title: <one-line title>
surface: mcp_tool                # mcp_tool | cli | env_var | protocol | other
status: stable
since_version: <X.Y.Z>
last_changed_in: <X.Y.Z>
relates_modules: []
shaped_by_decisions: []
tags: []
---
```

Body sections unchanged: `## What it does / How it works / Recent material changes / Related`.

#### 2.6 `glossary`

```yaml
---
permalink: <PROJECT>/glossary/<term-slug>/main
type: glossary
title: <Term>
status: stable
tags: [glossary]
---

**<Term>** — <one-sentence canonical definition>.

<Optional notes paragraph for nuance, common confusions, or related terms.>
```

### 3. Server surface — `extract.tmpl` reviewer-prompt + schema delta

#### 3.1 Schema delta (Go)

In `internal/verdict/extract.go`, add a 6th `ProposalType` constant:

```go
const (
    ProposalTypeDecision ProposalType = "decision"
    ProposalTypeModule   ProposalType = "module"
    ProposalTypeFeature  ProposalType = "feature"
    ProposalTypeGlossary ProposalType = "glossary"
    ProposalTypeEpic     ProposalType = "epic"
    ProposalTypeStory    ProposalType = "story"
)
```

In `internal/verdict/extract_schema.json`, the `proposals[].type` enum gains `"story"`. The `proposalWire` switch in `extract_parser.go` gains the same case. Existing strict-mode invariants (`schema_invariants_test.go`) accept the additive enum entry without modification.

#### 3.2 Reviewer-prompt changes

Five changes to the `## What to do` section of `extract.tmpl`:

**Step 1 (modified)** — distinguish durable knowledge from milestone events:

> Read each completion envelope as the authoritative record of what just shipped. Identify two distinct kinds of signal:
> - **Durable knowledge** worth capturing as a new note: an architectural decision, a module's invariants, a new feature's behavior, a glossary term. Always proposable; not gated on milestones.
> - **Milestone events** that warrant a dashboard update on an existing `epic` or `story` note. A milestone is ONE of: a PR opened, a PR transitioning state (draft→ready / review→merged / merged→closed), a deployment landing in any environment, or a decision finalizing (status: accepted). Story-done is NOT a separate milestone — it's the natural consequence of the last PR for a story merging.

**Step 2 (modified)** — `type` enum gains `story`. Otherwise unchanged.

**New Step 2a (added)** — project-prefix inference:

> All permalinks use the shape `<PROJECT>/<type>/<slug-or-ticket-id>/main`. Infer `<PROJECT>` from the most common prefix in `kb_index` permalinks. **Tie-break:** if two or more prefixes are tied on count, prefer the prefix that matches a repo / project name referenced in the task spec or `plan_text` (when present); failing that, take the alphabetically first prefix. If `kb_index` is empty or no prefix can be inferred even with tie-break, fall back to the literal placeholder `<PROJECT>` in your proposal permalinks AND emit one `missing_index_entry` (severity: major) finding naming the absence — callers must then resolve the placeholder before applying the bm_commands.
>
> Note: `missing_index_entry` is reused from the v0.6.0 prime-side finding vocabulary — no new finding category is introduced. The same finding fires from both prime and extract, on different triggers.

**New Step 3a (added)** — milestone-only dashboard-update rule:

> Dashboard updates require a milestone. If a completion envelope surfaces a milestone, propose `action: update` on the relevant `epic` or `story` note with a `replace_section` (or `frontmatter_patch` / `insert_before_section`) bm_command that rewrites the affected section or frontmatter field verbatim. To produce the new section content, use the matching entry from `current_kb_excerpts` to read the current section, identify the row(s) that change, and emit the full updated section text (NOT a diff). If `current_kb_excerpts` has no entry for the target note, emit an `insufficient_evidence` finding rather than fabricating a section body.
>
> **Detecting "last PR for story":** the reviewer treats a PR merge as the story's terminal PR ONLY when the story's frontmatter `status` in `current_kb_excerpts` is already `done` (or `review`, when the merging PR is the last `draft`/`review` row in the story's `## PRs` table). The caller — the agent or human who closes the story — sets `status: done` on the story note BEFORE invoking extract for the terminal-merge milestone. This makes story closure an explicit caller gesture rather than an LLM-inferred heuristic; the reviewer's role is to amplify the closure into all the dashboard consequences. If `status` is still `in_progress` (or absent) when a PR merges, the merge falls into the "PR merged, NOT last for story" row instead.
>
> Map of milestone → sections to update:
>
> | Milestone | Story-side update | Epic-side update |
> |---|---|---|
> | PR opened or state change (still open) | story `## PRs` (row state) | epic `## Open PRs` (add/update row) |
> | PR merged, NOT last for story | story `## PRs` (row state → merged) | epic `## Open PRs` (drop row) |
> | PR merged + last for story (caller has set story `status: done`) | story frontmatter (`status: done`, `closed_at: <YYYY-MM-DD>`), story `## PRs` (row state → merged), story `## Deployment state` (if landed) | epic `## Stories` (status → done), epic `## Open PRs` (drop row) |
> | Deployment landed | story `## Deployment state`, story `## PRs` (Deployed column) | epic `## Stories` (Deployment column) |
> | Decision finalized | story `## Decisions produced` (append link) | epic `## Progress ledger` (append entry), epic frontmatter `produces_decisions` (append permalink) |
>
> **Epic `## Acceptance` ticking is intentionally NOT in the table.** Matching a closed story to an epic-level AC requires semantic judgement that an LLM can't reliably make across reviewer models (AC text ⊇ story title? frontmatter `relates` link? semantic similarity?). Per the §4.1 "extract proposes, humans curate" posture, epic-AC ticks remain a human curation step done at story-close or epic-close. If field signal later shows a deterministic rule that works (e.g., explicit AC-permalink references in the story body), revisit in a follow-up minor.

**Step 7 (modified, basic-memory branch)** — `replace_section` operation explicit. The reviewer is told to use `edit_note` with explicit `operation` enum values: `replace_section` for dashboard sections (overwrites the whole section with a new body — correct because dashboard tables are reconstructed in full from `current_kb_excerpts`), `frontmatter_patch` for frontmatter cross-ref appends (e.g., adding to `produces_decisions`), and `insert_before_section` (keyed on the section AFTER the target) for the epic's append-only `## Progress ledger`. The progress ledger uses `insert_before_section` rather than `replace_section` because the ledger accumulates entries over an epic's lifetime — `replace_section` would clobber prior entries; targeting `insert_before_section` keyed on the section that FOLLOWS `## Progress ledger` (usually `## Open questions`) appends the new entry to the end of the ledger without touching what's already there. An implementing agent must not default to `replace_section` for ledger appends.

The remaining steps (3, 4, 5, 6) stay unchanged.

### 4. Adopter conventions & dogfood examples

#### 4.1 `docs/team-setup/project-knowledge-conventions.md`

Eight short sections, ~150 lines total. Each section answers one adopter question:

1. **When this pattern earns its keep.** Epic-scale, multi-agent, ticket-driven workflow. Single-author short-lived work shouldn't bother with the dashboards — the v0.6.0 5-type taxonomy is enough.
2. **One BM project per git repo.** With the monorepo exception (namespace per-product under `products/<product>/` interior path). Generic `<PROJECT>` placeholder; substitute when copying templates.
3. **Issue-ID format.** Pick one consistent format for `<TICKET-ID>` (Jira / Linear / GitHub Issues / etc.). Templates use `<TICKET-ID>` placeholder; adopters substitute.
4. **Folder convention.** `<PROJECT>/{epics,stories,decisions,modules,features,glossary}/<key>/main`. Folder-per-ticket allows arbitrary side-docs (charter, retro, sub-decisions) under the same ticket folder.
5. **Milestone events.** PR state change, deployment landed, decision finalized. Adopters can extend (e.g., security review passed) — the reviewer prompt is generic about what counts as a milestone; the doc lists the recommended set.
6. **Project-prefix bootstrap.** How to seed the first epic so future `kb_index` calls have a project prefix to infer. One-time setup; everything inherits from there.
7. **Tracker integration.** Generic `tracker_url` field on epic + story. Substitute your tracker's URL shape.
8. **Maintenance ownership.** Who updates what — extract proposes dashboards via milestone events; humans curate the durable layer; epic charters at kickoff; retros at close.

#### 4.2 `examples/project-knowledge/dogfood/`

Frozen snapshots of anti-tangent's actual KB at v0.7.0 release. Live versions live in anti-tangent's own BM project; the dogfood is re-snapshotted manually on major releases when the picture has materially shifted.

| File | Content |
|---|---|
| `dogfood/README.md` | Framing: "frozen snapshots at vX.Y.Z; live KB lives elsewhere; re-snapshot on major releases." |
| `dogfood/epics/gh-23/main.md` | v0.6.x project-knowledge epic with dashboard sections populated. |
| `dogfood/stories/gh-25/main.md` | Story for the shape-guard fix (#25 → v0.6.1). Single PR, single subtask. |
| `dogfood/decisions/0001-text-only-reviewer/main.md` | Seminal anti-tangent decision: reviewer is text-only. |
| `dogfood/modules/review-pipeline/main.md` | Coherent capability across mcpsrv + verdict + prompts + providers. |

No feature or glossary dogfood — the shipped templates cover those types, and INTEGRATION.md already documents anti-tangent's features and glossary terms; KB notes would duplicate.

### 5. Testing & validation

#### 5.1 Code-level tests

- All existing `internal/verdict/extract_parser_test.go` tests stay green.
- New test `TestParseExtract_AcceptsStoryType`: a valid extract output with one `type: "story"` proposal parses successfully.
- New regression test `TestParseExtract_AcceptsAllSixTypes`: table-driven; all six values (`decision`, `module`, `feature`, `glossary`, `epic`, `story`) round-trip through the parser.
- Strict-mode invariants in `schema_invariants_test.go` stay green.

#### 5.2 Prompt-layer tests

- Regenerate `extract_basic.golden` with `go test ./internal/prompts/... -update -run TestRenderExtract_Basic`. Diff reviewed by eye before commit.
- New golden test `TestRenderExtract_Milestone` + `extract_milestone.golden`: covers a completion envelope that mentions a PR-merged event in `final_diff` and carries an `epic_permalink`. The reviewer prompt should render the milestone table and the `replace_section` operation hint.

#### 5.3 Smoke test (manual)

After PR merges, before declaring v0.7.0 stable:

1. Write one `story` note in a real BM instance using the new template. Confirm BM accepts the frontmatter + permalink shape.
2. Call `extract_project_knowledge` against a small completion envelope mentioning "PR #42 merged" in `final_diff`. Verify extract emits a `replace_section` `bm_command` targeting an existing epic's `## Stories` section.
3. Apply the bm_command. Confirm the dashboard updates without clobbering unrelated content.

#### 5.4 Doc consistency

Standard repo grep pattern before commit:

```bash
rg "five types|5 types|5-type" INTEGRATION.md README.md docs/team-setup/   # expected: 0
rg "<PROJECT>" examples/project-knowledge/ docs/team-setup/                 # expected: many
rg "<PROJECT>" internal/ go.mod                                             # expected: 0
rg "<TICKET-ID>" examples/project-knowledge/ docs/team-setup/               # expected: many
rg "<TICKET-ID>" internal/                                                  # expected: 0
```

INTEGRATION.md's "Project knowledge (optional)" anchor (referenced from README.md:343) MUST still resolve. The deep-link target stays as-is.

**INTEGRATION.md size budget — hard check** (per the §1 callout):

```bash
size=$(wc -c < INTEGRATION.md)
if [ "$size" -ge 40000 ]; then
  echo "FAIL: INTEGRATION.md is $size bytes; must be < 40000"
  exit 1
fi
echo "OK: INTEGRATION.md is $size bytes (limit 40000)"
```

If this fails, trim other prose in the same PR until it passes. The v0.7.0 addition is small (mention of 6 types + one link); any growth beyond what the existing ~1.2 KB headroom absorbs is a signal that the new content needs tightening, not that the threshold can be relaxed.

#### 5.5 CHANGELOG + release verification

- `## [0.7.0] - YYYY-MM-DD` entry covers: new `story` type, conventions doc, `epic.md` rewrite, `decision`'s `story_origin` field, the four other template path updates, the dogfood directory, the extract.tmpl reviewer-prompt expansion.
- Branch name `version/0.7.0` matches the CHANGELOG entry — CI enforces this.
- Merge commit subject contains `[minor]` to drive the version-bump in release automation.

## Migration & backward compatibility

v0.6.x → v0.7.0 is strictly additive. Pre-v0.7.0 extract outputs (5-type proposals with v0.6.x permalink shapes — no project prefix, no folder-per-ticket) parse against the v0.7.0 parser without modification. Adopters who don't switch templates keep working; they just don't get the `story` type or dashboard features.

The v0.7.0 reviewer prompt assumes the project-prefix convention but degrades gracefully when `kb_index` lacks a consistent prefix — it falls back to the literal `<PROJECT>` placeholder and emits a `missing_index_entry` finding. Existing v0.6.x KBs without project prefixes will trigger this finding on their first v0.7.0 extract call; resolving it once (by establishing a project prefix in the KB) restores quiet operation.

No schema breaking changes; no env-var additions; no tool surface additions. Pure documentation, template, and reviewer-prompt expansion.

## Documentation deliverables

- `examples/project-knowledge/story.md` — new 6th template.
- `examples/project-knowledge/epic.md` — rewritten with dashboard sections.
- `examples/project-knowledge/decision.md` — permalink + `story_origin` field.
- `examples/project-knowledge/module.md`, `feature.md`, `glossary.md` — permalink-shape updates.
- `examples/project-knowledge/README.md` — 6-type taxonomy summary; module framing clarification; link to conventions doc + dogfood.
- `examples/project-knowledge/dogfood/` — four real anti-tangent example notes + a README.
- `docs/team-setup/project-knowledge-conventions.md` — adopter-tuning guide.
- `INTEGRATION.md` — small update to the "Project knowledge (optional)" section: mention 6 types; link to conventions doc. **The total file size MUST stay under 40,000 bytes** (the user-instructions warning threshold); see §1 callout and §5.4 hard check. Post-v0.6.2 trim sits at 38,835 bytes.
- `CHANGELOG.md` — `## [0.7.0]` entry.

## Non-goals (reaffirmed)

- No new finding categories. The existing six v0.6.0 categories cover every failure mode this design needs.
- No anti-tangent-side enforcement of issue-ID format. Templates show `<TICKET-ID>` placeholder; adopters substitute.
- No runtime BM integration. Anti-tangent remains text-only; extract emits paste-ready bm_commands.
- No concurrent-write resolution beyond `replace_section`. Sequential extract calls in our protocol are the norm.
- No dogfood-snapshot auto-update. Manual re-snapshot on major releases.
- No story-done milestone. PR-merged covers it.
- No `epic_origin` / `story_origin` extension to module or feature proposals in this minor. v0.6.0's deferred open question stays deferred; field signal decides.

## Open questions

None at design time. Two items deferred from v0.6.0 stay deferred:

- Whether `epic_origin` belongs on `module` and `feature` proposals too (deferred to field evidence).
- Whether `validate_completion` latency outliers warrant a soft `final_files` payload-size threshold (deferred per the v0.5.2 design; see issue #22 follow-up).
