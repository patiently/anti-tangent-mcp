package mcpsrv

import (
	"context"
	"fmt"
	"strings"
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

func TestValidatePlan_ChecklistDoesNotLiftTheVerdict(t *testing.T) {
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
	planID := verdict.Fingerprint(verdict.CategoryAmbiguousSpec, planScopeKey, "AC")
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

// TestValidatePlan_TaskFindingKeyComesFromThePlanHeading covers a reviewer that
// restates a task's title differently every round. The finding's id, and a
// ruling's waiver of it, follow the plan's heading on a fresh review and on a
// cache hit, while the response keeps the reviewer's own task_title.
func TestValidatePlan_TaskFindingKeyComesFromThePlanHeading(t *testing.T) {
	plan := "# Plan\n\n### Task 1: Add the `parse` helper\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n"
	finding := `{"severity":"major","category":"quality","criterion":"spec","evidence":"thin","suggestion":"s"}`
	id := verdict.Fingerprint(verdict.CategoryQuality, "Add the `parse` helper", "spec")

	rv := &fakeReviewer{name: "openai"}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}
	round := func(reviewerTitle string, args ValidatePlanArgs) verdict.PlanResult {
		t.Helper()
		rv.resp = providers.Response{RawJSON: planJSON("", reviewerTitle, finding), Model: "gpt-5"}
		_, pr, err := h.ValidatePlan(context.Background(), nil, args)
		require.NoError(t, err)
		require.Len(t, pr.Tasks, 1)
		return pr
	}

	first := round("Task 1: Add the parse helper", ValidatePlanArgs{PlanText: plan})
	require.Len(t, first.Tasks[0].Findings, 1)
	assert.Equal(t, id, first.Tasks[0].Findings[0].ID)
	assert.Equal(t, "Task 1: Add the parse helper", first.Tasks[0].TaskTitle, "the response keeps the reviewer's title")

	ruled := ValidatePlanArgs{PlanText: plan, ControllerRulings: []ControllerRulingArg{{FindingID: id, Ruling: "Covered by a later task"}}}
	second := round("Task 1: parse helper", ruled)
	assert.Empty(t, second.Tasks[0].Findings)
	require.Len(t, second.Tasks[0].WaivedFindings, 1)
	assert.Equal(t, id, second.Tasks[0].WaivedFindings[0].ID)

	calls := rv.Calls
	third := round("Task 1: parse helper", ruled)
	require.Equal(t, calls, rv.Calls, "the third call must be a cache hit")
	require.Len(t, third.Tasks[0].WaivedFindings, 1)
	assert.Equal(t, id, third.Tasks[0].WaivedFindings[0].ID)
}

// TestValidatePlan_ChunkedTaskFindingKeyIgnoresAChunkLocalIndex covers a
// chunked review whose second chunk numbers its task 1 instead of 9. The
// chunk's titles were checked against the plan, so the finding is keyed on
// task 9's heading, as a single-call review of the same plan keys it.
func TestValidatePlan_ChunkedTaskFindingKeyIgnoresAChunkLocalIndex(t *testing.T) {
	id := verdict.Fingerprint(verdict.CategoryQuality, "t9", "spec")
	sr := &scriptedReviewer{responses: []providers.Response{
		passOneResp(),
		chunkResp(t, titlesRange(1, 8)),
		{RawJSON: []byte(`{"tasks":[{"task_index":1,"task_title":"Task 9: t9","verdict":"warn","findings":[` +
			`{"severity":"major","category":"quality","criterion":"spec","evidence":"thin","suggestion":"s"}` +
			`],"suggested_header_block":"","suggested_header_reason":""}]}`), Model: "claude-sonnet-4-6"},
	}}
	h := &handlers{deps: newDepsWithScripted(t, sr, 8)}
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          buildPlanWithNTasks(9),
		ControllerRulings: []ControllerRulingArg{{FindingID: id, Ruling: "Covered by a later task"}},
	})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 9)
	assert.Empty(t, pr.Tasks[8].Findings)
	require.Len(t, pr.Tasks[8].WaivedFindings, 1)
	assert.Equal(t, id, pr.Tasks[8].WaivedFindings[0].ID)
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
		assert.Contains(t, prompt, "- f_0123abcd:\n````text\nCovered elsewhere\n````\n", "call %d", i)
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

// TestValidatePlan_AHugeControllerRulingIDIsTruncatedInTheAdvisory covers a
// pathologically long finding_id on a malformed validate_plan ruling: it is
// never a valid display ID, so it draws the malformed-ID advisory, whose
// evidence must not echo the id verbatim.
func TestValidatePlan_AHugeControllerRulingIDIsTruncatedInTheAdvisory(t *testing.T) {
	huge := strings.Repeat("y", 10000)
	pr, _, _ := runPlanWithArgs(t, passPlanResp("go").RawJSON, ValidatePlanArgs{
		PlanText:          buildPlanWithNTasks(1),
		ControllerRulings: []ControllerRulingArg{{FindingID: huge, Ruling: "malformed"}},
	})
	var advisories []verdict.Finding
	for _, f := range pr.PlanFindings {
		if f.Criterion == "controller_rulings" {
			advisories = append(advisories, f)
		}
	}
	require.Len(t, advisories, 1)
	assert.Less(t, len(advisories[0].Evidence), 1000)
	assert.NotContains(t, advisories[0].Evidence, huge)
}

func TestValidatePlan_TheChecklistCannotBeWaived(t *testing.T) {
	raw := planJSON("", "Task 1: t1",
		`{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"cites Foo.kt","suggestion":"verify"}`)
	pr, _, _ := runPlanWithArgs(t, raw, ValidatePlanArgs{
		PlanText: buildPlanWithNTasks(1),
		ControllerRulings: []ControllerRulingArg{{
			FindingID: verdict.Fingerprint(verdict.CategoryUnverifiableCodebaseClaim, planScopeKey, "codebase_reference_checklist"),
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
		ControllerRulings: []ControllerRulingArg{{FindingID: verdict.Fingerprint(verdict.CategoryAmbiguousSpec, planScopeKey, "AC"), Ruling: "Intended"}},
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

// The deterministic Create/Modify finding is a plan-level finding like any
// other: a ruling on its id waives it. The id is a fingerprint of category,
// scope and criterion, not evidence, so the id one round reports still
// matches after the plan is edited and the evidence changes.
func TestValidatePlan_RulingWaivesTheFileConsistencyFinding(t *testing.T) {
	plan := func(file string) string {
		return "# Plan\n\n" +
			"### Task 1: t1\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Modify: `" + file + "`\n\n" +
			"### Task 2: t2\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Create: `" + file + "`\n\n"
	}
	id := verdict.Fingerprint(verdict.CategoryOther, planScopeKey, "task_order_contradiction")

	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 2), singlePlanResp(t, 2)}}
	h := &handlers{deps: newDepsWithScripted(t, sr, 8)}

	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan("a.go")})
	require.NoError(t, err)
	var reported bool
	for _, f := range first.PlanFindings {
		if f.Criterion == "task_order_contradiction" {
			reported = true
			assert.Equal(t, id, f.ID)
		}
	}
	require.True(t, reported, "round 1 must report the ordering violation")

	_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          plan("b.go"),
		ControllerRulings: []ControllerRulingArg{{FindingID: id, Ruling: "Ordered on purpose"}},
	})
	require.NoError(t, err)
	assert.False(t, hasCriterion(second.PlanFindings, "task_order_contradiction"))
	require.Len(t, second.WaivedFindings, 1)
	w := second.WaivedFindings[0]
	assert.Equal(t, id, w.ID)
	assert.Equal(t, "Ordered on purpose", w.Ruling)
	assert.Contains(t, w.Evidence, "`b.go`")
	assert.Equal(t, verdict.VerdictPass, second.PlanVerdict)
	assert.Contains(t, second.SummaryBlock, fmt.Sprintf("waived: %s", id))
}
