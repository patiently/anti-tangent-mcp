# Integrating anti-tangent-mcp

`anti-tangent-mcp` is an advisory MCP server that helps prevent implementing-subagent drift by reviewing the task spec and the in-progress work at three lifecycle points (pre / mid / post). The reviewer LLM is intentionally a different model from the implementer, so reviews are not blind to the implementer's blind spots. See [`README.md`](README.md) for the tool surface and [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md) for the authoritative design.

This document tells you how to wire the server into your workflow. It has two real audiences:

- **Plan authors** — get a recommended task format that maps directly to `validate_task_spec` inputs (one-time read while drafting).
- **Implementing subagents** — get a paste-in lifecycle clause that mandates pre + post calls, recommends mid calls, and tells them how to handle findings.

There is also a short addendum for **controllers** who orchestrate subagent dispatches (e.g. superpowers' `subagent-driven-development`, hone-ai's equivalent, or hand-rolled loops).

The integration is **system-agnostic**: it works with superpowers, hone-ai, vanilla Claude Code with a project-level `CLAUDE.md`, Cursor, or any harness that supports MCP servers. It ships as a single markdown document; you paste the relevant chunks where they need to go.

> **When does anti-tangent-mcp earn its keep?** Its value compounds when (a) tasks are specced before being implemented, (b) the implementer is an LLM that can drift, and (c) the implementer LLM differs from the reviewer LLM. Without all three, anti-tangent is just extra latency.

---

## 1. Setup

### 1.1 Install the binary

Pick one:

```bash
# Option A: build from source
go install github.com/patiently/anti-tangent-mcp@latest
```

```bash
# Option B: download a prebuilt binary
# https://github.com/patiently/anti-tangent-mcp/releases
```

```bash
# Option C: container image
docker pull ghcr.io/patiently/anti-tangent-mcp:latest
```

### 1.2 Register the MCP server with your harness

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

Other harnesses (Cursor, Continue, Zed, custom) accept the same `command` + `env` shape — adapt to their config file.

### 1.3 Provider keys

Set at least one of `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`. Only providers with a key set are activated; calling a missing-keyed provider returns a clear error from the tool.

### 1.4 Pick a model split (the load-bearing decision)

The reviewer LLM should not be the same model as the implementer. Same model + same training data ≈ same blind spots, which defeats the point. Recommended starter config:

| If your implementer is… | Set `ANTI_TANGENT_*_MODEL` to… |
|---|---|
| Anthropic Claude (Sonnet/Opus) | `openai:gpt-5` and/or `google:gemini-2.5-pro` |
| OpenAI GPT-5 family | `anthropic:claude-sonnet-4-6` and/or `google:gemini-2.5-pro` |
| Google Gemini | `anthropic:claude-sonnet-4-6` and/or `openai:gpt-5` |

The mid-hook (`check_progress`) is called more often, so use a cheaper fast model:

```dotenv
ANTI_TANGENT_PRE_MODEL=openai:gpt-5
ANTI_TANGENT_MID_MODEL=openai:gpt-5-mini
ANTI_TANGENT_POST_MODEL=openai:gpt-5
```

Or mix providers across hooks if you have multiple keys.

### 1.5 Smoke test

Launch your harness with debug logging on and confirm the three tools (`validate_task_spec`, `check_progress`, `validate_completion`) appear in the discovered tool catalog. If the server fails at startup, it prints the configuration error to stderr.

---

## 2. For plan authors — the anti-tangent-friendly task format

When you write a plan, give each task a small structured header block. The implementing subagent will pass these fields verbatim into `validate_task_spec`, and the reviewer LLM uses them to decide whether the spec is implementable as written.

### 2.1 The required shape

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

### 2.2 Worked example

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

### 2.3 What `validate_task_spec` actually checks

- **Structural completeness.** Is the goal stated? Are there acceptance criteria? Are non-goals declared where they help bound scope?
- **Acceptance-criterion quality.** Is each AC testable, specific, and unambiguous? For any vague AC, the reviewer suggests a concrete rewrite.
- **Implicit assumptions.** Each assumption a fresh implementer would have to make becomes a finding, so the spec author can either pin it down or explicitly mark it as implementer's discretion.

### 2.4 Mapping to existing plan-writers

- **superpowers `writing-plans`:** add the Goal/AC/Non-goals/Context block to the top of each `### Task N:` section. The skill's existing "Files:" / "Steps:" structure stays untouched below.
- **hone-ai:** same pattern — header block above the task body.
- **Vanilla CLAUDE.md plans:** same pattern.

### 2.5 Anti-pattern: keep implementation steps OUT of the AC list

Acceptance criteria describe *what done looks like*, not *how to get there*. Implementation steps belong in the "Steps:" / "Files:" portion of the task, where they always lived. Mixing them produces brittle ACs that the reviewer flags as either redundant or hyper-specific.

---

## 3. For implementers — the lifecycle protocol

If you're an implementing subagent (or you're writing a system prompt for one), this section is what to follow.

### 3.1 Protocol summary

| Phase | Tool | Required? | When to call |
|---|---|---|---|
| Start | `validate_task_spec` | **Yes** | Once, before writing any code |
| During | `check_progress` | Recommended | After each meaningful chunk; at any moment of uncertainty |
| End | `validate_completion` | **Yes** | Before reporting DONE |

One task = one session = one subagent. The session_id returned by `validate_task_spec` lives in the implementer's context for the lifetime of the task; it is not handed off to anyone else.

### 3.2 The implementer-prompt clause (paste this into every dispatch)

```markdown
## Drift-protection protocol (anti-tangent-mcp)

Before, during, and after this task, you must use the `validate_task_spec`,
`check_progress`, and `validate_completion` MCP tools.

**1. At the start (REQUIRED).** Before writing any code, call
`validate_task_spec` with the structured task fields below. Save the
returned `session_id` — you'll thread it through subsequent calls.
- Read the findings list. Treat `severity: critical` as blocking and
  `severity: major` as "address or explain." If the spec is too ambiguous
  to proceed, stop and ask the controller for clarification rather than
  guessing.

**2. During work (RECOMMENDED).** After each meaningful change (a new
file, a non-trivial logic block, finishing one acceptance criterion),
call `check_progress` with: the session_id, a one-sentence `working_on`
summary, and the changed files. Address findings before continuing.

**3. Before reporting DONE (REQUIRED).** Call `validate_completion` with
the session_id, your summary, the final files, and any test evidence.
If the verdict is `fail` or contains `critical`/`major` findings, do
not report DONE — fix the findings and re-validate.

## Task spec (pass these fields verbatim to validate_task_spec)

- task_title:           <from the task block>
- goal:                 <from "Goal:">
- acceptance_criteria:  <from "Acceptance criteria:" bullets>
- non_goals:            <from "Non-goals:" bullets if present>
- context:              <from "Context:" if present>
```

### 3.3 How to address findings

**Address vs. push back.** Reviewer LLMs can be wrong. If a finding misreads the code, document the disagreement in the next call's `working_on` field — for example, `working_on: "addressed all findings except F#3 which is incorrect because the helper does in fact perform the length check, see handlers.go line 42"` — and re-validate. Don't silently ignore: the next reviewer call won't see your reasoning unless you write it.

**The retry loop.** Parse failures on the reviewer's response are handled inside the server (one retry with a JSON-only reminder). The implementer does not need to handle that.

**Session not found.** If `check_progress` or `validate_completion` returns a finding with `category: session_not_found`, the session expired (default TTL 4h) or was never created. Call `validate_task_spec` again to start a fresh session and continue with the new ID.

### 3.4 Concrete examples

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

Final call with `summary: "Implemented /healthz returning ok"` and the final file. Response:

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
  "next_action": "Add the benchmark and re-validate; do not report DONE."
}
```

The implementer adds the benchmark, re-runs `validate_completion` with the new test evidence, gets `verdict: "pass"`, and reports DONE.

---

<!-- Sections 4–5 added in subsequent commits -->
