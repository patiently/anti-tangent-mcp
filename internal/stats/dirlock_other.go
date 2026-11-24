//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package stats

import (
	"errors"
	"os"
)

// Without a file lock first-seen.json is never written here; every call still
// computes from what it read.
func tryLockFile(*os.File) error { return errors.ErrUnsupported }

func unlockFile(*os.File) error { return nil }
