# Run Outcome Scorecard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Score anti-tangent's verdicts against an independent review of the finished work, per task, grouped by the models involved, and show the result locally and pooled across a team through Basic Memory.

**Architecture:** The server records a content-free per-task snapshot (`runs.jsonl`) including a log of every anti-tangent call and the model that answered it, and a new deterministic tool `record_review_outcome` records what the final review or `review-now` found per task (`outcomes.jsonl`). A new public, stdlib-only package `scorecard/` joins the two into `scorecard.json`; the gnome-topbar daemon imports the same package to render `/ui/runs` and to score records pooled from Basic Memory.

**Tech Stack:** Go 1.25, `github.com/modelcontextprotocol/go-sdk/mcp`, stdlib `net/http` + `html` in the daemon, Basic Memory over the daemon's existing MCP-HTTP client.

**Spec:** `docs/superpowers/specs/2026-09-23-run-outcome-scorecard-design.md`

## Global Constraints

- **Branch `version/0.26.0`.** Every task that changes user-visible behaviour adds one bullet under `## [0.26.0] - 2026-09-23` → `### Added` in `CHANGELOG.md` (Task 1 creates the heading). Do **not** edit `VERSION`; the release workflow bumps it.
- **Content-free on disk and in BM.** `runs.jsonl`, `outcomes.jsonl`, `scorecard.json` and every BM `at_run` note hold no task titles, plan headings, finding text, raw `plan_run_id`, or raw session id. The only free-text field is an outcome finding's `category`, normalised by `scorecard.NormalizeCategory` (lower-case, trimmed, ≤ 40 runes).
- **Stats stay best-effort.** No stats write may change a hook's result or fail a call; errors are logged via `slog` and swallowed. `record_review_outcome` reports problems in its result (`recorded:false` + `reason`), never as an MCP-level error.
- **The server stays advisory.** `record_review_outcome` makes no reviewer call and blocks nothing.
- **Import rules.** `scorecard/` imports only the standard library. `internal/stats` may import `scorecard` but never `internal/planrun` or `internal/mcpsrv`. `internal/planrun` may import `scorecard`.
- **JSON keys are a cross-module contract.** Every exported field of every `scorecard` type that is serialized (written to `runs.jsonl`, `outcomes.jsonl`, `scorecard.json` or a BM note) carries an explicit snake_case `json` tag exactly as written in this plan; the daemon decodes by those keys. Two deliberate exceptions: `scorecard.Options` is an in-process argument and is never serialized, and `Group` embeds `Metrics` without a tag so its fields flatten into the group object.
- **Wire enums (exact strings):** sources `final_review`, `review_now`; outcome severities `critical`, `major`, `minor`; regression values `no_baseline`, `insufficient_data`, `regressed`, `ok`; tools `validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`; configured-model roles `plan`, `pre`, `mid`, `post`, `worker`.
- **Defaults:** `ANTI_TANGENT_SCORECARD_MIN_RUNS=10`; call log capped at 32 entries per task; Wilson interval at 90% (z = 1.6448536269514722); `ANTI_TANGENT_SHARE_STATS` is on only when exactly `1`.
- **Comment policy (root `CLAUDE.md` → Comments):** comments explain non-obvious behaviour or invariants only; no task, issue, PR or version references, no "previously"/"no longer".
- **Tests:** `go test -race ./...` from the repo root for server tasks; `cd gnome-topbar/daemon && go test -race ./...` for daemon tasks. No test touches the network.
- **Protocol byte budgets (CI-enforced):** each `docs/protocol/*.md` < 16,000 bytes, `INTEGRATION.md` < 2,000 bytes, and `plugin/anti-tangent-protocol/protocol/` identical to `docs/protocol/`.

**User decisions (already made):**
- Ground truth is the final whole-plan review (`final_review`) and `review-now` / human PR review (`review_now`); scored separately, never summed.
- Outcome findings are attributed per task (`task_index`, `0` = not attributable).
- Approach A: a new MCP tool `record_review_outcome`, not an agent-appended file.
- Start with the proposed metrics: escape rate (headline), noise (unconfirmed-flag rate, waive rate), cost, regression flag gated on ≥ N runs per cohort.
- One branch, one release for server and daemon.
- The gnome-topbar daemon publishes shared records; publishing is gated by `ANTI_TANGENT_SHARE_STATS` (default `0`).
- The shared data is viewable in the daemon UI by every developer, with a per-user breakdown; reading is not gated by the publish flag.
- The overview groups by anti-tangent tool × validator model with performance, and every run shows the models anti-tangent used (configured per role, and per call).

---

### Task 1: `scorecard` package — records, assembly, cohort metrics

**Goal:** A stdlib-only public package `scorecard` that defines the on-disk record types and computes per-cohort and per-review-model metrics with Wilson intervals and a regression flag.

**Files:**
- Create: `scorecard/records.go`
- Create: `scorecard/assemble.go`
- Create: `scorecard/metrics.go`
- Create: `scorecard/compute.go`
- Test: `scorecard/compute_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `scorecard.Compute(runs, outcomes, Options{MinRuns: 10})` returns `Cohorts` and `ByReviewModel` groups whose `escape_rate.num`/`n` count, per source, scored tasks whose final `post_verdict` is `pass` and whose outcome has ≥ 1 `critical`/`major` finding with that `task_index`, over scored tasks whose final `post_verdict` is `pass`.
- [ ] A task whose snapshots go `warn` → `pass` with a clean outcome increments `caught_and_fixed` and does not count toward `unconfirmed_flag_rate.num`.
- [ ] `task_index: 0` outcome findings are counted once per run in `unattributed_findings` by severity and never attributed to a task.
- [ ] For the same `(run_hash, source)` the outcome with the latest `ts` wins; for the same `(run_hash, task index)` the snapshot with the latest `ts` wins.
- [ ] `wilson(3, 10)` returns `lo` ≈ 0.1269 and `hi` ≈ 0.5583 (±0.001); `wilson(0, 0)` returns `{0, 0, 1, 0, 0}`.
- [ ] Baseline eligibility: a group's baseline is another group **in the same view** (`cohorts` or `by_review_model`) with the same `source` and `publisher` whose last scored task is strictly before this group's first scored task; any cohort-key dimension may differ (a new review model, server version or implementer model is exactly the change being measured). Among eligible groups the one with the latest last-scored task wins; a tie goes to the group that sorts first in the view's order (source, publisher, review model, server version, implementer model).
- [ ] `regression` is `no_baseline` with no eligible baseline, `insufficient_data` when either cohort has fewer than `MinRuns` runs, `regressed` when the current escape-rate `lo` exceeds the baseline's `hi`, otherwise `ok`.
- [ ] `scorecard.HashRunID("s", "pr_abc")` returns `r_6ffe28feb9d3bc37b1441388`; it is the only implementation of the run-hash construction in the repository.
- [ ] `go list -deps ./scorecard` lists only standard-library packages besides `scorecard` itself.

**Verify:** `go test -race ./scorecard/...` → `ok`

**Steps:**

- [ ] **Step 1: Write `scorecard/records.go`**

```go
// Package scorecard scores anti-tangent's verdicts against an independent
// review of the same work. It is public and stdlib-only because two Go
// modules compute with it: the MCP server over its local stats files, and the
// gnome-topbar daemon over records pooled from Basic Memory. Both must derive
// identical numbers from identical records, so neither may fork this logic.
package scorecard

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SourceFinalReview = "final_review"
	SourceReviewNow   = "review_now"
)

// Sources lists every outcome source in display order.
var Sources = []string{SourceFinalReview, SourceReviewNow}

func ValidSource(s string) bool { return s == SourceFinalReview || s == SourceReviewNow }

func ValidSeverity(s string) bool { return s == "critical" || s == "major" || s == "minor" }

// ToolCall is one anti-tangent call made on behalf of a task (or, for
// validate_plan, a run).
type ToolCall struct {
	Tool     string `json:"tool"`
	Model    string `json:"model"`
	Verdict  string `json:"verdict,omitempty"`
	Findings int    `json:"findings"`
	MS       int64  `json:"ms"`
	Partial  bool   `json:"partial,omitempty"`
}

// TaskSnapshot is the content-free state of one plan task after a change.
type TaskSnapshot struct {
	Index          int            `json:"index"`
	PreVerdict     string         `json:"pre_verdict,omitempty"`
	PostVerdict    string         `json:"post_verdict,omitempty"`
	Checkpoints    int            `json:"checkpoints"`
	Attempts       int            `json:"attempts,omitempty"`
	Severity       map[string]int `json:"severity,omitempty"`
	Waived         int            `json:"waived,omitempty"`
	Escalated      bool           `json:"escalated,omitempty"`
	Lite           bool           `json:"lite,omitempty"`
	Unmatched      bool           `json:"unmatched,omitempty"`
	CodesceneState string         `json:"codescene_state,omitempty"`
	Calls          []ToolCall     `json:"calls,omitempty"`
	CallsDropped   int            `json:"calls_dropped,omitempty"`
}

// RunLine is one line of runs.jsonl: a run header (Header true, Task nil) or
// one task's snapshot. Publisher is empty on the server and filled in by the
// daemon when records are pooled.
type RunLine struct {
	Ts               time.Time         `json:"ts"`
	RunHash          string            `json:"run_hash"`
	Publisher        string            `json:"publisher,omitempty"`
	ServerVersion    string            `json:"server_version,omitempty"`
	Header           bool              `json:"header,omitempty"`
	PlanVerdict      string            `json:"plan_verdict,omitempty"`
	PlanQuality      string            `json:"plan_quality,omitempty"`
	TaskCount        int               `json:"task_count,omitempty"`
	ConfiguredModels map[string]string `json:"configured_models,omitempty"`
	PlanCall         *ToolCall         `json:"plan_call,omitempty"`
	Task             *TaskSnapshot     `json:"task,omitempty"`
}

type OutcomeFinding struct {
	TaskIndex int    `json:"task_index"`
	Severity  string `json:"severity"`
	Category  string `json:"category"`
}

type ImplementerModel struct {
	TaskIndex int    `json:"task_index"`
	Model     string `json:"model"`
}

// OutcomeLine is one line of outcomes.jsonl: what one independent review
// found, per task. An empty Findings slice means the review found nothing.
type OutcomeLine struct {
	Ts                time.Time          `json:"ts"`
	RunHash           string             `json:"run_hash"`
	Publisher         string             `json:"publisher,omitempty"`
	Source            string             `json:"source"`
	ReviewerModel     string             `json:"reviewer_model,omitempty"`
	ImplementerModels []ImplementerModel `json:"implementer_models,omitempty"`
	Findings          []OutcomeFinding   `json:"findings"`
}

// HashRunID is the salted digest that stands in for a plan_run_id in every
// record. It lives here, not in either module, because the server writes it
// and the daemon recomputes it to join local task titles: two copies of the
// construction would drift silently and the join would just find nothing.
func HashRunID(salt, planRunID string) string {
	sum := sha256.Sum256([]byte(salt + ":run:" + planRunID))
	return "r_" + hex.EncodeToString(sum[:12])
}

// NormalizeCategory is the only transformation free text gets before it is
// stored: lower-cased, trimmed, and cut to 40 runes so a caller that pastes
// a finding description instead of a category word cannot store it whole.
func NormalizeCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if utf8.RuneCountInString(s) <= 40 {
		return s
	}
	return string([]rune(s)[:40])
}
```

- [ ] **Step 2: Write `scorecard/assemble.go`**

```go
package scorecard

import "time"

type runKey struct{ publisher, hash string }

type task struct {
	snap        TaskSnapshot
	ts          time.Time
	version     string
	everFlagged bool
}

type run struct {
	key      runKey
	header   *RunLine
	tasks    map[int]*task
	outcomes map[string]OutcomeLine
}

func isFlag(v string) bool { return v == "warn" || v == "fail" }

// assemble folds snapshot and outcome lines into runs. For each task the
// latest snapshot wins (a later line wins a timestamp tie); everFlagged
// remembers whether any snapshot ended warn/fail, which is what separates a
// catch that was fixed from a task that simply passed. For each source the
// latest outcome wins, so a re-reviewed run replaces its earlier outcome.
// publisher, when non-empty, keeps only that publisher's records.
func assemble(lines []RunLine, outcomes []OutcomeLine, publisher string) map[runKey]*run {
	runs := map[runKey]*run{}
	get := func(k runKey) *run {
		r, ok := runs[k]
		if !ok {
			r = &run{key: k, tasks: map[int]*task{}, outcomes: map[string]OutcomeLine{}}
			runs[k] = r
		}
		return r
	}
	for i := range lines {
		l := lines[i]
		if publisher != "" && l.Publisher != publisher {
			continue
		}
		r := get(runKey{l.Publisher, l.RunHash})
		if l.Header {
			if r.header == nil || !l.Ts.Before(r.header.Ts) {
				h := l
				r.header = &h
			}
			continue
		}
		if l.Task == nil {
			continue
		}
		t, ok := r.tasks[l.Task.Index]
		flagged := isFlag(l.Task.PostVerdict)
		if !ok {
			r.tasks[l.Task.Index] = &task{snap: *l.Task, ts: l.Ts, version: l.ServerVersion, everFlagged: flagged}
			continue
		}
		t.everFlagged = t.everFlagged || flagged
		if !l.Ts.Before(t.ts) {
			t.snap, t.ts, t.version = *l.Task, l.Ts, l.ServerVersion
		}
	}
	for _, o := range outcomes {
		if publisher != "" && o.Publisher != publisher {
			continue
		}
		if !ValidSource(o.Source) {
			continue
		}
		r := get(runKey{o.Publisher, o.RunHash})
		if prev, ok := r.outcomes[o.Source]; !ok || !o.Ts.Before(prev.Ts) {
			r.outcomes[o.Source] = o
		}
	}
	return runs
}

// latestCall returns the task's most recent call of tool.
func (t *task) latestCall(tool string) (ToolCall, bool) {
	for i := len(t.snap.Calls) - 1; i >= 0; i-- {
		if t.snap.Calls[i].Tool == tool {
			return t.snap.Calls[i], true
		}
	}
	return ToolCall{}, false
}

func (t *task) reviewModel() string {
	if c, ok := t.latestCall("validate_completion"); ok && c.Model != "" {
		return c.Model
	}
	return "unknown"
}

func (t *task) serverVersion() string {
	if t.version == "" {
		return "unknown"
	}
	return t.version
}

// implementerModel prefers the final review's report, because the controller
// that files it is the one that dispatched each task.
func (r *run) implementerModel(index int) string {
	for _, src := range Sources {
		for _, m := range r.outcomes[src].ImplementerModels {
			if m.TaskIndex == index && m.Model != "" {
				return m.Model
			}
		}
	}
	return "unknown"
}

// outcomeCounts returns how many critical/major and how many minor outcome
// findings o attributes to task index.
func outcomeCounts(o OutcomeLine, index int) (high, minor int) {
	for _, f := range o.Findings {
		if f.TaskIndex != index {
			continue
		}
		switch f.Severity {
		case "critical", "major":
			high++
		case "minor":
			minor++
		}
	}
	return high, minor
}
```

- [ ] **Step 3: Write `scorecard/metrics.go`**

```go
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
```

- [ ] **Step 4: Write `scorecard/compute.go`**

```go
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
```

- [ ] **Step 5: Write the failing tests `scorecard/compute_test.go`**

```go
package scorecard

import (
	"math"
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
	want := CohortKey{ReviewModel: "m", ServerVersion: "0.26.0", ImplementerModel: "anthropic:claude-sonnet-5"}
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
```

- [ ] **Step 6: Run the tests**

Run: `go test -race ./scorecard/...`
Expected: `ok`. If a test fails, fix the implementation, not the expectation. The expectations are the spec.

- [ ] **Step 7: Verify the dependency rule**

Run: `go list -deps ./scorecard | grep '\.'`
Expected: exactly one line, `github.com/patiently/anti-tangent-mcp/scorecard` (standard-library import paths contain no dot).

- [ ] **Step 8: Add the CHANGELOG heading and first bullet**

Insert above the latest release entry in `CHANGELOG.md`:

```markdown
## [0.26.0] - 2026-09-23

### Added

- `scorecard` package: scores anti-tangent's per-task verdicts against an independent review of the finished work (escape rate, unconfirmed-flag rate, waive rate, caught-and-fixed), per model cohort, with 90% Wilson intervals and a regression flag that stays `insufficient_data` until both cohorts have enough runs.
```

- [ ] **Step 9: Commit**

```bash
git add scorecard/ CHANGELOG.md
git commit -m "feat(scorecard): score verdicts against an independent review per model cohort"
```

```json:metadata
{"files": ["scorecard/records.go", "scorecard/assemble.go", "scorecard/metrics.go", "scorecard/compute.go", "scorecard/compute_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./scorecard/...", "acceptanceCriteria": ["escape_rate counts pass tasks with a critical/major outcome finding over pass tasks, per source", "warn then pass with a clean outcome is caught_and_fixed, not unconfirmed", "task_index 0 findings counted once per run in unattributed_findings", "latest outcome per (run, source) and latest snapshot per (run, task) win", "wilson(3,10) ~ [0.1269, 0.5583]; wilson(0,0) = {0,0,1,0,0}", "baseline = latest-ending earlier group in the same view, same source and publisher, any key may differ, ties to first-sorted; regression enum no_baseline/insufficient_data/regressed/ok", "HashRunID(\"s\",\"pr_abc\") = r_6ffe28feb9d3bc37b1441388, the single run-hash implementation", "scorecard depends only on the standard library"], "modelTier": "standard"}
```

---

### Task 2: `scorecard` views — tool × model, publishers, model sets, run escapes

**Goal:** Add the overview view grouped by anti-tangent tool × validator model, the per-publisher variants, the configured-model-set strip, and a single-run escape helper for the tool response.

**Files:**
- Create: `scorecard/toolmodel.go`
- Create: `scorecard/runescapes.go`
- Modify: `scorecard/compute.go`
- Test: `scorecard/toolmodel_test.go`

**Acceptance Criteria:**
- [ ] `Scorecard.ByToolModel` has one row per `(tool, model)` seen in any task call log or run-header `plan_call`, ordered `validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`, then by model; each row's `calls`, `verdict_counts`, `findings_per_call`, `ms_p50`, `ms_p95`, `partial_rate` count every call retained in the call logs (the latest 32 per task, per Task 3's cap) and every run-header `plan_call`, including calls in runs that have no outcome. Calls evicted by the cap are not attributable to a tool or model and are reported only as the task's `calls_dropped`.
- [ ] A row's per-source `outcomes[].escape_rate` scores tasks by the model of that tool's **latest** call on the task (`validate_task_spec`, `validate_completion`), by every model that made a checkpoint (`check_progress`), and by the run's `plan_call` model (`validate_plan`); its `unconfirmed_flag_rate` uses that tool's own verdict (for `check_progress`, any warn/fail checkpoint by that model; for `validate_plan`, the run's `plan_verdict`).
- [ ] `Scorecard.Publishers` lists distinct non-empty publishers, sorted. `Scorecard.ByPublisher` is non-nil only when there are ≥ 2 publishers and `Options.Publisher` is empty, and then carries `by_review_model` and `by_tool_model` with `publisher` set on every group and row.
- [ ] `Scorecard.ModelSets` counts runs per distinct `configured_models` map, sorted by runs descending, then by the map's `role=model` string.
- [ ] `RunEscapes(lines, o)` returns the passed tasks with a critical/major finding (highest severity reported), the count of tasks with a final verdict, and `known == (len(lines) > 0)`.

**Verify:** `go test -race ./scorecard/...` → `ok`

**Steps:**

- [ ] **Step 1: Write `scorecard/toolmodel.go`**

```go
package scorecard

import "sort"

// ToolOrder is the display order of anti-tangent's reviewing tools.
var ToolOrder = []string{"validate_plan", "validate_task_spec", "check_progress", "validate_completion"}

type ToolModelOutcome struct {
	Source              string `json:"source"`
	EscapeRate          Rate   `json:"escape_rate"`
	UnconfirmedFlagRate Rate   `json:"unconfirmed_flag_rate"`
}

// ToolModelRow reports one validator model's work in one anti-tangent tool.
// Its operational columns cover the calls still in each task's capped call
// log; a call evicted by the cap is known only as a count, with no tool or
// model, so it cannot be placed in any row.
// The outcome columns slice escapes by the model that reviewed the task in
// this tool; a task has one outcome and up to four reviewing models, so this
// is a slice, not a causal attribution.
type ToolModelRow struct {
	Tool            string             `json:"tool"`
	Model           string             `json:"model"`
	Publisher       string             `json:"publisher,omitempty"`
	Calls           int                `json:"calls"`
	Runs            int                `json:"runs"`
	Tasks           int                `json:"tasks"`
	VerdictCounts   map[string]int     `json:"verdict_counts"`
	FindingsPerCall float64            `json:"findings_per_call"`
	MSP50           int64              `json:"ms_p50"`
	MSP95           int64              `json:"ms_p95"`
	PartialRate     float64            `json:"partial_rate"`
	Outcomes        []ToolModelOutcome `json:"outcomes,omitempty"`
}

type ModelSet struct {
	Models map[string]string `json:"models"`
	Runs   int               `json:"runs"`
}

type tmKey struct{ tool, model, publisher string }

type tmAcc struct {
	calls, findings, partial int
	verdicts                 map[string]int
	ms                       []int64
	runs                     map[runKey]bool
	tasks                    map[[2]any]bool
	out                      map[string]*[4]int // source -> passN, escNum, flagN, unconfNum
}

func toolModelRows(runs map[runKey]*run, byPublisher bool) []ToolModelRow {
	accs := map[tmKey]*tmAcc{}
	get := func(tool, model string, r *run) *tmAcc {
		k := tmKey{tool: tool, model: model}
		if byPublisher {
			k.publisher = r.key.publisher
		}
		a, ok := accs[k]
		if !ok {
			a = &tmAcc{verdicts: map[string]int{}, runs: map[runKey]bool{}, tasks: map[[2]any]bool{}, out: map[string]*[4]int{}}
			accs[k] = a
		}
		return a
	}
	addCall := func(a *tmAcc, r *run, c ToolCall) {
		a.calls++
		a.findings += c.Findings
		if c.Partial {
			a.partial++
		}
		if c.Verdict != "" {
			a.verdicts[c.Verdict]++
		}
		a.ms = append(a.ms, c.MS)
		a.runs[r.key] = true
	}
	score := func(a *tmAcc, src string, t *task, high int, flagged bool) {
		o, ok := a.out[src]
		if !ok {
			o = &[4]int{}
			a.out[src] = o
		}
		if t.snap.PostVerdict == "pass" {
			o[0]++
			if high > 0 {
				o[1]++
			}
		}
		if flagged {
			o[2]++
			if high == 0 {
				o[3]++
			}
		}
	}
	for _, r := range runs {
		if r.header != nil && r.header.PlanCall != nil {
			a := get("validate_plan", r.header.PlanCall.Model, r)
			addCall(a, r, *r.header.PlanCall)
		}
		for idx, t := range r.tasks {
			for _, c := range t.snap.Calls {
				a := get(c.Tool, c.Model, r)
				addCall(a, r, c)
				a.tasks[[2]any{r.key, idx}] = true
			}
		}
		for _, src := range Sources {
			o, ok := r.outcomes[src]
			if !ok {
				continue
			}
			for _, t := range r.tasks {
				if t.snap.PostVerdict == "" {
					continue
				}
				high, _ := outcomeCounts(o, t.snap.Index)
				for _, tool := range []string{"validate_task_spec", "validate_completion"} {
					if c, ok := t.latestCall(tool); ok {
						score(get(tool, c.Model, r), src, t, high, isFlag(c.Verdict))
					}
				}
				checkpointFlag := map[string]bool{}
				for _, c := range t.snap.Calls {
					if c.Tool == "check_progress" {
						checkpointFlag[c.Model] = checkpointFlag[c.Model] || isFlag(c.Verdict)
					}
				}
				for model, flagged := range checkpointFlag {
					score(get("check_progress", model, r), src, t, high, flagged)
				}
				if r.header != nil && r.header.PlanCall != nil {
					score(get("validate_plan", r.header.PlanCall.Model, r), src, t, high, isFlag(r.header.PlanVerdict))
				}
			}
		}
	}
	rows := make([]ToolModelRow, 0, len(accs))
	for k, a := range accs {
		row := ToolModelRow{
			Tool: k.tool, Model: k.model, Publisher: k.publisher,
			Calls: a.calls, Runs: len(a.runs), Tasks: len(a.tasks),
			VerdictCounts: a.verdicts,
			MSP50:         percentile(a.ms, 50), MSP95: percentile(a.ms, 95),
		}
		if k.tool == "validate_plan" {
			row.Tasks = 0
			for rk := range a.runs {
				if h := runs[rk].header; h != nil {
					row.Tasks += h.TaskCount
				}
			}
		}
		if a.calls > 0 {
			row.FindingsPerCall = float64(a.findings) / float64(a.calls)
			row.PartialRate = float64(a.partial) / float64(a.calls)
		}
		for _, src := range Sources {
			if o, ok := a.out[src]; ok {
				row.Outcomes = append(row.Outcomes, ToolModelOutcome{Source: src, EscapeRate: wilson(o[1], o[0]), UnconfirmedFlagRate: wilson(o[3], o[2])})
			}
		}
		rows = append(rows, row)
	}
	rank := map[string]int{}
	for i, t := range ToolOrder {
		rank[t] = i
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if rank[a.Tool] != rank[b.Tool] {
			return rank[a.Tool] < rank[b.Tool]
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Publisher < b.Publisher
	})
	return rows
}

func modelSets(runs map[runKey]*run) []ModelSet {
	type entry struct {
		models map[string]string
		runs   int
	}
	byKey := map[string]*entry{}
	for _, r := range runs {
		if r.header == nil || len(r.header.ConfiguredModels) == 0 {
			continue
		}
		k := modelSetKey(r.header.ConfiguredModels)
		e, ok := byKey[k]
		if !ok {
			e = &entry{models: r.header.ConfiguredModels}
			byKey[k] = e
		}
		e.runs++
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if byKey[keys[i]].runs != byKey[keys[j]].runs {
			return byKey[keys[i]].runs > byKey[keys[j]].runs
		}
		return keys[i] < keys[j]
	})
	out := make([]ModelSet, 0, len(keys))
	for _, k := range keys {
		out = append(out, ModelSet{Models: byKey[k].models, Runs: byKey[k].runs})
	}
	return out
}

func modelSetKey(m map[string]string) string {
	roles := make([]string, 0, len(m))
	for r := range m {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	s := ""
	for _, r := range roles {
		s += r + "=" + m[r] + ";"
	}
	return s
}

func publishers(runs map[runKey]*run) []string {
	seen := map[string]bool{}
	for k := range runs {
		if k.publisher != "" {
			seen[k.publisher] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 2: Extend `scorecard/compute.go`**

Add these fields to `Scorecard`, after `ByReviewModel`:

```go
	ByToolModel []ToolModelRow  `json:"by_tool_model"`
	Publishers  []string        `json:"publishers,omitempty"`
	ByPublisher *PublisherViews `json:"by_publisher,omitempty"`
	ModelSets   []ModelSet      `json:"model_sets"`
```

Add the type:

```go
// PublisherViews repeats the review-model and tool-model views with the
// publisher in every group's identity, for the per-user breakdown.
type PublisherViews struct {
	ByReviewModel []Group        `json:"by_review_model"`
	ByToolModel   []ToolModelRow `json:"by_tool_model"`
}
```

Replace the `return Scorecard{...}` in `Compute` with:

```go
	byReview := func(_ *run, t *task) CohortKey { return CohortKey{ReviewModel: t.reviewModel()} }
	sc := Scorecard{
		GeneratedAt: opts.Now,
		MinRuns:     opts.MinRuns,
		Cohorts: groupTasks(runs, func(r *run, t *task) CohortKey {
			return CohortKey{t.reviewModel(), t.serverVersion(), r.implementerModel(t.snap.Index)}
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
```

- [ ] **Step 3: Write `scorecard/runescapes.go`**

```go
package scorecard

import "sort"

type Escape struct {
	TaskIndex          int    `json:"task_index"`
	AntiTangentVerdict string `json:"anti_tangent_verdict"`
	OutcomeSeverity    string `json:"outcome_severity"`
}

// RunEscapes scores one run's snapshot lines against one outcome: the tasks
// anti-tangent passed that the outcome found a critical or major problem in,
// how many tasks had a final verdict, and whether any snapshot exists.
func RunEscapes(lines []RunLine, o OutcomeLine) (escapes []Escape, tasksScored int, known bool) {
	escapes = []Escape{}
	if len(lines) == 0 {
		return escapes, 0, false
	}
	runs := assemble(lines, nil, "")
	for _, r := range runs {
		for _, t := range r.tasks {
			if t.snap.PostVerdict == "" {
				continue
			}
			tasksScored++
			if t.snap.PostVerdict != "pass" {
				continue
			}
			worst := ""
			for _, f := range o.Findings {
				if f.TaskIndex != t.snap.Index {
					continue
				}
				if f.Severity == "critical" || (f.Severity == "major" && worst == "") {
					worst = f.Severity
				}
			}
			if worst != "" {
				escapes = append(escapes, Escape{TaskIndex: t.snap.Index, AntiTangentVerdict: "pass", OutcomeSeverity: worst})
			}
		}
	}
	sort.Slice(escapes, func(i, j int) bool { return escapes[i].TaskIndex < escapes[j].TaskIndex })
	return escapes, tasksScored, true
}
```

- [ ] **Step 4: Write the tests `scorecard/toolmodel_test.go`**

```go
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
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./scorecard/...`
Expected: `ok`

- [ ] **Step 6: Commit**

```bash
git add scorecard/
git commit -m "feat(scorecard): tool x model overview, per-publisher views, run escapes"
```

```json:metadata
{"files": ["scorecard/toolmodel.go", "scorecard/runescapes.go", "scorecard/compute.go", "scorecard/toolmodel_test.go"], "verifyCommand": "go test -race ./scorecard/...", "acceptanceCriteria": ["by_tool_model has one row per (tool, model) in tool order then model, with operational columns over every retained call and plan_call", "outcome columns slice by each tool's latest-call model, checkpoint models, and plan_call model", "publishers sorted; by_publisher only with >=2 publishers and no filter", "model_sets counts runs per configured_models map", "RunEscapes returns passed tasks with critical/major findings, tasks scored, and known"], "modelTier": "standard"}
```

---

### Task 3: `planrun` — per-task call log and run metadata

**Goal:** Record, on each plan-run task row, a capped log of every anti-tangent call and its model, and on each run, the configured models, server version and the `validate_plan` call.

**Files:**
- Modify: `internal/planrun/planrun.go`
- Test: `internal/planrun/calls_test.go`

**Acceptance Criteria:**
- [ ] `planrun.ToolCall` is a type alias of `scorecard.ToolCall`.
- [ ] `TaskRow.AppendCall` appends a call; past 32 entries it keeps the latest 32 and adds the number dropped to `TaskRow.CallsDropped`.
- [ ] `Store.SetMeta(runID, RunMeta{...})` stores `ConfiguredModels`, `ServerVersion` and `PlanCall` on the run and returns false for an unknown run.
- [ ] `Store.Snapshot` returns copies: mutating a snapshot's `Rows[i].Calls` or `ConfiguredModels` does not change the stored run.
- [ ] The plan ledger line for a row carries `calls` (the `Row` field marshals the new fields; no ledger code change).

**Verify:** `go test -race ./internal/planrun/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing test `internal/planrun/calls_test.go`**

```go
package planrun

import (
	"testing"
	"time"
)

func TestAppendCallCapsAtThirtyTwo(t *testing.T) {
	var row TaskRow
	for i := 0; i < 40; i++ {
		row.AppendCall(ToolCall{Tool: "check_progress", Model: "m", MS: int64(i)})
	}
	if len(row.Calls) != maxCallLog || row.CallsDropped != 8 {
		t.Fatalf("len=%d dropped=%d", len(row.Calls), row.CallsDropped)
	}
	if row.Calls[0].MS != 8 || row.Calls[maxCallLog-1].MS != 39 {
		t.Fatalf("kept the wrong end: first=%d last=%d", row.Calls[0].MS, row.Calls[maxCallLog-1].MS)
	}
}

func TestSetMetaAndSnapshotIsolation(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	if s.SetMeta("pr_nope", RunMeta{}) {
		t.Fatal("SetMeta on an unknown run reported success")
	}
	ok := s.SetMeta(r.ID, RunMeta{
		ConfiguredModels: map[string]string{"plan": "p", "post": "q"},
		ServerVersion:    "0.26.0",
		PlanCall:         &ToolCall{Tool: "validate_plan", Model: "p"},
	})
	if !ok {
		t.Fatal("SetMeta failed")
	}
	if _, ok := s.Attach(r.ID, "s1", TaskRef{Index: 1}, "pass"); !ok {
		t.Fatal("attach")
	}
	s.UpdateRow(r.ID, "s1", func(row *TaskRow) { row.AppendCall(ToolCall{Tool: "validate_task_spec", Model: "x"}) })

	snap, _ := s.Snapshot(r.ID)
	snap.ConfiguredModels["plan"] = "mutated"
	snap.Rows[0].Calls[0].Model = "mutated"
	snap.PlanCall.Model = "mutated"

	again, _ := s.Snapshot(r.ID)
	if again.ConfiguredModels["plan"] != "p" || again.Rows[0].Calls[0].Model != "x" || again.PlanCall.Model != "p" || again.ServerVersion != "0.26.0" {
		t.Fatalf("snapshot aliased the live run: %+v", again)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/planrun/ -run 'AppendCall|SetMeta'`
Expected: FAIL (undefined: `ToolCall`, `maxCallLog`, `RunMeta`, `SetMeta`)

- [ ] **Step 3: Implement in `internal/planrun/planrun.go`**

Add the import `"github.com/patiently/anti-tangent-mcp/scorecard"`, then:

```go
// ToolCall is shared with the scorecard so a row's call log is written to
// runs.jsonl without conversion.
type ToolCall = scorecard.ToolCall

// maxCallLog bounds a row's call log; a task that checkpoints without limit
// must not grow its row, and every ledger line that carries it, without limit.
const maxCallLog = 32

// AppendCall records one anti-tangent call made for this task.
func (row *TaskRow) AppendCall(c ToolCall) {
	row.Calls = append(row.Calls, c)
	if over := len(row.Calls) - maxCallLog; over > 0 {
		row.Calls = append([]ToolCall(nil), row.Calls[over:]...)
		row.CallsDropped += over
	}
}

// RunMeta is what validate_plan knows about the models behind a run.
type RunMeta struct {
	ConfiguredModels map[string]string
	ServerVersion    string
	PlanCall         *ToolCall
}

// SetMeta stores meta on run runID. Returns false when the run is unknown.
func (s *Store) SetMeta(runID string, meta RunMeta) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	r.ConfiguredModels = cloneStringMap(meta.ConfiguredModels)
	r.ServerVersion = meta.ServerVersion
	r.PlanCall = cloneCall(meta.PlanCall)
	return true
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func cloneCall(c *ToolCall) *ToolCall {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}
```

Add to `TaskRow` (after `Unmatched`):

```go
	// Calls logs every anti-tangent call made for this task, oldest first,
	// capped by AppendCall.
	Calls        []ToolCall `json:"calls,omitempty"`
	CallsDropped int        `json:"calls_dropped,omitempty"`
```

Add to `Run` (after `Tasks`):

```go
	ConfiguredModels map[string]string `json:"configured_models,omitempty"`
	ServerVersion    string            `json:"server_version,omitempty"`
	PlanCall         *ToolCall         `json:"plan_call,omitempty"`
```

In `cloneRow`, add `row.Calls = append([]ToolCall(nil), row.Calls...)` before `return row`. In `Snapshot`, after `cp.Tasks = ...`, add:

```go
	cp.ConfiguredModels = cloneStringMap(r.ConfiguredModels)
	cp.PlanCall = cloneCall(r.PlanCall)
```

- [ ] **Step 4: Run the package tests**

Run: `go test -race ./internal/planrun/...`
Expected: `ok` (existing ledger tests still pass; `calls` is `omitempty`).

- [ ] **Step 5: Commit**

```bash
git add internal/planrun/
git commit -m "feat(planrun): per-task call log and run model metadata"
```

```json:metadata
{"files": ["internal/planrun/planrun.go", "internal/planrun/calls_test.go"], "verifyCommand": "go test -race ./internal/planrun/...", "acceptanceCriteria": ["planrun.ToolCall aliases scorecard.ToolCall", "AppendCall keeps the latest 32 and counts the dropped", "SetMeta stores configured models, version, plan call; false for unknown run", "Snapshot deep-copies Calls, ConfiguredModels and PlanCall", "ledger rows carry calls via the Row field"], "modelTier": "mechanical"}
```

---

### Task 4: `stats` — runs/outcomes files, run hash, scorecard.json

**Goal:** Let the stats recorder append content-free run snapshots and outcomes, hash plan-run ids, compute and write `scorecard.json` (on compaction and after every outcome), prune the new files by retention, and feed the scorecard to the LLM summary.

**Files:**
- Modify: `internal/stats/io.go`
- Modify: `internal/stats/recorder.go`
- Modify: `internal/stats/compactor.go`
- Create: `internal/stats/runs.go`
- Test: `internal/stats/runs_test.go`
- Modify: `internal/stats/compactor_test.go` (only if an existing assertion pins the old system-prompt text or `Compact`'s signature)
- Modify: `internal/config/config.go`
- Test: `internal/config/stats_config_test.go`
- Modify: `cmd/anti-tangent-mcp/main.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `Recorder.RunHash(id)` delegates to `scorecard.HashRunID(salt, id)`; it is `"r_"` + 24 lowercase hex characters, stable across two `stats.New` calls on the same dir, different from `HashSession(id)`, and `""` for a nil recorder or empty id.
- [ ] `Recorder.RecordRunLine` appends to `runs.jsonl`; `Recorder.RecordOutcome` appends to `outcomes.jsonl`, logs one `slog` warning on a write failure and also returns that error (the tool reports it as `recorded:false`); both are no-ops (nil error) on a nil recorder.
- [ ] `Recorder.RunLines(hash)` returns only that hash's lines.
- [ ] `Recorder.WriteScorecard()` writes `scorecard.json` with `min_runs` equal to `Options.MinRuns` (default 10) and `skipped_lines` counting corrupt lines in both files; `RecordOutcome` triggers it asynchronously (single-flight).
- [ ] Compaction writes `scorecard.json` before the summary call, and the summary prompt includes the `by_review_model` JSON when it is non-empty.
- [ ] Retention prunes a run's lines only when the run's newest line is older than the cutoff, and prunes outcomes by their own `ts`.
- [ ] `ANTI_TANGENT_SCORECARD_MIN_RUNS` sets `Config.ScorecardMinRuns` (default 10); a non-positive or non-integer value is a config error naming the variable.

**Verify:** `go test -race ./internal/stats/... ./internal/config/... ./cmd/...` → `ok`

**Steps:**

- [ ] **Step 1: File names in `internal/stats/io.go`**

Extend the `const` block:

```go
	runsFile      = "runs.jsonl"
	outcomesFile  = "outcomes.jsonl"
	scorecardFile = "scorecard.json"
```

Replace `readJSONL` with a counting variant plus a wrapper, keeping the doc comment:

```go
func readJSONL[T any](dir, name string) ([]T, error) {
	out, _, err := readJSONLCounted[T](dir, name)
	return out, err
}

// readJSONLCounted is readJSONL that also reports how many non-blank lines
// failed to parse.
func readJSONLCounted[T any](dir, name string) ([]T, int, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var out []T
	skipped := 0
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			skipped++
			continue
		}
		out = append(out, v)
	}
	return out, skipped, nil
}
```

- [ ] **Step 2: Write the failing tests `internal/stats/runs_test.go`**

```go
package stats

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func newTestRecorder(t *testing.T, dir string, minRuns int) *Recorder {
	t.Helper()
	r, err := New(Options{Dir: dir, SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000,
		RetentionDays: 30, MinRuns: minRuns, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunHashStableAndDistinct(t *testing.T) {
	dir := t.TempDir()
	a := newTestRecorder(t, dir, 0)
	b := newTestRecorder(t, dir, 0)
	h := a.RunHash("pr_0123456789ab")
	if !strings.HasPrefix(h, "r_") || len(h) != 26 || h != b.RunHash("pr_0123456789ab") {
		t.Fatalf("hash %q not stable across recorders", h)
	}
	if strings.TrimPrefix(h, "r_") == a.HashSession("pr_0123456789ab") {
		t.Fatal("run hash must not equal the session hash of the same string")
	}
	var nilRec *Recorder
	if nilRec.RunHash("x") != "" || a.RunHash("") != "" {
		t.Fatal("nil recorder or empty id must hash to empty")
	}
}

func TestRecordAndScore(t *testing.T) {
	dir := t.TempDir()
	r := newTestRecorder(t, dir, 3)
	now := time.Now().UTC()
	h := r.RunHash("pr_aaaaaaaaaaaa")
	r.RecordRunLine(scorecard.RunLine{Ts: now, RunHash: h, Task: &scorecard.TaskSnapshot{Index: 1, PostVerdict: "pass",
		Calls: []scorecard.ToolCall{{Tool: "validate_completion", Model: "m", Verdict: "pass"}}}})
	r.RecordRunLine(scorecard.RunLine{Ts: now, RunHash: "r_other", Task: &scorecard.TaskSnapshot{Index: 1}})
	if err := r.RecordOutcome(scorecard.OutcomeLine{Ts: now, RunHash: h, Source: scorecard.SourceFinalReview,
		Findings: []scorecard.OutcomeFinding{{TaskIndex: 1, Severity: "major", Category: "x"}}}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, runsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()

	lines, err := r.RunLines(h)
	if err != nil || len(lines) != 1 {
		t.Fatalf("RunLines = %d lines, err %v", len(lines), err)
	}

	sc := r.WriteScorecard()
	if sc.MinRuns != 3 || sc.SkippedLines != 1 || len(sc.ByReviewModel) != 1 || sc.ByReviewModel[0].EscapeRate.Num != 1 {
		t.Fatalf("scorecard = %+v", sc)
	}
	b, err := os.ReadFile(filepath.Join(dir, scorecardFile))
	if err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	if json.Unmarshal(b, &disk) != nil || disk["min_runs"].(float64) != 3 {
		t.Fatalf("scorecard.json = %s", b)
	}
}

func TestNilRecorderRunMethods(t *testing.T) {
	var r *Recorder
	r.RecordRunLine(scorecard.RunLine{})
	if err := r.RecordOutcome(scorecard.OutcomeLine{}); err != nil {
		t.Fatal(err)
	}
}

func TestPruneRunsKeepsLiveRunsWhole(t *testing.T) {
	dir := t.TempDir()
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old, recent := cutoff.Add(-48*time.Hour), cutoff.Add(time.Hour)
	_ = rewriteJSONL(dir, runsFile, []scorecard.RunLine{
		{Ts: old, RunHash: "r_live", Header: true},
		{Ts: recent, RunHash: "r_live", Task: &scorecard.TaskSnapshot{Index: 1}},
		{Ts: old, RunHash: "r_dead", Header: true},
	})
	_ = rewriteJSONL(dir, outcomesFile, []scorecard.OutcomeLine{
		{Ts: old, RunHash: "r_live", Source: "final_review"},
		{Ts: recent, RunHash: "r_live", Source: "review_now"},
	})
	if err := pruneRuns(dir, cutoff); err != nil {
		t.Fatal(err)
	}
	if err := pruneOutcomes(dir, cutoff); err != nil {
		t.Fatal(err)
	}
	runs, _ := readJSONL[scorecard.RunLine](dir, runsFile)
	outs, _ := readJSONL[scorecard.OutcomeLine](dir, outcomesFile)
	if len(runs) != 2 || runs[0].RunHash != "r_live" || len(outs) != 1 || outs[0].Source != "review_now" {
		t.Fatalf("runs=%+v outs=%+v", runs, outs)
	}
}

func TestSummaryPromptCarriesScorecard(t *testing.T) {
	sc := &scorecard.Scorecard{ByReviewModel: []scorecard.Group{{Key: scorecard.CohortKey{ReviewModel: "openai:gpt-x"}}}}
	if p := buildSummaryPrompt(Rollup{}, "", sc); !strings.Contains(p, "openai:gpt-x") {
		t.Fatalf("prompt lacks the scorecard: %s", p)
	}
	if p := buildSummaryPrompt(Rollup{}, "", &scorecard.Scorecard{}); strings.Contains(p, "Scorecard") {
		t.Fatalf("empty scorecard must add nothing: %s", p)
	}
}
```

- [ ] **Step 3: Run to see them fail**

Run: `go test ./internal/stats/ -run 'RunHash|RecordAndScore|NilRecorderRun|PruneRuns|SummaryPromptCarries'`
Expected: FAIL (undefined methods and fields).

- [ ] **Step 4: Write `internal/stats/runs.go`**

```go
package stats

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

// RunHash returns the salted digest of a plan_run_id. It is keyed differently
// from HashSession so a run id and a session id that happen to share a value
// never share a digest. The raw plan_run_id is never written to runs.jsonl or
// outcomes.jsonl; this digest is the only join key between them.
func (r *Recorder) RunHash(planRunID string) string {
	if r == nil || planRunID == "" {
		return ""
	}
	return scorecard.HashRunID(r.state.Salt, planRunID)
}

// RecordRunLine appends one content-free run snapshot. Best-effort.
func (r *Recorder) RecordRunLine(l scorecard.RunLine) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := appendJSONL(r.dir, runsFile, l); err != nil {
		r.logger.Warn("stats run snapshot append failed", "err", err)
	}
}

// RecordOutcome appends one outcome and schedules a scorecard refresh, so the
// scorecard reflects an outcome as soon as it lands rather than at the next
// compaction. The error is returned because the tool reports it to its caller.
func (r *Recorder) RecordOutcome(o scorecard.OutcomeLine) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	err := appendJSONL(r.dir, outcomesFile, o)
	r.mu.Unlock()
	if err != nil {
		r.logger.Warn("stats outcome append failed", "err", err)
		return err
	}
	if r.scoring.CompareAndSwap(false, true) {
		go func() {
			defer r.scoring.Store(false)
			defer func() {
				if v := recover(); v != nil {
					r.logger.Warn("stats scorecard refresh panicked", "panic", v)
				}
			}()
			r.WriteScorecard()
		}()
	}
	return nil
}

// RunLines returns runs.jsonl's lines for one run hash.
func (r *Recorder) RunLines(runHash string) ([]scorecard.RunLine, error) {
	if r == nil {
		return nil, nil
	}
	r.mu.Lock()
	all, err := readJSONL[scorecard.RunLine](r.dir, runsFile)
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []scorecard.RunLine
	for _, l := range all {
		if l.RunHash == runHash {
			out = append(out, l)
		}
	}
	return out, nil
}

// WriteScorecard recomputes scorecard.json from runs.jsonl and
// outcomes.jsonl and returns what it wrote. Best-effort: a write failure is
// logged and the computed scorecard is still returned.
func (r *Recorder) WriteScorecard() scorecard.Scorecard {
	r.mu.Lock()
	runs, skipR, errR := readJSONLCounted[scorecard.RunLine](r.dir, runsFile)
	outs, skipO, errO := readJSONLCounted[scorecard.OutcomeLine](r.dir, outcomesFile)
	r.mu.Unlock()
	if errR != nil || errO != nil {
		r.logger.Warn("stats scorecard read failed", "runs_err", errR, "outcomes_err", errO)
	}
	sc := scorecard.Compute(runs, outs, scorecard.Options{MinRuns: r.minRuns, Now: r.clock()})
	sc.SkippedLines = skipR + skipO
	if err := writeJSON(r.dir, scorecardFile, sc); err != nil {
		r.logger.Warn("stats scorecard write failed", "err", err)
	}
	return sc
}

// pruneRuns drops a run's lines only when the run's newest line is older than
// cutoff. Pruning line by line would strip a long-lived run's header while its
// later task lines survive, leaving the run without its models and verdict.
func pruneRuns(dir string, cutoff time.Time) error {
	lines, err := readJSONL[scorecard.RunLine](dir, runsFile)
	if err != nil || lines == nil {
		return err
	}
	newest := map[string]time.Time{}
	for _, l := range lines {
		if l.Ts.After(newest[l.RunHash]) {
			newest[l.RunHash] = l.Ts
		}
	}
	kept := lines[:0]
	for _, l := range lines {
		if !newest[l.RunHash].Before(cutoff) {
			kept = append(kept, l)
		}
	}
	return rewriteJSONL(dir, runsFile, kept)
}

func pruneOutcomes(dir string, cutoff time.Time) error {
	outs, err := readJSONL[scorecard.OutcomeLine](dir, outcomesFile)
	if err != nil || outs == nil {
		return err
	}
	kept := outs[:0]
	for _, o := range outs {
		if !o.Ts.Before(cutoff) {
			kept = append(kept, o)
		}
	}
	return rewriteJSONL(dir, outcomesFile, kept)
}
```

- [ ] **Step 5: Wire it into `internal/stats/recorder.go`**

Add to `Options` (after `RetentionDays`):

```go
	// MinRuns is the scorecard's regression gate; zero means the scorecard
	// default.
	MinRuns int
```

Add to `Recorder`: `minRuns int` and `scoring atomic.Bool` (next to `running`). In `New`, set `minRuns: opts.MinRuns`.

In `compact`, replace `r.compactor.Compact(completedAt, events, csEvents)` with:

```go
	sc := r.WriteScorecard()
	r.compactor.Compact(completedAt, events, csEvents, &sc)
```

and, inside the prune block after `pruneCodescene`, add:

```go
	if err := pruneRuns(r.dir, cutoff); err != nil {
		r.logger.Warn("stats runs prune failed", "err", err)
	}
	if err := pruneOutcomes(r.dir, cutoff); err != nil {
		r.logger.Warn("stats outcomes prune failed", "err", err)
	}
```

- [ ] **Step 6: Feed the scorecard to the summary in `internal/stats/compactor.go`**

Change the signatures to `func (c *Compactor) Compact(now time.Time, events []Event, csEvents []CodesceneEvent, sc *scorecard.Scorecard)` and `func buildSummaryPrompt(r Rollup, prev string, sc *scorecard.Scorecard) string`; pass `sc` through. At the end of `buildSummaryPrompt`, before `return`:

```go
	if sc != nil && len(sc.ByReviewModel) > 0 {
		b, _ := json.MarshalIndent(sc.ByReviewModel, "", "  ")
		fmt.Fprintf(&sb, "\nScorecard by review model (JSON; escape rates are measured against an independent review):\n%s\n", string(b))
	}
```

Replace the sentence `This tool is advisory and has NO ground truth on whether findings were correct or acted upon — do NOT claim findings were right, wrong, useful, or ignored.` in `summarySystemPrompt` with:

```
This tool is advisory and the rollup has NO ground truth on whether findings were correct or acted upon — do NOT claim findings were right, wrong, useful, or ignored. The one exception is the scorecard block when present: its escape rates are measured against an independent review, so you may report them, always with their n, and say whether a regression is flagged; never generalize beyond those numbers.
```

Update every existing caller of `Compact` / `buildSummaryPrompt` in `internal/stats/*_test.go` to pass `nil` as the new last argument. If an existing test asserts the old sentence verbatim, change it to the new text.

- [ ] **Step 7: Config — `internal/config/config.go`**

Add the field after `StatsMaxTokens`:

```go
	// ScorecardMinRuns gates the scorecard's regression flag: a cohort and its
	// baseline each need this many runs before it may read regressed or ok.
	ScorecardMinRuns int
```

Default `ScorecardMinRuns: 10` in the `cfg := Config{...}` literal. Parse it next to `ANTI_TANGENT_STATS_RETENTION_DAYS`, using the same helper and error format that block uses, so `ANTI_TANGENT_SCORECARD_MIN_RUNS=0`, `-1` and `abc` each return an error whose text contains `ANTI_TANGENT_SCORECARD_MIN_RUNS`. Add to `internal/config/stats_config_test.go`:

```go
func TestScorecardMinRuns(t *testing.T) {
	base := map[string]string{"ANTHROPIC_API_KEY": "x"}
	load := func(v string) (Config, error) {
		env := map[string]string{"ANTI_TANGENT_SCORECARD_MIN_RUNS": v}
		for k, val := range base {
			env[k] = val
		}
		return Load(func(k string) string { return env[k] })
	}
	cfg, err := load("")
	if err != nil || cfg.ScorecardMinRuns != 10 {
		t.Fatalf("default: %d, %v", cfg.ScorecardMinRuns, err)
	}
	if cfg, err := load("25"); err != nil || cfg.ScorecardMinRuns != 25 {
		t.Fatalf("25: %d, %v", cfg.ScorecardMinRuns, err)
	}
	for _, bad := range []string{"0", "-1", "abc"} {
		if _, err := load(bad); err == nil || !strings.Contains(err.Error(), "ANTI_TANGENT_SCORECARD_MIN_RUNS") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}
```

(Add the `strings` import if the file lacks it.)

- [ ] **Step 8: `cmd/anti-tangent-mcp/main.go`**

In the `stats.Options{...}` literal add `MinRuns: cfg.ScorecardMinRuns,`.

- [ ] **Step 9: Run the tests**

Run: `go test -race ./internal/stats/... ./internal/config/... ./cmd/...`
Expected: `ok`

- [ ] **Step 10: CHANGELOG bullet** under `## [0.26.0]` → `### Added`:

```markdown
- `runs.jsonl`, `outcomes.jsonl` and `scorecard.json` in `ANTI_TANGENT_STATS_DIR`: content-free per-task run snapshots (verdicts, severity counts and a log of every anti-tangent call with the model that answered it), independent-review outcomes, and the scorecard computed from them; `summary.md` now narrates escape rates per review model. `ANTI_TANGENT_SCORECARD_MIN_RUNS` (default 10) sets how many runs a cohort needs before its regression flag can fire.
```

- [ ] **Step 11: Commit**

```bash
git add internal/stats/ internal/config/ cmd/anti-tangent-mcp/main.go CHANGELOG.md
git commit -m "feat(stats): run snapshots, outcomes and scorecard.json"
```

```json:metadata
{"files": ["internal/stats/io.go", "internal/stats/recorder.go", "internal/stats/compactor.go", "internal/stats/runs.go", "internal/stats/runs_test.go", "internal/stats/compactor_test.go", "internal/config/config.go", "internal/config/stats_config_test.go", "cmd/anti-tangent-mcp/main.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/stats/... ./internal/config/... ./cmd/...", "acceptanceCriteria": ["RunHash is r_ + 24 hex, stable per dir, distinct from HashSession, empty for nil/empty", "RecordRunLine/RecordOutcome append; nil-safe; RecordOutcome returns write error", "RunLines filters by hash", "WriteScorecard writes min_runs and skipped_lines; RecordOutcome triggers it async single-flight", "compaction writes scorecard first and the summary prompt carries by_review_model", "runs pruned per run by newest line; outcomes by ts", "ANTI_TANGENT_SCORECARD_MIN_RUNS default 10, invalid values error naming the var"], "modelTier": "standard"}
```

---

### Task 5: `mcpsrv` — log every call and write run snapshots

**Goal:** Make each plan-run row change append the call that caused it to the row's call log and write a content-free snapshot line to `runs.jsonl`, and record configured models, server version and the `validate_plan` call when a run is minted.

**Files:**
- Create: `internal/mcpsrv/run_snapshots.go`
- Modify: `internal/mcpsrv/plan_run_rows.go`
- Modify: `internal/mcpsrv/handlers.go` (the `Attach` block in `ValidateTaskSpec`, the `recordCheckpointRow(sess)` call in `CheckProgress`, and each `planCallContext{...}` literal that sets `PlanLedger: h.deps.PlanLedger`)
- Modify: `internal/mcpsrv/review_error.go`
- Test: `internal/mcpsrv/run_snapshots_test.go`

**Acceptance Criteria:**
- [ ] After `validate_plan` → `validate_task_spec` → `check_progress` → `validate_completion` on one task with stats enabled, the run snapshot's task call log lists exactly those three lifecycle calls in order, each with the envelope's `model_used`, verdict, finding count and `review_ms`.
- [ ] The run's snapshot header line carries `configured_models` with keys `plan`, `pre`, `mid`, `post`, `worker` (from `Config`), `server_version` equal to `mcpsrv.Version`, and a `plan_call` with `tool: "validate_plan"`.
- [ ] `runs.jsonl` contains no task title and no raw `plan_run_id` (asserted on the file bytes).
- [ ] With `Stats` nil, the same sequence writes no file and every existing test still passes.

**Verify:** `go test -race ./internal/mcpsrv/...` → `ok`

**Steps:**

- [ ] **Step 1: Write `internal/mcpsrv/run_snapshots.go`**

```go
package mcpsrv

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func callFromEnvelope(tool string, env Envelope) planrun.ToolCall {
	return planrun.ToolCall{
		Tool:     tool,
		Model:    env.ModelUsed,
		Verdict:  env.Verdict,
		Findings: len(env.Findings),
		MS:       env.ReviewMS,
		Partial:  env.Partial,
	}
}

// configuredModels is the server's model per reviewing role. A role whose
// model is unset is recorded as "" rather than omitted, so a reader can tell
// an unset worker from a record that predates the role.
func (h *handlers) configuredModels() map[string]string {
	cfg := h.deps.Cfg
	ref := func(m config.ModelRef) string {
		if m.Model == "" {
			return ""
		}
		return m.String()
	}
	return map[string]string{
		"plan":   ref(cfg.PlanModel),
		"pre":    ref(cfg.PreModel),
		"mid":    ref(cfg.MidModel),
		"post":   ref(cfg.PostModel),
		"worker": ref(cfg.WorkerModel),
	}
}

// onPlanRunMinted records the models behind a freshly minted run and writes
// its snapshot header.
func (h *handlers) onPlanRunMinted(runID string, call planrun.ToolCall) {
	h.deps.PlanRuns.SetMeta(runID, planrun.RunMeta{
		ConfiguredModels: h.configuredModels(),
		ServerVersion:    Version,
		PlanCall:         &call,
	})
	if h.deps.Stats == nil {
		return
	}
	run, ok := h.deps.PlanRuns.Snapshot(runID)
	if !ok {
		return
	}
	h.deps.Stats.RecordRunLine(scorecard.RunLine{
		Ts:               time.Now().UTC(),
		RunHash:          h.deps.Stats.RunHash(run.ID),
		ServerVersion:    Version,
		Header:           true,
		PlanVerdict:      run.PlanVerdict,
		PlanQuality:      run.PlanQuality,
		TaskCount:        run.TaskCount,
		ConfiguredModels: run.ConfiguredModels,
		PlanCall:         run.PlanCall,
	})
}

// snapshotRow writes the content-free form of a changed row. TaskTitle and
// SessionID are deliberately not copied.
func (h *handlers) snapshotRow(runID string, row planrun.TaskRow) {
	if h.deps.Stats == nil {
		return
	}
	h.deps.Stats.RecordRunLine(scorecard.RunLine{
		Ts:            time.Now().UTC(),
		RunHash:       h.deps.Stats.RunHash(runID),
		ServerVersion: Version,
		Task: &scorecard.TaskSnapshot{
			Index:          row.Index,
			PreVerdict:     row.PreVerdict,
			PostVerdict:    row.PostVerdict,
			Checkpoints:    row.Checkpoints,
			Attempts:       row.Attempts,
			Severity:       row.Severity,
			Waived:         row.Waived,
			Escalated:      row.Escalated,
			Lite:           row.Lite,
			Unmatched:      row.Unmatched,
			CodesceneState: row.CodesceneState,
			Calls:          row.Calls,
			CallsDropped:   row.CallsDropped,
		},
	})
}
```

- [ ] **Step 2: Route every row change through the snapshot, in `plan_run_rows.go`**

In `appendPlanLedger`, after the `if !ok { return }` guard and before the ledger append, add `h.snapshotRow(runID, row)`. Update its doc comment to say it writes the row to the plan ledger and the run snapshot.

Change `recordCheckpointRow(sess *session.Session)` to `recordCheckpointRow(sess *session.Session, env Envelope)` and its mutate to:

```go
	row, ok := h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, func(row *planrun.TaskRow) {
		row.Checkpoints++
		row.AppendCall(callFromEnvelope("check_progress", env))
	})
```

In `completionRowUpdate`, build the call before the closure (`call := callFromEnvelope("validate_completion", env)`) and add `row.AppendCall(call)` as the closure's last statement.

- [ ] **Step 3: `handlers.go`**

In `CheckProgress`, change `h.recordCheckpointRow(sess)` to `h.recordCheckpointRow(sess, env)`.

In `ValidateTaskSpec`, replace the `Attach` block with:

```go
	if args.PlanRunID != "" && env.SessionID != "" {
		// Best-effort: an unknown or expired run must not fail the review.
		ref := planrun.TaskRef{Index: args.TaskIndex, Title: args.TaskTitle}
		if row, ok := h.deps.PlanRuns.Attach(args.PlanRunID, env.SessionID, ref, env.Verdict); ok {
			call := callFromEnvelope("validate_task_spec", env)
			if logged, ok := h.deps.PlanRuns.UpdateRow(args.PlanRunID, env.SessionID, func(r *planrun.TaskRow) { r.AppendCall(call) }); ok {
				row = logged
			}
			h.appendPlanLedger(args.PlanRunID, row)
		} else {
			slog.Warn("plan run attach failed; run unknown or expired",
				"plan_run_id", args.PlanRunID, "session_id", env.SessionID)
		}
	}
```

In every `planCallContext{...}` literal that sets `PlanLedger: h.deps.PlanLedger,` add `OnMint: h.onPlanRunMinted,` on the next line. Find them with `grep -n "PlanLedger: *h.deps.PlanLedger" internal/mcpsrv/handlers.go`. Every hit must get the line.

- [ ] **Step 4: `review_error.go`**

Add to `planCallContext`, after `PlanLedger`:

```go
	// OnMint, when set, is told about every freshly minted run together with
	// the validate_plan call that produced it. Nil in tests that build a
	// context by hand.
	OnMint func(runID string, call planrun.ToolCall)
```

In `mintPlanRunID`, after the ledger header block, add:

```go
	if c.OnMint != nil {
		c.OnMint(run.ID, planrun.ToolCall{
			Tool:     "validate_plan",
			Model:    c.ModelUsed,
			Verdict:  string(pr.PlanVerdict),
			Findings: len(planFindings(*pr)),
			MS:       c.ReviewMS,
		})
	}
```

- [ ] **Step 5: Write the test `internal/mcpsrv/run_snapshots_test.go`**

Build handlers with a stats recorder (the same `stats.New` options `handlers_stats_test.go` uses) plus the reviewer fixture `handlers_stats_test.go`'s `TestValidatePlanRecordsStats` uses, so `validate_plan` passes. Then:

1. Call `ValidatePlan` on a one-task plan whose only task heading is `### Task 1: Secret title ZXQ`; read `PlanRunID` from the result.
2. Call `ValidateTaskSpec` with that `PlanRunID` and `TaskIndex: 1` and keep `env.SessionID`.
3. Call `CheckProgress` for that session (reuse the args shape from `TestCheckProgress_HappyPath` in `handlers_test.go`).
4. Call `ValidateCompletion` with `completionCallArgs(sessionID)`.
5. Read `runs.jsonl` with `stats`' public surface: `rec.RunLines(rec.RunHash(planRunID))`.

Assert:

```go
	var header *scorecard.RunLine
	var last *scorecard.TaskSnapshot
	for i := range lines {
		if lines[i].Header {
			header = &lines[i]
		} else if lines[i].Task != nil {
			last = lines[i].Task
		}
	}
	require.NotNil(t, header)
	assert.Equal(t, Version, header.ServerVersion)
	for _, role := range []string{"plan", "pre", "mid", "post", "worker"} {
		_, ok := header.ConfiguredModels[role]
		assert.True(t, ok, "configured_models lacks %s", role)
	}
	require.NotNil(t, header.PlanCall)
	assert.Equal(t, "validate_plan", header.PlanCall.Tool)

	require.NotNil(t, last)
	var tools []string
	for _, c := range last.Calls {
		tools = append(tools, c.Tool)
		assert.NotEmpty(t, c.Model)
	}
	assert.Equal(t, []string{"validate_task_spec", "check_progress", "validate_completion"}, tools)

	raw, err := os.ReadFile(filepath.Join(dir, "runs.jsonl"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "ZXQ")
	assert.NotContains(t, string(raw), planRunID)
```

Add a second test that runs the same four calls with `Stats: nil` and asserts `os.ReadDir(dir)` shows no `runs.jsonl`. The dir is a `t.TempDir()` not handed to any recorder.

- [ ] **Step 6: Run the package tests**

Run: `go test -race ./internal/mcpsrv/...`
Expected: `ok`. A compile error in an existing test that calls `recordCheckpointRow` directly is fixed by passing `Envelope{}`.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(mcpsrv): log every lifecycle call per task and write run snapshots"
```

```json:metadata
{"files": ["internal/mcpsrv/run_snapshots.go", "internal/mcpsrv/plan_run_rows.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/review_error.go", "internal/mcpsrv/run_snapshots_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/...", "acceptanceCriteria": ["task call log lists validate_task_spec, check_progress, validate_completion in order with model, verdict, findings, ms", "header carries configured_models plan/pre/mid/post/worker, server_version = Version, plan_call validate_plan", "runs.jsonl bytes contain no task title and no raw plan_run_id", "with Stats nil no file is written and existing tests pass"], "modelTier": "standard"}
```

---

### Task 6: `record_review_outcome` tool

**Goal:** Register a tenth, deterministic MCP tool that records an independent review's per-task findings for a plan run, validates them, and returns this run's escapes immediately.

**Files:**
- Create: `internal/mcpsrv/outcome_handler.go`
- Test: `internal/mcpsrv/outcome_handler_test.go`
- Modify: `internal/mcpsrv/server.go`
- Modify: `internal/mcpsrv/tool_schema_contract_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `tools/list` includes `record_review_outcome`; its input schema requires exactly `findings`, `plan_run_id`, `source`; `findings[]` requires `category`, `severity`, `task_index`; `implementer_models[]` requires `model`, `task_index`; every property has a description.
- [ ] With `Stats` nil the call returns `recorded:false` and a `reason` naming `ANTI_TANGENT_STATS_DIR`, and no error.
- [ ] An empty `plan_run_id`, a `source` other than `final_review`/`review_now`, a severity outside `critical`/`major`/`minor`, a negative `task_index`, more than 500 findings, an `implementer_models` entry with `task_index < 1` or an empty model, or (for a run known from the live store or a snapshot header, including one with zero tasks) a `task_index` above the run's task count each return `recorded:false` with a `reason` naming the offending field, write nothing, and return no MCP error.
- [ ] A valid call for a known run appends one `outcomes.jsonl` line (categories normalised, `run_hash` not the raw id) and returns `recorded:true`, `run_known:true`, `tasks_scored`, and `escapes` listing each passed task with a critical/major finding.
- [ ] A valid call for an unknown run is recorded with `run_known:false` and `escapes: []`. A run the live plan-run store knows but that has no snapshot lines (stats enabled after the run was minted) reports `run_known:true`, `tasks_scored: 0`, `escapes: []`.
- [ ] `summary_block` starts with `record_review_outcome` and names the source, the run id, the tasks scored and the escape count; one `slog` line with msg `record_review_outcome` is emitted per call carrying `duration_ms`, `source`, `recorded`, `run_known`, `escapes`.

**Verify:** `go test -race ./internal/mcpsrv/...` → `ok`

**Steps:**

- [ ] **Step 1: Write `internal/mcpsrv/outcome_handler.go`**

```go
package mcpsrv

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

const maxOutcomeFindings = 500

type OutcomeFindingArg struct {
	TaskIndex int    `json:"task_index" jsonschema:"1-based plan task the finding belongs to; 0 when it cannot be attributed to one task."`
	Severity  string `json:"severity" jsonschema:"critical, major or minor. Map your own scale; drop nits rather than sending them."`
	Category  string `json:"category" jsonschema:"One category word such as correctness, security, tests, docs. Never the finding text: only the first 40 characters, lower-cased, are stored."`
}

type OutcomeImplementerModelArg struct {
	TaskIndex int    `json:"task_index" jsonschema:"1-based plan task."`
	Model     string `json:"model" jsonschema:"provider:model the task was dispatched on, e.g. anthropic:claude-sonnet-5."`
}

type RecordReviewOutcomeArgs struct {
	PlanRunID         string                       `json:"plan_run_id" jsonschema:"The plan_run_id returned by validate_plan for the run the review covered."`
	Source            string                       `json:"source" jsonschema:"final_review for the controller's whole-plan review, review_now for a human-adjudicated PR review."`
	ReviewerModel     string                       `json:"reviewer_model,omitempty" jsonschema:"provider:model that performed the review, when known."`
	ImplementerModels []OutcomeImplementerModelArg `json:"implementer_models,omitempty" jsonschema:"The model each task was dispatched on. The controller knows this; the server cannot see it."`
	Findings          []OutcomeFindingArg          `json:"findings" jsonschema:"Every finding the review kept, attributed to a task. An empty array means the review found nothing, which is itself recorded."`
}

type RecordReviewOutcomeResult struct {
	Recorded     bool               `json:"recorded"`
	Reason       string             `json:"reason,omitempty"`
	RunKnown     bool               `json:"run_known"`
	TasksScored  int                `json:"tasks_scored"`
	Escapes      []scorecard.Escape `json:"escapes"`
	SummaryBlock string             `json:"summary_block"`
}

func recordReviewOutcomeTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "record_review_outcome",
		Description: "Record what an independent review of a finished plan run found, per task, so anti-tangent's own verdicts can be scored against it. " +
			"Call once after the final whole-plan review (source final_review), and again after a human-adjudicated PR review (source review_now); a later call for the same run and source replaces the earlier one. " +
			"Send categories and severities only, never finding text. Deterministic and free: no reviewer model is called. Returns the tasks anti-tangent passed that the review found a critical or major problem in.",
	}
}

func (h *handlers) RecordReviewOutcome(_ context.Context, _ *mcp.CallToolRequest, args RecordReviewOutcomeArgs) (*mcp.CallToolResult, RecordReviewOutcomeResult, error) {
	start := time.Now()
	res := h.recordReviewOutcome(args)
	res.SummaryBlock = formatOutcomeSummary(args, res)
	slog.Info("record_review_outcome",
		slog.String("tool", "record_review_outcome"),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		slog.String("source", args.Source),
		slog.Bool("recorded", res.Recorded),
		slog.Bool("run_known", res.RunKnown),
		slog.Int("escapes", len(res.Escapes)),
	)
	return nil, res, nil
}

func (h *handlers) recordReviewOutcome(args RecordReviewOutcomeArgs) RecordReviewOutcomeResult {
	res := RecordReviewOutcomeResult{Escapes: []scorecard.Escape{}}
	if h.deps.Stats == nil {
		res.Reason = "stats disabled: set ANTI_TANGENT_STATS_DIR to record review outcomes"
		return res
	}
	runID := strings.TrimSpace(args.PlanRunID)
	if reason := validateOutcomeArgs(runID, args); reason != "" {
		res.Reason = reason
		return res
	}
	runHash := h.deps.Stats.RunHash(runID)
	lines, err := h.deps.Stats.RunLines(runHash)
	if err != nil {
		slog.Warn("record_review_outcome: reading run snapshots failed", "err", err)
	}
	if n, known := h.outcomeTaskCount(runID, lines); known {
		for i, f := range args.Findings {
			if f.TaskIndex > n {
				res.Reason = fmt.Sprintf("findings[%d].task_index %d exceeds the run's %d tasks", i, f.TaskIndex, n)
				return res
			}
		}
	}
	o := scorecard.OutcomeLine{
		Ts:            time.Now().UTC(),
		RunHash:       runHash,
		Source:        args.Source,
		ReviewerModel: strings.TrimSpace(args.ReviewerModel),
		Findings:      make([]scorecard.OutcomeFinding, 0, len(args.Findings)),
	}
	for _, f := range args.Findings {
		o.Findings = append(o.Findings, scorecard.OutcomeFinding{TaskIndex: f.TaskIndex, Severity: f.Severity, Category: scorecard.NormalizeCategory(f.Category)})
	}
	for _, m := range args.ImplementerModels {
		o.ImplementerModels = append(o.ImplementerModels, scorecard.ImplementerModel{TaskIndex: m.TaskIndex, Model: strings.TrimSpace(m.Model)})
	}
	if err := h.deps.Stats.RecordOutcome(o); err != nil {
		res.Reason = "writing outcomes.jsonl failed: " + err.Error()
		return res
	}
	res.Recorded = true
	var snapshotted bool
	res.Escapes, res.TasksScored, snapshotted = scorecard.RunEscapes(lines, o)
	// A run minted before stats were enabled is live but has no snapshot
	// lines; it is still a run this server knows.
	_, live := h.deps.PlanRuns.PlanTaskCount(runID)
	res.RunKnown = snapshotted || live
	return res
}

func validateOutcomeArgs(runID string, args RecordReviewOutcomeArgs) string {
	switch {
	case runID == "":
		return "plan_run_id is required"
	case !scorecard.ValidSource(args.Source):
		return `source must be "final_review" or "review_now"`
	case len(args.Findings) > maxOutcomeFindings:
		return fmt.Sprintf("findings has %d entries; at most %d are accepted", len(args.Findings), maxOutcomeFindings)
	}
	for i, f := range args.Findings {
		if !scorecard.ValidSeverity(f.Severity) {
			return fmt.Sprintf("findings[%d].severity %q must be critical, major or minor", i, f.Severity)
		}
		if f.TaskIndex < 0 {
			return fmt.Sprintf("findings[%d].task_index must be 0 or a 1-based task number", i)
		}
	}
	for i, m := range args.ImplementerModels {
		if m.TaskIndex < 1 || strings.TrimSpace(m.Model) == "" {
			return fmt.Sprintf("implementer_models[%d] needs a 1-based task_index and a model", i)
		}
	}
	return ""
}

// outcomeTaskCount is the run's task count from the live store, else from
// its snapshot header. known is false only when neither has the run; then
// task indexes cannot be range-checked. A known run with zero tasks is still
// known, so every positive task_index is rejected for it.
func (h *handlers) outcomeTaskCount(runID string, lines []scorecard.RunLine) (n int, known bool) {
	if n, ok := h.deps.PlanRuns.PlanTaskCount(runID); ok {
		return n, true
	}
	for _, l := range lines {
		if l.Header {
			return l.TaskCount, true
		}
	}
	return 0, false
}

func formatOutcomeSummary(args RecordReviewOutcomeArgs, res RecordReviewOutcomeResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "record_review_outcome · source: %s · run: %s\n", args.Source, strings.TrimSpace(args.PlanRunID))
	if !res.Recorded {
		fmt.Fprintf(&b, "recorded: no — %s\n", res.Reason)
		return b.String()
	}
	known := "yes"
	if !res.RunKnown {
		known = "no (no snapshot for this run yet; it is scored once one exists)"
	}
	fmt.Fprintf(&b, "recorded: yes · run known: %s · tasks scored: %d · escapes: %d\n", known, res.TasksScored, len(res.Escapes))
	for _, e := range res.Escapes {
		fmt.Fprintf(&b, "- task %d: anti-tangent %s, review found %s\n", e.TaskIndex, e.AntiTangentVerdict, e.OutcomeSeverity)
	}
	return b.String()
}
```

- [ ] **Step 2: Register it in `server.go`**

Add `mcp.AddTool(srv, recordReviewOutcomeTool(), h.RecordReviewOutcome)` after the `plan_run_report` registration. Rewrite the `New` doc comment's tool list so it names all ten tools without version tags, e.g. `// New creates and returns a configured MCP server with its ten tools: validate_task_spec, check_progress, validate_completion, validate_plan, prime_project_knowledge, extract_project_knowledge, plan_run_report, record_review_outcome, bulk_read and code_write.`

- [ ] **Step 3: Update `tool_schema_contract_test.go`**

Add `"record_review_outcome"` to the sorted name list in `TestToolInputSchemas_ToolSetAndShape` (between `prime_project_knowledge` and `validate_completion`). Add to `want` in `TestToolInputSchemas_RequiredSetsUnchanged`:

```go
		"record_review_outcome":                      {"findings", "plan_run_id", "source"},
		"record_review_outcome.findings[]":           {"category", "severity", "task_index"},
		"record_review_outcome.implementer_models[]": {"model", "task_index"},
```

- [ ] **Step 4: Write `internal/mcpsrv/outcome_handler_test.go`**

```go
package mcpsrv

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func outcomeHandlers(t *testing.T) (*handlers, *stats.Recorder, string) {
	t.Helper()
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{Dir: dir, SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000, RetentionDays: 30, Logger: slog.Default()})
	require.NoError(t, err)
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	h.deps.Stats = rec
	return h, rec, dir
}

func recordOutcome(t *testing.T, h *handlers, args RecordReviewOutcomeArgs) RecordReviewOutcomeResult {
	t.Helper()
	_, res, err := h.RecordReviewOutcome(context.Background(), nil, args)
	require.NoError(t, err)
	return res
}

func TestRecordReviewOutcome_StatsDisabled(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: "pr_x", Source: "final_review"})
	assert.False(t, res.Recorded)
	assert.Contains(t, res.Reason, "ANTI_TANGENT_STATS_DIR")
}

func TestRecordReviewOutcome_Validation(t *testing.T) {
	h, _, dir := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 2)
	cases := map[string]RecordReviewOutcomeArgs{
		"plan_run_id":        {Source: "final_review"},
		"source":             {PlanRunID: run.ID, Source: "coderabbit"},
		"severity":           {PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{{TaskIndex: 1, Severity: "nit", Category: "x"}}},
		"task_index":         {PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{{TaskIndex: -1, Severity: "major", Category: "x"}}},
		"exceeds":            {PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{{TaskIndex: 3, Severity: "major", Category: "x"}}},
		"implementer_models": {PlanRunID: run.ID, Source: "final_review", ImplementerModels: []OutcomeImplementerModelArg{{TaskIndex: 0, Model: "m"}}},
	}
	many := make([]OutcomeFindingArg, maxOutcomeFindings+1)
	for i := range many {
		many[i] = OutcomeFindingArg{TaskIndex: 1, Severity: "minor", Category: "x"}
	}
	cases["at most"] = RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review", Findings: many}
	for want, args := range cases {
		res := recordOutcome(t, h, args)
		assert.False(t, res.Recorded, want)
		assert.Contains(t, res.Reason, want)
	}
	_, err := os.Stat(filepath.Join(dir, "outcomes.jsonl"))
	assert.True(t, os.IsNotExist(err), "a rejected outcome must write nothing")
}

func TestRecordReviewOutcome_KnownRunReportsEscapes(t *testing.T) {
	h, rec, dir := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 2)
	for i, post := range []string{"pass", "warn"} {
		sid := "s" + string(rune('1'+i))
		_, ok := h.deps.PlanRuns.Attach(run.ID, sid, planrun.TaskRef{Index: i + 1}, "pass")
		require.True(t, ok)
		row, _ := h.deps.PlanRuns.UpdateRow(run.ID, sid, func(r *planrun.TaskRow) { r.PostVerdict = post })
		h.snapshotRow(run.ID, row)
	}
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{
		PlanRunID: run.ID, Source: "final_review",
		Findings: []OutcomeFindingArg{
			{TaskIndex: 1, Severity: "major", Category: "  Correctness  "},
			{TaskIndex: 2, Severity: "major", Category: "tests"},
		},
	})
	require.True(t, res.Recorded, res.Reason)
	assert.True(t, res.RunKnown)
	assert.Equal(t, 2, res.TasksScored)
	assert.Equal(t, []scorecard.Escape{{TaskIndex: 1, AntiTangentVerdict: "pass", OutcomeSeverity: "major"}}, res.Escapes)
	assert.True(t, strings.HasPrefix(res.SummaryBlock, "record_review_outcome"))
	assert.Contains(t, res.SummaryBlock, "escapes: 1")

	raw, err := os.ReadFile(filepath.Join(dir, "outcomes.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"category":"correctness"`)
	assert.Contains(t, string(raw), rec.RunHash(run.ID))
	assert.NotContains(t, string(raw), run.ID)
}

func TestRecordReviewOutcome_UnknownRunStillRecorded(t *testing.T) {
	h, _, _ := outcomeHandlers(t)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: "pr_000000000000", Source: "review_now",
		Findings: []OutcomeFindingArg{{TaskIndex: 9, Severity: "minor", Category: "docs"}}})
	assert.True(t, res.Recorded)
	assert.False(t, res.RunKnown)
	assert.Equal(t, []scorecard.Escape{}, res.Escapes)
}
```

Add a live-run-without-snapshots case:

```go
func TestRecordReviewOutcome_ZeroTaskKnownRunRejectsTaskIndex(t *testing.T) {
	h, _, _ := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 0)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review",
		Findings: []OutcomeFindingArg{{TaskIndex: 1, Severity: "major", Category: "x"}}})
	assert.False(t, res.Recorded)
	assert.Contains(t, res.Reason, "exceeds")
}

func TestRecordReviewOutcome_LiveRunWithoutSnapshotsIsKnown(t *testing.T) {
	h, _, _ := outcomeHandlers(t)
	run := h.deps.PlanRuns.Create("pass", "rigorous", 1)
	res := recordOutcome(t, h, RecordReviewOutcomeArgs{PlanRunID: run.ID, Source: "final_review", Findings: []OutcomeFindingArg{}})
	assert.True(t, res.Recorded)
	assert.True(t, res.RunKnown)
	assert.Equal(t, 0, res.TasksScored)
}
```

Also add `assert.True(t, catalogHas(t, "record_review_outcome"))` as `TestRecordReviewOutcomeRegisteredInCatalog`, using the `catalogHas` helper in `worker_handlers_test.go`.

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/mcpsrv/...`
Expected: `ok`

- [ ] **Step 6: CHANGELOG bullet** under `### Added`:

```markdown
- `record_review_outcome` tool: records what the final whole-plan review (`final_review`) or a human-adjudicated PR review (`review_now`) found, per task, for a plan run, and returns the tasks anti-tangent passed that the review found a critical or major problem in. Deterministic, no reviewer call; categories and severities only.
```

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/ CHANGELOG.md
git commit -m "feat(mcpsrv): record_review_outcome tool"
```

```json:metadata
{"files": ["internal/mcpsrv/outcome_handler.go", "internal/mcpsrv/outcome_handler_test.go", "internal/mcpsrv/server.go", "internal/mcpsrv/tool_schema_contract_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/mcpsrv/...", "acceptanceCriteria": ["tool listed; required sets findings/plan_run_id/source, findings[] category/severity/task_index, implementer_models[] model/task_index; all described", "Stats nil -> recorded false, reason names ANTI_TANGENT_STATS_DIR, no error", "each invalid input -> recorded false, reason names the field, nothing written, no MCP error", "valid known run -> one outcomes line, normalised categories, hashed run id, run_known, tasks_scored, escapes", "unknown run -> recorded with run_known false and escapes []; live run without snapshots -> run_known true, tasks_scored 0", "summary_block format and one slog exit line with duration_ms, source, recorded, run_known, escapes"], "modelTier": "standard"}
```

---

### Task 7: Protocol part `outcome.md` and routing

**Goal:** Tell controllers when and how to call `record_review_outcome`, in a new protocol part routed from the skill and `INTEGRATION.md`, within every byte budget.

**Files:**
- Create: `docs/protocol/outcome.md`
- Modify: `docs/protocol/core.md`
- Modify: `INTEGRATION.md`
- Modify: `plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md`
- Create: `plugin/anti-tangent-protocol/protocol/outcome.md` (bundle sync)
- Modify: `plugin/anti-tangent-protocol/protocol/core.md` (bundle sync)

**Acceptance Criteria:**
- [ ] `docs/protocol/outcome.md` exists, holds §5.10 exactly as written in Step 1, and is under 16,000 bytes.
- [ ] `core.md` says "ten tools" and names `record_review_outcome`, and stays under 16,000 bytes.
- [ ] `INTEGRATION.md` lists `outcome.md` in its table and stays under 2,000 bytes.
- [ ] The skill's Step 1 tells a controller finishing a plan run to read `../../protocol/outcome.md`.
- [ ] `diff -r docs/protocol plugin/anti-tangent-protocol/protocol` prints nothing.
- [ ] No existing section number changes.

**Verify:** `wc -c docs/protocol/*.md INTEGRATION.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo SYNCED` → every protocol part < 16000, INTEGRATION.md < 2000, `SYNCED`

**Steps:**

- [ ] **Step 1: Write `docs/protocol/outcome.md`**

````markdown
# anti-tangent protocol — recording the review outcome

**Controllers read this part once per plan run, after the last task is done.** It covers one
call, `record_review_outcome`, which lets anti-tangent's verdicts be scored against an
independent review of the same work. Without it, the stats show what anti-tangent said, never
whether it was right.

## 5. For controllers (continued)

### 5.10 Recording the review outcome

**When.** After the final whole-plan review returns, before finishing the branch. Call it even
when the review found nothing: an empty `findings` array is the signal that anti-tangent's passes
held up.

**Call.**

```json
{
  "plan_run_id": "<the id validate_plan returned>",
  "source": "final_review",
  "reviewer_model": "<provider:model of the reviewer, if known>",
  "implementer_models": [{"task_index": 1, "model": "<provider:model task 1 was dispatched on>"}],
  "findings": [{"task_index": 3, "severity": "major", "category": "correctness"}]
}
```

**Attributing a finding to a task.** Use the file it points at: the task whose `**Files:**` list
(or `plan_run_report` row) owns that file is its task. When a finding genuinely spans tasks, or
names no file, use `task_index: 0`. It is then counted for the run, never for a task.

**Severity.** `critical`, `major` or `minor`. Map the reviewer's scale onto these. Drop nits and
style preferences rather than sending them as `minor`.

**Category, never text.** Send one word (`correctness`, `security`, `tests`, `docs`,
`performance`, `maintainability`). Only the first 40 characters are kept; finding descriptions
must not be sent.

**Implementer models.** Pass the model you dispatched each task on. With model routing this
differs per task, and the server cannot see it. Omit a task you do not know.

**PR body line.** When you open the pull request, add one line per plan run to its body:

```text
anti-tangent-plan-run: <plan_run_id>
```

A later human PR review (`review-now`) reads that line to file its own outcome with
`source: "review_now"`. A second call for the same run and source replaces the first.

**What comes back.** `escapes` lists the tasks anti-tangent passed that the review found a
critical or major problem in. Surface them with the final review; they are the cases the
reviewer model missed. `recorded: false` with a `reason` means nothing was stored: fix the named
field and call again. `run_known: false` means the server has no snapshot of this run yet (for
example, stats were enabled mid-run); the outcome is still kept.

The call is deterministic and free: no reviewer model runs. It is advisory like every other
anti-tangent tool, and it blocks nothing.
````

- [ ] **Step 2: `core.md` tool surface**

In `docs/protocol/core.md` line 9, change `It exposes nine tools:` to `It exposes ten tools:`, and change `a deterministic plan-run report (\`plan_run_report\`),` to `a deterministic plan-run report and outcome record (\`plan_run_report\` / \`record_review_outcome\`),`. Run `wc -c docs/protocol/core.md`. It must print a number below 16000. If it does not, shorten the same sentence (e.g. drop "deliberately" from the next sentence) until it does. Do not touch other sections.

- [ ] **Step 3: `INTEGRATION.md`**

Add a table row after the `controller.md` row:

```markdown
| [`outcome.md`](docs/protocol/outcome.md) | controllers, at the end of a run | §5.10 recording the independent review's outcome |
```

and change `The five parts live in` to `The six parts live in`, and `§5 in \`controller.md\`.` to `§5 in \`controller.md\` (§5.10 in \`outcome.md\`).`. Run `wc -c INTEGRATION.md`. It must print a number below 2000.

- [ ] **Step 4: Skill routing**

In `plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md` Step 1, after the Controller bullet add:

```markdown
- **Controller finishing a plan run** (the last task is done and the final review has
  returned): also `../../protocol/outcome.md`
```

- [ ] **Step 5: Sync the bundle**

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
```

- [ ] **Step 6: Verify budgets and sync**

Run: `wc -c docs/protocol/*.md INTEGRATION.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo SYNCED`
Expected: all parts < 16000, INTEGRATION.md < 2000, `SYNCED`.

- [ ] **Step 7: Commit**

```bash
git add docs/protocol/ INTEGRATION.md plugin/anti-tangent-protocol/
git commit -m "docs(protocol): outcome.md — record the review outcome after a plan run"
```

```json:metadata
{"files": ["docs/protocol/outcome.md", "docs/protocol/core.md", "INTEGRATION.md", "plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md", "plugin/anti-tangent-protocol/protocol/outcome.md", "plugin/anti-tangent-protocol/protocol/core.md"], "verifyCommand": "wc -c docs/protocol/*.md INTEGRATION.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo SYNCED", "acceptanceCriteria": ["outcome.md holds §5.10 as written and is < 16000 bytes", "core.md says ten tools, names record_review_outcome, < 16000 bytes", "INTEGRATION.md lists outcome.md, < 2000 bytes", "skill routes a controller finishing a run to outcome.md", "bundle identical to docs/protocol", "no section renumbered"], "modelTier": "mechanical"}
```

---

### Task 8: Server documentation

**Goal:** Bring README, root `CLAUDE.md` and the authoritative design spec up to ten tools and document the new files, tool and env var.

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`

**Acceptance Criteria:**
- [ ] `grep -n "nine" README.md CLAUDE.md docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` finds no remaining reference to the tool count (hits using "nine" for anything else are listed in the task report).
- [ ] README describes the call log as the latest 32 calls per task plus `calls_dropped`, not as every call.
- [ ] README's tool list has a `record_review_outcome` entry beside `plan_run_report`; its tool-catalog check lists ten tools; its stats section documents `runs.jsonl`, `outcomes.jsonl`, `scorecard.json` and `ANTI_TANGENT_SCORECARD_MIN_RUNS`.
- [ ] Root `CLAUDE.md`'s overview names the tenth tool, its architecture block lists `scorecard/` and `outcome_handler.go`, and its logging paragraph lists `record_review_outcome` among the tools that emit one exit line per call.
- [ ] The design spec's tool list includes `record_review_outcome`.

**Verify:** `grep -c record_review_outcome README.md CLAUDE.md docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` → each count ≥ 1

**Steps:**

- [ ] **Step 1: README.** Update the one-line tool count and the catalog-check sentence at `README.md:291` to ten tools including `record_review_outcome`. Add after the `plan_run_report` bullet (near `README.md:503`):

```markdown
- `record_review_outcome` — deterministic, no reviewer call. Call after the final whole-plan review (`source: "final_review"`) and optionally after a human PR review (`source: "review_now"`) with each finding's `task_index`, `severity` and `category`. Returns the tasks anti-tangent passed that the review found a critical or major problem in, and feeds `scorecard.json`. Requires `ANTI_TANGENT_STATS_DIR`.
```

In the stats section, add a subsection:

```markdown
#### Scorecard

With `ANTI_TANGENT_STATS_DIR` set, the server also writes three content-free files:

- `runs.jsonl`: one line per plan-run task change, carrying the task's verdicts, severity counts and its call log: the latest 32 anti-tangent calls made for the task, each with the model that answered it, plus `calls_dropped` counting any older calls the cap evicted; and a header per run with the configured model per role.
- `outcomes.jsonl`: one line per `record_review_outcome` call.
- `scorecard.json`: escape rate (tasks anti-tangent passed that the independent review found a critical or major problem in), unconfirmed-flag rate, waive rate and cost, by review-model cohort and by anti-tangent tool × model, each rate with its n and 90% interval. `regression` stays `insufficient_data` until a cohort and its baseline each have `ANTI_TANGENT_SCORECARD_MIN_RUNS` runs (default 10).

The gnome-topbar daemon renders these at `/ui/runs`.
```

Add `ANTI_TANGENT_SCORECARD_MIN_RUNS` to the environment-variable table with default `10`.

- [ ] **Step 2: Root `CLAUDE.md`.** In the overview, change "exposes nine tools" to ten, adding `record_review_outcome` beside `plan_run_report` in the enumeration ("a deterministic per-task report (`plan_run_report`) and outcome record (`record_review_outcome`)"). Adjust "Six of these send context under review…" so the counts stay true (six reviewer tools, two I/O tools, two deterministic tools). In the architecture block add `scorecard/   public, stdlib-only: scores verdicts against review outcomes (shared with the daemon)` above `internal/`, and `outcome_handler.go         # record_review_outcome` under `mcpsrv/`. In **Logging Conventions**, add `record_review_outcome` to the tools that emit one summary line per call, on exit.

- [ ] **Step 3: Design spec.** In `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`, add `record_review_outcome` to the tool enumeration with a one-sentence description and a pointer to `docs/superpowers/specs/2026-09-23-run-outcome-scorecard-design.md`.

- [ ] **Step 4: Verify.**

Run: `grep -c record_review_outcome README.md CLAUDE.md docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md; grep -n "nine" README.md CLAUDE.md docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`
Expected: each count ≥ 1. Read every line the second grep prints: none may state a tool count. A hit that uses "nine" for something else is fine; name it in the task report.

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md
git commit -m "docs: ten tools, scorecard files and ANTI_TANGENT_SCORECARD_MIN_RUNS"
```

```json:metadata
{"files": ["README.md", "CLAUDE.md", "docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md"], "verifyCommand": "grep -c record_review_outcome README.md CLAUDE.md docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md", "acceptanceCriteria": ["no 'nine tools' left in the three files", "README documents the tool, the three files and ANTI_TANGENT_SCORECARD_MIN_RUNS", "CLAUDE.md overview, architecture block and logging paragraph updated", "design spec lists record_review_outcome"], "modelTier": "mechanical"}
```

---

### Task 9: Daemon — import `scorecard` and read the run files

**Goal:** Give the gnome-topbar daemon a reader for `runs.jsonl` and `outcomes.jsonl` (it recomputes the scorecard itself, so local and pooled views use identical code) plus the server's configured `min_runs` from `scorecard.json`, and local task titles joined from the plan ledger through the stats salt, using the root module's `scorecard` package.

**Files:**
- Modify: `gnome-topbar/daemon/go.mod`
- Modify: `gnome-topbar/daemon/go.sum`
- Create: `gnome-topbar/daemon/internal/atruns/atruns.go`
- Test: `gnome-topbar/daemon/internal/atruns/atruns_test.go`

**Acceptance Criteria:**
- [ ] `gnome-topbar/daemon/go.mod` requires `github.com/patiently/anti-tangent-mcp` with `replace github.com/patiently/anti-tangent-mcp => ../..`, and `go build ./...` in the daemon succeeds.
- [ ] `atruns.Read(dir)` returns `Present=false` and no error when `runs.jsonl` is absent; otherwise the parsed run lines, outcome lines, the count of corrupt lines skipped, titles keyed by run hash then task index, and `MinRuns` read from `scorecard.json`'s `min_runs` (0 when the file is absent or unparseable, which `scorecard.Compute` treats as its default). No other `scorecard.json` field is read: the daemon recomputes every view.
- [ ] Titles come from `plan-runs.jsonl` rows only, joined by `scorecard.HashRunID(salt, plan_run_id)` with the salt from `state.json`, the same function the server's `Recorder.RunHash` calls; the daemon contains no hash construction of its own.
- [ ] `atruns.Runs(lines, outcomes)` returns one `RunSummary` per `(publisher, run_hash)`, newest first by latest line time, with plan verdict, task count, configured models, implementer models, the sources that have reported, and the escape count per source.

**Verify:** `cd gnome-topbar/daemon && go build ./... && go test -race ./internal/atruns/...` → `ok`

**Steps:**

- [ ] **Step 1: Module wiring**

```bash
cd gnome-topbar/daemon
go mod edit -require=github.com/patiently/anti-tangent-mcp@v0.0.0-00010101000000-000000000000
go mod edit -replace=github.com/patiently/anti-tangent-mcp=../..
```

(`go mod tidy` runs in Step 4, after the first import exists.)

- [ ] **Step 2: Write `internal/atruns/atruns.go`**

```go
// Package atruns reads anti-tangent's run snapshots, review outcomes and
// scorecard from the stats directory, and joins in task titles from the local
// plan ledger when it exists. Titles never leave this machine: they are for
// the local run-detail page only and are not part of any shared record.
package atruns

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

type Data struct {
	Present  bool
	Lines    []scorecard.RunLine
	Outcomes []scorecard.OutcomeLine
	Skipped  int
	// Titles maps run hash -> task index -> task title, local runs only.
	Titles map[string]map[int]string
	// MinRuns is the server's configured regression gate, taken from
	// scorecard.json so the daemon's recomputed views gate the same way.
	MinRuns int
}

func Read(dir string) (Data, error) {
	var d Data
	lines, skipR, err := readJSONL[scorecard.RunLine](filepath.Join(dir, "runs.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	outs, skipO, err := readJSONL[scorecard.OutcomeLine](filepath.Join(dir, "outcomes.jsonl"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return d, err
	}
	d.Present, d.Lines, d.Outcomes, d.Skipped = true, lines, outs, skipR+skipO
	d.Titles = readTitles(dir)
	d.MinRuns = readMinRuns(dir)
	return d, nil
}

func readMinRuns(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "scorecard.json"))
	if err != nil {
		return 0
	}
	var sc struct {
		MinRuns int `json:"min_runs"`
	}
	if json.Unmarshal(b, &sc) != nil {
		return 0
	}
	return sc.MinRuns
}

func readTitles(dir string) map[string]map[int]string {
	out := map[string]map[int]string{}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return out
	}
	var st struct {
		Salt string `json:"salt"`
	}
	if json.Unmarshal(b, &st) != nil || st.Salt == "" {
		return out
	}
	type ledgerRow struct {
		PlanRunID string `json:"plan_run_id"`
		Row       struct {
			Index     int    `json:"index"`
			TaskTitle string `json:"task_title"`
		} `json:"row"`
	}
	rows, _, err := readJSONL[ledgerRow](filepath.Join(dir, "plan-runs.jsonl"))
	if err != nil {
		return out
	}
	for _, r := range rows {
		if r.PlanRunID == "" || r.Row.Index == 0 || r.Row.TaskTitle == "" {
			continue
		}
		h := scorecard.HashRunID(st.Salt, r.PlanRunID)
		if out[h] == nil {
			out[h] = map[int]string{}
		}
		out[h][r.Row.Index] = r.Row.TaskTitle
	}
	return out
}

type RunSummary struct {
	Publisher         string
	RunHash           string
	Latest            time.Time
	PlanVerdict       string
	TaskCount         int
	ConfiguredModels  map[string]string
	ImplementerModels []string
	Sources           []string
	Escapes           map[string]int
}

// Runs summarises every run, newest first.
func Runs(lines []scorecard.RunLine, outcomes []scorecard.OutcomeLine) []RunSummary {
	type key struct{ pub, hash string }
	byKey := map[key]*RunSummary{}
	perRun := map[key][]scorecard.RunLine{}
	get := func(k key) *RunSummary {
		s, ok := byKey[k]
		if !ok {
			s = &RunSummary{Publisher: k.pub, RunHash: k.hash, Escapes: map[string]int{}}
			byKey[k] = s
		}
		return s
	}
	for _, l := range lines {
		k := key{l.Publisher, l.RunHash}
		s := get(k)
		perRun[k] = append(perRun[k], l)
		if l.Ts.After(s.Latest) {
			s.Latest = l.Ts
		}
		if l.Header {
			s.PlanVerdict, s.TaskCount, s.ConfiguredModels = l.PlanVerdict, l.TaskCount, l.ConfiguredModels
		}
	}
	latestOutcome := map[key]map[string]scorecard.OutcomeLine{}
	for _, o := range outcomes {
		k := key{o.Publisher, o.RunHash}
		if latestOutcome[k] == nil {
			latestOutcome[k] = map[string]scorecard.OutcomeLine{}
		}
		if prev, ok := latestOutcome[k][o.Source]; !ok || !o.Ts.Before(prev.Ts) {
			latestOutcome[k][o.Source] = o
		}
	}
	for k, bySource := range latestOutcome {
		s := get(k)
		seen := map[string]bool{}
		for _, src := range scorecard.Sources {
			o, ok := bySource[src]
			if !ok {
				continue
			}
			s.Sources = append(s.Sources, src)
			esc, _, _ := scorecard.RunEscapes(perRun[k], o)
			s.Escapes[src] = len(esc)
			for _, m := range o.ImplementerModels {
				if !seen[m.Model] {
					seen[m.Model] = true
					s.ImplementerModels = append(s.ImplementerModels, m.Model)
				}
			}
		}
		sort.Strings(s.ImplementerModels)
	}
	out := make([]RunSummary, 0, len(byKey))
	for _, s := range byKey {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Latest.Equal(out[j].Latest) {
			return out[i].Latest.After(out[j].Latest)
		}
		return out[i].RunHash < out[j].RunHash
	})
	return out
}

func readJSONL[T any](path string) ([]T, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	var out []T
	skipped := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v T
		if json.Unmarshal(line, &v) != nil {
			skipped++
			continue
		}
		out = append(out, v)
	}
	return out, skipped, sc.Err()
}
```

- [ ] **Step 3: Write `internal/atruns/atruns_test.go`**

```go
package atruns

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadAbsent(t *testing.T) {
	d, err := Read(t.TempDir())
	if err != nil || d.Present {
		t.Fatalf("absent dir: %+v %v", d, err)
	}
}

func TestReadJoinsTitlesAndCountsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	h := scorecard.HashRunID("salt1", "pr_abc")
	write(t, dir, "state.json", `{"salt":"salt1"}`)
	write(t, dir, "runs.jsonl", `{"ts":"2026-09-01T00:00:00Z","run_hash":"`+h+`","task":{"index":1,"post_verdict":"pass","checkpoints":0}}`+"\n{broken\n")
	write(t, dir, "outcomes.jsonl", `{"ts":"2026-09-01T01:00:00Z","run_hash":"`+h+`","source":"final_review","findings":[]}`+"\n")
	write(t, dir, "plan-runs.jsonl", `{"plan_run_id":"pr_abc","row":{"index":1,"task_title":"Task 1: Alpha"}}`+"\n")
	write(t, dir, "scorecard.json", `{"min_runs":7}`)
	d, err := Read(dir)
	if err != nil || !d.Present || d.Skipped != 1 || len(d.Lines) != 1 || len(d.Outcomes) != 1 || d.MinRuns != 7 {
		t.Fatalf("read: %+v %v", d, err)
	}
	if d.Titles[h][1] != "Task 1: Alpha" {
		t.Fatalf("titles: %+v", d.Titles)
	}
}

func TestRunsSummaries(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lines := []scorecard.RunLine{
		{Ts: t0, RunHash: "r_old", Header: true, PlanVerdict: "pass", TaskCount: 1},
		{Ts: t0.Add(time.Hour), RunHash: "r_new", Header: true, PlanVerdict: "warn", TaskCount: 2,
			ConfiguredModels: map[string]string{"post": "m"}},
		{Ts: t0.Add(2 * time.Hour), RunHash: "r_new", Task: &scorecard.TaskSnapshot{Index: 1, PostVerdict: "pass"}},
	}
	outs := []scorecard.OutcomeLine{{Ts: t0.Add(3 * time.Hour), RunHash: "r_new", Source: "final_review",
		ImplementerModels: []scorecard.ImplementerModel{{TaskIndex: 1, Model: "sonnet"}},
		Findings:          []scorecard.OutcomeFinding{{TaskIndex: 1, Severity: "major", Category: "x"}}}}
	rs := Runs(lines, outs)
	if len(rs) != 2 || rs[0].RunHash != "r_new" || rs[0].Escapes["final_review"] != 1 ||
		rs[0].ImplementerModels[0] != "sonnet" || rs[0].ConfiguredModels["post"] != "m" || len(rs[1].Sources) != 0 {
		t.Fatalf("runs: %+v", rs)
	}
}
```

The join key comes from `scorecard.HashRunID`, which Task 1 pins with a literal test; the daemon has no hash code of its own.

- [ ] **Step 4: Tidy, build, test**

Run: `cd gnome-topbar/daemon && go mod tidy && go build ./... && go test -race ./internal/atruns/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add gnome-topbar/daemon/go.mod gnome-topbar/daemon/go.sum gnome-topbar/daemon/internal/atruns/
git commit -m "feat(gnome-topbar): read run snapshots, outcomes and local task titles"
```

```json:metadata
{"files": ["gnome-topbar/daemon/go.mod", "gnome-topbar/daemon/go.sum", "gnome-topbar/daemon/internal/atruns/atruns.go", "gnome-topbar/daemon/internal/atruns/atruns_test.go"], "verifyCommand": "cd gnome-topbar/daemon && go build ./... && go test -race ./internal/atruns/...", "acceptanceCriteria": ["daemon go.mod requires the root module with replace ../..; build succeeds", "Read: Present false when runs.jsonl absent; otherwise lines, outcomes, skipped count, titles, and MinRuns from scorecard.json (0 when absent)", "title join uses scorecard.HashRunID, the function the server uses; no daemon-side hash code", "Runs returns one summary per (publisher, run_hash), newest first, with verdict, task count, configured and implementer models, sources, escapes per source"], "modelTier": "standard"}
```

---

### Task 10: Daemon — publish to and read from Basic Memory

**Goal:** When `ANTI_TANGENT_SHARE_STATS=1`, publish every locally scored run as a content-free `at_run` BM note, and always read the team's `at_run` notes to build the pooled record set.

**Files:**
- Create: `gnome-topbar/daemon/internal/bm/runs.go`
- Test: `gnome-topbar/daemon/internal/bm/runs_test.go`
- Create: `gnome-topbar/daemon/internal/atruns/share.go`
- Test: `gnome-topbar/daemon/internal/atruns/share_test.go`
- Modify: `gnome-topbar/daemon/internal/config/config.go`
- Modify: `gnome-topbar/config.example.toml`

**Acceptance Criteria:**
- [ ] `atruns.ShareEnabled(getenv)` is true only for the exact value `1`.
- [ ] `atruns.NoteBody(publisher, lines, outcomes)` returns a markdown body with a single fenced `json` block holding `{"schema":1,"publisher":…,"lines":[…],"outcomes":[…]}`, and the body contains no `task_title`, no `plan_run_id` key, and no title passed to it by any other path. Every outcome category in the body is passed through `scorecard.NormalizeCategory` (on copies; the input is not mutated), so an un-normalised category in `outcomes.jsonl` is never published verbatim.
- [ ] `Publisher.Publish(ctx, data)` writes one BM note per run hash that has at least one outcome, with directory `anti-tangent/runs`, title equal to the run hash, `note_type` `at_run`, and project `share_project` (default: the daemon's `bm_project`); it skips a run whose body hash equals the one in `published.json` and records the new hash after a successful write.
- [ ] `bm.Client.ListRunNotes(ctx)` pages through every `at_run` note in the project; `atruns.ParseNote(body)` returns the lines and outcomes with `Publisher` set from the note's `publisher` on every record, and an error for a note without a `schema: 1` JSON block.
- [ ] `atruns.Pool(ctx, client)` returns every parsed note's records plus the count of notes skipped as unparseable; one bad note never aborts the rest.

**Verify:** `cd gnome-topbar/daemon && go test -race ./internal/bm/... ./internal/atruns/... ./internal/config/...` → `ok`

**Steps:**

- [ ] **Step 1: BM client `internal/bm/runs.go`**

```go
package bm

import "context"

// WriteRunNote creates or overwrites the at_run note titled title in
// directory, in project.
func (c *Client) WriteRunNote(ctx context.Context, project, directory, title, content string) error {
	_, err := c.caller.CallTool(ctx, "write_note", map[string]any{
		"title":     title,
		"directory": directory,
		"content":   content,
		"note_type": "at_run",
		"project":   project,
		// Without overwrite, BM's write_note errors when the note exists
		// (or follows its server-side default), and a changed run could
		// never be republished.
		"overwrite": true,
	})
	return err
}

// ListRunNotes returns every at_run note's permalink in project.
func (c *Client) ListRunNotes(ctx context.Context, project string) ([]SearchResult, error) {
	sub := &Client{caller: c.caller, project: project}
	return sub.listAllByTypes(ctx, []string{"at_run"})
}

// ReadRunNote returns an at_run note's raw markdown.
func (c *Client) ReadRunNote(ctx context.Context, project, permalink string) (string, error) {
	return c.caller.CallTool(ctx, "read_note", map[string]any{"identifier": permalink, "project": project})
}
```

BM's `write_note` takes `title`, `content`, `directory` (required) and `note_type`, `project`, `overwrite` (optional). This was checked against the live server's schema while writing this plan.

`internal/bm/runs_test.go` uses the fake `Caller` the existing `bm` tests use (see `write_test.go`) and asserts the exact tool name and args map for `WriteRunNote`, and that `ListRunNotes` sends `note_types: ["at_run"]` with the given project.

- [ ] **Step 2: `internal/atruns/share.go`**

```go
package atruns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

const (
	noteSchema    = 1
	noteDirectory = "anti-tangent/runs"
)

// ShareEnabled reports whether this daemon publishes its runs. Anything but
// exactly "1" is off, so a stray "true" or "yes" never starts sharing.
func ShareEnabled(getenv func(string) string) bool { return getenv("ANTI_TANGENT_SHARE_STATS") == "1" }

type notePayload struct {
	Schema    int                     `json:"schema"`
	Publisher string                  `json:"publisher"`
	Lines     []scorecard.RunLine     `json:"lines"`
	Outcomes  []scorecard.OutcomeLine `json:"outcomes"`
}

// NoteBody renders one run's records as an at_run note. Only scorecard
// records go in; they are content-free by construction, and titles from
// Data.Titles are never passed here.
func NoteBody(publisher string, lines []scorecard.RunLine, outcomes []scorecard.OutcomeLine) (string, error) {
	// Categories are normalised again here, on copies, because this is the
	// last point before the text leaves the machine: a hand-edited or older
	// outcomes.jsonl line must not publish a finding description.
	clean := make([]scorecard.OutcomeLine, len(outcomes))
	for i, o := range outcomes {
		o.Findings = append([]scorecard.OutcomeFinding(nil), o.Findings...)
		for k := range o.Findings {
			o.Findings[k].Category = scorecard.NormalizeCategory(o.Findings[k].Category)
		}
		clean[i] = o
	}
	b, err := json.MarshalIndent(notePayload{Schema: noteSchema, Publisher: publisher, Lines: lines, Outcomes: clean}, "", "  ")
	if err != nil {
		return "", err
	}
	return "anti-tangent run record (content-free; generated by gnome-topbar).\n\n```json\n" + string(b) + "\n```\n", nil
}

// ParseNote extracts a note's records and stamps every one with the note's
// publisher, so a pooled record cannot claim another publisher.
func ParseNote(body string) ([]scorecard.RunLine, []scorecard.OutcomeLine, error) {
	start := strings.Index(body, "```json\n")
	if start < 0 {
		return nil, nil, errors.New("no json block")
	}
	rest := body[start+len("```json\n"):]
	end := strings.Index(rest, "\n```")
	if end < 0 {
		return nil, nil, errors.New("unterminated json block")
	}
	var p notePayload
	if err := json.Unmarshal([]byte(rest[:end]), &p); err != nil {
		return nil, nil, err
	}
	if p.Schema != noteSchema || p.Publisher == "" {
		return nil, nil, fmt.Errorf("unsupported note: schema %d, publisher %q", p.Schema, p.Publisher)
	}
	for i := range p.Lines {
		p.Lines[i].Publisher = p.Publisher
	}
	for i := range p.Outcomes {
		p.Outcomes[i].Publisher = p.Publisher
	}
	return p.Lines, p.Outcomes, nil
}

type Publisher struct {
	Client    *bm.Client
	Project   string
	Username  string
	StatePath string
}

// Publish writes every run with at least one outcome whose note body changed
// since the last successful publish. It returns how many notes it wrote.
func (p *Publisher) Publish(ctx context.Context, d Data) (int, error) {
	published := loadPublished(p.StatePath)
	lines := map[string][]scorecard.RunLine{}
	for _, l := range d.Lines {
		lines[l.RunHash] = append(lines[l.RunHash], l)
	}
	outs := map[string][]scorecard.OutcomeLine{}
	for _, o := range d.Outcomes {
		outs[o.RunHash] = append(outs[o.RunHash], o)
	}
	wrote := 0
	var firstErr error
	for hash, ocs := range outs {
		body, err := NoteBody(p.Username, lines[hash], ocs)
		if err != nil {
			continue
		}
		sum := sha256.Sum256([]byte(body))
		digest := hex.EncodeToString(sum[:])
		if published[hash] == digest {
			continue
		}
		if err := p.Client.WriteRunNote(ctx, p.Project, noteDirectory, hash, body); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		published[hash] = digest
		wrote++
	}
	if wrote > 0 {
		savePublished(p.StatePath, published)
	}
	return wrote, firstErr
}

// Pool reads every at_run note in the project. A note that fails to read or
// parse is counted and skipped; it never hides the others.
func Pool(ctx context.Context, c *bm.Client, project string) (Data, int, error) {
	notes, err := c.ListRunNotes(ctx, project)
	if err != nil {
		return Data{}, 0, err
	}
	var d Data
	skipped := 0
	for _, n := range notes {
		body, err := c.ReadRunNote(ctx, project, n.Permalink)
		if err != nil {
			skipped++
			continue
		}
		ls, ocs, err := ParseNote(body)
		if err != nil {
			skipped++
			continue
		}
		d.Lines = append(d.Lines, ls...)
		d.Outcomes = append(d.Outcomes, ocs...)
	}
	d.Present = len(d.Lines) > 0 || len(d.Outcomes) > 0
	return d, skipped, nil
}

func loadPublished(path string) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func savePublished(path string, m map[string]string) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}
```

- [ ] **Step 3: `internal/atruns/share_test.go`**

Use a fake `bm.Caller` that records calls and serves `search_notes`/`read_note` from a map. Tests:

```go
func TestShareEnabledOnlyForExactlyOne(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "": false, "0": false, "true": false, " 1": false} {
		if got := ShareEnabled(func(string) string { return v }); got != want {
			t.Fatalf("%q -> %v", v, got)
		}
	}
}

func TestNoteRoundTripStampsPublisher(t *testing.T) {
	body, err := NoteBody("alice", []scorecard.RunLine{{RunHash: "r_1", Publisher: "mallory"}}, []scorecard.OutcomeLine{{RunHash: "r_1", Source: "final_review"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "task_title") || strings.Contains(body, "plan_run_id") {
		t.Fatalf("note leaks content: %s", body)
	}
	ls, ocs, err := ParseNote(body)
	if err != nil || ls[0].Publisher != "alice" || ocs[0].Publisher != "alice" {
		t.Fatalf("parse: %+v %+v %v", ls, ocs, err)
	}
	if _, _, err := ParseNote("no block here"); err == nil {
		t.Fatal("a note without a json block must not parse")
	}
}

func TestNoteBodyNormalisesCategories(t *testing.T) {
	long := "  The Handler Swallows The Error And Returns 200 To The Caller Anyway  "
	in := []scorecard.OutcomeLine{{RunHash: "r_1", Source: "final_review", Findings: []scorecard.OutcomeFinding{{TaskIndex: 1, Severity: "major", Category: long}}}}
	body, err := NoteBody("alice", nil, in)
	if err != nil {
		t.Fatal(err)
	}
	want := scorecard.NormalizeCategory(long)
	if !strings.Contains(body, `"category": "`+want+`"`) || strings.Contains(body, "Anyway") {
		t.Fatalf("category not normalised: %s", body)
	}
	if in[0].Findings[0].Category != long {
		t.Fatal("NoteBody must not mutate its input")
	}
}
```

plus `TestPublishSkipsUnchangedAndRunsWithoutOutcomes`: two runs, one with an outcome; the first `Publish` writes 1 note with directory `anti-tangent/runs` and title `r_1`; a second `Publish` with the same data writes 0; changing the outcome writes 1 again. And `TestPoolSkipsBadNotes`: the fake serves two `at_run` notes, one valid and one garbage; `Pool` returns the valid note's records and `skipped == 1`.

- [ ] **Step 4: Config**

Add to `internal/config/config.go`'s `Config`: `ShareProject string \`toml:"share_project"\``, defaulting to `c.BMProject` after `BMProject` is defaulted. Add a commented example to `gnome-topbar/config.example.toml`:

```toml
# Basic Memory project that holds shared anti-tangent run records (at_run notes).
# Defaults to bm_project. Publishing also needs ANTI_TANGENT_SHARE_STATS=1 in the
# daemon's environment; reading the team's records needs only BM access.
# share_project = "main"
```

Add a config test asserting `ShareProject` defaults to `BMProject` and honours an explicit value.

- [ ] **Step 5: Run the tests**

Run: `cd gnome-topbar/daemon && go test -race ./internal/bm/... ./internal/atruns/... ./internal/config/...`
Expected: `ok`

- [ ] **Step 6: Commit**

```bash
git add gnome-topbar/daemon/internal/bm/runs.go gnome-topbar/daemon/internal/bm/runs_test.go gnome-topbar/daemon/internal/atruns/share.go gnome-topbar/daemon/internal/atruns/share_test.go gnome-topbar/daemon/internal/config/ gnome-topbar/config.example.toml
git commit -m "feat(gnome-topbar): publish and pool content-free run records via Basic Memory"
```

```json:metadata
{"files": ["gnome-topbar/daemon/internal/bm/runs.go", "gnome-topbar/daemon/internal/bm/runs_test.go", "gnome-topbar/daemon/internal/atruns/share.go", "gnome-topbar/daemon/internal/atruns/share_test.go", "gnome-topbar/daemon/internal/config/config.go", "gnome-topbar/config.example.toml"], "verifyCommand": "cd gnome-topbar/daemon && go test -race ./internal/bm/... ./internal/atruns/... ./internal/config/...", "acceptanceCriteria": ["ShareEnabled true only for exactly 1", "NoteBody: one json block with schema 1, publisher, lines, outcomes; no task_title or plan_run_id; categories re-normalised on copies", "Publish writes one at_run note per run with an outcome in anti-tangent/runs, skips unchanged bodies via published.json", "ListRunNotes pages at_run notes; ParseNote stamps publisher on every record and rejects notes without a schema-1 block", "Pool returns all parsed records and counts unparseable notes without aborting"], "modelTier": "standard"}
```

---

### Task 11: Daemon — `/ui/runs` page and poller wiring

**Goal:** Serve `/ui/runs` with a Mine / Team / per-publisher scope selector, the tool × model overview, the runs list and a run detail view, and wire the poller to refresh local and team records and publish when sharing is on.

**Files:**
- Create: `gnome-topbar/daemon/internal/server/runspage.go`
- Test: `gnome-topbar/daemon/internal/server/runspage_test.go`
- Modify: `gnome-topbar/daemon/internal/server/server.go`
- Modify: `gnome-topbar/daemon/internal/server/ui.go`
- Modify: `gnome-topbar/daemon/internal/server/statspage.go`
- Modify: `gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go`
- Test: `gnome-topbar/daemon/cmd/gnome-topbar-daemon/main_test.go`
- Modify: every test fake that implements `server.Provider` (find with `grep -rn "ListMyNotes(ctx context.Context)" gnome-topbar/daemon --include=*_test.go`)
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `GET /ui/runs` (behind `uiAuth`) renders the overview for scope `mine` by default: a table per tool in `scorecard.ToolOrder` with one row per model showing calls, runs, verdict mix, findings/call, p50/p95 ms, partial rate, and per source the escape rate and unconfirmed-flag rate as `value (lo–hi, n=N)`; followed by the `by_review_model` table with each group's `regression` value, and the model-set strip.
- [ ] `?scope=team` renders the same views over the pooled team records, with a "by user" section listing `by_publisher.by_tool_model` rows grouped by publisher; `?scope=user:<name>` filters to one publisher; when no team records exist, the page says so instead of rendering empty tables.
- [ ] In `user:<name>` scope, `RunsView` returns `Data` already filtered to that publisher, so `?scope=user:alice&run=<hash>` never shows another publisher's records under the same hash.
- [ ] `?run=<hash>` (optionally with `&publisher=<name>`) renders the run detail: configured models, the `validate_plan` call, and per task its call log (tool, model, verdict, findings, ms), pre/post verdict, severity counts, waived, attempts, and the outcome findings per source, with escapes marked. Task titles appear only in `mine` scope and only when `atruns.Data.Titles` has them.
- [ ] The runs list shows, per run: publisher (team scope), plan verdict, task count, configured models, implementer models, sources reported, escape count; each row links to its detail view.
- [ ] Every value interpolated into HTML goes through `esc`; a test renders a model name containing `<script>` and asserts it appears escaped.
- [ ] `/ui/stats` links to `/ui/runs`; the `/ui/search` card list includes `🧪 Runs`.
- [ ] The poller refreshes local run data on the anti-tangent interval, publishes when `ANTI_TANGENT_SHARE_STATS=1` and `bm_username` is set, refreshes team data on the BM interval whenever `bm_url` is set (independent of `bm_username`), and a BM failure sets a `runs-team` source error without clearing the local view.

**Verify:** `cd gnome-topbar/daemon && go build ./... && go test -race ./...` → `ok`

**Steps:**

- [ ] **Step 1: Provider surface — `internal/server/server.go`**

Add to `Provider`:

```go
	// RunsView returns the scored view for a scope: "mine", "team", or
	// "user:<publisher>".
	RunsView(scope string) RunsView
```

and define in the same file:

```go
type RunsView struct {
	Scope      string
	Present    bool
	Scorecard  scorecard.Scorecard
	Runs       []atruns.RunSummary
	Data       atruns.Data
	Publishers []string
	TeamError  string
	Skipped    int
}
```

Import `github.com/patiently/anti-tangent-mcp/scorecard` and the `atruns` package. Add a `RunsView(string) RunsView` method returning a zero value to every test fake that implements `Provider`.

- [ ] **Step 2: Register the page — `internal/server/ui.go`**

```go
	mux.HandleFunc("/ui/runs", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		scope := q.Get("scope")
		if scope == "" {
			scope = "mine"
		}
		v := p.RunsView(scope)
		if hash := q.Get("run"); hash != "" {
			writeHTML(w, renderRunDetail(v, hash, q.Get("publisher")))
			return
		}
		writeHTML(w, renderRunsPage(v))
	}))
```

Add `` `<li><a href="/ui/runs">🧪 Runs</a></li>` `` to the `/ui/search` card list after the Stats card. In `renderStatsPage` (statspage.go), append `<p><a href="/ui/runs">Runs and scorecard →</a></p>` after the `<h1>`.

- [ ] **Step 3: Write `internal/server/runspage.go`**

Build on the helpers already in `statspage.go` (`esc`, `heading`, `kvTable`) and `pageShell`. Required functions and their output:

```go
func fmtRate(r scorecard.Rate) string {
	if r.N == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%% (%.0f–%.0f%%, n=%d)", r.Value*100, r.Lo*100, r.Hi*100, r.N)
}

func scopeNav(v RunsView) string // links: Mine (/ui/runs?scope=mine), Team (/ui/runs?scope=team), one per publisher (/ui/runs?scope=user:<url-escaped name>); the current scope is rendered as <strong>, not a link
func overviewTables(rows []scorecard.ToolModelRow) string // one <h3> per tool in scorecard.ToolOrder that has rows; columns: Model, Calls, Runs, Verdicts ("pass 3 · warn 1"), Findings/call (%.2f), p50 ms, p95 ms, Partial (%), then for each source in scorecard.Sources: "<source> escape", "<source> unconfirmed"
func reviewModelTable(groups []scorecard.Group) string // columns: Source, Review model, Runs, Tasks, Escape, Minor escape, Unconfirmed, Waive, Caught & fixed, Regression, Baseline
func modelSetStrip(sets []scorecard.ModelSet) string  // one line per set: "plan=… · pre=… · mid=… · post=… · worker=… — N runs" (roles in that order; empty model shown as "—")
func runsList(v RunsView) string // columns: Publisher (team scope only), Latest, Plan, Tasks, Models (configured, compact), Implementers, Reviewed by (sources), Escapes; the first cell links to ?scope=<scope>&run=<hash>[&publisher=<pub>]
func renderRunsPage(v RunsView) string
func renderRunDetail(v RunsView, hash, publisher string) string
```

`renderRunsPage` layout, in order:
1. `<h1>anti-tangent runs</h1>` and `scopeNav`.
2. If `v.TeamError != ""`, a `<p class="muted">Team records unavailable: …</p>` banner.
3. If `!v.Present`: in `mine` scope `<p class="muted">No run records yet (runs.jsonl absent — set ANTI_TANGENT_STATS_DIR on the server).</p>`; in `team`/`user:` scope `<p class="muted">No shared run records in Basic Memory yet. A developer shares by running the daemon with ANTI_TANGENT_SHARE_STATS=1.</p>`; then return.
4. `<h2>Overview — anti-tangent tool × validator model</h2>` + `overviewTables(v.Scorecard.ByToolModel)` + a `<p class="muted">` note: `Escape columns slice tasks by the model that reviewed them in this tool. A task has one outcome and up to four reviewing models, so a row points at a model; it does not prove the model caused the miss.`
5. In `team` scope with `v.Scorecard.ByPublisher != nil`: `<h2>By user</h2>` and `overviewTables` of `ByPublisher.ByToolModel`, grouped under one `<h3>` per publisher.
6. `<h2>Regression by review model</h2>` + `reviewModelTable(v.Scorecard.ByReviewModel)` with `<p class="muted">regression stays insufficient_data until a model and its baseline each have N runs.</p>`, N = `v.Scorecard.MinRuns`.
7. `<h2>Configured model sets</h2>` + `modelSetStrip`.
8. `<h2>Runs</h2>` + `runsList`.
9. If `v.Skipped > 0`: `<p class="muted">N unreadable records skipped.</p>`.

`renderRunDetail` filters `v.Data.Lines`/`v.Data.Outcomes` to `RunHash == hash` and (when given) `Publisher == publisher`, then renders:
- a `kvTable` of the header's configured models (roles in order `plan`, `pre`, `mid`, `post`, `worker`) plus `Server version` and the plan call (`model · verdict · N findings · ms`);
- per task index ascending (latest snapshot per index): `<h3>Task N</h3>`, preceded by the title only when `v.Scope == "mine"` and `v.Data.Titles[hash][N]` is set; a table of its call log; a `kvTable` of pre/post verdict, severity counts, waived, attempts, implementer model; and, per source with an outcome, the findings for that index as `severity · category`, with a `<strong>escape</strong>` marker when the task's post verdict is `pass` and a finding is `critical`/`major`;
- run-level (`task_index: 0`) findings per source under `<h3>Not attributed to a task</h3>`.
- An unknown hash renders `<p class="muted">No such run in this scope.</p>`.

- [ ] **Step 4: Write `internal/server/runspage_test.go`**

Cover each acceptance criterion with table-driven render tests on hand-built `RunsView` values (no HTTP needed except one handler test):
- default scope renders `Overview — anti-tangent tool × validator model`, a `validate_completion` heading, and a rate formatted `50% (`;
- `!Present` in `team` scope renders the ANTI_TANGENT_SHARE_STATS hint;
- detail view shows the title in `mine` scope and not in `team` scope for the same data;
- detail view marks an escape;
- `Poller.RunsView("user:alice")` over team data where alice and bob both published run hash `r_x` returns `Data.Lines`/`Data.Outcomes` containing only alice's records (put this test in `cmd/gnome-topbar-daemon/main_test.go`, constructing a `Poller` with `runsTeam` set directly);
- a model name `<script>x</script>` appears as `&lt;script&gt;` and never raw;
- `GET /ui/runs?scope=team` through `server.New(fake, token)` with the token cookie returns 200 and calls `RunsView("team")` (fake records the scope).

- [ ] **Step 5: Poller wiring — `cmd/gnome-topbar-daemon/main.go`**

Add fields to `Poller`: `runsLocal atruns.Data`, `runsTeam atruns.Data`, `runsTeamErr string`, `runsTeamSkipped int`, `publisher *atruns.Publisher` (nil when sharing is off or `bm_username` is empty). In `main`, immediately after the `p := &Poller{...}` literal (not after `bmc`: `p` does not exist yet there):

```go
	if atruns.ShareEnabled(os.Getenv) && cfg.BMUsername != "" {
		p.publisher = &atruns.Publisher{Client: bmc, Project: cfg.ShareProject, Username: cfg.BMUsername,
			StatePath: filepath.Join(stateDir, "published.json")}
		log.Info("anti-tangent run sharing enabled", "project", cfg.ShareProject)
	}
```

Add `refreshRuns(ctx)`: reads `atruns.Read(p.cfg.StatsDir)` into `runsLocal` under `p.mu`; if `p.publisher != nil`, calls `Publish` with the local data outside the lock and logs one warning line on error. Add `refreshRunsTeam(ctx)`: calls `atruns.Pool(ctx, p.bm, p.cfg.ShareProject)`; on error sets `runsTeamErr` and `p.snap.Sources["runs-team"] = state.SourceStatus{OK: false, Error: err.Error()}` and keeps the previous `runsTeam`; on success replaces `runsTeam`, clears the error and sets the source OK. Schedule `refreshRuns` alongside `refreshAntiTangent` (same interval). Schedule `refreshRunsTeam` on the BM interval whenever `cfg.BMURL != ""`, **outside** the `cfg.BMUsername != ""` branch: reading the team's records needs BM access only, not a username, so a developer who has not set `bm_username` still sees the Team scope. Publishing is the only part that needs `bm_username` (it is the publisher name).

Implement `func (p *Poller) RunsView(scope string) server.RunsView`:

```go
func (p *Poller) RunsView(scope string) server.RunsView {
	p.mu.RLock()
	local, team, teamErr, teamSkipped := p.runsLocal, p.runsTeam, p.runsTeamErr, p.runsTeamSkipped
	p.mu.RUnlock()
	v := server.RunsView{Scope: scope, TeamError: teamErr}
	opts := scorecard.Options{Now: time.Now().UTC(), MinRuns: local.MinRuns}
	src := local
	if scope != "mine" {
		src = team
		v.Skipped = teamSkipped
		if pub, ok := strings.CutPrefix(scope, "user:"); ok {
			opts.Publisher = pub
		}
	} else {
		v.Skipped = local.Skipped
	}
	v.Present = src.Present
	v.Data = src
	v.Scorecard = scorecard.Compute(src.Lines, src.Outcomes, opts)
	v.Publishers = scorecard.Compute(team.Lines, team.Outcomes, scorecard.Options{}).Publishers
	if opts.Publisher != "" {
		// Filter Data itself, not only the runs list: the detail view reads
		// v.Data, and two publishers can share a run hash.
		v.Data.Lines, v.Data.Outcomes = filterPublisher(src.Lines, src.Outcomes, opts.Publisher)
	}
	v.Runs = atruns.Runs(v.Data.Lines, v.Data.Outcomes)
	if scope != "mine" {
		v.Data.Titles = nil
	}
	return v
}
```

with `filterPublisher` a small helper in `main.go` returning only the records whose `Publisher` equals the argument. The `Titles = nil` line is what guarantees titles never render outside the local scope. Keep it even though `Pool` never fills titles.

- [ ] **Step 6: Build and test the whole daemon**

Run: `cd gnome-topbar/daemon && go build ./... && go test -race ./...`
Expected: `ok`

- [ ] **Step 7: CHANGELOG bullet** under `### Added`:

```markdown
- gnome-topbar: `/ui/runs` page. The overview is grouped by anti-tangent tool × validator model (calls, verdict mix, latency, escape and unconfirmed-flag rates with n), then regression by review model, the configured model sets, and a runs list with per-task drill-down showing every call's model. A Mine / Team / per-user scope reads the team's shared records from Basic Memory; `ANTI_TANGENT_SHARE_STATS=1` on a daemon publishes its own runs there as content-free `at_run` notes (`share_project`, default `bm_project`).
```

- [ ] **Step 8: Commit**

```bash
git add gnome-topbar/ CHANGELOG.md
git commit -m "feat(gnome-topbar): /ui/runs page with team view and run sharing"
```

```json:metadata
{"files": ["gnome-topbar/daemon/internal/server/runspage.go", "gnome-topbar/daemon/internal/server/runspage_test.go", "gnome-topbar/daemon/internal/server/server.go", "gnome-topbar/daemon/internal/server/ui.go", "gnome-topbar/daemon/internal/server/statspage.go", "gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go", "gnome-topbar/daemon/cmd/gnome-topbar-daemon/main_test.go", "CHANGELOG.md"], "verifyCommand": "cd gnome-topbar/daemon && go build ./... && go test -race ./...", "acceptanceCriteria": ["/ui/runs mine scope renders tool x model overview with rates as value (lo-hi, n=N), regression table, model sets", "team scope pools shared records with a by-user section; user:<name> filters; empty team shows the sharing hint", "user:<name> scope returns Data filtered to that publisher; ?run=<hash> renders configured models, plan call, per-task call log, verdicts, outcomes with escapes; titles only in mine scope", "runs list columns and links as specified", "all interpolated values escaped; <script> model name rendered escaped", "/ui/stats links to /ui/runs; search cards include Runs", "poller refreshes local runs, publishes when ANTI_TANGENT_SHARE_STATS=1 and bm_username set, refreshes team on BM interval whenever bm_url is set (not gated on bm_username), BM failure sets runs-team source error and keeps local view"], "modelTier": "standard"}
```

---

## Out of scope for this plan (tracked separately)

- claude-sandbox: `review-now` reads `anti-tangent-plan-run:` lines from the PR body and calls `record_review_outcome` with `source: "review_now"` after the console round closes, and the pinned protocol plugin is bumped. That is a separate repo and change.
- Release mechanics (merge with `[minor]`, then the `gnome-topbar-vX.Y.Z` tag from the same merge commit) follow the gnome-topbar release procedure after review.
