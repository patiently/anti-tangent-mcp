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

// taskSpecInputs holds the post-validation normalized form of the optional
// task-spec inputs. The combined helper consolidates the two error paths so
// the calling handler stays under cyclomatic-complexity thresholds.
type taskSpecInputs struct {
	Phase    string
	PinnedBy []string
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
	return taskSpecInputs{Phase: phase, PinnedBy: pinnedBy}, nil
}
