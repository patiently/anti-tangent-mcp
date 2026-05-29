# Howto Note Type Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `howto` as the eighth project-knowledge note type — a slug-keyed, update-in-place operational runbook — extractable via `extract_project_knowledge` and authored via a new `bm-scribe:create-howto` skill.

**Architecture:** Mirror the v0.8.0 `gotcha` addition exactly, adapting the contract: `howto` is durable-reference (not lessons-learned), slug-keyed (not ADR-numbered), and updated in place (extract actions `create`/`update`, never `supersede`). Anti-tangent's responsibility ends at emitting `Proposal{type: howto}`; the plugin owns the user-facing creator flow; the read side (`prime`/`validate_plan`) is unchanged because it is type-agnostic via `kb_index` `tags`.

**Tech Stack:** Go 1.x (`internal/verdict`, `internal/prompts` golden tests), embedded JSON schema + Go text/template, markdown skill contracts (`plugin/bm-scribe`), Basic Memory v0.21.1 three-step pattern.

**Spec:** [`docs/superpowers/specs/2026-05-29-howto-note-type-design.md`](../specs/2026-05-29-howto-note-type-design.md)

**Preflight (dispatch context):** This plan is executed on the **already-created** `version/0.9.0` branch — do NOT create or switch branches, and do NOT bump `VERSION` (the release workflow owns it; pre-bumping fails CI). Each task that changes files ends with a `git commit` on this branch (repo frequent-commit convention); **Task 7 is verification-only — it commits only if its checks surface a fix, otherwise nothing.** Markdown-only tasks (1, 4, 5, and the skill/template files in 6) have no Go unit tests by design — skills and templates are markdown contracts validated by manual dogfooding out of band, exactly as the existing `create-*` skills shipped; their per-task verification is the literal `grep` checks shown.

---

## File Structure

**Anti-tangent (Go):**
- `internal/verdict/extract.go` — add `ProposalTypeHowto` constant.
- `internal/verdict/extract_parser.go` — add `ProposalTypeHowto` to the type-validation switch (line 82).
- `internal/verdict/extract_schema.json` — add `"howto"` to the `proposals[].type` enum (line 53).
- `internal/verdict/extract_parser_test.go` — extend round-trip type table; add howto create + update acceptance tests.
- `internal/prompts/templates/extract.tmpl` — add `howto` to durable-knowledge + type lists; add new `3a-howto` guidance block.
- `internal/prompts/testdata/extract_basic.golden`, `extract_milestone.golden` — regenerated with `-update`.

**Plugin (markdown contracts):**
- `plugin/bm-scribe/skills/create-howto/SKILL.md` — NEW creator skill (slug key, create + update-in-place).
- `plugin/bm-scribe/README.md` — subcommand table + skill count.
- `plugin/bm-scribe/.claude-plugin/plugin.json`, `package.json`, `gemini-extension.json` — version 0.2.0 → 0.3.0.

**Docs:**
- `CHANGELOG.md` — new `## [0.9.0]` block.
- `examples/project-knowledge/howto.md` — NEW template.
- `examples/project-knowledge/README.md` — type count, group list, maintenance table.
- `INTEGRATION.md` — note-types table, v0.7.0 layout, auto-apply ladder.
- `docs/team-setup/project-knowledge-conventions.md` — folder list, kb_index coverage.
- `README.md` — plugin skill count + creator list.

**Out of scope (do NOT touch):** the user's global `~/.claude/anti-tangent.md` (mirrored from `INTEGRATION.md` out of band); `internal/verdict/prime.go`, `internal/prompts/templates/prime.tmpl` (type-agnostic, no change); `VERSION` (the release workflow bumps it — do NOT pre-bump on the branch).

---

### Task 1: Create the 0.9.0 CHANGELOG entry

Front-loaded so CI's branch-name-matches-CHANGELOG check is green from the first push.

**Files:**
- Modify: `CHANGELOG.md:8` (insert above the `## [0.8.3] - 2026-05-27` line)

- [ ] **Step 1: Insert the 0.9.0 block**

Insert this block immediately above the existing `## [0.8.3] - 2026-05-27` line (there is no `## [Unreleased]` section):

```markdown
## [0.9.0] - 2026-05-29

### Added
- `howto` project-knowledge note type (eighth type) — a slug-keyed, update-in-place operational runbook; the durable-reference counterpart to `gotcha`. Proposed by `extract_project_knowledge` with `action: create` / `action: update` (never `supersede`).
- `bm-scribe:create-howto` skill — captures a `howto` at `<PROJECT>/howtos/<slug>/main` via the three-step BM v0.21.1 pattern, with in-place update of an existing runbook's `## Steps`.

### Changed
- `plugin/bm-scribe` bumped to 0.3.0 (new `create-howto` skill; 14 skills total).

```

- [ ] **Step 2: Verify the CHANGELOG header matches the branch**

Run: `head -12 CHANGELOG.md | grep -n '0.9.0'`
Expected: a line `## [0.9.0] - 2026-05-29` is printed (matches branch `version/0.9.0`).

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): open 0.9.0 — howto note type"
```

---

### Task 2: Wire `howto` through the Go type system (TDD)

Makes `howto` a fully valid proposal type end-to-end on the Go side: enum constant, parser switch, structured-output schema enum. There is **no** automated lockstep test between `extract_schema.json`'s `type` enum and the Go constants, so the schema edit is manual; the extended round-trip test covers parser acceptance.

**Files:**
- Modify: `internal/verdict/extract.go:15-23`
- Modify: `internal/verdict/extract_parser.go:82`
- Modify: `internal/verdict/extract_schema.json:53`
- Test: `internal/verdict/extract_parser_test.go:473-482` (extend) + new test functions

- [ ] **Step 1: Write the failing tests**

In `internal/verdict/extract_parser_test.go`, add `howto` to the round-trip type table. Change the `pathSeg` map (currently lines 473-481) and the `types` slice (line 482) to:

```go
		pathSeg := map[string]string{
			"decision": "decisions",
			"module":   "modules",
			"feature":  "features",
			"glossary": "glossary",
			"epic":     "epics",
			"story":    "stories",
			"gotcha":   "gotchas",
			"howto":    "howtos",
		}
		types := []string{"decision", "module", "feature", "glossary", "epic", "story", "gotcha", "howto"}
```

Then append three new test functions at the end of the file (after `TestParseExtract_AcceptsGotchaSupersede`) — two acceptance tests (create, update) and one negative test pinning the no-supersede invariant:

```go
func TestParseExtract_AcceptsHowtoType(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [{
			"action": "create",
			"type": "howto",
			"permalink": "monorepo/howtos/deploy-release/main",
			"title": "Deploy a release",
			"frontmatter_json": "{\"status\":\"active\",\"modules\":[\"release\",\"ci\"],\"last_verified\":\"2026-05-29\"}",
			"body": "## When to use\n\nCutting a tagged release.\n\n## Steps\n\n1. Bump VERSION.\n2. Merge to main.\n\n## Verification\n\nCI publishes the artifact.",
			"body_patch": "",
			"rationale": "Saves the next releaser from re-deriving the deploy sequence",
			"evidence_refs": ["completion_envelopes[0].summary"],
			"supersedes": []
		}],
		"bm_commands": [],
		"next_action": "Apply the howto note."
	}`)
	r, err := verdict.ParseExtract(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(r.Proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(r.Proposals))
	}
	if r.Proposals[0].Type != verdict.ProposalTypeHowto {
		t.Fatalf("expected type howto, got %q", r.Proposals[0].Type)
	}
}

func TestParseExtract_AcceptsHowtoUpdate(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [{
			"action": "update",
			"type": "howto",
			"permalink": "monorepo/howtos/deploy-release/main",
			"title": "Deploy a release",
			"frontmatter_json": "{\"last_verified\":\"2026-05-29\"}",
			"body": "",
			"body_patch": "## Steps\n\n1. Bump VERSION.\n2. Open a version/X.Y.Z PR.\n3. Merge with the bump tag.",
			"rationale": "The deploy sequence gained a PR-gating step",
			"evidence_refs": ["completion_envelopes[0].final_diff"],
			"supersedes": []
		}],
		"bm_commands": [],
		"next_action": "Apply the howto update."
	}`)
	r, err := verdict.ParseExtract(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(r.Proposals) != 1 || r.Proposals[0].Action != verdict.ProposalActionUpdate {
		t.Fatalf("expected 1 update proposal, got %+v", r.Proposals)
	}
	if r.Proposals[0].Type != verdict.ProposalTypeHowto {
		t.Fatalf("expected type howto, got %q", r.Proposals[0].Type)
	}
}

func TestParseExtract_RejectsHowtoSupersede(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [{
			"action": "supersede",
			"type": "howto",
			"permalink": "monorepo/howtos/deploy-release/main",
			"title": "Deploy a release",
			"frontmatter_json": "{\"status\":\"deprecated\"}",
			"body": "",
			"body_patch": "",
			"rationale": "attempt to supersede a howto (must be rejected)",
			"evidence_refs": ["completion_envelopes[0].summary"],
			"supersedes": ["monorepo/howtos/old-deploy/main"]
		}],
		"bm_commands": [],
		"next_action": "noop"
	}`)
	if _, err := verdict.ParseExtract(raw); err == nil {
		t.Fatal("expected error: howto notes cannot be superseded")
	}
}
```

(The negative test asserts only that `ParseExtract` returns a non-nil error — it deliberately does NOT assert the exact message, so the test is not coupled to the guard's wording.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/verdict/ -run 'TestParseExtract_(RoundTrip|AcceptsHowto|RejectsHowto)' -v`
Expected: FAIL — does not compile because `verdict.ProposalTypeHowto` is undefined until Step 3 adds the constant. (Once the constant exists but before the guard, the round-trip `howto` subtest and the two accept tests would error with `proposal[0]: invalid type "howto"`, and `RejectsHowtoSupersede` would still fail because the parser accepts howto+supersede until the Step 3 guard lands.)

- [ ] **Step 3: Add the enum constant, the parser switch case, and the schema enum value**

In `internal/verdict/extract.go`, add the constant to the `ProposalType` block (after `ProposalTypeGotcha`, line 22):

```go
	ProposalTypeGotcha   ProposalType = "gotcha"
	ProposalTypeHowto    ProposalType = "howto"
```

In `internal/verdict/extract_parser.go`, add `ProposalTypeHowto` to the type-validation switch (line 82):

```go
		case ProposalTypeDecision, ProposalTypeModule, ProposalTypeFeature, ProposalTypeGlossary, ProposalTypeEpic, ProposalTypeStory, ProposalTypeGotcha, ProposalTypeHowto:
```

Then, immediately after that `switch p.Type { … }` block (after its closing `}` on line 85, before the `if p.Permalink == ""` presence check on line 95), add the no-supersede guard so the "create/update, never supersede" contract is a hard parser invariant rather than just reviewer-prompt guidance:

```go
		// howto is a slug-keyed living document, updated in place — it is
		// never superseded (design spec §6.4). Reject any howto proposal
		// carrying a supersede action; create/update with empty supersedes
		// are already enforced by the action-conditional checks below.
		if p.Type == ProposalTypeHowto && p.Action == ProposalActionSupersede {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: howto notes are update-in-place and cannot be superseded", i)
		}
```

In `internal/verdict/extract_schema.json`, add `"howto"` to the `proposals[].type` enum (line 53):

```json
          "type":             { "type": "string", "enum": ["decision", "module", "feature", "glossary", "epic", "story", "gotcha", "howto"] },
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/verdict/ -run 'TestParseExtract_(RoundTrip|AcceptsHowto|RejectsHowto)' -v`
Expected: PASS (the round-trip `howto` subtest, both accept tests, and `RejectsHowtoSupersede` — the guard now makes the parser return an error for howto+supersede).

- [ ] **Step 5: Run the full verdict package with the race detector**

Run: `go build ./... && go test -race ./internal/verdict/...`
Expected: PASS, no build errors.

- [ ] **Step 6: Commit**

```bash
git add internal/verdict/extract.go internal/verdict/extract_parser.go internal/verdict/extract_schema.json internal/verdict/extract_parser_test.go
git commit -m "feat(verdict): add howto ProposalType to extract enum + schema"
```

---

### Task 3: Add the `3a-howto` reviewer guidance to `extract.tmpl` and regenerate goldens

The golden test fails first (rendered prompt changed), confirming the template edit takes effect; then regenerate.

**Files:**
- Modify: `internal/prompts/templates/extract.tmpl:67`, `:71`, `:157`, and insert a new block after `:93`
- Modify: `internal/prompts/testdata/extract_basic.golden`, `internal/prompts/testdata/extract_milestone.golden` (via `-update`)

- [ ] **Step 1: Add `howto` to the durable-knowledge list (line 67)**

Replace the trailing clause of line 67 (`… or a **gotcha** (… cache invalidation). Always proposable; not gated on milestones.`) so it reads:

```
   - **Durable knowledge** worth capturing as a new note: an architectural decision, a module's invariants, a new feature's behavior, a glossary term, a **gotcha** (a project-and-module-scoped lesson learned — an N+1 query that bit us, a CSV parser that strips trailing newlines, a deploy step that races a cache invalidation), or a **howto** (a project-and-module-scoped operational procedure — a runbook for deploying, migrating, or setting up that future work should follow rather than rediscover). Always proposable; not gated on milestones.
```

- [ ] **Step 2: Add `howto` to the `type` list (line 71)**

```
   - `type`: one of `decision`, `module`, `feature`, `glossary`, `epic`, `story`, `gotcha`, `howto`.
```

- [ ] **Step 3: Add `howto` to the schema-shape `type` enum (line 157)**

```
      "type": "decision|module|feature|glossary|epic|story|gotcha|howto",
```

- [ ] **Step 4: Insert the `3a-howto` guidance block**

Insert this block immediately AFTER the `3a-gotcha-supersede.` paragraph (line 93) and BEFORE the `3a. **Dashboard updates require a milestone.**` line (line 95), separated by blank lines:

```
3a-howto. **Howto proposals (`type: howto`).** A `howto` is a project-and-module-scoped operational procedure — a runbook the team should follow rather than rediscover (deploy steps, a migration runbook, local-env setup, a release cut). Choose `howto` over neighbors carefully: a `gotcha` is a *pitfall / lesson* (what NOT to do) where a `howto` is the *procedure* (what TO do); a `feature` is a user-facing capability; a `module` is a capability surface + invariants; a `decision` is a choice + rationale. When you propose a `howto`:
    - Set `permalink` to `<PROJECT>/howtos/<slug>/main`, using a kebab-case `<slug>` derived from the title. NO ADR number — howtos are slug-keyed living documents, not a supersede chain.
    - Use `action: "create"` for a new procedure. Use `action: "update"` (with a `body_patch` carrying the changed section) when an envelope shows an existing howto's steps changed. NEVER emit `action: "supersede"` for a `howto`.
    - Set `frontmatter_json` to include at minimum: `status: "active"`, `modules: ["<slug>", ...]` (one or more module slugs visible in the envelopes or `kb_index`), `last_verified: "<YYYY-MM-DD>"`. Optional but recommended: `origin: "<PROJECT>/stories/<TICKET-ID>/main"` (or epic/PR permalink) pointing at the work that produced or validated the procedure.
    - Structure the `body` (for `create`) with required sections `## When to use` and `## Steps`, in that order, optionally followed by `## Prerequisites`, `## Verification`, `## Rollback / if it goes wrong`, and `## Related` with `[[wikilinks]]`. The `## Steps` section is the load-bearing one — it carries the procedure that prime will excerpt into the next plan's `project_knowledge`.
    - `rationale`: one sentence explaining why this procedure is worth recording (capture the cost of NOT recording it — the re-discovery tax on the next person).
    - `evidence_refs`: at least one ref into the supplied envelopes pointing at the source of the procedure. Do not fabricate evidence.
```

- [ ] **Step 5: Run the prompt golden tests to verify they fail**

Run: `go test ./internal/prompts/...`
Expected: FAIL — golden mismatch on `extract_basic.golden` and/or `extract_milestone.golden` (the rendered prompt now contains the new `3a-howto` block and the updated type lists). Skim the diff in the failure output to confirm only the intended additions appear.

- [ ] **Step 6: Regenerate the goldens**

Run: `go test ./internal/prompts/... -update`
Then inspect the diff to confirm ONLY the howto additions changed:
Run: `git diff -- internal/prompts/testdata/`
Expected: the diff shows the new `3a-howto` block plus `, howto` / `|howto` added to the type lists, and nothing else.

- [ ] **Step 7: Run the prompt tests to verify they pass**

Run: `go test ./internal/prompts/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/prompts/templates/extract.tmpl internal/prompts/testdata/extract_basic.golden internal/prompts/testdata/extract_milestone.golden
git commit -m "feat(prompts): teach extract reviewer the howto note type"
```

---

### Task 4: Add the `howto` template and update the examples README

**Non-goals (covered by other tasks):** wiring `howto` into the parser/schema (Task 2); editing the extract reviewer prompt or goldens (Task 3); adding the `bm-scribe:create-howto` skill (Task 6). This task is the example template + examples README only.

**Files:**
- Create: `examples/project-knowledge/howto.md`
- Modify: `examples/project-knowledge/README.md:3`, `:14`, `:16`, `:24`, `:25`, `:34-42`

- [ ] **Step 1: Create `examples/project-knowledge/howto.md`**

```markdown
---
permalink: <PROJECT>/howtos/<slug>/main
type: howto
title: <one-line title — the procedure>
status: active                   # active | deprecated
modules: [<slug>, <slug>]        # one or more module slugs this procedure touches
last_verified: <YYYY-MM-DD>      # when the steps were last confirmed to work
origin: <PROJECT>/stories/<TICKET-ID>/main   # optional; story / epic / PR that produced or validated the procedure
tags: []
---

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

- [[<PROJECT>/modules/<slug>/main]]
- [[<origin permalink>]]
```

- [ ] **Step 2: Update the count and intro (line 3)**

Change `Seven templates seed the project-knowledge schema` to `Eight templates seed the project-knowledge schema`.

- [ ] **Step 3: Update the group heading and durable layer (lines 14, 16)**

Change the heading on line 14 from `## Seven types in three groups` to `## Eight types in three groups`.

Change line 16's durable layer list to add `howto`:

```
- **Durable reference layer** (timeless / slow-evolving): `decision`, `module`, `feature`, `glossary`, `howto`. Survives epics.
```

- [ ] **Step 4: Update the permalink convention (lines 24, 25)**

Line 24 — add `howtos` to the `<type>` folder list:

```
- `<type>` is one of `decisions`, `modules`, `features`, `glossary`, `epics`, `stories`, `gotchas`, `howtos`.
```

Line 25 — add `howtos` to the slug-keyed list:

```
- `<key>` is either a slug (decisions, modules, features, glossary, gotchas, howtos — gotchas use ADR-numbered slugs like decisions, howtos use plain slugs like modules/features) or a ticket ID (epics, stories).
```

- [ ] **Step 5: Add the `howto` row to the maintenance-ownership table**

After the `gotcha` row (line 42), append:

```
| `howto` | Drafted by `extract_project_knowledge` (post-plan) → reviewed by human → applied via the three-step BM creator pattern | Updated in place via `action: "update"` (edit `## Steps`, bump `last_verified`); retired by flipping `status: active` → `status: deprecated`; module-scoped via the `modules:` frontmatter array; prime surfaces active howtos on future plans touching the same modules |
```

- [ ] **Step 6: Verify rendering**

Run: `grep -n 'Eight\|howto' examples/project-knowledge/README.md`
Expected: the intro, heading, durable layer, both permalink lines, and the maintenance row all reference `howto`/`howtos`; no remaining `Seven types`.

- [ ] **Step 7: Commit**

```bash
git add examples/project-knowledge/howto.md examples/project-knowledge/README.md
git commit -m "docs(examples): add howto template + list it as the eighth type"
```

---

### Task 5: Update INTEGRATION.md, conventions doc, and top-level README

**Files:**
- Modify: `INTEGRATION.md:312`, `:322` (add row), `:328`, `:348` (add ladder rows)
- Modify: `docs/team-setup/project-knowledge-conventions.md:43`, `:132`
- Modify: `README.md:368`

- [ ] **Step 1: INTEGRATION.md — note-types table (lines 312, 322)**

Change the heading on line 312 from `### Seven note types in three groups` to `### Eight note types in three groups`.

Add a `howto` row to the table immediately after the `glossary` row (line 319, keeping it in the durable group):

```
| `howto` | durable | operational runbook; slug key; update-in-place; `status: active`/`deprecated`; module/status encoded into `kb_index` `tags` (v0.9.0+) |
```

- [ ] **Step 2: INTEGRATION.md — v0.7.0 canonical layout (line 328)**

Edit the sentence so the plural-folder list and `<key>` clause include `howtos`:

```
Permalinks follow `<PROJECT>/<type>/<key>/main`. Type folders are **plural** (`epics`, `stories`, `decisions`, `modules`, `features`, `glossary`, `gotchas`, `howtos`); `<key>` is a `<TICKET-ID>` for epics/stories, a `<NNNN>-<slug>` (ADR-numbered) for decisions and gotchas, a `<slug>` for modules/features/howtos, and a `<term>` for glossary. Example: `monorepo/decisions/0001-text-only-reviewer/main`. The `plugin/bm-scribe/` plugin (v0.7.1+) auto-picks ADR numbers and enforces this layout. Date-prefix forms are a v0.6.x artifact — see conventions doc § 6 for migration.
```

- [ ] **Step 3: INTEGRATION.md — auto-apply ladder (after line 348)**

Add two rows immediately after the `glossary` create row (line 348), before the `contradicts_existing` row:

```
| `howto` create | **Human review** |
| `howto` update | **Human review** |
```

- [ ] **Step 4: conventions doc — folder list (line 43)**

Add `howtos` to the slug-keyed list:

```
All seven note types use `<PROJECT>/<type>/<key>/main` where `<key>` is either a slug (`decisions`, `modules`, `features`, `glossary`, `gotchas`, `howtos`) or a ticket ID (`epics`, `stories`). Gotchas use ADR-numbered slugs (`0042-graphql-n+1-on-driver-search`) like decisions, not ticket IDs; howtos use plain slugs like modules/features. The trailing `/main.md` allows arbitrary side-docs per ticket:
```

Then change the leading `All seven note types` to `All eight note types`.

- [ ] **Step 5: conventions doc — kb_index coverage (line 132)**

Change `Cover all seven note types when building kb_index … search across decisions, modules, features, glossary, epics, stories, and gotchas` to `all eight note types` and add `howtos`:

```
Cover all eight note types when building `kb_index` for a new plan — search across `decisions`, `modules`, `features`, `glossary`, `epics`, `stories`, `gotchas`, and `howtos` for entries matching the plan's `touches_modules`. In particular, include `<PROJECT>/gotchas/` and `<PROJECT>/howtos/` matches so accepted-and-superseded gotchas and active howtos surface alongside relevant decisions, modules, and features.
```

- [ ] **Step 6: top-level README — plugin skill count (THREE mentions: lines 72, 359, 368)**

`README.md` says "thirteen skills" in three places — update all three:

Line 72 (install step 9): change `standard `basic-memory` MCP tools with thirteen skills that enforce the` to `standard `basic-memory` MCP tools with fourteen skills that enforce the`.

Line 359 (Companion section intro): change `It wraps the standard `basic-memory` MCP tools with thirteen narrowly-scoped skills that enforce` to `It wraps the standard `basic-memory` MCP tools with fourteen narrowly-scoped skills that enforce`.

Line 368 (skill enumeration): replace with:

```
Verify with `claude plugin list`. The plugin exposes fourteen skills under the `bm-scribe:` namespace — eight project-knowledge creators (`create-epic`, `create-story`, `create-decision`, `create-module`, `create-feature`, `create-glossary`, `create-gotcha`, `create-howto`) plus six personal-namespace verbs (`add-todo`, `list-todos`, `tick-todo`, `add-note`, `fetch-note`, `list-notes`).
```

- [ ] **Step 7: Verify no stray counts remain in the living docs**

Sweep only the living surfaces that carry type/skill counts — NOT `CHANGELOG.md` (append-only history) or `docs/superpowers/plans/` + `specs/` (frozen design artifacts that legitimately say "seven"/"thirteen"):

```bash
grep -rni --include='*.md' \
  -e 'seven note types' -e 'seven project-knowledge' -e 'seven types in three' \
  -e 'thirteen skills' -e 'thirteen narrowly' \
  INTEGRATION.md README.md plugin/bm-scribe/README.md examples/project-knowledge/README.md docs/team-setup/
```

Expected: no output (all updated to eight / fourteen). If any line prints, fix it to match. (Run AFTER Task 6 too, since `plugin/bm-scribe/README.md` is updated there.)

- [ ] **Step 8: Commit**

```bash
git add INTEGRATION.md docs/team-setup/project-knowledge-conventions.md README.md
git commit -m "docs: document howto as the eighth note type (integration, conventions, readme)"
```

---

### Task 6: Add the `create-howto` skill and bump the plugin version

**Files:**
- Create: `plugin/bm-scribe/skills/create-howto/SKILL.md`
- Modify: `plugin/bm-scribe/README.md:14`, `:26`, subcommand table (after line 44)
- Modify: `plugin/bm-scribe/.claude-plugin/plugin.json:4`, `plugin/bm-scribe/package.json:3`, `plugin/bm-scribe/gemini-extension.json:4`

- [ ] **Step 1: Create `plugin/bm-scribe/skills/create-howto/SKILL.md`**

```markdown
---
name: bm-scribe:create-howto
description: Use when creating or updating a project-knowledge howto note (an operational runbook). Walks through the three-step BM v0.21.1 pattern and lands the note at the canonical v0.7.0 permalink `<PROJECT>/howtos/<slug>/main`.
---

# create-howto

Creates (or updates) a project-knowledge `howto` note at `<PROJECT>/howtos/<slug>/main` per the [v0.7.0 canonical layout](../../../../docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) and the [three-step pattern](../../docs/three-step-pattern.md). A `howto` is an operational runbook — the procedure the team should follow rather than rediscover. It is slug-keyed and updated in place (no ADR number, no supersede chain).

## Step 1 — Gather inputs

If the most recent `extract_project_knowledge` envelope in this conversation carries one or more `type: howto` proposals, offer to pre-fill from one (show its `title`, `modules`, and proposed `## Steps`). Otherwise gather interactively. Ask the user for:

- `<slug>` — kebab-case procedure name; required, comes from the invocation argument if provided.
- `title` — one-line human title.
- `modules` — list of module slugs this procedure touches.
- `when_to_use` — one or two sentences on the trigger (the `## When to use` section).
- `steps` — the ordered procedure (the load-bearing `## Steps` section).
- `verification` — how to confirm it worked (recommended).
- `prerequisites`, `rollback`, `related` — optional sections.

If `BM_SCRIBE_PROJECT` is unset, ask the user which BM project to write to and remember the answer for the rest of this session.

## Step 2 — Resolve project + permalink

- `<PROJECT>` = `$BM_SCRIBE_PROJECT` (or the answer from Step 1).
- Canonical permalink = `<PROJECT>/howtos/<slug>/main` (substitute `<PROJECT>` and `<slug>`).
- Directory portion (passed to `write_note`) = canonical permalink with the trailing `/main` stripped.

## Step 3 — Decide create vs update

```text
basic-memory:read_note(identifier="<PROJECT>/howtos/<slug>/main")
```

- If it returns a note → this is an UPDATE; go to **Step 3-update**.
- If it errors with "not found" → this is a CREATE; go to **Step 3-create**.

## Step 3-create — Issue the three-step BM call sequence

Follow [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md). Render the body with `## When to use` and `## Steps` (required), plus any supplied optional sections (`## Prerequisites`, `## Verification`, `## Rollback / if it goes wrong`, `## Related`).

```text
# Step 3a — create. BM ignores metadata.permalink.
basic-memory:write_note(
  title=<title>,
  directory="<PROJECT>/howtos/<slug>",
  note_type="howto",
  content=<rendered body>,
  metadata={
    permalink: "<PROJECT>/howtos/<slug>/main",
    status: "active",
    modules: <list>,
    last_verified: "<YYYY-MM-DD>",
  }
)
# Capture the returned permalink — call it AUTO_PERMALINK.

# Step 3b — relocate.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<PROJECT>/howtos/<slug>/main.md"
)

# Step 3c — read the moved note to capture the YAML permalink line verbatim.
basic-memory:read_note(identifier="<PROJECT>/howtos/<slug>/main")
# Extract the current `permalink: …` line; call it CURRENT_PERMALINK_LINE.

# Step 3d — rewrite the YAML permalink line.
basic-memory:edit_note(
  identifier="<PROJECT>/howtos/<slug>/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: <PROJECT>/howtos/<slug>/main"
)
```

## Step 3-update — Edit the existing howto in place

The note already lives at the canonical permalink — NO `move_note`, NO permalink rewrite.

```text
# Replace the procedure section with the new steps.
basic-memory:edit_note(
  identifier="<PROJECT>/howtos/<slug>/main",
  operation="replace_section",
  section="## Steps",
  content=<new steps>
)

# Bump the freshness stamp.
basic-memory:read_note(identifier="<PROJECT>/howtos/<slug>/main")
# Extract the current `last_verified: …` line; call it CURRENT_LAST_VERIFIED_LINE.
basic-memory:edit_note(
  identifier="<PROJECT>/howtos/<slug>/main",
  operation="find_replace",
  find_text=CURRENT_LAST_VERIFIED_LINE,
  replace_text="last_verified: <YYYY-MM-DD>"
)
```

To retire a dead procedure, `edit_note(find_replace)` flipping the frontmatter line `status: active` → `status: deprecated`.

## Step 4 — Verify

- `basic-memory:read_note(identifier="<PROJECT>/howtos/<slug>/main")` returns the note.
- The YAML `permalink:` equals `<PROJECT>/howtos/<slug>/main` exactly.
- For an update, `## Steps` reflects the new procedure and `last_verified` is today's date.
- Report success with the canonical permalink in paste-ready form: `[[<PROJECT>/howtos/<slug>/main]]`.
```

- [ ] **Step 2: Update `plugin/bm-scribe/README.md` skill count (lines 14, 26)**

Line 14: change `The plugin's thirteen skills become available` to `The plugin's fourteen skills become available`.

Line 26: change `with thirteen narrowly-scoped skills that enforce:` to `with fourteen narrowly-scoped skills that enforce:`.

- [ ] **Step 3: Add the `create-howto` row to the subcommand table**

Immediately after the `create-gotcha` row (line 44), insert:

```
| `create-howto <slug>` | Create or update a project-knowledge howto (operational runbook) at `<PROJECT>/howtos/<slug>/main`. |
```

- [ ] **Step 4: Bump the plugin version in all three manifests**

In `plugin/bm-scribe/.claude-plugin/plugin.json` (line 4), `plugin/bm-scribe/package.json` (line 3), and `plugin/bm-scribe/gemini-extension.json` (line 4), change `"version": "0.2.0"` to `"version": "0.3.0"`.

- [ ] **Step 5: Verify the three versions agree**

Run: `grep -h '"version"' plugin/bm-scribe/.claude-plugin/plugin.json plugin/bm-scribe/package.json plugin/bm-scribe/gemini-extension.json`
Expected: three lines, each `"version": "0.3.0",`.

- [ ] **Step 6: Commit**

```bash
git add plugin/bm-scribe/skills/create-howto/SKILL.md plugin/bm-scribe/README.md plugin/bm-scribe/.claude-plugin/plugin.json plugin/bm-scribe/package.json plugin/bm-scribe/gemini-extension.json
git commit -m "feat(bm-scribe): add create-howto skill; bump plugin to 0.3.0"
```

---

### Task 7: Full-suite verification

**Files:** none (verification only).

- [ ] **Step 1: Build and run the full test suite with the race detector**

Run: `go build ./... && go test -race ./...`
Expected: PASS across all packages, no build errors.

- [ ] **Step 2: Run the golden tests without `-update` to confirm goldens are committed in sync**

Run: `go test ./internal/prompts/...`
Expected: PASS (no golden drift — confirms Task 3's regenerated goldens match the committed template).

- [ ] **Step 3: Confirm no stray type-count or skill-count strings remain in the living docs**

Sweep only the living surfaces that carry counts. Deliberately EXCLUDE `CHANGELOG.md` (append-only history — its v0.8.0 entries legitimately say "Seven note types") and `docs/superpowers/plans/` + `specs/` (frozen design artifacts; the v0.8.0 gotcha plan legitimately says "seven note types" dozens of times):

```bash
grep -rni --include='*.md' \
  -e 'seven note types' \
  -e 'seven project-knowledge' \
  -e 'seven types in three' \
  -e 'thirteen skills' \
  -e 'thirteen narrowly' \
  INTEGRATION.md README.md plugin/bm-scribe/README.md examples/project-knowledge/README.md docs/team-setup/
```

Expected: no output. Any hit is a living doc this plan missed updating — fix it to read eight/fourteen. (Do NOT edit `CHANGELOG.md` history or the frozen gotcha plan/spec; those counts are correct as historical record.)

- [ ] **Step 4: Confirm the branch name matches the CHANGELOG**

Run: `git branch --show-current && head -10 CHANGELOG.md | grep '0.9.0'`
Expected: `version/0.9.0` and a `## [0.9.0] - 2026-05-29` line.

- [ ] **Step 5: Final commit (only if Steps 1-4 surfaced fixes)**

```bash
git add -A
git commit -m "chore: howto note type — final verification fixes"
```

If Steps 1-4 produced no changes, skip this step (nothing to commit).

---

## Self-Review notes (for the executor)

- **Spec coverage:** §4 architecture → Tasks 2-3, 6; §6 note shape → Tasks 3, 4, 6; §7 prime/kb_index → Task 5 (docs only, no code, as the spec mandates); §8 extract guidance → Task 3; §9 ladder → Task 5; §10 testing → Tasks 2-3, 7; §11 versioning → Tasks 1, 6.
- **No supersede:** every howto site uses `create`/`update` only — confirm no task introduces a `supersede` path for `howto`.
- **Type consistency:** the Go constant is `ProposalTypeHowto` (Task 2) and is referenced by that exact name in the tests (Task 2) and nowhere else; the folder is always plural `howtos`; the frontmatter fields are exactly `status` / `modules` / `last_verified` / `origin` across the template (Task 4), the skill (Task 6), and the reviewer guidance (Task 3).
- **Read side untouched:** no task edits `prime.go`, `prime.tmpl`, or `validate_plan` — howtos are found via the generic `module:<slug>` / `status:<value>` `kb_index` tags, identical to gotcha.
