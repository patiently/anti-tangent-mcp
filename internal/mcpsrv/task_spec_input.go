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
	ProjectKnowledge             string
}

// normalizeProjectKnowledge trims surrounding whitespace. The cumulative
// payload-cap check happens in normalizeTaskSpecInputs so it can sum
// project_knowledge against every other string field on the args. We
// deliberately do NOT reject here on a per-field cap — a 200 KB
// project_knowledge alone is still under the cap; what matters is total
// args size.
func normalizeProjectKnowledge(raw string) string {
	return strings.TrimSpace(raw)
}

// sumLen is a tiny helper used by the error formatter and totalNormalizedTaskSpecBytes.
func sumLen(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}

// harnessShapeAttestationsBytes returns a conservative byte-cost upper bound
// for a slice of attestations. We JSON-marshal the slice and use the encoded
// length as the cost — this is an upper bound (marshalling adds field names,
// quotes, brackets) but captures the harness + path + assertion text in a
// single deterministic computation. If marshalling fails (shouldn't, since
// HarnessShapeAttestation has no unsupported types), we fall back to a manual
// sum so the cap guard cannot be silently bypassed.
func harnessShapeAttestationsBytes(entries []session.HarnessShapeAttestation) int {
	if len(entries) == 0 {
		return 0
	}
	if b, err := json.Marshal(entries); err == nil {
		return len(b)
	}
	// Fallback: manual byte sum across harness + path + assertions.
	n := 0
	for _, e := range entries {
		n += len(e.Harness) + len(e.Path) + sumLen(e.Assertions)
	}
	return n
}

// totalNormalizedTaskSpecBytes returns the byte sum of every user-supplied
// string field on a task-spec call AFTER per-list normalization (trim +
// drop-empty via normalizeBoundedStringList). Counting raw args.* would
// include whitespace-only entries that the renderer drops, producing
// spurious cap rejections. TaskTitle / Goal / Context / projectKnowledge
// are not list-normalized — their raw lengths flow through verbatim.
// HarnessShapeAttestation is a slice of structs; we count via
// harnessShapeAttestationsBytes (JSON-marshal length as a conservative
// upper bound). Used to enforce MaxPayloadBytes cumulatively (spec §5.2 / §3.3).
func totalNormalizedTaskSpecBytes(args ValidateTaskSpecArgs, projectKnowledge string, in taskSpecInputs) int {
	total := len(args.TaskTitle) + len(args.Goal) + len(args.Context) + len(projectKnowledge)
	for _, s := range args.AcceptanceCriteria {
		total += len(s)
	}
	for _, s := range args.NonGoals {
		total += len(s)
	}
	total += sumLen(in.PinnedBy)
	total += sumLen(in.ControllerVerifiedReferences)
	total += sumLen(in.TestStrategyNotes)
	total += sumLen(in.CodebaseConventions)
	total += sumLen(in.TestabilityExtractions)
	total += sumLen(in.NormativeTestBodies)
	total += harnessShapeAttestationsBytes(in.HarnessShapeAttestations)
	return total
}

func normalizeTaskSpecInputs(args ValidateTaskSpecArgs, maxPayload int) (taskSpecInputs, error) {
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
	projectKnowledge := normalizeProjectKnowledge(args.ProjectKnowledge)
	in := taskSpecInputs{
		Phase:                        phase,
		PinnedBy:                     pinnedBy,
		ControllerVerifiedReferences: controllerVerifiedReferences,
		TestStrategyNotes:            testStrategyNotes,
		CodebaseConventions:          codebaseConventions,
		TestabilityExtractions:       testabilityExtractions,
		NormativeTestBodies:          normativeTestBodies,
		HarnessShapeAttestations:     harnessShapeAttestations,
		ProjectKnowledge:             projectKnowledge,
	}
	if total := totalNormalizedTaskSpecBytes(args, projectKnowledge, in); total > maxPayload {
		// The error names the cumulative cap and reports each major
		// contributor's byte count so the caller can see at a glance which
		// field is most likely the cause. We do not single out
		// project_knowledge unless it is in fact the largest contributor.
		// Report normalized contributor lengths where available (lists that
		// went through normalizeBoundedStringList). project_knowledge and
		// context are not list-normalized — their raw len is what counts.
		// acceptance_criteria stays raw because it isn't list-normalized
		// either (no per-entry trim helper applied today).
		return taskSpecInputs{}, fmt.Errorf(
			"task spec payload %d bytes > cap %d (project_knowledge: %d, context: %d, normative_test_bodies: %d, acceptance_criteria: %d)",
			total, maxPayload,
			len(projectKnowledge), len(args.Context),
			sumLen(in.NormativeTestBodies), sumLen(args.AcceptanceCriteria),
		)
	}
	return in, nil
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
