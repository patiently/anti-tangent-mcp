package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParsePrime decodes provider output into a PrimeResult, validating enum
// fields and rejecting extra/missing fields. Tolerates ```json fences.
// Decodes into the wire-only `primeWire` struct so reviewer-emitted
// `summary_block` / `partial` fields are rejected as unknown — server-owned
// fields land on PrimeResult only via handler-side population.
func ParsePrime(raw []byte) (PrimeResult, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var w primeWire
	if err := dec.Decode(&w); err != nil {
		return PrimeResult{}, fmt.Errorf("decode prime result: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return PrimeResult{}, fmt.Errorf("decode prime result: extra JSON after document")
	}
	r := PrimeResult{
		Verdict:    w.Verdict,
		Findings:   w.Findings,
		Picks:      w.Picks,
		BMCommands: w.BMCommands,
		NextAction: w.NextAction,
		// SummaryBlock and Partial deliberately left zero — handler populates.
	}
	switch r.Verdict {
	case VerdictPass, VerdictWarn, VerdictFail:
	default:
		return PrimeResult{}, fmt.Errorf("invalid verdict %q", r.Verdict)
	}
	if r.NextAction == "" {
		return PrimeResult{}, fmt.Errorf("decode prime result: next_action is required")
	}
	if r.Findings == nil {
		return PrimeResult{}, fmt.Errorf("decode prime result: findings is required (use [] for none)")
	}
	if r.Picks == nil {
		return PrimeResult{}, fmt.Errorf("decode prime result: picks is required (use [] for none)")
	}
	if r.BMCommands == nil {
		return PrimeResult{}, fmt.Errorf("decode prime result: bm_commands is required (use [] for none)")
	}
	for i := range r.Findings {
		f := r.Findings[i]
		switch f.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return PrimeResult{}, fmt.Errorf("finding[%d]: invalid severity %q", i, f.Severity)
		}
		if !validCategory(f.Category) {
			return PrimeResult{}, fmt.Errorf("finding[%d]: invalid category %q", i, f.Category)
		}
		// Delegate the criterion/evidence/suggestion non-empty checks to the
		// shared validateFindingStrings helper added in Task 1 step 5a. This
		// keeps the rule in one place and matches Parse / ParsePlan /
		// ParseTasksOnly.
		if err := validateFindingStrings(f, fmt.Sprintf("finding[%d]", i)); err != nil {
			return PrimeResult{}, err
		}
		// Delegate to the canonical severity-floor helper in parser_partial.go
		// so the prime parser stays in lock-step with the per-task parser.
		// applySeverityFloor floors BOTH unverifiable_codebase_claim AND
		// convention_deviation to minor; duplicating the rule here would drift.
		r.Findings[i] = applySeverityFloor(r.Findings[i])
	}
	for i, p := range r.Picks {
		switch p.Priority {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return PrimeResult{}, fmt.Errorf("pick[%d]: invalid priority %q", i, p.Priority)
		}
		if p.Permalink == "" {
			return PrimeResult{}, fmt.Errorf("pick[%d]: permalink is required", i)
		}
		if p.Reason == "" {
			return PrimeResult{}, fmt.Errorf("pick[%d]: reason is required", i)
		}
	}
	for i, c := range r.BMCommands {
		if c.Tool == "" {
			return PrimeResult{}, fmt.Errorf("bm_commands[%d]: tool is required", i)
		}
		// args_json must be a JSON object literal (BM tool args are always
		// an object). Empty-object `{}` is acceptable; anything else (array,
		// scalar, `null`, malformed JSON) is rejected with an actionable error.
		if c.ArgsJSON == "" {
			return PrimeResult{}, fmt.Errorf("bm_commands[%d]: args_json is required (use \"{}\" for none)", i)
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(c.ArgsJSON), &probe); err != nil {
			return PrimeResult{}, fmt.Errorf("bm_commands[%d]: args_json is not a JSON object: %w", i, err)
		}
		// json.Unmarshal of `null` into a map[string]any pointer succeeds
		// with probe == nil, which would silently slip past as "valid JSON
		// object." Reject explicitly — the contract is a JSON object literal.
		if probe == nil {
			return PrimeResult{}, fmt.Errorf("bm_commands[%d]: args_json must be a JSON object literal (use \"{}\" for none, not \"null\")", i)
		}
	}
	return r, nil
}
