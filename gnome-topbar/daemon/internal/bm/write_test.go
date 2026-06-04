package bm

import (
	"context"
	"testing"
	"time"
)

func TestAppendTodoCall(t *testing.T) {
	fc := &fakeCaller{}
	c := New(fc, "main")
	if err := c.AppendTodo(context.Background(), "alice", "buy milk"); err != nil {
		t.Fatal(err)
	}
	if fc.last.name != "edit_note" {
		t.Fatalf("tool = %q", fc.last.name)
	}
	if fc.last.args["identifier"] != "alice/todo/main" {
		t.Errorf("identifier = %v", fc.last.args["identifier"])
	}
	if fc.last.args["project"] != "main" {
		t.Errorf("project = %v", fc.last.args["project"])
	}
	if fc.last.args["operation"] != "insert_before_section" || fc.last.args["section"] != "## Done" {
		t.Errorf("op/section = %v / %v", fc.last.args["operation"], fc.last.args["section"])
	}
	if fc.last.args["content"] != "- [ ] buy milk\n" {
		t.Errorf("content = %q", fc.last.args["content"])
	}
}

func TestMarkTodoDoneCall(t *testing.T) {
	fc := &fakeCaller{}
	c := New(fc, "main")
	raw := "- [ ] [2026-06-04] ship the thing"
	today := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	if err := c.MarkTodoDone(context.Background(), "alice", raw, today); err != nil {
		t.Fatal(err)
	}
	if fc.last.args["project"] != "main" {
		t.Errorf("project = %v", fc.last.args["project"])
	}
	if fc.last.args["operation"] != "find_replace" {
		t.Fatalf("op = %v", fc.last.args["operation"])
	}
	if fc.last.args["find_text"] != raw {
		t.Errorf("find_text = %v", fc.last.args["find_text"])
	}
	// BM find_replace puts the replacement in `content`, not `replace_text`.
	if fc.last.args["content"] != "- [x] [2026-06-04] ship the thing — done 2026-06-04" {
		t.Errorf("content = %v", fc.last.args["content"])
	}
	if _, bad := fc.last.args["replace_text"]; bad {
		t.Errorf("must not send replace_text (BM rejects it)")
	}
}
