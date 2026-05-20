# anti-tangent-mcp v0.5.2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the eight in-scope items from the v0.5.2 design (`docs/superpowers/specs/2026-05-19-anti-tangent-v0.5.2-design.md`): server-side verdict-severity ladder, session-propagated normative bodies at post-hook, reviewer-emitted major→minor demotion, six new `malformed_evidence` patterns, deterministic Go-side CVR suppression, new `harness_shape_attestation` input + `attestation_contradiction` finding category, `.trimIndent()` pre-task heuristic, and a `check_progress` docs nudge.

**Architecture:** Three architectural moves landed across 20 tasks. (1) `internal/verdict` gains two finalization helpers (`FinalizeVerdict`, `FinalizePlanVerdict`) that derive verdict from finding-severity counts after the parser-side severity floors run. (2) `internal/mcpsrv` rewires the per-task and plan-level pipelines so finalization runs AFTER suppression/rollup AND after clamp insertion, and the partial-recovery path returns a `Result`/`PlanResult` that goes through the same finalizer. (3) `internal/prompts` templates gain four new directives (normative-body rendering at post-hook, demotion rule, `.trimIndent()` heuristic, harness-shape-attestation rendering with the new `attestation_contradiction` category); the four reviewer-output schemas gain `attestation_contradiction` in their `category` enum.

**Tech Stack:** Go 1.22+; embedded JSON Schemas; `text/template` for prompts; `mcp.AddTool` reflection on args structs for MCP input-schema generation; golden-file tests for prompt rendering; `go test -race ./...` as the mainline gate.

**Reference spec sections** (cited as `[spec §X]`): #1 verdict ladder §47–187; #2 normative bodies §188–223; #3 demotion §225–246; #4 malformed_evidence §248–272; #5 CVR claim-level §274–321; #6 harness_shape_attestation §323–425; #7 .trimIndent() §427–451; #9 docs nudge §453–466; migration §485–493; testing §495–508.

---

## File Structure

### Files created

- `internal/verdict/finalize.go` — `FinalizeVerdict(Result) Result` and `FinalizePlanVerdict(*PlanResult)`. New file so finalization logic is reviewable in isolation; verdict pkg already splits parser/verdict/plan across files.
- `internal/verdict/finalize_test.go` — table-driven tests for both helpers (every severity-count branch + idempotence + noise_cluster suppression-on-rerun).
- `internal/mcpsrv/task_spec_normalize_test.go` — focused unit tests for `suppressUnverifiableCodebaseClaim` (new helper) + the existing `suppressTestabilityExtractionScopeDrift`. (Note: if a test file by this name already exists, append to it; the file structure here documents intent.)

### Files modified

- `internal/verdict/verdict.go` — add `CategoryAttestationContradiction` constant.
- `internal/verdict/parser.go` — include `CategoryAttestationContradiction` in `validCategory`.
- `internal/verdict/parser_partial.go` — update `applySeverityFloor` doc comment only; clarify `attestation_contradiction` is NOT floored.
- `internal/verdict/schema.json`, `internal/verdict/plan_schema.json`, `internal/verdict/tasks_only_schema.json`, `internal/verdict/plan_findings_only_schema.json` — add `"attestation_contradiction"` to the `category` enum (4 files, identical one-line addition each).
- `internal/session/session.go` — define `HarnessShapeAttestation` struct AND add `HarnessShapeAttestations []HarnessShapeAttestation` to `TaskSpec`.
- `internal/mcpsrv/task_spec_input.go` — add `normalizeHarnessShapeAttestation` helper; thread normalized value into `taskSpecInputs` and `normalizeTaskSpecInputs`.
- `internal/mcpsrv/task_spec_normalize.go` — add `suppressUnverifiableCodebaseClaim` helper.
- `internal/mcpsrv/handlers.go` — (a) `ValidateTaskSpecArgs` gains `HarnessShapeAttestation` field; (b) per-task handler pipelines reorder clamp insertion into `result.Findings` BEFORE `FinalizeVerdict`; (c) `ValidatePlan` calls `FinalizePlanVerdict` from inside `finalizePlanResult`; (d) `truncatedEnvelope` → `truncatedResult` returning `verdict.Result` with `SeverityMajor`; (e) `tooLargeEnvelope`/`malformedEvidenceEnvelope` synthetic-finding severity bumped to critical; (f) `evidenceTruncationPatterns` slice gains six new entries.
- `internal/mcpsrv/review_error.go` — only `handlePerTaskReviewErr` is rewired (Task 4): it now calls the refactored `recoverPartialFindings` returning a `Result`, folds the clamp into `Result.Findings` BEFORE `FinalizeVerdict`, and assembles the envelope from the finalized `Result`. `handlePlanReviewErr` stays structurally unchanged (see Task 6 Step 4) — `planEnvelopeResult` → `finalizePlanResult` already runs `FinalizePlanVerdict` after Task 6's wiring.
- `internal/prompts/templates/pre.tmpl` — four additions: tightened CVR instruction with worked example; new `## Harness shape attestations` section; major→minor demotion rule; `.trimIndent()` heuristic.
- `internal/prompts/templates/post.tmpl` — two additions: `## Normative test bodies (binding)` section; major→minor demotion rule.
- `internal/prompts/testdata/pre_basic.golden`, `internal/prompts/testdata/post_basic.golden` — regenerated.
- `internal/prompts/prompts_test.go` — `Contains` assertions for every new template directive; converse `NotContains` assertion for the empty-normative-bodies case.
- `internal/mcpsrv/handlers_test.go` — input validation, session round-trip, suppression-ordering, ladder-integration, hard-rejection severity, and `harness_shape_attestation` round-trip tests.
- `INTEGRATION.md` — §3, §4, §5.7, §6 doc updates (new input + new category + check_progress nudge + CVR clarification).
- `README.md` — extend the v0.5.0 list of `validate_task_spec` optional inputs from four to five.
- `CHANGELOG.md` — new `## [0.5.2] - 2026-05-19` entry opened in Task 1; each subsequent task adds its bullet.

### Branch & version

Work happens on the existing `version/0.5.2` branch. Merge into `main` carries no `[major]` / `[minor]` tag → defaults to patch bump per project convention. Branch name matches `## [0.5.2]` in CHANGELOG.

### Testing strategy

Mainline gate: `go test -race ./...` after every task. Goldens regenerated with `go test ./internal/prompts/... -update` only on tasks that change template files; review the diff before staging. Don't commit golden changes without the source-template change in the same commit.

---

## Cross-cutting conventions

### Commit messages

Per project CLAUDE.md, use Keep-a-Changelog subsection prefixes. Bodies are short; the CHANGELOG entry carries the user-facing detail. Examples used in the plan:

- `feat(verdict): add FinalizeVerdict ladder + noise_cluster advisory`
- `feat(mcpsrv): wire FinalizeVerdict into per-task handlers`
- `fix(mcpsrv): bump hard-rejection synthetic-finding severity to critical`
- `feat(prompts): add harness shape attestation section to pre.tmpl`

### CHANGELOG bullet placement

Each task adds its own bullet to the `## [0.5.2]` block under the right `### Added` / `### Changed` / `### Fixed` subsection in the same commit. Per CLAUDE.md: "Add the entry as you write the code, not at the end." Subsection mapping per task is documented at the bottom of each task.

### TDD discipline

Every task follows test-first: write the failing test, run it to confirm it fails for the right reason, write the minimal change, run it to confirm it passes, commit. Run the full suite (`go test -race ./...`) before committing — non-regression matters.

### Test scaffolding reference

All `internal/mcpsrv` tests use the existing pair: a `fakeReviewer` struct (defined at `internal/mcpsrv/handlers_test.go:21-32`) and a `newDeps(t, rv)` constructor (lines 42-57). The pattern for any new test in this plan:

```go
rv := &fakeReviewer{
    name: "anthropic",
    resp: providers.Response{
        RawJSON: []byte(reviewerJSON),
        Model:   "claude-sonnet-4-6",
    },
}
d := newDeps(t, rv)
h := &handlers{deps: d}
```

For truncation errors: add `err: providers.ErrResponseTruncated` to the `fakeReviewer` literal (the existing pattern is shown at `handlers_test.go:521`).

For partial-recovery cases: set BOTH `resp.RawJSON` (to the truncated bytes) AND `err: providers.ErrResponseTruncated` (the existing pattern is shown at `handlers_test.go:605` and `handlers_test.go:1210`).

For tests that need to spy on the prompt sent to the reviewer, see Task 4 Step 0 (one-time extension of `fakeReviewer` to capture the last `providers.Request`).

### Severity-floor (parser-side) is a separate axis from FinalizeVerdict (handler-side)

Two existing categories — `unverifiable_codebase_claim`, `convention_deviation` — are floored to `minor` inside `internal/verdict/parser_partial.go::applySeverityFloor`. That runs at parse time. `FinalizeVerdict` runs at handler time, AFTER suppression and rollup, and computes the verdict label. The two are independent. `attestation_contradiction` is intentionally NOT in `applySeverityFloor`'s list (a reviewer-detected contradiction with caller-attested shape is a hard finding, not "can't verify"). This separation is the rationale behind Task 13's doc-comment update.

---

## Task 1: Open CHANGELOG entry for v0.5.2

**Goal:** Add the `## [0.5.2] - 2026-05-19` header skeleton to `CHANGELOG.md` so every subsequent task can append its bullet in the same commit as the code change (per project CLAUDE.md "add the entry as you write the code").

**Acceptance criteria:**
- A new `## [0.5.2] - 2026-05-19` block appears immediately above `## [0.5.1] - 2026-05-19`.
- The new block contains the standard six Keep-a-Changelog subsection headers (`### Added`, `### Changed`, `### Fixed`, `### Removed`, `### Deprecated`, `### Security`) — each empty.
- `git diff CHANGELOG.md` shows only the seven-line skeleton insertion; no other change.

**Non-goals:**
- No bullets are populated in this task — every subsequent task fills its own subsection.

**Files:**
- Modify: `CHANGELOG.md` (insert immediately after `# Changelog` header and intro, before the `## [0.5.1]` block).

- [ ] **Step 1: Open the v0.5.2 entry with the standard subsection skeleton**

Insert after line 7 (immediately before `## [0.5.1] - 2026-05-19`):

```markdown
## [0.5.2] - 2026-05-19

### Added

### Changed

### Fixed

### Removed

### Deprecated

### Security

```

- [ ] **Step 2: Verify nothing else changed**

Run: `git diff CHANGELOG.md`
Expected: only the seven-line skeleton insertion above `## [0.5.1]`.

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): open 0.5.2 entry"
```

---

## Task 2: Add `FinalizeVerdict` ladder helper

**Goal:** Server computes the canonical verdict from finding severities AFTER the parser-side severity floors run. Adds a `noise_cluster` advisory when the `≥3 minor → warn` branch fires.

**Acceptance criteria:**
- `verdict.FinalizeVerdict(Result) Result` is exported from `internal/verdict/finalize.go`.
- Ladder: `critical >= 1 OR major >= 2 → fail`; `major >= 1 OR minor >= 3 → warn`; otherwise `pass`.
- The reviewer's `Verdict` value on the input `Result` is overwritten by the server-derived value (`TestFinalizeVerdict_OverridesReviewerVerdict`).
- When `critical == 0 AND major == 0 AND minor >= 3`, a single `noise_cluster` advisory (`severity: minor`, `category: other`, `criterion: noise_cluster`) is appended to `Findings`.
- Idempotent: calling twice does not double-append `noise_cluster` (`TestFinalizeVerdict_Idempotent`).
- All eleven cases in the table-driven `TestFinalizeVerdict_Ladder` pass.

**Non-goals:**
- Handler-side wiring is out of scope — that lands in Tasks 4 and 6.
- No template, schema, or input changes.

**Files:**
- Create: `internal/verdict/finalize.go`
- Create: `internal/verdict/finalize_test.go`

- [ ] **Step 1: Write the failing table-driven test**

Create `internal/verdict/finalize_test.go` with:

```go
package verdict

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFinalizeVerdict_Ladder(t *testing.T) {
	type tc struct {
		name             string
		findings         []Finding
		wantVerdict      Verdict
		wantNoiseCluster bool
		wantFindingCount int
	}
	mk := func(s Severity) Finding {
		return Finding{Severity: s, Category: CategoryQuality, Criterion: "x", Evidence: "y", Suggestion: "z"}
	}
	cases := []tc{
		{"empty → pass", nil, VerdictPass, false, 0},
		{"single minor → pass", []Finding{mk(SeverityMinor)}, VerdictPass, false, 1},
		{"two minor → pass", []Finding{mk(SeverityMinor), mk(SeverityMinor)}, VerdictPass, false, 2},
		{"three minor → warn + noise_cluster", []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}, VerdictWarn, true, 4},
		{"four minor → warn + noise_cluster", []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}, VerdictWarn, true, 5},
		{"single major → warn (no noise_cluster)", []Finding{mk(SeverityMajor)}, VerdictWarn, false, 1},
		{"major + three minor → warn (no noise_cluster — major present)", []Finding{mk(SeverityMajor), mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}, VerdictWarn, false, 4},
		{"two major → fail", []Finding{mk(SeverityMajor), mk(SeverityMajor)}, VerdictFail, false, 2},
		{"single critical → fail", []Finding{mk(SeverityCritical)}, VerdictFail, false, 1},
		{"critical + three minor → fail (no noise_cluster)", []Finding{mk(SeverityCritical), mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}, VerdictFail, false, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Result{Verdict: VerdictPass, Findings: append([]Finding(nil), c.findings...), NextAction: "n"}
			out := FinalizeVerdict(r)
			require.Equal(t, c.wantVerdict, out.Verdict)
			require.Len(t, out.Findings, c.wantFindingCount)
			has := false
			for _, f := range out.Findings {
				if f.Category == CategoryOther && f.Criterion == "noise_cluster" {
					has = true
				}
			}
			require.Equal(t, c.wantNoiseCluster, has, "noise_cluster presence")
		})
	}
}

func TestFinalizeVerdict_Idempotent(t *testing.T) {
	r := Result{
		Verdict: VerdictPass,
		Findings: []Finding{
			{Severity: SeverityMinor, Category: CategoryQuality, Criterion: "a", Evidence: "b", Suggestion: "c"},
			{Severity: SeverityMinor, Category: CategoryQuality, Criterion: "a", Evidence: "b", Suggestion: "c"},
			{Severity: SeverityMinor, Category: CategoryQuality, Criterion: "a", Evidence: "b", Suggestion: "c"},
		},
		NextAction: "n",
	}
	once := FinalizeVerdict(r)
	twice := FinalizeVerdict(once)
	require.Equal(t, once.Verdict, twice.Verdict)
	require.Len(t, twice.Findings, len(once.Findings), "second call must not re-append noise_cluster")
}

func TestFinalizeVerdict_OverridesReviewerVerdict(t *testing.T) {
	r := Result{
		Verdict:    VerdictFail, // reviewer-emitted; should be overridden
		Findings:   []Finding{{Severity: SeverityMinor, Category: CategoryQuality, Criterion: "a", Evidence: "b", Suggestion: "c"}},
		NextAction: "n",
	}
	out := FinalizeVerdict(r)
	require.Equal(t, VerdictPass, out.Verdict)
}
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/verdict/ -run TestFinalizeVerdict -race`
Expected: FAIL with "undefined: FinalizeVerdict".

- [ ] **Step 3: Implement `FinalizeVerdict`**

Create `internal/verdict/finalize.go`:

```go
package verdict

import "fmt"

// FinalizeVerdict derives r.Verdict from r.Findings via the severity ladder
//
//	critical >= 1 OR major >= 2  → fail
//	major >= 1 OR minor >= 3     → warn
//	otherwise                    → pass
//
// and appends a `noise_cluster` advisory finding when the `minor >= 3 → warn`
// branch fires AND no critical/major exists. The reviewer's r.Verdict is
// overwritten by the server-derived value. Idempotent: a second call
// observes the noise_cluster advisory it appended and does not append again.
func FinalizeVerdict(r Result) Result {
	var critical, major, minor int
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityCritical:
			critical++
		case SeverityMajor:
			major++
		case SeverityMinor:
			minor++
		}
	}
	switch {
	case critical >= 1 || major >= 2:
		r.Verdict = VerdictFail
	case major >= 1 || minor >= 3:
		r.Verdict = VerdictWarn
	default:
		r.Verdict = VerdictPass
	}
	if critical == 0 && major == 0 && minor >= 3 {
		for _, f := range r.Findings {
			if f.Category == CategoryOther && f.Criterion == "noise_cluster" {
				return r
			}
		}
		r.Findings = append(r.Findings, Finding{
			Severity:   SeverityMinor,
			Category:   CategoryOther,
			Criterion:  "noise_cluster",
			Evidence:   fmt.Sprintf("%d minor findings on this call (no critical or major). Each finding is individually advisory; the cluster lifts verdict to warn.", minor),
			Suggestion: "Inspect the minor findings as a group. If they're all low-signal noise, the next caller iteration can ignore them collectively. If any one warrants escalation, address it individually.",
		})
	}
	return r
}
```

- [ ] **Step 4: Run the tests and confirm pass**

Run: `go test ./internal/verdict/ -race`
Expected: PASS, including existing tests.

- [ ] **Step 5: Add CHANGELOG bullet**

Edit `CHANGELOG.md` under `## [0.5.2]` → `### Added`:

```markdown
- `verdict.FinalizeVerdict(Result) Result` derives the canonical verdict from finding-severity counts via a published ladder: `critical >= 1 OR major >= 2 → fail`; `major >= 1 OR minor >= 3 → warn`; otherwise `pass`. When the `minor >= 3 → warn` branch fires (no critical/major), an advisory `noise_cluster` finding (`severity: minor`, `category: other`, `criterion: noise_cluster`) is appended so callers can see why. Idempotent.
```

- [ ] **Step 6: Commit**

```bash
git add internal/verdict/finalize.go internal/verdict/finalize_test.go CHANGELOG.md
git commit -m "feat(verdict): add FinalizeVerdict ladder + noise_cluster advisory"
```

---

## Task 3: Add `FinalizePlanVerdict` plan ladder helper

**Goal:** Per-task and plan-level finalization in `validate_plan`, with `ApplyPlanQualitySanity` rerun so `PlanQuality` reflects the server-derived `PlanVerdict`.

**Acceptance criteria:**
- `verdict.FinalizePlanVerdict(*PlanResult)` is exported from `internal/verdict/finalize.go`.
- Per-task verdicts are derived via the same severity ladder (delegates to `FinalizeVerdict`).
- Plan-level verdict is derived from `PlanFindings`.
- `noise_cluster` advisories append at task level AND plan level when the `≥3 minor → warn` branch fires.
- `ApplyPlanQualitySanity` is re-applied at the end so `PlanQuality` is consistent with the server-derived `PlanVerdict` (e.g. reviewer-emitted `rigorous` becomes `rough` when finalization concludes `fail`).
- Nil-safe (`TestFinalizePlanVerdict_NilSafe`).
- Idempotent at both task and plan level (`TestFinalizePlanVerdict_Idempotent`).

**Non-goals:**
- Handler-side wiring inside `finalizePlanResult` lands in Task 6.

**Files:**
- Modify: `internal/verdict/finalize.go` (append)
- Modify: `internal/verdict/finalize_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `internal/verdict/finalize_test.go`:

```go
func TestFinalizePlanVerdict_PerTaskAndPlanLadder(t *testing.T) {
	mk := func(s Severity) Finding {
		return Finding{Severity: s, Category: CategoryQuality, Criterion: "x", Evidence: "y", Suggestion: "z"}
	}
	pr := PlanResult{
		PlanVerdict:  VerdictPass, // reviewer-emitted; should be overridden
		PlanFindings: []Finding{mk(SeverityMajor)},
		Tasks: []PlanTaskResult{
			{TaskIndex: 0, TaskTitle: "t0", Verdict: VerdictPass, Findings: []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}},
			{TaskIndex: 1, TaskTitle: "t1", Verdict: VerdictFail, Findings: []Finding{mk(SeverityMinor)}},
		},
		NextAction:  "n",
		PlanQuality: PlanQualityRigorous, // reviewer-emitted; sanity rerun should keep it (warn → actionable default doesn't apply because reviewer's value is valid)
	}
	FinalizePlanVerdict(&pr)
	require.Equal(t, VerdictWarn, pr.PlanVerdict, "≥1 major → warn")
	require.Equal(t, VerdictWarn, pr.Tasks[0].Verdict, "task 0: three minor → warn")
	require.Equal(t, VerdictPass, pr.Tasks[1].Verdict, "task 1: single minor → pass (reviewer fail overridden)")
	// Task 0's noise_cluster advisory appended.
	taskHasNoise := false
	for _, f := range pr.Tasks[0].Findings {
		if f.Category == CategoryOther && f.Criterion == "noise_cluster" {
			taskHasNoise = true
		}
	}
	require.True(t, taskHasNoise, "task 0 should carry noise_cluster")
}

func TestFinalizePlanVerdict_RerunsApplyPlanQualitySanity(t *testing.T) {
	mk := func(s Severity) Finding {
		return Finding{Severity: s, Category: CategoryQuality, Criterion: "x", Evidence: "y", Suggestion: "z"}
	}
	// Reviewer emitted PlanQuality=rigorous but findings force fail.
	pr := PlanResult{
		PlanVerdict:  VerdictPass, // reviewer-emitted; ladder will derive fail
		PlanFindings: []Finding{mk(SeverityCritical)},
		Tasks:        []PlanTaskResult{},
		NextAction:   "n",
		PlanQuality:  PlanQualityRigorous,
	}
	FinalizePlanVerdict(&pr)
	require.Equal(t, VerdictFail, pr.PlanVerdict, "ladder derives fail")
	require.Equal(t, PlanQualityRough, pr.PlanQuality, "ApplyPlanQualitySanity forces rough on fail")
}

func TestFinalizePlanVerdict_Idempotent(t *testing.T) {
	mk := func(s Severity) Finding {
		return Finding{Severity: s, Category: CategoryQuality, Criterion: "x", Evidence: "y", Suggestion: "z"}
	}
	pr := PlanResult{
		PlanVerdict:  VerdictPass,
		PlanFindings: []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)},
		Tasks: []PlanTaskResult{
			{TaskIndex: 0, TaskTitle: "t0", Verdict: VerdictPass, Findings: []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}},
		},
		NextAction: "n",
	}
	FinalizePlanVerdict(&pr)
	beforeLen := len(pr.PlanFindings)
	beforeTaskLen := len(pr.Tasks[0].Findings)
	FinalizePlanVerdict(&pr)
	require.Equal(t, beforeLen, len(pr.PlanFindings), "plan noise_cluster not re-appended")
	require.Equal(t, beforeTaskLen, len(pr.Tasks[0].Findings), "task noise_cluster not re-appended")
}

func TestFinalizePlanVerdict_NilSafe(t *testing.T) {
	FinalizePlanVerdict(nil) // must not panic
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/verdict/ -run TestFinalizePlanVerdict -race`
Expected: FAIL with "undefined: FinalizePlanVerdict".

- [ ] **Step 3: Implement `FinalizePlanVerdict`**

Append to `internal/verdict/finalize.go`:

```go
// FinalizePlanVerdict derives per-task and plan-level verdicts from current
// findings via the severity ladder (see FinalizeVerdict), appends
// noise_cluster advisories where applicable, and re-runs
// ApplyPlanQualitySanity so PlanQuality stays consistent with the
// server-derived PlanVerdict (e.g. reviewer-emitted `rigorous` becomes
// `rough` when finalization concludes `fail`). Mutates *p in place.
// Idempotent. Nil-safe.
func FinalizePlanVerdict(p *PlanResult) {
	if p == nil {
		return
	}
	for i := range p.Tasks {
		tmp := Result{Verdict: p.Tasks[i].Verdict, Findings: p.Tasks[i].Findings}
		tmp = FinalizeVerdict(tmp)
		p.Tasks[i].Verdict = tmp.Verdict
		p.Tasks[i].Findings = tmp.Findings
	}
	tmp := Result{Verdict: p.PlanVerdict, Findings: p.PlanFindings}
	tmp = FinalizeVerdict(tmp)
	p.PlanVerdict = tmp.Verdict
	p.PlanFindings = tmp.Findings
	ApplyPlanQualitySanity(p)
}
```

- [ ] **Step 4: Run the tests and confirm pass**

Run: `go test ./internal/verdict/ -race`
Expected: PASS.

- [ ] **Step 5: Add CHANGELOG bullet**

Edit `CHANGELOG.md` under `### Added`:

```markdown
- `verdict.FinalizePlanVerdict(*PlanResult)` derives per-task verdicts via the same severity ladder, derives the plan-level verdict from `PlanFindings`, appends noise_cluster advisories at task and plan level where applicable, and re-runs `ApplyPlanQualitySanity` so `plan_quality` stays consistent with the server-derived `plan_verdict`. Idempotent. Nil-safe.
```

- [ ] **Step 6: Commit**

```bash
git add internal/verdict/finalize.go internal/verdict/finalize_test.go CHANGELOG.md
git commit -m "feat(verdict): add FinalizePlanVerdict plan ladder"
```

---

## Task 4: Wire `FinalizeVerdict` into per-task handlers (clamp ordering + truncatedResult)

**Goal:** Three per-task handlers (`ValidateTaskSpec`, `CheckProgress`, `ValidateCompletion`) and the per-task truncation path consume `FinalizeVerdict` so verdicts derive from server-computed counts. Clamp findings participate in the ladder by sitting at `result.Findings[0]` before finalization. The per-task `truncatedEnvelope` becomes `truncatedResult` returning a `verdict.Result` with `SeverityMajor` so the ladder derives `warn` consistently.

**Acceptance criteria:**
- `fakeReviewer` carries a `LastRequest providers.Request` field that captures the last reviewer request (Step 0).
- `truncatedEnvelope` is removed; `truncatedResult() verdict.Result` exists with `SeverityMajor` on its synthetic finding.
- `recoverPartialFindings` returns `(verdict.Result, bool)` (signature change — drops the `id` and `model` parameters).
- `handlePerTaskReviewErr` folds the clamp into `result.Findings[0]` BEFORE calling `verdict.FinalizeVerdict`, then assembles the envelope from the finalized result.
- Per-task happy paths in `ValidateTaskSpec` / `CheckProgress` / `ValidateCompletion` insert the clamp into `result.Findings[0]` BEFORE `FinalizeVerdict` and DO NOT call `prependClamp(env, ...)` after envelope construction.
- `TestValidateTaskSpec_FinalizeVerdict_ClampParticipatesInLadder` passes (two reviewer minors + clamp minor → `warn` + `noise_cluster`).
- `TestValidateTaskSpec_SuppressionRunsBeforeFinalize` passes (five suppressed scope_drift findings → `pass`).
- `TestValidateTaskSpec_TruncatedResponseSurfacesWarnWithMajorFinding` passes (single `major` truncation finding → ladder derives `warn`).
- Pre-existing tests that asserted the old `truncatedEnvelope`'s `minor` severity are updated to expect `major`.

**Non-goals:**
- Plan-level finalization (Task 6).
- Hard-rejection severity bumps (Task 5).
- Schema or template changes.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (per-task handler bodies; `truncatedEnvelope` → `truncatedResult`)
- Modify: `internal/mcpsrv/review_error.go` (`handlePerTaskReviewErr` consumes `truncatedResult`; full refactor of partial-recovery + clamp ordering lands here too)
- Modify: `internal/mcpsrv/handlers_test.go` (assertions reflecting the new behavior; `fakeReviewer.LastRequest` field added in Step 0)

**Note:** Task 7 is folded into this task because the clamp-ordering refactor in `handlePerTaskReviewErr` is what makes the per-task truncation path's verdict consistent under the new ladder. Splitting them would leave the codebase in an inconsistent intermediate state across two commits.

- [ ] **Step 0: Extend `fakeReviewer` to capture the last request**

This is a one-time scaffolding extension. Task 16 needs it to assert that the post-hook prompt contains the session-propagated normative bodies; cheap enough to land here so later tasks just use the field.

Edit `internal/mcpsrv/handlers_test.go` lines 21-32. Replace:

```go
type fakeReviewer struct {
	name  string
	resp  providers.Response
	err   error
	Calls int
}

func (f *fakeReviewer) Name() string { return f.name }
func (f *fakeReviewer) Review(ctx context.Context, _ providers.Request) (providers.Response, error) {
	f.Calls++
	return f.resp, f.err
}
```

with:

```go
type fakeReviewer struct {
	name        string
	resp        providers.Response
	err         error
	Calls       int
	LastRequest providers.Request // captured on every Review call; tests inspect rv.LastRequest.User to assert prompt content
}

func (f *fakeReviewer) Name() string { return f.name }
func (f *fakeReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	f.Calls++
	f.LastRequest = req
	return f.resp, f.err
}
```

Run `go test ./internal/mcpsrv/ -race` to confirm no test regressed (the parameter rename from `_` to `req` is backward-compatible; the new field is zero-value for existing tests that don't read it).

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_FinalizeVerdict_ClampParticipatesInLadder(t *testing.T) {
	// Two reviewer-emitted minors + a clamp minor (over-ceiling override) =
	// three minors → ladder derives warn + appends noise_cluster.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"pass","findings":[
				{"severity":"minor","category":"quality","criterion":"a","evidence":"b","suggestion":"c"},
				{"severity":"minor","category":"quality","criterion":"d","evidence":"e","suggestion":"f"}
			],"next_action":"n"}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	d.Cfg.MaxTokensCeiling = 1000
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g",
		AcceptanceCriteria: []string{"ac1"},
		MaxTokensOverride:  10000, // over ceiling → clamp finding (minor)
	})
	require.NoError(t, err)
	require.Equal(t, "warn", env.Verdict, "two minors + clamp minor → warn")
	hasNoise := false
	for _, f := range env.Findings {
		if f.Category == verdict.CategoryOther && f.Criterion == "noise_cluster" {
			hasNoise = true
		}
	}
	require.True(t, hasNoise, "noise_cluster appended on ≥3 minor → warn")
	// Clamp finding sits at Findings[0].
	require.Equal(t, "max_tokens_override", env.Findings[0].Criterion)
}

func TestValidateTaskSpec_SuppressionRunsBeforeFinalize(t *testing.T) {
	// Five scope_drift findings, all matching a testability_extractions entry,
	// should all be suppressed BEFORE FinalizeVerdict runs → verdict pass.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"fail","findings":[
				{"severity":"major","category":"scope_drift","criterion":"a","evidence":"helperFn","suggestion":"x"},
				{"severity":"major","category":"scope_drift","criterion":"b","evidence":"helperFn","suggestion":"x"},
				{"severity":"major","category":"scope_drift","criterion":"c","evidence":"helperFn","suggestion":"x"},
				{"severity":"major","category":"scope_drift","criterion":"d","evidence":"helperFn","suggestion":"x"},
				{"severity":"major","category":"scope_drift","criterion":"e","evidence":"helperFn","suggestion":"x"}
			],"next_action":"n"}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g",
		AcceptanceCriteria:     []string{"ac1"},
		TestabilityExtractions: []string{"helperFn"},
	})
	require.NoError(t, err)
	require.Equal(t, "pass", env.Verdict, "all five suppressed → pass")
	require.Empty(t, env.Findings, "all findings suppressed before finalize")
}

func TestValidateTaskSpec_TruncatedResponseSurfacesWarnWithMajorFinding(t *testing.T) {
	// truncatedResult emits SeverityMajor; FinalizeVerdict derives warn.
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"},
	})
	require.NoError(t, err)
	require.Equal(t, "warn", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity, "truncated finding bumped to major")
	require.Equal(t, "reviewer_response", env.Findings[0].Criterion)
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/mcpsrv/ -run "TestValidateTaskSpec_FinalizeVerdict_ClampParticipatesInLadder|TestValidateTaskSpec_SuppressionRunsBeforeFinalize|TestValidateTaskSpec_TruncatedResponseSurfacesWarnWithMajorFinding" -race`
Expected: FAIL — current behavior either (a) returns reviewer's `verdict` field verbatim, or (b) returns the old `minor` truncation finding.

- [ ] **Step 3: Refactor `truncatedEnvelope` → `truncatedResult`**

Edit `internal/mcpsrv/handlers.go` around lines 390-403. Replace:

```go
func truncatedEnvelope(id string, model config.ModelRef) Envelope {
	return Envelope{
		SessionID: id,
		Verdict:   string(verdict.VerdictWarn),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMinor,
			Category:   verdict.CategoryOther,
			Criterion:  "reviewer_response",
			Evidence:   providers.ErrResponseTruncated.Error(),
			Suggestion: "Raise ANTI_TANGENT_PER_TASK_MAX_TOKENS or pass max_tokens_override and retry.",
		}},
		NextAction: "Retry with a higher max_tokens_override (or raise the configured max-tokens cap).",
		ModelUsed:  model.String(),
	}
}
```

with:

```go
// truncatedResult is the no-recovery fallback for per-task truncation. It
// returns a Result (not an Envelope) so the caller can fold any clamp into
// Findings, run FinalizeVerdict, and assemble the envelope. The synthetic
// finding is SeverityMajor (was minor pre-0.5.2) so the ladder derives warn
// consistently with the previously-explicit Verdict assignment.
func truncatedResult() verdict.Result {
	return verdict.Result{
		Verdict: verdict.VerdictWarn, // overwritten by FinalizeVerdict; set for clarity
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryOther,
			Criterion:  "reviewer_response",
			Evidence:   providers.ErrResponseTruncated.Error(),
			Suggestion: "Raise ANTI_TANGENT_PER_TASK_MAX_TOKENS or pass max_tokens_override and retry.",
		}},
		NextAction: "Retry with a higher max_tokens_override (or raise the configured max-tokens cap).",
	}
}
```

- [ ] **Step 4: Refactor `recoverPartialFindings` → returns `(verdict.Result, bool)`**

In `internal/mcpsrv/handlers.go` (around line 462), replace `recoverPartialFindings` with:

```go
// recoverPartialFindings attempts to extract complete findings from a
// truncated reviewer response. Returns (result, true) when at least one
// finding was recovered; (zero, false) when the caller should fall back to
// truncatedResult.
//
// The returned Result has Verdict left as the parser's value (overwritten by
// FinalizeVerdict downstream), a single minor "truncation marker" finding
// appended after the recovered findings, Partial=true, and NextAction = the
// parsed result's next_action when non-empty, otherwise a generic fallback
// pointing the caller at max_tokens_override.
func recoverPartialFindings(rawJSON []byte, envVar string) (verdict.Result, bool) {
	if len(rawJSON) == 0 {
		return verdict.Result{}, false
	}
	r, ok := verdict.ParseResultPartial(rawJSON)
	if !ok || len(r.Findings) == 0 {
		return verdict.Result{}, false
	}
	marker := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "reviewer_response",
		Evidence:   fmt.Sprintf("reviewer output truncated at the max_tokens cap; %d complete findings recovered", len(r.Findings)),
		Suggestion: "Raise " + envVar + " or pass max_tokens_override on the next call to capture more.",
	}
	r.Findings = append(r.Findings, marker)
	switch {
	case r.NextAction == "":
		r.NextAction = "Address recovered findings; reviewer output was truncated. Re-call with a higher max_tokens_override (or raise " + envVar + ") to capture the full review."
	case !strings.Contains(r.NextAction, "max_tokens_override"):
		r.NextAction = r.NextAction + " Reviewer output was truncated; re-call with a higher max_tokens_override (or raise " + envVar + ") to capture the full review."
	}
	r.Partial = true
	return r, true
}
```

- [ ] **Step 5: Rewire `handlePerTaskReviewErr`**

Edit `internal/mcpsrv/review_error.go`. Replace the body of `handlePerTaskReviewErr` (lines 163-180):

```go
func (h *handlers) handlePerTaskReviewErr(in perTaskReviewErrInputs) (*mcp.CallToolResult, Envelope, bool, error) {
	if in.Err == nil {
		return nil, Envelope{}, false, nil
	}
	if !errors.Is(in.Err, providers.ErrResponseTruncated) {
		return nil, Envelope{}, true, in.Err
	}
	r, ok := recoverPartialFindings(in.PartialRaw, in.EnvVar)
	if !ok {
		r = truncatedResult()
	}
	if in.Clamp.Severity != "" {
		r.Findings = append([]verdict.Finding{in.Clamp}, r.Findings...)
	}
	r = verdict.FinalizeVerdict(r)
	env := Envelope{
		SessionID:  in.SessionID,
		Verdict:    string(r.Verdict),
		Findings:   r.Findings,
		NextAction: r.NextAction,
		ModelUsed:  in.Model.String(),
		Partial:    r.Partial,
	}
	if in.Sess != nil {
		env = h.withSessionTTL(env, in.Sess)
	}
	res, e, err := envelopeResult(env)
	return res, e, true, err
}
```

- [ ] **Step 6: Rewire `ValidateTaskSpec` happy path**

Edit `internal/mcpsrv/handlers.go` lines 128-150. Replace:

```go
	result.Findings = suppressTestabilityExtractionScopeDrift(result.Findings, inputs.TestabilityExtractions)
	result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)

	// Create the session only after the review succeeds so failed reviews
	// don't leave orphan sessions in the store waiting for TTL eviction.
	sess := h.deps.Sessions.Create(spec)
	h.deps.Sessions.SetPreFindings(sess.ID, result.Findings)
	// Re-fetch after SetPreFindings so LastAccessed reflects the final mutation.
	if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
		sess = refreshed
	}

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	env = prependClamp(env, cc.Clamp)
	env = h.withSessionTTL(env, sess)
	return envelopeResult(env)
```

with:

```go
	result.Findings = suppressTestabilityExtractionScopeDrift(result.Findings, inputs.TestabilityExtractions)
	result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
	if cc.Clamp.Severity != "" {
		result.Findings = append([]verdict.Finding{cc.Clamp}, result.Findings...)
	}
	result = verdict.FinalizeVerdict(result)

	// Create the session only after the review succeeds so failed reviews
	// don't leave orphan sessions in the store waiting for TTL eviction.
	sess := h.deps.Sessions.Create(spec)
	h.deps.Sessions.SetPreFindings(sess.ID, result.Findings)
	if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
		sess = refreshed
	}

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	env = h.withSessionTTL(env, sess)
	return envelopeResult(env)
```

- [ ] **Step 7: Rewire `CheckProgress` happy path**

Edit `internal/mcpsrv/handlers.go` lines 325-348. Replace:

```go
	h.deps.Sessions.AppendCheckpoint(sess.ID, session.Checkpoint{
		At:        time.Now(),
		WorkingOn: args.WorkingOn,
		FileCount: len(args.ChangedFiles),
		Verdict:   result.Verdict,
		Findings:  result.Findings,
	})
	// Re-fetch after AppendCheckpoint so LastAccessed reflects the final mutation.
	if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
		sess = refreshed
	}

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	env = prependClamp(env, clamp)
	env = h.withSessionTTL(env, sess)
	return envelopeResult(env)
```

with:

```go
	if clamp.Severity != "" {
		result.Findings = append([]verdict.Finding{clamp}, result.Findings...)
	}
	result = verdict.FinalizeVerdict(result)

	h.deps.Sessions.AppendCheckpoint(sess.ID, session.Checkpoint{
		At:        time.Now(),
		WorkingOn: args.WorkingOn,
		FileCount: len(args.ChangedFiles),
		Verdict:   result.Verdict,
		Findings:  result.Findings,
	})
	if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
		sess = refreshed
	}

	env := Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	env = h.withSessionTTL(env, sess)
	return envelopeResult(env)
```

- [ ] **Step 8: Rewire `ValidateCompletion` happy path**

Edit `internal/mcpsrv/handlers.go` lines 970-991. Replace:

```go
	if !lightweight {
		h.deps.Sessions.SetPostFindings(sess.ID, result.Findings)
		if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
			sess = refreshed
		}
		sessID = sess.ID
	}

	env := Envelope{
		SessionID:  sessID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	env = prependClamp(env, clamp)
	if !lightweight {
		env = h.withSessionTTL(env, sess)
	}
	return envelopeResult(env)
```

with:

```go
	if clamp.Severity != "" {
		result.Findings = append([]verdict.Finding{clamp}, result.Findings...)
	}
	result = verdict.FinalizeVerdict(result)

	if !lightweight {
		h.deps.Sessions.SetPostFindings(sess.ID, result.Findings)
		if refreshed, ok := h.deps.Sessions.Get(sess.ID); ok {
			sess = refreshed
		}
		sessID = sess.ID
	}

	env := Envelope{
		SessionID:  sessID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	}
	if !lightweight {
		env = h.withSessionTTL(env, sess)
	}
	return envelopeResult(env)
```

- [ ] **Step 9: Audit other `prependClamp` callers**

Run: `grep -n "prependClamp" internal/mcpsrv/*.go`
Expected callers remaining (these are hard-rejection envelopes where the explicit `Verdict: fail` is correct and FinalizeVerdict is NOT invoked):
- `notFoundEnvelope` callers (CheckProgress, ValidateCompletion) — keep the trailing `prependClamp(env, clamp)`
- `tooLargeEnvelope` callers — keep
- `malformedEvidenceEnvelope` cache-hit branch (line 903) — keep
- `malformedEvidenceEnvelope` fresh-rejection branch (line 908) — keep

These four call sites are intentionally outside the finalize pipeline; the synthetic-finding severity bumps in Task 5 keep them ladder-consistent in the unlikely event a caller derives a verdict from their findings.

- [ ] **Step 10: Run the full suite**

Run: `go test -race ./...`
Expected: PASS. The three new tests pass. Update any pre-existing test that asserts the old `truncatedEnvelope`'s `minor` severity to expect `major` (search the test file for `truncated` / `SeverityMinor` / `reviewer_response`).

- [ ] **Step 11: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- Per-task handlers (`validate_task_spec`, `check_progress`, `validate_completion`) now derive `verdict` server-side via `FinalizeVerdict` AFTER suppression/rollup AND after the clamp finding is folded into the result, so `max_tokens_override` clamps participate in the severity ladder. The per-task no-recovery truncation finding is bumped from `minor` to `major` so the ladder derives `warn` consistently with the previously-explicit assignment.
```

- [ ] **Step 12: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/review_error.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "feat(mcpsrv): wire FinalizeVerdict into per-task handlers"
```

---

## Task 5: Bump hard-rejection synthetic-finding severity to critical

**Goal:** `tooLargeEnvelope`, `malformedEvidenceEnvelope`, AND the plan-level `tooLargePlanResult` emit a `critical` synthetic finding so any caller (or future code path) that derives a verdict from envelope findings produces `fail` consistently. `notFoundEnvelope` already emits `critical` — confirm with a regression test.

**Acceptance criteria:**
- `tooLargeEnvelope` emits a single `critical` finding (`category: payload_too_large`); pre-existing tests asserting `major` are updated.
- `malformedEvidenceEnvelope` emits a single `critical` finding (`category: malformed_evidence`); pre-existing tests asserting `major` are updated.
- `tooLargePlanResult` emits a single `critical` finding (`category: payload_too_large`); after Task 6 wires `FinalizePlanVerdict`, the ladder derives `fail` from one critical so the explicit `Verdict: fail` stays consistent. `TestValidatePlan_PayloadTooLarge` is updated to assert `SeverityCritical`.
- `notFoundEnvelope` regression test pins `SeverityCritical` (no code change needed).

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (three literal severity changes — `tooLargeEnvelope`, `malformedEvidenceEnvelope`, `tooLargePlanResult`)
- Modify: `internal/mcpsrv/handlers_test.go` (add regression tests; update pre-existing tests asserting `major`)

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestTooLargeEnvelope_SyntheticFindingSeverityIsCritical(t *testing.T) {
	env := tooLargeEnvelope("sess-1", config.ModelRef{Provider: "anthropic", Model: "x"}, 1000, 500, "trim")
	require.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.SeverityCritical, env.Findings[0].Severity)
	require.Equal(t, verdict.CategoryTooLarge, env.Findings[0].Category)
}

func TestMalformedEvidenceEnvelope_SyntheticFindingSeverityIsCritical(t *testing.T) {
	env := malformedEvidenceEnvelope("sess-1", "reason", "model")
	require.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.SeverityCritical, env.Findings[0].Severity)
	require.Equal(t, verdict.CategoryMalformedEvidence, env.Findings[0].Category)
}

func TestNotFoundEnvelope_SyntheticFindingSeverityIsCritical(t *testing.T) {
	// Confirm-only test: notFoundEnvelope was already critical before 0.5.2.
	env := notFoundEnvelope("sess-1", config.ModelRef{Provider: "anthropic", Model: "x"})
	require.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.SeverityCritical, env.Findings[0].Severity)
}

func TestTooLargePlanResult_SyntheticFindingSeverityIsCritical(t *testing.T) {
	pr := tooLargePlanResult(1000, 500)
	require.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	require.Equal(t, verdict.SeverityCritical, pr.PlanFindings[0].Severity)
	require.Equal(t, verdict.CategoryTooLarge, pr.PlanFindings[0].Category)
}
```

- [ ] **Step 2: Run the tests and confirm failures**

Run: `go test ./internal/mcpsrv/ -run "TestTooLargeEnvelope|TestMalformedEvidenceEnvelope|TestNotFoundEnvelope|TestTooLargePlanResult" -race`
Expected: `TestNotFoundEnvelope_…` PASSES; the other three FAIL (current severity is `major`).

- [ ] **Step 3: Bump severities in `handlers.go`**

In `tooLargeEnvelope` (around line 627), change:

```go
Severity:   verdict.SeverityMajor,
```

to:

```go
Severity:   verdict.SeverityCritical,
```

In `malformedEvidenceEnvelope` (around line 826), change:

```go
Severity:   verdict.SeverityMajor,
```

to:

```go
Severity:   verdict.SeverityCritical,
```

In `tooLargePlanResult` (around line 1223), change:

```go
Severity:   verdict.SeverityMajor,
```

to:

```go
Severity:   verdict.SeverityCritical,
```

Also update each function's doc comment to mention the bump rationale (one short line per function: "Critical so the ladder derives fail from one critical, matching the explicit Verdict: fail.").

- [ ] **Step 4: Update pre-existing tests asserting `major`**

Run: `grep -n "SeverityMajor" internal/mcpsrv/handlers_test.go | head -20`
Inspect each hit. Any test that asserts `SeverityMajor` on a finding produced by `tooLargeEnvelope`, `malformedEvidenceEnvelope`, or `tooLargePlanResult` needs the assertion changed to `SeverityCritical`. Likely candidates: `TestValidateCompletion_PayloadTooLargeSuggestsFinalDiff` (line 415), `TestCheckProgress_PayloadTooLarge` (line 389), `TestValidatePlan_PayloadTooLarge` (line 791 — note this test currently asserts `CategoryTooLarge` without severity, but Task 6 will run it through `FinalizePlanVerdict`; pin `SeverityCritical` here so the ladder derives `fail`), any malformed-evidence tests.

For each affected test, change the asserted severity and keep the rest of the assertion intact.

- [ ] **Step 5: Run full suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- Hard-rejection synthetic findings (`payload_too_large` in both per-task and plan-level paths, `malformed_evidence`) bumped from `major` to `critical` so the verdict ladder derives `fail` consistently with the envelopes' explicit `Verdict: fail`. `session_not_found` was already `critical` and is unchanged.
```

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "fix(mcpsrv): bump hard-rejection synthetic-finding severity to critical"
```

---

## Task 6: Wire `FinalizePlanVerdict` into `ValidatePlan`

**Goal:** Plan-level pipeline finalizes verdicts from severities via `FinalizePlanVerdict`, slotted inside the existing `finalizePlanResult` helper so rollup, calibration, and summary-block formatting all continue to run on every exit path (happy, partial-recovery, no-recovery, too-large, no-headings).

**Acceptance criteria:**
- `finalizePlanResult` (in `internal/mcpsrv/handlers.go`) calls `verdict.FinalizePlanVerdict(&pr)` instead of the standalone `verdict.ApplyPlanQualitySanity(&pr)`; the surrounding rollup → calibrate → finalize → summary-block ordering is preserved.
- `handlePlanReviewErr` is unchanged structurally: it still calls `prependPlanClamp(pr, in.Clamp)` then `planEnvelopeResult(pr, modelUsed, in.ReviewMS)` (which internally calls `finalizePlanResult` and therefore the new `FinalizePlanVerdict`). No double-finalize concern because `FinalizePlanVerdict` is idempotent (proved in Task 3) and `planEnvelopeResult` calls `finalizePlanResult` exactly once.
- `TestValidatePlan_FinalizePlanVerdict_ClampParticipatesInLadder` passes (two reviewer minors + clamp minor → `warn` + plan-level `noise_cluster`; clamp at `PlanFindings[0]`).
- `TestTruncatedPlanResult_SeverityIsMajor` passes (regression pin — finding is already `major`; ladder derives `warn`).
- `TestValidatePlan_TruncatedResponse_PreservesFinalizePlanResultSideEffects` passes (the truncation/no-recovery path produces a non-empty `SummaryBlock` and runs the rollup/calibration pipeline — guards against accidentally bypassing `finalizePlanResult`).

**Non-goals:**
- Per-task handler wiring (Task 4).
- The plan-level `tooLargePlanResult` severity bump (Task 5).
- Changing the `handlePlanReviewErr` call shape — keep `planEnvelopeResult` (not `planEnvelopeResultFinalized`) so the rollup/calibration/summary steps run on the truncation path.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (single line inside `finalizePlanResult` — replace `verdict.ApplyPlanQualitySanity(&pr)` with `verdict.FinalizePlanVerdict(&pr)`)
- Modify: `internal/mcpsrv/handlers_test.go` (clamp-participation + truncatedPlanResult regression + truncation-finalizes-side-effects tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidatePlan_FinalizePlanVerdict_ClampParticipatesInLadder(t *testing.T) {
	// Reviewer emits two plan-level minors. Caller passes over-ceiling
	// max_tokens_override → clamp adds a third minor. Plan-level ladder
	// derives warn + appends noise_cluster.
	rv := &fakeReviewer{
		name: "openai",
		resp: providers.Response{
			RawJSON: []byte(`{"plan_verdict":"pass","plan_findings":[
				{"severity":"minor","category":"quality","criterion":"a","evidence":"b","suggestion":"c"},
				{"severity":"minor","category":"quality","criterion":"d","evidence":"e","suggestion":"f"}
			],"tasks":[{"task_index":1,"task_title":"t","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":"","lightweight_eligible":false,"lightweight_reason":"","exit_contracts":[],"exit_contracts_inferred":false}],"next_action":"n","plan_quality":"actionable"}`),
			Model: "gpt-5",
		},
	}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	d.Cfg.MaxTokensCeiling = 1000
	h := &handlers{deps: d}
	planText := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac1\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          planText,
		MaxTokensOverride: 10000, // → clamp minor
	})
	require.NoError(t, err)
	require.Equal(t, verdict.VerdictWarn, pr.PlanVerdict, "two minors + clamp minor → warn")
	hasNoise := false
	for _, f := range pr.PlanFindings {
		if f.Category == verdict.CategoryOther && f.Criterion == "noise_cluster" {
			hasNoise = true
		}
	}
	require.True(t, hasNoise, "plan-level noise_cluster appended")
	require.Equal(t, "max_tokens_override", pr.PlanFindings[0].Criterion, "clamp at PlanFindings[0]")
}

func TestTruncatedPlanResult_SeverityIsMajor(t *testing.T) {
	// Regression confirmation: spec §169 says plan-level truncatedPlanResult
	// is already major; this test pins it.
	pr := truncatedPlanResult()
	require.Equal(t, verdict.VerdictWarn, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	require.Equal(t, verdict.SeverityMajor, pr.PlanFindings[0].Severity)
	require.Equal(t, verdict.PlanQualityRough, pr.PlanQuality)
}
```

- [ ] **Step 2: Run the tests and confirm**

Run: `go test ./internal/mcpsrv/ -run "TestValidatePlan_FinalizePlanVerdict|TestTruncatedPlanResult_SeverityIsMajor" -race`
Expected: `TestTruncatedPlanResult_…` PASSES (regression confirm); `TestValidatePlan_FinalizePlanVerdict_…` FAILS (ladder not wired yet).

- [ ] **Step 3: Wire `FinalizePlanVerdict`**

Edit `internal/mcpsrv/handlers.go` around line 1247 (`finalizePlanResult`). Replace:

```go
func finalizePlanResult(pr verdict.PlanResult, modelUsed string, ms int64) verdict.PlanResult {
	// Order is load-bearing: rollup first so calibration sees no remaining
	// task-level unverifiable findings; calibrate before sanity because
	// calibration owns the verdict→quality mapping for the unverifiable-only
	// case (sanity is then a passthrough on the already-valid values).
	normalizePlanUnverifiableFindings(&pr)
	calibratePlanVerdictForUnverifiableOnly(&pr)
	verdict.ApplyPlanQualitySanity(&pr)
	pr.SummaryBlock = formatPlanSummary(pr, modelUsed, ms)
	return pr
}
```

with:

```go
func finalizePlanResult(pr verdict.PlanResult, modelUsed string, ms int64) verdict.PlanResult {
	// Order is load-bearing:
	//   1. rollup unverifiable-codebase-claim findings (else calibration sees
	//      noise);
	//   2. calibrate verdict for the unverifiable-only case (preserves the
	//      v0.4.0 verdict→quality mapping for plans whose only findings are
	//      unverifiable claims);
	//   3. FinalizePlanVerdict (per-task + plan-level severity ladder +
	//      noise_cluster + ApplyPlanQualitySanity rerun).
	// FinalizePlanVerdict's ApplyPlanQualitySanity rerun replaces the
	// stand-alone call this function previously made.
	normalizePlanUnverifiableFindings(&pr)
	calibratePlanVerdictForUnverifiableOnly(&pr)
	verdict.FinalizePlanVerdict(&pr)
	pr.SummaryBlock = formatPlanSummary(pr, modelUsed, ms)
	return pr
}
```

- [ ] **Step 4: Confirm `handlePlanReviewErr` stays untouched**

Read `internal/mcpsrv/review_error.go::handlePlanReviewErr` (lines 113-130). The current shape is:

```go
pr, ok := recoverPartialPlanFindings(in.PartialRaw, in.Prior)
if !ok {
    pr = truncatedPlanResult()
}
pr = prependPlanClamp(pr, in.Clamp)
modelUsed := in.ModelUsed
if modelUsed == "" {
    modelUsed = in.Model.String()
}
r, p, err := planEnvelopeResult(pr, modelUsed, in.ReviewMS)
```

This is already correct under v0.5.2: `prependPlanClamp` folds the clamp into `pr.PlanFindings` BEFORE the envelope is built, so when `planEnvelopeResult` calls `finalizePlanResult` (which now runs `FinalizePlanVerdict` per Step 3), the clamp finding participates in the ladder AND the rollup, calibration, and `formatPlanSummary` steps still run.

Do NOT replace this with a direct `verdict.FinalizePlanVerdict(&pr)` followed by `planEnvelopeResultFinalized` — that would bypass `finalizePlanResult` and lose `normalizePlanUnverifiableFindings`, `calibratePlanVerdictForUnverifiableOnly`, and `formatPlanSummary` (the last of which populates `SummaryBlock`).

- [ ] **Step 5: Write the failing truncation-side-effects test**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidatePlan_TruncatedResponse_PreservesFinalizePlanResultSideEffects(t *testing.T) {
	// Trigger the truncation no-recovery path and assert that
	// finalizePlanResult's side effects (SummaryBlock formatting,
	// ApplyPlanQualitySanity, calibration) still run after Task 6's
	// wiring change. Guards against accidental bypass via direct
	// planEnvelopeResultFinalized calls.
	rv := &fakeReviewer{name: "openai", err: providers.ErrResponseTruncated}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	planText := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac1\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planText})
	require.NoError(t, err)
	// Ladder derives warn from the single major truncation finding.
	require.Equal(t, verdict.VerdictWarn, pr.PlanVerdict)
	// finalizePlanResult side effects must have run.
	require.NotEmpty(t, pr.SummaryBlock, "formatPlanSummary must run on the truncation path")
	require.Equal(t, verdict.PlanQualityRough, pr.PlanQuality, "ApplyPlanQualitySanity (via FinalizePlanVerdict) must have run; truncatedPlanResult explicitly sets rough")
}
```

- [ ] **Step 6: Run and confirm pass**

Run: `go test -race ./...`
Expected: PASS, including the new test.

- [ ] **Step 7: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- `validate_plan` derives per-task and plan-level verdicts server-side via `FinalizePlanVerdict`, which slots into the existing `finalizePlanResult` pipeline after unverifiable-rollup and calibration. The plan-level `max_tokens_override` clamp now participates in the severity ladder. The plan-level no-analysis truncation finding remains `major` (already was — confirmed by regression test).
```

- [ ] **Step 8: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "feat(mcpsrv): wire FinalizePlanVerdict into validate_plan"
```

---

## Task 7: Extend `evidenceTruncationPatterns` with six new patterns

**Goal:** Catch six additional placeholder/truncation patterns observed in field reports: `/* ... */`, `/* ...rest unchanged */`, `// snip`, `// elided`, `// ... rest unchanged`, `/...`.

**Acceptance criteria:**
- `evidenceTruncationPatterns` includes the six new lowercased literal substrings.
- `checkEvidenceShape` rejects each pattern in BOTH `final_diff` AND `final_files[].content` (parametric `TestCheckEvidenceShape_NewPatternsRejected` covers all 12 combinations).
- The rejection reason names the input field (`"final_diff"` or `"final_files"`) so callers can identify which field tripped the guard.
- No false positives on legitimate code: `grep -rn "/\\.\\.\\." internal/ --include='*.go'` (excluding test files) is reviewed before committing.

**Non-goals:**
- No new categories; the rejection still uses `category: malformed_evidence`.
- No change to the rejection cache or the rest of `checkEvidenceShape`.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (extend the `evidenceTruncationPatterns` slice around line 690)
- Modify: `internal/mcpsrv/handlers_test.go` (parametric per-pattern test on both `final_diff` AND `final_files[].content`)

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestCheckEvidenceShape_NewPatternsRejected(t *testing.T) {
	// Per spec §256–264. Each pattern must match in BOTH final_diff AND
	// final_files[].content because the walker iterates both via the same
	// pattern slice.
	patterns := []string{
		"/* ... */",
		"/* ...rest unchanged */",
		"// snip",
		"// elided",
		"// ... rest unchanged",
		"/...",
	}
	for _, p := range patterns {
		t.Run("final_diff:"+p, func(t *testing.T) {
			args := ValidateCompletionArgs{
				SessionID: "s",
				Summary:   "s",
				FinalDiff: "valid header\n" + p + "\nmore",
			}
			reason := checkEvidenceShape(args)
			require.NotEmpty(t, reason, "must reject %q in final_diff", p)
			require.Contains(t, reason, "final_diff")
		})
		t.Run("final_files:"+p, func(t *testing.T) {
			args := ValidateCompletionArgs{
				SessionID:  "s",
				Summary:    "s",
				FinalFiles: []FileArg{{Path: "foo.go", Content: "valid header\n" + p + "\nmore"}},
			}
			reason := checkEvidenceShape(args)
			require.NotEmpty(t, reason, "must reject %q in final_files[].content", p)
			require.Contains(t, reason, "final_files")
		})
	}
}
```

- [ ] **Step 2: Run and confirm failures**

Run: `go test ./internal/mcpsrv/ -run TestCheckEvidenceShape_NewPatternsRejected -race`
Expected: FAIL — none of the new patterns are in the slice yet.

- [ ] **Step 3: Extend the slice**

Edit `internal/mcpsrv/handlers.go` around line 690. Replace:

```go
var evidenceTruncationPatterns = []string{
	"(truncated)",
	"[truncated]",
	"// ... unchanged",
	"<!-- truncated -->",
}
```

with:

```go
// evidenceTruncationPatterns are case-insensitive substrings that strongly
// indicate the caller pasted truncated/elided evidence rather than a complete
// diff or full file contents. The checkEvidenceShape walker applies every
// entry to BOTH final_diff AND every final_files[].content, so adding a
// pattern here automatically extends both inputs. Patterns must be
// lowercased; the walker calls strings.ToLower on the input first.
//
// The list is intentionally narrow: only patterns that have negligible chance
// of appearing in legitimate code or diffs. "diff --git with zero @@" is NOT
// included — it false-fires on mode-only / rename-only / binary diffs.
var evidenceTruncationPatterns = []string{
	"(truncated)",
	"[truncated]",
	"// ... unchanged",
	"<!-- truncated -->",
	// Added in v0.5.2 from field reports:
	"/* ... */",
	"/* ...rest unchanged */",
	"// snip",
	"// elided",
	"// ... rest unchanged",
	"/...",
}
```

- [ ] **Step 4: Run and confirm pass**

Run: `go test -race ./...`
Expected: PASS. If any pre-existing test now false-positives on real-looking content, audit the fixture — none of the new patterns should appear in legitimate test fixtures, but the `/...` substring is short enough to warrant a manual grep first:

```bash
grep -rn "/\\.\\.\\." internal/ --include="*.go" | grep -v _test.go
```

If a legitimate use surfaces, narrow the pattern (e.g. anchor to whitespace before `/...`); otherwise proceed.

- [ ] **Step 5: Add CHANGELOG bullet**

Under `### Fixed`:

```markdown
- `validate_completion` `malformed_evidence` shape-guard extended with six new placeholder/truncation patterns observed in the field: `/* ... */`, `/* ...rest unchanged */`, `// snip`, `// elided`, `// ... rest unchanged`, `/...`. Each is matched (case-insensitive substring) against BOTH `final_diff` AND every `final_files[].content`.
```

- [ ] **Step 6: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "fix(mcpsrv): extend malformed_evidence patterns with field-observed markers"
```

---

## Task 8: Add Go-side `suppressUnverifiableCodebaseClaim` helper

**Goal:** Belt-and-suspenders deterministic suppression of `unverifiable_codebase_claim` findings against the caller's CVR entries. Substring match in either direction. 4-code-point floor on CVR entries. Treat empty `evidence` AND empty `criterion` as non-match.

**Acceptance criteria:**
- `suppressUnverifiableCodebaseClaim(findings []verdict.Finding, cvr []string) []verdict.Finding` exists in `internal/mcpsrv/task_spec_normalize.go`.
- Non-`unverifiable_codebase_claim` findings pass through unchanged (`TestSuppressUnverifiableCodebaseClaim_NonUnverifiableUntouched`).
- Substring match works in BOTH directions — entry contains claim, or claim contains entry (`TestSuppressUnverifiableCodebaseClaim_BothDirections`).
- CVR entries shorter than 4 code points are skipped during matching (`TestSuppressUnverifiableCodebaseClaim_ShortCVREntryIgnored`).
- Empty `evidence` AND empty `criterion` does not match (`TestSuppressUnverifiableCodebaseClaim_EmptyEvidenceAndCriterion_NotSuppressed`).
- Empty CVR list OR empty findings list short-circuits to the input (no allocation) (`TestSuppressUnverifiableCodebaseClaim_EmptyInputs_ShortCircuit`).

**Non-goals:**
- Handler wiring (Task 9).
- Prompt clarification (Task 10).

**Files:**
- Modify: `internal/mcpsrv/task_spec_normalize.go` (append helper)
- Modify: `internal/mcpsrv/task_spec_normalize_test.go` (create if absent, append if present)

- [ ] **Step 1: Write the failing tests**

Create or append to `internal/mcpsrv/task_spec_normalize_test.go`:

```go
package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestSuppressUnverifiableCodebaseClaim_MultiSymbolClaim_SinglePathMatch(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "spec",
		Evidence:   `claim 'XService.findFoo at path/to/file.kt:L42 returns a sorted slice'`,
		Suggestion: "verify",
	}}
	cvr := []string{"path/to/file.kt"}
	out := suppressUnverifiableCodebaseClaim(findings, cvr)
	require.Empty(t, out, "claim suppressed by single-substring match")
}

func TestSuppressUnverifiableCodebaseClaim_NoMatch_Retained(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "spec",
		Evidence:   `claim 'YService.bar in elsewhere/file.kt'`,
		Suggestion: "verify",
	}}
	cvr := []string{"path/to/file.kt"}
	out := suppressUnverifiableCodebaseClaim(findings, cvr)
	require.Len(t, out, 1, "no overlap → retained")
}

func TestSuppressUnverifiableCodebaseClaim_ShortCVREntryIgnored(t *testing.T) {
	// 4-code-point floor: "T" is too short to match.
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "spec",
		Evidence:   "T.foo at file.kt",
		Suggestion: "verify",
	}}
	cvr := []string{"T", "abc"} // both < 4 code points
	out := suppressUnverifiableCodebaseClaim(findings, cvr)
	require.Len(t, out, 1, "short CVR entries not used for matching")
}

func TestSuppressUnverifiableCodebaseClaim_EmptyEvidenceAndCriterion_NotSuppressed(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "",
		Evidence:   "",
		Suggestion: "verify",
	}}
	out := suppressUnverifiableCodebaseClaim(findings, []string{"abcd"})
	require.Len(t, out, 1, "empty evidence + criterion not suppressed (avoid strings.Contains empty-string trap)")
}

func TestSuppressUnverifiableCodebaseClaim_NonUnverifiableUntouched(t *testing.T) {
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryAmbiguousSpec,
		Criterion:  "spec",
		Evidence:   "path/to/file.kt is ambiguous",
		Suggestion: "rewrite",
	}}
	out := suppressUnverifiableCodebaseClaim(findings, []string{"path/to/file.kt"})
	require.Len(t, out, 1, "non-unverifiable category not affected")
}

func TestSuppressUnverifiableCodebaseClaim_BothDirections(t *testing.T) {
	// Evidence is substring of CVR entry: should also suppress.
	findings := []verdict.Finding{{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "spec",
		Evidence:   "file.kt",
		Suggestion: "verify",
	}}
	cvr := []string{"path/to/file.kt"}
	out := suppressUnverifiableCodebaseClaim(findings, cvr)
	require.Empty(t, out, "evidence-as-substring-of-CVR-entry direction also suppresses")
}

func TestSuppressUnverifiableCodebaseClaim_EmptyInputs_ShortCircuit(t *testing.T) {
	require.Nil(t, suppressUnverifiableCodebaseClaim(nil, []string{"x"}))
	some := []verdict.Finding{{Category: verdict.CategoryUnverifiableCodebaseClaim}}
	out := suppressUnverifiableCodebaseClaim(some, nil)
	require.Equal(t, len(some), len(out), "empty CVR short-circuits to input")
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/mcpsrv/ -run TestSuppressUnverifiableCodebaseClaim -race`
Expected: FAIL with "undefined: suppressUnverifiableCodebaseClaim".

- [ ] **Step 3: Implement the helper**

Append to `internal/mcpsrv/task_spec_normalize.go`:

```go
// suppressUnverifiableCodebaseClaim drops any unverifiable_codebase_claim
// finding whose evidence OR criterion substring-matches any CVR entry (either
// direction: entry-is-substring-of-text OR text-is-substring-of-entry). It
// mirrors the prompt-side instruction in pre.tmpl §48 but provides
// deterministic, reviewer-compliance-independent behavior.
//
// Defensive guards mirror suppressTestabilityExtractionScopeDrift:
//   - empty CVR or empty findings short-circuits to the input slice
//   - empty/whitespace-only evidence AND criterion is treated as non-match
//     (avoids the strings.Contains(non_empty, "") trap)
//   - CVR entries shorter than 4 code points are skipped (avoid single-letter
//     false matches like a CVR entry of "T" swallowing every claim)
//
// Findings whose category is NOT unverifiable_codebase_claim pass through
// unchanged.
func suppressUnverifiableCodebaseClaim(findings []verdict.Finding, cvr []string) []verdict.Finding {
	if len(cvr) == 0 || len(findings) == 0 {
		return findings
	}
	// Pre-filter CVR for the 4-code-point floor so we don't re-check on every
	// finding.
	usable := make([]string, 0, len(cvr))
	for _, e := range cvr {
		if len([]rune(e)) >= 4 {
			usable = append(usable, e)
		}
	}
	if len(usable) == 0 {
		return findings
	}
	out := make([]verdict.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Category != verdict.CategoryUnverifiableCodebaseClaim {
			out = append(out, f)
			continue
		}
		evidence := strings.TrimSpace(f.Evidence)
		criterion := strings.TrimSpace(f.Criterion)
		if evidence == "" && criterion == "" {
			out = append(out, f)
			continue
		}
		matched := false
		for _, e := range usable {
			if evidence != "" && (strings.Contains(evidence, e) || strings.Contains(e, evidence)) {
				matched = true
				break
			}
			if criterion != "" && (strings.Contains(criterion, e) || strings.Contains(e, criterion)) {
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

- [ ] **Step 4: Run and confirm pass**

Run: `go test ./internal/mcpsrv/ -run TestSuppressUnverifiableCodebaseClaim -race`
Expected: PASS.

- [ ] **Step 5: Commit (no CHANGELOG entry yet — wire-up commit in Task 9 adds it)**

```bash
git add internal/mcpsrv/task_spec_normalize.go internal/mcpsrv/task_spec_normalize_test.go
git commit -m "feat(mcpsrv): add suppressUnverifiableCodebaseClaim helper"
```

---

## Task 9: Wire `suppressUnverifiableCodebaseClaim` into `ValidateTaskSpec`

**Goal:** Call the new suppression helper between `suppressTestabilityExtractionScopeDrift` and `normalizeTaskSpecUnverifiableFindings` so suppression runs before rollup (avoids dropping the synthetic `codebase_reference_checklist` finding).

**Acceptance criteria:**
- The call order inside `ValidateTaskSpec` is: `suppressTestabilityExtractionScopeDrift` → `suppressUnverifiableCodebaseClaim` → `normalizeTaskSpecUnverifiableFindings` → clamp insert → `FinalizeVerdict`.
- `TestValidateTaskSpec_CVRSuppressesUnverifiableClaim_ClaimLevel` passes (single CVR entry suppresses a multi-symbol claim → `pass` with empty findings).
- `TestValidateTaskSpec_CVRSuppression_RunsBeforeRollup` passes (two suppressible unverifiables produce zero findings post-rollup — rollup sees zero unverifiables to consolidate).

**Non-goals:**
- Prompt-side worked example (Task 10).
- Multi-substring fuzzy matching (out of scope per design §284-294).

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (single line insertion in `ValidateTaskSpec`)
- Modify: `internal/mcpsrv/handlers_test.go` (integration test confirming ordering and end-to-end suppression)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_CVRSuppressesUnverifiableClaim_ClaimLevel(t *testing.T) {
	// Reviewer emits an unverifiable_codebase_claim. CVR matches one substring
	// of the claim's evidence → entire finding suppressed.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[{
				"severity":"minor",
				"category":"unverifiable_codebase_claim",
				"criterion":"spec",
				"evidence":"XService.findFoo at path/to/file.kt:L42",
				"suggestion":"verify"
			}],"next_action":"n"}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"},
		ControllerVerifiedReferences: []string{"path/to/file.kt"},
	})
	require.NoError(t, err)
	require.Empty(t, env.Findings, "claim suppressed by CVR substring match")
	require.Equal(t, "pass", env.Verdict, "no findings → pass")
}

func TestValidateTaskSpec_CVRSuppression_RunsBeforeRollup(t *testing.T) {
	// If suppression ran AFTER rollup, the synthetic codebase_reference_checklist
	// finding wouldn't be dropped, leaving a spurious warn. This test ensures
	// suppression runs FIRST and rollup sees zero unverifiable findings.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"path/to/file.kt:L1","suggestion":"v"},
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"path/to/file.kt:L2","suggestion":"v"}
			],"next_action":"n"}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"},
		ControllerVerifiedReferences: []string{"path/to/file.kt"},
	})
	require.NoError(t, err)
	require.Empty(t, env.Findings, "both unverifiables suppressed before rollup; rollup sees zero")
}
```

- [ ] **Step 2: Run and confirm failures**

Run: `go test ./internal/mcpsrv/ -run TestValidateTaskSpec_CVR -race`
Expected: FAIL — CVR Go-side suppression isn't wired yet.

- [ ] **Step 3: Wire the call**

Edit `internal/mcpsrv/handlers.go` in `ValidateTaskSpec` (around line 128). Replace:

```go
result.Findings = suppressTestabilityExtractionScopeDrift(result.Findings, inputs.TestabilityExtractions)
result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
if cc.Clamp.Severity != "" {
    result.Findings = append([]verdict.Finding{cc.Clamp}, result.Findings...)
}
result = verdict.FinalizeVerdict(result)
```

with:

```go
result.Findings = suppressTestabilityExtractionScopeDrift(result.Findings, inputs.TestabilityExtractions)
result.Findings = suppressUnverifiableCodebaseClaim(result.Findings, inputs.ControllerVerifiedReferences)
result.Findings = normalizeTaskSpecUnverifiableFindings(result.Findings)
if cc.Clamp.Severity != "" {
    result.Findings = append([]verdict.Finding{cc.Clamp}, result.Findings...)
}
result = verdict.FinalizeVerdict(result)
```

- [ ] **Step 4: Run and confirm pass**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- `controller_verified_references` suppression for `unverifiable_codebase_claim` findings now runs server-side (deterministic Go-side) in addition to the existing reviewer-prompt instruction. Suppression scope is per-claim: any CVR-entry substring match against the finding's `evidence` or `criterion` (either direction) suppresses the entire finding. 4-code-point floor on CVR entries prevents single-letter false matches.
```

- [ ] **Step 6: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "feat(mcpsrv): wire suppressUnverifiableCodebaseClaim into ValidateTaskSpec"
```

---

## Task 10: Tighten pre.tmpl CVR instruction with worked example

**Goal:** Prompt-side defense in depth — give the reviewer an explicit multi-symbol example so prompt-driven suppression matches the Go-side semantics.

**Acceptance criteria:**
- `pre.tmpl` line-48-era CVR instruction explicitly states "Suppression is per-claim: a single matching substring suppresses the entire claim even if the claim mentions other symbols not in the CVR list."
- The worked example `XService.findFoo at path/to/file.kt:L42` with CVR `["path/to/file.kt"]` is in the template.
- `pre_basic.golden` regenerated and committed alongside the template change.
- `TestRenderPre_CVRInstructionIncludesMultiSymbolExample` passes with both substring assertions.

**Non-goals:**
- No code change (suppression is already wired in Task 9).

**Files:**
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/testdata/pre_basic.golden`
- Modify: `internal/prompts/prompts_test.go` (add `Contains` assertion)

- [ ] **Step 1: Write the failing test**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_CVRInstructionIncludesMultiSymbolExample(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: session.TaskSpec{
		Title: "t", Goal: "g",
		AcceptanceCriteria:           []string{"ac1"},
		ControllerVerifiedReferences: []string{"path/to/file.kt"},
	}})
	require.NoError(t, err)
	require.Contains(t, out.User, "XService.findFoo at path/to/file.kt:L42")
	require.Contains(t, out.User, "the path matches one of the claim's substrings")
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/prompts/ -run TestRenderPre_CVRInstructionIncludesMultiSymbolExample -race`
Expected: FAIL — the worked example isn't in the template yet.

- [ ] **Step 3: Edit pre.tmpl**

In `internal/prompts/templates/pre.tmpl`, find the existing line 48 instruction:

```
If a Controller-verified references section is present, treat those entries as caller-supplied attestations that the controller grep-verified specific codebase references before dispatch. Suppress an `unverifiable_codebase_claim` for claim C only when some entry in controller_verified_references is a substring of C, or C is a substring of some entry. Do not suppress logical contradictions, missing acceptance criteria, or ambiguity findings.
```

Replace with:

```
If a Controller-verified references section is present, treat those entries as caller-supplied attestations that the controller grep-verified specific codebase references before dispatch. Suppress an `unverifiable_codebase_claim` for claim C when some entry in controller_verified_references is a substring of C, or C is a substring of some entry. Suppression is per-claim: a single matching substring suppresses the entire claim even if the claim mentions other symbols not in the CVR list.

Example: claim "XService.findFoo at path/to/file.kt:L42 returns a sorted slice" with CVR `["path/to/file.kt"]` → suppress (the path matches one of the claim's substrings).

Do not suppress logical contradictions, missing acceptance criteria, or ambiguity findings.
```

- [ ] **Step 4: Regenerate the golden**

Run: `go test ./internal/prompts/... -update -race`
Then: `git diff internal/prompts/testdata/pre_basic.golden`
Expected: the golden gains the new CVR example block; nothing else changed. Stage the golden along with the template.

- [ ] **Step 5: Run the test**

Run: `go test ./internal/prompts/ -race`
Expected: PASS.

- [ ] **Step 6: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- `pre.tmpl` CVR-suppression instruction now includes a worked multi-symbol example, mirroring the Go-side `suppressUnverifiableCodebaseClaim` semantics.
```

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/templates/pre.tmpl internal/prompts/testdata/pre_basic.golden internal/prompts/prompts_test.go CHANGELOG.md
git commit -m "feat(prompts): clarify CVR suppression with worked example"
```

---

## Task 11: Add `attestation_contradiction` to schemas, parser, and verdict constant

**Goal:** New finding category visible end-to-end:
- 4 reviewer-output JSON schemas (enum value)
- `internal/verdict/verdict.go` (Go constant)
- `internal/verdict/parser.go::validCategory` (allowlist)
- `internal/verdict/parser_partial.go::applySeverityFloor` (doc comment clarifying NOT floored)

**Acceptance criteria:**
- `CategoryAttestationContradiction Category = "attestation_contradiction"` exists in `internal/verdict/verdict.go`.
- `validCategory` accepts the new category (`internal/verdict/parser.go`).
- All four reviewer-output JSON schema files include `"attestation_contradiction"` in the `category` enum.
- The v0.5.1 `TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode` regression test continues to pass (enum addition does not affect required/properties parity).
- `applySeverityFloor` doc comment explains that `attestation_contradiction` is intentionally NOT floored.
- `TestParse_AttestationContradiction_AcceptedAndNotFloored` passes — a reviewer-emitted `major` finding with the new category parses through with severity unchanged.

**Non-goals:**
- The reviewer-prompt instruction to emit this category (lands in Task 15 inside the new `pre.tmpl` section).
- The `harness_shape_attestation` input shape (Tasks 12-14).

**Files:**
- Modify: `internal/verdict/schema.json`, `plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`
- Modify: `internal/verdict/verdict.go`
- Modify: `internal/verdict/parser.go`
- Modify: `internal/verdict/parser_partial.go` (doc comment only)
- Modify: `internal/verdict/parser_test.go` (add test that major attestation_contradiction stays major)

- [ ] **Step 1: Write the failing test**

Append to `internal/verdict/parser_test.go`:

```go
func TestParse_AttestationContradiction_AcceptedAndNotFloored(t *testing.T) {
	raw := []byte(`{
		"verdict":"warn",
		"findings":[{
			"severity":"major",
			"category":"attestation_contradiction",
			"criterion":"records emitted spans",
			"evidence":"AC asserts no spans recorded",
			"suggestion":"Revise AC or harness"
		}],
		"next_action":"address contradiction"
	}`)
	r, err := Parse(raw)
	require.NoError(t, err, "attestation_contradiction must be a valid category")
	require.Len(t, r.Findings, 1)
	require.Equal(t, SeverityMajor, r.Findings[0].Severity, "attestation_contradiction must NOT be floored to minor")
	require.Equal(t, CategoryAttestationContradiction, r.Findings[0].Category)
}
```

- [ ] **Step 2: Confirm the failure**

Run: `go test ./internal/verdict/ -run TestParse_AttestationContradiction -race`
Expected: FAIL — currently the parser rejects unknown categories with `invalid category "attestation_contradiction"`.

- [ ] **Step 3: Add the Go constant**

Edit `internal/verdict/verdict.go`. Append to the `Category` const block (around line 55, immediately before `CategoryOther`):

```go
	// CategoryAttestationContradiction is emitted by the reviewer when an
	// acceptance criterion explicitly contradicts a caller-attested harness
	// shape (see HarnessShapeAttestation on TaskSpec). Distinct from
	// convention_deviation: attestations are caller-attested shape facts, so
	// a reviewer-detected contradiction is a hard finding, not "can't
	// verify." Intentionally NOT in applySeverityFloor's list — the
	// reviewer's chosen severity (typically major) is preserved.
	CategoryAttestationContradiction Category = "attestation_contradiction"
```

- [ ] **Step 4: Add to parser allowlist**

Edit `internal/verdict/parser.go::validCategory` (around line 54). Change:

```go
case CategoryMissingAC, CategoryScopeDrift, CategoryAmbiguousSpec,
    CategoryUnaddressed, CategoryQuality, CategorySessionMissing,
    CategoryTooLarge, CategoryUnverifiableCodebaseClaim,
    CategoryConventionDeviation, CategoryOther:
    return true
```

to:

```go
case CategoryMissingAC, CategoryScopeDrift, CategoryAmbiguousSpec,
    CategoryUnaddressed, CategoryQuality, CategorySessionMissing,
    CategoryTooLarge, CategoryUnverifiableCodebaseClaim,
    CategoryConventionDeviation, CategoryAttestationContradiction,
    CategoryOther:
    return true
```

- [ ] **Step 5: Update `applySeverityFloor` doc comment**

Edit `internal/verdict/parser_partial.go` around line 9. Replace the doc comment for `applySeverityFloor`:

```go
// applySeverityFloor enforces the category-based severity floors that
// match the strict parser's behavior, so partial-recovery output is
// consistent with strict output. Floored categories:
//   - unverifiable_codebase_claim → minor (reviewer can't verify the claim)
//   - convention_deviation → minor (reviewer can't know if implementation will deviate)
```

with:

```go
// applySeverityFloor enforces the category-based severity floors that
// match the strict parser's behavior, so partial-recovery output is
// consistent with strict output. Floored categories:
//   - unverifiable_codebase_claim → minor (reviewer can't verify the claim)
//   - convention_deviation → minor (reviewer can't know if implementation will deviate)
//
// attestation_contradiction is intentionally NOT floored: attestations are
// caller-attested shape facts, so a reviewer-detected contradiction with the
// AC's prose is a hard finding (the reviewer's chosen severity — typically
// major — is preserved).
```

No code change to the function body — `attestation_contradiction` is not in the function's switch, so it's not floored automatically.

- [ ] **Step 6: Add to the four JSON schemas**

For each of `schema.json`, `plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`, find the `category` enum (search for `"convention_deviation"`) and add `"attestation_contradiction"` as the last entry before `"other"`. Example for `schema.json` around line 30:

Before:

```json
"unverifiable_codebase_claim",
"convention_deviation",
"other"
```

After:

```json
"unverifiable_codebase_claim",
"convention_deviation",
"attestation_contradiction",
"other"
```

Apply the same one-line change to all four files. `plan_schema.json` has the enum at BOTH the top-level `category` definition AND the nested `tasks` items' `category` — verify both locations. (Actually `plan_schema.json` uses `$ref: "#/definitions/finding"` for both task and plan findings, so the enum lives once in `definitions/finding`. Confirm by reading the file.)

- [ ] **Step 7: Run the tests**

Run: `go test -race ./...`
Expected: PASS, including the v0.5.1 `TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode` regression test (the enum addition does not violate the properties-equal-required invariant).

- [ ] **Step 8: Add CHANGELOG bullets**

Under `### Added`:

```markdown
- New finding category `attestation_contradiction` (NOT severity-floored — distinct from `convention_deviation` / `unverifiable_codebase_claim`). Emitted by the reviewer when an acceptance criterion explicitly contradicts a caller-attested harness shape; see `harness_shape_attestation` input below. Added to all four reviewer-output JSON schemas and to the parser's `validCategory` allowlist.
```

- [ ] **Step 9: Commit**

```bash
git add internal/verdict/verdict.go internal/verdict/parser.go internal/verdict/parser_partial.go \
        internal/verdict/schema.json internal/verdict/plan_schema.json \
        internal/verdict/tasks_only_schema.json internal/verdict/plan_findings_only_schema.json \
        internal/verdict/parser_test.go CHANGELOG.md
git commit -m "feat(verdict): add attestation_contradiction category"
```

---

## Task 12: Define `HarnessShapeAttestation` struct + extend `TaskSpec`

**Goal:** Pure data-shape addition in the session package. The struct must live in `internal/session` because `internal/mcpsrv` already imports `internal/session`; defining it in `mcpsrv` would create an import cycle once `session.TaskSpec` references it.

**Acceptance criteria:**
- `session.HarnessShapeAttestation` struct exists with three string-typed fields: `Harness`, `Path`, `Assertions []string`, each carrying a lowercase JSON tag (`harness`, `path`, `assertions`).
- `session.TaskSpec.HarnessShapeAttestations []HarnessShapeAttestation` exists with JSON tag `harness_shape_attestations,omitempty`.
- `TestHarnessShapeAttestation_JSONRoundTrip` confirms marshaling/unmarshaling preserves all three fields.
- `TestTaskSpec_HarnessShapeAttestations_OmittedWhenEmpty` confirms the field is omitted from JSON when nil/empty.
- `TestTaskSpec_HarnessShapeAttestations_SerializesWhenSet` confirms the field serializes correctly when populated.

**Non-goals:**
- The `ValidateTaskSpecArgs` field (Task 14).
- The normalizer (Task 13).
- The template rendering (Task 15).

**Files:**
- Modify: `internal/session/session.go`
- Modify: `internal/session/session_test.go` (create if absent, append if present) — JSON round-trip test

- [ ] **Step 1: Write the failing test**

Append to (or create) `internal/session/session_test.go`:

```go
package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHarnessShapeAttestation_JSONRoundTrip(t *testing.T) {
	in := HarnessShapeAttestation{
		Harness:    "TestHarnessX",
		Path:       "test/path/foo.kt:L100-L200",
		Assertions: []string{"records emitted spans", "does not stub the validator"},
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Contains(t, string(b), `"harness"`)
	require.Contains(t, string(b), `"path"`)
	require.Contains(t, string(b), `"assertions"`)
	var out HarnessShapeAttestation
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in, out)
}

func TestTaskSpec_HarnessShapeAttestations_OmittedWhenEmpty(t *testing.T) {
	spec := TaskSpec{Title: "t", Goal: "g"}
	b, err := json.Marshal(spec)
	require.NoError(t, err)
	require.NotContains(t, string(b), "harness_shape_attestations", "must be omitempty")
}

func TestTaskSpec_HarnessShapeAttestations_SerializesWhenSet(t *testing.T) {
	spec := TaskSpec{
		Title: "t", Goal: "g",
		HarnessShapeAttestations: []HarnessShapeAttestation{
			{Harness: "H", Path: "p", Assertions: []string{"a"}},
		},
	}
	b, err := json.Marshal(spec)
	require.NoError(t, err)
	require.Contains(t, string(b), `"harness_shape_attestations":[`)
}
```

- [ ] **Step 2: Run and confirm failures**

Run: `go test ./internal/session/ -race`
Expected: FAIL — `HarnessShapeAttestation` and the `TaskSpec` field do not exist yet.

- [ ] **Step 3: Add the struct and field**

Edit `internal/session/session.go`. Insert immediately above the `TaskSpec` struct (around line 12):

```go
// HarnessShapeAttestation declares a caller-attested shape fact about a test
// harness or fixture referenced in a task spec. The reviewer treats each
// attestation as authoritative context (no independent verification) and
// flags ACs that explicitly contradict an entry as
// `attestation_contradiction` findings. See INTEGRATION.md §3 for the use
// case.
//
// JSON tags pin the caller-visible field names. Without them, reflection on
// ValidateTaskSpecArgs would surface capitalized Go names (Harness, Path,
// Assertions) in the generated MCP input schema instead of the documented
// lowercase ones.
type HarnessShapeAttestation struct {
	Harness    string   `json:"harness"`
	Path       string   `json:"path"`
	Assertions []string `json:"assertions"`
}
```

Then modify the `TaskSpec` struct (lines 12-25). Insert a new field immediately after `NormativeTestBodies` (around line 23):

```go
HarnessShapeAttestations    []HarnessShapeAttestation `json:"harness_shape_attestations,omitempty"`
```

The full `TaskSpec` becomes:

```go
type TaskSpec struct {
	Title                        string                    `json:"title"`
	Goal                         string                    `json:"goal"`
	AcceptanceCriteria           []string                  `json:"acceptance_criteria,omitempty"`
	NonGoals                     []string                  `json:"non_goals,omitempty"`
	Context                      string                    `json:"context,omitempty"`
	PinnedBy                     []string                  `json:"pinned_by,omitempty"`
	ControllerVerifiedReferences []string                  `json:"controller_verified_references,omitempty"`
	TestStrategyNotes            []string                  `json:"test_strategy_notes,omitempty"`
	CodebaseConventions          []string                  `json:"codebase_conventions,omitempty"`
	TestabilityExtractions       []string                  `json:"testability_extractions,omitempty"`
	NormativeTestBodies          []string                  `json:"normative_test_bodies,omitempty"`
	HarnessShapeAttestations     []HarnessShapeAttestation `json:"harness_shape_attestations,omitempty"`
	Phase                        string                    `json:"phase,omitempty"`
}
```

- [ ] **Step 4: Run and confirm pass**

Run: `go test ./internal/session/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/session.go internal/session/session_test.go
git commit -m "feat(session): add HarnessShapeAttestation type and TaskSpec field"
```

---

## Task 13: Add `normalizeHarnessShapeAttestation` helper

**Goal:** Normalizer for the new input. Caps: 25 entries; `harness`/`path` ≤ 240 code points; ≤ 10 assertions each ≤ 480 code points. Reject empty harness or empty assertions. Trim whitespace. Dedup by canonical-JSON hash.

**Acceptance criteria:**
- `normalizeHarnessShapeAttestation([]session.HarnessShapeAttestation) ([]session.HarnessShapeAttestation, error)` exists in `internal/mcpsrv/task_spec_input.go`.
- All caps enforced: 25 entries; 240 chars on `harness` and `path`; 10 assertions per entry; 480 chars per assertion.
- Empty `harness` or empty `assertions` array rejected with a field-naming error.
- Empty-string assertion entries rejected with a field-naming error.
- Whitespace is trimmed from `harness`, `path`, and each assertion before length checks and dedup.
- Dedup by canonical-JSON SHA-256 of the normalized entry (post-trim).
- Nil/empty input returns `(nil, nil)` without allocation.
- All seven `TestNormalizeHarnessShapeAttestation_*` subtests pass.

**Non-goals:**
- Wiring through `normalizeTaskSpecInputs` and `ValidateTaskSpecArgs` (Task 14).

**Files:**
- Modify: `internal/mcpsrv/task_spec_input.go` (append helper + threading)
- Modify: `internal/mcpsrv/task_spec_input_test.go` (create if absent, append if present)

- [ ] **Step 1: Write the failing tests**

Create or append to `internal/mcpsrv/task_spec_input_test.go`:

```go
package mcpsrv

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/session"
)

func TestNormalizeHarnessShapeAttestation_HappyPath(t *testing.T) {
	in := []session.HarnessShapeAttestation{
		{Harness: " H ", Path: " p ", Assertions: []string{" a1 ", "a2"}},
	}
	out, err := normalizeHarnessShapeAttestation(in)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "H", out[0].Harness, "whitespace trimmed")
	require.Equal(t, "p", out[0].Path)
	require.Equal(t, []string{"a1", "a2"}, out[0].Assertions)
}

func TestNormalizeHarnessShapeAttestation_Caps(t *testing.T) {
	t.Run("too many entries", func(t *testing.T) {
		in := make([]session.HarnessShapeAttestation, 26)
		for i := range in {
			in[i] = session.HarnessShapeAttestation{Harness: "h", Path: "p", Assertions: []string{"a"}}
		}
		// Distinguish entries so dedup doesn't collapse them.
		for i := range in {
			in[i].Harness = "h" + string(rune('a'+i%26))
			in[i].Path = "p" + string(rune('a'+i%26))
		}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "at most 25 entries")
	})
	t.Run("harness too long", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: strings.Repeat("x", 241), Path: "p", Assertions: []string{"a"}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "harness")
		require.Contains(t, err.Error(), "240")
	})
	t.Run("path too long", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: strings.Repeat("x", 241), Assertions: []string{"a"}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "path")
		require.Contains(t, err.Error(), "240")
	})
	t.Run("too many assertions", func(t *testing.T) {
		assertions := make([]string, 11)
		for i := range assertions {
			assertions[i] = "a" + string(rune('a'+i))
		}
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: "p", Assertions: assertions}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "assertions")
		require.Contains(t, err.Error(), "10")
	})
	t.Run("assertion too long", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: "p", Assertions: []string{strings.Repeat("x", 481)}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "480")
	})
}

func TestNormalizeHarnessShapeAttestation_RejectsEmpty(t *testing.T) {
	t.Run("empty harness", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "  ", Path: "p", Assertions: []string{"a"}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "harness")
	})
	t.Run("empty assertions array", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: "p", Assertions: []string{}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "assertions")
	})
	t.Run("assertion with empty string", func(t *testing.T) {
		in := []session.HarnessShapeAttestation{{Harness: "h", Path: "p", Assertions: []string{"valid", "  "}}}
		_, err := normalizeHarnessShapeAttestation(in)
		require.Error(t, err)
		require.Contains(t, err.Error(), "assertion")
	})
}

func TestNormalizeHarnessShapeAttestation_DedupCanonical(t *testing.T) {
	in := []session.HarnessShapeAttestation{
		{Harness: "h", Path: "p", Assertions: []string{"a", "b"}},
		{Harness: " h ", Path: "p", Assertions: []string{" a ", "b"}}, // identical after trim
		{Harness: "h2", Path: "p", Assertions: []string{"a"}},
	}
	out, err := normalizeHarnessShapeAttestation(in)
	require.NoError(t, err)
	require.Len(t, out, 2, "dup collapses; distinct entry stays")
}

func TestNormalizeHarnessShapeAttestation_EmptyInputOk(t *testing.T) {
	out, err := normalizeHarnessShapeAttestation(nil)
	require.NoError(t, err)
	require.Empty(t, out)
}
```

- [ ] **Step 2: Run and confirm failures**

Run: `go test ./internal/mcpsrv/ -run TestNormalizeHarnessShapeAttestation -race`
Expected: FAIL with "undefined: normalizeHarnessShapeAttestation".

- [ ] **Step 3: Update the import block at the top of the file**

Edit `internal/mcpsrv/task_spec_input.go`. The current import block (lines 5-9) reads:

```go
import (
	"errors"
	"fmt"
	"strings"
)
```

Replace it with:

```go
import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/session"
)
```

Imports MUST stay at the top of the file (between the package clause and any declarations) — Go syntax does not permit an `import (...)` block partway through a file.

- [ ] **Step 4: Add caps constants**

Append to the existing `const` block (around line 17):

```go
const (
	// existing entries unchanged …
	maxHarnessShapeAttestationEntries        = 25
	maxHarnessShapeAttestationHarnessChars   = 240
	maxHarnessShapeAttestationPathChars      = 240
	maxHarnessShapeAttestationAssertions     = 10
	maxHarnessShapeAttestationAssertionChars = 480
)
```

(Append the five new entries to the existing block rather than creating a new const block. The "existing entries unchanged" comment is shorthand for the implementer; do NOT paste that comment into the file.)

- [ ] **Step 5: Append the helper function**

Append after `normalizeTaskSpecInputs` (which currently ends around line 108):

```go
// normalizeHarnessShapeAttestation trims whitespace, caps lengths and counts,
// dedupes by canonical-JSON SHA-256, and rejects entries with empty harness
// or empty assertions array. Returns a fresh slice; the input is not
// mutated. Mirrors normalizeBoundedStringList's error-message style so the
// errors are friendly to MCP callers.
func normalizeHarnessShapeAttestation(entries []session.HarnessShapeAttestation) ([]session.HarnessShapeAttestation, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]session.HarnessShapeAttestation, 0, len(entries))
	seen := make(map[[32]byte]struct{}, len(entries))
	for i, e := range entries {
		harness := strings.TrimSpace(e.Harness)
		if harness == "" {
			return nil, fmt.Errorf("harness_shape_attestation[%d].harness must be non-empty", i)
		}
		if len([]rune(harness)) > maxHarnessShapeAttestationHarnessChars {
			return nil, fmt.Errorf("harness_shape_attestation[%d].harness must be at most %d characters", i, maxHarnessShapeAttestationHarnessChars)
		}
		path := strings.TrimSpace(e.Path)
		if len([]rune(path)) > maxHarnessShapeAttestationPathChars {
			return nil, fmt.Errorf("harness_shape_attestation[%d].path must be at most %d characters", i, maxHarnessShapeAttestationPathChars)
		}
		if len(e.Assertions) == 0 {
			return nil, fmt.Errorf("harness_shape_attestation[%d].assertions must be non-empty", i)
		}
		if len(e.Assertions) > maxHarnessShapeAttestationAssertions {
			return nil, fmt.Errorf("harness_shape_attestation[%d].assertions must contain at most %d entries", i, maxHarnessShapeAttestationAssertions)
		}
		normAssertions := make([]string, 0, len(e.Assertions))
		for j, a := range e.Assertions {
			trimmed := strings.TrimSpace(a)
			if trimmed == "" {
				return nil, fmt.Errorf("harness_shape_attestation[%d].assertions[%d] must be non-empty", i, j)
			}
			if len([]rune(trimmed)) > maxHarnessShapeAttestationAssertionChars {
				return nil, fmt.Errorf("harness_shape_attestation[%d].assertions[%d] must be at most %d characters", i, j, maxHarnessShapeAttestationAssertionChars)
			}
			normAssertions = append(normAssertions, trimmed)
		}
		norm := session.HarnessShapeAttestation{
			Harness:    harness,
			Path:       path,
			Assertions: normAssertions,
		}
		// Dedup by canonical-JSON hash.
		raw, mErr := json.Marshal(norm)
		if mErr != nil {
			return nil, fmt.Errorf("harness_shape_attestation[%d]: canonical encode: %w", i, mErr)
		}
		key := sha256.Sum256(raw)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, norm)
		if len(out) > maxHarnessShapeAttestationEntries {
			return nil, errors.New("harness_shape_attestation must contain at most 25 entries")
		}
	}
	return out, nil
}
```

- [ ] **Step 6: Run and confirm pass**

Run: `go test ./internal/mcpsrv/ -run TestNormalizeHarnessShapeAttestation -race`
Expected: PASS.

- [ ] **Step 7: Commit (CHANGELOG entry added in Task 14 once wired)**

```bash
git add internal/mcpsrv/task_spec_input.go internal/mcpsrv/task_spec_input_test.go
git commit -m "feat(mcpsrv): add normalizeHarnessShapeAttestation helper"
```

---

## Task 14: Wire `HarnessShapeAttestation` through args, normalizer, handler, and session

**Goal:** New optional input is parsed from MCP args, normalized, stored on the session, and threaded through `prompts.PreInput` so the template can render it (template change lands in Task 15).

**Acceptance criteria:**
- `ValidateTaskSpecArgs.HarnessShapeAttestation []session.HarnessShapeAttestation` exists with JSON tag `harness_shape_attestation,omitempty`.
- `taskSpecInputs.HarnessShapeAttestations` exists (plural; matches the session field name).
- `normalizeTaskSpecInputs` calls `normalizeHarnessShapeAttestation` and threads the normalized value into the returned `taskSpecInputs`.
- `ValidateTaskSpec` copies `inputs.HarnessShapeAttestations` into `spec.HarnessShapeAttestations` before session creation.
- `TestValidateTaskSpec_HarnessShapeAttestationStoredOnSession` passes (round-trip through Sessions.Get).
- `TestValidateTaskSpec_HarnessShapeAttestationLimitsRejected` passes (normalizer error surfaces as handler error).
- `TestValidateTaskSpecArgs_HarnessShapeAttestation_JSONTag` passes — reflects on BOTH `ValidateTaskSpecArgs` (outer field tag) AND `session.HarnessShapeAttestation` (nested `harness` / `path` / `assertions` tags + types). Per design §504, `mcp.AddTool` reflects on these tags to generate the MCP input schema, so testing the tags is testing the schema.
- `TestValidateTaskSpec_NewServerBootsAndHandlerAcceptsNewField` passes — a registration smoke test that boots `New(deps)` (proving `mcp.AddTool` schema generation doesn't panic on the new args field) AND calls the handler directly with the new field populated (proving the args-struct → session.TaskSpec flow round-trips). It does NOT exercise the MCP protocol layer (no JSON-RPC encode/decode), which is acceptable here because the only new surface is the args field — the protocol-level decode reflects on the same JSON tags the reflection test above already pins.

**Non-goals:**
- The `pre.tmpl` rendering (Task 15).

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (add field to `ValidateTaskSpecArgs`; thread into `session.TaskSpec`)
- Modify: `internal/mcpsrv/task_spec_input.go` (extend `taskSpecInputs` + `normalizeTaskSpecInputs`)
- Modify: `internal/mcpsrv/handlers_test.go` (round-trip + MCP-schema reflection tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_HarnessShapeAttestationStoredOnSession(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"n"}`), Model: "claude-sonnet-4-6"},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	att := []session.HarnessShapeAttestation{
		{Harness: "TestHarnessX", Path: "test/foo.kt:L1", Assertions: []string{"records emitted spans", "does not stub validator"}},
	}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g",
		AcceptanceCriteria:      []string{"ac1"},
		HarnessShapeAttestation: att,
	})
	require.NoError(t, err)
	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	require.Equal(t, att, sess.Spec.HarnessShapeAttestations)
}

func TestValidateTaskSpec_HarnessShapeAttestationLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"n"}`), Model: "claude-sonnet-4-6"},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	att := []session.HarnessShapeAttestation{{Harness: "", Path: "p", Assertions: []string{"a"}}}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g",
		AcceptanceCriteria:      []string{"ac1"},
		HarnessShapeAttestation: att,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "harness")
}

func TestValidateTaskSpecArgs_HarnessShapeAttestation_JSONTag(t *testing.T) {
	// mcp.AddTool reflects on ValidateTaskSpecArgs struct tags at registration
	// time to generate the MCP input schema (per design §504 mechanism note).
	// This test verifies the source-of-truth tags on the args struct AND on
	// the nested session.HarnessShapeAttestation struct, so any drift between
	// the documented MCP shape (`harness_shape_attestation: [{harness, path,
	// assertions}]`) and the Go types is caught here.
	rt := reflect.TypeOf(ValidateTaskSpecArgs{})
	f, ok := rt.FieldByName("HarnessShapeAttestation")
	require.True(t, ok, "field must exist on args struct")
	require.Equal(t, `harness_shape_attestation,omitempty`, f.Tag.Get("json"))
	require.Equal(t, "[]session.HarnessShapeAttestation", f.Type.String())

	// Nested struct tags determine the inner {harness, path, assertions} shape.
	nested := reflect.TypeOf(session.HarnessShapeAttestation{})
	type want struct{ name, tag, typ string }
	for _, w := range []want{
		{"Harness", "harness", "string"},
		{"Path", "path", "string"},
		{"Assertions", "assertions", "[]string"},
	} {
		fld, ok := nested.FieldByName(w.name)
		require.True(t, ok, "nested field %s must exist", w.name)
		require.Equal(t, w.tag, fld.Tag.Get("json"), "nested field %s json tag", w.name)
		require.Equal(t, w.typ, fld.Type.String(), "nested field %s type", w.name)
	}
}

func TestValidateTaskSpec_NewServerBootsAndHandlerAcceptsNewField(t *testing.T) {
	// Two-part smoke test:
	//   (a) New(d) — proves mcp.AddTool's schema generation does not panic
	//       on the new ValidateTaskSpecArgs.HarnessShapeAttestation field.
	//       AddTool reflects on the args struct's JSON tags at registration
	//       time; a bad tag or unsupported nested type would panic here.
	//   (b) Direct h.ValidateTaskSpec(...) call — proves the args-struct →
	//       normalize → session.TaskSpec round-trip works for the new field.
	// This is NOT an MCP-protocol-level test (no JSON-RPC encode/decode).
	// Per design §504, protocol-level decoding reflects on the same JSON
	// tags the sibling reflection test pins, so this smoke + reflection
	// combo covers the documented failure modes.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"n"}`),
			Model:   "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	srv := New(d)
	require.NotNil(t, srv, "(a) New(d) constructs without panic — mcp.AddTool accepted the new args shape")

	h := &handlers{deps: d}
	att := []session.HarnessShapeAttestation{
		{Harness: "TestHarnessX", Path: "test/foo.kt:L1", Assertions: []string{"records emitted spans"}},
	}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:               "t",
		Goal:                    "g",
		AcceptanceCriteria:      []string{"ac1"},
		HarnessShapeAttestation: att,
	})
	require.NoError(t, err, "(b) handler accepts the new field and round-trips it to the session")
	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	require.Equal(t, att, sess.Spec.HarnessShapeAttestations)
}
```

(Imports needed at top: `reflect` and the `session` package; both already used in other tests in this file. `New(d)` is the server-builder exported from `internal/mcpsrv/server.go`.)

**Why two tests, not one full MCP RPC test?** The MCP go-sdk's tool-listing surface (and JSON-Schema inspection of registered tools) is not used elsewhere in this repo's test suite, so a deep RPC-level test would require new scaffolding that's out of scope here. The reflection test pins the source-of-truth struct tags (which `mcp.AddTool` reflects on at registration time), and the smoke test pins (a) successful `New(d)` boot and (b) the handler's args → session round-trip for the new field. Together they cover the failure modes the design §504 note worried about: wrong JSON tag, registration panicking on the new args shape, and the handler dropping the field on the way to the session.

- [ ] **Step 2: Run and confirm failures**

Run: `go test ./internal/mcpsrv/ -run "TestValidateTaskSpec_HarnessShapeAttestation|TestValidateTaskSpecArgs_HarnessShapeAttestation" -race`
Expected: FAIL — `HarnessShapeAttestation` field is not on `ValidateTaskSpecArgs` yet.

- [ ] **Step 3: Extend `ValidateTaskSpecArgs`**

Edit `internal/mcpsrv/handlers.go` around line 51 (immediately after `NormativeTestBodies`):

```go
type ValidateTaskSpecArgs struct {
	TaskTitle                    string                            `json:"task_title"           jsonschema:"required"`
	Goal                         string                            `json:"goal"                 jsonschema:"required"`
	AcceptanceCriteria           []string                          `json:"acceptance_criteria,omitempty"`
	NonGoals                     []string                          `json:"non_goals,omitempty"`
	Context                      string                            `json:"context,omitempty"`
	PinnedBy                     []string                          `json:"pinned_by,omitempty"`
	ControllerVerifiedReferences []string                          `json:"controller_verified_references,omitempty"`
	TestStrategyNotes            []string                          `json:"test_strategy_notes,omitempty"`
	CodebaseConventions          []string                          `json:"codebase_conventions,omitempty"`
	TestabilityExtractions       []string                          `json:"testability_extractions,omitempty"`
	NormativeTestBodies          []string                          `json:"normative_test_bodies,omitempty"`
	HarnessShapeAttestation      []session.HarnessShapeAttestation `json:"harness_shape_attestation,omitempty"`
	Phase                        string                            `json:"phase,omitempty"`
	ModelOverride                string                            `json:"model_override,omitempty"`
	MaxTokensOverride            int                               `json:"max_tokens_override,omitempty"`
}
```

- [ ] **Step 4: Extend `taskSpecInputs` and `normalizeTaskSpecInputs`**

Edit `internal/mcpsrv/task_spec_input.go`. Add to the `taskSpecInputs` struct:

```go
type taskSpecInputs struct {
	Phase                        string
	PinnedBy                     []string
	ControllerVerifiedReferences []string
	TestStrategyNotes            []string
	CodebaseConventions          []string
	TestabilityExtractions       []string
	NormativeTestBodies          []string
	HarnessShapeAttestations     []session.HarnessShapeAttestation
}
```

Extend `normalizeTaskSpecInputs` (around line 70). Add an additional normalization step after `normativeTestBodies`:

```go
normativeTestBodies, err := normalizeBoundedStringList("normative_test_bodies", args.NormativeTestBodies, maxNormativeTestBodyEntries, maxNormativeTestBodyChars)
if err != nil {
	return taskSpecInputs{}, err
}
harnessShapeAttestations, err := normalizeHarnessShapeAttestation(args.HarnessShapeAttestation)
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
	HarnessShapeAttestations:     harnessShapeAttestations,
}, nil
```

- [ ] **Step 5: Thread into `session.TaskSpec` inside `ValidateTaskSpec`**

Edit `internal/mcpsrv/handlers.go` around line 91 (the `spec := session.TaskSpec{...}` block):

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
	HarnessShapeAttestations:     inputs.HarnessShapeAttestations,
	Phase:                        inputs.Phase,
}
```

- [ ] **Step 6: Run the full suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 7: Add CHANGELOG bullet**

Under `### Added`:

```markdown
- `validate_task_spec` accepts a new optional `harness_shape_attestation` input: a list of `{harness, path, assertions[]}` objects declaring caller-attested shape facts about test harnesses or fixtures. Caps: ≤ 25 entries; harness/path ≤ 240 code points; ≤ 10 assertions each ≤ 480 code points; whitespace-trim + canonical-JSON dedup. Threads through the session and into the pre-hook prompt for reviewer rendering (see Task 15 / pre.tmpl).
```

- [ ] **Step 8: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/task_spec_input.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "feat(mcpsrv): wire harness_shape_attestation through args + session"
```

---

## Task 15: Add `## Harness shape attestations` section to pre.tmpl

**Goal:** Reviewer prompt renders the new input as authoritative context and instructs the reviewer to flag explicit contradictions using the new `attestation_contradiction` category.

**Acceptance criteria:**
- `pre.tmpl` renders a `## Harness shape attestations (caller-attested)` section only when `len(.Spec.HarnessShapeAttestations) > 0`.
- The section quotes the "NOT exhaustive" caveat verbatim ("the absence of a capability from the list means 'not asserted,' NOT 'forbidden.'").
- The section enumerates the two explicit-contradiction triggers (a) and (b).
- The section instructs the reviewer to emit findings with `category: attestation_contradiction`.
- `pre_basic.golden` shows no diff (the basic fixture has no attestations, so the conditional renders empty).
- `TestRenderPre_WithHarnessShapeAttestations_IncludesSection` passes (all six substring assertions).
- `TestRenderPre_WithoutHarnessShapeAttestations_OmitsSection` passes (section absent AND `attestation_contradiction` not mentioned when input is empty).

**Non-goals:**
- Any code change — the template is pure rendering.
- The `attestation_contradiction` category constant (Task 11).

**Files:**
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/testdata/pre_basic.golden` (regenerate)
- Modify: `internal/prompts/prompts_test.go` (`Contains` assertions)

- [ ] **Step 1: Write the failing tests**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_WithHarnessShapeAttestations_IncludesSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: session.TaskSpec{
		Title: "t", Goal: "g",
		AcceptanceCriteria: []string{"ac1"},
		HarnessShapeAttestations: []session.HarnessShapeAttestation{
			{Harness: "TestHarnessX", Path: "test/foo.kt:L1", Assertions: []string{
				"records emitted spans via getEmittedSpans()",
				"does not stub the validator method",
			}},
		},
	}})
	require.NoError(t, err)
	require.Contains(t, out.User, "## Harness shape attestations (caller-attested)")
	require.Contains(t, out.User, "TestHarnessX")
	require.Contains(t, out.User, "test/foo.kt:L1")
	require.Contains(t, out.User, "records emitted spans via getEmittedSpans()")
	require.Contains(t, out.User, "does not stub the validator method")
	require.Contains(t, out.User, "category: attestation_contradiction")
	require.Contains(t, out.User, "NOT exhaustive")
	require.Contains(t, out.User, "the absence of a capability from the list means \"not asserted,\" NOT \"forbidden.\"")
}

func TestRenderPre_WithoutHarnessShapeAttestations_OmitsSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: session.TaskSpec{Title: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"}}})
	require.NoError(t, err)
	require.NotContains(t, out.User, "## Harness shape attestations")
	require.NotContains(t, out.User, "attestation_contradiction") // category isn't mentioned when no attestations
}
```

- [ ] **Step 2: Run and confirm failures**

Run: `go test ./internal/prompts/ -run TestRenderPre_WithHarnessShapeAttestations -race`
Expected: FAIL.

- [ ] **Step 3: Append the new section to pre.tmpl**

Edit `internal/prompts/templates/pre.tmpl`. Insert the new conditional section AFTER the existing `Normative test bodies` block (which today ends around line 32) and BEFORE the `## What to evaluate` heading (line 33).

```text
{{if .Spec.HarnessShapeAttestations}}
## Harness shape attestations (caller-attested)

The controller has declared the following shape facts about test harnesses or fixtures referenced in this task. Treat each attestation as authoritative context — the controller has verified the assertions before dispatch. The reviewer does NOT independently verify these against the codebase.

These attestations are NOT exhaustive. An assertion list states what the controller confirmed; the absence of a capability from the list means "not asserted," NOT "forbidden." Do NOT flag ACs merely because they depend on a capability that isn't in the list.

Flag ONLY EXPLICIT contradictions:

  (a) An AC asks the implementer to do something a `does not ...` (or analogous negative) assertion explicitly forbids.

  (b) An AC asserts a fixture state, value, or invariant that DIRECTLY contradicts a stated positive assertion (e.g. attestation says "records emitted spans"; AC says "no spans should be recorded").

When (a) or (b) holds, emit a finding with:
  - `category: attestation_contradiction`
  - `severity: major` (or `critical` for a structural contradiction that prevents the task from being implementable)
  - `criterion: <which attestation is contradicted, quoted>`
  - `evidence: <which AC contradicts it, quoted>`
  - `suggestion: <revised AC or explicit harness change request>`

{{range .Spec.HarnessShapeAttestations}}- **{{.Harness}}** (at `{{.Path}}`):
{{range .Assertions}}    - {{.}}
{{end}}{{end}}
{{end}}
```

- [ ] **Step 4: Regenerate golden**

Run: `go test ./internal/prompts/... -update -race`
Then: `git diff internal/prompts/testdata/pre_basic.golden`
Expected: no change — the existing `pre_basic.golden` fixture has no `HarnessShapeAttestations` set, so the conditional renders empty. If the diff shows whitespace changes only, accept; if it shows the section rendering, the test fixture is wrong — verify.

- [ ] **Step 5: Run prompts tests**

Run: `go test ./internal/prompts/ -race`
Expected: PASS.

- [ ] **Step 6: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- `pre.tmpl` gains a `## Harness shape attestations` section (rendered only when `harness_shape_attestation` is non-empty) and instructs the reviewer to emit `attestation_contradiction` findings ONLY for explicit AC-vs-attestation contradictions (not for absent capabilities).
```

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/templates/pre.tmpl internal/prompts/testdata/pre_basic.golden internal/prompts/prompts_test.go CHANGELOG.md
git commit -m "feat(prompts): add harness shape attestation section to pre.tmpl"
```

---

## Task 16: Add `## Normative test bodies (binding)` section to post.tmpl

**Goal:** Session-propagated normative bodies render in the post-hook reviewer prompt. No struct change — `prompts.PostInput.Spec` already passes through `session.TaskSpec`, which already carries `NormativeTestBodies` from v0.5.0.

**Acceptance criteria:**
- `post.tmpl` renders a `## Normative test bodies (binding)` section only when `len(.Spec.NormativeTestBodies) > 0`.
- The section is placed AFTER the existing `{{if .ExitContracts}}...{{end}}` block per spec §200 — when both bodies and exit-contracts are present, exit-contracts renders first.
- The section instructs the reviewer "Do NOT flag AC-vs-fixture mismatches when a normative body explicitly pins the value."
- Each body renders inside a `---` / `---` delimited block.
- `TestRenderPost_WithNormativeTestBodies_IncludesSection` passes.
- `TestRenderPost_WithoutNormativeTestBodies_OmitsSection` passes (NO orphan heading when empty).
- `TestValidateCompletion_NormativeBodiesPropagatedFromSession` passes via the `rv.LastRequest.User` spy (relies on Task 4 Step 0's `fakeReviewer.LastRequest` extension).
- `TestValidateCompletion_LightweightMode_OmitsNormativeBodiesSection` passes — calls `ValidateCompletion` with empty `session_id` and asserts (a) no error, (b) `rv.LastRequest.User` does NOT contain `## Normative test bodies (binding)`.

**Non-goals:**
- Any new handler code path; the post-hook handler already passes `Spec` into the prompt context.

**Files:**
- Modify: `internal/prompts/templates/post.tmpl`
- Modify: `internal/prompts/testdata/post_basic.golden` (regenerate if applicable)
- Modify: `internal/prompts/prompts_test.go` (`Contains` + `NotContains` assertions)
- Modify: `internal/mcpsrv/handlers_test.go` (session round-trip test)

- [ ] **Step 1: Write the failing prompts tests**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPost_WithNormativeTestBodies_IncludesSection(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec: session.TaskSpec{
			Title: "t", Goal: "g",
			AcceptanceCriteria:  []string{"ac1"},
			NormativeTestBodies: []string{"@Test fun emits_spans() { /* binding */ }"},
		},
		Summary:   "did it",
		FinalDiff: "diff --git ...",
	})
	require.NoError(t, err)
	require.Contains(t, out.User, "## Normative test bodies (binding)")
	require.Contains(t, out.User, "@Test fun emits_spans() { /* binding */ }")
	require.Contains(t, out.User, "Do NOT flag AC-vs-fixture mismatches when a normative body explicitly pins the value.")
}

func TestRenderPost_WithoutNormativeTestBodies_OmitsSection(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:      session.TaskSpec{Title: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"}},
		Summary:   "did it",
		FinalDiff: "diff --git ...",
	})
	require.NoError(t, err)
	require.NotContains(t, out.User, "## Normative test bodies (binding)")
}
```

- [ ] **Step 2: Write the failing session round-trip test**

Append to `internal/mcpsrv/handlers_test.go`. The test relies on the `fakeReviewer.LastRequest` extension added in Task 4 Step 0 — `rv.LastRequest.User` carries the rendered prompt of the most recent reviewer call.

```go
func TestValidateCompletion_NormativeBodiesPropagatedFromSession(t *testing.T) {
	// Stage 1: validate_task_spec stores normative bodies on the session.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"n"}`), Model: "claude-sonnet-4-6"},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, preEnv, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "t",
		Goal:                "g",
		AcceptanceCriteria:  []string{"ac1"},
		NormativeTestBodies: []string{"@Test fun emits_spans() { /* binding */ }"},
	})
	require.NoError(t, err)

	// Stage 2: ValidateCompletion uses the SAME fakeReviewer (rv). The
	// LastRequest field captures the request used for the post-hook call.
	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: preEnv.SessionID,
		Summary:   "did it",
		FinalDiff: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n",
	})
	require.NoError(t, err)
	require.Contains(t, rv.LastRequest.User, "## Normative test bodies (binding)",
		"post-hook prompt must surface the session-stored normative bodies")
	require.Contains(t, rv.LastRequest.User, "@Test fun emits_spans() { /* binding */ }")
}

func TestValidateCompletion_LightweightMode_OmitsNormativeBodiesSection(t *testing.T) {
	// Lightweight mode (empty session_id) synthesizes an empty Spec. The
	// post.tmpl conditional `{{if .Spec.NormativeTestBodies}}` evaluates
	// false → the section MUST NOT render. This test pins that behavior
	// end-to-end through the handler, not just the prompt-render layer.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"n"}`), Model: "claude-sonnet-4-6"},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: "", // lightweight marker
		Summary:   "did it",
		FinalDiff: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n",
	})
	require.NoError(t, err, "lightweight ValidateCompletion must not error")
	require.NotContains(t, rv.LastRequest.User, "## Normative test bodies (binding)",
		"lightweight mode (empty session_id) must not render the normative-bodies section")
}
```

- [ ] **Step 3: Run and confirm failures**

Both commands MUST run regardless of the other's exit code, so issue them separately rather than chaining with `&&` (which would skip the second on first-command failure):

```bash
go test ./internal/prompts/ -run TestRenderPost_WithNormativeTestBodies -race
go test ./internal/mcpsrv/ -run TestValidateCompletion_NormativeBodiesPropagatedFromSession -race
```

Expected: BOTH fail. The prompts test fails because the template change is not yet made; the mcpsrv test fails because the post-hook prompt doesn't yet include the normative-bodies section, so `rv.LastRequest.User` doesn't contain the expected substring.

- [ ] **Step 4: Edit post.tmpl**

Edit `internal/prompts/templates/post.tmpl`. Per spec §200, insert the new conditional section immediately AFTER the existing `{{if .ExitContracts}}...{{end}}` block (around line 25) so it lands AFTER exit-contracts when both are present, but BEFORE the `## Major pre-task findings to verify` heading. When `ExitContracts` is empty, the section still renders just before the `MajorPreFindings` block; when both `NormativeTestBodies` and `ExitContracts` are empty, the section is omitted entirely.

The new block:

```text
{{if .Spec.NormativeTestBodies}}
## Normative test bodies (binding)

The following test bodies were declared as binding when the implementer started this task. They are authoritative for fixture state, exact strings, and assertions. The AC list above is authoritative for behavior — but when an AC and a normative body appear to disagree on a fixture value, the body wins.

Do NOT flag AC-vs-fixture mismatches when a normative body explicitly pins the value.

{{range .Spec.NormativeTestBodies}}---
{{.}}
---
{{end}}{{end}}
```

- [ ] **Step 5: Regenerate goldens**

Run: `go test ./internal/prompts/... -update -race`
Then: `git diff internal/prompts/testdata/post_basic.golden`
Expected: no change (basic fixture has no normative bodies). If it does change, audit the fixture.

- [ ] **Step 6: Run tests**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 7: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- `validate_completion` now sees `normative_test_bodies` from the session at post-hook time. `post.tmpl` renders a `## Normative test bodies (binding)` section that instructs the reviewer to treat the bodies as authoritative for fixture state, exact strings, and assertions; AC-vs-fixture mismatches are suppressed when a body pins the value. Lightweight mode (empty `session_id`) is unaffected — no session, no bodies, no section.
```

- [ ] **Step 8: Commit**

```bash
git add internal/prompts/templates/post.tmpl internal/prompts/testdata/post_basic.golden internal/prompts/prompts_test.go internal/mcpsrv/handlers_test.go CHANGELOG.md
git commit -m "feat(prompts): render normative_test_bodies at post-hook"
```

---

## Task 17: Add major→minor demotion instruction to pre.tmpl AND post.tmpl

**Goal:** Reviewer-emitted demotion: when reviewer would emit `major ambiguous_spec` but a normative test body explicitly pins the ambiguity, downgrade to `minor` and append `(resolved-by-normative-body: <citation>)` to `suggestion`. Parser passes the demoted finding through unchanged (no server-side re-derivation of severity).

**Acceptance criteria:**
- Both `pre.tmpl` and `post.tmpl` include the demotion instruction string `(resolved-by-normative-body:` AND `downgrade the severity to `minor``.
- Goldens regenerated and committed alongside the template change.
- `verdict.Parse` accepts a reviewer-emitted `minor ambiguous_spec` finding whose `suggestion` carries the `(resolved-by-normative-body: …)` suffix WITHOUT re-deriving severity (design §501).

**Files:**
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/templates/post.tmpl`
- Modify: `internal/prompts/testdata/pre_basic.golden`, `post_basic.golden` (regenerate)
- Modify: `internal/prompts/prompts_test.go` (prompt-render assertions)
- Modify: `internal/verdict/parser_test.go` (parser-passthrough assertion)

- [ ] **Step 1: Write the failing tests**

Append to `internal/prompts/prompts_test.go`:

```go
const anchorDemotionRule = "(resolved-by-normative-body:"

func TestRenderPre_IncludesDemotionRule(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: session.TaskSpec{Title: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"}}})
	require.NoError(t, err)
	require.Contains(t, out.User, anchorDemotionRule)
	require.Contains(t, out.User, "downgrade the severity to `minor`")
}

func TestRenderPost_IncludesDemotionRule(t *testing.T) {
	out, err := RenderPost(PostInput{Spec: session.TaskSpec{Title: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"}}, Summary: "s", FinalDiff: "d"})
	require.NoError(t, err)
	require.Contains(t, out.User, anchorDemotionRule)
	require.Contains(t, out.User, "downgrade the severity to `minor`")
}
```

Append to `internal/verdict/parser_test.go` (design §501 — parser must pass demoted findings through unchanged, no server-side severity re-derivation):

```go
func TestParse_DemotedAmbiguousSpec_PassesThroughUnchanged(t *testing.T) {
	raw := []byte(`{
		"verdict":"warn",
		"findings":[{
			"severity":"minor",
			"category":"ambiguous_spec",
			"criterion":"AC1: emits spans",
			"evidence":"AC reads ambiguous about span count",
			"suggestion":"Pin count in the AC. (resolved-by-normative-body: Test fun emits_spans pins exactly 2 calls)"
		}],
		"next_action":"address as minor"
	}`)
	r, err := Parse(raw)
	require.NoError(t, err)
	require.Len(t, r.Findings, 1)
	require.Equal(t, SeverityMinor, r.Findings[0].Severity, "demoted finding stays minor; parser does not re-derive")
	require.Equal(t, CategoryAmbiguousSpec, r.Findings[0].Category)
	require.Contains(t, r.Findings[0].Suggestion, "(resolved-by-normative-body:")
}
```

- [ ] **Step 2: Run and confirm failures**

Run the two test commands separately so the parser test executes even when the prompts test fails (chaining with `&&` would short-circuit and skip the second):

```bash
go test ./internal/prompts/ -run "TestRenderPre_IncludesDemotionRule|TestRenderPost_IncludesDemotionRule" -race
go test ./internal/verdict/ -run TestParse_DemotedAmbiguousSpec -race
```

Expected: prompts tests FAIL (template not edited yet); the parser test PASSES (this is a regression-pin test — `applySeverityFloor` doesn't touch `ambiguous_spec`, so it should already pass today. Running it first confirms the invariant before Task 17's template change introduces the suffix at the prompt layer).

- [ ] **Step 3: Edit pre.tmpl**

In `internal/prompts/templates/pre.tmpl`, after the existing `Severity:` line (around line 58 today, the line that reads `Severity: critical = spec is unimplementable as written; ...`), insert:

```text

If you would emit a finding with `category: ambiguous_spec` AND `severity: major`, but a normative test body (provided in the `Normative test bodies` section above) explicitly pins the value or assertion that the AC's prose leaves ambiguous, downgrade the severity to `minor` and append `(resolved-by-normative-body: <short citation of which body resolves it, max 80 chars>)` to the `suggestion` field. The `criterion` and `evidence` fields stay as you would have written them.
```

- [ ] **Step 4: Edit post.tmpl**

In `internal/prompts/templates/post.tmpl`, insert the SAME instruction text. Place it immediately before the closing line `Respond with the verdict JSON only.` (currently the last line of the template):

```text

If you would emit a finding with `category: ambiguous_spec` AND `severity: major`, but a normative test body (provided in the `Normative test bodies` section above) explicitly pins the value or assertion that the AC's prose leaves ambiguous, downgrade the severity to `minor` and append `(resolved-by-normative-body: <short citation of which body resolves it, max 80 chars>)` to the `suggestion` field. The `criterion` and `evidence` fields stay as you would have written them.

```

- [ ] **Step 5: Regenerate goldens**

Run: `go test ./internal/prompts/... -update -race`
Then: `git diff internal/prompts/testdata/pre_basic.golden internal/prompts/testdata/post_basic.golden`
Expected: each golden gains the new instruction.

- [ ] **Step 6: Run tests**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 7: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- Reviewer is now instructed to demote `major ambiguous_spec` findings to `minor` when a normative test body explicitly pins the ambiguous value/assertion. Demoted findings carry a `(resolved-by-normative-body: <citation>)` suffix on `suggestion` so callers can see why. Instruction lands in both `pre.tmpl` and `post.tmpl`.
```

- [ ] **Step 8: Commit**

```bash
git add internal/prompts/templates/pre.tmpl internal/prompts/templates/post.tmpl \
        internal/prompts/testdata/pre_basic.golden internal/prompts/testdata/post_basic.golden \
        internal/prompts/prompts_test.go internal/verdict/parser_test.go CHANGELOG.md
git commit -m "feat(prompts): instruct reviewer to demote ambiguous_spec when pinned by normative body"
```

---

## Task 18: Add `.trimIndent()` raw-string heuristic to pre.tmpl

**Goal:** Reviewer flags raw-string-trim constructs paired with multi-line literal comparison so callers see the §3.7 caveat as a finding rather than discovering it via a test miss.

**Acceptance criteria:**
- `pre.tmpl` instructs the reviewer to emit a `minor` `ambiguous_spec` finding when plan text contains `.trimIndent()` / `.trimMargin()` / `textwrap.dedent` / tagged-template `dedent` alongside a multi-line literal comparison.
- The instruction names the criterion verbatim: `"raw-string trimming caveat — see INTEGRATION.md §3.7"`.
- `pre_basic.golden` is regenerated and committed alongside the template change.
- `TestRenderPre_IncludesTrimIndentHeuristic` passes — all five substring assertions hit.

**Non-goals:**
- Server-side detection (the heuristic is reviewer-evaluated, not a deterministic check).

**Files:**
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/testdata/pre_basic.golden` (regenerate)
- Modify: `internal/prompts/prompts_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_IncludesTrimIndentHeuristic(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: session.TaskSpec{Title: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"}}})
	require.NoError(t, err)
	require.Contains(t, out.User, "RAW-STRING TRIMMING CAVEAT")
	require.Contains(t, out.User, ".trimIndent()")
	require.Contains(t, out.User, ".trimMargin()")
	require.Contains(t, out.User, "textwrap.dedent")
	require.Contains(t, out.User, "INTEGRATION.md §3.7")
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/prompts/ -run TestRenderPre_IncludesTrimIndentHeuristic -race`
Expected: FAIL.

- [ ] **Step 3: Edit pre.tmpl**

In `internal/prompts/templates/pre.tmpl`, append immediately before the closing line `Respond with the verdict JSON only.`:

```text

RAW-STRING TRIMMING CAVEAT (§3.7 heuristic):
If the task's plan text or context contains `.trimIndent()`, `.trimMargin()`, `textwrap.dedent`, or a tagged-template `dedent`, AND any acceptance criterion compares the implementation's output against a multi-line string literal in the same plan block, emit a `minor` `ambiguous_spec` finding with:
  - criterion: "raw-string trimming caveat — see INTEGRATION.md §3.7"
  - evidence: "<quoted trim construct and the multi-line literal>"
  - suggestion: "Pin example strings the implementation will compare against to a single source line, OR phrase the AC against the rendered string (e.g. 'output contains <phrase>') rather than against source layout."

```

- [ ] **Step 4: Regenerate golden + run**

Run: `go test ./internal/prompts/... -update -race && go test ./internal/prompts/ -race`
Expected: PASS.

- [ ] **Step 5: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- `pre.tmpl` now instructs the reviewer to emit a `minor ambiguous_spec` finding citing INTEGRATION.md §3.7 when plan text contains `.trimIndent()` / `.trimMargin()` / `textwrap.dedent` / tagged-template `dedent` alongside a multi-line string literal comparison.
```

- [ ] **Step 6: Commit**

```bash
git add internal/prompts/templates/pre.tmpl internal/prompts/testdata/pre_basic.golden internal/prompts/prompts_test.go CHANGELOG.md
git commit -m "feat(prompts): add .trimIndent() pre-task heuristic"
```

---

## Task 19: INTEGRATION.md docs updates

**Goal:** Document the new input, the new finding category, the `check_progress` trigger nudge, and the deterministic CVR suppression. Keep edits surgical — INTEGRATION.md is already trimmed for the 40k user-instructions budget; don't push over.

**Acceptance criteria:**
- §3 (Plan authors) describes `harness_shape_attestation` alongside the existing `pinned_by` / `controller_verified_references` paragraphs.
- §4.2 (paste-clause args list) names `harness_shape_attestation` as a structured optional input.
- §6 (FAQ / finding categories) lists `attestation_contradiction` with its NOT-floored note.
- §5.7 (CVR feature description) states that suppression now runs deterministically server-side as well as in the reviewer prompt.
- §4 lifecycle-table row for `check_progress` AND §4.2 paste-clause "During work" step each include the literal substring `test that 'should' fail`.
- `grep -cF "test that 'should' fail" INTEGRATION.md` outputs exactly `2` (design §506 — fails on 1 or 3+).
- `grep -cF ">5 min debugging" INTEGRATION.md` outputs exactly `2`.
- `wc -c INTEGRATION.md` stays well below 40,000 (target ≤ 35,000 — currently 33,186).

**Non-goals:**
- Restructuring or further trimming beyond these four edits.

**Files:**
- Modify: `INTEGRATION.md`

- [ ] **Step 1: Open INTEGRATION.md and locate the sections to edit**

Read INTEGRATION.md once to refresh — the section numbering follows the trimmed v0.5.1 layout. Use the existing `pinned_by` / `controller_verified_references` / `normative_test_bodies` paragraphs in §3 as templates for the new entries.

- [ ] **Step 2: Add `harness_shape_attestation` docs**

In §3 (Plan authors), in the same area as the existing optional-inputs description, add a short paragraph:

```markdown
**`harness_shape_attestation`** is a structured optional input on `validate_task_spec` (added in v0.5.2). Each entry is `{harness: string, path: string, assertions: []string}`. Use it when a task's acceptance criteria depend on a test harness's stated capabilities (or stated non-capabilities). The reviewer treats each attestation as authoritative caller-attested context (no independent verification) and flags ACs that EXPLICITLY contradict an entry — e.g. an AC asks for behavior a `does not …` assertion forbids, or an AC asserts a state that directly contradicts a positive assertion — as `attestation_contradiction` findings. Absence of a capability is NOT a contradiction; do not list things to forbid them.
```

Then in §4.2 (or wherever the implementer paste-clause lists the structured inputs), extend the args list:

```markdown
- harness_shape_attestation: <optional structured input; see §3>
```

- [ ] **Step 3: Add `attestation_contradiction` to the finding categories list**

If §6 (FAQ) or a similar section enumerates the categories, append `attestation_contradiction` with one sentence: "Emitted when an AC explicitly contradicts a `harness_shape_attestation` entry. NOT severity-floored (unlike `convention_deviation` / `unverifiable_codebase_claim`); the reviewer's chosen severity is preserved."

- [ ] **Step 4: Update §5.7 CVR description**

In §5.7 (Using review-context features), find the existing `controller_verified_references` paragraph. Append:

```markdown
Suppression now runs server-side (deterministic) as well as in the reviewer prompt: a CVR-entry substring match against the finding's `evidence` or `criterion` (either direction; 4-code-point floor on CVR entries) suppresses the entire `unverifiable_codebase_claim`. The behavior is independent of reviewer compliance.
```

- [ ] **Step 5: Add `check_progress` docs nudge**

In §4 (For implementers — the lifecycle protocol), the lifecycle table row for `check_progress` (currently reads "Optional (advisory)"), replace with:

```markdown
| During | `check_progress` | Optional (advisory; low-signal in field data — call only when you suspect drift, OR when a test that 'should' fail doesn't, OR you've spent >5 min debugging behavior the spec leaves under-specified) | When you suspect drift mid-task |
```

In §4.2 (the implementer paste-clause), the "During work (OPTIONAL)" step's body — extend the trigger sentence to include "OR when a test that 'should' fail doesn't, OR you've spent >5 min debugging behavior the spec leaves under-specified."

- [ ] **Step 6: Verify the check_progress docs-nudge marker count (design §506)**

Run: `grep -cF "test that 'should' fail" INTEGRATION.md`
Expected output: `2` (exactly two occurrences — one in the §4 lifecycle table row, one in the §4.2 paste-clause "During work" step).

If the count is `1`: one of the two intended edits is missing — re-check both locations.
If the count is `3` or more: an accidental duplicate paste — diff the file against `main` and remove the extra.

Then verify the >5 min phrase also lands in both spots:

Run: `grep -cF ">5 min debugging" INTEGRATION.md`
Expected output: `2`.

- [ ] **Step 7: Verify size cap**

Run: `wc -c INTEGRATION.md`
Expected: well below the 40,000-char budget. Current size before this task: ~33,186 chars. After: should stay under 35,000. If a section grew unexpectedly, trim before commit.

- [ ] **Step 8: Add CHANGELOG bullets**

Under `### Changed`:

```markdown
- `INTEGRATION.md` documents `harness_shape_attestation` (§3 + §4.2 + §6), the `attestation_contradiction` finding category (§6), the deterministic server-side CVR suppression (§5.7), and adds the `check_progress` trigger nudge ("test that 'should' fail doesn't" / ">5 min debugging") to both §4 lifecycle table and §4.2 paste-clause "During work" step.
```

- [ ] **Step 9: Commit**

```bash
git add INTEGRATION.md CHANGELOG.md
git commit -m "docs(integration): v0.5.2 input + category + CVR + check_progress nudge"
```

---

## Task 20: README.md update

**Goal:** Reflect the new optional `validate_task_spec` input in the README intro list.

**Acceptance criteria:**
- README's existing list of `validate_task_spec` optional inputs (located via `grep -n controller_verified_references README.md`) gains a one-line bullet for `harness_shape_attestation` matching the format used by the existing entries.
- If the README intro counts the inputs (e.g., "five optional inputs"), the count is updated accordingly.

**Non-goals:**
- Other README content (install prompts, smoke test, etc.) untouched.

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Locate the validate_task_spec inputs list**

Read README.md; find the v0.5.0-era section that describes `validate_task_spec` optional inputs. Run:

```bash
grep -n controller_verified_references README.md
```

…to anchor on the inputs list. Inventory the entries currently listed (likely subset of `pinned_by`, `controller_verified_references`, `test_strategy_notes`, `codebase_conventions`, `testability_extractions`, `normative_test_bodies`) and record the count for Step 2's update.

- [ ] **Step 2: Insert `harness_shape_attestation`**

Add a one-line entry mirroring the existing format. Example:

```markdown
- **`harness_shape_attestation`** (added in v0.5.2): structured `{harness, path, assertions[]}` entries declaring caller-attested test-harness shape facts. Pairs with the new `attestation_contradiction` finding category, which flags ACs that explicitly contradict an attested assertion.
```

- [ ] **Step 3: Run a quick check**

Run: `git diff README.md`
Expected: only the new bullet plus any small adjacent prose updates if the list intro counts inputs (e.g., "five optional inputs" → "six optional inputs").

- [ ] **Step 4: Add CHANGELOG bullet**

Under `### Changed`:

```markdown
- `README.md` lists `harness_shape_attestation` alongside the existing optional `validate_task_spec` inputs.
```

- [ ] **Step 5: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs(readme): list harness_shape_attestation optional input"
```

---

## Final verification

After the last task commits, run the full suite one more time to confirm nothing regressed and no test was inadvertently skipped:

```bash
go test -race ./...
```

Then sanity-check the version + branch state:

```bash
grep "## \[0.5.2\]" CHANGELOG.md  # confirm header
git log --oneline main..HEAD | wc -l  # roughly 20 commits since main
```

Optional: run `goreleaser release --snapshot --clean --skip=publish` to confirm the release artifact builds without errors. Not required as part of the plan — happens automatically on merge.

Branch lifecycle: when all tasks pass, hand off to `superpowers:finishing-a-development-branch` for the merge-or-PR choice. Branch name `version/0.5.2` matches the `## [0.5.2]` CHANGELOG entry per project convention; merge commit carries no `[major]` / `[minor]` tag so the release workflow defaults to patch bump.
