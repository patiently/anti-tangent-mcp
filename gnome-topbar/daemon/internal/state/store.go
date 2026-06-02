package state

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	for _, id := range ids {
		s.seen[id] = true
	}
	return s.persist()
}

func (s *Store) persist() error {
	ids := make([]string, 0, len(s.seen))
	for id := range s.seen {
		ids = append(ids, id)
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
