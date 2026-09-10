# anti-tangent protocol — for implementers

Read [`core.md`](core.md) first. This part covers the per-task lifecycle, the dispatch clause,
lightweight mode, and the CodeScene companion. If your task block looks thin or ambiguous, the
format it should have had is in [`authoring.md`](authoring.md).

## 4. For implementers — the lifecycle protocol

> **Lightweight eligibility first.** Many tasks qualify for lightweight mode (skip `validate_task_spec` and `check_progress`; keep `validate_completion` as the sanity gate). See [Lightweight protocol mode](#lightweight-protocol-mode-v031) below for criteria and clause.

| Phase | Tool | Required? | When to call |
|---|---|---|---|
| Start | `validate_task_spec` | **Yes** | Once, before writing any code |
| During | `check_progress` | Optional (advisory; low-signal in field data) | When you suspect drift, a test that 'should' fail doesn't, or you've spent >5 min on behavior the spec leaves under-specified |
| End | `validate_completion` | **Yes** | Before reporting DONE |

One task = one session = one subagent. The `session_id` from `validate_task_spec` lives in the implementer's context for the task's lifetime.

### 4.2 The implementer-prompt clause (paste this into every dispatch)

```markdown
## Drift-protection protocol (anti-tangent-mcp)

At task start and before DONE, you must use `validate_task_spec` and
`validate_completion`. Use `check_progress` only when you suspect drift.

**1. At the start (REQUIRED).** Before writing any code, call
`validate_task_spec` with the structured task fields below. Save the
returned `session_id` — you'll thread it through subsequent calls.
- Read the findings list. Treat `severity: critical` as blocking and
  `severity: major` as "address or explain." If the spec is too ambiguous
  to proceed, stop and ask the controller for clarification rather than
  guessing.
- A `warn` carrying only `minor` findings is a legitimate place to proceed:
  iterate while any finding is `critical` or `major`, proceed once every
  remaining one is `severity: minor`. That is the stop signal. A `major`
  finding whose wording has stopped changing across rounds will not move by
  re-validating again — fix it, or accept it with the one-sentence
  mitigation below and proceed on that basis.

**2. During work (OPTIONAL).** Call `check_progress` ONLY if you suspect
you're drifting mid-task, OR a test that 'should' fail doesn't, OR
you've spent >5 min debugging behavior the spec leaves under-specified.
This call is advisory — most tasks skip it. When you do call, pass: the
session_id, a one-sentence `working_on` summary, and the changed files.

**2b. CodeScene mid-task check (REQUIRED when codescene-mcp is
configured in your host).** Call `pre_commit_code_health_safeguard` after
meaningful changes to catch Code Health regressions on
uncommitted/staged files — deterministic and fast (no LLM call),
complementary to the LLM-based `check_progress` and higher-signal
mid-task. State any deliberate skip in your DONE report. If codescene-mcp
is not configured, skip this step silently.

**3. Before reporting DONE (REQUIRED).** Call `validate_completion` with
the session_id, your summary, **a complete `final_diff` (or full
`final_files`)**, and test evidence. A complete diff is a **precondition
of the first call**, not something to add after a rejection —
evidence-poor submissions usually fail and buy you a formatting review
instead of a code review.
**Copy the `summary_block` field from the response verbatim into your DONE report.**
If the verdict is `fail` or contains `critical`/`major` findings, do
not report DONE — fix the findings and re-validate. **Exception: when the
response carries `submission_defect_only: true`, every blocking finding is
about what you submitted, not about your code. Attach the missing evidence
and re-submit; no rework is implied.**
- Prefer paths over inline content: omit a `final_files` entry's `content` and the server reads
  its absolute `path`, and pass `final_diff_path` instead of `final_diff`. Write the diff first:
  `git add -- <task paths> && git diff HEAD -- <task paths> > "$(git rev-parse --absolute-git-dir)/anti-tangent-change.diff"` —
  a repo-local scratch path, guaranteed writable and never tracked by git — then pass that
  absolute path as `final_diff_path`. Use `--absolute-git-dir`, not `--git-dir`: the latter prints
  a relative `.git` in a normal checkout, which fails the server's absolute-path check, and it is
  also the form that resolves in a worktree, where `.git` is a file and `"$PWD/.git/..."` dies
  with `Not a directory`. **That filename is fixed per git directory** — where another agent may
  write to the same one (parallel tasks, a shared worktree), use a task-unique name or the later
  write silently clobbers the earlier evidence. **Scope both the `git add` and the `git diff` to
  your task's own paths — never `git add -A` or bare `git diff HEAD`.** The whole diff goes to a
  third-party reviewer LLM, so an unscoped stage-and-diff discloses every non-ignored change in
  the worktree: unrelated tracked edits, scratch files, another task's half-finished work. The
  task's `**Files:**` list is that set — use it as the pathspec. A new file must fall inside the
  same pathspec (`git add -- <new-file-path>`, before the diff is generated) or be passed as its
  own `final_files[].path` entry — never stage the whole worktree to catch it. If the work is
  already committed, diff the same pathspec from the commit the task started at
  (`git diff <task-base-commit> -- <task paths>`), since `git diff HEAD` is then empty. An empty
  diff file is rejected with a structured error, not a silent pass, so a wrong recipe surfaces
  immediately. Truncation checks apply to the resolved content — a file containing `// snip` or a
  bare `...` line is rejected exactly as inline evidence would be. If your host sets
  `ANTI_TANGENT_PLAN_ROOTS`, the scratch location must fall inside one of those roots or the
  server refuses the path: a bare `/tmp` path will NOT satisfy a roots list scoped to your project
  directories, and neither will the worktree answer `<main-checkout>/.git/worktrees/<name>` when
  the roots list covers only the worktree directory.

**3b. CodeScene pre-DONE check (REQUIRED when codescene-mcp is
configured in your host).** Call `analyze_change_set` for the full
branch-vs-base Code Health view, then pass the result to
`validate_completion` as the `codescene` argument:
`{"ran": true, "quality_gate": …, "verdicts": {…}, "trend": …, "net_pp": …, "category_counts": {…}}`.
If the run was attempted and failed, pass `{"ran": false, "skip_reason": "…", "skip_evidence": "<the tool's own error text>"}` instead; omitting `skip_evidence` draws a major, like omitting the argument.
The structured field supersedes the prose status line: it reaches the reviewer as
caller-attested context (no independent verification) and lands in the plan-run
report. If codescene-mcp is not configured, omit the argument.

## Project knowledge (auto-attached by the controller)

The task brief above includes a "Project knowledge" section with excerpts
the controller pre-selected from the project KB. Read it before
`validate_task_spec` — it carries decisions, module invariants and prior
context for this task. Treat it as authoritative.

When calling `validate_task_spec`, also pass that same section verbatim as
`project_knowledge` so the reviewer has the same grounding you do. (Omit
this block if there is no KB attached.)

## Task spec (pass these fields verbatim to validate_task_spec)

- task_title:           <from the task block>
- goal:                 <from "Goal:">
- acceptance_criteria:  <from "Acceptance criteria:" bullets>
- non_goals:            <from "Non-goals:" bullets if present>
- context:              <from "Context:" if present>
- pinned_by:            <optional anchors for existing behavior>
- controller_verified_references: <optional references the controller already verified>
- plan_run_id:          <optional, v0.15.0+; from the controller's validate_plan>
- project_knowledge:    <optional, v0.6.0+; markdown excerpts the controller pre-selected from the KB>
- harness_shape_attestation: <optional structured input; see §3.8>
- phase:                <optional; "pre" (default) or "post" for post-hoc/session-recovery>
```

If a `severity: major` pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.

**Short variant** — for agents already carrying the full clause in their system prompt:

````markdown
## Drift protection

Use anti-tangent per the standard dispatch protocol. For this task:
- Call `validate_task_spec` before edits unless the controller set `lightweight_eligible: true`.
- Call `validate_completion` before DONE and paste its `summary_block`.
- If CodeScene MCP is configured, `pre_commit_code_health_safeguard` (mid-task) and `analyze_change_set` (pre-DONE) are required; pass the pre-DONE result to `validate_completion` as the `codescene` argument (or, for an attempted-and-failed run, `{"ran": false, "skip_reason": "…", "skip_evidence": "…"}`).
- If the response carries `submission_defect_only: true`, attach the missing evidence and re-submit — a submission defect, not a code defect.
- If any major pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.
- If a Project knowledge section is auto-attached, read it before validate_task_spec and pass it verbatim as project_knowledge.
````

**Language-scoping prose caveat.** Reviewers can surface `ambiguous_spec` findings around closure/scoping semantics (Kotlin `var` captured by a lambda, Python `nonlocal`, JS `let`/`const` in arrow bodies) when the prose AC reads ambiguously though the plan's verbatim code block does not. Trust the verbatim plan code; deviate only if the *tests* disagree with the prose, and ask the controller if you can't reconcile the two.

### Lightweight protocol mode (v0.3.1+)

For trivial tasks — doc-only edits, single-file mechanical relocations, dependency bumps — the full clause is overhead. Controllers may dispatch a **lightweight clause**: skip `validate_task_spec`, skip `check_progress`, keep `validate_completion` as the sanity gate (its handler accepts an empty `session_id` when any of `final_files` / `final_diff` / `test_evidence` is non-empty).

Use lightweight mode when ALL of: (a) ≤ 2 files or docs/config/data-only; (b) mechanical (no new logic, no test-design choices); (c) the spec gives literal text, an exact diff, command or insertion shape. `validate_plan`'s `lightweight_eligible` / `lightweight_reason` hints are advisory, not permission to skip judgment.

Use the full protocol for new production logic, test-design choices, or ACs requiring observable invariants. Reference lightweight dispatch clause: `examples/lightweight-dispatch.md`.

**Lightweight mode and `ANTI_TANGENT_CODESCENE=required`.** Unset: lightweight tasks skip the companion calls (`pre_commit_code_health_safeguard` / `analyze_change_set`) — nothing meaningful on a trivial edit — and `codescene` is optional. `required` is an operator assertion CodeScene is present, so lightweight tasks must **run `analyze_change_set` and submit its result, exactly as any other task** — being lightweight is not a skip reason, and the failed-run shape in §4.2 step 3b covers an attempted run that failed, never one never attempted.

### CodeScene MCP companion

CodeScene covers anti-tangent's text-only blind spot (see `## Scope and limits`): the [CodeScene MCP server](https://github.com/codescene-oss/codescene-mcp-server) runs deterministic Code Health analysis over the actual files, complementing anti-tangent's LLM review of plan text.

**Tool-to-phase mapping.** When CodeScene MCP is configured, these calls are **required** (§4.2 steps 2b/3b):

- Mid-task: `pre_commit_code_health_safeguard` after meaningful changes (uncommitted/staged only; deterministic and fast).
- Before DONE: `analyze_change_set` for the full branch-vs-base view — see §4.2 step 3b for what to do with the result.
- Drill-down on a flagged issue: `code_health_review`.

Enforcement is prompt-level: the requirement to call these tools lives here and in §4.2, not the server. Once you do call `validate_completion`, `ANTI_TANGENT_CODESCENE=required` can deterministically add a `codescene_not_run` / `codescene_skipped` finding server-side (see `core.md`) — but no CodeScene finding alone reaches `fail`: a lone adoption `major` yields `warn`, though it can be the second `major` (alongside a reviewer major or the `test_evidence` major) that tips a verdict to `fail`. If CodeScene MCP isn't configured the companion calls are skipped, as on unset-mode lightweight tasks — but the `codescene` argument is a separate requirement under `required` mode; see [Lightweight protocol mode](#lightweight-protocol-mode-v031) above.

**CodeScene stats:** CodeScene keeps no history — [docs/team-setup/codescene-stats.md](https://github.com/patiently/anti-tangent-mcp/blob/main/docs/team-setup/codescene-stats.md) logs Code Health to `codescene-events.jsonl`.

### Large reads

If a `Read` is blocked for exceeding the line threshold, do not work around it
with `cat` and do not lower the threshold. Call `bulk_read` with a **question**
— "which methods write to the database?", not "summarise this file". You get
bullets led by exact identifiers.

To EDIT what the answer found, take a targeted read of that region
(`offset`/`limit`); targeted reads are never blocked. Never edit from the answer
alone — it carries no reliable line anchors.

### 4.3 How to address findings

**Address vs. push back.** Reviewer LLMs can be wrong. If a finding misreads the code, document the disagreement in the next call's `working_on` field — e.g. `working_on: "addressed all findings except F#3, which is incorrect: the helper does perform the length check, handlers.go line 42"` — and re-validate. Don't silently ignore: the next reviewer call won't see your reasoning unless you write it.

**The retry loop.** Parse failures on the reviewer's response are handled inside the server (one retry with a JSON-only reminder); the implementer does nothing.

**Session not found.** A `category: session_not_found` finding means the session expired (default TTL 4h) or was never created. Call `validate_task_spec` again and continue with the new ID.

### 4.4 Comments

Comments explain non-trivial behaviour, or a non-obvious invariant or hazard
that would bite the next editor. The test: the comment reads correctly to
someone who never saw the change that introduced it.

Comments do NOT carry change history — no tracker keys (`ABC-1234:`), issue,
pull-request or task references, no version references, no "previously" / "no
longer" / "this replaced". Git holds that, and a comment repeating it goes
stale on the next change.

When you touch code whose comments break these rules, remove or rewrite them as
part of your task. There is no separate cleanup pass.

If `anti-tangent-guard` is installed, a clean scanner run is not evidence
the policy above was followed; apply it yourself. Its `PreToolUse` hook
can also refuse an `Edit`/`Write` outright — rewrite the flagged comment and
retry the edit. Mechanics and limits: the plugin's
[README](https://github.com/patiently/anti-tangent-mcp/blob/main/plugin/anti-tangent-guard/README.md).
