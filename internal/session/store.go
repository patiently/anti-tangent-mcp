package session

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
}

func NewStore(ttl time.Duration) *Store {
	return &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
}

func (s *Store) TTL() time.Duration { return s.ttl }

func (s *Store) Create(spec TaskSpec) *Session {
	now := time.Now()
	sess := &Session{
		ID:           uuid.NewString(),
		CreatedAt:    now,
		LastAccessed: now,
		Spec:         spec,
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}

func (s *Store) Get(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	sess.LastAccessed = time.Now()
	return sess, true
}

func (s *Store) AppendCheckpoint(id string, cp Checkpoint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	sess.Checkpoints = append(sess.Checkpoints, cp)
	sess.LastAccessed = time.Now()
	return true
}

func (s *Store) SetPreFindings(id string, findings []verdict.Finding) bool {
	return s.setFindings(id, findings, true)
}

func (s *Store) SetPostFindings(id string, findings []verdict.Finding) bool {
	return s.setFindings(id, findings, false)
}

func (s *Store) setFindings(id string, findings []verdict.Finding, pre bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	if pre {
		sess.PreFindings = findings
	} else {
		sess.PostFindings = findings
	}
	sess.LastAccessed = time.Now()
	return true
}

// EvictExpired removes sessions whose LastAccessed is older than now - ttl.
// Returns the number of sessions evicted. Intended to be called periodically
// from a background goroutine.
func (s *Store) EvictExpired(now time.Time) int {
	cutoff := now.Add(-s.ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	evicted := 0
	for id, sess := range s.sessions {
		if sess.LastAccessed.Before(cutoff) {
			delete(s.sessions, id)
			evicted++
		}
	}
	return evicted
}
