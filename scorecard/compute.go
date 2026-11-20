package scorecard

import (
	"cmp"
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
	Kind             string `json:"kind,omitempty"`
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
	GeneratedAt   time.Time       `json:"generated_at"`
	MinRuns       int             `json:"min_runs"`
	SkippedLines  int             `json:"skipped_lines"`
	Cohorts       []Group         `json:"cohorts"`
	ByReviewModel []Group         `json:"by_review_model"`
	ByToolModel   []ToolModelRow  `json:"by_tool_model"`
	Publishers    []string        `json:"publishers,omitempty"`
	ByPublisher   *PublisherViews `json:"by_publisher,omitempty"`
	ModelSets     []ModelSet      `json:"model_sets"`
}

// PublisherViews repeats the review-model and tool-model views with the
// publisher in every group's identity, for the per-user breakdown.
type PublisherViews struct {
	ByReviewModel []Group        `json:"by_review_model"`
	ByToolModel   []ToolModelRow `json:"by_tool_model"`
}

func Compute(lines []RunLine, outcomes []OutcomeLine, opts Options) Scorecard {
	if opts.MinRuns <= 0 {
		opts.MinRuns = DefaultMinRuns
	}
	runs := assemble(lines, outcomes, opts.Publisher)
	byReview := func(r *run, t *task) CohortKey { return CohortKey{Kind: r.kind(), ReviewModel: t.reviewModel()} }
	sc := Scorecard{
		GeneratedAt: opts.Now,
		MinRuns:     opts.MinRuns,
		Cohorts: groupTasks(runs, func(r *run, t *task) CohortKey {
			return CohortKey{r.kind(), t.reviewModel(), t.serverVersion(), r.implementerModel(t.snap.Index)}
		}, false, opts.MinRuns),
		ByReviewModel: groupTasks(runs, byReview, false, opts.MinRuns),
		ByToolModel:   toolModelRows(runs, false),
		Publishers:    publishers(runs),
		ModelSets:     modelSets(runs),
	}
	if opts.Publisher == "" && len(sc.Publishers) > 1 {
		sc.ByPublisher = &PublisherViews{
			ByReviewModel: groupTasks(runs, byReview, true, opts.MinRuns),
			ByToolModel:   toolModelRows(runs, true),
		}
	}
	return sc
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
	sort.Slice(groups, func(i, j int) bool { return groupLess(groups[i], groups[j]) })
	assignRegression(groups, minRuns)
	return groups
}

// groupLess orders groups by source, publisher, run kind, review model,
// server version and implementer model.
func groupLess(a, b Group) bool {
	return cmp.Or(
		cmp.Compare(a.Source, b.Source),
		cmp.Compare(a.Publisher, b.Publisher),
		cmp.Compare(a.Key.Kind, b.Key.Kind),
		cmp.Compare(a.Key.ReviewModel, b.Key.ReviewModel),
		cmp.Compare(a.Key.ServerVersion, b.Key.ServerVersion),
		cmp.Compare(a.Key.ImplementerModel, b.Key.ImplementerModel),
	) < 0
}

// comparableGroups reports whether c may serve as a baseline for g: same
// source, publisher and run kind, and wholly earlier.
func comparableGroups(g, c *Group) bool {
	return c.Source == g.Source && c.Publisher == g.Publisher && c.Key.Kind == g.Key.Kind && c.last.Before(g.first)
}

// assignRegression compares each group with its baseline. Candidates are the
// groups comparableGroups admits. Among them, the most recent one reviewed and
// implemented by the same models is the baseline, so a release is measured
// against the same models on the release before; when no candidate shares both
// models, the most recent candidate of any key is used. groups arrives sorted,
// and the strict After below keeps the first-sorted group on a tie. Only a
// disjoint interval counts as a regression, and only once both sides have
// minRuns runs.
func assignRegression(groups []Group, minRuns int) {
	for i := range groups {
		g := &groups[i]
		var base, sameModels *Group
		for j := range groups {
			c := &groups[j]
			if j == i || !comparableGroups(g, c) {
				continue
			}
			if base == nil || c.last.After(base.last) {
				base = c
			}
			if c.Key.ReviewModel == g.Key.ReviewModel && c.Key.ImplementerModel == g.Key.ImplementerModel &&
				(sameModels == nil || c.last.After(sameModels.last)) {
				sameModels = c
			}
		}
		if sameModels != nil {
			base = sameModels
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
