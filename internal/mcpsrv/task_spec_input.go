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

func normalizePinnedBy(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("pinned_by[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("pinned_by must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

func normalizeControllerVerifiedReferences(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("controller_verified_references[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("controller_verified_references must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

func normalizeTestStrategyNotes(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("test_strategy_notes[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("test_strategy_notes must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

func normalizeCodebaseConventions(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("codebase_conventions[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("codebase_conventions must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

func normalizeTestabilityExtractions(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxPinnedByChars {
			return nil, fmt.Errorf("testability_extractions[%d] must be at most %d characters", i, maxPinnedByChars)
		}
		out = append(out, trimmed)
		if len(out) > maxPinnedByEntries {
			return nil, fmt.Errorf("testability_extractions must contain at most %d entries", maxPinnedByEntries)
		}
	}
	return out, nil
}

func normalizeNormativeTestBodies(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxNormativeTestBodyChars {
			return nil, fmt.Errorf("normative_test_bodies[%d] must be at most %d characters", i, maxNormativeTestBodyChars)
		}
		out = append(out, trimmed)
		if len(out) > maxNormativeTestBodyEntries {
			return nil, fmt.Errorf("normative_test_bodies must contain at most %d entries", maxNormativeTestBodyEntries)
		}
	}
	return out, nil
}

// taskSpecInputs holds the post-validation normalized form of the optional
// task-spec inputs. The combined helper consolidates the two error paths so
// the calling handler stays under cyclomatic-complexity thresholds.
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
	pinnedBy, err := normalizePinnedBy(args.PinnedBy)
	if err != nil {
		return taskSpecInputs{}, err
	}
	controllerVerifiedReferences, err := normalizeControllerVerifiedReferences(args.ControllerVerifiedReferences)
	if err != nil {
		return taskSpecInputs{}, err
	}
	testStrategyNotes, err := normalizeTestStrategyNotes(args.TestStrategyNotes)
	if err != nil {
		return taskSpecInputs{}, err
	}
	codebaseConventions, err := normalizeCodebaseConventions(args.CodebaseConventions)
	if err != nil {
		return taskSpecInputs{}, err
	}
	testabilityExtractions, err := normalizeTestabilityExtractions(args.TestabilityExtractions)
	if err != nil {
		return taskSpecInputs{}, err
	}
	normativeTestBodies, err := normalizeNormativeTestBodies(args.NormativeTestBodies)
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
