# Lightweight dispatch clause (anti-tangent-mcp, v0.3.1+)

> Use this template for trivial tasks (doc-only edits, single-file mechanical relocations, dependency bumps). For any task that produces new production logic or has test-design choices, use the full dispatch clause from `INTEGRATION.md` instead.

What lightweight mode skips:

- **Skip** `validate_task_spec` (the spec is fully prescriptive; no design choices for the reviewer to shape).
- **Skip** `check_progress` (already optional in full mode).
- **Skip** the CodeScene MCP companion calls (`pre_commit_code_health_safeguard`, `analyze_change_set`) — there's nothing meaningful for static analysis on a trivial doc edit.

## Drift-protection protocol (lightweight)

Before reporting DONE (REQUIRED). Call `validate_completion` with the fields below, and AT LEAST ONE of: `final_files` (full file contents), `final_diff` (a unified diff), or `test_evidence` (test command output). Copy the `summary_block` field from the response verbatim into your DONE report.

If the verdict is `fail` or contains `critical`/`major` findings, do not report DONE — fix the findings and re-validate.

## Task spec (pass these fields verbatim to validate_completion)

- `session_id`: pass an empty string `""`. Lightweight mode skips `validate_task_spec`, so there is no session_id to thread. The handler accepts the empty string when at least one piece of evidence is non-empty; it synthesizes a minimal task spec (Goal = summary; no ACs) for the reviewer.
- `summary`: <one-paragraph summary of what was implemented>
- `final_files`, `final_diff`, `test_evidence`: at least one must be non-empty
