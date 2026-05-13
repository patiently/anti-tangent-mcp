package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-test",
	}))
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-test", cfg.AnthropicKey)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-sonnet-4-6"}, cfg.PreModel)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"}, cfg.MidModel)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}, cfg.PostModel)
	assert.Equal(t, 4*time.Hour, cfg.SessionTTL)
	assert.Equal(t, 204800, cfg.MaxPayloadBytes)
	assert.Equal(t, 180*time.Second, cfg.RequestTimeout)
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"OPENAI_API_KEY":                 "sk-test",
		"ANTI_TANGENT_PRE_MODEL":         "openai:gpt-5",
		"ANTI_TANGENT_SESSION_TTL":       "30m",
		"ANTI_TANGENT_MAX_PAYLOAD_BYTES": "1024",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, 30*time.Minute, cfg.SessionTTL)
	assert.Equal(t, 1024, cfg.MaxPayloadBytes)
}

func TestLoad_NoKeys(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of")
}

func TestLoad_BadModelRef(t *testing.T) {
	_, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":      "x",
		"ANTI_TANGENT_PRE_MODEL": "no-colon",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected provider:model")
}

func TestLoad_NonPositiveTunables(t *testing.T) {
	cases := []map[string]string{
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_SESSION_TTL": "0s"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_SESSION_TTL": "-1m"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_MAX_PAYLOAD_BYTES": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_MAX_PAYLOAD_BYTES": "-1024"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_REQUEST_TIMEOUT": "0s"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_REQUEST_TIMEOUT": "-5s"},
	}
	for _, tc := range cases {
		_, err := Load(env(tc))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be positive")
	}
}

func TestParseModelRef(t *testing.T) {
	mr, err := ParseModelRef("anthropic:claude-opus-4-7")
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}, mr)
	assert.Equal(t, "anthropic:claude-opus-4-7", mr.String())

	_, err = ParseModelRef("bad")
	require.Error(t, err)
}

func TestLoad_PlanModel_DefaultsToPre(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY": "k",
	}))
	require.NoError(t, err)
	assert.Equal(t, cfg.PreModel, cfg.PlanModel)
}

func TestLoad_PlanModel_InheritsPreOverride(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":      "k",
		"ANTI_TANGENT_PRE_MODEL": "openai:gpt-5",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, cfg.PreModel, cfg.PlanModel)
}

func TestLoad_PlanModel_ExplicitOverride(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":       "k",
		"ANTI_TANGENT_PRE_MODEL":  "openai:gpt-5",
		"ANTI_TANGENT_PLAN_MODEL": "google:gemini-2.5-pro",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, ModelRef{Provider: "google", Model: "gemini-2.5-pro"}, cfg.PlanModel)
}

func TestLoad_TokenBudgetsAndChunkSize_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-test",
	}))
	require.NoError(t, err)
	assert.Equal(t, 4096, cfg.PerTaskMaxTokens)
	assert.Equal(t, 4096, cfg.PlanMaxTokens)
	assert.Equal(t, 8, cfg.PlanTasksPerChunk)
}

func TestLoad_TokenBudgetsAndChunkSize_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":                 "sk-ant-test",
		"ANTI_TANGENT_PER_TASK_MAX_TOKENS":  "8192",
		"ANTI_TANGENT_PLAN_MAX_TOKENS":      "16384",
		"ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "12",
	}))
	require.NoError(t, err)
	assert.Equal(t, 8192, cfg.PerTaskMaxTokens)
	assert.Equal(t, 16384, cfg.PlanMaxTokens)
	assert.Equal(t, 12, cfg.PlanTasksPerChunk)
}

func TestLoad_MaxTokensCeiling(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"default when unset", "", 16384, false},
		{"valid override", "32768", 32768, false},
		{"invalid string rejected", "abc", 0, true},
		{"zero rejected", "0", 0, true},
		{"negative rejected", "-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(func(k string) string {
				switch k {
				case "ANTHROPIC_API_KEY":
					return "k"
				case "ANTI_TANGENT_MAX_TOKENS_CEILING":
					return tt.value
				}
				return ""
			})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.MaxTokensCeiling)
		})
	}
}

func TestLoad_TokenBudgetsAndChunkSize_Reject(t *testing.T) {
	cases := []map[string]string{
		// ANTI_TANGENT_PER_TASK_MAX_TOKENS
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "-1"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PER_TASK_MAX_TOKENS": "not-an-int"},
		// ANTI_TANGENT_PLAN_MAX_TOKENS
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_MAX_TOKENS": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_MAX_TOKENS": "-1"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_MAX_TOKENS": "not-an-int"},
		// ANTI_TANGENT_PLAN_TASKS_PER_CHUNK
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "0"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "-1"},
		{"ANTHROPIC_API_KEY": "x", "ANTI_TANGENT_PLAN_TASKS_PER_CHUNK": "not-an-int"},
	}
	for _, tc := range cases {
		_, err := Load(env(tc))
		require.Error(t, err)
	}
}
