# Capturing CodeScene stats over time

CodeScene (the [codescene-oss MCP server](https://github.com/codescene-oss/codescene-mcp-server)) recomputes Code Health on each call but keeps **no history**, and anti-tangent's server never sees those calls (they run in the agent's MCP-client context). So the implementer hands the result over: it passes `analyze_change_set`'s output to `validate_completion` as the `codescene` argument, and the server appends one counts-only record per reported run to `codescene-events.jsonl` in `ANTI_TANGENT_STATS_DIR`. anti-tangent's Compactor aggregates that file into `rollup.json`.

This is active only when `ANTI_TANGENT_STATS_DIR` is set. Nothing has to be registered in the host.

## What is recorded, when

A record is written for a `validate_completion` call that reached the reviewer and whose `codescene` argument reports a run (`ran: true`, or raw `analyze_change_set` output, which the server reduces). A skip (`ran: false`) and a call with no `codescene` argument write nothing.

An implementer resends its CodeScene result on every `validate_completion` retry, and a retry is not a new run. Within one task session the server therefore writes a record only when the reported result differs from the one it last recorded for that session. A lightweight call (no `session_id`) has no session to remember that in, so each such call that reports a run writes a record.

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

- `tool` is `analyze_change_set`; a `codescene` argument that names any other tool is recorded as `other`, never by the name it sent.
- `quality_gate` ∈ `passed | failed` (the tool's `quality_gates`).
- `verdicts` = per-file `verdict` tally.
- `net_pp` = Σ(finding `new-pp` − `old-pp`) across all findings; positive = more problem points after = worse.
- `trend` = sign of `net_pp`: `>0 → regression`, `<0 → improvement`, `0 → neutral`.
- `category_counts` = count of findings per CodeScene category (e.g. "Complex Method", "Bumpy Road Ahead"). The keys are the category names the caller sent, of which the server keeps the 20 largest. In this file a key is kept only when it reads as a plain category name — up to 40 characters, starting with a letter, of letters, spaces, commas, apostrophes and hyphens; any other key (one carrying a path, a digit or a colon, say) is counted as `other`.
- **No file paths, no code, no function names, no session id** — privacy parity with anti-tangent's own `events.jsonl`. The argument's free-text fields (`skip_reason`, `skip_evidence`, `base_ref`) are never written to this file.

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

**Note on zero-valued fields.** The record's fields (`quality_gate`, `files_analyzed`, `verdicts`, `trend`, `net_pp`, `category_counts`) are all `omitempty` in Go, so a genuinely-neutral record (e.g. `net_pp: 0`, no findings) is missing keys that the example above shows. The zero value and "key absent" decode to the same thing and no consumer reads the raw file directly, so don't read a missing key as "field never recorded."

## The PostToolUse hook is retired

Earlier versions had no server-side writer: a Claude Code `PostToolUse` hook, `examples/hooks/codescene-log.sh`, appended the record on every `analyze_change_set` call. The server writes it now, so with the hook still registered each run would be counted twice.

**If you registered that hook, remove its entry** — the `PostToolUse` block in `~/.claude/settings.json` whose matcher is `mcp__codescene__analyze_change_set`. The script still ships so that a settings file pointing at it does not fail, but it writes nothing and exits 0.

Two differences from the hook's records are worth knowing when reading a trend across the change:

- The hook recorded every `analyze_change_set` call, including ones made outside a task. The server records only a run that was reported to `validate_completion`.
- A run reported by several retries of one task is one record, not one per call.

## Per-task attribution: `plan-runs.jsonl`

The same `codescene` argument attributes the result to its task exactly, and surfaces it in `plan_run_report`'s per-task table.

Set `ANTI_TANGENT_PLAN_LEDGER=1` (with `ANTI_TANGENT_STATS_DIR`) to persist one line per
completed task to `plan-runs.jsonl`, so `plan_run_report` survives a server restart — the
in-memory plan-run store is otherwise lost like every other session state. The ledger also holds
a header line per run minted by `validate_plan` — run id, verdict, quality, task count, creation
time and the plan's task headings — pruned by its creation time, so a run no task ever attached to
is still known after a restart. A task run without a plan writes a session-title line instead,
carrying the salted run hash and the task title (never the session id), pruned by its `written_at`
time; gnome-topbar uses it to name session runs.

**Privacy: this file is different from the others.** `events.jsonl` and
`codescene-events.jsonl` are deliberately content-free — no titles, no paths, no code.
`plan-runs.jsonl` carries **task titles**, and the caller's `skip_reason`, `skip_evidence` and
`base_ref` as sent. That is why it needs its own opt-in instead of
inheriting `ANTI_TANGENT_STATS_DIR`. `plan-runs.jsonl` **is** subject to
`ANTI_TANGENT_STATS_RETENTION_DAYS` pruning, on the same retention tick as the two files above:
a row is dropped once its task's completion time is older than the cutoff. A row written
without a completion timestamp is retained rather than treated as infinitely old.

One argument feeds both files: `codescene-events.jsonl`, which feeds the rollup's `codescene`
block, and `plan-runs.jsonl`, which feeds `plan_run_report`. They are different views of the same
run, not two counts of it.
