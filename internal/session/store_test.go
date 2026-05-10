package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestStore_CreateAndGet(t *testing.T) {
	s := NewStore(1 * time.Hour)
	sess := s.Create(TaskSpec{Title: "t", Goal: "g"})
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
	sess := s.Create(TaskSpec{Title: "t"})

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
	sess := s.Create(TaskSpec{Title: "t"})

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
	sess := s.Create(TaskSpec{Title: "t"})
	first := sess.LastAccessed

	time.Sleep(2 * time.Millisecond)
	got, _ := s.Get(sess.ID)
	assert.True(t, got.LastAccessed.After(first))
}
