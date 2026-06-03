package claudestats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadParsesValidFile(t *testing.T) {
	s, err := Read("testdata")
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
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
}

func TestReadParsesFractionalResetTimestamp(t *testing.T) {
	s, _ := Read("testdata")
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
	s, _ := Read("testdata")
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
	if alt.Week == nil || alt.Week.CostUSD != 1.92 {
		t.Errorf("alt.Week.CostUSD = %v, want 1.92", alt.Week)
	}
	def := s.Accounts["default"]
	if def.Limits.SevenDayOpus != nil {
		t.Error("default seven_day_opus should be nil (null in fixture)")
	}
}

func TestReadAbsentReturnsNoError(t *testing.T) {
	// Absent file / disabled feature is the common case, not an error.
	if s, err := Read(t.TempDir()); err != nil || s.Present {
		t.Errorf("absent file: got (present=%v, err=%v), want (false, nil)", s.Present, err)
	}
	if s, err := Read(""); err != nil || s.Present {
		t.Errorf("empty dir: got (present=%v, err=%v), want (false, nil)", s.Present, err)
	}
}

func TestReadCorruptReturnsError(t *testing.T) {
	// A present-but-malformed file IS an error (diagnosable), distinct from absent.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude-stats.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Read(dir)
	if err == nil {
		t.Error("corrupt file should return an error so the failure is diagnosable")
	}
	if s.Present {
		t.Error("corrupt file: Present = true, want false")
	}
}

func TestReadUnsupportedMajorIsError(t *testing.T) {
	dir := t.TempDir()
	body := `{"schema_version":"2.0","generated_at":"2026-06-03T09:05:00Z","accounts":{}}`
	if err := os.WriteFile(filepath.Join(dir, "claude-stats.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Read(dir)
	if err == nil {
		t.Error("schema_version 2.0 should be rejected (consumer supports major 1)")
	}
	if s.Present {
		t.Error("unsupported major: Present should be false")
	}

	// A MINOR bump within major 1 stays supported.
	body11 := `{"schema_version":"1.9","generated_at":"2026-06-03T09:05:00Z","accounts":{}}`
	if err := os.WriteFile(filepath.Join(dir, "claude-stats.json"), []byte(body11), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := Read(dir); err != nil || !s.Present {
		t.Errorf("schema_version 1.9 should be accepted: got (present=%v, err=%v)", s.Present, err)
	}
}

func TestReadCapsLongErrorStrings(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("x", 5000)
	body := `{"schema_version":"1.1","generated_at":"2026-06-03T09:05:00Z","accounts":{` +
		`"a":{"error":"` + huge + `","limits":{"fetched_at":"2026-06-03T09:05:00Z","error":"` + huge + `"}}}}`
	if err := os.WriteFile(filepath.Join(dir, "claude-stats.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	a := s.Accounts["a"]
	if a.Error == nil || len([]rune(*a.Error)) > maxErrLen+1 {
		t.Errorf("account error not capped: len=%d", len([]rune(*a.Error)))
	}
	if a.Limits == nil || a.Limits.Error == nil || len([]rune(*a.Limits.Error)) > maxErrLen+1 {
		t.Errorf("limits error not capped")
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
	if (Stats{Present: false}).Stale(base) {
		t.Error("absent snapshot is not 'stale' (nothing rendered)")
	}
	// A present file with a zero/omitted generated_at must NOT read as maximally stale.
	if (Stats{Present: true}).Stale(base) {
		t.Error("zero generated_at should not be treated as stale")
	}
}
