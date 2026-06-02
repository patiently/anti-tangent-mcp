// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AnthropicKey      string
	OpenAIKey         string
	GoogleKey         string
	PreModel          ModelRef
	MidModel          ModelRef
	PostModel         ModelRef
	PlanModel         ModelRef
	PrimeModel        ModelRef
	ExtractModel      ModelRef
	SessionTTL        time.Duration
	MaxPayloadBytes   int
	RequestTimeout    time.Duration
	LogLevel          slog.Level
	PerTaskMaxTokens  int
	PlanMaxTokens     int
	PrimeMaxTokens    int
	ExtractMaxTokens  int
	PlanTasksPerChunk int
	MaxTokensCeiling  int
	// Stats subsystem (opt-in; see spec 2026-06-02). StatsDir == "" disables
	// it entirely.
	StatsDir              string
	StatsModel            ModelRef
	StatsSummaryInterval  time.Duration
	StatsSummaryThreshold int
	StatsRetentionDays    int
	StatsMaxTokens        int
	// KBStore selects the optional knowledge-store integration used for
	// output adaptation by the prime/extract tools. Empty string (the
	// default) disables KB-specific output (e.g. paste-ready commands);
	// "basic-memory" enables Basic Memory-shaped output. Any other value
	// is rejected at startup.
	KBStore string
}

type ModelRef struct {
	Provider string
	Model    string
}

func (m ModelRef) String() string { return m.Provider + ":" + m.Model }

func ParseModelRef(s string) (ModelRef, error) {
	provider, model, ok := strings.Cut(s, ":")
	if !ok || provider == "" || model == "" {
		return ModelRef{}, fmt.Errorf("invalid model ref %q: expected provider:model", s)
	}
	return ModelRef{Provider: provider, Model: model}, nil
}

// Load reads configuration from the given env lookup function.
// Pass os.Getenv in production; pass a map-backed function in tests.
func Load(env func(string) string) (Config, error) {
	cfg := Config{
		AnthropicKey:          env("ANTHROPIC_API_KEY"),
		OpenAIKey:             env("OPENAI_API_KEY"),
		GoogleKey:             env("GOOGLE_API_KEY"),
		SessionTTL:            4 * time.Hour,
		MaxPayloadBytes:       204800,
		RequestTimeout:        180 * time.Second,
		LogLevel:              slog.LevelInfo,
		PerTaskMaxTokens:      4096,
		PlanMaxTokens:         4096,
		PrimeMaxTokens:        4096,
		ExtractMaxTokens:      8192,
		PlanTasksPerChunk:     8,
		MaxTokensCeiling:      16384,
		StatsSummaryInterval:  24 * time.Hour,
		StatsSummaryThreshold: 50,
		StatsRetentionDays:    30,
		StatsMaxTokens:        2048,
	}

	if cfg.AnthropicKey == "" && cfg.OpenAIKey == "" && cfg.GoogleKey == "" {
		return Config{}, errors.New("at least one of ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY must be set")
	}

	defaults := map[*ModelRef][2]string{
		&cfg.PreModel:  {"ANTI_TANGENT_PRE_MODEL", "anthropic:claude-sonnet-4-6"},
		&cfg.MidModel:  {"ANTI_TANGENT_MID_MODEL", "anthropic:claude-haiku-4-5-20251001"},
		&cfg.PostModel: {"ANTI_TANGENT_POST_MODEL", "anthropic:claude-opus-4-7"},
	}
	for ptr, spec := range defaults {
		val := env(spec[0])
		if val == "" {
			val = spec[1]
		}
		mr, err := ParseModelRef(val)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", spec[0], err)
		}
		*ptr = mr
	}

	// PlanModel: optional override; defaults to whatever PreModel resolved to.
	if v := env("ANTI_TANGENT_PLAN_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MODEL: %w", err)
		}
		cfg.PlanModel = mr
	} else {
		cfg.PlanModel = cfg.PreModel
	}

	// PrimeModel / ExtractModel: optional overrides used by the v0.6.0
	// project-knowledge tools. Resolution order is explicit env override
	// -> resolved PlanModel -> resolved PreModel. PlanModel itself already
	// falls back to PreModel above, so assigning PlanModel here gives the
	// full chain.
	if v := env("ANTI_TANGENT_PRIME_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MODEL: %w", err)
		}
		cfg.PrimeModel = mr
	} else {
		cfg.PrimeModel = cfg.PlanModel
	}
	if v := env("ANTI_TANGENT_EXTRACT_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MODEL: %w", err)
		}
		cfg.ExtractModel = mr
	} else {
		cfg.ExtractModel = cfg.PlanModel
	}

	// KBStore: optional knowledge-store selector. Empty (the default)
	// disables KB-specific output adaptation; "basic-memory" enables it.
	// Any other value is a startup error naming the env var.
	if v := env("ANTI_TANGENT_KB_STORE"); v != "" {
		switch v {
		case "basic-memory":
			cfg.KBStore = v
		default:
			return Config{}, fmt.Errorf("ANTI_TANGENT_KB_STORE: unknown value %q (allowed: \"\", \"basic-memory\")", v)
		}
	}

	if v := env("ANTI_TANGENT_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_SESSION_TTL: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_SESSION_TTL: must be positive, got %s", d)
		}
		cfg.SessionTTL = d
	}
	if v := env("ANTI_TANGENT_MAX_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_PAYLOAD_BYTES: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_PAYLOAD_BYTES: must be positive, got %d", n)
		}
		cfg.MaxPayloadBytes = n
	}
	if v := env("ANTI_TANGENT_REQUEST_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_REQUEST_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_REQUEST_TIMEOUT: must be positive, got %s", d)
		}
		cfg.RequestTimeout = d
	}
	if v := env("ANTI_TANGENT_PER_TASK_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PER_TASK_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PER_TASK_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.PerTaskMaxTokens = n
	}
	if v := env("ANTI_TANGENT_PLAN_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.PlanMaxTokens = n
	}
	if v := env("ANTI_TANGENT_PRIME_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.PrimeMaxTokens = n
	}
	if v := env("ANTI_TANGENT_EXTRACT_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.ExtractMaxTokens = n
	}
	if v := env("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_TASKS_PER_CHUNK: must be positive, got %d", n)
		}
		cfg.PlanTasksPerChunk = n
	}
	if v := env("ANTI_TANGENT_MAX_TOKENS_CEILING"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_TOKENS_CEILING: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_TOKENS_CEILING: must be positive, got %d", n)
		}
		cfg.MaxTokensCeiling = n
	}

	cfg.StatsDir = env("ANTI_TANGENT_STATS_DIR")

	// StatsModel: explicit override -> MidModel.
	if v := env("ANTI_TANGENT_STATS_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MODEL: %w", err)
		}
		cfg.StatsModel = mr
	} else {
		cfg.StatsModel = cfg.MidModel
	}

	if v := env("ANTI_TANGENT_STATS_SUMMARY_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_INTERVAL: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_INTERVAL: must be positive, got %s", d)
		}
		cfg.StatsSummaryInterval = d
	}
	if v := env("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_SUMMARY_THRESHOLD: must be positive, got %d", n)
		}
		cfg.StatsSummaryThreshold = n
	}
	if v := env("ANTI_TANGENT_STATS_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_RETENTION_DAYS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_RETENTION_DAYS: must be positive, got %d", n)
		}
		cfg.StatsRetentionDays = n
	}
	if v := env("ANTI_TANGENT_STATS_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_STATS_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.StatsMaxTokens = n
	}
	if cfg.StatsMaxTokens > cfg.MaxTokensCeiling {
		cfg.StatsMaxTokens = cfg.MaxTokensCeiling
	}

	if v := env("ANTI_TANGENT_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			return Config{}, fmt.Errorf("ANTI_TANGENT_LOG_LEVEL: unknown level %q", v)
		}
	}

	return cfg, nil
}
