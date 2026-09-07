//go:build !windows

package mcpsrv

import (
	"os"
	"syscall"
)

// openWriteNoFollow opens resolved for writing, refusing to follow a symlink
// at the final component.
//
// O_NOFOLLOW is the load-bearing flag: without it a symlink planted at the
// leaf between the containment check and the open redirects the write to an
// arbitrary path outside the roots. It applies regardless of overwrite —
// writing THROUGH a symlink is never what code_write means.
//
// O_EXCL additionally makes "does not already exist" atomic when overwrite is
// false, rather than a stat-then-open race.
func openWriteNoFollow(resolved string, overwrite bool) (*os.File, error) {
	flags := syscall.O_WRONLY | syscall.O_CREAT | syscall.O_NOFOLLOW
	if overwrite {
		flags |= syscall.O_TRUNC
	} else {
		flags |= syscall.O_EXCL
	}
	fd, err := syscall.Open(resolved, flags, 0o644)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: resolved, Err: err}
	}
	return os.NewFile(uintptr(fd), resolved), nil
}
