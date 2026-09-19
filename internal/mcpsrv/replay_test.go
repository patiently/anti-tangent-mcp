package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

func writeReplayFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
}

func TestLoadReplayFixtures_LoadsInNameOrderAndIgnoresUnknownArguments(t *testing.T) {
	dir := t.TempDir()
	writeReplayFixture(t, dir, "b.json", `{"validate_completion":{"summary":"s","final_diff":"d","an_argument_this_server_lacks":true},
		"expectations":[{"call":"validate_completion","any_of_keywords":["stale"]}]}`)
	writeReplayFixture(t, dir, "a.json", `{"name":"gate","validate_task_spec":{"task_title":"T","goal":"G"}}`)
	writeReplayFixture(t, dir, "notes.txt", "not a fixture")

	fixtures, err := loadReplayFixtures(dir)
	require.NoError(t, err)
	require.Len(t, fixtures, 2)
	assert.Equal(t, "gate", fixtures[0].Name)
	assert.Equal(t, "b", fixtures[1].Name)
	assert.Equal(t, "d", fixtures[1].ValidateCompletion.FinalDiff)
}

func TestLoadReplayFixtures_RejectsAnExpectationOnACallTheFixtureLacks(t *testing.T) {
	dir := t.TempDir()
	writeReplayFixture(t, dir, "x.json", `{"validate_task_spec":{"task_title":"T","goal":"G"},
		"expectations":[{"call":"validate_completion","any_of_keywords":["stale"]}]}`)
	_, err := loadReplayFixtures(dir)
	require.ErrorContains(t, err, `expectations[0] names validate_completion, which fixture "x" does not record`)
}

func TestFilterReplayFixtures(t *testing.T) {
	fixtures := []replayFixture{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	kept, err := filterReplayFixtures(fixtures, " c, a ")
	require.NoError(t, err)
	assert.Equal(t, []replayFixture{{Name: "a"}, {Name: "c"}}, kept)

	all, err := filterReplayFixtures(fixtures, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	_, err = filterReplayFixtures(fixtures, "a,zzz")
	require.ErrorContains(t, err, "zzz")
}

const replayTestDiff = "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n@@ -1 +1 @@\n-x := 1\n+x := 2\n"

func TestRunReplayFixture_TalliesEachExpectationAcrossRuns(t *testing.T) {
	stale := reviewerFindingsResp(findingObj("minor", "quality", "stale_comments", "f.go:3 still names handleRetired", ""))
	blocking := reviewerFindingsResp(findingObj("major", "scope_drift", "AC 1", "wires the dispatcher", ""))
	sr := &scriptedReviewer{responses: []providers.Response{
		passResp("claude-sonnet-4-6"), stale,
		passResp("claude-sonnet-4-6"), blocking,
	}}
	cfg := newDeps(t, &fakeReviewer{name: "anthropic"}).Cfg
	fx := replayFixture{
		Name:               "stale",
		ValidateTaskSpec:   &ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC 1"}},
		ValidateCompletion: &ValidateCompletionArgs{SessionID: "ignored", Summary: "done", FinalDiff: replayTestDiff, RepoRoot: "relative/path"},
		Expectations: []replayExpectation{
			{Call: replayCallCompletion, AnyOfKeywords: []string{"HANDLERETIRED"}},
			{Call: replayCallCompletion, AnyOfKeywords: []string{"scope_drift"}},
			{Call: replayCallTaskSpec, AnyOfKeywords: []string{"anything"}},
		},
	}

	report := runReplayFixture(context.Background(), newReplayEnv(cfg, providers.Registry{"anthropic": sr}), fx, 2)

	require.Equal(t, 4, sr.calls)
	assert.Contains(t, sr.requests[1].User, "Title: T", "validate_completion runs on the session validate_task_spec opened")
	assert.Equal(t, 1, report.Expectations[0].Matched)
	assert.Equal(t, []string{"run 1: quality stale_comments: f.go:3 still names handleRetired"}, report.Expectations[0].Matches)
	assert.Equal(t, 1, report.Expectations[1].Matched)
	assert.Equal(t, 0, report.Expectations[2].Matched)
	completion := report.Calls[replayCallCompletion]
	assert.Equal(t, map[string]int{"pass": 1, "warn": 1}, completion.Verdicts)
	assert.Equal(t, map[string]int{`major scope_drift "AC 1"`: 1}, completion.Blocking)
	assert.Equal(t, map[string]int{"repo_root": 2}, completion.Advisories, "the unusable repo_root advisory rides every run, unrelated to the reviewer's own findings")
	assert.Positive(t, completion.PromptBytes)
	assert.Contains(t, report.String(), `validate_completion ["HANDLERETIRED"]: 1/2`)
	assert.Contains(t, report.String(), "advisory repo_root in 2/2")
}

// TestRunReplayFixture_CriterionRestrictsTheMatch: an expectation's keywords
// can also appear in an unrelated, earlier finding (here a
// missing_acceptance_criterion finding whose evidence names "CursorStore").
// Without a Criterion the first keyword match is recorded; with Criterion
// "over_building" only a finding with that criterion can meet the
// expectation, so the later over_building finding is recorded instead.
func TestRunReplayFixture_CriterionRestrictsTheMatch(t *testing.T) {
	unrelated := findingObj("major", "missing_acceptance_criterion", "spec", "CursorStore is not wired to the poller", "")
	overBuilding := findingObj("minor", "quality", "over_building", "CursorStore is an unrequested interface", "")
	sr := &scriptedReviewer{responses: []providers.Response{reviewerFindingsResp(unrelated, overBuilding)}}
	cfg := newDeps(t, &fakeReviewer{name: "anthropic"}).Cfg
	fx := replayFixture{
		Name: "criterion",
		ValidateCompletion: &ValidateCompletionArgs{
			Summary:   "done",
			FinalDiff: replayTestDiff,
			RepoRoot:  "relative/path", // resolveDirInput rejects a relative repo_root
		},
		Expectations: []replayExpectation{
			{Call: replayCallCompletion, AnyOfKeywords: []string{"CursorStore"}},
			{Call: replayCallCompletion, AnyOfKeywords: []string{"CursorStore"}, Criterion: "over_building"},
		},
	}

	report := runReplayFixture(context.Background(), newReplayEnv(cfg, providers.Registry{"anthropic": sr}), fx, 1)

	require.Equal(t, 1, sr.calls)
	assert.Equal(t, 1, report.Expectations[0].Matched)
	assert.Equal(t, []string{"run 1: missing_acceptance_criterion spec: CursorStore is not wired to the poller"}, report.Expectations[0].Matches, "without Criterion, the first keyword match wins")
	assert.Equal(t, 1, report.Expectations[1].Matched)
	assert.Equal(t, []string{"run 1: quality over_building: CursorStore is an unrequested interface"}, report.Expectations[1].Matches, "with Criterion set, only the over_building finding can meet the expectation")
}

// TestRunReplayFixture_MatchesOnlyTheReviewersOwnFindings pins A1: a keyword
// that only appears in a server-added advisory (never in anything the
// reviewer itself said) must not count as a match, or the replay's recall
// number would be inflated by the server's own text rather than the
// reviewer's.
func TestRunReplayFixture_MatchesOnlyTheReviewersOwnFindings(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{reviewerFindingsResp()}}
	cfg := newDeps(t, &fakeReviewer{name: "anthropic"}).Cfg
	fx := replayFixture{
		Name: "repo-root-advisory",
		ValidateCompletion: &ValidateCompletionArgs{
			Summary:   "done",
			FinalDiff: replayTestDiff,
			RepoRoot:  "relative/path", // resolveDirInput rejects a relative repo_root
		},
		Expectations: []replayExpectation{
			{Call: replayCallCompletion, AnyOfKeywords: []string{"repo_root"}},
		},
	}

	report := runReplayFixture(context.Background(), newReplayEnv(cfg, providers.Registry{"anthropic": sr}), fx, 1)

	require.Equal(t, 1, sr.calls)
	completion := report.Calls[replayCallCompletion]
	require.Equal(t, map[string]int{"repo_root": 1}, completion.Advisories, "the envelope does carry the repo_root advisory")
	assert.Equal(t, 0, report.Expectations[0].Matched, "the advisory is not a reviewer finding, so it must not match")
}

// TestRunReplayFixture_CompletionOnlyFixtureIgnoresATranscriptSessionID pins
// A3: a completion-only fixture runs lightweight even when it carries a
// session_id copied from a recorded transcript, since no store in this run
// ever opened that session.
func TestRunReplayFixture_CompletionOnlyFixtureIgnoresATranscriptSessionID(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{passResp("claude-sonnet-4-6")}}
	cfg := newDeps(t, &fakeReviewer{name: "anthropic"}).Cfg
	fx := replayFixture{
		Name: "completion-only",
		ValidateCompletion: &ValidateCompletionArgs{
			SessionID: "s_from_transcript",
			Summary:   "done",
			FinalDiff: replayTestDiff,
		},
	}

	report := runReplayFixture(context.Background(), newReplayEnv(cfg, providers.Registry{"anthropic": sr}), fx, 1)

	assert.Equal(t, 1, sr.calls)
	completion := report.Calls[replayCallCompletion]
	require.NotNil(t, completion)
	assert.Empty(t, completion.Errors)
	assert.Equal(t, map[string]int{"pass": 1}, completion.Verdicts)
}

func TestRunReplayFixture_SkipsTheCompletionWhenTheTaskSpecOpensNoSession(t *testing.T) {
	sr := &scriptedReviewer{}
	cfg := newDeps(t, &fakeReviewer{name: "anthropic"}).Cfg
	fx := replayFixture{
		Name:               "broken",
		ValidateTaskSpec:   &ValidateTaskSpecArgs{Goal: "G"},
		ValidateCompletion: &ValidateCompletionArgs{Summary: "s", FinalDiff: replayTestDiff},
	}

	report := runReplayFixture(context.Background(), newReplayEnv(cfg, providers.Registry{"anthropic": sr}), fx, 1)

	assert.Equal(t, 0, sr.calls)
	assert.Equal(t, []string{"task_title and goal are required"}, report.Calls[replayCallTaskSpec].Errors)
	assert.Equal(t, []string{"skipped: validate_task_spec opened no session"}, report.Calls[replayCallCompletion].Errors)
}

func TestRunReplayFixture_ADryRunMeasuresPromptsWithoutFindings(t *testing.T) {
	cfg := newDeps(t, &fakeReviewer{name: "anthropic"}).Cfg
	fx := replayFixture{
		Name:             "gate",
		ValidateTaskSpec: &ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"},
		Expectations:     []replayExpectation{{Call: replayCallTaskSpec, AnyOfKeywords: []string{"non-goal"}}},
	}

	report := runReplayFixture(context.Background(), newReplayEnv(cfg, providers.Registry{"anthropic": replayDryRunReviewer{name: "anthropic"}}), fx, 3)

	assert.Equal(t, 0, report.Expectations[0].Matched)
	assert.Equal(t, map[string]int{"pass": 3}, report.Calls[replayCallTaskSpec].Verdicts)
	assert.Greater(t, report.Calls[replayCallTaskSpec].PromptBytes, 1000)
}

func TestLoadReplayFixtures_RejectsADuplicateName(t *testing.T) {
	dir := t.TempDir()
	writeReplayFixture(t, dir, "a.json", `{"name":"same","validate_task_spec":{"task_title":"T","goal":"G"}}`)
	writeReplayFixture(t, dir, "b.json", `{"name":"same","validate_task_spec":{"task_title":"T","goal":"G"}}`)

	_, err := loadReplayFixtures(dir)

	require.ErrorContains(t, err, `fixture name "same" is already used by`)
}

func TestLoadReplayFixtures_RejectsBlankKeywords(t *testing.T) {
	dir := t.TempDir()
	writeReplayFixture(t, dir, "x.json", `{"validate_completion":{"summary":"s","final_diff":"d"},
		"expectations":[{"call":"validate_completion","any_of_keywords":["", "  "]}]}`)

	_, err := loadReplayFixtures(dir)

	require.ErrorContains(t, err, "expectations[0] has no any_of_keywords that are not blank")
}

func TestLoadReplayFixtures_LeanFixtures(t *testing.T) {
	fixtures, err := loadReplayFixtures("testdata/replay/lean")
	require.NoError(t, err)
	require.Len(t, fixtures, 2)

	names := []string{fixtures[0].Name, fixtures[1].Name}
	assert.ElementsMatch(t, []string{"lean", "over-built"}, names)

	for _, fx := range fixtures {
		require.NotNil(t, fx.ValidateTaskSpec, "fixture %q", fx.Name)
		require.NotNil(t, fx.ValidateCompletion, "fixture %q", fx.Name)
		require.NotEmpty(t, fx.Expectations, "fixture %q", fx.Name)
		for _, e := range fx.Expectations {
			assert.Equal(t, replayCallCompletion, e.Call, "fixture %q", fx.Name)
			assert.Equal(t, "over_building", e.Criterion, "fixture %q", fx.Name)
		}
	}
}
