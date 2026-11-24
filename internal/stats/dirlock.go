package stats

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
)

const (
	defaultFirstSeenLockWait = time.Second
	// firstSeenLockStale is how old a lock file must be before it is taken to
	// be left by a crashed process and removed. A holder keeps it only for one
	// small read and atomic write, far below this.
	firstSeenLockStale = 10 * time.Second
	dirLockPoll        = 10 * time.Millisecond
)

// acquireDirLock takes a cross-process lock by creating path with O_EXCL,
// which is atomic on every platform the server ships for, and returns the
// function that releases it. It polls until wait elapses (zero means
// defaultFirstSeenLockWait) and never blocks longer. A lock file older than
// stale is removed and retaken; two waiters can both judge the same file
// stale, so in that window two holders are possible, which costs only the
// unlocked behaviour the lock otherwise prevents. hook, when non-nil, is
// called with "busy" each time the lock is found held.
func acquireDirLock(path string, wait, stale time.Duration, hook func(string)) (func(), error) {
	if wait <= 0 {
		wait = defaultFirstSeenLockWait
	}
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		// Windows reports a lock file whose removal is still pending as
		// access denied rather than as existing, so there both count as held.
		// Elsewhere access denied means the directory is unwritable, which
		// waiting will not fix.
		held := errors.Is(err, os.ErrExist) || (runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission))
		if !held {
			return nil, err
		}
		if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) > stale {
			if os.Remove(path) == nil {
				continue
			}
		}
		if hook != nil {
			hook("busy")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock %s not acquired within %s: %w", path, wait, err)
		}
		time.Sleep(dirLockPoll)
	}
}
