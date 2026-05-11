# validate_plan chunking — v0.1.4

**Status:** approved
**Author:** Patrick Gilmore (with Claude)
**Date:** 2026-05-11
**Supersedes:** none (extends `2026-05-09-validate-plan-design.md`)

## Problem

`validate_plan` fails on real-world plans of ~12+ tasks. The MCP server hardcodes `MaxTokens: 4096` for the reviewer call (`internal/mcpsrv/handlers.go:413`). For each task the model emits a verdict, a findings list, and a `suggested_header_block` markdown string — typically 250–400 output tokens per task. A 25-task plan needs ~7,500 output tokens. The provider hits the cap, truncates JSON mid-string, and the parser returns:

```
plan provider response failed schema after retry: decode plan result: EOF
```

The caller sees an opaque error with no signal that length was the cause and no path forward other than splitting the plan by hand. This blocks the plan-handoff gate exactly when it matters most — large, multi-phase plans.

## Goals

- Make `validate_plan` succeed on plans of arbitrary task count without the caller having to pre-split.
- Keep the existing single-call path (cheap, fast) for small plans.
- Preserve the public `PlanResult` shape: consumers see the same envelope whether the work was done in one call or many.
- Surface the output budget as configuration, so operators can tune for their plan density.

## Non-goals

- Parallel execution of chunks. Sequential is fine and avoids rate-limit complexity.
- Persistent state. Chunking is per-request; the server remains stateless for plan validation.
- Eager truncation detection per provider (`finish_reason: "length"` etc.). With "always chunk above N" the chunks are sized to fit by construction; an overflowing chunk is an operator-tuning issue, not a runtime fallback.
- Cross-chunk deduplication of `plan_findings`. We make exactly one plan-findings call by design.
- Partial-success protocol on chunk error. If any chunk call fails, the whole `validate_plan` returns an error envelope; the caller retries.

## Decision summary

| Question | Decision |
|---|---|
| Strategy | Always chunk above N tasks (deterministic; predictable cost) |
| Default chunk size | 8 tasks per chunk, env-configurable |
| Plan-findings | Dedicated plan-findings-only pass (compact output, full plan context) |
| Output budget | Configurable per-task and per-plan caps, env-configurable |
| Failure mode | Whole-request failure on any chunk error; per-chunk retry-once preserved |

## Architecture

### High-level flow

```
validate_plan(plan_text)
├── parse `### Task N:` headings → tasks[] ([]RawTask from planparser)
├── if len(tasks) ≤ PlanTasksPerChunk:
│   └── single-call path (today's behavior; uses PlanMaxTokens)
└── else (chunking path):
    ├── Pass 1 — plan-findings-only call
    │   prompt: full plan_text
    │   schema: PlanFindingsOnlySchema
    │   returns: {plan_verdict, plan_findings[], next_action}
    ├── Pass 2..K+1 — per-task chunks where K = ceil(len(tasks)/N)
    │   chunk i covers tasks[i*N : min((i+1)*N, len(tasks))]
    │   prompt: full plan_text + an explicit list of the heading titles in this chunk
    │   schema: TasksOnlySchema
    │   returns: {tasks: [...]}
    └── merge → PlanResult{plan_verdict, plan_findings, tasks (flat, ordered), next_action, model_used, review_ms (sum)}
```

The plan text is sent in full on every call. Input tokens are cheap and not the bottleneck; what overflows is the response. Splitting the output (not the input) is the correct fix.

### Chunking math

For `n = len(tasks)` and `N = PlanTasksPerChunk`, the chunk count is **strictly `K = ceil(n/N)`**. The slicing rule is `tasks[i*N : min((i+1)*N, n)]` for `i = 0..K-1`. The last chunk may contain anywhere from 1 to N tasks. Worked examples (all with `N=8`):

| `n` | `K` | Chunk sizes |
|---|---|---|
| 8  | 1 (single-call path) | 8 |
| 9  | 2 | 8, 1 |
| 16 | 2 | 8, 8 |
| 17 | 3 | 8, 8, 1 |
| 25 | 4 | 8, 8, 8, 1 |
| 50 | 7 | 8×6, 2 |

A 1-task trailing chunk costs one extra reviewer call (~$0.005) and ~500 ms — accepted as the price of deterministic, easy-to-reason-about chunking. No "absorb the remainder into the previous chunk" optimization (would push a chunk past `N`, risking the very overflow this design exists to prevent).

### Chunk-range identification (no slice-index ambiguity)

`planparser.RawTask.Title` carries the full heading text (e.g. `"Task 4: Add /healthz endpoint"`). `### Task N:` numbers may not be contiguous in arbitrary plans, so the implementation **never** passes integer ranges to the reviewer. Instead, `PlanChunkInput` carries the *slice of `RawTask`* for the chunk, and the template renders the exact heading titles the reviewer must emit:

```
PlanChunkInput { PlanText string; ChunkTasks []planparser.RawTask }
```

Template directive (paraphrased): "Return per-task verdicts for **these specific tasks**, identified by their `### Task N: Title` headings: [enumerated list]. Do not emit `plan_findings`. Do not emit results for any task outside this list."

The merge step uses the same `RawTask` slice to validate the response (see error handling below).

### Why a separate plan-findings pass

Cross-cutting findings (e.g. "tasks 3 and 17 have overlapping ACs", "task 5 depends on task 12 which isn't scoped") inherently need full-plan visibility. Asking each chunk for its slice of `plan_findings` would either duplicate or fragment them. A single dedicated pass with a compact schema (no per-task data) is both correct and cheap — typical output is a few hundred tokens.

### Why 8 as the default chunk size

Output budget per chunk = `PlanMaxTokens` (default 4096) minus JSON envelope overhead (~500 tokens). Per-task output is empirically 250–400 tokens (verdict + findings array + `suggested_header_block` when missing). 8 tasks × 350 tokens = ~2,800 tokens, leaving comfortable headroom. Configurable via env for plans with denser per-task content.

## Configuration

Three new optional env vars, all validated as positive integers (zero/negative rejected at startup, same pattern as existing positive-int vars):

| Env var | Default | Used in |
|---|---|---|
| `ANTI_TANGENT_PER_TASK_MAX_TOKENS` | `4096` | `review()` for `validate_task_spec` / `check_progress` / `validate_completion` |
| `ANTI_TANGENT_PLAN_MAX_TOKENS` | `4096` | `reviewPlanSingle` and each chunk of `reviewPlanChunked` |
| `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` | `8` | threshold + per-chunk size in `reviewPlanChunked` |

Defaults are chosen so that **plans of 8 tasks or fewer see zero behavioral change** from v0.1.3. Operators with larger or denser plans can raise `PlanMaxTokens` (and optionally `PlanTasksPerChunk`) without code changes.

**Operator notes.**

- *`PER_TASK` naming.* `ANTI_TANGENT_PER_TASK_MAX_TOKENS` governs all three task-scoped lifecycle hooks (`validate_task_spec`, `check_progress`, `validate_completion`) collectively. The dichotomy is per-task vs. plan-level; per-task is accurate because each hook reviews exactly one task. We're not adding three separate envs — that would be over-configuration.
- *`PlanTasksPerChunk` as a single knob.* The same value acts as both the chunking threshold (`len(tasks) > N` triggers chunking) and the per-chunk size (`tasks[i*N : min((i+1)*N, n)]`). One knob, one mental model: "above N tasks, batch in groups of N." If a real operator ever needs them decoupled, we'll split it then (YAGNI).
- *Per-call vs whole-request timeout.* `ANTI_TANGENT_REQUEST_TIMEOUT` (default 120 s) applies **per reviewer call**, not to the whole `reviewPlanChunked` invocation. A 25-task plan does 5 sequential calls, so worst-case wall-clock is ~5 × 120 s = 10 min. MCP clients (Claude Code, opencode) may have their own shorter tool-call timeouts; operators running large plans should be aware and may need to lower `PlanTasksPerChunk` (more, smaller calls) rather than raise `RequestTimeout` to fit within client deadlines.
- *Bundled fix.* `ANTI_TANGENT_PER_TASK_MAX_TOKENS` and `ANTI_TANGENT_PLAN_MAX_TOKENS` both make a previously-hardcoded literal (`MaxTokens: 4096` at `handlers.go:109` and `:413`) configurable. This is intentionally bundled with the chunking work because the same root cause — fixed output budget — motivates both knobs, and shipping them together avoids a second release for a one-line config change.

## Components

### `internal/config/config.go` (extend)

Add three fields to `Config`:
- `PerTaskMaxTokens int`
- `PlanMaxTokens int`
- `PlanTasksPerChunk int`

Parse each from its env var; default if unset; reject `<= 0` with `must be positive` error. Mirror the validation style already used for `ANTI_TANGENT_MAX_PAYLOAD_BYTES` and `ANTI_TANGENT_SESSION_TTL`.

Add unit tests in `internal/config/config_test.go`: default values, valid overrides, invalid (zero, negative, non-integer) rejection.

### `internal/verdict/plan.go` (extend)

Keep the existing `PlanResult` and `PlanSchema()` exactly as-is for the single-call path. `PlanSchema()` returns `[]byte` (embedded JSON loaded via `//go:embed`); the two new schemas must follow the same pattern to fit `providers.Request.JSONSchema []byte` without changing the provider interface.

Add:
- `plan_findings_only_schema.json` (embedded) + `PlanFindingsOnlySchema() []byte` — schema with `plan_verdict`, `plan_findings`, optional `next_action`; `tasks` field omitted entirely.
- `tasks_only_schema.json` (embedded) + `TasksOnlySchema() []byte` — schema with `tasks` (required, same item shape as the existing `PlanTaskResult` schema), and nothing else.
- `ParsePlanFindingsOnly(raw json.RawMessage) (PlanFindingsOnly, error)` — typed parse for the plan-findings-only response.
- `ParseTasksOnly(raw json.RawMessage) (TasksOnly, error)` — typed parse for the per-chunk response.

Add small types:
```go
type PlanFindingsOnly struct {
    PlanVerdict  Verdict   `json:"plan_verdict"`
    PlanFindings []Finding `json:"plan_findings"`
    NextAction   string    `json:"next_action,omitempty"`
}

type TasksOnly struct {
    Tasks []PlanTaskResult `json:"tasks"`
}
```

These two types and their schemas exist purely for the chunked path. They never appear in the MCP response — the handler merges them into the public `PlanResult`.

### `internal/prompts/templates/`

Two new templates plus their golden files:

- `plan_findings_only.tmpl` — system + user prompt that:
  - Embeds the full `plan_text`.
  - Asks for cross-cutting plan-level findings only (consistency, dependencies, gaps, redundancy).
  - Explicitly forbids per-task `tasks[]` output.
  - References the JSON schema (provider's `response_format` handles the binding).

- `plan_tasks_chunk.tmpl` — system + user prompt that:
  - Embeds the full `plan_text`.
  - Enumerates the *exact* heading titles for the chunk: "Return per-task verdicts for the following tasks, identified by their `### Task N:` headings: `{{range .ChunkTasks}}{{.Title}}{{end}}` (one per line). Do not emit `plan_findings`. Do not emit results for any task outside this list."
  - Notes that the model has the full plan as context so cross-task reasoning is allowed — but only emitted tasks count.

Render functions in `internal/prompts/prompts.go`:
- `RenderPlanFindingsOnly(input PlanInput) (Output, error)`
- `RenderPlanTasksChunk(input PlanChunkInput) (Output, error)` where:
  ```go
  type PlanChunkInput struct {
      PlanText   string
      ChunkTasks []planparser.RawTask
  }
  ```
  Carrying `[]RawTask` (not integer ranges) sidesteps the heading-number-vs-slice-index ambiguity for plans with non-contiguous `### Task N:` numbering. The template iterates the slice to render heading titles verbatim.

Golden files added under `internal/prompts/testdata/`. Generate with `go test ./internal/prompts/... -update` and review the diff before committing.

### `internal/mcpsrv/handlers.go`

Replace `reviewPlan` with two helpers that **both take `planText string` and render internally**. This is a deliberate departure from the current `reviewPlan(ctx, model, prompts.Output)` shape: the chunked path renders multiple distinct templates per call (one for plan-findings, one per chunk), so pre-rendering at the dispatch site doesn't make sense. Making both helpers symmetric (each renders its own prompt) keeps the code consistent and the dispatch site clean.

```go
// Single-call path; current behavior preserved. Used when len(tasks) <= chunkSize.
// Renders prompts.RenderPlan internally; uses PlanSchema; MaxTokens = PlanMaxTokens.
func (h *handlers) reviewPlanSingle(ctx context.Context, model config.ModelRef, planText string) (verdict.PlanResult, string, int64, error)

// Chunked path; used when len(tasks) > chunkSize.
// Orchestrates Pass 1 (plan-findings) + K per-task chunks, merges, returns PlanResult.
// Renders RenderPlanFindingsOnly and RenderPlanTasksChunk internally; uses PlanFindingsOnlySchema/TasksOnlySchema.
func (h *handlers) reviewPlanChunked(ctx context.Context, model config.ModelRef, planText string, tasks []planparser.RawTask, chunkSize int) (verdict.PlanResult, string, int64, error)
```

Dispatch in `validate_plan`:

```go
if len(tasks) <= h.deps.Cfg.PlanTasksPerChunk {
    pr, modelUsed, ms, err = h.reviewPlanSingle(ctx, model, args.PlanText)
} else {
    pr, modelUsed, ms, err = h.reviewPlanChunked(ctx, model, args.PlanText, tasks, h.deps.Cfg.PlanTasksPerChunk)
}
```

The existing `prompts.RenderPlan` call at the dispatch site is **removed**; rendering moves inside the single-call helper. Both helpers read `h.deps.Cfg.PlanMaxTokens` for their `MaxTokens` field. The per-task `review()` helper (lines ~98–128) is updated to read `h.deps.Cfg.PerTaskMaxTokens` for the same purpose.

The per-chunk schema-retry path (existing pattern: malformed JSON → retry once with `verdict.RetryHint()`) is preserved inside each individual reviewer call.

### Merge semantics

```go
// passFindings = result of Pass 1 (PlanFindingsOnly)
// chunkResults = ordered slice of TasksOnly, one per chunk
// modelRef     = the config.ModelRef both passes used (same for all calls)
// totalMs      = accumulated wall-clock across Pass 1 + all chunks

result := verdict.PlanResult{
    PlanVerdict:  passFindings.PlanVerdict,
    PlanFindings: passFindings.PlanFindings,
    NextAction:   passFindings.NextAction, // taken verbatim; no server-side fallback
    Tasks:        make([]verdict.PlanTaskResult, 0, len(tasks)),
}
for _, c := range chunkResults {
    result.Tasks = append(result.Tasks, c.Tasks...)
}
```

**`next_action` rule.** Use Pass 1's value verbatim — even if empty. This matches the single-call path, which has **no server-side fallback** today (`internal/mcpsrv/handlers.go` trusts whatever the LLM returns). Adding a synthesized default here would create a behavioral asymmetry between the single-call and chunked paths; symmetry is more valuable than the marginal benefit of a guaranteed-non-empty string.

**`model_used` rule.** All calls use the same `config.ModelRef`. The handler returns the model identifier from the first reviewer response (matches the existing pattern at `handlers.go:433`: `model.Provider + ":" + resp.Model`, falling back to `model.String()` if `resp.Model` is empty). This is reported in the MCP envelope, not stored on `PlanResult` itself — the existing return tuple `(PlanResult, modelUsed string, ms int64, error)` is preserved.

**`review_ms` rule.** `reviewPlanChunked` accumulates `time.Since(start).Milliseconds()` from each individual reviewer call into `totalMs` and returns it as the third tuple element. The MCP envelope reports the sum, matching consumer expectations (one MCP tool call = one `review_ms` value).

**Task ordering.** Chunks are dispatched in slice order, and tasks within each chunk are appended in the order the reviewer emits them. The model is instructed (via the enumerated heading-titles list) to emit tasks in the same order they appear in the chunk. The merge preserves that order.

**Post-merge validation.** After all chunks return, the handler checks:

```go
if len(result.Tasks) != len(tasks) {
    return verdict.PlanResult{}, "", 0,
        fmt.Errorf("chunked plan review returned %d task results, expected %d",
            len(result.Tasks), len(tasks))
}
```

This is a server-side guard against the reviewer dropping or duplicating tasks across chunks. It surfaces as a normal `validate_plan` error (not partial results), and the caller can retry. No retry loop inside the server — keep the failure mode simple.

## Data flow example — 25-task plan, defaults

1. Parse headings → 25 `RawTask`s. `25 > 8` → chunked path. `K = ceil(25/8) = 4`.
2. **Pass 1** (plan-findings-only): full plan text in; ~500 tokens out. Returns `{plan_verdict, plan_findings, next_action}`.
3. **Pass 2** (chunk 0, tasks `[0:8]`): full plan + enumerated headings for the chunk; ~2,500 tokens out. Returns `{tasks: [8 entries]}`.
4. **Pass 3** (chunk 1, tasks `[8:16]`): same; ~2,500 tokens out.
5. **Pass 4** (chunk 2, tasks `[16:24]`): same; ~2,500 tokens out.
6. **Pass 5** (chunk 3, tasks `[24:25]`): single trailing task; ~350 tokens out.
7. Post-merge validation: `len(result.Tasks) == 25`. Pass.
8. Return 25-task `PlanResult`. Total: **5 sequential reviewer calls** (1 plan-findings + 4 task chunks), all within the 4096 cap.

For a 50-task plan: 1 + `ceil(50/8)` = 1 + 7 = **8 calls** (chunks 8+8+8+8+8+8+2). Linear in task count; bounded by the natural size of the plan.

## Error handling

- Any chunk call returns a non-retryable error (after the inner retry-once on schema decode) → the whole `validate_plan` request returns that error in its envelope. No partial results.
- The existing top-level retry semantics for the parser ("retry once with RetryHint") are preserved **per chunk**, not at the whole-request level.
- Config validation at startup rejects `PlanTasksPerChunk <= 0`, `PlanMaxTokens <= 0`, `PerTaskMaxTokens <= 0`. Server fails fast with a clear message; same pattern as `ANTI_TANGENT_SESSION_TTL` and `ANTI_TANGENT_MAX_PAYLOAD_BYTES`.

## Testing

### Unit tests
- `internal/config/config_test.go`: defaults, valid overrides, invalid-rejection for each new env var.
- `internal/verdict/plan_test.go`: schema validation for `PlanFindingsOnlySchema` and `TasksOnlySchema`; parser round-trips for `ParsePlanFindingsOnly` and `ParseTasksOnly`.
- `internal/prompts/prompts_test.go`: golden tests for both new templates (regenerated via `-update`).
- `internal/mcpsrv/handlers_test.go` (or a new `handlers_plan_test.go`):
  - `reviewPlanChunked` with a fake `Reviewer` returning canned responses:
    - 16 tasks → 1 plan-findings call + 2 task chunks → assert correct ranges, correct merge order.
    - 9 tasks (boundary just above chunk size of 8) → 1 + 2 (last chunk has 1 task) → assert handling of small trailing chunk.
    - 25 tasks → 1 + 4 → assert call count + final `PlanResult` task count.
  - Chunk error mid-stream → whole result is the error.
  - Single-call path (8 tasks exactly) → exactly one call, behaves as today.

### Integration tests
- `internal/mcpsrv/integration_test.go`: 12-task plan end-to-end through chunked path with a stub reviewer; assert public envelope is the same shape consumers expect today.

### E2E (`-tags=e2e`)
- Existing live test stays untouched.
- Add a `TestValidatePlanLarge_E2E` gated on `ANTI_TANGENT_E2E_LARGE=1` (set in addition to provider API keys) that runs a real 25-task plan against the default OpenAI reviewer. Off by default so PRs don't burn provider credits.

## Backward compatibility

- `PlanResult` JSON shape is unchanged. Consumers (Claude Code, opencode, anything else) see the same envelope.
- Plans of 8 tasks or fewer take exactly the single-call code path with `MaxTokens=4096` — identical to v0.1.3.
- New env vars are all optional with safe defaults.
- No changes to existing tool schemas (`validate_task_spec`, `check_progress`, `validate_completion`).
- README / INTEGRATION.md will gain a short "Large plans" section under the validate_plan docs, plus a row in the env-var table for each of the three new vars.

## Release plan

- Branch: `version/0.1.4`.
- `VERSION` → `0.1.4`.
- `CHANGELOG.md` → `## [0.1.4] - 2026-05-11` block under `### Added` (env vars, chunking) and `### Fixed` (large-plan EOF). Add as code lands, per repo convention.
- Merge commit carries `[minor]` (backward-compatible feature). Branch name and merge bump must agree (CI enforces).

## YAGNI / future considerations (explicitly deferred)

- **Parallel chunk execution.** Sequential is fine at this scale. If a future plan size makes it a problem (e.g. 100+ tasks), revisit.
- **Adaptive chunk sizing.** If we see real plans with very dense tasks blowing past `PlanMaxTokens` per chunk, add a truncation-fallback path then. For now, operators tune via env.
- **Streaming JSON parsing.** Same — only worth it if we encounter real-world overflow.
- **Cross-chunk consistency findings.** The dedicated plan-findings pass already has full-plan context, so this is covered.
