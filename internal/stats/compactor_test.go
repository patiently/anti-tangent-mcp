package stats

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

type fakeReviewer struct {
	resp    providers.Response
	err     error
	lastReq providers.Request // captured so tests can assert MaxTokens/JSONSchema
}

func (f *fakeReviewer) Name() string { return "fake" }
func (f *fakeReviewer) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	f.lastReq = req
	return f.resp, f.err
}

func sampleEvents(base time.Time) []Event {
	return []Event{
		{Ts: base, Tool: "validate_task_spec", Verdict: "pass", ReviewMS: 100, Model: "anthropic:m"},
		{Ts: base.Add(time.Minute), Tool: "validate_completion", Verdict: "warn", FindingsTotal: 1,
			SeverityCounts: map[string]int{"major": 1}, ReviewMS: 200, Model: "anthropic:m"},
	}
}

func TestCompactWritesRollupAndSummary(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	fr := &fakeReviewer{resp: providers.Response{RawJSON: []byte(`{"summary":"All green. 2 calls."}`)}}
	c := &Compactor{
		dir:       dir,
		reviewer:  fr,
		model:     "anthropic:m",
		maxTokens: 2048,
		timeout:   5 * time.Second,
		logger:    slog.Default(),
	}
	c.Compact(now, sampleEvents(now), nil, nil)

	// The summary call must use the configured StatsMaxTokens and a JSONSchema
	// (all providers force JSON output, so the schema is mandatory).
	if fr.lastReq.MaxTokens != 2048 {
		t.Errorf("reviewer MaxTokens = %d, want 2048 (StatsMaxTokens)", fr.lastReq.MaxTokens)
	}
	if len(fr.lastReq.JSONSchema) == 0 {
		t.Error("reviewer request must carry a JSONSchema")
	}

	// rollup.json present + parseable + correct count.
	rb, err := os.ReadFile(filepath.Join(dir, rollupFile))
	if err != nil {
		t.Fatalf("rollup.json: %v", err)
	}
	var r Rollup
	if err := json.Unmarshal(rb, &r); err != nil {
		t.Fatalf("rollup unmarshal: %v", err)
	}
	if r.TotalCalls != 2 {
		t.Errorf("TotalCalls = %d, want 2", r.TotalCalls)
	}

	// summary.md present with the canned text.
	sb, err := os.ReadFile(filepath.Join(dir, summaryMDFile))
	if err != nil {
		t.Fatalf("summary.md: %v", err)
	}
	if string(sb) != "All green. 2 calls." {
		t.Errorf("summary.md = %q", string(sb))
	}

	// summaries.jsonl has one entry.
	recs, err := readJSONL[summaryRecord](dir, summariesFile)
	if err != nil || len(recs) != 1 {
		t.Fatalf("summaries.jsonl: recs=%d err=%v", len(recs), err)
	}

	// CodeScene block: present only when codescene events are passed.
	csNow := now
	c.Compact(csNow, sampleEvents(csNow), []CodesceneEvent{
		{Ts: csNow, Digest: codescene.Digest{Tool: "analyze_change_set", QualityGate: "failed", NetPP: -0.3, Trend: "regression"}},
	}, nil)
	rb2, _ := os.ReadFile(filepath.Join(dir, rollupFile))
	var r2 Rollup
	if err := json.Unmarshal(rb2, &r2); err != nil {
		t.Fatalf("rollup2 unmarshal: %v", err)
	}
	if r2.Codescene == nil || r2.Codescene.Runs != 1 || r2.Codescene.LatestTrend != "regression" {
		t.Errorf("codescene block = %+v", r2.Codescene)
	}
}

func TestCompactReviewerErrorSkipsSummary(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	c := &Compactor{
		dir:      dir,
		reviewer: &fakeReviewer{err: context.DeadlineExceeded},
		model:    "anthropic:m", maxTokens: 2048, timeout: time.Second, logger: slog.Default(),
	}
	c.Compact(now, sampleEvents(now), nil, nil)

	if _, err := os.Stat(filepath.Join(dir, rollupFile)); err != nil {
		t.Errorf("rollup.json should exist even on reviewer error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, summaryMDFile)); !os.IsNotExist(err) {
		t.Errorf("summary.md should be absent on reviewer error, stat err = %v", err)
	}
}

func TestCompactNilReviewerWritesRollupOnly(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	c := &Compactor{dir: dir, reviewer: nil, logger: slog.Default()}
	c.Compact(now, sampleEvents(now), nil, nil)
	if _, err := os.Stat(filepath.Join(dir, rollupFile)); err != nil {
		t.Errorf("rollup.json should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, summaryMDFile)); !os.IsNotExist(err) {
		t.Errorf("summary.md should be absent with nil reviewer, stat err = %v", err)
	}
}

func leadSignals() []scorecard.Signal {
	return []scorecard.Signal{
		{ID: "test_volume_drift:session/claude-sonnet-5-5/gpt-x", Kind: scorecard.RevisitTestVolume, Direction: "up",
			Cohort: &scorecard.CohortKey{Kind: "session", ImplementerModel: "claude-sonnet-5-5", ReviewModel: "gpt-x"}},
		{ID: "new_model:claude-haiku-5-5", Kind: scorecard.RevisitNewModel, Model: "claude-haiku-5-5"},
	}
}

func TestCompactWritesRevisitLead(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	fr := &fakeReviewer{resp: providers.Response{RawJSON: []byte(`{"summary":"All green."}`)}}
	c := &Compactor{dir: dir, reviewer: fr, model: "anthropic:m", maxTokens: 2048, timeout: 5 * time.Second, logger: slog.Default()}
	sigs := leadSignals()
	sc := &scorecard.Scorecard{Revisit: &sigs}
	c.Compact(now, sampleEvents(now), nil, sc)

	sb, err := os.ReadFile(filepath.Join(dir, summaryMDFile))
	if err != nil {
		t.Fatalf("summary.md: %v", err)
	}
	want := "Cycle review due: 2 signals — test_volume_drift up (session · claude-sonnet-5-5 / gpt-x); new model claude-haiku-5-5\n\nAll green."
	if string(sb) != want {
		t.Errorf("summary.md = %q\nwant %q", string(sb), want)
	}
	recs, err := readJSONL[summaryRecord](dir, summariesFile)
	if err != nil || len(recs) != 1 {
		t.Fatalf("summaries.jsonl: recs=%d err=%v", len(recs), err)
	}
	if recs[0].Text != "All green." {
		t.Errorf("summaries.jsonl text = %q, want LLM text alone", recs[0].Text)
	}
}

func TestCompactWithoutSignalsWritesLLMTextOnly(t *testing.T) {
	for _, sc := range []*scorecard.Scorecard{nil, {}} {
		dir := t.TempDir()
		now := time.Unix(1700000000, 0).UTC()
		fr := &fakeReviewer{resp: providers.Response{RawJSON: []byte(`{"summary":"All green."}`)}}
		c := &Compactor{dir: dir, reviewer: fr, model: "anthropic:m", maxTokens: 2048, timeout: 5 * time.Second, logger: slog.Default()}
		c.Compact(now, sampleEvents(now), nil, sc)
		sb, err := os.ReadFile(filepath.Join(dir, summaryMDFile))
		if err != nil {
			t.Fatalf("summary.md: %v", err)
		}
		if string(sb) != "All green." {
			t.Errorf("summary.md = %q, want exactly the LLM text", string(sb))
		}
	}
}

func TestRevisitLeadCapsAtThree(t *testing.T) {
	var sigs []scorecard.Signal
	for i := 0; i < 5; i++ {
		sigs = append(sigs, scorecard.Signal{Kind: scorecard.RevisitNewModel, Model: "m"})
	}
	got := revisitLead(sigs)
	if !strings.HasSuffix(got, "; +2 more") {
		t.Errorf("revisitLead = %q, want suffix %q", got, "; +2 more")
	}
	if n := strings.Count(got, "new model m"); n != 3 {
		t.Errorf("rendered %d items, want 3: %q", n, got)
	}
}

func TestRevisitItemWithoutCohortFallsBackToKind(t *testing.T) {
	if got := revisitItem(scorecard.Signal{Kind: scorecard.RevisitUnconfirmed}); got != string(scorecard.RevisitUnconfirmed) {
		t.Errorf("revisitItem = %q", got)
	}
}

func TestRevisitLeadUnconfirmedSource(t *testing.T) {
	got := revisitItem(scorecard.Signal{Kind: scorecard.RevisitUnconfirmed, Direction: "down", Source: "codescene",
		Cohort: &scorecard.CohortKey{Kind: "session", ImplementerModel: "i", ReviewModel: "r"}})
	want := "unconfirmed_flags_drift down (session · i / r · codescene)"
	if got != want {
		t.Errorf("revisitItem = %q, want %q", got, want)
	}
}

func TestRevisitLeadEmpty(t *testing.T) {
	if got := revisitLead(nil); got != "" {
		t.Errorf("revisitLead(nil) = %q, want empty", got)
	}
}

func TestReadPrevSummaryStripsLead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, summaryMDFile), []byte("Cycle review due: 1 signal — new model m\n\nAll green."), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Compactor{dir: dir}
	if got := c.readPrevSummary(); got != "All green." {
		t.Errorf("readPrevSummary = %q, want %q", got, "All green.")
	}
}

func TestSummaryPromptRevisitBlock(t *testing.T) {
	r := computeRollup(sampleEvents(time.Unix(1700000000, 0).UTC()), time.Unix(1700000000, 0).UTC())
	sigs := leadSignals()
	withSigs := buildSummaryPrompt(r, "", &scorecard.Scorecard{Revisit: &sigs})
	if !strings.Contains(withSigs, "Revisit signals") {
		t.Errorf("prompt with signals lacks revisit block:\n%s", withSigs)
	}
	without := buildSummaryPrompt(r, "", &scorecard.Scorecard{})
	if strings.Contains(without, "Revisit signals") {
		t.Errorf("prompt without signals carries revisit block")
	}
}
