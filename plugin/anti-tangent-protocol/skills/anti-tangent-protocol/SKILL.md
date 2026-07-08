---
name: anti-tangent-protocol:anti-tangent-protocol
description: Use when you are about to implement, or dispatch a subagent to implement, a task that has a Goal / Acceptance-criteria header from an implementation plan. Loads the anti-tangent-mcp drift-protection protocol (validate_task_spec → check_progress → validate_completion; validate_plan for controllers).
---

# anti-tangent-protocol

Loads the full anti-tangent-mcp integration protocol on demand, so the
~10k-token `INTEGRATION.md` is not always resident in context.

## When this applies

Only when the current task carries a structured **Goal / Acceptance criteria /
(Non-goals) / (Context)** header from an implementation plan. For read-only
research, Q&A, ad-hoc edits, plan authoring, or code review, the protocol does
not apply — stop here.

## Step 1 — Read the protocol

`Read` the bundled protocol document (relative to this skill file):

    ../../INTEGRATION.md

It is the single source of truth: the plan-handoff gate (`validate_plan`), the
per-task lifecycle (`validate_task_spec` → `check_progress` →
`validate_completion`), the dispatch clauses, and the scope/limits.

## Step 2 — Follow the clause that fits your role

- **Implementer** (you will write the code for this task): follow the §4
  lifecycle — call `validate_task_spec` before editing, `validate_completion`
  before reporting DONE, and paste its `summary_block`.
- **Controller** (you dispatch subagents): run the §5.1 `validate_plan`
  handoff gate before dispatch, and paste the §4.2 clause into each
  implementer's prompt.

Anti-tangent is advisory — it never blocks. Treat `critical` / `major` findings
as blocking-or-explain per the protocol.
