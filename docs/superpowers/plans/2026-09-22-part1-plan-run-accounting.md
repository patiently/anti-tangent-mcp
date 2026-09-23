# Plan-run accounting and finding identity (0.25.0 Part 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `plan_run_report` counts plan tasks instead of sessions, records lightweight tasks, counts the branch's CodeScene delta once, survives a restart intact, reports real reviewer time on truncation, and plan-level finding IDs never collide with session finding IDs.

**Architecture:** `internal/planrun` gains the plan's task list and resolves every call to a plan task (by `task_index`, then by heading, then by an existing row's title), so a re-validation updates its row instead of appending one. The report's totals are computed per plan task and the CodeScene total takes the latest row per base ref. The handlers pass task references in and write the ledger each time a row changes. Plan-level findings get a reserved fingerprint scope key.

**Tech Stack:** Go (stdlib, `testify`), the MCP go-sdk already in use.

**Spec:** `docs/superpowers/specs/2026-09-22-field-report-0.24.0-improvements-design.md`, Part 1 (§1.1–§1.9) and the Protocol text section.

## Global Constraints

- **Branch:** all work is on `version/0.25.0`. Do not bump `VERSION`; the release workflow does that.
- **Tests:** `go test -race ./...` passes after every task. Unit tests never touch the network. Run `gofmt -l .` and fix anything it lists.
- **No anti-tangent gating.** This plan changes anti-tangent's own bookkeeping and finding identity. Implementers do NOT call `validate_task_spec`, `check_progress` or `validate_completion`, and the controller does not call `validate_plan` on this plan. The gate is the per-task spec and code-quality review plus the final whole-branch review.
- **CodeScene, every task:** run `pre_commit_code_health_safeguard` on the staged change before each commit and fix any degradation it reports. Before reporting DONE, run `analyze_change_set` with `base_ref: "origin/main"` and report its quality gate and net problem points in the DONE report.
- **Comments** (project CLAUDE.md "Comments"): a comment explains non-obvious behaviour or a hazard. No change history in comments: no version numbers, issue or task references, "previously", "no longer", "now". Where a step shows a comment, use it as written.
- **Public repository:** no consumer-project names, ticket IDs or code in code, tests, comments or the changelog.
- **CHANGELOG:** one entry, `## [0.25.0] - 2026-09-22`, created by Task 1. Every later task adds its own bullets to that entry under the subsection the step names. Keep a Changelog subsections only.
- **Protocol budget:** each `docs/protocol/*.md` part stays under 16,000 bytes, `INTEGRATION.md` under 2,000, and `plugin/anti-tangent-protocol/protocol/` identical to `docs/protocol/`.
- **Commit trailer:** end every commit message with

  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01LLiejXXFdTJgRg4ZKeCkAd
  ```

**User decisions (already made):**
- Rows are keyed by plan task and a re-validation updates its row (spec option 1); no `session_id` on `validate_task_spec`.
- Three parts ship as one minor release; nothing reaches `main` before Part 3.
- anti-tangent is not used to gate this plan's tasks.

---

### Task 1: The plan-run store keys rows by plan task

**Goal:** `planrun.Store` knows the plan's tasks and resolves every attach or update to one row per plan task, so re-validation, a second session and a lightweight completion all land on the same row.

**Files:**
- Modify: `internal/planrun/planrun.go`
- Modify: `internal/planrun/planrun_test.go`
- Modify: `internal/mcpsrv/handlers.go` (the three store call sites only, to keep the build green)
- Modify: `internal/mcpsrv/handlers_plan_run_report_test.go` (`TestPlanRunReport_NoProviderCall` only)
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `Run` carries `Tasks []PlanTask`; `Store.CreateWithTasks` stores them and `Store.Create(verdict, quality, n)` creates `n` untitled tasks numbered 1..n.
- [ ] `Store.Attach` resolves a `TaskRef` by in-range `Index`, then by a unique heading match, then by an existing row with the same title; an untitled plan takes new rows in dispatch order; anything else becomes an `Unmatched` row numbered after the plan's tasks.
- [ ] Attaching the same task again updates its row (`PreVerdict`, `Attempts++`) and never adds a row; every session ever attached to a row keeps updating it through `UpdateRow`.
- [ ] `Store.UpsertLite` updates the named task's row, or adds a `Lite` row titled from the plan when the task has none.
- [ ] `Rows` stays in `Index` order; `Attach`, `UpdateRow` and `UpsertLite` return a deep copy of the row.
- [ ] `AppendRow` is gone; `UpdateRow` returns `(TaskRow, bool)`.
- [ ] `go test -race ./...` passes.

**Verify:** `go test -race ./internal/planrun/... ./internal/mcpsrv/...` → `ok` for both packages.

**Steps:**

- [ ] **Step 1: Write the failing tests**

Add to `internal/planrun/planrun_test.go` (add `"fmt"` to its imports):

```go
// attach replaces AppendRow in these tests: it records one validate_task_spec
// session against the task named by title.
func attach(t *testing.T, s *Store, runID, sessionID, title, pre string) TaskRow {
	t.Helper()
	row, ok := s.Attach(runID, sessionID, TaskRef{Title: title}, pre)
	require.True(t, ok)
	return row
}

func titledTasks(titles ...string) []PlanTask {
	out := make([]PlanTask, len(titles))
	for i, title := range titles {
		out[i] = PlanTask{Index: i + 1, Title: title}
	}
	return out
}

func TestAttach_ReValidationUpdatesTheSameRow(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha", "Task 2: Beta"))
	attach(t, s, r.ID, "s1", "Alpha", "fail")
	attach(t, s, r.ID, "s2", "Task 1:  alpha", "fail")
	row := attach(t, s, r.ID, "s3", "Task 1: Alpha", "warn")

	assert.Equal(t, 1, row.Index)
	assert.Equal(t, 3, row.Attempts)
	assert.Equal(t, "warn", row.PreVerdict)
	got, _ := s.Snapshot(r.ID)
	assert.Len(t, got.Rows, 1, "a re-validation must not add a row")
}

func TestAttach_IndexWinsOverTitle(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha", "Task 2: Beta"))
	row, ok := s.Attach(r.ID, "s1", TaskRef{Index: 2, Title: "Alpha"}, "pass")
	require.True(t, ok)
	assert.Equal(t, 2, row.Index)
}

func TestAttach_OutOfRangeIndexFallsBackToTitle(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha", "Task 2: Beta"))
	row, ok := s.Attach(r.ID, "s1", TaskRef{Index: 9, Title: "Beta"}, "pass")
	require.True(t, ok)
	assert.Equal(t, 2, row.Index)
	assert.False(t, row.Unmatched)
}

func TestAttach_AmbiguousHeadingIsUnmatchedButStillOneRow(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Same", "Task 2: Same"))
	first := attach(t, s, r.ID, "s1", "Same", "fail")
	again := attach(t, s, r.ID, "s2", "Same", "pass")

	assert.True(t, first.Unmatched)
	assert.Equal(t, 3, first.Index, "an unmatched row is numbered after the plan's tasks")
	assert.Equal(t, first.Index, again.Index)
	assert.Equal(t, 2, again.Attempts)
}

func TestAttach_UntitledPlanTakesDispatchOrder(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 2)
	assert.Equal(t, 1, attach(t, s, r.ID, "s1", "Task A", "pass").Index)
	assert.Equal(t, 2, attach(t, s, r.ID, "s2", "Task B", "pass").Index)
	assert.Equal(t, 1, attach(t, s, r.ID, "s3", "Task A", "warn").Index, "same title, same row")
	extra := attach(t, s, r.ID, "s4", "Task C", "pass")
	assert.True(t, extra.Unmatched, "a plan with every task dispatched has no slot left")
	assert.Equal(t, 3, extra.Index)
}

func TestAttach_EmptyRefRecordsNothing(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 2)
	_, ok := s.Attach(r.ID, "s1", TaskRef{Title: "  "}, "pass")
	assert.False(t, ok)
	got, _ := s.Snapshot(r.ID)
	assert.Empty(t, got.Rows)
}

func TestUpdateRow_EverySessionOfATaskUpdatesIt(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha"))
	attach(t, s, r.ID, "s1", "Alpha", "fail")
	attach(t, s, r.ID, "s2", "Alpha", "pass")

	row, ok := s.UpdateRow(r.ID, "s1", func(row *TaskRow) { row.PostVerdict = "pass" })
	require.True(t, ok, "the first session still names the task")
	assert.Equal(t, "pass", row.PostVerdict)
	_, ok = s.UpdateRow(r.ID, "never-attached", func(*TaskRow) {})
	assert.False(t, ok)
}

func TestUpsertLite_AddsALiteRowTitledFromThePlan(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha", "Task 2: Beta"))
	row, ok := s.UpsertLite(r.ID, TaskRef{Index: 2}, func(row *TaskRow) { row.PostVerdict = "pass" })
	require.True(t, ok)
	assert.True(t, row.Lite)
	assert.Equal(t, 2, row.Index)
	assert.Equal(t, "Task 2: Beta", row.TaskTitle)
	assert.Equal(t, "pass", row.PostVerdict)
	assert.Zero(t, row.Attempts, "a lightweight task has no validate_task_spec session")
}

func TestUpsertLite_UpdatesAnAttachedRowWithoutMarkingItLite(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: Alpha"))
	attach(t, s, r.ID, "s1", "Alpha", "pass")
	row, ok := s.UpsertLite(r.ID, TaskRef{Title: "Alpha"}, func(row *TaskRow) { row.PostVerdict = "warn" })
	require.True(t, ok)
	assert.False(t, row.Lite)
	assert.Equal(t, "warn", row.PostVerdict)
	got, _ := s.Snapshot(r.ID)
	assert.Len(t, got.Rows, 1)
}

func TestRowsStayInIndexOrder(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: A", "Task 2: B", "Task 3: C"))
	attach(t, s, r.ID, "s3", "C", "pass")
	attach(t, s, r.ID, "s1", "A", "pass")
	got, _ := s.Snapshot(r.ID)
	require.Len(t, got.Rows, 2)
	assert.Equal(t, []int{1, 3}, []int{got.Rows[0].Index, got.Rows[1].Index})
}

func TestPlanTaskCount(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.CreateWithTasks("pass", "rigorous", titledTasks("Task 1: A", "Task 2: B"))
	n, ok := s.PlanTaskCount(r.ID)
	assert.True(t, ok)
	assert.Equal(t, 2, n)
	_, ok = s.PlanTaskCount("pr_000000000000")
	assert.False(t, ok)
}
```

Then rewrite the existing tests in the same file that use the removed API:

- Every `require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: X, TaskTitle: Y, PreVerdict: Z}))` becomes `attach(t, s, r.ID, X, Y, Z)`. Where the literal has no `PreVerdict`, pass `"pass"`.
- Every `require.True(t, s.UpdateRow(...))` becomes `_, ok := s.UpdateRow(...)` followed by `require.True(t, ok)` (use `=` instead of `:=` when `ok` is already declared).
- `TestUpdateRow_UnknownIDs`: replace the `AppendRow` assertion with
  `_, ok := s.Attach("pr_deadbeefdead", "x", TaskRef{Title: "t"}, "pass")` and `assert.False(t, ok)`, and the `UpdateRow` assertion with `_, ok = s.UpdateRow(r.ID, "nope", func(*TaskRow) {})` and `assert.False(t, ok)`.
- `TestConcurrentAppend` and `TestConcurrentSnapshotWhileUpdating` create their run with `s.Create("pass", "rigorous", 50)`, and every row is attached with a distinct title, `fmt.Sprintf("task %d", i)`, and a distinct session id, `fmt.Sprintf("s%d", i)` (in `TestConcurrentAppend`, `s.Attach(r.ID, fmt.Sprintf("s%d", i), TaskRef{Title: fmt.Sprintf("task %d", i)}, "pass")` inside the goroutine; the existing `sessionIDs` slice in the other test is filled the same way). Both still expect 50 rows.
- `TestSnapshot_IndependentOfLaterUpdate`: its mutation sets `row.TaskTitle = "mutated-after-snapshot"`; keep that, the title is not a key once a session is attached.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/planrun/...`
Expected: FAIL to compile — `undefined: TaskRef`, `s.CreateWithTasks undefined`, `s.Attach undefined`.

- [ ] **Step 3: Implement**

In `internal/planrun/planrun.go`, add `"regexp"`, `"sort"` and `"strings"` to the imports, and make these changes.

Append three fields to `TaskRow`, after `Escalated`:

```go
	// Attempts is how many validate_task_spec sessions attached to the task.
	// A re-validation updates the task's row instead of adding one.
	Attempts int `json:"attempts,omitempty"`
	// Lite marks a row a lightweight validate_completion created: it has no
	// session and no pre-task verdict.
	Lite bool `json:"lite,omitempty"`
	// Unmatched marks a row that named no plan task, by index or by title. It
	// is numbered after the plan's tasks.
	Unmatched bool `json:"unmatched,omitempty"`
```

Add after `TaskRow`:

```go
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
```

Replace the `Run` struct with:

```go
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
	Rows []TaskRow `json:"rows"`
	// sessions maps every session ever attached to a row to that row's Index,
	// so an implementer that re-validated and carried on with its first
	// session still updates its task.
	sessions map[string]int
}
```

Replace `Create` with:

```go
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
```

In `Snapshot`, replace the whole deep-copy loop with a call to a new `cloneRow`, copy `Tasks`, and drop the session index from the copy:

```go
	cp := *r
	cp.Tasks = append([]PlanTask(nil), r.Tasks...)
	cp.sessions = nil
	cp.Rows = make([]TaskRow, len(r.Rows))
	for i, row := range r.Rows {
		cp.Rows[i] = cloneRow(row)
	}
	return &cp, true
```

and add, moving the existing Severity / Codescene copy logic into it unchanged:

```go
// cloneRow deep-copies row: Severity and Codescene would otherwise still alias
// the live row's map and digest.
func cloneRow(row TaskRow) TaskRow {
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
	return row
}
```

Keep `Snapshot`'s existing doc comment, but shorten its second paragraph to say only that rows are deep-copied via `cloneRow` because `Severity` and `Codescene` would otherwise alias the live row.

Delete `AppendRow` and replace `UpdateRow` with:

```go
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
	row := &r.Rows[r.rowFor(ref, false)]
	row.SessionID = sessionID
	row.PreVerdict = preVerdict
	row.Attempts++
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
	if key != "" {
		for i, row := range r.Rows {
			if titleKey(row.TaskTitle) == key {
				return i, row.Index, row.Unmatched
			}
		}
	}
	if !r.titled() {
		if next := r.nextFreeTask(); next > 0 {
			return -1, next, false
		}
	}
	return -1, r.nextUnmatchedIndex(), true
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
```

Keep the build green in `internal/mcpsrv/handlers.go`:

- In `ValidateTaskSpec`, replace the `h.deps.PlanRuns.AppendRow(args.PlanRunID, planrun.TaskRow{...})` call with
  `_, ok := h.deps.PlanRuns.Attach(args.PlanRunID, env.SessionID, planrun.TaskRef{Title: args.TaskTitle}, env.Verdict)` and test `!ok` in the existing `if` that logs the warning. Leave the rest of that block alone; Task 4 finishes it.
- In `CheckProgress` and `ValidateCompletion`, change `if !h.deps.PlanRuns.UpdateRow(...)` to `if _, ok := h.deps.PlanRuns.UpdateRow(...); !ok`.

In `internal/mcpsrv/handlers_plan_run_report_test.go`, `TestPlanRunReport_NoProviderCall` replaces its `store.AppendRow(...)` with:

```go
	_, ok := store.Attach(run.ID, "s1", planrun.TaskRef{Title: "Task one"}, "pass")
	require.True(t, ok)
	_, ok = store.UpdateRow(run.ID, "s1", func(row *planrun.TaskRow) { row.PostVerdict = "pass" })
	require.True(t, ok)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/planrun/... ./internal/mcpsrv/...`
Expected: `ok` for both.

- [ ] **Step 5: Add the CHANGELOG entry**

Insert above `## [0.24.0] - 2026-09-20` in `CHANGELOG.md`:

```markdown
## [0.25.0] - 2026-09-22

### Fixed

- `plan_run_report` keeps one row per plan task. A task is found by its position in the plan or by
  its title matching a plan heading, and validating the same task again updates its row and counts
  an attempt instead of adding a second row. Every session a task ever opened keeps updating that
  row, so an implementer that re-validated and carried on with its first `session_id` still lands
  on the right task.
```

- [ ] **Step 6: Commit**

Run `pre_commit_code_health_safeguard` on the staged change first.

```bash
git add internal/planrun/planrun.go internal/planrun/planrun_test.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_plan_run_report_test.go CHANGELOG.md
git commit -m "fix(planrun): key plan-run rows by plan task, not by session"
```

```json:metadata
{"files": ["internal/planrun/planrun.go", "internal/planrun/planrun_test.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/handlers_plan_run_report_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/planrun/... ./internal/mcpsrv/...", "acceptanceCriteria": ["Run carries Tasks; CreateWithTasks stores them; Create(n) makes n untitled tasks", "Attach resolves by index, unique heading, existing row title, dispatch order for untitled plans, else Unmatched after the plan's tasks", "Re-attach updates the row and increments Attempts; every attached session updates the row via UpdateRow", "UpsertLite updates or adds a Lite row titled from the plan", "Rows stay in Index order; Attach/UpdateRow/UpsertLite return deep copies", "AppendRow removed; UpdateRow returns (TaskRow, bool)", "go test -race ./... passes"], "modelTier": "standard"}
```

---

### Task 2: The report counts plan tasks and the branch CodeScene delta once

**Goal:** `planrun.Totals` and `planrun.Render` count per plan task, list tasks never dispatched by heading, count unmatched rows separately, count CodeScene only for completed tasks, and total CodeScene net problem points as the latest result per base ref.

**Files:**
- Modify: `internal/codescene/codescene.go`
- Modify: `internal/codescene/codescene_test.go`
- Modify: `internal/planrun/report.go`
- Modify: `internal/planrun/report_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `codescene.Digest` has `BaseRef` (`json:"base_ref,omitempty"`); `Normalize` trims it and keeps its first 200 runes; the raw `analyze_change_set` shape with a `base_ref` key keeps it.
- [ ] `RunTotals` gains `NeverDispatched` and `Unmatched`; completed, pass/warn/fail and incomplete count only rows that name a plan task.
- [ ] CodeScene ran/skipped/missing count only rows with a post verdict.
- [ ] `NetPP` sums, over each distinct base ref (absent is its own group), the net problem points of the most recently completed row that ran; the rendered line reads `branch net problem points (latest per base ref)`.
- [ ] The table has an AT cell of `pass`, `pass (lite)` or `open (pre: warn)` and a Tries column; the footer lists never-dispatched tasks by heading and counts unmatched rows; the "N rows for M tasks" note is gone.
- [ ] `go test -race ./...` passes.

**Verify:** `go test -race ./internal/planrun/... ./internal/codescene/...` → `ok` for both.

**Steps:**

- [ ] **Step 1: Write the failing tests**

In `internal/codescene/codescene_test.go` add:

```go
func TestNormalize_TrimsAndCapsBaseRef(t *testing.T) {
	d := Digest{BaseRef: "  " + strings.Repeat("a", 250) + "  "}
	d.Normalize()
	assert.Equal(t, strings.Repeat("a", 200)+"…", d.BaseRef)

	short := Digest{BaseRef: " origin/main "}
	short.Normalize()
	assert.Equal(t, "origin/main", short.BaseRef)
}

func TestUnmarshal_RawShapeKeepsBaseRef(t *testing.T) {
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(`{"quality_gates":"passed","results":[],"base_ref":"origin/main"}`), &d))
	assert.True(t, d.Ran)
	assert.Equal(t, "origin/main", d.BaseRef)
}
```

(Add `strings`, `encoding/json`, `assert` and `require` to that file's imports if it does not already have them.)

In `internal/planrun/report_test.go`:

- `TestRender_Contents`: replace `assert.Contains(t, got, "incomplete")` with `assert.Contains(t, got, "open (pre: warn)")`.
- Rename `TestRender_IncompleteIsNotFail` to `TestRender_OpenIsNotFail`, and assert `row3` contains `"open (pre: warn)"` instead of `"incomplete"`. Keep its `NotContains(t, row3, "fail")`.
- `TestTotals`: change the CodeScene-missing expectation to `assert.Equal(t, 0, tot.CodesceneMissing, "an open task has not had its chance to run CodeScene")` and add `assert.Equal(t, 0, tot.NeverDispatched)` and `assert.Equal(t, 0, tot.Unmatched)`.
- Delete `TestRender_OverDispatchedRows` and add:

```go
func TestTotals_CountsPlanTasksNotRows(t *testing.T) {
	r := &Run{
		ID: "pr_shape0000001", PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 7,
		Tasks: []PlanTask{
			{Index: 1, Title: "Task 1: One"}, {Index: 2, Title: "Task 2: Two"}, {Index: 3, Title: "Task 3: Three"},
			{Index: 4, Title: "Task 4: Four"}, {Index: 5, Title: "Task 5: Five"}, {Index: 6, Title: "Task 6: Six"},
			{Index: 7, Title: "Task 7: Seven"},
		},
		Rows: []TaskRow{
			{Index: 1, TaskTitle: "One", PreVerdict: "warn", PostVerdict: "pass", Attempts: 3},
			{Index: 2, TaskTitle: "Two", PreVerdict: "warn", PostVerdict: "pass", Attempts: 1},
			{Index: 3, TaskTitle: "Three", PreVerdict: "warn", PostVerdict: "pass", Attempts: 1},
			{Index: 4, TaskTitle: "Task 4: Four", PostVerdict: "pass", Lite: true},
			{Index: 5, TaskTitle: "Task 5: Five", PostVerdict: "warn", Lite: true},
			{Index: 6, TaskTitle: "Task 6: Six", PostVerdict: "pass", Lite: true},
			{Index: 8, TaskTitle: "Stray", PostVerdict: "fail", Unmatched: true},
		},
	}
	tot := Totals(r)
	assert.Equal(t, 7, tot.Tasks)
	assert.Equal(t, 6, tot.Completed)
	assert.Equal(t, 5, tot.Pass)
	assert.Equal(t, 1, tot.Warn)
	assert.Equal(t, 0, tot.Fail, "an unmatched row is not a plan task")
	assert.Equal(t, 1, tot.NeverDispatched)
	assert.Equal(t, 1, tot.Unmatched)

	got := Render(r)
	assert.Contains(t, got, "tasks: 6 of 7 completed")
	assert.Contains(t, got, "never dispatched: 1\n")
	assert.Contains(t, got, "Task 7: Seven")
	assert.Contains(t, got, "unmatched: 1 row(s) named no plan task by task_index or title")
	assert.Contains(t, got, "pass (lite)")
	assert.NotContains(t, got, "rows for", "the duplicate-row note is gone")
}

func TestRender_NeverDispatchedWithoutHeadingsListsNumbers(t *testing.T) {
	r := &Run{ID: "pr_legacy000001", TaskCount: 3, Rows: []TaskRow{{Index: 1, TaskTitle: "a", PostVerdict: "pass"}}}
	got := Render(r)
	assert.Contains(t, got, "never dispatched: 2\n")
	assert.Contains(t, got, "    2  task 2\n")
	assert.Contains(t, got, "    3  task 3\n")
}

func TestTotals_BranchNetPPCountsEachBaseRefOnce(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 9, 22, h, 0, 0, 0, time.UTC) }
	ran := func(base string, pp float64) *codescene.Digest {
		return &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: pp, BaseRef: base}
	}
	r := &Run{ID: "pr_netpp0000001", TaskCount: 5, Rows: []TaskRow{
		{Index: 1, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("origin/main", -2), CompletedAt: at(10)},
		{Index: 2, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("origin/main", -2), CompletedAt: at(11)},
		{Index: 3, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("origin/main", -3), CompletedAt: at(12)},
		{Index: 4, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("", 1), CompletedAt: at(9)},
		{Index: 5, PostVerdict: "pass", CodesceneState: StateRan, Codescene: ran("", 4), CompletedAt: at(8)},
	}}
	tot := Totals(r)
	assert.InDelta(t, -3+1, tot.NetPP, 0.0001, "latest per base ref: -3 for origin/main, +1 for the rows naming none")
	assert.Contains(t, Render(r), "branch net problem points (latest per base ref): -2.0")
}

func TestVerdictCell(t *testing.T) {
	assert.Equal(t, "pass", verdictCell(TaskRow{PostVerdict: "pass"}))
	assert.Equal(t, "warn (lite)", verdictCell(TaskRow{PostVerdict: "warn", Lite: true}))
	assert.Equal(t, "open (pre: fail)", verdictCell(TaskRow{PreVerdict: "fail"}))
	assert.Equal(t, "open", verdictCell(TaskRow{}))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/planrun/... ./internal/codescene/...`
Expected: FAIL to compile — `unknown field BaseRef`, `tot.NeverDispatched undefined`, `undefined: verdictCell`.

- [ ] **Step 3: Implement**

In `internal/codescene/codescene.go`, add to `Digest` after `CategoryCounts`:

```go
	BaseRef string `json:"base_ref,omitempty" jsonschema:"The base ref the analysis compared against, as passed to analyze_change_set. plan_run_report counts the latest result for each base ref once, so tasks that each measured the whole branch are not added together. The first 200 characters are kept."`
```

add the constant next to the other codescene caps:

```go
// codesceneBaseRefMaxRunes caps a caller's base ref: a ref name, never content.
const codesceneBaseRefMaxRunes = 200
```

and in `Normalize` add as its last line:

```go
	d.BaseRef = truncateRunes(strings.TrimSpace(d.BaseRef), codesceneBaseRefMaxRunes)
```

In `internal/planrun/report.go`, add `"strconv"` to the imports and replace `RunTotals` and `Totals` with:

```go
// RunTotals is the aggregate line of a plan-run report. Completed, Pass,
// Warn, Fail and Incomplete count rows that name a plan task; a row that
// names none counts only in Unmatched.
type RunTotals struct {
	Tasks            int     `json:"tasks"`
	Completed        int     `json:"completed"`
	Pass             int     `json:"pass"`
	Warn             int     `json:"warn"`
	Fail             int     `json:"fail"`
	Incomplete       int     `json:"incomplete"`
	NeverDispatched  int     `json:"never_dispatched"`
	Unmatched        int     `json:"unmatched"`
	CodesceneRan     int     `json:"codescene_ran"`
	CodesceneSkipped int     `json:"codescene_skipped"`
	CodesceneMissing int     `json:"codescene_missing"`
	NetPP            float64 `json:"net_pp"`
	Waived           int     `json:"waived"`
	Escalated        int     `json:"escalated"`
}

// Totals aggregates a run's rows. The CodeScene counts cover completed rows
// only: a task still open has not had its chance to run the analysis. NetPP
// is the branch delta rather than a sum over tasks; see branchNetPP.
func Totals(r *Run) RunTotals {
	t := RunTotals{Tasks: r.TaskCount}
	for _, row := range r.Rows {
		t.Waived += row.Waived
		if row.Escalated {
			t.Escalated++
		}
		if row.PostVerdict != "" {
			countCodescene(&t, row)
		}
		if row.Unmatched {
			t.Unmatched++
			continue
		}
		switch row.PostVerdict {
		case "pass":
			t.Pass++
			t.Completed++
		case "warn":
			t.Warn++
			t.Completed++
		case "fail":
			t.Fail++
			t.Completed++
		default:
			t.Incomplete++
		}
	}
	t.NeverDispatched = len(neverDispatched(r))
	t.NetPP = branchNetPP(r.Rows)
	return t
}

func countCodescene(t *RunTotals, row TaskRow) {
	switch row.CodesceneState {
	case StateRan:
		t.CodesceneRan++
	case StateSkipped:
		t.CodesceneSkipped++
	default:
		t.CodesceneMissing++
	}
}

// branchNetPP sums, over each distinct base ref, the net problem points of
// the most recently completed row whose analysis ran against it. Each task is
// asked for a branch-versus-base analysis, so rows sharing a base ref report
// the same cumulative change and adding them would count it once per task.
// Rows that named no base ref form one group. Keys are summed in sorted order
// so the float result does not depend on map iteration.
func branchNetPP(rows []TaskRow) float64 {
	latest := map[string]TaskRow{}
	for _, row := range rows {
		if row.Codescene == nil || !row.Codescene.Ran {
			continue
		}
		key := row.Codescene.BaseRef
		if cur, ok := latest[key]; !ok || !row.CompletedAt.Before(cur.CompletedAt) {
			latest[key] = row
		}
	}
	keys := make([]string, 0, len(latest))
	for k := range latest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sum float64
	for _, k := range keys {
		sum += latest[k].Codescene.NetPP
	}
	return sum
}

// neverDispatched lists, by Index, the plan tasks no row names.
func neverDispatched(r *Run) []int {
	has := map[int]bool{}
	for _, row := range r.Rows {
		if !row.Unmatched {
			has[row.Index] = true
		}
	}
	var out []int
	for i := 1; i <= r.TaskCount; i++ {
		if !has[i] {
			out = append(out, i)
		}
	}
	return out
}

// verdictCell renders the AT column: the post-task verdict, marked when a
// lightweight call recorded it, or "open" with the pre-task verdict for a
// task that has not completed.
func verdictCell(row TaskRow) string {
	switch {
	case row.PostVerdict != "" && row.Lite:
		return row.PostVerdict + " (lite)"
	case row.PostVerdict != "":
		return row.PostVerdict
	case row.PreVerdict != "":
		return "open (pre: " + row.PreVerdict + ")"
	default:
		return "open"
	}
}

func triesCell(row TaskRow) string {
	if row.Attempts == 0 {
		return "-"
	}
	return strconv.Itoa(row.Attempts)
}
```

In `Render`, change the table header and row lines to:

```go
	fmt.Fprintf(&b, "  #  %-*s  %-16s %-5s %-20s %s\n", width, "Task", "AT", "Tries", "Rulings", "CodeScene")
```

```go
		fmt.Fprintf(&b, "  %-2d %-*s  %-16s %-5s %-20s %s\n", row.Index, width,
			escapeReportCell(title), escapeReportCell(verdictCell(row)), triesCell(row),
			rulingsCell(row), escapeReportCell(codesceneCell(row)))
```

(delete the `at := row.PostVerdict` / `"incomplete"` lines), and replace everything from the `net problem points across run` line to the end of the function with:

```go
	fmt.Fprintf(&b, "  branch net problem points (latest per base ref): %+.1f\n", t.NetPP)
	if missing := neverDispatched(r); len(missing) > 0 {
		fmt.Fprintf(&b, "  never dispatched: %d\n", len(missing))
		for _, idx := range missing {
			label := r.taskTitle(idx)
			if strings.TrimSpace(label) == "" {
				label = fmt.Sprintf("task %d", idx)
			}
			fmt.Fprintf(&b, "    %-2d %s\n", idx, escapeReportCell(label))
		}
	}
	if t.Unmatched > 0 {
		fmt.Fprintf(&b, "  unmatched: %d row(s) named no plan task by task_index or title\n", t.Unmatched)
	}
	return b.String()
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/planrun/... ./internal/codescene/... ./internal/mcpsrv/...`
Expected: `ok` for all three. If an `internal/mcpsrv` test asserted `"incomplete"` or `"net problem points across run"` in a rendered report, update it to `"open (pre: "` or `"branch net problem points (latest per base ref)"`.

- [ ] **Step 5: CHANGELOG**

Add under `### Fixed` in the `0.25.0` entry:

```markdown
- `plan_run_report`'s counts are per plan task: tasks never dispatched are listed by heading, a row
  that named no plan task is counted as unmatched instead of inflating the totals, a task still in
  progress reads `open (pre: <verdict>)`, and a lightweight task's verdict is marked `(lite)`.
  CodeScene runs are counted as missing only for tasks that completed.
- The run's CodeScene total no longer adds up cumulative branch deltas. Each task is asked for a
  branch-versus-base analysis, so the report now takes the most recent result for each base ref,
  and the `codescene` argument accepts the `base_ref` that analysis compared against.
```

- [ ] **Step 6: Commit**

Run `pre_commit_code_health_safeguard` on the staged change first.

```bash
git add internal/codescene/codescene.go internal/codescene/codescene_test.go internal/planrun/report.go internal/planrun/report_test.go CHANGELOG.md
git commit -m "fix(planrun): count plan tasks and the branch CodeScene delta once in the report"
```

```json:metadata
{"files": ["internal/codescene/codescene.go", "internal/codescene/codescene_test.go", "internal/planrun/report.go", "internal/planrun/report_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/planrun/... ./internal/codescene/... ./internal/mcpsrv/...", "acceptanceCriteria": ["Digest.BaseRef trimmed and capped at 200 runes; raw shape keeps base_ref", "RunTotals gains NeverDispatched and Unmatched; task counts exclude unmatched rows", "CodeScene counts only rows with a post verdict", "NetPP is latest per base ref summed across refs; rendered as branch net problem points (latest per base ref)", "AT cell pass / pass (lite) / open (pre: X), Tries column, never-dispatched list, unmatched count, no rows-for note", "go test -race ./... passes"], "modelTier": "standard"}
```

---

### Task 3: The ledger carries task headings and rows that have not completed

**Goal:** the plan ledger's header records the plan's task headings, rows are keyed for pruning by when they were written when they have not completed, and a recovered run carries its tasks.

**Files:**
- Modify: `internal/planrun/ledger.go`
- Modify: `internal/planrun/ledger_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `AppendHeader` writes `tasks` (index and title per plan task); `Load` restores `Run.Tasks` from it.
- [ ] `Append` stamps `written_at`; `Prune` keys a row line on `completed_at`, else on `written_at`, and still retains a line that has neither.
- [ ] `Load` of two lines for one `Index` returns the last one, whether or not it completed.
- [ ] The doc comments on `ledgerLine`, `AppendHeader` and `Load` describe rows written as they change, and the header carrying headings.
- [ ] `go test -race ./...` passes.

**Verify:** `go test -race ./internal/planrun/...` → `ok`.

**Steps:**

- [ ] **Step 1: Write the failing tests**

In `internal/planrun/ledger_test.go`:

- In `TestLedger_HeaderOnlyRunLoads`, delete the three lines that read the file and assert `NotContains(..., "task_title", ...)`; the header now carries plan headings by design (next test).
- In `TestLedger_PruneRetainsZeroCompletedAt`, replace the `l.Append(run, TaskRow{Index: 1, TaskTitle: "no-timestamp"})` call with a raw line that carries neither timestamp, so the test keeps pinning the "no timestamp is kept" rule now that `Append` always stamps `written_at`:

```go
	raw := `{"plan_run_id":"pr_zero","row":{"index":1,"task_title":"no-timestamp","pre_verdict":"","checkpoints":0}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ledgerFile), []byte(raw), 0o600))
```

Add:

```go
func TestLedger_HeaderCarriesTaskHeadings(t *testing.T) {
	l := &Ledger{Dir: t.TempDir()}
	run := &Run{ID: "pr_titles000001", CreatedAt: time.Now().UTC(), TaskCount: 2,
		Tasks: []PlanTask{{Index: 1, Title: "Task 1: Alpha"}, {Index: 2, Title: "Task 2: Beta"}}}
	require.NoError(t, l.AppendHeader(run))

	got, ok := l.Load(run.ID)
	require.True(t, ok)
	assert.Equal(t, run.Tasks, got.Tasks)
}

func TestLedger_OpenRowLoadsAndTheLastLineWins(t *testing.T) {
	l := &Ledger{Dir: t.TempDir()}
	run := &Run{ID: "pr_open00000001", TaskCount: 1}
	require.NoError(t, l.Append(run, TaskRow{Index: 1, TaskTitle: "Alpha", PreVerdict: "fail", Attempts: 1}))
	require.NoError(t, l.Append(run, TaskRow{Index: 1, TaskTitle: "Alpha", PreVerdict: "warn", Attempts: 2}))

	got, ok := l.Load(run.ID)
	require.True(t, ok)
	require.Len(t, got.Rows, 1)
	assert.Equal(t, "warn", got.Rows[0].PreVerdict)
	assert.Equal(t, 2, got.Rows[0].Attempts)
	assert.Empty(t, got.Rows[0].PostVerdict)
}

func TestLedger_PruneKeysAnOpenRowOnWrittenAt(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lines := `{"plan_run_id":"pr_stale0000001","row":{"index":1,"task_title":"stale","pre_verdict":"pass","checkpoints":0},"written_at":"2026-08-01T00:00:00Z"}` + "\n" +
		`{"plan_run_id":"pr_fresh0000001","row":{"index":1,"task_title":"fresh","pre_verdict":"pass","checkpoints":0},"written_at":"2026-09-10T00:00:00Z"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ledgerFile), []byte(lines), 0o600))

	require.NoError(t, l.Prune(cutoff))
	_, ok := l.Load("pr_stale0000001")
	assert.False(t, ok, "an open row written before the cutoff is pruned")
	_, ok = l.Load("pr_fresh0000001")
	assert.True(t, ok)
}

func TestLedger_AppendStampsWrittenAt(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	require.NoError(t, l.Append(&Run{ID: "pr_stamp0000001"}, TaskRow{Index: 1, TaskTitle: "a"}))
	b, err := os.ReadFile(filepath.Join(dir, ledgerFile))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"written_at":"`)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/planrun/...`
Expected: FAIL — `TestLedger_HeaderCarriesTaskHeadings` (Tasks nil), `TestLedger_PruneKeysAnOpenRowOnWrittenAt` (stale row kept), `TestLedger_AppendStampsWrittenAt`.

- [ ] **Step 3: Implement**

In `internal/planrun/ledger.go`:

Add to `ledgerLine`, after `CreatedAt`:

```go
	// Tasks is set on header lines: the plan's task numbers and headings.
	Tasks []PlanTask `json:"tasks,omitempty"`
	// WrittenAt is set on task-row lines: when the line was appended. Prune
	// keys a row that has not completed on it.
	WrittenAt time.Time `json:"written_at,omitzero"`
```

Add `Tasks []PlanTask \`json:"tasks,omitempty"\`` to `ledgerHeaderLine` after `TaskCount`, and set `Tasks: run.Tasks` in `AppendHeader`'s literal.

In `Append`, add `WrittenAt: time.Now().UTC(),` to the `ledgerLine` literal.

In `Load`, inside `if ln.Header {`, after the `CreatedAt` assignment add:

```go
			if run.Tasks == nil {
				run.Tasks = ln.Tasks
			}
```

In `Prune`, replace

```go
		stamp := ln.Row.CompletedAt
		if ln.Header {
			stamp = ln.CreatedAt
		}
```

with

```go
		stamp := ln.Row.CompletedAt
		switch {
		case ln.Header:
			stamp = ln.CreatedAt
		case stamp.IsZero():
			stamp = ln.WrittenAt
		}
```

Rewrite these doc comments so they describe the behaviour as it now is (no history wording):

- `ledgerLine`: the first sentence becomes "ledgerLine is one line of the ledger file: a task row, written each time the row changes, or a run header." In the PRIVACY paragraph, say the record carries task titles and the header carries the plan's task headings.
- `Ledger`'s type comment: "appends completed task rows" becomes "appends a task row each time one changes".
- `AppendHeader`: replace "It carries no task title." with "It carries the plan's task headings, which the report lists for tasks never dispatched."
- `Load`: replace the paragraph that begins "validate_completion may legitimately run more than once" with: "A task's row is written when validate_task_spec attaches it and again whenever it changes, and a row's Index is its plan task number, so Load keeps the last line seen per Index: the task's current state, whether or not it completed. Rows are then sorted by Index."
- `Prune`: say a task row is keyed on `Row.CompletedAt`, or on `WrittenAt` when it has not completed, and that a line with neither is retained.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/planrun/...`
Expected: `ok`.

- [ ] **Step 5: CHANGELOG**

Add under `### Fixed`:

```markdown
- A plan-run report recovered from the plan ledger after a restart matches the live one. Rows are
  written when a task attaches and again whenever they change, not only at completion, so a task
  still in progress survives a restart; the ledger header records the plan's task headings.
```

- [ ] **Step 6: Commit**

Run `pre_commit_code_health_safeguard` on the staged change first.

```bash
git add internal/planrun/ledger.go internal/planrun/ledger_test.go CHANGELOG.md
git commit -m "fix(planrun): record task headings and open rows in the plan ledger"
```

```json:metadata
{"files": ["internal/planrun/ledger.go", "internal/planrun/ledger_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/planrun/...", "acceptanceCriteria": ["AppendHeader writes tasks; Load restores Run.Tasks", "Append stamps written_at; Prune keys completed_at else written_at; a line with neither is retained", "Load returns the last line per Index, open or completed", "Doc comments describe rows written as they change and headings in the header", "go test -race ./... passes"], "modelTier": "mechanical"}
```

---

### Task 4: The handlers attach tasks, record lightweight tasks, and write the ledger as rows change

**Goal:** `validate_plan` creates its run with the plan's task headings, `validate_task_spec` takes `task_index`, a lightweight `validate_completion` records its task, and every row change is written to the ledger.

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/mcpsrv/review_error.go` (`mintPlanRunID`)
- Create: `internal/mcpsrv/handlers_plan_run_rows_test.go`
- Modify: `internal/mcpsrv/handlers_plan_ledger_test.go`
- Modify: `internal/mcpsrv/handlers_plan_run_attach_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `validate_plan` mints its run with `CreateWithTasks`, one `PlanTask` per parsed task heading (falling back to the reviewer's task titles when the plan parsed no tasks).
- [ ] `ValidateTaskSpecArgs.TaskIndex` exists; the call attaches through `Attach` with `TaskRef{Index, Title}`; an out-of-range index adds a minor `task_index` advisory that does not change the verdict; the attached row is written to the ledger.
- [ ] `ValidateCompletionArgs` has `PlanRunID`, `TaskIndex` and `TaskTitle`, read only when `session_id` is empty; such a call upserts a `Lite` row and writes it to the ledger; `plan_run_id` without index or title adds a minor `plan_run_id` advisory; with a session they are ignored.
- [ ] A session completion updates its row through `UpdateRow` and writes the returned row to the ledger; no ledger write scans `Snapshot` rows by session id.
- [ ] `unattachedPlanRunFinding`'s ledger text says the ledger records a task when it attaches.
- [ ] The change-history comment `// Added in v0.5.2 from field reports:` in `evidenceTruncationPatterns` is deleted.
- [ ] `go test -race ./...` passes.

**Verify:** `go test -race ./internal/mcpsrv/...` → `ok`.

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/mcpsrv/handlers_plan_run_rows_test.go`:

```go
package mcpsrv

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func titledRun(h *handlers, titles ...string) *planrun.Run {
	tasks := make([]planrun.PlanTask, len(titles))
	for i, title := range titles {
		tasks[i] = planrun.PlanTask{Index: i + 1, Title: title}
	}
	return h.deps.PlanRuns.CreateWithTasks("pass", "actionable", tasks)
}

func specFor(t *testing.T, h *handlers, runID, title string, index int) Envelope {
	t.Helper()
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: title, Goal: "G", AcceptanceCriteria: []string{"AC"}, PlanRunID: runID, TaskIndex: index,
	})
	require.NoError(t, err)
	require.NotEmpty(t, env.SessionID)
	return env
}

func completeSession(t *testing.T, h *handlers, sessionID string) Envelope {
	t.Helper()
	_, env, err := h.ValidateCompletion(context.Background(), nil, completionCallArgs(sessionID))
	require.NoError(t, err)
	return env
}

func completeLite(t *testing.T, h *handlers, runID string, index int, title string) Envelope {
	t.Helper()
	args := completionCallArgs("")
	args.PlanRunID, args.TaskIndex, args.TaskTitle = runID, index, title
	_, env, err := h.ValidateCompletion(context.Background(), nil, args)
	require.NoError(t, err)
	return env
}

func rowsOf(t *testing.T, h *handlers, runID string) []planrun.TaskRow {
	t.Helper()
	snap, ok := h.deps.PlanRuns.Snapshot(runID)
	require.True(t, ok)
	return snap.Rows
}

func findingsWithCriterion(fs []verdict.Finding, criterion string) []verdict.Finding {
	var out []verdict.Finding
	for _, f := range fs {
		if f.Criterion == criterion {
			out = append(out, f)
		}
	}
	return out
}

func TestValidateTaskSpec_ReValidationUpdatesOneRow(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: Alpha", "Task 2: Beta")
	for i := 0; i < 3; i++ {
		specFor(t, h, run.ID, "Task 1: Alpha", 0)
	}
	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].Index)
	assert.Equal(t, 3, rows[0].Attempts)
}

func TestValidateTaskSpec_TaskIndexNamesTheTask(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: Alpha", "Task 2: Beta")
	specFor(t, h, run.ID, "a title matching nothing", 2)
	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0].Index)
	assert.False(t, rows[0].Unmatched)
}

func TestValidateTaskSpec_OutOfRangeTaskIndexIsAdvisedNotFatal(t *testing.T) {
	baseline := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
	want := specFor(t, baseline, titledRun(baseline, "Task 1: Alpha", "Task 2: Beta").ID, "Beta", 0)

	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
	run := titledRun(h, "Task 1: Alpha", "Task 2: Beta")
	env := specFor(t, h, run.ID, "Beta", 9)

	got := findingsWithCriterion(env.Findings, "task_index")
	require.Len(t, got, 1)
	assert.Equal(t, verdict.SeverityMinor, got[0].Severity)
	assert.NotEmpty(t, got[0].ID)
	assert.Equal(t, want.Verdict, env.Verdict, "the advisory must not change the verdict")
	assert.Equal(t, 2, rowsOf(t, h, run.ID)[0].Index, "the title still finds the task")
}

func TestValidateCompletion_AnEarlierSessionStillUpdatesItsTask(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: Alpha")
	first := specFor(t, h, run.ID, "Alpha", 0)
	specFor(t, h, run.ID, "Alpha", 0)
	completeSession(t, h, first.SessionID)

	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, "pass", rows[0].PostVerdict)
}

func TestValidateCompletion_LightweightRecordsItsTask(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: One", "Task 2: Two", "Task 3: Three")
	env := completeLite(t, h, run.ID, 3, "")
	assert.True(t, env.Lightweight)

	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, 3, rows[0].Index)
	assert.True(t, rows[0].Lite)
	assert.Equal(t, "Task 3: Three", rows[0].TaskTitle)
	assert.Equal(t, env.Verdict, rows[0].PostVerdict)
}

func TestValidateCompletion_LightweightWithoutATaskIsAdvised(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: One")
	env := completeLite(t, h, run.ID, 0, "")

	got := findingsWithCriterion(env.Findings, "plan_run_id")
	require.Len(t, got, 1)
	assert.Equal(t, verdict.SeverityMinor, got[0].Severity)
	assert.Empty(t, rowsOf(t, h, run.ID))
}

func TestValidateCompletion_ASessionCallIgnoresTheTaskFields(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: One", "Task 2: Two")
	pre := specFor(t, h, run.ID, "One", 0)
	args := completionCallArgs(pre.SessionID)
	args.PlanRunID, args.TaskIndex = run.ID, 2
	_, _, err := h.ValidateCompletion(context.Background(), nil, args)
	require.NoError(t, err)

	rows := rowsOf(t, h, run.ID)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].Index)
	assert.Equal(t, "pass", rows[0].PostVerdict)
}

func TestPlanRunReport_CountsTasksForARunMixingModes(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := titledRun(h, "Task 1: One", "Task 2: Two", "Task 3: Three", "Task 4: Four",
		"Task 5: Five", "Task 6: Six", "Task 7: Seven")
	var last Envelope
	for i := 0; i < 3; i++ {
		last = specFor(t, h, run.ID, "Task 1: One", 0)
	}
	completeSession(t, h, last.SessionID)
	completeSession(t, h, specFor(t, h, run.ID, "Two", 0).SessionID)
	completeSession(t, h, specFor(t, h, run.ID, "Task 3: Three", 0).SessionID)
	for idx := 4; idx <= 6; idx++ {
		completeLite(t, h, run.ID, idx, "")
	}

	_, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	assert.Len(t, res.Tasks, 6)
	assert.Equal(t, 6, res.Totals.Completed)
	assert.Equal(t, 1, res.Totals.NeverDispatched)
	assert.Equal(t, 0, res.Totals.Unmatched)
	assert.Equal(t, 3, res.Tasks[0].Attempts)
	assert.Contains(t, res.SummaryBlock, "never dispatched: 1")
	assert.Contains(t, res.SummaryBlock, "Task 7: Seven")
	assert.Contains(t, res.SummaryBlock, "pass (lite)")
}

func TestPlanLedger_RecoveredReportMatchesTheLiveOne(t *testing.T) {
	ledger := &planrun.Ledger{Dir: t.TempDir()}
	store := planrun.NewStore(time.Hour)
	h := &handlers{deps: planLedgerTestDeps(t, ledger, store)}
	run := store.CreateWithTasks("pass", "actionable", []planrun.PlanTask{{Index: 1, Title: "Task 1: One"}, {Index: 2, Title: "Task 2: Two"}})
	require.NoError(t, ledger.AppendHeader(run))
	completeSession(t, h, specFor(t, h, run.ID, "One", 0).SessionID)
	specFor(t, h, run.ID, "Two", 0)

	_, live, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	restarted := &handlers{deps: planLedgerTestDeps(t, ledger, planrun.NewStore(time.Hour))}
	_, recovered, err := restarted.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)

	assert.Equal(t, live.Totals, recovered.Totals)
	require.Len(t, recovered.Tasks, 2)
	assert.Empty(t, recovered.Tasks[1].PostVerdict, "the open task survives the restart")
	assert.Equal(t, "pass", recovered.Tasks[1].PreVerdict)
}

func TestValidatePlan_RunKnowsItsTaskHeadings(t *testing.T) {
	h := newTestPlanHandlers(t)
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: buildPlanWithNTasks(2)})
	require.NoError(t, err)
	snap, ok := h.deps.PlanRuns.Snapshot(pr.PlanRunID)
	require.True(t, ok)
	require.Len(t, snap.Tasks, 2)
	assert.Equal(t, 1, snap.Tasks[0].Index)
	assert.Regexp(t, `^Task 1:`, snap.Tasks[0].Title)
	assert.Equal(t, 2, snap.TaskCount)
}
```

`twoMinorsResp`, `newTestPlanHandlers`, `buildPlanWithNTasks`, `planLedgerTestDeps` and `completionCallArgs` already exist in this package's tests; if `buildPlanWithNTasks` does not produce headings of the form `### Task N: …`, assert on the headings it does produce.

Update the existing tests:

- `handlers_plan_ledger_test.go`, `TestValidateCompletion_ResubmitDedupesLedgerRow`: the raw-lines assertion becomes `require.Len(t, lines, 3, "one line when validate_task_spec attaches the task, then one per completion call")`.
- `handlers_plan_run_attach_test.go`, `TestValidatePlan_LedgerHeaderKeepsAnUnattachedRunKnown`: replace `assert.Contains(t, got[0].Evidence, "finished validate_completion")` with `assert.Contains(t, got[0].Evidence, "no task attached to it while the ledger was enabled")`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcpsrv/...`
Expected: FAIL to compile — `unknown field TaskIndex in struct literal of type ValidateTaskSpecArgs`, `args.PlanRunID undefined (type ValidateCompletionArgs …)`.

- [ ] **Step 3: Implement**

In `internal/mcpsrv/handlers.go`:

Add to `ValidateTaskSpecArgs`, after `PlanRunID`:

```go
	TaskIndex int `json:"task_index,omitempty" jsonschema:"The task's 1-based position in the plan, from the controller's dispatch. With plan_run_id it names the plan task this call belongs to; without it the task is found by matching task_title against the plan's headings. Validating the same task again updates its plan_run_report row instead of adding one."`
```

Add to `ValidateCompletionArgs`, after `ControllerRulings`:

```go
	PlanRunID string `json:"plan_run_id,omitempty" jsonschema:"Lightweight calls only (empty session_id): the plan_run_id from the controller's final passing validate_plan call, so plan_run_report counts this task. Pass task_index or task_title with it. Ignored when session_id is set, because the session already carries it; an unknown or expired id does not fail the call."`
	TaskIndex int    `json:"task_index,omitempty" jsonschema:"Lightweight calls only: the task's 1-based position in the plan. Ignored when session_id is set."`
	TaskTitle string `json:"task_title,omitempty" jsonschema:"Lightweight calls only: the task's heading in the plan, used to find the task when task_index is absent. Ignored when session_id is set."`
```

Add these helpers near `planRunIDAdvisory`:

```go
// taskIndexAdvisory explains a task_index that names no task of the plan run.
// It reports false when there is nothing to explain: no index, an index in
// range, or a run this server does not know.
func (h *handlers) taskIndexAdvisory(runID string, index int) (verdict.Finding, bool) {
	if index == 0 {
		return verdict.Finding{}, false
	}
	n, ok := h.deps.PlanRuns.PlanTaskCount(runID)
	if !ok || (index >= 1 && index <= n) {
		return verdict.Finding{}, false
	}
	return verdict.Finding{
		Severity:  verdict.SeverityMinor,
		Category:  verdict.CategoryOther,
		Criterion: "task_index",
		Evidence: fmt.Sprintf("task_index %d names no task of plan run %s, which has %d; the task was looked up by its title instead.",
			index, runID, n),
		Suggestion: "Pass the task's 1-based position in the plan, or leave task_index out.",
	}, true
}

// untargetedLitePlanRunAdvisory explains a lightweight validate_completion
// that passed plan_run_id without naming its task, which is therefore not
// recorded in the run.
func untargetedLitePlanRunAdvisory() verdict.Finding {
	return verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "plan_run_id",
		Evidence:   "plan_run_id was passed without task_index or task_title, so this lightweight task is not recorded in the plan run.",
		Suggestion: "Pass the task's 1-based task_index, or its task_title, with plan_run_id.",
	}
}

// appendPlanLedger writes a changed plan-run row to the plan ledger. Best
// effort: a failure is logged and never changes a result.
func (h *handlers) appendPlanLedger(runID string, row planrun.TaskRow) {
	run, ok := h.deps.PlanRuns.Get(runID)
	if !ok {
		return
	}
	if err := h.deps.PlanLedger.Append(run, row); err != nil {
		slog.Warn("plan ledger append failed", "plan_run_id", runID, "err", err)
	}
}

// completionRowUpdate is the plan-run row write for one validate_completion
// result.
func completionRowUpdate(env Envelope, cs *codescene.Digest) func(*planrun.TaskRow) {
	sev, _, _, _ := stats.CountFindings(env.Findings)
	state := planrun.StateMissing
	if cs != nil {
		if cs.Ran {
			state = planrun.StateRan
		} else {
			state = planrun.StateSkipped
		}
	}
	completedAt := time.Now().UTC()
	return func(row *planrun.TaskRow) {
		row.PostVerdict = env.Verdict
		row.Severity = sev
		row.SubmissionOnly = env.SubmissionDefectOnly
		row.Codescene = cs
		row.CodesceneState = state
		row.CompletedAt = completedAt
		row.Waived = len(env.WaivedFindings)
		row.Escalated = row.Escalated || env.Escalate
	}
}
```

In `ValidateTaskSpec`, turn the `if args.PlanRunID == "" { … }` advisory block into:

```go
	if args.PlanRunID == "" {
		if run, ok := h.deps.PlanRuns.Latest(); ok {
			env.Findings = append(env.Findings, planRunIDAdvisory(run.ID))
		}
	} else if f, ok := h.taskIndexAdvisory(args.PlanRunID, args.TaskIndex); ok {
		env.Findings = append(env.Findings, f)
	}
```

and replace the whole `if args.PlanRunID != "" && env.SessionID != "" { … }` block with:

```go
	if args.PlanRunID != "" && env.SessionID != "" {
		// Best-effort: an unknown or expired run must not fail the review.
		ref := planrun.TaskRef{Index: args.TaskIndex, Title: args.TaskTitle}
		if row, ok := h.deps.PlanRuns.Attach(args.PlanRunID, env.SessionID, ref, env.Verdict); ok {
			h.appendPlanLedger(args.PlanRunID, row)
		} else {
			slog.Warn("plan run attach failed; run unknown or expired",
				"plan_run_id", args.PlanRunID, "session_id", env.SessionID)
		}
	}
```

In `ValidateCompletion`, right after `env.Findings = append(env.Findings, repoRootAdvisories...)`, add:

```go
	if lightweight && args.PlanRunID != "" {
		if args.TaskIndex == 0 && strings.TrimSpace(args.TaskTitle) == "" {
			env.Findings = append(env.Findings, untargetedLitePlanRunAdvisory())
		} else if f, ok := h.taskIndexAdvisory(args.PlanRunID, args.TaskIndex); ok {
			env.Findings = append(env.Findings, f)
		}
	}
```

and replace the whole `if !lightweight && sess.PlanRunID != "" { … }` block (row update plus the Snapshot-scanning ledger append) with:

```go
	rowUpdate := completionRowUpdate(env, args.Codescene)
	switch {
	case !lightweight && sess.PlanRunID != "":
		if row, ok := h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, rowUpdate); ok {
			h.appendPlanLedger(sess.PlanRunID, row)
		} else {
			slog.Warn("plan run row update failed; run or row unknown",
				"plan_run_id", sess.PlanRunID, "session_id", sess.ID)
		}
	case lightweight && args.PlanRunID != "":
		ref := planrun.TaskRef{Index: args.TaskIndex, Title: args.TaskTitle}
		if row, ok := h.deps.PlanRuns.UpsertLite(args.PlanRunID, ref, rowUpdate); ok {
			h.appendPlanLedger(args.PlanRunID, row)
		} else {
			slog.Warn("plan run lightweight update skipped; run unknown or expired, or no task named",
				"plan_run_id", args.PlanRunID)
		}
	}
```

In `unattachedPlanRunFinding`, replace the doc comment's sentence about the ledger recording a task "only when it finishes validate_completion" with "A run recovered from the ledger has no rows because no task attached to it while the ledger was enabled.", and replace the `fromLedger` branch's `Evidence` and `Suggestion` with:

```go
			Evidence: fmt.Sprintf("Plan run %s is known from the plan ledger (%d tasks in the plan), but no task attached to it "+
				"while the ledger was enabled: the ledger records a task as soon as validate_task_spec, or a lightweight "+
				"validate_completion, passes this plan_run_id.", run.ID, run.TaskCount),
			Suggestion: "Report from the per-task DONE envelopes. Pass plan_run_id on every validate_task_spec call, " +
				"and on a lightweight task's validate_completion.",
```

In `evidenceTruncationPatterns`, delete the line `// Added in v0.5.2 from field reports:`.

In `internal/mcpsrv/review_error.go`, change `mintPlanRunID`'s create call to
`run := c.PlanRuns.CreateWithTasks(string(pr.PlanVerdict), string(pr.PlanQuality), planRunTasks(*pr, c.Tasks))`
and add:

```go
// planRunTasks lists the plan's tasks for a new run: the parsed headings, in
// order, or the reviewer's task titles when the plan parsed no tasks.
func planRunTasks(pr verdict.PlanResult, tasks []planparser.RawTask) []planrun.PlanTask {
	if len(tasks) > 0 {
		out := make([]planrun.PlanTask, len(tasks))
		for i, t := range tasks {
			out[i] = planrun.PlanTask{Index: i + 1, Title: t.Title}
		}
		return out
	}
	out := make([]planrun.PlanTask, len(pr.Tasks))
	for i, t := range pr.Tasks {
		out[i] = planrun.PlanTask{Index: i + 1, Title: t.TaskTitle}
	}
	return out
}
```

(add the `planparser` and `planrun` imports to `review_error.go` if missing).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/...`
Expected: `ok`. If a `validate_plan` test asserted a run's `TaskCount` equal to the reviewer's task count on a plan whose parsed heading count differs, assert the parsed count instead; the run now counts the plan's own headings.

- [ ] **Step 5: CHANGELOG**

Add under a new `### Added` subsection of the `0.25.0` entry (above `### Fixed`):

```markdown
### Added

- `validate_task_spec` accepts `task_index`, the task's 1-based position in the plan, which names
  its `plan_run_report` row directly; without it the task is found by its title. An index outside
  the plan draws a minor advisory and the title is used instead.
- A lightweight `validate_completion` (empty `session_id`) accepts `plan_run_id` with `task_index`
  or `task_title`, and the task then appears in `plan_run_report`, marked `(lite)`. Lightweight
  tasks were previously invisible to the report.
```

- [ ] **Step 6: Commit**

Run `pre_commit_code_health_safeguard` on the staged change first.

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/review_error.go internal/mcpsrv/handlers_plan_run_rows_test.go internal/mcpsrv/handlers_plan_ledger_test.go internal/mcpsrv/handlers_plan_run_attach_test.go CHANGELOG.md
git commit -m "feat(plan-run): attach tasks by index or heading and record lightweight tasks"
```

```json:metadata
{"files": ["internal/mcpsrv/handlers.go", "internal/mcpsrv/review_error.go", "internal/mcpsrv/handlers_plan_run_rows_test.go", "internal/mcpsrv/handlers_plan_ledger_test.go", "internal/mcpsrv/handlers_plan_run_attach_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/mcpsrv/...", "acceptanceCriteria": ["validate_plan mints its run with CreateWithTasks from parsed headings, falling back to reviewer titles", "validate_task_spec task_index attaches via Attach; out-of-range index is a minor task_index advisory that keeps the verdict; row written to the ledger", "validate_completion plan_run_id/task_index/task_title read only without a session; upsert a Lite row and write the ledger; plan_run_id alone draws a minor plan_run_id advisory", "session completion updates via UpdateRow and writes the returned row; no Snapshot scan", "unattachedPlanRunFinding ledger text says a task is recorded when it attaches", "the v0.5.2 change-history comment is deleted", "go test -race ./... passes"], "modelTier": "standard"}
```

---

### Task 5: Truncated reviews report the time the reviewer spent

**Goal:** every truncation path returns the elapsed reviewer time instead of 0, so `review_ms` and stats are honest for truncated calls.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`review`, `reviewPlanSingle`, `reviewPlanChunked`)
- Modify: `internal/mcpsrv/review_error.go` (`runReview`)
- Modify: `internal/mcpsrv/handlers_test.go` (`fakeReviewer`)
- Modify: `internal/mcpsrv/handlers_truncation_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `review` and `reviewPlanSingle` return `time.Since(start).Milliseconds()` on both truncation returns (first call and parse retry).
- [ ] `reviewPlanChunked` returns the accumulated time on a Pass-1 truncation, first call and retry.
- [ ] `runReview` carries the elapsed time into `reviewOutcome.ReviewMS` for a truncated review.
- [ ] `go test -race ./...` passes.

**Verify:** `go test -race ./internal/mcpsrv/... -run 'Truncat'` → `ok`.

**Steps:**

- [ ] **Step 1: Write the failing tests**

In `internal/mcpsrv/handlers_test.go`, give `fakeReviewer` a delay:

```go
type fakeReviewer struct {
	name        string
	resp        providers.Response
	err         error
	delay       time.Duration // slept before answering, so a test can observe elapsed reviewer time
	Calls       int
	LastRequest providers.Request // captured on every Review call; tests inspect rv.LastRequest.User to assert prompt content
}

func (f *fakeReviewer) Name() string { return f.name }
func (f *fakeReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	f.Calls++
	f.LastRequest = req
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.resp, f.err
}
```

(add `"time"` to the imports if missing).

In `internal/mcpsrv/handlers_truncation_test.go` add (add `"time"` and the `prompts` import):

```go
const truncationDelay = 20 * time.Millisecond

func TestValidateTaskSpec_TruncatedReviewReportsElapsedTime(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated, delay: truncationDelay}
	h := &handlers{deps: newDeps(t, rv)}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, env.ReviewMS, truncationDelay.Milliseconds())
}

func TestReviewPlanSingle_TruncationReportsElapsedTime(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated, delay: truncationDelay}
	h := &handlers{deps: newDeps(t, rv)}
	_, _, ms, _, err := h.reviewPlanSingle(context.Background(), h.deps.Cfg.PlanModel, prompts.Output{System: "s", User: "u"}, 100)
	require.ErrorIs(t, err, providers.ErrResponseTruncated)
	assert.GreaterOrEqual(t, ms, truncationDelay.Milliseconds())
}

func TestReviewPlanChunked_PassOneTruncationReportsElapsedTime(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: providers.ErrResponseTruncated, delay: truncationDelay}
	h := &handlers{deps: newDeps(t, rv)}
	rendered := renderedPlanReview{FindingsOnly: &prompts.Output{System: "s", User: "u"}}
	_, _, ms, _, err := h.reviewPlanChunked(context.Background(), h.deps.Cfg.PlanModel, rendered, 100)
	require.ErrorIs(t, err, providers.ErrResponseTruncated)
	assert.GreaterOrEqual(t, ms, truncationDelay.Milliseconds())
}
```

If `newDeps`'s config does not register the plan model's provider as `"anthropic"`, name the fake reviewer after `h.deps.Cfg.PlanModel.Provider` instead.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcpsrv/... -run 'TruncatedReviewReportsElapsedTime|TruncationReportsElapsedTime'`
Expected: FAIL — each reports `0` where at least `20` was expected.

- [ ] **Step 3: Implement**

In `internal/mcpsrv/handlers.go`:

- `review`: both truncation returns become `return verdict.Result{}, "", time.Since(start).Milliseconds(), resp.RawJSON, err`.
- `reviewPlanSingle`: both truncation returns become `return verdict.PlanResult{}, "", time.Since(start).Milliseconds(), resp.RawJSON, err`.
- `reviewPlanChunked`, Pass 1: both truncation returns become `return verdict.PlanResult{}, "", totalMs + time.Since(start).Milliseconds(), resp.RawJSON, err`.

In `internal/mcpsrv/review_error.go`, `runReview`: change `out := reviewOutcome{ModelUsed: model.String(), Truncated: true}` to
`out := reviewOutcome{ModelUsed: model.String(), ReviewMS: ms, Truncated: true}`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/...`
Expected: `ok`.

- [ ] **Step 5: CHANGELOG**

Add under `### Fixed`:

```markdown
- A truncated review reports the time the reviewer actually spent in `review_ms`, and in stats,
  instead of 0.
```

- [ ] **Step 6: Commit**

Run `pre_commit_code_health_safeguard` on the staged change first.

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/review_error.go internal/mcpsrv/handlers_test.go internal/mcpsrv/handlers_truncation_test.go CHANGELOG.md
git commit -m "fix(review): report elapsed reviewer time on a truncated response"
```

```json:metadata
{"files": ["internal/mcpsrv/handlers.go", "internal/mcpsrv/review_error.go", "internal/mcpsrv/handlers_test.go", "internal/mcpsrv/handlers_truncation_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/mcpsrv/...", "acceptanceCriteria": ["review and reviewPlanSingle return elapsed ms on both truncation returns", "reviewPlanChunked returns accumulated ms on a Pass-1 truncation", "runReview carries elapsed ms into ReviewMS for a truncated review", "go test -race ./... passes"], "modelTier": "mechanical"}
```

---

### Task 6: Plan-level findings get their own fingerprint scope

**Goal:** plan-level findings are fingerprinted with a reserved task key, so no plan-level ID equals a session-tool ID and a ruling on one never waives the other.

**Files:**
- Modify: `internal/mcpsrv/finding_ids.go`
- Modify: `internal/mcpsrv/plan_normalize.go` (`waivePlanFindings`)
- Modify: `internal/mcpsrv/handlers_ids_test.go`
- Modify: `internal/mcpsrv/handlers_rulings_test.go`
- Modify: `internal/mcpsrv/handlers_plan_rulings_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `planScopeKey` is a constant starting with a control character; `assignPlanIDs` uses it for plan-level findings and plan-level waived entries, and `waivePlanFindings` uses it for plan-level findings.
- [ ] Plan per-task finding IDs and session-tool finding IDs are unchanged.
- [ ] A plan-level ID passed in `validate_completion`'s `controller_rulings` waives nothing.
- [ ] A `validate_plan` ruling on a plan-level finding's new ID still waives it.
- [ ] `go test -race ./...` passes.

**Verify:** `go test -race ./internal/mcpsrv/... -run 'IDs|Ruling'` → `ok`.

**Steps:**

- [ ] **Step 1: Write the failing tests**

In `internal/mcpsrv/handlers_ids_test.go`, `TestValidatePlan_FindingsCarryDisplayIDs`: change the plan-level expectation to

```go
	assert.Equal(t, verdict.Fingerprint(verdict.CategoryAmbiguousSpec, planScopeKey, "AC"), pr.PlanFindings[0].ID)
	assert.NotEqual(t, verdict.Fingerprint(verdict.CategoryAmbiguousSpec, "", "AC"), pr.PlanFindings[0].ID,
		"a plan-level finding must not share a session finding's fingerprint")
```

and leave the task-level expectation (`"t1"`) as it is.

In `internal/mcpsrv/handlers_rulings_test.go` add:

```go
func TestValidateCompletion_APlanLevelIDNeverWaivesASessionFinding(t *testing.T) {
	const overBuilding = `{"severity":"minor","category":"quality","criterion":"over_building",` +
		`"evidence":"x.go:3: yagni: a helper with one caller. Inline it.\nnet: -4 lines","suggestion":"inline it","same_as":null}`
	h, rv := newRulingsHandlers(t)
	sid := startTask(t, h, rv)
	sessionID := completeWith(t, h, rv, completionCallArgs(sid), reviewerFindingsResp(overBuilding)).Findings[0].ID
	planID := verdict.Fingerprint(verdict.CategoryQuality, planScopeKey, "over_building")
	require.NotEqual(t, verdict.BaseID(sessionID), planID)

	args := completionCallArgs(sid)
	args.ControllerRulings = []ControllerRulingArg{{FindingID: planID, Ruling: "the plan asked for this helper"}}
	env := completeWith(t, h, rv, args, reviewerFindingsResp(overBuilding))
	assert.Empty(t, env.WaivedFindings, "a ruling on a plan-level id must not waive a completion finding")
}
```

In `internal/mcpsrv/handlers_plan_rulings_test.go`, three tests build a plan-level ID with the empty task key. Switch each to `planScopeKey` so they keep testing what they claim:

- `TestValidatePlan_RulingsWaivePlanAndTaskFindings`: `planID := verdict.Fingerprint(verdict.CategoryAmbiguousSpec, planScopeKey, "AC")`. This is the test that a `validate_plan` ruling on a plan-level finding still waives it.
- `TestValidatePlan_TheChecklistCannotBeWaived`: the ruling's `FindingID` becomes `verdict.Fingerprint(verdict.CategoryUnverifiableCodebaseClaim, planScopeKey, "codebase_reference_checklist")`. With the old key the ruling would name no finding at all, and the test would pass without testing anything.
- `TestValidatePlan_CacheHitReproducesWaivers`: the ruling's `FindingID` becomes `verdict.Fingerprint(verdict.CategoryAmbiguousSpec, planScopeKey, "AC")`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcpsrv/... -run 'FindingsCarryDisplayIDs|APlanLevelIDNeverWaives'`
Expected: FAIL to compile — `undefined: planScopeKey`.

- [ ] **Step 3: Implement**

In `internal/mcpsrv/finding_ids.go` add:

```go
// planScopeKey is the task key plan-level findings are fingerprinted under.
// It starts with a control character that no plan heading carries, so a
// plan-level finding never shares a fingerprint with a session tool's finding
// (empty task key) or with a plan task's (its heading): a ruling on one can
// never waive the other.
const planScopeKey = "\x1eplan"
```

and in `assignPlanIDs` change `a.Assign(pr.PlanFindings, "")` to `a.Assign(pr.PlanFindings, planScopeKey)` and `a.AssignWaived(pr.WaivedFindings, "")` to `a.AssignWaived(pr.WaivedFindings, planScopeKey)`.

In `internal/mcpsrv/plan_normalize.go`, `waivePlanFindings`: change `waiveRuled(pr.PlanFindings, "", rulings, nil)` to `waiveRuled(pr.PlanFindings, planScopeKey, rulings, nil)`. Update its doc comment to say plan-level findings are fingerprinted under `planScopeKey`.

Search for any other place a plan-level finding's fingerprint is computed with an empty task key: `grep -n 'Fingerprint(\|waiveRuled(\|Assign(\|AssignWaived(' internal/mcpsrv/*.go | grep -v _test`. Every plan-level site must use `planScopeKey`; session-tool sites keep `""`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/...`
Expected: `ok`. A plan test that hard-codes a plan-level ID computed with `""` must switch to `planScopeKey`.

- [ ] **Step 5: CHANGELOG**

Add under `### Fixed`:

```markdown
- Plan-level findings are fingerprinted apart from session findings. Every `over_building` finding
  outside a plan task shared one ID, `f_b720ec2d`, so a ruling on the plan-level finding could waive
  a `validate_completion` finding. Plan-level IDs change once with this release: a ruling carried
  over from an earlier round on a plan-level finding needs to be given again under the new ID.
```

- [ ] **Step 6: Commit**

Run `pre_commit_code_health_safeguard` on the staged change first.

```bash
git add internal/mcpsrv/finding_ids.go internal/mcpsrv/plan_normalize.go internal/mcpsrv/handlers_ids_test.go internal/mcpsrv/handlers_rulings_test.go internal/mcpsrv/handlers_plan_rulings_test.go CHANGELOG.md
git commit -m "fix(ids): fingerprint plan-level findings under their own scope"
```

```json:metadata
{"files": ["internal/mcpsrv/finding_ids.go", "internal/mcpsrv/plan_normalize.go", "internal/mcpsrv/handlers_ids_test.go", "internal/mcpsrv/handlers_rulings_test.go", "internal/mcpsrv/handlers_plan_rulings_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/mcpsrv/...", "acceptanceCriteria": ["planScopeKey constant with a leading control character used by assignPlanIDs (findings and waived) and waivePlanFindings", "plan per-task and session IDs unchanged", "a plan-level ID in validate_completion controller_rulings waives nothing", "a validate_plan ruling on the new plan-level ID still waives it", "go test -race ./... passes"], "modelTier": "mechanical"}
```

---

### Task 7: Protocol, example and README

**Goal:** the controller protocol, the lightweight example and the README tell callers to pass `plan_run_id` and `task_index`, including on lightweight tasks, and describe the corrected report.

**Files:**
- Modify: `docs/protocol/controller.md` (step 6 of §5.1)
- Modify: `plugin/anti-tangent-protocol/protocol/controller.md` (resync)
- Modify: `examples/lightweight-dispatch.md`
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `controller.md` step 6 reads exactly as in Step 1 below; the part is under 16,000 bytes and the plugin copy is identical.
- [ ] `examples/lightweight-dispatch.md` lists `plan_run_id`, `task_index` and `task_title` in its task-spec field list.
- [ ] `README.md` documents `task_index`, the lightweight `plan_run_id`, the codescene `base_ref`, and the per-task report with its branch net problem points.
- [ ] `go test -race ./...` passes and the CI byte checks pass locally.

**Verify:** `for f in docs/protocol/*.md; do wc -c "$f"; done; diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo identical` → every part under 16000 bytes, then `identical`.

**Steps:**

- [ ] **Step 1: Rewrite controller step 6**

In `docs/protocol/controller.md`, replace the whole of step 6 (from `6. **Capture \`plan_run_id\`**` through `use the id from your **final** passing call.`) with:

```markdown
6. **Capture `plan_run_id`** from the final passing `validate_plan` call and add it, with the
   task's 1-based `task_index`, to the dispatch clause: implementers pass both to
   `validate_task_spec`, lightweight ones to `validate_completion`. After the last task reports
   DONE, call `plan_run_report` with that id and surface the table to the user. The report is
   deterministic and free (no reviewer call).
```

Then resync the plugin bundle:

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
wc -c docs/protocol/controller.md
```

Expected: about 15,830 bytes, under 16,000.

- [ ] **Step 2: Lightweight example**

In `examples/lightweight-dispatch.md`, under `## Task spec (pass these fields verbatim to validate_completion)`, add after the `summary` bullet:

```markdown
- `plan_run_id`: the controller's `plan_run_id`, when the task belongs to a plan run, with
  `task_index` (the task's 1-based position in the plan) or `task_title` (its heading), so
  `plan_run_report` counts the task. Without one of them the task is not recorded.
```

- [ ] **Step 3: README**

In `README.md`:

- The `validate_task_spec` bullet in the tools list: after "Accepts the controller's `plan_run_id` (optional, best-effort) to tie the task to its plan run." add "Pass `task_index`, the task's 1-based position in the plan, with it; without one the task is found by matching its title against the plan's headings, and validating the same task again updates its report row."
- The `validate_completion` bullet: add "In lightweight mode (empty `session_id`) it also accepts `plan_run_id` with `task_index` or `task_title`, so the task is counted in `plan_run_report`."
- The `plan_run_report` bullet: replace "Returns a per-task table" with "Returns one row per plan task (a re-validated task keeps one row and counts its attempts; lightweight tasks are marked `(lite)`; tasks never dispatched are listed by heading)", and add "The CodeScene total is the branch's net problem points: the latest result per base ref, not a sum over tasks."
- The in-band attribution paragraph under CodeScene: after the `skip_evidence` mention add "and `base_ref`, the ref the analysis compared against, so `plan_run_report` counts a branch-versus-base result once".

- [ ] **Step 4: CHANGELOG**

Add a `### Changed` subsection to the `0.25.0` entry, between `### Added` and `### Fixed`:

```markdown
### Changed

- The controller protocol passes `task_index` with `plan_run_id` in every dispatch, and a
  lightweight task passes both to `validate_completion`, so every dispatched task appears in
  `plan_run_report`.
```

- [ ] **Step 5: Verify and commit**

Run: `for f in docs/protocol/*.md; do wc -c "$f"; done; wc -c INTEGRATION.md; diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo identical; go test -race ./...`
Expected: every protocol part under 16000, `INTEGRATION.md` under 2000, `identical`, all packages `ok`.

```bash
git add docs/protocol/controller.md plugin/anti-tangent-protocol/protocol/controller.md examples/lightweight-dispatch.md README.md CHANGELOG.md
git commit -m "docs: pass plan_run_id and task_index to every task, lightweight ones included"
```

```json:metadata
{"files": ["docs/protocol/controller.md", "plugin/anti-tangent-protocol/protocol/controller.md", "examples/lightweight-dispatch.md", "README.md", "CHANGELOG.md"], "verifyCommand": "for f in docs/protocol/*.md; do wc -c \"$f\"; done; diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo identical && go test -race ./...", "acceptanceCriteria": ["controller.md step 6 replaced verbatim; part under 16000 bytes; plugin copy identical", "lightweight example lists plan_run_id with task_index or task_title", "README documents task_index, lightweight plan_run_id, codescene base_ref, per-task report and branch net problem points", "go test -race ./... passes and byte checks pass"], "modelTier": "mechanical"}
```
