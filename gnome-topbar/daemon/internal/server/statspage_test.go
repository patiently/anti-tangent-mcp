package server

import (
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)

func TestRenderStatsPageCodesceneEmptyState(t *testing.T) {
	at := atstats.Stats{Present: true, TotalCalls: 10, PerTool: map[string]int{"validate_completion": 6}}
	out := renderStatsPage(at)
	if !strings.Contains(out, "anti-tangent stats") || !strings.Contains(out, "No data yet") {
		t.Fatalf("stats page missing rollup or CodeScene empty-state:\n%s", out)
	}
}

func TestRenderStatsPageCodescenePresent(t *testing.T) {
	at := atstats.Stats{Present: true, CodeScene: &atstats.CodeSceneStats{LatestScore: 8.4, LatestTrend: "regression"}}
	if !strings.Contains(renderStatsPage(at), "8.4") {
		t.Fatal("CodeScene score not rendered")
	}
}

func TestRenderClaudePageFableAndError(t *testing.T) {
	util := 69.0
	reset := time.Date(2026, 6, 8, 20, 0, 0, 0, time.UTC)
	errStr := "usage endpoint HTTP 401"
	cs := claudestats.Stats{Present: true, Accounts: map[string]claudestats.Account{
		"default": {DisplayName: "default", Limits: &claudestats.Limits{
			WeeklyModels: map[string]*claudestats.Window{"Fable": {Utilization: &util, ResetsAt: &reset}},
		}},
		"alt": {DisplayName: "alt", Limits: &claudestats.Limits{Error: &errStr}},
	}}
	out := renderClaudePage(cs, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	if !strings.Contains(out, "Fable") || !strings.Contains(out, "69%") {
		t.Errorf("Fable window not rendered:\n%s", out)
	}
	if !strings.Contains(out, "limits unavailable") {
		t.Errorf("limits error not rendered:\n%s", out)
	}
}

func TestRenderPagesAbsentAreGraceful(t *testing.T) {
	if !strings.Contains(renderStatsPage(atstats.Stats{}), "No anti-tangent stats") {
		t.Error("absent stats not graceful")
	}
	if !strings.Contains(renderClaudePage(claudestats.Stats{}, time.Now()), "No claude-stats.json") {
		t.Error("absent claude stats not graceful")
	}
}

// TestRenderStatsPageThroughputUnits pins the 0–1 → percent conversion for the
// cache-hit / partial rates (a units bug would otherwise render "0.25%").
func TestRenderStatsPageThroughputUnits(t *testing.T) {
	at := atstats.Stats{Present: true, TotalCalls: 100, CacheHitRate: 0.25, PartialRate: 0.1}
	out := renderStatsPage(at)
	if !strings.Contains(out, "25%") {
		t.Errorf("cache hit 0.25 should render 25%%:\n%s", out)
	}
	if !strings.Contains(out, "10%") {
		t.Errorf("partial 0.1 should render 10%%:\n%s", out)
	}
}

// TestRenderClaudePageUsageAndStale exercises the usage table (CostTokens) and the
// stale banner — branches the other tests leave untaken.
func TestRenderClaudePageUsageAndStale(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	cs := claudestats.Stats{
		Present:     true,
		GeneratedAt: now.Add(-15 * time.Minute), // stale (> 10m)
		Accounts: map[string]claudestats.Account{
			"default": {DisplayName: "default", Today: &claudestats.Usage{CostUSD: 12.47, TotalTokens: 4821093}},
		},
	}
	out := renderClaudePage(cs, now)
	if !strings.Contains(out, "stats stale") {
		t.Errorf("stale banner not rendered:\n%s", out)
	}
	if !strings.Contains(out, "$12.47") || !strings.Contains(out, "4.8M") {
		t.Errorf("usage row not rendered via CostTokens:\n%s", out)
	}
}

// TestRenderClaudePageHighUtilWarns confirms the web page now carries the same ⚠
// high-utilization marker as the tray (review F2).
func TestRenderClaudePageHighUtilWarns(t *testing.T) {
	util := 91.0
	reset := time.Date(2026, 6, 9, 20, 0, 0, 0, time.UTC)
	cs := claudestats.Stats{Present: true, Accounts: map[string]claudestats.Account{
		"default": {DisplayName: "default", Limits: &claudestats.Limits{
			FiveHour: &claudestats.Window{Utilization: &util, ResetsAt: &reset},
		}},
	}}
	out := renderClaudePage(cs, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	if !strings.Contains(out, "91% ⚠") {
		t.Errorf("high-utilization ⚠ marker missing on /ui/claude:\n%s", out)
	}
}
