# Revisit signals and session-run titles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The server computes cohort drift and new-model revisit signals into `scorecard.json`, adds a "Cycle review due" lead to the summary, and records session-run titles locally; the gnome-topbar daemon shows unacked signals in the tray with ack-until-clears, and shows session titles on `/ui/runs`.

**Architecture:** A pure `scorecard.Revisit` (public, stdlib-only) reuses the scorecard's `assemble` and cohort functions. `internal/stats` owns `first-seen.json` and wires `Revisit` into `WriteScorecard`, and the compactor adds a deterministic lead. The plan ledger gains a minimal session-title line keyed by the salted run hash. The daemon only reads `scorecard.json`; acks live in its existing `seen.json` under a `revisit:` prefix.

**Tech Stack:** Go 1.25, stdlib only in `scorecard/`; `fyne.io/systray` in the daemon. Tests: `go test -race`.

**Spec:** `docs/superpowers/specs/2026-11-24-revisit-signals-and-session-titles-design.md`, the source of truth. Read the sections each task names.

## Global Constraints

- `scorecard/` stays stdlib-only and pure: no I/O, no clock reads. `Now` comes from options.
- Drift rule: disjoint **90%** Wilson intervals using the existing `wilson` helper (`scorecard/metrics.go:22`). `recent.Lo > baseline.Hi` → `up`, `recent.Hi < baseline.Lo` → `down`. Each side needs ≥ `MinRuns` eligible tasks **and** ≥ 2 distinct runs. `MinRuns ≤ 0` means `DefaultMinRuns`.
- Windows: `RevisitWindow = 14 * 24 * time.Hour`. Recent is `[Now − 14d, Now]`; baseline is everything earlier.
- Cohort models come only from `run.implementerModel(index)` and `task.reviewModel()`. They are never recomputed differently, and `"unknown"` is excluded from first-seen.
- Signal IDs: `<kind>:<cohort.kind>/<impl>/<review>`, plus `/<source>` for `unconfirmed_flags_drift`, or `new_model:<model>`.
- `scorecard.json` keys `revisit` and `revisit_coverage` are both present (possibly `[]`) when the reads succeeded, and both **absent** when a read failed.
- The daemon's `RevisitKnown` is true only when `scorecard.json` parsed **and** the `revisit` key was present. `Retain` runs only when it is true.
- No raw session id on disk. Session title lines carry `session_run_hash`.
- Logging: structured `slog` to stderr. At most one warning per call per degraded surface, never one per item inside a loop.
- Comments explain non-obvious behaviour only. No change history, no task, PR or version references (CLAUDE.md "Comments").
- `go test -race ./...` passes at the repo root **and** in `gnome-topbar/daemon` after every task.
- Each task adds its own bullet under `## [0.32.0] - 2026-11-24` in `CHANGELOG.md`, in the right subsection. Task 1 creates the block. Never bump `VERSION`.
- **anti-tangent gating:** this plan changes anti-tangent's plan-run bookkeeping and stats. Per the project memory, implementers do **not** call `validate_task_spec` / `check_progress` / `validate_completion`; the SDD task reviews and the final whole-branch review are the gate. Keep `go test -race` and CodeScene `pre_commit_code_health_safeguard` in every task.

**User decisions (already made):**
- "Full 0.32.0, server too": one spec and one branch (`version/0.32.0`) for server and daemon.
- Thresholds are statistical, with no tuned bars (disjoint 90% intervals plus a sample gate).
- Ack lasts until the signal clears.
- Show coverage: publish gated comparisons, and have the tray say "too small to judge".
- The daemon only reads; the server computes.
- The summary lead is deterministic.
- Session titles live in `plan-runs.jsonl` under the ledger opt-in, keyed by run hash.
- First-seen models live in `first-seen.json`.

---

### Task 1: `scorecard.Revisit`, `Signal`, `Gated`, `CohortModels`

**Goal:** A pure, public `scorecard.Revisit` that returns drift and new-model signals plus gated comparisons per spec §3.

**Files:**
- Create: `scorecard/revisit.go`
- Create: `scorecard/revisit_test.go`
- Modify: `scorecard/compute.go` (add the two `Scorecard` fields only)
- Modify: `CHANGELOG.md` (create the `## [0.32.0] - 2026-11-24` block)

**Acceptance Criteria:**
- [ ] `Revisit` fires each of `test_value_drift`, `test_volume_drift`, `test_adequacy_share` and `unconfirmed_flags_drift` in both directions on synthetic data, and fires nothing when either side is under `MinRuns` eligible tasks or under 2 distinct runs.
- [ ] Plan and session cohorts are never compared. Pre-0.31 tasks (no `tests_added`, no `criteria`, `server_version` < 0.31.0) fire nothing.
- [ ] `unconfirmed_flags_drift` scores each run once, against the first source in `Sources` order that the run has, and puts the source in `Signal.Source` and the ID.
- [ ] `new_model` fires for a non-zero first-seen within 14 days of `Now`, never for a zero time, and not after 14 days.
- [ ] `Gated` lists a refused comparison with its counts. A comparison with no eligible task on either side is not listed.
- [ ] Two calls on the same input give identical `Signals` and `Gated` (order and IDs). `Signals` and `Gated` are non-nil (possibly empty).
- [ ] `CohortModels` returns the sorted, de-duplicated implementer and review models of post-verdicted tasks, without `"unknown"`.
- [ ] `Scorecard` has `Revisit *[]Signal \`json:"revisit,omitempty"\`` and `RevisitCoverage *[]Gated \`json:"revisit_coverage,omitempty"\``, and `Compute` leaves both nil.

**Verify:** `go test -race ./scorecard/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `scorecard/revisit_test.go`. Reuse `t0`, `completion` and `outcome` from `compute_test.go` (same package). Build data with a local helper:

```go
package scorecard

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

var rvNow = time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)

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

func TestRevisitVolumeDriftUpAndDown(t *testing.T) {
	base := rvNow.Add(-20 * 24 * time.Hour)
	recent := rvNow.Add(-2 * 24 * time.Hour)
	up := append(volumeSide("b", base, 20, 2), volumeSide("r", recent, 20, 15)...)
	res := Revisit(up, nil, nil, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 1 || res.Signals[0].Kind != RevisitTestVolume || res.Signals[0].Direction != "up" {
		t.Fatalf("up: %+v", res.Signals)
	}
	if want := "test_volume_drift:plan/claude-sonnet-5-5/openai:gpt-x"; res.Signals[0].ID != want {
		t.Fatalf("id = %q, want %q", res.Signals[0].ID, want)
	}
	down := append(volumeSide("b", base, 20, 15), volumeSide("r", recent, 20, 2)...)
	res = Revisit(down, nil, nil, RevisitOptions{Now: rvNow})
	if len(res.Signals) != 1 || res.Signals[0].Direction != "down" {
		t.Fatalf("down: %+v", res.Signals)
	}
}
```

  The `want` ID uses `NormalizeModel("openai:gpt-x")`. Check what `NormalizeModel` returns for that input in `scorecard/records.go:178` and use its exact output; `reviewModel()` normalises.

  Add these tests in the same file, using the same construction:
  - `TestRevisitGateRefusesSmallOrSingleRunSides`:
    - 9 tasks per side with `MinRuns: 10` → no signal, and one `Gated{Kind: RevisitTestVolume, RecentN: 9, BaselineN: 9, RecentRuns: 9, BaselineRuns: 9}`;
    - 20 tasks per side, but every baseline task in the **same** run hash (indexes 1..20) → no signal, and `Gated` has `BaselineRuns: 1`.
  - `TestRevisitNeverComparesKinds`: the recent side's runs carry `sessionHeader` lines, so they are session-kind, and the baseline side is plan-kind → no signal. Two `Gated` entries, one per kind, each with one side zero.
  - `TestRevisitIgnoresPre031Lines`:
    - baseline of 20 tasks at `ServerVersion "0.30.0"` with no `TestsAdded`, no `Criteria`, no `Categories`;
    - recent of 20 tasks at `"0.31.0"`, with `TestsAdded=intp(3)`, `Criteria{"test_value":1}` on 15 of them and `Categories{"test_adequacy":1}` on 0 of them.
    - Expect no `test_value_drift` and no `test_adequacy_share` signal.
  - `TestRevisitTestValueAndAdequacyFire`: both sides at `"0.31.0"` with `TestsAdded=intp(2)`; baseline `Criteria{"test_value":1}` on 2/20 and recent on 15/20 → a `test_value_drift` `up` signal. The same pattern with `Categories{"test_adequacy":1}` → `test_adequacy_share` `up`.
  - `TestRevisitUnconfirmedFlagsOneSourcePerRun`:
    - every task `PostVerdict:"warn"`;
    - each run has a `final_review` outcome **and** a `review_now` outcome (helper `outcome(hash, SourceFinalReview, ts)`, with or without an `OutcomeFinding{TaskIndex:1, Severity:"major"}`);
    - baseline: 18/20 runs with a major in `final_review` (so confirmed); recent: 2/20.
    - Expect one `unconfirmed_flags_drift` `up` signal with `Source == SourceFinalReview`, an ID ending in `/final_review`, and `Recent.N == 20` (not 40).
  - `TestRevisitNewModel`, with `firstSeen` holding:
    - `"a": rvNow.Add(-3*24*time.Hour)` → fires, `Since` set, ID `new_model:a`;
    - `"b": time.Time{}` → never fires;
    - `"c": rvNow.Add(-15*24*time.Hour)` → no longer fires.
  - `TestRevisitStableOutput`: call `Revisit` twice on a mixed input firing three signals; `slices.EqualFunc` on IDs and `Gated` kinds and cohorts in order. Also assert `res.Signals != nil && res.Gated != nil` for empty input.
  - `TestCohortModels`: two tasks with impl `"claude-sonnet-5-5"` / `""` and reviewer `"openai:gpt-x"` → `["claude-sonnet-5-5", <normalised reviewer>]`, with no `"unknown"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./scorecard/ -run 'Revisit|CohortModels'`
Expected: FAIL (undefined: `Revisit`, `RevisitOptions`, …)

- [ ] **Step 3: Implement `scorecard/revisit.go`**

```go
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
		// test_adequacy was recalibrated in 0.31.0; older lines would read as a drop.
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
				s[w].n++
				s[w].runs[r.key] = true
				if hit {
					s[w].num++
				}
			}
		}
	}
	res := RevisitResult{Signals: []Signal{}, Gated: []Gated{}}
	for id, s := range sides {
		base, rec := s[0], s[1]
		if base.n < opts.MinRuns || rec.n < opts.MinRuns || len(base.runs) < 2 || len(rec.runs) < 2 {
			res.Gated = append(res.Gated, Gated{Kind: id.kind, Cohort: id.key, RecentN: rec.n, BaselineN: base.n,
				RecentRuns: len(rec.runs), BaselineRuns: len(base.runs)})
			continue
		}
		b, rc := wilson(base.num, base.n), wilson(rec.num, rec.n)
		dir := ""
		switch {
		case rc.Lo > b.Hi:
			dir = "up"
		case rc.Hi < b.Lo:
			dir = "down"
		default:
			continue
		}
		key := id.key
		sig := Signal{Kind: id.kind, Cohort: &key, Direction: dir, Recent: &rc, Baseline: &b, Source: id.source,
			ID: fmt.Sprintf("%s:%s/%s/%s", id.kind, key.Kind, key.ImplementerModel, key.ReviewModel)}
		if id.source != "" {
			sig.ID += "/" + id.source
		}
		res.Signals = append(res.Signals, sig)
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
	x := []string{a.Kind, a.Cohort.Kind, a.Cohort.ImplementerModel, a.Cohort.ReviewModel}
	y := []string{b.Kind, b.Cohort.Kind, b.Cohort.ImplementerModel, b.Cohort.ReviewModel}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

// CohortModels returns the models Revisit's cohorts are keyed by, so the
// caller records first-seen times for exactly those values.
func CohortModels(lines []RunLine, outcomes []OutcomeLine) []string {
	set := map[string]bool{}
	for _, r := range assemble(lines, outcomes, "") {
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
```

  Then add to `Scorecard` in `scorecard/compute.go`, after `ModelSets`:

```go
	// Revisit and RevisitCoverage are set by the server after Compute; both
	// are nil, and their keys absent, when the run or outcome read failed.
	Revisit         *[]Signal `json:"revisit,omitempty"`
	RevisitCoverage *[]Gated  `json:"revisit_coverage,omitempty"`
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./scorecard/...`
Expected: PASS. If a test's synthetic numbers do not separate the 90% intervals, fix the **numbers** in the test, not the rule.

- [ ] **Step 5: CHANGELOG.** Insert above `## [0.31.0] - 2026-11-20`:

```markdown
## [0.32.0] - 2026-11-24

### Added

- Revisit signals: `scorecard.Revisit` compares each (run kind, implementer model, review model) cohort's last 14 days with its own earlier retained tasks and reports `test_value_drift`, `test_volume_drift`, `test_adequacy_share` and `unconfirmed_flags_drift` when the two 90% Wilson intervals are disjoint (each side ≥ `ANTI_TANGENT_SCORECARD_MIN_RUNS` eligible tasks from ≥ 2 runs), plus `new_model` for a model first seen in the last 14 days. Comparisons the sample gate refuses are listed too, so a quiet result can be told apart from "too small to judge".
```

- [ ] **Step 6: Commit**

```bash
git add scorecard/revisit.go scorecard/revisit_test.go scorecard/compute.go CHANGELOG.md
git commit -m "feat(scorecard): revisit signals and gated comparisons"
```

```json:metadata
{"files": ["scorecard/revisit.go", "scorecard/revisit_test.go", "scorecard/compute.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./scorecard/...", "acceptanceCriteria": ["four drift kinds fire up and down; gate (MinRuns tasks, >=2 runs) refuses", "kinds never compared; pre-0.31 lines fire nothing", "unconfirmed scored once per run against first source, source in ID", "new_model within 14d only, zero never", "Gated lists refused comparisons, omits empty ones", "stable, non-nil output", "CohortModels excludes unknown", "Scorecard gains Revisit/RevisitCoverage pointers, nil from Compute"], "modelTier": "standard"}
```

---

### Task 2: `first-seen.json` and `WriteScorecard` wiring

**Goal:** `WriteScorecard` maintains `first-seen.json` (seeded, merged, multi-process-safe) and writes `revisit` / `revisit_coverage` per spec §4.1–4.2.

**Files:**
- Create: `internal/stats/firstseen.go`
- Create: `internal/stats/firstseen_test.go`
- Modify: `internal/stats/runs.go:96-110` (`WriteScorecard`)
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] On a dir with no `first-seen.json`, `WriteScorecard` writes `{"seeded":true,"models":{…zero times…}}` for every `CohortModels` value, and `scorecard.json` has `"revisit": []` with no `new_model` signal.
- [ ] A model that first appears in a later call is stamped with the recorder clock, and that call's `revisit` has `new_model:<model>`.
- [ ] When `runs.jsonl` cannot be read (e.g. it is a directory), `first-seen.json` is not created or changed and `scorecard.json` has neither `revisit` nor `revisit_coverage`.
- [ ] `mergeFirstSeen` keeps zero over any time, else the earlier time, and `seeded` is the OR. A corrupt `first-seen.json` logs one warning and is treated as unseeded.
- [ ] Two goroutines calling `WriteScorecard` 20 times each pass under `-race`.
- [ ] A failed `first-seen.json` write logs one warning and still returns the signals.

**Verify:** `go test -race ./internal/stats/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `internal/stats/firstseen_test.go`. Use `newTestRecorder(t, 1000)` (`recorder_test.go:31`) and set `r.clock` to a fixed func. Write run lines with `r.RecordRunLine(scorecard.RunLine{…})`, using a post-verdicted `TaskSnapshot` with `Calls: []scorecard.ToolCall{{Tool: "validate_completion", Model: "openai:gpt-x", Verdict: "pass"}}` and `ImplementerModel: "claude-sonnet-5-5"`. Then:

```go
func readScorecardJSON(t *testing.T, dir string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, scorecardFile))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWriteScorecardSeedsThenStampsNewModel(t *testing.T) {
	r := newTestRecorder(t, 1000)
	now := time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	r.clock = func() time.Time { return now }
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	sc := r.WriteScorecard()
	if sc.Revisit == nil || len(*sc.Revisit) != 0 {
		t.Fatalf("seeding must not signal: %+v", sc.Revisit)
	}
	fs, err := readFirstSeen(r.dir)
	if err != nil || !fs.Seeded || !fs.Models["claude-sonnet-5-5"].IsZero() {
		t.Fatalf("seeded state: %+v %v", fs, err)
	}
	recordTask(t, r, "r2", now.Add(-time.Minute), "claude-haiku-5-5")
	sc = r.WriteScorecard()
	ids := []string{}
	for _, s := range *sc.Revisit {
		ids = append(ids, s.ID)
	}
	if !slices.Contains(ids, "new_model:claude-haiku-5-5") {
		t.Fatalf("want new_model for haiku, got %v", ids)
	}
	if m := readScorecardJSON(t, r.dir); m["revisit"] == nil || m["revisit_coverage"] == nil {
		t.Fatalf("keys missing: %v", m)
	}
}
```

  `recordTask(t, r, hash, ts, impl)` is a small helper in the test file that calls `r.RecordRunLine`.

  Also add:
  - `TestWriteScorecardReadFailureSkipsRevisit`: `os.Mkdir(filepath.Join(r.dir, runsFile), 0o700)` so the read fails, then `WriteScorecard` → `first-seen.json` does not exist and the `scorecard.json` map has no `revisit` / `revisit_coverage` key.
  - `TestMergeFirstSeen`: a table over (zero vs time → zero), (t1 vs t2 → min), (missing vs t → t), and `seeded` OR.
  - `TestFirstSeenCorruptIsUnseeded`: write `{` to `first-seen.json` → `WriteScorecard` re-seeds (zero times), no `new_model`.
  - `TestWriteScorecardConcurrent`: two goroutines × 20 calls, `sync.WaitGroup`; run under `-race`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/stats/ -run 'FirstSeen|WriteScorecard'`
Expected: FAIL (undefined `readFirstSeen`, …)

- [ ] **Step 3: Implement `internal/stats/firstseen.go`**

```go
package stats

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const firstSeenFile = "first-seen.json"

// firstSeen records when each cohort model was first seen. It has its own
// file because every server process rewrites state.json whole from memory on
// each event, which would clobber a field another process just added.
type firstSeen struct {
	Seeded bool                 `json:"seeded"`
	Models map[string]time.Time `json:"models"`
}

// readFirstSeen returns a missing file as unseeded with no error, and a
// corrupt one as unseeded with the parse error.
func readFirstSeen(dir string) (firstSeen, error) {
	fs := firstSeen{Models: map[string]time.Time{}}
	b, err := os.ReadFile(filepath.Join(dir, firstSeenFile))
	if errors.Is(err, os.ErrNotExist) {
		return fs, nil
	}
	if err != nil {
		return fs, err
	}
	if err := json.Unmarshal(b, &fs); err != nil {
		return firstSeen{Models: map[string]time.Time{}}, err
	}
	if fs.Models == nil {
		fs.Models = map[string]time.Time{}
	}
	return fs, nil
}

// mergeFirstSeen combines two views so concurrent writers converge: a zero
// time (seeded, never new) beats any stamp, otherwise the earlier stamp wins.
func mergeFirstSeen(a, b firstSeen) firstSeen {
	out := firstSeen{Seeded: a.Seeded || b.Seeded, Models: map[string]time.Time{}}
	for _, src := range []map[string]time.Time{a.Models, b.Models} {
		for m, t := range src {
			prev, ok := out.Models[m]
			switch {
			case !ok:
				out.Models[m] = t
			case prev.IsZero() || t.IsZero():
				out.Models[m] = time.Time{}
			case t.Before(prev):
				out.Models[m] = t
			}
		}
	}
	return out
}

// updateFirstSeen seeds or stamps models and persists the result, returning a
// copy the caller may use without the lock.
func (r *Recorder) updateFirstSeen(models []string) map[string]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, err := readFirstSeen(r.dir)
	if err != nil {
		r.logger.Warn("stats first_seen unreadable, reseeding", "err", err)
	}
	next := mergeFirstSeen(cur, firstSeen{})
	changed := false
	for _, m := range models {
		if _, ok := next.Models[m]; ok {
			continue
		}
		if next.Seeded {
			next.Models[m] = r.clock()
		} else {
			next.Models[m] = time.Time{}
		}
		changed = true
	}
	if !next.Seeded {
		next.Seeded, changed = true, true
	}
	if changed {
		if disk, err := readFirstSeen(r.dir); err == nil {
			next = mergeFirstSeen(next, disk)
		}
		b, err := json.Marshal(next)
		if err == nil {
			err = writeFileAtomic(filepath.Join(r.dir, firstSeenFile), b, 0o644)
		}
		if err != nil {
			r.logger.Warn("stats first_seen write failed", "err", err)
		}
	}
	out := make(map[string]time.Time, len(next.Models))
	for m, t := range next.Models {
		out[m] = t
	}
	return out
}
```

  Then, in `WriteScorecard` (`internal/stats/runs.go`), after `sc.SkippedLines = skipR + skipO`:

```go
	if errR == nil && errO == nil {
		fs := r.updateFirstSeen(scorecard.CohortModels(runs, outs))
		res := scorecard.Revisit(runs, outs, fs, scorecard.RevisitOptions{MinRuns: r.minRuns, Now: r.clock()})
		sc.Revisit, sc.RevisitCoverage = &res.Signals, &res.Gated
	}
```

  Confirm that `readJSONLCounted` returns a non-nil error when `runs.jsonl` is a directory. If it treats any error as "missing", make the test create the failure another way that `errR` reports (e.g. `chmod 000` on the file, skipped when running as root).

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/stats/...`
Expected: PASS

- [ ] **Step 5: CHANGELOG.** Under `### Added` in `[0.32.0]`:

```markdown
- `scorecard.json` gains `revisit` (signals) and `revisit_coverage` (comparisons too small to judge); both keys are omitted when the scorecard's run or outcome read failed. First-seen models are kept in a new `first-seen.json` in the stats directory; the first 0.32 run records every model already in use without flagging it as new.
```

- [ ] **Step 6: Commit**

```bash
git add internal/stats/firstseen.go internal/stats/firstseen_test.go internal/stats/runs.go CHANGELOG.md
git commit -m "feat(stats): first-seen models and revisit signals in scorecard.json"
```

```json:metadata
{"files": ["internal/stats/firstseen.go", "internal/stats/firstseen_test.go", "internal/stats/runs.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/stats/...", "acceptanceCriteria": ["first call seeds zero times, no new_model, revisit [] present", "later new model stamped and signalled", "read failure: no first-seen write, no revisit keys", "merge: zero wins else earliest; seeded OR; corrupt => unseeded + one warning", "concurrent WriteScorecard race-clean", "write failure: one warning, signals still returned"], "modelTier": "frontier", "tierReason": "Concurrency and multi-process file state: two server processes share the stats dir, and WriteScorecard runs concurrently from the outcome refresh and compaction. A wrong merge or an unlocked map reaches every operator as false new_model signals or lost first-seen history, and races only show under -race."}
```

---

### Task 3: "Cycle review due" summary lead

**Goal:** The compactor prepends a deterministic plain-text lead to `summary.md` when signals exist, keeps it out of `summaries.jsonl` and the previous-summary prompt input, and adds a revisit block to the prompt (spec §4.3).

**Files:**
- Create: `internal/stats/revisitlead.go`
- Modify: `internal/stats/compactor.go:54-100` (`Compact`, `readPrevSummary`), `:103-116` (`buildSummaryPrompt`)
- Test: `internal/stats/compactor_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `revisitLead` renders at most 3 signals then `; +N more`. Drift reads `<kind> up|down (<cohort.kind> · <impl> / <review>)`, with ` · <source>` inside the parentheses for unconfirmed. New model reads `new model <model>`. An empty input gives `""`.
- [ ] With signals, `summary.md` starts with `Cycle review due: ` and the LLM text follows after a blank line. The `summaries.jsonl` record's `text` is the LLM text alone.
- [ ] Without signals (or `sc == nil`, or `sc.Revisit == nil`), `summary.md` is exactly the LLM text.
- [ ] `readPrevSummary` strips a leading `Cycle review due:` line and the blank line after it.
- [ ] `buildSummaryPrompt` includes a revisit block when there are signals, even if `ByReviewModel` is empty, and none otherwise.

**Verify:** `go test -race ./internal/stats/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `compactor_test.go`. Follow `TestCompactWritesRollupAndSummary` (`compactor_test.go:36`): a `fakeReviewer` returning `{"summary":"All green."}`, and `sc := &scorecard.Scorecard{Revisit: &sigs}` with two signals. Assert:
  - `summary.md` == `"Cycle review due: 2 signals — test_volume_drift up (session · claude-sonnet-5-5 / gpt-x); new model claude-haiku-5-5\n\nAll green."`;
  - the last `summaries.jsonl` record's `Text == "All green."`.

  Also:
  - `TestRevisitLeadCapsAtThree`: 5 signals → ends with `; +2 more`.
  - `TestReadPrevSummaryStripsLead`: write that `summary.md` → `readPrevSummary() == "All green."`.
  - `TestSummaryPromptRevisitBlock`: with an empty `ByReviewModel` and one signal, `buildSummaryPrompt` contains `"Revisit signals"`; with no signals it does not.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/stats/ -run 'Lead|PrevSummary|RevisitBlock|Compact'`
Expected: FAIL

- [ ] **Step 3: Implement**

  `internal/stats/revisitlead.go`:

```go
package stats

import (
	"fmt"
	"strings"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

const revisitLeadPrefix = "Cycle review due:"

// revisitLead is the plain-text first line of summary.md when signals exist.
// Plain text because the daemon renders the summary escaped, and capped at
// three signals because the daemon cuts the summary at 600 characters.
func revisitLead(sigs []scorecard.Signal) string {
	if len(sigs) == 0 {
		return ""
	}
	noun := "signals"
	if len(sigs) == 1 {
		noun = "signal"
	}
	items := make([]string, 0, 4)
	for i, s := range sigs {
		if i == 3 {
			items = append(items, fmt.Sprintf("+%d more", len(sigs)-3))
			break
		}
		items = append(items, revisitItem(s))
	}
	return fmt.Sprintf("%s %d %s — %s", revisitLeadPrefix, len(sigs), noun, strings.Join(items, "; "))
}

func revisitItem(s scorecard.Signal) string {
	if s.Kind == scorecard.RevisitNewModel || s.Cohort == nil {
		return "new model " + s.Model
	}
	c := fmt.Sprintf("%s · %s / %s", s.Cohort.Kind, s.Cohort.ImplementerModel, s.Cohort.ReviewModel)
	if s.Source != "" {
		c += " · " + s.Source
	}
	return fmt.Sprintf("%s %s (%s)", s.Kind, s.Direction, c)
}

// stripRevisitLead removes a lead line written by revisitLead, so the
// previous summary fed back to the model never carries a stale lead.
func stripRevisitLead(s string) string {
	if !strings.HasPrefix(s, revisitLeadPrefix) {
		return s
	}
	_, rest, _ := strings.Cut(s, "\n")
	return strings.TrimLeft(rest, "\n")
}

func scorecardSignals(sc *scorecard.Scorecard) []scorecard.Signal {
	if sc == nil || sc.Revisit == nil {
		return nil
	}
	return *sc.Revisit
}
```

  In `Compact`, replace the `summary.md` write so the file gets `md` and `summaries.jsonl` keeps `sr.Summary`:

```go
	md := sr.Summary
	if lead := revisitLead(scorecardSignals(sc)); lead != "" {
		md = lead + "\n\n" + sr.Summary
	}
	if err := writeFileAtomic(filepath.Join(c.dir, summaryMDFile), []byte(md), 0o644); err != nil {
```

  In `readPrevSummary`: `return stripRevisitLead(string(b))`.

  In `buildSummaryPrompt`, after the scorecard block:

```go
	if sigs := scorecardSignals(sc); len(sigs) > 0 {
		b, _ := json.MarshalIndent(sigs, "", "  ")
		fmt.Fprintf(&sb, "\nRevisit signals (JSON). Each compares one cohort's last 14 days with its own earlier tasks; the operator already sees a \"Cycle review due\" line, so do not restate it, and do not read a signal as a verdict on quality:\n%s\n", string(b))
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/stats/...`
Expected: PASS (`TestSummaryPromptCarriesScorecard` at `runs_test.go:135` still passes)

- [ ] **Step 5: CHANGELOG.** Under `### Added`:

```markdown
- `summary.md` opens with a `Cycle review due:` line naming up to three revisit signals when any are live. The line is written by the server, not the model, and is kept out of `summaries.jsonl`. With no reviewer configured, or when the summary call fails, no summary is written and the signals are in `scorecard.json` and the gnome-topbar tray only.
```

- [ ] **Step 6: Commit**

```bash
git add internal/stats/revisitlead.go internal/stats/compactor.go internal/stats/compactor_test.go CHANGELOG.md
git commit -m "feat(stats): deterministic cycle-review lead in the summary"
```

```json:metadata
{"files": ["internal/stats/revisitlead.go", "internal/stats/compactor.go", "internal/stats/compactor_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/stats/...", "acceptanceCriteria": ["revisitLead format and 3-item cap", "summary.md carries lead, summaries.jsonl text does not", "no signals => summary.md is LLM text only", "readPrevSummary strips the lead", "prompt revisit block on its own condition"], "modelTier": "mechanical"}
```

---

### Task 4: Session-run title ledger lines

**Goal:** With the plan ledger enabled, a session run writes one minimal `plan-runs.jsonl` line `{plan_run_id:"", session_run_hash, row:{index:1, task_title}, written_at}` that no plan-run reader matches (spec §6.1).

**Files:**
- Modify: `internal/planrun/ledger.go` (`ledgerLine` field, new `AppendSessionTitle`)
- Modify: `internal/session/store.go:~91` (`UpdateRow` sets `TaskTitle`)
- Modify: `internal/mcpsrv/run_snapshots.go:91-121` (`recordSessionRow`)
- Test: `internal/planrun/ledger_test.go`, `internal/mcpsrv/run_snapshots_test.go` (or the existing file that tests `recordSessionRow`; find it with `grep -rln recordSessionRow internal/mcpsrv/*_test.go`)
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `AppendSessionTitle(hash, title, at)` writes exactly `{"plan_run_id":"","session_run_hash":"<hash>","row":{"index":1,"task_title":"<title>"},"written_at":"<RFC3339>"}` (key order as marshalled). It is a no-op for a nil ledger, an empty `Dir`, an empty hash or an empty title.
- [ ] `Load(id)` for any non-empty id never returns a run built from that line. `Prune` drops it when `written_at` is before the cutoff and keeps it otherwise.
- [ ] A session's row carries `TaskTitle == Spec.Title`, and `runs.jsonl` still carries no title (`taskSnapshot` unchanged).
- [ ] `recordSessionRow` appends the title line once per session run, on the header path, only after the header append succeeds. A failure logs `slog.Warn("plan ledger session title append failed", "err", err)`.
- [ ] With the ledger disabled (`PlanLedger == nil`) nothing is written.

**Verify:** `go test -race ./internal/planrun/... ./internal/session/... ./internal/mcpsrv/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests**
  - In `ledger_test.go`:
    - `TestAppendSessionTitleShapeAndInvisibility`: append one, read the file, assert the exact JSON line (unmarshal into `map[string]any` and check `plan_run_id == ""`, `session_run_hash`, `row.index == 1`, `row.task_title`, and that `written_at` is present at the **top level**). `l.Load("r_any")` returns `ok == false`.
    - `TestPruneDropsSessionTitleByWrittenAt`: two lines at `t-40d` and `t-1d`, then `Prune(t-30d)` keeps only the second.
  - In the mcpsrv test: drive a session through `validate_task_spec` + `validate_completion` with a temp stats dir and `PlanLedger: &planrun.Ledger{Dir: dir}`, the same way the existing session-run test does (find it with `grep -rn "KindSession" internal/mcpsrv/*_test.go`). Assert that `plan-runs.jsonl` has one line with `session_run_hash == deps.Stats.SessionRunHash(sessionID)` and `task_title` == the spec title, and that a second `validate_completion` adds no second title line.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/planrun/ ./internal/mcpsrv/ -run 'SessionTitle'`
Expected: FAIL

- [ ] **Step 3: Implement**
  - `ledgerLine` gains, after `HeaderPlanRunID`:

```go
	// SessionRunHash marks a session run's title line. Its plan_run_id is
	// empty, so no reader matching task rows on plan_run_id ever sees it.
	SessionRunHash string `json:"session_run_hash,omitempty"`
```

  - The new method:

```go
type sessionTitleLine struct {
	PlanRunID      string          `json:"plan_run_id"`
	SessionRunHash string          `json:"session_run_hash"`
	Row            sessionTitleRow `json:"row"`
	WrittenAt      time.Time       `json:"written_at"`
}

type sessionTitleRow struct {
	Index     int    `json:"index"`
	TaskTitle string `json:"task_title"`
}

// AppendSessionTitle records a session run's title under its salted run hash,
// so the daemon can name the run without the raw session id reaching disk.
func (l *Ledger) AppendSessionTitle(runHash, title string, at time.Time) error {
	if l == nil || l.Dir == "" || runHash == "" || title == "" {
		return nil
	}
	b, err := json.Marshal(sessionTitleLine{SessionRunHash: runHash,
		Row: sessionTitleRow{Index: 1, TaskTitle: title}, WrittenAt: at.UTC()})
	if err != nil {
		return err
	}
	return l.appendLine(b)
}
```

  - In `session.Store.UpdateRow`, before `sess.Row.Index = 1`:

```go
	if sess.Row.TaskTitle == "" {
		sess.Row.TaskTitle = sess.Spec.Title
	}
```

  - In `recordSessionRow`, inside `if firstWrite {` after the header `RecordRunLine` succeeds (after the `if err != nil {…return}` block):

```go
		if err := h.deps.PlanLedger.AppendSessionTitle(hash, row.TaskTitle, time.Now()); err != nil {
			slog.Warn("plan ledger session title append failed", "err", err)
		}
```

  - Confirm that `taskSnapshot(row)` does not copy `TaskTitle` (`grep -n "func taskSnapshot" -A30 internal/mcpsrv/*.go`). If it does, that is a privacy bug: stop and report it instead of changing it.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/planrun/... ./internal/session/... ./internal/mcpsrv/...`
Expected: PASS

- [ ] **Step 5: CHANGELOG.** Under `### Added`:

```markdown
- With `ANTI_TANGENT_PLAN_LEDGER=1`, a task run without a plan writes one line to `plan-runs.jsonl` carrying its salted run hash and task title (never the session id), so gnome-topbar can name session runs. Without the ledger opt-in, session runs keep showing by hash.
```

- [ ] **Step 6: Commit**

```bash
git add internal/planrun/ledger.go internal/planrun/ledger_test.go internal/session/store.go internal/mcpsrv/run_snapshots.go internal/mcpsrv/*_test.go CHANGELOG.md
git commit -m "feat(planrun): record session-run titles under the run hash"
```

```json:metadata
{"files": ["internal/planrun/ledger.go", "internal/planrun/ledger_test.go", "internal/session/store.go", "internal/mcpsrv/run_snapshots.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/planrun/... ./internal/session/... ./internal/mcpsrv/...", "acceptanceCriteria": ["exact minimal line shape, written_at top-level; no-op guards", "Load never matches; Prune by written_at", "row TaskTitle = Spec.Title; runs.jsonl carries no title", "once per session run after header success; warn on failure", "nothing written when ledger disabled"], "modelTier": "standard"}
```

---

### Task 5: Daemon reads revisit state and session titles

**Goal:** `atruns.Read` decodes `revisit` / `revisit_coverage` with a presence flag and resolves session titles. `/ui/runs` shows a session run's title in its list row (spec §5.1, §6.2).

**Files:**
- Modify: `gnome-topbar/daemon/internal/atruns/atruns.go` (`Data`, `readMinRuns` → `readScorecard`, `readTitles`, `RunSummary.Kind`)
- Modify: `gnome-topbar/daemon/internal/server/runspage.go:~346` (the Plan cell in `runsList`)
- Test: `gnome-topbar/daemon/internal/atruns/atruns_test.go`, `gnome-topbar/daemon/internal/server/runspage_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] A `scorecard.json` with `"revisit":[…]` and `"revisit_coverage":[…]` → `Data.Revisit` and `Data.RevisitGated` decoded, and `RevisitKnown == true`. With `"revisit":[]`, known is true and the slice is empty.
- [ ] A missing file, an unparsable file, or a file without the `revisit` key → `RevisitKnown == false`. `MinRuns` behaves as before.
- [ ] A ledger line with `session_run_hash` and a title → `Titles[hash][1] == title`. Plan-row titles still resolve as before.
- [ ] `RunSummary.Kind` comes from the run header (`""` when none).
- [ ] In `mine` scope, a `kind: session` run with a title shows the title, HTML-escaped and cut to 80 runes with `…`, in the Plan cell instead of the plan verdict. Other scopes and plan runs are unchanged.

**Verify:** `cd gnome-topbar/daemon && go test -race ./...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `atruns_test.go`. Write fixture files to `t.TempDir()`: `runs.jsonl` (one line is enough for `Present`), `state.json` (`{"salt":"s"}`), `scorecard.json` and `plan-runs.jsonl`. Then:
  - `TestReadRevisitKnownOnlyWithKey`: a table of `{"min_runs":10}` → known false; `{"min_runs":10,"revisit":[]}` → known true, len 0; `{"revisit":[{"id":"new_model:x","kind":"new_model","model":"x"}],"revisit_coverage":[{"kind":"test_volume_drift","cohort":{"kind":"session","review_model":"r"},"recent_n":3,"baseline_n":0,"recent_runs":3,"baseline_runs":0}]}` → known true, one each; `{` → known false; no file → known false.
  - `TestReadTitlesSessionRunHash`: a `plan-runs.jsonl` line `{"plan_run_id":"","session_run_hash":"r_abc","row":{"index":1,"task_title":"Fix X"},"written_at":"2026-11-24T00:00:00Z"}` → `d.Titles["r_abc"][1] == "Fix X"`.

  In `runspage_test.go`, `TestRunsListShowsSessionTitle`: a `RunsView{Scope:"mine", Runs: []atruns.RunSummary{{RunHash:"r_abc", Kind:"session", TaskCount:1}}, Data: atruns.Data{Titles: map[string]map[int]string{"r_abc":{1:"Fix <X>"}}}}` → output contains `Fix &lt;X&gt;`. The same view with `Scope:"team"` does not contain `Fix`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd gnome-topbar/daemon && go test ./internal/atruns/ ./internal/server/ -run 'Revisit|SessionRunHash|SessionTitle'`
Expected: FAIL

- [ ] **Step 3: Implement**
  - `Data` gains:

```go
	// Revisit and RevisitGated are the server's revisit signals and the
	// comparisons too small to judge. RevisitKnown is true only when
	// scorecard.json parsed and carried the revisit key: an older server, or
	// one whose own read failed, omits it, and that must not read as "no
	// signals" or the tray would forget every ack.
	Revisit      []scorecard.Signal
	RevisitGated []scorecard.Gated
	RevisitKnown bool
```

  - Replace `readMinRuns` and its call with `readScorecard`:

```go
func readScorecard(dir string) (minRuns int, rev []scorecard.Signal, gated []scorecard.Gated, known bool) {
	b, err := os.ReadFile(filepath.Join(dir, "scorecard.json"))
	if err != nil {
		return 0, nil, nil, false
	}
	var sc struct {
		MinRuns int                  `json:"min_runs"`
		Revisit *[]scorecard.Signal  `json:"revisit"`
		Gated   *[]scorecard.Gated   `json:"revisit_coverage"`
	}
	if json.Unmarshal(b, &sc) != nil {
		return 0, nil, nil, false
	}
	if sc.Revisit == nil {
		return sc.MinRuns, nil, nil, false
	}
	if sc.Gated != nil {
		gated = *sc.Gated
	}
	return sc.MinRuns, *sc.Revisit, gated, true
}
```

    with, in `Read`: `d.MinRuns, d.Revisit, d.RevisitGated, d.RevisitKnown = readScorecard(dir)`.
  - In `readTitles`, add `SessionRunHash string \`json:"session_run_hash"\`` to `ledgerRow`. At the top of the loop:

```go
		if r.SessionRunHash != "" {
			if r.Row.TaskTitle != "" {
				if out[r.SessionRunHash] == nil {
					out[r.SessionRunHash] = map[int]string{}
				}
				out[r.SessionRunHash][1] = r.Row.TaskTitle
			}
			continue
		}
```

    Session lines need no salt, so move the `state.json` salt read so that a missing salt skips only plan-row lines: compute `salt` before the loop and `continue` plan rows when it is empty.
  - `RunSummary` gains `Kind string`, set in `Runs` alongside the other header fields: `s.Kind = l.Kind`.
  - In `runsList`, replace the line ``b.WriteString(`<td>` + esc(orDash(r.PlanVerdict)) + `</td>`)`` with:

```go
		plan := orDash(r.PlanVerdict)
		if t := sessionTitle(v, r); t != "" {
			plan = t
		}
		b.WriteString(`<td>` + esc(plan) + `</td>`)
```

    and add this function to `runspage.go`:

```go
// sessionTitle is a session run's local title for the runs list, cut to 80
// runes; a one-task run is found by its title, not its hash.
func sessionTitle(v RunsView, r atruns.RunSummary) string {
	if v.Scope != "mine" || r.Kind != "session" {
		return ""
	}
	t := []rune(v.Data.Titles[r.RunHash][1])
	if len(t) > 80 {
		return string(t[:79]) + "…"
	}
	return string(t)
}
```

- [ ] **Step 4: Run the tests**

Run: `cd gnome-topbar/daemon && go test -race ./...`
Expected: PASS

- [ ] **Step 5: CHANGELOG.** Under `### Added`:

```markdown
- gnome-topbar: `/ui/runs` names session runs by their task title (local `mine` scope, with the plan ledger on) in the runs list and on the run page.
```

- [ ] **Step 6: Commit**

```bash
git add gnome-topbar/daemon/internal/atruns/atruns.go gnome-topbar/daemon/internal/atruns/atruns_test.go gnome-topbar/daemon/internal/server/runspage.go gnome-topbar/daemon/internal/server/runspage_test.go CHANGELOG.md
git commit -m "feat(gnome-topbar): read revisit signals; name session runs"
```

```json:metadata
{"files": ["gnome-topbar/daemon/internal/atruns/atruns.go", "gnome-topbar/daemon/internal/atruns/atruns_test.go", "gnome-topbar/daemon/internal/server/runspage.go", "gnome-topbar/daemon/internal/server/runspage_test.go", "CHANGELOG.md"], "verifyCommand": "cd gnome-topbar/daemon && go test -race ./...", "acceptanceCriteria": ["revisit/revisit_coverage decoded, RevisitKnown true only with key present", "missing/unparsable/no key => known false, MinRuns unchanged", "session_run_hash title resolves at index 1", "RunSummary.Kind from header", "mine-scope session title in Plan cell, escaped, 80-rune cap"], "modelTier": "standard"}
```

---

### Task 6: Ack state, snapshot wiring and the tray line

**Goal:** Unacked signals and the gated count reach the tray. A click acks a signal until it clears, and acks survive unknown reads (spec §5.2–5.3).

**Files:**
- Modify: `gnome-topbar/daemon/internal/state/store.go` (add `Retain`)
- Modify: `gnome-topbar/daemon/internal/state/state.go` (`Snapshot` fields, `ComputeRevisit`)
- Create: `gnome-topbar/daemon/internal/tray/revisit.go` (`revisitLabel`, `revisitTitle`)
- Modify: `gnome-topbar/daemon/internal/tray/tray.go` (`Actions.AckRevisit`, `Actions.OpenRuns`, pool, render, tooltip)
- Modify: `gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go` (`refreshRuns`, `recompute`, actions)
- Test: `gnome-topbar/daemon/internal/state/store_test.go`, `state_test.go`, `gnome-topbar/daemon/internal/tray/revisit_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `Retain("revisit:", live)` removes seen `revisit:` ids not in `live`, keeps every non-prefixed id and every live one, persists atomically, and does not write when nothing changes.
- [ ] `ComputeRevisit(sigs, store, now)` drops acked signals (`revisit:<id>` seen) and `new_model` signals whose `Since` is more than 14 days before `now`.
- [ ] `refreshRuns` calls `Retain` outside `p.mu` only when `RevisitKnown`, then stores `runsLocal` and calls `recompute()` under `p.mu`. `recompute` sets `snap.Revisit`, `snap.RevisitGated` (count) and `snap.RevisitKnown`.
- [ ] `revisitTitle(n, g)`:
  - `(2,0)` → `🔁 Cycle review due (2)`;
  - `(2,4)` → `🔁 Cycle review due (2) · 4 too small to judge`;
  - `(0,4)` → `🔁 Cycle review: none due · 4 too small to judge`;
  - `(0,0)` → `""` (hidden).
- [ ] `revisitLabel` renders drift as `<kind> ↑|↓ <cohort.kind> · <impl> / <review> — <recent%> vs <baseline%>` (`· <source>` appended for unconfirmed), and new model as `new model: <model>`.
- [ ] Clicking a pool item calls `Actions.AckRevisit(id)`, which acks via `p.Ack([]string{"revisit:"+id})`. The menu is hidden when `RevisitKnown` is false. `Open runs page` calls `Actions.OpenRuns`. The tooltip count adds the unacked signal count, not the gated count. No desktop notification is raised for signals.
- [ ] A failed `Retain` logs one warning: `p.log.Warn("revisit ack retain failed", "err", err)`.

**Verify:** `cd gnome-topbar/daemon && go test -race ./...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests**
  - `store_test.go`:
    - `TestRetainForgetsOnlyClearedPrefixed`: seed `["pr:a#1","revisit:x","revisit:y"]`, `Retain("revisit:", []string{"x"})`, then reload from disk → `pr:a#1` and `revisit:x` seen, `revisit:y` new.
    - `TestRetainNoChangeNoWrite`: record the file's mtime, call `Retain` with every id live, assert the mtime is unchanged.
  - `state_test.go` `TestComputeRevisitFiltersAckedAndStaleNewModel`: three signals (`a` drift, `b` acked, `new_model:c` with `Since` 20 days ago) → only `a`.
  - `tray/revisit_test.go`: table tests for `revisitTitle` and `revisitLabel` (a drift with `Recent{Value:.41}`, `Baseline{Value:.18}` → contains `41% vs 18%`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd gnome-topbar/daemon && go test ./internal/state/ ./internal/tray/ -run 'Retain|ComputeRevisit|revisit|Revisit'`
Expected: FAIL

- [ ] **Step 3: Implement**
  - `store.go`:

```go
// Retain forgets every seen id with prefix that is not prefix+id for an id in
// live, so an ack lasts only while its signal does. The caller must only pass
// a live set it actually read: an empty set forgets every prefixed ack.
func (s *Store) Retain(prefix string, live []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := make(map[string]bool, len(live))
	for _, id := range live {
		keep[prefix+id] = true
	}
	next := make(map[string]bool, len(s.seen))
	for id := range s.seen {
		if strings.HasPrefix(id, prefix) && !keep[id] {
			continue
		}
		next[id] = true
	}
	if len(next) == len(s.seen) {
		return nil
	}
	if err := s.persistSet(next); err != nil {
		return err
	}
	s.seen = next
	return nil
}
```

  - `state.go`: `Snapshot` gains:

```go
	Revisit      []scorecard.Signal `json:"revisit"`
	RevisitGated int                `json:"revisit_gated"`
	RevisitKnown bool               `json:"revisit_known"`
```

    and:

```go
// newModelShown bounds how long a new_model signal shows, because a signal
// only clears when the server recomputes and an idle server never does.
const newModelShown = 14 * 24 * time.Hour

// ComputeRevisit returns the signals not yet acked.
func ComputeRevisit(sigs []scorecard.Signal, store *Store, now time.Time) []scorecard.Signal {
	var out []scorecard.Signal
	for _, s := range sigs {
		if !store.IsNew("revisit:" + s.ID) {
			continue
		}
		if s.Kind == scorecard.RevisitNewModel && s.Since != nil && now.Sub(*s.Since) > newModelShown {
			continue
		}
		out = append(out, s)
	}
	return out
}
```

  - `tray/revisit.go`:

```go
package tray

import (
	"fmt"
	"math"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func revisitTitle(n, gated int) string {
	switch {
	case n == 0 && gated == 0:
		return ""
	case n == 0:
		return fmt.Sprintf("🔁 Cycle review: none due · %d too small to judge", gated)
	case gated == 0:
		return fmt.Sprintf("🔁 Cycle review due (%d)", n)
	default:
		return fmt.Sprintf("🔁 Cycle review due (%d) · %d too small to judge", n, gated)
	}
}

func revisitLabel(s scorecard.Signal) string {
	if s.Kind == scorecard.RevisitNewModel || s.Cohort == nil {
		return "new model: " + s.Model
	}
	arrow := "↑"
	if s.Direction == "down" {
		arrow = "↓"
	}
	l := fmt.Sprintf("%s %s %s · %s / %s", s.Kind, arrow, s.Cohort.Kind, s.Cohort.ImplementerModel, s.Cohort.ReviewModel)
	if s.Source != "" {
		l += " · " + s.Source
	}
	if s.Recent != nil && s.Baseline != nil {
		l += fmt.Sprintf(" — %d%% vs %d%%", pct(s.Recent.Value), pct(s.Baseline.Value))
	}
	return l
}

func pct(v float64) int { return int(math.Round(v * 100)) }
```

  - `tray.go`:
    - `Actions` gains `AckRevisit func(id string)` and `OpenRuns func()`.
    - Add `capRevisit = 10`.
    - `Tray` gains `revisitParent *systray.MenuItem`, `revisitPool []*systray.MenuItem`, `revisitIDs []string` and `revisitRunsItem *systray.MenuItem`.
    - In `onReady`, after the stats block:

```go
	t.revisitParent = systray.AddMenuItem("🔁 Cycle review", "anti-tangent revisit signals")
	t.revisitParent.Hide()
	t.revisitPool, t.revisitIDs = t.makeAckPool(capRevisit, t.revisitParent)
	t.revisitRunsItem = t.revisitParent.AddSubMenuItem("Open runs page", "open /ui/runs")
	bindClick(t.revisitRunsItem, t.act.OpenRuns)
```

    - `makeAckPool` mirrors `makeDonePool` exactly, but reads `ids[idx]` and calls `t.act.AckRevisit(id)` and then `t.render()`.
    - In `render`, under `t.mu`, after the stats block:

```go
	title := revisitTitle(len(snap.Revisit), snap.RevisitGated)
	if snap.RevisitKnown && title != "" {
		t.revisitParent.SetTitle(title)
		for i, mi := range t.revisitPool {
			if i < len(snap.Revisit) {
				t.revisitIDs[i] = snap.Revisit[i].ID
				mi.SetTitle(revisitLabel(snap.Revisit[i]))
				mi.Show()
			} else {
				t.revisitIDs[i] = ""
				mi.Hide()
			}
		}
		t.revisitParent.Show()
	} else {
		t.revisitParent.Hide()
	}
```

    - The tooltip count becomes `len(snap.PRs.ReviewRequested) + len(snap.Todos.Due) + len(snap.Revisit)`.
  - `main.go`:
    - `refreshRuns`, between the read and the lock:

```go
	if d.RevisitKnown {
		live := make([]string, len(d.Revisit))
		for i, s := range d.Revisit {
			live[i] = s.ID
		}
		if err := p.store.Retain("revisit:", live); err != nil {
			p.log.Warn("revisit ack retain failed", "err", err)
		}
	}
```

      and inside the lock, after `p.runsLocal = d`, call `p.recompute()`.
    - `recompute`:

```go
	p.snap.RevisitKnown = p.runsLocal.RevisitKnown
	p.snap.RevisitGated = len(p.runsLocal.RevisitGated)
	p.snap.Revisit = state.ComputeRevisit(p.runsLocal.Revisit, p.store, time.Now())
```

    - `actions` gains `AckRevisit: func(id string) { p.Ack([]string{"revisit:" + id}) }` and `OpenRuns: func() { openLocal("/ui/runs") }`.

- [ ] **Step 4: Run the tests**

Run: `cd gnome-topbar/daemon && go test -race ./... && go vet ./...`
Expected: PASS

- [ ] **Step 5: CHANGELOG.** Under `### Added`:

```markdown
- gnome-topbar: a `🔁 Cycle review due (n)` tray menu lists the server's revisit signals; clicking one acks it until the signal clears, and the title adds `· g too small to judge` when comparisons were too small to decide. A server older than 0.32.0, or a scorecard written after a failed read, shows no menu and keeps existing acks.
```

- [ ] **Step 6: Commit**

```bash
git add gnome-topbar/daemon CHANGELOG.md
git commit -m "feat(gnome-topbar): cycle-review tray menu with ack-until-clears"
```

```json:metadata
{"files": ["gnome-topbar/daemon/internal/state/store.go", "gnome-topbar/daemon/internal/state/state.go", "gnome-topbar/daemon/internal/tray/revisit.go", "gnome-topbar/daemon/internal/tray/tray.go", "gnome-topbar/daemon/cmd/gnome-topbar-daemon/main.go", "CHANGELOG.md"], "verifyCommand": "cd gnome-topbar/daemon && go test -race ./... && go vet ./...", "acceptanceCriteria": ["Retain forgets only cleared prefixed ids, atomic, no-op without change", "ComputeRevisit drops acked and >14d new_model", "refreshRuns: Retain outside lock only when known; recompute under lock sets Revisit/Gated/Known", "revisitTitle four cases", "revisitLabel drift and new model", "click acks revisit:<id>; hidden when unknown; Open runs page; tooltip adds n only; no notification", "Retain failure: one warning"], "modelTier": "standard"}
```

---

### Task 7: Docs, protocol resync, spec example fix

**Goal:** The cycle protocol, both READMEs and the spec describe the shipped behaviour, and the protocol bundle is in sync.

**Files:**
- Modify: `docs/protocol/cycle.md` (§7.4 Revisit)
- Sync: `plugin/anti-tangent-protocol/protocol/*.md`
- Modify: `README.md` (stats section), `gnome-topbar/README.md` (tray and runs page)
- Modify: `docs/superpowers/specs/2026-11-24-revisit-signals-and-session-titles-design.md` §6.1 (JSON example: `written_at` at the top level, not inside `row`)

**Acceptance Criteria:**
- [ ] `cycle.md` §7.4 has one sentence naming `scorecard.json`'s `revisit` array, the summary's `Cycle review due:` line and the gnome-topbar `🔁 Cycle review` menu as where a revisit trigger shows, and saying that a "too small to judge" count is not an all-clear. The part stays under 16,000 bytes.
- [ ] `diff -r docs/protocol plugin/anti-tangent-protocol/protocol` is empty.
- [ ] `README.md` documents `revisit`, `revisit_coverage`, `first-seen.json`, the 14-day windows, the gate (`ANTI_TANGENT_SCORECARD_MIN_RUNS` tasks, ≥ 2 runs), that retention ≤ 14 days disables drift signals, that the lead appears only when a summary is written, and session titles needing `ANTI_TANGENT_PLAN_LEDGER=1`.
- [ ] `gnome-topbar/README.md` documents the tray menu, ack-until-clears, the gated count, and session titles on `/ui/runs`.
- [ ] The spec §6.1 example shows `"written_at"` at the top level.

**Verify:** `diff -r docs/protocol plugin/anti-tangent-protocol/protocol && wc -c docs/protocol/cycle.md && go test -race ./...` → no diff output, size < 16000, `ok`

**Steps:**

- [ ] **Step 1:** Find §7.4 with `grep -n '7.4' docs/protocol/cycle.md` and append this sentence to it:

```markdown
A revisit trigger shows in three places: `scorecard.json`'s `revisit` array, a `Cycle review due:` first line in the stats summary, and gnome-topbar's `🔁 Cycle review` menu. A "too small to judge" count there means the cohort lacks the data to decide, not that nothing changed.
```

- [ ] **Step 2:** Resync the bundle:

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
```

- [ ] **Step 3:** Add the README paragraphs. Find the stats section with `grep -n 'scorecard.json' README.md`, and the runs page section with `grep -n '/ui/runs' gnome-topbar/README.md`. Write them in the existing style, plain prose, no change history.

- [ ] **Step 4:** Fix the spec §6.1 JSON example to:

```json
{"plan_run_id":"","session_run_hash":"r_…","row":{"index":1,"task_title":"…"},"written_at":"…"}
```

- [ ] **Step 5:** Run the Verify command, then commit:

```bash
git add docs/protocol/cycle.md plugin/anti-tangent-protocol/protocol README.md gnome-topbar/README.md docs/superpowers/specs/2026-11-24-revisit-signals-and-session-titles-design.md
git commit -m "docs: revisit signals, cycle review menu and session titles"
```

```json:metadata
{"files": ["docs/protocol/cycle.md", "plugin/anti-tangent-protocol/protocol/cycle.md", "README.md", "gnome-topbar/README.md", "docs/superpowers/specs/2026-11-24-revisit-signals-and-session-titles-design.md"], "verifyCommand": "diff -r docs/protocol plugin/anti-tangent-protocol/protocol && wc -c docs/protocol/cycle.md && go test -race ./...", "acceptanceCriteria": ["cycle.md 7.4 sentence names three channels and too-small meaning; <16000 bytes", "bundle identical", "README documents revisit/coverage/first-seen/windows/gate/retention/lead/session titles", "gnome-topbar README documents tray menu, ack, gated count, titles", "spec 6.1 example has top-level written_at"], "modelTier": "mechanical"}
```

---

## Dependencies

Task 2 needs Task 1. Task 3 needs Task 1. Task 5 needs Task 1. Task 6 needs Task 5. Task 7 needs Tasks 1–6. Task 4 is independent.
