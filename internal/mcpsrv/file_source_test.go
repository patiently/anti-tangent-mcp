package mcpsrv

import (
	"bytes"
	"os"
	"path/filepath"
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
