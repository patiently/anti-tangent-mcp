package mcpsrv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func planRunIDFindings(fs []verdict.Finding) []verdict.Finding {
	var out []verdict.Finding
	for _, f := range fs {
		if f.Criterion == "plan_run_id" {
			out = append(out, f)
		}
	}
	return out
}

// twoMinorsResp returns two minor findings, one short of the noise_cluster
// rule, so an advisory that leaked into verdict finalization would lift the
// verdict and the comparison below would catch it.
func twoMinorsResp() providers.Response {
	return providers.Response{
		RawJSON: []byte(`{"verdict":"pass","findings":[` +
			`{"severity":"minor","category":"quality","criterion":"a","evidence":"e","suggestion":"s"},` +
			`{"severity":"minor","category":"quality","criterion":"b","evidence":"e","suggestion":"s"}` +
			`],"next_action":"go"}`),
		Model: "claude-sonnet-4-6",
	}
}

func TestValidateTaskSpec_PlanRunIDAdvisory(t *testing.T) {
	args := ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}}

	t.Run("no live run, no advisory", func(t *testing.T) {
		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, args)
		require.NoError(t, err)
		assert.Empty(t, planRunIDFindings(env.Findings))
	})

	t.Run("live run and no plan_run_id names the run without moving the verdict", func(t *testing.T) {
		baseline := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		_, want, err := baseline.ValidateTaskSpec(context.Background(), nil, args)
		require.NoError(t, err)

		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		run := h.deps.PlanRuns.Create("pass", "actionable", 3)
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, args)
		require.NoError(t, err)

		got := planRunIDFindings(env.Findings)
		require.Len(t, got, 1)
		assert.Equal(t, verdict.SeverityMinor, got[0].Severity)
		assert.Equal(t, verdict.CategoryOther, got[0].Category)
		assert.Contains(t, got[0].Suggestion, "plan_run_id="+run.ID)
		assert.Contains(t, got[0].Suggestion, "session_id")
		assert.Equal(t, want.Verdict, env.Verdict, "the advisory must not change the verdict")
		assert.Contains(t, env.SummaryBlock, run.ID)
	})

	t.Run("plan_run_id passed, no advisory", func(t *testing.T) {
		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		run := h.deps.PlanRuns.Create("pass", "actionable", 3)
		withRun := args
		withRun.PlanRunID = run.ID
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, withRun)
		require.NoError(t, err)
		assert.Empty(t, planRunIDFindings(env.Findings))
	})
}

func TestPlanRunReport_UnattachedRunIsExplained(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := h.deps.PlanRuns.Create("warn", "actionable", 18)

	_, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	got := planRunIDFindings(res.Findings)
	require.Len(t, got, 1)
	assert.Equal(t, verdict.CategoryOther, got[0].Category)
	assert.Contains(t, got[0].Evidence, "no validate_task_spec call passed")
	assert.Contains(t, got[0].Evidence, "18 tasks")
}

func TestPlanRunReport_UnknownRunNamesTheUsualCauseFirst(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	_, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: "pr_does_not_exist"})
	require.NoError(t, err)
	require.Len(t, res.Findings, 1)
	e := res.Findings[0].Evidence
	cause := strings.Index(e, "no validate_task_spec call passed it as plan_run_id")
	expiry := strings.Index(e, h.deps.PlanRuns.TTL().String())
	restart := strings.Index(e, "restarted server")
	require.True(t, cause >= 0 && expiry >= 0 && restart >= 0, "evidence: %s", e)
	assert.True(t, cause < expiry && expiry < restart, "causes must appear in likelihood order: %s", e)
}

func TestValidatePlan_LedgerHeaderKeepsAnUnattachedRunKnown(t *testing.T) {
	ledger := &planrun.Ledger{Dir: t.TempDir()}
	h := newTestPlanHandlers(t)
	h.deps.PlanLedger = ledger

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: "### Task 1: t1\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n",
	})
	require.NoError(t, err)
	require.NotEmpty(t, pr.PlanRunID)

	restarted := newTestPlanHandlers(t)
	restarted.deps.PlanRuns = planrun.NewStore(time.Hour)
	restarted.deps.PlanLedger = ledger
	_, res, err := restarted.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: pr.PlanRunID})
	require.NoError(t, err)
	assert.False(t, hasCategory(res.Findings, verdict.CategorySessionMissing), "a header-only run is known: %+v", res.Findings)
	got := planRunIDFindings(res.Findings)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Evidence, "finished validate_completion")
	assert.NotContains(t, got[0].Evidence, "no validate_task_spec call passed")
	assert.Equal(t, string(pr.PlanVerdict), res.PlanVerdict)
}

func TestValidatePlan_OneLedgerHeaderPerMintedRun(t *testing.T) {
	dir := t.TempDir()
	h := newTestPlanHandlers(t)
	h.deps.PlanLedger = &planrun.Ledger{Dir: dir}
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}

	_, first, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	_, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	require.Equal(t, first.PlanRunID, second.PlanRunID, "an identical passing call inside the cache window reuses its run")

	b, err := os.ReadFile(filepath.Join(dir, "plan-runs.jsonl"))
	require.NoError(t, err)
	headers := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var rec struct {
			HeaderPlanRunID string `json:"header_plan_run_id"`
			Header          bool   `json:"header"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		if rec.Header && rec.HeaderPlanRunID == first.PlanRunID {
			headers++
		}
	}
	assert.Equal(t, 1, headers, "a cache hit must not append a second header")
}
