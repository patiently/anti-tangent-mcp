package verdict

import "fmt"

// FinalizeVerdict derives r.Verdict from r.Findings via the severity ladder
//
//	critical >= 1 OR major >= 2  → fail
//	major >= 1 OR minor >= 3     → warn
//	otherwise                    → pass
//
// and appends a `noise_cluster` advisory finding when the `minor >= 3 → warn`
// branch fires AND no critical/major exists. The reviewer's r.Verdict is
// overwritten by the server-derived value. Idempotent: a second call
// observes the noise_cluster advisory it appended and does not append again.
func FinalizeVerdict(r Result) Result {
	var critical, major, minor int
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityCritical:
			critical++
		case SeverityMajor:
			major++
		case SeverityMinor:
			minor++
		}
	}
	switch {
	case critical >= 1 || major >= 2:
		r.Verdict = VerdictFail
	case major >= 1 || minor >= 3:
		r.Verdict = VerdictWarn
	default:
		r.Verdict = VerdictPass
	}
	if critical == 0 && major == 0 && minor >= 3 {
		for _, f := range r.Findings {
			if f.Category == CategoryOther && f.Criterion == "noise_cluster" {
				return r
			}
		}
		r.Findings = append(r.Findings, Finding{
			Severity:   SeverityMinor,
			Category:   CategoryOther,
			Criterion:  "noise_cluster",
			Evidence:   fmt.Sprintf("%d minor findings on this call (no critical or major). Each finding is individually advisory; the cluster lifts verdict to warn.", minor),
			Suggestion: "Inspect the minor findings as a group. If they're all low-signal noise, the next caller iteration can ignore them collectively. If any one warrants escalation, address it individually.",
		})
	}
	return r
}
