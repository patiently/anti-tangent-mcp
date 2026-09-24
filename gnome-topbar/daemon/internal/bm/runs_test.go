package bm

import (
	"context"
	"testing"
)

func TestWriteRunNoteCall(t *testing.T) {
	fc := &fakeCaller{}
	c := New(fc, "main")
	err := c.WriteRunNote(context.Background(), "team", "anti-tangent/runs", "r_abc123", "note body")
	if err != nil {
		t.Fatal(err)
	}
	if fc.last.name != "write_note" {
		t.Fatalf("tool = %q", fc.last.name)
	}
	want := map[string]any{
		"title":     "r_abc123",
		"directory": "anti-tangent/runs",
		"content":   "note body",
		"note_type": "at_run",
		"project":   "team",
		"overwrite": true,
	}
	for k, v := range want {
		if fc.last.args[k] != v {
			t.Errorf("args[%q] = %v, want %v", k, fc.last.args[k], v)
		}
	}
	if len(fc.last.args) != len(want) {
		t.Errorf("args = %+v, want exactly %+v", fc.last.args, want)
	}
}

func TestListRunNotesQueriesAtRunInProject(t *testing.T) {
	fc := &fakeCaller{ret: `{"results":[{"title":"r_abc123","type":"entity","permalink":"anti-tangent/runs/r_abc123/main","metadata":{"note_type":"at_run"}}]}`}
	c := New(fc, "main")
	res, err := c.ListRunNotes(context.Background(), "team")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Permalink != "anti-tangent/runs/r_abc123/main" {
		t.Fatalf("results = %+v", res)
	}
	if fc.last.name != "search_notes" {
		t.Fatalf("tool = %q", fc.last.name)
	}
	if fc.last.args["project"] != "team" {
		t.Errorf("project = %v, want team", fc.last.args["project"])
	}
	types, _ := fc.last.args["note_types"].([]string)
	if len(types) != 1 || types[0] != "at_run" {
		t.Errorf("note_types = %v", fc.last.args["note_types"])
	}
}

func TestReadRunNoteCall(t *testing.T) {
	fc := &fakeCaller{ret: "note markdown"}
	c := New(fc, "main")
	got, err := c.ReadRunNote(context.Background(), "team", "anti-tangent/runs/r_abc123/main")
	if err != nil {
		t.Fatal(err)
	}
	if got != "note markdown" {
		t.Fatalf("got %q", got)
	}
	if fc.last.name != "read_note" {
		t.Fatalf("tool = %q", fc.last.name)
	}
	if fc.last.args["identifier"] != "anti-tangent/runs/r_abc123/main" || fc.last.args["project"] != "team" {
		t.Fatalf("bad call: %+v", fc.last.args)
	}
}
