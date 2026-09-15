package mcpsrv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/stalecomments"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// sweepDiff removes handleRetired from pkg/sweep.go. The one comment in its
// hunk that names it is line 3 of the post-change file.
const sweepDiff = "diff --git a/pkg/sweep.go b/pkg/sweep.go\n" +
	"--- a/pkg/sweep.go\n" +
	"+++ b/pkg/sweep.go\n" +
	"@@ -3,4 +3,2 @@\n" +
	" // handleRetired runs before the sweep.\n" +
	"-func handleRetired() {\n" +
	"-}\n" +
	" func sweep() {}\n"

const sweepHunkHit = "pkg/sweep.go:3: // handleRetired runs before the sweep."

// sweepFile is a post-change pkg/sweep.go consistent with sweepDiff, whose
// line 40, far outside the hunk, is a comment naming handleRetired.
func sweepFile() string {
	lines := []string{"package pkg", "", "// handleRetired runs before the sweep.", "func sweep() {}"}
	for len(lines) < 39 {
		lines = append(lines, "var diskOnlySentinel = 1")
	}
	lines = append(lines, "// the queue still calls handleRetired on shutdown.")
	return strings.Join(lines, "\n") + "\n"
}

const sweepDiskHit = "pkg/sweep.go:40: // the queue still calls handleRetired on shutdown."

func writeRepoFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func hintCfg(t *testing.T) config.Config {
	t.Helper()
	return newDeps(t, &fakeReviewer{name: "anthropic"}).Cfg
}

func TestStaleCommentHint_WithoutRepoRootScansTheDiffHunks(t *testing.T) {
	hint, advisories := staleCommentHint(hintCfg(t), sweepDiff, "", nil)
	assert.Empty(t, advisories)
	require.NotNil(t, hint)
	assert.Equal(t, []string{"handleRetired"}, hint.Names)
	assert.Equal(t, []string{sweepHunkHit}, hint.Hits)
}

func TestStaleCommentHint_NoRemovedNameMeansNoHint(t *testing.T) {
	hint, advisories := staleCommentHint(hintCfg(t), "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-x := 1\n+x := 2\n", "", nil)
	assert.Nil(t, hint)
	assert.Empty(t, advisories)
}

func TestStaleCommentHint_ReadsThePostChangeFileUnderRepoRoot(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/sweep.go", sweepFile())

	hint, advisories := staleCommentHint(hintCfg(t), sweepDiff, root, nil)

	assert.Empty(t, advisories)
	require.NotNil(t, hint)
	assert.Equal(t, []string{sweepHunkHit, sweepDiskHit}, hint.Hits)
}

func TestStaleCommentHint_RepoRootOutsidePlanRootsFallsBackWithAnAdvisory(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/sweep.go", sweepFile())
	cfg := hintCfg(t)
	cfg.PlanRoots = []string{t.TempDir()}

	hint, advisories := staleCommentHint(cfg, sweepDiff, root, nil)

	require.Len(t, advisories, 1)
	assert.Equal(t, verdict.SeverityMinor, advisories[0].Severity)
	assert.Equal(t, verdict.CategoryOther, advisories[0].Category)
	assert.Equal(t, "repo_root", advisories[0].Criterion)
	assert.Contains(t, advisories[0].Evidence, "outside ANTI_TANGENT_PLAN_ROOTS")
	require.NotNil(t, hint)
	assert.Equal(t, []string{sweepHunkHit}, hint.Hits)
}

func TestStaleCommentHint_DoesNotFollowASymlinkOutOfRepoRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := writeRepoFile(t, outside, "sweep.go", sweepFile())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	require.NoError(t, os.Symlink(target, filepath.Join(root, "pkg", "sweep.go")))

	hint, _ := staleCommentHint(hintCfg(t), sweepDiff, root, nil)

	require.NotNil(t, hint)
	assert.Equal(t, []string{sweepHunkHit}, hint.Hits)
}

func TestStaleCommentHint_SkipsAFileOverThePerFileCap(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/sweep.go", sweepFile())
	cfg := hintCfg(t)
	cfg.ContextMaxFileBytes = 64

	hint, _ := staleCommentHint(cfg, sweepDiff, root, nil)

	require.NotNil(t, hint)
	assert.Equal(t, []string{sweepHunkHit}, hint.Hits)
}

func TestReadUnderRepoRoot_ReturnsFalseOnceTheAttemptBudgetIsSpent(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/sweep.go", sweepFile())
	cfg := hintCfg(t)
	budget := &diskReadBudget{files: 0, bytes: cfg.ContextMaxPayloadBytes}

	_, ok := readUnderRepoRoot(cfg, root, "pkg/sweep.go", budget)

	assert.False(t, ok, "an existing, in-budget-bytes file is still refused once the attempt budget is spent")
}

func TestPostChangeSources_StopsReadingPastTheAttemptCap(t *testing.T) {
	root := t.TempDir()
	cfg := hintCfg(t)
	n := maxContextFiles + 1

	var diff strings.Builder
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("pkg/f%d.go", i)
		writeRepoFile(t, root, path, fmt.Sprintf("disk line %d\n", i))
		fmt.Fprintf(&diff, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1 +1 @@\n diff line %d\n",
			path, path, path, path, i)
	}
	diffFiles := stalecomments.ParseDiff(diff.String())
	require.Len(t, diffFiles, n)

	sources := postChangeSources(cfg, diffFiles, nil, root)
	require.Len(t, sources, n)

	for i := 0; i < maxContextFiles; i++ {
		want := stalecomments.FileLines(fmt.Sprintf("disk line %d\n", i))
		assert.Equal(t, want, sources[i].Lines, "source %d is within the %d-attempt cap, so it is read from disk", i, maxContextFiles)
	}
	last := sources[n-1]
	assert.Equal(t, diffFiles[n-1].Post, last.Lines,
		"the (maxContextFiles+1)th file is past the attempt cap, so its diff hunk lines are used instead of a disk read")
}

func TestStaleCommentHint_StopsReadingAtTheWholeSetCap(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/sweep.go", sweepFile())
	writeRepoFile(t, root, "pkg/queue.go", "package pkg\n// handleRetired drains the queue.\n")
	diff := sweepDiff + "--- a/pkg/queue.go\n+++ b/pkg/queue.go\n@@ -1 +1 @@\n package pkg\n"
	cfg := hintCfg(t)
	cfg.ContextMaxPayloadBytes = len(sweepFile()) + 10

	hint, _ := staleCommentHint(cfg, diff, root, nil)

	require.NotNil(t, hint)
	assert.Equal(t, []string{sweepHunkHit, sweepDiskHit}, hint.Hits,
		"pkg/queue.go no longer fits the budget, so only its hunk is scanned, and the hunk names nothing")
}

func TestStaleCommentHint_NeverReadsADeletedFile(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/old.go", "// handleRetired lived here.\n")
	diff := "diff --git a/pkg/old.go b/pkg/old.go\ndeleted file mode 100644\n--- a/pkg/old.go\n+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n-// handleRetired lived here.\n-func handleRetired() {}\n"

	hint, _ := staleCommentHint(hintCfg(t), diff, root, nil)

	assert.Nil(t, hint)
}

// TestStaleCommentHint_NeverReadsAPathEscapingOrAbsolutePostChangePath pins
// B5: a +++ header naming a path that walks out of repo_root with ".." or
// names an absolute path must never be read from disk, even when a file
// matching the removed name genuinely sits at that location. Both diff files
// below remove handleRetired, but neither's hunk context or added lines
// mention it, so a hit can only mean the disk file — outside repo_root —
// was read.
func TestStaleCommentHint_NeverReadsAPathEscapingOrAbsolutePostChangePath(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, filepath.Dir(root), "outside.go", "// handleRetired still runs the sweep.\n")

	diff := "diff --git a/../outside.go b/../outside.go\n" +
		"--- a/../outside.go\n" +
		"+++ b/../outside.go\n" +
		"@@ -1,2 +1,1 @@\n" +
		" package outside\n" +
		"-func handleRetired() {}\n" +
		"diff --git a/etc/passwd b/etc/passwd\n" +
		"--- a/etc/passwd\n" +
		"+++ /etc/passwd\n" +
		"@@ -1,2 +1,1 @@\n" +
		" root:x:0:0\n" +
		"-func handleRetired() {}\n"

	hint, advisories := staleCommentHint(hintCfg(t), diff, root, nil)

	assert.Empty(t, advisories)
	assert.Nil(t, hint, "neither post-change path is read from disk, so only the hunks are scanned, and neither hunk's context or added lines name handleRetired")
}

func TestStaleCommentHint_AFinalFilesEntryStandsInForItsDiffFileOnce(t *testing.T) {
	files := []FileArg{
		{Path: "/checkout/pkg/sweep.go", Content: sweepFile()},
		{Path: "/checkout/pkg/other.go", Content: "// handleRetired is named here too.\n"},
	}

	hint, _ := staleCommentHint(hintCfg(t), sweepDiff, "", files)

	require.NotNil(t, hint)
	assert.Equal(t, []string{sweepHunkHit, sweepDiskHit, "/checkout/pkg/other.go:1: // handleRetired is named here too."}, hint.Hits)
}

func TestValidateCompletion_StaleCommentHintReachesThePromptWithoutTheFile(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/sweep.go", sweepFile())

	env := completeWith(t, h, rv, ValidateCompletionArgs{Summary: "Removed handleRetired.", FinalDiff: sweepDiff, RepoRoot: root}, passResp("claude-opus-4-7"))

	assert.Equal(t, "pass", env.Verdict)
	assert.Contains(t, rv.LastRequest.User, "## Comments naming removed symbols (server hint)")
	assert.Contains(t, rv.LastRequest.User, sweepDiskHit)
	assert.NotContains(t, rv.LastRequest.User, "diskOnlySentinel", "only matching comment lines reach the prompt")
}

func TestValidateCompletion_AnUnusableRepoRootIsAnAdvisoryThatKeepsTheVerdict(t *testing.T) {
	h, rv := newRulingsHandlers(t)

	env := completeWith(t, h, rv, ValidateCompletionArgs{Summary: "Removed handleRetired.", FinalDiff: sweepDiff, RepoRoot: "relative/checkout"}, passResp("claude-opus-4-7"))

	assert.Equal(t, "pass", env.Verdict)
	require.NotEmpty(t, env.Findings)
	last := env.Findings[len(env.Findings)-1]
	assert.Equal(t, "repo_root", last.Criterion)
	assert.Contains(t, last.Evidence, "must be absolute")
	assert.Contains(t, rv.LastRequest.User, sweepHunkHit)
}

func TestValidateCompletion_ARejectedCallCarriesNoRepoRootAdvisory(t *testing.T) {
	h, rv := newRulingsHandlers(t)
	h.deps.Cfg.MaxPayloadBytes = 10

	env := completeWith(t, h, rv, ValidateCompletionArgs{Summary: "s", FinalDiff: sweepDiff, RepoRoot: "relative/checkout"}, passResp("claude-opus-4-7"))

	require.True(t, hasCategory(env.Findings, verdict.CategoryTooLarge))
	for _, f := range env.Findings {
		assert.NotEqual(t, "repo_root", f.Criterion)
	}
}
