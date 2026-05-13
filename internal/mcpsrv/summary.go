package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// summaryEvidenceMax is the per-finding evidence cap (in runes) used in the
// paste-ready summary_block. Long evidence is suffixed with a UTF-8 ellipsis
// so the summary stays compact and deterministic regardless of reviewer
// verbosity. Operates on rune boundaries to avoid splitting multi-byte
// characters mid-codepoint.
const summaryEvidenceMax = 120

// formatEnvelopeSummary renders a deterministic, paste-ready text block for a
// per-task Envelope (validate_task_spec / check_progress / validate_completion).
// It includes the session id, verdict, partial flag (when set), model + review
// timing, optional session TTL line, findings counts plus per-finding lines,
// and the next_action. Output is plain text and intentionally stable so
// downstream tooling can substring-assert against it.
func formatEnvelopeSummary(env Envelope) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope\n")
	fmt.Fprintf(&b, "  session_id:    %s\n", env.SessionID)
	fmt.Fprintf(&b, "  verdict:       %s\n", env.Verdict)
	if env.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", env.ModelUsed)
	fmt.Fprintf(&b, "  review_ms:     %d\n", env.ReviewMS)
	if env.SessionTTLRemainingSeconds != nil {
		fmt.Fprintf(&b, "  session_ttl_remaining_seconds: %d\n", *env.SessionTTLRemainingSeconds)
	}
	writeFindingsSummary(&b, env.Findings, "  ")
	fmt.Fprintf(&b, "  next_action:   %s\n", env.NextAction)
	return b.String()
}

// formatPlanSummary renders a deterministic, paste-ready text block for a
// PlanResult (validate_plan). It includes the plan verdict, plan_quality,
// partial flag (when set), model + review timing, plan-level findings counts
// and lines, then a per-task block with verdict/findings counts and lines.
// Mirrors formatEnvelopeSummary's conventions so consumers can build a single
// renderer for both shapes if desired.
func formatPlanSummary(pr verdict.PlanResult, modelUsed string, reviewMS int64) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope (validate_plan)\n")
	fmt.Fprintf(&b, "  plan_verdict:  %s\n", pr.PlanVerdict)
	fmt.Fprintf(&b, "  plan_quality:  %s\n", pr.PlanQuality)
	if pr.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", modelUsed)
	fmt.Fprintf(&b, "  review_ms:     %d\n", reviewMS)
	crit, maj, min := countSeverities(pr.PlanFindings)
	fmt.Fprintf(&b, "  plan_findings: %d (%d/%d/%d)\n", len(pr.PlanFindings), crit, maj, min)
	for _, f := range pr.PlanFindings {
		fmt.Fprintf(&b, "    - [%s][%s] %s — %s\n", f.Severity, f.Category, f.Criterion, truncate(f.Evidence, summaryEvidenceMax))
	}
	fmt.Fprintf(&b, "  tasks: %d\n", len(pr.Tasks))
	for _, t := range pr.Tasks {
		tCrit, tMaj, tMin := countSeverities(t.Findings)
		fmt.Fprintf(&b, "    Task %d: %s  [%s]  findings: %d (%d/%d/%d)\n",
			t.TaskIndex, t.TaskTitle, t.Verdict, len(t.Findings), tCrit, tMaj, tMin)
		for _, f := range t.Findings {
			fmt.Fprintf(&b, "      - [%s] %s — %s\n", f.Severity, f.Criterion, truncate(f.Evidence, summaryEvidenceMax))
		}
	}
	fmt.Fprintf(&b, "  next_action:   %s\n", pr.NextAction)
	return b.String()
}

// writeFindingsSummary writes the `findings: N total (C critical, M major, m minor)`
// summary line and one bullet per finding to b, prefixed with the supplied
// indent. Shared by formatEnvelopeSummary so the layout stays identical.
func writeFindingsSummary(b *strings.Builder, findings []verdict.Finding, indent string) {
	crit, maj, min := countSeverities(findings)
	fmt.Fprintf(b, "%sfindings:      %d total (%d critical, %d major, %d minor)\n", indent, len(findings), crit, maj, min)
	for _, f := range findings {
		fmt.Fprintf(b, "%s  - [%s][%s] %s — %s\n", indent, f.Severity, f.Category, f.Criterion, truncate(f.Evidence, summaryEvidenceMax))
	}
}

// countSeverities tallies critical/major/minor findings. Any other severity
// value (defensively, given the schema constrains this to the three known
// values) is ignored.
func countSeverities(findings []verdict.Finding) (critical, major, minor int) {
	for _, f := range findings {
		switch f.Severity {
		case verdict.SeverityCritical:
			critical++
		case verdict.SeverityMajor:
			major++
		case verdict.SeverityMinor:
			minor++
		}
	}
	return critical, major, minor
}

// truncate returns s if its rune count is at or below max; otherwise the first
// max runes followed by a single UTF-8 ellipsis. Rune-based truncation avoids
// splitting multi-byte UTF-8 characters mid-codepoint, which a byte-based
// slice would do for non-ASCII evidence text.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
