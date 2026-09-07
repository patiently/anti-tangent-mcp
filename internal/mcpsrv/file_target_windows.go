//go:build windows

package mcpsrv

import "os"

// openWriteNoFollow on Windows has no O_NOFOLLOW equivalent in Go's syscall
// package, exactly as the read path's openNoFollow does not. The
// final-component symlink-swap window is therefore NOT closed on Windows;
// ANTI_TANGENT_PLAN_ROOTS is advisory against caller mistakes there rather
// than enforced against a race. Documented in the README alongside the
// identical read-path caveat.
func openWriteNoFollow(resolved string, overwrite bool) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	return os.OpenFile(resolved, flags, 0o644)
}
