# anti-tangent protocol — for controllers

Read [`core.md`](core.md) first. This part covers the plan-handoff gate, the dispatch addendum,
and end-of-run reporting. The clause you paste into each subagent lives in
[`implementer.md`](implementer.md) §4.2. If a project knowledge base is in play, also read
[`project-knowledge.md`](project-knowledge.md).

## 5. For controllers — plan-handoff gate + dispatch addendum

Controllers (superpowers' `subagent-driven-development`, hone-ai's equivalent, or a hand-rolled loop) have **two** responsibilities the implementer can't cover.

### 5.1 Plan-handoff gate (REQUIRED before any dispatch)

Before executing a multi-task plan — whether you implement it yourself or dispatch to
subagents — **call `validate_plan` once, passing `plan_path` with the absolute path to the
plan file**. The server reads it, so plan size costs you no output tokens and the reviewer is
guaranteed to see the same document your subagents will. Use `plan_text` only when the plan is
not on disk; it is deprecated and will be removed in 1.0.0.

**Procedure:**

1. Call `validate_plan`, passing `plan_path` when the plan is on disk, otherwise `plan_text`. Capture the `PlanResult`.
2. **Surface results to the user.** Show `plan_verdict`, plan-level findings, and per-task verdicts/findings. For any task whose `suggested_header_block` is non-empty, show the proposed header and ask the human to adopt or revise. If task results include `lightweight_eligible` / `lightweight_reason`, treat them as advisory hints.
3. **Apply the proposed header blocks** (the controller may apply automatically when verdicts are `pass`/`warn` and the human approves; defer to the human for `fail`).
4. If anything material changed, call `validate_plan` again. Repeat until `plan_verdict: "pass"` (or every `warn` is explicitly justified).
5. **Only proceed to dispatch when the plan-level gate passes.**
6. **Capture `plan_run_id`** from the passing `validate_plan` response and pass it as
   `plan_run_id` in each implementing subagent's `validate_task_spec` call — add it to the
   dispatch clause's task-spec field list. After the last task reports DONE, call
   `plan_run_report` with that id and surface the table to the user. The report is
   deterministic and free (no reviewer call). If you re-validate the plan after the 3-minute
   cache window expires, use the id from your **final** passing call.

The implementing subagent still calls `validate_task_spec` at task start in its own session — see §4. The plan-level gate and the per-task implementer gate are two different responsibilities at two different moments.

**Why this matters:** catching a vague AC at handoff costs one `validate_plan` call — cents without `context_paths`, up to roughly $1.31 per round with a large attached set (see §5.8) — versus a wasted dispatch after a subagent spent 10 minutes against a misread spec.

**Skip this gate** when the plan has only one task (go straight to per-task validation), or when the work didn't come from a plan at all (see §1).

### 5.2 Dispatch addendum (paste the §4.2 clause into every implementer prompt)

For each task you dispatch to an implementing subagent, paste the §4.2 clause verbatim into that subagent's prompt — subagents do not inherit your CLAUDE.md or any harness-level system prompt. Append it right before the "Report Format" section of your existing dispatch template. Apply only to subagents that will implement a Goal/AC/Non-goals task; skip for read-only research subagents per §1.

### 5.3 DONE-gate (recommended)

After the subagent reports DONE, you may want to require evidence that `validate_completion` was called and returned `pass` (or `warn` with all findings addressed). The simplest way: ask for the verdict + findings JSON in the subagent's DONE report. The MCP server does not enforce this; the prompt does.

### 5.4 Anti-pattern: don't re-validate completion from the controller

Do NOT have the controller call `validate_completion` itself after the subagent reports DONE. The implementer's session was created in its own context — the controller doesn't have the `session_id`, so a fresh `validate_completion` call from the controller would either fail with a `session_not_found` finding or, if the controller passed an arbitrary id, return spurious findings. The subagent's post-hook IS the gate.

(This is different from §5.1, which is `validate_plan` at plan-handoff time before any subagent has started — that's pre-implementation and lives in the controller's own context.)

### 5.5 `validate_plan` vs `validate_task_spec` — when to use which

| Tool | Caller | Lifecycle moment | Returns |
|---|---|---|---|
| `validate_plan` | Controller | Once, before any dispatch | Plan-wide + per-task analysis with ready-to-paste header blocks. Stateless. |
| `validate_task_spec` | Implementing subagent | Once at task start, after dispatch | Per-task structural/quality review. **Creates a session** that the implementer threads through `check_progress` and `validate_completion`. |

The two analyses overlap intentionally: the plan gate catches plan-wide and per-task issues at handoff; the implementer gate catches anything that changed between handoff and dispatch and produces the session that the rest of the lifecycle uses.

The `plan_quality` field (v0.3.1+) is a separate axis from `plan_verdict`: `plan_verdict` answers "is this dispatchable?" (pass / warn / fail); `plan_quality` answers "how close is this to ship-ready?" (rough / actionable / rigorous). When consecutive `warn` verdicts aren't changing, watch `plan_quality` for convergence — `actionable → rigorous` is meaningful even when the verdict stays `warn`. Ship at `actionable` for ASAP work, `rigorous` for quarterly-rewrite scope.

### 5.6 Per-call tool args and partial-response handling (v0.3.0+)

**`max_tokens_override`** (all six reviewer-calling tools — `plan_run_report` makes no reviewer call, so it takes no token budget): optional non-negative int. Replaces `PerTaskMaxTokens` / `PlanMaxTokens` for this call. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values are clamped and a `minor` finding appended. Negative values rejected with `max_tokens_override must be ≥ 0`.

**`mode`** (`validate_plan` only): optional `"quick"` or `"thorough"` (default `"thorough"`). `"quick"` surfaces only the most-severe findings (at most 3 per scope) and omits stylistic nits. Invalid values rejected with `mode must be "quick" or "thorough"`.

**`partial: true`** envelope field: when the reviewer's output was truncated at its `max_tokens` cap but at least one complete finding could be recovered, the response carries `"partial": true` and the synthetic truncation finding is `severity: minor`. `omitempty` — absent in the common case. If no complete finding survives, the envelope falls back to the legacy `severity: major` truncation marker with no `partial` field.

Passing `validate_plan` calls are cached for 3 minutes when the rendered prompt, model, mode, and token budget are identical. Cache hits return `review_ms: 0` and prefix `next_action` with `[cached <=3m]`.

### 5.7 Using review-context features

Use `pinned_by` when a terse AC is backed by existing tests, docs, commands, or static checks. Example shape:

```json
{
  "acceptance_criteria": ["Existing retry behavior remains unchanged."],
  "pinned_by": ["RetryHandlerTest.retries_transient_errors", "go test ./internal/retry -run RetryHandler", "docs/retry-contract.md"]
}
```

Use `phase: "post"` only to recover a task session after implementation already happened; normal execution still calls `validate_task_spec` before coding.

Use `controller_verified_references` when the controller has already grep-verified specific file paths, symbols, line anchors, commands, or adjacent patterns. Example: `controller_verified_references: ["cmd/import.go", "ParserOptions.Strict", "ParseFile"]`.

CVR entries are caller attestations: they suppress matching `unverifiable_codebase_claim` findings by substring match only, not real contradictions or ambiguity. Suppression runs server-side (deterministic) as well as in the reviewer prompt — a substring match against the finding's `evidence` or `criterion` (either direction; 4-code-point floor on CVR entries) suppresses the entire `unverifiable_codebase_claim`, independent of reviewer compliance.

### 5.8 Attaching source files: `context_paths` and `repo_root`

`validate_plan` accepts `context_paths` — a list of **absolute** paths to source files the plan
makes claims about. The server reads each one whole and renders it into the prompt ahead of the
plan (governed by the same `ANTI_TANGENT_PLAN_ROOTS` allowlist as `plan_path`). For attached
files the reviewer verifies claims directly instead of emitting `unverifiable_codebase_claim`,
and emits `contradicted_codebase_claim` (see [`core.md`](core.md)) when an attached file refutes
one.

**Opt-in and expensive — attach only the files the plan actually makes claims about.** Measured
on a real 9-task, 170KB plan: the 24 referenced paths that existed cost ~100K tokens of
attachments, about 2.2× the plan's own size, and turned a chunked round into 3 reviewer calls at
~147K input tokens each — roughly **$1.31 per round** at the default plan model's rate, against
cents per round with no attachments.

Oversized attachments are **refused, never truncated**: the reviewer is told attached files are
complete, and a silently-shortened one would make that a lie. `repo_root` (optional, absolute,
same allowlist) enables the disk tier of `validate_plan`'s deterministic, reviewer-free
Create/Modify consistency check — a `Modify:` target with no earlier `Create:` and nothing on
disk emits a plan-level `task_order_contradiction` finding; without `repo_root`, only the
plan-text order tier runs.
