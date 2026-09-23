package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestValidateTaskSpec_AttachedFilesReachTheReviewer(t *testing.T) {
	dir := t.TempDir()
	brief := filepath.Join(dir, "brief.md")
	require.NoError(t, os.WriteFile(brief, []byte("NET is internal/net/network.go\n"), 0o600))

	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	d := newDeps(t, rv)
	d.Cfg.PlanRoots = []string{dir}
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}, ContextPaths: []string{brief},
	})
	require.NoError(t, err)
	assert.Equal(t, "pass", env.Verdict)
	assert.Contains(t, rv.LastRequest.User, "NET is internal/net/network.go")
	assert.Contains(t, rv.LastRequest.User, brief)
}

func TestValidateTaskSpec_AnOversizedAttachmentIsRejectedWithoutAReview(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	d := newDeps(t, rv)
	d.Cfg.PlanRoots = []string{dir}
	d.Cfg.ContextMaxFileBytes = 16
	require.NoError(t, os.WriteFile(big, []byte(strings.Repeat("x", 64)), 0o600))
	h := &handlers{deps: d}

	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", ContextPaths: []string{big},
	})
	require.NoError(t, err)
	assert.Equal(t, "fail", env.Verdict)
	assert.True(t, hasCategory(env.Findings, verdict.CategoryTooLarge))
	assert.Zero(t, rv.Calls, "an oversized attachment costs no reviewer call")
}

func TestValidateCompletion_RelatedFilesReachTheReviewer(t *testing.T) {
	for _, withSession := range []bool{false, true} {
		dir := t.TempDir()
		sibling := filepath.Join(dir, "slug.go")
		require.NoError(t, os.WriteFile(sibling, []byte("package feed\nfunc Slugify(s string) string { return s }\n"), 0o600))

		rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
		d := newDeps(t, rv)
		d.Cfg.PlanRoots = []string{dir}
		h := &handlers{deps: d}

		args := ValidateCompletionArgs{Summary: "done", FinalDiff: replayTestDiff, ContextPaths: []string{sibling}}
		if withSession {
			_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}})
			require.NoError(t, err)
			args.SessionID = pre.SessionID
		}
		_, env, err := h.ValidateCompletion(context.Background(), nil, args)
		require.NoError(t, err, "session=%v", withSession)
		assert.Equal(t, "pass", env.Verdict, "session=%v", withSession)
		assert.Contains(t, rv.LastRequest.User, "func Slugify", "session=%v", withSession)
		assert.Contains(t, rv.LastRequest.User, sibling, "session=%v", withSession)
		assert.Contains(t, rv.LastRequest.User, "never evidence that an acceptance criterion is met", "session=%v", withSession)
	}
}

func TestValidateCompletion_AnOversizedRelatedFileIsRejectedWithoutAReview(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(big, []byte(strings.Repeat("x", 64)), 0o600))
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	d := newDeps(t, rv)
	d.Cfg.PlanRoots = []string{dir}
	d.Cfg.ContextMaxFileBytes = 16
	h := &handlers{deps: d}

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "done", FinalDiff: replayTestDiff, ContextPaths: []string{big},
	})
	require.NoError(t, err)
	assert.Equal(t, "fail", env.Verdict)
	assert.Equal(t, "validate_completion", env.Tool)
	assert.True(t, env.Lightweight)
	assert.True(t, hasCategory(env.Findings, verdict.CategoryTooLarge))
	assert.Zero(t, rv.Calls, "an oversized attachment costs no reviewer call")
}

// TestValidateCompletion_AnOversizedRelatedFileOnASessionCarriesTheSessionID
// pins the fix for a context_paths rejection that dropped the caller's
// session_id: the payload-cap rejection already set env.SessionID from
// args.SessionID, and the context_paths rejection must do the same.
func TestValidateCompletion_AnOversizedRelatedFileOnASessionCarriesTheSessionID(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(big, []byte(strings.Repeat("x", 64)), 0o600))
	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	d := newDeps(t, rv)
	d.Cfg.PlanRoots = []string{dir}
	d.Cfg.ContextMaxFileBytes = 16
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	require.NotEmpty(t, pre.SessionID)
	initialCalls := rv.Calls

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID, Summary: "done", FinalDiff: replayTestDiff, ContextPaths: []string{big},
	})
	require.NoError(t, err)
	assert.Equal(t, "fail", env.Verdict)
	assert.True(t, hasCategory(env.Findings, verdict.CategoryTooLarge))
	assert.Equal(t, pre.SessionID, env.SessionID, "the context_paths rejection must carry the caller's session_id")
	assert.Equal(t, initialCalls, rv.Calls, "an oversized attachment costs no reviewer call")
}

// TestValidateCompletion_AContextPathOutsidePlanRootsStaysATransportError:
// unlike a cap breach, a context_paths entry outside ANTI_TANGENT_PLAN_ROOTS
// is a caller mistake, not a size problem, so resolveContextPaths' error is
// not a *contextTooLargeError and rejectCompletionContextPaths must let it
// through as a plain transport error rather than turning it into an envelope.
func TestValidateCompletion_AContextPathOutsidePlanRootsStaysATransportError(t *testing.T) {
	allowedRoot := t.TempDir()
	outside := t.TempDir()
	p := filepath.Join(outside, "sibling.go")
	require.NoError(t, os.WriteFile(p, []byte("package feed\n"), 0o600))

	rv := &fakeReviewer{name: "anthropic", resp: passResp("m")}
	d := newDeps(t, rv)
	d.Cfg.PlanRoots = []string{allowedRoot}
	h := &handlers{deps: d}

	_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary: "done", FinalDiff: replayTestDiff, ContextPaths: []string{p},
	})
	require.Error(t, err, "a context_paths entry outside ANTI_TANGENT_PLAN_ROOTS must stay a transport error")
	assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
	assert.Zero(t, rv.Calls, "an outside-roots context_paths entry costs no reviewer call")
}
