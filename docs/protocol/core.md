# anti-tangent protocol — core

**Everyone reads this part.** It carries the tool surface, what the reviewer can and cannot
catch, when the protocol applies at all, and the FAQ. Then read the part for your role:
[`authoring.md`](authoring.md) (drafting task blocks), [`implementer.md`](implementer.md)
(writing the code), [`controller.md`](controller.md) (dispatching subagents or running a plan
end to end).

# Integrating anti-tangent-mcp

`anti-tangent-mcp` is an advisory MCP server that helps prevent implementing-subagent drift while working on **tasks from a written implementation plan**. It exposes six tools: a plan-level handoff gate (`validate_plan`), three per-task lifecycle hooks (`validate_task_spec` / `check_progress` / `validate_completion`), and an optional project-knowledge pair (`prime_project_knowledge` / `extract_project_knowledge`). The reviewer LLM is intentionally a different model from the implementer, so reviews are not blind to the implementer's blind spots. See [`README.md`](README.md) for the tool surface and [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md) for the authoritative design.

**Install and configure:** see [`README.md`](README.md). This document covers the using-the-MCP protocol.

This document has three audiences:

- **Plan authors** — get a recommended task format that maps directly to `validate_task_spec` inputs (one-time read while drafting).
- **Controllers** (orchestrators that dispatch implementing subagents — superpowers' `subagent-driven-development`, hone-ai's equivalent, or a hand-rolled loop) — get a **required plan-handoff gate** plus a paste-in dispatch clause to thread the protocol into each subagent prompt.
- **Implementing subagents** — get a paste-in lifecycle clause that mandates pre + post calls, treats mid calls as optional (call only when you suspect drift), and tells them how to handle findings.

The integration is **system-agnostic**: it works with superpowers, hone-ai, vanilla Claude Code with a project-level `CLAUDE.md`, Cursor, or any harness that supports MCP servers. It ships as a single markdown document; you paste the relevant chunks where they need to go.

> **When does anti-tangent-mcp earn its keep?** Its value compounds when (a) tasks are specced before being implemented, (b) the implementer is an LLM that can drift, and (c) the implementer LLM differs from the reviewer LLM. Without all three, anti-tangent is just extra latency.


---

## Scope and limits

**Good at.** Plan-internal consistency: contradictions between ACs, missing observable assertions, scope creep relative to non-goals, structural completeness of task headers, hedge language in ACs.

**Structurally cannot catch.** The reviewer reasons over plan text and submitted evidence — *not* the codebase. It will not detect:

- Field/symbol names that don't exist in the codebase.
- Function signatures or insertion points that don't exist.
- Repo-wide invariants encoded elsewhere (e.g. a constant whose characters another module's validator rejects).
- Existing conventions in adjacent code.
- CI/test policy declared in `CLAUDE.md` / `AGENTS.md`.
- Type-system facts (required fields with no default).

**Pair with a codebase-aware review for any plan that lands in real code.** Text-only + codebase-aware catches both classes; either alone has a known blind spot.

When the reviewer encounters a plan claim it cannot verify text-only, as of v0.3.1 it flags `unverifiable_codebase_claim` rather than silently passing. These are *not failures* — treat them as "things to grep before dispatching."

### Reducing text-only review noise

- Pre-flight grep before calling `validate_task_spec` when the task names codebase references.
- Use `pinned_by` to name existing tests/docs/commands that pin "unchanged behavior" ACs.
- Use `controller_verified_references` for specific paths, symbols, line anchors, commands, or adjacent patterns the controller already verified before dispatch.
- Do not paste self-review claims like "all file references were verified" into the plan text — the reviewer cannot confirm such claims and will flag them as `unverifiable_codebase_claim`.
- State commit-policy carve-outs literally in the plan text. The reviewer reads only `plan_text`, not repo-level policy files.
- For doc deliverables, submit full content via `final_files`; diffs or prose summaries are often insufficient evidence.

### Choosing `pinned_by`, `context`, and `controller_verified_references`

- **`context`** — background a fresh implementer needs (constraints, repo carve-outs, prior decisions). Helps the reviewer judge ambiguity; not a code-reference claim.
- **`pinned_by`** — existing tests, docs, commands, or static checks pinning a terse AC like "retry behavior remains unchanged." Caller-supplied anchors, not verified facts.
- **`controller_verified_references`** — code refs the controller already grep-verified (paths, symbols, anchors). Pre-task reviewer suppresses `unverifiable_codebase_claim` on deterministic substring match only; contradictions, missing ACs, ambiguity, `convention_deviation` findings are NOT suppressed. `testability_extractions` suppresses `scope_drift` on intentional extractions; `codebase_conventions` triggers `convention_deviation` findings.

---

## 1. When the protocol applies

**Strict trigger:** the work is a task from an implementation plan with the structured **Goal / Acceptance criteria / (Non-goals) / (Context)** header (see §3). If those fields are present, the protocol applies — whether you implement directly or dispatch to a subagent.

**Skip the protocol entirely** for:

- Read-only research, exploration, Q&A.
- Code review of existing code.
- Plan or spec authoring (the author isn't implementing yet).
- Brainstorming / design discussions.
- Ad-hoc one-off changes that didn't come from a plan (typo fixes, config tweaks, mid-conversation refactors, debugging help).
- Subagents dispatched for non-implementation work (Explore, summarizers, code/security reviewers).
- Doc-only edits unless the doc IS the planned task.

If you're unsure, look for the structured task block. No block → no protocol. Don't fire the tools "for safety" on ad-hoc work — calls have real cost and noise dilutes the signal.

---

## 6. FAQ / failure modes

**Finding categories.** Canonical set surfaced by the reviewer (see `internal/verdict/verdict.go` for the authoritative enum):

- Spec / lifecycle: `missing_acceptance_criterion`, `scope_drift`, `ambiguous_spec`, `unaddressed_finding`, `quality`, `convention_deviation`, `attestation_contradiction`, `unverifiable_codebase_claim`, `other`.
- Operational: `session_not_found`, `payload_too_large`.
- Project-knowledge (v0.6.0+): `kb_gap`, `ambiguous_pick`, `missing_index_entry` (prime); `insufficient_evidence`, `redundant_proposal`, `contradicts_existing` (extract).

**My implementer is also Claude Sonnet — does this still help?** Less than if they were different models — same model + same training data ≈ same blind spots. Different provider is best; failing that, different family (Sonnet implementer, Opus reviewer; or Haiku for cheap mid-checks plus Opus for post).

**How do I know my session expired?** A `category: session_not_found` finding. Default TTL is 4h. Re-call `validate_task_spec` to start a fresh session.

**My payload is too big.** A `category: payload_too_large` finding. Default cap is 200 KB across `changed_files`, `final_files`, and `final_diff`. For `validate_completion`, pass `final_diff` instead of or alongside `final_files`; for `check_progress`, reduce `changed_files` or split the call. `ANTI_TANGENT_MAX_PAYLOAD_BYTES` controls the cap.

**A `validate_completion` call returned `category: malformed_evidence`.** The server's evidence-shape guard rejected your submission pre-review. The `evidence` field names the offending pattern — typically a truncation marker (`(truncated)`, `[truncated]`, `// ... unchanged`), a `...`-only placeholder line, or empty `Path` entries in `final_files`. Re-submit with full file contents or a complete unified diff. Rejection is cached for 5 minutes by canonical content hash. If your file legitimately contains one of these literal strings (e.g. a fixture or doc), pass a complete `final_diff` rather than `final_files`.

**A hook returned `category: other` with `criterion: reviewer_response`.** Reviewer output was cut off at the token budget. As of v0.3.0, the server runs truncated responses through a tolerant parser and surfaces any complete findings before the cap (look for `"partial": true` and a `severity: minor` truncation marker). To get the full response next call, raise `ANTI_TANGENT_PER_TASK_MAX_TOKENS` / `ANTI_TANGENT_PLAN_MAX_TOKENS` globally, or pass `max_tokens_override`.

**A finding has `category: attestation_contradiction` — what is that?** An AC explicitly contradicts a `harness_shape_attestation` entry (see §3.8). NOT severity-floored (unlike `convention_deviation` / `unverifiable_codebase_claim`); the reviewer's chosen severity is preserved.

**`validate_task_spec` is asking for ACs my plan doesn't have.** Spec quality gate working as designed. Either (a) add the missing ACs and re-validate, or (b) acknowledge the gap in the next `working_on` description so the reviewer expects implementer-discretion choices.

**What if the implementer skips the post-hook?** Two defenses: §4.2 marks post REQUIRED in the implementer prompt, and the controller can require the post-hook envelope in the DONE report (§5.3).

**Does `check_progress` catch failing tests?** No — the reviewer reasons over text, not execution. Use it for drift detection (scope creep, untouched ACs, unaddressed prior findings); run tests separately.

**Cost / latency overhead.** Roughly 1–2 s and $0.001–$0.02 per call. One mandatory `validate_plan` per handoff, two mandatory implementer calls per task (pre + post). Use a cheap-fast model for mid-checks and a stronger model for handoff/post.

**Where do I file bugs?** [`https://github.com/patiently/anti-tangent-mcp/issues`](https://github.com/patiently/anti-tangent-mcp/issues).
