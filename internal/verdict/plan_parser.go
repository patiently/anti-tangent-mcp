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
	for i, f := range r.PlanFindings {
		if err := validateFinding(f, fmt.Sprintf("plan_findings[%d]", i)); err != nil {
			return PlanResult{}, err
		}
	}
	for i, t := range r.Tasks {
		prefix := fmt.Sprintf("task[%d]", i)
		if err := validatePlanVerdict(t.Verdict, prefix+".verdict"); err != nil {
			return PlanResult{}, err
		}
		for j, f := range t.Findings {
			if err := validateFinding(f, fmt.Sprintf("%s.findings[%d]", prefix, j)); err != nil {
				return PlanResult{}, err
			}
		}
	}
	return r, nil
}

func validatePlanVerdict(v Verdict, where string) error {
	switch v {
	case VerdictPass, VerdictWarn, VerdictFail:
		return nil
	}
	return fmt.Errorf("plan: invalid %s %q", where, v)
}

func validateFinding(f Finding, where string) error {
	switch f.Severity {
	case SeverityCritical, SeverityMajor, SeverityMinor:
	default:
		return fmt.Errorf("plan: %s.severity invalid %q", where, f.Severity)
	}
	if !validCategory(f.Category) {
		return fmt.Errorf("plan: %s.category invalid %q", where, f.Category)
	}
	return nil
}
