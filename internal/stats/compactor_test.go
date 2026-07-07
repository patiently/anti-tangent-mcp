package stats

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
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
	c.Compact(now, sampleEvents(now), nil)

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
		{Ts: csNow, Tool: "analyze_change_set", QualityGate: "failed", NetPP: -0.3, Trend: "regression"},
	})
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
	c.Compact(now, sampleEvents(now), nil)

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
	c.Compact(now, sampleEvents(now), nil)
	if _, err := os.Stat(filepath.Join(dir, rollupFile)); err != nil {
		t.Errorf("rollup.json should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, summaryMDFile)); !os.IsNotExist(err) {
		t.Errorf("summary.md should be absent with nil reviewer, stat err = %v", err)
	}
}
