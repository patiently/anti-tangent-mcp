package planrun

import (
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

// attach records one validate_task_spec session against the task named by title.
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

func TestCreate_IDShape(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 5)
	assert.Regexp(t, regexp.MustCompile(`^pr_[0-9a-f]{12}$`), r.ID)
	assert.Equal(t, 5, r.TaskCount)
}

func TestAppendAndUpdateRow_PreservesOrder(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "actionable", 2)

	attach(t, s, r.ID, "s1", "first", "pass")
	attach(t, s, r.ID, "s2", "second", "warn")

	_, ok := s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.PostVerdict = "pass"
		row.CodesceneState = StateRan
		row.Codescene = &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: -1.5}
	})
	require.True(t, ok)

	got, ok := s.Get(r.ID)
	require.True(t, ok)
	require.Len(t, got.Rows, 2)
	assert.Equal(t, "first", got.Rows[0].TaskTitle)
	assert.Equal(t, "second", got.Rows[1].TaskTitle)
	assert.Equal(t, "pass", got.Rows[0].PostVerdict)
	assert.Equal(t, "", got.Rows[1].PostVerdict, "incomplete tasks keep an empty post verdict")
}

func TestUpdateRow_UnknownIDs(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	_, ok := s.Attach("pr_deadbeefdead", "x", TaskRef{Title: "t"}, "pass")
	assert.False(t, ok)
	_, ok = s.UpdateRow(r.ID, "nope", func(*TaskRow) {})
	assert.False(t, ok)
}

func TestEvictExpired(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	s.runs[r.ID].LastAccessed = time.Now().Add(-2 * time.Hour)

	assert.Equal(t, 1, s.EvictExpired(time.Now()))
	_, ok := s.Get(r.ID)
	assert.False(t, ok)
}

func TestConcurrentAppend(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 50)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Attach(r.ID, fmt.Sprintf("s%d", i), TaskRef{Title: fmt.Sprintf("task %d", i)}, "pass")
		}(i)
	}
	wg.Wait()

	got, ok := s.Get(r.ID)
	require.True(t, ok)
	assert.Len(t, got.Rows, 50)
}

// TestSnapshot_IndependentOfLaterUpdate is the load-bearing proof that
// Snapshot copies Rows rather than aliasing the live slice. The lever is
// UpdateRow: it writes into the existing backing array via mutate(&r.Rows[i])
// with no reallocation, so a non-copying Snapshot would alias that same array
// and the snapshot would observe the mutation. An append-only write is NOT
// discriminating here: appending to a slice at cap==len forces Go to
// allocate a new backing array regardless of whether Snapshot copied, so the
// isolation would come from the runtime's realloc, not from the fix under
// test.
func TestSnapshot_IndependentOfLaterUpdate(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	attach(t, s, r.ID, "s1", "first", "pass")

	snap, ok := s.Snapshot(r.ID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1)

	_, ok = s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.TaskTitle = "mutated-after-snapshot"
	})
	require.True(t, ok)

	assert.Equal(t, "first", snap.Rows[0].TaskTitle, "snapshot row must not observe the later in-place mutation")

	got, ok := s.Get(r.ID)
	require.True(t, ok)
	assert.Equal(t, "mutated-after-snapshot", got.Rows[0].TaskTitle)
}

// TestSnapshot_DeepCopiesSeverityAndCodescene proves Snapshot does not share
// TaskRow.Severity (map) or TaskRow.Codescene (pointer) with the live run.
// Both are mutated IN PLACE on the live run's row after the snapshot is
// taken — reassigning the whole field (row.Severity = newMap) would pass
// even against a shallow copy, since the snapshot would keep the old
// map/pointer value regardless. Only a write into the SAME underlying
// map/struct a shallow copy would still alias can prove the copy is deep.
func TestSnapshot_DeepCopiesSeverityAndCodescene(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	attach(t, s, r.ID, "s1", "first", "pass")
	_, ok := s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.Severity = map[string]int{"major": 1}
		row.Codescene = &codescene.Digest{Ran: true, NetPP: -1.5}
	})
	require.True(t, ok)

	snap, ok := s.Snapshot(r.ID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1)

	// Mutate the live run's map/struct in place, not by reassigning the field.
	s.mu.Lock()
	s.runs[r.ID].Rows[0].Severity["major"] = 99
	s.runs[r.ID].Rows[0].Codescene.NetPP = 999
	s.mu.Unlock()

	assert.Equal(t, 1, snap.Rows[0].Severity["major"], "snapshot must not observe an in-place Severity mutation")
	assert.InDelta(t, -1.5, snap.Rows[0].Codescene.NetPP, 0.0001, "snapshot must not observe an in-place Codescene mutation")
}

// TestSnapshot_DeepCopiesCodesceneNestedFields proves Snapshot's copy of
// TaskRow.Codescene goes one level deeper than the Digest struct itself:
// codescene.Digest.Verdicts (pointer) and .CategoryCounts (map) would
// otherwise still alias the live row's values even after `d := *row.Codescene`
// copies the struct by value. As with TestSnapshot_DeepCopiesSeverityAndCodescene,
// mutating IN PLACE after the snapshot is taken — not reassigning the whole
// field — is what discriminates a deep copy from a shallow one.
func TestSnapshot_DeepCopiesCodesceneNestedFields(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	attach(t, s, r.ID, "s1", "first", "pass")
	_, ok := s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.Codescene = &codescene.Digest{
			Ran:            true,
			Verdicts:       &codescene.Verdicts{Improved: 1},
			CategoryCounts: map[string]int{"complexity": 1},
		}
	})
	require.True(t, ok)

	snap, ok := s.Snapshot(r.ID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1)

	// Mutate the live run's nested Verdicts/CategoryCounts in place, not by
	// reassigning the field.
	s.mu.Lock()
	s.runs[r.ID].Rows[0].Codescene.Verdicts.Improved = 99
	s.runs[r.ID].Rows[0].Codescene.CategoryCounts["complexity"] = 99
	s.mu.Unlock()

	assert.Equal(t, 1, snap.Rows[0].Codescene.Verdicts.Improved, "snapshot must not observe an in-place Verdicts mutation")
	assert.Equal(t, 1, snap.Rows[0].Codescene.CategoryCounts["complexity"], "snapshot must not observe an in-place CategoryCounts mutation")
}

// TestConcurrentSnapshotWhileUpdating is the race-detector counterpart to
// TestSnapshot_IndependentOfLaterUpdate. An append-only write only ever
// writes into array slots beyond the length any earlier-captured snapshot
// already saw (or, on reallocation, leaves the old array's contents
// untouched), so a reader ranging over snap.Rows (bounded by the length
// captured at Snapshot time) never physically overlaps memory with an
// append-only write. That makes append-only writes structurally incapable of
// ever catching a non-copying Snapshot under -race, no matter how many
// iterations. UpdateRow is the only operation that writes into an index a
// prior snapshot's slice already exposed, so it is the only lever that can
// produce a genuine overlapping, unsynchronized access against a broken
// (aliasing) Snapshot.
func TestConcurrentSnapshotWhileUpdating(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 50)
	sessionIDs := make([]string, 50)
	for i := 0; i < 50; i++ {
		sessionIDs[i] = fmt.Sprintf("s%d", i)
		attach(t, s, r.ID, sessionIDs[i], fmt.Sprintf("task %d", i), "pass")
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snap, ok := s.Snapshot(r.ID)
			if !ok {
				continue
			}
			for _, row := range snap.Rows {
				_ = row.TaskTitle
			}
		}
	}()

	var writers sync.WaitGroup
	for _, id := range sessionIDs {
		writers.Add(1)
		go func(id string) {
			defer writers.Done()
			s.UpdateRow(r.ID, id, func(row *TaskRow) {
				row.TaskTitle = "updated"
			})
		}(id)
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	got, ok := s.Get(r.ID)
	require.True(t, ok)
	assert.Len(t, got.Rows, 50)
}

func TestLatest_MostRecentLiveRun(t *testing.T) {
	s := NewStore(time.Minute)
	_, ok := s.Latest()
	assert.False(t, ok, "empty store")

	older := s.Create("pass", "actionable", 1)
	newer := s.Create("warn", "rigorous", 2)
	older.CreatedAt = time.Now().Add(-10 * time.Second)

	got, ok := s.Latest()
	require.True(t, ok)
	assert.Equal(t, newer.ID, got.ID)

	newer.LastAccessed = time.Now().Add(-2 * time.Minute)
	got, ok = s.Latest()
	require.True(t, ok)
	assert.Equal(t, older.ID, got.ID, "a run idle past the TTL is skipped")

	before := older.LastAccessed
	_, _ = s.Latest()
	assert.Equal(t, before, older.LastAccessed, "Latest must not refresh LastAccessed")
}

func TestLatest_NilStore(t *testing.T) {
	var s *Store
	_, ok := s.Latest()
	assert.False(t, ok)
}
