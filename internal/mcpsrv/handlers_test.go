package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
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

type fakeReviewer struct {
	name        string
	resp        providers.Response
	err         error
	Calls       int
	LastRequest providers.Request // captured on every Review call; tests inspect rv.LastRequest.User to assert prompt content
}

func (f *fakeReviewer) Name() string { return f.name }
func (f *fakeReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	f.Calls++
	f.LastRequest = req
	return f.resp, f.err
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
		Cfg:       cfg,
		Sessions:  session.NewStore(1 * time.Hour),
		Reviews:   providers.Registry{"anthropic": rv},
		planCache: newPlanPassCache(),
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

func TestValidateTaskSpec_RollsUpUnverifiableFindings(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"internal/example.go defines Foo","suggestion":"verify against the actual code before dispatching."},
				{"severity":"major","category":"ambiguous_spec","criterion":"AC1","evidence":"AC1 has two interpretations","suggestion":"clarify AC1"},
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"docs/example.md documents Bar","suggestion":"verify against the actual code before dispatching."}
			],
			"next_action":"clarify"
		}`),
		Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "T",
		Goal:               "G",
		AcceptanceCriteria: []string{"AC1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "warn", env.Verdict)
	require.Len(t, env.Findings, 2)

	assert.Equal(t, verdict.CategoryAmbiguousSpec, env.Findings[0].Category)
	assert.Equal(t, "AC1", env.Findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	assert.Equal(t, "AC1 has two interpretations", env.Findings[0].Evidence)
	assert.Equal(t, "clarify AC1", env.Findings[0].Suggestion)

	rolledUp := env.Findings[1]
	assert.Equal(t, verdict.CategoryUnverifiableCodebaseClaim, rolledUp.Category)
	assert.Equal(t, verdict.SeverityMinor, rolledUp.Severity)
	assert.Equal(t, "codebase_reference_checklist", rolledUp.Criterion)
	assert.Contains(t, rolledUp.Evidence, "internal/example.go defines Foo")
	assert.Contains(t, rolledUp.Evidence, "docs/example.md documents Bar")
	assert.Equal(t, "Pre-flight these references with grep or codebase-aware review before implementation. If they were already verified, treat this as a checklist rather than a spec-quality defect.", rolledUp.Suggestion)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, env.Findings, sess.PreFindings)
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

func TestValidateTaskSpec_InvalidPhaseRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		Phase:     "during",
	})
	require.Error(t, err)
	assert.EqualError(t, err, `phase must be "pre" or "post"`)
	assert.Equal(t, 0, rv.Calls)
}

func TestValidateTaskSpec_PinnedByTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		PinnedBy:  []string{"  TestA.pins_behavior  ", "", "   ", "docs/spec.md"},
		Phase:     "post",
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"TestA.pins_behavior", "docs/spec.md"}, sess.Spec.PinnedBy)
	assert.Equal(t, "post", sess.Spec.Phase)
}

func TestValidateTaskSpec_PinnedByLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "Test.pins_behavior"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		PinnedBy:  tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned_by must contain at most 50 entries")

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		PinnedBy:  []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned_by[0] must be at most 500 characters")
	assert.Equal(t, 0, rv.Calls)

	// 500 multibyte runes (1000 bytes) must pass — the cap is on runes, not bytes.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		PinnedBy:  []string{strings.Repeat("é", 500)},
	})
	require.NoError(t, err)

	// 501 multibyte runes must fail at the same boundary as ASCII.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
		PinnedBy:  []string{strings.Repeat("é", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned_by[0] must be at most 500 characters")
}

func TestValidateTaskSpec_ControllerVerifiedReferencesTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:                    "T",
		Goal:                         "G",
		ControllerVerifiedReferences: []string{"  internal/foo.go:12  ", "", "   ", "Foo.Bar"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"internal/foo.go:12", "Foo.Bar"}, sess.Spec.ControllerVerifiedReferences)
}

func TestValidateTaskSpec_ControllerVerifiedReferencesLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "internal/foo.go"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:                    "T",
		Goal:                         "G",
		ControllerVerifiedReferences: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller_verified_references must contain at most 50 entries")

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:                    "T",
		Goal:                         "G",
		ControllerVerifiedReferences: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller_verified_references[0] must be at most 500 characters")
	assert.Equal(t, 0, rv.Calls)

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:                    "T",
		Goal:                         "G",
		ControllerVerifiedReferences: []string{strings.Repeat("é", 500)},
	})
	require.NoError(t, err)

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:                    "T",
		Goal:                         "G",
		ControllerVerifiedReferences: []string{strings.Repeat("é", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller_verified_references[0] must be at most 500 characters")
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

func TestValidateTaskSpec_PartialFindingsRecoveredOnTruncation(t *testing.T) {
	// Populated RawJSON with one complete finding, then truncation in the
	// middle of a second finding.
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"major","category":"other","criterion":"ac1","evidence":"e1","suggestion":"s1"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)
	assert.Equal(t, "warn", env.Verdict)
	assert.True(t, env.Partial, "envelope should signal partial recovery")
	assert.Empty(t, env.SessionID, "validate_task_spec should NOT create a session on partial-recovery flows")

	// One recovered finding + one minor truncation marker = 2 total.
	require.Len(t, env.Findings, 2)
	// Recovered finding comes first.
	assert.Equal(t, "ac1", env.Findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity)
	// Truncation marker is minor and references both env var and override.
	assert.Equal(t, verdict.SeverityMinor, env.Findings[1].Severity)
	assert.Equal(t, verdict.CategoryOther, env.Findings[1].Category)
	assert.Contains(t, env.Findings[1].Evidence, "1 complete findings recovered")
	assert.Contains(t, env.Findings[1].Suggestion, "max_tokens_override")
	assert.Contains(t, env.Findings[1].Suggestion, "ANTI_TANGENT_PER_TASK_MAX_TOKENS")

	// next_action steers the caller to re-run with a higher cap.
	assert.Contains(t, env.NextAction, "max_tokens_override")
}

// TestRecoverPartialFindings_PreservesReviewerNextActionWithOverrideHint
// exercises the defensive AC-MUST branch: when the partial parser yields a
// non-empty NextAction that does NOT mention max_tokens_override, the helper
// preserves the reviewer's text AND appends the override hint so the
// envelope still satisfies the "next_action MUST mention max_tokens_override"
// requirement. ParseResultPartial's array-truncation path strips trailing
// keys, so this branch is most reliably reached via a strict-parse path
// (well-formed JSON paired with a truncation signal from the provider);
// we test the helper directly here for branch coverage independent of how
// the partial parser happens to behave in any given scenario.
func TestRecoverPartialFindings_PreservesReviewerNextActionWithOverrideHint(t *testing.T) {
	// Well-formed JSON that strict-parses, but with a non-empty next_action
	// that does NOT mention max_tokens_override.
	raw := []byte(`{"verdict":"warn","findings":[` +
		`{"severity":"major","category":"other","criterion":"ac1","evidence":"e1","suggestion":"s1"}` +
		`],"next_action":"Tighten AC1 wording."}`)

	r, ok := recoverPartialFindings(raw, "ANTI_TANGENT_PER_TASK_MAX_TOKENS")
	require.True(t, ok)
	assert.True(t, r.Partial)
	assert.Contains(t, r.NextAction, "Tighten AC1 wording.")
	assert.Contains(t, r.NextAction, "max_tokens_override")
}

func TestCheckProgress_PartialFindingsRecoveredOnTruncation(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G",
	})
	require.NoError(t, err)

	h.deps.Reviews = providers.Registry{"anthropic": &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"major","category":"other","criterion":"cp1","evidence":"e1","suggestion":"s1"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}}

	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: pre.SessionID, WorkingOn: "x",
	})
	require.NoError(t, err)
	assert.True(t, env.Partial)
	require.Len(t, env.Findings, 2)
	assert.Equal(t, "cp1", env.Findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMinor, env.Findings[1].Severity)
	assert.Contains(t, env.Findings[1].Suggestion, "max_tokens_override")
	assert.Equal(t, pre.SessionID, env.SessionID, "existing session is preserved on partial recovery")
}

func TestValidateCompletion_PartialFindingsRecoveredOnTruncation(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	h.deps.Reviews = providers.Registry{"anthropic": &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"critical","category":"other","criterion":"vc1","evidence":"e1","suggestion":"s1"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}}

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "done",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n"}},
	})
	require.NoError(t, err)
	assert.True(t, env.Partial)
	require.Len(t, env.Findings, 2)
	assert.Equal(t, "vc1", env.Findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMinor, env.Findings[1].Severity)
	assert.Contains(t, env.Findings[1].Suggestion, "max_tokens_override")
	assert.Equal(t, pre.SessionID, env.SessionID, "existing session is preserved on partial recovery")
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
	// No-analysis truncation is major (not minor) so callers can't mistake it
	// for a cosmetic concern; plan_quality drops to rough because no analysis
	// occurred, and the suggestion / next_action are self-contained retry
	// instructions naming all three knobs.
	assert.Equal(t, verdict.SeverityMajor, pr.PlanFindings[0].Severity)
	assert.Equal(t, verdict.PlanQualityRough, pr.PlanQuality)
	assert.Contains(t, pr.PlanFindings[0].Suggestion, "ANTI_TANGENT_PLAN_MAX_TOKENS")
	assert.Contains(t, pr.PlanFindings[0].Suggestion, "max_tokens_override")
	assert.Contains(t, pr.PlanFindings[0].Suggestion, "ANTI_TANGENT_MAX_TOKENS_CEILING")
	assert.Contains(t, pr.NextAction, "max_tokens_override >= 16000")
	assert.Contains(t, pr.NextAction, "ANTI_TANGENT_PLAN_MAX_TOKENS")
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
	assert.Equal(t, verdict.SeverityCritical, pr.PlanFindings[0].Severity)
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

func TestValidatePlan_InvalidModeRejected(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: "# P\n\n### Task 1: X\n", Mode: "fast",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `mode must be "quick" or "thorough"`)
}

func TestValidatePlan_ModeQuickPlumbedToPrompt(t *testing.T) {
	cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "openai", resp: planPassResp()}}
	d := newDeps(t, &cap.fakeReviewer)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": cap}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: "# P\n\n### Task 1: X\n", Mode: "quick",
	})
	require.NoError(t, err)
	assert.Contains(t, cap.LastRequest.User, "**Quick mode.**", "quick mode should plumb through to the rendered prompt")
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

// TestValidatePlan_AdaptiveMaxTokensTable exercises the four (taskCount,
// override, planMax, ceiling) corners of validate_plan's adaptive budget rule.
// Each subtest name preserves the rationale of the original standalone test it
// replaced; the rationale comments before each case describe the boundary the
// case pins. The shared sub-runner builds a captureReviewer + Deps + handler
// once so the boilerplate doesn't repeat per case.
func TestValidatePlan_AdaptiveMaxTokensTable(t *testing.T) {
	// Each case asserts the MaxTokens value the provider receives for a plan
	// of taskCount tasks under the given PlanMaxTokens/MaxTokensCeiling config
	// and (optional) caller-supplied override.
	cases := []struct {
		// name carries the original-test rationale so failures stay legible.
		name string
		// taskCount drives buildPlanWithNTasks; affects only the adaptive
		// formula (2000 + 800*taskCount).
		taskCount     int
		override      int
		planMax       int
		ceiling       int
		wantMaxTokens int
	}{
		{
			// adaptive-formula path: 8 tasks → 2000 + 800*8 = 8400; above
			// PlanMaxTokens (4096), below ceiling (16384), so 8400 wins.
			name:          "UsesAdaptivePlanMaxTokensWhenUnset",
			taskCount:     8,
			override:      0,
			planMax:       4096,
			ceiling:       16384,
			wantMaxTokens: 8400,
		},
		{
			// 5000 is between PlanMaxTokens (4096) and adaptive 8400 for 8
			// tasks: the explicit override must still win.
			name:          "ExplicitOverrideBeatsAdaptivePlanMaxTokens",
			taskCount:     8,
			override:      5000,
			planMax:       4096,
			ceiling:       16384,
			wantMaxTokens: 5000,
		},
		{
			// Upper bound: when adaptive 8400 exceeds ceiling 6000, ceiling
			// wins. 8 tasks keeps us on the single-pass review path so the
			// captureReviewer sees exactly one call.
			name:          "AdaptivePlanMaxTokensClampedByCeiling",
			taskCount:     8,
			override:      0,
			planMax:       4096,
			ceiling:       6000,
			wantMaxTokens: 6000,
		},
		{
			// Lower bound: for tiny plans the adaptive floor (2000 + 800*1 =
			// 2800) is below PlanMaxTokens (4096), so PlanMaxTokens wins.
			name:          "AdaptivePlanMaxTokensFloorBelowPlanMaxTokens",
			taskCount:     1,
			override:      0,
			planMax:       4096,
			ceiling:       16384,
			wantMaxTokens: 4096,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &captureReviewer{name: "openai", Response: planPassResp()}
			d := newDeps(t, &fakeReviewer{name: "anthropic"})
			d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
			d.Cfg.PlanMaxTokens = tc.planMax
			d.Cfg.MaxTokensCeiling = tc.ceiling
			d.Reviews = providers.Registry{"openai": cap}
			h := &handlers{deps: d}

			_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
				PlanText:          buildPlanWithNTasks(tc.taskCount),
				MaxTokensOverride: tc.override,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantMaxTokens, cap.LastRequest.MaxTokens)
		})
	}
}

// reviewerCapture is a fakeReviewer that also records the last providers.Request
// so override tests can assert on MaxTokens while preserving the resp/err
// behavior of fakeReviewer (including error-returning truncation paths).
type reviewerCapture struct {
	fakeReviewer
	LastRequest providers.Request
}

func (c *reviewerCapture) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	c.LastRequest = req
	c.Calls++
	return c.resp, c.err
}

// overrideCase is one row of the max_tokens_override table.
type overrideCase struct {
	name      string
	override  int
	wantSent  int
	wantClamp bool
}

// "zero" and "unset" are the same wire-shape (json:omitempty), so they are
// covered by the single override=0 case per the plan's Step 8 table.
var overrideCases = []overrideCase{
	{"unset uses default", 0, 4096, false},
	{"in-range uses override", 8000, 8000, false},
	{"over-ceiling clamps", 32000, 16384, true},
}

// assertClampFinding asserts the findings list does/does not start with a
// clamp finding (per spec: prepended once per call, at the head).
func assertClampFinding(t *testing.T, findings []verdict.Finding, want bool) {
	t.Helper()
	if !want {
		for _, f := range findings {
			assert.NotEqual(t, "max_tokens_override", f.Criterion, "should not have clamp finding")
		}
		return
	}
	require.NotEmpty(t, findings)
	assert.Equal(t, "max_tokens_override", findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMinor, findings[0].Severity)
}

func TestMaxTokensOverride_ValidateTaskSpec(t *testing.T) {
	for _, tc := range overrideCases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}}
			d := newDeps(t, &fakeReviewer{name: "anthropic"})
			d.Cfg.PerTaskMaxTokens = 4096
			d.Cfg.MaxTokensCeiling = 16384
			d.Reviews = providers.Registry{"anthropic": cap}
			h := &handlers{deps: d}

			_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
				TaskTitle: "T", Goal: "G", MaxTokensOverride: tc.override,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantSent, cap.LastRequest.MaxTokens)
			assertClampFinding(t, env.Findings, tc.wantClamp)
		})
	}
}

func TestMaxTokensOverride_CheckProgress(t *testing.T) {
	for _, tc := range overrideCases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}}
			d := newDeps(t, &fakeReviewer{name: "anthropic"})
			d.Cfg.PerTaskMaxTokens = 4096
			d.Cfg.MaxTokensCeiling = 16384
			d.Reviews = providers.Registry{"anthropic": cap}
			h := &handlers{deps: d}

			// Seed the session via a default-tokens ValidateTaskSpec call, then
			// reset LastRequest so CheckProgress's request is the one under test.
			_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
				TaskTitle: "T", Goal: "G",
			})
			require.NoError(t, err)
			cap.LastRequest = providers.Request{}

			_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
				SessionID:         pre.SessionID,
				WorkingOn:         "x",
				MaxTokensOverride: tc.override,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantSent, cap.LastRequest.MaxTokens)
			assertClampFinding(t, env.Findings, tc.wantClamp)
		})
	}
}

func TestMaxTokensOverride_ValidateCompletion(t *testing.T) {
	for _, tc := range overrideCases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}}
			d := newDeps(t, &fakeReviewer{name: "anthropic"})
			d.Cfg.PerTaskMaxTokens = 4096
			d.Cfg.MaxTokensCeiling = 16384
			d.Reviews = providers.Registry{"anthropic": cap}
			h := &handlers{deps: d}

			_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
				TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
			})
			require.NoError(t, err)
			cap.LastRequest = providers.Request{}

			_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
				SessionID:         pre.SessionID,
				Summary:           "done",
				FinalFiles:        []FileArg{{Path: "f.go", Content: "package f\n"}},
				MaxTokensOverride: tc.override,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantSent, cap.LastRequest.MaxTokens)
			assertClampFinding(t, env.Findings, tc.wantClamp)
		})
	}
}

func TestMaxTokensOverride_ValidatePlan(t *testing.T) {
	for _, tc := range overrideCases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "openai", resp: planPassResp()}}
			d := newDeps(t, &fakeReviewer{name: "anthropic"})
			d.Cfg.PlanMaxTokens = 4096
			d.Cfg.MaxTokensCeiling = 16384
			d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
			d.Reviews = providers.Registry{"openai": cap}
			h := &handlers{deps: d}

			_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
				PlanText:          "# Plan\n\n### Task 1: First\n\nbody.\n",
				MaxTokensOverride: tc.override,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantSent, cap.LastRequest.MaxTokens)
			assertClampFinding(t, pr.PlanFindings, tc.wantClamp)
		})
	}
}

// TestMaxTokensOverride_NegativeRejected covers all four tools: a negative
// MaxTokensOverride must be rejected at the handler boundary with the exact
// error string `max_tokens_override must be ≥ 0`, before any provider call.
func TestMaxTokensOverride_NegativeRejected(t *testing.T) {
	negCases := []struct {
		name string
		run  func(*handlers) error
	}{
		{"ValidateTaskSpec", func(h *handlers) error {
			_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
				TaskTitle: "T", Goal: "G", MaxTokensOverride: -1,
			})
			return err
		}},
		{"CheckProgress", func(h *handlers) error {
			_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
				TaskTitle: "T", Goal: "G",
			})
			require.NoError(t, err)
			_, _, err = h.CheckProgress(context.Background(), nil, CheckProgressArgs{
				SessionID: pre.SessionID, WorkingOn: "x", MaxTokensOverride: -5,
			})
			return err
		}},
		{"ValidateCompletion", func(h *handlers) error {
			_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
				TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
			})
			require.NoError(t, err)
			_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
				SessionID:         pre.SessionID,
				Summary:           "done",
				FinalFiles:        []FileArg{{Path: "f.go", Content: "package f\n"}},
				MaxTokensOverride: -1,
			})
			return err
		}},
		{"ValidatePlan", func(h *handlers) error {
			_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
				PlanText: "# Plan\n\n### Task 1: T\n", MaxTokensOverride: -1,
			})
			return err
		}},
	}
	for _, tc := range negCases {
		t.Run(tc.name, func(t *testing.T) {
			rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
			d := newDeps(t, rv)
			d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
			d.Reviews["openai"] = &fakeReviewer{name: "openai", resp: planPassResp()}
			h := &handlers{deps: d}

			err := tc.run(h)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "max_tokens_override must be ≥ 0")
		})
	}
}

// TestMaxTokensOverride_ClampComposesWithTruncation asserts that a call which
// clamps the override AND triggers truncation surfaces all three: the clamp
// finding (prepended), the recovered finding, and the truncation marker.
// The clamp must NOT be suppressed on partial-recovery flows — exactly the
// caller who raised the cap is the one who most needs to see the ceiling
// signal alongside the truncation.
func TestMaxTokensOverride_ClampComposesWithTruncation(t *testing.T) {
	cap := &reviewerCapture{fakeReviewer: fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"major","category":"other","criterion":"recovered","evidence":"e","suggestion":"s"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}}
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Cfg.PerTaskMaxTokens = 4096
	d.Cfg.MaxTokensCeiling = 16384
	d.Reviews = providers.Registry{"anthropic": cap}
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		MaxTokensOverride: 32000, // over ceiling → clamp + truncate
	})
	require.NoError(t, err)
	assert.Equal(t, 16384, cap.LastRequest.MaxTokens, "ceiling used")
	assert.True(t, env.Partial)
	require.Len(t, env.Findings, 3, "clamp + recovered finding + truncation marker")
	// Clamp is prepended first.
	assert.Equal(t, "max_tokens_override", env.Findings[0].Criterion)
	assert.Equal(t, verdict.SeverityMinor, env.Findings[0].Severity)
	// Recovered finding.
	assert.Equal(t, "recovered", env.Findings[1].Criterion)
	// Truncation marker.
	assert.Equal(t, verdict.SeverityMinor, env.Findings[2].Severity)
	assert.Contains(t, env.Findings[2].Evidence, "complete findings recovered")
}

// TestMaxTokensOverride_ClampSurvivesEarlyExits asserts that the
// max_tokens_override clamp finding is prepended on every envelope-returning
// early-exit branch, not just the review-result branches. The AC says the
// clamp fires "regardless of which exit branch the handler takes" — the four
// branches covered here previously dropped the clamp silently:
//   - CheckProgress / ValidateCompletion: notFoundEnvelope (expired session)
//   - CheckProgress / ValidateCompletion: tooLargeEnvelope (payload over cap)
//   - ValidatePlan: noHeadingsPlanResult (no `### Task N:` headings)
//   - ValidatePlan: tooLargePlanResult (plan_text over cap)
func TestMaxTokensOverride_ClampSurvivesEarlyExits(t *testing.T) {
	t.Run("CheckProgress session_not_found", func(t *testing.T) {
		// No reviewer call happens on the not-found branch, but we still
		// need a registered reviewer for newDeps to construct a valid Deps.
		d := newDeps(t, &fakeReviewer{name: "anthropic"})
		d.Cfg.PerTaskMaxTokens = 4096
		d.Cfg.MaxTokensCeiling = 16384
		h := &handlers{deps: d}

		_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
			SessionID:         "missing",
			WorkingOn:         "x",
			MaxTokensOverride: 32000, // over ceiling → clamp
		})
		require.NoError(t, err)
		require.Len(t, env.Findings, 2, "clamp + session_not_found finding")
		// Clamp is prepended first.
		assert.Equal(t, "max_tokens_override", env.Findings[0].Criterion)
		assert.Equal(t, verdict.SeverityMinor, env.Findings[0].Severity)
		// Original session-not-found finding is preserved.
		assert.Equal(t, "session_not_found", string(env.Findings[1].Category))
	})

	t.Run("ValidateCompletion session_not_found", func(t *testing.T) {
		d := newDeps(t, &fakeReviewer{name: "anthropic"})
		d.Cfg.PerTaskMaxTokens = 4096
		d.Cfg.MaxTokensCeiling = 16384
		h := &handlers{deps: d}

		_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
			SessionID:         "missing",
			Summary:           "x",
			TestEvidence:      "go test PASS",
			MaxTokensOverride: 32000,
		})
		require.NoError(t, err)
		require.Len(t, env.Findings, 2, "clamp + session_not_found finding")
		assert.Equal(t, "max_tokens_override", env.Findings[0].Criterion)
		assert.Equal(t, "session_not_found", string(env.Findings[1].Category))
	})

	t.Run("CheckProgress payload_too_large", func(t *testing.T) {
		rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
		d := newDeps(t, rv)
		d.Cfg.PerTaskMaxTokens = 4096
		d.Cfg.MaxTokensCeiling = 16384
		d.Cfg.MaxPayloadBytes = 10
		h := &handlers{deps: d}

		// Seed a session via ValidateTaskSpec (default tokens — no clamp).
		_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
			TaskTitle: "T", Goal: "G",
		})
		require.NoError(t, err)

		_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
			SessionID:         pre.SessionID,
			WorkingOn:         "x",
			ChangedFiles:      []FileArg{{Path: "f", Content: "this is way too much"}},
			MaxTokensOverride: 32000,
		})
		require.NoError(t, err)
		require.Len(t, env.Findings, 2, "clamp + payload_too_large finding")
		assert.Equal(t, "max_tokens_override", env.Findings[0].Criterion)
		assert.Equal(t, "payload_too_large", string(env.Findings[1].Category))
	})

	t.Run("ValidateCompletion payload_too_large", func(t *testing.T) {
		rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
		d := newDeps(t, rv)
		d.Cfg.PerTaskMaxTokens = 4096
		d.Cfg.MaxTokensCeiling = 16384
		d.Cfg.MaxPayloadBytes = 10
		h := &handlers{deps: d}

		_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
			TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
		})
		require.NoError(t, err)

		_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
			SessionID:         pre.SessionID,
			Summary:           "implemented",
			FinalFiles:        []FileArg{{Path: "f.go", Content: "this is way too much"}},
			MaxTokensOverride: 32000,
		})
		require.NoError(t, err)
		require.Len(t, env.Findings, 2, "clamp + payload_too_large finding")
		assert.Equal(t, "max_tokens_override", env.Findings[0].Criterion)
		assert.Equal(t, "payload_too_large", string(env.Findings[1].Category))
	})

	t.Run("ValidatePlan no headings", func(t *testing.T) {
		// noHeadingsPlanResult fires before any reviewer call.
		d := newDeps(t, &fakeReviewer{name: "anthropic"})
		d.Cfg.PlanMaxTokens = 4096
		d.Cfg.MaxTokensCeiling = 16384
		d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
		d.Reviews["openai"] = &fakeReviewer{name: "openai"}
		h := &handlers{deps: d}

		// Plan body without any `### Task N:` heading.
		_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
			PlanText:          "# Plan\n\nJust some prose, no task headings.\n",
			MaxTokensOverride: 32000,
		})
		require.NoError(t, err)
		require.Len(t, pr.PlanFindings, 2, "clamp + no-headings finding")
		assert.Equal(t, "max_tokens_override", pr.PlanFindings[0].Criterion)
		assert.Equal(t, verdict.SeverityMinor, pr.PlanFindings[0].Severity)
		// Original no-headings finding is preserved.
		assert.Equal(t, "structure", pr.PlanFindings[1].Criterion)
		assert.Contains(t, pr.PlanFindings[1].Evidence, "no `### Task N:` headings")
	})

	t.Run("ValidatePlan plan_text too large", func(t *testing.T) {
		d := newDeps(t, &fakeReviewer{name: "anthropic"})
		d.Cfg.PlanMaxTokens = 4096
		d.Cfg.MaxTokensCeiling = 16384
		d.Cfg.MaxPayloadBytes = 10
		d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
		d.Reviews["openai"] = &fakeReviewer{name: "openai"}
		h := &handlers{deps: d}

		// Anything over 10 bytes triggers the payload-too-large branch.
		_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
			PlanText:          "# Plan\n\n### Task 1: First\n\nplenty of body to exceed the cap easily.\n",
			MaxTokensOverride: 32000,
		})
		require.NoError(t, err)
		require.Len(t, pr.PlanFindings, 2, "clamp + payload_too_large finding")
		assert.Equal(t, "max_tokens_override", pr.PlanFindings[0].Criterion)
		assert.Equal(t, "payload_too_large", string(pr.PlanFindings[1].Category))
	})
}

// ---------------------------------------------------------------------------
// summary_block population (Task 5)
//
// These integration tests verify that every exit path through envelopeResult
// and planEnvelopeResult ends up with a populated summary_block field. Five
// tests cover: ValidateTaskSpec happy, ValidateCompletion happy, ValidatePlan
// happy, CheckProgress notFoundEnvelope (bogus session), and ValidatePlan
// noHeadingsPlanResult (synthetic, never reaches reviewer).
// ---------------------------------------------------------------------------

func TestValidateTaskSpec_PopulatesSummaryBlock(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: newDeps(t, rv)}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "T",
		Goal:               "G",
		AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, env.SummaryBlock, "happy-path envelope must carry summary_block")
	assert.Contains(t, env.SummaryBlock, "anti-tangent envelope")
	assert.Contains(t, env.SummaryBlock, env.SessionID)
	assert.Contains(t, env.SummaryBlock, "verdict:       pass")
}

func TestValidateCompletion_PopulatesSummaryBlock(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "implemented",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n"}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, env.SummaryBlock, "validate_completion happy-path must carry summary_block")
	assert.Contains(t, env.SummaryBlock, "anti-tangent envelope")
	assert.Contains(t, env.SummaryBlock, "verdict:       pass")
}

func TestValidatePlan_PopulatesSummaryBlock(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	plan := "# Plan\n\n### Task 1: First\n\nSome body.\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.NotEmpty(t, pr.SummaryBlock, "validate_plan happy-path must carry summary_block")
	assert.True(t, strings.HasPrefix(pr.SummaryBlock, "anti-tangent envelope (validate_plan)"),
		"plan summary must begin with the validate_plan banner, got:\n%s", pr.SummaryBlock)
	assert.Contains(t, pr.SummaryBlock, "plan_verdict:  pass")
}

func TestCheckProgress_NotFoundEnvelope_PopulatesSummaryBlock(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-haiku-4-5-20251001")}
	h := &handlers{deps: newDeps(t, rv)}

	_, env, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: "no-such-session",
		WorkingOn: "anything",
	})
	require.NoError(t, err)
	require.NotEmpty(t, env.SummaryBlock, "notFoundEnvelope path must still populate summary_block")
	assert.Contains(t, env.SummaryBlock, "anti-tangent envelope")
	assert.Contains(t, env.SummaryBlock, "verdict:       fail")
	assert.Contains(t, env.SummaryBlock, "session_not_found")
}

func TestValidatePlan_NoHeadings_PopulatesSummaryBlock(t *testing.T) {
	rv := &fakeReviewer{name: "openai", resp: planPassResp()}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "Not a plan, no headings."})
	require.NoError(t, err)
	require.NotEmpty(t, pr.SummaryBlock, "noHeadingsPlanResult path must still populate summary_block")
	assert.True(t, strings.HasPrefix(pr.SummaryBlock, "anti-tangent envelope (validate_plan)"),
		"plan summary must begin with the validate_plan banner, got:\n%s", pr.SummaryBlock)
	assert.Contains(t, pr.SummaryBlock, "plan_verdict:  fail")
	// Synthetic PlanResults get plan_quality from ApplyPlanQualitySanity, which
	// forces "rough" on any fail verdict.
	assert.Contains(t, pr.SummaryBlock, "plan_quality:  rough")
}

// ---------------------------------------------------------------------------
// validate_completion evidence-shape guard + lightweight mode (Task 6)
//
// Pre-reviewer guard that rejects malformed evidence (truncation markers in
// final_diff or final_files, empty Path entries) before the LLM call. Rejections
// are cached for 5 minutes by canonical content hash. The handler also accepts
// an empty session_id when at least one piece of evidence is non-empty, by
// synthesizing a minimal task spec for the reviewer.
// ---------------------------------------------------------------------------

func TestValidateCompletion_EvidenceGuard_RejectsTruncationMarkerInDiff(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	initialCalls := rv.Calls
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalDiff: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n(truncated)\n+new\n",
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != string(verdict.VerdictFail) {
		t.Errorf("verdict = %s, want fail", env.Verdict)
	}
	if len(env.Findings) == 0 || env.Findings[0].Category != verdict.CategoryMalformedEvidence {
		t.Errorf("expected malformed_evidence finding, got: %+v", env.Findings)
	}
	if rv.Calls != initialCalls {
		t.Errorf("reviewer was called (%d -> %d); guard should have rejected before reviewer", initialCalls, rv.Calls)
	}
}

func TestValidateCompletion_EvidenceGuard_RejectsTruncationMarkerInFinalFiles(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	initialCalls := rv.Calls
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "done",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n// ... unchanged\nfunc Foo() {}\n"}},
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != string(verdict.VerdictFail) {
		t.Errorf("verdict = %s, want fail", env.Verdict)
	}
	if len(env.Findings) == 0 || env.Findings[0].Category != verdict.CategoryMalformedEvidence {
		t.Errorf("expected malformed_evidence finding, got: %+v", env.Findings)
	}
	if rv.Calls != initialCalls {
		t.Errorf("reviewer was called (%d -> %d); guard should have rejected before reviewer", initialCalls, rv.Calls)
	}
}

func TestValidateCompletion_EvidenceGuard_RejectsEmptyFilePath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	initialCalls := rv.Calls
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "done",
		FinalFiles: []FileArg{{Path: "", Content: "anything"}},
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != string(verdict.VerdictFail) {
		t.Errorf("verdict = %s, want fail", env.Verdict)
	}
	if len(env.Findings) == 0 || env.Findings[0].Category != verdict.CategoryMalformedEvidence {
		t.Errorf("expected malformed_evidence finding, got: %+v", env.Findings)
	}
	if rv.Calls != initialCalls {
		t.Errorf("reviewer was called (%d -> %d); guard should have rejected before reviewer", initialCalls, rv.Calls)
	}
}

func TestValidateCompletion_EvidenceGuard_CompleteDiffPassesThrough(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalDiff: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n",
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != "pass" {
		t.Errorf("complete diff should pass through to reviewer (pass), got %s", env.Verdict)
	}
}

func TestValidateCompletion_EvidenceGuard_ModeOnlyDiffPassesThrough(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	modeOnlyDiff := "diff --git a/script.sh b/script.sh\nold mode 100644\nnew mode 100755\n"
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "made script executable",
		FinalDiff: modeOnlyDiff,
	})
	if err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
	if env.Verdict != "pass" {
		t.Errorf("mode-only diff should pass through (pass), got verdict=%s findings=%+v", env.Verdict, env.Findings)
	}
}

func TestValidateCompletion_EvidenceGuard_CacheHitShortCircuits(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	args := ValidateCompletionArgs{
		SessionID: pre.SessionID,
		Summary:   "done",
		FinalDiff: "diff --git a/x b/x\n@@ -1 +1 @@\n(truncated)\n",
	}
	_, env1, err := h.ValidateCompletion(context.Background(), nil, args)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if env1.Verdict != string(verdict.VerdictFail) {
		t.Fatalf("first call should reject")
	}
	callsAfterFirst := rv.Calls
	_, env2, err := h.ValidateCompletion(context.Background(), nil, args)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if env2.Verdict != string(verdict.VerdictFail) {
		t.Errorf("second call should also reject (from cache)")
	}
	if rv.Calls != callsAfterFirst {
		t.Errorf("reviewer should not have been called between cached rejections; calls before=%d after=%d", callsAfterFirst, rv.Calls)
	}
}

func TestValidateCompletion_LightweightMode_EmptySessionAccepted(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  "",
		Summary:    "trivial doc change",
		FinalFiles: []FileArg{{Path: "doc.md", Content: "updated\n"}},
	})
	if err != nil {
		t.Fatalf("ValidateCompletion (lightweight): %v", err)
	}
	if env.SessionID != "" {
		t.Errorf("lightweight mode should not surface a session_id, got %q", env.SessionID)
	}
	if env.Verdict != "pass" {
		t.Errorf("lightweight mode reviewer call should pass with stub response, got %s", env.Verdict)
	}
}

func TestValidateCompletion_LightweightMode_OmitsMajorPreFindings(t *testing.T) {
	cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}}
	d := newDeps(t, &cap.fakeReviewer)
	d.Reviews = providers.Registry{"anthropic": cap}
	h := &handlers{deps: d}

	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  "",
		Summary:    "trivial doc change",
		FinalFiles: []FileArg{{Path: "doc.md", Content: "updated\n"}},
	})
	require.NoError(t, err)
	assert.NotContains(t, cap.LastRequest.User, "Major pre-task findings to verify")
}

func TestReferencedPathsMissingEvidence(t *testing.T) {
	args := ValidateCompletionArgs{
		Summary:    "Created docs/audit.md and reports/result.yaml.",
		FinalFiles: []FileArg{{Path: "docs/audit.md", Content: "# Audit\n"}},
		FinalDiff:  "diff --git a/other.txt b/other.txt\n",
	}
	assert.Equal(t, []string{"reports/result.yaml"}, referencedPathsMissingEvidence(args))
}

func TestReferencedPathsMissingEvidence_DedupsRepeatedReferences(t *testing.T) {
	args := ValidateCompletionArgs{
		Summary:      "Created docs/audit.md. Then re-edited docs/audit.md.",
		TestEvidence: "ran tests",
	}
	assert.Equal(t, []string{"docs/audit.md"}, referencedPathsMissingEvidence(args))
}

func TestValidateCompletion_RendersReferencedPathEvidenceNote(t *testing.T) {
	cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}}
	d := newDeps(t, &cap.fakeReviewer)
	d.Reviews = providers.Registry{"anthropic": cap}
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)

	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:    pre.SessionID,
		Summary:      "Created docs/audit.md.",
		TestEvidence: "not run; docs only",
	})
	require.NoError(t, err)
	assert.Contains(t, cap.LastRequest.User, "summary references these paths")
	assert.Contains(t, cap.LastRequest.User, "docs/audit.md")
}

func TestValidateCompletion_RendersMajorPreFindings(t *testing.T) {
	cap := &reviewerCapture{fakeReviewer: fakeReviewer{name: "anthropic"}}
	d := newDeps(t, &cap.fakeReviewer)
	d.Reviews = providers.Registry{"anthropic": cap}
	h := &handlers{deps: d}

	cap.resp = providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"major","category":"ambiguous_spec","criterion":"AC","evidence":"Pre-task review found AC did not specify load.","suggestion":"Clarify load."},
				{"severity":"minor","category":"quality","criterion":"spec","evidence":"Minor pre-finding should not render.","suggestion":"Consider wording."}
			],
			"next_action":"continue"
		}`),
		Model: "claude-sonnet-4-6",
	}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)

	cap.resp = passResp("claude-sonnet-4-6")
	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:    pre.SessionID,
		Summary:      "Implemented AC with explicit load coverage.",
		TestEvidence: "PASS: TestACUnderLoad",
	})
	require.NoError(t, err)
	assert.Contains(t, cap.LastRequest.User, "Major pre-task findings to verify")
	assert.Contains(t, cap.LastRequest.User, "Pre-task review found AC did not specify load.")
	assert.NotContains(t, cap.LastRequest.User, "Minor pre-finding should not render.")
}

func TestValidateCompletion_LightweightMode_NoEvidenceErrors(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: "",
		Summary:   "x",
	})
	if err == nil {
		t.Errorf("expected error when session_id is empty AND no evidence is provided")
	}
	if err != nil && !strings.Contains(err.Error(), "at least one of") {
		t.Errorf("error should mention 'at least one of'; got: %v", err)
	}
}

func TestValidateTaskSpec_TestStrategyNotesTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: []string{"  AC #2 covered jointly by tests A and B  ", "", "   ", "AC #3 negative case split across X/Y"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"AC #2 covered jointly by tests A and B", "AC #3 negative case split across X/Y"}, sess.Spec.TestStrategyNotes)
}

func TestValidateTaskSpec_TestStrategyNotesLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	// 50 entries pass, 51 fail.
	fifty := make([]string, 50)
	for i := range fifty {
		fifty[i] = "joint"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: fifty,
	})
	require.NoError(t, err)

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "joint"
	}
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test_strategy_notes must contain at most 50 entries")

	// 500 ASCII runes pass, 501 fail.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: []string{strings.Repeat("x", 500)},
	})
	require.NoError(t, err)

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test_strategy_notes[0] must be at most 500 characters")

	// 500 multibyte runes (1000 bytes) must pass — the cap is on runes.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:         "T",
		Goal:              "G",
		TestStrategyNotes: []string{strings.Repeat("é", 500)},
	})
	require.NoError(t, err)
}

func TestValidateTaskSpec_CodebaseConventionsTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: []string{"  id is canonically UUID in memory  ", "", "Instant fields use @Serializable"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"id is canonically UUID in memory", "Instant fields use @Serializable"}, sess.Spec.CodebaseConventions)
}

func TestValidateTaskSpec_CodebaseConventionsLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	// 50 entries pass, 51 fail.
	fifty := make([]string, 50)
	for i := range fifty {
		fifty[i] = "convention"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: fifty,
	})
	require.NoError(t, err)

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "convention"
	}
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codebase_conventions must contain at most 50 entries")

	// 500 ASCII runes pass, 501 fail.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: []string{strings.Repeat("x", 500)},
	})
	require.NoError(t, err)

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codebase_conventions[0] must be at most 500 characters")

	// 500 multibyte runes (1000 bytes) must pass — cap is rune-based, not byte-based.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		CodebaseConventions: []string{strings.Repeat("é", 500)},
	})
	require.NoError(t, err)
}

func TestValidateTaskSpec_TestabilityExtractionsTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{"  buildDeclineWinddownHandlerOutput  ", "", "runHiringAreaRecheck"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"buildDeclineWinddownHandlerOutput", "runHiringAreaRecheck"}, sess.Spec.TestabilityExtractions)
}

func TestValidateTaskSpec_TestabilityExtractionsLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	// 50 entries pass, 51 fail.
	fifty := make([]string, 50)
	for i := range fifty {
		fifty[i] = "helper"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: fifty,
	})
	require.NoError(t, err)

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "helper"
	}
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "testability_extractions must contain at most 50 entries")

	// 500 ASCII runes pass, 501 fail.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{strings.Repeat("x", 500)},
	})
	require.NoError(t, err)

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "testability_extractions[0] must be at most 500 characters")

	// 500 multibyte runes (1000 bytes) must pass — cap is rune-based, not byte-based.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{strings.Repeat("é", 500)},
	})
	require.NoError(t, err)
}

func TestValidateTaskSpec_NormativeTestBodiesTrimmedAndStored(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: []string{"  @Test fun whenX_thenY() { ... }  ", "", "// excerpt: see plan §3 test 2"},
	})
	require.NoError(t, err)

	sess, ok := d.Sessions.Get(env.SessionID)
	require.True(t, ok)
	assert.Equal(t, []string{"@Test fun whenX_thenY() { ... }", "// excerpt: see plan §3 test 2"}, sess.Spec.NormativeTestBodies)
}

func TestValidateTaskSpec_NormativeTestBodiesLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	// 20 entries pass, 21 fail.
	twenty := make([]string, 20)
	for i := range twenty {
		twenty[i] = "@Test fun tx() {}"
	}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: twenty,
	})
	require.NoError(t, err)

	twentyOne := append([]string(nil), twenty...)
	twentyOne = append(twentyOne, "@Test fun extra() {}")
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: twentyOne,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "normative_test_bodies must contain at most 20 entries")

	// 4000 runes pass, 4001 fail.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: []string{strings.Repeat("x", 4000)},
	})
	require.NoError(t, err)

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: []string{strings.Repeat("x", 4001)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "normative_test_bodies[0] must be at most 4000 characters")

	// 4000 multibyte runes must pass — cap is rune-based, not byte-based.
	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:           "T",
		Goal:                "G",
		NormativeTestBodies: []string{strings.Repeat("é", 4000)},
	})
	require.NoError(t, err)
}

func TestValidateTaskSpec_TestabilityExtractionsSuppressScopeDrift(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"minor","category":"scope_drift","criterion":"spec","evidence":"buildDeclineWinddownHandlerOutput is extracted as a top-level helper","suggestion":"keep helpers inline"},
				{"severity":"major","category":"scope_drift","criterion":"spec","evidence":"adds an unrelated retry wrapper","suggestion":"remove the wrapper"},
				{"severity":"minor","category":"ambiguous_spec","criterion":"AC1","evidence":"runHiringAreaRecheck closure semantics unclear","suggestion":"pin the closure"}
			],
			"next_action":"address findings"
		}`),
		Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		AcceptanceCriteria:     []string{"AC1"},
		TestabilityExtractions: []string{"buildDeclineWinddownHandlerOutput", "runHiringAreaRecheck"},
	})
	require.NoError(t, err)

	// The first scope_drift (matches buildDeclineWinddownHandlerOutput) is suppressed.
	// The second scope_drift (unrelated) survives.
	// The ambiguous_spec finding survives even though its evidence names a different
	// extraction — suppression is scope_drift-only.
	require.Len(t, env.Findings, 2)
	assert.Equal(t, verdict.CategoryScopeDrift, env.Findings[0].Category)
	assert.Equal(t, "adds an unrelated retry wrapper", env.Findings[0].Evidence)
	assert.Equal(t, verdict.CategoryAmbiguousSpec, env.Findings[1].Category)
}

func TestValidateTaskSpec_TestabilityExtractionsReverseSubstringDrop(t *testing.T) {
	// Reviewer evidence is a substring of the extraction entry (the inverse
	// match direction). The finding must still be dropped.
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"minor","category":"scope_drift","criterion":"spec","evidence":"buildDecline","suggestion":"keep helpers inline"}
			],
			"next_action":"address findings"
		}`),
		Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{"buildDeclineWinddownHandlerOutput"},
	})
	require.NoError(t, err)
	assert.Empty(t, env.Findings)
}

func TestValidateTaskSpec_TestabilityExtractionsRollupOrdering(t *testing.T) {
	// Suppression runs BEFORE the unverifiable rollup, so a suppressed
	// scope_drift never enters the rolled-up checklist. The unrelated
	// unverifiable_codebase_claim must still be rolled up normally.
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"minor","category":"scope_drift","criterion":"spec","evidence":"buildDeclineWinddownHandlerOutput is extracted","suggestion":"keep helpers inline"},
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"internal/example.go defines Foo","suggestion":"verify against the actual code before dispatching."}
			],
			"next_action":"address findings"
		}`),
		Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:              "T",
		Goal:                   "G",
		TestabilityExtractions: []string{"buildDeclineWinddownHandlerOutput"},
	})
	require.NoError(t, err)

	// scope_drift suppressed; unverifiable rolled up into a single checklist entry.
	require.Len(t, env.Findings, 1)
	assert.Equal(t, verdict.CategoryUnverifiableCodebaseClaim, env.Findings[0].Category)
	assert.Equal(t, "codebase_reference_checklist", env.Findings[0].Criterion)
	assert.Contains(t, env.Findings[0].Evidence, "internal/example.go defines Foo")
	// Confirm the suppressed scope_drift evidence is NOT in the rolled-up checklist.
	assert.NotContains(t, env.Findings[0].Evidence, "buildDeclineWinddownHandlerOutput")
}

func TestValidateTaskSpec_TestabilityExtractionsEmptyIsNoop(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: providers.Response{
		RawJSON: []byte(`{
			"verdict":"warn",
			"findings":[
				{"severity":"minor","category":"scope_drift","criterion":"spec","evidence":"adds unrelated helper","suggestion":"keep scoped"}
			],
			"next_action":"address findings"
		}`),
		Model: "claude-sonnet-4-6",
	}}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T",
		Goal:      "G",
	})
	require.NoError(t, err)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, verdict.CategoryScopeDrift, env.Findings[0].Category)
}

func TestNormalizeCompletionExitContracts_TrimsAndDropsEmpties(t *testing.T) {
	out, err := normalizeCompletionExitContracts([]string{"  contract A  ", "", "\tcontract B\n", "   "})
	require.NoError(t, err)
	require.Equal(t, []string{"contract A", "contract B"}, out)
}

func TestValidateCompletion_ExitContractsLimitsRejected(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "contract"
	}
	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:     "anything",
		Summary:       "s",
		FinalDiff:     "diff",
		ExitContracts: tooMany,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit_contracts must contain at most 50 entries")

	_, _, err = h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:     "anything",
		Summary:       "s",
		FinalDiff:     "diff",
		ExitContracts: []string{strings.Repeat("x", 501)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit_contracts[0] must be at most 500 characters")
}

func TestValidateTaskSpec_FinalizeVerdict_ClampParticipatesInLadder(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"pass","findings":[
				{"severity":"minor","category":"quality","criterion":"a","evidence":"b","suggestion":"c"},
				{"severity":"minor","category":"quality","criterion":"d","evidence":"e","suggestion":"f"}
			],"next_action":"n"}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	d.Cfg.MaxTokensCeiling = 1000
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g",
		AcceptanceCriteria: []string{"ac1"},
		MaxTokensOverride:  10000,
	})
	require.NoError(t, err)
	require.Equal(t, "warn", env.Verdict, "two minors + clamp minor → warn")
	hasNoise := false
	for _, f := range env.Findings {
		if f.Category == verdict.CategoryOther && f.Criterion == "noise_cluster" {
			hasNoise = true
		}
	}
	require.True(t, hasNoise, "noise_cluster appended on ≥3 minor → warn")
	require.Equal(t, "max_tokens_override", env.Findings[0].Criterion)
}

func TestValidateTaskSpec_SuppressionRunsBeforeFinalize(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"fail","findings":[
				{"severity":"major","category":"scope_drift","criterion":"a","evidence":"helperFn","suggestion":"x"},
				{"severity":"major","category":"scope_drift","criterion":"b","evidence":"helperFn","suggestion":"x"},
				{"severity":"major","category":"scope_drift","criterion":"c","evidence":"helperFn","suggestion":"x"},
				{"severity":"major","category":"scope_drift","criterion":"d","evidence":"helperFn","suggestion":"x"},
				{"severity":"major","category":"scope_drift","criterion":"e","evidence":"helperFn","suggestion":"x"}
			],"next_action":"n"}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g",
		AcceptanceCriteria:     []string{"ac1"},
		TestabilityExtractions: []string{"helperFn"},
	})
	require.NoError(t, err)
	require.Equal(t, "pass", env.Verdict, "all five suppressed → pass")
	require.Empty(t, env.Findings, "all findings suppressed before finalize")
}

func TestValidateTaskSpec_TruncatedResponseSurfacesWarnWithMajorFinding(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"},
	})
	require.NoError(t, err)
	require.Equal(t, "warn", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.SeverityMajor, env.Findings[0].Severity, "truncated finding bumped to major")
	require.Equal(t, "reviewer_response", env.Findings[0].Criterion)
}

func TestTooLargeEnvelope_SyntheticFindingSeverityIsCritical(t *testing.T) {
	env := tooLargeEnvelope("sess-1", config.ModelRef{Provider: "anthropic", Model: "x"}, 1000, 500, "trim")
	require.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.SeverityCritical, env.Findings[0].Severity)
	require.Equal(t, verdict.CategoryTooLarge, env.Findings[0].Category)
}

func TestMalformedEvidenceEnvelope_SyntheticFindingSeverityIsCritical(t *testing.T) {
	env := malformedEvidenceEnvelope("sess-1", "reason", "model")
	require.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.SeverityCritical, env.Findings[0].Severity)
	require.Equal(t, verdict.CategoryMalformedEvidence, env.Findings[0].Category)
}

func TestNotFoundEnvelope_SyntheticFindingSeverityIsCritical(t *testing.T) {
	env := notFoundEnvelope("sess-1", config.ModelRef{Provider: "anthropic", Model: "x"})
	require.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	require.Equal(t, verdict.SeverityCritical, env.Findings[0].Severity)
}

func TestTooLargePlanResult_SyntheticFindingSeverityIsCritical(t *testing.T) {
	pr := tooLargePlanResult(1000, 500)
	require.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	require.Equal(t, verdict.SeverityCritical, pr.PlanFindings[0].Severity)
	require.Equal(t, verdict.CategoryTooLarge, pr.PlanFindings[0].Category)
}

func TestValidatePlan_FinalizePlanVerdict_ClampParticipatesInLadder(t *testing.T) {
	rv := &fakeReviewer{
		name: "openai",
		resp: providers.Response{
			RawJSON: []byte(`{"plan_verdict":"pass","plan_findings":[
				{"severity":"minor","category":"quality","criterion":"a","evidence":"b","suggestion":"c"},
				{"severity":"minor","category":"quality","criterion":"d","evidence":"e","suggestion":"f"}
			],"tasks":[{"task_index":1,"task_title":"t","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":"","lightweight_eligible":false,"lightweight_reason":"","exit_contracts":[],"exit_contracts_inferred":false}],"next_action":"n","plan_quality":"actionable"}`),
			Model: "gpt-5",
		},
	}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	d.Cfg.MaxTokensCeiling = 1000
	h := &handlers{deps: d}
	planText := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac1\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          planText,
		MaxTokensOverride: 10000,
	})
	require.NoError(t, err)
	require.Equal(t, verdict.VerdictWarn, pr.PlanVerdict, "two minors + clamp minor → warn")
	hasNoise := false
	for _, f := range pr.PlanFindings {
		if f.Category == verdict.CategoryOther && f.Criterion == "noise_cluster" {
			hasNoise = true
		}
	}
	require.True(t, hasNoise, "plan-level noise_cluster appended")
	require.Equal(t, "max_tokens_override", pr.PlanFindings[0].Criterion, "clamp at PlanFindings[0]")
}

func TestTruncatedPlanResult_SeverityIsMajor(t *testing.T) {
	pr := truncatedPlanResult()
	require.Equal(t, verdict.VerdictWarn, pr.PlanVerdict)
	require.Len(t, pr.PlanFindings, 1)
	require.Equal(t, verdict.SeverityMajor, pr.PlanFindings[0].Severity)
	require.Equal(t, verdict.PlanQualityRough, pr.PlanQuality)
}

func TestValidatePlan_TruncatedResponse_PreservesFinalizePlanResultSideEffects(t *testing.T) {
	rv := &fakeReviewer{name: "openai", err: providers.ErrResponseTruncated}
	d := newDeps(t, rv)
	d.Cfg.PlanModel = config.ModelRef{Provider: "openai", Model: "gpt-5"}
	d.Reviews = providers.Registry{"openai": rv}
	h := &handlers{deps: d}

	planText := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac1\n"
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planText})
	require.NoError(t, err)
	require.Equal(t, verdict.VerdictWarn, pr.PlanVerdict)
	require.NotEmpty(t, pr.SummaryBlock, "formatPlanSummary must run on the truncation path")
	require.Equal(t, verdict.PlanQualityRough, pr.PlanQuality, "ApplyPlanQualitySanity (via FinalizePlanVerdict) must have run; truncatedPlanResult explicitly sets rough")
}

func TestCheckEvidenceShape_NewPatternsRejected(t *testing.T) {
	patterns := []string{
		"/* ... */",
		"/* ...rest unchanged */",
		"// snip",
		"// elided",
		"// ... rest unchanged",
		"/...",
	}
	for _, p := range patterns {
		t.Run("final_diff:"+p, func(t *testing.T) {
			args := ValidateCompletionArgs{
				SessionID: "s",
				Summary:   "s",
				FinalDiff: "valid header\n" + p + "\nmore",
			}
			reason := checkEvidenceShape(args)
			require.NotEmpty(t, reason, "must reject %q in final_diff", p)
			require.Contains(t, reason, "final_diff")
		})
		t.Run("final_files:"+p, func(t *testing.T) {
			args := ValidateCompletionArgs{
				SessionID:  "s",
				Summary:    "s",
				FinalFiles: []FileArg{{Path: "foo.go", Content: "valid header\n" + p + "\nmore"}},
			}
			reason := checkEvidenceShape(args)
			require.NotEmpty(t, reason, "must reject %q in final_files[].content", p)
			require.Contains(t, reason, "final_files")
		})
	}
}

func TestValidateTaskSpec_CVRSuppressesUnverifiableClaim_ClaimLevel(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[{
				"severity":"minor",
				"category":"unverifiable_codebase_claim",
				"criterion":"spec",
				"evidence":"XService.findFoo at path/to/file.kt:L42",
				"suggestion":"verify"
			}],"next_action":"n"}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"},
		ControllerVerifiedReferences: []string{"path/to/file.kt"},
	})
	require.NoError(t, err)
	require.Empty(t, env.Findings, "claim suppressed by CVR substring match")
	require.Equal(t, "pass", env.Verdict, "no findings → pass")
}

func TestValidateTaskSpec_CVRSuppression_RunsBeforeRollup(t *testing.T) {
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"path/to/file.kt:L1","suggestion":"v"},
				{"severity":"minor","category":"unverifiable_codebase_claim","criterion":"spec","evidence":"path/to/file.kt:L2","suggestion":"v"}
			],"next_action":"n"}`),
			Model: "claude-sonnet-4-6",
		},
	}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"ac1"},
		ControllerVerifiedReferences: []string{"path/to/file.kt"},
	})
	require.NoError(t, err)
	require.Empty(t, env.Findings, "both unverifiables suppressed before rollup; rollup sees zero")
}
