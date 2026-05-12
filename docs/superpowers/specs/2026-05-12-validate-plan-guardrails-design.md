# validate_plan reviewer guardrails — design

**Status:** approved 2026-05-12
**Target version:** 0.2.1
**Tracking issue:** [#8](https://github.com/patiently/anti-tangent-mcp/issues/8)
**Branch:** `version/0.2.1`

## Background

`validate_plan` reviews a *plan markdown text*; the reviewer has no access to the codebase. In 0.2.0, the three plan-prompt templates (`plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl`) do not explicitly pin the reviewer's epistemic horizon to the plan text. When the plan mentions code identifiers in passing — function names, file paths, struct fields, env-var names — the reviewer extrapolates from its training-data prior on those identifiers and surfaces findings about their behavior. The findings are confabulations; the reviewer never saw the code.

The most acute failure mode is on the per-chunk path (`plan_tasks_chunk.tmpl`) because its `unstated_assumption` instruction is broad enough that any identifier becomes a candidate "unstated assumption" to fabricate a finding around. The single-shot (`plan.tmpl`) and Pass-1 findings (`plan_findings_only.tmpl`) paths show the same structural risk; they just trigger it less often because the cross-task framing gives the reviewer more textual evidence to ground against.

Issue [#8](https://github.com/patiently/anti-tangent-mcp/issues/8) captures the field-report shape.

## Scope

In scope:

- Add three guardrail paragraphs to `plan.tmpl`, `plan_findings_only.tmpl`, and `plan_tasks_chunk.tmpl`.
- Inline copy across the three templates (no shared partial — the codebase has no existing partial-template pattern, and three copies of ~150 words is cheaper to maintain than introducing new template-loading machinery for this change).
- Golden file regeneration plus one anchor-assertion test per template.

Out of scope (deferred):

- Per-task templates (`pre.tmpl`, `mid.tmpl`, `post.tmpl`). `mid.tmpl` and `post.tmpl` receive actual code or diffs, so the hallucination pattern is materially different there. `pre.tmpl` shares the no-code property with the plan templates but has not been observed misfiring; if it does in the wild, file a separate issue.
- Schema changes. `Finding.evidence` is already a free-text string; no envelope shape changes.
- Reviewer model swaps. This is a prompt-only change.

## Bump rationale

Patch (`0.2.0` → `0.2.1`). Prompt-only, additive guardrails; no schema, API, or behavioral break. Existing callers see the same envelope shape; the only observable change is that some previously-emitted findings will no longer surface — which is the desired effect.

## Design

### Placement

The guardrails render **before** the `## Plan under review` block, under a new `## Reviewer ground rules` heading. Placing the rules before plan ingestion frames the reviewer's reading of the plan, rather than retroactively asking it to suppress extractions it has already made. Final shape of each rendered prompt:

```text
## Reviewer ground rules

<three guardrail paragraphs>

## Plan under review

{{.PlanText}}

## What to evaluate

<existing instructions, unchanged>

## Output

<existing JSON schema directive, unchanged>
```

`plan_tasks_chunk.tmpl` keeps its `{{range .ChunkTasks}}…{{end}}` chunk-list block and its closing reminder about including the `Task N:` prefix unchanged; only the new `## Reviewer ground rules` block is added before `## Plan under review`.

### The three guardrail paragraphs (verbatim, identical across all three templates)

**Paragraph 1 — Epistemic boundary.**

> You have access ONLY to the plan markdown rendered below. You have NOT been given the codebase. Any function name, file path, variable, struct field, environment variable, or other symbol that appears in the plan is an identifier reference — you do not know its definition, signature, return type, or behavior. Treat such identifiers as black-box references.

**Paragraph 2 — Constrained `unstated_assumption` findings.**

> For `unstated_assumption` findings, only flag assumption gaps that are visible in the plan text itself (e.g. an AC that says "fast" without a measurable target). Do NOT speculate about behavior of named code symbols you cannot see. If the plan says "update Foo.Bar to handle X", do NOT emit a finding about what Foo.Bar's current signature is or what error types it returns — you cannot know.

**Paragraph 3 — Concrete-evidence rule.**

> Every finding's `evidence` field must quote or paraphrase text that literally appears in the plan above, OR describe an expected piece of plan text that is absent (e.g. "the task block has no `Acceptance criteria:` section"). If your evidence cannot be tied to plan text — present or missing — do not emit the finding.

The third paragraph is the heaviest hitter: it forces every emitted finding back to plan-text evidence (present or absent), which is the only thing the reviewer actually has.

### Tests

- **Golden regen** for the three templates via `go test ./internal/prompts -update`. Diff reviewed in PR.
- **One anchor-assertion test per template** (three new tests in `internal/prompts/prompts_test.go`):
  - Renders the template with a representative input.
  - Asserts each of the three anchor strings is present in the rendered output:
    - `"You have access ONLY to the plan markdown"`
    - `"For \`unstated_assumption\` findings, only flag"`
    - `"Every finding's \`evidence\` field must quote or paraphrase"`
- **No new E2E test.** Hallucination is a fuzzy reviewer-side behavior and a gated test would be flaky. The anchor-assertion tests plus golden-diff review at PR time provide the right level of coverage.

### Files touched

- Modify: `internal/prompts/templates/plan.tmpl`
- Modify: `internal/prompts/templates/plan_findings_only.tmpl`
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl`
- Modify: `internal/prompts/testdata/plan.golden` (if it exists; otherwise regen creates it)
- Modify: `internal/prompts/testdata/plan_findings_only.golden`
- Modify: `internal/prompts/testdata/plan_tasks_chunk.golden`
- Modify: `internal/prompts/prompts_test.go` — three new anchor tests
- Modify: `CHANGELOG.md` — add `## [0.2.1] - 2026-05-12` block

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Paragraph 3 (concrete-evidence rule) under-fires findings about absent-but-needed plan content | Paragraph 3 explicitly allows pointing at expected-but-missing text. PR-time review of the golden diff against a known-buggy sample plan confirms existing legitimate findings still surface. |
| Three inline copies of ~150 words drift apart over future edits | Tolerable for a rarely-edited guardrail block. If the cost ever rises, extract to a partial in a later release. |
| Reviewer ignores the new section because LLMs sometimes weight later instructions more heavily than earlier ones | Placing the rules under a numbered/explicit heading and using "ONLY" / "do NOT" / "MUST" cue tokens reduces the risk. If hallucination still appears in the wild after this lands, re-evaluate with the rules placed AFTER `## Plan under review` instead. |

## Commit shape

Single commit on `version/0.2.1`: template edits + golden regen + new tests + CHANGELOG entry. The diff is small (~50 net lines across templates, ~30 lines of golden, ~50 lines of test). Splitting hurts review legibility for this size.

No bump tag is needed in the merge commit subject — patch is the default per the project release workflow (`[minor]` and `[major]` are the only tags that override).

## CHANGELOG entry (0.2.1)

```markdown
### Changed
- `validate_plan` prompt templates (`plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl`) now include a `## Reviewer ground rules` block that pins the reviewer's epistemic horizon to the plan text — no claims about behavior of code symbols the reviewer cannot see. `unstated_assumption` findings are constrained to assumption gaps visible in the plan itself, and every finding's `evidence` field must point at plan text (present or expected-but-absent). Closes [#8](https://github.com/patiently/anti-tangent-mcp/issues/8).
```
