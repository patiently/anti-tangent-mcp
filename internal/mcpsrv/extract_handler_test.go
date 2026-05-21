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

// extractPassRaw is a canned reviewer response that satisfies ParseExtract:
// one create proposal with all required fields present, no findings, empty
// bm_commands, and a non-empty next_action.
const extractPassRaw = `{
	"verdict": "pass",
	"findings": [],
	"proposals": [
		{
			"action": "create",
			"type": "decision",
			"permalink": "decisions/0099-cache-pass",
			"title": "Add cache pass",
			"frontmatter_json": "{}",
			"body": "## Context\n\nWe needed caching.",
			"body_patch": "",
			"rationale": "Capture the caching decision.",
			"evidence_refs": ["completion_envelopes[0].summary"],
			"supersedes": []
		}
	],
	"bm_commands": [],
	"next_action": "review and merge the decision note"
}`

func extractPassResp(model string) providers.Response {
	return providers.Response{
		RawJSON:     []byte(extractPassRaw),
		Model:       model,
		InputTokens: 3, OutputTokens: 2,
	}
}

func extractArgs() ExtractProjectKnowledgeArgs {
	return ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{
				TaskTitle:    "Implement caching",
				Summary:      "added a cache pass",
				Verdict:      "pass",
				FinalDiff:    "diff --git a/c.go b/c.go\n@@ -0,0 +1 @@\n+package c\n",
				TestEvidence: "go test ./...  PASS",
			},
		},
	}
}

func TestExtract_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, r.Verdict)
	require.Len(t, r.Proposals, 1)
	assert.Equal(t, verdict.ProposalActionCreate, r.Proposals[0].Action)
	assert.Equal(t, "decisions/0099-cache-pass", r.Proposals[0].Permalink)
	// KBStore=="" → bm_commands stripped to empty (non-nil) slice.
	assert.NotNil(t, r.BMCommands)
	assert.Len(t, r.BMCommands, 0)
	// SummaryBlock populated and contains envelope identification + proposal.
	assert.NotEmpty(t, r.SummaryBlock)
	assert.Contains(t, r.SummaryBlock, "extract_project_knowledge")
	assert.Contains(t, r.SummaryBlock, "decisions/0099-cache-pass")
	// Exactly one reviewer call on happy path.
	assert.Equal(t, 1, rv.Calls)
}

func TestExtract_EnvelopeShapeParity(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	out, r, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.NoError(t, err)
	require.Len(t, out.Content, 1)
	tc, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	// Decode the JSON wire envelope: ExtractResult body PLUS model_used /
	// review_ms siblings (the wrapper shape extractEnvelopeResult marshals).
	var wire struct {
		verdict.ExtractResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &wire))

	assert.Equal(t, "anthropic:claude-sonnet-4-6", wire.ModelUsed)
	assert.GreaterOrEqual(t, wire.ReviewMS, int64(0))
	assert.NotEmpty(t, wire.SummaryBlock)
	assert.Equal(t, r.Verdict, wire.Verdict)
	assert.Len(t, wire.Proposals, 1)
}

func TestExtract_EmptyEnvelopes_SyntheticRefusal(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, ExtractProjectKnowledgeArgs{})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, r.Verdict)
	require.Len(t, r.Findings, 1)
	assert.Equal(t, verdict.SeverityCritical, r.Findings[0].Severity)
	assert.Equal(t, verdict.CategoryInsufficientEvidence, r.Findings[0].Category)
	// Reviewer was NOT called for the empty-envelopes refusal.
	assert.Equal(t, 0, rv.Calls)
	// Required slices are present as empty arrays (load-bearing for strict schema).
	assert.NotNil(t, r.Proposals)
	assert.NotNil(t, r.BMCommands)
	assert.Len(t, r.Proposals, 0)
	assert.Len(t, r.BMCommands, 0)
}

// TestExtract_SyntheticRefusal_JSONRoundTrip asserts that a synthetic refusal
// envelope marshals with "proposals":[] / "bm_commands":[] — never null. The
// strict-schema invariant requires non-null arrays; a nil Go slice would
// regress this contract.
func TestExtract_SyntheticRefusal_JSONRoundTrip(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	out, _, err := h.ExtractProjectKnowledge(context.Background(), nil, ExtractProjectKnowledgeArgs{})
	require.NoError(t, err)
	require.Len(t, out.Content, 1)
	tc, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, `"proposals": []`)
	assert.Contains(t, tc.Text, `"bm_commands": []`)
	assert.NotContains(t, tc.Text, `"proposals": null`)
	assert.NotContains(t, tc.Text, `"bm_commands": null`)
}

func TestExtract_OversizedPayload_TooLargeCritical(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 100 // tiny cap so a normal call exceeds it
	h := &handlers{deps: d}

	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{
				TaskTitle:    "Some task with a fairly long title",
				Summary:      strings.Repeat("x", 200),
				Verdict:      "pass",
				TestEvidence: "go test ./... PASS",
			},
		},
	}
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, r.Verdict)
	require.Len(t, r.Findings, 1)
	// v0.5.2 reconciliation: too-large is SeverityCritical (NOT major).
	assert.Equal(t, verdict.SeverityCritical, r.Findings[0].Severity)
	assert.Equal(t, verdict.CategoryTooLarge, r.Findings[0].Category)
	assert.Equal(t, 0, rv.Calls)
	// Required empty slices initialised.
	assert.NotNil(t, r.Proposals)
	assert.NotNil(t, r.BMCommands)
}

func TestExtract_AllEnvelopesBare_SyntheticRefusal(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{TaskTitle: "Bare 1", Summary: "no evidence", Verdict: "pass"},
			{TaskTitle: "Bare 2", Summary: "no evidence", Verdict: "pass"},
		},
	}
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, r.Verdict)
	require.Len(t, r.Findings, 2, "one insufficient_evidence finding per bare envelope")
	for _, f := range r.Findings {
		assert.Equal(t, verdict.SeverityMajor, f.Severity)
		assert.Equal(t, verdict.CategoryInsufficientEvidence, f.Category)
	}
	assert.Equal(t, 0, rv.Calls, "reviewer not called when every envelope is evidence-bare")
}

func TestExtract_OnlyTestEvidence_NotBare(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{TaskTitle: "Tests-only", Summary: "added tests", Verdict: "pass", TestEvidence: "go test ./... PASS"},
		},
	}
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, r.Verdict)
	assert.Equal(t, 1, rv.Calls)
	// No insufficient_evidence pre-finding should have been emitted.
	for _, f := range r.Findings {
		assert.NotEqual(t, verdict.CategoryInsufficientEvidence, f.Category)
	}
}

func TestExtract_MixedEnvelopes_PreFindingsPrepended(t *testing.T) {
	// Reviewer returns one proposal with no findings. We supply one envelope
	// with real evidence + one evidence-bare envelope; the handler should
	// dispatch the reviewer AND prepend an insufficient_evidence finding for
	// the bare envelope so the caller sees it above reviewer findings.
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{
				TaskTitle:    "Has evidence",
				Summary:      "did something",
				Verdict:      "pass",
				TestEvidence: "go test ./... PASS",
			},
			{
				TaskTitle: "Bare envelope",
				Summary:   "nothing to show",
				Verdict:   "pass",
			},
		},
	}
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.NotEmpty(t, r.Findings)
	// Pre-finding for the bare envelope sits at the head.
	assert.Equal(t, verdict.CategoryInsufficientEvidence, r.Findings[0].Category)
	assert.Equal(t, verdict.SeverityMajor, r.Findings[0].Severity)
	assert.Contains(t, r.Findings[0].Criterion, "completion_envelopes[1]")
	// Reviewer was dispatched.
	assert.Equal(t, 1, rv.Calls)
	// Proposals from the reviewer survived.
	require.Len(t, r.Proposals, 1)
}

func TestExtract_TruncatedDiff_WithTestEvidence_QualitySubFinding(t *testing.T) {
	// One envelope: final_diff has a truncation marker AND test_evidence is
	// populated. The envelope counts as having evidence (test evidence
	// grounds the reviewer), but a `quality` sub-finding should accumulate
	// describing the malformed diff. Reviewer IS dispatched.
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{
				TaskTitle:    "Truncated diff",
				Summary:      "did something",
				Verdict:      "pass",
				FinalDiff:    "diff --git a/c.go b/c.go\n@@\n// ... unchanged\n",
				TestEvidence: "go test ./... PASS",
			},
		},
	}
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, 1, rv.Calls, "reviewer must still be dispatched when test_evidence grounds the envelope")
	// Find the quality sub-finding for the envelope.
	var found bool
	for _, f := range r.Findings {
		if f.Category == verdict.CategoryQuality && strings.Contains(f.Criterion, "completion_envelopes[0]") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a quality sub-finding citing the truncated diff")
}

func TestExtract_TruncatedDiff_NoTestEvidence_AllBareRefusal(t *testing.T) {
	// One envelope: final_diff has a truncation marker, no test_evidence.
	// The diff/files are unusable; envelope counts as evidence-bare; with
	// only one envelope this triggers the all-bare refusal short-circuit.
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{
				TaskTitle: "Truncated, no tests",
				Summary:   "did something",
				Verdict:   "pass",
				FinalDiff: "diff --git a/c.go b/c.go\n@@\n// ... unchanged\n",
			},
		},
	}
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, r.Verdict)
	require.Len(t, r.Findings, 1)
	assert.Equal(t, verdict.CategoryInsufficientEvidence, r.Findings[0].Category)
	assert.Contains(t, r.Findings[0].Evidence, "truncation marker")
	assert.Equal(t, 0, rv.Calls)
}

func TestExtract_BadModelOverride_AllBare_StillRefuses(t *testing.T) {
	// Even with a malformed model_override, an all-evidence-bare envelope
	// list must still produce the insufficient_evidence refusal — NOT a
	// model-validation error. This is the load-bearing ordering: evidence
	// accounting runs BEFORE model resolution.
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{TaskTitle: "Bare", Summary: "nothing"},
		},
		ModelOverride: "not-a-valid-model::ref",
	}
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err, "must not surface model-validation error when evidence-bare")
	assert.Equal(t, verdict.VerdictFail, r.Verdict)
	require.NotEmpty(t, r.Findings)
	assert.Equal(t, verdict.CategoryInsufficientEvidence, r.Findings[0].Category)
	assert.Equal(t, 0, rv.Calls)
}

func TestExtract_KBStoreEmpty_StripsBMCommands(t *testing.T) {
	// Reviewer returns one valid bm_command; with KBStore="" the handler
	// must replace bm_commands with an empty (non-nil) slice.
	withBM := `{
		"verdict": "pass",
		"findings": [],
		"proposals": [
			{
				"action": "create",
				"type": "decision",
				"permalink": "decisions/0099",
				"title": "x",
				"frontmatter_json": "{}",
				"body": "body",
				"body_patch": "",
				"rationale": "r",
				"evidence_refs": ["completion_envelopes[0].summary"],
				"supersedes": []
			}
		],
		"bm_commands": [
			{"tool":"write_note","args_json":"{\"permalink\":\"decisions/0099\"}"}
		],
		"next_action": "go"
	}`
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(withBM), Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	require.Equal(t, "", d.Cfg.KBStore)
	h := &handlers{deps: d}

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.NoError(t, err)
	require.NotNil(t, r.BMCommands)
	assert.Len(t, r.BMCommands, 0, "KBStore=='' must strip bm_commands to an empty slice")
}

func TestExtract_KBStoreBasicMemory_PreservesBMCommands(t *testing.T) {
	// Reviewer returns one valid BM-shaped command; with KBStore="basic-memory"
	// the handler must preserve bm_commands unchanged.
	withBM := `{
		"verdict": "pass",
		"findings": [],
		"proposals": [
			{
				"action": "create",
				"type": "decision",
				"permalink": "decisions/0099",
				"title": "x",
				"frontmatter_json": "{}",
				"body": "body",
				"body_patch": "",
				"rationale": "r",
				"evidence_refs": ["completion_envelopes[0].summary"],
				"supersedes": []
			}
		],
		"bm_commands": [
			{"tool":"write_note","args_json":"{\"permalink\":\"decisions/0099\"}"}
		],
		"next_action": "go"
	}`
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(withBM), Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	d.Cfg.KBStore = "basic-memory"
	h := &handlers{deps: d}

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.NoError(t, err)
	require.Len(t, r.BMCommands, 1)
	assert.Equal(t, "write_note", r.BMCommands[0].Tool)
}

func TestExtract_KBStoreBasicMemory_EmitsKBStoreMismatch_OnProposalPermalink(t *testing.T) {
	// Reviewer emits a proposal whose permalink starts with "/" (non-BM
	// shape). With KBStore="basic-memory" the handler appends a minor
	// kb_store_mismatch finding.
	withNonBM := `{
		"verdict": "pass",
		"findings": [],
		"proposals": [
			{
				"action": "create",
				"type": "decision",
				"permalink": "/abs/path/decisions/0099",
				"title": "x",
				"frontmatter_json": "{}",
				"body": "b",
				"body_patch": "",
				"rationale": "r",
				"evidence_refs": ["completion_envelopes[0].summary"],
				"supersedes": []
			}
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

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.NoError(t, err)
	var mismatch int
	for _, f := range r.Findings {
		if f.Category == verdict.CategoryOther && f.Criterion == "kb_store_mismatch" {
			assert.Equal(t, verdict.SeverityMinor, f.Severity)
			mismatch++
		}
	}
	assert.Equal(t, 1, mismatch, "expected one kb_store_mismatch finding for the non-BM proposal permalink")
}

func TestExtract_KBStoreBasicMemory_EmitsKBStoreMismatch_OnBMCommandPermalink(t *testing.T) {
	// Reviewer emits a BM-shaped proposal permalink, but the bm_command
	// args_json carries a non-BM-shape permalink (URI scheme). With
	// KBStore="basic-memory" the handler appends a minor kb_store_mismatch
	// finding for the bm_command permalink.
	withNonBM := `{
		"verdict": "pass",
		"findings": [],
		"proposals": [
			{
				"action": "create",
				"type": "decision",
				"permalink": "decisions/0099",
				"title": "x",
				"frontmatter_json": "{}",
				"body": "b",
				"body_patch": "",
				"rationale": "r",
				"evidence_refs": ["completion_envelopes[0].summary"],
				"supersedes": []
			}
		],
		"bm_commands": [
			{"tool":"write_note","args_json":"{\"permalink\":\"https://example.com/note\"}"}
		],
		"next_action": "go"
	}`
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(withNonBM), Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	d.Cfg.KBStore = "basic-memory"
	h := &handlers{deps: d}

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.NoError(t, err)
	var mismatch int
	for _, f := range r.Findings {
		if f.Category == verdict.CategoryOther && f.Criterion == "kb_store_mismatch" {
			assert.Equal(t, verdict.SeverityMinor, f.Severity)
			mismatch++
		}
	}
	assert.Equal(t, 1, mismatch, "expected one kb_store_mismatch finding for the non-BM bm_command permalink")
}

func TestExtract_TruncationSurfacesWarnMajor(t *testing.T) {
	// Provider returns ErrResponseTruncated. Handler synthesises a warn
	// envelope with one SeverityMajor / other / reviewer_response finding
	// (v0.5.2 ladder posture — NOT SeverityMinor as the plan snippet text
	// might imply).
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictWarn, r.Verdict)
	require.Len(t, r.Findings, 1)
	assert.Equal(t, verdict.SeverityMajor, r.Findings[0].Severity)
	assert.Equal(t, verdict.CategoryOther, r.Findings[0].Category)
	assert.Equal(t, "reviewer_response", r.Findings[0].Criterion)
	assert.False(t, r.Partial, "no-analysis truncation must NOT set Partial=true")
	assert.Contains(t, r.Findings[0].Suggestion, "ANTI_TANGENT_EXTRACT_MAX_TOKENS")
	assert.NotNil(t, r.Proposals)
	assert.NotNil(t, r.BMCommands)
	assert.NotEmpty(t, r.SummaryBlock)
}

func TestExtract_ParseRetry_RecoversOnSecondCall(t *testing.T) {
	// First reviewer call returns malformed JSON; second returns valid.
	// reviewExtract should swallow the first parse error, append RetryHint
	// to the User prompt, and return the parsed ExtractResult from the
	// second.
	sr := &scriptedReviewer{
		responses: []providers.Response{
			{RawJSON: []byte(`{ not json`), Model: "claude-sonnet-4-6"},
			{RawJSON: []byte(extractPassRaw), Model: "claude-sonnet-4-6"},
		},
	}
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Reviews = providers.Registry{"anthropic": sr}
	h := &handlers{deps: d}

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, r.Verdict)
	require.Len(t, r.Proposals, 1)
	assert.Equal(t, 2, sr.calls, "reviewExtract must retry exactly once on malformed JSON")
}

func TestExtract_ParseRetry_ExhaustedReturnsError(t *testing.T) {
	// Both reviewer calls return malformed JSON.
	sr := &scriptedReviewer{
		responses: []providers.Response{
			{RawJSON: []byte(`{ not json`), Model: "claude-sonnet-4-6"},
			{RawJSON: []byte(`also { not } json`), Model: "claude-sonnet-4-6"},
		},
	}
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Reviews = providers.Registry{"anthropic": sr}
	h := &handlers{deps: d}

	_, _, err := h.ExtractProjectKnowledge(context.Background(), nil, extractArgs())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extract provider response failed schema after retry")
	assert.Equal(t, 2, sr.calls)
}

func TestExtract_ClampFindingPrependedOnOverrideExceedingCeiling(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	args := extractArgs()
	// Push override over ceiling so effectiveMaxTokens emits a clamp finding.
	args.MaxTokensOverride = d.Cfg.MaxTokensCeiling + 1000

	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.NotEmpty(t, r.Findings)
	assert.Equal(t, verdict.SeverityMinor, r.Findings[0].Severity)
	assert.Equal(t, verdict.CategoryOther, r.Findings[0].Category)
	assert.Equal(t, "max_tokens_override", r.Findings[0].Criterion)
	assert.NotContains(t, r.NextAction, "max_tokens_override")
}

func TestExtract_ClampPropagatesOnTooLargePayload(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 100
	h := &handlers{deps: d}

	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{
				TaskTitle:    "Some task with a fairly long title",
				Summary:      strings.Repeat("x", 200),
				Verdict:      "pass",
				TestEvidence: "go test ./... PASS",
			},
		},
		MaxTokensOverride: d.Cfg.MaxTokensCeiling + 1000,
	}
	_, r, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(r.Findings), 2, "expect clamp + too-large findings")
	assert.Equal(t, "max_tokens_override", r.Findings[0].Criterion, "clamp must be first")
	assert.Equal(t, verdict.CategoryTooLarge, r.Findings[1].Category, "too-large must follow clamp")
	assert.Equal(t, verdict.VerdictFail, r.Verdict)
	assert.Equal(t, 0, rv.Calls)
}

// TestExtract_LogEmitsOnEveryExitPath asserts that every handler exit path
// emits exactly one structured JSON log line on stderr (the deferred-logger
// posture). Spec §5.6 requires the line; the deferred logger ensures synthetic
// refusals (empty envelopes, all-bare, payload too large) and truncation
// paths log just like the success path.
func TestExtract_LogEmitsOnEveryExitPath(t *testing.T) {
	cases := []struct {
		name          string
		setup         func(t *testing.T) (*handlers, ExtractProjectKnowledgeArgs)
		wantOutcome   string
		wantVerdict   string
		wantEnvelopes int
	}{
		{
			name: "success",
			setup: func(t *testing.T) (*handlers, ExtractProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
				d := newDeps(t, rv)
				return &handlers{deps: d}, extractArgs()
			},
			wantOutcome:   "success",
			wantVerdict:   "pass",
			wantEnvelopes: 1,
		},
		{
			name: "empty_envelopes",
			setup: func(t *testing.T) (*handlers, ExtractProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
				d := newDeps(t, rv)
				return &handlers{deps: d}, ExtractProjectKnowledgeArgs{}
			},
			wantOutcome:   "empty_envelopes",
			wantVerdict:   "fail",
			wantEnvelopes: 0,
		},
		{
			name: "payload_too_large",
			setup: func(t *testing.T) (*handlers, ExtractProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
				d := newDeps(t, rv)
				d.Cfg.MaxPayloadBytes = 100
				args := ExtractProjectKnowledgeArgs{
					CompletionEnvelopes: []CompletionEnvelopeArg{
						{TaskTitle: "x", Summary: strings.Repeat("y", 200), Verdict: "pass", TestEvidence: "PASS"},
					},
				}
				return &handlers{deps: d}, args
			},
			wantOutcome:   "payload_too_large",
			wantVerdict:   "fail",
			wantEnvelopes: 1,
		},
		{
			name: "all_envelopes_bare",
			setup: func(t *testing.T) (*handlers, ExtractProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
				d := newDeps(t, rv)
				args := ExtractProjectKnowledgeArgs{
					CompletionEnvelopes: []CompletionEnvelopeArg{{TaskTitle: "bare", Summary: "n", Verdict: "pass"}},
				}
				return &handlers{deps: d}, args
			},
			wantOutcome:   "all_envelopes_bare",
			wantVerdict:   "fail",
			wantEnvelopes: 1,
		},
		{
			name: "truncated",
			setup: func(t *testing.T) (*handlers, ExtractProjectKnowledgeArgs) {
				rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
				d := newDeps(t, rv)
				return &handlers{deps: d}, extractArgs()
			},
			wantOutcome:   "truncated",
			wantVerdict:   "warn",
			wantEnvelopes: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
			defer slog.SetDefault(prev)
			h, args := tc.setup(t)
			_, _, _ = h.ExtractProjectKnowledge(context.Background(), nil, args)

			// Exactly one log line per call (split on \n, drop the trailing empty entry).
			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			require.Len(t, lines, 1, "expected exactly one structured log line, got %d:\n%s", len(lines), buf.String())

			var rec map[string]any
			require.NoError(t, json.Unmarshal([]byte(lines[0]), &rec))
			assert.Equal(t, "extract_project_knowledge", rec["tool"])
			assert.Equal(t, tc.wantOutcome, rec["outcome"])
			assert.Equal(t, tc.wantVerdict, rec["verdict"])
			assert.EqualValues(t, tc.wantEnvelopes, rec["envelopes"])
		})
	}
}

func TestExtract_TooLargeUsesConfiguredModel_NotResolvedOverride(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: extractPassResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 100
	h := &handlers{deps: d}

	// model_override is set, but the payload-cap rejection must happen
	// BEFORE resolveModel, so the synthetic envelope cites cfg.ExtractModel.
	args := ExtractProjectKnowledgeArgs{
		CompletionEnvelopes: []CompletionEnvelopeArg{
			{
				TaskTitle:    "Some task with a fairly long title",
				Summary:      strings.Repeat("x", 200),
				Verdict:      "pass",
				TestEvidence: "go test ./... PASS",
			},
		},
		ModelOverride: "openai:gpt-4o-mini",
	}
	out, _, err := h.ExtractProjectKnowledge(context.Background(), nil, args)
	require.NoError(t, err)
	require.Len(t, out.Content, 1)
	tc, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, d.Cfg.ExtractModel.String(),
		"too-large envelope must cite cfg.ExtractModel even when ModelOverride was set")
}
