package planrun

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const ledgerFile = "plan-runs.jsonl"

// ledgerLine is one completed task row, denormalized with its run's header so
// the file can be replayed without a separate index.
//
// PRIVACY: unlike events.jsonl and codescene-events.jsonl, which are
// deliberately content-free, this record carries TaskTitle. That is why it
// requires its own opt-in (ANTI_TANGENT_PLAN_LEDGER=1) on top of
// ANTI_TANGENT_STATS_DIR rather than inheriting the stats opt-in. Silently
// changing the privacy posture of every operator who already set
// ANTI_TANGENT_STATS_DIR would be wrong, so this is a second, explicit flag —
// do not fold it into the stats opt-in as a "simplification".
//
// Row embeds TaskRow, whose SessionID field carries `json:"-"`: that tag is
// deliberate (SessionID is an in-process join key, not for disk) and must be
// preserved, not overridden, when this type is marshalled.
type ledgerLine struct {
	PlanRunID   string  `json:"plan_run_id"`
	PlanVerdict string  `json:"plan_verdict,omitempty"`
	PlanQuality string  `json:"plan_quality,omitempty"`
	TaskCount   int     `json:"task_count,omitempty"`
	Row         TaskRow `json:"row"`
}

// Ledger appends completed task rows to plan-runs.jsonl. A nil *Ledger is a
// no-op, so the disabled path is a single nil check.
//
// mu serializes Append against Prune. Append does a raw O_APPEND write;
// Prune reads the whole file, filters, and atomically replaces it via a
// temp-file rename. Without a shared lock, an Append landing between Prune's
// read and its rename would be silently discarded: Prune's rewrite is built
// from a snapshot taken before the Append happened, and the rename replaces
// the file wholesale, wiping out anything written after the snapshot. The
// mutex removes the interleaving: a concurrent Append either completes
// before Prune starts reading, or queues behind Prune's Unlock and lands in
// the freshly-pruned file. Load is intentionally NOT part of this lock — it
// already tolerates a torn trailing line for the pre-existing unsynchronized
// Append race, and serializing a read-only reconstruction against Prune
// isn't needed for correctness: Prune's rename is atomic at the OS level, so
// Load always sees either the pre- or post-prune file, never a torn one.
type Ledger struct {
	Dir string
	mu  sync.Mutex

	// afterPruneRead is a test-only seam, nil in production. If set, Prune
	// calls it — while mu is still held — after it has read and filtered the
	// ledger's rows but before it performs the atomic rewrite. It lets a test
	// deterministically land a concurrent Append mid-Prune (the exact window
	// mu exists to close) instead of relying on goroutine-scheduling luck.
	afterPruneRead func()

	// afterAppendLock is a test-only seam, nil in production. If set, Append
	// calls it immediately after acquiring mu, before touching the file. It
	// lets a test observe precisely when a concurrent Append has passed the
	// mutex gate, so a "must still be blocked" assertion doesn't depend on
	// outracing the rest of Prune's I/O (unreliable — see the comment on
	// TestLedger_PruneConcurrentAppendNotLost for how a first version of that
	// test, which timed the race instead of gating on this hook, passed 50/50
	// runs even with the mutex removed).
	afterAppendLock func()
}

func (l *Ledger) Append(run *Run, row TaskRow) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	b, err := json.Marshal(ledgerLine{
		PlanRunID: run.ID, PlanVerdict: run.PlanVerdict,
		PlanQuality: run.PlanQuality, TaskCount: run.TaskCount, Row: row,
	})
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.afterAppendLock != nil {
		l.afterAppendLock()
	}
	f, err := os.OpenFile(filepath.Join(l.Dir, ledgerFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Load reconstructs a run from the ledger. Returns false when the ledger is
// disabled, unreadable, or holds no rows for the id.
//
// validate_completion may legitimately run more than once for the same
// session — that is exactly the submission-defect re-submit loop
// (isSubmissionDefectOnly / resubmitNextAction in mcpsrv), and Append is
// deliberately dumb and best-effort, so a resubmitted task writes one ledger
// line per completion call, all sharing the same Row.Index (UpdateRow's
// mutate closure never touches Index — it is stable across resubmissions).
// Load, not Append, is where that gets collapsed back down: it keeps the
// *last* line seen per Index — the final outcome, which is the one that
// actually stands — rather than suppressing earlier writes at append time,
// so a crash between resubmissions never loses the earlier line. Rows are
// then sorted by Index so a ledger-recovered run's ordering matches the live
// store's dispatch order, regardless of the order tasks happened to
// *complete* in.
func (l *Ledger) Load(planRunID string) (*Run, bool) {
	if l == nil || l.Dir == "" {
		return nil, false
	}
	f, err := os.Open(filepath.Join(l.Dir, ledgerFile))
	if err != nil {
		return nil, false
	}
	defer f.Close()

	var run *Run
	byIndex := map[int]TaskRow{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ln ledgerLine
		if err := json.Unmarshal(sc.Bytes(), &ln); err != nil {
			continue // tolerate a torn trailing line
		}
		if ln.PlanRunID != planRunID {
			continue
		}
		if run == nil {
			run = &Run{
				ID: ln.PlanRunID, PlanVerdict: ln.PlanVerdict,
				PlanQuality: ln.PlanQuality, TaskCount: ln.TaskCount,
			}
		}
		byIndex[ln.Row.Index] = ln.Row // last-seen wins: a resubmission overwrites its own index
	}
	if run == nil {
		return nil, false
	}
	run.Rows = make([]TaskRow, 0, len(byIndex))
	for _, row := range byIndex {
		run.Rows = append(run.Rows, row)
	}
	sort.Slice(run.Rows, func(i, j int) bool { return run.Rows[i].Index < run.Rows[j].Index })
	return run, true
}

// Prune rewrites plan-runs.jsonl, keeping only rows at or after cutoff. It
// keys on Row.CompletedAt, since a ledger line carries no timestamp of its
// own outside the embedded row (see ledgerLine).
//
// A row whose CompletedAt is the zero value is always retained, never
// treated as "at or after" nor "before" cutoff by a literal comparison. Zero
// means "no completion time was recorded" — some code path wrote the row
// without stamping CompletedAt — not "this row is infinitely old". A naive
// `CompletedAt.Before(cutoff)` would evaluate true for the zero value (since
// the zero time predates any real cutoff) and silently drop rows we have no
// actual evidence are old. Keeping them is the conservative choice: at worst
// a timestamp-less row lingers past retention; the alternative is a
// data-loss bug indistinguishable from a real timestamp check. Not reachable
// via today's single call site (Append always follows a CompletedAt stamp);
// kept for forward compatibility.
//
// A torn trailing line (unparseable JSON, e.g. a partial write from a killed
// process) is skipped exactly like Load already does, and is never written
// back — a corrupt fragment must not survive a prune by accident.
//
// The rewrite is atomic (temp file in the same directory, then rename), so a
// crash mid-prune leaves either the pre- or post-prune file intact, never a
// truncated one. A missing plan-runs.jsonl is treated as zero rows: Prune
// returns nil without creating a file, matching the "empty Dir is a no-op"
// spirit — a plan ledger that has never received a row should not gain an
// empty file just because a retention tick fired.
func (l *Ledger) Prune(cutoff time.Time) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	path := filepath.Join(l.Dir, ledgerFile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var kept [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var ln ledgerLine
		if err := json.Unmarshal(line, &ln); err != nil {
			continue // tolerate (drop) a torn trailing line, same as Load
		}
		if !ln.Row.CompletedAt.IsZero() && ln.Row.CompletedAt.Before(cutoff) {
			continue // has a real completion time and it is stale: drop
		}
		kept = append(kept, append([]byte(nil), line...)) // copy: sc.Bytes() is reused by the next Scan
	}
	scanErr := sc.Err()
	closeErr := f.Close()
	if scanErr != nil {
		// A genuine read error (not a parse error on one line) means we only
		// saw a partial file. Rewriting from that would be data loss, not a
		// prune, so bail out and leave the existing file untouched.
		return scanErr
	}
	if closeErr != nil {
		// Mirrors writeFileAtomic's own close-error handling below: don't
		// swallow it just because it happens on the read side too. A close
		// failure on a read-only fd is unlikely to indicate lost data, but
		// propagating it (rather than proceeding to rewrite) keeps the same
		// "don't guess, bail out" posture as the scanErr branch above.
		return closeErr
	}

	if l.afterPruneRead != nil {
		l.afterPruneRead()
	}

	var buf bytes.Buffer
	for _, line := range kept {
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeFileAtomic(path, buf.Bytes(), 0o600)
}

// writeFileAtomic mirrors internal/stats/io.go's helper of the same name.
// Kept as a local copy rather than a shared package: internal/planrun must
// not import internal/stats (see the package doc), and this is a handful of
// lines — not worth a shared dependency edge either package would then own.
func writeFileAtomic(path string, b []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
