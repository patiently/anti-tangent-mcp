// Package providers ships HTTP clients for the supported reviewer LLMs.
package providers

import (
	"context"
	"fmt"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

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
		"gpt-5.5-2026-04-23":      true,
		"gpt-5.4-mini-2026-03-17": true,
	},
	"google": {
		"gemini-2.5-pro":   true,
		"gemini-2.5-flash": true,
	},
}

func ValidateModel(mr config.ModelRef) error {
	models, ok := allowlist[mr.Provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (supported: anthropic, openai, google)", mr.Provider)
	}
	if !models[mr.Model] {
		return fmt.Errorf("model %q not in allowlist for provider %q", mr.Model, mr.Provider)
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
