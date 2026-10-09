package prompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/ratedigest"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

var sampleBoundaryRules = []string{
	"Code never inspects reply or driver words: no regex or phrase matching over them.",
	"The LLM writes every reply; the only code-written text is verbatim legal text.",
}

func boundarySpec() session.TaskSpec {
	s := sampleSpec()
	s.BoundaryRules = sampleBoundaryRules
	return s
}

const agentNetworkPlan = "# Plan\n\n**Plan kind:** agent-network\n\n" +
	"### Task 1: Zip mapper\n\n**Goal:** map the ZIP field\n\n" +
	"### Task 2: Zip prompt\n\n**Kind:** experiment\n**Rung:** prompt\n\n**Goal:** ask for a missing ZIP\n"

func agentNetworkTasks(t *testing.T) []planparser.RawTask {
	t.Helper()
	tasks, _ := planparser.SplitTasks(agentNetworkPlan)
	require.Len(t, tasks, 2)
	return tasks
}

func TestRenderPre_WithBoundaryRules_Golden(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: boundarySpec()})
	require.NoError(t, err)
	golden(t, "pre_with_boundary_rules", out.System+"\n---USER---\n"+out.User)
}

func TestRenderMid_WithBoundaryRules_Golden(t *testing.T) {
	out, err := RenderMid(MidInput{
		Spec:      boundarySpec(),
		WorkingOn: "routing the ZIP question",
		Files:     []File{{Path: "bot/zip.kt", Content: "fun ask(reply: String) = reply.contains(\"zip\")\n"}},
	})
	require.NoError(t, err)
	golden(t, "mid_with_boundary_rules", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPost_WithBoundaryRules_Golden(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:      boundarySpec(),
		Summary:   "Added the ZIP question.",
		FinalDiff: "--- a/bot/zip.kt\n+++ b/bot/zip.kt\n@@ -1 +1 @@\n-fun ask() = null\n+fun ask(reply: String) = reply.contains(\"zip\")\n",
	})
	require.NoError(t, err)
	golden(t, "post_with_boundary_rules", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlan_WithBoundaryRules_Golden(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: agentNetworkPlan, PlanKind: planparser.PlanKindAgentNetwork, BoundaryRules: sampleBoundaryRules})
	require.NoError(t, err)
	golden(t, "plan_with_boundary_rules", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlanChunked_WithBoundaryRules_SharePrefix(t *testing.T) {
	findingsOnly, err := RenderPlanFindingsOnly(PlanInput{PlanText: agentNetworkPlan, PlanKind: planparser.PlanKindAgentNetwork, BoundaryRules: sampleBoundaryRules})
	require.NoError(t, err)
	chunk, err := RenderPlanTasksChunk(PlanChunkInput{PlanText: agentNetworkPlan, ChunkTasks: agentNetworkTasks(t), PlanKind: planparser.PlanKindAgentNetwork, BoundaryRules: sampleBoundaryRules})
	require.NoError(t, err)
	require.Equal(t, findingsOnly.UserPrefix, chunk.UserPrefix, "the rules render in the cached prefix every call of a review shares")
	require.Contains(t, chunk.UserPrefix, "## Boundary rules (caller-supplied, authoritative)")
	golden(t, "plan_findings_only_with_boundary_rules", findingsOnly.System+"\n---USER---\n"+findingsOnly.User)
	golden(t, "plan_tasks_chunk_with_boundary_rules", chunk.System+"\n---USER---\n"+chunk.User)
}

func TestBoundaryRules_FenceOutrunsARuleBacktickRun(t *testing.T) {
	spec := sampleSpec()
	spec.BoundaryRules = []string{"never write ````` in a reply"}
	out, err := RenderPre(PreInput{Spec: spec})
	require.NoError(t, err)
	require.Contains(t, out.User, "``````text\n1. never write ````` in a reply\n``````")
}

func TestRigidityTag_OnlyInAgentNetworkOrRulesMode(t *testing.T) {
	plain := sampleSpec()
	agent := sampleSpec()
	agent.PlanKind = planparser.PlanKindAgentNetwork
	for _, c := range []struct {
		spec session.TaskSpec
		want bool
	}{{plain, false}, {agent, true}, {boundarySpec(), true}} {
		mid, err := RenderMid(MidInput{Spec: c.spec, WorkingOn: "w"})
		require.NoError(t, err)
		post, err := RenderPost(PostInput{Spec: c.spec, Summary: "s", FinalDiff: "+x\n"})
		require.NoError(t, err)
		pre, err := RenderPre(PreInput{Spec: c.spec})
		require.NoError(t, err)
		for _, body := range []string{mid.User, post.User, pre.User} {
			require.Equal(t, c.want, strings.Contains(body, "`rigidity:`"))
		}
	}
}

func experimentSpec() session.TaskSpec {
	s := sampleSpec()
	s.Title = "Ask for a missing ZIP"
	s.Goal = "The bot asks for a missing ZIP more often"
	s.AcceptanceCriteria = []string{
		"evals/core/zip-missing.yaml: after-rate >= 8/10 at n=10, baseline 5/10",
		"keep only if the after-rate meets the threshold and the suite shows no regression; otherwise revert",
	}
	s.TaskKind = planparser.TaskKindExperiment
	s.Rung = "prompt"
	s.PlanKind = planparser.PlanKindAgentNetwork
	return s
}

func agentNetworkBuildSpec() session.TaskSpec {
	s := sampleSpec()
	s.TaskKind = planparser.TaskKindBuild
	s.PlanKind = planparser.PlanKindAgentNetwork
	return s
}

func TestRenderPre_Experiment_Golden(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: experimentSpec()})
	require.NoError(t, err)
	golden(t, "pre_experiment", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPre_AgentNetworkBuild_Golden(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: agentNetworkBuildSpec()})
	require.NoError(t, err)
	golden(t, "pre_agent_network_build", out.System+"\n---USER---\n"+out.User)
}

func TestRenderMid_AgentNetwork_Golden(t *testing.T) {
	out, err := RenderMid(MidInput{Spec: experimentSpec(), WorkingOn: "rewording the ZIP prompt"})
	require.NoError(t, err)
	golden(t, "mid_agent_network", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPost_Experiment_Golden(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:         experimentSpec(),
		Summary:      "Measured 5/10 before and 7/10 after at n=10; below the 8/10 threshold, so reverted.",
		TestEvidence: "zip-missing: before 5/10, after 7/10 (n=10)\nsuite: 42 evals, 0 regressions\n",
	})
	require.NoError(t, err)
	golden(t, "post_experiment", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPost_AgentNetworkBuild_Golden(t *testing.T) {
	out, err := RenderPost(PostInput{Spec: agentNetworkBuildSpec(), Summary: "s", FinalDiff: "+x\n"})
	require.NoError(t, err)
	golden(t, "post_agent_network_build", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlanChunked_AgentNetwork_Golden(t *testing.T) {
	findingsOnly, err := RenderPlanFindingsOnly(PlanInput{PlanText: agentNetworkPlan, PlanKind: planparser.PlanKindAgentNetwork})
	require.NoError(t, err)
	chunk, err := RenderPlanTasksChunk(PlanChunkInput{PlanText: agentNetworkPlan, ChunkTasks: agentNetworkTasks(t), PlanKind: planparser.PlanKindAgentNetwork})
	require.NoError(t, err)
	require.Equal(t, findingsOnly.UserPrefix, chunk.UserPrefix)
	golden(t, "plan_findings_only_agent_network", findingsOnly.System+"\n---USER---\n"+findingsOnly.User)
	golden(t, "plan_tasks_chunk_agent_network", chunk.System+"\n---USER---\n"+chunk.User)
}

func TestRenderPlan_ExperimentWithoutAgentNetworkPlan(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "### Task 1: x\n\n**Kind:** experiment\n", ExperimentTitles: []string{"Task 1: x"}})
	require.NoError(t, err)
	require.Contains(t, out.User, "Apply this to the experiment tasks named below.")
	require.Contains(t, out.User, "**Experiment protocol.** These tasks of the plan are experiments:\n- Task 1: x\n")
	require.NotContains(t, out.User, "### Fix ladder", "the fix ladder is for agent-network plans only")
	require.NotContains(t, out.User, "`rigidity:`")
}

func TestAgentModeSections_AbsentByDefault(t *testing.T) {
	pre, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	post, err := RenderPost(PostInput{Spec: sampleSpec(), Summary: "s", FinalDiff: "+x\n"})
	require.NoError(t, err)
	plan, err := RenderPlan(PlanInput{PlanText: "### Task 1: x\n"})
	require.NoError(t, err)
	for _, body := range []string{pre.User, post.User, plan.User} {
		for _, marker := range []string{"determinism_demand", "experiment_protocol", "### Fix ladder", "### Rates as evidence", "### Experiment outcome", "## Boundary rules"} {
			require.NotContains(t, body, marker)
		}
	}
}

func TestRenderPost_ExperimentWithRateDigest_Golden(t *testing.T) {
	five, seven, kept := 5, 7, false
	out, err := RenderPost(PostInput{
		Spec:         experimentSpec(),
		Summary:      "Measured 5/10 before and 7/10 after at n=10; below the 8/10 threshold, so reverted.",
		TestEvidence: "zip-missing: before 5/10, after 7/10 (n=10)\nsuite: 42 evals, 0 regressions\n",
		RateDigest: &ratedigest.Digest{
			TargetEval: "evals/core/zip-missing.yaml", N: 10, BeforeK: &five, AfterK: &seven,
			Suite: &ratedigest.Suite{Evals: 42}, Kept: &kept,
			RigidityDelta: &ratedigest.RigidityDelta{},
		},
	})
	require.NoError(t, err)
	golden(t, "post_experiment_with_rate_digest", out.System+"\n---USER---\n"+out.User)
}
