package mcpsrv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

func ctxCfg(fileCap, setCap int, roots []string) config.Config {
	return config.Config{
		ContextMaxFileBytes:    fileCap,
		ContextMaxPayloadBytes: setCap,
		PlanRoots:              roots,
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestResolveContextPaths_HappyPath(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.go", "package a\n")
	b := writeTemp(t, dir, "b.go", "package b\n")

	files, total, err := resolveContextPaths([]string{a, b}, ctxCfg(1000, 10000, nil))
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "package a\n", files[0].Content)
	assert.Equal(t, "package b\n", files[1].Content)
	assert.Equal(t, len("package a\n")+len("package b\n"), total)
	assert.NotEmpty(t, files[0].Source.SHA256)
}

func TestResolveContextPaths_CountCapRefusesBeforeAnyRead(t *testing.T) {
	// A path that does not exist: if the count cap is checked first (as it
	// must be), we never try to open it and the error is about the count.
	paths := make([]string, maxContextFiles+1)
	for i := range paths {
		paths[i] = "/nonexistent/does-not-exist.go"
	}
	_, _, err := resolveContextPaths(paths, ctxCfg(1000, 10000, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "51")
	assert.Contains(t, err.Error(), "50")
	assert.NotContains(t, err.Error(), "does-not-exist",
		"count cap must be enforced before any path is opened")

	// All three caps surface the same way. The count cap used to be the odd
	// one out — a plain error, i.e. a transport error, while the two byte
	// caps produced the too-large ENVELOPE — so a caller had to handle the
	// same class of mistake in two places.
	var tle *contextTooLargeError
	require.True(t, errors.As(err, &tle), "the count cap must map to the too-large envelope")
	assert.Equal(t, 51, tle.Count)
	assert.Equal(t, maxContextFiles, tle.Limit)
	assert.Empty(t, tle.Path, "a count breach names no single file")
}

// resolveDirInput's roots check had no coverage with a non-nil roots list —
// only the unrestricted (nil) case was exercised, so the allowlist could have
// been inverted or skipped entirely and every test would still pass.
func TestResolveDirInput_RootsAllowlist(t *testing.T) {
	allowed := t.TempDir()
	other := t.TempDir()
	roots := []string{mustEval(t, allowed)}

	sub := filepath.Join(allowed, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o750))

	got, err := resolveDirInput(sub, roots)
	require.NoError(t, err, "a directory under an allowlisted root must resolve")
	assert.Equal(t, mustEval(t, sub), got)

	got, err = resolveDirInput(allowed, roots)
	require.NoError(t, err, "the root itself must resolve")
	assert.Equal(t, mustEval(t, allowed), got)

	_, err = resolveDirInput(other, roots)
	require.Error(t, err, "a directory outside every root must be refused")
	assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")

	// A symlink INSIDE an allowlisted root that points outside it must be
	// refused on the resolved target, not on the requested path.
	link := filepath.Join(allowed, "escape")
	require.NoError(t, os.Symlink(other, link))
	_, err = resolveDirInput(link, roots)
	require.Error(t, err, "symlinks resolve before the roots check")
	assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
}

func TestResolveContextPaths_PerFileCap(t *testing.T) {
	dir := t.TempDir()
	big := writeTemp(t, dir, "big.go", strings.Repeat("x", 300))

	_, _, err := resolveContextPaths([]string{big}, ctxCfg(100, 10000, nil))
	var tle *contextTooLargeError
	require.True(t, errors.As(err, &tle))
	assert.Equal(t, 100, tle.Limit)
	assert.Contains(t, tle.Path, "big.go")
	assert.Contains(t, tle.Error(), "ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES")
}

func TestResolveContextPaths_SetCap(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.go", strings.Repeat("a", 60))
	b := writeTemp(t, dir, "b.go", strings.Repeat("b", 60))

	_, _, err := resolveContextPaths([]string{a, b}, ctxCfg(100, 100, nil))
	var tle *contextTooLargeError
	require.True(t, errors.As(err, &tle))
	assert.Equal(t, "", tle.Path, "set-level breach names no single file")
	assert.Equal(t, 120, tle.Bytes)
	assert.Equal(t, 100, tle.Limit)
	assert.Contains(t, tle.Error(), "ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES")
}

func TestResolveContextPaths_DeduplicatesAfterSymlinkResolution(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.go", strings.Repeat("a", 60))
	link := filepath.Join(dir, "link.go")
	require.NoError(t, os.Symlink(a, link))

	// Set cap of 100 would be breached if the 60-byte file were counted twice.
	files, total, err := resolveContextPaths([]string{a, link}, ctxCfg(100, 100, nil))
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, 60, total)
}

func TestResolveContextPaths_BadPathsAreOrdinaryErrors(t *testing.T) {
	dir := t.TempDir()
	for name, p := range map[string]string{
		"missing":  filepath.Join(dir, "nope.go"),
		"relative": "internal/config/config.go",
		"dir":      dir,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolveContextPaths([]string{p}, ctxCfg(1000, 10000, nil))
			require.Error(t, err)
			var tle *contextTooLargeError
			assert.False(t, errors.As(err, &tle),
				"bad input is a transport error, not a too-large envelope")
		})
	}
}

func TestResolveContextPaths_OutsideRoots(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	a := writeTemp(t, other, "a.go", "package a\n")

	_, _, err := resolveContextPaths([]string{a}, ctxCfg(1000, 10000, []string{dir}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
}

// Security-relevant (design §11): a symlink that LIVES inside
// ANTI_TANGENT_PLAN_ROOTS but RESOLVES to a target outside it must be
// refused just like a path given directly outside roots. The roots check
// must apply to the symlink-resolved (real) path, never to where the
// symlink itself sits — otherwise a symlink planted inside an allowed root
// is a bypass that reads any file on disk into the reviewer prompt.
func TestResolveContextPaths_SymlinkInsideRootsButResolvesOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := writeTemp(t, outside, "secret.go", "package secret\n")
	link := filepath.Join(root, "link.go")
	require.NoError(t, os.Symlink(secret, link))

	_, _, err := resolveContextPaths([]string{link}, ctxCfg(1000, 10000, []string{root}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
}

func TestResolveContextPaths_EmptyInput(t *testing.T) {
	files, total, err := resolveContextPaths(nil, ctxCfg(1000, 10000, nil))
	require.NoError(t, err)
	assert.Empty(t, files)
	assert.Equal(t, 0, total)
}

func TestResolveDirInput(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.go", "package a\n")

	got, err := resolveDirInput(dir, nil)
	require.NoError(t, err)
	assert.Equal(t, mustEval(t, dir), got)

	_, err = resolveDirInput(f, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")

	_, err = resolveDirInput("relative/path", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}
