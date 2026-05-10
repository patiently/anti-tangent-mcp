# anti-tangent-mcp Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `INTEGRATION.md` at the root of the `anti-tangent-mcp` repo per `docs/superpowers/specs/2026-05-08-anti-tangent-integration-design.md`, plus a one-line link from `README.md`.

**Architecture:** Single self-contained markdown document organized by audience: setup → plan authors → implementers (with a load-bearing copy-pasteable lifecycle clause) → controllers → FAQ. No code, no tests, no skill packaging. Users paste relevant chunks into their CLAUDE.md / dispatch-prompt template.

**Tech Stack:** Markdown. Verified with the existing `gofmt`/`go vet`/`go test -race` smoke (the new file does not affect the Go build, but a green CI proves we didn't accidentally break anything).

---

## File map

```text
anti-tangent-mcp/
├── INTEGRATION.md       <-- new, this plan creates it
├── README.md            <-- existing; gets one new "## Integration" linking line
└── docs/superpowers/
    ├── specs/2026-05-08-anti-tangent-integration-design.md   <-- spec we're following
    └── plans/2026-05-08-anti-tangent-integration-plan.md     <-- this file
```

`INTEGRATION.md` lives at the repo root for visibility on GitHub. README gains a brief pointer so first-time visitors know it exists.

---

## Task 1: Scaffold INTEGRATION.md with intro + setup sections

**Files:**
- Create: `INTEGRATION.md`

This task establishes the file and writes Sections 1 (What this is) and 2 (Setup) per the spec. The remaining sections are filled in by Tasks 2–4.

- [ ] **Step 1: Verify spec is on the branch**

Run: `cat docs/superpowers/specs/2026-05-08-anti-tangent-integration-design.md | head -20`
Expected: prints the spec header (`# anti-tangent-mcp Integration — Design`).

- [ ] **Step 2: Create `INTEGRATION.md` with intro + setup sections**

Write to `INTEGRATION.md`:

````markdown
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

<!-- Sections 2–5 added in subsequent commits -->
````

- [ ] **Step 3: Verify markdown renders without obvious lint issues**

Run: `wc -l INTEGRATION.md && grep -c '^##' INTEGRATION.md`
Expected: a non-zero line count and at least 1 H2 heading.

- [ ] **Step 4: Verify the build is unaffected**

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`
Expected: all clean.

- [ ] **Step 5: Commit**

```bash
git add INTEGRATION.md
git commit -m "docs: scaffold INTEGRATION.md (intro + setup)"
```

---

## Task 2: Add Section 2 — For plan authors

**Files:**
- Modify: `INTEGRATION.md` (replace the trailing `<!-- Sections 2–5 added in subsequent commits -->` placeholder with this section, leaving a fresh placeholder beneath it for Tasks 3–4)

- [ ] **Step 1: Append the plan-authors section**

Replace the trailing placeholder line in `INTEGRATION.md` with:

````markdown
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

<!-- Sections 3–5 added in subsequent commits -->
````

- [ ] **Step 2: Sanity check**

Run: `grep -c '^## ' INTEGRATION.md`
Expected: at least 2 (the H2 for Section 1 Setup and the new H2 for Section 2).

Run: `go build ./... && go test -race ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add INTEGRATION.md
git commit -m "docs(integration): plan-author task format section"
```

---

## Task 3: Add Section 3 — For implementers (lifecycle protocol)

**Files:**
- Modify: `INTEGRATION.md` (replace the trailing placeholder)

This is the longest section in the document. It contains the **load-bearing reusable artifact**: the §3.2 prompt clause that controllers paste into every implementer-subagent dispatch.

- [ ] **Step 1: Append the implementers section**

Replace the trailing `<!-- Sections 3–5 added in subsequent commits -->` placeholder with:

````markdown
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
````

- [ ] **Step 2: Sanity check**

Run: `grep -c '^## ' INTEGRATION.md`
Expected: at least 3.

Run: `go build ./... && go test -race ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add INTEGRATION.md
git commit -m "docs(integration): implementer lifecycle protocol section"
```

---

## Task 4: Add Sections 4 + 5 — Controllers + FAQ

**Files:**
- Modify: `INTEGRATION.md` (replace the trailing placeholder)
- Modify: `README.md` (add a single "Integration" linking line)

- [ ] **Step 1: Append controllers + FAQ sections**

Replace the trailing `<!-- Sections 4–5 added in subsequent commits -->` placeholder with:

````markdown
## 4. For controllers — optional dispatch addendum

If you orchestrate implementer subagents — superpowers' `subagent-driven-development`, hone-ai's equivalent, or a hand-rolled dispatch loop — paste the §3.2 clause into every dispatch prompt. The controller itself does **not** call the MCP tools; it just makes sure each subagent knows to.

> **Append the §3.2 clause to your implementer-subagent prompt template, right before the "Report Format" section.**

Per-skill-system pointers:

- **superpowers:** open `subagent-driven-development/implementer-prompt.md` and paste before the "Report Format" heading.
- **hone-ai:** the equivalent dispatch template file.
- **Vanilla harness:** wherever your dispatch prompt lives (a CLAUDE.md, a system-prompt template, etc.).

### 4.1 DONE-gate (recommended)

After the subagent reports DONE, you may want to require evidence that `validate_completion` was called and returned `pass` (or `warn` with all findings addressed). The simplest way: ask for the verdict + findings JSON in the subagent's DONE report. The MCP server does not enforce this; the prompt does.

### 4.2 Anti-pattern: don't double-validate from the controller

Do NOT have the controller call `validate_completion` itself after the subagent reports DONE. Sessions are scoped to the subagent's lifetime, and the post-hook the subagent already called IS the gate. Calling it again from the controller produces a `session_not_found` finding and adds noise.

---

## 5. FAQ / failure modes

**What if the reviewer is wrong?**
Findings are advisory. If a finding misreads the code, document the disagreement in the next call's `working_on` field so the next reviewer call sees your reasoning, then re-validate. Don't silently ignore.

**My implementer is also Claude Sonnet — does this still help?**
Less than if they were different models. Same model + same training data ≈ same blind spots. If you can't run a different provider, at least pick a different family (Sonnet implementer, Opus reviewer; or Sonnet implementer, Haiku for cheap mid-checks plus Opus for post). Different provider is best.

**How do I know my session expired?**
You'll get a finding with `category: session_not_found`. Default TTL is 4h. Re-call `validate_task_spec` to start a new session and continue with the new ID.

**My payload is too big.**
The MCP returns a finding with `category: payload_too_large`. Default cap is 200 KB across `changed_files` / `final_files`. Send a unified diff against the prior state, or split the call.

**`validate_task_spec` is asking for ACs my plan doesn't have.**
That's the spec quality gate working as designed. Either (a) add the missing ACs to the plan and re-validate, or (b) acknowledge the gap in the next `working_on` description so the reviewer knows to expect implementer-discretion choices.

**What if the implementer skips the post-hook?**
Two defenses: the §3.2 prompt clause marks post REQUIRED, and the controller can require the post-hook envelope in the subagent's DONE report (see §4.1).

**Does `check_progress` catch failing tests?**
No — the reviewer LLM reasons over text, not execution. Use mid-checks for drift detection (scope creep, untouched ACs, unaddressed prior findings), not for debugging. Run tests separately.

**Cost / latency overhead.**
Roughly 1–2 s and $0.001–$0.02 per call, depending on payload size and model choice. Two mandatory calls per task minimum (pre + post). Use a cheap-fast model for mid-checks and a stronger model for post.

**Where do I file bugs?**
[`https://github.com/patiently/anti-tangent-mcp/issues`](https://github.com/patiently/anti-tangent-mcp/issues).
````

- [ ] **Step 2: Add a one-line "Integration" link to README.md**

Read `README.md` and find the existing "## Design" section. Add a new H2 immediately above it (or below "## The 3 tools" — whichever reads more naturally with the surrounding context). The new section reads:

```markdown
## Integration

For wiring this MCP into your LLM-driven implementation workflow (superpowers, hone-ai, vanilla Claude Code, or any harness with MCP support), see [`INTEGRATION.md`](INTEGRATION.md).
```

- [ ] **Step 3: Final sanity check**

Run: `grep -c '^## ' INTEGRATION.md`
Expected: 5 (Setup, plan authors, implementers, controllers, FAQ).

Run: `grep -c 'INTEGRATION.md' README.md`
Expected: at least 1.

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`
Expected: clean (the doc-only changes don't affect Go).

- [ ] **Step 4: Commit**

```bash
git add INTEGRATION.md README.md
git commit -m "docs(integration): controllers, FAQ, and README link"
```

- [ ] **Step 5: Push**

```bash
git push
```

The PR (#1) picks up the new commits automatically.

---

## Self-Review Notes

Spec coverage check (each spec section → task):

- §1 What this is → Task 1 (intro paragraph in scaffold)
- §2 Setup → Task 1
- §3 Plan-author format → Task 2
- §4 Implementer lifecycle protocol (4a-4d) → Task 3
- §5 Controller addendum → Task 4
- §6 FAQ / failure modes → Task 4
- README pointer → Task 4 step 2

No spec gaps.

Placeholder scan: the `<!-- Sections … added in subsequent commits -->` lines appear in Tasks 1, 2, 3 by design as per-task transitions and are removed by the next task. They are not orphan placeholders.

Naming/property consistency:
- The lifecycle clause uses `validate_task_spec`, `check_progress`, `validate_completion` consistently across §3.1, §3.2, §3.3, §4, §5 — matches the actual MCP tool names from `internal/mcpsrv/handlers.go`.
- `task_title` / `goal` / `acceptance_criteria` / `non_goals` / `context` match `ValidateTaskSpecArgs` field names.
- Finding categories cited in examples (`ambiguous_spec`, `scope_drift`, `missing_acceptance_criterion`, `session_not_found`, `payload_too_large`) match the constants in `internal/verdict/verdict.go`.
