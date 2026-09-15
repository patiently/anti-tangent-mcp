//go:build e2e

package mcpsrv

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// TestReplay_E2E replays recorded task fixtures against the configured
// reviewers and reports, per expectation, how many runs raised the issue. The
// fixtures hold a consumer project's code, so they live outside this
// repository; replayFixture documents their shape.
//
//	ANTI_TANGENT_REPLAY_DIR       directory of *.json fixtures; the test skips when unset
//	ANTI_TANGENT_REPLAY_RUNS      runs per fixture, default 5
//	ANTI_TANGENT_REPLAY_ONLY      comma-separated fixture names to run
//	ANTI_TANGENT_REPLAY_OUT       file to write the reports to, as JSON
//	ANTI_TANGENT_REPLAY_DRY_RUN=1 answer every review locally with an empty pass
//
// Every run that is not a dry run makes paid reviewer calls: dry-run first to
// size the prompts, and estimate the spend before a real run.
//
//	ANTI_TANGENT_REPLAY_DIR=/abs/fixtures ANTHROPIC_API_KEY=… \
//	  go test -tags=e2e -count=1 -timeout 4h -run TestReplay_E2E ./internal/mcpsrv/ -v
func TestReplay_E2E(t *testing.T) {
	dir := os.Getenv("ANTI_TANGENT_REPLAY_DIR")
	if dir == "" {
		t.Skip("set ANTI_TANGENT_REPLAY_DIR to a fixture directory to enable; every run makes paid reviewer calls unless ANTI_TANGENT_REPLAY_DRY_RUN=1")
	}
	runs := 5
	if v := os.Getenv("ANTI_TANGENT_REPLAY_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		require.NoError(t, err, "ANTI_TANGENT_REPLAY_RUNS")
		require.Positive(t, n, "ANTI_TANGENT_REPLAY_RUNS")
		runs = n
	}
	fixtures, err := loadReplayFixtures(dir)
	require.NoError(t, err)
	fixtures, err = filterReplayFixtures(fixtures, os.Getenv("ANTI_TANGENT_REPLAY_ONLY"))
	require.NoError(t, err)
	require.NotEmpty(t, fixtures, "no fixtures to run in %s", dir)

	dryRun := os.Getenv("ANTI_TANGENT_REPLAY_DRY_RUN") == "1"
	getenv := os.Getenv
	if dryRun {
		// A dry run calls no provider and needs no key, but config.Load refuses
		// to start without one.
		getenv = func(k string) string {
			if v := os.Getenv(k); v != "" || k != "ANTHROPIC_API_KEY" {
				return v
			}
			return "dry-run"
		}
	}
	cfg, err := config.Load(getenv)
	require.NoError(t, err)

	reviewers := providers.Registry{}
	if dryRun {
		for _, name := range []string{"anthropic", "openai", "google"} {
			reviewers[name] = replayDryRunReviewer{name: name}
		}
	} else {
		if cfg.AnthropicKey != "" {
			reviewers["anthropic"] = providers.NewAnthropic(cfg.AnthropicKey, "", cfg.RequestTimeout)
		}
		if cfg.OpenAIKey != "" {
			reviewers["openai"] = providers.NewOpenAI(cfg.OpenAIKey, "", cfg.RequestTimeout)
		}
		if cfg.GoogleKey != "" {
			reviewers["google"] = providers.NewGoogle(cfg.GoogleKey, "", cfg.RequestTimeout)
		}
	}

	reports := make([]replayReport, 0, len(fixtures))
	for _, fx := range fixtures {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(runs)*15*time.Minute)
		report := runReplayFixture(ctx, cfg, reviewers, fx, runs)
		cancel()
		t.Log("\n" + report.String())
		reports = append(reports, report)
	}
	if out := os.Getenv("ANTI_TANGENT_REPLAY_OUT"); out != "" {
		b, err := json.MarshalIndent(reports, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(out, append(b, '\n'), 0o600))
	}
}
