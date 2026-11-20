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

func TestRunsFallsBackToTaskImplementerModel(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lines := []scorecard.RunLine{
		{Ts: t0, RunHash: "r_sess", Header: true, Kind: scorecard.KindSession, TaskCount: 1},
		{Ts: t0.Add(time.Minute), RunHash: "r_sess", Task: &scorecard.TaskSnapshot{Index: 1, ImplementerModel: "claude-sonnet-4-5"}},
		{Ts: t0, RunHash: "r_plan", Header: true, TaskCount: 2},
		{Ts: t0.Add(time.Minute), RunHash: "r_plan", Task: &scorecard.TaskSnapshot{Index: 1, ImplementerModel: "self-reported"}},
		{Ts: t0.Add(2 * time.Minute), RunHash: "r_plan", Task: &scorecard.TaskSnapshot{Index: 2, ImplementerModel: "haiku"}},
	}
	outs := []scorecard.OutcomeLine{{Ts: t0.Add(time.Hour), RunHash: "r_plan", Source: "final_review",
		ImplementerModels: []scorecard.ImplementerModel{{TaskIndex: 1, Model: "sonnet"}}}}
	got := map[string][]string{}
	for _, r := range Runs(lines, outs) {
		got[r.RunHash] = r.ImplementerModels
	}
	if m := got["r_sess"]; len(m) != 1 || m[0] != "claude-sonnet-4-5" {
		t.Fatalf("session run models: %v", m)
	}
	if m := got["r_plan"]; len(m) != 2 || m[0] != "haiku" || m[1] != "sonnet" {
		t.Fatalf("plan run models: %v", m)
	}
}

func TestRunsResolvesOneModelPerTask(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lines := []scorecard.RunLine{
		{Ts: t0, RunHash: "r", Header: true, TaskCount: 3},
		{Ts: t0.Add(time.Minute), RunHash: "r", Task: &scorecard.TaskSnapshot{Index: 2, ImplementerModel: "claude-sonnet-5-5"}},
		{Ts: t0.Add(time.Minute), RunHash: "r", Task: &scorecard.TaskSnapshot{Index: 3, ImplementerModel: "model-a"}},
		{Ts: t0.Add(2 * time.Minute), RunHash: "r", Task: &scorecard.TaskSnapshot{Index: 3, ImplementerModel: "model-b"}},
	}
	outs := []scorecard.OutcomeLine{{Ts: t0.Add(time.Hour), RunHash: "r", Source: "final_review",
		ImplementerModels: []scorecard.ImplementerModel{{TaskIndex: 1, Model: "anthropic:claude-sonnet-5-5"}}}}
	rs := Runs(lines, outs)
	if len(rs) != 1 {
		t.Fatalf("runs: %+v", rs)
	}
	if m := rs[0].ImplementerModels; len(m) != 2 || m[0] != "claude-sonnet-5-5" || m[1] != "model-b" {
		t.Fatalf("models: %v", m)
	}
}
