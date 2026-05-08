package mcpsrv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

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

	go func() { _ = srv.Run(ctx, st) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer cs.Close()

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
