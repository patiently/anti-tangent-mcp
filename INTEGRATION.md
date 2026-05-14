# Integrating anti-tangent-mcp

`anti-tangent-mcp` is an advisory MCP server that helps prevent implementing-subagent drift while working on **tasks from a written implementation plan**. It exposes four tools: a plan-level handoff gate (`validate_plan`) and three per-task lifecycle hooks (`validate_task_spec` / `check_progress` / `validate_completion`). The reviewer LLM is intentionally a different model from the implementer, so reviews are not blind to the implementer's blind spots. See [`README.md`](README.md) for the tool surface and [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md) for the authoritative design.

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
- Do not paste self-review claims like "all file references were verified" into the plan text — the reviewer cannot confirm such claims and will flag them as `unverifiable_codebase_claim`.
- State commit-policy carve-outs literally in the plan text. The reviewer reads only `plan_text`, not repo-level policy files.
- For doc deliverables, submit full content via `final_files`; diffs or prose summaries are often insufficient evidence.

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

## 2. Setup

### 2.1 Install the binary

Pick one:

```bash
# Option A: build from source
go install github.com/patiently/anti-tangent-mcp/cmd/anti-tangent-mcp@latest
```

```bash
# Option B: download a prebuilt binary
# https://github.com/patiently/anti-tangent-mcp/releases
```

```bash
# Option C: container image
docker pull ghcr.io/patiently/anti-tangent-mcp:latest
```

### 2.2 Register the MCP server with your harness

Claude Code (`.mcp.json`):

```json
{
  "mcpServers": {
    "anti-tangent": {
      "command": "anti-tangent-mcp",
      "env": {
        "ANTHROPIC_API_KEY": "sk-ant-...",
        "OPENAI_API_KEY":    "sk-..."
      }
    }
  }
}
```

opencode (`~/.config/opencode/opencode.json`):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "anti-tangent": {
      "type": "local",
      "command": ["/absolute/path/to/anti-tangent-mcp"],
      "environment": {
        "ANTHROPIC_API_KEY": "sk-ant-...",
        "GOOGLE_API_KEY":    "...",
        "ANTI_TANGENT_PRE_MODEL":  "google:gemini-3.1-pro-preview",
        "ANTI_TANGENT_MID_MODEL":  "google:gemini-3.1-flash-lite",
        "ANTI_TANGENT_POST_MODEL": "google:gemini-3.1-pro-preview",
        "ANTI_TANGENT_PLAN_MODEL": "google:gemini-3.1-pro-preview"
      }
    }
  }
}
```

opencode-specific notes: `command` is an array (not a string), the env block is named `environment` (not `env`), and the binary path must be absolute — opencode does not consult `$PATH`. To make the protocol guidance from `~/.claude/anti-tangent.md` (or your equivalent) load into opencode sessions, add it to the top-level `instructions` array:

```json
{
  "instructions": ["/home/you/.claude/anti-tangent.md"]
}
```

Other harnesses (Cursor, Continue, Zed, custom) accept the same `command` + env-map shape — adapt to their config file.

### 2.3 Provider keys

Set at least one of `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`. Only providers with a key set are activated; calling a missing-keyed provider returns a clear error from the tool.

### 2.4 Pick a model split (the load-bearing decision)

The reviewer LLM should not be the same model as the implementer. Same model + same training data ≈ same blind spots, which defeats the point. Recommended starter config:

| If your implementer is… | Set `ANTI_TANGENT_*_MODEL` to… |
|---|---|
| Anthropic Claude (Sonnet/Opus) | `openai:gpt-5` and/or `google:gemini-3.1-pro-preview` |
| OpenAI GPT-5 family | `anthropic:claude-sonnet-4-6` and/or `google:gemini-3.1-pro-preview` |
| Google Gemini | `anthropic:claude-sonnet-4-6` and/or `openai:gpt-5` |

The mid-hook (`check_progress`) is called more often, so use a cheaper fast model. The plan-level hook (`validate_plan`) reasons over the whole plan in one shot — give it a strong tier:

```dotenv
ANTI_TANGENT_PRE_MODEL=openai:gpt-5
ANTI_TANGENT_MID_MODEL=openai:gpt-5-mini
ANTI_TANGENT_POST_MODEL=openai:gpt-5
ANTI_TANGENT_PLAN_MODEL=openai:gpt-5    # optional; defaults to ANTI_TANGENT_PRE_MODEL
```

`ANTI_TANGENT_PLAN_MODEL` falls back to `ANTI_TANGENT_PRE_MODEL` if unset, so single-tier users keep working without changes. Or mix providers across hooks if you have multiple keys.

### Output budgets and chunking for `validate_plan` (v0.1.4+)

Four env vars tune output-token budgets, the chunking behavior of `validate_plan`, and the per-call `max_tokens_override` ceiling:

```dotenv
ANTI_TANGENT_PER_TASK_MAX_TOKENS=4096    # default 4096; output cap for validate_task_spec / check_progress / validate_completion; raise if a stateful hook returns a truncation finding
ANTI_TANGENT_PLAN_MAX_TOKENS=4096        # default 4096; output cap per reviewer call in validate_plan (single-call and per-chunk); raise if plan validation returns a truncation finding
ANTI_TANGENT_PLAN_TASKS_PER_CHUNK=8      # default 8; chunking threshold + per-chunk task count
ANTI_TANGENT_MAX_TOKENS_CEILING=16384    # default 16384; max value accepted for per-call max_tokens_override (v0.3.0+)
```

Plans with more than `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` tasks are automatically chunked: one Pass-1 reviewer call for cross-cutting `plan_findings` plus `ceil(n/N)` per-task chunks, each with the full plan as context. The merged response is shape-identical to the single-call path — callers see no difference.

Operator notes:

- The `PER_TASK` name covers all three task-scoped lifecycle hooks (validate_task_spec, check_progress, validate_completion) — each reviews exactly one task.
- `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` doubles as both the chunking threshold (`len(tasks) > N` triggers chunking) and the per-chunk size (chunks of N tasks each). Single knob, single mental model: "above N tasks, batch in groups of N."
- `ANTI_TANGENT_REQUEST_TIMEOUT` (default `180s`) applies **per reviewer call**, not to the whole chunked invocation. A 25-task plan does ~5 sequential calls (worst case `5 × RequestTimeout` wall-clock). MCP clients may have shorter tool-call deadlines; if you hit those, lower `PLAN_TASKS_PER_CHUNK` (more, smaller calls) rather than raising `REQUEST_TIMEOUT`. When a timeout occurs, the error message includes the configured timeout value and the `ANTI_TANGENT_REQUEST_TIMEOUT` env-var name so you can self-diagnose and adjust.
- All four env vars reject `0`, negative, and non-integer values at startup with a clear error.

#### Supported reviewer models

Use `provider:model-id`. The server validates against this allowlist at startup and rejects unknown IDs with a clear error (e.g. `model "gemini-3-pro" not in allowlist for provider "google"`).

| Provider | Model id | Tier |
|---|---|---|
| `anthropic` | `claude-opus-4-7` | heavy |
| `anthropic` | `claude-sonnet-4-6` | balanced |
| `anthropic` | `claude-haiku-4-5-20251001` | fast |
| `openai` | `gpt-5` | heavy |
| `openai` | `gpt-5-mini` | balanced |
| `openai` | `gpt-5-nano` | fast |
| `openai` | `gpt-5.5` | heavy (rolling snapshot) |
| `openai` | `gpt-5.5-2026-04-23` | heavy (pinned) |
| `openai` | `gpt-5.4-mini` | balanced (rolling snapshot) |
| `openai` | `gpt-5.4-mini-2026-03-17` | balanced (pinned) |
| `google` | `gemini-3.1-pro-preview` | heavy |
| `google` | `gemini-3.1-flash-lite` | fast |
| `google` | `gemini-2.5-pro` | heavy |
| `google` | `gemini-2.5-flash` | fast |

To add a new model id, edit [`internal/providers/reviewer.go`](internal/providers/reviewer.go) — it's a one-line change.

### 2.5 Smoke test

Launch your harness with debug logging on and confirm all four tools — `validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion` — appear in the discovered tool catalog. If the server fails at startup, it prints the configuration error to stderr.

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

A common style mistake is a vague AC like `should be fast`. `validate_task_spec` will flag it as `category: ambiguous_spec` with a suggestion to pin the load profile (e.g. the `p95 < 50ms at 100 RPS` rewrite above). Catch this at plan time, not at implementation time.

### 3.3 What `validate_task_spec` actually checks

- **Structural completeness.** Is the goal stated? Are there acceptance criteria? Are non-goals declared where they help bound scope?
- **Acceptance-criterion quality.** Is each AC testable, specific, and unambiguous? For any vague AC, the reviewer suggests a concrete rewrite.
- **Implicit assumptions.** Each assumption a fresh implementer would have to make becomes a finding, so the spec author can either pin it down or explicitly mark it as implementer's discretion.

### 3.4 Mapping to existing plan-writers

- **superpowers `writing-plans`:** add the Goal/AC/Non-goals/Context block to the top of each `### Task N:` section. The skill's existing "Files:" / "Steps:" structure stays untouched below.
- **hone-ai:** same pattern — header block above the task body.
- **Vanilla CLAUDE.md plans:** same pattern.

### 3.5 Anti-pattern: keep implementation steps OUT of the AC list

Acceptance criteria describe *what done looks like*, not *how to get there*. Implementation steps belong in the "Steps:" / "Files:" portion of the task, where they always lived. Mixing them produces brittle ACs that the reviewer flags as either redundant or hyper-specific.

---

## 4. For implementers — the lifecycle protocol

If you're an implementing subagent (or you're writing a system prompt for one), this section is what to follow.

### 4.1 Protocol summary

| Phase | Tool | Required? | When to call |
|---|---|---|---|
| Start | `validate_task_spec` | **Yes** | Once, before writing any code |
| During | `check_progress` | Optional | When you suspect drift mid-task; otherwise skip |
| End | `validate_completion` | **Yes** | Before reporting DONE |

One task = one session = one subagent. The session_id returned by `validate_task_spec` lives in the implementer's context for the lifetime of the task; it is not handed off to anyone else.

(Note: the controller may have separately called `validate_task_spec` against the same task at the plan-handoff gate — see §5.1. That created a different session that's already gone. The implementer always creates its own fresh session at task start.)

#### `check_progress` per-tool note

**Status:** OPTIONAL / advisory (was RECOMMENDED prior to v0.3.1).

Field data from execution-phase usage shows `check_progress` consistently produces low-signal findings — mid-implementation context is inherently ambiguous (tests not yet written, function not yet finished, assertion not yet reached). The fast-model default magnifies the issue. Call it when *you* sense drift mid-task; do not treat it as a mandatory gate. The strong-model `validate_completion` post-impl call is far higher signal for a typical task.

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
you're drifting mid-task. Per the 0.3.1 protocol revision this call is
advisory — most tasks will skip it. When you do call, pass: the
session_id, a one-sentence `working_on` summary, and the changed files.

**2b. CodeScene mid-task check (OPTIONAL — when codescene-mcp is
configured in your host).** Call `pre_commit_code_health_safeguard` to
catch Code Health regressions on uncommitted/staged files. This is
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

## Task spec (pass these fields verbatim to validate_task_spec)

- task_title:           <from the task block>
- goal:                 <from "Goal:">
- acceptance_criteria:  <from "Acceptance criteria:" bullets>
- non_goals:            <from "Non-goals:" bullets if present>
- context:              <from "Context:" if present>
```

### Lightweight protocol mode (v0.3.1+)

For trivial tasks — doc-only edits, single-file mechanical relocations, dependency bumps — the full dispatch clause is overhead-heavy (~50 lines of boilerplate for ~15 lines of actual work). Controllers may use a **lightweight clause** for these tasks:

- **Skip** `validate_task_spec` (the spec is fully prescriptive; no design choices for the reviewer to shape).
- **Skip** `check_progress` (already optional in full mode).
- **Keep** `validate_completion` as a sanity gate before reporting DONE. The handler accepts an empty `session_id` when at least one of `final_files` / `final_diff` / `test_evidence` is non-empty.

Use lightweight mode when ALL of: (a) the task touches ≤ 2 files; (b) the task is mechanical (no new logic, no test-design choices); (c) the spec includes the literal text or diff to write.

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

- During mid-task work (when you'd consider `check_progress`): call CodeScene's `pre_commit_code_health_safeguard`. It analyzes only uncommitted/staged files and is fast enough to run after each meaningful change. The field-data rationale for demoting anti-tangent's `check_progress` to OPTIONAL (low-signal mid-task LLM reviews) does NOT apply to CodeScene — its mid-task call is deterministic and high-signal. Many implementations will want to skip `check_progress` and rely on `pre_commit_code_health_safeguard` instead.
- Before reporting DONE (alongside `validate_completion`): call CodeScene's `analyze_change_set` for the full branch-vs-base view. If the Code Health delta is negative or a regression is reported, surface it in the DONE summary and consider iterating — anti-tangent itself remains advisory-only, but the implementer-side judgment call benefits from the codebase-grounded second opinion.
- For drill-down on a flagged issue: `code_health_review`.

**Advisory posture.** Anti-tangent never enforces CodeScene findings server-side. The integration lives at the dispatch-clause / convention layer: a controller that has CodeScene MCP installed updates the dispatch clause to include the companion calls; the implementer cites the findings in its DONE summary. If CodeScene MCP isn't configured in the host, the companion calls are simply skipped — anti-tangent's own protocol is unchanged.

**Lightweight mode.** Tasks dispatched under the lightweight protocol (doc-only edits, mechanical relocations) skip `validate_task_spec`, `check_progress`, and the CodeScene companion calls, while still requiring `validate_completion` as the sanity gate.

**Enabling CodeScene companion tools.** Anti-tangent does not call CodeScene automatically. To make the companion steps available, configure CodeScene MCP in the same MCP host that runs anti-tangent.

Consumer setup checklist:

- Install or run the CodeScene MCP package: `npx -y @codescene/codehealth-mcp`.
- Set `CS_ACCESS_TOKEN` in the environment available to the MCP host.
- Add a CodeScene MCP server entry to your host configuration. The exact file differs by host, but the server entry should be equivalent to:

```json
{
  "mcpServers": {
    "codescene": {
      "command": "npx",
      "args": ["-y", "@codescene/codehealth-mcp"],
      "env": {
        "CS_ACCESS_TOKEN": "${CS_ACCESS_TOKEN}"
      }
    }
  }
}
```

Keep anti-tangent and CodeScene as separate MCP servers. See the [CodeScene MCP installation guide](https://github.com/codescene-oss/codescene-mcp-server#installation) for the upstream host-specific config shapes. Once the server is wired in, the dispatch clause in §4.2 already covers the recommended cadence (Step 2b / Step 3b); `code_health_review` is available as a drill-down when a safeguard returns `degraded`.

### 4.3 How to address findings

**Address vs. push back.** Reviewer LLMs can be wrong. If a finding misreads the code, document the disagreement in the next call's `working_on` field — for example, `working_on: "addressed all findings except F#3 which is incorrect because the helper does in fact perform the length check, see handlers.go line 42"` — and re-validate. Don't silently ignore: the next reviewer call won't see your reasoning unless you write it.

**The retry loop.** Parse failures on the reviewer's response are handled inside the server (one retry with a JSON-only reminder). The implementer does not need to handle that.

**Session not found.** If `check_progress` or `validate_completion` returns a finding with `category: session_not_found`, the session expired (default TTL 4h) or was never created. Call `validate_task_spec` again to start a fresh session and continue with the new ID.

### 4.4 Concrete examples

**Example A — pre-hook surfaces a vague AC.**

Initial call:

```json
{
  "task_title": "Add /healthz endpoint",
  "goal": "Liveness probe for the HTTP server",
  "acceptance_criteria": [
    "Returns 200 OK with body \"ok\"",
    "Should be fast"
  ]
}
```

Response (abridged):

```json
{
  "verdict": "warn",
  "findings": [{
    "severity": "major",
    "category": "ambiguous_spec",
    "criterion": "Should be fast",
    "evidence": "AC #2 lacks a measurable target",
    "suggestion": "Replace with: 'p95 latency < 50 ms at 100 RPS'"
  }],
  "next_action": "Tighten AC #2 and re-validate."
}
```

The implementer surfaces this to the controller, the AC is rewritten, and a fresh `validate_task_spec` call returns `verdict: "pass"`.

**Example B — mid-hook catches scope drift.**

After writing 200 lines, the implementer calls `check_progress` with `working_on: "added Prometheus metrics endpoint"` and the changed files. Response:

```json
{
  "verdict": "warn",
  "findings": [{
    "severity": "major",
    "category": "scope_drift",
    "criterion": "non-goal: 'Authentication on the endpoint'",
    "evidence": "metrics_handler.go line 17 wires the auth middleware",
    "suggestion": "Remove the auth middleware from the new route; metrics handler is out of scope for this task entirely."
  }],
  "next_action": "Revert the auth wiring AND remove the metrics endpoint (out of scope)."
}
```

The implementer rolls back the metrics work and the next mid-check passes.

**Example C — post-hook catches an untouched AC.**

Final call with `summary: "Implemented /healthz returning ok"`, a unified diff in `final_diff`, and test output in `test_evidence` (v0.2.0+: at least one of `final_files`, `final_diff`, or `test_evidence` must be non-empty). Request:

```json
{
  "session_id": "...",
  "summary": "Implemented /healthz returning ok; see diff and test run.",
  "final_diff": "diff --git a/handlers/health.go b/handlers/health.go\n...",
  "test_evidence": "go test ./handlers/... -run TestHealthz\nok handlers 0.012s"
}
```

Response:

```json
{
  "verdict": "fail",
  "findings": [{
    "severity": "critical",
    "category": "missing_acceptance_criterion",
    "criterion": "p95 latency < 50 ms at 100 RPS",
    "evidence": "no benchmark or load test was added",
    "suggestion": "Add a Go benchmark in handlers/health_test.go that runs 100 RPS for 10s and asserts p95 < 50ms; include the result in test_evidence."
  }],
  "next_action": "Add the benchmark and re-validate; do not report DONE.",
  "session_expires_at": "2026-05-12T18:30:00Z",
  "session_ttl_remaining_seconds": 12600
}
```

`session_expires_at` and `session_ttl_remaining_seconds` are included in all stateful-hook responses (v0.2.0+). If the session TTL expires mid-task (default 4h), `check_progress` or `validate_completion` returns a `category: session_not_found` finding — re-call `validate_task_spec` to start a fresh session.

The implementer adds the benchmark, re-runs `validate_completion` with the new test evidence, gets `verdict: "pass"`, and reports DONE.

---

## 5. For controllers — plan-handoff gate + dispatch addendum

If you orchestrate implementer subagents — superpowers' `subagent-driven-development`, hone-ai's equivalent, or a hand-rolled dispatch loop — you have **two** responsibilities that the implementer can't cover on its own.

### 5.1 Plan-handoff gate (REQUIRED before any dispatch)

When you are about to execute a multi-task plan — whether you do the work yourself or dispatch each task to a subagent — **first call `validate_plan` once with the full plan markdown**, before any implementation work begins.

**Procedure:**

1. Call `validate_plan` once with the full plan markdown. Capture the `PlanResult`.
2. **Surface results to the user.** Show `plan_verdict`, plan-level findings, and per-task verdicts/findings. For any task whose `suggested_header_block` is non-empty, show the proposed header and ask the human to adopt or revise.
3. **Apply the proposed header blocks** (the controller may apply automatically when verdicts are `pass`/`warn` and the human approves; always defer to the human for `fail`).
4. If anything material changed (headers added, ACs rewritten), call `validate_plan` again to confirm. Repeat until `plan_verdict: "pass"` (or every `warn` is explicitly justified).
5. **Only proceed to dispatch when the plan-level gate passes.**

The implementing subagent still calls `validate_task_spec` at task start in its own session — see §4. The plan-level gate (`validate_plan`) and the per-task implementer gate (`validate_task_spec`) are two different responsibilities at two different lifecycle moments.

**Why this matters:** catching a vague AC at handoff time costs one `validate_plan` call (~$0.01–$0.02 for a typical plan); catching it after a subagent has spent 10 minutes implementing against a misread of the spec costs a wasted dispatch. The plan-handoff gate is the cheap insurance.

**Skip this gate** when the plan only has one task (just go straight to per-task validation), or when the work item didn't come from a plan at all (see §1).

### 5.2 Dispatch addendum (paste the §4.2 clause into every implementer prompt)

For each task you actually dispatch to an implementing subagent, paste the §4.2 clause into that subagent's prompt verbatim. Subagents do not inherit your CLAUDE.md or any harness-level system prompt — they only see what you put in their dispatch.

> **Append the §4.2 clause to your implementer-subagent prompt template, right before the "Report Format" section.**

Per-skill-system pointers:

- **superpowers:** open `subagent-driven-development/implementer-prompt.md` and paste before the "Report Format" heading.
- **hone-ai:** the equivalent dispatch template file.
- **Vanilla harness:** wherever your dispatch prompt lives (a CLAUDE.md, a system-prompt template, etc.).

Apply this only to subagents that will implement a task with the Goal/AC/Non-goals structure. Skip it for read-only research subagents (Explore, summarizers, code reviewers, security reviews) per §1.

### 5.3 DONE-gate (recommended)

After the subagent reports DONE, you may want to require evidence that `validate_completion` was called and returned `pass` (or `warn` with all findings addressed). The simplest way: ask for the verdict + findings JSON in the subagent's DONE report. The MCP server does not enforce this; the prompt does.

### 5.4 Anti-pattern: don't re-validate completion from the controller

Do NOT have the controller call `validate_completion` itself after the subagent reports DONE. The implementer's session was created in its own context — the controller doesn't have the `session_id`, so a fresh `validate_completion` call from the controller would either fail with a `session_not_found` finding (no session to thread) or, if the controller passed an arbitrary id, return spurious findings. Either way it duplicates the post-hook gate the subagent already cleared and adds noise. The subagent's post-hook IS the gate.

(This is different from §5.1, which is `validate_task_spec` against fresh sessions before any subagent has started — that's pre-implementation and lives in the controller's own context.)

### 5.5 `validate_plan` vs `validate_task_spec` — when to use which

| Tool | Caller | Lifecycle moment | Returns |
|---|---|---|---|
| `validate_plan` | Controller | Once, before any dispatch | Plan-wide + per-task analysis with ready-to-paste header blocks. Stateless. |
| `validate_task_spec` | Implementing subagent | Once at task start, after dispatch | Per-task structural/quality review. **Creates a session** that the implementer threads through `check_progress` and `validate_completion`. |

The two tools' analyses overlap intentionally: the plan gate catches plan-wide and per-task issues at handoff; the implementer gate catches anything that changed between handoff and dispatch (e.g. another agent edited the plan in the meantime) and produces the session that the rest of the implementer's lifecycle uses.

The `plan_quality` field (v0.3.1+) is a separate axis from `plan_verdict`. While `plan_verdict` answers "is this dispatchable?" (pass / warn / fail), `plan_quality` answers "how close is this to ship-ready?" (rough / actionable / rigorous). When you see consecutive `warn` verdicts that aren't changing, watch `plan_quality` for convergence: `actionable → rigorous` is a meaningful improvement even if the verdict stays `warn`. Use `plan_quality` to decide when to stop iterating: most callers can ship at `actionable` for ASAP work, and at `rigorous` for quarterly-rewrite scope.

### 5.6 Per-call tool args (v0.3.0+)

**`max_tokens_override`** (all four tools): optional non-negative int. Replaces the configured `PerTaskMaxTokens` / `PlanMaxTokens` for this call. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values are clamped and a `minor` clamp finding is appended to the envelope. Negative values are rejected with `max_tokens_override must be ≥ 0`. Use when you know a particular call needs a larger reviewer budget without modifying global config — handy when paired with partial-findings recovery on truncated responses.

**`mode`** (`validate_plan` only): optional `"quick"` or `"thorough"` (default `"thorough"`). `"quick"` instructs the reviewer to surface only the most-severe findings — at most 3 per scope (plan-level + each task) — and omit stylistic nits. Useful for small ASAP plans where rounds 5+ surface only polish. Invalid values are rejected with `mode must be "quick" or "thorough"`.

### 5.7 `partial: true` envelope field (v0.3.0+)

When the reviewer's output was truncated at its `max_tokens` cap but at least one complete finding could be recovered, the response envelope (`Result` for per-task tools, `PlanResult` for `validate_plan`) carries `"partial": true` and the synthetic truncation finding is `severity: minor` rather than `major`. The field is `omitempty` — absent in the common (non-truncated) case, so pre-0.3.0 callers continue to work. If partial recovery fails (no complete finding before the cap hit), the envelope falls back to the legacy single `severity: major` truncation finding with no `partial` field set.

### 5.8 Using v0.3.3 review-context features

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

For `validate_plan`, normally omit `max_tokens_override`. v0.3.3 scales the default budget by task count. If a no-analysis truncation response asks for a retry, pass a higher `max_tokens_override` or raise `ANTI_TANGENT_PLAN_MAX_TOKENS` / `ANTI_TANGENT_MAX_TOKENS_CEILING`. Treat `codebase_reference_checklist` as a pre-flight checklist, not as a blocking plan-quality defect by itself.

For `validate_completion`, submit doc/generated deliverables through `final_files`, complete code changes through `final_diff`, and command outputs through `test_evidence`. If the summary names a `.md`, `.txt`, `.json`, `.yaml`, or `.yml` path that is missing from evidence, the reviewer prompt will call that out.

---

## 6. FAQ / failure modes

**What happens if a task fails the plan-handoff gate?**
The controller surfaces the verdict + findings to the user and proposes revisions to the plan. Plan changes land first; only after every task passes (or every `warn` is explicitly justified) does dispatch begin. This catches a vague AC at handoff time — one cheap call — rather than after a subagent has already started writing code against a misread spec.

**What if the reviewer is wrong?**
Findings are advisory. If a finding misreads the code, document the disagreement in the next call's `working_on` field so the next reviewer call sees your reasoning, then re-validate. Don't silently ignore.

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

**`validate_task_spec` is asking for ACs my plan doesn't have.**
That's the spec quality gate working as designed. Either (a) add the missing ACs to the plan and re-validate, or (b) acknowledge the gap in the next `working_on` description so the reviewer knows to expect implementer-discretion choices.

**What if the implementer skips the post-hook?**
Two defenses: the implementer-prompt clause (§4.2) marks post REQUIRED, and the controller can require the post-hook envelope in the subagent's DONE report (see §5.3).

**Does `check_progress` catch failing tests?**
No — the reviewer LLM reasons over text, not execution. Use mid-checks for drift detection (scope creep, untouched ACs, unaddressed prior findings), not for debugging. Run tests separately.

**Cost / latency overhead.**
Roughly 1–2 s and $0.001–$0.02 per call, depending on payload size and model choice. One mandatory `validate_plan` call per plan-handoff, and two mandatory implementer calls per task minimum (pre + post). Use a cheap-fast model for mid-checks and a stronger model for handoff/post.

**Should I use this for ad-hoc code changes outside a plan?**
No. The protocol only fires for tasks with the structured Goal/AC/Non-goals header — see §1 ("When the protocol applies"). Ad-hoc edits, debugging help, code review, and brainstorming all skip the protocol.

**Where do I file bugs?**
[`https://github.com/patiently/anti-tangent-mcp/issues`](https://github.com/patiently/anti-tangent-mcp/issues).
