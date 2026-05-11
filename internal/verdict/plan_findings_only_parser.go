package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParsePlanFindingsOnly decodes a Pass-1 reviewer response into a PlanFindingsOnly
// and validates enum fields. It uses DisallowUnknownFields so a payload that
// contains a top-level "tasks" key (or any undeclared field) is rejected.
func ParsePlanFindingsOnly(raw []byte) (PlanFindingsOnly, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r PlanFindingsOnly
	if err := dec.Decode(&r); err != nil {
		return PlanFindingsOnly{}, fmt.Errorf("decode plan_findings_only: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return PlanFindingsOnly{}, fmt.Errorf("decode plan_findings_only: extra JSON after document")
	}
	switch r.PlanVerdict {
	case VerdictPass, VerdictWarn, VerdictFail:
	default:
		return PlanFindingsOnly{}, fmt.Errorf("plan_findings_only: invalid plan_verdict %q", r.PlanVerdict)
	}
	if r.PlanFindings == nil {
		return PlanFindingsOnly{}, fmt.Errorf("plan_findings_only: plan_findings is required")
	}
	if r.NextAction == "" {
		return PlanFindingsOnly{}, fmt.Errorf("plan_findings_only: next_action must be non-empty")
	}
	for i, f := range r.PlanFindings {
		if err := validateFinding(f, fmt.Sprintf("plan_findings[%d]", i)); err != nil {
			return PlanFindingsOnly{}, err
		}
	}
	return r, nil
}
