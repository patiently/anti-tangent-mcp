package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atruns"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

var t0 = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func sampleToolModelRow(model string) scorecard.ToolModelRow {
	return scorecard.ToolModelRow{
		Tool: "validate_completion", Model: model, Calls: 5, Runs: 2, Tasks: 3,
		VerdictCounts:   map[string]int{"pass": 3, "warn": 1},
		FindingsPerCall: 1.5, MSP50: 100, MSP95: 200, PartialRate: 0.1,
		Outcomes: []scorecard.ToolModelOutcome{
			{Source: "final_review", EscapeRate: scorecard.Rate{Value: 0.5, Lo: 0.25, Hi: 0.75, Num: 2, N: 4}},
		},
	}
}

func TestRenderRunsPageMineOverview(t *testing.T) {
	v := RunsView{
		Scope:   "mine",
		Present: true,
		Scorecard: scorecard.Scorecard{
			MinRuns:     10,
			ByToolModel: []scorecard.ToolModelRow{sampleToolModelRow("sonnet")},
		},
	}
	out := renderRunsPage(v)
	if !strings.Contains(out, "Overview — anti-tangent tool × validator model") {
		t.Errorf("overview heading missing:\n%s", out)
	}
	if !strings.Contains(out, "validate_completion") {
		t.Errorf("validate_completion heading missing:\n%s", out)
	}
	if !strings.Contains(out, "50% (") {
		t.Errorf("rate not formatted as value (lo-hi, n=N):\n%s", out)
	}
}

func TestRenderRunsPageByUserSection(t *testing.T) {
	rowAlice := sampleToolModelRow("sonnet")
	rowAlice.Publisher = "alice"
	rowBob := sampleToolModelRow("opus")
	rowBob.Publisher = "bob"
	v := RunsView{
		Scope:   "team",
		Present: true,
		Scorecard: scorecard.Scorecard{
			MinRuns:     10,
			ByToolModel: []scorecard.ToolModelRow{sampleToolModelRow("sonnet")},
			ByPublisher: &scorecard.PublisherViews{
				ByToolModel: []scorecard.ToolModelRow{rowAlice, rowBob},
			},
		},
	}
	out := renderRunsPage(v)
	if !strings.Contains(out, "By user") {
		t.Errorf("by-user heading missing:\n%s", out)
	}
	if !strings.Contains(out, ">alice<") || !strings.Contains(out, ">bob<") {
		t.Errorf("per-publisher headings missing:\n%s", out)
	}
}

func TestRunsListLinksToDetail(t *testing.T) {
	v := RunsView{
		Scope: "team",
		Runs: []atruns.RunSummary{{
			Publisher: "alice", RunHash: "r_1", Latest: t0, PlanVerdict: "pass", TaskCount: 2,
			ConfiguredModels: map[string]string{"plan": "sonnet"}, ImplementerModels: []string{"sonnet"},
			Sources: []string{"final_review"}, Escapes: map[string]int{"final_review": 1},
		}},
	}
	out := runsList(v)
	if !strings.Contains(out, `href="/ui/runs?scope=team&amp;run=r_1&amp;publisher=alice"`) {
		t.Errorf("run link missing/malformed:\n%s", out)
	}
	if !strings.Contains(out, "<th>Publisher</th>") {
		t.Errorf("Publisher column missing in team scope:\n%s", out)
	}
}

func TestRenderRunsPageTeamAbsentShowsShareHint(t *testing.T) {
	v := RunsView{Scope: "team", Present: false}
	out := renderRunsPage(v)
	if !strings.Contains(out, "ANTI_TANGENT_SHARE_STATS=1") {
		t.Errorf("share hint missing:\n%s", out)
	}
}

func TestRenderRunsPageMineAbsentShowsStatsDirHint(t *testing.T) {
	v := RunsView{Scope: "mine", Present: false}
	out := renderRunsPage(v)
	if !strings.Contains(out, "ANTI_TANGENT_STATS_DIR") {
		t.Errorf("stats dir hint missing:\n%s", out)
	}
}

func detailFixtureData() (string, atruns.Data) {
	hash := "r_test"
	lines := []scorecard.RunLine{
		{Ts: t0, RunHash: hash, Header: true, ConfiguredModels: map[string]string{"plan": "m1"}},
		{Ts: t0.Add(time.Minute), RunHash: hash, Task: &scorecard.TaskSnapshot{Index: 1, PostVerdict: "pass"}},
	}
	titles := map[string]map[int]string{hash: {1: "Task 1: Alpha"}}
	return hash, atruns.Data{Lines: lines, Titles: titles}
}

func TestRenderRunDetailTitleOnlyInMineScope(t *testing.T) {
	hash, data := detailFixtureData()

	mine := renderRunDetail(RunsView{Scope: "mine", Data: data}, hash, "")
	if !strings.Contains(mine, "Task 1: Alpha") {
		t.Errorf("mine scope should show task title:\n%s", mine)
	}

	team := renderRunDetail(RunsView{Scope: "team", Data: data}, hash, "")
	if strings.Contains(team, "Alpha") {
		t.Errorf("team scope must not show a local task title:\n%s", team)
	}
}

func TestRenderRunDetailMarksEscape(t *testing.T) {
	hash := "r_esc"
	lines := []scorecard.RunLine{
		{Ts: t0, RunHash: hash, Header: true},
		{Ts: t0.Add(time.Minute), RunHash: hash, Task: &scorecard.TaskSnapshot{Index: 1, PostVerdict: "pass"}},
	}
	outs := []scorecard.OutcomeLine{
		{Ts: t0.Add(2 * time.Minute), RunHash: hash, Source: "final_review",
			Findings: []scorecard.OutcomeFinding{{TaskIndex: 1, Severity: "critical", Category: "x"}}},
	}
	v := RunsView{Scope: "team", Data: atruns.Data{Lines: lines, Outcomes: outs}}
	out := renderRunDetail(v, hash, "")
	if !strings.Contains(out, "<strong>escape</strong>") {
		t.Errorf("escape marker missing:\n%s", out)
	}
}

func TestRenderRunDetailUnknownHash(t *testing.T) {
	out := renderRunDetail(RunsView{Scope: "mine"}, "r_missing", "")
	if !strings.Contains(out, "No such run in this scope.") {
		t.Errorf("unknown-hash message missing:\n%s", out)
	}
}

// TestModelNameEscaped pins the escaping contract: any model name reaching
// the page — however it got there — must never appear as raw HTML.
func TestModelNameEscaped(t *testing.T) {
	const xss = "<script>x</script>"
	v := RunsView{
		Scope:   "mine",
		Present: true,
		Scorecard: scorecard.Scorecard{
			MinRuns:     10,
			ByToolModel: []scorecard.ToolModelRow{sampleToolModelRow(xss)},
			ModelSets:   []scorecard.ModelSet{{Models: map[string]string{"plan": xss}, Runs: 1}},
		},
		Runs: []atruns.RunSummary{{
			RunHash: "r_1", Latest: t0, ImplementerModels: []string{xss},
			ConfiguredModels: map[string]string{"plan": xss},
		}},
	}
	out := renderRunsPage(v)
	if strings.Contains(out, xss) {
		t.Fatalf("raw <script> leaked into rendered page:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("escaped model name not found:\n%s", out)
	}
}

// runsFake implements server.Provider, recording the scope RunsView was
// called with, for the handler-level test below.
type runsFake struct {
	gotScope  string
	refreshed bool
}

func (f *runsFake) Snapshot() state.Snapshot                                  { return state.Snapshot{} }
func (f *runsFake) Search(context.Context, string) ([]bm.SearchResult, error) { return nil, nil }
func (f *runsFake) Ack([]string)                                              {}
func (f *runsFake) ReadNote(context.Context, string) (string, error)          { return "", nil }
func (f *runsFake) AppendTodo(context.Context, string) error                  { return nil }
func (f *runsFake) ListHowtos(context.Context) ([]bm.SearchResult, error)     { return nil, nil }
func (f *runsFake) ListGotchas(context.Context) ([]bm.SearchResult, error)    { return nil, nil }
func (f *runsFake) ListModules(context.Context) ([]bm.SearchResult, error)    { return nil, nil }
func (f *runsFake) ListFeatures(context.Context) ([]bm.SearchResult, error)   { return nil, nil }
func (f *runsFake) ListDecisions(context.Context) ([]bm.SearchResult, error)  { return nil, nil }
func (f *runsFake) ListMyNotes(context.Context) ([]bm.SearchResult, error)    { return nil, nil }
func (f *runsFake) RefreshTeamRuns(context.Context)                           { f.refreshed = true }
func (f *runsFake) RunsView(scope string) RunsView {
	f.gotScope = scope
	return RunsView{Scope: scope}
}

func TestRunsHandlerCallsRunsViewWithScope(t *testing.T) {
	fp := &runsFake{}
	r := httptest.NewRequest("GET", "/ui/runs?scope=team", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	New(fp, tok).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if fp.gotScope != "team" {
		t.Errorf("RunsView called with scope %q, want team", fp.gotScope)
	}
}
