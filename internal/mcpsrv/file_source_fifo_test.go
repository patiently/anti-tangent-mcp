//go:build !windows

// Package mcpsrv: FIX 1 regression coverage. Split into its own
// build-tagged file (rather than living in file_source_test.go, which
// compiles on every GOOS) because syscall.Mkfifo has no Windows
// counterpart — referencing it at all would break `GOOS=windows go build`.
package mcpsrv

import (
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
