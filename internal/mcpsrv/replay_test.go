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
		ValidateCompletion: &ValidateCompletionArgs{SessionID: "ignored", Summary: "done", FinalDiff: replayTestDiff},
		Expectations: []replayExpectation{
			{Call: replayCallCompletion, AnyOfKeywords: []string{"HANDLERETIRED"}},
			{Call: replayCallCompletion, AnyOfKeywords: []string{"scope_drift"}},
			{Call: replayCallTaskSpec, AnyOfKeywords: []string{"anything"}},
		},
	}

	report := runReplayFixture(context.Background(), cfg, providers.Registry{"anthropic": sr}, fx, 2)

	require.Equal(t, 4, sr.calls)
	assert.Contains(t, sr.requests[1].User, "Title: T", "validate_completion runs on the session validate_task_spec opened")
	assert.Equal(t, 1, report.Expectations[0].Matched)
	assert.Equal(t, []string{"run 1: quality stale_comments: f.go:3 still names handleRetired"}, report.Expectations[0].Matches)
	assert.Equal(t, 1, report.Expectations[1].Matched)
	assert.Equal(t, 0, report.Expectations[2].Matched)
	completion := report.Calls[replayCallCompletion]
	assert.Equal(t, map[string]int{"pass": 1, "warn": 1}, completion.Verdicts)
	assert.Equal(t, map[string]int{`major scope_drift "AC 1"`: 1}, completion.Blocking)
	assert.Positive(t, completion.PromptBytes)
	assert.Contains(t, report.String(), `validate_completion ["HANDLERETIRED"]: 1/2`)
}

func TestRunReplayFixture_SkipsTheCompletionWhenTheTaskSpecOpensNoSession(t *testing.T) {
	sr := &scriptedReviewer{}
	cfg := newDeps(t, &fakeReviewer{name: "anthropic"}).Cfg
	fx := replayFixture{
		Name:               "broken",
		ValidateTaskSpec:   &ValidateTaskSpecArgs{Goal: "G"},
		ValidateCompletion: &ValidateCompletionArgs{Summary: "s", FinalDiff: replayTestDiff},
	}

	report := runReplayFixture(context.Background(), cfg, providers.Registry{"anthropic": sr}, fx, 1)

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

	report := runReplayFixture(context.Background(), cfg, providers.Registry{"anthropic": replayDryRunReviewer{name: "anthropic"}}, fx, 3)

	assert.Equal(t, 0, report.Expectations[0].Matched)
	assert.Equal(t, map[string]int{"pass": 3}, report.Calls[replayCallTaskSpec].Verdicts)
	assert.Greater(t, report.Calls[replayCallTaskSpec].PromptBytes, 1000)
}
