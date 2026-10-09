package mcpsrv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func storeAndCacheRun(h *handlers) *planrun.Run {
	return h.deps.PlanRuns.CreateWithTasks("pass", "rigorous", []planrun.PlanTask{
		{Index: 1, Title: "Task 1: Store"},
		{Index: 2, Title: "Task 2: Cache"},
	})
}

func TestValidateTaskSpec_AttachesByTitleToTheSingleLiveRun(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
	run := storeAndCacheRun(h)

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Cache", Goal: "g"})
	require.NoError(t, err)

	assert.Equal(t, "pass", env.Verdict, "the advisory describes the call and moves no verdict")
	advisories := planRunIDFindings(env.Findings)
	require.Len(t, advisories, 1)
	assert.Contains(t, advisories[0].Evidence, "was attached to that run")
	assert.Contains(t, advisories[0].Suggestion, "plan_run_id="+run.ID)

	snap, ok := h.deps.PlanRuns.Snapshot(run.ID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1)
	assert.Equal(t, 2, snap.Rows[0].Index)
	assert.Equal(t, "pass", snap.Rows[0].PreVerdict)
	assert.False(t, snap.Rows[0].Unmatched)

	sess, ok := h.deps.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, run.ID, sess.PlanRunID, "later calls on the session update the same row")
	assert.NotContains(t, sess.PreFindings, advisories[0], "the advisory is not a pre-task finding")
}

func TestValidateTaskSpec_DoesNotGuessBetweenRunsOrTitles(t *testing.T) {
	t.Run("a title matching no heading attaches nothing", func(t *testing.T) {
		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		run := storeAndCacheRun(h)
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Something else", Goal: "g"})
		require.NoError(t, err)

		advisories := planRunIDFindings(env.Findings)
		require.Len(t, advisories, 1)
		assert.Contains(t, advisories[0].Evidence, "passed no plan_run_id, but this server holds a live plan run")
		snap, _ := h.deps.PlanRuns.Snapshot(run.ID)
		assert.Empty(t, snap.Rows)
		sess, _ := h.deps.Sessions.Get(env.SessionID)
		assert.Empty(t, sess.PlanRunID)
	})

	t.Run("two live runs attach nothing", func(t *testing.T) {
		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		first, second := storeAndCacheRun(h), storeAndCacheRun(h)
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Cache", Goal: "g"})
		require.NoError(t, err)

		for _, run := range []*planrun.Run{first, second} {
			snap, _ := h.deps.PlanRuns.Snapshot(run.ID)
			assert.Empty(t, snap.Rows)
		}
		require.Len(t, planRunIDFindings(env.Findings), 1)
	})

	t.Run("a named run is used as named", func(t *testing.T) {
		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		run := storeAndCacheRun(h)
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Cache", Goal: "g", PlanRunID: run.ID})
		require.NoError(t, err)
		assert.Empty(t, planRunIDFindings(env.Findings))
		snap, _ := h.deps.PlanRuns.Snapshot(run.ID)
		assert.Len(t, snap.Rows, 1)
	})
}

func TestValidateTaskSpec_ARevisedPlanStillHasOneRunToAttachTo(t *testing.T) {
	h, _ := roundHandlers(t, 8,
		roundSingleResp("", roundTitles(2)...),
		roundPlanLevelResp("", "n"),
		roundChunkResp(roundTask{title: "Task 2: t2"}),
		passResp("claude-sonnet-4-6"),
	)
	first := validatePlanRound(t, h, ValidatePlanArgs{PlanText: buildPlanWithNTasks(2)})
	validatePlanRound(t, h, ValidatePlanArgs{PlanText: planWithEditedTask(2, 2), PlanRunID: first.PlanRunID})

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Task 2: t2", Goal: "g"})
	require.NoError(t, err)
	require.NotEmpty(t, env.SessionID)

	snap, ok := h.deps.PlanRuns.Snapshot(first.PlanRunID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1, "two validate_plan rounds that named the run leave one run, so the task finds it by title")
	assert.Equal(t, 2, snap.Rows[0].Index)
}

func TestValidateTaskSpec_AttachByTitleWithNoRunStoreIsANoOp(t *testing.T) {
	d := newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")})
	d.PlanRuns = nil
	d.Sessions = session.NewStore(d.Cfg.SessionTTL)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Cache", Goal: "g"})
	require.NoError(t, err)
	assert.Empty(t, planRunIDFindings(env.Findings))
}

func TestValidateTaskSpec_AttachByTitleIgnoresATaskIndexSentWithoutARun(t *testing.T) {
	h := &handlers{deps: newDeps(t, taskSpecClaimsReviewer())}
	run := h.deps.PlanRuns.CreateWithTasks("pass", "rigorous", []planrun.PlanTask{
		{Index: 1, Title: "Task 1: Store", Files: []string{"pkg/store.go"}},
		{Index: 2, Title: "Task 2: Cache", Files: []string{"pkg/cache.go"}},
	})

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Cache", Goal: "g", TaskIndex: 1})
	require.NoError(t, err)

	snap, ok := h.deps.PlanRuns.Snapshot(run.ID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1)
	assert.Equal(t, 2, snap.Rows[0].Index, "the title found the task, so the title names the row")
	assert.Equal(t, []string{"pkg/store.go"}, env.CodebaseReferenceChecklist,
		"the paths the run lists for the matched task are dropped, not those of the task at task_index")
}

func TestValidateTaskSpec_ATruncatedReviewIsNotToldItWasAttached(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
	h := &handlers{deps: newDeps(t, rv)}
	run := storeAndCacheRun(h)

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Cache", Goal: "g"})
	require.NoError(t, err)
	require.Empty(t, env.SessionID, "a truncated review creates no session")

	advisories := planRunIDFindings(env.Findings)
	require.Len(t, advisories, 1)
	assert.NotContains(t, advisories[0].Evidence, "was attached")
	assert.Contains(t, advisories[0].Evidence, "passed no plan_run_id, but this server holds a live plan run")
	snap, _ := h.deps.PlanRuns.Snapshot(run.ID)
	assert.Empty(t, snap.Rows)
}

func TestWithdrawAttachedByTitle_KeepsTheFindingsPlaceAndID(t *testing.T) {
	env := Envelope{Findings: []verdict.Finding{
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality, Criterion: "naming", Evidence: "unclear name"},
		attachedByTitleAdvisory("pr_0123456789ab"),
	}}
	assignEnvelopeIDs(&env)
	reviewer, id := env.Findings[0], env.Findings[1].ID
	require.NotEmpty(t, id)

	withdrawAttachedByTitle(&env, "pr_0123456789ab")

	require.Len(t, env.Findings, 2)
	assert.Equal(t, reviewer, env.Findings[0])
	plain := planRunIDAdvisory("pr_0123456789ab")
	plain.ID = id
	assert.Equal(t, plain, env.Findings[1], "a task that was not attached is not told it was")
	assert.NotContains(t, env.Findings[1].Evidence, "was attached")

	untouched := Envelope{Findings: []verdict.Finding{reviewer}}
	withdrawAttachedByTitle(&untouched, "pr_0123456789ab")
	assert.Equal(t, []verdict.Finding{reviewer}, untouched.Findings)
	withdrawAttachedByTitle(&Envelope{}, "pr_0123456789ab")
}

func TestValidateTaskSpec_AttachedByTitleAdvisoryFollowsTheModeNotes(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
	storeAndCacheRun(h)

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "Cache", Goal: "g", TaskKind: "spike"})
	require.NoError(t, err)

	require.NotEmpty(t, env.Findings)
	last := env.Findings[len(env.Findings)-1]
	assert.Equal(t, "plan_run_id", last.Criterion, "withdrawAttachedByTitle rewrites the last finding")
	assert.Contains(t, findingCategories(env.Findings), verdict.CategoryUnknownKind)
}
