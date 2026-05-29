package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParseExtract decodes provider output into an ExtractResult, validating
// enums and rejecting extra/missing fields. Decodes into the wire-only
// `extractWire` struct so reviewer-emitted `summary_block` / `partial` are
// rejected as unknown — server-owned fields land on ExtractResult only via
// handler-side population.
func ParseExtract(raw []byte) (ExtractResult, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var w extractWire
	if err := dec.Decode(&w); err != nil {
		return ExtractResult{}, fmt.Errorf("decode extract result: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return ExtractResult{}, fmt.Errorf("decode extract result: extra JSON after document")
	}
	// Proposals is filled in after the per-proposal validation loop lifts
	// each proposalWire into a Proposal; leave it nil here so the existing
	// nil-rejection check below still distinguishes "field missing" from
	// "empty list" on the wire.
	r := ExtractResult{
		Verdict:    w.Verdict,
		Findings:   w.Findings,
		BMCommands: w.BMCommands,
		NextAction: w.NextAction,
		// SummaryBlock and Partial deliberately left zero — handler populates.
	}
	switch r.Verdict {
	case VerdictPass, VerdictWarn, VerdictFail:
	default:
		return ExtractResult{}, fmt.Errorf("invalid verdict %q", r.Verdict)
	}
	if r.NextAction == "" {
		return ExtractResult{}, fmt.Errorf("decode extract result: next_action is required")
	}
	if r.Findings == nil {
		return ExtractResult{}, fmt.Errorf("decode extract result: findings is required (use [] for none)")
	}
	if w.Proposals == nil {
		return ExtractResult{}, fmt.Errorf("decode extract result: proposals is required (use [] for none)")
	}
	if r.BMCommands == nil {
		return ExtractResult{}, fmt.Errorf("decode extract result: bm_commands is required (use [] for none)")
	}
	for i := range r.Findings {
		f := r.Findings[i]
		switch f.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return ExtractResult{}, fmt.Errorf("finding[%d]: invalid severity %q", i, f.Severity)
		}
		if !validCategory(f.Category) {
			return ExtractResult{}, fmt.Errorf("finding[%d]: invalid category %q", i, f.Category)
		}
		// Delegate the criterion/evidence/suggestion non-empty checks to the
		// shared validateFindingStrings helper added in Task 1 step 5a.
		if err := validateFindingStrings(f, fmt.Sprintf("finding[%d]", i)); err != nil {
			return ExtractResult{}, err
		}
		// Delegate to the canonical severity-floor helper (see Task 5 §3 for
		// the same call in ParsePrime). applySeverityFloor floors BOTH
		// unverifiable_codebase_claim AND convention_deviation to minor.
		r.Findings[i] = applySeverityFloor(r.Findings[i])
	}
	proposals := make([]Proposal, 0, len(w.Proposals))
	for i, p := range w.Proposals {
		switch p.Action {
		case ProposalActionCreate, ProposalActionUpdate, ProposalActionSupersede:
		default:
			return ExtractResult{}, fmt.Errorf("proposal[%d]: invalid action %q", i, p.Action)
		}
		switch p.Type {
		case ProposalTypeDecision, ProposalTypeModule, ProposalTypeFeature, ProposalTypeGlossary, ProposalTypeEpic, ProposalTypeStory, ProposalTypeGotcha, ProposalTypeHowto:
		default:
			return ExtractResult{}, fmt.Errorf("proposal[%d]: invalid type %q", i, p.Type)
		}
		// howto is a slug-keyed living document, updated in place — it is
		// never superseded (design spec §6.4). Reject any howto proposal
		// carrying a supersede action; create/update with empty supersedes
		// are already enforced by the action-conditional checks below.
		if p.Type == ProposalTypeHowto && p.Action == ProposalActionSupersede {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: howto notes are update-in-place and cannot be superseded", i)
		}
		// PRESENCE CHECKS first — schema-required fields must be present
		// (the reviewer emits placeholders when empty). Run these before
		// action-conditional checks so a missing field gets the actionable
		// "use [] / use \"{}\" for none" hint instead of being swallowed by
		// a generic action=supersede check (len(nil) == 0, so the action
		// check below would otherwise match a literally-missing supersedes).
		// The *string-typed wire fields let us distinguish missing (== nil)
		// from present-but-empty (!= nil, deref == ""); the strict-mode
		// schema requires all four to be present, so missing is a hard fail.
		if p.Permalink == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: permalink is required", i)
		}
		if p.Title == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: title is required", i)
		}
		if p.Rationale == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: rationale is required", i)
		}
		if len(p.EvidenceRefs) == 0 {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: evidence_refs is required (must be non-empty)", i)
		}
		// frontmatter_json / body / body_patch must be PRESENT on the wire
		// (non-nil pointer). Empty-string value is acceptable as a placeholder.
		if p.FrontmatterJSON == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: frontmatter_json is required (use \"{}\" for none)", i)
		}
		if p.Body == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: body is required (use \"\" for none)", i)
		}
		if p.BodyPatch == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: body_patch is required (use \"\" for none)", i)
		}
		// Then validate frontmatter_json content shape ("{}" string is the
		// minimal valid placeholder; reviewer must not emit "null" or non-object).
		if *p.FrontmatterJSON == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: frontmatter_json must not be empty string (use \"{}\" for none)", i)
		}
		var fmProbe map[string]any
		if err := json.Unmarshal([]byte(*p.FrontmatterJSON), &fmProbe); err != nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: frontmatter_json is not a JSON object: %w", i, err)
		}
		if fmProbe == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: frontmatter_json must be a JSON object literal (use \"{}\" for none, not \"null\")", i)
		}
		if p.Supersedes == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: supersedes is required (use [] for none)", i)
		}
		// ACTION-CONDITIONAL checks run last. By this point all four wire
		// pointers are non-nil and Supersedes is non-nil; the *string deref
		// is safe. body/body_patch can both be empty strings (acceptable
		// for action=supersede).
		body := *p.Body
		bodyPatch := *p.BodyPatch
		if p.Action == ProposalActionCreate && body == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: action=create requires non-empty body", i)
		}
		// NOTE: the following two checks compare the local `body` and
		// `bodyPatch` strings (set above to `*p.Body` and `*p.BodyPatch`),
		// NOT the *string fields on p directly. `p.Body != ""` would be a
		// type error (cannot compare *string to ""); we deref once above
		// and then operate on the values.
		if p.Action == ProposalActionUpdate && body == "" && bodyPatch == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: action=update requires body or body_patch", i)
		}
		if body != "" && bodyPatch != "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: body and body_patch are mutually exclusive", i)
		}
		if p.Action == ProposalActionSupersede && len(p.Supersedes) == 0 {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: action=supersede requires non-empty supersedes (use a permalink list, not [])", i)
		}
		// Lift the validated wire entry into a Proposal.
		proposals = append(proposals, Proposal{
			Action:          p.Action,
			Type:            p.Type,
			Permalink:       p.Permalink,
			Title:           p.Title,
			FrontmatterJSON: *p.FrontmatterJSON,
			Body:            body,
			BodyPatch:       bodyPatch,
			Rationale:       p.Rationale,
			EvidenceRefs:    p.EvidenceRefs,
			Supersedes:      p.Supersedes,
		})
	}
	r.Proposals = proposals
	for i, c := range r.BMCommands {
		if c.Tool == "" {
			return ExtractResult{}, fmt.Errorf("bm_commands[%d]: tool is required", i)
		}
		// args_json must be a JSON object literal. Empty-object `{}` is
		// acceptable; anything else (array, scalar, `null`, malformed) is
		// rejected. The nil-map check below catches `null`, which would
		// otherwise unmarshal successfully into a nil map[string]any.
		if c.ArgsJSON == "" {
			return ExtractResult{}, fmt.Errorf("bm_commands[%d]: args_json is required (use \"{}\" for none)", i)
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(c.ArgsJSON), &probe); err != nil {
			return ExtractResult{}, fmt.Errorf("bm_commands[%d]: args_json is not a JSON object: %w", i, err)
		}
		if probe == nil {
			return ExtractResult{}, fmt.Errorf("bm_commands[%d]: args_json must be a JSON object literal (use \"{}\" for none, not \"null\")", i)
		}
	}
	return r, nil
}
