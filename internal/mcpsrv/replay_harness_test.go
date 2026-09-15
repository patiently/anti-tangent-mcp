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
// session that call opened, whatever session_id the fixture carries.
type replayFixture struct {
	Name               string                  `json:"name"`
	ValidateTaskSpec   *ValidateTaskSpecArgs   `json:"validate_task_spec,omitempty"`
	ValidateCompletion *ValidateCompletionArgs `json:"validate_completion,omitempty"`
	Expectations       []replayExpectation     `json:"expectations,omitempty"`
}

// replayExpectation names a call and the keywords that identify the issue it
// should raise. A run meets it when any finding on that call contains any
// keyword, ignoring case, in its category, criterion, evidence or suggestion.
type replayExpectation struct {
	Call          string   `json:"call"`
	AnyOfKeywords []string `json:"any_of_keywords"`
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
	fixtures := make([]replayFixture, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var fx replayFixture
		if err := json.Unmarshal(raw, &fx); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if fx.Name == "" {
			fx.Name = strings.TrimSuffix(filepath.Base(p), ".json")
		}
		if err := fx.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		fixtures = append(fixtures, fx)
	}
	return fixtures, nil
}

func (fx replayFixture) validate() error {
	if fx.ValidateTaskSpec == nil && fx.ValidateCompletion == nil {
		return fmt.Errorf("fixture %q records neither validate_task_spec nor validate_completion", fx.Name)
	}
	for i, e := range fx.Expectations {
		switch {
		case e.Call != replayCallTaskSpec && e.Call != replayCallCompletion:
			return fmt.Errorf("expectations[%d].call must be %s or %s, got %q", i, replayCallTaskSpec, replayCallCompletion, e.Call)
		case e.Call == replayCallTaskSpec && fx.ValidateTaskSpec == nil,
			e.Call == replayCallCompletion && fx.ValidateCompletion == nil:
			return fmt.Errorf("expectations[%d] names %s, which fixture %q does not record", i, e.Call, fx.Name)
		case len(e.AnyOfKeywords) == 0:
			return fmt.Errorf("expectations[%d] has no any_of_keywords", i)
		}
	}
	return nil
}

// filterReplayFixtures keeps the fixtures named in only, a comma-separated
// list, in their loaded order. An empty list keeps every fixture; a name no
// fixture has is an error, so a typo cannot silently skip a paid run's target.
func filterReplayFixtures(fixtures []replayFixture, only string) ([]replayFixture, error) {
	want := map[string]bool{}
	for _, name := range strings.Split(only, ",") {
		if name = strings.TrimSpace(name); name != "" {
			want[name] = true
		}
	}
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
		unknown := make([]string, 0, len(want))
		for name := range want {
			unknown = append(unknown, name)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("ANTI_TANGENT_REPLAY_ONLY names fixtures that do not exist: %s", strings.Join(unknown, ", "))
	}
	return kept, nil
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
	Errors   []string       `json:"errors,omitempty"`
	// PromptBytes is the largest prompt the call sent a reviewer in any run.
	PromptBytes int `json:"prompt_bytes"`
}

// replayMeter records the size of the last prompt sent through a reviewer.
type replayMeter struct {
	providers.Reviewer
	lastPromptBytes int
}

func (m *replayMeter) Review(ctx context.Context, req providers.Request) (providers.Response, error) {
	m.lastPromptBytes = len(req.System) + len(req.User)
	return m.Reviewer.Review(ctx, req)
}

// replayDryRunReviewer answers every review with an empty pass, so a dry run
// checks fixtures and measures prompts without a paid call.
type replayDryRunReviewer struct{ name string }

func (d replayDryRunReviewer) Name() string { return d.name }

func (d replayDryRunReviewer) Review(context.Context, providers.Request) (providers.Response, error) {
	return providers.Response{RawJSON: []byte(`{"verdict":"pass","findings":[],"next_action":"dry run"}`), Model: "dry-run"}, nil
}

// runReplayFixture replays fx runs times, one call at a time, each run on
// fresh session and plan-run stores, and reports what the runs raised.
func runReplayFixture(ctx context.Context, cfg config.Config, reviewers providers.Registry, fx replayFixture, runs int) replayReport {
	meters := make([]*replayMeter, 0, len(reviewers))
	registry := providers.Registry{}
	for name, rv := range reviewers {
		m := &replayMeter{Reviewer: rv}
		meters = append(meters, m)
		registry[name] = m
	}
	// lastPromptBytes reads, and resets, the largest prompt sent since the
	// previous read.
	lastPromptBytes := func() int {
		n := 0
		for _, m := range meters {
			n = max(n, m.lastPromptBytes)
			m.lastPromptBytes = 0
		}
		return n
	}

	report := replayReport{Fixture: fx.Name, Runs: runs, Calls: map[string]*replayCallStats{}}
	for _, e := range fx.Expectations {
		report.Expectations = append(report.Expectations, replayTally{replayExpectation: e})
	}
	for run := 1; run <= runs; run++ {
		h := &handlers{deps: Deps{
			Cfg:      cfg,
			Sessions: session.NewStore(cfg.SessionTTL),
			Reviews:  registry,
			PlanRuns: planrun.NewStore(cfg.SessionTTL),
		}}
		findings := map[string][]verdict.Finding{}
		sessionID := ""
		if fx.ValidateTaskSpec != nil {
			_, env, err := h.ValidateTaskSpec(ctx, nil, *fx.ValidateTaskSpec)
			report.record(replayCallTaskSpec, env, err, lastPromptBytes())
			findings[replayCallTaskSpec] = env.Findings
			sessionID = env.SessionID
		}
		if fx.ValidateCompletion != nil {
			if fx.ValidateTaskSpec != nil && sessionID == "" {
				report.record(replayCallCompletion, Envelope{}, errors.New("skipped: validate_task_spec opened no session"), 0)
			} else {
				args := *fx.ValidateCompletion
				if fx.ValidateTaskSpec != nil {
					args.SessionID = sessionID
				}
				_, env, err := h.ValidateCompletion(ctx, nil, args)
				report.record(replayCallCompletion, env, err, lastPromptBytes())
				findings[replayCallCompletion] = env.Findings
			}
		}
		for i := range report.Expectations {
			tally := &report.Expectations[i]
			if f, ok := firstMatchingFinding(findings[tally.Call], tally.AnyOfKeywords); ok {
				tally.Matched++
				tally.Matches = append(tally.Matches, fmt.Sprintf("run %d: %s %s: %s", run, f.Category, f.Criterion, truncate(f.Evidence, 200)))
			}
		}
	}
	return report
}

func (r *replayReport) record(call string, env Envelope, err error, promptBytes int) {
	st := r.Calls[call]
	if st == nil {
		st = &replayCallStats{Verdicts: map[string]int{}, Blocking: map[string]int{}}
		r.Calls[call] = st
	}
	st.PromptBytes = max(st.PromptBytes, promptBytes)
	if err != nil {
		st.Errors = append(st.Errors, err.Error())
		return
	}
	st.Verdicts[env.Verdict]++
	seen := map[string]bool{}
	for _, f := range env.Findings {
		if f.Severity != verdict.SeverityCritical && f.Severity != verdict.SeverityMajor {
			continue
		}
		key := fmt.Sprintf("%s %s %q", f.Severity, f.Category, f.Criterion)
		if !seen[key] {
			seen[key] = true
			st.Blocking[key]++
		}
	}
}

func firstMatchingFinding(findings []verdict.Finding, keywords []string) (verdict.Finding, bool) {
	for _, f := range findings {
		text := strings.ToLower(strings.Join([]string{string(f.Category), f.Criterion, f.Evidence, f.Suggestion}, "\n"))
		for _, k := range keywords {
			if k != "" && strings.Contains(text, strings.ToLower(k)) {
				return f, true
			}
		}
	}
	return verdict.Finding{}, false
}

// String renders the report for a test log.
func (r replayReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "fixture %s (%d runs)\n", r.Fixture, r.Runs)
	for _, e := range r.Expectations {
		fmt.Fprintf(&b, "  %s %q: %d/%d\n", e.Call, e.AnyOfKeywords, e.Matched, r.Runs)
		for _, m := range e.Matches {
			fmt.Fprintf(&b, "    %s\n", m)
		}
	}
	for _, call := range []string{replayCallTaskSpec, replayCallCompletion} {
		st := r.Calls[call]
		if st == nil {
			continue
		}
		fmt.Fprintf(&b, "  %s verdicts %v, largest prompt %d bytes\n", call, st.Verdicts, st.PromptBytes)
		keys := make([]string, 0, len(st.Blocking))
		for k := range st.Blocking {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "    blocking %s in %d/%d\n", k, st.Blocking[k], r.Runs)
		}
		for _, e := range st.Errors {
			fmt.Fprintf(&b, "    error: %s\n", e)
		}
	}
	return b.String()
}
