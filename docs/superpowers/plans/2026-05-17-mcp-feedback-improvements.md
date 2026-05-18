# MCP Feedback Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the 0.4.0 feedback-driven improvements from issue #18: lower per-task reviewer noise, identify lightweight plan tasks, cache duplicate passing plan reviews, and tighten companion workflow docs.

**Architecture:** Keep anti-tangent text-only and advisory. Add optional inputs and optional output fields at existing handler/parser boundaries, normalize reviewer findings server-side, and keep the new cache process-local with a short TTL.

**Tech Stack:** Go, `testing`, `testify`, embedded `text/template` prompt templates, JSON Schema files in `internal/verdict`, existing fake-reviewer MCP handler tests.

---

## File Structure

- Start implementation on branch `version/0.4.0` and add a matching `## [0.4.0] - 2026-05-17` entry to `CHANGELOG.md` before code tasks land.
- Task 0 must run first. Tasks 1-6 are sequential because they share handler, prompt, and golden-test files; do not dispatch them in parallel. Tasks 7 and 8 are docs-only and must run after Task 6 completes so they document implemented behavior; they both touch `INTEGRATION.md`, so run them sequentially too.
- Modify `internal/session/session.go` to store `ControllerVerifiedReferences` in `session.TaskSpec`.
- Modify `internal/mcpsrv/task_spec_input.go` to normalize `controller_verified_references` with the existing 50-entry / 500-character limits.
- Modify `internal/mcpsrv/handlers.go` to wire new task-spec fields, normalize pre-findings, render major pre-findings in post prompts, and use a plan pass cache.
- Modify or create `internal/mcpsrv/task_spec_normalize.go` for per-task `unverifiable_codebase_claim` rollup helpers.
- Create `internal/mcpsrv/plan_cache.go` for process-local `validate_plan` pass caching.
- Modify `internal/session/store.go` only if existing session mutation helpers cannot expose major pre-findings cleanly.
- Modify `internal/prompts/prompts.go` to add `MajorPreFindings []verdict.Finding` to `PostInput`; `ControllerVerifiedReferences` flows through `session.TaskSpec`.
- Modify `internal/prompts/templates/pre.tmpl`, `post.tmpl`, `plan.tmpl`, and `plan_tasks_chunk.tmpl` for reviewer guidance.
- Modify `internal/verdict/plan.go`, `plan_schema.json`, and `tasks_only_schema.json` for lightweight task metadata.
- Modify tests in `internal/mcpsrv/handlers_test.go`, `internal/mcpsrv/handlers_plan_test.go`, `internal/verdict/plan_test.go`, and `internal/prompts/prompts_test.go`.
- Update golden prompt files under `internal/prompts/testdata/` with `go test ./internal/prompts/... -update` after intentional prompt changes.
- Update `README.md`, `INTEGRATION.md`, `examples/lightweight-dispatch.md`, and `CHANGELOG.md`.
- Create anonymized CodeScene draft issue files under `docs/feedback/codescene/`.

---

### Task 0: Prepare 0.4.0 branch and changelog stub

**Goal:** The implementation branch and changelog satisfy repository release policy before any code commits land.

**Acceptance criteria:**
- Current branch is `version/0.4.0` before Task 1 starts.
- `CHANGELOG.md` contains `## [0.4.0] - 2026-05-17` before any code commit is pushed.
- The changelog stub has Added/Changed/Documentation subsections matching the planned 0.4.0 surface.

**Non-goals:**
- Do not implement runtime behavior in this task.
- Do not update `VERSION`; release automation owns version bumps.

**Context:**
CI enforces that a `version/X.Y.Z` branch has a matching changelog entry. Creating the changelog stub first prevents later task commits from failing branch-policy checks on push.

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Create or switch to the version branch**

Run: `git switch -c version/0.4.0`

Expected: branch changes to `version/0.4.0`. If the branch already exists locally, run `git switch version/0.4.0` instead.

- [ ] **Step 2: Add the changelog stub**

Add this entry near the top of `CHANGELOG.md`, below the intro:

```markdown
## [0.4.0] - 2026-05-17

### Added
- `validate_task_spec` accepts optional `controller_verified_references` entries so controllers can identify codebase references they already grep-verified before dispatch.
- `validate_plan` task results include optional `lightweight_eligible` and `lightweight_reason` fields to guide controller-side lightweight dispatch decisions.
- `validate_plan` caches identical passing plan reviews in memory for 3 minutes, returning cached hits with `review_ms: 0` and a `[cached <=3m]` `next_action` prefix.

### Changed
- `validate_task_spec` rolls multiple per-task `unverifiable_codebase_claim` findings into one `codebase_reference_checklist` finding.
- `validate_completion` prompts now include prior major pre-task findings so reviewers can check whether the implementation mitigated them.
- `validate_task_spec` prompt guidance is tuned for test-only tasks to reduce repeated low-value `null`/`unchanged` ambiguity findings while preserving invocation-count and negative-assertion critiques.

### Documentation
- Integration docs clarify `pinned_by` vs `context` vs `controller_verified_references`, shorten the target dispatch clause, and make CodeScene's deterministic mid-task safeguard recommended when configured.
```

- [ ] **Step 3: Verify branch-policy precondition**

Run: `git branch --show-current`

Expected: `version/0.4.0`

Run: `rg '^## \[0\.4\.0\] - 2026-05-17' CHANGELOG.md`

Expected: one matching changelog heading.

- [ ] **Step 4: Commit task 0**

```bash
git add CHANGELOG.md
git commit -m "docs: add 0.4.0 changelog stub" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1: Add `controller_verified_references` to task specs

**Goal:** `validate_task_spec` accepts, normalizes, stores, and renders controller-verified reference context without changing behavior for existing callers.

**Acceptance criteria:**
- `ValidateTaskSpecArgs` accepts optional `controller_verified_references`.
- `session.TaskSpec` stores normalized `ControllerVerifiedReferences` values.
- Empty/whitespace entries are dropped.
- More than 50 non-empty entries or any entry longer than 500 Unicode code points is rejected before provider cost.
- `pre.tmpl` renders `Controller-verified references:` only when entries exist.
- Reviewer guidance uses the deterministic substring suppression rule and says not to suppress contradictions, missing ACs, or ambiguity findings.

**Non-goals:**
- Do not verify references against the filesystem or codebase.
- Do not add the field to `validate_plan`.
- Do not change `pinned_by` semantics.

**Context:**
The existing `pinned_by` normalization in `internal/mcpsrv/task_spec_input.go` already defines `maxPinnedByEntries = 50` and `maxPinnedByChars = 500`; reuse those constants for this new field.

**Files:**
- Modify: `internal/session/session.go`
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/mcpsrv/task_spec_input.go`
- Modify: `internal/prompts/templates/pre.tmpl`
- Test: `internal/mcpsrv/handlers_test.go`
- Test: `internal/prompts/prompts_test.go`
- Test: `internal/prompts/testdata/pre_basic.golden`

- [ ] **Step 1: Add failing handler tests for normalization and storage**

Add these tests near existing `ValidateTaskSpec` input-normalization tests in `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_ControllerVerifiedReferencesTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:                    "T",
		Goal:                         "G",
		ControllerVerifiedReferences: []string{"  internal/foo.go:12  ", "", "   ", "Foo.Bar"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"internal/foo.go:12", "Foo.Bar"}, sess.Spec.ControllerVerifiedReferences)
}

func TestValidateTaskSpec_ControllerVerifiedReferencesLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "internal/foo.go"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:                    "T",
		Goal:                         "G",
		ControllerVerifiedReferences: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller_verified_references must contain at most 50 entries")

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:                    "T",
		Goal:                         "G",
		ControllerVerifiedReferences: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller_verified_references[0] must be at most 500 characters")
	assert.Equal(t, 0, rv.Calls)
}
```

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/mcpsrv -run 'TestValidateTaskSpec_ControllerVerifiedReferences'`

Expected: FAIL with compile errors for missing `ControllerVerifiedReferences` fields.

- [ ] **Step 3: Add the new stored and request fields**

Add to `session.TaskSpec` in `internal/session/session.go`:

```go
ControllerVerifiedReferences []string `json:"controller_verified_references,omitempty"`
```

Add to `ValidateTaskSpecArgs` in `internal/mcpsrv/handlers.go`:

```go
ControllerVerifiedReferences []string `json:"controller_verified_references,omitempty"`
```

- [ ] **Step 4: Normalize controller-verified references**

In `internal/mcpsrv/task_spec_input.go`, add:

```go
func normalizeControllerVerifiedReferences(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("controller_verified_references[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("controller_verified_references must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}
```

Extend `taskSpecInputs`:

```go
type taskSpecInputs struct {
	Phase                        string
	PinnedBy                     []string
	ControllerVerifiedReferences []string
}
```

Extend `normalizeTaskSpecInputs` after `pinnedBy` normalization:

```go
controllerVerifiedReferences, err := normalizeControllerVerifiedReferences(args.ControllerVerifiedReferences)
if err != nil {
	return taskSpecInputs{}, err
}
return taskSpecInputs{
	Phase:                        phase,
	PinnedBy:                     pinnedBy,
	ControllerVerifiedReferences: controllerVerifiedReferences,
}, nil
```

- [ ] **Step 5: Wire normalized values into session spec**

In `ValidateTaskSpec`, add the field to the `session.TaskSpec` literal:

```go
ControllerVerifiedReferences: inputs.ControllerVerifiedReferences,
```

- [ ] **Step 6: Add prompt rendering tests**

Add to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_WithControllerVerifiedReferencesIncludesGuidance(t *testing.T) {
	spec := sampleSpec()
	spec.ControllerVerifiedReferences = []string{"internal/foo.go:12", "Foo.Bar"}
	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Controller-verified references:")
	assert.Contains(t, out.User, "internal/foo.go:12")
	assert.Contains(t, out.User, "substring")
	assert.Contains(t, out.User, "Do not suppress logical contradictions")
}

func TestRenderPre_WithoutControllerVerifiedReferencesOmitsSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "Controller-verified references:")
}
```

- [ ] **Step 7: Render the new pre-template section**

In `internal/prompts/templates/pre.tmpl`, after the `Pinned by:` block, add this whitespace-trimmed block so absent `ControllerVerifiedReferences` does not churn golden output:

```gotemplate
{{- if .Spec.ControllerVerifiedReferences}}
Controller-verified references:
{{range .Spec.ControllerVerifiedReferences}}- {{.}}
{{end}}{{- end}}
```

After the existing `pinned_by` guidance paragraph, add:

```text
If a Controller-verified references section is present, treat those entries as caller-supplied attestations that the controller grep-verified specific codebase references before dispatch. Suppress an `unverifiable_codebase_claim` for claim C only when some entry in controller_verified_references is a substring of C, or C is a substring of some entry. Do not suppress logical contradictions, missing acceptance criteria, or ambiguity findings.
```

- [ ] **Step 8: Update golden prompt output**

Run: `go test ./internal/prompts/... -update`

Expected: PASS and changed golden files only for intentional prompt template updates.

- [ ] **Step 9: Run focused tests**

Run: `go test ./internal/mcpsrv ./internal/prompts -run 'ControllerVerifiedReferences|RenderPre'`

Expected: PASS.

- [ ] **Step 10: Commit task 1**

```bash
git add internal/session/session.go internal/mcpsrv/handlers.go internal/mcpsrv/task_spec_input.go internal/mcpsrv/handlers_test.go internal/prompts/templates/pre.tmpl internal/prompts/prompts_test.go internal/prompts/testdata/pre_basic.golden
git commit -m "feat(task-spec): accept verified references" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Roll up per-task unverifiable findings

**Goal:** `validate_task_spec` returns and stores one compact `codebase_reference_checklist` finding instead of many `unverifiable_codebase_claim` findings.

**Acceptance criteria:**
- Multiple `unverifiable_codebase_claim` pre-findings become one minor finding with `criterion: codebase_reference_checklist`.
- Non-unverifiable findings are preserved unchanged.
- The reviewer-emitted verdict is preserved; unverifiable-only `warn` remains `warn`.
- Session `PreFindings` stores the normalized list.
- Existing plan-level rollup behavior remains unchanged.

**Non-goals:**
- Do not force-pass per-task unverifiable-only findings.
- Do not change the canonical finding schema.
- Do not alter `validate_plan` rollup semantics.

**Context:**
Reuse `rollupEvidencePerTaskMax = 240` from `internal/mcpsrv/plan_normalize.go` for evidence truncation consistency.

**Files:**
- Create: `internal/mcpsrv/task_spec_normalize.go`
- Test: `internal/mcpsrv/handlers_test.go`
- Modify: `internal/mcpsrv/handlers.go`

- [ ] **Step 1: Add failing handler test for pre-finding rollup**

Add to `internal/mcpsrv/handlers_test.go` near `ValidateTaskSpec` tests:

```go
func TestValidateTaskSpec_RollsUpUnverifiableFindings(t *testing.T) {
	raw := []byte(`{
		"verdict":"warn",
		"findings":[
			{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Foo.kt:10","suggestion":"verify"},
			{"severity":"minor","category":"ambiguous_spec","criterion":"AC","evidence":"AC is vague","suggestion":"clarify"},
			{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Bar.kt:20","suggestion":"verify"}
		],
		"next_action":"verify references"
	}`)
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{RawJSON: raw, Model: "claude-sonnet-4-6"}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	require.Len(t, env.Findings, 2)
	assert.Equal(t, verdict.CategoryAmbiguousSpec, env.Findings[0].Category)
	assert.Equal(t, verdict.CategoryUnverifiableCodebaseClaim, env.Findings[1].Category)
	assert.Equal(t, "codebase_reference_checklist", env.Findings[1].Criterion)
	assert.Contains(t, env.Findings[1].Evidence, "Foo.kt:10")
	assert.Contains(t, env.Findings[1].Evidence, "Bar.kt:20")
	assert.Equal(t, string(verdict.VerdictWarn), env.Verdict)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, env.Findings, sess.PreFindings)
}
```

- [ ] **Step 2: Run the focused failing test**

Run: `go test ./internal/mcpsrv -run TestValidateTaskSpec_RollsUpUnverifiableFindings`

Expected: FAIL because findings are still returned individually.

- [ ] **Step 3: Add per-task normalization helper**

Create `internal/mcpsrv/task_spec_normalize.go`:

```go
package mcpsrv

import (
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func normalizeTaskSpecUnverifiableFindings(findings []verdict.Finding) []verdict.Finding {
	kept, evidence := splitTaskUnverifiable(findings)
	if len(evidence) == 0 {
		return kept
	}
	kept = append(kept, verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "codebase_reference_checklist",
		Evidence:   truncate(strings.Join(evidence, "; "), rollupEvidencePerTaskMax),
		Suggestion: "Pre-flight these references with grep or codebase-aware review before implementation. If they were already verified, treat this as a checklist rather than a spec-quality defect.",
	})
	return kept
}
```

- [ ] **Step 4: Apply normalization in `ValidateTaskSpec`**

In `ValidateTaskSpec`, immediately after the review error handling and before session creation, add:

```go
result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
```

- [ ] **Step 5: Run focused tests**

Run: `go test ./internal/mcpsrv -run 'ValidateTaskSpec.*Unverifiable|ValidatePlan.*Rollup'`

Expected: PASS.

- [ ] **Step 6: Commit task 2**

```bash
git add internal/mcpsrv/task_spec_normalize.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "feat(task-spec): roll up unverifiable findings" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Add lightweight eligibility to plan task results

**Goal:** `validate_plan` can annotate each task with optional lightweight-dispatch guidance in both single-call and chunked paths.

**Acceptance criteria:**
- `PlanTaskResult` includes optional `lightweight_eligible` and `lightweight_reason` fields.
- `plan_schema.json` and `tasks_only_schema.json` allow those fields but do not require them.
- Plan and task-chunk prompts instruct reviewers to emit the fields using conservative criteria.
- `lightweight_reason` is required by prompt when `lightweight_eligible` is true, but older reviewer JSON without the fields still parses.
- Tests cover schema presence and JSON round-trip.

**Non-goals:**
- Do not make the server enforce lightweight dispatch.
- Do not change `validate_completion` lightweight mode behavior.
- Do not mark plan-level Pass 1 responses with lightweight fields.

**Context:**
Both `plan_schema.json` and `tasks_only_schema.json` set `additionalProperties: false`, so schema updates are required before reviewer output can include the new task properties.
`PlanChunkInput.ChunkTasks` is `[]planparser.RawTask` in `internal/prompts/prompts.go`, so prompt tests can construct chunk inputs directly with `planparser.RawTask{Title: "Task 1: Docs"}`.

**Files:**
- Modify: `internal/verdict/plan.go`
- Modify: `internal/verdict/plan_schema.json`
- Modify: `internal/verdict/tasks_only_schema.json`
- Modify: `internal/prompts/templates/plan.tmpl`
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl`
- Test: `internal/verdict/plan_test.go`
- Test: `internal/prompts/prompts_test.go`

- [ ] **Step 1: Add failing plan parser/schema tests**

Add to `internal/verdict/plan_test.go`:

```go
func TestPlanTaskResult_LightweightFieldsRoundTrip(t *testing.T) {
	r := PlanResult{
		PlanVerdict:  VerdictPass,
		PlanFindings: []Finding{},
		Tasks: []PlanTaskResult{{
			TaskIndex:             1,
			TaskTitle:             "Task 1: Docs",
			Verdict:               VerdictPass,
			Findings:              []Finding{},
			SuggestedHeaderBlock:  "",
			SuggestedHeaderReason: "",
			LightweightEligible:   true,
			LightweightReason:     "single docs file with exact text",
		}},
		NextAction:  "go",
		PlanQuality: PlanQualityRigorous,
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	var back PlanResult
	require.NoError(t, json.Unmarshal(b, &back))
	assert.True(t, back.Tasks[0].LightweightEligible)
	assert.Equal(t, "single docs file with exact text", back.Tasks[0].LightweightReason)
}

func TestPlanSchemas_AllowLightweightTaskFields(t *testing.T) {
	for name, raw := range map[string][]byte{"plan": PlanSchema(), "tasks_only": TasksOnlySchema()} {
		t.Run(name, func(t *testing.T) {
			var schema map[string]any
			require.NoError(t, json.Unmarshal(raw, &schema))
			props := schema["properties"].(map[string]any)
			tasks := props["tasks"].(map[string]any)
			items := tasks["items"].(map[string]any)
			taskProps := items["properties"].(map[string]any)
			assert.Contains(t, taskProps, "lightweight_eligible")
			assert.Contains(t, taskProps, "lightweight_reason")
			required := items["required"].([]any)
			assert.NotContains(t, required, "lightweight_eligible")
			assert.NotContains(t, required, "lightweight_reason")
		})
	}
}

func TestParsePlan_OlderTaskJSONDefaultsLightweightFields(t *testing.T) {
	in := []byte(`{
		"plan_verdict":"pass",
		"plan_quality":"rigorous",
		"plan_findings":[],
		"tasks":[{"task_index":1,"task_title":"Task 1: Existing","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],
		"next_action":"go"
	}`)
	r, err := ParsePlan(in)
	require.NoError(t, err)
	require.Len(t, r.Tasks, 1)
	assert.False(t, r.Tasks[0].LightweightEligible)
	assert.Empty(t, r.Tasks[0].LightweightReason)
}
```

- [ ] **Step 2: Run failing tests**

Run: `go test ./internal/verdict -run 'Lightweight|PlanSchemas'`

Expected: FAIL with missing struct fields and schema properties.

- [ ] **Step 3: Add fields to `PlanTaskResult`**

In `internal/verdict/plan.go`, add to `PlanTaskResult`:

```go
LightweightEligible bool   `json:"lightweight_eligible,omitempty"`
LightweightReason   string `json:"lightweight_reason,omitempty"`
```

- [ ] **Step 4: Add fields to both schemas**

In both `internal/verdict/plan_schema.json` and `internal/verdict/tasks_only_schema.json`, add these properties inside each task item `properties` object after `suggested_header_reason`:

```json
"lightweight_eligible": { "type": "boolean" },
"lightweight_reason":   { "type": "string" }
```

Do not add either field to the `required` arrays.

- [ ] **Step 5: Add plan prompt guidance**

In `internal/prompts/templates/plan.tmpl`, add a fourth numbered responsibility after plan-wide review:

```text
4. **Lightweight eligibility.** For every task, emit `lightweight_eligible` and `lightweight_reason`. Set `lightweight_eligible: true` only when ALL are true: the task touches at most two files or is docs/config/data-only; the task is mechanical with no production-design or test-design choices; and the plan includes literal text, exact diff, exact command, or exact insertion shape. When true, `lightweight_reason` must be a non-empty short explanation. When false, leave `lightweight_reason` empty.
```

In `internal/prompts/templates/plan_tasks_chunk.tmpl`, add the same paragraph after the per-task evaluation list.

- [ ] **Step 6: Add prompt test coverage**

Add to `internal/prompts/prompts_test.go`:

```go
func TestRenderPlan_IncludesLightweightEligibilityGuidance(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Plan\n\n### Task 1: Docs\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, "lightweight_eligible")
	assert.Contains(t, out.User, "ALL are true")
}

func TestRenderPlanTasksChunk_IncludesLightweightEligibilityGuidance(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   "# Plan\n\n### Task 1: Docs\n",
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: Docs"}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "lightweight_eligible")
	assert.Contains(t, out.User, "lightweight_reason")
}
```

- [ ] **Step 7: Run focused tests**

Run: `go test ./internal/verdict ./internal/prompts -run 'Lightweight|PlanSchemas|RenderPlan'`

Expected: PASS.

- [ ] **Step 8: Commit task 3**

```bash
git add internal/verdict/plan.go internal/verdict/plan_schema.json internal/verdict/tasks_only_schema.json internal/verdict/plan_test.go internal/prompts/templates/plan.tmpl internal/prompts/templates/plan_tasks_chunk.tmpl internal/prompts/prompts_test.go
git commit -m "feat(plan): annotate lightweight tasks" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Cache identical passing `validate_plan` results

**Goal:** Repeated identical passing `validate_plan` calls return from a short-lived in-memory cache with `review_ms: 0` and without losing the original `next_action`.

**Acceptance criteria:**
- Cache key includes rendered prompt content, mode, effective model, effective max-token budget, and a cache version string.
- Only final normalized `plan_verdict: pass` results are cached.
- Cache TTL is 3 minutes.
- Cache hit does not call the reviewer, returns `review_ms: 0`, and prepends `[cached <=3m] ` to the original `next_action`.
- Changed plan text, mode, model, max-token budget, or rendered prompt misses the cache.
- Warn/fail/truncated/error results are not cached.
- Cache is process-local only.

**Non-goals:**
- Do not persist cache entries across process restarts.
- Do not cache per-task lifecycle hooks.
- Do not cache warn or fail plan results.

**Context:**
`ValidatePlan` currently renders prompts inside `reviewPlanSingle`, `reviewPlanChunked`, and `reviewOnePlanChunk`. This task must refactor rendering so the same rendered prompt(s) are used for both the cache key and the reviewer call. Do not render once for the cache and again inside review helpers.

**Files:**
- Create: `internal/mcpsrv/plan_cache.go`
- Modify: `internal/mcpsrv/handlers.go`
- Test: `internal/mcpsrv/handlers_plan_test.go`

- [ ] **Step 1: Add failing cache tests**

Add to `internal/mcpsrv/handlers_plan_test.go`:

```go
func TestValidatePlan_CachesPassingResult(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}
	plan := "# Plan\n\n### Task 1: Docs\n\n**Goal:** Update docs.\n\n**Acceptance criteria:**\n- Docs mention cache.\n"

	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, first.PlanVerdict)
	require.Equal(t, 1, rv.Calls)

	_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, second.PlanVerdict)
	assert.Equal(t, 1, rv.Calls)
	assert.Contains(t, second.NextAction, "[cached <=3m]")
}

func TestValidatePlan_DoesNotCacheWarnResult(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[{"severity":"minor","category":"quality","criterion":"plan","evidence":"e","suggestion":"s"}],"tasks":[{"task_index":1,"task_title":"Task 1: Docs","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"fix"}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}
	plan := "# Plan\n\n### Task 1: Docs\n"

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	_, _, err = h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, 2, rv.Calls)
}
```

- [ ] **Step 2: Run failing cache tests**

Run: `go test ./internal/mcpsrv -run 'ValidatePlan_Cache|ValidatePlan_DoesNotCache'`

Expected: FAIL because reviewer is called on every request.

- [ ] **Step 3: Add cache implementation**

Create `internal/mcpsrv/plan_cache.go`:

```go
package mcpsrv

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

const (
	planPassCacheTTL     = 3 * time.Minute
	planPassCacheVersion = "v1"
)

type planPassCacheKey [32]byte

type planPassCacheEntry struct {
	result    verdict.PlanResult
	modelUsed string
	expiresAt time.Time
}

var (
	planPassCacheMu sync.Mutex
	planPassCache   = map[planPassCacheKey]planPassCacheEntry{}
)

func newPlanPassCacheKey(planText, mode, model, renderedSystem, renderedUser string, maxTokens int) planPassCacheKey {
	input := struct {
		Version        string `json:"version"`
		PlanText       string `json:"plan_text"`
		Mode           string `json:"mode"`
		Model          string `json:"model"`
		MaxTokens      int    `json:"max_tokens"`
		RenderedSystem string `json:"rendered_system"`
		RenderedUser   string `json:"rendered_user"`
	}{planPassCacheVersion, planText, mode, model, maxTokens, renderedSystem, renderedUser}
	b, _ := json.Marshal(input)
	return sha256.Sum256(b)
}

func lookupPlanPassCache(key planPassCacheKey) (verdict.PlanResult, string, bool) {
	planPassCacheMu.Lock()
	defer planPassCacheMu.Unlock()
	entry, ok := planPassCache[key]
	if !ok {
		return verdict.PlanResult{}, "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(planPassCache, key)
		return verdict.PlanResult{}, "", false
	}
	pr := entry.result
	pr.NextAction = "[cached <=3m] " + pr.NextAction
	return pr, entry.modelUsed, true
}

func storePlanPassCache(key planPassCacheKey, pr verdict.PlanResult, modelUsed string) {
	if pr.PlanVerdict != verdict.VerdictPass {
		return
	}
	planPassCacheMu.Lock()
	defer planPassCacheMu.Unlock()
	now := time.Now()
	for k, v := range planPassCache {
		if now.After(v.expiresAt) {
			delete(planPassCache, k)
		}
	}
	planPassCache[key] = planPassCacheEntry{result: pr, modelUsed: modelUsed, expiresAt: now.Add(planPassCacheTTL)}
}
```

- [ ] **Step 4: Refactor plan rendering so cache and review share the same prompt path**

Add these helper types near the plan-review helpers in `internal/mcpsrv/handlers.go` or in `plan_cache.go`:

```go
type renderedPlanReview struct {
	single prompts.Output
	pass1  prompts.Output
	chunks []renderedPlanChunk
}

type renderedPlanChunk struct {
	tasks  []planparser.RawTask
	output prompts.Output
}
```

Add this helper. It must pick the same single-vs-chunked path as `ValidatePlan` uses for review:

```go
func renderPlanReviewForCache(planText string, mode string, tasks []planparser.RawTask, chunkSize int) (renderedPlanReview, string, string, error) {
	if len(tasks) <= chunkSize {
		out, err := prompts.RenderPlan(prompts.PlanInput{PlanText: planText, Mode: mode})
		if err != nil {
			return renderedPlanReview{}, "", "", err
		}
		return renderedPlanReview{single: out}, out.System, out.User, nil
	}
	pass1, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{PlanText: planText, Mode: mode})
	if err != nil {
		return renderedPlanReview{}, "", "", err
	}
	rendered := renderedPlanReview{pass1: pass1}
	systemForKey := pass1.System
	userForKey := pass1.User
	for i := 0; i < len(tasks); i += chunkSize {
		end := i + chunkSize
		if end > len(tasks) {
			end = len(tasks)
		}
		chunkTasks := tasks[i:end]
		out, err := prompts.RenderPlanTasksChunk(prompts.PlanChunkInput{PlanText: planText, ChunkTasks: chunkTasks, Mode: mode})
		if err != nil {
			return renderedPlanReview{}, "", "", err
		}
		rendered.chunks = append(rendered.chunks, renderedPlanChunk{tasks: chunkTasks, output: out})
		systemForKey += "\n---chunk---\n" + out.System
		userForKey += "\n---chunk---\n" + out.User
	}
	return rendered, systemForKey, userForKey, nil
}
```

Refactor `reviewPlanSingle`, `reviewPlanChunked`, and `reviewOnePlanChunk` so they accept pre-rendered `prompts.Output` values instead of rendering internally. `ValidatePlan` should render once, hash those rendered bytes, and pass the same outputs into the review helpers.

- [ ] **Step 5: Store passing normalized results after review**

After successful `reviewPlanSingle` / `reviewPlanChunked` and before returning, normalize the result the same way `planEnvelopeResult` will. To avoid double-normalization, add a helper:

```go
func finalizePlanResult(pr verdict.PlanResult, modelUsed string, ms int64) verdict.PlanResult {
	normalizePlanUnverifiableFindings(&pr)
	calibratePlanVerdictForUnverifiableOnly(&pr)
	verdict.ApplyPlanQualitySanity(&pr)
	pr.SummaryBlock = formatPlanSummary(pr, modelUsed, ms)
	return pr
}
```

Then make `planEnvelopeResult` call `finalizePlanResult` exactly once, and add a lower-level marshalling helper for already-finalized results:

```go
func planEnvelopeResult(pr verdict.PlanResult, modelUsed string, ms int64) (*mcp.CallToolResult, verdict.PlanResult, error) {
	return planEnvelopeResultFinalized(finalizePlanResult(pr, modelUsed, ms), modelUsed, ms)
}

func planEnvelopeResultFinalized(pr verdict.PlanResult, modelUsed string, ms int64) (*mcp.CallToolResult, verdict.PlanResult, error) {
	body, err := json.MarshalIndent(struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{pr, modelUsed, ms}, "", "  ")
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, pr, nil
}
```

In `ValidatePlan`, finalize once after a successful reviewer call, store the finalized pass result, and return it through `planEnvelopeResultFinalized`. Cache hits should also use `planEnvelopeResultFinalized` so rollup is not applied twice.

The cache key uses the configured `model.String()` while `model_used` stored in the cache entry comes from the provider response and may include a provider-returned model id. On a cache hit, returning the first call's `model_used` is intentional because it describes the review result being reused.

- [ ] **Step 6: Add cache expiry test**

Add an unexported test helper in `plan_cache.go` so expiry can be tested without sleeping:

```go
func expirePlanPassCacheForTest(key planPassCacheKey) {
	planPassCacheMu.Lock()
	defer planPassCacheMu.Unlock()
	entry := planPassCache[key]
	entry.expiresAt = time.Now().Add(-time.Second)
	planPassCache[key] = entry
}
```

Use it in a unit test in `handlers_plan_test.go` after a first passing call has stored the cache entry. Then expire that exact key and assert a second call increments the reviewer call count.

- [ ] **Step 7: Run focused cache tests**

Run: `go test ./internal/mcpsrv -run 'ValidatePlan.*Cache|PlanPassCache'`

Expected: PASS.

- [ ] **Step 8: Commit task 4**

```bash
git add internal/mcpsrv/plan_cache.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_plan_test.go
git commit -m "feat(plan): cache identical passing reviews" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Thread major pre-findings into completion review

**Goal:** `validate_completion` prompts include prior major pre-task findings so the reviewer checks whether the implementer mitigated them.

**Acceptance criteria:**
- `PostInput` includes `MajorPreFindings []verdict.Finding`.
- Stateful `ValidateCompletion` passes major session `PreFindings` into `RenderPost`.
- Lightweight completion mode passes no major pre-findings.
- `post.tmpl` renders `Major pre-task findings to verify` only when major findings exist.
- Prompt text instructs the reviewer to check summary/evidence/test evidence for explicit mitigation.
- Tests cover rendering and handler wiring.

**Non-goals:**
- Do not create a new MCP tool.
- Do not require implementers to submit a separate mitigation field.
- Do not include minor pre-findings in the post prompt.

**Context:**
The session already stores `PreFindings`; Task 2 ensures those findings are normalized before storage.
Task 2 normalizes `PreFindings` before storage; the rolled-up `codebase_reference_checklist` is always minor and never threads into `MajorPreFindings`.

**Files:**
- Modify: `internal/prompts/prompts.go`
- Modify: `internal/prompts/templates/post.tmpl`
- Modify: `internal/mcpsrv/handlers.go`
- Test: `internal/prompts/prompts_test.go`
- Test: `internal/mcpsrv/handlers_test.go`
- Test: `internal/prompts/testdata/post_basic.golden`

- [ ] **Step 1: Add prompt rendering test**

Add to `internal/prompts/prompts_test.go`:

```go
func TestRenderPost_WithMajorPreFindingsIncludesMitigationGuidance(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:    sampleSpec(),
		Summary: "implemented",
		MajorPreFindings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryAmbiguousSpec,
			Criterion:  "AC",
			Evidence:   "ambiguous state transition",
			Suggestion: "clarify whether to clear stored state",
		}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Major pre-task findings to verify")
	assert.Contains(t, out.User, "ambiguous state transition")
	assert.Contains(t, out.User, "explicitly mitigates")
}
```

- [ ] **Step 2: Add handler wiring test**

Add to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateCompletion_RendersMajorPreFindings(t *testing.T) {
	cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}}
	d := newDeps(t, &cap.fakeReviewer)
	d.Reviews = providers.Registry{"anthropic": cap}
	h := &handlers{deps: d}
	raw := []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"ambiguous state transition","suggestion":"clarify"}],"next_action":"clarify"}`)
	cap.resp = providers.Response{RawJSON: raw, Model: "claude-sonnet-4-6"}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}})
	require.NoError(t, err)

	cap.resp = providers.Response{RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"done"}`), Model: "claude-sonnet-4-6"}
	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:    pre.SessionID,
		Summary:      "implemented after clarifying state transition",
		TestEvidence: "go test ./... PASS",
	})
	require.NoError(t, err)
	assert.Contains(t, cap.LastRequest.User, "Major pre-task findings to verify")
	assert.Contains(t, cap.LastRequest.User, "ambiguous state transition")
}
```

- [ ] **Step 3: Run failing tests**

Run: `go test ./internal/prompts ./internal/mcpsrv -run 'MajorPreFindings|RendersMajorPreFindings'`

Expected: FAIL with missing `MajorPreFindings` field or missing prompt text.

- [ ] **Step 4: Add prompt input field**

In `internal/prompts/prompts.go`, extend `PostInput`:

```go
MajorPreFindings []verdict.Finding
```

- [ ] **Step 5: Add post-template section**

In `internal/prompts/templates/post.tmpl`, after the task spec block and before `## What to evaluate`, add:

```gotemplate
{{if .MajorPreFindings}}
## Major pre-task findings to verify

The pre-implementation review raised these major findings. Check whether the implementer's summary, final evidence, or test evidence explicitly mitigates each one. If a major pre-finding appears unresolved and could affect acceptance-criterion completion, emit a completion finding mapped to the relevant AC or to `spec`.
{{range .MajorPreFindings}}- {{.Category}} / {{.Criterion}}: {{.Evidence}} Suggestion: {{.Suggestion}}
{{end}}{{end}}
```

- [ ] **Step 6: Pass major pre-findings from `ValidateCompletion`**

Add helper in `internal/mcpsrv/handlers.go` or a small nearby file:

```go
func majorFindings(findings []verdict.Finding) []verdict.Finding {
	out := make([]verdict.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Severity == verdict.SeverityMajor {
			out = append(out, f)
		}
	}
	return out
}
```

In `ValidateCompletion`, create a local slice before rendering:

```go
var majorPreFindings []verdict.Finding
if !lightweight {
	majorPreFindings = majorFindings(sess.PreFindings)
}
```

Pass it into `prompts.RenderPost`:

```go
MajorPreFindings: majorPreFindings,
```

- [ ] **Step 7: Update post golden output**

Run: `go test ./internal/prompts/... -update`

Expected: PASS with intentional golden updates.

- [ ] **Step 8: Run focused tests**

Run: `go test ./internal/prompts ./internal/mcpsrv -run 'MajorPreFindings|RendersMajorPreFindings|ValidateCompletion'`

Expected: PASS.

- [ ] **Step 9: Commit task 5**

```bash
git add internal/prompts/prompts.go internal/prompts/templates/post.tmpl internal/prompts/prompts_test.go internal/prompts/testdata/post_basic.golden internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "feat(completion): surface major pre-findings" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Tune test-only task prompt guidance

**Goal:** `validate_task_spec` prompt guidance reduces repeated low-value findings for test-only specs while preserving useful test-design critiques.

**Acceptance criteria:**
- `pre.tmpl` includes explicit test-only task guidance.
- Guidance accepts `null`/`unchanged` assertions when initial state, fixture setup, or harness assumptions are specified.
- Guidance still asks reviewers to flag missing invocation counts, missing negative assertions, unclear scenario setup, and unclear system path.
- Guidance asks reviewers to prefer one consolidated finding for repeated ambiguity.
- Golden prompt tests reflect the intentional template change.

**Non-goals:**
- Do not add task-type detection code.
- Do not add a new `task_type` input.
- Do not make reviewer consolidation behavior unit-test deterministic.

**Context:**
The reviewer can infer test-only status from the task spec text. This is prompt-only tuning.

**Files:**
- Modify: `internal/prompts/templates/pre.tmpl`
- Test: `internal/prompts/prompts_test.go`
- Test: `internal/prompts/testdata/pre_basic.golden`

- [ ] **Step 1: Add prompt test**

Add to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_IncludesTestOnlyGuidance(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.Contains(t, out.User, "For explicitly test-only tasks")
	assert.Contains(t, out.User, "missing invocation counts")
	assert.Contains(t, out.User, "one consolidated finding")
}
```

- [ ] **Step 2: Run failing prompt test**

Run: `go test ./internal/prompts -run TestRenderPre_IncludesTestOnlyGuidance`

Expected: FAIL because the prompt lacks test-only guidance.

- [ ] **Step 3: Add test-only guidance to `pre.tmpl`**

In `internal/prompts/templates/pre.tmpl`, after the acceptance-criterion quality explanation, add:

```text
For explicitly test-only tasks, treat `null`, `unchanged`, or similar assertions as acceptable when the spec names the initial state, fixture setup, or existing test harness assumption. Still flag missing invocation counts, missing negative assertions, unclear scenario setup, or unclear path through the system. When the same ambiguity repeats across multiple scenarios, prefer one consolidated finding instead of one finding per scenario.
```

- [ ] **Step 4: Update golden prompt output**

Run: `go test ./internal/prompts/... -update`

Expected: PASS with `pre_basic.golden` updated.

- [ ] **Step 5: Run focused prompt tests**

Run: `go test ./internal/prompts -run 'RenderPre|Golden'`

Expected: PASS.

- [ ] **Step 6: Commit task 6**

```bash
git add internal/prompts/templates/pre.tmpl internal/prompts/prompts_test.go internal/prompts/testdata/pre_basic.golden
git commit -m "fix(prompts): tune test-only spec review" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Update integration docs

**Goal:** User-facing docs describe the new 0.4.0 behavior, shorter dispatch shape, and stronger CodeScene companion guidance.

**Acceptance criteria:**
- `README.md` documents per-task rollup, `controller_verified_references`, lightweight annotations, and plan pass cache.
- `INTEGRATION.md` explains `pinned_by` vs `context` vs `controller_verified_references`.
- `INTEGRATION.md` includes the shorter dispatch target shape and major pre-finding mitigation rule.
- `INTEGRATION.md` makes CodeScene `pre_commit_code_health_safeguard` recommended when configured while anti-tangent `check_progress` remains optional.
- `examples/lightweight-dispatch.md` mentions `lightweight_eligible` as an advisory plan annotation.
- The 0.4.0 changelog stub from Task 0 is updated if implementation details changed during Tasks 1-6.

**Non-goals:**
- Do not claim CodeScene behavior changed.
- Do not remove the full dispatch clause unless all references still have a complete canonical version.
- Do not document fields that were not implemented in prior tasks.

**Context:**
Repo policy requires a changelog entry matching the `version/0.4.0` branch before release work.

**Files:**
- Modify: `CHANGELOG.md` only if Task 0's stub needs wording adjustments
- Modify: `README.md`
- Modify: `INTEGRATION.md`
- Modify: `examples/lightweight-dispatch.md`

- [ ] **Step 1: Confirm changelog stub still matches implementation**

Run: `rg '^## \[0\.4\.0\] - 2026-05-17|controller_verified_references|lightweight_eligible|cached <=3m' CHANGELOG.md`

Expected: matches for the 0.4.0 heading and the implemented user-visible features.

- [ ] **Step 2: Update README sections**

In `README.md`, update the `validate_task_spec arguments` section with:

```markdown
- `controller_verified_references` (optional): paths, symbols, line anchors, commands, or adjacent patterns the controller already verified. The reviewer treats these as caller-supplied attestations and suppresses matching `unverifiable_codebase_claim` findings only by deterministic substring match; contradictions and ambiguity still surface normally.
```

In the `validate_plan` behavior paragraphs, add:

```markdown
Task results may include `lightweight_eligible` and `lightweight_reason`. These are advisory controller hints for dispatching trivial mechanical tasks under lightweight mode; controllers can override them.

Identical passing `validate_plan` calls are cached in memory for 3 minutes using the rendered prompt, model, mode, and token budget as the cache identity. Cache hits return `review_ms: 0` and preserve the original `next_action` behind a `[cached <=3m]` prefix.
```

- [ ] **Step 3: Update INTEGRATION guidance**

In `INTEGRATION.md`, add a subsection under review-noise guidance:

```markdown
### Choosing `pinned_by`, `context`, and `controller_verified_references`

Use `pinned_by` when reverting or breaking the behavior would make a named test, command, static check, or document fail. Use `context` for background a human needs to understand the task. Use `controller_verified_references` for codebase facts the controller already grep-verified, such as paths, symbols, line anchors, or adjacent patterns. A value can appear in more than one field when it plays more than one role.
```

Replace or supplement the dispatch-clause guidance with the short target shape from the spec:

```markdown
## Drift protection

Use anti-tangent per the standard dispatch protocol. For this task:
- Call `validate_task_spec` before edits unless `lightweight_eligible: true` is explicitly set by the controller.
- Call `validate_completion` before DONE and paste its `summary_block`.
- If CodeScene MCP is configured, run `pre_commit_code_health_safeguard` after meaningful code changes.
- If any major pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.
```

- [ ] **Step 4: Update CodeScene companion wording**

In `INTEGRATION.md`, change CodeScene mid-task language from optional to recommended when configured:

```markdown
When CodeScene MCP is configured, `pre_commit_code_health_safeguard` is recommended after meaningful code changes. This does not change anti-tangent's `check_progress`, which remains optional because field data showed low marginal LLM signal mid-task.
```

Add delta guidance:

```markdown
On already-degraded files, read per-finding `value` vs `value_before` before reacting to the top-level gate. A top-level `failed` can mean the file was already over threshold; the task-relevant signal is the delta.
```

- [ ] **Step 5: Update lightweight example**

In `examples/lightweight-dispatch.md`, add:

```markdown
If `validate_plan` returns `lightweight_eligible: true` with a reason that matches the controller's own judgment, dispatch this lightweight template. The annotation is advisory; use the full protocol when the task has production logic, test-design choices, or ambiguous state transitions.
```

- [ ] **Step 6: Build after docs updates**

Run: `go build ./...`

Expected: exit 0 with no output.

- [ ] **Step 7: Commit task 7**

```bash
git add CHANGELOG.md README.md INTEGRATION.md examples/lightweight-dispatch.md
git commit -m "docs: document feedback improvements" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Add anonymized CodeScene upstream issue drafts

**Goal:** Preserve field-derived CodeScene companion feedback as public-safe issue drafts without implying anti-tangent owns CodeScene runtime behavior.

**Acceptance criteria:**
- Draft files exist under `docs/feedback/codescene/`.
- Drafts strip private ticket IDs, product names, internal file/class/function names, and private commit identifiers.
- Drafts preserve public tool names and the reproducible shape of each issue.
- Drafts cover already-degraded-file delta emphasis, per-task attribution/ledger, pasteable companion envelope, and test-file quiet behavior.
- `INTEGRATION.md` links to the drafts as upstream feedback examples.

**Non-goals:**
- Do not create issues in the CodeScene repository unless the user explicitly asks.
- Do not include consumer code snippets.
- Do not claim CodeScene behavior has changed.

**Context:**
This repository is public. Drafts must be anonymized and useful without leaking consumer details.

**Files:**
- Create: `docs/feedback/codescene/already-degraded-file-deltas.md`
- Create: `docs/feedback/codescene/per-task-attribution-ledger.md`
- Create: `docs/feedback/codescene/pasteable-companion-envelope.md`
- Create: `docs/feedback/codescene/test-file-classification.md`
- Modify: `INTEGRATION.md`

- [ ] **Step 1: Create directory and already-degraded-file draft**

Before creating files, verify parent exists: `ls docs`

Create `docs/feedback/codescene/already-degraded-file-deltas.md`:

```markdown
# CodeScene MCP feedback: foreground deltas on already-degraded files

## Summary

When `pre_commit_code_health_safeguard` runs on a file that was already over a quality threshold before the current task, the top-level gate can remain `failed` even when the current task adds only a small delta. The implementer needs the per-finding `value` vs `value_before` delta foregrounded more strongly than the binary gate.

## Reproduction shape

1. Start from a branch where a large source file is already over one or more Code Health thresholds.
2. Make a small, localized change that increments one or two metrics but does not introduce a new threshold crossing.
3. Run `pre_commit_code_health_safeguard` on the changed file.

## Observed friction

The top-level gate says `failed`, which is technically correct but not task-specific. The actionable information is the per-finding delta: the file was already failing, and this task changed metric values by a small amount.

## Suggested improvement

When a file was already over threshold before the current change, surface a `degraded` or `already_failed_with_delta` framing and put `value_before`, `value`, and threshold-crossing status first in the result.
```

- [ ] **Step 2: Add per-task attribution draft**

Create `docs/feedback/codescene/per-task-attribution-ledger.md`:

```markdown
# CodeScene MCP feedback: per-task attribution ledger

## Summary

For multi-task implementation branches that repeatedly touch one hotspot file, controllers need a simple way to attribute Code Health deltas to the task or checkpoint that introduced them.

## Reproduction shape

1. Execute a multi-task plan where several tasks touch the same large file.
2. Run `analyze_change_set` after each task.
3. Compare file-level findings across task boundaries.

## Suggested helper

Offer a pasteable ledger row or helper output with: task label, commit or checkpoint, file, finding name, `value_before`, `value`, threshold, and whether this task crossed the threshold.

## Why this helps

The branch-level result is useful before merge, but controllers need per-task attribution during subagent-driven development to decide which task introduced a regression and whether it is in scope to remediate immediately.
```

- [ ] **Step 3: Add companion envelope draft**

Create `docs/feedback/codescene/pasteable-companion-envelope.md`:

````markdown
# CodeScene MCP feedback: pasteable companion envelope

## Summary

Anti-tangent emits a paste-ready `summary_block` that implementers can include verbatim in DONE reports. CodeScene MCP results are structured, but implementers currently summarize them manually.

## Suggested output shape

```text
codescene envelope
  tool:          analyze_change_set
  base_ref:      main
  quality_gate:  passed
  findings:      0 total
  key_deltas:    none
  next_action:   no Code Health regressions detected
```

## Why this helps

A consistent envelope would make multi-tool DONE reports easier to scan and would reduce manual reformatting differences between implementers.
````

- [ ] **Step 4: Add test-file classification draft**

Create `docs/feedback/codescene/test-file-classification.md`:

```markdown
# CodeScene MCP feedback: document test-file classification expectations

## Summary

Field use showed CodeScene staying quiet on a large test-only addition with many test fixtures and mocks. That quiet behavior is useful and worth documenting so callers do not skip CodeScene checks on test-heavy tasks unnecessarily.

## Suggested documentation

Document how test files are classified and when large test files are expected to trigger Code Health findings. If test files are intentionally scored differently from production files, state that directly.

## Why this helps

Controllers can keep deterministic CodeScene checks enabled for test-heavy work without fearing predictable false positives from normal test setup patterns.
```

- [ ] **Step 5: Link drafts from integration docs**

Add to the CodeScene companion section in `INTEGRATION.md`:

```markdown
The repository also keeps anonymized upstream-feedback drafts under `docs/feedback/codescene/`. These are public-safe issue bodies for CodeScene maintainers; they document companion-tool friction but do not change anti-tangent runtime behavior.
```

- [ ] **Step 6: Public hygiene grep**

Run the public-hygiene grep for ticket-like IDs and private-identifier placeholder phrases against `docs/feedback/codescene`.

Expected: no matches.

- [ ] **Step 7: Commit task 8**

```bash
git add docs/feedback/codescene INTEGRATION.md
git commit -m "docs: draft CodeScene companion feedback" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Final verification and release readiness

**Goal:** The complete 0.4.0 change set is tested, documented, and ready for PR review.

**Acceptance criteria:**
- `go test -race ./...` passes.
- `go build ./...` passes.
- Prompt golden files are intentionally updated and reviewed.
- `CHANGELOG.md` has `## [0.4.0] - 2026-05-17`.
- Branch name is `version/0.4.0` before opening the implementation PR.
- The final diff contains no private consumer identifiers.

**Non-goals:**
- Do not create a GitHub release.
- Do not push or create a PR unless explicitly requested.
- Do not open upstream CodeScene issues unless explicitly requested.

**Context:**
This repo expects `go test -race ./...` as the mainline verification command.

**Files:**
- Verify: all files changed by Tasks 1-8

- [ ] **Step 1: Run full tests**

Run: `go test -race ./...`

Expected: PASS for all packages.

- [ ] **Step 2: Run build**

Run: `go build ./...`

Expected: exit 0 with no output.

- [ ] **Step 3: Run public hygiene grep**

Run the public-hygiene grep for ticket-like IDs and private-identifier placeholder phrases against `README.md`, `INTEGRATION.md`, `CHANGELOG.md`, `docs/feedback`, `docs/superpowers/plans`, and `docs/superpowers/specs`.

Expected: no matches except historical spec/plan files that predate this 0.4.0 work. If matches appear in new 0.4.0 files, remove or anonymize them.

- [ ] **Step 4: Inspect diff**

Run: `git diff --stat main...HEAD`

Expected: shows only intended Go, schema, prompt, docs, and changelog changes.

- [ ] **Step 5: Commit final fixes if needed**

If verification required fixes, commit them:

```bash
git add .
git commit -m "fix: address feedback improvements verification" \
  -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

If no fixes were needed, do not create an empty commit.
