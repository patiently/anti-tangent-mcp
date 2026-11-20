package session

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
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

func (s *Store) Create(spec TaskSpec, planRunID string) *Session {
	now := time.Now()
	sess := &Session{
		ID:           uuid.NewString(),
		CreatedAt:    now,
		LastAccessed: now,
		Spec:         spec,
		PlanRunID:    planRunID,
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
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	sess.PreFindings = findings
	sess.LastAccessed = time.Now()
	return true
}

// UpdateRow applies fn to the session's own task row under the store lock and
// returns a deep copy of the result, so the caller can marshal it unlocked.
// firstWrite is true exactly once per session, on the first update that leaves
// the row with a post verdict: the caller that must write the run header.
func (s *Store) UpdateRow(id string, fn func(*planrun.TaskRow)) (row planrun.TaskRow, firstWrite bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, found := s.sessions[id]
	if !found {
		return planrun.TaskRow{}, false, false
	}
	fn(&sess.Row)
	sess.Row.Index = 1
	sess.LastAccessed = time.Now()
	if sess.Row.PostVerdict != "" && !sess.RunHeaderWritten {
		sess.RunHeaderWritten = true
		firstWrite = true
	}
	return planrun.CloneRow(sess.Row), firstWrite, true
}

// ReleaseRunHeader undoes the claim UpdateRow made with firstWrite, so the
// next update that leaves a post verdict claims the header again. A caller
// whose header append failed calls it.
func (s *Store) ReleaseRunHeader(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		sess.RunHeaderWritten = false
	}
}

func (s *Store) SetImplementerModel(id, model string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	sess.ImplementerModel = model
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

// ReviewState is a copy of what validate_completion and check_progress read
// from a session before their review. Handlers read a live *Session without
// the store's lock, and concurrent calls on one session write these fields
// (AppendCheckpoint, SetPreFindings, ApplyReview), so PriorFindings,
// IssuedIDs, Rulings, Escalated, PreFindings and CheckpointFindings are all
// handed out only as copies — a handler must never read the equivalent field
// off the live *Session.
type ReviewState struct {
	PriorFindings []verdict.Finding
	IssuedIDs     map[string]bool
	Rulings       map[string]Ruling
	Escalated     bool
	// PreFindings is a copy of the session's pre-task findings.
	PreFindings []verdict.Finding
	// CheckpointFindings is one copy of Findings per checkpoint, in order.
	CheckpointFindings [][]verdict.Finding
	// CodesceneSkipReported mirrors Session.CodesceneSkipReported.
	CodesceneSkipReported bool
	// OverBuildingAnswered mirrors Session.OverBuildingAnswered.
	OverBuildingAnswered bool
	// CodesceneEventKey mirrors Session.CodesceneEventKey.
	CodesceneEventKey string
}

// ReviewState returns a copy of the session's review state.
func (s *Store) ReviewState(id string) (ReviewState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return ReviewState{}, false
	}
	sess.LastAccessed = time.Now()
	st := ReviewState{
		PriorFindings:      append([]verdict.Finding(nil), sess.PostFindings...),
		IssuedIDs:          make(map[string]bool, len(sess.IssuedIDs)),
		Rulings:            make(map[string]Ruling, len(sess.Rulings)),
		Escalated:          sess.Escalated,
		PreFindings:        append([]verdict.Finding(nil), sess.PreFindings...),
		CheckpointFindings: make([][]verdict.Finding, len(sess.Checkpoints)),

		CodesceneSkipReported: sess.CodesceneSkipReported,
		OverBuildingAnswered:  sess.OverBuildingAnswered,
		CodesceneEventKey:     sess.CodesceneEventKey,
	}
	for k := range sess.IssuedIDs {
		st.IssuedIDs[k] = true
	}
	for k, v := range sess.Rulings {
		st.Rulings[k] = v
	}
	for i, cp := range sess.Checkpoints {
		st.CheckpointFindings[i] = append([]verdict.Finding(nil), cp.Findings...)
	}
	return st, true
}

// ReviewUpdate is what one validate_completion writes back to its session.
type ReviewUpdate struct {
	// ReplacePrior replaces the stored prior findings with PriorFindings. A
	// truncated review leaves it false: its findings are incomplete.
	ReplacePrior  bool
	PriorFindings []verdict.Finding
	IssuedIDs     []string
	// Rulings is keyed by fingerprint. Each replaces a stored ruling on the
	// same fingerprint; a new fingerprint is added only while the session holds
	// fewer than MaxRulings.
	Rulings   map[string]Ruling
	Escalated bool
	// CodesceneSkipReported sets the session's flag; false leaves it as it is.
	CodesceneSkipReported bool
	// OverBuildingAnswered sets the session's flag; false leaves it as it is.
	OverBuildingAnswered bool
	// CodesceneEventKey replaces the session's key; "" leaves it as it is.
	CodesceneEventKey string
}

// ApplyReview merges u into the session under one lock, into the session as
// it is now rather than the copy the caller read before its review, so two
// concurrent calls on one session keep each other's writes.
func (s *Store) ApplyReview(id string, u ReviewUpdate) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	if u.ReplacePrior {
		sess.PostFindings = append([]verdict.Finding{}, u.PriorFindings...)
	}
	addIssuedIDs(sess, u.IssuedIDs)
	fingerprints := make([]string, 0, len(u.Rulings))
	for fp := range u.Rulings {
		fingerprints = append(fingerprints, fp)
	}
	// Sorted, so which rulings a full session drops does not depend on map
	// iteration order.
	sort.Strings(fingerprints)
	for _, fp := range fingerprints {
		if sess.Rulings == nil {
			sess.Rulings = map[string]Ruling{}
		}
		if _, exists := sess.Rulings[fp]; !exists && len(sess.Rulings) >= MaxRulings {
			continue
		}
		sess.Rulings[fp] = u.Rulings[fp]
	}
	if u.Escalated {
		sess.Escalated = true
	}
	if u.CodesceneSkipReported {
		sess.CodesceneSkipReported = true
	}
	if u.OverBuildingAnswered {
		sess.OverBuildingAnswered = true
	}
	if u.CodesceneEventKey != "" {
		sess.CodesceneEventKey = u.CodesceneEventKey
	}
	sess.LastAccessed = time.Now()
	return true
}

// RecordIssuedIDs adds ids to the session's issued-ID set.
func (s *Store) RecordIssuedIDs(id string, ids []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	addIssuedIDs(sess, ids)
	sess.LastAccessed = time.Now()
	return true
}

func addIssuedIDs(sess *Session, ids []string) {
	if len(ids) == 0 {
		return
	}
	if sess.IssuedIDs == nil {
		sess.IssuedIDs = make(map[string]bool, len(ids))
	}
	for _, id := range ids {
		sess.IssuedIDs[id] = true
	}
}

// ExpiresAt is when the session expires if nothing touches it again. It reads
// LastAccessed under the store's lock, which a handler holding the live
// *Session cannot do while another call on the same session writes it.
func (s *Store) ExpiresAt(id string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok {
		return time.Time{}, false
	}
	return sess.LastAccessed.Add(s.ttl), true
}
