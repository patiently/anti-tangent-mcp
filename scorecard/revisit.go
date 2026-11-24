package scorecard

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RevisitWindow is the length of the recent window; the baseline is every
// retained task before it.
const RevisitWindow = 14 * 24 * time.Hour

const (
	RevisitTestValue    = "test_value_drift"
	RevisitTestVolume   = "test_volume_drift"
	RevisitTestAdequacy = "test_adequacy_share"
	RevisitUnconfirmed  = "unconfirmed_flags_drift"
	RevisitNewModel     = "new_model"
)

type Signal struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Cohort    *CohortKey `json:"cohort,omitempty"`
	Direction string     `json:"direction,omitempty"`
	Recent    *Rate      `json:"recent,omitempty"`
	Baseline  *Rate      `json:"baseline,omitempty"`
	Source    string     `json:"source,omitempty"`
	Model     string     `json:"model,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
}

// Gated is a comparison the sample gate refused. It is published so a quiet
// tray can say "too small to judge" rather than read as an all-clear.
type Gated struct {
	Kind         string    `json:"kind"`
	Cohort       CohortKey `json:"cohort"`
	Source       string    `json:"source,omitempty"`
	RecentN      int       `json:"recent_n"`
	BaselineN    int       `json:"baseline_n"`
	RecentRuns   int       `json:"recent_runs"`
	BaselineRuns int       `json:"baseline_runs"`
}

type RevisitOptions struct {
	MinRuns int
	Now     time.Time
}

type RevisitResult struct {
	Signals []Signal
	Gated   []Gated
}

// driftTest reports whether a task is eligible for a kind and, if so, whether
// it counts toward the numerator. src is the run's scoring source ("" if none).
type driftTest func(r *run, t *task, src string) (eligible, hit bool)

var driftKinds = []struct {
	kind string
	test driftTest
}{
	{RevisitTestValue, func(_ *run, t *task, _ string) (bool, bool) {
		// Only a task that added tests can draw the test-value lens, which
		// also keeps lines from servers that predate tests_added out.
		if t.snap.TestsAdded == nil || *t.snap.TestsAdded <= 0 {
			return false, false
		}
		return true, t.snap.Criteria["test_value"] > 0
	}},
	{RevisitTestVolume, func(_ *run, t *task, _ string) (bool, bool) {
		if t.snap.TestsAdded == nil || t.snap.ACCount <= 0 {
			return false, false
		}
		return true, *t.snap.TestsAdded > t.snap.ACCount
	}},
	{RevisitTestAdequacy, func(_ *run, t *task, _ string) (bool, bool) {
		// Lines from servers that did not yet count this category would read as a drop.
		if !versionAtLeast(t.version, 0, 31, 0) {
			return false, false
		}
		return true, t.snap.Categories["test_adequacy"] > 0
	}},
	{RevisitUnconfirmed, func(r *run, t *task, src string) (bool, bool) {
		if src == "" || !isFlag(t.snap.PostVerdict) {
			return false, false
		}
		high, _ := outcomeCounts(r.outcomes[src], t.snap.Index)
		return true, high == 0
	}},
}

type revisitSide struct {
	num, n int
	runs   map[runKey]bool
}

type revisitID struct {
	kind, source string
	key          CohortKey
}

// Revisit compares each cohort's recent window with its own baseline and
// reports drifts, models first seen within the window, and the comparisons
// the sample gate refused. firstSeen maps a normalised model to when it was
// first seen; a zero time means it predates tracking and never fires.
func Revisit(lines []RunLine, outcomes []OutcomeLine, firstSeen map[string]time.Time, opts RevisitOptions) RevisitResult {
	if opts.MinRuns <= 0 {
		opts.MinRuns = DefaultMinRuns
	}
	cut := opts.Now.Add(-RevisitWindow)
	res := RevisitResult{Signals: []Signal{}, Gated: []Gated{}}
	for id, s := range tallyRevisit(notAfter(lines, opts.Now), outcomes, cut) {
		base, rec := s[0], s[1]
		if !base.enough(opts.MinRuns) || !rec.enough(opts.MinRuns) {
			res.Gated = append(res.Gated, Gated{Kind: id.kind, Cohort: id.key, Source: id.source, RecentN: rec.n, BaselineN: base.n,
				RecentRuns: len(rec.runs), BaselineRuns: len(base.runs)})
			continue
		}
		if sig, ok := driftSignal(id, base, rec); ok {
			res.Signals = append(res.Signals, sig)
		}
	}
	for m, seen := range firstSeen {
		if seen.IsZero() || seen.Before(cut) || seen.After(opts.Now) {
			continue
		}
		since := seen
		res.Signals = append(res.Signals, Signal{ID: RevisitNewModel + ":" + m, Kind: RevisitNewModel, Model: m, Since: &since})
	}
	sort.Slice(res.Signals, func(i, j int) bool { return res.Signals[i].ID < res.Signals[j].ID })
	sort.Slice(res.Gated, func(i, j int) bool { return gatedLess(res.Gated[i], res.Gated[j]) })
	return res
}

// tallyRevisit counts, per (kind, cohort), the eligible tasks and hits on the
// baseline side (index 0) and the recent side (index 1, from cut onward).
func tallyRevisit(lines []RunLine, outcomes []OutcomeLine, cut time.Time) map[revisitID]*[2]revisitSide {
	sides := map[revisitID]*[2]revisitSide{}
	for _, r := range assemble(lines, outcomes, "") {
		src := scoringSource(r)
		for _, t := range r.tasks {
			if t.snap.PostVerdict == "" {
				continue
			}
			key := CohortKey{Kind: r.kind(), ReviewModel: t.reviewModel(), ImplementerModel: r.implementerModel(t.snap.Index)}
			w := 0
			if !t.ts.Before(cut) {
				w = 1
			}
			for _, dk := range driftKinds {
				ok, hit := dk.test(r, t, src)
				if !ok {
					continue
				}
				id := revisitID{kind: dk.kind, key: key}
				if dk.kind == RevisitUnconfirmed {
					id.source = src
				}
				s := sides[id]
				if s == nil {
					s = &[2]revisitSide{{runs: map[runKey]bool{}}, {runs: map[runKey]bool{}}}
					sides[id] = s
				}
				s[w].add(r.key, hit)
			}
		}
	}
	return sides
}

// enough reports whether a side has the tasks and the distinct runs to be compared.
func (s revisitSide) enough(minRuns int) bool { return s.n >= minRuns && len(s.runs) >= 2 }

func (s *revisitSide) add(k runKey, hit bool) {
	s.n++
	s.runs[k] = true
	if hit {
		s.num++
	}
}

// driftSignal reports a signal only when the two Wilson intervals are disjoint.
func driftSignal(id revisitID, base, rec revisitSide) (Signal, bool) {
	b, rc := wilson(base.num, base.n), wilson(rec.num, rec.n)
	dir := ""
	switch {
	case rc.Lo > b.Hi:
		dir = "up"
	case rc.Hi < b.Lo:
		dir = "down"
	default:
		return Signal{}, false
	}
	key := id.key
	sig := Signal{Kind: id.kind, Cohort: &key, Direction: dir, Recent: &rc, Baseline: &b, Source: id.source,
		ID: fmt.Sprintf("%s:%s/%s/%s", id.kind, key.Kind, key.ImplementerModel, key.ReviewModel)}
	if id.source != "" {
		sig.ID += "/" + id.source
	}
	return sig, true
}

// scoringSource is the one outcome source a run's unconfirmed-flag share is
// scored against, so a run with two outcomes is counted once.
func scoringSource(r *run) string {
	for _, src := range Sources {
		if _, ok := r.outcomes[src]; ok {
			return src
		}
	}
	return ""
}

func gatedLess(a, b Gated) bool {
	x := []string{a.Kind, a.Cohort.Kind, a.Cohort.ImplementerModel, a.Cohort.ReviewModel, a.Source}
	y := []string{b.Kind, b.Cohort.Kind, b.Cohort.ImplementerModel, b.Cohort.ReviewModel, b.Source}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

// notAfter drops lines stamped after now (clock skew between processes)
// before assembly, so a future snapshot can neither displace a task's real
// latest one nor introduce a model. Revisit and CohortModels share it so the
// models recorded as first seen are exactly the ones Revisit scores.
func notAfter(lines []RunLine, now time.Time) []RunLine {
	out := make([]RunLine, 0, len(lines))
	for _, l := range lines {
		if !l.Ts.After(now) {
			out = append(out, l)
		}
	}
	return out
}

// CohortModels returns the models Revisit's cohorts are keyed by at now, so
// the caller records first-seen times for exactly those values.
func CohortModels(lines []RunLine, outcomes []OutcomeLine, now time.Time) []string {
	set := map[string]bool{}
	for _, r := range assemble(notAfter(lines, now), outcomes, "") {
		for _, t := range r.tasks {
			if t.snap.PostVerdict == "" {
				continue
			}
			set[r.implementerModel(t.snap.Index)] = true
			set[t.reviewModel()] = true
		}
	}
	delete(set, "unknown")
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// versionAtLeast compares a "X.Y.Z" server version, tolerating a leading "v"
// and a "-suffix"; anything unparsable is treated as older.
func versionAtLeast(v string, major, minor, patch int) bool {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	got := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		got[i] = n
	}
	want := []int{major, minor, patch}
	for i := range got {
		if got[i] != want[i] {
			return got[i] > want[i]
		}
	}
	return true
}
