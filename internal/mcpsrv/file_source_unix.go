//go:build !windows

package mcpsrv

import (
	"os"
	"syscall"
)

// openNoFollow opens the already-resolved path for reading, refusing to
// follow a symlink at the final path component. This exists to close the
// TOCTOU window between resolveFileInput's EvalSymlinks/withinRoots check
// and the actual read: if the final component was swapped for a symlink
// pointing outside the checked roots in the interval between the check and
// this call, O_NOFOLLOW makes the open fail (ELOOP) instead of silently
// following it.
//
// O_NONBLOCK is also set on the open itself — this is FIX 1 for a
// regression O_NOFOLLOW introduced: without it, opening a FIFO O_RDONLY
// blocks the calling goroutine until a writer connects. resolveFileInput's
// `!info.Mode().IsRegular()` rejection runs on the stat AFTER this open, so
// a leftover mkfifo artifact at path or final_diff_path would hang here
// forever — resolveFileInput takes no context.Context, so neither
// ANTI_TANGENT_REQUEST_TIMEOUT nor MCP request cancellation can unblock it,
// leaking the goroutine and its fd for the process lifetime. O_NONBLOCK is
// a no-op for regular files (POSIX: reads from a regular file never return
// EAGAIN regardless of this flag), so it only changes behavior for the
// pathological FIFO case, letting the open return immediately so the
// IsRegular check below can do its job.
//
// Plain syscall.Open (rather than golang.org/x/sys/unix, which this module
// only pulls in transitively) is enough here: O_NOFOLLOW and O_NONBLOCK are
// both part of the POSIX-ish syscall package on every non-Windows GOOS this
// project targets.
func openNoFollow(resolved string) (*os.File, error) {
	fd, err := syscall.Open(resolved, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), resolved), nil
}
