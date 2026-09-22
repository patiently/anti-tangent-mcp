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

// ledgerLine is one line of the ledger file: a task row, written each time the
// row changes, or a run header. Header is true for a header line, written once when
// validate_plan mints a run before any task attaches to it, and false for a
// task row. Both kinds carry the run's PlanVerdict, PlanQuality, and
// TaskCount, denormalized so the file can be replayed without a separate
// index; a task row carries the task itself in Row and its run id under
// PlanRunID, while a header carries no row, only its own CreatedAt and its
// run id under HeaderPlanRunID — a distinct key, because a reader that
// matches task rows on plan_run_id must never see a header line as one. A
// reader that prunes only on a task row's completion time finds no such
// stamp on a header line's zero-valued Row, so it keeps header lines instead
// of pruning them.
//
// PRIVACY: unlike events.jsonl and codescene-events.jsonl, which are
// deliberately content-free, this record carries task titles and the header
// carries the plan's task headings. That is why it requires its own opt-in
// (ANTI_TANGENT_PLAN_LEDGER=1) on top of ANTI_TANGENT_STATS_DIR rather than
// inheriting the stats opt-in. Silently changing the privacy posture of every
// operator who already set ANTI_TANGENT_STATS_DIR would be wrong, so this is
// a second, explicit flag — do not fold it into the stats opt-in as a
// "simplification".
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
	// Header marks the line validate_plan writes when it mints a run, before any
	// task is attached. It has no row, so Load must not turn it into one.
	Header bool `json:"header,omitempty"`
	// HeaderPlanRunID carries a header line's run id. It is a separate key
	// from PlanRunID so a reader that matches task rows on plan_run_id (this
	// one, or an older binary sharing the same ledger file) never mistakes a
	// header line for a row.
	HeaderPlanRunID string `json:"header_plan_run_id,omitempty"`
	// CreatedAt is set on header lines only. Prune keys a task row on
	// Row.CompletedAt, or WrittenAt when it has not completed; a header has
	// no row to key on.
	CreatedAt time.Time `json:"created_at,omitzero"`
	// Tasks is set on header lines: the plan's task numbers and headings.
	Tasks []PlanTask `json:"tasks,omitempty"`
	// WrittenAt is set on task-row lines: when the line was appended. Prune
	// keys a row that has not completed on it.
	WrittenAt time.Time `json:"written_at,omitzero"`
}

// ledgerHeaderLine is the on-disk shape of a header. It is marshalled from its
// own type rather than from ledgerLine so the line carries no zero-valued row,
// and its run id field is keyed header_plan_run_id, not plan_run_id, so a
// reader that matches task rows on plan_run_id never mistakes this line for
// one.
type ledgerHeaderLine struct {
	HeaderPlanRunID string     `json:"header_plan_run_id"`
	PlanVerdict     string     `json:"plan_verdict,omitempty"`
	PlanQuality     string     `json:"plan_quality,omitempty"`
	TaskCount       int        `json:"task_count,omitempty"`
	Header          bool       `json:"header"`
	CreatedAt       time.Time  `json:"created_at"`
	Tasks           []PlanTask `json:"tasks,omitempty"`
}

// Ledger appends a task row each time one changes to plan-runs.jsonl, plus
// one header line per run validate_plan mints. A nil *Ledger is a no-op, so
// the disabled path is a single nil check.
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
		WrittenAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return l.appendLine(b)
}

// AppendHeader records a run when validate_plan mints it, so a run that no
// task was ever attached to is still known to Load. It carries the plan's
// task headings, which the report lists for tasks never dispatched.
func (l *Ledger) AppendHeader(run *Run) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	b, err := json.Marshal(ledgerHeaderLine{
		HeaderPlanRunID: run.ID, PlanVerdict: run.PlanVerdict, PlanQuality: run.PlanQuality,
		TaskCount: run.TaskCount, Header: true, CreatedAt: run.CreatedAt.UTC(), Tasks: run.Tasks,
	})
	if err != nil {
		return err
	}
	return l.appendLine(b)
}

func (l *Ledger) appendLine(b []byte) error {
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
	// OpenFile's mode argument only applies at creation, so a file created with
	// a wider mode would otherwise never get tightened by a plain append. Chmod
	// is best-effort and its error is intentionally swallowed: ledger writes
	// are advisory, and a chmod failure must never turn into a failed append.
	_ = f.Chmod(0o600)
	_, err = f.Write(append(b, '\n'))
	return err
}

// ledgerLineMatch resolves the run id ln carries and whether it belongs to
// planRunID. A header line's id lives under HeaderPlanRunID; a task row's
// under PlanRunID — see ledgerLine's doc comment for why the two are kept
// apart.
func ledgerLineMatch(ln ledgerLine, planRunID string) (id string, matches bool) {
	if ln.Header {
		return ln.HeaderPlanRunID, ln.HeaderPlanRunID == planRunID
	}
	return ln.PlanRunID, ln.PlanRunID == planRunID
}

// Load reconstructs a run from the ledger. Returns false when the ledger is
// disabled, unreadable, or holds no line for the id. A run recorded by only
// a header line — one that no task has attached to yet — comes back with ok
// true and Rows empty, not as not-found.
//
// A task's row is written when validate_task_spec attaches it and again
// whenever it changes, and a row's Index is its plan task number, so Load
// keeps the last line seen per Index: the task's current state, whether or
// not it completed. Rows are then sorted by Index.
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
		id, matches := ledgerLineMatch(ln, planRunID)
		if !matches {
			continue
		}
		if run == nil {
			run = &Run{
				ID: id, PlanVerdict: ln.PlanVerdict,
				PlanQuality: ln.PlanQuality, TaskCount: ln.TaskCount,
			}
		}
		if ln.Header {
			if run.CreatedAt.IsZero() {
				run.CreatedAt = ln.CreatedAt
			}
			if run.Tasks == nil {
				run.Tasks = ln.Tasks
			}
			continue
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

// shouldPrune returns true if ln should be discarded during a prune at cutoff.
// A task row is keyed on Row.CompletedAt, or on WrittenAt when it has not
// completed; a header line is keyed on CreatedAt. A line with no timestamp is
// retained.
func shouldPrune(ln ledgerLine, cutoff time.Time) bool {
	stamp := ln.Row.CompletedAt
	switch {
	case ln.Header:
		stamp = ln.CreatedAt
	case stamp.IsZero():
		stamp = ln.WrittenAt
	}
	return !stamp.IsZero() && stamp.Before(cutoff)
}

// Prune rewrites plan-runs.jsonl, keeping only lines at or after cutoff. A
// task row is keyed on Row.CompletedAt, or on WrittenAt when it has not
// completed, and a line with neither timestamp is retained.
//
// A row that has not completed is keyed on WrittenAt. A line carrying neither
// timestamp is always retained, because a zero time means no time was recorded,
// not that the line is infinitely old, and dropping it would be data loss
// indistinguishable from a real timestamp check.
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
		if shouldPrune(ln, cutoff) {
			continue // has a real timestamp and it is stale: drop
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
