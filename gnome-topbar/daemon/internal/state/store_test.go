package state

import (
	"path/filepath"
	"testing"
)

func TestStoreSeenAckRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seen.json")
	s, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsNew("pr:o/r#1") {
		t.Fatal("expected new before ack")
	}
	if err := s.MarkSeen([]string{"pr:o/r#1"}); err != nil {
		t.Fatal(err)
	}
	if s.IsNew("pr:o/r#1") {
		t.Fatal("expected not-new after ack")
	}
	// reload persists
	s2, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.IsNew("pr:o/r#1") {
		t.Fatal("ack did not persist across reload")
	}
}
