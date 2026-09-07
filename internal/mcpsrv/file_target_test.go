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
// directory) and overwrite=true fails EISDIR (O_TRUNC against a directory).
// But that safety would come from incidental POSIX open(2) semantics, not
// from this function's own guard — so this test proves the guard itself
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
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(p, []byte("a very long previous body"), 0o644))

	f, err := resolveWriteTarget(p, nil, true)
	require.NoError(t, err)
	_, err = f.WriteString("new")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	b, readErr := os.ReadFile(p)
	require.NoError(t, readErr)
	assert.Equal(t, "new", string(b), "O_TRUNC missing?")
}

// TestWriteTargetAllowsSymlinkedParentInsideRoots covers the AC "A
// symlinked parent inside the roots resolves and is allowed". It lives here
// rather than in file_target_unix_test.go: the accepted Windows non-goal is
// narrowly about the LEAF guarantee (no O_NOFOLLOW equivalent there).
// Parent resolution is filepath.EvalSymlinks + withinRoots, which have no
// platform-specific behavior at all, so gating this test to !windows would
// leave this AC with no Windows coverage for code that runs identically on
// every platform. Degrades gracefully (skip, not fail) where symlinks need
// privilege — the same pattern TestWriteTargetRejectsControlCharInResolvedParent
// above already uses.
//
// The roots argument names `real` (the symlink's target), not `link` — a
// resolveWriteTarget that containment-checked the unresolved parent instead
// of parentResolved would refuse this case, since `link`'s directory entry
// itself is not inside `real`.
func TestWriteTargetAllowsSymlinkedParentInsideRoots(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	f, err := resolveWriteTarget(filepath.Join(link, "a.go"), []string{real}, false)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}
