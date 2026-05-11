package mcpsrv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

type switchingFakeReviewer struct {
	name string
}

func (s *switchingFakeReviewer) Name() string { return s.name }

func (s *switchingFakeReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	if strings.Contains(req.User, "## Plan under review") {
		return providers.Response{
			RawJSON:     []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[{"task_index":0,"task_title":"Task 1: First","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`),
			Model:       "claude-sonnet-4-6",
			InputTokens: 5, OutputTokens: 4,
		}, nil
	}
	return providers.Response{
		RawJSON:     []byte(`{"verdict":"pass","findings":[],"next_action":"go"}`),
		Model:       "claude-sonnet-4-6",
		InputTokens: 3, OutputTokens: 2,
	}, nil
}

func TestIntegration_FullLifecycle(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)

	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	deps := Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(1 * time.Hour),
		Reviews:  providers.Registry{"anthropic": rv},
	}
	srv := New(deps)

	// NewInMemoryTransports returns (serverTransport, clientTransport).
	// Servers must be connected before clients per SDK docs.
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		if err := srv.Run(ctx, st); err != nil && ctx.Err() == nil {
			t.Errorf("srv.Run: %v", err)
		}
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() {
		if err := cs.Close(); err != nil {
			t.Errorf("cs.Close: %v", err)
		}
	}()

	// 1. validate_task_spec
	pre := callTool(t, ctx, cs, "validate_task_spec", map[string]any{
		"task_title": "X", "goal": "Y", "acceptance_criteria": []string{"AC1"},
	})
	assert.Equal(t, "pass", pre.Verdict)
	require.NotEmpty(t, pre.SessionID)

	// 2. check_progress
	mid := callTool(t, ctx, cs, "check_progress", map[string]any{
		"session_id":    pre.SessionID,
		"working_on":    "writing handler",
		"changed_files": []map[string]string{{"path": "h.go", "content": "package h\n"}},
	})
	assert.Equal(t, pre.SessionID, mid.SessionID)

	// 3. validate_completion
	post := callTool(t, ctx, cs, "validate_completion", map[string]any{
		"session_id":  pre.SessionID,
		"summary":     "done",
		"final_files": []map[string]string{{"path": "h.go", "content": "package h\n"}},
	})
	assert.Equal(t, pre.SessionID, post.SessionID)
	assert.Equal(t, "pass", post.Verdict)
}

func callTool(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) Envelope {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %v", res.Content)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var env Envelope
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &env))
	return env
}

func TestIntegration_ValidatePlan(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)

	rv := &switchingFakeReviewer{name: "anthropic"}
	deps := Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(1 * time.Hour),
		Reviews:  providers.Registry{"anthropic": rv},
	}
	srv := New(deps)

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		if err := srv.Run(ctx, st); err != nil && ctx.Err() == nil {
			t.Errorf("srv.Run: %v", err)
		}
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() {
		if err := cs.Close(); err != nil {
			t.Errorf("cs.Close: %v", err)
		}
	}()

	plan := "# Plan\n\n### Task 1: First\n\nSome body.\n"
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "validate_plan",
		Arguments: map[string]any{"plan_text": plan},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %v", res.Content)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	var pr struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &pr))
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	require.Len(t, pr.Tasks, 1)
	assert.Equal(t, "Task 1: First", pr.Tasks[0].TaskTitle)
}

// TestIntegration_ValidatePlanChunked exercises the chunked path end-to-end
// through the MCP transport. A 12-task plan with PlanTasksPerChunk=8 triggers
// the chunked dispatch: 1 plan-findings call + 2 per-chunk calls (sizes 8 and
// 4). The merged envelope must be shape-compatible with the single-call path.
func TestIntegration_ValidatePlanChunked(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)
	cfg.PlanTasksPerChunk = 8

	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),
			chunkResp(t, titlesRange(1, 8)),
			chunkResp(t, titlesRange(9, 12)),
		},
	}

	deps := Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(1 * time.Hour),
		Reviews:  providers.Registry{"anthropic": sr},
	}
	srv := New(deps)

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		if err := srv.Run(ctx, st); err != nil && ctx.Err() == nil {
			t.Errorf("srv.Run: %v", err)
		}
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() {
		if err := cs.Close(); err != nil {
			t.Errorf("cs.Close: %v", err)
		}
	}()

	plan := buildPlanWithNTasks(12)
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "validate_plan",
		Arguments: map[string]any{"plan_text": plan},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %v", res.Content)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	var pr struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &pr))
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	require.Len(t, pr.Tasks, 12, "merged Tasks length")
	assert.NotEmpty(t, pr.NextAction)
	assert.Equal(t, 3, sr.calls, "reviewer call count (1 plan-findings + 2 chunks)")
	// Spot-check ordering: first and last task titles match the plan.
	assert.Equal(t, "Task 1: t1", pr.Tasks[0].TaskTitle)
	assert.Equal(t, "Task 12: t12", pr.Tasks[11].TaskTitle)
}
