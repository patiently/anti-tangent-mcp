package mcpsrv

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

const maxOutcomeFindings = 500

type OutcomeFindingArg struct {
	TaskIndex int    `json:"task_index" jsonschema:"1-based plan task the finding belongs to; 0 when it cannot be attributed to one task."`
	Severity  string `json:"severity" jsonschema:"critical, major or minor. Map your own scale; drop nits rather than sending them."`
	Category  string `json:"category" jsonschema:"One category word such as correctness, security, tests, docs. Never the finding text: only the first 40 characters, lower-cased, are stored."`
}

type OutcomeImplementerModelArg struct {
	TaskIndex int    `json:"task_index" jsonschema:"1-based plan task."`
	Model     string `json:"model" jsonschema:"provider:model the task was dispatched on, e.g. anthropic:claude-sonnet-5."`
}

type RecordReviewOutcomeArgs struct {
	PlanRunID         string                       `json:"plan_run_id" jsonschema:"The plan_run_id returned by validate_plan for the run the review covered."`
	Source            string                       `json:"source" jsonschema:"final_review for the controller's whole-plan review, review_now for a human-adjudicated PR review."`
	ReviewerModel     string                       `json:"reviewer_model,omitempty" jsonschema:"provider:model that performed the review, when known."`
	ImplementerModels []OutcomeImplementerModelArg `json:"implementer_models,omitempty" jsonschema:"The model each task was dispatched on. The controller knows this; the server cannot see it."`
	Findings          []OutcomeFindingArg          `json:"findings" jsonschema:"Every finding the review kept, attributed to a task. An empty array means the review found nothing, which is itself recorded."`
}

type RecordReviewOutcomeResult struct {
	Recorded     bool               `json:"recorded"`
	Reason       string             `json:"reason,omitempty"`
	RunKnown     bool               `json:"run_known"`
	TasksScored  int                `json:"tasks_scored"`
	Escapes      []scorecard.Escape `json:"escapes"`
	SummaryBlock string             `json:"summary_block"`
}

func recordReviewOutcomeTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "record_review_outcome",
		Description: "Record what an independent review of a finished plan run found, per task, so anti-tangent's own verdicts can be scored against it. " +
			"Call once after the final whole-plan review (source final_review), and again after a human-adjudicated PR review (source review_now); a later call for the same run and source replaces the earlier one. " +
			"Send categories and severities only, never finding text. Deterministic and free: no reviewer model is called. Returns the tasks anti-tangent passed that the review found a critical or major problem in.",
	}
}

func (h *handlers) RecordReviewOutcome(_ context.Context, _ *mcp.CallToolRequest, args RecordReviewOutcomeArgs) (*mcp.CallToolResult, RecordReviewOutcomeResult, error) {
	start := time.Now()
	res := h.recordReviewOutcome(args)
	res.SummaryBlock = formatOutcomeSummary(args, res)
	slog.Info("record_review_outcome",
		slog.String("tool", "record_review_outcome"),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		slog.String("source", args.Source),
		slog.Bool("recorded", res.Recorded),
		slog.Bool("run_known", res.RunKnown),
		slog.Int("escapes", len(res.Escapes)),
	)
	return nil, res, nil
}

func (h *handlers) recordReviewOutcome(args RecordReviewOutcomeArgs) RecordReviewOutcomeResult {
	res := RecordReviewOutcomeResult{Escapes: []scorecard.Escape{}}
	if h.deps.Stats == nil {
		res.Reason = "stats disabled: set ANTI_TANGENT_STATS_DIR to record review outcomes"
		return res
	}
	runID := strings.TrimSpace(args.PlanRunID)
	if reason := validateOutcomeArgs(runID, args); reason != "" {
		res.Reason = reason
		return res
	}
	runHash := h.deps.Stats.RunHash(runID)
	lines, err := h.deps.Stats.RunLines(runHash)
	if err != nil {
		slog.Warn("record_review_outcome: reading run snapshots failed", "err", err)
	}
	if reason := h.validateOutcomeTaskIndexes(runID, args.Findings, lines); reason != "" {
		res.Reason = reason
		return res
	}
	o := outcomeLineFromArgs(runHash, args)
	if err := h.deps.Stats.RecordOutcome(o); err != nil {
		res.Reason = "writing outcomes.jsonl failed: " + err.Error()
		return res
	}
	res.Recorded = true
	var snapshotted bool
	res.Escapes, res.TasksScored, snapshotted = scorecard.RunEscapes(lines, o)
	// A run minted before stats were enabled is live but has no snapshot
	// lines; it is still a run this server knows.
	_, live := h.deps.PlanRuns.PlanTaskCount(runID)
	res.RunKnown = snapshotted || live
	return res
}

// outcomeLineFromArgs converts the tool's wire args into the content-free
// record outcomes.jsonl stores: normalised categories, and the run hash in
// place of the raw plan_run_id.
func outcomeLineFromArgs(runHash string, args RecordReviewOutcomeArgs) scorecard.OutcomeLine {
	o := scorecard.OutcomeLine{
		Ts:            time.Now().UTC(),
		RunHash:       runHash,
		Source:        args.Source,
		ReviewerModel: strings.TrimSpace(args.ReviewerModel),
		Findings:      make([]scorecard.OutcomeFinding, 0, len(args.Findings)),
	}
	for _, f := range args.Findings {
		o.Findings = append(o.Findings, scorecard.OutcomeFinding{TaskIndex: f.TaskIndex, Severity: f.Severity, Category: scorecard.NormalizeCategory(f.Category)})
	}
	for _, m := range args.ImplementerModels {
		o.ImplementerModels = append(o.ImplementerModels, scorecard.ImplementerModel{TaskIndex: m.TaskIndex, Model: strings.TrimSpace(m.Model)})
	}
	return o
}

func validateOutcomeArgs(runID string, args RecordReviewOutcomeArgs) string {
	switch {
	case runID == "":
		return "plan_run_id is required"
	case !scorecard.ValidSource(args.Source):
		return `source must be "final_review" or "review_now"`
	case len(args.Findings) > maxOutcomeFindings:
		return fmt.Sprintf("findings has %d entries; at most %d are accepted", len(args.Findings), maxOutcomeFindings)
	}
	if reason := validateOutcomeFindings(args.Findings); reason != "" {
		return reason
	}
	return validateOutcomeImplementerModels(args.ImplementerModels)
}

func validateOutcomeFindings(findings []OutcomeFindingArg) string {
	for i, f := range findings {
		if !scorecard.ValidSeverity(f.Severity) {
			return fmt.Sprintf("findings[%d].severity %q must be critical, major or minor", i, f.Severity)
		}
		if f.TaskIndex < 0 {
			return fmt.Sprintf("findings[%d].task_index must be 0 or a 1-based task number", i)
		}
	}
	return ""
}

func validateOutcomeImplementerModels(models []OutcomeImplementerModelArg) string {
	for i, m := range models {
		if m.TaskIndex < 1 || strings.TrimSpace(m.Model) == "" {
			return fmt.Sprintf("implementer_models[%d] needs a 1-based task_index and a model", i)
		}
	}
	return ""
}

// validateOutcomeTaskIndexes range-checks every finding's task_index against
// the run's task count. It returns "" both when every index is in range and
// when the run itself is unknown, since an unknown run's indexes cannot be
// range-checked at all.
func (h *handlers) validateOutcomeTaskIndexes(runID string, findings []OutcomeFindingArg, lines []scorecard.RunLine) string {
	n, known := h.outcomeTaskCount(runID, lines)
	if !known {
		return ""
	}
	for i, f := range findings {
		if f.TaskIndex > n {
			return fmt.Sprintf("findings[%d].task_index %d exceeds the run's %d tasks", i, f.TaskIndex, n)
		}
	}
	return ""
}

// outcomeTaskCount is the run's task count from the live store, else from
// its snapshot header. known is false only when neither has the run; then
// task indexes cannot be range-checked. A known run with zero tasks is still
// known, so every positive task_index is rejected for it.
func (h *handlers) outcomeTaskCount(runID string, lines []scorecard.RunLine) (n int, known bool) {
	if n, ok := h.deps.PlanRuns.PlanTaskCount(runID); ok {
		return n, true
	}
	for _, l := range lines {
		if l.Header {
			return l.TaskCount, true
		}
	}
	return 0, false
}

// formatOutcomeSummary's Source, PlanRunID, Reason and Escape fields are all
// caller- or reviewer-attested plain strings the schema does not shape-check
// beyond non-emptiness, so each goes through escapeBlockValue: see that
// function's doc comment for why every such value in every formatter here
// does, and summary_forgery_test.go for what drives a forged payload through
// them.
func formatOutcomeSummary(args RecordReviewOutcomeArgs, res RecordReviewOutcomeResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "record_review_outcome · source: %s · run: %s\n", escapeBlockValue(args.Source), escapeBlockValue(strings.TrimSpace(args.PlanRunID)))
	if !res.Recorded {
		fmt.Fprintf(&b, "recorded: no — %s\n", escapeBlockValue(res.Reason))
		return b.String()
	}
	known := "yes"
	if !res.RunKnown {
		known = "no (no snapshot for this run yet; it is scored once one exists)"
	}
	fmt.Fprintf(&b, "recorded: yes · run known: %s · tasks scored: %d · escapes: %d\n", known, res.TasksScored, len(res.Escapes))
	for _, e := range res.Escapes {
		fmt.Fprintf(&b, "- task %d: anti-tangent %s, review found %s\n", e.TaskIndex, escapeBlockValue(e.AntiTangentVerdict), escapeBlockValue(e.OutcomeSeverity))
	}
	return b.String()
}
