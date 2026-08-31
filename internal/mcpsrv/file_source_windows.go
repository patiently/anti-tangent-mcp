//go:build windows

package mcpsrv

import "os"

// openNoFollow is the Windows counterpart of file_source_unix.go's
// O_NOFOLLOW open. The Go syscall package does not expose an O_NOFOLLOW
// equivalent on Windows, so this is a plain open — resolveFileInput's
// EvalSymlinks + withinRoots check still runs before this, but the
// symlink-swap window this function closes on Unix is left open on this
// platform. See README's filesystem-access section: the trust model already
// assumes the calling agent has its own filesystem access, so this is
// defense in depth rather than the sole guarantee either way.
func openNoFollow(resolved string) (*os.File, error) {
	return os.Open(resolved)
}
