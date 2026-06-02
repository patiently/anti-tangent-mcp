package bm

import (
	"context"
	"testing"
)

type fakeCaller struct {
	last struct {
		name string
		args map[string]any
	}
	ret string
	err error
}

func (f *fakeCaller) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	f.last.name = name
	f.last.args = args
	return f.ret, f.err
}

func TestReadNotePassesIdentifierAndProject(t *testing.T) {
	fc := &fakeCaller{ret: "note-body"}
	c := New(fc, "main")
	got, err := c.ReadNote(context.Background(), "alice/todo/main")
	if err != nil {
		t.Fatal(err)
	}
	if got != "note-body" {
		t.Fatalf("got %q", got)
	}
	if fc.last.name != "read_note" || fc.last.args["identifier"] != "alice/todo/main" || fc.last.args["project"] != "main" {
		t.Fatalf("bad call: %+v", fc.last)
	}
}
