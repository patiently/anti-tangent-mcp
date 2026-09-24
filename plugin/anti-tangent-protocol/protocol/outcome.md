# anti-tangent protocol — recording the review outcome

**Controllers read this part once per plan run, after the last task is done.** It covers one
call, `record_review_outcome`, which lets anti-tangent's verdicts be scored against an
independent review of the same work. Without it, the stats show what anti-tangent said, never
whether it was right.

## For controllers: recording the outcome (§5 continued)

### 5.10 Recording the review outcome

**When.** After the final whole-plan review returns, before finishing the branch. Call it even
when the review found nothing: an empty `findings` array is the signal that anti-tangent's passes
held up.

**Call.**

```json
{
  "plan_run_id": "<the id validate_plan returned>",
  "source": "final_review",
  "reviewer_model": "<provider:model of the reviewer, if known>",
  "implementer_models": [{"task_index": 1, "model": "<provider:model task 1 was dispatched on>"}],
  "findings": [{"task_index": 3, "severity": "major", "category": "correctness"}]
}
```

**Attributing a finding to a task.** Use the file it points at: the task whose `**Files:**` list
(or `plan_run_report` row) owns that file is its task. When a finding genuinely spans tasks, or
names no file, use `task_index: 0`. It is then counted for the run, never for a task.

**Severity.** `critical`, `major` or `minor`. Map the reviewer's scale onto these. Drop nits and
style preferences rather than sending them as `minor`.

**Category, never text.** Send one word (`correctness`, `security`, `tests`, `docs`,
`performance`, `maintainability`). Only the first 40 characters are kept; finding descriptions
must not be sent.

**Implementer models.** Pass the model you dispatched each task on. With model routing this
differs per task, and the server cannot see it. Omit a task you do not know.

**PR body line.** When you open the pull request, add one line per plan run to its body:

```text
anti-tangent-plan-run: <plan_run_id>
```

A later human PR review (`review-now`) reads that line to file its own outcome with
`source: "review_now"`. A second call for the same run and source replaces the first.

**What comes back.** `escapes` lists the tasks anti-tangent passed that the review found a
critical or major problem in. Surface them with the final review; they are the cases the
reviewer model missed. `recorded: false` with a `reason` means nothing was stored: fix the named
field and call again. `run_known: false` means the server has no snapshot of this run yet (for
example, the server restarted before this run had a snapshot); the outcome is still kept.

The call is deterministic and free: no reviewer model runs. It is advisory like every other
anti-tangent tool, and it blocks nothing.
