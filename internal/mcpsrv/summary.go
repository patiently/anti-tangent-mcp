package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/blocktext"
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
// It includes the originating tool name (when set), the session id, verdict,
// partial flag (when set), model + review timing, optional session TTL line,
// findings counts plus per-finding lines, and the next_action. Output is
// plain text and intentionally stable so downstream tooling can
// substring-assert against it.
//
// The `tool:` line exists so a consumer that sees only this pasted text (not
// which MCP tool produced it) can still tell the three per-task tools apart —
// they otherwise render byte-identical envelopes. See
// plugin/anti-tangent-guard/hooks/check-task-complete, which requires
// `tool: validate_completion` before treating a block as its pass signal.
func formatEnvelopeSummary(env Envelope) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope\n")
	if env.Tool != "" {
		fmt.Fprintf(&b, "  tool:          %s\n", escapeBlockValue(env.Tool))
	}
	fmt.Fprintf(&b, "  session_id:    %s\n", escapeBlockValue(env.SessionID))
	fmt.Fprintf(&b, "  verdict:       %s\n", escapeBlockValue(env.Verdict))
	if env.Partial {
		b.WriteString("  partial:       true\n")
	}
	if env.SubmissionDefectOnly {
		b.WriteString("  submission_defect_only: true — re-submit with the missing evidence; no code rework implied\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", escapeBlockValue(env.ModelUsed))
	fmt.Fprintf(&b, "  review_ms:     %d\n", env.ReviewMS)
	if env.SessionTTLRemainingSeconds != nil {
		fmt.Fprintf(&b, "  session_ttl_remaining_seconds: %d\n", *env.SessionTTLRemainingSeconds)
	}
	writeFindingsSummary(&b, env.Findings, "  ")
	// next_action is reviewer-authored free text (schema: minLength 1, no
	// other constraint — see internal/verdict/schema.json) rendered LAST in
	// this block, after every finding. Escaping it matters for the same
	// reason as Criterion/Evidence below: an embedded newline here would
	// otherwise land at true column 0 (nothing precedes a continuation line
	// of the last field), able to forge a bare "anti-tangent envelope"
	// header or a "verdict:"/"tool:" line unindented — a stronger version of
	// the same hole. See escapeContinuationLines.
	fmt.Fprintf(&b, "  next_action:   %s\n", escapeBlockValue(env.NextAction))
	return b.String()
}

// blockValueContIndent aligns a multi-line "  <label>: <value>" line's
// continuation lines under its value column. Every label in every formatter
// in this file is padded to the same width ("  next_action:   ",
// "  session_id:    ", "  source:        ", …), so one constant serves them
// all. It also matches the "                 - %s (%d B)\n" convention used
// for meta.ContextFiles in formatPlanSummary.
const blockValueContIndent = "                 "

// contextFileContIndent is blockValueContIndent plus the width of the "- "
// bullet formatPlanSummary renders each context file with, so a continuation
// line sits under the path rather than under the bullet.
const contextFileContIndent = blockValueContIndent + "  "

// escapeBlockValue is escapeContinuationLines for the value half of a
// "  <label>: <value>" line — see blockValueContIndent. EVERY such value in
// EVERY formatter in this file goes through it or through
// escapeContinuationLines directly; summary_forgery_test.go enumerates the
// formatters straight from this package's source and fails if a new one is
// added without that treatment.
func escapeBlockValue(s string) string {
	return escapeContinuationLines(s, blockValueContIndent)
}

// planSummaryMeta bundles the non-PlanResult inputs to formatPlanSummary.
// Carried on a struct rather than as scalars so the signature stays narrow
// (1 arg vs. 4) and matches CodeScene's "max arguments = 4" threshold; mirrors
// the renderPlanReviewInputs / planReviewErrInputs pattern.
//
// Source is the pre-rendered provenance string, empty when plan_text was used.
// It is passed per-call rather than stored on the cache entry: planPassCacheKey
// hashes content, so two different paths holding identical plans share an
// entry, and echoing the stored path would name an earlier caller's file.
type planSummaryMeta struct {
	ModelUsed string
	ReviewMS  int64
	Source    string
	// ContextFiles is the attached set for THIS call. Carried per-call, never
	// stored on the cache entry, for the same reason as Source: planPassCacheKey
	// hashes content, so echoing a stored entry's list would name another
	// caller's files.
	ContextFiles []fileSource
}

// formatPlanSummary renders a deterministic, paste-ready text block for a
// PlanResult (validate_plan). It includes the plan verdict, plan_quality,
// provenance (when the plan was read from a file), partial flag (when set),
// model + review timing, plan-level findings counts and lines, then a
// per-task block with verdict/findings counts and lines. Mirrors
// formatEnvelopeSummary's conventions so consumers can build a single
// renderer for both shapes if desired.
func formatPlanSummary(pr verdict.PlanResult, meta planSummaryMeta) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope (validate_plan)\n")
	fmt.Fprintf(&b, "  plan_verdict:  %s\n", pr.PlanVerdict)
	fmt.Fprintf(&b, "  plan_quality:  %s\n", pr.PlanQuality)
	if pr.PlanRunID != "" {
		fmt.Fprintf(&b, "  plan_run_id:   %s\n", escapeBlockValue(pr.PlanRunID))
	}
	if meta.Source != "" {
		fmt.Fprintf(&b, "  source:        %s\n", escapeBlockValue(meta.Source))
	}
	if len(meta.ContextFiles) > 0 {
		totalCtx := 0
		for _, f := range meta.ContextFiles {
			totalCtx += f.Bytes
		}
		fmt.Fprintf(&b, "  context:       %d files, %d B\n", len(meta.ContextFiles), totalCtx)
		for _, f := range meta.ContextFiles {
			fmt.Fprintf(&b, "                 - %s (%d B)\n", escapeContinuationLines(f.Path, contextFileContIndent), f.Bytes)
		}
	}
	if pr.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", escapeBlockValue(meta.ModelUsed))
	fmt.Fprintf(&b, "  review_ms:     %d\n", meta.ReviewMS)
	crit, maj, min := countSeverities(pr.PlanFindings)
	fmt.Fprintf(&b, "  plan_findings: %d (%d/%d/%d)\n", len(pr.PlanFindings), crit, maj, min)
	for _, f := range pr.PlanFindings {
		fmt.Fprintf(&b, "    - [%s][%s] %s — %s\n", f.Severity, f.Category,
			escapeContinuationLines(f.Criterion, "      "), formatFindingEvidence(f.Evidence, "      "))
	}
	fmt.Fprintf(&b, "  tasks: %d\n", len(pr.Tasks))
	for _, t := range pr.Tasks {
		tCrit, tMaj, tMin := countSeverities(t.Findings)
		fmt.Fprintf(&b, "    Task %d: %s  [%s]  findings: %d (%d/%d/%d)\n",
			t.TaskIndex, escapeContinuationLines(t.TaskTitle, "      "), t.Verdict, len(t.Findings), tCrit, tMaj, tMin)
		for _, f := range t.Findings {
			fmt.Fprintf(&b, "      - [%s] %s — %s\n", f.Severity,
				escapeContinuationLines(f.Criterion, "        "), formatFindingEvidence(f.Evidence, "        "))
		}
	}
	fmt.Fprintf(&b, "  next_action:   %s\n", escapeBlockValue(pr.NextAction))
	return b.String()
}

// formatPrimeSummary renders a deterministic, paste-ready text block for a
// PrimeResult (prime_project_knowledge). Mirrors formatEnvelopeSummary's
// conventions but emits a `picks:` list and an optional `bm_commands:` count
// in place of session/TTL data, since prime is stateless.
func formatPrimeSummary(r verdict.PrimeResult, modelUsed string, reviewMS int64) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope (prime_project_knowledge)\n")
	fmt.Fprintf(&b, "  verdict:       %s\n", r.Verdict)
	if r.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", escapeBlockValue(modelUsed))
	fmt.Fprintf(&b, "  review_ms:     %d\n", reviewMS)
	writeFindingsSummary(&b, r.Findings, "  ")
	fmt.Fprintf(&b, "  picks: %d\n", len(r.Picks))
	for _, p := range r.Picks {
		fmt.Fprintf(&b, "    - [%s] %s — %s\n", p.Priority,
			escapeContinuationLines(p.Permalink, "      "),
			escapeContinuationLines(truncate(p.Reason, summaryEvidenceMax), "      "))
	}
	if len(r.BMCommands) > 0 {
		fmt.Fprintf(&b, "  bm_commands: %d\n", len(r.BMCommands))
	}
	fmt.Fprintf(&b, "  next_action:   %s\n", escapeBlockValue(r.NextAction))
	return b.String()
}

// formatExtractSummary renders a deterministic, paste-ready text block for an
// ExtractResult (extract_project_knowledge). Mirrors formatPrimeSummary but
// emits a `proposals:` list (one bullet per Proposal showing action/type/
// permalink/rationale) and an optional `bm_commands:` count in place of
// session/TTL data, since extract is stateless.
func formatExtractSummary(r verdict.ExtractResult, modelUsed string, reviewMS int64) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope (extract_project_knowledge)\n")
	fmt.Fprintf(&b, "  verdict:       %s\n", r.Verdict)
	if r.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", escapeBlockValue(modelUsed))
	fmt.Fprintf(&b, "  review_ms:     %d\n", reviewMS)
	writeFindingsSummary(&b, r.Findings, "  ")
	fmt.Fprintf(&b, "  proposals: %d\n", len(r.Proposals))
	for _, p := range r.Proposals {
		fmt.Fprintf(&b, "    - [%s] %s %s — %s\n", p.Action, p.Type,
			escapeContinuationLines(p.Permalink, "      "),
			escapeContinuationLines(truncate(p.Rationale, summaryEvidenceMax), "      "))
	}
	if len(r.BMCommands) > 0 {
		fmt.Fprintf(&b, "  bm_commands: %d\n", len(r.BMCommands))
	}
	fmt.Fprintf(&b, "  next_action:   %s\n", escapeBlockValue(r.NextAction))
	return b.String()
}

// writeFindingsSummary writes the `findings: N total (C critical, M major, m minor)`
// summary line and one bullet per finding to b, prefixed with the supplied
// indent. Shared by formatEnvelopeSummary so the layout stays identical.
func writeFindingsSummary(b *strings.Builder, findings []verdict.Finding, indent string) {
	crit, maj, min := countSeverities(findings)
	fmt.Fprintf(b, "%sfindings:      %d total (%d critical, %d major, %d minor)\n", indent, len(findings), crit, maj, min)
	for _, f := range findings {
		// Criterion is reviewer-authored free text (schema: minLength 1, no
		// other constraint), same as Evidence. Escape it too: unlike
		// Evidence, Criterion was never truncated or re-indented, so an
		// embedded newline in it used to land at true column 0 — a cleaner
		// forgery vector than Evidence's (whitespace-only, pre-fix) indent.
		criterion := escapeContinuationLines(f.Criterion, indent+"    ")
		fmt.Fprintf(b, "%s  - [%s][%s] %s — %s\n", indent, f.Severity, f.Category, criterion, formatFindingEvidence(f.Evidence, indent+"    "))
	}
}

// formatFindingEvidence renders a finding's Evidence for the paste-ready
// summary block. Single-line evidence is truncated at summaryEvidenceMax
// runes (unchanged behavior). Multi-line evidence — used today by the
// validate_plan unverifiable-claim rollup which lists one task per line — is
// truncated per-line, then escaped via escapeContinuationLines so a
// continuation line sits visually under the bullet text AND can never be
// mistaken for one of the block's own label lines.
func formatFindingEvidence(evidence, contIndent string) string {
	if !strings.Contains(evidence, "\n") {
		return truncate(evidence, summaryEvidenceMax)
	}
	lines := strings.Split(evidence, "\n")
	for i, ln := range lines {
		lines[i] = truncate(ln, summaryEvidenceMax)
	}
	return escapeContinuationLines(strings.Join(lines, "\n"), contIndent)
}

// escapeContinuationLines is this package's entry point to
// blocktext.EscapeContinuationLines — see that function for the mechanic (a
// non-whitespace "| " sentinel on every continuation line) and for why it is
// hygiene rather than a security boundary.
//
// THE INVARIANT IT SERVES, which is wider than any one field: EVERY
// plain-string value rendered by ANY formatter in this file goes through this
// function (usually via escapeBlockValue). All four formatters here emit a
// header carrying the substring plugin/anti-tangent-guard's hook keys on, so
// escaping one of them and not its siblings closes nothing — that was
// task-12c's defect, exploited end-to-end through formatPlanSummary's
// Finding.Criterion. The same treatment is owed by any renderer whose output
// becomes a summary_block, in this package or not: internal/planrun's
// plan-run report is the other one today.
//
// summary_forgery_test.go enumerates the summary-block producers from the
// repository's own source — by what they are ASSIGNED TO, not by what their
// header literal says — and drives a forged payload through every
// plain-string field of each one's input, so neither a new producer nor a new
// field can be added without escaping and stay green. Read that file's header
// for what it does and does not cover.
//
// Named string types (verdict.Verdict, Severity, Category, PlanQuality,
// ProposalAction, ProposalType) are deliberately NOT escaped and are
// excluded from that test: every parser in internal/verdict rejects a value
// outside its enum before a formatter ever sees it, so they cannot carry a
// newline. Plain `string` fields have no such gate and are all escaped —
// including server-set ones (tool, model_used, plan_run_id), where the
// escape is a free no-op today and removes the need to re-audit if the
// field's provenance ever changes.
func escapeContinuationLines(s, contIndent string) string {
	return blocktext.EscapeContinuationLines(s, contIndent)
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
