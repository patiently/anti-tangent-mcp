package stats

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

// newRunsTestRecorder is distinct from newTestRecorder (recorder_test.go):
// this package's tests need a shared dir and a configurable MinRuns, which
// that helper's (t, threshold) signature does not expose.
func newRunsTestRecorder(t *testing.T, dir string, minRuns int) *Recorder {
	t.Helper()
	r, err := New(Options{Dir: dir, SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000,
		RetentionDays: 30, MinRuns: minRuns, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunHashStableAndDistinct(t *testing.T) {
	dir := t.TempDir()
	a := newRunsTestRecorder(t, dir, 0)
	b := newRunsTestRecorder(t, dir, 0)
	h := a.RunHash("pr_0123456789ab")
	if !strings.HasPrefix(h, "r_") || len(h) != 26 || h != b.RunHash("pr_0123456789ab") {
		t.Fatalf("hash %q not stable across recorders", h)
	}
	if strings.TrimPrefix(h, "r_") == a.HashSession("pr_0123456789ab") {
		t.Fatal("run hash must not equal the session hash of the same string")
	}
	var nilRec *Recorder
	if nilRec.RunHash("x") != "" || a.RunHash("") != "" {
		t.Fatal("nil recorder or empty id must hash to empty")
	}
}

// waitForScoring blocks until RecordOutcome's async single-flight scorecard
// refresh (r.scoring) has finished. A caller's t.TempDir() cleanup runs
// RemoveAll on the recorder's directory, and that refresh's writeFileAtomic
// call creates a temp file in the same directory out-of-band from the test's
// own synchronous calls; without this wait, RemoveAll can list the
// directory's entries before the goroutine's temp file lands, then fail with
// "directory not empty" once it does. Polling the single-flight flag, not the
// scorecard file's existence, because the file can already exist from the
// test's own synchronous WriteScorecard call.
func waitForScoring(t *testing.T, r *Recorder) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for r.scoring.Load() {
		if time.Now().After(deadline) {
			t.Fatal("RecordOutcome's async scorecard refresh did not finish before the wait deadline")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRecordAndScore(t *testing.T) {
	dir := t.TempDir()
	r := newRunsTestRecorder(t, dir, 3)
	now := time.Now().UTC()
	h := r.RunHash("pr_aaaaaaaaaaaa")
	r.RecordRunLine(scorecard.RunLine{Ts: now, RunHash: h, Task: &scorecard.TaskSnapshot{Index: 1, PostVerdict: "pass",
		Calls: []scorecard.ToolCall{{Tool: "validate_completion", Model: "m", Verdict: "pass"}}}})
	r.RecordRunLine(scorecard.RunLine{Ts: now, RunHash: "r_other", Task: &scorecard.TaskSnapshot{Index: 1}})
	if err := r.RecordOutcome(scorecard.OutcomeLine{Ts: now, RunHash: h, Source: scorecard.SourceFinalReview,
		Findings: []scorecard.OutcomeFinding{{TaskIndex: 1, Severity: "major", Category: "x"}}}); err != nil {
		t.Fatal(err)
	}
	waitForScoring(t, r)
	f, _ := os.OpenFile(filepath.Join(dir, runsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()

	lines, err := r.RunLines(h)
	if err != nil || len(lines) != 1 {
		t.Fatalf("RunLines = %d lines, err %v", len(lines), err)
	}

	sc := r.WriteScorecard()
	if sc.MinRuns != 3 || sc.SkippedLines != 1 || len(sc.ByReviewModel) != 1 || sc.ByReviewModel[0].EscapeRate.Num != 1 {
		t.Fatalf("scorecard = %+v", sc)
	}
	b, err := os.ReadFile(filepath.Join(dir, scorecardFile))
	if err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	if json.Unmarshal(b, &disk) != nil || disk["min_runs"].(float64) != 3 {
		t.Fatalf("scorecard.json = %s", b)
	}
}

func TestNilRecorderRunMethods(t *testing.T) {
	var r *Recorder
	r.RecordRunLine(scorecard.RunLine{})
	if err := r.RecordOutcome(scorecard.OutcomeLine{}); err != nil {
		t.Fatal(err)
	}
}

func TestPruneRunsKeepsLiveRunsWhole(t *testing.T) {
	dir := t.TempDir()
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old, recent := cutoff.Add(-48*time.Hour), cutoff.Add(time.Hour)
	_ = rewriteJSONL(dir, runsFile, []scorecard.RunLine{
		{Ts: old, RunHash: "r_live", Header: true},
		{Ts: recent, RunHash: "r_live", Task: &scorecard.TaskSnapshot{Index: 1}},
		{Ts: old, RunHash: "r_dead", Header: true},
	})
	_ = rewriteJSONL(dir, outcomesFile, []scorecard.OutcomeLine{
		{Ts: old, RunHash: "r_live", Source: "final_review"},
		{Ts: recent, RunHash: "r_live", Source: "review_now"},
	})
	if err := pruneRuns(dir, cutoff); err != nil {
		t.Fatal(err)
	}
	if err := pruneOutcomes(dir, cutoff); err != nil {
		t.Fatal(err)
	}
	runs, _ := readJSONL[scorecard.RunLine](dir, runsFile)
	outs, _ := readJSONL[scorecard.OutcomeLine](dir, outcomesFile)
	if len(runs) != 2 || runs[0].RunHash != "r_live" || len(outs) != 1 || outs[0].Source != "review_now" {
		t.Fatalf("runs=%+v outs=%+v", runs, outs)
	}
}

func TestSummaryPromptCarriesScorecard(t *testing.T) {
	sc := &scorecard.Scorecard{ByReviewModel: []scorecard.Group{{Key: scorecard.CohortKey{ReviewModel: "openai:gpt-x"}}}}
	if p := buildSummaryPrompt(Rollup{}, "", sc); !strings.Contains(p, "openai:gpt-x") {
		t.Fatalf("prompt lacks the scorecard: %s", p)
	}
	if p := buildSummaryPrompt(Rollup{}, "", &scorecard.Scorecard{}); strings.Contains(p, "Scorecard") {
		t.Fatalf("empty scorecard must add nothing: %s", p)
	}
}
