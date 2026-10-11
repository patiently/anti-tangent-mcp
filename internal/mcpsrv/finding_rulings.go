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
	// maxEchoedIDRunes caps a caller-supplied finding_id at the length a
	// legitimate display ID could ever reach before it is echoed into an
	// advisory's evidence. Unlike response/ruling text, finding_id has no
	// length cap of its own — an unknown or malformed id is still accepted so
	// its advisory can name it — so this bounds only what gets echoed back.
	maxEchoedIDRunes = 32
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
// findings a ruling can settle: the reviewer's, and a deterministic finding a
// controller can prove wrong, such as validate_plan's task-order check. An
// advisory about this call's own input reports something a resubmission fixes,
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

// carriedMinors is the previous call's minor findings that a minor finding
// on this call can raise again, answered or not. Each is matched once, whether
// through take or, for an answered one, through drop: a fingerprint is only a
// category and a criterion, and the completion prompt pins some criteria
// (comment_hygiene, correctness), so two new findings can share a fingerprint
// with one old one. Matching each old finding once keeps the second from
// being read as already reported.
type carriedMinors struct {
	left []prompts.PriorFinding
}

// take returns the ID of the prior minor finding f raises again — the one its
// same_as names, else the first with its fingerprint — and removes it, or
// returns "" when none is left.
func (c *carriedMinors) take(f verdict.Finding, shown map[string]bool) string {
	named, fp, pick := sameAsID(f, shown), fingerprintOf(f), -1
	for i, p := range c.left {
		if named != "" && p.ID == named {
			pick = i
			break
		}
		if pick < 0 && fingerprintOf(p.Finding) == fp {
			pick = i
		}
	}
	if pick < 0 {
		return ""
	}
	id := c.left[pick].ID
	c.left = append(c.left[:pick], c.left[pick+1:]...)
	return id
}

// drop removes the prior finding with the given ID and reports whether it was
// still there. An answered prior minor is consumed through it, so it is matched
// once like the rest.
func (c *carriedMinors) drop(id string) bool {
	for i, p := range c.left {
		if p.ID == id {
			c.left = append(c.left[:i], c.left[i+1:]...)
			return true
		}
	}
	return false
}

// markRepeats sets RepeatOf on every finding that raises a prior finding
// again — matched by a same_as naming it, or by fingerprint — in two cases: a
// finding of any severity when this call answered the prior one, and a minor
// finding that raises a prior minor finding, answered or not. The second case
// is what lets FinalizeVerdict leave a carried-over nit out of the minor
// count. A minor takes an answered prior of any severity at most once, so a
// fingerprint shared by several new minors cannot hide them all; critical and
// major findings may always take it. An unanswered critical or major repeat
// stays unmarked: it is an open finding, not a dispute. Returns the prior IDs
// the critical and major repeats raise again, each once, in order, and clears
// same_as on every finding once read.
func markRepeats(fs []verdict.Finding, prior []prompts.PriorFinding, shown map[string]bool) []string {
	answered := map[string]bool{}
	answeredByFingerprint := map[string]string{}
	var carried carriedMinors
	priorMinor := map[string]bool{}
	takenByMinor := map[string]bool{}
	for _, p := range prior {
		if p.Severity == verdict.SeverityMinor {
			priorMinor[p.ID] = true
			carried.left = append(carried.left, p)
		}
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
		id := ""
		if named := sameAsID(*f, shown); named != "" && answered[named] {
			id = named
		} else {
			id = answeredByFingerprint[fingerprintOf(*f)]
		}
		isMinor := f.Severity == verdict.SeverityMinor
		if id != "" && isMinor {
			if priorMinor[id] {
				if !carried.drop(id) {
					id = ""
				}
			} else if takenByMinor[id] {
				id = ""
			} else {
				takenByMinor[id] = true
			}
		}
		if id != "" {
			f.RepeatOf = id
		} else if isMinor {
			f.RepeatOf = carried.take(*f, shown)
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
	// preToVerify is the pre-task findings the final review verifies, every
	// major one, every ambiguous_spec one, and every quality/over_building
	// one, that no ruling covers. A minor ambiguous_spec finding is there so
	// the reviewer can tell a known spec ambiguity from a deviation the
	// implementer chose; a minor over_building finding is there so a
	// completion over_building finding can name it in same_as when the
	// structure it named got built.
	preToVerify []verdict.Finding
	// rulings is every ruling in force for this review, by fingerprint.
	rulings map[string]session.Ruling
	// newRulings is what this call adds or replaces, written after the review.
	newRulings map[string]session.Ruling
	// shown is every ID the prompt shows — prior and pre-task findings,
	// and every ruling — so a same_as naming any other ID is ignored.
	shown map[string]bool
	// preShown is the subset of shown that names a pre-task finding rather
	// than a prior completion-round finding or a ruling. A same_as naming an
	// ID in preShown identifies a genuine pre-task link rather than a repeat
	// the implementer answered, so it survives to the response instead of
	// being cleared with every other same_as.
	preShown map[string]bool
	// advisories report argument entries the server ignored.
	advisories []verdict.Finding
}

// verifiedAtCompletion reports whether a pre-task finding is one the final
// review verifies: every major finding, every ambiguous_spec finding
// regardless of severity — a minor ambiguous_spec finding lets the reviewer
// tell a known spec ambiguity from a deviation the implementer chose — and
// every quality/over_building finding regardless of severity, so a
// completion over_building finding can name it in same_as when the
// structure it named got built.
func verifiedAtCompletion(f verdict.Finding) bool {
	if f.Severity == verdict.SeverityMajor {
		return true
	}
	if f.Category == verdict.CategoryAmbiguousSpec {
		return true
	}
	return isOverBuilding(f.Category, f.Criterion)
}

// overBuildingCriterion is the criterion every over-building finding carries.
const overBuildingCriterion = "over_building"

// isOverBuilding reports whether a category and criterion are the reviewer's
// over-building finding. Criterion is free text on the wire, so case and
// surrounding space are ignored.
func isOverBuilding(category verdict.Category, criterion string) bool {
	return category == verdict.CategoryQuality &&
		strings.ToLower(strings.TrimSpace(criterion)) == overBuildingCriterion
}

// overBuildingCompanion is the major finding that accompanies an
// over_building finding raised again with nobody having answered it. An
// over_building finding is minor by template, so without the companion
// nothing obliges an implementer to act on one. priorID is the earlier
// finding it names.
func overBuildingCompanion(priorID string) verdict.Finding {
	return verdict.Finding{
		Severity:  verdict.SeverityMajor,
		Category:  verdict.CategoryUnaddressed,
		Criterion: overBuildingCriterion,
		Evidence: "The over_building finding " + priorID + " from an earlier validate_completion call is raised " +
			"again on this one, and no finding_responses entry has answered it.",
		Suggestion: "Cut the structure that finding names, or answer " + priorID + " in finding_responses with " +
			"the reason it stays. A controller ruling on " + priorID + " also settles it.",
	}
}

// overBuildingReview is what one validate_completion review shows about
// over-building.
type overBuildingReview struct {
	// priorID is the over_building finding the previous complete review
	// raised, or "" when it raised none.
	priorID string
	// open reports whether this review raises an over_building finding, which
	// is the implementer's to act on. Structure an acceptance criterion
	// mandated arrives under its own criterion, addressed to the plan author,
	// and is not one.
	open bool
	// settled reports whether the implementer or the controller has answered
	// for the structure: a finding_responses answer on this call or an earlier
	// one, or a ruling on the companion finding.
	settled bool
}

// reviewOverBuilding reads the over-building state of one review. reviewer is
// the reviewer's findings after the ruling waiver. answeredBefore is the
// session's memory of an earlier answer.
func reviewOverBuilding(reviewer []verdict.Finding, cr completionReview, answeredBefore bool) overBuildingReview {
	ob := overBuildingReview{settled: answeredBefore}
	for _, p := range cr.prior {
		if !isOverBuilding(p.Category, p.Criterion) {
			continue
		}
		if ob.priorID == "" {
			ob.priorID = p.ID
		}
		ob.settled = ob.settled || p.Response != ""
	}
	if _, ruled := cr.rulings[fingerprintOf(overBuildingCompanion(""))]; ruled {
		ob.settled = true
	}
	for _, f := range reviewer {
		if isOverBuilding(f.Category, f.Criterion) {
			ob.open = true
		}
	}
	return ob
}

// answersOverBuilding reports whether this call's finding_responses answered
// an over_building finding, which the session then remembers.
func answersOverBuilding(prior []prompts.PriorFinding) bool {
	for _, p := range prior {
		if isOverBuilding(p.Category, p.Criterion) && p.Response != "" {
			return true
		}
	}
	return false
}

// companion returns the finding to add when an open over_building finding is
// raised again and nobody has answered for it.
func (ob overBuildingReview) companion() (verdict.Finding, bool) {
	if !ob.open || ob.settled || ob.priorID == "" {
		return verdict.Finding{}, false
	}
	return overBuildingCompanion(ob.priorID), true
}

// ruled reports whether the call settled an over_building finding without
// cutting the structure: a controller ruling waived it, or it is open and
// answered for.
func (ob overBuildingReview) ruled(waived []verdict.WaivedFinding) bool {
	for _, w := range waived {
		if isOverBuilding(w.Category, w.Criterion) {
			return true
		}
	}
	return ob.open && ob.settled
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
		preShown:   map[string]bool{},
	}
	for fp, r := range state.Rulings {
		cr.rulings[fp] = r
	}

	var unknownRulings, overCap []string
	for _, e := range rulings {
		if !state.IssuedIDs[e.FindingID] {
			unknownRulings = appendUnique(unknownRulings, truncate(e.FindingID, maxEchoedIDRunes))
			continue
		}
		fp := verdict.BaseID(e.FindingID)
		if _, exists := cr.rulings[fp]; !exists && len(cr.rulings) >= session.MaxRulings {
			overCap = appendUnique(overCap, truncate(e.FindingID, maxEchoedIDRunes))
			continue
		}
		r := session.Ruling{ID: e.FindingID, Text: e.Ruling}
		found := false
		for _, f := range known {
			if f.ID == e.FindingID {
				r.Category, r.Criterion = f.Category, f.Criterion
				found = true
				break
			}
		}
		// An issued ID not among known findings names a finding a prior ruling
		// already waived off of known: fall back to the existing ruling on the
		// same fingerprint, which always carries the category and criterion,
		// since a ruling waived it.
		if !found {
			if existing, ok := cr.rulings[fp]; ok {
				r.Category, r.Criterion = existing.Category, existing.Criterion
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
			unknownResponses = appendUnique(unknownResponses, truncate(e.FindingID, maxEchoedIDRunes))
		}
	}

	for _, f := range preFindings {
		if !verifiedAtCompletion(f) {
			continue
		}
		if _, ruled := cr.rulings[fingerprintOf(f)]; ruled {
			continue
		}
		cr.preToVerify = append(cr.preToVerify, f)
		cr.shown[f.ID] = true
		cr.preShown[f.ID] = true
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

// noSessionResponsesAdvisory reports finding_responses sent without a session.
// A response answers a finding the reviewer raised in an earlier review of the
// same session; without one there is no earlier review, and the server has no
// finding to show. A controller ruling settles a finding here when the task is
// in a plan run.
func noSessionResponsesAdvisory() verdict.Finding {
	return ignoredArgumentAdvisory("session_id",
		"finding_responses were sent without a session_id; without a session there is no earlier review to answer, so they were ignored.",
		"Fix the finding, or, on a task in a plan run, have the controller rule on it and resubmit with plan_run_id, task_index and controller_rulings.")
}

// escalationNextAction is prefixed onto next_action when a critical or major
// finding raises again a prior finding the implementer answered.
func escalationNextAction(ids []string) string {
	return "Stop resubmitting: report " + strings.Join(ids, ", ") +
		" and your responses to your controller for a ruling, then resubmit with controller_rulings. Then: "
}

// AppliedRuling is one controller ruling a validate_completion review applied.
type AppliedRuling struct {
	FindingID string `json:"finding_id"`
	Ruling    string `json:"ruling"`
}

// appliedRulings lists the rulings a review applied, in finding_id order and
// with their full text. The summary block shows each one, so a ruling the
// reviewer obeyed, which leaves no waived entry, is still in front of the
// controller at DONE.
func appliedRulings(rulings map[string]session.Ruling) []AppliedRuling {
	sorted := rulingsForPrompt(rulings)
	if len(sorted) == 0 {
		return nil
	}
	out := make([]AppliedRuling, 0, len(sorted))
	for _, r := range sorted {
		out = append(out, AppliedRuling{FindingID: r.ID, Ruling: r.Text})
	}
	return out
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
// every checkpoint's, and the stored prior findings. It reads state rather
// than a live *session.Session because a concurrent check_progress call
// mutates the session's checkpoints outside this call's lock.
func knownSessionFindings(state session.ReviewState) []verdict.Finding {
	out := append([]verdict.Finding(nil), state.PreFindings...)
	for _, cp := range state.CheckpointFindings {
		out = append(out, cp...)
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

// planRulings keys validate_plan's rulings by fingerprint. The tool keeps no
// session, so there is no issued-ID set to check an ID against: only its shape
// is checked, and a malformed ID is returned for the advisory. When one call
// rules twice on a fingerprint, the last entry wins.
func planRulings(in []ControllerRulingArg) (map[string]session.Ruling, []string) {
	out := map[string]session.Ruling{}
	var malformed []string
	for _, e := range in {
		if !verdict.ValidDisplayID(e.FindingID) {
			malformed = appendUnique(malformed, truncate(e.FindingID, maxEchoedIDRunes))
			continue
		}
		out[verdict.BaseID(e.FindingID)] = session.Ruling{ID: e.FindingID, Text: e.Ruling}
	}
	return out, malformed
}

// malformedPlanRulingsAdvisory reports validate_plan rulings whose ID is not a
// finding id. A well-formed ruling that waives nothing draws no advisory: a
// reviewer that honours the rendered ruling and leaves the finding out is the
// ruling working.
func malformedPlanRulingsAdvisory(ids []string) verdict.Finding {
	return ignoredArgumentAdvisory("controller_rulings",
		"These controller_rulings ids are not finding ids, so they were ignored: "+strings.Join(ids, ", ")+".",
		"Copy each id exactly as a validate_plan response showed it: f_ and eight hex digits, with an optional -n suffix.")
}

// lightweightRulings builds a lightweight validate_completion review's ruling
// state. A lightweight task has no session, so its plan-run row stands in for
// one: a ruling applies only when its base ID is among issued, the IDs earlier
// lightweight calls on that row returned. shown is the issued set, so a
// reviewer's same_as reaches a finding only through an ID the server showed.
// unissued lists the well-formed ruling IDs that were dropped, sorted.
func lightweightRulings(rulingArgs []ControllerRulingArg, issued []string) (rulings map[string]session.Ruling, shown map[string]bool, malformed, unissued []string) {
	all, malformed := planRulings(rulingArgs)
	shown = make(map[string]bool, len(issued))
	issuedBase := make(map[string]bool, len(issued))
	for _, id := range issued {
		shown[id] = true
		issuedBase[verdict.BaseID(id)] = true
	}
	rulings = make(map[string]session.Ruling, len(all))
	for base, r := range all {
		if issuedBase[base] {
			rulings[base] = r
			continue
		}
		unissued = appendUnique(unissued, truncate(r.ID, maxEchoedIDRunes))
	}
	sort.Strings(unissued)
	return rulings, shown, malformed, unissued
}

// unboundLiteRulingsAdvisory reports controller_rulings on a lightweight call
// that names no plan-run task: no earlier call issued an ID they could name.
func unboundLiteRulingsAdvisory() verdict.Finding {
	return ignoredArgumentAdvisory("controller_rulings",
		"controller_rulings on a lightweight validate_completion apply only to finding ids an earlier lightweight validate_completion returned for the same plan_run_id and task; this call names no such task, so they were ignored.",
		"Resubmit with plan_run_id and task_index (or task_title) naming the task whose earlier validate_completion returned the ids.")
}

// unissuedLiteRulingsAdvisory reports lightweight ruling IDs no earlier call
// on the row returned. The IDs passed ValidDisplayID, so they are safe to echo.
func unissuedLiteRulingsAdvisory(ids []string) verdict.Finding {
	return ignoredArgumentAdvisory("controller_rulings",
		"These controller_rulings ids were not returned by an earlier validate_completion on this task, so they were ignored: "+strings.Join(ids, ", ")+".",
		"Rule only on ids a validate_completion response for this task showed.")
}
