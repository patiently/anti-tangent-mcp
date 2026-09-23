package mcpsrv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestValidateTaskSpec_FindingsCarryDisplayIDsAndTheSessionRecordsThem(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: reviewerFindingsResp(
		`{"severity":"minor","category":"quality","criterion":"spec","evidence":"one","suggestion":"s","same_as":"f_0123abcd"}`,
		`{"severity":"minor","category":"quality","criterion":"Spec.","evidence":"two","suggestion":"s"}`,
	)}
	h := &handlers{deps: newDeps(t, rv)}
	out, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}})
	require.NoError(t, err)

	fp := verdict.Fingerprint(verdict.CategoryQuality, "", "spec")
	require.Len(t, env.Findings, 2)
	assert.Equal(t, fp, env.Findings[0].ID)
	assert.Equal(t, fp+"-2", env.Findings[1].ID)
	assert.Nil(t, env.Findings[0].SameAs, "same_as is never echoed")
	assert.NotContains(t, out.Content[0].(*mcp.TextContent).Text, "same_as")
	assert.Contains(t, env.SummaryBlock, fp+"-2 [minor][quality]")

	st, ok := h.deps.Sessions.ReviewState(env.SessionID)
	require.True(t, ok)
	assert.True(t, st.IssuedIDs[fp])
	assert.True(t, st.IssuedIDs[fp+"-2"])
}

func TestValidateTaskSpec_PlanRunIDAdvisoryCarriesAnIDButIsNotAPreTaskFinding(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: newDeps(t, rv)}
	h.deps.PlanRuns.Create("pass", "actionable", 1)

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, "plan_run_id", env.Findings[0].Criterion)
	assert.NotEmpty(t, env.Findings[0].ID)

	sess, ok := h.deps.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Empty(t, sess.PreFindings, "an advisory about this call's arguments is not a pre-task finding")
}

func TestCheckProgress_FindingsCarryIDsAndTheSessionRecordsThem(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: newDeps(t, rv)}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)

	rv.resp = reviewerFindingsResp(`{"severity":"major","category":"scope_drift","criterion":"AC","evidence":"drift","suggestion":"s"}`)
	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{SessionID: pre.SessionID, WorkingOn: "x"})
	require.NoError(t, err)
	require.Len(t, env.Findings, 1)
	id := env.Findings[0].ID
	assert.Equal(t, verdict.Fingerprint(verdict.CategoryScopeDrift, "", "AC"), id)

	st, ok := h.deps.Sessions.ReviewState(pre.SessionID)
	require.True(t, ok)
	assert.True(t, st.IssuedIDs[id])
	sess, _ := h.deps.Sessions.Get(pre.SessionID)
	require.Len(t, sess.Checkpoints, 1)
	assert.Equal(t, id, sess.Checkpoints[0].Findings[0].ID)
}

func TestValidateCompletion_StoresTheReviewerFindingsWithTheIDsItReturned(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	d.Cfg.Codescene = "required"
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}})
	require.NoError(t, err)

	rv.resp = reviewerFindingsResp(`{"severity":"major","category":"scope_drift","criterion":"AC","evidence":"drift","suggestion":"s"}`)
	_, env, err := h.ValidateCompletion(context.Background(), nil, completionCallArgs(pre.SessionID))
	require.NoError(t, err)
	require.Len(t, env.Findings, 2)
	assert.Equal(t, verdict.CategoryCodesceneNotRun, env.Findings[0].Category, "server findings come first")

	st, ok := h.deps.Sessions.ReviewState(pre.SessionID)
	require.True(t, ok)
	require.Len(t, st.PriorFindings, 1, "only the reviewer's finding is a prior finding")
	assert.Equal(t, env.Findings[1], st.PriorFindings[0])
	assert.True(t, st.IssuedIDs[env.Findings[0].ID])
	assert.True(t, st.IssuedIDs[env.Findings[1].ID])
}

func TestValidateCompletion_RejectedCallsCarryIDs(t *testing.T) {
	h := newTestHandlers(t)
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: "missing", Summary: "x", TestEvidence: "go test ./... PASS",
	})
	require.NoError(t, err)
	require.NotEmpty(t, env.Findings)
	assert.Equal(t, verdict.Fingerprint(verdict.CategorySessionMissing, "", "session"), env.Findings[0].ID)
	assert.Contains(t, env.SummaryBlock, env.Findings[0].ID)
}

// TestValidateCompletion_ConcurrentMalformedEvidenceRejectionsDoNotRace runs
// identical malformed-evidence calls on one session at once. Calls that reach
// the evidence-shape guard store the rejection and the rest read it back from
// the rejection cache; every response must assign its IDs to findings of its
// own, not to the cached entry's, so this passes under -race.
func TestValidateCompletion_ConcurrentMalformedEvidenceRejectionsDoNotRace(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	args := ValidateCompletionArgs{
		SessionID: sid,
		Summary:   "done",
		FinalDiff: "diff --git a/x b/x\n@@ -1 +1 @@\n(truncated)\n",
	}
	want := verdict.Fingerprint(verdict.CategoryMalformedEvidence, "", "evidence_shape")

	const calls = 8
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, env, err := h.ValidateCompletion(context.Background(), nil, args)
			if !assert.NoError(t, err) || !assert.NotEmpty(t, env.Findings) {
				return
			}
			assert.Equal(t, want, env.Findings[0].ID)
			assert.Contains(t, out.Content[0].(*mcp.TextContent).Text, want)
		}()
	}
	wg.Wait()

	cached, ok := lookupCachedRejection(evidenceCacheKey(sid, args.FinalDiff, nil, ""))
	require.True(t, ok)
	assert.Empty(t, cached.Findings[0].ID, "the cached rejection carries no IDs of its own")
}

func TestCheckProgress_RejectedCallCarriesIDs(t *testing.T) {
	h := newTestHandlers(t)
	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{SessionID: "missing", WorkingOn: "x"})
	require.NoError(t, err)
	require.NotEmpty(t, env.Findings)
	assert.NotEmpty(t, env.Findings[0].ID)
}

func TestValidatePlan_FindingsCarryDisplayIDs(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable",
		"plan_findings":[{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"vague","suggestion":"s"}],
		"tasks":[{"task_index":1,"task_title":"Task 1: t1","verdict":"warn","findings":[{"severity":"major","category":"quality","criterion":"spec","evidence":"e","suggestion":"s"}],"suggested_header_block":"","suggested_header_reason":""}],
		"next_action":"n"}`)
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 1)
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, verdict.Fingerprint(verdict.CategoryAmbiguousSpec, planScopeKey, "AC"), pr.PlanFindings[0].ID)
	assert.NotEqual(t, verdict.Fingerprint(verdict.CategoryAmbiguousSpec, "", "AC"), pr.PlanFindings[0].ID,
		"a plan-level finding must not share a session finding's fingerprint")
	require.Len(t, pr.Tasks[0].Findings, 1)
	assert.Equal(t, verdict.Fingerprint(verdict.CategoryQuality, "t1", "spec"), pr.Tasks[0].Findings[0].ID)
	assert.Contains(t, pr.SummaryBlock, pr.Tasks[0].Findings[0].ID)
}

func TestValidatePlan_TaskFindingIDSurvivesRenumbering(t *testing.T) {
	finding := `{"severity":"major","category":"quality","criterion":"spec","evidence":"e","suggestion":"s"}`
	task := func(n int, title string) string {
		return fmt.Sprintf("### Task %d: %s\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n", n, title)
	}
	taskResult := func(n int, title, findings string) string {
		return fmt.Sprintf(`{"task_index":%d,"task_title":"Task %d: %s","verdict":"warn","findings":[%s],"suggested_header_block":"","suggested_header_reason":""}`, n, n, title, findings)
	}
	planResp := func(tasks ...string) []byte {
		return []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[],"tasks":[` + strings.Join(tasks, ",") + `],"next_action":"n"}`)
	}

	first, _, _ := runPlanWithArgs(t, planResp(taskResult(1, "Add parser", finding)),
		ValidatePlanArgs{PlanText: "# Plan\n\n" + task(1, "Add parser")})
	second, _, _ := runPlanWithArgs(t, planResp(taskResult(1, "Add lexer", ""), taskResult(2, "Add parser", finding)),
		ValidatePlanArgs{PlanText: "# Plan\n\n" + task(1, "Add lexer") + task(2, "Add parser")})

	require.Len(t, first.Tasks[0].Findings, 1)
	require.Len(t, second.Tasks[1].Findings, 1)
	assert.Equal(t, first.Tasks[0].Findings[0].ID, second.Tasks[1].Findings[0].ID)
}

func TestValidatePlan_EarlyExitFindingsCarryIDs(t *testing.T) {
	dir := t.TempDir()
	bigPlan := filepath.Join(dir, "big.md")
	require.NoError(t, os.WriteFile(bigPlan, bytes.Repeat([]byte("x"), 5000), 0o600))
	bigContext := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(bigContext, bytes.Repeat([]byte("x"), 500), 0o600))

	cases := []struct {
		name     string
		setup    func(*handlers)
		args     ValidatePlanArgs
		category verdict.Category
	}{
		{"plan too large", func(h *handlers) { h.deps.Cfg.PlanMaxPayloadBytes = 1024 },
			ValidatePlanArgs{PlanPath: bigPlan}, verdict.CategoryTooLarge},
		{"context too large", func(h *handlers) { h.deps.Cfg.ContextMaxFileBytes = 100 },
			ValidatePlanArgs{PlanText: buildPlanWithNTasks(1), ContextPaths: []string{bigContext}}, verdict.CategoryTooLarge},
		{"payload too large", func(h *handlers) { h.deps.Cfg.PlanMaxPayloadBytes = 10 },
			ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}, verdict.CategoryTooLarge},
		{"no task headings", func(*handlers) {},
			ValidatePlanArgs{PlanText: "# a plan with no task headings\n"}, verdict.CategoryOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("go")}
			h := &handlers{deps: newDeps(t, rv)}
			tc.setup(h)
			_, pr, err := h.ValidatePlan(context.Background(), nil, tc.args)
			require.NoError(t, err)
			require.Zero(t, rv.Calls, "an early exit never reaches the reviewer")
			require.Equal(t, verdict.VerdictFail, pr.PlanVerdict)

			var exitFinding bool
			for _, f := range pr.PlanFindings {
				assert.True(t, verdict.ValidDisplayID(f.ID), "finding %q has id %q", f.Criterion, f.ID)
				assert.Contains(t, pr.SummaryBlock, f.ID+" [")
				if f.Category == tc.category && f.Severity == verdict.SeverityCritical {
					exitFinding = true
				}
			}
			assert.True(t, exitFinding, "the early exit's own finding is present: %+v", pr.PlanFindings)
		})
	}
}

func TestValidatePlan_CacheHitCarriesTheSameIDs(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{"plan_verdict":"pass","plan_quality":"actionable","plan_findings":[{"severity":"minor","category":"quality","criterion":"nit","evidence":"e","suggestion":"s"}],"tasks":[{"task_index":1,"task_title":"Task 1: t1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`),
		Model:   "claude-sonnet-4-6",
	}}
	h := &handlers{deps: newDeps(t, rv)}
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}

	_, first, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	_, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	require.Equal(t, 1, rv.Calls, "the second call must be a cache hit")
	assert.Equal(t, first.PlanFindings, second.PlanFindings)
}
