# 0.2.1 validate_plan Reviewer Guardrails Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the three reviewer-ground-rule guardrails (epistemic boundary, constrained `unstated_assumption`, concrete-evidence rule) to the three `validate_plan` prompt templates so the reviewer no longer hallucinates findings about code symbols it cannot see.

**Architecture:** Prompt-only change. Inline copies of an identical ~150-word guardrail block in each of `internal/prompts/templates/plan.tmpl`, `internal/prompts/templates/plan_findings_only.tmpl`, and `internal/prompts/templates/plan_tasks_chunk.tmpl`, placed before the `## Plan under review` block. No code changes, no schema changes, no API changes.

**Tech Stack:** Go `text/template`, golden test fixtures in `internal/prompts/testdata/`, `testify`, `go test ./internal/prompts -update` for golden regeneration, `-race` always on.

---

## Drift-protection protocol notes

**IMPORTANT — SKIP `validate_plan` when executing this plan.** The reviewer-hallucination behavior is exactly what this work fixes; calling `validate_plan` against this plan would be circular and the findings would be unreliable on the very change that fixes the underlying behavior.

The per-task lifecycle hooks (`validate_task_spec` → `check_progress` → `validate_completion`) DO still apply for each implementing subagent on Task 1. Paste the dispatch clause from `~/.claude/anti-tangent.md` verbatim into the implementer prompt. Task 2 is verification-only with no AC-bearing implementation work, so the per-task hooks are not required for it.

---

## File Structure

- Modify: `internal/prompts/templates/plan.tmpl` — add `## Reviewer ground rules` block at top.
- Modify: `internal/prompts/templates/plan_findings_only.tmpl` — same block.
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl` — same block.
- Modify: `internal/prompts/testdata/plan.golden` — golden regen.
- Modify: `internal/prompts/testdata/plan_findings_only.golden` — golden regen.
- Modify: `internal/prompts/testdata/plan_tasks_chunk.golden` — golden regen.
- Modify: `internal/prompts/prompts_test.go` — three new anchor-assertion tests.
- Modify: `CHANGELOG.md` — add `## [0.2.1] - 2026-05-12` section.

---

### Task 1: Add Reviewer Ground Rules to validate_plan Templates

**Goal:** All three `plan_*.tmpl` templates render a `## Reviewer ground rules` section with the three guardrail paragraphs, three new anchor-assertion tests pass, golden files are regenerated cleanly, and the CHANGELOG carries a 0.2.1 entry — all in a single commit.

**Acceptance criteria:**
- `plan.tmpl`, `plan_findings_only.tmpl`, and `plan_tasks_chunk.tmpl` each contain a `## Reviewer ground rules` heading followed by three paragraphs. The anchor strings that must appear in the rendered output (and therefore in each template body) are:
  - `You have access ONLY to the plan markdown`
  - `` For `unstated_assumption` findings, only flag ``
  - `` Every finding's `evidence` field must quote or paraphrase ``
- The guardrail block is placed BEFORE the `## Plan under review` block in each template.
- `internal/prompts/prompts_test.go` contains three new tests (one per template) that render the template with representative inputs and assert all three anchor strings — plus the `## Reviewer ground rules` heading — are present.
- Golden files (`plan.golden`, `plan_findings_only.golden`, `plan_tasks_chunk.golden`) regenerated; diffs only include the new guardrail block (no incidental whitespace churn).
- `CHANGELOG.md` has a new `## [0.2.1] - 2026-05-12` section with a `### Changed` entry that closes #8.
- `go test -race ./internal/prompts ./internal/mcpsrv` passes.

**Non-goals:**
- Do not touch `pre.tmpl`, `mid.tmpl`, or `post.tmpl`. The hallucination pattern is specific to `validate_plan` and a fix to other templates is a separate decision.
- Do not extract guardrails into a shared partial template; inline copies across the three files are intentional per the spec.
- Do not modify the `Finding` schema, the `Envelope` shape, or any provider/handler logic.
- Do not call `validate_plan` against this plan — see "Drift-protection protocol notes" above.

**Context:** Per `docs/superpowers/specs/2026-05-12-validate-plan-guardrails-design.md` (committed in `a795995` on this branch), the three guardrail paragraphs are identical across the three templates. Placement BEFORE `## Plan under review` frames the reviewer's reading of the plan rather than retroactively asking it to suppress extractions it has already made. The third paragraph (concrete-evidence rule) is the heaviest hitter and explicitly allows pointing at expected-but-missing plan text so absent-AC findings still surface correctly.

**Files:**
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/prompts/templates/plan.tmpl`
- Modify: `internal/prompts/templates/plan_findings_only.tmpl`
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl`
- Modify: `internal/prompts/testdata/plan.golden`
- Modify: `internal/prompts/testdata/plan_findings_only.golden`
- Modify: `internal/prompts/testdata/plan_tasks_chunk.golden`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Read existing test patterns**

Open `internal/prompts/prompts_test.go` and look for the existing tests that exercise the three plan templates. Note:

- The render function names — likely `RenderPlan`, `RenderPlanFindingsOnly`, and `RenderPlanTasksChunk`, but verify against the file.
- The input struct names — likely `PlanInput` and `PlanTasksChunkInput`, but verify.
- Any shared sample helpers (e.g., `samplePlanText()`, `sampleChunkTasks()`); reuse rather than re-define.
- Existing import set (especially `require`, `assert`, and `planparser` if chunk tests reference it).

The test code in Step 2 uses the names above as a starting sketch; substitute the actual names from the file before saving.

- [ ] **Step 2: Write three failing anchor tests**

Add to `internal/prompts/prompts_test.go` (near the existing plan-render tests):

```go
const (
    anchorReviewerGroundRules    = "## Reviewer ground rules"
    anchorEpistemicBoundary      = "You have access ONLY to the plan markdown"
    anchorUnstatedAssumptionRule = "For `unstated_assumption` findings, only flag"
    anchorConcreteEvidenceRule   = "Every finding's `evidence` field must quote or paraphrase"
)

func TestRenderPlan_IncludesReviewerGroundRules(t *testing.T) {
    out, err := RenderPlan(PlanInput{PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
    require.NoError(t, err)
    assert.Contains(t, out.User, anchorReviewerGroundRules)
    assert.Contains(t, out.User, anchorEpistemicBoundary)
    assert.Contains(t, out.User, anchorUnstatedAssumptionRule)
    assert.Contains(t, out.User, anchorConcreteEvidenceRule)
}

func TestRenderPlanFindingsOnly_IncludesReviewerGroundRules(t *testing.T) {
    out, err := RenderPlanFindingsOnly(PlanInput{PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
    require.NoError(t, err)
    assert.Contains(t, out.User, anchorReviewerGroundRules)
    assert.Contains(t, out.User, anchorEpistemicBoundary)
    assert.Contains(t, out.User, anchorUnstatedAssumptionRule)
    assert.Contains(t, out.User, anchorConcreteEvidenceRule)
}

func TestRenderPlanTasksChunk_IncludesReviewerGroundRules(t *testing.T) {
    out, err := RenderPlanTasksChunk(PlanTasksChunkInput{
        PlanText:   "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n",
        ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
    })
    require.NoError(t, err)
    assert.Contains(t, out.User, anchorReviewerGroundRules)
    assert.Contains(t, out.User, anchorEpistemicBoundary)
    assert.Contains(t, out.User, anchorUnstatedAssumptionRule)
    assert.Contains(t, out.User, anchorConcreteEvidenceRule)
}
```

If the actual render function or input-struct names differ, adjust the calls. If `planparser` isn't already imported, add the import. If the file already uses constants for anchor strings elsewhere, place these new constants alongside them; otherwise put them just above the first new test.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/prompts -run 'TestRenderPlan.*_IncludesReviewerGroundRules'`

Expected: all three FAIL with assertion errors that the anchor strings (or the `## Reviewer ground rules` heading) are not contained in the rendered output.

- [ ] **Step 4: Add the guardrail block to `plan.tmpl`**

Open `internal/prompts/templates/plan.tmpl`. Insert the following block at the very top of the file, before the existing `## Plan under review` heading. The block must end with a single blank line before `## Plan under review`:

```text
## Reviewer ground rules

You have access ONLY to the plan markdown rendered below. You have NOT been given the codebase. Any function name, file path, variable, struct field, environment variable, or other symbol that appears in the plan is an identifier reference — you do not know its definition, signature, return type, or behavior. Treat such identifiers as black-box references.

For `unstated_assumption` findings, only flag assumption gaps that are visible in the plan text itself (e.g. an AC that says "fast" without a measurable target). Do NOT speculate about behavior of named code symbols you cannot see. If the plan says "update Foo.Bar to handle X", do NOT emit a finding about what Foo.Bar's current signature is or what error types it returns — you cannot know.

Every finding's `evidence` field must quote or paraphrase text that literally appears in the plan above, OR describe an expected piece of plan text that is absent (e.g. "the task block has no `Acceptance criteria:` section"). If your evidence cannot be tied to plan text — present or missing — do not emit the finding.

```

Do not modify any other line in the file.

- [ ] **Step 5: Add the SAME guardrail block to `plan_findings_only.tmpl`**

Open `internal/prompts/templates/plan_findings_only.tmpl`. Insert the EXACT same block from Step 4 at the top of the file, before `## Plan under review`. Copy-paste verbatim — the wording must be byte-identical across all three templates.

- [ ] **Step 6: Add the SAME guardrail block to `plan_tasks_chunk.tmpl`**

Open `internal/prompts/templates/plan_tasks_chunk.tmpl`. Insert the EXACT same block from Step 4 at the top of the file, before `## Plan under review`. Copy-paste verbatim — the wording must be byte-identical across all three templates. Do NOT touch the existing `{{range .ChunkTasks}}` block or the closing reminder about the `Task N:` prefix lower in the file.

- [ ] **Step 7: Run anchor tests to verify they pass**

Run: `go test ./internal/prompts -run 'TestRenderPlan.*_IncludesReviewerGroundRules'`

Expected: all three PASS.

The pre-existing golden tests (e.g., `TestRenderPlan_Golden`) will FAIL at this point because the templates have changed. That is expected; Step 8 regenerates the goldens.

- [ ] **Step 8: Regenerate goldens and review the diff**

Run: `go test ./internal/prompts -update`

Then inspect: `git diff -- internal/prompts/testdata/plan*.golden`

Expected: each of `plan.golden`, `plan_findings_only.golden`, and `plan_tasks_chunk.golden` gains exactly the new `## Reviewer ground rules` block at the top (plus a trailing blank line), then the existing content unchanged. No churn elsewhere (no trailing-whitespace edits, no line-ending changes, no reordering).

If the diff shows incidental churn:
- Check that the guardrail block in each template ends with a single blank line before `## Plan under review` (not zero, not two).
- Check that none of the templates introduced tab/space drift.
- Re-edit, then `-update` again.

- [ ] **Step 9: Run focused tests**

Run: `go test -race ./internal/prompts ./internal/mcpsrv`

Expected: PASS — anchor tests, golden tests, and any plan-prompt integration tests in `mcpsrv` all green.

- [ ] **Step 10: Add the 0.2.1 CHANGELOG entry**

Open `CHANGELOG.md`. Insert a new section at the top of the version blocks (between the preamble and the existing `## [0.2.0] - 2026-05-12` section). Use this exact body:

```markdown
## [0.2.1] - 2026-05-12

### Changed
- `validate_plan` prompt templates (`plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl`) now include a `## Reviewer ground rules` block that pins the reviewer's epistemic horizon to the plan text — no claims about behavior of code symbols the reviewer cannot see. `unstated_assumption` findings are constrained to assumption gaps visible in the plan itself, and every finding's `evidence` field must point at plan text (present or expected-but-absent). Closes [#8](https://github.com/patiently/anti-tangent-mcp/issues/8).
```

Verify with `rg -c '^## \[0\.2\.1\]' CHANGELOG.md` — expected: `1`.

- [ ] **Step 11: Run full test sweep**

Run: `go test -race ./... -count=1`

Expected: all packages PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/prompts/templates/plan.tmpl internal/prompts/templates/plan_findings_only.tmpl internal/prompts/templates/plan_tasks_chunk.tmpl internal/prompts/testdata/plan.golden internal/prompts/testdata/plan_findings_only.golden internal/prompts/testdata/plan_tasks_chunk.golden internal/prompts/prompts_test.go CHANGELOG.md
git commit -m "fix: add reviewer ground rules to validate_plan templates"
```

If one of the golden files does not exist on disk yet (i.e., the `-update` step CREATED it rather than modifying it), `git add` will pick it up regardless — but verify with `git status` after staging that all 8 files are listed.

---

### Task 2: Final Verification

**Goal:** Confirm the 0.2.1 implementation is coherent, race-safe, and scoped exactly to the spec's intent.

**Acceptance criteria:**
- `go test -race ./...` passes cleanly with `-count=1` (uncached).
- `git diff --stat main..HEAD` shows changes only in the 8 implementation files (3 templates + 3 goldens + `prompts_test.go` + `CHANGELOG.md`) plus the spec file `docs/superpowers/specs/2026-05-12-validate-plan-guardrails-design.md` (committed earlier in `a795995`). No other files.
- `CHANGELOG.md` has exactly one `## [0.2.1]` section.
- Golden diffs show exactly the new `## Reviewer ground rules` block in each of the three plan-template goldens — no incidental changes.

**Non-goals:**
- Do not push the branch or open a PR unless the user asks.
- Do not run e2e tests (`-tags=e2e`) unless API keys are configured AND the user explicitly requests it.
- Do not introduce additional commits unless verification exposes a real failure.

**Context:** This task is verification-only. If a check fails, return to Task 1's files and fix in place; re-run the full sweep; if the fix is substantive (more than a typo), commit as a follow-up. If verification passes without changes, no commit is created for this task.

**Files:** Verify repository-wide.

- [ ] **Step 1: Run full test suite with race detector**

Run: `go test -race ./... -count=1`

Expected: every package PASS.

- [ ] **Step 2: Confirm files-touched scope**

Run: `git diff --stat main..HEAD`

Expected files (in some order):
- `CHANGELOG.md`
- `docs/superpowers/specs/2026-05-12-validate-plan-guardrails-design.md`
- `internal/prompts/prompts_test.go`
- `internal/prompts/templates/plan.tmpl`
- `internal/prompts/templates/plan_findings_only.tmpl`
- `internal/prompts/templates/plan_tasks_chunk.tmpl`
- `internal/prompts/testdata/plan.golden`
- `internal/prompts/testdata/plan_findings_only.golden`
- `internal/prompts/testdata/plan_tasks_chunk.golden`

Plus this plan file (if the executing skill committed it on this branch — fine to include).

No other files.

- [ ] **Step 3: Verify CHANGELOG section count**

Run: `rg -c '^## \[0\.2\.1\]' CHANGELOG.md`

Expected: `1`.

- [ ] **Step 4: Review the golden diffs by eye**

Run: `git diff main..HEAD -- internal/prompts/testdata/plan*.golden`

Expected: each golden file gains the `## Reviewer ground rules` block (heading + three paragraphs + blank line) at the top, then the previously-existing content unchanged. No reordering, no trailing-whitespace edits, no other changes.

- [ ] **Step 5: Handle verification fixes**

If any of Steps 1-4 surfaces a problem (test failure, unexpected file in the diff, churn in goldens), go back to Task 1 and fix in place. Re-run `go test -race ./...` after each fix. Commit follow-ups only if the fix is substantive enough to warrant its own commit message; otherwise amend or fold into Task 1's commit at the implementer's discretion.

If verification passes without changes, do not create a commit for this task.

---

## Self-Review Notes

- **Spec coverage:** Task 1 implements all four spec components — template edits (Steps 4-6), golden regeneration (Step 8), anchor tests (Step 2), CHANGELOG entry (Step 10). Task 2 covers the spec's commit-shape and bump-rationale expectations via the scope check (Step 2).
- **Placeholder scan:** No TBD / TODO / fill-in placeholders. Every code-edit step includes verbatim text or commands.
- **Type consistency:** Anchor strings (constants and assertions in Step 2) are byte-identical to the strings emitted by the template edits (Steps 4-6) and to the acceptance-criteria bullet. Render-function names are flagged in Step 1 for the implementer to verify against the actual code; Steps 2-3 explicitly note the names may need to be adjusted.
- **Scope check:** One logical change, two tasks. Single commit per spec. No scope creep into `pre/mid/post` templates or schema changes.
