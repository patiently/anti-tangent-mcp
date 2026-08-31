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
// Plain syscall.Open (rather than golang.org/x/sys/unix, which this module
// only pulls in transitively) is enough here: O_NOFOLLOW is part of the
// POSIX-ish syscall package on every non-Windows GOOS this project targets.
func openNoFollow(resolved string) (*os.File, error) {
	fd, err := syscall.Open(resolved, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), resolved), nil
}
