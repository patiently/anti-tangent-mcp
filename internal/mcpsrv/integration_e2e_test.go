//go:build e2e

package mcpsrv

import (
	"context"
	"encoding/json"
	"os"
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

// TestValidatePlan_E2E_LargePlanChunked exercises the chunked validate_plan
// path against a live OpenAI reviewer with a 25-task plan. ~5 sequential
// reviewer calls (~30s each) ≈ 2-3 min wall clock.
//
// Gated on ANTI_TANGENT_E2E_LARGE=1 (in addition to the `e2e` build tag) to
// keep cost off the default e2e run. Requires OPENAI_API_KEY in the
// environment.
//
// Run with:
//
//	ANTI_TANGENT_E2E_LARGE=1 OPENAI_API_KEY=sk-... \
//	  go test -tags=e2e -race -count=1 \
//	    -run TestValidatePlan_E2E_LargePlanChunked \
//	    ./internal/mcpsrv/... -v -timeout 10m
func TestValidatePlan_E2E_LargePlanChunked(t *testing.T) {
	if os.Getenv("ANTI_TANGENT_E2E_LARGE") != "1" {
		t.Skip("set ANTI_TANGENT_E2E_LARGE=1 to enable (5 live reviewer calls; costs real money)")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY required for live e2e")
	}

	cfg, err := config.Load(os.Getenv)
	require.NoError(t, err)
	// Force the plan path through the OpenAI reviewer so the test exercises
	// the same provider regardless of caller's ANTI_TANGENT_*_MODEL settings.
	cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	cfg.PlanTasksPerChunk = 8

	deps := Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(1 * time.Hour),
		Reviews: providers.Registry{
			"openai": providers.NewOpenAI(cfg.OpenAIKey, "", cfg.RequestTimeout),
		},
	}
	srv := New(deps)

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	go func() {
		if err := srv.Run(ctx, st); err != nil && ctx.Err() == nil {
			t.Errorf("srv.Run: %v", err)
		}
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer cs.Close()

	plan := buildPlanWithNTasks(25)
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
	assert.Len(t, pr.Tasks, 25, "merged Tasks length")
	assert.NotEmpty(t, pr.NextAction)
}
