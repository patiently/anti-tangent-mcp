package mcpsrv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
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

// TestReviewPlanChunked_CachePrefixWiring pins the wiring between
// prompts.Output.UserPrefix and providers.Request.CachePrefix at the handler
// level, where nothing else in the default suite asserts it. Without this,
// a regression such as `User: rendered.User` on a chunk request (sending the
// prefix duplicated into the body instead of the suffix alone) — or
// resurrecting a CachePrefix on the Pass-1 request — would pass every other
// test, which is exactly how the bug this fix wave addresses reached final
// review.
func TestReviewPlanChunked_CachePrefixWiring(t *testing.T) {
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

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, sr.requests, 3, "Pass1 + 2 chunks = 3 requests")

	pass1Req, chunk1Req, chunk2Req := sr.requests[0], sr.requests[1], sr.requests[2]

	assert.Empty(t, pass1Req.CachePrefix,
		"Pass 1 must send an EMPTY CachePrefix: its tools block (PlanFindingsOnlySchema) "+
			"differs from the chunk calls' (TasksOnlySchema), so a cache entry it wrote "+
			"could never be read back — a breakpoint there is pure write premium")

	require.NotEmpty(t, chunk1Req.CachePrefix, "chunk 1's request must carry a non-empty CachePrefix")
	require.NotEmpty(t, chunk2Req.CachePrefix, "chunk 2's request must carry a non-empty CachePrefix")
	assert.Equal(t, chunk1Req.CachePrefix, chunk2Req.CachePrefix,
		"all chunk requests must share the identical CachePrefix value so Anthropic can match the cache entry across chunks")

	// Independently re-render what reviewPlanChunked itself would have
	// rendered for this plan, and check the captured requests against it
	// directly rather than against each other only.
	tasks, _ := planparser.SplitTasks(plan)
	require.Len(t, tasks, 9)
	rendered, err := renderPlanReview(renderPlanReviewInputs{
		PlanText:  plan,
		Tasks:     tasks,
		ChunkSize: 8,
	})
	require.NoError(t, err)
	require.Len(t, rendered.Chunks, 2, "9 tasks at chunk size 8 renders 2 chunks")
	wantPrefix := rendered.Chunks[0].Prompt.UserPrefix
	require.NotEmpty(t, wantPrefix)
	assert.Equal(t, wantPrefix, chunk1Req.CachePrefix, "chunk 1's CachePrefix must equal the rendered UserPrefix")
	assert.Equal(t, wantPrefix, chunk2Req.CachePrefix, "chunk 2's CachePrefix must equal the rendered UserPrefix")

	// Each chunk's User must be the SUFFIX only. A regression that instead
	// sends the full rendered.User (prefix+suffix) would still "work"
	// functionally, but would duplicate the prefix into the body and the
	// cache would never match — so assert the negative directly.
	assert.Equal(t, rendered.Chunks[0].Prompt.UserSuffix, chunk1Req.User, "chunk 1's User must be exactly the rendered suffix")
	assert.Equal(t, rendered.Chunks[1].Prompt.UserSuffix, chunk2Req.User, "chunk 2's User must be exactly the rendered suffix")
	assert.False(t, strings.HasPrefix(chunk1Req.User, chunk1Req.CachePrefix),
		"chunk 1's User must NOT start with its own CachePrefix — that would mean the full body was sent instead of the suffix")
	assert.False(t, strings.HasPrefix(chunk2Req.User, chunk2Req.CachePrefix),
		"chunk 2's User must NOT start with its own CachePrefix — that would mean the full body was sent instead of the suffix")
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
	// plan_findings has the plan_text deprecation notice (leading — see
	// prependPlanDeprecation), the original major finding, and the minor
	// truncation marker.
	require.Len(t, pr.PlanFindings, 3)
	assert.Equal(t, "input", pr.PlanFindings[0].Criterion, "plan_text deprecation notice must lead")
	assert.Equal(t, "pf1", pr.PlanFindings[1].Criterion)
	assert.Equal(t, verdict.SeverityMinor, pr.PlanFindings[2].Severity)
	assert.Contains(t, pr.PlanFindings[2].Suggestion, "max_tokens_override")
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

	// Pass-1 plan finding must survive the truncation recovery. PlanFindings[0]
	// is the plan_text deprecation notice (this test uses PlanText), which
	// must also survive the truncation-recovery path — see
	// TestValidatePlan_TruncatedResponseSurfacesWarn.
	require.GreaterOrEqual(t, len(pr.PlanFindings), 3,
		"expected at least deprecation notice + Pass-1 finding + truncation marker; got %d", len(pr.PlanFindings))
	assert.Equal(t, "input", pr.PlanFindings[0].Criterion, "plan_text deprecation notice must lead")
	assert.Equal(t, "pass1_pf", pr.PlanFindings[1].Criterion,
		"Pass-1 plan finding must be the second PlanFinding, right after the deprecation notice")
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

// runValidatePlanWithReviewerJSON drives ValidatePlan against a fakeReviewer
// that returns the supplied raw JSON. Returns the resulting PlanResult and any
// error, so callers can focus on their AC-specific assertions. taskCount sets
// the number of `### Task N:` headings in the synthetic plan text — it must
// match the task_index values the reviewer JSON references.
func runValidatePlanWithReviewerJSON(t *testing.T, raw []byte, taskCount int) (verdict.PlanResult, error) {
	t.Helper()
	rv := &fakeReviewer{name: "openai", resp: providers.Response{RawJSON: raw, Model: "gpt-5"}}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: buildPlanWithNTasks(taskCount),
	})
	// Every caller of this helper uses plan_text and predates the plan_path
	// deprecation notice; strip it here rather than rework each caller's
	// unrelated rollup/calibration assertions to account for an extra,
	// orthogonal finding. See TestValidatePlanPathInput for deprecation
	// coverage.
	pr.PlanFindings = stripPlanDeprecationFinding(pr.PlanFindings)
	return pr, err
}

// stripPlanDeprecationFinding removes the plan_text deprecation notice (see
// prependPlanDeprecation) from a findings slice, if present.
func stripPlanDeprecationFinding(findings []verdict.Finding) []verdict.Finding {
	out := make([]verdict.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Category == verdict.CategoryOther && f.Criterion == "input" {
			continue
		}
		out = append(out, f)
	}
	return out
}

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
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 2)
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
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 1)
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
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 1)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.Equal(t, verdict.PlanQualityRigorous, pr.PlanQuality,
		"rigorous plan_quality from reviewer must survive unverifiable-only calibration")
}

func TestValidatePlan_MixedFindings_PlanLevelDerivesPassTaskLevelDerivesWarn(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"AC is vague","suggestion":"rewrite"},{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Rewrite AC."}`)
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 1)
	require.NoError(t, err)
	// Task-level ladder (via FinalizePlanVerdict, v0.5.2): the major
	// ambiguous_spec finding survives rollup → task verdict derives to warn.
	require.Len(t, pr.Tasks, 1)
	require.Len(t, pr.Tasks[0].Findings, 1)
	assert.Equal(t, verdict.CategoryAmbiguousSpec, pr.Tasks[0].Findings[0].Category)
	assert.Equal(t, verdict.VerdictWarn, pr.Tasks[0].Verdict, "task ladder derives warn from one major finding")
	// Plan-level: the rolled-up codebase_reference_checklist (1 minor) drives
	// the ladder to pass. Plan-level verdict is derived from PlanFindings only;
	// task-level severity does NOT propagate up to the plan verdict.
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict, "plan ladder derives pass from one minor plan-level finding")
}

func TestValidatePlan_PreservesPlanLevelUnverifiableBesideTaskRollup(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"warn",
		"plan_quality":"actionable",
		"plan_findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"plan","evidence":"Plan-level claim cites package ownership","suggestion":"verify"}],
		"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Task 1 cites Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}],
		"next_action":"Verify codebase claims."
	}`)
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 1)
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
	pr.PlanFindings = stripPlanDeprecationFinding(pr.PlanFindings)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[0].Criterion)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 1")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 9")
}

// TestValidatePlan_MultipleUnverifiableUnderSameTaskJoinedWithSemicolon
// pins the spec §3 "one compact line per affected task" wording: when a
// single task emits more than one unverifiable_codebase_claim, the rollup
// must list "Task N: ..." once with intra-task evidence joined by "; ",
// not duplicate the Task N: prefix per finding.
func TestValidatePlan_MultipleUnverifiableUnderSameTaskJoinedWithSemicolon(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"warn",
		"plan_quality":"actionable",
		"plan_findings":[],
		"tasks":[
			{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Foo.kt:10","suggestion":"verify"},
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Bar.kt:20","suggestion":"verify"}
			],"suggested_header_block":"","suggested_header_reason":""}
		],
		"next_action":"Verify."
	}`)
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 1)
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, 1, strings.Count(pr.PlanFindings[0].Evidence, "Task 1:"))
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Foo.kt:10; Bar.kt:20")
}

// TestValidatePlan_EmptyFindings_LadderDerivesPass locks in the
// allPlanFindingsAreMinorUnverifiable sentinel: a plan with zero
// findings is NOT force-passed by the calibration helper itself. After
// v0.5.2 the post-calibration FinalizePlanVerdict ladder derives `pass`
// from zero findings (the reviewer's bare `warn` claim with no findings
// is correctly recognized as inconsistent), but the calibration helper
// alone leaves the reviewer's verdict untouched. The NextAction check
// guards against the unverifiable-only force-pass message leaking onto
// this non-unverifiable input.
func TestValidatePlan_EmptyFindings_LadderDerivesPass(t *testing.T) {
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: one","verdict":"warn","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Reviewer warned but emitted no findings."}`)
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 1)
	require.NoError(t, err)
	// FinalizePlanVerdict ladder: zero findings → pass.
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.NotContains(t, pr.NextAction, "No blocking plan-quality findings",
		"the calibration helper's unverifiable-only message must not leak onto an empty-findings input")
}

// TestValidatePlan_RollupFallsBackToMergedPositionForBadTaskIndex defends
// against reviewers that emit chunk-local or zero task_index values:
// validateChunkIdentity only pins titles/order, so a stitched-result task
// can still carry index=0 or a chunk-local 1. The rollup must label such
// tasks with their merged-position ordinal (i+1) instead, so the human
// checklist remains sequentially unique rather than showing "Task 0:" or
// duplicate "Task 1:" lines.
func TestValidatePlan_RollupFallsBackToMergedPositionForBadTaskIndex(t *testing.T) {
	raw := []byte(`{
		"plan_verdict":"warn",
		"plan_quality":"actionable",
		"plan_findings":[],
		"tasks":[
			{"task_index":0,"task_title":"Task 1: one","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Foo.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""},
			{"task_index":1,"task_title":"Task 2: two","verdict":"warn","findings":[{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"Bar.kt","suggestion":"verify"}],"suggested_header_block":"","suggested_header_reason":""}
		],
		"next_action":"Verify."
	}`)
	pr, err := runValidatePlanWithReviewerJSON(t, raw, 2)
	require.NoError(t, err)
	require.Len(t, pr.PlanFindings, 1)
	// Without the fallback the rollup would emit "Task 0:" and "Task 1:".
	// With the fallback the first task lands at merged-position 1 and the
	// second keeps its reviewer-provided index of 1 — but the fallback only
	// fires on the first (TaskIndex == 0). Either way, no "Task 0:" appears
	// and merged-position 1 is referenced at least once.
	assert.NotContains(t, pr.PlanFindings[0].Evidence, "Task 0:")
	assert.Contains(t, pr.PlanFindings[0].Evidence, "Task 1:")
}

// ---------------------------------------------------------------------------
// Task 4 — validate_plan pass-result cache
// ---------------------------------------------------------------------------

func passPlanResp(nextAction string) providers.Response {
	return providers.Response{
		RawJSON: []byte(`{"plan_verdict":"pass","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: t1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":` + strconv.Quote(nextAction) + `}`),
		Model:   "claude-sonnet-4-6",
	}
}

func warnPlanResp(nextAction string) providers.Response {
	return providers.Response{
		RawJSON: []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"AC is vague","suggestion":"Clarify it."}],"tasks":[{"task_index":1,"task_title":"Task 1: t1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":` + strconv.Quote(nextAction) + `}`),
		Model:   "claude-sonnet-4-6",
	}
}

func planEnvelopeBody(t *testing.T, out *mcp.CallToolResult) struct {
	verdict.PlanResult
	ModelUsed string `json:"model_used"`
	ReviewMS  int64  `json:"review_ms"`
} {
	t.Helper()
	require.NotNil(t, out)
	require.Len(t, out.Content, 1)
	text, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var body struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &body))
	return body
}

func TestValidatePlan_CachePassingResult(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}

	_, first, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, first.PlanVerdict)
	assert.Equal(t, 1, rv.Calls)

	out, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, second.PlanVerdict)
	assert.Equal(t, 1, rv.Calls, "cache hit must not call reviewer")
	assert.Equal(t, "[cached <=3m] Proceed with implementation.", second.NextAction)
	assert.NotEmpty(t, first.PlanRunID, "a passing plan must mint a plan_run_id")
	assert.Equal(t, first.PlanRunID, second.PlanRunID,
		"cache hit must reuse the original call's plan_run_id, not mint a second run")
	body := planEnvelopeBody(t, out)
	assert.Equal(t, int64(0), body.ReviewMS)
	assert.Contains(t, body.SummaryBlock, "review_ms:     0")
	assert.Contains(t, body.SummaryBlock, "next_action:   [cached <=3m] Proceed with implementation.")
	assert.Equal(t, second.SummaryBlock, body.SummaryBlock)
}

func TestValidatePlan_CacheIsScopedToDeps(t *testing.T) {
	rv1 := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	d1 := newDeps(t, rv1)
	h1 := &handlers{deps: d1}
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}

	_, _, err := h1.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	_, _, err = h1.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, 1, rv1.Calls, "same handler should use its own cache")

	rv2 := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	d2 := newDeps(t, rv2)
	h2 := &handlers{deps: d2}
	_, _, err = h2.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, 1, rv2.Calls, "separate deps must not share cached plan results")
}

func TestValidatePlan_CacheHitChunkedPath(t *testing.T) {
	plan := buildPlanWithNTasks(9)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),
			chunkResp(t, titlesRange(1, 8)),
			chunkResp(t, titlesRange(9, 9)),
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, first.Tasks, 9)
	assert.Equal(t, 3, sr.calls)

	out, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, second.Tasks, 9)
	assert.Equal(t, 3, sr.calls, "chunked cache hit must avoid pass1 and chunk reviewer calls")
	assert.Equal(t, int64(0), planEnvelopeBody(t, out).ReviewMS)
}

func TestValidatePlan_CacheMissAxes(t *testing.T) {
	tests := []struct {
		name   string
		first  ValidatePlanArgs
		second ValidatePlanArgs
	}{
		{
			name:   "plan text",
			first:  ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)},
			second: ValidatePlanArgs{PlanText: buildPlanWithNTasks(2)},
		},
		{
			name:   "mode",
			first:  ValidatePlanArgs{PlanText: buildPlanWithNTasks(1), Mode: "quick"},
			second: ValidatePlanArgs{PlanText: buildPlanWithNTasks(1), Mode: "thorough"},
		},
		{
			name:   "model",
			first:  ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)},
			second: ValidatePlanArgs{PlanText: buildPlanWithNTasks(1), ModelOverride: "anthropic:claude-opus-4-7"},
		},
		{
			name:   "max token budget",
			first:  ValidatePlanArgs{PlanText: buildPlanWithNTasks(1), MaxTokensOverride: 4096},
			second: ValidatePlanArgs{PlanText: buildPlanWithNTasks(1), MaxTokensOverride: 8192},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
			d := newDeps(t, rv)
			h := &handlers{deps: d}

			_, _, err := h.ValidatePlan(context.Background(), nil, tt.first)
			require.NoError(t, err)
			_, second, err := h.ValidatePlan(context.Background(), nil, tt.second)
			require.NoError(t, err)
			assert.Equal(t, verdict.VerdictPass, second.PlanVerdict)
			assert.Equal(t, 2, rv.Calls, "changed %s should miss cache", tt.name)
		})
	}
}

func TestValidatePlan_TruncatedResultNotCached(t *testing.T) {
	partial := []byte(`{"plan_verdict":"warn","plan_quality":"rough","plan_findings":[{"severity":"major","category":"ambiguous_spec","criterion":"plan","evidence":"e","suggestion":"s"}],"tasks":[],"next_action":"retry"`)
	sr := &scriptedReviewer{
		responses: []providers.Response{
			{RawJSON: partial, Model: "claude-sonnet-4-6"},
			passPlanResp("Proceed with implementation."),
		},
		errors: []error{providers.ErrResponseTruncated, nil},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}

	_, first, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, first.Partial)
	_, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, second.PlanVerdict)
	assert.Equal(t, 2, sr.calls, "partial/truncated result must not populate pass cache")
}

func TestPlanPassCache_EvictsOldestExpiryWhenFull(t *testing.T) {
	cache := newPlanPassCache()
	for i := 0; i < planPassCacheMaxEntries+1; i++ {
		key := [32]byte{byte(i + 1)}
		cache.store(key, verdict.PlanResult{PlanVerdict: verdict.VerdictPass, NextAction: strconv.Itoa(i)}, "claude-sonnet-4-6")
	}
	assert.Equal(t, planPassCacheMaxEntries, cache.entryCountForTest())
	_, _, ok := cache.lookup([32]byte{1}, "")
	assert.False(t, ok, "oldest entry should be evicted when cache reaches max size")
}

func TestValidatePlan_CacheKeyIncludesClampState(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	d := newDeps(t, rv)
	d.Cfg.MaxTokensCeiling = 16384
	h := &handlers{deps: d}
	planText := buildPlanWithNTasks(1)

	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          planText,
		MaxTokensOverride: 32000,
	})
	require.NoError(t, err)
	require.Len(t, first.PlanFindings, 2)
	// The plan_text deprecation notice is prepended after the ladder runs,
	// landing ahead of the clamp finding.
	assert.Equal(t, "input", first.PlanFindings[0].Criterion)
	assert.Equal(t, "max_tokens_override", first.PlanFindings[1].Criterion)
	assert.Equal(t, 1, rv.Calls)

	_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          planText,
		MaxTokensOverride: 16384,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, rv.Calls, "same effective maxTokens with different clamp state must not share a cache entry")
	for _, finding := range second.PlanFindings {
		assert.NotEqual(t, "max_tokens_override", finding.Criterion, "exact-ceiling override must not inherit clamp finding")
	}
}

func TestPlanPassCache_LookupReturnsIndependentSlices(t *testing.T) {
	cache := newPlanPassCache()
	key := [32]byte{1}
	cache.store(key, verdict.PlanResult{
		PlanVerdict: verdict.VerdictPass,
		PlanFindings: []verdict.Finding{{
			Severity:  verdict.SeverityMinor,
			Category:  verdict.CategoryQuality,
			Criterion: "plan",
			Evidence:  "cached plan evidence",
		}},
		Tasks: []verdict.PlanTaskResult{{
			TaskIndex: 1,
			TaskTitle: "Task 1: t1",
			Verdict:   verdict.VerdictPass,
			Findings: []verdict.Finding{{
				Severity:  verdict.SeverityMinor,
				Category:  verdict.CategoryQuality,
				Criterion: "task",
				Evidence:  "cached task evidence",
			}},
		}},
		NextAction: "Proceed.",
	}, "claude-sonnet-4-6")

	first, _, ok := cache.lookup(key, "")
	require.True(t, ok)
	first.PlanFindings[0].Evidence = "mutated plan evidence"
	first.Tasks[0].Findings[0].Evidence = "mutated task evidence"

	second, _, ok := cache.lookup(key, "")
	require.True(t, ok)
	assert.Equal(t, "cached plan evidence", second.PlanFindings[0].Evidence)
	assert.Equal(t, "cached task evidence", second.Tasks[0].Findings[0].Evidence)
}

func TestValidatePlan_DoesNotCacheWarnResult(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: warnPlanResp("Clarify AC.")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}

	_, first, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictWarn, first.PlanVerdict)
	_, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictWarn, second.PlanVerdict)
	assert.Equal(t, 2, rv.Calls, "warn results must not be cached")
}

func TestPlanPassCache_ExpiredEntryMisses(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}

	_, _, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	d.planCache.expireForTest()

	_, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, second.PlanVerdict)
	assert.Equal(t, 2, rv.Calls, "expired cache entry must miss")
	assert.NotContains(t, second.NextAction, "[cached <=3m]")
}

func TestValidatePlan_PopulatesNormativeTestBodies(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{
				"plan_verdict":"pass",
				"plan_findings":[],
				"tasks":[
					{"task_index":1,"task_title":"Task 1: with bodies","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}
				],
				"next_action":"proceed",
				"plan_quality":"actionable"
			}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: with bodies\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\n@Test fun t() { /* body */ }\n```\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 1)
	assert.Equal(t, []string{"@Test fun t() { /* body */ }"}, pr.Tasks[0].NormativeTestBodies)
}

func TestValidatePlan_PopulatesNormativeTestBodies_ZeroBasedTaskIndex(t *testing.T) {
	// plan_schema.json accepts task_index minimum:0, and some reviewers emit
	// 0-based indices. Confirm populateNormativeTestBodies detects the base
	// and still maps the first task to tasks[0].
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{
				"plan_verdict":"pass",
				"plan_findings":[],
				"tasks":[
					{"task_index":0,"task_title":"Task 1: with bodies","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},
					{"task_index":1,"task_title":"Task 2: also bodies","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}
				],
				"next_action":"proceed",
				"plan_quality":"actionable"
			}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: with bodies\n\n**Goal:** g\n\n**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\n@Test fun first() {}\n```\n\n### Task 2: also bodies\n\n**Goal:** g\n\n**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\n@Test fun second() {}\n```\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Len(t, pr.Tasks, 2)
	assert.Equal(t, []string{"@Test fun first() {}"}, pr.Tasks[0].NormativeTestBodies)
	assert.Equal(t, []string{"@Test fun second() {}"}, pr.Tasks[1].NormativeTestBodies)
}

// ---------------------------------------------------------------------------
// Task 4 (v0.6.0) — validate_plan project_knowledge field
// ---------------------------------------------------------------------------

// TestValidatePlan_ProjectKnowledge_OverCap exercises the cumulative payload
// guard: when plan_text + project_knowledge exceeds PlanMaxPayloadBytes, the
// synthetic payload_too_large finding's evidence must name both contributors
// so the caller can tell which to shrink.
func TestValidatePlan_ProjectKnowledge_OverCap(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed.")}
	d := newDeps(t, rv)
	d.Cfg.PlanMaxPayloadBytes = 20
	h := &handlers{deps: d}

	planText := buildPlanWithNTasks(1) // > 20 bytes
	pk := "extra context"              // contributes to total
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:         planText,
		ProjectKnowledge: pk,
	})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	assert.Equal(t, 0, rv.Calls, "over-cap rejection must short-circuit the reviewer")
	// 2 findings: the plan_text deprecation notice (prepended ahead of the
	// too-large envelope, safe because the critical too-large finding
	// already forces fail) plus the too-large finding itself.
	require.Len(t, pr.PlanFindings, 2)
	assert.Equal(t, verdict.CategoryOther, pr.PlanFindings[0].Category)
	require.Equal(t, verdict.CategoryTooLarge, pr.PlanFindings[1].Category)
	evidence := pr.PlanFindings[1].Evidence
	assert.Contains(t, evidence, "plan:")
	assert.Contains(t, evidence, "project_knowledge:")
	assert.Contains(t, evidence, strconv.Itoa(len(planText)), "evidence reports plan_text byte count")
	assert.Contains(t, evidence, strconv.Itoa(len(pk)), "evidence reports project_knowledge byte count")
}

// TestValidatePlan_ProjectKnowledge_UnderCap_DispatchesReviewer ensures the
// reviewer is called normally when the cumulative payload fits and that the
// rendered prompt threaded into the request includes the Project knowledge
// section.
func TestValidatePlan_ProjectKnowledge_UnderCap_DispatchesReviewer(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	pk := "Decision 0042: cache pass reviews for 3 minutes."
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:         buildPlanWithNTasks(1),
		ProjectKnowledge: pk,
	})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.Equal(t, 1, rv.Calls, "under-cap call must dispatch to reviewer")
	for _, f := range pr.PlanFindings {
		assert.NotEqual(t, verdict.CategoryTooLarge, f.Category, "under-cap must not emit payload_too_large")
	}
	assert.Contains(t, rv.LastRequest.User, "## Project knowledge", "rendered prompt must include the Project knowledge section")
	assert.Contains(t, rv.LastRequest.User, pk, "rendered prompt must contain the project_knowledge value")
}

// TestValidatePlan_ProjectKnowledge_CacheKeySeparation guards against the
// regression where the plan-pass cache key omits project_knowledge and serves
// stale grounding. Two calls with identical plan_text but different non-empty
// project_knowledge values must both dispatch to the reviewer; neither call
// may be served from the other's cache entry.
func TestValidatePlan_ProjectKnowledge_CacheKeySeparation(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed.")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	planText := buildPlanWithNTasks(1)
	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:         planText,
		ProjectKnowledge: "Decision 0001: short ttl.",
	})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, first.PlanVerdict)
	assert.NotContains(t, first.NextAction, "[cached <=3m]")
	require.Equal(t, 1, rv.Calls)

	_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:         planText,
		ProjectKnowledge: "Decision 0002: different value.",
	})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, second.PlanVerdict)
	assert.NotContains(t, second.NextAction, "[cached <=3m]", "different project_knowledge must not hit the first call's cache entry")
	assert.Equal(t, 2, rv.Calls, "different project_knowledge must dispatch a fresh reviewer call")
}

// ---------------------------------------------------------------------------
// Task 8 — mint and thread plan_run_id
// ---------------------------------------------------------------------------

func TestValidatePlan_MintsPlanRunID(t *testing.T) {
	h := newTestPlanHandlers(t) // existing helper
	plan := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n"

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Regexp(t, `^pr_[0-9a-f]{12}$`, pr.PlanRunID)
	assert.Contains(t, pr.SummaryBlock, pr.PlanRunID)

	run, ok := h.deps.PlanRuns.Get(pr.PlanRunID)
	require.True(t, ok)
	assert.Equal(t, 1, run.TaskCount)
}

func TestValidatePlan_SchemaHasNoPlanRunID(t *testing.T) {
	assert.NotContains(t, string(verdict.PlanSchema()), "plan_run_id",
		"plan_run_id is server-set; a reviewer must never be asked to emit it")
}

func TestValidateTaskSpec_UnknownPlanRunIDStillSucceeds(t *testing.T) {
	h := newTestHandlers(t)
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", PlanRunID: "pr_000000000000",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, env.SessionID, "run bookkeeping must not block the review")
}

// TestValidateTaskSpec_ValidPlanRunID_AppendsRow pins that a validate_task_spec
// call carrying a real plan_run_id appends a row to the run, carrying the task
// title, the session id, and the pre-verdict.
func TestValidateTaskSpec_ValidPlanRunID_AppendsRow(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{
		passPlanResp("Proceed."),      // ValidatePlan
		passResp("claude-sonnet-4-6"), // ValidateTaskSpec
	}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	plan := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.NotEmpty(t, pr.PlanRunID)

	_, taskEnv, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 1: t", Goal: "g", PlanRunID: pr.PlanRunID,
	})
	require.NoError(t, err)

	run, ok := h.deps.PlanRuns.Snapshot(pr.PlanRunID)
	require.True(t, ok)
	require.Len(t, run.Rows, 1)
	assert.Equal(t, "Task 1: t", run.Rows[0].TaskTitle)
	assert.Equal(t, taskEnv.SessionID, run.Rows[0].SessionID)
	assert.Equal(t, taskEnv.Verdict, run.Rows[0].PreVerdict)
}

// TestCheckProgress_ValidPlanRunID_IncrementsCheckpoints pins that a
// check_progress call against a session created under a plan run increments
// that run's row checkpoint count.
func TestCheckProgress_ValidPlanRunID_IncrementsCheckpoints(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{
		passPlanResp("Proceed."),      // ValidatePlan
		passResp("claude-sonnet-4-6"), // ValidateTaskSpec
		passResp("claude-sonnet-4-6"), // CheckProgress
	}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	plan := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)

	_, taskEnv, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 1: t", Goal: "g", PlanRunID: pr.PlanRunID,
	})
	require.NoError(t, err)

	_, _, err = h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: taskEnv.SessionID, WorkingOn: "writing code",
	})
	require.NoError(t, err)

	run, ok := h.deps.PlanRuns.Snapshot(pr.PlanRunID)
	require.True(t, ok)
	require.Len(t, run.Rows, 1)
	assert.Equal(t, 1, run.Rows[0].Checkpoints)
}

// TestValidateCompletion_ValidPlanRunID_UpdatesRow pins that a
// validate_completion call against a session created under a plan run writes
// the post verdict, severity counts, submission_defect_only flag, CodeScene
// digest, and CodeScene state into that run's row.
func TestValidateCompletion_ValidPlanRunID_UpdatesRow(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{
		passPlanResp("Proceed."),      // ValidatePlan
		passResp("claude-sonnet-4-6"), // ValidateTaskSpec
		{
			RawJSON: []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"insufficient_evidence","criterion":"c","evidence":"e","suggestion":"s"}],"next_action":"n"}`),
			Model:   "claude-sonnet-4-6",
		}, // ValidateCompletion
	}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	plan := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)

	_, taskEnv, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 1: t", Goal: "g", PlanRunID: pr.PlanRunID,
	})
	require.NoError(t, err)

	_, complEnv, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: taskEnv.SessionID,
		Summary:   "did it",
		FinalDiff: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n",
		Codescene: &codescene.Digest{Ran: true, QualityGate: "passed"},
	})
	require.NoError(t, err)
	require.True(t, complEnv.SubmissionDefectOnly, "test setup must exercise the submission-defect-only path")

	run, ok := h.deps.PlanRuns.Snapshot(pr.PlanRunID)
	require.True(t, ok)
	require.Len(t, run.Rows, 1)
	row := run.Rows[0]
	assert.Equal(t, "warn", row.PostVerdict)
	assert.Equal(t, 1, row.Severity["major"])
	assert.True(t, row.SubmissionOnly)
	require.NotNil(t, row.Codescene)
	assert.True(t, row.Codescene.Ran)
	assert.Equal(t, planrun.StateRan, row.CodesceneState)
	assert.False(t, row.CompletedAt.IsZero())
}

// ---------------------------------------------------------------------------
// Task 3 (v0.16.0) — validate_plan plan_path input, plan_text deprecation
// ---------------------------------------------------------------------------

func TestValidatePlanPathInput(t *testing.T) {
	planMD := "# P\n\n### Task 1: Do a thing\n\n**Goal:** g\n\n**Acceptance criteria:**\n- [ ] a\n"

	t.Run("neither input", func(t *testing.T) {
		h := newTestPlanHandlers(t)
		_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan_text or plan_path is required")
	})

	t.Run("both inputs", func(t *testing.T) {
		h := newTestPlanHandlers(t)
		_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
			PlanText: planMD, PlanPath: "/tmp/x.md",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("plan_path matches plan_text", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "plan.md")
		require.NoError(t, os.WriteFile(p, []byte(planMD), 0o644))

		h := newTestPlanHandlers(t)
		_, viaPath, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: p})
		require.NoError(t, err)

		h2 := newTestPlanHandlers(t)
		_, viaText, err := h2.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planMD})
		require.NoError(t, err)

		assert.Equal(t, len(viaPath.Tasks), len(viaText.Tasks))
		assert.Equal(t, viaPath.PlanVerdict, viaText.PlanVerdict)
	})

	t.Run("plan_text emits one deprecation finding", func(t *testing.T) {
		h := newTestPlanHandlers(t)
		_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planMD})
		require.NoError(t, err)

		var dep []verdict.Finding
		for _, f := range pr.PlanFindings {
			if f.Criterion == "input" {
				dep = append(dep, f)
			}
		}
		require.Len(t, dep, 1)
		assert.Equal(t, verdict.SeverityMinor, dep[0].Severity)
		assert.Equal(t, verdict.CategoryOther, dep[0].Category)
		assert.Contains(t, dep[0].Suggestion, "plan_path")
	})

	t.Run("plan_path over plan cap returns envelope", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "big.md")
		require.NoError(t, os.WriteFile(p, bytes.Repeat([]byte("x"), 5000), 0o644))

		h := newTestPlanHandlers(t)
		h.deps.Cfg.PlanMaxPayloadBytes = 1024
		_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: p})
		require.NoError(t, err, "too-large is an envelope, not a transport error")
		require.NotEmpty(t, pr.PlanFindings)
		assert.Equal(t, verdict.CategoryTooLarge, pr.PlanFindings[0].Category)
		assert.Contains(t, pr.PlanFindings[0].Evidence, "5000", "true file size")
		assert.Contains(t, pr.PlanFindings[0].Evidence, "plan:")
		assert.NotContains(t, pr.PlanFindings[0].Evidence, "plan_text:")
	})

	t.Run("plan cap is independent of shared cap", func(t *testing.T) {
		h := newTestPlanHandlers(t)
		h.deps.Cfg.MaxPayloadBytes = 10 // would reject if the plan path used it
		h.deps.Cfg.PlanMaxPayloadBytes = 1 << 20
		_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planMD})
		require.NoError(t, err)
		for _, f := range pr.PlanFindings {
			assert.NotEqual(t, verdict.CategoryTooLarge, f.Category)
		}
	})
}

// ---------------------------------------------------------------------------
// Task 3 fix round 1 (v0.16.0) — deprecation must not leak across the shared
// plan-pass cache entry when plan_text and plan_path supply identical content
// ---------------------------------------------------------------------------

// countPlanDeprecationFindings counts how many findings in the slice are the
// plan_text deprecation notice (see prependPlanDeprecation). Unlike
// stripPlanDeprecationFinding, this does not mutate/copy — it's used purely
// for assertions on finding count.
func countPlanDeprecationFindings(findings []verdict.Finding) int {
	n := 0
	for _, f := range findings {
		if f.Category == verdict.CategoryOther && f.Criterion == "input" {
			n++
		}
	}
	return n
}

// assertPlanSummaryMatchesFindings checks that SummaryBlock's
// `plan_findings: N (...)` count matches len(pr.PlanFindings), so the two
// never drift when the deprecation finding is added/omitted after
// SummaryBlock was first computed.
func assertPlanSummaryMatchesFindings(t *testing.T, pr verdict.PlanResult) {
	t.Helper()
	want := "plan_findings: " + strconv.Itoa(len(pr.PlanFindings)) + " ("
	assert.Contains(t, pr.SummaryBlock, want,
		"SummaryBlock's plan_findings count must match len(PlanFindings)")
}

// TestValidatePlan_DeprecationNotCachedAcrossInputMethods is the regression
// test for the review finding that planPassCacheKey keys only on plan
// content/model/mode, not on which argument (plan_text or plan_path)
// supplied it. Two calls with byte-identical plan content share one cache
// entry within the pass cache's TTL; the deprecation finding must therefore
// be applied per-call from the shared entry, never stored on it — otherwise
// whichever call populates the entry first would decide whether the OTHER
// call's response carries the deprecation notice, independent of what that
// second call actually passed.
func TestValidatePlan_DeprecationNotCachedAcrossInputMethods(t *testing.T) {
	planMD := buildPlanWithNTasks(1)

	t.Run("plan_path then plan_text: cache hit gains the deprecation finding", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "plan.md")
		require.NoError(t, os.WriteFile(p, []byte(planMD), 0o644))

		rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
		h := &handlers{deps: newDeps(t, rv)}

		_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: p})
		require.NoError(t, err)
		require.Equal(t, verdict.VerdictPass, first.PlanVerdict, "only pass results are cached")
		assert.Equal(t, 0, countPlanDeprecationFindings(first.PlanFindings),
			"plan_path call must not carry the deprecation finding")
		assertPlanSummaryMatchesFindings(t, first)
		require.Equal(t, 1, rv.Calls)

		_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planMD})
		require.NoError(t, err)
		assert.Equal(t, 1, rv.Calls, "second call must be a cache hit, not a fresh reviewer call")
		assert.Contains(t, second.NextAction, "[cached",
			"second call must be served from the shared cache entry, proving this exercises the cache-hit path")
		assert.Equal(t, 1, countPlanDeprecationFindings(second.PlanFindings),
			"plan_text call must carry exactly one deprecation finding, even on a cache hit")
		assertPlanSummaryMatchesFindings(t, second)
	})

	t.Run("plan_text then plan_path: cache hit does not inherit the deprecation finding", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "plan.md")
		require.NoError(t, os.WriteFile(p, []byte(planMD), 0o644))

		rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
		h := &handlers{deps: newDeps(t, rv)}

		_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planMD})
		require.NoError(t, err)
		require.Equal(t, verdict.VerdictPass, first.PlanVerdict, "only pass results are cached")
		assert.Equal(t, 1, countPlanDeprecationFindings(first.PlanFindings),
			"plan_text call must carry exactly one deprecation finding")
		assertPlanSummaryMatchesFindings(t, first)
		require.Equal(t, 1, rv.Calls)

		_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: p})
		require.NoError(t, err)
		assert.Equal(t, 1, rv.Calls, "second call must be a cache hit, not a fresh reviewer call")
		assert.Contains(t, second.NextAction, "[cached",
			"second call must be served from the shared cache entry, proving this exercises the cache-hit path")
		assert.Equal(t, 0, countPlanDeprecationFindings(second.PlanFindings),
			"plan_path call must not inherit the plan_text call's deprecation finding")
		assertPlanSummaryMatchesFindings(t, second)
	})
}

// TestValidatePlanProvenanceOnCacheHit is the regression test for the
// provenance requirement that a cache hit must render the CURRENT call's
// source, never the stored entry's. planPassCacheKey hashes content, so two
// different paths holding byte-identical plans share one cache entry;
// echoing the stored path would name an earlier caller's file.
func TestValidatePlanProvenanceOnCacheHit(t *testing.T) {
	planMD := "# P\n\n### Task 1: T\n\n**Goal:** g\n\n**Acceptance criteria:**\n- [ ] a\n"
	dirA, dirB := t.TempDir(), t.TempDir()
	pa := filepath.Join(dirA, "plan.md")
	pb := filepath.Join(dirB, "plan.md")
	require.NoError(t, os.WriteFile(pa, []byte(planMD), 0o644))
	require.NoError(t, os.WriteFile(pb, []byte(planMD), 0o644)) // identical content

	h := newTestPlanHandlers(t)
	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: pa})
	require.NoError(t, err)
	require.Equal(t, verdict.VerdictPass, first.PlanVerdict, "must pass so it is cached")

	_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: pb})
	require.NoError(t, err)
	assert.Contains(t, second.SummaryBlock, "[cached", "identical content hits the cache")
	assert.Contains(t, second.SummaryBlock, dirB, "echoes the CURRENT call's path")
	assert.NotContains(t, second.SummaryBlock, dirA, "must not echo the cached entry's path")
}

// TestPlanPassCacheKey_CoversBothHalves is the regression test for the
// requirement that planPassCacheKey covers the FULL rendered prompt —
// UserPrefix and UserSuffix — not just the shared prefix. If either half
// dropped out of the key, a template edit confined to that half would be
// invisible to the cache and could serve stale cached "pass" results for up
// to planPassCacheTTL.
func TestPlanPassCacheKey_CoversBothHalves(t *testing.T) {
	build := func(prefix, suffix string) renderedPlanReview {
		return renderedPlanReview{
			FindingsOnly: &prompts.Output{System: "sys", UserPrefix: prefix, UserSuffix: suffix},
			Chunks: []renderedPlanChunk{
				{Prompt: prompts.Output{System: "sys", UserPrefix: prefix, UserSuffix: "chunk-body"}},
			},
		}
	}
	keyOf := func(r renderedPlanReview) [32]byte {
		return planPassCacheKey("plan", "pk", "mode", "model", 100, 0, r, "")
	}

	base := keyOf(build("shared-prefix", "suffix-v1"))

	t.Run("suffix-only edit changes the key", func(t *testing.T) {
		edited := keyOf(build("shared-prefix", "suffix-v2"))
		assert.NotEqual(t, base, edited, "a per-call suffix template edit must change the cache key")
	})

	t.Run("prefix-only edit changes the key", func(t *testing.T) {
		edited := keyOf(build("shared-prefix-edited", "suffix-v1"))
		assert.NotEqual(t, base, edited, "a shared-prefix template edit must change the cache key")
	})

	t.Run("identical inputs produce identical keys", func(t *testing.T) {
		again := keyOf(build("shared-prefix", "suffix-v1"))
		assert.Equal(t, base, again)
	})
}

func TestValidatePlan_ContextPaths_ReachThePrompt(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.go")
	require.NoError(t, os.WriteFile(f, []byte("package config\n// SENTINEL_ATTACHED\n"), 0o600))

	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 1)}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(1),
		ContextPaths: []string{f},
	})
	require.NoError(t, err)
	require.NotEmpty(t, sr.requests)
	assert.Contains(t, sr.requests[0].User, "SENTINEL_ATTACHED")
	assert.Contains(t, sr.requests[0].User, "## Attached source files")
}

func TestValidatePlan_ContextPaths_TooLargeIsAnEnvelope(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(f, []byte(strings.Repeat("x", 500)), 0o600))

	sr := &scriptedReviewer{}
	d := newDepsWithScripted(t, sr, 8)
	d.Cfg.ContextMaxFileBytes = 100
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(1),
		ContextPaths: []string{f},
	})
	require.NoError(t, err, "a cap breach is an envelope, not a transport error")
	assert.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 2, "the too-large finding plus the plan_text deprecation notice")
	assert.Equal(t, 1, countPlanDeprecationFindings(pr.PlanFindings),
		"a plan_text caller must still get the deprecation notice on this envelope, like every other too-large exit")

	var tooLarge *verdict.Finding
	for i := range pr.PlanFindings {
		if pr.PlanFindings[i].Category == verdict.CategoryTooLarge {
			tooLarge = &pr.PlanFindings[i]
		}
	}
	require.NotNil(t, tooLarge, "the too-large finding must still be present")
	assert.Contains(t, tooLarge.Evidence, "ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES")
	assert.Zero(t, sr.calls, "no reviewer call is made on a refused payload")
}

// TestValidatePlan_ContextPaths_CacheHitAcrossSeparateCalls is the
// end-to-end regression test for the plan-pass cache fix: two SEPARATE
// ValidatePlan calls with byte-identical plan_text AND byte-identical
// context_paths, inside the cache TTL, must produce a cache hit (the
// reviewer call count must stay at 1). planPassCacheKey hashes the fully
// rendered prompt text, so this is the capability a crypto/rand-backed
// per-render nonce would have silently killed for every context_paths
// caller — the calls that cost the most — and that a unit test on nonce
// equality alone would not prove, since it never exercises the cache.
func TestValidatePlan_ContextPaths_CacheHitAcrossSeparateCalls(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.go")
	require.NoError(t, os.WriteFile(f, []byte("package config\n// SENTINEL_ATTACHED\n"), 0o600))

	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 1)}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	args := ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(1),
		ContextPaths: []string{f},
	}

	_, first, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	require.Equal(t, verdict.VerdictPass, first.PlanVerdict, "must pass so it is cached")
	require.Equal(t, 1, sr.calls)

	_, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, 1, sr.calls,
		"a second call with identical plan_text and identical context_paths must be a cache hit, not a fresh reviewer call")
	assert.Contains(t, second.NextAction, "[cached",
		"second call must be served from the shared cache entry, proving this exercises the cache-hit path")
}

func TestValidatePlan_ContextPaths_BadPathIsTransportError(t *testing.T) {
	sr := &scriptedReviewer{}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(1),
		ContextPaths: []string{"/nonexistent/nope.go"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context_paths")
	assert.Zero(t, sr.calls)
}

// Spec §11: the attachment block must land inside the CACHEABLE prefix on
// the chunked path, not in the per-call suffix — that placement is the whole
// reason attachments render before the plan.
func TestValidatePlan_ContextPaths_LandInTheCachePrefix(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.go")
	require.NoError(t, os.WriteFile(f, []byte("package config\n// SENTINEL_ATTACHED\n"), 0o600))

	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),
			chunkResp(t, titlesRange(1, 8)),
			chunkResp(t, titlesRange(9, 9)),
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(9),
		ContextPaths: []string{f},
	})
	require.NoError(t, err)
	require.Len(t, sr.requests, 3)

	// Pass 1 carries no breakpoint (its tools block differs), so the
	// attachment is in its plain User body.
	assert.Empty(t, sr.requests[0].CachePrefix)
	assert.Contains(t, sr.requests[0].User, "SENTINEL_ATTACHED")

	// Both chunk calls carry the attachment in the SHARED prefix, and their
	// per-call suffixes must not repeat it.
	for i := 1; i < 3; i++ {
		assert.Contains(t, sr.requests[i].CachePrefix, "SENTINEL_ATTACHED", "chunk %d prefix", i)
		assert.NotContains(t, sr.requests[i].User, "SENTINEL_ATTACHED", "chunk %d suffix", i)
	}
	assert.Equal(t, sr.requests[1].CachePrefix, sr.requests[2].CachePrefix,
		"the prefix must be byte-identical across chunks or nothing is ever cache-read")
}

func TestValidatePlan_NoContextPaths_PromptUnchanged(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 1)}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: buildPlanWithNTasks(1),
	})
	require.NoError(t, err)
	assert.NotContains(t, sr.requests[0].User, "## Attached source files")
	assert.Contains(t, sr.requests[0].User, "You have access ONLY to the plan markdown")
}

// A stale review after an attached file is edited is the failure this guards:
// a caller who fixes the file a finding complained about and re-validates
// inside the 3-minute TTL must not get the pre-fix review back.
//
// The key covers attachments through the RENDERED PROMPTS, which interpolate
// each file's path, byte count, short hash and complete contents — so this
// drives the real render rather than hand-assembling a key projection, and
// would catch the attachment section being dropped from the template just as
// well as the key being dropped.
func TestPlanPassCacheKey_VariesWithAttachedContent(t *testing.T) {
	mk := func(t *testing.T, content string) [32]byte {
		t.Helper()
		sum := sha256.Sum256([]byte(content))
		files := []contextFile{{
			Source:  fileSource{Path: "/a.go", Bytes: len(content), SHA256: hex.EncodeToString(sum[:])},
			Content: content,
		}}
		plan := buildPlanWithNTasks(1)
		tasks, _ := planparser.SplitTasks(plan)
		rendered, err := renderPlanReview(renderPlanReviewInputs{
			PlanText:     plan,
			Tasks:        tasks,
			ChunkSize:    8,
			ContextFiles: toPromptContextFiles(files),
		})
		require.NoError(t, err)
		return planPassCacheKey("plan", "", "", "m", 100, 0, rendered, "")
	}
	assert.NotEqual(t, mk(t, "package a\n"), mk(t, "package a\n\nfunc B() {}\n"))
	assert.Equal(t, mk(t, "package a\n"), mk(t, "package a\n"))
}

// The mirror of the above: with no attachments at all the key must still be
// stable across identical calls.

func TestPlanPassCacheKey_NoAttachmentsIsStable(t *testing.T) {
	rendered := renderedPlanReview{}
	a := planPassCacheKey("plan", "", "", "m", 100, 0, rendered, "")
	b := planPassCacheKey("plan", "", "", "m", 100, 0, rendered, "")
	assert.Equal(t, a, b)
}

// TestPlanPassCacheKey_VariesWithRepoRoot is the unit-level companion to
// TestValidatePlan_RepoRootChangeBustsPlanPassCache: repo_root gates
// checkFileConsistency's disk tier, so two calls with byte-identical plan
// text but different repo_root can produce genuinely different results and
// MUST NOT collide in the cache.
func TestPlanPassCacheKey_VariesWithRepoRoot(t *testing.T) {
	rendered := renderedPlanReview{}
	mk := func(repoRoot string) [32]byte {
		return planPassCacheKey("plan", "", "", "m", 100, 0, rendered, repoRoot)
	}
	assert.NotEqual(t, mk(""), mk("/repo/a"))
	assert.NotEqual(t, mk("/repo/a"), mk("/repo/b"))
	assert.Equal(t, mk("/repo/a"), mk("/repo/a"))
}

func TestValidatePlan_FileConsistencyFindingReachesTheEnvelope(t *testing.T) {
	plan := "# Plan\n\n" +
		"### Task 1: t1\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Modify: `a.go`\n\n" +
		"### Task 2: t2\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Create: `a.go`\n\n"

	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 2)}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)

	var found bool
	for _, f := range pr.PlanFindings {
		if f.Criterion == "task_order_contradiction" {
			found = true
			assert.Equal(t, verdict.SeverityMajor, f.Severity)
		}
	}
	assert.True(t, found, "the deterministic check's finding must reach the envelope")
}

// TestValidatePlan_RepoRootChangeBustsPlanPassCache is the regression test
// for the review finding that planPassCacheKey did not key on repoRoot: a
// caller who first validates without repo_root (order tier only; passes and
// is cached) and then re-validates the byte-identical plan WITH repo_root
// must get a fresh review that runs the disk tier — not the stale cached
// pass, which would silently skip the disk tier the caller just asked for.
// A unit test on planPassCacheKey alone cannot catch this: the bug lives in
// the interaction between the cache-hit return at the top of ValidatePlan
// and the checkFileConsistency call 44 lines later on the fresh path.
func TestValidatePlan_RepoRootChangeBustsPlanPassCache(t *testing.T) {
	plan := "# Plan\n\n" +
		"### Task 1: t1\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Modify: `a.go`\n\n"

	dir := t.TempDir() // empty: a.go does not exist here

	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	h := &handlers{deps: newDeps(t, rv)}

	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.Equal(t, verdict.VerdictPass, first.PlanVerdict, "no repo_root: order tier alone finds nothing")
	require.Equal(t, 1, rv.Calls)
	for _, f := range first.PlanFindings {
		require.NotEqual(t, "task_order_contradiction", f.Criterion)
	}

	_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan, RepoRoot: dir})
	require.NoError(t, err)
	assert.Equal(t, 2, rv.Calls, "repo_root must change the cache key: this must be a fresh review, not a cache hit")
	assert.NotContains(t, second.NextAction, "[cached", "must not be served from the no-repo_root entry")

	var found bool
	for _, f := range second.PlanFindings {
		if f.Criterion == "task_order_contradiction" {
			found = true
			assert.Contains(t, f.Evidence, "does not exist")
		}
	}
	assert.True(t, found, "the disk tier must run once repo_root is supplied, even though the plan text is byte-identical to the cached call")
}

// planRespWithContradiction is a plan response carrying one
// contradicted_codebase_claim at plan level and one on the single task.
func planRespWithContradiction(t *testing.T, severity string) providers.Response {
	t.Helper()
	f := `{"severity":"` + severity + `","category":"contradicted_codebase_claim","criterion":"c","evidence":"e","suggestion":"s"}`
	raw := []byte(`{"plan_verdict":"warn","plan_quality":"actionable","plan_findings":[` + f + `],"tasks":[` +
		`{"task_index":1,"task_title":"Task 1: t1","verdict":"warn","findings":[` + f + `],"suggested_header_block":"","suggested_header_reason":""}` +
		`],"next_action":"fix it"}`)
	return providers.Response{RawJSON: raw, Model: "claude-sonnet-4-6"}
}

// contradicted_codebase_claim exists because an attached file is ground truth,
// which is why it alone among the codebase-claim categories carries NO
// severity floor. With nothing attached the reviewer has no file to refute
// anything with, so an unfloored major/critical finding can fail a plan gate
// over code the reviewer never saw. The prompt already forbids it; suppression
// must ALSO run server-side, independent of reviewer compliance
// (controller.md §5.8).
func TestValidatePlan_ContradictionWithoutAttachmentsIsDemoted(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{planRespWithContradiction(t, "major")}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: buildPlanWithNTasks(1),
	})
	require.NoError(t, err)

	all := append([]verdict.Finding{}, pr.PlanFindings...)
	for _, tk := range pr.Tasks {
		all = append(all, tk.Findings...)
	}
	var contradictions, demoted int
	for _, f := range all {
		switch f.Category {
		case verdict.CategoryContradictedCodebaseClaim:
			contradictions++
		case verdict.CategoryUnverifiableCodebaseClaim:
			demoted++
			assert.Equal(t, verdict.SeverityMinor, f.Severity,
				"a demoted contradiction must land on the unverifiable floor")
		}
	}
	assert.Zero(t, contradictions,
		"no contradicted_codebase_claim may survive a call with no context_paths")
	assert.Equal(t, 2, demoted, "both the plan-level and the per-task finding must be demoted")
}

// The mirror: WITH an attachment the category is legitimate and must survive
// at the reviewer's chosen severity, floor-free.
func TestValidatePlan_ContradictionWithAttachmentsSurvives(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.go", "package a\n")

	sr := &scriptedReviewer{responses: []providers.Response{planRespWithContradiction(t, "major")}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(1),
		ContextPaths: []string{f},
	})
	require.NoError(t, err)

	require.NotEmpty(t, pr.PlanFindings)
	var found bool
	for _, fd := range pr.PlanFindings {
		if fd.Category == verdict.CategoryContradictedCodebaseClaim {
			found = true
			assert.Equal(t, verdict.SeverityMajor, fd.Severity,
				"contradicted_codebase_claim has no severity floor when a file backs it")
		}
	}
	assert.True(t, found, "an attached call must keep contradicted_codebase_claim")
}

// The Create/Modify consistency check needs no reviewer and cannot itself be
// truncated, so a truncated reviewer response must not silently drop it — it
// is the one finding the server already knows for certain. The same envelope
// must also still name the attached files in its summary, which it did not:
// the truncation path built planSummaryMeta with no ContextFiles while the
// same call's stats counted every attached byte.
func TestValidatePlan_TruncationKeepsFileConsistencyAndContextProvenance(t *testing.T) {
	dir := t.TempDir()
	attached := writeTemp(t, dir, "attached.go", "package a\n")

	// Task 1 modifies a.go; Task 2 creates it — an order-tier contradiction
	// provable from the plan text alone.
	plan := "# Plan\n\n" +
		"### Task 1: t1\n\n**Goal:** g1\n\n**Files:**\n- Modify: `a.go`\n\n" +
		"### Task 2: t2\n\n**Goal:** g2\n\n**Files:**\n- Create: `a.go`\n\n"

	rawJSON := []byte(`{"plan_verdict":"warn","plan_findings":[` +
		`{"severity":"major","category":"other","criterion":"pf1","evidence":"e","suggestion":"s"}` +
		`],"tasks":[` +
		`{"task_index":1,"task_title":"Task 1: t1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2,"task_title":"Task 2: t2","verdict":"warn","find`)

	sr := &scriptedReviewer{
		responses: []providers.Response{{RawJSON: rawJSON, Model: "claude-sonnet-4-6"}},
		errors:    []error{providers.ErrResponseTruncated},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     plan,
		ContextPaths: []string{attached},
	})
	require.NoError(t, err)
	require.True(t, pr.Partial, "this must be the truncation-recovery path")

	var gotConsistency bool
	for _, f := range pr.PlanFindings {
		if f.Criterion == "task_order_contradiction" {
			gotConsistency = true
		}
	}
	assert.True(t, gotConsistency,
		"the reviewer-free Create/Modify finding must survive a truncated reviewer response")

	assert.Contains(t, pr.SummaryBlock, "context:",
		"a truncated review must still name what the reviewer was given")
	assert.Contains(t, pr.SummaryBlock, mustEval(t, attached))
}
