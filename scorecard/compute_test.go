package scorecard

import (
	"math"
	"slices"
	"sort"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func completion(model, verdict string) ToolCall {
	return ToolCall{Tool: "validate_completion", Model: model, Verdict: verdict, MS: 1000}
}

func taskLine(hash string, ts time.Time, idx int, post string, calls ...ToolCall) RunLine {
	return RunLine{Ts: ts, RunHash: hash, ServerVersion: "0.26.0",
		Task: &TaskSnapshot{Index: idx, PreVerdict: "pass", PostVerdict: post, Calls: calls}}
}

func outcome(hash, src string, ts time.Time, fs ...OutcomeFinding) OutcomeLine {
	return OutcomeLine{Ts: ts, RunHash: hash, Source: src, Findings: fs}
}

func only(t *testing.T, gs []Group, src string) Group {
	t.Helper()
	var out []Group
	for _, g := range gs {
		if g.Source == src {
			out = append(out, g)
		}
	}
	if len(out) != 1 {
		t.Fatalf("want 1 group for %s, got %d: %+v", src, len(out), out)
	}
	return out[0]
}

func TestWilson(t *testing.T) {
	r := wilson(3, 10)
	if math.Abs(r.Lo-0.1269) > 0.001 || math.Abs(r.Hi-0.5583) > 0.001 {
		t.Fatalf("wilson(3,10) = %+v", r)
	}
	if got := wilson(0, 0); got != (Rate{Lo: 0, Hi: 1}) {
		t.Fatalf("wilson(0,0) = %+v", got)
	}
}

func TestEscapeRateCountsMajorFindingsInPassedTasks(t *testing.T) {
	m := "openai:gpt-x"
	lines := []RunLine{
		taskLine("r1", t0, 1, "pass", completion(m, "pass")),
		taskLine("r1", t0, 2, "pass", completion(m, "pass")),
		taskLine("r1", t0, 3, "warn", completion(m, "warn")),
	}
	outs := []OutcomeLine{outcome("r1", SourceFinalReview, t0.Add(time.Hour),
		OutcomeFinding{TaskIndex: 1, Severity: "major", Category: "correctness"},
		OutcomeFinding{TaskIndex: 2, Severity: "minor", Category: "docs"})}
	g := only(t, Compute(lines, outs, Options{}).ByReviewModel, SourceFinalReview)
	if g.EscapeRate.Num != 1 || g.EscapeRate.N != 2 {
		t.Fatalf("escape = %+v", g.EscapeRate)
	}
	if g.MinorEscapeRate.Num != 1 || g.MinorEscapeRate.N != 2 {
		t.Fatalf("minor escape = %+v", g.MinorEscapeRate)
	}
	if g.UnconfirmedFlagRate.Num != 1 || g.UnconfirmedFlagRate.N != 1 {
		t.Fatalf("unconfirmed = %+v", g.UnconfirmedFlagRate)
	}
	if g.Key.ReviewModel != m || g.Runs != 1 || g.Tasks != 3 {
		t.Fatalf("group = %+v", g)
	}
}

func TestFixedWarnIsACatchNotNoise(t *testing.T) {
	lines := []RunLine{
		taskLine("r1", t0, 1, "warn", completion("m", "warn")),
		taskLine("r1", t0.Add(time.Minute), 1, "pass", completion("m", "warn"), completion("m", "pass")),
	}
	outs := []OutcomeLine{outcome("r1", SourceFinalReview, t0.Add(time.Hour))}
	g := only(t, Compute(lines, outs, Options{}).ByReviewModel, SourceFinalReview)
	if g.CaughtAndFixed != 1 || g.UnconfirmedFlagRate.N != 0 || g.EscapeRate.N != 1 {
		t.Fatalf("group = %+v", g)
	}
}

func TestUnattributedCountedOncePerRun(t *testing.T) {
	lines := []RunLine{taskLine("r1", t0, 1, "pass", completion("m", "pass")), taskLine("r1", t0, 2, "pass", completion("m", "pass"))}
	outs := []OutcomeLine{outcome("r1", SourceFinalReview, t0, OutcomeFinding{TaskIndex: 0, Severity: "minor", Category: "x"})}
	g := only(t, Compute(lines, outs, Options{}).ByReviewModel, SourceFinalReview)
	if g.UnattributedFindings["minor"] != 1 || g.EscapeRate.Num != 0 {
		t.Fatalf("group = %+v", g)
	}
}

func TestLatestOutcomePerSourceWins(t *testing.T) {
	lines := []RunLine{taskLine("r1", t0, 1, "pass", completion("m", "pass"))}
	outs := []OutcomeLine{
		outcome("r1", SourceReviewNow, t0.Add(time.Hour), OutcomeFinding{TaskIndex: 1, Severity: "major", Category: "x"}),
		outcome("r1", SourceReviewNow, t0.Add(2*time.Hour)),
	}
	g := only(t, Compute(lines, outs, Options{}).ByReviewModel, SourceReviewNow)
	if g.EscapeRate.Num != 0 {
		t.Fatalf("superseded outcome still counted: %+v", g.EscapeRate)
	}
}

func TestRunWithoutOutcomeIsNotScored(t *testing.T) {
	sc := Compute([]RunLine{taskLine("r1", t0, 1, "pass", completion("m", "pass"))}, nil, Options{})
	if len(sc.ByReviewModel) != 0 || len(sc.Cohorts) != 0 {
		t.Fatalf("unscored run produced groups: %+v", sc)
	}
}

func TestCohortKeyCarriesVersionAndImplementer(t *testing.T) {
	lines := []RunLine{taskLine("r1", t0, 1, "pass", completion("m", "pass"))}
	o := outcome("r1", SourceFinalReview, t0)
	o.ImplementerModels = []ImplementerModel{{TaskIndex: 1, Model: "anthropic:claude-sonnet-5"}}
	g := only(t, Compute(lines, []OutcomeLine{o}, Options{}).Cohorts, SourceFinalReview)
	want := CohortKey{Kind: KindPlan, ReviewModel: "m", ServerVersion: "0.26.0", ImplementerModel: "claude-sonnet-5"}
	if g.Key != want {
		t.Fatalf("key = %+v", g.Key)
	}
}

// cohortRuns builds n one-task runs reviewed by model, starting at start, with
// escapes of them escaping.
func cohortRuns(prefix, model string, start time.Time, n, escapes int) ([]RunLine, []OutcomeLine) {
	var lines []RunLine
	var outs []OutcomeLine
	for i := 0; i < n; i++ {
		h := prefix + string(rune('a'+i))
		ts := start.Add(time.Duration(i) * time.Minute)
		lines = append(lines, taskLine(h, ts, 1, "pass", completion(model, "pass")))
		o := outcome(h, SourceFinalReview, ts)
		if i < escapes {
			o.Findings = []OutcomeFinding{{TaskIndex: 1, Severity: "major", Category: "x"}}
		}
		outs = append(outs, o)
	}
	return lines, outs
}

func regressionFor(t *testing.T, oldN, oldEsc, newN, newEsc, minRuns int) Group {
	t.Helper()
	l1, o1 := cohortRuns("o", "old", t0, oldN, oldEsc)
	l2, o2 := cohortRuns("n", "new", t0.Add(24*time.Hour), newN, newEsc)
	sc := Compute(append(l1, l2...), append(o1, o2...), Options{MinRuns: minRuns})
	for _, g := range sc.ByReviewModel {
		if g.Key.ReviewModel == "new" {
			return g
		}
	}
	t.Fatal("no group for the new model")
	return Group{}
}

func TestRegressionFlag(t *testing.T) {
	if g := regressionFor(t, 9, 0, 20, 15, 10); g.Regression != RegressionInsufficientData {
		t.Fatalf("baseline below min runs: %q", g.Regression)
	}
	if g := regressionFor(t, 20, 0, 20, 15, 10); g.Regression != RegressionRegressed || g.Baseline == nil || g.Baseline.ReviewModel != "old" {
		t.Fatalf("disjoint intervals: %+v", g)
	}
	if g := regressionFor(t, 20, 2, 20, 3, 10); g.Regression != RegressionOK {
		t.Fatalf("overlapping intervals: %q", g.Regression)
	}
	l, o := cohortRuns("n", "only", t0, 12, 0)
	if g := only(t, Compute(l, o, Options{}).ByReviewModel, SourceFinalReview); g.Regression != RegressionNoBaseline {
		t.Fatalf("single cohort: %q", g.Regression)
	}
}

// The literal is sha256("s:run:pr_abc")[:12] in hex. The server's stats
// recorder and the daemon's title join both call HashRunID, so this one test
// pins the join key for both.
func TestHashRunIDPinned(t *testing.T) {
	if got := HashRunID("s", "pr_abc"); got != "r_6ffe28feb9d3bc37b1441388" {
		t.Fatalf("HashRunID = %q", got)
	}
}

func TestNormalizeCategory(t *testing.T) {
	if got := NormalizeCategory("  Correctness "); got != "correctness" {
		t.Fatalf("got %q", got)
	}
	long := "this is a whole finding description that should never be stored verbatim"
	if got := NormalizeCategory(long); len([]rune(got)) != 40 {
		t.Fatalf("got %d runes", len([]rune(got)))
	}
}

func TestFixedWarnThatStillEscapedIsNotCaughtAndFixed(t *testing.T) {
	lines := []RunLine{
		taskLine("r1", t0, 1, "warn", completion("m", "warn")),
		taskLine("r1", t0.Add(time.Minute), 1, "pass", completion("m", "warn"), completion("m", "pass")),
	}
	outs := []OutcomeLine{outcome("r1", SourceFinalReview, t0.Add(time.Hour), OutcomeFinding{TaskIndex: 1, Severity: "major", Category: "x"})}
	g := only(t, Compute(lines, outs, Options{}).ByReviewModel, SourceFinalReview)
	if g.CaughtAndFixed != 0 || g.EscapeRate.Num != 1 {
		t.Fatalf("a flagged task the review still found a major problem in is an escape, not a fix: %+v", g)
	}
}

func TestTasksWithVerdict(t *testing.T) {
	lines := []RunLine{
		taskLine("r1", t0, 3, "pass", completion("m", "pass")),
		taskLine("r1", t0, 1, "warn", completion("m", "warn")),
		taskLine("r1", t0, 2, ""),
	}
	got := TasksWithVerdict(lines)
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("TasksWithVerdict = %v, want [1 3]", got)
	}
	if got := TasksWithVerdict(nil); got == nil || len(got) != 0 {
		t.Fatalf("TasksWithVerdict(nil) = %#v, want an empty non-nil slice", got)
	}
}

func TestCohortsKeyOnRunKindAndNormalisedImplementerModel(t *testing.T) {
	header := func(hash, kind string, ts time.Time) RunLine {
		return RunLine{Ts: ts, RunHash: hash, ServerVersion: "0.26.0", Header: true, Kind: kind}
	}
	snap := func(hash string, ts time.Time, model string) RunLine {
		l := taskLine(hash, ts, 1, "pass", completion("m", "pass"))
		l.Task.ImplementerModel = model
		return l
	}
	planOutcome := func(ts time.Time, model string) OutcomeLine {
		o := outcome("plan", SourceFinalReview, ts.Add(time.Hour))
		o.ImplementerModels = []ImplementerModel{{TaskIndex: 1, Model: model}}
		return o
	}
	later := t0.Add(48 * time.Hour)

	tests := []struct {
		name        string
		lines       []RunLine
		outs        []OutcomeLine
		wantKinds   []string
		wantSession string
	}{
		{
			name: "provider prefix and bracket suffix share a model, kinds stay apart",
			lines: []RunLine{
				header("plan", "", t0), snap("plan", t0, ""),
				header("sess", KindSession, t0), snap("sess", t0, "claude-sonnet-5-5[1m]"),
			},
			outs:      []OutcomeLine{planOutcome(t0, "anthropic:claude-sonnet-5-5"), outcome("sess", SourceFinalReview, t0.Add(time.Hour))},
			wantKinds: []string{KindPlan, KindSession},
		},
		{
			name: "a later session cohort has no plan baseline",
			lines: []RunLine{
				header("plan", KindPlan, t0), snap("plan", t0, "claude-sonnet-5-5"),
				header("sess", KindSession, later), snap("sess", later, "claude-sonnet-5-5"),
			},
			outs:        []OutcomeLine{planOutcome(t0, "claude-sonnet-5-5"), outcome("sess", SourceFinalReview, later.Add(time.Hour))},
			wantKinds:   []string{KindPlan, KindSession},
			wantSession: RegressionNoBaseline,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var kinds []string
			for _, g := range Compute(tc.lines, tc.outs, Options{}).Cohorts {
				if g.Source != SourceFinalReview {
					continue
				}
				kinds = append(kinds, g.Key.Kind)
				if g.Key.ImplementerModel != "claude-sonnet-5-5" {
					t.Errorf("implementer model = %q", g.Key.ImplementerModel)
				}
				if g.Key.Kind == KindSession && g.Regression != tc.wantSession && tc.wantSession != "" {
					t.Errorf("session regression = %q, want %q", g.Regression, tc.wantSession)
				}
			}
			sort.Strings(kinds)
			if !slices.Equal(kinds, tc.wantKinds) {
				t.Errorf("kinds = %v, want %v", kinds, tc.wantKinds)
			}
		})
	}
}

// kindSplitFixture is one plan run and one later session run per publisher,
// all reviewed by the same model, each with a final review outcome.
func kindSplitFixture() ([]RunLine, []OutcomeLine) {
	later := t0.Add(48 * time.Hour)
	var lines []RunLine
	var outs []OutcomeLine
	for _, pub := range []string{"alice", "bob"} {
		for _, r := range []struct {
			hash, kind string
			ts         time.Time
		}{{pub + "-plan", KindPlan, t0}, {pub + "-sess", KindSession, later}} {
			task := taskLine(r.hash, r.ts, 1, "pass", completion("m", "pass"))
			task.Publisher = pub
			o := outcome(r.hash, SourceFinalReview, r.ts.Add(time.Hour))
			o.Publisher = pub
			lines = append(lines, RunLine{Ts: r.ts, RunHash: r.hash, ServerVersion: "0.26.0", Header: true, Kind: r.kind, Publisher: pub}, task)
			outs = append(outs, o)
		}
	}
	return lines, outs
}

// assertKindSplit checks that gs's final-review groups are want groups across
// both kinds and that no session group took a baseline.
func assertKindSplit(t *testing.T, name string, gs []Group, want int) {
	t.Helper()
	var kinds []string
	for _, g := range gs {
		if g.Source != SourceFinalReview {
			continue
		}
		kinds = append(kinds, g.Key.Kind)
		if g.Key.Kind == KindSession && g.Regression != RegressionNoBaseline {
			t.Errorf("%s: session group regression = %q against %+v, want %q", name, g.Regression, g.Baseline, RegressionNoBaseline)
		}
	}
	sort.Strings(kinds)
	wantKinds := slices.Repeat([]string{KindPlan}, want/2)
	wantKinds = append(wantKinds, slices.Repeat([]string{KindSession}, want/2)...)
	if !slices.Equal(kinds, wantKinds) {
		t.Errorf("%s: kinds = %v, want %v", name, kinds, wantKinds)
	}
}

// TestViewsKeyOnRunKind covers the review-model, tool-model and publisher
// views: with one plan run and one later session run reviewed by the same
// model, each view keeps the kinds apart, and the session group finds no plan
// group to take as its baseline.
func TestViewsKeyOnRunKind(t *testing.T) {
	lines, outs := kindSplitFixture()
	sc := Compute(lines, outs, Options{})
	if sc.ByPublisher == nil {
		t.Fatal("no per-publisher views for two publishers")
	}
	assertKindSplit(t, "by_review_model", sc.ByReviewModel, 2)
	assertKindSplit(t, "by_publisher.by_review_model", sc.ByPublisher.ByReviewModel, 4)

	var kinds []string
	for _, row := range sc.ByToolModel {
		if row.Tool == "validate_completion" {
			kinds = append(kinds, row.Kind)
		}
	}
	if !slices.Equal(kinds, []string{KindPlan, KindSession}) {
		t.Errorf("by_tool_model validate_completion kinds = %v, want [plan session]", kinds)
	}
}
