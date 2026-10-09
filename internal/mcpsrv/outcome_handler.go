package mcpsrv

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

const (
	maxOutcomeFindings     = 500
	maxOutcomeMeasurements = 200
)

type OutcomeFindingArg struct {
	TaskIndex int    `json:"task_index" jsonschema:"1-based plan task the finding belongs to; 0 when it cannot be attributed to one task."`
	Severity  string `json:"severity" jsonschema:"critical, major or minor. Map your own scale; drop nits rather than sending them."`
	Category  string `json:"category" jsonschema:"One category word such as correctness, security, tests, docs. Never the finding text: only the first 40 characters, lower-cased, are stored."`
}

type OutcomeImplementerModelArg struct {
	TaskIndex int    `json:"task_index" jsonschema:"1-based plan task."`
	Model     string `json:"model" jsonschema:"provider:model the task was dispatched on, e.g. anthropic:claude-sonnet-5. At most 100 characters, no control character."`
}

type OutcomeMeasurementArg struct {
	TaskIndex int     `json:"task_index" jsonschema:"1-based plan task the measurement belongs to; 0 when it covers the whole run."`
	Metric    string  `json:"metric" jsonschema:"A short metric name, such as zip_missing_rate. Only the first 40 characters, lower-cased, are stored."`
	Before    float64 `json:"before" jsonschema:"The value before the change."`
	After     float64 `json:"after" jsonschema:"The value after the change."`
	N         int     `json:"n,omitempty" jsonschema:"The sample size, when the value is a rate."`
}

type RecordReviewOutcomeArgs struct {
	PlanRunID         string                       `json:"plan_run_id" jsonschema:"The plan_run_id returned by validate_plan for the run the review covered."`
	Source            string                       `json:"source" jsonschema:"final_review for the controller's whole-plan review, review_now for a human-adjudicated PR review."`
	ReviewerModel     string                       `json:"reviewer_model,omitempty" jsonschema:"provider:model that performed the review, when known. At most 100 characters, no control character."`
	ImplementerModels []OutcomeImplementerModelArg `json:"implementer_models,omitempty" jsonschema:"The model each task was dispatched on, one entry per dispatched task. The controller knows this; the server cannot see it. For final_review, tasks left out are listed in missing_implementer_models and scored in an unknown cohort."`
	Findings          []OutcomeFindingArg          `json:"findings" jsonschema:"Every finding the review kept, attributed to a task. An empty array means the review found nothing, which is itself recorded."`
	Measurements      []OutcomeMeasurementArg      `json:"measurements,omitempty" jsonschema:"Numbers measured for a task, such as an eval's rate before and after an experiment. Stored with the outcome so runs can be compared over time; the scorecard does not score them. At most 200 entries."`
}

type RecordReviewOutcomeResult struct {
	Recorded    bool               `json:"recorded"`
	Reason      string             `json:"reason,omitempty"`
	RunKnown    bool               `json:"run_known"`
	TasksScored int                `json:"tasks_scored"`
	Escapes     []scorecard.Escape `json:"escapes"`
	// MissingImplementerModels lists, for a final_review call, the tasks that
	// have a final verdict and that the call named no implementer model for.
	// It is always empty for review_now.
	MissingImplementerModels []int  `json:"missing_implementer_models"`
	SummaryBlock             string `json:"summary_block"`
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
	res := RecordReviewOutcomeResult{Escapes: []scorecard.Escape{}, MissingImplementerModels: []int{}}
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
	if reason := h.validateOutcomeMeasurementIndexes(runID, args.Measurements, lines); reason != "" {
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
	if args.Source == scorecard.SourceFinalReview {
		res.MissingImplementerModels = missingImplementerModels(scorecard.TasksWithVerdict(lines), args.ImplementerModels)
	}
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
	for _, m := range args.Measurements {
		o.Measurements = append(o.Measurements, scorecard.Measurement{
			TaskIndex: m.TaskIndex, Metric: scorecard.NormalizeCategory(m.Metric), Before: m.Before, After: m.After, N: m.N,
		})
	}
	return o
}

// missingImplementerModels returns the entries of tasks, in order, that models
// names no model for.
func missingImplementerModels(tasks []int, models []OutcomeImplementerModelArg) []int {
	named := make(map[int]bool, len(models))
	for _, m := range models {
		named[m.TaskIndex] = true
	}
	out := []int{}
	for _, idx := range tasks {
		if !named[idx] {
			out = append(out, idx)
		}
	}
	return out
}

func validateOutcomeArgs(runID string, args RecordReviewOutcomeArgs) string {
	switch {
	case runID == "":
		return "plan_run_id is required"
	case !scorecard.ValidSource(args.Source):
		return `source must be "final_review" or "review_now"`
	case len(args.Findings) > maxOutcomeFindings:
		return fmt.Sprintf("findings has %d entries; at most %d are accepted", len(args.Findings), maxOutcomeFindings)
	case len(args.Measurements) > maxOutcomeMeasurements:
		return fmt.Sprintf("measurements has %d entries; at most %d are accepted", len(args.Measurements), maxOutcomeMeasurements)
	case !scorecard.ValidModelString(strings.TrimSpace(args.ReviewerModel)):
		return fmt.Sprintf("reviewer_model must be at most %d characters and contain no control character", scorecard.MaxModelRunes)
	}
	if reason := validateOutcomeFindings(args.Findings); reason != "" {
		return reason
	}
	if reason := validateOutcomeMeasurements(args.Measurements); reason != "" {
		return reason
	}
	return validateOutcomeImplementerModels(args.ImplementerModels)
}

func validateOutcomeMeasurements(ms []OutcomeMeasurementArg) string {
	for i, m := range ms {
		if m.TaskIndex < 0 {
			return fmt.Sprintf("measurements[%d].task_index must be 0 or a 1-based task number", i)
		}
		if scorecard.NormalizeCategory(m.Metric) == "" {
			return fmt.Sprintf("measurements[%d].metric is required", i)
		}
		if strings.IndexFunc(m.Metric, unicode.IsControl) >= 0 {
			return fmt.Sprintf("measurements[%d].metric must not contain control characters", i)
		}
		if m.N < 0 {
			return fmt.Sprintf("measurements[%d].n must not be negative", i)
		}
	}
	return ""
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
		model := strings.TrimSpace(m.Model)
		if m.TaskIndex < 1 || model == "" {
			return fmt.Sprintf("implementer_models[%d] needs a 1-based task_index and a model", i)
		}
		if !scorecard.ValidModelString(model) {
			return fmt.Sprintf("implementer_models[%d].model must be at most %d characters and contain no control character", i, scorecard.MaxModelRunes)
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

// validateOutcomeMeasurementIndexes range-checks measurements as
// validateOutcomeTaskIndexes checks findings.
func (h *handlers) validateOutcomeMeasurementIndexes(runID string, ms []OutcomeMeasurementArg, lines []scorecard.RunLine) string {
	n, known := h.outcomeTaskCount(runID, lines)
	if !known {
		return ""
	}
	for i, m := range ms {
		if m.TaskIndex > n {
			return fmt.Sprintf("measurements[%d].task_index %d exceeds the run's %d tasks", i, m.TaskIndex, n)
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
	if len(args.Measurements) > 0 {
		fmt.Fprintf(&b, "measurements: %d stored, not scored\n", len(args.Measurements))
	}
	for _, e := range res.Escapes {
		fmt.Fprintf(&b, "- task %d: anti-tangent %s, review found %s\n", e.TaskIndex, escapeBlockValue(e.AntiTangentVerdict), escapeBlockValue(e.OutcomeSeverity))
	}
	if len(res.MissingImplementerModels) > 0 {
		idx := make([]string, len(res.MissingImplementerModels))
		for i, n := range res.MissingImplementerModels {
			idx[i] = strconv.Itoa(n)
		}
		fmt.Fprintf(&b, "implementer model missing for tasks: %s — call again with implementer_models for every task\n", strings.Join(idx, ", "))
	}
	return b.String()
}
