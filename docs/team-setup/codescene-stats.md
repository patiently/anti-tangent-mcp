# Capturing CodeScene stats over time

CodeScene (the [codescene-oss MCP server](https://github.com/codescene-oss/codescene-mcp-server)) recomputes Code Health on each call but keeps **no history**, and anti-tangent's server never sees those calls (they run in the agent's MCP-client context). So to accumulate a Code Health trend, the **agent** appends one counts-only record per run to a file in `ANTI_TANGENT_STATS_DIR`, and anti-tangent's Compactor aggregates it into `rollup.json`.

This is active only when `ANTI_TANGENT_STATS_DIR` is set **and** CodeScene is configured in the host.

## What to append, when

Once per task, after the pre-DONE `analyze_change_set` call (the branch-vs-base Code Health delta — the meaningful per-task metric), append one JSON line to `$ANTI_TANGENT_STATS_DIR/codescene-events.jsonl`. In the subagent/controller model, the controller appends from the implementer's DONE report; in the inline model, the single agent appends when it runs `analyze_change_set`. Mid-task `pre_commit_code_health_safeguard` and `code_health_review` drill-downs are **not** recorded.

## Record shape (counts + metadata only)

```json
{
  "ts": "2026-06-02T14:03:00Z",
  "tool": "analyze_change_set",
  "score_before": 8.7,
  "score_after": 8.4,
  "delta": -0.3,
  "trend": "regression",
  "files_analyzed": 5,
  "category_counts": {"complex-method": 2, "bumpy-road": 1}
}
```

- `trend` ∈ `regression | improvement | neutral` (the sign of `delta`).
- **No file paths, no code, no raw session id** — privacy parity with anti-tangent's own `events.jsonl`.
- Best-effort: any field CodeScene doesn't return may be omitted.

## How it surfaces

During compaction, anti-tangent reads `codescene-events.jsonl`, aggregates the current window, and writes a nested `codescene` block into `rollup.json`:

```json
"codescene": {
  "runs": 12, "latest_score": 8.4, "latest_delta": -0.3,
  "latest_trend": "regression", "score_p50": 8.6,
  "regressions": 3, "improvements": 7, "neutral": 2,
  "category_histogram": {"complex-method": 5, "bumpy-road": 2},
  "window_start": "...Z", "window_end": "...Z"
}
```

Consumers read `rollup.json` and look for the optional `codescene` block — **absence means "no CodeScene data this window," not an error.** The raw `codescene-events.jsonl` is retention-pruned by `ANTI_TANGENT_STATS_RETENTION_DAYS` alongside `events.jsonl`.
