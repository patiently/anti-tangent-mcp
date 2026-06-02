package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
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
