// Package planrun tracks one execution of a multi-task implementation plan:
// the plan-level verdict from validate_plan, and one row per task carrying
// the anti-tangent verdict and the CodeScene result.
//
// State is in memory with TTL eviction, mirroring internal/session — a plan
// run and the sessions belonging to it expire together. Durable persistence
// is optional and lives in ledger.go.
package planrun

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

// CodeScene adoption states recorded per task row.
const (
	StateRan     = "ran"
	StateSkipped = "skipped"
	StateMissing = "missing"
)

// TaskRow is one task's outcome within a plan run.
type TaskRow struct {
	SessionID      string            `json:"-"`
	Index          int               `json:"index"`
	TaskTitle      string            `json:"task_title"`
	PreVerdict     string            `json:"pre_verdict"`
	Checkpoints    int               `json:"checkpoints"`
	PostVerdict    string            `json:"post_verdict,omitempty"`
	Severity       map[string]int    `json:"severity,omitempty"`
	SubmissionOnly bool              `json:"submission_defect_only,omitempty"`
	Codescene      *codescene.Digest `json:"codescene,omitempty"`
	CodesceneState string            `json:"codescene_state,omitempty"`
	CompletedAt    time.Time         `json:"completed_at,omitempty"`
}

// Run is one plan execution.
type Run struct {
	ID           string    `json:"plan_run_id"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccessed time.Time `json:"-"`
	PlanVerdict  string    `json:"plan_verdict"`
	PlanQuality  string    `json:"plan_quality"`
	// TaskCount is how many tasks the validated plan contained, so the report
	// can tell a failed task from one that was never dispatched.
	TaskCount int       `json:"task_count"`
	Rows      []TaskRow `json:"rows"`
}

// Store holds plan runs in memory.
type Store struct {
	mu   sync.Mutex
	runs map[string]*Run
	ttl  time.Duration
}

func NewStore(ttl time.Duration) *Store {
	return &Store{runs: map[string]*Run{}, ttl: ttl}
}

func (s *Store) TTL() time.Duration { return s.ttl }

// newID returns "pr_" plus 12 lowercase hex characters.
func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is not recoverable and not worth propagating
		// through every call site; a time-derived fallback keeps the server up.
		return "pr_" + hex.EncodeToString([]byte(time.Now().UTC().Format("150405")))[:12]
	}
	return "pr_" + hex.EncodeToString(b[:])
}

func (s *Store) Create(planVerdict, planQuality string, taskCount int) *Run {
	now := time.Now()
	r := &Run{
		ID:           newID(),
		CreatedAt:    now,
		LastAccessed: now,
		PlanVerdict:  planVerdict,
		PlanQuality:  planQuality,
		TaskCount:    taskCount,
	}
	s.mu.Lock()
	s.runs[r.ID] = r
	s.mu.Unlock()
	return r
}

func (s *Store) Get(id string) (*Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, false
	}
	r.LastAccessed = time.Now()
	return r, true
}

// Snapshot returns a copy of the run with its rows copied, safe to read after
// the lock is released. Use it for any read that walks Rows; Get returns the
// live run and is for callers that only need identity or metadata.
//
// Rows are deep-copied, not just the slice: TaskRow.Codescene (pointer) and
// TaskRow.Severity (map) would otherwise still alias the live row's values.
// Every current mutation site replaces both wholesale (UpdateRow's mutate
// closures assign row.Severity = newMap / row.Codescene = newDigest rather
// than writing into the existing map/struct), so aliasing is harmless today —
// but row.Checkpoints++ three lines away in UpdateRow establishes an
// in-place-mutation pattern too, and nothing enforces "replace only" for the
// other fields. Deep-copying here removes the dependency on that convention
// holding forever.
func (s *Store) Snapshot(id string) (*Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, false
	}
	r.LastAccessed = time.Now()
	cp := *r
	cp.Rows = make([]TaskRow, len(r.Rows))
	for i, row := range r.Rows {
		if row.Severity != nil {
			sevCopy := make(map[string]int, len(row.Severity))
			for k, v := range row.Severity {
				sevCopy[k] = v
			}
			row.Severity = sevCopy
		}
		if row.Codescene != nil {
			d := *row.Codescene
			if d.Verdicts != nil {
				v := *d.Verdicts
				d.Verdicts = &v
			}
			if d.CategoryCounts != nil {
				ccCopy := make(map[string]int, len(d.CategoryCounts))
				for k, v := range d.CategoryCounts {
					ccCopy[k] = v
				}
				d.CategoryCounts = ccCopy
			}
			row.Codescene = &d
		}
		cp.Rows[i] = row
	}
	return &cp, true
}

// AppendRow adds a task row, stamping its Index from the current length.
// Returns false when the run id is unknown or expired.
func (s *Store) AppendRow(runID string, row TaskRow) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	row.Index = len(r.Rows) + 1
	r.Rows = append(r.Rows, row)
	r.LastAccessed = time.Now()
	return true
}

// UpdateRow applies mutate to the row owned by sessionID. Returns false when
// the run or the row is unknown.
func (s *Store) UpdateRow(runID, sessionID string, mutate func(*TaskRow)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	for i := range r.Rows {
		if r.Rows[i].SessionID == sessionID {
			mutate(&r.Rows[i])
			r.LastAccessed = time.Now()
			return true
		}
	}
	return false
}

// EvictExpired drops runs untouched for longer than the TTL.
func (s *Store) EvictExpired(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, r := range s.runs {
		if now.Sub(r.LastAccessed) > s.ttl {
			delete(s.runs, id)
			n++
		}
	}
	return n
}
