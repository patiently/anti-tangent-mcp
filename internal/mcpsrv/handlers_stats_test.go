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
