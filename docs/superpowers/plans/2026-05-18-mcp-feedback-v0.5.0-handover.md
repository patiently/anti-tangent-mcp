# v0.5.0 Implementation Handover

## What to do in the fresh session

Paste this prompt verbatim into a fresh Claude Code session in this repo:

> Execute the v0.5.0 implementation plan at
> `docs/superpowers/plans/2026-05-18-mcp-feedback-v0.5.0-implementation.md`
> using the superpowers:subagent-driven-development skill. The branch is
> already `version/0.5.0`; the plan is committed. Read this file first:
> `docs/superpowers/plans/2026-05-18-mcp-feedback-v0.5.0-handover.md` —
> it carries critical execution carve-outs that the plan's "Execution
> notes" section summarizes but the subagent dispatcher must respect.

The fresh session takes it from there.

## Branch state at handover

- Branch: `version/0.5.0` (not yet merged to `main`)
- HEAD: `fa9f06c docs(plan): add v0.5.0 implementation plan`
- Working tree: clean
- Prior commits on the branch: spec authoring + three rounds of spec review
- `CHANGELOG.md` does NOT yet have a `## [0.5.0]` heading — Task 1 adds it
- `INTEGRATION.md` and the four reviewer-output schemas are pre-0.5.0 state — every later task that needs them brings them current

## Authoritative artifacts

- **Spec (source of truth for behavior):** `docs/superpowers/specs/2026-05-18-mcp-feedback-v0.5.0-design.md`
- **Plan (source of truth for execution):** `docs/superpowers/plans/2026-05-18-mcp-feedback-v0.5.0-implementation.md`
- **Project CLAUDE.md (conventions):** `CLAUDE.md` — branch/version conventions, CHANGELOG handling, testing conventions
- **User-global anti-tangent protocol:** `~/.claude/anti-tangent.md` — dispatch-clause source. Patrick's session will already load this.

If the controller and a subagent disagree, the plan wins. If the plan and the spec disagree, the spec wins — surface the discrepancy to the user, do not silently re-interpret.

## Critical execution carve-outs (lifted from the plan)

These are easy to miss because the dispatch protocol *normally* recommends calling all anti-tangent hooks on every task. This plan modifies the very surfaces those hooks depend on:

1. **Do NOT call `validate_plan` on this plan.** The plan edits `plan.tmpl`, `plan_tasks_chunk.tmpl`, `plan_schema.json`, `tasks_only_schema.json`, the `PlanTaskResult` struct, and the `planparser` package — exactly what `validate_plan` reads and writes. See the user-memory `skip-validate-plan-when-fixing-it.md`.

2. **Per-task hooks (`validate_task_spec`, `validate_completion`) DO still apply**, with four task-specific carve-outs the controller must respect:
   - **Task 1** modifies the reviewer-output schemas and the parser's `validCategory` allowlist. After Task 1 lands, subsequent `validate_task_spec` calls work normally.
   - **Task 3** edits `pre.tmpl` itself (the template `validate_task_spec` uses). Validate Task 3 *before* editing the template; treat any post-edit re-run as smoke-checking golden-test rendering rather than reviewer-finding signal.
   - **Task 6** edits `plan.tmpl` / `plan_tasks_chunk.tmpl` — only consumed by `validate_plan`, which is already skipped. No carve-out needed beyond #1.
   - **Task 7** edits `post.tmpl` itself (the template `validate_completion` uses). Same caveat as Task 3 — validate before editing the template.

3. **`check_progress` is OPTIONAL per the v0.3.1 protocol.** Default to skipping; call only if the implementer subagent suspects drift mid-task.

4. **CodeScene companion hooks are OPTIONAL.** If the executor's host has `codescene-mcp` configured, the dispatch clause's Steps 2b / 3b activate. If not, skip silently. Anti-tangent's protocol is unchanged either way.

## What the controller does between tasks

Per superpowers:subagent-driven-development:

- Dispatch one subagent per task. The subagent's prompt must include the structured Goal / Acceptance criteria / Non-goals / Context block from the plan task (copy verbatim) plus the dispatch clause that wires anti-tangent into the subagent.
- After the subagent reports DONE with its `validate_completion` `summary_block`, the controller reviews the diff, runs `go test -race ./...`, and decides whether to proceed to the next task or iterate.
- The controller does NOT re-call `validate_completion` itself — the subagent's call is the gate (per the user-global anti-tangent.md).

## Branch hygiene

- Commit per task as the plan instructs. Do not batch.
- Do NOT `git push` or merge to `main` from this execution. Stop after Task 8's commit.
- Do not amend committed work; create new commits if a step needs to be revisited.
- CHANGELOG enforcement: CI requires `## [0.5.0] - YYYY-MM-DD` to match the branch name. Task 1 adds the heading with today's actual date; if today's date drifts during multi-day execution, leave the original date — CI checks heading shape, not freshness.

## When to stop and ask the user

- Any task whose `validate_task_spec` envelope returns `critical` findings or unresolved `major` findings the implementer cannot defensibly mitigate.
- Any unexpected git state (uncommitted work that didn't come from this plan, an existing 0.5.0 changelog entry from a prior partial run, etc.) — investigate before overwriting.
- Any test that goes from green to red and the cause is not obviously a transient test-environment issue.
- Any divergence between what a task expects to find in a file and what's actually there (e.g., line numbers shifted because of an earlier task — that's normal and adapt; but file content shape diverging from what the plan describes is worth checking).

## Resuming a partially-executed plan

If a prior execution attempt landed some tasks already, the new controller can resume by:

1. Reading `git log --oneline` since `b8a904f` (the spec's last commit) to see which tasks landed.
2. Cross-referencing committed work against the plan's Tasks 1–8 — each task ends in one commit with a clear `feat(...)` or `docs(...)` subject.
3. Starting subagent dispatch at the first task whose commit is not yet present.

Plan tasks are independent within their AC bounds — re-running an already-landed task is a no-op only if you've inspected the diff first. If unsure, ask the user.
