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
