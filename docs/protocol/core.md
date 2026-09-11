# anti-tangent protocol — core

**Everyone reads this part.** It carries the tool surface, what the reviewer can and cannot
catch, when the protocol applies, and the FAQ. Then read your role's part:
[`authoring.md`](authoring.md) (drafting task blocks), [`implementer.md`](implementer.md)
(writing the code), [`controller.md`](controller.md) (dispatching subagents or running a plan
end to end).

`anti-tangent-mcp` is an advisory MCP server that helps prevent implementing-subagent drift while working on **tasks from a written implementation plan**. It exposes nine tools: a plan-level handoff gate (`validate_plan`), three per-task lifecycle hooks (`validate_task_spec` / `check_progress` / `validate_completion`), an optional project-knowledge pair (`prime_project_knowledge` / `extract_project_knowledge`), a deterministic plan-run report (`plan_run_report`), and an I/O-delegation pair (`bulk_read` / `code_write`) routing large reads and boilerplate generation to a cheap worker model. The first six use a reviewer LLM deliberately different from the implementer, so review isn't blind to its blind spots; the delegation pair sends nothing for review — it moves volume, not judgement. Authoritative design: [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md).

**Install, configure, and the full tool surface:** see [`README.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/README.md). This document covers the protocol for using it.

This document has three audiences:

- **Plan authors** — a task format that maps directly to `validate_task_spec` inputs (one-time read while drafting).
- **Controllers** (orchestrators dispatching implementers — superpowers' `subagent-driven-development`, hone-ai's equivalent, or a hand-rolled loop) — a **required plan-handoff gate** plus a paste-in dispatch clause per subagent prompt.
- **Implementing subagents** — a paste-in lifecycle clause mandating pre + post calls, treating mid calls as optional (call only when you suspect drift), and how to handle findings.

The integration is **system-agnostic**: superpowers, hone-ai, vanilla Claude Code with a project-level `CLAUDE.md`, Cursor, or any MCP-capable harness. It ships as this core part plus the role parts above and the optional `project-knowledge.md`; load the ones your role needs and paste the relevant chunks where they belong.

> **When does anti-tangent-mcp earn its keep?** Its value compounds when (a) tasks are specced before implementation, (b) the implementer is an LLM that can drift, and (c) implementer and reviewer LLMs differ. Without all three it is just extra latency.

---

## Scope and limits

**Good at.** Plan-internal consistency: contradictions between ACs, missing observable assertions, scope creep against non-goals, structural completeness of task headers, hedge language in ACs.

**Structurally cannot catch.** The reviewer reasons over plan text and submitted evidence — *not* the codebase. It will not detect:

- Field/symbol names that don't exist in the codebase.
- Function signatures or insertion points that don't exist.
- Repo-wide invariants encoded elsewhere (a constant whose characters another module's validator rejects).
- Existing conventions in adjacent code.
- CI/test policy declared in `CLAUDE.md` / `AGENTS.md`.
- Type-system facts (required fields with no default).

**Pair with a codebase-aware review for any plan that lands in real code.** Text-only + codebase-aware catches both classes; either alone has a known blind spot.

When the reviewer meets a plan claim it cannot verify text-only, it flags `unverifiable_codebase_claim` rather than silently passing. These are *not failures* — treat them as "things to grep before dispatching."

When `validate_plan`'s `context_paths` attached the relevant file, the reviewer
verifies the claim against it instead of flagging `unverifiable_codebase_claim`. If the attached
file refutes the claim it emits `contradicted_codebase_claim`, which keeps its own severity (no
`minor` floor), stays out of the `codebase_reference_checklist` rollup and is never force-passed
by the unverifiable-only verdict calibration — an attached file is ground truth read from disk,
not a caller claim. That holds only while the finding names one of the attached files; naming
none demotes it to `unverifiable_codebase_claim`, floor, rollup and force-pass included.

### Reducing text-only review noise

- Pre-flight grep before calling `validate_task_spec` when the task names codebase references.
- Use `pinned_by` to name tests/docs/commands that pin "unchanged behavior" ACs.
- Use `controller_verified_references` for paths, symbols, line anchors, commands or adjacent patterns the controller verified pre-dispatch.
- Do not paste self-review claims like "all file references were verified" into the plan — the reviewer cannot confirm them and flags `unverifiable_codebase_claim`.
- State commit-policy carve-outs literally in the plan. The reviewer sees only the plan
  content you provide — via `plan_text` or `plan_path` — never repo policy files like
  `CLAUDE.md` / `AGENTS.md`.
- For doc deliverables, submit full content via `final_files`; diffs or prose summaries are often insufficient.

### Choosing `pinned_by`, `context`, and `controller_verified_references`

- **`context`** — background a fresh implementer needs (constraints, repo carve-outs, prior decisions). Helps the reviewer judge ambiguity; not a code-reference claim.
- **`pinned_by`** — existing tests, docs, commands, or static checks pinning a terse AC like "retry behavior remains unchanged." Caller-supplied anchors, not verified facts.
- **`controller_verified_references`** — code refs the controller already grep-verified (paths, symbols, anchors). The pre-task reviewer suppresses `unverifiable_codebase_claim` on deterministic substring match only; contradictions, missing ACs, ambiguity and `convention_deviation` are NOT suppressed. `testability_extractions` suppresses `scope_drift` on intentional extractions; `codebase_conventions` triggers `convention_deviation`.

---

## What is never delegated

Two loops hand work to another model: the review loop (a reviewer LLM judges
a task) and the I/O loop (a worker reads files or generates boilerplate).
Both stop at the same line.

- **Reasoning stays with the implementer.** Debugging, architecture, any
  correctness argument — a digest doesn't substitute for reading the file
  when the question is *why* something misbehaves.
- **Changes to existing code stay with the implementer.** A worker's answer
  has no reliable line anchors: use it to find the region, then read and edit
  it yourself. Generating a NEW file from a pattern is delegable — but picking
  the reference and proving the result work stay yours. Generated code is
  **verified, not inspected**: run the tests and the build, don't read it
  back — that's the saving.
- **Judgement stays with the implementer.** Delegating the reading is not
  delegating the deciding.
- **Small inputs aren't worth delegating.** Below threshold the round trip
  costs more than it saves.

Delegation moves *volume* off the implementer's context, never
responsibility: they still answer for the result at `validate_completion`.

---

## 1. When the protocol applies

**Strict trigger:** the work is a task from an implementation plan with the structured **Goal / Acceptance criteria / (Non-goals) / (Context)** header (see [`authoring.md`](authoring.md) §3). If those fields are present, the protocol applies — whether you implement directly or dispatch it.

**Skip the protocol entirely** for:

- Read-only research, exploration, Q&A.
- Code review of existing code.
- Plan or spec authoring (the author isn't implementing yet).
- Brainstorming / design discussions.
- One-off changes that didn't come from a plan (typo fixes, config tweaks, mid-conversation refactors, debugging help).
- Subagents dispatched for non-implementation work (Explore, summarizers, code/security reviewers).
- Doc-only edits unless the doc IS the planned task.

If unsure, look for the structured task block. No block → no protocol. Don't fire the tools "for safety" on ad-hoc work — calls cost and noise dilutes the signal.

---

## 6. FAQ / failure modes

**Finding categories.** Canonical set surfaced by the reviewer (authoritative enum: `internal/verdict/verdict.go`):

- Spec / lifecycle: `missing_acceptance_criterion`, `scope_drift`, `ambiguous_spec`, `unaddressed_finding`, `quality`, `convention_deviation`, `attestation_contradiction`, `unverifiable_codebase_claim`, `contradicted_codebase_claim`, `other`.
- Evidence: `insufficient_evidence` — emitted by `validate_completion` when an AC cannot be assessed from the submitted evidence, and by `extract_project_knowledge`. Server-only: `malformed_evidence`, `codescene_not_run`, `codescene_skipped`.
- Operational: `session_not_found`, `payload_too_large`.
- Project-knowledge: `kb_gap`, `ambiguous_pick`, `missing_index_entry` (prime); `redundant_proposal`, `contradicts_existing` (extract).

**My implementer is also Claude Sonnet — does this still help?** Less than with different models — same model + same training data ≈ same blind spots. Different provider is best; failing that, different family (Sonnet implementer, Opus reviewer; Haiku for cheap mid-checks, Opus for post).

**How do I know my session expired?** A `category: session_not_found` finding. Default TTL 4h; re-call `validate_task_spec` for a fresh session.

**My payload is too big.** A `category: payload_too_large` finding. Default cap 200 KB across `changed_files`, `final_files` and `final_diff`, set by `ANTI_TANGENT_MAX_PAYLOAD_BYTES`. For `validate_completion`, pass `final_diff` instead of or alongside `final_files`; for `check_progress`, reduce `changed_files` or split the call. `validate_plan` uses `ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES`, and `context_paths` adds two of its own — `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES` per file, `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` for the attached set — plus a fixed 50-file count cap. `evidence` names which one was breached.

**A `validate_completion` call returned `category: malformed_evidence`.** The server's evidence-shape guard rejected your submission pre-review. `evidence` names the offending pattern — a truncation marker (`(truncated)`, `[truncated]`, `// ... unchanged`), a `...`-only placeholder line, or empty `Path` entries in `final_files`. Re-submit with full file contents or a complete unified diff. Rejection is cached for 5 minutes by canonical content hash. If a file legitimately contains one of these strings (a fixture or doc), pass a complete `final_diff` instead.

**A `validate_completion` call returned `category: codescene_not_run` or `category: codescene_skipped`.** Only fires when `ANTI_TANGENT_CODESCENE=required`. Four cases:

- No `codescene` argument on the call → `codescene_not_run`, `severity: major` — a submission defect (see `submission_defect_only` below).
- `codescene: {"ran": false, "skip_reason": "…"}` → `codescene_skipped`, `severity: major`; add `skip_evidence` to lower it to `minor`.
- `codescene: {"ran": false}` with no `skip_reason` → `codescene_not_run`, `severity: major`, same as no argument at all. **An undeclared skip is treated as a non-run** — without a stated reason the server can't tell "forgot to run it" from "ran it and didn't say", so state one.
- `codescene: {"ran": true, …}` → no adoption finding.

Fix: pass the `codescene` argument (see [`implementer.md`](implementer.md) §4.2 step 3b), or if you deliberately skipped, include both `skip_reason` and `skip_evidence` — `skip_reason` alone still draws a major.

**A `validate_completion` finding carries `criterion: test_evidence`.** The submitted evidence says no test executed — a Gradle test task's `NO-SOURCE`, "no tests ran", "no tests found", or `[no test files]`, with nothing beside it showing a suite run. Cached and up-to-date runs do not fire it. Re-run against a target with tests and re-submit that output, or attach JUnit XML `tests=`/`failures=` counts — a submission defect, see below.

**What is `submission_defect_only: true`?** Every blocking finding on that `validate_completion`
response is about what you submitted — absent evidence, malformed evidence, or a CodeScene run
that did not happen — not about your code. Attach what is missing and call again. No rework is
implied; the reviewer has not yet seen your code.

**A hook returned `category: other` with `criterion: reviewer_response`.** Reviewer output was cut off at the token budget. The server parses truncated responses tolerantly and surfaces any complete findings before the cap (look for `"partial": true` and a `severity: minor` truncation marker). For the full response next call, raise `ANTI_TANGENT_PER_TASK_MAX_TOKENS` / `ANTI_TANGENT_PLAN_MAX_TOKENS` globally, or pass `max_tokens_override`.

**A finding has `category: attestation_contradiction` — what is that?** An AC explicitly contradicts a `harness_shape_attestation` entry (see [`authoring.md`](authoring.md) §3.8). NOT severity-floored (unlike `convention_deviation` / `unverifiable_codebase_claim`); the reviewer's chosen severity is preserved.

**`validate_task_spec` is asking for ACs my plan doesn't have.** Spec quality gate working as designed. Either (a) add the missing ACs and re-validate, or (b) acknowledge the gap in the next `working_on` so the reviewer expects implementer-discretion choices.

**What if the implementer skips the post-hook?** Two defenses: [`implementer.md`](implementer.md) §4.2 marks post REQUIRED, and the controller can require the post-hook envelope in the DONE report ([`controller.md`](controller.md) §5.3).

**Does `check_progress` catch failing tests?** No — the reviewer reasons over text, not execution. Use it for drift detection (scope creep, untouched ACs, unaddressed findings); run tests separately.

**Cost / latency overhead.** Roughly 1–2 s and $0.001–$0.02 per call without `context_paths`. With a large attached set a single `validate_plan` round can reach ~$1.31 — [`controller.md`](controller.md) §5.8 has the measurement and its drivers. One mandatory `validate_plan` per handoff, two mandatory implementer calls per task (pre + post). Use a cheap-fast model for mid-checks, a stronger one for handoff/post.

**Where do I file bugs?** <https://github.com/patiently/anti-tangent-mcp/issues>.

---

## Environment variables

Defaults shown; [`README.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/README.md) has the full dotenv block.

- `ANTI_TANGENT_CODESCENE` — `""` (off). Set to `required` to add a `major` finding on a missing
  `codescene` argument or an unevidenced skip — see the ladder above. A lone CodeScene major
  yields `warn`; combined with another major it can tip a verdict to `fail`.
- `ANTI_TANGENT_PLAN_LEDGER` — `0` (off). With `ANTI_TANGENT_STATS_DIR` set, `1` persists each
  completed task row to `plan-runs.jsonl` so `plan_run_report` survives a restart. Unlike every
  other stats artifact it carries task titles, hence its own opt-in.
