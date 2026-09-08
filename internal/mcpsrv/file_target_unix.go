//go:build !windows

package mcpsrv

import (
	"os"
	"syscall"
)

// writeTargetSupported reports whether this platform can offer the leaf
// guarantee code_write's target_path depends on. Unix can — see
// openCreateNoFollow and inspectOverwriteTarget — so target_path is enabled.
func writeTargetSupported() error { return nil }

// openCreateNoFollow creates resolved, refusing to follow a symlink at the
// final component and refusing outright if anything is already there.
//
// Only the overwrite: false path reaches this. The overwrite path never opens
// the target at all — it writes a sibling temp file and renames it — so this
// no longer needs an overwrite mode.
//
// O_EXCL is the load-bearing flag for both properties at once: it makes "does
// not already exist" atomic rather than a stat-then-open race, and POSIX
// requires it to fail when the final component is a symlink, dangling or not.
// O_NOFOLLOW is therefore redundant here, and kept anyway — it states the
// intent in the flags rather than in a comment, so a later edit that relaxes
// O_EXCL (say, to allow overwriting again) does not silently take the
// symlink refusal with it.
func openCreateNoFollow(resolved string) (*os.File, error) {
	flags := syscall.O_WRONLY | syscall.O_CREAT | syscall.O_EXCL | syscall.O_NOFOLLOW
	fd, err := syscall.Open(resolved, flags, 0o644)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: resolved, Err: err}
	}
	return os.NewFile(uintptr(fd), resolved), nil
}
