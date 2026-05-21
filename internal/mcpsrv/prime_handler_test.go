package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// primePassRaw is a canned reviewer response that satisfies ParsePrime: one
// pick, no findings, an empty bm_commands array (required by the strict
// schema), and a non-empty next_action.
const primePassRaw = `{
	"verdict": "pass",
	"findings": [],
	"picks": [
		{"permalink": "decisions/0042", "reason": "shaped recent caching", "priority": "major"}
	],
	"bm_commands": [],
	"next_action": "attach picks and dispatch"
}`

func primePassResp(model string) providers.Response {
	return providers.Response{
		RawJSON:     []byte(primePassRaw),
		Model:       model,
		InputTokens: 3, OutputTokens: 2,
	}
}

func primeArgs() PrimeProjectKnowledgeArgs {
	return PrimeProjectKnowledgeArgs{
		TaskTitle:          "Implement X",
		Goal:               "Implementation that does X",
		AcceptanceCriteria: []string{"AC1"},
		KBIndex: []KBIndexEntryArg{
			{Permalink: "decisions/0042", Type: "decision", Title: "Caching", Summary: "shape caching"},
		},
	}
}

func TestPrime_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, primeArgs())
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, r.Verdict)
	require.Len(t, r.Picks, 1)
	assert.Equal(t, "decisions/0042", r.Picks[0].Permalink)
	// KBStore=="" means we strip bm_commands to an empty slice (not nil).
	assert.NotNil(t, r.BMCommands)
	assert.Len(t, r.BMCommands, 0)
	// SummaryBlock is populated and includes envelope identification.
	assert.NotEmpty(t, r.SummaryBlock)
	assert.Contains(t, r.SummaryBlock, "prime_project_knowledge")
	assert.Contains(t, r.SummaryBlock, "decisions/0042")
}

func TestPrime_EnvelopeShapeParity(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	out, r, err := h.PrimeProjectKnowledge(context.Background(), nil, primeArgs())
	require.NoError(t, err)
	require.Len(t, out.Content, 1)
	tc, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	// Decode the JSON wire envelope: PrimeResult body PLUS model_used /
	// review_ms siblings (the wrapper shape primeEnvelopeResult marshals).
	var wire struct {
		verdict.PrimeResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &wire))

	// model_used: provider:model format (assembled from passResp's Model).
	assert.Equal(t, "anthropic:claude-sonnet-4-6", wire.ModelUsed)
	// review_ms: non-negative integer.
	assert.GreaterOrEqual(t, wire.ReviewMS, int64(0))
	// summary_block: non-empty.
	assert.NotEmpty(t, wire.SummaryBlock)
	// PrimeResult shape preserved end-to-end.
	assert.Equal(t, r.Verdict, wire.Verdict)
	assert.Len(t, wire.Picks, 1)
}

func TestPrime_MissingTaskTitle_Error(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, _, err := h.PrimeProjectKnowledge(context.Background(), nil, PrimeProjectKnowledgeArgs{
		TaskTitle: "   ", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_title")
}

func TestPrime_MissingGoal_Error(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, _, err := h.PrimeProjectKnowledge(context.Background(), nil, PrimeProjectKnowledgeArgs{
		TaskTitle: "T", Goal: " ", AcceptanceCriteria: []string{"AC"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goal")
}

func TestPrime_MissingAcceptanceCriteria_Error(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, _, err := h.PrimeProjectKnowledge(context.Background(), nil, PrimeProjectKnowledgeArgs{
		TaskTitle: "T", Goal: "G",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acceptance_criteria")
}

func TestPrime_OversizedPayload_TooLargeCritical(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 50 // tiny cap so a normal call exceeds it
	h := &handlers{deps: d}

	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, PrimeProjectKnowledgeArgs{
		TaskTitle:          "Implement an example task with a slightly longer title",
		Goal:               "A goal whose text alone is bigger than fifty bytes already",
		AcceptanceCriteria: []string{"AC1 with a fairly long description"},
		Context:            "Some context that overflows the cap",
	})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, r.Verdict)
	require.Len(t, r.Findings, 1)
	assert.Equal(t, verdict.SeverityCritical, r.Findings[0].Severity)
	assert.Equal(t, verdict.CategoryTooLarge, r.Findings[0].Category)
	// Reviewer was NOT called for the rejected payload.
	assert.Equal(t, 0, rv.Calls)
}

func TestPrime_KBStoreEmpty_StripsBMCommands(t *testing.T) {
	// Reviewer returns one bm_command; with KBStore="" the handler must
	// replace bm_commands with an empty (non-nil) slice.
	withBM := `{
		"verdict": "pass",
		"findings": [],
		"picks": [{"permalink": "decisions/0042", "reason": "x", "priority": "minor"}],
		"bm_commands": [{"tool":"basic-memory:read_note","args_json":"{\"permalink\":\"decisions/0042\"}"}],
		"next_action": "go"
	}`
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(withBM), Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	require.Equal(t, "", d.Cfg.KBStore)
	h := &handlers{deps: d}

	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, primeArgs())
	require.NoError(t, err)
	require.NotNil(t, r.BMCommands)
	assert.Len(t, r.BMCommands, 0, "KBStore=='' must strip bm_commands to an empty slice")
}

func TestPrime_KBStoreBasicMemory_EmitsKBStoreMismatch(t *testing.T) {
	// Reviewer returns a non-BM permalink (leading "/") and a URI-scheme
	// permalink. Handler should append two minor kb_store_mismatch findings.
	withNonBM := `{
		"verdict": "pass",
		"findings": [],
		"picks": [
			{"permalink": "/abs/path/note.md", "reason": "leading slash", "priority": "minor"},
			{"permalink": "https://example.com/note", "reason": "uri scheme", "priority": "minor"}
		],
		"bm_commands": [],
		"next_action": "go"
	}`
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(withNonBM), Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	d.Cfg.KBStore = "basic-memory"
	h := &handlers{deps: d}

	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, primeArgs())
	require.NoError(t, err)

	// Two synthetic kb_store_mismatch findings should be present (one per non-BM pick).
	var mismatchCount int
	for _, f := range r.Findings {
		if f.Category == verdict.CategoryOther && f.Criterion == "kb_store_mismatch" {
			assert.Equal(t, verdict.SeverityMinor, f.Severity)
			mismatchCount++
		}
	}
	assert.Equal(t, 2, mismatchCount, "expected two kb_store_mismatch findings")
}

func TestPrime_EmptyKBIndex_Accepted(t *testing.T) {
	// Reviewer is expected to emit kb_gap in the wild, but for the handler
	// path we only need to assert that an empty kb_index does NOT trigger
	// a validation error and reaches the reviewer.
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := primeArgs()
	args.KBIndex = nil

	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, r.Verdict)
	assert.Equal(t, 1, rv.Calls)
}

func TestPrime_MaxPicksDefaultsAndCeilings(t *testing.T) {
	// MaxPicks==0 → default 10; MaxPicks>25 → ceiling 25. The rendered prompt
	// carries the effective max_picks value as a "Return at most N picks"
	// line (see prompts/templates/prime.tmpl), so we can assert the
	// effective value via the captured reviewer request body.
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	// MaxPicks==0 → handler uses default 10.
	args := primeArgs()
	args.MaxPicks = 0
	_, _, err := h.PrimeProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Contains(t, rv.LastRequest.User, "at most 10",
		"effective max_picks must be defaultMaxPicks(=10) when caller passes 0")

	// MaxPicks==999 → handler caps at 25.
	args.MaxPicks = 999
	_, _, err = h.PrimeProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Contains(t, rv.LastRequest.User, "at most 25",
		"effective max_picks must be maxMaxPicks(=25) when caller passes 999")
}

func TestPrime_TruncationSurfacesWarnMajor(t *testing.T) {
	// Provider returns ErrResponseTruncated. Handler should synthesise a
	// warn envelope with a single SeverityMajor / category:other /
	// criterion:reviewer_response finding (v0.5.2 ladder posture).
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, primeArgs())
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictWarn, r.Verdict)
	require.Len(t, r.Findings, 1)
	assert.Equal(t, verdict.SeverityMajor, r.Findings[0].Severity)
	assert.Equal(t, verdict.CategoryOther, r.Findings[0].Category)
	assert.Equal(t, "reviewer_response", r.Findings[0].Criterion)
	assert.False(t, r.Partial, "no-analysis truncation must NOT set Partial=true")
	assert.Contains(t, r.Findings[0].Suggestion, "ANTI_TANGENT_PRIME_MAX_TOKENS")
	// Picks/bm_commands present as empty slices (not nil) so JSON marshals as [].
	assert.NotNil(t, r.Picks)
	assert.NotNil(t, r.BMCommands)
	assert.NotEmpty(t, r.SummaryBlock)
}

func TestPrime_ParseRetry_RecoversOnSecondCall(t *testing.T) {
	// First reviewer call returns malformed JSON; second returns valid.
	// reviewPrime should swallow the first parse error, append RetryHint to
	// the User prompt, and return the parsed PrimeResult from the second.
	sr := &scriptedReviewer{
		responses: []providers.Response{
			{RawJSON: []byte(`{ not json`), Model: "claude-sonnet-4-6"},
			{RawJSON: []byte(primePassRaw), Model: "claude-sonnet-4-6"},
		},
	}
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Reviews = providers.Registry{"anthropic": sr}
	h := &handlers{deps: d}

	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, primeArgs())
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, r.Verdict)
	require.Len(t, r.Picks, 1)
	assert.Equal(t, 2, sr.calls, "reviewPrime must retry exactly once on malformed JSON")
}

func TestPrime_ParseRetry_ExhaustedReturnsError(t *testing.T) {
	// Both reviewer calls return malformed JSON. reviewPrime should return
	// the wrapped "prime provider response failed schema after retry" error.
	sr := &scriptedReviewer{
		responses: []providers.Response{
			{RawJSON: []byte(`{ not json`), Model: "claude-sonnet-4-6"},
			{RawJSON: []byte(`also { not } json`), Model: "claude-sonnet-4-6"},
		},
	}
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Reviews = providers.Registry{"anthropic": sr}
	h := &handlers{deps: d}

	_, _, err := h.PrimeProjectKnowledge(context.Background(), nil, primeArgs())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prime provider response failed schema after retry")
	assert.Equal(t, 2, sr.calls)
}

func TestPrime_ClampFindingPrependedOnOverrideExceedingCeiling(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := primeArgs()
	// Push override over ceiling so effectiveMaxTokens emits a clamp finding.
	args.MaxTokensOverride = d.Cfg.MaxTokensCeiling + 1000

	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	// Clamp finding sits at the head of Findings (prependPrimeClamp posture).
	require.NotEmpty(t, r.Findings)
	assert.Equal(t, verdict.SeverityMinor, r.Findings[0].Severity)
	assert.Equal(t, verdict.CategoryOther, r.Findings[0].Category)
	assert.Equal(t, "max_tokens_override", r.Findings[0].Criterion)
	// Clamp must NOT modify next_action.
	assert.NotContains(t, r.NextAction, "max_tokens_override")
}

func TestPrime_ClampPropagatesOnTooLargePayload(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 50
	h := &handlers{deps: d}

	// Combine: override-over-ceiling AND oversized payload. The clamp
	// finding should still appear at the head of the synthetic too-large
	// envelope's findings.
	args := PrimeProjectKnowledgeArgs{
		TaskTitle:          "Implement an example task with a slightly longer title",
		Goal:               "A goal whose text alone is bigger than fifty bytes already",
		AcceptanceCriteria: []string{"AC1 with a fairly long description"},
		MaxTokensOverride:  d.Cfg.MaxTokensCeiling + 1000,
	}
	_, r, err := h.PrimeProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(r.Findings), 2, "expect clamp + too-large findings")
	assert.Equal(t, "max_tokens_override", r.Findings[0].Criterion, "clamp must be first")
	assert.Equal(t, verdict.CategoryTooLarge, r.Findings[1].Category, "too-large must follow clamp")
	assert.Equal(t, verdict.VerdictFail, r.Verdict)
	// Reviewer was not called.
	assert.Equal(t, 0, rv.Calls)
}

// TestPrime_LogEmitsOnEveryExitPath asserts that every handler exit path emits
// exactly one structured JSON log line on stderr (the deferred-logger posture
// backported from extract_handler.go after CodeRabbit flagged that the
// pre-deferred-logger prime path only logged on success). Spec §5.6 requires
// the line; the deferred logger ensures synthetic refusals (payload too large)
// and truncation paths log just like the success path.
func TestPrime_LogEmitsOnEveryExitPath(t *testing.T) {
	cases := []struct {
		name        string
		setup       func(t *testing.T) (*handlers, PrimeProjectKnowledgeArgs)
		wantOutcome string
		wantVerdict string
	}{
		{
			name: "success",
			setup: func(t *testing.T) (*handlers, PrimeProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
				d := newDeps(t, rv)
				return &handlers{deps: d}, primeArgs()
			},
			wantOutcome: "success",
			wantVerdict: "pass",
		},
		{
			name: "payload_too_large",
			setup: func(t *testing.T) (*handlers, PrimeProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
				d := newDeps(t, rv)
				d.Cfg.MaxPayloadBytes = 50
				args := PrimeProjectKnowledgeArgs{
					TaskTitle:          "Implement an example task with a slightly longer title",
					Goal:               "A goal whose text alone is bigger than fifty bytes already",
					AcceptanceCriteria: []string{"AC1 with a fairly long description"},
				}
				return &handlers{deps: d}, args
			},
			wantOutcome: "payload_too_large",
			wantVerdict: "fail",
		},
		{
			name: "truncated",
			setup: func(t *testing.T) (*handlers, PrimeProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
				d := newDeps(t, rv)
				return &handlers{deps: d}, primeArgs()
			},
			wantOutcome: "truncated",
			wantVerdict: "warn",
		},
		{
			name: "validation_error",
			setup: func(t *testing.T) (*handlers, PrimeProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
				d := newDeps(t, rv)
				return &handlers{deps: d}, PrimeProjectKnowledgeArgs{Goal: "g", AcceptanceCriteria: []string{"a"}}
			},
			wantOutcome: "validation_error",
			wantVerdict: "fail",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
			defer slog.SetDefault(prev)
			h, args := tc.setup(t)
			_, _, _ = h.PrimeProjectKnowledge(context.Background(), nil, args)

			// Exactly one log line per call (split on \n, drop the trailing empty entry).
			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			require.Len(t, lines, 1, "expected exactly one structured log line, got %d:\n%s", len(lines), buf.String())

			var rec map[string]any
			require.NoError(t, json.Unmarshal([]byte(lines[0]), &rec))
			assert.Equal(t, "prime_project_knowledge", rec["tool"])
			assert.Equal(t, tc.wantOutcome, rec["outcome"])
			assert.Equal(t, tc.wantVerdict, rec["verdict"])
		})
	}
}

func TestPrime_TooLargeUsesConfiguredModel_NotResolvedOverride(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: primePassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 50
	h := &handlers{deps: d}

	// model_override is set, but the payload-cap rejection must happen
	// BEFORE resolveModel, so the synthetic envelope should cite cfg.PrimeModel.
	args := PrimeProjectKnowledgeArgs{
		TaskTitle:          "Implement an example task with a slightly longer title",
		Goal:               "A goal whose text alone is bigger than fifty bytes already",
		AcceptanceCriteria: []string{"AC1 with a fairly long description"},
		ModelOverride:      "openai:gpt-4o-mini",
	}
	out, _, err := h.PrimeProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.Len(t, out.Content, 1)
	tc, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, d.Cfg.PrimeModel.String(),
		"too-large envelope must cite cfg.PrimeModel even when ModelOverride was set")
}
