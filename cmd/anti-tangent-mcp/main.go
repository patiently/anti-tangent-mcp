package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/mcpsrv"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
)

// version is set at build time via -ldflags "-X main.version=$(cat VERSION)".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	logger.Info("starting", "version", version,
		"pre_model", cfg.PreModel.String(),
		"mid_model", cfg.MidModel.String(),
		"post_model", cfg.PostModel.String(),
		"plan_model", cfg.PlanModel.String(),
		"session_ttl", cfg.SessionTTL.String())

	if err := providers.ValidateModel(cfg.PreModel); err != nil {
		fail(logger, "pre model invalid", err)
	}
	if err := providers.ValidateModel(cfg.MidModel); err != nil {
		fail(logger, "mid model invalid", err)
	}
	if err := providers.ValidateModel(cfg.PostModel); err != nil {
		fail(logger, "post model invalid", err)
	}
	if err := providers.ValidateModel(cfg.PlanModel); err != nil {
		fail(logger, "plan model invalid", err)
	}
	if err := providers.ValidateModel(cfg.PrimeModel); err != nil {
		fail(logger, "prime model invalid", err)
	}
	if err := providers.ValidateModel(cfg.ExtractModel); err != nil {
		fail(logger, "extract model invalid", err)
	}

	registry := providers.Registry{}
	if cfg.AnthropicKey != "" {
		registry["anthropic"] = providers.NewAnthropic(cfg.AnthropicKey, "", cfg.RequestTimeout)
	}
	if cfg.OpenAIKey != "" {
		registry["openai"] = providers.NewOpenAI(cfg.OpenAIKey, "", cfg.RequestTimeout)
	}
	if cfg.GoogleKey != "" {
		registry["google"] = providers.NewGoogle(cfg.GoogleKey, "", cfg.RequestTimeout)
	}

	store := session.NewStore(cfg.SessionTTL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go evictLoop(ctx, store, 5*time.Minute, logger)

	var statsRec *stats.Recorder
	if cfg.StatsDir != "" {
		if err := providers.ValidateModel(cfg.StatsModel); err != nil {
			fail(logger, "stats model invalid", err)
		}
		// A missing API key for the stats provider disables only the summary
		// step (reviewer == nil); recording + rollup still work.
		statsReviewer := registry[cfg.StatsModel.Provider]
		rec, err := stats.New(stats.Options{
			Dir:              cfg.StatsDir,
			Reviewer:         statsReviewer,
			Model:            cfg.StatsModel.Model,
			MaxTokens:        cfg.StatsMaxTokens,
			RequestTimeout:   cfg.RequestTimeout,
			SummaryInterval:  cfg.StatsSummaryInterval,
			SummaryThreshold: cfg.StatsSummaryThreshold,
			RetentionDays:    cfg.StatsRetentionDays,
			Logger:           logger,
		})
		if err != nil {
			logger.Warn("stats disabled", "err", err)
		} else {
			statsRec = rec
			logger.Info("stats enabled", "dir", cfg.StatsDir, "model", cfg.StatsModel.String(), "summary_enabled", statsReviewer != nil)
		}
	}

	mcpsrv.Version = version
	srv := mcpsrv.New(mcpsrv.Deps{
		Cfg:      cfg,
		Sessions: store,
		Reviews:  registry,
		Stats:    statsRec,
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutting down")
		cancel()
	}()

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("mcp run cancelled")
			return
		}
		logger.Error("mcp run failed", "err", err)
		os.Exit(1)
	}
}

func evictLoop(ctx context.Context, store *session.Store, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n := store.EvictExpired(time.Now())
			if n > 0 {
				logger.Info("evicted sessions", "count", n)
			}
		}
	}
}

func fail(logger *slog.Logger, msg string, err error) {
	logger.Error(msg, "err", err)
	os.Exit(1)
}
