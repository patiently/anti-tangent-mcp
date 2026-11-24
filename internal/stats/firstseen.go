package stats

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const firstSeenFile = "first-seen.json"

// firstSeen records when each cohort model was first seen. It has its own
// file because every server process rewrites state.json whole from memory on
// each event, which would clobber a field another process just added.
type firstSeen struct {
	Seeded bool                 `json:"seeded"`
	Models map[string]time.Time `json:"models"`
}

var errFirstSeenParse = errors.New("first-seen.json does not parse")

// readFirstSeen returns a missing file as unseeded with no error, and a
// corrupt one as unseeded with an error wrapping errFirstSeenParse. Any other
// read error is returned as is.
func readFirstSeen(dir string) (firstSeen, error) {
	fs := firstSeen{Models: map[string]time.Time{}}
	b, err := os.ReadFile(filepath.Join(dir, firstSeenFile))
	if errors.Is(err, os.ErrNotExist) {
		return fs, nil
	}
	if err != nil {
		return fs, err
	}
	if err := json.Unmarshal(b, &fs); err != nil {
		return firstSeen{Models: map[string]time.Time{}}, errors.Join(errFirstSeenParse, err)
	}
	if fs.Models == nil {
		fs.Models = map[string]time.Time{}
	}
	return fs, nil
}

// mergeFirstSeen combines two views so concurrent writers converge: a zero
// time (seeded, never new) beats any stamp, otherwise the earlier stamp wins.
func mergeFirstSeen(a, b firstSeen) firstSeen {
	out := firstSeen{Seeded: a.Seeded || b.Seeded, Models: map[string]time.Time{}}
	for _, src := range []map[string]time.Time{a.Models, b.Models} {
		for m, t := range src {
			prev, ok := out.Models[m]
			switch {
			case !ok:
				out.Models[m] = t
			case prev.IsZero() || t.IsZero():
				out.Models[m] = time.Time{}
			case t.Before(prev):
				out.Models[m] = t
			}
		}
	}
	return out
}

// updateFirstSeen seeds or stamps models and persists the result, returning a
// copy the caller may use without the lock. A file that exists but cannot be
// read is left untouched and yields nil, so the caller publishes no revisit
// data for that call; a file that fails to parse is reseeded. r.mu orders
// calls in this process; persistFirstSeen orders the write against other
// processes sharing the directory. The first read here is unlocked and may be
// stale, which is harmless: the locked reread merges in whatever another
// process wrote since.
func (r *Recorder) updateFirstSeen(models []string, now time.Time) map[string]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, err := readFirstSeen(r.dir)
	if err != nil {
		if !errors.Is(err, errFirstSeenParse) {
			r.logger.Warn("stats first_seen read failed", "err", err)
			return nil
		}
		r.logger.Warn("stats first_seen unreadable, reseeding", "err", err)
	}
	next, changed := addModels(cur, models, now)
	if changed {
		next = r.persistFirstSeen(next)
	}
	out := make(map[string]time.Time, len(next.Models))
	for m, t := range next.Models {
		out[m] = t
	}
	return out
}

// addModels returns a copy of cur with every missing model added: at the zero
// time while unseeded, else at now. now must be the same instant the caller
// passes to Revisit, which ignores first-seen times after it; a later clock
// reading would hide a model's new_model signal on the call that stamps it.
// now is read before the lock is taken, so a call clocked earlier can run
// after one clocked later; a stamp after now is therefore lowered to now, which
// keeps the earliest-wins merge convergent. changed reports whether a write is
// due.
func addModels(cur firstSeen, models []string, now time.Time) (firstSeen, bool) {
	next := mergeFirstSeen(cur, firstSeen{})
	changed := !next.Seeded
	stamp := time.Time{}
	if next.Seeded {
		stamp = now
	}
	for _, m := range models {
		t, ok := next.Models[m]
		if !ok || (next.Seeded && !t.IsZero() && t.After(now)) {
			next.Models[m] = stamp
			changed = true
		}
	}
	next.Seeded = true
	return next, changed
}

// persistFirstSeen merges next with whatever is on disk, writes the result and
// returns it, so the caller's signals use the merged view even when the write
// fails. The reread, merge and rename run under first-seen.json.lock, so this
// write keeps every entry another process wrote before it. If the lock cannot
// be taken in time the write goes ahead unlocked: that write can drop an entry
// a concurrent writer added, and a dropped seeded model would be stamped and
// reported as new on a later call. Each call logs at most one warning, for a
// failed write or else for a missed lock; the next call retries.
func (r *Recorder) persistFirstSeen(next firstSeen) firstSeen {
	unlock, lockErr := acquireDirLock(filepath.Join(r.dir, firstSeenFile+".lock"), r.firstSeenLockWait, firstSeenLockStale, r.firstSeenHook)
	if lockErr == nil {
		defer unlock()
	}
	if disk, err := readFirstSeen(r.dir); err == nil {
		next = mergeFirstSeen(next, disk)
	}
	if r.firstSeenHook != nil {
		r.firstSeenHook("reread")
	}
	b, err := json.Marshal(next)
	if err == nil {
		err = writeFileAtomic(filepath.Join(r.dir, firstSeenFile), b, 0o644)
	}
	switch {
	case err != nil:
		r.logger.Warn("stats first_seen write failed", "err", err, "lock_err", lockErr)
	case lockErr != nil:
		r.logger.Warn("stats first_seen lock unavailable, wrote unlocked", "err", lockErr)
	}
	return next
}
