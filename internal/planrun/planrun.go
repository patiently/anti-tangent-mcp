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
	"github.com/patiently/anti-tangent-mcp/internal/ratedigest"
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
	// Categories counts, per finding category, the findings every
	// validate_completion call on the task returned, summed over the calls.
	Categories map[string]int `json:"categories,omitempty"`
	// LinesAdded and LinesRemoved are the size of the most recent final_diff
	// any validate_completion call on the task sent; zero when none sent one.
	LinesAdded   int `json:"lines_added,omitempty"`
	LinesRemoved int `json:"lines_removed,omitempty"`
	// OverBuildingRuled counts the validate_completion calls on the task whose
	// over_building finding was settled by an answer or a controller ruling
	// instead of by cutting the structure.
	OverBuildingRuled int `json:"over_building_ruled,omitempty"`
	// RateDigest is the counts of the most recent rate_digest a
	// validate_completion call on the task sent, without its eval path.
	RateDigest *ratedigest.Digest `json:"rate_digest,omitempty"`
}

// PlanTask is one task of the validated plan: its 1-based position and its
// heading.
type PlanTask struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	// Files are the paths the task's own Files: section lists. They are kept
	// in memory only, so the ledger header never carries them.
	Files []string `json:"-"`
	// Kind and Rung are the task's **Kind:** and **Rung:** headers as the
	// plan parser read them. In memory only, like Files.
	Kind string `json:"-"`
	Rung string `json:"-"`
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
	// Revision counts the validate_plan rounds that reviewed this run's plan:
	// 1 when the run is minted, one more for every later round that names it.
	Revision int `json:"revision,omitempty"`
	// PlanKind is the plan's **Plan kind:** header and BoundaryRules the
	// boundary_rules the latest validate_plan round sent. In memory only:
	// rules are caller text, and the ledger holds none.
	PlanKind      string   `json:"-"`
	BoundaryRules []string `json:"-"`
	// review is the caller's record of the plan's latest complete review. The
	// store never looks inside it and hands back the same value, so the caller
	// must treat a stored value as immutable.
	review any
	// sessions maps every session ever attached to a row to that row, so an
	// implementer that re-validated and carried on with its first session
	// still updates its task.
	sessions map[string]sessionRef
}

// sessionRef is the row a session attached to and the plan heading of the
// task it resolved to then. The heading is kept per session, apart from the
// row, because a caller's title may paraphrase it and because a revision can
// leave an earlier task's row at the Index a later task resolves to.
type sessionRef struct {
	index   int
	heading string
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

// cloneTasks copies tasks and each task's Files, so the store's task list
// shares no backing array with a caller's slice or with a copy it hands out.
func cloneTasks(tasks []PlanTask) []PlanTask {
	cp := append([]PlanTask(nil), tasks...)
	for i := range cp {
		cp[i].Files = append([]string(nil), cp[i].Files...)
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
	return s.CreateForPlan(planVerdict, planQuality, tasks, "", nil)
}

// CreateForPlan mints a run for the plan's tasks with the plan kind and
// boundary rules of the validate_plan round that reviewed it. The run is
// stored whole, so no reader sees it without its declarations.
func (s *Store) CreateForPlan(planVerdict, planQuality string, tasks []PlanTask, planKind string, rules []string) *Run {
	now := time.Now()
	r := &Run{
		ID:            newID(),
		CreatedAt:     now,
		LastAccessed:  now,
		PlanVerdict:   planVerdict,
		PlanQuality:   planQuality,
		TaskCount:     len(tasks),
		Tasks:         cloneTasks(tasks),
		PlanKind:      planKind,
		BoundaryRules: append([]string(nil), rules...),
		Revision:      1,
		sessions:      map[string]sessionRef{},
	}
	s.mu.Lock()
	s.runs[r.ID] = r
	s.mu.Unlock()
	return r
}

// Review returns the review record last stored on run runID and the run's
// revision. ok is false when the run is unknown or expired; a known run that
// has no record yet returns a nil review.
func (s *Store) Review(runID string) (review any, revision int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, found := s.runs[runID]
	if !found {
		return nil, 0, false
	}
	r.LastAccessed = time.Now()
	return r.review, r.Revision, true
}

// SetReview stores review on run runID. Returns false when the run is unknown.
func (s *Store) SetReview(runID string, review any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	r.review = review
	r.LastAccessed = time.Now()
	return true
}

// Revise records a later validate_plan round on run runID under one lock: the
// run takes the round's verdict, quality, task list, plan kind, boundary rules
// and review record, and its revision goes up by one. Rows already attached keep their Index, so a
// round that renumbers tasks after dispatch leaves them where they were.
// Returns a copy of the run as this round left it, taken under the same lock,
// and false when the run is unknown or expired.
func (s *Store) Revise(runID, planVerdict, planQuality string, tasks []PlanTask, planKind string, rules []string, review any) (*Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return nil, false
	}
	r.PlanVerdict = planVerdict
	r.PlanQuality = planQuality
	r.TaskCount = len(tasks)
	r.Tasks = cloneTasks(tasks)
	r.PlanKind = planKind
	r.BoundaryRules = append([]string(nil), rules...)
	r.Revision++
	r.review = review
	r.LastAccessed = time.Now()
	return r.snapshot(), true
}

// TaskFiles returns the paths run runID's plan lists for the task ref names,
// by ref.Index when it is one of the plan's tasks and else by the one heading
// matching ref.Title. It returns nil when the run is unknown or expired, or
// ref names no plan task.
func (s *Store) TaskFiles(runID string, ref TaskRef) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return nil
	}
	index := ref.Index
	if index < 1 || index > len(r.Tasks) {
		index = r.taskByTitle(titleKey(ref.Title))
	}
	for _, t := range r.Tasks {
		if t.Index == index {
			return append([]string(nil), t.Files...)
		}
	}
	return nil
}

// AgentNetwork is what a plan run holds about one task's agent-network
// declarations: the plan's kind and boundary rules, and the task's own kind
// and rung, empty when the call names no task of the plan. TaskFound
// reports whether it named one, so an empty kind can be told apart from no
// task.
type AgentNetwork struct {
	PlanKind      string
	BoundaryRules []string
	TaskKind      string
	Rung          string
	TaskFound     bool
}

// TaskAgentNetwork returns run runID's agent-network declarations for the
// task ref names, found as TaskFiles finds it. ok is false when the run is
// unknown or expired, or the store is nil; a ref that names no plan task still
// returns the run's plan kind and rules, with an empty TaskKind and Rung.
func (s *Store) TaskAgentNetwork(runID string, ref TaskRef) (AgentNetwork, bool) {
	if s == nil {
		return AgentNetwork{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return AgentNetwork{}, false
	}
	out := AgentNetwork{PlanKind: r.PlanKind, BoundaryRules: append([]string(nil), r.BoundaryRules...)}
	if ref.empty() {
		return out, true
	}
	index := ref.Index
	if index < 1 || index > len(r.Tasks) {
		index = r.taskByTitle(titleKey(ref.Title))
	}
	out.taskFrom(r, index)
	return out, true
}

// taskFrom sets the kind and rung of r's task numbered index, and TaskFound,
// when the plan has that task.
func (a *AgentNetwork) taskFrom(r *Run, index int) {
	for _, t := range r.Tasks {
		if t.Index == index {
			a.TaskKind, a.Rung, a.TaskFound = t.Kind, t.Rung, true
			return
		}
	}
}

// SessionAgentNetwork returns run runID's agent-network declarations for the
// task session sessionID is attached to. ok is false when the run is unknown
// or expired, the session is attached to none of its rows, or the store is
// nil.
func (s *Store) SessionAgentNetwork(runID, sessionID string) (AgentNetwork, bool) {
	if s == nil {
		return AgentNetwork{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return AgentNetwork{}, false
	}
	ref, ok := r.sessions[sessionID]
	if !ok {
		return AgentNetwork{}, false
	}
	// A revision can renumber tasks while attached rows keep their Index, so
	// the session's plan heading, else its row's title, finds the task first
	// and the row's position is the fallback.
	index := ref.index
	key := titleKey(ref.heading)
	if pos := r.rowPos(index); key == "" && pos >= 0 {
		key = titleKey(r.Rows[pos].TaskTitle)
	}
	if byTitle := r.taskByTitle(key); key != "" && byTitle != 0 {
		index = byTitle
	}
	out := AgentNetwork{PlanKind: r.PlanKind, BoundaryRules: append([]string(nil), r.BoundaryRules...)}
	out.taskFrom(r, index)
	return out, true
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
	return r.snapshot(), true
}

// snapshot returns the copy Snapshot hands out. The caller holds the store's
// lock.
func (r *Run) snapshot() *Run {
	cp := *r
	cp.Tasks = cloneTasks(r.Tasks)
	cp.sessions = nil
	cp.review = nil
	cp.Rows = make([]TaskRow, len(r.Rows))
	for i, row := range r.Rows {
		cp.Rows[i] = cloneRow(row)
	}
	cp.ConfiguredModels = cloneStringMap(r.ConfiguredModels)
	cp.PlanCall = cloneCall(r.PlanCall)
	cp.BoundaryRules = append([]string(nil), r.BoundaryRules...)
	return &cp
}

// cloneRow deep-copies row: Severity, Categories and Codescene would
// otherwise still alias the live row's maps and digest.
func cloneRow(row TaskRow) TaskRow {
	row.Severity = cloneIntMap(row.Severity)
	row.Categories = cloneIntMap(row.Categories)
	row.Codescene = cloneDigest(row.Codescene)
	row.RateDigest = row.RateDigest.ForRecord()
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

// SoleLiveByTitle returns the id of the server's single live run when title
// matches exactly one of that run's plan headings. It reports false when no
// run, or more than one, has been used within the TTL, or the title matches
// no heading or several, or is blank once its "Task N:" prefix is removed: a
// guess between two runs or two tasks would attach a task to the wrong row. It does not refresh LastAccessed. Safe on a nil
// Store.
func (s *Store) SoleLiveByTitle(title string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var sole *Run
	for _, r := range s.runs {
		if now.Sub(r.LastAccessed) > s.ttl {
			continue
		}
		if sole != nil {
			return "", false
		}
		sole = r
	}
	// An empty key would match a heading that is itself only "Task N:".
	key := titleKey(title)
	if sole == nil || key == "" || sole.taskByTitle(key) == 0 {
		return "", false
	}
	return sole.ID, true
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
	pos, task := r.rowFor(ref, false)
	row := &r.Rows[pos]
	row.SessionID = sessionID
	row.PreVerdict = preVerdict
	row.Attempts++
	row.Lite = false
	if r.sessions == nil {
		r.sessions = map[string]sessionRef{}
	}
	r.sessions[sessionID] = sessionRef{index: row.Index, heading: r.taskTitle(task)}
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
	ref, ok := r.sessions[sessionID]
	if !ok {
		return TaskRow{}, false
	}
	pos := r.rowPos(ref.index)
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
	pos, _ := r.rowFor(ref, true)
	mutate(&r.Rows[pos])
	r.LastAccessed = time.Now()
	return cloneRow(r.Rows[pos]), true
}

// rowFor returns the position of the row ref names, adding the row when the
// task has none, and the Index of the plan task ref resolved to, or 0 when it
// named none. A new row takes the plan's heading when ref carries no title.
func (r *Run) rowFor(ref TaskRef, lite bool) (pos, task int) {
	pos, index, unmatched := r.resolve(ref)
	if !unmatched {
		task = index
	}
	if pos >= 0 {
		return pos, task
	}
	title := ref.Title
	if strings.TrimSpace(title) == "" {
		title = r.taskTitle(index)
	}
	return r.insertRow(TaskRow{
		Index: index, TaskTitle: title, Unmatched: unmatched, Lite: lite,
		CodesceneState: StateMissing,
	}), task
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
