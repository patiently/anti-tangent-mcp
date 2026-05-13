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
