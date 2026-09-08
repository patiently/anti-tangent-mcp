package mcpsrv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteTargetRejectsInvalidPath covers AC "Relative, empty and
// whitespace-only paths are refused, each with its own table case". Each
// case is a distinct kind of invalid input to resolveWriteTarget's own
// upfront validation, before any filesystem access happens.
func TestWriteTargetRejectsInvalidPath(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"relative", "rel/path.go"},
		{"empty", ""},
		{"whitespace-only", "   \t  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveWriteTarget(tc.target, nil, false)
			require.Error(t, err)
		})
	}
}

// TestWriteTargetRejectsDotDotLeaf covers a path whose base name is "..",
// e.g. target_path "/root/foo/..". filepath.Base returns the literal string
// ".." for that input, which the "leaf == \".\"" guard alone does not catch.
//
// "foo" is a real, pre-existing directory here so that, absent the guard,
// resolveWriteTarget would sail past the "parent must exist" check, resolve
// straight through to dir itself (foo/.. cleans to dir), and reach the
// open() call — where review found this is in fact not independently
// exploitable: overwrite=false fails EEXIST (O_EXCL against an existing
// directory) and overwrite=true is refused by inspectOverwriteTarget's
// "not a regular file" branch. But that safety would come from a check aimed
// at something else — so this test proves the guard itself
// fires, by asserting the specific "does not name a file" message, which
// only the guard (not an open(2) failure) produces, and by asserting `foo`
// itself was never touched.
func TestWriteTargetRejectsDotDotLeaf(t *testing.T) {
	dir := t.TempDir()
	foo := filepath.Join(dir, "foo")
	require.NoError(t, os.Mkdir(foo, 0o755))

	// Built by string concatenation, not filepath.Join: Join would Clean the
	// ".." away (collapsing "foo/.." back to dir), which is exactly the
	// behavior that must NOT be relied upon here — filepath.Base does not
	// clean multi-component paths that way, so the literal ".." survives to
	// reach resolveWriteTarget's own guard.
	target := foo + string(filepath.Separator) + ".."
	require.Equal(t, "..", filepath.Base(target), "test setup: Base must still see the literal \"..\"")

	_, err := resolveWriteTarget(target, nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not name a file")

	info, statErr := os.Stat(foo)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "foo must still be an untouched directory")
}

func TestWriteTargetRejectsMissingParent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope", "a.go")
	_, err := resolveWriteTarget(p, nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parent")
}

func TestWriteTargetRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	_, err := resolveWriteTarget(filepath.Join(other, "a.go"), []string{root}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PLAN_ROOTS")
}

// TestWriteTargetRejectsControlOrFormatChar covers the AC that a Unicode
// FORMAT character (e.g. U+200B) is refused, not only a C0 control like a
// newline — the two take different branches of rejectControlChars'
// forgesPromptStructure predicate (r < 0x20 vs unicode.Is(unicode.Cf, r)).
// A test that only ever supplied a newline would still pass if the Cf branch
// were deleted from rejectControlChars entirely.
func TestWriteTargetRejectsControlOrFormatChar(t *testing.T) {
	cases := []struct {
		name string
		leaf string
		want string // the U+XXXX the error must name
	}{
		{"C0 control (newline)", "a\nb.go", "U+000A"},
		{"Unicode format character (ZERO WIDTH SPACE)", "a​b.go", "U+200B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, err := resolveWriteTarget(filepath.Join(dir, tc.leaf), nil, false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestWriteTargetRejectsControlCharInResolvedParent covers the AC "A
// resolved path containing a control or Unicode format character is
// refused" as distinct from the leaf-only case above: here the literal
// target string passed in is clean, and the format character only appears
// after the parent symlink is resolved. A resolveWriteTarget that checked
// rejectControlChars against the unresolved target (or against parent
// rather than parentResolved) would pass this test's raw input straight
// through and only fail the assertion below.
func TestWriteTargetRejectsControlCharInResolvedParent(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "gen​ops")
	require.NoError(t, os.Mkdir(realParent, 0o755))

	link := filepath.Join(root, "link")
	if err := os.Symlink(realParent, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	target := filepath.Join(link, "a.go") // no format character in the literal string
	_, err := resolveWriteTarget(target, nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "U+200B")
}

func TestWriteTargetRefusesExistingWithoutOverwrite(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))

	_, err := resolveWriteTarget(p, nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overwrite")

	b, readErr := os.ReadFile(p)
	require.NoError(t, readErr)
	assert.Equal(t, "old", string(b), "the existing file must be untouched")
}

func TestWriteTargetOverwriteTruncates(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(p, []byte("a very long previous body"), 0o644))

	f, err := resolveWriteTarget(p, nil, true)
	require.NoError(t, err)
	_, err = f.WriteString("new")
	require.NoError(t, err)
	require.NoError(t, f.Commit())

	b, readErr := os.ReadFile(p)
	require.NoError(t, readErr)
	assert.Equal(t, "new", string(b), "overwrite must replace the previous body")
	assertNoTempLeftBehind(t, dir, "a.go")
}

// TestWriteTargetAllowsSymlinkedParentInsideRoots covers the AC "A
// symlinked parent inside the roots resolves and is allowed". It lives here
// rather than in file_target_unix_test.go: the accepted Windows non-goal is
// narrowly about the LEAF guarantee (no O_NOFOLLOW equivalent there).
// Parent resolution is filepath.EvalSymlinks + withinRoots, which have no
// platform-specific behavior at all, so a build tag would leave this AC with
// no Windows coverage for code that runs identically on every platform. It
// still skips at RUNTIME on Windows, because target_path writes are refused
// outright there (file_target_windows.go) and this test needs the write to
// succeed — a skip keeps the reason visible in the test output, which a build
// tag would not. Degrades gracefully (skip, not fail) where symlinks need
// privilege too — the same pattern
// TestWriteTargetRejectsControlCharInResolvedParent above already uses.
//
// The roots argument names `real` (the symlink's target), not `link` — a
// resolveWriteTarget that containment-checked the unresolved parent instead
// of parentResolved would refuse this case, since `link`'s directory entry
// itself is not inside `real`.
func TestWriteTargetAllowsSymlinkedParentInsideRoots(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	f, err := resolveWriteTarget(filepath.Join(link, "a.go"), []string{real}, false)
	require.NoError(t, err)
	require.NoError(t, f.Commit())
}

// TestWriteTargetOverwritePreservesFileMode pins the one thing the temp-file
// rewrite could have silently taken away. The old O_TRUNC open kept the same
// inode, so the file's permissions survived for free; os.CreateTemp opens
// 0600, and renaming that over the target would tighten a group- or
// world-readable generated file to owner-only without saying so.
func TestWriteTargetOverwritePreservesFileMode(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))
	require.NoError(t, os.Chmod(p, 0o640))

	f, err := resolveWriteTarget(p, nil, true)
	require.NoError(t, err)
	_, err = f.WriteString("new")
	require.NoError(t, err)
	require.NoError(t, f.Commit())

	info, statErr := os.Stat(p)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm(), "overwrite must not change the file's mode")
}

// TestWriteTargetOverwriteRenameFailureKeepsOriginal exercises the last
// failure point in the overwrite sequence — the rename itself. It is the one
// step with no counterpart in the old implementation, so it is also the one a
// caller-level failure injection cannot reach: the seam in worker_handlers.go
// can fail WriteString or Commit as a whole, not the rename inside it.
//
// The injection is to swap the target for a directory after the transaction
// has already been opened, which is a state rename(2) must refuse (ENOTDIR /
// EISDIR). Both halves of the contract are then asserted: the thing standing
// at target_path is untouched, and no temp file survives.
func TestWriteTargetOverwriteRenameFailureKeepsOriginal(t *testing.T) {
	skipIfWriteTargetUnsupported(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))

	f, err := resolveWriteTarget(p, nil, true)
	require.NoError(t, err)
	_, err = f.WriteString("new")
	require.NoError(t, err)

	require.NoError(t, os.Remove(p))
	require.NoError(t, os.Mkdir(p, 0o755))

	err = f.Commit()
	require.Error(t, err, "renaming onto a directory must fail")
	assert.Contains(t, err.Error(), p, "the error must name the target the caller asked for")

	info, statErr := os.Stat(p)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "a failed rename must leave the target exactly as it found it")
	assertNoTempLeftBehind(t, dir, "a.go")

	// Commit already cleaned up, so the caller's own Discard must be a
	// harmless no-op rather than a second removal aimed at the target.
	assert.Contains(t, f.Discard(), "unchanged")
	_, statErr = os.Stat(p)
	assert.NoError(t, statErr, "Discard after a self-cleaning Commit must not remove the target")
}

// assertNoTempLeftBehind fails if dir holds anything beyond want. Every temp
// file the write path creates is a sibling of the target (rename(2) is atomic
// only within one filesystem), so a leaked one shows up here and nowhere
// else — which is what makes a whole-directory listing the right assertion
// rather than an overreach.
func assertNoTempLeftBehind(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}
	assert.ElementsMatch(t, want, got, "a temporary file was left behind in %s", dir)
}

// skipIfWriteTargetUnsupported skips a test that needs a target_path write to
// actually happen, on platforms where code_write refuses target_path outright
// (Windows — see file_target_windows.go). Tests asserting a REFUSAL do not
// need this: those inputs are rejected before the platform gate is reached,
// so they assert the same thing everywhere.
func skipIfWriteTargetUnsupported(t *testing.T) {
	t.Helper()
	if err := writeTargetSupported(); err != nil {
		t.Skipf("target_path writes are not supported on this platform: %v", err)
	}
}
