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
