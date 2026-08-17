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
the session_id, your summary, the final files, and any test evidence.
**Copy the `summary_block` field from the response verbatim into your DONE report** — it carries the full envelope formatted for paste; you do not need to re-extract JSON fields.
If the verdict is `fail` or contains `critical`/`major` findings, do
not report DONE — fix the findings and re-validate.

**3b. CodeScene pre-DONE check (REQUIRED when codescene-mcp is
configured in your host).** Call `analyze_change_set` for the full
branch-vs-base Code Health view. **Record a one-line CodeScene status
in your DONE report either way** — the change set's delta (surface any
regression alongside `summary_block` and consider iterating first), or
that you skipped and why.
Anti-tangent stays advisory; CodeScene is codebase-grounded signal the
text-only reviewer can't produce. If codescene-mcp is not configured,
skip silently; otherwise a missing status line reads as non-adoption.

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
- If CodeScene MCP is configured, `pre_commit_code_health_safeguard` (mid-task) and `analyze_change_set` (pre-DONE) are required; report the pre-DONE CodeScene status in DONE (delta, or skip + reason).
- If any major pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.
- If a Project knowledge section is auto-attached, read it before validate_task_spec and pass it verbatim as project_knowledge.
````

**Language-scoping prose caveat.** Reviewers can surface `ambiguous_spec` findings around closure/scoping semantics (Kotlin `var` captured by a lambda, Python `nonlocal`, JS `let`/`const` in arrow bodies) when the prose AC reads ambiguously even though the verbatim code block in the plan is unambiguous. Trust the verbatim plan code; only deviate if the *tests* disagree with the prose. If you can't reconcile code and prose, ask the controller.

### Lightweight protocol mode (v0.3.1+)

For trivial tasks — doc-only edits, single-file mechanical relocations, dependency bumps — the full clause is overhead-heavy. Controllers may dispatch a **lightweight clause**: skip `validate_task_spec`, skip `check_progress`, keep `validate_completion` as the sanity gate (its handler accepts an empty `session_id` when at least one of `final_files` / `final_diff` / `test_evidence` is non-empty).

Use lightweight mode when ALL of: (a) ≤ 2 files or docs/config/data-only; (b) mechanical (no new logic, no test-design choices); (c) the spec includes literal text, exact diff, exact command, or exact insertion shape. `validate_plan`'s `lightweight_eligible` / `lightweight_reason` hints are advisory, not permission to skip judgment.

Use the full protocol for: new production logic, test-design choices, or ACs requiring observable invariants. Reference lightweight dispatch clause: `examples/lightweight-dispatch.md`.

### CodeScene MCP companion

CodeScene covers anti-tangent's text-only blind spot (see `## Scope and limits`): the open-source [CodeScene MCP server](https://github.com/codescene-oss/codescene-mcp-server) runs deterministic Code Health analysis over the actual files, complementing anti-tangent's LLM review of plan text.

**Tool-to-phase mapping.** When CodeScene MCP is configured, these calls are **required** (§4.2 steps 2b/3b); record the pre-DONE CodeScene status in the DONE report (delta, or skip + reason):

- Mid-task: `pre_commit_code_health_safeguard` after meaningful changes (uncommitted/staged only; deterministic and fast).
- Before DONE (with `validate_completion`): `analyze_change_set` for the full branch-vs-base view.
- Drill-down on a flagged issue: `code_health_review`.

The requirement is prompt-level — anti-tangent never enforces CodeScene findings server-side, staying advisory. If CodeScene MCP isn't configured, the companion calls are skipped, as are all CodeScene calls on lightweight-protocol tasks (doc-only / mechanical).

**CodeScene stats:** CodeScene keeps no history — see [docs/team-setup/codescene-stats.md](docs/team-setup/codescene-stats.md) to log Code Health to `codescene-events.jsonl`.

### 4.3 How to address findings

**Address vs. push back.** Reviewer LLMs can be wrong. If a finding misreads the code, document the disagreement in the next call's `working_on` field — e.g. `working_on: "addressed all findings except F#3 which is incorrect because the helper does perform the length check, see handlers.go line 42"` — and re-validate. Don't silently ignore: the next reviewer call won't see your reasoning unless you write it.

**The retry loop.** Parse failures on the reviewer's response are handled inside the server (one retry with a JSON-only reminder); the implementer does not handle that.

**Session not found.** A `category: session_not_found` finding means the session expired (default TTL 4h) or was never created. Call `validate_task_spec` again to start a fresh session and continue with the new ID.
