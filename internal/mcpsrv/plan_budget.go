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
