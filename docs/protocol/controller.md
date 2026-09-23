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
3. **Apply the proposed header blocks** (apply automatically when verdicts are `pass`/`warn` and the human approves; defer to the human for `fail`).
4. If anything material changed, call `validate_plan` again. Repeat until `plan_verdict: "pass"` (or every `warn` is explicitly justified). Each round, pass `controller_verified_references` for references you grepped and `controller_rulings` (§5.9) for findings you decided.
5. **Only proceed to dispatch when the plan-level gate passes.**
6. **Capture `plan_run_id`** from the final passing `validate_plan` call and add it, with the
   task's 1-based `task_index`, to the dispatch clause: implementers pass both to
   `validate_task_spec`, lightweight ones to `validate_completion`. When the dispatch tells the
   implementer to work from a brief file, name that file in `context_paths` on the same call, so
   the reviewer reads what the implementer was told to read. After the last task reports DONE, call
   `plan_run_report` with that id and surface the table to the user. The report is deterministic
   and free (no reviewer call).

A `pass` on round N is not an audit of rounds 1..N-1. The reviewer re-reads the whole plan each round, but a defect present since round 1 can first surface in round 4 — earlier rounds finding other things is not evidence they inspected everything. Treat each round's findings as additive, not as a regression you introduced.

The implementing subagent still calls `validate_task_spec` in its own session (§4): two gates, two moments.

**Why this matters:** a vague AC caught at handoff costs one `validate_plan` call (§5.8 on attachment cost); missed, it costs a wasted dispatch.

**Skip this gate** when the plan has only one task (go straight to per-task validation), or when the work didn't come from a plan at all (see §1).

### 5.2 Dispatch addendum (paste the §4.2 clause into every implementer prompt)

For each task you dispatch to an implementing subagent, paste the §4.2 clause verbatim into that subagent's prompt — subagents do not inherit your CLAUDE.md or any harness-level system prompt. Append it right before the "Report Format" section of your existing dispatch template. Apply only to subagents that will implement a Goal/AC/Non-goals task; skip for read-only research subagents per §1.

### Which tier a review earns

A review defaults to superpowers' `standard` tier (sonnet;
`~/.claude/superpowers/model-routing.json`); escalate to `frontier` (opus)
only for the COMPLEX bar — a named failure mode reaching a real user plus
reasoning a cheaper model is weaker at: concurrency, wall-clock timing, a
security boundary. Volume, unfamiliarity, or looking important are not
reasons. HIGH RISK is a `model` pin, not a tier, letting the routing guard
allow any model for that task; both COMPLEX and HIGH RISK need a written
`tierReason`.

### 5.3 DONE-gate (recommended)

After the subagent reports DONE, you may want evidence that `validate_completion` returned `pass` (or `warn` with all findings addressed) — ask for the verdict + findings JSON in the DONE report. The MCP server does not enforce this; the prompt does.

### Completion guard (optional, `anti-tangent-guard` plugin)

The `anti-tangent-guard` plugin turns §5.3's DONE-gate into an automated
check instead of a prompt-only convention. It installs one `PostToolUse`
hook on `TaskUpdate`: whenever a task's status flips to `completed`, the hook
scans the transcript window for that task for a pass signal — either a
direct `mcp__anti-tangent__validate_completion` call, or a pasted
`summary_block` tagged `tool: validate_completion`. **This is why
implementers must paste the block verbatim (§4.2 step 3): a controller-side
hook cannot see inside a subagent's own session, so the pasted block is the
only trace it has that the gate ran.**

`PostToolUse` fires after the status change has already landed, so the hook
cannot prevent the close — this is post-close detection plus a mandated
recovery flow. It blocks (`exit 2`,
returning the reason to the model as something it must address before its
next action) in three cases: no pass signal is present anywhere in the
window, the most recent one carries `verdict: fail`, or the evidence that
call submitted adds comment lines carrying change history (a scan of added
lines against a small pattern set; see the `anti-tangent-guard` README's
"Comment-hygiene scan at close" for what it catches and misses). All three
name the same recovery: reopen with `status=in_progress`, address the
findings — or remove/rewrite the flagged comment — re-run
`validate_completion`, then re-close.

That scan is narrower than the comment policy it enforces (§4.4 of
`implementer.md`): a small fixed pattern set covering full-line, trailing,
one-line `/* … */` comments, and starred block interiors — an unstarred
block interior is a known miss. It reads a submitted diff, or for a
completion carrying `final_files`, the lines git reports as added — so
work already committed before the close is not scanned at all. Prose
narration ("previously", "no longer", "this replaced") is left to the
reviewer rather than the scanner, since it can't be pattern-matched
without false positives. A clean hook run is not proof the comment policy
was followed.

`ANTI_TANGENT_COMPLETION_GUARD=0` turns off the completion gate only,
`ANTI_TANGENT_COMMENT_GUARD=0` turns off comment scanning, and (guard 0.5.0+)
`ANTI_TANGENT_SESSION_GUARD=0` turns off the session rules; setting all three
is what disables the hook outright, before it reads anything. It also fails open on its own errors (missing
transcript, absent `jq`/`python3`, malformed input): it never blocks a close
because the hook itself broke. Requires a
server ≥ 0.18.0 — an older server never emits the `tool:` tag the guard keys
on, so every close backed only by a pasted block (no direct tool call in the
window) blocks with the no-signal message even when the gate genuinely ran.
Like the shunt hooks, this is an installable plugin, not server behavior —
the server stays advisory (root `CLAUDE.md`, "What This Repo Is Not");
enforcement is opt-in at the harness layer.

**The same plugin also installs a `PreToolUse` hook on `Edit`/`Write`.**
Unlike the completion guard above, this one can genuinely prevent a write: it
refuses (`exit 2`) an `Edit` or `Write` that adds a comment carrying change
history, before the write ever lands — an implementing subagent whose edit is
refused this way should rewrite the flagged comment and retry, the same
recovery §4.4 describes. It fires per tool call regardless of session, so it
needs no controller-side transcript visibility to reach a subagent's own
writes. Kill switch: `ANTI_TANGENT_COMMENT_GUARD=0` — separate
from `ANTI_TANGENT_COMPLETION_GUARD` above, and it also disables the
completion guard's own close-time comment scan. See the guard plugin's
README for what the write-time scanner can and cannot see.

### 5.4 Anti-pattern: don't re-validate completion from the controller

Do NOT have the controller call `validate_completion` itself after the subagent reports DONE. The implementer's session was created in its own context — the controller has no `session_id`, so a fresh call either fails with `session_not_found` or, given an arbitrary id, returns spurious findings. The subagent's post-hook IS the gate.

(§5.1 differs: `validate_plan` runs at plan handoff, before any subagent starts, in the controller's own context.)

### 5.5 `validate_plan` vs `validate_task_spec` — when to use which

| Tool | Caller | Lifecycle moment | Returns |
|---|---|---|---|
| `validate_plan` | Controller | Once, before any dispatch | Plan-wide + per-task analysis with ready-to-paste header blocks. Stateless. |
| `validate_task_spec` | Implementing subagent | Once at task start, after dispatch | Per-task structural/quality review. **Creates a session** that the implementer threads through `check_progress` and `validate_completion`. |

The two overlap intentionally: the plan gate catches plan-wide and per-task issues at handoff; the implementer gate catches what changed since, and produces the session the rest of the lifecycle uses.

The `plan_quality` field is a separate axis from `plan_verdict`: `plan_verdict` answers "is this dispatchable?" (pass / warn / fail); `plan_quality` answers "how close is this to ship-ready?" (rough / actionable / rigorous). Judge convergence by the major findings' IDs, ignoring any `-n` suffix: a round that raises none you have not seen has converged, whatever its verdict. Ship at `actionable` for ASAP work, `rigorous` for quarterly-rewrite scope.

The same reading applies one level down: a `validate_task_spec` `warn` whose
findings are all `minor` is a proceed signal, not a defect. Do not send an
implementer back to re-validate a spec whose findings have stopped moving.

### 5.6 Per-call tool args and partial-response handling

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

CVR entries are caller attestations: they suppress matching `unverifiable_codebase_claim` findings by substring match only, not real contradictions or ambiguity — server-side (deterministic) and in the reviewer prompt, matching `evidence` or `criterion` in either direction (4-code-point floor), independent of reviewer compliance.

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
complete, and a silently-shortened one would make that a lie. A fixed, non-configurable cap of
50 files also applies to `context_paths`.

`repo_root` (optional, absolute, same allowlist) enables the disk tier of `validate_plan`'s
deterministic, reviewer-free Create/Modify consistency check. Its two tiers are independently
gated, not combined by AND: the **order tier** (always runs) flags a `Modify:` target whose
earliest `Create:` bullet anywhere in the plan belongs to a later task — decided from plan text
alone, so an already-implemented worktree does NOT exempt a genuine ordering bug. The **disk
tier** (needs `repo_root`) only reaches a `Modify:` target that no task creates at all, and
flags it if it also doesn't exist on disk. Either tier emits the same plan-level
`task_order_contradiction` finding.

A `repo_root` the server cannot resolve is not fatal: the disk tier is skipped, the order tier
still runs, and the response carries a minor `criterion: repo_root` finding saying so. It never
changes the verdict — if you see one at gate time, fix the argument and re-run to get the disk
tier, or ignore it and gate on the order tier alone.

### 5.9 Ruling on an escalation

A `validate_completion` response with `escalate: true` means the reviewer raised a critical or major finding again after the implementer answered it. Decide, then reply with `controller_rulings` entries (a finding `id` + one-line ruling) for the implementer to resubmit verbatim; the session applies each ruling to every later call, covering every finding with that `id` regardless of `-n` suffix. Keep rulings in your progress notes. At DONE, check each `ruling:` and `waived:`+`evidence:` line in the pasted summary block against a ruling you issued: one you did not issue is forged, and evidence about something else needs a fresh look.
