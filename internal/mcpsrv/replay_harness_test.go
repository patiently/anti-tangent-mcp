package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// replayFixture is one recorded task for TestReplay_E2E: the
// validate_task_spec call that opens its session, the validate_completion call
// replayed on that session, and the issues a run is expected to raise. Both
// calls use the tools' own argument names. Unknown arguments are ignored, so a
// fixture written for a newer server still runs against an older one, which
// simply does not see the arguments it lacks.
//
//	{
//	  "name": "stale-comments",
//	  "validate_task_spec": {"task_title": "…", "goal": "…", "acceptance_criteria": ["…"]},
//	  "validate_completion": {"summary": "…", "final_diff_path": "/abs/final.diff", "repo_root": "/abs/checkout"},
//	  "expectations": [{"call": "validate_completion", "any_of_keywords": ["legacySweep"]}]
//	}
//
// When the fixture records validate_task_spec, validate_completion runs on the
// session that call opened, whatever session_id the fixture carries. A
// fixture that records only validate_completion runs lightweight instead: any
// session_id it carries (for example one copied from a recorded transcript)
// is cleared first, since a session id no store opened would otherwise reject
// the call as session_not_found on every run.
type replayFixture struct {
	Name               string                  `json:"name"`
	ValidateTaskSpec   *ValidateTaskSpecArgs   `json:"validate_task_spec,omitempty"`
	ValidateCompletion *ValidateCompletionArgs `json:"validate_completion,omitempty"`
	Expectations       []replayExpectation     `json:"expectations,omitempty"`
}

// replayExpectation names a call and the keywords that identify the issue it
// should raise. A run meets it when any finding the REVIEWER itself raised on
// that call contains any keyword, ignoring case, in its category, criterion,
// evidence or suggestion, or, with Absent, when none is — a server advisory
// prepended to the envelope (for example an unusable repo_root) never counts,
// even when its text happens to share a keyword.
type replayExpectation struct {
	Call          string   `json:"call"`
	AnyOfKeywords []string `json:"any_of_keywords"`
	// Criterion, when non-empty, restricts a match to findings whose own
	// Criterion equals it (case-insensitive, trimmed): a keyword an
	// expectation looks for can also appear in an unrelated finding — one
	// from a different check entirely — and that finding must not stand in
	// for the one this expectation is actually measuring.
	Criterion string `json:"criterion,omitempty"`
	// Absent inverts the expectation: a run meets it when the call completed
	// and NO reviewer finding matches. A call that errored, was skipped or
	// came back partial proves nothing absent, so it never meets one.
	Absent bool `json:"absent,omitempty"`
}

const (
	replayCallTaskSpec   = "validate_task_spec"
	replayCallCompletion = "validate_completion"
)

// loadReplayFixtures reads every *.json file in dir, in file-name order. A
// fixture without a name takes its file name.
func loadReplayFixtures(dir string) ([]replayFixture, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	base, err := replayFixtureBase(dir)
	if err != nil {
		return nil, err
	}
	loader := replayFixtureLoader{base: base, named: make(map[string]string, len(paths))}
	fixtures := make([]replayFixture, 0, len(paths))
	for _, p := range paths {
		fx, err := loader.load(p)
		if err != nil {
			return nil, err
		}
		fixtures = append(fixtures, fx)
	}
	return fixtures, nil
}

// replayFixtureLoader reads and validates fixture files sharing one base
// directory, for resolving their relative paths, and one named set.
type replayFixtureLoader struct {
	base string
	// named maps each fixture name already claimed to the file that claimed
	// it. Names address fixtures in ANTI_TANGENT_REPLAY_ONLY, which matches
	// each name once, so two fixtures sharing one name would silently run
	// once.
	named map[string]string
}

// load reads and validates the fixture at p, resolving its relative paths
// against l.base and rejecting a fixture name a prior file already claimed.
func (l *replayFixtureLoader) load(p string) (replayFixture, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return replayFixture{}, err
	}
	var fx replayFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		return replayFixture{}, fmt.Errorf("%s: %w", p, err)
	}
	fx.resolveRelativePaths(l.base)
	if fx.Name == "" {
		fx.Name = strings.TrimSuffix(filepath.Base(p), ".json")
	}
	if first, dup := l.named[fx.Name]; dup {
		return replayFixture{}, fmt.Errorf("%s: fixture name %q is already used by %s", p, fx.Name, first)
	}
	l.named[fx.Name] = p
	if err := fx.validate(); err != nil {
		return replayFixture{}, fmt.Errorf("%s: %w", p, err)
	}
	return fx, nil
}

// replayFixtureBase is the absolute, symlink-resolved fixture directory that
// relative paths in a fixture join onto. Resolved, so the joined paths pass a
// PlanRoots check that compares symlink-resolved paths.
func replayFixtureBase(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// resolveRelativePaths joins each relative file path the fixture names onto
// base. The handlers require absolute paths, and a committed fixture cannot
// know the absolute path of the checkout it runs in.
func (fx *replayFixture) resolveRelativePaths(base string) {
	if ts := fx.ValidateTaskSpec; ts != nil {
		ts.ContextPaths = joinRelative(base, ts.ContextPaths)
	}
	vc := fx.ValidateCompletion
	if vc == nil {
		return
	}
	vc.ContextPaths = joinRelative(base, vc.ContextPaths)
	if vc.FinalDiffPath != "" && !filepath.IsAbs(vc.FinalDiffPath) {
		vc.FinalDiffPath = filepath.Join(base, vc.FinalDiffPath)
	}
}

// joinRelative joins base onto each relative, non-empty path in paths.
// len(paths) == 0 returns paths unchanged rather than a fresh empty slice, so
// a fixture with no context_paths keeps its nil ContextPaths.
func joinRelative(base string, paths []string) []string {
	if len(paths) == 0 {
		return paths
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		if p != "" && !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		out[i] = p
	}
	return out
}

// replayConfigCovering lets the handlers read the files a fixture in dir
// names. With PlanRoots empty every absolute path is already readable, so the
// config is left as the operator set it.
func replayConfigCovering(cfg config.Config, dir string) config.Config {
	if len(cfg.PlanRoots) == 0 {
		return cfg
	}
	base, err := replayFixtureBase(dir)
	if err != nil {
		return cfg
	}
	cfg.PlanRoots = append(append([]string(nil), cfg.PlanRoots...), base)
	return cfg
}

func (fx replayFixture) validate() error {
	if fx.ValidateTaskSpec == nil && fx.ValidateCompletion == nil {
		return fmt.Errorf("fixture %q records neither validate_task_spec nor validate_completion", fx.Name)
	}
	for i, e := range fx.Expectations {
		if err := fx.validateExpectation(i, e); err != nil {
			return err
		}
	}
	return nil
}

// validateExpectation checks that expectations[i] names a call this fixture
// records, that the call is one of the two replay supports, and that it
// carries at least one keyword to match findings against.
func (fx replayFixture) validateExpectation(i int, e replayExpectation) error {
	switch {
	case e.Call != replayCallTaskSpec && e.Call != replayCallCompletion:
		return fmt.Errorf("expectations[%d].call must be %s or %s, got %q", i, replayCallTaskSpec, replayCallCompletion, e.Call)
	case e.Call == replayCallTaskSpec && fx.ValidateTaskSpec == nil,
		e.Call == replayCallCompletion && fx.ValidateCompletion == nil:
		return fmt.Errorf("expectations[%d] names %s, which fixture %q does not record", i, e.Call, fx.Name)
	case len(e.AnyOfKeywords) == 0:
		return fmt.Errorf("expectations[%d] has no any_of_keywords", i)
	}
	// A blank keyword matches nothing, so an expectation carrying only blanks
	// would report 0 of N runs and read as a change that did not help.
	for _, keyword := range e.AnyOfKeywords {
		if strings.TrimSpace(keyword) != "" {
			return nil
		}
	}
	return fmt.Errorf("expectations[%d] has no any_of_keywords that are not blank", i)
}

// filterReplayFixtures keeps the fixtures named in only, a comma-separated
// list, in their loaded order. An empty list keeps every fixture; a name no
// fixture has is an error, so a typo cannot silently skip a paid run's target.
func filterReplayFixtures(fixtures []replayFixture, only string) ([]replayFixture, error) {
	want := parseReplayOnly(only)
	if len(want) == 0 {
		return fixtures, nil
	}
	var kept []replayFixture
	for _, fx := range fixtures {
		if want[fx.Name] {
			kept = append(kept, fx)
			delete(want, fx.Name)
		}
	}
	if len(want) > 0 {
		return nil, unknownReplayNamesError(want)
	}
	return kept, nil
}

// parseReplayOnly splits only into the set of names it requests, trimming
// space and dropping blanks. An empty or all-blank only requests every fixture.
func parseReplayOnly(only string) map[string]bool {
	want := map[string]bool{}
	for _, name := range strings.Split(only, ",") {
		if name = strings.TrimSpace(name); name != "" {
			want[name] = true
		}
	}
	return want
}

// unknownReplayNamesError reports the names in want that no fixture matched,
// sorted for a stable message.
func unknownReplayNamesError(want map[string]bool) error {
	unknown := make([]string, 0, len(want))
	for name := range want {
		unknown = append(unknown, name)
	}
	sort.Strings(unknown)
	return fmt.Errorf("ANTI_TANGENT_REPLAY_ONLY names fixtures that do not exist: %s", strings.Join(unknown, ", "))
}

// replayReport is what one fixture's runs raised.
type replayReport struct {
	Fixture      string                      `json:"fixture"`
	Runs         int                         `json:"runs"`
	Expectations []replayTally               `json:"expectations,omitempty"`
	Calls        map[string]*replayCallStats `json:"calls"`
}

// replayTally counts the runs that met one expectation, and quotes the finding
// that met it on each, so a keyword that matched the wrong issue can be seen.
type replayTally struct {
	replayExpectation
	Matched int      `json:"matched"`
	Matches []string `json:"matches,omitempty"`
}

// replayCallStats describes one call across a fixture's runs.
type replayCallStats struct {
	Verdicts map[string]int `json:"verdicts"`
	// Blocking counts the runs that raised each critical or major finding,
	// keyed by severity, category and criterion, so two reports can be
	// compared for a blocking finding only one of them raised.
	Blocking map[string]int `json:"blocking,omitempty"`
	// Advisories counts the runs that raised each minor server advisory
	// (category "other", such as an unusable repo_root), keyed by
	// criterion, so a degraded run — one where the replay environment
	// could not give the reviewer everything a real checkout would —
	// stays visible instead of silently lowering recall.
	Advisories map[string]int `json:"advisories,omitempty"`
	Errors     []string       `json:"errors,omitempty"`
	// PromptBytes is the largest prompt the call sent a reviewer in any run.
	PromptBytes int `json:"prompt_bytes"`
}

// replayMeter records the size of the last prompt sent through a reviewer,
// and the raw bytes of its last response, so replay can tell the reviewer's
// own findings apart from the server advisories the envelope adds around
// them.
type replayMeter struct {
	providers.Reviewer
	lastPromptBytes int
	lastRawJSON     []byte
}

func (m *replayMeter) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	m.lastPromptBytes = len(req.System) + len(req.User)
	resp, err := m.Reviewer.Review(ctx, req)
	// RawJSON carries the partial body even on ErrResponseTruncated, so a
	// truncated call still yields whatever complete findings the reviewer
	// emitted before the cut; any other error leaves resp (and RawJSON) zero.
	m.lastRawJSON = resp.RawJSON
	return resp, err
}

// replayMeters wraps a providers.Registry so every reviewer's calls record
// their prompt size and response, and reports the largest prompt, and the
// findings from the last response, seen since the last read.
type replayMeters struct {
	list []*replayMeter
}

// wrap returns reviewers wrapped in meters, and remembers them for largest.
func (m *replayMeters) wrap(reviewers providers.Registry) providers.Registry {
	registry := providers.Registry{}
	for name, rv := range reviewers {
		meter := &replayMeter{Reviewer: rv}
		m.list = append(m.list, meter)
		registry[name] = meter
	}
	return registry
}

// largest reads, and resets, the largest prompt sent since the previous read.
func (m *replayMeters) largest() int {
	n := 0
	for _, meter := range m.list {
		n = max(n, meter.lastPromptBytes)
		meter.lastPromptBytes = 0
	}
	return n
}

// lastFindings reads, and resets, the reviewer findings from the last
// response recorded since the previous read. It parses the raw bytes with
// verdict.ParseResultPartial rather than trusting the caller's envelope, so
// a keyword only present in a server-added advisory (never in anything the
// reviewer itself said) cannot count as a match.
func (m *replayMeters) lastFindings() []verdict.Finding {
	var raw []byte
	for _, meter := range m.list {
		if len(meter.lastRawJSON) > 0 {
			raw = meter.lastRawJSON
		}
		meter.lastRawJSON = nil
	}
	if raw == nil {
		return nil
	}
	result, ok := verdict.ParseResultPartial(raw)
	if !ok {
		return nil
	}
	return result.Findings
}

// replayDryRunReviewer answers every review with an empty pass, so a dry run
// checks fixtures and measures prompts without a paid call.
type replayDryRunReviewer struct{ name string }

func (d replayDryRunReviewer) Name() string { return d.name }

func (d replayDryRunReviewer) Review(context.Context, providers.Request) (providers.Response, error) {
	return providers.Response{RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"dry run"}`), Model: "dry-run"}, nil
}

// replayEnv is what every run of every fixture shares: the server config and
// a reviewer registry wrapped in meters that measure each call's prompt.
type replayEnv struct {
	cfg      config.Config
	registry providers.Registry
	meters   *replayMeters
}

func newReplayEnv(cfg config.Config, reviewers providers.Registry) replayEnv {
	meters := &replayMeters{}
	return replayEnv{cfg: cfg, registry: meters.wrap(reviewers), meters: meters}
}

// runReplayFixture replays fx runs times, one call at a time, each run on
// fresh session and plan-run stores, and reports what the runs raised.
func runReplayFixture(ctx context.Context, re replayEnv, fx replayFixture, runs int) replayReport {
	report := replayReport{Fixture: fx.Name, Runs: runs, Calls: map[string]*replayCallStats{}}
	for _, e := range fx.Expectations {
		report.Expectations = append(report.Expectations, replayTally{replayExpectation: e})
	}
	for run := 1; run <= runs; run++ {
		results := replayOneRun(ctx, re, fx, &report)
		tallyExpectations(&report, run, results)
	}
	return report
}

// replayCallResult is one call's outcome in one run: the reviewer's own
// findings, and whether the call completed with a whole response — no
// handler error, not skipped, and not a partial (truncated) envelope.
type replayCallResult struct {
	findings []verdict.Finding
	complete bool
}

// replayOneRun runs fx's calls once, on fresh session and plan-run stores,
// records each call's outcome onto report, and returns each call's result,
// keyed by call name, for the expectation tally — findings are the
// REVIEWER's own, not the envelope's, which also carries the server's own
// advisories.
func replayOneRun(ctx context.Context, re replayEnv, fx replayFixture, report *replayReport) map[string]replayCallResult {
	h := &handlers{deps: Deps{
		Cfg:      re.cfg,
		Sessions: session.NewStore(re.cfg.SessionTTL),
		Reviews:  re.registry,
		PlanRuns: planrun.NewStore(re.cfg.SessionTTL),
	}}
	results := map[string]replayCallResult{}
	sessionID := ""
	if fx.ValidateTaskSpec != nil {
		_, taskEnv, err := h.ValidateTaskSpec(ctx, nil, *fx.ValidateTaskSpec)
		complete := report.record(replayCallTaskSpec, taskEnv, err, re.meters.largest())
		results[replayCallTaskSpec] = replayCallResult{findings: re.meters.lastFindings(), complete: complete}
		sessionID = taskEnv.SessionID
	}
	if fx.ValidateCompletion == nil {
		return results
	}
	if fx.ValidateTaskSpec != nil && sessionID == "" {
		report.record(replayCallCompletion, Envelope{}, errors.New("skipped: validate_task_spec opened no session"), 0)
		return results
	}
	args := *fx.ValidateCompletion
	if fx.ValidateTaskSpec != nil {
		args.SessionID = sessionID
	} else {
		// A completion-only fixture runs lightweight: a session_id it
		// carries names no session this run's fresh store opened.
		args.SessionID = ""
	}
	_, completionEnv, err := h.ValidateCompletion(ctx, nil, args)
	complete := report.record(replayCallCompletion, completionEnv, err, re.meters.largest())
	results[replayCallCompletion] = replayCallResult{findings: re.meters.lastFindings(), complete: complete}
	return results
}

// tallyExpectations records, for run, which of report's expectations the
// results from that run met.
func tallyExpectations(report *replayReport, run int, results map[string]replayCallResult) {
	for i := range report.Expectations {
		tally := &report.Expectations[i]
		res := results[tally.Call]
		f, found := firstMatchingFinding(res.findings, tally.AnyOfKeywords, tally.Criterion)
		switch {
		case tally.Absent && found:
			tally.Matches = append(tally.Matches, fmt.Sprintf("run %d: present: %s", run, describeMatch(f)))
		case tally.Absent:
			if res.complete {
				tally.Matched++
			}
		case found:
			tally.Matched++
			tally.Matches = append(tally.Matches, fmt.Sprintf("run %d: %s", run, describeMatch(f)))
		}
	}
}

// describeMatch quotes a finding for a tally line: category, criterion and
// evidence, and the suggestion when there is one, since a finding addressed to
// the plan author, or one offering a test-side route, says so only there.
func describeMatch(f verdict.Finding) string {
	s := fmt.Sprintf("%s %s: %s", f.Category, f.Criterion, truncate(f.Evidence, 200))
	if f.Suggestion != "" {
		s += " | suggestion: " + truncate(f.Suggestion, 200)
	}
	return s
}

// record tallies call's outcome onto r and reports whether it completed with
// a whole response: no handler error and not a partial (truncated) envelope.
func (r *replayReport) record(call string, env Envelope, err error, promptBytes int) bool {
	st := r.Calls[call]
	if st == nil {
		st = &replayCallStats{Verdicts: map[string]int{}, Blocking: map[string]int{}, Advisories: map[string]int{}}
		r.Calls[call] = st
	}
	st.PromptBytes = max(st.PromptBytes, promptBytes)
	if err != nil {
		st.Errors = append(st.Errors, err.Error())
		return false
	}
	st.Verdicts[env.Verdict]++
	tallyServerFindings(st, env.Findings)
	return !env.Partial
}

// tallyServerFindings counts, at most once per finding kind, each blocking
// (critical or major) finding and each minor category-other advisory in
// findings onto st.
func tallyServerFindings(st *replayCallStats, findings []verdict.Finding) {
	seenBlocking := map[string]bool{}
	seenAdvisory := map[string]bool{}
	for _, f := range findings {
		if isBlockingSeverity(f.Severity) {
			key := fmt.Sprintf("%s %s %q", f.Severity, f.Category, f.Criterion)
			if !seenBlocking[key] {
				seenBlocking[key] = true
				st.Blocking[key]++
			}
			continue
		}
		if isAdvisory(f) && !seenAdvisory[f.Criterion] {
			seenAdvisory[f.Criterion] = true
			st.Advisories[f.Criterion]++
		}
	}
}

func isBlockingSeverity(s verdict.Severity) bool {
	return s == verdict.SeverityCritical || s == verdict.SeverityMajor
}

func isAdvisory(f verdict.Finding) bool {
	return f.Severity == verdict.SeverityMinor && f.Category == verdict.CategoryOther
}

// firstMatchingFinding returns the first finding in findings whose keyword
// text contains any of keywords, ignoring case. When criterion is non-empty,
// a finding must also carry that exact Criterion (trimmed, case-insensitive)
// to be eligible — otherwise an earlier, unrelated finding that happens to
// share a keyword would be recorded instead of the one being measured.
func firstMatchingFinding(findings []verdict.Finding, keywords []string, criterion string) (verdict.Finding, bool) {
	criterion = strings.ToLower(strings.TrimSpace(criterion))
	for _, f := range findings {
		if findingMatches(f, keywords, criterion) {
			return f, true
		}
	}
	return verdict.Finding{}, false
}

// findingMatches reports whether f meets criterion (already trimmed and
// lower-cased by the caller; empty means unrestricted) and contains any
// keyword, ignoring case, in its category, criterion, evidence or suggestion.
func findingMatches(f verdict.Finding, keywords []string, criterion string) bool {
	if criterion != "" && strings.ToLower(strings.TrimSpace(f.Criterion)) != criterion {
		return false
	}
	text := strings.ToLower(strings.Join([]string{string(f.Category), f.Criterion, f.Evidence, f.Suggestion}, "\n"))
	for _, k := range keywords {
		if k != "" && strings.Contains(text, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// String renders the report for a test log.
func (r replayReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "fixture %s (%d runs)\n", r.Fixture, r.Runs)
	for _, e := range r.Expectations {
		if e.Absent {
			fmt.Fprintf(&b, "  %s absent %q: %d/%d\n", e.Call, e.AnyOfKeywords, e.Matched, r.Runs)
		} else {
			fmt.Fprintf(&b, "  %s %q: %d/%d\n", e.Call, e.AnyOfKeywords, e.Matched, r.Runs)
		}
		for _, m := range e.Matches {
			fmt.Fprintf(&b, "    %s\n", m)
		}
	}
	for _, call := range []string{replayCallTaskSpec, replayCallCompletion} {
		if st := r.Calls[call]; st != nil {
			writeCallStats(&b, call, st, r.Runs)
		}
	}
	return b.String()
}

// writeCallStats appends one call's verdicts, blocking findings, advisories
// and errors to b, the blocking and advisory lines each sorted by key for a
// stable render.
func writeCallStats(b *strings.Builder, call string, st *replayCallStats, runs int) {
	fmt.Fprintf(b, "  %s verdicts %v, largest prompt %d bytes\n", call, st.Verdicts, st.PromptBytes)
	for _, k := range sortedKeys(st.Blocking) {
		fmt.Fprintf(b, "    blocking %s in %d/%d\n", k, st.Blocking[k], runs)
	}
	for _, k := range sortedKeys(st.Advisories) {
		fmt.Fprintf(b, "    advisory %s in %d/%d\n", k, st.Advisories[k], runs)
	}
	for _, e := range st.Errors {
		fmt.Fprintf(b, "    error: %s\n", e)
	}
}

// sortedKeys returns m's keys sorted, for a stable render order.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
