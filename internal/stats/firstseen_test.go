package stats

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func recordTask(t *testing.T, r *Recorder, hash string, ts time.Time, impl string) {
	t.Helper()
	err := r.RecordRunLine(scorecard.RunLine{Ts: ts, RunHash: hash, Task: &scorecard.TaskSnapshot{
		Index: 1, PostVerdict: "pass", ImplementerModel: impl,
		Calls: []scorecard.ToolCall{{Tool: "validate_completion", Model: "openai:gpt-x", Verdict: "pass"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func mustRuns(t *testing.T, r *Recorder) []scorecard.RunLine {
	t.Helper()
	lines, err := readJSONL[scorecard.RunLine](r.dir, runsFile)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func readScorecardJSON(t *testing.T, dir string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, scorecardFile))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func assertRevisitKeys(t *testing.T, dir string, want bool) {
	t.Helper()
	m := readScorecardJSON(t, dir)
	for _, k := range []string{"revisit", "revisit_coverage"} {
		if _, ok := m[k]; ok != want {
			t.Fatalf("scorecard.json key %q present=%v, want %v", k, ok, want)
		}
	}
}

func signalIDs(sc scorecard.Scorecard) []string {
	ids := []string{}
	if sc.Revisit == nil {
		return ids
	}
	for _, s := range *sc.Revisit {
		ids = append(ids, s.ID)
	}
	return ids
}

// mustSeeded reads first-seen.json and fails unless it is seeded with exactly
// want models, every one at the zero time.
func mustSeeded(t *testing.T, dir string, want int) firstSeen {
	t.Helper()
	fs, err := readFirstSeen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fs.Seeded {
		t.Fatalf("not seeded: %+v", fs)
	}
	if len(fs.Models) != want {
		t.Fatalf("want %d models, got %+v", want, fs.Models)
	}
	for m, seen := range fs.Models {
		if !seen.IsZero() {
			t.Fatalf("model %q seeded with a stamp: %v", m, seen)
		}
	}
	return fs
}

func assertWarnings(t *testing.T, buf *bytes.Buffer, msg string, want int) {
	t.Helper()
	if n := strings.Count(buf.String(), msg); n != want {
		t.Fatalf("want %d %q warnings, got %d:\n%s", want, msg, n, buf.String())
	}
}

func assertSameModels(t *testing.T, got, want map[string]time.Time) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for m, w := range want {
		if g, ok := got[m]; !ok || !g.Equal(w) {
			t.Fatalf("model %q: got %v, want %v", m, g, w)
		}
	}
}

func fixedClockRecorder(t *testing.T) (*Recorder, time.Time) {
	t.Helper()
	r := newTestRecorder(t, 1000)
	now := time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	r.clock = func() time.Time { return now }
	return r, now
}

func TestWriteScorecardSeedsThenStampsNewModel(t *testing.T) {
	r, now := fixedClockRecorder(t)
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	sc := r.WriteScorecard()
	if sc.Revisit == nil || len(*sc.Revisit) != 0 {
		t.Fatalf("seeding must not signal: %+v", sc.Revisit)
	}
	fs := mustSeeded(t, r.dir, 2)
	for _, m := range scorecard.CohortModels(mustRuns(t, r), nil, now) {
		if _, ok := fs.Models[m]; !ok {
			t.Fatalf("cohort model %q not seeded: %+v", m, fs.Models)
		}
	}
	recordTask(t, r, "r2", now.Add(-time.Minute), "claude-haiku-5-5")
	sc = r.WriteScorecard()
	if ids := signalIDs(sc); !slices.Contains(ids, "new_model:claude-haiku-5-5") {
		t.Fatalf("want new_model for haiku, got %v", ids)
	}
	fs, _ = readFirstSeen(r.dir)
	if !fs.Models["claude-haiku-5-5"].Equal(now) {
		t.Fatalf("haiku not stamped with the recorder clock: %+v", fs.Models)
	}
	assertRevisitKeys(t, r.dir, true)
}

// A real clock advances between reads, so a stamp taken from a second reading
// would land after the instant Revisit judges against and be ignored.
func TestNewModelSignalledOnStampingCallWithAdvancingClock(t *testing.T) {
	r := newTestRecorder(t, 1000)
	t0 := time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var reads []time.Time
	r.clock = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		next := t0.Add(time.Duration(len(reads)) * time.Millisecond)
		reads = append(reads, next)
		return next
	}
	recordTask(t, r, "r1", t0.Add(-time.Hour), "claude-sonnet-5-5")
	r.WriteScorecard()
	recordTask(t, r, "r2", t0.Add(-time.Minute), "claude-haiku-5-5")
	before := len(reads)
	sc := r.WriteScorecard()
	if got := len(reads) - before; got != 1 {
		t.Fatalf("WriteScorecard read the clock %d times, want 1", got)
	}
	now := reads[before]
	var sig *scorecard.Signal
	for i, s := range *sc.Revisit {
		if s.ID == "new_model:claude-haiku-5-5" {
			sig = &(*sc.Revisit)[i]
		}
	}
	if sig == nil {
		t.Fatalf("stamping call did not signal haiku: %v", signalIDs(sc))
	}
	fs, _ := readFirstSeen(r.dir)
	if !fs.Models["claude-haiku-5-5"].Equal(now) || !sig.Since.Equal(now) {
		t.Fatalf("stamp %v / since %v, want the call's now %v", fs.Models["claude-haiku-5-5"], sig.Since, now)
	}
}

func TestWriteScorecardReadFailureSkipsRevisit(t *testing.T) {
	r, _ := fixedClockRecorder(t)
	if err := os.Mkdir(filepath.Join(r.dir, runsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	sc := r.WriteScorecard()
	if sc.Revisit != nil || sc.RevisitCoverage != nil {
		t.Fatalf("revisit computed despite a read failure: %+v %+v", sc.Revisit, sc.RevisitCoverage)
	}
	if _, err := os.Stat(filepath.Join(r.dir, firstSeenFile)); !os.IsNotExist(err) {
		t.Fatalf("first-seen.json touched on a read failure: %v", err)
	}
	assertRevisitKeys(t, r.dir, false)
}

func TestMergeFirstSeen(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	cases := []struct {
		name string
		a, b firstSeen
		want firstSeen
	}{
		{"zero beats a stamp",
			firstSeen{Models: map[string]time.Time{"m": {}}},
			firstSeen{Models: map[string]time.Time{"m": t1}},
			firstSeen{Models: map[string]time.Time{"m": {}}}},
		{"stamp then zero",
			firstSeen{Models: map[string]time.Time{"m": t1}},
			firstSeen{Models: map[string]time.Time{"m": {}}},
			firstSeen{Models: map[string]time.Time{"m": {}}}},
		{"earlier stamp wins",
			firstSeen{Models: map[string]time.Time{"m": t2}},
			firstSeen{Models: map[string]time.Time{"m": t1}},
			firstSeen{Models: map[string]time.Time{"m": t1}}},
		{"missing takes the other",
			firstSeen{Models: map[string]time.Time{}},
			firstSeen{Models: map[string]time.Time{"m": t2}},
			firstSeen{Models: map[string]time.Time{"m": t2}}},
		{"seeded ORs",
			firstSeen{Seeded: true},
			firstSeen{},
			firstSeen{Seeded: true, Models: map[string]time.Time{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, got := range []firstSeen{mergeFirstSeen(c.a, c.b), mergeFirstSeen(c.b, c.a)} {
				if got.Seeded != c.want.Seeded {
					t.Fatalf("seeded: got %v, want %v", got.Seeded, c.want.Seeded)
				}
				assertSameModels(t, got.Models, c.want.Models)
			}
		})
	}
}

func TestFirstSeenCorruptIsUnseeded(t *testing.T) {
	r, now := fixedClockRecorder(t)
	var buf bytes.Buffer
	r.logger = slog.New(slog.NewTextHandler(&buf, nil))
	if err := os.WriteFile(filepath.Join(r.dir, firstSeenFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	sc := r.WriteScorecard()
	if ids := signalIDs(sc); len(ids) != 0 {
		t.Fatalf("reseeding must not signal: %v", ids)
	}
	mustSeeded(t, r.dir, 2)
	assertWarnings(t, &buf, "first_seen unreadable", 1)
}

func TestFirstSeenReadErrorKeepsFileAndSkipsStep(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	r, now := fixedClockRecorder(t)
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	r.WriteScorecard()
	path := filepath.Join(r.dir, firstSeenFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	var buf bytes.Buffer
	r.logger = slog.New(slog.NewTextHandler(&buf, nil))
	recordTask(t, r, "r2", now.Add(-time.Minute), "claude-haiku-5-5")
	sc := r.WriteScorecard()
	if sc.Revisit == nil {
		t.Fatal("revisit key missing")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("first-seen.json changed: %s -> %s", before, after)
	}
	assertWarnings(t, &buf, "first_seen read failed", 1)
	assertWarnings(t, &buf, "unreadable, reseeding", 0)
}

func TestFutureLineNeitherSeedsNorSignals(t *testing.T) {
	r, now := fixedClockRecorder(t)
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	r.WriteScorecard()
	recordTask(t, r, "r2", now.Add(time.Hour), "claude-haiku-5-5")
	sc := r.WriteScorecard()
	fs, err := readFirstSeen(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.Models["claude-haiku-5-5"]; ok {
		t.Fatalf("future line seeded a model: %+v", fs.Models)
	}
	if ids := signalIDs(sc); slices.Contains(ids, "new_model:claude-haiku-5-5") {
		t.Fatalf("future line signalled: %v", ids)
	}
}

func TestWriteScorecardConcurrent(t *testing.T) {
	r, now := fixedClockRecorder(t)
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				r.WriteScorecard()
			}
		}()
	}
	wg.Wait()
	mustSeeded(t, r.dir, 2)
}

// The stats dir is made read-only so the atomic writer cannot create its temp
// file while first-seen.json itself stays readable; a directory at the target
// path would fail the read too and test reseeding instead of a write failure.
// writeFileAtomic picks a random temp name, so no single temp path can be
// blocked in advance.
func TestFirstSeenWriteFailureStillSignals(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	r, now := fixedClockRecorder(t)
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	r.WriteScorecard()
	recordTask(t, r, "r2", now.Add(-time.Minute), "claude-haiku-5-5")
	path := filepath.Join(r.dir, firstSeenFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(r.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(r.dir, 0o700) })
	var buf bytes.Buffer
	r.logger = slog.New(slog.NewTextHandler(&buf, nil))
	sc := r.WriteScorecard()
	assertWarnings(t, &buf, "first_seen write failed", 1)
	assertWarnings(t, &buf, "first_seen unreadable", 0)
	if ids := signalIDs(sc); !slices.Contains(ids, "new_model:claude-haiku-5-5") {
		t.Fatalf("want new_model for haiku despite the write failure, got %v", ids)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("first-seen.json changed:\nbefore %s\nafter  %s", before, after)
	}
}
