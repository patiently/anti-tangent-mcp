//go:build !windows

package mcpsrv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteTargetRefusesSymlinkLeaf covers the AC "On Unix: a symlink at the
// leaf is refused under BOTH overwrite values". Asserting only that an error
// came back would still pass a resolveWriteTarget that dropped O_NOFOLLOW
// and instead let the open follow the symlink and then failed for some
// unrelated reason (e.g. permissions) — so this also asserts the symlink's
// target file was never touched, which is the property O_NOFOLLOW actually
// protects and the property a dropped flag would break.
func TestWriteTargetRefusesSymlinkLeaf(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.go")
	require.NoError(t, os.WriteFile(victim, []byte("precious"), 0o644))

	link := filepath.Join(dir, "link.go")
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, ow := range []bool{false, true} {
		_, err := resolveWriteTarget(link, nil, ow)
		assert.Errorf(t, err, "overwrite=%v: writing through a symlink must be refused", ow)
	}

	b, readErr := os.ReadFile(victim)
	require.NoError(t, readErr)
	assert.Equal(t, "precious", string(b), "symlink target was modified")
}

// TestWriteTargetAllowsSymlinkedParentInsideRoots covers the AC "A
// symlinked parent inside the roots resolves and is allowed". The roots
// argument names `real` (the symlink's target), not `link` — a
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
