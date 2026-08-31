package mcpsrv

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveFileInput(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "plan.md")
	require.NoError(t, os.WriteFile(real, []byte("hello"), 0o644))

	// Resolve the temp dir itself — macOS /var -> /private/var makes the raw
	// t.TempDir() path a symlink, which would false-fail the roots checks.
	dirResolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	t.Run("reads file and reports provenance", func(t *testing.T) {
		content, src, err := resolveFileInput(real, nil, 1024)
		require.NoError(t, err)
		assert.Equal(t, "hello", content)
		assert.Equal(t, 5, src.Bytes)
		// sha256("hello")
		assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", src.SHA256)
		assert.Equal(t, filepath.Join(dirResolved, "plan.md"), src.Path)
	})

	t.Run("relative path rejected", func(t *testing.T) {
		_, _, err := resolveFileInput("docs/plan.md", nil, 1024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "absolute")
	})

	t.Run("missing file", func(t *testing.T) {
		_, _, err := resolveFileInput(filepath.Join(dir, "nope.md"), nil, 1024)
		require.Error(t, err)
	})

	t.Run("directory rejected", func(t *testing.T) {
		_, _, err := resolveFileInput(dir, nil, 1024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "regular file")
	})

	t.Run("over cap reports true size and does not read", func(t *testing.T) {
		big := filepath.Join(dir, "big.md")
		require.NoError(t, os.WriteFile(big, bytes.Repeat([]byte("x"), 5000), 0o644))
		_, src, err := resolveFileInput(big, nil, 1024)
		require.ErrorIs(t, err, errTooLarge)
		assert.Equal(t, 5000, src.Bytes, "true size, not cap+1")
	})

	t.Run("symlink escaping root rejected", func(t *testing.T) {
		outside := t.TempDir()
		target := filepath.Join(outside, "secret.md")
		require.NoError(t, os.WriteFile(target, []byte("secret"), 0o644))
		link := filepath.Join(dir, "escape.md")
		require.NoError(t, os.Symlink(target, link))

		_, _, err := resolveFileInput(link, []string{dirResolved}, 1024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
	})

	t.Run("symlink into root accepted", func(t *testing.T) {
		linkDir := t.TempDir()
		link := filepath.Join(linkDir, "into.md")
		require.NoError(t, os.Symlink(real, link))

		content, _, err := resolveFileInput(link, []string{dirResolved}, 1024)
		require.NoError(t, err)
		assert.Equal(t, "hello", content)
	})

	t.Run("root prefix does not match sibling directory", func(t *testing.T) {
		parent := t.TempDir()
		parentResolved, err := filepath.EvalSymlinks(parent)
		require.NoError(t, err)
		require.NoError(t, os.Mkdir(filepath.Join(parent, "foo"), 0o755))
		require.NoError(t, os.Mkdir(filepath.Join(parent, "foobar"), 0o755))
		victim := filepath.Join(parent, "foobar", "p.md")
		require.NoError(t, os.WriteFile(victim, []byte("x"), 0o644))

		_, _, err = resolveFileInput(victim, []string{filepath.Join(parentResolved, "foo")}, 1024)
		require.Error(t, err, "/foo must not authorize /foobar")
	})

	t.Run("empty roots is unrestricted", func(t *testing.T) {
		_, _, err := resolveFileInput(real, nil, 1024)
		require.NoError(t, err)
	})

	t.Run("filesystem root as root allows a nested path", func(t *testing.T) {
		root := filepath.VolumeName(real) + string(filepath.Separator)
		_, _, err := resolveFileInput(real, []string{root}, 1024)
		require.NoError(t, err, `root "/" must authorize everything beneath it`)
	})

	// FIX 4 regression: the final path component must not be followed if it
	// is a symlink at open time, even though it passed EvalSymlinks +
	// withinRoots a moment earlier as a plain file. This simulates the swap
	// by pointing resolveFileInput directly at a symlink that itself escapes
	// the allowed root — i.e. exercising openNoFollow's refusal rather than
	// the earlier EvalSymlinks-based check (which a real TOCTOU race would
	// have already passed before the swap).
	t.Run("open refuses to follow a symlink at the final component", func(t *testing.T) {
		outside := t.TempDir()
		secret := filepath.Join(outside, "secret.md")
		require.NoError(t, os.WriteFile(secret, []byte("secret"), 0o644))

		swapDir := t.TempDir()
		swapDirResolved, err := filepath.EvalSymlinks(swapDir)
		require.NoError(t, err)
		link := filepath.Join(swapDir, "swapped.md")
		require.NoError(t, os.Symlink(secret, link))

		f, err := openNoFollow(link)
		if err == nil {
			f.Close()
			t.Fatal("openNoFollow followed a symlink at the final component")
		}

		// resolveFileInput itself still rejects this shape too, via the
		// existing EvalSymlinks+withinRoots check — confirming the two
		// layers agree rather than one silently overriding the other.
		_, _, err = resolveFileInput(link, []string{swapDirResolved}, 1024)
		require.Error(t, err)
	})

	t.Run("read is capped even when the pre-read stat would have allowed it", func(t *testing.T) {
		// Exercises the wiring end-to-end: a file within the cap at the
		// pre-read stat still round-trips through resolveFileInput normally.
		// readCapped's own boundary behavior (independent of any stat) is
		// covered directly by TestReadCapped below.
		small := filepath.Join(dir, "small.md")
		require.NoError(t, os.WriteFile(small, bytes.Repeat([]byte("y"), 4), 0o644))
		content, src, err := resolveFileInput(small, nil, 4)
		require.NoError(t, err)
		assert.Equal(t, "yyyy", content)
		assert.Equal(t, 4, src.Bytes)
	})
}

// TestReadCapped unit-tests the cap-at-read boundary in isolation from any
// filesystem stat — this is the FIX 4 defense against a file growing past
// its cap between os.Stat and the read: readCapped enforces the cap purely
// from what it actually reads, so it holds even when a stat taken moments
// earlier said the file was small enough.
func TestReadCapped(t *testing.T) {
	t.Run("at cap passes", func(t *testing.T) {
		b, err := readCapped(strings.NewReader("abcd"), 4)
		require.NoError(t, err)
		assert.Equal(t, "abcd", string(b))
	})

	t.Run("one byte over cap is rejected regardless of what a prior stat claimed", func(t *testing.T) {
		_, err := readCapped(strings.NewReader("abcde"), 4)
		require.ErrorIs(t, err, errTooLarge)
	})

	t.Run("well under cap passes", func(t *testing.T) {
		b, err := readCapped(strings.NewReader("a"), 4)
		require.NoError(t, err)
		assert.Equal(t, "a", string(b))
	})
}

func TestWithinRoots(t *testing.T) {
	sep := string(filepath.Separator)

	t.Run("filesystem root matches everything beneath it", func(t *testing.T) {
		assert.True(t, withinRoots(sep+filepath.Join("home", "x", "plan.md"), []string{sep}))
	})

	t.Run("root does not authorize a sibling with a shared prefix", func(t *testing.T) {
		root := sep + filepath.Join("home", "foo")
		victim := sep + filepath.Join("home", "foobar")
		assert.False(t, withinRoots(victim, []string{root}), "/home/foo must not authorize /home/foobar")
	})

	t.Run("exact match root is contained", func(t *testing.T) {
		root := sep + filepath.Join("home", "foo")
		assert.True(t, withinRoots(root, []string{root}))
	})
}

// TestWithinRootsFold is the FIX 3 regression test: ANTI_TANGENT_PLAN_ROOTS
// containment must be case-insensitive on Windows (operator-written
// casing vs. EvalSymlinks' on-disk casing must still match) and must stay
// byte-for-byte case-sensitive everywhere else, since folding case on Unix
// would widen the roots allowlist rather than merely tolerate a cosmetic
// difference. This exercises the fold decision directly — see
// withinRootsFold's doc comment for why the real filepath.Rel Windows
// behavior can't be exercised from a non-Windows test binary, and why
// testing the fold decision in isolation is what "runs everywhere" here.
func TestWithinRootsFold(t *testing.T) {
	t.Run("windows folds differently-cased volume and path elements to the same key", func(t *testing.T) {
		assert.Equal(t,
			withinRootsFold(`C:\Repo\plan.md`, "windows"),
			withinRootsFold(`c:\repo\plan.md`, "windows"),
			"a Windows build must treat these as the same path for containment purposes")
	})

	t.Run("non-windows leaves case untouched", func(t *testing.T) {
		s := "/Home/Foo/Plan.md"
		assert.Equal(t, s, withinRootsFold(s, "linux"), "unix must not fold case at all")
		assert.NotEqual(t,
			withinRootsFold("/Home/Foo", "darwin"),
			withinRootsFold("/home/foo", "darwin"),
			"non-windows GOOS values must stay case-sensitive — folding here would widen the roots allowlist")
	})
}

func TestFileSourceString(t *testing.T) {
	t.Run("zero value renders empty", func(t *testing.T) {
		assert.Equal(t, "", fileSource{}.String())
	})

	t.Run("with hash", func(t *testing.T) {
		s := fileSource{Path: "/abs/plan.md", Bytes: 170158, SHA256: "4f2a9c1eabcdef0123456789"}
		assert.Equal(t, "/abs/plan.md (170158 B, sha256 4f2a9c1e…)", s.String())
	})

	t.Run("empty hash omits the sha256 fragment", func(t *testing.T) {
		// This is the too-large early exit's shape: resolveFileInput returns
		// before hashing, so SHA256 is empty. The line must not render as
		// "sha256 …" with zero hex digits.
		s := fileSource{Path: "/abs/path.md", Bytes: 5000}
		assert.Equal(t, "/abs/path.md (5000 B)", s.String())
		assert.NotContains(t, s.String(), "sha256")
	})
}
