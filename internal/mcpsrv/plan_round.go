// Package mcpsrv: validate_plan rounds. A round that names an earlier round's
// plan run re-reviews only what changed since that round and carries the
// rest.
package mcpsrv

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// planReview is what a plan run keeps of its plan's latest complete review:
// what was reviewed, as hashes, and what the reviewer returned for it, before
// any ruling, verified reference or verdict ladder was applied. Keeping the
// reviewer's own output is what lets a later round apply that round's rulings
// to a carried task.
//
// A stored planReview is shared between calls and must never be changed:
// every reader copies what it takes.
type planReview struct {
	// InputsKey covers everything but the plan text that the reviewer was
	// shown. A round whose key differs carries nothing.
	InputsKey string
	// PlanKey covers the plan text: the text outside every task and each
	// task's hash, in order.
	PlanKey string
	// TaskHashes holds one hash per parsed task, in plan order, and Tasks the
	// reviewer's result for the task at the same position, nil when the
	// reviewer returned none.
	TaskHashes []string
	Tasks      []*verdict.PlanTaskResult

	PlanVerdict  verdict.Verdict
	PlanQuality  verdict.PlanQuality
	PlanFindings []verdict.Finding
	NextAction   string
	ModelUsed    string
}

// planInputs is what, besides the plan text, decides a plan review's result.
// Rulings and verified references are absent on purpose: the server applies
// both to the reviewer's output on every round, so a round that adds one
// still carries its unchanged tasks. repo_root is absent because the check it
// gates runs on every round without the reviewer.
type planInputs struct {
	ProjectKnowledge string
	Mode             string
	Model            string
	ContextFiles     []fileSource
	// PlanKind and BoundaryRules render in every prompt of the review, so a
	// change to either reviews every task again.
	PlanKind      string
	BoundaryRules []string
}

// hashFields hashes fields as the bytes they are, each behind its length, so
// a field boundary is part of the hash and no byte is rewritten first: a JSON
// encoding would replace every invalid UTF-8 byte with U+FFFD and give two
// texts that differ only in such bytes one hash.
func hashFields(fields ...string) string {
	h := sha256.New()
	var size [8]byte
	for _, f := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(f)))
		h.Write(size[:])
		io.WriteString(h, f)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// key identifies the inputs by what the reviewer is asked, not by how the
// caller spelled it: an omitted mode is the thorough review, and the attached
// files count as a set, whatever order context_paths named them in.
func (in planInputs) key() string {
	mode := in.Mode
	if mode == "" {
		mode = "thorough"
	}
	files := append([]fileSource(nil), in.ContextFiles...)
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	fields := []string{"plan-round-v1", in.ProjectKnowledge, mode, in.Model}
	for _, f := range files {
		fields = append(fields, f.Path, strconv.Itoa(f.Bytes), f.SHA256)
	}
	// Appended only when set, so the key of a review that uses neither does
	// not depend on them.
	if in.PlanKind != "" || len(in.BoundaryRules) > 0 {
		fields = append(fields, "agent-network", in.PlanKind, strconv.Itoa(len(in.BoundaryRules)))
		fields = append(fields, in.BoundaryRules...)
	}
	return hashFields(fields...)
}

// planTaskHash identifies a task by its heading and its whole body. The
// heading carries the task's number, so a renumbered task is a changed task.
func planTaskHash(t planparser.RawTask) string {
	return hashFields(t.Title, t.Body)
}

// planRound is one validate_plan call placed against the plan run it names.
type planRound struct {
	// RunID is the live run this round revises, "" when the call named none or
	// named one this server does not hold.
	RunID string
	// UnknownRunID is a plan_run_id the caller passed that names no live run.
	UnknownRunID string
	// Revision is the run's revision before this round, 0 without a run.
	Revision int

	inputsKey  string
	planKey    string
	taskHashes []string
	// carried holds, per parsed task, the earlier round's result for a task
	// whose text is unchanged, and nil for a task this round must review.
	carried []*verdict.PlanTaskResult
	// changed lists the tasks to review, in plan order.
	changed []planparser.RawTask
	// planLevel says the plan-level pass must run: the plan text differs from
	// the earlier round's, or there is no earlier review to carry.
	planLevel bool
	// prior is the earlier review this round carries from, nil when there is
	// none usable.
	prior *planReview
}

// newPlanRound places a call against the run it names. A run with no usable
// review — none stored, or stored under other inputs — is revised with every
// task reviewed.
func (h *handlers) newPlanRound(planRunID, preamble string, tasks []planparser.RawTask, inputs planInputs) planRound {
	hashes := make([]string, len(tasks))
	for i, t := range tasks {
		hashes[i] = planTaskHash(t)
	}
	round := planRound{
		inputsKey:  inputs.key(),
		planKey:    hashFields(append([]string{preamble}, hashes...)...),
		taskHashes: hashes,
		carried:    make([]*verdict.PlanTaskResult, len(tasks)),
		changed:    tasks,
		planLevel:  true,
	}
	if planRunID == "" {
		return round
	}
	stored, revision, ok := h.deps.PlanRuns.Review(planRunID)
	if !ok {
		round.UnknownRunID = planRunID
		return round
	}
	round.RunID, round.Revision = planRunID, revision
	prior, _ := stored.(*planReview)
	if prior == nil || prior.InputsKey != round.inputsKey {
		return round
	}
	round.prior = prior
	round.planLevel = prior.PlanKey != round.planKey
	round.carry(tasks)
	return round
}

// carry matches each task to an earlier result by hash. A hash the earlier
// plan held twice is matched once per occurrence, in order.
func (r *planRound) carry(tasks []planparser.RawTask) {
	byHash := map[string][]*verdict.PlanTaskResult{}
	for i, hash := range r.prior.TaskHashes {
		if res := r.prior.Tasks[i]; res != nil {
			byHash[hash] = append(byHash[hash], res)
		}
	}
	r.changed = nil
	for i, hash := range r.taskHashes {
		if queue := byHash[hash]; len(queue) > 0 {
			r.carried[i], byHash[hash] = queue[0], queue[1:]
			continue
		}
		r.changed = append(r.changed, tasks[i])
	}
}

// tasksCarried is how many tasks this round takes from the earlier one.
func (r planRound) tasksCarried() int {
	return len(r.carried) - len(r.changed)
}

// priorPlanFindings returns a copy of the earlier round's plan-level
// findings, for the findings-only prompt.
func (r planRound) priorPlanFindings() []verdict.Finding {
	if r.prior == nil {
		return nil
	}
	return append([]verdict.Finding(nil), r.prior.PlanFindings...)
}

// scope reports what this round sent to the reviewer.
func (r planRound) scope() *verdict.PlanReviewScope {
	return &verdict.PlanReviewScope{
		Revision:          r.Revision + 1,
		TasksReviewed:     len(r.changed),
		TasksCarried:      r.tasksCarried(),
		PlanLevelReviewed: r.planLevel,
	}
}

// review builds the record to store for this round from raw, the round's
// merged reviewer output before any ruling or ladder ran. Each parsed task
// takes the result that names it (see parsedTaskIndexes); a task the reviewer
// returned no result for keeps none and is reviewed again next round.
func (r planRound) review(raw verdict.PlanResult, tasks []planparser.RawTask, modelUsed string) *planReview {
	raw = clonePlanResult(raw)
	out := &planReview{
		InputsKey:    r.inputsKey,
		PlanKey:      r.planKey,
		TaskHashes:   r.taskHashes,
		Tasks:        make([]*verdict.PlanTaskResult, len(tasks)),
		PlanVerdict:  raw.PlanVerdict,
		PlanQuality:  raw.PlanQuality,
		PlanFindings: raw.PlanFindings,
		NextAction:   raw.NextAction,
		ModelUsed:    modelUsed,
	}
	for i, idx := range parsedTaskIndexes(raw.Tasks, tasks) {
		if idx >= 0 && out.Tasks[idx] == nil {
			out.Tasks[idx] = &raw.Tasks[i]
		}
	}
	return out
}

// renderPlanRound renders the prompts a round on a known run needs: the
// findings-only prompt when the plan-level pass must run, showing it the
// earlier plan-level findings, and one chunk prompt per ChunkSize changed
// tasks. A round with nothing to review renders nothing.
func renderPlanRound(in renderPlanReviewInputs, round planRound) (renderedPlanReview, error) {
	if in.ChunkSize <= 0 {
		return renderedPlanReview{}, fmt.Errorf("renderPlanRound: chunkSize must be positive, got %d", in.ChunkSize)
	}
	var contextFilesNonce string
	if len(in.ContextFiles) > 0 {
		nonce, err := prompts.DeriveContextFilesNonce(in.ContextFiles)
		if err != nil {
			return renderedPlanReview{}, fmt.Errorf("derive context files nonce: %w", err)
		}
		contextFilesNonce = nonce
	}
	var rendered renderedPlanReview
	if round.planLevel {
		findingsOnly, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{
			PlanText:                     in.PlanText,
			ProjectKnowledge:             in.ProjectKnowledge,
			Mode:                         in.Mode,
			ContextFiles:                 in.ContextFiles,
			ContextFilesNonce:            contextFilesNonce,
			ControllerRulings:            in.ControllerRulings,
			ControllerVerifiedReferences: in.ControllerVerifiedReferences,
			PriorPlanFindings:            round.priorPlanFindings(),
			PlanKind:                     in.PlanKind,
			BoundaryRules:                in.BoundaryRules,
		})
		if err != nil {
			return renderedPlanReview{}, fmt.Errorf("render plan_findings_only: %w", err)
		}
		rendered.FindingsOnly = &findingsOnly
	}
	for i := 0; i < len(round.changed); i += in.ChunkSize {
		chunkTasks := round.changed[i:min(i+in.ChunkSize, len(round.changed))]
		chunkPrompt, err := prompts.RenderPlanTasksChunk(prompts.PlanChunkInput{
			PlanText:                     in.PlanText,
			ProjectKnowledge:             in.ProjectKnowledge,
			ChunkTasks:                   chunkTasks,
			Mode:                         in.Mode,
			ContextFiles:                 in.ContextFiles,
			ContextFilesNonce:            contextFilesNonce,
			ControllerRulings:            in.ControllerRulings,
			ControllerVerifiedReferences: in.ControllerVerifiedReferences,
			PlanKind:                     in.PlanKind,
			BoundaryRules:                in.BoundaryRules,
		})
		if err != nil {
			return renderedPlanReview{}, fmt.Errorf("render plan_tasks_chunk: %w", err)
		}
		rendered.Chunks = append(rendered.Chunks, renderedPlanChunk{Tasks: chunkTasks, Prompt: chunkPrompt})
	}
	return rendered, nil
}

// reviewPlanRound runs a round on a known run: the plan-level pass when
// rendered carries its prompt, then the changed tasks' chunks, and merges
// their results with the carried ones in plan order. A round that changed
// nothing makes no reviewer call and returns the earlier review as it was.
//
// The return shape is reviewPlanChunked's. On ErrResponseTruncated the result
// holds what the round has so far — the plan-level pass's output when that
// pass finished, the carried tasks and every chunk that completed — for
// truncatedRoundResult to finish. The truncating call's partial bytes are
// returned but not read: a task cut short is reviewed again on the retry.
func (h *handlers) reviewPlanRound(
	ctx context.Context,
	model config.ModelRef,
	rendered renderedPlanReview,
	round planRound,
	maxTokens int,
) (verdict.PlanResult, string, int64, []byte, error) {
	result := verdict.PlanResult{Tasks: make([]verdict.PlanTaskResult, 0)}
	modelUsed := model.String()
	if round.prior != nil {
		result.PlanVerdict = round.prior.PlanVerdict
		result.PlanQuality = round.prior.PlanQuality
		result.PlanFindings = append([]verdict.Finding(nil), round.prior.PlanFindings...)
		result.NextAction = round.prior.NextAction
		modelUsed = round.prior.ModelUsed
	}
	if rendered.reviewerCalls() == 0 {
		result.Tasks = round.mergeTasks(nil)
		return clonePlanResult(result), modelUsed, 0, nil, nil
	}
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PlanResult{}, "", 0, nil, err
	}

	var totalMs int64
	if rendered.FindingsOnly != nil {
		pf, used, ms, partialRaw, err := reviewPlanFindingsPass(ctx, rv, model, *rendered.FindingsOnly, maxTokens)
		totalMs += ms
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				// The earlier round's plan-level verdict and findings are left
				// out: the pass that would have confirmed or dropped them did
				// not finish.
				cut := verdict.PlanResult{Tasks: round.mergeTasks(nil)}
				return clonePlanResult(cut), modelUsed, totalMs, partialRaw, err
			}
			return verdict.PlanResult{}, "", 0, nil, err
		}
		result.PlanVerdict, result.PlanQuality = pf.PlanVerdict, pf.PlanQuality
		result.PlanFindings, result.NextAction = pf.PlanFindings, pf.NextAction
		modelUsed = used
	}

	reviewed, ms, partialRaw, err := h.reviewPlanChunks(ctx, rv, model, rendered.Chunks, maxTokens)
	totalMs += ms
	result.Tasks = round.mergeTasks(reviewed)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			return clonePlanResult(result), modelUsed, totalMs, partialRaw, err
		}
		return verdict.PlanResult{}, "", 0, nil, err
	}
	return clonePlanResult(result), modelUsed, totalMs, nil, nil
}

// mergeTasks lays the round's task results out in plan order: a carried
// result where the task is unchanged, else the next of reviewed, the changed
// tasks' results in the order they were sent. Each result takes its task's
// 1-based plan position as its task_index, since a carried result holds the
// position it had in an earlier revision and a chunk's results number from
// the chunk. A changed task with no result yet — the round was cut short — is
// left out.
func (r planRound) mergeTasks(reviewed []verdict.PlanTaskResult) []verdict.PlanTaskResult {
	out := make([]verdict.PlanTaskResult, 0, len(r.carried))
	next := 0
	for i, carried := range r.carried {
		var t verdict.PlanTaskResult
		switch {
		case carried != nil:
			t = *carried
		case next < len(reviewed):
			t = reviewed[next]
			next++
		default:
			continue
		}
		t.TaskIndex = i + 1
		out = append(out, t)
	}
	return out
}

// planPositions maps each task result of a round on a known run to its parsed
// task by the task_index mergeTasks gave it. A round cut short returns a list
// with gaps, where a result's place in the list is not its task's place in
// the plan, and two tasks that share a title cannot be told apart by title.
func (r planRound) planPositions(results []verdict.PlanTaskResult) []int {
	out := make([]int, len(results))
	for i, res := range results {
		out[i] = res.TaskIndex - 1
		if out[i] < 0 || out[i] >= len(r.carried) {
			out[i] = -1
		}
	}
	return out
}

// truncatedRoundResult turns what a round on a known run had when a reviewer
// call was cut short into the partial result it returns: the carried tasks
// and the tasks reviewed before the cut. The tasks the round did not reach
// have no result, and a round that names only carried tasks would otherwise
// read as a clean review of text the reviewer never finished, so the second
// return value is the major finding that holds the verdict down.
//
// The finding is returned apart from the result because the caller must add
// it after controller rulings are applied: its fingerprint is the same on
// every truncated round, and a ruling that reached it would waive it and let
// the round pass with a task unreviewed.
func truncatedRoundResult(partial verdict.PlanResult, round planRound) (verdict.PlanResult, verdict.Finding) {
	pr := clonePlanResult(partial)
	missing := len(round.carried) - len(pr.Tasks)
	planLevel := ""
	if round.planLevel && pr.PlanVerdict == "" {
		planLevel = " The plan-level pass did not finish either, so no plan-level findings are reported."
	}
	advice := truncatedPlanResult()
	pr.PlanVerdict = verdict.VerdictWarn
	if pr.PlanQuality == "" {
		pr.PlanQuality = verdict.PlanQualityRough
	}
	pr.NextAction = advice.NextAction
	pr.Partial = true
	return pr, verdict.Finding{
		Severity:  verdict.SeverityMajor,
		Category:  verdict.CategoryOther,
		Criterion: "reviewer_response",
		Evidence: fmt.Sprintf("%s. This round did not finish: %d of the plan's %d task(s) have no result.%s",
			providers.ErrResponseTruncated.Error(), missing, len(round.carried), planLevel),
		Suggestion: advice.PlanFindings[0].Suggestion,
	}
}

// carriedNextAction is the next_action for a round that made no plan-level
// pass, where pr is the round's finalized result and raw the stored reviewer
// output it was built from. The stored next_action was written for that
// output as the reviewer saw it, so it stays only while everything the server
// applies to that output — this round's rulings and verified references, and
// the file-consistency check — leaves the outcome as the reviewer's output
// alone gives it. Once they change the verdict, which findings remain or the
// checklist, the stored sentence may tell the controller to fix what the
// round just waived, or to dispatch a plan the server now holds back, and the
// server's own sentence replaces it. A next_action the ladder wrote for this
// round is kept.
func (c planCallContext) carriedNextAction(pr, raw verdict.PlanResult) string {
	prior := c.Round.prior
	if c.Round.RunID == "" || c.Round.planLevel || prior == nil || pr.NextAction != prior.NextAction {
		return pr.NextAction
	}
	unruled := c
	unruled.Rulings, unruled.VerifiedReferences, unruled.FileConsistency = nil, nil, nil
	base := clonePlanResult(raw)
	unruled.applyPreLadder(&base)
	finalizePlanVerdict(&base, c.Tasks)
	if planOutcome(base) == planOutcome(pr) {
		return pr.NextAction
	}
	if pr.PlanVerdict == verdict.VerdictPass {
		return planPassesNextAction
	}
	return "This round made no reviewer call: its rulings, verified references or file checks changed the outcome " +
		"of the earlier review. Fix each finding above or rule on it, then call validate_plan again with the same plan_run_id."
}

// planOutcome renders what a plan result tells the controller to act on: the
// verdict, every remaining finding by where it sits, its severity, category
// and criterion, and the checklist. Two results with the same outcome differ
// at most in wording, so a waived finding replaced by a new one of the same
// severity still changes it.
func planOutcome(pr verdict.PlanResult) string {
	var b strings.Builder
	add := func(scope string, findings []verdict.Finding) {
		for _, f := range findings {
			fmt.Fprintf(&b, "%s|%s|%s|%q\n", scope, f.Severity, f.Category, f.Criterion)
		}
	}
	fmt.Fprintf(&b, "%s\n", pr.PlanVerdict)
	add("plan", pr.PlanFindings)
	for i, t := range pr.Tasks {
		add(fmt.Sprintf("task %d", i), t.Findings)
	}
	for _, entry := range pr.CodebaseReferenceChecklist {
		fmt.Fprintf(&b, "checklist|%q\n", entry)
	}
	return b.String()
}

// unknownPlanRunIDEchoMax bounds, in runes, how much of an unknown plan_run_id
// the advisory repeats back.
const unknownPlanRunIDEchoMax = 64

// unknownPlanRunAdvisory tells a validate_plan caller that the plan_run_id it
// passed names no run this server holds, so nothing was carried from it and
// the response belongs to another run, minted for this call or found in the
// pass cache. It describes the call's arguments, not
// the plan, so it is added after the verdict is finalized.
func unknownPlanRunAdvisory(passed, current string) verdict.Finding {
	// passed is caller text of any length; an id this server mints is far
	// shorter than the cap.
	passed = truncate(passed, unknownPlanRunIDEchoMax)
	return verdict.Finding{
		Severity:  verdict.SeverityMinor,
		Category:  verdict.CategoryOther,
		Criterion: "plan_run_id",
		Evidence: fmt.Sprintf("plan_run_id %s names no live plan run on this server: it is unknown, or it expired. "+
			"Nothing was carried from it, and this response belongs to plan run %s.", passed, current),
		Suggestion: fmt.Sprintf("Pass plan_run_id=%s on the next validate_plan round and on every validate_task_spec call.", current),
	}
}
