//go:build windows

package mcpsrv

import "os"

// openWriteNoFollow on Windows has no O_NOFOLLOW equivalent in Go's syscall
// package, exactly as the read path's openNoFollow does not — see
// file_source_windows.go, whose identical caveat is already documented in
// the README's file-path trust-model section for reads. ANTI_TANGENT_PLAN_ROOTS
// is advisory against caller mistakes on this platform rather than enforced
// against a race. The write-path README section covering this same gap
// lands with Task 15; until then, this comment is the caveat's only home.
func openWriteNoFollow(resolved string, overwrite bool) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	return os.OpenFile(resolved, flags, 0o644)
}
