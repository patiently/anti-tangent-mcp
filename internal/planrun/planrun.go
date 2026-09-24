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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

// CodeScene adoption states recorded per task row.
const (
	StateRan     = "ran"
	StateSkipped = "skipped"
	StateMissing = "missing"
)

// ToolCall is shared with the scorecard so a row's call log is written to
// runs.jsonl without conversion.
type ToolCall = scorecard.ToolCall

// maxCallLog bounds a row's call log; a task that checkpoints without limit
// must not grow its row, and every ledger line that carries it, without limit.
const maxCallLog = 32

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
	// Waived is how many findings controller rulings waived on the task's most
	// recent validate_completion.
	Waived int `json:"waived,omitempty"`
	// Escalated is set once any validate_completion on the task escalated.
	Escalated bool `json:"escalated,omitempty"`
	// Attempts is how many validate_task_spec sessions attached to the task.
	// A re-validation updates the task's row instead of adding one.
	Attempts int `json:"attempts,omitempty"`
	// Lite marks a row a lightweight validate_completion created: it has no
	// session and no pre-task verdict.
	Lite bool `json:"lite,omitempty"`
	// Unmatched marks a row that named no plan task, by index or by title. It
	// is numbered after the plan's tasks.
	Unmatched bool `json:"unmatched,omitempty"`
	// Calls logs every anti-tangent call made for this task, oldest first,
	// capped by AppendCall.
	Calls        []ToolCall `json:"calls,omitempty"`
	CallsDropped int        `json:"calls_dropped,omitempty"`
}

// PlanTask is one task of the validated plan: its 1-based position and its
// heading.
type PlanTask struct {
	Index int    `json:"index"`
	Title string `json:"title"`
}

// TaskRef is what a call says about the plan task it belongs to.
type TaskRef struct {
	// Index is the task's 1-based position in the plan, or 0 when not given.
	Index int
	// Title is the task title the caller sent.
	Title string
}

func (ref TaskRef) empty() bool {
	return ref.Index <= 0 && titleKey(ref.Title) == ""
}

var taskNumberPrefixRe = regexp.MustCompile(`^(?i:task)\s+\d+\s*:\s*`)

// titleKey is the form titles are compared in: without a leading "Task N:",
// lowercased, with every whitespace run collapsed to one space.
func titleKey(s string) string {
	s = taskNumberPrefixRe.ReplaceAllString(strings.TrimSpace(s), "")
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
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
	TaskCount int `json:"task_count"`
	// Tasks lists the plan's tasks. A run created from a task count alone has
	// untitled tasks, and then takes new rows in dispatch order.
	Tasks []PlanTask `json:"tasks,omitempty"`
	// Rows holds one row per task, in Index order.
	Rows             []TaskRow         `json:"rows"`
	ConfiguredModels map[string]string `json:"configured_models,omitempty"`
	ServerVersion    string            `json:"server_version,omitempty"`
	PlanCall         *ToolCall         `json:"plan_call,omitempty"`
	// sessions maps every session ever attached to a row to that row's Index,
	// so an implementer that re-validated and carried on with its first
	// session still updates its task.
	sessions map[string]int
}

// Store holds plan runs in memory.
type Store struct {
	mu   sync.Mutex
	runs map[string]*Run
	ttl  time.Duration
}

// RunMeta is what validate_plan knows about the models behind a run.
type RunMeta struct {
	ConfiguredModels map[string]string
	ServerVersion    string
	PlanCall         *ToolCall
}

func NewStore(ttl time.Duration) *Store {
	return &Store{runs: map[string]*Run{}, ttl: ttl}
}

func (s *Store) TTL() time.Duration { return s.ttl }

// SetMeta stores meta on run runID. Returns false when the run is unknown.
func (s *Store) SetMeta(runID string, meta RunMeta) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	r.ConfiguredModels = cloneStringMap(meta.ConfiguredModels)
	r.ServerVersion = meta.ServerVersion
	r.PlanCall = cloneCall(meta.PlanCall)
	return true
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func cloneCall(c *ToolCall) *ToolCall {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

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

// Create mints a run for a plan of taskCount tasks whose headings are not
// known.
func (s *Store) Create(planVerdict, planQuality string, taskCount int) *Run {
	tasks := make([]PlanTask, taskCount)
	for i := range tasks {
		tasks[i].Index = i + 1
	}
	return s.CreateWithTasks(planVerdict, planQuality, tasks)
}

// CreateWithTasks mints a run for the plan's tasks.
func (s *Store) CreateWithTasks(planVerdict, planQuality string, tasks []PlanTask) *Run {
	now := time.Now()
	r := &Run{
		ID:           newID(),
		CreatedAt:    now,
		LastAccessed: now,
		PlanVerdict:  planVerdict,
		PlanQuality:  planQuality,
		TaskCount:    len(tasks),
		Tasks:        append([]PlanTask(nil), tasks...),
		sessions:     map[string]int{},
	}
	s.mu.Lock()
	s.runs[r.ID] = r
	s.mu.Unlock()
	return r
}

// PlanTaskCount returns how many tasks run runID's plan has, and false when
// the run is unknown or expired.
func (s *Store) PlanTaskCount(runID string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return 0, false
	}
	return r.TaskCount, true
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
// Rows are deep-copied via cloneRow: TaskRow.Codescene (pointer) and
// TaskRow.Severity (map) would otherwise still alias the live row's values.
func (s *Store) Snapshot(id string) (*Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, false
	}
	r.LastAccessed = time.Now()
	cp := *r
	cp.Tasks = append([]PlanTask(nil), r.Tasks...)
	cp.sessions = nil
	cp.Rows = make([]TaskRow, len(r.Rows))
	for i, row := range r.Rows {
		cp.Rows[i] = cloneRow(row)
	}
	cp.ConfiguredModels = cloneStringMap(r.ConfiguredModels)
	cp.PlanCall = cloneCall(r.PlanCall)
	return &cp, true
}

// cloneRow deep-copies row: Severity and Codescene would otherwise still alias
// the live row's map and digest.
func cloneRow(row TaskRow) TaskRow {
	row.Severity = cloneIntMap(row.Severity)
	row.Codescene = cloneDigest(row.Codescene)
	row.Calls = append([]ToolCall(nil), row.Calls...)
	return row
}

// AppendCall records one anti-tangent call made for this task.
func (row *TaskRow) AppendCall(c ToolCall) {
	row.Calls = append(row.Calls, c)
	if over := len(row.Calls) - maxCallLog; over > 0 {
		row.Calls = append([]ToolCall(nil), row.Calls[over:]...)
		row.CallsDropped += over
	}
}

// cloneIntMap returns a copy of m, or nil when m is nil.
func cloneIntMap(m map[string]int) map[string]int {
	if m == nil {
		return nil
	}
	cp := make(map[string]int, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// cloneDigest returns a deep copy of d — including its Verdicts pointer and
// CategoryCounts map — or nil when d is nil.
func cloneDigest(d *codescene.Digest) *codescene.Digest {
	if d == nil {
		return nil
	}
	cp := *d
	if cp.Verdicts != nil {
		v := *cp.Verdicts
		cp.Verdicts = &v
	}
	cp.CategoryCounts = cloneIntMap(cp.CategoryCounts)
	return &cp
}

// Latest returns the most recently created run that has not been idle past
// the TTL. It does not refresh LastAccessed: naming a run in an advisory must
// not keep a stale run alive. Safe on a nil Store.
func (s *Store) Latest() (*Run, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var latest *Run
	for _, r := range s.runs {
		if now.Sub(r.LastAccessed) > s.ttl {
			continue
		}
		if latest == nil || r.CreatedAt.After(latest.CreatedAt) {
			latest = r
		}
	}
	return latest, latest != nil
}

// Attach records a validate_task_spec session against the task ref names
// (see resolve), adding the task's row on its first attach. A later attach is
// a re-validation: the row takes the new pre-verdict and one more attempt, and
// keeps its earlier sessions. Returns a copy of the row, and false when the
// run is unknown or expired or ref names nothing.
func (s *Store) Attach(runID, sessionID string, ref TaskRef, preVerdict string) (TaskRow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok || ref.empty() {
		return TaskRow{}, false
	}
	pos := r.rowFor(ref, false)
	row := &r.Rows[pos]
	row.SessionID = sessionID
	row.PreVerdict = preVerdict
	row.Attempts++
	row.Lite = false
	if r.sessions == nil {
		r.sessions = map[string]int{}
	}
	r.sessions[sessionID] = row.Index
	r.LastAccessed = time.Now()
	return cloneRow(*row), true
}

// UpdateRow applies mutate to the row sessionID is attached to and returns a
// copy of the result. mutate must not change Index. Returns false when the
// run or the session is unknown.
func (s *Store) UpdateRow(runID, sessionID string, mutate func(*TaskRow)) (TaskRow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return TaskRow{}, false
	}
	index, ok := r.sessions[sessionID]
	if !ok {
		return TaskRow{}, false
	}
	pos := r.rowPos(index)
	if pos < 0 {
		return TaskRow{}, false
	}
	mutate(&r.Rows[pos])
	r.LastAccessed = time.Now()
	return cloneRow(r.Rows[pos]), true
}

// UpsertLite applies mutate to the row ref names, first adding a Lite row
// when the task has none: a lightweight validate_completion has no session to
// attach. mutate must not change Index. Returns a copy of the row, and false
// when the run is unknown or expired or ref names nothing.
func (s *Store) UpsertLite(runID string, ref TaskRef, mutate func(*TaskRow)) (TaskRow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok || ref.empty() {
		return TaskRow{}, false
	}
	pos := r.rowFor(ref, true)
	mutate(&r.Rows[pos])
	r.LastAccessed = time.Now()
	return cloneRow(r.Rows[pos]), true
}

// rowFor returns the position of the row ref names, adding the row when the
// task has none. A new row takes the plan's heading when ref carries no title.
func (r *Run) rowFor(ref TaskRef, lite bool) int {
	pos, index, unmatched := r.resolve(ref)
	if pos >= 0 {
		return pos
	}
	title := ref.Title
	if strings.TrimSpace(title) == "" {
		title = r.taskTitle(index)
	}
	return r.insertRow(TaskRow{
		Index: index, TaskTitle: title, Unmatched: unmatched, Lite: lite,
		CodesceneState: StateMissing,
	})
}

// resolve finds the row ref names: by ref.Index when it is one of the plan's
// tasks; else by the one plan heading matching ref.Title; else by an existing
// row with that title. pos is the row's position in Rows, or -1 when the task
// has no row yet, and then index and unmatched describe the row to add. A
// plan whose headings are unknown takes new rows in dispatch order.
func (r *Run) resolve(ref TaskRef) (pos, index int, unmatched bool) {
	key := titleKey(ref.Title)
	switch {
	case ref.Index >= 1 && ref.Index <= len(r.Tasks):
		index = ref.Index
	case key != "":
		index = r.taskByTitle(key)
	}
	if index > 0 {
		return r.rowPos(index), index, false
	}
	if pos, row, ok := r.rowByTitle(key); ok {
		return pos, row.Index, row.Unmatched
	}
	if next := r.nextFreeUntitledTask(); next > 0 {
		return -1, next, false
	}
	return -1, r.nextUnmatchedIndex(), true
}

// rowByTitle returns the position and value of the existing row whose title
// matches key, or ok false when key is empty or no row matches.
func (r *Run) rowByTitle(key string) (pos int, row TaskRow, ok bool) {
	if key == "" {
		return 0, TaskRow{}, false
	}
	for i, row := range r.Rows {
		if titleKey(row.TaskTitle) == key {
			return i, row, true
		}
	}
	return 0, TaskRow{}, false
}

// nextFreeUntitledTask returns nextFreeTask's result, but only for a plan
// whose headings are unknown — a titled plan's tasks are matched by heading,
// not by dispatch order.
func (r *Run) nextFreeUntitledTask() int {
	if r.titled() {
		return 0
	}
	return r.nextFreeTask()
}

// taskByTitle returns the Index of the one plan task whose heading matches
// key, or 0 when none or several do.
func (r *Run) taskByTitle(key string) int {
	found := 0
	for _, t := range r.Tasks {
		if titleKey(t.Title) != key {
			continue
		}
		if found != 0 {
			return 0
		}
		found = t.Index
	}
	return found
}

func (r *Run) titled() bool {
	for _, t := range r.Tasks {
		if strings.TrimSpace(t.Title) != "" {
			return true
		}
	}
	return false
}

// taskTitle returns the heading of plan task index, or "" when it has none.
func (r *Run) taskTitle(index int) string {
	for _, t := range r.Tasks {
		if t.Index == index {
			return t.Title
		}
	}
	return ""
}

// nextFreeTask returns the lowest plan task Index that has no row, or 0.
func (r *Run) nextFreeTask() int {
	for _, t := range r.Tasks {
		if r.rowPos(t.Index) < 0 {
			return t.Index
		}
	}
	return 0
}

// nextUnmatchedIndex numbers a row that names no plan task after every plan
// task and every existing row.
func (r *Run) nextUnmatchedIndex() int {
	n := len(r.Tasks)
	for _, row := range r.Rows {
		if row.Index > n {
			n = row.Index
		}
	}
	return n + 1
}

// rowPos returns the position in Rows of the row numbered index, or -1.
func (r *Run) rowPos(index int) int {
	for i := range r.Rows {
		if r.Rows[i].Index == index {
			return i
		}
	}
	return -1
}

// insertRow adds row to Rows in Index order and returns its position.
func (r *Run) insertRow(row TaskRow) int {
	pos := sort.Search(len(r.Rows), func(i int) bool { return r.Rows[i].Index > row.Index })
	r.Rows = append(r.Rows, TaskRow{})
	copy(r.Rows[pos+1:], r.Rows[pos:])
	r.Rows[pos] = row
	return pos
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
