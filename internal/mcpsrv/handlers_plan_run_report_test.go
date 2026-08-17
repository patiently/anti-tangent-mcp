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
	store.AppendRow(run.ID, planrun.TaskRow{
		TaskTitle: "Task one", PreVerdict: "pass", PostVerdict: "pass", CodesceneState: planrun.StateMissing,
	})

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
