package session

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestStore_CreateAndGet(t *testing.T) {
	s := NewStore(1 * time.Hour)
	sess := s.Create(TaskSpec{Title: "t", Goal: "g"}, "")
	require.NotEmpty(t, sess.ID)

	got, ok := s.Get(sess.ID)
	require.True(t, ok)
	assert.Equal(t, sess.ID, got.ID)
	assert.Equal(t, "t", got.Spec.Title)
}

func TestStore_GetUnknown(t *testing.T) {
	s := NewStore(1 * time.Hour)
	_, ok := s.Get("nope")
	assert.False(t, ok)
}

func TestStore_AppendCheckpoint(t *testing.T) {
	s := NewStore(1 * time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")

	cp := Checkpoint{
		At:        time.Now(),
		WorkingOn: "writing handler",
		FileCount: 3,
		Verdict:   verdict.VerdictPass,
	}
	require.True(t, s.AppendCheckpoint(sess.ID, cp))

	got, _ := s.Get(sess.ID)
	require.Len(t, got.Checkpoints, 1)
	assert.Equal(t, "writing handler", got.Checkpoints[0].WorkingOn)
}

func TestStore_AppendCheckpointUnknown(t *testing.T) {
	s := NewStore(1 * time.Hour)
	assert.False(t, s.AppendCheckpoint("nope", Checkpoint{}))
}

func TestStore_TTL_Eviction(t *testing.T) {
	s := NewStore(50 * time.Millisecond)
	sess := s.Create(TaskSpec{Title: "t"}, "")

	// Force LastAccessed into the past by directly mutating (test-only).
	s.mu.Lock()
	s.sessions[sess.ID].LastAccessed = time.Now().Add(-1 * time.Hour)
	s.mu.Unlock()

	evicted := s.EvictExpired(time.Now())
	assert.Equal(t, 1, evicted)

	_, ok := s.Get(sess.ID)
	assert.False(t, ok)
}

func TestStore_GetUpdatesLastAccessed(t *testing.T) {
	s := NewStore(1 * time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	first := sess.LastAccessed

	time.Sleep(2 * time.Millisecond)
	got, _ := s.Get(sess.ID)
	assert.True(t, got.LastAccessed.After(first))
}

func TestStore_ReviewStateIsACopy(t *testing.T) {
	s := NewStore(time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	require.True(t, s.ApplyReview(sess.ID, ReviewUpdate{
		ReplacePrior:  true,
		PriorFindings: []verdict.Finding{{ID: "f_0123abcd", Criterion: "c"}},
		IssuedIDs:     []string{"f_0123abcd"},
		Rulings:       map[string]Ruling{"f_0123abcd": {ID: "f_0123abcd", Text: "ruled"}},
	}))

	st, ok := s.ReviewState(sess.ID)
	require.True(t, ok)
	st.PriorFindings[0].Criterion = "mutated"
	st.IssuedIDs["f_ffffffff"] = true
	st.Rulings["f_ffffffff"] = Ruling{ID: "f_ffffffff"}

	again, ok := s.ReviewState(sess.ID)
	require.True(t, ok)
	assert.Equal(t, "c", again.PriorFindings[0].Criterion)
	assert.Equal(t, map[string]bool{"f_0123abcd": true}, again.IssuedIDs)
	assert.Equal(t, map[string]Ruling{"f_0123abcd": {ID: "f_0123abcd", Text: "ruled"}}, again.Rulings)
}

func TestStore_ReviewStatePreAndCheckpointFindingsAreCopies(t *testing.T) {
	s := NewStore(time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	require.True(t, s.SetPreFindings(sess.ID, []verdict.Finding{{ID: "f_0123abcd", Criterion: "pre"}}))
	require.True(t, s.AppendCheckpoint(sess.ID, Checkpoint{Findings: []verdict.Finding{{ID: "f_89abcdef", Criterion: "cp1"}}}))

	st, ok := s.ReviewState(sess.ID)
	require.True(t, ok)
	require.Len(t, st.PreFindings, 1)
	require.Len(t, st.CheckpointFindings, 1)
	require.Len(t, st.CheckpointFindings[0], 1)
	st.PreFindings[0].Criterion = "mutated"
	st.CheckpointFindings[0][0].Criterion = "mutated"

	again, ok := s.ReviewState(sess.ID)
	require.True(t, ok)
	assert.Equal(t, "pre", again.PreFindings[0].Criterion)
	assert.Equal(t, "cp1", again.CheckpointFindings[0][0].Criterion)
}

func TestStore_ReviewMethodsRejectAnUnknownSession(t *testing.T) {
	s := NewStore(time.Hour)
	_, ok := s.ReviewState("nope")
	assert.False(t, ok)
	assert.False(t, s.ApplyReview("nope", ReviewUpdate{}))
	assert.False(t, s.RecordIssuedIDs("nope", []string{"f_0123abcd"}))
	_, ok = s.ExpiresAt("nope")
	assert.False(t, ok)
}

func TestStore_ApplyReviewWithoutReplacePriorKeepsThePriorFindings(t *testing.T) {
	s := NewStore(time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	require.True(t, s.ApplyReview(sess.ID, ReviewUpdate{
		ReplacePrior:  true,
		PriorFindings: []verdict.Finding{{Criterion: "complete"}},
	}))
	require.True(t, s.ApplyReview(sess.ID, ReviewUpdate{
		PriorFindings: []verdict.Finding{{Criterion: "truncated"}},
		IssuedIDs:     []string{"f_0123abcd"},
	}))

	st, _ := s.ReviewState(sess.ID)
	require.Len(t, st.PriorFindings, 1)
	assert.Equal(t, "complete", st.PriorFindings[0].Criterion)
	assert.True(t, st.IssuedIDs["f_0123abcd"])
}

func TestStore_ApplyReviewReplacesARulingAndCapsNewOnes(t *testing.T) {
	s := NewStore(time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	full := map[string]Ruling{}
	for i := 0; i < MaxRulings; i++ {
		fp := fmt.Sprintf("f_%08x", i)
		full[fp] = Ruling{ID: fp, Text: "first"}
	}
	require.True(t, s.ApplyReview(sess.ID, ReviewUpdate{Rulings: full}))
	require.True(t, s.ApplyReview(sess.ID, ReviewUpdate{Rulings: map[string]Ruling{
		"f_00000000": {ID: "f_00000000-2", Text: "replaced"},
		"f_ffffffff": {ID: "f_ffffffff", Text: "one too many"},
	}}))

	st, _ := s.ReviewState(sess.ID)
	assert.Len(t, st.Rulings, MaxRulings)
	assert.Equal(t, "replaced", st.Rulings["f_00000000"].Text)
	_, kept := st.Rulings["f_ffffffff"]
	assert.False(t, kept, "a new fingerprint past MaxRulings is dropped")
}

func TestStore_EscalatedIsSticky(t *testing.T) {
	s := NewStore(time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	require.True(t, s.ApplyReview(sess.ID, ReviewUpdate{Escalated: true}))
	require.True(t, s.ApplyReview(sess.ID, ReviewUpdate{}))
	st, _ := s.ReviewState(sess.ID)
	assert.True(t, st.Escalated)
}

func TestStore_RecordIssuedIDs(t *testing.T) {
	s := NewStore(time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	require.True(t, s.RecordIssuedIDs(sess.ID, []string{"f_0123abcd", "f_0123abcd-2"}))
	require.True(t, s.RecordIssuedIDs(sess.ID, []string{"f_89abcdef"}))
	st, _ := s.ReviewState(sess.ID)
	assert.Equal(t, map[string]bool{"f_0123abcd": true, "f_0123abcd-2": true, "f_89abcdef": true}, st.IssuedIDs)
}

func TestStore_ApplyReviewConcurrentCallsKeepEachOthersRulings(t *testing.T) {
	s := NewStore(time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fp := fmt.Sprintf("f_%08x", i)
			s.ApplyReview(sess.ID, ReviewUpdate{
				IssuedIDs: []string{fp},
				Rulings:   map[string]Ruling{fp: {ID: fp, Text: "r"}},
			})
		}(i)
	}
	wg.Wait()

	st, _ := s.ReviewState(sess.ID)
	assert.Len(t, st.Rulings, 20)
	assert.Len(t, st.IssuedIDs, 20)
}

func TestStore_ExpiresAt(t *testing.T) {
	s := NewStore(time.Hour)
	sess := s.Create(TaskSpec{Title: "t"}, "")
	exp, ok := s.ExpiresAt(sess.ID)
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Hour), exp, 5*time.Second)
}
