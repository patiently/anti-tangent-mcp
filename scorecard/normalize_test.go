package scorecard

import (
	"testing"
	"time"
)

func TestNormalizeModel(t *testing.T) {
	cases := map[string]string{
		"anthropic:claude-haiku-4-5-20251001": "anthropic:claude-haiku-4-5",
		"  Anthropic:Claude-Haiku-4-5 ":       "anthropic:claude-haiku-4-5",
		"anthropic:claude-haiku-4-5":          "anthropic:claude-haiku-4-5",
		"anthropic:claude-sonnet-5-5":         "anthropic:claude-sonnet-5-5",
		"openai:gpt-5.6-terra":                "openai:gpt-5.6-terra",
		"openai:gpt-5-2025-08-07":             "openai:gpt-5",
		"":                                    "",
	}
	for in, want := range cases {
		if got := NormalizeModel(in); got != want {
			t.Errorf("NormalizeModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCohortsMergeDatedAndUndatedImplementerModel(t *testing.T) {
	lines := []RunLine{
		taskLine("r1", t0, 1, "pass", completion("m", "pass")),
		taskLine("r2", t0.Add(time.Minute), 1, "pass", completion("m", "pass")),
	}
	o1 := outcome("r1", SourceFinalReview, t0.Add(time.Hour))
	o1.ImplementerModels = []ImplementerModel{{TaskIndex: 1, Model: "anthropic:claude-haiku-4-5"}}
	o2 := outcome("r2", SourceFinalReview, t0.Add(time.Hour))
	o2.ImplementerModels = []ImplementerModel{{TaskIndex: 1, Model: "anthropic:claude-haiku-4-5-20251001"}}
	g := only(t, Compute(lines, []OutcomeLine{o1, o2}, Options{}).Cohorts, SourceFinalReview)
	if g.Runs != 2 || g.Key.ImplementerModel != "claude-haiku-4-5" {
		t.Fatalf("cohort = %+v runs=%d", g.Key, g.Runs)
	}
}

func TestReviewModelIsNormalisedInEveryView(t *testing.T) {
	lines := []RunLine{
		taskLine("r1", t0, 1, "pass", completion("openai:gpt-x", "pass")),
		taskLine("r1", t0, 2, "pass", completion("openai:gpt-x-20260101", "pass")),
	}
	sc := Compute(lines, []OutcomeLine{outcome("r1", SourceFinalReview, t0.Add(time.Hour))}, Options{})
	g := only(t, sc.ByReviewModel, SourceFinalReview)
	if g.Key.ReviewModel != "openai:gpt-x" || g.Tasks != 2 {
		t.Fatalf("by review model = %+v tasks=%d", g.Key, g.Tasks)
	}
	rows := 0
	for _, r := range sc.ByToolModel {
		if r.Tool == "validate_completion" {
			rows++
			if r.Model != "openai:gpt-x" || r.Calls != 2 {
				t.Fatalf("tool-model row = %+v", r)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("want 1 validate_completion row, got %d", rows)
	}
}

// versionedCohort builds n one-task runs for one implementer model on one
// server version, a minute apart from start.
func versionedCohort(prefix, version, impl string, start time.Time, n int) ([]RunLine, []OutcomeLine) {
	var lines []RunLine
	var outs []OutcomeLine
	for i := 0; i < n; i++ {
		h := prefix + string(rune('a'+i))
		ts := start.Add(time.Duration(i) * time.Minute)
		l := taskLine(h, ts, 1, "pass", completion("m", "pass"))
		l.ServerVersion = version
		lines = append(lines, l)
		o := outcome(h, SourceFinalReview, ts)
		o.ImplementerModels = []ImplementerModel{{TaskIndex: 1, Model: impl}}
		outs = append(outs, o)
	}
	return lines, outs
}

func TestBaselinePrefersSameModels(t *testing.T) {
	la, oa := versionedCohort("a", "0.26.0", "impl-a", t0, 3)
	lb, ob := versionedCohort("b", "0.26.0", "impl-b", t0.Add(24*time.Hour), 3)
	lc, oc := versionedCohort("c", "0.27.0", "impl-a", t0.Add(48*time.Hour), 3)
	lines := append(append(la, lb...), lc...)
	outs := append(append(oa, ob...), oc...)
	for _, g := range Compute(lines, outs, Options{MinRuns: 1}).Cohorts {
		if g.Key.ServerVersion != "0.27.0" {
			continue
		}
		if g.Baseline == nil || g.Baseline.ImplementerModel != "impl-a" || g.Baseline.ServerVersion != "0.26.0" {
			t.Fatalf("baseline = %+v, want the impl-a 0.26.0 cohort", g.Baseline)
		}
		return
	}
	t.Fatal("no 0.27.0 cohort")
}

func TestCheckpointSpellingsOfOneModelScoreTheTaskOnce(t *testing.T) {
	a := ToolCall{Tool: "check_progress", Model: "openai:gpt-x", Verdict: "pass", MS: 1}
	b := ToolCall{Tool: "check_progress", Model: "openai:gpt-x-20260101", Verdict: "warn", MS: 1}
	c := ToolCall{Tool: "check_progress", Model: "openai:gpt-x-20260102", Verdict: "warn", MS: 1}
	lines := []RunLine{taskLine("r1", t0, 1, "pass", a, b, c)}
	outs := []OutcomeLine{outcome("r1", SourceFinalReview, t0.Add(time.Hour))}
	sc := Compute(lines, outs, Options{})
	var rows []ToolModelRow
	for _, r := range sc.ByToolModel {
		if r.Tool == "check_progress" {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 || rows[0].Model != "openai:gpt-x" || rows[0].Calls != 3 {
		t.Fatalf("check_progress rows = %+v", rows)
	}
	var found bool
	for _, o := range rows[0].Outcomes {
		if o.Source == SourceFinalReview {
			found = true
			if o.UnconfirmedFlagRate.N != 1 {
				t.Fatalf("unconfirmed = %+v", o.UnconfirmedFlagRate)
			}
		}
	}
	if !found {
		t.Fatalf("no final_review outcome: %+v", rows[0].Outcomes)
	}
}
