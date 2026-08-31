//go:build !windows

// Package mcpsrv: Unix-only regression coverage that would either hang or
// assert the wrong thing on Windows. Split into its own build-tagged file
// (rather than living in file_source_test.go, which compiles on every GOOS)
// because:
//   - FIX 1 (TestResolveFileInput_FIFODoesNotHang): syscall.Mkfifo has no
//     Windows counterpart — referencing it at all would break
//     `GOOS=windows go build`.
//   - FIX 4 (TestOpenNoFollow_RejectsSymlinkAtFinalComponent): openNoFollow
//     only refuses to follow a final-component symlink on Unix, via
//     O_NOFOLLOW (see file_source_unix.go); on Windows it is a plain
//     os.Open and follows the symlink, which is correct behavior there,
//     not a regression — so the rejection assertion only holds on Unix.
package mcpsrv

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestResolveFileInput_FIFODoesNotHang guards against the regression
// openNoFollow's O_NOFOLLOW open introduced: syscall.Open(path,
// O_RDONLY|O_NOFOLLOW) on a named pipe blocks until a writer connects,
// which ran BEFORE resolveFileInput's `!info.Mode().IsRegular()` check —
// so a leftover mkfifo artifact at plan_path/final_diff_path would hang the
// tool call forever. resolveFileInput takes no context.Context, so neither
// ANTI_TANGENT_REQUEST_TIMEOUT nor MCP request cancellation could unblock
// it; the goroutine and its fd would leak for the process lifetime.
//
// The call runs in a goroutine with a short timeout so a regression here
// fails this test fast instead of wedging the whole `go test` run (and CI
// with it).
func TestResolveFileInput_FIFODoesNotHang(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "leftover.fifo")
	require.NoError(t, syscall.Mkfifo(fifoPath, 0o600))

	errCh := make(chan error, 1)
	go func() {
		_, _, err := resolveFileInput(fifoPath, nil, 1024)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		require.Error(t, err, "a FIFO must be rejected as not-a-regular-file, not silently read")
		require.Contains(t, err.Error(), "regular file")
	case <-time.After(5 * time.Second):
		t.Fatal("resolveFileInput hung opening a FIFO — O_NONBLOCK regression (FIX 1); " +
			"the fd and this goroutine now leak for the process lifetime")
	}
}

// TestOpenNoFollow_RejectsSymlinkAtFinalComponent is the FIX 4 regression
// test: the final path component must not be followed if it is a symlink
// at open time, even though it passed EvalSymlinks + withinRoots a moment
// earlier as a plain file. This simulates the swap by pointing
// resolveFileInput directly at a symlink that itself escapes the allowed
// root — i.e. exercising openNoFollow's refusal rather than the earlier
// EvalSymlinks-based check (which a real TOCTOU race would have already
// passed before the swap).
//
// Unix-only: openNoFollow's O_NOFOLLOW open is what rejects this on Unix
// (file_source_unix.go). On Windows, openNoFollow is a plain os.Open
// (file_source_windows.go) and follows the symlink instead — correct
// behavior for that platform, not a regression — so this assertion would
// fail against correct code there. The separate EvalSymlinks+withinRoots
// layer that also rejects a symlink escaping the root is platform-neutral
// and already covered by TestResolveFileInput's "symlink escaping root
// rejected" subtest in file_source_test.go.
func TestOpenNoFollow_RejectsSymlinkAtFinalComponent(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	require.NoError(t, os.WriteFile(secret, []byte("secret"), 0o644))

	swapDir := t.TempDir()
	link := filepath.Join(swapDir, "swapped.md")
	require.NoError(t, os.Symlink(secret, link))

	f, err := openNoFollow(link)
	if err == nil {
		_ = f.Close()
		t.Fatal("openNoFollow followed a symlink at the final component")
	}
}
