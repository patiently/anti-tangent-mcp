package stats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := State{
		LastSummaryAt:      time.Unix(1700000000, 0).UTC(),
		EventsSinceSummary: 7,
		Salt:               "deadbeef",
	}
	if err := saveState(dir, want); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	got := loadState(dir)
	if !got.LastSummaryAt.Equal(want.LastSummaryAt) || got.EventsSinceSummary != 7 || got.Salt != "deadbeef" {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLoadStateMissingGeneratesSalt(t *testing.T) {
	st := loadState(t.TempDir())
	if st.Salt == "" {
		t.Fatal("expected a generated salt for a missing state file")
	}
	if !st.LastSummaryAt.IsZero() || st.EventsSinceSummary != 0 {
		t.Fatalf("expected zero cadence, got %+v", st)
	}
}

func TestLoadStateCorruptFileGeneratesSalt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st := loadState(dir)
	if st.Salt == "" {
		t.Fatal("expected a generated salt for a corrupt state file")
	}
}

func TestLoadStateEmptySaltGeneratesSalt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte(`{"salt":""}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st := loadState(dir)
	if st.Salt == "" {
		t.Fatal("expected a generated salt when stored salt is empty")
	}
}

func TestDue(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	cases := []struct {
		name  string
		since int
		last  time.Time
		want  bool
	}{
		{"threshold hit", 50, base, true},
		{"under both", 10, base, false},
		{"interval elapsed", 0, base.Add(-25 * time.Hour), true},
		{"interval not elapsed", 0, base.Add(-1 * time.Hour), false},
	}
	for _, c := range cases {
		got := due(base, State{LastSummaryAt: c.last, EventsSinceSummary: c.since}, 24*time.Hour, 50)
		if got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
}
