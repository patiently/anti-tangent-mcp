package config_test

import (
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

func envWith(extra map[string]string) func(string) string {
	return func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "test-key"
		}
		return extra[k]
	}
}

func TestStatsDefaults(t *testing.T) {
	cfg, err := config.Load(envWith(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StatsDir != "" {
		t.Errorf("StatsDir = %q, want empty (disabled)", cfg.StatsDir)
	}
	if cfg.StatsModel != cfg.MidModel {
		t.Errorf("StatsModel = %v, want MidModel %v", cfg.StatsModel, cfg.MidModel)
	}
	if cfg.StatsSummaryInterval != 24*time.Hour {
		t.Errorf("StatsSummaryInterval = %v, want 24h", cfg.StatsSummaryInterval)
	}
	if cfg.StatsSummaryThreshold != 50 {
		t.Errorf("StatsSummaryThreshold = %d, want 50", cfg.StatsSummaryThreshold)
	}
	if cfg.StatsRetentionDays != 30 {
		t.Errorf("StatsRetentionDays = %d, want 30", cfg.StatsRetentionDays)
	}
	if cfg.StatsMaxTokens != 2048 {
		t.Errorf("StatsMaxTokens = %d, want 2048", cfg.StatsMaxTokens)
	}
}

func TestStatsOverridesAndClamp(t *testing.T) {
	cfg, err := config.Load(envWith(map[string]string{
		"ANTI_TANGENT_STATS_DIR":               "/tmp/at-stats",
		"ANTI_TANGENT_STATS_MODEL":             "openai:gpt-5-mini",
		"ANTI_TANGENT_STATS_SUMMARY_INTERVAL":  "1h",
		"ANTI_TANGENT_STATS_SUMMARY_THRESHOLD": "5",
		"ANTI_TANGENT_STATS_RETENTION_DAYS":    "7",
		"ANTI_TANGENT_STATS_MAX_TOKENS":        "999999", // above ceiling
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StatsDir != "/tmp/at-stats" {
		t.Errorf("StatsDir = %q", cfg.StatsDir)
	}
	if cfg.StatsModel.String() != "openai:gpt-5-mini" {
		t.Errorf("StatsModel = %v", cfg.StatsModel)
	}
	if cfg.StatsSummaryInterval != time.Hour {
		t.Errorf("interval = %v", cfg.StatsSummaryInterval)
	}
	if cfg.StatsSummaryThreshold != 5 {
		t.Errorf("StatsSummaryThreshold = %d, want 5", cfg.StatsSummaryThreshold)
	}
	if cfg.StatsRetentionDays != 7 {
		t.Errorf("StatsRetentionDays = %d, want 7", cfg.StatsRetentionDays)
	}
	if cfg.StatsMaxTokens != cfg.MaxTokensCeiling {
		t.Errorf("StatsMaxTokens = %d, want clamped to ceiling %d", cfg.StatsMaxTokens, cfg.MaxTokensCeiling)
	}
}

func TestStatsInvalidValues(t *testing.T) {
	for _, k := range []string{
		"ANTI_TANGENT_STATS_SUMMARY_INTERVAL",
		"ANTI_TANGENT_STATS_SUMMARY_THRESHOLD",
		"ANTI_TANGENT_STATS_RETENTION_DAYS",
		"ANTI_TANGENT_STATS_MAX_TOKENS",
	} {
		// Non-positive / unparseable values must error and name the var.
		bad := "0"
		if k == "ANTI_TANGENT_STATS_SUMMARY_INTERVAL" {
			bad = "nope"
		}
		if _, err := config.Load(envWith(map[string]string{k: bad})); err == nil {
			t.Errorf("%s=%q: expected error, got nil", k, bad)
		}
	}

	// ANTI_TANGENT_STATS_MODEL: malformed value (no colon) must error and name the var.
	if _, err := config.Load(envWith(map[string]string{"ANTI_TANGENT_STATS_MODEL": "notamodel"})); err == nil {
		t.Errorf("ANTI_TANGENT_STATS_MODEL=%q: expected error, got nil", "notamodel")
	}

	// ANTI_TANGENT_STATS_SUMMARY_INTERVAL: "0s" parses as a duration but must fail the
	// "must be positive" guard (separate from the parse-failure path covered above).
	if _, err := config.Load(envWith(map[string]string{"ANTI_TANGENT_STATS_SUMMARY_INTERVAL": "0s"})); err == nil {
		t.Errorf("ANTI_TANGENT_STATS_SUMMARY_INTERVAL=%q: expected error, got nil", "0s")
	}
}
