package tray

import (
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

func sampleSnapshot() state.Snapshot {
	var s state.Snapshot
	s.Sources = map[string]state.SourceStatus{"github": {OK: true}, "basic-memory": {OK: true}}
	s.NowWorking = bm.NowWorking{Body: "Wiring the tray", HasUpdated: true,
		Updated: time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)}
	s.PRs.ReviewRequested = []github.PR{{Repo: "o/r", Number: 7, Title: "Fix", URL: "https://x/7"}}
	s.PRs.Authored = []github.PR{{Repo: "o/r", Number: 8, Title: "Add", URL: "https://x/8"}}
	due := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	s.Todos.Due = []bm.TodoItem{{Text: "ship it", Due: &due, Overdue: true}}
	s.Todos.Active = []bm.TodoItem{{Text: "later"}}
	return s
}

func find(rows []Row, kind RowKind) []Row {
	var out []Row
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func TestBuildMenuPRsAndTodos(t *testing.T) {
	rows := BuildMenu(sampleSnapshot(), time.Date(2026, 6, 2, 8, 5, 0, 0, time.UTC))
	prs := find(rows, RowPR)
	if len(prs) != 2 {
		t.Fatalf("want 2 PR rows, got %d", len(prs))
	}
	if prs[0].URL != "https://x/7" {
		t.Fatalf("review-requested PR should be first with its URL: %+v", prs[0])
	}
	// a PR row is clickable (has URL, not disabled); a todo row is disabled.
	todos := find(rows, RowTodo)
	if len(todos) != 2 {
		t.Fatalf("want 2 todo rows, got %d", len(todos))
	}
	for _, td := range todos {
		if !td.Disabled {
			t.Fatal("todo rows must be disabled")
		}
	}
}

func TestBuildMenuNowWorkingNotSet(t *testing.T) {
	s := sampleSnapshot()
	s.NowWorking = bm.NowWorking{NotFound: true}
	rows := BuildMenu(s, time.Now())
	h := find(rows, RowHeader)
	found := false
	for _, r := range h {
		if r.Label == "🛠 Currently working on — (not set up)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected not-set header, headers=%v", h)
	}
}

func TestBuildMenuStatsOmittedWhenAbsent(t *testing.T) {
	s := sampleSnapshot()
	s.AntiTangent = atstats.Stats{Present: false}
	rows := BuildMenu(s, time.Now())
	for _, r := range rows {
		if r.Kind == RowStat {
			t.Fatalf("stats must be omitted when absent: %+v", r)
		}
	}
}

func TestBuildMenuStatsShownWhenPresent(t *testing.T) {
	s := sampleSnapshot()
	s.AntiTangent = atstats.Stats{Present: true, TotalCalls: 10, PassPct: 70, ReviewMSP95: 1800, TopCategory: "ambiguous_spec"}
	rows := BuildMenu(s, time.Now())
	if len(find(rows, RowStat)) == 0 {
		t.Fatal("expected a stat row when present")
	}
}

func TestBuildMenuAlwaysHasRefreshAndQuit(t *testing.T) {
	rows := BuildMenu(sampleSnapshot(), time.Now())
	acts := find(rows, RowAction)
	if len(acts) != 2 || acts[0].Label != "↻ Refresh" || acts[1].Label != "Quit" {
		t.Fatalf("want Refresh+Quit actions, got %+v", acts)
	}
}
