# Review-Noise and Evidence-Context UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce predictable text-only reviewer noise while giving callers structured ways to provide evidence context for `validate_plan`, `validate_task_spec`, and `validate_completion`.

**Architecture:** Keep the reviewer text-only and advisory. Add small server-side normalization helpers around existing handler boundaries, then update prompt inputs/templates so optional caller context influences reviewer behavior without changing the verdict schema.

**Tech Stack:** Go, `testing`, `testify`, embedded `text/template` prompts, existing MCP handler tests with fake reviewers.

---

## File Structure

- Before implementation, work from branch `version/0.3.3` so it matches the required `CHANGELOG.md` entry.
- Modify `internal/session/session.go` to carry new optional task-spec context (`PinnedBy`, `Phase`) in sessions.
- Modify `internal/mcpsrv/handlers.go` to validate new args, calculate adaptive plan budgets, normalize plan unverifiable findings, calibrate verdicts, and detect referenced completion paths.
- Modify `internal/prompts/prompts.go` to expose referenced-path evidence hints to `post.tmpl`; `PinnedBy` and `Phase` flow through `session.TaskSpec`.
- Modify `internal/prompts/templates/pre.tmpl` to render `Pinned by:` and phase-specific guidance.
- Modify `internal/prompts/templates/post.tmpl` to render `Pinned by:` anchors and missing referenced-path evidence notes.
- Modify `internal/mcpsrv/handlers_test.go`, `internal/mcpsrv/handlers_plan_test.go`, and `internal/prompts/prompts_test.go` for behavior and prompt coverage.
- Modify `internal/prompts/testdata/pre_basic.golden` and `internal/prompts/testdata/post_basic.golden` after intentional prompt changes.
- Modify `README.md`, `INTEGRATION.md`, and `CHANGELOG.md` for user-facing docs.

---

### Task 1: Add `pinned_by` and `phase` to `validate_task_spec`

**Goal:** `validate_task_spec` accepts optional `pinned_by` and `phase` inputs, stores them in the task session, and renders reviewer guidance that uses them as caller-supplied context.

**Acceptance criteria:**
- `ValidateTaskSpecArgs` accepts `pinned_by` as `[]string` and `phase` as `string` without changing existing callers.
- Empty/whitespace `pinned_by` entries are dropped before review and session creation.
- More than 50 `pinned_by` entries or any entry longer than 500 characters is rejected before provider cost.
- `phase` accepts only omitted/`pre`/`post`; invalid values return `phase must be "pre" or "post"`.
- `pre.tmpl` renders `Pinned by:` only when entries exist and renders post-hoc guidance only for `phase: post`.
- `post.tmpl` renders `Pinned by:` only when entries exist so completion review sees caller-supplied anchors.
- Existing `validate_task_spec` tests still pass.

**Non-goals:**
- Do not add `pinned_by` to `validate_plan`.
- Do not verify `pinned_by` entries against the filesystem or codebase.
- Do not add a new severity enum.

**Context:**
YN-10178 showed that terse ACs were often pinned by existing tests, but the API had no structured way to express that. `phase: post` is a recovery hint, not a replacement for task-start validation.

**Files:**
- Modify: `internal/session/session.go`
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/templates/post.tmpl`
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/prompts/testdata/pre_basic.golden`
- Modify: `internal/prompts/testdata/post_basic.golden`
- Test: `internal/mcpsrv/handlers_test.go`

- [ ] **Step 1: Add failing handler tests for phase validation and pinned_by storage**

Append these tests near the existing `ValidateTaskSpec` tests in `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_InvalidPhaseRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		Phase:     "during",
	})
	require.Error(t, err)
	assert.EqualError(t, err, `phase must be "pre" or "post"`)
	assert.Equal(t, 0, rv.Calls)
}

func TestValidateTaskSpec_PinnedByTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		PinnedBy:  []string{"  TestA.pins_behavior  ", "", "   ", "docs/spec.md"},
		Phase:     "post",
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"TestA.pins_behavior", "docs/spec.md"}, sess.Spec.PinnedBy)
	assert.Equal(t, "post", sess.Spec.Phase)
}

func TestValidateTaskSpec_PinnedByLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "Test.pins_behavior"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		PinnedBy:  tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned_by must contain at most 50 entries")

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		PinnedBy:  []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned_by[0] must be at most 500 characters")
	assert.Equal(t, 0, rv.Calls)
}
```

- [ ] **Step 2: Run the focused failing tests**

Run: `go test ./internal/mcpsrv -run 'TestValidateTaskSpec_(InvalidPhaseRejected|PinnedByTrimmedAndStored|PinnedByLimitsRejected)'`

Expected: FAIL with compile errors for missing `PinnedBy` and `Phase` fields.

- [ ] **Step 3: Add fields to task spec and request args**

Update `internal/session/session.go`:

```go
type TaskSpec struct {
	Title              string   `json:"title"`
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	Context            string   `json:"context,omitempty"`
	PinnedBy           []string `json:"pinned_by,omitempty"`
	Phase              string   `json:"phase,omitempty"`
}
```

Update `ValidateTaskSpecArgs` in `internal/mcpsrv/handlers.go`:

```go
type ValidateTaskSpecArgs struct {
	TaskTitle          string   `json:"task_title"           jsonschema:"required"`
	Goal               string   `json:"goal"                 jsonschema:"required"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	Context            string   `json:"context,omitempty"`
	PinnedBy           []string `json:"pinned_by,omitempty"`
	Phase              string   `json:"phase,omitempty"`
	ModelOverride      string   `json:"model_override,omitempty"`
	MaxTokensOverride  int      `json:"max_tokens_override,omitempty"`
}
```

- [ ] **Step 4: Add normalization helpers and wire them into `ValidateTaskSpec`**

Add near `effectiveMaxTokens` in `internal/mcpsrv/handlers.go`:

```go
const (
	maxPinnedByEntries = 50
	maxPinnedByChars   = 500
)

func normalizePhase(phase string) (string, error) {
	phase = strings.TrimSpace(phase)
	switch phase {
	case "", "pre":
		return "pre", nil
	case "post":
		return "post", nil
	default:
		return "", errors.New(`phase must be "pre" or "post"`)
	}
}

func normalizePinnedBy(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("pinned_by[%d] must be at most %d characters", len(out), maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("pinned_by must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}
```

Inside `ValidateTaskSpec`, after required-field validation and before max-token/model work, add:

```go
phase, err := normalizePhase(args.Phase)
if err != nil {
	return nil, Envelope{}, err
}
pinnedBy, err := normalizePinnedBy(args.PinnedBy)
if err != nil {
	return nil, Envelope{}, err
}
```

Update the `session.TaskSpec` literal:

```go
spec := session.TaskSpec{
	Title:              args.TaskTitle,
	Goal:               args.Goal,
	AcceptanceCriteria: args.AcceptanceCriteria,
	NonGoals:           args.NonGoals,
	Context:            args.Context,
	PinnedBy:           pinnedBy,
	Phase:              phase,
}
```

- [ ] **Step 5: Run focused handler tests**

Run: `go test ./internal/mcpsrv -run 'TestValidateTaskSpec_(InvalidPhaseRejected|PinnedByTrimmedAndStored|PinnedByLimitsRejected)'`

Expected: PASS.

- [ ] **Step 6: Add failing prompt tests for pinned_by and post phase**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_WithPinnedByIncludesAnchors(t *testing.T) {
	spec := sampleSpec()
	spec.PinnedBy = []string{"HealthHandlerTest.TestOK", "go test ./internal/http"}
	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Pinned by:")
	assert.Contains(t, out.User, "HealthHandlerTest.TestOK")
	assert.Contains(t, out.User, "caller-supplied anchors")
}

func TestRenderPre_WithoutPinnedByOmitsSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "Pinned by:")
}

func TestRenderPre_PostPhaseIncludesGuidance(t *testing.T) {
	spec := sampleSpec()
	spec.Phase = "post"
	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Phase: post-hoc")
	assert.Contains(t, out.User, "implementation-alignment")
}

func TestRenderPost_WithPinnedByIncludesAnchors(t *testing.T) {
	spec := sampleSpec()
	spec.PinnedBy = []string{"HealthHandlerTest.TestOK", "go test ./internal/http"}
	out, err := RenderPost(PostInput{Spec: spec, Summary: "implemented", TestEvidence: "go test PASS"})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Pinned by:")
	assert.Contains(t, out.User, "HealthHandlerTest.TestOK")
	assert.Contains(t, out.User, "caller-supplied anchors")
}
```

- [ ] **Step 7: Run prompt tests to verify they fail**

Run: `go test ./internal/prompts -run 'TestRenderPre_(WithPinnedByIncludesAnchors|WithoutPinnedByOmitsSection|PostPhaseIncludesGuidance)|TestRenderPost_WithPinnedByIncludesAnchors'`

Expected: FAIL because `pre.tmpl` and `post.tmpl` do not render the new sections yet.

- [ ] **Step 8: Update `pre.tmpl` and `post.tmpl`**

Modify `internal/prompts/templates/pre.tmpl` after the `Context:` block and before `## What to evaluate`:

```gotemplate
{{if .Spec.PinnedBy}}
Pinned by:
{{range .Spec.PinnedBy}}- {{.}}
{{end}}{{end}}
```

Modify the first evaluation paragraph:

```gotemplate
{{if eq .Spec.Phase "post"}}Phase: post-hoc. The caller may already have implemented the task and needs a session/review baseline. Prioritize contradictions that could make completion validation unreliable. Do not fail solely because an AC is terse when pinned_by provides concrete existing tests or docs.
{{else}}You are evaluating the SPEC ITSELF, not any code. The implementer has not started yet.
{{end}}
```

Add this paragraph after the three numbered checks:

```text
If a `Pinned by:` section is present, treat those entries as caller-supplied anchors for existing behavior. They are not codebase facts you can independently verify. Do not emit `unverifiable_codebase_claim` merely because a pinned_by entry names a file, class, method, command, or document. For ACs like "existing behavior remains unchanged," prefer a minor `quality` suggestion to enumerate the behavior only when pinned_by is empty or unrelated.
```

Modify `internal/prompts/templates/post.tmpl` after the `Context:` block and before `## What to evaluate`:

```gotemplate
{{if .Spec.PinnedBy}}
Pinned by:
{{range .Spec.PinnedBy}}- {{.}}
{{end}}{{end}}
```

Add this paragraph near the start of the post-review evaluation instructions:

```text
If a `Pinned by:` section is present, treat those entries as caller-supplied anchors for existing behavior. They are not codebase facts you can independently verify. Do not emit `unverifiable_codebase_claim` merely because a pinned_by entry names a file, class, method, command, or document. For ACs like "existing behavior remains unchanged," judge whether the completion evidence plausibly preserved the pinned behavior before asking for more AC wording.
```

- [ ] **Step 9: Run prompt tests and update golden**

Run: `go test ./internal/prompts -run 'TestRenderPre_(WithPinnedByIncludesAnchors|WithoutPinnedByOmitsSection|PostPhaseIncludesGuidance)|TestRenderPost_WithPinnedByIncludesAnchors'`

Expected: PASS.

Run: `go test ./internal/prompts -run 'TestRenderPre|TestRenderPost' -update`

Expected: PASS and `internal/prompts/testdata/pre_basic.golden` plus `internal/prompts/testdata/post_basic.golden` update intentionally.

---

### Task 2: Improve `validate_plan` token budget and truncation UX

**Goal:** `validate_plan` uses a task-count-scaled default output budget and no-analysis truncation responses are impossible to mistake for a minor concern.

**Acceptance criteria:**
- Without `max_tokens_override`, `validate_plan` sends `max(cfg.PlanMaxTokens, min(cfg.MaxTokensCeiling, 2000 + 800*taskCount))`.
- Explicit `max_tokens_override` behavior remains unchanged, including clamping and clamp findings.
- No-analysis `truncatedPlanResult` emits a `major` finding with `ANTI_TANGENT_PLAN_MAX_TOKENS`, `max_tokens_override`, and `ANTI_TANGENT_MAX_TOKENS_CEILING` in actionable guidance.
- No-analysis `truncatedPlanResult` sets `plan_quality: rough`.
- Partial-recovery truncation markers remain `minor`.

**Non-goals:**
- Do not change per-task hook token defaults.
- Do not add new env vars for adaptive constants in this release.

**Context:**
YN-10178 needed `max_tokens_override=16000` to get full analysis for an 8-task large plan. The default budget should scale before the user discovers truncation.

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Test: `internal/mcpsrv/handlers_test.go`

- [ ] **Step 1: Add failing adaptive-budget test**

Add to `internal/mcpsrv/handlers_test.go` near `TestValidatePlan_UsesConfiguredPlanMaxTokens`:

```go
func TestValidatePlan_UsesAdaptivePlanMaxTokensWhenUnset(t *testing.T) {
	cap := &captureReviewer{
		name: "openai",
		Response: providers.Response{
			RawJSON: []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[{"task_index":0,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`),
			Model:   "gpt-5",
		},
	}
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Cfg.PlanMaxTokens = 4096
	d.Cfg.MaxTokensCeiling = 16384
	d.Reviews = providers.Registry{"openai": cap}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(8)})
	require.NoError(t, err)
	assert.Equal(t, 8400, cap.LastRequest.MaxTokens)
}

func TestValidatePlan_ExplicitOverrideBeatsAdaptivePlanMaxTokens(t *testing.T) {
	cap := &captureReviewer{name: "openai", Response: planPassResp()}
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Cfg.PlanMaxTokens = 4096
	d.Cfg.MaxTokensCeiling = 16384
	d.Reviews = providers.Registry{"openai": cap}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          buildPlanWithNTasks(8),
		MaxTokensOverride: 5000,
	})
	require.NoError(t, err)
	assert.Equal(t, 5000, cap.LastRequest.MaxTokens)
}
```

- [ ] **Step 2: Run focused tests to verify failure**

Run: `go test ./internal/mcpsrv -run 'TestValidatePlan_(UsesAdaptivePlanMaxTokensWhenUnset|ExplicitOverrideBeatsAdaptivePlanMaxTokens)'`

Expected: first test FAILS with `4096` sent instead of `8400`; second should PASS or fail only due setup.

- [ ] **Step 3: Implement adaptive helper and use task count**

Add near `effectiveMaxTokens` in `internal/mcpsrv/handlers.go`:

```go
func effectivePlanMaxTokens(args ValidatePlanArgs, cfg config.Config, taskCount int) (int, verdict.Finding, error) {
	if args.MaxTokensOverride != 0 {
		return effectiveMaxTokens(args.MaxTokensOverride, cfg.PlanMaxTokens, cfg.MaxTokensCeiling)
	}
	floor := 2000 + 800*taskCount
	if floor > cfg.MaxTokensCeiling {
		floor = cfg.MaxTokensCeiling
	}
	if floor < cfg.PlanMaxTokens {
		floor = cfg.PlanMaxTokens
	}
	return floor, verdict.Finding{}, nil
}
```

In `ValidatePlan`, move token calculation until after `tasks` are split. Replace:

```go
maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PlanMaxTokens, h.deps.Cfg.MaxTokensCeiling)
```

with an explicit early override/clamp calculation before payload-size and no-heading early exits:

```go
maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PlanMaxTokens, h.deps.Cfg.MaxTokensCeiling)
if err != nil {
	return nil, verdict.PlanResult{}, err
}
```

Keep the existing `prependPlanClamp(tooLargePlanResult(...), clamp)` and `prependPlanClamp(noHeadingsPlanResult(), clamp)` early-exit behavior unchanged. After `tasks` are split and before provider resolution, add:

```go
if args.MaxTokensOverride == 0 {
	maxTokens, clamp, err = effectivePlanMaxTokens(args, h.deps.Cfg, len(tasks))
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}
}
```

This preserves the existing `TestMaxTokensOverride_ClampSurvivesEarlyExits` coverage for no-headings and payload-too-large plan exits while applying the adaptive floor only on normal no-override review paths.

- [ ] **Step 4: Run adaptive tests**

Run: `go test ./internal/mcpsrv -run 'TestValidatePlan_(UsesAdaptivePlanMaxTokensWhenUnset|ExplicitOverrideBeatsAdaptivePlanMaxTokens|MaxTokensOverride_ValidatePlan|MaxTokensOverride_NegativeRejected)'`

Expected: PASS.

- [ ] **Step 5: Add failing truncation UX test**

Update `TestValidatePlan_TruncatedResponseSurfacesWarn` in `internal/mcpsrv/handlers_test.go` to assert major severity and actionable next action:

```go
assert.Equal(t, verdict.SeverityMajor, pr.PlanFindings[0].Severity)
assert.Equal(t, verdict.PlanQualityRough, pr.PlanQuality)
assert.Contains(t, pr.PlanFindings[0].Suggestion, "ANTI_TANGENT_PLAN_MAX_TOKENS")
assert.Contains(t, pr.PlanFindings[0].Suggestion, "max_tokens_override")
assert.Contains(t, pr.PlanFindings[0].Suggestion, "ANTI_TANGENT_MAX_TOKENS_CEILING")
assert.Contains(t, pr.NextAction, "max_tokens_override >= 16000")
assert.Contains(t, pr.NextAction, "ANTI_TANGENT_PLAN_MAX_TOKENS")
```

- [ ] **Step 6: Run truncation test to verify failure**

Run: `go test ./internal/mcpsrv -run TestValidatePlan_TruncatedResponseSurfacesWarn`

Expected: FAIL because current severity is `minor` and `next_action` is generic.

- [ ] **Step 7: Update `truncatedPlanResult`**

In `internal/mcpsrv/handlers.go`, update `truncatedPlanResult()` finding:

```go
Severity:   verdict.SeverityMajor,
Suggestion: "Retry with max_tokens_override >= 16000, set ANTI_TANGENT_PLAN_MAX_TOKENS in the MCP server env, or raise ANTI_TANGENT_MAX_TOKENS_CEILING if overrides are clamped.",
```

Update `NextAction`:

```go
NextAction: "Retry with max_tokens_override >= 16000, or set ANTI_TANGENT_PLAN_MAX_TOKENS in the MCP server env. If overrides are clamped, raise ANTI_TANGENT_MAX_TOKENS_CEILING.",
PlanQuality: verdict.PlanQualityRough,
```

- [ ] **Step 8: Run truncation and partial-recovery tests**

Run: `go test ./internal/mcpsrv -run 'TestValidatePlan_TruncatedResponseSurfacesWarn|TestValidatePlan_PartialFindingsRecoveredOnTruncation'`

Expected: PASS and partial-recovery marker remains `minor`.

---

### Task 3: Roll up plan-level unverifiable findings and calibrate verdicts

**Goal:** Repeated task-level `unverifiable_codebase_claim` findings become one plan-level checklist and do not alone degrade `plan_verdict` to `warn`.

**Acceptance criteria:**
- Task-level `unverifiable_codebase_claim` findings are removed from task findings and represented by one `minor` plan-level `codebase_reference_checklist` finding.
- The rollup evidence identifies affected tasks and preserves useful original evidence text.
- If all remaining findings are minor unverifiable claims, `plan_verdict` becomes `pass` and `plan_quality` becomes `actionable` unless already `rigorous`.
- If any non-unverifiable finding remains, reviewer verdict and quality are not force-passed.

**Non-goals:**
- Do not roll up per-task `validate_task_spec` findings in this release.
- Do not parse code symbols out of evidence with a custom grammar.

**Context:**
YN-10178 produced 8 near-identical plan findings saying to verify code references. The human needs one checklist, not repeated per-task noise.

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Test: `internal/mcpsrv/handlers_plan_test.go`
- Modify: `internal/mcpsrv/summary.go` only if summary output is unreadable with rollup evidence

- [ ] **Step 1: Add failing plan-rollup tests**

Append to `internal/mcpsrv/handlers_plan_test.go`. If the file does not already import `encoding/json`, add it to the import block.

```go
func TestValidatePlan_RollsUpTaskUnverifiableFindings(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"warn",
		"plan_quality":"actionable",
		"plan_findings":[],
		"tasks":[
			{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt:10 and Foo.bar","suggestion":"verify against the actual code before dispatching"}],"suggested_header_block":"","suggested_header_reason":""},
			{"task_index":2,"task_title":"Task 2: two","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 2 cites Baz.qux","suggestion":"verify against the actual code before dispatching"}],"suggested_header_block":"","suggested_header_reason":""}
		],
		"next_action":"Verify codebase claims."
	}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(2)})
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, verdict.CategoryUnverifiableCodebaseClaim, pr.PlanFindings[0].Category)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[0].Criterion)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 1")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Foo.kt:10")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 2")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Baz.qux")
	assert.Empty(t, pr.Tasks[0].Findings)
	assert.Empty(t, pr.Tasks[1].Findings)
}

func TestValidatePlan_UnverifiableOnlyCalibratesToPass(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Verify codebase claims."}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.Equal(t, verdict.PlanQualityActionable, pr.PlanQuality)
	assert.Contains(t, pr.NextAction, "No blocking plan-quality findings")
}

func TestValidatePlan_MixedFindingsDoNotCalibrateToPass(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"AC is vague","suggestion":"rewrite"},{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Rewrite AC."}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictWarn, pr.PlanVerdict)
	require.Len(t, pr.Tasks, 1)
	require.Len(t, pr.Tasks[0].Findings, 1)
	assert.Equal(t, verdict.CategoryAmbiguousSpec, pr.Tasks[0].Findings[0].Category)
}

func TestValidatePlan_PreservesPlanLevelUnverifiableBesideTaskRollup(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"warn",
		"plan_quality":"actionable",
		"plan_findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"plan","evidence":"Plan-level claim cites package ownership","suggestion":"verify"}],
		"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],
		"next_action":"Verify codebase claims."
	}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 2)
	assert.Equal(t, "plan", pr.PlanFindings[0].Criterion)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[1].Criterion)
	assert.Contains(t, pr.PlanFindings[1].Evidence, "Task 1")
}

func TestValidatePlan_ChunkedUnverifiableFindingsRollUp(t *testing.T) {
	chunkWithFinding := func(titles []string, findingPosition int, evidence string) providers.Response {
		t.Helper()
		type item struct {
			TaskIndex             int               `json:"task_index"`
			TaskTitle             string            `json:"task_title"`
			Verdict               string            `json:"verdict"`
			Findings              []verdict.Finding `json:"findings"`
			SuggestedHeaderBlock  string            `json:"suggested_header_block"`
			SuggestedHeaderReason string            `json:"suggested_header_reason"`
		}
		items := make([]item, 0, len(titles))
		for i, title := range titles {
			findings := []verdict.Finding{}
			if i == findingPosition {
				findings = []verdict.Finding{{
					Severity:   verdict.SeverityMinor,
					Category:   verdict.CategoryUnverifiableCodebaseClaim,
					Criterion:  "spec",
					Evidence:   evidence,
					Suggestion: "verify",
				}}
			}
			items = append(items, item{TaskIndex: i + 1, TaskTitle: title, Verdict: "warn", Findings: findings})
		}
		raw, err := json.Marshal(struct {
			Tasks []item `json:"tasks"`
		}{items})
		require.NoError(t, err)
		return providers.Response{RawJSON: raw, Model: "claude-sonnet-4-6"}
	}

	rv := &scriptedReviewer{responses: []providers.Response{
		passOneResp(),
		chunkWithFinding(titlesRange(1, 8), 0, "Task 1 cites Foo.kt"),
		chunkWithFinding(titlesRange(9, 9), 0, "Task 9 cites Baz.kt"),
	}}
	d := newDepsWithScripted(t, rv, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(9)})
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[0].Criterion)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 1")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 9")
}
```

- [ ] **Step 2: Run rollup tests to verify failure**

Run: `go test ./internal/mcpsrv -run 'TestValidatePlan_(RollsUpTaskUnverifiableFindings|UnverifiableOnlyCalibratesToPass|MixedFindingsDoNotCalibrateToPass|PreservesPlanLevelUnverifiableBesideTaskRollup|ChunkedUnverifiableFindingsRollUp)'`

Expected: FAIL because findings are still per-task and verdict remains `warn`.

- [ ] **Step 3: Implement normalization helpers**

Add to `internal/mcpsrv/handlers.go` near plan helpers:

```go
const rollupEvidencePerTaskMax = 240 // Roughly two compact lines of paths/symbols per affected task.

func normalizePlanUnverifiableFindings(pr *verdict.PlanResult) {
	var evidence []string
	for i := range pr.Tasks {
		kept := make([]verdict.Finding, 0, len(pr.Tasks[i].Findings))
		for _, f := range pr.Tasks[i].Findings {
			if f.Category != verdict.CategoryUnverifiableCodebaseClaim {
				kept = append(kept, f)
				continue
			}
			evidence = append(evidence, fmt.Sprintf("Task %d: %s", pr.Tasks[i].TaskIndex, truncate(f.Evidence, rollupEvidencePerTaskMax)))
		}
		pr.Tasks[i].Findings = kept
	}
	if len(evidence) == 0 {
		return
	}
	pr.PlanFindings = append(pr.PlanFindings, verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "codebase_reference_checklist",
		Evidence:   strings.Join(evidence, "\n"),
		Suggestion: "Pre-flight these references with grep or codebase-aware review before dispatch. Do not treat this checklist as a plan-quality defect if the references were already verified.",
	})
}

func calibratePlanVerdictForUnverifiableOnly(pr *verdict.PlanResult) {
	if !allPlanFindingsAreMinorUnverifiable(*pr) {
		return
	}
	pr.PlanVerdict = verdict.VerdictPass
	if pr.PlanQuality != verdict.PlanQualityRigorous {
		pr.PlanQuality = verdict.PlanQualityActionable
	}
	pr.NextAction = "No blocking plan-quality findings remain; pre-flight the rolled-up codebase references before dispatch."
}

func allPlanFindingsAreMinorUnverifiable(pr verdict.PlanResult) bool {
	found := false
	for _, f := range pr.PlanFindings {
		found = true
		if f.Severity != verdict.SeverityMinor || f.Category != verdict.CategoryUnverifiableCodebaseClaim {
			return false
		}
	}
	for _, task := range pr.Tasks {
		for _, f := range task.Findings {
			found = true
			if f.Severity != verdict.SeverityMinor || f.Category != verdict.CategoryUnverifiableCodebaseClaim {
				return false
			}
		}
	}
	return found
}
```

Use existing `truncate` from `summary.go` because it is in the same package.

- [ ] **Step 4: Wire normalization in `planEnvelopeResult`**

At the start of `planEnvelopeResult`, before `verdict.ApplyPlanQualitySanity(&pr)`, add:

```go
// Normalize before sanity rules: calibration needs the reviewer's original PlanQuality
// so a rigorous unverifiable-only plan can stay rigorous instead of being defaulted.
normalizePlanUnverifiableFindings(&pr)
calibratePlanVerdictForUnverifiableOnly(&pr)
```

Then keep existing sanity and summary formatting.

- [ ] **Step 5: Run rollup tests**

Run: `go test ./internal/mcpsrv -run 'TestValidatePlan_(RollsUpTaskUnverifiableFindings|UnverifiableOnlyCalibratesToPass|MixedFindingsDoNotCalibrateToPass|PreservesPlanLevelUnverifiableBesideTaskRollup|ChunkedUnverifiableFindingsRollUp)'`

Expected: PASS.

---

### Task 4: Add completion referenced-path evidence hint

**Goal:** When a completion summary references a doc/artifact path missing from submitted evidence, the post-review prompt tells the reviewer to require full evidence if that path is a deliverable.

**Acceptance criteria:**
- Referenced `.md`, `.txt`, `.json`, `.yaml`, or `.yml` paths in `summary` are detected.
- Paths already present in `final_files.Path` or `final_diff` are not reported missing.
- Missing paths render a prompt note before reviewer evaluation.
- The note is not a hard rejection and does not add synthetic findings.

**Non-goals:**
- Do not infer every possible source-code path extension.
- Do not reject the request solely because a referenced path is missing.

**Context:**
YN-10178 Task 8 needed a second `validate_completion` call because the audit doc was referenced but not included in evidence.

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/prompts/prompts.go`
- Modify: `internal/prompts/templates/post.tmpl`
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/prompts/testdata/post_basic.golden`

- [ ] **Step 1: Add prompt rendering tests**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPost_WithMissingReferencedPathsIncludesEvidenceNote(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:                           sampleSpec(),
		Summary:                        "Wrote docs/audit.md and updated implementation.",
		ReferencedPathsMissingEvidence: []string{"docs/audit.md"},
		TestEvidence:                   "go test ./... PASS",
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "summary references these paths")
	assert.Contains(t, out.User, "docs/audit.md")
}

func TestRenderPost_WithoutMissingReferencedPathsOmitsEvidenceNote(t *testing.T) {
	out, err := RenderPost(PostInput{Spec: sampleSpec(), Summary: "No referenced deliverable."})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "summary references these paths")
}
```

- [ ] **Step 2: Run prompt tests to verify failure**

Run: `go test ./internal/prompts -run 'TestRenderPost_(WithMissingReferencedPathsIncludesEvidenceNote|WithoutMissingReferencedPathsOmitsEvidenceNote)'`

Expected: FAIL with missing `ReferencedPathsMissingEvidence` field.

- [ ] **Step 3: Add prompt input field and template section**

Update `PostInput` in `internal/prompts/prompts.go`:

```go
type PostInput struct {
	Spec                           session.TaskSpec
	Summary                        string
	Files                          []File
	FinalDiff                      string
	TestEvidence                   string
	ReferencedPathsMissingEvidence []string
}
```

In `internal/prompts/templates/post.tmpl`, after `{{.Summary}}` and before `## Final implementation`, add:

```gotemplate
{{if .ReferencedPathsMissingEvidence}}

## Referenced paths missing from evidence

The implementer's summary references these paths, but they are not present in final_files paths or final_diff text. If a referenced path is a deliverable for an acceptance criterion, require full evidence for it.
{{range .ReferencedPathsMissingEvidence}}- {{.}}
{{end}}{{end}}
```

- [ ] **Step 4: Add handler-side detection tests**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestReferencedPathsMissingEvidence(t *testing.T) {
	args := ValidateCompletionArgs{
		Summary:    "Created docs/audit.md and reports/result.yaml.",
		FinalFiles: []FileArg{{Path: "docs/audit.md", Content: "# Audit\n"}},
		FinalDiff:  "diff --git a/other.txt b/other.txt\n",
	}
	assert.Equal(t, []string{"reports/result.yaml"}, referencedPathsMissingEvidence(args))
}

func TestValidateCompletion_RendersReferencedPathEvidenceNote(t *testing.T) {
	cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}}
	d := newDeps(t, &cap.fakeReviewer)
	d.Reviews = providers.Registry{"anthropic": cap}
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)

	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:     pre.SessionID,
		Summary:       "Created docs/audit.md.",
		TestEvidence:  "not run; docs only",
	})
	require.NoError(t, err)
	assert.Contains(t, cap.LastRequest.User, "summary references these paths")
	assert.Contains(t, cap.LastRequest.User, "docs/audit.md")
}
```

- [ ] **Step 5: Implement referenced-path detection**

Add near completion evidence helpers in `internal/mcpsrv/handlers.go`:

```go
var referencedEvidencePathRE = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(?:md|txt|json|ya?ml)\b`)

func referencedPathsMissingEvidence(args ValidateCompletionArgs) []string {
	candidates := referencedEvidencePathRE.FindAllString(args.Summary, -1)
	if len(candidates) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	missing := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if pathPresentInEvidence(path, args) {
			continue
		}
		missing = append(missing, path)
	}
	return missing
}

func pathPresentInEvidence(path string, args ValidateCompletionArgs) bool {
	for _, f := range args.FinalFiles {
		if f.Path == path {
			return true
		}
	}
	return strings.Contains(args.FinalDiff, path)
}
```

In `ValidateCompletion`, pass the field to `prompts.RenderPost`:

```go
ReferencedPathsMissingEvidence: referencedPathsMissingEvidence(args),
```

- [ ] **Step 6: Run focused tests and update golden**

Run: `go test ./internal/prompts -run 'TestRenderPost_(WithMissingReferencedPathsIncludesEvidenceNote|WithoutMissingReferencedPathsOmitsEvidenceNote)'`

Expected: PASS.

Run: `go test ./internal/mcpsrv -run 'TestReferencedPathsMissingEvidence|TestValidateCompletion_RendersReferencedPathEvidenceNote'`

Expected: PASS.

Run: `go test ./internal/prompts -run TestRenderPost -update`

Expected: PASS and `internal/prompts/testdata/post_basic.golden` updates intentionally if template spacing changed.

---

### Task 5: Update docs and changelog

**Goal:** User-facing docs describe the new API inputs, calibrated plan behavior, completion evidence guidance, and CodeScene cadence learned from the field reports.

**Acceptance criteria:**
- `CHANGELOG.md` has a `## [0.3.3] - 2026-05-14` entry with Added/Changed/Documentation bullets.
- `README.md` documents `pinned_by`, `phase`, adaptive plan budget, and unverifiable rollup/verdict calibration.
- `INTEGRATION.md` documents caller discipline from YN-10178: pre-flight greps, `final_files` for doc deliverables, avoiding self-verified code claims in plan prose, explicit commit-policy carve-outs, and CodeScene cadence.
- `INTEGRATION.md` includes a consumer-facing CodeScene setup subsection that names `codescene-mcp`, `CS_ACCESS_TOKEN`, the `npx -y @codescene/codehealth-mcp` server command, and the dispatch-clause calls consumers must add.
- `INTEGRATION.md` includes usage examples for `pinned_by`, `phase: "post"`, adaptive `validate_plan` retries/rollups, and completion evidence selection.
- `validateTaskSpecTool()` mentions `pinned_by`/`phase`, and `validatePlanTool()` mentions literal plan-policy carve-outs.

**Non-goals:**
- Do not document `info` severity.
- Do not promise automatic CodeScene invocation.

**Context:**
The behavioral changes reduce noise, but the field reports also exposed caller-side practices that should be durable docs.

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `README.md`
- Modify: `INTEGRATION.md`
- Modify: `internal/mcpsrv/handlers.go`

- [ ] **Step 1: Update tool descriptions**

In `validateTaskSpecTool()` description, replace the final sentence with:

```go
"Call this once at the start of every task. Optional pinned_by entries can name existing tests/docs that pin behavior; optional phase=post is for post-hoc recovery only."
```

In `validatePlanTool()` description, append:

```go
"If repo policy has exceptions such as docs-only commit carve-outs, state them literally in plan_text because the reviewer cannot read external CLAUDE.md policy."
```

- [ ] **Step 2: Update `CHANGELOG.md`**

Insert above `## [0.3.2]`:

```markdown
## [0.3.3] - 2026-05-14

### Added
- `validate_task_spec` accepts optional `pinned_by` entries for existing tests/docs/static checks that pin behavior, plus optional `phase` (`pre` default, `post` for post-hoc recovery reviews).
- `validate_completion` prompts now highlight summary-referenced doc/artifact paths that are missing from `final_files` and `final_diff` evidence.

### Changed
- `validate_plan` now scales its default output-token budget by task count when no `max_tokens_override` is supplied, bounded by `ANTI_TANGENT_MAX_TOKENS_CEILING`.
- No-analysis `validate_plan` truncation responses now emit a `major` finding with self-contained retry guidance.
- Task-level `unverifiable_codebase_claim` findings from `validate_plan` are rolled up into a single plan-level checklist finding.
- Plans whose only findings are minor `unverifiable_codebase_claim` checklist items now return `plan_verdict: pass` with `plan_quality: actionable`.

### Documentation
- `INTEGRATION.md` documents pre-flight grep discipline, `final_files` for doc deliverables, avoiding unverifiable self-review claims in plan prose, literal commit-policy carve-outs, CodeScene MCP setup requirements, and recommended CodeScene cadence.
```

- [ ] **Step 3: Update `README.md` API docs**

Find the `validate_task_spec` arguments section and add bullets:

```markdown
- `pinned_by` (optional): existing tests, docs, commands, or static checks that pin referenced behavior. The reviewer treats these as caller-supplied anchors, not independently verified codebase facts.
- `phase` (optional): `pre` (default) or `post`. Use `post` only for post-hoc/session-recovery reviews; normal protocol still calls this at task start.
```

Find the `validate_plan` section and add:

```markdown
`validate_plan` scales its default output budget by task count when no `max_tokens_override` is supplied. If reviewer output still truncates before any usable analysis, the response is a `warn` with a `major` truncation finding and retry guidance naming `max_tokens_override`, `ANTI_TANGENT_PLAN_MAX_TOKENS`, and `ANTI_TANGENT_MAX_TOKENS_CEILING`.

Task-level `unverifiable_codebase_claim` findings are rolled into one plan-level checklist. If that checklist is the only remaining finding category, `plan_verdict` is `pass` and `plan_quality` is `actionable`; callers should still pre-flight the references before dispatch.
```

- [ ] **Step 4: Update `INTEGRATION.md` guidance**

Add a subsection near `Scope and limits`:

```markdown
### Reducing text-only review noise

- Pre-flight grep before calling `validate_task_spec` when the task names codebase references.
- Use `pinned_by` to name existing tests/docs/commands that pin "unchanged behavior" ACs.
- Do not add plan prose like "all file references were verified"; that assertion is itself unverifiable to the reviewer.
- State commit-policy carve-outs literally in the plan text. The reviewer reads only `plan_text`, not repo-level policy files.
- For doc deliverables, submit full content via `final_files`; diffs or prose summaries are often insufficient evidence.
```

Add a subsection near the existing CodeScene companion section:

````markdown
### Enabling CodeScene companion tools

Anti-tangent does not call CodeScene automatically. To make the optional CodeScene companion steps available, configure CodeScene MCP in the same MCP host that runs anti-tangent.

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

Keep anti-tangent and CodeScene as separate MCP servers. Anti-tangent remains the text-only plan/task/completion reviewer; CodeScene supplies deterministic code-health checks over the working tree.
````

Update CodeScene companion guidance to include this exact call mapping:

```markdown
- Call `pre_commit_code_health_safeguard` for each task that touches production code.
- Call `analyze_change_set` during the branch verification sweep or before final handoff.
- Call `code_health_review` when a safeguard returns `degraded` and you need refactor-trigger context for a sibling-ticket recommendation.
```

Add a subsection showing the new anti-tangent API features in use:

````markdown
### Using v0.3.3 review-context features

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
````

- [ ] **Step 5: Run docs grep checks**

Run: `go test ./internal/mcpsrv ./internal/prompts`

Expected: PASS.

Run: `grep -R "pinned_by\|phase\|codebase_reference_checklist\|CS_ACCESS_TOKEN\|pre_commit_code_health_safeguard\|analyze_change_set" README.md INTEGRATION.md CHANGELOG.md`

Expected: output includes the new docs entries for each term, including CodeScene setup/configuration and companion call mapping in `INTEGRATION.md`.

---

### Task 6: Full verification and release hygiene

**Goal:** The complete change set builds, tests pass under race detector, prompt goldens are intentional, and the implementation matches the approved spec.

**Acceptance criteria:**
- `go test -race ./...` passes.
- Prompt golden diffs are limited to intentional `pre.tmpl` and `post.tmpl` changes.
- The implementation satisfies every in-scope spec item and does not implement out-of-scope items.
- Work is on branch `version/0.3.3`, matching the `## [0.3.3]` changelog entry.
- No commit is created unless the user explicitly asks.

**Non-goals:**
- Do not run e2e provider tests unless API keys and explicit user direction are available.
- Do not create a release tag.

**Context:**
This repo’s mainline verification command is `go test -race ./...`.

**Files:**
- Verify: all modified files

- [ ] **Step 1: Run full test suite**

Run: `go test -race ./...`

Expected: PASS across all packages.

- [ ] **Step 2: Review diff**

Run: `git diff -- internal session README.md INTEGRATION.md CHANGELOG.md docs/superpowers/specs/2026-05-14-review-noise-and-evidence-context-design.md docs/superpowers/plans/2026-05-14-review-noise-and-evidence-context.md`

Expected: diff contains only planned changes. If the pathspec is too broad or invalid, run `git diff -- .` and inspect the same files manually.

- [ ] **Step 3: Verify no sentinel draft text in plan/spec/docs**

Run: `rg 'T[B]D|TO[D]O|place''holder' docs/superpowers/specs/2026-05-14-review-noise-and-evidence-context-design.md docs/superpowers/plans/2026-05-14-review-noise-and-evidence-context.md README.md INTEGRATION.md CHANGELOG.md`

Expected: no output, except pre-existing unrelated changelog text if present.

- [ ] **Step 4: Final anti-tangent validation**

Call `validate_completion` with a complete diff or final file contents and `test_evidence` containing the `go test -race ./...` output.

Expected: verdict is not `fail` and contains no `critical` or `major` findings that remain unaddressed.
