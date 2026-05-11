# validate_plan Chunking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `validate_plan` succeed on plans of arbitrary task count by chunking the reviewer's output into multiple sequential calls, eliminating the `decode plan result: EOF` failure mode on plans of ~12+ tasks.

**Architecture:** Always-chunk-above-N strategy. For plans with `len(tasks) > PlanTasksPerChunk` (default 8), the server makes one Pass-1 reviewer call for cross-cutting `plan_findings` and `ceil(n/N)` per-chunk calls for tasks (each carrying the slice of `RawTask` for that chunk and instructing the model to emit results only for those exact headings). The handler merges results into the existing `PlanResult` shape — no consumer-visible change. Plans ≤8 tasks take the existing single-call path unchanged.

**Tech Stack:** Go 1.22+, `text/template`, `encoding/json`, `//go:embed`, `net/http` (existing reviewer clients). Tests use `go test -race ./...` and `httptest.Server`.

**Spec:** [`docs/superpowers/specs/2026-05-11-validate-plan-chunking-design.md`](../specs/2026-05-11-validate-plan-chunking-design.md) (CR-approved at commit `b7ee7df`).

**Branch:** `version/0.1.4` (already created and pushed).

---

## File-by-file impact

| File | Action |
|---|---|
| `internal/config/config.go` | Add 3 fields, parse + validate 3 new env vars |
| `internal/config/config_test.go` | Tests for defaults / overrides / rejection |
| `internal/verdict/plan_findings_only_schema.json` | New embedded schema |
| `internal/verdict/tasks_only_schema.json` | New embedded schema |
| `internal/verdict/plan.go` | Add `PlanFindingsOnly`, `TasksOnly` types + schema fns + parsers |
| `internal/verdict/plan_test.go` | Tests for new schemas + parsers |
| `internal/prompts/templates/plan_findings_only.tmpl` | New template |
| `internal/prompts/templates/plan_tasks_chunk.tmpl` | New template |
| `internal/prompts/prompts.go` | Add `PlanChunkInput`, `RenderPlanFindingsOnly`, `RenderPlanTasksChunk` |
| `internal/prompts/prompts_test.go` | Golden tests for new templates |
| `internal/prompts/testdata/` | New golden files (`-update` will generate) |
| `internal/mcpsrv/handlers.go` | Rename `reviewPlan` → `reviewPlanSingle`, add `reviewPlanChunked`, wire config in `review()` and plan dispatch |
| `internal/mcpsrv/handlers_test.go` | Unit tests for chunked path (boundary cases + identity validation) |
| `internal/mcpsrv/integration_test.go` | 12-task plan integration test |
| `internal/mcpsrv/integration_e2e_test.go` (or add to existing) | 25-task gated E2E test |
| `README.md` | Document 3 new env vars + chunking behavior |
| `INTEGRATION.md` | Same |
| `CHANGELOG.md` | `[0.1.4]` entry with `### Added` and `### Fixed` |
| `VERSION` | `0.1.4` |

---

### Task 1: Config — add per-task / plan max-tokens + per-chunk size

**Goal:** Make the previously-hardcoded `MaxTokens: 4096` literals configurable via three new positive-integer env vars, with safe defaults that preserve v0.1.3 behavior on small plans.

**Acceptance criteria:**
- `config.Config` has new fields `PerTaskMaxTokens int`, `PlanMaxTokens int`, `PlanTasksPerChunk int`.
- Defaults: `PerTaskMaxTokens=4096`, `PlanMaxTokens=4096`, `PlanTasksPerChunk=8` when env vars are unset.
- `ANTI_TANGENT_PER_TASK_MAX_TOKENS`, `ANTI_TANGENT_PLAN_MAX_TOKENS`, `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` each parse as integers and override the default.
- Each env var rejects non-integer input with a clear error.
- Each env var rejects `<= 0` with `"must be positive, got <n>"` error, matching the existing `ANTI_TANGENT_MAX_PAYLOAD_BYTES` pattern.
- Unit tests cover: defaults, valid override (e.g. `16384`, `12`), zero, negative, non-integer.

**Non-goals:**
- No changes to `review()` or `reviewPlan` yet (Task 6 wires them).
- No upper-bound validation (operators may legitimately need 32k+ for large reviewer models).

**Context:** Existing positive-int validation pattern lives at `internal/config/config.go:97-106` (`ANTI_TANGENT_MAX_PAYLOAD_BYTES`). Follow that exactly.

**Files:**
- Modify: `internal/config/config.go` (add fields, defaults, parsing, validation)
- Modify: `internal/config/config_test.go` (add test cases)

- [ ] **Step 1: Write failing tests for the three new fields**

Add to `internal/config/config_test.go` (append to existing test or new test functions matching the file's style; reuse the existing `withEnv` / map-backed lookup pattern visible in current tests):

```go
func TestLoad_TokenBudgetsAndChunkSize_Defaults(t *testing.T) {
    cfg, err := Load(envFromMap(map[string]string{
        "ANTHROPIC_API_KEY": "x",
    }))
    if err != nil {
        t.Fatalf("Load: %v", err)
    }
    if cfg.PerTaskMaxTokens != 4096 {
        t.Errorf("PerTaskMaxTokens default = %d, want 4096", cfg.PerTaskMaxTokens)
    }
    if cfg.PlanMaxTokens != 4096 {
        t.Errorf("PlanMaxTokens default = %d, want 4096", cfg.PlanMaxTokens)
    }
    if cfg.PlanTasksPerChunk != 8 {
        t.Errorf("PlanTasksPerChunk default = %d, want 8", cfg.PlanTasksPerChunk)
    }
}

func TestLoad_TokenBudgetsAndChunkSize_Overrides(t *testing.T) {
    cfg, err := Load(envFromMap(map[string]string{
        "ANTHROPIC_API_KEY":                 "x",
        "ANTI_TANGENT_PER_TASK_MAX_TOKENS":  "8192",
        "ANTI_TANGENT_PLAN_MAX_TOKENS":      "16384",
        "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "12",
    }))
    if err != nil {
        t.Fatalf("Load: %v", err)
    }
    if cfg.PerTaskMaxTokens != 8192 {
        t.Errorf("PerTaskMaxTokens override = %d, want 8192", cfg.PerTaskMaxTokens)
    }
    if cfg.PlanMaxTokens != 16384 {
        t.Errorf("PlanMaxTokens override = %d, want 16384", cfg.PlanMaxTokens)
    }
    if cfg.PlanTasksPerChunk != 12 {
        t.Errorf("PlanTasksPerChunk override = %d, want 12", cfg.PlanTasksPerChunk)
    }
}

func TestLoad_TokenBudgetsAndChunkSize_Reject(t *testing.T) {
    cases := []map[string]string{
        {"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "0"},
        {"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "-1"},
        {"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "abc"},
        {"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_MAX_TOKENS": "0"},
        {"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_MAX_TOKENS": "-100"},
        {"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "0"},
        {"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "-3"},
        {"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "two"},
    }
    for _, env := range cases {
        if _, err := Load(envFromMap(env)); err == nil {
            t.Errorf("Load(%v) should have failed", env)
        }
    }
}
```

If `envFromMap` isn't an existing helper in `config_test.go`, replace each `envFromMap(map[string]string{...})` with `func(k string) string { return m[k] }` inline, matching whatever helper currently exists in the file.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/config/...
```

Expected: tests fail with errors like `PerTaskMaxTokens default = 0, want 4096` (because the fields don't exist / aren't being set yet — compile error or zero values).

- [ ] **Step 3: Add fields and defaults to Config struct**

Edit `internal/config/config.go`. Add three fields to the struct (insert after `RequestTimeout`):

```go
type Config struct {
    AnthropicKey      string
    OpenAIKey         string
    GoogleKey         string
    PreModel          ModelRef
    MidModel          ModelRef
    PostModel         ModelRef
    PlanModel         ModelRef
    SessionTTL        time.Duration
    MaxPayloadBytes   int
    RequestTimeout    time.Duration
    PerTaskMaxTokens  int
    PlanMaxTokens     int
    PlanTasksPerChunk int
    LogLevel          slog.Level
}
```

Add the three defaults to the `cfg := Config{...}` block at the top of `Load`:

```go
cfg := Config{
    AnthropicKey:      env("ANTHROPIC_API_KEY"),
    OpenAIKey:         env("OPENAI_API_KEY"),
    GoogleKey:         env("GOOGLE_API_KEY"),
    SessionTTL:        4 * time.Hour,
    MaxPayloadBytes:   204800,
    RequestTimeout:    120 * time.Second,
    PerTaskMaxTokens:  4096,
    PlanMaxTokens:     4096,
    PlanTasksPerChunk: 8,
    LogLevel:          slog.LevelInfo,
}
```

- [ ] **Step 4: Add parsing + validation for the three env vars**

Still in `internal/config/config.go`, insert before the `ANTI_TANGENT_LOG_LEVEL` block (mirror the existing `MaxPayloadBytes` pattern):

```go
if v := env("ANTI_TANGENT_PER_TASK_MAX_TOKENS"); v != "" {
    n, err := strconv.Atoi(v)
    if err != nil {
        return Config{}, fmt.Errorf("ANTI_TANGENT_PER_TASK_MAX_TOKENS: %w", err)
    }
    if n <= 0 {
        return Config{}, fmt.Errorf("ANTI_TANGENT_PER_TASK_MAX_TOKENS: must be positive, got %d", n)
    }
    cfg.PerTaskMaxTokens = n
}
if v := env("ANTI_TANGENT_PLAN_MAX_TOKENS"); v != "" {
    n, err := strconv.Atoi(v)
    if err != nil {
        return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_TOKENS: %w", err)
    }
    if n <= 0 {
        return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_TOKENS: must be positive, got %d", n)
    }
    cfg.PlanMaxTokens = n
}
if v := env("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK"); v != "" {
    n, err := strconv.Atoi(v)
    if err != nil {
        return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK: %w", err)
    }
    if n <= 0 {
        return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK: must be positive, got %d", n)
    }
    cfg.PlanTasksPerChunk = n
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test -race ./internal/config/...
```

Expected: all tests pass, including the three new ones.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "config: make per-task / plan max-tokens + chunk size configurable

Adds three new positive-integer env vars:
  - ANTI_TANGENT_PER_TASK_MAX_TOKENS (default 4096)
  - ANTI_TANGENT_PLAN_MAX_TOKENS     (default 4096)
  - ANTI_TANGENT_PLAN_TASKS_PER_CHUNK (default 8)

Validation mirrors the existing ANTI_TANGENT_MAX_PAYLOAD_BYTES pattern.
Defaults preserve v0.1.3 behavior; handlers will be wired in a
subsequent commit."
```

---

### Task 2: Verdict — `PlanFindingsOnly` type, schema, parser

**Goal:** Add the types and schema needed for the Pass-1 plan-findings-only reviewer call: a compact response containing `plan_verdict`, `plan_findings`, and `next_action` (no `tasks` array).

**Acceptance criteria:**
- New file `internal/verdict/plan_findings_only_schema.json` is embedded JSON Schema (draft-07) requiring `plan_verdict`, `plan_findings`, `next_action`; **no** `tasks` field; reuses the existing `finding` definition (or duplicates it identically — duplication is fine, matches `plan_schema.json` style).
- `verdict.PlanFindingsOnly` struct has `PlanVerdict Verdict`, `PlanFindings []Finding`, `NextAction string` fields with matching JSON tags.
- `verdict.PlanFindingsOnlySchema() []byte` returns a defensive byte copy of the embedded schema (mirrors `PlanSchema()`).
- `verdict.ParsePlanFindingsOnly(raw json.RawMessage) (PlanFindingsOnly, error)` unmarshals + validates that required fields are present (verdict is one of `pass`/`warn`/`fail`; `next_action` is non-empty).
- Unit tests: schema is valid JSON, returns a defensive copy (mutation of returned slice doesn't affect subsequent calls), parser accepts valid payload, parser rejects missing `plan_verdict`, parser rejects invalid verdict enum.

**Non-goals:**
- No template or handler changes (Tasks 4 and 7).
- Don't share schema definitions across files via `$ref` cross-file resolution; embed everything in this schema (consistent with `plan_schema.json` style).

**Context:** Existing schema pattern is in `internal/verdict/plan.go:5-14` and `internal/verdict/plan_schema.json`. The `finding` definition can be copied verbatim from `plan_schema.json` `definitions/finding`. Existing parser pattern: see `verdict.ParsePlan` for reference if it exists; otherwise model the parser on the unmarshal style used by `verdict.Parse` in `internal/verdict/verdict.go`.

**Files:**
- Create: `internal/verdict/plan_findings_only_schema.json`
- Modify: `internal/verdict/plan.go` (add type, schema func, parser)
- Modify: `internal/verdict/plan_test.go` (add tests)

- [ ] **Step 1: Create the embedded schema file**

Write `internal/verdict/plan_findings_only_schema.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "PlanFindingsOnly",
  "type": "object",
  "required": ["plan_verdict", "plan_findings", "next_action"],
  "additionalProperties": false,
  "properties": {
    "plan_verdict": { "type": "string", "enum": ["pass", "warn", "fail"] },
    "plan_findings": {
      "type": "array",
      "items": { "$ref": "#/definitions/finding" }
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
            "missing_acceptance_criterion",
            "scope_drift",
            "ambiguous_spec",
            "unaddressed_finding",
            "quality",
            "session_not_found",
            "payload_too_large",
            "other"
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

- [ ] **Step 2: Write failing tests**

Append to `internal/verdict/plan_test.go`:

```go
func TestPlanFindingsOnlySchema_IsValidJSON(t *testing.T) {
    var v any
    if err := json.Unmarshal(PlanFindingsOnlySchema(), &v); err != nil {
        t.Fatalf("PlanFindingsOnlySchema not valid JSON: %v", err)
    }
}

func TestPlanFindingsOnlySchema_DefensiveCopy(t *testing.T) {
    a := PlanFindingsOnlySchema()
    if len(a) == 0 {
        t.Fatal("PlanFindingsOnlySchema returned empty")
    }
    a[0] = 0
    b := PlanFindingsOnlySchema()
    if b[0] == 0 {
        t.Fatalf("PlanFindingsOnlySchema not defensively copied")
    }
}

func TestParsePlanFindingsOnly_Valid(t *testing.T) {
    raw := []byte(`{
        "plan_verdict": "warn",
        "plan_findings": [
            {"severity":"major","category":"ambiguous_spec","criterion":"AC clarity","evidence":"task 3","suggestion":"specify measurable target"}
        ],
        "next_action": "Address warnings before dispatching tasks."
    }`)
    r, err := ParsePlanFindingsOnly(raw)
    if err != nil {
        t.Fatalf("ParsePlanFindingsOnly: %v", err)
    }
    if r.PlanVerdict != VerdictWarn {
        t.Errorf("PlanVerdict = %q, want warn", r.PlanVerdict)
    }
    if len(r.PlanFindings) != 1 {
        t.Errorf("len(PlanFindings) = %d, want 1", len(r.PlanFindings))
    }
    if r.NextAction == "" {
        t.Errorf("NextAction empty")
    }
}

func TestParsePlanFindingsOnly_RejectsInvalid(t *testing.T) {
    cases := map[string][]byte{
        "missing plan_verdict": []byte(`{"plan_findings":[],"next_action":"go"}`),
        "invalid verdict enum": []byte(`{"plan_verdict":"maybe","plan_findings":[],"next_action":"go"}`),
        "empty next_action":    []byte(`{"plan_verdict":"pass","plan_findings":[],"next_action":""}`),
    }
    for name, raw := range cases {
        if _, err := ParsePlanFindingsOnly(raw); err == nil {
            t.Errorf("%s: expected error, got nil", name)
        }
    }
}
```

If `VerdictWarn` (or equivalent constant) doesn't exist in the package, use the string literal `"warn"` and adjust the assertion to `if r.PlanVerdict != "warn"`.

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test -race ./internal/verdict/...
```

Expected: compile errors — `undefined: PlanFindingsOnlySchema`, `undefined: ParsePlanFindingsOnly`.

- [ ] **Step 4: Add the type, schema function, and parser**

Append to `internal/verdict/plan.go`:

```go
//go:embed plan_findings_only_schema.json
var planFindingsOnlySchema []byte

// PlanFindingsOnlySchema returns a defensive byte copy of the plan-findings-only
// JSON schema (used by validate_plan's chunking fallback Pass 1).
func PlanFindingsOnlySchema() []byte {
    out := make([]byte, len(planFindingsOnlySchema))
    copy(out, planFindingsOnlySchema)
    return out
}

// PlanFindingsOnly is the Pass-1 response shape during chunked plan review.
// Carries cross-cutting findings and next_action; no per-task data.
type PlanFindingsOnly struct {
    PlanVerdict  Verdict   `json:"plan_verdict"`
    PlanFindings []Finding `json:"plan_findings"`
    NextAction   string    `json:"next_action"`
}

// ParsePlanFindingsOnly unmarshals a Pass-1 reviewer response and validates
// required-field constraints (verdict enum, non-empty next_action).
func ParsePlanFindingsOnly(raw []byte) (PlanFindingsOnly, error) {
    var r PlanFindingsOnly
    if err := json.Unmarshal(raw, &r); err != nil {
        return PlanFindingsOnly{}, fmt.Errorf("decode plan_findings_only: %w", err)
    }
    switch r.PlanVerdict {
    case VerdictPass, VerdictWarn, VerdictFail:
    default:
        return PlanFindingsOnly{}, fmt.Errorf("plan_findings_only: invalid plan_verdict %q", r.PlanVerdict)
    }
    if r.NextAction == "" {
        return PlanFindingsOnly{}, fmt.Errorf("plan_findings_only: next_action must be non-empty")
    }
    return r, nil
}
```

If the imports `encoding/json` and `fmt` aren't already in `plan.go` (currently it only imports `embed`), add them.

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test -race ./internal/verdict/...
```

Expected: all tests pass, including the four new ones.

- [ ] **Step 6: Commit**

```bash
git add internal/verdict/plan_findings_only_schema.json internal/verdict/plan.go internal/verdict/plan_test.go
git commit -m "verdict: add PlanFindingsOnly type, schema, parser

Pass-1 response shape for chunked validate_plan: plan_verdict +
plan_findings + next_action, no tasks array. Embedded JSON schema
mirrors the plan_schema.json finding definition. Used by handler in
a subsequent commit."
```

---

### Task 3: Verdict — `TasksOnly` type, schema, parser

**Goal:** Add the types and schema for per-chunk reviewer calls: a response containing only the `tasks` array (one entry per requested chunk task).

**Acceptance criteria:**
- New file `internal/verdict/tasks_only_schema.json` defines a schema with a single required field `tasks` (array of objects with the same item shape as the existing `plan_schema.json` `tasks[]` items: `task_index`, `task_title`, `verdict`, `findings`, `suggested_header_block`, `suggested_header_reason`).
- `verdict.TasksOnly` struct has `Tasks []PlanTaskResult` field with matching JSON tag.
- `verdict.TasksOnlySchema() []byte` returns a defensive byte copy.
- `verdict.ParseTasksOnly(raw json.RawMessage) (TasksOnly, error)` unmarshals + validates each task's verdict enum and that `task_title` is non-empty (matches `plan_schema.json` `minLength: 1`).
- Unit tests: schema valid JSON, defensive copy, parser accepts valid payload (1+ tasks), parser rejects empty tasks array (the schema's `minItems: 1` constraint), parser rejects invalid verdict enum in a task.

**Non-goals:**
- No template or handler changes (Tasks 5 and 7).

**Context:** Existing item shape is in `internal/verdict/plan_schema.json` lines 13-28. Copy the `tasks[].items` object verbatim and embed the `finding` definition.

**Files:**
- Create: `internal/verdict/tasks_only_schema.json`
- Modify: `internal/verdict/plan.go` (add type, schema func, parser)
- Modify: `internal/verdict/plan_test.go` (add tests)

- [ ] **Step 1: Create the embedded schema file**

Write `internal/verdict/tasks_only_schema.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "TasksOnly",
  "type": "object",
  "required": ["tasks"],
  "additionalProperties": false,
  "properties": {
    "tasks": {
      "type": "array",
      "minItems": 1,
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
    }
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
            "missing_acceptance_criterion",
            "scope_drift",
            "ambiguous_spec",
            "unaddressed_finding",
            "quality",
            "session_not_found",
            "payload_too_large",
            "other"
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

- [ ] **Step 2: Write failing tests**

Append to `internal/verdict/plan_test.go`:

```go
func TestTasksOnlySchema_IsValidJSON(t *testing.T) {
    var v any
    if err := json.Unmarshal(TasksOnlySchema(), &v); err != nil {
        t.Fatalf("TasksOnlySchema not valid JSON: %v", err)
    }
}

func TestTasksOnlySchema_DefensiveCopy(t *testing.T) {
    a := TasksOnlySchema()
    if len(a) == 0 {
        t.Fatal("TasksOnlySchema returned empty")
    }
    a[0] = 0
    b := TasksOnlySchema()
    if b[0] == 0 {
        t.Fatal("TasksOnlySchema not defensively copied")
    }
}

func TestParseTasksOnly_Valid(t *testing.T) {
    raw := []byte(`{
        "tasks": [
            {"task_index":1,"task_title":"Task 1: foo","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},
            {"task_index":2,"task_title":"Task 2: bar","verdict":"warn","findings":[],"suggested_header_block":"","suggested_header_reason":""}
        ]
    }`)
    r, err := ParseTasksOnly(raw)
    if err != nil {
        t.Fatalf("ParseTasksOnly: %v", err)
    }
    if len(r.Tasks) != 2 {
        t.Errorf("len(Tasks) = %d, want 2", len(r.Tasks))
    }
    if r.Tasks[0].TaskTitle != "Task 1: foo" {
        t.Errorf("Tasks[0].TaskTitle = %q, want \"Task 1: foo\"", r.Tasks[0].TaskTitle)
    }
}

func TestParseTasksOnly_RejectsInvalid(t *testing.T) {
    cases := map[string][]byte{
        "empty tasks":    []byte(`{"tasks":[]}`),
        "missing title":  []byte(`{"tasks":[{"task_index":1,"task_title":"","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}]}`),
        "bad verdict":    []byte(`{"tasks":[{"task_index":1,"task_title":"Task 1: x","verdict":"maybe","findings":[],"suggested_header_block":"","suggested_header_reason":""}]}`),
    }
    for name, raw := range cases {
        if _, err := ParseTasksOnly(raw); err == nil {
            t.Errorf("%s: expected error, got nil", name)
        }
    }
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test -race ./internal/verdict/...
```

Expected: compile errors (`undefined: TasksOnlySchema`, etc.).

- [ ] **Step 4: Add the type, schema function, and parser**

Append to `internal/verdict/plan.go`:

```go
//go:embed tasks_only_schema.json
var tasksOnlySchema []byte

// TasksOnlySchema returns a defensive byte copy of the per-chunk reviewer
// response schema (used by validate_plan's chunking fallback Passes 2..K+1).
func TasksOnlySchema() []byte {
    out := make([]byte, len(tasksOnlySchema))
    copy(out, tasksOnlySchema)
    return out
}

// TasksOnly is the per-chunk response shape during chunked plan review.
type TasksOnly struct {
    Tasks []PlanTaskResult `json:"tasks"`
}

// ParseTasksOnly unmarshals a per-chunk reviewer response and validates
// required-field constraints (non-empty tasks, valid verdict enum, non-empty
// task_title).
func ParseTasksOnly(raw []byte) (TasksOnly, error) {
    var r TasksOnly
    if err := json.Unmarshal(raw, &r); err != nil {
        return TasksOnly{}, fmt.Errorf("decode tasks_only: %w", err)
    }
    if len(r.Tasks) == 0 {
        return TasksOnly{}, fmt.Errorf("tasks_only: tasks array must be non-empty")
    }
    for i, t := range r.Tasks {
        switch t.Verdict {
        case VerdictPass, VerdictWarn, VerdictFail:
        default:
            return TasksOnly{}, fmt.Errorf("tasks_only: tasks[%d]: invalid verdict %q", i, t.Verdict)
        }
        if t.TaskTitle == "" {
            return TasksOnly{}, fmt.Errorf("tasks_only: tasks[%d]: task_title must be non-empty", i)
        }
    }
    return r, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test -race ./internal/verdict/...
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/verdict/tasks_only_schema.json internal/verdict/plan.go internal/verdict/plan_test.go
git commit -m "verdict: add TasksOnly type, schema, parser

Per-chunk response shape for chunked validate_plan. Items mirror the
existing plan_schema.json tasks[] shape. Parser validates non-empty
tasks array, verdict enum, and non-empty task_title."
```

---

### Task 4: Prompts — `RenderPlanFindingsOnly` template + golden

**Goal:** Add a render function and template that produce the Pass-1 plan-findings-only prompt: the reviewer sees the full plan text and is instructed to return only `plan_verdict`, `plan_findings`, and `next_action`.

**Acceptance criteria:**
- New file `internal/prompts/templates/plan_findings_only.tmpl` exists and embeds `{{.PlanText}}` plus instructions explicitly forbidding `tasks[]` output.
- New function `RenderPlanFindingsOnly(in PlanInput) (Output, error)` exists in `internal/prompts/prompts.go`, returns `Output{System: systemPrompt, User: <rendered>}`.
- Golden test `TestRenderPlanFindingsOnly_Golden` reads expected content from `internal/prompts/testdata/plan_findings_only.golden` and compares against rendered output for a fixed `PlanInput`.
- Running `go test ./internal/prompts/... -update` regenerates the golden; a diff review before commit confirms the content is correct.

**Non-goals:**
- No new `PlanInput` field (existing `PlanText string` is sufficient for Pass 1).

**Context:** Existing render pattern at `internal/prompts/prompts.go:76-82` (`RenderPlan`). Existing template style at `internal/prompts/templates/plan.tmpl` (read first to match tone — system says "you are an exacting reviewer", template gives plan + instructions + "Respond with a JSON object matching the provided schema").

**Files:**
- Create: `internal/prompts/templates/plan_findings_only.tmpl`
- Create: `internal/prompts/testdata/plan_findings_only.golden` (generated via `-update`)
- Modify: `internal/prompts/prompts.go` (add `RenderPlanFindingsOnly`)
- Modify: `internal/prompts/prompts_test.go` (add golden test)

- [ ] **Step 1: Create the template**

Write `internal/prompts/templates/plan_findings_only.tmpl`:

```
## Plan under review

{{.PlanText}}

## What to evaluate

You are reviewing an entire implementation plan BEFORE any tasks are dispatched.
Your job in THIS call is **plan-level analysis only** — cross-cutting findings
that require visibility into the whole plan. Per-task analysis happens in
separate follow-up calls; do not emit per-task data here.

Specifically:

1. **plan_verdict** — overall: `pass`, `warn`, or `fail` based on plan-wide
   issues only (not per-task quality).
2. **plan_findings** — cross-cutting issues only. Examples:
   - Two tasks have overlapping or contradictory acceptance criteria.
   - A task depends on something not produced by any earlier task.
   - The plan is missing an architecture/intro section that an implementer
     would need.
   - Duplicate task titles.
   - Tasks out of logical order (e.g. tests written before the code they test
     when the codebase is TDD-strict, or deployment before testing).
   Do NOT emit findings about individual task quality (vague AC on one task,
   missing Non-goals on one task, etc.) — those are evaluated separately.
3. **next_action** — one sentence telling the controller what to do next,
   given the plan-level verdict.

Severity guide: critical = controller should not dispatch any task until
resolved; major = controller should resolve before dispatching but plan is
otherwise structurally sound; minor = nit.

## Output

Respond with a JSON object matching the provided schema. Do not include the
`tasks` field. Do not emit per-task findings — they belong in the per-task
calls. Do not include prose outside the JSON.
```

- [ ] **Step 2: Write the failing golden test**

Append to `internal/prompts/prompts_test.go` (match the existing golden-test style — look at the file first to see how the `-update` flag and `goldenPath` helpers work; if there's a helper like `assertGolden(t, name, actual)`, use it):

```go
func TestRenderPlanFindingsOnly_Golden(t *testing.T) {
    out, err := RenderPlanFindingsOnly(PlanInput{
        PlanText: "## Phase 1\n\n### Task 1: do thing\n\n**Goal:** thing\n\n**Acceptance criteria:**\n- thing happens\n",
    })
    if err != nil {
        t.Fatalf("RenderPlanFindingsOnly: %v", err)
    }
    assertGolden(t, "plan_findings_only.golden", out.User)
}
```

If `assertGolden` doesn't exist, use whatever the file actually uses (likely `compareGolden`, or inline `os.ReadFile` + compare with `-update` flag handling).

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test -race ./internal/prompts/...
```

Expected: compile error — `undefined: RenderPlanFindingsOnly`.

- [ ] **Step 4: Add the render function**

Append to `internal/prompts/prompts.go`:

```go
// RenderPlanFindingsOnly produces the Pass-1 prompt for the chunked validate_plan
// path: full plan as context, plan-level findings only, no per-task data.
func RenderPlanFindingsOnly(in PlanInput) (Output, error) {
    body, err := render("plan_findings_only.tmpl", in)
    if err != nil {
        return Output{}, err
    }
    return Output{System: systemPrompt, User: body}, nil
}
```

- [ ] **Step 5: Generate the golden file and verify content**

```bash
go test ./internal/prompts/... -update
```

This regenerates `internal/prompts/testdata/plan_findings_only.golden`. Read it back to verify it contains the expected rendered prompt (plan text in the middle, instructions, JSON-only output directive).

```bash
cat internal/prompts/testdata/plan_findings_only.golden
```

Expected: the rendered template body — plan text + the "What to evaluate" + "Output" sections.

- [ ] **Step 6: Re-run tests to verify they pass**

```bash
go test -race ./internal/prompts/...
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/templates/plan_findings_only.tmpl internal/prompts/prompts.go internal/prompts/prompts_test.go internal/prompts/testdata/plan_findings_only.golden
git commit -m "prompts: add RenderPlanFindingsOnly template + golden

Pass-1 prompt for chunked validate_plan. Full plan as context;
reviewer is instructed to emit plan-level findings only (no tasks[],
no per-task analysis)."
```

---

### Task 5: Prompts — `PlanChunkInput` + `RenderPlanTasksChunk` template + golden

**Goal:** Add the per-chunk render path: a new input type carrying the chunk's `[]planparser.RawTask` and a template that enumerates the exact heading titles the reviewer must emit results for.

**Acceptance criteria:**
- New `PlanChunkInput` struct in `internal/prompts/prompts.go` with fields `PlanText string` and `ChunkTasks []planparser.RawTask`.
- New file `internal/prompts/templates/plan_tasks_chunk.tmpl` embeds the full plan text, then iterates `{{range .ChunkTasks}}` to render a bulleted list of heading titles, then instructs the reviewer to emit results **only** for those tasks.
- New function `RenderPlanTasksChunk(in PlanChunkInput) (Output, error)` exists, mirrors `RenderPlan`.
- Golden test reads expected content from `internal/prompts/testdata/plan_tasks_chunk.golden` for a fixed input of 2 chunk tasks; running `-update` regenerates it.

**Non-goals:**
- Do not pass integer ranges (`RangeStart`/`RangeEnd`) — see spec for the rationale (non-contiguous `### Task N:` numbering).

**Context:** `planparser.RawTask` is defined in `internal/planparser/planparser.go`. Its `Title` field carries strings like `"Task 4: Add /healthz endpoint"`.

**Files:**
- Create: `internal/prompts/templates/plan_tasks_chunk.tmpl`
- Create: `internal/prompts/testdata/plan_tasks_chunk.golden` (via `-update`)
- Modify: `internal/prompts/prompts.go` (add `PlanChunkInput`, `RenderPlanTasksChunk`, import `planparser`)
- Modify: `internal/prompts/prompts_test.go` (add golden test)

- [ ] **Step 1: Create the template**

Write `internal/prompts/templates/plan_tasks_chunk.tmpl`:

```
## Plan under review

{{.PlanText}}

## What to evaluate (this call: per-task analysis, restricted to a subset)

You are reviewing an implementation plan. You have the **full plan** above as
context, so you can reason about cross-task dependencies — but in this call
you must emit per-task results for **only the following tasks**, identified
by their `### Task N: Title` heading:

{{range .ChunkTasks -}}
- {{.Title}}
{{end}}
For each task in the list above, evaluate:

1. **Structural completeness**: Goal / Acceptance criteria / (optional Non-goals
   and Context) blocks present and well-formed.
2. **Acceptance criteria quality**: each AC is testable, specific, and
   unambiguous; ACs are observable outcomes, not implementation steps.
3. **Unstated assumptions**: knowledge a fresh implementer would lack.

If a task is missing the structured header block entirely, synthesize one in
`suggested_header_block` (Goal + ACs + optional Non-goals/Context) and explain
your reasoning in `suggested_header_reason`. If the header is already adequate,
leave both empty.

Severity: critical = unimplementable as written; major = implementer would
misimplement; minor = nit.

## Output

Respond with a JSON object matching the provided schema. The `tasks` array
must contain exactly one entry per task in the list above, **in the same
order**, with `task_title` matching the heading text verbatim. Do NOT emit
results for any task outside the list. Do NOT emit `plan_findings` — those
are handled in a separate call. Do not include prose outside the JSON.
```

- [ ] **Step 2: Add the `PlanChunkInput` type and `RenderPlanTasksChunk` function**

Edit `internal/prompts/prompts.go`. Add the import (if not already present):

```go
import (
    // ... existing imports ...
    "github.com/patiently/anti-tangent-mcp/internal/planparser"
)
```

Add the type after `PlanInput`:

```go
// PlanChunkInput is the input for one per-task chunk in chunked validate_plan.
// ChunkTasks carries the exact subset of tasks the reviewer should emit
// results for; PlanText carries the full plan for cross-task reasoning.
type PlanChunkInput struct {
    PlanText   string
    ChunkTasks []planparser.RawTask
}
```

Add the render function alongside `RenderPlan`:

```go
// RenderPlanTasksChunk produces a per-chunk prompt for the chunked validate_plan
// path: full plan as context, but the reviewer is instructed to emit results
// only for the subset of tasks in ChunkTasks.
func RenderPlanTasksChunk(in PlanChunkInput) (Output, error) {
    body, err := render("plan_tasks_chunk.tmpl", in)
    if err != nil {
        return Output{}, err
    }
    return Output{System: systemPrompt, User: body}, nil
}
```

- [ ] **Step 3: Write the failing golden test**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPlanTasksChunk_Golden(t *testing.T) {
    out, err := RenderPlanTasksChunk(PlanChunkInput{
        PlanText: "## Phase 1\n\n### Task 1: do thing\n\n### Task 2: do other thing\n",
        ChunkTasks: []planparser.RawTask{
            {Title: "Task 1: do thing", Body: "### Task 1: do thing\n"},
            {Title: "Task 2: do other thing", Body: "### Task 2: do other thing\n"},
        },
    })
    if err != nil {
        t.Fatalf("RenderPlanTasksChunk: %v", err)
    }
    assertGolden(t, "plan_tasks_chunk.golden", out.User)
}
```

Adjust import in `prompts_test.go` to include `planparser` if not already present.

- [ ] **Step 4: Run tests to verify they fail**

```bash
go test -race ./internal/prompts/...
```

Expected: golden file missing → test fails with "golden file not found" or similar. If the file exists, the test fails on content mismatch.

- [ ] **Step 5: Generate the golden file and review it**

```bash
go test ./internal/prompts/... -update
cat internal/prompts/testdata/plan_tasks_chunk.golden
```

Expected: rendered template with both task titles enumerated as bullets, plus the "emit only these tasks" instruction.

- [ ] **Step 6: Re-run tests to verify they pass**

```bash
go test -race ./internal/prompts/...
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/templates/plan_tasks_chunk.tmpl internal/prompts/prompts.go internal/prompts/prompts_test.go internal/prompts/testdata/plan_tasks_chunk.golden
git commit -m "prompts: add RenderPlanTasksChunk template + golden

Per-chunk prompt for chunked validate_plan. PlanChunkInput carries
the full plan plus the slice of RawTask for the chunk; template
enumerates each task's heading title and instructs the reviewer to
emit results only for those tasks."
```

---

### Task 6: Handlers — wire config max-tokens; rename `reviewPlan` to `reviewPlanSingle`

**Goal:** Replace the two hardcoded `MaxTokens: 4096` literals in `handlers.go` with reads from `h.deps.Cfg.PerTaskMaxTokens` and `h.deps.Cfg.PlanMaxTokens`, and rename `reviewPlan` to `reviewPlanSingle` while changing its signature to take `planText string` (rendering moves inside). The single-call dispatch path remains the only path for plans of any size at this stage — chunking is added in Task 7.

**Acceptance criteria:**
- `review()` (currently around line 98-128) reads `h.deps.Cfg.PerTaskMaxTokens` instead of the literal `4096`.
- `reviewPlan` is renamed `reviewPlanSingle`, takes `planText string` instead of `prompts.Output`, and calls `prompts.RenderPlan(prompts.PlanInput{PlanText: planText})` internally. It uses `h.deps.Cfg.PlanMaxTokens`.
- The `ValidatePlan` handler (around line 384-398) no longer pre-renders via `prompts.RenderPlan`; it just calls `reviewPlanSingle(ctx, model, args.PlanText)`.
- All existing tests in `internal/mcpsrv/` still pass.
- A new unit test confirms that `MaxTokens` propagation works: a fake reviewer captures the `providers.Request` it receives and the test asserts `req.MaxTokens` matches the configured value (e.g. set `cfg.PerTaskMaxTokens = 7777` and assert).

**Non-goals:**
- No chunking dispatch yet (Task 7).
- No changes to `validate_task_spec` / `check_progress` / `validate_completion` external behavior — only their internal MaxTokens source.

**Context:** Current code at `internal/mcpsrv/handlers.go:109` (`MaxTokens: 4096` in `review()`) and `:413` (`MaxTokens: 4096` in `reviewPlan`). Dispatch site at `:389` does the `prompts.RenderPlan` call.

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/mcpsrv/handlers_test.go` (add MaxTokens propagation test; optionally extend an existing test if there's a fake reviewer helper already)

- [ ] **Step 1: Write the failing MaxTokens propagation test**

Read `internal/mcpsrv/handlers_test.go` first to learn the testing harness (look for a `fakeReviewer` or similar). If a captured-request fake exists, reuse it. If not, define a minimal one inline:

```go
type captureReviewer struct {
    LastRequest providers.Request
    Response    providers.Response
}

func (c *captureReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
    c.LastRequest = req
    return c.Response, nil
}
```

Add a test that asserts the configured `PerTaskMaxTokens` flows into the reviewer request. The exact shape depends on the existing harness — match how other handler tests assemble `handlers` with a custom `Reviews` registry. Sketch:

```go
func TestValidateTaskSpec_UsesConfiguredPerTaskMaxTokens(t *testing.T) {
    cap := &captureReviewer{Response: providers.Response{
        // A valid Result JSON that passes verdict.Parse — copy from an existing test.
        RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"Proceed."}`),
        Model:   "test-model",
    }}
    h := newTestHandlers(t, withConfig(func(c *config.Config) {
        c.PerTaskMaxTokens = 7777
    }), withReviewer("anthropic", cap))

    // Call validate_task_spec with a minimal valid spec — copy from existing test.
    // ... existing call shape ...

    if cap.LastRequest.MaxTokens != 7777 {
        t.Errorf("MaxTokens propagated = %d, want 7777", cap.LastRequest.MaxTokens)
    }
}

func TestValidatePlan_UsesConfiguredPlanMaxTokens(t *testing.T) {
    cap := &captureReviewer{Response: providers.Response{
        RawJSON: []byte(`{
            "plan_verdict":"pass","plan_findings":[],"tasks":[],
            "next_action":"Proceed with implementation."
        }`),
        Model: "test-model",
    }}
    h := newTestHandlers(t, withConfig(func(c *config.Config) {
        c.PlanMaxTokens = 8888
    }), withReviewer("anthropic", cap))

    // Call validate_plan with a no-task plan ("no headings" path) OR a single-task plan
    // depending on what the existing tests do. Use a single-task plan to ensure
    // the reviewer actually gets called.
    // ... existing call shape ...

    if cap.LastRequest.MaxTokens != 8888 {
        t.Errorf("plan MaxTokens propagated = %d, want 8888", cap.LastRequest.MaxTokens)
    }
}
```

If `newTestHandlers` / `withConfig` / `withReviewer` helpers don't exist, write the test by directly constructing the `handlers` struct in the way other tests in the file do. The key invariant is: assert that `cap.LastRequest.MaxTokens` matches the configured value.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/mcpsrv/...
```

Expected: tests fail because `MaxTokens` is still hardcoded to 4096.

- [ ] **Step 3: Wire config into `review()`**

Edit `internal/mcpsrv/handlers.go`, find the `review` function (around line 98). Change:

```go
req := providers.Request{
    Model:      model.Model,
    System:     p.System,
    User:       p.User,
    MaxTokens:  4096,
    JSONSchema: verdict.Schema(),
}
```

to:

```go
req := providers.Request{
    Model:      model.Model,
    System:     p.System,
    User:       p.User,
    MaxTokens:  h.deps.Cfg.PerTaskMaxTokens,
    JSONSchema: verdict.Schema(),
}
```

- [ ] **Step 4: Rename `reviewPlan` → `reviewPlanSingle` and change its signature**

Find the `reviewPlan` function (around line 403). Rename it to `reviewPlanSingle` and change the signature so it takes `planText string` and renders internally:

```go
// reviewPlanSingle runs one reviewer call for the entire plan — the
// behavior used today for plans whose task count is at or below
// h.deps.Cfg.PlanTasksPerChunk. Renders the prompt internally.
func (h *handlers) reviewPlanSingle(ctx context.Context, model config.ModelRef, planText string) (verdict.PlanResult, string, int64, error) {
    rendered, err := prompts.RenderPlan(prompts.PlanInput{PlanText: planText})
    if err != nil {
        return verdict.PlanResult{}, "", 0, fmt.Errorf("render plan prompt: %w", err)
    }
    rv, err := h.deps.Reviews.Get(model.Provider)
    if err != nil {
        return verdict.PlanResult{}, "", 0, err
    }
    start := time.Now()
    req := providers.Request{
        Model:      model.Model,
        System:     rendered.System,
        User:       rendered.User,
        MaxTokens:  h.deps.Cfg.PlanMaxTokens,
        JSONSchema: verdict.PlanSchema(),
    }
    resp, err := rv.Review(ctx, req)
    if err != nil {
        return verdict.PlanResult{}, "", 0, err
    }
    r, err := verdict.ParsePlan(resp.RawJSON)
    if err != nil {
        // One retry with explicit reminder.
        req.User = rendered.User + "\n\n" + verdict.RetryHint()
        resp, err = rv.Review(ctx, req)
        if err != nil {
            return verdict.PlanResult{}, "", 0, err
        }
        r, err = verdict.ParsePlan(resp.RawJSON)
        if err != nil {
            return verdict.PlanResult{}, "", 0, fmt.Errorf("plan provider response failed schema after retry: %w", err)
        }
    }
    modelUsed := model.Provider + ":" + resp.Model
    if resp.Model == "" {
        modelUsed = model.String()
    }
    return r, modelUsed, time.Since(start).Milliseconds(), nil
}
```

- [ ] **Step 5: Update the `ValidatePlan` dispatch site**

In the `ValidatePlan` handler (around line 360-398), remove the `prompts.RenderPlan` call and the `rendered` variable, and call `reviewPlanSingle` with the raw plan text. After the change, that section should read like:

```go
model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PlanModel)
if err != nil {
    return nil, verdict.PlanResult{}, err
}

pr, modelUsed, ms, err := h.reviewPlanSingle(ctx, model, args.PlanText)
if err != nil {
    return nil, verdict.PlanResult{}, err
}
return planEnvelopeResult(pr, modelUsed, ms)
```

(The earlier portions — `args` parsing, payload-size check, heading parsing, `noHeadingsPlanResult` short-circuit — remain unchanged.)

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test -race ./...
```

Expected: all existing tests still pass, plus the two new MaxTokens-propagation tests now pass.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "mcpsrv: wire config max-tokens; rename reviewPlan → reviewPlanSingle

review() now reads PerTaskMaxTokens from config; the renamed
reviewPlanSingle reads PlanMaxTokens and now takes planText string
(rendering moved inside the helper for symmetry with the chunked
path added in a subsequent commit). No external behavior change yet."
```

---

### Task 7: Handlers — add `reviewPlanChunked`

**Goal:** Implement the chunked plan review path: Pass 1 (plan-findings-only) plus `ceil(n/N)` per-chunk calls, with per-chunk identity validation, post-merge count check, and a merged `PlanResult` indistinguishable from the single-call shape.

**Acceptance criteria:**
- New helper `reviewPlanChunked(ctx, model, planText, tasks, chunkSize) (verdict.PlanResult, string, int64, error)` exists in `handlers.go`.
- It makes exactly `1 + ceil(len(tasks)/chunkSize)` reviewer calls in sequence.
- Pass 1 uses `prompts.RenderPlanFindingsOnly` and `verdict.PlanFindingsOnlySchema()`; per-chunk passes use `prompts.RenderPlanTasksChunk` and `verdict.TasksOnlySchema()`.
- Each call uses `h.deps.Cfg.PlanMaxTokens` for `MaxTokens`.
- Each call preserves the existing schema-retry-once pattern (malformed JSON → retry with `verdict.RetryHint()`).
- After each per-chunk call, the handler validates **identity**: every returned task's `task_title` must match (via case-sensitive equality with leading/trailing whitespace trimmed) one of the chunk's `RawTask.Title` values, and the response contains exactly `len(chunkTasks)` entries. Identity mismatch triggers the retry-once path; a second failure returns an error.
- Merge: `result.PlanVerdict` and `result.PlanFindings` come from Pass 1; `result.NextAction` is Pass 1's value verbatim (no synthesized fallback); `result.Tasks` is the concatenation of per-chunk results in chunk order.
- `modelUsed` is taken from the first reviewer response (Pass 1), with `model.String()` fallback if `resp.Model` is empty (mirrors `reviewPlanSingle`).
- `review_ms` is the sum of all individual call durations.
- Post-merge count check: if `len(result.Tasks) != len(tasks)`, return an error (`"chunked plan review returned %d task results, expected %d"`).

**Non-goals:**
- No parallel execution (sequential only).
- No dispatch site changes yet — `ValidatePlan` still calls only `reviewPlanSingle`. The dispatch wiring is Task 8.
- No streaming JSON; no truncation detection.

**Context:** This helper sits beside `reviewPlanSingle`. Use the chunk slicing rule from the spec: `tasks[i*N : min((i+1)*N, n)]` for `i = 0..K-1`. The `RetryHint` is in `internal/verdict/verdict.go` (see how `reviewPlanSingle` uses it).

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (add `reviewPlanChunked`)

- [ ] **Step 1: Add `reviewPlanChunked` (full implementation)**

Append to `internal/mcpsrv/handlers.go`:

```go
// reviewPlanChunked runs Pass 1 (plan-findings-only) plus one per-chunk call
// per ceil(len(tasks)/chunkSize) batches of tasks. Each per-chunk call carries
// the full plan as context but instructs the reviewer to emit results only for
// the tasks in the chunk. Results merge into a PlanResult identical in shape
// to the single-call path.
func (h *handlers) reviewPlanChunked(
    ctx context.Context,
    model config.ModelRef,
    planText string,
    tasks []planparser.RawTask,
    chunkSize int,
) (verdict.PlanResult, string, int64, error) {
    rv, err := h.deps.Reviews.Get(model.Provider)
    if err != nil {
        return verdict.PlanResult{}, "", 0, err
    }

    var totalMs int64
    var modelUsed string

    // ----- Pass 1: plan-findings only -----
    rendered, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{PlanText: planText})
    if err != nil {
        return verdict.PlanResult{}, "", 0, fmt.Errorf("render plan_findings_only: %w", err)
    }
    req := providers.Request{
        Model:      model.Model,
        System:     rendered.System,
        User:       rendered.User,
        MaxTokens:  h.deps.Cfg.PlanMaxTokens,
        JSONSchema: verdict.PlanFindingsOnlySchema(),
    }
    start := time.Now()
    resp, err := rv.Review(ctx, req)
    if err != nil {
        return verdict.PlanResult{}, "", 0, err
    }
    pf, err := verdict.ParsePlanFindingsOnly(resp.RawJSON)
    if err != nil {
        req.User = rendered.User + "\n\n" + verdict.RetryHint()
        resp, err = rv.Review(ctx, req)
        if err != nil {
            return verdict.PlanResult{}, "", 0, err
        }
        pf, err = verdict.ParsePlanFindingsOnly(resp.RawJSON)
        if err != nil {
            return verdict.PlanResult{}, "", 0, fmt.Errorf("plan_findings_only failed schema after retry: %w", err)
        }
    }
    totalMs += time.Since(start).Milliseconds()
    modelUsed = model.Provider + ":" + resp.Model
    if resp.Model == "" {
        modelUsed = model.String()
    }

    result := verdict.PlanResult{
        PlanVerdict:  pf.PlanVerdict,
        PlanFindings: pf.PlanFindings,
        NextAction:   pf.NextAction,
        Tasks:        make([]verdict.PlanTaskResult, 0, len(tasks)),
    }

    // ----- Passes 2..K+1: per-task chunks -----
    n := len(tasks)
    for i := 0; i < n; i += chunkSize {
        end := i + chunkSize
        if end > n {
            end = n
        }
        chunkTasks := tasks[i:end]

        chunkResult, ms, err := h.reviewOnePlanChunk(ctx, rv, model, planText, chunkTasks)
        if err != nil {
            return verdict.PlanResult{}, "", 0, err
        }
        totalMs += ms
        result.Tasks = append(result.Tasks, chunkResult.Tasks...)
    }

    if len(result.Tasks) != len(tasks) {
        return verdict.PlanResult{}, "", 0,
            fmt.Errorf("chunked plan review returned %d task results, expected %d",
                len(result.Tasks), len(tasks))
    }

    return result, modelUsed, totalMs, nil
}

// reviewOnePlanChunk runs one per-chunk reviewer call with identity validation
// and the existing schema-retry-once pattern.
func (h *handlers) reviewOnePlanChunk(
    ctx context.Context,
    rv providers.Reviewer,
    model config.ModelRef,
    planText string,
    chunkTasks []planparser.RawTask,
) (verdict.TasksOnly, int64, error) {
    rendered, err := prompts.RenderPlanTasksChunk(prompts.PlanChunkInput{
        PlanText:   planText,
        ChunkTasks: chunkTasks,
    })
    if err != nil {
        return verdict.TasksOnly{}, 0, fmt.Errorf("render plan_tasks_chunk: %w", err)
    }

    req := providers.Request{
        Model:      model.Model,
        System:     rendered.System,
        User:       rendered.User,
        MaxTokens:  h.deps.Cfg.PlanMaxTokens,
        JSONSchema: verdict.TasksOnlySchema(),
    }

    // Build the expected-title set once per chunk for identity validation.
    expected := make(map[string]struct{}, len(chunkTasks))
    for _, t := range chunkTasks {
        expected[strings.TrimSpace(t.Title)] = struct{}{}
    }

    attempt := func(user string) (verdict.TasksOnly, int64, error) {
        req.User = user
        start := time.Now()
        resp, err := rv.Review(ctx, req)
        if err != nil {
            return verdict.TasksOnly{}, 0, err
        }
        ms := time.Since(start).Milliseconds()
        parsed, err := verdict.ParseTasksOnly(resp.RawJSON)
        if err != nil {
            return verdict.TasksOnly{}, ms, err
        }
        if err := validateChunkIdentity(parsed, expected, len(chunkTasks)); err != nil {
            return verdict.TasksOnly{}, ms, err
        }
        return parsed, ms, nil
    }

    parsed, ms, err := attempt(rendered.User)
    if err == nil {
        return parsed, ms, nil
    }
    // Schema or identity failure → retry once with hint.
    parsed2, ms2, err2 := attempt(rendered.User + "\n\n" + verdict.RetryHint())
    if err2 != nil {
        return verdict.TasksOnly{}, ms + ms2, fmt.Errorf("plan_tasks_chunk failed after retry: %w", err2)
    }
    return parsed2, ms + ms2, nil
}

// validateChunkIdentity checks that the parsed chunk response contains exactly
// the expected number of tasks and that every returned task_title is in the
// expected set. Returns a descriptive error on any mismatch.
func validateChunkIdentity(parsed verdict.TasksOnly, expected map[string]struct{}, want int) error {
    if len(parsed.Tasks) != want {
        return fmt.Errorf("chunk identity: got %d tasks, expected %d", len(parsed.Tasks), want)
    }
    for i, t := range parsed.Tasks {
        title := strings.TrimSpace(t.TaskTitle)
        if _, ok := expected[title]; !ok {
            return fmt.Errorf("chunk identity: tasks[%d].task_title %q not in requested chunk", i, title)
        }
    }
    return nil
}
```

Add `"strings"` to the imports if not already present.

- [ ] **Step 2: Build to verify it compiles**

```bash
go build ./...
```

Expected: clean build, no errors.

- [ ] **Step 3: Run all tests to verify nothing regressed**

```bash
go test -race ./...
```

Expected: all existing tests still pass (no new tests yet for `reviewPlanChunked` — Task 9 covers those).

- [ ] **Step 4: Commit**

```bash
git add internal/mcpsrv/handlers.go
git commit -m "mcpsrv: add reviewPlanChunked (no dispatch wiring yet)

Implements Pass-1 plan-findings-only + ceil(n/N) per-chunk calls
with identity validation and post-merge count check. Each chunk
preserves the existing schema-retry-once pattern. Not yet wired
into ValidatePlan dispatch — that's the next commit."
```

---

### Task 8: Handlers — dispatch chunked path in `ValidatePlan`

**Goal:** Wire `reviewPlanChunked` into the `ValidatePlan` handler so that plans with `len(tasks) > PlanTasksPerChunk` route to the chunked path while plans at or below the threshold continue to use `reviewPlanSingle` unchanged.

**Acceptance criteria:**
- The `ValidatePlan` handler dispatches based on `len(tasks) <= h.deps.Cfg.PlanTasksPerChunk`.
- For plans below or at threshold: `reviewPlanSingle` is called (current behavior).
- For plans above threshold: `reviewPlanChunked` is called with the full slice of `RawTask` and `PlanTasksPerChunk`.
- The MCP envelope returned to the caller is the same shape in both branches (`planEnvelopeResult(pr, modelUsed, ms)`).
- An existing test (or a new minimal test) confirms that for an above-threshold plan, the chunked path is taken. A captureReviewer can assert call count: 1 (single) vs. 1 + ceil(n/N) (chunked).

**Non-goals:**
- No new tests for chunked-path correctness here (Task 9 does that).
- No fancy logging.

**Context:** `tasks` is already in scope at the dispatch point (see `handlers.go:380`).

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/mcpsrv/handlers_test.go` (add dispatch-routing test)

- [ ] **Step 1: Write the failing routing test**

Append to `internal/mcpsrv/handlers_test.go`. Use a captureReviewer that counts calls; assert call count is 1 for a small plan and >1 for a large plan.

```go
type countingReviewer struct {
    calls    int
    response providers.Response
}

func (c *countingReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
    c.calls++
    return c.response, nil
}

func TestValidatePlan_DispatchesToChunkedAboveThreshold(t *testing.T) {
    // 10-task plan, chunkSize=4 → should take chunked path (1 plan-findings + 3 chunks = 4 calls).
    plan := buildPlanWithNTasks(10) // helper that returns markdown with 10 `### Task N: …` headings
    crv := &countingReviewer{
        response: providers.Response{
            RawJSON: validPlanFindingsOnlyJSON(t), // helper returns valid Pass-1 JSON
            Model:   "test-model",
        },
    }
    // For per-chunk responses we need a different RawJSON. Easiest: wrap the
    // reviewer to return different payloads based on JSONSchema bytes (or just
    // detect by inspecting req.User content). See helper sketch below.
    // ... wire up countingReviewer + cfg.PlanTasksPerChunk = 4 ...

    // Call ValidatePlan via handlers.ValidatePlan(ctx, args).
    // ... existing call shape ...

    if crv.calls != 4 {
        t.Errorf("reviewer calls = %d, want 4 (1 plan-findings + 3 chunks for 10 tasks at chunkSize=4)", crv.calls)
    }
}

func TestValidatePlan_DispatchesToSingleAtOrBelowThreshold(t *testing.T) {
    plan := buildPlanWithNTasks(4) // 4 tasks
    crv := &countingReviewer{
        response: providers.Response{
            RawJSON: validPlanResultJSON(t), // helper returns valid full PlanResult JSON
            Model:   "test-model",
        },
    }
    // ... wire up countingReviewer + cfg.PlanTasksPerChunk = 4 ...
    // ... call ValidatePlan ...

    if crv.calls != 1 {
        t.Errorf("reviewer calls = %d, want 1 (single-call path at threshold)", crv.calls)
    }
}
```

If you don't have `buildPlanWithNTasks`, write it as a quick helper that emits N `### Task k: …` headings each with `**Goal:**` and `**Acceptance criteria:**` blocks. For `validPlanFindingsOnlyJSON` and `validPlanResultJSON`, return constants matching the parser's expectations. Because the chunked path needs different responses for Pass 1 vs per-chunk, the `countingReviewer` needs to return Pass-1 JSON on the first call and `TasksOnly` JSON afterward — extend it:

```go
type scriptedReviewer struct {
    responses []providers.Response // one per call, in order
    calls     int
}

func (s *scriptedReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
    if s.calls >= len(s.responses) {
        return providers.Response{}, fmt.Errorf("scriptedReviewer: unexpected call #%d", s.calls+1)
    }
    r := s.responses[s.calls]
    s.calls++
    return r, nil
}
```

Then the test scripts the exact sequence: `[plan_findings_only, tasks_only, tasks_only, tasks_only]` for the 10-task case.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/mcpsrv/...
```

Expected: tests fail because the chunked path isn't dispatched yet (only `reviewPlanSingle` is called → exactly 1 call regardless of task count).

- [ ] **Step 3: Update the `ValidatePlan` dispatch**

In `internal/mcpsrv/handlers.go`, find the section that calls `reviewPlanSingle` (added in Task 6). Change it to branch on task count:

```go
model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PlanModel)
if err != nil {
    return nil, verdict.PlanResult{}, err
}

var pr verdict.PlanResult
var modelUsed string
var ms int64
if len(tasks) <= h.deps.Cfg.PlanTasksPerChunk {
    pr, modelUsed, ms, err = h.reviewPlanSingle(ctx, model, args.PlanText)
} else {
    pr, modelUsed, ms, err = h.reviewPlanChunked(ctx, model, args.PlanText, tasks, h.deps.Cfg.PlanTasksPerChunk)
}
if err != nil {
    return nil, verdict.PlanResult{}, err
}
return planEnvelopeResult(pr, modelUsed, ms)
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -race ./...
```

Expected: routing tests pass; all existing tests still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "mcpsrv: dispatch validate_plan to chunked path above threshold

Plans with len(tasks) > PlanTasksPerChunk now route to
reviewPlanChunked; smaller plans continue to use reviewPlanSingle
unchanged. Adds scripted-reviewer test helper and routing tests."
```

---

### Task 9: Tests — chunked-path unit tests (boundary cases + identity validation)

**Goal:** Add exhaustive unit tests for `reviewPlanChunked` covering boundary task counts (8, 9, 16, 17, 25), correct task ordering after merge, mid-stream chunk errors, identity-validation failures (wrong titles, wrong count), and post-merge count-mismatch errors.

**Acceptance criteria:**
- Test for **n=9, N=8**: expects 1 + 2 = 3 reviewer calls (chunks of sizes 8 and 1); resulting `PlanResult.Tasks` has 9 entries in original plan order with `task_title`s matching the input headings.
- Test for **n=16, N=8**: expects 1 + 2 = 3 calls (8 and 8); merged tasks have correct count and order.
- Test for **n=17, N=8**: expects 1 + 3 = 4 calls (8, 8, 1).
- Test for **n=25, N=8**: expects 1 + 4 = 5 calls (8, 8, 8, 1).
- Test that **n=8, N=8** does NOT call `reviewPlanChunked` (single-call path; covered in Task 8's tests, but a direct unit test of the helper for `n=9, N=8` boundary suffices here).
- Test that an error in the middle chunk (e.g. scriptedReviewer returns a network error on the 3rd call) propagates as an error from `reviewPlanChunked` and prevents partial results.
- Test that identity validation kicks in: scripted Pass-2 response with a hallucinated `task_title` triggers the retry-once path; a second hallucination returns an error matching `plan_tasks_chunk failed after retry`.
- Test that a chunk response with wrong count (e.g. only 7 tasks when 8 were requested) triggers retry and ultimately errors.
- Test that post-merge count mismatch (if somehow each chunk returns valid identity-passing tasks but the cumulative total doesn't match) returns the `chunked plan review returned %d task results, expected %d` error.
- Test that `model_used` returned from `reviewPlanChunked` matches the first response's model.
- Test that `review_ms` returned is `>0` and is the sum of per-call latencies (use a reviewer that sleeps deterministically, or just assert `> 0`).

**Non-goals:**
- No live provider tests here (E2E in Task 11).
- No exhaustive negative tests for every chunk position; one mid-stream error case is enough.

**Context:** Use the `scriptedReviewer` helper from Task 8 (and any plan-building helper). Each test builds a plan with N `### Task k:` headings, scripts the reviewer responses for Pass 1 + each chunk, and asserts both the call count and the merged result.

**Files:**
- Modify: `internal/mcpsrv/handlers_test.go` (or create `internal/mcpsrv/handlers_plan_test.go` if `handlers_test.go` grows beyond ~600 lines — judgment call)

- [ ] **Step 1: Write all the chunked-path tests**

Append the following tests. Each builds a plan, scripts a reviewer with the exact response sequence, runs `ValidatePlan`, and asserts the outcome. Sketch (fill in concrete helpers as you go):

```go
func TestReviewPlanChunked_9Tasks_2Chunks(t *testing.T) {
    plan := buildPlanWithNTasks(9)
    rv := scriptReviewer(
        passOneJSON(t),
        chunkJSON(t, []string{"Task 1: a", "Task 2: b", "Task 3: c", "Task 4: d", "Task 5: e", "Task 6: f", "Task 7: g", "Task 8: h"}),
        chunkJSON(t, []string{"Task 9: i"}),
    )
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))
    res := callValidatePlan(t, h, plan)
    if got := rv.calls; got != 3 {
        t.Errorf("calls = %d, want 3", got)
    }
    if got := len(res.PlanResult.Tasks); got != 9 {
        t.Errorf("merged tasks = %d, want 9", got)
    }
    // Order check:
    for i, task := range res.PlanResult.Tasks {
        wantTitle := fmt.Sprintf("Task %d:", i+1)
        if !strings.HasPrefix(task.TaskTitle, wantTitle) {
            t.Errorf("Tasks[%d].TaskTitle = %q, want prefix %q", i, task.TaskTitle, wantTitle)
        }
    }
}

func TestReviewPlanChunked_16Tasks_2Chunks(t *testing.T) {
    plan := buildPlanWithNTasks(16)
    rv := scriptReviewer(
        passOneJSON(t),
        chunkJSON(t, titlesRange(1, 8)),
        chunkJSON(t, titlesRange(9, 16)),
    )
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))
    res := callValidatePlan(t, h, plan)
    if rv.calls != 3 || len(res.PlanResult.Tasks) != 16 {
        t.Errorf("calls=%d tasks=%d, want 3/16", rv.calls, len(res.PlanResult.Tasks))
    }
}

func TestReviewPlanChunked_17Tasks_3Chunks(t *testing.T) {
    plan := buildPlanWithNTasks(17)
    rv := scriptReviewer(
        passOneJSON(t),
        chunkJSON(t, titlesRange(1, 8)),
        chunkJSON(t, titlesRange(9, 16)),
        chunkJSON(t, titlesRange(17, 17)),
    )
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))
    res := callValidatePlan(t, h, plan)
    if rv.calls != 4 || len(res.PlanResult.Tasks) != 17 {
        t.Errorf("calls=%d tasks=%d, want 4/17", rv.calls, len(res.PlanResult.Tasks))
    }
}

func TestReviewPlanChunked_25Tasks_4Chunks(t *testing.T) {
    plan := buildPlanWithNTasks(25)
    rv := scriptReviewer(
        passOneJSON(t),
        chunkJSON(t, titlesRange(1, 8)),
        chunkJSON(t, titlesRange(9, 16)),
        chunkJSON(t, titlesRange(17, 24)),
        chunkJSON(t, titlesRange(25, 25)),
    )
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))
    res := callValidatePlan(t, h, plan)
    if rv.calls != 5 || len(res.PlanResult.Tasks) != 25 {
        t.Errorf("calls=%d tasks=%d, want 5/25", rv.calls, len(res.PlanResult.Tasks))
    }
}

func TestReviewPlanChunked_MidStreamError(t *testing.T) {
    plan := buildPlanWithNTasks(17)
    rv := scriptReviewerWithError(
        []providers.Response{
            {RawJSON: passOneJSON(t), Model: "m"},
            {RawJSON: chunkJSON(t, titlesRange(1, 8)), Model: "m"},
        },
        fmt.Errorf("simulated network error"), // returned on the 3rd call
    )
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))
    if _, _, err := callValidatePlanRaw(t, h, plan); err == nil {
        t.Fatal("expected error from mid-stream failure, got nil")
    }
}

func TestReviewPlanChunked_IdentityMismatch_RetriesThenFails(t *testing.T) {
    plan := buildPlanWithNTasks(9)
    rv := scriptReviewer(
        passOneJSON(t),
        chunkJSON(t, []string{"Task 1: a", "Task 2: b", "Task 3: c", "Task 4: d", "Task 5: e", "Task 6: f", "Task 7: g", "Task 8: h"}),
        chunkJSON(t, []string{"Task 42: hallucinated"}), // first attempt at chunk 2 — wrong title
        chunkJSON(t, []string{"Task 99: still hallucinated"}), // retry — also wrong
    )
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))
    _, _, err := callValidatePlanRaw(t, h, plan)
    if err == nil || !strings.Contains(err.Error(), "plan_tasks_chunk failed after retry") {
        t.Errorf("expected identity failure error, got %v", err)
    }
}

func TestReviewPlanChunked_IdentityMismatch_RetrySucceeds(t *testing.T) {
    plan := buildPlanWithNTasks(9)
    rv := scriptReviewer(
        passOneJSON(t),
        chunkJSON(t, []string{"Task 1: a", "Task 2: b", "Task 3: c", "Task 4: d", "Task 5: e", "Task 6: f", "Task 7: g", "Task 8: h"}),
        chunkJSON(t, []string{"Task 42: hallucinated"}), // first attempt — wrong
        chunkJSON(t, []string{"Task 9: i"}),             // retry — correct
    )
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))
    res := callValidatePlan(t, h, plan)
    if rv.calls != 4 {
        t.Errorf("calls = %d, want 4 (Pass1 + chunk1 + chunk2-fail + chunk2-retry)", rv.calls)
    }
    if len(res.PlanResult.Tasks) != 9 {
        t.Errorf("merged tasks = %d, want 9", len(res.PlanResult.Tasks))
    }
}

func TestReviewPlanChunked_WrongCount_TriggersRetry(t *testing.T) {
    plan := buildPlanWithNTasks(9)
    rv := scriptReviewer(
        passOneJSON(t),
        chunkJSON(t, titlesRange(1, 7)), // 7 tasks when 8 requested
        chunkJSON(t, titlesRange(1, 8)), // retry returns all 8
        chunkJSON(t, titlesRange(9, 9)),
    )
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))
    res := callValidatePlan(t, h, plan)
    if rv.calls != 4 {
        t.Errorf("calls = %d, want 4 (Pass1 + chunk1-fail + chunk1-retry + chunk2)", rv.calls)
    }
    if len(res.PlanResult.Tasks) != 9 {
        t.Errorf("merged tasks = %d, want 9", len(res.PlanResult.Tasks))
    }
}
```

Helpers (`buildPlanWithNTasks`, `titlesRange`, `passOneJSON`, `chunkJSON`, `scriptReviewer`, `scriptReviewerWithError`, `newTestHandlersWith`, `callValidatePlan`, `callValidatePlanRaw`, `withPlanTasksPerChunk`) should be defined once in a `handlers_helpers_test.go` file (or inline at the top of `handlers_test.go`). They are mechanical builders. Example:

```go
func buildPlanWithNTasks(n int) string {
    var b strings.Builder
    b.WriteString("# Plan\n\n")
    for i := 1; i <= n; i++ {
        fmt.Fprintf(&b, "### Task %d: t%d\n\n**Goal:** g%d\n\n**Acceptance criteria:**\n- ac%d\n\n", i, i, i, i)
    }
    return b.String()
}

func titlesRange(lo, hi int) []string {
    out := make([]string, 0, hi-lo+1)
    for i := lo; i <= hi; i++ {
        out = append(out, fmt.Sprintf("Task %d: t%d", i, i))
    }
    return out
}

func passOneJSON(t *testing.T) []byte {
    t.Helper()
    return []byte(`{"plan_verdict":"pass","plan_findings":[],"next_action":"Proceed with implementation."}`)
}

func chunkJSON(t *testing.T, titles []string) []byte {
    t.Helper()
    type item struct {
        TaskIndex             int       `json:"task_index"`
        TaskTitle             string    `json:"task_title"`
        Verdict               string    `json:"verdict"`
        Findings              []any     `json:"findings"`
        SuggestedHeaderBlock  string    `json:"suggested_header_block"`
        SuggestedHeaderReason string    `json:"suggested_header_reason"`
    }
    items := make([]item, 0, len(titles))
    for i, ttl := range titles {
        items = append(items, item{
            TaskIndex: i + 1, TaskTitle: ttl, Verdict: "pass",
            Findings: []any{},
            SuggestedHeaderBlock: "", SuggestedHeaderReason: "",
        })
    }
    raw, err := json.Marshal(struct {
        Tasks []item `json:"tasks"`
    }{items})
    if err != nil {
        t.Fatalf("chunkJSON marshal: %v", err)
    }
    return raw
}
```

- [ ] **Step 2: Run tests to verify they pass**

```bash
go test -race ./internal/mcpsrv/... -v -run TestReviewPlanChunked
```

Expected: all boundary, error, identity, and count tests pass. Fix any helper issues that surface.

- [ ] **Step 3: Run the full test suite**

```bash
go test -race ./...
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpsrv/handlers_test.go internal/mcpsrv/handlers_helpers_test.go
git commit -m "mcpsrv: unit tests for reviewPlanChunked

Covers boundary task counts (9, 16, 17, 25 at chunkSize=8), task
ordering after merge, mid-stream chunk error propagation, identity
validation retry+failure, identity retry success, and per-chunk
count mismatch retry. scriptedReviewer helper supports deterministic
per-call response scripting."
```

---

### Task 10: Tests — integration test for chunked path

**Goal:** Add a black-box integration test that constructs a real MCP handler stack and verifies `validate_plan` on a 12-task plan returns a correctly-merged envelope, end-to-end, with no live provider call.

**Acceptance criteria:**
- Test name: `TestValidatePlan_ChunkedIntegration` (or follow whatever naming convention the existing `internal/mcpsrv/integration_test.go` uses).
- Sets up a `handlers` with `PlanTasksPerChunk=8` and a scripted reviewer that returns 1 Pass-1 response + 2 chunk responses (sizes 8 and 4).
- Calls `ValidatePlan` via the same path the MCP server uses.
- Asserts the returned envelope has `verdict: pass`, 12 tasks in `tasks[]`, non-empty `next_action`, and the model identifier reflects the reviewer's response.
- Test passes under `go test -race ./internal/mcpsrv/...`.

**Non-goals:**
- Not an e2e/live test (Task 11).
- Not testing every edge case (Task 9 covers those).

**Context:** Look at the existing test in `internal/mcpsrv/integration_test.go` to see how the handler stack is built — likely with stub or in-memory `providers.Reviewer` and `session.Store`. Mirror that pattern.

**Files:**
- Modify: `internal/mcpsrv/integration_test.go`

- [ ] **Step 1: Read the existing integration test to learn the pattern**

```bash
cat internal/mcpsrv/integration_test.go
```

Note the existing setup (how `handlers` is constructed, how reviewers are registered, how `ValidatePlan` or its equivalent is invoked).

- [ ] **Step 2: Write the new integration test**

Append to `internal/mcpsrv/integration_test.go`:

```go
func TestValidatePlan_ChunkedIntegration(t *testing.T) {
    plan := buildPlanWithNTasks(12)
    rv := scriptReviewer(
        passOneJSON(t),
        chunkJSON(t, titlesRange(1, 8)),
        chunkJSON(t, titlesRange(9, 12)),
    )
    // Construct handlers exactly the way other tests in this file do,
    // overriding cfg.PlanTasksPerChunk = 8 and registering rv for the
    // anthropic provider (or whatever the existing tests use).
    h := newTestHandlersWith(t, rv, withPlanTasksPerChunk(8))

    env, _, err := callValidatePlanRaw(t, h, plan)
    if err != nil {
        t.Fatalf("ValidatePlan: %v", err)
    }
    if env.PlanResult.PlanVerdict != verdict.VerdictPass {
        t.Errorf("PlanVerdict = %q, want pass", env.PlanResult.PlanVerdict)
    }
    if got := len(env.PlanResult.Tasks); got != 12 {
        t.Errorf("Tasks = %d, want 12", got)
    }
    if env.PlanResult.NextAction == "" {
        t.Error("NextAction empty")
    }
    if rv.calls != 3 {
        t.Errorf("reviewer calls = %d, want 3", rv.calls)
    }
}
```

(If the helpers from Task 9 live in `handlers_test.go` and aren't visible to `integration_test.go` because they're in the same `_test` package — they are; Go pools `_test.go` files within a package — they're directly usable. If for some reason they're not, lift them to a shared helper file `handlers_helpers_test.go`.)

- [ ] **Step 3: Run the integration test**

```bash
go test -race ./internal/mcpsrv/... -v -run TestValidatePlan_ChunkedIntegration
```

Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpsrv/integration_test.go
git commit -m "mcpsrv: integration test for chunked validate_plan path

12-task plan through the handler stack with a scripted reviewer;
asserts merged envelope shape (verdict, task count, next_action,
model) matches the single-call envelope contract."
```

---

### Task 11: Tests — E2E live test gated on `ANTI_TANGENT_E2E_LARGE`

**Goal:** Add a live, opt-in E2E test that runs `validate_plan` on a 25-task plan against the real default OpenAI reviewer. Gated to prevent CI from burning provider credits on every push.

**Acceptance criteria:**
- New test (or new file) with `//go:build e2e` build tag, matching whatever pattern the existing e2e tests use.
- Test reads `ANTI_TANGENT_E2E_LARGE`; if unset or not `"1"`, calls `t.Skip("set ANTI_TANGENT_E2E_LARGE=1 to enable")`.
- When enabled, constructs a 25-task plan (use `buildPlanWithNTasks(25)` from Task 9 helpers; consider promoting it into a non-`_test.go` testdata package only if it isn't already reachable from the e2e test file's package — otherwise duplicate the 12-line helper).
- Constructs handlers using `config.Load(os.Getenv)` (real config from env vars including `OPENAI_API_KEY`).
- Calls `ValidatePlan` end-to-end and asserts: no error, 25 tasks returned, non-empty `next_action`, total reviewer calls = 5.
- Test runs in <5 minutes (5 sequential calls at ~30s each is acceptable; document this expectation in the test comment).

**Non-goals:**
- No mocking — this is a live test by design.
- Doesn't run in standard CI.

**Context:** Look at any existing `*_e2e_test.go` or `_test.go` file with `//go:build e2e` to see the harness. Common pattern: skip on missing API key, then run.

**Files:**
- Create or modify: `internal/mcpsrv/integration_e2e_test.go` (or whatever the existing e2e file is named)

- [ ] **Step 1: Find the existing e2e test scaffolding**

```bash
grep -rln "go:build e2e" internal/
```

Expected: at least one file. Read it to learn the pattern.

- [ ] **Step 2: Write the new e2e test**

Append (or create the file with the build tag at top):

```go
//go:build e2e

package mcpsrv

import (
    "context"
    "os"
    "testing"
    "time"
    // ... other imports as needed ...
)

// TestValidatePlan_E2E_LargePlanChunked exercises the chunked path against a
// live reviewer. ~5 sequential calls × ~30s/call ≈ 2-3 min wall clock.
// Gated on ANTI_TANGENT_E2E_LARGE=1 to keep cost off the default e2e run.
func TestValidatePlan_E2E_LargePlanChunked(t *testing.T) {
    if os.Getenv("ANTI_TANGENT_E2E_LARGE") != "1" {
        t.Skip("set ANTI_TANGENT_E2E_LARGE=1 to enable (5 live reviewer calls)")
    }
    if os.Getenv("OPENAI_API_KEY") == "" {
        t.Skip("OPENAI_API_KEY required")
    }

    cfg, err := config.Load(os.Getenv)
    if err != nil {
        t.Fatalf("config.Load: %v", err)
    }
    // Force OpenAI reviewer for the plan path so the test is deterministic
    // about which provider is exercised.
    cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"} // adjust to a currently allowlisted model
    cfg.PlanTasksPerChunk = 8

    h := newProdHandlers(t, cfg) // helper that wires real providers + in-memory session store

    plan := buildPlanWithNTasks(25)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()

    env, _, err := callValidatePlanRaw(t, h, plan, ctx)
    if err != nil {
        t.Fatalf("ValidatePlan: %v", err)
    }
    if got := len(env.PlanResult.Tasks); got != 25 {
        t.Errorf("Tasks = %d, want 25", got)
    }
    if env.PlanResult.NextAction == "" {
        t.Error("NextAction empty")
    }
}
```

`newProdHandlers` should mirror whatever production-wiring helper already exists in the e2e file. If none exists, construct the handler stack the way `cmd/anti-tangent-mcp/main.go` does.

The model id `gpt-5` is a placeholder — use whatever the current allowlist holds (see `internal/providers/reviewer.go`). The default reviewer for the plan path is `cfg.PreModel` if `PLAN_MODEL` is unset; setting it explicitly here removes ambiguity.

- [ ] **Step 3: Verify the test builds with the e2e tag**

```bash
go test -tags=e2e -race -count=1 -run TestValidatePlan_E2E_LargePlanChunked ./internal/mcpsrv/... -v
```

Without `ANTI_TANGENT_E2E_LARGE=1`, the test should compile and SKIP.

```
--- SKIP: TestValidatePlan_E2E_LargePlanChunked (0.00s)
    integration_e2e_test.go:NN: set ANTI_TANGENT_E2E_LARGE=1 to enable (5 live reviewer calls)
```

Optionally (cost), enable it once locally to confirm a real run:

```bash
ANTI_TANGENT_E2E_LARGE=1 OPENAI_API_KEY=sk-... go test -tags=e2e -race -count=1 -run TestValidatePlan_E2E_LargePlanChunked ./internal/mcpsrv/... -v
```

Expected: passes within ~3 min, returns 25 tasks.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpsrv/integration_e2e_test.go
git commit -m "mcpsrv: e2e test for chunked validate_plan (gated)

Runs a 25-task plan against a live OpenAI reviewer; ~5 sequential
calls. Gated on ANTI_TANGENT_E2E_LARGE=1 to keep cost off the
default e2e run."
```

---

### Task 12: Docs — README + INTEGRATION env vars and chunking explanation

**Goal:** Document the three new env vars and the chunking behavior in both `README.md` and `INTEGRATION.md`, in the same shape and place as existing env-var documentation.

**Acceptance criteria:**
- `README.md`: existing "Configuration" or env-var section gains three new rows in the same table style, with default and one-line description for each.
- A new subsection (or paragraph) under the `validate_plan` documentation explains the chunking behavior in 3-5 sentences: when it kicks in, that it's transparent to callers, and that operators can tune `PlanTasksPerChunk` and `PlanMaxTokens`.
- `INTEGRATION.md`: same updates, in the same style.
- No changes to existing rows / sections — only additions.

**Non-goals:**
- No long architecture write-up — the spec is the source of truth and is linked.

**Files:**
- Modify: `README.md`
- Modify: `INTEGRATION.md`

- [ ] **Step 1: Read both files to find the right sections**

```bash
grep -n "ANTI_TANGENT_\|validate_plan\|Configuration\|## Configuration" README.md INTEGRATION.md
```

- [ ] **Step 2: Add the three env-var rows to the README config table**

In `README.md`, locate the env-var table (likely near `ANTI_TANGENT_PLAN_MODEL`). Insert three rows in the same table style:

```markdown
| `ANTI_TANGENT_PER_TASK_MAX_TOKENS` | Max output tokens per reviewer call for the per-task hooks (`validate_task_spec`, `check_progress`, `validate_completion`). Default `4096`. Raise if your reviewer model truncates long findings lists. |
| `ANTI_TANGENT_PLAN_MAX_TOKENS`     | Max output tokens per reviewer call for `validate_plan` (both single-call and per-chunk in the chunked path). Default `4096`. Raise if individual chunk responses truncate. |
| `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` | Plans with more than this many tasks are reviewed via the chunked path; the same value sets the per-chunk task count. Default `8`. Lower for plans with very dense per-task content (long header blocks). |
```

(Adjust the exact markdown to match the existing table — pipe alignment, header row presence, etc.)

- [ ] **Step 3: Add a chunking explanation under `validate_plan`**

Locate where `validate_plan` is described in `README.md`. Append a short paragraph:

```markdown
### Large plans (chunking)

`validate_plan` automatically chunks plans with more than `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` tasks (default 8). Internally the server makes one Pass-1 reviewer call for cross-cutting `plan_findings` plus `ceil(n/N)` per-task chunks, each carrying the full plan as context. The merged `PlanResult` is identical in shape to the single-call path — callers see no difference. Operators with very dense per-task content (long `**Goal:**` / `**Acceptance criteria:**` blocks) can lower `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` to reduce per-chunk output size, or raise `ANTI_TANGENT_PLAN_MAX_TOKENS` if their reviewer model supports it. Worst-case wall-clock for a 25-task plan is ~5 sequential calls.
```

- [ ] **Step 4: Mirror the changes in `INTEGRATION.md`**

Apply the same env-var rows and chunking paragraph to `INTEGRATION.md` (whatever the equivalent sections are named in that file — match the existing pattern).

- [ ] **Step 5: Verify the markdown renders correctly**

Visually inspect both files:

```bash
git diff README.md INTEGRATION.md
```

- [ ] **Step 6: Commit**

```bash
git add README.md INTEGRATION.md
git commit -m "docs: document chunked validate_plan + 3 new env vars

README and INTEGRATION gain rows for ANTI_TANGENT_PER_TASK_MAX_TOKENS,
ANTI_TANGENT_PLAN_MAX_TOKENS, and ANTI_TANGENT_PLAN_TASKS_PER_CHUNK,
plus a short subsection explaining the chunking behavior under
validate_plan. Behavior is transparent to callers; PlanResult shape
is unchanged."
```

---

### Task 13: Release — CHANGELOG, VERSION bump, full test run

**Goal:** Add the `[0.1.4]` CHANGELOG entry, bump `VERSION`, run the full test suite plus build, and prepare the branch for merge.

**Acceptance criteria:**
- `CHANGELOG.md` has a new `## [0.1.4] - <today>` section above `## [0.1.3]`, with `### Added` (3 env vars + chunking behavior) and `### Fixed` (the `decode plan result: EOF` symptom on large plans).
- `VERSION` file contains exactly `0.1.4\n`.
- `go test -race ./...` passes cleanly.
- `go build ./...` produces a clean binary.
- All changes committed; branch ready for PR conversion from draft → ready.

**Non-goals:**
- No actual release/tag push — the merge workflow handles that.
- No upgrade of any goreleaser / CI config.

**Context:** Date format follows the existing CHANGELOG entries (`YYYY-MM-DD`). Today is 2026-05-11.

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `VERSION`

- [ ] **Step 1: Add the CHANGELOG entry**

Edit `CHANGELOG.md`. Insert above the existing `## [0.1.3]` section:

```markdown
## [0.1.4] - 2026-05-11

### Added
- `validate_plan` now automatically chunks large plans so reviewer responses don't truncate mid-JSON. Plans with more than `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` tasks (default 8) are reviewed via one Pass-1 plan-findings call plus `ceil(n/N)` per-chunk calls; the merged `PlanResult` is identical in shape to the single-call path. Plans of 8 tasks or fewer take the existing single-call path unchanged.
- Three new optional env vars: `ANTI_TANGENT_PER_TASK_MAX_TOKENS` (default 4096) governs output budget for `validate_task_spec` / `check_progress` / `validate_completion`; `ANTI_TANGENT_PLAN_MAX_TOKENS` (default 4096) governs output budget for `validate_plan` (single-call and per-chunk); `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` (default 8) sets both the chunking threshold and per-chunk task count. All three reject zero / negative / non-integer values at startup.

### Fixed
- `validate_plan` returning `decode plan result: EOF` on plans of ~12+ tasks. Root cause was a hardcoded `MaxTokens: 4096` cap that the reviewer's JSON response was overflowing on dense plans; both the cap is now configurable and the chunking path keeps each individual response well within budget.
```

- [ ] **Step 2: Bump VERSION**

Write `0.1.4\n` to the `VERSION` file:

```bash
echo "0.1.4" > VERSION
```

- [ ] **Step 3: Run the full test suite**

```bash
go test -race ./...
```

Expected: all packages PASS.

- [ ] **Step 4: Build**

```bash
go build ./...
```

Expected: clean build.

- [ ] **Step 5: Verify the version flag works**

```bash
go run ./cmd/anti-tangent-mcp --version
```

Expected: prints `0.1.4` (or whatever format the existing --version handler uses).

- [ ] **Step 6: Commit**

```bash
git add CHANGELOG.md VERSION
git commit -m "release: prep v0.1.4 — chunked validate_plan

CHANGELOG entry covers the chunking feature, the three new env vars,
and the EOF-on-large-plans fix. VERSION → 0.1.4 (minor bump; branch
name matches per project convention)."
```

- [ ] **Step 7: Push the branch and convert PR from draft to ready**

```bash
git push origin version/0.1.4
gh pr ready 5
```

Expected: PR #5 transitions from Draft to Open and CI runs the changelog-enforcement check (now satisfied) plus tests.

- [ ] **Step 8: Verify CI is green**

```bash
gh pr checks 5
```

Expected: all checks PASS. If any fail, investigate and fix in additional commits.

---

## Implementation order summary

1. **Foundation:** Task 1 (config)
2. **Schemas + types:** Tasks 2 + 3 (verdict)
3. **Prompts:** Tasks 4 + 5 (templates + render functions)
4. **Handlers:** Task 6 (rename + wire config) → Task 7 (chunked helper) → Task 8 (dispatch)
5. **Tests:** Tasks 9 (unit) + 10 (integration) + 11 (e2e gated)
6. **Docs + release:** Tasks 12 + 13

Each task lands its own commit. The branch already exists (`version/0.1.4`) and PR #5 is open as draft; Task 13 flips it ready.
