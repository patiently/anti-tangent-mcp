// Package mcpsrv: input normalization for validate_task_spec. Pure helpers
// over ValidateTaskSpecArgs; no I/O. See ValidateTaskSpec in handlers.go.
package mcpsrv

import (
	"errors"
	"fmt"
	"strings"
)

const (
	maxPinnedByEntries = 50
	maxPinnedByChars   = 500

	maxNormativeTestBodyEntries = 20
	maxNormativeTestBodyChars   = 4000
)

func normalizePhase(phase string) (string, error) {
	phase = strings.TrimSpace(phase)
	switch phase {
	case "", "pre":
		return "pre", nil
	case "post":
		return "post", nil
	default:
		return "", errors.New(`phase must be "pre" or "post"`)
	}
}

// normalizeBoundedStringList trims whitespace from each entry, drops empties,
// caps per-entry length in runes, and caps total entry count. Errors name the
// JSON snake_case field so callers can pass them straight through.
func normalizeBoundedStringList(field string, entries []string, maxEntries, maxChars int) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxChars {
			return nil, fmt.Errorf("%s[%d] must be at most %d characters", field, i, maxChars)
		}
		out = append(out, trimmed)
		if len(out) > maxEntries {
			return nil, fmt.Errorf("%s must contain at most %d entries", field, maxEntries)
		}
	}
	return out, nil
}

// normalizeCompletionExitContracts trims and bounds the optional
// exit_contracts list submitted to validate_completion.
func normalizeCompletionExitContracts(entries []string) ([]string, error) {
	return normalizeBoundedStringList("exit_contracts", entries, maxPinnedByEntries, maxPinnedByChars)
}

// taskSpecInputs holds the post-validation normalized form of the optional
// task-spec inputs.
type taskSpecInputs struct {
	Phase                        string
	PinnedBy                     []string
	ControllerVerifiedReferences []string
	TestStrategyNotes            []string
	CodebaseConventions          []string
	TestabilityExtractions       []string
	NormativeTestBodies          []string
}

func normalizeTaskSpecInputs(args ValidateTaskSpecArgs) (taskSpecInputs, error) {
	phase, err := normalizePhase(args.Phase)
	if err != nil {
		return taskSpecInputs{}, err
	}
	pinnedBy, err := normalizeBoundedStringList("pinned_by", args.PinnedBy, maxPinnedByEntries, maxPinnedByChars)
	if err != nil {
		return taskSpecInputs{}, err
	}
	controllerVerifiedReferences, err := normalizeBoundedStringList("controller_verified_references", args.ControllerVerifiedReferences, maxPinnedByEntries, maxPinnedByChars)
	if err != nil {
		return taskSpecInputs{}, err
	}
	testStrategyNotes, err := normalizeBoundedStringList("test_strategy_notes", args.TestStrategyNotes, maxPinnedByEntries, maxPinnedByChars)
	if err != nil {
		return taskSpecInputs{}, err
	}
	codebaseConventions, err := normalizeBoundedStringList("codebase_conventions", args.CodebaseConventions, maxPinnedByEntries, maxPinnedByChars)
	if err != nil {
		return taskSpecInputs{}, err
	}
	testabilityExtractions, err := normalizeBoundedStringList("testability_extractions", args.TestabilityExtractions, maxPinnedByEntries, maxPinnedByChars)
	if err != nil {
		return taskSpecInputs{}, err
	}
	normativeTestBodies, err := normalizeBoundedStringList("normative_test_bodies", args.NormativeTestBodies, maxNormativeTestBodyEntries, maxNormativeTestBodyChars)
	if err != nil {
		return taskSpecInputs{}, err
	}
	return taskSpecInputs{
		Phase:                        phase,
		PinnedBy:                     pinnedBy,
		ControllerVerifiedReferences: controllerVerifiedReferences,
		TestStrategyNotes:            testStrategyNotes,
		CodebaseConventions:          codebaseConventions,
		TestabilityExtractions:       testabilityExtractions,
		NormativeTestBodies:          normativeTestBodies,
	}, nil
}
