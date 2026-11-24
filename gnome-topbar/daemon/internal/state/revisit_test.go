package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func TestRetainForgetsOnlyClearedPrefixed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seen.json")
	s, _ := LoadStore(path)
	if err := s.MarkSeen([]string{"pr:a#1", "revisit:x", "revisit:y"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Retain("revisit:", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	r, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.IsNew("pr:a#1") || r.IsNew("revisit:x") {
		t.Error("pr:a#1 and revisit:x must stay seen")
	}
	if !r.IsNew("revisit:y") {
		t.Error("revisit:y must be forgotten")
	}
}

func TestRetainNoChangeNoWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seen.json")
	s, _ := LoadStore(path)
	if err := s.MarkSeen([]string{"pr:a#1", "revisit:x"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	old := before.ModTime().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Retain("revisit:", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(old) {
		t.Error("Retain wrote the file although nothing changed")
	}
}

func TestComputeRevisitFiltersAckedAndStaleNewModel(t *testing.T) {
	store, _ := LoadStore(filepath.Join(t.TempDir(), "seen.json"))
	if err := store.MarkSeen([]string{"revisit:b"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-20 * 24 * time.Hour)
	sigs := []scorecard.Signal{
		{ID: "a", Kind: scorecard.RevisitTestVolume},
		{ID: "b", Kind: scorecard.RevisitTestVolume},
		{ID: "new_model:c", Kind: scorecard.RevisitNewModel, Since: &old},
	}
	got := ComputeRevisit(sigs, store, now)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %+v, want only a", got)
	}
}
