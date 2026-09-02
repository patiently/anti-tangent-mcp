package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
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

// DemoteUnattachedContradictions rewrites every contradicted_codebase_claim
// in pr that NO attached file backs into an unverifiable_codebase_claim,
// floored to minor. attachedPaths is this call's attached set (absolute
// paths); an empty set demotes every contradiction.
//
// contradicted_codebase_claim exists specifically because an attached file is
// ground truth read from disk, which is why — unlike
// unverifiable_codebase_claim — it carries no severity floor and is not
// force-passed by the unverifiable-only verdict calibration. With no file
// behind it the category is a hallucination wearing a hard severity: it can
// fail a plan gate on a claim about code the reviewer never saw.
//
// Per-finding, not per-call: gating the whole sweep on "nothing was attached"
// made the suppression all-or-nothing, so a single attached file re-armed the
// unfloored severity for a contradiction about some OTHER file the reviewer
// had never been shown.
//
// The match is deliberately conservative — a finding survives when its
// evidence OR its criterion names ANY attached path's BASENAME. Both fields
// are read because reviewers put the path in either one; the sibling guard
// suppressUnverifiableCodebaseClaim (mcpsrv/task_spec_normalize.go) has read
// both since it existed, and controller.md documents that shape. Reading
// evidence alone meant a correct, ground-truth refutation whose path sat in
// `criterion` was demoted to a minor unverifiable claim, which
// calibratePlanVerdictForUnverifiableOnly then force-passes — the refutation
// disappeared from the gate verdict entirely.
//
// The ground rules require the reviewer to quote the attached file's absolute
// path, but reviewers routinely quote the repo-relative form instead, so
// basename matching fails OPEN: a legitimate contradiction is kept even when
// the quoting is loose, and only a finding naming none of the attached files
// is demoted. The match is on the WHOLE filename, though, not on raw
// containment — see mentionsBasename.
//
// The prompt already tells the reviewer never to emit it about an unattached
// file, but suppression must run SERVER-SIDE, independent of reviewer
// compliance (controller.md §5.8). Demotion, not deletion: the observation
// may still be worth surfacing, just at the severity a claim nobody could
// check deserves.
func DemoteUnattachedContradictions(pr *PlanResult, attachedPaths []string) {
	bases := make([]string, 0, len(attachedPaths))
	for _, p := range attachedPaths {
		if b := path.Base(filepath.ToSlash(p)); b != "" && b != "." && b != "/" {
			bases = append(bases, b)
		}
	}
	backed := func(f Finding) bool {
		for _, b := range bases {
			if mentionsBasename(f.Evidence, b) || mentionsBasename(f.Criterion, b) {
				return true
			}
		}
		return false
	}
	demote := func(fs []Finding) {
		for i := range fs {
			if fs[i].Category != CategoryContradictedCodebaseClaim {
				continue
			}
			if backed(fs[i]) {
				continue
			}
			fs[i].Category = CategoryUnverifiableCodebaseClaim
			fs[i] = applySeverityFloor(fs[i])
		}
	}
	demote(pr.PlanFindings)
	for i := range pr.Tasks {
		demote(pr.Tasks[i].Findings)
	}
}

func validatePlanVerdict(v Verdict, where string) error {
	switch v {
	case VerdictPass, VerdictWarn, VerdictFail:
		return nil
	}
	return fmt.Errorf("plan: invalid %s %q", where, v)
}

// validateFinding validates severity and category. It also applies the
// per-category severity floor in-place so plan-shape parsers behave
// identically to the per-task parser.
func validateFinding(f *Finding, where string) error {
	switch f.Severity {
	case SeverityCritical, SeverityMajor, SeverityMinor:
	default:
		return fmt.Errorf("plan: %s.severity invalid %q", where, f.Severity)
	}
	if !validCategory(f.Category) {
		return fmt.Errorf("plan: %s.category invalid %q", where, f.Category)
	}
	*f = applySeverityFloor(*f)
	if err := validateFindingStrings(*f, where); err != nil {
		return fmt.Errorf("plan: %w", err)
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

// mentionsBasename reports whether text names base as a whole filename
// rather than as a fragment of a longer one.
//
// BOTH boundaries are checked, and both are load-bearing. Raw containment
// (strings.Contains) accepted `config.golden` as a mention of `config.go`,
// and a before-only boundary test still does: the character that produces
// that false positive is the one AFTER the basename (`l`), not the one
// before it. Half the rule is no rule.
//
// The boundary test is a DENYLIST, not an allowlist: a boundary holds unless
// the adjacent rune could itself be part of an unbroken filename token (see
// isFilenameRune). An allowlist of the punctuation reviewers were observed
// using was tried first and was wrong in the expensive direction — every
// character nobody thought of became a false negative, so markdown emphasis
// (`**config.go**`), brackets (`[config.go]`), a fragment anchor
// (`config.go#L42`), a semicolon, angle brackets and an em-dash all bound to
// nothing. Inverting the test makes the unforeseen case fail OPEN, which is
// the stance this guard is supposed to take.
//
// A false NEGATIVE here is the expensive direction: it demotes a ground-truth
// refutation to a minor unverifiable claim, which the unverifiable-only
// calibration then force-passes. A false positive merely keeps a finding at
// the severity the reviewer chose.
//
// The match is on the basename, so it is deliberately NOT whole-FILENAME
// matching: `.` is not a filename-token rune, so an attached `config.go`
// also binds to a mention of `config.go.bak` or `config.go.orig`. That is
// accepted for the same fail-open reason — a sibling backup name is a far
// cheaper wrong answer than dropping a real refutation on the floor.
func mentionsBasename(text, base string) bool {
	if base == "" || text == "" {
		return false
	}
	for off := 0; off+len(base) <= len(text); {
		i := strings.Index(text[off:], base)
		if i < 0 {
			return false
		}
		start := off + i
		end := start + len(base)
		if basenameStartsAt(text[:start]) && basenameEndsAt(text[end:]) {
			return true
		}
		off = start + 1
	}
	return false
}

// basenameStartsAt reports whether prefix — everything before a candidate
// match — ends at a filename boundary. Anything that is not a filename-token
// rune is a boundary, so a path separator (`/` or the `\` a Windows host
// renders), a backtick, a quote, an opening bracket or paren, markdown
// emphasis, whitespace, and every punctuation mark nobody enumerated all
// bind.
func basenameStartsAt(prefix string) bool {
	if prefix == "" {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(prefix)
	return !isFilenameRune(r)
}

// basenameEndsAt reports whether suffix — everything after a candidate match
// — begins at a filename boundary. This is the half the original fix
// proposal omitted, and the half that rejects `config.golden` (`l` is a
// filename rune) and `myconfig.go` from the other side.
func basenameEndsAt(suffix string) bool {
	if suffix == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(suffix)
	return !isFilenameRune(r)
}

// isFilenameRune reports whether r can be part of an unbroken filename token:
// a letter, a digit, `_`, or `-`. Only these break a boundary.
//
// `.` is deliberately absent — a period is how a filename ends a sentence,
// and treating it as a token rune would reject `…refuted by config.go.` The
// price is that `config.go.bak` also binds; see mentionsBasename.
//
// `_` and `-` ARE token runes, because Go source files in this repository are
// snake_case: admitting `_` as a boundary would bind a mention of
// `plan_parser.go` to an attached `parser.go`. The cost is that markdown
// underscore emphasis (`_config.go_`) still does not bind; asterisk emphasis
// (`**config.go**`), which is what reviewers actually emit, does.
func isFilenameRune(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
