//go:build e2e

package mcpsrv

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

// usageRecordingReviewer wraps a real Reviewer and records every Response it
// returns, in call order, so a test can inspect cache usage across a
// sequence of live calls.
type usageRecordingReviewer struct {
	inner  providers.Reviewer
	usages []providers.Response
}

func (u *usageRecordingReviewer) Name() string { return u.inner.Name() }

func (u *usageRecordingReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	resp, err := u.inner.Review(ctx, req)
	u.usages = append(u.usages, resp)
	return resp, err
}

// realAnthropicReviewer builds a live Anthropic reviewer from
// ANTHROPIC_API_KEY, skipping the test when it isn't set.
func realAnthropicReviewer(t *testing.T) providers.Reviewer {
	t.Helper()
	cfg, err := config.Load(os.Getenv)
	require.NoError(t, err)
	if cfg.AnthropicKey == "" {
		t.Skip("ANTHROPIC_API_KEY required for live e2e")
	}
	return providers.NewAnthropic(cfg.AnthropicKey, "", cfg.RequestTimeout)
}

// TestPlanCachingReadsPrefixE2E sends a plan large enough to clear the
// minimum cacheable prefix and asserts a later chunk call reads it back.
// Without this the caching change is unfalsifiable: the request looks right
// and the bill is unchanged.
//
// 40 tasks keeps the rendered plan_tasks_chunk/plan_findings_only prefix
// (ground rules + the plan itself, everything before "## What to evaluate")
// comfortably above both the default PlanTasksPerChunk (8, so the plan
// chunks at all) and Anthropic's ~1024-token minimum cacheable prefix. If
// the prefix ever comes in under that minimum, the cache write is silently
// skipped and this test fails with zeroes on rec.usages[1] — grow the task
// count rather than weakening the assertions below.
//
// Run with:
//
//	ANTHROPIC_API_KEY=sk-ant-... \
//	  go test -tags=e2e -race -count=1 \
//	    -run TestPlanCachingReadsPrefixE2E \
//	    ./internal/mcpsrv/... -v -timeout 5m
func TestPlanCachingReadsPrefixE2E(t *testing.T) {
	plan := buildPlanWithNTasks(40) // 40 > default PlanTasksPerChunk 8 -> chunked, with margin on the cache minimum
	rec := &usageRecordingReviewer{inner: realAnthropicReviewer(t)}

	cfg, err := config.Load(os.Getenv)
	require.NoError(t, err)
	cfg.PlanTasksPerChunk = 8

	d := Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(1 * time.Hour),
		Reviews:   providers.Registry{"anthropic": rec},
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(1 * time.Hour),
	}
	h := &handlers{deps: d}

	_, _, err = h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rec.usages), 2, "chunked path makes >= 2 calls")

	assert.Greater(t, rec.usages[0].CacheCreationInputTokens, 0, "first call writes the prefix")
	assert.Greater(t, rec.usages[1].CacheReadInputTokens, 0, "second call reads it back")
}
