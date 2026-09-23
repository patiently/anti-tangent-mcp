package mcpsrv

import (
	"context"
	"strconv"
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

// truncateThenPass fails every odd-numbered call with ErrResponseTruncated
// and answers every even-numbered call normally, so a test can tell an
// automatic retry from its absence — including across two separate handler
// calls sharing one instance (each handler's own first attempt truncates,
// its retry passes), not only the instance's very first call ever. delay, when
// set, is slept on every call, so a test can assert the reported ReviewMS
// covers every attempt made, not just the last one.
type truncateThenPass struct {
	name     string
	calls    int
	maxToken []int
	delay    time.Duration
}

func (r *truncateThenPass) Name() string { return r.name }
func (r *truncateThenPass) Review(_ context.Context, req providers.Request) (providers.Response, error) {
	r.calls++
	r.maxToken = append(r.maxToken, req.MaxTokens)
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	if r.calls%2 == 1 {
		return providers.Response{}, providers.ErrResponseTruncated
	}
	return passResp("claude-opus-4-7"), nil
}

func TestValidateTaskSpec_TruncationRetriesOnceAtTheCeiling(t *testing.T) {
	rv := &truncateThenPass{name: "anthropic"}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	assert.Equal(t, 2, rv.calls, "one automatic retry")
	assert.Equal(t, h.deps.Cfg.MaxTokensCeiling, rv.maxToken[1], "the retry uses the ceiling")
	assert.Equal(t, "pass", env.Verdict, "the retry's result is the result")
	assert.NotEmpty(t, env.SessionID, "a recovered review still opens a session")
}

func TestValidateTaskSpec_AnOverriddenBudgetDoesNotRetry(t *testing.T) {
	rv := &truncateThenPass{name: "anthropic"}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", MaxTokensOverride: 1024,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, rv.calls, "the caller chose the budget; the server does not overrule it")
	assert.True(t, hasCriterion(env.Findings, "reviewer_response"))
	assert.Contains(t, findingWithCriterion(env.Findings, "reviewer_response").Suggestion,
		strconv.Itoa(h.deps.Cfg.MaxTokensCeiling), "the suggestion names the budget to pass")
}

// TestValidateTaskSpec_RetryReviewMSCoversBothAttempts pins that the ReviewMS
// the caller sees is the sum of the truncated first attempt and the retry,
// not just the retry's own elapsed time — runReview accumulates `ms` across
// both h.review calls (see review_error.go).
func TestValidateTaskSpec_RetryReviewMSCoversBothAttempts(t *testing.T) {
	rv := &truncateThenPass{name: "anthropic", delay: truncationDelay}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	assert.Equal(t, 2, rv.calls, "one automatic retry")
	// A lower bound with margin, not an exact duration: two sleeps of
	// truncationDelay must both be reflected, so anything below 2x (minus a
	// little slack for scheduler jitter) means only the last attempt's time
	// was reported.
	assert.GreaterOrEqual(t, env.ReviewMS, 2*truncationDelay.Milliseconds()-5,
		"ReviewMS must cover both attempts, not just the last one")
}

// TestValidateTaskSpec_BudgetAtCeilingNeverRetries pins the other half of
// perTaskRetryBudget: a per-task budget already at the ceiling has nothing to
// raise, so no automatic retry is attempted even though nothing was
// caller-overridden.
func TestValidateTaskSpec_BudgetAtCeilingNeverRetries(t *testing.T) {
	rv := &truncateThenPass{name: "anthropic"}
	d := newDeps(t, rv)
	d.Cfg.PerTaskMaxTokens = d.Cfg.MaxTokensCeiling
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	assert.Equal(t, 1, rv.calls, "the configured budget is already the ceiling; there is nothing to retry at")
	assert.Equal(t, "warn", env.Verdict)
}

// TestValidateTaskSpec_AlwaysTruncatingReviewerRetriesOnlyOnce pins that the
// automatic retry fires at most once per call: a reviewer that truncates on
// every attempt, retry included, must never be re-called a third time.
func TestValidateTaskSpec_AlwaysTruncatingReviewerRetriesOnlyOnce(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	assert.Equal(t, 2, rv.Calls, "the retry is attempted exactly once, even when it also truncates")
	assert.Equal(t, "warn", env.Verdict)
}

func TestValidateCompletion_TruncationRetriesOnceAtTheCeiling(t *testing.T) {
	rv := &truncateThenPass{name: "anthropic"}
	h := &handlers{deps: newDeps(t, rv)}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	before := rv.calls
	_, env, err := h.ValidateCompletion(context.Background(), nil, completionCallArgs(pre.SessionID))
	require.NoError(t, err)
	assert.Equal(t, before+2, rv.calls)
	assert.Equal(t, "pass", env.Verdict)
}

// findingWithCriterion returns the first finding with the given criterion, or
// the zero value if none matches.
func findingWithCriterion(fs []verdict.Finding, criterion string) verdict.Finding {
	for _, f := range fs {
		if f.Criterion == criterion {
			return f
		}
	}
	return verdict.Finding{}
}
