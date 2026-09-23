# Lightweight dispatch clause (anti-tangent-mcp, v0.3.1+)

> Use this template for trivial tasks (doc-only edits, single-file mechanical relocations, dependency bumps). `validate_plan` may annotate a task with `lightweight_eligible: true` and `lightweight_reason`, but that annotation is advisory. Use the full dispatch clause from `docs/protocol/implementer.md` §4.2 for any task that produces production logic, has test-design choices, or involves ambiguous state transitions.

What lightweight mode skips:

- **Skip** `validate_task_spec` (the spec is fully prescriptive; no design choices for the reviewer to shape).
- **Skip** `check_progress` (already optional in full mode).
- **Skip** the CodeScene MCP companion calls (`pre_commit_code_health_safeguard`, `analyze_change_set`) — but only when `ANTI_TANGENT_CODESCENE` is unset. There's nothing meaningful for static analysis on a trivial doc edit, and `codescene` is optional on the `validate_completion` call.

**`ANTI_TANGENT_CODESCENE=required` overrides the bullet above.** The mode is an operator assertion that CodeScene is present on this host, so lightweight tasks must **run `analyze_change_set` and submit its result, exactly as any other task** — being lightweight is not a skip reason. `{"ran": false, "skip_reason": "…", "skip_evidence": "<the tool's own error text>"}` is for an attempted run that failed, never one not attempted; a skip carrying no `skip_evidence` draws a major, the same as omitting the argument.

## Drift-protection protocol (lightweight)

> Keep this heading verbatim when you adapt the clause. `anti-tangent-guard` reads it as the
> lightweight marker: without it, a close whose `validate_completion` ran with an empty
> `session_id` is blocked as a skipped `validate_task_spec`.

Before reporting DONE (REQUIRED). Call `validate_completion` with the fields below, and AT LEAST ONE of: `final_files` (full file contents), `final_diff` (a unified diff), or `test_evidence` (test command output). Copy the `summary_block` field from the response verbatim into your DONE report.

If the verdict is `fail` or contains `critical`/`major` findings, do not report DONE — fix the findings and re-validate.

## Task spec (pass these fields verbatim to validate_completion)

- `session_id`: pass an empty string `""`. Lightweight mode skips `validate_task_spec`, so there is no session_id to thread. The handler accepts the empty string when at least one piece of evidence is non-empty; it synthesizes a minimal task spec (Goal = summary; no ACs) for the reviewer.
- `summary`: <one-paragraph summary of what was implemented>
- `plan_run_id`: the controller's `plan_run_id`, when the task belongs to a plan run, with
  `task_index` (the task's 1-based position in the plan) or `task_title` (its heading), so
  `plan_run_report` counts the task. Without one of them the task is not recorded.
- `final_files`, `final_diff`, `test_evidence`: at least one must be non-empty
- `final_diff` / `final_diff_path`: generate the diff with git — `git diff <base>..HEAD > /abs/path/final.diff` — and pass `final_diff_path`. Within each `diff --git`- or `diff --cc`-headered section, a hunk that runs backwards or a hunk whose declared line count doesn't match its body is rejected as malformed evidence before the review. The check does not identify file boundaries, so do not concatenate two files' hunks under one header. A diff with no such header is not checked this way, so generate it with git rather than assembling it by hand.
- `context_paths`: optional. Absolute paths to related files the change does not touch — the package's
  existing helpers, say — so the reviewer can report a helper the diff re-implements. They are never evidence
  that the work is done. Same limits as `validate_task_spec`'s `context_paths`: under `ANTI_TANGENT_PLAN_ROOTS`
  when it is set, at most 50 files, within `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES` and
  `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES`.
- `controller_rulings`: a ruling your controller issued on a finding from an earlier review of this task, copied verbatim. Rulings apply without a session; `finding_responses` do not.
- `codescene`: required under `ANTI_TANGENT_CODESCENE=required` — pass the `analyze_change_set` result the same as any other task; see the CodeScene bullets above for the skip shape and what draws a major. Optional and may be omitted when `ANTI_TANGENT_CODESCENE` is unset.
