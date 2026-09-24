package scorecard

import (
	"math"
	"sort"
	"time"
)

// Rate is a proportion with its 90% Wilson score interval. Num and N are
// always carried so no reader can mistake a rate over three tasks for one
// over three hundred.
type Rate struct {
	Value float64 `json:"value"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
	Num   int     `json:"num"`
	N     int     `json:"n"`
}

const z90 = 1.6448536269514722

func wilson(num, n int) Rate {
	if n <= 0 {
		return Rate{Lo: 0, Hi: 1}
	}
	p := float64(num) / float64(n)
	nf := float64(n)
	denom := 1 + z90*z90/nf
	center := (p + z90*z90/(2*nf)) / denom
	half := z90 * math.Sqrt(p*(1-p)/nf+z90*z90/(4*nf*nf)) / denom
	return Rate{Value: p, Lo: math.Max(0, center-half), Hi: math.Min(1, center+half), Num: num, N: n}
}

type Metrics struct {
	Source               string         `json:"source"`
	Runs                 int            `json:"runs"`
	Tasks                int            `json:"tasks"`
	EscapeRate           Rate           `json:"escape_rate"`
	MinorEscapeRate      Rate           `json:"minor_escape_rate"`
	UnconfirmedFlagRate  Rate           `json:"unconfirmed_flag_rate"`
	WaiveRate            Rate           `json:"waive_rate"`
	CaughtAndFixed       int            `json:"caught_and_fixed"`
	UnattributedFindings map[string]int `json:"unattributed_findings,omitempty"`
	CallsPerTask         float64        `json:"calls_per_task"`
	ReviewMSP50          int64          `json:"review_ms_p50"`
	ReviewMSP95          int64          `json:"review_ms_p95"`
}

// acc accumulates one group's scored tasks.
type acc struct {
	source                string
	runs                  map[runKey]bool
	tasks                 int
	passN, escNum, minNum int
	flagN, unconfNum      int
	waived, atFindings    int
	caught                int
	unattr                map[string]int
	calls                 int
	ms                    []int64
	first, last           time.Time
}

func newAcc(source string) *acc {
	return &acc{source: source, runs: map[runKey]bool{}, unattr: map[string]int{}}
}

func (a *acc) addTask(r *run, t *task, o OutcomeLine) {
	if !a.runs[r.key] {
		a.runs[r.key] = true
		for _, f := range o.Findings {
			if f.TaskIndex == 0 {
				a.unattr[f.Severity]++
			}
		}
	}
	a.tasks++
	high, minor := outcomeCounts(o, t.snap.Index)
	if t.snap.PostVerdict == "pass" {
		a.passN++
		if high > 0 {
			a.escNum++
		} else if minor > 0 {
			a.minNum++
		}
		if t.everFlagged {
			a.caught++
		}
	}
	if isFlag(t.snap.PostVerdict) {
		a.flagN++
		if high == 0 {
			a.unconfNum++
		}
	}
	sev := 0
	for _, n := range t.snap.Severity {
		sev += n
	}
	a.waived += t.snap.Waived
	a.atFindings += sev + t.snap.Waived
	a.calls += len(t.snap.Calls) + t.snap.CallsDropped
	for _, c := range t.snap.Calls {
		if c.Tool == "validate_completion" {
			a.ms = append(a.ms, c.MS)
		}
	}
	if a.first.IsZero() || t.ts.Before(a.first) {
		a.first = t.ts
	}
	if t.ts.After(a.last) {
		a.last = t.ts
	}
}

func (a *acc) metrics() Metrics {
	m := Metrics{
		Source:              a.source,
		Runs:                len(a.runs),
		Tasks:               a.tasks,
		EscapeRate:          wilson(a.escNum, a.passN),
		MinorEscapeRate:     wilson(a.minNum, a.passN),
		UnconfirmedFlagRate: wilson(a.unconfNum, a.flagN),
		WaiveRate:           wilson(a.waived, a.atFindings),
		CaughtAndFixed:      a.caught,
		ReviewMSP50:         percentile(a.ms, 50),
		ReviewMSP95:         percentile(a.ms, 95),
	}
	if len(a.unattr) > 0 {
		m.UnattributedFindings = a.unattr
	}
	if a.tasks > 0 {
		m.CallsPerTask = float64(a.calls) / float64(a.tasks)
	}
	return m
}

// percentile is nearest-rank, matching internal/stats' rollup.
func percentile(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := int(math.Ceil(float64(p)/100*float64(len(s)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}
