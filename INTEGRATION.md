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

## 3. For plan authors — the anti-tangent-friendly task format

Give each task a small structured header block. The implementing subagent passes these fields verbatim into `validate_task_spec`; the reviewer uses them to decide whether the spec is implementable as written.

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

When a task pastes verbatim test code the implementer must land as written, wrap each test body in a fenced block immediately under a literal `**NORMATIVE TEST BODIES (verbatim):**` header. `validate_plan` extracts each fence server-side and threads the list into the per-task `validate_task_spec` `normative_test_bodies` input; the reviewer treats each entry as binding scope. Adjacent fences extract as separate entries. Bodies > 4000 Unicode code points are server-truncated with a `// truncated` marker; for legitimately longer bodies, paraphrase or excerpt and prefix with `// excerpt:` so the reviewer treats it as partial coverage.

### 3.7 `.trimIndent()` raw-string caveat

When a plan snippet is wrapped in `.trimIndent()` (or any equivalent raw-string trim), multi-line source phrases render newlines exactly where they sit in the markdown — anti-tangent reads the source, not the rendered output. Keep example strings on a single source line, and phrase ACs against the rendered string (e.g. "output contains `please decline politely`"), not against source layout.

### 3.8 Harness shape attestations (v0.5.2+)

`harness_shape_attestation` is a structured optional input on `validate_task_spec`. Each entry is `{harness: string, path: string, assertions: []string}`. Use it when ACs depend on a test harness's stated capabilities (or non-capabilities). The reviewer treats each attestation as authoritative caller-attested context (no independent verification) and flags ACs that EXPLICITLY contradict an entry — e.g. an AC asking for behavior a `does not …` assertion forbids, or asserting a state directly contradicting a positive assertion — as `attestation_contradiction` findings. Absence of a capability is NOT a contradiction; do not list things to forbid them.

---

## 4. For implementers — the lifecycle protocol

> **Lightweight eligibility first.** Many tasks qualify for lightweight mode (skip `validate_task_spec` and `check_progress`; keep `validate_completion` as the sanity gate). See [Lightweight protocol mode](#lightweight-protocol-mode-v031) below for criteria and reference clause.

| Phase | Tool | Required? | When to call |
|---|---|---|---|
| Start | `validate_task_spec` | **Yes** | Once, before writing any code |
| During | `check_progress` | Optional (advisory; low-signal in field data) | When you suspect drift, a test that 'should' fail doesn't, or you've spent >5 min on behavior the spec leaves under-specified |
| End | `validate_completion` | **Yes** | Before reporting DONE |

One task = one session = one subagent. The `session_id` returned by `validate_task_spec` lives in the implementer's context for the lifetime of the task.

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

If a `severity: major` pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.

**Short variant** — for agents that already carry the full clause in their system prompt:

````markdown
## Drift protection

Use anti-tangent per the standard dispatch protocol. For this task:
- Call `validate_task_spec` before edits unless `lightweight_eligible: true` is set by the controller.
- Call `validate_completion` before DONE and paste its `summary_block`.
- If CodeScene MCP is configured, run `pre_commit_code_health_safeguard` after meaningful code changes.
- If any major pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.
- If a Project knowledge section is auto-attached, read it before validate_task_spec and pass it verbatim as project_knowledge.
````

**Language-scoping prose caveat.** Reviewers can surface `ambiguous_spec` findings around closure/scoping semantics (Kotlin `var` captured by a lambda, Python `nonlocal`, JS `let`/`const` in arrow bodies) when the prose AC reads ambiguously even though the verbatim code block in the plan is unambiguous. Trust the verbatim plan code; only deviate if the *tests* disagree with the prose. If you can't reconcile code and prose, ask the controller.

### Lightweight protocol mode (v0.3.1+)

For trivial tasks — doc-only edits, single-file mechanical relocations, dependency bumps — the full clause is overhead-heavy. Controllers may dispatch a **lightweight clause**: skip `validate_task_spec`, skip `check_progress`, keep `validate_completion` as the sanity gate (its handler accepts an empty `session_id` when at least one of `final_files` / `final_diff` / `test_evidence` is non-empty).

Use lightweight mode when ALL of: (a) ≤ 2 files or docs/config/data-only; (b) mechanical (no new logic, no test-design choices); (c) the spec includes literal text, exact diff, exact command, or exact insertion shape. `validate_plan`'s `lightweight_eligible` / `lightweight_reason` hints are advisory, not permission to skip judgment.

Use the full protocol for: new production logic, test-design choices, or ACs requiring observable invariants. Reference lightweight dispatch clause: `examples/lightweight-dispatch.md`.

### CodeScene MCP companion (optional)

The recommended pairing for anti-tangent's text-only blind spot (see `## Scope and limits`) is the open-source [CodeScene MCP server](https://github.com/codescene-oss/codescene-mcp-server), which exposes deterministic Code Health analysis as MCP tools. The two are complementary: anti-tangent reasons over plan text via an LLM reviewer; CodeScene reasons over the actual files on disk via static analysis.

**Tool-to-phase mapping.** When CodeScene MCP is configured alongside anti-tangent, instruct implementers to also call:

- Mid-task: `pre_commit_code_health_safeguard` after meaningful changes (uncommitted/staged files only; deterministic and fast). High-signal, unlike anti-tangent's `check_progress`.
- Before DONE (alongside `validate_completion`): `analyze_change_set` for the full branch-vs-base view. If the Code Health delta is negative, surface it in the DONE summary and consider iterating.
- Drill-down on a flagged issue: `code_health_review`.

Anti-tangent never enforces CodeScene findings server-side; the integration lives at the dispatch-clause layer. If CodeScene MCP isn't configured, the companion calls are skipped. Lightweight-protocol tasks (doc-only / mechanical) skip all CodeScene calls too.

### 4.3 How to address findings

**Address vs. push back.** Reviewer LLMs can be wrong. If a finding misreads the code, document the disagreement in the next call's `working_on` field — e.g. `working_on: "addressed all findings except F#3 which is incorrect because the helper does perform the length check, see handlers.go line 42"` — and re-validate. Don't silently ignore: the next reviewer call won't see your reasoning unless you write it.

**The retry loop.** Parse failures on the reviewer's response are handled inside the server (one retry with a JSON-only reminder); the implementer does not handle that.

**Session not found.** A `category: session_not_found` finding means the session expired (default TTL 4h) or was never created. Call `validate_task_spec` again to start a fresh session and continue with the new ID.

---

## Project knowledge (optional)

An optional v0.6.0+ loop that grounds the reviewer in **what's already true about your project** — decisions, module invariants, feature surfaces, glossary terms, epic progress. Earns its keep on epic-scale projects with multiple agents and multiple authors where each task validates cleanly but the pieces stop composing into a working end product. Skip on single-author or short-lived projects.

Two new MCP tools — `prime_project_knowledge` (pre-task; recommends notes to read) and `extract_project_knowledge` (post-task; proposes notes to write) — plus a `project_knowledge` field on `validate_task_spec` and `validate_plan`. Knowledge lives in [Basic Memory](https://github.com/basicmachines-co/basic-memory) (recommended) or any markdown-backed store; anti-tangent has **zero code dependency** on Basic Memory.

Architecture diagram and component boundaries: see [project-knowledge design spec §1](docs/superpowers/specs/2026-05-18-project-knowledge-design.md#1-architecture--boundaries).

### Controller workflow (per epic)

The server is stateless; the controller's dispatch logic ties prime → implement → extract together.

1. **Before dispatch.** Search the KB by task terms + epic's `touches_modules` / `relates` → `kb_index`. Call `prime_project_knowledge` with task fields + `kb_index` + `epic_permalink`; it returns `picks` (and `bm_commands` when `ANTI_TANGENT_KB_STORE=basic-memory`). Read the picked notes into a `kb_excerpts` markdown string.
2. **Dispatch.** Include `kb_excerpts` in the implementer's brief AND pass it verbatim as `project_knowledge` into `validate_task_spec`. The subagent makes no prime/extract calls.
3. **After DONE.** Call `extract_project_knowledge` with the completion envelope(s), `kb_index`, optional `current_kb_excerpts`, and `epic_permalink`. Returns `proposals` (and `bm_commands` when configured).
4. **Apply.** A human (or the controller, gated by the ladder below) reviews proposals and pastes the `bm_commands` — see the "Applying bm_commands to BM v0.21.1" subsection immediately below for the translation steps **before** you paste.

### Applying bm_commands to BM v0.21.1

Anti-tangent's `bm_commands` arrays are paste-ready *conceptual* shape — the tool names match BM verbatim, but the arg shapes track the spec's logical model rather than each BM release's literal signature (the explicit non-goal: don't couple anti-tangent to BM's per-release API churn). Field-tested against BM v0.21.1 on 2026-05-21, three small translation steps land between paste and apply.

**`write_note` arg mapping** (extract's `Proposal{action: "create"}` and supersede-leg-1):

| Extract emits | BM v0.21.1 takes | Mapping |
|---|---|---|
| `permalink: "<dir>/<slug>"` | `directory` + `title` | Split on the last `/`; prefix is `directory`. Pass `proposal.title` directly as `title` rather than slug-back-to-title. |
| `frontmatter: {…}` | `metadata: {…}` | Verbatim — BM merges into the YAML frontmatter at the top of the file. |
| `body: "…"` | `content: "…"` | Verbatim. |
| `proposal.type` | `note_type` | E.g. `"decision"`, `"epic"`, `"feature"`, `"module"`, `"glossary"`. |

**`edit_note` operation hints.** BM v0.21.1's `edit_note` requires an explicit `operation` enum that extract does not emit; the agent picks based on the target note's structure:

- Ledger / "Recent material changes" appends — `insert_before_section` keyed on the section AFTER your target (puts the new entry at the bottom of the target section without clobbering).
- Supersede-leg-2 (flipping a predecessor's `status` to `superseded`) — `find_replace` against the frontmatter line, or BM's frontmatter-patch verb if available in your version.
- Replacing a whole section's body — `replace_section`.
- Appending to the very end of the note (no section anchor) — `append`.

**Permalink-slug expectations.** BM auto-derives the stored slug from `title` (lowercased, hyphenated), so the permalink extract proposes (e.g. `<PROJECT>/decisions/0042-docker-bm-deployment-is-alternative`) diverges from what BM stores. Cross-links (`epic_origin`, etc.) then won't resolve. Cleanest fix: a **three-step pattern** — `write_note` to create, `move_note` to the canonical path, `edit_note(find_replace)` to rewrite the YAML `permalink:` line. Step 3 is load-bearing; steps 1+2 alone leave wikilinks broken.

**Worked example.** See [`plugin/bm-scribe/docs/three-step-pattern.md`](plugin/bm-scribe/docs/three-step-pattern.md) for a literal end-to-end example showing `write_note → move_note → read_note → edit_note(find_replace)` with annotated BM responses at each step. The `plugin/bm-scribe/` plugin shipped from this repo encodes this pattern across every creator skill.

### Eight note types in three groups

| Type | Layer | Body |
|---|---|---|
| `decision` | durable | ADR-style; append-only; new decisions supersede old ones |
| `module` | durable | coherent capabilities (user-facing surface), not 1:1 Go packages |
| `feature` | durable | user-facing capability catalog with release-tagged change pointers |
| `glossary` | durable | canonical domain-term definitions |
| `howto` | durable | operational runbook; slug key; update-in-place; `status: active`/`deprecated` (v0.9.0+) |
| `epic` | operational | live dashboard: charter, stories table, open PRs, acceptance checklist, progress ledger |
| `story` | operational | live dashboard: brief, multi-PR table, subtasks, deployment state, decisions produced (v0.7.0+) |
| `gotcha` | lessons-learned | module-scoped lesson learned; ADR-numbered slug; supersede chain (v0.8.0+) |

Templates: [`examples/project-knowledge/`](examples/project-knowledge/); frozen real examples: [`examples/project-knowledge/dogfood/`](examples/project-knowledge/dogfood/). Per-project tuning: [`docs/team-setup/project-knowledge-conventions.md`](docs/team-setup/project-knowledge-conventions.md).

### v0.7.0 canonical layout

Permalinks follow `<PROJECT>/<type>/<key>/main`. Type folders are **plural** (`epics`, `stories`, `decisions`, `modules`, `features`, `glossary`, `gotchas`, `howtos`); `<key>` is a `<TICKET-ID>` for epics/stories, a `<NNNN>-<slug>` (ADR-numbered) for decisions and gotchas, a `<slug>` for modules/features/howtos, and a `<term>` for glossary. Example: `monorepo/decisions/0001-text-only-reviewer/main`. The `plugin/bm-scribe/` plugin (v0.7.1+) auto-picks ADR numbers and enforces this layout.

### The `project_knowledge` field

`validate_task_spec` and `validate_plan` accept an optional `project_knowledge` string (markdown ok). The reviewer treats it as **authoritative** — same posture as `pinned_by` — so stated facts are not flagged as `unverifiable_codebase_claim`. Counts against the 200 KB payload cap; keep under ~16 KB per call (prime's picks keep it bounded).

`check_progress` and `validate_completion` deliberately do **not** accept `project_knowledge` (spec §3.3): the field is session-context-only, never persisted, because KB content can change during a task's session and a snapshot taken at `validate_task_spec` time would silently drift.

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
| `howto` create | **Human review** |
| `howto` update | **Human review** |
| Anything with `contradicts_existing` finding | **Human review, blocking** |

### Anchored Basic Memory tool names

When `ANTI_TANGENT_KB_STORE=basic-memory`, prime and extract emit `bm_commands` arrays referencing canonical BM tool names (`search_notes`, `read_note`, `write_note`, `edit_note`, `move_note`, `delete_note`). BM has no `supersede_note` verb — a `Proposal{action: "supersede"}` maps to `write_note` (new note with `status: accepted`, `supersedes: [<predecessor>]`) plus `edit_note` flipping the predecessor's `status` to `superseded`. Full contract: [Basic Memory contract block](docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md#basic-memory-contract-verified-yyyy-mm-dd) at the bottom of the v0.6.0 plan.

For the operator-side topology of running BM as a shared service across a team, see [`docs/team-setup/basic-memory-shared-vm.md`](docs/team-setup/basic-memory-shared-vm.md) — covers a dedicated VM via stdio-over-SSH and a Docker container via SSE behind a reverse proxy.

### Environment variables

Defaults shown; see [`README.md`](README.md) for the full dotenv block.

- `ANTI_TANGENT_KB_STORE` — `""` (off). Set to `basic-memory` to enable `bm_commands` arrays in prime/extract outputs. Any other non-empty value is rejected at startup.
- `ANTI_TANGENT_PRIME_MODEL` — reviewer for `prime_project_knowledge`. Falls back to `ANTI_TANGENT_PLAN_MODEL` then `ANTI_TANGENT_PRE_MODEL`.
- `ANTI_TANGENT_EXTRACT_MODEL` — reviewer for `extract_project_knowledge`. Same fallback chain.
- `ANTI_TANGENT_PRIME_MAX_TOKENS` — output cap for prime; default `4096`. Ceiling-clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING`.
- `ANTI_TANGENT_EXTRACT_MAX_TOKENS` — output cap for extract; default `8192`. Ceiling-clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING`.

Existing flows are unaffected when both `ANTI_TANGENT_KB_STORE` and `project_knowledge` are unset (backward-compat guarantee).

---

## 5. For controllers — plan-handoff gate + dispatch addendum

Controllers (superpowers' `subagent-driven-development`, hone-ai's equivalent, or a hand-rolled loop) have **two** responsibilities the implementer can't cover.

### 5.1 Plan-handoff gate (REQUIRED before any dispatch)

Before executing a multi-task plan — whether you implement it yourself or dispatch to subagents — **call `validate_plan` once with the full plan markdown** first.

**Procedure:**

1. Call `validate_plan` with the full plan markdown. Capture the `PlanResult`.
2. **Surface results to the user.** Show `plan_verdict`, plan-level findings, and per-task verdicts/findings. For any task whose `suggested_header_block` is non-empty, show the proposed header and ask the human to adopt or revise. If task results include `lightweight_eligible` / `lightweight_reason`, treat them as advisory hints.
3. **Apply the proposed header blocks** (the controller may apply automatically when verdicts are `pass`/`warn` and the human approves; defer to the human for `fail`).
4. If anything material changed, call `validate_plan` again. Repeat until `plan_verdict: "pass"` (or every `warn` is explicitly justified).
5. **Only proceed to dispatch when the plan-level gate passes.**

The implementing subagent still calls `validate_task_spec` at task start in its own session — see §4. The plan-level gate and the per-task implementer gate are two different responsibilities at two different moments.

**Why this matters:** catching a vague AC at handoff costs one `validate_plan` call (~$0.01–$0.02); catching it after a subagent spent 10 minutes against a misread spec costs a wasted dispatch.

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

**`max_tokens_override`** (all six tools): optional non-negative int. Replaces `PerTaskMaxTokens` / `PlanMaxTokens` for this call. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values are clamped and a `minor` finding appended. Negative values rejected with `max_tokens_override must be ≥ 0`.

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
