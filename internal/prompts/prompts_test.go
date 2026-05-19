package prompts

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

var update = flag.Bool("update", false, "update golden files")

func sampleSpec() session.TaskSpec {
	return session.TaskSpec{
		Title: "Add /healthz endpoint",
		Goal:  "Liveness probe for the HTTP server",
		AcceptanceCriteria: []string{
			"Returns 200 OK with body \"ok\"",
			"Responds in under 50ms p95",
		},
		NonGoals: []string{"Database health (covered separately)"},
		Context:  "Service is a Gin app on port 8080.",
	}
}

func golden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

func TestRenderPre(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	golden(t, "pre_basic", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPre_WithControllerVerifiedReferencesIncludesGuidance(t *testing.T) {
	spec := sampleSpec()
	spec.ControllerVerifiedReferences = []string{"internal/foo.go:12", "Foo.Bar"}

	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Controller-verified references:")
	assert.Contains(t, out.User, "- internal/foo.go:12")
	assert.Contains(t, out.User, "- Foo.Bar")
	assert.Contains(t, out.User, "some entry in controller_verified_references is a substring of C")
	assert.Contains(t, out.User, "Do not suppress logical contradictions, missing acceptance criteria, or ambiguity findings")
}

func TestRenderPre_IncludesTestOnlyGuidance(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.Contains(t, out.User, "For explicitly test-only tasks")
	assert.Contains(t, out.User, "missing invocation counts")
	assert.Contains(t, out.User, "when call-count behavior matters")
	assert.Contains(t, out.User, "when a no-change or no-call invariant is intended")
	assert.Contains(t, out.User, "one consolidated finding")
}

func TestRenderPre_WithoutControllerVerifiedReferencesOmitsSection(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "Controller-verified references:")
}

func TestRenderMid(t *testing.T) {
	out, err := RenderMid(MidInput{
		Spec: sampleSpec(),
		PriorFindings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryAmbiguousSpec,
			Criterion:  "AC #2",
			Evidence:   "\"under 50ms\" — at what load?",
			Suggestion: "Pin the load profile (RPS).",
		}},
		WorkingOn: "writing the handler",
		Files: []File{{
			Path:    "handlers/health.go",
			Content: "package handlers\nfunc Health(c *gin.Context) { c.String(200, \"ok\") }\n",
		}},
		Questions: []string{"Should we expose this on a separate port?"},
	})
	require.NoError(t, err)
	golden(t, "mid_basic", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPost(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:    sampleSpec(),
		Summary: "Added Gin handler at /healthz returning \"ok\".",
		Files: []File{{
			Path:    "handlers/health.go",
			Content: "package handlers\nfunc Health(c *gin.Context) { c.String(200, \"ok\") }\n",
		}},
		TestEvidence: "PASS: TestHealthReturns200",
	})
	require.NoError(t, err)
	golden(t, "post_basic", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPost_WithMajorPreFindingsIncludesMitigationGuidance(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:    sampleSpec(),
		Summary: "Clarified the load profile and added a benchmark-backed test.",
		MajorPreFindings: []verdict.Finding{{
			Severity:  verdict.SeverityMajor,
			Category:  verdict.CategoryAmbiguousSpec,
			Criterion: "Responds in under 50ms p95",
			Evidence:  "Pre-task review found the load profile was undefined.",
		}},
		TestEvidence: "PASS: TestHealthP95UnderLoad",
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Major pre-task findings to verify")
	assert.Contains(t, out.User, "Pre-task review found the load profile was undefined.")
	assert.Contains(t, out.User, "explicitly mitigates")
}

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
	golden(t, "plan_tasks_chunk", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlanFindingsOnly_Golden(t *testing.T) {
	out, err := RenderPlanFindingsOnly(PlanInput{
		PlanText: "## Phase 1\n\n### Task 1: do thing\n\n**Goal:** thing\n\n**Acceptance criteria:**\n- thing happens\n",
	})
	if err != nil {
		t.Fatalf("RenderPlanFindingsOnly: %v", err)
	}
	golden(t, "plan_findings_only", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPost_WithFinalDiff(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:      sampleSpec(),
		Summary:   "Changed health handler.",
		FinalDiff: "diff --git a/handlers/health.go b/handlers/health.go\n+@@\n+-old\n++new\n",
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "## Final diff")
	assert.Contains(t, out.User, "diff --git")
}

func TestRenderPost_WithoutFinalDiffOmitsSection(t *testing.T) {
	out, err := RenderPost(PostInput{Spec: sampleSpec(), Summary: "No diff."})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "## Final diff")
}

func TestRenderPost_IncludesEvidenceToleranceGuidance(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:         sampleSpec(),
		Summary:      "Implemented AC via diff and tests.",
		TestEvidence: "go test ./... PASS",
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Context:` block in the task spec above is authoritative")
	assert.Contains(t, out.User, "the summary on its own is not evidence")
	assert.Contains(t, out.User, "prefer `verdict: pass` with a `category: quality` finding")
	assert.Contains(t, out.User, "left unaddressed by any of the provided evidence")
}

const (
	anchorReviewerGroundRules    = "## Reviewer ground rules"
	anchorEpistemicBoundary      = "You have access ONLY to the plan markdown"
	anchorUnstatedAssumptionRule = "For `unstated_assumption` findings, only flag"
	anchorConcreteEvidenceRule   = "Every finding's `evidence` field must quote or paraphrase"
)

func TestRenderPlan_IncludesReviewerGroundRules(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorReviewerGroundRules)
	assert.Contains(t, out.User, anchorEpistemicBoundary)
	assert.Contains(t, out.User, anchorUnstatedAssumptionRule)
	assert.Contains(t, out.User, anchorConcreteEvidenceRule)
}

func TestRenderPlan_IncludesLightweightGuidance(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Sample plan\n\n### Task 1: Docs\n\n**Goal:** Update docs\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, "lightweight_eligible")
	assert.Contains(t, out.User, "lightweight_reason")
	assert.Contains(t, out.User, "the task touches at most two files or is docs/config/data-only")
	assert.Contains(t, out.User, "mechanical with no production-design or test-design choices")
	assert.Contains(t, out.User, "Reason required when true, empty when false")
}

func TestRenderPlanFindingsOnly_IncludesReviewerGroundRules(t *testing.T) {
	out, err := RenderPlanFindingsOnly(PlanInput{PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorReviewerGroundRules)
	assert.Contains(t, out.User, anchorEpistemicBoundary)
	assert.Contains(t, out.User, anchorUnstatedAssumptionRule)
	assert.Contains(t, out.User, anchorConcreteEvidenceRule)
}

func TestRenderPlanTasksChunk_IncludesReviewerGroundRules(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n",
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorReviewerGroundRules)
	assert.Contains(t, out.User, anchorEpistemicBoundary)
	assert.Contains(t, out.User, anchorUnstatedAssumptionRule)
	assert.Contains(t, out.User, anchorConcreteEvidenceRule)
}

func TestRenderPlanTasksChunk_IncludesLightweightGuidance(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   "# Sample plan\n\n### Task 1: Docs\n\n**Goal:** Update docs\n",
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: Docs"}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, "lightweight_eligible")
	assert.Contains(t, out.User, "lightweight_reason")
	assert.Contains(t, out.User, "the task touches at most two files or is docs/config/data-only")
	assert.Contains(t, out.User, "mechanical with no production-design or test-design choices")
	assert.Contains(t, out.User, "Reason required when true, empty when false")
}

const (
	anchorQuickModeBasic        = "**Quick mode.** Surface only the most-severe findings — at most 3 per scope"
	anchorQuickModeFindingsOnly = "**Quick mode.** Surface only the most-severe findings — at most 3 plan-level findings"
	anchorQuickModeTasksChunk   = "**Quick mode.** For each task in the list above, surface only the most-severe findings — at most 3 per task"
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

func TestPlanTemplates_DefaultMode_OmitsQuickInstruction(t *testing.T) {
	planText := "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"

	for _, mode := range []string{"", "thorough"} {
		t.Run("mode="+mode, func(t *testing.T) {
			out, err := RenderPlan(PlanInput{PlanText: planText, Mode: mode})
			require.NoError(t, err)
			assert.NotContains(t, out.User, "**Quick mode.**", "plan.tmpl should not include quick-mode block for mode=%q", mode)

			out, err = RenderPlanFindingsOnly(PlanInput{PlanText: planText, Mode: mode})
			require.NoError(t, err)
			assert.NotContains(t, out.User, "**Quick mode.**", "plan_findings_only.tmpl should not include quick-mode block for mode=%q", mode)

			out, err = RenderPlanTasksChunk(PlanChunkInput{
				PlanText:   planText,
				ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
				Mode:       mode,
			})
			require.NoError(t, err)
			assert.NotContains(t, out.User, "**Quick mode.**", "plan_tasks_chunk.tmpl should not include quick-mode block for mode=%q", mode)
		})
	}
}

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

const (
	anchorUnverifiableCategory = "unverifiable_codebase_claim"
	anchorUnverifiableGuidance = "verify against the actual code"
	anchorPlanQualityCategory  = "plan_quality"
	anchorPreUnverifiableHead  = "### Unverifiable codebase claims"
)

func TestRenderPlan_UnverifiableCodebaseClaim_InstructionPresent(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorUnverifiableCategory, "plan.tmpl should mention unverifiable_codebase_claim category")
	assert.Contains(t, out.User, anchorUnverifiableGuidance, "plan.tmpl should include the 'verify against the actual code' guidance")
}

func TestRenderPlanFindingsOnly_UnverifiableCodebaseClaim_InstructionPresent(t *testing.T) {
	out, err := RenderPlanFindingsOnly(PlanInput{PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorUnverifiableCategory, "plan_findings_only.tmpl should mention unverifiable_codebase_claim category")
	assert.Contains(t, out.User, anchorUnverifiableGuidance, "plan_findings_only.tmpl should include the 'verify against the actual code' guidance")
}

func TestRenderPlanTasksChunk_UnverifiableCodebaseClaim_InstructionPresent(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n",
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorUnverifiableCategory, "plan_tasks_chunk.tmpl should mention unverifiable_codebase_claim category")
	assert.Contains(t, out.User, anchorUnverifiableGuidance, "plan_tasks_chunk.tmpl should include the 'verify against the actual code' guidance")
}

func TestRenderPre_UnverifiableCodebaseClaim_InstructionPresent(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorUnverifiableCategory, "pre.tmpl should mention unverifiable_codebase_claim category")
	assert.Contains(t, out.User, anchorPreUnverifiableHead, "pre.tmpl should include the new section heading")
}

func TestRenderMid_DoesNotMentionUnverifiableCodebaseClaim(t *testing.T) {
	out, err := RenderMid(MidInput{
		Spec:      sampleSpec(),
		WorkingOn: "writing the handler",
	})
	require.NoError(t, err)
	assert.NotContains(t, out.User, anchorUnverifiableCategory, "mid.tmpl should NOT include unverifiable_codebase_claim (it receives code)")
}

func TestRenderPost_DoesNotMentionUnverifiableCodebaseClaim(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:    sampleSpec(),
		Summary: "implemented X",
	})
	require.NoError(t, err)
	assert.NotContains(t, out.User, anchorUnverifiableCategory, "post.tmpl should NOT include unverifiable_codebase_claim (it receives code)")
}

func TestRenderPlan_PlanQuality_InstructionPresent(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorPlanQualityCategory, "plan.tmpl should mention plan_quality")
	for _, v := range []string{"rough", "actionable", "rigorous"} {
		assert.Contains(t, out.User, v, "plan.tmpl should mention %q quality value", v)
	}
}

func TestRenderPlanFindingsOnly_PlanQuality_InstructionPresent(t *testing.T) {
	out, err := RenderPlanFindingsOnly(PlanInput{PlanText: "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorPlanQualityCategory, "plan_findings_only.tmpl should mention plan_quality")
	for _, v := range []string{"rough", "actionable", "rigorous"} {
		assert.Contains(t, out.User, v, "plan_findings_only.tmpl should mention %q quality value", v)
	}
}

func TestRenderPlanTasksChunk_DoesNotMentionPlanQuality(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   "# Sample plan\n\n### Task 1: A\n\n**Goal:** Test\n",
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: A"}},
	})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "**plan_quality**", "plan_tasks_chunk.tmpl should NOT include the **plan_quality** emission instruction (chunked Pass 2+ doesn't emit it)")
}

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

const (
	anchorExitContractsInstruction          = "exit_contracts"
	anchorExitContractsInferredFlag         = "exit_contracts_inferred"
	anchorExitContractsExplicitHeader       = "**Exit contracts:**"
	anchorExitContractsMaxGuidance          = "at most 20 contracts"
	anchorNormativeServerSideInstruction    = "populated server-side"
	anchorNormativeDoNotEmitInstruction     = "Do NOT emit `normative_test_bodies`"
)

func TestRenderPlan_ExitContractsInstructionPresent(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Plan\n\n### Task 1: A\n\n**Goal:** Test\n"})
	require.NoError(t, err)
	assert.Contains(t, out.User, anchorExitContractsInstruction, "plan.tmpl should ask reviewer to populate exit_contracts")
	assert.Contains(t, out.User, anchorExitContractsInferredFlag, "plan.tmpl should ask reviewer to set exit_contracts_inferred")
	assert.Contains(t, out.User, anchorExitContractsExplicitHeader, "plan.tmpl should mention the explicit **Exit contracts:** plan-side syntax")
	assert.Contains(t, out.User, anchorExitContractsMaxGuidance, "plan.tmpl should bound contracts per task")
	assert.Contains(t, out.User, anchorNormativeServerSideInstruction, "plan.tmpl should tell reviewer NOT to emit normative_test_bodies (server-populated)")
	assert.Contains(t, out.User, anchorNormativeDoNotEmitInstruction, "plan.tmpl should explicitly forbid emitting normative_test_bodies")
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

func TestRenderPost_WithPinnedByIncludesAnchors(t *testing.T) {
	spec := sampleSpec()
	spec.PinnedBy = []string{"HealthHandlerTest.TestOK", "go test ./internal/http"}
	out, err := RenderPost(PostInput{Spec: spec, Summary: "implemented", TestEvidence: "go test PASS"})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Pinned by:")
	assert.Contains(t, out.User, "HealthHandlerTest.TestOK")
	assert.Contains(t, out.User, "caller-supplied anchors")
}

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
