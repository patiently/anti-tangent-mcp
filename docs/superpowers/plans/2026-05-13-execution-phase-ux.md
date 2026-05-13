# Execution-phase UX (0.3.1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship 0.3.1 of `anti-tangent-mcp` addressing the execution-phase field report (issue [#12](https://github.com/patiently/anti-tangent-mcp/issues/12)): explicit text-only boundary docs, evidence-shape guard on `validate_completion`, paste-ready `summary_block` field on every response, `check_progress` demoted to advisory, lightweight protocol mode docs, new `unverifiable_codebase_claim` and `malformed_evidence` finding categories, and a `plan_quality` axis on `PlanResult` for convergence visibility.

**Architecture:** Mostly additive. Two new `Finding.Category` enum values + one new `PlanQuality` enum field added to `verdict.*` types. Reviewer prompts gain a paragraph in all four text-only templates (`plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl`, `pre.tmpl`). Handler-level: a `validate_completion` pre-reviewer guard rejects malformed evidence by content hash, and the marshalling helpers (`envelopeResult` / `planEnvelopeResult`) gain a one-line `summary_block` population so every exit path carries it. Lightweight protocol mode is doc-only plus a tiny handler tweak: `validate_completion` accepts an empty `session_id` when at least one piece of evidence is non-empty.

**Tech Stack:** Go 1.22+, `text/template`, `encoding/json`, `crypto/sha256`, `httptest`, `testify`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-05-13-execution-phase-ux-design.md` (read this first; it is the source of truth, including the round-3 revisions on cache-key canonical encoding and `malformed_evidence` schema scoping).

---

## Drift-protection protocol notes

**SKIP `validate_plan` for this plan.** The spec itself extensively rewrites the plan templates AND adds new finding categories the reviewer would emit during analysis. Running `validate_plan` against this plan would feed back through the very prompt-template edits we're shipping — circular. Per-task `validate_task_spec` still applies and provides equivalent gating.

**Per-task lifecycle hooks DO apply.** Each task below has structured `Goal / Acceptance criteria / Non-goals / Context` so `validate_task_spec` is happy. Any dispatched implementer must paste the standard dispatch clause (below) into the subagent prompt and call `validate_task_spec` / `check_progress` (optional per the spec) / `validate_completion` for that task.

---

## File structure

**Created:**
- `internal/mcpsrv/summary.go` — `formatEnvelopeSummary` and `formatPlanSummary` helpers.
- `internal/mcpsrv/summary_test.go` — format-determinism + truncation + omitempty tests.
- `examples/lightweight-dispatch.md` — reference lightweight dispatch clause for trivial tasks.

**Modified:**
- `internal/verdict/verdict.go` — add `CategoryUnverifiableCodebaseClaim` and `CategoryMalformedEvidence` constants; add `SummaryBlock` field to `Result`.
- `internal/verdict/plan.go` — add `PlanQuality` type + constants; add `PlanQuality` and `SummaryBlock` fields to `PlanResult`; add `PlanQuality` field to `PlanFindingsOnly`.
- `internal/verdict/schema.json` — add both new categories to the enum.
- `internal/verdict/plan_schema.json` — add `unverifiable_codebase_claim` (only) and `plan_quality` enum + required field.
- `internal/verdict/plan_findings_only_schema.json` — same as plan_schema.json.
- `internal/verdict/tasks_only_schema.json` — add `unverifiable_codebase_claim` only.
- `internal/verdict/parser.go` — severity floor for `unverifiable_codebase_claim`; updated `validCategory`.
- `internal/verdict/parser_partial.go` — same severity floor.
- `internal/verdict/plan_parser.go` — same severity floor + `plan_quality` sanity check + fallback.
- `internal/verdict/parser_test.go` — new tests for severity floor + new categories.
- `internal/verdict/plan_parser_test.go` — new tests for plan_quality sanity check.
- `internal/prompts/templates/plan.tmpl` — new ground-rules paragraph + `plan_quality` instruction.
- `internal/prompts/templates/plan_findings_only.tmpl` — same.
- `internal/prompts/templates/plan_tasks_chunk.tmpl` — new ground-rules paragraph only (no `plan_quality`).
- `internal/prompts/templates/pre.tmpl` — new `### Unverifiable codebase claims` section.
- `internal/prompts/testdata/plan_basic.golden`, `plan_findings_only.golden`, `plan_tasks_chunk.golden`, `plan_basic_quick.golden`, `plan_findings_only_quick.golden`, `plan_tasks_chunk_quick.golden`, `pre_basic.golden` — regenerate.
- `internal/prompts/prompts_test.go` — anchor-assertion tests for new instruction + plan_quality + negative tests.
- `internal/mcpsrv/handlers.go` — `summary_block` population inside `envelopeResult` / `planEnvelopeResult`; evidence-shape guard + cache; lightweight-mode `session_id == ""` allowance in `validate_completion`.
- `internal/mcpsrv/handlers_test.go` — evidence-shape tests + summary_block tests + lightweight-mode test + early-return summary tests.
- `INTEGRATION.md` — Scope-and-limits section; `check_progress` demote; lightweight mode section; `summary_block` paste-instruction in dispatch clause; `plan_quality` description in validate_plan section; `malformed_evidence` troubleshooting entry.
- `README.md` — short paragraph on lightweight-mode capability + pointer to `examples/lightweight-dispatch.md`.
- `CHANGELOG.md` — `## [0.3.1] - 2026-05-13` block.

---

## Subagent dispatch clause (paste verbatim into every implementer prompt)

```markdown
## Drift-protection protocol (anti-tangent-mcp)

Before, during, and after this task, you must use the `validate_task_spec`,
`check_progress`, and `validate_completion` MCP tools.

**1. At the start (REQUIRED).** Before writing any code, call
`validate_task_spec` with the structured task fields below. Save the
returned `session_id` — you'll thread it through subsequent calls.
- Read the findings list. Treat `severity: critical` as blocking and
  `severity: major` as "address or explain." If the spec is too ambiguous
  to proceed, stop and ask the controller for clarification rather than
  guessing.

**2. During work (OPTIONAL).** Call `check_progress` ONLY if you suspect
you're drifting mid-task. Per the 0.3.1 protocol revision this call is
advisory — most tasks will skip it. When you do call, pass: the
session_id, a one-sentence `working_on` summary, and the changed files.

**3. Before reporting DONE (REQUIRED).** Call `validate_completion` with
the session_id, your summary, AND at least one of: `final_files` (full
file contents), `final_diff` (a unified diff), or `test_evidence` (test
command output). Summary-only requests are rejected with `at least one
of final_files, final_diff, or test_evidence must be non-empty`. Prefer
`final_diff` when changes are large enough that pasting whole files
would risk the 200KB payload cap. **Copy the `summary_block` field from
the response verbatim into your DONE report** — it carries the full
envelope formatted for paste; you do not need to re-extract JSON
fields. If the verdict is `fail` or contains `critical`/`major`
findings, do not report DONE — fix the findings and re-validate.

## Task spec (pass these fields verbatim to validate_task_spec)

- task_title:           <from the task block>
- goal:                 <from "Goal:">
- acceptance_criteria:  <from "Acceptance criteria:" bullets>
- non_goals:            <from "Non-goals:" bullets if present>
- context:              <from "Context:" if present>
```

---

### Task 1: Documentation — Scope-and-limits, check_progress demote, lightweight mode

**Goal:** Land four documentation changes that align operator expectations with the tool's text-only architectural boundary, demote `check_progress` to advisory based on field data, document the lightweight protocol mode, and ship the reference lightweight dispatch template — all before any code changes that depend on the new docs.

**Acceptance criteria:**
- `INTEGRATION.md` has a new `## Scope and limits` section near the top (before per-tool docs) that explicitly enumerates what the tool catches vs. what it structurally cannot, and recommends pairing with a codebase-aware review.
- The `check_progress` per-tool section in `INTEGRATION.md` is prefixed with a `**Status:** OPTIONAL / advisory (was RECOMMENDED prior to v0.3.1).` block plus the field-data rationale.
- The dispatch-clause template in `INTEGRATION.md` updates Step 2 to read "OPTIONAL" instead of "RECOMMENDED" with the "call only when you suspect drift" framing.
- `INTEGRATION.md` has a new `### Lightweight protocol mode (v0.3.1+)` section explaining when and how to use it.
- `examples/lightweight-dispatch.md` exists (new file) with a ~15-line dispatch template referencing only `validate_completion`.
- `go test -race ./...` is green (sanity — docs changes should not touch code).
- `git grep "RECOMMENDED" INTEGRATION.md` no longer matches the `check_progress` line.

**Non-goals:**
- No code changes in this task. `check_progress` itself is unchanged in 0.3.1; only its dispatch-clause status is demoted.
- No CHANGELOG entry yet — Task 7 lands the full CHANGELOG block at the end.
- No README changes yet — Task 7 lands the README mention of lightweight mode.

**Context:** Field report at [#12](https://github.com/patiently/anti-tangent-mcp/issues/12) documents 9 codebase-grounded findings that `validate_plan` text-only review could not catch, plus `check_progress`'s 0/5 substantive-catch rate across an execution phase. Spec §1 contains the exact paragraph text for each of the four edits. The dispatch clause in `INTEGRATION.md` is the canonical template controllers copy into their CLAUDE.md / agent-prompt files; updating it propagates to new dispatches.

**Files:**
- Modify: `INTEGRATION.md` (multiple sections)
- Create: `examples/lightweight-dispatch.md`

- [ ] **Step 1: Find the right insertion points in `INTEGRATION.md`**

Run: `grep -n '^## \|^### ' INTEGRATION.md`
Expected: section list including the per-tool sections for `validate_task_spec`, `check_progress`, `validate_completion`, `validate_plan`, and the dispatch-protocol section.

- [ ] **Step 2: Add the `## Scope and limits` section near the top**

Open `INTEGRATION.md`. Locate the first major section header AFTER the project intro / TL;DR (typically the dispatch-protocol section). INSERT this entire block IMMEDIATELY BEFORE that section:

```markdown
## Scope and limits

**What `anti-tangent-mcp` is good at.** Plan-internal consistency: contradictions between ACs, missing observable assertions, scope creep relative to non-goals, structural completeness of task headers, hedge language in acceptance criteria.

**What it structurally cannot catch.** The reviewer reasons over the plan text and submitted evidence — *not* the codebase. It will not detect:

- Field/symbol names that don't exist in the codebase.
- Function signatures or insertion points that don't exist.
- Repo-wide invariants encoded elsewhere (e.g. a constant containing characters another module's validator rejects).
- Existing conventions in adjacent code.
- CI/test policy declared in `CLAUDE.md` / `AGENTS.md`.
- Type-system facts (required fields with no default).

**Pair with a codebase-aware review for any plan that lands in real code.** A text-only reviewer paired with a codebase-aware pass catches both classes of bugs; either alone has a known blind spot.

When the reviewer encounters a plan or task-spec statement about codebase facts it cannot verify text-only, as of v0.3.1 it flags an `unverifiable_codebase_claim` finding rather than silently passing. These are explicitly *not failures* — they're a checklist for the human or a codebase-aware follow-up review. A plan that converges to `pass` with several `unverifiable_codebase_claim` findings is still implementable; treat the findings as "things to grep before dispatching."
```

- [ ] **Step 3: Demote `check_progress` to OPTIONAL in its per-tool section**

In `INTEGRATION.md`, locate the per-tool section for `check_progress` (search for `### check_progress` or `## check_progress` depending on heading depth). INSERT this block IMMEDIATELY after the heading and BEFORE the existing description:

```markdown
**Status:** OPTIONAL / advisory (was RECOMMENDED prior to v0.3.1).

Field data from execution-phase usage shows `check_progress` consistently produces low-signal findings — mid-implementation context is inherently ambiguous (tests not yet written, function not yet finished, assertion not yet reached). The fast-model default magnifies the issue. Call it when *you* sense drift mid-task; do not treat it as a mandatory gate. The strong-model `validate_completion` post-impl call is far higher signal for a typical task.

```

(Note the trailing blank line — it separates the new block from whatever existing prose follows.)

- [ ] **Step 4: Update the dispatch-clause template's Step 2 wording**

In `INTEGRATION.md`, find the dispatch-clause template (search for `**1. At the start (REQUIRED)`). The full clause is a fenced markdown code block. Inside that block, find Step 2 — currently reads something like:

```
**2. During work (RECOMMENDED).** After each meaningful change (a new
file, a non-trivial logic block, finishing one acceptance criterion),
call `check_progress` with: the session_id, a one-sentence `working_on`
summary, and the changed files. Address findings before continuing.
```

REPLACE Step 2 with:

```
**2. During work (OPTIONAL).** Call `check_progress` ONLY if you suspect
you're drifting mid-task. Per the 0.3.1 protocol revision this call is
advisory — most tasks will skip it. When you do call, pass: the
session_id, a one-sentence `working_on` summary, and the changed files.
```

Also, in Step 3 of the same template, add the `summary_block` paste instruction. The current Step 3 ends with something like "If the verdict is `fail` or contains `critical`/`major` findings, do not report DONE — fix the findings and re-validate." INSERT this sentence BEFORE that closing sentence:

```
**Copy the `summary_block` field from the response verbatim into your DONE report** — it carries the full envelope formatted for paste; you do not need to re-extract JSON fields.
```

- [ ] **Step 5: Add the `### Lightweight protocol mode (v0.3.1+)` section**

In `INTEGRATION.md`, locate the end of the dispatch-protocol section (immediately after the dispatch clause code block). INSERT this new subsection:

```markdown
### Lightweight protocol mode (v0.3.1+)

For trivial tasks — doc-only edits, single-file mechanical relocations, dependency bumps — the full dispatch clause is overhead-heavy (~50 lines of boilerplate for ~15 lines of actual work). Controllers may use a **lightweight clause** for these tasks:

- **Skip** `validate_task_spec` (the spec is fully prescriptive; no design choices for the reviewer to shape).
- **Skip** `check_progress` (already optional in full mode).
- **Keep** `validate_completion` as a sanity gate before reporting DONE. The handler accepts an empty `session_id` when at least one of `final_files` / `final_diff` / `test_evidence` is non-empty.

Use lightweight mode when ALL of: (a) the task touches ≤ 2 files; (b) the task is mechanical (no new logic, no test-design choices); (c) the spec includes the literal text or diff to write.

Use the full protocol for: any task that produces new production logic, any task with test-design choices, any task whose ACs require observable invariants.

A reference lightweight dispatch clause is at `examples/lightweight-dispatch.md`.
```

- [ ] **Step 6: Create `examples/lightweight-dispatch.md`**

If `examples/` does not exist as a directory, create it. Then create the file with this content:

```markdown
# Lightweight dispatch clause (anti-tangent-mcp, v0.3.1+)

> Use this template for trivial tasks (doc-only edits, single-file mechanical relocations, dependency bumps). For any task that produces new production logic or has test-design choices, use the full dispatch clause from `INTEGRATION.md` instead.

## Drift-protection protocol (lightweight)

Before reporting DONE (REQUIRED). Call `validate_completion` with the fields below, and AT LEAST ONE of: `final_files` (full file contents), `final_diff` (a unified diff), or `test_evidence` (test command output). Copy the `summary_block` field from the response verbatim into your DONE report.

If the verdict is `fail` or contains `critical`/`major` findings, do not report DONE — fix the findings and re-validate.

## Task spec (pass these fields verbatim to validate_completion)

- `session_id`: pass an empty string `""`. Lightweight mode skips `validate_task_spec`, so there is no session_id to thread. The handler accepts the empty string when at least one piece of evidence is non-empty; it synthesizes a minimal task spec (Goal = summary; no ACs) for the reviewer.
- `summary`: <one-paragraph summary of what was implemented>
- `final_files`, `final_diff`, `test_evidence`: at least one must be non-empty
```

- [ ] **Step 7: Run the full test suite (sanity check — docs only)**

Run: `go test -race ./...`
Expected: PASS across all packages. Docs changes should not affect any code path.

- [ ] **Step 8: Confirm grep checks**

Run: `grep -n 'OPTIONAL / advisory' INTEGRATION.md`
Expected: one match (the new check_progress status line).

Run: `grep -n 'examples/lightweight-dispatch' INTEGRATION.md`
Expected: one match (the lightweight-mode pointer).

Run: `grep -n '^## Scope and limits' INTEGRATION.md`
Expected: one match.

- [ ] **Step 9: Commit**

```bash
git add INTEGRATION.md examples/lightweight-dispatch.md
git commit -m "docs: INTEGRATION.md scope-and-limits + check_progress demote + lightweight mode

Adds the v0.3.1 documentation surfaces from issue #12:
- New ## Scope and limits section documents the text-only
  architectural boundary explicitly.
- check_progress demoted from RECOMMENDED to OPTIONAL in its per-tool
  section and in the dispatch-clause template (field data: 0
  substantive catches across 5 representative tasks).
- New ### Lightweight protocol mode section + reference dispatch
  template at examples/lightweight-dispatch.md.
- Dispatch-clause Step 3 now instructs implementers to paste
  summary_block (lands in Task 5).

Refs #12."
```

---

### Task 2: New finding categories (`unverifiable_codebase_claim` + `malformed_evidence`) with severity floor

**Goal:** Add two new `Finding.Category` constants and corresponding JSON-schema enum entries, with `unverifiable_codebase_claim` available to all four per-task and plan schemas, `malformed_evidence` available only on `schema.json`, and parser-side severity floor forcing `unverifiable_codebase_claim` findings to `minor` regardless of what the reviewer emits.

**Acceptance criteria:**
- `internal/verdict/verdict.go` exports `CategoryUnverifiableCodebaseClaim` and `CategoryMalformedEvidence` constants.
- `internal/verdict/parser.go`'s `validCategory` recognizes both new constants.
- JSON schemas update per the spec's strict scoping: `schema.json` and the three plan-shape schemas add `unverifiable_codebase_claim` to their `category` enums. **`malformed_evidence` is NOT added to any JSON schema and is NOT added to `validCategory`** — the constant exists in `verdict.go` purely for server-side construction (the `validate_completion` guard builds the envelope directly; it never passes through `Parse()`). This prevents reviewers from emitting a server-only category.
- Parser-side severity floor: when a finding has `category: unverifiable_codebase_claim` and `severity != minor`, the parser silently corrects to `severity: minor` (no error). Applies in `parser.go`, `parser_partial.go`, and `plan_parser.go`.
- `internal/verdict/parser_test.go` has 1+ new test asserting the severity floor.
- `go test -race ./internal/verdict/...` is green; `go test -race ./...` is green.

**Non-goals:**
- No prompt template changes in this task — templates are Task 4.
- No `plan_quality` work — that's Task 3.
- No `summary_block` work — that's Task 5.

**Context:** Spec §4 (`unverifiable_codebase_claim`) and §6 (`malformed_evidence`). The reviewer emits `unverifiable_codebase_claim` when it can't verify a plan statement about codebase facts; severity is forced to `minor` because the reviewer doesn't know if the claim is wrong (only that it can't check). `malformed_evidence` is emitted exclusively by the server-side `validate_completion` evidence-shape guard (Task 6); the schema add here is for the per-task `Result` shape that `validate_completion` returns. Schema scoping is critical — including `malformed_evidence` in `plan_*.json` would invite plan-template reviewers to emit a nonsensical category.

**Files:**
- Modify: `internal/verdict/verdict.go`
- Modify: `internal/verdict/parser.go`
- Modify: `internal/verdict/parser_partial.go`
- Modify: `internal/verdict/plan_parser.go`
- Modify: `internal/verdict/schema.json`
- Modify: `internal/verdict/plan_schema.json`
- Modify: `internal/verdict/plan_findings_only_schema.json`
- Modify: `internal/verdict/tasks_only_schema.json`
- Modify: `internal/verdict/parser_test.go`

- [ ] **Step 1: Write the failing parser severity-floor test**

Add to `internal/verdict/parser_test.go` (append at end, before any closing braces):

```go
func TestParse_UnverifiableCodebaseClaim_SeverityFloorToMinor(t *testing.T) {
	// Reviewer emits a `major` unverifiable_codebase_claim — server should
	// silently floor to `minor` because the reviewer doesn't know if the
	// claim is wrong, only that it can't check.
	raw := []byte(`{
		"verdict": "warn",
		"findings": [{
			"severity":   "major",
			"category":   "unverifiable_codebase_claim",
			"criterion":  "plan-stated fact",
			"evidence":   "plan says: uses field StateMachineOutput.currentState",
			"suggestion": "verify against the actual code before dispatching"
		}],
		"next_action": "Address the warnings before dispatching."
	}`)

	r, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(r.Findings))
	}
	if got, want := r.Findings[0].Severity, SeverityMinor; got != want {
		t.Errorf("severity = %q, want %q (server should floor)", got, want)
	}
	if got, want := r.Findings[0].Category, CategoryUnverifiableCodebaseClaim; got != want {
		t.Errorf("category = %q, want %q", got, want)
	}
}

func TestParse_MalformedEvidenceCategory_RejectedFromReviewerOutput(t *testing.T) {
	// malformed_evidence is emitted ONLY by the server-side validate_completion
	// guard, never by the reviewer. If a reviewer somehow emits it, the
	// parser must reject it as an invalid category.
	raw := []byte(`{
		"verdict": "fail",
		"findings": [{
			"severity":   "major",
			"category":   "malformed_evidence",
			"criterion":  "evidence_shape",
			"evidence":   "final_diff contains (truncated) at offset 142",
			"suggestion": "Submit full file contents."
		}],
		"next_action": "Re-submit with complete evidence."
	}`)
	_, err := Parse(raw)
	if err == nil {
		t.Fatalf("Parse should reject reviewer-emitted malformed_evidence (server-only category)")
	}
	if !strings.Contains(err.Error(), "category") {
		t.Errorf("error should mention invalid category; got %v", err)
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test -race ./internal/verdict/ -run "TestParse_UnverifiableCodebaseClaim_SeverityFloorToMinor|TestParse_MalformedEvidenceCategory_Accepted" -v`
Expected: FAIL — both tests reference undefined `CategoryUnverifiableCodebaseClaim` / `CategoryMalformedEvidence`, OR compile-error.

- [ ] **Step 3: Add the new Category constants**

In `internal/verdict/verdict.go`, replace the existing Category constants block with:

```go
const (
	CategoryMissingAC                  Category = "missing_acceptance_criterion"
	CategoryScopeDrift                 Category = "scope_drift"
	CategoryAmbiguousSpec              Category = "ambiguous_spec"
	CategoryUnaddressed                Category = "unaddressed_finding"
	CategoryQuality                    Category = "quality"
	CategorySessionMissing             Category = "session_not_found"
	CategoryTooLarge                   Category = "payload_too_large"
	CategoryUnverifiableCodebaseClaim  Category = "unverifiable_codebase_claim"
	CategoryMalformedEvidence          Category = "malformed_evidence"
	CategoryOther                      Category = "other"
)
```

- [ ] **Step 4: Update `validCategory` in `parser.go` to recognize `unverifiable_codebase_claim` ONLY (NOT `malformed_evidence`)**

`malformed_evidence` is server-only — emitted by the `validate_completion` evidence-shape guard which builds envelopes directly (never round-trips through `Parse()`). The reviewer must not be able to emit it; the parser rejects it.

In `internal/verdict/parser.go`, replace the existing `validCategory` function with:

```go
func validCategory(c Category) bool {
	switch c {
	case CategoryMissingAC, CategoryScopeDrift, CategoryAmbiguousSpec,
		CategoryUnaddressed, CategoryQuality, CategorySessionMissing,
		CategoryTooLarge, CategoryUnverifiableCodebaseClaim,
		CategoryOther:
		return true
	}
	return false
}
```

Note: `CategoryMalformedEvidence` is intentionally NOT in `validCategory`. The constant is defined in `verdict.go` so server-side code in `internal/mcpsrv/handlers.go` (the evidence-shape guard) can use it when constructing rejection envelopes directly, without going through the parser.

- [ ] **Step 5: Add the severity floor in `parser.go`**

In `internal/verdict/parser.go`, inside the existing `Parse` function, locate the finding-validation loop (the `for i, f := range r.Findings {` block). Add the severity-floor adjustment AFTER the existing severity / category validation, BEFORE the loop closes. The full updated loop:

```go
	for i := range r.Findings {
		f := r.Findings[i]
		switch f.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return Result{}, fmt.Errorf("finding[%d]: invalid severity %q", i, f.Severity)
		}
		if !validCategory(f.Category) {
			return Result{}, fmt.Errorf("finding[%d]: invalid category %q", i, f.Category)
		}
		// Severity floor: unverifiable_codebase_claim findings are always
		// minor — the reviewer can't know if the claim is wrong, only that
		// it can't check.
		if f.Category == CategoryUnverifiableCodebaseClaim && f.Severity != SeverityMinor {
			r.Findings[i].Severity = SeverityMinor
		}
	}
```

Note: the loop now uses `for i := range` + `f := r.Findings[i]` instead of `for i, f := range` so that the mutation `r.Findings[i].Severity = SeverityMinor` actually updates the slice element (not a value copy).

- [ ] **Step 6: Run the parser tests to verify they now pass**

Run: `go test -race ./internal/verdict/ -run "TestParse_UnverifiableCodebaseClaim_SeverityFloorToMinor|TestParse_MalformedEvidenceCategory_Accepted" -v`
Expected: both PASS.

- [ ] **Step 7: Apply the same severity floor in `plan_parser.go`**

In `internal/verdict/plan_parser.go`, locate the `validateFinding` helper. It's used for both plan_findings and per-task findings. The helper currently doesn't mutate the finding. Change its signature to accept a pointer so it can apply the floor, and update both call sites:

```go
func validateFinding(f *Finding, where string) error {
	switch f.Severity {
	case SeverityCritical, SeverityMajor, SeverityMinor:
	default:
		return fmt.Errorf("plan: %s.severity invalid %q", where, f.Severity)
	}
	if !validCategory(f.Category) {
		return fmt.Errorf("plan: %s.category invalid %q", where, f.Category)
	}
	if f.Category == CategoryUnverifiableCodebaseClaim && f.Severity != SeverityMinor {
		f.Severity = SeverityMinor
	}
	return nil
}
```

Update the call sites in `ParsePlan`:

```go
	for i := range r.PlanFindings {
		if err := validateFinding(&r.PlanFindings[i], fmt.Sprintf("plan_findings[%d]", i)); err != nil {
			return PlanResult{}, err
		}
	}
	for i := range r.Tasks {
		t := &r.Tasks[i]
		prefix := fmt.Sprintf("task[%d]", i)
		if err := validatePlanVerdict(t.Verdict, prefix+".verdict"); err != nil {
			return PlanResult{}, err
		}
		if t.TaskIndex < 0 {
			return PlanResult{}, fmt.Errorf("plan: %s.task_index must be >= 0, got %d", prefix, t.TaskIndex)
		}
		if t.TaskTitle == "" {
			return PlanResult{}, fmt.Errorf("plan: %s.task_title is required", prefix)
		}
		for j := range t.Findings {
			if err := validateFinding(&t.Findings[j], fmt.Sprintf("%s.findings[%d]", prefix, j)); err != nil {
				return PlanResult{}, err
			}
		}
	}
```

- [ ] **Step 8: Apply the same severity floor in `parser_partial.go`**

In `internal/verdict/parser_partial.go`, locate where findings are appended to the recovered result (search for `Findings` assignments inside `ParseResultPartial` and `ParsePlanResultPartial`). After parsing each finding, BEFORE appending it to the slice, apply the floor. The cleanest place is to add a small helper at the top of the file:

```go
// applySeverityFloor enforces the category-based severity floors that
// match the strict parser's behavior, so partial-recovery output is
// consistent with strict output.
func applySeverityFloor(f Finding) Finding {
	if f.Category == CategoryUnverifiableCodebaseClaim && f.Severity != SeverityMinor {
		f.Severity = SeverityMinor
	}
	return f
}
```

Then locate every place in `parser_partial.go` where a `Finding` is constructed or appended into `r.Findings` / `pr.PlanFindings` / `t.Findings`. Wrap the value with `applySeverityFloor(...)`. (Inspect the file for the exact sites; in practice this is 1-3 spots in the partial-parser walker.)

If the partial parser uses `json.Unmarshal` on a recovered subslice first and then accepts whatever comes out, apply the floor with a post-pass:

```go
// After the partial parser has populated r.Findings (or pr.PlanFindings + each task's Findings):
for i := range r.Findings {
	r.Findings[i] = applySeverityFloor(r.Findings[i])
}
```

…and analogously for `pr.PlanFindings` and each `pr.Tasks[i].Findings`.

- [ ] **Step 9: Update `internal/verdict/schema.json` enum (add `unverifiable_codebase_claim` ONLY, NOT `malformed_evidence`)**

Open `internal/verdict/schema.json`. Locate the `category` enum array (lines roughly 19-28). Replace it with:

```json
            "enum": [
              "missing_acceptance_criterion",
              "scope_drift",
              "ambiguous_spec",
              "unaddressed_finding",
              "quality",
              "session_not_found",
              "payload_too_large",
              "unverifiable_codebase_claim",
              "other"
            ]
```

(`malformed_evidence` is intentionally absent — the schema constrains reviewer-emitted JSON, and reviewers never emit this category.)

- [ ] **Step 10: Update `internal/verdict/plan_schema.json` enum (unverifiable_codebase_claim ONLY, no malformed_evidence)**

Open `internal/verdict/plan_schema.json`. Locate the `category` enum (inside the `definitions.finding.properties.category` block). Replace with:

```json
            "enum": [
              "missing_acceptance_criterion",
              "scope_drift",
              "ambiguous_spec",
              "unaddressed_finding",
              "quality",
              "session_not_found",
              "payload_too_large",
              "unverifiable_codebase_claim",
              "other"
            ]
```

(Note: NO `malformed_evidence` — it's emitted only by `validate_completion`'s server-side guard, never by the validate_plan reviewer.)

- [ ] **Step 11: Update `internal/verdict/plan_findings_only_schema.json` (unverifiable_codebase_claim ONLY)**

Same change as Step 10 but for `plan_findings_only_schema.json`. Locate the `category` enum and append `"unverifiable_codebase_claim"` (NOT `malformed_evidence`).

- [ ] **Step 12: Update `internal/verdict/tasks_only_schema.json` (unverifiable_codebase_claim ONLY)**

Same change as Step 10 but for `tasks_only_schema.json`. Locate the `category` enum and append `"unverifiable_codebase_claim"` (NOT `malformed_evidence`).

- [ ] **Step 13: Run the full verdict test suite**

Run: `go test -race ./internal/verdict/...`
Expected: PASS — all existing tests still pass; the new severity-floor test passes.

- [ ] **Step 14: Run the full project test suite to confirm no cross-package regressions**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 15: Commit**

```bash
git add internal/verdict/verdict.go internal/verdict/parser.go internal/verdict/parser_partial.go internal/verdict/plan_parser.go internal/verdict/schema.json internal/verdict/plan_schema.json internal/verdict/plan_findings_only_schema.json internal/verdict/tasks_only_schema.json internal/verdict/parser_test.go
git commit -m "feat(verdict): add unverifiable_codebase_claim and malformed_evidence categories

unverifiable_codebase_claim is emitted by the reviewer when a plan or
task-spec statement asserts a codebase fact (field name, signature,
file existence, repo convention) that can't be verified from text
alone. Server enforces severity: minor for this category — the
reviewer can't know if the claim is wrong, only that it can't check.

malformed_evidence is reserved for the server-side validate_completion
evidence-shape guard (lands in Task 6); the parser accepts the
category and the schema.json enum lists it. NOT added to plan_*.json
schemas — those constrain validate_plan reviewer output, which never
emits this category.

Refs #12."
```

---

### Task 3: `plan_quality` field with server sanity check

**Goal:** Add the `PlanQuality` enum type, the `PlanQuality` field on `PlanResult` and `PlanFindingsOnly`, the JSON-schema enum entries, and the parser-side sanity check that overrides reviewer output when contradicted by hard signals (critical findings or `fail` verdict) and fills a verdict-based default when the field is missing or invalid.

**Acceptance criteria:**
- `internal/verdict/plan.go` exports a new `PlanQuality` type and constants `PlanQualityRough`, `PlanQualityActionable`, `PlanQualityRigorous`.
- `PlanResult` and `PlanFindingsOnly` gain a `PlanQuality` field (`json:"plan_quality"`, no `omitempty`).
- `plan_schema.json` and `plan_findings_only_schema.json` add `plan_quality` to required fields and to the `enum` constraint for that field.
- `ParsePlan` in `plan_parser.go` applies the sanity-check rules from spec §5: critical findings present → force `rough`; verdict == fail → force `rough`; empty/invalid value → fill from verdict-based default.
- 3+ new unit tests in `plan_parser_test.go` covering: override-on-critical, fill-on-empty, fill-on-invalid.
- `tasks_only_schema.json` does NOT add `plan_quality` (chunked per-task passes don't emit it).
- `go test -race ./internal/verdict/...` and `go test -race ./...` are green.

**Non-goals:**
- No prompt-template changes (Task 4 adds the reviewer instructions to emit `plan_quality`).
- No handler-level changes (Task 5/6).
- No CHANGELOG entry (Task 7).

**Context:** Spec §5. `plan_quality` is a separate axis from `plan_verdict`: where `plan_verdict` answers "is this dispatchable?", `plan_quality` answers "how close to ship-ready?". Reviewer emits it on the single-call path (`plan.tmpl`) and on chunked Pass 1 (`plan_findings_only.tmpl`); chunked Pass 2+ (`plan_tasks_chunk.tmpl` → `TasksOnly`) does NOT emit it. The server has two defensive layers: JSON schema constrains the happy path (reviewer is told it's required); Go parser tolerates omission/invalid via verdict-based fallback.

**Files:**
- Modify: `internal/verdict/plan.go`
- Modify: `internal/verdict/plan_parser.go`
- Modify: `internal/verdict/plan_findings_only_parser.go` (parse + validate `PlanQuality` for chunked Pass 1)
- Modify: `internal/verdict/plan_schema.json`
- Modify: `internal/verdict/plan_findings_only_schema.json`
- Modify: `internal/verdict/plan_parser_test.go`
- Modify: `internal/mcpsrv/handlers.go` (thread `pf.PlanQuality` into the chunked-assembled `PlanResult` in `reviewPlanChunked`)
- Modify: `internal/mcpsrv/handlers_plan_test.go` (chunked-path plan_quality threading test)

- [ ] **Step 1: Write the failing parser sanity-check tests**

Add to `internal/verdict/plan_parser_test.go`:

```go
func TestParsePlan_PlanQuality_CriticalOverridesToRough(t *testing.T) {
	// Reviewer claims `rigorous` but emitted a critical finding —
	// server overrides to `rough`.
	raw := []byte(`{
		"plan_verdict": "warn",
		"plan_findings": [{
			"severity":   "critical",
			"category":   "missing_acceptance_criterion",
			"criterion":  "Task 1 AC",
			"evidence":   "no acceptance criteria listed",
			"suggestion": "add ACs"
		}],
		"tasks": [],
		"next_action": "Address findings.",
		"plan_quality": "rigorous"
	}`)
	pr, err := ParsePlan(raw)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if got, want := pr.PlanQuality, PlanQualityRough; got != want {
		t.Errorf("PlanQuality = %q, want %q (critical finding should override to rough)", got, want)
	}
}

func TestParsePlan_PlanQuality_FailVerdictOverridesToRough(t *testing.T) {
	raw := []byte(`{
		"plan_verdict": "fail",
		"plan_findings": [],
		"tasks": [],
		"next_action": "Plan is unimplementable.",
		"plan_quality": "actionable"
	}`)
	pr, err := ParsePlan(raw)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if got, want := pr.PlanQuality, PlanQualityRough; got != want {
		t.Errorf("PlanQuality = %q, want %q (fail verdict should override to rough)", got, want)
	}
}

func TestParsePlan_PlanQuality_EmptyFilledFromVerdict(t *testing.T) {
	cases := []struct {
		name        string
		verdict     string
		wantQuality PlanQuality
	}{
		{"pass→rigorous", "pass", PlanQualityRigorous},
		{"warn→actionable", "warn", PlanQualityActionable},
		{"fail→rough", "fail", PlanQualityRough},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{
				"plan_verdict": "` + tc.verdict + `",
				"plan_findings": [],
				"tasks": [],
				"next_action": "go.",
				"plan_quality": ""
			}`)
			pr, err := ParsePlan(raw)
			if err != nil {
				t.Fatalf("ParsePlan: %v", err)
			}
			if got, want := pr.PlanQuality, tc.wantQuality; got != want {
				t.Errorf("PlanQuality = %q, want %q", got, want)
			}
		})
	}
}

func TestParsePlan_PlanQuality_InvalidFilledFromVerdict(t *testing.T) {
	// Reviewer drift: emits "sparkly" — parser falls back to
	// verdict-based default ("warn" → actionable) without erroring.
	raw := []byte(`{
		"plan_verdict": "warn",
		"plan_findings": [],
		"tasks": [],
		"next_action": "go.",
		"plan_quality": "sparkly"
	}`)
	pr, err := ParsePlan(raw)
	if err != nil {
		t.Fatalf("ParsePlan should not error on invalid plan_quality: %v", err)
	}
	if got, want := pr.PlanQuality, PlanQualityActionable; got != want {
		t.Errorf("PlanQuality = %q, want %q (invalid value should fall back to verdict-based default)", got, want)
	}
}
```

- [ ] **Step 2: Run to verify tests fail (compile-error or wrong value)**

Run: `go test -race ./internal/verdict/ -run "TestParsePlan_PlanQuality_" -v`
Expected: FAIL — `PlanQuality` type undefined.

- [ ] **Step 3: Add `PlanQuality` type and field to `plan.go`**

In `internal/verdict/plan.go`, add this block (recommended location: after the `PlanFindingsOnly` struct or before `PlanResult`):

```go
// PlanQuality is a separate axis from PlanVerdict, indicating how close
// the plan is to ship-ready independent of whether it's dispatchable.
//
//	rough      — implementer cannot start; missing pieces / contradictions.
//	actionable — dispatchable but with gaps an implementer might have to
//	             ask about; some quality issues that risk rework.
//	rigorous   — ready to hand to a fresh implementer with high confidence;
//	             remaining findings are stylistic.
type PlanQuality string

const (
	PlanQualityRough      PlanQuality = "rough"
	PlanQualityActionable PlanQuality = "actionable"
	PlanQualityRigorous   PlanQuality = "rigorous"
)
```

Update the `PlanResult` struct to:

```go
type PlanResult struct {
	PlanVerdict  Verdict          `json:"plan_verdict"`
	PlanFindings []Finding        `json:"plan_findings"`
	Tasks        []PlanTaskResult `json:"tasks"`
	NextAction   string           `json:"next_action"`
	Partial      bool             `json:"partial,omitempty"`
	PlanQuality  PlanQuality      `json:"plan_quality"`
}
```

Update the `PlanFindingsOnly` struct to:

```go
type PlanFindingsOnly struct {
	PlanVerdict  Verdict     `json:"plan_verdict"`
	PlanFindings []Finding   `json:"plan_findings"`
	NextAction   string      `json:"next_action"`
	PlanQuality  PlanQuality `json:"plan_quality"`
}
```

(Note: `TasksOnly` is intentionally NOT updated — chunked Pass 2+ doesn't emit `plan_quality`.)

- [ ] **Step 4: Add a helper for the sanity check + fallback in `plan_parser.go`**

In `internal/verdict/plan_parser.go`, add this helper (recommended location: near the bottom, after `validateFinding`). **Exported** — `handlers.go`'s `planEnvelopeResult` calls it from a different package in Task 5:

```go
// ApplyPlanQualitySanity enforces the plan_quality contract:
//
//   - any critical finding forces "rough" regardless of what the reviewer emitted
//   - fail verdict forces "rough"
//   - empty/invalid value falls back to a verdict-based default:
//       pass → rigorous, warn → actionable, fail → rough
//
// This is defensive: the JSON schema requires plan_quality on the happy
// path, but raw-response drift (parse miss, prompt drift, missing field)
// must not produce empty output.
func ApplyPlanQualitySanity(pr *PlanResult) {
	// 1. fail verdict floors to rough.
	if pr.PlanVerdict == VerdictFail {
		pr.PlanQuality = PlanQualityRough
		return
	}
	// 2. any critical finding (plan-level or task-level) floors to rough.
	hasCritical := false
	for _, f := range pr.PlanFindings {
		if f.Severity == SeverityCritical {
			hasCritical = true
			break
		}
	}
	if !hasCritical {
		for _, t := range pr.Tasks {
			for _, f := range t.Findings {
				if f.Severity == SeverityCritical {
					hasCritical = true
					break
				}
			}
			if hasCritical {
				break
			}
		}
	}
	if hasCritical {
		pr.PlanQuality = PlanQualityRough
		return
	}
	// 3. empty or invalid → verdict-based default.
	switch pr.PlanQuality {
	case PlanQualityRough, PlanQualityActionable, PlanQualityRigorous:
		// reviewer emitted a valid value; trust it.
	default:
		switch pr.PlanVerdict {
		case VerdictPass:
			pr.PlanQuality = PlanQualityRigorous
		case VerdictWarn:
			pr.PlanQuality = PlanQualityActionable
		case VerdictFail:
			pr.PlanQuality = PlanQualityRough
		}
	}
}
```

- [ ] **Step 5: Wire the sanity check into `ParsePlan`**

In `internal/verdict/plan_parser.go`, at the end of `ParsePlan` (right before the `return r, nil`), add a call to the sanity helper:

```go
	// ... existing validation loop ...
	ApplyPlanQualitySanity(&r)
	return r, nil
```

- [ ] **Step 6: Run the parser tests to verify they now pass**

Run: `go test -race ./internal/verdict/ -run "TestParsePlan_PlanQuality_" -v`
Expected: PASS — all 4 sub-tests (and the 3 verdict-based subtests in TestParsePlan_PlanQuality_EmptyFilledFromVerdict) pass.

- [ ] **Step 7: Update `internal/verdict/plan_schema.json`**

Open the file. Locate the top-level `required` array (currently `["plan_verdict", "plan_findings", "tasks", "next_action"]`). Replace with:

```json
  "required": ["plan_verdict", "plan_findings", "tasks", "next_action", "plan_quality"],
```

Then locate the top-level `properties` object. Add a new property after the existing entries:

```json
    "plan_quality": {
      "type": "string",
      "enum": ["rough", "actionable", "rigorous"]
    }
```

(Add a comma after the previous property entry if needed to preserve valid JSON.)

- [ ] **Step 8: Update `internal/verdict/plan_findings_only_schema.json`**

Same shape as Step 7: add `plan_quality` to the top-level `required` array and a new `plan_quality` property with the enum constraint.

- [ ] **Step 9: Verify the chunked-path `TasksOnly` JSON is untouched**

Run: `grep -c plan_quality internal/verdict/tasks_only_schema.json`
Expected: `0` — chunked Pass 2+ never emits `plan_quality`.

- [ ] **Step 10: Run the full verdict test suite**

Run: `go test -race ./internal/verdict/...`
Expected: PASS — all existing tests still pass, new sanity-check tests pass.

- [ ] **Step 11: Update `ParsePlanFindingsOnly` to parse + validate `PlanQuality`**

Open `internal/verdict/plan_findings_only_parser.go`. Locate `ParsePlanFindingsOnly`. Add the same enum validation and fallback that `ParsePlan` does, scoped to the `PlanFindingsOnly` shape. After the existing per-finding validation loop, add:

```go
	// Validate plan_quality enum (defensive — reviewer should emit; we
	// tolerate omission/invalid and let the assembling caller in
	// reviewPlanChunked apply the verdict-based fallback via the shared
	// sanity helper).
	switch r.PlanQuality {
	case PlanQualityRough, PlanQualityActionable, PlanQualityRigorous, "":
		// OK — empty is tolerated; chunked assembler will apply fallback.
	default:
		// Reviewer drift; clear so the assembler applies the fallback.
		r.PlanQuality = ""
	}
```

(This keeps `ParsePlanFindingsOnly` lenient — same defensive policy as `ParsePlan`.)

- [ ] **Step 12: Update `reviewPlanChunked` to thread `pf.PlanQuality` + apply sanity check**

In `internal/mcpsrv/handlers.go`, locate `reviewPlanChunked`. Find the point where it assembles a `PlanResult` from the parsed `PlanFindingsOnly` (search for `pf.PlanVerdict` or similar). The current code likely does something like:

```go
result := verdict.PlanResult{
	PlanVerdict:  pf.PlanVerdict,
	PlanFindings: pf.PlanFindings,
	NextAction:   pf.NextAction,
}
```

Update to:

```go
result := verdict.PlanResult{
	PlanVerdict:  pf.PlanVerdict,
	PlanFindings: pf.PlanFindings,
	NextAction:   pf.NextAction,
	PlanQuality:  pf.PlanQuality,
}
```

The chunked path does NOT need to call `applyPlanQualitySanity` directly — Task 5's `planEnvelopeResult` change (next task) calls it inside the marshaller so every PlanResult exit path gets it. But verify that ordering: Task 5 Step 7 makes `planEnvelopeResult` apply the sanity check.

- [ ] **Step 13: Add a chunked-path threading test**

Add to `internal/mcpsrv/handlers_plan_test.go`:

```go
func TestValidatePlan_Chunked_PlanQualityThreadedFromPass1(t *testing.T) {
	// Use a stub scripted reviewer that returns plan_quality:"actionable"
	// in Pass 1 and a tasks-only response in Pass 2. The assembled
	// PlanResult must carry "actionable", not empty/invalid.
	scripts := []providers.Response{
		{
			// Pass 1: plan_findings_only
			RawJSON: []byte(`{"plan_verdict":"warn","plan_findings":[{"severity":"minor","category":"quality","criterion":"c","evidence":"e","suggestion":"s"}],"next_action":"go","plan_quality":"actionable"}`),
			Model:   "claude-opus-4-7",
		},
		{
			// Pass 2: tasks_only (single chunk with all tasks)
			RawJSON: []byte(`{"tasks":[{"task_index":1,"task_title":"Task 1: x","verdict":"warn","findings":[],"suggested_header_block":"","suggested_header_reason":""}]}`),
			Model:   "claude-opus-4-7",
		},
	}
	rv := newScriptedReviewer("anthropic", scripts)
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}
	d.Cfg.PlanTasksPerChunk = 1
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: x\n\nbody.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	if err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	if pr.PlanQuality != verdict.PlanQualityActionable {
		t.Errorf("plan_quality = %q, want actionable (threaded from Pass 1)", pr.PlanQuality)
	}
}
```

(If `newScriptedReviewer` doesn't exist, look at how existing chunked-path tests script multi-response reviewers and follow that pattern; or define it as a small helper.)

- [ ] **Step 14: Run the full verdict + mcpsrv test suite**

Run: `go test -race ./internal/verdict/... ./internal/mcpsrv/...`
Expected: PASS.

- [ ] **Step 15: Run the full project test suite to verify no cross-package regressions**

Run: `go test -race ./...`
Expected: PASS — note: `handlers_plan_test.go` tests that consume `PlanResult` JSON may show the new `plan_quality` field; they should still pass because they assert on specific fields (verdict, findings, etc.) not on full JSON shape.

If any test fails on JSON-equality comparison (full-string match against expected JSON), update the expected JSON to include the new `plan_quality` field.

- [ ] **Step 16: Commit**

```bash
git add internal/verdict/plan.go internal/verdict/plan_parser.go internal/verdict/plan_findings_only_parser.go internal/verdict/plan_schema.json internal/verdict/plan_findings_only_schema.json internal/verdict/plan_parser_test.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_plan_test.go
git commit -m "feat(verdict): plan_quality field with sanity check

PlanResult and PlanFindingsOnly gain a plan_quality field
(rough/actionable/rigorous), separate axis from plan_verdict. The
sanity check overrides the reviewer's emitted value when contradicted
by hard signals (critical findings or fail verdict) and fills a
verdict-based default when the field is missing or invalid. Two
defensive layers: JSON schema constrains the happy path; Go parser
fallback handles raw-response drift.

TasksOnly is unchanged — chunked Pass 2+ doesn't emit plan_quality.

Refs #12."
```

---

### Task 4: Prompt template edits + golden regeneration

**Goal:** Add the new reviewer instructions to all four text-only templates: a paragraph in the `## Reviewer ground rules` block of `plan.tmpl`, `plan_findings_only.tmpl`, and `plan_tasks_chunk.tmpl` instructing the reviewer to emit `unverifiable_codebase_claim` findings, plus a `plan_quality` emission instruction in the first two of those (not the chunk template); and a new `### Unverifiable codebase claims` section in `pre.tmpl` with the same instruction adapted for task-spec context. Regenerate all 7 affected golden files.

**Acceptance criteria:**
- `plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl` each contain a new ground-rules paragraph anchored on the literal string `"unverifiable_codebase_claim"`.
- `plan.tmpl` and `plan_findings_only.tmpl` also contain a `**plan_quality**` instruction anchored on `"plan_quality"` and the three quality values.
- `plan_tasks_chunk.tmpl` does NOT contain the `**plan_quality**` instruction.
- `pre.tmpl` contains a new `### Unverifiable codebase claims` section anchored on `"unverifiable_codebase_claim"`.
- `mid.tmpl` and `post.tmpl` do NOT contain `"unverifiable_codebase_claim"` (they receive code; no blind spot).
- All 7 golden files regenerate cleanly: `plan_basic.golden`, `plan_findings_only.golden`, `plan_tasks_chunk.golden`, `plan_basic_quick.golden`, `plan_findings_only_quick.golden`, `plan_tasks_chunk_quick.golden`, `pre_basic.golden`.
- 5+ new anchor-assertion tests in `prompts_test.go` cover the inclusions and 1 negative test confirms `mid.tmpl`/`post.tmpl` exclusion.
- `go test -race ./internal/prompts/...` is green; `go test -race ./...` is green.

**Non-goals:**
- No schema or parser changes (Tasks 2 and 3).
- No handler or marshaller changes (Tasks 5 and 6).

**Context:** Spec §4 (unverifiable_codebase_claim) and §5 (plan_quality). The exact paragraph text is identical across all four text-only templates except for "plan"/"task spec" terminology and the `plan_quality` instruction's scoping. `pre.tmpl` doesn't have a `## Reviewer ground rules` block today, so the new instruction lives under a fresh `### Unverifiable codebase claims` section near the existing severity rubric.

**Files:**
- Modify: `internal/prompts/templates/plan.tmpl`
- Modify: `internal/prompts/templates/plan_findings_only.tmpl`
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl`
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/testdata/plan_basic.golden`
- Modify: `internal/prompts/testdata/plan_findings_only.golden`
- Modify: `internal/prompts/testdata/plan_tasks_chunk.golden`
- Modify: `internal/prompts/testdata/plan_basic_quick.golden`
- Modify: `internal/prompts/testdata/plan_findings_only_quick.golden`
- Modify: `internal/prompts/testdata/plan_tasks_chunk_quick.golden`
- Modify: `internal/prompts/testdata/pre_basic.golden`
- Modify: `internal/prompts/prompts_test.go`

- [ ] **Step 1: Write the failing anchor-assertion tests**

Add to `internal/prompts/prompts_test.go`:

```go
func TestRenderPlan_UnverifiableCodebaseClaim_InstructionPresent(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: samplePlanText()})
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	if !strings.Contains(out, "unverifiable_codebase_claim") {
		t.Errorf("plan.tmpl should mention unverifiable_codebase_claim category")
	}
	if !strings.Contains(out, "verify against the actual code") {
		t.Errorf("plan.tmpl should include the 'verify against the actual code' guidance")
	}
}

func TestRenderPlanFindingsOnly_UnverifiableCodebaseClaim_InstructionPresent(t *testing.T) {
	out, err := RenderPlanFindingsOnly(PlanInput{PlanText: samplePlanText()})
	if err != nil {
		t.Fatalf("RenderPlanFindingsOnly: %v", err)
	}
	if !strings.Contains(out, "unverifiable_codebase_claim") {
		t.Errorf("plan_findings_only.tmpl should mention unverifiable_codebase_claim category")
	}
}

func TestRenderPlanTasksChunk_UnverifiableCodebaseClaim_InstructionPresent(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanTasksChunkInput{
		PlanText:   samplePlanText(),
		ChunkTasks: []ChunkTaskRef{{Index: 1, Title: "Task 1: example"}},
	})
	if err != nil {
		t.Fatalf("RenderPlanTasksChunk: %v", err)
	}
	if !strings.Contains(out, "unverifiable_codebase_claim") {
		t.Errorf("plan_tasks_chunk.tmpl should mention unverifiable_codebase_claim category")
	}
}

func TestRenderPre_UnverifiableCodebaseClaim_InstructionPresent(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	if err != nil {
		t.Fatalf("RenderPre: %v", err)
	}
	if !strings.Contains(out, "unverifiable_codebase_claim") {
		t.Errorf("pre.tmpl should mention unverifiable_codebase_claim category")
	}
	if !strings.Contains(out, "### Unverifiable codebase claims") {
		t.Errorf("pre.tmpl should include the new section heading")
	}
}

func TestRenderMid_DoesNotMentionUnverifiableCodebaseClaim(t *testing.T) {
	out, err := RenderMid(MidInput{
		Spec:          sampleSpec(),
		WorkingOn:     "writing the handler",
		ChangedFiles:  nil,
		PriorFindings: nil,
	})
	if err != nil {
		t.Fatalf("RenderMid: %v", err)
	}
	if strings.Contains(out, "unverifiable_codebase_claim") {
		t.Errorf("mid.tmpl should NOT include unverifiable_codebase_claim (it receives code)")
	}
}

func TestRenderPost_DoesNotMentionUnverifiableCodebaseClaim(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:    sampleSpec(),
		Summary: "implemented X",
	})
	if err != nil {
		t.Fatalf("RenderPost: %v", err)
	}
	if strings.Contains(out, "unverifiable_codebase_claim") {
		t.Errorf("post.tmpl should NOT include unverifiable_codebase_claim (it receives code)")
	}
}

func TestRenderPlan_PlanQuality_InstructionPresent(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: samplePlanText()})
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	if !strings.Contains(out, "plan_quality") {
		t.Errorf("plan.tmpl should mention plan_quality")
	}
	for _, v := range []string{"rough", "actionable", "rigorous"} {
		if !strings.Contains(out, v) {
			t.Errorf("plan.tmpl should mention %q quality value", v)
		}
	}
}

func TestRenderPlanFindingsOnly_PlanQuality_InstructionPresent(t *testing.T) {
	out, err := RenderPlanFindingsOnly(PlanInput{PlanText: samplePlanText()})
	if err != nil {
		t.Fatalf("RenderPlanFindingsOnly: %v", err)
	}
	if !strings.Contains(out, "plan_quality") {
		t.Errorf("plan_findings_only.tmpl should mention plan_quality")
	}
}

func TestRenderPlanTasksChunk_DoesNotMentionPlanQuality(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanTasksChunkInput{
		PlanText:   samplePlanText(),
		ChunkTasks: []ChunkTaskRef{{Index: 1, Title: "Task 1: example"}},
	})
	if err != nil {
		t.Fatalf("RenderPlanTasksChunk: %v", err)
	}
	if strings.Contains(out, "**plan_quality**") {
		t.Errorf("plan_tasks_chunk.tmpl should NOT include the **plan_quality** emission instruction (chunked Pass 2+ doesn't emit it)")
	}
}
```

(If any of `samplePlanText` / `sampleSpec` aren't already defined as helpers in `prompts_test.go`, look for existing test fixtures and reuse them — the existing 0.3.0 tests reference `RenderPlan(PlanInput{PlanText: ...})` and `RenderPre(PreInput{Spec: ...})` so the inputs work the same way.)

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test -race ./internal/prompts/ -run "TestRender.*_UnverifiableCodebaseClaim|TestRender.*_PlanQuality" -v`
Expected: FAIL — anchor strings not yet in templates.

- [ ] **Step 3: Edit `plan.tmpl` — add ground-rules paragraph + plan_quality instruction**

Open `internal/prompts/templates/plan.tmpl`. Locate the `## Reviewer ground rules` heading. After the existing ground-rules content (the 0.3.0 hypothetical-marker paragraph is the last one), ADD as a new paragraph:

```
When the plan asserts something about the codebase that you cannot verify from the plan text alone — a field name, function signature, file path, insertion point in a graph, existing convention in adjacent code, or a type-system fact — DO emit a finding with `category: unverifiable_codebase_claim`. Severity should be `minor` (the claim might be true; you just can't check). `evidence` quotes the claim verbatim from the plan. `suggestion` says "verify against the actual code before dispatching." Do this instead of silently passing or fabricating a critique. The human will see the checklist and grep the codebase.
```

THEN locate the `## What to evaluate` section (or wherever the schema-fields are described). ADD the `plan_quality` instruction:

```
**plan_quality** — emit one of `"rough"`, `"actionable"`, or `"rigorous"`:

- `rough`: implementer cannot start; spec is missing critical pieces, or contradictions block coherent dispatch.
- `actionable`: spec is dispatchable but has meaningful gaps an implementer would have to ask clarifying questions about, or quality issues that risk rework.
- `rigorous`: spec is ready to hand to a fresh implementer with high confidence; remaining findings are stylistic or expected-of-the-process.
```

- [ ] **Step 4: Edit `plan_findings_only.tmpl` — same edits**

Open `internal/prompts/templates/plan_findings_only.tmpl`. Apply the SAME two edits from Step 3: append the unverifiable_codebase_claim paragraph to the `## Reviewer ground rules` block, and add the `plan_quality` instruction in the same scope-appropriate section.

- [ ] **Step 5: Edit `plan_tasks_chunk.tmpl` — ground-rules paragraph ONLY (no plan_quality)**

Open `internal/prompts/templates/plan_tasks_chunk.tmpl`. Add ONLY the unverifiable_codebase_claim paragraph to the `## Reviewer ground rules` block. Do NOT add the `plan_quality` instruction — chunked Pass 2+ doesn't emit it.

- [ ] **Step 6: Edit `pre.tmpl` — new `### Unverifiable codebase claims` section**

Open `internal/prompts/templates/pre.tmpl`. After the existing severity rubric line (`Severity: critical = spec is unimplementable as written; ...`), INSERT a new section:

```
### Unverifiable codebase claims

When the task spec asserts something about the codebase that you cannot verify from the text alone — a field name, function signature, file path, insertion point in a graph, existing convention in adjacent code, or a type-system fact — DO emit a finding with `category: unverifiable_codebase_claim`. Severity should be `minor` (the claim might be true; you just can't check). `evidence` quotes the claim verbatim. `suggestion` says "verify against the actual code before dispatching." Do this instead of silently passing or fabricating a critique. The human will see the checklist and grep the codebase.
```

- [ ] **Step 7: Run the anchor tests to verify they now pass**

Run: `go test -race ./internal/prompts/ -run "TestRender.*_UnverifiableCodebaseClaim|TestRender.*_PlanQuality" -v`
Expected: all anchor tests PASS; the two negative tests on `mid.tmpl` and `post.tmpl` also PASS (no edits there); the `TestRenderPlanTasksChunk_DoesNotMentionPlanQuality` negative also PASSes (no plan_quality in chunk template).

- [ ] **Step 8: Regenerate all golden files**

Run: `go test ./internal/prompts/... -update`
Expected: tests PASS; golden files are rewritten. Note this will modify all 7 affected goldens: 3 plan default + 3 plan quick + 1 pre.

- [ ] **Step 9: Inspect each regenerated golden by diffing against base**

Run: `git diff --stat internal/prompts/testdata/`
Expected: 7 files changed.

For each file, run `git diff internal/prompts/testdata/<file>` and verify the diff shows ONLY the additions (the new paragraph + the plan_quality instruction where applicable). No incidental whitespace changes, no template-variable changes. If a diff shows unexpected content, abort and investigate.

Specifically:
- `plan_basic.golden`, `plan_findings_only.golden` — should show unverifiable_codebase_claim paragraph AND plan_quality instruction additions.
- `plan_tasks_chunk.golden` — should show ONLY the unverifiable_codebase_claim paragraph (no plan_quality).
- The three quick-mode goldens — same shape as their non-quick counterparts (the mode-suffix only changes the existing quick-mode content, which the spec leaves unchanged).
- `pre_basic.golden` — should show the new `### Unverifiable codebase claims` section.

- [ ] **Step 10: Run the full prompts test suite**

Run: `go test -race ./internal/prompts/...`
Expected: PASS — all anchor tests, all golden-comparison tests.

- [ ] **Step 11: Run the full project test suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/prompts/templates/plan.tmpl internal/prompts/templates/plan_findings_only.tmpl internal/prompts/templates/plan_tasks_chunk.tmpl internal/prompts/templates/pre.tmpl internal/prompts/testdata/plan_basic.golden internal/prompts/testdata/plan_findings_only.golden internal/prompts/testdata/plan_tasks_chunk.golden internal/prompts/testdata/plan_basic_quick.golden internal/prompts/testdata/plan_findings_only_quick.golden internal/prompts/testdata/plan_tasks_chunk_quick.golden internal/prompts/testdata/pre_basic.golden internal/prompts/prompts_test.go
git commit -m "feat(prompts): unverifiable_codebase_claim guidance + plan_quality instruction

- plan.tmpl, plan_findings_only.tmpl, plan_tasks_chunk.tmpl gain a new
  paragraph in ## Reviewer ground rules instructing the reviewer to
  emit unverifiable_codebase_claim findings when it can't verify a
  plan statement against the codebase (severity floor: minor).
- plan.tmpl and plan_findings_only.tmpl additionally gain a
  **plan_quality** instruction in the schema-fields section. The
  chunk template does NOT (chunked Pass 2+ doesn't emit plan_quality).
- pre.tmpl gains a new ### Unverifiable codebase claims section
  with the same instruction adapted for task-spec context.
- 7 golden files regenerated.

Refs #12."
```

---

### Task 5: `summary_block` field populated inside envelope marshallers

**Goal:** Add `SummaryBlock` fields to `verdict.Result` (for per-task envelopes) and `verdict.PlanResult` (for the plan envelope), implement `formatEnvelopeSummary` and `formatPlanSummary` helpers in a new `internal/mcpsrv/summary.go`, and populate the field INSIDE `envelopeResult` and `planEnvelopeResult` so every exit path — happy paths, partial-recovery paths, legacy-truncation paths, `notFoundEnvelope`, `tooLargeEnvelope`, `noHeadingsPlanResult`, `truncatedPlanResult`, evidence-shape rejection (Task 6) — populates the field automatically.

**Acceptance criteria:**
- `mcpsrv.Envelope` (in `internal/mcpsrv/handlers.go`) gains a `SummaryBlock string` field with `json:"summary_block,omitempty"`. Per-task tools return Envelopes, not `verdict.Result`, so the field belongs here.
- `verdict.PlanResult` (in `internal/verdict/plan.go`) gains a `SummaryBlock string` field with `json:"summary_block,omitempty"`. The plan tool returns `PlanResult` directly (via `planEnvelopeResult`), so the field goes on the response type.
- **`verdict.Result` does NOT gain `SummaryBlock`.** Result is an intermediate parse target for reviewer JSON; it is never the response shape returned to the MCP caller. Adding it there would relax `verdict.Parse` to accept a `summary_block` field from the reviewer (against the schema's `additionalProperties: false`), which is undesired.
- `internal/mcpsrv/summary.go` exports `formatEnvelopeSummary(Envelope) string` and `formatPlanSummary(PlanResult, modelUsed string, reviewMS int64) string` per the spec's format spec (plain text, deterministic order, 120-char evidence truncation with `…` suffix).
- `envelopeResult` populates `env.SummaryBlock = formatEnvelopeSummary(env)` before marshaling; `planEnvelopeResult` populates `pr.SummaryBlock = formatPlanSummary(pr, modelUsed, reviewMS)` AND calls `verdict.ApplyPlanQualitySanity(&pr)` before marshaling (so synthetic plan envelopes get the same sanity treatment as parsed ones — fixes #2 from the review).
- `internal/mcpsrv/summary_test.go` has 6+ format-determinism tests with inline expected strings, 1 truncation test, 1 empty-envelope omitempty test.
- 4+ handler integration tests confirm `summary_block` is populated on the happy path of each tool.
- 4+ early-return tests confirm `summary_block` is ALSO populated for `notFoundEnvelope`, `tooLargeEnvelope`, `noHeadingsPlanResult`, evidence-shape rejection (the last is verified after Task 6 lands the guard; in this task, write the test against the existing reject envelope shape).
- `go test -race ./...` is green.

**Non-goals:**
- No evidence-shape guard (Task 6).
- No lightweight-mode session_id allowance (Task 6).

**Context:** Spec §3. The wrappers `envelopeResult` (`handlers.go:210`) and `planEnvelopeResult` (`handlers.go:847`) are the choke points where MCP `*CallToolResult` is marshalled. By populating `SummaryBlock` INSIDE these wrappers, every caller (success, partial-recovery, legacy-truncation, every early-return envelope) gets the field automatically — no per-handler wiring. Format: plain text, single-line findings truncated at 120 chars, stable field order, no markdown/ANSI/emoji.

**Files:**
- Modify: `internal/verdict/plan.go` (add `SummaryBlock` field to `PlanResult`)
- Modify: `internal/verdict/plan_parser.go` (rename `applyPlanQualitySanity` → exported `ApplyPlanQualitySanity` so handlers can call it from `planEnvelopeResult`; OR keep unexported and add a thin exported wrapper).
- Create: `internal/mcpsrv/summary.go`
- Create: `internal/mcpsrv/summary_test.go`
- Modify: `internal/mcpsrv/handlers.go` (add `Envelope.SummaryBlock` field; `envelopeResult` populates summary_block; `planEnvelopeResult` populates summary_block AND applies plan_quality sanity check)
- Modify: `internal/mcpsrv/handlers_test.go` (new integration tests)

- [ ] **Step 1: Add `SummaryBlock` to `mcpsrv.Envelope`**

In `internal/mcpsrv/handlers.go`, locate the `Envelope` struct (near the top of the file). Add a new field:

```go
type Envelope struct {
	// ... existing fields ...
	SummaryBlock string `json:"summary_block,omitempty"`
}
```

Place the new field after the existing optional fields (`Partial`, `SessionExpiresAt`, `SessionTTLRemainingSeconds`, etc.) so JSON output ordering stays predictable.

**Do NOT add `SummaryBlock` to `verdict.Result`.** Result is an intermediate parse target for reviewer JSON. The schema's `additionalProperties: false` plus the Go parser's `DisallowUnknownFields` policy means even a leaked `summary_block` on the reviewer side would error — and we don't want to relax that. The Envelope is the response shape; that's where the field goes.

- [ ] **Step 2: Add `SummaryBlock` to `PlanResult`**

In `internal/verdict/plan.go`, update the `PlanResult` struct:

```go
type PlanResult struct {
	PlanVerdict  Verdict          `json:"plan_verdict"`
	PlanFindings []Finding        `json:"plan_findings"`
	Tasks        []PlanTaskResult `json:"tasks"`
	NextAction   string           `json:"next_action"`
	Partial      bool             `json:"partial,omitempty"`
	PlanQuality  PlanQuality      `json:"plan_quality"`
	SummaryBlock string           `json:"summary_block,omitempty"`
}
```

- [ ] **Step 3: Write the format-determinism tests**

Create `internal/mcpsrv/summary_test.go`:

```go
package mcpsrv

import (
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestFormatEnvelopeSummary_Basic(t *testing.T) {
	env := Envelope{
		SessionID:    "sess-abc",
		Verdict:      string(verdict.VerdictWarn),
		Findings: []verdict.Finding{
			{
				Severity:   verdict.SeverityMajor,
				Category:   verdict.CategoryAmbiguousSpec,
				Criterion:  "AC #2",
				Evidence:   `"under 50ms" — at what load?`,
				Suggestion: "Pin the load profile (RPS).",
			},
		},
		NextAction: "Pin the load profile and re-run.",
		ModelUsed:  "anthropic:claude-sonnet-4-6",
		ReviewMS:   1234,
	}
	got := formatEnvelopeSummary(env)
	wantLines := []string{
		"anti-tangent envelope",
		"  session_id:    sess-abc",
		"  verdict:       warn",
		"  model_used:    anthropic:claude-sonnet-4-6",
		"  review_ms:     1234",
		"  findings:      1 total (0 critical, 1 major, 0 minor)",
		`    - [major][ambiguous_spec] AC #2 — "under 50ms" — at what load?`,
		"  next_action:   Pin the load profile and re-run.",
	}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Errorf("summary missing line %q\n--- got ---\n%s", line, got)
		}
	}
}

func TestFormatEnvelopeSummary_TruncatesLongEvidence(t *testing.T) {
	longEvidence := strings.Repeat("x", 500)
	env := Envelope{
		Verdict:  string(verdict.VerdictPass),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMinor,
			Category:   verdict.CategoryOther,
			Criterion:  "long",
			Evidence:   longEvidence,
			Suggestion: "fix it",
		}},
		NextAction: "ok",
		ModelUsed:  "anthropic:claude-sonnet-4-6",
	}
	got := formatEnvelopeSummary(env)
	// Find the line with the long evidence; assert it's truncated to ≤120
	// chars after the marker and ends with the truncation ellipsis.
	lines := strings.Split(got, "\n")
	var findingLine string
	for _, l := range lines {
		if strings.Contains(l, "long") {
			findingLine = l
			break
		}
	}
	if findingLine == "" {
		t.Fatalf("could not find finding line:\n%s", got)
	}
	if !strings.Contains(findingLine, "…") {
		t.Errorf("expected truncation marker (…) in finding line, got:\n%s", findingLine)
	}
	// The evidence portion (after "— ") should be ≤120 chars + 1 for the marker.
	if idx := strings.Index(findingLine, "— "); idx != -1 {
		evidence := findingLine[idx+len("— "):]
		if len(evidence) > 121 {
			t.Errorf("evidence too long: %d chars (expected ≤121)\n%s", len(evidence), evidence)
		}
	}
}

func TestFormatEnvelopeSummary_NoSession_NoTTLLine(t *testing.T) {
	env := Envelope{
		// SessionID empty, no TTL fields
		Verdict:    string(verdict.VerdictFail),
		Findings:   nil,
		NextAction: "Re-submit with complete evidence.",
		ModelUsed:  "anthropic:claude-sonnet-4-6",
	}
	got := formatEnvelopeSummary(env)
	if strings.Contains(got, "session_ttl_remaining_seconds") {
		t.Errorf("no-TTL envelope should not include session_ttl line:\n%s", got)
	}
}

func TestFormatEnvelopeSummary_Partial_LineShown(t *testing.T) {
	env := Envelope{
		SessionID:  "sess-xyz",
		Verdict:    string(verdict.VerdictWarn),
		Partial:    true,
		NextAction: "Retry with higher max_tokens_override.",
		ModelUsed:  "anthropic:claude-sonnet-4-6",
	}
	got := formatEnvelopeSummary(env)
	if !strings.Contains(got, "partial:       true") {
		t.Errorf("partial=true envelope should show the partial line:\n%s", got)
	}
}

func TestFormatPlanSummary_Basic(t *testing.T) {
	pr := verdict.PlanResult{
		PlanVerdict:  verdict.VerdictWarn,
		PlanFindings: []verdict.Finding{},
		Tasks: []verdict.PlanTaskResult{
			{
				TaskIndex:             1,
				TaskTitle:             "Task 1: example",
				Verdict:               verdict.VerdictPass,
				Findings:              []verdict.Finding{},
				SuggestedHeaderBlock:  "",
				SuggestedHeaderReason: "",
			},
		},
		NextAction:  "go",
		PlanQuality: verdict.PlanQualityRigorous,
	}
	got := formatPlanSummary(pr, "anthropic:claude-opus-4-7", 5678)
	for _, line := range []string{
		"anti-tangent envelope (validate_plan)",
		"  plan_verdict:  warn",
		"  plan_quality:  rigorous",
		"  model_used:    anthropic:claude-opus-4-7",
		"  review_ms:     5678",
		"  tasks: 1",
		"    Task 1: Task 1: example  [pass]",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("plan summary missing line %q\n--- got ---\n%s", line, got)
		}
	}
}

func TestFormatPlanSummary_PartialFlag_Shown(t *testing.T) {
	pr := verdict.PlanResult{
		PlanVerdict: verdict.VerdictWarn,
		Partial:     true,
		NextAction:  "retry",
		PlanQuality: verdict.PlanQualityActionable,
	}
	got := formatPlanSummary(pr, "anthropic:claude-opus-4-7", 100)
	if !strings.Contains(got, "partial:       true") {
		t.Errorf("partial=true plan should show partial line:\n%s", got)
	}
}
```

- [ ] **Step 4: Run the failing tests**

Run: `go test -race ./internal/mcpsrv/ -run "TestFormatEnvelopeSummary|TestFormatPlanSummary" -v`
Expected: FAIL — `formatEnvelopeSummary` / `formatPlanSummary` undefined.

- [ ] **Step 5: Implement the helpers in `internal/mcpsrv/summary.go`**

Create `internal/mcpsrv/summary.go`:

```go
package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// summaryEvidenceMax is the maximum character count for the evidence
// portion of a finding line in the summary_block. Longer evidence is
// truncated with a trailing ellipsis.
const summaryEvidenceMax = 120

// formatEnvelopeSummary builds the per-task summary_block. Plain text,
// deterministic field order, single-line findings with truncation.
// Documented as human-readable, NOT a stable machine interface.
func formatEnvelopeSummary(env Envelope) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope\n")
	fmt.Fprintf(&b, "  session_id:    %s\n", env.SessionID)
	fmt.Fprintf(&b, "  verdict:       %s\n", env.Verdict)
	if env.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", env.ModelUsed)
	fmt.Fprintf(&b, "  review_ms:     %d\n", env.ReviewMS)
	if env.SessionTTLRemainingSeconds != nil {
		fmt.Fprintf(&b, "  session_ttl_remaining_seconds: %d\n", *env.SessionTTLRemainingSeconds)
	}
	writeFindingsSummary(&b, env.Findings, "  ")
	fmt.Fprintf(&b, "  next_action:   %s\n", env.NextAction)
	return b.String()
}

// formatPlanSummary builds the plan-level summary_block.
func formatPlanSummary(pr verdict.PlanResult, modelUsed string, reviewMS int64) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope (validate_plan)\n")
	fmt.Fprintf(&b, "  plan_verdict:  %s\n", pr.PlanVerdict)
	fmt.Fprintf(&b, "  plan_quality:  %s\n", pr.PlanQuality)
	if pr.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", modelUsed)
	fmt.Fprintf(&b, "  review_ms:     %d\n", reviewMS)
	crit, maj, min := countSeverities(pr.PlanFindings)
	fmt.Fprintf(&b, "  plan_findings: %d (%d/%d/%d)\n", len(pr.PlanFindings), crit, maj, min)
	for _, f := range pr.PlanFindings {
		fmt.Fprintf(&b, "    - [%s][%s] %s — %s\n", f.Severity, f.Category, f.Criterion, truncate(f.Evidence, summaryEvidenceMax))
	}
	fmt.Fprintf(&b, "  tasks: %d\n", len(pr.Tasks))
	for _, t := range pr.Tasks {
		crit, maj, min := countSeverities(t.Findings)
		fmt.Fprintf(&b, "    Task %d: %s  [%s]  findings: %d (%d/%d/%d)\n",
			t.TaskIndex, t.TaskTitle, t.Verdict, len(t.Findings), crit, maj, min)
		for _, f := range t.Findings {
			fmt.Fprintf(&b, "      - [%s] %s — %s\n", f.Severity, f.Criterion, truncate(f.Evidence, summaryEvidenceMax))
		}
	}
	fmt.Fprintf(&b, "  next_action:   %s\n", pr.NextAction)
	return b.String()
}

func writeFindingsSummary(b *strings.Builder, findings []verdict.Finding, indent string) {
	crit, maj, min := countSeverities(findings)
	fmt.Fprintf(b, "%sfindings:      %d total (%d critical, %d major, %d minor)\n", indent, len(findings), crit, maj, min)
	for _, f := range findings {
		fmt.Fprintf(b, "%s  - [%s][%s] %s — %s\n", indent, f.Severity, f.Category, f.Criterion, truncate(f.Evidence, summaryEvidenceMax))
	}
}

func countSeverities(findings []verdict.Finding) (critical, major, minor int) {
	for _, f := range findings {
		switch f.Severity {
		case verdict.SeverityCritical:
			critical++
		case verdict.SeverityMajor:
			major++
		case verdict.SeverityMinor:
			minor++
		}
	}
	return critical, major, minor
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
```

- [ ] **Step 6: Populate `summary_block` inside `envelopeResult`**

In `internal/mcpsrv/handlers.go`, locate `envelopeResult` (around line 210). Update to:

```go
func envelopeResult(env Envelope) (*mcp.CallToolResult, Envelope, error) {
	env.SummaryBlock = formatEnvelopeSummary(env)
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, Envelope{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, env, nil
}
```

- [ ] **Step 7: Populate `summary_block` AND apply `plan_quality` sanity check inside `planEnvelopeResult`**

In `internal/mcpsrv/handlers.go`, locate `planEnvelopeResult` (around line 847). Update to:

```go
func planEnvelopeResult(pr verdict.PlanResult, modelUsed string, ms int64) (*mcp.CallToolResult, verdict.PlanResult, error) {
	// Apply plan_quality sanity check on EVERY exit path. Synthetic
	// PlanResults (noHeadingsPlanResult, tooLargePlanResult,
	// truncatedPlanResult, evidence-shape rejection, etc.) skip the
	// parser, so the sanity helper has to run here to fill the field
	// from the verdict-based default.
	verdict.ApplyPlanQualitySanity(&pr)
	pr.SummaryBlock = formatPlanSummary(pr, modelUsed, ms)
	body, err := json.MarshalIndent(struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{pr, modelUsed, ms}, "", "  ")
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, pr, nil
}
```

This requires `verdict.ApplyPlanQualitySanity` to be exported. In `internal/verdict/plan_parser.go`, rename `applyPlanQualitySanity` → `ApplyPlanQualitySanity`. The function called inside `ParsePlan` (Task 3 Step 5) now also uses the exported name.

- [ ] **Step 8: Run the format-determinism tests**

Run: `go test -race ./internal/mcpsrv/ -run "TestFormatEnvelopeSummary|TestFormatPlanSummary" -v`
Expected: PASS — all 6 tests.

- [ ] **Step 9: Add handler integration tests for the happy path**

Add to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_PopulatesSummaryBlock(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"go"}`),
			Model:   "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "T",
		Goal:               "G",
		AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("ValidateTaskSpec: %v", err)
	}
	if env.SummaryBlock == "" {
		t.Errorf("summary_block should be populated on happy path")
	}
	if !strings.Contains(env.SummaryBlock, "anti-tangent envelope") {
		t.Errorf("summary_block should start with envelope header, got:\n%s", env.SummaryBlock)
	}
}

func TestValidateCompletion_PopulatesSummaryBlock(t *testing.T) {
	// Pre-create a session via ValidateTaskSpec.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"go"}`),
			Model:   "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "T",
		Goal:               "G",
		AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("validate_task_spec setup: %v", err)
	}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n"}},
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.SummaryBlock == "" {
		t.Errorf("summary_block should be populated on validate_completion")
	}
}

func TestValidatePlan_PopulatesSummaryBlock(t *testing.T) {
	rv := &fakeReviewer{
		name: "openai",
		resp: providers.Response{
			RawJSON: []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: x","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go","plan_quality":"rigorous"}`),
			Model:   "gpt-5",
		},
	}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: x\n\nbody.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	if err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	if pr.SummaryBlock == "" {
		t.Errorf("plan summary_block should be populated")
	}
	if !strings.Contains(pr.SummaryBlock, "anti-tangent envelope (validate_plan)") {
		t.Errorf("plan summary_block should start with plan header, got:\n%s", pr.SummaryBlock)
	}
}

func TestValidateTaskSpec_NotFoundEnvelope_PopulatesSummaryBlock(t *testing.T) {
	// CheckProgress with a bogus session_id hits notFoundEnvelope → summary_block must still populate.
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: "bogus-session-id",
		WorkingOn: "x",
	})
	if err != nil {
		t.Fatalf("CheckProgress: %v", err)
	}
	if env.SummaryBlock == "" {
		t.Errorf("notFoundEnvelope path should still populate summary_block")
	}
	if env.Verdict != string(verdict.VerdictFail) {
		t.Errorf("expected verdict=fail, got %s", env.Verdict)
	}
}

func TestValidatePlan_NoHeadingsResult_PopulatesSummaryBlock(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: providers.Response{Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: "no task headings here",
	})
	if err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	if pr.SummaryBlock == "" {
		t.Errorf("noHeadingsPlanResult path should still populate summary_block")
	}
}
```

- [ ] **Step 10: Run the new integration tests**

Run: `go test -race ./internal/mcpsrv/ -run "_PopulatesSummaryBlock" -v`
Expected: all 5 PASS.

- [ ] **Step 11: Run full test suite**

Run: `go test -race ./...`
Expected: PASS. Existing JSON-equality-based handler tests may surface the new `summary_block` field in marshalled output; if any fail, update their expected JSON or weaken to substring assertions.

- [ ] **Step 12: Commit**

```bash
git add internal/verdict/plan.go internal/verdict/plan_parser.go internal/mcpsrv/summary.go internal/mcpsrv/summary_test.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "feat(mcpsrv): summary_block field populated inside envelope marshallers

Adds mcpsrv.Envelope.SummaryBlock and verdict.PlanResult.SummaryBlock
(both json:'summary_block,omitempty'). verdict.Result intentionally
does NOT get the field — it's an intermediate parse target, not the
response shape. New formatEnvelopeSummary and formatPlanSummary
helpers in summary.go build a plain-text, deterministic, paste-ready
envelope for implementer DONE reports.

Population happens INSIDE envelopeResult and planEnvelopeResult so
every exit path (happy, partial-recovery, legacy-truncation,
notFoundEnvelope, tooLargeEnvelope, noHeadingsPlanResult,
truncatedPlanResult, eventual evidence-shape rejection) carries the
field automatically. planEnvelopeResult also runs the plan_quality
sanity check via the now-exported verdict.ApplyPlanQualitySanity, so
synthetic PlanResults (which skip ParsePlan) get the same enum
sanitization as parsed ones.

Refs #12."
```

---

### Task 6: `validate_completion` evidence-shape guard + lightweight mode

**Goal:** Add a pre-reviewer guard to `validate_completion` that rejects malformed evidence (truncation markers in `final_diff` or `final_files`, empty `Path` entries) before the LLM call. Cache rejections by canonical content hash (per spec §2 round-3 update). Accept an empty `session_id` when at least one piece of evidence is non-empty, with a synthesized minimal task spec for the reviewer (lightweight protocol mode handler change).

**Acceptance criteria:**
- `validate_completion` returns an envelope with `Verdict: fail`, `Findings: [{ Severity: major, Category: malformed_evidence, Criterion: "evidence_shape", ... }]`, and `NextAction` mentioning "Re-submit with complete evidence" when ANY of the spec's three detection rules fires.
- Rejections are cached in an in-process cache keyed by `sha256(json.Marshal({SessionID, FinalDiff, FinalFiles (sorted by Path), TestEvidence}))`. Cache TTL: 5 minutes from insertion. Cache hit → return same envelope without calling reviewer (verified by call-counter test).
- The rejection envelope's `summary_block` is populated (Task 5's marshaller path covers this).
- When `args.SessionID == ""` AND at least one of `final_files` / `final_diff` / `test_evidence` is non-empty, the handler synthesizes a minimal `session.TaskSpec` (Title: "(lightweight task)", Goal: args.Summary, no ACs) and proceeds with the reviewer call (no session is created or returned).
- When `args.SessionID == ""` AND ALL of `final_files` / `final_diff` / `test_evidence` are empty, the existing "at least one of ... must be non-empty" error fires (unchanged from 0.3.0).
- 4 unit tests cover rejection rules: truncation marker in `final_diff`, truncation marker in `final_files[].Content`, empty `final_files[].Path`, cache-hit short-circuit.
- 2 negative tests confirm: a complete diff with `@@` hunks AND complete `final_files` passes through; a mode-only diff (`old mode 100644 / new mode 100755` with no `@@`) ALSO passes through.
- 1 lightweight-mode test: empty `session_id` + valid evidence → reviewer is called, envelope returned with empty SessionID.
- `go test -race ./internal/mcpsrv/...` and `go test -race ./...` are green.

**Non-goals:**
- No changes to other tools' handlers (only `validate_completion` gets the guard).
- No docs (Task 1 already covered the doc text; Task 7 covers the CHANGELOG entry).

**Context:** Spec §2 (with round-3 canonical-hash revision). Detection rules: (1) `final_diff` contains a truncation marker via case-insensitive substring match against `(truncated)`, `[truncated]`, `// ... unchanged`, `<!-- truncated -->`, or line-anchored `(?m)^\s*\.\.\.\s*$`; (2) `final_files` entries with empty `Path`; (3) `final_files` entries with `Content` matching the same patterns as rule 1. NOT included (dropped from earlier draft): "diff --git with zero @@" rule (false-positives on mode-only / rename-only / binary diffs). The cache key uses canonical JSON over a struct with files sorted by Path to avoid hash collisions from plain concatenation.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`ValidateCompletion` handler + guard helper + cache + lightweight-mode allowance)
- Modify: `internal/mcpsrv/handlers_test.go` (new tests)

- [ ] **Step 1: Write the failing guard tests**

Add to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateCompletion_EvidenceGuard_RejectsTruncationMarkerInDiff(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	// Set up a session first
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	initialCalls := rv.Calls

	// Submit a diff with a truncation marker; the guard should reject without calling the reviewer.
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalDiff: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n(truncated)\n+new\n",
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != string(verdict.VerdictFail) {
		t.Errorf("verdict = %s, want fail", env.Verdict)
	}
	if len(env.Findings) == 0 || env.Findings[0].Category != verdict.CategoryMalformedEvidence {
		t.Errorf("expected malformed_evidence finding, got: %+v", env.Findings)
	}
	if rv.Calls != initialCalls {
		t.Errorf("reviewer was called (%d → %d); guard should have rejected before reviewer", initialCalls, rv.Calls)
	}
}

func TestValidateCompletion_EvidenceGuard_RejectsTruncationMarkerInFinalFiles(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n// ... unchanged\nfunc Foo() {}\n"}},
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != string(verdict.VerdictFail) {
		t.Errorf("verdict = %s, want fail", env.Verdict)
	}
}

func TestValidateCompletion_EvidenceGuard_RejectsEmptyFilePath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalFiles: []FileArg{{Path: "", Content: "anything"}},
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != string(verdict.VerdictFail) {
		t.Errorf("verdict = %s, want fail", env.Verdict)
	}
}

func TestValidateCompletion_EvidenceGuard_CompleteDiffPassesThrough(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalDiff: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n",
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != "pass" {
		t.Errorf("complete diff should pass through to reviewer (pass), got %s", env.Verdict)
	}
}

func TestValidateCompletion_EvidenceGuard_ModeOnlyDiffPassesThrough(t *testing.T) {
	// Pure chmod change: `diff --git` header + old/new mode, no @@ hunks.
	// Must NOT be rejected (the dropped rule 2 would have false-failed this).
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	modeOnlyDiff := "diff --git a/script.sh b/script.sh\nold mode 100644\nnew mode 100755\n"
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "made script executable",
		FinalDiff: modeOnlyDiff,
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != "pass" {
		t.Errorf("mode-only diff should pass through to reviewer (pass), got verdict=%s findings=%+v", env.Verdict, env.Findings)
	}
}

func TestValidateCompletion_EvidenceGuard_CacheHitShortCircuits(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	callsBefore := rv.Calls
	args := ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalDiff: "diff --git a/x b/x\n@@ -1 +1 @@\n(truncated)\n",
	}
	// First call — rejection cached.
	_, env1, err := h.ValidateCompletion(context.Background(), nil, args)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if env1.Verdict != string(verdict.VerdictFail) {
		t.Fatalf("first call should reject")
	}
	callsAfterFirst := rv.Calls
	// Second call with identical args — must hit cache, no reviewer call.
	_, env2, err := h.ValidateCompletion(context.Background(), nil, args)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if env2.Verdict != string(verdict.VerdictFail) {
		t.Errorf("second call should also reject (from cache)")
	}
	// Reviewer should not have been called in either rejection.
	if rv.Calls != callsAfterFirst {
		t.Errorf("reviewer should not have been called between cached rejections; calls before=%d after=%d", callsAfterFirst, rv.Calls)
	}
	_ = callsBefore
}

func TestValidateCompletion_LightweightMode_EmptySessionAccepted(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: "",
		Summary:   "trivial doc change",
		FinalFiles: []FileArg{{Path: "doc.md", Content: "updated\n"}},
	})
	if err != nil {
		t.Fatalf("ValidateCompletion (lightweight): %v", err)
	}
	if env.SessionID != "" {
		t.Errorf("lightweight mode should not surface a session_id, got %q", env.SessionID)
	}
	if env.Verdict != "pass" {
		t.Errorf("lightweight mode reviewer call should pass with stub response, got %s", env.Verdict)
	}
}

func TestValidateCompletion_LightweightMode_NoEvidenceErrors(t *testing.T) {
	// Empty session_id AND all evidence empty → existing "at least one" error.
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: "",
		Summary:   "x",
	})
	if err == nil {
		t.Errorf("expected error when session_id is empty AND no evidence is provided")
	}
	if !strings.Contains(err.Error(), "at least one of") {
		t.Errorf("error should mention 'at least one of'; got: %v", err)
	}
}
```

- [ ] **Step 2: Run the failing tests**

Run: `go test -race ./internal/mcpsrv/ -run "TestValidateCompletion_EvidenceGuard|TestValidateCompletion_LightweightMode" -v`
Expected: FAIL — guard not yet implemented; empty session_id rejected by existing code.

- [ ] **Step 3: Add evidence-shape detection helper and cache**

In `internal/mcpsrv/handlers.go`, near the top imports add:

```go
import (
	"crypto/sha256"
	"encoding/json"
	"regexp"
	"sort"
	"sync"
	"time"
)
```

(Merge with existing imports — many of these are likely already present. `regexp`, `crypto/sha256`, `sort`, and `sync` may be new.)

Add this block somewhere in the file (recommend: near the existing handler helpers, around line 200-220):

```go
// evidenceTruncationPatterns is the case-insensitive substring match
// list for evidence-shape rejection.
var evidenceTruncationPatterns = []string{
	"(truncated)",
	"[truncated]",
	"// ... unchanged",
	"<!-- truncated -->",
}

// evidenceEllipsisLine matches a line consisting only of `...` and
// optional whitespace (line-anchored).
var evidenceEllipsisLine = regexp.MustCompile(`(?m)^\s*\.\.\.\s*$`)

// checkEvidenceShape returns a non-empty rejection reason when the
// submitted evidence appears malformed. The reason is included in the
// rejection envelope's evidence field; an empty return means OK.
func checkEvidenceShape(args ValidateCompletionArgs) string {
	// Rule 1: final_diff contains truncation markers.
	if args.FinalDiff != "" {
		lower := strings.ToLower(args.FinalDiff)
		for _, p := range evidenceTruncationPatterns {
			if idx := strings.Index(lower, p); idx >= 0 {
				return fmt.Sprintf("final_diff contains truncation marker %q at offset %d", p, idx)
			}
		}
		if loc := evidenceEllipsisLine.FindStringIndex(args.FinalDiff); loc != nil {
			return fmt.Sprintf("final_diff contains a placeholder line `...` at offset %d", loc[0])
		}
	}
	// Rule 2: final_files entries with empty Path.
	for i, f := range args.FinalFiles {
		if f.Path == "" {
			return fmt.Sprintf("final_files[%d].path is empty", i)
		}
	}
	// Rule 3: final_files entries with Content matching the same patterns.
	for i, f := range args.FinalFiles {
		lower := strings.ToLower(f.Content)
		for _, p := range evidenceTruncationPatterns {
			if idx := strings.Index(lower, p); idx >= 0 {
				return fmt.Sprintf("final_files[%d].content (path %q) contains truncation marker %q at offset %d", i, f.Path, p, idx)
			}
		}
		if loc := evidenceEllipsisLine.FindStringIndex(f.Content); loc != nil {
			return fmt.Sprintf("final_files[%d].content (path %q) contains a placeholder line `...` at offset %d", i, f.Path, loc[0])
		}
	}
	return ""
}

// rejectionCache short-circuits identical malformed-evidence submissions
// for 5 minutes.
type rejectionCacheEntry struct {
	envelope  Envelope
	expiresAt time.Time
}

var (
	rejectionCacheMu sync.Mutex
	rejectionCache   = map[[32]byte]rejectionCacheEntry{}
)

const rejectionCacheTTL = 5 * time.Minute

// evidenceCacheKey returns a canonical hash over the evidence payload.
// Plain string concatenation would be collision-prone (a file with
// path "abc",content "" concatenates identically to path "",content "abc");
// JSON-marshaling a struct with files pre-sorted by Path produces a
// deterministic, collision-resistant key.
func evidenceCacheKey(args ValidateCompletionArgs) [32]byte {
	sortedFiles := append([]FileArg(nil), args.FinalFiles...)
	sort.Slice(sortedFiles, func(i, j int) bool { return sortedFiles[i].Path < sortedFiles[j].Path })
	keyInput := struct {
		SessionID    string    `json:"session_id"`
		FinalDiff    string    `json:"final_diff"`
		FinalFiles   []FileArg `json:"final_files"`
		TestEvidence string    `json:"test_evidence"`
	}{
		SessionID:    args.SessionID,
		FinalDiff:    args.FinalDiff,
		FinalFiles:   sortedFiles,
		TestEvidence: args.TestEvidence,
	}
	keyJSON, _ := json.Marshal(keyInput)
	return sha256.Sum256(keyJSON)
}

// lookupCachedRejection returns the cached envelope and ok=true if a
// non-expired rejection exists for the given key.
func lookupCachedRejection(key [32]byte) (Envelope, bool) {
	rejectionCacheMu.Lock()
	defer rejectionCacheMu.Unlock()
	entry, ok := rejectionCache[key]
	if !ok {
		return Envelope{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(rejectionCache, key)
		return Envelope{}, false
	}
	return entry.envelope, true
}

func storeRejection(key [32]byte, env Envelope) {
	rejectionCacheMu.Lock()
	defer rejectionCacheMu.Unlock()
	rejectionCache[key] = rejectionCacheEntry{
		envelope:  env,
		expiresAt: time.Now().Add(rejectionCacheTTL),
	}
}

// malformedEvidenceEnvelope builds the rejection envelope for evidence-shape
// failures. SummaryBlock is populated later by envelopeResult.
func malformedEvidenceEnvelope(sessionID, reason, modelUsed string) Envelope {
	return Envelope{
		SessionID:  sessionID,
		Verdict:    string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryMalformedEvidence,
			Criterion:  "evidence_shape",
			Evidence:   reason,
			Suggestion: "Submit full file contents in final_files, or a complete unified diff (no truncation markers) in final_diff.",
		}},
		NextAction: "Re-submit with complete evidence; current submission appears truncated.",
		ModelUsed:  modelUsed,
	}
}
```

- [ ] **Step 4: Wire the guard + cache + lightweight-mode allowance into `ValidateCompletion`**

In `internal/mcpsrv/handlers.go`, locate the `ValidateCompletion` handler (around line 632). The current top includes (in order): (1) `args.SessionID == ""` validation, (2) `args.Summary == ""` validation, (3) `effectiveMaxTokens` computation with clamp finding, (4) `totalCompletionBytes` payload-size check returning `tooLargeEnvelope` with clamp, (5) session lookup returning `notFoundEnvelope` with clamp, (6) prompt rendering, (7) reviewer call. Preserve all of these — the rewrite reorders for lightweight mode but does not drop any.

**Explicit ordering for the rewritten handler** (top of `ValidateCompletion`):

```go
func (h *handlers) ValidateCompletion(ctx context.Context, _ *mcp.CallToolRequest, args ValidateCompletionArgs) (*mcp.CallToolResult, Envelope, error) {
	// 1. Summary is always required.
	if args.Summary == "" {
		return nil, Envelope{}, errors.New("summary is required")
	}

	// 2. At least one of final_files, final_diff, test_evidence must be
	//    non-empty (unchanged from 0.2.0). If session_id is also empty,
	//    this error fires before lightweight mode is even considered —
	//    a "completely empty" call cannot proceed.
	if len(args.FinalFiles) == 0 && args.FinalDiff == "" && args.TestEvidence == "" {
		return nil, Envelope{}, errors.New("at least one of final_files, final_diff, or test_evidence must be non-empty")
	}

	// 3. Lightweight mode marker. Empty session_id + evidence present
	//    → lightweight; reviewer is called with a synthesized spec.
	lightweight := args.SessionID == ""

	// 4. Compute effective max_tokens + clamp finding (preserves the
	//    0.3.0 max_tokens_override flow). Clamp is applied to all
	//    early-return envelopes via prependClamp below.
	maxTokens, clampFinding, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PostModel, h.deps.Cfg.PostMaxTokens)
	if err != nil {
		return nil, Envelope{}, err
	}

	// 5. Payload cap (preserves the 0.2.0 MaxPayloadBytes check). On
	//    overflow, return tooLargeEnvelope. In lightweight mode, sess is
	//    nil so we pass an empty session ID into the envelope.
	if size := totalCompletionBytes(args.FinalFiles, args.FinalDiff); size > h.deps.Cfg.MaxPayloadBytes {
		sessID := ""
		if !lightweight {
			sessID = args.SessionID
		}
		return envelopeResult(prependClamp(
			tooLargeEnvelope(sessID, h.deps.Cfg.PostModel, size, h.deps.Cfg.MaxPayloadBytes,
				"Send a unified diff via final_diff, or split the call into smaller chunks."),
			clampFinding,
		))
	}

	// 6. Evidence-shape guard with rejection cache. Runs BEFORE session
	//    lookup so a malformed lightweight-mode submission is rejected
	//    cleanly, without an unrelated session error.
	cacheKey := evidenceCacheKey(args)
	if cachedEnv, ok := lookupCachedRejection(cacheKey); ok {
		return envelopeResult(prependClamp(cachedEnv, clampFinding))
	}
	if reason := checkEvidenceShape(args); reason != "" {
		rejection := malformedEvidenceEnvelope(args.SessionID, reason, h.deps.Cfg.PostModel.String())
		storeRejection(cacheKey, rejection)
		return envelopeResult(prependClamp(rejection, clampFinding))
	}

	// 7. Session lookup (lightweight mode skips this).
	var sess *session.Session
	if !lightweight {
		var ok bool
		sess, ok = h.deps.Sessions.Get(args.SessionID)
		if !ok {
			return envelopeResult(prependClamp(
				notFoundEnvelope(args.SessionID, h.deps.Cfg.PostModel),
				clampFinding,
			))
		}
	}

	// 8. Determine the spec the reviewer sees: lightweight mode
	//    synthesizes; per-session mode reads from the session.
	var spec session.TaskSpec
	if lightweight {
		spec = session.TaskSpec{
			Title:              "(lightweight task)",
			Goal:               args.Summary,
			AcceptanceCriteria: nil,
			NonGoals:           nil,
			Context:            "",
		}
	} else {
		spec = sess.Spec
	}

	// ... existing body continues from here, using `spec`, `maxTokens`,
	// and `clampFinding`. Replace any reference to `sess.Spec` with
	// `spec`; gate any reference to `sess.ID`, `sess.LastAccessed`,
	// `sess.PreFindings` on `!lightweight`. The reviewer call below uses
	// `maxTokens`. The final envelope construction uses `sessID = ""` in
	// lightweight mode and `sess.ID` otherwise. prependClamp wraps the
	// final envelope just as it does in the existing code path.
}
```

(`PostMaxTokens` and `effectiveMaxTokens` already exist in the 0.3.0 codebase — this rewrite preserves their use unchanged.)

In the body of the handler that comes after the rewrite shown above, find every place where `sess.Spec` / `sess.ID` / `sess.LastAccessed` / `sess.PreFindings` is referenced, and guard each with the lightweight check:

- Replace `sess.Spec` → `spec` (defined above).
- Replace `sess.ID` (used in the final Envelope construction) → conditional: `sessID := ""; if !lightweight { sessID = sess.ID }`.
- Skip any session.Store update calls (e.g. `s.UpdateLastAccessed`) when `lightweight` is true.
- Skip the `sess.PreFindings` lookup when `lightweight` is true (treat as empty).

The final envelope construction (after the reviewer call) becomes:

```go
	sessID := ""
	if !lightweight {
		sessID = sess.ID
	}
	env := Envelope{
		SessionID:                  sessID,
		Verdict:                    string(result.Verdict),
		Findings:                   result.Findings,
		NextAction:                 result.NextAction,
		ModelUsed:                  resp.Model,
		ReviewMS:                   ms,
		Partial:                    result.Partial,
		SessionExpiresAt:           sessExpiresAt,           // nil in lightweight mode
		SessionTTLRemainingSeconds: sessTTL,                  // nil in lightweight mode
	}
	env = prependClamp(env, clampFinding)
	return envelopeResult(env)
```

(Initialize `sessExpiresAt` and `sessTTL` as `nil` at the top of the handler, then populate them only when `!lightweight` after the session lookup.)

- [ ] **Step 5: Run the new tests**

Run: `go test -race ./internal/mcpsrv/ -run "TestValidateCompletion_EvidenceGuard|TestValidateCompletion_LightweightMode" -v`
Expected: all PASS.

- [ ] **Step 6: Confirm existing tests still pass**

Run: `go test -race ./internal/mcpsrv/...`
Expected: PASS — including the existing `TestValidateCompletion_*` tests that use a real session_id.

- [ ] **Step 7: Confirm full project**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "feat(mcpsrv): validate_completion evidence-shape guard + lightweight mode

Adds a pre-reviewer guard to validate_completion that rejects malformed
evidence (truncation markers in final_diff or final_files, empty Path
entries) with a malformed_evidence finding. Rejections are cached for
5 minutes by canonical content hash (SHA-256 over JSON-marshalled
struct with files pre-sorted by Path — avoids collision risk of plain
concatenation).

Detection rules deliberately exclude 'diff --git with zero @@' (would
false-fail mode-only / rename-only / binary diffs). Only truncation-
marker patterns and empty Paths fire the guard.

Lightweight protocol mode: validate_completion now accepts an empty
session_id when at least one piece of evidence is non-empty. The
handler synthesizes a minimal task spec (Title: '(lightweight task)',
Goal: args.Summary, no ACs) for the reviewer. No session is created or
returned; the envelope's session_id stays empty.

Refs #12."
```

---

### Task 7: CHANGELOG / README / final docs

**Goal:** Add the `## [0.3.1] - 2026-05-13` CHANGELOG block, add a README paragraph pointing at the new lightweight-mode capability, add an INTEGRATION.md troubleshooting entry for the new `malformed_evidence` category, and add the `plan_quality` description to the `validate_plan` per-tool section.

**Acceptance criteria:**
- `CHANGELOG.md` has a new `## [0.3.1] - 2026-05-13` block above the `## [0.3.0]` entry. Per repo convention (see `CLAUDE.md` Keep-a-Changelog discipline; established by the 0.3.0 PR's CodeRabbit feedback), the entry includes ALL SIX subsections — `### Added`, `### Changed`, `### Fixed`, `### Removed`, `### Deprecated`, `### Security` — using `_None._` placeholders where empty. Plus a `### Documentation` subsection for the INTEGRATION.md scope-and-limits + lightweight-mode docs.
- `README.md` mentions lightweight protocol mode and links to `examples/lightweight-dispatch.md`.
- `INTEGRATION.md` has a troubleshooting paragraph for `malformed_evidence` near the existing `payload_too_large` troubleshooting block.
- `INTEGRATION.md`'s `validate_plan` per-tool section has a paragraph describing the new `plan_quality` field.
- `git grep '## \[0.3.1\]' CHANGELOG.md` returns 1 match.
- `go test -race ./...` is green (docs-only — sanity check).
- Branch is now ready to push and open as a PR.

**Non-goals:**
- No VERSION file edit — release workflow handles that on merge.
- No GitHub release notes — workflow generates from CHANGELOG.

**Context:** Spec's CHANGELOG entry preview (in §"CHANGELOG entry (0.3.1)"). The 0.3.0 PR had a CodeRabbit finding about Keep a Changelog subsection completeness — the repo's `CLAUDE.md` says all six subsections (Added, Changed, Fixed, Removed, Deprecated, Security) are required. Use `_None._` for empty subsections to satisfy that convention.

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `README.md`
- Modify: `INTEGRATION.md`

- [ ] **Step 1: Add the CHANGELOG entry**

Open `CHANGELOG.md`. Find the `## [0.3.0] - 2026-05-12` entry. INSERT this block immediately above:

```markdown
## [0.3.1] - 2026-05-13

### Added
- `summary_block` field on every tool response: paste-ready textual envelope (verdict, findings, model_used, review_ms, session_ttl_remaining_seconds) that implementers can copy verbatim into DONE reports. Reduces the protocol's reliance on the implementer correctly formatting JSON.
- `plan_quality` field on `PlanResult` (`rough` | `actionable` | `rigorous`). Separate axis from `plan_verdict` — tracks "how close to ship-ready" rather than "is this dispatchable." Reviewer-emitted with a server sanity check (critical findings or `fail` verdict force `rough`; missing/invalid values fall back to verdict-based default).
- `unverifiable_codebase_claim` finding category: lets the reviewer explicitly flag plan or task-spec statements it cannot verify from text alone (field names, signatures, file paths, repo conventions) rather than silently passing or fabricating critiques. Server enforces `severity: minor` for this category. Applies to `validate_plan` and `validate_task_spec` (both text-only inputs); not applied to `check_progress` / `validate_completion` which receive code.
- `malformed_evidence` finding category: the new `validate_completion` evidence-shape guard rejects submissions that contain truncation markers (`(truncated)`, `[truncated]`, `// ... unchanged`, etc.) or empty `final_files.Path` entries — saves strong-model time on cycles that were driven by tooling friction rather than correctness. Replaces the (misleading) previous reuse of `payload_too_large` for shape failures.
- `examples/lightweight-dispatch.md` reference template for trivial tasks (doc edits, mechanical relocations).

### Changed
- `check_progress` demoted from RECOMMENDED to OPTIONAL in the dispatch-clause template. Field data showed 0 substantive catches across 5 representative tasks; the call is now advisory.
- `validate_completion` rejected-submissions are cached for 5 minutes by canonical content hash to short-circuit identical re-submissions (see the new `malformed_evidence` category above).
- `validate_completion` now accepts an empty `session_id` when `final_files`, `final_diff`, or `test_evidence` is non-empty — supports the new lightweight protocol mode. The reviewer is called with a synthesized task spec (Goal = `args.Summary`, no ACs).
- `summary_block` population moved to the marshalling helpers (`envelopeResult` / `planEnvelopeResult`) so every exit path — happy paths, partial-recovery, legacy-truncation, `notFoundEnvelope`, `tooLargeEnvelope`, `noHeadingsPlanResult`, evidence-shape rejection — populates the field automatically.

### Fixed
_None._

### Removed
_None._

### Deprecated
_None._

### Security
_None._

### Documentation
- New `## Scope and limits` section in `INTEGRATION.md` explicitly documents the text-only architectural boundary: what the tool catches, what it structurally cannot (codebase symbol existence, function signatures, repo-wide invariants encoded elsewhere, CI/test policy), and the recommendation to pair with a codebase-aware review for any plan that lands in real code.
- New `### Lightweight protocol mode` section in `INTEGRATION.md` documents the controller-side convention for trivial tasks.

Closes [#12](https://github.com/patiently/anti-tangent-mcp/issues/12).

```

- [ ] **Step 2: Add the README paragraph for lightweight mode**

Open `README.md`. Find a natural location — typically near the existing dispatch-protocol section or after the per-tool descriptions. INSERT this paragraph:

```markdown
### Lightweight protocol mode (v0.3.1+)

For trivial tasks (doc-only edits, mechanical relocations, dependency bumps), the full anti-tangent dispatch protocol is overhead-heavy. As of v0.3.1 the project ships a lightweight dispatch template at [`examples/lightweight-dispatch.md`](examples/lightweight-dispatch.md) that skips `validate_task_spec` and `check_progress`, keeping only `validate_completion` as a sanity gate. See `INTEGRATION.md`'s "Lightweight protocol mode" section for when to use it.
```

- [ ] **Step 3: Add the `plan_quality` paragraph to INTEGRATION.md's validate_plan section**

In `INTEGRATION.md`, locate the `validate_plan` per-tool section. INSERT this paragraph somewhere in the section (recommended: after the existing field descriptions, before any chunking sub-section):

```markdown
The `plan_quality` field (v0.3.1+) is a separate axis from `plan_verdict`. While `plan_verdict` answers "is this dispatchable?" (pass / warn / fail), `plan_quality` answers "how close is this to ship-ready?" (rough / actionable / rigorous). When you see consecutive `warn` verdicts that aren't changing, watch `plan_quality` for convergence: `actionable → rigorous` is a meaningful improvement even if the verdict stays `warn`. Use `plan_quality` to decide when to stop iterating: most callers can ship at `actionable` for ASAP work, and at `rigorous` for quarterly-rewrite scope.
```

- [ ] **Step 4: Add the `malformed_evidence` troubleshooting entry**

In `INTEGRATION.md`, find the existing troubleshooting block (search for `payload_too_large` or look for a section titled "Troubleshooting"). INSERT this entry near the `payload_too_large` description:

```markdown
**A `validate_completion` call returned a finding with `category: malformed_evidence`.**
The server's evidence-shape guard rejected your submission before sending it to the reviewer. The `evidence` field names the specific pattern that matched — typically a truncation marker like `(truncated)`, `[truncated]`, `// ... unchanged`, or a placeholder line consisting only of `...`, or empty `Path` entries in `final_files`. Re-submit with full file contents in `final_files` or a complete unified diff in `final_diff`. The rejection is cached for 5 minutes by canonical content hash, so identical re-submissions are short-circuited.
```

- [ ] **Step 5: Sanity-check CHANGELOG version matches branch name**

Run: `git branch --show-current`
Expected: `version/0.3.1`.

Run: `grep -n '## \[0.3.1\]' CHANGELOG.md`
Expected: one match at the top of the recent-changes section.

- [ ] **Step 6: Run the full test suite (sanity)**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add CHANGELOG.md README.md INTEGRATION.md
git commit -m "docs: CHANGELOG / README / INTEGRATION troubleshooting for 0.3.1

Final documentation lands the ## [0.3.1] - 2026-05-13 CHANGELOG block
(Added: summary_block, plan_quality, unverifiable_codebase_claim,
malformed_evidence categories, lightweight dispatch example; Changed:
check_progress demote, evidence-shape cache, lightweight session_id
allowance, summary_block marshaller placement; Documentation: scope-
and-limits and lightweight-mode sections in INTEGRATION.md).

Includes Removed/Deprecated/Security subsections with _None._ per
repo Keep a Changelog convention (0.3.0 CR finding).

README gains a paragraph pointer to the lightweight-mode capability.
INTEGRATION.md gains a plan_quality paragraph in the validate_plan
section and a malformed_evidence troubleshooting entry near
payload_too_large.

Closes #12."
```

- [ ] **Step 8: Sanity-check the final branch state**

Run: `git log --oneline version/0.3.1 ^main`
Expected: 7 commits (Tasks 1-7) plus the 4 design-spec commits = 11 commits since the merge of 0.3.0.

Run: `go test -race ./...`
Expected: PASS.

Run: `goreleaser release --snapshot --clean --skip=publish` (if installed)
Expected: PASS — local release artifacts build successfully (validates the binary still ships).

---

## Self-review (controller, post-write)

**Spec coverage (cross-reference spec §1-§6):**

- §1 Documentation (scope-and-limits, check_progress demote, lightweight mode): Task 1 ✓
- §2 evidence-shape guard: Task 6 ✓
- §3 summary_block: Task 5 (Envelope + PlanResult only; not verdict.Result) ✓
- §4 unverifiable_codebase_claim: Task 2 (constant + schema + parser severity floor + validCategory) + Task 4 (prompt edits in 4 templates) ✓
- §5 plan_quality: Task 3 (schema + ParsePlan + ParsePlanFindingsOnly + chunked-path threading + exported sanity helper) + Task 4 (prompt edits in 2 templates) + Task 5 (sanity helper called from planEnvelopeResult so synthetic results get sanitized) ✓
- §6 malformed_evidence: Task 2 (constant in verdict.go only; NOT in schema.json or validCategory — server-side only) + Task 6 (use site in the validate_completion guard) ✓

**Type/signature consistency:**

- `CategoryUnverifiableCodebaseClaim` and `CategoryMalformedEvidence`: defined in Task 2 Step 3, used consistently in Tasks 2 (parser) and 6 (use site).
- `PlanQualityRough`, `PlanQualityActionable`, `PlanQualityRigorous`: defined in Task 3 Step 3, used consistently in Task 3 (parser).
- `SummaryBlock` field name: used identically across Result, PlanResult, and the helpers `formatEnvelopeSummary` / `formatPlanSummary` in Task 5.
- `formatEnvelopeSummary(env Envelope) string` and `formatPlanSummary(pr verdict.PlanResult, modelUsed string, reviewMS int64) string`: signatures consistent between Task 5 Step 5 (definition) and Task 5 Step 6/7 (use sites).
- `evidenceCacheKey(args ValidateCompletionArgs) [32]byte`: signature consistent in Task 6.
- `malformedEvidenceEnvelope(sessionID, reason, modelUsed string) Envelope`: signature consistent in Task 6 Step 3 (definition) and Step 4 (use).

**Placeholder scan:** No "TBD", "TODO", or "fill in details" present. The handler-body section of Task 6 Step 4 has some hand-waving ("You will need to carefully thread this through the existing handler body") because the existing handler body is too large to lift verbatim and the implementer will need to make minor judgment calls about where each `if !lightweight` guard goes. The Acceptance Criteria pin the behavior precisely; the implementer can follow tests as the contract.

**No-placeholder violations to fix:** The `samplePlanText()` / `sampleSpec()` references in Task 4 Step 1 assume those helpers exist in the prompts_test.go file. Implementer should verify they do; if not, define them as small local helpers using the same patterns the existing 0.3.0 prompts_test.go uses (the spec mentions these helpers do exist from prior work).
