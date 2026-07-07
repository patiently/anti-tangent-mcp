# Capturing CodeScene stats over time

CodeScene (the [codescene-oss MCP server](https://github.com/codescene-oss/codescene-mcp-server)) recomputes Code Health on each call but keeps **no history**, and anti-tangent's server never sees those calls (they run in the agent's MCP-client context). So to accumulate a CodeScene trend, a Claude Code **PostToolUse hook** appends one counts-only record per `analyze_change_set` run to a file in `ANTI_TANGENT_STATS_DIR`, and anti-tangent's Compactor aggregates it into `rollup.json`.

This is active only when `ANTI_TANGENT_STATS_DIR` is set **and** CodeScene is configured in the host.

## What is recorded, when

`analyze_change_set` is the branch-vs-base Code Health review — the meaningful per-task metric. The hook fires on **every** `analyze_change_set` call and appends one record; the mid-task `pre_commit_code_health_safeguard` and `code_health_review` are **not** matched, so they are not recorded. No dedup — one record per run.

`analyze_change_set` returns **categorical** output (per-file verdicts, a quality gate, and per-finding problem-points), **not** a numeric Code Health score — so the record is verdict/problem-point based, not score based.

## Record shape (counts + metadata only)

```json
{
  "ts": "2026-07-07T13:11:28Z",
  "tool": "analyze_change_set",
  "quality_gate": "failed",
  "files_analyzed": 2,
  "verdicts": {"improved": 0, "degraded": 2, "stable": 0},
  "trend": "regression",
  "net_pp": 2.0,
  "category_counts": {"Complex Method": 2, "Complex Conditional": 1}
}
```

- `quality_gate` ∈ `passed | failed` (the tool's `quality_gates`).
- `verdicts` = per-file `verdict` tally.
- `net_pp` = Σ(finding `new-pp` − `old-pp`) across all findings; positive = more problem points after = worse.
- `trend` = sign of `net_pp`: `>0 → regression`, `<0 → improvement`, `0 → neutral`.
- `category_counts` = count of findings per CodeScene category (e.g. "Complex Method", "Bumpy Road Ahead").
- **No file paths, no code, no function names, no session id** — privacy parity with anti-tangent's own `events.jsonl`.

## How it surfaces

During compaction, anti-tangent reads `codescene-events.jsonl`, aggregates the current window, and writes a nested `codescene` block into `rollup.json`:

```json
"codescene": {
  "runs": 12, "gates_passed": 7, "gates_failed": 5,
  "latest_gate": "failed", "latest_trend": "regression",
  "latest_net_pp": 2.0, "net_pp_p50": 0.5,
  "regressions": 5, "improvements": 6, "neutral": 1,
  "files_analyzed": 84,
  "category_histogram": {"Complex Method": 18, "Bumpy Road Ahead": 6},
  "window_start": "...Z", "window_end": "...Z"
}
```

Consumers read `rollup.json` and look for the optional `codescene` block — **absence means "no CodeScene data this window," not an error.** The raw `codescene-events.jsonl` is retention-pruned by `ANTI_TANGENT_STATS_RETENTION_DAYS` alongside `events.jsonl`.

## Enable it

The hook script ships in this repo at `examples/hooks/codescene-log.sh`. Register it as a Claude Code PostToolUse hook in `~/.claude/settings.json` (additive — keep any existing PostToolUse entries):

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "mcp__codescene__analyze_change_set",
        "hooks": [
          { "type": "command", "command": "/absolute/path/to/anti-tangent-mcp/examples/hooks/codescene-log.sh", "timeout": 10 }
        ]
      }
    ]
  }
}
```

The hook reads the PostToolUse stdin (`tool_response` is a content-block array `[{type:"text", text:"<json>"}]`), extracts the `analyze_change_set` result, and appends the record. It is fire-and-forget: it always exits 0 and silently skips when `ANTI_TANGENT_STATS_DIR` is unset or the payload is unusable.
