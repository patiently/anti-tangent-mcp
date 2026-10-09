// Package mcpsrv: agent-network mode. A plan that declares
// `**Plan kind:** agent-network`, a task that declares `**Kind:** experiment`,
// and a call that sends boundary_rules are reviewed for work that belongs to
// the model rather than to code. This file holds the server's side of that:
// argument limits, the findings the server adds, and the ones it removes.
package mcpsrv

import (
	"fmt"
	"slices"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

const (
	maxBoundaryRules     = 20
	maxBoundaryRuleChars = 1000
	// fixLadderCriterion marks the boundary_violation an agent-network plan's
	// review raises for a fix on the wrong rung. It needs no boundary rule.
	fixLadderCriterion = "fix_ladder"
	// experimentLightweightReason is the lightweight_reason the server sets
	// when it forces an experiment task out of lightweight mode.
	experimentLightweightReason = "experiment task"
	// agentNoteValueMax bounds, in runes, how much of an unknown header or
	// argument value a note repeats back.
	agentNoteValueMax = 64
)

func normalizeBoundaryRules(rules []string) ([]string, error) {
	return normalizeBoundedStringList("boundary_rules", rules, maxBoundaryRules, maxBoundaryRuleChars)
}

// dropUnrequestedBoundaryViolations removes the boundary_violation findings a
// call without boundary rules cannot have asked for: the reviewer had no rule
// to judge against. On an agent-network plan the fix_ladder finding is kept,
// since the fix ladder is the plan kind's own rule.
func dropUnrequestedBoundaryViolations(fs []verdict.Finding, hasRules, agentNetwork bool) []verdict.Finding {
	if hasRules || len(fs) == 0 {
		return fs
	}
	out := make([]verdict.Finding, 0, len(fs))
	for _, f := range fs {
		fixLadder := strings.EqualFold(strings.TrimSpace(f.Criterion), fixLadderCriterion)
		if f.Category == verdict.CategoryBoundaryViolation && !(agentNetwork && fixLadder) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// applyPlanAgentNetwork runs on a plan review's merged results before the
// verdict ladder. It removes unrequested boundary findings, records each
// task's kind, rung and plan kind from the parsed plan, and takes every
// experiment task out of lightweight mode, whatever the reviewer returned.
func applyPlanAgentNetwork(pr *verdict.PlanResult, tasks []planparser.RawTask, planKind string, rules []string) {
	agentNetwork := planKind == planparser.PlanKindAgentNetwork
	hasRules := len(rules) > 0
	pr.PlanFindings = dropUnrequestedBoundaryViolations(pr.PlanFindings, hasRules, agentNetwork)
	for i, idx := range parsedTaskIndexes(pr.Tasks, tasks) {
		t := &pr.Tasks[i]
		t.Findings = dropUnrequestedBoundaryViolations(t.Findings, hasRules, agentNetwork)
		t.PlanKind = planKind
		if idx < 0 {
			continue
		}
		t.TaskKind, t.Rung = declaredKind(planKind, tasks[idx]), tasks[idx].Rung
		if t.TaskKind == planparser.TaskKindExperiment {
			t.LightweightEligible = false
			t.LightweightReason = experimentLightweightReason
		}
	}
}

// declaredKind is the task kind the plan declares for t. On a plan with no
// **Plan kind:** header the parser's default, build, declares nothing, so a
// plain plan's results and run carry no task kind and a per-task call's own
// task_kind still counts.
func declaredKind(planKind string, t planparser.RawTask) string {
	if planKind == "" && t.Kind == planparser.TaskKindBuild {
		return ""
	}
	return t.Kind
}

// addPlanAgentNetworkNotes adds the minor notes about how the plan and this
// call declared their kind. They describe the declarations, not the plan's
// quality, so they join after the verdict ladder and never move the verdict.
func addPlanAgentNetworkNotes(pr *verdict.PlanResult, tasks []planparser.RawTask, planKind, unknownPlanKind string, rules []string) {
	if unknownPlanKind != "" {
		pr.PlanFindings = append(pr.PlanFindings, unknownPlanKindNote(unknownPlanKind))
	}
	pr.PlanFindings = append(pr.PlanFindings, declarationNotes(planKind, unknownPlanKind, len(rules) > 0)...)
	for i, idx := range parsedTaskIndexes(pr.Tasks, tasks) {
		if idx < 0 {
			continue
		}
		pr.Tasks[i].Findings = append(pr.Tasks[i].Findings, taskHeaderNotes(planKind != "" || unknownPlanKind != "", tasks[idx])...)
	}
}

// declarationNotes returns boundary_rules_missing for an agent-network plan
// reviewed without rules, and plan_kind_missing for rules sent for a plan
// with no **Plan kind:** header.
func declarationNotes(planKind, unknownPlanKind string, hasRules bool) []verdict.Finding {
	switch {
	case planKind == planparser.PlanKindAgentNetwork && !hasRules:
		return []verdict.Finding{boundaryRulesMissingNote()}
	case planKind == "" && unknownPlanKind == "" && hasRules:
		return []verdict.Finding{planKindMissingNote()}
	}
	return nil
}

// taskHeaderNotes returns a parsed task's notes: an unknown **Kind:** or
// **Rung:** value, and an experiment with no rung. A plan with no
// **Plan kind:** header may use Kind and Rung labels of its own, so its
// unknown values raise nothing.
func taskHeaderNotes(hasPlanKind bool, t planparser.RawTask) []verdict.Finding {
	var out []verdict.Finding
	if hasPlanKind && t.UnknownKind != "" {
		out = append(out, unknownTaskKindNote(t.UnknownKind))
	}
	if hasPlanKind && t.UnknownRung != "" {
		out = append(out, unknownRungNote(t.UnknownRung))
	}
	if t.Kind == planparser.TaskKindExperiment && t.Rung == "" {
		out = append(out, rungMissingNote())
	}
	return out
}

func agentNote(cat verdict.Category, criterion, evidence, suggestion string) verdict.Finding {
	return verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   cat,
		Criterion:  criterion,
		Evidence:   evidence,
		Suggestion: suggestion,
	}
}

func quoteValue(v string) string {
	return fmt.Sprintf("%q", truncate(strings.TrimSpace(v), agentNoteValueMax))
}

func unknownPlanKindNote(v string) verdict.Finding {
	return agentNote(verdict.CategoryUnknownKind, "plan_kind",
		fmt.Sprintf("**Plan kind:** %s is not a plan kind anti-tangent knows, so the plan is reviewed as an ordinary plan.", quoteValue(v)),
		"Write `**Plan kind:** agent-network` above the first task heading, or remove the line.")
}

func unknownTaskKindNote(v string) verdict.Finding {
	return agentNote(verdict.CategoryUnknownKind, "task_kind",
		fmt.Sprintf("**Kind:** %s is not a task kind anti-tangent knows, so the task is reviewed as a build task.", quoteValue(v)),
		"Write `**Kind:** experiment` or `**Kind:** build`.")
}

func unknownRungNote(v string) verdict.Finding {
	return agentNote(verdict.CategoryUnknownKind, "rung",
		fmt.Sprintf("**Rung:** %s is not a fix-ladder rung, so the task is treated as naming none.", quoteValue(v)),
		"Name one of: "+strings.Join(planparser.Rungs, ", ")+".")
}

func rungMissingNote() verdict.Finding {
	return agentNote(verdict.CategoryRungMissing, "rung",
		"This experiment task names no fix-ladder rung, so the plan's fix ladder cannot place it.",
		"Add a `**Rung:**` line naming the rung the experiment works at: "+strings.Join(planparser.Rungs, ", ")+".")
}

func boundaryRulesMissingNote() verdict.Finding {
	return agentNote(verdict.CategoryBoundaryRulesMissing, "boundary_rules",
		"This is agent-network work, but the call sent no boundary_rules, so nothing is checked against a boundary rule.",
		"Pass the project's boundary rules as boundary_rules on validate_plan, or on validate_task_spec and a lightweight validate_completion; a session carries them to its later calls.")
}

func planKindMissingNote() verdict.Finding {
	return agentNote(verdict.CategoryPlanKindMissing, "plan_kind",
		"This call sent boundary_rules for work with no plan kind. The rules are checked, but the determinism, rate, experiment and fix-ladder checks stay off.",
		"If this is agent-network work, add `**Plan kind:** agent-network` above the plan's first task heading, or pass plan_kind.")
}

// agentNetworkArgs are a per-task call's agent-network declarations, as sent
// or as resolved against its plan run. TaskKind "" means a build task.
type agentNetworkArgs struct {
	TaskKind      string
	Rung          string
	PlanKind      string
	BoundaryRules []string
	KindFromRun   bool
}

// normalizeAgentNetworkArgs trims and lower-cases the declared kinds and
// bounds boundary_rules. An unknown kind or rung is dropped and noted; only
// the rules' limits are argument errors.
func normalizeAgentNetworkArgs(in agentNetworkArgs) (agentNetworkArgs, []verdict.Finding, error) {
	rules, err := normalizeBoundaryRules(in.BoundaryRules)
	if err != nil {
		return agentNetworkArgs{}, nil, err
	}
	out := agentNetworkArgs{BoundaryRules: rules}
	var notes []verdict.Finding
	switch v := strings.ToLower(strings.TrimSpace(in.TaskKind)); v {
	case "", planparser.TaskKindBuild, planparser.TaskKindExperiment:
		out.TaskKind = v
	default:
		notes = append(notes, unknownArgNote("task_kind", v, "experiment or build"))
	}
	switch v := strings.ToLower(strings.TrimSpace(in.PlanKind)); v {
	case "", planparser.PlanKindAgentNetwork:
		out.PlanKind = v
	default:
		notes = append(notes, unknownArgNote("plan_kind", v, planparser.PlanKindAgentNetwork))
	}
	if v := strings.ToLower(strings.TrimSpace(in.Rung)); v == "" || planparser.IsRung(v) {
		out.Rung = v
	} else {
		notes = append(notes, unknownArgNote("rung", v, "one of "+strings.Join(planparser.Rungs, ", ")))
	}
	return out, notes, nil
}

// resolveAgentNetwork decides a per-task call's mode. A call attached to a
// plan run takes the run's plan kind, and the task's kind and rung when the
// run's plan has the task; a declaration the call sent that differs is
// ignored and noted. Boundary rules the call sends are its own, noted when
// they differ from the run's, and a call that sends none takes the run's. A
// call attached to no run keeps its own.
func resolveAgentNetwork(args agentNetworkArgs, stored planrun.AgentNetwork, attached bool, runID string) (agentNetworkArgs, []verdict.Finding) {
	if !attached {
		return args, nil
	}
	out := args
	var notes []verdict.Finding
	conflict := func(field, sent, kept string) {
		if sent != "" && sent != kept {
			notes = append(notes, kindConflictNote(field, sent, kept, runID))
		}
	}
	conflict("plan_kind", args.PlanKind, stored.PlanKind)
	out.PlanKind = stored.PlanKind
	if stored.TaskKind != "" {
		conflict("task_kind", args.TaskKind, stored.TaskKind)
		conflict("rung", args.Rung, stored.Rung)
		out.TaskKind, out.Rung, out.KindFromRun = stored.TaskKind, stored.Rung, true
	}
	switch {
	case len(out.BoundaryRules) == 0:
		out.BoundaryRules = stored.BoundaryRules
	case len(stored.BoundaryRules) > 0 && !slices.Equal(out.BoundaryRules, stored.BoundaryRules):
		notes = append(notes, rulesOverrideNote(len(out.BoundaryRules), len(stored.BoundaryRules), runID))
	}
	return out, notes
}

// rulesOverrideNote reports a per-task call whose own boundary rules replace
// the different ones its plan run records.
func rulesOverrideNote(sent, stored int, runID string) verdict.Finding {
	return agentNote(verdict.CategoryKindConflict, "boundary_rules",
		fmt.Sprintf("This call sent %d boundary rules that differ from the %d plan run %s records; this task is reviewed against the call's rules only.", sent, stored, runID),
		"Leave boundary_rules out of per-task calls on a plan run, so the task inherits the run's rules, unless replacing them for this task is intended.")
}

// modeNotes returns the notes a resolved per-task mode draws: an experiment
// with no rung, an agent-network task without rules, and rules without a plan
// kind.
func modeNotes(m agentNetworkArgs) []verdict.Finding {
	var out []verdict.Finding
	if m.TaskKind == planparser.TaskKindExperiment && m.Rung == "" {
		out = append(out, rungMissingNote())
	}
	return append(out, declarationNotes(m.PlanKind, "", len(m.BoundaryRules) > 0)...)
}

func (m agentNetworkArgs) applyTo(spec *session.TaskSpec) {
	spec.TaskKind, spec.Rung, spec.PlanKind, spec.BoundaryRules = m.TaskKind, m.Rung, m.PlanKind, m.BoundaryRules
	spec.KindFromRun = m.KindFromRun
}

// sessionSpec returns the session's task spec with the task kind, rung and
// plan kind its plan run records now, so a plan revised after the task
// started is reviewed in its new mode. A kind the call declared itself stays
// unless the plan now declares one. Boundary rules stay as the task's
// validate_task_spec call resolved them.
func (h *handlers) sessionSpec(sess *session.Session) session.TaskSpec {
	spec := sess.Spec
	if sess.PlanRunID == "" {
		return spec
	}
	stored, ok := h.deps.PlanRuns.SessionAgentNetwork(sess.PlanRunID, sess.ID)
	if !ok {
		return spec
	}
	spec.PlanKind = stored.PlanKind
	if stored.TaskFound && (stored.TaskKind != "" || spec.KindFromRun) {
		spec.TaskKind, spec.Rung = stored.TaskKind, stored.Rung
		spec.KindFromRun = stored.TaskKind != ""
	}
	return spec
}

// capProgressBoundaryViolations lowers every boundary_violation to minor.
// check_progress sees whole files and no diff, so it cannot tell code the
// task added from code that was already there.
func capProgressBoundaryViolations(fs []verdict.Finding) []verdict.Finding {
	for i := range fs {
		if fs[i].Category == verdict.CategoryBoundaryViolation {
			fs[i].Severity = verdict.SeverityMinor
		}
	}
	return fs
}

// agentRejection returns the finding that rejects a validate_completion before
// review for an agent-network reason, and false when none applies: an
// experiment needs a session, and a boundary-checked or experiment task that
// sends files, or records its change as kept, must also send a diff, since
// the boundary check reads only the lines a diff adds.
func agentRejection(spec session.TaskSpec, lightweight bool, finalDiff string, files []FileArg, kept bool) (verdict.Finding, string, bool) {
	if lightweight && spec.Experiment() {
		return verdict.Finding{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryInsufficientEvidence,
			Criterion:  "session",
			Evidence:   "experiment tasks need a session; call validate_task_spec first",
			Suggestion: "Call validate_task_spec for this task, then validate_completion with the session_id it returns.",
		}, "Call validate_task_spec for this experiment task, then call validate_completion with its session_id.", true
	}
	if finalDiff == "" && (len(files) > 0 || kept) && (len(spec.BoundaryRules) > 0 || spec.Experiment()) {
		return diffRequiredFinding(), "Re-submit with final_diff or final_diff_path: the boundary check runs on the lines the diff adds.", true
	}
	return verdict.Finding{}, "", false
}

// boundaryUncheckedNote returns the note for a build task's completion that
// carries boundary rules but neither a diff nor files: the boundary check
// reads only added lines, so it checked nothing. A reverted experiment is
// exempt because it leaves no code to check.
func boundaryUncheckedNote(spec session.TaskSpec, finalDiff string, files []FileArg) (verdict.Finding, bool) {
	if finalDiff != "" || len(files) > 0 || len(spec.BoundaryRules) == 0 || spec.Experiment() {
		return verdict.Finding{}, false
	}
	return agentNote(verdict.CategoryBoundaryUnchecked, "final_diff",
		"This task carries boundary rules, but the call sent no diff, so its code was not checked against them.",
		"Send final_diff or final_diff_path with the task's change to have it checked against the boundary rules."), true
}

func diffRequiredFinding() verdict.Finding {
	return verdict.Finding{
		Severity:   verdict.SeverityMajor,
		Category:   verdict.CategoryDiffRequired,
		Criterion:  "final_diff",
		Evidence:   "the boundary check needs a diff: it judges only the lines a change adds, and whole files do not show which those are",
		Suggestion: "Send final_diff or final_diff_path with the task's change.",
	}
}

// agentRejectionEnvelope is validate_completion's answer to a call
// agentRejection refuses. No reviewer call is made and no session is written.
func agentRejectionEnvelope(sessionID, modelUsed string, lightweight bool, f verdict.Finding, nextAction string) Envelope {
	res := verdict.FinalizeVerdict(verdict.Result{Findings: []verdict.Finding{f}, NextAction: nextAction})
	return Envelope{
		Tool:                 "validate_completion",
		SessionID:            sessionID,
		Verdict:              string(res.Verdict),
		Findings:             res.Findings,
		NextAction:           res.NextAction,
		ModelUsed:            modelUsed,
		Lightweight:          lightweight,
		SubmissionDefectOnly: isSubmissionDefectOnly(res.Findings),
	}
}

func unknownArgNote(field, v, accepts string) verdict.Finding {
	return agentNote(verdict.CategoryUnknownKind, field,
		fmt.Sprintf("%s %s is not a value anti-tangent knows, so it is ignored.", field, quoteValue(v)),
		fmt.Sprintf("%s takes %s.", field, accepts))
}

func kindConflictNote(field, sent, kept, runID string) verdict.Finding {
	if kept == "" {
		kept = "none"
	}
	return agentNote(verdict.CategoryKindConflict, field,
		fmt.Sprintf("This call sent %s %s, but plan run %s records %s for this task; the run's value is used.", field, quoteValue(sent), runID, quoteValue(kept)),
		"Pass the value validate_plan returned for this task, or change the plan and run validate_plan again.")
}

// taskSpecMode is a validate_task_spec call's resolved mode, its notes, and
// the plan run it resolved against: the run it names, or the one its title
// finds (byTitle). The call attaches to that same run after its review.
type taskSpecMode struct {
	mode    agentNetworkArgs
	notes   []verdict.Finding
	runID   string
	byTitle bool
}

// taskSpecAgentNetwork resolves a validate_task_spec call's mode against the
// plan run it attaches to.
func (h *handlers) taskSpecAgentNetwork(args ValidateTaskSpecArgs) (taskSpecMode, error) {
	sent, notes, err := normalizeAgentNetworkArgs(agentNetworkArgs{
		TaskKind: args.TaskKind, Rung: args.Rung, PlanKind: args.PlanKind, BoundaryRules: args.BoundaryRules,
	})
	if err != nil {
		return taskSpecMode{}, err
	}
	runID, byTitle := h.taskSpecPlanRun(args)
	ref := planrun.TaskRef{Index: args.TaskIndex, Title: args.TaskTitle}
	if byTitle {
		ref.Index = 0
	}
	stored, attached := h.deps.PlanRuns.TaskAgentNetwork(runID, ref)
	mode, conflicts := resolveAgentNetwork(sent, stored, attached, runID)
	notes = append(notes, conflicts...)
	return taskSpecMode{mode: mode, notes: append(notes, modeNotes(mode)...), runID: runID, byTitle: byTitle}, nil
}

// lightweightAgentNetwork resolves a lightweight validate_completion's mode
// against the plan run its plan_run_id names.
func (h *handlers) lightweightAgentNetwork(args ValidateCompletionArgs) (agentNetworkArgs, []verdict.Finding, error) {
	sent, notes, err := normalizeAgentNetworkArgs(agentNetworkArgs{
		TaskKind: args.TaskKind, Rung: args.Rung, PlanKind: args.PlanKind, BoundaryRules: args.BoundaryRules,
	})
	if err != nil {
		return agentNetworkArgs{}, nil, err
	}
	runID := strings.TrimSpace(args.PlanRunID)
	stored, attached := h.deps.PlanRuns.TaskAgentNetwork(runID, planrun.TaskRef{Index: args.TaskIndex, Title: args.TaskTitle})
	mode, conflicts := resolveAgentNetwork(sent, stored, attached, runID)
	notes = append(notes, conflicts...)
	return mode, append(notes, modeNotes(mode)...), nil
}

// rateDigestNote reports a rate_digest the server dropped as malformed.
func rateDigestNote(problem string) verdict.Finding {
	return agentNote(verdict.CategoryOther, "rate_digest",
		"rate_digest was dropped: "+problem+".",
		"Send rate_digest with n and the counts as integers, or leave it out.")
}
