# validate_plan Iteration UX (0.3.0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `0.3.0` of `anti-tangent-mcp` with the highest-leverage subset of field-report issue [#10](https://github.com/patiently/anti-tangent-mcp/issues/10): recover partial findings on reviewer-output truncation, expose per-call `max_tokens_override`, add `mode: quick | thorough` to `validate_plan`, and land two prompt ride-alongs (hypothetical-marker + `next_action` specificity).

**Architecture:** Three layers change. (1) Providers return populated `Response` bytes alongside the `ErrResponseTruncated` sentinel instead of dropping them. (2) A new tolerant JSON parser in `internal/verdict` recovers complete findings from a truncated response. (3) Handlers route truncation through the partial parser, surface a new `partial: true` envelope field, and downgrade the truncation marker from `major` to `minor`. Two new tool args (`max_tokens_override` and `mode`) thread through the existing handler-input plumbing.

**Tech Stack:** Go 1.22+, `text/template`, `encoding/json`, `httptest`, `testify`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-05-12-validate-plan-iteration-ux-design.md`. Read it before starting; this plan is the implementation order, not the source of truth on the design.

---

## Drift-protection protocol notes

**SKIP `validate_plan` for this plan.** Issue #10 is the field report that motivated this very plan. Running `validate_plan` against the plan that fixes its iteration UX bugs would be circular — the reviewer would be applying the buggy behaviour we're fixing to its own design doc. The fixes ship in this release; future plans benefit from them.

**Per-task lifecycle hooks DO apply.** Each task below has a structured Goal / Acceptance criteria / Non-goals / Context header so `validate_task_spec` is happy. Any dispatched implementer must paste the standard dispatch clause (from `~/.claude/anti-tangent.md`) into the subagent prompt and call `validate_task_spec` / `check_progress` / `validate_completion` for that task.

---

## File structure

**Created:**
- `internal/verdict/parser_partial.go` — tolerant JSON parser for truncated reviewer output. Two exported functions: `ParseResultPartial` and `ParsePlanResultPartial`. Self-contained — no dependency on the existing strict parser beyond the shared types.
- `internal/verdict/parser_partial_test.go` — table-driven unit tests for the tolerant parser. Seven cases covering complete-input parity, mid-finding truncation (per-task), mid-task truncation (plan), truncation-before-any-finding, truncation-inside-string, truncation-at-trailing-whitespace, parity with `json.Unmarshal` on a complete plan response.
- `internal/prompts/testdata/plan_basic_quick.golden`
- `internal/prompts/testdata/plan_findings_only_quick.golden`
- `internal/prompts/testdata/plan_tasks_chunk_quick.golden`

**Modified:**
- `internal/providers/openai.go` — extract `Choices[0].Message.Content` before the finish-reason check; on `"length"`, return `Response{RawJSON: ..., ...}` with the sentinel error wrapped.
- `internal/providers/anthropic.go` — same restructure for the tool_use `Input`. On `"max_tokens"` stop reason, return populated `Response` with the sentinel.
- `internal/providers/google.go` — same restructure for `Candidates[0].Content.Parts[0].Text`.
- `internal/providers/openai_test.go`, `anthropic_test.go`, `google_test.go` — one new test per file asserting that on a truncated response the returned `Response.RawJSON` is non-empty.
- `internal/verdict/verdict.go` — add `Partial bool \`json:"partial,omitempty"\`` to `Result`.
- `internal/verdict/plan.go` — add `Partial bool \`json:"partial,omitempty"\`` to `PlanResult`.
- `internal/config/config.go` — add `MaxTokensCeiling int` to `Config`; read `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384, must be positive).
- `internal/mcpsrv/handlers.go` — add `MaxTokensOverride int` to all four `*Args` structs; add `Mode string` to `ValidatePlanArgs`; route truncation through `ParseResultPartial` / `ParsePlanResultPartial`; thread `MaxTokensOverride` to `providers.Request.MaxTokens` with ceiling clamp; thread `Mode` to `PlanInput`.
- `internal/mcpsrv/handlers_test.go` — extend `fakeReviewer` to return both resp and err; add four partial-recovery tests; add `max_tokens_override` tests (zero / in-range / over-ceiling) for each tool; add `mode` plumbing and invalid-value tests.
- `internal/mcpsrv/handlers_plan_test.go` — add plan-level partial-recovery test that asserts `tasks[]` is recovered up to the truncation point.
- `internal/prompts/prompts.go` — add `Mode string` to `PlanInput`.
- `internal/prompts/templates/plan.tmpl` — add 4th paragraph (hypothetical-marker) to `## Reviewer ground rules`; add quick-mode `{{ if eq .Mode "quick" }}…{{ end }}` block in `## What to evaluate`; add `next_action` specificity nudge to `## Output`.
- `internal/prompts/templates/plan_findings_only.tmpl` — same three additions.
- `internal/prompts/templates/plan_tasks_chunk.tmpl` — same three additions.
- `internal/prompts/testdata/plan_basic.golden`, `plan_findings_only.golden`, `plan_tasks_chunk.golden` — regenerate.
- `internal/prompts/prompts_test.go` — new anchor-assertion tests for hypothetical-marker, next_action nudge, quick-mode-on, quick-mode-off.
- `CHANGELOG.md` — add `## [0.3.0] - 2026-05-12` section.
- `README.md` — document `max_tokens_override`, `mode`, and `ANTI_TANGENT_MAX_TOKENS_CEILING`.
- `INTEGRATION.md` — document `partial: true` envelope field, `max_tokens_override`, `mode`, and updated truncation-finding shape.

---

## Subagent dispatch clause (paste verbatim into every implementer prompt)

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
the session_id, your summary, AND at least one of: `final_files` (full
file contents), `final_diff` (a unified diff), or `test_evidence` (test
command output). Summary-only requests are rejected (since 0.2.0) with
`at least one of final_files, final_diff, or test_evidence must be
non-empty`. Prefer `final_diff` when changes are large enough that
pasting whole files would risk the 200KB payload cap. If the verdict is
`fail` or contains `critical`/`major` findings, do not report DONE —
fix the findings and re-validate.

## Task spec (pass these fields verbatim to validate_task_spec)

- task_title:           <from the task block>
- goal:                 <from "Goal:">
- acceptance_criteria:  <from "Acceptance criteria:" bullets>
- non_goals:            <from "Non-goals:" bullets if present>
- context:              <from "Context:" if present>
```

---

### Task 1: Provider truncation returns partial response bytes

**Goal:** Each of the three reviewer providers, when the model's finish reason indicates a `max_tokens` cap hit, returns a populated `Response{RawJSON, Model, InputTokens, OutputTokens}` alongside the `ErrResponseTruncated` sentinel instead of an empty `Response{}`.

**Acceptance criteria:**
- `openai.go`, `anthropic.go`, `google.go` each return a non-empty `Response.RawJSON` (the partial model output text) when their truncation branch fires.
- The `ErrResponseTruncated` sentinel is still wrapped with the provider prefix (e.g. `"openai: reviewer response truncated at max_tokens limit"`), so `errors.Is(err, providers.ErrResponseTruncated)` continues to work at call sites.
- One new unit test per provider file asserts that a stubbed truncated HTTP response yields `(non-empty Response, error wrapping ErrResponseTruncated)`.
- `go test -race ./internal/providers/...` is green.
- No new test against a real network; all use `httptest.Server`.

**Non-goals:**
- No changes to handlers, parsers, or envelopes in this task — Task 3 wires the new bytes into the handler partial-recovery branch.
- No new env vars in this task.

**Context:** Current shape is uniform across providers — see `internal/providers/openai.go:105`, `anthropic.go:100`, `google.go:109`. Each provider parses its response JSON, checks the finish reason, and *returns early* if truncated, discarding the partial text that's already in the parsed struct. The fix is to extract the partial text *before* the finish-reason check, then return it inside the `Response` even when truncation is signalled.

**Files:**
- Modify: `internal/providers/openai.go:86-115`
- Modify: `internal/providers/anthropic.go:85-115`
- Modify: `internal/providers/google.go:88-122`
- Modify: `internal/providers/openai_test.go` (add one test)
- Modify: `internal/providers/anthropic_test.go` (add one test)
- Modify: `internal/providers/google_test.go` (add one test)

- [ ] **Step 1: Write the OpenAI partial-bytes test**

Add at the bottom of `internal/providers/openai_test.go`:

```go
func TestOpenAI_TruncatedResponseReturnsPartialBytes(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "gpt-5",
			"choices": [{
				"finish_reason": "length",
				"message": {"content": "{\"verdict\":\"warn\",\"findings\":[{\"severity\":\"major\""}
			}],
			"usage": {"prompt_tokens": 100, "completion_tokens": 4096}
		}`))
	}))
	defer srv.Close()

	rv := NewOpenAI("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model: "gpt-5", System: "s", User: "u",
		MaxTokens: 4096, JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrResponseTruncated))
	assert.NotEmpty(t, resp.RawJSON, "truncated response should still carry partial bytes")
	assert.Contains(t, string(resp.RawJSON), `"severity":"major"`)
	assert.Equal(t, "gpt-5", resp.Model)
	assert.Equal(t, 100, resp.InputTokens)
	assert.Equal(t, 4096, resp.OutputTokens)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./internal/providers/ -run TestOpenAI_TruncatedResponseReturnsPartialBytes -v`
Expected: FAIL with `truncated response should still carry partial bytes` because the current code returns `Response{}` on truncation.

- [ ] **Step 3: Update `internal/providers/openai.go` to return partial bytes**

Replace the block from line 102 (`if len(parsed.Choices) == 0`) through line 114 (closing `}, nil`) with:

```go
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: no choices in response")
	}

	resp := Response{
		RawJSON:      []byte(parsed.Choices[0].Message.Content),
		Model:        parsed.Model,
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}
	if parsed.Choices[0].FinishReason == "length" {
		return resp, fmt.Errorf("openai: %w", ErrResponseTruncated)
	}
	return resp, nil
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./internal/providers/ -run TestOpenAI_TruncatedResponseReturnsPartialBytes -v`
Expected: PASS.

- [ ] **Step 5: Write the Anthropic partial-bytes test**

Add at the bottom of `internal/providers/anthropic_test.go`:

```go
func TestAnthropic_TruncatedResponseReturnsPartialBytes(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "claude-opus-4-7",
			"stop_reason": "max_tokens",
			"content": [
				{"type": "tool_use", "input": {"verdict":"warn","findings":[{"severity":"major"}]}}
			],
			"usage": {"input_tokens": 200, "output_tokens": 4096}
		}`))
	}))
	defer srv.Close()

	rv := NewAnthropic("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model: "claude-opus-4-7", System: "s", User: "u",
		MaxTokens: 4096, JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrResponseTruncated))
	assert.NotEmpty(t, resp.RawJSON, "truncated response should still carry partial bytes")
	assert.Contains(t, string(resp.RawJSON), `"severity":"major"`)
	assert.Equal(t, "claude-opus-4-7", resp.Model)
	assert.Equal(t, 200, resp.InputTokens)
	assert.Equal(t, 4096, resp.OutputTokens)
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `go test -race ./internal/providers/ -run TestAnthropic_TruncatedResponseReturnsPartialBytes -v`
Expected: FAIL because current code returns `Response{}` on `max_tokens` stop reason.

- [ ] **Step 7: Update `internal/providers/anthropic.go` to return partial bytes**

Replace the block from line 100 (`if parsed.StopReason == "max_tokens"`) through line 114 (`return Response{}, fmt.Errorf("anthropic: no tool_use block in response")`) with:

```go
	// Extract the tool_use input first — Anthropic returns it inside the
	// content array even when stop_reason is "max_tokens".
	var raw json.RawMessage
	for _, c := range parsed.Content {
		if c.Type == "tool_use" && len(c.Input) > 0 {
			raw = c.Input
			break
		}
	}
	if len(raw) == 0 {
		// Truncation that hit before any tool_use input materialized;
		// fall through to the "no tool_use block" error.
		if parsed.StopReason == "max_tokens" {
			return Response{}, fmt.Errorf("anthropic: %w", ErrResponseTruncated)
		}
		return Response{}, fmt.Errorf("anthropic: no tool_use block in response")
	}

	resp := Response{
		RawJSON:      []byte(raw),
		Model:        parsed.Model,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}
	if parsed.StopReason == "max_tokens" {
		return resp, fmt.Errorf("anthropic: %w", ErrResponseTruncated)
	}
	return resp, nil
```

- [ ] **Step 8: Run the Anthropic test to verify it passes**

Run: `go test -race ./internal/providers/ -run TestAnthropic_TruncatedResponseReturnsPartialBytes -v`
Expected: PASS.

- [ ] **Step 9: Write the Google partial-bytes test**

Add at the bottom of `internal/providers/google_test.go`:

```go
func TestGoogle_TruncatedResponseReturnsPartialBytes(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"finishReason": "MAX_TOKENS",
				"content": {"parts": [{"text": "{\"verdict\":\"warn\",\"findings\":[{\"severity\":\"major\""}]}
			}],
			"usageMetadata": {"promptTokenCount": 300, "candidatesTokenCount": 4096},
			"modelVersion": "gemini-2.5-pro"
		}`))
	}))
	defer srv.Close()

	rv := NewGoogle("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model: "gemini-2.5-pro", System: "s", User: "u",
		MaxTokens: 4096, JSONSchema: []byte(`{"type":"object"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrResponseTruncated))
	assert.NotEmpty(t, resp.RawJSON, "truncated response should still carry partial bytes")
	assert.Contains(t, string(resp.RawJSON), `"severity":"major"`)
	assert.Equal(t, "gemini-2.5-pro", resp.Model)
	assert.Equal(t, 300, resp.InputTokens)
	assert.Equal(t, 4096, resp.OutputTokens)
}
```

- [ ] **Step 10: Run the test to verify it fails**

Run: `go test -race ./internal/providers/ -run TestGoogle_TruncatedResponseReturnsPartialBytes -v`
Expected: FAIL because current code returns `Response{}` before extracting text.

- [ ] **Step 11: Update `internal/providers/google.go` to return partial bytes**

Replace the block from line 106 (`if len(parsed.Candidates) == 0`) through line 122 (closing `}, nil`) with:

```go
	if len(parsed.Candidates) == 0 {
		return Response{}, fmt.Errorf("google: no candidates in response")
	}

	var text string
	if len(parsed.Candidates[0].Content.Parts) > 0 {
		text = parsed.Candidates[0].Content.Parts[0].Text
	}

	resp := Response{
		RawJSON:      []byte(text),
		Model:        parsed.ModelVersion,
		InputTokens:  parsed.UsageMetadata.PromptTokenCount,
		OutputTokens: parsed.UsageMetadata.CandidatesTokenCount,
	}
	if parsed.Candidates[0].FinishReason == "MAX_TOKENS" {
		return resp, fmt.Errorf("google: %w", ErrResponseTruncated)
	}
	if text == "" {
		return Response{}, fmt.Errorf("google: candidate has no content parts")
	}
	return resp, nil
```

- [ ] **Step 12: Run the Google test to verify it passes**

Run: `go test -race ./internal/providers/ -run TestGoogle_TruncatedResponseReturnsPartialBytes -v`
Expected: PASS.

- [ ] **Step 13: Run the full provider test suite to verify no regressions**

Run: `go test -race ./internal/providers/...`
Expected: PASS — including the existing truncation tests, which were only checking that `ErrResponseTruncated` was returned; they do not assert empty `Response{}` so they continue to hold.

- [ ] **Step 14: Commit**

```bash
git add internal/providers/openai.go internal/providers/anthropic.go internal/providers/google.go internal/providers/openai_test.go internal/providers/anthropic_test.go internal/providers/google_test.go
git commit -m "feat(providers): return partial bytes on truncation

Each provider now extracts the model's partial output text BEFORE
checking the finish reason, so truncated responses carry the partial
bytes alongside the ErrResponseTruncated sentinel. Foundation for
partial-findings recovery in subsequent handler change.

Refs #10."
```

---

### Task 2: Tolerant JSON parser for truncated reviewer output

**Goal:** Two new functions in `internal/verdict` — `ParseResultPartial(raw []byte) (Result, bool)` and `ParsePlanResultPartial(raw []byte) (PlanResult, bool)` — that accept possibly-truncated reviewer JSON, attempt a strict parse first, and on failure walk the bytes to recover any complete `Finding` and `PlanTaskResult` elements before the truncation point.

**Acceptance criteria:**
- Both functions return `(zero, false)` only when no complete findings could be recovered; otherwise return `(result, true)` with `Partial: true` set.
- For `ParsePlanResultPartial`, "at least one complete finding" counts findings across `plan_findings[]` AND each `tasks[].findings[]` — recovering only task-level findings still returns `(result, true)`.
- Complete inputs (no truncation) round-trip identically to `json.Unmarshal`. This is asserted by a parity test that feeds both a strict input and a strict plan input.
- Truncation inside a JSON string literal returns `(zero, false)` — we don't attempt to recover from mid-string truncation.
- Truncation at trailing whitespace after valid JSON returns the full parsed result with `Partial: false` (the JSON was actually complete).
- All seven table-driven cases in `parser_partial_test.go` pass.
- `go test -race ./internal/verdict/...` is green.

**Non-goals:**
- No changes to the strict `Parse` / `ParsePlan` functions — they continue to be the happy-path parser.
- No new third-party JSON library — uses only `encoding/json` and a hand-rolled brace-tracker.
- No changes to the JSON schemas (`schema.json`, `plan_schema.json`) — the recovery is over the wire format, not the schema.

**Context:** Reviewer output is constrained by the JSON schema to a fixed shape with a small number of top-level keys. The only unbounded arrays are `findings[]` for per-task results, and `plan_findings[]` / `tasks[]` / `tasks[i].findings[]` for plan results. Truncation almost always hits inside one of these arrays. The algorithm: try strict parse first; on failure, find the last complete `{...}` element inside the deepest unbounded array containing the truncation point, truncate after it, close any open brackets/braces in reverse depth order, retry strict parse.

**Files:**
- Create: `internal/verdict/parser_partial.go`
- Create: `internal/verdict/parser_partial_test.go`
- Modify: `internal/verdict/verdict.go` (add `Partial` field to `Result`)
- Modify: `internal/verdict/plan.go` (add `Partial` field to `PlanResult`)

- [ ] **Step 1: Add `Partial` field to `Result` and `PlanResult`**

In `internal/verdict/verdict.go`, change the `Result` struct (line 44):

```go
type Result struct {
	Verdict    Verdict   `json:"verdict"`
	Findings   []Finding `json:"findings"`
	NextAction string    `json:"next_action"`
	Partial    bool      `json:"partial,omitempty"`
}
```

In `internal/verdict/plan.go`, change the `PlanResult` struct (line 36):

```go
type PlanResult struct {
	PlanVerdict  Verdict          `json:"plan_verdict"`
	PlanFindings []Finding        `json:"plan_findings"`
	Tasks        []PlanTaskResult `json:"tasks"`
	NextAction   string           `json:"next_action"`
	Partial      bool             `json:"partial,omitempty"`
}
```

- [ ] **Step 2: Run existing verdict tests to confirm field addition is non-breaking**

Run: `go test -race ./internal/verdict/...`
Expected: PASS — the new field has zero value `false` and `omitempty` keeps it out of serialized output when not set, so existing tests do not see it.

- [ ] **Step 3: Write the parser_partial tests**

Create `internal/verdict/parser_partial_test.go`:

```go
package verdict

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResultPartial_CompleteInputMatchesStrict(t *testing.T) {
	raw := []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"other","criterion":"c","evidence":"e","suggestion":"s"}],"next_action":"do thing"}`)

	got, ok := ParseResultPartial(raw)
	require.True(t, ok, "complete input should parse successfully")
	assert.False(t, got.Partial, "complete input should not be marked partial")

	var want Result
	require.NoError(t, json.Unmarshal(raw, &want))
	assert.Equal(t, want.Verdict, got.Verdict)
	assert.Equal(t, want.Findings, got.Findings)
	assert.Equal(t, want.NextAction, got.NextAction)
}

func TestParsePlanResultPartial_CompleteInputMatchesStrict(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_findings":[{"severity":"major","category":"other","criterion":"c","evidence":"e","suggestion":"s"}],"tasks":[{"task_index":0,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`)

	got, ok := ParsePlanResultPartial(raw)
	require.True(t, ok)
	assert.False(t, got.Partial)

	var want PlanResult
	require.NoError(t, json.Unmarshal(raw, &want))
	assert.Equal(t, want.PlanVerdict, got.PlanVerdict)
	assert.Equal(t, want.PlanFindings, got.PlanFindings)
	assert.Len(t, got.Tasks, 1)
}

func TestParseResultPartial_TruncatedMidFinding(t *testing.T) {
	// Three findings; truncation hits in the middle of the 3rd object.
	raw := []byte(`{"verdict":"warn","findings":[` +
		`{"severity":"major","category":"other","criterion":"c1","evidence":"e1","suggestion":"s1"},` +
		`{"severity":"minor","category":"other","criterion":"c2","evidence":"e2","suggestion":"s2"},` +
		`{"severity":"critical","category":"other","crit`)

	got, ok := ParseResultPartial(raw)
	require.True(t, ok, "should recover the first two complete findings")
	assert.True(t, got.Partial)
	assert.Len(t, got.Findings, 2)
	assert.Equal(t, "c1", got.Findings[0].Criterion)
	assert.Equal(t, "c2", got.Findings[1].Criterion)
}

func TestParsePlanResultPartial_TruncatedMidTask(t *testing.T) {
	// Two complete tasks, truncation inside the third task's findings list.
	raw := []byte(`{"plan_verdict":"warn","plan_findings":[],"tasks":[` +
		`{"task_index":0,"task_title":"T0","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":1,"task_title":"T1","verdict":"warn","findings":[{"severity":"minor","category":"other","criterion":"c","evidence":"e","suggestion":"s"}],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2,"task_title":"T2","verdict":"warn","findings":[{"severity":"major","cat`)

	got, ok := ParsePlanResultPartial(raw)
	require.True(t, ok, "should recover the two complete tasks")
	assert.True(t, got.Partial)
	assert.Len(t, got.Tasks, 2)
	assert.Equal(t, "T0", got.Tasks[0].TaskTitle)
	assert.Equal(t, "T1", got.Tasks[1].TaskTitle)
}

func TestParseResultPartial_TruncatedBeforeAnyFinding(t *testing.T) {
	raw := []byte(`{"verdict":"warn","findings":[{"severity":"maj`)

	got, ok := ParseResultPartial(raw)
	assert.False(t, ok, "no complete finding recovered should return false")
	assert.Empty(t, got.Findings)
}

func TestParseResultPartial_TruncatedInsideStringLiteral(t *testing.T) {
	// Truncation hits inside the evidence string of the first finding.
	raw := []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"other","criterion":"c1","evidence":"this is a long evidence stri`)

	got, ok := ParseResultPartial(raw)
	assert.False(t, ok, "truncation inside a string literal cannot recover")
	assert.Empty(t, got.Findings)
}

func TestParseResultPartial_TruncatedAtTrailingWhitespace(t *testing.T) {
	// Valid complete JSON followed by truncation in trailing whitespace.
	raw := []byte(`{"verdict":"pass","findings":[],"next_action":"go"}    `)

	got, ok := ParseResultPartial(raw)
	require.True(t, ok)
	assert.False(t, got.Partial, "complete JSON with trailing whitespace should not be marked partial")
	assert.Equal(t, Verdict("pass"), got.Verdict)
}
```

- [ ] **Step 4: Run the partial-parser tests to verify they fail**

Run: `go test -race ./internal/verdict/ -run "TestParseResultPartial|TestParsePlanResultPartial" -v`
Expected: FAIL with `undefined: ParseResultPartial` and `undefined: ParsePlanResultPartial`.

- [ ] **Step 5: Implement the tolerant parser**

Create `internal/verdict/parser_partial.go`:

```go
package verdict

import (
	"bytes"
	"encoding/json"
)

// ParseResultPartial parses a possibly-truncated reviewer response into a
// Result. Returns (result, true) when partial recovery succeeded with at
// least one complete finding; (result, false) when no findings could be
// recovered (caller should fall back to truncatedEnvelope).
func ParseResultPartial(raw []byte) (Result, bool) {
	// Try strict parse first — most calls aren't truncated.
	var r Result
	if err := json.Unmarshal(bytes.TrimSpace(raw), &r); err == nil {
		return r, true
	}

	// Locate the findings array and recover complete elements.
	recovered, ok := repairTopLevelArray(raw, "findings")
	if !ok {
		return Result{}, false
	}
	if err := json.Unmarshal(recovered, &r); err != nil {
		return Result{}, false
	}
	if len(r.Findings) == 0 {
		return Result{}, false
	}
	r.Partial = true
	return r, true
}

// ParsePlanResultPartial does the same for the plan-level shape. The
// "at least one complete finding" success criterion counts findings
// across both plan_findings[] AND each tasks[].findings[] — recovering
// only task-level findings still returns (result, true).
func ParsePlanResultPartial(raw []byte) (PlanResult, bool) {
	// Try strict parse first.
	var pr PlanResult
	if err := json.Unmarshal(bytes.TrimSpace(raw), &pr); err == nil {
		return pr, true
	}

	// Plan-level recovery walks down: try to recover tasks[], then
	// inside each surviving task, plan_findings[] is whatever made it
	// through. The repair tries the outermost unbounded array first.
	recovered, ok := repairTopLevelArray(raw, "tasks")
	if !ok {
		// Truncation may have hit before tasks[] started; try recovering
		// plan_findings[] only.
		recovered, ok = repairTopLevelArray(raw, "plan_findings")
		if !ok {
			return PlanResult{}, false
		}
	}
	if err := json.Unmarshal(recovered, &pr); err != nil {
		return PlanResult{}, false
	}

	totalFindings := len(pr.PlanFindings)
	for _, t := range pr.Tasks {
		totalFindings += len(t.Findings)
	}
	if totalFindings == 0 {
		return PlanResult{}, false
	}
	pr.Partial = true
	return pr, true
}

// repairTopLevelArray scans raw for the named array (e.g. "findings"),
// finds the last complete `{...}` element, truncates after it, closes any
// open brackets/braces in reverse depth order, and returns the repaired
// bytes. Returns ok=false if the array couldn't be located or no complete
// element exists.
//
// The walk tracks: brace depth, bracket depth, quote state (including
// backslash-escape), and stack of opener characters. Truncation inside a
// string literal returns ok=false — we don't try to recover from mid-string
// cuts.
func repairTopLevelArray(raw []byte, arrayKey string) ([]byte, bool) {
	// Locate `"<arrayKey>":` and then the opening `[`.
	needle := []byte(`"` + arrayKey + `":`)
	arrStart := bytes.Index(raw, needle)
	if arrStart < 0 {
		return nil, false
	}
	// Advance past the colon and any whitespace, then expect '['.
	i := arrStart + len(needle)
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\n') {
		i++
	}
	if i >= len(raw) || raw[i] != '[' {
		return nil, false
	}
	arrayOpenIdx := i
	i++ // move past '['

	// Walk forward, tracking depth. lastCompleteElementEnd is the byte
	// index just AFTER the last complete `{...}` (or primitive) inside
	// this array's top level.
	depth := 1 // inside the named array
	inString := false
	escape := false
	lastCompleteElementEnd := -1

	// Track which stack openers we passed through ('[' or '{') and their
	// positions to know where to close in repair mode.
	openers := []byte{'['}

	for ; i < len(raw); i++ {
		b := raw[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if b == '\\' {
				escape = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '[':
			depth++
			openers = append(openers, '[')
		case '{':
			depth++
			openers = append(openers, '{')
		case ']':
			depth--
			if len(openers) > 0 {
				openers = openers[:len(openers)-1]
			}
			if depth == 0 {
				// Array closed cleanly — caller can defer to strict parse,
				// but we got here because strict failed, so something after
				// this array is the problem. Return everything up to i+1
				// plus closing of any further structure handled below.
				// For simplicity, return the prefix up to i+1 plus closing
				// braces that the outer object needs.
				return closeOuterObject(raw[:i+1]), true
			}
			if depth == 1 {
				lastCompleteElementEnd = i + 1
			}
		case '}':
			depth--
			if len(openers) > 0 {
				openers = openers[:len(openers)-1]
			}
			if depth == 1 {
				lastCompleteElementEnd = i + 1
			}
		case ',':
			if depth == 1 {
				// A comma at depth 1 separates array elements; the element
				// before it is complete.
				lastCompleteElementEnd = i
			}
		}
	}

	// Reached end of input while still inside the array.
	if inString {
		return nil, false
	}
	if lastCompleteElementEnd <= arrayOpenIdx+1 {
		// No complete element inside the array yet.
		return nil, false
	}

	// Build repaired bytes: prefix up to last complete element, close ']'
	// for the array, then close enclosing '}' for the outer object.
	repaired := make([]byte, 0, lastCompleteElementEnd+8)
	repaired = append(repaired, raw[:lastCompleteElementEnd]...)
	repaired = append(repaired, ']', '}')
	return repaired, true
}

// closeOuterObject takes a byte slice ending with a complete inner array
// and appends a closing '}' if the outer object is missing one. Used when
// the named array closed cleanly but the outer object did not.
func closeOuterObject(prefix []byte) []byte {
	out := make([]byte, len(prefix)+1)
	copy(out, prefix)
	out[len(prefix)] = '}'
	return out
}
```

- [ ] **Step 6: Run the partial-parser tests to verify they pass**

Run: `go test -race ./internal/verdict/ -run "TestParseResultPartial|TestParsePlanResultPartial" -v`
Expected: all seven tests PASS.

- [ ] **Step 7: Run the full verdict test suite to confirm no regressions**

Run: `go test -race ./internal/verdict/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/verdict/parser_partial.go internal/verdict/parser_partial_test.go internal/verdict/verdict.go internal/verdict/plan.go
git commit -m "feat(verdict): tolerant JSON parser for truncated responses

Adds ParseResultPartial and ParsePlanResultPartial that recover any
complete findings from a reviewer response that was cut short by a
max_tokens cap. Adds a Partial bool field (omitempty) to Result and
PlanResult. Complete inputs round-trip identically to strict
json.Unmarshal; truncation inside a string literal returns no result.

Refs #10."
```

---

### Task 3: Handler-level partial-findings recovery for all four tools

**Goal:** Each of the four tool handlers — `ValidateTaskSpec`, `CheckProgress`, `ValidateCompletion`, `ValidatePlan` — routes a truncated reviewer response through `ParseResultPartial` / `ParsePlanResultPartial` and surfaces any recovered findings in the envelope. The synthetic truncation finding is downgraded from `major` to `minor` and reworded to reference both env-var and `max_tokens_override` mitigations.

**Acceptance criteria:**
- When a provider returns `(populated Response, ErrResponseTruncated)`, the handler attempts partial parse; if at least one finding is recovered, the envelope returns `Partial: true`, the recovered findings, AND a single `minor` truncation finding noting the count.
- When the partial parser returns `(zero, false)`, the handler falls back to the existing single-finding truncation envelope (current behaviour).
- The new behaviour is asserted by four new tests (one per tool), each with a `fakeReviewer` returning `(populated Response, ErrResponseTruncated)`.
- Existing truncation tests in `handlers_test.go` (lines 340, 359, 387, 414) need updating to match the new severity (`minor`) and finding count when partial bytes are present; tests with empty-bytes truncation continue to surface the legacy `major` finding.
- `fakeReviewer.Review` is changed to return `(f.resp, f.err)` so tests can stub the "(partial bytes, truncation error)" shape.
- `go test -race ./internal/mcpsrv/...` is green.

**Non-goals:**
- No new args in this task — `max_tokens_override` is Task 4, `mode` is Task 5.
- No prompt template changes in this task — prompts ride-along is Task 6.
- No documentation changes — Task 7.

**Context:** Currently the handlers have a uniform branch (e.g. `handlers.go:82`, `:247`, `:438`, `:488`) that converts `ErrResponseTruncated` into a hard-coded "warn + single major finding" envelope and discards `resp.RawJSON`. After Task 1, `resp.RawJSON` will be populated; after Task 2, the partial parser can extract findings from it. This task wires those two together at the handler boundary. The new helper, `recoverPartialFindings`, encapsulates the parse-or-fallback decision.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` — modify `truncatedEnvelope` (line 317) and `truncatedPlanResult` (line 333); add `recoverPartialFindings` helper; thread partial-recovery into all four handlers.
- Modify: `internal/mcpsrv/handlers_test.go` — update `fakeReviewer.Review`; update existing truncation tests; add four new partial-recovery tests.
- Modify: `internal/mcpsrv/handlers_plan_test.go` — add a plan-level partial-recovery test asserting `tasks[]` is recovered up to the truncation point.

- [ ] **Step 1: Update `fakeReviewer.Review` to return both resp and err**

In `internal/mcpsrv/handlers_test.go` change the `Review` method (line 28-34):

```go
func (f *fakeReviewer) Review(ctx context.Context, _ providers.Request) (providers.Response, error) {
	f.Calls++
	return f.resp, f.err
}
```

- [ ] **Step 2: Run the existing handler test suite to confirm the fakeReviewer change is non-breaking**

Run: `go test -race ./internal/mcpsrv/...`
Expected: PASS — existing tests either set only `resp` (no `err`) or only `err` (no `resp`), so behaviour is unchanged.

- [ ] **Step 3: Write the ValidateTaskSpec partial-recovery test**

Add to `internal/mcpsrv/handlers_test.go` (near the other truncation tests, after `TestValidateTaskSpec_TruncatedResponseSurfacesWarn`):

```go
func TestValidateTaskSpec_PartialFindingsRecoveredOnTruncation(t *testing.T) {
	// Populated RawJSON with one complete finding, then truncation in the
	// middle of a second finding.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"major","category":"other","criterion":"ac1","evidence":"e1","suggestion":"s1"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)
	assert.Equal(t, "warn", env.Verdict)

	// One recovered finding + one minor truncation marker = 2 total.
	require.Len(t, env.Findings, 2)
	// Recovered finding comes first.
	assert.Equal(t, "ac1", env.Findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	// Truncation marker is minor and references both env var and override.
	assert.Equal(t, verdict.SeverityMinor, env.Findings[1].Severity)
	assert.Equal(t, verdict.CategoryOther, env.Findings[1].Category)
	assert.Contains(t, env.Findings[1].Evidence, "1 complete findings recovered")
	assert.Contains(t, env.Findings[1].Suggestion, "max_tokens_override")
	assert.Contains(t, env.Findings[1].Suggestion, "ANTI_TANGENT_PER_TASK_MAX_TOKENS")
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test -race ./internal/mcpsrv/ -run TestValidateTaskSpec_PartialFindingsRecoveredOnTruncation -v`
Expected: FAIL — currently the handler returns a single `major` finding with no recovered content.

- [ ] **Step 5: Add the `recoverPartialFindings` helper and wire it into `ValidateTaskSpec`**

In `internal/mcpsrv/handlers.go`, add this helper near `truncatedEnvelope` (around line 317):

```go
// recoverPartialFindings attempts to extract complete findings from a
// truncated reviewer response. Returns (envelope, true) when at least
// one finding was recovered; (zero, false) when the caller should fall
// back to truncatedEnvelope.
//
// The returned envelope has Verdict="warn", Findings = recovered list
// plus a single minor "truncation marker" finding, Partial=true via
// the underlying Result, and NextAction = result's next_action if
// recovered or a generic fallback.
func recoverPartialFindings(id string, model config.ModelRef, rawJSON []byte, envVar string) (Envelope, bool) {
	if len(rawJSON) == 0 {
		return Envelope{}, false
	}
	r, ok := verdict.ParseResultPartial(rawJSON)
	if !ok || len(r.Findings) == 0 {
		return Envelope{}, false
	}
	marker := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "reviewer_response",
		Evidence:   fmt.Sprintf("reviewer output truncated at the max_tokens cap; %d complete findings recovered", len(r.Findings)),
		Suggestion: "Raise " + envVar + " or pass max_tokens_override on the next call to capture more.",
	}
	findings := append([]verdict.Finding{}, r.Findings...)
	findings = append(findings, marker)
	next := r.NextAction
	if next == "" {
		next = "Address recovered findings; reviewer output was truncated, so the list may be incomplete."
	}
	return Envelope{
		SessionID:  id,
		Verdict:    string(verdict.VerdictWarn),
		Findings:   findings,
		NextAction: next,
		ModelUsed:  model.String(),
	}, true
}
```

Then update the `ValidateTaskSpec` truncation branch (currently lines 82-85):

```go
	result, modelUsed, ms, err := h.review(ctx, model, rendered)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			// review() doesn't return the partial bytes today — we need the
			// resp to reach this branch. Refactor below in Step 7.
			return envelopeResult(truncatedEnvelope("", model))
		}
		return nil, Envelope{}, err
	}
```

You'll see that the existing `review` helper consumes the response internally and discards it on error. We need to surface the partial bytes too. Continue to step 6.

- [ ] **Step 6: Update `review` to return the partial response on truncation**

In `internal/mcpsrv/handlers.go`, change the `review` helper (line 110) to return the response bytes even on truncation. New signature returns `(Result, string, int64, []byte, error)` — the new `[]byte` is the partial `RawJSON` when truncation occurred:

```go
// review runs a single reviewer call with one parse-retry on malformed JSON.
// On ErrResponseTruncated, the returned []byte carries the partial response
// bytes (possibly empty if the provider gave none) so the caller can attempt
// partial-findings recovery.
func (h *handlers) review(ctx context.Context, model config.ModelRef, p prompts.Output) (verdict.Result, string, int64, []byte, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.Result{}, "", 0, nil, err
	}
	start := time.Now()

	req := providers.Request{
		Model:      model.Model,
		System:     p.System,
		User:       p.User,
		MaxTokens:  h.deps.Cfg.PerTaskMaxTokens,
		JSONSchema: verdict.Schema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			return verdict.Result{}, "", 0, resp.RawJSON, err
		}
		return verdict.Result{}, "", 0, nil, err
	}
	r, err := verdict.Parse(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder.
		req.User = p.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.Result{}, "", 0, resp.RawJSON, err
			}
			return verdict.Result{}, "", 0, nil, err
		}
		r, err = verdict.Parse(resp.RawJSON)
		if err != nil {
			return verdict.Result{}, "", 0, nil, fmt.Errorf("provider response failed schema after retry: %w", err)
		}
	}

	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil, nil
}
```

- [ ] **Step 7: Update `ValidateTaskSpec` to use partial recovery**

Replace the existing call to `h.review` plus the truncation branch (lines 80-86):

```go
	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			if env, ok := recoverPartialFindings("", model, partialRaw, "ANTI_TANGENT_PER_TASK_MAX_TOKENS"); ok {
				return envelopeResult(env)
			}
			return envelopeResult(truncatedEnvelope("", model))
		}
		return nil, Envelope{}, err
	}
```

- [ ] **Step 8: Run the ValidateTaskSpec partial-recovery test to verify it passes**

Run: `go test -race ./internal/mcpsrv/ -run TestValidateTaskSpec_PartialFindingsRecoveredOnTruncation -v`
Expected: PASS.

- [ ] **Step 9: Update CheckProgress and ValidateCompletion truncation branches**

In `internal/mcpsrv/handlers.go`, replace the `CheckProgress` truncation branch (line 245-250):

```go
	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			if env, ok := recoverPartialFindings(sess.ID, model, partialRaw, "ANTI_TANGENT_PER_TASK_MAX_TOKENS"); ok {
				env = h.withSessionTTL(env, sess)
				return envelopeResult(env)
			}
			return envelopeResult(truncatedEnvelope(sess.ID, model))
		}
		return nil, Envelope{}, err
	}
```

And the `ValidateCompletion` truncation branch (line 436-441):

```go
	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			if env, ok := recoverPartialFindings(sess.ID, model, partialRaw, "ANTI_TANGENT_PER_TASK_MAX_TOKENS"); ok {
				env = h.withSessionTTL(env, sess)
				return envelopeResult(env)
			}
			return envelopeResult(truncatedEnvelope(sess.ID, model))
		}
		return nil, Envelope{}, err
	}
```

- [ ] **Step 10: Add CheckProgress and ValidateCompletion partial-recovery tests**

Add to `internal/mcpsrv/handlers_test.go`:

```go
func TestCheckProgress_PartialFindingsRecoveredOnTruncation(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G",
	})
	require.NoError(t, err)

	h.deps.Reviews = providers.Registry{"anthropic": &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"major","category":"other","criterion":"cp1","evidence":"e1","suggestion":"s1"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}}

	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: pre.SessionID, WorkingOn: "x",
	})
	require.NoError(t, err)
	require.Len(t, env.Findings, 2)
	assert.Equal(t, "cp1", env.Findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMinor, env.Findings[1].Severity)
	assert.Contains(t, env.Findings[1].Suggestion, "max_tokens_override")
	assert.Equal(t, pre.SessionID, env.SessionID)
}

func TestValidateCompletion_PartialFindingsRecoveredOnTruncation(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	h.deps.Reviews = providers.Registry{"anthropic": &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"critical","category":"other","criterion":"vc1","evidence":"e1","suggestion":"s1"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}}

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n"}},
	})
	require.NoError(t, err)
	require.Len(t, env.Findings, 2)
	assert.Equal(t, "vc1", env.Findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMinor, env.Findings[1].Severity)
	assert.Contains(t, env.Findings[1].Suggestion, "max_tokens_override")
	assert.Equal(t, pre.SessionID, env.SessionID)
}
```

- [ ] **Step 11: Run the per-task partial-recovery tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/ -run "TestValidateTaskSpec_PartialFindingsRecoveredOnTruncation|TestCheckProgress_PartialFindingsRecoveredOnTruncation|TestValidateCompletion_PartialFindingsRecoveredOnTruncation" -v`
Expected: PASS.

- [ ] **Step 12: Update existing truncation tests to assert empty-bytes fallback**

The existing tests at `handlers_test.go:340`, `:359`, `:387` use `err: providers.ErrResponseTruncated` with no `resp` populated, so `resp.RawJSON` will be empty and `recoverPartialFindings` will return `(_, false)`. The existing assertions on `verdict.SeverityMajor` and `Findings.Len == 1` should continue to pass — but verify by running them.

Run: `go test -race ./internal/mcpsrv/ -run "TestValidateTaskSpec_TruncatedResponseSurfacesWarn|TestCheckProgress_TruncatedResponseSurfacesWarn|TestValidateCompletion_TruncatedResponseSurfacesWarn" -v`
Expected: PASS — no change needed because empty `resp.RawJSON` triggers the legacy fallback.

- [ ] **Step 13: Update the plan-level helpers to take rawJSON parameters**

The plan path is a little different because `reviewPlanSingle` and `reviewPlanChunked` (handlers.go:499 and :590) currently don't have a uniform return-on-truncation shape. Each makes multiple HTTP calls and consumes the responses internally. For this release, change just `reviewPlanSingle` to surface partial bytes:

```go
// reviewPlanSingle runs one reviewer call for the entire plan — the
// behavior used today for plans whose task count is at or below
// h.deps.Cfg.PlanTasksPerChunk. Renders the prompt internally.
// On ErrResponseTruncated, the returned []byte carries the partial
// response bytes so the caller can attempt partial-findings recovery.
func (h *handlers) reviewPlanSingle(ctx context.Context, model config.ModelRef, planText string) (verdict.PlanResult, string, int64, []byte, error) {
	rendered, err := prompts.RenderPlan(prompts.PlanInput{PlanText: planText})
	if err != nil {
		return verdict.PlanResult{}, "", 0, nil, fmt.Errorf("render plan prompt: %w", err)
	}
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PlanResult{}, "", 0, nil, err
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
		if errors.Is(err, providers.ErrResponseTruncated) {
			return verdict.PlanResult{}, "", 0, resp.RawJSON, err
		}
		return verdict.PlanResult{}, "", 0, nil, err
	}
	r, err := verdict.ParsePlan(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder.
		req.User = rendered.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.PlanResult{}, "", 0, resp.RawJSON, err
			}
			return verdict.PlanResult{}, "", 0, nil, err
		}
		r, err = verdict.ParsePlan(resp.RawJSON)
		if err != nil {
			return verdict.PlanResult{}, "", 0, nil, fmt.Errorf("plan provider response failed schema after retry: %w", err)
		}
	}
	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil, nil
}
```

Apply the same `(verdict.PlanResult, string, int64, []byte, error)` signature change to `reviewPlanChunked` (line 590). The chunked path emits `ErrResponseTruncated` at multiple points; on first truncation, return the partial bytes from THAT call. The result type at that point is `TasksOnly` from a per-chunk call, not a full `PlanResult` — but the tolerant parser handles either shape via `ParsePlanResultPartial` which accepts both.

Note: chunked-path recovery is best-effort. If truncation happens during Pass 1 (plan_findings_only), the partial bytes can yield a plan_findings list; if during a per-chunk Pass 2..K+1, the partial bytes can yield a partial tasks[] list for that chunk only. Document this limitation in the function comment but do not exhaustively recover across multiple calls — that's a follow-up.

- [ ] **Step 14: Update ValidatePlan to use partial recovery**

Replace the truncation branch in `ValidatePlan` (handlers.go:487-491):

```go
	var pr verdict.PlanResult
	var modelUsed string
	var ms int64
	var partialRaw []byte
	if len(tasks) <= h.deps.Cfg.PlanTasksPerChunk {
		pr, modelUsed, ms, partialRaw, err = h.reviewPlanSingle(ctx, model, args.PlanText)
	} else {
		pr, modelUsed, ms, partialRaw, err = h.reviewPlanChunked(ctx, model, args.PlanText, tasks, h.deps.Cfg.PlanTasksPerChunk)
	}
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			if recovered, ok := recoverPartialPlanFindings(model, partialRaw); ok {
				return planEnvelopeResult(recovered, model.String(), 0)
			}
			return planEnvelopeResult(truncatedPlanResult(), model.String(), 0)
		}
		return nil, verdict.PlanResult{}, err
	}
	return planEnvelopeResult(pr, modelUsed, ms)
```

- [ ] **Step 15: Add `recoverPartialPlanFindings` helper**

Add this helper near `truncatedPlanResult` in `handlers.go`:

```go
// recoverPartialPlanFindings attempts to extract complete plan findings
// and tasks from a truncated reviewer response. Returns (planResult, true)
// when at least one finding was recovered anywhere in the structure;
// (zero, false) otherwise.
func recoverPartialPlanFindings(model config.ModelRef, rawJSON []byte) (verdict.PlanResult, bool) {
	if len(rawJSON) == 0 {
		return verdict.PlanResult{}, false
	}
	pr, ok := verdict.ParsePlanResultPartial(rawJSON)
	if !ok {
		return verdict.PlanResult{}, false
	}
	count := len(pr.PlanFindings)
	for _, t := range pr.Tasks {
		count += len(t.Findings)
	}
	pr.PlanFindings = append(pr.PlanFindings, verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "reviewer_response",
		Evidence:   fmt.Sprintf("reviewer output truncated at the max_tokens cap; %d complete findings recovered across plan and tasks", count),
		Suggestion: "Raise ANTI_TANGENT_PLAN_MAX_TOKENS or pass max_tokens_override on the next call to capture more.",
	})
	if pr.NextAction == "" {
		pr.NextAction = "Address recovered findings; reviewer output was truncated, so the list may be incomplete."
	}
	// Ensure Tasks is non-nil so JSON marshaling produces an empty array
	// rather than null.
	if pr.Tasks == nil {
		pr.Tasks = []verdict.PlanTaskResult{}
	}
	return pr, true
}
```

- [ ] **Step 16: Add the plan-level partial-recovery test**

Add to `internal/mcpsrv/handlers_plan_test.go`:

```go
func TestValidatePlan_PartialFindingsRecoveredOnTruncation(t *testing.T) {
	// Two complete tasks; truncation hits in the third.
	rawJSON := []byte(`{"plan_verdict":"warn","plan_findings":[` +
		`{"severity":"major","category":"other","criterion":"pf1","evidence":"e","suggestion":"s"}` +
		`],"tasks":[` +
		`{"task_index":0,"task_title":"T0","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":1,"task_title":"T1","verdict":"warn","findings":[{"severity":"minor","category":"other","criterion":"tf1","evidence":"e","suggestion":"s"}],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2,"task_title":"T2","verdict":"warn","find`)

	rv := &fakeReviewer{
		name: "openai",
		resp: providers.Response{RawJSON: rawJSON, Model: "gpt-5"},
		err:  providers.ErrResponseTruncated,
	}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: First\n\nbody.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.True(t, pr.Partial)
	require.Len(t, pr.Tasks, 2)
	assert.Equal(t, "T0", pr.Tasks[0].TaskTitle)
	assert.Equal(t, "T1", pr.Tasks[1].TaskTitle)
	// plan_findings has the original major finding plus the minor truncation marker.
	require.Len(t, pr.PlanFindings, 2)
	assert.Equal(t, "pf1", pr.PlanFindings[0].Criterion)
	assert.Equal(t, verdict.SeverityMinor, pr.PlanFindings[1].Severity)
	assert.Contains(t, pr.PlanFindings[1].Suggestion, "max_tokens_override")
}
```

- [ ] **Step 17: Run the full mcpsrv test suite**

Run: `go test -race ./internal/mcpsrv/...`
Expected: PASS — all new partial-recovery tests pass; existing truncation tests still pass (empty-bytes fallback path); other tests unchanged.

- [ ] **Step 18: Run the full project test suite to confirm cross-package compatibility**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 19: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go internal/mcpsrv/handlers_plan_test.go
git commit -m "feat(mcpsrv): partial-findings recovery in all four handlers

Truncated reviewer responses now route through the tolerant parser
added in the prior commit. Complete findings produced before the cap
hit are surfaced in the envelope alongside a downgraded (minor)
truncation marker. Closes the actual bug from #10 — previously, large
plans could yield zero usable feedback when the reviewer hit
max_tokens mid-response.

Refs #10."
```

---

### Task 4: `max_tokens_override` arg with ceiling clamp

**Goal:** All four tools accept an optional `max_tokens_override` int argument that overrides the configured `PerTaskMaxTokens` / `PlanMaxTokens` for that single call. Values are clamped to a new `MaxTokensCeiling` config knob (default 16384). Over-ceiling values emit a single `minor` clamp finding so the behavior is visible.

**Acceptance criteria:**
- `MaxTokensCeiling` is added to `Config` with default 16384, sourced from `ANTI_TANGENT_MAX_TOKENS_CEILING` (must be positive when set).
- `MaxTokensOverride int` added (with `json:"max_tokens_override,omitempty"`) to all four `*Args` structs.
- Negative `MaxTokensOverride` is rejected at the handler boundary with `errors.New("max_tokens_override must be ≥ 0")`.
- Zero (or unset) `MaxTokensOverride` uses the configured default — no clamp finding.
- In-range override is used directly — no clamp finding.
- Over-ceiling override uses the ceiling AND emits a single `minor` `clamp` finding in the envelope's findings list.
- One unit test per tool (4 tools × 4 cases = 16 cases via table-driven test) covers: unset, zero, in-range, over-ceiling.
- `go test -race ./internal/...` is green.

**Non-goals:**
- No changes to the partial-recovery code from Task 3 — clamping happens before the provider call.
- No new ceiling per-tool — single global ceiling applies to all four tools.

**Context:** Currently `h.deps.Cfg.PerTaskMaxTokens` (for non-plan tools) and `h.deps.Cfg.PlanMaxTokens` (for `validate_plan`) are read directly into `providers.Request.MaxTokens` (e.g. handlers.go:121, 513). We need to swap that with `effectiveMaxTokens(override, default, ceiling)` and route the resulting clamp finding (if any) through to the envelope.

**Files:**
- Modify: `internal/config/config.go` — add `MaxTokensCeiling` field, parse `ANTI_TANGENT_MAX_TOKENS_CEILING` env var.
- Modify: `internal/config/config_test.go` — add a test for the new env var (default, valid, invalid, non-positive).
- Modify: `internal/mcpsrv/handlers.go` — add `MaxTokensOverride` to all `*Args` structs; add `effectiveMaxTokens` helper; thread through `review` and `reviewPlanSingle` / `reviewPlanChunked` calls.
- Modify: `internal/mcpsrv/handlers_test.go` — add table-driven test for override behavior.

- [ ] **Step 1: Add `MaxTokensCeiling` to config**

In `internal/config/config.go`, add the field to `Config` (line 13-28):

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
	LogLevel          slog.Level
	PerTaskMaxTokens  int
	PlanMaxTokens     int
	PlanTasksPerChunk int
	MaxTokensCeiling  int
}
```

And add `MaxTokensCeiling: 16384,` to the `cfg := Config{...}` initializer (line 48), then add an env-var block near the other max-tokens parsing (after line 142):

```go
	if v := env("ANTI_TANGENT_MAX_TOKENS_CEILING"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_TOKENS_CEILING: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_TOKENS_CEILING: must be positive, got %d", n)
		}
		cfg.MaxTokensCeiling = n
	}
```

- [ ] **Step 2: Write a config test for the new env var**

Add to `internal/config/config_test.go` (mirror the existing `PerTaskMaxTokens` tests):

```go
func TestLoad_MaxTokensCeiling(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"default when unset", "", 16384, false},
		{"valid override", "32768", 32768, false},
		{"invalid string rejected", "abc", 0, true},
		{"zero rejected", "0", 0, true},
		{"negative rejected", "-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(func(k string) string {
				switch k {
				case "ANTHROPIC_API_KEY":
					return "k"
				case "ANTI_TANGENT_MAX_TOKENS_CEILING":
					return tt.value
				}
				return ""
			})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.MaxTokensCeiling)
		})
	}
}
```

- [ ] **Step 3: Run the config tests**

Run: `go test -race ./internal/config/...`
Expected: PASS.

- [ ] **Step 4: Add `MaxTokensOverride` to all four `*Args` structs**

In `internal/mcpsrv/handlers.go`, add this field to:

- `ValidateTaskSpecArgs` (line 35)
- `CheckProgressArgs` (line 206)
- `ValidateCompletionArgs` (line 373)
- `ValidatePlanArgs` (line 383)

Each gets:

```go
	MaxTokensOverride int `json:"max_tokens_override,omitempty"`
```

- [ ] **Step 5: Add the `effectiveMaxTokens` helper**

Add near `resolveModel` (around line 149) in `handlers.go`:

```go
// effectiveMaxTokens returns the max-tokens value to send to the provider,
// the optional clamp finding (zero value if no clamp occurred), and an
// error if the override is invalid.
//
//	override == 0  → use defaultMaxTokens; no clamp finding
//	override < 0   → return error (rejected at handler boundary)
//	override <= ceiling → use override; no clamp finding
//	override > ceiling  → use ceiling; emit minor clamp finding
func effectiveMaxTokens(override, defaultMaxTokens, ceiling int) (int, verdict.Finding, error) {
	if override < 0 {
		return 0, verdict.Finding{}, errors.New("max_tokens_override must be ≥ 0")
	}
	if override == 0 {
		return defaultMaxTokens, verdict.Finding{}, nil
	}
	if override <= ceiling {
		return override, verdict.Finding{}, nil
	}
	finding := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "max_tokens_override",
		Evidence:   fmt.Sprintf("max_tokens_override (%d) exceeds ceiling (%d); used %d", override, ceiling, ceiling),
		Suggestion: "Raise ANTI_TANGENT_MAX_TOKENS_CEILING if you need a larger budget.",
	}
	return ceiling, finding, nil
}
```

- [ ] **Step 6: Thread `MaxTokensOverride` through `ValidateTaskSpec`**

In `ValidateTaskSpec` (line 57), after the existing `args.TaskTitle == ""` validation, add:

```go
	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PerTaskMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, Envelope{}, err
	}
```

The `review` helper needs to accept `maxTokens` as a parameter rather than reading `h.deps.Cfg.PerTaskMaxTokens` directly. Update `review`'s signature to accept it:

```go
func (h *handlers) review(ctx context.Context, model config.ModelRef, p prompts.Output, maxTokens int) (verdict.Result, string, int64, []byte, error) {
```

…and inside, replace `MaxTokens: h.deps.Cfg.PerTaskMaxTokens,` (line 121) with `MaxTokens: maxTokens,`.

Update the `ValidateTaskSpec` call:

```go
	result, modelUsed, ms, partialRaw, err := h.review(ctx, model, rendered, maxTokens)
```

After the existing `env := Envelope{...}` block in `ValidateTaskSpec` (around line 97-104 in the post-Task-3 file) and before the `env = h.withSessionTTL(env, sess)` line, prepend the clamp finding to the envelope's findings list if non-empty:

```go
	if clamp.Severity != "" {
		env.Findings = append([]verdict.Finding{clamp}, env.Findings...)
	}
```

For partial-recovery flows (the `recoverPartialFindings` path), do NOT prepend the clamp here — it would conflict with the truncation marker. The clamp finding only fires on successful (non-truncated) calls in this release.

- [ ] **Step 7: Apply the same threading to CheckProgress, ValidateCompletion, ValidatePlan**

For each of the other three handlers, mirror the pattern from Step 6:

- Validate `args.MaxTokensOverride ≥ 0`.
- Call `effectiveMaxTokens(args.MaxTokensOverride, default, h.deps.Cfg.MaxTokensCeiling)` where `default` is `PerTaskMaxTokens` for `CheckProgress` / `ValidateCompletion` and `PlanMaxTokens` for `ValidatePlan`.
- Pass the returned `maxTokens` into `review` / `reviewPlanSingle` / `reviewPlanChunked`.
- Prepend the clamp finding to the envelope's findings (or `PlanFindings`) if non-empty.

The `reviewPlanSingle` and `reviewPlanChunked` functions need their `MaxTokens: h.deps.Cfg.PlanMaxTokens` reads (handlers.go:513, 620, 700) replaced with the threaded parameter. Add `maxTokens int` to both signatures.

- [ ] **Step 8: Write the max_tokens_override table-driven test**

Add to `internal/mcpsrv/handlers_test.go`:

```go
// reviewerCapture captures the last request a fakeReviewer received so
// tests can assert MaxTokens was set correctly.
type reviewerCapture struct {
	fakeReviewer
	LastRequest providers.Request
}

func (c *reviewerCapture) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	c.LastRequest = req
	c.fakeReviewer.Calls++
	if c.fakeReviewer.err != nil {
		return providers.Response{}, c.fakeReviewer.err
	}
	return c.fakeReviewer.resp, nil
}

func TestMaxTokensOverride_AllTools(t *testing.T) {
	tests := []struct {
		name         string
		override     int
		ceiling      int
		defaultMax   int
		wantSent     int
		wantClamp    bool
		wantErrEmpty bool
	}{
		{"unset uses default", 0, 16384, 4096, 4096, false, true},
		{"in-range uses override", 8000, 16384, 4096, 8000, false, true},
		{"over-ceiling clamps", 32000, 16384, 4096, 16384, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}}
			d := newDeps(t, &cap.fakeReviewer)
			d.Cfg.PerTaskMaxTokens = tt.defaultMax
			d.Cfg.MaxTokensCeiling = tt.ceiling
			d.Reviews = providers.Registry{"anthropic": cap}
			h := &handlers{deps: d}

			_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
				TaskTitle: "T", Goal: "G", MaxTokensOverride: tt.override,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantSent, cap.LastRequest.MaxTokens, "MaxTokens sent to provider")
			if tt.wantClamp {
				require.NotEmpty(t, env.Findings, "should have clamp finding when over-ceiling")
				assert.Equal(t, "max_tokens_override", env.Findings[0].Criterion)
				assert.Equal(t, verdict.SeverityMinor, env.Findings[0].Severity)
			}
		})
	}
}

func TestMaxTokensOverride_NegativeRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", MaxTokensOverride: -1,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_tokens_override must be ≥ 0")
}
```

- [ ] **Step 9: Run the override tests**

Run: `go test -race ./internal/mcpsrv/ -run "TestMaxTokensOverride" -v`
Expected: PASS for all six cases.

- [ ] **Step 10: Run the full mcpsrv suite**

Run: `go test -race ./internal/mcpsrv/...`
Expected: PASS. Existing `TestValidateTaskSpec_UsesConfiguredPerTaskMaxTokens` (line 511) and `TestValidatePlan_UsesConfiguredPlanMaxTokens` (line 534) should continue to pass since they don't set `MaxTokensOverride` and the default-path through `effectiveMaxTokens` returns the unmodified config value.

- [ ] **Step 11: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "feat(mcpsrv): max_tokens_override arg with ceiling clamp

All four tools now accept an optional max_tokens_override int that
replaces the configured default for that single call. Clamped to a new
MaxTokensCeiling config knob (default 16384, env
ANTI_TANGENT_MAX_TOKENS_CEILING). Over-ceiling values use the ceiling
and emit a minor clamp finding; negative values are rejected at the
handler boundary.

Refs #10."
```

---

### Task 5: `mode: quick | thorough` on validate_plan

**Goal:** `ValidatePlanArgs` accepts an optional `mode` string field. Empty / `"thorough"` preserves current behaviour. `"quick"` adds an instruction in the three plan prompt templates asking the reviewer to surface only the most-severe findings (at most 3 per scope) and omit nits. Invalid values are rejected at the handler boundary.

**Acceptance criteria:**
- `Mode string \`json:"mode,omitempty"\`` added to `ValidatePlanArgs`.
- Empty string and `"thorough"` produce the existing prompt (no quick-mode block).
- `"quick"` adds the instruction block to all three plan templates.
- Other values produce an error: `errors.New("mode must be \"quick\" or \"thorough\"")`.
- `Mode string` added to `prompts.PlanInput` and `prompts.PlanChunkInput`.
- All three plan templates contain a `{{ if eq .Mode "quick" }}…{{ end }}` block in the `## What to evaluate` section.
- Three new golden files (`plan_basic_quick.golden`, `plan_findings_only_quick.golden`, `plan_tasks_chunk_quick.golden`) cover the quick-mode rendered output.
- Four new anchor-assertion tests: one per template asserting the quick-mode anchor is present when `Mode == "quick"`; one negative test asserting the anchor is absent when `Mode == ""` or `Mode == "thorough"`.
- `go test -race ./internal/prompts/... ./internal/mcpsrv/...` is green.

**Non-goals:**
- No server-side post-processing cap. Reviewer-side instruction only.
- No `mode` arg on `validate_task_spec` / `check_progress` / `validate_completion` — quick-mode doesn't apply to single-task review.
- No prompt ride-alongs in this task — that's Task 6.

**Context:** The three plan templates already share a structure (`## Reviewer ground rules`, `## Plan under review`, `## What to evaluate`, `## Output`). The quick-mode block belongs in `## What to evaluate` because it shapes how the reviewer applies severity.

**Files:**
- Modify: `internal/prompts/prompts.go` — add `Mode` field to `PlanInput` (line 48) and `PlanChunkInput` (line 89).
- Modify: `internal/prompts/templates/plan.tmpl` — add quick-mode block.
- Modify: `internal/prompts/templates/plan_findings_only.tmpl` — same.
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl` — same.
- Create: `internal/prompts/testdata/plan_basic_quick.golden`
- Create: `internal/prompts/testdata/plan_findings_only_quick.golden`
- Create: `internal/prompts/testdata/plan_tasks_chunk_quick.golden`
- Modify: `internal/prompts/prompts_test.go` — add four anchor-assertion tests.
- Modify: `internal/mcpsrv/handlers.go` — thread `Mode` into `PlanInput` / `PlanChunkInput` from `ValidatePlanArgs.Mode`; validate at handler boundary.
- Modify: `internal/mcpsrv/handlers_test.go` — add a test for invalid mode value; add a test that `Mode == "quick"` reaches the prompt.

- [ ] **Step 1: Add `Mode` to `PlanInput` and `PlanChunkInput`**

In `internal/prompts/prompts.go`:

```go
type PlanInput struct {
	PlanText string
	Mode     string
}
```

And:

```go
type PlanChunkInput struct {
	PlanText   string
	ChunkTasks []planparser.RawTask
	Mode       string
}
```

- [ ] **Step 2: Add the quick-mode block to `plan.tmpl`**

Open `internal/prompts/templates/plan.tmpl`. After the closing `## Output` section line "Severity: critical = unimplementable as written; major = implementer would still misimplement; minor = nit." (currently line 41) — wait, actually the quick-mode block belongs in `## What to evaluate`, after that severity line but before `## Output`. Insert (line 42 in the current file, immediately before `## Output`):

```
{{ if eq .Mode "quick" -}}
**Quick mode.** Surface only the most-severe findings — at most 3 per scope (3 plan-level findings, and at most 3 findings per task). Omit minor nits and stylistic suggestions. Prefer fewer high-quality findings over many low-value ones.

{{ end -}}
```

- [ ] **Step 3: Add the same block to `plan_findings_only.tmpl`**

In `internal/prompts/templates/plan_findings_only.tmpl`, insert immediately before `## Output` (currently line 40):

```
{{ if eq .Mode "quick" -}}
**Quick mode.** Surface only the most-severe findings — at most 3 plan-level findings. Omit minor nits and stylistic suggestions. Prefer fewer high-quality findings over many low-value ones.

{{ end -}}
```

(Note: plan_findings_only doesn't have per-task findings, so the cap mention drops the "per task" phrasing.)

- [ ] **Step 4: Add the same block to `plan_tasks_chunk.tmpl`**

In `internal/prompts/templates/plan_tasks_chunk.tmpl`, insert immediately before `## Output` (after the severity line):

```
{{ if eq .Mode "quick" -}}
**Quick mode.** For each task in the list above, surface only the most-severe findings — at most 3 per task. Omit minor nits and stylistic suggestions. Prefer fewer high-quality findings over many low-value ones.

{{ end -}}
```

- [ ] **Step 5: Write the anchor-assertion tests**

Add to `internal/prompts/prompts_test.go`:

```go
const (
	anchorQuickModeBasic         = "**Quick mode.** Surface only the most-severe findings — at most 3 per scope"
	anchorQuickModeFindingsOnly  = "**Quick mode.** Surface only the most-severe findings — at most 3 plan-level findings"
	anchorQuickModeTasksChunk    = "**Quick mode.** For each task in the list above, surface only the most-severe findings — at most 3 per task"
)

func TestRenderPlan_QuickMode_IncludesInstruction(t *testing.T) {
	out, err := RenderPlan(PlanInput{
		PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n",
		Mode:     "quick",
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorQuickModeBasic)
}

func TestRenderPlanFindingsOnly_QuickMode_IncludesInstruction(t *testing.T) {
	out, err := RenderPlanFindingsOnly(PlanInput{
		PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n",
		Mode:     "quick",
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorQuickModeFindingsOnly)
}

func TestRenderPlanTasksChunk_QuickMode_IncludesInstruction(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n",
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
		Mode:       "quick",
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorQuickModeTasksChunk)
}

func TestRenderPlan_DefaultMode_OmitsQuickInstruction(t *testing.T) {
	for _, mode := range []string{"", "thorough"} {
		out, err := RenderPlan(PlanInput{
			PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n",
			Mode:     mode,
		})
		require.NoError(t, err)
		assert.NotContains(t, out.User, "**Quick mode.**", "default/thorough mode should not include quick-mode block (mode=%q)", mode)
	}
}
```

- [ ] **Step 6: Run the anchor tests to verify they pass**

Run: `go test -race ./internal/prompts/ -run "TestRenderPlan_QuickMode|TestRenderPlanFindingsOnly_QuickMode|TestRenderPlanTasksChunk_QuickMode|TestRenderPlan_DefaultMode" -v`
Expected: PASS.

- [ ] **Step 7: Generate the `*_quick.golden` files**

First, add three golden tests to `internal/prompts/prompts_test.go`:

```go
func TestRenderPlan_QuickMode_Golden(t *testing.T) {
	out, err := RenderPlan(PlanInput{
		PlanText: `# Sample Plan

### Task 1: Bootstrap

Files:
- main.go

Step 1: write main.

### Task 2: Add tests

**Goal:** Cover the bootstrap with a smoke test.

**Acceptance criteria:**
- main_test.go exists
- go test ./... passes
`,
		Mode: "quick",
	})
	require.NoError(t, err)
	golden(t, "plan_basic_quick", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlanTasksChunk_QuickMode_Golden(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText: "## Phase 1\n\n### Task 1: do thing\n\n### Task 2: do other thing\n",
		ChunkTasks: []planparser.RawTask{
			{Title: "Task 1: do thing", Body: "### Task 1: do thing\n"},
			{Title: "Task 2: do other thing", Body: "### Task 2: do other thing\n"},
		},
		Mode: "quick",
	})
	require.NoError(t, err)
	golden(t, "plan_tasks_chunk_quick", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlanFindingsOnly_QuickMode_Golden(t *testing.T) {
	out, err := RenderPlanFindingsOnly(PlanInput{
		PlanText: "## Phase 1\n\n### Task 1: do thing\n\n**Goal:** thing\n\n**Acceptance criteria:**\n- thing happens\n",
		Mode:     "quick",
	})
	require.NoError(t, err)
	golden(t, "plan_findings_only_quick", out.System+"\n---USER---\n"+out.User)
}
```

Then run with `-update` to materialise the golden files:

Run: `go test ./internal/prompts/ -update -run "TestRenderPlan_QuickMode_Golden|TestRenderPlanTasksChunk_QuickMode_Golden|TestRenderPlanFindingsOnly_QuickMode_Golden" -v`
Expected: tests PASS and three new `*_quick.golden` files appear in `internal/prompts/testdata/`.

- [ ] **Step 8: Inspect each new golden file**

Run: `cat internal/prompts/testdata/plan_basic_quick.golden | head -50`
Verify the `**Quick mode.**` block is present and the existing structure is intact. Repeat for the other two goldens.

- [ ] **Step 9: Run the golden tests without `-update` to confirm reproducibility**

Run: `go test -race ./internal/prompts/ -run "QuickMode_Golden" -v`
Expected: PASS — goldens match.

- [ ] **Step 10: Wire `Mode` into the handler call chain**

In `internal/mcpsrv/handlers.go`, update `reviewPlanSingle` (around line 499) to accept `mode string` and pass it into `PlanInput`:

```go
func (h *handlers) reviewPlanSingle(ctx context.Context, model config.ModelRef, planText string, maxTokens int, mode string) (verdict.PlanResult, string, int64, []byte, error) {
	rendered, err := prompts.RenderPlan(prompts.PlanInput{PlanText: planText, Mode: mode})
	// … rest unchanged …
}
```

And `reviewPlanChunked` (line 590) — thread `mode` into both `RenderPlanFindingsOnly` and the per-chunk `RenderPlanTasksChunk` calls:

```go
func (h *handlers) reviewPlanChunked(
	ctx context.Context,
	model config.ModelRef,
	planText string,
	tasks []planparser.RawTask,
	chunkSize int,
	maxTokens int,
	mode string,
) (verdict.PlanResult, string, int64, []byte, error) {
	// …
	rendered, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{PlanText: planText, Mode: mode})
	// …
	// Per-chunk call:
	rendered, err := prompts.RenderPlanTasksChunk(prompts.PlanChunkInput{
		PlanText:   planText,
		ChunkTasks: chunkTasks,
		Mode:       mode,
	})
	// …
}
```

In `ValidatePlan` (line 462), validate `args.Mode` and pass it through:

```go
	if args.Mode != "" && args.Mode != "quick" && args.Mode != "thorough" {
		return nil, verdict.PlanResult{}, errors.New(`mode must be "quick" or "thorough"`)
	}
	// …
	if len(tasks) <= h.deps.Cfg.PlanTasksPerChunk {
		pr, modelUsed, ms, partialRaw, err = h.reviewPlanSingle(ctx, model, args.PlanText, maxTokens, args.Mode)
	} else {
		pr, modelUsed, ms, partialRaw, err = h.reviewPlanChunked(ctx, model, args.PlanText, tasks, h.deps.Cfg.PlanTasksPerChunk, maxTokens, args.Mode)
	}
```

- [ ] **Step 11: Add the invalid-mode handler test**

Add to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidatePlan_InvalidModeRejected(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: "# P\n\n### Task 1: X\n", Mode: "fast",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `mode must be "quick" or "thorough"`)
}

func TestValidatePlan_ModeQuickPlumbedToPrompt(t *testing.T) {
	cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "openai", resp: planPassResp()}}
	d := newDeps(t, &cap.fakeReviewer)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": cap}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: "# P\n\n### Task 1: X\n", Mode: "quick",
	})
	require.NoError(t, err)
	assert.Contains(t, cap.LastRequest.User, "**Quick mode.**", "quick mode should plumb through to the rendered prompt")
}
```

- [ ] **Step 12: Run the mode tests**

Run: `go test -race ./internal/mcpsrv/ -run "TestValidatePlan_InvalidModeRejected|TestValidatePlan_ModeQuickPlumbedToPrompt" -v`
Expected: PASS.

- [ ] **Step 13: Run the full test suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 14: Commit**

```bash
git add internal/prompts/prompts.go internal/prompts/templates/plan.tmpl internal/prompts/templates/plan_findings_only.tmpl internal/prompts/templates/plan_tasks_chunk.tmpl internal/prompts/testdata/plan_basic_quick.golden internal/prompts/testdata/plan_findings_only_quick.golden internal/prompts/testdata/plan_tasks_chunk_quick.golden internal/prompts/prompts_test.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "feat(validate_plan): mode quick|thorough arg

ValidatePlan accepts an optional mode arg. quick mode adds an
instruction asking the reviewer to surface only the most-severe
findings (at most 3 per scope) and omit nits. thorough (default)
preserves current behaviour. Invalid values are rejected at the
handler boundary.

Refs #10."
```

---

### Task 6: Prompt ride-alongs — hypothetical-marker and next_action specificity

**Goal:** Add a 4th paragraph to the `## Reviewer ground rules` block (about marking illustrative code-symbol examples) and a sentence in the `## Output` section (about `next_action` specificity) in all three plan templates. Regenerate the three existing plan goldens AND the three quick-mode goldens added in Task 5 to reflect the new content.

**Acceptance criteria:**
- `## Reviewer ground rules` in `plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl` has four paragraphs (3 existing from 0.2.1 + 1 new hypothetical-marker).
- `## Output` section in the same three templates contains the `next_action` specificity sentence.
- Two new anchor-assertion tests assert both anchors are present in all three templates.
- The 0.2.1 anchor-assertion tests (`TestRenderPlan_IncludesReviewerGroundRules` etc.) continue to pass — the 4th paragraph is additive.
- All six plan goldens (3 default + 3 quick) are regenerated and committed.
- `go test -race ./internal/prompts/...` is green.

**Non-goals:**
- No changes to per-task templates (`pre.tmpl`, `mid.tmpl`, `post.tmpl`).
- No prompt edits beyond the two specified ride-alongs.

**Context:** The 0.2.1 release added the `## Reviewer ground rules` block to the same three templates. This task extends that block and tightens the `## Output` directive. Field-report items #3 (hypothetical-marker) and #5 (next_action specificity) from issue #10.

**Files:**
- Modify: `internal/prompts/templates/plan.tmpl` — add 4th ground-rules paragraph + next_action sentence.
- Modify: `internal/prompts/templates/plan_findings_only.tmpl` — same.
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl` — same.
- Modify: `internal/prompts/testdata/plan_basic.golden` — regen.
- Modify: `internal/prompts/testdata/plan_findings_only.golden` — regen.
- Modify: `internal/prompts/testdata/plan_tasks_chunk.golden` — regen.
- Modify: `internal/prompts/testdata/plan_basic_quick.golden` — regen.
- Modify: `internal/prompts/testdata/plan_findings_only_quick.golden` — regen.
- Modify: `internal/prompts/testdata/plan_tasks_chunk_quick.golden` — regen.
- Modify: `internal/prompts/prompts_test.go` — add two new anchor-assertion tests.

- [ ] **Step 1: Add the 4th ground-rules paragraph to `plan.tmpl`**

In `internal/prompts/templates/plan.tmpl`, after the existing 3rd paragraph (line 7, ending `…do not emit the finding.`), add a blank line then:

```
When you cite a code symbol's signature, an example sibling key, or an adjacent attribute value as an *illustration*, prefix it with `e.g. illustrative —`. The reader does not know whether your illustration matches the codebase, and false confidence is worse than no example.
```

- [ ] **Step 2: Add the next_action sentence to `plan.tmpl`**

In the same file's `## Output` section (currently line 43-46), change:

```
## Output

Respond with a JSON object matching the provided schema. Do not include
prose outside the JSON.
```

…to:

```
## Output

Respond with a JSON object matching the provided schema. Do not include
prose outside the JSON.

The `next_action` field must name the single highest-leverage finding the author should address next — not generic advice like "address the warnings." If no findings warrant immediate attention, say so explicitly: "no blocking findings; proceed to dispatch."
```

- [ ] **Step 3: Apply the same two edits to `plan_findings_only.tmpl`**

After the 3rd ground-rules paragraph (line 7), add the hypothetical-marker paragraph. In `## Output`, append the next_action sentence after the existing "Do not include prose outside the JSON." sentence.

- [ ] **Step 4: Apply the same two edits to `plan_tasks_chunk.tmpl`**

Same pattern.

- [ ] **Step 5: Write the anchor-assertion tests**

Add to `internal/prompts/prompts_test.go`:

```go
const (
	anchorHypotheticalMarker = "e.g. illustrative —"
	anchorNextActionNudge    = "single highest-leverage finding"
)

func TestPlanTemplates_IncludeHypotheticalMarker(t *testing.T) {
	planText := "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"

	out, err := RenderPlan(PlanInput{PlanText: planText})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorHypotheticalMarker, "plan.tmpl should include hypothetical-marker rule")

	out, err = RenderPlanFindingsOnly(PlanInput{PlanText: planText})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorHypotheticalMarker, "plan_findings_only.tmpl should include hypothetical-marker rule")

	out, err = RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   planText,
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorHypotheticalMarker, "plan_tasks_chunk.tmpl should include hypothetical-marker rule")
}

func TestPlanTemplates_IncludeNextActionNudge(t *testing.T) {
	planText := "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"

	out, err := RenderPlan(PlanInput{PlanText: planText})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorNextActionNudge, "plan.tmpl should include next_action specificity nudge")

	out, err = RenderPlanFindingsOnly(PlanInput{PlanText: planText})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorNextActionNudge, "plan_findings_only.tmpl should include next_action specificity nudge")

	out, err = RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   planText,
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorNextActionNudge, "plan_tasks_chunk.tmpl should include next_action specificity nudge")
}
```

- [ ] **Step 6: Run the anchor tests to verify they pass**

Run: `go test -race ./internal/prompts/ -run "TestPlanTemplates_IncludeHypotheticalMarker|TestPlanTemplates_IncludeNextActionNudge" -v`
Expected: PASS.

- [ ] **Step 7: Regenerate all six plan goldens**

Run: `go test ./internal/prompts/ -update`
Expected: PASS — six golden files in `internal/prompts/testdata/` are rewritten.

- [ ] **Step 8: Inspect each regenerated golden**

Run: `git diff internal/prompts/testdata/plan_basic.golden | head -60`
Verify the diff shows ONLY the additions (4th paragraph + next_action sentence). Repeat for each of the six goldens. Make sure no unintended changes (e.g. accidental template variable renamed).

- [ ] **Step 9: Run the full prompts test suite**

Run: `go test -race ./internal/prompts/...`
Expected: PASS — all anchor tests (0.2.1 + this release) green; all six golden tests green.

- [ ] **Step 10: Run the full project test suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/prompts/templates/plan.tmpl internal/prompts/templates/plan_findings_only.tmpl internal/prompts/templates/plan_tasks_chunk.tmpl internal/prompts/testdata/plan_basic.golden internal/prompts/testdata/plan_findings_only.golden internal/prompts/testdata/plan_tasks_chunk.golden internal/prompts/testdata/plan_basic_quick.golden internal/prompts/testdata/plan_findings_only_quick.golden internal/prompts/testdata/plan_tasks_chunk_quick.golden internal/prompts/prompts_test.go
git commit -m "chore(prompts): hypothetical-marker + next_action specificity

Adds a 4th paragraph to the ## Reviewer ground rules block in all
three validate_plan templates: when citing a code symbol's signature
or adjacent attribute as an illustration, prefix with 'e.g.
illustrative —'. Adds a next_action specificity sentence to the
## Output section telling the reviewer to name the single
highest-leverage finding rather than generic advice. Goldens
regenerated.

Refs #10."
```

---

### Task 7: CHANGELOG, README, INTEGRATION, version bump

**Goal:** Document the 0.3.0 release in `CHANGELOG.md`, `README.md`, and `INTEGRATION.md`. CI enforces that the branch name's version matches a `## [X.Y.Z] - YYYY-MM-DD` entry in `CHANGELOG.md`, so the entry must land before PR open.

**Acceptance criteria:**
- `CHANGELOG.md` has a new `## [0.3.0] - 2026-05-12` block above the 0.2.1 entry, with `### Added`, `### Changed`, `### Fixed` subsections covering the new args, the `partial: true` envelope field, the truncation bug fix, and the prompt edits.
- `README.md` documents `max_tokens_override`, `mode: "quick" | "thorough"`, and `ANTI_TANGENT_MAX_TOKENS_CEILING`.
- `INTEGRATION.md` documents the `partial: true` envelope field shape, the new args, and the updated (minor severity) truncation finding wording.
- The merge commit subject (when this branch is merged into main) will carry `[minor]` to drive the release workflow's version bump (already handled by maintainer; not part of this task).
- `go test -race ./...` is green.

**Non-goals:**
- No `VERSION` file edit in this task — the release workflow handles that based on the merge-commit tag.
- No GitHub release notes — the workflow generates those from `CHANGELOG.md`.

**Context:** Project convention in `CLAUDE.md`: branch name matches a CHANGELOG entry; CI enforces this. The README and INTEGRATION updates make the new behavior discoverable to integrators.

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `README.md`
- Modify: `INTEGRATION.md`

- [ ] **Step 1: Add the 0.3.0 CHANGELOG entry**

In `CHANGELOG.md`, insert above the `## [0.2.1] - 2026-05-12` block:

```markdown
## [0.3.0] - 2026-05-12

### Added
- `max_tokens_override` optional arg on all four tools (`validate_task_spec`, `check_progress`, `validate_completion`, `validate_plan`) for per-call control over the reviewer's output-token budget. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values emit a `minor` clamp finding so the behaviour is visible. Negative values are rejected at the handler boundary.
- `mode: "quick" | "thorough"` optional arg on `validate_plan`. `quick` instructs the reviewer to surface at most 3 most-severe findings per scope (plan-level and each task) and omit stylistic nits; `thorough` (default) preserves prior behavior. Invalid values are rejected at the handler boundary.
- `partial: true` field on `Result` and `PlanResult` envelopes when the reviewer's response was truncated at the `max_tokens` cap but partial findings could be recovered. Marshaled with `omitempty` so the field is absent in the common (non-truncated) case.
- Hypothetical-marker guardrail (`e.g. illustrative —` prefix) added as a 4th paragraph in the `## Reviewer ground rules` block in `validate_plan` templates, complementing the 0.2.1 epistemic-boundary work.
- `next_action` specificity nudge in `validate_plan` templates: the field must name the single highest-leverage finding, not generic advice.
- `ANTI_TANGENT_MAX_TOKENS_CEILING` env var (default 16384) caps the per-call `max_tokens_override` value.

### Fixed
- Reviewer-output truncation no longer discards complete findings produced before the cap hit. All four tools now run truncated responses through a tolerant JSON parser and emit any recoverable findings alongside a downgraded (`minor`) truncation marker. Previously, ~9 KB of plan input could yield zero usable feedback when the reviewer's output cap was reached mid-response. Closes [#10](https://github.com/patiently/anti-tangent-mcp/issues/10).

### Changed
- The synthetic truncation finding emitted on `max_tokens` cap hits is now `severity: minor` (was `major`), with wording that references both the env-var and `max_tokens_override` mitigations.
```

- [ ] **Step 2: Update README.md**

The README's env-var dotenv block lives around line 35-45. Find the line:

```
ANTI_TANGENT_PLAN_TASKS_PER_CHUNK=8      # plans above this task count are reviewed via the chunked path; also the per-chunk size
```

Add immediately after it:

```
ANTI_TANGENT_MAX_TOKENS_CEILING=16384    # cap on per-call max_tokens_override; over-ceiling values are clamped and emit a minor clamp finding (v0.3.0+)
```

If the README has a per-tool args section that describes `model_override`, follow the same pattern there for the new args. Search the README (`grep -n model_override README.md`) to locate the right block; if none exists, add a brief paragraph above the `## Use with Claude Code` section:

```markdown
### Per-call tool args (v0.3.0+)

All four tools accept an optional `max_tokens_override` int — replaces the configured default (`PerTaskMaxTokens` or `PlanMaxTokens`) for this call only. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING`. Use when you know one specific call needs a larger reviewer budget without changing global config.

`validate_plan` additionally accepts an optional `mode` arg of `"quick"` or `"thorough"` (default `"thorough"`). `"quick"` instructs the reviewer to surface only the most-severe findings (at most 3 per scope) — useful for small ASAP plans where you don't want round-after-round of stylistic refinement.
```

- [ ] **Step 3: Update INTEGRATION.md**

In INTEGRATION.md's `### Output budgets and chunking for validate_plan (v0.1.4+)` section (around line 130), update the dotenv block to include the new env var:

```dotenv
ANTI_TANGENT_MAX_TOKENS_CEILING=16384    # default 16384; max value accepted for per-call max_tokens_override (v0.3.0+)
```

Then update the troubleshooting paragraph at line 472 (the one starting `**A hook returned a finding with`category: other`and`criterion: reviewer_response`.**`) to reflect the new behavior:

```markdown
**A hook returned a finding with `category: other` and `criterion: reviewer_response`.**
The reviewer's response was cut off at the output token budget. As of v0.3.0, the server runs truncated responses through a tolerant parser and surfaces any complete findings produced before the cap — look for `"partial": true` on the envelope and a `severity: minor` truncation marker. To get the full response on the next call, either raise `ANTI_TANGENT_PER_TASK_MAX_TOKENS` / `ANTI_TANGENT_PLAN_MAX_TOKENS` globally, or pass `max_tokens_override` (clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING`, default 16384) for that single call. Pre-0.3.0 servers would emit a single `severity: major` truncation finding and discard any partial output.
```

Add a new subsection just before `## FAQ` (or wherever per-tool arg documentation lives — search `grep -n 'model_override' INTEGRATION.md`) for the new args:

```markdown
### Per-call tool args (v0.3.0+)

**`max_tokens_override`** (all four tools): optional non-negative int. Replaces the configured `PerTaskMaxTokens` / `PlanMaxTokens` for this call. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values are clamped and a `minor` clamp finding is appended to the envelope. Negative values are rejected with `max_tokens_override must be ≥ 0`. Use when you know a particular call needs a larger reviewer budget without modifying global config — handy when paired with partial-findings recovery on truncated responses.

**`mode`** (`validate_plan` only): optional `"quick"` or `"thorough"` (default `"thorough"`). `"quick"` instructs the reviewer to surface only the most-severe findings — at most 3 per scope (plan-level + each task) — and omit stylistic nits. Useful for small ASAP plans where rounds 5+ surface only polish. Invalid values are rejected with `mode must be "quick" or "thorough"`.

### `partial: true` envelope field (v0.3.0+)

When the reviewer's output was truncated at its `max_tokens` cap but at least one complete finding could be recovered, the response envelope (`Result` for per-task tools, `PlanResult` for `validate_plan`) carries `"partial": true` and the synthetic truncation finding is `severity: minor` rather than `major`. The field is `omitempty` — absent in the common (non-truncated) case, so pre-0.3.0 callers continue to work. If partial recovery fails (no complete finding before the cap hit), the envelope falls back to the legacy single `severity: major` truncation finding with no `partial` field set.
```

- [ ] **Step 4: Verify CHANGELOG version matches branch name**

Run: `git branch --show-current`
Expected: `version/0.3.0`. The `## [0.3.0]` heading in CHANGELOG matches.

- [ ] **Step 5: Run the full test suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add CHANGELOG.md README.md INTEGRATION.md
git commit -m "docs: CHANGELOG / README / INTEGRATION for 0.3.0

Documents max_tokens_override, mode quick|thorough, the partial: true
envelope field, the truncation-bug fix, and the
ANTI_TANGENT_MAX_TOKENS_CEILING env var. Closes #10."
```

- [ ] **Step 7: Sanity-check the final state**

Run: `git log --oneline version/0.3.0 ^main`
Expected: 8 commits — 1 from spec, 7 from this plan's tasks. Each commit message follows the project convention and references issue #10 (except the spec commit which doesn't need to).

Run: `go test -race ./...`
Expected: PASS.

Run: `goreleaser release --snapshot --clean --skip=publish`
Expected: PASS — local release artifacts build successfully (validates the binary still ships).
