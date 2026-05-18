# anti-tangent-mcp v0.5.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the four new optional `validate_task_spec` inputs, the new `convention_deviation` finding category, and the hybrid `exit_contracts` + `normative_test_bodies` flow specified in [`docs/superpowers/specs/2026-05-18-mcp-feedback-v0.5.0-design.md`](../specs/2026-05-18-mcp-feedback-v0.5.0-design.md), plus the paired prompt-template tunes, INTEGRATION.md doc additions, and CHANGELOG entry, on branch `version/0.5.0`.

**Architecture:** All changes are additive to the existing four-tool MCP surface. Go-side input normalization mirrors the established `pinned_by` / `controller_verified_references` shape (one helper per field, rune-based caps). New reviewer-output enums land in all four JSON schemas (`schema.json`, `plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`). Server-deterministic markdown extraction for `normative_test_bodies` runs in a new `internal/planparser` helper called from `ValidatePlan` before the reviewer prompt is rendered. Prompt-template changes regenerate golden files in `internal/prompts/testdata/`.

**Tech Stack:** Go 1.x; `text/template` embedded prompts; JSON Schema (draft-07 subset); `testify` for unit tests; `httptest` for provider-shaped tests; `golden`-file fixtures regenerated with `go test ./internal/prompts/... -update`.

---

## Execution notes (for the future executor)

- **Do NOT call `validate_plan` on this plan.** This plan modifies `plan.tmpl`, `plan_tasks_chunk.tmpl`, `plan_schema.json`, `tasks_only_schema.json`, the `PlanTaskResult` struct, and the `planparser` package — exactly the surfaces `validate_plan` depends on. See the user-memory note `skip-validate-plan-when-fixing-it.md`.
- **Per-task hooks DO still apply** (`validate_task_spec` at task start, `validate_completion` before DONE), with two carve-outs:
  - **Task 1** modifies the reviewer-output schemas and the parser's `validCategory` allowlist. The schema/parser changes are what `validate_task_spec`'s post-reviewer normalization depends on. After Task 1 lands, `validate_task_spec` on later tasks will work normally.
  - **Task 3** modifies `pre.tmpl` itself. The reviewer prompt used by `validate_task_spec` is exactly the template Task 3 is editing. Run `validate_task_spec` for Task 3 *before* editing the template; on the post-edit re-run if you need one, expect golden-test churn rather than reviewer-finding churn.
  - **Task 6** modifies `plan.tmpl` / `plan_tasks_chunk.tmpl` — only consumed by `validate_plan`, which we are already skipping.
  - **Task 7** modifies `post.tmpl` — the template `validate_completion` itself uses. Same carve-out as Task 3: validate Task 7 before editing the template; treat post-edit re-runs as smoke checks for the template-rendering golden tests rather than reviewer-finding signal.
- **CHANGELOG enforcement:** CI requires a `## [0.5.0] - YYYY-MM-DD` heading in `CHANGELOG.md` matching the branch name. Task 1 adds that heading; each subsequent task appends its own entry under it. Use today's actual date in the heading.
- **`go test -race ./...` is the mainline command.** Run it after each task. E2E tests (`-tags=e2e`) hit real provider APIs and are NOT required per-task; they are not exercised in this plan.
- **Golden-file workflow:** when a task changes a prompt template, the order is (1) write/extend `Contains`-style assertions on the new sections in `prompts_test.go`, (2) run tests and confirm they fail, (3) edit the template, (4) run `go test ./internal/prompts/... -update` to regenerate goldens, (5) inspect the diff, (6) re-run without `-update` to confirm pass.
- **No `git push` and no `git merge` in this plan.** The work lands as commits on `version/0.5.0`; merging is a separate decision after the branch is complete.

---

## File structure

New files:
- `internal/planparser/normative_bodies.go` — server-deterministic extraction of `**NORMATIVE TEST BODIES (verbatim):**` sections from per-task plan markdown.
- `internal/planparser/normative_bodies_test.go` — unit tests for the extractor (header detection, fenced-block boundaries, paragraph fallback, truncation marker, entry-count cap).

Modified files (high-traffic):
- `internal/verdict/verdict.go` — add `CategoryConventionDeviation` constant + register in `validCategory`.
- `internal/verdict/parser.go` — extend severity-floor logic so `convention_deviation` is floored to `minor` (matching `unverifiable_codebase_claim`).
- `internal/verdict/plan_parser.go` — same flooring in `validateFinding` (used by `ParsePlan`, `ParsePlanFindingsOnly`, `ParseTasksOnly`).
- `internal/verdict/plan.go` — extend `PlanTaskResult` with `ExitContracts []string`, `ExitContractsInferred bool`, `NormativeTestBodies []string` (all `omitempty`).
- `internal/verdict/schema.json` — add `convention_deviation` to the `findings[].category` enum.
- `internal/verdict/plan_schema.json` — add `convention_deviation` to the enum AND add the three new optional task-item fields to `properties` (not `required`); keep `additionalProperties: false`.
- `internal/verdict/tasks_only_schema.json` — same as `plan_schema.json`.
- `internal/verdict/plan_findings_only_schema.json` — add `convention_deviation` to the enum only.
- `internal/mcpsrv/handlers.go` — extend `ValidateTaskSpecArgs` with the four new optional `[]string` fields; extend `ValidateCompletionArgs` with `ExitContracts []string` and `ExitContractsInferred bool`; wire the new inputs through the respective handlers; wire server-side `normative_test_bodies` extraction into `ValidatePlan` results.
- `internal/mcpsrv/task_spec_input.go` — add new caps (`maxNormativeTestBodyEntries`, `maxNormativeTestBodyChars`); add per-field normalize helpers; extend `taskSpecInputs` struct and `normalizeTaskSpecInputs`.
- `internal/mcpsrv/task_spec_normalize.go` — add deterministic substring suppression for `scope_drift` findings whose evidence names a `testability_extractions` entry.
- `internal/session/session.go` — extend `TaskSpec` with the four new `[]string` fields so the prompt template can read them off the spec.
- `internal/prompts/prompts.go` — extend `PostInput` with `ExitContracts []string` and `ExitContractsInferred bool`; the `PreInput.Spec` channel already carries the four new `[]string` fields once `session.TaskSpec` is extended.
- `internal/prompts/templates/pre.tmpl` — render `Normative test bodies (caller-supplied, treat as binding AC):`, `Codebase conventions (caller-supplied):`, `Testability extractions (caller-supplied):`, and `Test strategy notes (caller-supplied):` sections; add reviewer guidance for each.
- `internal/prompts/templates/post.tmpl` — render provenance-aware `Exit contracts (...):` section + reviewer guidance.
- `internal/prompts/templates/plan.tmpl` — instruct reviewer to populate `exit_contracts` + `exit_contracts_inferred` per task, respecting explicit `**Exit contracts:**` sections.
- `internal/prompts/templates/plan_tasks_chunk.tmpl` — same.
- `internal/prompts/testdata/pre_basic.golden`, `post_basic.golden`, `plan_basic.golden`, `plan_basic_quick.golden`, `plan_tasks_chunk.golden`, `plan_tasks_chunk_quick.golden` — regenerated via `-update`.
- `INTEGRATION.md` — D1–D5 doc additions.
- `CHANGELOG.md` — new `## [0.5.0] - YYYY-MM-DD` block, populated incrementally across tasks.

---

### Task 1: Add `convention_deviation` finding category + JSON schema enums + severity floor

**Goal:** Introduce the new reviewer-output finding category `convention_deviation` end-to-end — Go constant, parser severity floor (minor-only, mirroring `unverifiable_codebase_claim`), and inclusion in all four reviewer-output JSON schema `category` enums — without changing any existing call site's behavior.

**Acceptance criteria:**
- `verdict.CategoryConventionDeviation` is defined in `internal/verdict/verdict.go` with value `"convention_deviation"`, and `validCategory` returns `true` for it.
- `verdict.Parse` floors any `convention_deviation` finding's severity to `minor`, even when the reviewer emits `critical` or `major` (mirroring the existing `unverifiable_codebase_claim` floor).
- `verdict.ParsePlan`, `verdict.ParsePlanFindingsOnly`, and `verdict.ParseTasksOnly` apply the same floor (via `validateFinding`).
- All four schema files (`schema.json`, `plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`) accept `"convention_deviation"` in `findings[].category` and continue to reject unknown values (`additionalProperties: false` preserved).
- `CategoryMalformedEvidence` (server-only) remains rejected by `validCategory` — proof that the new category did not loosen the allowlist.
- `CHANGELOG.md` has a new `## [0.5.0] - <today>` heading (Keep-a-Changelog format) with an `### Added` bullet describing the new category.
- `go test -race ./...` passes.

**Non-goals:**
- Wiring `codebase_conventions` input (that's Task 2 + Task 3).
- Reviewer prompt updates that ask the reviewer to *emit* `convention_deviation` (that's Task 3).

**Context:**
The existing pattern for a minor-floored category is `CategoryUnverifiableCodebaseClaim` (defined in `internal/verdict/verdict.go:33-39`, floored in `internal/verdict/parser.go:42-47` and `internal/verdict/plan_parser.go:78-80`). Mirror that exactly. The reviewer-output schemas embed the category enum identically across all four files; keep them in lockstep.

**Files:**
- Modify: `internal/verdict/verdict.go`
- Modify: `internal/verdict/parser.go`
- Modify: `internal/verdict/plan_parser.go`
- Modify: `internal/verdict/schema.json`
- Modify: `internal/verdict/plan_schema.json`
- Modify: `internal/verdict/tasks_only_schema.json`
- Modify: `internal/verdict/plan_findings_only_schema.json`
- Test: `internal/verdict/parser_test.go` (extend)
- Test: `internal/verdict/plan_test.go` (extend) — new test for plan-side flooring
- Test: `internal/verdict/edge_test.go` (extend) — new test asserting all four schemas accept `convention_deviation`
- Modify: `CHANGELOG.md`

---

- [ ] **Step 1: Add failing test for `verdict.Parse` flooring of `convention_deviation`.**

Append to `internal/verdict/parser_test.go`:

```go
func TestParse_ConventionDeviation_SeverityFloorToMinor(t *testing.T) {
	// Reviewer emits a `major` convention_deviation — server should floor to
	// `minor`, matching the unverifiable_codebase_claim pattern.
	raw := []byte(`{
		"verdict": "warn",
		"findings": [{
			"severity":   "major",
			"category":   "convention_deviation",
			"criterion":  "codebase_convention",
			"evidence":   "spec serializes the id as String at the in-memory layer",
			"suggestion": "Use UUID in memory; serialize to String only at the persistence boundary."
		}],
		"next_action": "Address the convention before dispatch."
	}`)

	r, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(r.Findings))
	}
	if got, want := r.Findings[0].Severity, SeverityMinor; got != want {
		t.Errorf("severity = %q, want %q (server should floor)", got, want)
	}
	if got, want := r.Findings[0].Category, CategoryConventionDeviation; got != want {
		t.Errorf("category = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Add failing test for plan-side flooring of `convention_deviation`.**

Append to `internal/verdict/plan_test.go`:

```go
func TestParsePlan_ConventionDeviation_SeverityFloorToMinor(t *testing.T) {
	raw := []byte(`{
		"plan_verdict": "warn",
		"plan_findings": [{
			"severity":   "critical",
			"category":   "convention_deviation",
			"criterion":  "codebase_convention",
			"evidence":   "Task 3 stores id as String at the in-memory layer",
			"suggestion": "Use UUID in memory."
		}],
		"tasks": [],
		"next_action": "Address the convention deviation.",
		"plan_quality": "actionable"
	}`)
	r, err := ParsePlan(raw)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if got, want := r.PlanFindings[0].Severity, SeverityMinor; got != want {
		t.Errorf("plan-side severity = %q, want %q (server should floor)", got, want)
	}
}
```

- [ ] **Step 3: Add failing schema acceptance test for all four reviewer-output schemas.**

Append to `internal/verdict/edge_test.go` (or create a new `schema_test.go` if the file is awkward to extend; this plan assumes append-to-edge_test.go since the file already exists):

```go
func TestSchemas_AcceptConventionDeviationCategory(t *testing.T) {
	// All four reviewer-output schemas must include "convention_deviation"
	// in their findings[].category enum. The test is a substring assertion;
	// JSON-Schema validation of round-trip parsing is covered by the parser
	// tests above. We assert here that the enum text is present so a future
	// schema edit cannot silently drop the value.
	schemas := map[string][]byte{
		"schema.json":                    Schema(),
		"plan_schema.json":               PlanSchema(),
		"tasks_only_schema.json":         TasksOnlySchema(),
		"plan_findings_only_schema.json": PlanFindingsOnlySchema(),
	}
	for name, body := range schemas {
		if !strings.Contains(string(body), `"convention_deviation"`) {
			t.Errorf("%s: missing \"convention_deviation\" in category enum", name)
		}
		// Sanity: additionalProperties:false is still enforced (no regression).
		if !strings.Contains(string(body), `"additionalProperties": false`) {
			t.Errorf("%s: lost additionalProperties:false", name)
		}
	}
}
```

If `internal/verdict/edge_test.go` does not already import `"strings"`, add it.

- [ ] **Step 4: Run tests to verify they fail.**

Run: `go test -race ./internal/verdict/...`
Expected: three test failures naming `CategoryConventionDeviation` undefined / category enum missing.

- [ ] **Step 5: Add the `CategoryConventionDeviation` constant.**

Edit `internal/verdict/verdict.go`. After the `CategoryUnverifiableCodebaseClaim` block (around line 39), add:

```go
	// CategoryConventionDeviation is emitted by the reviewer when a caller-
	// supplied codebase_conventions entry conflicts with the spec text — for
	// example, when the spec implies a type or identifier choice that
	// contradicts a stated module convention. Parser-side severity floor (see
	// Parse / validateFinding) forces these findings to SeverityMinor — the
	// reviewer can't know whether the implementation will actually deviate,
	// only that the spec suggests it might.
	CategoryConventionDeviation Category = "convention_deviation"
```

- [ ] **Step 6: Add `CategoryConventionDeviation` to `validCategory`.**

Edit `internal/verdict/parser.go`. In `validCategory` (around line 57-64), extend the switch:

```go
func validCategory(c Category) bool {
	switch c {
	case CategoryMissingAC, CategoryScopeDrift, CategoryAmbiguousSpec,
		CategoryUnaddressed, CategoryQuality, CategorySessionMissing,
		CategoryTooLarge, CategoryUnverifiableCodebaseClaim,
		CategoryConventionDeviation, CategoryOther:
		return true
	}
	return false
}
```

- [ ] **Step 7: Apply the per-task severity floor in `verdict.Parse`.**

Edit `internal/verdict/parser.go`. In the per-finding loop in `Parse` (around line 32-48), extend the existing flooring branch:

```go
		// Severity floor: unverifiable_codebase_claim and convention_deviation
		// findings are always minor — the reviewer can't know if the claim/
		// deviation is wrong, only that it can't fully verify.
		if f.Category == CategoryUnverifiableCodebaseClaim && f.Severity != SeverityMinor {
			r.Findings[i].Severity = SeverityMinor
		}
		if f.Category == CategoryConventionDeviation && f.Severity != SeverityMinor {
			r.Findings[i].Severity = SeverityMinor
		}
```

- [ ] **Step 8: Apply the plan-side severity floor in `validateFinding`.**

Edit `internal/verdict/plan_parser.go`. In `validateFinding` (around line 69-82), extend the floor:

```go
	if f.Category == CategoryUnverifiableCodebaseClaim && f.Severity != SeverityMinor {
		f.Severity = SeverityMinor
	}
	if f.Category == CategoryConventionDeviation && f.Severity != SeverityMinor {
		f.Severity = SeverityMinor
	}
```

- [ ] **Step 9: Add `"convention_deviation"` to all four reviewer-output schemas.**

Edit `internal/verdict/schema.json`, `internal/verdict/plan_schema.json`, `internal/verdict/tasks_only_schema.json`, `internal/verdict/plan_findings_only_schema.json`. In each file's `findings[].category.enum` array (under `plan_schema.json` and `tasks_only_schema.json` this lives under `definitions.finding.properties.category.enum`), add `"convention_deviation"` as the last entry before `"other"`:

```json
            "enum": [
              "missing_acceptance_criterion",
              "scope_drift",
              "ambiguous_spec",
              "unaddressed_finding",
              "quality",
              "session_not_found",
              "payload_too_large",
              "unverifiable_codebase_claim",
              "convention_deviation",
              "other"
            ]
```

Make the same edit in all four schemas. Order matters only for readability, not for validation — but keeping the four enums in lockstep matters for future maintenance.

- [ ] **Step 10: Run tests to verify they pass.**

Run: `go test -race ./internal/verdict/...`
Expected: PASS (all three new tests plus the existing suite).

- [ ] **Step 11: Add the 0.5.0 CHANGELOG header and the first entry.**

Edit `CHANGELOG.md`. Insert a new block above the existing `## [0.4.0] - 2026-05-17` heading. Substitute today's actual date for `YYYY-MM-DD`:

```markdown
## [0.5.0] - YYYY-MM-DD

### Added
- New finding category `convention_deviation` (minor-floored) emitted when a `codebase_conventions` entry conflicts with the spec. Added to the reviewer-output JSON schema category enums.

### Changed

### Fixed

### Removed

### Deprecated

### Security

```

The empty subsections are placeholders — later tasks fill them in. Keep them present so the format matches the Keep-a-Changelog convention used by prior releases.

- [ ] **Step 12: Run the full test suite.**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 13: Commit.**

```bash
git add internal/verdict/verdict.go internal/verdict/parser.go internal/verdict/plan_parser.go internal/verdict/schema.json internal/verdict/plan_schema.json internal/verdict/tasks_only_schema.json internal/verdict/plan_findings_only_schema.json internal/verdict/parser_test.go internal/verdict/plan_test.go internal/verdict/edge_test.go CHANGELOG.md
git commit -m "feat(verdict): add convention_deviation finding category"
```

---

### Task 2: Add four new optional `validate_task_spec` inputs (Go struct + normalization)

**Goal:** Accept `test_strategy_notes`, `codebase_conventions`, `testability_extractions`, and `normative_test_bodies` as optional `[]string` arguments on `validate_task_spec`, normalize them under two distinct cap regimes, and store them on `session.TaskSpec` so the prompt template can read them.

**Acceptance criteria:**
- `mcpsrv.ValidateTaskSpecArgs` exposes the four new optional `[]string` fields with `json:"...,omitempty"` tags matching the spec's snake_case names.
- `session.TaskSpec` exposes the same four fields (so the rendered prompt template can read them).
- `internal/mcpsrv/task_spec_input.go` defines two new caps: `maxNormativeTestBodyEntries = 20` and `maxNormativeTestBodyChars = 4000`. The other three fields reuse the existing `maxPinnedByEntries = 50` and `maxPinnedByChars = 500`.
- Per-field normalize helpers trim whitespace, drop empty entries, and return a Go error naming the field when caps are exceeded (matching the existing `normalizePinnedBy` shape).
- `normalizeTaskSpecInputs` returns the four normalized slices on the `taskSpecInputs` struct alongside the existing fields, and `ValidateTaskSpec` writes them onto the created `session.TaskSpec`.
- Unit tests cover, for each of the four fields independently: trim-and-drop, entry-count cap at `N` (pass) and `N+1` (fail with field-named error), per-entry char cap at `M` runes (pass), `M+1` runes (fail), and `M` multibyte runes (pass — cap is rune-based, not byte-based).
- Existing handler tests (HappyPath, RollsUpUnverifiableFindings, PinnedBy*, ControllerVerifiedReferences*) still pass unchanged.
- CHANGELOG `### Added` bullet appended.
- `go test -race ./...` passes.

**Non-goals:**
- Rendering the new fields in `pre.tmpl` (Task 3).
- `scope_drift` suppression for `testability_extractions` (Task 4).
- Server-side `normative_test_bodies` extraction from plan markdown (Task 5).

**Context:**
Two cap regimes are deliberate: `normative_test_bodies` carries verbatim test code (longer than the prose attestations in the other three fields). Per spec §1 normalization: 20 entries × 4000 runes = 80KB worst case per field, well inside the 200KB payload limit. The other three reuse the existing `pinned_by` / `controller_verified_references` regime (50 × 500). All four are validated in Go (caller-input), not in JSON schema files — see spec §1 "Input validation."

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (extend `ValidateTaskSpecArgs`, write fields onto `session.TaskSpec` in `ValidateTaskSpec`)
- Modify: `internal/mcpsrv/task_spec_input.go` (new caps, new normalize helpers, extended `taskSpecInputs`, extended `normalizeTaskSpecInputs`)
- Modify: `internal/session/session.go` (extend `TaskSpec`)
- Test: `internal/mcpsrv/handlers_test.go` (extend) — boundary tests per field
- Modify: `CHANGELOG.md`

---

- [ ] **Step 1: Add failing trim-and-store test for `test_strategy_notes`.**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_TestStrategyNotesTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: []string{"  AC #2 covered jointly by tests A and B  ", "", "   ", "AC #3 negative case split across X/Y"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"AC #2 covered jointly by tests A and B", "AC #3 negative case split across X/Y"}, sess.Spec.TestStrategyNotes)
}

func TestValidateTaskSpec_TestStrategyNotesLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "joint"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test_strategy_notes must contain at most 50 entries")

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test_strategy_notes[0] must be at most 500 characters")

	// 500 multibyte runes (1000 bytes) must pass — the cap is on runes.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: []string{strings.Repeat("é", 500)},
	})
	require.NoError(t, err)
}
```

- [ ] **Step 2: Add the analogous failing tests for `codebase_conventions` and `testability_extractions`.**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_CodebaseConventionsTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: []string{"  id is canonically UUID in memory  ", "", "Instant fields use @Serializable"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"id is canonically UUID in memory", "Instant fields use @Serializable"}, sess.Spec.CodebaseConventions)
}

func TestValidateTaskSpec_CodebaseConventionsLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "convention"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codebase_conventions must contain at most 50 entries")

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codebase_conventions[0] must be at most 500 characters")
}

func TestValidateTaskSpec_TestabilityExtractionsTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{"  buildDeclineWinddownHandlerOutput  ", "", "runHiringAreaRecheck"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"buildDeclineWinddownHandlerOutput", "runHiringAreaRecheck"}, sess.Spec.TestabilityExtractions)
}

func TestValidateTaskSpec_TestabilityExtractionsLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "helper"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "testability_extractions must contain at most 50 entries")

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "testability_extractions[0] must be at most 500 characters")
}
```

- [ ] **Step 3: Add the failing test for `normative_test_bodies` (different cap regime: 20 / 4000).**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_NormativeTestBodiesTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: []string{"  @Test fun whenX_thenY() { ... }  ", "", "// excerpt: see plan §3 test 2"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"@Test fun whenX_thenY() { ... }", "// excerpt: see plan §3 test 2"}, sess.Spec.NormativeTestBodies)
}

func TestValidateTaskSpec_NormativeTestBodiesLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	// 20 entries pass, 21 fail.
	twenty := make([]string, 20)
	for i := range twenty {
		twenty[i] = "@Test fun t" + strings.Repeat("x", 1) + "() {}"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: twenty,
	})
	require.NoError(t, err)

	twentyOne := append([]string(nil), twenty...)
	twentyOne = append(twentyOne, "@Test fun extra() {}")
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: twentyOne,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "normative_test_bodies must contain at most 20 entries")

	// 4000 runes pass, 4001 fail.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: []string{strings.Repeat("x", 4000)},
	})
	require.NoError(t, err)

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: []string{strings.Repeat("x", 4001)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "normative_test_bodies[0] must be at most 4000 characters")

	// 4000 multibyte runes must pass — cap is rune-based, not byte-based.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: []string{strings.Repeat("é", 4000)},
	})
	require.NoError(t, err)
}
```

- [ ] **Step 4: Run the new tests to verify they fail.**

Run: `go test -race ./internal/mcpsrv/... -run TestValidateTaskSpec_TestStrategyNotes -v`
Expected: compile error (unknown field `TestStrategyNotes` on `ValidateTaskSpecArgs`).

- [ ] **Step 5: Extend `ValidateTaskSpecArgs` in `internal/mcpsrv/handlers.go`.**

Edit `internal/mcpsrv/handlers.go` (around line 40-51). Add the four new optional fields:

```go
type ValidateTaskSpecArgs struct {
	TaskTitle                    string   `json:"task_title"           jsonschema:"required"`
	Goal                         string   `json:"goal"                 jsonschema:"required"`
	AcceptanceCriteria           []string `json:"acceptance_criteria,omitempty"`
	NonGoals                     []string `json:"non_goals,omitempty"`
	Context                      string   `json:"context,omitempty"`
	PinnedBy                     []string `json:"pinned_by,omitempty"`
	ControllerVerifiedReferences []string `json:"controller_verified_references,omitempty"`
	TestStrategyNotes            []string `json:"test_strategy_notes,omitempty"`
	CodebaseConventions          []string `json:"codebase_conventions,omitempty"`
	TestabilityExtractions       []string `json:"testability_extractions,omitempty"`
	NormativeTestBodies          []string `json:"normative_test_bodies,omitempty"`
	Phase                        string   `json:"phase,omitempty"`
	ModelOverride                string   `json:"model_override,omitempty"`
	MaxTokensOverride            int      `json:"max_tokens_override,omitempty"`
}
```

- [ ] **Step 6: Extend `session.TaskSpec`.**

Edit `internal/session/session.go`. Add the four fields:

```go
type TaskSpec struct {
	Title                        string   `json:"title"`
	Goal                         string   `json:"goal"`
	AcceptanceCriteria           []string `json:"acceptance_criteria,omitempty"`
	NonGoals                     []string `json:"non_goals,omitempty"`
	Context                      string   `json:"context,omitempty"`
	PinnedBy                     []string `json:"pinned_by,omitempty"`
	ControllerVerifiedReferences []string `json:"controller_verified_references,omitempty"`
	TestStrategyNotes            []string `json:"test_strategy_notes,omitempty"`
	CodebaseConventions          []string `json:"codebase_conventions,omitempty"`
	TestabilityExtractions       []string `json:"testability_extractions,omitempty"`
	NormativeTestBodies          []string `json:"normative_test_bodies,omitempty"`
	Phase                        string   `json:"phase,omitempty"`
}
```

- [ ] **Step 7: Add caps and normalize helpers in `internal/mcpsrv/task_spec_input.go`.**

Edit `internal/mcpsrv/task_spec_input.go`. Below the existing const block (around line 11-14), add the new caps:

```go
const (
	maxPinnedByEntries = 50
	maxPinnedByChars   = 500

	maxNormativeTestBodyEntries = 20
	maxNormativeTestBodyChars   = 4000
)
```

Below `normalizeControllerVerifiedReferences`, add four new helpers. The first three reuse the standard regime; `normalizeNormativeTestBodies` uses the larger regime:

```go
func normalizeTestStrategyNotes(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("test_strategy_notes[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("test_strategy_notes must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

func normalizeCodebaseConventions(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("codebase_conventions[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("codebase_conventions must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

func normalizeTestabilityExtractions(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("testability_extractions[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("testability_extractions must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

func normalizeNormativeTestBodies(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxNormativeTestBodyChars {
			return nil, fmt.Errorf("normative_test_bodies[%d] must be at most %d characters", i, maxNormativeTestBodyChars)
		}
		out = append(out, trimmed)
		if len(out) > maxNormativeTestBodyEntries {
			return nil, fmt.Errorf("normative_test_bodies must contain at most %d entries", maxNormativeTestBodyEntries)
		}
	}
	return out, nil
}
```

- [ ] **Step 8: Extend `taskSpecInputs` and `normalizeTaskSpecInputs`.**

Edit `internal/mcpsrv/task_spec_input.go`. Replace the existing `taskSpecInputs` struct and `normalizeTaskSpecInputs` function (around line 67-91) with:

```go
type taskSpecInputs struct {
	Phase                        string
	PinnedBy                     []string
	ControllerVerifiedReferences []string
	TestStrategyNotes            []string
	CodebaseConventions          []string
	TestabilityExtractions       []string
	NormativeTestBodies          []string
}

func normalizeTaskSpecInputs(args ValidateTaskSpecArgs) (taskSpecInputs, error) {
	phase, err := normalizePhase(args.Phase)
	if err != nil {
		return taskSpecInputs{}, err
	}
	pinnedBy, err := normalizePinnedBy(args.PinnedBy)
	if err != nil {
		return taskSpecInputs{}, err
	}
	controllerVerifiedReferences, err := normalizeControllerVerifiedReferences(args.ControllerVerifiedReferences)
	if err != nil {
		return taskSpecInputs{}, err
	}
	testStrategyNotes, err := normalizeTestStrategyNotes(args.TestStrategyNotes)
	if err != nil {
		return taskSpecInputs{}, err
	}
	codebaseConventions, err := normalizeCodebaseConventions(args.CodebaseConventions)
	if err != nil {
		return taskSpecInputs{}, err
	}
	testabilityExtractions, err := normalizeTestabilityExtractions(args.TestabilityExtractions)
	if err != nil {
		return taskSpecInputs{}, err
	}
	normativeTestBodies, err := normalizeNormativeTestBodies(args.NormativeTestBodies)
	if err != nil {
		return taskSpecInputs{}, err
	}
	return taskSpecInputs{
		Phase:                        phase,
		PinnedBy:                     pinnedBy,
		ControllerVerifiedReferences: controllerVerifiedReferences,
		TestStrategyNotes:            testStrategyNotes,
		CodebaseConventions:          codebaseConventions,
		TestabilityExtractions:       testabilityExtractions,
		NormativeTestBodies:          normativeTestBodies,
	}, nil
}
```

- [ ] **Step 9: Write the new fields onto the session in `ValidateTaskSpec`.**

Edit `internal/mcpsrv/handlers.go` (around line 87-96, the `spec := session.TaskSpec{...}` block). Extend it:

```go
	spec := session.TaskSpec{
		Title:                        args.TaskTitle,
		Goal:                         args.Goal,
		AcceptanceCriteria:           args.AcceptanceCriteria,
		NonGoals:                     args.NonGoals,
		Context:                      args.Context,
		PinnedBy:                     inputs.PinnedBy,
		ControllerVerifiedReferences: inputs.ControllerVerifiedReferences,
		TestStrategyNotes:            inputs.TestStrategyNotes,
		CodebaseConventions:          inputs.CodebaseConventions,
		TestabilityExtractions:       inputs.TestabilityExtractions,
		NormativeTestBodies:          inputs.NormativeTestBodies,
		Phase:                        inputs.Phase,
	}
```

- [ ] **Step 10: Run the new + existing tests to verify they pass.**

Run: `go test -race ./internal/mcpsrv/... ./internal/session/... ./internal/verdict/...`
Expected: PASS.

- [ ] **Step 11: Append a CHANGELOG `### Added` bullet under the 0.5.0 block.**

Edit `CHANGELOG.md`. Under the `### Added` subsection of the `## [0.5.0]` block, add:

```markdown
- `validate_task_spec` accepts optional `test_strategy_notes`, `codebase_conventions`, `testability_extractions`, and `normative_test_bodies` so controllers can surface joint-coverage intent, module conventions, intentional testability extractions, and binding test bodies that the structured-fields-only spec otherwise hides from the reviewer.
```

- [ ] **Step 12: Run the full test suite.**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 13: Commit.**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/task_spec_input.go internal/session/session.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "feat(mcpsrv): accept four new optional validate_task_spec inputs"
```

---

### Task 3: Render new `validate_task_spec` fields in `pre.tmpl` + reviewer guidance

**Goal:** Make `pre.tmpl` render the four new caller-supplied sections (test strategy notes, codebase conventions, testability extractions, normative test bodies) with provenance-aware reviewer guidance — and regenerate the golden so it stays the canonical render snapshot.

**Acceptance criteria:**
- When `Spec.TestStrategyNotes` is non-empty, the rendered prompt includes a `Test strategy notes (caller-supplied):` section listing each entry as a bullet.
- When `Spec.CodebaseConventions` is non-empty, the rendered prompt includes a `Codebase conventions (caller-supplied):` section + reviewer instruction to emit `category: convention_deviation` (with `severity: minor`, `criterion: codebase_convention`) only when there is positive evidence of deviation in the spec text — not when the spec is silent.
- When `Spec.TestabilityExtractions` is non-empty, the rendered prompt includes a `Testability extractions (caller-supplied):` section + reviewer instruction to suppress matching `scope_drift` findings by substring (advisory in the prompt; the Go-side suppression lands in Task 4).
- When `Spec.NormativeTestBodies` is non-empty, the rendered prompt includes a `Normative test bodies (caller-supplied, treat as binding AC):` section + reviewer instruction to treat each entry as binding AC and to interpret `// excerpt:` prefixes as partial coverage.
- When any of the four fields is empty, its section is omitted from the rendered prompt (no header, no blank section).
- New golden file `pre_basic.golden` reflects the unchanged "all four empty" rendering — i.e. the golden test for the existing `sampleSpec()` (with none of the four new fields populated) continues to pass after regeneration. (Existing test `TestRenderPre` should still pass with no golden diff, because `sampleSpec()` does not populate the new fields.)
- New `Contains`-style tests assert each section's presence-when-populated and absence-when-empty.
- `go test -race ./...` passes (including all four prompt-template golden tests).
- CHANGELOG `### Changed` bullet appended.

**Non-goals:**
- Deterministic suppression for `testability_extractions` (Task 4 — the Go-side mechanism that complements this prompt-side instruction).
- `validate_completion` template changes (Task 7).
- `validate_plan` template changes (Task 6).

**Context:**
The existing `pre.tmpl` lives at `internal/prompts/templates/pre.tmpl`. The four new sections should be appended after the existing `Controller-verified references:` section (lines 17-20) and before the `## What to evaluate` heading (line 21). Reviewer guidance for each new section should live inline near the existing guidance paragraphs (lines 32-36) so the structure stays "spec → guidance" rather than "all spec, then all guidance." The existing pattern for "render section iff non-empty" is `{{if .Spec.PinnedBy}}...{{end}}` — mirror it.

**Files:**
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/prompts_test.go` (add Contains-style tests)
- Regenerate: `internal/prompts/testdata/pre_basic.golden` (should be unchanged — `sampleSpec()` does not populate the new fields)
- Modify: `CHANGELOG.md`

---

- [ ] **Step 1: Add failing tests for each new section's presence-when-populated and absence-when-empty.**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_WithTestStrategyNotesIncludesGuidance(t *testing.T) {
	spec := sampleSpec()
	spec.TestStrategyNotes = []string{"AC #2 jointly covered by tests A and B"}

	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Test strategy notes (caller-supplied):")
	assert.Contains(t, out.User, "- AC #2 jointly covered by tests A and B")
	assert.Contains(t, out.User, "Do not emit `missing_acceptance_criterion` for joint-coverage gaps")
}

func TestRenderPre_WithoutTestStrategyNotesOmitsSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "Test strategy notes (caller-supplied):")
}

func TestRenderPre_WithCodebaseConventionsIncludesGuidance(t *testing.T) {
	spec := sampleSpec()
	spec.CodebaseConventions = []string{"id is canonically UUID in memory"}

	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Codebase conventions (caller-supplied):")
	assert.Contains(t, out.User, "- id is canonically UUID in memory")
	assert.Contains(t, out.User, "category: convention_deviation")
	assert.Contains(t, out.User, "criterion: codebase_convention")
	assert.Contains(t, out.User, "positive evidence of deviation")
}

func TestRenderPre_WithoutCodebaseConventionsOmitsSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "Codebase conventions (caller-supplied):")
}

func TestRenderPre_WithTestabilityExtractionsIncludesGuidance(t *testing.T) {
	spec := sampleSpec()
	spec.TestabilityExtractions = []string{"buildDeclineWinddownHandlerOutput"}

	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Testability extractions (caller-supplied):")
	assert.Contains(t, out.User, "- buildDeclineWinddownHandlerOutput")
	assert.Contains(t, out.User, "suppress that specific finding")
}

func TestRenderPre_WithoutTestabilityExtractionsOmitsSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "Testability extractions (caller-supplied):")
}

func TestRenderPre_WithNormativeTestBodiesIncludesGuidance(t *testing.T) {
	spec := sampleSpec()
	spec.NormativeTestBodies = []string{"@Test fun t() { ... }"}

	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Normative test bodies (caller-supplied, treat as binding AC):")
	assert.Contains(t, out.User, "@Test fun t() { ... }")
	assert.Contains(t, out.User, "binding test scope")
	assert.Contains(t, out.User, "// excerpt:")
}

func TestRenderPre_WithoutNormativeTestBodiesOmitsSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "Normative test bodies (caller-supplied, treat as binding AC):")
}
```

- [ ] **Step 2: Run the new tests to verify they fail.**

Run: `go test -race ./internal/prompts/... -run TestRenderPre_With -v`
Expected: FAIL with "string does not contain Y" for each new section.

- [ ] **Step 3: Edit `pre.tmpl` to render the four new sections + reviewer guidance.**

Edit `internal/prompts/templates/pre.tmpl`. The structure is preserved; we add four `{{if}}` blocks after the existing `Controller-verified references` block (lines 17-20). Replace the section from the existing `{{end}}` after `Controller-verified references` (line 20) through the `## What to evaluate` heading (line 21) so the file reads:

```text
{{end}}{{if .Spec.TestStrategyNotes}}
Test strategy notes (caller-supplied):
{{range .Spec.TestStrategyNotes}}- {{.}}
{{end}}{{end}}{{if .Spec.CodebaseConventions}}
Codebase conventions (caller-supplied):
{{range .Spec.CodebaseConventions}}- {{.}}
{{end}}{{end}}{{if .Spec.TestabilityExtractions}}
Testability extractions (caller-supplied):
{{range .Spec.TestabilityExtractions}}- {{.}}
{{end}}{{end}}{{if .Spec.NormativeTestBodies}}
Normative test bodies (caller-supplied, treat as binding AC):
{{range .Spec.NormativeTestBodies}}- {{.}}
{{end}}{{end}}
## What to evaluate
```

Then extend the guidance block (after the existing `Controller-verified references` guidance paragraph at line 36) with four new paragraphs — one per new section. Insert them between the existing CVR guidance paragraph (line 36) and the `Severity:` paragraph (line 38). Use this verbatim text:

```text
If a Test strategy notes section is present, treat each entry as authoritative caller context about why test coverage is split. Do not emit `missing_acceptance_criterion` for joint-coverage gaps when an entry explains the split that covers the gap. Continue to emit `missing_acceptance_criterion` when the gap is unrelated (e.g. an AC bullet with zero tests at all, or an invocation-count assertion that is genuinely missing).

If a Codebase conventions section is present, treat each entry as a convention the implementation must follow. When the task spec or surrounding context shows positive evidence of deviation, emit a finding with `category: convention_deviation`, `criterion: codebase_convention`, severity `minor`, evidence quoting the spec text that suggests deviation, and a suggestion that re-states the convention and asks the implementer to confirm or document an exception. Do not emit `convention_deviation` when the spec is silent on the convention — only when there is positive evidence of deviation.

If a Testability extractions section is present, treat each entry as a helper that exists in production code purely so tests can call it directly. When a `scope_drift` finding would name one of these helpers, suppress that specific finding. Continue to emit `scope_drift` for unrelated additions.

If a Normative test bodies section is present, treat each entry as binding test scope — equivalent in authority to a bullet under Acceptance criteria. Do not emit `ambiguous_spec` for "test scope unclear" findings about coverage that one of these bodies already pins. An entry that begins with `// excerpt:` (or an analogous comment marker) is partial — flag invocation-count or assertion-shape gaps only when the excerpt itself reveals them, not when the gap might exist in the omitted portion. Continue to flag invocation-count gaps, missing negative assertions, or unrelated coverage holes when the entry is a complete body.
```

- [ ] **Step 4: Regenerate the golden file.**

Run: `go test ./internal/prompts/... -update`
Expected: tests pass; `internal/prompts/testdata/pre_basic.golden` is rewritten — but since `sampleSpec()` does not populate the four new fields, the file content should be IDENTICAL to before. Inspect the diff:

```bash
git diff internal/prompts/testdata/pre_basic.golden
```

If the diff is non-empty, something other than the four new sections changed — investigate. If the diff is empty, no further action.

- [ ] **Step 5: Run tests without `-update` to verify pass.**

Run: `go test -race ./internal/prompts/...`
Expected: PASS for both the new Contains-style tests and all existing golden tests.

- [ ] **Step 6: Append a CHANGELOG `### Changed` bullet.**

Edit `CHANGELOG.md`. Under the `### Changed` subsection of the `## [0.5.0]` block, add:

```markdown
- `pre.tmpl` treats `normative_test_bodies` as binding AC, treats adjacent complementary tests as joint coverage when `test_strategy_notes` explains the split, emits `convention_deviation` findings on observed deviations from `codebase_conventions`, and respects `testability_extractions` when judging scope drift.
```

- [ ] **Step 7: Run the full test suite.**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 8: Commit.**

```bash
git add internal/prompts/templates/pre.tmpl internal/prompts/prompts_test.go internal/prompts/testdata/pre_basic.golden CHANGELOG.md
git commit -m "feat(prompts): render four new caller-supplied sections in pre.tmpl"
```

---

### Task 4: Deterministic substring suppression for `testability_extractions`

**Goal:** When the reviewer emits a `scope_drift` finding whose evidence names a helper that the caller listed in `testability_extractions`, drop that specific finding at the same place we currently roll up `unverifiable_codebase_claim` findings — server-side, post-reviewer, deterministic substring match in either direction (entry is substring of evidence OR evidence is substring of entry).

**Acceptance criteria:**
- A new helper `suppressTestabilityExtractionScopeDrift(findings []verdict.Finding, extractions []string) []verdict.Finding` lives in `internal/mcpsrv/task_spec_normalize.go` and is called inside `ValidateTaskSpec` between the reviewer-response receipt and the existing unverifiable rollup.
- For each `scope_drift` finding `f`, if any entry `e` in `extractions` satisfies `strings.Contains(f.Evidence, e) || strings.Contains(e, f.Evidence)`, the finding is dropped from the returned slice.
- Non-`scope_drift` findings pass through untouched. `scope_drift` findings whose evidence does not match any extraction also pass through untouched.
- The order in which the rollup and the suppression run matters: suppression runs BEFORE the unverifiable rollup, so a suppressed `scope_drift` cannot accidentally end up in the rolled-up checklist (the rollup is `unverifiable_codebase_claim`-specific, but the ordering keeps the data flow easy to reason about).
- Empty `extractions` (the common case) is a no-op fast path.
- Unit tests cover: (a) match in either direction drops the finding, (b) non-matching `scope_drift` passes through, (c) non-`scope_drift` categories are never dropped, (d) empty extractions is a no-op, (e) interaction with the existing unverifiable rollup is correct.
- `go test -race ./...` passes.
- CHANGELOG `### Changed` bullet appended.

**Non-goals:**
- Reviewer-prompt changes (already done in Task 3).
- Suppressing categories other than `scope_drift`.

**Context:**
The pattern to mirror is `normalizeTaskSpecUnverifiableFindings` in `internal/mcpsrv/task_spec_normalize.go` (the helper that rolls up `unverifiable_codebase_claim`). Both run after the reviewer call inside `ValidateTaskSpec` (handlers.go around line 120). The substring rule is identical to the existing `controller_verified_references` suppression rule — see `internal/prompts/templates/pre.tmpl` line 36 and the corresponding behavior is text-only (reviewer instruction). The new helper here is the Go-side enforcement that pairs with that prompt instruction.

**Files:**
- Modify: `internal/mcpsrv/task_spec_normalize.go`
- Modify: `internal/mcpsrv/handlers.go` (one new call in `ValidateTaskSpec` before the existing rollup)
- Test: `internal/mcpsrv/handlers_test.go` (extend)
- Modify: `CHANGELOG.md`

---

- [ ] **Step 1: Add failing tests for the new suppression behavior.**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_TestabilityExtractionsSuppressScopeDrift(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"minor","category":"scope_drift","criterion":"spec","evidence":"buildDeclineWinddownHandlerOutput is extracted as a top-level helper","suggestion":"keep helpers inline"},
				{"severity":"major","category":"scope_drift","criterion":"spec","evidence":"adds an unrelated retry wrapper","suggestion":"remove the wrapper"},
				{"severity":"minor","category":"ambiguous_spec","criterion":"AC1","evidence":"runHiringAreaRecheck closure semantics unclear","suggestion":"pin the closure"}
			],
			"next_action":"address findings"
		}`),
		Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		AcceptanceCriteria:     []string{"AC1"},
		TestabilityExtractions: []string{"buildDeclineWinddownHandlerOutput", "runHiringAreaRecheck"},
	})
	require.NoError(t, err)

	// The first scope_drift (matches buildDeclineWinddownHandlerOutput) is suppressed.
	// The second scope_drift (unrelated) survives.
	// The ambiguous_spec finding survives even though its evidence names a different
	// extraction — suppression is scope_drift-only.
	require.Len(t, env.Findings, 2)
	assert.Equal(t, verdict.CategoryScopeDrift, env.Findings[0].Category)
	assert.Equal(t, "adds an unrelated retry wrapper", env.Findings[0].Evidence)
	assert.Equal(t, verdict.CategoryAmbiguousSpec, env.Findings[1].Category)
}

func TestValidateTaskSpec_TestabilityExtractionsReverseSubstringDrop(t *testing.T) {
	// Reviewer evidence is a substring of the extraction entry (the inverse
	// match direction). The finding must still be dropped.
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"minor","category":"scope_drift","criterion":"spec","evidence":"buildDecline","suggestion":"keep helpers inline"}
			],
			"next_action":"address findings"
		}`),
		Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{"buildDeclineWinddownHandlerOutput"},
	})
	require.NoError(t, err)
	assert.Empty(t, env.Findings)
}

func TestValidateTaskSpec_TestabilityExtractionsEmptyIsNoop(t *testing.T) {
	// When no extractions are supplied, the suppression helper must not drop
	// scope_drift findings.
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"minor","category":"scope_drift","criterion":"spec","evidence":"adds unrelated helper","suggestion":"keep scoped"}
			],
			"next_action":"address findings"
		}`),
		Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
	})
	require.NoError(t, err)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, verdict.CategoryScopeDrift, env.Findings[0].Category)
}
```

- [ ] **Step 2: Run the new tests to verify they fail.**

Run: `go test -race ./internal/mcpsrv/... -run TestValidateTaskSpec_TestabilityExtractions -v`
Expected: the suppression tests FAIL with "expected 2 findings, got 3" (or similar). The no-op test passes already (no behavior change in that path).

- [ ] **Step 3: Add the suppression helper.**

Edit `internal/mcpsrv/task_spec_normalize.go`. Append below the existing function:

```go
// suppressTestabilityExtractionScopeDrift drops any scope_drift finding whose
// evidence matches a testability_extractions entry by substring in either
// direction (entry is substring of evidence OR evidence is substring of
// entry). Non-scope_drift findings pass through unchanged. Empty extractions
// short-circuits to the input slice (no allocation).
func suppressTestabilityExtractionScopeDrift(findings []verdict.Finding, extractions []string) []verdict.Finding {
	if len(extractions) == 0 || len(findings) == 0 {
		return findings
	}
	out := make([]verdict.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Category != verdict.CategoryScopeDrift {
			out = append(out, f)
			continue
		}
		matched := false
		for _, e := range extractions {
			if strings.Contains(f.Evidence, e) || strings.Contains(e, f.Evidence) {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, f)
		}
	}
	return out
}
```

- [ ] **Step 4: Call the new helper from `ValidateTaskSpec`.**

Edit `internal/mcpsrv/handlers.go` (around line 119-120). Replace:

```go
	result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
```

with:

```go
	result.Findings = suppressTestabilityExtractionScopeDrift(result.Findings, inputs.TestabilityExtractions)
	result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
```

Order matters: suppression first (so a suppressed `scope_drift` cannot accidentally be referenced anywhere), rollup second.

- [ ] **Step 5: Run the new tests to verify they pass.**

Run: `go test -race ./internal/mcpsrv/...`
Expected: PASS.

- [ ] **Step 6: Append a CHANGELOG `### Changed` bullet.**

Edit `CHANGELOG.md`. Under the `### Changed` subsection of the `## [0.5.0]` block, add:

```markdown
- `validate_task_spec` deterministically suppresses reviewer-emitted `scope_drift` findings whose evidence names a caller-supplied `testability_extractions` entry (substring match in either direction).
```

- [ ] **Step 7: Run the full test suite.**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 8: Commit.**

```bash
git add internal/mcpsrv/task_spec_normalize.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "feat(mcpsrv): suppress scope_drift findings on testability_extractions"
```

---

### Task 5: Server-deterministic `normative_test_bodies` extraction in `validate_plan`

**Goal:** When `validate_plan` processes a multi-task plan, extract `**NORMATIVE TEST BODIES (verbatim):**` sections from each task's markdown server-side (not reviewer-driven), populate `PlanTaskResult.NormativeTestBodies` accordingly, and surface that field through the response envelope. The reviewer does not extract or emit this field.

**Acceptance criteria:**
- A new package-level function `planparser.ExtractNormativeTestBodies(body string) []string` returns the ordered list of extracted entries from one task's markdown body.
- Header detection: scans for the literal line `**NORMATIVE TEST BODIES (verbatim):**` (case-sensitive, exact-text match anywhere in the body).
- Fenced-block extraction: each immediately-following fenced code block (open on its own line with ```` ``` ```` optionally followed by a language tag; close on its own line with ```` ``` ````) becomes one entry. Adjacent fenced blocks (separated only by whitespace) are extracted as separate entries, in source order.
- Paragraph fallback: when the header is followed by non-fenced content, the paragraph (text up to the next blank line) becomes one entry.
- Per-entry truncation: an entry whose rune count exceeds `maxNormativeTestBodyChars` (4000) is truncated to `4000 - len("\n// truncated") = 3987` runes and appended with the literal marker `\n// truncated`.
- Entry-count cap: at most `maxNormativeTestBodyEntries` (20) entries per task; later entries are dropped silently.
- Empty body, missing header, or no following fenced/paragraph content yields an empty (nil-safe) slice.
- `PlanTaskResult.NormativeTestBodies []string` is added (omitempty JSON tag) — this is the same struct edit that Task 6 also touches, so the two tasks must not collide; this task adds only the `NormativeTestBodies` field, Task 6 adds the `ExitContracts` / `ExitContractsInferred` fields.
- In `ValidatePlan` (after the reviewer call returns and tasks are merged), for each `pr.Tasks[i]`, look up the matching `planparser.RawTask` by 1-based `TaskIndex` and call `ExtractNormativeTestBodies(rawTask.Body)`; assign the result to `pr.Tasks[i].NormativeTestBodies`. Run this in both the single-call path (`reviewPlanSingle`) and the chunked path (`reviewPlanChunked`), so the field is populated regardless of which path the plan took.
- The field round-trips through `verdict.ParsePlan` and `verdict.ParseTasksOnly` (i.e. the reviewer's response with the field absent does NOT break parsing — this requires no schema change because `additionalProperties:false` applies to the reviewer output, and the field is server-populated AFTER parse; see the integration step below).
- Unit tests cover: simple single-fenced extraction, adjacent fenced blocks, paragraph fallback, truncation marker, entry-count cap, no header → empty, header + no content → empty.
- Integration: `ValidatePlan`'s response includes the field when the plan has the header; `validate_plan` returns it through the JSON envelope; `omitempty` keeps it absent otherwise.
- `go test -race ./...` passes.
- CHANGELOG `### Added` bullet appended.

**Non-goals:**
- Reviewer-side prompt changes asking the reviewer to consume the bodies (the bodies are populated server-side AFTER the reviewer call returns).
- Threading the extracted bodies into `validate_task_spec` (that's the controller's job, exercised in Task 2's input wiring).
- `exit_contracts` plan extraction (Task 6).

**Context:**
The reviewer-output JSON schemas already use `additionalProperties: false`, which would reject a reviewer-emitted `normative_test_bodies` field. The design avoids that conflict by populating the field SERVER-SIDE only, after `verdict.ParsePlan` / `verdict.ParseTasksOnly` has accepted the reviewer's JSON. The field is added to the Go struct (with `omitempty`) so the response envelope carries it when populated and omits it otherwise. The schema files do NOT need a new entry for this field (the reviewer never emits it).

Per-task `Body` from `planparser.SplitTasks` carries the full task markdown including the `### Task N:` heading line. `TaskIndex` returned by the reviewer is 1-based (the reviewer is instructed to use the same numbering as the plan headings). Use 1-based indexing when matching back to the raw tasks.

**Files:**
- Create: `internal/planparser/normative_bodies.go`
- Create: `internal/planparser/normative_bodies_test.go`
- Modify: `internal/verdict/plan.go` (add `NormativeTestBodies []string` to `PlanTaskResult`)
- Modify: `internal/mcpsrv/handlers.go` (call the extractor inside both plan-path branches; share a small helper for the assignment loop)
- Test: `internal/mcpsrv/handlers_plan_test.go` (extend) — end-to-end check that the response includes the field
- Modify: `CHANGELOG.md`

---

- [ ] **Step 1: Write failing extraction tests.**

Create `internal/planparser/normative_bodies_test.go`:

```go
package planparser

import (
	"strings"
	"testing"
)

func TestExtractNormativeTestBodies_SimpleFenced(t *testing.T) {
	body := "### Task 1: thing\n\n**Goal:** thing\n\n**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\n@Test fun t() { assertThat(x).isEqualTo(y) }\n```\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d (%v)", len(got), got)
	}
	if got[0] != "@Test fun t() { assertThat(x).isEqualTo(y) }" {
		t.Errorf("entry = %q", got[0])
	}
}

func TestExtractNormativeTestBodies_AdjacentFenced(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\nbody1\n```\n\n```kotlin\nbody2\n```\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d (%v)", len(got), got)
	}
	if got[0] != "body1" || got[1] != "body2" {
		t.Errorf("entries = %v", got)
	}
}

func TestExtractNormativeTestBodies_ParagraphFallback(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\ndef test_x(): assert phrase in OUTPUT\n\nnext paragraph is not part of the body.\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d (%v)", len(got), got)
	}
	if got[0] != "def test_x(): assert phrase in OUTPUT" {
		t.Errorf("entry = %q", got[0])
	}
}

func TestExtractNormativeTestBodies_TruncationMarker(t *testing.T) {
	// 5000-char body must truncate to 3987 chars + "\n// truncated" suffix.
	big := strings.Repeat("x", 5000)
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n```\n" + big + "\n```\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if !strings.HasSuffix(got[0], "\n// truncated") {
		t.Errorf("entry missing truncation marker; suffix = %q", got[0][len(got[0])-20:])
	}
	if want := 3987 + len("\n// truncated"); len([]rune(got[0])) != want {
		t.Errorf("entry rune count = %d, want %d", len([]rune(got[0])), want)
	}
}

func TestExtractNormativeTestBodies_EntryCountCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("**NORMATIVE TEST BODIES (verbatim):**\n\n")
	for i := 0; i < 25; i++ {
		sb.WriteString("```\nbody" + string(rune('a'+i)) + "\n```\n\n")
	}
	got := ExtractNormativeTestBodies(sb.String())
	if len(got) != 20 {
		t.Errorf("want 20 entries (cap), got %d", len(got))
	}
}

func TestExtractNormativeTestBodies_NoHeader(t *testing.T) {
	body := "### Task 1: thing\n\nNo normative bodies here.\n\n```kotlin\nnot extracted\n```\n"
	got := ExtractNormativeTestBodies(body)
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestExtractNormativeTestBodies_HeaderNoContent(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n"
	got := ExtractNormativeTestBodies(body)
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestExtractNormativeTestBodies_Empty(t *testing.T) {
	if got := ExtractNormativeTestBodies(""); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail.**

Run: `go test -race ./internal/planparser/... -run ExtractNormativeTestBodies -v`
Expected: compile error (`ExtractNormativeTestBodies` undefined).

- [ ] **Step 3: Create the extractor.**

Create `internal/planparser/normative_bodies.go`:

```go
package planparser

import (
	"strings"
)

const (
	normativeHeader               = "**NORMATIVE TEST BODIES (verbatim):**"
	normativeMaxEntries           = 20
	normativeMaxChars             = 4000
	normativeTruncationMarker     = "\n// truncated"
	normativeTruncationMarkerRuns = 13 // len("\n// truncated") in runes (ASCII)
)

// ExtractNormativeTestBodies scans body for the literal NORMATIVE TEST BODIES
// header and returns the immediately-following fenced code blocks (one entry
// per block) in source order. If the header is followed by non-fenced text,
// the paragraph up to the next blank line is returned as a single entry.
//
// Per-entry truncation: entries longer than normativeMaxChars runes are
// truncated to (normativeMaxChars - normativeTruncationMarkerRuns) runes and
// the literal "\n// truncated" marker is appended. Entry-count cap: at most
// normativeMaxEntries entries; later entries are dropped silently.
//
// Returns nil when no header is present, when the header is at the very end
// of the body with nothing following, or when body is empty.
func ExtractNormativeTestBodies(body string) []string {
	if body == "" {
		return nil
	}
	idx := strings.Index(body, normativeHeader)
	if idx < 0 {
		return nil
	}
	rest := body[idx+len(normativeHeader):]
	rest = strings.TrimLeft(rest, "\n")
	if rest == "" {
		return nil
	}

	var out []string
	for len(out) < normativeMaxEntries {
		rest = strings.TrimLeft(rest, "\n")
		if rest == "" {
			break
		}
		entry, remainder, ok := readNormativeEntry(rest)
		if !ok {
			break
		}
		out = append(out, capNormativeEntry(entry))
		rest = remainder
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// readNormativeEntry reads one entry from the head of `rest`. If `rest` begins
// with a fenced code block (``` optionally followed by a language tag on its
// own line), it returns the inner text up to the closing fence. Otherwise it
// returns the paragraph up to the next blank line. Returns ok=false when no
// entry could be read (e.g. an unterminated fence with no closing line).
func readNormativeEntry(rest string) (entry, remainder string, ok bool) {
	// Fence case: the first line starts with ``` (after any trailing language tag).
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		// Single trailing line. Treat as paragraph entry.
		return strings.TrimSpace(rest), "", true
	}
	firstLine := strings.TrimRight(rest[:nl], "\r")
	if strings.HasPrefix(strings.TrimSpace(firstLine), "```") {
		// Find closing fence on its own line.
		inner := rest[nl+1:]
		closeRel := indexLineStarts(inner, "```")
		if closeRel < 0 {
			// Unterminated fence — bail. (Plans must close fences; treat as
			// not-an-entry rather than swallowing the rest of the body.)
			return "", "", false
		}
		entry = strings.TrimRight(inner[:closeRel], "\n")
		// Advance past the closing fence line.
		closeEnd := closeRel + len("```")
		if closeEnd < len(inner) && inner[closeEnd] == '\n' {
			closeEnd++
		}
		return entry, inner[closeEnd:], true
	}

	// Paragraph case: read up to the next blank line (\n\n).
	if blank := strings.Index(rest, "\n\n"); blank >= 0 {
		return strings.TrimSpace(rest[:blank]), rest[blank+2:], true
	}
	return strings.TrimSpace(rest), "", true
}

// indexLineStarts returns the offset (relative to s) of the first occurrence
// of `prefix` at the start of a line. Returns -1 when not found. Treats the
// string-start as a line boundary.
func indexLineStarts(s, prefix string) int {
	if strings.HasPrefix(s, prefix) {
		return 0
	}
	needle := "\n" + prefix
	if i := strings.Index(s, needle); i >= 0 {
		return i + 1
	}
	return -1
}

// capNormativeEntry truncates entry to normativeMaxChars runes if necessary,
// appending the truncation marker so the reviewer can see the body was clipped.
func capNormativeEntry(entry string) string {
	runes := []rune(entry)
	if len(runes) <= normativeMaxChars {
		return entry
	}
	cut := normativeMaxChars - normativeTruncationMarkerRuns
	return string(runes[:cut]) + normativeTruncationMarker
}
```

- [ ] **Step 4: Run the extractor tests to verify they pass.**

Run: `go test -race ./internal/planparser/...`
Expected: PASS for all `ExtractNormativeTestBodies` tests plus the existing `SplitTasks` tests.

- [ ] **Step 5: Add `NormativeTestBodies` to `PlanTaskResult`.**

Edit `internal/verdict/plan.go` (around line 64-73). Extend the struct:

```go
type PlanTaskResult struct {
	TaskIndex             int       `json:"task_index"`
	TaskTitle             string    `json:"task_title"`
	Verdict               Verdict   `json:"verdict"`
	Findings              []Finding `json:"findings"`
	SuggestedHeaderBlock  string    `json:"suggested_header_block"`
	SuggestedHeaderReason string    `json:"suggested_header_reason"`
	LightweightEligible   bool      `json:"lightweight_eligible,omitempty"`
	LightweightReason     string    `json:"lightweight_reason,omitempty"`
	NormativeTestBodies   []string  `json:"normative_test_bodies,omitempty"`
}
```

- [ ] **Step 6: Wire server-side population into `ValidatePlan`.**

Edit `internal/mcpsrv/handlers.go`. The `ValidatePlan` function ends around line 1046. After the reviewer-call branch (where `pr` has been populated from either `reviewPlanSingle` or `reviewPlanChunked`) and after the truncation-handler returns, but BEFORE `prependPlanClamp(pr, clamp)`, populate `NormativeTestBodies`. The cleanest place is to write a helper and call it in one location.

Add a new helper just below `noHeadingsPlanResult` (around line 1170):

```go
// populateNormativeTestBodies fills in pr.Tasks[i].NormativeTestBodies from
// the matching planparser.RawTask body. Match is by 1-based TaskIndex; tasks
// whose index is out of range are left alone (defensive — chunked-path
// reviewer responses occasionally drift on task_index, and we'd rather emit
// no extraction for that task than panic or misattribute).
func populateNormativeTestBodies(pr *verdict.PlanResult, tasks []planparser.RawTask) {
	for i := range pr.Tasks {
		idx := pr.Tasks[i].TaskIndex - 1 // 1-based -> 0-based
		if idx < 0 || idx >= len(tasks) {
			continue
		}
		pr.Tasks[i].NormativeTestBodies = planparser.ExtractNormativeTestBodies(tasks[idx].Body)
	}
}
```

Then in `ValidatePlan`, between the truncation handler and the `prependPlanClamp` call (around line 1042), insert:

```go
	populateNormativeTestBodies(&pr, tasks)
```

The `tasks` variable is already in scope from the `planparser.SplitTasks(args.PlanText)` call at line 989.

- [ ] **Step 7: Add an integration test in handlers_plan_test.go.**

Append to `internal/mcpsrv/handlers_plan_test.go`:

```go
func TestValidatePlan_PopulatesNormativeTestBodies(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{
				"plan_verdict":"pass",
				"plan_findings":[],
				"tasks":[
					{"task_index":1,"task_title":"Task 1: with bodies","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}
				],
				"next_action":"proceed",
				"plan_quality":"actionable"
			}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: with bodies\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\n@Test fun t() { /* body */ }\n```\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 1)
	assert.Equal(t, []string{"@Test fun t() { /* body */ }"}, pr.Tasks[0].NormativeTestBodies)
}
```

- [ ] **Step 8: Run all tests.**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 9: Append a CHANGELOG `### Added` bullet.**

Edit `CHANGELOG.md`. Under the `### Added` subsection of the `## [0.5.0]` block, add:

```markdown
- `validate_plan` task results include optional `normative_test_bodies`, populated server-side by deterministic markdown extraction of `**NORMATIVE TEST BODIES (verbatim):**` sections from each task's plan markdown.
```

- [ ] **Step 10: Commit.**

```bash
git add internal/planparser/normative_bodies.go internal/planparser/normative_bodies_test.go internal/verdict/plan.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_plan_test.go CHANGELOG.md
git commit -m "feat(validate_plan): extract normative_test_bodies server-side"
```

---

### Task 6: Reviewer-emitted `exit_contracts` + `exit_contracts_inferred` in `validate_plan`

**Goal:** Extend `PlanTaskResult` with `ExitContracts []string` and `ExitContractsInferred bool` so the plan-level reviewer can populate cross-task contracts (hybrid: explicit if the task has an `**Exit contracts:**` section, inferred otherwise). Update `plan.tmpl` and `plan_tasks_chunk.tmpl` to ask for these fields, update `plan_schema.json` and `tasks_only_schema.json` to accept them, and regenerate golden files.

**Acceptance criteria:**
- `verdict.PlanTaskResult` exposes `ExitContracts []string` (omitempty) and `ExitContractsInferred bool` (omitempty).
- `plan_schema.json` and `tasks_only_schema.json` accept both new fields in the task-item `properties` block (not `required`), preserving `additionalProperties: false`. `plan_findings_only_schema.json` is untouched (it has no per-task data). `schema.json` is untouched (per-task hook only).
- `plan.tmpl` instructs the reviewer to emit `exit_contracts` (max 20 per task, max 240 code points per contract) and `exit_contracts_inferred` (false if the task contains a literal `**Exit contracts:**` section; true otherwise — the reviewer infers contracts from cross-task references). The template must also note that `normative_test_bodies` is populated server-side and the reviewer must NOT emit it.
- `plan_tasks_chunk.tmpl` carries the same instructions.
- New tests assert: (a) round-trip parse accepts JSON with the new fields, (b) round-trip parse accepts JSON without the new fields (backward compat), (c) the `plan.tmpl` and `plan_tasks_chunk.tmpl` renders include the new instructions (Contains-style), (d) the `plan.tmpl` render explicitly tells the reviewer NOT to emit `normative_test_bodies`.
- Golden files for `plan_basic`, `plan_basic_quick`, `plan_tasks_chunk`, `plan_tasks_chunk_quick` regenerated and reviewed.
- `go test -race ./...` passes.
- CHANGELOG `### Added` + `### Changed` bullets appended.

**Non-goals:**
- `validate_completion`-side `exit_contracts` (Task 7).
- Plan-author syntax migration tooling (out of scope per spec).

**Context:**
This task collides on `internal/verdict/plan.go` (`PlanTaskResult`) with Task 5. Task 5 added `NormativeTestBodies`; this task adds `ExitContracts` and `ExitContractsInferred`. Apply this task's struct change as an addition to whatever Task 5 left behind — do NOT overwrite.

The reviewer's response is allowed to omit either new field; the parser (`json.Decoder` with `DisallowUnknownFields`) will accept omitted but reject unknown — the schema enforces the same. `omitempty` on the Go fields keeps the response envelope clean when the reviewer chose not to emit.

The "max 20 contracts / max 240 code points per contract" is reviewer-side guidance in the prompt; we don't enforce it server-side this release (the spec calls it "defensive limits, reviewer-emitted"). The downstream `validate_completion` input cap (50 entries / 500 chars) is roomier; that's intentional.

**Files:**
- Modify: `internal/verdict/plan.go` (extend `PlanTaskResult`)
- Modify: `internal/verdict/plan_schema.json` (add new task-item fields to `properties`)
- Modify: `internal/verdict/tasks_only_schema.json` (same)
- Modify: `internal/prompts/templates/plan.tmpl`
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl`
- Test: `internal/verdict/plan_test.go` (extend)
- Test: `internal/prompts/prompts_test.go` (extend)
- Regenerate: `internal/prompts/testdata/plan_basic.golden`, `plan_basic_quick.golden`, `plan_tasks_chunk.golden`, `plan_tasks_chunk_quick.golden`
- Modify: `CHANGELOG.md`

---

- [ ] **Step 1: Add failing round-trip parser test.**

Append to `internal/verdict/plan_test.go`:

```go
func TestParsePlan_ExitContractsRoundTrip(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"pass",
		"plan_findings":[],
		"tasks":[
			{
				"task_index":1,
				"task_title":"Task 1: emit",
				"verdict":"pass",
				"findings":[],
				"suggested_header_block":"",
				"suggested_header_reason":"",
				"exit_contracts":["Defines handlerName constant","Exports DECLINE_NODE_NAME"],
				"exit_contracts_inferred":true
			}
		],
		"next_action":"proceed",
		"plan_quality":"actionable"
	}`)
	pr, err := ParsePlan(raw)
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 1)
	assert.Equal(t, []string{"Defines handlerName constant", "Exports DECLINE_NODE_NAME"}, pr.Tasks[0].ExitContracts)
	assert.True(t, pr.Tasks[0].ExitContractsInferred)
}

func TestParsePlan_ExitContractsAbsent_BackCompat(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"pass",
		"plan_findings":[],
		"tasks":[
			{
				"task_index":1,
				"task_title":"Task 1: emit",
				"verdict":"pass",
				"findings":[],
				"suggested_header_block":"",
				"suggested_header_reason":""
			}
		],
		"next_action":"proceed",
		"plan_quality":"actionable"
	}`)
	pr, err := ParsePlan(raw)
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 1)
	assert.Empty(t, pr.Tasks[0].ExitContracts)
	assert.False(t, pr.Tasks[0].ExitContractsInferred)
}
```

If `internal/verdict/plan_test.go` does not yet import `require` / `assert` from testify, add the imports (mirror `parser_test.go`'s style — many tests in this package use standard `testing` only; pick whichever is already in use in plan_test.go).

- [ ] **Step 2: Add failing prompt-rendering tests.**

Append to `internal/prompts/prompts_test.go`:

```go
const (
	anchorExitContractsInstruction      = "exit_contracts"
	anchorExitContractsInferredFlag     = "exit_contracts_inferred"
	anchorExitContractsExplicitHeader   = "**Exit contracts:**"
	anchorExitContractsMaxGuidance      = "at most 20 contracts"
	anchorNormativeServerSideInstruction = "populated server-side"
)

func TestRenderPlan_ExitContractsInstructionPresent(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorExitContractsInstruction, "plan.tmpl should ask reviewer to populate exit_contracts")
	assert.Contains(t, out.User, anchorExitContractsInferredFlag, "plan.tmpl should ask reviewer to set exit_contracts_inferred")
	assert.Contains(t, out.User, anchorExitContractsExplicitHeader, "plan.tmpl should mention the explicit **Exit contracts:** plan-side syntax")
	assert.Contains(t, out.User, anchorExitContractsMaxGuidance, "plan.tmpl should bound contracts per task")
	assert.Contains(t, out.User, anchorNormativeServerSideInstruction, "plan.tmpl should tell reviewer NOT to emit normative_test_bodies (server-populated)")
}

func TestRenderPlanTasksChunk_ExitContractsInstructionPresent(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   "# Plan\n\n### Task 1: A\n\n**Goal:** Test\n",
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorExitContractsInstruction, "plan_tasks_chunk.tmpl should ask reviewer to populate exit_contracts")
	assert.Contains(t, out.User, anchorExitContractsInferredFlag, "plan_tasks_chunk.tmpl should ask reviewer to set exit_contracts_inferred")
	assert.Contains(t, out.User, anchorExitContractsExplicitHeader, "plan_tasks_chunk.tmpl should mention the explicit **Exit contracts:** plan-side syntax")
	assert.Contains(t, out.User, anchorExitContractsMaxGuidance, "plan_tasks_chunk.tmpl should bound contracts per task")
}
```

- [ ] **Step 3: Run the new tests to verify they fail.**

Run: `go test -race ./internal/verdict/... ./internal/prompts/... -run ExitContracts -v`
Expected: compile error on `ExitContracts` undefined plus Contains failures on the prompt tests.

- [ ] **Step 4: Extend `PlanTaskResult`.**

Edit `internal/verdict/plan.go`. The struct already has `LightweightEligible` / `LightweightReason` from v0.4.0 and (after Task 5) `NormativeTestBodies`. Add the two new fields:

```go
type PlanTaskResult struct {
	TaskIndex             int       `json:"task_index"`
	TaskTitle             string    `json:"task_title"`
	Verdict               Verdict   `json:"verdict"`
	Findings              []Finding `json:"findings"`
	SuggestedHeaderBlock  string    `json:"suggested_header_block"`
	SuggestedHeaderReason string    `json:"suggested_header_reason"`
	LightweightEligible   bool      `json:"lightweight_eligible,omitempty"`
	LightweightReason     string    `json:"lightweight_reason,omitempty"`
	ExitContracts         []string  `json:"exit_contracts,omitempty"`
	ExitContractsInferred bool      `json:"exit_contracts_inferred,omitempty"`
	NormativeTestBodies   []string  `json:"normative_test_bodies,omitempty"`
}
```

- [ ] **Step 5: Update `plan_schema.json` task-item properties.**

Edit `internal/verdict/plan_schema.json`. In the `tasks.items.properties` block (around lines 17-29), add the two new optional fields after `lightweight_reason`:

```json
          "task_index":              { "type": "integer", "minimum": 0 },
          "task_title":              { "type": "string", "minLength": 1 },
          "verdict":                 { "type": "string", "enum": ["pass", "warn", "fail"] },
          "findings":                { "type": "array", "items": { "$ref": "#/definitions/finding" } },
          "suggested_header_block":  { "type": "string" },
          "suggested_header_reason": { "type": "string" },
          "lightweight_eligible":    { "type": "boolean" },
          "lightweight_reason":      { "type": "string" },
          "exit_contracts":          { "type": "array", "items": { "type": "string", "minLength": 1 } },
          "exit_contracts_inferred": { "type": "boolean" }
```

Do NOT add `normative_test_bodies` here — the reviewer does NOT emit it (per Task 5, that field is server-populated only, after parse). Keep `additionalProperties: false` unchanged.

- [ ] **Step 6: Update `tasks_only_schema.json` task-item properties.**

Edit `internal/verdict/tasks_only_schema.json`. Same edit as Step 5; add the two new optional fields after `lightweight_reason`. Keep `additionalProperties: false`.

- [ ] **Step 7: Edit `plan.tmpl` to instruct reviewer to emit `exit_contracts`.**

Edit `internal/prompts/templates/plan.tmpl`. After the existing lightweight guidance (lines 44-51) and before the `Severity:` line (line 53), insert a new paragraph block:

```text
For every task, also emit `exit_contracts` and `exit_contracts_inferred`.

`exit_contracts` is a list of plain-English contract strings — symbols, types, constants, or fields the task introduces that later tasks in the plan explicitly reference. One contract per consumed surface. Emit at most 20 contracts per task, with each contract at most 240 Unicode code points; if more would apply, summarize. If the task's markdown contains an explicit `**Exit contracts:**` bullet section, respect it verbatim and set `exit_contracts_inferred` to false. Otherwise, infer the contracts by reading the plan as a whole and set `exit_contracts_inferred` to true. When no later task references anything the task introduces, emit an empty list and still set `exit_contracts_inferred` honestly.

Do NOT emit `normative_test_bodies` per task. That field is populated server-side from `**NORMATIVE TEST BODIES (verbatim):**` sections in the plan markdown; you do not need to extract or re-emit it.

```

- [ ] **Step 8: Edit `plan_tasks_chunk.tmpl` to carry the same instruction.**

Edit `internal/prompts/templates/plan_tasks_chunk.tmpl`. After the existing lightweight guidance (lines 35-42) and before the `If a task is missing the structured header block entirely` paragraph (line 44), insert the same instruction block:

```text
For every task, also emit `exit_contracts` and `exit_contracts_inferred`.

`exit_contracts` is a list of plain-English contract strings — symbols, types, constants, or fields the task introduces that later tasks in the plan explicitly reference. One contract per consumed surface. Emit at most 20 contracts per task, with each contract at most 240 Unicode code points; if more would apply, summarize. If the task's markdown contains an explicit `**Exit contracts:**` bullet section, respect it verbatim and set `exit_contracts_inferred` to false. Otherwise, infer the contracts by reading the plan as a whole and set `exit_contracts_inferred` to true. When no later task references anything the task introduces, emit an empty list and still set `exit_contracts_inferred` honestly.

```

(The chunked template does NOT need the `normative_test_bodies` carve-out because Pass-2 reviewer responses use the `tasks_only` schema which does not include that field anyway — but adding the same note doesn't hurt and keeps the two templates consistent. Add it here too if it simplifies reasoning; otherwise omit.)

For this plan, OMIT the `normative_test_bodies` note from `plan_tasks_chunk.tmpl` — the smaller surface keeps the chunked-path prompt focused on per-task analysis.

- [ ] **Step 9: Regenerate golden files.**

Run: `go test ./internal/prompts/... -update`
Inspect:

```bash
git diff internal/prompts/testdata/plan_basic.golden internal/prompts/testdata/plan_basic_quick.golden internal/prompts/testdata/plan_tasks_chunk.golden internal/prompts/testdata/plan_tasks_chunk_quick.golden
```

Confirm the diff contains the new `exit_contracts` instruction block in `plan_basic*.golden` and `plan_tasks_chunk*.golden`, and the `normative_test_bodies` server-populated note ONLY in `plan_basic*.golden`. No other content should change.

- [ ] **Step 10: Run tests without `-update` to verify pass.**

Run: `go test -race ./internal/prompts/... ./internal/verdict/... ./internal/mcpsrv/...`
Expected: PASS.

- [ ] **Step 11: Append CHANGELOG bullets.**

Edit `CHANGELOG.md`. Under `### Added` of the `## [0.5.0]` block, add:

```markdown
- `validate_plan` task results include optional `exit_contracts` (hybrid: explicit `**Exit contracts:**` section if present, reviewer-inferred otherwise) with a sibling `exit_contracts_inferred` provenance flag.
```

Under `### Changed`, add:

```markdown
- `plan.tmpl` and `plan_tasks_chunk.tmpl` ask the reviewer to populate `exit_contracts` and `exit_contracts_inferred` per task. `plan.tmpl` also notes that `normative_test_bodies` is populated server-side and must not be reviewer-emitted.
```

- [ ] **Step 12: Run the full test suite.**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 13: Commit.**

```bash
git add internal/verdict/plan.go internal/verdict/plan_schema.json internal/verdict/tasks_only_schema.json internal/prompts/templates/plan.tmpl internal/prompts/templates/plan_tasks_chunk.tmpl internal/verdict/plan_test.go internal/prompts/prompts_test.go internal/prompts/testdata/plan_basic.golden internal/prompts/testdata/plan_basic_quick.golden internal/prompts/testdata/plan_tasks_chunk.golden internal/prompts/testdata/plan_tasks_chunk_quick.golden CHANGELOG.md
git commit -m "feat(validate_plan): reviewer emits exit_contracts per task"
```

---

### Task 7: `validate_completion` accepts `exit_contracts` + provenance-aware `post.tmpl` rendering

**Goal:** Add `ExitContracts []string` and `ExitContractsInferred bool` inputs to `validate_completion`, normalize them under the standard caller-input cap (50 entries × 500 runes), and render a provenance-aware "Exit contracts (...)" section in `post.tmpl` with reviewer guidance for on-miss severity calibration. The reviewer flags misses as `missing_acceptance_criterion` with `criterion: exit_contract`.

**Acceptance criteria:**
- `ValidateCompletionArgs` exposes `ExitContracts []string` and `ExitContractsInferred bool` with `json:"...,omitempty"` tags.
- New normalization helper `normalizeCompletionExitContracts(entries []string) ([]string, error)` in `internal/mcpsrv/task_spec_input.go` (or a new file `completion_input.go` if cleaner) trims, drops empties, and caps at 50 entries × 500 runes (the standard caller-input regime). On exceed, returns a field-named error.
- The normalization is called from `ValidateCompletion` early, before the evidence-shape guard.
- `prompts.PostInput` exposes `ExitContracts []string` and `ExitContractsInferred bool`.
- `post.tmpl` renders an `Exit contracts (explicit — author-authored, must be satisfied):` section when `ExitContractsInferred == false` and `Exit contracts (reviewer-inferred — verify but do not gate harshly):` when `ExitContractsInferred == true`. The section header is omitted entirely when `ExitContracts` is empty.
- `post.tmpl` reviewer guidance instructs: for each contract, assess against `final_files` / `final_diff`. On miss for an **explicit** contract, the reviewer may emit `severity: major` for hard mismatches (named symbol absent from diff) and `severity: minor` for soft mismatches (cannot verify but evidence does not contradict). On miss for an **inferred** contract, cap at `severity: minor` unless the evidence is structurally inconsistent. Miss findings use `category: missing_acceptance_criterion`, `criterion: exit_contract`, evidence quoting the contract and closest production-code surface, and a suggestion naming the specific edit.
- New unit tests cover: trim-and-store, cap-rejection, the rendered prompt branches (explicit vs inferred), and the empty-omitted path.
- Regenerated golden `post_basic.golden` is unchanged (the existing `TestRenderPost` does not populate the new fields).
- `go test -race ./...` passes.
- CHANGELOG `### Added` + `### Changed` bullets appended.

**Non-goals:**
- Server-side flooring of exit-contract miss severities (the "cap at minor" for inferred misses is reviewer-side guidance, not server enforcement).
- Lightweight-mode interactions (the existing lightweight-mode path in `ValidateCompletion` continues to work; the new inputs are additive and the prompt section is gated on `ExitContracts` non-empty).

**Context:**
The existing `ValidateCompletion` shape lives at `internal/mcpsrv/handlers.go:851-971`. Normalize early — place the call between Step 5 (payload-cap check) and Step 6 (evidence-shape guard) so the bad-input rejection error type matches the other caller-input validators (handler returns the Go error rather than building a finding envelope). The `prompts.PostInput` struct lives at `internal/prompts/prompts.go:40-48` — add the two new fields there. The new prompt section should land in `post.tmpl` after the existing `Pinned by:` block (lines 14-17) and before the `MajorPreFindings` block (line 17), so the spec/anchors render first and the binding contracts render near the spec — not buried near the evidence.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (extend `ValidateCompletionArgs`, normalize early, thread to `PostInput`)
- Modify: `internal/mcpsrv/task_spec_input.go` (add `normalizeCompletionExitContracts`)
- Modify: `internal/prompts/prompts.go` (extend `PostInput`)
- Modify: `internal/prompts/templates/post.tmpl`
- Test: `internal/mcpsrv/handlers_test.go` (extend)
- Test: `internal/prompts/prompts_test.go` (extend)
- Regenerate: `internal/prompts/testdata/post_basic.golden` (should be unchanged)
- Modify: `CHANGELOG.md`

---

- [ ] **Step 1: Add failing handler-level tests for `exit_contracts` input validation.**

The handler-level test focuses on caller-input validation (caps, errors). Round-trip-into-prompt assertions live in the `prompts_test.go` Contains-style tests added in Step 2 — adding a request-capturing reviewer to assert prompt-body substrings from the handler test would duplicate that coverage.

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateCompletion_ExitContractsLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "contract"
	}
	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:     "anything",
		Summary:       "s",
		FinalDiff:     "diff",
		ExitContracts: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit_contracts must contain at most 50 entries")

	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:     "anything",
		Summary:       "s",
		FinalDiff:     "diff",
		ExitContracts: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit_contracts[0] must be at most 500 characters")
}
```

For the round-trip-to-prompt assertion, defer to the `prompts_test.go` Contains-style tests in Step 2 — the handler test in Step 1 covers normalization and is sufficient; deep prompt-content assertions live in the prompts package.

- [ ] **Step 2: Add failing prompt-render tests for the provenance-aware section.**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPost_WithExplicitExitContractsIncludesSection(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:                  sampleSpec(),
		Summary:               "implemented X",
		FinalDiff:             "diff --git",
		ExitContracts:         []string{"Defines handlerName"},
		ExitContractsInferred: false,
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Exit contracts (explicit — author-authored, must be satisfied):")
	assert.Contains(t, out.User, "- Defines handlerName")
	assert.Contains(t, out.User, "criterion: exit_contract")
	assert.Contains(t, out.User, "explicit")
}

func TestRenderPost_WithInferredExitContractsIncludesSection(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:                  sampleSpec(),
		Summary:               "implemented X",
		FinalDiff:             "diff --git",
		ExitContracts:         []string{"Exports DECLINE_NODE"},
		ExitContractsInferred: true,
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Exit contracts (reviewer-inferred — verify but do not gate harshly):")
	assert.Contains(t, out.User, "- Exports DECLINE_NODE")
	assert.Contains(t, out.User, "cap at `severity: minor`")
}

func TestRenderPost_WithoutExitContractsOmitsSection(t *testing.T) {
	out, err := RenderPost(PostInput{Spec: sampleSpec(), Summary: "x", FinalDiff: "diff"})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "Exit contracts (")
}
```

- [ ] **Step 3: Run the new tests to verify they fail.**

Run: `go test -race ./internal/mcpsrv/... ./internal/prompts/... -run "(Exit|exit)Contracts" -v`
Expected: compile errors on missing fields plus Contains failures.

- [ ] **Step 4: Extend `ValidateCompletionArgs`.**

Edit `internal/mcpsrv/handlers.go` (around line 638-646):

```go
type ValidateCompletionArgs struct {
	SessionID             string    `json:"session_id"  jsonschema:"required"`
	Summary               string    `json:"summary"     jsonschema:"required"`
	FinalFiles            []FileArg `json:"final_files,omitempty"`
	FinalDiff             string    `json:"final_diff,omitempty"`
	TestEvidence          string    `json:"test_evidence,omitempty"`
	ExitContracts         []string  `json:"exit_contracts,omitempty"`
	ExitContractsInferred bool      `json:"exit_contracts_inferred,omitempty"`
	ModelOverride         string    `json:"model_override,omitempty"`
	MaxTokensOverride     int       `json:"max_tokens_override,omitempty"`
}
```

- [ ] **Step 5: Add `normalizeCompletionExitContracts`.**

Edit `internal/mcpsrv/task_spec_input.go`. Append below the four new helpers from Task 2:

```go
func normalizeCompletionExitContracts(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("exit_contracts[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("exit_contracts must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}
```

- [ ] **Step 6: Wire the normalizer into `ValidateCompletion` and thread to the prompt.**

Edit `internal/mcpsrv/handlers.go`. In `ValidateCompletion` (around line 851-971), after Step 5 (payload-cap check, around line 874-877) and before Step 6 (evidence-shape guard, around line 879), insert:

```go
	exitContracts, err := normalizeCompletionExitContracts(args.ExitContracts)
	if err != nil {
		return nil, Envelope{}, err
	}
```

Then in the `prompts.RenderPost(prompts.PostInput{...})` call (around line 917-927), add the two new fields:

```go
			return prompts.RenderPost(prompts.PostInput{
				Spec:                           spec,
				Summary:                        args.Summary,
				Files:                          toPromptFiles(args.FinalFiles),
				FinalDiff:                      args.FinalDiff,
				TestEvidence:                   args.TestEvidence,
				MajorPreFindings:               majorPreFindings,
				ReferencedPathsMissingEvidence: referencedPathsMissingEvidence(args),
				ExitContracts:                  exitContracts,
				ExitContractsInferred:          args.ExitContractsInferred,
			})
```

- [ ] **Step 7: Extend `PostInput`.**

Edit `internal/prompts/prompts.go` (around line 40-48):

```go
type PostInput struct {
	Spec                           session.TaskSpec
	Summary                        string
	Files                          []File
	FinalDiff                      string
	TestEvidence                   string
	MajorPreFindings               []verdict.Finding
	ReferencedPathsMissingEvidence []string
	ExitContracts                  []string
	ExitContractsInferred          bool
}
```

- [ ] **Step 8: Edit `post.tmpl` to render the provenance-aware section.**

Edit `internal/prompts/templates/post.tmpl`. After the `Pinned by:` block (lines 14-17) and before the `MajorPreFindings` block (line 17), insert:

```text
{{end}}{{if .ExitContracts}}
{{if .ExitContractsInferred}}Exit contracts (reviewer-inferred — verify but do not gate harshly):
{{else}}Exit contracts (explicit — author-authored, must be satisfied):
{{end}}{{range .ExitContracts}}- {{.}}
{{end}}
For each contract above, assess whether `final_files` / `final_diff` evidence satisfies it. On miss, emit a finding with `category: missing_acceptance_criterion`, `criterion: exit_contract`, `evidence` quoting the contract and the closest matching production-code surface, and `suggestion` naming the specific edit that would satisfy the contract.

{{if .ExitContractsInferred}}These contracts were inferred from cross-task references rather than authored explicitly. On miss, cap at `severity: minor` unless the evidence is structurally inconsistent — e.g. the contract names a constant the diff explicitly leaves undefined — in which case `severity: major` is appropriate.{{else}}These contracts were authored explicitly by the plan. On miss, you may emit `severity: major` for hard mismatches (a named symbol the contract references is absent from the diff) and `severity: minor` for soft mismatches (evidence suggests the contract is satisfied but cannot be verified from the supplied evidence).{{end}}
{{end}}
```

Note the template balance: the existing `Pinned by:` block ends with `{{end}}{{if .MajorPreFindings}}`. After the insert, the new `{{if .ExitContracts}}` closes with `{{end}}` and we fall through to the existing `{{if .MajorPreFindings}}` block. Verify by rendering and inspecting the golden diff.

- [ ] **Step 9: Regenerate the post golden.**

Run: `go test ./internal/prompts/... -update`
Inspect:

```bash
git diff internal/prompts/testdata/post_basic.golden
```

Expected: empty diff (the existing `TestRenderPost` does not populate `ExitContracts`).

- [ ] **Step 10: Run tests without `-update`.**

Run: `go test -race ./internal/prompts/... ./internal/mcpsrv/...`
Expected: PASS.

- [ ] **Step 11: Append CHANGELOG bullets.**

Edit `CHANGELOG.md`. Under `### Added`:

```markdown
- `validate_completion` accepts optional `exit_contracts` plus `exit_contracts_inferred`; reviewer flags misses as `missing_acceptance_criterion` with `criterion: exit_contract`, calibrating miss severity by provenance.
```

Under `### Changed`:

```markdown
- `post.tmpl` renders a provenance-aware `Exit contracts (...)` section when `exit_contracts` is non-empty and instructs the reviewer to walk each contract against final-file evidence.
```

- [ ] **Step 12: Run the full test suite.**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 13: Commit.**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/task_spec_input.go internal/prompts/prompts.go internal/prompts/templates/post.tmpl internal/mcpsrv/handlers_test.go internal/prompts/prompts_test.go internal/prompts/testdata/post_basic.golden CHANGELOG.md
git commit -m "feat(validate_completion): accept exit_contracts inputs"
```

---

### Task 8: INTEGRATION.md doc additions (D1–D5) + finalize CHANGELOG `### Changed` doc line

**Goal:** Land the five doc-only items from spec §3 (normative-test-bodies convention, CVR scope clarification, `.trimIndent()` raw-string caveat, language-scoping prose caveat, lightweight-mode visibility) in `INTEGRATION.md`, and add the matching CHANGELOG `### Changed` bullet (doc-only items fold under `### Changed` per project CLAUDE.md, not `### Documentation`).

**Acceptance criteria:**
- `INTEGRATION.md` includes a new section (D1) under "For plan authors" describing the `**NORMATIVE TEST BODIES (verbatim):**` header convention with one worked example showing a fenced code block immediately following the header.
- `INTEGRATION.md` updates the existing "Choosing `pinned_by`, `context`, and `controller_verified_references`" subsection (D2) to state explicitly that `controller_verified_references` substring suppression applies ONLY to `unverifiable_codebase_claim` findings — AC ambiguity, scope drift, missing criteria, and `convention_deviation` are NOT suppressed.
- `INTEGRATION.md` includes a new "**`.trimIndent()` raw-string caveat**" subsection (D3) under "For plan authors" warning that multi-line plan snippets bound by `.trimIndent()` must keep example phrases on a single source line; mid-phrase wraps render as newlines at runtime. Add the test-on-rendered-string suggestion.
- `INTEGRATION.md` includes a new "**Language-scoping prose caveat**" subsection (D4) under "For implementers" noting that the text-only reviewer may surface `ambiguous_spec` findings around closure/scoping semantics (Kotlin `var` + lambda, Python nested-scope, etc.); mitigation is to trust the verbatim plan code block.
- `INTEGRATION.md` restructures the dispatch-clause area so the lightweight-mode eligibility criteria are introduced at the TOP of the clause section, before the full-protocol clause (D5). The existing `### Lightweight protocol mode (v0.3.1+)` heading moves up, and a one-paragraph callout appears at the top of `## 4. For implementers — the lifecycle protocol` directing readers to check lightweight eligibility before reaching for the full clause.
- `CHANGELOG.md` `### Changed` subsection of `## [0.5.0]` includes the doc-additions bullet matching spec §5.
- `go test -race ./...` still passes (this task only edits markdown).

**Non-goals:**
- Mirroring INTEGRATION.md changes into `~/.claude/anti-tangent.md` — that lives outside this repo and is the human's downstream mirror step (spec §Rollout).
- Touching README.md (the spec §3 doc items are scoped to INTEGRATION.md).

**Context:**
The relevant existing INTEGRATION.md anchors:
- Section "## 3. For plan authors — the anti-tangent-friendly task format" (line 216) — D1 and D3 land here.
- Subsection "### Choosing `pinned_by`, `context`, and `controller_verified_references`" (line 43) — D2 edits the existing prose.
- Section "## 4. For implementers — the lifecycle protocol" (line 283) — D4 and D5 land here.
- Existing subsection "### Lightweight protocol mode (v0.3.1+)" (line 375) — D5 restructures.

Per project CLAUDE.md, doc-only items fold under `### Changed` (not `### Documentation`). The v0.4.0 release used `### Documentation` and the spec calls this a divergence to be re-aligned — but for v0.5.0 we re-align to `### Changed`.

**Files:**
- Modify: `INTEGRATION.md`
- Modify: `CHANGELOG.md`

---

- [ ] **Step 1: Add D1 — normative-test-bodies convention.**

Edit `INTEGRATION.md`. After Section 3.5 ("### 3.5 Anti-pattern: keep implementation steps OUT of the AC list", around line 277-279), add a new subsection:

```markdown
### 3.6 Normative test bodies (binding test code in plans)

When a task's plan markdown pastes verbatim test code that the implementer should land as written — not as advisory illustration — wrap each test body in a fenced code block immediately following the literal header:

```markdown
**NORMATIVE TEST BODIES (verbatim):**

```kotlin
@Test fun whenInputIsX_thenOutputIsY() {
    val result = subject.process(X)
    assertThat(result.decision).isEqualTo(DECLINE)
    assertThat(result.handlerName).isEqualTo(WINDDOWN_NODE_NAME)
}
```
```

`validate_plan` extracts each fenced block server-side (deterministic markdown parsing, not reviewer-driven) and populates `PlanTaskResult.NormativeTestBodies`. Controllers thread that list into `validate_task_spec`'s `normative_test_bodies` input on dispatch. The reviewer is then instructed to treat each entry as binding test scope — equivalent in authority to a bullet under Acceptance criteria — so the `validate_task_spec`-only view of the task (Goal / AC / Non-goals / Context) does not blindly flag "test scope unclear" when the plan's per-step code block already pins it.

Adjacent fenced blocks (separated only by whitespace) extract as separate entries. When a body exceeds 4000 Unicode code points, the server truncates it and appends a `// truncated` marker so the reviewer sees the body was clipped. If you need to pin a body that legitimately exceeds 4000 code points, paraphrase or excerpt with the leading comment marker `// excerpt:` (or analogous language-appropriate comment) — the reviewer treats `// excerpt:` entries as partial coverage rather than full bodies.
```

- [ ] **Step 2: Add D2 — extend CVR scope clarification.**

Edit `INTEGRATION.md`. In the existing "### Choosing `pinned_by`, `context`, and `controller_verified_references`" subsection (around line 43-49), append a new sentence to the `controller_verified_references` paragraph:

Replace the existing line 49:

```markdown
Use `controller_verified_references` for codebase references the controller has already checked before dispatch: paths, symbols, line anchors, commands, or adjacent patterns. The pre-task reviewer suppresses `unverifiable_codebase_claim` only when the task claim and a controller-verified entry match by deterministic substring; contradictions, missing ACs, and ambiguity still surface.
```

with:

```markdown
Use `controller_verified_references` for codebase references the controller has already checked before dispatch: paths, symbols, line anchors, commands, or adjacent patterns. The pre-task reviewer suppresses `unverifiable_codebase_claim` only when the task claim and a controller-verified entry match by deterministic substring; contradictions, missing ACs, ambiguity, and `convention_deviation` findings are NOT suppressed. CVR is a single-category suppression — use `testability_extractions` to suppress `scope_drift` on intentional helper extractions and `codebase_conventions` to actively trigger `convention_deviation` findings.
```

- [ ] **Step 3: Add D3 — `.trimIndent()` raw-string caveat.**

Edit `INTEGRATION.md`. After the new Section 3.6 from Step 1, add:

```markdown
### 3.7 `.trimIndent()` raw-string caveat

When a plan snippet is wrapped in `.trimIndent()` (or any equivalent raw-string trimming construct), multi-line phrases render newlines exactly where they sit in source. A phrase that wraps mid-sentence in the plan markdown for readability will land as a newline at runtime — anti-tangent cannot catch this; the reviewer reads only the source text, not the rendered string.

Two rules for plan authors:

- Keep example phrases on a single source line, even if it makes the markdown wider. Wrapping for prose readability is fine for surrounding commentary, but example strings that the implementation will compare against must be one line in source.
- Where ACs assert on the rendered output, phrase the AC against the *rendered* string, not the source layout. For example, "the output contains the phrase `please decline politely`" beats "the output contains the phrase `please decline\npolitely`" — the second hides a regression behind plan-layout choices.
```

- [ ] **Step 4: Add D4 — language-scoping prose caveat.**

Edit `INTEGRATION.md`. In Section 4 ("## 4. For implementers — the lifecycle protocol"), after the existing 4.2a subsection (around line 361-373) and before the existing Lightweight section (around line 375), add:

```markdown
### 4.2b Language-scoping prose caveat

The text-only reviewer can surface `ambiguous_spec` findings around closure/scoping semantics — Kotlin `var` captured by a lambda, Python nested-scope `nonlocal`, JavaScript `let` vs `const` in arrow bodies, etc. — when the prose AC reads ambiguously even though the verbatim code block in the plan is unambiguous.

Implementer mitigation: trust the verbatim plan code block. Paste it as-is, run the tests, and only deviate from the code if the *tests* disagree with the prose. Do not re-interpret the prose against your own model of the language's scoping rules — the plan author's code block already encodes the intent. If you genuinely cannot reconcile the code and the prose, stop and ask the controller; do not silently rewrite either.
```

- [ ] **Step 5: Add D5 — reposition lightweight callout.**

Edit `INTEGRATION.md`. At the top of Section 4 ("## 4. For implementers — the lifecycle protocol", around line 285), insert a one-paragraph callout BEFORE the existing "If you're an implementing subagent..." sentence:

```markdown
> **Before reaching for the full protocol, check lightweight eligibility.** Many tasks qualify for lightweight mode (skip `validate_task_spec`, skip `check_progress`, keep `validate_completion` as the sanity gate). The full clause below adds ~50 lines of dispatch boilerplate; lightweight is ~15 lines. Lightweight applies when ALL of: (a) the task touches at most two files OR is docs/config/data-only; (b) the task is mechanical with no production-design or test-design choices; (c) the spec includes the literal text, exact diff, exact command, or exact insertion shape. `validate_plan` may pre-annotate tasks with `lightweight_eligible: true` and a `lightweight_reason` — treat those as advisory controller hints rather than permission to skip judgment. See [Lightweight protocol mode](#lightweight-protocol-mode-v031) below for the reference clause.
```

(Markdown auto-anchors lowercase the heading and replace spaces with hyphens; the link uses GitHub's auto-anchor for `### Lightweight protocol mode (v0.3.1+)`. If the anchor is wrong, use a plain prose pointer like "see the Lightweight protocol mode section below" instead.)

Leave the existing detailed `### Lightweight protocol mode (v0.3.1+)` subsection (line 375) where it is — the callout above is a discoverability nudge, not a replacement.

- [ ] **Step 6: Append the CHANGELOG `### Changed` doc bullet.**

Edit `CHANGELOG.md`. Under `### Changed` of `## [0.5.0]`, add:

```markdown
- Integration docs add the normative-test-bodies convention, CVR-scope clarification (single-category suppression; `convention_deviation` not suppressed), `.trimIndent()` raw-string caveat, language-scoping prose caveat, and a lightweight-mode callout at the top of the implementer section. (Doc-only items folded under `### Changed` per project CLAUDE.md convention on Keep-a-Changelog subsections; v0.4.0 used `### Documentation`, which is a divergence — this release re-aligns.)
```

- [ ] **Step 7: Run the full test suite as a smoke check.**

Run: `go test -race ./...`
Expected: PASS. (Markdown-only edits should not affect tests, but the smoke check catches accidental edits to files this task does not intend to touch.)

- [ ] **Step 8: Confirm CHANGELOG covers every code-changing task plus this doc task.**

Read `CHANGELOG.md`'s `## [0.5.0]` block and confirm:
- `### Added` has at least 4 bullets (convention_deviation category, four new task-spec inputs, validate_plan normative_test_bodies, validate_plan exit_contracts, validate_completion exit_contracts).
- `### Changed` has at least 4 bullets (pre.tmpl, testability_extractions suppression, plan.tmpl+chunk, post.tmpl, doc additions).

If any bullet is missing, the earlier task that should have added it forgot — fix at this step by going back to that task's CHANGELOG step.

- [ ] **Step 9: Commit.**

```bash
git add INTEGRATION.md CHANGELOG.md
git commit -m "docs(integration): add D1-D5 v0.5.0 doc updates"
```

---

## Final verification (after all tasks)

Run the canonical smoke after the last commit:

```bash
go test -race ./...
goreleaser release --snapshot --clean --skip=publish
```

The `goreleaser` snapshot is optional — it confirms the release pipeline would still build cleanly. The CHANGELOG enforcement check that CI runs on PR is:

```bash
grep -q '^## \[0.5.0\] - [0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}$' CHANGELOG.md
```

If the date or format is off, CI will reject. Adjust the date string to match the merge day if needed (re-amend the appropriate commit, NOT a separate "fix changelog" commit, because the changelog date should match the heading exactly).

When all of the above is green, the branch is ready for review and merge with `[minor]` in the merge-commit subject per project CLAUDE.md.

