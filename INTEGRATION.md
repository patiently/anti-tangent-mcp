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

**What `anti-tangent-mcp` is good at.** Plan-internal consistency: contradictions between ACs, missing observable assertions, scope creep relative to non-goals, structural completeness of task headers, hedge language in acceptance criteria.

**What it structurally cannot catch.** The reviewer reasons over the plan text and submitted evidence — *not* the codebase. It will not detect:

- Field/symbol names that don't exist in the codebase.
- Function signatures or insertion points that don't exist.
- Repo-wide invariants encoded elsewhere (e.g. a constant containing characters another module's validator rejects).
- Existing conventions in adjacent code.
- CI/test policy declared in `CLAUDE.md` / `AGENTS.md`.
- Type-system facts (required fields with no default).

**Pair with a codebase-aware review for any plan that lands in real code.** A text-only reviewer paired with a codebase-aware pass catches both classes of bugs; either alone has a known blind spot.

When the reviewer encounters a plan or task-spec statement about codebase facts it cannot verify text-only, as of v0.3.1 it flags an `unverifiable_codebase_claim` finding rather than silently passing. These are explicitly *not failures* — they're a checklist for the human or a codebase-aware follow-up review. A plan that converges to `pass` with several `unverifiable_codebase_claim` findings is still implementable; treat the findings as "things to grep before dispatching."

### Reducing text-only review noise

- Pre-flight grep before calling `validate_task_spec` when the task names codebase references.
- Use `pinned_by` to name existing tests/docs/commands that pin "unchanged behavior" ACs.
- Use `controller_verified_references` for specific paths, symbols, line anchors, commands, or adjacent patterns the controller already verified before dispatch.
- Do not paste self-review claims like "all file references were verified" into the plan text — the reviewer cannot confirm such claims and will flag them as `unverifiable_codebase_claim`.
- State commit-policy carve-outs literally in the plan text. The reviewer reads only `plan_text`, not repo-level policy files.
- For doc deliverables, submit full content via `final_files`; diffs or prose summaries are often insufficient evidence.

### Choosing `pinned_by`, `context`, and `controller_verified_references`

Use `context` for background a fresh implementer needs to understand the task: product constraints, repo policy carve-outs, prior decisions, or why a non-obvious approach is required. It helps the reviewer judge ambiguity, but it is not a claim that a specific code reference exists.

Use `pinned_by` for anchors that preserve behavior: existing tests, docs, commands, or static checks that pin a terse AC such as "retry behavior remains unchanged." The reviewer treats these entries as caller-supplied anchors, not independently verified codebase facts.

Use `controller_verified_references` for codebase references the controller has already checked before dispatch: paths, symbols, line anchors, commands, or adjacent patterns. The pre-task reviewer suppresses `unverifiable_codebase_claim` only when the task claim and a controller-verified entry match by deterministic substring; contradictions, missing ACs, ambiguity, and `convention_deviation` findings are NOT suppressed. CVR is a single-category suppression — use `testability_extractions` to suppress `scope_drift` on intentional helper extractions and `codebase_conventions` to actively trigger `convention_deviation` findings.

---

## 1. When the protocol applies

**Strict trigger:** the work item is a task from an implementation plan that has the structured **Goal / Acceptance criteria / (Non-goals) / (Context)** header (see §3 for the exact shape). If those fields are present, the protocol applies — whether you do the work directly or dispatch it to a subagent.

**Skip the protocol entirely** for any of:

- Read-only research, exploration, or Q&A.
- Code review of existing code.
- Plan or spec authoring (the plan author isn't implementing yet — they're producing the task spec the implementer will validate against).
- Brainstorming / design discussions.
- Ad-hoc one-off changes that didn't come from a plan: a quick typo fix, a small config tweak, a refactor that arose mid-conversation, debugging help, etc.
- Subagents dispatched for non-implementation work (Explore, summarizers, code reviewers, security reviewers, etc.).
- Doc-only edits unless the doc IS the planned task.

If you're unsure whether work is in scope, look for the structured task block. No structured task block → no protocol. Don't fire the tools "for safety" on ad-hoc work; the calls have real cost and noise findings dilute the signal when it actually matters.

---

## 3. For plan authors — the anti-tangent-friendly task format

When you write a plan, give each task a small structured header block. The implementing subagent will pass these fields verbatim into `validate_task_spec`, and the reviewer LLM uses them to decide whether the spec is implementable as written.

### 3.1 The required shape

```markdown
### Task N: <one-line title>

**Goal:** <one sentence: what success looks like>

**Acceptance criteria:**
- <testable criterion 1>
- <testable criterion 2>

**Non-goals:** *(optional but recommended)*
- <thing this task explicitly does NOT cover>

**Context:** *(optional)*
<relevant background, constraints, or links a fresh implementer needs>

<… your existing plan structure: Files / Steps / Code / etc. …>
```

The existing "Files:" / "Steps:" structure that superpowers, hone-ai, and most CLAUDE.md plans already use lives below the header block. The header is additive.

### 3.2 Worked example

```markdown
### Task 4: Add /healthz endpoint

**Goal:** Expose a liveness probe for the HTTP server.

**Acceptance criteria:**
- `GET /healthz` returns HTTP 200 with body `ok`.
- p95 latency under 50 ms at 100 RPS on a warm process.
- Endpoint is registered in `cmd/api/router.go` and covered by a handler test.

**Non-goals:**
- Database health (covered separately by `/healthz/deep`).
- Authentication on the endpoint.

**Context:**
The service is a Gin app on port 8080. The probe is consumed by the
Kubernetes liveness check defined in `deploy/k8s/api.yaml`.
```

### 3.3 What `validate_task_spec` actually checks

- **Structural completeness.** Is the goal stated? Are there acceptance criteria? Are non-goals declared where they help bound scope?
- **Acceptance-criterion quality.** Is each AC testable, specific, and unambiguous? For any vague AC, the reviewer suggests a concrete rewrite.
- **Implicit assumptions.** Each assumption a fresh implementer would have to make becomes a finding, so the spec author can either pin it down or explicitly mark it as implementer's discretion.

### 3.5 Anti-pattern: keep implementation steps OUT of the AC list

Acceptance criteria describe *what done looks like*, not *how to get there*. Implementation steps belong in the "Steps:" / "Files:" portion of the task, where they always lived. Mixing them produces brittle ACs that the reviewer flags as either redundant or hyper-specific.

### 3.6 Normative test bodies (binding test code in plans)

When a task's plan pastes verbatim test code that the implementer must land as written, wrap each test body in a fenced block immediately under a literal `**NORMATIVE TEST BODIES (verbatim):**` header. `validate_plan` extracts each fence server-side (deterministic markdown parsing) and threads the list into the per-task `validate_task_spec` `normative_test_bodies` input; the reviewer then treats each entry as binding scope. Adjacent fences extract as separate entries. Bodies exceeding 4000 Unicode code points are server-truncated with a `// truncated` marker; for legitimately longer bodies, paraphrase or excerpt and start the body with `// excerpt:` so the reviewer treats it as partial coverage.

### 3.7 `.trimIndent()` raw-string caveat

When a plan snippet is wrapped in `.trimIndent()` (or any equivalent raw-string trim), multi-line source phrases render newlines exactly where they sit in the markdown — anti-tangent reads the source, not the rendered output. Keep example strings the implementation will compare against on a single source line, and phrase ACs against the rendered string (e.g. "output contains `please decline politely`"), not against source layout.

### 3.8 Harness shape attestations (v0.5.2+)

`harness_shape_attestation` is a structured optional input on `validate_task_spec`. Each entry is `{harness: string, path: string, assertions: []string}`. Use it when a task's acceptance criteria depend on a test harness's stated capabilities (or stated non-capabilities). The reviewer treats each attestation as authoritative caller-attested context (no independent verification) and flags ACs that EXPLICITLY contradict an entry — e.g. an AC asks for behavior a `does not …` assertion forbids, or an AC asserts a state that directly contradicts a positive assertion — as `attestation_contradiction` findings. Absence of a capability is NOT a contradiction; do not list things to forbid them.

---

## 4. For implementers — the lifecycle protocol

> **Lightweight eligibility first.** Many tasks qualify for lightweight mode (skip `validate_task_spec`, skip `check_progress`, keep `validate_completion` as the sanity gate). Lightweight applies when ALL of: (a) the task touches ≤ 2 files OR is docs/config/data-only; (b) it is mechanical (no production-design or test-design choices); (c) the spec includes the literal text, exact diff, exact command, or exact insertion shape. `validate_plan` may pre-annotate tasks with `lightweight_eligible: true` and `lightweight_reason` — advisory only. See [Lightweight protocol mode](#lightweight-protocol-mode-v031) below for the reference clause.

| Phase | Tool | Required? | When to call |
|---|---|---|---|
| Start | `validate_task_spec` | **Yes** | Once, before writing any code |
| During | `check_progress` | Optional (advisory; low-signal in field data — call only when you suspect drift, OR when a test that 'should' fail doesn't, OR you've spent >5 min debugging behavior the spec leaves under-specified) | When you suspect drift mid-task |
| End | `validate_completion` | **Yes** | Before reporting DONE |

One task = one session = one subagent. The session_id returned by `validate_task_spec` lives in the implementer's context for the lifetime of the task; it is not handed off to anyone else.

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

**2. During work (OPTIONAL).** Call `check_progress` ONLY if you suspect
you're drifting mid-task, OR a test that 'should' fail doesn't, OR
you've spent >5 min debugging behavior the spec leaves under-specified.
Per the 0.3.1 protocol revision this call is advisory — most tasks will
skip it. When you do call, pass: the session_id, a one-sentence
`working_on` summary, and the changed files.

**2b. CodeScene mid-task check (RECOMMENDED — when codescene-mcp is
configured in your host).** Call `pre_commit_code_health_safeguard` after
meaningful code changes to catch Code Health regressions on uncommitted/staged files. This is
deterministic and fast (no LLM call) — complementary to the
LLM-based `check_progress` and higher-signal mid-task. If
codescene-mcp is not configured, skip this step silently.

**3. Before reporting DONE (REQUIRED).** Call `validate_completion` with
the session_id, your summary, the final files, and any test evidence.
**Copy the `summary_block` field from the response verbatim into your DONE report** — it carries the full envelope formatted for paste; you do not need to re-extract JSON fields.
If the verdict is `fail` or contains `critical`/`major` findings, do
not report DONE — fix the findings and re-validate.

**3b. CodeScene pre-DONE check (OPTIONAL — when codescene-mcp is
configured in your host).** Call `analyze_change_set` for the full
branch-vs-base Code Health view. If the delta shows a regression,
include the finding in your DONE summary alongside anti-tangent's
`summary_block` and consider iterating before declaring DONE.
Anti-tangent remains advisory-only; CodeScene findings are
codebase-grounded signal that the text-only reviewer can't produce.
If codescene-mcp is not configured, skip this step silently.

## Project knowledge (auto-attached by the controller)

The task brief above includes a "Project knowledge" section with excerpts
the controller pre-selected from the project KB. Read it before
`validate_task_spec` — it carries decisions, module invariants, and prior
context relevant to this task. Treat it as authoritative.

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
- project_knowledge:    <optional, v0.6.0+; markdown excerpts the controller pre-selected from the KB>
- harness_shape_attestation: <optional structured input; see §3.8>
- phase:                <optional; "pre" (default) or "post" for post-hoc/session-recovery>
```

If any `severity: major` pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE so `validate_completion` and the controller can see how the risk was handled.

**Short variant for agents with the protocol already in their system prompt.** If the implementer already has the full clause above in its system prompt or local instructions, controllers may dispatch the shorter clause:

````markdown
## Drift protection

Use anti-tangent per the standard dispatch protocol. For this task:
- Call `validate_task_spec` before edits unless `lightweight_eligible: true` is set by the controller.
- Call `validate_completion` before DONE and paste its `summary_block`.
- If CodeScene MCP is configured, run `pre_commit_code_health_safeguard` after meaningful code changes.
- If any major pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.
- If a Project knowledge section is auto-attached, read it before validate_task_spec and pass it verbatim as project_knowledge.
````

**Language-scoping prose caveat.** Reviewers can surface `ambiguous_spec` findings around closure/scoping semantics — Kotlin `var` captured by a lambda, Python `nonlocal`, JS `let`/`const` in arrow bodies — when the prose AC reads ambiguously even though the verbatim code block in the plan is unambiguous. Trust the verbatim plan code block; only deviate if the *tests* disagree with the prose. If you genuinely cannot reconcile code and prose, stop and ask the controller.

### Lightweight protocol mode (v0.3.1+)

For trivial tasks — doc-only edits, single-file mechanical relocations, dependency bumps — the full dispatch clause is overhead-heavy (~50 lines of boilerplate for ~15 lines of actual work). Controllers may use a **lightweight clause** for these tasks:

- **Skip** `validate_task_spec` (the spec is fully prescriptive; no design choices for the reviewer to shape).
- **Skip** `check_progress` (already optional in full mode).
- **Keep** `validate_completion` as a sanity gate before reporting DONE. The handler accepts an empty `session_id` when at least one of `final_files` / `final_diff` / `test_evidence` is non-empty.

Use lightweight mode when ALL of: (a) the task touches ≤ 2 files or is docs/config/data-only; (b) the task is mechanical (no new logic, no test-design choices); (c) the spec includes the literal text, exact diff, exact command, or exact insertion shape. `validate_plan` may annotate tasks with `lightweight_eligible` and `lightweight_reason`, but those fields are advisory controller hints rather than permission to skip judgment.

Use the full protocol for: any task that produces new production logic, any task with test-design choices, any task whose ACs require observable invariants.

A reference lightweight dispatch clause is at `examples/lightweight-dispatch.md`.

### CodeScene MCP companion (optional)

Anti-tangent's `## Scope and limits` section above documents what the text-only reviewer structurally cannot catch — codebase-grounded facts like field/symbol existence, function signatures, repo-wide invariants, and adjacent-code conventions. The recommended pairing for that blind spot is the open-source [CodeScene MCP server](https://github.com/codescene-oss/codescene-mcp-server), which exposes deterministic Code Health analysis as MCP tools.

The two tools are complementary, not redundant:

| Surface | anti-tangent-mcp | codescene-mcp |
| --- | --- | --- |
| Reasons over | plan text + submitted evidence | actual files on disk |
| Verdict basis | LLM reviewer (different provider than implementer) | deterministic static analysis |
| Strength | plan-internal consistency, AC quality, scope drift | Code Health regressions, complexity, cohesion |
| Cost | one LLM call per hook | local, near-zero |

**Tool-to-phase mapping.** When CodeScene MCP is configured in your host alongside anti-tangent, instruct dispatched implementers to also call:

- During mid-task work: call CodeScene's `pre_commit_code_health_safeguard` after meaningful code changes. It analyzes only uncommitted/staged files and is fast enough to run repeatedly. The field-data rationale for demoting anti-tangent's `check_progress` to OPTIONAL (low-signal mid-task LLM reviews) does NOT apply to CodeScene — its mid-task call is deterministic and high-signal. Many implementations should skip anti-tangent `check_progress` unless they suspect drift, while still running `pre_commit_code_health_safeguard` when CodeScene is configured.
- Before reporting DONE (alongside `validate_completion`): call CodeScene's `analyze_change_set` for the full branch-vs-base view. If the Code Health delta is negative or a regression is reported, surface it in the DONE summary and consider iterating — anti-tangent itself remains advisory-only, but the implementer-side judgment call benefits from the codebase-grounded second opinion.
- For drill-down on a flagged issue: `code_health_review`.

**Advisory posture.** Anti-tangent never enforces CodeScene findings server-side. The integration lives at the dispatch-clause / convention layer: a controller that has CodeScene MCP installed updates the dispatch clause to include the companion calls; the implementer cites the findings in its DONE summary. If CodeScene MCP isn't configured in the host, the companion calls are simply skipped — anti-tangent's own protocol is unchanged.

**Lightweight mode.** Tasks dispatched under the lightweight protocol (doc-only edits, mechanical relocations) skip `validate_task_spec`, `check_progress`, and the CodeScene companion calls, while still requiring `validate_completion` as the sanity gate.

### 4.3 How to address findings

**Address vs. push back.** Reviewer LLMs can be wrong. If a finding misreads the code, document the disagreement in the next call's `working_on` field — for example, `working_on: "addressed all findings except F#3 which is incorrect because the helper does in fact perform the length check, see handlers.go line 42"` — and re-validate. Don't silently ignore: the next reviewer call won't see your reasoning unless you write it.

**The retry loop.** Parse failures on the reviewer's response are handled inside the server (one retry with a JSON-only reminder). The implementer does not need to handle that.

**Session not found.** If `check_progress` or `validate_completion` returns a finding with `category: session_not_found`, the session expired (default TTL 4h) or was never created. Call `validate_task_spec` again to start a fresh session and continue with the new ID.

---

## Project knowledge (optional)

Project knowledge is an optional v0.6.0+ loop that lets the reviewer LLM ground its review in **what's already true about your project** — decisions taken (and why), module invariants, feature surfaces, glossary terms, and the in-flight epic's progress. It earns its keep on epic-scale projects with multiple agents and multiple human authors, where each task validates cleanly on its own but the pieces stop composing into a working end product. Skip it when the project is single-author or short-lived; anti-tangent's text-only reviewer is otherwise stuck inferring project context from the plan text alone.

The loop has two new MCP tools — `prime_project_knowledge` (pre-task; recommends notes to read) and `extract_project_knowledge` (post-task; proposes notes to write) — plus a `project_knowledge` field on `validate_task_spec` and `validate_plan`. The knowledge itself lives in [Basic Memory](https://github.com/basicmachines-co/basic-memory) (recommended) or any other markdown-backed store; anti-tangent has **zero code dependency** on Basic Memory and never reads or writes that store directly.

### Architecture

```
Multiple agents          Anti-tangent MCP                Reviewer LLM
& human authors          (advisory, stateless)           (different provider)
─────────────────        ─────────────────────           ────────────────
                                                                ▲
       ┌──── kb_index, excerpts ──▶ prime_project_knowledge ────┘
       │      (from BM queries)         │
       │                                ▼
       │                          "read these notes;
       │                           KB gaps: …"
       │
       └──── validate_task_spec(+project_knowledge) ──▶ existing review,
       │                                                grounded in KB excerpts
       │
       │                          completion envelopes
       │                                │
       │     ┌──── envelopes,     ──▶ extract_project_knowledge ──▶ proposals
       │     │     kb_index,            │                          (create/update/
       │     │     excerpts             ▼                           supersede)
       │     │                    structured proposals
       │     │                          │
       ▼     ▼                          ▼
┌────────────────────────┐  caller applies proposals
│   Basic Memory MCP     │◀────────────┘
│  (shared local store)  │
└────────────────────────┘
```

### Controller workflow (per epic)

The server is stateless; everything that ties prime → implement → extract together lives in the controller's dispatch logic.

1. **Per task, before dispatch.** Search the KB for notes relevant to the task (by task terms plus the epic's `touches_modules` and `relates`) → that's the `kb_index`. Call `prime_project_knowledge` with the task fields + `kb_index` + `epic_permalink` → it returns `picks` (and `bm_commands` when `ANTI_TANGENT_KB_STORE=basic-memory`). Read the picked notes from the KB → assemble them into a `kb_excerpts` markdown string.
2. **Dispatch.** Include the `kb_excerpts` block in the implementer's brief AND pass that same string as `project_knowledge` into `validate_task_spec`. The subagent itself makes no prime/extract calls — those are controller responsibilities (the controller owns epic context).
3. **Per task, after DONE.** Call `extract_project_knowledge` with the completion envelope(s), the `kb_index`, optional `current_kb_excerpts` for notes likely to be edited, and the `epic_permalink`. It returns `proposals` (and `bm_commands` when configured) describing new or updated notes.
4. **Apply.** A human (or the controller, gated by the ladder below) reviews the proposals and pastes the `bm_commands` to apply them.

### Five note types

| Type | Layer | Body |
|---|---|---|
| `decision` | durable | ADR-style; append-only; new decisions supersede old ones |
| `module` | durable | internal structural notes (purpose, invariants, conventions, touch-points) |
| `feature` | durable | user-facing capability catalog with release-tagged change pointers |
| `glossary` | durable | canonical domain-term definitions |
| `epic` | epic-scoped | charter + scope + acceptance + progress ledger; closed at epic-done |

Templates live in [`examples/project-knowledge/`](examples/project-knowledge/) — copy one into your shared KB, fill it in, and start linking. See [`examples/project-knowledge/README.md`](examples/project-knowledge/README.md) for the layering and maintenance-ownership conventions.

### The `project_knowledge` field

`validate_task_spec` and `validate_plan` accept an optional `project_knowledge` string (plain text; markdown is fine). The reviewer treats it as **authoritative** — same posture as `pinned_by` — so stated facts are not flagged as `unverifiable_codebase_claim`. The field counts against the existing 200 KB payload cap; keep it under ~16 KB per call in practice (the prime call's picks are what keep it bounded).

`check_progress` and `validate_completion` deliberately do **not** accept `project_knowledge` (spec §3.3). The field is session-context-only, never persisted: KB content can change during a task's session lifetime, and a snapshot stored at `validate_task_spec` time would silently drift. Field evidence can drive a follow-up minor bump if completion-time KB grounding turns out to be load-bearing.

### Auto-apply ladder for extract proposals

Recommended default disposition (the server doesn't enforce; teams can override):

| Proposal kind | Default disposition |
|---|---|
| `epic` progress-ledger append | Auto-apply |
| `feature` "Recent material changes" append | Auto-apply |
| `decision` create with `status: proposed` | Auto-apply (draft for humans to review) |
| `decision` create with `status: accepted` | **Human review** |
| `decision` supersede | **Human review** |
| `module` invariant/convention edit | **Human review** |
| `glossary` create | Auto-apply |
| Anything with `contradicts_existing` finding | **Human review, blocking** |

### Anchored Basic Memory tool names

When `ANTI_TANGENT_KB_STORE=basic-memory`, prime and extract emit `bm_commands` arrays that reference these canonical Basic Memory tool names (verified against BM v0.21.1 on 2026-05-20):

- `search_notes` — search across notes by query string.
- `read_note` — read a note by permalink.
- `write_note` — create or replace a note.
- `edit_note` — partial update (frontmatter patch / append / replace section).
- `move_note` — rename / relocate a note.
- `delete_note` — remove a note.

**Supersede mapping.** Basic Memory does **NOT** ship a `supersede_note` verb. A logical `Proposal{action: "supersede"}` therefore maps to a **pair** of `bm_commands` entries: (1) `write_note` to create the new note with `frontmatter.status: accepted` and `supersedes: [<predecessor>]`, then (2) `edit_note` to flip the predecessor's `frontmatter.status` to `superseded`. The prompts and goldens reference these names verbatim — a future BM rename is a doc + prompt-template change only.

For the operator-side topology of running BM as a shared service across a team, see [`docs/team-setup/basic-memory-shared-vm.md`](docs/team-setup/basic-memory-shared-vm.md).

### Environment variables

- `ANTI_TANGENT_KB_STORE` — default `""` (off; `bm_commands` arrays are omitted from prime/extract outputs). Set to `basic-memory` to enable `bm_commands` arrays. Any other non-empty value is rejected at startup with a configuration error.
- `ANTI_TANGENT_PRIME_MODEL` — reviewer for `prime_project_knowledge`. Falls back to `ANTI_TANGENT_PLAN_MODEL` then `ANTI_TANGENT_PRE_MODEL`.
- `ANTI_TANGENT_EXTRACT_MODEL` — reviewer for `extract_project_knowledge`. Same fallback chain.
- `ANTI_TANGENT_PRIME_MAX_TOKENS` — output cap for prime; default `4096`. Ceiling-clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING`.
- `ANTI_TANGENT_EXTRACT_MAX_TOKENS` — output cap for extract; default `8192`. Ceiling-clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING`.

Existing flows are unaffected when `ANTI_TANGENT_KB_STORE` is empty and `project_knowledge` is unset — that's the backward-compat guarantee.

---

## 5. For controllers — plan-handoff gate + dispatch addendum

If you orchestrate implementer subagents — superpowers' `subagent-driven-development`, hone-ai's equivalent, or a hand-rolled dispatch loop — you have **two** responsibilities that the implementer can't cover on its own.

### 5.1 Plan-handoff gate (REQUIRED before any dispatch)

When you are about to execute a multi-task plan — whether you do the work yourself or dispatch each task to a subagent — **first call `validate_plan` once with the full plan markdown**, before any implementation work begins.

**Procedure:**

1. Call `validate_plan` once with the full plan markdown. Capture the `PlanResult`.
2. **Surface results to the user.** Show `plan_verdict`, plan-level findings, and per-task verdicts/findings. For any task whose `suggested_header_block` is non-empty, show the proposed header and ask the human to adopt or revise. If task results include `lightweight_eligible` / `lightweight_reason`, treat them as advisory hints for choosing the full or lightweight dispatch clause.
3. **Apply the proposed header blocks** (the controller may apply automatically when verdicts are `pass`/`warn` and the human approves; always defer to the human for `fail`).
4. If anything material changed (headers added, ACs rewritten), call `validate_plan` again to confirm. Repeat until `plan_verdict: "pass"` (or every `warn` is explicitly justified).
5. **Only proceed to dispatch when the plan-level gate passes.**

The implementing subagent still calls `validate_task_spec` at task start in its own session — see §4. The plan-level gate (`validate_plan`) and the per-task implementer gate (`validate_task_spec`) are two different responsibilities at two different lifecycle moments.

**Why this matters:** catching a vague AC at handoff time costs one `validate_plan` call (~$0.01–$0.02 for a typical plan); catching it after a subagent has spent 10 minutes implementing against a misread of the spec costs a wasted dispatch. The plan-handoff gate is the cheap insurance.

**Skip this gate** when the plan only has one task (just go straight to per-task validation), or when the work item didn't come from a plan at all (see §1).

### 5.2 Dispatch addendum (paste the §4.2 clause into every implementer prompt)

For each task you dispatch to an implementing subagent, paste the §4.2 clause verbatim into that subagent's prompt — subagents do not inherit your CLAUDE.md or any harness-level system prompt. Append it right before the "Report Format" section of your existing dispatch template. Apply only to subagents that will implement a Goal/AC/Non-goals task; skip for read-only research subagents per §1.

### 5.3 DONE-gate (recommended)

After the subagent reports DONE, you may want to require evidence that `validate_completion` was called and returned `pass` (or `warn` with all findings addressed). The simplest way: ask for the verdict + findings JSON in the subagent's DONE report. The MCP server does not enforce this; the prompt does.

### 5.4 Anti-pattern: don't re-validate completion from the controller

Do NOT have the controller call `validate_completion` itself after the subagent reports DONE. The implementer's session was created in its own context — the controller doesn't have the `session_id`, so a fresh `validate_completion` call from the controller would either fail with a `session_not_found` finding (no session to thread) or, if the controller passed an arbitrary id, return spurious findings. Either way it duplicates the post-hook gate the subagent already cleared and adds noise. The subagent's post-hook IS the gate.

(This is different from §5.1, which is `validate_plan` at plan-handoff time before any subagent has started — that's pre-implementation and lives in the controller's own context.)

### 5.5 `validate_plan` vs `validate_task_spec` — when to use which

| Tool | Caller | Lifecycle moment | Returns |
|---|---|---|---|
| `validate_plan` | Controller | Once, before any dispatch | Plan-wide + per-task analysis with ready-to-paste header blocks. Stateless. |
| `validate_task_spec` | Implementing subagent | Once at task start, after dispatch | Per-task structural/quality review. **Creates a session** that the implementer threads through `check_progress` and `validate_completion`. |

The two tools' analyses overlap intentionally: the plan gate catches plan-wide and per-task issues at handoff; the implementer gate catches anything that changed between handoff and dispatch (e.g. another agent edited the plan in the meantime) and produces the session that the rest of the implementer's lifecycle uses.

The `plan_quality` field (v0.3.1+) is a separate axis from `plan_verdict`. While `plan_verdict` answers "is this dispatchable?" (pass / warn / fail), `plan_quality` answers "how close is this to ship-ready?" (rough / actionable / rigorous). When you see consecutive `warn` verdicts that aren't changing, watch `plan_quality` for convergence: `actionable → rigorous` is a meaningful improvement even if the verdict stays `warn`. Use `plan_quality` to decide when to stop iterating: most callers can ship at `actionable` for ASAP work, and at `rigorous` for quarterly-rewrite scope.

### 5.6 Per-call tool args and partial-response handling (v0.3.0+)

**`max_tokens_override`** (all six tools): optional non-negative int. Replaces the configured `PerTaskMaxTokens` / `PlanMaxTokens` for this call. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values are clamped and a `minor` clamp finding is appended. Negative values are rejected with `max_tokens_override must be ≥ 0`. Use when one specific call needs a larger reviewer budget without changing global config.

**`mode`** (`validate_plan` only): optional `"quick"` or `"thorough"` (default `"thorough"`). `"quick"` instructs the reviewer to surface only the most-severe findings — at most 3 per scope — and omit stylistic nits. Useful for small ASAP plans where late rounds surface only polish. Invalid values rejected with `mode must be "quick" or "thorough"`.

**`partial: true`** envelope field: when the reviewer's output was truncated at its `max_tokens` cap but at least one complete finding could be recovered, the response carries `"partial": true` and the synthetic truncation finding is `severity: minor` rather than `major`. The field is `omitempty` — absent in the common case. If partial recovery fails (no complete finding before the cap), the envelope falls back to the legacy single `severity: major` truncation finding with no `partial` field set.

Passing `validate_plan` calls are cached in memory for 3 minutes when the rendered prompt, model, mode, and token budget are identical. Cache hits return `review_ms: 0` and prefix the original `next_action` with `[cached <=3m]`.

### 5.7 Using review-context features

Use `pinned_by` when a terse acceptance criterion is backed by existing tests, docs, commands, or static checks:

```json
{
  "task_title": "Preserve retry behavior",
  "goal": "Change request parsing without changing retry semantics.",
  "acceptance_criteria": ["Existing retry behavior remains unchanged."],
  "pinned_by": [
    "RetryHandlerTest.retries_transient_errors",
    "go test ./internal/retry -run RetryHandler",
    "docs/retry-contract.md"
  ]
}
```

Use `phase: "post"` only to recover a task session after implementation already happened; normal task execution still calls `validate_task_spec` before coding.

Use `controller_verified_references` when the controller has already grep-verified specific file paths, symbols, line anchors, commands, or adjacent patterns and wants to reduce text-only reviewer noise:

```json
{
  "task_title": "Update parser call site",
  "goal": "Wire the new parser option into the existing command path.",
  "acceptance_criteria": ["cmd/import.go passes ParserOptions.Strict through to ParseFile."],
  "controller_verified_references": [
    "cmd/import.go",
    "ParserOptions.Strict",
    "ParseFile"
  ]
}
```

These entries are attestations from the caller. They suppress matching `unverifiable_codebase_claim` findings by substring match only; they do not suppress real contradictions or ambiguity.

Suppression now runs server-side (deterministic) as well as in the reviewer prompt: a CVR-entry substring match against the finding's `evidence` or `criterion` (either direction; 4-code-point floor on CVR entries) suppresses the entire `unverifiable_codebase_claim`. The behavior is independent of reviewer compliance.

---

## 6. FAQ / failure modes

**My implementer is also Claude Sonnet — does this still help?**
Less than if they were different models. Same model + same training data ≈ same blind spots. If you can't run a different provider, at least pick a different family (Sonnet implementer, Opus reviewer; or Sonnet implementer, Haiku for cheap mid-checks plus Opus for post). Different provider is best.

**How do I know my session expired?**
You'll get a finding with `category: session_not_found`. Default TTL is 4h. Re-call `validate_task_spec` to start a new session and continue with the new ID.

**My payload is too big.**
The MCP returns a finding with `category: payload_too_large`. Default cap is 200 KB across `changed_files`, `final_files`, and `final_diff` (the unified-diff body, when present on `validate_completion`). The finding includes a tool-specific suggestion: for `validate_completion`, pass `final_diff` instead of or in addition to `final_files`; for `check_progress`, reduce `changed_files` or split the call. The `ANTI_TANGENT_MAX_PAYLOAD_BYTES` env var controls the cap.

**A `validate_completion` call returned a finding with `category: malformed_evidence`.**
The server's evidence-shape guard rejected your submission before sending it to the reviewer. The `evidence` field names the specific pattern that matched — typically a truncation marker like `(truncated)`, `[truncated]`, `// ... unchanged`, or a placeholder line consisting only of `...`, or empty `Path` entries in `final_files`. Re-submit with full file contents in `final_files` or a complete unified diff in `final_diff`. The rejection is cached for 5 minutes by canonical content hash, so identical re-submissions are short-circuited. **Note:** if your file legitimately contains one of these literal strings (e.g., a test fixture or documentation file), pass a complete unified diff via `final_diff` instead of pasting the file content via `final_files`.

**A hook returned a finding with `category: other` and `criterion: reviewer_response`.**
The reviewer's response was cut off at the output token budget. As of v0.3.0, the server runs truncated responses through a tolerant parser and surfaces any complete findings produced before the cap — look for `"partial": true` on the envelope and a `severity: minor` truncation marker. To get the full response on the next call, either raise `ANTI_TANGENT_PER_TASK_MAX_TOKENS` / `ANTI_TANGENT_PLAN_MAX_TOKENS` globally, or pass `max_tokens_override` (clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING`, default 16384) for that single call. Pre-0.3.0 servers would emit a single `severity: major` truncation finding and discard any partial output.

**A finding has `category: attestation_contradiction` — what is that?**
Emitted when an AC explicitly contradicts a `harness_shape_attestation` entry (see §3.8). NOT severity-floored (unlike `convention_deviation` / `unverifiable_codebase_claim`); the reviewer's chosen severity is preserved.

**`validate_task_spec` is asking for ACs my plan doesn't have.**
That's the spec quality gate working as designed. Either (a) add the missing ACs to the plan and re-validate, or (b) acknowledge the gap in the next `working_on` description so the reviewer knows to expect implementer-discretion choices.

**What if the implementer skips the post-hook?**
Two defenses: the implementer-prompt clause (§4.2) marks post REQUIRED, and the controller can require the post-hook envelope in the subagent's DONE report (see §5.3).

**Does `check_progress` catch failing tests?**
No — the reviewer LLM reasons over text, not execution. Use mid-checks for drift detection (scope creep, untouched ACs, unaddressed prior findings), not for debugging. Run tests separately.

**Cost / latency overhead.**
Roughly 1–2 s and $0.001–$0.02 per call, depending on payload size and model choice. One mandatory `validate_plan` call per plan-handoff, and two mandatory implementer calls per task minimum (pre + post). Use a cheap-fast model for mid-checks and a stronger model for handoff/post.

**Where do I file bugs?**
[`https://github.com/patiently/anti-tangent-mcp/issues`](https://github.com/patiently/anti-tangent-mcp/issues).
