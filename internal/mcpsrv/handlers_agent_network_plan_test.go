package mcpsrv

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

const agentNetworkPlanText = "# Plan\n\n**Plan kind:** agent-network\n\n" +
	"### Task 1: Zip mapper\n\n**Goal:** map the ZIP field\n\n" +
	"### Task 2: Zip prompt\n\n**Kind:** experiment\n\n**Goal:** ask for a missing ZIP\n"

// agentNetworkPlanResp answers a two-task plan review. It marks the
// experiment lightweight-eligible, raises a boundary breach on it, and raises
// a plan-level fix-ladder finding.
func agentNetworkPlanResp() providers.Response {
	return providers.Response{
		RawJSON: []byte(`{"plan_verdict":"pass","plan_quality":"rigorous","plan_findings":[` +
			`{"severity":"major","category":"boundary_violation","criterion":"fix_ladder","evidence":"every fix task is rung commitment","suggestion":"add a facts task"}],` +
			`"tasks":[` +
			`{"task_index":1,"task_title":"Task 1: Zip mapper","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":"","lightweight_eligible":true,"lightweight_reason":"one file"},` +
			`{"task_index":2,"task_title":"Task 2: Zip prompt","verdict":"warn","findings":[{"severity":"major","category":"boundary_violation","criterion":"boundary:1","evidence":"AC requires a code-written question","suggestion":"put the question in the prompt"}],"suggested_header_block":"","suggested_header_reason":"","lightweight_eligible":true,"lightweight_reason":"one file"}` +
			`],"next_action":"go"}`),
		Model: "gpt-5",
	}
}

func agentNetworkPlanHandlers(t *testing.T, resp providers.Response) (*handlers, *fakeReviewer) {
	t.Helper()
	rv := &fakeReviewer{name: "openai", resp: resp}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	return &handlers{deps: d}, rv
}

// findingCategories lists fs's categories, leaving out CategoryOther: these
// tests pass plan_text, which draws the deprecation note.
func findingCategories(fs []verdict.Finding) []verdict.Category {
	out := make([]verdict.Category, 0, len(fs))
	for _, f := range fs {
		if f.Category != verdict.CategoryOther {
			out = append(out, f.Category)
		}
	}
	return out
}

func TestValidatePlan_AgentNetwork_NoRules(t *testing.T) {
	h, rv := agentNetworkPlanHandlers(t, agentNetworkPlanResp())
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: agentNetworkPlanText})
	require.NoError(t, err)

	require.Contains(t, rv.LastRequest.User, "### Fix ladder")
	require.NotContains(t, rv.LastRequest.User, "## Boundary rules")

	require.Len(t, pr.Tasks, 2)
	require.Equal(t, "build", pr.Tasks[0].TaskKind)
	require.True(t, pr.Tasks[0].LightweightEligible, "a build task keeps the reviewer's lightweight call")
	require.Equal(t, "experiment", pr.Tasks[1].TaskKind)
	require.Equal(t, "agent-network", pr.Tasks[1].PlanKind)
	require.False(t, pr.Tasks[1].LightweightEligible)
	require.Equal(t, "experiment task", pr.Tasks[1].LightweightReason)

	// No rules: the reviewer's boundary:1 finding is dropped, the fix-ladder
	// finding kept, and the experiment without a rung draws rung_missing.
	require.Equal(t, []verdict.Category{verdict.CategoryRungMissing}, findingCategories(pr.Tasks[1].Findings))
	require.Contains(t, findingCategories(pr.PlanFindings), verdict.CategoryBoundaryViolation)
	require.Contains(t, findingCategories(pr.PlanFindings), verdict.CategoryBoundaryRulesMissing)
	require.NotContains(t, findingCategories(pr.PlanFindings), verdict.CategoryPlanKindMissing)

	got, ok := h.deps.PlanRuns.TaskAgentNetwork(pr.PlanRunID, planrun.TaskRef{Index: 2})
	require.True(t, ok)
	require.Equal(t, planrun.AgentNetwork{PlanKind: "agent-network", TaskKind: "experiment", TaskFound: true}, got)
}

func TestValidatePlan_AgentNetwork_WithRules(t *testing.T) {
	h, rv := agentNetworkPlanHandlers(t, agentNetworkPlanResp())
	rules := []string{"  Code never matches on reply text.  ", ""}
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: agentNetworkPlanText, BoundaryRules: rules})
	require.NoError(t, err)

	require.Contains(t, rv.LastRequest.User, "1. Code never matches on reply text.\n")
	require.Equal(t, []verdict.Category{verdict.CategoryBoundaryViolation, verdict.CategoryRungMissing}, findingCategories(pr.Tasks[1].Findings))
	require.Equal(t, verdict.SeverityMajor, pr.Tasks[1].Findings[0].Severity)
	require.NotContains(t, findingCategories(pr.PlanFindings), verdict.CategoryBoundaryRulesMissing)

	got, ok := h.deps.PlanRuns.TaskAgentNetwork(pr.PlanRunID, planrun.TaskRef{Title: "Zip prompt"})
	require.True(t, ok)
	require.Equal(t, []string{"Code never matches on reply text."}, got.BoundaryRules)
}

func TestValidatePlan_RulesWithoutPlanKind(t *testing.T) {
	h, rv := agentNetworkPlanHandlers(t, planPassResp())
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:      "# Plan\n\n### Task 1: First\n\nbody\n",
		BoundaryRules: []string{"no regex over replies"},
	})
	require.NoError(t, err)
	require.Contains(t, rv.LastRequest.User, "## Boundary rules")
	require.NotContains(t, rv.LastRequest.User, "### Fix ladder")
	require.Contains(t, findingCategories(pr.PlanFindings), verdict.CategoryPlanKindMissing)
	require.Equal(t, verdict.VerdictPass, pr.PlanVerdict, "a minor note never moves the verdict")
}

func TestValidatePlan_UnknownHeaders(t *testing.T) {
	h, rv := agentNetworkPlanHandlers(t, planPassResp())
	plan := "# Plan\n\n**Plan kind:** chatbot\n\n### Task 1: First\n\n**Kind:** spike\n**Rung:** top\n\nbody\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.NotContains(t, rv.LastRequest.User, "### Fix ladder")
	require.Equal(t, []verdict.Category{verdict.CategoryUnknownKind}, findingCategories(pr.PlanFindings))
	require.Contains(t, pr.PlanFindings[len(pr.PlanFindings)-1].Evidence, `"chatbot"`)
	require.Len(t, pr.Tasks, 1)
	require.Equal(t, []verdict.Category{verdict.CategoryUnknownKind, verdict.CategoryUnknownKind}, findingCategories(pr.Tasks[0].Findings))
	require.Empty(t, pr.Tasks[0].TaskKind, "an unknown kind on a plan with no known plan kind declares nothing")
	require.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
}

func TestValidatePlan_PlainPlanOwnKindLabels(t *testing.T) {
	h, _ := agentNetworkPlanHandlers(t, planPassResp())
	plan := "# Plan\n\n### Task 1: First\n\n**Kind:** bugfix\n**Rung:** 2\n\nbody\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Empty(t, findingCategories(pr.PlanFindings))
	require.Empty(t, findingCategories(pr.Tasks[0].Findings), "a plan with no Plan kind header may use Kind and Rung labels of its own")
	require.Empty(t, pr.Tasks[0].TaskKind)
}

func TestValidatePlan_PlainPlanUnchanged(t *testing.T) {
	h, rv := agentNetworkPlanHandlers(t, planPassResp())
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "# Plan\n\n### Task 1: First\n\nbody\n"})
	require.NoError(t, err)
	require.Empty(t, findingCategories(pr.PlanFindings))
	require.Empty(t, pr.Tasks[0].Findings)
	require.Empty(t, pr.Tasks[0].PlanKind)
	require.Empty(t, pr.Tasks[0].TaskKind, "a plain plan declares no task kind")
	got, ok := h.deps.PlanRuns.TaskAgentNetwork(pr.PlanRunID, planrun.TaskRef{Index: 1})
	require.True(t, ok)
	require.Equal(t, planrun.AgentNetwork{TaskFound: true}, got, "a plain plan's run stores no declaration")
	for _, marker := range []string{"## Boundary rules", "### Fix ladder", "determinism_demand", "experiment_protocol"} {
		require.NotContains(t, rv.LastRequest.User, marker)
	}
}

func TestValidatePlan_BoundaryRulesLimits(t *testing.T) {
	h, _ := agentNetworkPlanHandlers(t, planPassResp())
	tooMany := make([]string, maxBoundaryRules+1)
	for i := range tooMany {
		tooMany[i] = "rule"
	}
	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "### Task 1: x\n", BoundaryRules: tooMany})
	require.ErrorContains(t, err, "boundary_rules must contain at most 20 entries")
	_, _, err = h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "### Task 1: x\n", BoundaryRules: []string{strings.Repeat("x", maxBoundaryRuleChars+1)}})
	require.ErrorContains(t, err, "boundary_rules[0] must be at most 1000 characters")
}

func TestPlanInputsKey_AgentNetwork(t *testing.T) {
	base := planInputs{ProjectKnowledge: "pk", Mode: "thorough", Model: "openai:gpt-5"}
	withKind := base
	withKind.PlanKind = "agent-network"
	withRules := withKind
	withRules.BoundaryRules = []string{"rule"}
	otherRules := withKind
	otherRules.BoundaryRules = []string{"other rule"}
	keys := map[string]bool{base.key(): true, withKind.key(): true, withRules.key(): true, otherRules.key(): true}
	require.Len(t, keys, 4, "plan kind and rules each change what the reviewer is shown")
}
