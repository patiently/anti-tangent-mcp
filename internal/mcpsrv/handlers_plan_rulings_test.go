package mcpsrv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// runPlanWithArgs runs one validate_plan call whose single-call reviewer
// answers raw, and strips the plan_text deprecation notice.
func runPlanWithArgs(t *testing.T, raw []byte, args ValidatePlanArgs) (verdict.PlanResult, *fakeReviewer, *handlers) {
	t.Helper()
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}
	_, pr, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	pr.PlanFindings = stripPlanDeprecationFinding(pr.PlanFindings)
	return pr, rv, h
}

func planJSON(planFindings, taskTitle, taskFindings string) []byte {
	return []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[` + planFindings +
		`],"tasks":[{"task_index":1,"task_title":"` + taskTitle + `","verdict":"warn","findings":[` + taskFindings +
		`],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"n"}`)
}

func TestValidatePlan_ChecklistNoLongerLiftsTheVerdict(t *testing.T) {
	raw := planJSON(
		`{"severity":"minor","category":"quality","criterion":"a","evidence":"e","suggestion":"s"},{"severity":"minor","category":"quality","criterion":"b","evidence":"e","suggestion":"s"}`,
		"Task 1: t1",
		`{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"cites Foo.kt","suggestion":"verify"}`)
	pr, _, _ := runPlanWithArgs(t, raw, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})

	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.True(t, hasCriterion(pr.PlanFindings, "codebase_reference_checklist"))
	assert.False(t, hasCriterion(pr.PlanFindings, "noise_cluster"))
}

func TestValidatePlan_RulingsWaivePlanAndTaskFindings(t *testing.T) {
	raw := planJSON(
		`{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"vague","suggestion":"s"}`,
		"Task 1: t1",
		`{"severity":"major","category":"quality","criterion":"spec","evidence":"thin","suggestion":"s"}`)
	planID := verdict.Fingerprint(verdict.CategoryAmbiguousSpec, "", "AC")
	taskID := verdict.Fingerprint(verdict.CategoryQuality, "t1", "spec")
	pr, _, _ := runPlanWithArgs(t, raw, ValidatePlanArgs{
		PlanText: buildPlanWithNTasks(1),
		ControllerRulings: []ControllerRulingArg{
			{FindingID: planID, Ruling: "Intended"},
			{FindingID: taskID + "-2", Ruling: "Covered by task 3"},
		},
	})

	assert.False(t, hasCriterion(pr.PlanFindings, "AC"))
	require.Len(t, pr.WaivedFindings, 1)
	assert.Equal(t, planID, pr.WaivedFindings[0].ID)
	assert.Equal(t, "Intended", pr.WaivedFindings[0].Ruling)
	assert.Empty(t, pr.Tasks[0].Findings)
	require.Len(t, pr.Tasks[0].WaivedFindings, 1)
	assert.Equal(t, taskID, pr.Tasks[0].WaivedFindings[0].ID)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.Equal(t, verdict.VerdictPass, pr.Tasks[0].Verdict)
	assert.Contains(t, pr.SummaryBlock, "waived: "+planID)
}

func TestValidatePlan_RulingMatchesATaskFindingAcrossRenumbering(t *testing.T) {
	raw := planJSON("", "Task 5: t1",
		`{"severity":"major","category":"quality","criterion":"spec","evidence":"thin","suggestion":"s"}`)
	pr, _, _ := runPlanWithArgs(t, raw, ValidatePlanArgs{
		PlanText:          buildPlanWithNTasks(1),
		ControllerRulings: []ControllerRulingArg{{FindingID: verdict.Fingerprint(verdict.CategoryQuality, "t1", "spec"), Ruling: "fine"}},
	})
	assert.Empty(t, pr.Tasks[0].Findings)
	assert.Len(t, pr.Tasks[0].WaivedFindings, 1)
}

func TestValidatePlan_RulingsAndVerifiedReferencesReachEveryReviewerCall(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{
		passOneResp(),
		chunkResp(t, titlesRange(1, 8)),
		chunkResp(t, titlesRange(9, 9)),
	}}
	h := &handlers{deps: newDepsWithScripted(t, sr, 8)}
	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:                     buildPlanWithNTasks(9),
		ControllerRulings:            []ControllerRulingArg{{FindingID: "f_0123abcd", Ruling: "Covered elsewhere"}},
		ControllerVerifiedReferences: []string{"internal/verdict/verdict.go"},
	})
	require.NoError(t, err)
	require.Len(t, sr.requests, 3)
	for i, req := range sr.requests {
		prompt := req.CachePrefix + req.User
		assert.Contains(t, prompt, "## Controller rulings (authoritative)", "call %d", i)
		assert.Contains(t, prompt, "- f_0123abcd: Covered elsewhere", "call %d", i)
		assert.Contains(t, prompt, "Controller-verified references:", "call %d", i)
		assert.Contains(t, prompt, "- internal/verdict/verdict.go", "call %d", i)
	}
}

func TestValidatePlan_RulingsKeyThePlanCache(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("go")}
	h := &handlers{deps: newDeps(t, rv)}
	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.NoError(t, err)
	_, _, err = h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          buildPlanWithNTasks(1),
		ControllerRulings: []ControllerRulingArg{{FindingID: "f_0123abcd", Ruling: "r"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, rv.Calls, "a new ruling changes the prompt, so it misses the cache")
}

func TestValidatePlan_VerifiedReferencesSuppressBeforeTheChecklist(t *testing.T) {
	raw := planJSON("", "Task 1: t1",
		`{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"internal/foo.go defines Bar","suggestion":"verify"}`)

	control, _, _ := runPlanWithArgs(t, raw, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.True(t, hasCriterion(control.PlanFindings, "codebase_reference_checklist"))

	pr, _, _ := runPlanWithArgs(t, raw, ValidatePlanArgs{
		PlanText:                     buildPlanWithNTasks(1),
		ControllerVerifiedReferences: []string{"internal/foo.go"},
	})
	assert.False(t, hasCriterion(pr.PlanFindings, "codebase_reference_checklist"))
}

func TestValidatePlan_ADemotedContradictionIsSuppressedByAVerifiedReference(t *testing.T) {
	raw := planJSON("", "Task 1: t1",
		`{"severity":"major","category":"contradicted_codebase_claim","criterion":"spec","evidence":"internal/foo.go has no Bar","suggestion":"fix"}`)
	pr, _, _ := runPlanWithArgs(t, raw, ValidatePlanArgs{
		PlanText:                     buildPlanWithNTasks(1),
		ControllerVerifiedReferences: []string{"internal/foo.go"},
	})
	assert.Empty(t, pr.Tasks[0].Findings)
	assert.False(t, hasCriterion(pr.PlanFindings, "codebase_reference_checklist"))
}

func TestValidatePlan_OnlyAMalformedRulingIDDrawsAnAdvisory(t *testing.T) {
	pr, _, _ := runPlanWithArgs(t, passPlanResp("go").RawJSON, ValidatePlanArgs{
		PlanText: buildPlanWithNTasks(1),
		ControllerRulings: []ControllerRulingArg{
			{FindingID: "F#3", Ruling: "malformed"},
			{FindingID: "f_0123abcd", Ruling: "well-formed, waives nothing"},
		},
	})
	var advisories []verdict.Finding
	for _, f := range pr.PlanFindings {
		if f.Criterion == "controller_rulings" {
			advisories = append(advisories, f)
		}
	}
	require.Len(t, advisories, 1)
	assert.Contains(t, advisories[0].Evidence, "F#3")
	assert.NotContains(t, advisories[0].Evidence, "f_0123abcd")
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
}

func TestValidatePlan_TheChecklistCannotBeWaived(t *testing.T) {
	raw := planJSON("", "Task 1: t1",
		`{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"cites Foo.kt","suggestion":"verify"}`)
	pr, _, _ := runPlanWithArgs(t, raw, ValidatePlanArgs{
		PlanText: buildPlanWithNTasks(1),
		ControllerRulings: []ControllerRulingArg{{
			FindingID: verdict.Fingerprint(verdict.CategoryUnverifiableCodebaseClaim, "", "codebase_reference_checklist"),
			Ruling:    "stop showing the checklist",
		}},
	})
	assert.True(t, hasCriterion(pr.PlanFindings, "codebase_reference_checklist"))
	assert.Empty(t, pr.WaivedFindings)
}

func TestValidatePlan_CacheHitReproducesWaivers(t *testing.T) {
	raw := planJSON(`{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"vague","suggestion":"s"}`, "Task 1: t1", "")
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{RawJSON: raw, Model: "claude-sonnet-4-6"}}
	h := &handlers{deps: newDeps(t, rv)}
	args := ValidatePlanArgs{
		PlanText:          buildPlanWithNTasks(1),
		ControllerRulings: []ControllerRulingArg{{FindingID: verdict.Fingerprint(verdict.CategoryAmbiguousSpec, "", "AC"), Ruling: "Intended"}},
	}

	_, first, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	require.Equal(t, verdict.VerdictPass, first.PlanVerdict)
	_, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)

	assert.Equal(t, 1, rv.Calls, "the second call must be a cache hit")
	assert.Equal(t, first.WaivedFindings, second.WaivedFindings)
}

func TestPlanPassCache_LookupReturnsIndependentWaivedSlices(t *testing.T) {
	cache := newPlanPassCache()
	key := [32]byte{2}
	cache.store(key, verdict.PlanResult{
		PlanVerdict:    verdict.VerdictPass,
		WaivedFindings: []verdict.WaivedFinding{{Ruling: "plan ruling"}},
		Tasks: []verdict.PlanTaskResult{{
			TaskIndex: 1, TaskTitle: "Task 1: t1", Verdict: verdict.VerdictPass,
			WaivedFindings: []verdict.WaivedFinding{{Ruling: "task ruling"}},
		}},
		NextAction: "Proceed.",
	}, "claude-sonnet-4-6")

	first, _, ok := cache.lookup(key, "")
	require.True(t, ok)
	first.WaivedFindings[0].Ruling = "mutated"
	first.Tasks[0].WaivedFindings[0].Ruling = "mutated"

	second, _, ok := cache.lookup(key, "")
	require.True(t, ok)
	assert.Equal(t, "plan ruling", second.WaivedFindings[0].Ruling)
	assert.Equal(t, "task ruling", second.Tasks[0].WaivedFindings[0].Ruling)
}
