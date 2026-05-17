# CodeScene MCP feedback: pasteable companion envelope

## Summary

Anti-tangent provides a paste-ready `summary_block` so implementers can include the full validation envelope in completion reports without hand-transcribing fields. A similar pasteable envelope from CodeScene MCP would make `analyze_change_set` results easier to report consistently alongside anti-tangent results.

## Reproduction Shape

1. Run CodeScene MCP tools such as `analyze_change_set` before reporting task completion.
2. Manually summarize the structured result into a DONE report.
3. Compare that hand-written summary with anti-tangent's paste-ready `summary_block`.
4. Observe that manual summarization can lead to inconsistent formatting, omitted fields, or harder-to-scan companion-tool evidence.

## Suggested Output Shape

Provide a compact, plain-text companion envelope that includes:

- Tool name and command, such as CodeScene MCP `analyze_change_set`
- Overall status
- Base and compared revisions or checkpoints, when available
- Finding counts by severity or category
- Changed files with Code Health regressions
- For each highlighted finding: file, finding name, `value_before`, `value`, threshold, and threshold-crossing status
- Recommended next action

## Why This Helps

A pasteable envelope reduces reporting friction and avoids lossy summaries. It also makes anti-tangent and CodeScene companion output easier to present together: anti-tangent can supply its `summary_block`, while CodeScene MCP can supply a similarly structured codebase-grounded companion block. This does not require anti-tangent to call CodeScene or change runtime behavior; it only improves how callers communicate companion-tool findings.
