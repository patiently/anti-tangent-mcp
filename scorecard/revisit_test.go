package scorecard

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

var rvNow = time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)

var (
	rvBase   = rvNow.Add(-20 * 24 * time.Hour)
	rvRecent = rvNow.Add(-2 * 24 * time.Hour)
)

// rvTask is one post-verdicted task line in its own run unless hash is shared.
func rvTask(hash string, ts time.Time, idx int, impl, review, version string, mut func(*TaskSnapshot)) RunLine {
	s := &TaskSnapshot{Index: idx, PreVerdict: "pass", PostVerdict: "pass", ImplementerModel: impl,
		Calls: []ToolCall{completion(review, "pass")}}
	if mut != nil {
		mut(s)
	}
	return RunLine{Ts: ts, RunHash: hash, ServerVersion: version, Task: s}
}

func sessionHeader(hash string, ts time.Time) RunLine {
	return RunLine{Ts: ts, RunHash: hash, Header: true, Kind: KindSession, TaskCount: 1}
}

func intp(n int) *int { return &n }

// volumeSide makes n single-task runs at ts; the first hit of them add more tests than ACs.
func volumeSide(prefix string, ts time.Time, n, hit int) []RunLine {
	var out []RunLine
	for i := 0; i < n; i++ {
		added := 1
		if i < hit {
			added = 5
		}
		out = append(out, rvTask(fmt.Sprintf("%s%d", prefix, i), ts, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
			func(s *TaskSnapshot) { s.TestsAdded = intp(added); s.ACCount = 2 }))
	}
	return out
}

// markSide makes n single-task runs at ts, each adding two tests, the first
// hit of them carrying the given criterion and category counts.
func markSide(prefix string, ts time.Time, n, hit int, criteria, categories map[string]int) []RunLine {
	var out []RunLine
	for i := 0; i < n; i++ {
		on := i < hit
		out = append(out, rvTask(fmt.Sprintf("%s%d", prefix, i), ts, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
			func(s *TaskSnapshot) {
				s.TestsAdded = intp(2)
				s.ACCount = 2
				if on {
					s.Criteria, s.Categories = criteria, categories
				}
			}))
	}
	return out
}

func signalsOfKind(res RevisitResult, kind string) []Signal {
	var out []Signal
	for _, s := range res.Signals {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func gatedOfKind(res RevisitResult, kind string) []Gated {
	var out []Gated
	for _, g := range res.Gated {
		if g.Kind == kind {
			out = append(out, g)
		}
	}
	return out
}

func TestRevisitVolumeDriftUpAndDown(t *testing.T) {
	up := append(volumeSide("b", rvBase, 20, 2), volumeSide("r", rvRecent, 20, 15)...)
	res := Revisit(up, nil, nil, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 1 || res.Signals[0].Kind != RevisitTestVolume || res.Signals[0].Direction != "up" {
		t.Fatalf("up: %+v", res.Signals)
	}
	if want := "test_volume_drift:plan/claude-sonnet-5-5/" + NormalizeModel("openai:gpt-x"); res.Signals[0].ID != want {
		t.Fatalf("id = %q, want %q", res.Signals[0].ID, want)
	}
	down := append(volumeSide("b", rvBase, 20, 15), volumeSide("r", rvRecent, 20, 2)...)
	res = Revisit(down, nil, nil, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 1 || res.Signals[0].Direction != "down" {
		t.Fatalf("down: %+v", res.Signals)
	}
}

func TestRevisitGateRefusesSmallOrSingleRunSides(t *testing.T) {
	small := append(volumeSide("b", rvBase, 9, 1), volumeSide("r", rvRecent, 9, 8)...)
	res := Revisit(small, nil, nil, RevisitOptions{Now: rvNow, MinRuns: 10})
	if len(res.Signals) != 0 {
		t.Fatalf("small sides fired: %+v", res.Signals)
	}
	want := Gated{Kind: RevisitTestVolume, Cohort: CohortKey{Kind: KindPlan, ImplementerModel: "claude-sonnet-5-5", ReviewModel: NormalizeModel("openai:gpt-x")},
		RecentN: 9, BaselineN: 9, RecentRuns: 9, BaselineRuns: 9}
	if got := gatedOfKind(res, RevisitTestVolume); len(got) != 1 || got[0] != want {
		t.Fatalf("gated = %+v, want %+v", got, want)
	}

	var oneRun []RunLine
	for i := 1; i <= 20; i++ {
		oneRun = append(oneRun, rvTask("same", rvBase, i, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
			func(s *TaskSnapshot) { s.TestsAdded = intp(1); s.ACCount = 2 }))
	}
	oneRun = append(oneRun, volumeSide("r", rvRecent, 20, 18)...)
	res = Revisit(oneRun, nil, nil, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 0 {
		t.Fatalf("single-run baseline fired: %+v", res.Signals)
	}
	got := gatedOfKind(res, RevisitTestVolume)
	if len(got) != 1 || got[0].BaselineRuns != 1 || got[0].BaselineN != 20 || got[0].RecentRuns != 20 {
		t.Fatalf("gated = %+v", got)
	}
}

func TestRevisitNeverComparesKinds(t *testing.T) {
	lines := volumeSide("b", rvBase, 20, 2)
	for i, l := range volumeSide("r", rvRecent, 20, 15) {
		lines = append(lines, sessionHeader(fmt.Sprintf("r%d", i), l.Ts), l)
	}
	res := Revisit(lines, nil, nil, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 0 {
		t.Fatalf("cross-kind comparison fired: %+v", res.Signals)
	}
	got := gatedOfKind(res, RevisitTestVolume)
	if len(got) != 2 {
		t.Fatalf("gated = %+v", got)
	}
	for _, g := range got {
		if (g.RecentN == 0) == (g.BaselineN == 0) {
			t.Fatalf("expected exactly one empty side: %+v", g)
		}
	}
}

func TestRevisitIgnoresPre031Lines(t *testing.T) {
	var lines []RunLine
	for i := 0; i < 20; i++ {
		lines = append(lines, rvTask(fmt.Sprintf("b%d", i), rvBase, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.30.0",
			func(s *TaskSnapshot) { s.Categories = map[string]int{"test_adequacy": 1} }))
	}
	for i := 0; i < 20; i++ {
		on := i < 15
		lines = append(lines, rvTask(fmt.Sprintf("r%d", i), rvRecent, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
			func(s *TaskSnapshot) {
				s.TestsAdded = intp(3)
				if on {
					s.Criteria = map[string]int{"test_value": 1}
				}
			}))
	}
	res := Revisit(lines, nil, nil, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 0 {
		t.Fatalf("pre-0.31 baseline produced signals: %+v", res.Signals)
	}
	if g := gatedOfKind(res, RevisitTestValue); len(g) != 1 || g[0].BaselineN != 0 {
		t.Fatalf("gated = %+v", g)
	}
	if g := gatedOfKind(res, RevisitTestAdequacy); len(g) != 1 || g[0].BaselineN != 0 || g[0].RecentN != 20 {
		t.Fatalf("adequacy gated = %+v", g)
	}
}

func TestRevisitTestValueAndAdequacyFire(t *testing.T) {
	cases := []struct {
		kind               string
		criteria, category map[string]int
	}{
		{RevisitTestValue, map[string]int{"test_value": 1}, nil},
		{RevisitTestAdequacy, nil, map[string]int{"test_adequacy": 1}},
	}
	for _, c := range cases {
		for _, dir := range []string{"up", "down"} {
			baseHit, recentHit := 2, 15
			if dir == "down" {
				baseHit, recentHit = 15, 2
			}
			lines := append(markSide("b", rvBase, 20, baseHit, c.criteria, c.category),
				markSide("r", rvRecent, 20, recentHit, c.criteria, c.category)...)
			res := Revisit(lines, nil, nil, RevisitOptions{Now: rvNow})
			if got := signalsOfKind(res, c.kind); len(got) != 1 || got[0].Direction != dir {
				t.Fatalf("%s %s: %+v", c.kind, dir, res.Signals)
			}
		}
	}
}

func TestRevisitIgnoresFutureLines(t *testing.T) {
	future := rvNow.Add(time.Hour)
	lines := append(volumeSide("b", rvBase, 20, 2), volumeSide("f", future, 20, 18)...)
	res := Revisit(lines, nil, nil, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 0 {
		t.Fatalf("future lines fired: %+v", res.Signals)
	}
	if g := gatedOfKind(res, RevisitTestVolume); len(g) != 1 || g[0].RecentN != 0 {
		t.Fatalf("gated = %+v", g)
	}

	early := rvTask("x", rvRecent, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
		func(s *TaskSnapshot) { s.TestsAdded = intp(5); s.ACCount = 2 })
	late := rvTask("x", future, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
		func(s *TaskSnapshot) { s.TestsAdded = intp(0); s.ACCount = 2 })
	res = Revisit([]RunLine{early, late}, nil, nil, RevisitOptions{Now: rvNow})
	g := gatedOfKind(res, RevisitTestVolume)
	if len(g) != 1 || g[0].RecentN != 1 || g[0].RecentRuns != 1 {
		t.Fatalf("gated = %+v", g)
	}
}

func TestComputeLeavesRevisitNil(t *testing.T) {
	sc := Compute(volumeSide("b", rvBase, 3, 1), nil, Options{})
	if sc.Revisit != nil || sc.RevisitCoverage != nil {
		t.Fatalf("Compute set revisit fields: %+v %+v", sc.Revisit, sc.RevisitCoverage)
	}
}

func TestRevisitUnconfirmedFlagsOneSourcePerRun(t *testing.T) {
	build := func(baseConfirmed, recentConfirmed int) ([]RunLine, []OutcomeLine) {
		var lines []RunLine
		var outs []OutcomeLine
		side := func(prefix string, ts time.Time, confirmed int) {
			for i := 0; i < 20; i++ {
				h := fmt.Sprintf("%s%d", prefix, i)
				lines = append(lines, rvTask(h, ts, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
					func(s *TaskSnapshot) { s.PostVerdict = "warn" }))
				var fs []OutcomeFinding
				if i < confirmed {
					fs = []OutcomeFinding{{TaskIndex: 1, Severity: "major"}}
				}
				outs = append(outs, outcome(h, SourceFinalReview, ts, fs...), outcome(h, SourceReviewNow, ts))
			}
		}
		side("b", rvBase, baseConfirmed)
		side("r", rvRecent, recentConfirmed)
		return lines, outs
	}
	lines, outs := build(18, 2)
	res := Revisit(lines, outs, nil, RevisitOptions{Now: rvNow})
	got := signalsOfKind(res, RevisitUnconfirmed)
	if len(got) != 1 || got[0].Direction != "up" || got[0].Source != SourceFinalReview ||
		got[0].Recent.N != 20 || got[0].Baseline.N != 20 {
		t.Fatalf("up: %+v", res.Signals)
	}
	if want := "/" + SourceFinalReview; got[0].ID[len(got[0].ID)-len(want):] != want {
		t.Fatalf("id = %q", got[0].ID)
	}
	lines, outs = build(2, 18)
	res = Revisit(lines, outs, nil, RevisitOptions{Now: rvNow})
	if got := signalsOfKind(res, RevisitUnconfirmed); len(got) != 1 || got[0].Direction != "down" {
		t.Fatalf("down: %+v", res.Signals)
	}
}

func TestRevisitUnconfirmedCountsEveryFlaggedTaskInARun(t *testing.T) {
	var lines []RunLine
	var outs []OutcomeLine
	for i := 0; i < 2; i++ {
		h := fmt.Sprintf("r%d", i)
		for idx := 1; idx <= 5; idx++ {
			lines = append(lines, rvTask(h, rvRecent, idx, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
				func(s *TaskSnapshot) { s.PostVerdict = "fail" }))
		}
		outs = append(outs, outcome(h, SourceFinalReview, rvRecent))
	}
	res := Revisit(lines, outs, nil, RevisitOptions{Now: rvNow})
	g := gatedOfKind(res, RevisitUnconfirmed)
	if len(g) != 1 || g[0].RecentN != 10 || g[0].RecentRuns != 2 {
		t.Fatalf("gated = %+v", g)
	}
}

func TestRevisitNewModel(t *testing.T) {
	seenA := rvNow.Add(-3 * 24 * time.Hour)
	res := Revisit(nil, nil, map[string]time.Time{
		"a": seenA,
		"b": {},
		"c": rvNow.Add(-15 * 24 * time.Hour),
	}, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 1 {
		t.Fatalf("signals = %+v", res.Signals)
	}
	s := res.Signals[0]
	if s.ID != "new_model:a" || s.Kind != RevisitNewModel || s.Model != "a" || s.Since == nil || !s.Since.Equal(seenA) {
		t.Fatalf("signal = %+v", s)
	}
}

func TestRevisitStableOutput(t *testing.T) {
	empty := Revisit(nil, nil, nil, RevisitOptions{Now: rvNow})
	if empty.Signals == nil || empty.Gated == nil {
		t.Fatalf("nil slices: %+v", empty)
	}
	lines := append(volumeSide("b", rvBase, 20, 2), volumeSide("r", rvRecent, 20, 15)...)
	lines = append(lines, markSide("mb", rvBase, 20, 2, map[string]int{"test_value": 1}, nil)...)
	lines = append(lines, markSide("mr", rvRecent, 20, 15, map[string]int{"test_value": 1}, nil)...)
	seen := map[string]time.Time{"m1": rvNow.Add(-time.Hour), "m2": rvNow.Add(-2 * time.Hour)}
	a := Revisit(lines, nil, seen, RevisitOptions{Now: rvNow})
	b := Revisit(lines, nil, seen, RevisitOptions{Now: rvNow})
	if len(a.Signals) < 3 {
		t.Fatalf("want at least three signals: %+v", a.Signals)
	}
	if !slices.EqualFunc(a.Signals, b.Signals, func(x, y Signal) bool { return x.ID == y.ID }) {
		t.Fatalf("signal order differs: %+v vs %+v", a.Signals, b.Signals)
	}
	if !slices.EqualFunc(a.Gated, b.Gated, func(x, y Gated) bool { return x == y }) {
		t.Fatalf("gated differs: %+v vs %+v", a.Gated, b.Gated)
	}
}

func TestCohortModels(t *testing.T) {
	lines := []RunLine{
		rvTask("a", rvRecent, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0", nil),
		rvTask("b", rvRecent, 1, "", "openai:gpt-x", "0.31.0", nil),
		rvTask("c", rvNow.Add(time.Hour), 1, "claude-haiku-5-5", "openai:gpt-x", "0.31.0", nil),
	}
	got := CohortModels(lines, nil, rvNow)
	want := []string{"claude-sonnet-5-5", NormalizeModel("openai:gpt-x")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRevisitGatedSeparatesSourcesDeterministically(t *testing.T) {
	var lines []RunLine
	var outs []OutcomeLine
	for i := 0; i < 4; i++ {
		h := fmt.Sprintf("r%d", i)
		lines = append(lines, rvTask(h, rvRecent, 1, "claude-sonnet-5-5", "openai:gpt-x", "0.31.0",
			func(s *TaskSnapshot) { s.PostVerdict = "warn" }))
		src := SourceFinalReview
		if i%2 == 1 {
			src = SourceReviewNow
		}
		outs = append(outs, outcome(h, src, rvRecent))
	}
	var first []Gated
	for call := 0; call < 20; call++ {
		got := gatedOfKind(Revisit(lines, outs, nil, RevisitOptions{Now: rvNow}), RevisitUnconfirmed)
		if len(got) != 2 || got[0].Source != SourceFinalReview || got[1].Source != SourceReviewNow {
			t.Fatalf("gated = %+v", got)
		}
		if first == nil {
			first = got
		} else if !slices.Equal(first, got) {
			t.Fatalf("order changed: %+v vs %+v", first, got)
		}
	}
}
