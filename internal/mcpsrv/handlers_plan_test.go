package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// ---------------------------------------------------------------------------
// Boundary happy paths
// ---------------------------------------------------------------------------

// TestReviewPlanChunked_9Tasks_2Chunks verifies that a 9-task plan with
// chunkSize=8 produces exactly 3 reviewer calls: Pass1 + chunk(8) + chunk(1).
// It also checks that the merged result contains 9 tasks in input order.
func TestReviewPlanChunked_9Tasks_2Chunks(t *testing.T) {
	plan := buildPlanWithNTasks(9)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),                   // call 1: Pass1
			chunkResp(t, titlesRange(1, 8)), // call 2: tasks 1-8
			chunkResp(t, titlesRange(9, 9)), // call 3: task 9
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, 3, sr.calls, "Pass1 + 2 chunks = 3 calls")
	require.Len(t, pr.Tasks, 9)
	// Verify order: tasks should appear in input order (Task 1 through 9).
	for i, task := range pr.Tasks {
		expected := titlesRange(i+1, i+1)[0]
		assert.Equal(t, expected, task.TaskTitle, "task[%d] title mismatch", i)
	}
}

// TestReviewPlanChunked_16Tasks_2Chunks verifies that a 16-task plan with
// chunkSize=8 produces exactly 3 reviewer calls: Pass1 + chunk(8) + chunk(8).
func TestReviewPlanChunked_16Tasks_2Chunks(t *testing.T) {
	plan := buildPlanWithNTasks(16)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),
			chunkResp(t, titlesRange(1, 8)),
			chunkResp(t, titlesRange(9, 16)),
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, 3, sr.calls, "Pass1 + 2 chunks = 3 calls")
	require.Len(t, pr.Tasks, 16)
}

// TestReviewPlanChunked_17Tasks_3Chunks verifies that a 17-task plan with
// chunkSize=8 produces exactly 4 reviewer calls: Pass1 + chunk(8) + chunk(8) + chunk(1).
func TestReviewPlanChunked_17Tasks_3Chunks(t *testing.T) {
	plan := buildPlanWithNTasks(17)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),
			chunkResp(t, titlesRange(1, 8)),
			chunkResp(t, titlesRange(9, 16)),
			chunkResp(t, titlesRange(17, 17)),
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, 4, sr.calls, "Pass1 + 3 chunks = 4 calls")
	require.Len(t, pr.Tasks, 17)
}

// TestReviewPlanChunked_25Tasks_4Chunks verifies that a 25-task plan with
// chunkSize=8 produces exactly 5 reviewer calls: Pass1 + chunk(8)*3 + chunk(1).
func TestReviewPlanChunked_25Tasks_4Chunks(t *testing.T) {
	plan := buildPlanWithNTasks(25)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),
			chunkResp(t, titlesRange(1, 8)),
			chunkResp(t, titlesRange(9, 16)),
			chunkResp(t, titlesRange(17, 24)),
			chunkResp(t, titlesRange(25, 25)),
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, 5, sr.calls, "Pass1 + 4 chunks = 5 calls")
	require.Len(t, pr.Tasks, 25)
}

// ---------------------------------------------------------------------------
// Error propagation
// ---------------------------------------------------------------------------

// TestReviewPlanChunked_MidStreamError verifies that when a chunk call returns
// a network-like error on both attempts (first + retry), ValidatePlan returns an
// error containing "plan_tasks_chunk failed after retry" and stops making calls.
// Uses a 17-task plan: Pass1 ok, chunk1 ok, chunk2 errors on both attempts.
func TestReviewPlanChunked_MidStreamError(t *testing.T) {
	plan := buildPlanWithNTasks(17)
	networkErr := errors.New("connection reset by peer")
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),                   // call 0: Pass1 — ok
			chunkResp(t, titlesRange(1, 8)), // call 1: chunk1 — ok
			{},                              // call 2: chunk2 first attempt — error via errors[2]
			{},                              // call 3: chunk2 retry — error via errors[3]
		},
		errors: []error{
			nil,        // call 0: Pass1 — ok
			nil,        // call 1: chunk1 — ok
			networkErr, // call 2: chunk2 first — network error
			networkErr, // call 3: chunk2 retry — network error again
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan_tasks_chunk failed after retry",
		"error should mention the retry exhaustion")
	// chunk3 (task 17) is never reached.
	assert.Equal(t, 4, sr.calls, "Pass1 + chunk1 + chunk2-fail + chunk2-retry = 4 calls")
}

// ---------------------------------------------------------------------------
// Identity validation: retry then fail
// ---------------------------------------------------------------------------

// TestReviewPlanChunked_IdentityMismatch_RetriesThenFails verifies that when
// a chunk response contains a hallucinated title on both the first attempt and
// the retry, ValidatePlan returns an error containing "plan_tasks_chunk failed
// after retry". Uses a 9-task plan so only 2 chunks: the first chunk passes and
// the second chunk always hallucinate.
func TestReviewPlanChunked_IdentityMismatch_RetriesThenFails(t *testing.T) {
	plan := buildPlanWithNTasks(9)

	// chunk2 (task 9) returns a hallucinated title both times.
	hallucinatedResp := func() providers.Response {
		return chunkResp(t, []string{"Task 42: hallucinated"})
	}

	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),                   // call 0: Pass1
			chunkResp(t, titlesRange(1, 8)), // call 1: chunk1 ok
			hallucinatedResp(),              // call 2: chunk2 first attempt — bad title
			hallucinatedResp(),              // call 3: chunk2 retry — still bad
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan_tasks_chunk failed after retry",
		"error should mention the retry failure")
	assert.Equal(t, 4, sr.calls, "Pass1 + chunk1 + chunk2-fail + chunk2-retry = 4 calls")
}

// ---------------------------------------------------------------------------
// Identity validation: retry then succeed
// ---------------------------------------------------------------------------

// TestReviewPlanChunked_IdentityMismatch_RetrySucceeds verifies that when chunk2
// first response contains a wrong title but the retry is correct, ValidatePlan
// succeeds. For a 9-task plan: Pass1 + chunk1 + chunk2-fail + chunk2-retry = 4 calls.
func TestReviewPlanChunked_IdentityMismatch_RetrySucceeds(t *testing.T) {
	plan := buildPlanWithNTasks(9)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),                                   // call 0: Pass1
			chunkResp(t, titlesRange(1, 8)),                 // call 1: chunk1 ok
			chunkResp(t, []string{"Task 42: hallucinated"}), // call 2: chunk2 first — bad
			chunkResp(t, titlesRange(9, 9)),                 // call 3: chunk2 retry — correct
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 9)
	assert.Equal(t, 4, sr.calls, "Pass1 + chunk1 + chunk2-fail + chunk2-retry = 4 calls")
	// Verify correct title for the last task after retry.
	assert.Equal(t, "Task 9: t9", pr.Tasks[8].TaskTitle)
}

// ---------------------------------------------------------------------------
// Wrong count: retry then succeed
// ---------------------------------------------------------------------------

// TestReviewPlanChunked_WrongCount_TriggersRetry verifies that when chunk1's
// first response returns 7 tasks instead of 8, the retry fires and returns the
// correct 8. Then chunk2 (task 9) succeeds on first try.
// Expected calls: Pass1 + chunk1-fail(7) + chunk1-retry(8) + chunk2(1) = 4 calls.
func TestReviewPlanChunked_WrongCount_TriggersRetry(t *testing.T) {
	plan := buildPlanWithNTasks(9)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),                   // call 0: Pass1
			chunkResp(t, titlesRange(1, 7)), // call 1: chunk1 first — only 7 tasks
			chunkResp(t, titlesRange(1, 8)), // call 2: chunk1 retry — correct 8
			chunkResp(t, titlesRange(9, 9)), // call 3: chunk2 ok
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 9)
	assert.Equal(t, 4, sr.calls, "Pass1 + chunk1-fail + chunk1-retry + chunk2 = 4 calls")
}

// ---------------------------------------------------------------------------
// Duplicate title detection
// ---------------------------------------------------------------------------

// TestReviewPlanChunked_DuplicateTitleInChunk_TriggersRetry covers the T7 bug
// fix: duplicate-title detection in validateChunkIdentity. chunk1 first response
// contains Task 1 duplicated and Task 7 dropped (count=8, but title set wrong).
// The retry returns correct titles. chunk2 (task 9) passes on first try.
// Expected calls: Pass1 + chunk1-fail(dup) + chunk1-retry(ok) + chunk2(ok) = 4 calls.
func TestReviewPlanChunked_DuplicateTitleInChunk_TriggersRetry(t *testing.T) {
	plan := buildPlanWithNTasks(9)

	// First response for chunk1: Task 1 is duplicated, Task 7 is missing.
	// Count=8 (passes count check) but duplicate triggers the identity error.
	dupTitles := []string{
		"Task 1: t1",
		"Task 1: t1", // duplicate!
		"Task 2: t2",
		"Task 3: t3",
		"Task 4: t4",
		"Task 5: t5",
		"Task 6: t6",
		"Task 8: t8", // Task 7 dropped, Task 8 is present — but Task 1 dup means identity fails
	}

	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),                   // call 0: Pass1
			chunkResp(t, dupTitles),         // call 1: chunk1 first — dup Task 1
			chunkResp(t, titlesRange(1, 8)), // call 2: chunk1 retry — correct
			chunkResp(t, titlesRange(9, 9)), // call 3: chunk2 ok
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 9)
	assert.Equal(t, 4, sr.calls, "Pass1 + chunk1-dup-fail + chunk1-retry + chunk2 = 4 calls")
	// Verify all expected titles present and ordered after merge.
	for i, task := range pr.Tasks {
		assert.Equal(t, titlesRange(i+1, i+1)[0], task.TaskTitle, "task[%d] title", i)
	}
}

// ---------------------------------------------------------------------------
// Post-merge count guard (positive path)
// ---------------------------------------------------------------------------

// TestReviewPlanChunked_PostMergeCount_NoErrorWhenCountsMatch verifies the
// happy path of the post-merge count guard: when each chunk individually
// passes identity validation AND the aggregated task count equals the
// original plan's task count, the guard does NOT fire and ValidatePlan
// returns the merged result. The guard's error path is a safety net
// reachable only if per-chunk validation passes but the merge is somehow
// wrong — given the positional identity check, the error path is
// effectively unreachable from real reviewer responses. This test pins the
// positive contract.
func TestReviewPlanChunked_PostMergeCount_NoErrorWhenCountsMatch(t *testing.T) {
	// We verify the post-merge guard by forcing a scenario where a chunk returns
	// fewer tasks than expected AND the retry also returns fewer (wrong count after
	// retry causes reviewOnePlanChunk to fail). We set chunkSize=1 and have a 2-task
	// plan. chunk1 response returns 0 tasks — ParseTasksOnly rejects empty, so that
	// triggers the retry path. chunk1-retry also returns 0 tasks → reviewOnePlanChunk
	// returns error → ValidatePlan propagates the error without ever reaching the
	// post-merge count guard. This shows the path leading to any error stops early.
	// For the post-merge count guard specifically, we construct it differently:
	// use chunkSize=2, plan has 3 tasks. chunk1 covers tasks 1-2 (passes), chunk2
	// covers task 3 (passes). The guard is satisfied → no error. The test is a
	// positive assertion that the guard does NOT fire when counts match.
	plan := buildPlanWithNTasks(3)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),
			chunkResp(t, titlesRange(1, 2)),
			chunkResp(t, titlesRange(3, 3)),
		},
	}
	d := newDepsWithScripted(t, sr, 2)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 3)
	assert.Equal(t, 3, sr.calls, "Pass1 + 2 chunks = 3 calls")
}

// ---------------------------------------------------------------------------
// plan_quality threading through the chunked path
// ---------------------------------------------------------------------------

// TestReviewPlanChunked_ThreadsPlanQuality verifies that the plan_quality
// value emitted by Pass-1 (PlanFindingsOnly) is threaded into the assembled
// PlanResult by reviewPlanChunked. Pass-2 chunk responses do NOT emit
// plan_quality (TasksOnly doesn't carry it), so the field must come from
// Pass-1 unchanged.
//
// We use a warn verdict with no critical findings and an "actionable"
// plan_quality so the sanity check (when later wired into the marshaller
// in Task 5) would leave the value untouched — the assertion holds
// regardless of whether the sanity helper has been wired into the
// envelope yet.
func TestReviewPlanChunked_ThreadsPlanQuality(t *testing.T) {
	plan := buildPlanWithNTasks(9)
	passOneWithQuality := providers.Response{
		RawJSON: []byte(`{
			"plan_verdict":"warn",
			"plan_findings":[],
			"next_action":"Address findings before dispatch.",
			"plan_quality":"actionable"
		}`),
		Model:        "claude-sonnet-4-6",
		InputTokens:  10,
		OutputTokens: 5,
	}
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneWithQuality,
			chunkResp(t, titlesRange(1, 8)),
			chunkResp(t, titlesRange(9, 9)),
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, 3, sr.calls, "Pass1 + 2 chunks = 3 calls")
	require.Len(t, pr.Tasks, 9)
	assert.Equal(t, verdict.PlanQualityActionable, pr.PlanQuality,
		"plan_quality from Pass-1 should thread into the assembled PlanResult")
}

// ---------------------------------------------------------------------------
// reviewPlanSingle retry path
// ---------------------------------------------------------------------------

// TestValidatePlan_SingleCall_RetryOnParseFailure exercises the single-call
// path's schema-retry-once behavior: first reviewer response is malformed
// JSON, retry response is valid → ValidatePlan succeeds with two reviewer
// calls. Symmetric to the chunked-path retry tests above, closes the gap
// in coverage for reviewPlanSingle's matching retry path.
func TestValidatePlan_SingleCall_RetryOnParseFailure(t *testing.T) {
	// 3 tasks ≤ default chunkSize=8 → single-call path.
	plan := buildPlanWithNTasks(3)
	validSingleCallResp := providers.Response{
		RawJSON: []byte(`{
			"plan_verdict":"pass",
			"plan_findings":[],
			"tasks":[
				{"task_index":1,"task_title":"Task 1: t1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},
				{"task_index":2,"task_title":"Task 2: t2","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},
				{"task_index":3,"task_title":"Task 3: t3","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}
			],
			"next_action":"Proceed with implementation."
		}`),
		Model: "test-model",
	}
	sr := &scriptedReviewer{
		responses: []providers.Response{
			{RawJSON: []byte(`not json at all`), Model: "test-model"}, // first attempt fails ParsePlan
			validSingleCallResp, // retry succeeds
		},
	}
	// chunkSize doesn't matter here since len(tasks)=3 ≤ default 8 forces single-call;
	// use 8 (the default) to keep the test obvious.
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 3)
	assert.Equal(t, 2, sr.calls, "first call fails parse, retry succeeds = 2 calls total")
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
}

// ---------------------------------------------------------------------------
// validateChunkIdentity: title normalization
// ---------------------------------------------------------------------------

func TestValidateChunkIdentity_PrefixStripped(t *testing.T) {
	parsed := verdict.TasksOnly{Tasks: []verdict.PlanTaskResult{
		{TaskTitle: "Add final diff"},
		{TaskTitle: "Surface TTL"},
	}}
	chunkTasks := []planparser.RawTask{
		{Title: "Task 1: Add final diff"},
		{Title: "Task 2: Surface TTL"},
	}

	require.NoError(t, validateChunkIdentity(parsed, chunkTasks))
}

func TestValidateChunkIdentity_WrongTitleAfterNormalization(t *testing.T) {
	parsed := verdict.TasksOnly{Tasks: []verdict.PlanTaskResult{{TaskTitle: "Wrong title"}}}
	chunkTasks := []planparser.RawTask{{Title: "Task 1: Right title"}}

	err := validateChunkIdentity(parsed, chunkTasks)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"Wrong title"`)
	assert.Contains(t, err.Error(), `"Task 1: Right title"`)
}

// TestValidateChunkIdentity_AllowsLegitimateDuplicateNormalizedTitles verifies
// that a plan with two tasks whose titles legitimately normalize to the same
// string (e.g. "Add tests" for two different tasks) is accepted when the
// reviewer correctly echoes both titles in order.
func TestValidateChunkIdentity_AllowsLegitimateDuplicateNormalizedTitles(t *testing.T) {
	parsed := verdict.TasksOnly{Tasks: []verdict.PlanTaskResult{
		{TaskTitle: "Task 1: Add tests"},
		{TaskTitle: "Task 2: Add tests"},
	}}
	chunkTasks := []planparser.RawTask{
		{Title: "Task 1: Add tests"},
		{Title: "Task 2: Add tests"},
	}

	require.NoError(t, validateChunkIdentity(parsed, chunkTasks))
}

// TestValidateChunkIdentity_ReviewerReturnsDuplicateForDistinctExpected verifies
// that when the reviewer echoes the same title twice but the expected titles are
// distinct (per-position mismatch fires), an error is returned. This replaces the
// old DuplicateAfterNormalization test whose premise was the bug now fixed above.
func TestValidateChunkIdentity_ReviewerReturnsDuplicateForDistinctExpected(t *testing.T) {
	// Reviewer returns "Same" for both positions, but position 1's expected
	// title normalizes to "Other". The per-position check fires at position 1.
	parsed := verdict.TasksOnly{Tasks: []verdict.PlanTaskResult{
		{TaskTitle: "Task 1: Same"},
		{TaskTitle: "Same"}, // reviewer echoed "Same" again instead of "Other"
	}}
	chunkTasks := []planparser.RawTask{
		{Title: "Task 1: Same"},
		{Title: "Task 2: Other"},
	}

	err := validateChunkIdentity(parsed, chunkTasks)
	require.Error(t, err)
	// Per-position mismatch: got "Same", expected "Task 2: Other".
	assert.Contains(t, err.Error(), "expected")
	assert.Contains(t, err.Error(), `"Task 2: Other"`)
}

// TestValidatePlan_PartialFindingsRecoveredOnTruncation verifies the
// plan-level partial-recovery branch: when reviewPlanSingle yields a
// truncated response with two complete tasks and a third task cut mid-find,
// ValidatePlan recovers the two cleanly-closed tasks plus the original
// plan-level finding, appends a minor truncation marker, and sets Partial=true.
func TestValidatePlan_PartialFindingsRecoveredOnTruncation(t *testing.T) {
	// Two complete tasks; truncation hits in the third.
	rawJSON := []byte(`{"plan_verdict":"warn","plan_findings":[` +
		`{"severity":"major","category":"other","criterion":"pf1","evidence":"e","suggestion":"s"}` +
		`],"tasks":[` +
		`{"task_index":1,"task_title":"Task 1: First","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2,"task_title":"Task 2: Second","verdict":"warn","findings":[{"severity":"minor","category":"other","criterion":"tf1","evidence":"e","suggestion":"s"}],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":3,"task_title":"Task 3: Third","verdict":"warn","find`)

	rv := &fakeReviewer{
		name: "openai",
		resp: providers.Response{RawJSON: rawJSON, Model: "gpt-5"},
		err:  providers.ErrResponseTruncated,
	}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: First\n\nbody.\n\n### Task 2: Second\n\nbody.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.True(t, pr.Partial)
	require.Len(t, pr.Tasks, 2)
	assert.Equal(t, "Task 1: First", pr.Tasks[0].TaskTitle)
	assert.Equal(t, "Task 2: Second", pr.Tasks[1].TaskTitle)
	// plan_findings has the original major finding plus the minor truncation marker.
	require.Len(t, pr.PlanFindings, 2)
	assert.Equal(t, "pf1", pr.PlanFindings[0].Criterion)
	assert.Equal(t, verdict.SeverityMinor, pr.PlanFindings[1].Severity)
	assert.Contains(t, pr.PlanFindings[1].Suggestion, "max_tokens_override")
}

// TestReviewPlanChunked_Pass2Truncation_PreservesPass1Findings exercises the
// chunked path with a Pass-2 chunk truncation and verifies that the Pass-1
// plan_findings AND the cleanly-closed chunk task results from earlier chunks
// are BOTH preserved in the final envelope, alongside whatever the parser can
// recover from the truncating chunk's partial bytes. Prior to the fix in
// reviewPlanChunked + recoverPartialPlanFindings, the Pass-1 findings and
// earlier-chunk tasks were silently dropped because reviewPlanChunked returned
// a zero PlanResult on Pass-2 truncation.
func TestReviewPlanChunked_Pass2Truncation_PreservesPass1Findings(t *testing.T) {
	// 9-task plan with chunkSize=8 → Pass1 + chunk1(8) + chunk2(1).
	// chunk2 truncates mid-response.
	plan := buildPlanWithNTasks(9)

	// Pass 1 returns a plan-level major finding we must preserve.
	pass1 := providers.Response{
		RawJSON: []byte(`{"plan_verdict":"warn","plan_findings":[` +
			`{"severity":"major","category":"other","criterion":"pass1_pf","evidence":"e","suggestion":"s"}` +
			`],"next_action":"address pass1_pf."}`),
		Model: "claude-sonnet-4-6",
	}
	// chunk1 returns complete results for tasks 1-8.
	chunk1 := chunkResp(t, titlesRange(1, 8))
	// chunk2 truncates: emit one well-formed task result then cut off.
	chunk2Partial := providers.Response{
		RawJSON: []byte(`{"tasks":[` +
			`{"task_index":9,"task_title":"Task 9: t9","verdict":"warn","findings":[` +
			`{"severity":"minor","category":"other","criterion":"recovered","evidence":"e","suggestion":"s"}` +
			`],"suggested_header_block":"","suggested_header_reason":""},` +
			`{"task_index":10,"task_title":"cut","verdict":"warn","find`),
		Model: "claude-sonnet-4-6",
	}

	sr := &scriptedReviewer{
		responses: []providers.Response{
			pass1,         // call 0: Pass1 — ok
			chunk1,        // call 1: chunk1 — ok
			chunk2Partial, // call 2: chunk2 — truncation
		},
		errors: []error{
			nil,
			nil,
			providers.ErrResponseTruncated,
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.True(t, pr.Partial, "envelope must be marked partial after Pass-2 truncation")

	// Pass-1 plan finding must survive the truncation recovery.
	require.GreaterOrEqual(t, len(pr.PlanFindings), 2,
		"expected at least Pass-1 finding + truncation marker; got %d", len(pr.PlanFindings))
	assert.Equal(t, "pass1_pf", pr.PlanFindings[0].Criterion,
		"Pass-1 plan finding must be the first PlanFinding")
	// Last finding must be the minor truncation marker.
	last := pr.PlanFindings[len(pr.PlanFindings)-1]
	assert.Equal(t, verdict.SeverityMinor, last.Severity)
	assert.Equal(t, "reviewer_response", last.Criterion)
	assert.Contains(t, last.Suggestion, "max_tokens_override")

	// Chunk1's 8 complete tasks must be preserved. The partial parser may or
	// may not recover task 9; we assert the floor (>= 8) and that task 1's
	// title is intact in position 0.
	require.GreaterOrEqual(t, len(pr.Tasks), 8,
		"expected at least 8 cleanly-closed chunk1 tasks preserved; got %d", len(pr.Tasks))
	assert.Equal(t, "Task 1: t1", pr.Tasks[0].TaskTitle,
		"Pass-2 chunk1 task results must lead the merged tasks list")

	// NextAction must mention max_tokens_override (mitigation hint contract).
	assert.Contains(t, pr.NextAction, "max_tokens_override")
}

// ---------------------------------------------------------------------------
// Task 3 — unverifiable rollup + verdict calibration
// ---------------------------------------------------------------------------

func TestValidatePlan_RollsUpTaskUnverifiableFindings(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"warn",
		"plan_quality":"actionable",
		"plan_findings":[],
		"tasks":[
			{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt:10 and Foo.bar","suggestion":"verify against the actual code before dispatching"}],"suggested_header_block":"","suggested_header_reason":""},
			{"task_index":2,"task_title":"Task 2: two","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 2 cites Baz.qux","suggestion":"verify against the actual code before dispatching"}],"suggested_header_block":"","suggested_header_reason":""}
		],
		"next_action":"Verify codebase claims."
	}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(2)})
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, verdict.CategoryUnverifiableCodebaseClaim, pr.PlanFindings[0].Category)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[0].Criterion)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 1")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Foo.kt:10")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 2")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Baz.qux")
	assert.Empty(t, pr.Tasks[0].Findings)
	assert.Empty(t, pr.Tasks[1].Findings)
}

func TestValidatePlan_UnverifiableOnlyCalibratesToPass(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Verify codebase claims."}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.Equal(t, verdict.PlanQualityActionable, pr.PlanQuality)
	assert.Contains(t, pr.NextAction, "No blocking plan-quality findings")
}

// TestValidatePlan_UnverifiableOnly_PreservesRigorousQuality covers the
// rigorous-preservation branch in calibratePlanVerdictForUnverifiableOnly:
// when the reviewer already emitted plan_quality:"rigorous", calibration
// must NOT downgrade it to actionable — even though the verdict still
// force-passes. Spec section 4 calls this out explicitly.
func TestValidatePlan_UnverifiableOnly_PreservesRigorousQuality(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"rigorous","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Verify codebase claims."}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.Equal(t, verdict.PlanQualityRigorous, pr.PlanQuality,
		"rigorous plan_quality from reviewer must survive unverifiable-only calibration")
}

func TestValidatePlan_MixedFindingsDoNotCalibrateToPass(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"AC is vague","suggestion":"rewrite"},{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Rewrite AC."}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictWarn, pr.PlanVerdict)
	require.Len(t, pr.Tasks, 1)
	require.Len(t, pr.Tasks[0].Findings, 1)
	assert.Equal(t, verdict.CategoryAmbiguousSpec, pr.Tasks[0].Findings[0].Category)
}

func TestValidatePlan_PreservesPlanLevelUnverifiableBesideTaskRollup(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"warn",
		"plan_quality":"actionable",
		"plan_findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"plan","evidence":"Plan-level claim cites package ownership","suggestion":"verify"}],
		"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],
		"next_action":"Verify codebase claims."
	}`)
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)})
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 2)
	assert.Equal(t, "plan", pr.PlanFindings[0].Criterion)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[1].Criterion)
	assert.Contains(t, pr.PlanFindings[1].Evidence, "Task 1")
}

func TestValidatePlan_ChunkedUnverifiableFindingsRollUp(t *testing.T) {
	chunkWithFinding := func(titles []string, findingPosition int, evidence string) providers.Response {
		t.Helper()
		type item struct {
			TaskIndex             int               `json:"task_index"`
			TaskTitle             string            `json:"task_title"`
			Verdict               string            `json:"verdict"`
			Findings              []verdict.Finding `json:"findings"`
			SuggestedHeaderBlock  string            `json:"suggested_header_block"`
			SuggestedHeaderReason string            `json:"suggested_header_reason"`
		}
		items := make([]item, 0, len(titles))
		for i, title := range titles {
			findings := []verdict.Finding{}
			if i == findingPosition {
				findings = []verdict.Finding{{
					Severity:   verdict.SeverityMinor,
					Category:   verdict.CategoryUnverifiableCodebaseClaim,
					Criterion:  "spec",
					Evidence:   evidence,
					Suggestion: "verify",
				}}
			}
			items = append(items, item{TaskIndex: i + 1, TaskTitle: title, Verdict: "warn", Findings: findings})
		}
		raw, err := json.Marshal(struct {
			Tasks []item `json:"tasks"`
		}{items})
		require.NoError(t, err)
		return providers.Response{RawJSON: raw, Model: "claude-sonnet-4-6"}
	}

	rv := &scriptedReviewer{responses: []providers.Response{
		passOneResp(),
		chunkWithFinding(titlesRange(1, 8), 0, "Task 1 cites Foo.kt"),
		chunkWithFinding(titlesRange(9, 9), 0, "Task 9 cites Baz.kt"),
	}}
	d := newDepsWithScripted(t, rv, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(9)})
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[0].Criterion)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 1")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 9")
}
