package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atruns"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/config"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/server"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestPoller() *Poller {
	return &Poller{log: discardLogger(), snap: state.Snapshot{Sources: map[string]state.SourceStatus{}}}
}

// fakeCaller implements bm.Caller: it records every call and, when errOn
// matches the tool name, returns an error instead of a canned result.
type fakeCaller struct {
	calls []string
	errOn string
}

func (f *fakeCaller) CallTool(_ context.Context, name string, _ map[string]any) (string, error) {
	f.calls = append(f.calls, name)
	if name == f.errOn {
		return "", errors.New("boom")
	}
	if name == "search_notes" {
		return `{"results":[],"has_more":false}`, nil
	}
	return "", nil
}

func TestPollerRunsViewFiltersByPublisher(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lines := []scorecard.RunLine{
		{Ts: t0, RunHash: "r_x", Publisher: "alice", Header: true, TaskCount: 1},
		{Ts: t0, RunHash: "r_x", Publisher: "bob", Header: true, TaskCount: 1},
		{Ts: t0.Add(time.Minute), RunHash: "r_x", Publisher: "alice", Task: &scorecard.TaskSnapshot{Index: 1, PostVerdict: "pass"}},
		{Ts: t0.Add(time.Minute), RunHash: "r_x", Publisher: "bob", Task: &scorecard.TaskSnapshot{Index: 1, PostVerdict: "pass"}},
	}
	outs := []scorecard.OutcomeLine{
		{Ts: t0.Add(2 * time.Minute), RunHash: "r_x", Publisher: "alice", Source: "final_review"},
		{Ts: t0.Add(2 * time.Minute), RunHash: "r_x", Publisher: "bob", Source: "final_review"},
	}
	p := newTestPoller()
	p.runsTeam = atruns.Data{Present: true, Lines: lines, Outcomes: outs}

	v := p.RunsView("user:alice")

	if len(v.Data.Lines) == 0 || len(v.Data.Outcomes) == 0 {
		t.Fatalf("expected alice's records, got lines=%d outcomes=%d", len(v.Data.Lines), len(v.Data.Outcomes))
	}
	assertOnlyPublisher(t, v, "alice")
}

// assertOnlyPublisher fails the test if any line, outcome or run summary in v
// belongs to a publisher other than want.
func assertOnlyPublisher(t *testing.T, v server.RunsView, want string) {
	t.Helper()
	assertLinesPublisher(t, v.Data.Lines, want)
	assertOutcomesPublisher(t, v.Data.Outcomes, want)
	assertRunSummariesPublisher(t, v.Runs, want)
}

func assertLinesPublisher(t *testing.T, lines []scorecard.RunLine, want string) {
	t.Helper()
	for _, l := range lines {
		if l.Publisher != want {
			t.Errorf("Data.Lines leaked publisher %q for hash %q", l.Publisher, l.RunHash)
		}
	}
}

func assertOutcomesPublisher(t *testing.T, outcomes []scorecard.OutcomeLine, want string) {
	t.Helper()
	for _, o := range outcomes {
		if o.Publisher != want {
			t.Errorf("Data.Outcomes leaked publisher %q for hash %q", o.Publisher, o.RunHash)
		}
	}
}

func assertRunSummariesPublisher(t *testing.T, runs []atruns.RunSummary, want string) {
	t.Helper()
	for _, r := range runs {
		if r.Publisher != want {
			t.Errorf("Runs leaked publisher %q for hash %q", r.Publisher, r.RunHash)
		}
	}
}

// testTeamCache wires a TeamCache over caller into p, with a controllable clock.
func testTeamCache(t *testing.T, p *Poller, caller bm.Caller, now *time.Time) {
	t.Helper()
	p.teamCache = &atruns.TeamCache{
		Client: bm.New(caller, "team"), Project: "team",
		Path:     filepath.Join(t.TempDir(), "team-runs.json"),
		Interval: time.Hour, FullEvery: 24 * time.Hour,
		Now: func() time.Time { return *now },
	}
}

func countCalls(f *fakeCaller, name string) int {
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

func TestRefreshRunsTeamKeepsPreviousDataOnError(t *testing.T) {
	prior := atruns.Data{Present: true, Lines: []scorecard.RunLine{{RunHash: "r_prior", Header: true}}}
	p := newTestPoller()
	now := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	testTeamCache(t, p, &fakeCaller{errOn: "search_notes"}, &now)
	p.runsTeam = prior

	p.pullTeam(context.Background(), false)

	if p.runsTeamErr == "" {
		t.Error("runsTeamErr not set on pool failure")
	}
	if st := p.snap.Sources["runs-team"]; st.OK {
		t.Errorf("runs-team source = %+v, want OK=false", st)
	}
	if len(p.runsTeam.Lines) != 1 || p.runsTeam.Lines[0].RunHash != "r_prior" {
		t.Errorf("runsTeam mutated on failure: %+v", p.runsTeam)
	}
}

func TestRefreshRunsTeamSucceeds(t *testing.T) {
	p := newTestPoller()
	now := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	testTeamCache(t, p, &fakeCaller{}, &now)

	p.pullTeam(context.Background(), false)

	if p.runsTeamErr != "" {
		t.Errorf("runsTeamErr = %q, want empty", p.runsTeamErr)
	}
	if st := p.snap.Sources["runs-team"]; !st.OK {
		t.Errorf("runs-team source = %+v, want OK=true", st)
	}
	if !p.runsTeamAsOf.Equal(now) {
		t.Errorf("runsTeamAsOf = %v, want %v", p.runsTeamAsOf, now)
	}
}

func TestRefreshRunsTeamIfDuePullsOnlyWhenDue(t *testing.T) {
	p := newTestPoller()
	fc := &fakeCaller{}
	now := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	testTeamCache(t, p, fc, &now)

	p.refreshRunsTeamIfDue(context.Background())
	if countCalls(fc, "search_notes") != 1 {
		t.Fatalf("an empty cache is due: search_notes calls = %d", countCalls(fc, "search_notes"))
	}
	now = now.Add(30 * time.Minute)
	p.refreshRunsTeamIfDue(context.Background())
	if countCalls(fc, "search_notes") != 1 {
		t.Fatal("a pull 30 minutes after the last one must not reach Basic Memory")
	}
	now = now.Add(30 * time.Minute)
	p.refreshRunsTeamIfDue(context.Background())
	if countCalls(fc, "search_notes") != 2 {
		t.Fatal("a pull an hour after the last one must reach Basic Memory")
	}
}

func TestRefreshTeamRunsForcesAPull(t *testing.T) {
	p := newTestPoller()
	fc := &fakeCaller{}
	now := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	testTeamCache(t, p, fc, &now)
	p.refreshRunsTeamIfDue(context.Background())

	p.RefreshTeamRuns(context.Background())
	if countCalls(fc, "search_notes") != 2 {
		t.Fatalf("Refresh now must pull even when not due: search_notes calls = %d", countCalls(fc, "search_notes"))
	}
}

func TestRefreshTeamRunsWithoutBMIsANoop(t *testing.T) {
	p := newTestPoller()
	p.RefreshTeamRuns(context.Background())
	if p.runsTeamErr != "" {
		t.Errorf("no team cache configured must not report an error: %q", p.runsTeamErr)
	}
}

func TestLoadTeamCacheServesDiskWithoutBM(t *testing.T) {
	p := newTestPoller()
	fc := &fakeCaller{}
	now := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	testTeamCache(t, p, fc, &now)
	p.pullTeam(context.Background(), false)
	path := p.teamCache.Path

	q := newTestPoller()
	fresh := &fakeCaller{}
	testTeamCache(t, q, fresh, &now)
	q.teamCache.Path = path
	q.loadTeamCache()
	if len(fresh.calls) != 0 {
		t.Fatalf("loading the cache called Basic Memory: %v", fresh.calls)
	}
	if !q.runsTeamAsOf.Equal(now) {
		t.Errorf("runsTeamAsOf = %v, want the cached pull time %v", q.runsTeamAsOf, now)
	}
}

func TestRefreshRunsReadsLocalAndPublishes(t *testing.T) {
	dir := t.TempDir()
	hash := scorecard.HashRunID("salt", "pr_1")
	if err := os.WriteFile(filepath.Join(dir, "runs.jsonl"),
		[]byte(`{"ts":"2026-09-01T00:00:00Z","run_hash":"`+hash+`","header":true,"task_count":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outcomes.jsonl"),
		[]byte(`{"ts":"2026-09-01T00:01:00Z","run_hash":"`+hash+`","source":"final_review","findings":[]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fc := &fakeCaller{}
	p := newTestPoller()
	p.cfg = config.Config{StatsDir: dir, ShareProject: "team"}
	p.publisher = &atruns.Publisher{Client: bm.New(fc, "team"), Project: "team", Username: "alice",
		StatePath: filepath.Join(t.TempDir(), "published.json")}

	p.refreshRuns(context.Background())

	if !p.runsLocal.Present || len(p.runsLocal.Lines) != 1 {
		t.Fatalf("runsLocal not populated: %+v", p.runsLocal)
	}
	found := false
	for _, c := range fc.calls {
		if c == "write_note" {
			found = true
		}
	}
	if !found {
		t.Errorf("refreshRuns did not publish via write_note; calls=%v", fc.calls)
	}
}

func TestFilterPublisher(t *testing.T) {
	lines := []scorecard.RunLine{{RunHash: "r_1", Publisher: "alice"}, {RunHash: "r_1", Publisher: "bob"}}
	outs := []scorecard.OutcomeLine{{RunHash: "r_1", Publisher: "alice"}, {RunHash: "r_1", Publisher: "bob"}}
	ls, ocs := filterPublisher(lines, outs, "alice")
	if len(ls) != 1 || ls[0].Publisher != "alice" {
		t.Errorf("lines = %+v", ls)
	}
	if len(ocs) != 1 || ocs[0].Publisher != "alice" {
		t.Errorf("outcomes = %+v", ocs)
	}
}
