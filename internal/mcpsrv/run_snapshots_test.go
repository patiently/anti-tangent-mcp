package mcpsrv

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

// scriptedRunReviewer answers each Review call with the next entry of resps,
// repeating the last one for any call beyond its length. validate_plan's
// PlanResult JSON shape differs from the plain Result shape the other three
// hooks parse, so the fixed single-response fakeReviewer cannot drive all
// four calls of a validate_plan -> validate_task_spec -> check_progress ->
// validate_completion sequence.
type scriptedRunReviewer struct {
	name  string
	resps []providers.Response
	calls int
}

func (s *scriptedRunReviewer) Name() string { return s.name }

func (s *scriptedRunReviewer) Review(_ context.Context, _ providers.Request) (providers.Response, error) {
	i := s.calls
	if i >= len(s.resps) {
		i = len(s.resps) - 1
	}
	s.calls++
	return s.resps[i], nil
}

// runSnapshotPlanResp is the validate_plan reviewer response for a one-task
// plan whose only heading is "Task 1: Secret title ZXQ". The title is
// deliberately unusual so the test can assert it never reaches runs.jsonl.
func runSnapshotPlanResp() providers.Response {
	return providers.Response{
		RawJSON: []byte(`{"plan_verdict":"pass","plan_quality":"actionable","plan_findings":[],` +
			`"tasks":[{"task_index":1,"task_title":"Task 1: Secret title ZXQ","verdict":"pass","findings":[],` +
			`"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Proceed."}`),
		Model: "claude-sonnet-4-6",
	}
}

// runSnapshotSequence drives one validate_plan -> validate_task_spec ->
// check_progress -> validate_completion sequence on h and returns the minted
// plan_run_id.
func runSnapshotSequence(t *testing.T, h *handlers) string {
	t.Helper()

	plan := "# Plan\n\n### Task 1: Secret title ZXQ\n\n**Goal:** g1\n\n**Acceptance criteria:**\n- ac1\n\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.NotEmpty(t, pr.PlanRunID)

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "Task 1: Secret title ZXQ",
		Goal:               "g1",
		AcceptanceCriteria: []string{"ac1"},
		PlanRunID:          pr.PlanRunID,
		TaskIndex:          1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, pre.SessionID)

	_, cp, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID:    pre.SessionID,
		WorkingOn:    "writing handler",
		ChangedFiles: []FileArg{{Path: "h.go", Content: "package h\n"}},
	})
	require.NoError(t, err)
	require.Equal(t, pre.SessionID, cp.SessionID)

	_, _, err = h.ValidateCompletion(context.Background(), nil, completionCallArgs(pre.SessionID))
	require.NoError(t, err)

	return pr.PlanRunID
}

// TestRunSnapshots_LifecycleCallLogAndHeader drives the full task lifecycle
// with stats enabled and asserts the run snapshot's task call log, header
// metadata, and content-freedom on disk.
func TestRunSnapshots_LifecycleCallLogAndHeader(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 100000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	require.NoError(t, err)

	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)

	rv := &scriptedRunReviewer{name: "anthropic", resps: []providers.Response{
		runSnapshotPlanResp(),
		passResp("claude-sonnet-4-6"),
		passResp("claude-haiku-4-5-20251001"),
		passResp("claude-opus-4-7"),
	}}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     rec,
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(cfg.SessionTTL),
	}}

	planRunID := runSnapshotSequence(t, h)

	lines, err := rec.RunLines(rec.RunHash(planRunID))
	require.NoError(t, err)

	var header *scorecard.RunLine
	var last *scorecard.TaskSnapshot
	for i := range lines {
		if lines[i].Header {
			header = &lines[i]
		} else if lines[i].Task != nil {
			last = lines[i].Task
		}
	}
	require.NotNil(t, header)
	assert.Equal(t, Version, header.ServerVersion)
	for _, role := range []string{"plan", "pre", "mid", "post", "worker"} {
		_, ok := header.ConfiguredModels[role]
		assert.True(t, ok, "configured_models lacks %s", role)
	}
	require.NotNil(t, header.PlanCall)
	assert.Equal(t, "validate_plan", header.PlanCall.Tool)

	require.NotNil(t, last)
	var tools []string
	for _, c := range last.Calls {
		tools = append(tools, c.Tool)
		assert.NotEmpty(t, c.Model)
	}
	assert.Equal(t, []string{"validate_task_spec", "check_progress", "validate_completion"}, tools)

	raw, err := os.ReadFile(filepath.Join(dir, "runs.jsonl"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "ZXQ")
	assert.NotContains(t, string(raw), planRunID)
}

// TestRunSnapshots_NilStatsWritesNothing runs the same lifecycle with Stats
// nil and asserts no runs.jsonl is ever written, and every call still
// succeeds.
func TestRunSnapshots_NilStatsWritesNothing(t *testing.T) {
	dir := t.TempDir() // never handed to any recorder

	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)

	rv := &scriptedRunReviewer{name: "anthropic", resps: []providers.Response{
		runSnapshotPlanResp(),
		passResp("claude-sonnet-4-6"),
		passResp("claude-haiku-4-5-20251001"),
		passResp("claude-opus-4-7"),
	}}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     nil,
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(cfg.SessionTTL),
	}}

	runSnapshotSequence(t, h)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, "runs.jsonl", e.Name())
	}
}
