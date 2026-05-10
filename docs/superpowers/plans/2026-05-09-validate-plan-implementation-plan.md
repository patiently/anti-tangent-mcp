# validate_plan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Anti-tangent-mcp protocol applies.** Each task below carries an explicit `Goal` / `Acceptance criteria` / `Non-goals` / `Context` header — the structured shape `validate_task_spec` expects. Implementing subagents must call `validate_task_spec` at task start, optionally `check_progress` mid-implementation, and `validate_completion` before reporting DONE.

**Goal:** Add a 4th MCP tool (`validate_plan`) that reviews an entire implementation plan in one call and proposes ready-to-paste structured-header blocks for tasks lacking them. Folds into v0.1.0.

**Architecture:** New `planparser` package splits the plan markdown into preamble + RawTask list. New `verdict.PlanResult` type with its own JSON schema (mirroring the existing `verdict.Result`). New `prompts.RenderPlan` template. New `mcpsrv.ValidatePlan` handler that orchestrates split → render → reviewer call → parse → return. The existing 3 tools' behavior is unchanged; the existing per-task validate_task_spec stays as the implementer's session-creating gate.

**Tech Stack:** Go 1.25, `github.com/modelcontextprotocol/go-sdk`, `github.com/stretchr/testify`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-05-09-validate-plan-design.md`

---

## File map

```text
anti-tangent-mcp/
├── internal/
│   ├── verdict/
│   │   ├── plan.go                # NEW: PlanResult, PlanTaskResult types + PlanSchema()
│   │   ├── plan_schema.json       # NEW: embedded JSON schema
│   │   ├── plan_parser.go         # NEW: ParsePlan with strict EOF check
│   │   └── plan_test.go           # NEW: types + parser tests
│   ├── planparser/                # NEW package
│   │   ├── planparser.go          # NEW: SplitTasks(plan_text) → ([]RawTask, preamble)
│   │   └── planparser_test.go     # NEW
│   ├── prompts/
│   │   ├── prompts.go             # MODIFY: add PlanInput type + RenderPlan func
│   │   ├── templates/plan.tmpl    # NEW: plan-level review prompt
│   │   ├── testdata/plan_basic.golden  # NEW: golden file
│   │   └── prompts_test.go        # MODIFY: add TestRenderPlan
│   ├── config/
│   │   ├── config.go              # MODIFY: add PlanModel field, default-from-PRE
│   │   └── config_test.go         # MODIFY: add tests for PlanModel default + override
│   └── mcpsrv/
│       ├── server.go              # MODIFY: register validate_plan tool
│       ├── handlers.go            # MODIFY: add ValidatePlanArgs, ValidatePlan handler, reviewPlan helper
│       ├── handlers_test.go       # MODIFY: add 4 ValidatePlan tests
│       └── integration_test.go    # MODIFY: extend lifecycle test with validate_plan call
├── INTEGRATION.md                 # MODIFY: rewrite §5.1, add §5.5, footnote in §1
├── README.md                      # MODIFY: tool list grows from 3 to 4
└── CHANGELOG.md                   # MODIFY: extend ## [0.1.0] ### Added with 2 new bullets
```

`~/.claude/anti-tangent.md` is **outside** this repo and is not modified by this plan; updating it is a post-merge step the user does locally to keep their personal protocol clause in sync.

---

## Task 1: PlanResult types and JSON schema

**Goal:** Define the canonical Go shape for plan-level reviewer output (PlanResult, PlanTaskResult) and ship the matching JSON schema embedded in the binary.

**Acceptance criteria:**
- `internal/verdict/plan.go` declares `PlanResult` and `PlanTaskResult` Go types with the exact field names and json tags from the spec.
- `internal/verdict/plan_schema.json` is a valid JSON Schema (draft-07) with the required keys, additionalProperties:false at root and at task level, and category enum equal to the existing `verdict.Category` constants.
- `PlanSchema()` exists, embeds `plan_schema.json` via `//go:embed`, and returns a defensive byte copy (mirroring `Schema()` in verdict.go).
- Tests in `plan_test.go` pass: a schema-validity test (Schema() unmarshals; has root keys plan_verdict/plan_findings/tasks/next_action) and a Result-round-trip test (a Go-defined PlanResult marshals + unmarshals back equal).
- `go test -race ./internal/verdict/...` is green.

**Non-goals:**
- Parser logic (Task 2 covers it).
- Wire integration (later tasks).

**Context:** Mirrors the existing `internal/verdict/verdict.go` and `schema.json`. The `PlanSchema()` defensive-copy pattern was applied to `Schema()` in commit 9e14705 — copy that idiom.

**Files:**
- Create: `internal/verdict/plan.go`
- Create: `internal/verdict/plan_schema.json`
- Create: `internal/verdict/plan_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/verdict/plan_test.go`:
```go
package verdict

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanSchema_IsValidJSON(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(PlanSchema(), &schema))
	assert.Equal(t, "object", schema["type"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "plan_verdict")
	assert.Contains(t, props, "plan_findings")
	assert.Contains(t, props, "tasks")
	assert.Contains(t, props, "next_action")
}

func TestPlanResult_RoundTripsJSON(t *testing.T) {
	r := PlanResult{
		PlanVerdict: VerdictWarn,
		PlanFindings: []Finding{},
		Tasks: []PlanTaskResult{{
			TaskIndex:             0,
			TaskTitle:             "Task 1: Bootstrap",
			Verdict:               VerdictWarn,
			Findings:              []Finding{{Severity: SeverityMajor, Category: CategoryAmbiguousSpec, Criterion: "header", Evidence: "no Goal", Suggestion: "add Goal"}},
			SuggestedHeaderBlock:  "**Goal:** Initialize repo.\n",
			SuggestedHeaderReason: "task lacks Goal/AC structure",
		}},
		NextAction: "Adopt suggested header for Task 1.",
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	var back PlanResult
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, r, back)
}

func TestPlanSchema_DefensiveCopy(t *testing.T) {
	a := PlanSchema()
	b := PlanSchema()
	require.Equal(t, a, b)
	// Mutate a; b must remain unchanged (proving Schema() returns a copy).
	a[0] = 'X'
	assert.NotEqual(t, a[0], b[0])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/verdict/... -run TestPlanResult -v 2>&1 | tail -20`
Expected: FAIL — `PlanResult`, `PlanTaskResult`, `PlanSchema` undefined.

- [ ] **Step 3: Create the JSON schema**

Create `internal/verdict/plan_schema.json`:
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

- [ ] **Step 4: Implement plan.go**

Create `internal/verdict/plan.go`:
```go
package verdict

import _ "embed"

//go:embed plan_schema.json
var planSchema []byte

// PlanSchema returns a defensive byte copy of the plan-level JSON schema.
// Providers are instructed to produce output matching this shape.
func PlanSchema() []byte {
	out := make([]byte, len(planSchema))
	copy(out, planSchema)
	return out
}

// PlanResult is the canonical shape returned by validate_plan.
type PlanResult struct {
	PlanVerdict  Verdict          `json:"plan_verdict"`
	PlanFindings []Finding        `json:"plan_findings"`
	Tasks        []PlanTaskResult `json:"tasks"`
	NextAction   string           `json:"next_action"`
}

// PlanTaskResult is the per-task analysis carried inside PlanResult.Tasks.
type PlanTaskResult struct {
	TaskIndex             int       `json:"task_index"`
	TaskTitle             string    `json:"task_title"`
	Verdict               Verdict   `json:"verdict"`
	Findings              []Finding `json:"findings"`
	SuggestedHeaderBlock  string    `json:"suggested_header_block"`
	SuggestedHeaderReason string    `json:"suggested_header_reason"`
}
```

- [ ] **Step 5: Run tests, confirm pass**

Run: `go test -race ./internal/verdict/... -v 2>&1 | tail -20`
Expected: All tests in plan_test.go PASS, plus all existing verdict tests still PASS.

Run: `gofmt -l ./internal && go vet ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/verdict/plan.go internal/verdict/plan_schema.json internal/verdict/plan_test.go
git commit -m "feat(verdict): PlanResult type + JSON schema for validate_plan"
```

---

## Task 2: PlanResult parser

**Goal:** Parse provider raw JSON into a `PlanResult`, with strict enum validation, fence stripping, and a strict EOF check (rejecting any extra JSON after the document).

**Acceptance criteria:**
- `internal/verdict/plan_parser.go` declares `func ParsePlan(raw []byte) (PlanResult, error)`.
- ParsePlan: trims whitespace, strips a leading ```` ```json ```` (or bare ```` ``` ````) wrapper if present (mirroring `stripFences` in parser.go), uses `json.Decoder` with `DisallowUnknownFields()`, and after the first decode performs a `dec.Decode(&struct{}{}) != io.EOF` check rejecting concatenated documents.
- ParsePlan validates: `plan_verdict` is one of pass/warn/fail; each `task.verdict` likewise; each finding (in plan_findings AND each task.findings) has severity ∈ {critical,major,minor} and category in `validCategory` enum.
- All error messages start with `"decode plan result"` or `"plan: invalid …"`.
- New tests in `plan_test.go` (append): valid JSON happy path; malformed JSON; invalid plan_verdict; invalid task verdict; invalid finding severity; invalid finding category; rejects extra JSON after document; strips ```json fences.
- `go test -race ./internal/verdict/...` is green.

**Non-goals:**
- Retry-on-malformed-JSON behavior (lives in handler layer; mirrors existing `review()` retry logic).
- Network or provider concerns.

**Context:** Mirror `internal/verdict/parser.go` exactly — same fence stripping, same DisallowUnknownFields, same one-extra-Decode strict EOF check (added in commit 93d8c72 after CR's round-2 review). Reuse `stripFences` from parser.go (it's package-private but same package).

**Files:**
- Create: `internal/verdict/plan_parser.go`
- Modify: `internal/verdict/plan_test.go` (append parser tests)

- [ ] **Step 1: Append failing parser tests**

Append to `internal/verdict/plan_test.go`:
```go
func TestParsePlan_Valid(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"pass",
		"plan_findings":[],
		"tasks":[
			{"task_index":0,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}
		],
		"next_action":"go"
	}`)
	r, err := ParsePlan(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictPass, r.PlanVerdict)
	require.Len(t, r.Tasks, 1)
	assert.Equal(t, "T1", r.Tasks[0].TaskTitle)
}

func TestParsePlan_Malformed(t *testing.T) {
	_, err := ParsePlan([]byte(`{not json`))
	require.Error(t, err)
}

func TestParsePlan_InvalidPlanVerdict(t *testing.T) {
	in := []byte(`{"plan_verdict":"maybe","plan_findings":[],"tasks":[],"next_action":"x"}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan_verdict")
}

func TestParsePlan_InvalidTaskVerdict(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"pass",
		"plan_findings":[],
		"tasks":[{"task_index":0,"task_title":"T","verdict":"meh","findings":[],"suggested_header_block":"","suggested_header_reason":""}],
		"next_action":"x"
	}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task[0]")
}

func TestParsePlan_InvalidFindingSeverity(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"warn",
		"plan_findings":[{"severity":"oops","category":"other","criterion":"c","evidence":"e","suggestion":"s"}],
		"tasks":[],
		"next_action":"x"
	}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "severity")
}

func TestParsePlan_InvalidFindingCategory(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"warn",
		"plan_findings":[{"severity":"major","category":"made_up","criterion":"c","evidence":"e","suggestion":"s"}],
		"tasks":[],
		"next_action":"x"
	}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "category")
}

func TestParsePlan_RejectsExtraJSON(t *testing.T) {
	in := []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[],"next_action":"a"}{"plan_verdict":"fail","plan_findings":[],"tasks":[],"next_action":"b"}`)
	_, err := ParsePlan(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extra JSON")
}

func TestParsePlan_StripsCodeFences(t *testing.T) {
	in := []byte("```json\n{\"plan_verdict\":\"pass\",\"plan_findings\":[],\"tasks\":[],\"next_action\":\"ok\"}\n```")
	r, err := ParsePlan(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictPass, r.PlanVerdict)
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/verdict/... -run TestParsePlan -v 2>&1 | tail -10`
Expected: FAIL — `ParsePlan` undefined.

- [ ] **Step 3: Implement plan_parser.go**

Create `internal/verdict/plan_parser.go`:
```go
package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParsePlan decodes provider output into a PlanResult and validates enum fields.
// It tolerates a ```json ... ``` wrapper and surrounding whitespace, and rejects
// any extra JSON after the single document.
func ParsePlan(raw []byte) (PlanResult, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r PlanResult
	if err := dec.Decode(&r); err != nil {
		return PlanResult{}, fmt.Errorf("decode plan result: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return PlanResult{}, fmt.Errorf("decode plan result: extra JSON after document")
	}
	if err := validatePlanVerdict(r.PlanVerdict, "plan_verdict"); err != nil {
		return PlanResult{}, err
	}
	for i, f := range r.PlanFindings {
		if err := validateFinding(f, fmt.Sprintf("plan_findings[%d]", i)); err != nil {
			return PlanResult{}, err
		}
	}
	for i, t := range r.Tasks {
		prefix := fmt.Sprintf("task[%d]", i)
		if err := validatePlanVerdict(t.Verdict, prefix+".verdict"); err != nil {
			return PlanResult{}, err
		}
		for j, f := range t.Findings {
			if err := validateFinding(f, fmt.Sprintf("%s.findings[%d]", prefix, j)); err != nil {
				return PlanResult{}, err
			}
		}
	}
	return r, nil
}

func validatePlanVerdict(v Verdict, where string) error {
	switch v {
	case VerdictPass, VerdictWarn, VerdictFail:
		return nil
	}
	return fmt.Errorf("plan: invalid %s %q", where, v)
}

func validateFinding(f Finding, where string) error {
	switch f.Severity {
	case SeverityCritical, SeverityMajor, SeverityMinor:
	default:
		return fmt.Errorf("plan: %s.severity invalid %q", where, f.Severity)
	}
	if !validCategory(f.Category) {
		return fmt.Errorf("plan: %s.category invalid %q", where, f.Category)
	}
	return nil
}
```

- [ ] **Step 4: Run tests, confirm pass**

Run: `go test -race ./internal/verdict/... -v 2>&1 | tail -30`
Expected: All TestParsePlan_* tests PASS.

Run: `gofmt -l ./internal && go vet ./...`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add internal/verdict/plan_parser.go internal/verdict/plan_test.go
git commit -m "feat(verdict): ParsePlan with strict EOF + enum validation"
```

---

## Task 3: planparser package

**Goal:** Implement `internal/planparser/planparser.go` that splits a plan markdown string into a preamble + ordered list of `RawTask{Title, Body, HasStructuredHeader}`.

**Acceptance criteria:**
- New package `internal/planparser` with `RawTask` struct (Title, Body, HasStructuredHeader fields) and `func SplitTasks(planText string) (tasks []RawTask, preamble string)`.
- The split regex matches `^### Task \d+:.*$` at line boundaries; only numbered task headings are matched.
- `HasStructuredHeader` is true iff the body contains BOTH `**Goal:**` and `**Acceptance criteria:**` substrings (case-sensitive, exact).
- Preamble is everything before the first heading (empty string if the plan starts with a task heading).
- Tests cover: plan with N structured tasks → flags all true; plan with N TDD-step tasks → flags all false; mixed shapes; empty input → empty tasks, empty preamble; plan with no headings → empty tasks, preamble == input; heading-without-number (`### Task: Foo`) → not matched; fenced code block containing the word "Task" → not falsely matched.
- `go test -race ./internal/planparser/...` is green.
- `go vet ./...` and `gofmt -l ./internal` clean.

**Non-goals:**
- Parsing the body itself (the reviewer LLM does that).
- Validating that each task body has Files / Steps sections (out of scope).

**Context:** Pure stdlib; no third-party deps. The split logic uses `regexp` to find heading line indices, then carves the input into chunks. Heading lines themselves are part of the task's Body so the reviewer sees the full `### Task N: Title` text.

**Files:**
- Create: `internal/planparser/planparser.go`
- Create: `internal/planparser/planparser_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/planparser/planparser_test.go`:
```go
package planparser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitTasks_AllStructured(t *testing.T) {
	in := `# Plan

Intro text.

### Task 1: First

**Goal:** Do thing one.

**Acceptance criteria:**
- AC

### Task 2: Second

**Goal:** Do thing two.

**Acceptance criteria:**
- AC
`
	tasks, preamble := SplitTasks(in)
	assert.Contains(t, preamble, "Intro text.")
	require.Equal(t, 2, len(tasks))
	assert.Equal(t, "Task 1: First", tasks[0].Title)
	assert.Equal(t, "Task 2: Second", tasks[1].Title)
	assert.True(t, tasks[0].HasStructuredHeader)
	assert.True(t, tasks[1].HasStructuredHeader)
}

func TestSplitTasks_AllTDDShape(t *testing.T) {
	in := `### Task 1: Bootstrap

Files:
- main.go

Step 1: write main.

### Task 2: Add tests

Files:
- main_test.go

Step 1: write tests.
`
	tasks, preamble := SplitTasks(in)
	assert.Empty(t, preamble)
	require.Equal(t, 2, len(tasks))
	assert.False(t, tasks[0].HasStructuredHeader)
	assert.False(t, tasks[1].HasStructuredHeader)
}

func TestSplitTasks_Mixed(t *testing.T) {
	in := `### Task 1: Structured

**Goal:** g
**Acceptance criteria:**
- AC

### Task 2: TDD

Files:
- f.go
`
	tasks, _ := SplitTasks(in)
	require.Equal(t, 2, len(tasks))
	assert.True(t, tasks[0].HasStructuredHeader)
	assert.False(t, tasks[1].HasStructuredHeader)
}

func TestSplitTasks_NoHeadings(t *testing.T) {
	in := "Just some text without any task headings."
	tasks, preamble := SplitTasks(in)
	assert.Empty(t, tasks)
	assert.Equal(t, in, preamble)
}

func TestSplitTasks_Empty(t *testing.T) {
	tasks, preamble := SplitTasks("")
	assert.Empty(t, tasks)
	assert.Empty(t, preamble)
}

func TestSplitTasks_HeadingWithoutNumberIgnored(t *testing.T) {
	in := `### Task: Not Numbered

Body.

### Task 1: Numbered

Body.
`
	tasks, preamble := SplitTasks(in)
	require.Equal(t, 1, len(tasks))
	assert.Equal(t, "Task 1: Numbered", tasks[0].Title)
	assert.Contains(t, preamble, "Task: Not Numbered")
}

func TestSplitTasks_FencedTaskWordNotMatched(t *testing.T) {
	in := "" +
		"### Task 1: Real\n\n" +
		"```\n" +
		"### Task 99: Inside fence\n" +
		"```\n\n" +
		"Body continues.\n"
	tasks, _ := SplitTasks(in)
	require.Equal(t, 1, len(tasks))
	assert.Equal(t, "Task 1: Real", tasks[0].Title)
	assert.Contains(t, tasks[0].Body, "### Task 99: Inside fence")
	assert.Contains(t, tasks[0].Body, "Body continues.")
}

// require helper to avoid importing testify/require at top
var require = struct {
	Equal func(t *testing.T, expected, actual any)
}{Equal: func(t *testing.T, expected, actual any) {
	t.Helper()
	if !assert.Equal(t, expected, actual) {
		t.FailNow()
	}
}}

// Suppress unused-import warnings for `strings`.
var _ = strings.Contains
```

NOTE: that ad-hoc `require` shim avoids a second test-file import. If you prefer the standard `github.com/stretchr/testify/require` package, replace the var declaration at the bottom with `import "github.com/stretchr/testify/require"` and use `require.Equal` / `require.Equal` directly. Stick with whichever the surrounding code style prefers.

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/planparser/... -v 2>&1 | tail -10`
Expected: FAIL — `SplitTasks`, `RawTask` undefined.

- [ ] **Step 3: Implement planparser.go**

Create `internal/planparser/planparser.go`:
```go
// Package planparser splits an implementation-plan markdown string into a
// preamble and an ordered list of tasks. It is intentionally minimal: each
// task's body is returned verbatim including its heading line so the reviewer
// LLM sees the same text the human wrote.
package planparser

import (
	"regexp"
	"strings"
)

// RawTask is one task carved out of a plan markdown document.
type RawTask struct {
	// Title is the heading text after "### " — for example, "Task 4: Add /healthz endpoint".
	Title string
	// Body is the full task content including the "### Task N: …" heading line.
	Body string
	// HasStructuredHeader is true iff the body contains both **Goal:** and
	// **Acceptance criteria:** markers. Used for telemetry only; not sent to
	// the reviewer.
	HasStructuredHeader bool
}

// taskHeadingRe matches lines like "### Task 4: Add /healthz endpoint".
var taskHeadingRe = regexp.MustCompile(`(?m)^### Task \d+:.*$`)

// SplitTasks carves planText into a preamble and an ordered list of RawTask.
// The preamble is everything before the first task heading (empty if the plan
// starts with a heading). If no headings match, tasks is nil and preamble is
// the full input.
func SplitTasks(planText string) ([]RawTask, string) {
	if planText == "" {
		return nil, ""
	}
	matches := taskHeadingRe.FindAllStringIndex(planText, -1)
	if len(matches) == 0 {
		return nil, planText
	}

	preamble := planText[:matches[0][0]]
	tasks := make([]RawTask, 0, len(matches))
	for i, m := range matches {
		end := len(planText)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		body := planText[m[0]:end]
		// Title: heading line minus the "### " prefix, trimmed.
		headingLine := planText[m[0]:m[1]]
		title := strings.TrimSpace(strings.TrimPrefix(headingLine, "### "))
		tasks = append(tasks, RawTask{
			Title:               title,
			Body:                body,
			HasStructuredHeader: hasStructuredHeader(body),
		})
	}
	return tasks, preamble
}

func hasStructuredHeader(body string) bool {
	return strings.Contains(body, "**Goal:**") && strings.Contains(body, "**Acceptance criteria:**")
}
```

- [ ] **Step 4: Run tests, confirm pass**

Run: `go test -race ./internal/planparser/... -v 2>&1 | tail -20`
Expected: All seven tests PASS.

Run: `go vet ./internal/planparser/... && gofmt -l ./internal`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add internal/planparser/
git commit -m "feat(planparser): split plan markdown into preamble + RawTask list"
```

---

## Task 4: Plan prompt template + RenderPlan

**Goal:** Add a `plan.tmpl` template embedded under `internal/prompts/templates/` and a `RenderPlan(in PlanInput) (Output, error)` function in the prompts package, golden-tested.

**Acceptance criteria:**
- New file `internal/prompts/templates/plan.tmpl` with the prompt body from the design spec (lines starting with "## Plan under review" through "Respond with a JSON object…").
- `internal/prompts/prompts.go` gains: `type PlanInput struct { PlanText string }` and `func RenderPlan(in PlanInput) (Output, error)`.
- `RenderPlan` uses the same shared `systemPrompt` constant; user content is the rendered template.
- The template is embedded via the existing `//go:embed templates/*.tmpl` directive (no new embed line needed).
- `internal/prompts/prompts_test.go` gains `TestRenderPlan` which uses a sample 2-task plan and compares against `internal/prompts/testdata/plan_basic.golden`.
- `go test -race ./internal/prompts/...` is green; the golden file is written with `-update` and stable.

**Non-goals:**
- Tuning prompt wording beyond the spec text.
- Including extra fields in PlanInput (e.g. preamble) — keep PlanInput minimal; the full plan_text is what the reviewer sees.

**Context:** Mirror the structure of existing `RenderPre` / `RenderMid` / `RenderPost`. The shared `systemPrompt` constant lives in `prompts.go`. The `render(name string, data any) (string, error)` helper is already there — reuse it.

**Files:**
- Create: `internal/prompts/templates/plan.tmpl`
- Create: `internal/prompts/testdata/plan_basic.golden` (generated via `-update`)
- Modify: `internal/prompts/prompts.go` (add PlanInput type + RenderPlan func)
- Modify: `internal/prompts/prompts_test.go` (add TestRenderPlan)

- [ ] **Step 1: Append failing test**

Append to `internal/prompts/prompts_test.go` (after the existing TestRenderPost):
```go
func TestRenderPlan(t *testing.T) {
	plan := `# Sample Plan

### Task 1: Bootstrap

Files:
- main.go

Step 1: write main.

### Task 2: Add tests

**Goal:** Cover the bootstrap with a smoke test.

**Acceptance criteria:**
- main_test.go exists
- go test ./... passes
`
	out, err := RenderPlan(PlanInput{PlanText: plan})
	require.NoError(t, err)
	golden(t, "plan_basic", out.System+"\n---USER---\n"+out.User)
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/prompts/... -run TestRenderPlan -v 2>&1 | tail -10`
Expected: FAIL — `RenderPlan` and `PlanInput` undefined.

- [ ] **Step 3: Create the template**

Create `internal/prompts/templates/plan.tmpl`:
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

- [ ] **Step 4: Add PlanInput + RenderPlan to prompts.go**

Modify `internal/prompts/prompts.go`. Locate the existing `PostInput` struct and add immediately after it:

```go
type PlanInput struct {
	PlanText string
}
```

Locate the existing `RenderPost` function and add immediately after it:

```go
func RenderPlan(in PlanInput) (Output, error) {
	body, err := render("plan.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}
```

- [ ] **Step 5: Generate golden file**

Run: `go test ./internal/prompts/... -update -run TestRenderPlan -v 2>&1 | tail -10`
Expected: PASS — golden file written to `internal/prompts/testdata/plan_basic.golden`.

Inspect: `head -40 internal/prompts/testdata/plan_basic.golden` — should start with the system prompt, then `---USER---`, then "## Plan under review" and the rendered task text.

- [ ] **Step 6: Run without -update, confirm stable**

Run: `go test -race ./internal/prompts/... -v 2>&1 | tail -20`
Expected: all four prompt tests (TestRenderPre, TestRenderMid, TestRenderPost, TestRenderPlan) PASS.

Run: `gofmt -l ./internal && go vet ./...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/
git commit -m "feat(prompts): plan template + RenderPlan with golden test"
```

---

## Task 5: ANTI_TANGENT_PLAN_MODEL config

**Goal:** Add a new optional env var `ANTI_TANGENT_PLAN_MODEL` to the Config struct, defaulting to whatever `ANTI_TANGENT_PRE_MODEL` resolved to.

**Acceptance criteria:**
- `internal/config/config.go`'s `Config` struct gains a `PlanModel ModelRef` field.
- After PreModel/MidModel/PostModel are resolved, PlanModel is set: if `ANTI_TANGENT_PLAN_MODEL` is set in env it's parsed via `ParseModelRef`; otherwise it equals the already-resolved `cfg.PreModel`.
- Tests in `config_test.go` cover: (a) PlanModel defaults to PreModel when no env var (with default PreModel); (b) PlanModel inherits PreModel even when PreModel was overridden via `ANTI_TANGENT_PRE_MODEL`; (c) explicit `ANTI_TANGENT_PLAN_MODEL` override wins.
- `go test -race ./internal/config/...` green.
- gofmt + vet clean.

**Non-goals:**
- Adding the model to the providers allowlist (no new models being introduced).
- Validating that the model is in the allowlist (validation happens at the provider layer, same as the other model refs).
- Adding a separate validation test that bad model strings fail (existing `TestLoad_BadModelRef` covers the parsing logic; PlanModel uses the same parser).

**Context:** Same parsing path as PreModel/MidModel/PostModel. The default-from-PRE behavior means a user with one provider key set continues to work without changes.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Append failing tests**

Append to `internal/config/config_test.go`:
```go
func TestLoad_PlanModel_DefaultsToPre(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY": "k",
	}))
	require.NoError(t, err)
	assert.Equal(t, cfg.PreModel, cfg.PlanModel)
}

func TestLoad_PlanModel_InheritsPreOverride(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":      "k",
		"ANTI_TANGENT_PRE_MODEL": "openai:gpt-5",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, cfg.PreModel, cfg.PlanModel)
}

func TestLoad_PlanModel_ExplicitOverride(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":       "k",
		"ANTI_TANGENT_PRE_MODEL":  "openai:gpt-5",
		"ANTI_TANGENT_PLAN_MODEL": "google:gemini-2.5-pro",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, ModelRef{Provider: "google", Model: "gemini-2.5-pro"}, cfg.PlanModel)
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/config/... -run TestLoad_PlanModel -v 2>&1 | tail -10`
Expected: FAIL — `cfg.PlanModel` undefined.

- [ ] **Step 3: Add PlanModel field and resolution to config.go**

Modify `internal/config/config.go`:

(a) In the `Config` struct, add a `PlanModel` field after `PostModel`:
```go
type Config struct {
	AnthropicKey    string
	OpenAIKey       string
	GoogleKey       string
	PreModel        ModelRef
	MidModel        ModelRef
	PostModel       ModelRef
	PlanModel       ModelRef
	SessionTTL      time.Duration
	MaxPayloadBytes int
	RequestTimeout  time.Duration
	LogLevel        slog.Level
}
```

(b) Locate the existing block that resolves PreModel/MidModel/PostModel via the `defaults` map. Right AFTER that block (before any duration parsing), add:

```go
	// PlanModel: optional override; defaults to whatever PreModel resolved to.
	if v := env("ANTI_TANGENT_PLAN_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MODEL: %w", err)
		}
		cfg.PlanModel = mr
	} else {
		cfg.PlanModel = cfg.PreModel
	}
```

- [ ] **Step 4: Run tests, confirm pass**

Run: `go test -race ./internal/config/... -v 2>&1 | tail -20`
Expected: all existing tests + 3 new TestLoad_PlanModel_* tests PASS.

Run: `gofmt -l ./internal && go vet ./...`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add ANTI_TANGENT_PLAN_MODEL with default-from-PRE"
```

---

## Task 6: validate_plan handler + tool registration

**Goal:** Implement the `validate_plan` MCP tool: handler logic that splits the plan, calls the reviewer with the plan template, parses PlanResult, and returns it via the SDK's 3-return signature.

**Acceptance criteria:**
- `internal/mcpsrv/handlers.go` declares: `type ValidatePlanArgs struct { PlanText string `+"`json:\"plan_text\" jsonschema:\"required\"`"+`; ModelOverride string `+"`json:\"model_override,omitempty\"`"+` }`.
- New `func validatePlanTool() *mcp.Tool` returns a Tool with name `"validate_plan"` and the description from the design spec.
- New handler `func (h *handlers) ValidatePlan(ctx, *mcp.CallToolRequest, ValidatePlanArgs) (*mcp.CallToolResult, verdict.PlanResult, error)` does:
  1. Validate `args.PlanText != ""`; else return Go error.
  2. Compute `len(args.PlanText) > h.deps.Cfg.MaxPayloadBytes`; if exceeded, return a `PlanResult` with `plan_verdict: fail` + `plan_findings: [{category: payload_too_large, ...}]` + empty `tasks` (using a new helper `tooLargePlanResult`).
  3. Call `planparser.SplitTasks(args.PlanText)`. If `len(tasks) == 0`, return a `PlanResult` with `plan_verdict: fail` + `plan_findings: [{category: other, criterion: "structure", evidence: "no `### Task N:` headings detected", suggestion: "use `### Task N: Title` for each task"}]` + empty `tasks` (helper `noHeadingsPlanResult`). NO provider call.
  4. Resolve model: `args.ModelOverride` if set (validate same as resolveModel), else `h.deps.Cfg.PlanModel`.
  5. Call `prompts.RenderPlan(prompts.PlanInput{PlanText: args.PlanText})`.
  6. Call new `reviewPlan(ctx, model, rendered)` helper that mirrors `review()` but parses with `verdict.ParsePlan`. The helper performs the same one-retry-on-malformed-JSON behavior as `review()`.
  7. Return the parsed PlanResult, ModelUsed string, ReviewMS int64.
- Tool registered in `server.go` via `mcp.AddTool(srv, validatePlanTool(), h.ValidatePlan)`.
- Four new unit tests in `handlers_test.go` (using existing fakeReviewer pattern):
  - `TestValidatePlan_HappyPath` — fakeReviewer returns valid PlanResult JSON; handler returns it; ModelUsed populated.
  - `TestValidatePlan_NoTaskHeadings` — input "no headings" returns `plan_verdict: fail`, category `other`, no provider call (assert fakeReviewer was not invoked using a call counter on the fake).
  - `TestValidatePlan_PayloadTooLarge` — `Cfg.MaxPayloadBytes = 10`, input > 10 chars returns `plan_verdict: fail`, category `payload_too_large`.
  - `TestValidatePlan_MissingPlanText` — empty input returns Go error.
- All existing tests still pass.
- `go test -race ./...`, `go vet`, `gofmt -l` clean.

**Non-goals:**
- End-to-end MCP wire test (covered by Task 7).
- Documentation updates (Task 8).
- Updating `~/.claude/anti-tangent.md` (outside repo).

**Context:**
- The `reviewPlan` helper is a near-duplicate of the existing `review()` helper but for `PlanResult`. Don't try to make `review()` generic — Go generics on methods are awkward and the duplication is small (~25 lines).
- The existing `fakeReviewer` in `handlers_test.go` will need a way to record whether it was called (for the no-headings short-circuit assertion). Add an int counter `Calls int` to the fake; bump it in `Review()`.
- The PlanResult fakeReviewer responses for HappyPath need to be valid against the plan schema (use a tiny example like the round-trip test from Task 1).

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/mcpsrv/server.go`
- Modify: `internal/mcpsrv/handlers_test.go`

- [ ] **Step 1: Append failing tests**

First, modify the existing `fakeReviewer` in `handlers_test.go` to add a call counter. Locate the existing `fakeReviewer` struct and add a `Calls int` field; in its `Review` method, add `f.Calls++` as the first line.

Then append to `handlers_test.go`:
```go
func planPassResp() providers.Response {
	return providers.Response{
		RawJSON:      []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[{"task_index":0,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`),
		Model:        "openai:gpt-5",
		InputTokens:  3, OutputTokens: 2,
	}
}

func TestValidatePlan_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: First\n\nSome body.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	require.Len(t, pr.Tasks, 1)
	assert.Equal(t, "T1", pr.Tasks[0].TaskTitle)
}

func TestValidatePlan_NoTaskHeadings(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "Not a plan, no headings."})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, verdict.CategoryOther, pr.PlanFindings[0].Category)
	assert.Equal(t, 0, rv.Calls, "no provider call should be made")
}

func TestValidatePlan_PayloadTooLarge(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 10
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "this plan text is far too large for the configured cap of 10 bytes; it should be rejected"})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, verdict.CategoryTooLarge, pr.PlanFindings[0].Category)
	assert.Equal(t, 0, rv.Calls)
}

func TestValidatePlan_MissingPlanText(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: ""})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/mcpsrv/... -run TestValidatePlan -v 2>&1 | tail -10`
Expected: FAIL — `ValidatePlan`, `ValidatePlanArgs` undefined.

- [ ] **Step 3: Add tool descriptor + handler + helpers to handlers.go**

Modify `internal/mcpsrv/handlers.go`. Add imports for `planparser` and ensure `prompts` and `verdict` are imported (likely already are).

(a) Add the `ValidatePlanArgs` type near the other Args types:
```go
type ValidatePlanArgs struct {
	PlanText      string `json:"plan_text"      jsonschema:"required"`
	ModelOverride string `json:"model_override,omitempty"`
}
```

(b) Add the tool descriptor near `validateCompletionTool`:
```go
func validatePlanTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_plan",
		Description: "Validate an implementation plan as a whole BEFORE dispatching subagents to implement individual tasks. " +
			"Returns per-task findings and ready-to-paste structured headers (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. " +
			"Call this once at plan-handoff time; the per-task `validate_task_spec` is still called by each implementing subagent at task start.",
	}
}
```

(c) Add the helpers + handler near the bottom of the file (before the integration_test imports if any):
```go
func noHeadingsPlanResult() verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictFail,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryOther,
			Criterion:  "structure",
			Evidence:   "no `### Task N:` headings detected",
			Suggestion: "use `### Task N: Title` for each task; this tool expects numbered tasks",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Add `### Task N: Title` headings for each task and re-run validate_plan.",
	}
}

func tooLargePlanResult(size, limit int) verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictFail,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("plan_text %d bytes exceeds cap %d", size, limit),
			Suggestion: "Split the plan into smaller chunks or pass a unified diff.",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Reduce plan_text size and retry.",
	}
}

func planResult(env verdict.PlanResult, modelUsed string, ms int64) verdict.PlanResult {
	// Future hook for trimming or post-processing; currently identity.
	_ = modelUsed
	_ = ms
	return env
}

func (h *handlers) ValidatePlan(ctx context.Context, _ *mcp.CallToolRequest, args ValidatePlanArgs) (*mcp.CallToolResult, verdict.PlanResult, error) {
	if args.PlanText == "" {
		return nil, verdict.PlanResult{}, errors.New("plan_text is required")
	}
	if size := len(args.PlanText); size > h.deps.Cfg.MaxPayloadBytes {
		return planEnvelopeResult(tooLargePlanResult(size, h.deps.Cfg.MaxPayloadBytes), h.deps.Cfg.PlanModel.String(), 0)
	}
	tasks, _ := planparser.SplitTasks(args.PlanText)
	if len(tasks) == 0 {
		return planEnvelopeResult(noHeadingsPlanResult(), h.deps.Cfg.PlanModel.String(), 0)
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PlanModel)
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}

	rendered, err := prompts.RenderPlan(prompts.PlanInput{PlanText: args.PlanText})
	if err != nil {
		return nil, verdict.PlanResult{}, fmt.Errorf("render plan prompt: %w", err)
	}

	pr, modelUsed, ms, err := h.reviewPlan(ctx, model, rendered)
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}
	return planEnvelopeResult(pr, modelUsed, ms)
}

// reviewPlan mirrors review() for plan-level analysis: one provider call,
// one parse, retry-once on malformed JSON.
func (h *handlers) reviewPlan(ctx context.Context, model config.ModelRef, p prompts.Output) (verdict.PlanResult, string, int64, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PlanResult{}, "", 0, err
	}
	start := time.Now()
	req := providers.Request{
		Model:      model.Model,
		System:     p.System,
		User:       p.User,
		MaxTokens:  4096,
		JSONSchema: verdict.PlanSchema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		return verdict.PlanResult{}, "", 0, err
	}
	r, err := verdict.ParsePlan(resp.RawJSON)
	if err != nil {
		req.User = p.User + "\n\n" + verdict.RetryHint()
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

// planEnvelopeResult marshals the PlanResult into a CallToolResult (mirrors envelopeResult).
func planEnvelopeResult(pr verdict.PlanResult, modelUsed string, ms int64) (*mcp.CallToolResult, verdict.PlanResult, error) {
	body, err := json.MarshalIndent(struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{pr, modelUsed, ms}, "", "  ")
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, pr, nil
}
```

NOTE on imports: this adds a dependency on `github.com/patiently/anti-tangent-mcp/internal/planparser`. Add it to the imports block at the top of handlers.go.

- [ ] **Step 4: Register the tool in server.go**

Modify `internal/mcpsrv/server.go`. Locate the existing `New(d Deps)` function and the three existing `mcp.AddTool` calls. Add a fourth, immediately after the validate_completion registration:

```go
	mcp.AddTool(srv, validatePlanTool(), h.ValidatePlan)
```

- [ ] **Step 5: Add Calls counter to fakeReviewer (if not done in Step 1)**

Confirm `internal/mcpsrv/handlers_test.go` has the Calls counter and that `Review()` increments it. If you haven't already, edit:

```go
type fakeReviewer struct {
	name  string
	resp  providers.Response
	err   error
	Calls int
}

func (f *fakeReviewer) Review(ctx context.Context, _ providers.Request) (providers.Response, error) {
	f.Calls++
	if f.err != nil {
		return providers.Response{}, f.err
	}
	return f.resp, nil
}
```

- [ ] **Step 6: Run all mcpsrv tests**

Run: `go test -race ./internal/mcpsrv/... -v 2>&1 | tail -30`
Expected: all existing handler tests + 4 new TestValidatePlan_* PASS. Existing TestIntegration_FullLifecycle still PASS (the fakeReviewer Calls counter doesn't break it).

Run: `go test -race ./...`
Expected: all packages green.

Run: `gofmt -l ./internal && go vet ./...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(mcpsrv): validate_plan handler with no-headings + payload-cap short-circuits"
```

---

## Task 7: Integration test extension

**Goal:** Extend `internal/mcpsrv/integration_test.go` so the full-lifecycle test verifies `validate_plan` is registered, reachable via the MCP transport, and returns a parseable `PlanResult`.

**Acceptance criteria:**
- The existing `TestIntegration_FullLifecycle` still PASSES.
- A new `TestIntegration_ValidatePlan` (or extension) calls `validate_plan` via the MCP client with a small structured plan; asserts the response's text content can be unmarshaled to a `PlanResult` with `plan_verdict: pass` (because the fakeReviewer returns the `planPassResp()` shape from Task 6).
- The test uses the same `mcp.NewInMemoryTransports()` + `mcp.NewClient` pattern as the existing integration test.
- `go test -race ./internal/mcpsrv/...` green.

**Non-goals:**
- Real provider API calls.
- Verifying every handler edge case (covered in Task 6 unit tests).

**Context:** The existing test sets up Deps with one fakeReviewer ("anthropic") and exercises the existing 3 tools. For validate_plan we need a reviewer matching `cfg.PlanModel.Provider`. The simplest path: make the existing fakeReviewer respond with a plan-like JSON when `validate_plan` is called. But fakeReviewer returns one fixed `Response` regardless of which tool calls it — so the JSON body must be valid for BOTH `verdict.Parse` (existing tools) AND `verdict.ParsePlan` (new tool). That's awkward.

The cleaner solution: register two fake reviewers in the test (one for the existing tools' shape, one returning plan-shaped JSON). But the Registry maps by provider name; we'd need the new tool to use a different provider. Easiest: configure `Cfg.PlanModel = anthropic:claude-sonnet-4-6` (same provider as PRE) but have the fakeReviewer return a context-aware response.

Simplest concrete approach: use a `fakeReviewerSwitch` that inspects the request's User text — if it contains "## Plan under review" (the plan template's marker), return a plan-shaped JSON; otherwise return the per-task shape. This keeps one Registry entry but two response shapes.

**Files:**
- Modify: `internal/mcpsrv/integration_test.go`

- [ ] **Step 1: Extend the test with a switching fake**

Modify `internal/mcpsrv/integration_test.go`. At the top of the file (after the existing imports, before the existing test):

```go
type switchingFakeReviewer struct {
	name string
}

func (s *switchingFakeReviewer) Name() string { return s.name }

func (s *switchingFakeReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	if strings.Contains(req.User, "## Plan under review") {
		return providers.Response{
			RawJSON:      []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[{"task_index":0,"task_title":"Task 1: First","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`),
			Model:        "claude-sonnet-4-6",
			InputTokens:  5, OutputTokens: 4,
		}, nil
	}
	return providers.Response{
		RawJSON:      []byte(`{"verdict":"pass","findings":[],"next_action":"go"}`),
		Model:        "claude-sonnet-4-6",
		InputTokens:  3, OutputTokens: 2,
	}, nil
}
```

Add `"strings"` to the imports if not present.

Then append a new test below the existing `TestIntegration_FullLifecycle`:

```go
func TestIntegration_ValidatePlan(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)

	rv := &switchingFakeReviewer{name: "anthropic"}
	deps := Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(1 * time.Hour),
		Reviews:  providers.Registry{"anthropic": rv},
	}
	srv := New(deps)

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = srv.Run(ctx, st) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() {
		if err := cs.Close(); err != nil {
			t.Errorf("cs.Close: %v", err)
		}
	}()

	plan := "# Plan\n\n### Task 1: First\n\nSome body.\n"
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "validate_plan",
		Arguments: map[string]any{"plan_text": plan},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %v", res.Content)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	var pr struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &pr))
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	require.Len(t, pr.Tasks, 1)
	assert.Equal(t, "Task 1: First", pr.Tasks[0].TaskTitle)
}
```

Add any missing imports (`encoding/json`, `github.com/patiently/anti-tangent-mcp/internal/verdict`).

- [ ] **Step 2: Run the integration test**

Run: `go test -race ./internal/mcpsrv/... -run TestIntegration -v 2>&1 | tail -20`
Expected: both `TestIntegration_FullLifecycle` and `TestIntegration_ValidatePlan` PASS.

Run: `go test -race ./...`
Expected: all packages green.

Run: `gofmt -l ./internal && go vet ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/mcpsrv/integration_test.go
git commit -m "test(mcpsrv): integration test for validate_plan"
```

---

## Task 8: Documentation updates

**Goal:** Update INTEGRATION.md, README.md, and CHANGELOG.md to reflect the new tool surface and the revised plan-handoff gate.

**Acceptance criteria:**
- `INTEGRATION.md` §5.1 procedure rewritten to use `validate_plan` (the per-task `validate_task_spec` loop is gone from the controller's gate description). The "skip when only one task" exemption stays.
- `INTEGRATION.md` gains a new §5.5 with the comparison table from the spec (validate_plan caller=Controller, validate_task_spec caller=Implementing subagent).
- `INTEGRATION.md` cost-overhead FAQ entry updated: now says "one validate_plan call per plan-handoff" instead of "N validate_task_spec calls."
- `README.md` tool list grows from 3 to 4 (adds `validate_plan` with one-line description). The "All return the same envelope" line gains a footnote: "Except `validate_plan`, which returns a richer `PlanResult` with per-task analysis. See [INTEGRATION.md](INTEGRATION.md) for details."
- `CHANGELOG.md` `## [0.1.0] - 2026-05-07` `### Added` list gains two new bullets: one for the `validate_plan` MCP tool, one for the `ANTI_TANGENT_PLAN_MODEL` env var.
- `go test -race ./...` still green (no Go code touched here, but verify the docs didn't accidentally break anything).
- `gofmt -l ./internal && go vet ./...` clean.

**Non-goals:**
- `~/.claude/anti-tangent.md` updates (outside repo; the user does this locally as a post-merge step).
- Updating the design spec at `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` — that's the v1 design; the new tool's design lives in `2026-05-09-validate-plan-design.md`.
- New entries for already-existing v0.1.0 features in CHANGELOG.

**Context:** The exact §5.1 rewrite text and §5.5 table are in the design spec under "Integration with the existing protocol" — copy verbatim.

**Files:**
- Modify: `INTEGRATION.md`
- Modify: `README.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Rewrite INTEGRATION.md §5.1**

Read `INTEGRATION.md`. Locate the existing `### 5.1 Plan-handoff gate (REQUIRED before any dispatch)` heading (around line 313). Replace its body (down to the next `###` heading) with:

```markdown
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
```

- [ ] **Step 2: Add §5.5**

In `INTEGRATION.md`, locate the end of §5.4 (`### 5.4 Anti-pattern: don't re-validate completion from the controller`). Immediately after its closing paragraph, before the next top-level `## ` heading, add:

```markdown
### 5.5 `validate_plan` vs `validate_task_spec` — when to use which

| Tool | Caller | Lifecycle moment | Returns |
|---|---|---|---|
| `validate_plan` | Controller | Once, before any dispatch | Plan-wide + per-task analysis with ready-to-paste header blocks. Stateless. |
| `validate_task_spec` | Implementing subagent | Once at task start, after dispatch | Per-task structural/quality review. **Creates a session** that the implementer threads through `check_progress` and `validate_completion`. |

The two tools' analyses overlap intentionally: the plan gate catches plan-wide and per-task issues at handoff; the implementer gate catches anything that changed between handoff and dispatch (e.g. another agent edited the plan in the meantime) and produces the session that the rest of the implementer's lifecycle uses.
```

- [ ] **Step 3: Update INTEGRATION.md FAQ cost entry**

In `INTEGRATION.md` §6 (FAQ), locate the entry beginning with `**Cost / latency overhead.**`. Replace it with:

```markdown
**Cost / latency overhead.**
Roughly 1–2 s and $0.001–$0.02 per call, depending on payload size and model choice. One mandatory `validate_plan` call per plan-handoff, and two mandatory implementer calls per task minimum (pre + post). Use a cheap-fast model for mid-checks and a stronger model for handoff/post.
```

- [ ] **Step 4: Update README.md**

Read `README.md`. Locate the "## The 3 tools" section. Rename the heading to `## The 4 tools` and add a new bullet at the top of the list:

```markdown
- `validate_plan` — call once at plan-handoff time. Reviews an entire implementation plan and proposes ready-to-paste structured headers (Goal / AC / Non-goals / Context) for tasks that lack them. Returns per-task findings.
```

(Keep the existing three bullets unchanged.)

Locate the "All return the same envelope:" line below the bullet list. Replace it with:

```markdown
The latter three return the same envelope; `validate_plan` returns a richer `PlanResult` with per-task analysis (see [INTEGRATION.md](INTEGRATION.md) §5.5):
```

(Keep the existing JSON example block unchanged below this new line.)

- [ ] **Step 5: Update CHANGELOG.md**

Read `CHANGELOG.md`. Locate the `## [0.1.0] - 2026-05-07` block. Inside its `### Added` list, append two new bullets at the end (before the closing of the section):

```markdown
- `validate_plan` MCP tool — plan-level handoff gate that reviews an entire implementation plan in one call and proposes ready-to-paste structured-header blocks (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. Replaces the per-task plan-handoff loop.
- `ANTI_TANGENT_PLAN_MODEL` env var — overrides the model used by `validate_plan`. Defaults to `ANTI_TANGENT_PRE_MODEL`.
```

- [ ] **Step 6: Verify**

Run: `go test -race ./...`
Expected: all packages green.

Run: `gofmt -l ./internal && go vet ./...`
Expected: no output.

Run: `grep -c '^### 5\.' INTEGRATION.md`
Expected: 5 (5.1 through 5.5).

Run: `grep -c 'validate_plan' README.md INTEGRATION.md CHANGELOG.md`
Expected: at least 1 in each file.

- [ ] **Step 7: Commit and push**

```bash
git add INTEGRATION.md README.md CHANGELOG.md
git commit -m "docs(integration): document validate_plan + revised handoff gate"
git push
```

The PR (#1) picks up the new commits automatically.

---

## Self-Review Notes

Spec coverage check (each spec section → task):
- Tool surface (input/output) → Tasks 1, 6
- Server architecture & parsing → Tasks 3, 6
- Prompt strategy & JSON schema → Tasks 1, 4
- Provider integration (no changes) → covered by reusing existing infra in Task 6
- Configuration → Task 5
- Error handling (empty/oversized/no-headings/transport/malformed JSON) → Task 6 (inline assertions)
- Testing layers → Tasks 1, 2, 3, 4, 6, 7
- Versioning (folded into v0.1.0) → Task 8 (CHANGELOG)
- INTEGRATION.md updates → Task 8

No spec gaps.

Placeholder scan: no "TBD" / "TODO" / "fill in" / "similar to Task N" / vague-error-handling phrases. The single hedge in Task 3 ("if you prefer the standard testify/require, replace …") describes a stylistic choice the engineer makes once, with both alternatives defined.

Naming/property consistency:
- `PlanResult`, `PlanTaskResult`, `PlanSchema()`, `ParsePlan` consistent across Tasks 1, 2, 6, 7.
- `ValidatePlanArgs` field names `plan_text`, `model_override` consistent with the spec and the JSON schema's expectations.
- `validate_plan` tool name consistent across handler, server registration, integration test, README, INTEGRATION, CHANGELOG.
- `ANTI_TANGENT_PLAN_MODEL` env-var name consistent across config, tests, design spec, CHANGELOG.
- `RawTask{Title, Body, HasStructuredHeader}` consistent in Tasks 3, 6.
- `RenderPlan` / `PlanInput{PlanText}` consistent in Tasks 4, 6.
