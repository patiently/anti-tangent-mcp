# Project-Knowledge Conventions (v0.7.0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the v0.7.0 project-knowledge conventions layer per [`docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md`](../specs/2026-05-21-project-knowledge-conventions-design.md): a 6th `story` note type, an `epic.md` dashboard rewrite, project-prefixed folder-per-ticket permalink shape across all six types, a `story_origin` field on decisions, milestone-only dashboard updates in `extract.tmpl`, an adopter conventions doc, and a small directory of frozen-snapshot dogfood example notes.

**Architecture:** Strictly additive on top of v0.6.x. The schema enum gains one value (`story`); the reviewer prompt gains story-awareness + milestone-detection rules; the templates gain project-prefix and dashboard sections; one new operator doc + a frozen-snapshot dogfood directory ship alongside. Pre-v0.7.0 extract outputs (5-type proposals with v0.6.x permalink shapes) continue to parse without modification.

**Tech Stack:** Go, embedded `text/template` prompts, JSON Schema in `internal/verdict/`, fake-reviewer MCP integration tests, golden files in `internal/prompts/testdata/`.

---

## Cross-cutting constraints

These two constraints apply across multiple tasks; each task references them by name rather than re-explaining.

**(A) INTEGRATION.md must stay under 40,000 bytes.** Post-v0.6.2 trim the file sits at 38,835 bytes. The v0.7.0 add is a short mention of the 6-type taxonomy + one link to the new conventions doc. Task 9 verifies the total stays under 40000 via `wc -c < INTEGRATION.md` — if it overshoots, trim other prose in the same task until it passes. Same posture as v0.5.1 and v0.6.2.

**(B) Backwards compatibility.** v0.6.x extract outputs (5-type proposals with v0.6.x permalink shapes) MUST continue to parse against v0.7.0's `ParseExtract`. Task 1's regression test (`TestParseExtract_AcceptsAllSixTypes`) covers all six values, and a separate v0.6.x fixture test confirms the legacy shape still works.

---

## File Structure

The implementation touches these files. Each task below names its exact files in its **Files:** header.

```
internal/verdict/
├── extract.go                    [MODIFY]   add ProposalTypeStory constant
├── extract_schema.json           [MODIFY]   proposals[].type enum gains "story"
├── extract_parser.go             [MODIFY]   proposalWire switch-case gains ProposalTypeStory
└── extract_parser_test.go        [MODIFY]   add story-type acceptance + 6-types regression

internal/prompts/templates/
└── extract.tmpl                  [MODIFY]   recognise story; milestone-detection rules; replace_section guidance

internal/prompts/testdata/
├── extract_basic.golden          [REGEN]    after extract.tmpl changes
└── extract_milestone.golden      [NEW]      covers a milestone-triggered dashboard update

internal/prompts/
└── prompts_test.go               [MODIFY]   add TestRenderExtract_Milestone

examples/project-knowledge/
├── README.md                     [MODIFY]   document 6 types + folder-per-ticket pattern + adopter-doc link
├── epic.md                       [REWRITE]  dashboard sections + folder-per-ticket permalink
├── story.md                      [NEW]      6th type
├── decision.md                   [MODIFY]   optional story_origin field; permalink shape
├── module.md                     [MODIFY]   permalink shape
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

CHANGELOG.md                      [FINALIZE] reconcile the v0.7.0 stub against shipped surface
INTEGRATION.md                    [MODIFY]   mention 6 types; link to conventions doc; verify size < 40000
```

---

## Tasks run sequentially.

Tasks 1, 5, 9, and 10 modify the same `internal/verdict/`, `internal/prompts/`, `INTEGRATION.md`, and `CHANGELOG.md` files. Sequential execution avoids race conditions across subagent dispatches.

---

### Task 0: Verify branch + CHANGELOG stub state

**Goal:** Confirm the branch state matches what the plan assumes before any code change lands.

**Files:**
- Verify (read-only): `CHANGELOG.md`, `VERSION`, `docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md`

- [ ] **Step 1: Verify current branch**

Run: `git branch --show-current`
Expected: `version/0.7.0`

If the branch is wrong, run `git switch version/0.7.0`.

- [ ] **Step 2: Verify CHANGELOG stub is in place**

Run: `grep -n "^## \[0.7.0\] - 2026-05-21" CHANGELOG.md`
Expected: one match around line 8.

If missing, the v0.7.0 stub has not been applied — check the merge state from main.

- [ ] **Step 3: Verify the spec is committed and current**

Run: `git log --oneline docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md | head -3`
Expected: at least one commit referencing the spec (specifically `2f54bb4` or later in the chain).

Also run: `wc -l docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md`
Expected: ~419 lines.

- [ ] **Step 4: Verify baseline tests pass**

Run: `go test -race ./...`
Expected: PASS across all packages.

This is the baseline before changes; any failures here are pre-existing and block the plan.

---

### Task 1: Add the `story` type to the schema, parser, and tests

**Goal:** `internal/verdict/` accepts `"story"` as a valid `proposals[].type` value in `ParseExtract` outputs. Strict-mode invariants stay green. v0.6.x five-type proposals still parse.

**Acceptance criteria:**
- `internal/verdict/extract.go` declares `ProposalTypeStory ProposalType = "story"` alongside the existing five constants.
- `internal/verdict/extract_schema.json`'s `proposals[].type` enum gains `"story"`.
- `internal/verdict/extract_parser.go`'s `proposalWire` validation switch-case includes `ProposalTypeStory` as a valid action-type combination.
- `internal/verdict/extract_parser_test.go` gains two new tests: `TestParseExtract_AcceptsStoryType` (a `type: "story"` proposal round-trips through the parser) and `TestParseExtract_AcceptsAllSixTypes` (table-driven; all six values round-trip).
- All four `schema_invariants_test.go` strict-mode invariants stay green.
- `go test -race ./internal/verdict/...` passes.

**Non-goals:**
- Do not touch `extract.tmpl` here — that's Task 5.
- Do not modify the `epic.md` template — that's Task 3.

**Context:**
The five existing `ProposalType` constants live at `internal/verdict/extract.go`. The JSON Schema enum lives at `internal/verdict/extract_schema.json` in `proposals[].items.properties.type.enum`. The parser-side validation switch lives in `internal/verdict/extract_parser.go` inside the per-proposal loop in `ParseExtract`. The existing test patterns are at `internal/verdict/extract_parser_test.go` (look for `TestParseExtract_AcceptsCreate` / similar fixtures).

- [ ] **Step 1: Locate the existing ProposalType constant block**

Run: `grep -n "ProposalTypeDecision\|ProposalTypeEpic" internal/verdict/extract.go`
Expected: two matches showing the constant declarations on adjacent lines.

- [ ] **Step 2: Add the `ProposalTypeStory` constant**

In `internal/verdict/extract.go`, find the existing `const ( ... )` block declaring the five `ProposalType` constants and add `ProposalTypeStory ProposalType = "story"` at the end of the block:

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

Confirm with `grep -n "ProposalTypeStory" internal/verdict/extract.go` — expected: one match.

- [ ] **Step 3: Update the JSON Schema enum**

In `internal/verdict/extract_schema.json`, find the `proposals.items.properties.type.enum` array and add `"story"` to it. Before:

```json
"type":             { "type": "string", "enum": ["create", "update", "supersede"] },
"permalink":        { "type": "string", "minLength": 1 },
```

Wait — actually `type` is the proposal type field, not the action. Let me re-read. Look at the schema with `grep -n -A2 '"type"' internal/verdict/extract_schema.json | head -20` to identify the right enum.

The right line is the `proposals.items.properties.type.enum` — the value enum for the proposal's `type` field. Add `"story"` after `"epic"`:

```json
"type":             { "type": "string", "enum": ["decision", "module", "feature", "glossary", "epic", "story"] },
```

Verify with: `python3 -c "import json; s=json.load(open('internal/verdict/extract_schema.json')); print(s['properties']['proposals']['items']['properties']['type']['enum'])"`
Expected: `['decision', 'module', 'feature', 'glossary', 'epic', 'story']`

- [ ] **Step 4: Update the parser-side validation switch**

In `internal/verdict/extract_parser.go`, find the per-proposal validation switch on `p.Type` in `ParseExtract`. The current switch:

```go
switch p.Type {
case ProposalTypeDecision, ProposalTypeModule, ProposalTypeFeature, ProposalTypeGlossary, ProposalTypeEpic:
default:
    return ExtractResult{}, fmt.Errorf("proposal[%d]: invalid type %q", i, p.Type)
}
```

Add `ProposalTypeStory` to the accepted set:

```go
switch p.Type {
case ProposalTypeDecision, ProposalTypeModule, ProposalTypeFeature, ProposalTypeGlossary, ProposalTypeEpic, ProposalTypeStory:
default:
    return ExtractResult{}, fmt.Errorf("proposal[%d]: invalid type %q", i, p.Type)
}
```

- [ ] **Step 5: Write the failing test `TestParseExtract_AcceptsStoryType`**

Append to `internal/verdict/extract_parser_test.go`:

```go
func TestParseExtract_AcceptsStoryType(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [{
			"action": "create",
			"type": "story",
			"permalink": "monorepo/stories/ABC-42/main",
			"title": "Add network probe healthcheck",
			"frontmatter_json": "{\"status\":\"planned\"}",
			"body": "## Story brief\n\nProbe the SSE listener via socket-connect.",
			"body_patch": "",
			"rationale": "Documents the story for ticket ABC-42 under the conventions",
			"evidence_refs": ["completion_envelopes[0].summary"],
			"supersedes": []
		}],
		"bm_commands": [],
		"next_action": "Apply the story note before next milestone."
	}`)
	r, err := verdict.ParseExtract(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(r.Proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(r.Proposals))
	}
	if r.Proposals[0].Type != verdict.ProposalTypeStory {
		t.Fatalf("expected type story, got %q", r.Proposals[0].Type)
	}
}
```

- [ ] **Step 6: Write the regression test `TestParseExtract_AcceptsAllSixTypes`**

Append to the same test file:

```go
func TestParseExtract_AcceptsAllSixTypes(t *testing.T) {
	types := []struct {
		typ      string
		extra    string
	}{
		{"decision", ""},
		{"module", ""},
		{"feature", ""},
		{"glossary", ""},
		{"epic", ""},
		{"story", ""},
	}
	for _, tc := range types {
		t.Run(tc.typ, func(t *testing.T) {
			raw := []byte(`{
				"verdict": "pass",
				"findings": [],
				"proposals": [{
					"action": "create",
					"type": "` + tc.typ + `",
					"permalink": "monorepo/` + tc.typ + `s/abc/main",
					"title": "round-trip ` + tc.typ + `",
					"frontmatter_json": "{}",
					"body": "## Body\n\ncontent",
					"body_patch": "",
					"rationale": "round-trip regression check",
					"evidence_refs": ["completion_envelopes[0].summary"],
					"supersedes": []
				}],
				"bm_commands": [],
				"next_action": "noop"
			}`)
			r, err := verdict.ParseExtract(raw)
			if err != nil {
				t.Fatalf("type %q: parse error: %v", tc.typ, err)
			}
			if len(r.Proposals) != 1 || string(r.Proposals[0].Type) != tc.typ {
				t.Fatalf("type %q: round-trip failed, got %+v", tc.typ, r.Proposals)
			}
		})
	}
}
```

Note the permalink shape (`monorepo/<type>s/abc/main`) — that's the v0.7.0 project-prefixed folder-per-ticket convention.

- [ ] **Step 7: Run the new tests, then the full verdict suite**

Run: `go test -race -run "TestParseExtract_AcceptsStoryType|TestParseExtract_AcceptsAllSixTypes" ./internal/verdict/... -v`
Expected: both PASS.

Run: `go test -race ./internal/verdict/...`
Expected: PASS across all verdict tests, including the four `schema_invariants_test.go` invariants (which walk the updated `extract_schema.json` and verify it still satisfies strict-mode rules).

- [ ] **Step 8: Run the full suite**

Run: `go test -race ./...`
Expected: PASS across all packages. The v0.6.0 prime/extract tests should still work because the new enum value is additive.

- [ ] **Step 9: Commit task 1**

```bash
git add internal/verdict/extract.go internal/verdict/extract_schema.json internal/verdict/extract_parser.go internal/verdict/extract_parser_test.go
git commit -m "$(cat <<'EOF'
feat(verdict): add story type to extract proposals

Adds the 6th note type `story` to the extract proposal taxonomy:
ProposalTypeStory constant, schema enum entry, parser switch-case
acceptance, and two regression tests (TestParseExtract_AcceptsStoryType
+ TestParseExtract_AcceptsAllSixTypes covering all six values).

Strict-mode invariants stay green; v0.6.x five-type proposals
continue to parse without modification.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Add `examples/project-knowledge/story.md` template

**Goal:** The new `story.md` template lands with full frontmatter + body sections matching spec §2.1.

**Acceptance criteria:**
- `examples/project-knowledge/story.md` exists.
- Frontmatter has the exact eight fields from spec §2.1 (permalink, type, title, status, opened_at, closed_at, parent_epic, owners, tracker_url, tags).
- Body sections in the order specified: Story brief, Acceptance criteria, PRs, Subtasks, Deployment state, Decisions produced, Open questions.
- All placeholder text uses `<PROJECT>`, `<TICKET-ID>`, `<EPIC-TICKET-ID>`, `<YYYY-MM-DD>`, `<handle>`, `<one-line story title>`, etc. — no real values.

**Files:**
- Create: `examples/project-knowledge/story.md`

- [ ] **Step 1: Create the template file**

Write `examples/project-knowledge/story.md`:

```markdown
---
permalink: <PROJECT>/stories/<TICKET-ID>/main
type: story
title: <one-line story title>
status: planned                  # planned | in_progress | review | done | abandoned
opened_at: <YYYY-MM-DD>
closed_at: null
parent_epic: <PROJECT>/epics/<EPIC-TICKET-ID>/main   # null if standalone (rare)
owners: ["@<handle>"]
tracker_url: <issue URL or null>
tags: [story]
---

## Story brief

<one to two paragraphs: scope, why it's part of the parent epic, what success looks like>

## Acceptance criteria

- <testable criterion 1>
- <testable criterion 2>

## PRs

<!-- Multi-PR tracking. State enum: draft | review | merged | closed.
     Relationship enum: initial | follow-up to #N | supersedes #N | parallel with #N. -->

| PR | State | Branch | Relationship | Merged into | Deployed |
|---|---|---|---|---|---|
| #<N> | draft | <branch-name> | initial | — | none |

## Subtasks

- [ ] <subtask> (linked to #<PR> if applicable)
- [ ] <subtask>
- [x] <completed subtask>

## Deployment state

<!-- One row per environment the team tracks. Highest-env-reached shows up on the parent epic's `## Stories` row. -->

| Env | Latest landed | Date | Notes |
|---|---|---|---|
| staging | — | — | — |
| prod | — | — | — |

## Decisions produced

<!-- KB permalinks to decision notes produced under this story.
     Extract auto-populates this section by walking `story_origin` matches across decision notes. -->

- [<PROJECT>/decisions/<NNNN>-<slug>/main](<PROJECT>/decisions/<NNNN>-<slug>/main) — <one-sentence summary>

## Open questions

- <bullet>
```

- [ ] **Step 2: Sanity-check the file**

Run: `wc -l examples/project-knowledge/story.md`
Expected: ~50 lines.

Run: `grep -c "<PROJECT>\|<TICKET-ID>\|<EPIC-TICKET-ID>" examples/project-knowledge/story.md`
Expected: 4 or more (placeholders present).

Run: `grep -c "^## " examples/project-knowledge/story.md`
Expected: 7 (seven H2 body sections).

- [ ] **Step 3: Commit task 2**

```bash
git add examples/project-knowledge/story.md
git commit -m "$(cat <<'EOF'
docs(examples): add story.md note template (v0.7.0 6th type)

New `story` note template under examples/project-knowledge/.
Frontmatter covers issue ID, parent epic, owners, tracker URL.
Body sections cover the operational dashboard: story brief, AC,
multi-PR table (with state + relationship), subtasks checklist,
deployment-state table per env, decisions produced (KB permalinks),
and open questions. Permalinks use the v0.7.0 project-prefixed
folder-per-ticket shape <PROJECT>/stories/<TICKET-ID>/main.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Rewrite `examples/project-knowledge/epic.md` with dashboard sections

**Goal:** `epic.md` becomes a live operational dashboard per spec §2.2 while retaining charter + progress-ledger as supporting context.

**Acceptance criteria:**
- Existing `examples/project-knowledge/epic.md` is replaced.
- Frontmatter matches spec §2.2 (permalink uses `<PROJECT>/epics/<EPIC-TICKET-ID>/main`; gains `tracker_url`; touches_modules holds KB permalinks).
- Body sections in spec-defined order: Charter, In scope / Out of scope, Acceptance (epic-level), Stories, Open PRs, Progress ledger, Open questions.
- The "Note on terminal-status asymmetry" paragraph from spec §2.2 lands as a short callout near the end of the template.
- The Stories-table Deployment column note ("highest env reached: prod > staging > none") is included as an inline comment.

**Files:**
- Modify (full rewrite): `examples/project-knowledge/epic.md`

- [ ] **Step 1: Rewrite the file**

Replace the entire contents of `examples/project-knowledge/epic.md` with:

```markdown
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

## Charter

<two to three sentences naming the user-visible goal and the success criterion>

## In scope / Out of scope

- **In:** <bullet>
- **In:** <bullet>
- **Out:** <bullet>
- **Out:** <bullet>

## Acceptance (epic-level)

<!-- Per-story ACs live in story notes. Epic-AC ticks are a human-curation gesture
     at story-close or epic-close — extract does not auto-tick (matching closed
     stories to ACs requires semantic judgement that's not deterministic across
     reviewer models). -->

- [ ] <epic-level AC>
- [ ] <epic-level AC>
- [x] <ticked once the corresponding story closes and someone confirms the match>

## Stories

<!-- Deployment column shows the HIGHEST env reached by the story.
     Rank: prod > staging > none. Customise the rank for your env ladder
     in docs/team-setup/project-knowledge-conventions.md. -->

| Story | Status | Deployment | Tracker |
|---|---|---|---|
| [<TICKET-ID>](<PROJECT>/stories/<TICKET-ID>/main) — <story title> | planned | none | [<TICKET-ID>](<tracker URL>) |

## Open PRs

<!-- Aggregated across all stories in this epic. A PR enters this table when opened
     and leaves it when merged or closed. The story's own `## PRs` keeps the durable
     per-story history. -->

| PR | Story | State | Title |
|---|---|---|---|
| #<N> | [<TICKET-ID>](<PROJECT>/stories/<TICKET-ID>/main) | draft\|review | <PR title> |

## Progress ledger

<!-- Append-only. One entry per milestone. The extract tool writes here via
     edit_note(insert_before_section, "## Open questions") so existing entries
     are never clobbered. -->

- <YYYY-MM-DD> — <one-line summary of the milestone>

## Open questions

- <bullet>

---

**Note on terminal status.** Stories terminate with `status: done`; epics terminate with `status: closed`. The asymmetry is deliberate — engineering finishes a story; product management closes an epic once all stories are done AND any post-merge follow-through (deployment, retro, AC confirmation) has landed.
```

- [ ] **Step 2: Sanity-check**

Run: `grep -c "^## " examples/project-knowledge/epic.md`
Expected: 7 (seven H2 body sections).

Run: `grep -n "Note on terminal status" examples/project-knowledge/epic.md`
Expected: one match near the end of the file.

Run: `grep -c "<PROJECT>\|<EPIC-TICKET-ID>\|<TICKET-ID>" examples/project-knowledge/epic.md`
Expected: many (placeholders present).

- [ ] **Step 3: Commit task 3**

```bash
git add examples/project-knowledge/epic.md
git commit -m "$(cat <<'EOF'
docs(examples): rewrite epic.md with live-dashboard sections (v0.7.0)

The v0.6.0 epic template was a charter + progress-ledger doc. v0.7.0
makes the epic note a live operational dashboard while keeping the
charter as supporting context.

New/changed sections:
- `## Acceptance (epic-level)` — checklist for human curation.
- `## Stories` — table of stories in the epic with status +
  highest-env-deployed + tracker link.
- `## Open PRs` — aggregated open PRs across all stories.
- `## Progress ledger` — append-only ledger written by extract via
  edit_note(insert_before_section).

Frontmatter gains `tracker_url`; `touches_modules` holds KB
permalinks (not Go-package paths); permalink uses the v0.7.0
project-prefixed folder-per-ticket shape <PROJECT>/epics/<TICKET-ID>/main.

Added "Note on terminal status" callout explaining the deliberate
done/closed asymmetry between stories and epics.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Update `decision.md`, `module.md`, `feature.md`, `glossary.md`, `README.md`

**Goal:** The four remaining note-type templates adopt the v0.7.0 project-prefixed permalink shape. The decision template gains an optional `story_origin` field. The `README.md` documents the 6-type taxonomy and points at the new conventions doc + dogfood directory.

**Acceptance criteria:**
- `decision.md` permalink is `<PROJECT>/decisions/<NNNN>-<slug>/main`; gains `story_origin` frontmatter field after `epic_origin`; permalink-list fields (`supersedes`, `relates`) use the new shape in placeholder.
- `module.md`, `feature.md`, `glossary.md` permalinks adopt `<PROJECT>/<type>/<key>/main` shape; cross-ref list fields use permalink shapes.
- `README.md` documents all 6 types (the existing 5 + story), the folder-per-ticket pattern, and links to the new conventions doc + dogfood directory.

**Files:**
- Modify: `examples/project-knowledge/decision.md`
- Modify: `examples/project-knowledge/module.md`
- Modify: `examples/project-knowledge/feature.md`
- Modify: `examples/project-knowledge/glossary.md`
- Modify: `examples/project-knowledge/README.md`

- [ ] **Step 1: Update `decision.md`**

Replace the existing contents of `examples/project-knowledge/decision.md` with:

```markdown
---
permalink: <PROJECT>/decisions/<NNNN>-<slug>/main
type: decision
title: <one-line title>
status: proposed                 # proposed | accepted | superseded | rejected
supersedes: []                   # list of <PROJECT>/decisions/<NNNN>-<slug>/main permalinks
proposed_by: "@<handle>"
decided_at: <YYYY-MM-DD>
epic_origin: <PROJECT>/epics/<EPIC-TICKET-ID>/main   # optional; epic-level origin (no story owner)
story_origin: <PROJECT>/stories/<TICKET-ID>/main      # optional; story-scoped origin (preferred when set)
relates: []                      # list of permalinks
tags: []
---

## Context

<the constraint or pressure that forced a choice>

## Decision

<what we chose, in one paragraph>

## Consequences

- <bullet>

## Alternatives considered

- <bullet>
```

**Note on origins:** in practice set EITHER `story_origin` OR `epic_origin`, not both. When `story_origin` is set, the story's own `parent_epic` provides the epic context transitively. Set `epic_origin` only when the decision was made at the epic level without a specific owning story.

- [ ] **Step 2: Update `module.md`**

Replace the existing contents with:

```markdown
---
permalink: <PROJECT>/modules/<module-name>/main
type: module
title: <one-line title>
status: stable                   # experimental | stable | deprecated | removed
last_changed_in: <X.Y.Z>
relates_features: []             # list of <PROJECT>/features/<slug>/main permalinks
shaped_by_decisions: []          # list of <PROJECT>/decisions/<NNNN>-<slug>/main permalinks
tags: [module]
---

## Purpose

<one sentence on what this module exists to do>

## Invariants

- <bullet>

## Conventions

- <bullet>

## Touch-points

- <module path or capability boundary>
```

Modules describe **coherent capabilities** (user-facing surface), not 1:1 Go-package mappings. A single module note can span multiple Go packages that jointly implement one user-facing capability. The README documents this framing.

- [ ] **Step 3: Update `feature.md`**

Replace with:

```markdown
---
permalink: <PROJECT>/features/<slug>/main
type: feature
title: <one-line title>
surface: mcp_tool                # mcp_tool | cli | env_var | protocol | other
status: stable                   # experimental | stable | deprecated | removed
since_version: <X.Y.Z>
last_changed_in: <X.Y.Z>
relates_modules: []              # list of <PROJECT>/modules/<name>/main permalinks
shaped_by_decisions: []          # list of <PROJECT>/decisions/<NNNN>-<slug>/main permalinks
tags: []
---

## What it does

<one paragraph user-visible description>

## How it works

<one paragraph architectural summary; link decisions / modules by permalink>

## Recent material changes

- <X.Y.Z> — <one line; details live in the linked decision>

## Related

- <links>
```

- [ ] **Step 4: Update `glossary.md`**

Replace with:

```markdown
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

- [ ] **Step 5: Rewrite `README.md`**

Replace the existing contents of `examples/project-knowledge/README.md` with:

```markdown
# Project-knowledge note templates

Six templates seed the project-knowledge schema used by the optional v0.6.0+
`prime_project_knowledge` and `extract_project_knowledge` tools. They are
markdown with [Basic Memory](https://github.com/basicmachines-co/basic-memory)
frontmatter; copy a template into your shared KB and fill it in.

**Authoritative spec:** [`docs/superpowers/specs/2026-05-18-project-knowledge-design.md`](../../docs/superpowers/specs/2026-05-18-project-knowledge-design.md) (v0.6.0 base) + [`docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md`](../../docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) (v0.7.0 conventions layer).

**Adopter tuning:** [`docs/team-setup/project-knowledge-conventions.md`](../../docs/team-setup/project-knowledge-conventions.md) covers issue-ID format, folder convention, milestone events, and the per-project tuning loop. Read this before adopting.

**Dogfood examples:** [`dogfood/`](dogfood/) contains frozen-snapshot real notes from anti-tangent's own KB at v0.7.0. Study them for shape and rationale; do not copy verbatim.

## Six types in two layers

- **Durable reference layer** (timeless / slow-evolving): `decision`, `module`, `feature`, `glossary`. Survives epics.
- **Operational layer** (time-bounded; live state during work): `epic`, `story`. Both terminate at completion — epics with `status: closed` (PM gesture), stories with `status: done` (engineering gesture).

## Permalink convention

All six types use the shape `<PROJECT>/<type>/<key>/main`, where:
- `<PROJECT>` is the BM project name (one BM project per git repo; see the conventions doc).
- `<type>` is one of `decisions`, `modules`, `features`, `glossary`, `epics`, `stories`.
- `<key>` is either a slug (decisions, modules, features, glossary) or a ticket ID (epics, stories).
- The trailing `/main` allows arbitrary side-docs (charter, retro, sub-decisions) to live in the same folder.

## Modules describe coherent capabilities, not Go packages

A `module` note describes one user-facing capability — what the module DOES — and may span multiple Go packages. Example from anti-tangent: `modules/review-pipeline/main` covers `internal/mcpsrv` + `internal/verdict` + `internal/prompts` + `internal/providers` jointly implementing the `validate_X` surface. Avoid the temptation to write one module note per Go package; the user-facing surface is the right granularity.

## Maintenance ownership

| Type | Author at birth | Updated by |
|---|---|---|
| `epic` | Human at kickoff | Mostly automated (extract appends ledger via milestone events; humans edit open questions and AC checklist) |
| `story` | Human at story-open | Mostly automated (extract updates dashboard sections on milestone events; humans set `status: done` to signal terminal merge) |
| `decision` | Drafted by extract → reviewed by human → merged | Append-only; new decisions supersede old ones |
| `module` | Human (or seeded from a spec) | Mostly human; extract proposes invariant/convention edits when it sees drift |
| `feature` | Human (or seeded from a spec) | Mostly human; extract proposes "Recent material changes" entries |
| `glossary` | Opportunistic (human or extract) | Opportunistic |
```

- [ ] **Step 6: Verify all five files**

Run: `for f in decision module feature glossary README; do echo "=== $f.md ==="; head -5 examples/project-knowledge/$f.md; done`

Expected: each file starts with the v0.7.0 shape — `decision.md` / `module.md` / `feature.md` / `glossary.md` show YAML frontmatter beginning with `permalink: <PROJECT>/`; `README.md` starts with `# Project-knowledge note templates`.

Run: `grep -l "story_origin" examples/project-knowledge/`
Expected: `examples/project-knowledge/decision.md` (one match).

Run: `grep -c "story" examples/project-knowledge/README.md`
Expected: several (README mentions the 6th type).

- [ ] **Step 7: Commit task 4**

```bash
git add examples/project-knowledge/decision.md examples/project-knowledge/module.md examples/project-knowledge/feature.md examples/project-knowledge/glossary.md examples/project-knowledge/README.md
git commit -m "$(cat <<'EOF'
docs(examples): update remaining 4 templates + README for v0.7.0

- decision.md: gains optional story_origin frontmatter field; permalink + cross-ref lists use <PROJECT>/<type>/<key>/main shape.
- module.md, feature.md, glossary.md: permalinks adopt the v0.7.0 shape; cross-ref list fields use permalink shapes.
- README.md: rewritten to document 6 types in two layers (durable
  reference + operational), the permalink convention, the modules-
  describe-capabilities framing, the maintenance-ownership table,
  and links to the conventions doc + dogfood directory.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Update `extract.tmpl` reviewer-prompt for milestone detection

**Goal:** `internal/prompts/templates/extract.tmpl` recognises the `story` type, infers the project prefix from `kb_index`, and proposes dashboard-update bm_commands only on milestone events per spec §3.2.

**Acceptance criteria:**
- Step 1 enumerates the canonical milestone events: PR opened (draft or ready), PR transitioning state (draft→review, review→merged, review→closed-without-merge), deployment landed in any env, decision finalized (status: accepted). Story-done is NOT a separate milestone — it's the natural consequence of the last PR merging.
- Step 2 lists 6 valid types including `story`.
- A new Step 2a covers project-prefix inference from `kb_index` permalinks with the tie-break rule (task-spec/plan_text-referenced → alphabetical → `missing_index_entry` finding); explicitly notes `missing_index_entry` is reused from prime-side vocabulary.
- A new Step 3a covers milestone-only dashboard-update rule with the full milestone-to-sections table (including `PR closed without merge` and the explicit terminal-merge detection rule pinned to caller's `status: done` signal), plus the explicit "Epic `## Acceptance` ticking is intentionally NOT in the table" note.
- Step 7 (the basic-memory branch) names the three `edit_note` operations: `replace_section`, `frontmatter_patch`, `insert_before_section`, and explains why progress-ledger appends use `insert_before_section` keyed on `## Open questions`.

**Files:**
- Modify: `internal/prompts/templates/extract.tmpl`

**Context:**
The existing `extract.tmpl` (post-v0.6.2) has a `## What to do` section with steps 1–7 documented in the v0.6.0 design's §3 and the v0.6.2 INTEGRATION.md trim. Look at the current file for the precise step boundaries before editing.

- [ ] **Step 1: Read the current `extract.tmpl`**

Run: `wc -l internal/prompts/templates/extract.tmpl`
Expected: ~140 lines.

Run: `grep -n "^## " internal/prompts/templates/extract.tmpl`
Expected: section headings including `## Completion envelopes`, `## What to do`, `## Output schema`.

Run: `grep -n "^[0-9]\." internal/prompts/templates/extract.tmpl`
Expected: numbered steps 1–7 inside `## What to do`.

- [ ] **Step 2: Replace Step 1's milestone enumeration**

Find Step 1 in `## What to do`. Its current text (post-v0.6.0) explains "durable knowledge worth capturing". Replace it with text that distinguishes durable knowledge from milestone events:

```
1. Read each completion envelope as the authoritative record of what just
   shipped. Identify two distinct kinds of signal:
   - **Durable knowledge** worth capturing as a new note: an architectural
     decision, a module's invariants, a new feature's behavior, a glossary
     term. Always proposable; not gated on milestones.
   - **Milestone events** that warrant a dashboard update on an existing
     `epic` or `story` note. A milestone is ONE of: a PR opened (draft or
     ready), a PR transitioning state (`draft → review`, `review → merged`,
     `review → closed-without-merge`), a deployment landing in any
     environment, or a decision finalizing (`status: accepted`). Story-done
     is NOT a separate milestone — it's the natural consequence of the last
     PR for a story merging.
```

- [ ] **Step 3: Update Step 2's type list**

Step 2 currently lists 5 valid types. Update the type-enum line to include `story`:

```
   - `type`: one of `decision`, `module`, `feature`, `glossary`, `epic`, `story`.
```

- [ ] **Step 4: Insert new Step 2a — project-prefix inference**

After Step 2 (and before Step 3), insert:

```
2a. **Permalink shape and project-prefix inference.** All permalinks use
    the shape `<PROJECT>/<type>/<slug-or-ticket-id>/main`. Infer
    `<PROJECT>` from the most common prefix in `kb_index` permalinks.
    Tie-break: if two or more prefixes are tied on count, prefer the
    prefix that matches a repo or project name referenced in the task
    spec or `plan_text` (when present); failing that, take the
    alphabetically first prefix. If `kb_index` is empty or no prefix
    can be inferred even with tie-break, fall back to the literal
    placeholder `<PROJECT>` in your proposal permalinks AND emit one
    `missing_index_entry` (severity: major) finding naming the absence
    — callers must then resolve the placeholder before applying the
    bm_commands.

    Note: `missing_index_entry` is reused from the v0.6.0 prime-side
    finding vocabulary — no new finding category is introduced. The
    same finding fires from both prime and extract, on different
    triggers.
```

- [ ] **Step 5: Insert new Step 3a — milestone-only dashboard-update rule**

After Step 3 (insufficient_evidence rule) and before Step 4 (redundant_proposal rule), insert:

```
3a. **Dashboard updates require a milestone.** If a completion envelope
    surfaces a milestone, propose `action: update` on the relevant
    `epic` or `story` note with a `replace_section` (or
    `frontmatter_patch` / `insert_before_section`) bm_command that
    rewrites the affected section or frontmatter field verbatim. To
    produce the new section content, use the matching entry from
    `current_kb_excerpts` to read the current section, identify the
    row(s) that change, and emit the full updated section text (NOT a
    diff). If `current_kb_excerpts` has no entry for the target note,
    emit an `insufficient_evidence` finding rather than fabricating a
    section body.

    **Detecting "last PR for story":** the reviewer treats a PR merge
    as the story's terminal PR ONLY when the story's frontmatter
    `status` in `current_kb_excerpts` is already `done`. The caller —
    the agent or human who closes the story — sets `status: done` on
    the story note BEFORE invoking extract for the terminal-merge
    milestone. This makes story closure an explicit caller gesture
    rather than an LLM-inferred heuristic; the reviewer's role is to
    amplify the closure into all the dashboard consequences. The
    reviewer does NOT scan `## PRs` rows or `## Subtasks` checkboxes
    to infer terminality — those signals are not authoritative across
    reviewer models. If `status` is anything other than `done` when a
    PR merges (including `in_progress`, `review`, or absent), the
    merge falls into the "PR merged, NOT last for story" row instead.

    Map of milestone → sections to update:

    | Milestone | Story-side update | Epic-side update |
    |---|---|---|
    | PR opened or state change (still open) | story `## PRs` (row state) | epic `## Open PRs` (add/update row) |
    | PR closed without merge | story `## PRs` (row state → closed) | epic `## Open PRs` (drop row) |
    | PR merged, NOT last for story | story `## PRs` (row state → merged) | epic `## Open PRs` (drop row) |
    | PR merged + last for story (caller has set story `status: done`) | story frontmatter (`status: done`, `closed_at: <YYYY-MM-DD>`), story `## PRs` (row state → merged), story `## Deployment state` (if landed) | epic `## Stories` (status → done), epic `## Open PRs` (drop row) |
    | Deployment landed | story `## Deployment state`, story `## PRs` (Deployed column) | epic `## Stories` (Deployment column — highest env reached) |
    | Decision finalized | story `## Decisions produced` (append link) | epic `## Progress ledger` (append entry), epic frontmatter `produces_decisions` (append permalink) |

    **Epic `## Acceptance` ticking is intentionally NOT in the table.**
    Matching a closed story to an epic-level AC requires semantic
    judgement that an LLM can't reliably make across reviewer models.
    Epic-AC ticks remain a human curation step done at story-close or
    epic-close.
```

- [ ] **Step 6: Update Step 7 (basic-memory branch) — operation guidance**

Find the existing Step 7 inside the `{{if .KBStoreIsBasicMemory}}` branch. Replace the operation-guidance prose with:

```
   For dashboard sections, use `edit_note` with `operation:
   replace_section`. This overwrites the whole section with the new
   body — correct because dashboard tables are reconstructed in full
   from `current_kb_excerpts`.

   For frontmatter cross-ref appends (e.g., adding to
   `produces_decisions`), use `operation: frontmatter_patch`.

   For the epic's append-only `## Progress ledger`, use `operation:
   insert_before_section` keyed on the section that FOLLOWS the
   ledger (usually `## Open questions`). The ledger uses
   `insert_before_section` rather than `replace_section` because the
   ledger accumulates entries over an epic's lifetime —
   `replace_section` would clobber prior entries. Do NOT default to
   `replace_section` for ledger appends.

   For the full mapping from extract's emitted `bm_commands` shape to
   BM v0.21.1's literal `write_note` / `edit_note` MCP signatures, see
   INTEGRATION.md § "Applying bm_commands to BM v0.21.1" (added in
   v0.6.2).
```

- [ ] **Step 7: Verify the file**

Run: `wc -l internal/prompts/templates/extract.tmpl`
Expected: increased from ~140 to ~200 lines.

Run: `grep -c "story" internal/prompts/templates/extract.tmpl`
Expected: many matches (story type and milestone-related).

Run: `grep -n "missing_index_entry\|replace_section\|insert_before_section\|frontmatter_patch" internal/prompts/templates/extract.tmpl`
Expected: each operation mentioned.

- [ ] **Step 8: Commit task 5**

```bash
git add internal/prompts/templates/extract.tmpl
git commit -m "$(cat <<'EOF'
feat(prompts): teach extract.tmpl the v0.7.0 conventions

Five reviewer-prompt updates per spec §3.2:

- Step 1: distinguish durable knowledge from milestone events;
  enumerate the canonical milestone set (PR state changes,
  deployment, decision finalized). Story-done is the consequence
  of the last PR merging, not a separate milestone.
- Step 2: type enum gains `story` (6th type).
- New Step 2a: project-prefix inference from kb_index permalinks
  with tie-break rule (task-spec/plan_text-referenced →
  alphabetical → missing_index_entry finding). Reuses the v0.6.0
  prime-side missing_index_entry — no new category.
- New Step 3a: milestone-only dashboard-update rule with the
  full milestone→sections table (PR opened/closed/merged-not-last/
  merged-last/deployment/decision). Detection of "last PR for
  story" pinned to caller's explicit `status: done` signal in
  current_kb_excerpts; reviewer does NOT scan PR rows or subtask
  checkboxes. Epic AC-ticking is intentionally NOT auto.
- Step 7: edit_note operation guidance — replace_section for
  dashboard sections, frontmatter_patch for cross-ref appends,
  insert_before_section for the epic's append-only progress
  ledger (keyed on `## Open questions` to avoid clobbering).
  Points to INTEGRATION.md §"Applying bm_commands" for the BM
  v0.21.1 MCP-signature mapping.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Regenerate `extract_basic.golden` + add milestone golden test

**Goal:** The existing extract golden test refreshes against the new template; a new test exercises the milestone-detection path with an explicit milestone-bearing completion envelope.

**Acceptance criteria:**
- `internal/prompts/testdata/extract_basic.golden` is regenerated and its diff against the previous version reflects only the v0.7.0 template additions (new Step 2a, Step 3a, modified Step 7 — no unrelated changes).
- A new test `TestRenderExtract_Milestone` exists in `internal/prompts/prompts_test.go`.
- A new golden file `internal/prompts/testdata/extract_milestone.golden` is generated and committed.
- `go test -race ./internal/prompts/...` passes.

**Files:**
- Modify: `internal/prompts/prompts_test.go`
- Regenerate: `internal/prompts/testdata/extract_basic.golden`
- Create: `internal/prompts/testdata/extract_milestone.golden`

- [ ] **Step 1: Regenerate `extract_basic.golden`**

Run: `go test ./internal/prompts/... -update -run TestRenderExtract_Basic`
Expected: PASS; the golden file is rewritten.

- [ ] **Step 2: Review the diff**

Run: `git diff internal/prompts/testdata/extract_basic.golden | head -100`

Expected diff: only the Step 1 milestone enumeration prose change, new Step 2a, new Step 3a (with table), and modified Step 7 (operation guidance). NO unrelated changes (no schema fragments, no completion envelope content drift).

If the diff shows unrelated changes, the template edit in Task 5 introduced something that shouldn't be there — fix and re-run.

- [ ] **Step 3: Add `TestRenderExtract_Milestone`**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderExtract_Milestone(t *testing.T) {
	envelope := prompts.CompletionEnvelopeForExtract{
		TaskTitle:   "Land network-probe healthcheck",
		Verdict:     "pass",
		Summary:     "Implemented the network-probe healthcheck variant per spec §13.3. PR #42 merged into main; deploy to staging triggered.",
		FinalDiff:   "diff --git a/docs/team-setup/basic-memory-shared-vm.md b/docs/team-setup/basic-memory-shared-vm.md\n@@ -100,3 +100,5 @@\n+  healthcheck:\n+    test: [...]",
		Findings:    []verdict.Finding{},
		TestEvidence: "go test -race ./... PASS",
	}
	in := prompts.ExtractInput{
		CompletionEnvelopes: []prompts.CompletionEnvelopeForExtract{envelope},
		PlanText:            "",
		KBIndex: []prompts.KBIndexEntry{
			{Permalink: "monorepo/epics/ABC-100/main", Type: "epic", Title: "v0.7.0 healthcheck rework", Summary: "Epic covering the BM Docker healthcheck variant + per-story tracking", Tags: []string{"epic"}},
			{Permalink: "monorepo/stories/ABC-101/main", Type: "story", Title: "Story for the network-probe healthcheck", Summary: "Single PR (PR #42); subtask: write the python socket probe", Tags: []string{"story"}},
		},
		CurrentKBExcerpts: map[string]string{
			"monorepo/epics/ABC-100/main": "## Stories\n\n| Story | Status | Deployment | Tracker |\n|---|---|---|---|\n| [ABC-101](monorepo/stories/ABC-101/main) — Story title | in_progress | none | [ABC-101](https://example.com/ABC-101) |\n",
			"monorepo/stories/ABC-101/main": "## PRs\n\n| PR | State | Branch | Relationship | Merged into | Deployed |\n|---|---|---|---|---|---|\n| #42 | review | story/probe | initial | — | none |\n",
		},
		EpicPermalink:        "monorepo/epics/ABC-100/main",
		KBStoreIsBasicMemory: true,
	}
	out, err := prompts.RenderExtract(in)
	require.NoError(t, err)
	golden(t, "extract_milestone", out.System+"\n---USER---\n"+out.User)
}
```

- [ ] **Step 4: Materialize the new golden**

Run: `go test ./internal/prompts/... -update -run TestRenderExtract_Milestone`
Expected: PASS; the new golden `internal/prompts/testdata/extract_milestone.golden` is written.

- [ ] **Step 5: Inspect the new golden**

Run: `cat internal/prompts/testdata/extract_milestone.golden | head -80`

Verify the rendered prompt contains:
- The completion envelope's `summary` mentioning "PR #42 merged".
- The current_kb_excerpts content for both the epic and story.
- The milestone-table from Step 3a.
- The replace_section operation hint from Step 7.

If anything looks off in the rendered prompt, the template edit in Task 5 has a bug — fix Task 5 and re-run both update commands.

- [ ] **Step 6: Run the full prompts suite**

Run: `go test -race ./internal/prompts/...`
Expected: PASS.

- [ ] **Step 7: Commit task 6**

```bash
git add internal/prompts/prompts_test.go internal/prompts/testdata/extract_basic.golden internal/prompts/testdata/extract_milestone.golden
git commit -m "$(cat <<'EOF'
test(prompts): regen extract goldens + add milestone path

- Regenerated extract_basic.golden against the v0.7.0 template
  changes (Task 5). Diff covers only Step 1/2a/3a/7 modifications.
- New TestRenderExtract_Milestone with a completion envelope
  carrying a PR-merged signal + current_kb_excerpts of the
  target epic and story. Exercises the milestone-detection path
  and produces extract_milestone.golden as a pinned reference for
  the rendered prompt shape.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Create `docs/team-setup/project-knowledge-conventions.md`

**Goal:** Operator-facing adopter doc covering the 8 topics from spec §4.1, with concrete paste-ready guidance for issue-ID format, folder convention, milestone events, project-prefix bootstrap (fresh + migration), tracker integration, and maintenance ownership.

**Acceptance criteria:**
- `docs/team-setup/project-knowledge-conventions.md` exists.
- File has 8 H2 sections matching spec §4.1 (When this earns its keep / One BM project per git repo / Issue-ID format / Folder convention / Milestone events / Project-prefix bootstrap (including v0.6.x migration) / Tracker integration / Maintenance ownership).
- Section 6 includes BOTH fresh-start guidance AND the v0.6.x→v0.7.0 migration path (bulk move_note rename OR leave-legacy-and-let-prefix-vote-stabilise) per spec §"Migration & backward compatibility".
- File is < 200 lines.

**Files:**
- Create: `docs/team-setup/project-knowledge-conventions.md`

- [ ] **Step 1: Create the file**

Write `docs/team-setup/project-knowledge-conventions.md`:

```markdown
# Project-knowledge conventions

How to tune the v0.6.0+ project-knowledge feature for your team's project structure. Pairs with [`examples/project-knowledge/`](../../examples/project-knowledge/) templates and the v0.6.0 + v0.7.0 design specs.

## 1. When this pattern earns its keep

The conventions in this doc target **epic-scale, multi-agent, ticket-driven workflows** — teams running multi-week initiatives broken into tracked stories, with multiple PRs per story, agents and humans both contributing. If you're a single-author developer on a short-lived project, the v0.6.0 five-type taxonomy (decision / module / feature / glossary / epic) is enough — the operational dashboards in `epic.md` and `story.md` are overhead you don't need.

Pick the operational layer (`epic` + `story` dashboards) when:
- You track work as tickets in Jira / Linear / GitHub Issues / similar.
- Multiple PRs can land under one ticket (initial + follow-up).
- You want a single KB query to answer "where are we on epic X?"
- Multiple agents may pick up work without shared session context.

Skip it when:
- You're a single human doing small commits without ticket scaffolding.
- Your work doesn't accumulate; each PR is independent.

## 2. One BM project per git repo

Recommended pattern: **one BM project per git repo**. Name the BM project after the repo so the project-prefix in permalinks is self-explanatory.

**Exception: monorepos.** If your repo is a monorepo holding multiple products, run one BM project for the monorepo and namespace per-product notes under `products/<product-name>/` interior directories:

- `<MONOREPO>/products/<product>/decisions/<NNNN>-<slug>/main` — product-scoped decisions.
- `<MONOREPO>/products/<product>/stories/<TICKET-ID>/main` — product-scoped stories.
- `<MONOREPO>/decisions/<NNNN>-<slug>/main` — monorepo-wide decisions (rare; reserve for genuinely cross-product policy).

Substitute `<PROJECT>` in the shipped templates with the BM project name you pick.

## 3. Issue-ID format

Pick **one** consistent format for `<TICKET-ID>` across your KB. Common shapes:

- **Jira / Linear:** `ABC-123` (team prefix + number).
- **GitHub Issues:** `gh-NNN` or `issue-NNN` (avoid `#NNN` in paths — the `#` is fragile across some tools).
- **YouTrack:** `XX-NNN`.

The templates use `<TICKET-ID>` placeholder; adopters substitute. Don't mix formats within the same BM project — the project-prefix inference in `extract_project_knowledge` treats the entire permalink shape uniformly.

## 4. Folder convention

All six note types use `<PROJECT>/<type>/<key>/main` where `<key>` is either a slug (`decisions`, `modules`, `features`, `glossary`) or a ticket ID (`epics`, `stories`). The trailing `/main.md` allows arbitrary side-docs per ticket:

- `<PROJECT>/epics/<TICKET-ID>/main.md` — the live operational dashboard.
- `<PROJECT>/epics/<TICKET-ID>/charter.md` — extended charter (optional).
- `<PROJECT>/epics/<TICKET-ID>/retro.md` — retrospective written at epic-close.
- `<PROJECT>/stories/<TICKET-ID>/main.md` — story dashboard.
- `<PROJECT>/stories/<TICKET-ID>/postmortem.md` — post-incident note if applicable.

Extract's dashboard updates target the `main.md` files; side-docs are human-curated.

## 5. Milestone events

The reviewer prompt for `extract_project_knowledge` recognises these milestone events:

- **PR opened** (any state — draft or ready).
- **PR transitions state**: `draft → review`, `review → merged`, `review → closed-without-merge`.
- **Deployment lands** in any environment (staging, prod, etc.).
- **Decision finalizes** (`status: accepted` in the decision's frontmatter).

When a completion envelope surfaces one of these milestones, extract proposes a `replace_section` (or related) bm_command to update the relevant epic / story dashboard section.

**To extend with your own milestones:** the reviewer prompt is generic about what counts; if your team needs (say) "security review passed" as a milestone, mention it explicitly in your completion envelope's `summary` and the reviewer will recognise it as a milestone-like signal. Anti-tangent doesn't enforce a fixed milestone enumeration — the above list is the recommended default.

## 6. Project-prefix bootstrap (fresh setup AND v0.6.x migration)

The reviewer infers `<PROJECT>` from the most common prefix in `kb_index` permalinks. To bootstrap or migrate, follow the appropriate path below.

### Fresh setup (new KB)

1. Pick your `<PROJECT>` name (per §2).
2. Write your first `epic` note at `<PROJECT>/epics/<FIRST-TICKET>/main` using the shipped template.
3. From this point on, all `extract_project_knowledge` calls will infer the prefix correctly.

### Migration from v0.6.x (existing KB without project prefixes)

If your existing notes use the v0.6.x shape (`decisions/0042-x` — no project prefix), you have two migration paths:

**Path A — Bulk rename via BM `move_note` (recommended for small KBs, < 50 notes):**

```
move_note(permalink="decisions/0042-x", new_permalink="<PROJECT>/decisions/0042-x/main")
move_note(permalink="modules/foo",     new_permalink="<PROJECT>/modules/foo/main")
… repeat for each note …
```

Run via your BM MCP client. One-time setup; future `extract` calls see a consistent prefix immediately.

**Path B — Leave legacy notes; tag new notes with the prefix (recommended for larger KBs):**

Write all NEW notes under the v0.7.0 shape; let the legacy notes keep their old permalinks. The `missing_index_entry` finding will fire on the first `extract_project_knowledge` call (because legacy notes dilute the prefix-count vote). Once enough new-shape notes accumulate, the prefix becomes the majority and inference stabilises. Operationally noisier than Path A but avoids the bulk-rename step.

Either way, the v0.7.0 reviewer never modifies existing notes' permalinks — it proposes NEW notes under the new shape and emits `replace_section` updates against existing notes at their current permalinks.

## 7. Tracker integration

The `epic.md` and `story.md` templates have a `tracker_url` frontmatter field. Substitute your team's tracker URL pattern. Examples:

- Jira: `https://<org>.atlassian.net/browse/ABC-123`
- Linear: `https://linear.app/<team>/issue/TEAM-123`
- GitHub Issues: `https://github.com/<org>/<repo>/issues/NNN`
- YouTrack: `https://<org>.youtrack.cloud/issue/XX-NNN`

The reviewer prompt doesn't validate or enforce the URL shape — it's pure human-readable context for KB readers.

## 8. Maintenance ownership

Who updates what:

| Action | Owner |
|---|---|
| Create an epic note at kickoff | Human (PM or tech lead) |
| Create a story note at story-open | Human (engineer picking up the ticket) |
| Update `## Stories` table on epic when a story changes status | Extract (via milestone events) |
| Update `## Open PRs` tables (story + epic) on PR state changes | Extract (via milestone events) |
| Append to `## Progress ledger` on milestone | Extract (via milestone events) |
| Tick `## Acceptance` checkboxes on epic | Human (at story-close or epic-close) |
| Set story `status: done` before final-PR merge | Human or agent (explicit closure gesture; signals terminal-merge to extract) |
| Write a decision note | Drafted by extract → reviewed by human → merged |
| Edit `## Open questions` on epic / story | Human (discussion notes; not auto) |

The principle: **extract proposes dashboard updates from milestone events; humans curate the durable layer**.

---

## See also

- [`examples/project-knowledge/`](../../examples/project-knowledge/) — the shipped templates.
- [`examples/project-knowledge/dogfood/`](../../examples/project-knowledge/dogfood/) — frozen-snapshot real anti-tangent example notes.
- [v0.6.0 spec](../superpowers/specs/2026-05-18-project-knowledge-design.md) and [v0.7.0 spec](../superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) — authoritative design docs.
- [`INTEGRATION.md` § "Project knowledge (optional)"](../../INTEGRATION.md#project-knowledge-optional) — generic-adopter integration guide.
```

- [ ] **Step 2: Verify**

Run: `wc -l docs/team-setup/project-knowledge-conventions.md`
Expected: 100–200 lines.

Run: `grep -c "^## " docs/team-setup/project-knowledge-conventions.md`
Expected: 8 H2 sections.

Run: `grep -c "<PROJECT>\|<TICKET-ID>" docs/team-setup/project-knowledge-conventions.md`
Expected: many.

- [ ] **Step 3: Commit task 7**

```bash
git add docs/team-setup/project-knowledge-conventions.md
git commit -m "$(cat <<'EOF'
docs(team-setup): add project-knowledge conventions adopter guide

Eight-section adopter-facing doc covering: when this pattern earns
its keep, the 1-BM-project-per-repo recommendation (with monorepo
namespacing exception), issue-ID format guidance, folder convention,
milestone events, project-prefix bootstrap (fresh-setup AND
v0.6.x→v0.7.0 migration via either bulk move_note OR
leave-legacy-and-stabilise), tracker integration, and maintenance
ownership.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Create the dogfood directory + four example notes

**Goal:** `examples/project-knowledge/dogfood/` ships with the README plus four frozen-snapshot real anti-tangent notes per spec §4.2.

**Acceptance criteria:**
- `examples/project-knowledge/dogfood/README.md` exists with the framing text.
- Four example notes exist at the paths from spec §4.2.
- Each example note matches the v0.7.0 template shape for its type (project-prefixed permalink, frontmatter, body sections).
- Each example uses `anti-tangent-mcp` as the `<PROJECT>` concrete value (anti-tangent's own dogfood project name).

**Files:**
- Create: `examples/project-knowledge/dogfood/README.md`
- Create: `examples/project-knowledge/dogfood/epics/gh-23/main.md`
- Create: `examples/project-knowledge/dogfood/stories/gh-25/main.md`
- Create: `examples/project-knowledge/dogfood/decisions/0001-text-only-reviewer/main.md`
- Create: `examples/project-knowledge/dogfood/modules/review-pipeline/main.md`

- [ ] **Step 1: Create `dogfood/README.md`**

```markdown
# anti-tangent dogfood examples

Frozen-snapshot real example notes from anti-tangent's own KB at the v0.7.0 release. Each file follows the v0.7.0 templates in [`../`](..) and uses the conventions from [`docs/team-setup/project-knowledge-conventions.md`](../../../docs/team-setup/project-knowledge-conventions.md).

**These are educational references**, not live state. Anti-tangent's live KB lives in its own Basic Memory project. This directory is re-snapshotted manually on major releases when the picture has materially shifted; it is NOT auto-updated.

Read each file as a worked example for its type:

- [`epics/gh-23/main.md`](epics/gh-23/main.md) — v0.6.x project-knowledge epic, with the dashboard sections populated.
- [`stories/gh-25/main.md`](stories/gh-25/main.md) — single-PR single-subtask story for the shape-guard fix (issue #25 → v0.6.1).
- [`decisions/0001-text-only-reviewer/main.md`](decisions/0001-text-only-reviewer/main.md) — anti-tangent's seminal architectural decision: the reviewer is text-only and never reads the codebase.
- [`modules/review-pipeline/main.md`](modules/review-pipeline/main.md) — one coherent capability (the validate_X surface) spanning four Go packages.

No feature or glossary dogfood — INTEGRATION.md and the design specs already document anti-tangent's features and glossary terms; KB notes would duplicate.
```

- [ ] **Step 2: Create `dogfood/epics/gh-23/main.md`**

```markdown
---
permalink: anti-tangent-mcp/epics/gh-23/main
type: epic
title: v0.6.x project-knowledge feature
status: in_progress
opened_at: 2026-05-18
closed_at: null
owners: ["@pgilmore"]
tracker_url: https://github.com/patiently/anti-tangent-mcp/issues/23
plan_refs: [docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md]
touches_modules: [anti-tangent-mcp/modules/review-pipeline/main]
produces_decisions: []
relates: []
tags: [epic]
---

## Charter

Ship the optional project-knowledge MCP layer: two new stateless tools (prime + extract), per-task `project_knowledge` input, a five-type note schema (v0.6.0) extended to six types with dashboards in v0.7.0, and operator-facing setup docs for Basic Memory deployment.

## In scope / Out of scope

- **In:** prime_project_knowledge and extract_project_knowledge tools; note-type templates; conventions doc; dogfood examples; team-setup playbook (VM and Docker variants).
- **Out:** runtime BM integration; concurrent-write resolution; story-done auto-detection.

## Acceptance (epic-level)

- [x] v0.6.0 ships the 5-type taxonomy + the two new tools.
- [x] v0.6.1 ships the Docker-container alternative + the shape-guard fix.
- [x] v0.6.2 trims INTEGRATION.md back under 40k + documents bm_commands→BM API mapping.
- [ ] v0.7.0 ships the 6th `story` type + epic-dashboard rewrite + project-prefix permalinks.

## Stories

| Story | Status | Deployment | Tracker |
|---|---|---|---|
| [gh-25](anti-tangent-mcp/stories/gh-25/main) — shape-guard false-positive fix | done | prod | [#25](https://github.com/patiently/anti-tangent-mcp/issues/25) |
| [gh-29](anti-tangent-mcp/stories/gh-29/main) — INTEGRATION.md trim + bm_commands translation | done | prod | [#29](https://github.com/patiently/anti-tangent-mcp/issues/29) |
| [gh-31](anti-tangent-mcp/stories/gh-31/main) — v0.7.0 project-knowledge conventions | in_progress | none | [#31](https://github.com/patiently/anti-tangent-mcp/issues/31) |

## Open PRs

| PR | Story | State | Title |
|---|---|---|---|
| #32 | [gh-31](anti-tangent-mcp/stories/gh-31/main) | draft | v0.7.0: project-knowledge conventions design (spec-only) |

## Progress ledger

- 2026-05-20 — v0.6.0 shipped: 16 tasks, 5-type taxonomy, prime+extract tools, INTEGRATION.md + team-setup docs.
- 2026-05-21 — v0.6.1 shipped: shape-guard fix (#25), Docker-container alternative (#26 tracker, PR #27).
- 2026-05-21 — v0.6.2 shipped: INTEGRATION.md trim back under 40k + bm_commands→BM API mapping subsection (#28 closed, #29 tracker, PR #30).
- 2026-05-21 — v0.7.0 spec landed on `version/0.7.0` branch (PR #32 in CR review).

## Open questions

- Whether `epic_origin`/`story_origin` should extend to `module` and `feature` proposals. Deferred to field signal.
- Whether `validate_completion` latency outliers warrant a soft `final_files` size threshold. Deferred per the v0.5.2 design.

---

**Note on terminal status.** Stories terminate with `status: done`; epics terminate with `status: closed`. This epic remains `in_progress` until v0.7.0 ships.
```

- [ ] **Step 3: Create `dogfood/stories/gh-25/main.md`**

```markdown
---
permalink: anti-tangent-mcp/stories/gh-25/main
type: story
title: validate_completion shape-guard false-positives on Go ./pkg/... syntax
status: done
opened_at: 2026-05-21
closed_at: 2026-05-21
parent_epic: anti-tangent-mcp/epics/gh-23/main
owners: ["@pgilmore"]
tracker_url: https://github.com/patiently/anti-tangent-mcp/issues/25
tags: [story]
---

## Story brief

The v0.5.2 release added six placeholder/truncation patterns to `evidenceTruncationPatterns`. One of them (`/...`) false-positives on Go's standard package-recursion syntax (`./internal/<pkg>/...`) when test_evidence prose or test files quote a `go test ./pkg/...` command. v0.6.0 implementation subagents on Tasks 9 and 10 both worked around the issue. This story fixes it in v0.6.1.

## Acceptance criteria

- The bare `/...` substring is removed from `evidenceTruncationPatterns`.
- A regression test confirms `final_diff` content containing `./internal/<pkg>/...` is no longer rejected.
- The other five v0.5.2 comment-form placeholders (`/* ... */`, `// snip`, etc.) remain — they're unambiguous.

## PRs

| PR | State | Branch | Relationship | Merged into | Deployed |
|---|---|---|---|---|---|
| #27 | merged | version/0.6.1 | initial | main | prod |

## Subtasks

- [x] Drop `/...` from `evidenceTruncationPatterns` in `internal/mcpsrv/handlers.go`.
- [x] Add `TestCheckEvidenceShape_GoPackageRecursionAccepted` regression test.
- [x] Document the change in CHANGELOG v0.6.1 entry.

## Deployment state

| Env | Latest landed | Date | Notes |
|---|---|---|---|
| prod | PR #27 | 2026-05-21 | v0.6.1 release shipped. |

## Decisions produced

None — pure bug fix.

## Open questions

None — story closed.
```

- [ ] **Step 4: Create `dogfood/decisions/0001-text-only-reviewer/main.md`**

```markdown
---
permalink: anti-tangent-mcp/decisions/0001-text-only-reviewer/main
type: decision
title: Reviewer LLM reasons over plan-text only; never reads the codebase
status: accepted
supersedes: []
proposed_by: "@pgilmore"
decided_at: 2026-05-07
epic_origin: anti-tangent-mcp/epics/v0.1-design-and-mvp/main
relates: []
tags: [architecture, seminal]
---

## Context

When designing anti-tangent's reviewer pipeline, a choice was needed: should the reviewer LLM have access to the codebase (via tools like Read, Grep, etc.) or operate purely on the plan text + submitted evidence the controller hands it?

A codebase-aware reviewer can catch more bugs (missing symbols, wrong signatures, repo-wide invariants). A text-only reviewer is faster, cheaper, simpler to operate, and never has to fight tool authentication / sandboxing / parallel-execution issues.

## Decision

The reviewer is **text-only**. It reasons over the plan text, the submitted task spec, and any evidence the caller pastes into the tool call (`final_files`, `final_diff`, `test_evidence`, `pinned_by`, `controller_verified_references`, `harness_shape_attestation`, `project_knowledge`). It NEVER reads the codebase directly.

## Consequences

- Anti-tangent is **advisory, not authoritative**. It catches plan-internal contradictions but cannot detect codebase facts (missing symbols, signature mismatches).
- Pair with a codebase-aware review for any plan that lands real code. Default recommendation: CodeRabbit.
- The reviewer prompt explicitly tells the model it cannot verify codebase claims; when it encounters one, it emits `unverifiable_codebase_claim` (severity-floored to minor) so the caller can grep before dispatch.
- Several follow-up mechanisms (`controller_verified_references`, `harness_shape_attestation`, `codebase_conventions`) exist to let the caller anchor text-only review against codebase reality without giving the reviewer codebase access.

## Alternatives considered

- **Codebase-aware reviewer with read-only repo access.** Rejected because of operational complexity — tool authentication, sandboxing, parallel execution conflicts, file-system permissions. Also slower (the reviewer would re-grep the repo on every call).
- **Hybrid: text-only by default + opt-in codebase tool.** Rejected because the opt-in cliff would create two different behaviors for callers to reason about. Better to be uniformly text-only and let upstream tools (CodeRabbit, CodeScene) own the codebase-aware layer.
```

- [ ] **Step 5: Create `dogfood/modules/review-pipeline/main.md`**

```markdown
---
permalink: anti-tangent-mcp/modules/review-pipeline/main
type: module
title: Review pipeline — plan/task/completion validation surface
status: stable
last_changed_in: 0.6.2
relates_features: [anti-tangent-mcp/features/validate-plan/main, anti-tangent-mcp/features/validate-task-spec/main, anti-tangent-mcp/features/check-progress/main, anti-tangent-mcp/features/validate-completion/main]
shaped_by_decisions: [anti-tangent-mcp/decisions/0001-text-only-reviewer/main]
tags: [module, core-surface]
---

## Purpose

The review pipeline is anti-tangent's core capability. It takes plan text, task specs, or completion envelopes from a caller; runs a reviewer LLM against them with a structured prompt; parses the reviewer's JSON output into a verdict envelope; and returns the envelope to the caller. The four user-facing tools (`validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`) are different entry points into the same pipeline.

This is the **user-facing surface** of anti-tangent. The Go-package breakdown is incidental — the pipeline spans `internal/mcpsrv` (handler wiring), `internal/verdict` (verdict types, schemas, parsing), `internal/prompts` (embedded prompt templates + render functions), and `internal/providers` (per-provider HTTP clients to Anthropic / OpenAI / Google). None of these packages do useful work alone; together they implement the review pipeline.

## Invariants

- **Stateless except for sessions.** Each per-task hook creates / continues a `Session` (TTL-bounded, in-memory only). Sessions tie the lifecycle hooks together; the rest of the pipeline is pure request → reviewer → response.
- **Reviewer is text-only** ([decisions/0001-text-only-reviewer](anti-tangent-mcp/decisions/0001-text-only-reviewer/main)). The pipeline never grants the reviewer codebase access.
- **Strict OpenAI schema compatibility.** The four reviewer-output schemas (`schema.json`, `plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`, plus v0.6.0's `prime_schema.json` and `extract_schema.json`) all pass the strict-mode invariants in `schema_invariants_test.go`. Any new property must appear in `required`; no freeform objects; `additionalProperties: false` everywhere.
- **`stdout` is reserved for MCP stdio traffic.** All logging goes to stderr via structured `slog`. Any incidental `fmt.Println` to stdout would break the MCP transport.

## Conventions

- Reviewer output is always parsed via `verdict.Parse*` helpers; the parser owns enum validation, severity-floor enforcement, partial-recovery, and field-presence checks. Handlers don't reimplement these.
- Per-hook handler files (`internal/mcpsrv/{validate_*,prime_handler,extract_handler}.go`) all follow the same ordering: payload-size guard → model resolve → render prompt → call reviewer → parse output → finalize verdict → return envelope.
- New reviewer-output finding categories require updates in five places: `verdict.Category` constants, `validCategory` switch, all four canonical schemas' enum entries, plus `prime_schema.json` and `extract_schema.json` if applicable. The `schema_invariants_test.go::TestReviewerSchemas_CategoryEnumsAreInLockstep` catches lockstep failures.
- Embedded prompt templates live in `internal/prompts/templates/`; the package's `Render*` functions are the only entry points (no direct template execution from handler code).

## Touch-points

- `internal/mcpsrv/server.go` — MCP server entrypoint; registers all 6 tools.
- `internal/mcpsrv/handlers.go` — shared helpers (`review`, `effectiveMaxTokens`, `envelopeResult`, etc.).
- `internal/mcpsrv/{prime,extract}_handler.go` — v0.6.0 stateless handlers.
- `internal/verdict/` — verdict types, JSON schemas, parsers, severity-floor helpers.
- `internal/prompts/prompts.go` + `internal/prompts/templates/*.tmpl` — prompt rendering.
- `internal/providers/{anthropic,openai,google}.go` — provider HTTP clients.
```

- [ ] **Step 6: Verify all five files**

Run: `ls -la examples/project-knowledge/dogfood/`
Expected: README.md plus four subdirectories with `main.md` files.

Run: `find examples/project-knowledge/dogfood -name '*.md' | wc -l`
Expected: 5 (README + 4 example notes).

Run: `grep -lh "anti-tangent-mcp/" examples/project-knowledge/dogfood/ -r | wc -l`
Expected: ≥ 4 (all four example notes use the concrete project name).

- [ ] **Step 7: Commit task 8**

```bash
git add examples/project-knowledge/dogfood/
git commit -m "$(cat <<'EOF'
docs(examples): add v0.7.0 dogfood directory with 4 example notes

Frozen-snapshot real anti-tangent notes at v0.7.0:
- epics/gh-23/main.md — v0.6.x project-knowledge feature epic
  with dashboard sections populated (Stories list with gh-25, gh-29,
  gh-31; Open PRs showing PR #32; Progress ledger covering v0.6.0,
  v0.6.1, v0.6.2 shipping milestones).
- stories/gh-25/main.md — single-PR story for the shape-guard fix
  (issue #25 → v0.6.1, PR #27).
- decisions/0001-text-only-reviewer/main.md — seminal anti-tangent
  decision: reviewer is text-only, never reads codebase.
- modules/review-pipeline/main.md — coherent capability across
  internal/mcpsrv + internal/verdict + internal/prompts +
  internal/providers.
- README.md — framing as educational frozen snapshot; no feature
  or glossary dogfood (INTEGRATION.md covers those).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Update `INTEGRATION.md` + verify size budget

**Goal:** INTEGRATION.md's "Project knowledge (optional)" section mentions all 6 types and links to the new conventions doc. Total file size stays under the 40,000-byte threshold per constraint (A).

**Acceptance criteria:**
- INTEGRATION.md's "Project knowledge (optional)" H2 section text mentions "six types" (or equivalent enumeration including `story`).
- INTEGRATION.md links to `docs/team-setup/project-knowledge-conventions.md` once in the project-knowledge section.
- INTEGRATION.md total size is **less than 40,000 bytes** (`wc -c`).
- The "Project knowledge (optional)" anchor referenced from README.md:343 still resolves (the H2 heading text is unchanged).

**Files:**
- Modify: `INTEGRATION.md`

- [ ] **Step 1: Measure starting size**

Run: `wc -c < INTEGRATION.md`
Record the current byte count (expected ≈ 38835 post-v0.6.2 trim).

- [ ] **Step 2: Locate the project-knowledge section**

Run: `grep -n "^## Project knowledge (optional)" INTEGRATION.md`
Expected: one match. Note the line number.

- [ ] **Step 3: Add the 6-type mention + conventions-doc link**

Find the opening paragraph of `## Project knowledge (optional)`. After the existing description, append a single sentence mentioning the 6 types AND the conventions doc. Keep it tight.

Example shape (substitute the exact wording into the file):

```markdown
## Project knowledge (optional)

[existing opening paragraph stays unchanged]

**Six note types** in two layers: the durable reference layer (`decision`, `module`, `feature`, `glossary`) and the operational layer (`epic`, `story` — added in v0.7.0 with live-dashboard sections). For the per-project tuning loop — issue-ID format, folder convention, milestone events, and the v0.6.x→v0.7.0 migration path — see [`docs/team-setup/project-knowledge-conventions.md`](docs/team-setup/project-knowledge-conventions.md).

[rest of section stays unchanged]
```

This addition is ~300 bytes. Headroom is ~1,200 bytes; we land comfortably under 40k.

- [ ] **Step 4: Verify the size budget**

Run: `wc -c < INTEGRATION.md`
Expected: between 38,800 and 40,000.

If the result is >= 40,000, you've blown the budget; trim other prose in INTEGRATION.md until the byte count is < 40,000. Re-verify after each trim.

Hard check (matches spec §5.4):

```bash
size=$(wc -c < INTEGRATION.md)
if [ "$size" -ge 40000 ]; then
  echo "FAIL: INTEGRATION.md is $size bytes; must be < 40000"
  exit 1
fi
echo "OK: INTEGRATION.md is $size bytes (limit 40000)"
```

Expected: `OK:` line.

- [ ] **Step 5: Verify the anchor + cross-link**

Run: `grep -c "INTEGRATION.md#project-knowledge-optional" README.md docs/team-setup/`
Expected: ≥ 1 (README.md:343 still resolves).

Run: `grep -c "docs/team-setup/project-knowledge-conventions.md" INTEGRATION.md`
Expected: 1.

Run: `grep -c "six types" INTEGRATION.md`
Expected: 1.

- [ ] **Step 6: Commit task 9**

```bash
git add INTEGRATION.md
git commit -m "$(cat <<'EOF'
docs(integration): mention 6 types + link conventions doc (v0.7.0)

Adds a single-sentence mention of the v0.7.0 6-type taxonomy and
a link to docs/team-setup/project-knowledge-conventions.md in
INTEGRATION.md's "Project knowledge (optional)" section.

Total file size verified < 40,000 bytes per the v0.6.2 size budget
(constraint A in the v0.7.0 plan). The deep-link anchor at
INTEGRATION.md#project-knowledge-optional (from README.md:343) is
preserved.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Finalize CHANGELOG + smoke test + release-readiness check

**Goal:** CHANGELOG v0.7.0 entry reconciles against the shipped surface. Manual smoke test verified per spec §5.3. Branch is release-ready.

**Acceptance criteria:**
- `CHANGELOG.md`'s `## [0.7.0] - 2026-05-21` entry under `### Added` lists: 6th `story` type, conventions doc, dogfood directory, `story_origin` field on decisions.
- `### Changed` lists: `epic.md` rewrite, project-prefixed permalink shape across all six types, `extract.tmpl` milestone-detection rules, INTEGRATION.md 6-types mention.
- `### Fixed`, `### Removed`, `### Deprecated`, `### Security` subsections present (even if empty) per Keep-a-Changelog convention.
- Smoke test from spec §5.3 has been run (or documented as deferred until a real BM instance is available).
- `go test -race ./...` is green.
- `wc -c < INTEGRATION.md` is < 40,000.

**Files:**
- Modify (reconcile against actual shipped surface): `CHANGELOG.md`

- [ ] **Step 1: Read the existing v0.7.0 stub**

Run: `sed -n '/^## \[0\.7\.0\]/,/^## \[/p' CHANGELOG.md | head -30`

Compare against what actually shipped in Tasks 1–9. Adjust prose if any task scope shifted during execution.

- [ ] **Step 2: Sanity-check the entry's content**

Verify all six v0.7.0 deliverables are reflected somewhere in the entry:
- (Added) New 6th type `story` — schema + template + parser
- (Added) Conventions doc `docs/team-setup/project-knowledge-conventions.md`
- (Added) Dogfood directory at `examples/project-knowledge/dogfood/`
- (Added) `story_origin` field on `decision` frontmatter
- (Changed) `epic.md` rewritten with live-dashboard sections
- (Changed) `<PROJECT>/<type>/<key>/main` permalink shape across all 6 templates
- (Changed) `extract.tmpl` reviewer-prompt — milestone detection, project-prefix inference, dashboard updates
- (Changed) INTEGRATION.md — 6-types mention; size kept under 40k

Verify the entry has the standard Keep-a-Changelog subsections (Added, Changed, Fixed, Removed, Deprecated, Security) even if some are empty.

- [ ] **Step 3: Run the manual smoke test (per spec §5.3)**

This step is optional if no real BM instance is available; document the deferral if so.

1. Write a seed `epic` note in a real BM instance using the v0.7.0 template (with empty `## Stories` and `## Open PRs` tables). Then write one `story` note linked via `parent_epic`. Confirm BM accepts both frontmatter shapes + the folder-per-ticket permalink.
2. Call `extract_project_knowledge` against a small completion envelope mentioning "PR #42 merged" in `final_diff`, with `epic_permalink` set to the seed epic's permalink and `current_kb_excerpts` carrying the epic's `## Stories` section. Verify extract emits a `replace_section` `bm_command` targeting that epic's `## Stories` section AND (if applicable) a separate `bm_command` for the story's `## PRs` row.
3. Apply both bm_commands. Confirm the dashboard updates without clobbering unrelated content.

If unable to run against a real BM, document that the smoke test is deferred until the v0.7.0 release-readiness review.

- [ ] **Step 4: Final release-readiness checks**

Run all final-state checks per spec §5.4:

```bash
rg "five types|5 types|5-type" INTEGRATION.md README.md docs/team-setup/  # expected: 0
rg "<PROJECT>" examples/project-knowledge/ docs/team-setup/                 # expected: many
rg "<PROJECT>" internal/ go.mod                                              # expected: 0
rg "<TICKET-ID>" examples/project-knowledge/ docs/team-setup/                # expected: many
rg "<TICKET-ID>" internal/                                                   # expected: 0
```

Run: `wc -c < INTEGRATION.md`
Expected: < 40,000.

Run: `go test -race ./...`
Expected: PASS across all packages.

Run: `grep -c "INTEGRATION.md#project-knowledge-optional" README.md`
Expected: ≥ 1.

- [ ] **Step 5: Commit task 10**

```bash
git add CHANGELOG.md
git commit -m "$(cat <<'EOF'
docs(changelog): finalize v0.7.0 entry against shipped surface

Reconciles the v0.7.0 stub with the actual shipped surface:
- (Added) story note type — schema enum + template + parser
- (Added) docs/team-setup/project-knowledge-conventions.md
- (Added) examples/project-knowledge/dogfood/ (4 example notes)
- (Added) story_origin frontmatter field on decision notes
- (Changed) epic.md rewritten with dashboard sections
- (Changed) <PROJECT>/<type>/<key>/main permalink shape across all 6
- (Changed) extract.tmpl with milestone-detection rules + project-
  prefix inference + dashboard-update operation guidance
- (Changed) INTEGRATION.md mention of 6 types (kept under 40k)

Manual smoke test per spec §5.3 [run / deferred] against a real BM
instance.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Plan-level reminders

- `go test -race ./...` after every task that touches code. Tasks 1, 5, 6 touch Go/templates; the rest are markdown-only.
- After Task 5 (extract.tmpl edits) you MUST run `go test ./internal/prompts/... -update` in Task 6 to regenerate the golden — leaving it stale fails CI.
- INTEGRATION.md size budget (constraint A) is checked twice: Task 9 (after the addition) and Task 10 (final pre-release check). If the budget overshoots at Task 9, trim in Task 9 — don't defer.
- Backwards compatibility (constraint B) is verified by `TestParseExtract_AcceptsAllSixTypes` (Task 1) AND by the smoke test (Task 10).
- Tasks are sequential — Tasks 5 and 6 are paired (template edit + golden regen). Don't reorder.
- After all tasks: PR review by CodeRabbit; address findings per the standard repo CR loop; merge to main with `[minor]` in the merge commit subject so the release workflow bumps 0.6.2 → 0.7.0.
