package mcpsrv

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// findingObj is a per-task reviewer finding object literal. An empty sameAs
// renders null.
func findingObj(severity, category, criterion, evidence, sameAs string) string {
	sa := "null"
	if sameAs != "" {
		sa = `"` + sameAs + `"`
	}
	return `{"severity":"` + severity + `","category":"` + category + `","criterion":"` + criterion +
		`","evidence":"` + evidence + `","suggestion":"s","same_as":` + sa + `}`
}

func newRulingsHandlers(t *testing.T) (*handlers, *fakeReviewer) {
	t.Helper()
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	return &handlers{deps: newDeps(t, rv)}, rv
}

// startTask runs a passing validate_task_spec and returns its session id.
func startTask(t *testing.T, h *handlers, rv *fakeReviewer) string {
	t.Helper()
	rv.resp = passResp("claude-sonnet-4-6")
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC 1"}})
	require.NoError(t, err)
	return pre.SessionID
}

// completeWith runs validate_completion with the reviewer answering resp.
func completeWith(t *testing.T, h *handlers, rv *fakeReviewer, args ValidateCompletionArgs, resp providers.Response) Envelope {
	t.Helper()
	rv.resp = resp
	_, env, err := h.ValidateCompletion(context.Background(), nil, args)
	require.NoError(t, err)
	return env
}

const driftFinding = `{"severity":"major","category":"scope_drift","criterion":"AC 1","evidence":"wires the dispatcher","suggestion":"s","same_as":null}`

func TestValidateCompletion_PriorFindingsAndAnswersReachThePrompt(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	args := completionCallArgs(sid)
	args.FindingResponses = []FindingResponseArg{{FindingID: id, Response: "Task 7 owns the dispatcher wiring"}}
	completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	assert.Contains(t, rv.LastRequest.User, "## Prior findings")
	assert.Contains(t, rv.LastRequest.User, "- ID: "+id)
	assert.Contains(t, rv.LastRequest.User, "Task 7 owns the dispatcher wiring")
}

func TestValidateCompletion_TheLastAnswerToAnIDWins(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	args := completionCallArgs(sid)
	args.FindingResponses = []FindingResponseArg{{FindingID: id, Response: "first answer"}, {FindingID: id, Response: "second answer"}}
	completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	assert.Contains(t, rv.LastRequest.User, "second answer")
	assert.NotContains(t, rv.LastRequest.User, "first answer")
}

func TestValidateCompletion_AnsweredMajorRepeatEscalates(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	args := completionCallArgs(sid)
	args.FindingResponses = []FindingResponseArg{{FindingID: id, Response: "Task 7 owns it"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(findingObj("major", "scope_drift", "ac 1.", "still wires it", "")))

	require.Len(t, env.Findings, 1)
	assert.Equal(t, id, env.Findings[0].RepeatOf)
	assert.True(t, env.Escalate)
	assert.True(t, strings.HasPrefix(env.NextAction,
		"Stop resubmitting: report "+id+" and your responses to your controller for a ruling, then resubmit with controller_rulings."), env.NextAction)
	assert.Contains(t, env.SummaryBlock, "escalate:      true")

	st, _ := h.deps.Sessions.ReviewState(sid)
	assert.True(t, st.Escalated)
}

func TestValidateCompletion_RepeatBySameAsAcrossCategories(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	args := completionCallArgs(sid)
	args.FindingResponses = []FindingResponseArg{{FindingID: id, Response: "Task 7 owns it"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(findingObj("major", "missing_acceptance_criterion", "AC 1 is not met", "no route", id)))

	require.Len(t, env.Findings, 1)
	assert.Equal(t, id, env.Findings[0].RepeatOf)
	assert.True(t, env.Escalate)
	assert.Nil(t, env.Findings[0].SameAs)
}

func TestValidateCompletion_OnlyAnAnsweredCriticalOrMajorRepeatEscalates(t *testing.T) {
	t.Run("unanswered", func(t *testing.T) {
		h, rv := newRulingsHandlers(t)
		sid := startTask(t, h, rv)
		completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding))
		env := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding))
		assert.Empty(t, env.Findings[0].RepeatOf, "nobody disputed it, so it is not a repeat")
		assert.False(t, env.Escalate)
	})
	t.Run("minor", func(t *testing.T) {
		h, rv := newRulingsHandlers(t)
		sid := startTask(t, h, rv)
		nit := findingObj("minor", "quality", "nit", "naming", "")
		id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(nit)).Findings[0].ID
		args := completionCallArgs(sid)
		args.FindingResponses = []FindingResponseArg{{FindingID: id, Response: "intended"}}
		env := completeWith(t, h, rv, args, reviewerFindingsResp(nit))
		assert.Equal(t, id, env.Findings[0].RepeatOf)
		assert.False(t, env.Escalate)
	})
}

func TestValidateCompletion_ASameAsNamingAnUnshownIDIsIgnored(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	args := completionCallArgs(sid)
	args.FindingResponses = []FindingResponseArg{{FindingID: id, Response: "answered"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(findingObj("major", "quality", "something else", "two", "f_99999999")))

	require.Len(t, env.Findings, 1)
	assert.Empty(t, env.Findings[0].RepeatOf)
	assert.False(t, env.Escalate)
}

func TestValidateCompletion_EscalationReplacesTheResubmitInstruction(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	gap := findingObj("major", "insufficient_evidence", "AC 1", "no test covers it", "")
	first := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(gap))
	require.True(t, first.SubmissionDefectOnly)

	args := completionCallArgs(sid)
	args.FindingResponses = []FindingResponseArg{{FindingID: first.Findings[0].ID, Response: "TestHealth covers it"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(gap))

	assert.True(t, env.Escalate)
	assert.False(t, env.SubmissionDefectOnly)
	assert.True(t, strings.HasPrefix(env.NextAction, "Stop resubmitting"), env.NextAction)
	assert.NotContains(t, env.NextAction, "Re-submit with the missing evidence")
}

func TestValidateCompletion_RulingWaivesByFingerprintAndPersists(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	args := completionCallArgs(sid)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: id, Ruling: "Task 7 owns the dispatcher wiring"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(driftFinding))

	assert.Empty(t, env.Findings)
	assert.Equal(t, "pass", env.Verdict)
	require.Len(t, env.WaivedFindings, 1)
	assert.Equal(t, id, env.WaivedFindings[0].ID)
	assert.Equal(t, "Task 7 owns the dispatcher wiring", env.WaivedFindings[0].Ruling)
	assert.Equal(t, "wires the dispatcher", env.WaivedFindings[0].Evidence)
	assert.Contains(t, env.SummaryBlock, "waived: "+id+" major/scope_drift")

	again := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding))
	assert.Empty(t, again.Findings, "a ruling persists without being resent")
	require.Len(t, again.WaivedFindings, 1)
	assert.Contains(t, rv.LastRequest.User, "## Controller rulings (authoritative)")
	assert.Contains(t, rv.LastRequest.User, `- `+id+` (scope_drift on "AC 1"): Task 7 owns the dispatcher wiring`)
}

func TestValidateCompletion_RulingOnASuffixedIDCoversEveryFindingWithItsFingerprint(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	a := findingObj("minor", "quality", "comment_hygiene", "stale comment in a.go", "")
	b := findingObj("minor", "quality", "comment_hygiene", "stale comment in b.go", "")
	first := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(a, b))
	require.Len(t, first.Findings, 2)
	require.Equal(t, first.Findings[0].ID+"-2", first.Findings[1].ID)

	args := completionCallArgs(sid)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: first.Findings[1].ID, Ruling: "Both comments go in a later task"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(b, a))

	assert.Empty(t, env.Findings)
	assert.Len(t, env.WaivedFindings, 2)
}

func TestValidateCompletion_RulingWaivesAReRaiseUnderAnotherCategoryThroughSameAs(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	args := completionCallArgs(sid)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: id, Ruling: "Task 7 owns it"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(findingObj("major", "missing_acceptance_criterion", "AC 1 is not met", "no route", id)))

	assert.Empty(t, env.Findings)
	require.Len(t, env.WaivedFindings, 1)
	assert.Equal(t, verdict.CategoryMissingAC, env.WaivedFindings[0].Category)
}

func TestValidateCompletion_AServerFindingIsNeverWaived(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	d.Cfg.Codescene = "required"
	h := &handlers{deps: d}
	sid := startTask(t, h, rv)

	first := completeWith(t, h, rv, completionCallArgs(sid), passResp("claude-opus-4-7"))
	var csID string
	for _, f := range first.Findings {
		if f.Category == verdict.CategoryCodesceneNotRun {
			csID = f.ID
		}
	}
	require.NotEmpty(t, csID)

	args := completionCallArgs(sid)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: csID, Ruling: "CodeScene is not set up here"}}
	env := completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	assert.True(t, hasCategory(env.Findings, verdict.CategoryCodesceneNotRun))
	assert.Empty(t, env.WaivedFindings)
}

func TestValidateCompletion_UnknownIDsDrawOneAdvisoryEachAndKeepTheVerdict(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)

	args := completionCallArgs(sid)
	args.FindingResponses = []FindingResponseArg{{FindingID: "f_00000000", Response: "a"}, {FindingID: "f_11111111", Response: "b"}}
	args.ControllerRulings = []ControllerRulingArg{{FindingID: "f_22222222", Ruling: "r"}}
	env := completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	assert.Equal(t, "pass", env.Verdict)
	var responses, rulings int
	for _, f := range env.Findings {
		switch f.Criterion {
		case "finding_responses":
			responses++
			assert.Contains(t, f.Evidence, "f_00000000, f_11111111")
		case "controller_rulings":
			rulings++
			assert.Contains(t, f.Evidence, "f_22222222")
		}
	}
	assert.Equal(t, 1, responses)
	assert.Equal(t, 1, rulings)

	st, _ := h.deps.Sessions.ReviewState(sid)
	assert.Empty(t, st.Rulings)
}

func TestValidateCompletion_RulingsPastTheSessionCapAreIgnoredWithAnAdvisory(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	full := map[string]session.Ruling{}
	for i := 0; i < session.MaxRulings; i++ {
		fp := fmt.Sprintf("f_%08x", i)
		full[fp] = session.Ruling{ID: fp, Text: "r"}
	}
	require.True(t, h.deps.Sessions.ApplyReview(sid, session.ReviewUpdate{Rulings: full, IssuedIDs: []string{"f_ffffffff"}}))

	args := completionCallArgs(sid)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: "f_ffffffff", Ruling: "one too many"}}
	env := completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	var advised bool
	for _, f := range env.Findings {
		if f.Criterion == "controller_rulings" && strings.Contains(f.Evidence, "f_ffffffff") && strings.Contains(f.Evidence, "50") {
			advised = true
		}
	}
	assert.True(t, advised, "%+v", env.Findings)
	st, _ := h.deps.Sessions.ReviewState(sid)
	assert.Len(t, st.Rulings, session.MaxRulings)
}

func TestValidateCompletion_TruncatedReviewWritesRulingsButKeepsPriorFindings(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	args := completionCallArgs(sid)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: id, Ruling: "Task 7 owns it"}}
	rv.err = providers.ErrResponseTruncated
	env := completeWith(t, h, rv, args, providers.Response{
		RawJSON: []byte(`{"verdict":"warn","findings":[` + driftFinding + `,{"severity":"minor","cat`),
		Model:   "claude-opus-4-7",
	})
	rv.err = nil

	require.True(t, env.Partial)
	require.Len(t, env.WaivedFindings, 1, "a truncated review still applies rulings")
	st, _ := h.deps.Sessions.ReviewState(sid)
	require.Len(t, st.PriorFindings, 1)
	assert.Equal(t, id, st.PriorFindings[0].ID, "the truncated review did not replace the prior findings")
	assert.Contains(t, st.Rulings, verdict.BaseID(id))
}

func TestValidateCompletion_ACallRejectedBeforeReviewWritesNoRulings(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	h.deps.Cfg.MaxPayloadBytes = 10
	args := completionCallArgs(sid)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: id, Ruling: "r"}}
	env := completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	require.True(t, hasCategory(env.Findings, verdict.CategoryTooLarge))
	for _, f := range env.Findings {
		assert.NotEqual(t, "controller_rulings", f.Criterion)
	}
	st, _ := h.deps.Sessions.ReviewState(sid)
	assert.Empty(t, st.Rulings)
}

func TestValidateCompletion_WithoutASessionIgnoresAnswersAndRulings(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	args := completionCallArgs("")
	args.FindingResponses = []FindingResponseArg{{FindingID: "f_0123abcd", Response: "a"}}
	env := completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	require.NotEmpty(t, env.Findings)
	assert.Equal(t, "session_id", env.Findings[len(env.Findings)-1].Criterion)
	assert.Equal(t, "pass", env.Verdict)
}

func TestValidateCompletion_ARuledPreTaskFindingLeavesThePrompt(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	rv.resp = reviewerFindingsResp(findingObj("major", "ambiguous_spec", "AC 1", "which load profile", ""))
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC 1"}})
	require.NoError(t, err)
	preID := pre.Findings[0].ID

	args := completionCallArgs(pre.SessionID)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: preID, Ruling: "Load profile is out of scope"}}
	completeWith(t, h, rv, args, passResp("claude-opus-4-7"))

	assert.NotContains(t, rv.LastRequest.User, "## Major pre-task findings to verify")
	assert.Contains(t, rv.LastRequest.User, `- `+preID+` (ambiguous_spec on "AC 1"): Load profile is out of scope`)
}

func TestPriorFindings_ListsEachFingerprintOnceAndLeavesOutRuledOnes(t *testing.T) {
	a := verdict.Finding{ID: verdict.Fingerprint(verdict.CategoryQuality, "", "x"), Severity: verdict.SeverityMinor,
		Category: verdict.CategoryQuality, Criterion: "x", Evidence: "pre-task"}
	aAgain := a
	aAgain.Evidence = "checkpoint 1"
	b := verdict.Finding{ID: verdict.Fingerprint(verdict.CategoryScopeDrift, "", "AC 1"), Severity: verdict.SeverityMajor,
		Category: verdict.CategoryScopeDrift, Criterion: "AC 1", Evidence: "drift"}
	state := session.ReviewState{
		PreFindings:        []verdict.Finding{a},
		CheckpointFindings: [][]verdict.Finding{{aAgain, b}},
	}

	got := priorFindings(state)
	require.Len(t, got, 2)
	assert.Equal(t, "checkpoint 1", got[0].Evidence, "only the most recent call's copy is listed")
	assert.Equal(t, "drift", got[1].Evidence)

	state.Rulings = map[string]session.Ruling{verdict.BaseID(b.ID): {ID: b.ID, Text: "r"}}
	got = priorFindings(state)
	require.Len(t, got, 1)
	assert.Equal(t, "checkpoint 1", got[0].Evidence)
}

func TestCheckProgress_RendersRulingsAndLeavesRuledFindingsOut(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	rv.resp = reviewerFindingsResp(findingObj("major", "ambiguous_spec", "AC 1", "which load profile", ""))
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC 1"}})
	require.NoError(t, err)
	preID := pre.Findings[0].ID
	require.True(t, h.deps.Sessions.ApplyReview(pre.SessionID, session.ReviewUpdate{Rulings: map[string]session.Ruling{
		verdict.BaseID(preID): {ID: preID, Category: verdict.CategoryAmbiguousSpec, Criterion: "AC 1", Text: "Load profile is out of scope"},
	}}))

	rv.resp = passResp("claude-haiku-4-5-20251001")
	_, _, err = h.CheckProgress(context.Background(), nil, CheckProgressArgs{SessionID: pre.SessionID, WorkingOn: "x"})
	require.NoError(t, err)
	assert.NotContains(t, rv.LastRequest.User, "which load profile")
	assert.Contains(t, rv.LastRequest.User, "## Controller rulings (authoritative)")
}

// lockedReviewer is a reviewer safe for concurrent calls, unlike fakeReviewer.
type lockedReviewer struct {
	mu   sync.Mutex
	resp providers.Response
}

func (l *lockedReviewer) Name() string { return "anthropic" }

func (l *lockedReviewer) Review(context.Context, providers.Request) (providers.Response, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.resp, nil
}

func TestValidateCompletion_ConcurrentCallsKeepEachOthersRulings(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	first := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(
		findingObj("major", "scope_drift", "AC 1", "one", ""),
		findingObj("major", "quality", "AC 1", "two", ""),
	))
	require.Len(t, first.Findings, 2)

	h.deps.Reviews = providers.Registry{"anthropic": &lockedReviewer{resp: passResp("claude-opus-4-7")}}
	var wg sync.WaitGroup
	for _, f := range first.Findings {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			args := completionCallArgs(sid)
			args.ControllerRulings = []ControllerRulingArg{{FindingID: id, Ruling: "ruled " + id}}
			_, _, err := h.ValidateCompletion(context.Background(), nil, args)
			assert.NoError(t, err)
		}(f.ID)
	}
	wg.Wait()

	st, _ := h.deps.Sessions.ReviewState(sid)
	assert.Len(t, st.Rulings, 2)
}

// TestValidateCompletionAndCheckProgress_ConcurrentCallsDoNotRace guards
// against reading a session's pre-task or checkpoint findings off the live
// *session.Session concurrently with check_progress's AppendCheckpoint, which
// mutates that same slice under the store's lock. Both loops read only
// through session.ReviewState, a locked copy, so this must pass under -race.
func TestValidateCompletionAndCheckProgress_ConcurrentCallsDoNotRace(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)

	h.deps.Reviews = providers.Registry{"anthropic": &lockedReviewer{resp: passResp("claude-opus-4-7")}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_, _, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{SessionID: sid, WorkingOn: "x"})
			assert.NoError(t, err)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_, _, err := h.ValidateCompletion(context.Background(), nil, completionCallArgs(sid))
			assert.NoError(t, err)
		}
	}()
	wg.Wait()
}
