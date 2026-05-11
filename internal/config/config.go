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
	SessionTTL        time.Duration
	MaxPayloadBytes   int
	RequestTimeout    time.Duration
	LogLevel          slog.Level
	PerTaskMaxTokens  int
	PlanMaxTokens     int
	PlanTasksPerChunk int
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
		AnthropicKey:      env("ANTHROPIC_API_KEY"),
		OpenAIKey:         env("OPENAI_API_KEY"),
		GoogleKey:         env("GOOGLE_API_KEY"),
		SessionTTL:        4 * time.Hour,
		MaxPayloadBytes:   204800,
		RequestTimeout:    120 * time.Second,
		LogLevel:          slog.LevelInfo,
		PerTaskMaxTokens:  4096,
		PlanMaxTokens:     4096,
		PlanTasksPerChunk: 8,
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
