package stats

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	defaultFirstSeenLockWait = time.Second
	fileLockPoll             = 10 * time.Millisecond
)

// errLockHeld is what tryLockFile returns when another open file holds the
// lock, as opposed to a failure that waiting will not fix.
var errLockHeld = errors.New("file lock held")

// acquireFileLock takes an exclusive OS advisory lock on path, creating the
// file if it is missing, and returns the function that releases it. The kernel
// drops the lock when its holder exits, so a crashed process never leaves it
// held and there is no staleness to judge. The file is never removed: a
// process that had already opened the old path would lock a different file
// from one that opened the new one. The lock belongs to the open file, so two
// opens in one process exclude each other too. It polls until wait elapses
// (zero means defaultFirstSeenLockWait) and never blocks longer. hook, when
// non-nil, is called with "busy" each time the lock is found held.
func acquireFileLock(path string, wait time.Duration, hook func(string)) (func(), error) {
	if wait <= 0 {
		wait = defaultFirstSeenLockWait
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := tryLockFile(f)
		if err == nil {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, errLockHeld) {
			_ = f.Close()
			return nil, err
		}
		if hook != nil {
			hook("busy")
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s not acquired within %s: %w", path, wait, err)
		}
		time.Sleep(fileLockPoll)
	}
}
