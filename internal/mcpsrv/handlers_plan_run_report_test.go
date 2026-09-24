package mcpsrv

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// planRunReportWireText extracts the actual marshalled JSON text the tool
// call would return over the wire. A Go-value nil-check on Tasks (e.g.
// require.NotNil + assert.Len(..., 0)) does NOT pin this: Go marshals a nil
// slice as `null` and a non-nil empty slice as `[]`, and this codebase's wire
// contract requires the `[]` form. Only inspecting the actual marshalled text
// catches a regression that reintroduces a nil Tasks slice.
func planRunReportWireText(t *testing.T, out *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, out)
	require.Len(t, out.Content, 1)
	tc, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func TestPlanRunReport_MissingID(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	_, _, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{})
	assert.EqualError(t, err, "plan_run_id is required")
}

// TestPlanRunReport_UnknownID pins the AC: an unknown or expired id returns a
// result carrying exactly one session_not_found finding with
// criterion:"plan_run_id" — reusing the existing category rather than
// inventing one — and Tasks must be a non-nil empty slice.
func TestPlanRunReport_UnknownID(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}

	out, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: "pr_does_not_exist"})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "pr_does_not_exist", res.PlanRunID)
	require.NotNil(t, res.Tasks)
	assert.Len(t, res.Tasks, 0)
	require.Len(t, res.Findings, 1)
	assert.Equal(t, verdict.CategorySessionMissing, res.Findings[0].Category)
	assert.Equal(t, "plan_run_id", res.Findings[0].Criterion)
	assert.NotEmpty(t, res.SummaryBlock)

	// Wire-level: the Go-value checks above cannot tell null from [] on the
	// actual marshalled JSON. Assert the wire text directly.
	assert.Contains(t, planRunReportWireText(t, out), `"tasks": []`)
}

// TestPlanRunReport_FoundZeroRows_TasksWireEmptyArray covers the other branch
// that can emit an empty Tasks list: a plan run that exists (validate_plan
// minted it) but has had no validate_task_spec calls append a row yet. This
// goes through the `run, ok := h.deps.PlanRuns.Snapshot(...)` success path
// and the `if res.Tasks == nil { res.Tasks = []planrun.TaskRow{} }` guard,
// which is distinct code from the not-found branch's inline
// `Tasks: []planrun.TaskRow{}` literal that TestPlanRunReport_UnknownID
// exercises.
func TestPlanRunReport_FoundZeroRows_TasksWireEmptyArray(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := h.deps.PlanRuns.Create("pass", "rigorous", 3) // minted; no rows appended yet

	out, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, run.ID, res.PlanRunID)
	require.NotNil(t, res.Tasks)
	assert.Len(t, res.Tasks, 0)

	assert.Contains(t, planRunReportWireText(t, out), `"tasks": []`)
}

// panicIfCalledReviewer is a Reviewer whose Review method panics
// unconditionally. Wiring it into Deps.Reviews turns "the handler touched
// the provider" into an immediate, unmistakable test failure rather than a
// silent pass — the structural proof the evidence standard asks for, not a
// comment claiming "no provider call".
type panicIfCalledReviewer struct{}

func (panicIfCalledReviewer) Name() string { return "anthropic" }
func (panicIfCalledReviewer) Review(context.Context, providers.Request) (providers.Response, error) {
	panic("plan_run_report must never call a reviewer — it is deterministic and free")
}

// TestPlanRunReport_NoProviderCall satisfies the AC "The handler makes no
// provider call — verified by a nil reviewer registry in the test" and the
// evidence standard's stronger form: Reviews is nil (any h.deps.Reviews.Get
// call returns an error, and any code path that then dereferences the
// resulting nil Reviewer interface to call .Review panics), AND — belt and
// suspenders — a second run wires in panicIfCalledReviewer so that even a
// direct map access (bypassing Get's error check) blows up loudly instead of
// silently returning a zero value.
func TestPlanRunReport_NoProviderCall(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)

	store := planrun.NewStore(1 * time.Hour)
	run := store.Create("pass", "rigorous", 2)
	_, ok := store.Attach(run.ID, "s1", planrun.TaskRef{Title: "Task one"}, "pass")
	require.True(t, ok)
	_, ok = store.UpdateRow(run.ID, "s1", func(row *planrun.TaskRow) { row.PostVerdict = "pass" })
	require.True(t, ok)

	t.Run("nil registry", func(t *testing.T) {
		h := &handlers{deps: Deps{
			Cfg:      cfg,
			Sessions: session.NewStore(1 * time.Hour),
			Reviews:  nil,
			PlanRuns: store,
		}}
		out, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
		require.NoError(t, err)
		require.NotNil(t, out)
		assert.Equal(t, run.ID, res.PlanRunID)
		require.Len(t, res.Tasks, 1)
		assert.Equal(t, "Task one", res.Tasks[0].TaskTitle)
	})

	t.Run("panicking registry", func(t *testing.T) {
		h := &handlers{deps: Deps{
			Cfg:      cfg,
			Sessions: session.NewStore(1 * time.Hour),
			Reviews:  providers.Registry{"anthropic": panicIfCalledReviewer{}},
			PlanRuns: store,
		}}
		out, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
		require.NoError(t, err)
		require.NotNil(t, out)
		assert.Equal(t, run.ID, res.PlanRunID)
		require.Len(t, res.Tasks, 1)
	})
}

// TestPlanRunReport_TasksCarryNoCallLog pins the fix for the finding that
// plan_run_report's Tasks unexpectedly serialised each row's internal call
// log (up to 32 entries per task). It drives a full validate_plan ->
// validate_task_spec -> check_progress -> validate_completion sequence via
// runSnapshotSequence, which populates the row's Calls with three entries,
// then asserts the report's wire JSON carries no "calls" key at all and that
// the live store's own row is untouched (the report must copy, not mutate).
func TestPlanRunReport_TasksCarryNoCallLog(t *testing.T) {
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
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": rv},
		PlanRuns: planrun.NewStore(cfg.SessionTTL),
	}}

	planRunID := runSnapshotSequence(t, h)

	// The live store's row really does carry a call log; otherwise this test
	// would pass trivially with nothing to clear.
	live, ok := h.deps.PlanRuns.Snapshot(planRunID)
	require.True(t, ok)
	require.Len(t, live.Rows, 1)
	require.NotEmpty(t, live.Rows[0].Calls, "precondition: the row must carry a call log")

	out, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: planRunID})
	require.NoError(t, err)
	require.Len(t, res.Tasks, 1)
	assert.Empty(t, res.Tasks[0].Calls)
	assert.Zero(t, res.Tasks[0].CallsDropped)

	wire := planRunReportWireText(t, out)
	assert.NotContains(t, wire, `"calls"`)
	assert.NotContains(t, wire, `"calls_dropped"`)

	// The report must not have mutated the store: a second Snapshot still
	// shows the row's call log intact.
	live2, ok := h.deps.PlanRuns.Snapshot(planRunID)
	require.True(t, ok)
	require.Len(t, live2.Rows, 1)
	assert.NotEmpty(t, live2.Rows[0].Calls, "plan_run_report must not clear the store's own call log")
	assert.Equal(t, live.Rows[0].Calls, live2.Rows[0].Calls)
}

func TestValidateCompletion_PlanRunRowCarriesWaivedAndEscalated(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "actionable", 1)
	rv.resp = passResp("claude-sonnet-4-6")
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC 1"}, PlanRunID: run.ID,
	})
	require.NoError(t, err)
	sid := pre.SessionID

	id := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(driftFinding)).Findings[0].ID

	answered := completionCallArgs(sid)
	answered.FindingResponses = []FindingResponseArg{{FindingID: id, Response: "Task 7 owns it"}}
	require.True(t, completeWith(t, h, rv, answered, reviewerFindingsResp(driftFinding)).Escalate)

	ruled := completionCallArgs(sid)
	ruled.ControllerRulings = []ControllerRulingArg{{FindingID: id, Ruling: "Task 7 owns it"}}
	require.Len(t, completeWith(t, h, rv, ruled, reviewerFindingsResp(driftFinding)).WaivedFindings, 1)

	snap, ok := h.deps.PlanRuns.Snapshot(run.ID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1)
	assert.Equal(t, 1, snap.Rows[0].Waived)
	assert.True(t, snap.Rows[0].Escalated, "escalation stays recorded after a later call that did not escalate")
}
