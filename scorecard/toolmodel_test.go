package scorecard

import (
	"testing"
	"time"
)

func row(t *testing.T, rows []ToolModelRow, tool, model, publisher string) ToolModelRow {
	t.Helper()
	for _, r := range rows {
		if r.Tool == tool && r.Model == model && r.Publisher == publisher {
			return r
		}
	}
	t.Fatalf("no row %s/%s/%s in %+v", tool, model, publisher, rows)
	return ToolModelRow{}
}

func header(hash, publisher string, planModel, planVerdict string, taskCount int) RunLine {
	return RunLine{Ts: t0, RunHash: hash, Publisher: publisher, Header: true, PlanVerdict: planVerdict, TaskCount: taskCount,
		ConfiguredModels: map[string]string{"plan": planModel, "pre": "pre-m", "mid": "mid-m", "post": "post-m", "worker": ""},
		PlanCall:         &ToolCall{Tool: "validate_plan", Model: planModel, Verdict: planVerdict, Findings: 2, MS: 5000}}
}

func TestToolModelRowsSliceByEachToolsModel(t *testing.T) {
	spec := ToolCall{Tool: "validate_task_spec", Model: "pre-m", Verdict: "warn", Findings: 1, MS: 100}
	cp := ToolCall{Tool: "check_progress", Model: "mid-m", Verdict: "pass", MS: 50}
	done := ToolCall{Tool: "validate_completion", Model: "post-m", Verdict: "pass", MS: 200, Partial: true}
	lines := []RunLine{
		header("r1", "", "plan-m", "pass", 2),
		taskLine("r1", t0, 1, "pass", spec, cp, done),
		taskLine("r1", t0, 2, "pass", done),
		header("r2", "", "plan-m", "warn", 1),
		taskLine("r2", t0, 1, "pass", done),
	}
	outs := []OutcomeLine{outcome("r1", SourceFinalReview, t0.Add(time.Hour), OutcomeFinding{TaskIndex: 1, Severity: "major", Category: "x"})}
	sc := Compute(lines, outs, Options{})

	if sc.ByToolModel[0].Tool != "validate_plan" || sc.ByToolModel[len(sc.ByToolModel)-1].Tool != "validate_completion" {
		t.Fatalf("order: %+v", sc.ByToolModel)
	}
	plan := row(t, sc.ByToolModel, "validate_plan", "plan-m", "")
	if plan.Calls != 2 || plan.Runs != 2 || plan.Tasks != 3 || plan.VerdictCounts["warn"] != 1 {
		t.Fatalf("plan row: %+v", plan)
	}
	post := row(t, sc.ByToolModel, "validate_completion", "post-m", "")
	if post.Calls != 3 || post.PartialRate != 1 {
		t.Fatalf("post row operational: %+v", post)
	}
	if len(post.Outcomes) != 1 || post.Outcomes[0].EscapeRate.Num != 1 || post.Outcomes[0].EscapeRate.N != 2 {
		t.Fatalf("post row outcome (only r1 has an outcome): %+v", post.Outcomes)
	}
	pre := row(t, sc.ByToolModel, "validate_task_spec", "pre-m", "")
	if pre.Outcomes[0].UnconfirmedFlagRate.N != 1 || pre.Outcomes[0].UnconfirmedFlagRate.Num != 0 {
		t.Fatalf("pre flagged a task the review confirmed: %+v", pre.Outcomes)
	}
	mid := row(t, sc.ByToolModel, "check_progress", "mid-m", "")
	if mid.Outcomes[0].EscapeRate.N != 1 {
		t.Fatalf("mid row scores only tasks it checkpointed: %+v", mid.Outcomes)
	}
	if len(sc.ModelSets) != 1 || sc.ModelSets[0].Runs != 2 {
		t.Fatalf("model sets: %+v", sc.ModelSets)
	}
}

func TestPublisherViews(t *testing.T) {
	done := ToolCall{Tool: "validate_completion", Model: "post-m", Verdict: "pass"}
	a := taskLine("r1", t0, 1, "pass", done)
	a.Publisher = "alice"
	b := taskLine("r1", t0, 1, "pass", done)
	b.Publisher = "bob"
	oa := outcome("r1", SourceFinalReview, t0)
	oa.Publisher = "alice"
	ob := outcome("r1", SourceFinalReview, t0, OutcomeFinding{TaskIndex: 1, Severity: "major", Category: "x"})
	ob.Publisher = "bob"

	sc := Compute([]RunLine{a, b}, []OutcomeLine{oa, ob}, Options{})
	if len(sc.Publishers) != 2 || sc.ByPublisher == nil {
		t.Fatalf("publishers: %+v, views: %v", sc.Publishers, sc.ByPublisher)
	}
	if g := only(t, sc.ByReviewModel, SourceFinalReview); g.Runs != 2 || g.EscapeRate.Num != 1 {
		t.Fatalf("same run hash from two publishers must be two runs: %+v", g)
	}
	row(t, sc.ByPublisher.ByToolModel, "validate_completion", "post-m", "bob")

	mine := Compute([]RunLine{a, b}, []OutcomeLine{oa, ob}, Options{Publisher: "alice"})
	if mine.ByPublisher != nil || only(t, mine.ByReviewModel, SourceFinalReview).EscapeRate.Num != 0 {
		t.Fatalf("publisher filter: %+v", mine)
	}
}

func TestRunEscapes(t *testing.T) {
	lines := []RunLine{
		taskLine("r1", t0, 1, "pass"),
		taskLine("r1", t0, 2, "warn"),
		taskLine("r1", t0, 3, "pass"),
		taskLine("r1", t0, 4, ""),
	}
	o := outcome("r1", SourceFinalReview, t0,
		OutcomeFinding{TaskIndex: 1, Severity: "major"}, OutcomeFinding{TaskIndex: 1, Severity: "critical"},
		OutcomeFinding{TaskIndex: 2, Severity: "major"}, OutcomeFinding{TaskIndex: 3, Severity: "minor"})
	esc, scored, known := RunEscapes(lines, o)
	if !known || scored != 3 || len(esc) != 1 || esc[0].TaskIndex != 1 || esc[0].OutcomeSeverity != "critical" {
		t.Fatalf("escapes=%+v scored=%d known=%v", esc, scored, known)
	}
	if esc, _, known := RunEscapes(nil, o); known || esc == nil {
		t.Fatalf("unknown run: %+v %v", esc, known)
	}
}
