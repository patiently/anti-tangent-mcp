# anti-tangent protocol — for implementers

Read [`core.md`](core.md) first. This part covers the per-task lifecycle, the dispatch clause,
lightweight mode, and the CodeScene companion. If your task block looks thin or ambiguous, the
format it should have had is in [`authoring.md`](authoring.md).

## 4. For implementers — the lifecycle protocol

> **Lightweight eligibility first.** Many tasks qualify for lightweight mode (skip `validate_task_spec` and `check_progress`; keep `validate_completion` as the sanity gate). See [Lightweight protocol mode](#lightweight-protocol-mode-v031) below for criteria and reference clause.

| Phase | Tool | Required? | When to call |
|---|---|---|---|
| Start | `validate_task_spec` | **Yes** | Once, before writing any code |
| During | `check_progress` | Optional (advisory; low-signal in field data) | When you suspect drift, a test that 'should' fail doesn't, or you've spent >5 min on behavior the spec leaves under-specified |
| End | `validate_completion` | **Yes** | Before reporting DONE |

One task = one session = one subagent. The `session_id` returned by `validate_task_spec` lives in the implementer's context for the lifetime of the task.

### 4.2 The implementer-prompt clause (paste this into every dispatch)

```markdown
## Drift-protection protocol (anti-tangent-mcp)

At task start and before DONE, you must use `validate_task_spec` and
`validate_completion`. Use `check_progress` only when you suspect drift.

**1. At the start (REQUIRED).** Before writing any code, call
`validate_task_spec` with the structured task fields below. Save the
returned `session_id` — you'll thread it through subsequent calls.
- Read the findings list. Treat `severity: critical` as blocking and
  `severity: major` as "address or explain." If the spec is too ambiguous
  to proceed, stop and ask the controller for clarification rather than
  guessing.

**2. During work (OPTIONAL).** Call `check_progress` ONLY if you suspect
you're drifting mid-task, OR a test that 'should' fail doesn't, OR
you've spent >5 min debugging behavior the spec leaves under-specified.
Per the 0.3.1 protocol revision this call is advisory — most tasks will
skip it. When you do call, pass: the session_id, a one-sentence
`working_on` summary, and the changed files.

**2b. CodeScene mid-task check (REQUIRED when codescene-mcp is
configured in your host).** Call `pre_commit_code_health_safeguard` after
meaningful code changes to catch Code Health regressions on uncommitted/staged files. This is
deterministic and fast (no LLM call) — complementary to the
LLM-based `check_progress` and higher-signal mid-task. State any
deliberate skip in your DONE report. If codescene-mcp is not
configured, skip this step silently.

**3. Before reporting DONE (REQUIRED).** Call `validate_completion` with
the session_id, your summary, **a complete `final_diff` (or full
`final_files`)**, and test evidence. A complete diff is a **precondition
of the first call**, not something to add after a rejection —
evidence-poor submissions fail most of the time and buy you a formatting
review instead of a code review.
**Copy the `summary_block` field from the response verbatim into your DONE report.**
If the verdict is `fail` or contains `critical`/`major` findings, do
not report DONE — fix the findings and re-validate. **Exception: when the
response carries `submission_defect_only: true`, every blocking finding is
about what you submitted, not about your code. Attach the missing evidence
and re-submit; no rework is implied.**
- Prefer paths over inline content: omit a `final_files` entry's `content` and the server reads
  its absolute `path`, and pass `final_diff_path` instead of `final_diff` (write it first with
  `git diff > "$PWD/.git/anti-tangent-change.diff"` — a repo-local scratch path, guaranteed
  writable and never tracked by git — then pass that same absolute path as `final_diff_path`).
  Truncation checks still apply to the resolved content — a file containing `// snip` or a bare
  `...` line is rejected exactly as inline evidence would be. If your host sets
  `ANTI_TANGENT_PLAN_ROOTS`, whatever scratch location you use must fall inside one of those
  roots or the server refuses the path — a bare `/tmp` path will NOT satisfy a roots list scoped
  to your project directories.

**3b. CodeScene pre-DONE check (REQUIRED when codescene-mcp is
configured in your host).** Call `analyze_change_set` for the full
branch-vs-base Code Health view, then pass the result to
`validate_completion` as the `codescene` argument:
`{"ran": true, "quality_gate": …, "verdicts": {…}, "trend": …, "net_pp": …, "category_counts": {…}}`.
If you deliberately skipped, pass `{"ran": false, "skip_reason": "…"}` instead.
The structured field supersedes the prose status line: it reaches the reviewer as
authoritative caller-attested context (no independent verification) and lands in the
plan-run report. If codescene-mcp
is not configured, omit the argument.

## Project knowledge (auto-attached by the controller)

The task brief above includes a "Project knowledge" section with excerpts
the controller pre-selected from the project KB. Read it before
`validate_task_spec` — it carries decisions, module invariants, and prior
context relevant to this task. Treat it as authoritative.

When calling `validate_task_spec`, also pass that same section verbatim as
`project_knowledge` so the reviewer has the same grounding you do. (Omit
this block if there is no KB attached.)

## Task spec (pass these fields verbatim to validate_task_spec)

- task_title:           <from the task block>
- goal:                 <from "Goal:">
- acceptance_criteria:  <from "Acceptance criteria:" bullets>
- non_goals:            <from "Non-goals:" bullets if present>
- context:              <from "Context:" if present>
- pinned_by:            <optional anchors for existing behavior>
- controller_verified_references: <optional references the controller already verified>
- plan_run_id:          <optional, v0.15.0+; from the controller's validate_plan>
- project_knowledge:    <optional, v0.6.0+; markdown excerpts the controller pre-selected from the KB>
- harness_shape_attestation: <optional structured input; see §3.8>
- phase:                <optional; "pre" (default) or "post" for post-hoc/session-recovery>
```

If a `severity: major` pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.

**Short variant** — for agents that already carry the full clause in their system prompt:

````markdown
## Drift protection

Use anti-tangent per the standard dispatch protocol. For this task:
- Call `validate_task_spec` before edits unless `lightweight_eligible: true` is set by the controller.
- Call `validate_completion` before DONE and paste its `summary_block`.
- If CodeScene MCP is configured, `pre_commit_code_health_safeguard` (mid-task) and `analyze_change_set` (pre-DONE) are required; pass the pre-DONE result to `validate_completion` as the `codescene` argument (or `{"ran": false, "skip_reason": "…"}`).
- If the response carries `submission_defect_only: true`, attach the missing evidence and re-submit — that is a submission defect, not a code defect.
- If any major pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.
- If a Project knowledge section is auto-attached, read it before validate_task_spec and pass it verbatim as project_knowledge.
````

**Language-scoping prose caveat.** Reviewers can surface `ambiguous_spec` findings around closure/scoping semantics (Kotlin `var` captured by a lambda, Python `nonlocal`, JS `let`/`const` in arrow bodies) when the prose AC reads ambiguously even though the verbatim code block in the plan is unambiguous. Trust the verbatim plan code; only deviate if the *tests* disagree with the prose. If you can't reconcile code and prose, ask the controller.

### Lightweight protocol mode (v0.3.1+)

For trivial tasks — doc-only edits, single-file mechanical relocations, dependency bumps — the full clause is overhead-heavy. Controllers may dispatch a **lightweight clause**: skip `validate_task_spec`, skip `check_progress`, keep `validate_completion` as the sanity gate (its handler accepts an empty `session_id` when at least one of `final_files` / `final_diff` / `test_evidence` is non-empty).

Use lightweight mode when ALL of: (a) ≤ 2 files or docs/config/data-only; (b) mechanical (no new logic, no test-design choices); (c) the spec includes literal text, exact diff, exact command, or exact insertion shape. `validate_plan`'s `lightweight_eligible` / `lightweight_reason` hints are advisory, not permission to skip judgment.

Use the full protocol for: new production logic, test-design choices, or ACs requiring observable invariants. Reference lightweight dispatch clause: `examples/lightweight-dispatch.md`.

**Lightweight mode and `ANTI_TANGENT_CODESCENE=required`.** Lightweight tasks skip the CodeScene MCP companion calls (`pre_commit_code_health_safeguard` / `analyze_change_set`) — there's nothing meaningful for static analysis on a trivial doc edit. That is independent of whether the `codescene` argument itself is required: under `required` mode the check is on the `validate_completion` call, not on whether the companion tools ran, so a lightweight task must still pass `{"ran": false, "skip_reason": "lightweight task"}` as the `codescene` argument. Omitting the argument entirely draws a major `codescene_not_run` finding just like it would on a full-protocol task.

### CodeScene MCP companion

CodeScene covers anti-tangent's text-only blind spot (see `## Scope and limits`): the open-source [CodeScene MCP server](https://github.com/codescene-oss/codescene-mcp-server) runs deterministic Code Health analysis over the actual files, complementing anti-tangent's LLM review of plan text.

**Tool-to-phase mapping.** When CodeScene MCP is configured, these calls are **required** (§4.2 steps 2b/3b):

- Mid-task: `pre_commit_code_health_safeguard` after meaningful changes (uncommitted/staged only; deterministic and fast).
- Before DONE: `analyze_change_set` for the full branch-vs-base view — see §4.2 step 3b for what to do with the result.
- Drill-down on a flagged issue: `code_health_review`.

Enforcement is prompt-level: the requirement to call these tools lives here and in §4.2, not in the server. Once you do call `validate_completion`, `ANTI_TANGENT_CODESCENE=required` can deterministically add a `codescene_not_run` / `codescene_skipped` finding server-side (see `core.md`) — but anti-tangent never *fails a verdict* on a CodeScene finding itself. If CodeScene MCP isn't configured, the companion calls above are skipped, as they are on lightweight-protocol tasks (doc-only / mechanical) — but the `codescene` argument to `validate_completion` is a separate requirement under `required` mode; see [Lightweight protocol mode](#lightweight-protocol-mode-v031) above for what a lightweight task must still submit.

**CodeScene stats:** CodeScene keeps no history — see [docs/team-setup/codescene-stats.md](https://github.com/patiently/anti-tangent-mcp/blob/main/docs/team-setup/codescene-stats.md) to log Code Health to `codescene-events.jsonl`.

### 4.3 How to address findings

**Address vs. push back.** Reviewer LLMs can be wrong. If a finding misreads the code, document the disagreement in the next call's `working_on` field — e.g. `working_on: "addressed all findings except F#3 which is incorrect because the helper does perform the length check, see handlers.go line 42"` — and re-validate. Don't silently ignore: the next reviewer call won't see your reasoning unless you write it.

**The retry loop.** Parse failures on the reviewer's response are handled inside the server (one retry with a JSON-only reminder); the implementer does not handle that.

**Session not found.** A `category: session_not_found` finding means the session expired (default TTL 4h) or was never created. Call `validate_task_spec` again to start a fresh session and continue with the new ID.
