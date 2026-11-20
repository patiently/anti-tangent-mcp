package mcpsrv

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestRunSnapshots_ImplementerModelAndTestCounts covers the implementer model
// and test counts on the run snapshot and the stats event, and that the
// ledger's text reaches neither file.
func TestRunSnapshots_ImplementerModelAndTestCounts(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, SummaryInterval: 24 * time.Hour, SummaryThreshold: 100000, RetentionDays: 30,
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

	const model = "anthropic:claude-sonnet-5-5"
	const protects = "UNIQUE-PROTECTS-TEXT"
	rv := &scriptedRunReviewer{name: "anthropic", resps: []providers.Response{
		runSnapshotPlanResp(),
		passResp("claude-sonnet-4-6"),
		reviewerFindingsResp(`{"severity":"minor","category":"quality","criterion":"test_value","evidence":"e","suggestion":"s"}`),
	}}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     rec,
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(cfg.SessionTTL),
	}}

	ctx := context.Background()
	plan := "# Plan\n\n### Task 1: Secret title ZXQ\n\n**Goal:** g1\n\n**Acceptance criteria:**\n- ac1\n\n"
	_, pr, err := h.ValidatePlan(ctx, nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	_, pre, err := h.ValidateTaskSpec(ctx, nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 1: Secret title ZXQ", Goal: "g1", AcceptanceCriteria: []string{"ac1"},
		PlanRunID: pr.PlanRunID, TaskIndex: 1, ImplementerModel: model,
	})
	require.NoError(t, err)

	diff := "diff --git a/f_test.go b/f_test.go\n--- a/f_test.go\n+++ b/f_test.go\n@@ -0,0 +1,3 @@\n" +
		"+func TestA(t *testing.T) {}\n+func TestB(t *testing.T) {}\n+func TestC(t *testing.T) {}\n"
	args := completionCallArgs(pre.SessionID)
	args.FinalDiff = diff
	args.FinalFiles = nil
	args.TestLedger = []TestLedgerEntryArg{{Test: "f_test.go::TestB", Protects: protects}}
	_, _, err = h.ValidateCompletion(ctx, nil, args)
	require.NoError(t, err)

	lines, err := rec.RunLines(rec.RunHash(pr.PlanRunID))
	require.NoError(t, err)
	var last *scorecard.TaskSnapshot
	for i := range lines {
		if lines[i].Task != nil {
			last = lines[i].Task
		}
	}
	require.NotNil(t, last)
	assert.Equal(t, model, last.ImplementerModel)
	require.NotNil(t, last.TestsAdded)
	assert.Equal(t, 3, *last.TestsAdded)
	assert.Equal(t, 1, last.ACCount)
	assert.Equal(t, 1, last.LedgerEntries)
	assert.Equal(t, 1, last.Criteria["test_value"])

	events, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	require.NoError(t, err)
	var found bool
	for _, ln := range strings.Split(strings.TrimSpace(string(events)), "\n") {
		var ev stats.Event
		require.NoError(t, json.Unmarshal([]byte(ln), &ev))
		if ev.Tool != "validate_completion" {
			continue
		}
		found = true
		assert.Equal(t, model, ev.ImplementerModel)
		require.NotNil(t, ev.TestsAdded)
		assert.Equal(t, 3, *ev.TestsAdded)
		assert.Equal(t, 1, ev.ACCount)
		assert.Equal(t, 1, ev.LedgerEntries)
	}
	assert.True(t, found, "no validate_completion event")

	for _, name := range []string{"runs.jsonl", "events.jsonl"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), protects, name)
	}
}

// TestRunSnapshots_StandaloneSessionIsOneTaskRun covers a session with no plan
// run: its completions write one session header and index-1 task lines under
// the session run hash, and a session that never completes writes nothing.
func TestRunSnapshots_StandaloneSessionIsOneTaskRun(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, SummaryInterval: 24 * time.Hour, SummaryThreshold: 100000, RetentionDays: 30,
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
	rv := &scriptedRunReviewer{name: "anthropic", resps: []providers.Response{passResp("claude-sonnet-4-6")}}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     rec,
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(cfg.SessionTTL),
	}}
	ctx := context.Background()
	spec := func(model string) ValidateTaskSpecArgs {
		return ValidateTaskSpecArgs{TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"}, ImplementerModel: model}
	}

	_, done, err := h.ValidateTaskSpec(ctx, nil, spec("claude-sonnet-5-5[1m]"))
	require.NoError(t, err)
	for range 2 {
		_, _, err = h.ValidateCompletion(ctx, nil, completionCallArgs(done.SessionID))
		require.NoError(t, err)
	}
	_, idle, err := h.ValidateTaskSpec(ctx, nil, spec(""))
	require.NoError(t, err)

	lines, err := rec.RunLines(rec.SessionRunHash(done.SessionID))
	require.NoError(t, err)
	var headers, tasks int
	for _, l := range lines {
		if l.Header {
			headers++
			assert.Equal(t, scorecard.KindSession, l.Kind)
			assert.Equal(t, 1, l.TaskCount)
			assert.NotEmpty(t, l.ConfiguredModels)
			continue
		}
		tasks++
		require.NotNil(t, l.Task)
		assert.Equal(t, 1, l.Task.Index)
		assert.Equal(t, "claude-sonnet-5-5[1m]", l.Task.ImplementerModel)
		assert.Equal(t, "pass", l.Task.PreVerdict)
		assert.Equal(t, 1, l.Task.Attempts)
		require.NotEmpty(t, l.Task.Calls)
		assert.Equal(t, "validate_task_spec", l.Task.Calls[0].Tool)
		assert.Equal(t, "pass", l.Task.Calls[0].Verdict)
	}
	assert.Equal(t, 1, headers)
	assert.Equal(t, 2, tasks)

	idleLines, err := rec.RunLines(rec.SessionRunHash(idle.SessionID))
	require.NoError(t, err)
	assert.Empty(t, idleLines)
}

// TestRunSnapshots_SessionHeaderRetriedAfterFailedAppend covers a session whose
// first header append fails: the next completion must write the header, so the
// run is never left with task lines and no session header.
func TestRunSnapshots_SessionHeaderRetriedAfterFailedAppend(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, SummaryInterval: 24 * time.Hour, SummaryThreshold: 100000, RetentionDays: 30,
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
	rv := &scriptedRunReviewer{name: "anthropic", resps: []providers.Response{passResp("claude-sonnet-4-6")}}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     rec,
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(cfg.SessionTTL),
	}}
	ctx := context.Background()
	_, done, err := h.ValidateTaskSpec(ctx, nil, ValidateTaskSpecArgs{TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"}})
	require.NoError(t, err)

	blocker := filepath.Join(dir, "runs.jsonl")
	require.NoError(t, os.Mkdir(blocker, 0o755))
	_, _, err = h.ValidateCompletion(ctx, nil, completionCallArgs(done.SessionID))
	require.NoError(t, err)
	require.NoError(t, os.Remove(blocker))
	_, _, err = h.ValidateCompletion(ctx, nil, completionCallArgs(done.SessionID))
	require.NoError(t, err)

	lines, err := rec.RunLines(rec.SessionRunHash(done.SessionID))
	require.NoError(t, err)
	require.NotEmpty(t, lines)
	assert.True(t, lines[0].Header, "the first line written for the session must be its header")
	assert.Equal(t, scorecard.KindSession, lines[0].Kind)
}

// TestRunSnapshots_TaskLinesFollowRowMutationOrder covers concurrent updates
// to one row: the scorecard keeps a task's latest-timestamped line, so every
// task line must carry a timestamp no earlier than the line written for the
// mutation before it, and be appended after it. Both the session row and the
// plan-run row paths are checked.
func TestRunSnapshots_TaskLinesFollowRowMutationOrder(t *testing.T) {
	const updates = 64
	assertOrdered := func(t *testing.T, lines []scorecard.RunLine) {
		t.Helper()
		var tasks []scorecard.RunLine
		for _, l := range lines {
			if l.Task != nil {
				tasks = append(tasks, l)
			}
		}
		require.Len(t, tasks, updates)
		for i := 1; i < len(tasks); i++ {
			prev, cur := tasks[i-1], tasks[i]
			require.Greater(t, cur.Task.Checkpoints, prev.Task.Checkpoints, "line %d was appended after a newer row", i)
			require.False(t, cur.Ts.Before(prev.Ts), "line %d is stamped before the line for an older row", i)
		}
	}
	hammer := func(update func()) {
		var wg sync.WaitGroup
		for range updates {
			wg.Add(1)
			go func() {
				defer wg.Done()
				update()
			}()
		}
		wg.Wait()
	}
	env := Envelope{Verdict: "pass"}

	t.Run("session", func(t *testing.T) {
		h, rec, _ := outcomeHandlers(t)
		sess := h.deps.Sessions.Create(session.TaskSpec{Title: "t"}, "")
		_, _, ok := h.deps.Sessions.UpdateRow(sess.ID, func(r *planrun.TaskRow) { r.PostVerdict = "pass" })
		require.True(t, ok)
		hammer(func() { h.recordCheckpointRow(sess, env) })
		lines, err := rec.RunLines(rec.SessionRunHash(sess.ID))
		require.NoError(t, err)
		assertOrdered(t, lines)
	})

	t.Run("plan", func(t *testing.T) {
		h, rec, _ := outcomeHandlers(t)
		run := h.deps.PlanRuns.Create("pass", "rigorous", 1)
		_, ok := h.deps.PlanRuns.Attach(run.ID, "s1", planrun.TaskRef{Index: 1}, "pass")
		require.True(t, ok)
		sess := &session.Session{ID: "s1", PlanRunID: run.ID}
		hammer(func() { h.recordCheckpointRow(sess, env) })
		lines, err := rec.RunLines(rec.RunHash(run.ID))
		require.NoError(t, err)
		assertOrdered(t, lines)
	})
}
