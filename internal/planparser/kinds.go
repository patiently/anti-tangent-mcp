package planparser

import (
	"regexp"
	"strings"
)

// Values the agent-network headers accept. Anything else is reported back to
// the caller as unknown and otherwise ignored.
const (
	PlanKindAgentNetwork = "agent-network"
	TaskKindBuild        = "build"
	TaskKindExperiment   = "experiment"
)

// Rungs are the fix-ladder rungs a task's **Rung:** header may name, lowest
// first.
var Rungs = []string{"oracle", "variance", "facts", "tool", "commitment", "prompt", "owner"}

var (
	planKindHeaderRe = regexp.MustCompile(`(?i)^\s*(?:[-*]\s+)?\*\*plan kind:\*\*(.*)$`)
	taskKindHeaderRe = regexp.MustCompile(`(?i)^\s*(?:[-*]\s+)?\*\*kind:\*\*(.*)$`)
	rungHeaderRe     = regexp.MustCompile(`(?i)^\s*(?:[-*]\s+)?\*\*rung:\*\*(.*)$`)
)

// PlanKind returns the plan's **Plan kind:** header from preamble, the text
// above the first task heading. kind is PlanKindAgentNetwork or "". unknown
// is the header's value when one is present and is not a known kind.
func PlanKind(preamble string) (kind, unknown string) {
	v, ok := headerValue(preamble, planKindHeaderRe)
	if !ok {
		return "", ""
	}
	if v == PlanKindAgentNetwork {
		return v, ""
	}
	return "", v
}

// taskKinds reads a task body's **Kind:** and **Rung:** headers. kind is
// TaskKindExperiment or TaskKindBuild, the default; rung is one of Rungs or
// "". The unknown values are a header's value that is present and not known.
func taskKinds(body string) (kind, unknownKind, rung, unknownRung string) {
	kind = TaskKindBuild
	if v, ok := headerValue(body, taskKindHeaderRe); ok {
		switch v {
		case TaskKindBuild, TaskKindExperiment:
			kind = v
		default:
			unknownKind = v
		}
	}
	if v, ok := headerValue(body, rungHeaderRe); ok {
		if IsRung(v) {
			rung = v
		} else {
			unknownRung = v
		}
	}
	return kind, unknownKind, rung, unknownRung
}

// IsRung reports whether v is one of Rungs.
func IsRung(v string) bool {
	for _, r := range Rungs {
		if v == r {
			return true
		}
	}
	return false
}

// headerValue returns the value of the first line outside fenced code that
// re matches: lower-cased, trimmed, without surrounding backticks or a
// trailing period. ok is false when no line matches or the value is empty.
func headerValue(text string, re *regexp.Regexp) (string, bool) {
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[1])
		v = strings.TrimSuffix(v, ".")
		v = strings.ToLower(strings.TrimSpace(strings.Trim(v, "`")))
		return v, v != ""
	}
	return "", false
}
