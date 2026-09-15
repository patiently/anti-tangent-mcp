package mcpsrv

import (
	"fmt"
	"sort"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

const (
	maxFindingResponseEntries  = 50
	maxFindingResponseChars    = 2000
	maxControllerRulingEntries = 50
	maxControllerRulingChars   = 2000
)

// FindingResponseArg is one finding_responses entry on validate_completion.
type FindingResponseArg struct {
	FindingID string `json:"finding_id" jsonschema:"The id of a finding from this task's last validate_completion response that was not partial, exactly as that response showed it."`
	Response  string `json:"response" jsonschema:"Why the finding is wrong or already addressed, citing what the reviewer misread. At most 2000 characters."`
}

// ControllerRulingArg is one controller_rulings entry, on validate_completion
// and on validate_plan.
type ControllerRulingArg struct {
	FindingID string `json:"finding_id" jsonschema:"The id of the finding the controller ruled on, exactly as a response showed it."`
	Ruling    string `json:"ruling" jsonschema:"The controller's ruling, verbatim. At most 2000 characters."`
}

// normalizeFindingResponses trims each entry and drops one whose id or answer
// is empty. An answer over the character cap, or more entries than the entry
// cap, is an argument error.
func normalizeFindingResponses(in []FindingResponseArg) ([]FindingResponseArg, error) {
	out := make([]FindingResponseArg, 0, len(in))
	for i, e := range in {
		id, text := strings.TrimSpace(e.FindingID), strings.TrimSpace(e.Response)
		if id == "" || text == "" {
			continue
		}
		if len([]rune(text)) > maxFindingResponseChars {
			return nil, fmt.Errorf("finding_responses[%d].response must be at most %d characters", i, maxFindingResponseChars)
		}
		out = append(out, FindingResponseArg{FindingID: id, Response: text})
		if len(out) > maxFindingResponseEntries {
			return nil, fmt.Errorf("finding_responses must contain at most %d entries", maxFindingResponseEntries)
		}
	}
	return out, nil
}

// normalizeControllerRulings trims each entry and drops one whose id or ruling
// is empty. A ruling over the character cap, or more entries than the entry
// cap, is an argument error.
func normalizeControllerRulings(in []ControllerRulingArg) ([]ControllerRulingArg, error) {
	out := make([]ControllerRulingArg, 0, len(in))
	for i, e := range in {
		id, text := strings.TrimSpace(e.FindingID), strings.TrimSpace(e.Ruling)
		if id == "" || text == "" {
			continue
		}
		if len([]rune(text)) > maxControllerRulingChars {
			return nil, fmt.Errorf("controller_rulings[%d].ruling must be at most %d characters", i, maxControllerRulingChars)
		}
		out = append(out, ControllerRulingArg{FindingID: id, Ruling: text})
		if len(out) > maxControllerRulingEntries {
			return nil, fmt.Errorf("controller_rulings must contain at most %d entries", maxControllerRulingEntries)
		}
	}
	return out, nil
}

// fingerprintOf is a session-tool finding's fingerprint; its task key is empty.
func fingerprintOf(f verdict.Finding) string {
	return verdict.Fingerprint(f.Category, "", f.Criterion)
}

// sameAsID returns the ID a finding's same_as names when the prompt showed
// that ID, and "" otherwise: a same_as naming anything else counts as null.
func sameAsID(f verdict.Finding, shown map[string]bool) string {
	if f.SameAs == nil || !shown[*f.SameAs] {
		return ""
	}
	return *f.SameAs
}

// waiveRuled splits fs into the findings no ruling covers and waived entries
// for the rest. A finding is covered when its fingerprint carries a ruling, or
// when its same_as names a shown finding whose fingerprint does. Pass only
// reviewer findings: a server finding reports something a resubmission fixes,
// which no ruling settles.
func waiveRuled(fs []verdict.Finding, taskKey string, rulings map[string]session.Ruling, shown map[string]bool) ([]verdict.Finding, []verdict.WaivedFinding) {
	if len(rulings) == 0 {
		return fs, nil
	}
	kept := make([]verdict.Finding, 0, len(fs))
	var waived []verdict.WaivedFinding
	for _, f := range fs {
		r, ok := rulings[verdict.Fingerprint(f.Category, taskKey, f.Criterion)]
		if !ok {
			if id := sameAsID(f, shown); id != "" {
				r, ok = rulings[verdict.BaseID(id)]
			}
		}
		if !ok {
			kept = append(kept, f)
			continue
		}
		waived = append(waived, verdict.WaivedFinding{
			Severity:  f.Severity,
			Category:  f.Category,
			Criterion: f.Criterion,
			Evidence:  f.Evidence,
			Ruling:    r.Text,
		})
	}
	return kept, waived
}

// markRepeats sets RepeatOf on every finding that raises again a prior
// finding this call answered — matched by fingerprint, or by a same_as naming
// it — and returns the prior IDs its critical and major repeats raise again,
// each once, in order. It clears same_as on every finding once read.
func markRepeats(fs []verdict.Finding, prior []prompts.PriorFinding, shown map[string]bool) []string {
	answered := map[string]bool{}
	answeredByFingerprint := map[string]string{}
	for _, p := range prior {
		if p.Response == "" {
			continue
		}
		answered[p.ID] = true
		if fp := fingerprintOf(p.Finding); answeredByFingerprint[fp] == "" {
			answeredByFingerprint[fp] = p.ID
		}
	}
	var escalate []string
	for i := range fs {
		f := &fs[i]
		if id := sameAsID(*f, shown); id != "" && answered[id] {
			f.RepeatOf = id
		} else if id := answeredByFingerprint[fingerprintOf(*f)]; id != "" {
			f.RepeatOf = id
		}
		f.SameAs = nil
		if f.RepeatOf != "" && (f.Severity == verdict.SeverityCritical || f.Severity == verdict.SeverityMajor) {
			escalate = appendUnique(escalate, f.RepeatOf)
		}
	}
	return escalate
}

// completionReview is what one validate_completion call brings to its review
// from its session and from its finding_responses and controller_rulings.
type completionReview struct {
	// prior is the stored prior findings no ruling covers, each with this
	// call's answer to it.
	prior []prompts.PriorFinding
	// majorPre is the major pre-task findings no ruling covers.
	majorPre []verdict.Finding
	// rulings is every ruling in force for this review, by fingerprint.
	rulings map[string]session.Ruling
	// newRulings is what this call adds or replaces, written after the review.
	newRulings map[string]session.Ruling
	// shown is every ID the prompt shows — prior and major pre-task findings,
	// and every ruling — so a same_as naming any other ID is ignored.
	shown map[string]bool
	// advisories report argument entries the server ignored.
	advisories []verdict.Finding
}

// buildCompletionReview matches this call's answers to the stored prior
// findings and its rulings to the IDs the session issued. known is every
// finding the session still holds, which lets a new ruling say what it rules
// on.
func buildCompletionReview(state session.ReviewState, preFindings, known []verdict.Finding, responses []FindingResponseArg, rulings []ControllerRulingArg) completionReview {
	cr := completionReview{
		rulings:    make(map[string]session.Ruling, len(state.Rulings)),
		newRulings: map[string]session.Ruling{},
		shown:      map[string]bool{},
	}
	for fp, r := range state.Rulings {
		cr.rulings[fp] = r
	}

	var unknownRulings, overCap []string
	for _, e := range rulings {
		if !state.IssuedIDs[e.FindingID] {
			unknownRulings = appendUnique(unknownRulings, e.FindingID)
			continue
		}
		fp := verdict.BaseID(e.FindingID)
		if _, exists := cr.rulings[fp]; !exists && len(cr.rulings) >= session.MaxRulings {
			overCap = appendUnique(overCap, e.FindingID)
			continue
		}
		r := session.Ruling{ID: e.FindingID, Text: e.Ruling}
		for _, f := range known {
			if f.ID == e.FindingID {
				r.Category, r.Criterion = f.Category, f.Criterion
				break
			}
		}
		cr.rulings[fp] = r
		cr.newRulings[fp] = r
	}
	for _, r := range cr.rulings {
		cr.shown[r.ID] = true
	}

	answers := map[string]string{}
	for _, e := range responses {
		answers[e.FindingID] = e.Response
	}
	priorIDs := map[string]bool{}
	for _, f := range state.PriorFindings {
		priorIDs[f.ID] = true
		if _, ruled := cr.rulings[fingerprintOf(f)]; ruled {
			continue
		}
		cr.prior = append(cr.prior, prompts.PriorFinding{Finding: f, Response: answers[f.ID]})
		cr.shown[f.ID] = true
	}
	var unknownResponses []string
	for _, e := range responses {
		if !priorIDs[e.FindingID] {
			unknownResponses = appendUnique(unknownResponses, e.FindingID)
		}
	}

	for _, f := range preFindings {
		if f.Severity != verdict.SeverityMajor {
			continue
		}
		if _, ruled := cr.rulings[fingerprintOf(f)]; ruled {
			continue
		}
		cr.majorPre = append(cr.majorPre, f)
		cr.shown[f.ID] = true
	}

	if len(unknownResponses) > 0 {
		cr.advisories = append(cr.advisories, ignoredArgumentAdvisory("finding_responses",
			"These finding_responses ids match no finding from this task's last complete validate_completion review, so they were ignored: "+
				strings.Join(unknownResponses, ", ")+".",
			"Answer the ids shown in the last validate_completion response that was not partial."))
	}
	if len(unknownRulings) > 0 {
		cr.advisories = append(cr.advisories, ignoredArgumentAdvisory("controller_rulings",
			"These controller_rulings ids were never issued on this session, so they were ignored: "+
				strings.Join(unknownRulings, ", ")+".",
			"Copy each finding id exactly as a response on this task's session showed it; an id from another session or from validate_plan does not apply here."))
	}
	if len(overCap) > 0 {
		cr.advisories = append(cr.advisories, ignoredArgumentAdvisory("controller_rulings",
			fmt.Sprintf("This session already holds %d rulings, the most it keeps, so these new rulings were ignored: %s.",
				session.MaxRulings, strings.Join(overCap, ", ")),
			"Rule only on findings that block the task, or start a new validate_task_spec session for it."))
	}
	return cr
}

// ignoredArgumentAdvisory reports argument entries the server ignored. It is a
// minor other finding appended after the verdict is finalized, so it never
// changes the verdict.
func ignoredArgumentAdvisory(criterion, evidence, suggestion string) verdict.Finding {
	return verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  criterion,
		Evidence:   evidence,
		Suggestion: suggestion,
	}
}

// noSessionRulingsAdvisory reports finding_responses or controller_rulings
// sent on a call with no session_id.
func noSessionRulingsAdvisory() verdict.Finding {
	return ignoredArgumentAdvisory("session_id",
		"finding_responses and controller_rulings were sent without a session_id; without a session there is no earlier review to answer or rule on, so they were ignored.",
		"Pass the session_id from this task's validate_task_spec call.")
}

// escalationNextAction is prefixed onto next_action when a critical or major
// finding raises again a prior finding the implementer answered.
func escalationNextAction(ids []string) string {
	return "Stop resubmitting: report " + strings.Join(ids, ", ") +
		" and your responses to your controller for a ruling, then resubmit with controller_rulings. Then: "
}

// rulingsForPrompt lists rulings in ID order, so a prompt renders them
// deterministically.
func rulingsForPrompt(rulings map[string]session.Ruling) []session.Ruling {
	out := make([]session.Ruling, 0, len(rulings))
	for _, r := range rulings {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// knownSessionFindings is every finding the session still holds: pre-task,
// every checkpoint's, and the stored prior findings.
func knownSessionFindings(sess *session.Session, state session.ReviewState) []verdict.Finding {
	out := append([]verdict.Finding(nil), sess.PreFindings...)
	for _, cp := range sess.Checkpoints {
		out = append(out, cp.Findings...)
	}
	return append(out, state.PriorFindings...)
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}
