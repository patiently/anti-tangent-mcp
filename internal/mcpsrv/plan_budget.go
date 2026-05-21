// Package mcpsrv: adaptive max-tokens budget computation for validate_plan.
// Scope: pure helpers; no I/O. See ValidatePlan in handlers.go for the caller.
package mcpsrv

import "github.com/patiently/anti-tangent-mcp/internal/config"

// Adaptive default plan budget: base + per-task increment, bounded by the
// configured PlanMaxTokens floor and MaxTokensCeiling cap. Plan output scales
// roughly with task count (one block per task plus plan-level findings and
// summary), so a single 4096-token default fits small plans but truncates
// large ones. Constants are sourced from design §1.
const (
	planAdaptiveBase    = 2000
	planAdaptivePerTask = 800
)

// adaptivePlanMaxTokens returns the max-tokens value for a validate_plan
// reviewer call WHEN no caller-supplied max_tokens_override is set. The
// formula is max(cfg.PlanMaxTokens, min(cfg.MaxTokensCeiling, base + per*tasks)).
// Adaptive bumps do not emit a clamp finding because they are not caller
// errors — callers asking for explicit overrides still route through
// effectiveMaxTokens at the ValidatePlan boundary.
func adaptivePlanMaxTokens(cfg config.Config, taskCount int) int {
	floor := planAdaptiveBase + planAdaptivePerTask*taskCount
	if floor > cfg.MaxTokensCeiling {
		floor = cfg.MaxTokensCeiling
	}
	if floor < cfg.PlanMaxTokens {
		floor = cfg.PlanMaxTokens
	}
	return floor
}

// adaptivePrimeMaxTokens implements design §5.3 sizing for prime_project_knowledge.
// Formula: max(cfg.PrimeMaxTokens, min(cfg.MaxTokensCeiling, 1500 + 50*kbIndexLen)).
// Applied only when no caller-supplied max_tokens_override is set; explicit
// overrides route through effectiveMaxTokens (with clamp) at the handler
// boundary like every other tool. Adaptive bumps do not emit a clamp finding.
func adaptivePrimeMaxTokens(cfg config.Config, kbIndexLen int) int {
	scaled := 1500 + 50*kbIndexLen
	if scaled > cfg.MaxTokensCeiling {
		scaled = cfg.MaxTokensCeiling
	}
	if scaled < cfg.PrimeMaxTokens {
		return cfg.PrimeMaxTokens
	}
	return scaled
}

// adaptiveExtractMaxTokens implements design §5.3 sizing for extract_project_knowledge.
// Formula: max(cfg.ExtractMaxTokens, min(cfg.MaxTokensCeiling, 2000 + 1200*envelopeCount)).
// Applied only when no caller-supplied max_tokens_override is set; explicit
// overrides route through effectiveMaxTokens (with clamp) at the handler
// boundary. Adaptive bumps do not emit a clamp finding.
func adaptiveExtractMaxTokens(cfg config.Config, envelopeCount int) int {
	scaled := 2000 + 1200*envelopeCount
	if scaled > cfg.MaxTokensCeiling {
		scaled = cfg.MaxTokensCeiling
	}
	if scaled < cfg.ExtractMaxTokens {
		return cfg.ExtractMaxTokens
	}
	return scaled
}
