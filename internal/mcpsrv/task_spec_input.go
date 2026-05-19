// Package mcpsrv: input normalization for validate_task_spec. Pure helpers
// over ValidateTaskSpecArgs; no I/O. See ValidateTaskSpec in handlers.go.
package mcpsrv

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/session"
)

const (
	maxPinnedByEntries = 50
	maxPinnedByChars   = 500

	maxNormativeTestBodyEntries = 20
	maxNormativeTestBodyChars   = 4000

	maxHarnessShapeAttestationEntries        = 25
	maxHarnessShapeAttestationHarnessChars   = 240
	maxHarnessShapeAttestationPathChars      = 240
	maxHarnessShapeAttestationAssertions     = 10
	maxHarnessShapeAttestationAssertionChars = 480
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
	HarnessShapeAttestations     []session.HarnessShapeAttestation
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
	harnessShapeAttestations, err := normalizeHarnessShapeAttestation(args.HarnessShapeAttestation)
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
		HarnessShapeAttestations:     harnessShapeAttestations,
	}, nil
}

// normalizeHarnessShapeAttestation trims whitespace, caps lengths and counts,
// dedupes by canonical-JSON SHA-256, and rejects entries with empty harness
// or empty assertions array. Returns a fresh slice; the input is not
// mutated. Mirrors normalizeBoundedStringList's error-message style so the
// errors are friendly to MCP callers.
func normalizeHarnessShapeAttestation(entries []session.HarnessShapeAttestation) ([]session.HarnessShapeAttestation, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]session.HarnessShapeAttestation, 0, len(entries))
	seen := make(map[[32]byte]struct{}, len(entries))
	for i, e := range entries {
		harness := strings.TrimSpace(e.Harness)
		if harness == "" {
			return nil, fmt.Errorf("harness_shape_attestation[%d].harness must be non-empty", i)
		}
		if len([]rune(harness)) > maxHarnessShapeAttestationHarnessChars {
			return nil, fmt.Errorf("harness_shape_attestation[%d].harness must be at most %d characters", i, maxHarnessShapeAttestationHarnessChars)
		}
		path := strings.TrimSpace(e.Path)
		if len([]rune(path)) > maxHarnessShapeAttestationPathChars {
			return nil, fmt.Errorf("harness_shape_attestation[%d].path must be at most %d characters", i, maxHarnessShapeAttestationPathChars)
		}
		if len(e.Assertions) == 0 {
			return nil, fmt.Errorf("harness_shape_attestation[%d].assertions must be non-empty", i)
		}
		if len(e.Assertions) > maxHarnessShapeAttestationAssertions {
			return nil, fmt.Errorf("harness_shape_attestation[%d].assertions must contain at most %d entries", i, maxHarnessShapeAttestationAssertions)
		}
		normAssertions := make([]string, 0, len(e.Assertions))
		for j, a := range e.Assertions {
			trimmed := strings.TrimSpace(a)
			if trimmed == "" {
				return nil, fmt.Errorf("harness_shape_attestation[%d].assertions[%d] must be non-empty", i, j)
			}
			if len([]rune(trimmed)) > maxHarnessShapeAttestationAssertionChars {
				return nil, fmt.Errorf("harness_shape_attestation[%d].assertions[%d] must be at most %d characters", i, j, maxHarnessShapeAttestationAssertionChars)
			}
			normAssertions = append(normAssertions, trimmed)
		}
		norm := session.HarnessShapeAttestation{
			Harness:    harness,
			Path:       path,
			Assertions: normAssertions,
		}
		raw, mErr := json.Marshal(norm)
		if mErr != nil {
			return nil, fmt.Errorf("harness_shape_attestation[%d]: canonical encode: %w", i, mErr)
		}
		key := sha256.Sum256(raw)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, norm)
		if len(out) > maxHarnessShapeAttestationEntries {
			return nil, fmt.Errorf("harness_shape_attestation must contain at most %d entries", maxHarnessShapeAttestationEntries)
		}
	}
	return out, nil
}
