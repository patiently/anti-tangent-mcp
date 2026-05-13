package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParsePlan decodes provider output into a PlanResult and validates enum fields.
// It tolerates a ```json ... ``` wrapper and surrounding whitespace, and rejects
// any extra JSON after the single document.
func ParsePlan(raw []byte) (PlanResult, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r PlanResult
	if err := dec.Decode(&r); err != nil {
		return PlanResult{}, fmt.Errorf("decode plan result: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return PlanResult{}, fmt.Errorf("decode plan result: extra JSON after document")
	}
	if err := validatePlanVerdict(r.PlanVerdict, "plan_verdict"); err != nil {
		return PlanResult{}, err
	}
	if r.NextAction == "" {
		return PlanResult{}, fmt.Errorf("decode plan result: next_action is required")
	}
	for i := range r.PlanFindings {
		if err := validateFinding(&r.PlanFindings[i], fmt.Sprintf("plan_findings[%d]", i)); err != nil {
			return PlanResult{}, err
		}
	}
	for i := range r.Tasks {
		t := &r.Tasks[i]
		prefix := fmt.Sprintf("task[%d]", i)
		if err := validatePlanVerdict(t.Verdict, prefix+".verdict"); err != nil {
			return PlanResult{}, err
		}
		if t.TaskIndex < 0 {
			return PlanResult{}, fmt.Errorf("plan: %s.task_index must be >= 0, got %d", prefix, t.TaskIndex)
		}
		if t.TaskTitle == "" {
			return PlanResult{}, fmt.Errorf("plan: %s.task_title is required", prefix)
		}
		for j := range t.Findings {
			if err := validateFinding(&t.Findings[j], fmt.Sprintf("%s.findings[%d]", prefix, j)); err != nil {
				return PlanResult{}, err
			}
		}
	}
	ApplyPlanQualitySanity(&r)
	return r, nil
}

func validatePlanVerdict(v Verdict, where string) error {
	switch v {
	case VerdictPass, VerdictWarn, VerdictFail:
		return nil
	}
	return fmt.Errorf("plan: invalid %s %q", where, v)
}

// validateFinding validates severity and category. It also applies the
// unverifiable_codebase_claim severity floor in-place (forcing such
// findings to SeverityMinor) so plan-shape parsers behave identically
// to the per-task parser.
func validateFinding(f *Finding, where string) error {
	switch f.Severity {
	case SeverityCritical, SeverityMajor, SeverityMinor:
	default:
		return fmt.Errorf("plan: %s.severity invalid %q", where, f.Severity)
	}
	if !validCategory(f.Category) {
		return fmt.Errorf("plan: %s.category invalid %q", where, f.Category)
	}
	if f.Category == CategoryUnverifiableCodebaseClaim && f.Severity != SeverityMinor {
		f.Severity = SeverityMinor
	}
	return nil
}

// Rules apply in order; the first matching rule wins and later rules are not evaluated.
// ApplyPlanQualitySanity enforces the plan_quality contract:
//
//   - any critical finding forces "rough" regardless of what the reviewer emitted
//   - fail verdict forces "rough"
//   - empty/invalid value falls back to a verdict-based default:
//     pass → rigorous, warn → actionable, fail → rough
//
// This is defensive: the JSON schema requires plan_quality on the happy
// path, but raw-response drift (parse miss, prompt drift, missing field)
// must not produce empty output.
func ApplyPlanQualitySanity(pr *PlanResult) {
	if pr.PlanVerdict == VerdictFail {
		pr.PlanQuality = PlanQualityRough
		return
	}
	hasCritical := false
	for _, f := range pr.PlanFindings {
		if f.Severity == SeverityCritical {
			hasCritical = true
			break
		}
	}
	if !hasCritical {
		for _, t := range pr.Tasks {
			for _, f := range t.Findings {
				if f.Severity == SeverityCritical {
					hasCritical = true
					break
				}
			}
			if hasCritical {
				break
			}
		}
	}
	if hasCritical {
		pr.PlanQuality = PlanQualityRough
		return
	}
	switch pr.PlanQuality {
	case PlanQualityRough, PlanQualityActionable, PlanQualityRigorous:
		// reviewer emitted a valid value; trust it.
	default:
		switch pr.PlanVerdict {
		case VerdictPass:
			pr.PlanQuality = PlanQualityRigorous
		case VerdictWarn:
			pr.PlanQuality = PlanQualityActionable
		case VerdictFail:
			pr.PlanQuality = PlanQualityRough
		}
	}
}
