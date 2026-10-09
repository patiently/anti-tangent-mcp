package mcpsrv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func digestArg(t *testing.T, s string) any {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal([]byte(s), &v))
	return v
}

const sampleDiff = "--- a/prompts/zip.md\n+++ b/prompts/zip.md\n@@ -1 +1 @@\n-Ask for the ZIP.\n+If the ZIP field is empty, ask for it.\n"

func TestValidateCompletion_RateDigestRenderedAndStored(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: agentNetworkPlanText, BoundaryRules: testRules})
	require.NoError(t, err)
	_, spec, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Task 2: Zip prompt", Goal: "ask", PlanRunID: pr.PlanRunID, TaskIndex: 2,
	})
	require.NoError(t, err)

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: spec.SessionID, Summary: "kept at 8/10", FinalDiff: sampleDiff, TestEvidence: "zip-missing 8/10",
		RateDigest: digestArg(t, `{"target_eval":"evals/core/zip-missing.yaml","n":10,"before_k":5,"after_k":8,"suite":{"evals":42,"regressions":0},"kept":true}`),
	})
	require.NoError(t, err)
	require.Contains(t, task.LastRequest.User, "## Rate digest (caller-supplied)")
	require.Contains(t, task.LastRequest.User, "Rate: 5→8/10")
	require.Contains(t, task.LastRequest.User, "### Experiment outcome")
	require.NotContains(t, findingCategories(env.Findings), verdict.CategoryDiffRequired)

	snap, ok := h.deps.PlanRuns.Snapshot(pr.PlanRunID)
	require.True(t, ok)
	var row planrun.TaskRow
	for _, r := range snap.Rows {
		if r.Index == 2 {
			row = r
		}
	}
	require.NotNil(t, row.RateDigest)
	require.Equal(t, "5→8/10 · reg 0 · kept", row.RateDigest.Cell())
	require.Empty(t, row.RateDigest.TargetEval, "the record keeps counts, not the eval's path")

	_, report, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: pr.PlanRunID})
	require.NoError(t, err)
	require.Contains(t, report.SummaryBlock, "5→8/10 · reg 0 · kept")
}

func TestValidateCompletion_KeptWithoutADiff(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	_, spec, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "Zip prompt", Goal: "ask", TaskKind: "experiment", Rung: "prompt",
	})
	require.NoError(t, err)
	calls := task.Calls

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: spec.SessionID, Summary: "kept", TestEvidence: "zip-missing 8/10",
		RateDigest: digestArg(t, `{"n":10,"after_k":8,"kept":true}`),
	})
	require.NoError(t, err)
	require.Equal(t, calls, task.Calls, "rejected before review")
	require.Equal(t, []verdict.Category{verdict.CategoryDiffRequired}, findingCategories(env.Findings))

	_, env, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: spec.SessionID, Summary: "reverted at 7/10", TestEvidence: "zip-missing 7/10",
		RateDigest: digestArg(t, `{"n":10,"after_k":7,"kept":false}`),
	})
	require.NoError(t, err)
	require.Equal(t, calls+1, task.Calls, "a reverted experiment with test evidence alone is reviewed")
	require.NotContains(t, findingCategories(env.Findings), verdict.CategoryDiffRequired)

	_, env, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: spec.SessionID, Summary: "kept", TestEvidence: "zip-missing 8/10",
		RateDigest: digestArg(t, `{"n":10,"after_k":11,"kept":true}`),
	})
	require.NoError(t, err)
	require.Equal(t, calls+1, task.Calls, "a malformed digest that says kept is still held to the diff rule")
	require.Equal(t, []verdict.Category{verdict.CategoryDiffRequired}, findingCategories(env.Findings))
}

func TestValidateCompletion_MalformedRateDigestIsDroppedNotRefused(t *testing.T) {
	h, task, _ := agentNetworkTaskHandlers(t)
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "s", FinalDiff: sampleDiff, RateDigest: digestArg(t, `{"n":10,"after_k":11,"kept":true}`),
	})
	require.NoError(t, err)
	require.Equal(t, 1, task.Calls)
	require.NotContains(t, task.LastRequest.User, "## Rate digest")
	last := env.Findings[len(env.Findings)-1]
	require.Equal(t, "rate_digest", last.Criterion)
	require.Equal(t, verdict.SeverityMinor, last.Severity)
	require.Contains(t, last.Evidence, "after_k must be an integer from 0 to n (10)")
	require.Equal(t, "pass", env.Verdict)
}

// TestValidateCompletion_RateDigestOfAnyShapePassesSchemaValidation runs the
// call through the MCP server, whose SDK validates arguments against the
// tool's input schema before the handler runs. A typed rate_digest there
// would turn a malformed digest into a transport error.
func TestValidateCompletion_RateDigestOfAnyShapePassesSchemaValidation(t *testing.T) {
	d := newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")})
	srv := New(d)
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = srv.Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer cs.Close()

	for _, digest := range []any{"5/10", map[string]any{"n": "ten"}, []any{1, 2}} {
		env := callTool(t, ctx, cs, "validate_completion", map[string]any{
			"session_id": "", "summary": "s", "final_diff": sampleDiff, "rate_digest": digest,
		})
		require.Equal(t, "rate_digest", env.Findings[len(env.Findings)-1].Criterion)
	}
}
