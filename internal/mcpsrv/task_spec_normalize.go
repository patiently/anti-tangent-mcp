package mcpsrv

import (
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func normalizeTaskSpecUnverifiableFindings(findings []verdict.Finding) []verdict.Finding {
	kept, evidence := splitTaskUnverifiable(findings)
	if len(evidence) == 0 {
		return kept
	}

	return append(kept, verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryUnverifiableCodebaseClaim,
		Criterion:  "codebase_reference_checklist",
		Evidence:   truncate(strings.Join(evidence, "; "), rollupEvidencePerTaskMax),
		Suggestion: "Pre-flight these references with grep or codebase-aware review before implementation. If they were already verified, treat this as a checklist rather than a spec-quality defect.",
	})
}

// suppressUnverifiableCodebaseClaim drops any unverifiable_codebase_claim
// finding whose evidence OR criterion substring-matches any CVR entry (either
// direction: entry-is-substring-of-text OR text-is-substring-of-entry). It
// mirrors the prompt-side instruction in pre.tmpl §48 but provides
// deterministic, reviewer-compliance-independent behavior.
//
// Defensive guards mirror suppressTestabilityExtractionScopeDrift:
//   - empty CVR or empty findings short-circuits to the input slice
//   - empty/whitespace-only evidence AND criterion is treated as non-match
//     (avoids the strings.Contains(non_empty, "") trap)
//   - CVR entries shorter than 4 code points are skipped (avoid single-letter
//     false matches like a CVR entry of "T" swallowing every claim)
//
// Findings whose category is NOT unverifiable_codebase_claim pass through
// unchanged.
func suppressUnverifiableCodebaseClaim(findings []verdict.Finding, cvr []string) []verdict.Finding {
	if len(cvr) == 0 || len(findings) == 0 {
		return findings
	}
	usable := make([]string, 0, len(cvr))
	for _, e := range cvr {
		if len([]rune(e)) >= 4 {
			usable = append(usable, e)
		}
	}
	if len(usable) == 0 {
		return findings
	}
	out := make([]verdict.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Category != verdict.CategoryUnverifiableCodebaseClaim {
			out = append(out, f)
			continue
		}
		evidence := strings.TrimSpace(f.Evidence)
		criterion := strings.TrimSpace(f.Criterion)
		if evidence == "" && criterion == "" {
			out = append(out, f)
			continue
		}
		matched := false
		for _, e := range usable {
			if evidence != "" && (strings.Contains(evidence, e) || strings.Contains(e, evidence)) {
				matched = true
				break
			}
			if criterion != "" && (strings.Contains(criterion, e) || strings.Contains(e, criterion)) {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, f)
		}
	}
	return out
}

// suppressTestabilityExtractionScopeDrift drops any scope_drift finding whose
// evidence matches a testability_extractions entry by substring in either
// direction (entry is substring of evidence OR evidence is substring of
// entry). Non-scope_drift findings pass through unchanged. Empty extractions
// short-circuits to the input slice (no allocation).
//
// Empty/whitespace-only evidence on a scope_drift finding is treated as
// non-match: `strings.Contains(non_empty, "")` returns true, which would
// otherwise let a non-compliant reviewer that emits empty Evidence suppress
// every scope_drift finding whenever extractions is non-empty.
func suppressTestabilityExtractionScopeDrift(findings []verdict.Finding, extractions []string) []verdict.Finding {
	if len(extractions) == 0 || len(findings) == 0 {
		return findings
	}
	out := make([]verdict.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Category != verdict.CategoryScopeDrift {
			out = append(out, f)
			continue
		}
		if strings.TrimSpace(f.Evidence) == "" {
			out = append(out, f)
			continue
		}
		matched := false
		for _, e := range extractions {
			if e == "" {
				continue
			}
			if strings.Contains(f.Evidence, e) || strings.Contains(e, f.Evidence) {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, f)
		}
	}
	return out
}
