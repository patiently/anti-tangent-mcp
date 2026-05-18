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

// suppressTestabilityExtractionScopeDrift drops any scope_drift finding whose
// evidence matches a testability_extractions entry by substring in either
// direction (entry is substring of evidence OR evidence is substring of
// entry). Non-scope_drift findings pass through unchanged. Empty extractions
// short-circuits to the input slice (no allocation).
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
		matched := false
		for _, e := range extractions {
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
