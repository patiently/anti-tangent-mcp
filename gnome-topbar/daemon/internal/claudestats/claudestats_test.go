package claudestats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadParsesValidFile(t *testing.T) {
	s := Read("testdata")
	if !s.Present {
		t.Fatal("Present = false, want true for a readable claude-stats.json")
	}
	if s.SchemaVersion != "1.1" {
		t.Errorf("SchemaVersion = %q, want %q", s.SchemaVersion, "1.1")
	}
	if len(s.Accounts) != 2 {
		t.Fatalf("len(Accounts) = %d, want 2", len(s.Accounts))
	}
	def, ok := s.Accounts["default"]
	if !ok {
		t.Fatal("missing 'default' account")
	}
	if def.Limits == nil || def.Limits.FiveHour == nil || def.Limits.FiveHour.Utilization == nil {
		t.Fatal("default.Limits.FiveHour.Utilization should be present")
	}
	if got := *def.Limits.FiveHour.Utilization; got != 4.0 {
		t.Errorf("default five_hour utilization = %v, want 4.0", got)
	}
	if def.Week == nil || def.Week.CostUSD != 84.10 {
		t.Errorf("default.Week.CostUSD = %v, want 84.10", def.Week)
	}
	if s.Totals.WeekCostUSD != 86.02 {
		t.Errorf("Totals.WeekCostUSD = %v, want 86.02", s.Totals.WeekCostUSD)
	}
}

func TestReadParsesFractionalResetTimestamp(t *testing.T) {
	s := Read("testdata")
	def := s.Accounts["default"]
	if def.Limits.FiveHour.ResetsAt == nil {
		t.Fatal("five_hour.resets_at should parse")
	}
	want := time.Date(2026, 6, 3, 11, 40, 0, 516530000, time.UTC)
	if !def.Limits.FiveHour.ResetsAt.Equal(want) {
		t.Errorf("five_hour resets_at = %v, want %v", def.Limits.FiveHour.ResetsAt, want)
	}
}

func TestReadNullableWindowsAndLimitError(t *testing.T) {
	s := Read("testdata")
	alt, ok := s.Accounts["alt"]
	if !ok {
		t.Fatal("missing 'alt' account")
	}
	if alt.Limits == nil {
		t.Fatal("alt.Limits should be present even on fetch error")
	}
	if alt.Limits.Error == nil || *alt.Limits.Error != "usage endpoint HTTP 401" {
		t.Errorf("alt.Limits.Error = %v, want 'usage endpoint HTTP 401'", alt.Limits.Error)
	}
	if alt.Limits.FiveHour != nil || alt.Limits.SevenDay != nil {
		t.Error("alt windows should be nil when the limit fetch failed")
	}
	// Cost still populates independently of the limit fetch.
	if alt.Week == nil || alt.Week.CostUSD != 1.92 {
		t.Errorf("alt.Week.CostUSD = %v, want 1.92", alt.Week)
	}
	// Optional per-model window absent in the producer output stays nil.
	def := s.Accounts["default"]
	if def.Limits.SevenDayOpus != nil {
		t.Error("default seven_day_opus should be nil (null in fixture)")
	}
}

func TestReadAbsentOrUnreadableIsNotPresent(t *testing.T) {
	if s := Read(t.TempDir()); s.Present {
		t.Error("absent file: Present = true, want false")
	}
	if s := Read(""); s.Present {
		t.Error("empty dir: Present = true, want false")
	}
	// Corrupt file → Present=false, no panic.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude-stats.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Read(dir); s.Present {
		t.Error("corrupt file: Present = true, want false")
	}
}

func TestStale(t *testing.T) {
	base := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	fresh := Stats{Present: true, GeneratedAt: base.Add(-2 * time.Minute)}
	if fresh.Stale(base) {
		t.Error("2-minute-old snapshot should not be stale")
	}
	old := Stats{Present: true, GeneratedAt: base.Add(-15 * time.Minute)}
	if !old.Stale(base) {
		t.Error("15-minute-old snapshot should be stale")
	}
	absent := Stats{Present: false}
	if absent.Stale(base) {
		t.Error("absent snapshot is not 'stale' (nothing rendered)")
	}
}
