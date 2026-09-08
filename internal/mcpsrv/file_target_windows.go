//go:build windows

package mcpsrv

import (
	"errors"
	"os"
)

// errWriteTargetUnsupported is what code_write returns on Windows instead of
// writing to target_path.
//
// The reason is the leaf. On Unix the write path guarantees that a symlink
// planted at the final component between the containment check and the write
// is refused, never followed — O_EXCL|O_NOFOLLOW on the create path,
// os.Lstat plus a non-following rename(2) on the overwrite path. Windows has
// no equivalent this code can rely on: Go's syscall package exposes no
// O_NOFOLLOW, and the alternatives (CreateFile with
// FILE_FLAG_OPEN_REPARSE_POINT, or deciding what CREATE_NEW and
// MoveFileEx(MOVEFILE_REPLACE_EXISTING) do when a reparse point sits at the
// destination) all hinge on Windows semantics that cannot be exercised from
// this project — nothing here builds or tests on Windows, only goreleaser
// cross-compiles for it. Shipping an untested guard would put a security
// property's weight on code no test has ever run.
//
// So the platform claim is made honest instead of made up. target_path is
// refused, and the refusal names what the caller should do: code_write
// without target_path still generates the code and returns it, which is the
// whole tool minus the context saving.
//
// This is narrower than the read path's accepted gap in file_source_windows.go
// and deliberately so. There, losing the race means an unintended file is
// read into a prompt by an agent that already has filesystem access. Here it
// means a WRITE lands outside ANTI_TANGENT_PLAN_ROOTS, which destroys data
// rather than exposing it.
var errWriteTargetUnsupported = errors.New(
	"code_write cannot write target_path on Windows: this platform has no way for the server to guarantee " +
		"that a reparse point planted at the target's final path component is refused rather than followed, " +
		"so the write could land outside ANTI_TANGENT_PLAN_ROOTS. Omit target_path — code_write still " +
		"generates the file and returns it as `code` for you to write yourself")

func writeTargetSupported() error { return errWriteTargetUnsupported }

// openCreateNoFollow is unreachable on Windows: writeTargetSupported refuses
// before resolveWriteTarget reaches any open. It returns the same refusal
// rather than a plain os.OpenFile so that a future caller which skips the
// gate cannot end up opening a leaf this platform cannot guard.
func openCreateNoFollow(string) (*os.File, error) { return nil, errWriteTargetUnsupported }
