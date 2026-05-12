package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
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

type fakeReviewer struct {
	name  string
	resp  providers.Response
	err   error
	Calls int
}

func (f *fakeReviewer) Name() string { return f.name }
func (f *fakeReviewer) Review(ctx context.Context, _ providers.Request) (providers.Response, error) {
	f.Calls++
	if f.err != nil {
		return providers.Response{}, f.err
	}
	return f.resp, nil
}

func passResp(model string) providers.Response {
	return providers.Response{
		RawJSON:     []byte(`{"verdict":"pass","findings":[],"next_action":"go"}`),
		Model:       model,
		InputTokens: 3, OutputTokens: 2,
	}
}

func newDeps(t *testing.T, rv *fakeReviewer) Deps {
	cfg, err := config.Load(func(k string) string {
		switch k {
		case "ANTHROPIC_API_KEY":
			return "k"
		}
		return ""
	})
	require.NoError(t, err)
	return Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(1 * time.Hour),
		Reviews:  providers.Registry{"anthropic": rv},
	}
}

func TestValidateTaskSpec_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	out, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "X",
		Goal:               "Y",
		AcceptanceCriteria: []string{"AC1"},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "pass", env.Verdict)
	assert.NotEmpty(t, env.SessionID)
	assert.Equal(t, "anthropic:claude-sonnet-4-6", env.ModelUsed)

	// Session was actually created.
	_, ok := d.Sessions.Get(env.SessionID)
	assert.True(t, ok)

	// TTL fields are populated on successful creation.
	require.NotNil(t, env.SessionExpiresAt)
	require.NotNil(t, env.SessionTTLRemainingSeconds)
	assert.Greater(t, *env.SessionTTLRemainingSeconds, 0)

	// And out.Content includes a TextContent with the JSON form of the envelope.
	require.Len(t, out.Content, 1)
	tc, ok := out.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, env.SessionID)
}

func TestEnvelope_SessionTTLFieldsSerializeCorrectly(t *testing.T) {
	t.Run("fields present when set", func(t *testing.T) {
		ts := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
		remaining := 3600
		env := Envelope{
			SessionID:                  "abc",
			Verdict:                    "pass",
			Findings:                   []verdict.Finding{},
			NextAction:                 "go",
			ModelUsed:                  "m",
			ReviewMS:                   10,
			SessionExpiresAt:           &ts,
			SessionTTLRemainingSeconds: &remaining,
		}
		b, err := json.Marshal(env)
		require.NoError(t, err)
		assert.Contains(t, string(b), `"session_expires_at"`)
		assert.Contains(t, string(b), `"session_ttl_remaining_seconds"`)
		assert.Contains(t, string(b), `"2030-01-01T12:00:00Z"`)
		assert.Contains(t, string(b), `3600`)
	})

	t.Run("fields absent when nil (omitempty)", func(t *testing.T) {
		env := Envelope{
			SessionID:  "abc",
			Verdict:    "fail",
			Findings:   []verdict.Finding{},
			NextAction: "fix",
			ModelUsed:  "m",
			ReviewMS:   5,
		}
		b, err := json.Marshal(env)
		require.NoError(t, err)
		assert.NotContains(t, string(b), `"session_expires_at"`)
		assert.NotContains(t, string(b), `"session_ttl_remaining_seconds"`)
	})

	t.Run("remaining seconds clamped to zero (not negative)", func(t *testing.T) {
		// Use a real store and a session whose LastAccessed is 2h in the past so
		// the computed expiry (LastAccessed + TTL) lies in the past, exercising
		// the clamp branch inside withSessionTTL.
		store := session.NewStore(1 * time.Hour)
		sess := store.Create(session.TaskSpec{Title: "t", Goal: "g"})
		sess.LastAccessed = time.Now().Add(-2 * time.Hour)

		h := &handlers{deps: Deps{Sessions: store}}
		env := h.withSessionTTL(Envelope{SessionID: "abc"}, sess)

		require.NotNil(t, env.SessionTTLRemainingSeconds)
		assert.Equal(t, 0, *env.SessionTTLRemainingSeconds)

		b, err := json.Marshal(env)
		require.NoError(t, err)
		assert.Contains(t, string(b), `"session_ttl_remaining_seconds":0`)
	})
}

func TestValidateTaskSpec_ProviderError(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: errors.New("boom")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "X", Goal: "Y",
	})
	require.Error(t, err)
}

func TestValidateTaskSpec_MissingFields(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: newDeps(t, rv)}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{Goal: "Y"})
	require.Error(t, err)
}

func TestCheckProgress_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-haiku-4-5-20251001")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	// Pre-create a session so check_progress has something to thread.
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	out, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID:    pre.SessionID,
		WorkingOn:    "writing handler",
		ChangedFiles: []FileArg{{Path: "h.go", Content: "package h\n"}},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, pre.SessionID, env.SessionID)
	assert.Equal(t, "pass", env.Verdict)

	// TTL fields are populated on successful progress check.
	require.NotNil(t, env.SessionExpiresAt)
	require.NotNil(t, env.SessionTTLRemainingSeconds)
	assert.Greater(t, *env.SessionTTLRemainingSeconds, 0)

	// A checkpoint was appended.
	got, _ := d.Sessions.Get(env.SessionID)
	require.Len(t, got.Checkpoints, 1)
}

func TestCheckProgress_UnknownSession(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-haiku-4-5-20251001")}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: "does-not-exist", WorkingOn: "x",
	})
	require.NoError(t, err)
	assert.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, "session_not_found", string(env.Findings[0].Category))
}

func TestCheckProgress_PayloadTooLarge(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-haiku-4-5-20251001")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 10
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)

	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID:    pre.SessionID,
		WorkingOn:    "x",
		ChangedFiles: []FileArg{{Path: "f", Content: "this is way too much"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, "payload_too_large", string(env.Findings[0].Category))
	assert.Contains(t, env.Findings[0].Suggestion, "smaller changed_files set")
	assert.Contains(t, env.Findings[0].Suggestion, "split")
	assert.NotContains(t, env.Findings[0].Suggestion, "final_diff")
	// Evidence must still include actual size and cap values.
	assert.Contains(t, env.Findings[0].Evidence, "bytes")
	assert.Contains(t, env.Findings[0].Evidence, "10")
}

func TestValidateCompletion_PayloadTooLargeSuggestsFinalDiff(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 10
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}})
	require.NoError(t, err)

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "implemented",
		FinalFiles: []FileArg{{Path: "f.go", Content: "this is way too much"}},
	})
	require.NoError(t, err)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, "payload_too_large", string(env.Findings[0].Category))
	assert.Contains(t, env.Findings[0].Suggestion, "final_diff")
	assert.Contains(t, env.Findings[0].Suggestion, "split")
	// Evidence must still include actual size and cap values.
	assert.Contains(t, env.Findings[0].Evidence, "bytes")
	assert.Contains(t, env.Findings[0].Evidence, "10")
}

func TestValidateCompletion_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "implemented X",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n"}},
	})
	require.NoError(t, err)
	assert.Equal(t, pre.SessionID, env.SessionID)
	assert.Equal(t, "pass", env.Verdict)

	// TTL fields are populated on successful completion.
	require.NotNil(t, env.SessionExpiresAt)
	require.NotNil(t, env.SessionTTLRemainingSeconds)
	assert.Greater(t, *env.SessionTTLRemainingSeconds, 0)

	got, _ := d.Sessions.Get(pre.SessionID)
	assert.NotNil(t, got.PostFindings)
}

func TestValidateCompletion_UnknownSession(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:    "missing",
		Summary:      "x",
		TestEvidence: "go test ./... PASS",
	})
	require.NoError(t, err)
	assert.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, "session_not_found", string(env.Findings[0].Category))
}

func TestValidateCompletion_FinalDiffOnly(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "Implemented AC in diff.",
		FinalDiff: "diff --git a/f.go b/f.go\n+@@\n++package f\n",
	})
	require.NoError(t, err)
	assert.Equal(t, "pass", env.Verdict)
}

func TestValidateCompletion_RejectsAllEmptyEvidence(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "Did stuff but didn't provide evidence.",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "final_files")
	assert.Contains(t, err.Error(), "final_diff")
	assert.Contains(t, err.Error(), "test_evidence")
}

func TestValidateTaskSpec_TruncatedResponseSurfacesWarn(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G",
	})
	require.NoError(t, err)
	assert.Equal(t, "warn", env.Verdict)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, verdict.CategoryOther, env.Findings[0].Category)
	assert.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	assert.Contains(t, env.Findings[0].Suggestion, "ANTI_TANGENT_PER_TASK_MAX_TOKENS")

	// No session should be created on truncation.
	assert.Empty(t, env.SessionID)
}

func TestCheckProgress_TruncatedResponseSurfacesWarn(t *testing.T) {
	// First call succeeds (ValidateTaskSpec), second call truncates (CheckProgress).
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G",
	})
	require.NoError(t, err)

	// Now override the reviewer on h.deps directly to return truncation.
	h.deps.Reviews = providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}}

	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID:    pre.SessionID,
		WorkingOn:    "implementing X",
		ChangedFiles: []FileArg{{Path: "f.go", Content: "package f\n"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "warn", env.Verdict)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, verdict.CategoryOther, env.Findings[0].Category)
	assert.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	assert.Contains(t, env.Findings[0].Suggestion, "ANTI_TANGENT_PER_TASK_MAX_TOKENS")
	assert.Equal(t, pre.SessionID, env.SessionID)
}

func TestValidateCompletion_TruncatedResponseSurfacesWarn(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	// Now override the reviewer on h.deps directly to return truncation.
	h.deps.Reviews = providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}}

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "done",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "warn", env.Verdict)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, verdict.CategoryOther, env.Findings[0].Category)
	assert.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	assert.Contains(t, env.Findings[0].Suggestion, "ANTI_TANGENT_PER_TASK_MAX_TOKENS")
	assert.Equal(t, pre.SessionID, env.SessionID)
}

func TestValidatePlan_TruncatedResponseSurfacesWarn(t *testing.T) {
	rv := &fakeReviewer{name: "openai", err: providers.ErrResponseTruncated}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: First\n\nSome body.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictWarn, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, verdict.CategoryOther, pr.PlanFindings[0].Category)
	assert.Equal(t, verdict.SeverityMajor, pr.PlanFindings[0].Severity)
	assert.Contains(t, pr.PlanFindings[0].Suggestion, "ANTI_TANGENT_PLAN_MAX_TOKENS")
}

func planPassResp() providers.Response {
	return providers.Response{
		RawJSON:      []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[{"task_index":0,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`),
		Model:        "gpt-5",
		InputTokens:  3,
		OutputTokens: 2,
	}
}

func TestValidatePlan_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: First\n\nSome body.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	require.Len(t, pr.Tasks, 1)
	assert.Equal(t, "T1", pr.Tasks[0].TaskTitle)
}

func TestValidatePlan_NoTaskHeadings(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "Not a plan, no headings."})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, verdict.CategoryOther, pr.PlanFindings[0].Category)
	assert.Equal(t, 0, rv.Calls, "no provider call should be made")
}

func TestValidatePlan_PayloadTooLarge(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 10
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "this plan text is far too large for the configured cap of 10 bytes; it should be rejected"})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, verdict.CategoryTooLarge, pr.PlanFindings[0].Category)
	assert.Equal(t, 0, rv.Calls)
}

func TestValidatePlan_MissingPlanText(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: ""})
	require.Error(t, err)
}

// captureReviewer records the last providers.Request it receives so tests can
// assert on fields like MaxTokens.
type captureReviewer struct {
	name        string
	LastRequest providers.Request
	Response    providers.Response
}

func (c *captureReviewer) Name() string { return c.name }
func (c *captureReviewer) Review(_ context.Context, req providers.Request) (providers.Response, error) {
	c.LastRequest = req
	return c.Response, nil
}

func TestValidateTaskSpec_UsesConfiguredPerTaskMaxTokens(t *testing.T) {
	cap := &captureReviewer{
		name: "anthropic",
		Response: providers.Response{
			RawJSON:     []byte(`{"verdict":"pass","findings":[],"next_action":"go"}`),
			Model:       "claude-sonnet-4-6",
			InputTokens: 3, OutputTokens: 2,
		},
	}
	d := newDeps(t, &fakeReviewer{name: "anthropic"}) // build base deps with valid config
	d.Cfg.PerTaskMaxTokens = 7777
	d.Reviews = providers.Registry{"anthropic": cap}
	h := &handlers{deps: d}

	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "X",
		Goal:               "Y",
		AcceptanceCriteria: []string{"AC1"},
	})
	require.NoError(t, err)
	assert.Equal(t, 7777, cap.LastRequest.MaxTokens, "review() should use PerTaskMaxTokens from config")
}

func TestValidatePlan_UsesConfiguredPlanMaxTokens(t *testing.T) {
	cap := &captureReviewer{
		name: "openai",
		Response: providers.Response{
			RawJSON:      []byte(`{"plan_verdict":"pass","plan_findings":[],"tasks":[{"task_index":0,"task_title":"T1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"go"}`),
			Model:        "gpt-5",
			InputTokens:  3,
			OutputTokens: 2,
		},
	}
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Cfg.PlanMaxTokens = 8888
	d.Reviews = providers.Registry{"openai": cap}
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: First\n\nSome body.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
	assert.Equal(t, 8888, cap.LastRequest.MaxTokens, "reviewPlanSingle() should use PlanMaxTokens from config")
}
