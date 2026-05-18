# CodeScene MCP feedback: foreground deltas on already-degraded files

## Summary

When `pre_commit_code_health_safeguard` reports findings for a file that was already over a threshold before the current edit, the most useful task-level signal is the metric delta. The output is easier to act on when `value_before` and `value` are foregrounded together, instead of requiring the caller to infer whether the current change made an already-degraded file worse.

## Reproduction Shape

1. Start with a file that already has a Code Health metric over a configured threshold.
2. Make a small edit in that file that either leaves the metric unchanged or improves it without moving it below the threshold.
3. Run `pre_commit_code_health_safeguard` on the uncommitted change.
4. Compare the reported `value_before` and `value` for each finding.

## Observed Friction

The top-level result can still require attention because the file remains over threshold, even when the current edit did not worsen the metric. In task review, that can blur the distinction between inherited degradation and degradation introduced by the current checkpoint.

## Suggested Improvement

For findings on already-degraded files, make the delta explicit in the primary message. For example, show `value_before`, `value`, and whether the metric improved, stayed unchanged, worsened, or crossed a threshold during the current change. This would let callers prioritize current-change regressions while still keeping inherited Code Health debt visible.
