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
