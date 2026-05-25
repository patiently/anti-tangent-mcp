# Gotcha note type — v0.8.0 implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `gotcha` as a seventh project-knowledge note type — extract proposes it from completion envelopes, a new `bm-scribe:create-gotcha` skill walks it with the user (post-plan default) or mines it from review text (`--from-review`), and the existing `prime_project_knowledge` loop surfaces gotchas on future plans via canonical-encoded `tags` entries.

**Architecture:** Two surfaces. Anti-tangent (Go) gains one new `ProposalType` value (`gotcha`) wired through the enum, the parser switch, the JSON schema, the extract reviewer template (`internal/prompts/templates/extract.tmpl`), and the goldens — no schema extension for `action` / `supersedes` (those are already wired for the existing six types). The extract reviewer template DOES receive gotcha-specific additions in Task 4 (a durable-knowledge bullet update, type-enum extension, and two new instruction steps `3a-gotcha` / `3a-gotcha-supersede`); there is no rework of the prime-side prompts (`internal/prompts/templates/prime.tmpl`), no rework of any pre / mid / post lifecycle prompts, and no anti-tangent Go change on the prime read side. The bm-scribe plugin gains one new SKILL.md (`create-gotcha`) that takes structured proposals from the most recent `extract_project_knowledge` envelope OR mines raw review text via inline Claude prompts, then applies the standard three-step BM v0.21.1 creator pattern with ADR-numbered slugs. Read side requires zero anti-tangent code change — controllers encode `status:<value>` and `module:<slug>` into the existing `KBIndexEntryArg.Tags` array, and prime's reviewer ranks naturally on that.

**Tech Stack:** Go 1.x (tests use `-race`), MCP server, BM v0.21.1 MCP client, markdown skill contracts.

**Spec:** docs/superpowers/specs/2026-05-23-gotcha-note-type-design.md. Issue #35. PR #36 (draft, already open on `version/0.8.0`).

**Branch:** Work continues on `version/0.8.0` (already pushed). Do NOT create new branches.

**Commit-policy carve-outs (literal — the reviewer reads only this plan_text, not CLAUDE.md):**
- This repo's CI enforces that `version/X.Y.Z` branches must carry a matching `## [X.Y.Z] - YYYY-MM-DD` block in CHANGELOG.md.
- The `VERSION` file is NEVER bumped on a `version/*` branch — the release workflow auto-bumps it on merge to `main`. Pre-bumping `VERSION` on the branch causes the release-workflow CHANGELOG-validate step to fail. Hence Task 13 only APPENDS implementation entries to the existing `## [0.8.0]` block created by the spec commit; no `VERSION` edit is in scope.
- Docs-only commits are allowed without accompanying test changes (the golden regeneration in Task 5 is the test gate for the prompt-template change; SKILL.md / README / CHANGELOG / template-prose commits are docs-only and do not require tests).
- Plugin manifests (`plugin/bm-scribe/package.json`, `plugin/bm-scribe/gemini-extension.json`) version independently of anti-tangent; bumping bm-scribe from 0.1.0 → 0.2.0 in Task 8 is unrelated to anti-tangent's release axis.

**Supersede support is already wired for the existing six types (caller-verified):**
- `internal/verdict/extract.go:5-11` defines `ProposalAction` with three constants including `ProposalActionSupersede = "supersede"`.
- `internal/verdict/extract_parser.go:77` accepts `ProposalActionSupersede` in the action switch and rejects all other actions at line 79.
- `internal/verdict/extract_parser.go:130-155` enforces the supersede invariants: `supersedes: []` must be present (non-nil), and `action: "supersede"` requires non-empty `supersedes`.
- `internal/verdict/extract_schema.json:52` lists `"supersede"` in the `action` enum, and line 61 requires `supersedes` as an array of permalinks.
- `internal/prompts/templates/extract.tmpl:70, 117-121` already document the `action: "supersede"` shape and emit a paired `write_note` + `edit_note(frontmatter_patch)` `bm_commands` recipe for it.

**Therefore:** the only Go-side wire change this plan needs is the `proposals[].type` enum extension (Task 2) and the parser type-switch extension (Task 1). Task 4 introduces gotcha-specific reviewer instructions for the supersede path but does NOT require schema or parser changes for the action/supersedes fields — those flow through the existing wire shape.

**What this plan does NOT cover (intentional out-of-scope, controller-owned):**

The actual *production* of `kb_index` entries (the `KBIndexEntryArg` struct passed by callers to `prime_project_knowledge`) lives in the **calling controller**, not in anti-tangent. Anti-tangent is the MCP server that *receives* `kb_index` and ranks it; the controller is the orchestrating agent (superpowers' `subagent-driven-development`, hone-ai's equivalent, or any harness that builds the `kb_index` array before dispatch). Controllers consume the conventions doc shipped by Task 11 to learn the canonical `status:<value>` / `module:<slug>` encoding; updating each individual controller is downstream of this PR and explicitly out of scope.

This means: no dispatchable Go-side or unit-test acceptance criterion in this plan can prove end-to-end that a real `kb_index` for a gotcha note carries the expected `tags`. That verification lives in the **Manual verification section step 5** at the end of this plan — a one-time smoke against a real controller after this PR ships. Tasks 1-14 verify that the documentation, the reviewer template, the parser, the schema, and the bm-scribe writer are all aligned; the controller-side production change is downstream work tracked separately.

If you read this plan and think "but the read-side outcome isn't actually verified by a dispatchable task" — that's correct, and the reason is the producer lives outside this repository. Task 14 Step 2 verifies the *contract* (the conventions doc that controllers read) reached the branch; the manual smoke verifies a real controller honors that contract.

---

## File map

**Modify (Go server):**
- `internal/verdict/extract.go:13-22` — add `ProposalTypeGotcha` constant
- `internal/verdict/extract_parser.go:82` — extend the type-switch case list
- `internal/verdict/extract_schema.json:53` — extend the `enum` for `proposals[].type`
- `internal/verdict/extract_parser_test.go:468-510` — rename `AcceptsAllSixTypes` → `AcceptsAllSevenTypes`, add `gotcha` entries
- `internal/verdict/extract_parser_test.go` — append a new `TestParseExtract_AcceptsGotchaType` (mirrors `TestParseExtract_AcceptsStoryType`)
- `internal/prompts/templates/extract.tmpl:67-71, 84-85, 147` — broaden type enum mentions; add a short paragraph explaining the `gotcha` category
- `internal/prompts/testdata/extract_basic.golden` — regenerated
- `internal/prompts/testdata/extract_milestone.golden` — regenerated (if affected by the template diff)

**Create (Go server):**
- *(none — the new test function lives in the existing `extract_parser_test.go`)*

**Create (plugin):**
- `plugin/bm-scribe/skills/create-gotcha/SKILL.md` — new dual-mode creator skill
- `examples/project-knowledge/gotcha.md` — frontmatter+body template, mirrors `decision.md`

**Modify (plugin + docs):**
- `plugin/bm-scribe/README.md` — add `create-gotcha` row to the subcommand catalogue
- `plugin/bm-scribe/package.json` — bump `version` to `0.2.0`
- `plugin/bm-scribe/gemini-extension.json` — bump `version` to `0.2.0`
- `examples/project-knowledge/README.md` — extend "Six types in two layers" → seven; add `gotcha` row to maintenance-ownership table
- `docs/team-setup/project-knowledge-conventions.md` — add gotcha to the pre-dispatch search hints + the new "Encoding gotcha metadata into kb_index `tags`" subsection
- `INTEGRATION.md` — update "Six note types in two layers" table to seven (gotcha row)
- `CHANGELOG.md` — append per-section entries for the actual implementation work to the existing `## [0.8.0]` block

**Test:**
- `internal/verdict/extract_parser_test.go` (modify)
- `internal/prompts/prompts_test.go` (no edit expected; the `-update` regeneration covers golden tests)
- `internal/verdict/schema_invariants_test.go` (no edit expected; new enum value is structurally identical to existing ones)

---

## TDD posture for this plan

Every Go-side change ships as a TDD micro-cycle (write failing test → see it fail → make it pass → see it pass → commit). Doc / template / SKILL.md changes are not unit-testable in the project's test-runner sense; for those we use the `-update` golden regeneration as the test gate plus a diff review before commit.

`go test -race ./...` is the mainline command per `CLAUDE.md`. The bm-scribe skill ships unverified by Go tests (skills are markdown contracts); verification is a manual smoke against a real BM, called out at the end.

---

### Task 1: Add `ProposalTypeGotcha` enum + parser switch (TDD)

**Goal:** The verdict package exposes a `ProposalTypeGotcha` constant and the extract parser accepts `"gotcha"` as a valid `proposals[].type` round-trip value.

**Acceptance criteria:**
- `verdict.ProposalTypeGotcha` constant exists with the string value `"gotcha"`.
- New test `TestParseExtract_AcceptsGotchaType` passes (round-trips a fully-shaped gotcha proposal through `verdict.ParseExtract` and asserts `Proposals[0].Type == verdict.ProposalTypeGotcha`).
- `go test -race ./internal/verdict/...` is green after the change.
- The existing six `ProposalType*` constants (`Decision`, `Module`, `Feature`, `Glossary`, `Epic`, `Story`) are unchanged in value and ordering relative to each other; only `ProposalTypeGotcha` is added at the end.
- No change to `ProposalAction` constants or to the parser's action switch — the existing `ProposalActionSupersede` already covers gotcha supersede proposals (see "Supersede support is already wired" callout at the top of this plan).

**Non-goals:**
- Schema enum update (Task 2).
- Renaming the AllSixTypes test (Task 3).
- Template / golden changes (Tasks 4, 5).
- Any change to `ProposalAction` or to the schema's `action` enum.

**Context:**
- `internal/verdict/extract.go:13-22` defines the existing `ProposalType` block as a six-constant `const ( ... )`.
- `internal/verdict/extract_parser.go:82` carries the type-switch allowlist; the default branch at line 83-84 rejects unknown types with `proposal[N]: invalid type "<value>"`.
- The existing `TestParseExtract_AcceptsStoryType` (referenced as the mirror for the new test) is the structural shape to follow.

**Files:**
- Modify: `internal/verdict/extract.go:13-22`
- Modify: `internal/verdict/extract_parser.go:82`
- Test: `internal/verdict/extract_parser_test.go` (append new test function)

- [ ] **Step 1: Write the failing test `TestParseExtract_AcceptsGotchaType`**

Append at the end of `internal/verdict/extract_parser_test.go` (after `TestParseExtract_AcceptsAllSixTypes`, before the final closing brace if any; place at the bottom of the file):

```go
func TestParseExtract_AcceptsGotchaType(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [{
			"action": "create",
			"type": "gotcha",
			"permalink": "monorepo/gotchas/0042-graphql-n+1-on-driver-search/main",
			"title": "GraphQL N+1 on driver-search",
			"frontmatter_json": "{\"status\":\"accepted\",\"modules\":[\"driver-search\"],\"severity\":\"medium\",\"discovered_at\":\"2026-05-23\"}",
			"body": "## Symptom\n\nN+1 on driver lookup.\n\n## Root cause\n\nResolver fans out per driver.\n\n## How to avoid\n\nUse a DataLoader.\n\n## Evidence\n\n- completion_envelopes[0].final_files[0]\n\n## Related\n\n- [[monorepo/modules/driver-search/main]]",
			"body_patch": "",
			"rationale": "Documents the N+1 surfaced during the search-perf story",
			"evidence_refs": ["completion_envelopes[0].summary"],
			"supersedes": []
		}],
		"bm_commands": [],
		"next_action": "Apply the gotcha note before next milestone."
	}`)
	r, err := verdict.ParseExtract(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(r.Proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(r.Proposals))
	}
	if r.Proposals[0].Type != verdict.ProposalTypeGotcha {
		t.Fatalf("expected type gotcha, got %q", r.Proposals[0].Type)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails (both compile + runtime path)**

Run: `go test -race -run TestParseExtract_AcceptsGotchaType ./internal/verdict/...`

Expected: compile error `undefined: verdict.ProposalTypeGotcha`. That's the failing-test signal. (TDD via "doesn't compile" is fine; the missing symbol IS the red.)

- [ ] **Step 3: Add the enum constant**

Edit `internal/verdict/extract.go` lines 15-22. Add a new constant `ProposalTypeGotcha` immediately after `ProposalTypeStory`:

```go
const (
	ProposalTypeDecision ProposalType = "decision"
	ProposalTypeModule   ProposalType = "module"
	ProposalTypeFeature  ProposalType = "feature"
	ProposalTypeGlossary ProposalType = "glossary"
	ProposalTypeEpic     ProposalType = "epic"
	ProposalTypeStory    ProposalType = "story"
	ProposalTypeGotcha   ProposalType = "gotcha"
)
```

- [ ] **Step 4: Re-run the test — still fails, but now with a different error**

Run: `go test -race -run TestParseExtract_AcceptsGotchaType ./internal/verdict/...`

Expected: compiles, but the test now fails with `proposal[0]: invalid type "gotcha"` (from the type-switch default branch in `extract_parser.go:82-84`).

- [ ] **Step 5: Extend the type switch in `extract_parser.go`**

Edit `internal/verdict/extract_parser.go:82`. Replace the case line with:

```go
		case ProposalTypeDecision, ProposalTypeModule, ProposalTypeFeature, ProposalTypeGlossary, ProposalTypeEpic, ProposalTypeStory, ProposalTypeGotcha:
```

(Adding `ProposalTypeGotcha` at the end of the case list.)

- [ ] **Step 6: Re-run the test — should pass now**

Run: `go test -race -run TestParseExtract_AcceptsGotchaType ./internal/verdict/...`

Expected: `PASS`.

- [ ] **Step 7: Commit**

```bash
git add internal/verdict/extract.go internal/verdict/extract_parser.go internal/verdict/extract_parser_test.go
git commit -m "feat(verdict): add ProposalTypeGotcha enum + parser switch (v0.8.0)

Wire the 7th project-knowledge note type, gotcha, through the
ProposalType enum and the extract parser's type-switch allowlist.
Test TestParseExtract_AcceptsGotchaType (mirrors AcceptsStoryType) round-trips
a fully-shaped gotcha proposal through ParseExtract.

Spec: docs/superpowers/specs/2026-05-23-gotcha-note-type-design.md §4-§6.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Extend `extract_schema.json` enum

**Goal:** The JSON Schema sent to the reviewer accepts `"gotcha"` as a valid `proposals[].type` value, aligned with the Go enum from Task 1.

**Acceptance criteria:**
- `internal/verdict/extract_schema.json` line 53 has `["decision", "module", "feature", "glossary", "epic", "story", "gotcha"]` as the enum for `proposals[].type`.
- `TestSchemaInvariants` continues to pass (baseline pre-change AND post-change).
- The Task 1 round-trip test still passes (no regression introduced by the schema diff).
- No change to the schema's `action` enum (still `["create", "update", "supersede"]` per `extract_schema.json:52`) and no change to the `supersedes` array definition — the existing schema already accepts the supersede shape that Task 4's reviewer instructions will exercise for gotchas.

**Non-goals:**
- Any change to extract.go / extract_parser.go (Task 1 already done).
- Any change to extract.tmpl (Task 4).
- Any change to the `action` or `supersedes` schema definitions.

**Context:** `TestSchemaInvariants` exercises the schema generically — it does not hardcode the six-type list; adding an enum value is structurally identical to the existing ones and should pass without test edits.

**Files:**
- Modify: `internal/verdict/extract_schema.json:53`
- Test: `internal/verdict/schema_invariants_test.go` (no edit — exercises invariants generically)

- [ ] **Step 1: Run the schema invariants test BEFORE the change to confirm baseline pass**

Run: `go test -race -run TestSchemaInvariants ./internal/verdict/...`

Expected: `PASS`.

- [ ] **Step 2: Update the enum line in `extract_schema.json`**

Edit `internal/verdict/extract_schema.json:53`. Replace:

```json
          "type":             { "type": "string", "enum": ["decision", "module", "feature", "glossary", "epic", "story"] },
```

with:

```json
          "type":             { "type": "string", "enum": ["decision", "module", "feature", "glossary", "epic", "story", "gotcha"] },
```

- [ ] **Step 3: Re-run schema invariants test**

Run: `go test -race -run TestSchemaInvariants ./internal/verdict/...`

Expected: `PASS`.

- [ ] **Step 4: Re-run the Task 1 gotcha round-trip test to confirm parse still works**

Run: `go test -race -run TestParseExtract_AcceptsGotchaType ./internal/verdict/...`

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/verdict/extract_schema.json
git commit -m "feat(verdict): add 'gotcha' to extract_schema.json proposals[].type enum

Aligns the JSON Schema sent to the reviewer (strict structured-outputs
contract) with the Go ProposalType enum added in the previous commit.
Without this, a reviewer emitting type: gotcha would be schema-rejected
upstream of the parser.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Update `TestParseExtract_AcceptsAllSixTypes` → `AcceptsAllSevenTypes`

**Goal:** The table-driven round-trip test covers all seven note types in one function, replacing the old six-type version.

**Acceptance criteria:**
- Function `TestParseExtract_AcceptsAllSevenTypes` exists at the location previously occupied by `TestParseExtract_AcceptsAllSixTypes` in `internal/verdict/extract_parser_test.go`.
- The new function iterates over the seven type strings (`decision`, `module`, `feature`, `glossary`, `epic`, `story`, `gotcha`) and round-trips each through `verdict.ParseExtract`.
- The `pathSeg` map within the function contains all seven entries with the canonical plural / singular forms (`gotcha` → `gotchas`).
- `go test -race -run TestParseExtract_AcceptsAllSevenTypes ./internal/verdict/...` is green with seven sub-test pass lines.
- `go test -race -run TestParseExtract_AcceptsAllSixTypes ./internal/verdict/...` reports `no tests to run` (the old function name is gone).

**Non-goals:**
- Any change to enum or schema or template (covered by Tasks 1, 2, 4).
- Any change to other test functions in `extract_parser_test.go` (including the Task 1 `TestParseExtract_AcceptsGotchaType` which remains separate).

**Files:**
- Modify: `internal/verdict/extract_parser_test.go:468-510`

- [ ] **Step 1: Rename the test function and extend the type list**

Edit `internal/verdict/extract_parser_test.go` lines 468-510. Replace the entire function with:

```go
func TestParseExtract_AcceptsAllSevenTypes(t *testing.T) {
	// Path-segment differs from the type name for `glossary` (singular) and
	// `story` (plural is "stories"). Use an explicit map rather than
	// `tc.typ + "s"` to avoid generating malformed permalinks like
	// `glossarys` / `storys`.
	pathSeg := map[string]string{
		"decision": "decisions",
		"module":   "modules",
		"feature":  "features",
		"glossary": "glossary",
		"epic":     "epics",
		"story":    "stories",
		"gotcha":   "gotchas",
	}
	types := []string{"decision", "module", "feature", "glossary", "epic", "story", "gotcha"}
	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			raw := []byte(`{
				"verdict": "pass",
				"findings": [],
				"proposals": [{
					"action": "create",
					"type": "` + typ + `",
					"permalink": "monorepo/` + pathSeg[typ] + `/abc/main",
					"title": "round-trip ` + typ + `",
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
				t.Fatalf("type %q: parse error: %v", typ, err)
			}
			if len(r.Proposals) != 1 || string(r.Proposals[0].Type) != typ {
				t.Fatalf("type %q: round-trip failed, got %+v", typ, r.Proposals)
			}
		})
	}
}
```

- [ ] **Step 2: Run the renamed test**

Run: `go test -race -run TestParseExtract_AcceptsAllSevenTypes ./internal/verdict/...`

Expected: `PASS` (seven sub-tests, including `gotcha`).

- [ ] **Step 3: Confirm the old name no longer exists (so we haven't dual-shipped)**

Run: `go test -race -run TestParseExtract_AcceptsAllSixTypes ./internal/verdict/...`

Expected: `testing: warning: no tests to run` (the old name is gone — that's correct, not a regression).

- [ ] **Step 4: Commit**

```bash
git add internal/verdict/extract_parser_test.go
git commit -m "test(verdict): rename AcceptsAllSixTypes -> AcceptsAllSevenTypes (v0.8.0)

Add 'gotcha' to the round-trip type list and to the pathSeg map
(plural form 'gotchas' for the permalink segment). The renamed test
now serves as the regression baseline for the seven-type taxonomy.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Update `extract.tmpl` to teach the reviewer about `gotcha`

**Goal:** The extract reviewer template instructs the model on when and how to emit `type: gotcha` proposals, including frontmatter shape, body template, and supersede mechanics (reusing the existing `action: "supersede"` + `supersedes: [...]` wire shape already documented in the template's step 7 BM mapping).

**Acceptance criteria:**
- The durable-knowledge bullet at line 67 mentions gotchas with concrete examples (N+1, CSV parser quirk, deploy race) in addition to decision/module/feature/glossary.
- The `type` enum bullet at line 71 lists all seven values including `gotcha`.
- Two new instruction steps `3a-gotcha` and `3a-gotcha-supersede` appear immediately BEFORE the existing `3a. **Dashboard updates require a milestone.**` step at line 85.
- The output-schema example block at line 147 includes `gotcha` in the `type` enum union (rendered as `decision|module|feature|glossary|epic|story|gotcha`).
- The template still references the existing `action: "supersede"` / `supersedes: [...]` wire shape — no new schema/action fields are introduced.

**Non-goals:**
- Regenerating goldens (Task 5).
- Any change to extract.go / parser / schema (Tasks 1-3 already done).
- Any new action verb beyond the existing `create | update | supersede`.

**Context:** The split between template-edit commit (this task) and golden-regeneration commit (Task 5) is intentional: it keeps the golden diff reviewable as a pure mechanical artifact. The `3a-gotcha` step text is normative — it is the contract the reviewer reads at runtime. Section names (`## Symptom`, `## Root cause`, `## How to avoid`, `## Evidence`) must match the body-template in Task 7's create-gotcha skill and Task 9's gotcha.md example, since prime excerpts the `## How to avoid` section onto future plans.

**Files:**
- Modify: `internal/prompts/templates/extract.tmpl:67, 71, 84-85, 147`

**NORMATIVE TEST BODIES (verbatim):**

```
3a-gotcha. **Gotcha proposals (`type: gotcha`).** A `gotcha` is a project-and-module-scoped lesson learned during implementation: surprises, regressions caught in review, footguns the codebase has, environmental quirks, and similar lore that future plans on the same module(s) should know about before writing code. When you propose a `gotcha`:
    - Set `permalink` to `<PROJECT>/gotchas/<NNNN>-<slug>/main`, where `<NNNN>` is the next zero-padded ADR-style number across existing `<PROJECT>/gotchas/` entries in `kb_index` (start at `0001` if none). Use a kebab-case `<slug>` derived from the title.
    - Set `frontmatter_json` to include at minimum: `status: "accepted"`, `modules: ["<slug>", ...]` (one or more module slugs visible in the envelopes or `kb_index`), `severity: "low" | "medium" | "high"`, `discovered_at: "<YYYY-MM-DD>"`. Optional but recommended: `origin: "<PROJECT>/stories/<TICKET-ID>/main"` (or epic/PR permalink) pointing at the work that surfaced it; `supersedes: []` (or a predecessor permalink array if this replaces an earlier gotcha — see step 3a-gotcha-supersede below).
    - Structure the `body` with FOUR required sections in this order: `## Symptom`, `## Root cause`, `## How to avoid`, `## Evidence`. Optionally add `## Related` with `[[wikilinks]]` to relevant module / story / epic notes. The `## How to avoid` section is the load-bearing one — it carries the actionable rule that prime will excerpt into the next plan's `project_knowledge`.
    - `rationale`: one sentence explaining why this gotcha is worth recording (not just descriptive — capture the cost of NOT recording it).
    - `evidence_refs`: at least one ref into the supplied envelopes pointing at the source of the lesson (a finding, a final-file diff, a summary line). Do not fabricate evidence.

3a-gotcha-supersede. **Superseding an existing gotcha.** If a completion envelope describes new understanding that contradicts an existing gotcha's "Root cause" or "How to avoid" (for example, the team found the *real* root cause was a different invariant, or the recommended fix has been replaced by a better one), emit `action: "supersede"` with `supersedes: ["<predecessor permalink>"]` rather than overwriting silently. The predecessor must be a `type: gotcha` note visible in `kb_index`. Use the same supersede `bm_commands` mapping documented in step 7 (a `write_note` for the new note + an `edit_note` with `frontmatter_patch` flipping the predecessor's `status` to `superseded`).
```

- [ ] **Step 1: Update the durable-knowledge line to mention gotchas**

Edit `internal/prompts/templates/extract.tmpl:67`. Replace the bullet:

```
   - **Durable knowledge** worth capturing as a new note: an architectural decision, a module's invariants, a new feature's behavior, a glossary term. Always proposable; not gated on milestones.
```

with:

```
   - **Durable knowledge** worth capturing as a new note: an architectural decision, a module's invariants, a new feature's behavior, a glossary term, or a **gotcha** (a project-and-module-scoped lesson learned — an N+1 query that bit us, a CSV parser that strips trailing newlines, a deploy step that races a cache invalidation). Always proposable; not gated on milestones.
```

- [ ] **Step 2: Update the `type` enum line in the "What to do" instructions**

Edit `internal/prompts/templates/extract.tmpl:71`. Replace:

```
   - `type`: one of `decision`, `module`, `feature`, `glossary`, `epic`, `story`.
```

with:

```
   - `type`: one of `decision`, `module`, `feature`, `glossary`, `epic`, `story`, `gotcha`.
```

- [ ] **Step 3: Add the two new instruction steps from the NORMATIVE TEST BODIES block above**

Edit `internal/prompts/templates/extract.tmpl`. Locate the existing instruction step `3a. **Dashboard updates require a milestone.**` at line 85. Immediately BEFORE that line (i.e. between the end of step 3 around line 84 and the start of step 3a at line 85), insert the two normative blocks (`3a-gotcha.` and `3a-gotcha-supersede.`) VERBATIM from the **NORMATIVE TEST BODIES (verbatim)** block above this Steps list.

- [ ] **Step 4: Update the output-schema example block**

Edit `internal/prompts/templates/extract.tmpl:147`. Replace:

```
      "type": "decision|module|feature|glossary|epic|story",
```

with:

```
      "type": "decision|module|feature|glossary|epic|story|gotcha",
```

- [ ] **Step 5: Confirm the template change by running the existing prompt tests in non-update mode (expected to fail — goldens are out of sync)**

Run: `go test -race ./internal/prompts/...`

Expected: golden-comparison tests FAIL because the rendered template now differs from the on-disk goldens. That's the expected red — the goldens haven't been regenerated yet. The failure message will name the differing golden files. Goldens will be regenerated in Task 5.

(This step is verification only — its outcome is the trigger for Task 5, not an acceptance criterion of Task 4.)

- [ ] **Step 6: Commit the template change separately from the goldens**

We split the template change and the golden regeneration into two commits so the regeneration commit is mechanical and reviewable as a pure diff against the previous golden.

```bash
git add internal/prompts/templates/extract.tmpl
git commit -m "feat(prompts): teach extract.tmpl about the 'gotcha' note type (v0.8.0)

- Extend the durable-knowledge bullet to mention gotchas with examples.
- Add 'gotcha' to the type enum on line 71 of the instruction block.
- Add steps 3a-gotcha and 3a-gotcha-supersede explaining the shape:
  ADR-style permalink, required frontmatter, four-section body
  template, supersede mechanics (reusing the existing action/supersedes
  wire shape and the step-7 BM mapping).
- Add 'gotcha' to the output-schema example's type enum.

Goldens regenerated in the next commit (split for reviewability).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Regenerate extract golden files

**Goal:** The on-disk extract golden files are regenerated to match the template edits from Task 4, so the prompt tests are green again.

**Acceptance criteria:**
- `internal/prompts/testdata/extract_basic.golden` is regenerated and contains the new gotcha-related text (durable-knowledge bullet, expanded type enum, new instruction steps).
- `internal/prompts/testdata/extract_milestone.golden` is regenerated if affected, OR is unchanged if the milestone template re-includes a different block.
- The diff against the previous goldens contains ONLY gotcha-related text changes — no unrelated whitespace churn, no other instruction-block edits.
- `go test -race ./internal/prompts/...` is green after regeneration.

**Non-goals:**
- Any change to extract.tmpl (Task 4).
- Any other golden file regeneration (e.g. plan/pre/mid/post goldens).

**Files:**
- Modify: `internal/prompts/testdata/extract_basic.golden`
- Modify: `internal/prompts/testdata/extract_milestone.golden` (only if the diff touches it)

- [ ] **Step 1: Regenerate goldens with `-update`**

Run: `go test ./internal/prompts/... -update`

Expected: tests pass; the two `extract_*.golden` files are rewritten in place.

- [ ] **Step 2: Inspect the diff to confirm ONLY the gotcha-related text changed**

Run: `git diff internal/prompts/testdata/`

Expected diff:
- `extract_basic.golden`: the bullet on durable knowledge gains the gotcha clause; the `type` enum lines gain `, gotcha` / `|gotcha`; the new steps `3a-gotcha` and `3a-gotcha-supersede` appear as multi-line blocks.
- `extract_milestone.golden`: same set of textual changes if the milestone template re-renders the same instructions block; if it doesn't, the file may be unchanged.

There must be NO unrelated diff (no whitespace churn elsewhere, no other instruction-block changes). If there are unrelated diffs, STOP and investigate before committing.

- [ ] **Step 3: Re-run prompt tests in normal mode**

Run: `go test -race ./internal/prompts/...`

Expected: `PASS`.

- [ ] **Step 4: Commit the regenerated goldens**

```bash
git add internal/prompts/testdata/extract_basic.golden internal/prompts/testdata/extract_milestone.golden
git commit -m "test(prompts): regenerate extract goldens for 'gotcha' template change

Mechanical regeneration via 'go test ./internal/prompts/... -update'
after the extract.tmpl edits in the previous commit. Diff is scoped
to: gotcha bullet in durable-knowledge step, expanded type enum on
two lines, the new 3a-gotcha and 3a-gotcha-supersede instruction
blocks.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

(If `git diff --cached --stat` shows only one golden changed because the milestone template re-includes a different block, drop the second file from the `git add` line — the commit message is otherwise the same.)

---

### Task 6: Run the full Go test suite

**Goal:** The full Go test suite is green after Tasks 1-5; no regression has been introduced elsewhere in the codebase, and the two new tests from Tasks 1 and 3 are observably running.

**Acceptance criteria:**
- `go test -race ./...` exits 0 with all packages reporting `ok` / `PASS`.
- A separate verbose check `go test -race -v -run 'TestParseExtract_(AcceptsGotchaType|AcceptsAllSevenTypes)' ./internal/verdict/...` exits 0 and prints both test names in its output (proving the new and renamed tests are observably discovered, not skipped or filtered).
- No failure elsewhere (e.g. a test that hardcoded the six-type list).

**Non-goals:**
- Any code change. This task is a verification checkpoint only — no commit if green.

**Files:** (none — verification only)

- [ ] **Step 1: Run the mainline test command**

Run: `go test -race ./...`

Expected: all packages `PASS` (or `ok`).

- [ ] **Step 2: Run the targeted verbose check that the new and renamed tests are present**

Run: `go test -race -v -run 'TestParseExtract_(AcceptsGotchaType|AcceptsAllSevenTypes)' ./internal/verdict/...`

Expected: output contains both `=== RUN   TestParseExtract_AcceptsGotchaType` and `=== RUN   TestParseExtract_AcceptsAllSevenTypes` lines, followed by `--- PASS:` for each.

- [ ] **Step 3: If any test fails, STOP and fix before continuing**

Common causes if something fails here:
- A test elsewhere hardcodes the list of six types (grep for `"decision".*"module".*"feature".*"glossary".*"epic".*"story"` to find).
- A test asserts a specific number of types (grep for `len(.*)\\s*==\\s*6` in `_test.go` files).
- A golden test other than the extract goldens was affected (grep `git diff` for `.golden` files we didn't expect).

For each failure, treat it as the next sub-task and address before moving on. Do not advance to Task 7 with a red `./...` suite.

- [ ] **Step 4: No commit needed for a green run — this is a checkpoint, not a code change**

---

### Task 7: Create `plugin/bm-scribe/skills/create-gotcha/SKILL.md`

**Goal:** A new bm-scribe skill (`bm-scribe:create-gotcha`) exists with dual-mode intake (Path A from extract envelope, Path B from review text via `--from-review pr:<N>|<file>|paste:`), applies the three-step BM v0.21.1 creator pattern with auto-picked ADR number, enforces the four-section body template, and handles supersede chains.

**Acceptance criteria:**
- `plugin/bm-scribe/skills/create-gotcha/SKILL.md` exists with valid YAML frontmatter (`name: bm-scribe:create-gotcha`, populated `description`).
- The skill documents two intake modes: default (Path A — reads extract envelope) and `--from-review <source>` (Path B — accepts `pr:<N>` / filesystem path / `paste:`).
- The skill specifies a concrete Path A retrieval contract: the skill reads the most recent `extract_project_knowledge` MCP tool-call response visible in the current conversation context (i.e. the most recent assistant turn whose tool-use envelope's tool name equals `extract_project_knowledge` or `mcp__anti-tangent__extract_project_knowledge`). If no such envelope is visible in the current conversation, the skill MUST NOT silently exit — it MUST ask the user to either (a) paste the most recent extract envelope JSON directly via heredoc (terminated by `EOF`), or (b) supply a filesystem path to a saved envelope file, or (c) switch to `--from-review <source>` mode.
- The skill applies the canonical BM creator pattern documented in `plugin/bm-scribe/docs/three-step-pattern.md`. The pattern is named "three-step" because it has three logical phases — **create** (`write_note`), **relocate** (`move_note`), and **rewrite-permalink** — even though the third phase is implemented as two BM calls in sequence (`read_note` to capture the auto-derived permalink line verbatim, then `edit_note(find_replace)` to rewrite it to the canonical form). The skill's Step 3 must show all four BM calls labelled as Steps 3a / 3b / 3c / 3d, matching `create-decision`'s convention.
- The skill auto-picks the next zero-padded four-digit ADR number by querying `basic-memory:search_notes` with prefix `<PROJECT>/gotchas/` (matches `create-decision`'s logic at sub-step 2.2 of `plugin/bm-scribe/skills/create-decision/SKILL.md`).
- The skill enforces a four-section body template: `## Symptom`, `## Root cause`, `## How to avoid`, `## Evidence` (+ optional `## Related`).
- The skill handles supersede chains: when the user names a predecessor, a follow-up `edit_note(find_replace)` flips the predecessor's `status:` line to `superseded`; on failure, the skill prints a warning and exits without rolling back the new note.

**Non-goals:**
- Plugin manifest version bumps (Task 8).
- Catalogue table update in `plugin/bm-scribe/README.md` (Task 8).
- Go-side tests for the skill (skills are markdown contracts; verification is the manual smoke at end of plan).

**Context:** The structural shape mirrors `plugin/bm-scribe/skills/create-decision/SKILL.md` — same Step 1 (gather inputs) / Step 2 (resolve project + permalink) / Step 3 (three-step BM sequence) / Step 4 (verify) backbone. The mining prompt in Path B runs against the host Claude model via inline prompt — there is no new MCP tool; the skill is pure markdown contract.

**Files:**
- Create: `plugin/bm-scribe/skills/create-gotcha/SKILL.md`

- [ ] **Step 1: Create the directory and the skill file**

```bash
mkdir -p plugin/bm-scribe/skills/create-gotcha
```

Write `plugin/bm-scribe/skills/create-gotcha/SKILL.md` with the following exact content:

````markdown
---
name: bm-scribe:create-gotcha
description: Use when creating a new project-knowledge gotcha note (a module-scoped lesson learned). Walks through the three-step BM v0.21.1 pattern and lands the note at the canonical v0.7.0 permalink `<PROJECT>/gotchas/<NNNN>-<slug>/main`. Two intake modes — default reads structured proposals from the most recent `extract_project_knowledge` envelope in the current conversation; `--from-review <source>` mines gotcha candidates from review text (PR comments, ultrareview / code-review / security-review output).
---

# create-gotcha

Creates a project-knowledge `gotcha` note at `<PROJECT>/gotchas/<NNNN>-<slug>/main` per the [v0.8.0 design](../../../../docs/superpowers/specs/2026-05-23-gotcha-note-type-design.md) and the [three-step pattern](../../docs/three-step-pattern.md).

Gotchas are module-scoped lessons learned during implementation or review — the kind of finding that's easy to re-introduce on the next plan unless someone wrote it down. Prime surfaces them on future plans touching the same module(s) via canonical-encoded `tags` entries in the controller's `kb_index` (see [`docs/team-setup/project-knowledge-conventions.md`](../../../../docs/team-setup/project-knowledge-conventions.md)).

## Intake modes

Choose the mode based on the invocation argument:

| Invocation | Mode | Candidate source |
|---|---|---|
| `/bm-scribe:create-gotcha` (no arg, default) | **Path A — post-plan** | Structured `Proposal{type: "gotcha", ...}` entries from the most recent `extract_project_knowledge` envelope in the current conversation context. |
| `/bm-scribe:create-gotcha --from-review pr:<N>` | **Path B — post-review (PR)** | All review-shaped comments on PR `<N>` of the current repo, fetched via `gh api`. |
| `/bm-scribe:create-gotcha --from-review <filesystem-path>` | **Path B — post-review (file)** | The file's contents read as plain text. |
| `/bm-scribe:create-gotcha --from-review paste:` | **Path B — post-review (paste)** | Multi-line stdin (heredoc) the user pastes interactively. |

If no recent extract envelope is visible AND no `--from-review` flag is given, ask the user which mode they want and re-invoke.

## Step 1 — Gather inputs

If `BM_SCRIBE_PROJECT` is unset, ask the user which BM project to write to and remember the answer for the rest of this session.

### Step 1 — Path A (post-plan) retrieval contract

Locate the most recent `extract_project_knowledge` MCP tool-call response in the current conversation context — concretely, the most recent assistant turn whose tool-use block has a tool name equal to either `extract_project_knowledge` or `mcp__anti-tangent__extract_project_knowledge`. The envelope is the JSON returned in that tool's response (the same JSON the controller would have parsed into an `ExtractResult`).

If no such envelope is visible in the current conversation context, do NOT silently exit. Print:

> No `extract_project_knowledge` envelope visible in this conversation. Choose:
>   (a) paste the envelope JSON directly (end with `EOF` on its own line)
>   (b) supply a filesystem path to a saved envelope file
>   (c) re-invoke with `--from-review <source>` instead

Wait for the user's response. For (a), read a heredoc terminated by `EOF`. For (b), read the named file as UTF-8 and parse as JSON. For (c), exit cleanly so the user can re-invoke.

Once an envelope is in hand, filter its `proposals` array to entries with `type == "gotcha"`. If the filter returns zero entries, print:

> No `gotcha`-typed proposals in the extract envelope. Did you mean `/bm-scribe:create-gotcha --from-review <source>`?

…and exit cleanly (not an error).

For each gotcha-typed proposal, present this summary to the user:

```
Proposal N/M
  Title:    <proposal.title>
  Permalink: <proposal.permalink>     (the controller may renumber; see Step 2)
  Modules:   <parsed from frontmatter_json>
  Severity:  <parsed from frontmatter_json, or "(unset)">
  Origin:    <parsed from frontmatter_json, or "(unset)">
  Rationale: <proposal.rationale>
  Evidence:  <proposal.evidence_refs joined>
  Body preview:
    <first ~10 lines of proposal.body>
```

Ask:

> Accept this proposal, edit it, or skip it? [a/e/s]

- `a` → carry the proposal forward to Step 2 unchanged.
- `e` → prompt for edits to title / modules / severity / origin / body, then carry forward.
- `s` → skip this proposal, advance to the next.

If the user marks any proposal as superseding an existing gotcha (ask: "Does this gotcha supersede an existing note? Paste the predecessor's permalink or press enter to skip"), capture the predecessor permalink for Step 3 supersede handling.

### Step 1 — Path B (post-review)

Resolve `<source>` to raw `review_text`:

- `pr:<N>` — fetch via:
  ```bash
  gh api "repos/$(gh repo view --json nameWithOwner -q .nameWithOwner)/issues/<N>/comments" \
    --jq '.[] | "[\(.user.login)] \(.body)"'
  gh api "repos/$(gh repo view --json nameWithOwner -q .nameWithOwner)/pulls/<N>/reviews" \
    --jq '.[] | select(.body != "" and .body != null) | "[\(.user.login) review] \(.body)"'
  gh api "repos/$(gh repo view --json nameWithOwner -q .nameWithOwner)/pulls/<N>/comments" \
    --jq '.[] | "[\(.user.login) inline @ \(.path):\(.line // 0)] \(.body)"'
  ```
  Concatenate the three outputs in that order. Dedup comments where two endpoints return the same `id`. If any `gh` call fails (auth missing, network), fall back to asking the user:
  > Could not reach PR <N> via gh. Paste the review text directly (end with EOF on its own line):

- Filesystem path — read the file as UTF-8.
- `paste:` — collect a heredoc from stdin (everything until a line containing only `EOF`).

Build the inline mining prompt using:

```
You are extracting "gotchas" from code-review feedback. A gotcha is a
module-scoped lesson learned — a surprise, a regression caught in review,
a codebase footgun, an environmental quirk — that a future plan touching
the same module should know before writing code.

Given the review text below and the list of known module slugs in this
project, return a JSON array of gotcha candidates. Each candidate has
the shape:

  {
    "title": "<one-line human title>",
    "modules": ["<slug>", ...],         // pick from the module list; can be empty if no clean match
    "severity": "low" | "medium" | "high",
    "symptom": "<one paragraph>",
    "root_cause": "<one paragraph>",
    "how_to_avoid": "<one paragraph — the load-bearing actionable rule>"
  }

Return ONLY the JSON array. If no review comment carries gotcha-shaped
signal, return [].

Known module slugs in this project:
<comma-separated list from kb_index entries with type=module>

Review text:
<concatenated review_text>
```

Run that prompt against the host Claude model. Parse the JSON. If parse fails, retry ONCE with the appended instruction `Return ONLY the JSON array. No prose. No code fences.`. If the second attempt also fails, print the raw response and exit (the user can salvage manually).

If the parsed array is empty, print:

> No gotcha candidates found in `<source>`.

…and exit cleanly.

For each candidate, present the same accept/edit/skip prompt as Path A.

Ask the supersede question for each accepted candidate (as in Path A).

## Step 2 — Resolve project + permalink

This Step has three sub-steps. Run them in order — sub-step 2 must complete before sub-step 3 because the canonical permalink contains `<NNNN>`.

### Sub-step 2.1 — Resolve `<PROJECT>`

`<PROJECT>` = `$BM_SCRIBE_PROJECT` (or the answer captured in Step 1).

### Sub-step 2.2 — Auto-pick ADR number

Query `basic-memory:search_notes` with the prefix `<PROJECT>/gotchas/`. For each returned permalink, parse the leading four-digit `NNNN-` prefix off the path segment immediately under `<PROJECT>/gotchas/`. Find the maximum across all matches and set `NNNN = max + 1`, zero-padded to four digits. If no gotchas exist, start at `0001`. Ignore returned permalinks that do not match the `NNNN-<slug>` shape. (Same logic as `create-decision`.)

### Sub-step 2.3 — Construct the canonical permalink

Canonical permalink = `<PROJECT>/gotchas/<NNNN>-<slug>/main` with `<PROJECT>` from sub-step 2.1 and `<NNNN>` from sub-step 2.2. The directory portion (passed to `write_note`) is the canonical permalink with the trailing `/main` stripped.

If the user supplied no `<slug>` in Path B, derive it from the title: lowercase, replace non-alphanumerics with `-`, collapse runs of `-`, trim trailing `-`, truncate to 60 chars.

## Step 3 — Issue the three-step BM call sequence

Follow [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md). Concretely (per accepted proposal/candidate):

```text
# Step 3a — create. BM ignores metadata.permalink.
basic-memory:write_note(
  title=<title>,
  directory="<PROJECT>/gotchas/<NNNN>-<slug>",
  note_type="gotcha",
  content=<rendered body — see template below>,
  metadata={
    permalink: "<PROJECT>/gotchas/<NNNN>-<slug>/main",
    type: "gotcha",
    status: "accepted",
    modules: ["<slug>", ...],
    severity: "<low|medium|high>",
    discovered_at: "<YYYY-MM-DD>",
    origin: "<origin permalink or null>",
    supersedes: <[] OR ["<predecessor permalink>"]>
  }
)
# Capture the returned permalink — call it AUTO_PERMALINK.

# Step 3b — relocate.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<PROJECT>/gotchas/<NNNN>-<slug>/main.md"
)

# Step 3c — read the moved note to capture the YAML permalink line verbatim.
basic-memory:read_note(identifier="<PROJECT>/gotchas/<NNNN>-<slug>/main")
# Extract the current `permalink: …` line from the frontmatter; call it CURRENT_PERMALINK_LINE.

# Step 3d — rewrite the YAML permalink line.
basic-memory:edit_note(
  identifier="<PROJECT>/gotchas/<NNNN>-<slug>/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: <PROJECT>/gotchas/<NNNN>-<slug>/main"
)
```

### Body template

The body MUST contain these four sections in this order. Section 5 (`## Related`) is optional but recommended:

```markdown
## Symptom

<what was observed — concrete, reproducible if possible>

## Root cause

<why it happens — code paths, invariants violated, env quirks>

## How to avoid

<the actionable rule for future plans touching these modules>

## Evidence

- <link to PR / commit / review comment / log line>
- <link to test that pins the fix, if any>

## Related

- [[<PROJECT>/modules/<slug>/main]]
- [[<origin permalink>]]
```

For Path A, populate the four sections by copying the corresponding sections from the proposal's `body` field (the extract reviewer has already structured them this way per the template in `extract.tmpl`). For Path B, populate from the mining-prompt output's `symptom` / `root_cause` / `how_to_avoid` fields and synthesise `## Evidence` from the review-text source references.

### Step 3 — Supersede handling

If the user named a predecessor permalink in Step 1:

1. Run Steps 3a-3d above to create the new gotcha. Make sure `supersedes` in metadata contains the predecessor permalink.
2. Then issue a second call to flip the predecessor's status:

```text
basic-memory:read_note(identifier="<predecessor permalink>")
# Extract the current `status:` line from frontmatter; call it CURRENT_STATUS_LINE.

basic-memory:edit_note(
  identifier="<predecessor permalink>",
  operation="find_replace",
  find_text=CURRENT_STATUS_LINE,
  replace_text="status: superseded"
)
```

If the second call returns "no match" or "not found" — DO NOT roll back the new note. Print to the user:

> WARNING: created the new gotcha at `<new permalink>` but could not find the predecessor `<predecessor permalink>` to flip its status to `superseded`. The new note carries `supersedes: ["<predecessor>"]` in its frontmatter but the predecessor is unchanged. Please review manually.

…and exit cleanly.

## Step 4 — Verify

For each created gotcha:

- `basic-memory:read_note(identifier="<PROJECT>/gotchas/<NNNN>-<slug>/main")` returns the note.
- The YAML `permalink:` field in the returned frontmatter equals `<PROJECT>/gotchas/<NNNN>-<slug>/main` exactly.
- The YAML `type:` field equals `gotcha`.
- Body contains `## Symptom`, `## Root cause`, `## How to avoid`, `## Evidence` as level-2 headers.
- Report success with a paste-ready permalink: `[[<PROJECT>/gotchas/<NNNN>-<slug>/main]]`.

If any check fails, print the failed expectation and the actual value; do not silently swallow.
````

- [ ] **Step 2: Verify the skill is discoverable**

Run: `ls plugin/bm-scribe/skills/create-gotcha/SKILL.md && head -5 plugin/bm-scribe/skills/create-gotcha/SKILL.md`

Expected: file exists, frontmatter starts with `---\nname: bm-scribe:create-gotcha`.

- [ ] **Step 3: Commit**

```bash
git add plugin/bm-scribe/skills/create-gotcha/SKILL.md
git commit -m "feat(bm-scribe): add create-gotcha skill (dual-mode intake)

Walks the user through accepting/editing/skipping gotcha proposals,
either from the most recent extract_project_knowledge envelope visible
in the conversation (default) or from mined review text (--from-review
pr:N | <file> | paste:). Applies the three-step BM v0.21.1 creator
pattern with ADR-numbered slug, enforces the four-section body
template (Symptom / Root cause / How to avoid / Evidence), and handles
supersede chains by flipping the predecessor's status to superseded
without rolling back the new note if the flip fails.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Update `plugin/bm-scribe/README.md` + bump plugin version

**Goal:** The bm-scribe README catalogues `create-gotcha` and both manifests are bumped to v0.2.0 in lockstep.

**Acceptance criteria:**
- `plugin/bm-scribe/README.md` contains a new row for `create-gotcha [--from-review <source>]` in the subcommand catalogue table, appended at the end of the project-knowledge creator group.
- `plugin/bm-scribe/package.json` has `"version": "0.2.0"`.
- `plugin/bm-scribe/gemini-extension.json` has `"version": "0.2.0"`.
- The new README table row is well-formed markdown (correct pipe-column count matching the table header).

**Non-goals:**
- Any other plugin manifest edits.
- Anti-tangent `VERSION` bump (the anti-tangent `VERSION` file stays at 0.7.0 on this branch per repo policy — release workflow auto-bumps on merge to main).

**Files:**
- Modify: `plugin/bm-scribe/README.md`
- Modify: `plugin/bm-scribe/package.json`
- Modify: `plugin/bm-scribe/gemini-extension.json`

- [ ] **Step 1: Add the `create-gotcha` row to the README catalogue**

Edit `plugin/bm-scribe/README.md`. Locate the subcommand catalogue table (the rows starting around line 38 — `create-epic`, `create-story`, etc.). Add a new row at the END of the project-knowledge creator group, BEFORE the personal-namespace rows:

```markdown
| `create-gotcha [--from-review <source>]` | Create a project-knowledge gotcha at `<PROJECT>/gotchas/<NNNN>-<slug>/main` from extract proposals (default) or mined review text (`pr:<N>` / filesystem path / `paste:`). |
```

- [ ] **Step 2: Bump `plugin/bm-scribe/package.json` version**

Edit `plugin/bm-scribe/package.json`. Change:

```json
  "version": "0.1.0",
```

to:

```json
  "version": "0.2.0",
```

- [ ] **Step 3: Bump `plugin/bm-scribe/gemini-extension.json` version**

Edit `plugin/bm-scribe/gemini-extension.json`. Change:

```json
  "version": "0.1.0",
```

to:

```json
  "version": "0.2.0",
```

(Both manifests must move in lockstep — they describe the same plugin.)

- [ ] **Step 4: Verify the README diff renders correctly**

Run: `grep -A1 -B1 "create-gotcha" plugin/bm-scribe/README.md`

Expected: one row visible, well-formed markdown table syntax (correct number of `|` columns matching the table header).

- [ ] **Step 5: Commit**

```bash
git add plugin/bm-scribe/README.md plugin/bm-scribe/package.json plugin/bm-scribe/gemini-extension.json
git commit -m "feat(bm-scribe): catalogue create-gotcha + bump plugin to v0.2.0

Adds create-gotcha to the subcommand catalogue and bumps both
manifests (package.json + gemini-extension.json) in lockstep. The
plugin version axis is independent of anti-tangent's; bm-scribe
moves from 0.1.0 to 0.2.0 because a new skill is a backward-compat
feature add.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Create `examples/project-knowledge/gotcha.md` template

**Goal:** A reference template for a gotcha note exists at `examples/project-knowledge/gotcha.md` with full frontmatter shape (including required default values) and the four-section body template.

**Acceptance criteria:**
- `examples/project-knowledge/gotcha.md` exists.
- The frontmatter block contains exactly these keys in this order: `permalink`, `type`, `title`, `status`, `modules`, `origin`, `severity`, `discovered_at`, `supersedes`, `tags`.
- The frontmatter line `type:` carries the literal default value `gotcha` (not a placeholder).
- The frontmatter line `status:` carries the literal default value `accepted` with an inline comment showing the enum: `status: accepted                 # accepted | superseded`.
- The frontmatter line `severity:` carries the literal default value `medium` with an inline comment showing the enum: `severity: medium                 # low | medium | high`.
- The frontmatter line `supersedes:` carries the literal default value `[]` with an inline comment explaining when it's non-empty.
- The frontmatter line `tags:` carries the literal default value `[]`.
- Placeholder fields (`permalink`, `title`, `modules`, `origin`, `discovered_at`) use angle-bracket placeholders like `<PROJECT>/gotchas/<NNNN>-<slug>/main`, `<one-line title — what bit us>`, `[<slug>, <slug>]`, etc.
- The body has the four required sections (`## Symptom`, `## Root cause`, `## How to avoid`, `## Evidence`) in that order plus the optional `## Related`.

**Non-goals:**
- Updating the README.md taxonomy (Task 10).
- Updating conventions.md (Task 11).

**Files:**
- Create: `examples/project-knowledge/gotcha.md`

- [ ] **Step 1: Write the template**

Write `examples/project-knowledge/gotcha.md` with this exact content:

```markdown
---
permalink: <PROJECT>/gotchas/<NNNN>-<slug>/main
type: gotcha
title: <one-line title — what bit us>
status: accepted                 # accepted | superseded
modules: [<slug>, <slug>]        # one or more module slugs this gotcha applies to
origin: <PROJECT>/stories/<TICKET-ID>/main   # optional; story / epic / PR permalink that surfaced this
severity: medium                 # low | medium | high
discovered_at: <YYYY-MM-DD>
supersedes: []                   # list of <PROJECT>/gotchas/<NNNN>-<slug>/main permalinks; non-empty if this replaces an earlier gotcha
tags: []
---

## Symptom

<what was observed — concrete, reproducible if possible>

## Root cause

<why it happens — code paths, invariants violated, environmental quirks>

## How to avoid

<the actionable rule for future plans touching these modules — the load-bearing section; prime excerpts this for the next plan>

## Evidence

- <link to PR / commit / review comment / log line>
- <link to test that pins the fix, if any>

## Related

- [[<PROJECT>/modules/<slug>/main]]
- [[<origin permalink>]]
```

- [ ] **Step 2: Verify**

Run: `head -20 examples/project-knowledge/gotcha.md`

Expected: the frontmatter block is visible with `type: gotcha`, `status: accepted`, `severity: medium`, `supersedes: []`, `tags: []` as literal defaults; angle-bracket placeholders for the other five keys.

- [ ] **Step 3: Commit**

```bash
git add examples/project-knowledge/gotcha.md
git commit -m "docs(examples): add gotcha.md template (v0.8.0)

Frontmatter shape mirrors decision.md (status, supersedes, origin,
discovered_at) plus gotcha-specific fields (modules array, severity).
Body uses the four-section template enforced by the create-gotcha
skill: Symptom / Root cause / How to avoid / Evidence + optional
Related.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Update `examples/project-knowledge/README.md` for the seventh type

**Goal:** The examples README reflects the seven-type taxonomy with a new "Lessons-learned layer" grouping and a maintenance-ownership row for `gotcha` with all column cells specified verbatim.

**Acceptance criteria:**
- The section heading previously labelled `## Six types in two layers` is renamed to `## Seven types in three groups`.
- The body of that section enumerates three groups: durable reference layer (4 types — `decision`, `module`, `feature`, `glossary`), operational layer (2 types — `epic`, `story`), lessons-learned layer (1 type — `gotcha`).
- The opener paragraph's "Six templates" is updated to "Seven templates" (and only where it refers to the note-type taxonomy).
- The `## Permalink convention` bullet that lists `<type>` folder names now includes `gotchas` as the seventh value.
- A new row for `gotcha` appears at the bottom of the `## Maintenance ownership` table, EXACTLY as specified in the Steps section below (cells for type / lifecycle / mutation).

**Non-goals:**
- Any other prose edits in the README (preserve unrelated context).
- INTEGRATION.md updates (Task 12).

**Files:**
- Modify: `examples/project-knowledge/README.md`

- [ ] **Step 1: Update the "Six types in two layers" section header and content**

Edit `examples/project-knowledge/README.md`. Find the section heading `## Six types in two layers`. Replace it AND its body with:

```markdown
## Seven types in three groups

- **Durable reference layer** (timeless / slow-evolving): `decision`, `module`, `feature`, `glossary`. Survives epics.
- **Operational layer** (time-bounded; live state during work): `epic`, `story`. Both terminate at completion — epics with `status: closed` (PM gesture), stories with `status: done` (engineering gesture).
- **Lessons-learned layer** (module-scoped; surfaced on future plans): `gotcha`. Accumulates across epics; superseded entries remain in scope but rank lower.
```

- [ ] **Step 2: Update the opener paragraph**

Find the paragraph starting `Six templates seed the project-knowledge schema...` (near the top of the file). Replace `Six` with `Seven`. The numeric mention `six` in any other prose in that paragraph stays as-is unless directly inside the same sentence (preserve unrelated context).

- [ ] **Step 3: Update the permalink convention section**

Find the section `## Permalink convention`. The first bullet under it lists the seven `<type>` values; update from `decisions, modules, features, glossary, epics, stories` to `decisions, modules, features, glossary, epics, stories, gotchas`.

- [ ] **Step 4: Append a row to the maintenance-ownership table — use this EXACT row**

Find the table under `## Maintenance ownership`. Add this exact new row at the bottom (matching the existing table's three-column shape: type / lifecycle / mutation policy):

```markdown
| `gotcha` | Drafted by `extract_project_knowledge` (post-plan) or by `bm-scribe:create-gotcha --from-review <source>` (post-review) → reviewed by human → applied via the three-step BM creator pattern | Append-only; new gotchas supersede old ones via `action: "supersede"`; module-scoped via the `modules:` frontmatter array; prime surfaces accepted and superseded entries alike on future plans touching the same modules |
```

- [ ] **Step 5: Verify the doc still reads coherently**

Run: `head -60 examples/project-knowledge/README.md`

Expected: the renamed section header, the updated opener, the updated type list, the new table row all visible without orphan references to "six types" in the immediate surrounding context.

If a stray "six" remains in this file that we did not touch, leave it alone (e.g. it may refer to a different concept) — only update sentences that enumerate the note-type taxonomy.

- [ ] **Step 6: Commit**

```bash
git add examples/project-knowledge/README.md
git commit -m "docs(examples): seven note types — add gotcha to taxonomy doc

Rename 'Six types in two layers' -> 'Seven types in three groups' and
add the gotcha row to the maintenance-ownership table. Update the
permalink-convention bullet to list 'gotchas' as a valid type folder.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Update `docs/team-setup/project-knowledge-conventions.md`

**Goal:** The conventions doc documents the canonical `status:<value>` / `module:<slug>` encoding controllers must use in `KBIndexEntryArg.Tags` to surface gotcha frontmatter into prime, and updates the seven-type references throughout.

**Acceptance criteria:**
- The line at ~line 43 reading `All six note types use ...` is updated to `All seven note types use ...`, and the same paragraph's type-folder bullet list appends `, gotchas` after `stories`.
- A new top-level section heading is added immediately BEFORE the existing `## Maintenance ownership` heading. The heading line, byte-for-byte (no leading or trailing whitespace, four words plus backticks-quoted `kb_index` and `tags`), is the literal string consisting of: two hash characters, one space, the word `Gotcha`, one space, the word `encoding`, one space, the word `in`, one space, a backtick, `kb_index`, a backtick, one space, a backtick, `tags`, a backtick — and nothing else. In plain copy-paste form: `## Gotcha encoding in \`kb_index\` \`tags\``. Task 14 Step 2 Command 1 greps for this exact whole line via `grep -Fxc '## Gotcha encoding in \`kb_index\` \`tags\`' …` and requires the count to be ≥ 1.
- The new section documents:
  - The two canonical tag formats `status:<value>` and `module:<slug>` (both literals must appear in the section prose).
  - A worked `KBIndexEntryArg` JSON example for a gotcha with two modules. The example's `"tags"` array MUST contain the literal strings `"status:accepted"` AND `"module:driver-search"` (and `"module:driver-network"` as the second module). These literals are the contract Task 14 Step 2 verifies via `grep`; if they are not present verbatim, Task 14's read-side verification will fail.
  - The rationale for keeping superseded entries in scope.
  - A subsection with the EXACT literal heading `### Pre-dispatch search hints` (Task 14 Step 2 greps for this header). The phrase `all seven note types` MUST appear in the FIRST TWO lines of the subsection body (i.e. within two lines after the `### Pre-dispatch search hints` heading) — Task 14 verifies this with a `-A2` grep. Body cites why the controller's pre-dispatch search must cover all seven note types (gotchas surface alongside decisions, modules, etc. for plans touching the same module slugs).
- The new section reads coherently when viewed standalone (it does not assume the reader has already read the design spec, though it links to it).

**Non-goals:**
- Any change to anti-tangent Go code (the design's posture is "no anti-tangent code change for prime read side").
- Any change to the INTEGRATION.md (Task 12).

**Context:** `KBIndexEntryArg` is defined in `internal/mcpsrv/prime_handler.go:30` with fields `permalink`, `type`, `title`, `summary`, `tags` — no dedicated `status` or `modules` fields. Encoding into existing `tags` is intentional per spec §3 + §7.

**Files:**
- Modify: `docs/team-setup/project-knowledge-conventions.md`

- [ ] **Step 1: Update the seven-type taxonomy reference**

Edit `docs/team-setup/project-knowledge-conventions.md:43`. Find the line that begins `All six note types use ...`. Replace `six` with `seven`. Then in the same paragraph, find the bullet list of type folders (`decisions`, `modules`, `features`, `glossary`, `epics`, `stories`) and append `, gotchas` after `stories`.

- [ ] **Step 2: Add a new section `## Gotcha encoding in `kb_index` `tags``**

Locate the section heading `## Maintenance ownership` (around line 113). Immediately BEFORE that heading, insert a new section:

```markdown
## Gotcha encoding in `kb_index` `tags`

The `KBIndexEntryArg` wire schema (`internal/mcpsrv/prime_handler.go`) carries `permalink`, `type`, `title`, `summary`, and `tags` — no dedicated `status` or `modules` fields. Anti-tangent's design explicitly does **not** extend the schema (see [gotcha design spec §3 + §7](../superpowers/specs/2026-05-23-gotcha-note-type-design.md)). When the controller builds a `kb_index` entry for a gotcha note, it MUST encode the gotcha's `status` and per-`modules` frontmatter into the existing `tags` array using two canonical key:value formats:

- `status:accepted` or `status:superseded` — one entry per gotcha.
- `module:<slug>` — one entry per module in the gotcha's frontmatter `modules:` array (a two-module gotcha contributes two `module:` tags).

Example: a gotcha at `<PROJECT>/gotchas/0042-graphql-n+1-on-driver-search/main` with frontmatter `status: accepted, modules: [driver-search, driver-network]` should produce this `KBIndexEntryArg`:

```json
{
  "permalink": "<PROJECT>/gotchas/0042-graphql-n+1-on-driver-search/main",
  "type": "gotcha",
  "title": "GraphQL N+1 on driver-search",
  "summary": "<the gotcha's How to avoid paragraph, ≤ 200 chars>",
  "tags": ["status:accepted", "module:driver-search", "module:driver-network"]
}
```

Prime's reviewer already considers `tags` when ranking relevance, so no prompt change is needed: a task touching `driver-search` matches the `module:driver-search` tag organically, and `status:superseded` reads as a de-prioritization signal to the reviewer's natural-language ranking. **Superseded entries remain in scope** rather than being filtered out — they still carry "we used to have this, here's the resolution" value, which prevents regressions on the next plan touching the same modules. If reviewer over-weighting becomes a problem in practice, revisit with a status-aware filter (deferred to a follow-up).

This encoding is not gotcha-specific in principle — any note type the controller wants to surface `status` or `module` signals for can use the same `status:<value>` / `module:<slug>` tag format. Gotchas are the first type to require it because prime's relevance match relies on module-scoping for them.

### Pre-dispatch search hints

When building `kb_index` for a new plan, the controller should search for notes matching the plan's `touches_modules`. Cover all seven note types in that search; in particular, include `<PROJECT>/gotchas/` matches so accepted-and-superseded gotchas surface alongside relevant decisions, modules, and features.
```

- [ ] **Step 3: Verify the new section reads coherently**

Run: `grep -B1 -A3 "Gotcha encoding" docs/team-setup/project-knowledge-conventions.md`

Expected: the heading appears once, the body paragraph references `KBIndexEntryArg` and the two canonical tag formats.

- [ ] **Step 4: Commit**

```bash
git add docs/team-setup/project-knowledge-conventions.md
git commit -m "docs(conventions): kb_index tag encoding for gotchas + 7-type update

Adds 'Gotcha encoding in kb_index tags' subsection above
'Maintenance ownership'. Documents the canonical tag formats
(status:<value>, module:<slug>) controllers must use, the worked
KBIndexEntryArg example, the rationale for keeping superseded
entries in scope, and a pre-dispatch search-hint note covering
all seven types.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Update `INTEGRATION.md` for the seventh type

**Goal:** INTEGRATION.md's note-type taxonomy table reflects all seven types with a row for `gotcha` in a new "lessons-learned" group, with the body cell content specified verbatim, and any other taxonomy references in the file are updated to seven where applicable.

**Acceptance criteria:**
- The section heading `### Six note types in two layers` is renamed to `### Seven note types in three groups`.
- A new table row is appended after the `story` row, EXACTLY as specified in the Steps section below (three pipe-delimited cells: type / layer / body).
- Other prose mentions of "six" that refer to the note-type taxonomy are updated to "seven"; unrelated "six" mentions (e.g. "six-month roadmap") are left untouched.
- The new table row is well-formed markdown (three pipe-delimited cells matching the header).

**Non-goals:**
- Updating the mirror file at `/home/pgilmore/.claude/anti-tangent.md` (the user re-syncs it manually on pull).
- Any code changes.

**Files:**
- Modify: `INTEGRATION.md`

- [ ] **Step 1: Locate and update the "Six note types in two layers" table**

Edit `INTEGRATION.md`. Find the section heading `### Six note types in two layers` (search for it explicitly — it's the heading right above the table that lists the six types).

Replace the heading line:

```markdown
### Six note types in two layers
```

with:

```markdown
### Seven note types in three groups
```

Then append this EXACT new row to the table immediately below the existing `story` row (matching the table's existing three-column shape: type / layer / body):

```markdown
| `gotcha` | lessons-learned | module-scoped lesson learned captured post-plan (`extract_project_knowledge`) or post-review (`bm-scribe:create-gotcha --from-review`); ADR-numbered slug at `<PROJECT>/gotchas/<NNNN>-<slug>/main`; supersede chain via existing `action: "supersede"` + `supersedes: [...]`; surfaces on future plans touching the same `modules:` via canonical `tags` encoding (`status:<value>` + `module:<slug>`) in `kb_index` (v0.8.0+) |
```

- [ ] **Step 2: Find any other prose mentions of "six" in the file that refer to the note-type taxonomy and update**

Run: `grep -n "six\|Six" INTEGRATION.md`

For each match, judge whether it refers to the note-type taxonomy:
- If yes (e.g. "the six note types", "Six templates"), update to "seven" in the same casing.
- If no (e.g. "six-month roadmap", a wholly unrelated phrase), leave it alone.

A safer alternative if it's unclear: do not touch the match. The integration-doc reviewer can iterate.

- [ ] **Step 3: Verify the table row is well-formed**

Run: `grep -A1 "gotcha.*lessons-learned" INTEGRATION.md`

Expected: one line, three pipe-delimited cells matching the table header.

- [ ] **Step 4: Commit**

```bash
git add INTEGRATION.md
git commit -m "docs(integration): seven note types — add gotcha row (v0.8.0+)

Rename 'Six note types in two layers' -> 'Seven note types in
three groups' and add the gotcha row. Body cites canonical 'tags'
encoding (status:<value>, module:<slug>) for prime-side relevance.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

(The mirror at `/home/pgilmore/.claude/anti-tangent.md` is downstream of this commit; the user can re-sync when they next pull. We do NOT touch the home dotfile from this repo.)

---

### Task 13: Update `CHANGELOG.md` with implementation entries

**Goal:** The existing `## [0.8.0] - 2026-05-23` block in CHANGELOG.md is expanded with per-section entries for the actual implementation work landed in Tasks 1-12.

**Acceptance criteria:**
- Under `## [0.8.0] - 2026-05-23` → `### Added`, six new bullets appear (one per implementation surface): enum/parser/schema; extract.tmpl + goldens; create-gotcha skill; gotcha.md template + examples README; conventions kb_index encoding; bm-scribe v0.2.0 bump.
- Under `## [0.8.0]` → `### Changed`, two new bullets appear: INTEGRATION.md rename; renamed test (`AcceptsAllSixTypes` → `AcceptsAllSevenTypes`).
- No new `## [0.8.0]` block is created — entries APPEND to the existing block that the spec commit (e374639) introduced.
- The block continues to validate against the repo's Keep a Changelog format (CI-enforced).
- The `VERSION` file is NOT modified (per the commit-policy carve-out at the top of this plan: release workflow auto-bumps; pre-bumping on a `version/*` branch causes release-workflow CHANGELOG-validate failure).

**Non-goals:**
- Bumping `VERSION` (do NOT touch).
- Creating new changelog blocks for other versions.

**Context:** The existing `## [0.8.0]` block was added in commit `e374639` with one bullet describing the design spec. This task appends six more implementation bullets to `### Added` plus two to `### Changed`.

**Files:**
- Modify: `CHANGELOG.md` (the existing `## [0.8.0] - 2026-05-23` block)

- [ ] **Step 1: Add per-section entries under `## [0.8.0] - 2026-05-23`**

Edit `CHANGELOG.md`. Locate the `## [0.8.0] - 2026-05-23` block (the first block in the file). The `### Added` section currently has one bullet describing the design spec. APPEND the following six bullets after the existing one:

```markdown
- New `ProposalTypeGotcha` constant in `internal/verdict/extract.go`, added to the parser type-switch allowlist in `internal/verdict/extract_parser.go`, and added to the `proposals[].type` enum in `internal/verdict/extract_schema.json`. The reviewer can now propose `gotcha`-typed entries from `extract_project_knowledge` envelopes; the parser round-trips them via the new `TestParseExtract_AcceptsGotchaType` test and the renamed `TestParseExtract_AcceptsAllSevenTypes` regression. No change to `ProposalAction` — supersede support reuses the existing `action: "supersede"` + `supersedes: [...]` wire shape.
- Extended `internal/prompts/templates/extract.tmpl` to teach the reviewer the gotcha category: ADR-style permalink shape, required frontmatter (`status`, `modules`, `severity`, `discovered_at`; optional `origin`, `supersedes`), four-section body template (`## Symptom` / `## Root cause` / `## How to avoid` / `## Evidence`), and supersede mechanics (new instructions `3a-gotcha` and `3a-gotcha-supersede`). Goldens regenerated.
- New `plugin/bm-scribe/skills/create-gotcha/SKILL.md` creator skill with dual-mode intake: default reads structured `gotcha`-typed proposals from the most recent `extract_project_knowledge` envelope in the conversation; `--from-review <source>` mines candidates from review text (PR comments via `gh api`, filesystem path, or `paste:` heredoc). Applies the three-step BM v0.21.1 creator pattern with auto-picked ADR number; supersede leg flips the predecessor's `status` to `superseded` without rolling back the new note on failure.
- New `examples/project-knowledge/gotcha.md` template with full frontmatter and the four-section body shape. `examples/project-knowledge/README.md` updated from "Six types in two layers" → "Seven types in three groups" with `gotcha` added under a new "Lessons-learned layer".
- New `` ## Gotcha encoding in `kb_index` `tags` `` subsection in `docs/team-setup/project-knowledge-conventions.md` documenting the canonical `status:<value>` / `module:<slug>` tag format controllers must use to surface gotcha frontmatter through `KBIndexEntryArg.Tags`. No anti-tangent code change required — the encoding rides on the existing `tags` array.
- `plugin/bm-scribe` bumped to `v0.2.0` (both `package.json` and `gemini-extension.json`) for the new creator skill.
```

- [ ] **Step 2: Add a `### Changed` entry for the INTEGRATION.md rename + the renamed Go test**

Under the existing empty `### Changed` heading in the `## [0.8.0]` block, add:

```markdown
- `INTEGRATION.md`: renamed "Six note types in two layers" → "Seven note types in three groups" and added the `gotcha` row.
- `internal/verdict/extract_parser_test.go`: renamed `TestParseExtract_AcceptsAllSixTypes` → `TestParseExtract_AcceptsAllSevenTypes`. The renamed test now covers all seven types (`decision`, `module`, `feature`, `glossary`, `epic`, `story`, `gotcha`) via a single table-driven sub-test loop.
```

- [ ] **Step 3: Verify the block parses as well-formed Keep a Changelog**

Run: `head -40 CHANGELOG.md`

Expected: `## [0.8.0] - 2026-05-23` heading, `### Added` with seven bullets total (one spec + six implementation), `### Changed` with two bullets, `### Fixed` / `### Removed` / `### Deprecated` / `### Security` empty.

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): record v0.8.0 implementation entries

Append Added entries for the ProposalTypeGotcha wiring, the
extract.tmpl + golden regen, the create-gotcha skill, the gotcha
template + README rename, the conventions kb_index encoding section,
and the bm-scribe v0.2.0 bump. Add Changed entries for the
INTEGRATION.md rename and the test rename.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 14: Final verification + push

**Goal:** All Go tests pass, all Task 1-13 file changes are present on the branch and pushed to `origin/version/0.8.0`, the read-side encoding contract is verifiably documented on the branch, CodeRabbit re-review is triggered, and the PR's draft/ready state matches the user's choice.

**Acceptance criteria:**
- `go test -race ./...` exits 0 immediately before the push.
- A read-side encoding verification check (Step 2 below) passes: the four `grep` commands listed in Step 2 each return a count of at least `1`. The four checks verify (in order) (a) the exact backticked heading `` ## Gotcha encoding in `kb_index` `tags` `` is present in `docs/team-setup/project-knowledge-conventions.md` on the branch tip, (b) the literal `status:accepted` appears (in the worked `KBIndexEntryArg` example's `tags` array), (c) the literal `module:driver-search` appears, and (d) the phrase `all seven note types` appears within two lines after the `### Pre-dispatch search hints` heading. The exact `grep` flags and quoting are specified verbatim in Step 2 to avoid shell-interpretation traps with the backticks.
- After the push, `origin/version/0.8.0` is a fast-forward target AND `git diff origin/version/0.8.0..HEAD` is empty (i.e. all Task 1-13 file changes are present on the remote, regardless of how they were grouped into commits — fixup commits are acceptable as long as the net diff is the intended set).
- `gh pr comment 36 --body "@coderabbitai review"` returns a comment URL.
- The session does NOT respond to CR findings in the same run (those are a follow-up CR loop, separate scope).

**Conditional acceptance criteria (user-gated):**
- If the user chooses to mark the PR ready (rather than keep it draft until CR comes back clean), `gh pr ready 36` exits 0 and the PR transitions from draft to ready. Otherwise this step is skipped and the PR remains a draft.

**Non-goals:**
- Merging the PR.
- Responding to CR findings in this same run (deferred to a follow-up CR fix loop, max 5 iterations per the user's standard process).

**Files:** (none — verification + remote push only)

- [ ] **Step 1: Full Go test suite (one more pass)**

Run: `go test -race ./...`

Expected: all `ok` / `PASS`.

- [ ] **Step 2: Read-side encoding verification (dispatchable check) — run these four commands verbatim**

Run these four greps verbatim. Each must return a count of at least `1`; if any returns `0`, STOP — Task 11 did not land its intended content on the branch.

Command 1 — verify the exact section heading is present (single quotes preserve the backticks literally; the `$` anchors end-of-line):
```bash
grep -Fxc '## Gotcha encoding in `kb_index` `tags`' docs/team-setup/project-knowledge-conventions.md
```

Command 2 — verify the worked example contains the literal `status:accepted` tag value:
```bash
grep -Fc 'status:accepted' docs/team-setup/project-knowledge-conventions.md
```

Command 3 — verify the worked example contains the literal `module:driver-search` tag value:
```bash
grep -Fc 'module:driver-search' docs/team-setup/project-knowledge-conventions.md
```

Command 4 — verify the phrase `all seven note types` appears within two lines after the `### Pre-dispatch search hints` heading. `-A2` includes the matched line plus the next two; piping into the second grep counts occurrences of the phrase within that window:
```bash
grep -F -A2 '### Pre-dispatch search hints' docs/team-setup/project-knowledge-conventions.md | grep -Fc 'all seven note types'
```

Notes on quoting / flags: `-F` is fixed-string (treats backticks and special chars literally — important because the heading contains backticks); `-x` is whole-line match; `-c` is count; `-A2` is "after-context 2 lines". Use single quotes around the pattern so the shell does not interpret backticks as command substitution.

Expected: every command outputs `1` (or higher if the documentation legitimately repeats these strings).

- [ ] **Step 3: Confirm the branch holds the intended diff**

Run: `git diff --stat origin/version/0.8.0..HEAD`

Expected: the diff stat lists exactly the Task 1-13 modified/created files (Go source, schema, tests, template, goldens, SKILL.md, examples, conventions, INTEGRATION, CHANGELOG). No `VERSION` change. No unrelated files.

- [ ] **Step 4: Push**

Run: `git push`

Expected: a fast-forward push to `origin/version/0.8.0`.

After the push completes, confirm the remote and local tips are identical (the `--stat` from Step 3 is local-only; this post-push check uses the just-updated remote ref):

```bash
git fetch origin version/0.8.0
git diff origin/version/0.8.0..HEAD
```

Expected: the second command outputs nothing (empty diff). If it outputs any lines, the push did not land as a fast-forward; STOP and investigate before triggering CodeRabbit.

- [ ] **Step 5: Trigger CodeRabbit re-review**

Run: `gh pr comment 36 --body "@coderabbitai review"`

Expected: a comment URL printed; CodeRabbit will respond within a few minutes.

- [ ] **Step 6: (Conditional) Mark the PR ready for review**

If the user explicitly opts in to flipping the PR from draft to ready in this run, run:

```bash
gh pr ready 36
```

Expected: the PR transitions from draft to ready. If the user prefers to keep it as a draft until CR comes back clean, SKIP this step — the PR stays a draft and the AC is treated as not applicable.

- [ ] **Step 7: STOP and hand back**

Do NOT merge. Do NOT respond to CR findings in this session — handle that in a follow-up using the CR loop pattern (read findings, fix, commit, push, re-trigger; up to 5 iterations or until clean).

The plan is complete when:
- `go test -race ./...` is green
- All Task 1-13 file changes are on `origin/version/0.8.0` (`git diff origin/version/0.8.0..HEAD` is empty after push)
- The read-side encoding verification (Step 2) passes
- The PR is either ready-for-review or in CR-fix-loop state
- The bm-scribe smoke test (next section) has been run at least once against a real BM

---

## Manual verification (bm-scribe smoke, not part of the commit chain)

Run this AFTER the Go suite is green and the PR is up. Not gated by automated tests — `bm-scribe` skills are markdown contracts.

1. **Path A smoke:** find a recent `extract_project_knowledge` envelope in your conversation history (or trigger one against a recently completed plan that introduced a known gotcha-shaped lesson). Confirm the `proposals` array now contains at least one `type: "gotcha"` entry (after Task 4's template change).
2. Run `/bm-scribe:create-gotcha` in this conversation. Walk the prompt: accept one proposal. Confirm the three-step BM call sequence lands a note at `<PROJECT>/gotchas/0001-<slug>/main` with frontmatter containing `type: gotcha`, `status: accepted`, `modules: [...]`, `severity: ...`, and a body with the four required sections.
3. **Supersede smoke:** run `/bm-scribe:create-gotcha` again, supply the previous gotcha's permalink as the predecessor on accept. Confirm: (a) new note's frontmatter has `supersedes: ["<previous>"]`, (b) previous note's `status` is now `superseded`.
4. **Path B smoke:** invoke `/bm-scribe:create-gotcha --from-review pr:36` (this PR). Confirm: review text fetches successfully, mining prompt runs, the candidate list is presented (may be empty since this PR is docs-only — that's the expected `No gotcha candidates found in pr:36` branch).
5. **Prime smoke:** trigger a `prime_project_knowledge` call against a task that touches a module named in the gotcha's `modules:`. Confirm the gotcha appears in `picks` and that the controller builds a `KBIndexEntryArg` with `tags: ["status:accepted", "module:<slug>", ...]` (the spec-mandated encoding from Task 11's docs).

If any step fails: the failure is the next task. File it as a follow-up issue, do not block the PR on smoke iteration unless the bug is in the committed code.

---

## Self-review (run before declaring this plan ready)

I ran the self-review checklist after writing this plan.

**Spec coverage:**

| Spec §  | Spec content | Covered by |
|---|---|---|
| §1, §2, §3 (overview / goals / non-goals) | n/a — descriptive | n/a |
| §4 (architecture) | enum + parser + schema + template | Tasks 1, 2, 4 |
| §5.1 (Path A flow) | skill reads extract envelope | Task 7 (Step 1 — Path A) |
| §5.2 (Path B flow) | skill mines review text | Task 7 (Step 1 — Path B) |
| §6.1 (permalink) | ADR-numbered slug | Task 7 (Sub-step 2.2-2.3) |
| §6.2 (frontmatter) | required fields | Task 7 (Step 3a), Task 9 (template) |
| §6.3 (body sections) | four required + optional | Task 7 (Body template), Task 9 |
| §6.4 (supersede) | two-call mechanics | Task 7 (Step 3 — Supersede handling) |
| §7.1 (prime read flow) | no anti-tangent code change | (verified — no Go task touches `prime.go`) |
| §7.2 (kb_index tag encoding) | canonical `status:` / `module:` tag format | Task 11 + Task 14 Step 2 verification |
| §7.3 (what stays unchanged) | prime prompt, validate_plan prompt, BM schema | (verified — no task touches those) |
| §8.1 (Go test surface) | enum + golden + round-trip | Tasks 1, 2, 3, 5, 6 |
| §8.2 (bm-scribe smoke) | manual smoke checklist | Manual verification section above |
| §8.3 (failure modes) | enumerate explicit handling | Task 7 (per-step + Step 3 supersede fallback) |
| §9 (versioning) | v0.8.0 + CHANGELOG | Task 13; branch already created |

No spec section is uncovered.

**Placeholder scan:** no `TBD`, no `TODO`, no "implement later". Every step shows the actual code or text to write. Test code blocks are complete.

**Type consistency:** `ProposalTypeGotcha` is the canonical constant name used in Task 1 (define), Task 1 step 1 (test asserts), Task 3 step 1 (extends the table-driven test's `types` list). The parser switch in Task 1 step 5 puts `ProposalTypeGotcha` at the end of the case list. The `gotcha` JSON value appears identically in Task 2 (schema enum), Task 4 (template), Task 7 (skill examples), Task 9 (template frontmatter), Task 11 (encoding doc), Task 12 (INTEGRATION.md row). No drift.

The plan is ready.
