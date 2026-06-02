package mcpsrv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
)

func TestValidateTaskSpecRecordsStats(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}

	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": rv},
		Stats:    rec,
	}}

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "Add healthz",
		Goal:               "Expose a liveness probe",
		AcceptanceCriteria: []string{"GET /healthz returns 200 ok"},
	})
	if err != nil {
		t.Fatalf("ValidateTaskSpec: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("events.jsonl: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("expected one event recorded, file is empty")
	}
}

// TestValidatePlanRecordsStats verifies that ValidatePlan with a Stats recorder
// writes exactly one event to events.jsonl, and that the event's Tool is
// "validate_plan" and its Verdict matches the plan-level verdict.
func TestValidatePlanRecordsStats(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 10000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}

	// Single-task plan so the single-call path fires (< default chunkSize=8).
	plan := buildPlanWithNTasks(1)
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"plan_verdict":"pass","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: t1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Proceed."}`),
			Model:   "claude-sonnet-4-6",
		},
	}
	cfg, cfgErr := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	if cfgErr != nil {
		t.Fatalf("config.Load: %v", cfgErr)
	}

	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     rec,
		planCache: newPlanPassCache(),
	}}

	_, pr, callErr := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	if callErr != nil {
		t.Fatalf("ValidatePlan: %v", callErr)
	}

	// Read and decode events.jsonl.
	b, readErr := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if readErr != nil {
		t.Fatalf("events.jsonl: %v", readErr)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 event line, got %d: %q", len(lines), string(b))
	}

	var ev stats.Event
	if jsonErr := json.Unmarshal([]byte(lines[0]), &ev); jsonErr != nil {
		t.Fatalf("unmarshal event: %v", jsonErr)
	}
	if ev.Tool != "validate_plan" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "validate_plan")
	}
	if ev.Verdict == "" {
		t.Error("event.Verdict must be non-empty")
	}
	if ev.Verdict != string(pr.PlanVerdict) {
		t.Errorf("event.Verdict = %q, want %q (plan verdict)", ev.Verdict, string(pr.PlanVerdict))
	}
}

// TestValidateTaskSpec_PartialRecoveryRecordsStat verifies that a
// truncation-recovered ValidateTaskSpec review records exactly one stat event
// with Partial=true, so partial_rate in the rollup is accurate.
func TestValidateTaskSpec_PartialRecoveryRecordsStat(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}

	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// Reviewer returns partial JSON (one complete finding + truncation mid-second).
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"major","category":"other","criterion":"ac1","evidence":"e1","suggestion":"s1"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": rv},
		Stats:    rec,
	}}

	_, env, callErr := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if callErr != nil {
		t.Fatalf("ValidateTaskSpec: %v", callErr)
	}
	if !env.Partial {
		t.Fatal("expected envelope.Partial=true on truncation recovery")
	}

	b, readErr := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if readErr != nil {
		t.Fatalf("events.jsonl: %v", readErr)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 stat event, got %d: %q", len(lines), string(b))
	}

	var ev stats.Event
	if jsonErr := json.Unmarshal([]byte(lines[0]), &ev); jsonErr != nil {
		t.Fatalf("unmarshal event: %v", jsonErr)
	}
	if ev.Tool != "validate_task_spec" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "validate_task_spec")
	}
	if !ev.Partial {
		t.Errorf("event.Partial = false, want true; partial_rate would remain silently 0")
	}
}

// newStatsRecorder builds a no-reviewer recorder writing into dir. Shared by the
// early-return recording tests below.
func newStatsRecorder(t *testing.T, dir string) *stats.Recorder {
	t.Helper()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 100000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}
	return rec
}

// readSingleEvent asserts events.jsonl holds exactly one record and returns it.
func readSingleEvent(t *testing.T, dir string) stats.Event {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("events.jsonl: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("events.jsonl is empty, expected exactly 1 event")
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 event line, got %d: %q", len(lines), string(b))
	}
	var ev stats.Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return ev
}

func statsTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// TestCheckProgressSessionMissingRecordsStat pins that a structured early-return
// (session not found) still lands exactly one events.jsonl record. Before the
// fix, recordStat only fired on the reviewer-result / truncation paths, so
// session-missing exits silently undercounted hook calls.
func TestCheckProgressSessionMissingRecordsStat(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}},
		Stats:    newStatsRecorder(t, dir),
	}}

	_, _, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: "does-not-exist", WorkingOn: "stuff",
	})
	if err != nil {
		t.Fatalf("CheckProgress: %v", err)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "check_progress" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "check_progress")
	}
	if ev.Verdict != "fail" {
		t.Errorf("event.Verdict = %q, want %q", ev.Verdict, "fail")
	}
}

// TestValidatePlanNoHeadingsRecordsStat pins that the no-headings early return
// (zero `### Task N:` headings) records one validate_plan event.
func TestValidatePlanNoHeadingsRecordsStat(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}},
		Stats:     newStatsRecorder(t, dir),
		planCache: newPlanPassCache(),
	}}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "no task headings here, just prose"})
	if err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "validate_plan" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "validate_plan")
	}
	if ev.Verdict != "fail" {
		t.Errorf("event.Verdict = %q, want %q", ev.Verdict, "fail")
	}
}

// TestExtractEmptyEnvelopesRecordsStat pins that the empty-completion_envelopes
// refusal records one extract event carrying the verdict (previously the
// success-only recordStat skipped this path AND omitted the verdict).
func TestExtractEmptyEnvelopesRecordsStat(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}},
		Stats:    newStatsRecorder(t, dir),
	}}

	_, _, err := h.ExtractProjectKnowledge(context.Background(), nil, ExtractProjectKnowledgeArgs{})
	if err != nil {
		t.Fatalf("ExtractProjectKnowledge: %v", err)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "extract_project_knowledge" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "extract_project_knowledge")
	}
	if ev.Verdict != "fail" {
		t.Errorf("event.Verdict = %q, want %q (verdict must be populated)", ev.Verdict, "fail")
	}
}

// TestPrimeTruncationRecordsStat pins that a truncated prime review records one
// event carrying the warn verdict — the non-success structured path that the
// old success-only recordStat skipped.
func TestPrimeTruncationRecordsStat(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{RawJSON: []byte(`{"picks":[`), Model: "claude-sonnet-4-6"},
		err:  providers.ErrResponseTruncated,
	}
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": rv},
		Stats:    newStatsRecorder(t, dir),
	}}

	_, _, err := h.PrimeProjectKnowledge(context.Background(), nil, PrimeProjectKnowledgeArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("PrimeProjectKnowledge: %v", err)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "prime_project_knowledge" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "prime_project_knowledge")
	}
	if ev.Verdict != "warn" {
		t.Errorf("event.Verdict = %q, want %q", ev.Verdict, "warn")
	}
}

func TestNilStatsDisabledNoFiles(t *testing.T) {
	// Nil Stats must not panic or error; there is no stats dir to check.
	cfg, _ := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: Deps{
		Cfg: cfg, Sessions: session.NewStore(cfg.SessionTTL),
		Reviews: providers.Registry{"anthropic": rv}, Stats: nil,
	}}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"a"},
	})
	if err != nil {
		t.Fatalf("ValidateTaskSpec: %v", err)
	}
}
