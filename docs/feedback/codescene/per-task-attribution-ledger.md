# CodeScene MCP feedback: per-task attribution ledger

## Summary

When a development branch contains multiple tasks or checkpoints, it helps to separate Code Health findings by the task that introduced or changed them. A lightweight attribution ledger would make it easier to distinguish inherited findings, earlier-task findings, and findings introduced by the current task.

## Reproduction Shape

1. Work on a branch with multiple task checkpoints or commits.
2. Run CodeScene MCP checks after each meaningful checkpoint.
3. Observe that the branch-level result contains findings across the full change set.
4. Try to determine which task first introduced each finding or which task changed its metric.

## Suggested Helper

Provide a helper or output mode that records a per-task attribution ledger with generic fields such as:

- Task label
- Commit/checkpoint
- File
- Finding name
- `value_before`
- `value`
- Threshold
- Threshold-crossing status

## Why This Helps

The ledger would support task-scoped review without hiding whole-branch risk. Reviewers could see whether a finding is inherited, introduced by the current task, worsened by the current task, or merely still present from an earlier checkpoint. That makes companion-tool feedback easier to paste into task completion reports and easier to triage before merge.
