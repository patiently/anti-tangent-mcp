# validate_plan tool — Design

**Date:** 2026-05-09
**Status:** Draft — pending spec review
**Folds into release:** v0.1.0 (the initial release; pre-tag)

## Purpose

Add a 4th MCP tool, `validate_plan`, that reviews an entire implementation plan in a single call and proposes ready-to-paste structured-header blocks (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. Replaces the per-task `validate_task_spec` loop that the controller currently runs at the plan-handoff gate.

The motivating gap: real-world plans (superpowers' `writing-plans` output, hone-ai's equivalent, vanilla Claude Code plans) use a TDD-step shape (`Files: …` / `Step 1: …`) without the structured Goal/AC/Non-goals header that anti-tangent's protocol requires. Today the protocol simply doesn't fire on those plans, defeating its purpose. `validate_plan` makes adoption frictionless: the controller passes the plan as-is, gets back per-task headers, the planner adopts/edits, and only then proceeds to dispatch.

## Goals

- One controller-side call analyzes a whole plan; no need to call `validate_task_spec` N times at handoff.
- For tasks lacking a Goal/AC/Non-goals/Context header, propose one inferred from the existing task content (title, Files, Steps).
- For tasks already carrying a structured header, run the same quality critique that `validate_task_spec` would.
- Plan-wide concerns (out-of-order tasks, missing intro, duplicate titles) get their own findings list, separate from per-task findings.
- The output is structured: per-task entries with verdict, findings, and the literal header markdown to paste in.
- Stateless. No session is created; the tool is idempotent. Sessions are still created per-task by the implementer's `validate_task_spec` call at dispatch time.
- Backward-compatible. The existing 3 tools' shapes are unchanged.

## Non-goals

- Not a write-side tool. `validate_plan` returns suggestions; it does not edit the plan file.
- No streaming output.
- No diff input. Each call analyzes whatever full plan text is passed.
- No multi-plan / stacked-plan input. One plan per call.
- No custom AC linting rules. The reviewer LLM does the quality assessment.
- No plan-level session. The tool is stateless and does not return a `session_id`.
- Not intended to replace `validate_task_spec`. The implementer still calls `validate_task_spec` at task start to create the session that `check_progress` and `validate_completion` thread through.

## Architecture

### Code layout

```
internal/
  mcpsrv/
    handlers.go           # add ValidatePlan handler + ValidatePlanArgs
    server.go             # register the 4th tool
  verdict/
    plan.go               # PlanResult, PlanTaskResult types
    plan_schema.json      # embedded JSON schema for plan-level reviewer output
    plan_parser.go        # ParsePlan(rawJSON) → PlanResult, with retry-once semantics
  prompts/
    templates/
      plan.tmpl           # 4th template, plan-level analysis
    testdata/
      plan_basic.golden
  planparser/             # NEW package
    planparser.go         # SplitTasks(plan_text) → []RawTask{Title, Body, HasStructuredHeader}
    planparser_test.go
```

`planparser` is a small isolated package that lives next to (not inside) `verdict/`. Putting markdown parsing inside `verdict/` would muddy that package's "canonical reviewer-output" responsibility.

### Tool surface

**Tool name:** `validate_plan`

**Description shown to subagent:** *"Validate an implementation plan as a whole BEFORE dispatching subagents to implement individual tasks. Returns per-task findings and ready-to-paste structured headers (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. Call this once at plan-handoff time; the per-task `validate_task_spec` is still called by each implementing subagent at task start."*

**Input:**
```json
{
  "plan_text": "string, required — the full plan markdown, including its task headings",
  "model_override": "string, optional — e.g. 'openai:gpt-5'"
}
```

**Output (new `PlanResult` type, replaces the existing `Envelope` for this tool only):**
```json
{
  "plan_verdict": "pass | warn | fail",
  "plan_findings": [
    {
      "severity": "critical | major | minor",
      "category": "missing_acceptance_criterion | ambiguous_spec | scope_drift | quality | other | session_not_found | payload_too_large | unaddressed_finding",
      "criterion": "string — plan-level concern",
      "evidence": "string",
      "suggestion": "string"
    }
  ],
  "tasks": [
    {
      "task_index": 0,
      "task_title": "Task 1: …",
      "verdict": "pass | warn | fail",
      "findings": [{ /* same Finding shape as plan_findings */ }],
      "suggested_header_block": "**Goal:** …\n\n**Acceptance criteria:**\n- …\n",
      "suggested_header_reason": "string — why this header is being proposed"
    }
  ],
  "next_action": "string",
  "model_used": "string",
  "review_ms": 1234
}
```

Notes on the shape:
- `plan_findings` carries plan-wide concerns (above any single task).
- `tasks[]` carries per-task analysis. `task_index` is the 0-based position in the plan.
- `suggested_header_block` is the literal markdown to paste at the top of the task. **Empty string** when the task already has a sufficient header.
- `suggested_header_reason` defends the suggestion in one short sentence.
- `plan_verdict` is the worst-of: `fail` if any task or plan-finding is `fail`, else `warn` if any `warn`, else `pass`.

The Finding shape (`severity`, `category`, `criterion`, `evidence`, `suggestion`) is identical to the existing `verdict.Finding`. We `$ref` it from the plan schema.

### Server architecture & parsing

**`planparser.SplitTasks(plan_text string) ([]RawTask, string)`** returns the task list plus the preamble:
- Splits `plan_text` on a regex matching `^### Task \d+:.*$` at line boundaries.
- Text before the first match is `plan_preamble` (intro + file map + non-task content).
- Each chunk after a heading is a `RawTask{Title, Body, HasStructuredHeader}`.
- `HasStructuredHeader` is determined by checking for both `**Goal:**` and `**Acceptance criteria:**` substrings in the body. Used for telemetry/logging only; not sent to the reviewer.

**Edge cases:**
- **No `### Task N:` headings detected** → handler short-circuits without a provider call: returns `PlanResult{plan_verdict: "fail", plan_findings: [{category: "other", criterion: "structure", evidence: "no `### Task N:` headings detected", suggestion: "use `### Task N: Title` for each task; this tool expects numbered tasks"}], tasks: []}`.
- **Headings out of order** (Task 1 → Task 3 → Task 2): preserved as-is in `tasks[]`; the reviewer is instructed to flag in `plan_findings` if it judges this a problem.
- **Plan smaller than one paragraph + no headings**: same as the no-headings case.
- **Heading without a number** (`### Task: Foo`): not matched by the regex; the parser ignores it. The reviewer sees the preamble (which contains it) and can flag it.

**Concurrency & state:** `validate_plan` is **stateless**. No session created, no `session_id` returned. Re-calling `validate_plan` is idempotent; each call analyzes whatever plan text is passed in.

**Reused infrastructure:** provider clients, allowlist, payload cap (`MaxPayloadBytes`, default 200KB applied to `plan_text`), and the retry-once-on-malformed-JSON logic in `mcpsrv/handlers.go`. The retry helper is extracted into a shared function since both the existing 3 tools and `validate_plan` use it.

### Prompt strategy

The plan template (`prompts/templates/plan.tmpl`) shares the system prompt with the existing 3 templates. The user prompt:

```
## Plan under review

{{.PlanText}}

## What to evaluate

You are reviewing an entire implementation plan BEFORE any tasks are dispatched
to implementing subagents. Your job is twofold for EACH task:

1. **Critique what's there.** If a task already has a Goal / Acceptance
   criteria / Non-goals / Context block, evaluate the same way
   `validate_task_spec` does: structural completeness, AC quality (testable /
   specific / unambiguous), unstated assumptions. Emit findings.

2. **Generate what's missing.** If a task does NOT have a structured
   Goal/AC/Non-goals/Context header, propose one. Synthesize the Goal from
   the task title and any "Files:" / "Steps:" content already present.
   Synthesize Acceptance criteria from observable outcomes implied by the
   steps. Suggest Non-goals only when steps imply scope boundaries; leave
   empty otherwise. Suggest Context only when there's clear environmental
   info (paths, deps) the implementer needs. Put the proposed markdown
   verbatim in suggested_header_block.

3. **Plan-wide review.** In addition to per-task findings, review the plan
   as a whole: are tasks out of order, are there duplicate titles, is there
   an architecture/intro section if one would help? Emit those as
   plan_findings (separate from any task).

For tasks that already have a perfectly fine structured header, leave
suggested_header_block empty and emit findings only if quality issues exist.

Severity: critical = unimplementable as written; major = implementer would
still misimplement; minor = nit.

## Output

Respond with a JSON object matching the provided schema. Do not include
prose outside the JSON.
```

### JSON schema

`internal/verdict/plan_schema.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "PlanResult",
  "type": "object",
  "required": ["plan_verdict", "plan_findings", "tasks", "next_action"],
  "additionalProperties": false,
  "properties": {
    "plan_verdict": { "type": "string", "enum": ["pass", "warn", "fail"] },
    "plan_findings": {
      "type": "array",
      "items": { "$ref": "#/definitions/finding" }
    },
    "tasks": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["task_index", "task_title", "verdict", "findings", "suggested_header_block", "suggested_header_reason"],
        "additionalProperties": false,
        "properties": {
          "task_index":              { "type": "integer", "minimum": 0 },
          "task_title":              { "type": "string", "minLength": 1 },
          "verdict":                 { "type": "string", "enum": ["pass", "warn", "fail"] },
          "findings":                { "type": "array", "items": { "$ref": "#/definitions/finding" } },
          "suggested_header_block":  { "type": "string" },
          "suggested_header_reason": { "type": "string" }
        }
      }
    },
    "next_action": { "type": "string", "minLength": 1 }
  },
  "definitions": {
    "finding": {
      "type": "object",
      "required": ["severity", "category", "criterion", "evidence", "suggestion"],
      "additionalProperties": false,
      "properties": {
        "severity": { "type": "string", "enum": ["critical", "major", "minor"] },
        "category": {
          "type": "string",
          "enum": [
            "missing_acceptance_criterion", "scope_drift", "ambiguous_spec",
            "unaddressed_finding", "quality", "session_not_found",
            "payload_too_large", "other"
          ]
        },
        "criterion":  { "type": "string", "minLength": 1 },
        "evidence":   { "type": "string", "minLength": 1 },
        "suggestion": { "type": "string", "minLength": 1 }
      }
    }
  }
}
```

`suggested_header_block` is `"type": "string"` (no `minLength`) because empty is valid — it means "no suggestion needed for this task."

### Provider structured-output mechanics

Identical to existing tools — same three-way fan-out:
- **Anthropic** — single `submit_plan_review` tool with `input_schema` = the plan schema
- **OpenAI** — `response_format.json_schema` strict mode with the plan schema
- **Google** — `responseSchema` on `generationConfig`

`internal/providers/` requires no changes. Providers already accept any `JSONSchema []byte` in the Request struct; the handler passes `verdict.PlanSchema()` instead of `verdict.Schema()` for this tool.

### Parser

`internal/verdict/plan_parser.go` mirrors the existing `parser.go`:
- Tolerates leading/trailing ``` ``` ``` fences and surrounding whitespace
- `json.Decoder` with `DisallowUnknownFields()`
- Validates enums on `plan_verdict`, each `task.verdict`, and the `severity`/`category` of every finding
- Same retry-once logic if the first parse fails (handler appends `verdict.RetryHint()` and re-issues)

## Integration with the existing protocol

### `INTEGRATION.md` — `§5.1 Plan-handoff gate` rewritten

The procedure changes from "call `validate_task_spec` per task" to:

1. Call `validate_plan` once with the full plan markdown. Capture the `PlanResult`.
2. Surface results to the user. Show `plan_verdict`, `plan_findings`, and per-task verdicts/findings. For any task whose `suggested_header_block` is non-empty, show the proposed header and ask the human to adopt or revise.
3. Apply the proposed header blocks (controller may apply automatically when verdicts are pass/warn and the human approves; always defer to the human for fail).
4. If anything material changed (headers added, ACs rewritten), re-run `validate_plan` to confirm. Repeat until `plan_verdict: "pass"` or every `warn` is explicitly justified.
5. Only proceed to dispatch when the plan-level gate passes.

Cost framing: one call per plan instead of N. A 20-task plan goes from 20 handoff calls to 1.

The "skip when the plan only has one task" rule stays.

A new **§5.5** documents the relationship explicitly:

| Tool | Caller | Lifecycle moment | Returns |
|---|---|---|---|
| `validate_plan` | Controller | Once, before any dispatch | Plan-wide + per-task analysis with ready-to-paste header blocks. Stateless. |
| `validate_task_spec` | Implementing subagent | Once at task start, after dispatch | Per-task structural/quality review. **Creates a session** that the implementer threads through `check_progress` and `validate_completion`. |

The two analyses overlap intentionally: the plan gate catches plan-wide and per-task issues at handoff; the implementer gate catches anything that changed between handoff and dispatch and produces the session that the rest of the implementer's lifecycle uses.

### `~/.claude/anti-tangent.md` — "Plan-handoff gate" section rewritten

Same procedural change as INTEGRATION.md §5.1.

### Implementer-prompt clause (`§4.2`) — unchanged

The clause is about the per-task lifecycle the implementer follows. That doesn't change.

### `README.md` — tool list updated

Tool list grows from 3 to 4. The "all return the same envelope" line gets a footnote noting `validate_plan` returns a richer `PlanResult` (link to INTEGRATION.md).

## Configuration

One new env var, default-inheriting from PRE:

```
ANTI_TANGENT_PLAN_MODEL=<provider>:<model_id>   # default: same as ANTI_TANGENT_PRE_MODEL
```

Validated at startup against the allowlist, same as the other model env vars. Per-call `model_override` works the same way.

`ANTI_TANGENT_MAX_PAYLOAD_BYTES` (200KB default) applies to `plan_text`. No new tunables.

## Error handling

| Situation | Response |
|---|---|
| `plan_text` empty or missing | Tool returns Go error: `"plan_text is required"` |
| `plan_text` exceeds `MaxPayloadBytes` | `PlanResult{plan_verdict: "fail", plan_findings: [{category: "payload_too_large", ...}], tasks: []}`. No provider call. |
| No `### Task N:` headings detected | `PlanResult{plan_verdict: "fail", plan_findings: [{category: "other", criterion: "structure", ...}], tasks: []}`. No provider call. |
| Provider transport error (network / 5xx / rate limit) | MCP `isError: true` with the underlying cause. Same behavior as existing tools. |
| Provider returns malformed JSON | One retry with `RetryHint` appended; if still bad → MCP error. |
| Reviewer returns valid JSON but malformed against the plan schema | Schema's `additionalProperties: false` + `required` arrays surface this as a parsing error → falls into the malformed-JSON retry path. |

## Testing

Five test surfaces:

1. **`internal/planparser/planparser_test.go`** — pure-Go table-driven:
   - N structured tasks → `HasStructuredHeader: true`.
   - N TDD-step tasks (no `**Goal:**`) → `HasStructuredHeader: false`.
   - Mixed shapes → mixed flags.
   - No headings → empty `tasks[]`.
   - Whitespace, code fences containing the word "Task" → not falsely matched.
   - Preamble preserved as separate field.

2. **`internal/verdict/plan_test.go`** — golden + parser:
   - `PlanSchema()` returns valid JSON with the expected keys.
   - `ParsePlan` round-trips a known PlanResult.
   - Strips ``` ``` fences (mirror of existing parser test).
   - Rejects malformed enums, missing required fields, extra fields.

3. **`internal/mcpsrv/handlers_test.go`** — extended:
   - `TestValidatePlan_HappyPath` — fake reviewer returns a stub PlanResult; handler returns it intact + populates ModelUsed/ReviewMS.
   - `TestValidatePlan_NoTaskHeadings` — input with no `### Task N:` returns the short-circuit fail envelope without calling the reviewer.
   - `TestValidatePlan_PayloadTooLarge` — input larger than cap returns the size-limit envelope.
   - `TestValidatePlan_MissingPlanText` — empty/missing input returns Go error.

4. **`internal/prompts/prompts_test.go`** — extended:
   - `TestRenderPlan` — golden test for the plan template, given a sample plan input.

5. **`internal/mcpsrv/integration_test.go`** — extended:
   - Full lifecycle test extended to add a `validate_plan` call before the existing `validate_task_spec → check_progress → validate_completion` chain. Confirms the tool is registered, reachable through MCP transport, and returns a parseable PlanResult.

Mainline `go test -race ./...` continues to be the gate. No e2e additions in this scope.

## Versioning

Folded into **v0.1.0** (the initial release; not yet tagged). The PR for `validate_plan` lands on `version/0.1.0` after the existing v0.1.0 work.

- `VERSION` stays at `0.1.0`.
- `CHANGELOG.md`'s existing `## [0.1.0] - 2026-05-07` entry's `### Added` list gains:
  - `validate_plan` MCP tool — plan-level handoff gate that reviews an entire implementation plan in one call and proposes ready-to-paste structured header blocks (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. Replaces the per-task plan-handoff loop.
  - `ANTI_TANGENT_PLAN_MODEL` env var — overrides the model used by `validate_plan`. Defaults to `ANTI_TANGENT_PRE_MODEL`.

The bootstrap path for the first release (manual `v0.1.0` tag after merge to main) is unchanged.

## Out of scope (deferred)

- **Plan diff / re-validate-changed-only.** v0.1.0 always re-analyzes the whole plan on every call.
- **Auto-applying suggestions.** The tool returns suggestions; the controller (or human) decides whether to adopt them. There is no `apply_suggestions` write-side tool.
- **Multi-plan or stacked-plan input.** One plan per call.
- **Streaming output.** Single round-trip JSON only.
- **Custom AC linting rules.** The reviewer LLM does the quality assessment; we do not ship a separate static linter.
- **Plan-level session.** `validate_plan` is stateless. Re-calling it just re-analyzes whatever you pass.
- **Skill-formatted distribution of the integration guide.** Out of scope for this feature; tracked separately in the integration spec.
