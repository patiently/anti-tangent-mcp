#!/usr/bin/env bash
# Drives codescene-log.sh with the captured fixture and asserts the record + skip paths.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
out=$(mktemp -d)
export ANTI_TANGENT_STATS_DIR="$out"

# Happy path: real captured stdin → one record with all fields.
"$here/codescene-log.sh" < "$here/testdata/analyze_change_set.stdin.json"
[ -f "$out/codescene-events.jsonl" ] || { echo "FAIL: no record written"; exit 1; }
line=$(cat "$out/codescene-events.jsonl")
[ "$(printf '%s' "$line" | jq -r '.tool')" = "analyze_change_set" ] || { echo "FAIL tool"; exit 1; }
printf '%s' "$line" | jq -e '.ts and .quality_gate and (.files_analyzed|type=="number") and (.verdicts.degraded|type=="number") and .trend and (.net_pp|type=="number") and (.category_counts|type=="object")' >/dev/null || { echo "FAIL shape: $line"; exit 1; }
# Privacy: no file paths / function names / locations leaked into the record.
if printf '%s' "$line" | grep -qiE '"name"|"locations"|/|\.go|function'; then echo "FAIL privacy: $line"; exit 1; fi

# Skip: unset dir → no write.
rm -f "$out/codescene-events.jsonl"
( unset ANTI_TANGENT_STATS_DIR; "$here/codescene-log.sh" < "$here/testdata/analyze_change_set.stdin.json" )
[ -f "$out/codescene-events.jsonl" ] && { echo "FAIL: wrote despite unset dir"; exit 1; }

# Skip: empty results (array tool_response shape).
echo '{"tool_response":[{"type":"text","text":"{\"results\":[],\"quality_gates\":\"passed\"}"}]}' | "$here/codescene-log.sh"
[ -f "$out/codescene-events.jsonl" ] && { echo "FAIL: wrote on empty results"; exit 1; }

# Skip: non-JSON stdin and missing tool_response.
echo 'not json at all' | "$here/codescene-log.sh"
echo '{}' | "$here/codescene-log.sh"
[ -f "$out/codescene-events.jsonl" ] && { echo "FAIL: wrote on bad/missing input"; exit 1; }

echo OK
