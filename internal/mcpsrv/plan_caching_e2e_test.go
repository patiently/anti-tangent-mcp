//go:build e2e

package mcpsrv

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
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

// planCachingFillerSentences are rotated across tasks in
// buildLargePlanFixtureForCaching so the fixture's per-task text varies
// instead of repeating one line. Highly repetitive text (e.g. the same
// "### Task N: tN" line N times) is exactly the case where a bytes/4
// token-count heuristic is least trustworthy: real BPE tokenizers can
// compress repeated boilerplate far denser than 4 bytes/token, so a
// fixture sized close to a token-count line on that heuristic can
// silently fall short of the real minimum. Varied prose tokenizes closer
// to what the heuristic assumes, which is also just more representative
// of a real plan.
var planCachingFillerSentences = []string{
	"This task touches the ingestion pipeline and must preserve backward compatibility with existing consumers.",
	"Update the retry policy so transient network failures are retried with exponential backoff and a hard cap.",
	"The acceptance criteria below describe observable behavior; do not couple them to a specific implementation strategy.",
	"Coordinate with the schema migration in a prior task so the new field stays nullable during the rollout window.",
	"Add structured logging around the new code path so an on-call engineer can trace a failing request end to end.",
	"This change is purely additive; existing callers of the public API must see no behavior change.",
	"Validate all user-supplied input at the boundary and reject malformed requests with a clear error message.",
	"Treat any code symbol named here as a black-box reference with an unknown signature and unknown return type.",
	"Keep the change scoped to this package; cross-package refactors belong in a follow-up task, not this one.",
	"Document any new environment variable in the README alongside the existing configuration knobs.",
}

// largePlanCachingFixtureTasks sizes buildLargePlanFixtureForCaching for
// TestPlanCachingReadsPrefixE2E.
//
// Anthropic's minimum cacheable prefix length is model-dependent, and this
// repo's own provider allowlist (internal/providers/reviewer.go) spans three
// different minimums: 1024 tokens for claude-sonnet-4-6 (this repo's default
// PlanModel), 2048 for claude-opus-4-7, and 4096 for
// claude-haiku-4-5-20251001. ANTI_TANGENT_PLAN_MODEL can select any of the
// three, so sizing this fixture to clear only the default model's minimum
// would make the suite fail — on a perfectly correct implementation — for
// anyone running it against opus or haiku. A test that fails when the code
// is right is worse than no test: it trains people to ignore it.
//
// So this fixture targets ~2x the LARGEST allowlisted minimum (4096 tokens),
// i.e. comfortably north of 8000 tokens of rendered prefix, measured with
// real margin rather than right at the line. Measured directly against
// prompts.RenderPlanFindingsOnly (the same renderer reviewPlanChunked calls),
// 80 tasks of the varied prose above renders a 41590-byte UserPrefix, which
// is ~10,400 tokens on a 4-bytes/token estimate and still ~12,600 tokens
// even under a deliberately pessimistic 3.3-bytes/token estimate — both
// comfortably clear of the 4096-token worst case with margin to spare if the
// real tokenizer compresses this prose more than either estimate assumes.
// Do not shrink this back toward the 1024/2048/4096 lines without re-deriving
// the margin above.
const largePlanCachingFixtureTasks = 80

// buildLargePlanFixtureForCaching emits a markdown plan with n
// "### Task k: <title>" blocks, each carrying a Goal and two acceptance
// criteria built from planCachingFillerSentences (rotated per task, offset
// between the Goal and the second AC so adjacent tasks don't share a
// sentence pairing either). See largePlanCachingFixtureTasks for why this
// needs to be large and varied rather than a short repeated line.
func buildLargePlanFixtureForCaching(n int) string {
	var b strings.Builder
	b.WriteString("# Plan\n\n")
	for i := 1; i <= n; i++ {
		goalSentence := planCachingFillerSentences[(i-1)%len(planCachingFillerSentences)]
		acSentence := planCachingFillerSentences[i%len(planCachingFillerSentences)]
		fmt.Fprintf(&b,
			"### Task %d: implement feature slice %d\n\n"+
				"**Goal:** %s\n\n"+
				"**Acceptance criteria:**\n"+
				"- ac%d-a: %s\n"+
				"- ac%d-b: %s The change ships with a regression test covering the behavior described above.\n\n",
			i, i, goalSentence, i, goalSentence, i, acSentence)
	}
	return b.String()
}

// TestPlanCachingReadsPrefixE2E sends a plan large enough to clear the
// minimum cacheable prefix and asserts a later chunk call reads it back.
// Without this the caching change is unfalsifiable: the request looks right
// and the bill is unchanged.
//
// Run with:
//
//	ANTHROPIC_API_KEY=sk-ant-... \
//	  go test -tags=e2e -race -count=1 \
//	    -run TestPlanCachingReadsPrefixE2E \
//	    ./internal/mcpsrv/... -v -timeout 5m
func TestPlanCachingReadsPrefixE2E(t *testing.T) {
	plan := buildLargePlanFixtureForCaching(largePlanCachingFixtureTasks)
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

	// Log the actual rendered prefix size before asserting on cache usage,
	// so a future failure immediately shows whether the fixture came in too
	// small (grow largePlanCachingFixtureTasks) versus the cache genuinely
	// not being read (a real regression). This renders through the same
	// prompts.RenderPlanFindingsOnly call reviewPlanChunked makes, so the
	// byte count logged here is exactly what got sent as CachePrefix.
	rendered, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{PlanText: plan})
	require.NoError(t, err)
	t.Logf("rendered cache prefix: %d bytes (~%d tokens at 4 bytes/token)", len(rendered.UserPrefix), len(rendered.UserPrefix)/4)

	_, _, err = h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rec.usages), 2, "chunked path makes >= 2 calls")

	assert.Greater(t, rec.usages[0].CacheCreationInputTokens, 0, "first call writes the prefix")
	assert.Greater(t, rec.usages[1].CacheReadInputTokens, 0, "second call reads it back")
}
