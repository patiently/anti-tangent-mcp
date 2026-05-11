package mcpsrv

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
