package planrun

import (
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

func TestCreate_IDShape(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 5)
	assert.Regexp(t, regexp.MustCompile(`^pr_[0-9a-f]{12}$`), r.ID)
	assert.Equal(t, 5, r.TaskCount)
}

func TestAppendAndUpdateRow_PreservesOrder(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "actionable", 2)

	require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: "s1", TaskTitle: "first", PreVerdict: "pass"}))
	require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: "s2", TaskTitle: "second", PreVerdict: "warn"}))

	require.True(t, s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.PostVerdict = "pass"
		row.CodesceneState = StateRan
		row.Codescene = &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: -1.5}
	}))

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
	assert.False(t, s.AppendRow("pr_deadbeefdead", TaskRow{SessionID: "x"}))
	assert.False(t, s.UpdateRow(r.ID, "nope", func(*TaskRow) {}))
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
			s.AppendRow(r.ID, TaskRow{SessionID: string(rune('a' + i%26))})
		}(i)
	}
	wg.Wait()

	got, ok := s.Get(r.ID)
	require.True(t, ok)
	assert.Len(t, got.Rows, 50)
}

// TestSnapshot_IndependentOfLaterUpdate is the load-bearing proof that
// Snapshot copies Rows rather than aliasing the live slice. The lever is
// UpdateRow, not AppendRow: UpdateRow writes into the existing backing array
// via mutate(&r.Rows[i]) with no reallocation, so a non-copying Snapshot
// would alias that same array and the snapshot would observe the mutation.
// (An append-based version of this test is NOT discriminating: appending to
// a slice at cap==len forces Go to allocate a new backing array regardless
// of whether Snapshot copied, so the isolation would come from the runtime's
// realloc, not from the fix under test — see fix-round-2 notes in the task
// report for the empirical proof.)
func TestSnapshot_IndependentOfLaterUpdate(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: "s1", TaskTitle: "first"}))

	snap, ok := s.Snapshot(r.ID)
	require.True(t, ok)
	require.Len(t, snap.Rows, 1)

	require.True(t, s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.TaskTitle = "mutated-after-snapshot"
	}))

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
	require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: "s1", TaskTitle: "first"}))
	require.True(t, s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.Severity = map[string]int{"major": 1}
		row.Codescene = &codescene.Digest{Ran: true, NetPP: -1.5}
	}))

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
	require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: "s1", TaskTitle: "first"}))
	require.True(t, s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.Codescene = &codescene.Digest{
			Ran:            true,
			Verdicts:       &codescene.Verdicts{Improved: 1},
			CategoryCounts: map[string]int{"complexity": 1},
		}
	}))

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
// TestSnapshot_IndependentOfLaterUpdate. It replaces a prior
// TestConcurrentSnapshotWhileAppending, which raced Snapshot against
// AppendRow only: AppendRow only ever writes into array slots beyond the
// length any earlier-captured snapshot already saw (or, on reallocation,
// leaves the old array's contents untouched), so a reader ranging over
// snap.Rows (bounded by the length captured at Snapshot time) never
// physically overlaps memory with an AppendRow write. That made it
// structurally incapable of ever catching a non-copying Snapshot under
// -race, no matter how many iterations — confirmed empirically (see the
// task report). UpdateRow is the only operation that writes into an index a
// prior snapshot's slice already exposed, so it is the only lever that can
// produce a genuine overlapping, unsynchronized access against a broken
// (aliasing) Snapshot.
func TestConcurrentSnapshotWhileUpdating(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 50)
	sessionIDs := make([]string, 50)
	for i := 0; i < 50; i++ {
		sessionIDs[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
		require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: sessionIDs[i], TaskTitle: "x"}))
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
