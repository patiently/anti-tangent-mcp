package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type Store struct {
	path string
	mu   sync.Mutex
	seen map[string]bool
}

func LoadStore(path string) (*Store, error) {
	s := &Store{path: path, seen: map[string]bool{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
		s.seen[id] = true
	}
	return s, nil
}

func (s *Store) IsNew(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.seen[id]
}

func (s *Store) MarkSeen(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Persist the would-be set atomically and only commit it to the in-memory
	// map after the write succeeds — so a failed write never leaves memory and
	// disk inconsistent (which would suppress events that were never durably
	// acked).
	next := make(map[string]bool, len(s.seen)+len(ids))
	for id := range s.seen {
		next[id] = true
	}
	for _, id := range ids {
		next[id] = true
	}
	if err := s.persistSet(next); err != nil {
		return err
	}
	s.seen = next
	return nil
}

// persistSet writes the given set to disk atomically (temp file + rename) so a
// crash mid-write can't leave a truncated seen.json.
func (s *Store) persistSet(set map[string]bool) error {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids) // stable on-disk order
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".seen-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}
