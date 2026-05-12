// Package providers ships HTTP clients for the supported reviewer LLMs.
package providers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

// ErrResponseTruncated is returned when a provider signals that its response
// was cut short by the configured max-token limit. Callers should use
// errors.Is to detect it and surface advisory findings rather than opaque
// parse errors.
var ErrResponseTruncated = errors.New("reviewer response truncated at max_tokens limit")

type Reviewer interface {
	Name() string
	Review(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	Model      string
	System     string
	User       string
	MaxTokens  int
	JSONSchema []byte
}

type Response struct {
	RawJSON      []byte
	Model        string
	InputTokens  int
	OutputTokens int
}

// allowlist holds the known model IDs per provider. Adding a new model is a
// one-line change here; the validator runs at startup and on per-call overrides.
var allowlist = map[string]map[string]bool{
	"anthropic": {
		"claude-opus-4-7":           true,
		"claude-sonnet-4-6":         true,
		"claude-haiku-4-5-20251001": true,
	},
	"openai": {
		"gpt-5":                   true,
		"gpt-5-mini":              true,
		"gpt-5-nano":              true,
		"gpt-5.5":                 true,
		"gpt-5.5-2026-04-23":      true,
		"gpt-5.4-mini":            true,
		"gpt-5.4-mini-2026-03-17": true,
	},
	"google": {
		"gemini-2.5-pro":         true,
		"gemini-2.5-flash":       true,
		"gemini-3.1-pro-preview": true,
		"gemini-3.1-flash-lite":  true,
	},
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedProviders() []string {
	keys := make([]string, 0, len(allowlist))
	for k := range allowlist {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func ValidateModel(mr config.ModelRef) error {
	models, ok := allowlist[mr.Provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (supported: %s)", mr.Provider, strings.Join(sortedProviders(), ", "))
	}
	if !models[mr.Model] {
		return fmt.Errorf("model %q not in allowlist for provider %q (allowed: %s)", mr.Model, mr.Provider, strings.Join(sortedKeys(models), ", "))
	}
	return nil
}

// Registry maps provider name to a constructed Reviewer instance.
type Registry map[string]Reviewer

func (r Registry) Get(provider string) (Reviewer, error) {
	rv, ok := r[provider]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured (likely missing API key)", provider)
	}
	return rv, nil
}
