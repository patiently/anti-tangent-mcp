package mcpsrv

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// completionCallArgs is a minimal validate_completion call with one inline file.
func completionCallArgs(sessionID string) ValidateCompletionArgs {
	return ValidateCompletionArgs{
		SessionID:  sessionID,
		Summary:    "done",
		FinalFiles: []CompletionFileArg{{Path: "f.go", Content: strPtr("package f\n")}},
	}
}

// reviewerFindingsResp is a complete per-task reviewer response carrying the
// given finding objects, each a JSON object literal.
func reviewerFindingsResp(findings ...string) providers.Response {
	body := `{"verdict":"warn","findings":[`
	for i, f := range findings {
		if i > 0 {
			body += ","
		}
		body += f
	}
	return providers.Response{RawJSON: []byte(body + `],"next_action":"n"}`), Model: "claude-opus-4-7"}
}

func evidenceOf(fs []verdict.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Evidence)
	}
	return out
}

func TestRecoverPartialFindings_ReturnsTheMarkerApartFromReviewerFindings(t *testing.T) {
	raw := []byte(`{"verdict":"warn","findings":[` +
		`{"severity":"major","category":"other","criterion":"ac1","evidence":"e1","suggestion":"s1"},` +
		`{"severity":"minor","category":"other","crit`)
	r, marker, ok := recoverPartialFindings(raw, perTaskMaxTokensEnvVar)
	require.True(t, ok)
	require.Len(t, r.Findings, 1, "the marker is not one of the reviewer's findings")
	assert.Equal(t, "ac1", r.Findings[0].Criterion)
	assert.Equal(t, "reviewer_response", marker.Criterion)
	assert.Equal(t, verdict.SeverityMinor, marker.Severity)
	assert.Contains(t, marker.Evidence, "1 complete findings recovered")
}

func TestCheckProgress_TruncatedReviewRecordsNoCheckpoint(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: newDeps(t, rv)}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)

	rv.err = providers.ErrResponseTruncated
	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{SessionID: pre.SessionID, WorkingOn: "x"})
	require.NoError(t, err)
	assert.Equal(t, "warn", env.Verdict)

	sess, ok := h.deps.Sessions.Get(pre.SessionID)
	require.True(t, ok)
	assert.Empty(t, sess.Checkpoints, "a truncated review is not a checkpoint")
}

func TestValidateCompletion_TruncatedReviewRunsTheNormalTail(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	d.Cfg.Codescene = "required"
	h := &handlers{deps: d}
	run := h.deps.PlanRuns.Create("pass", "actionable", 1)
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}, PlanRunID: run.ID,
	})
	require.NoError(t, err)

	rv.err = providers.ErrResponseTruncated
	_, env, err := h.ValidateCompletion(context.Background(), nil, completionCallArgs(pre.SessionID))
	require.NoError(t, err)
	assert.True(t, hasCategory(env.Findings, verdict.CategoryCodesceneNotRun),
		"a truncated review still gets the CodeScene check: %+v", env.Findings)

	snap, ok := h.deps.PlanRuns.Snapshot(run.ID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1)
	assert.Equal(t, env.Verdict, snap.Rows[0].PostVerdict, "a truncated review still updates the plan-run row")
}

func TestValidateCompletion_TruncatedReviewKeepsTheLastCompleteFindings(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	h := &handlers{deps: newDeps(t, rv)}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}})
	require.NoError(t, err)

	rv.resp = reviewerFindingsResp(`{"severity":"major","category":"scope_drift","criterion":"AC","evidence":"first","suggestion":"s"}`)
	_, _, err = h.ValidateCompletion(context.Background(), nil, completionCallArgs(pre.SessionID))
	require.NoError(t, err)

	rv.resp = providers.Response{
		RawJSON: []byte(`{"verdict":"warn","findings":[` +
			`{"severity":"major","category":"quality","criterion":"AC","evidence":"second","suggestion":"s"},` +
			`{"severity":"minor","cat`),
		Model: "claude-opus-4-7",
	}
	rv.err = providers.ErrResponseTruncated
	_, env, err := h.ValidateCompletion(context.Background(), nil, completionCallArgs(pre.SessionID))
	require.NoError(t, err)
	require.True(t, env.Partial)

	sess, ok := h.deps.Sessions.Get(pre.SessionID)
	require.True(t, ok)
	assert.Contains(t, evidenceOf(sess.PostFindings), "first")
	assert.NotContains(t, evidenceOf(sess.PostFindings), "second")
}

const truncationDelay = 20 * time.Millisecond

func TestValidateTaskSpec_TruncatedReviewReportsElapsedTime(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated, delay: truncationDelay}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, env.ReviewMS, truncationDelay.Milliseconds())
}

func TestReviewPlanSingle_TruncationReportsElapsedTime(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated, delay: truncationDelay}
	h := &handlers{deps: newDeps(t, rv)}
	_, _, ms, _, err := h.reviewPlanSingle(context.Background(), h.deps.Cfg.PlanModel, prompts.Output{System: "s", User: "u"}, 100)
	require.ErrorIs(t, err, providers.ErrResponseTruncated)
	assert.GreaterOrEqual(t, ms, truncationDelay.Milliseconds())
}

func TestReviewPlanChunked_PassOneTruncationReportsElapsedTime(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated, delay: truncationDelay}
	h := &handlers{deps: newDeps(t, rv)}
	rendered := renderedPlanReview{FindingsOnly: &prompts.Output{System: "s", User: "u"}}
	_, _, ms, _, err := h.reviewPlanChunked(context.Background(), h.deps.Cfg.PlanModel, rendered, 100)
	require.ErrorIs(t, err, providers.ErrResponseTruncated)
	assert.GreaterOrEqual(t, ms, truncationDelay.Milliseconds())
}
