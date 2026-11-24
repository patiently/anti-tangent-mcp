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
// read is left untouched and yields nil, so the caller computes no new_model
// signal for that call; a file that fails to parse is reseeded. r.mu orders calls in this process
// only; across processes the write is last-rename-wins, so a model another
// process added in the same instant can be lost from disk and is re-stamped on
// the next call that sees it, delaying its new_model signal by at most one
// call. The merge keeps a seeded zero over any stamp, so that race can delay a
// new_model signal but never invent one. Behaviour across processes is
// therefore convergent, not locked.
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
// changed reports whether a write is due.
func addModels(cur firstSeen, models []string, now time.Time) (firstSeen, bool) {
	next := mergeFirstSeen(cur, firstSeen{})
	changed := !next.Seeded
	stamp := time.Time{}
	if next.Seeded {
		stamp = now
	}
	for _, m := range models {
		if _, ok := next.Models[m]; !ok {
			next.Models[m] = stamp
			changed = true
		}
	}
	next.Seeded = true
	return next, changed
}

// persistFirstSeen merges next with whatever is on disk, writes the result and
// returns it, so the caller's signals use the merged view even when the write
// fails. A failure is logged once; the next call retries.
func (r *Recorder) persistFirstSeen(next firstSeen) firstSeen {
	if disk, err := readFirstSeen(r.dir); err == nil {
		next = mergeFirstSeen(next, disk)
	}
	b, err := json.Marshal(next)
	if err == nil {
		err = writeFileAtomic(filepath.Join(r.dir, firstSeenFile), b, 0o644)
	}
	if err != nil {
		r.logger.Warn("stats first_seen write failed", "err", err)
	}
	return next
}
