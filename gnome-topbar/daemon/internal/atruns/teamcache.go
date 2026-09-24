package atruns

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

const teamCacheSchema = 1

// TeamCache keeps the team's shared run records on disk, keyed by note
// permalink, so the Team view survives restarts and a pull reads only the
// notes it has not seen. Basic Memory's listing carries no per-note change
// marker, so a republished note (a run that gained a later outcome) is picked
// up only by a full pull: one every FullEvery, or a forced one.
type TeamCache struct {
	Client    *bm.Client
	Project   string
	Path      string
	Interval  time.Duration
	FullEvery time.Duration
	Now       func() time.Time

	// mu serialises pulls and guards file: the hourly tick and the page's
	// refresh button can both start one.
	mu     sync.Mutex
	loaded bool
	file   teamCacheFile
}

type teamCacheFile struct {
	Schema   int                   `json:"schema"`
	Project  string                `json:"project"`
	PulledAt time.Time             `json:"pulled_at"`
	FullAt   time.Time             `json:"full_at"`
	Notes    map[string]cachedNote `json:"notes"`
}

// cachedNote is one at_run note's parsed records. Bad marks a note that did
// not parse; it is kept so hourly pulls do not re-read it, and a full pull
// tries it again.
type cachedNote struct {
	Lines    []scorecard.RunLine     `json:"lines,omitempty"`
	Outcomes []scorecard.OutcomeLine `json:"outcomes,omitempty"`
	Bad      bool                    `json:"bad,omitempty"`
}

// Load reads the cache file and returns its data, the time of its last
// successful pull, and how many cached notes did not parse. It never calls
// Basic Memory. A missing, unreadable, other-schema or other-project file
// yields an empty cache.
func (c *TeamCache) Load() (Data, time.Time, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	return c.snapshotLocked()
}

// Snapshot returns the cache's current data without touching disk or BM.
func (c *TeamCache) Snapshot() (Data, time.Time, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

// Due reports whether the last successful pull is at least Interval old.
func (c *TeamCache) Due() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	return c.file.PulledAt.IsZero() || c.Now().Sub(c.file.PulledAt) >= c.Interval
}

// Refresh pulls from Basic Memory. It lists every at_run note, reads the ones
// the cache lacks (all of them when full is set or FullEvery has passed),
// drops cached notes that are no longer listed, and saves the result. A
// listing failure leaves the cache and its pull time untouched, so the next
// tick retries; a single note that fails to read keeps its cached copy.
func (c *TeamCache) Refresh(ctx context.Context, full bool) (Data, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	notes, err := c.Client.ListRunNotes(ctx, c.Project)
	if err != nil {
		return Data{}, 0, err
	}
	now := c.Now()
	full = full || c.fullDue(now)
	next := make(map[string]cachedNote, len(notes))
	for _, n := range notes {
		if entry, keep := c.noteEntry(ctx, n.Permalink, full); keep {
			next[n.Permalink] = entry
		}
	}
	c.file.Notes = next
	c.file.PulledAt = now
	if full {
		c.file.FullAt = now
	}
	c.save()
	d, _, skipped := c.snapshotLocked()
	return d, skipped, nil
}

func (c *TeamCache) fullDue(now time.Time) bool {
	return c.file.FullAt.IsZero() || now.Sub(c.file.FullAt) >= c.FullEvery
}

// noteEntry returns a listed note's cache entry: the cached copy when the pull
// is incremental, otherwise a fresh read, falling back to the cached copy if
// that read fails. keep is false for a note whose first read failed: it stays
// out of the cache so the next pull retries it, rather than being remembered
// as unparseable until the next full pull.
func (c *TeamCache) noteEntry(ctx context.Context, permalink string, full bool) (entry cachedNote, keep bool) {
	prev, cached := c.file.Notes[permalink]
	if cached && !full {
		return prev, true
	}
	entry, ok := c.readNote(ctx, permalink)
	if !ok {
		return prev, cached
	}
	return entry, true
}

// readNote reads and parses one note. ok is false when the read itself
// failed, as opposed to the note being unparseable (recorded as Bad).
func (c *TeamCache) readNote(ctx context.Context, permalink string) (cachedNote, bool) {
	body, err := c.Client.ReadRunNote(ctx, c.Project, permalink)
	if err != nil {
		return cachedNote{}, false
	}
	ls, ocs, err := ParseNote(body)
	if err != nil {
		return cachedNote{Bad: true}, true
	}
	return cachedNote{Lines: ls, Outcomes: ocs}, true
}

func (c *TeamCache) loadLocked() {
	if c.loaded {
		return
	}
	c.loaded = true
	c.file = teamCacheFile{Schema: teamCacheSchema, Project: c.Project, Notes: map[string]cachedNote{}}
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return
	}
	var f teamCacheFile
	if json.Unmarshal(b, &f) != nil || !c.owns(f) {
		return
	}
	if f.Notes == nil {
		f.Notes = map[string]cachedNote{}
	}
	c.file = f
}

// owns reports whether a cache file was written by this schema for this
// project; a file for another project would mix two teams' records.
func (c *TeamCache) owns(f teamCacheFile) bool {
	return f.Schema == teamCacheSchema && f.Project == c.Project
}

func (c *TeamCache) snapshotLocked() (Data, time.Time, int) {
	var d Data
	skipped := 0
	for _, n := range c.file.Notes {
		if n.Bad {
			skipped++
			continue
		}
		d.Lines = append(d.Lines, n.Lines...)
		d.Outcomes = append(d.Outcomes, n.Outcomes...)
	}
	d.Present = len(d.Lines) > 0 || len(d.Outcomes) > 0
	return d, c.file.PulledAt, skipped
}

// save writes the cache through a temp file and rename, so a crash mid-write
// never leaves a truncated file for the next Load to discard.
func (c *TeamCache) save() {
	b, err := json.Marshal(c.file)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(c.Path), 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.Path), filepath.Base(c.Path)+".*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return
	}
	if os.Rename(name, c.Path) != nil {
		_ = os.Remove(name)
	}
}
