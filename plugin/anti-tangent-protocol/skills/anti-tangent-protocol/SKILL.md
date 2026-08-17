---
name: anti-tangent-protocol:anti-tangent-protocol
description: Use when you are about to implement, or dispatch a subagent to implement, a task that has a Goal / Acceptance-criteria header from an implementation plan — or when a multi-task plan run has just finished and you need to report on it. Loads the anti-tangent-mcp drift-protection protocol (validate_task_spec → check_progress → validate_completion; validate_plan and plan_run_report for controllers).
---

# anti-tangent-protocol

Loads the anti-tangent-mcp integration protocol on demand, sliced by role, so an agent reads
only the part that applies to it.

## When this applies

Either of:

- The current task carries a structured **Goal / Acceptance criteria / (Non-goals) /
  (Context)** header from an implementation plan.
- A multi-task plan run has just finished and you are reporting on it.
- You are drafting or revising task blocks for an implementation plan (plan authoring).

For read-only research, Q&A, ad-hoc edits, or code review, the protocol does not apply —
stop here.

Plan authors: read `authoring.md` in Step 1 below for the recommended task format, but do
NOT call the implementation lifecycle tools (`validate_task_spec`, `check_progress`,
`validate_completion`) — those are for implementers, not for drafting the plan.

## Step 1 — Read the protocol for your role

Paths are relative to this skill file.

Always read `../../protocol/core.md` — tool surface, scope and limits, when the protocol
applies, FAQ.

Then read the part matching what you are about to do:

- **Implementer** (you will write the code for this task): `../../protocol/implementer.md`
- **Controller** (you dispatch subagents, or you are running a plan end to end):
  `../../protocol/controller.md`
- **Plan author** (you are drafting or revising task blocks): `../../protocol/authoring.md`
- Additionally, **only if a project knowledge base is in play**:
  `../../protocol/project-knowledge.md`

Read more than one part when you hold more than one role. Do not read all of them by default —
the project-knowledge part in particular is controller-only, and an implementing subagent makes
no prime/extract calls.

## Step 2 — Follow the clause that fits your role

- **Implementer:** follow the §4 lifecycle — call `validate_task_spec` before editing,
  `validate_completion` before reporting DONE, and paste its `summary_block`. When CodeScene
  MCP is configured, pass the `analyze_change_set` result as the `codescene` argument.
- **Controller:** run the §5.1 `validate_plan` handoff gate before dispatch, capture the
  returned `plan_run_id`, and paste the §4.2 clause into each implementer's prompt. When the
  last task has reported DONE, call `plan_run_report` with that `plan_run_id` and surface the
  table to the user.
- **Plan author:** follow §3's task-block format so `validate_task_spec` inputs map cleanly.
  Plan authors do not call any of the lifecycle tools themselves — those run once the plan is
  handed off for implementation.

Anti-tangent is advisory — it never blocks. Treat `critical` / `major` findings as
blocking-or-explain per the protocol. A response carrying `submission_defect_only: true` is
telling you the submission was incomplete, not that the code is wrong: attach what is missing
and re-submit.
