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
