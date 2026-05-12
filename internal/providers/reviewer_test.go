package providers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

func TestValidateModel_KnownAnthropic(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-sonnet-4-6"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"}))
}

func TestValidateModel_KnownOpenAI(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "openai", Model: "gpt-5"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "openai", Model: "gpt-5.5"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "openai", Model: "gpt-5.4-mini"}))
}

func TestValidateModel_KnownGoogle(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "google", Model: "gemini-2.5-pro"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "google", Model: "gemini-2.5-flash"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "google", Model: "gemini-3.1-pro-preview"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "google", Model: "gemini-3.1-flash-lite"}))
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

func TestValidateModel_UnknownModelListsAllowedModels(t *testing.T) {
	err := ValidateModel(config.ModelRef{Provider: "openai", Model: "gpt-4o"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `model "gpt-4o" not in allowlist for provider "openai"`)
	assert.Contains(t, err.Error(), "allowed: ")
	// Verify deterministic sorted order: gpt-5 < gpt-5-mini < gpt-5-nano should be in sorted order
	errStr := err.Error()
	allowedIdx := strings.Index(errStr, "allowed: ")
	require.GreaterOrEqual(t, allowedIdx, 0)
	allowed := errStr[allowedIdx+len("allowed: "):]
	// gpt-5 should appear before gpt-5-mini in sorted output
	assert.Less(t, strings.Index(allowed, "gpt-5,"), strings.Index(allowed, "gpt-5-mini"),
		"sorted: gpt-5 should appear before gpt-5-mini")
	assert.Less(t, strings.Index(allowed, "gpt-5-mini"), strings.Index(allowed, "gpt-5-nano"),
		"sorted: gpt-5-mini should appear before gpt-5-nano")
}

func TestValidateModel_UnknownProviderListsSupportedProviders(t *testing.T) {
	err := ValidateModel(config.ModelRef{Provider: "openrouter", Model: "anything"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown provider "openrouter"`)
	assert.Contains(t, err.Error(), "supported: anthropic, google, openai")
}
