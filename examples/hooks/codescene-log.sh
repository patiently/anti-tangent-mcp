#!/usr/bin/env bash
# codescene-log.sh — Claude Code PostToolUse hook for mcp__codescene__analyze_change_set.
# Appends one counts-only record per run to $ANTI_TANGENT_STATS_DIR/codescene-events.jsonl.
# Fire-and-forget: always exit 0; silent skip when stats are off or the payload is unusable.
# Record matches internal/stats CodesceneEvent (verdicts / quality-gate / problem-points).
set -uo pipefail

dir="${ANTI_TANGENT_STATS_DIR:-}"
[ -n "$dir" ] || exit 0
command -v jq >/dev/null 2>&1 || exit 0

input=$(cat)

# tool_response is a content-block array [{type:"text", text:"<json>"}] (observed);
# also handle a bare JSON string or a {content:[{text}]} wrapper defensively.
resp=$(printf '%s' "$input" | jq -r '
  .tool_response
  | if type=="array" then (.[0].text // "")
    elif type=="string" then .
    elif type=="object" and ((.content?|type)=="array") then (.content[0].text // "")
    else "" end' 2>/dev/null)
[ -n "$resp" ] || exit 0

record=$(printf '%s' "$resp" | jq -c --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '
  select((.results?|type)=="array" and (.results|length) > 0) |
  ([.results[].findings[]? | ((.["new-pp"] // 0) - (.["old-pp"] // 0))] | add // 0) as $np |
  {
    ts: $ts,
    tool: "analyze_change_set",
    quality_gate: (.quality_gates // "unknown"),
    files_analyzed: (.results | length),
    verdicts: {
      improved: ([.results[] | select(.verdict=="improved")] | length),
      degraded: ([.results[] | select(.verdict=="degraded")] | length),
      stable:   ([.results[] | select(.verdict=="stable")]   | length)
    },
    trend: (if $np > 0 then "regression" elif $np < 0 then "improvement" else "neutral" end),
    net_pp: $np,
    category_counts: ([.results[].findings[]?.category] | group_by(.) | map({key: .[0], value: length}) | from_entries)
  }' 2>/dev/null)
[ -n "$record" ] || exit 0

mkdir -p "$dir"
printf '%s\n' "$record" >> "$dir/codescene-events.jsonl"
exit 0
