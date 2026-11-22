package verdict

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFinalizeVerdict_Ladder(t *testing.T) {
	type tc struct {
		name             string
		findings         []Finding
		wantVerdict      Verdict
		wantNoiseCluster bool
		wantFindingCount int
	}
	mk := func(s Severity) Finding {
		return Finding{Severity: s, Category: CategoryQuality, Criterion: "x", Evidence: "y", Suggestion: "z"}
	}
	cases := []tc{
		{"empty → pass", nil, VerdictPass, false, 0},
		{"single minor → pass", []Finding{mk(SeverityMinor)}, VerdictPass, false, 1},
		{"two minor → pass", []Finding{mk(SeverityMinor), mk(SeverityMinor)}, VerdictPass, false, 2},
		{"three minor → warn + noise_cluster", []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}, VerdictWarn, true, 4},
		{"four minor → warn + noise_cluster", []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}, VerdictWarn, true, 5},
		{"single major → warn (no noise_cluster)", []Finding{mk(SeverityMajor)}, VerdictWarn, false, 1},
		{"major + three minor → warn (no noise_cluster — major present)", []Finding{mk(SeverityMajor), mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}, VerdictWarn, false, 4},
		{"single major test_value → warn", []Finding{{Severity: SeverityMajor, Category: CategoryQuality, Criterion: "test_value", Evidence: "y", Suggestion: "z"}}, VerdictWarn, false, 1},
		{"two major → fail", []Finding{mk(SeverityMajor), mk(SeverityMajor)}, VerdictFail, false, 2},
		{"single critical → fail", []Finding{mk(SeverityCritical)}, VerdictFail, false, 1},
		{"critical + three minor → fail (no noise_cluster)", []Finding{mk(SeverityCritical), mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}, VerdictFail, false, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Result{Verdict: VerdictPass, Findings: append([]Finding(nil), c.findings...), NextAction: "n"}
			out := FinalizeVerdict(r)
			require.Equal(t, c.wantVerdict, out.Verdict)
			require.Len(t, out.Findings, c.wantFindingCount)
			has := false
			for _, f := range out.Findings {
				if f.Category == CategoryOther && f.Criterion == "noise_cluster" {
					has = true
				}
			}
			require.Equal(t, c.wantNoiseCluster, has, "noise_cluster presence")
		})
	}
}

func TestFinalizeVerdict_Idempotent(t *testing.T) {
	r := Result{
		Verdict: VerdictPass,
		Findings: []Finding{
			{Severity: SeverityMinor, Category: CategoryQuality, Criterion: "a", Evidence: "b", Suggestion: "c"},
			{Severity: SeverityMinor, Category: CategoryQuality, Criterion: "a", Evidence: "b", Suggestion: "c"},
			{Severity: SeverityMinor, Category: CategoryQuality, Criterion: "a", Evidence: "b", Suggestion: "c"},
		},
		NextAction: "n",
	}
	once := FinalizeVerdict(r)
	twice := FinalizeVerdict(once)
	require.Equal(t, once.Verdict, twice.Verdict)
	require.Len(t, twice.Findings, len(once.Findings), "second call must not re-append noise_cluster")
}

func TestFinalizeVerdict_OverridesReviewerVerdict(t *testing.T) {
	r := Result{
		Verdict:    VerdictFail, // reviewer-emitted; should be overridden
		Findings:   []Finding{{Severity: SeverityMinor, Category: CategoryQuality, Criterion: "a", Evidence: "b", Suggestion: "c"}},
		NextAction: "n",
	}
	out := FinalizeVerdict(r)
	require.Equal(t, VerdictPass, out.Verdict)
}

func TestFinalizePlanVerdict_PerTaskAndPlanLadder(t *testing.T) {
	mk := func(s Severity) Finding {
		return Finding{Severity: s, Category: CategoryQuality, Criterion: "x", Evidence: "y", Suggestion: "z"}
	}
	pr := PlanResult{
		PlanVerdict:  VerdictPass, // reviewer-emitted; should be overridden
		PlanFindings: []Finding{mk(SeverityMajor)},
		Tasks: []PlanTaskResult{
			{TaskIndex: 0, TaskTitle: "t0", Verdict: VerdictPass, Findings: []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}},
			{TaskIndex: 1, TaskTitle: "t1", Verdict: VerdictFail, Findings: []Finding{mk(SeverityMinor)}},
		},
		NextAction:  "n",
		PlanQuality: PlanQualityRigorous, // reviewer-emitted; sanity rerun should keep it (warn → actionable default doesn't apply because reviewer's value is valid)
	}
	FinalizePlanVerdict(&pr)
	require.Equal(t, VerdictWarn, pr.PlanVerdict, "≥1 major → warn")
	require.Equal(t, VerdictWarn, pr.Tasks[0].Verdict, "task 0: three minor → warn")
	require.Equal(t, VerdictPass, pr.Tasks[1].Verdict, "task 1: single minor → pass (reviewer fail overridden)")
	// Task 0's noise_cluster advisory appended.
	taskHasNoise := false
	for _, f := range pr.Tasks[0].Findings {
		if f.Category == CategoryOther && f.Criterion == "noise_cluster" {
			taskHasNoise = true
		}
	}
	require.True(t, taskHasNoise, "task 0 should carry noise_cluster")
}

func TestFinalizePlanVerdict_RerunsApplyPlanQualitySanity(t *testing.T) {
	mk := func(s Severity) Finding {
		return Finding{Severity: s, Category: CategoryQuality, Criterion: "x", Evidence: "y", Suggestion: "z"}
	}
	// Reviewer emitted PlanQuality=rigorous but findings force fail.
	pr := PlanResult{
		PlanVerdict:  VerdictPass, // reviewer-emitted; ladder will derive fail
		PlanFindings: []Finding{mk(SeverityCritical)},
		Tasks:        []PlanTaskResult{},
		NextAction:   "n",
		PlanQuality:  PlanQualityRigorous,
	}
	FinalizePlanVerdict(&pr)
	require.Equal(t, VerdictFail, pr.PlanVerdict, "ladder derives fail")
	require.Equal(t, PlanQualityRough, pr.PlanQuality, "ApplyPlanQualitySanity forces rough on fail")
}

func TestFinalizePlanVerdict_Idempotent(t *testing.T) {
	mk := func(s Severity) Finding {
		return Finding{Severity: s, Category: CategoryQuality, Criterion: "x", Evidence: "y", Suggestion: "z"}
	}
	pr := PlanResult{
		PlanVerdict:  VerdictPass,
		PlanFindings: []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)},
		Tasks: []PlanTaskResult{
			{TaskIndex: 0, TaskTitle: "t0", Verdict: VerdictPass, Findings: []Finding{mk(SeverityMinor), mk(SeverityMinor), mk(SeverityMinor)}},
		},
		NextAction: "n",
	}
	FinalizePlanVerdict(&pr)
	beforeLen := len(pr.PlanFindings)
	beforeTaskLen := len(pr.Tasks[0].Findings)
	FinalizePlanVerdict(&pr)
	require.Equal(t, beforeLen, len(pr.PlanFindings), "plan noise_cluster not re-appended")
	require.Equal(t, beforeTaskLen, len(pr.Tasks[0].Findings), "task noise_cluster not re-appended")
}

func TestFinalizePlanVerdict_NilSafe(t *testing.T) {
	FinalizePlanVerdict(nil) // must not panic
}

func TestFinalizeVerdict_RepeatedMinorsDoNotCountTowardTheMinorRung(t *testing.T) {
	minor := func(criterion, repeatOf string) Finding {
		return Finding{Severity: SeverityMinor, Category: CategoryQuality, Criterion: criterion, RepeatOf: repeatOf}
	}
	carried := FinalizeVerdict(Result{Findings: []Finding{
		minor("a", "f_00000001"), minor("b", "f_00000002"), minor("c", "f_00000003"),
	}})
	require.Equal(t, VerdictPass, carried.Verdict, "three minors already reported must not lift the verdict")
	require.Len(t, carried.Findings, 3, "no noise_cluster advisory for carried minors")

	mixed := FinalizeVerdict(Result{Findings: []Finding{
		minor("a", "f_00000001"), minor("b", ""), minor("c", ""),
	}})
	require.Equal(t, VerdictPass, mixed.Verdict, "two new minors and one carried is below the rung")

	fresh := FinalizeVerdict(Result{Findings: []Finding{
		minor("a", "f_00000001"), minor("b", ""), minor("c", ""), minor("d", ""),
	}})
	require.Equal(t, VerdictWarn, fresh.Verdict, "three new minors still lift the verdict")
	require.Len(t, fresh.Findings, 5)
	require.Contains(t, fresh.Findings[4].Evidence, "3 minor findings")
}
