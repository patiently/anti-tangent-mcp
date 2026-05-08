package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

func TestValidateModel_KnownAnthropic(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-sonnet-4-6"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5"}))
}

func TestValidateModel_KnownOpenAI(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "openai", Model: "gpt-5"}))
}

func TestValidateModel_KnownGoogle(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "google", Model: "gemini-2.5-pro"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "google", Model: "gemini-2.5-flash"}))
}

func TestValidateModel_UnknownProvider(t *testing.T) {
	err := ValidateModel(config.ModelRef{Provider: "openrouter", Model: "anything"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
}

func TestValidateModel_UnknownModel(t *testing.T) {
	err := ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in allowlist")
}
