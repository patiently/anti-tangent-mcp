package mcpsrv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// boundaryResp is a per-task review that raises one major boundary breach.
func boundaryResp() providers.Response {
	return providers.Response{
		RawJSON: []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"boundary_violation","criterion":"boundary:1",` +
			`"evidence":"+ if (reply.contains(\"zip\"))","suggestion":"give the model the ZIP field","same_as":null}],"next_action":"move the check"}`),
		Model: "claude-sonnet-4-6",
	}
}

// agentNetworkTaskHandlers serves per-task calls from task and validate_plan
// calls from plan, so one test can mint a plan run and attach tasks to it.
func agentNetworkTaskHandlers(t *testing.T) (*handlers, *fakeReviewer, *fakeReviewer) {
	t.Helper()
	task := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	plan := &fakeReviewer{name: "openai", resp: agentNetworkPlanResp()}
	d := newDeps(t, task)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"anthropic": task, "openai": plan}
	return &handlers{deps: d}, task, plan
}

var testRules = []string{"Code never matches on reply text."}

func TestValidateTaskSpec_BoundaryRulesReachTheSessionAndCheckProgress(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	task.resp = boundaryResp()
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Zip question", Goal: "ask for a missing ZIP", BoundaryRules: testRules,
	})
	require.NoError(t, err)
	require.Contains(t, task.LastRequest.User, "1. Code never matches on reply text.")
	require.Equal(t, []verdict.Category{verdict.CategoryBoundaryViolation, verdict.CategoryPlanKindMissing}, findingCategories(env.Findings))
	require.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	sess, ok := h.deps.Sessions.Get(env.SessionID)
	require.True(t, ok)
	require.Equal(t, testRules, sess.Spec.BoundaryRules)

	_, progress, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: env.SessionID, WorkingOn: "routing", ChangedFiles: []FileArg{{Path: "bot.kt", Content: "x"}},
	})
	require.NoError(t, err)
	require.Contains(t, task.LastRequest.User, "1. Code never matches on reply text.")
	require.Len(t, progress.Findings, 1)
	require.Equal(t, verdict.CategoryBoundaryViolation, progress.Findings[0].Category)
	require.Equal(t, verdict.SeverityMinor, progress.Findings[0].Severity, "whole files cannot show what the task added")
}

func TestValidateTaskSpec_NoRulesDropsBoundaryViolation(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	task.resp = boundaryResp()
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "X", Goal: "Y"})
	require.NoError(t, err)
	require.Empty(t, findingCategories(env.Findings))
	require.Equal(t, "pass", env.Verdict)
}

func TestValidateTaskSpec_PlanRunValuesWin(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: agentNetworkPlanText, BoundaryRules: testRules})
	require.NoError(t, err)

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 2: Zip prompt", Goal: "ask for a missing ZIP",
		PlanRunID: pr.PlanRunID, TaskIndex: 2, TaskKind: "build",
	})
	require.NoError(t, err)
	require.Equal(t, []verdict.Category{verdict.CategoryKindConflict, verdict.CategoryRungMissing}, findingCategories(env.Findings))
	require.Contains(t, env.Findings[0].Evidence, `task_kind "build"`)
	require.Equal(t, "pass", env.Verdict, "notes about the call never move the verdict")

	sess, ok := h.deps.Sessions.Get(env.SessionID)
	require.True(t, ok)
	require.Equal(t, "experiment", sess.Spec.TaskKind)
	require.Equal(t, "agent-network", sess.Spec.PlanKind)
	require.Equal(t, testRules, sess.Spec.BoundaryRules, "an attached call that sends no rules inherits the run's")
	require.Contains(t, task.LastRequest.User, "### Experiment protocol")

	_, _, err = h.CheckProgress(context.Background(), nil, CheckProgressArgs{SessionID: env.SessionID, WorkingOn: "w"})
	require.NoError(t, err)
	require.Contains(t, task.LastRequest.User, "`rigidity:`")
}

func TestValidateTaskSpec_OwnRulesOverrideTheRun(t *testing.T) {
	h, _, _ := agentNetworkTaskHandlers(t)
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: agentNetworkPlanText, BoundaryRules: testRules})
	require.NoError(t, err)
	own := []string{"No code-written outbound text."}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 1: Zip mapper", Goal: "map", PlanRunID: pr.PlanRunID, TaskIndex: 1, BoundaryRules: own,
	})
	require.NoError(t, err)
	sess, _ := h.deps.Sessions.Get(env.SessionID)
	require.Equal(t, own, sess.Spec.BoundaryRules)
	require.Equal(t, "build", sess.Spec.TaskKind)
	require.Contains(t, findingCategories(env.Findings), verdict.CategoryKindConflict, "replacing the run's rules is noted")
	var note verdict.Finding
	for _, f := range env.Findings {
		if f.Criterion == "boundary_rules" {
			note = f
		}
	}
	require.Contains(t, note.Evidence, "reviewed against the call's rules only")
}

func TestValidateTaskSpec_SameRulesAsTheRunDrawNoNote(t *testing.T) {
	h, _, _ := agentNetworkTaskHandlers(t)
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: agentNetworkPlanText, BoundaryRules: testRules})
	require.NoError(t, err)
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 1: Zip mapper", Goal: "map", PlanRunID: pr.PlanRunID, TaskIndex: 1, BoundaryRules: testRules,
	})
	require.NoError(t, err)
	require.NotContains(t, findingCategories(env.Findings), verdict.CategoryKindConflict)
}

func TestSessionSpec_APlainPlanRevisionThatDropsKindExperiment(t *testing.T) {
	h, _, _ := agentNetworkTaskHandlers(t)
	withKind := "# Plan\n\n### Task 1: Zip mapper\n\n**Goal:** map\n\n### Task 2: Zip prompt\n\n**Kind:** experiment\n\n**Goal:** ask\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: withKind})
	require.NoError(t, err)
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 2: Zip prompt", Goal: "ask", PlanRunID: pr.PlanRunID, TaskIndex: 2,
	})
	require.NoError(t, err)
	sess, ok := h.deps.Sessions.Get(env.SessionID)
	require.True(t, ok)
	require.Equal(t, "experiment", h.sessionSpec(sess).TaskKind)

	// The run as a revision without the Kind line leaves it: a plain plan
	// declares no task kind.
	_, ok = h.deps.PlanRuns.Revise(pr.PlanRunID, "pass", "rigorous", []planrun.PlanTask{
		{Index: 1, Title: "Task 1: Zip mapper"}, {Index: 2, Title: "Task 2: Zip prompt"},
	}, "", nil, nil)
	require.True(t, ok)
	require.Empty(t, h.sessionSpec(sess).TaskKind, "the plan no longer declares an experiment")
}

func TestSessionSpec_KeepsAKindTheCallDeclaredItself(t *testing.T) {
	h, _, _ := agentNetworkTaskHandlers(t)
	plain := "# Plan\n\n### Task 1: Zip mapper\n\n**Goal:** map\n\n### Task 2: Zip prompt\n\n**Goal:** ask\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plain})
	require.NoError(t, err)
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 2: Zip prompt", Goal: "ask", PlanRunID: pr.PlanRunID, TaskIndex: 2, TaskKind: "experiment", Rung: "prompt",
	})
	require.NoError(t, err)
	sess, _ := h.deps.Sessions.Get(env.SessionID)
	require.Equal(t, "experiment", h.sessionSpec(sess).TaskKind)
}

func TestValidateTaskSpec_UnknownArgValues(t *testing.T) {
	h, _, _ := agentNetworkTaskHandlers(t)
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "X", Goal: "Y", TaskKind: "spike", PlanKind: "chatbot", Rung: "top",
	})
	require.NoError(t, err)
	require.Equal(t, []verdict.Category{verdict.CategoryUnknownKind, verdict.CategoryUnknownKind, verdict.CategoryUnknownKind}, findingCategories(env.Findings))
	sess, _ := h.deps.Sessions.Get(env.SessionID)
	require.Empty(t, sess.Spec.TaskKind)
	require.Empty(t, sess.Spec.PlanKind)
}

func TestValidateCompletion_LightweightExperimentRejected(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "reverted at 7/10", TestEvidence: "zip-missing 7/10", TaskKind: "experiment", Rung: "prompt",
	})
	require.NoError(t, err)
	require.Zero(t, task.Calls, "rejected before review")
	require.Equal(t, "warn", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.CategoryInsufficientEvidence, env.Findings[0].Category)
	require.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	require.Equal(t, "experiment tasks need a session; call validate_task_spec first", env.Findings[0].Evidence)
	require.True(t, env.Lightweight)
}

func TestValidateCompletion_LightweightExperimentFromThePlanRun(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: agentNetworkPlanText, BoundaryRules: testRules})
	require.NoError(t, err)
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "done", TestEvidence: "ok", PlanRunID: pr.PlanRunID, TaskIndex: 2,
	})
	require.NoError(t, err)
	require.Zero(t, task.Calls)
	require.Equal(t, "session", env.Findings[0].Criterion, "the run records the task as an experiment though the call did not say so")
}

func TestValidateCompletion_DiffRequired(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	_, spec, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "X", Goal: "Y", BoundaryRules: testRules})
	require.NoError(t, err)
	callsAfterSpec := task.Calls

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: spec.SessionID, Summary: "s", FinalFiles: []CompletionFileArg{{Path: "bot.kt", Content: strPtr("x")}},
	})
	require.NoError(t, err)
	require.Equal(t, callsAfterSpec, task.Calls, "rejected before review")
	require.Equal(t, []verdict.Category{verdict.CategoryDiffRequired}, findingCategories(env.Findings))
	require.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	require.True(t, env.SubmissionDefectOnly)

	_, env, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: spec.SessionID, Summary: "s", TestEvidence: "ok 1 test",
	})
	require.NoError(t, err)
	require.Equal(t, callsAfterSpec+1, task.Calls, "test evidence alone is reviewed")
	require.NotContains(t, findingCategories(env.Findings), verdict.CategoryDiffRequired)
	require.Equal(t, []verdict.Category{verdict.CategoryBoundaryUnchecked}, findingCategories(env.Findings),
		"a build task's code went unchecked against the rules, and the caller is told")
	require.Equal(t, verdict.SeverityMinor, env.Findings[0].Severity)
	require.Equal(t, "pass", env.Verdict, "the note never moves the verdict")

	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: spec.SessionID, Summary: "s", FinalDiff: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n",
		FinalFiles: []CompletionFileArg{{Path: "bot.kt", Content: strPtr("x")}},
	})
	require.NoError(t, err)
	require.Equal(t, callsAfterSpec+2, task.Calls, "files with a diff are reviewed")
	require.Contains(t, task.LastRequest.User, "### Boundary breaches")
}

func TestValidateCompletion_LightweightRulesNeedADiff(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", FinalFiles: []CompletionFileArg{{Path: "bot.kt", Content: strPtr("x")}}, BoundaryRules: testRules,
	})
	require.NoError(t, err)
	require.Zero(t, task.Calls)
	require.Equal(t, []verdict.Category{verdict.CategoryDiffRequired}, findingCategories(env.Findings))
}

func TestValidateCompletion_NoRulesDropsBoundaryViolation(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	task.resp = boundaryResp()
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", FinalDiff: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n",
	})
	require.NoError(t, err)
	require.NotContains(t, findingCategories(env.Findings), verdict.CategoryBoundaryViolation)
}

func TestSessionSpec_APlanHeadingFindsATaskAttachedByIndexUnderAnotherTitle(t *testing.T) {
	h, _, _ := agentNetworkTaskHandlers(t)
	plain := "# Plan\n\n### Task 1: Zip mapper\n\n**Goal:** map\n\n### Task 2: Zip prompt\n\n**Goal:** ask\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plain})
	require.NoError(t, err)
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Map the ZIP fields", Goal: "map", PlanRunID: pr.PlanRunID, TaskIndex: 1,
	})
	require.NoError(t, err)
	sess, ok := h.deps.Sessions.Get(env.SessionID)
	require.True(t, ok)

	_, ok = h.deps.PlanRuns.Revise(pr.PlanRunID, "pass", "rigorous", []planrun.PlanTask{
		{Index: 1, Title: "Task 1: Probe", Kind: "experiment", Rung: "variance"},
		{Index: 2, Title: "Task 2: Zip mapper"},
		{Index: 3, Title: "Task 3: Zip prompt"},
	}, "", nil, nil)
	require.True(t, ok)
	require.Empty(t, h.sessionSpec(sess).TaskKind, "the mapper moved to Task 2; the probe inserted above it is not its task")
}

func TestValidateCompletion_ARevertedExperimentWithRulesDrawsNoUncheckedNote(t *testing.T) {
	h, _, _ := agentNetworkTaskHandlers(t)
	_, spec, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Zip prompt", Goal: "ask", TaskKind: "experiment", Rung: "prompt", BoundaryRules: testRules,
	})
	require.NoError(t, err)
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: spec.SessionID, Summary: "reverted at 7/10", TestEvidence: "zip-missing 7/10",
	})
	require.NoError(t, err)
	require.NotContains(t, findingCategories(env.Findings), verdict.CategoryBoundaryUnchecked, "a reverted experiment leaves no code to check")
}
