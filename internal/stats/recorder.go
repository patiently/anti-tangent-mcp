package stats

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// Options configures a Recorder. Constructed in main from config when
// ANTI_TANGENT_STATS_DIR is set.
type Options struct {
	Dir              string
	Reviewer         providers.Reviewer // nil => summary step skipped (rollup still written)
	Model            string
	MaxTokens        int
	RequestTimeout   time.Duration
	SummaryInterval  time.Duration
	SummaryThreshold int
	RetentionDays    int
	Logger           *slog.Logger
}

// Recorder appends counts-only events and launches single-flight async
// compaction when the trigger is due. All methods are nil-safe: a nil *Recorder
// (stats disabled) is a no-op, so the disabled call path is a single nil check.
type Recorder struct {
	dir           string
	mu            sync.Mutex // guards events.jsonl I/O + state
	state         State
	interval      time.Duration
	threshold     int
	retentionDays int
	compactor     *Compactor
	running       atomic.Bool
	clock         func() time.Time
	logger        *slog.Logger
	// runCompaction is launched (async, single-flight) when due. Defaults to
	// r.compact; tests override it.
	runCompaction func(now time.Time)
}

// New creates the stats dir (if needed), verifies it is writable, and loads or
// initializes state.json (fresh salt + LastSummaryAt=now on first enable).
// Returns an error only when the dir is unusable; the caller logs a warning and
// runs with stats disabled.
func New(opts Options) (*Recorder, error) {
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("stats dir: %w", err)
	}
	probe := filepath.Join(opts.Dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return nil, fmt.Errorf("stats dir not writable: %w", err)
	}
	_ = os.Remove(probe)

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clock := func() time.Time { return time.Now().UTC() }

	st := loadState(opts.Dir)
	if st.LastSummaryAt.IsZero() {
		st.LastSummaryAt = clock()
		_ = saveState(opts.Dir, st)
	}

	r := &Recorder{
		dir:           opts.Dir,
		state:         st,
		interval:      opts.SummaryInterval,
		threshold:     opts.SummaryThreshold,
		retentionDays: opts.RetentionDays,
		compactor: &Compactor{
			dir:       opts.Dir,
			reviewer:  opts.Reviewer,
			model:     opts.Model,
			maxTokens: opts.MaxTokens,
			timeout:   opts.RequestTimeout,
			logger:    logger,
		},
		clock:  clock,
		logger: logger,
	}
	r.runCompaction = r.compact
	return r, nil
}

// HashSession returns a salted, non-reversible digest of a session id, or "" for
// a nil recorder / empty id. The raw id is never written to disk.
func (r *Recorder) HashSession(id string) string {
	if r == nil || id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(r.state.Salt + ":" + id))
	return hex.EncodeToString(sum[:8])
}

// Record appends one event (best-effort) and, if a compaction is now due and
// none is in flight, launches one asynchronously. Safe on a nil Recorder.
func (r *Recorder) Record(ev Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if err := appendJSONL(r.dir, eventsFile, ev); err != nil {
		r.logger.Warn("stats append failed", "err", err)
	} else {
		r.state.EventsSinceSummary++
		_ = saveState(r.dir, r.state)
	}
	st := r.state
	r.mu.Unlock()

	now := r.clock()
	if due(now, st, r.interval, r.threshold) && r.running.CompareAndSwap(false, true) {
		go func() {
			defer r.running.Store(false)
			r.runCompaction(now)
		}()
	}
}

// compact snapshots events under the lock, runs the Compactor (LLM call happens
// without the lock held), then prunes by retention and stamps state.
func (r *Recorder) compact(now time.Time) {
	r.mu.Lock()
	events, err := readEvents(r.dir)
	r.mu.Unlock()
	if err != nil {
		r.logger.Warn("stats read events failed", "err", err)
		return
	}

	r.compactor.Compact(now, events)

	cutoff := now.AddDate(0, 0, -r.retentionDays)
	r.mu.Lock()
	if err := pruneEvents(r.dir, cutoff); err != nil {
		r.logger.Warn("stats prune failed", "err", err)
	}
	r.state.LastSummaryAt = now
	r.state.EventsSinceSummary = 0
	_ = saveState(r.dir, r.state)
	r.mu.Unlock()
}

func readEvents(dir string) ([]Event, error) {
	return readJSONL[Event](dir, eventsFile)
}

// pruneEvents rewrites events.jsonl keeping only records at/after cutoff.
func pruneEvents(dir string, cutoff time.Time) error {
	events, err := readEvents(dir)
	if err != nil {
		return err
	}
	kept := events[:0]
	for _, e := range events {
		if !e.Ts.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	return rewriteJSONL(dir, eventsFile, kept)
}
