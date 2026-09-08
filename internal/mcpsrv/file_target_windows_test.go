//go:build windows

package mcpsrv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteTargetRefusedOnWindows pins the platform contract that
// file_target_windows.go states: target_path is not written on Windows at
// all. It is the Windows counterpart of file_target_unix_test.go's
// TestWriteTargetRefusesSymlinkLeaf — the same question ("can a planted leaf
// redirect this write?") answered by refusing the whole operation instead of
// by a guard nothing here can test.
//
// Asserting only that an error came back would also pass for a target that
// was refused for some unrelated reason and still created, so this asserts
// the refusal names the platform AND that nothing appeared on disk.
func TestWriteTargetRefusedOnWindows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.go")

	for _, ow := range []bool{false, true} {
		_, err := resolveWriteTarget(target, []string{dir}, ow)
		require.Errorf(t, err, "overwrite=%v: target_path must be refused on Windows", ow)
		assert.Contains(t, err.Error(), "Windows")
		assert.Contains(t, err.Error(), "Omit target_path",
			"the refusal must tell the caller what to do instead")

		_, statErr := os.Stat(target)
		assert.Truef(t, os.IsNotExist(statErr), "overwrite=%v: nothing may be created", ow)
	}
}

// TestWriteTargetOverwriteRefusalLeavesOriginalOnWindows checks the refusal
// happens BEFORE anything is opened or truncated. A gate placed after the
// open would still return an error while having already emptied the caller's
// file — the exact damage the overwrite path was rewritten to prevent.
func TestWriteTargetOverwriteRefusalLeavesOriginalOnWindows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.go")
	const original = "package a\n"
	require.NoError(t, os.WriteFile(target, []byte(original), 0o644))

	_, err := resolveWriteTarget(target, []string{dir}, true)
	require.Error(t, err)

	b, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, original, string(b), "the refusal must not touch the caller's file")
	assertNoTempLeftBehind(t, dir, "a.go")
}
