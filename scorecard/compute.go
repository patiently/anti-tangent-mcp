package scorecard

import (
	"sort"
	"time"
)

const DefaultMinRuns = 10

const (
	RegressionNoBaseline       = "no_baseline"
	RegressionInsufficientData = "insufficient_data"
	RegressionRegressed        = "regressed"
	RegressionOK               = "ok"
)

type CohortKey struct {
	ReviewModel      string `json:"review_model"`
	ServerVersion    string `json:"server_version,omitempty"`
	ImplementerModel string `json:"implementer_model,omitempty"`
}

type Group struct {
	Key       CohortKey `json:"key"`
	Publisher string    `json:"publisher,omitempty"`
	Metrics
	Baseline   *CohortKey `json:"baseline,omitempty"`
	Regression string     `json:"regression"`

	first, last time.Time
}

type Options struct {
	// MinRuns is how many runs both a cohort and its baseline need before
	// Regression may be regressed or ok. Zero or less means DefaultMinRuns.
	MinRuns int
	// Publisher, when non-empty, scores only that publisher's records.
	Publisher string
	Now       time.Time
}

type Scorecard struct {
	GeneratedAt   time.Time `json:"generated_at"`
	MinRuns       int       `json:"min_runs"`
	SkippedLines  int       `json:"skipped_lines"`
	Cohorts       []Group   `json:"cohorts"`
	ByReviewModel []Group   `json:"by_review_model"`
}

func Compute(lines []RunLine, outcomes []OutcomeLine, opts Options) Scorecard {
	if opts.MinRuns <= 0 {
		opts.MinRuns = DefaultMinRuns
	}
	runs := assemble(lines, outcomes, opts.Publisher)
	return Scorecard{
		GeneratedAt: opts.Now,
		MinRuns:     opts.MinRuns,
		Cohorts: groupTasks(runs, func(r *run, t *task) CohortKey {
			return CohortKey{t.reviewModel(), t.serverVersion(), r.implementerModel(t.snap.Index)}
		}, false, opts.MinRuns),
		ByReviewModel: groupTasks(runs, func(_ *run, t *task) CohortKey {
			return CohortKey{ReviewModel: t.reviewModel()}
		}, false, opts.MinRuns),
	}
}

// groupTasks scores every task that has a final verdict, in every run that
// has an outcome for a source, into the group keyOf names. byPublisher adds
// the run's publisher to the group identity.
func groupTasks(runs map[runKey]*run, keyOf func(*run, *task) CohortKey, byPublisher bool, minRuns int) []Group {
	type gid struct {
		source, publisher string
		key               CohortKey
	}
	accs := map[gid]*acc{}
	for _, r := range runs {
		for _, src := range Sources {
			o, ok := r.outcomes[src]
			if !ok {
				continue
			}
			for _, t := range r.tasks {
				if t.snap.PostVerdict == "" {
					continue
				}
				id := gid{source: src, key: keyOf(r, t)}
				if byPublisher {
					id.publisher = r.key.publisher
				}
				a, ok := accs[id]
				if !ok {
					a = newAcc(src)
					accs[id] = a
				}
				a.addTask(r, t, o)
			}
		}
	}
	groups := make([]Group, 0, len(accs))
	for id, a := range accs {
		groups = append(groups, Group{Key: id.key, Publisher: id.publisher, Metrics: a.metrics(), first: a.first, last: a.last})
	}
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Publisher != b.Publisher {
			return a.Publisher < b.Publisher
		}
		if a.Key.ReviewModel != b.Key.ReviewModel {
			return a.Key.ReviewModel < b.Key.ReviewModel
		}
		if a.Key.ServerVersion != b.Key.ServerVersion {
			return a.Key.ServerVersion < b.Key.ServerVersion
		}
		return a.Key.ImplementerModel < b.Key.ImplementerModel
	})
	assignRegression(groups, minRuns)
	return groups
}

// assignRegression compares each group with its baseline: the group of the
// same source and publisher whose last scored task came most recently before
// this group's first. Any key dimension may differ, since a changed model or
// version is what is being measured. groups arrives sorted, and the strict
// After below keeps the first-sorted group on a tie. Only a disjoint interval
// counts as a regression, and only once both sides have minRuns runs.
func assignRegression(groups []Group, minRuns int) {
	for i := range groups {
		g := &groups[i]
		var base *Group
		for j := range groups {
			c := &groups[j]
			if j == i || c.Source != g.Source || c.Publisher != g.Publisher || !c.last.Before(g.first) {
				continue
			}
			if base == nil || c.last.After(base.last) {
				base = c
			}
		}
		if base == nil {
			g.Regression = RegressionNoBaseline
			continue
		}
		k := base.Key
		g.Baseline = &k
		switch {
		case g.Runs < minRuns || base.Runs < minRuns:
			g.Regression = RegressionInsufficientData
		case g.EscapeRate.Lo > base.EscapeRate.Hi:
			g.Regression = RegressionRegressed
		default:
			g.Regression = RegressionOK
		}
	}
}
